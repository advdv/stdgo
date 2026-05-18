// Package stdtemporalcodec implements a Temporal converter.PayloadCodec
// that encrypts payloads using AWS KMS envelope encryption, together with an
// HTTP handler that exposes the codec over Temporal's remote codec contract.
//
// Tenant isolation is achieved by passing an EncryptionContext of
// {"namespace": <ns>} to KMS on both GenerateDataKey and Decrypt. KMS will
// refuse to decrypt if the namespace in the context does not match the one
// used at encryption time.
//
// Wire format of the encrypted payload data:
//
//	| uint32 wrappedLen (big-endian) | wrapped DEK | 12-byte nonce | AES-GCM ciphertext |
//
// The plaintext that is encrypted is the protobuf-marshaled commonpb.Payload
// (including its original metadata + data). Payloads that are not encoded with
// MetadataEncodingEncrypted pass through Decode unchanged.
package stdtemporalcodec

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"google.golang.org/protobuf/proto"
)

// KMS abstracts the subset of the AWS KMS API used by Codec so it can be
// mocked or faked in tests.
type KMS interface {
	GenerateDataKey(
		ctx context.Context,
		in *kms.GenerateDataKeyInput,
		optFns ...func(*kms.Options),
	) (*kms.GenerateDataKeyOutput, error)
	Decrypt(
		ctx context.Context,
		in *kms.DecryptInput,
		optFns ...func(*kms.Options),
	) (*kms.DecryptOutput, error)
}

// Metadata keys and well-known values used on commonpb.Payload to identify
// payloads that were encrypted by this codec.
const (
	// MetadataEncodingEncrypted is the value of the standard "encoding"
	// metadata key for payloads produced by this codec.
	MetadataEncodingEncrypted = "binary/encrypted"

	// MetadataKeyID is the metadata key holding the KMS key ARN/alias used to
	// produce the wrapped data encryption key.
	MetadataKeyID = "encryption-key-id"

	// MetadataCipher is the metadata key holding the cipher identifier used
	// to encrypt the payload.
	MetadataCipher = "encryption-cipher"

	// MetadataContextNamespace is the metadata key holding the value of the
	// KMS EncryptionContext "namespace" entry. It is captured for
	// observability; KMS itself enforces the binding at decrypt time.
	MetadataContextNamespace = "encryption-context-namespace"

	// CipherAES256GCM identifies AES-256-GCM with a 12-byte nonce.
	CipherAES256GCM = "AES_256_GCM"

	// encryptionContextNamespaceKey is the key used inside the KMS
	// EncryptionContext map.
	encryptionContextNamespaceKey = "namespace"

	// dekSize is the size of the data encryption key in bytes.
	dekSize = 32

	// nonceSize is the size of the AES-GCM nonce in bytes.
	nonceSize = 12

	// standard encoding metadata key on commonpb.Payload.
	metadataEncodingKey = "encoding"
)

// Options configures a Codec.
type Options struct {
	// KeyID is the KMS key ARN or alias used to generate data encryption
	// keys. Required.
	KeyID string

	// Namespace is the Temporal namespace that scopes the codec instance.
	// It is included in the KMS EncryptionContext on every operation.
	// Required.
	Namespace string
}

// Codec implements converter.PayloadCodec using KMS-backed envelope
// encryption scoped to a single Temporal namespace.
type Codec struct {
	kms  KMS
	opts Options
}

// Ensure Codec implements the SDK interface at compile time.
var _ converter.PayloadCodec = (*Codec)(nil)

// New constructs a Codec. It returns an error if required options are
// missing.
func New(k KMS, opts Options) (*Codec, error) {
	if k == nil {
		return nil, errors.New("stdtemporalcodec: KMS client is required")
	}
	if opts.KeyID == "" {
		return nil, errors.New("stdtemporalcodec: Options.KeyID is required")
	}
	if opts.Namespace == "" {
		return nil, errors.New("stdtemporalcodec: Options.Namespace is required")
	}
	return &Codec{kms: k, opts: opts}, nil
}

// Namespace returns the namespace this codec is scoped to.
func (c *Codec) Namespace() string { return c.opts.Namespace }

// KeyID returns the configured KMS key id.
func (c *Codec) KeyID() string { return c.opts.KeyID }

// WithNamespace returns a copy of the codec scoped to a different namespace.
// This is useful on the server side where the namespace is determined per
// request.
func (c *Codec) WithNamespace(ns string) *Codec {
	cp := *c
	cp.opts.Namespace = ns
	return &cp
}

// Encode encrypts each payload using a freshly-generated KMS data key and
// returns new payloads with metadata describing the encryption parameters.
func (c *Codec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	out := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		enc, err := c.encodeOne(context.Background(), p)
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
		dec, err := c.decodeOne(context.Background(), p)
		if err != nil {
			return nil, fmt.Errorf("decode payload %d: %w", i, err)
		}
		out[i] = dec
	}
	return out, nil
}

func (c *Codec) encodeOne(ctx context.Context, p *commonpb.Payload) (*commonpb.Payload, error) {
	plaintext, err := proto.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	dataKey, err := c.kms.GenerateDataKey(ctx, &kms.GenerateDataKeyInput{
		KeyId:             aws.String(c.opts.KeyID),
		KeySpec:           types.DataKeySpecAes256,
		EncryptionContext: c.encryptionContext(),
	})
	if err != nil {
		return nil, fmt.Errorf("kms generate data key: %w", err)
	}
	if len(dataKey.Plaintext) != dekSize {
		return nil, fmt.Errorf("kms returned data key of unexpected size %d", len(dataKey.Plaintext))
	}

	block, err := aes.NewCipher(dataKey.Plaintext)
	if err != nil {
		return nil, fmt.Errorf("new aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}

	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("read nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	data, err := packEnvelope(dataKey.CiphertextBlob, nonce, ciphertext)
	if err != nil {
		return nil, err
	}

	return &commonpb.Payload{
		Metadata: map[string][]byte{
			metadataEncodingKey:      []byte(MetadataEncodingEncrypted),
			MetadataKeyID:            []byte(c.opts.KeyID),
			MetadataCipher:           []byte(CipherAES256GCM),
			MetadataContextNamespace: []byte(c.opts.Namespace),
		},
		Data: data,
	}, nil
}

func (c *Codec) decodeOne(ctx context.Context, p *commonpb.Payload) (*commonpb.Payload, error) {
	wrapped, nonce, ciphertext, err := unpackEnvelope(p.GetData())
	if err != nil {
		return nil, err
	}

	dataKey, err := c.kms.Decrypt(ctx, &kms.DecryptInput{
		CiphertextBlob:    wrapped,
		EncryptionContext: c.encryptionContext(),
	})
	if err != nil {
		return nil, fmt.Errorf("kms decrypt data key: %w", err)
	}
	if len(dataKey.Plaintext) != dekSize {
		return nil, fmt.Errorf("kms returned data key of unexpected size %d", len(dataKey.Plaintext))
	}

	block, err := aes.NewCipher(dataKey.Plaintext)
	if err != nil {
		return nil, fmt.Errorf("new aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("aes-gcm open: %w", err)
	}

	out := &commonpb.Payload{}
	if err := proto.Unmarshal(plaintext, out); err != nil {
		return nil, fmt.Errorf("unmarshal payload: %w", err)
	}
	return out, nil
}

func (c *Codec) encryptionContext() map[string]string {
	return map[string]string{encryptionContextNamespaceKey: c.opts.Namespace}
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

// packEnvelope serializes the wire format.
func packEnvelope(wrappedDEK, nonce, ciphertext []byte) ([]byte, error) {
	if uint64(len(wrappedDEK)) > uint64(^uint32(0)) {
		return nil, fmt.Errorf("wrapped data key too large: %d", len(wrappedDEK))
	}
	if len(nonce) != nonceSize {
		return nil, fmt.Errorf("nonce has unexpected size %d", len(nonce))
	}
	out := make([]byte, 0, 4+len(wrappedDEK)+nonceSize+len(ciphertext))
	var hdr [4]byte
	//nolint:gosec // bounds-checked above.
	binary.BigEndian.PutUint32(hdr[:], uint32(len(wrappedDEK)))
	out = append(out, hdr[:]...)
	out = append(out, wrappedDEK...)
	out = append(out, nonce...)
	out = append(out, ciphertext...)
	return out, nil
}

// unpackEnvelope parses the wire format.
func unpackEnvelope(data []byte) (wrappedDEK, nonce, ciphertext []byte, err error) {
	if len(data) < 4 {
		return nil, nil, nil, fmt.Errorf("envelope too short: %d bytes", len(data))
	}
	wrappedLen := binary.BigEndian.Uint32(data[:4])
	rest := data[4:]
	if uint64(len(rest)) < uint64(wrappedLen)+nonceSize {
		return nil, nil, nil, fmt.Errorf("envelope truncated: wrappedLen=%d, rest=%d", wrappedLen, len(rest))
	}
	wrappedDEK = rest[:wrappedLen]
	nonce = rest[wrappedLen : wrappedLen+nonceSize]
	ciphertext = rest[wrappedLen+nonceSize:]
	return wrappedDEK, nonce, ciphertext, nil
}
