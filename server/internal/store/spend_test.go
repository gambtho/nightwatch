package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/tomte/server/internal/store"
	"github.com/gambtho/tomte/server/internal/testpg"
)

func TestSpendLedgerCountsAgainstMonth(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "acme", testKEK)
	require.NoError(t, err)

	monthStart := time.Now().UTC().AddDate(0, 0, -1)
	spent, err := s.MonthSpendCents(ctx, tn.ID, monthStart)
	require.NoError(t, err)
	require.Zero(t, spent)

	// The paste-time verify call's cost is a non-run spend: it must count
	// against the month once a budget exists (pivot spec, "First run").
	err = s.RecordSpend(ctx, tn.ID, "endpoint_verify", 1, 12, 1, "https://api.example.com", "some-model")
	require.NoError(t, err)
	err = s.RecordSpend(ctx, tn.ID, "endpoint_verify", 2, 30, 1, "https://api.example.com", "some-model")
	require.NoError(t, err)

	spent, err = s.MonthSpendCents(ctx, tn.ID, monthStart)
	require.NoError(t, err)
	require.Equal(t, 3, spent)

	// Tenant-scoped: another tenant's month is untouched.
	other, err := s.CreateTenant(ctx, "other", testKEK)
	require.NoError(t, err)
	spent, err = s.MonthSpendCents(ctx, other.ID, monthStart)
	require.NoError(t, err)
	require.Zero(t, spent)
}

func TestRecordSpendRejectsUnknownKind(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "acme", testKEK)
	require.NoError(t, err)
	err = s.RecordSpend(ctx, tn.ID, "surprise", 1, 0, 0, "https://x", "m")
	require.Error(t, err)
}
