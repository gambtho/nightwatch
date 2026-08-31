import { render, screen } from "@testing-library/react";
import Home from "./Home";

test("lists every workflow with its last summary", () => {
  render(<Home onNew={() => {}} />);
  expect(screen.getByText("Weekly flake digest")).toBeInTheDocument();
  expect(screen.getByText("Dependency updates worth reading")).toBeInTheDocument();
  expect(screen.getByText("Stale PR nudges")).toBeInTheDocument();
});

test("tells the user they do not need to be here", () => {
  render(<Home onNew={() => {}} />);
  expect(screen.getByText("You don't need to check this page.")).toBeInTheDocument();
  expect(
    screen.getByText("If something goes wrong, we'll come find you."),
  ).toBeInTheDocument();
});
