import type { Workflow } from "../lib/types";

export const supportDigest: Workflow = {
  id: "wf-digest",
  name: "Weekly support digest",
  schedule: { label: "Mondays at 9:00 AM", timezone: "America/New_York" },
  steps: [{ id: "s1", text: "Summarize recurring support themes" }],
  permit: { capabilities: [], denied: [], maxCostCents: 200 },
  rubric: [
    { id: "themes", text: "Groups complaints by theme, not by ticket" },
    { id: "security", text: "Flags anything security-related separately" },
    { id: "length", text: "Fits in one screen" },
  ],
  runs: [
    {
      id: "r1",
      at: "2026-08-24T09:00:00Z",
      status: "ok",
      costCents: 41,
      ruleResults: [
        { ruleId: "themes", passed: true },
        { ruleId: "security", passed: true },
        { ruleId: "length", passed: true },
      ],
      summary: "Posted to #team-digest · met all 3 of your rules",
    },
  ],
  paused: false,
};

// The same workflow as `supportDigest`, in the weeks that follow, after the
// source data quietly changed underneath it. Home never shows this one — it
// exists so Alert can derive its story from graded data instead of
// hardcoding it.
export const supportDigestDegraded: Workflow = {
  id: "wf-digest",
  name: "Weekly support digest",
  schedule: { label: "Mondays at 9:00 AM", timezone: "America/New_York" },
  steps: [{ id: "s1", text: "Summarize recurring support themes" }],
  permit: { capabilities: [], denied: [], maxCostCents: 200 },
  rubric: [
    { id: "themes", text: "Groups complaints by theme, not by ticket" },
    { id: "security", text: "Flags anything security-related separately" },
    { id: "length", text: "Fits in one screen" },
  ],
  runs: [
    {
      id: "r1",
      at: "2026-08-31T09:00:00Z",
      status: "ok",
      costCents: 41,
      ruleResults: [
        { ruleId: "themes", passed: true },
        { ruleId: "security", passed: false },
        { ruleId: "length", passed: true },
      ],
      summary: "Posted to #team-digest · missed 1 of your 3 rules",
    },
    {
      id: "r2",
      at: "2026-09-07T09:00:00Z",
      status: "ok",
      costCents: 41,
      ruleResults: [
        { ruleId: "themes", passed: true },
        { ruleId: "security", passed: false },
        { ruleId: "length", passed: true },
      ],
      summary: "Posted to #team-digest · missed 1 of your 3 rules",
    },
    {
      id: "r3",
      at: "2026-09-14T09:00:00Z",
      status: "paused",
      costCents: 41,
      ruleResults: [
        { ruleId: "themes", passed: true },
        { ruleId: "security", passed: false },
        { ruleId: "length", passed: true },
      ],
      summary: "Posted to #team-digest · missed 1 of your 3 rules",
    },
  ],
  paused: true,
};

// Scheduled for the middle of the night on a laptop that sleeps: the run
// fired on wake. 3:00 AM America/New_York (EDT) is 07:00Z; 7:42 AM is
// 11:42Z. Home renders the gap honestly — "scheduled 3:00 AM · ran
// 7:42 AM, when your computer woke".
export const renewals: Workflow = {
  id: "wf-renewals",
  name: "Contract renewals coming up",
  schedule: { label: "Every night at 3:00 AM", timezone: "America/New_York" },
  steps: [{ id: "s1", text: "Check for contracts due in 60 days" }],
  permit: { capabilities: [], denied: [], maxCostCents: 100 },
  rubric: [{ id: "window", text: "Looks 60 days ahead" }],
  runs: [
    {
      id: "r1",
      at: "2026-08-31T11:42:00Z",
      fireTime: "2026-08-31T07:00:00Z",
      status: "ok",
      costCents: 12,
      ruleResults: [{ ruleId: "window", passed: true }],
      summary: "Nothing due in the next 60 days",
    },
  ],
  paused: false,
};

export const unanswered: Workflow = {
  id: "wf-unanswered",
  name: "Unanswered customer questions",
  schedule: { label: "Every day at 5:00 PM", timezone: "America/New_York" },
  steps: [{ id: "s1", text: "Nudge threads with no reply" }],
  permit: { capabilities: [], denied: [], maxCostCents: 100 },
  rubric: [
    { id: "age", text: "Only threads older than 24 hours" },
    { id: "tone", text: "Nudges politely" },
  ],
  runs: [
    {
      id: "r1",
      at: "2026-08-29T17:00:00Z",
      status: "ok",
      costCents: 8,
      ruleResults: [
        { ruleId: "age", passed: true },
        { ruleId: "tone", passed: true },
      ],
      summary: "Nudged 2 threads · met all 2 of your rules",
    },
  ],
  paused: false,
};

export const allWorkflows: Workflow[] = [supportDigest, renewals, unanswered];
