import { DENIED_BY_DEFAULT, parsePermit, providerLabel, spendLabel } from "../lib/permit";
import "./PermitDiagram.css";

// The blast radius: judged in about three seconds, driven only by the
// permit document itself. Permit v1 grants LLM egress and spend — no
// connected systems yet — so the read/write columns state that honestly
// instead of inventing capability the server would not enforce.

export default function PermitDiagram({ permit }: { permit: unknown }) {
  const view = parsePermit(permit);

  return (
    <figure className="permit" aria-label="What this workflow is allowed to reach">
      <div className="permit-boundary">
        <span className="permit-boundary-tag">Cannot go beyond this line</span>
        <div className="permit-row">
          <div className="permit-col">
            <div className="label permit-read-label">Can read</div>
            <div className="permit-empty">
              Nothing yet — no systems are connected in this version.
            </div>
          </div>

          <div className="permit-arrow" aria-hidden="true">
            →
          </div>

          <div className="permit-agent">
            <svg
              className="permit-agent-moon"
              viewBox="0 0 24 24"
              width="26"
              height="26"
              aria-hidden="true"
            >
              <path
                d="M20 14.5A8.5 8.5 0 0 1 9.5 4 8.5 8.5 0 1 0 20 14.5Z"
                fill="var(--moon)"
              />
            </svg>
            <div className="permit-agent-name">the agent</div>
            {view.providers.length > 0 ? (
              <div className="permit-agent-providers">
                thinks with {view.providers.map(providerLabel).join(", ")}
              </div>
            ) : (
              <div className="permit-agent-providers permit-agent-none">
                may not call any model — it cannot run
              </div>
            )}
            <div className="permit-agent-cost">{spendLabel(view)}</div>
          </div>

          <div className="permit-arrow" aria-hidden="true">
            →
          </div>

          <div className="permit-col">
            <div className="label permit-write-label">Can write</div>
            <div className="permit-empty">
              Nothing. It cannot change anything of yours.
            </div>
          </div>
        </div>
      </div>

      <ul className="permit-denied">
        {DENIED_BY_DEFAULT.map((d) => (
          <li key={d} className="permit-denied-item">
            <span aria-hidden="true">✕</span> <s>{d}</s>
          </li>
        ))}
      </ul>

      {!view.recognized && (
        <figcaption className="permit-unrecognized">
          This version's permit couldn't be read, so it grants nothing at all.
        </figcaption>
      )}
    </figure>
  );
}
