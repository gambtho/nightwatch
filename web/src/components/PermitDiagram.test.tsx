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

  it("fills the read/write columns from connector grants and the catalog", () => {
    render(
      <PermitDiagram
        permit={{
          v: 1,
          llm: { providers: ["anthropic"] },
          connections: {
            "google-calendar": {
              kind: "http",
              ops: ["list_events", "create_event"],
              resources: { create_event: { calendar_id: ["primary"] } },
            },
          },
        }}
        catalog={[
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
        ]}
      />,
    );
    expect(screen.getByText(/Google Calendar · list events/)).toBeInTheDocument();
    expect(screen.getByText(/Google Calendar · create event/)).toBeInTheDocument();
    expect(screen.getByText("only calendar id: primary")).toBeInTheDocument();
    expect(screen.queryByText(/no systems are connected/)).not.toBeInTheDocument();
    expect(screen.queryByText(/cannot change anything of yours/)).not.toBeInTheDocument();
  });

  it("keeps a granted op visible, marked, when no catalog can describe it", () => {
    render(
      <PermitDiagram
        permit={{
          v: 1,
          connections: { slack: { kind: "http", ops: ["post_message"] } },
        }}
      />,
    );
    expect(screen.getByText(/slack · post message/)).toBeInTheDocument();
    expect(screen.getByText(/not in today's catalog/)).toBeInTheDocument();
    // Unconfirmed ops sit in the write column — the conservative reading.
    expect(screen.getByText(/no systems are connected/)).toBeInTheDocument();
  });
});
