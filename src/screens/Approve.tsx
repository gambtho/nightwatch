import PermitDiagram from "../components/PermitDiagram";
import { buildScript, permitAfter } from "../fixtures/conversation";
import "./screens.css";

export default function Approve({ onApproved }: { onApproved: () => void }) {
  const permit = permitAfter(buildScript, buildScript.length);

  return (
    <div className="screen screen-narrow">
      <h2>Weekly support digest</h2>
      <p className="dim">Runs Mondays at 9:00 AM · America/New_York</p>

      <PermitDiagram permit={permit} highlightIds={[]} maxCostLabel="max $2.00 / run" />

      <p className="dim approve-note">
        It stops after 2 bad runs and tells you. It never retries silently.
      </p>

      <button className="btn" onClick={onApproved}>
        Approve &amp; schedule
      </button>
      <button className="btn btn-secondary">Change what it can reach</button>
    </div>
  );
}
