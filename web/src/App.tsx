import { Link, Navigate, Route, Routes, useLocation } from "react-router-dom";
import { useSession } from "./session";
import Login from "./screens/Login";
import Home from "./screens/Home";
import WorkflowDetail from "./screens/WorkflowDetail";
import Approve from "./screens/Approve";
import Build from "./screens/Build";
import Connections from "./screens/Connections";
import Setup from "./screens/Setup";
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
  const location = useLocation();
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
    const next = location.pathname + location.search;
    return (
      <Navigate
        to={next === "/" ? "/login" : `/login?next=${encodeURIComponent(next)}`}
        replace
      />
    );
  }
  return children;
}

export default function App() {
  return (
    <Shell>
      <Routes>
        <Route path="/login" element={<Login />} />
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
