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
//	NIGHTSHIFT_VAULT_KEY    base64, 32 bytes (required for serve/dev-session)
//	NIGHTSHIFT_LISTEN_ADDR  default 127.0.0.1:8080
//	NIGHTSHIFT_STATE_DIR    actor state root, default $TMPDIR/nightshift-actors
//	NIGHTSHIFT_PLATFORM_ANTHROPIC_KEY, NIGHTSHIFT_PLATFORM_OPENAI_KEY,
//	NIGHTSHIFT_PLATFORM_OPENROUTER_KEY
//	                        platform model credentials, per provider — injected
//	                        by the egress proxy, never visible to the harness
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
	"github.com/gambtho/nightwatch/server/internal/proxy"
	"github.com/gambtho/nightwatch/server/internal/proxyadapter"
	"github.com/gambtho/nightwatch/server/internal/redact"
	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/token"
	"github.com/gambtho/nightwatch/server/internal/vault"
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

	secretVals := make([]string, 0, len(platform))
	for _, v := range platform {
		secretVals = append(secretVals, v)
	}
	slog.SetDefault(slog.New(redact.Handler{
		Inner: slog.Default().Handler(),
		R:     redact.New(secretVals),
	}))

	addr := os.Getenv("NIGHTSHIFT_LISTEN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	stateDir := os.Getenv("NIGHTSHIFT_STATE_DIR")
	if stateDir == "" {
		stateDir = filepath.Join(os.TempDir(), "nightshift-actors")
	}

	baseURL := "http://" + addr
	factory := llm.NewFactory(llm.Config{
		AnthropicBaseURL:  baseURL + "/proxy/llm/anthropic",
		OpenAIBaseURL:     baseURL + "/proxy/llm/openai",
		OpenRouterBaseURL: baseURL + "/proxy/llm/openrouter",
	})
	local := compute.NewLocal(stateDir, func(ctx context.Context, req compute.InvokeRequest, stateDir string) {
		client := harness.NewClient(baseURL, req.RunID, req.RunToken)
		steps, err := client.Context(ctx)
		if err != nil {
			slog.Error("harness: fetch context", "run", req.RunID, "err", err)
			return
		}
		if _, err := harness.Run(ctx,
			harness.Input{Steps: steps, RunToken: req.RunToken},
			harness.Deps{ProviderFactory: factory, Sink: client}); err != nil {
			slog.Error("harness: run failed", "run", req.RunID, "err", err)
		}
	})

	mux := http.NewServeMux()
	httpapi.RegisterRoutes(mux, httpapi.Deps{Store: s, SessionKey: sessionKey, Signer: signer, Compute: local, Vault: master})
	internalapi.RegisterRoutes(mux, internalapi.Deps{Store: s, Signer: signer})

	adapters := proxyadapter.New(s, signer, master, platform)
	cfg := proxy.DefaultConfig()
	cfg.InternalBase = baseURL
	proxy.RegisterRoutes(mux, proxy.Deps{
		Auth: adapters.Auth, Permits: adapters.Permits,
		Credentials: adapters.Credentials, Events: adapters.Events,
		Hook: proxy.NopHook{}, Config: cfg,
	})

	slog.Info("nightshift: serving", "addr", addr)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		// WriteTimeout deliberately zero: streamed LLM responses run for
		// minutes and a server-wide write deadline would sever them.
		// ReadTimeout bounds slow-body clients (LLM prompts are modest);
		// IdleTimeout reclaims idle keep-alive connections.
	}
	return srv.ListenAndServe()
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
		master, err := vault.NewMaster(keyFromEnv("NIGHTSHIFT_VAULT_KEY"))
		if err != nil {
			return err
		}
		wrapped, err := master.NewTenantKEK()
		if err != nil {
			return err
		}
		tn, err = s.CreateTenant(ctx, *tenantName, wrapped)
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
