// Package compute is the seam between the control plane and whatever hosts
// actors: the control plane never depends on a concrete host, so a
// cluster-backed implementation can replace Local without a redesign.
// Local is the in-process implementation that keeps the seam honest.
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

// TemplateRef names the actor template (image + config) for hosts that
// need one. Local ignores it: run behavior comes from the run context the
// harness fetches per run, never from the template.
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
