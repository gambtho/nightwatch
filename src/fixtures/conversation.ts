import { emptyPermit, grant } from "../lib/permit";
import type { Capability, Permit } from "../lib/types";

export interface BuildTurn {
  id: string;
  speaker: "user" | "nightshift";
  text: string;
  grants: Capability[];
}

export const MAX_COST_CENTS = 200;

export const buildScript: BuildTurn[] = [
  {
    id: "t1",
    speaker: "user",
    text: "Every Monday, look at last week's CI failures and tell the team what keeps flaking.",
    grants: [],
  },
  {
    id: "t2",
    speaker: "nightshift",
    text: "Got it. I'll need to read your CI runs — is that GitHub Actions, or Buildkite?",
    grants: [
      {
        id: "gha-read",
        system: "github",
        label: "GitHub Actions runs",
        access: "read",
        detail: "read only",
      },
    ],
  },
  {
    id: "t3",
    speaker: "user",
    text: "Actions, plus whatever lands in #ci-alerts. Post the summary in #eng-quality.",
    grants: [
      {
        id: "slack-ci-alerts-read",
        system: "slack",
        label: "Slack #ci-alerts",
        access: "read",
        detail: "last 7 days",
      },
      {
        id: "slack-quality-write",
        system: "slack",
        label: "Slack #eng-quality",
        access: "write",
        detail: "post only",
      },
    ],
  },
  {
    id: "t4",
    speaker: "nightshift",
    text: "Done. What should I do if a failure looks like a real product bug, not a flake?",
    grants: [],
  },
  {
    id: "t5",
    speaker: "user",
    text: "Flag it separately at the top, and don't bury it.",
    grants: [],
  },
  {
    id: "t6",
    speaker: "nightshift",
    text: "That's everything I need. Want to see exactly what this will be able to touch?",
    grants: [],
  },
];

export function permitAfter(turns: BuildTurn[], upTo: number): Permit {
  return turns
    .slice(0, upTo)
    .flatMap((t) => t.grants)
    .reduce(grant, emptyPermit(MAX_COST_CENTS));
}
