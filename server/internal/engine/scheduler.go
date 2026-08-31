package engine

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/gambtho/tomte/server/internal/schedule"
	"github.com/gambtho/tomte/server/internal/store"
)

// CapChecker is the slice of the meter the scheduler needs: don't create
// runs that are doomed to a 429.
type CapChecker interface {
	OverCap(ctx context.Context, tenantID uuid.UUID) (bool, error)
}

type Scheduler struct {
	Engine   *Engine
	Store    *store.Store
	Caps     CapChecker
	Interval time.Duration
	Window   time.Duration
	Now      func() time.Time
}

func (s *Scheduler) interval() time.Duration {
	if s.Interval > 0 {
		return s.Interval
	}
	return time.Minute
}

func (s *Scheduler) window() time.Duration {
	if s.Window > 0 {
		return s.Window
	}
	if w := 2 * s.interval(); w > 5*time.Minute {
		return w
	}
	return 5 * time.Minute
}

func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Run ticks until the context ends. Each tick is crash-isolated: a panic
// or error skips the tick, never the loop.
func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(s.interval())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Tick(ctx)
		}
	}
}

// Tick is one create+dispatch pass. Create and dispatch are separate so a
// crash between them loses nothing: the next tick's dispatch step picks up
// created-but-undispatched rows (with a fresh token — see Redispatch).
//
// The lookback is wake-aware ("The sleeping machine"): the persisted
// last-completed-tick widens it to cover a sleep or downtime gap plus one
// interval of margin, so a 3 AM occurrence still fires when the machine
// wakes at 7:42 — once, because mostRecentDue returns only the latest
// occurrence and the fire-time index keeps even that idempotent. The
// heartbeat advances only after a clean create pass: advancing it past a
// failed pass would put the missed occurrence permanently outside the
// next tick's lookback.
func (s *Scheduler) Tick(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("scheduler: tick panic", "panic", r)
		}
	}()
	now := s.now()
	lookback := s.window()
	if last, err := s.Store.GetSchedulerHeartbeat(ctx); err != nil {
		slog.Error("scheduler: heartbeat load", "err", err) // fall back to the default window
	} else if last != nil {
		if gap := now.Sub(*last) + s.interval(); gap > lookback {
			lookback = gap
		}
	}
	ok := s.createDue(ctx, now, lookback)
	s.dispatchPending(ctx)
	if !ok {
		return
	}
	if err := s.Store.SetSchedulerHeartbeat(ctx, now); err != nil {
		slog.Error("scheduler: heartbeat save", "err", err)
	}
}

// mostRecentDue returns the latest occurrence <= now, walking Next from
// the window's start — a pure function of (schedule, now): deterministic
// across restarts, no cursor.
func mostRecentDue(sch *schedule.Schedule, now time.Time, window time.Duration) (time.Time, bool) {
	cursor := now.Add(-window)
	var due time.Time
	found := false
	for {
		next := sch.Next(cursor)
		// A schedule that (despite Parse's validation) never occurs
		// returns the zero time forever, and a schedule wedged on some
		// other non-advancing cursor would otherwise spin here just as
		// badly — defend the walk itself, not just its input.
		if next.IsZero() || !next.After(cursor) {
			break
		}
		if next.After(now) {
			break
		}
		due, found = next, true
		cursor = next
	}
	return due, found
}

// createDue reports whether the pass completed cleanly — deliberate skips
// (already fired, active run, over cap, corrupt schedule) count as clean;
// an infrastructure failure (list error, a fire that errored) does not,
// and the caller must then keep the old heartbeat.
func (s *Scheduler) createDue(ctx context.Context, now time.Time, lookback time.Duration) bool {
	workflows, err := s.Store.ListSchedulableWorkflows(ctx)
	if err != nil {
		slog.Error("scheduler: list schedulable", "err", err)
		return false
	}
	clean := true
	for _, w := range workflows {
		sch, err := schedule.Parse(w.Schedule)
		if err != nil {
			// Validated at creation; a parse failure here is corrupt data.
			slog.Error("scheduler: unparseable schedule", "workflow", w.WorkflowID, "err", err)
			continue
		}
		due, ok := mostRecentDue(sch, now, lookback)
		if !ok {
			continue
		}
		if s.Caps != nil {
			over, err := s.Caps.OverCap(ctx, w.TenantID)
			if err != nil || over {
				slog.Info("scheduler: skip (cap)", "tenant", w.TenantID, "workflow", w.WorkflowID, "err", err)
				continue
			}
		}
		_, err = s.Engine.Fire(ctx, w.TenantID, w.WorkflowID, w.Version, "schedule", &due)
		switch {
		case err == nil:
		case errorsIsAny(err, store.ErrAlreadyFired, store.ErrActiveRun):
			// Occurrence already exists, or a run is active: both are skips.
		default:
			slog.Error("scheduler: fire", "workflow", w.WorkflowID, "err", err)
			clean = false
		}
	}
	return clean
}

func (s *Scheduler) dispatchPending(ctx context.Context) {
	runs, err := s.Store.ListUndispatchedScheduledRuns(ctx)
	if err != nil {
		slog.Error("scheduler: list undispatched", "err", err)
		return
	}
	for _, run := range runs {
		if err := s.Engine.Redispatch(ctx, run); err != nil {
			slog.Error("scheduler: redispatch", "run", run.ID, "err", err)
		}
	}
}

func errorsIsAny(err error, targets ...error) bool {
	for _, t := range targets {
		if errors.Is(err, t) {
			return true
		}
	}
	return false
}
