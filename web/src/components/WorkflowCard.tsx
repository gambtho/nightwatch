import { Link } from "react-router-dom";
import type { WorkflowSummary } from "../screens/Home";
import { dollars, timeAgo } from "../lib/format";
import { describeSchedule, nextRunLabel } from "../lib/schedule";

function lastRunLine(summary: WorkflowSummary): string {
  const last = summary.last;
  if (!last) return "Hasn't run yet";
  switch (last.status) {
    case "succeeded": {
      const cost = last.cost_cents !== undefined ? ` · ${dollars(last.cost_cents)}` : "";
      return `Ran ${timeAgo(last.created_at)}${cost}`;
    }
    case "failed":
      return `Last run failed ${timeAgo(last.created_at)}`;
    default:
      return "Running now";
  }
}

function statusBadge(summary: WorkflowSummary) {
  const { last } = summary;
  // A pending draft outranks the approved state — it's the one thing on
  // this page that actually needs the user.
  if (summary.versions.latestDraft) {
    return <span className="wf-badge wf-badge-draft">needs your approval</span>;
  }
  if (last?.status === "failed") {
    return <span className="wf-badge wf-badge-failed">✕ failed</span>;
  }
  if (last?.status === "succeeded") {
    return <span className="wf-badge wf-badge-ok">✓ ran</span>;
  }
  return null;
}

export default function WorkflowCard({ summary }: { summary: WorkflowSummary }) {
  const { workflow, versions } = summary;
  const schedule = versions.approved?.schedule;
  const next = schedule ? nextRunLabel(schedule.cron, schedule.tz) : null;

  return (
    <div className="wf-card">
      <div className="wf-card-top">
        <Link to={`/workflows/${workflow.id}`} className="wf-card-name">
          {workflow.name}
        </Link>
        {statusBadge(summary)}
      </div>

      {versions.approved ? (
        <>
          <div className="dim wf-card-line">{lastRunLine(summary)}</div>
          <div className="dim wf-card-line">
            {schedule ? (
              <>
                {describeSchedule(schedule.cron, schedule.tz)}
                {next && <> · next: {next}</>}
              </>
            ) : (
              "Runs only when you fire it"
            )}
          </div>
          <div className="dim wf-card-line wf-card-rules">
            Rule checks arrive with grading — not yet scored
          </div>
          {versions.latestDraft && (
            <div className="wf-card-line">
              <Link to={`/workflows/${workflow.id}/approve`}>
                Review what it's allowed to reach →
              </Link>
            </div>
          )}
        </>
      ) : versions.latestDraft ? (
        <div className="wf-card-line">
          <Link to={`/workflows/${workflow.id}/approve`}>
            Review what it's allowed to reach →
          </Link>
        </div>
      ) : (
        <div className="dim wf-card-line">No versions yet</div>
      )}
    </div>
  );
}
