import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SessionProvider } from "../session";
import Login from "./Login";
import { mockApi } from "../test/helpers";

afterEach(() => {
  vi.unstubAllGlobals();
});

function renderLogin(initialPath = "/login") {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <SessionProvider>
        <Login />
      </SessionProvider>
    </MemoryRouter>,
  );
}

describe("Login", () => {
  it("requests a magic link and shows the sent state without confirming the account", async () => {
    const fetchMock = mockApi({
      "GET /v1/me": { status: 401, body: { error: "unauthorized" } },
      "POST /v1/auth/magic-link": { status: 202, body: { ok: true } },
    });
    renderLogin();

    await userEvent.type(screen.getByLabelText(/email/i), "night@example.com");
    await userEvent.click(
      screen.getByRole("button", { name: /email me a sign-in link/i }),
    );

    expect(await screen.findByText(/check your email/i)).toBeInTheDocument();
    expect(screen.getByText("night@example.com")).toBeInTheDocument();

    const linkCall = fetchMock.mock.calls.find(
      ([, init]) => (init as RequestInit | undefined)?.method === "POST",
    );
    expect(JSON.parse((linkCall![1] as RequestInit).body as string)).toEqual({
      email: "night@example.com",
    });
  });

  it("forwards the intended destination as next", async () => {
    const fetchMock = mockApi({
      "GET /v1/me": { status: 401, body: { error: "unauthorized" } },
      "POST /v1/auth/magic-link": { status: 202, body: { ok: true } },
    });
    renderLogin("/login?next=%2Fworkflows%2Fabc");

    await userEvent.type(screen.getByLabelText(/email/i), "night@example.com");
    await userEvent.click(
      screen.getByRole("button", { name: /email me a sign-in link/i }),
    );
    await screen.findByText(/check your email/i);

    const linkCall = fetchMock.mock.calls.find(
      ([, init]) => (init as RequestInit | undefined)?.method === "POST",
    );
    expect(JSON.parse((linkCall![1] as RequestInit).body as string)).toEqual({
      email: "night@example.com",
      next: "/workflows/abc",
    });
  });

  it("surfaces a send failure", async () => {
    mockApi({
      "GET /v1/me": { status: 401, body: { error: "unauthorized" } },
      "POST /v1/auth/magic-link": { status: 500, body: { error: "boom" } },
    });
    renderLogin();

    await userEvent.type(screen.getByLabelText(/email/i), "night@example.com");
    await userEvent.click(
      screen.getByRole("button", { name: /email me a sign-in link/i }),
    );

    expect(await screen.findByText(/couldn't send it \(boom\)/i)).toBeInTheDocument();
  });
});
