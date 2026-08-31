package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/gambtho/nightwatch/server/internal/llm"
	"github.com/gambtho/nightwatch/server/internal/permit"
	"github.com/gambtho/nightwatch/server/internal/schedule"
	"github.com/gambtho/nightwatch/server/internal/steps"
	"github.com/gambtho/nightwatch/server/internal/store"
)

// maxDocBytes bounds the request body decodeDoc will read, so a client
// cannot exhaust server memory with an oversized payload.
const maxDocBytes = 1 << 20

type workflowJSON struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// versionJSON returns the user-facing steps artifact only; the compiled
// execution form is internal and never serialized here (decision 9).
type versionJSON struct {
	Number     int             `json:"number"`
	Status     string          `json:"status"`
	Steps      json.RawMessage `json:"steps"`
	Permit     json.RawMessage `json:"permit"`
	Rubric     json.RawMessage `json:"rubric"`
	Schedule   json.RawMessage `json:"schedule,omitempty"`
	ApprovedAt *time.Time      `json:"approved_at,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

type versionDocJSON struct {
	Name     string          `json:"name"`
	Steps    json.RawMessage `json:"steps"`
	Permit   json.RawMessage `json:"permit"`
	Rubric   json.RawMessage `json:"rubric"`
	Schedule json.RawMessage `json:"schedule,omitempty"`
}

func toWorkflowJSON(wf store.Workflow) workflowJSON {
	return workflowJSON{ID: wf.ID, Name: wf.Name, CreatedAt: wf.CreatedAt}
}

func toVersionJSON(v store.Version) versionJSON {
	return versionJSON{
		Number: v.Number, Status: v.Status, Steps: v.Doc.Steps,
		Permit: v.Doc.Permit, Rubric: v.Doc.Rubric, Schedule: v.Doc.Schedule,
		ApprovedAt: v.ApprovedAt, CreatedAt: v.CreatedAt,
	}
}

// decodeDoc validates the version document. The parsed permit is
// returned so callers can run the catalog checks that need more than
// structure (validateConnections).
func decodeDoc(d Deps, w http.ResponseWriter, r *http.Request) (versionDocJSON, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxDocBytes)
	var body versionDocJSON
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
			return body, false
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return body, false
	}
	if body.Permit == nil {
		body.Permit = json.RawMessage(permit.Empty)
	}
	p, err := permit.Parse(body.Permit)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid permit: " + err.Error()})
		return body, false
	}
	if err := validateConnections(d.Catalog, p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return body, false
	}
	if body.Steps == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "steps required"})
		return body, false
	}
	if _, err := steps.Parse(body.Steps); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return body, false
	}
	if body.Rubric == nil {
		body.Rubric = json.RawMessage(`{}`)
	}
	if body.Schedule != nil {
		if _, err := schedule.Parse(body.Schedule); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return body, false
		}
	}
	return body, true
}

func (d Deps) createWorkflow(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	body, ok := decodeDoc(d, w, r)
	if !ok {
		return
	}
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	wf, v, err := d.Store.CreateWorkflow(r.Context(), claims.TenantID, body.Name,
		store.VersionDoc{Steps: body.Steps, Permit: body.Permit, Rubric: body.Rubric, Schedule: body.Schedule})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"workflow": toWorkflowJSON(wf), "version": toVersionJSON(v),
	})
}

func (d Deps) listWorkflows(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	wfs, err := d.Store.ListWorkflows(r.Context(), claims.TenantID)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]workflowJSON, 0, len(wfs))
	for _, wf := range wfs {
		out = append(out, toWorkflowJSON(wf))
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": out})
}

func (d Deps) getWorkflow(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	wf, err := d.Store.GetWorkflow(r.Context(), claims.TenantID, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	versions, err := d.Store.ListVersions(r.Context(), claims.TenantID, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	vout := make([]versionJSON, 0, len(versions))
	for _, v := range versions {
		vout = append(vout, toVersionJSON(v))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"workflow": toWorkflowJSON(wf), "versions": vout,
	})
}

func (d Deps) addVersion(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	body, ok := decodeDoc(d, w, r)
	if !ok {
		return
	}
	v, err := d.Store.AddVersion(r.Context(), claims.TenantID, id,
		store.VersionDoc{Steps: body.Steps, Permit: body.Permit, Rubric: body.Rubric, Schedule: body.Schedule})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"version": toVersionJSON(v)})
}

func (d Deps) approveVersion(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFrom(r.Context())
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	number, err := strconv.Atoi(r.PathValue("version"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid version"})
		return
	}
	// Approval is where the execution form is fixed: the platform-selected
	// model must be priced here (the unpriced-model 400 moved from
	// create/add when the model left the user's hands), and the compiled
	// document is written in the same transaction as the status change.
	cur, err := d.Store.GetVersion(r.Context(), claims.TenantID, id, number)
	if err != nil {
		writeErr(w, err)
		return
	}
	doc, err := steps.Parse(cur.Doc.Steps)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	provider, model := d.runModel()
	if !llm.Priced(provider, model) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "no pricing for platform run model " + provider + "/" + model + " — spend caps require a priced model",
		})
		return
	}
	// Fail closed on a corrupt permit: silently treating it as "no spend
	// cap" would compile a larger max_tokens than the owner approved.
	p, err := permit.Parse(cur.Doc.Permit)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid permit: " + err.Error()})
		return
	}
	// The platform run model must be inside the approved blast radius:
	// without this a permit omitting the platform provider approves
	// cleanly and then has every run denied at the proxy — the failure
	// belongs here, at approval, not at run time.
	if !p.AllowsProvider(provider) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "permit does not allow platform run provider " + provider + " — llm.providers must include it",
		})
		return
	}
	perRunCents := 0
	if p.Spend != nil {
		perRunCents = p.Spend.PerRunCents
	}
	compiled, err := json.Marshal(steps.Compile(doc, cur.Doc.Rubric, steps.Platform{
		Provider:  provider,
		Model:     model,
		MaxTokens: llm.MaxTokensForBudget(provider, model, perRunCents),
	}))
	if err != nil {
		writeErr(w, err)
		return
	}
	v, err := d.Store.ApproveVersion(r.Context(), claims.TenantID, id, number, claims.UserID, compiled)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": toVersionJSON(v)})
}
