# Nightshift Egress Proxy + Credential Vault Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The permit-enforcing egress proxy for LLM provider traffic plus the per-tenant-DEK credential vault, so no credential ever enters the harness and every model call is permit-checked.

**Architecture:** A new `internal/proxy` package (reverse-proxy gateway behind five narrow interfaces) mounted in the `nightshift` binary; `internal/permit` (schema v1), `internal/vault` (pure envelope crypto), `internal/proxyadapter` (interface implementations over store/token/vault). The run token rides in the provider-native auth-header slot; the proxy verifies it (signature + stored hash + active run), strips it, and injects the real key from the vault. Finalization clears the stored token hash, making it revocation.

**Tech Stack:** Go 1.26, stdlib `net/http` + `httputil.ReverseProxy`, `crypto/aes`+`cipher.GCM`, Postgres (goose migrations 00005/00006), existing `internal/{store,token,llm,harness}`.

**Spec:** `docs/superpowers/specs/2026-08-30-nightshift-egress-proxy-design.md` (parent: `docs/superpowers/specs/2026-08-30-nightshift-platform-design.md`)

## Global Constraints

- Module `github.com/gambtho/nightwatch/server`; work happens in this worktree (branch `proxy-spec`); never modify `/home/tng/workspace/cronfoundry` or `src/`.
- **Fail closed**: absent/unparseable permit → 403; provider not allowlisted → 403; vault decrypt failure → 500; bad/expired/revoked/finalized run token → 401. Denials are still enforced when audit-append fails.
- `internal/proxy` imports no internal package except `internal/permit` (and `uuid`/stdlib). It must not import `httpapi`, `internalapi`, `store`, `token`, or `vault` — the adapters do.
- The run token travels in the provider-native auth-header slot: `x-api-key` for `anthropic`, `Authorization: Bearer` for `openai`/`openrouter`. `CallOptions.APIKey` carries it; the proxy strips and replaces it. **Provider SDK code (`internal/llm/anthropic.go`, `openai.go`) is untouched.**
- Vault key hierarchy: `NIGHTSHIFT_VAULT_KEY` (env, base64 32 bytes — the third key domain) → per-tenant KEK (wrapped, `tenant_kek.version`) → per-secret DEK (AES-256-GCM). Decryption only inside `vault`/`proxyadapter` on the proxy request path; no handler-visible struct carries plaintext secrets.
- Permit v1 exactly per the spec: `{"v":1,"llm":{"providers":[...],"connection":"default"},"connections":{}}`; `connections` must be empty; empty `providers` = no LLM egress; `connection` defaults to `"default"`, resolved **per provider** (tenant BYO default, else platform key). Malformed permits are 400 at the workflow API.
- Store rules as established: every method takes `tenantID uuid.UUID`, every SQL statement filters on it, cross-tenant access is a test case; composite-FK/cascade conventions.
- Verification from `server/`: `gofmt -l .` (prints nothing), `go vet ./...`, `go build ./...`, `go test ./...` (Docker available for testcontainers). Conventional commits. Docs get `npx prettier --write` from the repo root before committing (run it yourself; no reliable hook).

---

## File structure

```
server/internal/permit/            permit.go, permit_test.go        (schema v1, no internal deps)
server/internal/vault/             vault.go, vault_test.go          (pure envelope crypto, no DB)
server/internal/store/             tenant.go (modified), connection.go (new), run.go (modified)
server/internal/db/migrations/     00005_run_token_revocation.sql, 00006_vault.sql
server/internal/httpapi/           workflows.go (permit validation), connections.go (new)
server/internal/proxy/             proxy.go (types+interfaces+config), handler.go, handler_test.go
server/internal/proxyadapter/      adapter.go, adapter_test.go      (impls over store/token/vault)
server/internal/harness/           harness.go (APIKey -> RunToken)
server/cmd/nightshift/main.go      wiring; docs/api/v1.md, server/README.md updated
```

---

### Task 1: Permit schema v1 and API validation

**Files:**

- Create: `server/internal/permit/permit.go`
- Modify: `server/internal/httpapi/workflows.go` (decodeDoc), test fixtures listed in Step 4
- Test: `server/internal/permit/permit_test.go`, `server/internal/httpapi/workflows_test.go` (one added test)

**Interfaces:**

- Consumes: nothing internal.
- Produces:

```go
package permit
type Permit struct {
	V           int                        `json:"v"`
	LLM         LLM                        `json:"llm"`
	Connections map[string]json.RawMessage `json:"connections,omitempty"`
}
type LLM struct {
	Providers  []string `json:"providers,omitempty"`
	Connection string   `json:"connection,omitempty"`
}
func Parse(raw []byte) (Permit, error) // strict v1; Connection defaulted to "default"
func (p Permit) AllowsProvider(name string) bool
var Empty = json.RawMessage(`{"v":1}`) // canonical deny-all permit
```

- [ ] **Step 1: Write the failing tests**

`server/internal/permit/permit_test.go`:

```go
package permit_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/permit"
)

func TestParseValid(t *testing.T) {
	p, err := permit.Parse([]byte(`{"v":1,"llm":{"providers":["anthropic","openai"],"connection":"work"},"connections":{}}`))
	require.NoError(t, err)
	require.True(t, p.AllowsProvider("anthropic"))
	require.False(t, p.AllowsProvider("openrouter"))
	require.Equal(t, "work", p.LLM.Connection)
}

func TestParseDefaultsAndDenyAll(t *testing.T) {
	// The canonical empty permit: valid, denies all egress, connection default.
	p, err := permit.Parse(permit.Empty)
	require.NoError(t, err)
	require.False(t, p.AllowsProvider("anthropic"))
	require.Equal(t, "default", p.LLM.Connection)
}

func TestParseRejects(t *testing.T) {
	for name, raw := range map[string]string{
		"missing v":            `{}`,
		"wrong v":              `{"v":2}`,
		"nonempty connections": `{"v":1,"connections":{"zendesk":{}}}`,
		"unknown field":        `{"v":1,"blast_radius":true}`,
		"not json":             `nope`,
		"empty provider name":  `{"v":1,"llm":{"providers":[""]}}`,
	} {
		_, err := permit.Parse([]byte(raw))
		require.Error(t, err, name)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd server && go test ./internal/permit/`
Expected: FAIL (package does not exist).

- [ ] **Step 3: Implement**

`server/internal/permit/permit.go`:

```go
// Package permit defines the workflow permit document, schema v1. The
// permit is the approved blast radius: what a workflow's runs may reach.
// v1 governs LLM provider egress only; the connections map is reserved
// for the connector catalog and must be empty.
package permit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
)

type Permit struct {
	V           int                        `json:"v"`
	LLM         LLM                        `json:"llm"`
	Connections map[string]json.RawMessage `json:"connections,omitempty"`
}

type LLM struct {
	Providers  []string `json:"providers,omitempty"`
	Connection string   `json:"connection,omitempty"`
}

// Empty is the canonical deny-all permit: valid v1, no egress allowed.
var Empty = json.RawMessage(`{"v":1}`)

// Parse validates raw as a v1 permit. Fail closed: anything unrecognized
// is an error, not a warning.
func Parse(raw []byte) (Permit, error) {
	var p Permit
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Permit{}, fmt.Errorf("permit: %w", err)
	}
	if p.V != 1 {
		return Permit{}, fmt.Errorf("permit: unsupported version %d", p.V)
	}
	if len(p.Connections) != 0 {
		return Permit{}, fmt.Errorf("permit: connections must be empty in v1")
	}
	for _, name := range p.LLM.Providers {
		if name == "" {
			return Permit{}, fmt.Errorf("permit: empty provider name")
		}
	}
	if p.LLM.Connection == "" {
		p.LLM.Connection = "default"
	}
	return p, nil
}

func (p Permit) AllowsProvider(name string) bool {
	return slices.Contains(p.LLM.Providers, name)
}
```

- [ ] **Step 4: Enforce at the workflow API and update fixtures**

In `server/internal/httpapi/workflows.go`, `decodeDoc`: replace the permit nil-default and add validation (rubric default stays as-is):

```go
	if body.Permit == nil {
		body.Permit = json.RawMessage(permit.Empty)
	}
	if _, err := permit.Parse(body.Permit); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid permit: " + err.Error()})
		return body, false
	}
```

(add import `"github.com/gambtho/nightwatch/server/internal/permit"`; note the existing default was `json.RawMessage("{}")` — `{}` is no longer a valid permit).

Update every fixture that sends a pre-v1 permit, replacing the permit value with `{"v":1,"llm":{"providers":["anthropic"]},"connections":{}}` (as a JSON string in Go fixtures, as a map in `map[string]any` fixtures):

- `server/internal/store/workflow_test.go` — `testDoc()`'s `Permit: json.RawMessage(...)`
- `server/internal/httpapi/workflows_test.go` — `workflowBody()`'s `"permit"` entry
- `server/internal/internalapi/internalapi_test.go` — `setup()`'s `Permit: []byte(...)`
- `server/e2e_test.go` — the `POST /v1/workflows` body gains a `"permit"` entry with that value (it currently omits permit and relied on the `{}` default)

Add one API test at the end of `server/internal/httpapi/workflows_test.go`:

```go
func TestCreateWorkflowRejectsInvalidPermit(t *testing.T) {
	e := newEnv(t)
	body := workflowBody()
	body["permit"] = map[string]any{"v": 2}
	resp, _ := e.do(t, "POST", "/v1/workflows", body)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}
```

- [ ] **Step 5: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS (store tests, httpapi tests, e2e all green with the new fixtures).

- [ ] **Step 6: Commit**

```bash
git add server
git commit -m "feat(server): permit schema v1 with fail-closed validation at the workflow API"
```

---

### Task 2: Run-token revocation on finalize

**Files:**

- Create: `server/internal/db/migrations/00005_run_token_revocation.sql`
- Modify: `server/internal/store/run.go` (`runCols`, `FinalizeRun`)
- Test: `server/internal/store/run_test.go` (extend `TestRunLifecycle`)

**Interfaces:**

- Consumes: Task-0 state (merged foundation).
- Produces: `FinalizeRun` clears `runner_token_hash` atomically with the terminal status; `Run.TokenHash` is `""` for a finalized run (scanned via `COALESCE`). Later tasks rely on `token.EqualHash(h, "")` being false for any real hash.

- [ ] **Step 1: Write the migration**

`server/internal/db/migrations/00005_run_token_revocation.sql`:

```sql
-- +goose Up
-- Finalization clears the run's token hash (atomic revocation), so the
-- column must accept NULL.
ALTER TABLE run ALTER COLUMN runner_token_hash DROP NOT NULL;

-- +goose Down
ALTER TABLE run ALTER COLUMN runner_token_hash SET NOT NULL;
```

- [ ] **Step 2: Extend the failing test**

In `server/internal/store/run_test.go`, `TestRunLifecycle`, immediately after the existing `FinalizeRun` assertions (`require.Equal(t, 100, *final.TokensIn)`), add:

```go
	// Finalization revokes the run token: the stored hash is cleared in the
	// same UPDATE that sets the terminal status.
	require.Empty(t, final.TokenHash)
	got, err := s.GetRun(ctx, tn.ID, runID)
	require.NoError(t, err)
	require.Empty(t, got.TokenHash)
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd server && go test ./internal/store/ -run TestRunLifecycle -v`
Expected: FAIL — `final.TokenHash` is still `"hash123"`.

- [ ] **Step 4: Implement**

In `server/internal/store/run.go`:

1. `runCols` — the hash column becomes NULL-able, so scan it through `COALESCE` (valid in both `SELECT` and `RETURNING` positions):

```go
const runCols = `id, tenant_id, workflow_id, version, status, started_at,
	finished_at, tokens_in, tokens_out, cost_cents, error_kind, error_msg,
	output, COALESCE(runner_token_hash, ''), created_at`
```

2. `FinalizeRun` — clear the hash in the same UPDATE:

```go
		`UPDATE run SET status = $3, finished_at = now(),
		        tokens_in = $4, tokens_out = $5, cost_cents = $6,
		        error_kind = NULLIF($7, ''), error_msg = NULLIF($8, ''),
		        output = $9,
		        runner_token_hash = NULL
		 WHERE id = $1 AND tenant_id = $2
		   AND status IN ('pending', 'running')
		 RETURNING ` + runCols,
```

(Only those two spots change; `scanRun` is untouched — `COALESCE` yields a non-NULL text.)

- [ ] **Step 5: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS (the internal API's hash check still passes for active runs; `TestInternalAPIRejectsMismatchedTokenHash` unaffected).

- [ ] **Step 6: Commit**

```bash
git add server
git commit -m "feat(server): clear runner_token_hash on finalize — finalization is revocation"
```

---

### Task 3: Vault crypto and tenant KEK

**Files:**

- Create: `server/internal/vault/vault.go`, `server/internal/db/migrations/00006_vault.sql`
- Modify: `server/internal/store/tenant.go` (`CreateTenant` gains a transaction + KEK arg), all `CreateTenant` call sites (Step 4 lists every one)
- Test: `server/internal/vault/vault_test.go`, `server/internal/store/tenant_test.go` (extend)

**Interfaces:**

- Consumes: nothing internal (vault is pure crypto).
- Produces:

```go
package vault
const KeyLen = 32
func NewMaster(key []byte) (*Master, error)            // errors unless len(key)==KeyLen
func (m *Master) NewTenantKEK() ([]byte, error)        // random KEK, AES-256-GCM-wrapped under master, nonce-prefixed
func (m *Master) EncryptSecret(wrappedKEK []byte, value string) (dekWrapped, ciphertext, nonce []byte, err error)
func (m *Master) DecryptSecret(wrappedKEK, dekWrapped, ciphertext, nonce []byte) (string, error)
```

and store: `CreateTenant(ctx, name string, wrappedKEK []byte) (Tenant, error)` (transactional: tenant + tenant_kek rows), `TenantKEK(ctx, tenantID uuid.UUID) (wrapped []byte, version int, err error)` (the CURRENT — highest-version — KEK, used on the encrypt path), and `TenantKEKAt(ctx, tenantID uuid.UUID, version int) (wrapped []byte, err error)` (a specific historical version, used on the decrypt path via `connection.kek_version`).

- [ ] **Step 1: Write the migration**

`server/internal/db/migrations/00006_vault.sql`:

```sql
-- +goose Up
CREATE TABLE tenant_kek (
    tenant_id uuid NOT NULL REFERENCES tenant (id) ON DELETE CASCADE,
    -- History table: rotation ADDS a row with version+1; old versions stay
    -- decryptable while connections still name them via kek_version.
    version int NOT NULL DEFAULT 1,
    wrapped_kek bytea NOT NULL,
    master_version int NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, version)
);

CREATE TABLE connection (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenant (id) ON DELETE CASCADE,
    name text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('llm_api_key')),
    provider text NOT NULL,
    dek_wrapped bytea NOT NULL,
    ciphertext bytea NOT NULL,
    nonce bytea NOT NULL,
    kek_version int NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    UNIQUE (tenant_id, provider, name)
);

-- +goose Down
DROP TABLE connection;
DROP TABLE tenant_kek;
```

- [ ] **Step 2: Write the failing vault tests**

`server/internal/vault/vault_test.go`:

```go
package vault_test

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/vault"
)

func testMaster(t *testing.T) *vault.Master {
	t.Helper()
	key := make([]byte, vault.KeyLen)
	_, err := rand.Read(key)
	require.NoError(t, err)
	m, err := vault.NewMaster(key)
	require.NoError(t, err)
	return m
}

func TestSecretRoundTrip(t *testing.T) {
	m := testMaster(t)
	kek, err := m.NewTenantKEK()
	require.NoError(t, err)

	dek, ct, nonce, err := m.EncryptSecret(kek, "sk-ant-secret")
	require.NoError(t, err)
	got, err := m.DecryptSecret(kek, dek, ct, nonce)
	require.NoError(t, err)
	require.Equal(t, "sk-ant-secret", got)
}

func TestTenantIsolationAndWrongMaster(t *testing.T) {
	m := testMaster(t)
	kekA, err := m.NewTenantKEK()
	require.NoError(t, err)
	kekB, err := m.NewTenantKEK()
	require.NoError(t, err)
	require.False(t, bytes.Equal(kekA, kekB))

	dek, ct, nonce, err := m.EncryptSecret(kekA, "tenant-a-secret")
	require.NoError(t, err)

	// Another tenant's KEK cannot open it.
	_, err = m.DecryptSecret(kekB, dek, ct, nonce)
	require.Error(t, err)

	// A different master cannot even unwrap the KEK.
	other := testMaster(t)
	_, err = other.DecryptSecret(kekA, dek, ct, nonce)
	require.Error(t, err)
}

func TestNewMasterRejectsBadKey(t *testing.T) {
	_, err := vault.NewMaster([]byte("short"))
	require.Error(t, err)
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd server && go test ./internal/vault/`
Expected: FAIL (package does not exist).

- [ ] **Step 4: Implement vault and the store change**

`server/internal/vault/vault.go`:

```go
// Package vault is the envelope-encryption core: master key -> per-tenant
// KEK -> per-secret DEK, all AES-256-GCM. It is pure crypto — no database,
// no HTTP. Persistence lives in store; composition for the proxy lives in
// proxyadapter. Decrypted values exist only on the proxy's request path.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

const KeyLen = 32

type Master struct {
	key []byte
}

func NewMaster(key []byte) (*Master, error) {
	if len(key) != KeyLen {
		return nil, fmt.Errorf("vault: master key must be %d bytes, got %d", KeyLen, len(key))
	}
	return &Master{key: append([]byte(nil), key...)}, nil
}

// seal encrypts plaintext under key; the random nonce is prefixed to the
// returned blob so a single []byte column can hold it.
func seal(key, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return append(nonce, gcm.Seal(nil, nonce, plaintext, nil)...), nil
}

func open(key, blob []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(blob) < gcm.NonceSize() {
		return nil, errors.New("vault: blob too short")
	}
	return gcm.Open(nil, blob[:gcm.NonceSize()], blob[gcm.NonceSize():], nil)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// NewTenantKEK mints a fresh tenant key-encryption key, returned wrapped
// under the master. The plaintext KEK never leaves this package.
func (m *Master) NewTenantKEK() ([]byte, error) {
	kek := make([]byte, KeyLen)
	if _, err := io.ReadFull(rand.Reader, kek); err != nil {
		return nil, err
	}
	return seal(m.key, kek)
}

// EncryptSecret generates a fresh DEK, wraps it under the tenant KEK, and
// encrypts value under the DEK with an explicit nonce (stored separately,
// matching the connection table's columns).
func (m *Master) EncryptSecret(wrappedKEK []byte, value string) (dekWrapped, ciphertext, nonce []byte, err error) {
	kek, err := open(m.key, wrappedKEK)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("vault: unwrap kek: %w", err)
	}
	dek := make([]byte, KeyLen)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return nil, nil, nil, err
	}
	if dekWrapped, err = seal(kek, dek); err != nil {
		return nil, nil, nil, err
	}
	gcm, err := newGCM(dek)
	if err != nil {
		return nil, nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, nil, err
	}
	return dekWrapped, gcm.Seal(nil, nonce, []byte(value), nil), nonce, nil
}

func (m *Master) DecryptSecret(wrappedKEK, dekWrapped, ciphertext, nonce []byte) (string, error) {
	kek, err := open(m.key, wrappedKEK)
	if err != nil {
		return "", fmt.Errorf("vault: unwrap kek: %w", err)
	}
	dek, err := open(kek, dekWrapped)
	if err != nil {
		return "", fmt.Errorf("vault: unwrap dek: %w", err)
	}
	gcm, err := newGCM(dek)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("vault: decrypt: %w", err)
	}
	return string(plain), nil
}
```

`server/internal/store/tenant.go` — replace `CreateTenant` and add `TenantKEK`:

```go
// CreateTenant inserts the tenant and its wrapped KEK in one transaction:
// a tenant without a KEK cannot hold secrets, so the two are born together.
func (s *Store) CreateTenant(ctx context.Context, name string, wrappedKEK []byte) (Tenant, error) {
	var t Tenant
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return t, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	err = tx.QueryRow(ctx,
		`INSERT INTO tenant (name) VALUES ($1) RETURNING id, name, created_at`,
		name,
	).Scan(&t.ID, &t.Name, &t.CreatedAt)
	if err != nil {
		return t, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO tenant_kek (tenant_id, wrapped_kek) VALUES ($1, $2)`,
		t.ID, wrappedKEK); err != nil {
		return t, err
	}
	return t, tx.Commit(ctx)
}

// TenantKEK returns the current (highest-version) KEK — the encrypt path.
func (s *Store) TenantKEK(ctx context.Context, tenantID uuid.UUID) ([]byte, int, error) {
	var wrapped []byte
	var version int
	err := s.pool.QueryRow(ctx,
		`SELECT wrapped_kek, version FROM tenant_kek
		 WHERE tenant_id = $1 ORDER BY version DESC LIMIT 1`,
		tenantID,
	).Scan(&wrapped, &version)
	return wrapped, version, notFound(err)
}

// TenantKEKAt returns a specific KEK version — the decrypt path, driven by
// connection.kek_version, which keeps rotation resumable.
func (s *Store) TenantKEKAt(ctx context.Context, tenantID uuid.UUID, version int) ([]byte, error) {
	var wrapped []byte
	err := s.pool.QueryRow(ctx,
		`SELECT wrapped_kek FROM tenant_kek WHERE tenant_id = $1 AND version = $2`,
		tenantID, version,
	).Scan(&wrapped)
	return wrapped, notFound(err)
}
```

**Update every `CreateTenant` call site** to pass a KEK. Test files (packages `store_test`, `httpapi_test`, `internalapi_test`, `server_test`) pass an opaque placeholder — add this helper once per package that needs it (name it `testKEK`):

```go
var testKEK = []byte("test-wrapped-kek") // opaque to the store; real KEKs arrive with vault tests
```

Sites (all become `CreateTenant(ctx, "<name>", testKEK)`):

- `server/internal/store/tenant_test.go` — 2 calls (also add: `TestTenantRoundTrip` asserts `TenantKEK` returns the same bytes and version 1)
- `server/internal/store/user_test.go` — 1 call
- `server/internal/store/workflow_test.go` — 2 calls
- `server/internal/store/run_test.go` — 2 calls (`setupApproved`, the `other` tenant)
- `server/internal/httpapi/workflows_test.go` — 1 call (`newEnv`)
- `server/internal/internalapi/internalapi_test.go` — 1 call (`setup`)
- `server/e2e_test.go` — 1 call
- `server/cmd/nightshift/main.go` (`devSession`) — pass a **real** KEK: `master, err := vault.NewMaster(keyFromEnv("NIGHTSHIFT_VAULT_KEY"))` then `wrapped, err := master.NewTenantKEK()` (add the `vault` import; dev-session now requires `NIGHTSHIFT_VAULT_KEY`, and the doc comment at the top of main.go gains that variable).

Extend `TestTenantRoundTrip` in `tenant_test.go`:

```go
	wrapped, version, err := s.TenantKEK(ctx, tn.ID)
	require.NoError(t, err)
	require.Equal(t, testKEK, wrapped)
	require.Equal(t, 1, version)
```

- [ ] **Step 5: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS across all packages with the updated call sites.

- [ ] **Step 6: Commit**

```bash
git add server
git commit -m "feat(server): vault envelope crypto and transactional tenant KEK minting"
```

---

### Task 4: Connection store

**Files:**

- Create: `server/internal/store/connection.go`
- Test: `server/internal/store/connection_test.go`

**Interfaces:**

- Consumes: migration 00006 (Task 3), `Store` scaffolding.
- Produces:

```go
type Connection struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	Name       string
	Kind       string
	Provider   string
	DEKWrapped []byte
	Ciphertext []byte
	Nonce      []byte
	KEKVersion int
	CreatedAt  time.Time
	UpdatedAt  time.Time
	LastUsedAt *time.Time
}
func (s *Store) UpsertConnection(ctx context.Context, tenantID uuid.UUID, name, kind, provider string, dekWrapped, ciphertext, nonce []byte, kekVersion int) (Connection, error)
func (s *Store) GetConnection(ctx context.Context, tenantID uuid.UUID, provider, name string) (Connection, error)
func (s *Store) ListConnections(ctx context.Context, tenantID uuid.UUID) ([]Connection, error)
func (s *Store) DeleteConnection(ctx context.Context, tenantID uuid.UUID, provider, name string) error
func (s *Store) TouchConnection(ctx context.Context, tenantID, id uuid.UUID) error
```

The struct carries ciphertext, never plaintext; only `proxyadapter` decrypts.

- [ ] **Step 1: Write the failing tests**

`server/internal/store/connection_test.go`:

```go
package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
)

func TestConnectionLifecycle(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "acme", testKEK)
	require.NoError(t, err)

	c, err := s.UpsertConnection(ctx, tn.ID, "default", "llm_api_key", "anthropic",
		[]byte("dek"), []byte("ct"), []byte("nonce"), 1)
	require.NoError(t, err)
	require.Equal(t, "anthropic", c.Provider)
	require.Nil(t, c.LastUsedAt)

	// Upsert replaces the secret material for the same (provider, name).
	c2, err := s.UpsertConnection(ctx, tn.ID, "default", "llm_api_key", "anthropic",
		[]byte("dek2"), []byte("ct2"), []byte("nonce2"), 1)
	require.NoError(t, err)
	require.Equal(t, c.ID, c2.ID)
	require.Equal(t, []byte("ct2"), c2.Ciphertext)

	// Same name under a different provider is a separate connection.
	_, err = s.UpsertConnection(ctx, tn.ID, "default", "llm_api_key", "openai",
		[]byte("dek"), []byte("ct"), []byte("nonce"), 1)
	require.NoError(t, err)

	got, err := s.GetConnection(ctx, tn.ID, "anthropic", "default")
	require.NoError(t, err)
	require.Equal(t, []byte("ct2"), got.Ciphertext)

	list, err := s.ListConnections(ctx, tn.ID)
	require.NoError(t, err)
	require.Len(t, list, 2)

	require.NoError(t, s.TouchConnection(ctx, tn.ID, got.ID))
	got, err = s.GetConnection(ctx, tn.ID, "anthropic", "default")
	require.NoError(t, err)
	require.NotNil(t, got.LastUsedAt)

	require.NoError(t, s.DeleteConnection(ctx, tn.ID, "anthropic", "default"))
	_, err = s.GetConnection(ctx, tn.ID, "anthropic", "default")
	require.ErrorIs(t, err, store.ErrNotFound)
	require.ErrorIs(t, s.DeleteConnection(ctx, tn.ID, "anthropic", "default"), store.ErrNotFound)
}

func TestConnectionCrossTenantIsolation(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tnA, err := s.CreateTenant(ctx, "a", testKEK)
	require.NoError(t, err)
	tnB, err := s.CreateTenant(ctx, "b", testKEK)
	require.NoError(t, err)

	_, err = s.UpsertConnection(ctx, tnA.ID, "default", "llm_api_key", "anthropic",
		[]byte("dek"), []byte("ct"), []byte("nonce"), 1)
	require.NoError(t, err)

	_, err = s.GetConnection(ctx, tnB.ID, "anthropic", "default")
	require.ErrorIs(t, err, store.ErrNotFound)
	require.ErrorIs(t, s.DeleteConnection(ctx, tnB.ID, "anthropic", "default"), store.ErrNotFound)
	list, err := s.ListConnections(ctx, tnB.ID)
	require.NoError(t, err)
	require.Empty(t, list)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd server && go test ./internal/store/ -run TestConnection`
Expected: FAIL (compile error — `UpsertConnection` undefined).

- [ ] **Step 3: Implement**

`server/internal/store/connection.go`:

```go
package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Connection is a stored credential. The value is present only as
// ciphertext; decryption belongs to the vault/proxyadapter layer, on the
// proxy's request path.
type Connection struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	Name       string
	Kind       string
	Provider   string
	DEKWrapped []byte
	Ciphertext []byte
	Nonce      []byte
	KEKVersion int
	CreatedAt  time.Time
	UpdatedAt  time.Time
	LastUsedAt *time.Time
}

const connectionCols = `id, tenant_id, name, kind, provider, dek_wrapped,
	ciphertext, nonce, kek_version, created_at, updated_at, last_used_at`

func scanConnection(row pgx.Row) (Connection, error) {
	var c Connection
	err := row.Scan(&c.ID, &c.TenantID, &c.Name, &c.Kind, &c.Provider,
		&c.DEKWrapped, &c.Ciphertext, &c.Nonce, &c.KEKVersion,
		&c.CreatedAt, &c.UpdatedAt, &c.LastUsedAt)
	return c, notFound(err)
}

func (s *Store) UpsertConnection(ctx context.Context, tenantID uuid.UUID, name, kind, provider string, dekWrapped, ciphertext, nonce []byte, kekVersion int) (Connection, error) {
	return scanConnection(s.pool.QueryRow(ctx,
		`INSERT INTO connection
		   (tenant_id, name, kind, provider, dek_wrapped, ciphertext, nonce, kek_version)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (tenant_id, provider, name) DO UPDATE SET
		   kind = EXCLUDED.kind,
		   dek_wrapped = EXCLUDED.dek_wrapped,
		   ciphertext = EXCLUDED.ciphertext,
		   nonce = EXCLUDED.nonce,
		   kek_version = EXCLUDED.kek_version,
		   updated_at = now()
		 RETURNING `+connectionCols,
		tenantID, name, kind, provider, dekWrapped, ciphertext, nonce, kekVersion))
}

func (s *Store) GetConnection(ctx context.Context, tenantID uuid.UUID, provider, name string) (Connection, error) {
	return scanConnection(s.pool.QueryRow(ctx,
		`SELECT `+connectionCols+` FROM connection
		 WHERE tenant_id = $1 AND provider = $2 AND name = $3`,
		tenantID, provider, name))
}

func (s *Store) ListConnections(ctx context.Context, tenantID uuid.UUID) ([]Connection, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+connectionCols+` FROM connection
		 WHERE tenant_id = $1 ORDER BY provider, name`,
		tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Connection
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) DeleteConnection(ctx context.Context, tenantID uuid.UUID, provider, name string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM connection WHERE tenant_id = $1 AND provider = $2 AND name = $3`,
		tenantID, provider, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) TouchConnection(ctx context.Context, tenantID, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE connection SET last_used_at = now() WHERE id = $1 AND tenant_id = $2`,
		id, tenantID)
	return err
}
```

- [ ] **Step 4: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server
git commit -m "feat(server): connection store with per-provider uniqueness"
```

---

### Task 5: Connections HTTP API

**Files:**

- Create: `server/internal/httpapi/connections.go`
- Modify: `server/internal/httpapi/httpapi.go` (`Deps` gains `Vault *vault.Master`; three routes)
- Test: `server/internal/httpapi/connections_test.go`

**Interfaces:**

- Consumes: Tasks 3-4 (`vault.Master.EncryptSecret`, `store.TenantKEK`, connection store methods), session auth.
- Produces: `PUT /v1/connections/{name}` (body `{"provider":"anthropic","value":"sk-..."}`, 200, response NEVER echoes value), `GET /v1/connections` (`{"connections":[{name,kind,provider,created_at,updated_at,last_used_at}]}`), `DELETE /v1/connections/{name}?provider=...` (204). `httpapi.Deps` is now `{Store, SessionKey, Signer, Compute, Vault *vault.Master}`.

- [ ] **Step 1: Write the failing tests**

`server/internal/httpapi/connections_test.go`:

```go
package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConnectionEndpoints(t *testing.T) {
	e := newEnv(t)

	resp, out := e.do(t, "PUT", "/v1/connections/default",
		map[string]any{"provider": "anthropic", "value": "sk-ant-test-123"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	conn := out["connection"].(map[string]any)
	require.Equal(t, "anthropic", conn["provider"])
	// The secret value never appears in any response.
	for k, v := range conn {
		s, ok := v.(string)
		require.False(t, ok && strings.Contains(s, "sk-ant"), "field %s leaked the secret", k)
	}

	resp, out = e.do(t, "GET", "/v1/connections", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, out["connections"], 1)

	resp, _ = e.do(t, "DELETE", "/v1/connections/default?provider=anthropic", nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp, _ = e.do(t, "DELETE", "/v1/connections/default?provider=anthropic", nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestConnectionPutValidation(t *testing.T) {
	e := newEnv(t)
	resp, _ := e.do(t, "PUT", "/v1/connections/default", map[string]any{"provider": "", "value": "x"})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp, _ = e.do(t, "PUT", "/v1/connections/default", map[string]any{"provider": "anthropic", "value": ""})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}
```

`newEnv` in `workflows_test.go` gains a real vault: build `master` from 32 random bytes (`vault.NewMaster`), pass it in `Deps`, and — required for the encrypt path — `newEnv`'s tenant must now be created with a **real** KEK from that master instead of `testKEK`:

```go
	// in newEnv, before CreateTenant:
	vkey := make([]byte, vault.KeyLen)
	_, err = rand.Read(vkey)
	require.NoError(t, err)
	master, err := vault.NewMaster(vkey)
	require.NoError(t, err)
	wrapped, err := master.NewTenantKEK()
	require.NoError(t, err)
	tn, err := s.CreateTenant(ctx, "acme", wrapped)
	// ... Deps gains Vault: master; env keeps master available as e.vault if needed
```

(imports: `crypto/rand`, the `vault` package; keep `testKEK` only where no vault is in play).

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd server && go test ./internal/httpapi/ -run TestConnection`
Expected: FAIL (compile error — `Deps` has no `Vault`, routes missing).

- [ ] **Step 3: Implement**

In `server/internal/httpapi/httpapi.go`: add `Vault *vault.Master` to `Deps` (import `"github.com/gambtho/nightwatch/server/internal/vault"`), and register:

```go
	mux.Handle("PUT /v1/connections/{name}", auth(d.putConnection))
	mux.Handle("GET /v1/connections", auth(d.listConnections))
	mux.Handle("DELETE /v1/connections/{name}", auth(d.deleteConnection))
```

`server/internal/httpapi/connections.go`:

```go
package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gambtho/nightwatch/server/internal/store"
)

// connectionJSON deliberately has no field that could carry the secret.
type connectionJSON struct {
	Name       string     `json:"name"`
	Kind       string     `json:"kind"`
	Provider   string     `json:"provider"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

func toConnectionJSON(c store.Connection) connectionJSON {
	return connectionJSON{
		Name: c.Name, Kind: c.Kind, Provider: c.Provider,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, LastUsedAt: c.LastUsedAt,
	}
}

func (d Deps) putConnection(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	name := r.PathValue("name")
	r.Body = http.MaxBytesReader(w, r.Body, maxDocBytes)
	var body struct {
		Provider string `json:"provider"`
		Value    string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Provider == "" || body.Value == "" || name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider and value required"})
		return
	}
	wrappedKEK, kekVersion, err := d.Store.TenantKEK(r.Context(), claims.TenantID)
	if err != nil {
		writeErr(w, err)
		return
	}
	dek, ct, nonce, err := d.Vault.EncryptSecret(wrappedKEK, body.Value)
	if err != nil {
		writeErr(w, err)
		return
	}
	c, err := d.Store.UpsertConnection(r.Context(), claims.TenantID, name, "llm_api_key",
		body.Provider, dek, ct, nonce, kekVersion)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connection": toConnectionJSON(c)})
}

func (d Deps) listConnections(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	cs, err := d.Store.ListConnections(r.Context(), claims.TenantID)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]connectionJSON, 0, len(cs))
	for _, c := range cs {
		out = append(out, toConnectionJSON(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": out})
}

func (d Deps) deleteConnection(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	name := r.PathValue("name")
	provider := r.URL.Query().Get("provider")
	if provider == "" || name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider query parameter required"})
		return
	}
	if err := d.Store.DeleteConnection(r.Context(), claims.TenantID, provider, name); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS (e2e still compiles — its `Deps` literal doesn't name `Vault`, which stays nil there until Task 10).

- [ ] **Step 5: Commit**

```bash
git add server
git commit -m "feat(server): write-only connections API with vault encryption"
```

---

### Task 6: Proxy core — types, interfaces, auth, permit, deny path

**Files:**

- Create: `server/internal/proxy/proxy.go`, `server/internal/proxy/handler.go`
- Test: `server/internal/proxy/handler_test.go`

**Interfaces:**

- Consumes: `internal/permit` only (plus stdlib/uuid). **No other internal imports — this is a spec constraint the reviewer must check.**
- Produces:

```go
package proxy
type RunIdentity struct{ TenantID, RunID uuid.UUID }
type Secret struct{ Value string }
type HookRequest struct {
	Identity RunIdentity
	Provider string
}
type AuthSource interface {
	VerifyRunToken(ctx context.Context, bearer string) (RunIdentity, error)
}
type PermitSource interface {
	PermitForRun(ctx context.Context, tenantID, runID uuid.UUID) (permit.Permit, error)
}
type CredentialSource interface {
	Credential(ctx context.Context, tenantID uuid.UUID, name, provider string) (Secret, error)
}
type EventSink interface {
	AppendEvent(ctx context.Context, tenantID, runID uuid.UUID, typ string, payload map[string]any) error
}
type Hook interface {
	Before(ctx context.Context, req HookRequest) error
}
type NopHook struct{}
// HookError lets a Hook (Plan 3 metering) choose the response status.
// Only 403 and 429 are honored; any other error maps to 403.
type HookError struct {
	Status int
	Msg    string
}
func (e HookError) Error() string
// ProviderRoute is a provider's entire v1 blast radius: one upstream base
// and exactly one allowed (method, path). Any other request to the origin
// is denied before credential injection.
type ProviderRoute struct {
	Base   string // upstream base URL, including any prefix the SDK folds into its base
	Method string
	Path   string // the forwarded remainder the SDK emits, no leading slash
}
type Config struct {
	Providers    map[string]ProviderRoute // DefaultConfig fills the three real providers
	InternalBase string                   // base URL of the internal API for the pass-through route
}
func DefaultConfig() Config // returns a FRESH map each call
type Deps struct {
	Auth        AuthSource
	Permits     PermitSource
	Credentials CredentialSource
	Events      EventSink
	Hook        Hook
	Config      Config
}
func RegisterRoutes(mux *http.ServeMux, d Deps)
```

Routes: `/proxy/llm/{provider}/{path...}` and `/proxy/internal/{path...}` (Task 7 implements forwarding; this task registers and stubs it 502). Auth-slot extraction: `anthropic` → `x-api-key` header; `openai`/`openrouter` → `Authorization: Bearer <token>`. **No permit cache** — the permit is resolved per request (auth already reads the run row per request; the spec deliberately dropped caching). Authorization requires BOTH the permit's provider allowlist AND the provider's `ProviderRoute` (method, path) match — one operation per provider in v1.

- [ ] **Step 1: Write the failing tests**

`server/internal/proxy/handler_test.go` — fakes plus the deny/auth matrix (Task 7 extends this file for the forward path):

```go
package proxy_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/permit"
	"github.com/gambtho/nightwatch/server/internal/proxy"
)

type fakeAuth struct {
	identity proxy.RunIdentity
	err      error
	sawToken string
}

func (f *fakeAuth) VerifyRunToken(ctx context.Context, bearer string) (proxy.RunIdentity, error) {
	f.sawToken = bearer
	return f.identity, f.err
}

type fakePermits struct {
	permit permit.Permit
	err    error
	calls  int
}

func (f *fakePermits) PermitForRun(ctx context.Context, tenantID, runID uuid.UUID) (permit.Permit, error) {
	f.calls++
	return f.permit, f.err
}

type fakeCreds struct {
	secret proxy.Secret
	err    error
}

func (f *fakeCreds) Credential(ctx context.Context, tenantID uuid.UUID, name, provider string) (proxy.Secret, error) {
	return f.secret, f.err
}

type fakeEvents struct {
	mu     sync.Mutex
	events []string
}

func (f *fakeEvents) AppendEvent(ctx context.Context, tenantID, runID uuid.UUID, typ string, payload map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, typ)
	return nil
}

func mustPermit(t *testing.T, raw string) permit.Permit {
	t.Helper()
	p, err := permit.Parse([]byte(raw))
	require.NoError(t, err)
	return p
}

type env struct {
	ts      *httptest.Server
	auth    *fakeAuth
	permits *fakePermits
	creds   *fakeCreds
	events  *fakeEvents
}

func newEnv(t *testing.T, upstream string, p permit.Permit) *env {
	t.Helper()
	e := &env{
		auth:    &fakeAuth{identity: proxy.RunIdentity{TenantID: uuid.New(), RunID: uuid.New()}},
		permits: &fakePermits{permit: p},
		creds:   &fakeCreds{secret: proxy.Secret{Value: "real-key"}},
		events:  &fakeEvents{},
	}
	cfg := proxy.DefaultConfig()
	if upstream != "" {
		for name, route := range cfg.Providers {
			route.Base = upstream
			cfg.Providers[name] = route
		}
	}
	mux := http.NewServeMux()
	proxy.RegisterRoutes(mux, proxy.Deps{
		Auth: e.auth, Permits: e.permits, Credentials: e.creds,
		Events: e.events, Hook: proxy.NopHook{}, Config: cfg,
	})
	e.ts = httptest.NewServer(mux)
	t.Cleanup(e.ts.Close)
	return e
}

func doAnthropic(t *testing.T, e *env, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), "POST",
		e.ts.URL+"/proxy/llm/anthropic/v1/messages", nil)
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("x-api-key", token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestDeniedProviderIs403WithEvent(t *testing.T) {
	e := newEnv(t, "", mustPermit(t, `{"v":1,"llm":{"providers":["openai"]}}`))
	resp := doAnthropic(t, e, "run-token")
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	require.Contains(t, e.events.events, "proxy.denied")
	require.Equal(t, "run-token", e.auth.sawToken)
}

func TestMissingOrBadTokenIs401(t *testing.T) {
	e := newEnv(t, "", mustPermit(t, `{"v":1,"llm":{"providers":["anthropic"]}}`))
	resp := doAnthropic(t, e, "")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	e.auth.err = errors.New("bad token")
	resp = doAnthropic(t, e, "nope")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestOpenAITokenRidesAuthorizationHeader(t *testing.T) {
	e := newEnv(t, "", mustPermit(t, `{"v":1,"llm":{"providers":[]}}`))
	// SDK-faithful path: openai-go's base already contains /v1, so the SDK
	// emits /chat/completions relative to it.
	req, err := http.NewRequestWithContext(context.Background(), "POST",
		e.ts.URL+"/proxy/llm/openai/chat/completions", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer run-token-xyz")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode) // authed, then denied (deny-all permit)
	require.Equal(t, "run-token-xyz", e.auth.sawToken)
}

func TestUnknownProviderIs403(t *testing.T) {
	e := newEnv(t, "", mustPermit(t, `{"v":1,"llm":{"providers":["anthropic"]}}`))
	req, err := http.NewRequestWithContext(context.Background(), "POST",
		e.ts.URL+"/proxy/llm/copilot/v1/x", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestAllowedProviderDisallowedPathIs403(t *testing.T) {
	// The provider is permitted, but v1 allows exactly one (method, path)
	// per provider — anything else on the origin is outside the blast radius.
	e := newEnv(t, "", mustPermit(t, `{"v":1,"llm":{"providers":["anthropic"]}}`))

	req, err := http.NewRequestWithContext(context.Background(), "POST",
		e.ts.URL+"/proxy/llm/anthropic/v1/files", nil)
	require.NoError(t, err)
	req.Header.Set("x-api-key", "tok")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	// Wrong method on the allowed path is denied too.
	req, err = http.NewRequestWithContext(context.Background(), "GET",
		e.ts.URL+"/proxy/llm/anthropic/v1/messages", nil)
	require.NoError(t, err)
	req.Header.Set("x-api-key", "tok")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	require.Contains(t, e.events.events, "proxy.denied")
}

func TestPermitSourceFailureFailsClosed(t *testing.T) {
	e := newEnv(t, "", permit.Permit{})
	e.permits.err = errors.New("db down")
	resp := doAnthropic(t, e, "tok")
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}
```

(Task 7 adds `strings` and `time` to this import block when its tests need them.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd server && go test ./internal/proxy/`
Expected: FAIL (package does not exist).

- [ ] **Step 3: Implement**

`server/internal/proxy/proxy.go` — exactly the types, interfaces, `Config`, `DefaultConfig`, `NopHook{}` (`Before` returns nil), and `Deps` from the Produces block above. `DefaultConfig` returns a **fresh map each call** (callers mutate it in tests), and the upstream bases include whatever path prefix the SDK folds into its base URL — the SDK appends only the post-base path, which is what the proxy forwards:

```go
func DefaultConfig() Config {
	return Config{Providers: map[string]ProviderRoute{
		// The SDKs fold a version prefix into their base URL, so the
		// forwarded {path...} excludes it; each Base carries the prefix the
		// real host expects, and Path is exactly what the SDK emits. One
		// (method, path) per provider IS the v1 blast radius.
		"anthropic":  {Base: "https://api.anthropic.com", Method: "POST", Path: "v1/messages"},
		"openai":     {Base: "https://api.openai.com/v1", Method: "POST", Path: "chat/completions"},
		"openrouter": {Base: "https://openrouter.ai/api/v1", Method: "POST", Path: "chat/completions"},
	}}
}
```

Package comment:

```go
// Package proxy is the egress gateway: the permit's enforcement point and
// the only place credentials are attached to outbound traffic. It depends
// only on the permit package — auth, storage, and crypto reach it through
// the narrow interfaces in Deps, which is what lets it become a standalone
// service later without a redesign.
```

`server/internal/proxy/handler.go`:

```go
package proxy

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/gambtho/nightwatch/server/internal/permit"
)

type handler struct {
	d Deps
}

func RegisterRoutes(mux *http.ServeMux, d Deps) {
	h := &handler{d: d}
	mux.HandleFunc("/proxy/llm/{provider}/{path...}", h.llm)
	mux.HandleFunc("/proxy/internal/{path...}", h.internal)
}

// extractRunToken pulls the run token from the provider-native auth-header
// slot: the SDKs can send exactly one credential header, so the run token
// travels where the API key would.
func extractRunToken(provider string, r *http.Request) string {
	switch provider {
	case "anthropic":
		return r.Header.Get("x-api-key")
	default: // openai, openrouter (OpenAI-shaped)
		bearer, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		return bearer
	}
}

func (h *handler) emit(r *http.Request, id RunIdentity, typ string, payload map[string]any) {
	// Best-effort: a denial is enforced even if recording it fails.
	if err := h.d.Events.AppendEvent(r.Context(), id.TenantID, id.RunID, typ, payload); err != nil {
		slog.Error("proxy: append event", "type", typ, "run", id.RunID, "err", err)
	}
}

// authorize runs the front half of every LLM request: authenticate,
// resolve the permit (per request — no cache, by design), check the
// provider allowlist, and check the provider's one allowed (method, path).
func (h *handler) authorize(w http.ResponseWriter, r *http.Request, provider string) (RunIdentity, permit.Permit, ProviderRoute, bool) {
	tok := extractRunToken(provider, r)
	if tok == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return RunIdentity{}, permit.Permit{}, ProviderRoute{}, false
	}
	id, err := h.d.Auth.VerifyRunToken(r.Context(), tok)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return RunIdentity{}, permit.Permit{}, ProviderRoute{}, false
	}

	p, err := h.d.Permits.PermitForRun(r.Context(), id.TenantID, id.RunID)
	if err != nil {
		// Fail closed: no permit, no egress.
		http.Error(w, "forbidden", http.StatusForbidden)
		return RunIdentity{}, permit.Permit{}, ProviderRoute{}, false
	}

	route, known := h.d.Config.Providers[provider]
	if !known || !p.AllowsProvider(provider) {
		h.emit(r, id, "proxy.denied", map[string]any{"provider": provider})
		http.Error(w, "forbidden", http.StatusForbidden)
		return RunIdentity{}, permit.Permit{}, ProviderRoute{}, false
	}
	// One operation per provider is the whole v1 blast radius: without
	// this, a run token buys the entire provider origin.
	if r.Method != route.Method || r.PathValue("path") != route.Path {
		h.emit(r, id, "proxy.denied", map[string]any{
			"provider": provider, "reason": "path", "method": r.Method, "path": r.PathValue("path"),
		})
		http.Error(w, "forbidden", http.StatusForbidden)
		return RunIdentity{}, permit.Permit{}, ProviderRoute{}, false
	}
	return id, p, route, true
}

func (h *handler) llm(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	id, p, route, ok := h.authorize(w, r, provider)
	if !ok {
		return
	}
	h.forward(w, r, provider, route, id, p) // Task 7
}

func (h *handler) internal(w http.ResponseWriter, r *http.Request) {
	h.passthrough(w, r) // Task 7
}
```

Plus, in the same file for this task only, stubs that Task 7 replaces (they keep the package compiling and every deny-path test honest):

```go
func (h *handler) forward(w http.ResponseWriter, r *http.Request, provider string, route ProviderRoute, id RunIdentity, p permit.Permit) {
	http.Error(w, "not implemented", http.StatusBadGateway)
}

func (h *handler) passthrough(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusBadGateway)
}
```

- [ ] **Step 4: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS. Also verify the import constraint: `grep -n "nightwatch/server/internal" internal/proxy/*.go` must show only `internal/permit`.

- [ ] **Step 5: Commit**

```bash
git add server
git commit -m "feat(server): proxy core — run-token auth, permit resolution, fail-closed deny path"
```

---

### Task 7: Proxy forwarding — injection, streaming, internal pass-through

**Files:**

- Modify: `server/internal/proxy/handler.go` (replace both stubs)
- Test: `server/internal/proxy/handler_test.go` (extend)

**Interfaces:**

- Consumes: Task 6's handler plumbing.
- Produces: real `forward` (strips the run token, injects the credential in the provider's slot, reverse-proxies with streaming, emits `proxy.request` with provider/status/duration, `proxy.error` on credential failure) and `passthrough` (forwards `/proxy/internal/{path...}` to `Config.InternalBase` preserving method, body, and `Authorization` — the internal API does its own auth).

- [ ] **Step 1: Write the failing tests**

Append to `server/internal/proxy/handler_test.go`:

```go
func TestForwardInjectsCredentialAndStrips(t *testing.T) {
	var gotAPIKey, gotAuthz string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("x-api-key")
		gotAuthz = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(upstream.Close)

	e := newEnv(t, upstream.URL, mustPermit(t, `{"v":1,"llm":{"providers":["anthropic"]}}`))
	resp := doAnthropic(t, e, "run-token")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "real-key", gotAPIKey)     // injected
	require.Empty(t, gotAuthz)                  // nothing else leaks upstream
	require.Contains(t, e.events.events, "proxy.request")
}

func TestForwardOpenAIUsesBearerSlot(t *testing.T) {
	var gotAuthz, gotAPIKey string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthz = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("x-api-key")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	e := newEnv(t, upstream.URL, mustPermit(t, `{"v":1,"llm":{"providers":["openai"]}}`))
	// SDK-faithful path: openai-go emits /chat/completions relative to its
	// /v1-suffixed base.
	req, err := http.NewRequestWithContext(context.Background(), "POST",
		e.ts.URL+"/proxy/llm/openai/chat/completions", strings.NewReader(`{}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer run-token")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "Bearer real-key", gotAuthz)
	require.Empty(t, gotAPIKey)
}

func TestForwardStreamsIncrementally(t *testing.T) {
	first := make(chan struct{})
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: one\n\n"))
		w.(http.Flusher).Flush()
		close(first)
		<-release
		_, _ = w.Write([]byte("data: two\n\n"))
	}))
	t.Cleanup(upstream.Close)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	e := newEnv(t, upstream.URL, mustPermit(t, `{"v":1,"llm":{"providers":["anthropic"]}}`))
	req, err := http.NewRequestWithContext(context.Background(), "POST",
		e.ts.URL+"/proxy/llm/anthropic/v1/messages", nil)
	require.NoError(t, err)
	req.Header.Set("x-api-key", "tok")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })

	// The first chunk must arrive while the upstream is still holding the
	// stream open — proof the proxy flushes instead of buffering.
	<-first
	buf := make([]byte, 64)
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	go func() { n, err := resp.Body.Read(buf); done <- result{n, err} }()
	select {
	case res := <-done:
		require.NoError(t, res.err)
		require.Contains(t, string(buf[:res.n]), "data: one")
	case <-time.After(2 * time.Second):
		t.Fatal("first chunk not delivered before upstream finished — proxy is buffering")
	}
	close(release)
}

type fakeHook struct{ err error }

func (f fakeHook) Before(ctx context.Context, req proxy.HookRequest) error { return f.err }

func TestHookErrorChoosesStatus(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("hook rejection must not reach upstream")
	}))
	t.Cleanup(upstream.Close)

	e := &env{
		auth:    &fakeAuth{identity: proxy.RunIdentity{TenantID: uuid.New(), RunID: uuid.New()}},
		permits: &fakePermits{permit: mustPermit(t, `{"v":1,"llm":{"providers":["anthropic"]}}`)},
		creds:   &fakeCreds{secret: proxy.Secret{Value: "real-key"}},
		events:  &fakeEvents{},
	}
	cfg := proxy.DefaultConfig()
	route := cfg.Providers["anthropic"]
	route.Base = upstream.URL
	cfg.Providers["anthropic"] = route
	mux := http.NewServeMux()
	proxy.RegisterRoutes(mux, proxy.Deps{Auth: e.auth, Permits: e.permits, Credentials: e.creds,
		Events: e.events, Hook: fakeHook{err: proxy.HookError{Status: http.StatusTooManyRequests, Msg: "budget"}},
		Config: cfg})
	e.ts = httptest.NewServer(mux)
	t.Cleanup(e.ts.Close)

	resp := doAnthropic(t, e, "tok")
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
}

func TestCredentialFailureIs500WithEvent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("must not reach upstream without a credential")
	}))
	t.Cleanup(upstream.Close)

	e := newEnv(t, upstream.URL, mustPermit(t, `{"v":1,"llm":{"providers":["anthropic"]}}`))
	e.creds.err = errors.New("kek unwrap failed")
	resp := doAnthropic(t, e, "tok")
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	require.Contains(t, e.events.events, "proxy.error")
}

func TestInternalPassthrough(t *testing.T) {
	var gotPath, gotAuthz string
	internalAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthz = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(internalAPI.Close)

	e := &env{
		auth:    &fakeAuth{},
		permits: &fakePermits{},
		creds:   &fakeCreds{},
		events:  &fakeEvents{},
	}
	cfg := proxy.DefaultConfig()
	cfg.InternalBase = internalAPI.URL
	mux := http.NewServeMux()
	proxy.RegisterRoutes(mux, proxy.Deps{Auth: e.auth, Permits: e.permits,
		Credentials: e.creds, Events: e.events, Hook: proxy.NopHook{}, Config: cfg})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	req, err := http.NewRequestWithContext(context.Background(), "POST",
		ts.URL+"/proxy/internal/internal/runs/abc/events", strings.NewReader(`{"type":"x"}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer run-token")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Equal(t, "/internal/runs/abc/events", gotPath)
	require.Equal(t, "Bearer run-token", gotAuthz) // bearer forwarded; internal API re-auths it
}
```

(`strings` joins the test imports; remove the `var _ = time.Second` / `var _ = json.Marshal` placeholders from Task 6 if the linter flags them — `time` and `json` are now genuinely used or removable.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd server && go test ./internal/proxy/`
Expected: FAIL — the stubs return 502.

- [ ] **Step 3: Implement**

Replace both stubs in `server/internal/proxy/handler.go`:

```go
func (h *handler) forward(w http.ResponseWriter, r *http.Request, provider string, route ProviderRoute, id RunIdentity, p permit.Permit) {
	if err := h.d.Hook.Before(r.Context(), HookRequest{Identity: id, Provider: provider}); err != nil {
		// The typed HookError picks 403 vs 429 (Plan 3 metering); anything
		// else fails closed as 403.
		status := http.StatusForbidden
		var he HookError
		if errors.As(err, &he) && (he.Status == http.StatusForbidden || he.Status == http.StatusTooManyRequests) {
			status = he.Status
		}
		http.Error(w, http.StatusText(status), status)
		return
	}
	secret, err := h.d.Credentials.Credential(r.Context(), id.TenantID, p.LLM.Connection, provider)
	if err != nil {
		h.emit(r, id, "proxy.error", map[string]any{"provider": provider, "stage": "credential"})
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	upstream, err := url.Parse(route.Base)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	start := time.Now()
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	rp := &httputil.ReverseProxy{
		FlushInterval: -1, // SSE: flush every write, never buffer
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(upstream)
			// SetURL joins the inbound path (which still carries the
			// /proxy/llm/{provider} prefix); replace it with the upstream's
			// own path prefix + the forwarded remainder.
			pr.Out.URL.Path = singleJoin(upstream.Path, pr.In.PathValue("path"))
			pr.Out.URL.RawPath = ""
			pr.Out.Host = upstream.Host
			// The run token must never reach the provider; the real
			// credential goes in the provider's native slot only.
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("x-api-key")
			switch provider {
			case "anthropic":
				pr.Out.Header.Set("x-api-key", secret.Value)
			default:
				pr.Out.Header.Set("Authorization", "Bearer "+secret.Value)
			}
		},
	}
	rp.ServeHTTP(sw, r)
	h.emit(r, id, "proxy.request", map[string]any{
		"provider":    provider,
		"status":      sw.status,
		"duration_ms": time.Since(start).Milliseconds(),
	})
}

// passthrough forwards /proxy/internal/{path...} to the internal API
// unchanged (method, body, bearer). The internal API performs its own
// run-token auth, so this route adds reachability, not authority — it
// exists so a sandboxed actor whose only egress is the proxy can still
// deliver run records.
func (h *handler) passthrough(w http.ResponseWriter, r *http.Request) {
	base, err := url.Parse(h.d.Config.InternalBase)
	if err != nil || h.d.Config.InternalBase == "" {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	rp := &httputil.ReverseProxy{
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(base)
			pr.Out.URL.Path = singleJoin(base.Path, pr.In.PathValue("path"))
			pr.Out.URL.RawPath = ""
			pr.Out.Host = base.Host
		},
	}
	rp.ServeHTTP(w, r)
}

// singleJoin joins an upstream base path and a forwarded remainder with
// exactly one slash between them.
func singleJoin(basePath, rest string) string {
	return strings.TrimSuffix(basePath, "/") + "/" + rest
}

// statusWriter records the upstream status for the audit event.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
```

(add imports `errors`, `net/http/httputil`, `net/url`, `time` to handler.go; async `TouchConnection` is the adapter's concern, not the proxy's — the proxy never sees connection IDs. `HookError.Error()` is simply `func (e HookError) Error() string { return e.Msg }` in proxy.go.)

- [ ] **Step 4: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS, including the streaming test.

- [ ] **Step 5: Commit**

```bash
git add server
git commit -m "feat(server): proxy forwarding — credential injection, SSE streaming, internal pass-through"
```

---

### Task 8: Proxy adapters over store/token/vault

**Files:**

- Create: `server/internal/proxyadapter/adapter.go`
- Test: `server/internal/proxyadapter/adapter_test.go`

**Interfaces:**

- Consumes: `proxy` interfaces (Task 6), `store` (Tasks 2-4 semantics), `token`, `vault`, `permit`.
- Produces:

```go
package proxyadapter
func New(s *store.Store, signer *token.Signer, master *vault.Master, platform map[string]string) Set
type Set struct {
	Auth        proxy.AuthSource
	Permits     proxy.PermitSource
	Credentials proxy.CredentialSource
	Events      proxy.EventSink
}
```

Semantics: `Auth.VerifyRunToken` = signature/expiry (`signer.Verify`) + `GetRun` + `token.EqualHash(signer.HashToken(bearer), run.TokenHash)` + `run.Status` in (`pending`,`running`) — any miss is an error (finalized runs fail on BOTH the cleared hash and the status check). `Permits.PermitForRun` = `GetRun` → `GetVersion` → `permit.Parse(version.Doc.Permit)`. `Credentials.Credential` = tenant BYO connection for (provider, name) decrypted via the vault (+ async best-effort `TouchConnection`), falling back to `platform[provider]` when the connection is absent **and** name is `"default"`; a non-default missing connection is an error. `Events.AppendEvent` = `store.AppendRunEvent` with the payload JSON-marshaled.

- [ ] **Step 1: Write the failing tests**

`server/internal/proxyadapter/adapter_test.go`:

```go
package proxyadapter_test

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/proxyadapter"
	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
	"github.com/gambtho/nightwatch/server/internal/token"
	"github.com/gambtho/nightwatch/server/internal/vault"
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
		Steps: store.StepsDoc{SystemPrompt: "x", Kickoff: "y", Provider: "anthropic", Model: "m", MaxTokens: 100},
		Permit: []byte(`{"v":1,"llm":{"providers":["anthropic"]},"connections":{}}`),
		Rubric: []byte(`{}`),
	})
	require.NoError(t, err)
	_, err = s.ApproveVersion(ctx, tn.ID, wf.ID, 1, user.ID)
	require.NoError(t, err)

	signer := token.New([]byte("0123456789abcdef0123456789abcdef"))
	set := proxyadapter.New(s, signer, master, map[string]string{"anthropic": "platform-key"})
	return &env{pool: pool, store: s, signer: signer, master: master, set: set, tenant: tn, wf: wf}
}

func (e *env) mintRun(t *testing.T) (uuid.UUID, string) {
	t.Helper()
	runID := uuid.New()
	bearer, hash, err := e.signer.Sign(token.RunClaims{
		RunID: runID, TenantID: e.tenant.ID, ExpiresAt: time.Now().Add(time.Hour),
	})
	require.NoError(t, err)
	_, err = e.store.CreateRun(context.Background(), e.tenant.ID, e.wf.ID, runID, 1, hash)
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
	_, err = e.store.FinalizeRun(ctx, e.tenant.ID, runID, store.RunFinal{Status: "succeeded"})
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd server && go test ./internal/proxyadapter/`
Expected: FAIL (package does not exist).

- [ ] **Step 3: Implement**

`server/internal/proxyadapter/adapter.go`:

```go
// Package proxyadapter implements the proxy's narrow interfaces over the
// control plane's real store, token signer, and vault. It is the only
// place proxy-bound secrets are decrypted.
package proxyadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/gambtho/nightwatch/server/internal/permit"
	"github.com/gambtho/nightwatch/server/internal/proxy"
	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/token"
	"github.com/gambtho/nightwatch/server/internal/vault"
)

type Set struct {
	Auth        proxy.AuthSource
	Permits     proxy.PermitSource
	Credentials proxy.CredentialSource
	Events      proxy.EventSink
}

func New(s *store.Store, signer *token.Signer, master *vault.Master, platform map[string]string) Set {
	return Set{
		Auth:        &auth{store: s, signer: signer},
		Permits:     &permits{store: s},
		Credentials: &credentials{store: s, master: master, platform: platform},
		Events:      &events{store: s},
	}
}

type auth struct {
	store  *store.Store
	signer *token.Signer
}

func (a *auth) VerifyRunToken(ctx context.Context, bearer string) (proxy.RunIdentity, error) {
	claims, err := a.signer.Verify(bearer)
	if err != nil {
		return proxy.RunIdentity{}, err
	}
	run, err := a.store.GetRun(ctx, claims.TenantID, claims.RunID)
	if err != nil {
		return proxy.RunIdentity{}, err
	}
	// A finalized run has a cleared hash, so this also acts as revocation.
	if !token.EqualHash(a.signer.HashToken(bearer), run.TokenHash) {
		return proxy.RunIdentity{}, errors.New("proxyadapter: token not bound to run")
	}
	if run.Status != "pending" && run.Status != "running" {
		return proxy.RunIdentity{}, fmt.Errorf("proxyadapter: run is %s", run.Status)
	}
	return proxy.RunIdentity{TenantID: claims.TenantID, RunID: claims.RunID}, nil
}

type permits struct {
	store *store.Store
}

func (p *permits) PermitForRun(ctx context.Context, tenantID, runID uuid.UUID) (permit.Permit, error) {
	run, err := p.store.GetRun(ctx, tenantID, runID)
	if err != nil {
		return permit.Permit{}, err
	}
	version, err := p.store.GetVersion(ctx, tenantID, run.WorkflowID, run.Version)
	if err != nil {
		return permit.Permit{}, err
	}
	return permit.Parse(version.Doc.Permit)
}

type credentials struct {
	store    *store.Store
	master   *vault.Master
	platform map[string]string
}

func (c *credentials) Credential(ctx context.Context, tenantID uuid.UUID, name, provider string) (proxy.Secret, error) {
	conn, err := c.store.GetConnection(ctx, tenantID, provider, name)
	switch {
	case err == nil:
		// Decrypt with the KEK version that wrapped this connection —
		// rotation-safe even while a rewrap job is mid-flight.
		wrapped, kerr := c.store.TenantKEKAt(ctx, tenantID, conn.KEKVersion)
		if kerr != nil {
			return proxy.Secret{}, kerr
		}
		value, derr := c.master.DecryptSecret(wrapped, conn.DEKWrapped, conn.Ciphertext, conn.Nonce)
		if derr != nil {
			return proxy.Secret{}, derr
		}
		go func() {
			if terr := c.store.TouchConnection(context.WithoutCancel(ctx), tenantID, conn.ID); terr != nil {
				slog.Error("proxyadapter: touch connection", "err", terr)
			}
		}()
		return proxy.Secret{Value: value}, nil
	case errors.Is(err, store.ErrNotFound) && name == "default":
		if key, ok := c.platform[provider]; ok && key != "" {
			return proxy.Secret{Value: key}, nil
		}
		return proxy.Secret{}, fmt.Errorf("proxyadapter: no platform key for %s", provider)
	default:
		return proxy.Secret{}, err
	}
}

type events struct {
	store *store.Store
}

func (e *events) AppendEvent(ctx context.Context, tenantID, runID uuid.UUID, typ string, payload map[string]any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return e.store.AppendRunEvent(ctx, tenantID, runID, typ, raw)
}
```

- [ ] **Step 4: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server
git commit -m "feat(server): proxy adapters — lifecycle-checked auth, permit lookup, vault-backed credentials"
```

---

### Task 9: Harness carries the run token instead of an API key

**Files:**

- Modify: `server/internal/harness/harness.go` (`Input`), `server/internal/harness/harness_test.go`, `server/cmd/nightshift/main.go` (one line in the runner closure)
- Test: `server/internal/harness/harness_test.go`

**Interfaces:**

- Consumes: nothing new.
- Produces: `harness.Input{Steps Steps; RunToken string}` — `APIKey` is gone. `Run` sets `CallOptions.APIKey = in.RunToken`, so the SDK carries the run token in its native auth-header slot for the proxy to verify, strip, and replace.

- [ ] **Step 1: Write the failing test**

In `server/internal/harness/harness_test.go`, add:

```go
func TestRunTokenRidesTheAPIKeySlot(t *testing.T) {
	var sawKey string
	provider := &keyCapturingProvider{onKey: func(k string) { sawKey = k }}
	_, err := harness.Run(context.Background(),
		harness.Input{Steps: steps(), RunToken: "run-jwt-abc"},
		harness.Deps{
			ProviderFactory: func(string) (llm.Provider, error) { return provider, nil },
			Sink:            &memSink{},
		})
	require.NoError(t, err)
	require.Equal(t, "run-jwt-abc", sawKey)
}

type keyCapturingProvider struct{ onKey func(string) }

func (p *keyCapturingProvider) Chat(ctx context.Context, msgs []llm.Message, opts llm.CallOptions, onChunk func(llm.StreamChunk)) (llm.Usage, error) {
	p.onKey(opts.APIKey)
	return llm.Usage{}, nil
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./internal/harness/`
Expected: FAIL (compile error — `Input` has no `RunToken`).

- [ ] **Step 3: Implement**

In `server/internal/harness/harness.go`:

```go
type Input struct {
	Steps Steps
	// RunToken is the run's bearer JWT. It rides in the provider-native
	// auth-header slot (CallOptions.APIKey) so the egress proxy can
	// authenticate the request, strip it, and inject the real credential.
	// No API key ever enters the harness.
	RunToken string
}
```

and in `Run`, the `Chat` call becomes:

```go
	usage, err := provider.Chat(ctx, msgs,
		llm.CallOptions{Model: in.Steps.Model, MaxTokens: maxTokens, APIKey: in.RunToken},
		func(c llm.StreamChunk) { out.WriteString(c.Delta) })
```

In `server/cmd/nightshift/main.go`, the runner closure's `harness.Input` literal becomes:

```go
			harness.Input{Steps: steps, RunToken: req.RunToken},
```

(`apiKeyFor` becomes unused by the closure — leave the function in place; Task 10 repurposes it for the proxy's platform-key map.)

- [ ] **Step 4: Run tests and full verification**

Run: `cd server && gofmt -l . && go vet ./... && go test ./...`
Expected: PASS (existing harness tests unaffected — they never asserted on APIKey; e2e passes because the scripted provider ignores CallOptions).

- [ ] **Step 5: Commit**

```bash
git add server
git commit -m "feat(server): harness carries the run token in the API-key slot — no credentials in the sandbox"
```

---

### Task 10: Wiring, e2e through the proxy, docs

**Files:**

- Modify: `server/cmd/nightshift/main.go` (serve wiring + doc comment), `server/e2e_test.go`, `docs/api/v1.md`, `server/README.md`
- Test: `server/e2e_test.go` (new `TestEndToEndRunThroughProxy`)

**Interfaces:**

- Consumes: everything above.
- Produces: a `serve()` in which the harness's LLM traffic flows through the mounted proxy with vault-backed credentials, and an e2e test proving a run completes with zero credentials in the harness.

- [ ] **Step 1: Wire serve()**

In `server/cmd/nightshift/main.go` `serve()`, after `signer := ...`:

```go
	master, err := vault.NewMaster(keyFromEnv("NIGHTSHIFT_VAULT_KEY"))
	if err != nil {
		return err
	}
	// Proxy-specific names, deliberately NOT the SDKs' well-known key
	// variables: the pinned SDK constructors auto-load those from the
	// environment into client options, which on Local compute (shared
	// process) would put real keys back into harness memory. These names
	// are invisible to the SDKs.
	platform := map[string]string{
		"anthropic":  os.Getenv("NIGHTSHIFT_PLATFORM_ANTHROPIC_KEY"),
		"openai":     os.Getenv("NIGHTSHIFT_PLATFORM_OPENAI_KEY"),
		"openrouter": os.Getenv("NIGHTSHIFT_PLATFORM_OPENROUTER_KEY"),
	}
```

(the `platform` map replaces per-run `apiKeyFor` usage — delete the now-unused `apiKeyFor` function; the old `ANTHROPIC_API_KEY`/`OPENAI_API_KEY`/`OPENROUTER_API_KEY` names are retired everywhere, including the doc comment), then replace `factory := llm.NewFactory(llm.Config{})` with proxy-pointed base URLs, and mount the proxy after the other routes:

```go
	baseURL := "http://" + addr
	factory := llm.NewFactory(llm.Config{
		AnthropicBaseURL:  baseURL + "/proxy/llm/anthropic",
		OpenAIBaseURL:     baseURL + "/proxy/llm/openai",
		OpenRouterBaseURL: baseURL + "/proxy/llm/openrouter",
	})
```

(move the existing `baseURL := "http://" + addr` line up so it precedes the factory), and after `internalapi.RegisterRoutes(...)`:

```go
	adapters := proxyadapter.New(s, signer, master, platform)
	cfg := proxy.DefaultConfig()
	cfg.InternalBase = baseURL
	proxy.RegisterRoutes(mux, proxy.Deps{
		Auth: adapters.Auth, Permits: adapters.Permits,
		Credentials: adapters.Credentials, Events: adapters.Events,
		Hook: proxy.NopHook{}, Config: cfg,
	})
```

`httpapi.Deps` gains `Vault: master`. Add imports `proxy`, `proxyadapter`, `vault`. Update the doc comment at the top of main.go: add `NIGHTSHIFT_VAULT_KEY` (base64, 32 bytes, required for serve/dev-session) and the three `NIGHTSHIFT_PLATFORM_*_KEY` variables (replacing the retired `*_API_KEY` lines).

**Server timeouts**: replace `return http.ListenAndServe(addr, mux)` with

```go
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		// WriteTimeout deliberately zero: streamed LLM responses run for
		// minutes and a server-wide write deadline would sever them.
	}
	return srv.ListenAndServe()
```

(add the `time` import if absent).

**Port `internal/redact` and wrap the logger** (the spec assigns the port here): copy `/home/tng/workspace/cronfoundry/internal/redact/redact.go` to `server/internal/redact/redact.go` unchanged except the package comment (do NOT copy `target.go` — it serves the publish package, which is Plan 4). Then create `server/internal/redact/slog.go`:

```go
package redact

import (
	"context"
	"log/slog"
)

// Handler wraps a slog.Handler, redacting known secret values from the
// message and every string attribute. Defense in depth: proxy and vault
// code never logs secrets on purpose; this catches accidents.
type Handler struct {
	Inner slog.Handler
	R     *Redactor
}

func (h Handler) Enabled(ctx context.Context, l slog.Level) bool { return h.Inner.Enabled(ctx, l) }
func (h Handler) WithGroup(name string) slog.Handler {
	return Handler{Inner: h.Inner.WithGroup(name), R: h.R}
}
func (h Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return Handler{Inner: h.Inner.WithAttrs(h.redactAttrs(attrs)), R: h.R}
}

func (h Handler) Handle(ctx context.Context, rec slog.Record) error {
	out := slog.NewRecord(rec.Time, rec.Level, h.R.Redact(rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(h.redactAttr(a))
		return true
	})
	return h.Inner.Handle(ctx, out)
}

func (h Handler) redactAttrs(attrs []slog.Attr) []slog.Attr {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = h.redactAttr(a)
	}
	return out
}

func (h Handler) redactAttr(a slog.Attr) slog.Attr {
	if a.Value.Kind() == slog.KindString {
		return slog.String(a.Key, h.R.Redact(a.Value.String()))
	}
	return a
}
```

and in `serve()`, right after building `platform`:

```go
	secretVals := make([]string, 0, len(platform))
	for _, v := range platform {
		secretVals = append(secretVals, v)
	}
	slog.SetDefault(slog.New(redact.Handler{
		Inner: slog.Default().Handler(),
		R:     redact.New(secretVals),
	}))
```

(add the `redact` import; `redact.New` already filters empty strings — that is cronfoundry's behavior, keep it.)

`server/internal/redact/slog_test.go`:

```go
package redact_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/redact"
)

func TestHandlerRedactsMessageAndAttrs(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(redact.Handler{
		Inner: slog.NewTextHandler(&buf, nil),
		R:     redact.New([]string{"sk-super-secret"}),
	})
	logger.Error("upstream said sk-super-secret", "detail", "key=sk-super-secret rest")
	out := buf.String()
	require.NotContains(t, out, "sk-super-secret")
	require.True(t, strings.Contains(out, "[REDACTED]"))
}
```

- [ ] **Step 2: Write the proxy e2e test**

Append to `server/e2e_test.go` (same package; reuses its helpers where sensible):

```go
// TestEndToEndRunThroughProxy proves the Plan 2 invariant: a run completes
// with ZERO credentials in the harness. The real ported openai provider is
// pointed at the proxy; the proxy authenticates the run token from the
// Authorization slot, injects the platform key, and forwards to a fake
// OpenAI upstream.
func TestEndToEndRunThroughProxy(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()

	sessionKey := bytes.Repeat([]byte{1}, 32)
	signer := token.New(bytes.Repeat([]byte{2}, 32))
	master, err := vault.NewMaster(bytes.Repeat([]byte{3}, 32))
	require.NoError(t, err)

	// Fake OpenAI upstream: asserts the injected platform key arrived (and
	// the run token did not), then streams one SSE chat chunk. Model the
	// body on internal/llm's openai fixture format — adjust until the
	// ported provider parses it; the provider's own tests show the shape.
	var upstreamAuth, upstreamPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAuth = r.Header.Get("Authorization")
		upstreamPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"proxied digest"}}]}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(upstream.Close)

	var baseURL string
	factory := func(name string) (llm.Provider, error) {
		return llm.NewOpenAI(baseURL + "/proxy/llm/openai"), nil
	}
	local := compute.NewLocal(t.TempDir(), func(ctx context.Context, req compute.InvokeRequest, stateDir string) {
		client := harness.NewClient(baseURL, req.RunID, req.RunToken)
		steps, err := client.Context(ctx)
		if err != nil {
			t.Errorf("harness context: %v", err)
			return
		}
		_, _ = harness.Run(ctx, harness.Input{Steps: steps, RunToken: req.RunToken}, harness.Deps{
			ProviderFactory: factory,
			Sink:            client,
		})
	})

	mux := http.NewServeMux()
	httpapi.RegisterRoutes(mux, httpapi.Deps{Store: s, SessionKey: sessionKey, Signer: signer, Compute: local, Vault: master})
	internalapi.RegisterRoutes(mux, internalapi.Deps{Store: s, Signer: signer})
	adapters := proxyadapter.New(s, signer, master, map[string]string{"openai": "platform-openai-key"})
	cfg := proxy.DefaultConfig()
	route := cfg.Providers["openai"]
	route.Base = upstream.URL // bare base: the SDK's emitted path arrives verbatim
	cfg.Providers["openai"] = route
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	baseURL = ts.URL
	cfg.InternalBase = baseURL
	proxy.RegisterRoutes(mux, proxy.Deps{Auth: adapters.Auth, Permits: adapters.Permits,
		Credentials: adapters.Credentials, Events: adapters.Events, Hook: proxy.NopHook{}, Config: cfg})

	wrapped, err := master.NewTenantKEK()
	require.NoError(t, err)
	tn, err := s.CreateTenant(ctx, "acme", wrapped)
	require.NoError(t, err)
	user, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)
	cookie, err := httpapi.SessionCookie(sessionKey,
		httpapi.SessionClaims{UserID: user.ID, TenantID: tn.ID, Role: "owner"}, time.Hour)
	require.NoError(t, err)

	do := newDoHelper(t, ts.URL, cookie)

	out := do("POST", "/v1/workflows", map[string]any{
		"name": "proxied digest",
		"steps": map[string]any{
			"system_prompt": "You prepare the weekly support digest.",
			"kickoff":       "Summarize last week's tickets.",
			"provider":      "openai",
			"model":         "gpt-4o-mini",
			"max_tokens":    256,
		},
		"permit": map[string]any{"v": 1, "llm": map[string]any{"providers": []string{"openai"}}, "connections": map[string]any{}},
	})
	wfID := out["workflow"].(map[string]any)["id"].(string)
	do("POST", "/v1/workflows/"+wfID+"/versions/1/approve", nil)
	out = do("POST", "/v1/workflows/"+wfID+"/runs", nil)
	runID := out["run"].(map[string]any)["id"].(string)

	local.Wait()

	out = do("GET", "/v1/runs/"+runID, nil)
	run := out["run"].(map[string]any)
	require.Equal(t, "succeeded", run["status"])
	require.Contains(t, run["output"], "proxied digest")
	require.Equal(t, "Bearer platform-openai-key", upstreamAuth) // injected, not the run token
	// The SDK emitted /chat/completions relative to its base; with a bare
	// upstream base the exact path proves the /v1 rewrite logic is right.
	require.Equal(t, "/chat/completions", upstreamPath)

	out = do("GET", "/v1/runs/"+runID+"/events", nil)
	var types []string
	for _, ev := range out["events"].([]any) {
		types = append(types, ev.(map[string]any)["type"].(string))
	}
	require.Contains(t, types, "proxy.request")
}
```

Refactor note (do it, it's mechanical): extract the existing `TestEndToEndRun`'s inline `do` closure into a shared `newDoHelper(t *testing.T, base string, cookie *http.Cookie) func(method, path string, body any) map[string]any` at file scope so both tests use it, and update the existing test's `CreateTenant` call to a real minted KEK (it already needs a `vault.Master` only if it uses connections — it doesn't, so `testKEK`-style opaque bytes remain fine there; keep whatever Task 3 left). Add imports `vault`, `proxy`, `proxyadapter` to e2e_test.go.

**Note on ordering in this test:** `proxy.RegisterRoutes` is called after `httptest.NewServer(mux)` — that is fine (`ServeMux` registration is safe before first use of those paths), but keep the registration before any request is fired, as written.

**If the fake upstream's SSE shape doesn't parse:** the ported provider's own tests (`server/internal/llm/openai_test.go` and its fixture helper) contain the exact chunk format the SDK accepts — copy the minimal streaming body from there rather than guessing. Adjust the fake, not the provider.

- [ ] **Step 3: Run the new test, then full verification**

Run: `cd server && go test . -run TestEndToEndRunThroughProxy -v`
Expected: PASS (iterate on the fake upstream shape if the provider rejects it).
Then: `cd server && go build ./... && gofmt -l . && go vet ./... && go test ./...`
Expected: all green.

- [ ] **Step 4: Update the docs**

`docs/api/v1.md`:

- Add the three `/v1/connections` endpoints to the endpoint table (write-only semantics: responses never include values; DELETE takes `?provider=`).
- Document permit v1 in the Resources section: the JSON shape, fail-closed semantics (empty providers = no egress; invalid permit = 400), per-provider `default` connection resolution.
- Note that `/proxy/*` routes exist but are **not** part of the public v1 contract (harness-facing, run-token authenticated).

`server/README.md`:

- Add `NIGHTSHIFT_VAULT_KEY` to the env var list; note the `*_API_KEY` vars are now the proxy's platform-default keys and that no credential reaches the harness.
- One paragraph on the proxy: what it enforces, and the enforcement-posture caveat (advisory on Local compute; the guarantee arrives with sandboxed compute — link the spec).
- Add `internal/permit`, `internal/vault`, `internal/proxy`, `internal/proxyadapter` rows to the layout table.

- [ ] **Step 5: Format docs and commit**

```bash
cd /home/tng/workspace/nightshift-worktrees/proxy-spec
npx prettier --write docs/api/v1.md server/README.md
git add server docs/api
git commit -m "feat(server): wire the egress proxy — LLM traffic permit-checked, credentials injected at the boundary"
```

---

## Final verification (after all tasks)

1. `cd server && gofmt -l . && go vet ./... && go build ./... && go test ./...` — all green.
2. Import-boundary check: `grep -rn "nightwatch/server/internal" server/internal/proxy/*.go` shows only `internal/permit`; `grep -rn "internal/httpapi\|internal/internalapi" server/internal/proxy server/internal/proxyadapter` shows nothing.
3. Credential-leak check: `grep -rn "APIKey" server/internal/harness/` shows no field named APIKey; `grep -rn "ANTHROPIC_API_KEY\|OPENAI_API_KEY\|OPENROUTER_API_KEY" server/` matches NOTHING (the SDK-visible names are retired; only `NIGHTSHIFT_PLATFORM_*_KEY` may appear, and only in `cmd/nightshift/main.go`).
   3b. Secret-logging check: `grep -rn "secret\|Secret" server/internal/proxy server/internal/proxyadapter | grep -i "slog\|log\."` shows no line that logs a secret value — BYOK values are protected by construction, per the spec's redaction policy.
4. Spec boundary check: primitives delivered are 1 (permit enforcement, LLM scope) and 5 (vault, keys-first); grading/metering/scheduling untouched; `Hook` exists and is a no-op.
5. `npm test` from the repo root — the prototype's 46 tests still pass.

## Deviations log

Executors: record deviations in `implementation-notes.md` at the worktree root; it is folded into the PR description at the end.
