// Wire types for the Nightshift /v1 API (docs/api/v1.md). Field names are
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
