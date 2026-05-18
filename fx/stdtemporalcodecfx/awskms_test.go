package stdtemporalcodecfx_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/advdv/stdgo/fx/stdtemporalcodecfx"
	"github.com/advdv/stdgo/fx/stdtemporalcodecfx/stdtemporalcodec"
	"github.com/advdv/stdgo/fx/stdtemporalcodecfx/stdtemporalcodecfxmock"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tink-crypto/tink-go/v2/aead"
	"github.com/tink-crypto/tink-go/v2/keyset"
	"github.com/tink-crypto/tink-go/v2/tink"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

// testKEKURI is a fake-but-syntactically-valid AWS KMS KEK URI used by
// the AWS-KMS-wrapped keyset tests. The mock KMS we install honours it
// without making any real network call.
const testKEKURI = "aws-kms://arn:aws:kms:us-east-1:111122223333:key/abcd1234-ab12-cd34-ef56-abcdef123456"

// fakeKEK is an in-process Tink AEAD that stands in for an AWS KMS KEK.
// It's used both to pre-wrap a Tink DEK keyset (so we have a realistic
// wrapped blob to feed in as the *_KEYSET env var) and to drive the
// MockKMS Encrypt/Decrypt expectations so loadKeyset can unwrap it.
type fakeKEK struct{ aead tink.AEAD }

func newFakeKEK(t *testing.T) fakeKEK {
	t.Helper()
	h, err := keyset.NewHandle(aead.AES256GCMKeyTemplate())
	require.NoError(t, err)
	a, err := aead.New(h)
	require.NoError(t, err)
	return fakeKEK{aead: a}
}

// wrapTinkKeyset returns a base64-encoded JSON tink keyset, wrapped by
// the fake KEK. Mirrors what `genkeyset --kek-uri` produces in production.
func (f fakeKEK) wrapTinkKeyset(t *testing.T) string {
	t.Helper()
	dek, err := keyset.NewHandle(aead.AES256GCMKeyTemplate())
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, dek.Write(keyset.NewJSONWriter(&buf), f.aead))
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// installOn programs the given MockKMS so that Encrypt/Decrypt are routed
// through the fake KEK's in-process AEAD. The encryption-context (if any)
// is used as the AEAD additionalData, exactly as tink-go-awskms does on
// the wire.
func (f fakeKEK) installOn(m *stdtemporalcodecfxmock.MockKMS) {
	m.EXPECT().
		Encrypt(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, in *kms.EncryptInput, _ ...func(*kms.Options)) (*kms.EncryptOutput, error) {
			ad := encryptionContextBytes(in.EncryptionContext)
			ct, err := f.aead.Encrypt(in.Plaintext, ad)
			if err != nil {
				return nil, err
			}
			return &kms.EncryptOutput{KeyId: in.KeyId, CiphertextBlob: ct}, nil
		}).
		Maybe()

	m.EXPECT().
		Decrypt(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, in *kms.DecryptInput, _ ...func(*kms.Options)) (*kms.DecryptOutput, error) {
			ad := encryptionContextBytes(in.EncryptionContext)
			pt, err := f.aead.Decrypt(in.CiphertextBlob, ad)
			if err != nil {
				return nil, err
			}
			out := &kms.DecryptOutput{Plaintext: pt}
			if in.KeyId != nil {
				out.KeyId = aws.String(*in.KeyId)
			}
			return out, nil
		}).
		Maybe()
}

// encryptionContextBytes serializes a KMS EncryptionContext to a single
// deterministic byte slice. It just needs to match between Encrypt and
// Decrypt; tink-go-awskms sets only one key ("associatedData" → hex(ad))
// so concatenating its sorted entries is sufficient.
func encryptionContextBytes(ec map[string]string) []byte {
	if len(ec) == 0 {
		return nil
	}
	keys := make([]string, 0, len(ec))
	for k := range ec {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b bytes.Buffer
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(ec[k])
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// provideMockKMS supplies the MockKMS to the fx graph under the KMS
// interface so loadKeyset picks it up instead of constructing a real
// *kms.Client from the ambient AWS config.
func provideMockKMS(m *stdtemporalcodecfxmock.MockKMS) fx.Option {
	return fx.Provide(func() stdtemporalcodecfx.KMS { return m })
}

func TestProvide_Enabled_AWSKMSBackend_RoundTrip(t *testing.T) {
	t.Parallel()

	kek := newFakeKEK(t)
	mockKMS := stdtemporalcodecfxmock.NewMockKMS(t)
	kek.installOn(mockKMS)

	env := map[string]string{
		"STDTEMPORALCODEC_ENABLED":        "true",
		"STDTEMPORALCODEC_KEYSET":         kek.wrapTinkKeyset(t),
		"STDTEMPORALCODEC_KEYSET_KEK_URI": testKEKURI,
		"STDTEMPORALCODEC_NAMESPACE":      "tenant-a",
	}

	var dc converter.DataConverter
	app := fxtest.New(t,
		stdenvcfg.ProvideExplicitEnvironment(env),
		stdzapfx.TestProvide(t),
		provideMockKMS(mockKMS),
		stdtemporalcodecfx.Provide(),
		fx.Populate(&dc),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	require.NotNil(t, dc)

	payloads, err := dc.ToPayloads("kms-wrapped-secret")
	require.NoError(t, err)
	require.NotEmpty(t, payloads.GetPayloads())
	for _, p := range payloads.GetPayloads() {
		assert.Equal(t, []byte("binary/encrypted"), p.GetMetadata()["encoding"])
		assert.Equal(t, []byte("tenant-a"), p.GetMetadata()[stdtemporalcodec.MetadataContextNamespace])
	}

	var got string
	require.NoError(t, dc.FromPayloads(payloads, &got))
	require.Equal(t, "kms-wrapped-secret", got)
}

func TestProvide_Enabled_AWSKMSBackend_KMSFailureFailsStartup(t *testing.T) {
	t.Parallel()

	mockKMS := stdtemporalcodecfxmock.NewMockKMS(t)
	// Decrypt is the path exercised at startup (we already have a wrapped
	// keyset to unwrap); fail it and observe the graph refuse to come up.
	mockKMS.EXPECT().
		Decrypt(mock.Anything, mock.Anything).
		Return(nil, errors.New("kms is on fire")).
		Maybe()
	mockKMS.EXPECT().
		Encrypt(mock.Anything, mock.Anything).
		Return(nil, errors.New("kms is on fire")).
		Maybe()

	// We still need a syntactically valid base64-encoded blob to even
	// reach the Decrypt call.
	kek := newFakeKEK(t)
	env := map[string]string{
		"STDTEMPORALCODEC_ENABLED":        "true",
		"STDTEMPORALCODEC_KEYSET":         kek.wrapTinkKeyset(t),
		"STDTEMPORALCODEC_KEYSET_KEK_URI": testKEKURI,
		"STDTEMPORALCODEC_NAMESPACE":      "tenant-a",
	}

	var dc converter.DataConverter
	app := fx.New(
		fx.NopLogger,
		stdenvcfg.ProvideExplicitEnvironment(env),
		stdzapfx.TestProvide(t),
		provideMockKMS(mockKMS),
		stdtemporalcodecfx.Provide(),
		fx.Populate(&dc),
	)
	require.Error(t, app.Err())
}

func TestProvide_Enabled_RejectsUnsupportedKEKURIScheme(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"STDTEMPORALCODEC_ENABLED":        "true",
		"STDTEMPORALCODEC_KEYSET":         freshKeyset(t),
		"STDTEMPORALCODEC_KEYSET_KEK_URI": "gcp-kms://does-not-exist",
		"STDTEMPORALCODEC_NAMESPACE":      "tenant-a",
	}

	var dc converter.DataConverter
	app := fx.New(
		fx.NopLogger,
		stdenvcfg.ProvideExplicitEnvironment(env),
		stdzapfx.TestProvide(t),
		stdtemporalcodecfx.Provide(),
		fx.Populate(&dc),
	)
	require.Error(t, app.Err())
}

func TestProvideServer_AWSKMSBackend_RoundTrip(t *testing.T) {
	t.Parallel()

	kek := newFakeKEK(t)
	mockKMS := stdtemporalcodecfxmock.NewMockKMS(t)
	kek.installOn(mockKMS)

	env := map[string]string{
		"STDTEMPORALCODECSERVER_ENABLED":            "true",
		"STDTEMPORALCODECSERVER_KEYSET":             kek.wrapTinkKeyset(t),
		"STDTEMPORALCODECSERVER_KEYSET_KEK_URI":     testKEKURI,
		"STDTEMPORALCODECSERVER_ALLOWED_NAMESPACES": "tenant-a",
	}

	var deps struct {
		fx.In
		Handler http.Handler `name:"codec"`
	}
	app := fxtest.New(t,
		stdenvcfg.ProvideExplicitEnvironment(env),
		stdzapfx.TestProvide(t),
		provideMockKMS(mockKMS),
		stdtemporalcodecfx.ProvideServer(),
		fx.Populate(&deps),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	require.NotNil(t, deps.Handler)

	srv := httptest.NewServer(deps.Handler)
	t.Cleanup(srv.Close)

	input := []*commonpb.Payload{{
		Metadata: map[string][]byte{"encoding": []byte("json/plain")},
		Data:     []byte(`{"hello":"kms"}`),
	}}
	encoded := roundTrip(t, srv, "/encode", "tenant-a", input)
	require.Len(t, encoded, 1)
	assert.Equal(t, []byte("binary/encrypted"), encoded[0].GetMetadata()["encoding"])

	decoded := roundTrip(t, srv, "/decode", "tenant-a", encoded)
	require.Len(t, decoded, 1)
	assert.Equal(t, input[0].GetData(), decoded[0].GetData())
}

// TestAWSKMSBackend_KMSWasActuallyCalled asserts the mock saw the
// startup Decrypt call. This is the load-bearing piece of the backend
// selection: a wrapped keyset must traverse the KMS path, not silently
// fall through to insecurecleartextkeyset.Read.
func TestAWSKMSBackend_KMSWasActuallyCalled(t *testing.T) {
	t.Parallel()

	kek := newFakeKEK(t)
	mockKMS := stdtemporalcodecfxmock.NewMockKMS(t)
	// Pin the Decrypt expectation (Times >= 1) and drop the Maybe() that
	// installOn uses, so the test fails loudly if KMS were skipped.
	mockKMS.EXPECT().
		Decrypt(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, in *kms.DecryptInput, _ ...func(*kms.Options)) (*kms.DecryptOutput, error) {
			ad := encryptionContextBytes(in.EncryptionContext)
			pt, err := kek.aead.Decrypt(in.CiphertextBlob, ad)
			if err != nil {
				return nil, err
			}
			return &kms.DecryptOutput{Plaintext: pt, KeyId: in.KeyId}, nil
		}).
		Once()

	env := map[string]string{
		"STDTEMPORALCODEC_ENABLED":        "true",
		"STDTEMPORALCODEC_KEYSET":         kek.wrapTinkKeyset(t),
		"STDTEMPORALCODEC_KEYSET_KEK_URI": testKEKURI,
		"STDTEMPORALCODEC_NAMESPACE":      "ns",
	}

	var dc converter.DataConverter
	app := fxtest.New(t,
		stdenvcfg.ProvideExplicitEnvironment(env),
		stdzapfx.TestProvide(t),
		provideMockKMS(mockKMS),
		stdtemporalcodecfx.Provide(),
		fx.Populate(&dc),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	require.NotNil(t, dc)
}
