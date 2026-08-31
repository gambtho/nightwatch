import type { Run } from "../api/types";

// The sleeping-machine promise, rendered (pivot spec, "The sleeping
// machine"): Tomte runs on a computer that sleeps, and a scheduled job
// that fires on wake says so plainly instead of pretending it ran on time:
//
//   scheduled 3:00 AM · ran 7:42 AM, when your computer woke
//
// `fire_time` is the scheduled occurrence the run satisfies; the gap
// between it and the actual start is the sleep (or downtime). A healthy
// scheduler fires within a tick of the occurrence, so a large gap is a
// wake, not jitter. Times render in the viewer's own clock — on a
// one-machine product that is the clock the schedule was set against.

/** Gap beyond which a scheduled run is described as fired-on-wake. */
export const WAKE_GAP_MS = 10 * 60 * 1000;

function clock(date: Date, withDay: boolean): string {
  return new Intl.DateTimeFormat("en-US", {
    ...(withDay ? { weekday: "short" as const } : {}),
    hour: "numeric",
    minute: "2-digit",
  }).format(date);
}

function sameLocalDay(a: Date, b: Date): boolean {
  return (
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate()
  );
}

type WakeFields = Pick<Run, "fire_reason" | "fire_time" | "started_at" | "created_at">;

/**
 * "scheduled 3:00 AM · ran 7:42 AM, when your computer woke", or null for
 * a run that fired on time, was fired by hand, or lacks the data to tell.
 * When the run started on a different day than it was scheduled, both
 * times carry the weekday so the line stays honest across long sleeps.
 */
export function wakeLine(run: WakeFields): string | null {
  if (run.fire_reason !== "schedule" || !run.fire_time) return null;
  const fired = new Date(run.fire_time);
  // created_at is when the scheduler admitted the run — on a wake-fire,
  // that is also the wake — so it stands in for a missing started_at.
  const started = new Date(run.started_at ?? run.created_at);
  if (Number.isNaN(fired.getTime()) || Number.isNaN(started.getTime())) return null;
  if (started.getTime() - fired.getTime() < WAKE_GAP_MS) return null;
  const withDay = !sameLocalDay(fired, started);
  return `scheduled ${clock(fired, withDay)} · ran ${clock(started, withDay)}, when your computer woke`;
}
