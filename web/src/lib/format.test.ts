import { describe, expect, it } from "vitest";
import { dollars, timeAgo } from "./format";

describe("dollars", () => {
  it("formats cents", () => {
    expect(dollars(3)).toBe("$0.03");
    expect(dollars(150)).toBe("$1.50");
  });
});

describe("timeAgo", () => {
  const now = new Date("2026-08-31T12:00:00Z");

  it("covers the ladder from seconds to days", () => {
    expect(timeAgo("2026-08-31T11:59:30Z", now)).toBe("just now");
    expect(timeAgo("2026-08-31T11:58:00Z", now)).toBe("2 minutes ago");
    expect(timeAgo("2026-08-31T10:00:00Z", now)).toBe("2 hours ago");
    expect(timeAgo("2026-08-30T10:00:00Z", now)).toBe("yesterday");
    expect(timeAgo("2026-08-28T10:00:00Z", now)).toBe("3 days ago");
  });

  it("falls back to a date for old timestamps", () => {
    // Rendered in the viewer's zone, so the exact day may shift by one.
    expect(timeAgo("2026-06-01T12:00:00Z", now)).toMatch(/May 31|Jun [12]/);
  });

  it("passes through unparseable input", () => {
    expect(timeAgo("not a date", now)).toBe("not a date");
  });
});
