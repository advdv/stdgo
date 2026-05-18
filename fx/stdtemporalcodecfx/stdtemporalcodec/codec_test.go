package stdtemporalcodec_test

import (
	"testing"

	"github.com/advdv/stdgo/fx/stdtemporalcodecfx/stdtemporalcodec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tink-crypto/tink-go/v2/aead"
	"github.com/tink-crypto/tink-go/v2/keyset"
	commonpb "go.temporal.io/api/common/v1"
)

func freshKeyset(t *testing.T) *keyset.Handle {
	t.Helper()
	h, err := keyset.NewHandle(aead.AES256GCMKeyTemplate())
	require.NoError(t, err)
	return h
}

func TestNew_Validates(t *testing.T) {
	t.Parallel()

	_, err := stdtemporalcodec.New(stdtemporalcodec.Options{Namespace: "n"})
	require.Error(t, err, "missing keyset must error")

	_, err = stdtemporalcodec.New(stdtemporalcodec.Options{Keyset: freshKeyset(t)})
	require.Error(t, err, "missing namespace must error")

	c, err := stdtemporalcodec.New(stdtemporalcodec.Options{
		Keyset:    freshKeyset(t),
		Namespace: "n",
	})
	require.NoError(t, err)
	require.Equal(t, "n", c.Namespace())
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	c, err := stdtemporalcodec.New(stdtemporalcodec.Options{
		Keyset:    freshKeyset(t),
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
	c, err := stdtemporalcodec.New(stdtemporalcodec.Options{Keyset: freshKeyset(t), Namespace: "ns"})
	require.NoError(t, err)

	in := []*commonpb.Payload{{
		Metadata: map[string][]byte{"encoding": []byte("json/plain")},
		Data:     []byte(`"hello"`),
	}}

	out, err := c.Decode(in)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, in[0].GetData(), out[0].GetData())
}

func TestDecode_NamespaceMismatchFails(t *testing.T) {
	t.Parallel()
	ks := freshKeyset(t)

	encCodec, err := stdtemporalcodec.New(stdtemporalcodec.Options{Keyset: ks, Namespace: "tenant-a"})
	require.NoError(t, err)
	decCodec, err := stdtemporalcodec.New(stdtemporalcodec.Options{Keyset: ks, Namespace: "tenant-b"})
	require.NoError(t, err)

	encoded, err := encCodec.Encode([]*commonpb.Payload{{
		Metadata: map[string][]byte{"encoding": []byte("json/plain")},
		Data:     []byte(`"secret"`),
	}})
	require.NoError(t, err)

	_, err = decCodec.Decode(encoded)
	require.Error(t, err, "decoding with a mismatched namespace must fail")
}

func TestDecode_WrongKeysetFails(t *testing.T) {
	t.Parallel()

	enc, err := stdtemporalcodec.New(stdtemporalcodec.Options{Keyset: freshKeyset(t), Namespace: "ns"})
	require.NoError(t, err)
	dec, err := stdtemporalcodec.New(stdtemporalcodec.Options{Keyset: freshKeyset(t), Namespace: "ns"})
	require.NoError(t, err)

	encoded, err := enc.Encode([]*commonpb.Payload{{Data: []byte("x")}})
	require.NoError(t, err)

	_, err = dec.Decode(encoded)
	require.Error(t, err, "decoding with a different keyset must fail")
}

func TestWithNamespace_ReturnsScopedCopy(t *testing.T) {
	t.Parallel()
	base, err := stdtemporalcodec.New(stdtemporalcodec.Options{Keyset: freshKeyset(t), Namespace: "tenant-a"})
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

func TestDecode_MalformedCiphertext(t *testing.T) {
	t.Parallel()
	c, err := stdtemporalcodec.New(stdtemporalcodec.Options{Keyset: freshKeyset(t), Namespace: "ns"})
	require.NoError(t, err)

	in := []*commonpb.Payload{{
		Metadata: map[string][]byte{"encoding": []byte("binary/encrypted")},
		Data:     []byte("too short"),
	}}
	_, err = c.Decode(in)
	require.Error(t, err)
}

// TestRotation_OldCiphertextDecryptsUnderRotatedKeyset exercises the core
// Tink rotation property: a payload encrypted under primary key K1 must
// continue to decrypt after K2 has been added and promoted to primary.
func TestRotation_OldCiphertextDecryptsUnderRotatedKeyset(t *testing.T) {
	t.Parallel()

	// Build a keyset with a single primary key (K1) and encrypt under it.
	ks1 := freshKeyset(t)
	c1, err := stdtemporalcodec.New(stdtemporalcodec.Options{Keyset: ks1, Namespace: "tenant-a"})
	require.NoError(t, err)
	encoded, err := c1.Encode([]*commonpb.Payload{{
		Metadata: map[string][]byte{"encoding": []byte("json/plain")},
		Data:     []byte(`"under-k1"`),
	}})
	require.NoError(t, err)

	// Build a rotated keyset: copy K1 in, then add and promote K2.
	mgr := keyset.NewManagerFromHandle(ks1)
	k2ID, err := mgr.Add(aead.AES256GCMKeyTemplate())
	require.NoError(t, err)
	require.NoError(t, mgr.SetPrimary(k2ID))
	ks2, err := mgr.Handle()
	require.NoError(t, err)

	// Rotated codec decrypts the K1 ciphertext just fine.
	c2, err := stdtemporalcodec.New(stdtemporalcodec.Options{Keyset: ks2, Namespace: "tenant-a"})
	require.NoError(t, err)
	decoded, err := c2.Decode(encoded)
	require.NoError(t, err)
	require.Len(t, decoded, 1)
	assert.Equal(t, []byte(`"under-k1"`), decoded[0].GetData())

	// New ciphertexts go through too.
	encoded2, err := c2.Encode([]*commonpb.Payload{{
		Metadata: map[string][]byte{"encoding": []byte("json/plain")},
		Data:     []byte(`"under-k2"`),
	}})
	require.NoError(t, err)
	decoded2, err := c2.Decode(encoded2)
	require.NoError(t, err)
	assert.Equal(t, []byte(`"under-k2"`), decoded2[0].GetData())
}
