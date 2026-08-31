import type { Workflow } from "./types";

// The user's monthly budget — "how much Tomte may spend from your key per
// month", set at first run, editable in settings.
export const MONTHLY_BUDGET_CENTS = 1000;

export function dollars(cents: number): string {
  return `$${(cents / 100).toFixed(2)}`;
}

export function spentCents(workflows: Workflow[]): number {
  return workflows
    .flatMap((w) => w.runs)
    .reduce((total, run) => total + run.costCents, 0);
}

export function budgetPercent(spent: number, budget: number): number {
  if (budget <= 0) return 100;
  return Math.min(100, Math.round((spent / budget) * 100));
}
