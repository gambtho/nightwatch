import { useState } from "react";
import Intake from "./screens/Intake";
import VerdictScreen from "./screens/Verdict";
import Build from "./screens/Build";
import Approve from "./screens/Approve";
import Home from "./screens/Home";
import Alert from "./screens/Alert";
import { supportDigestVerdict } from "./fixtures/verdict";
import "./App.css";

type Screen = "intake" | "verdict" | "build" | "approve" | "home" | "alert";

const NAV: { id: Screen; label: string }[] = [
  { id: "intake", label: "Intake" },
  { id: "verdict", label: "Verdict" },
  { id: "build", label: "Build" },
  { id: "approve", label: "Approve" },
  { id: "home", label: "Home" },
  { id: "alert", label: "Alert" },
];

export default function App() {
  const [screen, setScreen] = useState<Screen>("intake");

  return (
    <>
      <nav className="demo-nav">
        <span className="demo-nav-title">🌙 Nightshift prototype</span>
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

      {screen === "intake" && <Intake onSubmit={() => setScreen("verdict")} />}
      {screen === "verdict" && (
        <VerdictScreen
          verdict={supportDigestVerdict}
          onBuild={() => setScreen("build")}
        />
      )}
      {screen === "build" && <Build onApprove={() => setScreen("approve")} />}
      {screen === "approve" && <Approve onApproved={() => setScreen("home")} />}
      {screen === "home" && <Home onNew={() => setScreen("intake")} />}
      {screen === "alert" && <Alert />}
    </>
  );
}
