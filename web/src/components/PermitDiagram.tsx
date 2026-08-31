import type { CatalogConnector } from "../api/types";
import { DENIED_BY_DEFAULT, parsePermit, providerLabel, spendLabel } from "../lib/permit";
import { reachColumns } from "../lib/reach";
import type { ReachItem } from "../lib/reach";
import "./PermitDiagram.css";

// The blast radius: judged in about three seconds, driven only by the
// permit document itself. The read/write columns show the permit's
// connector op grants, described by the catalog when the caller passes
// one; with no grants they say "nothing", honestly, because that is what
// the server enforces.

const NOTE_TEXT: Record<NonNullable<ReachItem["note"]>, string> = {
  unlisted: "not in today's catalog",
  unchecked: "couldn't check the catalog",
  unreadable: "review before approving",
};

function ReachEntry({ item }: { item: ReachItem }) {
  return (
    <div className="permit-item" title={item.description}>
      <span className="permit-item-name">
        {item.connector} · {item.op}
      </span>
      {item.note && <span className="permit-item-warn"> — {NOTE_TEXT[item.note]}</span>}
      {item.resources.map((r) => (
        <div key={r} className="permit-item-only">
          only {r}
        </div>
      ))}
    </div>
  );
}

export default function PermitDiagram({
  permit,
  catalog,
}: {
  permit: unknown;
  /** GET /v1/catalog connectors; null/undefined when unavailable. */
  catalog?: CatalogConnector[] | null;
}) {
  const view = parsePermit(permit);
  const { read, write } = reachColumns(view.grants, catalog);

  return (
    <figure className="permit" aria-label="What this workflow is allowed to reach">
      <div className="permit-boundary">
        <span className="permit-boundary-tag">Cannot go beyond this line</span>
        <div className="permit-row">
          <div className="permit-col">
            <div className="label permit-read-label">Can read</div>
            {read.length === 0 ? (
              <div className="permit-empty">
                Nothing yet — no systems are connected in this version.
              </div>
            ) : (
              read.map((item) => <ReachEntry key={item.key} item={item} />)
            )}
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
            {write.length === 0 ? (
              <div className="permit-empty">
                Nothing. It cannot change anything of yours.
              </div>
            ) : (
              write.map((item) => <ReachEntry key={item.key} item={item} />)
            )}
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
