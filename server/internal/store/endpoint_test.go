package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gambtho/tomte/server/internal/store"
	"github.com/gambtho/tomte/server/internal/testpg"
)

func strPtr(s string) *string { return &s }

func TestLLMEndpointRoundtripAndUpsert(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "local", testKEK)
	require.NoError(t, err)

	_, err = s.GetLLMEndpoint(ctx, tn.ID)
	require.ErrorIs(t, err, store.ErrNotFound)

	e := store.LLMEndpoint{
		Preset: "anthropic", Kind: "anthropic",
		BaseURL: "https://api.anthropic.com", ConnectionName: strPtr("default"),
		RunModel: "claude-haiku-4-5",
	}
	require.NoError(t, s.PutLLMEndpoint(ctx, tn.ID, e))
	got, err := s.GetLLMEndpoint(ctx, tn.ID)
	require.NoError(t, err)
	require.Equal(t, e, *got)

	// Upsert replaces: one endpoint per tenant.
	e2 := store.LLMEndpoint{
		Preset: "local", Kind: "openai_compatible",
		BaseURL: "http://127.0.0.1:11434/v1", RunModel: "llama3", ZeroCost: true,
	}
	require.NoError(t, s.PutLLMEndpoint(ctx, tn.ID, e2))
	got, err = s.GetLLMEndpoint(ctx, tn.ID)
	require.NoError(t, err)
	require.Equal(t, e2, *got)
}

func TestLLMEndpointChecks(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "local", testKEK)
	require.NoError(t, err)

	// local may not carry a connection.
	err = s.PutLLMEndpoint(ctx, tn.ID, store.LLMEndpoint{
		Preset: "local", Kind: "openai_compatible",
		BaseURL: "http://127.0.0.1:11434/v1", ConnectionName: strPtr("default"),
		RunModel: "llama3", ZeroCost: true,
	})
	require.Error(t, err)

	// local must be zero-cost.
	err = s.PutLLMEndpoint(ctx, tn.ID, store.LLMEndpoint{
		Preset: "local", Kind: "openai_compatible",
		BaseURL: "http://127.0.0.1:11434/v1", RunModel: "llama3",
	})
	require.Error(t, err)

	// zero_cost is only for local/github.
	err = s.PutLLMEndpoint(ctx, tn.ID, store.LLMEndpoint{
		Preset: "openai", Kind: "openai_compatible",
		BaseURL: "https://api.openai.com/v1", ConnectionName: strPtr("default"),
		RunModel: "gpt-4o-mini", ZeroCost: true,
	})
	require.Error(t, err)
}

func TestModelPricesAndTenantIsolation(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tnA, err := s.CreateTenant(ctx, "a", testKEK)
	require.NoError(t, err)
	tnB, err := s.CreateTenant(ctx, "b", testKEK)
	require.NoError(t, err)

	base := "https://myco.services.ai.azure.com/openai/v1"
	require.NoError(t, s.UpsertModelPrice(ctx, tnA.ID, base, "gpt-4o", 250, 1000))
	in, out, err := s.GetModelPrice(ctx, tnA.ID, base, "gpt-4o")
	require.NoError(t, err)
	require.Equal(t, 250, in)
	require.Equal(t, 1000, out)

	// Upsert overwrites.
	require.NoError(t, s.UpsertModelPrice(ctx, tnA.ID, base, "gpt-4o", 300, 1200))
	in, out, err = s.GetModelPrice(ctx, tnA.ID, base, "gpt-4o")
	require.NoError(t, err)
	require.Equal(t, 300, in)
	require.Equal(t, 1200, out)

	// Keyed by base URL: another endpoint never inherits.
	_, _, err = s.GetModelPrice(ctx, tnA.ID, "https://other.example/v1", "gpt-4o")
	require.ErrorIs(t, err, store.ErrNotFound)

	// Tenant isolation.
	_, _, err = s.GetModelPrice(ctx, tnB.ID, base, "gpt-4o")
	require.ErrorIs(t, err, store.ErrNotFound)

	list, err := s.ListModelPrices(ctx, tnA.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, store.ModelPrice{BaseURL: base, Model: "gpt-4o", InputCentsPer1M: 300, OutputCentsPer1M: 1200}, list[0])
}

func TestAppendTenantEvent(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "local", testKEK)
	require.NoError(t, err)

	require.NoError(t, s.AppendTenantEvent(ctx, tn.ID, "endpoint.switched",
		json.RawMessage(`{"to":"https://api.openai.com/v1"}`)))
	evs, err := s.ListTenantEvents(ctx, tn.ID)
	require.NoError(t, err)
	require.Len(t, evs, 1)
	require.Equal(t, "endpoint.switched", evs[0].Type)
	require.JSONEq(t, `{"to":"https://api.openai.com/v1"}`, string(evs[0].Payload))
}

func TestSwitchLLMEndpointAtomic(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "local", testKEK)
	require.NoError(t, err)
	u, err := s.UpsertUser(ctx, tn.ID, "owner@tomte.local")
	require.NoError(t, err)

	doc := store.VersionDoc{
		Steps:  json.RawMessage(`{"v":1,"steps":[{"id":"s1","text":"do"}]}`),
		Permit: json.RawMessage(`{}`),
		Rubric: json.RawMessage(`{}`),
	}
	wf, v, err := s.CreateWorkflow(ctx, tn.ID, "wf", doc)
	require.NoError(t, err)
	_, err = s.ApproveVersion(ctx, tn.ID, wf.ID, v.Number, u.ID,
		json.RawMessage(`{"provider":"anthropic","model":"m"}`))
	require.NoError(t, err)

	e := store.LLMEndpoint{
		Preset: "openai", Kind: "openai_compatible",
		BaseURL: "https://api.openai.com/v1", ConnectionName: strPtr("default"),
		RunModel: "gpt-4o-mini",
	}
	newCompiled := json.RawMessage(`{"provider":"openai","model":"gpt-4o-mini"}`)
	require.NoError(t, s.SwitchLLMEndpoint(ctx, tn.ID, e,
		[]store.CompiledUpdate{{WorkflowID: wf.ID, Version: v.Number, Compiled: newCompiled}},
		json.RawMessage(`{"to":"https://api.openai.com/v1"}`)))

	got, err := s.GetLLMEndpoint(ctx, tn.ID)
	require.NoError(t, err)
	require.Equal(t, e, *got)
	av, err := s.GetApprovedVersion(ctx, tn.ID, wf.ID)
	require.NoError(t, err)
	require.JSONEq(t, string(newCompiled), string(av.Compiled))
	evs, err := s.ListTenantEvents(ctx, tn.ID)
	require.NoError(t, err)
	require.Len(t, evs, 1)

	// A bad recompilation target rolls the whole switch back.
	bad := store.LLMEndpoint{
		Preset: "local", Kind: "openai_compatible",
		BaseURL: "http://127.0.0.1:11434/v1", RunModel: "llama3", ZeroCost: true,
	}
	err = s.SwitchLLMEndpoint(ctx, tn.ID, bad,
		[]store.CompiledUpdate{{WorkflowID: wf.ID, Version: 99, Compiled: newCompiled}},
		json.RawMessage(`{}`))
	require.ErrorIs(t, err, store.ErrNotFound)
	got, err = s.GetLLMEndpoint(ctx, tn.ID)
	require.NoError(t, err)
	require.Equal(t, e, *got) // unchanged
	evs, err = s.ListTenantEvents(ctx, tn.ID)
	require.NoError(t, err)
	require.Len(t, evs, 1) // no second event
}

func TestListApprovedVersions(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "local", testKEK)
	require.NoError(t, err)
	u, err := s.UpsertUser(ctx, tn.ID, "owner@tomte.local")
	require.NoError(t, err)

	doc := store.VersionDoc{
		Steps:  json.RawMessage(`{"v":1,"steps":[{"id":"s1","text":"do"}]}`),
		Permit: json.RawMessage(`{}`),
		Rubric: json.RawMessage(`{}`),
	}
	wf1, v1, err := s.CreateWorkflow(ctx, tn.ID, "wf1", doc)
	require.NoError(t, err)
	_, err = s.ApproveVersion(ctx, tn.ID, wf1.ID, v1.Number, u.ID, json.RawMessage(`{"provider":"a","model":"m"}`))
	require.NoError(t, err)
	_, _, err = s.CreateWorkflow(ctx, tn.ID, "wf2-draft-only", doc)
	require.NoError(t, err)

	vs, err := s.ListApprovedVersions(ctx, tn.ID)
	require.NoError(t, err)
	require.Len(t, vs, 1)
	require.Equal(t, wf1.ID, vs[0].WorkflowID)
	require.Equal(t, "approved", vs[0].Status)
}
