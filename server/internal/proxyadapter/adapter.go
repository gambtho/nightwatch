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

	"github.com/gambtho/tomte/server/internal/endpoint"
	"github.com/gambtho/tomte/server/internal/permit"
	"github.com/gambtho/tomte/server/internal/proxy"
	"github.com/gambtho/tomte/server/internal/store"
	"github.com/gambtho/tomte/server/internal/token"
	"github.com/gambtho/tomte/server/internal/vault"
)

type Set struct {
	Auth        proxy.AuthSource
	Permits     proxy.PermitSource
	Credentials proxy.CredentialSource
	Events      proxy.EventSink
	Endpoints   proxy.EndpointSource
}

// New wires the adapter set.
func New(s *store.Store, signer *token.Signer, master *vault.Master, platform map[string]string) Set {
	return Set{
		Auth:        &auth{store: s, signer: signer},
		Permits:     &permits{store: s},
		Credentials: &credentials{store: s, master: master, platform: platform},
		Events:      &events{store: s},
		Endpoints:   &endpoints{store: s},
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

// resolve decrypts a connection into an injectable secret. All kinds are
// static now (llm_api_key, api_key) — there is nothing to refresh; a
// credential revoked upstream surfaces as a 401, whose MarkBroken hook
// demotes the row to needs_reauth until the user re-pastes.
func (c *credentials) resolve(ctx context.Context, tenantID uuid.UUID, provider, name string, conn store.Connection) (proxy.Secret, error) {
	if conn.Status != "ok" {
		return proxy.Secret{}, fmt.Errorf("proxyadapter: connection %s/%s needs re-authorization", provider, name)
	}
	value, err := c.decrypt(ctx, tenantID, conn)
	if err != nil {
		return proxy.Secret{}, err
	}
	id := conn.ID
	return proxy.Secret{
		Value: value,
		MarkBroken: func(ctx context.Context) (bool, error) {
			return c.store.MarkConnectionNeedsReauth(ctx, tenantID, id)
		},
	}, nil
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

type endpoints struct {
	store *store.Store
}

// EndpointFor maps the stored record to the proxy's endpoint type; no
// configured endpoint (a dev deployment in legacy env mode) is (nil, nil).
func (e *endpoints) EndpointFor(ctx context.Context, tenantID uuid.UUID) (*endpoint.Endpoint, error) {
	le, err := e.store.GetLLMEndpoint(ctx, tenantID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	conn := ""
	if le.ConnectionName != nil {
		conn = *le.ConnectionName
	}
	return &endpoint.Endpoint{
		Preset: le.Preset, Kind: le.Kind, BaseURL: le.BaseURL,
		Connection: conn, RunModel: le.RunModel, ZeroCost: le.ZeroCost,
	}, nil
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
