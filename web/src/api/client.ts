import type {
  CatalogConnector,
  CreateWorkflowBody,
  Me,
  Run,
  RunEvent,
  Version,
  Workflow,
} from "./types";

// Same-origin fetch client. The server authenticates with the
// __Host-tomte_session cookie and 403s any present-but-foreign Origin on
// mutating routes, so the app must be served from (or proxied through)
// the configured public origin — see vite.config.ts for the dev topology.

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export function isAuthError(err: unknown): boolean {
  return err instanceof ApiError && err.status === 401;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    credentials: "same-origin",
    headers: init?.body ? { "Content-Type": "application/json" } : undefined,
    ...init,
  });
  if (!res.ok) {
    // statusText is often empty over HTTP/2, so always fall back to the code.
    let message = res.statusText || `HTTP ${res.status}`;
    try {
      const body = (await res.json()) as { error?: string };
      if (body.error) message = body.error;
    } catch {
      // non-JSON error body; keep the fallback message
    }
    throw new ApiError(res.status, message);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export function getMe(): Promise<Me> {
  return request<Me>("/v1/me");
}

export function requestMagicLink(email: string, next?: string): Promise<void> {
  return request("/v1/auth/magic-link", {
    method: "POST",
    body: JSON.stringify(next ? { email, next } : { email }),
  });
}

export function logout(): Promise<void> {
  return request("/v1/auth/logout", { method: "POST" });
}

export function listWorkflows(): Promise<{ workflows: Workflow[] }> {
  return request("/v1/workflows");
}

export function getWorkflow(
  id: string,
): Promise<{ workflow: Workflow; versions: Version[] }> {
  return request(`/v1/workflows/${id}`);
}

export function listRuns(workflowId: string): Promise<{ runs: Run[] }> {
  return request(`/v1/workflows/${workflowId}/runs`);
}

export function getRunEvents(runId: string): Promise<{ events: RunEvent[] }> {
  return request(`/v1/runs/${runId}/events`);
}

export function approveVersion(
  workflowId: string,
  version: number,
): Promise<{ version: Version }> {
  return request(`/v1/workflows/${workflowId}/versions/${version}/approve`, {
    method: "POST",
  });
}

export function fireRun(workflowId: string): Promise<{ run: Run }> {
  return request(`/v1/workflows/${workflowId}/runs`, { method: "POST" });
}

export function createWorkflow(
  body: CreateWorkflowBody,
): Promise<{ workflow: Workflow; version: Version }> {
  return request("/v1/workflows", { method: "POST", body: JSON.stringify(body) });
}

export function getCatalog(): Promise<{ connectors: CatalogConnector[] }> {
  return request("/v1/catalog");
}
