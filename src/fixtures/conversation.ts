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
    text: "Every Monday, look at last week's support tickets and tell the team what keeps coming up.",
    grants: [],
  },
  {
    id: "t2",
    speaker: "nightshift",
    text: "Got it. I'll need to read your tickets — is that Zendesk, or the #support Slack channel?",
    grants: [
      {
        id: "zendesk-read",
        system: "zendesk",
        label: "Zendesk tickets",
        access: "read",
        detail: "read only",
      },
    ],
  },
  {
    id: "t3",
    speaker: "user",
    text: "Both. Post the summary in #team-digest.",
    grants: [
      {
        id: "slack-support-read",
        system: "slack",
        label: "Slack #support",
        access: "read",
        detail: "last 7 days",
      },
      {
        id: "slack-digest-write",
        system: "slack",
        label: "Slack #team-digest",
        access: "write",
        detail: "post only",
      },
    ],
  },
  {
    id: "t4",
    speaker: "nightshift",
    text: "Done. What should I do if something looks like a security problem?",
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
