import { Link, Navigate, Route, Routes } from "react-router-dom";
import { useSession } from "./session";
import Home from "./screens/Home";
import WorkflowDetail from "./screens/WorkflowDetail";
import Approve from "./screens/Approve";
import Build from "./screens/Build";
import Connections from "./screens/Connections";
import Setup from "./screens/Setup";
import FirstRun from "./screens/FirstRun";
import Settings from "./screens/Settings";
import "./App.css";

function Shell({ children }: { children: React.ReactNode }) {
  const { session, signOut } = useSession();
  return (
    <div className="shell">
      <header className="shell-header">
        <Link to="/" className="wordmark">
          <svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true">
            <path
              d="M20 14.5A8.5 8.5 0 0 1 9.5 4 8.5 8.5 0 1 0 20 14.5Z"
              fill="var(--moon)"
            />
          </svg>
          Tomte
        </Link>
        {session.status === "signed-in" && (
          <div className="shell-user">
            <Link className="dim" to="/settings">
              Settings
            </Link>
            <span className="dim">{session.me.user.email}</span>
            <button className="btn-quiet" onClick={() => void signOut()}>
              Sign out
            </button>
          </div>
        )}
      </header>
      <main className="shell-main">{children}</main>
    </div>
  );
}

function RequireSession({ children }: { children: React.ReactElement }) {
  const { session, retry } = useSession();
  if (session.status === "loading") {
    return <div className="dim boot-note">One moment…</div>;
  }
  if (session.status === "unreachable") {
    return (
      <div className="boot-note">
        <p className="error-note">Couldn't reach Tomte. Your work is untouched.</p>
        <button className="btn btn-secondary" onClick={retry}>
          Try again
        </button>
      </div>
    );
  }
  if (session.status === "anonymous") {
    // There is no login screen: the session arrives from outside the SPA —
    // the shell (or `tomte dev-session`) mints it, and "open in browser"
    // lands through GET /local/handoff?token=&next=, which sets the cookie.
    return (
      <div className="boot-note">
        <p>This browser isn't signed in to Tomte.</p>
        <p className="dim">
          Open Tomte from the app and it signs the browser in for you. If you're
          developing, mint a session with <code>tomte dev-session</code>.
        </p>
        <button className="btn btn-secondary" onClick={retry}>
          Check again
        </button>
      </div>
    );
  }
  return children;
}

export default function App() {
  return (
    <Shell>
      <Routes>
        <Route
          path="/"
          element={
            <RequireSession>
              <Home />
            </RequireSession>
          }
        />
        <Route
          path="/build"
          element={
            <RequireSession>
              <Build />
            </RequireSession>
          }
        />
        <Route
          path="/connections"
          element={
            <RequireSession>
              <Connections />
            </RequireSession>
          }
        />
        <Route
          path="/setup"
          element={
            <RequireSession>
              <Setup />
            </RequireSession>
          }
        />
        {/* The shell mints the session before the SPA loads, so /welcome is
            the packaged app's first paint; the gate only catches a browser
            that arrived without the handoff. */}
        <Route
          path="/welcome"
          element={
            <RequireSession>
              <FirstRun />
            </RequireSession>
          }
        />
        <Route
          path="/settings"
          element={
            <RequireSession>
              <Settings />
            </RequireSession>
          }
        />
        <Route
          path="/workflows/:id"
          element={
            <RequireSession>
              <WorkflowDetail />
            </RequireSession>
          }
        />
        <Route
          path="/workflows/:id/approve"
          element={
            <RequireSession>
              <Approve />
            </RequireSession>
          }
        />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </Shell>
  );
}
