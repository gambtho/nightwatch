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

type versionJSON struct {
	Number     int             `json:"number"`
	Status     string          `json:"status"`
	Steps      store.StepsDoc  `json:"steps"`
	Permit     json.RawMessage `json:"permit"`
	Rubric     json.RawMessage `json:"rubric"`
	Schedule   json.RawMessage `json:"schedule,omitempty"`
	ApprovedAt *time.Time      `json:"approved_at,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

type versionDocJSON struct {
	Name     string          `json:"name"`
	Steps    store.StepsDoc  `json:"steps"`
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

func decodeDoc(w http.ResponseWriter, r *http.Request) (versionDocJSON, bool) {
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
	if _, err := permit.Parse(body.Permit); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid permit: " + err.Error()})
		return body, false
	}
	if !llm.Priced(body.Steps.Provider, body.Steps.Model) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "no pricing for " + body.Steps.Provider + "/" + body.Steps.Model + " — spend caps require a priced model",
		})
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
	body, ok := decodeDoc(w, r)
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
	body, ok := decodeDoc(w, r)
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
	// Re-check pricing at approval, not just at write time: a draft persisted
	// before a price-table change could otherwise become approved with a
	// model CostCents no longer knows, silently under-recording spend.
	cur, err := d.Store.GetVersion(r.Context(), claims.TenantID, id, number)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !llm.Priced(cur.Doc.Steps.Provider, cur.Doc.Steps.Model) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "no pricing for " + cur.Doc.Steps.Provider + "/" + cur.Doc.Steps.Model + " — spend caps require a priced model",
		})
		return
	}
	v, err := d.Store.ApproveVersion(r.Context(), claims.TenantID, id, number, claims.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": toVersionJSON(v)})
}
