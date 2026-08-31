import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { approveVersion, getWorkflow } from "../api/client";
import type { Version, Workflow } from "../api/types";
import PermitDiagram from "../components/PermitDiagram";
import { parseSteps } from "../lib/steps";
import { summarizeVersions } from "../lib/versions";
import { describeSchedule } from "../lib/schedule";
import "./screens.css";

// Surface 3 — the only gate. Everything the user needs to judge must be
// legible here, because once approved the workflow runs unattended: the
// steps in their own words, the schedule in words, and the blast radius
// drawn from the permit document itself.

export default function Approve() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [workflow, setWorkflow] = useState<Workflow | null>(null);
  const [draft, setDraft] = useState<Version | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [approving, setApproving] = useState(false);

  useEffect(() => {
    if (!id) return;
    let cancelled = false;
    getWorkflow(id)
      .then(({ workflow, versions }) => {
        if (cancelled) return;
        setWorkflow(workflow);
        setDraft(summarizeVersions(versions).latestDraft ?? null);
        setLoaded(true);
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "couldn't load");
        }
      });
    return () => {
      cancelled = true;
    };
  }, [id]);

  async function approve() {
    if (!id || !draft) return;
    setApproving(true);
    setError(null);
    try {
      await approveVersion(id, draft.number);
      navigate(`/workflows/${id}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "approval failed");
      setApproving(false);
    }
  }

  if (error && !loaded) {
    return (
      <div className="screen">
        <p className="error-note">Couldn't load this workflow ({error}).</p>
      </div>
    );
  }
  if (!loaded || !workflow) {
    return <div className="screen dim">Loading…</div>;
  }
  if (!draft) {
    return (
      <div className="screen">
        <h1>{workflow.name}</h1>
        <p className="dim">
          Nothing here is waiting for approval.{" "}
          <Link to={`/workflows/${workflow.id}`}>Back to the workflow.</Link>
        </p>
      </div>
    );
  }

  const steps = parseSteps(draft.steps);

  return (
    <div className="screen">
      <h1>{workflow.name}</h1>
      {draft.schedule && (
        <p className="dim approve-schedule">
          Runs {describeSchedule(draft.schedule.cron, draft.schedule.tz)}
        </p>
      )}

      {steps.length > 0 && (
        <section className="approve-steps">
          <div className="label">What it will do</div>
          <ol>
            {steps.map((s) => (
              <li key={s.id}>{s.text}</li>
            ))}
          </ol>
        </section>
      )}

      <section className="approve-diagram">
        <div className="label">What it can reach</div>
        <PermitDiagram permit={draft.permit} />
      </section>

      <p className="dim approve-note">
        Approving locks exactly this. It can never grow its own reach — changing anything
        means coming back through this screen.
      </p>

      {error && <p className="error-note">{error}</p>}

      <div className="approve-actions">
        <button className="btn" onClick={() => void approve()} disabled={approving}>
          {approving ? "Approving…" : "Approve"}
        </button>
        <Link className="btn btn-secondary" to={`/workflows/${workflow.id}`}>
          Not yet
        </Link>
      </div>
    </div>
  );
}
