import { useState, type ReactNode } from "react";
import "./CaptureCard.css";

// The guided capture card (pivot spec, "Credentials without OAuth"):
// with OAuth gone, "go here, click this, paste this" is the front step
// for every secret in the product — LLM keys at first run, connector
// tokens and MCP keys in the connections manager. Structure, not prose:
// numbered steps, an instant shape check at paste, and a live verify
// before anything is stored. Any spend the verify causes is disclosed
// (`disclosure`) before it happens, never after.
//
// This file is intentionally byte-identical between the first-run and
// connections branches so the PRs stay independently mergeable; edit it
// in both or after both merge.

export type CaptureVerify = { ok: true } | { ok: false; message: string };

export interface CaptureCardProps {
  title: string;
  steps: string[];
  startUrl?: string;
  startLabel?: string;
  secretLabel?: string;
  placeholder?: string;
  /** Named before the verify happens, e.g. the metered test call. */
  disclosure?: string;
  verifyLabel?: string;
  /** Instant wrong-string-paste check; a message blocks the verify. */
  checkShape?: (secret: string) => string | null;
  onVerify: (secret: string) => Promise<CaptureVerify>;
  onVerified: (secret: string) => void;
  /** Extra fields above the secret input (e.g. a per-resource URL). */
  children?: ReactNode;
}

export default function CaptureCard(props: CaptureCardProps) {
  const [secret, setSecret] = useState("");
  const [shown, setShown] = useState(false);
  const [verifying, setVerifying] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const shapeError = secret !== "" ? (props.checkShape?.(secret) ?? null) : null;

  async function verify() {
    setError(null);
    setVerifying(true);
    try {
      const result = await props.onVerify(secret);
      if (result.ok) {
        props.onVerified(secret);
      } else {
        setError(result.message);
        setVerifying(false);
      }
    } catch {
      setError("Couldn't check the key. Your paste is untouched — try again.");
      setVerifying(false);
    }
  }

  return (
    <div className="capture-card">
      <div className="capture-title">{props.title}</div>
      {props.startUrl && (
        <a
          className="btn btn-secondary capture-start"
          href={props.startUrl}
          target="_blank"
          rel="noreferrer"
        >
          {props.startLabel ?? "Open it"} ↗
        </a>
      )}
      <ol className="capture-steps dim">
        {props.steps.map((step) => (
          <li key={step}>{step}</li>
        ))}
      </ol>
      {props.children}
      <label className="capture-secret">
        <span className="label">{props.secretLabel ?? "Key"}</span>
        <div className="capture-secret-row">
          <input
            type={shown ? "text" : "password"}
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
            placeholder={props.placeholder}
            autoComplete="off"
            spellCheck={false}
          />
          <button
            type="button"
            className="btn-quiet"
            onClick={() => setShown((s) => !s)}
            aria-pressed={shown}
          >
            {shown ? "Hide" : "Show"}
          </button>
        </div>
      </label>
      {shapeError && <p className="error-note">{shapeError}</p>}
      {props.disclosure && <p className="dim capture-disclosure">{props.disclosure}</p>}
      {error && <p className="error-note">{error}</p>}
      <button
        type="button"
        className="btn"
        onClick={() => void verify()}
        disabled={secret === "" || shapeError !== null || verifying}
      >
        {verifying ? "Checking…" : (props.verifyLabel ?? "Verify")}
      </button>
    </div>
  );
}
