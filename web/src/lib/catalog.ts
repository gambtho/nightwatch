import type { CatalogConnector, CatalogOp } from "../api/types";

// The one place that reads a connector's connection state. Today the
// catalog carries a plain `connected` boolean; connectors phase 2 adds a
// richer status field (needs_reauth, …) alongside it. Callers render the
// view returned here, so phase 2 changes this function — not the screens.

export interface ConnectionView {
  connected: boolean;
  /** Short badge text, e.g. "Connected" / "Not connected". */
  label: string;
}

export function connectionView(connector: CatalogConnector): ConnectionView {
  const connected = connector.connected === true;
  return { connected, label: connected ? "Connected" : "Not connected" };
}

/** "create_event" → "create event", for the 3-second diagram read. */
export function opLabel(name: string): string {
  return name.replace(/_/g, " ");
}

export function findOp(connector: CatalogConnector, name: string): CatalogOp | undefined {
  return connector.ops.find((op) => op.name === name);
}
