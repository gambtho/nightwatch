import { describe, expect, it } from "vitest";
import { parseSteps } from "./steps";

describe("parseSteps", () => {
  it("reads the decision-9 user-facing form", () => {
    const view = parseSteps({
      v: 1,
      steps: [
        { id: "gather", text: "Look at last week's tickets." },
        { id: "post", text: "Post a short digest." },
      ],
    });
    expect(view.recognized).toBe(true);
    expect(view.steps).toEqual([
      { id: "gather", text: "Look at last week's tickets." },
      { id: "post", text: "Post a short digest." },
    ]);
  });

  it("skips malformed entries rather than failing the whole list", () => {
    const view = parseSteps({
      v: 1,
      steps: [{ id: "ok", text: "fine" }, { id: 3 }, null, { text: "" }],
    });
    expect(view.steps).toEqual([{ id: "ok", text: "fine" }]);
    expect(view.recognized).toBe(true);
  });

  it("synthesizes one step from a legacy compiled document, mirroring the server migration", () => {
    const view = parseSteps({
      system_prompt: "You prepare the digest.",
      kickoff: "Summarize last week's tickets.",
      provider: "anthropic",
      model: "claude-sonnet-5",
    });
    expect(view.recognized).toBe(true);
    expect(view.steps).toEqual([{ id: "job", text: "Summarize last week's tickets." }]);
  });

  it("marks documents it cannot read as unrecognized", () => {
    for (const doc of [null, [], "steps", {}, { v: 2, steps: [] }]) {
      const view = parseSteps(doc);
      expect(view.steps).toEqual([]);
      expect(view.recognized).toBe(false);
    }
  });
});
