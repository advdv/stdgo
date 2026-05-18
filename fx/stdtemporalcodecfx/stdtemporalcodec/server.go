package stdtemporalcodec

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	commonpb "go.temporal.io/api/common/v1"
	"go.uber.org/zap"
	"google.golang.org/protobuf/encoding/protojson"
)

// HeaderNamespace is the HTTP request header that carries the Temporal
// namespace for the codec call.
const HeaderNamespace = "X-Namespace"

// Default route paths.
const (
	PathEncode = "/encode"
	PathDecode = "/decode"
)

// StripCloudAccountSuffix is a NormalizeNamespace helper that trims everything
// after (and including) the last dot in the given namespace. Temporal Cloud's
// Web UI sends X-Namespace as `<name>.<accountID>`; the codec is configured
// with bare namespace names, so callers integrating with Cloud should set
// HandlerOptions.NormalizeNamespace = StripCloudAccountSuffix.
//
// If the input contains no dot it is returned unchanged.
func StripCloudAccountSuffix(ns string) string {
	if i := strings.LastIndex(ns, "."); i >= 0 {
		return ns[:i]
	}
	return ns
}

// HandlerOptions configures the codec server handler.
type HandlerOptions struct {
	// Codec performs the actual encryption/decryption work. It will be
	// scoped to the per-request namespace via Codec.WithNamespace before
	// each call. Required.
	Codec *Codec

	// AllowedNamespaces is the set of Temporal namespaces the server will
	// service. A request bearing any other namespace is rejected with
	// 403 Forbidden. If empty, all namespaces are rejected. Compared
	// against the value returned by NormalizeNamespace (if set).
	AllowedNamespaces []string

	// NormalizeNamespace, if set, is applied to the X-Namespace header
	// value before allowlist comparison and before passing to the codec
	// (which forwards it as the KMS EncryptionContext namespace value).
	// Defaults to identity. Use StripCloudAccountSuffix when serving the
	// Temporal Cloud Web UI.
	NormalizeNamespace func(string) string

	// Logger receives structured warnings (auth denials) and errors
	// (codec failures). Defaults to zap.NewNop().
	Logger *zap.Logger
}

// Handler returns an http.Handler that serves POST /encode and POST /decode.
// The returned handler routes by request path suffix so it can be mounted at
// any prefix.
func Handler(opts HandlerOptions) (http.Handler, error) {
	if opts.Codec == nil {
		return nil, errors.New("stdtemporalcodec: HandlerOptions.Codec is required")
	}
	allowed := make(map[string]struct{}, len(opts.AllowedNamespaces))
	for _, ns := range opts.AllowedNamespaces {
		if ns == "" {
			continue
		}
		allowed[ns] = struct{}{}
	}
	normalize := opts.NormalizeNamespace
	if normalize == nil {
		normalize = func(s string) string { return s }
	}
	logs := opts.Logger
	if logs == nil {
		logs = zap.NewNop()
	}
	return &handler{
		codec:     opts.Codec,
		allowed:   allowed,
		normalize: normalize,
		logs:      logs,
	}, nil
}

type handler struct {
	codec     *Codec
	allowed   map[string]struct{}
	normalize func(string) string
	logs      *zap.Logger
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	operation, ok := routeOperation(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	rawNamespace := r.Header.Get(HeaderNamespace)
	if rawNamespace == "" {
		http.Error(w, "missing X-Namespace header", http.StatusBadRequest)
		return
	}
	namespace := h.normalize(rawNamespace)
	if _, allowed := h.allowed[namespace]; !allowed {
		h.logs.Warn("namespace not allowed",
			zap.String("namespace_raw", rawNamespace),
			zap.String("namespace_normalized", namespace))
		http.Error(w, fmt.Sprintf("namespace %q is not permitted", rawNamespace), http.StatusForbidden)
		return
	}

	payloads, err := readPayloads(r.Body)
	if err != nil {
		h.logs.Warn("malformed request body",
			zap.String("namespace", namespace),
			zap.Error(err))
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	codec := h.codec.WithNamespace(namespace)

	var out []*commonpb.Payload
	switch operation {
	case opEncode:
		//nolint:contextcheck // converter.PayloadCodec.Encode does not take a context.
		out, err = codec.Encode(payloads)
	case opDecode:
		//nolint:contextcheck // converter.PayloadCodec.Decode does not take a context.
		out, err = codec.Decode(payloads)
	}
	if err != nil {
		h.logs.Error("codec operation failed",
			zap.String("operation", operationName(operation)),
			zap.String("namespace", namespace),
			zap.Error(err))
		// Codec errors are typically caused by bad/forged input rather
		// than internal failure; report as 400 to mirror Temporal's
		// reference handler.
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := writePayloads(w, out); err != nil {
		h.logs.Error("write response failed",
			zap.String("namespace", namespace),
			zap.Error(err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

type op int

const (
	opEncode op = iota + 1
	opDecode
)

func operationName(o op) string {
	switch o {
	case opEncode:
		return "encode"
	case opDecode:
		return "decode"
	default:
		return "unknown"
	}
}

func routeOperation(path string) (op, bool) {
	switch {
	case strings.HasSuffix(path, PathEncode):
		return opEncode, true
	case strings.HasSuffix(path, PathDecode):
		return opDecode, true
	default:
		return 0, false
	}
}

func readPayloads(body io.Reader) ([]*commonpb.Payload, error) {
	if body == nil {
		return nil, errors.New("empty request body")
	}
	bs, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	var msg commonpb.Payloads
	if err := protojson.Unmarshal(bs, &msg); err != nil {
		return nil, fmt.Errorf("unmarshal payloads: %w", err)
	}
	return msg.GetPayloads(), nil
}

func writePayloads(w http.ResponseWriter, payloads []*commonpb.Payload) error {
	w.Header().Set("Content-Type", "application/json")
	// Temporal's reference handler uses encoding/json over the protobuf
	// struct rather than protojson. Match that to stay compatible with the
	// Cloud UI clients.
	return json.NewEncoder(w).Encode(commonpb.Payloads{Payloads: payloads})
}
