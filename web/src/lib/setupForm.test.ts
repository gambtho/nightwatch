import { describe, expect, it } from "vitest";
import { EMPTY_BUILD } from "./permitBuild";
import {
  resolveStepIds,
  slugify,
  toCreateBody,
  validateDraft,
  validTimeZone,
} from "./setupForm";
import type { SetupDraft } from "./setupForm";

function draft(overrides: Partial<SetupDraft>): SetupDraft {
  return {
    name: "weekly digest",
    steps: [{ id: "", text: "Look at last week's tickets." }],
    scheduleEnabled: false,
    cron: "0 9 * * 1",
    tz: "UTC",
    permit: { ...EMPTY_BUILD, providers: ["anthropic"] },
    ...overrides,
  };
}

describe("slugify", () => {
  it("lowercases and joins runs with single hyphens", () => {
    expect(slugify("Look at last week's tickets!")).toBe("look-at-last-week-s-tickets");
    expect(slugify("  --Post digest--  ")).toBe("post-digest");
  });

  it("returns empty when nothing usable survives", () => {
    expect(slugify("!!!")).toBe("");
  });

  it("caps at 64 chars without a trailing hyphen", () => {
    const slug = slugify("a".repeat(63) + " tail words beyond the limit");
    expect(slug.length).toBeLessThanOrEqual(64);
    expect(slug.endsWith("-")).toBe(false);
  });
});

describe("resolveStepIds", () => {
  it("keeps explicit ids and derives the rest from text", () => {
    expect(
      resolveStepIds([
        { id: "gather", text: "whatever" },
        { id: "", text: "Post the digest" },
      ]),
    ).toEqual(["gather", "post-the-digest"]);
  });

  it("de-duplicates derived ids with numeric suffixes", () => {
    expect(
      resolveStepIds([
        { id: "", text: "Check tickets" },
        { id: "", text: "Check tickets" },
        { id: "", text: "Check tickets" },
      ]),
    ).toEqual(["check-tickets", "check-tickets-2", "check-tickets-3"]);
  });

  it("avoids colliding with an explicit id but never rewrites one", () => {
    expect(
      resolveStepIds([
        { id: "check-tickets", text: "x" },
        { id: "", text: "Check tickets" },
      ]),
    ).toEqual(["check-tickets", "check-tickets-2"]);
    // Two identical explicit ids stay as written — the collision is the
    // user's to see (validateDraft reports it).
    expect(
      resolveStepIds([
        { id: "dup", text: "x" },
        { id: "dup", text: "y" },
      ]),
    ).toEqual(["dup", "dup"]);
  });

  it('falls back to "step" when the text has no usable characters', () => {
    expect(resolveStepIds([{ id: "", text: "!!!" }])).toEqual(["step"]);
  });
});

describe("validTimeZone", () => {
  it("accepts IANA zones and rejects Local, blanks, and junk", () => {
    expect(validTimeZone("UTC")).toBe(true);
    expect(validTimeZone("America/New_York")).toBe(true);
    expect(validTimeZone("Local")).toBe(false);
    expect(validTimeZone("")).toBe(false);
    expect(validTimeZone("Not/AZone")).toBe(false);
  });
});

describe("validateDraft", () => {
  it("accepts a minimal valid draft", () => {
    expect(validateDraft(draft({}), null)).toEqual([]);
  });

  it("requires a name and at least one step", () => {
    expect(validateDraft(draft({ name: "  " }), null)).toContain(
      "Give the workflow a name.",
    );
    expect(validateDraft(draft({ steps: [] }), null)).toContain("Add at least one step.");
  });

  it("caps steps at 20", () => {
    const steps = Array.from({ length: 21 }, (_, i) => ({ id: "", text: `Step ${i}` }));
    expect(validateDraft(draft({ steps }), null)).toContain("At most 20 steps.");
  });

  it("rejects empty and over-long step text", () => {
    expect(validateDraft(draft({ steps: [{ id: "", text: "  " }] }), null)).toContain(
      "Step 1 needs text.",
    );
    expect(
      validateDraft(draft({ steps: [{ id: "", text: "x".repeat(501) }] }), null),
    ).toContain("Step 1 is over 500 characters.");
  });

  it("counts characters as the server does — code points, not UTF-16 units", () => {
    // 500 astral-plane characters: 1000 UTF-16 units but 500 runes.
    const emoji = "🌙".repeat(500);
    expect(validateDraft(draft({ steps: [{ id: "s", text: emoji }] }), null)).toEqual([]);
    expect(
      validateDraft(draft({ steps: [{ id: "s", text: emoji + "x" }] }), null),
    ).toContain("Step 1 is over 500 characters.");
  });

  it("rejects bad and duplicate explicit ids", () => {
    const [badId] = validateDraft(
      draft({ steps: [{ id: "Not A Slug", text: "ok" }] }),
      null,
    );
    expect(badId).toMatch(/must be lowercase letters, digits, and single hyphens/);

    const dupErrors = validateDraft(
      draft({
        steps: [
          { id: "dup", text: "one" },
          { id: "dup", text: "two" },
        ],
      }),
      null,
    );
    expect(dupErrors).toEqual(['Step 2\'s id "dup" is already used by another step.']);
  });

  it("checks the schedule only when enabled", () => {
    expect(
      validateDraft(draft({ scheduleEnabled: false, cron: "junk", tz: "Nowhere" }), null),
    ).toEqual([]);
    const errors = validateDraft(
      draft({ scheduleEnabled: true, cron: "junk", tz: "Nowhere" }),
      null,
    );
    expect(errors).toHaveLength(2);
    expect(errors[0]).toMatch(/5-field cron/);
    expect(errors[1]).toMatch(/IANA zone/);
  });

  it("rejects a six-field cron — the server takes exactly five", () => {
    const errors = validateDraft(
      draft({ scheduleEnabled: true, cron: "0 0 9 * * 1", tz: "UTC" }),
      null,
    );
    expect(errors).toHaveLength(1);
  });
});

describe("toCreateBody", () => {
  it("assembles the documented POST /v1/workflows body", () => {
    const body = toCreateBody(
      draft({
        name: "  weekly digest  ",
        steps: [
          { id: "", text: " Look at tickets. " },
          { id: "post", text: "Post the digest." },
        ],
        scheduleEnabled: true,
        cron: "0 9 * * 1",
        tz: "UTC",
        permit: { ...EMPTY_BUILD, providers: ["anthropic"], perRunCents: 50 },
      }),
    );
    expect(body).toEqual({
      name: "weekly digest",
      steps: {
        v: 1,
        steps: [
          { id: "look-at-tickets", text: "Look at tickets." },
          { id: "post", text: "Post the digest." },
        ],
      },
      permit: {
        v: 1,
        llm: { providers: ["anthropic"] },
        spend: { per_run_cents: 50 },
      },
      schedule: { cron: "0 9 * * 1", tz: "UTC" },
    });
  });

  it("omits the schedule when disabled", () => {
    expect(toCreateBody(draft({ scheduleEnabled: false })).schedule).toBeUndefined();
  });
});
