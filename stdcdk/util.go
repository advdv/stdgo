// Package stdcdk provides some re-usable code for building AWS CDK constructs.
package stdcdk

import (
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

// RegionAcronym takes an AWS region identifier as input and returns its corresponding
// three-letter acronym. It supports common AWS regions and panics if an unsupported
// region is provided.
func RegionAcronym(region string) string {
	switch region {
	// US
	case "us-east-1":
		return "iad" // US East (N. Virginia)
	case "us-east-2":
		return "cmh" // US East (Ohio)
	case "us-west-1":
		return "sfo" // US West (N. California)
	case "us-west-2":
		return "pdx" // US West (Oregon)

	// Europe
	case "eu-central-1":
		return "fra" // EU (Frankfurt)
	case "eu-central-2":
		return "zrh" // EU (Zurich)
	case "eu-west-1":
		return "dub" // EU (Dublin, Ireland)
	case "eu-west-2":
		return "lhr" // EU (London)
	case "eu-west-3":
		return "cdg" // EU (Paris)
	case "eu-north-1":
		return "arn" // EU (Stockholm)
	case "eu-south-1":
		return "mxp" // EU (Milan)
	case "eu-south-2":
		return "mad" // EU (Madrid, Spain)

	// APAC
	case "ap-southeast-1":
		return "sin" // Asia Pacific (Singapore)
	case "ap-southeast-2":
		return "syd" // Asia Pacific (Sydney)
	case "ap-northeast-1":
		return "nrt" // Asia Pacific (Tokyo)
	case "ap-northeast-2":
		return "icn" // Asia Pacific (Seoul)
	case "ap-south-1":
		return "bom" // Asia Pacific (Mumbai)

	// Americas, Middle East, Africa
	case "sa-east-1":
		return "gru" // South America (São Paulo)
	case "ca-central-1":
		return "yul" // Canada (Central)
	case "me-south-1":
		return "bah" // Middle East (Bahrain)
	case "af-south-1":
		return "cpt" // Africa (Cape Town)
	default:
		panic(fmt.Sprintf("unknown AWS region: %s", region))
	}
}

// StringContext retrieves a string value from the context of the provided Construct scope
// using the specified key. It expects the context value associated with the key to be a string.
// If the value is not a string or if the key is missing, the function panics with a descriptive error.
// This function is useful for retrieving typed context values in CDK constructs.
func StringContext(scope constructs.Construct, key string) string {
	v := scope.Node().GetContext(jsii.String(key))

	s, ok := v.(string)
	if !ok {
		panic(fmt.Sprintf("value from context for key %s is not a string, got: %T", key, v))
	}

	return s
}

// NewStack creates and returns a new AWS CDK Stack within the specified scope,
// configured for a specific AWS region. It retrieves the "qualifier" and "environment"
// values from the scope's context to customize the stack's ID, description, and
// synthesizer settings. The stack ID is composed of the qualifier and the region acronym.
func NewStack(scope constructs.Construct, region string) awscdk.Stack {
	qual, env := StringContext(scope, "qualifier"), StringContext(scope, "environment")
	acr := RegionAcronym(region)

	return awscdk.NewStack(scope, jsii.Sprintf("%s%s", qual, acr), &awscdk.StackProps{
		Env: &awscdk.Environment{
			Account: jsii.String(os.Getenv("CDK_DEFAULT_ACCOUNT")),
			Region:  jsii.String(region),
		},
		Description: jsii.String(fmt.Sprintf("%s (env: %s, region: %s)", qual, env, region)),
		Synthesizer: awscdk.NewDefaultStackSynthesizer(&awscdk.DefaultStackSynthesizerProps{
			Qualifier: jsii.String(strings.ToLower(qual)),
		}),
	})
}
