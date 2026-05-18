// Package stdtemporalcodecfx wires the KMS-backed Temporal payload codec
// into an fx application.
//
// Two composable fx.Options are exposed:
//
//   - Provide() wires the client/worker side. It produces a
//     converter.DataConverter that stdtemporalfx (or any Temporal client)
//     installs on its connection. When Config.Enabled is false a no-op
//     DataConverter is provided so local development works without KMS;
//     when true, payloads are envelope-encrypted with the configured KMS
//     key. The KMS client is built from the ambient aws.Config (typically
//     provided by stdawsfx).
//
//   - ProvideServer() wires the codec HTTP server. It produces an
//     http.Handler under the fx name tag "codec" implementing Temporal's
//     remote codec contract (POST /encode and POST /decode). The handler
//     enforces an allowlist on the X-Namespace request header. The KMS
//     client is built from the ambient aws.Config.
//
// Tests can substitute the KMS dependency by supplying a
// stdtemporalcodec.KMS directly:
//
//	fx.Supply(fx.Annotate(fakeKMS, fx.As(new(stdtemporalcodec.KMS))))
//
// When supplied that way it takes precedence over the AWS-built client and
// aws.Config does not need to be in the graph.
package stdtemporalcodecfx

import (
	"net/http"

	"github.com/advdv/stdgo/fx/stdtemporalcodecfx/stdtemporalcodec"
	"github.com/advdv/stdgo/stdfx"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"go.temporal.io/sdk/converter"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// seedNamespace satisfies the (non-empty) namespace requirement of
// stdtemporalcodec.New for the server constructor. It is overwritten on
// every request via Codec.WithNamespace, so its value is never used on the
// wire.
const seedNamespace = "_codec_server_seed_"

// Config configures the client/worker side of the codec module (Provide).
// Environment variables are prefixed with STDTEMPORALCODEC_.
type Config struct {
	// Enabled toggles KMS encryption of Temporal payloads. When false a
	// pass-through DataConverter is provided so local development works
	// without KMS. Default false.
	Enabled bool `env:"ENABLED"`

	// KMSKeyID is the KMS key ARN or alias used to generate data keys.
	// Required when Enabled is true.
	KMSKeyID string `env:"KMS_KEY_ID"`

	// Namespace is the Temporal namespace this client/worker operates in.
	// It is bound into the KMS EncryptionContext to enforce cryptographic
	// tenant isolation. Required when Enabled is true.
	Namespace string `env:"NAMESPACE"`
}

// Params holds the dependencies for Provide.
type Params struct {
	fx.In

	Config    Config
	AWSConfig aws.Config `optional:"true"`
	// KMS is an optional override for the KMS client. When nil the codec
	// uses kms.NewFromConfig(AWSConfig). Tests supply a fake here.
	KMS stdtemporalcodec.KMS `optional:"true"`
}

// Result holds the values provided by Provide.
type Result struct {
	fx.Out

	// DataConverter is suitable for installing on a Temporal client.
	// stdtemporalfx already consumes it as an optional dependency.
	DataConverter converter.DataConverter
}

// New constructs the data converter.
func New(par Params) (Result, error) {
	if !par.Config.Enabled {
		return Result{DataConverter: converter.GetDefaultDataConverter()}, nil
	}
	k := par.KMS
	if k == nil {
		k = kms.NewFromConfig(par.AWSConfig)
	}
	codec, err := stdtemporalcodec.New(k, stdtemporalcodec.Options{
		KeyID:     par.Config.KMSKeyID,
		Namespace: par.Config.Namespace,
	})
	if err != nil {
		return Result{}, err
	}
	dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), codec)
	return Result{DataConverter: dc}, nil
}

// Provide returns an fx.Option providing the client/worker side data
// converter. See package documentation for details.
func Provide() fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdtemporalcodec", New)
}

// ServerConfig configures the codec server (ProvideServer). Environment
// variables are prefixed with STDTEMPORALCODECSERVER_.
type ServerConfig struct {
	// KMSKeyID is the KMS key ARN/alias the codec uses to wrap data keys.
	// Required.
	KMSKeyID string `env:"KMS_KEY_ID,required"`

	// AllowedNamespaces lists the Temporal namespaces this server will
	// service. Requests bearing any other (normalized) namespace are
	// rejected with 403 Forbidden. If empty, all requests are rejected.
	AllowedNamespaces []string `env:"ALLOWED_NAMESPACES" envSeparator:","`

	// StripCloudSuffix toggles the StripCloudAccountSuffix normalizer
	// (which trims everything after the last dot in X-Namespace). Defaults
	// to true so the handler works out of the box with the Temporal Cloud
	// Web UI.
	StripCloudSuffix bool `env:"STRIP_CLOUD_SUFFIX" envDefault:"true"`
}

// ServerParams holds the dependencies for ProvideServer.
type ServerParams struct {
	fx.In

	Config    ServerConfig
	Logger    *zap.Logger
	AWSConfig aws.Config `optional:"true"`
	// KMS is an optional override for the KMS client. When nil the server
	// uses kms.NewFromConfig(AWSConfig). Tests supply a fake here.
	KMS stdtemporalcodec.KMS `optional:"true"`
}

// ServerResult holds the values provided by ProvideServer.
type ServerResult struct {
	fx.Out

	// Handler is the codec server handler, exposing POST /encode and
	// POST /decode (suffix-matched so it can be mounted anywhere).
	Handler http.Handler `name:"codec"`
}

// NewServer constructs the codec server handler.
func NewServer(par ServerParams) (ServerResult, error) {
	k := par.KMS
	if k == nil {
		k = kms.NewFromConfig(par.AWSConfig)
	}
	codec, err := stdtemporalcodec.New(k, stdtemporalcodec.Options{
		KeyID:     par.Config.KMSKeyID,
		Namespace: seedNamespace,
	})
	if err != nil {
		return ServerResult{}, err
	}

	srvOpts := stdtemporalcodec.HandlerOptions{
		Codec:             codec,
		AllowedNamespaces: par.Config.AllowedNamespaces,
		Logger:            par.Logger,
	}
	if par.Config.StripCloudSuffix {
		srvOpts.NormalizeNamespace = stdtemporalcodec.StripCloudAccountSuffix
	}

	handler, err := stdtemporalcodec.Handler(srvOpts)
	if err != nil {
		return ServerResult{}, err
	}
	return ServerResult{Handler: handler}, nil
}

// ProvideServer returns an fx.Option providing the codec server http.Handler
// under the fx name tag "codec". See package documentation for details.
func ProvideServer() fx.Option {
	return stdfx.ZapEnvCfgModule[ServerConfig]("stdtemporalcodecserver", NewServer)
}
