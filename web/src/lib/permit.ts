// The blast-radius view model, derived from the real permit document —
// never from prose. Permit v1 (docs/api/v1.md) governs LLM provider
// egress, per-run spend, and the connections map: per-connector grants at
// operation granularity. A document we cannot recognize at all renders as
// the fail-closed permit (matching the server: omitted/empty permit = no
// egress); an unparseable connections entry inside a recognized document
// stays visible, flagged unreadable, so reach is never under-reported.

export interface GrantedOp {
  name: string;
  /** Approved values per constrained arg field, from the permit itself. */
  resources: Record<string, string[]>;
}

export interface ConnectionGrant {
  connector: string;
  ops: GrantedOp[];
  /** True when part of this entry couldn't be parsed — the diagram must
   * say so rather than under-report reach. */
  unreadable?: boolean;
}

export interface PermitView {
  /** Model providers the run may call, in plain language. */
  providers: string[];
  /** Named credential the egress proxy injects. */
  connection: string;
  /** Per-run spend cap in cents; absent = only the tenant monthly cap. */
  spendPerRunCents?: number;
  /** Per-connector op grants from the permit's connections map. */
  grants: ConnectionGrant[];
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
  grants: [],
  recognized: false,
};

export function parsePermit(doc: unknown): PermitView {
  if (typeof doc !== "object" || doc === null || Array.isArray(doc)) {
    return FAIL_CLOSED;
  }
  const permit = doc as Record<string, unknown>;
  if (permit.v !== 1) return FAIL_CLOSED;

  const view: PermitView = {
    providers: [],
    connection: "default",
    grants: [],
    recognized: true,
  };

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

  // The connections map is server-validated at version-write time, so a
  // stored permit's entries are well-formed. Should one still fail to
  // parse, it must stay visible as unreadable — a dropped grant would
  // under-report the blast radius at the one gate. (Dropped resource
  // narrowing renders broader-than-written, the conservative direction.)
  const connections = permit.connections;
  if (
    typeof connections === "object" &&
    connections !== null &&
    !Array.isArray(connections)
  ) {
    for (const [connector, rawEntry] of Object.entries(connections)) {
      if (typeof rawEntry !== "object" || rawEntry === null || Array.isArray(rawEntry)) {
        view.grants.push({ connector, ops: [], unreadable: true });
        continue;
      }
      const entry = rawEntry as Record<string, unknown>;
      if (!Array.isArray(entry.ops)) {
        view.grants.push({ connector, ops: [], unreadable: true });
        continue;
      }
      const resources =
        typeof entry.resources === "object" &&
        entry.resources !== null &&
        !Array.isArray(entry.resources)
          ? (entry.resources as Record<string, unknown>)
          : {};
      let unreadable = false;
      const ops: GrantedOp[] = [];
      for (const op of entry.ops) {
        if (typeof op !== "string") {
          unreadable = true;
          continue;
        }
        const fields: Record<string, string[]> = {};
        const rawFields = resources[op];
        if (
          typeof rawFields === "object" &&
          rawFields !== null &&
          !Array.isArray(rawFields)
        ) {
          for (const [field, values] of Object.entries(rawFields)) {
            if (Array.isArray(values)) {
              fields[field] = values.filter((v): v is string => typeof v === "string");
            }
          }
        }
        ops.push({ name: op, resources: fields });
      }
      if (ops.length > 0 || unreadable) {
        view.grants.push({ connector, ops, ...(unreadable && { unreadable: true }) });
      }
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
