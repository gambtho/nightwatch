import { dollars } from "../lib/budget";
import { wakeLine } from "../lib/time";
import type { Workflow } from "../lib/types";

export default function WorkflowCard({ workflow }: { workflow: Workflow }) {
  const last = workflow.runs[workflow.runs.length - 1];
  const wake = wakeLine(last, workflow.schedule.timezone);
  return (
    <div className="wf-card">
      <div className="wf-card-top">
        <strong>{workflow.name}</strong>
        <span className="wf-ok">✓ ran</span>
      </div>
      {wake && <div className="dim wf-card-line wf-wake">{wake}</div>}
      <div className="dim wf-card-line">
        {last.summary} · {dollars(last.costCents)}
      </div>
      <div className="dim wf-card-line">Next: {workflow.schedule.label}</div>
    </div>
  );
}
