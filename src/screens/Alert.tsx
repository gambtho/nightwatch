import { consecutiveFailures, failingRules, shouldAutoPause } from "../lib/grading";
import { supportDigestDegraded } from "../fixtures/workflows";
import "./screens.css";

export default function Alert() {
  const workflow = supportDigestDegraded;
  const failing = failingRules(workflow);
  const rule = failing[0];
  const streak = consecutiveFailures(workflow, rule.id);
  const otherRulesCount = workflow.rubric.length - failing.length;
  const paused = shouldAutoPause(workflow);

  return (
    <div className="screen screen-narrow">
      <p className="dim alert-channel">
        Email + push — they didn't open the app. It found them.
      </p>

      <h2>⚠️ Your Monday digest has been getting worse, and I think I know why.</h2>
      <p className="dim">
        It still ran. It just stopped doing one of the things you asked for.
      </p>

      <div className="block block-cannot">
        <div className="label">THE RULE IT'S MISSING</div>
        <div>"{rule.text}"</div>
        <p className="dim">
          Failed {streak} Mondays running. Your other {otherRulesCount} rules are still
          fine.
        </p>
      </div>

      <div className="block block-access">
        <div className="label">WHY I THINK IT'S HAPPENING</div>
        <div>
          Since Aug 4, almost every failure has come through with empty{" "}
          <strong>job annotations</strong>. I've been guessing which ones are real
          product bugs, and I've been guessing badly.
        </div>
      </div>

      {paused && (
        <div className="block block-can">
          <div className="label">WHAT I DID ABOUT IT</div>
          <div>
            Paused it. It won't run again until you decide — I'd rather stop than keep
            sending you something you trust and shouldn't.
          </div>
        </div>
      )}

      <button className="btn">Show me the {streak} runs</button>
      <button className="btn btn-secondary">Let's fix it</button>
      <button className="btn btn-secondary">It's fine, resume</button>
    </div>
  );
}
