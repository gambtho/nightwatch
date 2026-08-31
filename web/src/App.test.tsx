import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { SessionProvider } from "./session";
import { mockApi } from "./test/helpers";

afterEach(() => {
  vi.unstubAllGlobals();
});

// There is no login screen: the session is minted outside the SPA (shell or
// `tomte dev-session`) and delivered via GET /local/handoff. A browser that
// arrives without it gets an explanation, not a dead redirect.
describe("App without a session", () => {
  it("shows the signed-out notice with the dev-session hint", async () => {
    mockApi({ "GET /v1/me": { status: 401, body: { error: "unauthenticated" } } });
    render(
      <MemoryRouter>
        <SessionProvider>
          <App />
        </SessionProvider>
      </MemoryRouter>,
    );
    expect(
      await screen.findByText(/this browser isn't signed in to tomte/i),
    ).toBeInTheDocument();
    expect(screen.getByText(/tomte dev-session/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /check again/i })).toBeInTheDocument();
  });
});
