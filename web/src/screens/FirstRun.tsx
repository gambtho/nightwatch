import { useState } from "react";
import { useNavigate } from "react-router-dom";
import CaptureCard from "../components/CaptureCard";
import EndpointChooser from "../components/EndpointChooser";
import {
  keyShapeError,
  presetById,
  validateBaseUrl,
  type EndpointPreset,
  type EndpointRecord,
  type PresetId,
} from "../local/endpoints";
import { saveBudget, saveEndpoint, verifyEndpointKey } from "../local/config";
import { SUGGESTED_BUDGET_CENTS } from "../local/config";
import "./screens.css";
import "./endpoint.css";

// First run (pivot spec, "First run — paste a key and go"): choose where
// your AI runs → paste a key, verified with one disclosed, metered test
// call before anything else is asked → set a monthly budget → land in
// the build conversation. "On this computer" skips key and budget — $0
// by explicit classification.
//
// Prototype notes: mounted at /welcome behind the existing session gate;
// in the packaged app the shell mints the session and opens this screen
// first, and this route becomes the window's first paint. The endpoint,
// verify, and budget calls are the local/config.ts fake seam until P1's
// API lands.

const VERIFY_DISCLOSURE =
  "To check the key, we'll make one tiny test call — well under a cent, on your key. It counts toward your budget once you set one.";

type Phase =
  | { step: "choose" }
  | { step: "capture"; preset: EndpointPreset }
  | { step: "budget"; preset: EndpointPreset };

function recordFor(preset: EndpointPreset, baseUrl: string): EndpointRecord {
  return {
    kind: preset.kind,
    preset: preset.id,
    base_url: preset.baseUrl ?? baseUrl,
    local: preset.local === true,
  };
}

export default function FirstRun() {
  const navigate = useNavigate();
  const [phase, setPhase] = useState<Phase>({ step: "choose" });
  const [baseUrl, setBaseUrl] = useState("");
  const [baseUrlError, setBaseUrlError] = useState<string | null>(null);
  const [budgetText, setBudgetText] = useState(String(SUGGESTED_BUDGET_CENTS / 100));
  const [budgetError, setBudgetError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  function choose(id: PresetId) {
    const preset = presetById(id);
    setBaseUrl(preset.needsBaseUrl ? "" : (preset.baseUrl ?? ""));
    setBaseUrlError(null);
    setPhase({ step: "capture", preset });
  }

  async function verifyKey(preset: EndpointPreset, key: string) {
    if (preset.needsBaseUrl) {
      const checked = validateBaseUrl(baseUrl, {
        requireLoopback: preset.requireLoopback,
      });
      if (!checked.ok) return checked;
    }
    return verifyEndpointKey(preset, key);
  }

  async function keyVerified(preset: EndpointPreset) {
    let url = preset.baseUrl ?? "";
    if (preset.needsBaseUrl) {
      const checked = validateBaseUrl(baseUrl, {
        requireLoopback: preset.requireLoopback,
      });
      if (!checked.ok) {
        // verifyKey already re-checks the URL, so this shouldn't fire —
        // but an unvalidated address is never persisted on a race.
        setBaseUrlError(checked.message);
        return;
      }
      url = checked.url;
    }
    await saveEndpoint(recordFor(preset, url));
    setPhase({ step: "budget", preset });
  }

  async function finishLocal(preset: EndpointPreset) {
    const checked = validateBaseUrl(baseUrl, { requireLoopback: true });
    if (!checked.ok) {
      setBaseUrlError(checked.message);
      return;
    }
    setSaving(true);
    try {
      await saveEndpoint(recordFor(preset, checked.url));
      navigate("/build");
    } finally {
      setSaving(false);
    }
  }

  async function finishBudget() {
    const trimmed = budgetText.trim();
    const dollars = /^\d+(\.\d{1,2})?$/.test(trimmed) ? Number(trimmed) : NaN;
    if (!(dollars > 0)) {
      setBudgetError("Enter a dollar amount, like 10.");
      return;
    }
    setBudgetError(null);
    setSaving(true);
    try {
      await saveBudget(Math.round(dollars * 100));
      navigate("/build");
    } finally {
      setSaving(false);
    }
  }

  if (phase.step === "choose") {
    return (
      <div className="screen">
        <h1>Choose where your AI runs</h1>
        <p className="dim">
          Tomte does its work through an AI service of your choice, with a key you bring.
          You can switch later in settings.
        </p>
        <EndpointChooser onChoose={choose} />
      </div>
    );
  }

  const { preset } = phase;

  if (phase.step === "capture") {
    const baseUrlField = preset.needsBaseUrl && (
      <label className="endpoint-baseurl">
        <span className="label">
          {preset.id === "azure" ? "Your resource's endpoint URL" : "Address"}
        </span>
        <input
          value={baseUrl}
          onChange={(e) => {
            setBaseUrl(e.target.value);
            setBaseUrlError(null);
          }}
          placeholder={preset.baseUrlPlaceholder}
          spellCheck={false}
        />
        {baseUrlError && <p className="error-note">{baseUrlError}</p>}
      </label>
    );

    if (!preset.needsKey) {
      // "On this computer": no key, no budget — free by construction.
      return (
        <div className="screen">
          <h1>{preset.label}</h1>
          <p className="dim">
            Point Tomte at the model server running on this computer. No key, and runs are
            free — it's your own machine doing the work.
          </p>
          {baseUrlField}
          <div className="endpoint-actions">
            <button
              className="btn"
              onClick={() => void finishLocal(preset)}
              disabled={saving}
            >
              {saving ? "Saving…" : "Use this computer"}
            </button>
            <button
              className="btn-quiet"
              onClick={() => setPhase({ step: "choose" })}
              disabled={saving}
            >
              ← Different service
            </button>
          </div>
        </div>
      );
    }

    return (
      <div className="screen">
        <h1>Connect {preset.label}</h1>
        <CaptureCard
          title={`Your ${preset.label} key`}
          steps={preset.guide?.steps ?? []}
          startUrl={preset.guide?.startUrl}
          startLabel={preset.guide?.startLabel}
          placeholder={preset.keyPlaceholder}
          disclosure={VERIFY_DISCLOSURE}
          verifyLabel="Check the key"
          checkShape={(key) => keyShapeError(preset, key)}
          onVerify={(key) => verifyKey(preset, key)}
          onVerified={() => void keyVerified(preset)}
        >
          {baseUrlField}
        </CaptureCard>
        <button className="btn-quiet" onClick={() => setPhase({ step: "choose" })}>
          ← Different service
        </button>
      </div>
    );
  }

  return (
    <div className="screen">
      <h1>Set a monthly budget</h1>
      <p className="dim endpoint-budget-copy">
        How much may Tomte spend from your key per month? It checks before every call and
        stops when the month's budget is used up. The test call from a moment ago counts
        toward it.
      </p>
      {preset.budgetNote && (
        <p className="dim endpoint-budget-copy">{preset.budgetNote}</p>
      )}
      <label className="endpoint-budget">
        <span className="label">Per month</span>
        <div className="endpoint-budget-row">
          <span aria-hidden="true">$</span>
          <input
            value={budgetText}
            onChange={(e) => setBudgetText(e.target.value)}
            inputMode="decimal"
            aria-label="Monthly budget in dollars"
          />
        </div>
      </label>
      {budgetError && <p className="error-note">{budgetError}</p>}
      <div className="endpoint-actions">
        <button className="btn" onClick={() => void finishBudget()} disabled={saving}>
          {saving ? "Saving…" : "Set budget and start"}
        </button>
      </div>
      <p className="dim endpoint-foot">
        Tomte can only count what goes through Tomte — other uses of the same key are
        invisible to it. Change the budget any time in settings.
      </p>
    </div>
  );
}
