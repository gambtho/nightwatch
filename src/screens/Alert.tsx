import "./screens.css";

export default function Alert() {
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
        <div>"Flags anything security-related separately"</div>
        <p className="dim">
          Failed 3 Mondays running. Your other 2 rules are still fine.
        </p>
      </div>

      <div className="block block-access">
        <div className="label">WHY I THINK IT'S HAPPENING</div>
        <div>
          Since Aug 4, almost every ticket has come through with an empty{" "}
          <strong>category</strong> field. I've been guessing which ones are security
          issues, and I've been guessing badly.
        </div>
      </div>

      <div className="block block-can">
        <div className="label">WHAT I DID ABOUT IT</div>
        <div>
          Paused it. It won't run again until you decide — I'd rather stop than keep
          sending you something you trust and shouldn't.
        </div>
      </div>

      <button className="btn">Show me the 3 runs</button>
      <button className="btn btn-secondary">Let's fix it</button>
      <button className="btn btn-secondary">It's fine, resume</button>
    </div>
  );
}
