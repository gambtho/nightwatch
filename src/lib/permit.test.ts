import { emptyPermit, grant, permitCounts, reads, writes } from "./permit";
import type { Capability } from "./types";

const supportRead: Capability = {
  id: "slack-support-read",
  system: "slack",
  label: "Slack #support",
  access: "read",
  detail: "last 7 days",
};

const digestWrite: Capability = {
  id: "slack-digest-write",
  system: "slack",
  label: "Slack #team-digest",
  access: "write",
  detail: "post only",
};

test("an empty permit has no capabilities but keeps its spend cap", () => {
  const permit = emptyPermit(200);
  expect(permit.capabilities).toEqual([]);
  expect(permit.maxCostCents).toBe(200);
});

test("granting adds a capability without mutating the original permit", () => {
  const before = emptyPermit(200);
  const after = grant(before, supportRead);
  expect(before.capabilities).toHaveLength(0);
  expect(after.capabilities).toHaveLength(1);
});

test("granting the same capability twice does not duplicate it", () => {
  const permit = grant(grant(emptyPermit(200), supportRead), supportRead);
  expect(permit.capabilities).toHaveLength(1);
});

test("reads and writes are separated by access", () => {
  const permit = grant(grant(emptyPermit(200), supportRead), digestWrite);
  expect(reads(permit).map((c) => c.id)).toEqual(["slack-support-read"]);
  expect(writes(permit).map((c) => c.id)).toEqual(["slack-digest-write"]);
});

test("permitCounts summarizes both sides", () => {
  const permit = grant(grant(emptyPermit(200), supportRead), digestWrite);
  expect(permitCounts(permit)).toEqual({ reads: 1, writes: 1 });
});
