import { describe, expect, it } from "vitest";
import { parseSteps } from "./steps";

describe("parseSteps", () => {
  it("reads the decision-9 user-facing form", () => {
    const steps = parseSteps({
      v: 1,
      steps: [
        { id: "gather", text: "Look at last week's tickets." },
        { id: "post", text: "Post a short digest." },
      ],
    });
    expect(steps).toEqual([
      { id: "gather", text: "Look at last week's tickets." },
      { id: "post", text: "Post a short digest." },
    ]);
  });

  it("skips malformed entries rather than failing the whole list", () => {
    const steps = parseSteps({
      v: 1,
      steps: [{ id: "ok", text: "fine" }, { id: 3 }, null, { text: "" }],
    });
    expect(steps).toEqual([{ id: "ok", text: "fine" }]);
  });

  it("synthesizes one step from a legacy compiled document, mirroring the server migration", () => {
    const steps = parseSteps({
      system_prompt: "You prepare the digest.",
      kickoff: "Summarize last week's tickets.",
      provider: "anthropic",
      model: "claude-sonnet-5",
    });
    expect(steps).toEqual([{ id: "job", text: "Summarize last week's tickets." }]);
  });

  it("returns nothing for documents it cannot read", () => {
    for (const doc of [null, [], "steps", {}, { v: 2, steps: [] }]) {
      expect(parseSteps(doc)).toEqual([]);
    }
  });
});
