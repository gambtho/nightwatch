import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import App from "./App";

test("starts on intake", () => {
  render(<App />);
  expect(screen.getByText("What do you want taken care of?")).toBeInTheDocument();
});

test("walks intake to verdict to build", async () => {
  render(<App />);
  await userEvent.type(screen.getByRole("textbox"), "Every Monday I dig through tickets");
  await userEvent.click(screen.getByRole("button", { name: "See what you'd do" }));
  expect(screen.getByText("I'D GET THIS WRONG")).toBeInTheDocument();

  await userEvent.click(screen.getByRole("button", { name: "Build this" }));
  expect(screen.getByText("Its reach — updating live")).toBeInTheDocument();
});

test("the facilitator nav jumps straight to any screen", async () => {
  render(<App />);
  await userEvent.click(screen.getByRole("button", { name: "Alert" }));
  expect(screen.getByText("THE RULE IT'S MISSING")).toBeInTheDocument();
});
