import type { CatalogConnector, CatalogOp } from "../api/types";

// The one place that reads a connector's connection state. Today the
// catalog carries a plain `connected` boolean; connectors phase 2 adds a
// richer status field (needs_reauth, …) alongside it. Screens render the
// label and tone returned here rather than branching on the boolean, so
// phase 2's states mostly land in this function; a state needing a new
// affordance (e.g. a reconnect button) will still touch the screen.

export interface ConnectionView {
  connected: boolean;
  /** Short badge text, e.g. "Connected" / "Not connected". */
  label: string;
  /** Badge styling intent — "ok" renders calm, "attention" stands out. */
  tone: "ok" | "attention";
}

export function connectionView(connector: CatalogConnector): ConnectionView {
  const connected = connector.connected === true;
  return {
    connected,
    label: connected ? "Connected" : "Not connected",
    tone: connected ? "ok" : "attention",
  };
}

/** "create_event" → "create event", for the 3-second diagram read. */
export function opLabel(name: string): string {
  return name.replace(/_/g, " ");
}

export function findOp(connector: CatalogConnector, name: string): CatalogOp | undefined {
  return connector.ops.find((op) => op.name === name);
}
