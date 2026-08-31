import { describe, expect, it } from "vitest";
import { nextFire, parseCron } from "./cron";

describe("parseCron", () => {
  it("parses stars, values, ranges, steps, and lists", () => {
    const f = parseCron("0 9 * * 1,3-5");
    expect([...f.minutes]).toEqual([0]);
    expect([...f.hours]).toEqual([9]);
    expect(f.domStar).toBe(true);
    expect([...f.dow].sort()).toEqual([1, 3, 4, 5]);
  });

  it("treats 7 as Sunday", () => {
    const f = parseCron("0 0 * * 7");
    expect(f.dow.has(0)).toBe(true);
  });

  it("expands steps over ranges", () => {
    const f = parseCron("*/15 0-6/2 * * *");
    expect([...f.minutes]).toEqual([0, 15, 30, 45]);
    expect([...f.hours]).toEqual([0, 2, 4, 6]);
  });

  it("rejects the wrong number of fields", () => {
    expect(() => parseCron("0 9 * *")).toThrow();
  });

  it("rejects out-of-range values", () => {
    expect(() => parseCron("60 9 * * *")).toThrow();
    expect(() => parseCron("0 24 * * *")).toThrow();
  });
});

describe("nextFire", () => {
  it("finds the next daily fire in UTC", () => {
    const from = new Date("2026-08-31T10:30:00Z");
    const fire = nextFire("0 9 * * *", "UTC", from);
    expect(fire?.toISOString()).toBe("2026-09-01T09:00:00.000Z");
  });

  it("fires the same day when still ahead", () => {
    const from = new Date("2026-08-31T08:00:00Z");
    const fire = nextFire("0 9 * * *", "UTC", from);
    expect(fire?.toISOString()).toBe("2026-08-31T09:00:00.000Z");
  });

  it("includes an exact minute boundary", () => {
    const from = new Date("2026-08-31T09:00:00Z");
    const fire = nextFire("0 9 * * *", "UTC", from);
    expect(fire?.toISOString()).toBe("2026-08-31T09:00:00.000Z");
  });

  it("respects day-of-week", () => {
    // 2026-08-31 is a Monday; next Monday 9AM after Monday 10AM is Sep 7.
    const from = new Date("2026-08-31T10:00:00Z");
    const fire = nextFire("0 9 * * 1", "UTC", from);
    expect(fire?.toISOString()).toBe("2026-09-07T09:00:00.000Z");
  });

  it("evaluates wall time in the schedule's zone", () => {
    // 9AM New York in summer is 13:00 UTC (EDT, -4).
    const from = new Date("2026-08-31T00:00:00Z");
    const fire = nextFire("0 9 * * *", "America/New_York", from);
    expect(fire?.toISOString()).toBe("2026-08-31T13:00:00.000Z");
  });

  it("follows the zone across a DST transition", () => {
    // US DST ends 2026-11-01; 9AM New York is then 14:00 UTC (EST, -5).
    const from = new Date("2026-11-02T00:00:00Z");
    const fire = nextFire("0 9 * * *", "America/New_York", from);
    expect(fire?.toISOString()).toBe("2026-11-02T14:00:00.000Z");
  });

  it("skips a wall time erased by spring-forward", () => {
    // US DST starts 2026-03-08: 02:30 New York does not exist that day.
    const from = new Date("2026-03-08T00:00:00Z");
    const fire = nextFire("30 2 * * *", "America/New_York", from);
    expect(fire?.toISOString()).toBe("2026-03-09T06:30:00.000Z");
  });

  it("matches day-of-month OR day-of-week when both are restricted", () => {
    // Classic cron: dom=15, dow=Mon fires on the 15th and every Monday.
    const from = new Date("2026-09-01T00:00:00Z");
    const first = nextFire("0 9 15 * 1", "UTC", from);
    // Sep 7 2026 is the first Monday after Sep 1, before the 15th.
    expect(first?.toISOString()).toBe("2026-09-07T09:00:00.000Z");
  });

  it("returns null for an unsatisfiable schedule", () => {
    const fire = nextFire("0 9 31 2 *", "UTC", new Date("2026-01-01T00:00:00Z"));
    expect(fire).toBeNull();
  });
});
