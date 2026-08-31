import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { isAuthError, listWorkflows } from "../api/client";
import CaptureCard from "../components/CaptureCard";
import EndpointChooser from "../components/EndpointChooser";
import {
  endpointLabel,
  keyShapeError,
  presetById,
  validateBaseUrl,
  type EndpointPreset,
  type EndpointRecord,
  type PresetId,
} from "../local/endpoints";
import {
  getLocalConfig,
  saveBudget,
  saveEndpoint,
  savePrice,
  setAutostart,
  unpricedModels,
  verifyEndpointKey,
  type LocalConfig,
} from "../local/config";
import { dollars } from "../lib/format";
import { useSession } from "../session";
import "./screens.css";
import "./endpoint.css";

// Settings (pivot spec, "Endpoint agnosticism" + "Vault and metering"):
// the endpoint, the budget, autostart. Switching endpoints is a
// governance act, not a silent setting — the switch is an explicit
// confirmation naming the workflows it affects, and the pricing gate
// re-runs against the new endpoint before the switch completes, so an
// unpriced model gets the two-number price form here, not a failed 3 AM
// run. Backed by the local/config.ts fake seam until P1's API lands.

const VERIFY_DISCLOSURE =
  "To check the key, we'll make one tiny test call — well under a cent, on your key. It counts toward this month's budget.";

type Switch =
  | null
  | { step: "choose" }
  | { step: "capture"; preset: EndpointPreset }
  | { step: "confirm"; record: EndpointRecord; unpriced: string[] };

interface PriceDraft {
  in: string;
  out: string;
}

function parseDollars(text: string): number | null {
  const trimmed = text.trim();
  if (!/^\d+(\.\d{1,2})?$/.test(trimmed)) return null;
  const value = Number(trimmed);
  return value > 0 ? value : null;
}

export default function Settings() {
  const { expire } = useSession();
  const [config, setConfig] = useState<LocalConfig | null>(null);
  const [workflowCount, setWorkflowCount] = useState<number | null>(null);

  const [sw, setSw] = useState<Switch>(null);
  const [baseUrl, setBaseUrl] = useState("");
  const [baseUrlError, setBaseUrlError] = useState<string | null>(null);
  const [prices, setPrices] = useState<Record<string, PriceDraft>>({});
  const [switching, setSwitching] = useState(false);

  const [budgetText, setBudgetText] = useState("");
  const [budgetEditing, setBudgetEditing] = useState(false);
  const [budgetError, setBudgetError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    getLocalConfig().then((c) => {
      if (cancelled) return;
      setConfig(c);
      if (c.monthly_budget_cents !== null) {
        setBudgetText(String(c.monthly_budget_cents / 100));
      }
    });
    listWorkflows()
      .then(({ workflows }) => {
        if (!cancelled) setWorkflowCount(workflows.length);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        if (isAuthError(err)) {
          expire();
          return;
        }
        setWorkflowCount(null); // unknown — the confirmation degrades honestly
      });
    return () => {
      cancelled = true;
    };
  }, [expire]);

  function choose(id: PresetId) {
    const preset = presetById(id);
    setBaseUrl(preset.needsBaseUrl ? "" : (preset.baseUrl ?? ""));
    setBaseUrlError(null);
    setSw({ step: "capture", preset });
  }

  function recordFor(preset: EndpointPreset): EndpointRecord | { error: string } {
    let url = preset.baseUrl ?? "";
    if (preset.needsBaseUrl) {
      const checked = validateBaseUrl(baseUrl, {
        requireLoopback: preset.requireLoopback,
      });
      if (!checked.ok) return { error: checked.message };
      url = checked.url;
    }
    return {
      kind: preset.kind,
      preset: preset.id,
      base_url: url,
      local: preset.local === true,
    };
  }

  async function toConfirm(preset: EndpointPreset) {
    const record = recordFor(preset);
    if ("error" in record) {
      setBaseUrlError(record.error);
      return;
    }
    const unpriced = await unpricedModels(record);
    setPrices(Object.fromEntries(unpriced.map((m) => [m, { in: "", out: "" }])));
    setSw({ step: "confirm", record, unpriced });
  }

  async function verifyKey(preset: EndpointPreset, key: string) {
    const record = recordFor(preset);
    if ("error" in record) return { ok: false as const, message: record.error };
    return verifyEndpointKey(preset, key);
  }

  const pricesValid =
    sw?.step === "confirm" &&
    sw.unpriced.every((m) => {
      const draft = prices[m];
      return draft && parseDollars(draft.in) !== null && parseDollars(draft.out) !== null;
    });

  async function confirmSwitch() {
    if (sw?.step !== "confirm" || !pricesValid) return;
    setSwitching(true);
    // The fake seam never rejects; the finally keeps the dialog usable
    // once these become real API calls under P1.
    try {
      for (const model of sw.unpriced) {
        const draft = prices[model]!;
        await savePrice(
          sw.record.base_url,
          model,
          Math.round(parseDollars(draft.in)! * 100),
          Math.round(parseDollars(draft.out)! * 100),
        );
      }
      await saveEndpoint(sw.record);
      setConfig((c) => (c ? { ...c, endpoint: sw.record } : c));
      setSw(null);
    } finally {
      setSwitching(false);
    }
  }

  async function submitBudget() {
    const value = parseDollars(budgetText);
    if (value === null) {
      setBudgetError("Enter a dollar amount, like 10.");
      return;
    }
    setBudgetError(null);
    const cents = Math.round(value * 100);
    await saveBudget(cents);
    setConfig((c) => (c ? { ...c, monthly_budget_cents: cents } : c));
    setBudgetEditing(false);
  }

  async function toggleAutostart() {
    if (!config) return;
    const next = !config.autostart;
    // Optimistic: the fake seam's writes never reject (see local/config).
    setConfig({ ...config, autostart: next });
    await setAutostart(next);
  }

  if (config === null) {
    return <div className="screen dim">Loading…</div>;
  }

  return (
    <div className="screen">
      <h1>Settings</h1>

      <section className="settings-section">
        <div className="label">Where your AI runs</div>
        {config.endpoint === null ? (
          <p className="dim">
            Not set up yet. <Link to="/welcome">Choose where your AI runs.</Link>
          </p>
        ) : (
          <>
            <p>
              {endpointLabel(config.endpoint)}{" "}
              <span className="dim">· {config.endpoint.base_url}</span>
              {config.endpoint.local && (
                <span className="dim"> · runs on your computer — free</span>
              )}
            </p>
            {sw === null && (
              <button
                className="btn btn-secondary"
                onClick={() => setSw({ step: "choose" })}
              >
                Switch service…
              </button>
            )}
          </>
        )}

        {sw?.step === "choose" && (
          <>
            <EndpointChooser onChoose={choose} />
            <button className="btn-quiet" onClick={() => setSw(null)}>
              Keep{" "}
              {config.endpoint ? endpointLabel(config.endpoint) : "the current setup"}
            </button>
          </>
        )}

        {sw?.step === "capture" && (
          <>
            {sw.preset.needsBaseUrl && (
              <label className="endpoint-baseurl">
                <span className="label">
                  {sw.preset.id === "azure" ? "Your resource's endpoint URL" : "Address"}
                </span>
                <input
                  value={baseUrl}
                  onChange={(e) => {
                    setBaseUrl(e.target.value);
                    setBaseUrlError(null);
                  }}
                  placeholder={sw.preset.baseUrlPlaceholder}
                  spellCheck={false}
                />
                {baseUrlError && <p className="error-note">{baseUrlError}</p>}
              </label>
            )}
            {sw.preset.needsKey ? (
              <CaptureCard
                title={`Your ${sw.preset.label} key`}
                steps={sw.preset.guide?.steps ?? []}
                startUrl={sw.preset.guide?.startUrl}
                startLabel={sw.preset.guide?.startLabel}
                placeholder={sw.preset.keyPlaceholder}
                disclosure={VERIFY_DISCLOSURE}
                verifyLabel="Check the key"
                checkShape={(key) => keyShapeError(sw.preset, key)}
                onVerify={(key) => verifyKey(sw.preset, key)}
                onVerified={() => void toConfirm(sw.preset)}
              />
            ) : (
              <button className="btn" onClick={() => void toConfirm(sw.preset)}>
                Continue
              </button>
            )}
            <button className="btn-quiet" onClick={() => setSw({ step: "choose" })}>
              ← Different service
            </button>
          </>
        )}

        {sw?.step === "confirm" && (
          <div className="endpoint-confirm">
            <p>
              {workflowCount === null
                ? "Your workflows will now run against "
                : workflowCount === 0
                  ? "You have no workflows yet; new ones will run against "
                  : `Your ${workflowCount} workflow${workflowCount === 1 ? "" : "s"} will now run against `}
              <strong>{endpointLabel(sw.record)}</strong>{" "}
              <span className="dim">({sw.record.base_url})</span>.
            </p>
            <p className="dim">
              {sw.record.local
                ? "Runs on your computer — free. Your approved spend caps stay in place."
                : "Your approved per-run caps and monthly budget apply there exactly as before. The switch is recorded."}
            </p>
            {sw.unpriced.length > 0 && (
              <div className="endpoint-prices">
                <p className="dim">
                  Tomte doesn't know this service's prices, and it never spends unmetered
                  — enter them from the service's{" "}
                  {presetById(sw.record.preset).pricingUrl ? (
                    <a
                      href={presetById(sw.record.preset).pricingUrl}
                      target="_blank"
                      rel="noreferrer"
                    >
                      pricing page
                    </a>
                  ) : (
                    "pricing page"
                  )}{" "}
                  to switch.
                </p>
                {sw.unpriced.map((model) => (
                  <div key={model} className="endpoint-price-row">
                    <span className="endpoint-price-model">{model}</span>
                    <label>
                      <span className="label">$ / million tokens in</span>
                      <input
                        value={prices[model]?.in ?? ""}
                        inputMode="decimal"
                        onChange={(e) =>
                          setPrices((p) => ({
                            ...p,
                            [model]: { in: e.target.value, out: p[model]?.out ?? "" },
                          }))
                        }
                      />
                    </label>
                    <label>
                      <span className="label">$ / million tokens out</span>
                      <input
                        value={prices[model]?.out ?? ""}
                        inputMode="decimal"
                        onChange={(e) =>
                          setPrices((p) => ({
                            ...p,
                            [model]: { in: p[model]?.in ?? "", out: e.target.value },
                          }))
                        }
                      />
                    </label>
                  </div>
                ))}
              </div>
            )}
            <div className="endpoint-actions">
              <button
                className="btn"
                onClick={() => void confirmSwitch()}
                disabled={!pricesValid || switching}
              >
                {switching ? "Switching…" : "Switch"}
              </button>
              <button
                className="btn-quiet"
                onClick={() => setSw(null)}
                disabled={switching}
              >
                Cancel
              </button>
            </div>
          </div>
        )}
      </section>

      <section className="settings-section">
        <div className="label">Monthly budget</div>
        {config.endpoint?.local ? (
          <p className="dim">
            Runs on your computer are free, so the budget isn't in use. It comes back if
            you switch to a paid service.
          </p>
        ) : budgetEditing ? (
          <>
            <div className="endpoint-budget-row">
              <span aria-hidden="true">$</span>
              <input
                value={budgetText}
                onChange={(e) => setBudgetText(e.target.value)}
                inputMode="decimal"
                aria-label="Monthly budget in dollars"
              />
              <button className="btn" onClick={() => void submitBudget()}>
                Save
              </button>
              <button className="btn-quiet" onClick={() => setBudgetEditing(false)}>
                Cancel
              </button>
            </div>
            {budgetError && <p className="error-note">{budgetError}</p>}
          </>
        ) : (
          <p>
            {config.monthly_budget_cents === null
              ? "No budget set."
              : `${dollars(config.monthly_budget_cents)} per month`}{" "}
            <button className="btn-quiet" onClick={() => setBudgetEditing(true)}>
              Change
            </button>
          </p>
        )}
        <p className="dim endpoint-foot">
          How much Tomte may spend from your key this month. It meters what goes through
          Tomte — it can't see other uses of the same key.
        </p>
      </section>

      <section className="settings-section">
        <div className="label">Starting up</div>
        <label className="setup-check">
          <input
            type="checkbox"
            checked={config.autostart}
            onChange={() => void toggleAutostart()}
          />
          Start Tomte when you log in
        </label>
        <p className="dim endpoint-foot">
          Tomte starts with your computer so your scheduled work happens without you.
          Turning this off means nothing runs until you open Tomte yourself.
        </p>
      </section>
    </div>
  );
}
