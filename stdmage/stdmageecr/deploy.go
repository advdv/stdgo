// Package stdmageecr is a package for deploying via the AWS ECR.
package stdmageecr

import (
	"fmt"
	"os"
	"strings"

	"github.com/advdv/stdgo/stdmage/stdmagedev"
	"github.com/magefile/mage/sh"
)

var (
	_awsProfile                  string
	_awsRegion                   string
	_registry                    string
	_dockerImageToInspectForHash string
	_repository                  string
	_parameterPrefix             string
)

// Init inits the mage targets. The weird signature is to make Mage ignore this when importing.
func Init(
	awsProfile string,
	awsRegion string,
	registry string,
	dockerImageToInspectForHash string,
	repository string,
	parameterPrefix string,
	_ ...[]string, // just here so Mage doesn't recognize a "init" target.
) {
	_awsProfile = awsProfile
	_awsRegion = awsRegion
	_registry = registry
	_dockerImageToInspectForHash = dockerImageToInspectForHash
	_repository = repository
	_parameterPrefix = parameterPrefix
}

// BuildPushSetParam triggers a docker build, pushes the image, and sets a parameter in the AWS Parameter store.
func BuildPushSetParam(env string) error {
	parameterName := _parameterPrefix + "/api/stag/main"
	if env == "prod" {
		parameterName = _parameterPrefix + "/api/prod/main"
	}

	// fixes: https://github.com/aws/aws-cdk/issues/33264
	os.Setenv("BUILDX_NO_DEFAULT_ATTESTATIONS", "1")

	// instruct docker compose to build the image for us.
	if err := stdmagedev.Serve(); err != nil {
		return fmt.Errorf("failed to serve new containers: %w", err)
	}

	// login to the ecr repo for pushing
	if err := sh.Run("bash", "-c",
		`aws ecr get-login-password --profile `+_awsProfile+` --region `+_awsRegion+` |`+
			` docker login --username AWS --password-stdin `+_registry,
	); err != nil {
		return fmt.Errorf("failed to login: %w", err)
	}

	digestTag, err := sh.Output("docker", "inspect", _dockerImageToInspectForHash, "-f", "{{index .RepoDigests 0}}")
	if err != nil {
		return fmt.Errorf("failed to output digest: %w", err)
	}

	_, digest, _ := strings.Cut(digestTag, "@sha256:")
	finalTag := fmt.Sprintf("%s/%s:%s", _registry, _repository, digest)

	if err := sh.Run("docker", "tag",
		_dockerImageToInspectForHash, finalTag); err != nil {
		return fmt.Errorf("failed to tag docker image for pushing: %w", err)
	}

	if err := sh.Run("docker", "push", finalTag); err != nil {
		return fmt.Errorf("failed to push: %w", err)
	}

	if err := sh.Run("aws", "ssm", "put-parameter",
		"--region", _awsRegion,
		"--profile", _awsProfile,
		"--name", parameterName,
		"--type", "String",
		"--value", digest,
		"--no-cli-pager",
		"--overwrite",
	); err != nil {
		return fmt.Errorf("failed to set SSM parameter: %w", err)
	}

	return nil
}
