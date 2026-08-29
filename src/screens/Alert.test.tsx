import { render, screen } from "@testing-library/react";
import Alert from "./Alert";

test("names the failing rule and how long it has failed", () => {
  render(<Alert />);
  expect(
    screen.getByText(/Flags anything security-related separately/),
  ).toBeInTheDocument();
  expect(screen.getByText(/Failed 3 Mondays running/)).toBeInTheDocument();
});

test("explains the suspected cause and what it already did", () => {
  render(<Alert />);
  expect(screen.getByText("WHY I THINK IT'S HAPPENING")).toBeInTheDocument();
  expect(screen.getByText(/Paused it/)).toBeInTheDocument();
});
