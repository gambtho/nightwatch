// Command tomte runs the Tomte control plane.
//
//	tomte migrate      apply database migrations and exit
//	tomte serve        migrate, then serve the public and internal APIs
//	tomte dev-session  mint a tenant, owner, and session cookie for local use
//
// Configuration (env):
//
//	DATABASE_URL            Postgres DSN (required)
//	TOMTE_PUBLIC_BASE_URL
//	                        optional origin override (dev topologies like
//	                        Vite-as-origin); by default serve derives the
//	                        loopback origin from the bound listener.
//	                        HTTPS required; http for loopback hosts only.
//	TOMTE_RUNNER_KEY   base64, 32 bytes (required for serve)
//	TOMTE_VAULT_KEY    base64, 32 bytes (required for serve;
//	                        dev-session needs it only when minting a new
//	                        tenant)
//	TOMTE_LISTEN_ADDR  default 127.0.0.1:8080
//	TOMTE_STATE_DIR    actor state root, default $TMPDIR/tomte-actors
//	TOMTE_PLATFORM_ANTHROPIC_KEY, TOMTE_PLATFORM_OPENAI_KEY,
//	TOMTE_PLATFORM_OPENROUTER_KEY
//	                        platform model credentials, per provider — injected
//	                        by the egress proxy, never visible to the harness
//	TOMTE_RUN_PROVIDER, TOMTE_RUN_MODEL
//	                        legacy env-mode execution pair, used only while
//	                        no endpoint is configured; defaults anthropic /
//	                        claude-haiku-4-5, and must then be a priced
//	                        pair or approvals 400.
//	TOMTE_RUN_TOKEN_TTL  Go duration, default 1h
//	TOMTE_RUN_DEADLINE   Go duration, default 2h; must exceed
//	                          TOMTE_RUN_TOKEN_TTL — a run whose token
//	                          expired can never finalize itself, so the
//	                          reaper only sweeps runs past a strictly longer
//	                          deadline
//	TOMTE_DEFAULT_MONTHLY_CAP_CENTS
//	                          default monthly budget in cents (the user's
//	                          own spend from their key), default 0
//	                          (unlimited)
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"

	server "github.com/gambtho/tomte/server"
	"github.com/gambtho/tomte/server/internal/db"
	"github.com/gambtho/tomte/server/internal/httpapi"
	"github.com/gambtho/tomte/server/internal/store"
	"github.com/gambtho/tomte/server/internal/vault"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: tomte <serve|migrate|dev-session>")
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
		slog.Error("tomte", "err", err)
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

func durationFromEnv(name string, fallback time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		slog.Error("env var must be a positive Go duration", "name", name, "value", v)
		os.Exit(2)
	}
	return d
}

func intFromEnv(name string, fallback int) int {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		slog.Error("env var must be a non-negative integer", "name", name, "value", v)
		os.Exit(2)
	}
	return n
}

func serve(ctx context.Context) error {
	opts := server.Options{
		DatabaseURL:            mustEnv("DATABASE_URL"),
		ListenAddr:             os.Getenv("TOMTE_LISTEN_ADDR"),
		PublicBaseURL:          os.Getenv("TOMTE_PUBLIC_BASE_URL"), // optional: default is the bound loopback origin
		RunnerKey:              keyFromEnv("TOMTE_RUNNER_KEY"),
		VaultKey:               keyFromEnv("TOMTE_VAULT_KEY"),
		StateDir:               os.Getenv("TOMTE_STATE_DIR"),
		RunProvider:            os.Getenv("TOMTE_RUN_PROVIDER"),
		RunModel:               os.Getenv("TOMTE_RUN_MODEL"),
		RunTokenTTL:            durationFromEnv("TOMTE_RUN_TOKEN_TTL", time.Hour),
		RunDeadline:            durationFromEnv("TOMTE_RUN_DEADLINE", 2*time.Hour),
		DefaultMonthlyCapCents: intFromEnv("TOMTE_DEFAULT_MONTHLY_CAP_CENTS", 0),
		// Proxy-specific names, deliberately NOT the SDKs' well-known key
		// variables: the pinned SDK constructors auto-load those from the
		// environment, which on Local compute (shared process) would put
		// real keys back into harness memory.
		PlatformKeys: map[string]string{
			"anthropic":  os.Getenv("TOMTE_PLATFORM_ANTHROPIC_KEY"),
			"openai":     os.Getenv("TOMTE_PLATFORM_OPENAI_KEY"),
			"openrouter": os.Getenv("TOMTE_PLATFORM_OPENROUTER_KEY"),
		},
	}
	sv, err := server.Start(ctx, opts)
	if err != nil {
		return err
	}
	return <-sv.Err()
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

	// Resolve the email first: the global unique index means an email maps
	// to at most one tenant, so an existing user's tenant is reused and
	// only a genuinely new email mints one.
	var tn store.Tenant
	user, err := s.UserByEmail(ctx, *email)
	switch {
	case err == nil:
		if tn, err = s.GetTenant(ctx, user.TenantID); err != nil {
			return err
		}
		if *tenantID != "" {
			id, err := uuidParse(*tenantID)
			if err != nil {
				return err
			}
			if id != tn.ID {
				return fmt.Errorf("email %s already belongs to tenant %s, not %s", user.Email, tn.ID, id)
			}
		}
	case errors.Is(err, store.ErrNotFound):
		if *tenantID != "" {
			id, err := uuidParse(*tenantID)
			if err != nil {
				return err
			}
			if tn, err = s.GetTenant(ctx, id); err != nil {
				return err
			}
		} else {
			master, err := vault.NewMaster(keyFromEnv("TOMTE_VAULT_KEY"))
			if err != nil {
				return err
			}
			wrapped, err := master.NewTenantKEK()
			if err != nil {
				return err
			}
			if tn, err = s.CreateTenant(ctx, *tenantName, wrapped); err != nil {
				return err
			}
		}
		if user, err = s.UpsertUser(ctx, tn.ID, *email); err != nil {
			return err
		}
	default:
		return err
	}

	cookie, err := httpapi.MintLocalSession(ctx, s, nil, tn.ID, user.ID)
	if err != nil {
		return err
	}
	fmt.Printf("tenant: %s\nuser:   %s\ncookie: %s=%s\n", tn.ID, user.ID, cookie.Name, cookie.Value)
	return nil
}

func uuidParse(s string) (id uuid.UUID, err error) { return uuid.Parse(s) }
