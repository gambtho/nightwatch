export type SystemId = "zendesk" | "slack" | "gmail" | "gcal" | "drive" | "github";

export type Access = "read" | "write";

export interface Capability {
  id: string;
  system: SystemId;
  label: string;
  access: Access;
  detail?: string;
}

export interface Permit {
  capabilities: Capability[];
  denied: string[];
  maxCostCents: number;
}

export interface RubricRule {
  id: string;
  text: string;
}

export interface WorkflowStep {
  id: string;
  text: string;
}

export interface Schedule {
  label: string;
  timezone: string;
}

export type RunStatus = "ok" | "failed" | "paused";

export interface RuleResult {
  ruleId: string;
  passed: boolean;
}

export interface Run {
  id: string;
  at: string;
  status: RunStatus;
  costCents: number;
  ruleResults: RuleResult[];
  summary: string;
}

export interface Workflow {
  id: string;
  name: string;
  schedule: Schedule;
  steps: WorkflowStep[];
  permit: Permit;
  rubric: RubricRule[];
  runs: Run[];
  paused: boolean;
}

export interface Verdict {
  can: string[];
  cannot: string[];
  access: Capability[];
}
