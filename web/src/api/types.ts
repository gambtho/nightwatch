// Wire types for the Tomte /v1 API (docs/api/v1.md). Field names are
// the server's snake_case, verbatim. Sparse run fields are omitted by the
// server until the run reaches that stage — they are optional here, never null.

export interface Me {
  user: { id: string; email: string; role: string };
  tenant: { id: string; name: string };
}

export interface Workflow {
  id: string;
  name: string;
  created_at: string;
}

export type VersionStatus = "draft" | "approved" | "superseded";

export interface Version {
  number: number;
  status: VersionStatus;
  steps: unknown;
  permit: unknown;
  rubric: unknown;
  schedule?: ScheduleDoc;
  approved_at: string | null;
  created_at: string;
}

export interface ScheduleDoc {
  cron: string;
  tz: string;
}

export type RunStatus = "pending" | "running" | "succeeded" | "failed";

export interface Run {
  id: string;
  workflow_id: string;
  version: number;
  status: RunStatus;
  fire_reason: "manual" | "schedule";
  fire_time?: string;
  started_at?: string;
  finished_at?: string;
  tokens_in?: number;
  tokens_out?: number;
  cost_cents?: number;
  error_kind?: string;
  error_msg?: string;
  output?: string;
  created_at: string;
}

export interface RunEvent {
  type: string;
  payload: unknown;
  created_at: string;
}

// The connector catalog (GET /v1/catalog): what the platform can reach,
// with plain-language copy written once, for this surface. `connected` is
// a plain boolean today; a richer status field joins it in connectors
// phase 2 — read it through lib/catalog.ts, not directly.

export interface CatalogOp {
  name: string;
  description: string;
  effect: "read" | "write";
  scopes: string[];
  args_schema: unknown;
  /** Arg fields whose values the permit must pin to an approved list. */
  constraints?: string[];
}

export interface CatalogConnector {
  id: string;
  name: string;
  description: string;
  auth_provider: string;
  connected: boolean;
  ops: CatalogOp[];
}

// Documents the create form writes (docs/api/v1.md).

export interface StepsDoc {
  v: 1;
  steps: { id: string; text: string }[];
}

export interface PermitConnectionDoc {
  kind: "http";
  connection?: string;
  ops: string[];
  resources?: Record<string, Record<string, string[]>>;
}

export interface PermitDoc {
  v: 1;
  llm?: { providers?: string[]; connection?: string };
  spend?: { per_run_cents: number };
  connections?: Record<string, PermitConnectionDoc>;
}

export interface CreateWorkflowBody {
  name: string;
  steps: StepsDoc;
  permit?: PermitDoc;
  schedule?: ScheduleDoc;
}
