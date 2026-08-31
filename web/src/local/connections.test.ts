import { beforeEach, describe, expect, it } from "vitest";
import type { CatalogConnector } from "../api/types";
import {
  captureGuideFor,
  connectWithToken,
  connectionState,
  disconnect,
  listMcpServers,
  registerMcpServer,
  removeMcpServer,
  resetConnectionsForTests,
  stateOverlay,
  stateView,
} from "./connections";

// The fake seam's own contract. Replaced wholesale when connectors P2
// lands the richer status, the catalog capture guide, and the verify.

beforeEach(() => {
  resetConnectionsForTests();
});

const slack: CatalogConnector = {
  id: "slack",
  name: "Slack",
  description: "Post messages.",
  auth_provider: "slack",
  connected: false,
  ops: [],
};

describe("connectionState", () => {
  it("maps the catalog boolean onto the P2 vocabulary", () => {
    expect(connectionState(slack, {})).toBe("missing");
    expect(connectionState({ ...slack, connected: true }, {})).toBe("ok");
  });

  it("lets the local overlay win, including needs_reauth", () => {
    expect(connectionState({ ...slack, connected: true }, { slack: "missing" })).toBe(
      "missing",
    );
    expect(connectionState(slack, { slack: "needs_reauth" })).toBe("needs_reauth");
    expect(stateView("needs_reauth").tone).toBe("attention");
  });
});

describe("connect and disconnect (fake)", () => {
  it("verifies then stores, and disconnect reverses it", async () => {
    const bad = await connectWithToken("slack", "xoxb-x-bad");
    expect(bad.ok).toBe(false);
    expect(await stateOverlay()).toEqual({});

    expect((await connectWithToken("slack", "xoxb-good")).ok).toBe(true);
    expect(connectionState(slack, await stateOverlay())).toBe("ok");

    await disconnect("slack");
    expect(connectionState({ ...slack, connected: true }, await stateOverlay())).toBe(
      "missing",
    );
  });
});

describe("captureGuideFor", () => {
  it("gives Slack the manifest-flow guide with the xoxb shape check", () => {
    const guide = captureGuideFor(slack);
    expect(guide.startUrl).toContain("api.slack.com/apps");
    expect(guide.secretPrefix).toBe("xoxb-");
    expect(guide.steps).toHaveLength(3);
  });

  it("falls back to a generic guide for other connectors", () => {
    const guide = captureGuideFor({ ...slack, id: "x", auth_provider: "x", name: "X" });
    expect(guide.secretPrefix).toBeUndefined();
    expect(guide.steps.length).toBeGreaterThan(0);
  });
});

describe("MCP registry (fake)", () => {
  it("registers an https server and removes it again", async () => {
    expect((await registerMcpServer("cal", "http://mcp.example.com", "k")).ok).toBe(
      false,
    );
    expect((await registerMcpServer("", "https://mcp.example.com", "k")).ok).toBe(false);

    expect((await registerMcpServer("cal", "https://mcp.example.com", "k")).ok).toBe(
      true,
    );
    const servers = await listMcpServers();
    expect(servers).toHaveLength(1);
    expect(servers[0]!.state).toBe("ok");

    await removeMcpServer(servers[0]!.id);
    expect(await listMcpServers()).toEqual([]);
  });
});
