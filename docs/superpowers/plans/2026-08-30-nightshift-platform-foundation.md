# Nightshift Platform Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `server/` Go module with real multi-tenancy, the public v1 API (workflows, versions, approval, manual runs), the `Compute` seam with a local implementation, and a harvested tool-less harness that pushes run records back over an internal API.

**Architecture:** Control plane (stdlib `net/http` + Postgres) exposes a session-authenticated public API and a run-JWT-authenticated internal API. Firing a run signs a run token, records the run, and invokes the workflow's actor through the `Compute` interface; the local implementation runs the harness in-process, and the harness fetches its context and pushes events/finalization over HTTP exactly as a sandboxed actor will later. No scheduler, no egress proxy, no tools, no grading — those are Plans 2–4 (see the [roadmap](./2026-08-30-nightshift-platform-roadmap.md)).

**Tech Stack:** Go 1.26, Postgres 16, `pgx/v5`, `goose/v3` (embedded SQL migrations), `golang-jwt/v5`, `google/uuid`, `testcontainers-go` (Postgres for tests), Anthropic + OpenAI Go SDKs (ported provider code from `~/workspace/cronfoundry`).

**Spec:** `docs/superpowers/specs/2026-08-30-nightshift-platform-design.md` (companion UX spec: `docs/superpowers/specs/2026-08-28-nightshift-design.md`)

**Revised:** 2026-08-30, after a Codex PAIR review — folded in: per-actor run serialization (Task 10), run-record failure safety (Tasks 8/11), token-hash enforcement and terminal-run immutability (Tasks 7/9), version-allocation locking (Task 3), internal-API body caps (Task 9), ported provider behavioral tests (Task 5), and the v1-unstable stamp (Tasks 3/12).

## Global Constraints

- Module path: `github.com/gambtho/nightwatch/server`, rooted at `server/` in this repo. Go `1.26`.
- Vocabulary: **tenant** (not org), **workflow**, **version**, **run**, **run event**. A workflow version's document is the three artifacts: **steps**, **permit**, **rubric**.
- **Every store method takes `tenantID uuid.UUID` as an explicit parameter** and every SQL statement filters on it. No method may resolve "the tenant" by lookup. Cross-tenant access is a test case in every store task.
- No HTTP framework: stdlib `http.ServeMux` with Go 1.22+ method+wildcard patterns (`"GET /v1/runs/{id}"`, `r.PathValue("id")`).
- Foreign keys to workflow-scoped children are **composite**: `FOREIGN KEY (tenant_id, workflow_id) REFERENCES workflow (tenant_id, id)`.
- Keys are separate per concern and are base64-encoded 32-byte values from env: `NIGHTSHIFT_SESSION_KEY` (session HMAC), `NIGHTSHIFT_RUNNER_KEY` (run-JWT master).
- Pinned harvest-source versions (known-good in cronfoundry): `pgx/v5@v5.9.2`, `goose/v3@v3.27.0`, `anthropic-sdk-go@v1.37.0`, `openai-go@v1.12.0`. Other deps use `@latest`.
- The permit and rubric are stored and versioned from day one but are **opaque JSON** in this plan (enforcement is Plan 2, grading Plan 4).
- Run all Go verification from `server/`: `gofmt -l .` (must print nothing), `go vet ./...`, `go test ./...`. Store/API tests need Docker (testcontainers).
- Commit messages: conventional (`feat:`, `test:`, `docs:`). Docs pass `npx prettier --write <file>` from the repo root before committing — run it yourself; do not rely on a pre-commit hook (none is active in a fresh checkout).
- The harvest source is read-only reference: `/home/tng/workspace/cronfoundry`. Never modify it.

---

## File structure

```
server/
  go.mod, go.sum
  cmd/nightshift/main.go        serve | migrate | dev-session
  internal/db/                  pool, goose migrate, migrations/*.sql
  internal/testpg/              shared Postgres testcontainer helper
  internal/store/               hand-written pgx queries; one file per aggregate
  internal/httpapi/             public /v1 API + session auth
  internal/internalapi/         harness-facing /internal API (run-JWT auth)
  internal/token/               run-JWT signer (HKDF + HS256)
  internal/llm/                 ported providers + pricing; llmtest/ fake
  internal/harness/             the agent loop (tool-less) + HTTP client/sink
  internal/compute/             Compute interface + Local implementation
docs/api/v1.md                  the public API contract (Task 12)
```

---

### Task 1: Server module, DB layer, tenant table

**Files:**

- Create: `server/go.mod` (via commands), `server/internal/db/pool.go`, `server/internal/db/migrate.go`, `server/internal/db/migrations/00001_tenant.sql`, `server/internal/testpg/testpg.go`, `server/internal/store/store.go`, `server/internal/store/tenant.go`
- Test: `server/internal/store/tenant_test.go`

**Interfaces:**

- Consumes: nothing (first task).
- Produces: `db.NewPool(ctx, dsn) (*pgxpool.Pool, error)`; `db.Migrate(ctx, dsn) error`; `testpg.New(t *testing.T) *pgxpool.Pool` (fresh migrated database per call, one shared container per test process); `store.New(pool *pgxpool.Pool) *Store`; `store.Tenant{ID uuid.UUID; Name string; CreatedAt time.Time}`; `(*Store).CreateTenant(ctx, name string) (Tenant, error)`; `(*Store).GetTenant(ctx, id uuid.UUID) (Tenant, error)`; sentinel `store.ErrNotFound`.

- [ ] **Step 1: Initialize the module and fetch dependencies**

```bash
mkdir -p server && cd server
go mod init github.com/gambtho/nightwatch/server
go get github.com/jackc/pgx/v5@v5.9.2 github.com/pressly/goose/v3@v3.27.0 \
  github.com/google/uuid@latest github.com/stretchr/testify@latest \
  github.com/testcontainers/testcontainers-go@latest \
  github.com/testcontainers/testcontainers-go/modules/postgres@latest
```

- [ ] **Step 2: Write the DB layer**

`server/internal/db/pool.go`:

```go
package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}
```

`server/internal/db/migrate.go`:

```go
package db

import (
	"context"
	"embed"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies all embedded migrations. Idempotent.
func Migrate(ctx context.Context, dsn string) error {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return err
	}
	sqlDB := stdlib.OpenDB(*cfg)
	defer sqlDB.Close()
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.UpContext(ctx, sqlDB, "migrations")
}
```

`server/internal/db/migrations/00001_tenant.sql`:

```sql
-- +goose Up
CREATE TABLE tenant (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE tenant;
```

- [ ] **Step 3: Write the shared test-Postgres helper**

`server/internal/testpg/testpg.go`:

```go
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
```

- [ ] **Step 4: Write the failing tenant-store test**

`server/internal/store/tenant_test.go`:

```go
package store_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
)

func TestTenantRoundTrip(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()

	tn, err := s.CreateTenant(ctx, "acme")
	require.NoError(t, err)
	require.Equal(t, "acme", tn.Name)
	require.NotEqual(t, uuid.Nil, tn.ID)

	got, err := s.GetTenant(ctx, tn.ID)
	require.NoError(t, err)
	require.Equal(t, tn.ID, got.ID)
}

func TestGetTenantNotFound(t *testing.T) {
	s := store.New(testpg.New(t))
	_, err := s.GetTenant(context.Background(), uuid.New())
	require.ErrorIs(t, err, store.ErrNotFound)
}
```

- [ ] **Step 5: Run the test to verify it fails**

Run: `cd server && go test ./internal/store/`
Expected: FAIL (compile error — `undefined: store.New`).

- [ ] **Step 6: Implement the store**

`server/internal/store/store.go`:

```go
// Package store is the tenant-scoped persistence layer. Every method takes
// the tenant id explicitly; no method resolves "the tenant" by lookup.
package store

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("store: not found")

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
```

`server/internal/store/tenant.go`:

```go
package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Tenant struct {
	ID        uuid.UUID
	Name      string
	CreatedAt time.Time
}

func (s *Store) CreateTenant(ctx context.Context, name string) (Tenant, error) {
	var t Tenant
	err := s.pool.QueryRow(ctx,
		`INSERT INTO tenant (name) VALUES ($1) RETURNING id, name, created_at`,
		name,
	).Scan(&t.ID, &t.Name, &t.CreatedAt)
	return t, err
}

func (s *Store) GetTenant(ctx context.Context, id uuid.UUID) (Tenant, error) {
	var t Tenant
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, created_at FROM tenant WHERE id = $1`,
		id,
	).Scan(&t.ID, &t.Name, &t.CreatedAt)
	return t, notFound(err)
}
```

- [ ] **Step 7: Run the tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS, `gofmt` prints nothing.

- [ ] **Step 8: Commit**

```bash
git add server
git commit -m "feat(server): module scaffold, migrations, tenant store"
```

---

### Task 2: Users and session auth

**Files:**

- Create: `server/internal/db/migrations/00002_app_user.sql`, `server/internal/store/user.go`, `server/internal/httpapi/session.go`
- Test: `server/internal/store/user_test.go`, `server/internal/httpapi/session_test.go`

**Interfaces:**

- Consumes: `store.Store`, `testpg.New` (Task 1).
- Produces: `store.User{ID, TenantID uuid.UUID; Email, Role string}`; `(*Store).UpsertUser(ctx, tenantID uuid.UUID, email string) (User, error)`; `httpapi.SessionClaims{UserID, TenantID uuid.UUID; Role string; Exp int64}`; `httpapi.SignSession(c SessionClaims, key []byte, ttl time.Duration) (string, error)`; `httpapi.VerifySession(value string, key []byte) (SessionClaims, error)`; `httpapi.RequireSession(key []byte, next http.Handler) http.Handler`; `httpapi.ClaimsFrom(ctx context.Context) SessionClaims`; `httpapi.SessionCookie(key []byte, c SessionClaims, ttl time.Duration) (*http.Cookie, error)`; cookie name constant `httpapi.SessionCookieName = "ns_session"`.

- [ ] **Step 1: Write the migration**

`server/internal/db/migrations/00002_app_user.sql`:

```sql
-- +goose Up
CREATE TABLE app_user (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenant (id) ON DELETE CASCADE,
    email text NOT NULL,
    role text NOT NULL DEFAULT 'owner' CHECK (role IN ('owner')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, email)
);

-- +goose Down
DROP TABLE app_user;
```

(Only `owner` exists: the UX spec defers multi-user governance — one person builds and approves.)

- [ ] **Step 2: Write the failing tests**

`server/internal/store/user_test.go`:

```go
package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
)

func TestUpsertUserIdempotent(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "acme")
	require.NoError(t, err)

	u1, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)
	require.Equal(t, "owner", u1.Role)

	u2, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)
	require.Equal(t, u1.ID, u2.ID)
}
```

`server/internal/httpapi/session_test.go`:

```go
package httpapi_test

import (
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/httpapi"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	return key
}

func TestSessionRoundTrip(t *testing.T) {
	key := testKey(t)
	claims := httpapi.SessionClaims{UserID: uuid.New(), TenantID: uuid.New(), Role: "owner"}
	val, err := httpapi.SignSession(claims, key, time.Hour)
	require.NoError(t, err)

	got, err := httpapi.VerifySession(val, key)
	require.NoError(t, err)
	require.Equal(t, claims.UserID, got.UserID)
	require.Equal(t, claims.TenantID, got.TenantID)
}

func TestSessionRejectsTamperAndExpiry(t *testing.T) {
	key := testKey(t)
	claims := httpapi.SessionClaims{UserID: uuid.New(), TenantID: uuid.New(), Role: "owner"}

	val, err := httpapi.SignSession(claims, key, time.Hour)
	require.NoError(t, err)
	_, err = httpapi.VerifySession(val+"x", key)
	require.Error(t, err)

	expired, err := httpapi.SignSession(claims, key, -time.Minute)
	require.NoError(t, err)
	_, err = httpapi.VerifySession(expired, key)
	require.Error(t, err)
}

func TestRequireSession(t *testing.T) {
	key := testKey(t)
	var seen httpapi.SessionClaims
	h := httpapi.RequireSession(key, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = httpapi.ClaimsFrom(r.Context())
	}))

	// No cookie: 401.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/workflows", nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// Valid cookie: passes and exposes claims.
	claims := httpapi.SessionClaims{UserID: uuid.New(), TenantID: uuid.New(), Role: "owner"}
	cookie, err := httpapi.SessionCookie(key, claims, time.Hour)
	require.NoError(t, err)
	req := httptest.NewRequest("GET", "/v1/workflows", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, claims.TenantID, seen.TenantID)
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd server && go test ./internal/store/ ./internal/httpapi/`
Expected: FAIL (compile errors — `UpsertUser`, package `httpapi` undefined).

- [ ] **Step 4: Implement**

`server/internal/store/user.go`:

```go
package store

import (
	"context"

	"github.com/google/uuid"
)

type User struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	Email    string
	Role     string
}

func (s *Store) UpsertUser(ctx context.Context, tenantID uuid.UUID, email string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		INSERT INTO app_user (tenant_id, email) VALUES ($1, $2)
		ON CONFLICT (tenant_id, email) DO UPDATE SET email = EXCLUDED.email
		RETURNING id, tenant_id, email, role`,
		tenantID, email,
	).Scan(&u.ID, &u.TenantID, &u.Email, &u.Role)
	return u, err
}
```

`server/internal/httpapi/session.go` — signed cookie, `base64url(JSON payload) + "." + base64url(HMAC-SHA256)` (harvested shape from cronfoundry `internal/webapi/session.go`, with the tenant added — the field its `SessionClaims` was missing):

```go
// Package httpapi is the public, session-authenticated /v1 API.
package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const SessionCookieName = "ns_session"

type SessionClaims struct {
	UserID   uuid.UUID `json:"uid"`
	TenantID uuid.UUID `json:"tid"`
	Role     string    `json:"role"`
	Exp      int64     `json:"exp"`
}

type claimsKey struct{}

func mac(key, payload []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(payload)
	return m.Sum(nil)
}

func SignSession(c SessionClaims, key []byte, ttl time.Duration) (string, error) {
	c.Exp = time.Now().Add(ttl).Unix()
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString(payload) + "." + enc.EncodeToString(mac(key, payload)), nil
}

func VerifySession(value string, key []byte) (SessionClaims, error) {
	var c SessionClaims
	part, sig, ok := strings.Cut(value, ".")
	if !ok {
		return c, errors.New("session: malformed")
	}
	enc := base64.RawURLEncoding
	payload, err := enc.DecodeString(part)
	if err != nil {
		return c, errors.New("session: malformed")
	}
	gotMAC, err := enc.DecodeString(sig)
	if err != nil || !hmac.Equal(gotMAC, mac(key, payload)) {
		return c, errors.New("session: bad signature")
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		return c, err
	}
	if time.Now().Unix() >= c.Exp {
		return SessionClaims{}, errors.New("session: expired")
	}
	return c, nil
}

func SessionCookie(key []byte, c SessionClaims, ttl time.Duration) (*http.Cookie, error) {
	val, err := SignSession(c, key, ttl)
	if err != nil {
		return nil, err
	}
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    val,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}, nil
}

func RequireSession(key []byte, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(SessionCookieName)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		claims, err := VerifySession(cookie.Value, key)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), claimsKey{}, claims)))
	})
}

func ClaimsFrom(ctx context.Context) SessionClaims {
	c, _ := ctx.Value(claimsKey{}).(SessionClaims)
	return c
}
```

- [ ] **Step 5: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add server
git commit -m "feat(server): app_user table and tenant-scoped session auth"
```

---

### Task 3: Workflow and version store

**Files:**

- Create: `server/internal/db/migrations/00003_workflow.sql`, `server/internal/store/workflow.go`
- Test: `server/internal/store/workflow_test.go`

**Interfaces:**

- Consumes: Task 1's store scaffolding.
- Produces:

Note on `StepsDoc`: this is the **compiled execution form** of the steps artifact, not
the user-facing one (the UX prototype's steps are `{id, text}` items —
`src/lib/types.ts`). The v1 API is stamped **unstable** for exactly this reason (Task
12's contract doc); the user-facing artifact joins the version document, and the
execution form stops being client-supplied, before the contract freezes.

```go
type StepsDoc struct {
	SystemPrompt string `json:"system_prompt"`
	Kickoff      string `json:"kickoff"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	MaxTokens    int    `json:"max_tokens"`
}
type VersionDoc struct {
	Steps  StepsDoc        `json:"steps"`
	Permit json.RawMessage `json:"permit"`
	Rubric json.RawMessage `json:"rubric"`
}
type Workflow struct{ ID, TenantID uuid.UUID; Name string; CreatedAt time.Time }
type Version struct {
	WorkflowID uuid.UUID
	TenantID   uuid.UUID
	Number     int
	Doc        VersionDoc
	Status     string // draft | approved | superseded
	ApprovedBy *uuid.UUID
	ApprovedAt *time.Time
	CreatedAt  time.Time
}
func (s *Store) CreateWorkflow(ctx context.Context, tenantID uuid.UUID, name string, doc VersionDoc) (Workflow, Version, error)
func (s *Store) AddVersion(ctx context.Context, tenantID, workflowID uuid.UUID, doc VersionDoc) (Version, error)
func (s *Store) ApproveVersion(ctx context.Context, tenantID, workflowID uuid.UUID, number int, approvedBy uuid.UUID) (Version, error)
func (s *Store) GetWorkflow(ctx context.Context, tenantID, id uuid.UUID) (Workflow, error)
func (s *Store) ListWorkflows(ctx context.Context, tenantID uuid.UUID) ([]Workflow, error)
func (s *Store) GetVersion(ctx context.Context, tenantID, workflowID uuid.UUID, number int) (Version, error)
func (s *Store) ListVersions(ctx context.Context, tenantID, workflowID uuid.UUID) ([]Version, error)
func (s *Store) GetApprovedVersion(ctx context.Context, tenantID, workflowID uuid.UUID) (Version, error)
```

- [ ] **Step 1: Write the migration**

`server/internal/db/migrations/00003_workflow.sql`:

```sql
-- +goose Up
CREATE TABLE workflow (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenant (id) ON DELETE CASCADE,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id)
);

CREATE TABLE workflow_version (
    workflow_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    version int NOT NULL,
    steps jsonb NOT NULL,
    permit jsonb NOT NULL,
    rubric jsonb NOT NULL,
    status text NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'approved', 'superseded')),
    approved_by uuid,
    approved_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workflow_id, version),
    -- Composite FK: enforces same-tenant parentage, the gap cronfoundry's
    -- own migration comments admit its single-column FKs leave open.
    FOREIGN KEY (tenant_id, workflow_id)
        REFERENCES workflow (tenant_id, id) ON DELETE CASCADE
);

-- At most one approved version per workflow; enforced by the database,
-- not by application discipline.
CREATE UNIQUE INDEX workflow_version_one_approved
    ON workflow_version (workflow_id) WHERE status = 'approved';

-- +goose Down
DROP TABLE workflow_version;
DROP TABLE workflow;
```

- [ ] **Step 2: Write the failing tests**

`server/internal/store/workflow_test.go` (full file):

```go
package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
)

func testDoc() store.VersionDoc {
	return store.VersionDoc{
		Steps: store.StepsDoc{
			SystemPrompt: "You prepare the weekly support digest.",
			Kickoff:      "Summarize last week's tickets.",
			Provider:     "anthropic",
			Model:        "claude-sonnet-5",
			MaxTokens:    2048,
		},
		Permit: json.RawMessage(`{"read":["zendesk"],"write":["slack:#support"]}`),
		Rubric: json.RawMessage(`{"rules":["never miss a security issue"]}`),
	}
}

func TestWorkflowVersionLifecycle(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "acme")
	require.NoError(t, err)
	user, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)

	wf, v1, err := s.CreateWorkflow(ctx, tn.ID, "weekly digest", testDoc())
	require.NoError(t, err)
	require.Equal(t, 1, v1.Number)
	require.Equal(t, "draft", v1.Status)

	// No approved version yet.
	_, err = s.GetApprovedVersion(ctx, tn.ID, wf.ID)
	require.ErrorIs(t, err, store.ErrNotFound)

	// Approve v1.
	av, err := s.ApproveVersion(ctx, tn.ID, wf.ID, 1, user.ID)
	require.NoError(t, err)
	require.Equal(t, "approved", av.Status)
	require.NotNil(t, av.ApprovedAt)

	// A new draft version; approving it supersedes v1.
	v2, err := s.AddVersion(ctx, tn.ID, wf.ID, testDoc())
	require.NoError(t, err)
	require.Equal(t, 2, v2.Number)
	_, err = s.ApproveVersion(ctx, tn.ID, wf.ID, 2, user.ID)
	require.NoError(t, err)

	got, err := s.GetApprovedVersion(ctx, tn.ID, wf.ID)
	require.NoError(t, err)
	require.Equal(t, 2, got.Number)

	old, err := s.GetVersion(ctx, tn.ID, wf.ID, 1)
	require.NoError(t, err)
	require.Equal(t, "superseded", old.Status)

	// Approving an already-superseded version fails: only drafts approve.
	_, err = s.ApproveVersion(ctx, tn.ID, wf.ID, 1, user.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestWorkflowCrossTenantIsolation(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tnA, err := s.CreateTenant(ctx, "a")
	require.NoError(t, err)
	tnB, err := s.CreateTenant(ctx, "b")
	require.NoError(t, err)

	wf, _, err := s.CreateWorkflow(ctx, tnA.ID, "a's workflow", testDoc())
	require.NoError(t, err)

	// Tenant B sees nothing of tenant A's workflow, by any path.
	_, err = s.GetWorkflow(ctx, tnB.ID, wf.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = s.AddVersion(ctx, tnB.ID, wf.ID, testDoc())
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = s.ApproveVersion(ctx, tnB.ID, wf.ID, 1, uuid.New())
	require.ErrorIs(t, err, store.ErrNotFound)
	list, err := s.ListWorkflows(ctx, tnB.ID)
	require.NoError(t, err)
	require.Empty(t, list)
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd server && go test ./internal/store/`
Expected: FAIL (compile error — `undefined: store.VersionDoc`).

- [ ] **Step 4: Implement the workflow store**

`server/internal/store/workflow.go`:

```go
package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type StepsDoc struct {
	SystemPrompt string `json:"system_prompt"`
	Kickoff      string `json:"kickoff"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	MaxTokens    int    `json:"max_tokens"`
}

type VersionDoc struct {
	Steps  StepsDoc        `json:"steps"`
	Permit json.RawMessage `json:"permit"`
	Rubric json.RawMessage `json:"rubric"`
}

type Workflow struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Name      string
	CreatedAt time.Time
}

type Version struct {
	WorkflowID uuid.UUID
	TenantID   uuid.UUID
	Number     int
	Doc        VersionDoc
	Status     string
	ApprovedBy *uuid.UUID
	ApprovedAt *time.Time
	CreatedAt  time.Time
}

const versionCols = `workflow_id, tenant_id, version, steps, permit, rubric,
	status, approved_by, approved_at, created_at`

func scanVersion(row pgx.Row) (Version, error) {
	var v Version
	var steps []byte
	err := row.Scan(&v.WorkflowID, &v.TenantID, &v.Number, &steps,
		&v.Doc.Permit, &v.Doc.Rubric, &v.Status, &v.ApprovedBy,
		&v.ApprovedAt, &v.CreatedAt)
	if err != nil {
		return v, notFound(err)
	}
	if err := json.Unmarshal(steps, &v.Doc.Steps); err != nil {
		return v, err
	}
	return v, nil
}

func (s *Store) CreateWorkflow(ctx context.Context, tenantID uuid.UUID, name string, doc VersionDoc) (Workflow, Version, error) {
	var wf Workflow
	var v Version
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return wf, v, err
	}
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx,
		`INSERT INTO workflow (tenant_id, name) VALUES ($1, $2)
		 RETURNING id, tenant_id, name, created_at`,
		tenantID, name,
	).Scan(&wf.ID, &wf.TenantID, &wf.Name, &wf.CreatedAt)
	if err != nil {
		return wf, v, err
	}

	steps, err := json.Marshal(doc.Steps)
	if err != nil {
		return wf, v, err
	}
	v, err = scanVersion(tx.QueryRow(ctx,
		`INSERT INTO workflow_version
		   (workflow_id, tenant_id, version, steps, permit, rubric)
		 VALUES ($1, $2, 1, $3, $4, $5)
		 RETURNING `+versionCols,
		wf.ID, tenantID, steps, doc.Permit, doc.Rubric))
	if err != nil {
		return wf, v, err
	}
	return wf, v, tx.Commit(ctx)
}

func (s *Store) AddVersion(ctx context.Context, tenantID, workflowID uuid.UUID, doc VersionDoc) (Version, error) {
	steps, err := json.Marshal(doc.Steps)
	if err != nil {
		return Version{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Version{}, err
	}
	defer tx.Rollback(ctx)

	// FOR UPDATE serializes concurrent AddVersion calls on one workflow so
	// MAX(version)+1 cannot collide; the tenant filter doubles as the
	// cross-tenant guard (wrong tenant -> no row -> ErrNotFound).
	var id uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT id FROM workflow WHERE id = $1 AND tenant_id = $2 FOR UPDATE`,
		workflowID, tenantID).Scan(&id)
	if err != nil {
		return Version{}, notFound(err)
	}

	v, err := scanVersion(tx.QueryRow(ctx,
		`INSERT INTO workflow_version
		   (workflow_id, tenant_id, version, steps, permit, rubric)
		 VALUES ($1, $2,
		        (SELECT COALESCE(MAX(version), 0) + 1 FROM workflow_version WHERE workflow_id = $1),
		        $3, $4, $5)
		 RETURNING `+versionCols,
		workflowID, tenantID, steps, doc.Permit, doc.Rubric))
	if err != nil {
		return Version{}, err
	}
	return v, tx.Commit(ctx)
}

func (s *Store) ApproveVersion(ctx context.Context, tenantID, workflowID uuid.UUID, number int, approvedBy uuid.UUID) (Version, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Version{}, err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx,
		`UPDATE workflow_version SET status = 'superseded'
		 WHERE workflow_id = $1 AND tenant_id = $2 AND status = 'approved'`,
		workflowID, tenantID)
	if err != nil {
		return Version{}, err
	}
	v, err := scanVersion(tx.QueryRow(ctx,
		`UPDATE workflow_version
		 SET status = 'approved', approved_by = $3, approved_at = now()
		 WHERE workflow_id = $1 AND tenant_id = $2 AND version = $4
		   AND status = 'draft'
		 RETURNING `+versionCols,
		workflowID, tenantID, approvedBy, number))
	if err != nil {
		return Version{}, err
	}
	return v, tx.Commit(ctx)
}

func (s *Store) GetWorkflow(ctx context.Context, tenantID, id uuid.UUID) (Workflow, error) {
	var wf Workflow
	err := s.pool.QueryRow(ctx,
		`SELECT id, tenant_id, name, created_at FROM workflow
		 WHERE id = $1 AND tenant_id = $2`,
		id, tenantID,
	).Scan(&wf.ID, &wf.TenantID, &wf.Name, &wf.CreatedAt)
	return wf, notFound(err)
}

func (s *Store) ListWorkflows(ctx context.Context, tenantID uuid.UUID) ([]Workflow, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, tenant_id, name, created_at FROM workflow
		 WHERE tenant_id = $1 ORDER BY created_at`,
		tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Workflow
	for rows.Next() {
		var wf Workflow
		if err := rows.Scan(&wf.ID, &wf.TenantID, &wf.Name, &wf.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, wf)
	}
	return out, rows.Err()
}

func (s *Store) GetVersion(ctx context.Context, tenantID, workflowID uuid.UUID, number int) (Version, error) {
	return scanVersion(s.pool.QueryRow(ctx,
		`SELECT `+versionCols+` FROM workflow_version
		 WHERE workflow_id = $1 AND tenant_id = $2 AND version = $3`,
		workflowID, tenantID, number))
}

func (s *Store) ListVersions(ctx context.Context, tenantID, workflowID uuid.UUID) ([]Version, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+versionCols+` FROM workflow_version
		 WHERE workflow_id = $1 AND tenant_id = $2 ORDER BY version`,
		workflowID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Version
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) GetApprovedVersion(ctx context.Context, tenantID, workflowID uuid.UUID) (Version, error) {
	return scanVersion(s.pool.QueryRow(ctx,
		`SELECT `+versionCols+` FROM workflow_version
		 WHERE workflow_id = $1 AND tenant_id = $2 AND status = 'approved'`,
		workflowID, tenantID))
}
```

Note: `scanVersion` takes `pgx.Row`; `pgx.Rows` satisfies it, which is why `ListVersions` can reuse it.

- [ ] **Step 5: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add server
git commit -m "feat(server): workflow and version store with approval semantics"
```

---

### Task 4: Workflow HTTP API

**Files:**

- Create: `server/internal/httpapi/httpapi.go`, `server/internal/httpapi/workflows.go`
- Test: `server/internal/httpapi/workflows_test.go`

**Interfaces:**

- Consumes: `store` (Task 3), session auth (Task 2).
- Produces: `httpapi.Deps{Store *store.Store; SessionKey []byte; Signer *token.Signer; Compute compute.Compute}` — declare only `Store` and `SessionKey` now; `Signer`/`Compute` fields are added in Task 11 (until then the struct has just the two fields). `httpapi.RegisterRoutes(mux *http.ServeMux, d Deps)`. Routes this task: `POST /v1/workflows`, `GET /v1/workflows`, `GET /v1/workflows/{id}`, `POST /v1/workflows/{id}/versions`, `POST /v1/workflows/{id}/versions/{version}/approve`. JSON wire shapes below.

Wire shapes (also the contract for Task 12's `docs/api/v1.md`):

```json
POST /v1/workflows
{"name": "weekly digest", "steps": {"system_prompt": "...", "kickoff": "...", "provider": "anthropic", "model": "claude-sonnet-5", "max_tokens": 2048}, "permit": {}, "rubric": {}}
-> 201 {"workflow": {"id": "...", "name": "...", "created_at": "..."}, "version": {"number": 1, "status": "draft", ...}}

GET /v1/workflows            -> 200 {"workflows": [ ... ]}
GET /v1/workflows/{id}       -> 200 {"workflow": {...}, "versions": [ ... ]}
POST /v1/workflows/{id}/versions          (body: steps/permit/rubric) -> 201 {"version": {...}}
POST /v1/workflows/{id}/versions/{version}/approve -> 200 {"version": {...}}
```

- [ ] **Step 1: Write the failing tests**

`server/internal/httpapi/workflows_test.go`:

```go
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/httpapi"
	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
)

type env struct {
	ts     *httptest.Server
	store  *store.Store
	key    []byte
	cookie *http.Cookie
	tenant store.Tenant
	user   store.User
}

func newEnv(t *testing.T) *env {
	t.Helper()
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "acme")
	require.NoError(t, err)
	user, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)

	key := testKey(t)
	cookie, err := httpapi.SessionCookie(key,
		httpapi.SessionClaims{UserID: user.ID, TenantID: tn.ID, Role: "owner"},
		time.Hour)
	require.NoError(t, err)

	mux := http.NewServeMux()
	httpapi.RegisterRoutes(mux, httpapi.Deps{Store: s, SessionKey: key})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return &env{ts: ts, store: s, key: key, cookie: cookie, tenant: tn, user: user}
}

func (e *env) do(t *testing.T, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req, err := http.NewRequest(method, e.ts.URL+path, &buf)
	require.NoError(t, err)
	req.AddCookie(e.cookie)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func workflowBody() map[string]any {
	return map[string]any{
		"name": "weekly digest",
		"steps": map[string]any{
			"system_prompt": "You prepare the weekly support digest.",
			"kickoff":       "Summarize last week's tickets.",
			"provider":      "anthropic",
			"model":         "claude-sonnet-5",
			"max_tokens":    2048,
		},
		"permit": map[string]any{"read": []string{"zendesk"}},
		"rubric": map[string]any{"rules": []string{"under a page"}},
	}
}

func TestWorkflowEndpoints(t *testing.T) {
	e := newEnv(t)

	resp, out := e.do(t, "POST", "/v1/workflows", workflowBody())
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	wf := out["workflow"].(map[string]any)
	id := wf["id"].(string)
	require.Equal(t, float64(1), out["version"].(map[string]any)["number"])

	resp, out = e.do(t, "GET", "/v1/workflows", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, out["workflows"], 1)

	resp, out = e.do(t, "POST", fmt.Sprintf("/v1/workflows/%s/versions/1/approve", id), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "approved", out["version"].(map[string]any)["status"])

	resp, out = e.do(t, "POST", fmt.Sprintf("/v1/workflows/%s/versions", id), workflowBody())
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.Equal(t, float64(2), out["version"].(map[string]any)["number"])

	resp, out = e.do(t, "GET", "/v1/workflows/"+id, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, out["versions"], 2)
}

func TestWorkflowAPIRequiresSession(t *testing.T) {
	e := newEnv(t)
	resp, err := http.Get(e.ts.URL + "/v1/workflows")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestWorkflowAPINotFoundAndBadInput(t *testing.T) {
	e := newEnv(t)
	resp, _ := e.do(t, "GET", "/v1/workflows/"+uuid.NewString(), nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp, _ = e.do(t, "GET", "/v1/workflows/not-a-uuid", nil)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd server && go test ./internal/httpapi/`
Expected: FAIL (compile error — `undefined: httpapi.Deps`).

- [ ] **Step 3: Implement**

`server/internal/httpapi/httpapi.go`:

```go
package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/gambtho/nightwatch/server/internal/store"
)

type Deps struct {
	Store      *store.Store
	SessionKey []byte
}

func RegisterRoutes(mux *http.ServeMux, d Deps) {
	auth := func(h http.HandlerFunc) http.Handler {
		return RequireSession(d.SessionKey, h)
	}
	mux.Handle("POST /v1/workflows", auth(d.createWorkflow))
	mux.Handle("GET /v1/workflows", auth(d.listWorkflows))
	mux.Handle("GET /v1/workflows/{id}", auth(d.getWorkflow))
	mux.Handle("POST /v1/workflows/{id}/versions", auth(d.addVersion))
	mux.Handle("POST /v1/workflows/{id}/versions/{version}/approve", auth(d.approveVersion))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("httpapi: encode response", "err", err)
	}
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	default:
		slog.Error("httpapi: internal error", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

func pathUUID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid " + name})
		return uuid.Nil, false
	}
	return id, true
}
```

`server/internal/httpapi/workflows.go`:

```go
package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/gambtho/nightwatch/server/internal/store"
)

type workflowJSON struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type versionJSON struct {
	Number     int             `json:"number"`
	Status     string          `json:"status"`
	Steps      store.StepsDoc  `json:"steps"`
	Permit     json.RawMessage `json:"permit"`
	Rubric     json.RawMessage `json:"rubric"`
	ApprovedAt *time.Time      `json:"approved_at,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

type versionDocJSON struct {
	Name   string          `json:"name"`
	Steps  store.StepsDoc  `json:"steps"`
	Permit json.RawMessage `json:"permit"`
	Rubric json.RawMessage `json:"rubric"`
}

func toWorkflowJSON(wf store.Workflow) workflowJSON {
	return workflowJSON{ID: wf.ID, Name: wf.Name, CreatedAt: wf.CreatedAt}
}

func toVersionJSON(v store.Version) versionJSON {
	return versionJSON{
		Number: v.Number, Status: v.Status, Steps: v.Doc.Steps,
		Permit: v.Doc.Permit, Rubric: v.Doc.Rubric,
		ApprovedAt: v.ApprovedAt, CreatedAt: v.CreatedAt,
	}
}

func decodeDoc(w http.ResponseWriter, r *http.Request) (versionDocJSON, bool) {
	var body versionDocJSON
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return body, false
	}
	if body.Permit == nil {
		body.Permit = json.RawMessage(`{}`)
	}
	if body.Rubric == nil {
		body.Rubric = json.RawMessage(`{}`)
	}
	return body, true
}

func (d Deps) createWorkflow(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	body, ok := decodeDoc(w, r)
	if !ok {
		return
	}
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	wf, v, err := d.Store.CreateWorkflow(r.Context(), claims.TenantID, body.Name,
		store.VersionDoc{Steps: body.Steps, Permit: body.Permit, Rubric: body.Rubric})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"workflow": toWorkflowJSON(wf), "version": toVersionJSON(v),
	})
}

func (d Deps) listWorkflows(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	wfs, err := d.Store.ListWorkflows(r.Context(), claims.TenantID)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]workflowJSON, 0, len(wfs))
	for _, wf := range wfs {
		out = append(out, toWorkflowJSON(wf))
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": out})
}

func (d Deps) getWorkflow(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	wf, err := d.Store.GetWorkflow(r.Context(), claims.TenantID, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	versions, err := d.Store.ListVersions(r.Context(), claims.TenantID, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	vout := make([]versionJSON, 0, len(versions))
	for _, v := range versions {
		vout = append(vout, toVersionJSON(v))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"workflow": toWorkflowJSON(wf), "versions": vout,
	})
}

func (d Deps) addVersion(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	body, ok := decodeDoc(w, r)
	if !ok {
		return
	}
	v, err := d.Store.AddVersion(r.Context(), claims.TenantID, id,
		store.VersionDoc{Steps: body.Steps, Permit: body.Permit, Rubric: body.Rubric})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"version": toVersionJSON(v)})
}

func (d Deps) approveVersion(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	number, err := strconv.Atoi(r.PathValue("version"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid version"})
		return
	}
	v, err := d.Store.ApproveVersion(r.Context(), claims.TenantID, id, number, claims.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": toVersionJSON(v)})
}
```

- [ ] **Step 4: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server
git commit -m "feat(server): public v1 workflow endpoints"
```

---

### Task 5: LLM provider port

**Files:**

- Create: `server/internal/llm/provider.go`, `server/internal/llm/anthropic.go`, `server/internal/llm/openai.go`, `server/internal/llm/pricing.go`, `server/internal/llm/factory.go`, `server/internal/llm/llmtest/scripted.go`
- Test: `server/internal/llm/pricing_test.go`, `server/internal/llm/factory_test.go`, plus the ported `server/internal/llm/anthropic_test.go` and `server/internal/llm/openai_test.go`
- Reference (read-only): `/home/tng/workspace/cronfoundry/internal/llm/{provider,anthropic,openai,pricing}.go` and their `_test.go` files

**Interfaces:**

- Consumes: nothing internal.
- Produces: the `llm` package with cronfoundry's types **unchanged in shape**: `Role` (+ four constants), `Message{Role Role; Content string; ToolUses []ToolUse; ToolUseID string}`, `StreamChunk{Delta string}`, `Usage{InputTokens, OutputTokens int}`, `ToolUse`, `ToolDef`, `TurnResult`, `Provider` (with `Chat(ctx, []Message, CallOptions, func(StreamChunk)) (Usage, error)`), `ToolCapableProvider` (with `ChatTurn`) — **except** `CallOptions`, reduced to `{Model string; MaxTokens int; APIKey string}` (the `Endpoint`/`Deployment` fields served the dropped Azure Foundry provider). Plus `CostCents(provider, model string, u Usage) int`, `Config{AnthropicBaseURL, OpenAIBaseURL, OpenRouterBaseURL string}`, `NewFactory(cfg Config) func(name string) (Provider, error)` (names: `anthropic`, `openai`, `openrouter`), and `llmtest.Scripted` implementing `llm.Provider`.

- [ ] **Step 1: Fetch the SDKs and copy the source files**

```bash
cd server
go get github.com/anthropics/anthropic-sdk-go@v1.37.0 github.com/openai/openai-go@v1.12.0
mkdir -p internal/llm/llmtest
cp /home/tng/workspace/cronfoundry/internal/llm/provider.go internal/llm/
cp /home/tng/workspace/cronfoundry/internal/llm/anthropic.go internal/llm/
cp /home/tng/workspace/cronfoundry/internal/llm/openai.go internal/llm/
cp /home/tng/workspace/cronfoundry/internal/llm/pricing.go internal/llm/
cp /home/tng/workspace/cronfoundry/internal/llm/anthropic_test.go internal/llm/
cp /home/tng/workspace/cronfoundry/internal/llm/openai_test.go internal/llm/
```

Then edit the copies:

1. `provider.go`: delete the `Endpoint` and `Deployment` fields from `CallOptions` (and their comments). Keep everything else, including `ToolCapableProvider` — the tool loop returns with connector work.
2. `anthropic.go` / `openai.go`: fix any import paths referencing `github.com/gambtho/cronfoundry/...` to this module; delete any code paths reading the removed `CallOptions` fields or referencing the `copilot`/`azure-foundry` providers (compile errors will point at each). Do not otherwise change behavior.
3. `pricing.go`: delete the `"copilot-enterprise"` entry from `priceTable`. Keep the table comment noting prices are a point-in-time list; DB-driven pricing is a later concern (metering, Plan 3).
4. `anthropic_test.go` / `openai_test.go`: same import fixes; delete cases exercising the removed `CallOptions` fields or dropped providers. The behavioral coverage — streaming, usage accounting, HTTP error bodies, tool turns — must survive the port: this code is supposed to behave identically to its source, and compile-only verification cannot show that.
5. Do **not** copy `factory.go`, `azurefoundry.go`, or `copilot.go` (or their tests).

- [ ] **Step 2: Write the failing tests**

`server/internal/llm/pricing_test.go`:

```go
package llm

import "testing"

func TestCostCentsKnownAndUnknown(t *testing.T) {
	u := Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}
	if got := CostCents("anthropic", "no-such-model", u); got != 0 {
		t.Fatalf("unknown model: want 0, got %d", got)
	}
	// Any priced anthropic model must cost more than zero at 1M/1M tokens.
	// Pick the first entry in the table rather than hardcoding a model name.
	for model := range priceTable["anthropic"] {
		if got := CostCents("anthropic", model, u); got <= 0 {
			t.Fatalf("model %s: want > 0, got %d", model, got)
		}
		break
	}
}
```

`server/internal/llm/factory_test.go`:

```go
package llm

import "testing"

func TestNewFactory(t *testing.T) {
	factory := NewFactory(Config{})
	for _, name := range []string{"anthropic", "openai", "openrouter"} {
		if _, err := factory(name); err != nil {
			t.Fatalf("factory(%q): %v", name, err)
		}
	}
	if _, err := factory("copilot-enterprise"); err == nil {
		t.Fatal("dropped provider should error")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd server && go test ./internal/llm/`
Expected: FAIL (compile error — `undefined: NewFactory`; possibly earlier copy-edit errors to fix first).

- [ ] **Step 4: Write the factory and the scripted fake**

`server/internal/llm/factory.go` (replaces cronfoundry's env-var factory with explicit config — process-global base URLs don't survive multi-tenancy):

```go
package llm

import "fmt"

type Config struct {
	AnthropicBaseURL  string // "" means the SDK default
	OpenAIBaseURL     string
	OpenRouterBaseURL string
}

const defaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"

// NewFactory returns a provider lookup. Supported: anthropic, openai,
// openrouter. API keys are per-call (CallOptions), not per-factory.
func NewFactory(cfg Config) func(name string) (Provider, error) {
	return func(name string) (Provider, error) {
		switch name {
		case "anthropic":
			return NewAnthropic(cfg.AnthropicBaseURL), nil
		case "openai":
			return NewOpenAI(cfg.OpenAIBaseURL), nil
		case "openrouter":
			base := cfg.OpenRouterBaseURL
			if base == "" {
				base = defaultOpenRouterBaseURL
			}
			return NewOpenAI(base), nil
		default:
			return nil, fmt.Errorf("llm: unknown provider %q (supported: anthropic, openai, openrouter)", name)
		}
	}
}
```

`server/internal/llm/llmtest/scripted.go`:

```go
// Package llmtest provides a scripted in-memory Provider for tests.
package llmtest

import (
	"context"

	"github.com/gambtho/nightwatch/server/internal/llm"
)

type Scripted struct {
	Response string
	Usage    llm.Usage
	Err      error
	Calls    [][]llm.Message
}

func (s *Scripted) Chat(ctx context.Context, msgs []llm.Message, opts llm.CallOptions, onChunk func(llm.StreamChunk)) (llm.Usage, error) {
	s.Calls = append(s.Calls, msgs)
	if s.Err != nil {
		return llm.Usage{}, s.Err
	}
	if onChunk != nil {
		onChunk(llm.StreamChunk{Delta: s.Response})
	}
	return s.Usage, nil
}
```

- [ ] **Step 5: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS. If the copied provider files fail to compile against the pinned SDKs, fix imports/removed-field references only — no behavioral edits.

- [ ] **Step 6: Commit**

```bash
git add server
git commit -m "feat(server): port anthropic/openai/openrouter providers and pricing from cronfoundry"
```

---

### Task 6: Run-token signer

**Files:**

- Create: `server/internal/token/token.go`
- Test: `server/internal/token/token_test.go`
- Reference (read-only): `/home/tng/workspace/cronfoundry/internal/token/`

**Interfaces:**

- Consumes: nothing internal.
- Produces:

```go
type RunClaims struct {
	RunID     uuid.UUID
	TenantID  uuid.UUID
	ExpiresAt time.Time
}
func New(master []byte) *Signer                                  // HKDF-SHA256, info "nightshift:run-jwt"
func (s *Signer) Sign(c RunClaims) (token, hash string, err error) // HS256 JWT + sha256-hex of the token
func (s *Signer) Verify(bearer string) (RunClaims, error)
func (s *Signer) HashToken(tok string) string
```

- [ ] **Step 1: Write the failing test**

`server/internal/token/token_test.go`:

```go
package token_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/token"
)

func TestSignVerify(t *testing.T) {
	s := token.New([]byte("0123456789abcdef0123456789abcdef"))
	claims := token.RunClaims{
		RunID: uuid.New(), TenantID: uuid.New(),
		ExpiresAt: time.Now().Add(time.Hour),
	}
	tok, hash, err := s.Sign(claims)
	require.NoError(t, err)
	require.Equal(t, s.HashToken(tok), hash)

	got, err := s.Verify(tok)
	require.NoError(t, err)
	require.Equal(t, claims.RunID, got.RunID)
	require.Equal(t, claims.TenantID, got.TenantID)
}

func TestVerifyRejects(t *testing.T) {
	s := token.New([]byte("0123456789abcdef0123456789abcdef"))
	other := token.New([]byte("ffffffffffffffffffffffffffffffff"))

	claims := token.RunClaims{RunID: uuid.New(), TenantID: uuid.New(), ExpiresAt: time.Now().Add(time.Hour)}
	tok, _, err := s.Sign(claims)
	require.NoError(t, err)
	_, err = other.Verify(tok) // wrong key
	require.Error(t, err)

	expired := token.RunClaims{RunID: uuid.New(), TenantID: uuid.New(), ExpiresAt: time.Now().Add(-time.Minute)}
	tok, _, err = s.Sign(expired)
	require.NoError(t, err)
	_, err = s.Verify(tok)
	require.Error(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go get github.com/golang-jwt/jwt/v5@latest golang.org/x/crypto@latest && go test ./internal/token/`
Expected: FAIL (compile error — package does not exist).

- [ ] **Step 3: Implement**

`server/internal/token/token.go` (shape harvested from cronfoundry `internal/token`, with `TenantID` in place of `OrgID` and no `SecretRefs` — secret grants arrive with Plan 2):

```go
// Package token signs and verifies the per-run bearer JWT the harness uses
// against the internal API. The stored hash lets the control plane hold
// proof of issuance without holding the plaintext token.
package token

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/hkdf"
)

const hkdfInfo = "nightshift:run-jwt"

type RunClaims struct {
	RunID     uuid.UUID
	TenantID  uuid.UUID
	ExpiresAt time.Time
}

type Signer struct {
	key []byte
}

// New derives the signing key from the master key so the master itself is
// never used directly for HMAC.
func New(master []byte) *Signer {
	r := hkdf.New(sha256.New, master, nil, []byte(hkdfInfo))
	key := make([]byte, 32)
	if _, err := io.ReadFull(r, key); err != nil {
		panic(fmt.Sprintf("token: hkdf: %v", err)) // cannot fail for sha256
	}
	return &Signer{key: key}
}

func (s *Signer) Sign(c RunClaims) (string, string, error) {
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"run_id": c.RunID.String(),
		"tid":    c.TenantID.String(),
		"exp":    c.ExpiresAt.Unix(),
	})
	tok, err := t.SignedString(s.key)
	if err != nil {
		return "", "", err
	}
	return tok, s.HashToken(tok), nil
}

func (s *Signer) Verify(bearer string) (RunClaims, error) {
	var out RunClaims
	parsed, err := jwt.Parse(bearer,
		func(t *jwt.Token) (any, error) { return s.key, nil },
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return out, err
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return out, errors.New("token: unexpected claims type")
	}
	runID, _ := claims["run_id"].(string)
	tid, _ := claims["tid"].(string)
	if out.RunID, err = uuid.Parse(runID); err != nil {
		return out, errors.New("token: bad run_id")
	}
	if out.TenantID, err = uuid.Parse(tid); err != nil {
		return out, errors.New("token: bad tid")
	}
	exp, err := claims.GetExpirationTime()
	if err != nil || exp == nil {
		return out, errors.New("token: bad exp")
	}
	out.ExpiresAt = exp.Time
	return out, nil
}

func (s *Signer) HashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// EqualHash compares two token hashes in constant time.
func EqualHash(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
```

- [ ] **Step 4: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server
git commit -m "feat(server): run-token signer with tenant claims"
```

---

### Task 7: Run and run-event store

**Files:**

- Create: `server/internal/db/migrations/00004_run.sql`, `server/internal/store/run.go`
- Test: `server/internal/store/run_test.go`

**Interfaces:**

- Consumes: Tasks 1 and 3.
- Produces:

```go
type Run struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	WorkflowID uuid.UUID
	Version    int
	Status     string // pending | running | succeeded | failed
	StartedAt  *time.Time
	FinishedAt *time.Time
	TokensIn   *int
	TokensOut  *int
	CostCents  *int
	ErrorKind  *string
	ErrorMsg   *string
	Output     *string
	TokenHash  string // sha256 of the run bearer; internal API compares it
	CreatedAt  time.Time
}
type RunEvent struct {
	ID        int64
	RunID     uuid.UUID
	Type      string
	Payload   json.RawMessage
	CreatedAt time.Time
}
type RunFinal struct {
	Status    string
	ErrorKind string
	ErrorMsg  string
	Output    string
	TokensIn  int
	TokensOut int
	CostCents int
}
func (s *Store) CreateRun(ctx context.Context, tenantID, workflowID, id uuid.UUID, version int, tokenHash string) (Run, error)
func (s *Store) GetRun(ctx context.Context, tenantID, id uuid.UUID) (Run, error)
func (s *Store) ListRuns(ctx context.Context, tenantID, workflowID uuid.UUID) ([]Run, error)
func (s *Store) MarkRunRunning(ctx context.Context, tenantID, id uuid.UUID) error
func (s *Store) FinalizeRun(ctx context.Context, tenantID, id uuid.UUID, fin RunFinal) (Run, error)
func (s *Store) AppendRunEvent(ctx context.Context, tenantID, runID uuid.UUID, typ string, payload json.RawMessage) error
func (s *Store) ListRunEvents(ctx context.Context, tenantID, runID uuid.UUID) ([]RunEvent, error)
```

Note `CreateRun` takes the id: the caller generates it so the run token (which embeds the id) can be signed before the row exists.

Terminal runs are **immutable**: `FinalizeRun` and `AppendRunEvent` only touch runs whose status is `pending` or `running` (returning `ErrNotFound` otherwise). The run record is the audit surface; a bearer must not be able to rewrite it after completion.

- [ ] **Step 1: Write the migration**

`server/internal/db/migrations/00004_run.sql`:

```sql
-- +goose Up
CREATE TABLE run (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    workflow_id uuid NOT NULL,
    version int NOT NULL,
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'succeeded', 'failed')),
    fire_reason text NOT NULL DEFAULT 'manual'
        CHECK (fire_reason IN ('manual', 'schedule')),
    started_at timestamptz,
    finished_at timestamptz,
    tokens_in int,
    tokens_out int,
    cost_cents int,
    error_kind text,
    error_msg text,
    output text,
    runner_token_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, workflow_id)
        REFERENCES workflow (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workflow_id, version)
        REFERENCES workflow_version (workflow_id, version)
);

CREATE INDEX run_workflow_created_idx ON run (workflow_id, created_at DESC);

CREATE TABLE run_event (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    run_id uuid NOT NULL REFERENCES run (id) ON DELETE CASCADE,
    tenant_id uuid NOT NULL,
    type text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX run_event_run_idx ON run_event (run_id, id);

-- +goose Down
DROP TABLE run_event;
DROP TABLE run;
```

(`fire_reason` is always `manual` in this plan; the column and check exist so Plan 3's scheduler doesn't need a migration to the core table.)

- [ ] **Step 2: Write the failing tests**

`server/internal/store/run_test.go`:

```go
package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
)

func setupApproved(t *testing.T, s *store.Store) (store.Tenant, store.Workflow) {
	t.Helper()
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "acme")
	require.NoError(t, err)
	user, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)
	wf, _, err := s.CreateWorkflow(ctx, tn.ID, "weekly digest", testDoc())
	require.NoError(t, err)
	_, err = s.ApproveVersion(ctx, tn.ID, wf.ID, 1, user.ID)
	require.NoError(t, err)
	return tn, wf
}

func TestRunLifecycle(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, wf := setupApproved(t, s)

	runID := uuid.New()
	run, err := s.CreateRun(ctx, tn.ID, wf.ID, runID, 1, "hash123")
	require.NoError(t, err)
	require.Equal(t, "pending", run.Status)

	require.NoError(t, s.MarkRunRunning(ctx, tn.ID, runID))
	require.NoError(t, s.AppendRunEvent(ctx, tn.ID, runID, "run.start", json.RawMessage(`{}`)))

	final, err := s.FinalizeRun(ctx, tn.ID, runID, store.RunFinal{
		Status: "succeeded", Output: "the digest",
		TokensIn: 100, TokensOut: 50, CostCents: 3,
	})
	require.NoError(t, err)
	require.Equal(t, "succeeded", final.Status)
	require.NotNil(t, final.FinishedAt)
	require.Equal(t, "the digest", *final.Output)
	require.Equal(t, 100, *final.TokensIn)

	events, err := s.ListRunEvents(ctx, tn.ID, runID)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "run.start", events[0].Type)

	runs, err := s.ListRuns(ctx, tn.ID, wf.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1)

	// Terminal runs are immutable: no late events, no re-finalization.
	err = s.AppendRunEvent(ctx, tn.ID, runID, "late", json.RawMessage(`{}`))
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = s.FinalizeRun(ctx, tn.ID, runID, store.RunFinal{Status: "failed"})
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestRunCrossTenantIsolation(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, wf := setupApproved(t, s)
	other, err := s.CreateTenant(ctx, "other")
	require.NoError(t, err)

	runID := uuid.New()
	_, err = s.CreateRun(ctx, tn.ID, wf.ID, runID, 1, "hash123")
	require.NoError(t, err)

	_, err = s.GetRun(ctx, other.ID, runID)
	require.ErrorIs(t, err, store.ErrNotFound)
	err = s.AppendRunEvent(ctx, other.ID, runID, "x", json.RawMessage(`{}`))
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = s.FinalizeRun(ctx, other.ID, runID, store.RunFinal{Status: "succeeded"})
	require.ErrorIs(t, err, store.ErrNotFound)

	// Creating a run against another tenant's workflow must fail.
	_, err = s.CreateRun(ctx, other.ID, wf.ID, uuid.New(), 1, "h")
	require.Error(t, err)
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd server && go test ./internal/store/`
Expected: FAIL (compile error — `undefined: s.CreateRun`).

- [ ] **Step 4: Implement**

`server/internal/store/run.go`:

```go
package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Run struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	WorkflowID uuid.UUID
	Version    int
	Status     string
	StartedAt  *time.Time
	FinishedAt *time.Time
	TokensIn   *int
	TokensOut  *int
	CostCents  *int
	ErrorKind  *string
	ErrorMsg   *string
	Output     *string
	TokenHash  string
	CreatedAt  time.Time
}

type RunEvent struct {
	ID        int64
	RunID     uuid.UUID
	Type      string
	Payload   json.RawMessage
	CreatedAt time.Time
}

type RunFinal struct {
	Status    string
	ErrorKind string
	ErrorMsg  string
	Output    string
	TokensIn  int
	TokensOut int
	CostCents int
}

const runCols = `id, tenant_id, workflow_id, version, status, started_at,
	finished_at, tokens_in, tokens_out, cost_cents, error_kind, error_msg,
	output, runner_token_hash, created_at`

func scanRun(row pgx.Row) (Run, error) {
	var r Run
	err := row.Scan(&r.ID, &r.TenantID, &r.WorkflowID, &r.Version, &r.Status,
		&r.StartedAt, &r.FinishedAt, &r.TokensIn, &r.TokensOut, &r.CostCents,
		&r.ErrorKind, &r.ErrorMsg, &r.Output, &r.TokenHash, &r.CreatedAt)
	return r, notFound(err)
}

func (s *Store) CreateRun(ctx context.Context, tenantID, workflowID, id uuid.UUID, version int, tokenHash string) (Run, error) {
	return scanRun(s.pool.QueryRow(ctx,
		`INSERT INTO run (id, tenant_id, workflow_id, version, runner_token_hash)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING `+runCols,
		id, tenantID, workflowID, version, tokenHash))
}

func (s *Store) GetRun(ctx context.Context, tenantID, id uuid.UUID) (Run, error) {
	return scanRun(s.pool.QueryRow(ctx,
		`SELECT `+runCols+` FROM run WHERE id = $1 AND tenant_id = $2`,
		id, tenantID))
}

func (s *Store) ListRuns(ctx context.Context, tenantID, workflowID uuid.UUID) ([]Run, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+runCols+` FROM run
		 WHERE workflow_id = $1 AND tenant_id = $2
		 ORDER BY created_at DESC`,
		workflowID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) MarkRunRunning(ctx context.Context, tenantID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE run SET status = 'running', started_at = COALESCE(started_at, now())
		 WHERE id = $1 AND tenant_id = $2 AND status IN ('pending', 'running')`,
		id, tenantID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) FinalizeRun(ctx context.Context, tenantID, id uuid.UUID, fin RunFinal) (Run, error) {
	return scanRun(s.pool.QueryRow(ctx,
		`UPDATE run SET status = $3, finished_at = now(),
		        tokens_in = $4, tokens_out = $5, cost_cents = $6,
		        error_kind = NULLIF($7, ''), error_msg = NULLIF($8, ''),
		        output = $9
		 WHERE id = $1 AND tenant_id = $2
		   AND status IN ('pending', 'running')
		 RETURNING `+runCols,
		id, tenantID, fin.Status, fin.TokensIn, fin.TokensOut, fin.CostCents,
		fin.ErrorKind, fin.ErrorMsg, fin.Output))
}

func (s *Store) AppendRunEvent(ctx context.Context, tenantID, runID uuid.UUID, typ string, payload json.RawMessage) error {
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	tag, err := s.pool.Exec(ctx,
		`INSERT INTO run_event (run_id, tenant_id, type, payload)
		 SELECT $1, $2, $3, $4
		 WHERE EXISTS (SELECT 1 FROM run WHERE id = $1 AND tenant_id = $2
		               AND status IN ('pending', 'running'))`,
		runID, tenantID, typ, payload)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListRunEvents(ctx context.Context, tenantID, runID uuid.UUID) ([]RunEvent, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, run_id, type, payload, created_at FROM run_event
		 WHERE run_id = $1 AND tenant_id = $2 ORDER BY id`,
		runID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunEvent
	for rows.Next() {
		var e RunEvent
		if err := rows.Scan(&e.ID, &e.RunID, &e.Type, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
```

- [ ] **Step 5: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add server
git commit -m "feat(server): run and run-event store"
```

---

### Task 8: The harness

**Files:**

- Create: `server/internal/harness/harness.go`
- Test: `server/internal/harness/harness_test.go`
- Reference (read-only): `/home/tng/workspace/cronfoundry/internal/runner/runner.go` (the loop being reshaped)

**Interfaces:**

- Consumes: `llm` (Task 5), `llmtest`.
- Produces:

```go
type Status string
const (
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)
type Steps struct { // mirrors store.StepsDoc's JSON, without importing store
	SystemPrompt string `json:"system_prompt"`
	Kickoff      string `json:"kickoff"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	MaxTokens    int    `json:"max_tokens"`
}
type Input struct {
	Steps  Steps
	APIKey string
}
type RunEvent struct {
	Type    string
	Payload map[string]any
}
type Result struct {
	Status     Status
	ErrorKind  string
	ErrorMsg   string
	Output     string
	Usage      llm.Usage
	CostCents  int
	StartedAt  time.Time
	FinishedAt time.Time
}
type Sink interface {
	Event(ctx context.Context, ev RunEvent) error
	Finalize(ctx context.Context, res Result) error
}
type Deps struct {
	ProviderFactory func(name string) (llm.Provider, error)
	Sink            Sink
	Now             func() time.Time
}
func Run(ctx context.Context, in Input, d Deps) (Result, error)
```

- [ ] **Step 1: Write the failing tests**

`server/internal/harness/harness_test.go`:

```go
package harness_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/harness"
	"github.com/gambtho/nightwatch/server/internal/llm"
	"github.com/gambtho/nightwatch/server/internal/llm/llmtest"
)

type memSink struct {
	events   []harness.RunEvent
	final    *harness.Result
	finalErr error
}

func (m *memSink) Event(ctx context.Context, ev harness.RunEvent) error {
	m.events = append(m.events, ev)
	return nil
}

func (m *memSink) Finalize(ctx context.Context, res harness.Result) error {
	m.final = &res
	return m.finalErr
}

func steps() harness.Steps {
	return harness.Steps{
		SystemPrompt: "You prepare the weekly support digest.",
		Kickoff:      "Summarize last week's tickets.",
		Provider:     "scripted",
		Model:        "test-model",
		MaxTokens:    1024,
	}
}

func TestRunSuccess(t *testing.T) {
	provider := &llmtest.Scripted{Response: "the digest", Usage: llm.Usage{InputTokens: 100, OutputTokens: 50}}
	sink := &memSink{}
	res, err := harness.Run(context.Background(), harness.Input{Steps: steps()}, harness.Deps{
		ProviderFactory: func(string) (llm.Provider, error) { return provider, nil },
		Sink:            sink,
	})
	require.NoError(t, err)
	require.Equal(t, harness.StatusSucceeded, res.Status)
	require.Equal(t, "the digest", res.Output)
	require.Equal(t, 100, res.Usage.InputTokens)

	// System prompt and kickoff reached the model.
	require.Len(t, provider.Calls, 1)
	require.Equal(t, llm.RoleSystem, provider.Calls[0][0].Role)
	require.Equal(t, llm.RoleUser, provider.Calls[0][1].Role)

	// Events and finalization were pushed.
	require.NotNil(t, sink.final)
	require.Equal(t, harness.StatusSucceeded, sink.final.Status)
	var types []string
	for _, ev := range sink.events {
		types = append(types, ev.Type)
	}
	require.Equal(t, []string{"run.start", "run.finish"}, types)
}

func TestRunProviderError(t *testing.T) {
	provider := &llmtest.Scripted{Err: errors.New("model unavailable")}
	sink := &memSink{}
	res, err := harness.Run(context.Background(), harness.Input{Steps: steps()}, harness.Deps{
		ProviderFactory: func(string) (llm.Provider, error) { return provider, nil },
		Sink:            sink,
	})
	require.Error(t, err)
	require.Equal(t, harness.StatusFailed, res.Status)
	require.Equal(t, "llm_error", res.ErrorKind)
	require.NotNil(t, sink.final)
	require.Equal(t, harness.StatusFailed, sink.final.Status)
}

func TestRunUnknownProvider(t *testing.T) {
	res, err := harness.Run(context.Background(), harness.Input{Steps: steps()}, harness.Deps{
		ProviderFactory: func(string) (llm.Provider, error) { return nil, errors.New("nope") },
		Sink:            &memSink{},
	})
	require.Error(t, err)
	require.Equal(t, "provider_unknown", res.ErrorKind)
}

func TestRunFinalizeErrorSurfaces(t *testing.T) {
	// The finalization IS the run record (records are pushed, never
	// pulled), so failing to deliver it must not look like success.
	provider := &llmtest.Scripted{Response: "the digest"}
	sink := &memSink{finalErr: errors.New("control plane unreachable")}
	res, err := harness.Run(context.Background(), harness.Input{Steps: steps()}, harness.Deps{
		ProviderFactory: func(string) (llm.Provider, error) { return provider, nil },
		Sink:            sink,
	})
	require.Error(t, err)
	require.Equal(t, harness.StatusSucceeded, res.Status) // the work succeeded; recording it did not
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd server && go test ./internal/harness/`
Expected: FAIL (package does not exist).

- [ ] **Step 3: Implement**

`server/internal/harness/harness.go`:

```go
// Package harness is the agent loop that executes one run. It is the code
// that will later live inside a Substrate actor, which is why it reports
// results only through its Sink (run records are pushed, never pulled) and
// receives everything else through Input and Deps.
//
// This is the tool-less reshape of cronfoundry's internal/runner: the
// filesystem manifest/skill loading, git writeback, memory extraction, and
// MCP subprocess management did not survive the move to a hosted platform
// (see the platform spec's harvest table). The ChatTurn tool loop returns
// with connector work.
package harness

import (
	"context"
	"strings"
	"time"

	"github.com/gambtho/nightwatch/server/internal/llm"
)

type Status string

const (
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)

const defaultMaxTokens = 4096

type Steps struct {
	SystemPrompt string `json:"system_prompt"`
	Kickoff      string `json:"kickoff"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	MaxTokens    int    `json:"max_tokens"`
}

type Input struct {
	Steps  Steps
	APIKey string
}

type RunEvent struct {
	Type    string
	Payload map[string]any
}

type Result struct {
	Status     Status
	ErrorKind  string
	ErrorMsg   string
	Output     string
	Usage      llm.Usage
	CostCents  int
	StartedAt  time.Time
	FinishedAt time.Time
}

type Sink interface {
	Event(ctx context.Context, ev RunEvent) error
	Finalize(ctx context.Context, res Result) error
}

type Deps struct {
	ProviderFactory func(name string) (llm.Provider, error)
	Sink            Sink
	Now             func() time.Time
}

func Run(ctx context.Context, in Input, d Deps) (Result, error) {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	res := Result{StartedAt: now()}

	// Events are best-effort telemetry; the finalization is the run record
	// itself, so a Finalize failure surfaces to the caller.
	emit := func(typ string, payload map[string]any) {
		if d.Sink != nil {
			_ = d.Sink.Event(ctx, RunEvent{Type: typ, Payload: payload})
		}
	}
	finish := func() error {
		res.FinishedAt = now()
		if d.Sink == nil {
			return nil
		}
		return d.Sink.Finalize(ctx, res)
	}
	fail := func(kind string, err error) (Result, error) {
		res.Status = StatusFailed
		res.ErrorKind = kind
		res.ErrorMsg = err.Error()
		emit("run.fail", map[string]any{"kind": kind})
		if ferr := finish(); ferr != nil {
			err = errors.Join(err, ferr)
		}
		return res, err
	}

	emit("run.start", nil)

	provider, err := d.ProviderFactory(in.Steps.Provider)
	if err != nil {
		return fail("provider_unknown", err)
	}

	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: in.Steps.SystemPrompt},
		{Role: llm.RoleUser, Content: in.Steps.Kickoff},
	}
	maxTokens := in.Steps.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	var out strings.Builder
	usage, err := provider.Chat(ctx, msgs,
		llm.CallOptions{Model: in.Steps.Model, MaxTokens: maxTokens, APIKey: in.APIKey},
		func(c llm.StreamChunk) { out.WriteString(c.Delta) })
	if err != nil {
		return fail("llm_error", err)
	}

	res.Usage = usage
	res.CostCents = llm.CostCents(in.Steps.Provider, in.Steps.Model, usage)
	res.Output = out.String()
	res.Status = StatusSucceeded
	emit("run.finish", map[string]any{"status": string(res.Status)})
	if err := finish(); err != nil {
		return res, err
	}
	return res, nil
}
```

(Add `"errors"` to the imports.)

- [ ] **Step 4: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server
git commit -m "feat(server): tool-less harness with pushed events and finalization"
```

---

### Task 9: Internal API and the harness HTTP client

**Files:**

- Create: `server/internal/internalapi/internalapi.go`, `server/internal/harness/client.go`
- Test: `server/internal/internalapi/internalapi_test.go`

**Interfaces:**

- Consumes: `store` (Tasks 3, 7), `token` (Task 6), `harness` types (Task 8).
- Produces: `internalapi.Deps{Store *store.Store; Signer *token.Signer}`; `internalapi.RegisterRoutes(mux *http.ServeMux, d Deps)` with routes `GET /internal/runs/{id}/context`, `POST /internal/runs/{id}/events`, `POST /internal/runs/{id}/finalize` (bearer run-JWT; the claims' `RunID` must equal the path id — a runner can only touch its own run — **and** the bearer's sha256 must match the run's stored `runner_token_hash`, which binds the JWT to the row and gives the control plane a revocation lever; request bodies are capped at 1 MiB; fetching context marks the run running). And `harness.NewClient(base string, runID uuid.UUID, bearer string) *Client` with `(*Client).Context(ctx) (Steps, error)` plus `Event`/`Finalize` making `*Client` satisfy `harness.Sink`.

Wire shapes:

```json
GET  /internal/runs/{id}/context  -> 200 {"run_id": "...", "steps": {"system_prompt": ..., ...}}
POST /internal/runs/{id}/events   {"type": "run.start", "payload": {}} -> 204
POST /internal/runs/{id}/finalize {"status": "succeeded", "error_kind": "", "error_msg": "", "output": "...", "tokens_in": 100, "tokens_out": 50, "cost_cents": 3} -> 204
```

- [ ] **Step 1: Write the failing tests**

`server/internal/internalapi/internalapi_test.go`:

```go
package internalapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/harness"
	"github.com/gambtho/nightwatch/server/internal/internalapi"
	"github.com/gambtho/nightwatch/server/internal/llm"
	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
	"github.com/gambtho/nightwatch/server/internal/token"
)

func setup(t *testing.T) (*store.Store, *token.Signer, *httptest.Server, store.Tenant, store.Workflow) {
	t.Helper()
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "acme")
	require.NoError(t, err)
	user, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)
	wf, _, err := s.CreateWorkflow(ctx, tn.ID, "weekly digest", store.VersionDoc{
		Steps: store.StepsDoc{
			SystemPrompt: "You prepare the weekly support digest.",
			Kickoff:      "Summarize last week's tickets.",
			Provider:     "anthropic",
			Model:        "claude-sonnet-5",
			MaxTokens:    2048,
		},
		Permit: []byte(`{}`), Rubric: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = s.ApproveVersion(ctx, tn.ID, wf.ID, 1, user.ID)
	require.NoError(t, err)

	signer := token.New([]byte("0123456789abcdef0123456789abcdef"))
	mux := http.NewServeMux()
	internalapi.RegisterRoutes(mux, internalapi.Deps{Store: s, Signer: signer})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return s, signer, ts, tn, wf
}

func mintRun(t *testing.T, s *store.Store, signer *token.Signer, tn store.Tenant, wf store.Workflow) (uuid.UUID, string) {
	t.Helper()
	runID := uuid.New()
	bearer, hash, err := signer.Sign(token.RunClaims{
		RunID: runID, TenantID: tn.ID, ExpiresAt: time.Now().Add(time.Hour),
	})
	require.NoError(t, err)
	_, err = s.CreateRun(context.Background(), tn.ID, wf.ID, runID, 1, hash)
	require.NoError(t, err)
	return runID, bearer
}

func TestHarnessClientAgainstInternalAPI(t *testing.T) {
	s, signer, ts, tn, wf := setup(t)
	ctx := context.Background()
	runID, bearer := mintRun(t, s, signer, tn, wf)

	client := harness.NewClient(ts.URL, runID, bearer)

	steps, err := client.Context(ctx)
	require.NoError(t, err)
	require.Equal(t, "anthropic", steps.Provider)

	// Context fetch marked the run running.
	run, err := s.GetRun(ctx, tn.ID, runID)
	require.NoError(t, err)
	require.Equal(t, "running", run.Status)

	require.NoError(t, client.Event(ctx, harness.RunEvent{Type: "run.start"}))
	require.NoError(t, client.Finalize(ctx, harness.Result{
		Status: harness.StatusSucceeded, Output: "the digest",
		Usage: llm.Usage{InputTokens: 100, OutputTokens: 50}, CostCents: 3,
	}))

	run, err = s.GetRun(ctx, tn.ID, runID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", run.Status)
	require.Equal(t, "the digest", *run.Output)
	require.Equal(t, 100, *run.TokensIn)

	events, err := s.ListRunEvents(ctx, tn.ID, runID)
	require.NoError(t, err)
	require.Len(t, events, 1)
}

func TestInternalAPIAuth(t *testing.T) {
	s, signer, ts, tn, wf := setup(t)
	runID, _ := mintRun(t, s, signer, tn, wf)
	otherRunID, otherBearer := mintRun(t, s, signer, tn, wf)
	_ = otherRunID

	// No bearer: 401.
	resp, err := http.Get(ts.URL + "/internal/runs/" + runID.String() + "/context")
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// A token for another run must not open this run.
	req, err := http.NewRequest("GET", ts.URL+"/internal/runs/"+runID.String()+"/context", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+otherBearer)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestInternalAPIRejectsMismatchedTokenHash(t *testing.T) {
	// A structurally valid JWT is not enough: the bearer must be the exact
	// token minted for the run (the stored hash is the revocation lever).
	s, signer, ts, tn, wf := setup(t)
	runID := uuid.New()
	bearer, _, err := signer.Sign(token.RunClaims{
		RunID: runID, TenantID: tn.ID, ExpiresAt: time.Now().Add(time.Hour),
	})
	require.NoError(t, err)
	_, err = s.CreateRun(context.Background(), tn.ID, wf.ID, runID, 1, "not-the-real-hash")
	require.NoError(t, err)

	req, err := http.NewRequest("GET", ts.URL+"/internal/runs/"+runID.String()+"/context", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd server && go test ./internal/internalapi/`
Expected: FAIL (packages/identifiers do not exist).

- [ ] **Step 3: Implement the internal API**

`server/internal/internalapi/internalapi.go`:

```go
// Package internalapi is the harness-facing API: run context out, run
// records in. Substrate exposes no log or event retrieval API, so run
// records exist only because the harness pushes them here.
package internalapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/token"
)

type Deps struct {
	Store  *store.Store
	Signer *token.Signer
}

func RegisterRoutes(mux *http.ServeMux, d Deps) {
	mux.Handle("GET /internal/runs/{id}/context", d.auth(d.runContext))
	mux.Handle("POST /internal/runs/{id}/events", d.auth(d.appendEvent))
	mux.Handle("POST /internal/runs/{id}/finalize", d.auth(d.finalize))
}

type authedHandler func(w http.ResponseWriter, r *http.Request, claims token.RunClaims)

// The harness pushes small JSON records, not data; cap every body. Per-run
// event and output budgets are the metering plan's concern (Plan 3).
const maxBodyBytes = 1 << 20

// auth verifies the bearer run-JWT, requires that the token's run is the
// run in the path (a runner can only touch its own run), and requires the
// bearer to be the exact token minted for that run — the stored hash binds
// the JWT to the row and clearing it revokes the token.
func (d Deps) auth(next authedHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		claims, err := d.Signer.Verify(bearer)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		pathID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad run id", http.StatusBadRequest)
			return
		}
		if claims.RunID != pathID {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		run, err := d.Store.GetRun(r.Context(), claims.TenantID, claims.RunID)
		if err != nil {
			d.fail(w, err)
			return
		}
		if !token.EqualHash(d.Signer.HashToken(bearer), run.TokenHash) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r, claims)
	})
}

func (d Deps) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	slog.Error("internalapi: internal error", "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func (d Deps) runContext(w http.ResponseWriter, r *http.Request, claims token.RunClaims) {
	ctx := r.Context()
	run, err := d.Store.GetRun(ctx, claims.TenantID, claims.RunID)
	if err != nil {
		d.fail(w, err)
		return
	}
	version, err := d.Store.GetVersion(ctx, claims.TenantID, run.WorkflowID, run.Version)
	if err != nil {
		d.fail(w, err)
		return
	}
	if err := d.Store.MarkRunRunning(ctx, claims.TenantID, claims.RunID); err != nil {
		d.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"run_id": run.ID,
		"steps":  version.Doc.Steps,
	}); err != nil {
		slog.Error("internalapi: encode context", "err", err)
	}
}

func (d Deps) appendEvent(w http.ResponseWriter, r *http.Request, claims token.RunClaims) {
	var body struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Type == "" {
		http.Error(w, "bad event", http.StatusBadRequest)
		return
	}
	if err := d.Store.AppendRunEvent(r.Context(), claims.TenantID, claims.RunID, body.Type, body.Payload); err != nil {
		d.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) finalize(w http.ResponseWriter, r *http.Request, claims token.RunClaims) {
	var body struct {
		Status    string `json:"status"`
		ErrorKind string `json:"error_kind"`
		ErrorMsg  string `json:"error_msg"`
		Output    string `json:"output"`
		TokensIn  int    `json:"tokens_in"`
		TokensOut int    `json:"tokens_out"`
		CostCents int    `json:"cost_cents"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad finalize", http.StatusBadRequest)
		return
	}
	if body.Status != "succeeded" && body.Status != "failed" {
		http.Error(w, "bad status", http.StatusBadRequest)
		return
	}
	_, err := d.Store.FinalizeRun(r.Context(), claims.TenantID, claims.RunID, store.RunFinal{
		Status: body.Status, ErrorKind: body.ErrorKind, ErrorMsg: body.ErrorMsg,
		Output: body.Output, TokensIn: body.TokensIn, TokensOut: body.TokensOut,
		CostCents: body.CostCents,
	})
	if err != nil {
		d.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Implement the harness client**

`server/internal/harness/client.go`:

```go
package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// Client is the harness's channel back to the control plane. It is the
// same channel a sandboxed actor will use later, which is why the local
// Compute implementation goes over HTTP instead of calling the store.
type Client struct {
	base   string
	runID  uuid.UUID
	bearer string
	hc     *http.Client
}

func NewClient(base string, runID uuid.UUID, bearer string) *Client {
	return &Client{
		base:   base,
		runID:  runID,
		bearer: bearer,
		hc:     &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("harness client: %s %s: %s", method, path, resp.Status)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *Client) Context(ctx context.Context) (Steps, error) {
	var body struct {
		Steps Steps `json:"steps"`
	}
	err := c.do(ctx, "GET", "/internal/runs/"+c.runID.String()+"/context", nil, &body)
	return body.Steps, err
}

func (c *Client) Event(ctx context.Context, ev RunEvent) error {
	payload := ev.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	return c.do(ctx, "POST", "/internal/runs/"+c.runID.String()+"/events",
		map[string]any{"type": ev.Type, "payload": payload}, nil)
}

func (c *Client) Finalize(ctx context.Context, res Result) error {
	return c.do(ctx, "POST", "/internal/runs/"+c.runID.String()+"/finalize",
		map[string]any{
			"status":     string(res.Status),
			"error_kind": res.ErrorKind,
			"error_msg":  res.ErrorMsg,
			"output":     res.Output,
			"tokens_in":  res.Usage.InputTokens,
			"tokens_out": res.Usage.OutputTokens,
			"cost_cents": res.CostCents,
		}, nil)
}
```

- [ ] **Step 5: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add server
git commit -m "feat(server): internal run API and harness HTTP client"
```

---

### Task 10: The Compute seam and the local implementation

**Files:**

- Create: `server/internal/compute/compute.go`, `server/internal/compute/local.go`
- Test: `server/internal/compute/local_test.go`

**Interfaces:**

- Consumes: nothing internal (`uuid` only) — the seam must not know about the store or harness.
- Produces:

```go
type ActorID string
type WorkflowRef struct{ TenantID, WorkflowID uuid.UUID }
type TemplateRef struct{ Name string }
type InvokeRequest struct {
	RunID    uuid.UUID
	RunToken string
}
type Handle struct {
	ActorID ActorID
	RunID   uuid.UUID
}
type Compute interface {
	EnsureActor(ctx context.Context, w WorkflowRef, tmpl TemplateRef) (ActorID, error)
	Invoke(ctx context.Context, a ActorID, payload InvokeRequest) (Handle, error)
	Suspend(ctx context.Context, a ActorID) error
	Destroy(ctx context.Context, a ActorID) error
}
type RunnerFunc func(ctx context.Context, req InvokeRequest, stateDir string)
func NewLocal(baseDir string, runner RunnerFunc) *Local  // implements Compute
func (l *Local) Wait()                                   // test/shutdown helper: block until in-flight invocations finish
```

Invocations of the same actor **serialize**: the spec's overlap policy is "default to
serialize; treat concurrency as a later, explicit feature", and the actor's persistent
state directory makes concurrent runs a data race, not a feature. Different actors run
concurrently.

- [ ] **Step 1: Write the failing tests**

`server/internal/compute/local_test.go`:

```go
package compute_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/compute"
)

func TestLocalActorStatePersistsAcrossInvokes(t *testing.T) {
	var mu sync.Mutex
	var dirs []string
	runner := func(ctx context.Context, req compute.InvokeRequest, stateDir string) {
		mu.Lock()
		defer mu.Unlock()
		dirs = append(dirs, stateDir)
		// The actor's working state must survive between fires: a workflow
		// is a long-lived actor, a run is an episode in its life.
		// (t.Error, not require: this runs off the test goroutine.)
		path := filepath.Join(stateDir, "memory.txt")
		prev, _ := os.ReadFile(path)
		if err := os.WriteFile(path, append(prev, 'x'), 0o644); err != nil {
			t.Error(err)
		}
	}

	local := compute.NewLocal(t.TempDir(), runner)
	ctx := context.Background()
	ref := compute.WorkflowRef{TenantID: uuid.New(), WorkflowID: uuid.New()}

	actor, err := local.EnsureActor(ctx, ref, compute.TemplateRef{Name: "harness-v1"})
	require.NoError(t, err)

	_, err = local.Invoke(ctx, actor, compute.InvokeRequest{RunID: uuid.New()})
	require.NoError(t, err)
	local.Wait()

	// EnsureActor is idempotent: same ref, same actor, same state.
	actor2, err := local.EnsureActor(ctx, ref, compute.TemplateRef{Name: "harness-v1"})
	require.NoError(t, err)
	require.Equal(t, actor, actor2)

	_, err = local.Invoke(ctx, actor2, compute.InvokeRequest{RunID: uuid.New()})
	require.NoError(t, err)
	local.Wait()

	require.Len(t, dirs, 2)
	require.Equal(t, dirs[0], dirs[1])
	content, err := os.ReadFile(filepath.Join(dirs[0], "memory.txt"))
	require.NoError(t, err)
	require.Equal(t, "xx", string(content))
}

func TestLocalDestroyRemovesState(t *testing.T) {
	local := compute.NewLocal(t.TempDir(),
		func(ctx context.Context, req compute.InvokeRequest, stateDir string) {})
	ctx := context.Background()
	ref := compute.WorkflowRef{TenantID: uuid.New(), WorkflowID: uuid.New()}
	actor, err := local.EnsureActor(ctx, ref, compute.TemplateRef{Name: "harness-v1"})
	require.NoError(t, err)
	require.NoError(t, local.Destroy(ctx, actor))
	// Idempotent.
	require.NoError(t, local.Destroy(ctx, actor))
}

func TestLocalInvokesSerializePerActor(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	runner := func(ctx context.Context, req compute.InvokeRequest, stateDir string) {
		entered <- struct{}{}
		<-release
	}
	local := compute.NewLocal(t.TempDir(), runner)
	ctx := context.Background()
	ref := compute.WorkflowRef{TenantID: uuid.New(), WorkflowID: uuid.New()}
	actor, err := local.EnsureActor(ctx, ref, compute.TemplateRef{Name: "harness-v1"})
	require.NoError(t, err)

	_, err = local.Invoke(ctx, actor, compute.InvokeRequest{RunID: uuid.New()})
	require.NoError(t, err)
	_, err = local.Invoke(ctx, actor, compute.InvokeRequest{RunID: uuid.New()})
	require.NoError(t, err)

	<-entered // the first run is in
	select {
	case <-entered:
		t.Fatal("second run entered while the first was still active")
	case <-time.After(100 * time.Millisecond):
		// serialized, as required
	}
	close(release)
	local.Wait()
}
```

(Add `"time"` to the test file's imports.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd server && go test ./internal/compute/`
Expected: FAIL (package does not exist).

- [ ] **Step 3: Implement**

`server/internal/compute/compute.go`:

```go
// Package compute is the seam between the control plane and whatever hosts
// actors. The platform spec mandates this interface so the pre-1.0
// Substrate dependency stays replaceable; the Substrate and Kubernetes-Jobs
// implementations are Plan 5. Local is the in-process implementation that
// keeps the seam honest from day one.
package compute

import (
	"context"

	"github.com/google/uuid"
)

type ActorID string

type WorkflowRef struct {
	TenantID   uuid.UUID
	WorkflowID uuid.UUID
}

// TemplateRef names the actor template (image + config). Local ignores it;
// Substrate's ActorTemplates are immutable, so workflow-version changes
// will map to new templates (governance primitive #8).
type TemplateRef struct {
	Name string
}

type InvokeRequest struct {
	RunID    uuid.UUID
	RunToken string
}

type Handle struct {
	ActorID ActorID
	RunID   uuid.UUID
}

type Compute interface {
	EnsureActor(ctx context.Context, w WorkflowRef, tmpl TemplateRef) (ActorID, error)
	Invoke(ctx context.Context, a ActorID, payload InvokeRequest) (Handle, error)
	Suspend(ctx context.Context, a ActorID) error
	Destroy(ctx context.Context, a ActorID) error
}
```

`server/internal/compute/local.go`:

```go
package compute

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// RunnerFunc executes one run. stateDir is the actor's persistent working
// directory: it survives across invocations of the same actor.
type RunnerFunc func(ctx context.Context, req InvokeRequest, stateDir string)

type Local struct {
	baseDir string
	runner  RunnerFunc
	wg      sync.WaitGroup

	mu     sync.Mutex
	actors map[ActorID]*sync.Mutex
}

func NewLocal(baseDir string, runner RunnerFunc) *Local {
	return &Local{
		baseDir: baseDir,
		runner:  runner,
		actors:  make(map[ActorID]*sync.Mutex),
	}
}

func (l *Local) dir(a ActorID) string {
	return filepath.Join(l.baseDir, string(a))
}

func (l *Local) lockFor(a ActorID) *sync.Mutex {
	l.mu.Lock()
	defer l.mu.Unlock()
	m, ok := l.actors[a]
	if !ok {
		m = &sync.Mutex{}
		l.actors[a] = m
	}
	return m
}

func (l *Local) EnsureActor(ctx context.Context, w WorkflowRef, tmpl TemplateRef) (ActorID, error) {
	a := ActorID(filepath.Join(w.TenantID.String(), w.WorkflowID.String()))
	if err := os.MkdirAll(l.dir(a), 0o755); err != nil {
		return "", err
	}
	return a, nil
}

func (l *Local) Invoke(ctx context.Context, a ActorID, payload InvokeRequest) (Handle, error) {
	dir := l.dir(a)
	if _, err := os.Stat(dir); err != nil {
		return Handle{}, fmt.Errorf("compute: unknown actor %q: %w", a, err)
	}
	l.wg.Add(1)
	// The run outlives the HTTP request that fired it.
	runCtx := context.WithoutCancel(ctx)
	go func() {
		defer l.wg.Done()
		// One actor, one run at a time: the spec's overlap policy is
		// "default to serialize", and the shared state directory makes
		// concurrent runs a data race.
		m := l.lockFor(a)
		m.Lock()
		defer m.Unlock()
		l.runner(runCtx, payload, dir)
	}()
	return Handle{ActorID: a, RunID: payload.RunID}, nil
}

// Suspend is a no-op locally; suspension is Substrate's economic premise,
// not ours to fake here.
func (l *Local) Suspend(ctx context.Context, a ActorID) error { return nil }

func (l *Local) Destroy(ctx context.Context, a ActorID) error {
	return os.RemoveAll(l.dir(a))
}

// Wait blocks until all in-flight invocations complete.
func (l *Local) Wait() { l.wg.Wait() }
```

- [ ] **Step 4: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server
git commit -m "feat(server): Compute seam with local actor implementation"
```

---

### Task 11: Run endpoints — firing through the seam

**Files:**

- Create: `server/internal/httpapi/runs.go`
- Modify: `server/internal/httpapi/httpapi.go` (add `Signer *token.Signer` and `Compute compute.Compute` to `Deps`; register the new routes)
- Test: `server/internal/httpapi/runs_test.go`

**Interfaces:**

- Consumes: everything above.
- Produces: routes `POST /v1/workflows/{id}/runs` (202; 409 if the workflow has no approved version), `GET /v1/workflows/{id}/runs`, `GET /v1/runs/{id}`, `GET /v1/runs/{id}/events`. `httpapi.Deps` is now `{Store *store.Store; SessionKey []byte; Signer *token.Signer; Compute compute.Compute}`.

Wire shapes:

```json
POST /v1/workflows/{id}/runs -> 202 {"run": {"id": "...", "workflow_id": "...", "version": 2, "status": "pending", ...}}
GET  /v1/runs/{id}           -> 200 {"run": {..., "status": "succeeded", "output": "...", "cost_cents": 3}}
GET  /v1/workflows/{id}/runs -> 200 {"runs": [ ... ]}
GET  /v1/runs/{id}/events    -> 200 {"events": [{"type": "run.start", "payload": {}, "created_at": "..."}]}
```

- [ ] **Step 1: Write the failing tests**

`server/internal/httpapi/runs_test.go` (extends Task 4's `env`; update `newEnv` to build the fuller `Deps` shown here):

```go
package httpapi_test

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/compute"
)

// fakeCompute records invocations instead of running anything.
type fakeCompute struct {
	mu        sync.Mutex
	invokes   []compute.InvokeRequest
	invokeErr error // when set, Invoke fails
}

func (f *fakeCompute) EnsureActor(ctx context.Context, w compute.WorkflowRef, tmpl compute.TemplateRef) (compute.ActorID, error) {
	return compute.ActorID(w.WorkflowID.String()), nil
}

func (f *fakeCompute) Invoke(ctx context.Context, a compute.ActorID, req compute.InvokeRequest) (compute.Handle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.invokeErr != nil {
		return compute.Handle{}, f.invokeErr
	}
	f.invokes = append(f.invokes, req)
	return compute.Handle{ActorID: a, RunID: req.RunID}, nil
}

func (f *fakeCompute) Suspend(ctx context.Context, a compute.ActorID) error { return nil }
func (f *fakeCompute) Destroy(ctx context.Context, a compute.ActorID) error { return nil }

func TestFireRunRequiresApprovedVersion(t *testing.T) {
	e := newEnv(t)

	resp, out := e.do(t, "POST", "/v1/workflows", workflowBody())
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	id := out["workflow"].(map[string]any)["id"].(string)

	// Draft only: firing is refused.
	resp, _ = e.do(t, "POST", fmt.Sprintf("/v1/workflows/%s/runs", id), nil)
	require.Equal(t, http.StatusConflict, resp.StatusCode)

	resp, _ = e.do(t, "POST", fmt.Sprintf("/v1/workflows/%s/versions/1/approve", id), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, out = e.do(t, "POST", fmt.Sprintf("/v1/workflows/%s/runs", id), nil)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	run := out["run"].(map[string]any)
	require.Equal(t, "pending", run["status"])
	require.Equal(t, float64(1), run["version"])

	// The seam was invoked with a signed token for this run.
	require.Len(t, e.compute.invokes, 1)
	require.Equal(t, run["id"], e.compute.invokes[0].RunID.String())
	require.NotEmpty(t, e.compute.invokes[0].RunToken)

	// The run is visible through the read endpoints.
	resp, out = e.do(t, "GET", "/v1/runs/"+run["id"].(string), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp, out = e.do(t, "GET", fmt.Sprintf("/v1/workflows/%s/runs", id), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, out["runs"], 1)
}

func TestFireRunDispatchFailureMarksRunFailed(t *testing.T) {
	e := newEnv(t)

	resp, out := e.do(t, "POST", "/v1/workflows", workflowBody())
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	id := out["workflow"].(map[string]any)["id"].(string)
	resp, _ = e.do(t, "POST", fmt.Sprintf("/v1/workflows/%s/versions/1/approve", id), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	e.compute.invokeErr = errors.New("no workers")
	resp, _ = e.do(t, "POST", fmt.Sprintf("/v1/workflows/%s/runs", id), nil)
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	// A dispatch failure must not leave the run pending forever.
	resp, out = e.do(t, "GET", fmt.Sprintf("/v1/workflows/%s/runs", id), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	runs := out["runs"].([]any)
	require.Len(t, runs, 1)
	require.Equal(t, "failed", runs[0].(map[string]any)["status"])
	require.Equal(t, "dispatch_failed", runs[0].(map[string]any)["error_kind"])
}
```

(Add `"errors"` to the test file's imports.)

And in `workflows_test.go`, update `env`/`newEnv`:

```go
// env gains: compute *fakeCompute
// newEnv builds Deps as:
//   fc := &fakeCompute{}
//   signer := token.New([]byte("0123456789abcdef0123456789abcdef"))
//   httpapi.RegisterRoutes(mux, httpapi.Deps{Store: s, SessionKey: key, Signer: signer, Compute: fc})
// and returns &env{..., compute: fc}
```

(Add imports `token` and set the two new fields; the diff is mechanical.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd server && go test ./internal/httpapi/`
Expected: FAIL (compile errors — `Deps` has no `Signer`/`Compute`, no runs routes).

- [ ] **Step 3: Implement**

In `server/internal/httpapi/httpapi.go`, extend `Deps` and routes:

```go
type Deps struct {
	Store      *store.Store
	SessionKey []byte
	Signer     *token.Signer
	Compute    compute.Compute
}
```

and add to `RegisterRoutes`:

```go
	mux.Handle("POST /v1/workflows/{id}/runs", auth(d.fireRun))
	mux.Handle("GET /v1/workflows/{id}/runs", auth(d.listRuns))
	mux.Handle("GET /v1/runs/{id}", auth(d.getRun))
	mux.Handle("GET /v1/runs/{id}/events", auth(d.listRunEvents))
```

`server/internal/httpapi/runs.go`:

```go
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/gambtho/nightwatch/server/internal/compute"
	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/token"
)

const runTokenTTL = time.Hour

type runJSON struct {
	ID         uuid.UUID  `json:"id"`
	WorkflowID uuid.UUID  `json:"workflow_id"`
	Version    int        `json:"version"`
	Status     string     `json:"status"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	TokensIn   *int       `json:"tokens_in,omitempty"`
	TokensOut  *int       `json:"tokens_out,omitempty"`
	CostCents  *int       `json:"cost_cents,omitempty"`
	ErrorKind  *string    `json:"error_kind,omitempty"`
	ErrorMsg   *string    `json:"error_msg,omitempty"`
	Output     *string    `json:"output,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

func toRunJSON(r store.Run) runJSON {
	return runJSON{
		ID: r.ID, WorkflowID: r.WorkflowID, Version: r.Version, Status: r.Status,
		StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
		TokensIn: r.TokensIn, TokensOut: r.TokensOut, CostCents: r.CostCents,
		ErrorKind: r.ErrorKind, ErrorMsg: r.ErrorMsg, Output: r.Output,
		CreatedAt: r.CreatedAt,
	}
}

func (d Deps) fireRun(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	wfID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	version, err := d.Store.GetApprovedVersion(r.Context(), claims.TenantID, wfID)
	if errors.Is(err, store.ErrNotFound) {
		// Distinguish "no workflow" (404) from "no approved version" (409).
		if _, wfErr := d.Store.GetWorkflow(r.Context(), claims.TenantID, wfID); wfErr != nil {
			writeErr(w, wfErr)
			return
		}
		writeJSON(w, http.StatusConflict, map[string]string{"error": "workflow has no approved version"})
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}

	runID := uuid.New()
	bearer, hash, err := d.Signer.Sign(token.RunClaims{
		RunID: runID, TenantID: claims.TenantID,
		ExpiresAt: time.Now().Add(runTokenTTL),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	run, err := d.Store.CreateRun(r.Context(), claims.TenantID, wfID, runID, version.Number, hash)
	if err != nil {
		writeErr(w, err)
		return
	}

	actor, err := d.Compute.EnsureActor(r.Context(),
		compute.WorkflowRef{TenantID: claims.TenantID, WorkflowID: wfID},
		compute.TemplateRef{Name: "harness-v1"})
	if err != nil {
		d.failDispatch(r.Context(), claims.TenantID, runID, err)
		writeErr(w, err)
		return
	}
	if _, err := d.Compute.Invoke(r.Context(), actor,
		compute.InvokeRequest{RunID: runID, RunToken: bearer}); err != nil {
		d.failDispatch(r.Context(), claims.TenantID, runID, err)
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"run": toRunJSON(run)})
}

// failDispatch records a run that never reached its actor; without this a
// dispatch failure would leave the run pending forever.
func (d Deps) failDispatch(ctx context.Context, tenantID, runID uuid.UUID, cause error) {
	if _, err := d.Store.FinalizeRun(ctx, tenantID, runID, store.RunFinal{
		Status: "failed", ErrorKind: "dispatch_failed", ErrorMsg: cause.Error(),
	}); err != nil {
		slog.Error("httpapi: record dispatch failure", "run", runID, "err", err)
	}
}

func (d Deps) getRun(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	run, err := d.Store.GetRun(r.Context(), claims.TenantID, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": toRunJSON(run)})
}

func (d Deps) listRuns(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	wfID, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if _, err := d.Store.GetWorkflow(r.Context(), claims.TenantID, wfID); err != nil {
		writeErr(w, err)
		return
	}
	runs, err := d.Store.ListRuns(r.Context(), claims.TenantID, wfID)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]runJSON, 0, len(runs))
	for _, run := range runs {
		out = append(out, toRunJSON(run))
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": out})
}

func (d Deps) listRunEvents(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if _, err := d.Store.GetRun(r.Context(), claims.TenantID, id); err != nil {
		writeErr(w, err)
		return
	}
	events, err := d.Store.ListRunEvents(r.Context(), claims.TenantID, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	type eventJSON struct {
		Type      string          `json:"type"`
		Payload   json.RawMessage `json:"payload"`
		CreatedAt time.Time       `json:"created_at"`
	}
	out := make([]eventJSON, 0, len(events))
	for _, e := range events {
		out = append(out, eventJSON{Type: e.Type, Payload: e.Payload, CreatedAt: e.CreatedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}
```

- [ ] **Step 4: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS (including the updated Task 4 tests).

- [ ] **Step 5: Commit**

```bash
git add server
git commit -m "feat(server): fire and read runs through the Compute seam"
```

---

### Task 12: Wiring, end-to-end test, API contract doc

**Files:**

- Create: `server/cmd/nightshift/main.go`, `server/e2e_test.go`, `docs/api/v1.md`, `server/README.md`

**Interfaces:**

- Consumes: everything.
- Produces: the `nightshift` binary (`serve`, `migrate`, `dev-session` subcommands) and the wired runner closure connecting `compute.Local` → `harness.Client` → `harness.Run`.

- [ ] **Step 1: Write the failing end-to-end test**

`server/e2e_test.go` (package `main_test` is awkward at the root; use package `server_test` in the module root):

```go
// End-to-end: session → create workflow → approve → fire → the harness
// runs against a scripted provider and pushes its record back over the
// internal API → the public API shows the finished run.
package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/compute"
	"github.com/gambtho/nightwatch/server/internal/harness"
	"github.com/gambtho/nightwatch/server/internal/httpapi"
	"github.com/gambtho/nightwatch/server/internal/internalapi"
	"github.com/gambtho/nightwatch/server/internal/llm"
	"github.com/gambtho/nightwatch/server/internal/llm/llmtest"
	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
	"github.com/gambtho/nightwatch/server/internal/token"
)

func TestEndToEndRun(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()

	sessionKey := bytes.Repeat([]byte{1}, 32)
	signer := token.New(bytes.Repeat([]byte{2}, 32))
	provider := &llmtest.Scripted{
		Response: "This week: 40% of tickets were about the new billing page.",
		Usage:    llm.Usage{InputTokens: 100, OutputTokens: 50},
	}
	factory := func(string) (llm.Provider, error) { return provider, nil }

	// The runner closure needs the server's URL; the server needs Compute.
	// Resolve the cycle the same way serve() does: a variable captured by
	// the closure, assigned once the listener exists.
	var baseURL string
	local := compute.NewLocal(t.TempDir(), func(ctx context.Context, req compute.InvokeRequest, stateDir string) {
		client := harness.NewClient(baseURL, req.RunID, req.RunToken)
		steps, err := client.Context(ctx)
		if err != nil {
			t.Errorf("harness context: %v", err)
			return
		}
		_, _ = harness.Run(ctx, harness.Input{Steps: steps}, harness.Deps{
			ProviderFactory: factory,
			Sink:            client,
		})
	})

	mux := http.NewServeMux()
	httpapi.RegisterRoutes(mux, httpapi.Deps{Store: s, SessionKey: sessionKey, Signer: signer, Compute: local})
	internalapi.RegisterRoutes(mux, internalapi.Deps{Store: s, Signer: signer})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	baseURL = ts.URL

	tn, err := s.CreateTenant(ctx, "acme")
	require.NoError(t, err)
	user, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)
	cookie, err := httpapi.SessionCookie(sessionKey,
		httpapi.SessionClaims{UserID: user.ID, TenantID: tn.ID, Role: "owner"}, time.Hour)
	require.NoError(t, err)

	do := func(method, path string, body any) map[string]any {
		t.Helper()
		var buf bytes.Buffer
		if body != nil {
			require.NoError(t, json.NewEncoder(&buf).Encode(body))
		}
		req, err := http.NewRequest(method, ts.URL+path, &buf)
		require.NoError(t, err)
		req.AddCookie(cookie)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Less(t, resp.StatusCode, 300, "%s %s", method, path)
		var out map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
		return out
	}

	out := do("POST", "/v1/workflows", map[string]any{
		"name": "weekly digest",
		"steps": map[string]any{
			"system_prompt": "You prepare the weekly support digest.",
			"kickoff":       "Summarize last week's tickets.",
			"provider":      "anthropic",
			"model":         "claude-sonnet-5",
			"max_tokens":    2048,
		},
	})
	wfID := out["workflow"].(map[string]any)["id"].(string)
	do("POST", "/v1/workflows/"+wfID+"/versions/1/approve", nil)

	out = do("POST", "/v1/workflows/"+wfID+"/runs", nil)
	runID := out["run"].(map[string]any)["id"].(string)
	require.NoError(t, uuid.Validate(runID))

	local.Wait()

	out = do("GET", "/v1/runs/"+runID, nil)
	run := out["run"].(map[string]any)
	require.Equal(t, "succeeded", run["status"])
	require.Contains(t, run["output"], "billing page")
	require.Equal(t, float64(100), run["tokens_in"])

	out = do("GET", "/v1/runs/"+runID+"/events", nil)
	events := out["events"].([]any)
	require.GreaterOrEqual(t, len(events), 2) // run.start, run.finish
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd server && go test .`
Expected: FAIL only if wiring gaps exist; if it passes immediately, continue — the components were built to meet it. Either way it must pass before Step 4.

- [ ] **Step 3: Write the binary**

`server/cmd/nightshift/main.go`:

```go
// Command nightshift runs the Nightshift control plane.
//
//	nightshift migrate      apply database migrations and exit
//	nightshift serve        migrate, then serve the public and internal APIs
//	nightshift dev-session  mint a tenant, owner, and session cookie for local use
//
// Configuration (env):
//
//	DATABASE_URL            Postgres DSN (required)
//	NIGHTSHIFT_SESSION_KEY  base64, 32 bytes (required for serve/dev-session)
//	NIGHTSHIFT_RUNNER_KEY   base64, 32 bytes (required for serve)
//	NIGHTSHIFT_LISTEN_ADDR  default 127.0.0.1:8080
//	NIGHTSHIFT_STATE_DIR    actor state root, default $TMPDIR/nightshift-actors
//	ANTHROPIC_API_KEY, OPENAI_API_KEY, OPENROUTER_API_KEY
//	                        platform model credentials, per provider
package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gambtho/nightwatch/server/internal/compute"
	"github.com/gambtho/nightwatch/server/internal/db"
	"github.com/gambtho/nightwatch/server/internal/harness"
	"github.com/gambtho/nightwatch/server/internal/httpapi"
	"github.com/gambtho/nightwatch/server/internal/internalapi"
	"github.com/gambtho/nightwatch/server/internal/llm"
	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/token"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: nightshift <serve|migrate|dev-session>")
		os.Exit(2)
	}
	ctx := context.Background()
	var err error
	switch os.Args[1] {
	case "migrate":
		err = db.Migrate(ctx, mustEnv("DATABASE_URL"))
	case "serve":
		err = serve(ctx)
	case "dev-session":
		err = devSession(ctx, os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		slog.Error("nightshift", "err", err)
		os.Exit(1)
	}
}

func mustEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		slog.Error("missing required env var", "name", name)
		os.Exit(2)
	}
	return v
}

func keyFromEnv(name string) []byte {
	key, err := base64.StdEncoding.DecodeString(mustEnv(name))
	if err != nil || len(key) != 32 {
		slog.Error("env var must be base64-encoded 32 bytes", "name", name)
		os.Exit(2)
	}
	return key
}

func apiKeyFor(provider string) string {
	switch provider {
	case "anthropic":
		return os.Getenv("ANTHROPIC_API_KEY")
	case "openai":
		return os.Getenv("OPENAI_API_KEY")
	case "openrouter":
		return os.Getenv("OPENROUTER_API_KEY")
	}
	return ""
}

func serve(ctx context.Context) error {
	if err := db.Migrate(ctx, mustEnv("DATABASE_URL")); err != nil {
		return err
	}
	pool, err := db.NewPool(ctx, mustEnv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer pool.Close()

	s := store.New(pool)
	sessionKey := keyFromEnv("NIGHTSHIFT_SESSION_KEY")
	signer := token.New(keyFromEnv("NIGHTSHIFT_RUNNER_KEY"))
	factory := llm.NewFactory(llm.Config{})

	addr := os.Getenv("NIGHTSHIFT_LISTEN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	stateDir := os.Getenv("NIGHTSHIFT_STATE_DIR")
	if stateDir == "" {
		stateDir = filepath.Join(os.TempDir(), "nightshift-actors")
	}

	baseURL := "http://" + addr
	local := compute.NewLocal(stateDir, func(ctx context.Context, req compute.InvokeRequest, stateDir string) {
		client := harness.NewClient(baseURL, req.RunID, req.RunToken)
		steps, err := client.Context(ctx)
		if err != nil {
			slog.Error("harness: fetch context", "run", req.RunID, "err", err)
			return
		}
		if _, err := harness.Run(ctx,
			harness.Input{Steps: steps, APIKey: apiKeyFor(steps.Provider)},
			harness.Deps{ProviderFactory: factory, Sink: client}); err != nil {
			slog.Error("harness: run failed", "run", req.RunID, "err", err)
		}
	})

	mux := http.NewServeMux()
	httpapi.RegisterRoutes(mux, httpapi.Deps{Store: s, SessionKey: sessionKey, Signer: signer, Compute: local})
	internalapi.RegisterRoutes(mux, internalapi.Deps{Store: s, Signer: signer})

	slog.Info("nightshift: serving", "addr", addr)
	return http.ListenAndServe(addr, mux)
}

func devSession(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("dev-session", flag.ExitOnError)
	tenantName := fs.String("tenant", "dev", "name for a newly created tenant (use -tenant-id to reuse one instead)")
	tenantID := fs.String("tenant-id", "", "existing tenant id to reuse")
	email := fs.String("email", "dev@example.test", "user email")
	if err := fs.Parse(args); err != nil {
		return err
	}

	pool, err := db.NewPool(ctx, mustEnv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer pool.Close()
	s := store.New(pool)

	var tn store.Tenant
	if *tenantID != "" {
		id, err := uuidParse(*tenantID)
		if err != nil {
			return err
		}
		tn, err = s.GetTenant(ctx, id)
		if err != nil {
			return err
		}
	} else {
		tn, err = s.CreateTenant(ctx, *tenantName)
		if err != nil {
			return err
		}
	}
	user, err := s.UpsertUser(ctx, tn.ID, *email)
	if err != nil {
		return err
	}
	cookie, err := httpapi.SessionCookie(keyFromEnv("NIGHTSHIFT_SESSION_KEY"),
		httpapi.SessionClaims{UserID: user.ID, TenantID: tn.ID, Role: user.Role},
		24*time.Hour)
	if err != nil {
		return err
	}
	fmt.Printf("tenant: %s\nuser:   %s\ncookie: %s=%s\n", tn.ID, user.ID, cookie.Name, cookie.Value)
	return nil
}
```

Add a tiny helper at the bottom of `main.go` (keeps the uuid import localized):

```go
func uuidParse(s string) (id uuid.UUID, err error) { return uuid.Parse(s) }
```

with `"github.com/google/uuid"` added to the imports.

- [ ] **Step 4: Run the e2e test, the build, and full verification**

Run: `cd server && go build ./... && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS, everything builds, `gofmt` prints nothing.

- [ ] **Step 5: Write the contract doc and README**

`docs/api/v1.md` — document what exists, exactly as built. Structure:

```markdown
# Nightshift API v1

The UI↔server contract. The OSS boundary is undecided, so this API is
designed as a public contract: versioned under `/v1`, no leaked internals.

> **Stability: unstable (alpha).** The `steps` document currently exposes
> the compiled execution form (system prompt, provider, model). Before this
> contract freezes, the user-facing steps artifact — the `{id, text}` list
> the UX prototype defines in `src/lib/types.ts` — joins the version
> document, and the execution form becomes server-derived rather than
> client-supplied. Nothing under `/v1` is frozen until this notice is
> removed.

## Authentication

Browser endpoints use a signed session cookie (`ns_session`) carrying the
user, tenant, and role; every request is scoped to the session's tenant.
There is no cross-tenant access. (Production login is not built yet; see
`nightshift dev-session` in `server/README.md`.)

`/internal/*` endpoints are for the run harness only: bearer JWT scoped to
a single run.

## Resources

A **workflow** owns immutable numbered **versions**; each version is the
three artifacts (steps, permit, rubric). At most one version is approved;
only drafts can be approved; a new approval supersedes the old version.
Firing a workflow creates a **run** of its approved version. **Run
events** are the run's audit trail, pushed by the harness.

## Endpoints

[table of the nine /v1 endpoints: method, path, request, response, errors —
transcribe the wire shapes from Task 4 and Task 11 of the foundation plan,
corrected against the code as built]

## Statuses

[version: draft/approved/superseded; run: pending/running/succeeded/failed]
```

`server/README.md` — brief: what this module is, pointer to the two specs and this contract, how to run (`docker` + `DATABASE_URL`, the three key env vars, `go test ./...`), and one paragraph on the layout (the file-structure table from this plan's header, updated if reality diverged).

- [ ] **Step 6: Format docs and commit**

```bash
cd /home/tng/workspace/nightshift-worktrees/platform-plan
npx prettier --write docs/api/v1.md server/README.md
git add server docs/api
git commit -m "feat(server): nightshift binary, end-to-end run test, v1 API contract doc"
```

---

## Final verification (after all tasks)

1. `cd server && gofmt -l . && go vet ./... && go test ./...` — all green.
2. `go build ./...` — the binary builds.
3. Re-read the spec's "Governance primitives we must build" list and confirm this plan's boundary: primitives 6 (run records) and 8 (versioning + re-approval) are delivered; 1–5 and 7 are Plans 2–4 per the roadmap; the Compute seam for Plan 5 exists with a local implementation.
4. Confirm nothing in `server/` imports the prototype (`src/`) or cronfoundry.
5. `npm test` from the repo root — the prototype's 46 tests still pass (nothing here should touch them).

## Deviations log

Executors: record deviations from this plan in `implementation-notes.md` at the worktree root as you go; it is folded into the PR description at the end.
