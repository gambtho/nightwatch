// Package store is the tenant-scoped persistence layer. Every method takes
// the tenant id explicitly; no method resolves "the tenant" by lookup.
package store

import (
	"errors"

	"github.com/jackc/pgx/v5"
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

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
