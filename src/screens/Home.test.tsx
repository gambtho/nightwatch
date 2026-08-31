import { render, screen } from "@testing-library/react";
import Home from "./Home";
import { allWorkflows } from "../fixtures/workflows";
import { dollars, MONTHLY_BUDGET_CENTS, spentCents } from "../lib/budget";

test("lists every workflow with its last summary", () => {
  render(<Home onNew={() => {}} onAlert={() => {}} />);
  expect(screen.getByText("Weekly support digest")).toBeInTheDocument();
  expect(screen.getByText("Contract renewals coming up")).toBeInTheDocument();
  expect(screen.getByText("Unanswered customer questions")).toBeInTheDocument();
});

test("the overnight run says when it actually ran — on wake, honestly", () => {
  render(<Home onNew={() => {}} onAlert={() => {}} />);
  expect(
    screen.getByText("scheduled 3:00 AM · ran 7:42 AM, when your computer woke"),
  ).toBeInTheDocument();
});

test("the budget meter derives its numbers from the run history", () => {
  render(<Home onNew={() => {}} onAlert={() => {}} />);
  const spent = spentCents(allWorkflows);
  expect(spent).toBeGreaterThan(0);
  expect(
    screen.getByText(
      `${dollars(spent)} of ${dollars(MONTHLY_BUDGET_CENTS)} · resets on the 1st`,
    ),
  ).toBeInTheDocument();
});

test("the budget alert copy uses the spec's words", () => {
  render(<Home onNew={() => {}} onAlert={() => {}} />);
  expect(
    screen.getByText(
      /your budget is used up until the 1st — raise it in settings or wait/,
    ),
  ).toBeInTheDocument();
});

test("tells the user they do not need to be here", () => {
  render(<Home onNew={() => {}} onAlert={() => {}} />);
  expect(screen.getByText("You don't need to check this page.")).toBeInTheDocument();
  expect(
    screen.getByText("If something goes wrong, we'll come find you."),
  ).toBeInTheDocument();
});
