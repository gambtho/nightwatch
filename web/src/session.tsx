import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";
import { ApiError, getMe, logout as apiLogout } from "./api/client";
import type { Me } from "./api/types";

// GET /v1/me is the bootstrap call: it resolves the session cookie to the
// user and tenant, or 401s, which is how the app decides between the
// signed-out notice and the product. The cookie arrives from outside the
// SPA — the shell or `tomte dev-session` mints it, delivered via
// GET /local/handoff. Only a 401 means "not signed in" — any other failure
// is reported as unreachable, with a retry, rather than silently showing a
// signed-in user the signed-out notice.

type SessionState =
  | { status: "loading" }
  | { status: "anonymous" }
  | { status: "unreachable" }
  | { status: "signed-in"; me: Me };

interface SessionContextValue {
  session: SessionState;
  retry: () => void;
  signOut: () => Promise<void>;
  /** For data screens that get a 401 mid-session: drops to signed-out. */
  expire: () => void;
}

const SessionContext = createContext<SessionContextValue | null>(null);

export function SessionProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<SessionState>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setSession({ status: "loading" });
    getMe()
      .then((me) => {
        if (!cancelled) setSession({ status: "signed-in", me });
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        if (err instanceof ApiError && err.status === 401) {
          setSession({ status: "anonymous" });
        } else {
          setSession({ status: "unreachable" });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [attempt]);

  const retry = useCallback(() => setAttempt((n) => n + 1), []);
  const expire = useCallback(() => setSession({ status: "anonymous" }), []);

  const signOut = useCallback(async () => {
    try {
      await apiLogout();
      setSession({ status: "anonymous" });
    } catch {
      // The logout may not have reached the server, so the cookie could
      // still be live — never show "signed out" over a valid session.
      // Re-bootstrap and let /v1/me report the truth.
      setAttempt((n) => n + 1);
    }
  }, []);

  return (
    <SessionContext.Provider value={{ session, retry, signOut, expire }}>
      {children}
    </SessionContext.Provider>
  );
}

export function useSession(): SessionContextValue {
  const value = useContext(SessionContext);
  if (!value) throw new Error("useSession outside SessionProvider");
  return value;
}
