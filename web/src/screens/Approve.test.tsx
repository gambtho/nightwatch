import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import Approve from "./Approve";
import { SessionProvider } from "../session";
import { mockApi } from "../test/helpers";

afterEach(() => {
  vi.unstubAllGlobals();
});

const draft = {
  number: 2,
  status: "draft",
  steps: {
    v: 1,
    steps: [
      { id: "gather", text: "Look at last week's tickets." },
      { id: "post", text: "Post a short digest." },
    ],
  },
  permit: { v: 1, llm: { providers: ["anthropic"] }, spend: { per_run_cents: 50 } },
  rubric: {},
  schedule: { cron: "0 9 * * 1", tz: "UTC" },
  approved_at: null,
  created_at: "2026-08-30T00:00:00Z",
};

function renderApprove(routes: Parameters<typeof mockApi>[0]) {
  mockApi({
    "GET /v1/me": {
      body: {
        user: { id: "u", email: "e", role: "owner" },
        tenant: { id: "t", name: "dev" },
      },
    },
    ...routes,
  });
  return render(
    <MemoryRouter initialEntries={["/workflows/wf1/approve"]}>
      <SessionProvider>
        <Routes>
          <Route path="/workflows/:id/approve" element={<Approve />} />
          <Route path="/workflows/:id" element={<div>detail page</div>} />
        </Routes>
      </SessionProvider>
    </MemoryRouter>,
  );
}

const workflowResponse = {
  body: {
    workflow: { id: "wf1", name: "weekly digest", created_at: "2026-08-30T00:00:00Z" },
    versions: [draft],
  },
};

describe("Approve", () => {
  it("shows the steps, the blast radius, and the schedule for the draft", async () => {
    renderApprove({ "GET /v1/workflows/wf1": workflowResponse });

    expect(await screen.findByText("weekly digest")).toBeInTheDocument();
    expect(screen.getByText(/Mondays at 9:00 AM/)).toBeInTheDocument();
    expect(screen.getByText("Look at last week's tickets.")).toBeInTheDocument();
    expect(screen.getByText(/cannot go beyond this line/i)).toBeInTheDocument();
    expect(screen.getByText("max $0.50 / run")).toBeInTheDocument();
    expect(screen.getByText(/approving locks exactly this/i)).toBeInTheDocument();
  });

  it("approves the draft version and moves on", async () => {
    renderApprove({
      "GET /v1/workflows/wf1": workflowResponse,
      "POST /v1/workflows/wf1/versions/2/approve": {
        body: { version: { ...draft, status: "approved" } },
      },
    });

    await screen.findByText("weekly digest");
    await userEvent.click(screen.getByRole("button", { name: "Approve" }));

    expect(await screen.findByText("detail page")).toBeInTheDocument();
  });

  it("surfaces an approval failure and stays on the gate", async () => {
    renderApprove({
      "GET /v1/workflows/wf1": workflowResponse,
      "POST /v1/workflows/wf1/versions/2/approve": {
        status: 404,
        body: { error: "version not in draft status" },
      },
    });

    await screen.findByText("weekly digest");
    await userEvent.click(screen.getByRole("button", { name: "Approve" }));

    expect(await screen.findByText(/version not in draft status/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve" })).toBeEnabled();
  });

  it("says so when nothing awaits approval", async () => {
    renderApprove({
      "GET /v1/workflows/wf1": {
        body: {
          workflow: {
            id: "wf1",
            name: "weekly digest",
            created_at: "2026-08-30T00:00:00Z",
          },
          versions: [{ ...draft, status: "approved" }],
        },
      },
    });

    expect(
      await screen.findByText(/nothing here is waiting for approval/i),
    ).toBeInTheDocument();
  });
});

describe("Approve — unreadable steps", () => {
  it("says the steps couldn't be read instead of hiding the section", async () => {
    renderApprove({
      "GET /v1/workflows/wf1": {
        body: {
          workflow: {
            id: "wf1",
            name: "weekly digest",
            created_at: "2026-08-30T00:00:00Z",
          },
          versions: [{ ...draft, steps: { v: 99 } }],
        },
      },
    });

    expect(
      await screen.findByText(
        /steps couldn't be read. Don't approve what you can't see/i,
      ),
    ).toBeInTheDocument();
  });
});

describe("Approve — sleeping-machine guidance", () => {
  it("carries the promise and the always-on options next to the schedule", async () => {
    renderApprove({ "GET /v1/workflows/wf1": workflowResponse });

    expect(
      await screen.findByText(/works while your computer is on/i),
    ).toBeInTheDocument();
    expect(screen.getByText(/once, not twelve times/i)).toBeInTheDocument();
    // The three always-on options, in the spec's order.
    const items = screen
      .getAllByRole("listitem")
      .map((li) => li.textContent ?? "")
      .filter((t) => /keep this computer awake|stays on|hosted, always-on/i.test(t));
    expect(items).toHaveLength(3);
    expect(items[0]).toMatch(/keep this computer awake/i);
    expect(items[1]).toMatch(/machine that stays on/i);
    expect(items[2]).toMatch(/doesn't today/i);
  });

  it("stays quiet when the draft has no schedule", async () => {
    const { schedule: _schedule, ...unscheduled } = draft;
    renderApprove({
      "GET /v1/workflows/wf1": {
        body: {
          workflow: {
            id: "wf1",
            name: "weekly digest",
            created_at: "2026-08-30T00:00:00Z",
          },
          versions: [unscheduled],
        },
      },
    });

    expect(await screen.findByText("weekly digest")).toBeInTheDocument();
    expect(screen.queryByText(/works while your computer is on/i)).toBeNull();
  });
});
