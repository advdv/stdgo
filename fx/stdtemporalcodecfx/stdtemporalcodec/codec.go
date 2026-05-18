// Package stdtemporalcodec implements a Temporal converter.PayloadCodec that
// encrypts payloads using a Google Tink AEAD primitive backed by an
// AES-256-GCM keyset, together with an HTTP handler that exposes the codec
// over Temporal's remote codec contract.
//
// Why Tink instead of a hand-rolled AES-GCM construction:
//
//   - Tink owns the on-the-wire ciphertext format (a 5-byte type-prefix that
//     embeds the key id, followed by iv|ct|tag). We don't have to define and
//     freeze our own byte layout.
//   - Tink keysets are first-class containers for key rotation: every keyset
//     has one primary key (used to encrypt) and any number of additional
//     keys (all tried on decrypt). Rotation is therefore the boring case,
//     not a special path.
//   - AES-256-GCM is still the underlying primitive; tenant isolation is
//     enforced by passing the Temporal namespace into the AEAD's
//     additionalData argument. A ciphertext produced for namespace A cannot
//     be decrypted under namespace B: GCM authentication will fail.
//
// Payloads that are not encoded with MetadataEncodingEncrypted pass through
// Decode unchanged.
//
// # Key rotation
//
// To rotate keys:
//
//  1. Add a new key to the Tink keyset (e.g. with tinkey or the
//     cmd/stdtemporalcodec-genkeyset helper in this repo).
//  2. Promote it to primary.
//  3. Ship the new base64-encoded cleartext keyset to every worker, client
//     and codec-server process and redeploy.
//
// New ciphertexts are produced under the new primary; ciphertexts produced
// under the previous primary continue to decrypt because the old key is
// still present in the keyset. Once Temporal history retention has expired
// for all payloads encrypted under the old key, it can be removed from the
// keyset.
package stdtemporalcodec

import (
	"errors"
	"fmt"

	"github.com/tink-crypto/tink-go/v2/aead"
	"github.com/tink-crypto/tink-go/v2/keyset"
	"github.com/tink-crypto/tink-go/v2/tink"
	commonpb "go.temporal.io/api/common/v1"
	"google.golang.org/protobuf/proto"
)

// Metadata keys and well-known values used on commonpb.Payload to identify
// payloads that were encrypted by this codec.
const (
	// MetadataEncodingEncrypted is the value of the standard "encoding"
	// metadata key for payloads produced by this codec.
	MetadataEncodingEncrypted = "binary/encrypted"

	// MetadataContextNamespace is the metadata key holding the Temporal
	// namespace this payload was encrypted for. It is captured for
	// observability; the AEAD additionalData binding enforces the
	// constraint at decrypt time.
	MetadataContextNamespace = "encryption-context-namespace"

	// standard encoding metadata key on commonpb.Payload.
	metadataEncodingKey = "encoding"
)

// Options configures a Codec.
type Options struct {
	// Keyset is the Tink keyset used to encrypt (with the primary key)
	// and decrypt (trying every key in the keyset). Required.
	Keyset *keyset.Handle

	// Namespace is the Temporal namespace that scopes the codec instance.
	// It is bound into the AEAD additionalData on every operation.
	// Required.
	Namespace string
}

// Codec implements converter.PayloadCodec using a Tink AEAD primitive
// (AES-256-GCM in the typical configuration) scoped to a single Temporal
// namespace.
type Codec struct {
	aead      tink.AEAD
	namespace string
}

// New constructs a Codec. It returns an error if required options are
// missing.
func New(opts Options) (*Codec, error) {
	if opts.Keyset == nil {
		return nil, errors.New("stdtemporalcodec: Options.Keyset is required")
	}
	if opts.Namespace == "" {
		return nil, errors.New("stdtemporalcodec: Options.Namespace is required")
	}
	primitive, err := aead.New(opts.Keyset)
	if err != nil {
		return nil, fmt.Errorf("stdtemporalcodec: build aead primitive: %w", err)
	}
	return &Codec{aead: primitive, namespace: opts.Namespace}, nil
}

// Namespace returns the namespace this codec is scoped to.
func (c *Codec) Namespace() string { return c.namespace }

// WithNamespace returns a copy of the codec scoped to a different namespace.
// This is useful on the server side where the namespace is determined per
// request. The underlying Tink AEAD primitive (and keyset) is shared.
func (c *Codec) WithNamespace(ns string) *Codec {
	cp := *c
	cp.namespace = ns
	return &cp
}

// Encode encrypts each payload via the Tink AEAD primitive and returns new
// payloads tagged as MetadataEncodingEncrypted.
func (c *Codec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	out := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		enc, err := c.encodeOne(p)
		if err != nil {
			return nil, fmt.Errorf("encode payload %d: %w", i, err)
		}
		out[i] = enc
	}
	return out, nil
}

// Decode decrypts each payload that bears MetadataEncodingEncrypted; any
// other payload is passed through unchanged.
func (c *Codec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	out := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		if !isEncrypted(p) {
			out[i] = p
			continue
		}
		dec, err := c.decodeOne(p)
		if err != nil {
			return nil, fmt.Errorf("decode payload %d: %w", i, err)
		}
		out[i] = dec
	}
	return out, nil
}

func (c *Codec) encodeOne(p *commonpb.Payload) (*commonpb.Payload, error) {
	plaintext, err := proto.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	ciphertext, err := c.aead.Encrypt(plaintext, c.aad())
	if err != nil {
		return nil, fmt.Errorf("aead encrypt: %w", err)
	}
	return &commonpb.Payload{
		Metadata: map[string][]byte{
			metadataEncodingKey:      []byte(MetadataEncodingEncrypted),
			MetadataContextNamespace: []byte(c.namespace),
		},
		Data: ciphertext,
	}, nil
}

func (c *Codec) decodeOne(p *commonpb.Payload) (*commonpb.Payload, error) {
	plaintext, err := c.aead.Decrypt(p.GetData(), c.aad())
	if err != nil {
		return nil, fmt.Errorf("aead decrypt: %w", err)
	}
	out := &commonpb.Payload{}
	if err := proto.Unmarshal(plaintext, out); err != nil {
		return nil, fmt.Errorf("unmarshal payload: %w", err)
	}
	return out, nil
}

// aad returns the additional authenticated data binding the namespace into
// the ciphertext. Decrypting under a different namespace will fail
// authentication.
func (c *Codec) aad() []byte {
	return []byte("namespace=" + c.namespace)
}

func isEncrypted(p *commonpb.Payload) bool {
	if p == nil {
		return false
	}
	md := p.GetMetadata()
	if md == nil {
		return false
	}
	return string(md[metadataEncodingKey]) == MetadataEncodingEncrypted
}
