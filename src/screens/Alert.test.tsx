import { render, screen } from "@testing-library/react";
import Alert from "./Alert";
import { consecutiveFailures, shouldAutoPause } from "../lib/grading";
import { supportDigestDegraded } from "../fixtures/workflows";

test("names the failing rule and how long it has failed", () => {
  render(<Alert />);
  expect(
    screen.getByText(/Flags real product bugs separately/),
  ).toBeInTheDocument();
  expect(screen.getByText(/Failed 3 Mondays running/)).toBeInTheDocument();
});

test("explains the suspected cause and what it already did", () => {
  render(<Alert />);
  expect(screen.getByText("WHY I THINK IT'S HAPPENING")).toBeInTheDocument();
  expect(screen.getByText(/Paused it/)).toBeInTheDocument();
});

test("the streak shown is the graded fixture's actual streak, not a hardcoded number", () => {
  const streak = consecutiveFailures(supportDigestDegraded, "security");
  expect(streak).toBe(3);
  expect(shouldAutoPause(supportDigestDegraded)).toBe(true);

  render(<Alert />);
  expect(
    screen.getByText(new RegExp(`Failed ${streak} Mondays running`)),
  ).toBeInTheDocument();
  expect(screen.getByText(new RegExp(`Show me the ${streak} runs`))).toBeInTheDocument();
});
