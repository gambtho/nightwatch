# Nightshift user test 1 — facilitator kit

**Date:** 2026-08-30 · **Prototype:** UX-only, `npm run dev` (or the hosted copy —
private artifact, shareable from its own menu) · **Session length:** ~45 min
(5 intro · 25 tasks · 10 debrief · 5 buffer)

**What this session must answer** (from the design spec):

1. Does a non-AI user understand the blast-radius diagram **without being taught it**?
2. Does the "I'd get this wrong" verdict build trust or deter?
3. Can they describe a real job **in their own words** and recognize the result as theirs?
4. Does the quiet home read as reassuring or abandoned?

Companion: per-participant notes template →
[`2026-08-30-user-test-1-notes-template.md`](./2026-08-30-user-test-1-notes-template.md)

---

## Phase 0 — Logistics checklist

### Participant screening (recruit against the spec's target user)

- [ ] **Non-technical:** does not write code, YAML, or build automations (no
      Zapier/n8n power use). Developers are explicit non-users — exclude.
- [ ] **Doesn't regularly use AI:** uses ChatGPT/Claude/Copilot less than about once a
      week. Daily AI users are mis-calibrated in the opposite way — exclude.
- [ ] **Has a recurring judgment chore** they're already annoyed by (screener question:
      "Is there something at work you have to do every week that involves reading
      through things and deciding what matters?"). A yes here is what makes intake work.
- [ ] **Bonus, not required:** their chore touches support tickets, customer requests,
      or a shared inbox — this lands intake nearest the scripted scenario.

### Consent and recording

- [ ] Consent form signed **before** recording starts: screen + audio, research use
      only, may stop at any time, no right/wrong answers.
- [ ] If remote, confirm recording consent again on tape once recording begins.

### Dry run (do this yourself the day before, and again 15 min before each session)

- [ ] `npm run dev` starts; walk intake → verdict → build → approve → home, then jump
      to Alert via the top nav.
- [ ] Click **all five inert buttons** yourself so nothing surprises you (list in
      Phase 3).
- [ ] Confirm reloading the browser resets everything (no persistence) — that's the
      between-participants reset.
- [ ] Reread the five scripted inert-button lines and the intake nudge ladder.

### Remote vs local

- [ ] **Local:** prototype on the facilitator laptop, participant drives with mouse and
      keyboard. Facilitator sits beside, notes on a second device.
- [ ] **Remote:** share the hosted artifact link with the participant (share from the
      artifact's own menu — it is private by default) and have them drive while
      screen-sharing; fall back to facilitator screen-share with remote control if the
      link can't be shared. Test the link in an incognito window first.
- [ ] Either way: participant drives. If the facilitator ever has to click (the nav
      jump to Alert), say so out loud.
- [ ] The top "🌙 Nightshift prototype" nav bar is **facilitator equipment**. Ask the
      participant at the start to ignore it; only the facilitator uses it (once, for the
      Alert jump).

---

## Phase 1 — Intro framing (read aloud, ~5 min)

**Say:**

> "Thanks for doing this. We're testing an early design, not you — nothing you do is
> wrong, and confusing moments are the most useful thing you can give us. Please think
> out loud the whole way: what you're looking at, what you expect, what surprises you.
> Some parts aren't built yet, so a button might do nothing — if that happens, just tell
> me what you expected it to do. I'll mostly stay quiet; when I answer a question with a
> question, that's me doing my job, not dodging you."

**Deliberately do NOT say** (each of these would contaminate a research question):

- Anything about the permit/blast-radius diagram, what it means, or that it exists.
  Question 1 is whether it's understood **untaught** — if you explain it, the session
  can't answer its main question.
- "AI", "agent", "automation", or "safety" as framing. Introduce it only as: _"a tool
  that's meant to take over a recurring chore for you."_
- The words "approve", "permission", "what it can touch" — let the screens introduce
  those.
- Any preview of the scenario ("support tickets", "weekly digest") — that's steering
  work for Phase 2, done only if needed, from the nudge ladder.
- That silence/quiet is the intended design of the home screen.

---

## Phase 2 — Task sequence (participant drives, ~25 min)

### Act 1: Intake — "getting the job in their words"

Set up **before** they see the screen (this is the pre-steering; see nudge ladder below):

> "Think of a chore at work that comes back every week or so — something where you have
> to read through stuff that piled up and tell someone what matters. Got one? Hold onto
> it."

Then show the intake screen: _"Describe that chore to this thing, the way you'd
describe it to a coworker."_

- Watch: do they freeze? Do they use the starter chips? Do they write job language or
  try to write "instructions"?
- If they click **"See what you'd do"** with an empty box, use inert-button line 1.

#### Intake steering (research question 3 depends on this)

The verdict, build conversation, and every later screen script a **weekly
support-ticket digest** ("Every Monday, look at last week's support tickets and tell
the team what keeps coming up"). If intake lands far from that, the participant will be
shown someone else's job and question 3 can't be answered. Steer **before they type**,
with the minimum rung of this ladder, and record which rung you used:

1. _(nothing — their chore already involves recurring reading/summarizing)_ — say
   nothing more.
2. "Is there a version of that that happens weekly — a Monday kind of chore?"
3. "Anything where things pile up — messages, requests, tickets — and you have to go
   through them and pull out the themes for other people?"
4. **Last resort:** "A lot of people mention going through customer requests or support
   tickets. Anything like that anywhere in your week — even someone else's job you know
   well?"

Never dictate the sentence, never type for them, never say "digest" or "summarize the
tickets for the team." The point of the review note is that the words must stay theirs
— the ladder narrows the _territory_, not the phrasing.

**If they still land far away** (e.g. "schedule my meetings"): let them submit anyway
and watch the verdict moment — the mismatch reaction is real data, just not an answer
to question 3. Then say: _"This prototype has only learned one job so far, and it's
about to answer as if you'd asked about a different chore. Read it and tell me how
close or far it feels from yours."_ Mark question 3 **not cleanly answered** in notes.

### Act 2: Verdict — "the honest no"

Say nothing while they read. This screen answers question 2, and your silence is the
instrument.

- Watch: do they read the "I'D GET THIS WRONG" block, or skim past it? Facial/verbal
  reaction? Do they click "Build this" readily, hesitantly, or not at all?
- After they react (not before): _"Does this sound like the chore you described?"_
  (question 3 evidence) and _"What went through your mind on that middle section?"_
  (question 2 evidence).

### Act 3: Build — "chat with a live permit"

Frame the scripted chat honestly:

> "From here, the conversation is pre-recorded from someone with a chore like the one
> you described. Click Continue to step through it, read along, and tell me where it
> matches what you'd have said — and where it doesn't."

- Watch (question 1): do they **notice** the right panel at all? Do they notice it
  changing when a message adds capability (Zendesk read, Slack #support read, Slack
  #team-digest write)? Say nothing about it — noticing unprompted is the data.
- Watch (question 3): reaction to the scripted user turns — "that's basically me" vs
  "I wouldn't have said that."
- End of script: they click "Review what it can touch."

### Act 4: Approve — "the blast radius"

Say nothing while they take it in. Then the comprehension check, in this order:

1. _"In your own words, what is this screen telling you?"_
2. _"Once you hit approve, could this thing send an email to your boss?"_ (Correct
   answer: no — Email is struck out below the line.)
3. _"What does the dashed line mean to you?"_ — only if 1–2 didn't already cover it.

Let them choose freely between "Approve & schedule" and "Change what it can reach." If
they click the latter, use inert-button line 2. If they hesitate on approving, probe
(_"What would you want to know before pressing it?"_) rather than reassure.

### Act 5: Quiet home — "silence is good"

They land here after approving. Say nothing for a good ten seconds.

- Watch (question 4): what do they look at? Do they find "You don't need to check this
  page. If something goes wrong, we'll come find you."? Reaction to it?
- Then: _"You've approved it. It's Wednesday. What do you do with this page, if
  anything?"_

### Act 6: The alert — "it comes to find you"

Facilitator jumps via the top nav (say so): _"I'm going to skip us three weeks ahead.
It's a Tuesday; this arrived in your email and as a phone notification — you didn't
open the app."_ Then let them read.

- Watch: do they understand which promise broke ("flags security issues separately"),
  that it already paused itself, and why? Anger, relief, confusion?
- Every button here is inert — lines 3, 4, 5 below. Which one they reach for **is the
  data**; always ask what they expected before giving the line.

---

## Phase 3 — Inert-button responses (decided in advance, per the review note)

Universal rule: when an inert button is clicked, first ask _"what did you expect that
to do?"_, note the answer, **then** give the scripted line. Never apologize at length,
never improvise product promises.

| #   | Where   | Button                                                                                  | Scripted facilitator line (after the expectation probe)                                                                                                     |
| --- | ------- | --------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | Intake  | **"See what you'd do"** with an empty box (it silently does nothing — no error appears) | "It needs a few words first — nothing's wrong. Go ahead and type it the way you'd tell a coworker."                                                         |
| 2   | Approve | **"Change what it can reach"**                                                          | "Not built in this prototype. In the finished product it would take you back to the conversation to renegotiate. What were you hoping to add or take away?" |
| 3   | Alert   | **"Show me the 3 runs"**                                                                | "Not built here. It would show you the three Monday digests it actually sent. What were you hoping to check in them?"                                       |
| 4   | Alert   | **"Let's fix it"**                                                                      | "Not built here. It would reopen the conversation with this problem already on the table. What's the first thing you'd say to it?"                          |
| 5   | Alert   | **"It's fine, resume"**                                                                 | "Not built here. It would un-pause the workflow and it'd run again Monday, still guessing at the security flags. Walk me through why resume felt right."    |

Every other button in the prototype works.

---

## Phase 4 — Per-research-question observation guide

### Q1 — Blast-radius diagram understood without teaching?

- **Moments:** first glance at the Build right panel; whether they notice the
  highlight when a capability is added; the Approve screen silence; the three
  comprehension checks.
- **Pass:** unprompted, they describe reading vs writing (or "looking at" vs "posting"),
  the boundary ("it can't go past this"), at least one struck-out item, and answer the
  email question correctly.
- **Fail:** the panel is treated as decoration; they ask "what is this?"; they answer
  the email question wrong or with "I guess it could?"
- **Probes:** "What can this thing actually do once you approve?" · "Is there anything
  it can't do?" · "What's the $2.00 about, do you think?"

### Q2 — Does "I'd get this wrong" build trust or deter?

- **Moments:** reading the middle block on Verdict; the decision to click "Build this";
  any spontaneous comment on honesty or weakness.
- **Pass (trust):** credibility language ("good that it says that", "more believable"),
  proceeds without added hesitation.
- **Fail (deter):** cites the limitation as a reason to stop or downgrade ("so it can't
  really do the job"), or visibly loses interest. (Spec fallback if this dominates:
  surface limitations after the first successful run instead.)
- **Inconclusive:** skims past without reading — note it; that's a layout finding, not
  an answer.
- **Probes:** "What went through your mind on that middle section?" · "How did it change
  what you expect from this thing?" · "Would you have preferred it not mention that?"

### Q3 — Own words in, recognized as theirs out?

- **Moments:** what they type at intake (verbatim into notes) and which nudge rung it
  took; "does this sound like your chore?" at Verdict; reactions to the scripted user
  turns in Build; whether they talk about the workflow as "it" or "mine."
- **Pass:** typed a real chore of their own with rung ≤ 3; at Verdict and Build says
  some form of "that's basically my situation"; at Approve treats the thing being
  approved as theirs.
- **Fail:** couldn't produce a chore without rung 4; says "that's not what I meant /
  not my job" at Verdict or Build; talks about the scenario in the third person
  throughout.
- **Probes:** "Whose words do these messages feel like?" · "If this ran next Monday,
  is the result something you'd use as-is?" · "What would you have said differently?"

### Q4 — Quiet home: reassuring or abandoned?

- **Moments:** the ten silent seconds on Home; whether they find and react to the
  "we'll come find you" line; the Wednesday question; their reaction at the Alert to
  the claim that it came via email/push.
- **Pass:** relief or approval ("good, I don't want another dashboard"), says they
  wouldn't check, and at the Alert treats being found as expected behavior.
- **Fail:** anxiety ("how do I know it's actually running?"), plans to check daily,
  reads the sparse page as broken, unfinished, or dead.
- **Probes:** "When would you come back to this page, if ever?" · "How do you feel
  about being told not to check?" · "What do you expect happens if it breaks and
  you're on vacation?"

---

## Phase 5 — Debrief (~10 min)

Run in this order; the connector elicitation is mandatory (open risk #1 in the spec is
that the connector ceiling binds before the UX does — this test must collect demand).

1. **Overall:** "In one sentence, what is this product to you?" · "Would you let it run
   next Monday without you? What would stop you?"
2. **Desired-connectors elicitation:** "Forget this prototype's scenario. Walk me
   through where the stuff you'd actually want handled _lives_." Then walk the list and
   mark each: email — calendar — chat (Slack/Teams/other) — tickets/helpdesk — CRM —
   spreadsheets — shared drive — anything else (name it). For the chore they typed at
   intake: "Which of those would this thing have needed to reach?" Rank their top
   three. Record product names verbatim ("Zendesk", "HubSpot", "that Access database").
3. **The one gate:** "You approved it once and it ran for weeks. How do you feel about
   never being asked again?"
4. **Close:** "What almost made you say no?" · "Anything you kept expecting that never
   appeared?"

Immediately after the participant leaves (before notes go cold): fill all four rows of
the notes template's verdict table, using each row's own labels (pass/fail, trust/deter,
reassuring/abandoned, and their unclear variants), plus the top-3 connectors, and write
the single most surprising moment in one sentence.
