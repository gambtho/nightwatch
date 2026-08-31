import type { EndpointRecord, EndpointPreset } from "./endpoints";

// ── FAKE SEAM ────────────────────────────────────────────────────────
// The thin interface where P1's API lands. Every function here becomes
// one client call when the server grows the endpoint record, the
// user-entered price store, and the budget rename:
//
//   getLocalConfig / saveEndpoint  → the P1 endpoint record (+ the
//     switch-is-a-governance-act event; approval records the endpoint)
//   verifyEndpointKey              → the disclosed, metered one-call
//     verify through the proxy path (its cost is recorded so it counts
//     against the month once the budget exists)
//   saveBudget                     → tenant monthly budget (the meter
//     already enforces it; only the copy is new)
//   setAutostart                   → the desktop shell's autostart toggle
//   unpricedModels / savePrice     → the P1 pricing gate re-check and
//     the per-(endpoint, model) user-entered price
//
// Until then everything is localStorage plus a little latency, so the
// screens exercise real async states. Delete this store with the swap.
// ─────────────────────────────────────────────────────────────────────

export interface LocalConfig {
  endpoint: EndpointRecord | null;
  monthly_budget_cents: number | null;
  autostart: boolean;
}

export const SUGGESTED_BUDGET_CENTS = 1000;

const CONFIG_KEY = "tomte.local-config.v1";
const PRICES_KEY = "tomte.local-prices.v1";

// The platform-chosen run model (decision 9); P1's pricing re-check runs
// over every approved version's compiled (provider, model) instead.
const RUN_MODEL = "claude-haiku-4-5";

function delay(ms = 250): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// localStorage in the browser; an in-memory map where it's missing (the
// jsdom test environment exposes no localStorage). Both are throwaway —
// the whole store goes when P1's API replaces this seam.
const memory = new Map<string, string>();

function hasLocalStorage(): boolean {
  try {
    return typeof localStorage !== "undefined" && localStorage !== null;
  } catch {
    return false;
  }
}

function readJson<T>(key: string, fallback: T): T {
  try {
    const raw = hasLocalStorage() ? localStorage.getItem(key) : memory.get(key);
    return raw ? (JSON.parse(raw) as T) : fallback;
  } catch {
    return fallback;
  }
}

function writeJson(key: string, value: unknown): void {
  const raw = JSON.stringify(value);
  if (hasLocalStorage()) {
    localStorage.setItem(key, raw);
  } else {
    memory.set(key, raw);
  }
}

/** Clears the fake store — for tests only. */
export function resetLocalStoreForTests(): void {
  memory.clear();
  if (hasLocalStorage()) {
    localStorage.removeItem(CONFIG_KEY);
    localStorage.removeItem(PRICES_KEY);
  }
}

const emptyConfig: LocalConfig = {
  endpoint: null,
  monthly_budget_cents: null,
  autostart: true,
};

export async function getLocalConfig(): Promise<LocalConfig> {
  await delay(120);
  return { ...emptyConfig, ...readJson<Partial<LocalConfig>>(CONFIG_KEY, {}) };
}

export async function saveEndpoint(endpoint: EndpointRecord): Promise<void> {
  await delay();
  const config = readJson<Partial<LocalConfig>>(CONFIG_KEY, {});
  writeJson(CONFIG_KEY, { ...emptyConfig, ...config, endpoint });
}

export async function saveBudget(cents: number): Promise<void> {
  await delay();
  const config = readJson<Partial<LocalConfig>>(CONFIG_KEY, {});
  writeJson(CONFIG_KEY, { ...emptyConfig, ...config, monthly_budget_cents: cents });
}

export async function setAutostart(on: boolean): Promise<void> {
  await delay(120);
  const config = readJson<Partial<LocalConfig>>(CONFIG_KEY, {});
  writeJson(CONFIG_KEY, { ...emptyConfig, ...config, autostart: on });
}

export type VerifyResult = { ok: true } | { ok: false; message: string };

/**
 * The disclosed test call. Fake behavior: shape-checked keys pass after a
 * pause; any key ending in "-bad" fails, so the failure state is
 * reachable in demos.
 */
export async function verifyEndpointKey(
  preset: EndpointPreset,
  key: string,
): Promise<VerifyResult> {
  await delay(700);
  if (key.trim() === "") return { ok: false, message: "Paste a key first." };
  if (key.endsWith("-bad")) {
    return {
      ok: false,
      message: `${preset.label} said this key isn't valid. Copy it again and re-paste — a character may be missing.`,
    };
  }
  return { ok: true };
}

type PriceTable = Record<
  string,
  Record<string, { in_per_m_cents: number; out_per_m_cents: number }>
>;

/**
 * Models that would run unpriced against this endpoint. Presets ship in
 * the bundled price table and local endpoints are $0 by classification,
 * so only a custom endpoint asks — unless a user-entered price is
 * already stored for (endpoint, model).
 */
export async function unpricedModels(endpoint: EndpointRecord): Promise<string[]> {
  await delay(150);
  if (endpoint.local || endpoint.preset !== "custom") return [];
  const prices = readJson<PriceTable>(PRICES_KEY, {});
  return prices[endpoint.base_url]?.[RUN_MODEL] ? [] : [RUN_MODEL];
}

/** Stores a user-entered price keyed by (endpoint base URL, model). */
export async function savePrice(
  baseUrl: string,
  model: string,
  inPerMCents: number,
  outPerMCents: number,
): Promise<void> {
  await delay(150);
  const prices = readJson<PriceTable>(PRICES_KEY, {});
  prices[baseUrl] = {
    ...prices[baseUrl],
    [model]: { in_per_m_cents: inPerMCents, out_per_m_cents: outPerMCents },
  };
  writeJson(PRICES_KEY, prices);
}
