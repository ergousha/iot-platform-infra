package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iot"
	"github.com/aws/aws-sdk-go-v2/service/iot/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3PresignAPI defines the S3 presign operations utilized by this Lambda.
type S3PresignAPI interface {
	PresignGetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}

// IoTAPI defines the AWS IoT Core operations utilized by this Lambda.
type IoTAPI interface {
	ListThings(ctx context.Context, params *iot.ListThingsInput, optFns ...func(*iot.Options)) (*iot.ListThingsOutput, error)
	CreateJob(ctx context.Context, params *iot.CreateJobInput, optFns ...func(*iot.Options)) (*iot.CreateJobOutput, error)
}

// App encapsulates the application context, facilitating unit testing via client interfaces.
type App struct {
	s3Presign S3PresignAPI
	iotClient IoTAPI
}

// Response represents the Lambda proxy response structure.
type Response struct {
	StatusCode int    `json:"statusCode"`
	Body       string `json:"body"`
}

// buildJobID normalizes and limits the job ID to 64 characters to comply with AWS requirements.
func buildJobID(version string, timestamp int64) string {
	reg := regexp.MustCompile(`[^A-Za-z0-9_-]+`)
	safeVersion := reg.ReplaceAllString(version, "-")
	safeVersion = strings.Trim(safeVersion, "-_")
	if safeVersion == "" {
		safeVersion = "firmware"
	}
	prefix := "ota-"
	suffix := fmt.Sprintf("-%d", timestamp)
	const maxJobIDLength = 64
	availableLength := maxJobIDLength - len(prefix) - len(suffix)
	if len(safeVersion) > availableLength {
		safeVersion = safeVersion[:availableLength]
	}
	return prefix + safeVersion + suffix
}

// Handler processes the incoming S3 create object event and schedules a rolling OTA update.
func (a *App) Handler(ctx context.Context, event events.S3Event) (Response, error) {
	eventJSON, _ := json.Marshal(event)
	slog.Info("Received S3 event", "event", string(eventJSON))

	if len(event.Records) == 0 {
		slog.Warn("No records found in S3 event")
		return Response{StatusCode: 400, Body: "No records found"}, nil
	}

	bucketName := event.Records[0].S3.Bucket.Name
	objectKey := event.Records[0].S3.Object.URLDecodedKey
	if objectKey == "" {
		objectKey = event.Records[0].S3.Object.Key
	}

	// Generate presigned URL (valid for 24 hours)
	presignedReq, err := a.s3Presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	}, s3.WithPresignExpires(24*time.Hour))
	if err != nil {
		slog.Error("Error generating presigned URL", "error", err)
		return Response{StatusCode: 500, Body: "Error generating URL"}, nil
	}

	slog.Info("Generated presigned URL successfully", "bucket", bucketName, "key", objectKey)

	// Extract version from the object key (e.g. firmware_v0.0.1.bin)
	parts := strings.Split(objectKey, "/")
	filename := parts[len(parts)-1]
	version := strings.ReplaceAll(filename, ".bin", "")

	jobDocument := map[string]interface{}{
		"operation":        "firmware_update",
		"firmware_version": version,
		"download_url":     presignedReq.URL,
	}
	docBytes, err := json.Marshal(jobDocument)
	if err != nil {
		slog.Error("Error marshalling job document", "error", err)
		return Response{StatusCode: 500, Body: "Error marshalling job document"}, nil
	}

	jobID := buildJobID(version, time.Now().Unix())

	listOutput, err := a.iotClient.ListThings(ctx, &iot.ListThingsInput{
		MaxResults: aws.Int32(100),
	})
	if err != nil {
		slog.Error("Error listing things", "error", err)
		return Response{StatusCode: 500, Body: "Error listing things"}, nil
	}

	var targets []string
	for _, thing := range listOutput.Things {
		if thing.ThingArn != nil {
			targets = append(targets, *thing.ThingArn)
		}
	}

	if len(targets) == 0 {
		slog.Info("No things found to target")
		return Response{StatusCode: 200, Body: "No targets"}, nil
	}

	_, err = a.iotClient.CreateJob(ctx, &iot.CreateJobInput{
		JobId:           aws.String(jobID),
		Targets:         targets,
		Document:        aws.String(string(docBytes)),
		Description:     aws.String(fmt.Sprintf("OTA Update for %s", version)),
		TargetSelection: types.TargetSelectionSnapshot,
	})
	if err != nil {
		slog.Error("Error creating job", "error", err)
		return Response{StatusCode: 500, Body: "Error creating job"}, nil
	}

	slog.Info("Created job successfully", "jobID", jobID)
	return Response{
		StatusCode: 200,
		Body:       fmt.Sprintf("OTA job %s created successfully for %d targets.", jobID, len(targets)),
	}, nil
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		slog.Error("Unable to load AWS SDK config", "error", err)
		panic(fmt.Sprintf("unable to load SDK config, %v", err))
	}

	s3Client := s3.NewFromConfig(cfg)
	app := &App{
		s3Presign: s3.NewPresignClient(s3Client),
		iotClient: iot.NewFromConfig(cfg),
	}

	lambda.Start(app.Handler)
}
