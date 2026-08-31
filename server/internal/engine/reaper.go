package engine

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/gambtho/tomte/server/internal/store"
)

// Reaper finalizes runs stuck in a non-terminal state past the deadline —
// a crashed server, a harness that never finalized, a dispatch that died.
// Finalization clears the token hash, so a zombie harness that wakes later
// is locked out of the proxy and the internal API alike.
type Reaper struct {
	Store    *store.Store
	Deadline time.Duration
	Interval time.Duration
	Now      func() time.Time
}

// ValidateRunLifetimes enforces deadline > tokenTTL at startup: a run whose
// token expired can never finalize itself, so reaping after expiry is
// guaranteed-safe and reaping before it would be premature.
func ValidateRunLifetimes(tokenTTL, deadline time.Duration) error {
	if deadline <= tokenTTL {
		return fmt.Errorf("run deadline (%s) must exceed run token TTL (%s)", deadline, tokenTTL)
	}
	return nil
}

func (r *Reaper) deadline() time.Duration {
	if r.Deadline > 0 {
		return r.Deadline
	}
	return 2 * time.Hour
}

func (r *Reaper) interval() time.Duration {
	if r.Interval > 0 {
		return r.Interval
	}
	return 5 * time.Minute
}

func (r *Reaper) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Reaper) Run(ctx context.Context) {
	t := time.NewTicker(r.interval())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.Sweep(ctx)
		}
	}
}

func (r *Reaper) Sweep(ctx context.Context) int {
	defer func() {
		if p := recover(); p != nil {
			slog.Error("reaper: sweep panic", "panic", p)
		}
	}()
	stuck, err := r.Store.ListStuckRuns(ctx, r.now().Add(-r.deadline()))
	if err != nil {
		slog.Error("reaper: list stuck runs", "err", err)
		return 0
	}
	reaped := 0
	for _, run := range stuck {
		if _, err := r.Store.FinalizeRun(ctx, run.TenantID, run.ID, store.RunFinal{
			Status: "failed", ErrorKind: "orphaned",
			ErrorMsg: "run exceeded the platform deadline without finalizing",
		}, 0); err != nil {
			slog.Error("reaper: finalize", "run", run.ID, "err", err)
			continue
		}
		slog.Warn("reaper: orphaned run finalized", "run", run.ID, "tenant", run.TenantID)
		reaped++
	}
	return reaped
}
