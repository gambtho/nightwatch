import { describe, expect, it } from "vitest";
import type { Version } from "../api/types";
import { lastRun, summarizeVersions } from "./versions";

function version(number: number, status: Version["status"]): Version {
  return {
    number,
    status,
    steps: {},
    permit: {},
    rubric: {},
    approved_at: null,
    created_at: "2026-08-30T00:00:00Z",
  };
}

describe("summarizeVersions", () => {
  it("finds the approved version and the newest draft", () => {
    const s = summarizeVersions([
      version(1, "superseded"),
      version(2, "approved"),
      version(3, "draft"),
      version(4, "draft"),
    ]);
    expect(s.approved?.number).toBe(2);
    expect(s.latestDraft?.number).toBe(4);
  });

  it("handles a workflow with no approved version", () => {
    const s = summarizeVersions([version(1, "draft")]);
    expect(s.approved).toBeUndefined();
    expect(s.latestDraft?.number).toBe(1);
  });

  it("handles an empty list", () => {
    expect(summarizeVersions([])).toEqual({});
  });
});

describe("lastRun", () => {
  it("returns the most recently created entry", () => {
    const runs = [
      { created_at: "2026-08-29T00:00:00Z" },
      { created_at: "2026-08-31T00:00:00Z" },
      { created_at: "2026-08-30T00:00:00Z" },
    ];
    expect(lastRun(runs)?.created_at).toBe("2026-08-31T00:00:00Z");
  });

  it("returns undefined for no runs", () => {
    expect(lastRun([])).toBeUndefined();
  });
});
