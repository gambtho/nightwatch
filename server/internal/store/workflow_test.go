package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/nightwatch/server/internal/store"
	"github.com/gambtho/nightwatch/server/internal/testpg"
)

func testDoc() store.VersionDoc {
	return store.VersionDoc{
		Steps:    testStepsDoc,
		Permit:   json.RawMessage(`{"v":1,"llm":{"providers":["anthropic"]},"connections":{}}`),
		Rubric:   json.RawMessage(`{"rules":["never miss a security issue"]}`),
		Schedule: json.RawMessage(`{"cron":"0 9 * * MON","tz":"UTC"}`),
	}
}

func TestWorkflowVersionLifecycle(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tn, err := s.CreateTenant(ctx, "acme", testKEK)
	require.NoError(t, err)
	user, err := s.UpsertUser(ctx, tn.ID, "pat@acme.test")
	require.NoError(t, err)

	wf, v1, err := s.CreateWorkflow(ctx, tn.ID, "weekly digest", testDoc())
	require.NoError(t, err)
	require.Equal(t, 1, v1.Number)
	require.Equal(t, "draft", v1.Status)
	require.Nil(t, v1.Compiled, "drafts have no compiled document")
	require.JSONEq(t, string(testStepsDoc), string(v1.Doc.Steps))

	// No approved version yet.
	_, err = s.GetApprovedVersion(ctx, tn.ID, wf.ID)
	require.ErrorIs(t, err, store.ErrNotFound)

	// Approve v1.
	av, err := s.ApproveVersion(ctx, tn.ID, wf.ID, 1, user.ID, testCompiledDoc)
	require.NoError(t, err)
	require.Equal(t, "approved", av.Status)
	require.NotNil(t, av.ApprovedAt)
	require.JSONEq(t, string(testCompiledDoc), string(av.Compiled))

	// A new draft version; approving it supersedes v1.
	v2, err := s.AddVersion(ctx, tn.ID, wf.ID, testDoc())
	require.NoError(t, err)
	require.Equal(t, 2, v2.Number)
	_, err = s.ApproveVersion(ctx, tn.ID, wf.ID, 2, user.ID, testCompiledDoc)
	require.NoError(t, err)

	got, err := s.GetApprovedVersion(ctx, tn.ID, wf.ID)
	require.NoError(t, err)
	require.Equal(t, 2, got.Number)
	require.JSONEq(t, `{"cron":"0 9 * * MON","tz":"UTC"}`, string(got.Doc.Schedule))

	old, err := s.GetVersion(ctx, tn.ID, wf.ID, 1)
	require.NoError(t, err)
	require.Equal(t, "superseded", old.Status)

	// Approving an already-superseded version fails: only drafts approve.
	_, err = s.ApproveVersion(ctx, tn.ID, wf.ID, 1, user.ID, testCompiledDoc)
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestWorkflowCrossTenantIsolation(t *testing.T) {
	s := store.New(testpg.New(t))
	ctx := context.Background()
	tnA, err := s.CreateTenant(ctx, "a", testKEK)
	require.NoError(t, err)
	tnB, err := s.CreateTenant(ctx, "b", testKEK)
	require.NoError(t, err)

	wf, _, err := s.CreateWorkflow(ctx, tnA.ID, "a's workflow", testDoc())
	require.NoError(t, err)

	// Tenant B sees nothing of tenant A's workflow, by any path.
	_, err = s.GetWorkflow(ctx, tnB.ID, wf.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = s.AddVersion(ctx, tnB.ID, wf.ID, testDoc())
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = s.ApproveVersion(ctx, tnB.ID, wf.ID, 1, uuid.New(), testCompiledDoc)
	require.ErrorIs(t, err, store.ErrNotFound)
	list, err := s.ListWorkflows(ctx, tnB.ID)
	require.NoError(t, err)
	require.Empty(t, list)
}
