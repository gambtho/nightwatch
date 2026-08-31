import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import Approve from "./Approve";
import { AUTO_PAUSE_THRESHOLD } from "../lib/grading";

test("shows the workflow name and its schedule label", () => {
  render(<Approve onApproved={() => {}} />);
  expect(screen.getByText("Weekly flake digest")).toBeInTheDocument();
  expect(screen.getByText(/Mondays at 9:00 AM/)).toBeInTheDocument();
});

test("shows the full permit, not a partial one", () => {
  render(<Approve onApproved={() => {}} />);
  expect(screen.getByText("GitHub Actions runs")).toBeInTheDocument();
  expect(screen.getByText("Slack #ci-alerts")).toBeInTheDocument();
  expect(screen.getByText("Slack #eng-quality")).toBeInTheDocument();
});

test("nothing is highlighted as newly added on the approval screen", () => {
  render(<Approve onApproved={() => {}} />);
  expect(screen.getByTestId("cap-gha-read")).not.toHaveClass("just-added");
});

test("approving reports up", async () => {
  const onApproved = vi.fn();
  render(<Approve onApproved={onApproved} />);
  await userEvent.click(screen.getByRole("button", { name: "Approve & schedule" }));
  expect(onApproved).toHaveBeenCalledOnce();
});

test("the auto-pause note is derived from AUTO_PAUSE_THRESHOLD, not a hardcoded digit", () => {
  render(<Approve onApproved={() => {}} />);
  expect(
    screen.getByText(new RegExp(`It stops after ${AUTO_PAUSE_THRESHOLD} bad runs`)),
  ).toBeInTheDocument();
});
