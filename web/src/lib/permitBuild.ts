import type { CatalogConnector, PermitConnectionDoc, PermitDoc } from "../api/types";
import { findOp } from "./catalog";

// Builds the permit v1 document from the setup form's model. The server
// (permit.Parse + validateConnections in server/internal/httpapi) is the
// authority; this mirrors its rules so the user sees the error before the
// version write 400s. Two rules that bite a permit builder:
//   1. a granted op with a constrained arg field must carry a non-empty
//      resources list for each such field, and
//   2. resources on a field the op has no constraint for are rejected —
//      narrowing nothing enforces would be false security.

export interface OpGrant {
  op: string;
  /** Approved values per constrained arg field — exact strings, no predicates. */
  resources: Record<string, string[]>;
}

export interface ConnectorGrant {
  connector: string;
  ops: OpGrant[];
}

export interface PermitBuild {
  providers: string[];
  /** Named LLM credential; "default" (or blank) is the server's default. */
  connection: string;
  perRunCents?: number;
  grants: ConnectorGrant[];
}

export const EMPTY_BUILD: PermitBuild = {
  providers: [],
  connection: "default",
  grants: [],
};

/** Comma- or newline-separated exact values → a clean list. */
export function parseResourceList(text: string): string[] {
  return text
    .split(/[\n,]/)
    .map((v) => v.trim())
    .filter((v) => v !== "");
}

export function buildPermitDoc(build: PermitBuild): PermitDoc {
  const doc: PermitDoc = { v: 1 };

  const connection = build.connection.trim();
  if (build.providers.length > 0 || (connection !== "" && connection !== "default")) {
    doc.llm = {};
    if (build.providers.length > 0) doc.llm.providers = [...build.providers];
    if (connection !== "" && connection !== "default") doc.llm.connection = connection;
  }

  if (build.perRunCents !== undefined) {
    doc.spend = { per_run_cents: build.perRunCents };
  }

  const connections: Record<string, PermitConnectionDoc> = {};
  for (const grant of build.grants) {
    if (grant.ops.length === 0) continue; // deny-all is the entry's absence
    const entry: PermitConnectionDoc = {
      kind: "http",
      ops: grant.ops.map((o) => o.op),
    };
    const resources: Record<string, Record<string, string[]>> = {};
    for (const op of grant.ops) {
      const fields = Object.entries(op.resources).filter(([, v]) => v.length > 0);
      if (fields.length > 0) resources[op.op] = Object.fromEntries(fields);
    }
    if (Object.keys(resources).length > 0) entry.resources = resources;
    connections[grant.connector] = entry;
  }
  if (Object.keys(connections).length > 0) doc.connections = connections;

  return doc;
}

/**
 * Mirrors the server's write-time permit checks. `catalog` is null when
 * GET /v1/catalog couldn't be reached — grants can't be validated (or
 * honestly granted) without it.
 */
export function validatePermitBuild(
  build: PermitBuild,
  catalog: CatalogConnector[] | null,
): string[] {
  const errors: string[] = [];

  if (build.perRunCents !== undefined) {
    if (!Number.isInteger(build.perRunCents) || build.perRunCents <= 0) {
      errors.push("The spend cap must be a whole, positive number of cents.");
    }
  }

  const granted = build.grants.filter((g) => g.ops.length > 0);
  if (granted.length > 0 && catalog === null) {
    errors.push("Connector grants need the catalog, which couldn't be loaded.");
    return errors;
  }

  for (const grant of granted) {
    const connector = catalog?.find((c) => c.id === grant.connector);
    if (!connector) {
      errors.push(`The catalog has no connector "${grant.connector}".`);
      continue;
    }
    const seen = new Set<string>();
    for (const opGrant of grant.ops) {
      if (seen.has(opGrant.op)) {
        errors.push(`${connector.name}: "${opGrant.op}" is granted twice.`);
        continue;
      }
      seen.add(opGrant.op);
      const op = findOp(connector, opGrant.op);
      if (!op) {
        errors.push(`${connector.name} has no operation "${opGrant.op}".`);
        continue;
      }
      const constrained = new Set(op.constraints ?? []);
      for (const field of constrained) {
        if ((opGrant.resources[field] ?? []).length === 0) {
          errors.push(
            `${connector.name}: "${opGrant.op}" needs at least one approved ${field} value.`,
          );
        }
      }
      for (const field of Object.keys(opGrant.resources)) {
        if (opGrant.resources[field]!.length > 0 && !constrained.has(field)) {
          errors.push(
            `${connector.name}: "${opGrant.op}" can't be narrowed by ${field} — nothing enforces it.`,
          );
        }
      }
    }
  }

  return errors;
}
