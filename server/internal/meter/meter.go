// Package meter enforces spend caps. It implements proxy.Hook, so the
// monthly tenant cap is checked before every model request at the egress
// proxy — the enforcement point the platform spec mandates. Fail closed:
// if spend cannot be read, the request is denied.
package meter

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/gambtho/tomte/server/internal/proxy"
	"github.com/gambtho/tomte/server/internal/store"
)

type Meter struct {
	Store      *store.Store
	DefaultCap int // cents per calendar month (UTC); 0 = unlimited
	Now        func() time.Time
}

func (m *Meter) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func MonthStartUTC(now time.Time) time.Time {
	u := now.UTC()
	return time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// CapCents returns the tenant's monthly cap: its own override when set,
// else the platform default. 0 means unlimited.
func (m *Meter) CapCents(ctx context.Context, tenantID uuid.UUID) (int, error) {
	tn, err := m.Store.GetTenant(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	if tn.MonthlyCapCents != nil {
		return *tn.MonthlyCapCents, nil
	}
	return m.DefaultCap, nil
}

func (m *Meter) OverCap(ctx context.Context, tenantID uuid.UUID) (bool, error) {
	capCents, err := m.CapCents(ctx, tenantID)
	if err != nil {
		return false, err
	}
	if capCents <= 0 {
		return false, nil
	}
	spent, err := m.Store.MonthSpendCents(ctx, tenantID, MonthStartUTC(m.now()))
	if err != nil {
		return false, err
	}
	return spent >= capCents, nil
}

// Before implements proxy.Hook.
func (m *Meter) Before(ctx context.Context, req proxy.HookRequest) error {
	over, err := m.OverCap(ctx, req.Identity.TenantID)
	if err != nil {
		// No spend visibility, no spend. Log it: this denial hits every
		// provider call, and operators must be able to tell "store down"
		// from "cap reached".
		slog.Error("meter: spend check failed, denying request",
			"tenant_id", req.Identity.TenantID, "err", err)
		return proxy.HookError{Status: http.StatusForbidden, Msg: "metering unavailable"}
	}
	if over {
		return proxy.HookError{Status: http.StatusTooManyRequests, Msg: "monthly spend cap reached"}
	}
	return nil
}
