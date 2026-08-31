// Command nightshift runs the Nightshift control plane.
//
//	nightshift migrate      apply database migrations and exit
//	nightshift serve        migrate, then serve the public and internal APIs
//	nightshift dev-session  mint a tenant, owner, and session cookie for local use
//
// Configuration (env):
//
//	DATABASE_URL            Postgres DSN (required)
//	NIGHTSHIFT_PUBLIC_BASE_URL
//	                        canonical customer-facing origin, scheme + host
//	                        (required for serve). HTTPS required; http is
//	                        allowed for localhost only. Single source for
//	                        magic-link URLs, the Origin check, and redirect
//	                        joining — Host/proxy headers are never used.
//	NIGHTSHIFT_POSTMARK_TOKEN, NIGHTSHIFT_MAIL_FROM
//	                        Postmark server token and From address, set
//	                        together. Unset is allowed only for a localhost
//	                        base URL (magic links then go to the log);
//	                        setting exactly one refuses startup.
//	NIGHTSHIFT_RUNNER_KEY   base64, 32 bytes (required for serve)
//	NIGHTSHIFT_VAULT_KEY    base64, 32 bytes (required for serve;
//	                        dev-session needs it only when minting a new
//	                        tenant)
//	NIGHTSHIFT_LISTEN_ADDR  default 127.0.0.1:8080
//	NIGHTSHIFT_STATE_DIR    actor state root, default $TMPDIR/nightshift-actors
//	NIGHTSHIFT_PLATFORM_ANTHROPIC_KEY, NIGHTSHIFT_PLATFORM_OPENAI_KEY,
//	NIGHTSHIFT_PLATFORM_OPENROUTER_KEY
//	                        platform model credentials, per provider — injected
//	                        by the egress proxy, never visible to the harness
//	NIGHTSHIFT_RUN_PROVIDER, NIGHTSHIFT_RUN_MODEL
//	                        platform-selected execution model compiled into
//	                        every approved version (decision 9); defaults
//	                        anthropic / claude-haiku-4-5. Must be a priced
//	                        pair or approvals 400.
//	NIGHTSHIFT_RUN_TOKEN_TTL  Go duration, default 1h
//	NIGHTSHIFT_RUN_DEADLINE   Go duration, default 2h; must exceed
//	                          NIGHTSHIFT_RUN_TOKEN_TTL — a run whose token
//	                          expired can never finalize itself, so the
//	                          reaper only sweeps runs past a strictly longer
//	                          deadline
//	NIGHTSHIFT_DEFAULT_MONTHLY_CAP_CENTS
//	                          tenant monthly spend cap in cents, default 0
//	                          (unlimited)
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gambtho/nightwatch/server/internal/catalog"
	"github.com/gambtho/nightwatch/server/internal/compute"
	"github.com/gambtho/nightwatch/server/internal/db"
	"github.com/gambtho/nightwatch/server/internal/engine"
	"github.com/gambtho/nightwatch/server/internal/harness"
	"github.com/gambtho/nightwatch/server/internal/httpapi"
	"github.com/gambtho/nightwatch/server/internal/internalapi"
	"github.com/gambtho/nightwatch/server/internal/llm"
	"github.com/gambtho/nightwatch/server/internal/mail"
	"github.com/gambtho/nightwatch/server/internal/meter"
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
	if err := db.Migrate(ctx, mustEnv("DATABASE_URL")); err != nil {
		return err
	}
	pool, err := db.NewPool(ctx, mustEnv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer pool.Close()

	s := store.New(pool)
	publicBase, err := httpapi.ParsePublicBaseURL(mustEnv("NIGHTSHIFT_PUBLIC_BASE_URL"))
	if err != nil {
		return err
	}
	mailer, err := mail.Select(os.Getenv("NIGHTSHIFT_POSTMARK_TOKEN"), os.Getenv("NIGHTSHIFT_MAIL_FROM"),
		httpapi.IsLocalhost(publicBase))
	if err != nil {
		return err
	}
	if _, isLog := mailer.(mail.LogSender); isLog {
		slog.Warn("mail: no Postmark config; magic links go to the log (localhost dev mode)")
	}
	// The catalog refuses to load on invalid definitions or a
	// reach-widening drift from its committed baseline — serve does not
	// start with an unenforceable catalog.
	cat, err := catalog.Load()
	if err != nil {
		return err
	}
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
	// The inner handler must not be slog.Default().Handler(): that handler
	// writes through the log package, and slog.SetDefault rewires log's
	// output back through the new slog default — the first log call would
	// re-enter this handler and deadlock on log's mutex.
	slog.SetDefault(slog.New(redact.Handler{
		Inner: slog.NewTextHandler(os.Stderr, nil),
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

	tokenTTL := durationFromEnv("NIGHTSHIFT_RUN_TOKEN_TTL", time.Hour)
	runDeadline := durationFromEnv("NIGHTSHIFT_RUN_DEADLINE", 2*time.Hour)
	if err := engine.ValidateRunLifetimes(tokenTTL, runDeadline); err != nil {
		return err
	}
	defaultCap := intFromEnv("NIGHTSHIFT_DEFAULT_MONTHLY_CAP_CENTS", 0)

	eng := &engine.Engine{Store: s, Signer: signer, Compute: local, TokenTTL: tokenTTL}
	m := &meter.Meter{Store: s, DefaultCap: defaultCap}

	mux := http.NewServeMux()
	httpapi.RegisterRoutes(mux, httpapi.Deps{
		Store: s, Engine: eng, Vault: master, PublicBaseURL: publicBase, Mailer: mailer,
		RunProvider: os.Getenv("NIGHTSHIFT_RUN_PROVIDER"),
		RunModel:    os.Getenv("NIGHTSHIFT_RUN_MODEL"),
		Catalog:     cat,
	})
	internalapi.RegisterRoutes(mux, internalapi.Deps{Store: s, Signer: signer})

	adapters := proxyadapter.New(s, signer, master, platform)
	cfg := proxy.DefaultConfig()
	cfg.InternalBase = baseURL
	proxy.RegisterRoutes(mux, proxy.Deps{
		Auth: adapters.Auth, Permits: adapters.Permits,
		Credentials: adapters.Credentials, Events: adapters.Events,
		Hook: m, Config: cfg, Catalog: cat,
	})

	loopCtx, cancelLoops := context.WithCancel(context.Background())
	defer cancelLoops()
	sched := &engine.Scheduler{Engine: eng, Store: s, Caps: m}
	reaper := &engine.Reaper{Store: s, Deadline: runDeadline}
	go sched.Run(loopCtx)
	go reaper.Run(loopCtx)

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
			master, err := vault.NewMaster(keyFromEnv("NIGHTSHIFT_VAULT_KEY"))
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

	value, tokenHash, err := httpapi.NewOpaqueToken()
	if err != nil {
		return err
	}
	if err := s.CreateSession(ctx, tokenHash, tn.ID, user.ID); err != nil {
		return err
	}
	cookie := httpapi.SessionCookie(value)
	fmt.Printf("tenant: %s\nuser:   %s\ncookie: %s=%s\n", tn.ID, user.ID, cookie.Name, cookie.Value)
	return nil
}

func uuidParse(s string) (id uuid.UUID, err error) { return uuid.Parse(s) }
