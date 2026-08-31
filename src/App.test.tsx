import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import App from "./App";

test("starts on first run — choose where your AI runs", () => {
  render(<App />);
  expect(screen.getByText("Choose where your AI runs")).toBeInTheDocument();
});

test("the facilitator nav jumps straight to any screen", async () => {
  render(<App />);
  await userEvent.click(screen.getByRole("button", { name: "Alert" }));
  expect(screen.getByText("THE RULE IT'S MISSING")).toBeInTheDocument();
});

test("the whole pivot story walks end to end without the nav", async () => {
  render(<App />);

  // First run: pick Anthropic, paste a key, see the disclosed test call verify.
  await userEvent.click(screen.getByRole("button", { name: /Anthropic/ }));
  expect(
    screen.getByText("We'll make one tiny test call — well under a cent, on your key."),
  ).toBeInTheDocument();
  await userEvent.type(screen.getByLabelText("Anthropic key"), "sk-ant-demo-123");
  await userEvent.click(screen.getByRole("button", { name: "Check my key" }));
  await screen.findByText(/Key verified/, undefined, { timeout: 3000 });
  await userEvent.click(screen.getByRole("button", { name: "Set your budget" }));

  // Budget: the spec's wording, then land in the build conversation.
  expect(
    screen.getByText("How much Tomte may spend from your key per month."),
  ).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Open Tomte" }));

  // Ask for the job in plain words.
  await userEvent.type(screen.getByRole("textbox"), "Watch my support tickets");
  await userEvent.click(screen.getByRole("button", { name: "See what you'd do" }));

  // Build until Slack is needed, connect it once, and come back.
  const advance = () => userEvent.click(screen.getByRole("button", { name: "Continue" }));
  await advance();
  await advance();
  await advance();
  await userEvent.click(
    screen.getByRole("button", { name: "Connect Slack — one paste" }),
  );
  await userEvent.type(screen.getByLabelText("Slack bot token"), "xoxb-demo-token");
  await userEvent.click(screen.getByRole("button", { name: "Connect Slack" }));
  await screen.findByText(/You won't paste this again/, undefined, { timeout: 3000 });
  await userEvent.click(screen.getByRole("button", { name: "Back to your build" }));

  // Finish the conversation and get the verdict.
  await advance();
  await advance();
  await advance();
  await userEvent.click(screen.getByRole("button", { name: "See my honest read" }));
  expect(screen.getByText("I'D GET THIS WRONG")).toBeInTheDocument();

  // Approve on the blast-radius picture with the enforcement promise.
  await userEvent.click(screen.getByRole("button", { name: "Review what it can touch" }));
  expect(
    screen.getByText(
      /It can only act through Tomte's checkpoint, and every request is checked against this picture/,
    ),
  ).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Approve & schedule" }));

  // The quiet home: budget meter and the fire-on-wake run row.
  expect(screen.getByText("THIS MONTH'S BUDGET")).toBeInTheDocument();
  expect(
    screen.getByText("scheduled 3:00 AM · ran 7:42 AM, when your computer woke"),
  ).toBeInTheDocument();
}, 20000);
