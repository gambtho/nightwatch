import type { Capability, Permit } from "../lib/types";
import { reads, writes } from "../lib/permit";
import "./PermitDiagram.css";

interface Props {
  permit: Permit;
  highlightIds: string[];
  maxCostLabel: string;
}

function CapabilityList({
  items,
  highlightIds,
}: {
  items: Capability[];
  highlightIds: string[];
}) {
  if (items.length === 0) {
    return <div className="cap-empty">Nothing yet</div>;
  }
  return (
    <>
      {items.map((c) => (
        <div
          key={c.id}
          data-testid={`cap-${c.id}`}
          className={`cap cap-${c.access}${highlightIds.includes(c.id) ? " just-added" : ""}`}
        >
          <div className="cap-label">{c.label}</div>
          {c.detail && <div className="cap-detail">{c.detail}</div>}
        </div>
      ))}
    </>
  );
}

export default function PermitDiagram({ permit, highlightIds, maxCostLabel }: Props) {
  const readItems = reads(permit);
  const writeItems = writes(permit);

  return (
    <div className="permit">
      <div className="permit-boundary">
        <span className="permit-boundary-tag">CANNOT GO BEYOND THIS LINE</span>
        <div className="permit-row">
          <div className="permit-col">
            <div className="label read-label">Can read</div>
            <CapabilityList items={readItems} highlightIds={highlightIds} />
          </div>

          <div className="permit-arrow">→</div>

          <div className="permit-agent">
            <div className="permit-agent-icon">🧝</div>
            <div className="permit-agent-name">the agent</div>
            <div className="permit-agent-cost">{maxCostLabel}</div>
          </div>

          <div className="permit-arrow">→</div>

          <div className="permit-col">
            <div className="label write-label">Can write</div>
            <CapabilityList items={writeItems} highlightIds={highlightIds} />
          </div>
        </div>
      </div>

      <div className="permit-denied">
        {permit.denied.map((d) => (
          <span key={d} className="denied-item">
            ✕ <span>{d}</span>
          </span>
        ))}
      </div>
    </div>
  );
}
