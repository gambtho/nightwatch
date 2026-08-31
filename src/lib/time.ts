import type { Run } from "./types";

// Manual assembly instead of format(): newer ICU inserts a narrow no-break
// space before AM/PM, which breaks copy that must read exactly
// "scheduled 3:00 AM · ran 7:42 AM, when your computer woke".
export function clock(iso: string, timezone: string): string {
  const parts = new Intl.DateTimeFormat("en-US", {
    hour: "numeric",
    minute: "2-digit",
    hour12: true,
    timeZone: timezone,
  }).formatToParts(new Date(iso));
  const get = (type: string) => parts.find((p) => p.type === type)?.value ?? "";
  return `${get("hour")}:${get("minute")} ${get("dayPeriod")}`;
}

// The sleeping-machine line, rendered from data the run already carries.
export function wakeLine(run: Run, timezone: string): string | null {
  if (!run.fireTime || run.fireTime === run.at) return null;
  return `scheduled ${clock(run.fireTime, timezone)} · ran ${clock(run.at, timezone)}, when your computer woke`;
}
