// Package server exposes the Tomte control plane as a library: the
// packaging shell embeds it (Start → run → Shutdown) instead of spawning
// a subprocess with env-var plumbing. cmd/tomte's serve command is the
// thin env-to-Options translation over this entry point.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/gambtho/tomte/server/internal/captureverify"
	"github.com/gambtho/tomte/server/internal/catalog"
	"github.com/gambtho/tomte/server/internal/compute"
	"github.com/gambtho/tomte/server/internal/db"
	"github.com/gambtho/tomte/server/internal/engine"
	"github.com/gambtho/tomte/server/internal/harness"
	"github.com/gambtho/tomte/server/internal/httpapi"
	"github.com/gambtho/tomte/server/internal/internalapi"
	"github.com/gambtho/tomte/server/internal/llm"
	"github.com/gambtho/tomte/server/internal/llmverify"
	"github.com/gambtho/tomte/server/internal/meter"
	"github.com/gambtho/tomte/server/internal/proxy"
	"github.com/gambtho/tomte/server/internal/proxyadapter"
	"github.com/gambtho/tomte/server/internal/redact"
	"github.com/gambtho/tomte/server/internal/store"
	"github.com/gambtho/tomte/server/internal/token"
	"github.com/gambtho/tomte/server/internal/vault"
)

// Options configures Start. Zero values take the documented defaults;
// only DatabaseURL, RunnerKey, and VaultKey are required.
type Options struct {
	DatabaseURL string
	// ListenAddr defaults to "127.0.0.1:8080"; "127.0.0.1:0" binds an
	// ephemeral port (the shell's per-install random port).
	ListenAddr string
	// PublicBaseURL optionally overrides the app origin (dev topologies
	// like Vite-as-origin). Default: derived from the bound listener —
	// the auto-configured loopback origin.
	PublicBaseURL string
	RunnerKey     []byte // 32 bytes
	VaultKey      []byte // 32 bytes
	StateDir      string // actor state root; default $TMPDIR/tomte-actors
	// RunProvider/RunModel are the legacy env-mode execution pair, used
	// only while no endpoint record is configured.
	RunProvider, RunModel  string
	RunTokenTTL            time.Duration // default 1h
	RunDeadline            time.Duration // default 2h; must exceed RunTokenTTL
	DefaultMonthlyCapCents int
	// PlatformKeys is the dev/headless per-provider key fallback for the
	// "default" connection; user-pasted connections are the real path.
	PlatformKeys map[string]string
	// LogHandler is the slog sink; nil means text to stderr. It is always
	// wrapped in the redact handler (a security control, not a
	// convenience) and installed as the process default.
	LogHandler slog.Handler
}

// Server is a running Tomte control plane.
type Server struct {
	pool        interface{ Close() }
	store       *store.Store
	vault       *vault.Master
	public      *url.URL
	ln          net.Listener
	httpServer  *http.Server
	cancelLoops context.CancelFunc
	errCh       chan error
}

// Start migrates the database, binds the listener, wires the whole stack
// (API, proxy, scheduler, reaper, local compute), and serves in the
// background. Background loops stop when ctx ends or Shutdown is called.
func Start(ctx context.Context, o Options) (*Server, error) {
	if o.DatabaseURL == "" {
		return nil, errors.New("server: DatabaseURL required")
	}
	if o.ListenAddr == "" {
		o.ListenAddr = "127.0.0.1:8080"
	}
	if o.StateDir == "" {
		o.StateDir = filepath.Join(os.TempDir(), "tomte-actors")
	}
	if o.RunTokenTTL == 0 {
		o.RunTokenTTL = time.Hour
	}
	if o.RunDeadline == 0 {
		o.RunDeadline = 2 * time.Hour
	}
	if err := engine.ValidateRunLifetimes(o.RunTokenTTL, o.RunDeadline); err != nil {
		return nil, err
	}

	if err := db.Migrate(ctx, o.DatabaseURL); err != nil {
		return nil, err
	}
	pool, err := db.NewPool(ctx, o.DatabaseURL)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			pool.Close()
		}
	}()

	s := store.New(pool)
	// The catalog refuses to load on invalid definitions or any drift
	// from its committed baseline (reach-widening or otherwise) — the
	// server does not start with an unenforceable or unreviewed catalog.
	cat, err := catalog.Load()
	if err != nil {
		return nil, err
	}
	signer := token.New(o.RunnerKey)
	master, err := vault.NewMaster(o.VaultKey)
	if err != nil {
		return nil, err
	}

	secretVals := make([]string, 0, len(o.PlatformKeys))
	for _, v := range o.PlatformKeys {
		secretVals = append(secretVals, v)
	}
	inner := o.LogHandler
	if inner == nil {
		inner = slog.NewTextHandler(os.Stderr, nil)
	}
	// The inner handler must never be slog.Default().Handler(): that
	// handler writes through the log package, and slog.SetDefault rewires
	// log's output back through the new slog default — the first log call
	// would re-enter and deadlock on log's mutex.
	slog.SetDefault(slog.New(redact.Handler{Inner: inner, R: redact.New(secretVals)}))

	// Bind BEFORE deriving any origin: with ":0" the real port exists
	// only after Listen, and the loopback origin is derived, not assumed.
	ln, err := net.Listen("tcp", o.ListenAddr)
	if err != nil {
		return nil, err
	}
	defer func() {
		if !ok {
			_ = ln.Close()
		}
	}()
	baseURL := "http://" + ln.Addr().String()
	publicRaw := o.PublicBaseURL
	if publicRaw == "" {
		publicRaw = baseURL // the auto-configured loopback origin
	}
	public, err := httpapi.ParsePublicBaseURL(publicRaw)
	if err != nil {
		return nil, err
	}

	factory := llm.NewProxyFactory(baseURL)
	local := compute.NewLocal(o.StateDir, func(ctx context.Context, req compute.InvokeRequest, stateDir string) {
		client := harness.NewClient(baseURL, req.RunID, req.RunToken)
		steps, tools, err := client.Context(ctx)
		if err != nil {
			slog.Error("harness: fetch context", "run", req.RunID, "err", err)
			return
		}
		if _, err := harness.Run(ctx,
			harness.Input{Steps: steps, Tools: tools, RunToken: req.RunToken},
			harness.Deps{ProviderFactory: factory, Sink: client, Tools: client}); err != nil {
			slog.Error("harness: run failed", "run", req.RunID, "err", err)
		}
	})

	eng := &engine.Engine{Store: s, Signer: signer, Compute: local, TokenTTL: o.RunTokenTTL}
	m := &meter.Meter{Store: s, DefaultCap: o.DefaultMonthlyCapCents}

	mux := http.NewServeMux()
	httpapi.RegisterRoutes(mux, httpapi.Deps{
		Store: s, Engine: eng, Vault: master, PublicBaseURL: public,
		RunProvider: o.RunProvider, RunModel: o.RunModel, Catalog: cat,
		CaptureVerify: &captureverify.Client{},
		LLMVerify:     &llmverify.Client{},
		Meter:         m,
	})
	internalapi.RegisterRoutes(mux, internalapi.Deps{Store: s, Signer: signer, Catalog: cat})

	adapters := proxyadapter.New(s, signer, master, o.PlatformKeys)
	cfg := proxy.DefaultConfig()
	cfg.InternalBase = baseURL
	proxy.RegisterRoutes(mux, proxy.Deps{
		Auth: adapters.Auth, Permits: adapters.Permits,
		Credentials: adapters.Credentials, Events: adapters.Events,
		Endpoints: adapters.Endpoints,
		Hook:      m, Config: cfg, Catalog: cat,
	})

	// Host allowlist over the WHOLE mux ("Loopback security posture"):
	// DNS-rebinding requests arrive with the attacker's Host and die
	// here, covering /v1, /proxy/*, and /internal/* alike — checkOrigin
	// only wraps mutating /v1 routes.
	allowed := map[string]bool{ln.Addr().String(): true, public.Host: true}
	if public.Scheme == "http" && public.Port() == "" {
		allowed[public.Host+":80"] = true
	}
	handler := hostAllowlist(allowed, mux)

	loopCtx, cancelLoops := context.WithCancel(ctx)
	sched := &engine.Scheduler{Engine: eng, Store: s, Caps: m}
	reaper := &engine.Reaper{Store: s, Deadline: o.RunDeadline}
	go sched.Run(loopCtx)
	go reaper.Run(loopCtx)

	slog.Info("tomte: serving", "addr", ln.Addr().String(), "origin", public.String())
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		// WriteTimeout deliberately zero: streamed LLM responses run for
		// minutes and a server-wide write deadline would sever them.
	}
	sv := &Server{
		pool: pool, store: s, vault: master, public: public,
		ln: ln, httpServer: srv, cancelLoops: cancelLoops,
		errCh: make(chan error, 1),
	}
	go func() {
		err := srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		sv.errCh <- err
	}()
	ok = true
	return sv, nil
}

// hostAllowlist rejects requests whose Host is not the bound address (or
// the configured public origin's host) — the DNS-rebinding hardening.
func hostAllowlist(allowed map[string]bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.Host] {
			// Loud on purpose: this is either DNS rebinding or a
			// misconfigured origin (localhost vs 127.0.0.1) — both worth
			// an operator-visible line, and indistinguishable from a dead
			// server otherwise.
			slog.Warn("server: request rejected by host allowlist", "host", r.Host, "path", r.URL.Path)
			http.Error(w, "unknown host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Addr is the bound listen address (the real port, even for ":0").
func (s *Server) Addr() string { return s.ln.Addr().String() }

// BaseURL is the app's origin — the auto-configured loopback origin
// unless overridden by Options.PublicBaseURL.
func (s *Server) BaseURL() string { return s.public.String() }

// MintLocalSession resolves (or first-run-mints) the local tenant and
// owner and inserts a session row, returning the cookie the shell injects
// into its webview — at launch and on every window (re)open.
func (s *Server) MintLocalSession(ctx context.Context) (*http.Cookie, error) {
	tenantID, userID, err := httpapi.EnsureLocalOwner(ctx, s.store, s.vault)
	if err != nil {
		return nil, err
	}
	return httpapi.MintLocalSession(ctx, s.store, s.public, tenantID, userID)
}

// HandoffURL mints a single-use, short-TTL handoff token and returns the
// /local/handoff URL behind the tray's "open in browser".
func (s *Server) HandoffURL(ctx context.Context) (string, error) {
	tenantID, userID, err := httpapi.EnsureLocalOwner(ctx, s.store, s.vault)
	if err != nil {
		return "", err
	}
	tok, err := httpapi.NewHandoffToken(ctx, s.store, tenantID, userID)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/local/handoff?token=%s", s.BaseURL(), url.QueryEscape(tok)), nil
}

// Err yields the serve loop's exit (nil after a clean Shutdown).
func (s *Server) Err() <-chan error { return s.errCh }

// Shutdown stops the background loops, drains the HTTP server, and closes
// the pool. A serve-loop failure that happened before Shutdown (a dead
// listener) is joined into the result rather than left unread in Err.
func (s *Server) Shutdown(ctx context.Context) error {
	s.cancelLoops()
	err := s.httpServer.Shutdown(ctx)
	select {
	case serveErr := <-s.errCh:
		err = errors.Join(err, serveErr)
		s.errCh <- nil // keep Err() non-blocking for callers that also wait on it
	default:
	}
	s.pool.Close()
	return err
}
