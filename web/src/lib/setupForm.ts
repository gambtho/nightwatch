import type { CatalogConnector, CreateWorkflowBody, StepsDoc } from "../api/types";
import { parseCron } from "./cron";
import { buildPermitDoc, validatePermitBuild } from "./permitBuild";
import type { PermitBuild } from "./permitBuild";

// The developer-setup form's model and its client-side validation,
// mirroring the documented rules in docs/api/v1.md so the user sees the
// error before the server 400s. The server stays the authority — anything
// it rejects anyway is surfaced verbatim by the screen.

export const STEP_TEXT_MAX = 500;
export const STEPS_MAX = 20;
export const STEP_ID_MAX = 64;

// The steps/rubric id charset: [a-z0-9] runs joined by single interior hyphens.
const SLUG_RE = /^[a-z0-9]+(-[a-z0-9]+)*$/;

export interface StepDraft {
  /** Explicit id; blank means "derive one from the text". */
  id: string;
  text: string;
}

export interface SetupDraft {
  name: string;
  steps: StepDraft[];
  scheduleEnabled: boolean;
  cron: string;
  tz: string;
  permit: PermitBuild;
}

/** Best-effort slug from step text; "" when nothing usable survives. */
export function slugify(text: string): string {
  const slug = text
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, STEP_ID_MAX)
    .replace(/-+$/, "");
  return slug;
}

/**
 * The ids the document will actually carry: explicit ids verbatim,
 * blank ids derived from the text and de-duplicated with -2, -3, …
 * (explicit ids are never rewritten — a collision there is the user's
 * to see and fix).
 */
export function resolveStepIds(steps: StepDraft[]): string[] {
  const taken = new Set(steps.map((s) => s.id.trim()).filter((id) => id !== ""));
  return steps.map((step) => {
    const explicit = step.id.trim();
    if (explicit !== "") return explicit;
    const base = slugify(step.text) || "step";
    let candidate = base;
    for (let n = 2; taken.has(candidate); n++) {
      const suffix = `-${n}`;
      candidate = base.slice(0, STEP_ID_MAX - suffix.length).replace(/-+$/, "") + suffix;
    }
    taken.add(candidate);
    return candidate;
  });
}

export function validTimeZone(tz: string): boolean {
  // The server rejects "Local" explicitly; Intl accepts only real IANA names.
  if (tz === "" || tz === "Local") return false;
  try {
    new Intl.DateTimeFormat("en-US", { timeZone: tz });
    return true;
  } catch {
    return false;
  }
}

export function validateDraft(
  draft: SetupDraft,
  catalog: CatalogConnector[] | null,
): string[] {
  const errors: string[] = [];

  if (draft.name.trim() === "") {
    errors.push("Give the workflow a name.");
  }

  if (draft.steps.length < 1) {
    errors.push("Add at least one step.");
  }
  if (draft.steps.length > STEPS_MAX) {
    errors.push(`At most ${STEPS_MAX} steps.`);
  }

  const ids = resolveStepIds(draft.steps);
  const seen = new Set<string>();
  draft.steps.forEach((step, i) => {
    const n = i + 1;
    const text = step.text.trim();
    if (text === "") {
      errors.push(`Step ${n} needs text.`);
    } else if (text.length > STEP_TEXT_MAX) {
      errors.push(`Step ${n} is over ${STEP_TEXT_MAX} characters.`);
    }
    const id = ids[i]!;
    if (id.length > STEP_ID_MAX || !SLUG_RE.test(id)) {
      errors.push(
        `Step ${n}'s id "${id}" must be lowercase letters, digits, and single hyphens (max ${STEP_ID_MAX} chars).`,
      );
    } else if (seen.has(id)) {
      errors.push(`Step ${n}'s id "${id}" is already used by another step.`);
    }
    seen.add(id);
  });

  if (draft.scheduleEnabled) {
    try {
      parseCron(draft.cron);
    } catch {
      errors.push(
        'The schedule must be a 5-field cron expression (minute hour day-of-month month day-of-week), like "0 9 * * 1".',
      );
    }
    if (!validTimeZone(draft.tz)) {
      errors.push('The time zone must be an IANA zone name, like "America/New_York".');
    }
  }

  errors.push(...validatePermitBuild(draft.permit, catalog));

  return errors;
}

/** Assemble the POST /v1/workflows body. Call only on a valid draft. */
export function toCreateBody(draft: SetupDraft): CreateWorkflowBody {
  const ids = resolveStepIds(draft.steps);
  const steps: StepsDoc = {
    v: 1,
    steps: draft.steps.map((step, i) => ({ id: ids[i]!, text: step.text.trim() })),
  };
  const body: CreateWorkflowBody = {
    name: draft.name.trim(),
    steps,
    permit: buildPermitDoc(draft.permit),
  };
  if (draft.scheduleEnabled) {
    body.schedule = { cron: draft.cron.trim(), tz: draft.tz.trim() };
  }
  return body;
}
