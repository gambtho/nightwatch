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
// screen and the product.

type SessionState =
  { status: "loading" } | { status: "anonymous" } | { status: "signed-in"; me: Me };

interface SessionContextValue {
  session: SessionState;
  signOut: () => Promise<void>;
}

const SessionContext = createContext<SessionContextValue | null>(null);

export function SessionProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<SessionState>({ status: "loading" });

  useEffect(() => {
    let cancelled = false;
    getMe()
      .then((me) => {
        if (!cancelled) setSession({ status: "signed-in", me });
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        if (err instanceof ApiError && err.status === 401) {
          setSession({ status: "anonymous" });
        } else {
          // Network or server trouble: treat as anonymous so the user at
          // least sees the login screen rather than a spinner forever.
          setSession({ status: "anonymous" });
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const signOut = useCallback(async () => {
    await apiLogout();
    setSession({ status: "anonymous" });
  }, []);

  return (
    <SessionContext.Provider value={{ session, signOut }}>
      {children}
    </SessionContext.Provider>
  );
}

export function useSession(): SessionContextValue {
  const value = useContext(SessionContext);
  if (!value) throw new Error("useSession outside SessionProvider");
  return value;
}
