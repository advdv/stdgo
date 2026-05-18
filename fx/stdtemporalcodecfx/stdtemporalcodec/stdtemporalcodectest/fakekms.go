// Package stdtemporalcodectest provides test doubles for the
// stdtemporalcodec package.
package stdtemporalcodectest

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
)

// FakeKMS is an in-memory KMS implementation suitable for tests. It models
// envelope encryption with EncryptionContext binding: data keys encrypted
// with a given context can only be decrypted by providing the same context.
//
// It is safe for concurrent use.
type FakeKMS struct {
	// MasterKey is the symmetric secret used to derive wrapped data keys.
	// A random value is used if left empty.
	MasterKey []byte

	// GenerateCalls and DecryptCalls count the number of times each method
	// was invoked. Useful for assertions in tests.
	GenerateCalls atomic.Int64
	DecryptCalls  atomic.Int64

	// FailNextGenerateDataKey causes the next GenerateDataKey call to fail
	// with the given error and then reset to nil.
	FailNextGenerateDataKey error

	// FailNextDecrypt causes the next Decrypt call to fail with the given
	// error and then reset to nil.
	FailNextDecrypt error

	mu      sync.Mutex
	once    sync.Once
	counter uint64
}

// NewFakeKMS returns a FakeKMS with a randomly generated master key.
func NewFakeKMS() *FakeKMS {
	master := make([]byte, 32)
	if _, err := rand.Read(master); err != nil {
		panic(fmt.Errorf("fakekms: read master key: %w", err))
	}
	return &FakeKMS{MasterKey: master}
}

// GenerateDataKey returns a 32-byte data key whose ciphertext blob encodes
// the encryption context and the plaintext key, authenticated with the
// master key.
//
// Blob layout:
//
//	| 8 byte seq | 1 byte ctxLen | ctx | 32 byte plaintext | 32 byte hmac |
//
// The HMAC covers seq||ctx||plaintext using the master key. Decrypt verifies
// the HMAC and the provided context.
func (f *FakeKMS) GenerateDataKey(
	_ context.Context,
	input *kms.GenerateDataKeyInput,
	_ ...func(*kms.Options),
) (*kms.GenerateDataKeyOutput, error) {
	f.init()
	f.GenerateCalls.Add(1)
	if err := f.FailNextGenerateDataKey; err != nil {
		f.FailNextGenerateDataKey = nil
		return nil, err
	}
	if input.KeySpec != types.DataKeySpecAes256 {
		return nil, fmt.Errorf("fakekms: unsupported key spec %q", input.KeySpec)
	}

	plaintext := make([]byte, 32)
	if _, err := rand.Read(plaintext); err != nil {
		return nil, fmt.Errorf("fakekms: read data key: %w", err)
	}

	blob, err := f.wrap(plaintext, input.EncryptionContext)
	if err != nil {
		return nil, err
	}

	return &kms.GenerateDataKeyOutput{
		KeyId:          aws.String(aws.ToString(input.KeyId)),
		Plaintext:      plaintext,
		CiphertextBlob: blob,
	}, nil
}

// Decrypt returns the plaintext data key, validating the encryption context.
func (f *FakeKMS) Decrypt(
	_ context.Context,
	input *kms.DecryptInput,
	_ ...func(*kms.Options),
) (*kms.DecryptOutput, error) {
	f.init()
	f.DecryptCalls.Add(1)
	if err := f.FailNextDecrypt; err != nil {
		f.FailNextDecrypt = nil
		return nil, err
	}

	plaintext, err := f.unwrap(input.CiphertextBlob, input.EncryptionContext)
	if err != nil {
		return nil, err
	}
	return &kms.DecryptOutput{Plaintext: plaintext}, nil
}

func (f *FakeKMS) init() {
	f.once.Do(func() {
		if len(f.MasterKey) == 0 {
			f.MasterKey = make([]byte, 32)
			if _, err := rand.Read(f.MasterKey); err != nil {
				panic(fmt.Errorf("fakekms: read master key: %w", err))
			}
		}
	})
}

func (f *FakeKMS) wrap(plaintext []byte, ctx map[string]string) ([]byte, error) {
	if len(plaintext) != 32 {
		return nil, errors.New("fakekms: plaintext must be 32 bytes")
	}
	ctxBytes := serializeContext(ctx)
	if len(ctxBytes) > 255 {
		return nil, errors.New("fakekms: encryption context too large")
	}

	f.mu.Lock()
	f.counter++
	seq := f.counter
	f.mu.Unlock()

	out := make([]byte, 0, 8+1+len(ctxBytes)+32+sha256.Size)
	var seqb [8]byte
	binary.BigEndian.PutUint64(seqb[:], seq)
	out = append(out, seqb[:]...)
	//nolint:gosec // bounds-checked above.
	out = append(out, byte(len(ctxBytes)))
	out = append(out, ctxBytes...)
	out = append(out, plaintext...)
	mac := f.computeMAC(seqb[:], ctxBytes, plaintext)
	out = append(out, mac...)
	return out, nil
}

func (f *FakeKMS) unwrap(blob []byte, ctx map[string]string) ([]byte, error) {
	if len(blob) < 8+1+32+sha256.Size {
		return nil, errors.New("fakekms: ciphertext too short")
	}
	seq := blob[:8]
	ctxLen := int(blob[8])
	rest := blob[9:]
	if len(rest) < ctxLen+32+sha256.Size {
		return nil, errors.New("fakekms: ciphertext truncated")
	}
	storedCtx := rest[:ctxLen]
	plaintext := rest[ctxLen : ctxLen+32]
	mac := rest[ctxLen+32 : ctxLen+32+sha256.Size]

	wantCtx := serializeContext(ctx)
	if !hmac.Equal(storedCtx, wantCtx) {
		return nil, errors.New("fakekms: encryption context mismatch")
	}
	expectMAC := f.computeMAC(seq, storedCtx, plaintext)
	if !hmac.Equal(mac, expectMAC) {
		return nil, errors.New("fakekms: hmac verification failed")
	}
	cp := make([]byte, len(plaintext))
	copy(cp, plaintext)
	return cp, nil
}

func (f *FakeKMS) computeMAC(seq, ctxBytes, plaintext []byte) []byte {
	mac := hmac.New(sha256.New, f.MasterKey)
	mac.Write(seq)
	//nolint:gosec // ctxBytes length is bounded to 255 by callers.
	mac.Write([]byte{byte(len(ctxBytes))})
	mac.Write(ctxBytes)
	mac.Write(plaintext)
	return mac.Sum(nil)
}

// serializeContext produces a stable, length-prefixed encoding of the
// encryption context map.
func serializeContext(ctx map[string]string) []byte {
	// Sort keys for determinism.
	keys := make([]string, 0, len(ctx))
	for k := range ctx {
		keys = append(keys, k)
	}
	// Simple selection sort; small map.
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	var buf []byte
	for _, k := range keys {
		v := ctx[k]
		//nolint:gosec // map keys/values in tests are small.
		buf = append(buf, byte(len(k)))
		buf = append(buf, k...)
		//nolint:gosec // bounded to map value size used in tests.
		buf = append(buf, byte(len(v)>>8), byte(len(v)))
		buf = append(buf, v...)
	}
	return buf
}
