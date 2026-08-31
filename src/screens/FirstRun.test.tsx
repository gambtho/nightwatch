import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import FirstRun from "./FirstRun";
import { ENDPOINT_PRESETS } from "../fixtures/endpoints";

test("offers every endpoint preset, including Azure AI Foundry and GitHub Models", () => {
  render(<FirstRun onDone={() => {}} />);
  for (const name of [
    "Anthropic",
    "OpenAI",
    "OpenRouter",
    "Azure AI Foundry",
    "GitHub Models",
    "Another service",
    "On this computer",
  ]) {
    expect(screen.getByText(name)).toBeInTheDocument();
  }
  expect(ENDPOINT_PRESETS).toHaveLength(7);
});

test("a wrong-shaped key paste is caught instantly, before any call", async () => {
  render(<FirstRun onDone={() => {}} />);
  await userEvent.click(screen.getByRole("button", { name: /Anthropic/ }));
  await userEvent.type(screen.getByLabelText("Anthropic key"), "sk-proj-oops");
  expect(screen.getByText(/it should start with sk-ant-/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Check my key" })).toBeDisabled();
});

test("the test call is disclosed before it happens, in the spec's words", async () => {
  render(<FirstRun onDone={() => {}} />);
  await userEvent.click(screen.getByRole("button", { name: /OpenRouter/ }));
  expect(
    screen.getByText("We'll make one tiny test call — well under a cent, on your key."),
  ).toBeInTheDocument();
});

test("a good key verifies and moves on to the budget", async () => {
  render(<FirstRun onDone={() => {}} />);
  await userEvent.click(screen.getByRole("button", { name: /Anthropic/ }));
  await userEvent.type(screen.getByLabelText("Anthropic key"), "sk-ant-demo");
  await userEvent.click(screen.getByRole("button", { name: "Check my key" }));
  expect(screen.getByText("Making the test call…")).toBeInTheDocument();
  await screen.findByText(/Key verified/, undefined, { timeout: 3000 });
  await userEvent.click(screen.getByRole("button", { name: "Set your budget" }));
  expect(
    screen.getByText("How much Tomte may spend from your key per month."),
  ).toBeInTheDocument();
});

test("Azure AI Foundry also collects the per-resource endpoint URL", async () => {
  render(<FirstRun onDone={() => {}} />);
  await userEvent.click(screen.getByRole("button", { name: /Azure AI Foundry/ }));
  expect(screen.getByLabelText("Service address")).toBeInTheDocument();
  expect(screen.getByLabelText("Azure AI Foundry key")).toBeInTheDocument();
});

test("on this computer needs no key and says it plainly", async () => {
  render(<FirstRun onDone={() => {}} />);
  await userEvent.click(screen.getByRole("button", { name: /On this computer/ }));
  expect(screen.getByText("Runs on your computer — free.")).toBeInTheDocument();
});

test("finishing the budget step opens Tomte", async () => {
  const onDone = vi.fn();
  render(<FirstRun onDone={onDone} />);
  await userEvent.click(screen.getByRole("button", { name: /On this computer/ }));
  await userEvent.click(screen.getByRole("button", { name: "Continue" }));
  expect(
    screen.getByText(
      "Tomte starts with your computer so your scheduled work happens without you.",
    ),
  ).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Open Tomte" }));
  expect(onDone).toHaveBeenCalledOnce();
});
