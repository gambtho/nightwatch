import type { CatalogConnector } from "../api/types";
import { findOp, opLabel } from "./catalog";
import type { ConnectionGrant } from "./permit";

// Joins a permit's connector grants against the catalog to fill the
// blast-radius diagram's read/write columns. The permit records op names
// only; the catalog says what each op is and whether it reads or writes.
// When the catalog can't say (unreachable, or an op it no longer lists),
// the grant still must not vanish from the diagram — it renders in the
// write column, the conservative reading, and is flagged unrecognized.

export interface ReachItem {
  key: string;
  /** Connector display name (catalog), or its raw id when unknown. */
  connector: string;
  op: string;
  description?: string;
  /** "field: v1, v2" lines from the permit's approved resource lists. */
  resources: string[];
  /** True when the catalog couldn't confirm what this op does. */
  unrecognized: boolean;
}

export interface ReachColumns {
  read: ReachItem[];
  write: ReachItem[];
}

export function reachColumns(
  grants: ConnectionGrant[],
  catalog: CatalogConnector[] | null,
): ReachColumns {
  const columns: ReachColumns = { read: [], write: [] };
  for (const grant of grants) {
    const connector = catalog?.find((c) => c.id === grant.connector);
    for (const granted of grant.ops) {
      const op = connector ? findOp(connector, granted.name) : undefined;
      const item: ReachItem = {
        key: `${grant.connector}.${granted.name}`,
        connector: connector?.name ?? grant.connector,
        op: opLabel(granted.name),
        description: op?.description,
        resources: Object.entries(granted.resources)
          .filter(([, values]) => values.length > 0)
          .map(([field, values]) => `${field.replace(/_/g, " ")}: ${values.join(", ")}`),
        unrecognized: op === undefined,
      };
      if (op?.effect === "read") {
        columns.read.push(item);
      } else {
        columns.write.push(item);
      }
    }
  }
  return columns;
}
