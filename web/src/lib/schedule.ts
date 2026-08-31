import { nextFire, parseCron } from "./cron";

// Plain-language rendering of the schedule artifact for a non-technical
// reader. Common shapes get words; anything else falls back to the cron
// string rather than guessing.

const DAY_NAMES = [
  "Sundays",
  "Mondays",
  "Tuesdays",
  "Wednesdays",
  "Thursdays",
  "Fridays",
  "Saturdays",
];

function clockLabel(hour: number, minute: number): string {
  const h12 = hour % 12 === 0 ? 12 : hour % 12;
  const suffix = hour < 12 ? "AM" : "PM";
  return `${h12}:${String(minute).padStart(2, "0")} ${suffix}`;
}

function ordinal(n: number): string {
  const rem10 = n % 10;
  const rem100 = n % 100;
  if (rem10 === 1 && rem100 !== 11) return `${n}st`;
  if (rem10 === 2 && rem100 !== 12) return `${n}nd`;
  if (rem10 === 3 && rem100 !== 13) return `${n}rd`;
  return `${n}th`;
}

export function describeSchedule(cron: string, tz: string): string {
  let fields;
  try {
    fields = parseCron(cron);
  } catch {
    return cron;
  }
  const allMonths = fields.months.size === 12;
  if (fields.minutes.size !== 1 || fields.hours.size !== 1 || !allMonths) return cron;
  const minute = [...fields.minutes][0]!;
  const hour = [...fields.hours][0]!;
  const time = clockLabel(hour, minute);

  if (fields.domStar && fields.dowStar) return `Every day at ${time} (${tz})`;
  const dowDays = [...fields.dow].filter((d) => d !== 7).sort((a, b) => a - b);
  if (fields.domStar && dowDays.length === 5 && dowDays.every((d, i) => d === i + 1)) {
    return `Weekdays at ${time} (${tz})`;
  }
  if (fields.domStar && dowDays.length <= 3) {
    const days = dowDays.map((d) => DAY_NAMES[d]).join(", ");
    return `${days} at ${time} (${tz})`;
  }
  if (fields.dowStar && fields.dom.size === 1) {
    const dom = [...fields.dom][0]!;
    return `The ${ordinal(dom)} of each month at ${time} (${tz})`;
  }
  return cron;
}

/** "Next: Monday, Sep 7 at 9:00 AM" in the viewer's own clock, or null. */
export function nextRunLabel(
  cron: string,
  tz: string,
  from: Date = new Date(),
): string | null {
  let fire: Date | null;
  try {
    fire = nextFire(cron, tz, from);
  } catch {
    return null;
  }
  if (!fire) return null;
  return new Intl.DateTimeFormat("en-US", {
    weekday: "long",
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  }).format(fire);
}
