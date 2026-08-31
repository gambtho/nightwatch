import type { CatalogConnector } from "../api/types";

// ── FAKE SEAM ────────────────────────────────────────────────────────
// The thin interface where P2's API lands. The catalog itself is real
// (GET /v1/catalog, `connected` boolean); everything else here is faked
// until connectors P2:
//
//   connectionState / stateOverlay  → the richer per-connector status
//     (`ok` / `needs_reauth` / `missing`) joining the catalog in P2 —
//     a pasted token can still be revoked upstream
//   captureGuideFor                 → the structured capture guide in
//     curated connector auth (`auth.capture`: start_url, steps,
//     secret_prefix, verify_op)
//   connectWithToken                → PUT /v1/connections/{name} with
//     the control-plane verify (the connector's verify_op, invoked with
//     the pasted token before storing — never a run token)
//   disconnect                      → connection deletion
//   listMcpServers / registerMcpServer / removeMcpServer → remote MCP
//     registration (old connectors phases 5→6, now the main road; the
//     SSRF defense suite is server-side and applies unchanged)
//
// Storage is localStorage (in-memory under jsdom), plus latency so the
// screens exercise real async states. Delete with the swap.
// ─────────────────────────────────────────────────────────────────────

export type ConnectionState = "ok" | "needs_reauth" | "missing";

export interface CaptureGuide {
  startUrl?: string;
  startLabel?: string;
  steps: string[];
  secretPrefix?: string;
  placeholder?: string;
}

export interface McpServer {
  id: string;
  name: string;
  url: string;
  state: ConnectionState;
}

export type VerifyResult = { ok: true } | { ok: false; message: string };

const OVERLAY_KEY = "tomte.local-connections.v1";
const MCP_KEY = "tomte.local-mcp.v1";

const memory = new Map<string, string>();

function hasLocalStorage(): boolean {
  try {
    return typeof localStorage !== "undefined" && localStorage !== null;
  } catch {
    return false;
  }
}

function readJson<T>(key: string, fallback: T): T {
  try {
    const raw = hasLocalStorage() ? localStorage.getItem(key) : memory.get(key);
    return raw ? (JSON.parse(raw) as T) : fallback;
  } catch {
    return fallback;
  }
}

function writeJson(key: string, value: unknown): void {
  const raw = JSON.stringify(value);
  if (hasLocalStorage()) {
    localStorage.setItem(key, raw);
  } else {
    memory.set(key, raw);
  }
}

/** Clears the fake store — for tests only. */
export function resetConnectionsForTests(): void {
  memory.clear();
  if (hasLocalStorage()) {
    localStorage.removeItem(OVERLAY_KEY);
    localStorage.removeItem(MCP_KEY);
  }
}

function delay(ms = 250): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export type StateOverlay = Record<string, ConnectionState>;

export async function stateOverlay(): Promise<StateOverlay> {
  await delay(100);
  return readJson<StateOverlay>(OVERLAY_KEY, {});
}

/**
 * A connector's state: the local overlay (this prototype's connects and
 * disconnects) wins; otherwise the catalog's real boolean, mapped onto
 * the P2 vocabulary.
 */
export function connectionState(
  connector: CatalogConnector,
  overlay: StateOverlay,
): ConnectionState {
  return overlay[connector.id] ?? (connector.connected ? "ok" : "missing");
}

export interface StateView {
  label: string;
  tone: "ok" | "attention";
}

export function stateView(state: ConnectionState): StateView {
  switch (state) {
    case "ok":
      return { label: "Connected", tone: "ok" };
    case "needs_reauth":
      return { label: "Needs a fresh key", tone: "attention" };
    case "missing":
      return { label: "Not connected", tone: "attention" };
  }
}

// The Slack guide is the spec's worked example: the manifest link
// pre-authors the app so the paste is Create → Install → copy token.
const SLACK_GUIDE: CaptureGuide = {
  startUrl: "https://api.slack.com/apps?new_app=1",
  startLabel: "Create the Slack app",
  steps: [
    "Click Create App — we've pre-filled what Tomte needs.",
    "Click Install to your workspace and approve.",
    "Copy the token that starts with xoxb- and paste it below.",
  ],
  secretPrefix: "xoxb-",
  placeholder: "xoxb-…",
};

export function captureGuideFor(connector: CatalogConnector): CaptureGuide {
  if (connector.auth_provider === "slack") return SLACK_GUIDE;
  return {
    steps: [
      `Open ${connector.name}'s settings and create an API key for Tomte.`,
      "Copy the key and paste it below.",
    ],
    placeholder: "the key",
  };
}

/**
 * Verify-then-store. Fake behavior: a token ending in "-bad" fails, so
 * the failure state is reachable in demos; anything else connects.
 */
export async function connectWithToken(
  connectorId: string,
  token: string,
): Promise<VerifyResult> {
  await delay(700);
  if (token.trim() === "") return { ok: false, message: "Paste a token first." };
  if (token.endsWith("-bad")) {
    return {
      ok: false,
      message:
        "The service didn't accept this token. Copy it again and re-paste — a character may be missing.",
    };
  }
  const overlay = readJson<StateOverlay>(OVERLAY_KEY, {});
  writeJson(OVERLAY_KEY, { ...overlay, [connectorId]: "ok" });
  return { ok: true };
}

export async function disconnect(connectorId: string): Promise<void> {
  await delay();
  const overlay = readJson<StateOverlay>(OVERLAY_KEY, {});
  writeJson(OVERLAY_KEY, { ...overlay, [connectorId]: "missing" });
}

export async function listMcpServers(): Promise<McpServer[]> {
  await delay(100);
  return readJson<McpServer[]>(MCP_KEY, []);
}

/**
 * Registers a remote MCP server. URL shape is checked here for instant
 * feedback; the real SSRF defenses are server-side (P2) and are the
 * ones that count.
 */
export async function registerMcpServer(
  name: string,
  url: string,
  key: string,
): Promise<VerifyResult> {
  await delay(700);
  if (name.trim() === "") return { ok: false, message: "Give the server a name." };
  let parsed: URL;
  try {
    parsed = new URL(url.trim());
  } catch {
    return { ok: false, message: "That doesn't look like a web address." };
  }
  if (parsed.protocol !== "https:") {
    return { ok: false, message: "MCP servers must be reached over https://." };
  }
  if (key.endsWith("-bad")) {
    return {
      ok: false,
      message: "The server didn't accept this key. Copy it again and re-paste.",
    };
  }
  const servers = readJson<McpServer[]>(MCP_KEY, []);
  const id = `mcp-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
  writeJson(MCP_KEY, [
    ...servers,
    { id, name: name.trim(), url: parsed.toString(), state: "ok" },
  ]);
  return { ok: true };
}

export async function removeMcpServer(id: string): Promise<void> {
  await delay();
  const servers = readJson<McpServer[]>(MCP_KEY, []);
  writeJson(
    MCP_KEY,
    servers.filter((s) => s.id !== id),
  );
}
