package stdenttxfx_test

import (
	"context"
	"testing"

	"github.com/advdv/stdgo/fx/stdenttxfx/testdata/model"
	"github.com/advdv/stdgo/stdent"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest/observer"
)

func TestUser(t *testing.T) {
	t.Parallel()

	var obs *observer.ObservedLogs
	ctx, _, rw := setup(t, &obs)

	require.NoError(t, stdent.Transact0(ctx, rw, func(ctx context.Context, tx *model.Tx) error {
		return nil
	}))

	require.Equal(t, 1, obs.FilterMessage("hook called").Len())
}
