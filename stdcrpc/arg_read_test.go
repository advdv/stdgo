package stdcrpc_test

import (
	"testing"

	"github.com/advdv/stdgo/stdcrpc"
	"github.com/advdv/stdgo/stdlo"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestArgReadUUID(t *testing.T) {
	var ar stdcrpc.ArgRead
	uid1, ar := ar.UUID("invalid1")
	uid2, ar := ar.UUID("a03d1ab9-f506-4153-aXea-bec1ef3dd8e7")
	uid3, ar := ar.UUID("a03d1ab9-f506-4153-adea-bec1ef3dd8e7")

	require.Equal(t, uuid.Nil, uid1)
	require.Equal(t, uuid.Nil, uid2)
	require.Equal(t, "a03d1ab9-f506-4153-adea-bec1ef3dd8e7", uid3.String())

	err := ar.Error()
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid UUID length")
	require.ErrorContains(t, err, "invalid UUID format")
}

func TestArgReadUUIDp(t *testing.T) {
	var ar stdcrpc.ArgRead
	uid1, ar := ar.UUIDp(stdlo.ToPtr("invalid1"))
	uid2, ar := ar.UUIDp(stdlo.ToPtr("a03d1ab9-f506-4153-aXea-bec1ef3dd8e7"))
	uid3, ar := ar.UUIDp(stdlo.ToPtr("a03d1ab9-f506-4153-adea-bec1ef3dd8e7"))
	uid4, ar := ar.UUIDp(nil)

	require.Nil(t, uid1)
	require.Nil(t, uid2)
	require.NotNil(t, uid3)
	require.Equal(t, "a03d1ab9-f506-4153-adea-bec1ef3dd8e7", uid3.String())
	require.Nil(t, uid4)

	err := ar.Error()
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid UUID length")
	require.ErrorContains(t, err, "invalid UUID format")
}

func TestArgNoErr(t *testing.T) {
	var ar stdcrpc.ArgRead
	uid1, ar := ar.UUID("a03d1ab9-f506-4153-adea-bec1ef3dd8e7")
	uid2, ar := ar.UUIDp(stdlo.ToPtr("a03d1ab9-f506-4153-adea-bec1ef3dd8e8"))
	err := ar.Error()
	require.NoError(t, err)

	require.Equal(t, "a03d1ab9-f506-4153-adea-bec1ef3dd8e7", uid1.String())
	require.Equal(t, "a03d1ab9-f506-4153-adea-bec1ef3dd8e8", uid2.String())
}
