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
// user and tenant, or 401s, which is how the app decides between the login
// screen and the product. Only a 401 means "not signed in" — any other
// failure is reported as unreachable, with a retry, rather than silently
// showing a signed-in user the login screen.

type SessionState =
  | { status: "loading" }
  | { status: "anonymous" }
  | { status: "unreachable" }
  | { status: "signed-in"; me: Me };

interface SessionContextValue {
  session: SessionState;
  retry: () => void;
  signOut: () => Promise<void>;
  /** For data screens that get a 401 mid-session: routes back to login. */
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
    } finally {
      // Even if the request failed, locally treating the user as signed
      // out is the safe direction — the next bootstrap re-checks reality.
      setSession({ status: "anonymous" });
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
