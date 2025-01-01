// Package stdmagecdk provides targets for deploying with the AWS cdk.
package stdmagecdk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/advdv/stdgo/stdcdk"
	"github.com/destel/rill"
	"github.com/magefile/mage/sh"
)

var (
	// region we deploy to.
	region = "un.init.1"
	// profile profile for AWS.
	profile = "__uninitialized"
	// qualifier for the stack.
	qualifier = "__uninitialized"
	// docker image targets to build.
	targets = []string{}
)

// Init initializes the mage targets.
func Init(awsRegion, awsProfile, cdkQualifier string, dockerImageTargets []string) {
	region = awsRegion
	profile = awsProfile
	qualifier = cdkQualifier
	targets = dockerImageTargets
}

// Bootstrap the infra stack using AWS CDK.
func Bootstrap(env string) error {
	profile, qual := profileFromEnv(env)

	accountID, err := sh.Output("aws", "sts", "get-caller-identity",
		"--profile", profile, "--query", "Account", "--output", "text")
	if err != nil {
		return fmt.Errorf("failed to get account id: %w", err)
	}

	return cdk(env, qual, "bootstrap",
		"--profile", profile,
		"--cloudformation-execution-policies", strings.Join([]string{
			fmt.Sprintf("arn:aws:iam::%s:policy/StandardCdkBaseExecPolicy", accountID),
			"arn:aws:iam::aws:policy/SecretsManagerReadWrite",
			"arn:aws:iam::aws:policy/AmazonEC2FullAccess",
			"arn:aws:iam::aws:policy/AmazonRDSFullAccess",
			"arn:aws:iam::aws:policy/AWSKeyManagementServicePowerUser",
		}, ","),
	)
}

// Diff calculates and shows the diff for our infrastructure deploy.
func Diff(env string) error {
	return DiffStack(env, "")
}

// DiffStack calculates and shows the diff a specific stack (exclusively).
func DiffStack(env string, stack string) error {
	profile, qual := profileFromEnv(env)

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
func Deploy(env string) error {
	return DeployStack(env, "")
}

// DeployStack deploys a specific stack of our infrastructure (exclusively).
func DeployStack(env string, stack string) error {
	profile, qual := profileFromEnv(env)

	if stack == "" {
		stack = "--all"
	}

	return cdk(env, qual, "deploy", stack,
		"--exclusively",
		"--require-approval", "never",
		"--profile", profile,
	)
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
	if err := rill.ForEach(rill.FromSlice(targets, nil), 4, func(target string) error {
		tag := fmt.Sprintf("%s:%s", strings.ToLower(qualifier), target)
		if err := sh.RunWith(map[string]string{
			"DOCKER_BUILDKIT": "1", // only build stages that are required for the target
		}, "docker", "build",
			"-f", filepath.Join("lambda", target, "Dockerfile"),
			"--target", target,
			"--tag", tag,
			"--platform", "linux/amd64", "."); err != nil {
			return fmt.Errorf("failed to build: %w", err)
		}

		if err := sh.Run("docker", "save", tag,
			"-o", filepath.Join("infra", "infracdk", "builds", target+".tar"),
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
		return profile, qual + "S"
	default:
		panic("unsupported: " + env)
	}
}
