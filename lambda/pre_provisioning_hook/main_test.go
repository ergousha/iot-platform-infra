package main

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// MockDynamoDBClient is a mock of the DynamoDBAPI interface.
type MockDynamoDBClient struct {
	GetItemFunc    func(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	UpdateItemFunc func(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
}

func (m *MockDynamoDBClient) GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if m.GetItemFunc != nil {
		return m.GetItemFunc(ctx, params, optFns...)
	}
	return &dynamodb.GetItemOutput{}, nil
}

func (m *MockDynamoDBClient) UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	if m.UpdateItemFunc != nil {
		return m.UpdateItemFunc(ctx, params, optFns...)
	}
	return &dynamodb.UpdateItemOutput{}, nil
}

func TestHandler(t *testing.T) {
	tableName := "test-table"

	tests := []struct {
		name           string
		event          PreProvisioningEvent
		getItemFunc    func(ctx context.Context, params *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error)
		updateItemFunc func(ctx context.Context, params *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error)
		wantAllow      bool
		wantOverride   string
	}{
		{
			name: "Success - Approved Provisioning",
			event: PreProvisioningEvent{
				Parameters: map[string]string{
					"MacAddress":   "AA:BB:CC:DD:EE:FF",
					"Secret":       "supersecret",
					"SerialNumber": "123456",
				},
			},
			getItemFunc: func(ctx context.Context, params *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
				return &dynamodb.GetItemOutput{
					Item: map[string]types.AttributeValue{
						"mac_address": &types.AttributeValueMemberS{Value: "AA:BB:CC:DD:EE:FF"},
						"secret":      &types.AttributeValueMemberS{Value: "supersecret"},
						"allowed":     &types.AttributeValueMemberBOOL{Value: true},
					},
				}, nil
			},
			updateItemFunc: func(ctx context.Context, params *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
				// Verify inputs for update
				if *params.TableName != tableName {
					t.Errorf("expected table name %s, got %s", tableName, *params.TableName)
				}
				return &dynamodb.UpdateItemOutput{}, nil
			},
			wantAllow:    true,
			wantOverride: "AA:BB:CC:DD:EE:FF",
		},
		{
			name: "Success - Approved (DynamoDB update fails, best effort)",
			event: PreProvisioningEvent{
				Parameters: map[string]string{
					"MacAddress":   "AA:BB:CC:DD:EE:FF",
					"Secret":       "supersecret",
					"SerialNumber": "123456",
				},
			},
			getItemFunc: func(ctx context.Context, params *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
				return &dynamodb.GetItemOutput{
					Item: map[string]types.AttributeValue{
						"mac_address": &types.AttributeValueMemberS{Value: "AA:BB:CC:DD:EE:FF"},
						"secret":      &types.AttributeValueMemberS{Value: "supersecret"},
						"allowed":     &types.AttributeValueMemberBOOL{Value: true},
					},
				}, nil
			},
			updateItemFunc: func(ctx context.Context, params *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
				return nil, errors.New("database update error")
			},
			wantAllow:    true,
			wantOverride: "AA:BB:CC:DD:EE:FF",
		},
		{
			name: "Rejected - Missing MAC Address",
			event: PreProvisioningEvent{
				Parameters: map[string]string{
					"Secret": "supersecret",
				},
			},
			wantAllow: false,
		},
		{
			name: "Rejected - Missing Secret",
			event: PreProvisioningEvent{
				Parameters: map[string]string{
					"MacAddress": "AA:BB:CC:DD:EE:FF",
				},
			},
			wantAllow: false,
		},
		{
			name: "Rejected - DynamoDB Error",
			event: PreProvisioningEvent{
				Parameters: map[string]string{
					"MacAddress": "AA:BB:CC:DD:EE:FF",
					"Secret":     "supersecret",
				},
			},
			getItemFunc: func(ctx context.Context, params *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
				return nil, errors.New("dynamo read error")
			},
			wantAllow: false,
		},
		{
			name: "Rejected - Item Not Found",
			event: PreProvisioningEvent{
				Parameters: map[string]string{
					"MacAddress": "AA:BB:CC:DD:EE:FF",
					"Secret":     "supersecret",
				},
			},
			getItemFunc: func(ctx context.Context, params *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
				return &dynamodb.GetItemOutput{Item: nil}, nil
			},
			wantAllow: false,
		},
		{
			name: "Rejected - Device Allowed False",
			event: PreProvisioningEvent{
				Parameters: map[string]string{
					"MacAddress": "AA:BB:CC:DD:EE:FF",
					"Secret":     "supersecret",
				},
			},
			getItemFunc: func(ctx context.Context, params *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
				return &dynamodb.GetItemOutput{
					Item: map[string]types.AttributeValue{
						"mac_address": &types.AttributeValueMemberS{Value: "AA:BB:CC:DD:EE:FF"},
						"secret":      &types.AttributeValueMemberS{Value: "supersecret"},
						"allowed":     &types.AttributeValueMemberBOOL{Value: false},
					},
				}, nil
			},
			wantAllow: false,
		},
		{
			name: "Rejected - Secret Mismatch",
			event: PreProvisioningEvent{
				Parameters: map[string]string{
					"MacAddress": "AA:BB:CC:DD:EE:FF",
					"Secret":     "wrongsecret",
				},
			},
			getItemFunc: func(ctx context.Context, params *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
				return &dynamodb.GetItemOutput{
					Item: map[string]types.AttributeValue{
						"mac_address": &types.AttributeValueMemberS{Value: "AA:BB:CC:DD:EE:FF"},
						"secret":      &types.AttributeValueMemberS{Value: "supersecret"},
						"allowed":     &types.AttributeValueMemberBOOL{Value: true},
					},
				}, nil
			},
			wantAllow: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockDynamoDBClient{
				GetItemFunc: func(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
					if tt.getItemFunc != nil {
						return tt.getItemFunc(ctx, params)
					}
					return &dynamodb.GetItemOutput{}, nil
				},
				UpdateItemFunc: func(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
					if tt.updateItemFunc != nil {
						return tt.updateItemFunc(ctx, params)
					}
					return &dynamodb.UpdateItemOutput{}, nil
				},
			}

			app := &App{
				dbClient:  mockClient,
				tableName: tableName,
			}

			resp, err := app.Handler(context.Background(), tt.event)
			if err != nil {
				t.Fatalf("unexpected handler error: %v", err)
			}

			if resp.AllowProvisioning != tt.wantAllow {
				t.Errorf("expected allowProvisioning %v, got %v", tt.wantAllow, resp.AllowProvisioning)
			}

			if tt.wantAllow {
				if resp.ParameterOverrides == nil {
					t.Fatal("expected parameter overrides, got nil")
				}
				if resp.ParameterOverrides["MacAddress"] != tt.wantOverride {
					t.Errorf("expected MacAddress override %s, got %s", tt.wantOverride, resp.ParameterOverrides["MacAddress"])
				}
			} else {
				if resp.ParameterOverrides != nil {
					t.Errorf("expected nil parameter overrides, got %v", resp.ParameterOverrides)
				}
			}
		})
	}
}
