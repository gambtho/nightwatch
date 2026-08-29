import type { Workflow } from "../lib/types";

function dollars(cents: number): string {
  return `$${(cents / 100).toFixed(2)}`;
}

export default function WorkflowCard({ workflow }: { workflow: Workflow }) {
  const last = workflow.runs[workflow.runs.length - 1];
  return (
    <div className="wf-card">
      <div className="wf-card-top">
        <strong>{workflow.name}</strong>
        <span className="wf-ok">✓ ran</span>
      </div>
      <div className="dim wf-card-line">
        {last.summary} · {dollars(last.costCents)}
      </div>
      <div className="dim wf-card-line">Next: {workflow.schedule.label}</div>
    </div>
  );
}
