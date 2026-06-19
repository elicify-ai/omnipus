# The Happy-Path Playbook

For DESIGN mode. The happy path is the smooth default route a motivated user
takes from arrival to success when nothing goes wrong. Research consensus
(Baymard, NN/g, Wroblewski, Krug): **users take the path of least effort; every
unit of friction shed converts to completions.** Baymard estimates ~35% checkout
conversion lift from flow design alone.

Design the spine first, then decorate. Two laws govern it: Krug's "Don't Make Me
Think" (self-evident at a glance) and NN/g's interaction cost (minimize stops,
recall, backtracks).

## The four stages

### Stage 0 — Entry / intent
- Next action **obvious at a glance**; users scan, don't read.
- Primary CTA states the intent ("Place order", not "Submit").
- Total cost/outcome visible up front — **surprise costs are the #1 abandonment
  cause (39–48%)**. Never defer fees to a later step.

### Stage 1 — Core task (forms / data entry / decisions)
Where most friction lives. A good core task:
- **Asks the minimum.** Avg checkout shows ~13–15 fields; a good guest checkout
  needs **6–8**. Field count predicts UX more than step count.
- **Single column, persistent top-aligned labels**, easiest fields first.
- **Never placeholder-as-label** (it vanishes on input → forces deletion to
  re-read).
- **Smart defaults** for common answers (never for sensitive/consent fields).
- **Progressive disclosure** — show what's relevant now, defer advanced options.
- **Forgiving inputs** — accept any reasonable format and normalize (Postel).
- **Recognition over recall** — never make users remember prior-screen data.
- ≤5 options → radio buttons/visible selectors, not a dropdown; >25 → searchable.
- Mark required *and* optional fields; eliminate optional fields first.
- State password/format rules up front, not via error (hidden password rules
  cause up to 19% sign-in abandonment).

### Stage 2 — Completion (commit / submit)
- Commit action is the **single most prominent element**; de-emphasize or remove
  destructive actions (Reset/Clear).
- **No new info, fees, or steps** at the review/commit step — that destroys
  trust.
- Immediate click feedback; progress indicator for any wait >1 s.

### Stage 3 — Confirmation
- **Clear success state** confirming what happened and what's next (Krug: "show
  them the way home").
- If a later status-tracking step exists, tell the user *now* what reference info
  they'll need.
- Offer the next obvious action; never strand on a dead-end page.

**Cross-cutting**: mistakes are cheap and reversible — a working back button means
slips don't matter much.

## Error handling (the unhappy path made gentle)

- **Prevent first** (constraints, good defaults) before relying on messages.
- **Inline validation on field-exit** — Wroblewski's study: +22% completions,
  +31% satisfaction, −42% time, −22% errors. Not premature (while typing) — that
  scolds before the user finishes.
- **Preserve input on error** — never wipe the form. (The "pick a username or
  die, re-type everything" anti-pattern.)
- Errors are **multi-cue** (outline + color + text, not color alone),
  plain-language, with a suggested fix. Never blame the user; match tone to
  severity.
- Reserve confirmation dialogs for high-cost/irreversible actions; prefer undo
  ("the computer that cried Confirm" → users auto-click through).

## Friction table (common sources → fixes)

| Friction | Fix | Source |
|---|---|---|
| Too many fields | Cut to 6–8; single "Name" field; hide Address-2, coupon, billing-addr | Baymard |
| Surprise costs | Show all costs up front | Baymard |
| Forced account creation | Prominent guest checkout; defer account to post-purchase | Baymard (18–26% abandon) |
| Placeholder-as-label | Persistent visible labels | NN/g |
| Premature validation | Validate on field-exit | NN/g |
| Post-submit validation wipes input | Inline + preserve input | Wroblewski |
| Color-only errors | Outline + red + text + plain-language fix | NN/g |
| Dropdown for 2–5 options | Radio/visible selectors | NN/g |
| No progress on multi-step | "Step 2 of 4" tracker | NN/g |
| Confirmation overload | Reserve for irreversible; prefer undo | NN/g |

## First-run / onboarding & activation

First impressions form in **~50 ms** (Lindgaard et al.) — clean, conventional,
fast wins trust before a word is read.

- **Lead with value, not setup.** Let users hit the core benefit before
  configuration. 7-day activation correlates ~69% with 3-month retention
  (Amplitude). Flip "configure everything, then use" → "experience value, then
  customize" (templates, sample data, "try it now").
- **Minimize time-to-value.** Target TTV < 24 h (< 60 s for mobile core value).
  Optimized first sessions lift Day-1 retention from ~25% to 40%+.
- **Define the activation/aha metric** — the *earliest behavior predictive of
  long-term retention* (Lenny Rachitsky), NOT signup or "used it once". Find it
  via correlation→causation: brainstorm candidate aha actions → regression
  against the retention curve → experiment to confirm causality. Avg activation
  rate ≈ 34%, median ≈ 25%. B2B has two ahas (user + team).
- **Onboard progressively, not upfront.** ~70% skip product tours; they strain
  working memory and make the UI look more complex. Use just-in-time contextual
  tips (Canva: 72% engage contextual vs 19% upfront). Default to *no* tutorial;
  add guidance only where users demonstrably struggle. Make any tour skippable
  and re-launchable.
- **Empty states are onboarding surfaces** — explain why it's empty + show
  what's possible + one prominent CTA. Optionally seed starter/sample content.
- **Endowed-progress checklist** — 3–5 items with 1–2 pre-completed, sequenced
  easy→hard, **persistently visible** (hiding it kills the pull). Don't
  over-endow ("90 of 100 done" kills perceived value).
- **Personalize from a 1–2 question welcome survey** (goal + role) and route to a
  relevant flow; allow switching paths later.
- **Celebrate the first success** appropriately — but never with tone-deaf
  confetti during a high-stress flow (e.g. a failed payment).
- **Signup friction**: essential fields only (often email + password), social/
  SSO as a primary option, inline errors, save state so abandoners resume, total
  onboarding under ~2 minutes.
