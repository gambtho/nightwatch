import { clock, wakeLine } from "./time";
import { renewals } from "../fixtures/workflows";
import type { Run } from "./types";

const TZ = "America/New_York";

test("clock renders a plain-space 12-hour time in the workflow's timezone", () => {
  expect(clock("2026-08-31T07:00:00Z", TZ)).toBe("3:00 AM");
  expect(clock("2026-08-31T11:42:00Z", TZ)).toBe("7:42 AM");
});

test("the fire-on-wake fixture renders the spec's home-page line, verbatim", () => {
  const last = renewals.runs[renewals.runs.length - 1];
  expect(wakeLine(last, renewals.schedule.timezone)).toBe(
    "scheduled 3:00 AM · ran 7:42 AM, when your computer woke",
  );
});

test("a run that fired on time gets no wake line", () => {
  const onTime: Run = {
    id: "r",
    at: "2026-08-31T07:00:00Z",
    fireTime: "2026-08-31T07:00:00Z",
    status: "ok",
    costCents: 1,
    ruleResults: [],
    summary: "ran",
  };
  expect(wakeLine(onTime, TZ)).toBeNull();
  expect(wakeLine({ ...onTime, fireTime: undefined }, TZ)).toBeNull();
});
