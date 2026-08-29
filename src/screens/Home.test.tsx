import { render, screen } from "@testing-library/react";
import Home from "./Home";

test("lists every workflow with its last summary", () => {
  render(<Home onNew={() => {}} />);
  expect(screen.getByText("Weekly support digest")).toBeInTheDocument();
  expect(screen.getByText("Contract renewals coming up")).toBeInTheDocument();
  expect(screen.getByText("Unanswered customer questions")).toBeInTheDocument();
});

test("tells the user they do not need to be here", () => {
  render(<Home onNew={() => {}} />);
  expect(screen.getByText("You don't need to check this page.")).toBeInTheDocument();
  expect(
    screen.getByText("If something goes wrong, we'll come find you."),
  ).toBeInTheDocument();
});
