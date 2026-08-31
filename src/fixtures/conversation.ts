import { emptyPermit, grant } from "../lib/permit";
import type { Capability, Permit } from "../lib/types";

export interface BuildTurn {
  id: string;
  speaker: "user" | "tomte";
  text: string;
  grants: Capability[];
  // This turn pauses the build until Slack is connected in the
  // connections manager — connect once, every later build finds it.
  connectSlack?: boolean;
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
    speaker: "tomte",
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
    speaker: "tomte",
    text: "I can do that once Slack is connected. It's one paste — and every job you build after this one will find it already connected.",
    grants: [],
    connectSlack: true,
  },
  {
    id: "t5",
    speaker: "tomte",
    text: "What should I do if something looks like a security problem?",
    grants: [],
  },
  {
    id: "t6",
    speaker: "user",
    text: "Flag it separately at the top, and don't bury it.",
    grants: [],
  },
  {
    id: "t7",
    speaker: "tomte",
    text: "That's everything I need. Here's my honest read on what I can and can't do — before anything runs.",
    grants: [],
  },
];

export function permitAfter(turns: BuildTurn[], upTo: number): Permit {
  return turns
    .slice(0, upTo)
    .flatMap((t) => t.grants)
    .reduce(grant, emptyPermit(MAX_COST_CENTS));
}
