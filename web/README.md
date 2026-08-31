# Nightshift web

The real frontend, served over the `/v1` API — realizing the four surfaces of
[the UX design](../docs/superpowers/specs/2026-08-28-nightshift-design.md).
The UX prototype lives in the repo root's `src/` and is research, not shared
code; this app stands alone under `web/`.

## What exists, what's blocked

Built against today's merged API:

- **Login** — magic-link request and the `/v1/me` bootstrap; `/build` is
  claimed as the first-login landing (`httpapi.firstLoginPath`) but is an
  honest placeholder.
- **Approve (surface 3)** — the blast-radius diagram, driven only by the
  permit v1 document. Permit v1 governs LLM egress + spend, so the read/write
  columns truthfully show "nothing yet"; they fill in when connectors land.
- **The quiet home (surface 4)** — workflow list with last run, cost,
  schedule-in-words, client-computed next fire, run history, and run events.

**Blocked on the server's build resource** (`POST /v1/builds`, connectors):
intake, the build conversation, connection cards, structural pickers, rubric
editing — see the frontend checklist in
[the build-conversation spec](../docs/superpowers/specs/2026-08-31-nightshift-build-conversation-design.md).

The `steps` document is parsed in the decision-9 user-facing form
(`{v: 1, steps: [{id, text}]}`), with the legacy compiled form rendered via
the same synthesis the server migration uses.

## Development

The Go server enforces a strict Origin policy and sets no CORS headers, so
the app must be same-origin with the API. In dev, the Vite server is the
public origin and proxies `/v1` and `/auth` through:

```bash
# terminal 1 — the API, with the Vite origin as its public base URL
export NIGHTSHIFT_PUBLIC_BASE_URL=http://localhost:5173
go run ./server/cmd/nightshift serve   # plus DATABASE_URL etc., see server/README.md

# terminal 2 — the app
cd web
npm install
npm run dev
```

With no Postmark configured, the magic link is printed to the server log —
open it in the browser to sign in. `NIGHTSHIFT_SERVER_URL` overrides the
proxy target (default `http://localhost:8080`).

## Scripts

- `npm test` — vitest (jsdom + testing-library)
- `npm run typecheck` — strict TypeScript
- `npm run format` / `format:check` — Prettier
- `npm run build` — production build
