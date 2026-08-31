import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import Home from "./Home";
import { mockApi } from "../test/helpers";

afterEach(() => {
  vi.unstubAllGlobals();
});

const approvedVersion = {
  number: 1,
  status: "approved",
  steps: { v: 1, steps: [{ id: "job", text: "Summarize tickets." }] },
  permit: { v: 1, llm: { providers: ["anthropic"] }, spend: { per_run_cents: 50 } },
  rubric: {},
  schedule: { cron: "0 9 * * 1", tz: "UTC" },
  approved_at: "2026-08-30T00:00:00Z",
  created_at: "2026-08-30T00:00:00Z",
};

describe("Home", () => {
  it("lists workflows with last run, cost, schedule, and the reassurance line", async () => {
    mockApi({
      "GET /v1/workflows": {
        body: {
          workflows: [
            { id: "wf1", name: "weekly digest", created_at: "2026-08-30T00:00:00Z" },
          ],
        },
      },
      "GET /v1/workflows/wf1": {
        body: {
          workflow: {
            id: "wf1",
            name: "weekly digest",
            created_at: "2026-08-30T00:00:00Z",
          },
          versions: [approvedVersion],
        },
      },
      "GET /v1/workflows/wf1/runs": {
        body: {
          runs: [
            {
              id: "r1",
              workflow_id: "wf1",
              version: 1,
              status: "succeeded",
              fire_reason: "schedule",
              cost_cents: 3,
              created_at: "2026-08-31T09:00:00Z",
              output: "This week: billing questions.",
            },
          ],
        },
      },
    });

    render(
      <MemoryRouter>
        <Home />
      </MemoryRouter>,
    );

    expect(await screen.findByText("weekly digest")).toBeInTheDocument();
    expect(screen.getByText("✓ ran")).toBeInTheDocument();
    expect(screen.getByText(/\$0\.03/)).toBeInTheDocument();
    expect(screen.getByText(/Mondays at 9:00 AM/)).toBeInTheDocument();
    expect(screen.getByText(/not yet scored/i)).toBeInTheDocument();
    expect(screen.getByText(/you don't need to check this page/i)).toBeInTheDocument();
  });

  it("points a workflow with only a draft at the approval gate", async () => {
    mockApi({
      "GET /v1/workflows": {
        body: {
          workflows: [
            { id: "wf2", name: "renewals", created_at: "2026-08-30T00:00:00Z" },
          ],
        },
      },
      "GET /v1/workflows/wf2": {
        body: {
          workflow: { id: "wf2", name: "renewals", created_at: "2026-08-30T00:00:00Z" },
          versions: [{ ...approvedVersion, status: "draft", approved_at: null }],
        },
      },
      "GET /v1/workflows/wf2/runs": { body: { runs: [] } },
    });

    render(
      <MemoryRouter>
        <Home />
      </MemoryRouter>,
    );

    expect(await screen.findByText(/needs your approval/i)).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: /review what it's allowed to reach/i }),
    ).toHaveAttribute("href", "/workflows/wf2/approve");
  });

  it("shows the honest empty state", async () => {
    mockApi({ "GET /v1/workflows": { body: { workflows: [] } } });

    render(
      <MemoryRouter>
        <Home />
      </MemoryRouter>,
    );

    expect(
      await screen.findByText(/nothing on the night shift yet/i),
    ).toBeInTheDocument();
  });
});
