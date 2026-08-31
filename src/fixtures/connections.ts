// The connections manager's rows. Slack is the one curated connector; its
// capture guide follows the catalog's structured shape (start_url, steps,
// secret_prefix, verify_op). Calendar and inbox arrive as remote MCP
// services that do their own auth.
export const SLACK_CAPTURE = {
  startLabel: "Open Slack's app builder — we've pre-filled what Tomte needs",
  steps: [
    "Click Create App — we've pre-filled what Tomte needs.",
    "Click Install to your workspace and approve.",
    "Copy the token that starts with xoxb- and paste it below.",
  ],
  secretPrefix: "xoxb-",
  verifyingLabel: "Checking with Slack…",
};

export interface McpRow {
  id: string;
  name: string;
  detail: string;
}

export const MCP_ROWS: McpRow[] = [
  {
    id: "calendar",
    name: "Calendar",
    detail:
      "Connects through a calendar service of your choice. That service handles its own sign-in; you paste the key it gives you — same card as Slack.",
  },
  {
    id: "inbox",
    name: "Inbox",
    detail:
      "Connects through a mail service of your choice. Its site signs you in; you paste the key it issues.",
  },
];
