import WorkflowCard from "../components/WorkflowCard";
import { allWorkflows } from "../fixtures/workflows";
import { budgetPercent, dollars, MONTHLY_BUDGET_CENTS, spentCents } from "../lib/budget";
import "./screens.css";

export default function Home({
  onNew,
  onAlert,
}: {
  onNew: () => void;
  onAlert: () => void;
}) {
  const spent = spentCents(allWorkflows);
  const percent = budgetPercent(spent, MONTHLY_BUDGET_CENTS);

  return (
    <div className="screen screen-narrow">
      <h2>🧝 Everything's running</h2>

      <div className="meter-card">
        <div className="label">THIS MONTH'S BUDGET</div>
        <div className="meter">
          <div className="meter-fill" style={{ width: `${percent}%` }} />
        </div>
        <div className="meter-line">
          {dollars(spent)} of {dollars(MONTHLY_BUDGET_CENTS)} · resets on the 1st
        </div>
        <p className="dim meter-foot">
          Spent from your key, through Tomte — plus the one setup test call, well under a
          cent. If it ever runs out, jobs wait and you'll hear about it: "your budget is
          used up until the 1st — raise it in settings or wait."
        </p>
      </div>

      {allWorkflows.map((w) => (
        <WorkflowCard key={w.id} workflow={w} />
      ))}

      <div className="home-foot dim">
        <div>You don't need to check this page.</div>
        <div>If something goes wrong, we'll come find you.</div>
      </div>

      <button className="btn" onClick={onNew}>
        + Something else you want taken care of
      </button>
      <button className="btn btn-secondary" onClick={onAlert}>
        What it looks like when something goes wrong
      </button>
    </div>
  );
}
