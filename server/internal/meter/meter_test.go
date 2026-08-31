package meter_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/tomte/server/internal/meter"
	"github.com/gambtho/tomte/server/internal/proxy"
	"github.com/gambtho/tomte/server/internal/store"
	"github.com/gambtho/tomte/server/internal/testpg"
)

var testKEK = []byte("test-wrapped-kek")

func TestMeterMonthBoundaryAndCaps(t *testing.T) {
	pool := testpg.New(t)
	s := store.New(pool)
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "acme", testKEK)
	require.NoError(t, err)
	user, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)
	wf, _, err := s.CreateWorkflow(ctx, tn.ID, "wf", store.VersionDoc{
		Steps:  testStepsDoc,
		Permit: []byte(`{"v":1,"llm":{"providers":["anthropic"]},"connections":{}}`),
		Rubric: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = s.ApproveVersion(ctx, tn.ID, wf.ID, 1, user.ID, testCompiledDoc)
	require.NoError(t, err)

	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

	// One failed-but-costed run this month (counts), one run last month
	// (does not count). Create+finalize each, then move finished_at.
	mkRun := func(cost int, finishedAt time.Time, status string) {
		id := uuid.New()
		_, err := s.CreateRun(ctx, tn.ID, wf.ID, id, 1, "h", "manual", nil)
		require.NoError(t, err)
		_, err = s.FinalizeRun(ctx, tn.ID, id, store.RunFinal{Status: status, CostCents: cost}, 0)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `UPDATE run SET finished_at = $2 WHERE id = $1`, id, finishedAt)
		require.NoError(t, err)
	}
	mkRun(30, now.Add(-24*time.Hour), "failed")    // this month, failed: counts
	mkRun(100, now.AddDate(0, -1, 0), "succeeded") // last month: excluded

	m := &meter.Meter{Store: s, DefaultCap: 50, Now: func() time.Time { return now }}

	over, err := m.OverCap(ctx, tn.ID)
	require.NoError(t, err)
	require.False(t, over) // 30 < 50

	mkRun(25, now.Add(-time.Hour), "succeeded") // 55 total this month
	over, err = m.OverCap(ctx, tn.ID)
	require.NoError(t, err)
	require.True(t, over)

	// Hook contract: 429 with the typed error.
	err = m.Before(ctx, proxy.HookRequest{Identity: proxy.RunIdentity{TenantID: tn.ID}, Provider: "anthropic"})
	var he proxy.HookError
	require.ErrorAs(t, err, &he)
	require.Equal(t, http.StatusTooManyRequests, he.Status)

	// Tenant override beats the default.
	bigCap := 1000
	require.NoError(t, s.SetTenantMonthlyCap(ctx, tn.ID, &bigCap))
	require.NoError(t, m.Before(ctx, proxy.HookRequest{Identity: proxy.RunIdentity{TenantID: tn.ID}, Provider: "anthropic"}))

	// Zero cap = unlimited.
	zero := 0
	require.NoError(t, s.SetTenantMonthlyCap(ctx, tn.ID, &zero))
	require.NoError(t, m.Before(ctx, proxy.HookRequest{Identity: proxy.RunIdentity{TenantID: tn.ID}, Provider: "anthropic"}))
}

func TestMeterFailsClosed(t *testing.T) {
	pool := testpg.New(t)
	s := store.New(pool)
	m := &meter.Meter{Store: s, DefaultCap: 50}
	// Unknown tenant: GetTenant errors -> deny with 403-class HookError.
	err := m.Before(context.Background(), proxy.HookRequest{Identity: proxy.RunIdentity{TenantID: uuid.New()}})
	var he proxy.HookError
	require.ErrorAs(t, err, &he)
	require.Equal(t, http.StatusForbidden, he.Status)
}
