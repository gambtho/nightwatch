import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

// The screen is tested against the seam, not the fake store — the fake's
// latency and localStorage are its own test's business (local/config.test).
vi.mock("../local/config", () => ({
  getLocalConfig: vi.fn(async () => ({
    endpoint: null,
    monthly_budget_cents: null,
    autostart: true,
  })),
  saveEndpoint: vi.fn(async () => {}),
  saveBudget: vi.fn(async () => {}),
  verifyEndpointKey: vi.fn(async () => ({ ok: true }) as const),
  SUGGESTED_BUDGET_CENTS: 1000,
}));

import FirstRun from "./FirstRun";
import { saveBudget, saveEndpoint, verifyEndpointKey } from "../local/config";

beforeEach(() => {
  vi.clearAllMocks();
});

function renderFirstRun() {
  return render(
    <MemoryRouter initialEntries={["/welcome"]}>
      <Routes>
        <Route path="/welcome" element={<FirstRun />} />
        <Route path="/build" element={<div>build page</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("FirstRun", () => {
  it("offers all five presets plus another-service and on-this-computer", () => {
    renderFirstRun();
    for (const label of [
      "Anthropic",
      "OpenAI",
      "OpenRouter",
      "GitHub Models",
      "Azure AI Foundry",
      "Another service",
      "On this computer",
    ]) {
      expect(
        screen.getByRole("button", { name: new RegExp(`^${label}`) }),
      ).toBeInTheDocument();
    }
  });

  it("walks preset → disclosed verify → budget → build", async () => {
    const user = userEvent.setup();
    renderFirstRun();

    await user.click(screen.getByRole("button", { name: /Anthropic/ }));
    // The metered test call is named before it happens.
    expect(screen.getByText(/one tiny test call/i)).toBeInTheDocument();
    await user.type(screen.getByLabelText(/key/i), "sk-ant-abc123");
    await user.click(screen.getByRole("button", { name: /check the key/i }));

    expect(await screen.findByText(/set a monthly budget/i)).toBeInTheDocument();
    expect(verifyEndpointKey).toHaveBeenCalled();
    expect(saveEndpoint).toHaveBeenCalledWith({
      kind: "anthropic",
      preset: "anthropic",
      base_url: "https://api.anthropic.com",
      local: false,
    });

    // Suggested default is prefilled; accept it.
    expect(screen.getByLabelText(/monthly budget/i)).toHaveValue("10");
    await user.click(screen.getByRole("button", { name: /set budget and start/i }));
    expect(await screen.findByText("build page")).toBeInTheDocument();
    expect(saveBudget).toHaveBeenCalledWith(1000);
  });

  it("skips key and budget for on-this-computer — free by classification", async () => {
    const user = userEvent.setup();
    renderFirstRun();

    await user.click(screen.getByRole("button", { name: /On this computer/ }));
    expect(screen.getByText(/runs are free/i)).toBeInTheDocument();
    await user.type(screen.getByLabelText(/address/i), "http://localhost:11434/v1");
    await user.click(screen.getByRole("button", { name: /use this computer/i }));

    expect(await screen.findByText("build page")).toBeInTheDocument();
    expect(saveEndpoint).toHaveBeenCalledWith({
      kind: "openai_compatible",
      preset: "local",
      base_url: "http://localhost:11434/v1",
      local: true,
    });
    expect(saveBudget).not.toHaveBeenCalled();
  });

  it("refuses a remote address on the on-this-computer path", async () => {
    const user = userEvent.setup();
    renderFirstRun();

    await user.click(screen.getByRole("button", { name: /On this computer/ }));
    await user.type(screen.getByLabelText(/address/i), "https://api.example.com/v1");
    await user.click(screen.getByRole("button", { name: /use this computer/i }));

    expect(
      await screen.findByText(/only accepts an address on this computer/i),
    ).toBeInTheDocument();
    expect(saveEndpoint).not.toHaveBeenCalled();
  });

  it("collects the per-resource endpoint URL for Azure before the key passes", async () => {
    const user = userEvent.setup();
    renderFirstRun();

    await user.click(screen.getByRole("button", { name: /Azure AI Foundry/ }));
    const urlInput = screen.getByLabelText(/your resource's endpoint url/i);
    await user.type(screen.getByLabelText(/^key$/i), "abc123");
    await user.click(screen.getByRole("button", { name: /check the key/i }));
    // No URL yet: the verify reports the address problem instead of storing.
    expect(await screen.findByText(/enter the service's address/i)).toBeInTheDocument();

    await user.type(urlInput, "https://my-res.services.ai.azure.com/models");
    await user.click(screen.getByRole("button", { name: /check the key/i }));
    expect(await screen.findByText(/set a monthly budget/i)).toBeInTheDocument();
    expect(saveEndpoint).toHaveBeenCalledWith({
      kind: "openai_compatible",
      preset: "azure",
      base_url: "https://my-res.services.ai.azure.com/models",
      local: false,
    });
  });
});
