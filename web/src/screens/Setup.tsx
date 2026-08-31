import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { createWorkflow, getCatalog, isAuthError } from "../api/client";
import type { CatalogConnector } from "../api/types";
import PermitDiagram from "../components/PermitDiagram";
import { connectionView, findOp, opLabel } from "../lib/catalog";
import { providerLabel } from "../lib/permit";
import { buildPermitDoc, parseResourceList } from "../lib/permitBuild";
import type { PermitBuild } from "../lib/permitBuild";
import {
  STEP_TEXT_MAX,
  STEPS_MAX,
  slugify,
  toCreateBody,
  validateDraft,
} from "../lib/setupForm";
import type { SetupDraft, StepDraft } from "../lib/setupForm";
import { useSession } from "../session";
import "./screens.css";

// Developer setup: a hand-written form for the version-1 document, for
// developers and demos only. The product's real front door is the build
// conversation (docs/superpowers/specs/2026-08-31-nightshift-build-
// conversation-design.md), which replaces this screen; the UX design
// names "developers who would rather write the YAML" as an explicit
// non-user, and this is precisely that. It hands the created draft to
// the approve screen — the one gate — rather than duplicating it.

const PROVIDERS = ["anthropic", "openai", "openrouter"];

function browserZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

export default function Setup() {
  const navigate = useNavigate();
  const { expire } = useSession();

  const [name, setName] = useState("");
  const [steps, setSteps] = useState<StepDraft[]>([{ id: "", text: "" }]);
  const [scheduleEnabled, setScheduleEnabled] = useState(false);
  const [cron, setCron] = useState("0 9 * * 1");
  const [tz, setTz] = useState(browserZone);

  const [providers, setProviders] = useState<string[]>(["anthropic"]);
  const [connection, setConnection] = useState("default");
  const [spendText, setSpendText] = useState("50");

  // undefined = still loading; null = unreachable (grants unavailable).
  const [catalog, setCatalog] = useState<CatalogConnector[] | null | undefined>();
  const [opsByConnector, setOpsByConnector] = useState<Record<string, string[]>>({});
  const [resourceText, setResourceText] = useState<Record<string, string>>({});

  const [errors, setErrors] = useState<string[]>([]);
  const [serverError, setServerError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    let cancelled = false;
    getCatalog()
      .then(({ connectors }) => {
        if (!cancelled) setCatalog(connectors);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        if (isAuthError(err)) {
          expire();
          return;
        }
        setCatalog(null);
      });
    return () => {
      cancelled = true;
    };
  }, [expire]);

  function resourcesFor(connectorId: string, opName: string): Record<string, string[]> {
    const connector = catalog?.find((c) => c.id === connectorId);
    const fields = (connector && findOp(connector, opName)?.constraints) ?? [];
    return Object.fromEntries(
      fields.map((field) => [
        field,
        parseResourceList(resourceText[`${connectorId}.${opName}.${field}`] ?? ""),
      ]),
    );
  }

  const permitBuild: PermitBuild = useMemo(() => {
    const spend = spendText.trim();
    return {
      providers,
      connection,
      perRunCents: spend === "" ? undefined : Number(spend),
      grants: Object.entries(opsByConnector).map(([connector, ops]) => ({
        connector,
        ops: ops.map((op) => ({ op, resources: resourcesFor(connector, op) })),
      })),
    };
  }, [providers, connection, spendText, opsByConnector, resourceText, catalog]);

  function currentDraft(): SetupDraft {
    return { name, steps, scheduleEnabled, cron, tz, permit: permitBuild };
  }

  function setStep(i: number, patch: Partial<StepDraft>) {
    setSteps((prev) => prev.map((s, j) => (j === i ? { ...s, ...patch } : s)));
  }

  function toggleProvider(p: string) {
    setProviders((prev) =>
      prev.includes(p) ? prev.filter((x) => x !== p) : [...prev, p],
    );
  }

  function toggleOp(connectorId: string, opName: string) {
    setOpsByConnector((prev) => {
      const ops = prev[connectorId] ?? [];
      const next = ops.includes(opName)
        ? ops.filter((o) => o !== opName)
        : [...ops, opName];
      return { ...prev, [connectorId]: next };
    });
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setServerError(null);
    const draft = currentDraft();
    const found = validateDraft(draft, catalog ?? null);
    setErrors(found);
    if (found.length > 0) return;
    setSubmitting(true);
    try {
      const { workflow } = await createWorkflow(toCreateBody(draft));
      navigate(`/workflows/${workflow.id}/approve`);
    } catch (err) {
      if (isAuthError(err)) {
        expire();
        return;
      }
      setServerError(err instanceof Error ? err.message : "couldn't create");
      setSubmitting(false);
    }
  }

  return (
    <div className="screen setup">
      <h1>Developer setup</h1>
      <p className="dim">
        The hand-written path, for development and demos — not how Nightshift is meant to
        be handed a job. The <Link to="/build">build conversation</Link> replaces this.
        Whatever you write here still waits at the approval gate before it can run.
      </p>

      <form className="setup-form" onSubmit={(e) => void submit(e)}>
        <section className="setup-section">
          <label className="label" htmlFor="setup-name">
            Name
          </label>
          <input
            id="setup-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="weekly ticket digest"
          />
        </section>

        <section className="setup-section">
          <div className="label">Steps (1–{STEPS_MAX}, in job language)</div>
          {steps.map((step, i) => (
            <div key={i} className="setup-step">
              <textarea
                aria-label={`Step ${i + 1} text`}
                value={step.text}
                rows={2}
                maxLength={STEP_TEXT_MAX + 100}
                onChange={(e) => setStep(i, { text: e.target.value })}
                placeholder="Look at last week's support tickets."
              />
              <div className="setup-step-meta">
                <input
                  aria-label={`Step ${i + 1} id`}
                  className="setup-step-id"
                  value={step.id}
                  onChange={(e) => setStep(i, { id: e.target.value })}
                  placeholder={slugify(step.text) || "step id (auto)"}
                />
                <span
                  className={
                    step.text.trim().length > STEP_TEXT_MAX ? "error-note" : "dim"
                  }
                >
                  {step.text.trim().length}/{STEP_TEXT_MAX}
                </span>
                {steps.length > 1 && (
                  <button
                    type="button"
                    className="btn-quiet"
                    onClick={() => setSteps((prev) => prev.filter((_, j) => j !== i))}
                  >
                    Remove
                  </button>
                )}
              </div>
            </div>
          ))}
          {steps.length < STEPS_MAX && (
            <button
              type="button"
              className="btn btn-secondary"
              onClick={() => setSteps((prev) => [...prev, { id: "", text: "" }])}
            >
              + Add step
            </button>
          )}
        </section>

        <section className="setup-section">
          <label className="setup-check">
            <input
              type="checkbox"
              checked={scheduleEnabled}
              onChange={(e) => setScheduleEnabled(e.target.checked)}
            />
            Run on a schedule
          </label>
          {scheduleEnabled && (
            <div className="setup-schedule">
              <label>
                <span className="label">Cron (5 fields)</span>
                <input
                  value={cron}
                  onChange={(e) => setCron(e.target.value)}
                  placeholder="0 9 * * 1"
                />
              </label>
              <label>
                <span className="label">IANA time zone</span>
                <input
                  value={tz}
                  onChange={(e) => setTz(e.target.value)}
                  placeholder="America/New_York"
                />
              </label>
            </div>
          )}
        </section>

        <section className="setup-section">
          <div className="label">Model providers it may call</div>
          {PROVIDERS.map((p) => (
            <label key={p} className="setup-check">
              <input
                type="checkbox"
                checked={providers.includes(p)}
                onChange={() => toggleProvider(p)}
              />
              {providerLabel(p)}
            </label>
          ))}
          <div className="setup-permit-row">
            <label>
              <span className="label">Credential connection</span>
              <input
                value={connection}
                onChange={(e) => setConnection(e.target.value)}
                placeholder="default"
              />
            </label>
            <label>
              <span className="label">Per-run spend cap (cents)</span>
              <input
                value={spendText}
                onChange={(e) => setSpendText(e.target.value)}
                inputMode="numeric"
                placeholder="none"
              />
            </label>
          </div>
        </section>

        <section className="setup-section">
          <div className="label">Connected systems it may reach</div>
          {catalog === undefined && <p className="dim">Loading the catalog…</p>}
          {catalog === null && (
            <p className="dim">
              The connector catalog couldn't be loaded, so this version can't grant any
              connected systems. Model access and spend still apply.
            </p>
          )}
          {catalog?.map((connector) => {
            const conn = connectionView(connector);
            const granted = opsByConnector[connector.id] ?? [];
            return (
              <div key={connector.id} className="setup-connector">
                <div className="setup-connector-head">
                  <span className="setup-connector-name">{connector.name}</span>
                  <span
                    className={`wf-badge ${conn.connected ? "wf-badge-ok" : "wf-badge-draft"}`}
                  >
                    {conn.label}
                  </span>
                </div>
                <p className="dim setup-connector-desc">{connector.description}</p>
                {connector.ops.map((op) => (
                  <div key={op.name} className="setup-op">
                    <label className="setup-check">
                      <input
                        type="checkbox"
                        checked={granted.includes(op.name)}
                        onChange={() => toggleOp(connector.id, op.name)}
                      />
                      <span>
                        {opLabel(op.name)}{" "}
                        <span className={`setup-effect setup-effect-${op.effect}`}>
                          {op.effect}
                        </span>
                        <span className="dim"> — {op.description}</span>
                      </span>
                    </label>
                    {granted.includes(op.name) &&
                      (op.constraints ?? []).map((field) => (
                        <label key={field} className="setup-resource">
                          <span className="label">
                            Approved {field.replace(/_/g, " ")} values (comma-separated)
                          </span>
                          <input
                            value={
                              resourceText[`${connector.id}.${op.name}.${field}`] ?? ""
                            }
                            onChange={(e) =>
                              setResourceText((prev) => ({
                                ...prev,
                                [`${connector.id}.${op.name}.${field}`]: e.target.value,
                              }))
                            }
                            placeholder='exact values only, e.g. "primary"'
                          />
                        </label>
                      ))}
                  </div>
                ))}
              </div>
            );
          })}
        </section>

        <section className="setup-section">
          <div className="label">What the approval gate will show</div>
          <PermitDiagram permit={buildPermitDoc(permitBuild)} catalog={catalog ?? null} />
        </section>

        {errors.length > 0 && (
          <ul className="setup-errors">
            {errors.map((e) => (
              <li key={e} className="error-note">
                {e}
              </li>
            ))}
          </ul>
        )}
        {serverError && <p className="error-note">{serverError}</p>}

        <div className="setup-actions">
          <button className="btn" type="submit" disabled={submitting}>
            {submitting ? "Creating…" : "Create draft"}
          </button>
          <Link className="btn btn-secondary" to="/">
            Cancel
          </Link>
        </div>
        <p className="dim setup-foot">
          Creating makes version 1 as a draft. Nothing runs until you approve it on the
          next screen.
        </p>
      </form>
    </div>
  );
}
