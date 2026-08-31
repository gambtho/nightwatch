import type { Verdict } from "../lib/types";

// Dev-persona demo branch: same shape as the support-digest verdict on main,
// re-voiced for the weekly CI-flake digest scenario.
export const supportDigestVerdict: Verdict = {
  can: [
    "Read last week's CI failures every Monday morning",
    "Group them by root cause, not by test name",
    "Call out anything that looks like a real product bug, separately",
    "Have it waiting in #eng-quality before your standup",
  ],
  cannot: [
    "I can't tell you which flake to quarantine first. I can rank by how often it fails and how much it blocks — but that call needs to stay yours.",
  ],
  access: [
    {
      id: "gha-read",
      system: "github",
      label: "Your CI runs",
      access: "read",
      detail: "read only",
    },
    {
      id: "slack-quality-write",
      system: "slack",
      label: "One channel to post in",
      access: "write",
      detail: "post only",
    },
  ],
};
