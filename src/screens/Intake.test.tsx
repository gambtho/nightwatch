import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import Intake from "./Intake";

test("asks the question in the user's language", () => {
  render(<Intake onSubmit={() => {}} />);
  expect(screen.getByText("What do you want taken care of?")).toBeInTheDocument();
  expect(
    screen.getByText("Describe it how you'd describe it to a coworker."),
  ).toBeInTheDocument();
});

test("submitting passes the typed text up", async () => {
  const onSubmit = vi.fn();
  render(<Intake onSubmit={onSubmit} />);
  await userEvent.type(screen.getByRole("textbox"), "Every Monday I dig through tickets");
  await userEvent.click(screen.getByRole("button", { name: "See what you'd do" }));
  expect(onSubmit).toHaveBeenCalledWith("Every Monday I dig through tickets");
});

test("does not submit empty input", async () => {
  const onSubmit = vi.fn();
  render(<Intake onSubmit={onSubmit} />);
  await userEvent.click(screen.getByRole("button", { name: "See what you'd do" }));
  expect(onSubmit).not.toHaveBeenCalled();
});
