---
name: Define Done
description: Turn a goal into judgeable acceptance criteria and a Definition of Done before creating a task, plan, or goal. Use whenever you author, rewrite, or amend acceptance criteria — the quality bar here governs criteria-writing everywhere.
context: global
---

# Define Done

Turn a goal into a Definition of Done: a short set of written acceptance criteria a
fair reviewer — human or Judge — can rule on with confidence, and that the person who
set the goal would recognize as their intent.

Load this skill before you author criteria anywhere: `create_task`, `create_plan`,
`create_task_in_workspace`, `plan_correct` tail members, or a goal you are compiling.
The criteria you write are the exact things the Judge will later hold the work to —
write them for that reader.

## The quality bar

Every criterion you write must clear all of these:

1. **Outcome, not activity.** Describe what exists or is true when the work is done —
   never the steps taken. "The summary covers all three quarterly reports" — not
   "read the reports."
2. **Observable.** Its truth can be seen in real evidence: something produced,
   changed, sent, or visible in the record of what happened. If no evidence could
   ever show it, it cannot be judged — rewrite it until it can.
3. **Specific enough to fail.** A criterion that any half-effort would satisfy is not
   a criterion. "The email is polite, addresses each of the customer's three
   questions, and proposes a concrete next step" can fail; "the email is good" cannot.
4. **One thing per criterion.** Each criterion is separately judgeable. Bundling
   three requirements into one line means a partial failure gets an unclear verdict —
   split them.
5. **Complete but minimal.** Together, the criteria cover everything that matters for
   "done" — and nothing that doesn't. Padding with easy criteria dilutes the
   meaningful ones.
6. **Written for the judge that will read them.** Plain language, no insider
   shorthand, no references to context the Judge will not have. The Judge sees the
   criteria, the evidence, and nothing else you are thinking right now.

## When a technical check earns its place

A technical check (a real command whose exit status is machine-verified, or an
action-count check) is an **optional attachment** to a written criterion — never the
starting point, and never a requirement.

Attach one only when it genuinely adds value: the work is technical, the command
directly verifies the outcome the criterion states, and it can actually run in the
assignee's environment. A test-suite check on a code change earns its place. A test
check bolted onto a research note, an email, or a summary is noise, not rigor — its
absence on non-technical work is normal, not a deficiency.

## Worked examples

**Good:**
> "The research note names at least three competitors, states each one's pricing
> model with a source, and ends with a recommendation the reader could act on."

Fails honestly if any part is missing; judgeable from the produced document alone.
(Even better: split into three criteria — competitors named, pricing sourced,
actionable recommendation — so a partial failure gets a clear verdict.)

**Not good:**
> "Do thorough competitor research and write it up well."

"Thorough" and "well" cannot fail — any output arguably qualifies. The Judge is left
guessing what the goal-setter meant.

**Good:**
> "Every customer question from the ticket is answered in the reply."
> "The tone stays apologetic about the delay without over-promising a date we can't
> commit to."

Two criteria, each independently judgeable against the actual reply text.

**Not good:**
> "Handle the ticket. Also the tests should pass."

"Handle" is activity, not outcome — and a test check bolted onto a non-technical goal
is noise, not rigor.

## Unclear goals get questions, not guesses

If the goal is genuinely ambiguous — success could reasonably mean two different
things — ask the goal-setter before locking criteria in. A criteria set built on a
guess is worse than a short delay, because "done" then means something the
goal-setter never asked for.

- **Who to ask:** the goal-setter. For a goal set by a person in chat, ask that
  person in the conversation. For delegated work, the goal-setter is the delegating
  agent — ask it via `message_parent`.
- **How to ask:** at most two or three sharp questions that distinguish the readings
  you actually have. Name the interpretations; do not ask open-ended "what do you
  want?" questions.
- **When not to ask:** if the ambiguity would not change the criteria, resolve it
  yourself and state the assumption in the criteria text where the Judge can see it.

## Output shape

Write the Definition of Done as a short list of plain-language criteria, each one
clearing the quality bar above. Attach a technical payload to a criterion only where
it earned its place. Then echo the criteria back to whoever set the goal when the
flow calls for confirmation — they are agreeing to the list you wrote, so it must
say what they meant.
