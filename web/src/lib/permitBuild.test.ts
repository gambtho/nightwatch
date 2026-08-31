import { describe, expect, it } from "vitest";
import type { CatalogConnector } from "../api/types";
import { buildPermitDoc, parseResourceList, validatePermitBuild } from "./permitBuild";
import type { PermitBuild } from "./permitBuild";

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

function build(overrides: Partial<PermitBuild>): PermitBuild {
  return { providers: [], connection: "default", grants: [], ...overrides };
}

describe("parseResourceList", () => {
  it("splits on commas and newlines and drops blanks", () => {
    expect(parseResourceList(" primary, work\nteam , ,")).toEqual([
      "primary",
      "work",
      "team",
    ]);
    expect(parseResourceList("")).toEqual([]);
  });
});

describe("buildPermitDoc", () => {
  it("emits a bare v1 document when nothing is granted", () => {
    expect(buildPermitDoc(build({}))).toEqual({ v: 1 });
  });

  it("omits the default connection name but keeps a named one", () => {
    expect(buildPermitDoc(build({ providers: ["anthropic"] }))).toEqual({
      v: 1,
      llm: { providers: ["anthropic"] },
    });
    expect(
      buildPermitDoc(build({ providers: ["anthropic"], connection: "work" })),
    ).toEqual({ v: 1, llm: { providers: ["anthropic"], connection: "work" } });
  });

  it("emits spend and connector grants with resources", () => {
    const doc = buildPermitDoc(
      build({
        providers: ["anthropic"],
        perRunCents: 50,
        grants: [
          {
            connector: "google-calendar",
            ops: [
              { op: "list_events", resources: {} },
              { op: "create_event", resources: { calendar_id: ["primary"] } },
            ],
          },
        ],
      }),
    );
    expect(doc).toEqual({
      v: 1,
      llm: { providers: ["anthropic"] },
      spend: { per_run_cents: 50 },
      connections: {
        "google-calendar": {
          kind: "http",
          ops: ["list_events", "create_event"],
          resources: { create_event: { calendar_id: ["primary"] } },
        },
      },
    });
  });

  it("drops a grant with no ops — deny-all is the entry's absence", () => {
    const doc = buildPermitDoc(
      build({ grants: [{ connector: "google-calendar", ops: [] }] }),
    );
    expect(doc.connections).toBeUndefined();
  });
});

describe("validatePermitBuild", () => {
  it("accepts a clean build", () => {
    const errors = validatePermitBuild(
      build({
        providers: ["anthropic"],
        perRunCents: 50,
        grants: [
          {
            connector: "google-calendar",
            ops: [
              { op: "list_events", resources: {} },
              { op: "create_event", resources: { calendar_id: ["primary"] } },
            ],
          },
        ],
      }),
      catalog,
    );
    expect(errors).toEqual([]);
  });

  it("rejects a non-positive or fractional spend cap", () => {
    expect(validatePermitBuild(build({ perRunCents: 0 }), catalog)).toHaveLength(1);
    expect(validatePermitBuild(build({ perRunCents: -5 }), catalog)).toHaveLength(1);
    expect(validatePermitBuild(build({ perRunCents: 1.5 }), catalog)).toHaveLength(1);
    expect(validatePermitBuild(build({ perRunCents: NaN }), catalog)).toHaveLength(1);
  });

  it("requires a resource list for every constrained field of a granted op", () => {
    const errors = validatePermitBuild(
      build({
        grants: [
          {
            connector: "google-calendar",
            ops: [{ op: "create_event", resources: {} }],
          },
        ],
      }),
      catalog,
    );
    expect(errors).toEqual([
      'Google Calendar: "create_event" needs at least one approved calendar_id value.',
    ]);
  });

  it("rejects resources on a field the op has no constraint for", () => {
    const errors = validatePermitBuild(
      build({
        grants: [
          {
            connector: "google-calendar",
            ops: [{ op: "list_events", resources: { calendar_id: ["primary"] } }],
          },
        ],
      }),
      catalog,
    );
    expect(errors).toEqual([
      'Google Calendar: "list_events" can\'t be narrowed by calendar_id — nothing enforces it.',
    ]);
  });

  it("rejects grants for connectors or ops the catalog does not define", () => {
    expect(
      validatePermitBuild(
        build({ grants: [{ connector: "acme", ops: [{ op: "x", resources: {} }] }] }),
        catalog,
      ),
    ).toEqual(['The catalog has no connector "acme".']);
    expect(
      validatePermitBuild(
        build({
          grants: [
            { connector: "google-calendar", ops: [{ op: "delete_all", resources: {} }] },
          ],
        }),
        catalog,
      ),
    ).toEqual(['Google Calendar has no operation "delete_all".']);
  });

  it("rejects grants when the catalog is unavailable", () => {
    const errors = validatePermitBuild(
      build({
        grants: [
          { connector: "google-calendar", ops: [{ op: "list_events", resources: {} }] },
        ],
      }),
      null,
    );
    expect(errors).toEqual([
      "Connector grants need the catalog, which couldn't be loaded.",
    ]);
  });

  it("ignores grants with no ops at all", () => {
    expect(
      validatePermitBuild(build({ grants: [{ connector: "acme", ops: [] }] }), null),
    ).toEqual([]);
  });
});
