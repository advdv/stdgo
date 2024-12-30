package stdcdk_test

import (
	"fmt"
	"testing"

	"github.com/advdv/stdgo/stdcdk"
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/jsii-runtime-go"
	"github.com/stretchr/testify/require"
)

func TestRegionAcronym(t *testing.T) {
	t.Parallel()

	tests := []struct {
		region   string
		expected string
	}{
		{"us-east-1", "iad"},
		{"us-west-1", "sfo"},
		{"us-west-2", "pdx"},
		{"eu-central-1", "fra"},
		{"eu-west-1", "dub"},
		{"eu-west-2", "lhr"},
		{"eu-west-3", "cdg"},
		{"ap-southeast-1", "sin"},
		{"ap-southeast-2", "syd"},
		{"ap-northeast-1", "nrt"},
		{"ap-northeast-2", "icn"},
		{"ap-south-1", "bom"},
		{"sa-east-1", "gru"},
		{"ca-central-1", "yul"},
		{"me-south-1", "bah"},
		{"af-south-1", "cpt"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("Region %s", tt.region), func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.expected, stdcdk.RegionAcronym(tt.region), "for region %s", tt.region)
		})
	}

	t.Run("unknown region", func(t *testing.T) {
		t.Parallel()
		require.Panics(t, func() { stdcdk.RegionAcronym("unknown-region") }, "expected panic for unknown region")
	})
}

func TestStringContext(t *testing.T) {
	tests := []struct {
		name        string
		context     map[string]interface{}
		key         string
		expected    string
		shouldPanic bool
	}{
		{"String value", map[string]interface{}{"key1": "value1"}, "key1", "value1", false},
		{"Non-string value", map[string]interface{}{"key2": 123}, "key2", "", true},
		{"Missing key", map[string]interface{}{"key3": "value3"}, "missing_key", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := awscdk.NewApp(nil)
			for k, v := range tt.context {
				app.Node().SetContext(jsii.String(k), v)
			}

			if tt.shouldPanic {
				require.Panics(t, func() { stdcdk.StringContext(app, tt.key) })
			} else {
				require.Equal(t, tt.expected, stdcdk.StringContext(app, tt.key))
			}
		})
	}
}

func TestNewStack(t *testing.T) {
	tests := []struct {
		name            string
		context         map[string]interface{}
		region          string
		expectedStackID string
	}{
		{
			name:            "US East region with dev environment",
			context:         map[string]interface{}{"qualifier": "myapp", "environment": "dev"},
			region:          "us-east-1",
			expectedStackID: "myappiad",
		},
		{
			name:            "EU Central region with prod environment",
			context:         map[string]interface{}{"qualifier": "myapp", "environment": "prod"},
			region:          "eu-central-1",
			expectedStackID: "myappfra",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := awscdk.NewApp(nil)
			for k, v := range tt.context {
				app.Node().SetContext(jsii.String(k), v)
			}

			t.Setenv("CDK_DEFAULT_ACCOUNT", "123456789012")

			stack := stdcdk.NewStack(app, tt.region)
			require.Equal(t, tt.expectedStackID, *stack.StackName())
			require.Equal(t, tt.region, *stack.Region())
		})
	}
}
