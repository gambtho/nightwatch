// Package boot is the packaging spike's supervisor: it owns the state
// directory, an embedded Postgres, generated secrets, and a `tomte serve`
// child process. It exists to prove the risky packaging chain
// (init/start Postgres → migrations → server boot → loopback ready) end
// to end; production code will replace the subprocess with serve() as a
// library entry point once P1 lands.
package boot

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

// Config describes one Tomte install's state on disk.
type Config struct {
	StateDir  string // per-user application data dir; the spike defaults to ~/.local/share/tomte-spike
	ServerBin string // path to the tomte binary to supervise
	Logf      func(format string, args ...any)
}

// secrets is what production stores in the OS keychain; the spike keeps it
// in a 0600 file in the state dir — the spec's named Linux fallback shape.
type secrets struct {
	VaultKey   string `json:"vault_key"`   // base64 32 bytes; encrypts everything in the vault — must be stable across runs
	RunnerKey  string `json:"runner_key"`  // base64 32 bytes; signs run tokens
	PGPassword string `json:"pg_password"` // per-install random Postgres password
}

// Supervisor holds the running child tree.
type Supervisor struct {
	cfg     Config
	pg      *embeddedpostgres.EmbeddedPostgres
	pgPort  int
	server  *exec.Cmd
	BaseURL string
	// FirstRun is true when this boot initialized a fresh data directory.
	FirstRun bool
}

func (c Config) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

// Start brings up the full chain: state dir → secrets → Postgres →
// tomte serve (which runs migrations before binding) → loopback ready.
func Start(ctx context.Context, cfg Config) (*Supervisor, error) {
	s := &Supervisor{cfg: cfg}

	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return nil, fmt.Errorf("state dir: %w", err)
	}
	sec, firstSecrets, err := loadOrCreateSecrets(filepath.Join(cfg.StateDir, "secrets.json"))
	if err != nil {
		return nil, err
	}

	pgData := filepath.Join(cfg.StateDir, "pgdata")
	s.FirstRun = firstSecrets
	if _, err := os.Stat(filepath.Join(pgData, "PG_VERSION")); errors.Is(err, os.ErrNotExist) {
		s.FirstRun = true
	}

	// Loopback TCP on a per-install random port with a random password —
	// the spec's Windows posture, used everywhere in the spike for
	// simplicity; Unix-domain sockets on macOS/Linux are a production
	// follow-up.
	s.pgPort, err = freePort()
	if err != nil {
		return nil, err
	}
	cfg.logf("postgres: starting on 127.0.0.1:%d (data: %s, first run: %v)", s.pgPort, pgData, s.FirstRun)

	s.pg = embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V16).
		Port(uint32(s.pgPort)).
		Username("tomte").
		Password(sec.PGPassword).
		Database("tomte").
		DataPath(pgData).
		RuntimePath(filepath.Join(cfg.StateDir, "pgrun")).
		CachePath(filepath.Join(cfg.StateDir, "pgcache")).
		StartTimeout(60 * time.Second).
		Logger(io.Discard))
	if err := s.pg.Start(); err != nil {
		return nil, fmt.Errorf("embedded postgres: %w", err)
	}

	httpPort, err := freePort()
	if err != nil {
		s.stopPG()
		return nil, err
	}
	addr := fmt.Sprintf("127.0.0.1:%d", httpPort)
	s.BaseURL = "http://" + addr

	dbURL := fmt.Sprintf("postgres://tomte:%s@127.0.0.1:%d/tomte?sslmode=disable", sec.PGPassword, s.pgPort)
	cfg.logf("server: starting %s at %s", cfg.ServerBin, s.BaseURL)
	s.server = exec.CommandContext(ctx, cfg.ServerBin, "serve")
	s.server.Env = append(os.Environ(),
		"DATABASE_URL="+dbURL,
		"TOMTE_PUBLIC_BASE_URL="+s.BaseURL,
		"TOMTE_LISTEN_ADDR="+addr,
		"TOMTE_RUNNER_KEY="+sec.RunnerKey,
		"TOMTE_VAULT_KEY="+sec.VaultKey,
		"TOMTE_STATE_DIR="+filepath.Join(cfg.StateDir, "actors"),
	)
	s.server.Stdout = os.Stderr
	s.server.Stderr = os.Stderr
	if err := s.server.Start(); err != nil {
		s.stopPG()
		return nil, fmt.Errorf("server start: %w", err)
	}

	// serve() runs migrations before binding, so a listening socket means
	// the schema is current. 401 on a session-authed route proves the API
	// stack (not just a TCP listener) is up.
	if err := waitReady(ctx, s.BaseURL+"/v1/catalog", 60*time.Second); err != nil {
		s.Stop()
		return nil, fmt.Errorf("server never became ready: %w", err)
	}
	return s, nil
}

// Stop tears down in reverse order: server first, then Postgres.
func (s *Supervisor) Stop() {
	if s.server != nil && s.server.Process != nil {
		_ = s.server.Process.Kill()
		_, _ = s.server.Process.Wait()
	}
	s.stopPG()
}

func (s *Supervisor) stopPG() {
	if s.pg != nil {
		if err := s.pg.Stop(); err != nil {
			s.cfg.logf("postgres stop: %v", err)
		}
	}
}

func loadOrCreateSecrets(path string) (secrets, bool, error) {
	var sec secrets
	b, err := os.ReadFile(path)
	if err == nil {
		return sec, false, json.Unmarshal(b, &sec)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return sec, false, err
	}
	sec = secrets{
		VaultKey:   randKeyB64(),
		RunnerKey:  randKeyB64(),
		PGPassword: randKeyB64(),
	}
	b, err = json.Marshal(sec)
	if err != nil {
		return sec, false, err
	}
	return sec, true, os.WriteFile(path, b, 0o600)
}

func randKeyB64() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func waitReady(ctx context.Context, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusUnauthorized {
				return nil
			}
			return fmt.Errorf("unexpected status %d probing %s", resp.StatusCode, url)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timeout after %s", timeout)
}
