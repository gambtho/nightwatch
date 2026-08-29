import type { RubricRule, Workflow } from "./types";

export const AUTO_PAUSE_THRESHOLD = 3;

export function consecutiveFailures(workflow: Workflow, ruleId: string): number {
  let streak = 0;
  for (let i = workflow.runs.length - 1; i >= 0; i--) {
    const result = workflow.runs[i].ruleResults.find((r) => r.ruleId === ruleId);
    if (!result || result.passed) break;
    streak++;
  }
  return streak;
}

export function failingRules(workflow: Workflow): RubricRule[] {
  return workflow.rubric.filter((rule) => consecutiveFailures(workflow, rule.id) > 0);
}

export function shouldAutoPause(workflow: Workflow): boolean {
  return workflow.rubric.some(
    (rule) => consecutiveFailures(workflow, rule.id) >= AUTO_PAUSE_THRESHOLD,
  );
}
