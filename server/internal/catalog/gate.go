package catalog

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// The narrow-only gate. The spec's stale-permit policy — an edit may only
// narrow or preserve an op's reach; anything wider ships as a new op name
// — is meant to be enforced mechanically. The repo has no CI yet, so the
// full guarantee (a PR-time diff against the merge base) cannot exist;
// what can exist inside this binary is a baseline check: defs/ carries
// the live catalog, baseline/ carries a committed snapshot of it, and
// Load refuses a catalog that widens reach relative to the baseline (or
// that drifts from it at all — every catalog edit must touch the
// baseline, making the diff reviewable). This is tamper-evident, not
// tamper-proof: a widening edit that also updates the baseline boots
// fine and is caught only in review or by a future CI gate running
// Widenings between real revisions.

// Widenings compares two catalogs and returns every reach-widening
// change from old to new. Removal of a connector or op is not widening
// (matching permit entries fail closed as 403s). Where "narrower" is
// hard to compute, equality is required — conservative by design.
func Widenings(old, new *Catalog) []string {
	var out []string
	for _, oldCon := range old.Connectors() {
		newCon, ok := new.Connector(oldCon.ID)
		if !ok {
			continue // removal: fail-closed, not widening
		}
		for oldName, oldOp := range oldCon.ops {
			newOp, ok := newCon.ops[oldName]
			if !ok {
				continue // removal: fail-closed, not widening
			}
			prefix := fmt.Sprintf("%s.%s: ", oldCon.ID, oldName)
			for _, w := range opWidenings(oldOp, newOp) {
				out = append(out, prefix+w)
			}
		}
	}
	slices.Sort(out)
	return out
}

func opWidenings(old, new *Op) []string {
	var out []string
	b0, b1 := old.Binding, new.Binding
	if b1.Method != b0.Method {
		out = append(out, fmt.Sprintf("method changed %s -> %s", b0.Method, b1.Method))
	}
	if b1.Host != b0.Host {
		out = append(out, fmt.Sprintf("host changed %s -> %s", b0.Host, b1.Host))
	}
	if b1.Path != b0.Path {
		out = append(out, fmt.Sprintf("path template changed %s -> %s", b0.Path, b1.Path))
	}
	if new.Effect != old.Effect {
		out = append(out, fmt.Sprintf("effect changed %s -> %s", old.Effect, new.Effect))
	}
	for _, s := range new.Scopes {
		if !slices.Contains(old.Scopes, s) {
			out = append(out, fmt.Sprintf("scope %s added", s))
		}
	}
	oldFields := make([]string, 0, len(old.Constraints))
	for _, c := range old.Constraints {
		oldFields = append(oldFields, c.Field)
	}
	newFields := make([]string, 0, len(new.Constraints))
	for _, c := range new.Constraints {
		newFields = append(newFields, c.Field)
	}
	for _, f := range oldFields {
		if !slices.Contains(newFields, f) {
			out = append(out, fmt.Sprintf("constraint on %q removed", f))
		}
	}
	out = append(out, schemaWidenings(old.schema, new.schema)...)
	return out
}

// schemaWidenings: the new schema must accept a subset of what the old
// accepted. Within the supported subset that is exactly computable:
// no new properties, no required dropped, types unchanged, enums only
// introduced or shrunk.
func schemaWidenings(old, new *Schema) []string {
	var out []string
	for _, p := range new.Properties() {
		if !old.Has(p) {
			out = append(out, fmt.Sprintf("schema property %q added", p))
		}
	}
	for _, p := range old.Properties() {
		if !new.Has(p) {
			continue // property removed: narrowing
		}
		if old.Required(p) && !new.Required(p) {
			out = append(out, fmt.Sprintf("schema property %q no longer required", p))
		}
		if new.Type(p) != old.Type(p) {
			out = append(out, fmt.Sprintf("schema property %q type changed %s -> %s", p, old.Type(p), new.Type(p)))
		}
		oldEnum, newEnum := old.EnumOf(p), new.EnumOf(p)
		if len(oldEnum) > 0 {
			if len(newEnum) == 0 {
				out = append(out, fmt.Sprintf("schema property %q enum removed", p))
			} else {
				for _, v := range newEnum {
					if !slices.Contains(oldEnum, v) {
						out = append(out, fmt.Sprintf("schema property %q enum value %q added", p, v))
					}
				}
			}
		}
	}
	return out
}

// CheckAgainstBaseline enforces the boot-time policy: refuse a widening
// outright, and refuse any other drift until the baseline is updated in
// review.
func CheckAgainstBaseline(baseline, live *Catalog) error {
	if w := Widenings(baseline, live); len(w) > 0 {
		return fmt.Errorf("catalog: reach-widening edits vs baseline (widen by shipping a NEW op name, never by editing an existing one):\n  %s",
			strings.Join(w, "\n  "))
	}
	if !equalDefs(baseline, live) {
		return fmt.Errorf("catalog: defs drifted from baseline; review the diff and run catalog-gate -update-baseline")
	}
	return nil
}

func equalDefs(a, b *Catalog) bool {
	if len(a.connectors) != len(b.connectors) {
		return false
	}
	for id, ca := range a.connectors {
		cb, ok := b.connectors[id]
		if !ok || fingerprint(ca) != fingerprint(cb) {
			return false
		}
	}
	return true
}

func fingerprint(c *Connector) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s|%s|%s|%s|%v|", c.ID, c.Name, c.Description, c.Auth.Provider, c.Hosts)
	for _, op := range c.Ops {
		fmt.Fprintf(&sb, "%s|%s|%s|%v|%s|%+v|%+v|", op.Name, op.Description, op.Effect,
			op.Scopes, canonicalJSON(op.ArgsSchema), op.Binding, op.Constraints)
	}
	return sb.String()
}

func canonicalJSON(raw []byte) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(out)
}
