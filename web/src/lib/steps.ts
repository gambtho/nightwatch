// The user-facing steps artifact — build-conversation spec, scoping
// decision 9: {v: 1, steps: [{id, text}]}. The server migration for
// pre-decision-9 rows synthesizes a single step from the old compiled
// document's kickoff; we mirror that fallback so legacy versions render
// honestly until the migration lands, instead of showing nothing.

export interface Step {
  id: string;
  text: string;
}

export interface StepsView {
  steps: Step[];
  /** False when the document was neither decision-9 nor legacy shaped —
   * callers must say the steps are unreadable, not render an empty list. */
  recognized: boolean;
}

export function parseSteps(doc: unknown): StepsView {
  if (typeof doc !== "object" || doc === null || Array.isArray(doc)) {
    return { steps: [], recognized: false };
  }
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
    return { steps, recognized: true };
  }

  // Legacy compiled form: mirror the server's migration synthesis.
  if (typeof record.kickoff === "string" && record.kickoff !== "") {
    return { steps: [{ id: "job", text: record.kickoff }], recognized: true };
  }
  return { steps: [], recognized: false };
}
