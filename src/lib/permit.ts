import type { Capability, Permit } from "./types";

export const DENIED_BY_DEFAULT = [
  "Email",
  "Direct messages",
  "Deleting anything",
  "Payments",
  "The rest of the internet",
];

export function emptyPermit(maxCostCents: number): Permit {
  return { capabilities: [], denied: [...DENIED_BY_DEFAULT], maxCostCents };
}

export function grant(permit: Permit, capability: Capability): Permit {
  if (permit.capabilities.some((c) => c.id === capability.id)) {
    return permit;
  }
  return { ...permit, capabilities: [...permit.capabilities, capability] };
}

export function reads(permit: Permit): Capability[] {
  return permit.capabilities.filter((c) => c.access === "read");
}

export function writes(permit: Permit): Capability[] {
  return permit.capabilities.filter((c) => c.access === "write");
}

export function permitCounts(permit: Permit): { reads: number; writes: number } {
  return { reads: reads(permit).length, writes: writes(permit).length };
}

export function maxCostLabel(permit: Permit): string {
  return `max $${(permit.maxCostCents / 100).toFixed(2)} / run`;
}
