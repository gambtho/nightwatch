# Nightshift Identity + Onboarding — Design

**Status:** Design proposed; not yet planned or built
**Date:** 2026-08-30
**Author:** gambtho
**Companions:**
[`2026-08-28-nightshift-design.md`](./2026-08-28-nightshift-design.md) (the target
user this flow must not lose),
[`2026-08-30-nightshift-platform-design.md`](./2026-08-30-nightshift-platform-design.md)
(tenancy posture), and the roadmap's "Identity and onboarding" prerequisite note in
[`../plans/2026-08-30-nightshift-platform-roadmap.md`](../plans/2026-08-30-nightshift-platform-roadmap.md).

## What this is

The production signup/login story that replaces the dev-only session mint
(`nightshift dev-session`). It covers four decisions the roadmap left open: the auth
method, the tenant-creation flow, session issuance, and what "one person builds and
approves" means for roles until multi-user governance lands.

Spec only. No implementation plan exists yet; this feeds one.

## Current state (read 2026-08-30)

- `nightshift dev-session` (`server/cmd/nightshift/main.go`) mints a tenant + KEK via
  `store.CreateTenant`, upserts an owner user, and prints a signed cookie. It is the
  only way to get a session.
- Sessions are stateless HMAC cookies (`server/internal/httpapi/session.go`):
  base64(JSON claims) + HMAC-SHA256 under `NIGHTSHIFT_SESSION_KEY`. Claims carry
  `uid`, `tid`, `role`, `exp`. No issuance timestamp, no revocation, no `Secure`
  flag, 24 h TTL hardcoded in the mint.
- `store.CreateTenant` already creates tenant + wrapped KEK in one transaction ("a
  tenant without a KEK cannot hold secrets, so the two are born together"). Signup
  reuses it unchanged.
- `app_user` is unique on `(tenant_id, email)` with `role` constrained to
  `CHECK (role IN ('owner'))`. There is no cross-tenant identity: nothing prevents
  the same email existing in two tenants, and nothing maps an email to a tenant.

## Decisions

| Decision                                                    | Why                                                                                                                                                                                                                                                         |
| ----------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Email magic link, built in-house, the only method at v1** | The target user lives in their inbox. No password to reuse or forget, no OAuth consent screen, no hosted-IdP dependency. The infrastructure is one token table plus transactional email — and transactional email is needed for Plan 4 alerting regardless. |
| **Signup and login are the same flow**                      | The non-technical user cannot be asked to know whether they "have an account". One form: enter email, click the link. First verified login creates the tenant; later logins find it.                                                                        |
| **Tenant minted at verification, not at request**           | Creating tenants when the form is submitted would mint a tenant (and a KEK) for every typo'd or malicious email. Nothing is written until the address proves it can receive mail.                                                                           |
| **One tenant per email (v1 constraint)**                    | "One person builds and approves" means single-user tenants. A global unique index on `lower(email)` makes login resolution trivial (email → the tenant). The multi-user governance spec lifts this with an account/membership split; see below.             |
| **DB-backed opaque sessions replace the stateless cookie**  | The magic-link flow needs a token table anyway, so a per-request DB read is not a new cost class. Opaque sessions make logout, "log out everywhere", and support-driven revocation real — table stakes for a product whose pitch is governance.             |
| **`NIGHTSHIFT_SESSION_KEY` is retired**                     | With opaque sessions there is nothing to sign. `dev-session` gains DB access it already had and inserts a session row instead of printing a signed cookie. One fewer key to manage and rotate.                                                              |
| **Single `owner` role stands; the enum stays closed**       | Every capability belongs to `owner`. Keeping `CHECK (role IN ('owner'))` means no code grows speculative role logic that multi-user governance would have to unwind.                                                                                        |

### Alternatives considered

- **Hosted IdP (Auth0 / Clerk / Stytch / WorkOS).** Buys login UI and deliverability,
  but adds a paid dependency and a second user store outside our "tenant in every
  claim" posture. All authorization is ours regardless of who authenticates, so the
  IdP saves only the smallest layer while owning the most sensitive one. Revisit if
  enterprise SSO (SAML/OIDC) ever becomes a sales requirement — that is what WorkOS
  is actually for.
- **OAuth social login (Google/Microsoft).** A good accelerator later — same
  verified-email outcome, one click. Deferred because it needs provider app
  registration and review, doesn't cover everyone, and adds a second code path
  before the first one exists. When added, it keys on the verified email and joins
  the same tenant-resolution logic; no schema change.
- **Passkeys.** Wrong as the sole first-run method for a non-technical user
  (device-bound credentials, confusing ceremony, hard recovery). Right as a
  post-login upgrade — "skip the email next time" — once sessions exist. Deferred,
  not rejected.
- **Stateless cookie + revocation epoch.** Keeping the HMAC cookie but checking a
  per-user epoch column on each request is still a DB read per request, at which
  point the opaque session is strictly simpler and reveals nothing if leaked.

## The flow

### Requesting a link

`POST /v1/auth/magic-link` with `{"email": "..."}`.

- Normalize (trim, lowercase). Respond **202 with an identical body whether or not
  the email is known** — no account enumeration.
- Generate a 256-bit random token. Store only its SHA-256 hash in `login_token`
  with `email`, `created_at`, `expires_at` (15 minutes), `consumed_at NULL`.
- Send one email: "Sign in to Nightshift" with a link to
  `GET /auth/verify?token=...`.
- Rate limit: per email address and per source IP (small fixed budgets, e.g. 3
  outstanding links per email, sliding-window IP cap). Over budget → same 202,
  no email. The limits are anti-abuse, not UX; exact numbers are an
  implementation-plan detail.

### Verifying

**Email scanners prefetch links.** Corporate mail security (Safe Links, Mimecast)
follows every URL in an email, so a GET that consumes the token would burn it before
the user ever clicks. Standard mitigation, adopted here:

1. `GET /auth/verify?token=...` renders an interstitial page with one button
   ("Continue to Nightshift"). It reads nothing but the token's existence; it
   consumes nothing.
2. The button submits `POST /v1/auth/verify` with the token. The server, in one
   transaction:
   - looks up the hash; rejects if missing, expired, or already consumed;
   - marks it consumed;
   - resolves `lower(email)` → user. **If none exists:** `store.CreateTenant`
     (tenant named from the email's local part — the user is never asked to name
     a workspace) then `UpsertUser` → the `owner`. Tenant + KEK + user + consumed
     token commit together;
   - inserts a session row and sets the cookie;
   - redirects: first login → the build conversation (the UX spec's entry point);
     returning login → wherever the link's `next` pointed, validated same-origin.

A consumed or expired token renders "this link has expired — request a new one"
with the email form. Never an error page a non-technical user has to interpret.

### Sessions

Table `session`: `id`, `token_hash` (SHA-256 of a 256-bit random value, unique),
`user_id`, `tenant_id`, `created_at`, `last_seen_at`, `expires_at`.

- Cookie: `__Host-ns_session` — `Secure`, `HttpOnly`, `SameSite=Lax`, `Path=/`.
  (The `__Host-` prefix requires Secure + no Domain attribute; local dev runs
  behind the same rules via localhost's secure-context carve-out.)
- Lifetime: **7-day idle timeout, 30-day absolute cap.** `last_seen_at` is touched
  at most once per hour to avoid a write per request. Expiry checks read
  `last_seen_at + 7d` and `created_at + 30d`, whichever is sooner.
- `RequireSession` becomes: read cookie → hash → single indexed lookup → populate
  the same `SessionClaims{UserID, TenantID, Role}` context value. **Handlers and
  `ClaimsFrom` are untouched**; only issuance and verification change.
- `POST /v1/auth/logout` deletes the row and clears the cookie.
  "Log out everywhere" (delete all rows for the user) ships with the settings
  surface, but the store method exists from day one — it is also the
  support-revocation lever.
- `GET /v1/me` returns the current user + tenant — the UI's bootstrap call.

**CSRF.** `SameSite=Lax` blocks cross-site POSTs from forms and scripts in modern
browsers; as defence in depth every mutating `/v1` endpoint also rejects requests
whose `Origin` header is present and not our own origin. No token dance — the API
is same-origin `fetch` only.

### `dev-session` after this

The command survives for local development but changes output: it inserts a session
row and prints the cookie value, rather than signing claims. It stops needing
`NIGHTSHIFT_SESSION_KEY` (which no longer exists) and keeps needing `DATABASE_URL`
and `NIGHTSHIFT_VAULT_KEY`, exactly as today.

## Schema changes

- `login_token` table as above, with an index on `token_hash` and a sweep job (or
  opportunistic delete) for expired rows.
- `session` table as above.
- `CREATE UNIQUE INDEX ON app_user (lower(email))` — the v1 one-tenant-per-email
  constraint. `UpsertUser`'s `(tenant_id, email)` conflict target still works;
  the new index only forbids the same email appearing under a second tenant.
- No change to `tenant`, `tenant_kek`, or `app_user` columns.

## What "one person builds and approves" means for roles

Until the multi-user governance spec:

- **`owner` is the only role and holds every capability**: build, edit, approve,
  connect credentials, fire runs, read run records, delete. The
  `CHECK (role IN ('owner'))` constraint stays exactly as narrow.
- **Approval stays a distinct recorded act.** `POST …/versions/{v}/approve` remains
  its own endpoint and its own audit fact (who, when) even though the approver is
  always the builder. The value today is the record and the deliberate pause the UX
  spec designs around; the value later is that the endpoint's shape doesn't change
  when approver ≠ builder.
- **No role parameter appears anywhere in the v1 API.** Invitations, admin
  consoles, and builder/approver separation are all governance-spec territory.

**The seam multi-user will cut.** Today identity and membership are one row
(`app_user`). Multi-user governance splits them: a global `account` (the email, the
login) and per-tenant memberships carrying roles. The v1 global-unique-email index
is exactly the constraint that split removes. Nothing in this design hides that
seam: sessions reference `user_id` + `tenant_id` explicitly, so a future "one
account, several memberships" model changes login resolution and nothing else.

## Failure handling

- **Email not delivered** — the form's confirmation screen says to check spam and
  offers resend (subject to the rate budget). Deliverability itself is an open
  question below.
- **Link expired / reused / scanner-burned** — the interstitial + POST design makes
  scanner burn structurally impossible; expired and reused tokens land on the
  friendly re-request page.
- **Tenant-creation transaction fails mid-signup** — everything including the
  token consumption rolls back; the link remains valid and the user retries by
  clicking it again.
- **Session row missing/expired** — plain 401 from the API; the UI redirects to the
  email form. Indistinguishable from logout, by design.

## Testing

- Store tests: token single-use under concurrent consumption (two simultaneous
  verifies, one wins); global email uniqueness across tenants; session
  idle/absolute expiry boundaries; log-out-everywhere.
- Handler tests: enumeration resistance (known vs unknown email produce
  byte-identical responses), rate-budget behaviour, interstitial consumes nothing
  on GET, cookie attributes, Origin rejection on mutating routes.
- One end-to-end test: fresh email → link → verify → tenant + KEK + owner exist →
  `/v1/me` answers → logout → 401.

## Open questions

- **Transactional email provider.** Postmark/SES/Resend-class choice, sending
  domain, SPF/DKIM/DMARC setup. Shared with Plan 4's alert delivery — decide once.
- **Abuse beyond rate limits.** Disposable-email domains, bot signups minting
  tenants (each one costs a KEK row, not much else). Probably ignorable pre-launch;
  revisit with billing.
- **Email change and account recovery.** Out of v1; changing the login email
  touches the global-unique index and deserves the account/membership split first.
- **Support access.** Whether operators can ever mint a session into a customer
  tenant (impersonation), and under what audit. Policy question, not schema.
- **Tenant deletion / data export.** Owed before public launch; interacts with
  KEK destruction as the crypto-erase mechanism.

## Explicitly out of scope

Multi-user tenants, invitations, roles beyond `owner`, OAuth login, passkeys,
enterprise SSO, billing identity, the settings UI, and all connector OAuth (that is
the connector-catalog spec's problem — connecting a tool is authorization to a
third party, not authentication of the user).
