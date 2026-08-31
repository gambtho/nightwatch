import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import Build from "./Build";

test("starts with only the first turn and an empty permit", () => {
  render(<Build onApprove={() => {}} />);
  expect(
    screen.getByText(/Every Monday, look at last week's CI failures/),
  ).toBeInTheDocument();
  expect(screen.getAllByText("Nothing yet")).toHaveLength(2);
});

test("advancing the conversation grows the permit", async () => {
  render(<Build onApprove={() => {}} />);
  const next = screen.getByRole("button", { name: "Continue" });
  await userEvent.click(next);
  expect(screen.getByText("GitHub Actions runs")).toBeInTheDocument();
  await userEvent.click(next);
  expect(screen.getByText("Slack #eng-quality")).toBeInTheDocument();
});

test("the approve action appears only at the end of the script", async () => {
  render(<Build onApprove={() => {}} />);
  expect(screen.queryByRole("button", { name: "Review what it can touch" })).toBeNull();
  const next = screen.getByRole("button", { name: "Continue" });
  for (let i = 0; i < 5; i++) {
    await userEvent.click(next);
  }
  expect(
    screen.getByRole("button", { name: "Review what it can touch" }),
  ).toBeInTheDocument();
});
