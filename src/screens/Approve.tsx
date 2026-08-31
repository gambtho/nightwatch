import PermitDiagram from "../components/PermitDiagram";
import { buildScript, permitAfter } from "../fixtures/conversation";
import { dollars, MONTHLY_BUDGET_CENTS } from "../lib/budget";
import { AUTO_PAUSE_THRESHOLD } from "../lib/grading";
import { maxCostLabel } from "../lib/permit";
import "./screens.css";

export default function Approve({ onApproved }: { onApproved: () => void }) {
  const permit = permitAfter(buildScript, buildScript.length);

  return (
    <div className="screen screen-narrow">
      <h2>Weekly support digest</h2>
      <p className="dim">Runs Mondays at 9:00 AM · America/New_York</p>

      <PermitDiagram
        permit={permit}
        highlightIds={[]}
        maxCostLabel={maxCostLabel(permit)}
      />

      <p className="approve-guarantee">
        It can only act through Tomte's checkpoint, and every request is checked against
        this picture.
      </p>

      <div className="block block-access">
        <div className="label">WHAT THIS SPENDS</div>
        <div>Runs on your key · {maxCostLabel(permit)}</div>
        <div>Checking my work against your rules adds ~1–2¢ per run</div>
        <p className="dim">
          All of it counts against your {dollars(MONTHLY_BUDGET_CENTS)} monthly budget.
        </p>
      </div>

      <div className="block">
        <div className="label">IF YOUR COMPUTER IS ASLEEP</div>
        <div>
          Tomte works while your computer is on. Asleep at 9:00 AM Monday? The digest runs
          as soon as it wakes — once, not twelve times.
        </div>
        <p className="dim">
          Want true 9:00 AM every time? Keep this computer awake (plugged in, sleep set to
          never), or install Tomte on a machine that stays on. A hosted always-on option
          may come later.
        </p>
      </div>

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
