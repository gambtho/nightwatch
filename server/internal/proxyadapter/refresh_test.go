package proxyadapter_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/tomte/server/internal/oauth"
	"github.com/gambtho/tomte/server/internal/proxyadapter"
	"github.com/gambtho/tomte/server/internal/store"
)

// oauthEnv extends env with a fake token endpoint and an oauth-kind
// "default" connection for provider "fakeoauth".
type oauthEnv struct {
	*env
	set      proxyadapter.Set
	refreshN atomic.Int64
	respond  func(w http.ResponseWriter)
}

func newOAuthEnv(t *testing.T, bundle oauth.Bundle) *oauthEnv {
	t.Helper()
	oe := &oauthEnv{env: newEnv(t)}
	oe.respond = func(w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"access_token":"at-new","refresh_token":"rt-new","expires_in":3600}`))
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oe.refreshN.Add(1)
		oe.respond(w)
	}))
	t.Cleanup(ts.Close)

	svc := &oauth.Service{
		Providers: map[string]oauth.Endpoints{
			"fakeoauth": {AuthURL: "https://a.example.com", TokenURL: ts.URL},
		},
		Clients: func(ctx context.Context, provider string) (oauth.ClientCreds, error) {
			return oauth.ClientCreds{ID: "id", Secret: "sec"}, nil
		},
	}
	oe.set = proxyadapter.New(oe.store, oe.signer, oe.master, nil, svc)
	oe.seedBundle(t, bundle)
	return oe
}

func (oe *oauthEnv) seedBundle(t *testing.T, b oauth.Bundle) store.Connection {
	t.Helper()
	ctx := context.Background()
	raw, err := json.Marshal(b)
	require.NoError(t, err)
	wrapped, kekVersion, err := oe.store.TenantKEK(ctx, oe.tenant.ID)
	require.NoError(t, err)
	dek, ct, nonce, err := oe.master.EncryptSecret(wrapped, string(raw))
	require.NoError(t, err)
	meta, _ := json.Marshal(oauth.Metadata{Scopes: b.Scopes})
	conn, err := oe.store.UpsertConnectionBundle(ctx, oe.tenant.ID, "default", "fakeoauth",
		store.BundleUpdate{Kind: "oauth", DEKWrapped: dek, Ciphertext: ct, Nonce: nonce,
			KEKVersion: kekVersion, Metadata: meta})
	require.NoError(t, err)
	return conn
}

func (oe *oauthEnv) storedBundle(t *testing.T) (oauth.Bundle, store.Connection) {
	t.Helper()
	ctx := context.Background()
	conn, err := oe.store.GetConnection(ctx, oe.tenant.ID, "fakeoauth", "default")
	require.NoError(t, err)
	wrapped, err := oe.store.TenantKEKAt(ctx, oe.tenant.ID, conn.KEKVersion)
	require.NoError(t, err)
	raw, err := oe.master.DecryptSecret(wrapped, conn.DEKWrapped, conn.Ciphertext, conn.Nonce)
	require.NoError(t, err)
	var b oauth.Bundle
	require.NoError(t, json.Unmarshal([]byte(raw), &b))
	return b, conn
}

func TestOAuthFreshTokenNoRefresh(t *testing.T) {
	oe := newOAuthEnv(t, oauth.Bundle{
		AccessToken: "at-fresh", RefreshToken: "rt", Expiry: time.Now().Add(time.Hour),
	})
	sec, err := oe.set.Credentials.Credential(context.Background(), oe.tenant.ID, "default", "fakeoauth")
	require.NoError(t, err)
	require.Equal(t, "at-fresh", sec.Value)
	require.NotNil(t, sec.MarkBroken)
	require.EqualValues(t, 0, oe.refreshN.Load())
}

// The spec's refresh-race row: concurrent runs hitting one expired
// connection produce exactly one upstream refresh, and both proceed
// with the new token, which was persisted before use.
func TestOAuthRefreshRace(t *testing.T) {
	oe := newOAuthEnv(t, oauth.Bundle{
		AccessToken: "at-stale", RefreshToken: "rt", Expiry: time.Now().Add(10 * time.Second),
	})

	const callers = 8
	var wg sync.WaitGroup
	secrets := make([]string, callers)
	errs := make([]error, callers)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sec, err := oe.set.Credentials.Credential(context.Background(), oe.tenant.ID, "default", "fakeoauth")
			secrets[i], errs[i] = sec.Value, err
		}()
	}
	wg.Wait()
	for i := range callers {
		require.NoError(t, errs[i])
		require.Equal(t, "at-new", secrets[i])
	}
	require.EqualValues(t, 1, oe.refreshN.Load(), "one refresh across concurrent callers")

	// Persisted before use: the stored bundle is the refreshed one, and
	// the epoch advanced.
	b, conn := oe.storedBundle(t)
	require.Equal(t, "at-new", b.AccessToken)
	require.Equal(t, "rt-new", b.RefreshToken)
	require.EqualValues(t, 2, conn.Epoch)
	require.Equal(t, "ok", conn.Status)
}

func TestOAuthRefreshFailureMarksNeedsReauth(t *testing.T) {
	oe := newOAuthEnv(t, oauth.Bundle{
		AccessToken: "at-stale", RefreshToken: "rt-dead", Expiry: time.Now().Add(-time.Minute),
	})
	oe.respond = func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}
	_, err := oe.set.Credentials.Credential(context.Background(), oe.tenant.ID, "default", "fakeoauth")
	require.Error(t, err)

	_, conn := oe.storedBundle(t)
	require.Equal(t, "needs_reauth", conn.Status)

	// And a demoted connection fails fast without touching the provider.
	before := oe.refreshN.Load()
	_, err = oe.set.Credentials.Credential(context.Background(), oe.tenant.ID, "default", "fakeoauth")
	require.ErrorContains(t, err, "re-authorization")
	require.Equal(t, before, oe.refreshN.Load())
}

// The stale-401 row: a MarkBroken carrying an epoch that a refresh has
// since superseded misses the CAS and demotes nothing.
func TestOAuthStale401DoesNotDemote(t *testing.T) {
	oe := newOAuthEnv(t, oauth.Bundle{
		AccessToken: "at-1", RefreshToken: "rt", Expiry: time.Now().Add(time.Hour),
	})
	ctx := context.Background()

	stale, err := oe.set.Credentials.Credential(ctx, oe.tenant.ID, "default", "fakeoauth")
	require.NoError(t, err)

	// A re-consent (bundle write) bumps the epoch out from under it.
	oe.seedBundle(t, oauth.Bundle{AccessToken: "at-2", RefreshToken: "rt2", Expiry: time.Now().Add(time.Hour)})

	applied, err := stale.MarkBroken(ctx)
	require.NoError(t, err)
	require.False(t, applied, "stale epoch must miss the CAS")
	_, conn := oe.storedBundle(t)
	require.Equal(t, "ok", conn.Status)

	// A current-epoch 401 does demote.
	current, err := oe.set.Credentials.Credential(ctx, oe.tenant.ID, "default", "fakeoauth")
	require.NoError(t, err)
	applied, err = current.MarkBroken(ctx)
	require.NoError(t, err)
	require.True(t, applied)
	_, conn = oe.storedBundle(t)
	require.Equal(t, "needs_reauth", conn.Status)
}

// The revoke-mid-refresh row, store half: a delete taken while the
// refresh lock is held waits for it, and a writer that lost its row
// gets ErrNotFound instead of persisting into the void.
func TestConnectionLockSerializesDeleteAndRefresh(t *testing.T) {
	oe := newOAuthEnv(t, oauth.Bundle{AccessToken: "at", RefreshToken: "rt"})
	ctx := context.Background()

	holding := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := oe.store.WithConnectionLock(ctx, oe.tenant.ID, "fakeoauth", "default",
			func(cur store.Connection) (*store.BundleUpdate, error) {
				close(holding)
				<-release
				return nil, nil
			})
		done <- err
	}()
	<-holding

	deleted := make(chan error, 1)
	go func() {
		_, err := oe.store.DeleteConnectionLocked(ctx, oe.tenant.ID, "fakeoauth", "default")
		deleted <- err
	}()
	select {
	case err := <-deleted:
		t.Fatalf("delete completed while refresh lock held: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	close(release)
	require.NoError(t, <-done)
	require.NoError(t, <-deleted)

	// The row is gone; a late writer cannot resurrect it.
	_, err := oe.store.WithConnectionLock(ctx, oe.tenant.ID, "fakeoauth", "default",
		func(cur store.Connection) (*store.BundleUpdate, error) { return nil, nil })
	require.ErrorIs(t, err, store.ErrNotFound)
}
