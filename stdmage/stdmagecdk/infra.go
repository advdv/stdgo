// Package stdmagecdk provides targets for deploying with the AWS cdk.
package stdmagecdk

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	_ "embed"

	"github.com/advdv/stdgo/stdcdk"
	"github.com/advdv/stdgo/stdlo"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/destel/rill"
	"github.com/magefile/mage/sh"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var (
	// region we deploy to.
	region = "un.init.1"
	// profile profile for AWS.
	profile = "__uninitialized"
	// profile for bootstrapping the CDK.
	bootstrapProfile = "__uninitialized"
	// qualifier for the stack.
	qualifier = "__uninitialized"
	// docker image builds to build.
	builds = []DockerBuild{}
	// policies for bootstrapping.
	policies = []string{}
	// no noStaging can be set to true for projects that don't really have the concept of noStaging.
	noStaging bool
	// a ssm parameter prefix that will be added to the context (live) before diff and deploy.
	liveParamPrefix = "/__uninitialized"
)

// DockerBuild describes a Docker image to be build.
type DockerBuild struct {
	Name       string
	DockerFile string
	Platform   string
	Context    string
}

// Init initializes the mage targets.
func Init(
	awsRegion, awsProfile, awsBootstrapProfile, cdkQualifier string,
	dockerBuilds []DockerBuild,
	executionPolicies []string,
	noStagingEnv bool,
	ssmLiveParamPrefix string,
) {
	region = awsRegion
	profile = awsProfile
	bootstrapProfile = awsBootstrapProfile
	qualifier = cdkQualifier
	builds = dockerBuilds
	policies = executionPolicies
	noStaging = noStagingEnv
	liveParamPrefix = ssmLiveParamPrefix
}

//go:embed developer-boundary.yaml
var boundaryTemplate []byte

// Boundary sets up a permission boundary in the AWS IAM account.
func Boundary() error {
	tmpf, err := os.CreateTemp("", "")
	if err != nil {
		return fmt.Errorf("failed to create tmpl file: %w", err)
	}

	fmt.Fprintf(os.Stderr, "using temporary file: %v", tmpf.Name())
	if _, err := io.Copy(tmpf, bytes.NewReader(boundaryTemplate)); err != nil {
		return fmt.Errorf("failed to write template to temporary file: %w", err)
	}

	defer tmpf.Close()
	defer os.Remove(tmpf.Name())

	if err := sh.Run(
		"aws", "cloudformation", "create-stack",
		"--no-cli-pager",
		"--profile", "cl-sterndesk-admin",
		"--region", "eu-central-1",
		"--stack-name", "DeveloperPolicy",
		"--template-body", "file://"+tmpf.Name(),
		"--capabilities", "CAPABILITY_NAMED_IAM",
	); err != nil {
		return fmt.Errorf("failed ")
	}

	return nil
}

// Bootstrap the infra stack using AWS CDK.
func Bootstrap(env string) error {
	_, qual := profileFromEnv(env)

	accountID, err := sh.Output("aws", "sts", "get-caller-identity",
		"--profile", bootstrapProfile, "--query", "Account", "--output", "text")
	if err != nil {
		return fmt.Errorf("failed to get account id: %w", err)
	}

	policyNames := append([]string{
		fmt.Sprintf("arn:aws:iam::%s:policy/StandardCdkBaseExecPolicy", accountID),
	}, policies...)

	return cdk(env, qual, "bootstrap",
		"--profile", bootstrapProfile,
		"--custom-permissions-boundary", "developer-policy",
		"--cloudformation-execution-policies", strings.Join(policyNames, ","),
	)
}

// Diff calculates and shows the diff for our infrastructure deploy.
func Diff(ctx context.Context, env string) error {
	return DiffStack(ctx, env, "")
}

// DiffStack calculates and shows the diff a specific stack (exclusively).
func DiffStack(ctx context.Context, env string, stack string) error {
	profile, qual := profileFromEnv(env)
	if err := LiveContextParams(ctx, env); err != nil {
		return fmt.Errorf("failed to setup live SSM parameters: %w", err)
	}

	if stack == "" {
		stack = "--all"
	}

	return cdk(env, qual,
		"diff", stack,
		"--exclusively",
		"--profile", profile,
	)
}

// Deploy deploy our infrastructure.
func Deploy(ctx context.Context, env string) error {
	return DeployStack(ctx, env, "")
}

// DeployStack deploys a specific stack of our infrastructure (exclusively).
func DeployStack(ctx context.Context, env string, stack string) error {
	profile, qual := profileFromEnv(env)
	if err := LiveContextParams(ctx, env); err != nil {
		return fmt.Errorf("failed to setup live SSM parameters: %w", err)
	}

	if stack == "" {
		stack = "--all"
	}

	return cdk(env, qual, "deploy", stack,
		"--exclusively",
		"--require-approval", "never",
		"--profile", profile,
	)
}

// LiveContextParams updates the cdk context json with live parameter values.
func LiveContextParams(ctx context.Context, env string) error {
	profile, _ := profileFromEnv(env)

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithSharedConfigProfile(profile))
	if err != nil {
		return fmt.Errorf("failed to load aws config: %w", err)
	}

	idn, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return fmt.Errorf("failed to get AWS Account ID: %w", err)
	}

	cln := ssm.NewFromConfig(cfg)
	params, err := cln.GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
		Path:      aws.String(liveParamPrefix),
		Recursive: aws.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("failed to get parameters: %w", err)
	}

	contextPath := filepath.Join("infra", "infracdk", "cdk.context.json")

	data, err := os.ReadFile(contextPath)
	if err != nil {
		return fmt.Errorf("failed to read context file: %w", err)
	}

	for key := range gjson.ParseBytes(data).Map() {
		prefix := fmt.Sprintf("ssm:account=%s:parameterName=%s", *idn.Account, liveParamPrefix)
		if strings.HasPrefix(key, prefix) {
			data = stdlo.Must1(sjson.DeleteBytes(data, key))
		}
	}

	// write out parameters to the context in the format that the AWS CDK expects it. This
	// allows us to read the parameters like any other parameter in our CDK code.
	for _, param := range params.Parameters {
		key := fmt.Sprintf("ssm:account=%s:parameterName=%s:region=%s", *idn.Account, *param.Name, region)
		data = stdlo.Must1(sjson.SetBytes(data, key, param.Value))
	}

	if err := os.WriteFile(contextPath, data, 0o600); err != nil {
		return fmt.Errorf("failed to write updated context: %w", err)
	}

	return nil
}

// Build infra artifacts for deployment.
func Build() error {
	const buildDirPerm = 0o0700

	if err := os.MkdirAll(filepath.Join("infra", "infracdk", "builds"), buildDirPerm); err != nil {
		return fmt.Errorf("failed to create build dir: %w", err)
	}

	if err := buildImages(); err != nil {
		return fmt.Errorf("failed to build docker images: %w", err)
	}

	return nil
}

// build the docker images.
func buildImages() error {
	if err := rill.ForEach(rill.FromSlice(builds, nil), 4, func(build DockerBuild) error {
		if build.Platform == "" {
			build.Platform = "linux/amd64"
		}

		if build.DockerFile == "" {
			build.DockerFile = filepath.Join("lambda", build.Name, "Dockerfile")
		}

		if build.Context == "" {
			build.Context = "."
		}

		tag := fmt.Sprintf("%s:%s", strings.ToLower(qualifier), build.Name)
		if err := sh.RunWith(map[string]string{
			"DOCKER_BUILDKIT":                "1", // only build stages that are required for the target
			"BUILDX_NO_DEFAULT_ATTESTATIONS": "1", // we ran into: https://github.com/aws/aws-cdk/issues/30258
		}, "docker", "build",
			"-f", build.DockerFile,
			"--target", build.Name,
			"--tag", tag,
			"--platform", build.Platform, build.Context); err != nil {
			return fmt.Errorf("failed to build: %w", err)
		}

		if err := sh.Run("docker", "save", tag,
			"-o", filepath.Join("infra", "infracdk", "builds", build.Name+".tar"),
		); err != nil {
			return fmt.Errorf("failed to save docker image: %w", err)
		}

		return nil
	}); err != nil {
		return fmt.Errorf("failed to build image: %w", err)
	}

	return nil
}

// Output reads the output of the stack.
func Output(env, outputKey string) error {
	value, err := output(env, outputKey)
	if err != nil {
		return fmt.Errorf("failed to output: %w", err)
	}

	fmt.Fprintf(os.Stdout, "%s\n", value)

	return nil
}

func output(env, outputKey string) (string, error) {
	profile, qual := profileFromEnv(env)
	stackName := qual + stdcdk.RegionAcronym(region)

	value, err := sh.Output("aws", "cloudformation", "describe-stacks",
		"--stack-name", stackName,
		"--region", region,
		"--profile", profile,
		"--query", "Stacks[0].Outputs[?OutputKey==`"+outputKey+"`].OutputValue",
		"--output", "text")
	if err != nil {
		return "", fmt.Errorf("failed to read stack output: %w", err)
	}

	return value, nil
}

// List lists the stacks in our infrastructure.
func List(env string) error {
	profile, qual := profileFromEnv(env)

	return cdk(env, qual,
		"list",
		"--profile", profile,
	)
}

func cdk(env, qual string, args ...string) error {
	if err := os.Chdir(filepath.Join("infra", "infracdk")); err != nil {
		return fmt.Errorf("failed to chdir: %w", err)
	}

	// setup qualifier settings so we isolate our bootstrap between projects.
	args = append([]string{
		"cdk",
		"--toolkit-stack-name", qual + "Bootstrap",
		"--context", "qualifier=" + qual,
		"--context", "environment=" + env,
		"--qualifier", strings.ToLower(qual),
	}, args...)

	if err := sh.Run("npx", args...); err != nil {
		return fmt.Errorf("failed to run: %w", err)
	}

	return nil
}

// profileFromEnv determines the AWS credentials profile from the env argument.
func profileFromEnv(env string) (string, string) {
	profile, qual := profile, qualifier

	switch env {
	case "prod":
		return profile, qual + "P"
	case "stag":
		if noStaging {
			panic("staging no enabled")
		}

		return profile, qual + "S"
	default:
		panic("unsupported: " + env)
	}
}
