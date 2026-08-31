import type { Version } from "../api/types";

// At most one version is approved at a time (server invariant); drafts can
// pile up, and the newest one is the one awaiting the approval gate.

export interface VersionSummary {
  approved?: Version;
  latestDraft?: Version;
}

export function summarizeVersions(versions: Version[]): VersionSummary {
  const summary: VersionSummary = {};
  for (const v of versions) {
    if (v.status === "approved") summary.approved = v;
    if (v.status === "draft") {
      if (!summary.latestDraft || v.number > summary.latestDraft.number) {
        summary.latestDraft = v;
      }
    }
  }
  return summary;
}

/** The most recently created run, by created_at then array order. */
export function lastRun<T extends { created_at: string }>(runs: T[]): T | undefined {
  if (runs.length === 0) return undefined;
  return [...runs].sort((a, b) => a.created_at.localeCompare(b.created_at))[
    runs.length - 1
  ];
}
