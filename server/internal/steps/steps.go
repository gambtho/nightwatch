// Package steps defines the user-facing steps document, schema v1, and
// the deterministic compiler that turns an approved version's artifacts
// into the execution form the harness runs. The user-facing document is
// what the public API accepts and returns; the compiled form is
// server-derived at approval time and never leaves the internal run
// context (build-conversation spec, scoping decision 9).
package steps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

type Doc struct {
	V     int    `json:"v"`
	Steps []Step `json:"steps"`
}

type Step struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

const (
	maxSteps   = 20
	maxTextLen = 500
	maxIDLen   = 64
)

// Step ids share the rubric criterion-id charset (grading spec): a slug of
// [a-z0-9] runs joined by single interior hyphens — stable identity across
// edits, same reason.
var slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Parse validates raw as a v1 steps document. Fail closed: anything
// unrecognized is an error — in particular the old execution fields
// (system_prompt, kickoff, provider, model, max_tokens) are unknown
// fields here and are rejected, not ignored.
func Parse(raw []byte) (Doc, error) {
	var d Doc
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return Doc{}, fmt.Errorf("steps: %w", err)
	}
	if err := dec.Decode(new(struct{})); err != io.EOF {
		return Doc{}, fmt.Errorf("steps: trailing data after document")
	}
	if d.V != 1 {
		return Doc{}, fmt.Errorf("steps: unsupported version %d", d.V)
	}
	if len(d.Steps) == 0 {
		return Doc{}, fmt.Errorf("steps: at least one step required")
	}
	if len(d.Steps) > maxSteps {
		return Doc{}, fmt.Errorf("steps: at most %d steps allowed", maxSteps)
	}
	seen := make(map[string]bool, len(d.Steps))
	for i, s := range d.Steps {
		if len(s.ID) > maxIDLen || !slugRe.MatchString(s.ID) {
			return Doc{}, fmt.Errorf("steps: step %d: id must be a slug ([a-z0-9] with interior hyphens, at most %d chars)", i+1, maxIDLen)
		}
		if seen[s.ID] {
			return Doc{}, fmt.Errorf("steps: duplicate step id %q", s.ID)
		}
		seen[s.ID] = true
		if s.Text == "" {
			return Doc{}, fmt.Errorf("steps: step %q: text required", s.ID)
		}
		if utf8.RuneCountInString(s.Text) > maxTextLen {
			return Doc{}, fmt.Errorf("steps: step %q: text exceeds %d chars", s.ID, maxTextLen)
		}
	}
	return d, nil
}

// CompilerV stamps every compiled document so an audit can tell which
// preamble generation produced it. A compiler change affects only future
// approvals; migrated pre-decision-9 rows carry compiler_v 0.
const CompilerV = 1

// Compiled is the execution form served by the internal run context. It
// is a superset of the harness Steps shape (adds compiler_v), so the
// harness contract does not change shape.
type Compiled struct {
	SystemPrompt string `json:"system_prompt"`
	Kickoff      string `json:"kickoff"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	MaxTokens    int    `json:"max_tokens"`
	CompilerV    int    `json:"compiler_v"`
}

// Platform is the platform-selected execution policy: provider and model
// come from NIGHTSHIFT_RUN_PROVIDER/NIGHTSHIFT_RUN_MODEL, max tokens from
// the approved spend cap against that model's pricing.
type Platform struct {
	Provider  string
	Model     string
	MaxTokens int
}

// preamble is the fixed harness role and honesty rules. The escalation
// affordance and goal-mode objective join here when those features exist;
// bumping CompilerV when this text changes is what keeps approved
// versions' behavior from drifting.
const preamble = `You are a workflow agent running an approved, unattended job on the owner's behalf.

Rules:
- Follow the numbered steps below exactly as written; they are the text the owner approved.
- Be honest: report what you actually did and found. Never fabricate results, sources, or success.
- If a step cannot be completed, say so plainly in your output instead of guessing.`

// Compile assembles the execution form from the approved artifacts —
// deterministic template assembly, zero model calls. The user's step text
// is embedded byte-identical to what was approved (auditable by diff);
// the rubric is embedded verbatim in compact JSON form.
func Compile(doc Doc, rubric json.RawMessage, p Platform) Compiled {
	var b strings.Builder
	b.WriteString(preamble)
	b.WriteString("\n\nSteps:\n")
	for i, s := range doc.Steps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, s.Text)
	}
	if rules := compactRubric(rubric); rules != "" {
		b.WriteString("\nYour output is graded against these rules; know the promises you are graded on:\n")
		b.WriteString(rules)
		b.WriteString("\n")
	}
	return Compiled{
		SystemPrompt: b.String(),
		Kickoff:      "Carry out your steps now and report the outcome.",
		Provider:     p.Provider,
		Model:        p.Model,
		MaxTokens:    p.MaxTokens,
		CompilerV:    CompilerV,
	}
}

// compactRubric returns the rubric verbatim in compact form, or "" when
// there is no rubric content (nil, empty, or the default {}).
func compactRubric(rubric json.RawMessage) string {
	trimmed := bytes.TrimSpace(rubric)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte(`{}`)) || bytes.Equal(trimmed, []byte(`null`)) {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, trimmed); err != nil {
		// Rubrics arrive through the API's JSON decode, so they are valid
		// JSON by construction; if compaction still fails, embed as-is
		// rather than drop the promises silently.
		return string(trimmed)
	}
	return buf.String()
}
