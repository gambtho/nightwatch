import { consecutiveFailures, failingRules, shouldAutoPause } from "./grading";
import { supportDigest, supportDigestDegraded } from "../fixtures/workflows";
import type { Run, Workflow } from "./types";

function run(id: string, securityPassed: boolean): Run {
  return {
    id,
    at: `2026-08-${id}T09:00:00Z`,
    status: "ok",
    costCents: 41,
    ruleResults: [
      { ruleId: "themes", passed: true },
      { ruleId: "security", passed: securityPassed },
    ],
    summary: "Posted to #team-digest",
  };
}

function workflowWith(runs: Run[]): Workflow {
  return {
    id: "wf",
    name: "Weekly support digest",
    schedule: { label: "Mondays at 9:00 AM", timezone: "America/New_York" },
    steps: [],
    permit: { capabilities: [], denied: [], maxCostCents: 200 },
    rubric: [
      { id: "themes", text: "Groups complaints by theme, not by ticket" },
      { id: "security", text: "Flags anything security-related separately" },
    ],
    runs,
    paused: false,
  };
}

test("counts consecutive failures from the most recent run backwards", () => {
  const wf = workflowWith([run("10", true), run("17", false), run("24", false)]);
  expect(consecutiveFailures(wf, "security")).toBe(2);
  expect(consecutiveFailures(wf, "themes")).toBe(0);
});

test("a passing run resets the streak", () => {
  const wf = workflowWith([run("10", false), run("17", false), run("24", true)]);
  expect(consecutiveFailures(wf, "security")).toBe(0);
});

test("failingRules reports only rules currently failing", () => {
  const wf = workflowWith([run("17", false), run("24", false)]);
  expect(failingRules(wf).map((r) => r.id)).toEqual(["security"]);
});

test("auto-pause does not fire at two consecutive failures", () => {
  const wf = workflowWith([run("17", false), run("24", false)]);
  expect(shouldAutoPause(wf)).toBe(false);
});

test("auto-pause fires at three consecutive failures", () => {
  const wf = workflowWith([run("10", false), run("17", false), run("24", false)]);
  expect(shouldAutoPause(wf)).toBe(true);
});

test("a workflow with no runs is not paused", () => {
  expect(shouldAutoPause(workflowWith([]))).toBe(false);
});

test("the healthy support digest fixture (shown on Home) has no failing streak", () => {
  expect(consecutiveFailures(supportDigest, "security")).toBe(0);
  expect(shouldAutoPause(supportDigest)).toBe(false);
});

test("the degraded support digest fixture (shown on Alert) has failed security 3 times running", () => {
  expect(consecutiveFailures(supportDigestDegraded, "security")).toBe(3);
  expect(shouldAutoPause(supportDigestDegraded)).toBe(true);
});
