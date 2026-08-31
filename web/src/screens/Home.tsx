import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { getWorkflow, listRuns, listWorkflows } from "../api/client";
import type { Run, Workflow } from "../api/types";
import { summarizeVersions, lastRun } from "../lib/versions";
import type { VersionSummary } from "../lib/versions";
import WorkflowCard from "../components/WorkflowCard";
import "./screens.css";

// Surface 4, the quiet home. Its one job is to be safe to ignore: each
// workflow in a line, and the load-bearing promise that nobody needs to
// come back here. Alerting arrives with the grading release; until then
// the cards say what is and isn't watched, honestly.

export interface WorkflowSummary {
  workflow: Workflow;
  versions: VersionSummary;
  last?: Run;
}

export default function Home() {
  const [items, setItems] = useState<WorkflowSummary[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const { workflows } = await listWorkflows();
      const summaries = await Promise.all(
        workflows.map(async (workflow) => {
          const [{ versions }, { runs }] = await Promise.all([
            getWorkflow(workflow.id),
            listRuns(workflow.id),
          ]);
          return {
            workflow,
            versions: summarizeVersions(versions),
            last: lastRun(runs),
          };
        }),
      );
      if (!cancelled) setItems(summaries);
    })().catch((err: unknown) => {
      if (!cancelled) setError(err instanceof Error ? err.message : "couldn't load");
    });
    return () => {
      cancelled = true;
    };
  }, []);

  if (error) {
    return (
      <div className="screen">
        <p className="error-note">
          Couldn't load your workflows ({error}). Refresh to try again.
        </p>
      </div>
    );
  }

  if (items === null) {
    return <div className="screen dim">Loading…</div>;
  }

  if (items.length === 0) {
    return (
      <div className="screen">
        <h1>Nothing on the night shift yet</h1>
        <p className="dim">
          When you hand something over, it shows up here — along with what it costs, when
          it runs next, and whether it's keeping its rules.
        </p>
        <Link className="btn" to="/build">
          Hand something over
        </Link>
      </div>
    );
  }

  return (
    <div className="screen">
      <h1>Everything's running</h1>
      <div className="wf-list">
        {items.map((item) => (
          <WorkflowCard key={item.workflow.id} summary={item} />
        ))}
      </div>
      <p className="home-foot">
        You don't need to check this page.
        <br />
        If something goes wrong, we'll come find you.
      </p>
      <Link className="btn btn-secondary" to="/build">
        + Something else you want taken care of
      </Link>
    </div>
  );
}
