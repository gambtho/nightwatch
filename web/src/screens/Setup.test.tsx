import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import Setup from "./Setup";
import { SessionProvider } from "../session";
import { mockApi } from "../test/helpers";

afterEach(() => {
  vi.unstubAllGlobals();
});

const catalog = {
  connectors: [
    {
      id: "google-calendar",
      name: "Google Calendar",
      description: "Read events, create events you approve.",
      auth_provider: "google",
      connected: true,
      ops: [
        {
          name: "list_events",
          description: "List upcoming events on one calendar.",
          effect: "read",
          scopes: [],
          args_schema: {},
        },
        {
          name: "create_event",
          description: "Create an event on a calendar you have approved.",
          effect: "write",
          scopes: [],
          args_schema: {},
          constraints: ["calendar_id"],
        },
      ],
    },
    {
      id: "slack",
      name: "Slack",
      description: "Post messages.",
      auth_provider: "slack",
      connected: false,
      ops: [],
    },
  ],
};

function renderSetup(routes: Parameters<typeof mockApi>[0]) {
  const fetchMock = mockApi({
    "GET /v1/me": {
      body: {
        user: { id: "u", email: "e", role: "owner" },
        tenant: { id: "t", name: "dev" },
      },
    },
    "GET /v1/catalog": { body: catalog },
    ...routes,
  });
  render(
    <MemoryRouter initialEntries={["/setup"]}>
      <SessionProvider>
        <Routes>
          <Route path="/setup" element={<Setup />} />
          <Route path="/workflows/:id/approve" element={<div>approve gate</div>} />
        </Routes>
      </SessionProvider>
    </MemoryRouter>,
  );
  return fetchMock;
}

describe("Setup", () => {
  it("labels itself as the developer path, not the product", async () => {
    renderSetup({});
    expect(await screen.findByText("Developer setup")).toBeInTheDocument();
    expect(screen.getByText(/not how Tomte is meant/)).toBeInTheDocument();
  });

  it("shows catalog connectors with their connection state", async () => {
    renderSetup({});
    expect(await screen.findByText("Google Calendar")).toBeInTheDocument();
    expect(screen.getByText("Connected")).toBeInTheDocument();
    expect(screen.getByText("Slack")).toBeInTheDocument();
    expect(screen.getByText("Not connected")).toBeInTheDocument();
  });

  it("asks for approved values when a constrained op is granted", async () => {
    renderSetup({});
    await screen.findByText("Google Calendar");
    expect(screen.queryByText(/Approved calendar id values/)).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("checkbox", { name: /create event/ }));
    expect(screen.getByText(/Approved calendar id values/)).toBeInTheDocument();
  });

  it("surfaces client-side errors before any request reaches the server", async () => {
    const fetchMock = renderSetup({});
    await screen.findByText("Google Calendar");
    await userEvent.click(screen.getByRole("checkbox", { name: /create event/ }));
    await userEvent.click(screen.getByRole("button", { name: "Create draft" }));

    expect(screen.getByText("Give the workflow a name.")).toBeInTheDocument();
    expect(screen.getByText("Step 1 needs text.")).toBeInTheDocument();
    expect(
      screen.getByText(/needs at least one approved calendar_id value/),
    ).toBeInTheDocument();
    const posts = fetchMock.mock.calls.filter(
      ([, init]) => (init as RequestInit | undefined)?.method === "POST",
    );
    expect(posts).toHaveLength(0);
  });

  it("creates the documented body and hands off to the approve gate", async () => {
    const fetchMock = renderSetup({
      "POST /v1/workflows": {
        status: 201,
        body: {
          workflow: { id: "wf9", name: "digest", created_at: "2026-08-31T00:00:00Z" },
          version: { number: 1, status: "draft" },
        },
      },
    });
    await screen.findByText("Google Calendar");

    await userEvent.type(screen.getByLabelText("Name"), "digest");
    await userEvent.type(
      screen.getByLabelText("Step 1 text"),
      "Look at last week's tickets.",
    );
    await userEvent.click(screen.getByRole("checkbox", { name: /create event/ }));
    await userEvent.type(
      screen.getByLabelText(/Approved calendar id values/),
      "primary, team",
    );
    await userEvent.click(screen.getByRole("button", { name: "Create draft" }));

    expect(await screen.findByText("approve gate")).toBeInTheDocument();

    const post = fetchMock.mock.calls.find(
      ([, init]) => (init as RequestInit | undefined)?.method === "POST",
    );
    expect(post).toBeDefined();
    const body = JSON.parse((post![1] as RequestInit).body as string) as unknown;
    expect(body).toEqual({
      name: "digest",
      steps: {
        v: 1,
        steps: [
          { id: "look-at-last-week-s-tickets", text: "Look at last week's tickets." },
        ],
      },
      permit: {
        v: 1,
        llm: { providers: ["anthropic"] },
        spend: { per_run_cents: 50 },
        connections: {
          "google-calendar": {
            kind: "http",
            ops: ["create_event"],
            resources: { create_event: { calendar_id: ["primary", "team"] } },
          },
        },
      },
    });
  });

  it("surfaces a server rejection and stays on the form", async () => {
    renderSetup({
      "POST /v1/workflows": {
        status: 400,
        body: { error: "permit: unknown connector" },
      },
    });
    await screen.findByText("Google Calendar");
    await userEvent.type(screen.getByLabelText("Name"), "digest");
    await userEvent.type(screen.getByLabelText("Step 1 text"), "Do the thing.");
    await userEvent.click(screen.getByRole("button", { name: "Create draft" }));

    expect(await screen.findByText(/permit: unknown connector/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create draft" })).toBeEnabled();
  });

  it("says so when the catalog is unreachable, and still lets LLM-only creation through", async () => {
    renderSetup({
      "GET /v1/catalog": { status: 500, body: { error: "catalog unavailable" } },
      "POST /v1/workflows": {
        status: 201,
        body: {
          workflow: { id: "wf3", name: "digest", created_at: "2026-08-31T00:00:00Z" },
          version: { number: 1, status: "draft" },
        },
      },
    });
    expect(
      await screen.findByText(/connector catalog couldn't be loaded/),
    ).toBeInTheDocument();

    await userEvent.type(screen.getByLabelText("Name"), "digest");
    await userEvent.type(screen.getByLabelText("Step 1 text"), "Do the thing.");
    await userEvent.click(screen.getByRole("button", { name: "Create draft" }));
    expect(await screen.findByText("approve gate")).toBeInTheDocument();
  });
});
