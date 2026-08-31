import { useEffect, useState } from "react";
import { getCatalog, isAuthError } from "../api/client";
import type { CatalogConnector } from "../api/types";
import { useSession } from "../session";

// Best-effort catalog fetch for screens that render the blast-radius
// diagram: with it the read/write columns can describe granted ops; when
// it can't be reached the diagram still shows every grant, conservatively
// (see lib/reach.ts), so null is a degraded view — never a hidden grant.

export function useCatalog(): CatalogConnector[] | null {
  const { expire } = useSession();
  const [catalog, setCatalog] = useState<CatalogConnector[] | null>(null);

  useEffect(() => {
    let cancelled = false;
    getCatalog()
      .then(({ connectors }) => {
        if (!cancelled) setCatalog(connectors);
      })
      .catch((err: unknown) => {
        if (!cancelled && isAuthError(err)) expire();
        // Any other failure: leave null; the diagram degrades honestly.
      });
    return () => {
      cancelled = true;
    };
  }, [expire]);

  return catalog;
}
