// Package engine owns firing: the ONE path by which a run comes into
// existence and reaches an actor, shared by the HTTP API and the
// scheduler so the two can never drift. It also owns crash recovery
// (Redispatch) — the unique indexes give idempotent row creation; this
// package makes dispatch at-least-once on top of them.
package engine

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/gambtho/tomte/server/internal/compute"
	"github.com/gambtho/tomte/server/internal/store"
	"github.com/gambtho/tomte/server/internal/token"
)

type Engine struct {
	Store    *store.Store
	Signer   *token.Signer
	Compute  compute.Compute
	TokenTTL time.Duration
	Now      func() time.Time
}

func (e *Engine) ttl() time.Duration {
	if e.TokenTTL > 0 {
		return e.TokenTTL
	}
	return time.Hour
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Engine) Fire(ctx context.Context, tenantID, workflowID uuid.UUID, version int, fireReason string, fireTime *time.Time) (store.Run, error) {
	runID := uuid.New()
	bearer, hash, err := e.Signer.Sign(token.RunClaims{
		RunID: runID, TenantID: tenantID, ExpiresAt: e.now().Add(e.ttl()),
	})
	if err != nil {
		return store.Run{}, err
	}
	run, err := e.Store.CreateRun(ctx, tenantID, workflowID, runID, version, hash, fireReason, fireTime)
	if err != nil {
		return store.Run{}, err // sentinels (ErrActiveRun/ErrAlreadyFired) pass through
	}
	if err := e.dispatch(ctx, run, bearer); err != nil {
		return store.Run{}, err
	}
	return run, nil
}

// Redispatch recovers a run that was created but never dispatched (a crash
// between CreateRun and Invoke, or a run whose post-Invoke
// MarkRunDispatched failed and both attempts in dispatch were exhausted).
// The original bearer is gone — only its hash was stored — so a fresh
// token is signed and the stored hash swapped first. For the
// crash-before-Invoke case, an undispatched pending run has no token
// holder, so the swap is safe by construction; for the
// MarkRunDispatched-failure case the run is already live under the first
// bearer, so this swap races the harness — dispatch's retry (on a
// cancel-free context) is what keeps that path rare, and the residual
// risk is documented in server/README.md.
func (e *Engine) Redispatch(ctx context.Context, run store.Run) error {
	bearer, hash, err := e.Signer.Sign(token.RunClaims{
		RunID: run.ID, TenantID: run.TenantID, ExpiresAt: e.now().Add(e.ttl()),
	})
	if err != nil {
		return err
	}
	if err := e.Store.ResetRunToken(ctx, run.TenantID, run.ID, hash); err != nil {
		return err // e.g. someone else dispatched it between list and here
	}
	return e.dispatch(ctx, run, bearer)
}

func (e *Engine) dispatch(ctx context.Context, run store.Run, bearer string) error {
	actor, err := e.Compute.EnsureActor(ctx,
		compute.WorkflowRef{TenantID: run.TenantID, WorkflowID: run.WorkflowID},
		compute.TemplateRef{Name: "harness-v1"})
	if err != nil {
		e.failDispatch(ctx, run, err)
		return err
	}
	if _, err := e.Compute.Invoke(ctx, actor,
		compute.InvokeRequest{RunID: run.ID, RunToken: bearer}); err != nil {
		e.failDispatch(ctx, run, err)
		return err
	}
	if err := e.Store.MarkRunDispatched(ctx, run.TenantID, run.ID); err != nil {
		// The run is now live — the harness holds the bearer — but still
		// looks undispatched to the next tick, which would Redispatch it:
		// ResetRunToken invalidates the live token mid-run and a second
		// Invoke duplicates spend. Retry once on a cancel-free context
		// (the caller disconnecting shouldn't cost us the retry) before
		// accepting that risk.
		retryCtx := context.WithoutCancel(ctx)
		if retryErr := e.Store.MarkRunDispatched(retryCtx, run.TenantID, run.ID); retryErr != nil {
			slog.Error("engine: mark dispatched failed after retry; run may be double-dispatched next tick",
				"run", run.ID, "tenant", run.TenantID, "err", err, "retry_err", retryErr)
		}
	}
	return nil
}

// failDispatch records a run that never reached its actor, on a
// cancel-free context so a client disconnect can't abort the write.
func (e *Engine) failDispatch(ctx context.Context, run store.Run, cause error) {
	ctx = context.WithoutCancel(ctx)
	if _, err := e.Store.FinalizeRun(ctx, run.TenantID, run.ID, store.RunFinal{
		Status: "failed", ErrorKind: "dispatch_failed", ErrorMsg: cause.Error(),
	}, 0); err != nil {
		slog.Error("engine: record dispatch failure", "run", run.ID, "err", err)
	}
}
