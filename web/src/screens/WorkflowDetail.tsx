import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { fireRun, getRunEvents, getWorkflow, isAuthError, listRuns } from "../api/client";
import type { Run, RunEvent, Version, Workflow } from "../api/types";
import PermitDiagram from "../components/PermitDiagram";
import { dollars, timeAgo } from "../lib/format";
import { parseSteps } from "../lib/steps";
import { describeSchedule, nextRunLabel } from "../lib/schedule";
import { summarizeVersions } from "../lib/versions";
import { useSession } from "../session";
import "./screens.css";

function RunRow({ run }: { run: Run }) {
  const [open, setOpen] = useState(false);
  const [events, setEvents] = useState<RunEvent[] | null>(null);
  const [eventsError, setEventsError] = useState(false);

  useEffect(() => {
    if (!open || events !== null) return;
    let cancelled = false;
    setEventsError(false);
    getRunEvents(run.id)
      .then(({ events }) => {
        if (!cancelled) setEvents(events);
      })
      .catch(() => {
        if (!cancelled) setEventsError(true);
      });
    return () => {
      cancelled = true;
    };
  }, [open, events, run.id]);

  const statusLabel: Record<Run["status"], string> = {
    pending: "waiting to start",
    running: "running",
    succeeded: "✓ ran",
    failed: "✕ failed",
  };

  return (
    <div className={`run run-${run.status}`}>
      <button
        className="run-head"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
      >
        <span className={`run-status run-status-${run.status}`}>
          {statusLabel[run.status]}
        </span>
        <span className="dim">{timeAgo(run.created_at)}</span>
        {run.cost_cents !== undefined && (
          <span className="dim">{dollars(run.cost_cents)}</span>
        )}
        <span className="dim run-fire">
          {run.fire_reason === "schedule" ? "on schedule" : "fired by hand"}
        </span>
      </button>
      {open && (
        <div className="run-body">
          {run.error_msg && <p className="error-note">{run.error_msg}</p>}
          {run.output && <pre className="run-output">{run.output}</pre>}
          {run.tokens_in !== undefined && (
            <p className="dim run-tokens">
              {run.tokens_in} tokens in · {run.tokens_out ?? 0} out
            </p>
          )}
          <div className="label">What happened</div>
          {eventsError && <p className="error-note">Couldn't load this run's events.</p>}
          {events === null && !eventsError && <p className="dim">Loading…</p>}
          {events !== null && events.length === 0 && (
            <p className="dim">No events recorded.</p>
          )}
          {events !== null &&
            events.map((e, i) => (
              <div key={i} className="run-event">
                <code>{e.type}</code>
                <span className="dim"> · {timeAgo(e.created_at)}</span>
              </div>
            ))}
        </div>
      )}
    </div>
  );
}

// Keyed by workflow id so navigating between workflows remounts with fresh
// state — no stale data from the previous workflow can win a fetch race.
export default function WorkflowDetail() {
  const { id } = useParams<{ id: string }>();
  if (!id) return null;
  return <WorkflowDetailInner key={id} id={id} />;
}

function WorkflowDetailInner({ id }: { id: string }) {
  const { expire } = useSession();
  const [workflow, setWorkflow] = useState<Workflow | null>(null);
  const [versions, setVersions] = useState<Version[]>([]);
  const [runs, setRuns] = useState<Run[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [refresh, setRefresh] = useState(0);
  const [firing, setFiring] = useState(false);
  const [fireError, setFireError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    Promise.all([getWorkflow(id), listRuns(id)])
      .then(([{ workflow, versions }, { runs }]) => {
        if (cancelled) return;
        setWorkflow(workflow);
        setVersions(versions);
        setRuns([...runs].sort((a, b) => b.created_at.localeCompare(a.created_at)));
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        if (isAuthError(err)) {
          expire();
          return;
        }
        setError(err instanceof Error ? err.message : "couldn't load");
      });
    return () => {
      cancelled = true;
    };
  }, [id, refresh, expire]);

  async function fire() {
    setFiring(true);
    setFireError(null);
    try {
      await fireRun(id);
      setRefresh((n) => n + 1);
    } catch (err) {
      if (isAuthError(err)) {
        expire();
        return;
      }
      setFireError(err instanceof Error ? err.message : "couldn't start a run");
    } finally {
      setFiring(false);
    }
  }

  if (error) {
    return (
      <div className="screen">
        <p className="error-note">Couldn't load this workflow ({error}).</p>
        <Link to="/">Back home</Link>
      </div>
    );
  }
  if (!workflow) {
    return <div className="screen dim">Loading…</div>;
  }

  const { approved, latestDraft } = summarizeVersions(versions);
  const shown = approved ?? latestDraft;
  const stepsView = shown ? parseSteps(shown.steps) : null;
  const schedule = approved?.schedule;

  return (
    <div className="screen">
      <div className="wf-detail-head">
        <h1>{workflow.name}</h1>
        {approved && (
          <button
            className="btn btn-secondary"
            onClick={() => void fire()}
            disabled={firing}
          >
            {firing ? "Starting…" : "Run now"}
          </button>
        )}
      </div>
      {fireError && <p className="error-note">{fireError}</p>}

      {latestDraft && (
        <div className="draft-banner">
          Version {latestDraft.number} is waiting for you.{" "}
          <Link to={`/workflows/${workflow.id}/approve`}>
            Review what it's allowed to reach →
          </Link>
        </div>
      )}

      {schedule && (
        <p className="dim">
          {describeSchedule(schedule.cron, schedule.tz)}
          {(() => {
            const next = nextRunLabel(schedule.cron, schedule.tz);
            return next ? <> · next: {next}</> : null;
          })()}
        </p>
      )}

      {stepsView && stepsView.steps.length > 0 && (
        <section className="wf-section">
          <div className="label">What it does{shown && !approved ? " (draft)" : ""}</div>
          <ol className="wf-steps">
            {stepsView.steps.map((s) => (
              <li key={s.id}>{s.text}</li>
            ))}
          </ol>
        </section>
      )}
      {stepsView && !stepsView.recognized && (
        <p className="error-note">This version's steps couldn't be read.</p>
      )}

      {approved && (
        <section className="wf-section">
          <div className="label">What it can reach (as approved)</div>
          <PermitDiagram permit={approved.permit} />
        </section>
      )}

      <section className="wf-section">
        <div className="label">Runs</div>
        {runs.length === 0 && <p className="dim">Hasn't run yet.</p>}
        {runs.map((run) => (
          <RunRow key={run.id} run={run} />
        ))}
      </section>
    </div>
  );
}
