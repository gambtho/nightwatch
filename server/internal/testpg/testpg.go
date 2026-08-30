// Package testpg provides a migrated Postgres database per test, backed by
// one shared container per test process (cleaned up by the testcontainers
// reaper).
package testpg

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/gambtho/nightwatch/server/internal/db"
)

var (
	once     sync.Once
	baseDSN  string
	startErr error
	counter  atomic.Int64
)

func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	once.Do(func() {
		ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.BasicWaitStrategies())
		if err != nil {
			startErr = err
			return
		}
		baseDSN, startErr = ctr.ConnectionString(ctx, "sslmode=disable")
	})
	if startErr != nil {
		t.Fatalf("start postgres container: %v", startErr)
	}

	name := fmt.Sprintf("test_%d_%d", time.Now().UnixNano(), counter.Add(1))
	admin, err := pgxpool.New(ctx, baseDSN)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	_, err = admin.Exec(ctx, "CREATE DATABASE "+name)
	admin.Close()
	if err != nil {
		t.Fatalf("create database: %v", err)
	}

	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	u.Path = "/" + name
	dsn := u.String()

	if err := db.Migrate(ctx, dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := db.NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
