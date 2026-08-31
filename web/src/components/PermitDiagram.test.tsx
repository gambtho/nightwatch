import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import PermitDiagram from "./PermitDiagram";

describe("PermitDiagram", () => {
  it("draws the boundary, providers, spend cap, and denied list from the document", () => {
    render(
      <PermitDiagram
        permit={{
          v: 1,
          llm: { providers: ["anthropic"], connection: "default" },
          spend: { per_run_cents: 50 },
        }}
      />,
    );
    expect(screen.getByText(/cannot go beyond this line/i)).toBeInTheDocument();
    expect(screen.getByText(/thinks with Claude \(Anthropic\)/)).toBeInTheDocument();
    expect(screen.getByText("max $0.50 / run")).toBeInTheDocument();
    expect(screen.getByText("Email")).toBeInTheDocument();
    expect(screen.getByText("The rest of the internet")).toBeInTheDocument();
    expect(screen.getByText(/can read/i)).toBeInTheDocument();
    expect(screen.getByText(/can write/i)).toBeInTheDocument();
  });

  it("says the agent cannot run when the permit grants no provider", () => {
    render(<PermitDiagram permit={{ v: 1 }} />);
    expect(screen.getByText(/may not call any model/)).toBeInTheDocument();
    expect(screen.getByText("monthly cap only")).toBeInTheDocument();
  });

  it("flags an unreadable permit as granting nothing", () => {
    render(<PermitDiagram permit={{ v: 99 }} />);
    expect(
      screen.getByText(/couldn't be read, so it grants nothing/),
    ).toBeInTheDocument();
  });
});
