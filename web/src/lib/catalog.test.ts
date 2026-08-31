import { describe, expect, it } from "vitest";
import type { CatalogConnector } from "../api/types";
import { connectionView, opLabel } from "./catalog";

function connector(overrides: Partial<CatalogConnector>): CatalogConnector {
  return {
    id: "slack",
    name: "Slack",
    description: "Post messages.",
    auth_provider: "slack",
    connected: false,
    ops: [],
    ...overrides,
  };
}

describe("connectionView", () => {
  it("renders connected and not-connected from the boolean", () => {
    expect(connectionView(connector({ connected: true }))).toEqual({
      connected: true,
      label: "Connected",
    });
    expect(connectionView(connector({ connected: false }))).toEqual({
      connected: false,
      label: "Not connected",
    });
  });

  it("survives the phase-2 richer status field appearing alongside", () => {
    // Phase 2 adds a status field next to `connected`; until this helper
    // learns its values, the boolean stays the source of truth and the
    // extra field must not break rendering.
    const withStatus = {
      ...connector({ connected: true }),
      status: "needs_reauth",
    } as CatalogConnector;
    expect(connectionView(withStatus).connected).toBe(true);
  });
});

describe("opLabel", () => {
  it("humanizes op names", () => {
    expect(opLabel("create_event")).toBe("create event");
    expect(opLabel("post")).toBe("post");
  });
});
