import PermitDiagram from "../components/PermitDiagram";
import { buildScript, permitAfter } from "../fixtures/conversation";
import { AUTO_PAUSE_THRESHOLD } from "../lib/grading";
import { maxCostLabel } from "../lib/permit";
import "./screens.css";

export default function Approve({ onApproved }: { onApproved: () => void }) {
  const permit = permitAfter(buildScript, buildScript.length);

  return (
    <div className="screen screen-narrow">
      <h2>Weekly flake digest</h2>
      <p className="dim">Runs Mondays at 9:00 AM · America/New_York</p>

      <PermitDiagram
        permit={permit}
        highlightIds={[]}
        maxCostLabel={maxCostLabel(permit)}
      />

      <p className="dim approve-note">
        It stops after {AUTO_PAUSE_THRESHOLD} bad runs and tells you. It never retries
        silently.
      </p>

      <button className="btn" onClick={onApproved}>
        Approve &amp; schedule
      </button>
      <button className="btn btn-secondary">Change what it can reach</button>
    </div>
  );
}
