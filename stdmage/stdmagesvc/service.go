package stdmagesvc

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/advdv/stdgo/stdmage/stdmagedev"
	"github.com/destel/rill"
	"github.com/iancoleman/strcase"
	"github.com/magefile/mage/sh"
)

var (
	_awsProfile         string
	_awsRegion          string
	_registry           string
	_stackName          string
	_exportPrefix       string
	_serviceIdent       string
	_composeProjectName string
	_dockerImagePrefix  string
	_ecsClusterName     string
	_deploymentIdents   []string
)

// Init inits the mage targets. The weird signature is to make Mage ignore this when importing.
func Init(
	awsProfile string,
	awsRegion string,
	registry string,
	stackName string,
	exportPrefix string,
	serviceIdent string,
	composeProbjectName string,
	dockerImagePrefix string,
	ecsClusterName string,
	deploymentIdents []string,
	_ ...[]string, // just here so Mage doesn't recognize a "init" target.
) {
	_awsProfile = awsProfile
	_awsRegion = awsRegion
	_registry = registry
	_stackName = stackName
	_exportPrefix = exportPrefix
	_serviceIdent = serviceIdent
	_composeProjectName = composeProbjectName
	_dockerImagePrefix = dockerImagePrefix
	_ecsClusterName = ecsClusterName
	_deploymentIdents = deploymentIdents
}

// DockerLogin logs docker into ther registry.
func DockerLogin() error {
	if err := sh.Run("bash", "-c",
		`aws ecr get-login-password --profile `+_awsProfile+` --region `+_awsRegion+` |`+
			` docker login --username AWS --password-stdin `+_registry,
	); err != nil {
		return fmt.Errorf("docker login: %w", err)
	}

	return nil
}

// Build will build the service container(s).
func Build() error {
	// fixes: https://github.com/aws/aws-cdk/issues/33264
	os.Setenv("BUILDX_NO_DEFAULT_ATTESTATIONS", "1")

	// instruct docker compose to build the image for us.
	if err := stdmagedev.Serve(); err != nil {
		return fmt.Errorf("build and serve new containers: %w", err)
	}

	return nil
}

// Push push the last build container to the registry.
func Push(deploymentIdent string) error {
	return push(deploymentIdent, true)
}

func push(deploymentIdent string, doLogin bool) error {
	if err := checkDeploymentIdents(deploymentIdent); err != nil {
		return err
	}

	// instruct docker to login with the registry.
	if doLogin {
		if err := DockerLogin(); err != nil {
			return fmt.Errorf("docker login: %w", err)
		}
	}

	// read the service info.
	service, err := readServiceInfo(deploymentIdent)
	if err != nil {
		return fmt.Errorf("read service info: %w", err)
	}

	{
		// main image retagging and push
		mainDockerImageToReTag := fmt.Sprintf("%s-%s%s", _composeProjectName, _dockerImagePrefix, strcase.ToKebab(_serviceIdent))
		mainImageFinalTag := fmt.Sprintf("%s/%s:%s", _registry, service.RepositoryName, service.MainImageTag)
		if err := sh.Run("docker", "tag",
			mainDockerImageToReTag, mainImageFinalTag); err != nil {
			return fmt.Errorf("failed to tag docker image for pushing: %w", err)
		}

		if err := sh.Run("docker", "push", mainImageFinalTag); err != nil {
			return fmt.Errorf("failed to push: %w", err)
		}
	}

	return nil
}

// UpdateLambdas will update any lambdas associated with the service.
func UpdateLambdas(deploymentIdent string) error {
	if err := checkDeploymentIdents(deploymentIdent); err != nil {
		return err
	}

	service, err := readServiceInfo(deploymentIdent)
	if err != nil {
		return fmt.Errorf("read service info: %w", err)
	}

	// NOTE: if the need is there, the code below can be run concurrently for each Lambda.
	for _, lambda := range service.Lambdas {
		if err := sh.Run("aws", "lambda", "update-function-code",
			"--region", _awsRegion,
			"--profile", _awsProfile,
			"--function-name", lambda.FunctionName,
			"--image-uri", fmt.Sprintf("%s/%s:%s", _registry, service.RepositoryName, service.MainImageTag),
			"--no-cli-pager"); err != nil {
			return fmt.Errorf("failed to update function with new code: %w", err)
		}

		if err := sh.Run("aws", "lambda", "wait", "function-updated-v2",
			"--region", _awsRegion,
			"--profile", _awsProfile,
			"--function-name", lambda.FunctionName,
			"--no-cli-pager"); err != nil {
			return fmt.Errorf("failed to wait for function to be updated: %w", err)
		}
	}

	return nil
}

// UpdateService the service by forcing a new deployment and waiting for it to be stable.
func UpdateService(deploymentIdent string) error {
	if err := checkDeploymentIdents(deploymentIdent); err != nil {
		return err
	}

	service, err := readServiceInfo(deploymentIdent)
	if err != nil {
		return fmt.Errorf("read service info: %w", err)
	}

	if err := sh.Run("aws", "ecs", "update-service",
		"--region", _awsRegion,
		"--profile", _awsProfile,
		"--cluster", _ecsClusterName,
		"--service", service.ServiceName,
		"--no-cli-pager",
		"--force-new-deployment"); err != nil {
		return fmt.Errorf("update service with new deployment: %w", err)
	}

	if err := sh.Run("aws", "ecs", "wait", "services-stable",
		"--region", _awsRegion,
		"--profile", _awsProfile,
		"--cluster", _ecsClusterName,
		"--service", service.ServiceName,
		"--no-cli-pager"); err != nil {
		return fmt.Errorf("wait for service to be stable: %w", err)
	}

	return nil
}

// Deploy build and pushes a new docker image, then updates the service.
func Deploy(deploymentIdent string) error {
	return deploy(deploymentIdent, true, true)
}

func deploy(deploymentIdent string, doBuild, doLogin bool) error {
	if doBuild {
		if err := Build(); err != nil {
			return fmt.Errorf("build: %w", err)
		}
	}

	if err := push(deploymentIdent, doLogin); err != nil {
		return fmt.Errorf("push: %w", err)
	}

	if err := UpdateService(deploymentIdent); err != nil {
		return fmt.Errorf("update service: %w", err)
	}

	if err := UpdateLambdas(deploymentIdent); err != nil {
		return fmt.Errorf("update lambda: %w", err)
	}

	return nil
}

// DeployAll build, pushes and deploys new docker containers for all deployments.
func DeployAll() error {
	if err := Build(); err != nil {
		return fmt.Errorf("build: %w", err)
	}

	if err := DockerLogin(); err != nil {
		return fmt.Errorf("docker login: %w", err)
	}

	if err := rill.ForEach(rill.FromSlice(_deploymentIdents, nil), 5, func(deploymentIdent string) error {
		return deploy(deploymentIdent, false, false)
	}); err != nil {
		return fmt.Errorf("for each deployment: %w", err)
	}

	return nil
}

// Service describes service information.
type Service struct {
	RepositoryName string
	MainImageTag   string
	ServiceName    string
	Lambdas        []*Lambda
}

type Lambda struct {
	FunctionName string
}

// Deployment describes deployment information.
type Deployment struct {
	Services map[string]*Service
}

// Deployments hold the deployments we found.
type Deployments map[string]*Deployment

// ReadDeploymentInfo will read the exports of the stack and structure it in service information.
func ReadDeploymentInfo() (Deployments, error) {
	type shape struct {
		Exports []struct {
			Name, Value string
		}
	}

	exportData, err := sh.Output("aws", "cloudformation", "list-exports",
		"--region", _awsRegion,
		"--profile", _awsProfile,
		"--no-cli-pager",
	)
	if err != nil {
		return nil, fmt.Errorf("list stack exports: %w", err)
	}

	var exports shape
	if err := json.Unmarshal([]byte(exportData), &exports); err != nil {
		return nil, fmt.Errorf("unmarshal export data: %w", err)
	}

	deployments := make(Deployments)
	for _, export := range exports.Exports {
		_, name, found := strings.Cut(export.Name, _exportPrefix)
		if !found {
			continue
		}

		fields := strings.Split(name, ":")
		if len(fields) < 3 {
			return nil, fmt.Errorf("invalid export name: %s", name)
		}

		deploymentIdent, serviceIdent, propIdent := fields[0], fields[1], fields[2]

		deployment, ok := deployments[deploymentIdent]
		if !ok {
			deployment = &Deployment{Services: map[string]*Service{}}
		}

		service, ok := deployment.Services[serviceIdent]
		if !ok {
			service = &Service{}
		}

		switch propIdent {
		case "MainImageTag":
			service.MainImageTag = export.Value
		case "RepositoryName":
			service.RepositoryName = export.Value
		case "ServiceName":
			service.ServiceName = export.Value
		}

		// lambdas for the service are encoded as 4-part fields.
		if len(fields) == 4 {
			service.Lambdas = append(service.Lambdas, &Lambda{
				FunctionName: export.Value,
			})
		}

		deployment.Services[serviceIdent] = service
		deployments[deploymentIdent] = deployment
	}

	return deployments, nil
}

func readServiceInfo(deploymentIndent string) (*Service, error) {
	deployments, err := ReadDeploymentInfo()
	if err != nil {
		return nil, fmt.Errorf("read deployment info: %w", err)
	}

	deployment, ok := deployments[deploymentIndent]
	if !ok || deployment == nil {
		return nil, fmt.Errorf("no info about deployment: %s", deploymentIndent)
	}

	service, ok := deployment.Services[_serviceIdent]
	if !ok {
		return nil, fmt.Errorf("no info about service: %s", _serviceIdent)
	}

	return service, nil
}

func checkDeploymentIdents(ident string) error {
	if !slices.Contains(_deploymentIdents, ident) {
		return fmt.Errorf("unsupported ident '%s', allowed: %v", ident, _deploymentIdents)
	}
	return nil
}
