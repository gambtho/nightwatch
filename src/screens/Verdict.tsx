import type { Verdict } from "../lib/types";
import "./screens.css";

interface Props {
  verdict: Verdict;
  onReview: () => void;
}

export default function VerdictScreen({ verdict, onReview }: Props) {
  return (
    <div className="screen screen-narrow">
      <h2>Yes — mostly. Here's what I'd actually do.</h2>
      <p className="dim">
        And one part I'd get wrong, so I'm not going to pretend otherwise.
      </p>

      <div className="block block-can">
        <div className="label">I CAN DO THIS</div>
        {verdict.can.map((c) => (
          <div key={c}>✓ {c}</div>
        ))}
      </div>

      <div className="block block-cannot">
        <div className="label">I'D GET THIS WRONG</div>
        {verdict.cannot.map((c) => (
          <div key={c}>{c}</div>
        ))}
      </div>

      <div className="block block-access">
        <div className="label">I'D NEED ACCESS TO</div>
        {verdict.access.map((a) => (
          <div key={a.id}>
            {a.access === "read" ? "📖" : "✍️"} {a.label} — <em>{a.detail}</em>
          </div>
        ))}
        <p className="dim">You'll see exactly what it can touch before anything runs.</p>
      </div>

      <button className="btn" onClick={onReview}>
        Review what it can touch
      </button>
    </div>
  );
}
