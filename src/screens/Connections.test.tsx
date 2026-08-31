import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import Connections from "./Connections";

test("lists Slack and the MCP-road rows with their state", () => {
  render(<Connections slackConnected={false} onSlackConnected={() => {}} />);
  expect(screen.getByText("Slack")).toBeInTheDocument();
  expect(screen.getByText("Calendar")).toBeInTheDocument();
  expect(screen.getByText("Inbox")).toBeInTheDocument();
  expect(screen.getAllByText("not connected")).toHaveLength(3);
});

test("shows the manifest-flow capture steps from the catalog guide", () => {
  render(<Connections slackConnected={false} onSlackConnected={() => {}} />);
  expect(
    screen.getByText("Click Create App — we've pre-filled what Tomte needs."),
  ).toBeInTheDocument();
  expect(
    screen.getByText("Copy the token that starts with xoxb- and paste it below."),
  ).toBeInTheDocument();
});

test("a wrong-shaped token paste is caught instantly", async () => {
  render(<Connections slackConnected={false} onSlackConnected={() => {}} />);
  await userEvent.type(screen.getByLabelText("Slack bot token"), "xoxp-user-token");
  expect(screen.getByText(/it should start with xoxb-/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Connect Slack" })).toBeDisabled();
});

test("a good token verifies with Slack before it is kept", async () => {
  const onSlackConnected = vi.fn();
  render(<Connections slackConnected={false} onSlackConnected={onSlackConnected} />);
  await userEvent.type(screen.getByLabelText("Slack bot token"), "xoxb-demo");
  await userEvent.click(screen.getByRole("button", { name: "Connect Slack" }));
  expect(screen.getByText("Checking with Slack…")).toBeInTheDocument();
  await vi.waitFor(() => expect(onSlackConnected).toHaveBeenCalledOnce(), {
    timeout: 3000,
  });
});

test("once connected, capture is gone and the build can be returned to", async () => {
  const onBackToBuild = vi.fn();
  render(
    <Connections
      slackConnected
      onSlackConnected={() => {}}
      onBackToBuild={onBackToBuild}
    />,
  );
  expect(screen.getByText("✓ connected")).toBeInTheDocument();
  expect(screen.queryByLabelText("Slack bot token")).toBeNull();
  expect(screen.getByText(/You won't paste this again/)).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Back to your build" }));
  expect(onBackToBuild).toHaveBeenCalledOnce();
});
