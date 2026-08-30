// Package compute is the seam between the control plane and whatever hosts
// actors. The platform spec mandates this interface so the pre-1.0
// Substrate dependency stays replaceable; the Substrate and Kubernetes-Jobs
// implementations are Plan 5. Local is the in-process implementation that
// keeps the seam honest from day one.
package compute

import (
	"context"

	"github.com/google/uuid"
)

type ActorID string

type WorkflowRef struct {
	TenantID   uuid.UUID
	WorkflowID uuid.UUID
}

// TemplateRef names the actor template (image + config). Local ignores it;
// Substrate's ActorTemplates are immutable, so workflow-version changes
// will map to new templates (governance primitive #8).
type TemplateRef struct {
	Name string
}

type InvokeRequest struct {
	RunID    uuid.UUID
	RunToken string
}

type Handle struct {
	ActorID ActorID
	RunID   uuid.UUID
}

type Compute interface {
	EnsureActor(ctx context.Context, w WorkflowRef, tmpl TemplateRef) (ActorID, error)
	Invoke(ctx context.Context, a ActorID, payload InvokeRequest) (Handle, error)
	Suspend(ctx context.Context, a ActorID) error
	Destroy(ctx context.Context, a ActorID) error
}
