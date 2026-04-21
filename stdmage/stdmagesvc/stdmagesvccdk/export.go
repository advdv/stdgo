// Package stdmagesvccdk provides CDK export utilities for service lambdas.
package stdmagesvccdk

import (
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
	"github.com/iancoleman/strcase"
)

// ServiceLambda represents a lambda function within a service.
type ServiceLambda interface {
	FunctionName() *string
}

// Service represents a deployable service with lambdas.
type Service[L ServiceLambda] interface {
	ServiceIdent() string
	RepositoryName() *string
	ServiceName() *string
	MainImageTag() string
	Lambdas() map[string]L
}

// ExportServiceInfo exports CDK CloudFormation outputs for a service and its lambdas.
func ExportServiceInfo[L ServiceLambda](scope constructs.Construct, deploymentIdent string, svc Service[L]) {
	scope = constructs.NewConstruct(scope, jsii.Sprintf("ServiceInfo%s", strcase.ToCamel(svc.ServiceIdent())))

	awscdk.NewCfnOutput(scope, jsii.String("RepositoryNameOutput"),
		&awscdk.CfnOutputProps{
			ExportName: jsii.Sprintf("BwSvc:%s:%s:RepositoryName", deploymentIdent, svc.ServiceIdent()),
			Value:      svc.RepositoryName(),
		})
	awscdk.NewCfnOutput(scope, jsii.String("ServiceNameOutput"),
		&awscdk.CfnOutputProps{
			ExportName: jsii.Sprintf("BwSvc:%s:%s:ServiceName", deploymentIdent, svc.ServiceIdent()),
			Value:      svc.ServiceName(),
		})
	awscdk.NewCfnOutput(scope, jsii.String("ServiceMainImageTagOutput"),
		&awscdk.CfnOutputProps{
			ExportName: jsii.Sprintf("BwSvc:%s:%s:MainImageTag", deploymentIdent, svc.ServiceIdent()),
			Value:      jsii.String(svc.MainImageTag()),
		})

	lambdas := svc.Lambdas()
	idents := slices.Collect(maps.Keys(lambdas))
	sort.Strings(idents)

	for _, lambdaIdent := range idents {
		lambda := lambdas[lambdaIdent]
		exportName := fmt.Sprintf("BwSvc:%s:%s:lambda:%s",
			deploymentIdent, svc.ServiceIdent(), strcase.ToKebab(lambdaIdent))
		awscdk.NewCfnOutput(scope, jsii.String("ServiceLambdaNameOutput"+lambdaIdent),
			&awscdk.CfnOutputProps{
				ExportName: jsii.String(exportName),
				Value:      lambda.FunctionName(),
			})
	}
}
