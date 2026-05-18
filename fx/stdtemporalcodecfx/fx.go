// Package stdtemporalcodecfx wires the Tink-backed Temporal payload codec
// into an fx application.
//
// Two composable fx.Options are exposed:
//
//   - Provide() wires the client/worker side. It produces a
//     converter.DataConverter that stdtemporalfx (or any Temporal client)
//     installs on its connection. When Config.Enabled is false a no-op
//     DataConverter is provided so local development works without a
//     keyset; when true, payloads are encrypted via Tink (AES-256-GCM in
//     the typical configuration) using the configured keyset.
//
//   - ProvideServer() wires the codec HTTP server. It produces an
//     http.Handler under the fx name tag "codec" implementing Temporal's
//     remote codec contract (POST /encode and POST /decode). The handler
//     enforces an allowlist on the X-Namespace request header. Callers are
//     expected to mount the handler on their own HTTP server.
//
// The Tink keyset is read from the environment and MUST be the same value
// across every worker, client and codec-server process for a given
// namespace set. It is provided as a base64-encoded Tink cleartext keyset
// in JSON form. To generate one, run:
//
//	go run github.com/advdv/stdgo/fx/stdtemporalcodecfx/cmd/stdtemporalcodec-genkeyset
//
// and paste the output into STDTEMPORALCODEC_KEYSET (and/or
// STDTEMPORALCODECSERVER_KEYSET).
package stdtemporalcodecfx

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"

	"github.com/advdv/stdgo/fx/stdtemporalcodecfx/stdtemporalcodec"
	"github.com/advdv/stdgo/stdfx"
	"github.com/tink-crypto/tink-go/v2/insecurecleartextkeyset"
	"github.com/tink-crypto/tink-go/v2/keyset"
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
	// Enabled toggles encryption of Temporal payloads. When false a
	// pass-through DataConverter is provided so local development works
	// without a configured keyset. Default false.
	Enabled bool `env:"ENABLED"`

	// Keyset is the base64-encoded Tink cleartext keyset (JSON form).
	// Required when Enabled is true. It MUST be the same value as the
	// one configured on the codec server and on every other
	// worker/client in the same namespace.
	Keyset string `env:"KEYSET"`

	// Namespace is the Temporal namespace this client/worker operates in.
	// It is bound into the AEAD additionalData to enforce cryptographic
	// tenant isolation. Required when Enabled is true.
	Namespace string `env:"NAMESPACE"`
}

// Params holds the dependencies for Provide.
type Params struct {
	fx.In

	Config Config
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
	handle, err := decodeKeyset(par.Config.Keyset)
	if err != nil {
		return Result{}, err
	}
	codec, err := stdtemporalcodec.New(stdtemporalcodec.Options{
		Keyset:    handle,
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
	// Enabled toggles the codec server. When false a stub handler that
	// responds 404 to every request is produced under the "codec" name
	// tag, so consumers can mount it unconditionally. Default false.
	Enabled bool `env:"ENABLED"`

	// Keyset is the base64-encoded Tink cleartext keyset (JSON form).
	// Required when Enabled is true. Must match the value used by every
	// worker/client whose payloads this server is expected to decode.
	Keyset string `env:"KEYSET"`

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

	Config ServerConfig
	Logger *zap.Logger
}

// ServerResult holds the values provided by ProvideServer.
type ServerResult struct {
	fx.Out

	// Handler is the codec server handler, exposing POST /encode and
	// POST /decode (suffix-matched so it can be mounted anywhere).
	Handler http.Handler `name:"codec"`
}

// NewServer constructs the codec server handler. When Config.Enabled is
// false the result handler responds 404 to every request so consumers can
// mount it unconditionally; they should still gate any CORS / route
// registration on Enabled if they want to avoid the stub being reachable
// at all.
func NewServer(par ServerParams) (ServerResult, error) {
	if !par.Config.Enabled {
		return ServerResult{Handler: http.NotFoundHandler()}, nil
	}
	handle, err := decodeKeyset(par.Config.Keyset)
	if err != nil {
		return ServerResult{}, err
	}
	codec, err := stdtemporalcodec.New(stdtemporalcodec.Options{
		Keyset:    handle,
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

// decodeKeyset decodes the configured base64 Tink cleartext keyset JSON and
// validates that it carries a primary key.
func decodeKeyset(s string) (*keyset.Handle, error) {
	if s == "" {
		return nil, errors.New("stdtemporalcodecfx: keyset is required")
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("stdtemporalcodecfx: decode base64 keyset: %w", err)
	}
	handle, err := insecurecleartextkeyset.Read(keyset.NewJSONReader(bytes.NewReader(raw)))
	if err != nil {
		return nil, fmt.Errorf("stdtemporalcodecfx: read tink keyset: %w", err)
	}
	if handle.KeysetInfo().GetPrimaryKeyId() == 0 {
		return nil, errors.New("stdtemporalcodecfx: tink keyset has no primary key")
	}
	return handle, nil
}
