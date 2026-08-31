import { describe, expect, it, vi } from "vitest";

// Same-day detection depends on the runner's zone, so pin it before any
// Date/Intl use. Files run in isolated workers, so this leaks nowhere.
vi.stubEnv("TZ", "UTC");

import { wakeLine } from "./wake";

// Clock text comes from Intl in the runner's own zone, so expectations are
// built with the same formatter; the assertions of interest are when the
// line appears at all and when the weekday joins it.

function clock(iso: string, withDay = false): string {
  return new Intl.DateTimeFormat("en-US", {
    ...(withDay ? { weekday: "short" as const } : {}),
    hour: "numeric",
    minute: "2-digit",
  }).format(new Date(iso));
}

const base = {
  fire_reason: "schedule" as const,
  fire_time: "2026-08-31T03:00:00Z",
  created_at: "2026-08-31T03:00:10Z",
};

describe("wakeLine", () => {
  it("describes a scheduled run that started hours late as fired on wake", () => {
    const run = { ...base, started_at: "2026-08-31T07:42:00Z" };
    expect(wakeLine(run)).toBe(
      `scheduled ${clock(run.fire_time)} · ran ${clock(run.started_at)}, when your computer woke`,
    );
  });

  it("stays quiet for a run that fired within the normal window", () => {
    expect(wakeLine({ ...base, started_at: "2026-08-31T03:04:00Z" })).toBeNull();
  });

  it("stays quiet for manual runs and runs without a fire_time", () => {
    expect(
      wakeLine({
        fire_reason: "manual",
        created_at: base.created_at,
        started_at: "2026-08-31T07:42:00Z",
      }),
    ).toBeNull();
    expect(
      wakeLine({
        fire_reason: "schedule",
        created_at: base.created_at,
        started_at: "2026-08-31T07:42:00Z",
      }),
    ).toBeNull();
  });

  it("falls back to created_at when the run never recorded a start", () => {
    const run = {
      fire_reason: "schedule" as const,
      fire_time: "2026-08-31T03:00:00Z",
      created_at: "2026-08-31T07:42:00Z",
    };
    expect(wakeLine(run)).toBe(
      `scheduled ${clock(run.fire_time)} · ran ${clock(run.created_at)}, when your computer woke`,
    );
  });

  it("adds the weekday when the wake crossed into another day", () => {
    const run = { ...base, started_at: "2026-09-02T09:15:00Z" };
    expect(wakeLine(run)).toBe(
      `scheduled ${clock(run.fire_time, true)} · ran ${clock(run.started_at, true)}, when your computer woke`,
    );
  });

  it("stays quiet on unparseable timestamps", () => {
    expect(wakeLine({ ...base, fire_time: "not a date" })).toBeNull();
  });
});
