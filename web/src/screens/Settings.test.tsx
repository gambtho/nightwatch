import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../local/config", () => ({
  getLocalConfig: vi.fn(),
  saveEndpoint: vi.fn(async () => {}),
  saveBudget: vi.fn(async () => {}),
  setAutostart: vi.fn(async () => {}),
  savePrice: vi.fn(async () => {}),
  unpricedModels: vi.fn(async () => []),
  verifyEndpointKey: vi.fn(async () => ({ ok: true }) as const),
  SUGGESTED_BUDGET_CENTS: 1000,
}));

import Settings from "./Settings";
import {
  getLocalConfig,
  saveBudget,
  saveEndpoint,
  savePrice,
  setAutostart,
  unpricedModels,
} from "../local/config";
import { SessionProvider } from "../session";
import { mockApi } from "../test/helpers";

const anthropicEndpoint = {
  kind: "anthropic" as const,
  preset: "anthropic" as const,
  base_url: "https://api.anthropic.com",
  local: false,
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(getLocalConfig).mockResolvedValue({
    endpoint: anthropicEndpoint,
    monthly_budget_cents: 1000,
    autostart: true,
  });
  vi.mocked(unpricedModels).mockResolvedValue([]);
  mockApi({
    "GET /v1/me": {
      body: {
        user: { id: "u", email: "e", role: "owner" },
        tenant: { id: "t", name: "dev" },
      },
    },
    "GET /v1/workflows": {
      body: {
        workflows: [
          { id: "w1", name: "a", created_at: "2026-08-30T00:00:00Z" },
          { id: "w2", name: "b", created_at: "2026-08-30T00:00:00Z" },
          { id: "w3", name: "c", created_at: "2026-08-30T00:00:00Z" },
        ],
      },
    },
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function renderSettings() {
  return render(
    <MemoryRouter>
      <SessionProvider>
        <Settings />
      </SessionProvider>
    </MemoryRouter>,
  );
}

describe("Settings", () => {
  it("shows the endpoint, the budget, and autostart", async () => {
    renderSettings();
    expect(await screen.findByText("Anthropic")).toBeInTheDocument();
    expect(screen.getByText(/\$10\.00 per month/)).toBeInTheDocument();
    expect(
      screen.getByRole("checkbox", { name: /start tomte when you log in/i }),
    ).toBeChecked();
  });

  it("switches endpoints only through a confirmation naming the affected workflows", async () => {
    const user = userEvent.setup();
    renderSettings();

    await user.click(await screen.findByRole("button", { name: /switch service/i }));
    await user.click(screen.getByRole("button", { name: /OpenRouter/ }));
    await user.type(screen.getByLabelText(/key/i), "sk-or-abc");
    await user.click(screen.getByRole("button", { name: /check the key/i }));

    // The governance moment: the switch names what it changes.
    expect(
      await screen.findByText(/your 3 workflows will now run against/i),
    ).toBeInTheDocument();
    expect(screen.getByText("OpenRouter")).toBeInTheDocument();
    expect(saveEndpoint).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: /^switch$/i }));
    expect(await screen.findByText(/openrouter\.ai/)).toBeInTheDocument();
    expect(saveEndpoint).toHaveBeenCalledWith({
      kind: "openai_compatible",
      preset: "openrouter",
      base_url: "https://openrouter.ai/api/v1",
      local: false,
    });
  });

  it("holds the switch behind the price form when the new endpoint is unpriced", async () => {
    vi.mocked(unpricedModels).mockResolvedValue(["claude-haiku-4-5"]);
    const user = userEvent.setup();
    renderSettings();

    await user.click(await screen.findByRole("button", { name: /switch service/i }));
    await user.click(screen.getByRole("button", { name: /Another service/ }));
    await user.type(screen.getByLabelText(/address/i), "https://api.example.com/v1");
    await user.type(screen.getByLabelText(/key/i), "whatever");
    await user.click(screen.getByRole("button", { name: /check the key/i }));

    expect(
      await screen.findByText(/doesn't know this service's prices/i),
    ).toBeInTheDocument();
    const switchBtn = screen.getByRole("button", { name: /^switch$/i });
    expect(switchBtn).toBeDisabled();

    await user.type(screen.getByLabelText(/million tokens in/i), "0.25");
    await user.type(screen.getByLabelText(/million tokens out/i), "1.25");
    expect(switchBtn).toBeEnabled();
    await user.click(switchBtn);

    expect(await screen.findByText(/api\.example\.com/)).toBeInTheDocument();
    expect(savePrice).toHaveBeenCalledWith(
      "https://api.example.com/v1",
      "claude-haiku-4-5",
      25,
      125,
    );
    expect(saveEndpoint).toHaveBeenCalled();
  });

  it("edits the budget in dollars", async () => {
    const user = userEvent.setup();
    renderSettings();

    await user.click(await screen.findByRole("button", { name: /change/i }));
    const input = screen.getByLabelText(/monthly budget/i);
    await user.clear(input);
    await user.type(input, "25");
    await user.click(screen.getByRole("button", { name: /save/i }));

    expect(await screen.findByText(/\$25\.00 per month/)).toBeInTheDocument();
    expect(saveBudget).toHaveBeenCalledWith(2500);
  });

  it("toggles autostart", async () => {
    const user = userEvent.setup();
    renderSettings();

    await user.click(
      await screen.findByRole("checkbox", { name: /start tomte when you log in/i }),
    );
    expect(setAutostart).toHaveBeenCalledWith(false);
  });

  it("points at first run when nothing is configured yet", async () => {
    vi.mocked(getLocalConfig).mockResolvedValue({
      endpoint: null,
      monthly_budget_cents: null,
      autostart: true,
    });
    renderSettings();
    expect(
      await screen.findByRole("link", { name: /choose where your ai runs/i }),
    ).toHaveAttribute("href", "/welcome");
  });
});
