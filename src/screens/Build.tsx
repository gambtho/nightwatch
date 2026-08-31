import Chat from "../components/Chat";
import PermitDiagram from "../components/PermitDiagram";
import { buildScript, permitAfter } from "../fixtures/conversation";
import { maxCostLabel } from "../lib/permit";
import "./screens.css";

interface Props {
  shown: number;
  onAdvance: () => void;
  slackConnected: boolean;
  onConnectSlack: () => void;
  onVerdict: () => void;
}

// `shown` lives in App so the conversation survives the round trip through
// the connections manager.
export default function Build({
  shown,
  onAdvance,
  slackConnected,
  onConnectSlack,
  onVerdict,
}: Props) {
  const permit = permitAfter(buildScript, shown);
  const current = buildScript[shown - 1];
  const highlightIds = current.grants.map((g) => g.id);
  const atEnd = shown >= buildScript.length;
  const needsSlack = Boolean(current.connectSlack) && !slackConnected;

  return (
    <div className="screen build">
      <div className="build-chat">
        <div className="label">Chat</div>
        <Chat turns={buildScript.slice(0, shown)} />
        {current.connectSlack && slackConnected && (
          <p className="verify-ok">
            ✓ Slack connected — every future job finds it already connected.
          </p>
        )}
        {needsSlack ? (
          <button className="btn" onClick={onConnectSlack}>
            Connect Slack — one paste
          </button>
        ) : atEnd ? (
          <button className="btn" onClick={onVerdict}>
            See my honest read
          </button>
        ) : (
          <button className="btn" onClick={onAdvance}>
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
