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
	"github.com/google/uuid"
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

func uuidParse(s string) (id uuid.UUID, err error) { return uuid.Parse(s) }
