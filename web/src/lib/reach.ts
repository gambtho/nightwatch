import type { CatalogConnector } from "../api/types";
import { findOp, opLabel } from "./catalog";
import type { ConnectionGrant } from "./permit";

// Joins a permit's connector grants against the catalog to fill the
// blast-radius diagram's read/write columns. The permit records op names
// only; the catalog says what each op is and whether it reads or writes.
// When the catalog can't say, the grant still must not vanish from the
// diagram — it renders in the write column, the conservative reading,
// with a note naming why: the op isn't in the loaded catalog
// ("unlisted"), the catalog itself wasn't available to check
// ("unchecked"), or the permit entry couldn't be parsed ("unreadable").

export interface ReachItem {
  key: string;
  /** Connector display name (catalog), or its raw id when unknown. */
  connector: string;
  op: string;
  description?: string;
  /** "field: v1, v2" lines from the permit's approved resource lists. */
  resources: string[];
  /** Set when the catalog couldn't confirm what this op does. */
  note?: "unlisted" | "unchecked" | "unreadable";
}

export interface ReachColumns {
  read: ReachItem[];
  write: ReachItem[];
}

export function reachColumns(
  grants: ConnectionGrant[],
  catalog: CatalogConnector[] | null | undefined,
): ReachColumns {
  const columns: ReachColumns = { read: [], write: [] };
  for (const grant of grants) {
    const connector = catalog?.find((c) => c.id === grant.connector);
    const name = connector?.name ?? grant.connector;
    if (grant.unreadable) {
      columns.write.push({
        key: `${grant.connector}.__unreadable`,
        connector: name,
        op: "part of this grant couldn't be read",
        resources: [],
        note: "unreadable",
      });
    }
    for (const granted of grant.ops) {
      const op = connector ? findOp(connector, granted.name) : undefined;
      const item: ReachItem = {
        key: `${grant.connector}.${granted.name}`,
        connector: name,
        op: opLabel(granted.name),
        description: op?.description,
        resources: Object.entries(granted.resources)
          .filter(([, values]) => values.length > 0)
          .map(([field, values]) => `${field.replace(/_/g, " ")}: ${values.join(", ")}`),
      };
      if (op === undefined) {
        item.note = catalog ? "unlisted" : "unchecked";
      }
      if (op?.effect === "read") {
        columns.read.push(item);
      } else {
        columns.write.push(item);
      }
    }
  }
  return columns;
}
