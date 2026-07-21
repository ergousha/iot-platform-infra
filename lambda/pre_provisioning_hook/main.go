package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// PreProvisioningEvent represents the input payload from AWS IoT Fleet Provisioning.
type PreProvisioningEvent struct {
	ClaimCertificateID string            `json:"claimCertificateId"`
	CertificateID      string            `json:"certificateId"`
	Parameters         map[string]string `json:"parameters"`
	TemplateARN        string            `json:"templateArn"`
	TemplateName       string            `json:"templateName"`
}

// PreProvisioningResponse represents the output payload expected by AWS IoT Fleet Provisioning.
type PreProvisioningResponse struct {
	AllowProvisioning  bool              `json:"allowProvisioning"`
	ParameterOverrides map[string]string `json:"parameterOverrides,omitempty"`
}

// DeviceItem matches the schema of the DynamoDB Device Registry.
type DeviceItem struct {
	MacAddress  string `dynamodbav:"mac_address"`
	Secret      string `dynamodbav:"secret"`
	Allowed     bool   `dynamodbav:"allowed"`
	Provisioned bool   `dynamodbav:"provisioned"`
}

// DynamoDBAPI defines the DynamoDB operations utilized by this Lambda.
type DynamoDBAPI interface {
	GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
}

// App encapsulates the application context, facilitating unit testing via client interfaces.
type App struct {
	dbClient  DynamoDBAPI
	tableName string
}

// Handler processes the incoming IoT Fleet Provisioning pre-provisioning request.
func (a *App) Handler(ctx context.Context, event PreProvisioningEvent) (PreProvisioningResponse, error) {
	mac := event.Parameters["MacAddress"]
	secret := event.Parameters["Secret"]
	serial := event.Parameters["SerialNumber"]

	slog.Info("Pre-provisioning request received",
		"mac", mac,
		"serial", serial,
		"template", event.TemplateName,
	)

	if mac == "" || secret == "" {
		slog.Warn("REJECTED: MacAddress or Secret parameter missing")
		return PreProvisioningResponse{AllowProvisioning: false}, nil
	}

	resp, err := a.dbClient.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(a.tableName),
		Key: map[string]types.AttributeValue{
			"mac_address": &types.AttributeValueMemberS{Value: mac},
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		slog.Error("DynamoDB get_item error", "error", err)
		return PreProvisioningResponse{AllowProvisioning: false}, nil
	}

	if resp.Item == nil {
		slog.Warn("REJECTED: record does not exist in DynamoDB", "mac", mac)
		return PreProvisioningResponse{AllowProvisioning: false}, nil
	}

	var item DeviceItem
	err = attributevalue.UnmarshalMap(resp.Item, &item)
	if err != nil {
		slog.Error("Failed to unmarshal DynamoDB item", "error", err)
		return PreProvisioningResponse{AllowProvisioning: false}, nil
	}

	if !item.Allowed {
		slog.Warn("REJECTED: allowed=false", "mac", mac)
		return PreProvisioningResponse{AllowProvisioning: false}, nil
	}

	if !constantTimeEq(item.Secret, secret) {
		slog.Warn("REJECTED: secret mismatch", "mac", mac)
		return PreProvisioningResponse{AllowProvisioning: false}, nil
	}

	// Update the record to show it is provisioned (best-effort)
	serialOrMac := serial
	if serialOrMac == "" {
		serialOrMac = mac
	}

	_, err = a.dbClient.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(a.tableName),
		Key: map[string]types.AttributeValue{
			"mac_address": &types.AttributeValueMemberS{Value: mac},
		},
		UpdateExpression: aws.String("SET provisioned = :t, last_provisioned_serial = :s"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":t": &types.AttributeValueMemberBOOL{Value: true},
			":s": &types.AttributeValueMemberS{Value: serialOrMac},
		},
	})
	if err != nil {
		slog.Error("Failed to update provisioned flag (continuing anyway)", "error", err)
	}

	slog.Info("APPROVED: provisioning allowed", "mac", mac)
	return PreProvisioningResponse{
		AllowProvisioning: true,
		ParameterOverrides: map[string]string{
			"MacAddress": mac,
		},
	}, nil
}

// constantTimeEq compares two strings in constant time to prevent timing attacks.
func constantTimeEq(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func main() {
	// Configure logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	tableName := os.Getenv("DEVICE_TABLE")
	if tableName == "" {
		slog.Error("DEVICE_TABLE environment variable is required")
		panic(errors.New("DEVICE_TABLE environment variable is required"))
	}

	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		slog.Error("Unable to load AWS SDK config", "error", err)
		panic(fmt.Sprintf("unable to load SDK config, %v", err))
	}

	app := &App{
		dbClient:  dynamodb.NewFromConfig(cfg),
		tableName: tableName,
	}

	lambda.Start(app.Handler)
}
