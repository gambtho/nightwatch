import { useState } from "react";
import Chat from "../components/Chat";
import PermitDiagram from "../components/PermitDiagram";
import { buildScript, permitAfter } from "../fixtures/conversation";
import { maxCostLabel } from "../lib/permit";
import "./screens.css";

export default function Build({ onApprove }: { onApprove: () => void }) {
  const [shown, setShown] = useState(1);
  const permit = permitAfter(buildScript, shown);
  const highlightIds = buildScript[shown - 1].grants.map((g) => g.id);
  const atEnd = shown >= buildScript.length;

  return (
    <div className="screen build">
      <div className="build-chat">
        <div className="label">Chat</div>
        <Chat turns={buildScript.slice(0, shown)} />
        {atEnd ? (
          <button className="btn" onClick={onApprove}>
            Review what it can touch
          </button>
        ) : (
          <button className="btn" onClick={() => setShown(shown + 1)}>
            Continue
          </button>
        )}
      </div>

      <div className="build-permit">
        <div className="label">Its reach — updating live</div>
        <PermitDiagram
          permit={permit}
          highlightIds={highlightIds}
          maxCostLabel={maxCostLabel(permit)}
        />
      </div>
    </div>
  );
}
