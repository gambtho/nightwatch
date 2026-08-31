// Package store is the tenant-scoped persistence layer. Every method takes
// the tenant id explicitly; no method resolves "the tenant" by lookup.
package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("store: not found")

// ErrAlreadyFired: a run for this (workflow, fire_time) occurrence exists —
// idempotent-tick collisions map here and are treated as success by callers.
var ErrAlreadyFired = errors.New("store: occurrence already fired")

// ErrActiveRun: the one-active-run-per-workflow admission index rejected the
// insert. Manual fires surface this as 409; scheduled fires skip.
var ErrActiveRun = errors.New("store: a run is already active for this workflow")

type Store struct {
	pool *pgxpool.Pool
}

// querier is the subset of pgx shared by the pool and a transaction, so
// row-level operations can run standalone or inside a larger transaction.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
