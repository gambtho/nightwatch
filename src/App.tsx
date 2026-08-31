import { useState } from "react";
import FirstRun from "./screens/FirstRun";
import Intake from "./screens/Intake";
import Build from "./screens/Build";
import Connections from "./screens/Connections";
import VerdictScreen from "./screens/Verdict";
import Approve from "./screens/Approve";
import Home from "./screens/Home";
import Alert from "./screens/Alert";
import { supportDigestVerdict } from "./fixtures/verdict";
import "./App.css";

type Screen =
  "setup" | "intake" | "build" | "connections" | "verdict" | "approve" | "home" | "alert";

const NAV: { id: Screen; label: string }[] = [
  { id: "setup", label: "First run" },
  { id: "intake", label: "Ask" },
  { id: "build", label: "Build" },
  { id: "connections", label: "Connections" },
  { id: "verdict", label: "Verdict" },
  { id: "approve", label: "Approve" },
  { id: "home", label: "Home" },
  { id: "alert", label: "Alert" },
];

export default function App() {
  const [screen, setScreen] = useState<Screen>("setup");
  const [buildShown, setBuildShown] = useState(1);
  const [slackConnected, setSlackConnected] = useState(false);
  const [fromBuild, setFromBuild] = useState(false);

  return (
    <>
      <nav className="demo-nav">
        <span className="demo-nav-title">🧝 Tomte demo</span>
        {NAV.map((n) => (
          <button
            key={n.id}
            className={`demo-nav-btn${screen === n.id ? " active" : ""}`}
            onClick={() => setScreen(n.id)}
          >
            {n.label}
          </button>
        ))}
      </nav>

      {screen === "setup" && <FirstRun onDone={() => setScreen("intake")} />}
      {screen === "intake" && <Intake onSubmit={() => setScreen("build")} />}
      {screen === "build" && (
        <Build
          shown={buildShown}
          onAdvance={() => setBuildShown(buildShown + 1)}
          slackConnected={slackConnected}
          onConnectSlack={() => {
            setFromBuild(true);
            setScreen("connections");
          }}
          onVerdict={() => setScreen("verdict")}
        />
      )}
      {screen === "connections" && (
        <Connections
          slackConnected={slackConnected}
          onSlackConnected={() => setSlackConnected(true)}
          onBackToBuild={
            fromBuild
              ? () => {
                  setFromBuild(false);
                  setScreen("build");
                }
              : undefined
          }
        />
      )}
      {screen === "verdict" && (
        <VerdictScreen
          verdict={supportDigestVerdict}
          onReview={() => setScreen("approve")}
        />
      )}
      {screen === "approve" && <Approve onApproved={() => setScreen("home")} />}
      {screen === "home" && (
        <Home onNew={() => setScreen("intake")} onAlert={() => setScreen("alert")} />
      )}
      {screen === "alert" && <Alert />}
    </>
  );
}
