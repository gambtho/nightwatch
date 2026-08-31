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
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"

	"github.com/gambtho/nightwatch/server/internal/oauth"
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

// New wires the adapter set. oauthSvc may be nil in tests that never
// resolve oauth-kind connections; production passes the real service so
// injection-time refresh works.
func New(s *store.Store, signer *token.Signer, master *vault.Master, platform map[string]string, oauthSvc *oauth.Service) Set {
	return Set{
		Auth:        &auth{store: s, signer: signer},
		Permits:     &permits{store: s},
		Credentials: &credentials{store: s, master: master, platform: platform, oauth: oauthSvc},
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
	oauth    *oauth.Service
	// refreshes collapses concurrent refreshes of one connection into a
	// single in-process flight; the transaction-scoped advisory lock in
	// WithConnectionLock serializes across processes.
	refreshes singleflight.Group
}

// refreshSkew: a token expiring within this window is refreshed before
// use. A longer upstream call can still outlive a token injected just
// outside the skew — the connector 401 retry is the backstop there.
const refreshSkew = 60 * time.Second

func (c *credentials) Credential(ctx context.Context, tenantID uuid.UUID, name, provider string) (proxy.Secret, error) {
	conn, err := c.store.GetConnection(ctx, tenantID, provider, name)
	switch {
	case err == nil:
		value, err := c.resolve(ctx, tenantID, provider, name, conn)
		if err != nil {
			return proxy.Secret{}, err
		}
		go func() {
			touchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if terr := c.store.TouchConnection(touchCtx, tenantID, conn.ID); terr != nil {
				slog.Error("proxyadapter: touch connection", "err", terr)
			}
		}()
		return value, nil
	case errors.Is(err, store.ErrNotFound) && name == "default":
		if key, ok := c.platform[provider]; ok && key != "" {
			return proxy.Secret{Value: key}, nil
		}
		return proxy.Secret{}, fmt.Errorf("proxyadapter: no platform key for %s", provider)
	default:
		return proxy.Secret{}, err
	}
}

// resolve decrypts a connection into an injectable secret, refreshing
// an oauth bundle whose access token is inside the expiry skew.
func (c *credentials) resolve(ctx context.Context, tenantID uuid.UUID, provider, name string, conn store.Connection) (proxy.Secret, error) {
	if conn.Kind != "oauth" {
		value, err := c.decrypt(ctx, tenantID, conn)
		if err != nil {
			return proxy.Secret{}, err
		}
		return proxy.Secret{Value: value}, nil
	}
	if conn.Status != "ok" {
		return proxy.Secret{}, fmt.Errorf("proxyadapter: connection %s/%s needs re-authorization", provider, name)
	}
	raw, err := c.decrypt(ctx, tenantID, conn)
	if err != nil {
		return proxy.Secret{}, err
	}
	var b oauth.Bundle
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		return proxy.Secret{}, fmt.Errorf("proxyadapter: oauth bundle: %w", err)
	}
	if !c.needsRefresh(b) {
		return c.secretFor(tenantID, provider, name, conn, b), nil
	}
	refreshed, err := c.refresh(ctx, tenantID, provider, name)
	if err != nil {
		return proxy.Secret{}, err
	}
	return refreshed, nil
}

func (c *credentials) needsRefresh(b oauth.Bundle) bool {
	return !b.Expiry.IsZero() && time.Until(b.Expiry) < refreshSkew
}

// refresh runs the singleflight + advisory-lock refresh. The expiry
// decision made before the lock is discarded: the holder re-reads the
// connection inside the transaction and re-checks — a winner's fresh
// bundle is used as-is; a row gone or demoted mid-flight fails the tool
// call rather than retrying a possibly-rotated refresh token.
func (c *credentials) refresh(ctx context.Context, tenantID uuid.UUID, provider, name string) (proxy.Secret, error) {
	key := tenantID.String() + "/" + provider + "/" + name
	v, err, _ := c.refreshes.Do(key, func() (any, error) {
		// The flight serves every waiter, so it must not die with the
		// first caller's context; detach it and bound it ourselves.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		var out proxy.Secret
		final, err := c.store.WithConnectionLock(ctx, tenantID, provider, name,
			func(cur store.Connection) (*store.BundleUpdate, error) {
				if cur.Status != "ok" {
					return nil, fmt.Errorf("proxyadapter: connection %s/%s needs re-authorization", provider, name)
				}
				raw, err := c.decrypt(ctx, tenantID, cur)
				if err != nil {
					return nil, err
				}
				var b oauth.Bundle
				if err := json.Unmarshal([]byte(raw), &b); err != nil {
					return nil, fmt.Errorf("proxyadapter: oauth bundle: %w", err)
				}
				if !c.needsRefresh(b) {
					// A concurrent winner already refreshed; use the
					// stored bundle and refresh nothing.
					return nil, nil
				}
				if c.oauth == nil {
					return nil, errors.New("proxyadapter: oauth refresh unavailable")
				}
				nb, err := c.oauth.Refresh(ctx, provider, b)
				if err != nil {
					// Demote via epoch CAS OUTSIDE this transaction? No:
					// the CAS targets the epoch this stale bundle was
					// read under, and we hold the lock, so a direct
					// demotion here cannot race a refresh. Mark and
					// surface the failure as this tool call's error.
					if applied, cerr := c.store.MarkConnectionNeedsReauth(ctx, tenantID, cur.ID, cur.Epoch); cerr != nil {
						slog.Error("proxyadapter: mark needs_reauth", "err", cerr)
					} else if applied {
						slog.Warn("proxyadapter: connection demoted to needs_reauth", "provider", provider, "name", name)
					}
					return nil, fmt.Errorf("proxyadapter: refresh: %w", err)
				}
				bundleJSON, err := json.Marshal(nb)
				if err != nil {
					return nil, err
				}
				wrappedKEK, kekVersion, err := c.store.TenantKEK(ctx, tenantID)
				if err != nil {
					return nil, err
				}
				dek, ct, nonce, err := c.master.EncryptSecret(wrappedKEK, string(bundleJSON))
				if err != nil {
					return nil, err
				}
				meta, err := json.Marshal(oauth.Metadata{Scopes: nb.Scopes})
				if err != nil {
					return nil, err
				}
				return &store.BundleUpdate{
					Kind: "oauth", DEKWrapped: dek, Ciphertext: ct, Nonce: nonce,
					KEKVersion: kekVersion, Metadata: meta,
				}, nil
			})
		if err != nil {
			return proxy.Secret{}, err
		}
		raw, err := c.decrypt(ctx, tenantID, final)
		if err != nil {
			return proxy.Secret{}, err
		}
		var b oauth.Bundle
		if err := json.Unmarshal([]byte(raw), &b); err != nil {
			return proxy.Secret{}, err
		}
		out = c.secretFor(tenantID, provider, name, final, b)
		return out, nil
	})
	if err != nil {
		return proxy.Secret{}, err
	}
	return v.(proxy.Secret), nil
}

// secretFor builds the injectable secret with the 401-demotion hook:
// MarkBroken CAS-demotes on the epoch this token was issued under, so a
// stale 401 cannot demote a connection refreshed in the meantime.
func (c *credentials) secretFor(tenantID uuid.UUID, provider, name string, conn store.Connection, b oauth.Bundle) proxy.Secret {
	epoch := conn.Epoch
	id := conn.ID
	return proxy.Secret{
		Value: b.AccessToken,
		MarkBroken: func(ctx context.Context) (bool, error) {
			return c.store.MarkConnectionNeedsReauth(ctx, tenantID, id, epoch)
		},
	}
}

func (c *credentials) decrypt(ctx context.Context, tenantID uuid.UUID, conn store.Connection) (string, error) {
	// Decrypt with the KEK version that wrapped this connection —
	// rotation-safe even while a rewrap job is mid-flight.
	wrapped, err := c.store.TenantKEKAt(ctx, tenantID, conn.KEKVersion)
	if err != nil {
		return "", err
	}
	return c.master.DecryptSecret(wrapped, conn.DEKWrapped, conn.Ciphertext, conn.Nonce)
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
