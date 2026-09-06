---
name: Define Done
description: Articulate a goal, its acceptance criteria, and its Definition of Done before creating a task, plan, or goal. Use whenever you author, rewrite, or amend a goal or its criteria — the pattern here governs goal-, criteria-, and DoD-writing everywhere.
context: global
---

# Define Done

A well-formed goal has **three parts**, authored together:

1. **The goal statement** — the request restated as one clear sentence.
2. **Acceptance criteria** — the checklist that decides "done", where every line is
   answerable **yes/no**, by a **number**, or by a named **artifact**.
3. **Definition of Done (DoD)** — the standing quality gates that apply *on top of*
   the criteria.

Load this skill before you author a goal or its criteria anywhere: `create_task`,
`create_plan`, `create_task_in_workspace`, `plan_correct` tail members, or a goal you
are compiling. What you write here is exactly what the Judge will later hold the work
to, and what the setter is asked to confirm — write it for those readers.

## Part 1 — The goal statement

Restate the request as **one clear sentence**, staying close to the setter's own
words and filling only the gaps. Shape:

> Produce `<the outcome>` for `<who or what it serves>`, so that `<the one thing that
> is observably true when it is done>` — `<optional: by when / within a budget or
> attempt limit>`.

Rules:

- **One main outcome.** Not two goals in one sentence; extra outcomes become criteria.
- **An observable end-state, not an activity.** "an itinerary exists covering every
  travel date" — not "plan the trip".
- **The setter's own words.** Echo their nouns and verbs so the restatement cannot
  drift into something they did not mean.
- **A time or effort bound only if the request implies one** — never invented.
- **Do not assert it is achievable.** Feasibility is judged separately; it is not part
  of the sentence.
- The setter approves the statement (with the criteria) before work starts.

## Part 2 — Acceptance criteria

Each criterion must clear the quality bar **and** resolve to one of three judgment
kinds. If you cannot fit a criterion into a kind, it is too vague — rewrite it.

**The three kinds** (state the kind for each criterion):

- **yes/no** — a plain fact that is true or false. "Every travel day has lodging
  assigned."
- **number** — a value against a threshold or comparator. "Each day lists at least 2
  activities, each with an address."
- **artifact** — a named thing produced, changed, or sent, whose existence is
  checkable. "An itinerary document exists covering all travel dates."

**The quality bar** — every criterion must also clear all of these:

1. **Outcome, not activity.** Describe what exists or is true when the work is done —
   never the steps taken. "The summary covers all three quarterly reports" — not "read
   the reports."
2. **Observable.** Its truth can be seen in real evidence: something produced,
   changed, sent, or visible in the record of what happened. If no evidence could ever
   show it, it cannot be judged — rewrite it until it can.
3. **Specific enough to fail.** A criterion any half-effort would satisfy is not a
   criterion. "The email addresses each of the customer's three questions and proposes
   a concrete next step" can fail; "the email is good" cannot.
4. **One thing per criterion.** No "X *and* Y" on one line — a partial result then
   gets an unclear verdict. Split them.
5. **Complete but minimal.** Together the criteria cover everything that matters for
   "done" and nothing that doesn't. Padding with easy criteria dilutes the real ones.
6. **Written for the judge that will read them.** Plain language, no insider
   shorthand, no reference to context the Judge will not have. The Judge sees the
   criteria, the evidence, and nothing else you are thinking right now.

Honestly-subjective outcomes (e.g. "the headline names the product's main benefit")
stay as plain **yes/no** criteria the Judge rules on. Do **not** manufacture fake
numbers for matters of taste — over-quantifying is its own failure.

### Criteria: good vs not good

**Good** (split into judgeable lines):
> - *(number)* "The research note names at least three competitors."
> - *(yes/no)* "Each competitor's pricing model is stated with a source link."
> - *(yes/no)* "The note ends with a recommendation the reader could act on."

**Not good:**
> "Do thorough competitor research and write it up well."

"Thorough" and "well" cannot fail — any output arguably qualifies, and the Judge is
left guessing what the setter meant.

## Part 3 — Definition of Done

The DoD is the set of **standing quality gates** that apply to the goal *on top of*
its own acceptance criteria — the things that must be true of *any* work of this kind,
not just this one goal. **Every goal has a DoD** (it may be short). Generic quality
lives here; outcome-specific checks live in the criteria. Never bolt a generic gate
(e.g. "tests pass") onto a single criterion — it is a DoD item.

**Derive the DoD from these layers, highest authority first:**

1. **Stated in the goal.** Any quality gate the setter named ("…and cite sources")
   is a DoD item, verbatim in intent.
2. **Workspace / project instructions.** The standing conventions already in your
   context (the project's instruction files). Apply the ones that fit this goal's
   kind — "code goals: tests pass, no new lint errors"; "research: cite sources".
3. **The built-in floor.** A few universal gates that apply even when the goal and the
   workspace say nothing: no secrets or credentials in the output; every factual claim
   is grounded, not assumed. This layer is what **guarantees a DoD always exists**.
4. **Bounded inference.** For gaps the layers above leave, infer a few sensible gates
   appropriate to the *kind* of work — but only defensible ones, and **show them** so
   the setter can approve or drop them. Never silently invent quality gates.

The Judge evaluates the goal against **its acceptance criteria and its DoD together**.

## When a technical check earns its place

A technical check (a real command whose exit status is machine-verified, or an
action-count check) is an **optional attachment** to a written criterion or DoD item —
never the starting point, and never required. Attach one only when the work is
technical, the command directly verifies the stated outcome, and it can actually run
in the assignee's environment. A test-suite check on a code change earns its place. A
test check bolted onto a research note, an email, or a summary is noise, not rigor —
its absence on non-technical work is normal, not a deficiency.

## Unclear goals get questions, not guesses

If the goal is genuinely ambiguous — success could reasonably mean two different
things — ask the goal-setter before locking criteria in. A criteria set built on a
guess is worse than a short delay, because "done" then means something the setter
never asked for.

- **Who to ask:** the goal-setter. A person in chat → ask them in the conversation
  (use the `AskUserQuestion` card when available). Delegated work → the setter is the
  delegating agent → ask via `message_parent`.
- **How to ask:** at most a few sharp questions that distinguish the readings you
  actually have. Name the interpretations; never an open-ended "what do you want?".
- **When not to ask:** if the ambiguity would not change the criteria, resolve it
  yourself and state the assumption in the criteria text where the Judge can see it.

## Worked examples (all three parts)

### Software

**You say:** *"add rate limiting to the login endpoint so people can't brute-force it"*

- **Goal statement:** "Add rate limiting to the login API endpoint, so that repeated
  failed sign-in attempts from the same source are throttled against brute-force
  attacks."
- **Acceptance criteria:**
  - *(number)* After 5 failed logins from one IP within 1 minute, the 6th is rejected
    with HTTP 429.
  - *(yes/no)* A successful login resets that IP's failure count.
  - *(yes/no)* The threshold and window are configurable, not hard-coded.
  - *(artifact)* Tests exist covering the "5 allowed / 6th blocked" boundary.
- **Definition of Done:**
  - *(workspace)* Tests pass, no new lint errors.
  - *(floor)* No credentials in logs — failed attempts are not logged with the
    attempted password.
  - *(inferred — confirm)* The 429 response includes a `Retry-After` header.

### Non-software

**You say:** *"help me throw a surprise 40th birthday party for my wife next month"*

- **Goal statement:** "Plan a surprise 40th birthday party for my wife within the next
  month, so that there is a confirmed venue, an invited guest list, arranged food and
  drink, and a workable surprise plan, within budget."
- **Acceptance criteria:**
  - *(yes/no)* A venue is booked for a date next month my wife is free.
  - *(number)* At least 15 guests are invited and their yes/no is tracked.
  - *(yes/no)* Food and drink are arranged for the confirmed headcount.
  - *(artifact)* A written run-of-show exists (who brings her, when, the decoy plan).
  - *(number)* Total cost is at or under the stated budget.
- **Definition of Done:**
  - *(goal)* It stays a surprise — no invitation or plan detail is ever sent to my
    wife.
  - *(floor)* Availability and prices are confirmed, not assumed.
  - *(inferred — confirm)* A backup plan exists if the venue falls through.

## Output shape

Produce, together, and echo all three back to the setter when the flow calls for
confirmation (they are agreeing to what you wrote, so it must say what they meant):

1. **The goal statement** — one clear sentence per Part 1.
2. **The acceptance criteria** — each tagged **yes/no**, **number**, or **artifact**,
   each clearing the quality bar, one thing per line.
3. **The Definition of Done** — derived from the four layers, with any inferred item
   flagged for the setter to approve or drop.
