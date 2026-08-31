// The sleeping-machine copy at the schedule confirmation (pivot spec,
// "The sleeping machine" and "The always-on option"). The promise
// paragraph is load-bearing product copy — quoted from the spec, not
// paraphrased — and the three always-on options keep the spec's order:
// keep this machine awake, move to a machine that stays on, and the
// hosted tier named honestly as not existing yet.

export default function ScheduleExpectations() {
  return (
    <section className="schedule-expectations">
      <p className="dim">
        <strong>Tomte works while your computer is on.</strong> If your computer is asleep
        when this is scheduled, it runs as soon as the computer wakes — once, not twelve
        times. The home page will say so: “scheduled 3:00 AM · ran 7:42 AM, when your
        computer woke.”
      </p>
      <details className="schedule-always-on">
        <summary>Need it to run exactly on time?</summary>
        <ol className="dim">
          <li>
            <strong>Keep this computer awake</strong> — plugged in, with sleep set to
            Never in your computer's power settings (on a laptop, check what happens when
            the lid closes too). Tomte won't fight your power settings itself.
          </li>
          <li>
            <strong>Install Tomte on a machine that stays on</strong> — a desktop or a
            mini PC. One install per machine.
          </li>
          <li>A hosted, always-on Tomte may exist one day — it doesn't today.</li>
        </ol>
      </details>
    </section>
  );
}
