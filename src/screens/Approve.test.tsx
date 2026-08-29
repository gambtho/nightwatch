import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import Approve from "./Approve";

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
