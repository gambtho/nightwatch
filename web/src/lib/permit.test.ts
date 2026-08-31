import { describe, expect, it } from "vitest";
import { parsePermit, providerLabel, spendLabel } from "./permit";

describe("parsePermit", () => {
  it("reads a full permit v1 document", () => {
    const view = parsePermit({
      v: 1,
      llm: { providers: ["anthropic", "openai"], connection: "work" },
      connections: {},
      spend: { per_run_cents: 50 },
    });
    expect(view.recognized).toBe(true);
    expect(view.providers).toEqual(["anthropic", "openai"]);
    expect(view.connection).toBe("work");
    expect(view.spendPerRunCents).toBe(50);
  });

  it("defaults connection and leaves spend absent", () => {
    const view = parsePermit({ v: 1, llm: { providers: ["anthropic"] } });
    expect(view.connection).toBe("default");
    expect(view.spendPerRunCents).toBeUndefined();
  });

  it("treats a bare {v:1} as a recognized permit that grants nothing", () => {
    const view = parsePermit({ v: 1 });
    expect(view.recognized).toBe(true);
    expect(view.providers).toEqual([]);
  });

  it("fails closed on documents it does not recognize", () => {
    for (const doc of [null, undefined, [], "permit", 7, {}, { v: 2 }]) {
      const view = parsePermit(doc);
      expect(view.recognized).toBe(false);
      expect(view.providers).toEqual([]);
    }
  });

  it("ignores malformed provider entries and spend values", () => {
    const view = parsePermit({
      v: 1,
      llm: { providers: ["anthropic", 3, null] },
      spend: { per_run_cents: -5 },
    });
    expect(view.providers).toEqual(["anthropic"]);
    expect(view.spendPerRunCents).toBeUndefined();
  });
});

describe("labels", () => {
  it("names known providers and passes through unknown ones", () => {
    expect(providerLabel("anthropic")).toBe("Claude (Anthropic)");
    expect(providerLabel("acme")).toBe("acme");
  });

  it("renders the spend cap or the monthly-cap fallback", () => {
    expect(
      spendLabel({
        providers: [],
        connection: "default",
        spendPerRunCents: 150,
        recognized: true,
      }),
    ).toBe("max $1.50 / run");
    expect(spendLabel({ providers: [], connection: "default", recognized: true })).toBe(
      "monthly cap only",
    );
  });
});
