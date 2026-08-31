import { beforeEach, describe, expect, it } from "vitest";
import {
  getLocalConfig,
  resetLocalStoreForTests,
  saveBudget,
  saveEndpoint,
  savePrice,
  unpricedModels,
  verifyEndpointKey,
} from "./config";
import { presetById, type EndpointRecord } from "./endpoints";

// The fake seam's own contract: round-trips and the pricing-gate shape
// the settings switch relies on. Replaced wholesale when P1's API lands.

beforeEach(() => {
  resetLocalStoreForTests();
});

const custom: EndpointRecord = {
  kind: "openai_compatible",
  preset: "custom",
  base_url: "https://api.example.com/v1",
  local: false,
};

describe("local config store", () => {
  it("round-trips endpoint, budget, and defaults autostart on", async () => {
    expect((await getLocalConfig()).endpoint).toBeNull();
    await saveEndpoint(custom);
    await saveBudget(1000);
    const config = await getLocalConfig();
    expect(config.endpoint).toEqual(custom);
    expect(config.monthly_budget_cents).toBe(1000);
    expect(config.autostart).toBe(true);
  });
});

describe("verifyEndpointKey (fake)", () => {
  it("fails an empty paste and the demo bad key, passes otherwise", async () => {
    const preset = presetById("anthropic");
    expect((await verifyEndpointKey(preset, "")).ok).toBe(false);
    expect((await verifyEndpointKey(preset, "sk-ant-x-bad")).ok).toBe(false);
    expect((await verifyEndpointKey(preset, "sk-ant-x")).ok).toBe(true);
  });
});

describe("pricing gate (fake)", () => {
  it("asks for a price only on a custom endpoint, until one is stored for it", async () => {
    const localRec: EndpointRecord = {
      kind: "openai_compatible",
      preset: "local",
      base_url: "http://localhost:11434/v1",
      local: true,
    };
    expect(await unpricedModels(localRec)).toEqual([]);

    const unpriced = await unpricedModels(custom);
    expect(unpriced).toHaveLength(1);

    await savePrice(custom.base_url, unpriced[0]!, 25, 125);
    expect(await unpricedModels(custom)).toEqual([]);

    // Keyed by (endpoint, model): a different endpoint never inherits it.
    expect(
      await unpricedModels({ ...custom, base_url: "https://other.example.com" }),
    ).toHaveLength(1);
  });
});
