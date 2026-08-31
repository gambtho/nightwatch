// The blast-radius view model, derived from the real permit document —
// never from prose. Permit v1 (docs/api/v1.md) governs LLM provider egress
// and per-run spend only; the connector catalog is a reserved, empty map.
// Anything we cannot parse renders as the fail-closed permit, matching the
// server's own semantics (omitted/empty permit = no egress at all).

export interface PermitView {
  /** Model providers the run may call, in plain language. */
  providers: string[];
  /** Named credential the egress proxy injects. */
  connection: string;
  /** Per-run spend cap in cents; absent = only the tenant monthly cap. */
  spendPerRunCents?: number;
  /** True when the document was recognizably permit v1. */
  recognized: boolean;
}

const PROVIDER_LABELS: Record<string, string> = {
  anthropic: "Claude (Anthropic)",
  openai: "OpenAI",
  openrouter: "OpenRouter",
};

export function providerLabel(id: string): string {
  return PROVIDER_LABELS[id] ?? id;
}

// Everything outside the line, struck through. The first five are the UX
// spec's canonical list; the last is literally true of the egress proxy.
export const DENIED_BY_DEFAULT = [
  "Email",
  "Direct messages",
  "Deleting anything",
  "Payments",
  "The rest of the internet",
];

const FAIL_CLOSED: PermitView = {
  providers: [],
  connection: "default",
  recognized: false,
};

export function parsePermit(doc: unknown): PermitView {
  if (typeof doc !== "object" || doc === null || Array.isArray(doc)) {
    return FAIL_CLOSED;
  }
  const permit = doc as Record<string, unknown>;
  if (permit.v !== 1) return FAIL_CLOSED;

  const view: PermitView = { providers: [], connection: "default", recognized: true };

  const llm = permit.llm;
  if (typeof llm === "object" && llm !== null && !Array.isArray(llm)) {
    const { providers, connection } = llm as Record<string, unknown>;
    if (Array.isArray(providers)) {
      view.providers = providers.filter((p): p is string => typeof p === "string");
    }
    if (typeof connection === "string" && connection !== "") {
      view.connection = connection;
    }
  }

  const spend = permit.spend;
  if (typeof spend === "object" && spend !== null && !Array.isArray(spend)) {
    const perRun = (spend as Record<string, unknown>).per_run_cents;
    if (typeof perRun === "number" && Number.isInteger(perRun) && perRun > 0) {
      view.spendPerRunCents = perRun;
    }
  }

  return view;
}

export function spendLabel(view: PermitView): string {
  if (view.spendPerRunCents === undefined) return "monthly cap only";
  return `max $${(view.spendPerRunCents / 100).toFixed(2)} / run`;
}
