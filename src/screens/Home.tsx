import WorkflowCard from "../components/WorkflowCard";
import { allWorkflows } from "../fixtures/workflows";
import "./screens.css";

export default function Home({ onNew }: { onNew: () => void }) {
  return (
    <div className="screen screen-narrow">
      <h2>🌙 Everything's running</h2>

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
    </div>
  );
}
