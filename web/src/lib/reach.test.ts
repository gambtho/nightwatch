import { describe, expect, it } from "vitest";
import type { CatalogConnector } from "../api/types";
import { reachColumns } from "./reach";

const catalog: CatalogConnector[] = [
  {
    id: "google-calendar",
    name: "Google Calendar",
    description: "Calendars.",
    auth_provider: "google",
    connected: true,
    ops: [
      {
        name: "list_events",
        description: "List events.",
        effect: "read",
        scopes: [],
        args_schema: {},
      },
      {
        name: "create_event",
        description: "Create an event.",
        effect: "write",
        scopes: [],
        args_schema: {},
        constraints: ["calendar_id"],
      },
    ],
  },
];

describe("reachColumns", () => {
  it("splits granted ops into read and write by catalog effect", () => {
    const { read, write } = reachColumns(
      [
        {
          connector: "google-calendar",
          ops: [
            { name: "list_events", resources: {} },
            { name: "create_event", resources: { calendar_id: ["primary", "team"] } },
          ],
        },
      ],
      catalog,
    );
    expect(read).toHaveLength(1);
    expect(read[0]).toMatchObject({
      connector: "Google Calendar",
      op: "list events",
    });
    expect(read[0]!.note).toBeUndefined();
    expect(write).toHaveLength(1);
    expect(write[0]).toMatchObject({
      op: "create event",
      resources: ["calendar id: primary, team"],
    });
  });

  it("marks an op the loaded catalog does not list, in the write column", () => {
    const { read, write } = reachColumns(
      [{ connector: "acme", ops: [{ name: "mystery_op", resources: {} }] }],
      catalog,
    );
    expect(read).toHaveLength(0);
    expect(write).toHaveLength(1);
    expect(write[0]).toMatchObject({
      connector: "acme",
      op: "mystery op",
      note: "unlisted",
    });
  });

  it("marks grants unchecked, not unlisted, when the catalog is unavailable", () => {
    for (const cat of [null, undefined]) {
      const { read, write } = reachColumns(
        [{ connector: "google-calendar", ops: [{ name: "list_events", resources: {} }] }],
        cat,
      );
      expect(read).toHaveLength(0);
      expect(write).toHaveLength(1);
      expect(write[0]).toMatchObject({ op: "list events", note: "unchecked" });
    }
  });

  it("keeps an unreadable permit entry visible in the write column", () => {
    const { write } = reachColumns(
      [{ connector: "google-calendar", ops: [], unreadable: true }],
      catalog,
    );
    expect(write).toHaveLength(1);
    expect(write[0]).toMatchObject({
      connector: "Google Calendar",
      note: "unreadable",
    });
  });

  it("returns empty columns for no grants", () => {
    expect(reachColumns([], catalog)).toEqual({ read: [], write: [] });
  });
});
