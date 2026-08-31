import { useState } from "react";
import { MCP_ROWS, SLACK_CAPTURE } from "../fixtures/connections";
import { VERIFY_DELAY_MS } from "./FirstRun";
import "./screens.css";

interface Props {
  slackConnected: boolean;
  onSlackConnected: () => void;
  // Set when the build conversation linked here; capture returns to it.
  onBackToBuild?: () => void;
}

export default function Connections({
  slackConnected,
  onSlackConnected,
  onBackToBuild,
}: Props) {
  const [token, setToken] = useState("");
  const [checking, setChecking] = useState(false);

  const wrongPaste =
    token.trim().length > 0 && !token.startsWith(SLACK_CAPTURE.secretPrefix);

  return (
    <div className="screen screen-narrow">
      <h2>Connections</h2>
      <p className="dim">
        Connect a tool once — every job you build finds it here, already connected.
      </p>

      <div className="conn-row">
        <div className="conn-row-top">
          <strong>Slack</strong>
          <span className={`conn-chip ${slackConnected ? "conn-ok" : "conn-missing"}`}>
            {slackConnected ? "✓ connected" : "not connected"}
          </span>
        </div>

        {slackConnected ? (
          <>
            <p className="dim conn-detail">
              Verified with a live check — the token covers everything Tomte's Slack
              actions need. You won't paste this again.
            </p>
            {onBackToBuild && (
              <button className="btn" onClick={onBackToBuild}>
                Back to your build
              </button>
            )}
          </>
        ) : (
          <div className="capture-card">
            <button className="starter capture-start">
              {SLACK_CAPTURE.startLabel} ↗
            </button>
            {SLACK_CAPTURE.steps.map((s, i) => (
              <div key={s} className="capture-step">
                <span className="capture-num">{i + 1}</span>
                <span>{s}</span>
              </div>
            ))}

            <input
              className="key-input"
              placeholder="xoxb-…"
              aria-label="Slack bot token"
              value={token}
              onChange={(e) => setToken(e.target.value)}
            />
            {wrongPaste && (
              <p className="paste-error">
                That doesn't look like a bot token — it should start with{" "}
                {SLACK_CAPTURE.secretPrefix}.
              </p>
            )}

            {checking ? (
              <p className="verify-checking">{SLACK_CAPTURE.verifyingLabel}</p>
            ) : (
              <button
                className="btn"
                disabled={!token.startsWith(SLACK_CAPTURE.secretPrefix)}
                onClick={() => {
                  setChecking(true);
                  setTimeout(() => {
                    setChecking(false);
                    onSlackConnected();
                  }, VERIFY_DELAY_MS);
                }}
              >
                Connect Slack
              </button>
            )}
          </div>
        )}
      </div>

      {MCP_ROWS.map((row) => (
        <div key={row.id} className="conn-row">
          <div className="conn-row-top">
            <strong>{row.name}</strong>
            <span className="conn-chip conn-missing">not connected</span>
          </div>
          <p className="dim conn-detail">{row.detail}</p>
          <button className="starter">Add {row.name.toLowerCase()}</button>
        </div>
      ))}
    </div>
  );
}
