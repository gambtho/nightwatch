import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ComponentProps } from "react";
import Build from "./Build";
import { buildScript } from "../fixtures/conversation";

function renderBuild(overrides: Partial<ComponentProps<typeof Build>> = {}, shown = 1) {
  const props: ComponentProps<typeof Build> = {
    shown,
    onAdvance: () => {},
    slackConnected: false,
    onConnectSlack: () => {},
    onVerdict: () => {},
    ...overrides,
  };
  return render(<Build {...props} />);
}

const connectIndex = buildScript.findIndex((t) => t.connectSlack) + 1;

test("starts with only the first turn and an empty permit", () => {
  renderBuild();
  expect(
    screen.getByText(/Every Monday, look at last week's support tickets/),
  ).toBeInTheDocument();
  expect(screen.getAllByText("Nothing yet")).toHaveLength(2);
});

test("advancing the conversation grows the permit", () => {
  renderBuild({}, 2);
  expect(screen.getByText("Zendesk tickets")).toBeInTheDocument();
});

test("at the connect turn, an unconnected Slack blocks the build behind one paste", async () => {
  const onConnectSlack = vi.fn();
  renderBuild({ onConnectSlack }, connectIndex);
  expect(screen.queryByRole("button", { name: "Continue" })).toBeNull();
  await userEvent.click(
    screen.getByRole("button", { name: "Connect Slack — one paste" }),
  );
  expect(onConnectSlack).toHaveBeenCalledOnce();
});

test("with Slack connected, the connect turn continues and says it will stay connected", () => {
  renderBuild({ slackConnected: true }, connectIndex);
  expect(
    screen.getByText(/every future job finds it already connected/),
  ).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Continue" })).toBeInTheDocument();
});

test("the verdict action appears only at the end of the script", async () => {
  const onVerdict = vi.fn();
  renderBuild({ slackConnected: true, onVerdict }, buildScript.length);
  await userEvent.click(screen.getByRole("button", { name: "See my honest read" }));
  expect(onVerdict).toHaveBeenCalledOnce();
});
