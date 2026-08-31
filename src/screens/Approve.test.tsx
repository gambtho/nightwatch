import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import Approve from "./Approve";
import { AUTO_PAUSE_THRESHOLD } from "../lib/grading";

test("shows the workflow name and its schedule label", () => {
  render(<Approve onApproved={() => {}} />);
  expect(screen.getByText("Weekly support digest")).toBeInTheDocument();
  expect(screen.getByText(/Mondays at 9:00 AM/)).toBeInTheDocument();
});

test("shows the full permit, not a partial one", () => {
  render(<Approve onApproved={() => {}} />);
  expect(screen.getByText("Zendesk tickets")).toBeInTheDocument();
  expect(screen.getByText("Slack #support")).toBeInTheDocument();
  expect(screen.getByText("Slack #team-digest")).toBeInTheDocument();
});

test("states the enforcement posture in the spec's softened words, verbatim", () => {
  render(<Approve onApproved={() => {}} />);
  expect(
    screen.getByText(
      /It can only act through Tomte's checkpoint, and every request is checked against this picture\./,
    ),
  ).toBeInTheDocument();
});

test("carries the spend line: per-run cap, grader cost, monthly budget", () => {
  render(<Approve onApproved={() => {}} />);
  expect(screen.getByText(/Runs on your key/)).toBeInTheDocument();
  expect(
    screen.getByText(/Checking my work against your rules adds ~1–2¢ per run/),
  ).toBeInTheDocument();
  expect(screen.getByText(/\$10\.00 monthly\s+budget/)).toBeInTheDocument();
});

test("names the sleeping machine honestly — fire on wake, once", () => {
  render(<Approve onApproved={() => {}} />);
  expect(screen.getByText(/Tomte works while your computer is on/)).toBeInTheDocument();
  expect(screen.getByText(/once, not twelve times/)).toBeInTheDocument();
});

test("nothing is highlighted as newly added on the approval screen", () => {
  render(<Approve onApproved={() => {}} />);
  expect(screen.getByTestId("cap-zendesk-read")).not.toHaveClass("just-added");
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
