package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/iot"
	iottypes "github.com/aws/aws-sdk-go-v2/service/iot/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// MockS3PresignClient is a mock of the S3PresignAPI interface.
type MockS3PresignClient struct {
	PresignGetObjectFunc func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}

func (m *MockS3PresignClient) PresignGetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
	if m.PresignGetObjectFunc != nil {
		return m.PresignGetObjectFunc(ctx, params, optFns...)
	}
	return &v4.PresignedHTTPRequest{}, nil
}

// MockIoTClient is a mock of the IoTAPI interface.
type MockIoTClient struct {
	ListThingsFunc func(ctx context.Context, params *iot.ListThingsInput, optFns ...func(*iot.Options)) (*iot.ListThingsOutput, error)
	CreateJobFunc  func(ctx context.Context, params *iot.CreateJobInput, optFns ...func(*iot.Options)) (*iot.CreateJobOutput, error)
}

func (m *MockIoTClient) ListThings(ctx context.Context, params *iot.ListThingsInput, optFns ...func(*iot.Options)) (*iot.ListThingsOutput, error) {
	if m.ListThingsFunc != nil {
		return m.ListThingsFunc(ctx, params, optFns...)
	}
	return &iot.ListThingsOutput{}, nil
}

func (m *MockIoTClient) CreateJob(ctx context.Context, params *iot.CreateJobInput, optFns ...func(*iot.Options)) (*iot.CreateJobOutput, error) {
	if m.CreateJobFunc != nil {
		return m.CreateJobFunc(ctx, params, optFns...)
	}
	return &iot.CreateJobOutput{}, nil
}

func TestBuildJobID(t *testing.T) {
	timestamp := int64(1700000000)
	tests := []struct {
		name     string
		version  string
		expected string
	}{
		{
			name:     "Simple version",
			version:  "v1.0.0",
			expected: "ota-v1-0-0-1700000000",
		},
		{
			name:     "Invalid characters replaced",
			version:  "release/v2.1.0-beta@foo",
			expected: "ota-release-v2-1-0-beta-foo-1700000000",
		},
		{
			name:     "Extremely long version truncated",
			version:  strings.Repeat("a", 100),
			expected: "ota-" + strings.Repeat("a", 49) + "-1700000000", // 64 - 4 (prefix) - 11 (suffix) = 49
		},
		{
			name:     "Empty version fallback",
			version:  "",
			expected: "ota-firmware-1700000000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildJobID(tt.version, timestamp)
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
			if len(result) > 64 {
				t.Errorf("job ID exceeds 64 chars: %s (len: %d)", result, len(result))
			}
		})
	}
}

func TestOTAHandler(t *testing.T) {
	tests := []struct {
		name           string
		event          events.S3Event
		presignFunc    func(ctx context.Context, params *s3.GetObjectInput) (*v4.PresignedHTTPRequest, error)
		listThingsFunc func(ctx context.Context, params *iot.ListThingsInput) (*iot.ListThingsOutput, error)
		createJobFunc  func(ctx context.Context, params *iot.CreateJobInput) (*iot.CreateJobOutput, error)
		wantStatus     int
		wantBodySub    string
	}{
		{
			name: "Success - OTA Job Created",
			event: events.S3Event{
				Records: []events.S3EventRecord{
					{
						S3: events.S3Entity{
							Bucket: events.S3Bucket{Name: "test-bucket"},
							Object: events.S3Object{URLDecodedKey: "firmware/v1.0.0.bin"},
						},
					},
				},
			},
			presignFunc: func(ctx context.Context, params *s3.GetObjectInput) (*v4.PresignedHTTPRequest, error) {
				return &v4.PresignedHTTPRequest{URL: "https://presigned.s3.amazonaws.com/firmware/v1.0.0.bin"}, nil
			},
			listThingsFunc: func(ctx context.Context, params *iot.ListThingsInput) (*iot.ListThingsOutput, error) {
				arn := "arn:aws:iot:eu-central-1:123456789012:thing/device-1"
				return &iot.ListThingsOutput{
					Things: []iottypes.ThingAttribute{
						{ThingArn: &arn},
					},
				}, nil
			},
			createJobFunc: func(ctx context.Context, params *iot.CreateJobInput) (*iot.CreateJobOutput, error) {
				if !strings.HasPrefix(*params.JobId, "ota-") {
					t.Errorf("expected JobId prefix ota-, got %s", *params.JobId)
				}
				if len(params.Targets) != 1 || params.Targets[0] != "arn:aws:iot:eu-central-1:123456789012:thing/device-1" {
					t.Errorf("unexpected targets: %v", params.Targets)
				}
				if !strings.Contains(*params.Document, "https://presigned.s3.amazonaws.com/firmware/v1.0.0.bin") {
					t.Errorf("expected job doc to contain download URL, got %s", *params.Document)
				}
				return &iot.CreateJobOutput{}, nil
			},
			wantStatus:  200,
			wantBodySub: "created successfully for 1 targets",
		},
		{
			name:       "Error - Empty Records",
			event:      events.S3Event{Records: []events.S3EventRecord{}},
			wantStatus: 400,
		},
		{
			name: "Error - S3 Presign Fails",
			event: events.S3Event{
				Records: []events.S3EventRecord{
					{
						S3: events.S3Entity{
							Bucket: events.S3Bucket{Name: "test-bucket"},
							Object: events.S3Object{URLDecodedKey: "firmware/v1.0.0.bin"},
						},
					},
				},
			},
			presignFunc: func(ctx context.Context, params *s3.GetObjectInput) (*v4.PresignedHTTPRequest, error) {
				return nil, errors.New("s3 presign failed")
			},
			wantStatus: 500,
		},
		{
			name: "Error - ListThings Fails",
			event: events.S3Event{
				Records: []events.S3EventRecord{
					{
						S3: events.S3Entity{
							Bucket: events.S3Bucket{Name: "test-bucket"},
							Object: events.S3Object{URLDecodedKey: "firmware/v1.0.0.bin"},
						},
					},
				},
			},
			presignFunc: func(ctx context.Context, params *s3.GetObjectInput) (*v4.PresignedHTTPRequest, error) {
				return &v4.PresignedHTTPRequest{URL: "http://url"}, nil
			},
			listThingsFunc: func(ctx context.Context, params *iot.ListThingsInput) (*iot.ListThingsOutput, error) {
				return nil, errors.New("iot list failed")
			},
			wantStatus: 500,
		},
		{
			name: "Success - No Targets Found",
			event: events.S3Event{
				Records: []events.S3EventRecord{
					{
						S3: events.S3Entity{
							Bucket: events.S3Bucket{Name: "test-bucket"},
							Object: events.S3Object{URLDecodedKey: "firmware/v1.0.0.bin"},
						},
					},
				},
			},
			presignFunc: func(ctx context.Context, params *s3.GetObjectInput) (*v4.PresignedHTTPRequest, error) {
				return &v4.PresignedHTTPRequest{URL: "http://url"}, nil
			},
			listThingsFunc: func(ctx context.Context, params *iot.ListThingsInput) (*iot.ListThingsOutput, error) {
				return &iot.ListThingsOutput{Things: []iottypes.ThingAttribute{}}, nil
			},
			wantStatus:  200,
			wantBodySub: "No targets",
		},
		{
			name: "Error - CreateJob Fails",
			event: events.S3Event{
				Records: []events.S3EventRecord{
					{
						S3: events.S3Entity{
							Bucket: events.S3Bucket{Name: "test-bucket"},
							Object: events.S3Object{URLDecodedKey: "firmware/v1.0.0.bin"},
						},
					},
				},
			},
			presignFunc: func(ctx context.Context, params *s3.GetObjectInput) (*v4.PresignedHTTPRequest, error) {
				return &v4.PresignedHTTPRequest{URL: "http://url"}, nil
			},
			listThingsFunc: func(ctx context.Context, params *iot.ListThingsInput) (*iot.ListThingsOutput, error) {
				arn := "arn:aws:iot:eu-central-1:123456789012:thing/device-1"
				return &iot.ListThingsOutput{
					Things: []iottypes.ThingAttribute{
						{ThingArn: &arn},
					},
				}, nil
			},
			createJobFunc: func(ctx context.Context, params *iot.CreateJobInput) (*iot.CreateJobOutput, error) {
				return nil, errors.New("iot create job failed")
			},
			wantStatus: 500,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockPresign := &MockS3PresignClient{
				PresignGetObjectFunc: func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
					if tt.presignFunc != nil {
						return tt.presignFunc(ctx, params)
					}
					return &v4.PresignedHTTPRequest{}, nil
				},
			}

			mockIoT := &MockIoTClient{
				ListThingsFunc: func(ctx context.Context, params *iot.ListThingsInput, optFns ...func(*iot.Options)) (*iot.ListThingsOutput, error) {
					if tt.listThingsFunc != nil {
						return tt.listThingsFunc(ctx, params)
					}
					return &iot.ListThingsOutput{}, nil
				},
				CreateJobFunc: func(ctx context.Context, params *iot.CreateJobInput, optFns ...func(*iot.Options)) (*iot.CreateJobOutput, error) {
					if tt.createJobFunc != nil {
						return tt.createJobFunc(ctx, params)
					}
					return &iot.CreateJobOutput{}, nil
				},
			}

			app := &App{
				s3Presign: mockPresign,
				iotClient: mockIoT,
			}

			resp, err := app.Handler(context.Background(), tt.event)
			if err != nil {
				t.Fatalf("unexpected handler error: %v", err)
			}

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("expected status %d, got %d", tt.wantStatus, resp.StatusCode)
			}

			if tt.wantBodySub != "" && !strings.Contains(resp.Body, tt.wantBodySub) {
				t.Errorf("expected body to contain %s, got %s", tt.wantBodySub, resp.Body)
			}
		})
	}
}
