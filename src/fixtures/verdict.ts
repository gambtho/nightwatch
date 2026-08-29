import type { Verdict } from "../lib/types";

export const supportDigestVerdict: Verdict = {
  can: [
    "Read last week's tickets every Monday morning",
    "Group them by what's actually causing them, not by ticket",
    "Call out anything that looks security-related, separately",
    "Have it waiting in #team-digest before your standup",
  ],
  cannot: [
    "I can't tell you which of these engineering should drop everything for. I can rank by how often it comes up and how angry people are — but that call needs to stay yours.",
  ],
  access: [
    {
      id: "zendesk-read",
      system: "zendesk",
      label: "Your tickets",
      access: "read",
      detail: "read only",
    },
    {
      id: "slack-digest-write",
      system: "slack",
      label: "One channel to post in",
      access: "write",
      detail: "post only",
    },
  ],
};
