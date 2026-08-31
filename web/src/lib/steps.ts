// The user-facing steps artifact — build-conversation spec, scoping
// decision 9: {v: 1, steps: [{id, text}]}. The server migration for
// pre-decision-9 rows synthesizes a single step from the old compiled
// document's kickoff; we mirror that fallback so legacy versions render
// honestly until the migration lands, instead of showing nothing.

export interface Step {
  id: string;
  text: string;
}

export function parseSteps(doc: unknown): Step[] {
  if (typeof doc !== "object" || doc === null || Array.isArray(doc)) return [];
  const record = doc as Record<string, unknown>;

  if (record.v === 1 && Array.isArray(record.steps)) {
    const steps: Step[] = [];
    for (const raw of record.steps) {
      if (typeof raw !== "object" || raw === null) continue;
      const { id, text } = raw as Record<string, unknown>;
      if (typeof id === "string" && typeof text === "string" && text !== "") {
        steps.push({ id, text });
      }
    }
    return steps;
  }

  // Legacy compiled form: mirror the server's migration synthesis.
  if (typeof record.kickoff === "string" && record.kickoff !== "") {
    return [{ id: "job", text: record.kickoff }];
  }
  return [];
}
