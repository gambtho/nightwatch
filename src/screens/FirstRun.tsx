import { useState } from "react";
import { ENDPOINT_PRESETS, type EndpointPreset } from "../fixtures/endpoints";
import { dollars, MONTHLY_BUDGET_CENTS } from "../lib/budget";
import "./screens.css";

type Step = "endpoint" | "key" | "budget";
type VerifyState = "idle" | "checking" | "ok";

// Fake verify latency — long enough to read as a real round trip.
export const VERIFY_DELAY_MS = 800;

function keyLooksRight(preset: EndpointPreset, key: string): boolean {
  if (!key.trim()) return false;
  return preset.keyPrefix ? key.startsWith(preset.keyPrefix) : true;
}

export default function FirstRun({ onDone }: { onDone: () => void }) {
  const [step, setStep] = useState<Step>("endpoint");
  const [preset, setPreset] = useState<EndpointPreset | null>(null);
  const [baseUrl, setBaseUrl] = useState("");
  const [key, setKey] = useState("");
  const [verify, setVerify] = useState<VerifyState>("idle");
  const [budget, setBudget] = useState(String(MONTHLY_BUDGET_CENTS / 100));
  const [autostart, setAutostart] = useState(true);

  const wrongPaste =
    preset?.keyPrefix && key.trim().length > 0 && !key.startsWith(preset.keyPrefix);

  if (step === "endpoint") {
    return (
      <div className="screen screen-narrow">
        <div className="intake-head">
          <div className="intake-moon">🧝</div>
          <h1>Choose where your AI runs</h1>
          <p className="dim">
            Tomte does the work on this computer; a model service does the thinking — on
            your key, under your budget.
          </p>
        </div>

        <div className="preset-grid">
          {ENDPOINT_PRESETS.map((p) => (
            <button
              key={p.id}
              className="preset"
              onClick={() => {
                setPreset(p);
                setKey("");
                setVerify("idle");
                setStep("key");
              }}
            >
              <strong>{p.name}</strong>
              <span className="dim">{p.blurb}</span>
            </button>
          ))}
        </div>

        <p className="dim intake-foot">
          One choice, changeable later in settings. Nothing runs yet.
        </p>
      </div>
    );
  }

  if (step === "key" && preset) {
    if (preset.local) {
      return (
        <div className="screen screen-narrow">
          <h2>On this computer</h2>
          <p className="dim">
            No key needed. Tomte talks to the model server already running here — Ollama,
            LM Studio, and friends.
          </p>
          <div className="block block-can">
            <div className="label">WHAT THAT MEANS</div>
            <div>Runs on your computer — free.</div>
          </div>
          <button className="btn" onClick={() => setStep("budget")}>
            Continue
          </button>
          <button className="btn btn-secondary" onClick={() => setStep("endpoint")}>
            Back
          </button>
        </div>
      );
    }

    return (
      <div className="screen screen-narrow">
        <h2>Connect {preset.name}</h2>
        <p className="dim">Three small steps, done once.</p>

        <div className="capture-card">
          {preset.captureSteps.map((s, i) => (
            <div key={s} className="capture-step">
              <span className="capture-num">{i + 1}</span>
              <span>{s}</span>
            </div>
          ))}

          {preset.needsBaseUrl && (
            <input
              className="key-input"
              placeholder="https://…"
              aria-label="Service address"
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.target.value)}
            />
          )}

          <input
            className="key-input"
            placeholder={preset.keyPrefix ? `${preset.keyPrefix}…` : "Paste your key"}
            aria-label={`${preset.name} key`}
            value={key}
            onChange={(e) => {
              setKey(e.target.value);
              setVerify("idle");
            }}
          />
          {wrongPaste && (
            <p className="paste-error">
              That doesn't look like a {preset.name} key — it should start with{" "}
              {preset.keyPrefix}.
            </p>
          )}

          {verify === "idle" && (
            <>
              <p className="dim capture-disclose">
                We'll make one tiny test call — well under a cent, on your key.
              </p>
              <button
                className="btn"
                disabled={!keyLooksRight(preset, key)}
                onClick={() => {
                  setVerify("checking");
                  setTimeout(() => setVerify("ok"), VERIFY_DELAY_MS);
                }}
              >
                Check my key
              </button>
            </>
          )}
          {verify === "checking" && (
            <p className="verify-checking">Making the test call…</p>
          )}
          {verify === "ok" && (
            <>
              <p className="verify-ok">
                ✓ Key verified — the test call came back fine. Its cost is recorded and
                counts against your month once your budget is set.
              </p>
              <button className="btn" onClick={() => setStep("budget")}>
                Set your budget
              </button>
            </>
          )}
        </div>

        <button className="btn btn-secondary" onClick={() => setStep("endpoint")}>
          Back
        </button>
      </div>
    );
  }

  return (
    <div className="screen screen-narrow">
      <h2>Set a monthly budget</h2>
      <p className="dim">How much Tomte may spend from your key per month.</p>

      <div className="budget-row">
        <span className="budget-dollar">$</span>
        <input
          className="key-input budget-input"
          aria-label="Monthly budget in dollars"
          value={budget}
          onChange={(e) => setBudget(e.target.value)}
        />
        <span className="dim">per month</span>
      </div>
      <p className="dim">
        Suggested to start: {dollars(MONTHLY_BUDGET_CENTS)}. When it's used up, jobs wait
        until the 1st — nothing spends past it. Change it any time in settings.
      </p>
      <p className="dim">
        Tomte meters what goes through Tomte — it can't see other uses of the same key.
      </p>

      <label className="autostart">
        <input
          type="checkbox"
          checked={autostart}
          onChange={(e) => setAutostart(e.target.checked)}
        />
        <span>
          Tomte starts with your computer so your scheduled work happens without you.
        </span>
      </label>

      <button className="btn" onClick={onDone}>
        Open Tomte
      </button>
    </div>
  );
}
