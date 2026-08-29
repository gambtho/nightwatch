import { buildScript, permitAfter } from "./conversation";

test("the permit starts empty", () => {
  expect(permitAfter(buildScript, 0).capabilities).toHaveLength(0);
});

test("the permit accumulates grants as turns advance", () => {
  const full = permitAfter(buildScript, buildScript.length);
  expect(full.capabilities.length).toBeGreaterThan(1);
});

test("permitAfter is monotonic — reach never shrinks", () => {
  let previous = 0;
  for (let i = 0; i <= buildScript.length; i++) {
    const count = permitAfter(buildScript, i).capabilities.length;
    expect(count).toBeGreaterThanOrEqual(previous);
    previous = count;
  }
});

test("the script ends with at least one read and one write", () => {
  const full = permitAfter(buildScript, buildScript.length);
  expect(full.capabilities.some((c) => c.access === "read")).toBe(true);
  expect(full.capabilities.some((c) => c.access === "write")).toBe(true);
});
