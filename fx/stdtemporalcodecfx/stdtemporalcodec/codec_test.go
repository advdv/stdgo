package stdtemporalcodec_test

import (
	"errors"
	"testing"

	"github.com/advdv/stdgo/fx/stdtemporalcodecfx/stdtemporalcodec"
	"github.com/advdv/stdgo/fx/stdtemporalcodecfx/stdtemporalcodec/stdtemporalcodectest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
)

func TestNew_Validates(t *testing.T) {
	t.Parallel()
	fk := stdtemporalcodectest.NewFakeKMS()

	_, err := stdtemporalcodec.New(nil, stdtemporalcodec.Options{KeyID: "k", Namespace: "n"})
	require.Error(t, err)

	_, err = stdtemporalcodec.New(fk, stdtemporalcodec.Options{Namespace: "n"})
	require.Error(t, err)

	_, err = stdtemporalcodec.New(fk, stdtemporalcodec.Options{KeyID: "k"})
	require.Error(t, err)

	c, err := stdtemporalcodec.New(fk, stdtemporalcodec.Options{KeyID: "k", Namespace: "n"})
	require.NoError(t, err)
	require.Equal(t, "k", c.KeyID())
	require.Equal(t, "n", c.Namespace())
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	fk := stdtemporalcodectest.NewFakeKMS()
	c, err := stdtemporalcodec.New(fk, stdtemporalcodec.Options{
		KeyID:     "arn:aws:kms:us-east-1:123:key/abc",
		Namespace: "tenant-a",
	})
	require.NoError(t, err)

	payloads := []*commonpb.Payload{
		{
			Metadata: map[string][]byte{"encoding": []byte("json/plain")},
			Data:     []byte(`{"hello":"world"}`),
		},
		{
			Metadata: map[string][]byte{"encoding": []byte("binary/null")},
			Data:     nil,
		},
	}

	encoded, err := c.Encode(payloads)
	require.NoError(t, err)
	require.Len(t, encoded, 2)

	for i, ep := range encoded {
		assert.Equal(t, []byte("binary/encrypted"), ep.GetMetadata()["encoding"], "payload %d encoding", i)
		assert.Equal(t, []byte("arn:aws:kms:us-east-1:123:key/abc"), ep.GetMetadata()[stdtemporalcodec.MetadataKeyID])
		assert.Equal(t, []byte("AES_256_GCM"), ep.GetMetadata()[stdtemporalcodec.MetadataCipher])
		assert.Equal(t, []byte("tenant-a"), ep.GetMetadata()[stdtemporalcodec.MetadataContextNamespace])
		assert.NotEqual(t, payloads[i].GetData(), ep.GetData())
	}

	decoded, err := c.Decode(encoded)
	require.NoError(t, err)
	require.Len(t, decoded, 2)
	for i := range decoded {
		assert.Equal(t, payloads[i].GetMetadata()["encoding"], decoded[i].GetMetadata()["encoding"])
		assert.Equal(t, payloads[i].GetData(), decoded[i].GetData())
	}
}

func TestDecode_PassesThroughUnencryptedPayloads(t *testing.T) {
	t.Parallel()
	fk := stdtemporalcodectest.NewFakeKMS()
	c, err := stdtemporalcodec.New(fk, stdtemporalcodec.Options{KeyID: "k", Namespace: "ns"})
	require.NoError(t, err)

	in := []*commonpb.Payload{{
		Metadata: map[string][]byte{"encoding": []byte("json/plain")},
		Data:     []byte(`"hello"`),
	}}

	out, err := c.Decode(in)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, in[0].GetData(), out[0].GetData())
	assert.Equal(t, int64(0), fk.DecryptCalls.Load(), "KMS should not be called for non-encrypted payloads")
}

func TestDecode_NamespaceMismatchFails(t *testing.T) {
	t.Parallel()
	fk := stdtemporalcodectest.NewFakeKMS()

	encCodec, err := stdtemporalcodec.New(fk, stdtemporalcodec.Options{KeyID: "k", Namespace: "tenant-a"})
	require.NoError(t, err)
	decCodec, err := stdtemporalcodec.New(fk, stdtemporalcodec.Options{KeyID: "k", Namespace: "tenant-b"})
	require.NoError(t, err)

	encoded, err := encCodec.Encode([]*commonpb.Payload{{
		Metadata: map[string][]byte{"encoding": []byte("json/plain")},
		Data:     []byte(`"secret"`),
	}})
	require.NoError(t, err)

	_, err = decCodec.Decode(encoded)
	require.Error(t, err, "decoding with a mismatched namespace must fail")
}

func TestWithNamespace_ReturnsScopedCopy(t *testing.T) {
	t.Parallel()
	fk := stdtemporalcodectest.NewFakeKMS()
	base, err := stdtemporalcodec.New(fk, stdtemporalcodec.Options{KeyID: "k", Namespace: "tenant-a"})
	require.NoError(t, err)

	scoped := base.WithNamespace("tenant-b")
	require.Equal(t, "tenant-a", base.Namespace())
	require.Equal(t, "tenant-b", scoped.Namespace())

	encoded, err := scoped.Encode([]*commonpb.Payload{{
		Metadata: map[string][]byte{"encoding": []byte("json/plain")},
		Data:     []byte(`"x"`),
	}})
	require.NoError(t, err)
	assert.Equal(t, []byte("tenant-b"), encoded[0].GetMetadata()[stdtemporalcodec.MetadataContextNamespace])

	// Decoding with the base codec (different namespace) must fail.
	_, err = base.Decode(encoded)
	require.Error(t, err)

	// Decoding with the scoped codec succeeds.
	decoded, err := scoped.Decode(encoded)
	require.NoError(t, err)
	assert.Equal(t, []byte(`"x"`), decoded[0].GetData())
}

func TestEncode_PropagatesKMSError(t *testing.T) {
	t.Parallel()
	fk := stdtemporalcodectest.NewFakeKMS()
	want := errors.New("simulated kms outage")
	fk.FailNextGenerateDataKey = want

	c, err := stdtemporalcodec.New(fk, stdtemporalcodec.Options{KeyID: "k", Namespace: "ns"})
	require.NoError(t, err)

	_, err = c.Encode([]*commonpb.Payload{{Data: []byte("x")}})
	require.ErrorIs(t, err, want)
}

func TestDecode_PropagatesKMSError(t *testing.T) {
	t.Parallel()
	fk := stdtemporalcodectest.NewFakeKMS()
	c, err := stdtemporalcodec.New(fk, stdtemporalcodec.Options{KeyID: "k", Namespace: "ns"})
	require.NoError(t, err)

	encoded, err := c.Encode([]*commonpb.Payload{{Data: []byte("x")}})
	require.NoError(t, err)

	want := errors.New("simulated kms outage")
	fk.FailNextDecrypt = want

	_, err = c.Decode(encoded)
	require.ErrorIs(t, err, want)
}

func TestDecode_MalformedEnvelope(t *testing.T) {
	t.Parallel()
	fk := stdtemporalcodectest.NewFakeKMS()
	c, err := stdtemporalcodec.New(fk, stdtemporalcodec.Options{KeyID: "k", Namespace: "ns"})
	require.NoError(t, err)

	in := []*commonpb.Payload{{
		Metadata: map[string][]byte{"encoding": []byte("binary/encrypted")},
		Data:     []byte("too short"),
	}}
	_, err = c.Decode(in)
	require.Error(t, err)
}
