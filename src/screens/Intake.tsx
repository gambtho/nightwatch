import { useState } from "react";
import "./screens.css";

const STARTERS = [
  "I keep forgetting to follow up on…",
  "Someone should be watching…",
  "Every month I have to put together…",
];

export default function Intake({ onSubmit }: { onSubmit: (text: string) => void }) {
  const [text, setText] = useState("");

  return (
    <div className="screen screen-narrow">
      <div className="intake-head">
        <div className="intake-moon">🧝</div>
        <h1>What do you want taken care of?</h1>
        <p className="dim">Describe it how you'd describe it to a coworker.</p>
      </div>

      <textarea
        className="intake-box"
        value={text}
        onChange={(e) => setText(e.target.value)}
        rows={4}
      />

      <button
        className="btn"
        onClick={() => {
          if (text.trim()) onSubmit(text);
        }}
      >
        See what you'd do
      </button>

      <div className="label starters-label">Or start from one of these</div>
      <div className="starters">
        {STARTERS.map((s) => (
          <button key={s} className="starter" onClick={() => setText(s)}>
            {s}
          </button>
        ))}
      </div>

      <p className="dim intake-foot">Nothing is connected yet. Nothing runs yet.</p>
    </div>
  );
}
