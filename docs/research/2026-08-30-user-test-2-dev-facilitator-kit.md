# Nightshift user test 2 (developer variant) — facilitator kit

**Date:** 2026-08-30 · **Run after** user test 1 sessions ·
**Prototype:** same build, unchanged · **Session length:** ~45 min

This is a **companion** to
[`2026-08-30-user-test-1-facilitator-kit.md`](./2026-08-30-user-test-1-facilitator-kit.md).
All mechanics carry over unchanged unless overridden here: the six-act sequence, the
five inert-button lines (Phase 3), the dry-run and remote-vs-local checklists, and the
notes template (plus the dev addendum at the bottom of this file).

## Why test developers at all

The design spec lists developers as explicit **non-users** — "they'd rather write the
YAML; they're served by the substrate directly." That is an assumption, not a finding.
A developer will pass the comprehension questions trivially, so this session does not
re-run them. It answers four different questions:

1. **Does the permit survive technical scrutiny?** A non-technical user believes the
   dashed line or doesn't. A developer will ask _where it's enforced_ — and whether
   they believe it is the strongest credibility signal we can collect.
2. **Where is the build-vs-buy line?** They could cron a script with an API key
   tonight. What, specifically, would make them not?
3. **What primitives and escape hatches do they demand** before trusting or adopting
   it — logs, dry runs, versioning, an API, export-to-code?
4. **Is "developers are non-users" actually true?** Do they have neglected recurring
   judgment chores of their own, and would they hand one over?

---

## Screening (replaces test 1's screener)

- [ ] **Writes code professionally** — the inverse of test 1's first criterion.
- [ ] **Owns or suffers a recurring judgment chore**: on-call handoff summaries, flaky
      test triage, dependency-update review, stale-PR nudging, support-ticket rotation.
      (Screener: "Is there something you keep meaning to automate but never do,
      because it needs judgment, not just a script?")
- [ ] **Mix AI exposure across participants** — recruit at least one heavy agent user
      and one skeptic; note which each is. Their attacks will differ, and both matter.
- [ ] Consent/recording, dry-run, and setup: identical to test 1, Phase 0.

## Intro framing (replaces test 1's Phase 1 script)

> "You're going to see an early design for a product that runs agents unattended on a
> schedule. It's aimed at non-technical users, but I want your eyes on it precisely
> because you're not that. Think out loud, and don't be polite — when you see a claim
> you don't believe, say so and tell me what you'd check. Some buttons aren't wired;
> tell me what you expected and I'll tell you what they'd do."

**Still do NOT pre-explain the permit diagram.** Question 1 needs their unprompted
read of it before their attack on it. Everything else may be discussed freely — the
"don't say AI/agent/approve" restrictions from test 1 do not apply here.

## Act-by-act deltas (same six acts as test 1)

- **Intake:** no steering ladder. Ask for a real chore from their own work life —
  ideally the one from the screener. If it lands far from the scripted support-digest
  scenario, use test 1's mismatch line and move on; question 3 of test 1 isn't being
  tested here, so nothing is lost.
- **Verdict:** watch whether "I'd get this wrong" reads as honesty or as marketing.
  Probe: _"Does an admitted limitation make you trust the yeses more, or does it read
  as a script?"_
- **Build:** watch whether the live permit reads as real state or as theater. Probe:
  _"What do you think is actually accumulating as this conversation runs?"_
- **Approve — the core of this session.** After their unprompted read, run the
  adversarial sequence in order, and get verbatim answers:
  1. _"Where do you think 'cannot go beyond this line' is enforced?"_ (The real
     answer: an egress proxy outside the sandbox; credentials never enter it. Do not
     reveal until they've committed to a guess.)
  2. Then reveal, and: _"Does that change whether you believe the line?"_
  3. _"What's missing from the struck-out list?"_
  4. _"What would you demand to see before approving this against your production
     Slack?"_
- **Home + Alert:** probe the audit surface: _"What would you expect behind 'Show me
  the 3 runs'?"_ and _"Would auto-pause after 3 failed rubric checks be right for your
  chore, or infuriating?"_

## Debrief (replaces test 1's Phase 5; ~15 min — this is the payload)

1. **Build-vs-buy:** "You could cron this yourself with a script and an API key.
   Walk me through why you would or wouldn't, for the chore you named." Push past the
   first answer — "what would the script miss?" and "what would this miss?"
2. **Primitive elicitation:** which of these would they demand before adopting — run
   logs · dry run before approval · permit/steps under version control · a real API ·
   export the workflow as code · webhook/event triggers · anything else (verbatim)?
   Mark each: demanded / nice / don't care.
3. **Connector + API demand:** test 1's connector walk, plus: "Which of these would
   you expect to reach through an API you write against, rather than a built-in
   connector?"
4. **The non-user assumption:** "Straight answer: would you personally hand a chore to
   this, or is it only for people who can't script? Which chore, or why not?"

## Dev addendum — append to the test 1 notes template

- Participant: heavy agent user / skeptic · Their chore (verbatim): ""
- Enforcement guess before reveal (verbatim): "" · Believes the line after reveal? y/n
- Missing from denied list: \_\_ · Would demand before approving: \_\_
- Build-vs-buy verdict: build / buy / depends — hinge: ""
- Primitives: logs \_\_ · dry run \_\_ · versioning \_\_ · API \_\_ · export-to-code
  \_\_ · triggers \_\_ · other: \_\_ (demanded / nice / don't care each)
- Non-user assumption: confirmed / broken — evidence: ""

| Q   | Question                           | Verdict                    | Key evidence |
| --- | ---------------------------------- | -------------------------- | ------------ |
| D1  | Permit survives technical scrutiny | survives / falls / unclear |              |
| D2  | Build-vs-buy line located          | located / vague            |              |
| D3  | Demanded primitives captured       | list complete / gaps       |              |
| D4  | "Developers are non-users" holds   | holds / broken / partial   |              |
