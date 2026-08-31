package server_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	server "github.com/gambtho/tomte/server"
	"github.com/gambtho/tomte/server/internal/testpg"
)

func startServer(t *testing.T) *server.Server {
	t.Helper()
	pool := testpg.New(t)
	dsn := pool.Config().ConnString()
	pool.Close() // Start opens its own pool over the migrated database

	sv, err := server.Start(context.Background(), server.Options{
		DatabaseURL: dsn,
		ListenAddr:  "127.0.0.1:0", // the shell's per-install random port
		RunnerKey:   bytes.Repeat([]byte{7}, 32),
		VaultKey:    bytes.Repeat([]byte{8}, 32),
		StateDir:    t.TempDir(),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = sv.Shutdown(ctx)
	})
	return sv
}

// The packaging lane's consumption proof: Start binds an ephemeral
// loopback port, derives the origin, serves the API, and enforces the
// Host allowlist over every route.
func TestStartBootsAndServes(t *testing.T) {
	sv := startServer(t)
	require.Equal(t, "http://"+sv.Addr(), sv.BaseURL())

	// Routes are live; no cookie is 401, not 404.
	resp, err := http.Get(sv.BaseURL() + "/v1/me")
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// DNS-rebinding shape: correct IP, foreign Host → 403 before any
	// handler, on the proxy surface too.
	for _, path := range []string{"/v1/me", "/proxy/llm/openai/chat/completions", "/internal/runs/x/context"} {
		req, err := http.NewRequest("GET", sv.BaseURL()+path, nil)
		require.NoError(t, err)
		req.Host = "evil.example"
		resp, err = http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusForbidden, resp.StatusCode, path)
	}
}

func TestMintLocalSessionAndHandoff(t *testing.T) {
	sv := startServer(t)
	ctx := context.Background()

	cookie, err := sv.MintLocalSession(ctx)
	require.NoError(t, err)
	// The loopback origin is plain HTTP: the cookie must be the
	// non-__Host- shape or Safari would refuse it.
	require.Equal(t, "tomte_session", cookie.Name)
	require.False(t, cookie.Secure)

	req, err := http.NewRequest("GET", sv.BaseURL()+"/v1/me", nil)
	require.NoError(t, err)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Open-in-browser: the handoff URL sets a cookie and redirects once.
	handoff, err := sv.HandoffURL(ctx)
	require.NoError(t, err)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err = client.Get(handoff)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	require.Equal(t, sv.BaseURL()+"/build", resp.Header.Get("Location"))
	var minted *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "tomte_session" {
			minted = c
		}
	}
	require.NotNil(t, minted)

	// Single use.
	resp, err = client.Get(handoff)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestShutdownStopsServing(t *testing.T) {
	sv := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, sv.Shutdown(ctx))
	select {
	case err := <-sv.Err():
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("serve loop did not exit after Shutdown")
	}
	_, err := http.Get(sv.BaseURL() + "/v1/me")
	require.Error(t, err, "listener is closed")
}
