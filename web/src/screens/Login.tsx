import { useState, type FormEvent } from "react";
import { Navigate, useSearchParams } from "react-router-dom";
import { ApiError, requestMagicLink } from "../api/client";
import { useSession } from "../session";
import "./screens.css";

// Signup and login are the same flow: one email field, a magic link, and
// an identical response whether or not the address is known — the server
// never confirms account existence, so neither does this copy.

export default function Login() {
  const { session } = useSession();
  const [params] = useSearchParams();
  const [email, setEmail] = useState("");
  const [state, setState] = useState<"idle" | "sending" | "sent" | "error">("idle");
  const [errorDetail, setErrorDetail] = useState("");

  if (session.status === "signed-in") {
    return <Navigate to="/" replace />;
  }

  const next = params.get("next") ?? undefined;

  async function submit(e: FormEvent) {
    e.preventDefault();
    setState("sending");
    try {
      await requestMagicLink(email, next);
      setState("sent");
    } catch (err) {
      // Distinguish "the server refused" from "we couldn't reach it" —
      // neither is a typo in the address, so don't imply one.
      setErrorDetail(
        err instanceof ApiError
          ? `The server couldn't send it (${err.message}).`
          : "Couldn't reach Tomte.",
      );
      setState("error");
    }
  }

  if (state === "sent") {
    return (
      <div className="screen login">
        <h1>Check your email</h1>
        <p>
          If <strong>{email}</strong> is yours, a sign-in link is on its way. It works
          once, from this browser or any other.
        </p>
        <button className="btn-quiet" onClick={() => setState("idle")}>
          Use a different address
        </button>
      </div>
    );
  }

  return (
    <div className="screen login">
      <h1>Work that gets done while you sleep</h1>
      <p className="dim">
        Sign in with your email. No password — we send you a link instead.
      </p>
      <form onSubmit={(e) => void submit(e)} className="login-form">
        <label className="label" htmlFor="email">
          Email
        </label>
        <input
          id="email"
          type="email"
          required
          autoComplete="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          placeholder="you@example.com"
        />
        <button className="btn" type="submit" disabled={state === "sending"}>
          {state === "sending" ? "Sending…" : "Email me a sign-in link"}
        </button>
        {state === "error" && (
          <p className="error-note">{errorDetail} Try again in a moment.</p>
        )}
      </form>
    </div>
  );
}
