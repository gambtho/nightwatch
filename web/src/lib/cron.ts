// Client-side evaluation of the schedule artifact ({cron, tz} — 5-field
// cron, IANA zone) for the home screen's "Next:" line. The server's
// scheduler is authoritative; this exists only for display, so it favors
// clarity over exotic cron features. Zone math goes through Intl, so DST
// follows the zone's own rules; a wall time erased by a spring-forward gap
// is skipped rather than shifted.

interface CronFields {
  minutes: Set<number>;
  hours: Set<number>;
  dom: Set<number>;
  months: Set<number>;
  dow: Set<number>;
  domStar: boolean;
  dowStar: boolean;
}

function parseField(field: string, min: number, max: number): Set<number> {
  const values = new Set<number>();
  for (const part of field.split(",")) {
    const [rangePart, stepPart] = part.split("/");
    const step = stepPart === undefined ? 1 : Number(stepPart);
    if (!Number.isInteger(step) || step < 1) throw new Error(`bad step in ${field}`);
    let lo: number;
    let hi: number;
    if (rangePart === "*" || rangePart === "") {
      lo = min;
      hi = max;
    } else if (rangePart !== undefined && rangePart.includes("-")) {
      const [a, b] = rangePart.split("-");
      lo = Number(a);
      hi = Number(b);
    } else {
      lo = Number(rangePart);
      hi = stepPart === undefined ? lo : max;
    }
    if (
      !Number.isInteger(lo) ||
      !Number.isInteger(hi) ||
      lo < min ||
      hi > max ||
      lo > hi
    ) {
      throw new Error(`bad field ${field}`);
    }
    for (let v = lo; v <= hi; v += step) values.add(v);
  }
  return values;
}

export function parseCron(expr: string): CronFields {
  const parts = expr.trim().split(/\s+/);
  if (parts.length !== 5) throw new Error(`expected 5 cron fields, got ${parts.length}`);
  const [minute, hour, dom, month, dow] = parts as [
    string,
    string,
    string,
    string,
    string,
  ];
  const dowSet = parseField(dow, 0, 7);
  if (dowSet.has(7)) dowSet.add(0); // 7 is Sunday too, as in classic cron
  return {
    minutes: parseField(minute, 0, 59),
    hours: parseField(hour, 0, 23),
    dom: parseField(dom, 1, 31),
    months: parseField(month, 1, 12),
    dow: dowSet,
    domStar: dom === "*",
    dowStar: dow === "*",
  };
}

// Classic cron day matching: when both day-of-month and day-of-week are
// restricted, a day matches if either does; a starred field defers to the other.
function dayMatches(f: CronFields, dom: number, dow: number): boolean {
  if (f.domStar && f.dowStar) return true;
  if (f.domStar) return f.dow.has(dow);
  if (f.dowStar) return f.dom.has(dom);
  return f.dom.has(dom) || f.dow.has(dow);
}

interface WallTime {
  year: number;
  month: number;
  day: number;
  hour: number;
  minute: number;
}

const partFormatters = new Map<string, Intl.DateTimeFormat>();

function formatterFor(tz: string): Intl.DateTimeFormat {
  let fmt = partFormatters.get(tz);
  if (!fmt) {
    fmt = new Intl.DateTimeFormat("en-US", {
      timeZone: tz,
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      hourCycle: "h23",
    });
    partFormatters.set(tz, fmt);
  }
  return fmt;
}

function wallTimeAt(instant: Date, tz: string): WallTime {
  const parts: Partial<Record<string, number>> = {};
  for (const p of formatterFor(tz).formatToParts(instant)) {
    if (p.type !== "literal") parts[p.type] = Number(p.value);
  }
  return {
    year: parts.year ?? 0,
    month: parts.month ?? 0,
    day: parts.day ?? 0,
    hour: parts.hour ?? 0,
    minute: parts.minute ?? 0,
  };
}

// The instant whose wall clock in tz reads the given time, or null when the
// zone skips that wall time (spring-forward gap). Two-pass offset correction.
function instantFor(w: WallTime, tz: string): Date | null {
  const asUtc = Date.UTC(w.year, w.month - 1, w.day, w.hour, w.minute);
  let guess = asUtc;
  for (let i = 0; i < 2; i++) {
    const wall = wallTimeAt(new Date(guess), tz);
    const wallAsUtc = Date.UTC(
      wall.year,
      wall.month - 1,
      wall.day,
      wall.hour,
      wall.minute,
    );
    guess += asUtc - wallAsUtc;
  }
  const check = wallTimeAt(new Date(guess), tz);
  const matches =
    check.year === w.year &&
    check.month === w.month &&
    check.day === w.day &&
    check.hour === w.hour &&
    check.minute === w.minute;
  return matches ? new Date(guess) : null;
}

// Weekday depends only on the calendar date, so plain UTC arithmetic serves.
function weekdayOf(w: WallTime): number {
  return new Date(Date.UTC(w.year, w.month - 1, w.day)).getUTCDay();
}

function addDays(w: WallTime, days: number): WallTime {
  const d = new Date(Date.UTC(w.year, w.month - 1, w.day + days));
  return {
    year: d.getUTCFullYear(),
    month: d.getUTCMonth() + 1,
    day: d.getUTCDate(),
    hour: 0,
    minute: 0,
  };
}

/**
 * The next instant at or after `from` when the schedule fires, or null when
 * nothing matches within the next two years (an unsatisfiable schedule).
 */
export function nextFire(cron: string, tz: string, from: Date = new Date()): Date | null {
  const fields = parseCron(cron);
  const start = new Date(Math.ceil(from.getTime() / 60000) * 60000);
  const startWall = wallTimeAt(start, tz);
  const minutes = [...fields.minutes].sort((a, b) => a - b);
  const hours = [...fields.hours].sort((a, b) => a - b);

  let day: WallTime = { ...startWall, hour: 0, minute: 0 };
  for (let i = 0; i < 731; i++, day = addDays(day, 1)) {
    if (!fields.months.has(day.month)) continue;
    if (!dayMatches(fields, day.day, weekdayOf(day))) continue;
    const isFirstDay =
      day.year === startWall.year &&
      day.month === startWall.month &&
      day.day === startWall.day;
    for (const hour of hours) {
      for (const minute of minutes) {
        if (
          isFirstDay &&
          (hour < startWall.hour ||
            (hour === startWall.hour && minute < startWall.minute))
        ) {
          continue;
        }
        const instant = instantFor({ ...day, hour, minute }, tz);
        if (instant && instant.getTime() >= start.getTime()) return instant;
      }
    }
  }
  return null;
}
