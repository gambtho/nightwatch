package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// System query: the scheduler heartbeat is a single-row, deployment-wide
// fact — the one table exempt from the package's tenant-scoping rule,
// because the scheduler is a platform actor (see ListSchedulableWorkflows).
// GetSchedulerHeartbeat returns nil when the scheduler has never completed
// a tick (a fresh install), which callers treat as "no catch-up owed".
func (s *Store) GetSchedulerHeartbeat(ctx context.Context) (*time.Time, error) {
	var t time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT last_tick_at FROM scheduler_heartbeat WHERE id = 1`).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// System query: see GetSchedulerHeartbeat.
func (s *Store) SetSchedulerHeartbeat(ctx context.Context, t time.Time) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO scheduler_heartbeat (id, last_tick_at) VALUES (1, $1)
		 ON CONFLICT (id) DO UPDATE SET last_tick_at = EXCLUDED.last_tick_at`, t)
	return err
}
