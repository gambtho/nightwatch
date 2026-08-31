import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import CaptureCard from "./CaptureCard";

// This file is intentionally byte-identical between the first-run and
// connections branches; see the note in CaptureCard.tsx.

function renderCard(overrides: Partial<Parameters<typeof CaptureCard>[0]> = {}) {
  const onVerify = vi.fn(async () => ({ ok: true }) as const);
  const onVerified = vi.fn();
  render(
    <CaptureCard
      title="Your key"
      steps={["Click Create Key.", "Paste it below."]}
      startUrl="https://example.com/keys"
      startLabel="Open the console"
      placeholder="sk-…"
      onVerify={onVerify}
      onVerified={onVerified}
      {...overrides}
    />,
  );
  return { onVerify, onVerified };
}

describe("CaptureCard", () => {
  it("shows the guide steps and the start link in a new tab", () => {
    renderCard();
    expect(screen.getByText("Click Create Key.")).toBeInTheDocument();
    const link = screen.getByRole("link", { name: /open the console/i });
    expect(link).toHaveAttribute("href", "https://example.com/keys");
    expect(link).toHaveAttribute("target", "_blank");
  });

  it("catches a wrong-shaped paste instantly and blocks the verify", async () => {
    const user = userEvent.setup();
    const { onVerify } = renderCard({
      checkShape: (s) => (s.startsWith("sk-") ? null : "That doesn't look right."),
    });
    await user.type(screen.getByLabelText(/key/i), "xoxb-wrong");
    expect(screen.getByText("That doesn't look right.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /verify/i })).toBeDisabled();
    expect(onVerify).not.toHaveBeenCalled();
  });

  it("names the disclosure before the verify happens", () => {
    renderCard({ disclosure: "We'll make one tiny test call." });
    expect(screen.getByText(/one tiny test call/i)).toBeInTheDocument();
  });

  it("hands a verified secret to onVerified", async () => {
    const user = userEvent.setup();
    const { onVerify, onVerified } = renderCard();
    await user.type(screen.getByLabelText(/key/i), "sk-good");
    await user.click(screen.getByRole("button", { name: /verify/i }));
    expect(onVerify).toHaveBeenCalledWith("sk-good");
    expect(onVerified).toHaveBeenCalledWith("sk-good");
  });

  it("shows a failed verify and keeps the paste", async () => {
    const user = userEvent.setup();
    const { onVerified } = renderCard({
      onVerify: async () => ({ ok: false, message: "The service said no." }),
    });
    await user.type(screen.getByLabelText(/key/i), "sk-bad");
    await user.click(screen.getByRole("button", { name: /verify/i }));
    expect(await screen.findByText("The service said no.")).toBeInTheDocument();
    expect(onVerified).not.toHaveBeenCalled();
    expect(screen.getByLabelText(/key/i)).toHaveValue("sk-bad");
  });

  it("masks the secret until asked to show it", async () => {
    const user = userEvent.setup();
    renderCard();
    const input = screen.getByLabelText(/key/i);
    expect(input).toHaveAttribute("type", "password");
    await user.click(screen.getByRole("button", { name: /show/i }));
    expect(input).toHaveAttribute("type", "text");
  });
});
