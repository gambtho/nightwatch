import { render, screen } from "@testing-library/react";
import PermitDiagram from "./PermitDiagram";
import { emptyPermit, grant } from "../lib/permit";
import type { Capability } from "../lib/types";

const supportRead: Capability = {
  id: "slack-support-read",
  system: "slack",
  label: "Slack #support",
  access: "read",
  detail: "last 7 days",
};

const digestWrite: Capability = {
  id: "slack-digest-write",
  system: "slack",
  label: "Slack #team-digest",
  access: "write",
  detail: "post only",
};

const permit = grant(grant(emptyPermit(200), supportRead), digestWrite);

test("shows reads and writes with their detail text", () => {
  render(<PermitDiagram permit={permit} highlightIds={[]} maxCostLabel="$2.00 / run" />);
  expect(screen.getByText("Slack #support")).toBeInTheDocument();
  expect(screen.getByText("last 7 days")).toBeInTheDocument();
  expect(screen.getByText("Slack #team-digest")).toBeInTheDocument();
});

test("states the hard limit and the spend cap", () => {
  render(<PermitDiagram permit={permit} highlightIds={[]} maxCostLabel="$2.00 / run" />);
  expect(screen.getByText("CANNOT GO BEYOND THIS LINE")).toBeInTheDocument();
  expect(screen.getByText("$2.00 / run")).toBeInTheDocument();
});

test("lists what it can never do", () => {
  render(<PermitDiagram permit={permit} highlightIds={[]} maxCostLabel="$2.00 / run" />);
  expect(screen.getByText("Email")).toBeInTheDocument();
  expect(screen.getByText("Payments")).toBeInTheDocument();
});

test("marks highlighted capabilities as just added", () => {
  render(
    <PermitDiagram
      permit={permit}
      highlightIds={["slack-digest-write"]}
      maxCostLabel="$2.00 / run"
    />,
  );
  expect(screen.getByTestId("cap-slack-digest-write")).toHaveClass("just-added");
  expect(screen.getByTestId("cap-slack-support-read")).not.toHaveClass("just-added");
});

test("an empty permit shows an empty state on both sides", () => {
  render(
    <PermitDiagram
      permit={emptyPermit(200)}
      highlightIds={[]}
      maxCostLabel="$2.00 / run"
    />,
  );
  expect(screen.getAllByText("Nothing yet")).toHaveLength(2);
});
