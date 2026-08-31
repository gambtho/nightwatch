package proxyadapter_test

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/tomte/server/internal/proxyadapter"
	"github.com/gambtho/tomte/server/internal/store"
	"github.com/gambtho/tomte/server/internal/testpg"
	"github.com/gambtho/tomte/server/internal/token"
	"github.com/gambtho/tomte/server/internal/vault"
)

type env struct {
	pool   *pgxpool.Pool // raw pool: the status-guard test flips state store methods can't
	store  *store.Store
	signer *token.Signer
	master *vault.Master
	set    proxyadapter.Set
	tenant store.Tenant
	wf     store.Workflow
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testpg.New(t)
	s := store.New(pool)
	ctx := context.Background()

	vkey := make([]byte, vault.KeyLen)
	_, err := rand.Read(vkey)
	require.NoError(t, err)
	master, err := vault.NewMaster(vkey)
	require.NoError(t, err)
	wrapped, err := master.NewTenantKEK()
	require.NoError(t, err)

	tn, err := s.CreateTenant(ctx, "acme", wrapped)
	require.NoError(t, err)
	user, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)
	wf, _, err := s.CreateWorkflow(ctx, tn.ID, "digest", store.VersionDoc{
		Steps:  testStepsDoc,
		Permit: []byte(`{"v":1,"llm":{"providers":["anthropic"]},"connections":{}}`),
		Rubric: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = s.ApproveVersion(ctx, tn.ID, wf.ID, 1, user.ID, testCompiledDoc)
	require.NoError(t, err)

	signer := token.New([]byte("0123456789abcdef0123456789abcdef"))
	set := proxyadapter.New(s, signer, master, map[string]string{"anthropic": "platform-key"}, nil)
	return &env{pool: pool, store: s, signer: signer, master: master, set: set, tenant: tn, wf: wf}
}

func (e *env) mintRun(t *testing.T) (uuid.UUID, string) {
	t.Helper()
	runID := uuid.New()
	bearer, hash, err := e.signer.Sign(token.RunClaims{
		RunID: runID, TenantID: e.tenant.ID, ExpiresAt: time.Now().Add(time.Hour),
	})
	require.NoError(t, err)
	_, err = e.store.CreateRun(context.Background(), e.tenant.ID, e.wf.ID, runID, 1, hash, "manual", nil)
	require.NoError(t, err)
	return runID, bearer
}

func TestVerifyRunTokenLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	runID, bearer := e.mintRun(t)

	id, err := e.set.Auth.VerifyRunToken(ctx, bearer)
	require.NoError(t, err)
	require.Equal(t, runID, id.RunID)
	require.Equal(t, e.tenant.ID, id.TenantID)

	// A finalized run's token is dead: cleared hash AND inactive status.
	_, err = e.store.FinalizeRun(ctx, e.tenant.ID, runID, store.RunFinal{Status: "succeeded"}, 0)
	require.NoError(t, err)
	_, err = e.set.Auth.VerifyRunToken(ctx, bearer)
	require.Error(t, err)

	_, err = e.set.Auth.VerifyRunToken(ctx, "garbage")
	require.Error(t, err)
}

func TestVerifyRunTokenStatusGuardAlone(t *testing.T) {
	// Finalize clears the hash AND flips status, so it cannot isolate the
	// active-run guard. Flip status by direct SQL, hash intact: the guard
	// must reject on status alone.
	e := newEnv(t)
	ctx := context.Background()
	runID, bearer := e.mintRun(t)

	_, err := e.pool.Exec(ctx, `UPDATE run SET status = 'failed' WHERE id = $1`, runID)
	require.NoError(t, err)

	_, err = e.set.Auth.VerifyRunToken(ctx, bearer)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed")
}

func TestPermitForRun(t *testing.T) {
	e := newEnv(t)
	runID, _ := e.mintRun(t)
	p, err := e.set.Permits.PermitForRun(context.Background(), e.tenant.ID, runID)
	require.NoError(t, err)
	require.True(t, p.AllowsProvider("anthropic"))
	require.False(t, p.AllowsProvider("openai"))
}

func TestCredentialBYOKAndPlatformFallback(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	// No BYO connection: platform fallback for "default".
	sec, err := e.set.Credentials.Credential(ctx, e.tenant.ID, "default", "anthropic")
	require.NoError(t, err)
	require.Equal(t, "platform-key", sec.Value)

	// BYO default beats the platform key.
	wrapped, _, err := e.store.TenantKEK(ctx, e.tenant.ID)
	require.NoError(t, err)
	dek, ct, nonce, err := e.master.EncryptSecret(wrapped, "byo-key")
	require.NoError(t, err)
	_, err = e.store.UpsertConnection(ctx, e.tenant.ID, "default", "llm_api_key", "anthropic", dek, ct, nonce, 1)
	require.NoError(t, err)
	sec, err = e.set.Credentials.Credential(ctx, e.tenant.ID, "default", "anthropic")
	require.NoError(t, err)
	require.Equal(t, "byo-key", sec.Value)

	// A named (non-default) missing connection has no fallback.
	_, err = e.set.Credentials.Credential(ctx, e.tenant.ID, "work", "anthropic")
	require.Error(t, err)

	// A provider with neither connection nor platform key fails.
	_, err = e.set.Credentials.Credential(ctx, e.tenant.ID, "default", "openai")
	require.Error(t, err)
}

func TestAppendEvent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	runID, _ := e.mintRun(t)
	require.NoError(t, e.set.Events.AppendEvent(ctx, e.tenant.ID, runID, "proxy.request",
		map[string]any{"provider": "anthropic", "status": 200}))
	events, err := e.store.ListRunEvents(ctx, e.tenant.ID, runID)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "proxy.request", events[0].Type)
}
