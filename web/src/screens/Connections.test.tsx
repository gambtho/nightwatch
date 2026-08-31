import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// The screen is tested against the seam; the fake store's latency and
// persistence are its own test's business (local/connections.test).
vi.mock("../local/connections", async (importOriginal) => {
  const real = await importOriginal<typeof import("../local/connections")>();
  return {
    ...real,
    stateOverlay: vi.fn(async () => ({})),
    connectWithToken: vi.fn(async () => ({ ok: true }) as const),
    disconnect: vi.fn(async () => {}),
    listMcpServers: vi.fn(async () => []),
    registerMcpServer: vi.fn(async () => ({ ok: true }) as const),
    removeMcpServer: vi.fn(async () => {}),
  };
});

import Connections from "./Connections";
import {
  connectWithToken,
  disconnect,
  listMcpServers,
  registerMcpServer,
  stateOverlay,
} from "../local/connections";
import { SessionProvider } from "../session";
import { mockApi } from "../test/helpers";

const catalog = {
  connectors: [
    {
      id: "slack",
      name: "Slack",
      description: "Post messages to your workspace.",
      auth_provider: "slack",
      connected: false,
      ops: [
        {
          name: "post_message",
          description: "Post a message.",
          effect: "write",
          scopes: [],
          args_schema: {},
          constraints: ["channel"],
        },
        {
          name: "list_channels",
          description: "List channels.",
          effect: "read",
          scopes: [],
          args_schema: {},
        },
      ],
    },
  ],
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(stateOverlay).mockResolvedValue({});
  vi.mocked(listMcpServers).mockResolvedValue([]);
  mockApi({
    "GET /v1/me": {
      body: {
        user: { id: "u", email: "e", role: "owner" },
        tenant: { id: "t", name: "dev" },
      },
    },
    "GET /v1/catalog": { body: catalog },
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function renderConnections() {
  return render(
    <MemoryRouter>
      <SessionProvider>
        <Connections />
      </SessionProvider>
    </MemoryRouter>,
  );
}

describe("Connections", () => {
  it("lists catalog connectors with state and each op's read/write effect", async () => {
    renderConnections();
    expect(await screen.findByText("Slack")).toBeInTheDocument();
    expect(screen.getByText("Not connected")).toBeInTheDocument();
    expect(screen.getByText("post message")).toBeInTheDocument();
    // The effect chips: one write op, one read op (plus the two legend
    // chips in the intro copy).
    expect(screen.getAllByText("write").length).toBeGreaterThanOrEqual(2);
    expect(screen.getAllByText("read").length).toBeGreaterThanOrEqual(2);
  });

  it("connects through the Slack capture card and lands Connected", async () => {
    const user = userEvent.setup();
    vi.mocked(connectWithToken).mockImplementation(async () => {
      vi.mocked(stateOverlay).mockResolvedValue({ slack: "ok" });
      return { ok: true };
    });
    renderConnections();

    await user.click(await screen.findByRole("button", { name: /connect…/i }));
    expect(screen.getByText(/create app/i)).toBeInTheDocument();

    // The shape check catches a wrong paste instantly.
    const input = screen.getByLabelText(/token/i);
    await user.type(input, "sk-ant-oops");
    expect(screen.getByText(/should start with xoxb-/i)).toBeInTheDocument();
    await user.clear(input);

    await user.type(input, "xoxb-abc");
    await user.click(screen.getByRole("button", { name: /^connect$/i }));

    expect(await screen.findByText("Connected")).toBeInTheDocument();
    expect(connectWithToken).toHaveBeenCalledWith("slack", "xoxb-abc");
  });

  it("offers a fresh-key paste for needs_reauth", async () => {
    vi.mocked(stateOverlay).mockResolvedValue({ slack: "needs_reauth" });
    renderConnections();
    expect(await screen.findByText(/needs a fresh key/i)).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /paste a fresh key/i }),
    ).toBeInTheDocument();
  });

  it("disconnects only after naming the consequence", async () => {
    const user = userEvent.setup();
    vi.mocked(stateOverlay).mockResolvedValue({ slack: "ok" });
    renderConnections();

    await user.click(await screen.findByRole("button", { name: /disconnect/i }));
    expect(
      screen.getByText(/will start failing until it's reconnected/i),
    ).toBeInTheDocument();
    expect(disconnect).not.toHaveBeenCalled();

    // The header button is gone while confirming; the confirm button remains.
    await user.click(screen.getByRole("button", { name: /^disconnect$/i }));
    expect(disconnect).toHaveBeenCalledWith("slack");
  });

  it("registers an MCP server", async () => {
    const user = userEvent.setup();
    renderConnections();

    await user.click(await screen.findByRole("button", { name: /add an mcp server/i }));
    await user.type(screen.getByLabelText(/name/i), "my calendar");
    await user.type(screen.getByLabelText(/address/i), "https://mcp.example.com");
    await user.type(screen.getByLabelText(/^key$/i), "vendor-key");
    await user.click(screen.getByRole("button", { name: /register/i }));

    expect(registerMcpServer).toHaveBeenCalledWith(
      "my calendar",
      "https://mcp.example.com",
      "vendor-key",
    );
  });

  it("says so when the catalog is unreachable", async () => {
    mockApi({
      "GET /v1/me": {
        body: {
          user: { id: "u", email: "e", role: "owner" },
          tenant: { id: "t", name: "dev" },
        },
      },
      "GET /v1/catalog": { status: 500, body: { error: "boom" } },
    });
    renderConnections();
    expect(await screen.findByText(/catalog couldn't be loaded/i)).toBeInTheDocument();
  });
});
