import { vi } from "vitest";

type Handler = { status?: number; body?: unknown };

/**
 * Installs a fetch stub keyed by "METHOD /path". Unmatched requests fail the
 * test loudly rather than resolving to something plausible.
 */
export function mockApi(routes: Record<string, Handler>): ReturnType<typeof vi.fn> {
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url =
      typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const method = init?.method ?? "GET";
    const handler = routes[`${method} ${url}`];
    if (!handler) {
      throw new Error(`unexpected fetch: ${method} ${url}`);
    }
    const status = handler.status ?? 200;
    return new Response(status === 204 ? null : JSON.stringify(handler.body ?? {}), {
      status,
      headers: { "Content-Type": "application/json" },
    });
  });
  vi.stubGlobal("fetch", fn);
  return fn;
}
