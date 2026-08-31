package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/tomte/server/internal/store"
	"github.com/gambtho/tomte/server/internal/testpg"
)

func TestSchedulerHeartbeatRoundtrip(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()

	// Never ticked: nil, no error.
	got, err := s.GetSchedulerHeartbeat(ctx)
	require.NoError(t, err)
	require.Nil(t, got)

	first := time.Date(2026, 9, 7, 3, 0, 0, 0, time.UTC)
	require.NoError(t, s.SetSchedulerHeartbeat(ctx, first))
	got, err = s.GetSchedulerHeartbeat(ctx)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.True(t, got.Equal(first))

	// Single row: a later set overwrites, never accumulates.
	second := first.Add(time.Minute)
	require.NoError(t, s.SetSchedulerHeartbeat(ctx, second))
	got, err = s.GetSchedulerHeartbeat(ctx)
	require.NoError(t, err)
	require.True(t, got.Equal(second))
}
