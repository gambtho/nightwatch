import { describe, expect, it } from "vitest";
import { describeSchedule, nextRunLabel } from "./schedule";

describe("describeSchedule", () => {
  it("describes a weekly schedule in words", () => {
    expect(describeSchedule("0 9 * * 1", "America/New_York")).toBe(
      "Mondays at 9:00 AM (America/New_York)",
    );
  });

  it("describes a daily schedule", () => {
    expect(describeSchedule("30 7 * * *", "UTC")).toBe("Every day at 7:30 AM (UTC)");
  });

  it("describes weekdays", () => {
    expect(describeSchedule("0 18 * * 1-5", "UTC")).toBe("Weekdays at 6:00 PM (UTC)");
  });

  it("describes a short day list", () => {
    expect(describeSchedule("0 12 * * 1,4", "UTC")).toBe(
      "Mondays, Thursdays at 12:00 PM (UTC)",
    );
  });

  it("describes a monthly day", () => {
    expect(describeSchedule("0 9 1 * *", "UTC")).toBe(
      "The 1st of each month at 9:00 AM (UTC)",
    );
  });

  it("handles midnight and noon hours", () => {
    expect(describeSchedule("0 0 * * *", "UTC")).toBe("Every day at 12:00 AM (UTC)");
  });

  it("falls back to the cron string for shapes it can't word", () => {
    expect(describeSchedule("*/5 * * * *", "UTC")).toBe("*/5 * * * *");
  });

  it("falls back to the raw string for an unparseable expression", () => {
    expect(describeSchedule("not cron", "UTC")).toBe("not cron");
  });
});

describe("nextRunLabel", () => {
  it("renders the next fire", () => {
    const label = nextRunLabel("0 9 * * 1", "UTC", new Date("2026-08-31T10:00:00Z"));
    expect(label).toContain("Sep 7");
  });

  it("returns null when the schedule can't be evaluated", () => {
    expect(nextRunLabel("bad", "UTC")).toBeNull();
  });
});
