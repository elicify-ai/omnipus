---
name: elicify-UI-UX-Design
description: >-
  Apply research-backed UI/UX, visual-design, and behavioral-psychology best
  practices in three modes — REVIEW an existing screen/flow against a scored
  checklist, DESIGN a new flow with the happy-path playbook, or pull a focused
  CHECKLIST for a specific concern (forms, accessibility, motion, color, type,
  onboarding, engagement, trust). Use when building, reviewing, or critiquing
  any web/app interface; when asked "is this good UX?", "make this feel
  premium", "why does this convert badly", "audit this page", "design this
  flow", "what keeps users engaged"; or before shipping front-end UI. Every
  criterion carries a hard threshold or a named study so judgments are
  enforceable, not vibes.
---

# UI/UX Design — Review · Design · Checklist

A practitioner's reference distilled from NN/g, Laws of UX, Refactoring UI,
Material Design 3, WCAG 2.2, Baymard, Don Norman, BJ Fogg, Kahneman, Nir Eyal,
and 2025–2026 trend research. It exists to make interface judgments **specific
and checkable** — "body line length 45–75 characters," not "improve
readability."

## Two rules that govern everything

1. **Prefer thresholds to opinions.** Whenever you assert something is wrong,
   attach the number or the law (e.g. "contrast 2.8:1, fails WCAG AA 4.5:1" /
   "5 equal-weight CTAs violates Von Restorff — pick one"). If you can't name a
   threshold or principle, say so and mark it as taste, not a defect.
2. **The same mechanic that engages can manipulate.** Streaks, urgency,
   variable rewards, social proof, and defaults are all dual-use. Whenever you
   recommend one, note the boundary (see `knowledge/engagement.md`). The gate
   is the **Regret Test**: *would a fully-informed user still choose this and
   not regret it?* If no, it's a dark pattern — don't ship it.

## Pick a mode

### Mode 1 — REVIEW (audit an existing screen or flow)
Use when asked to critique, audit, or improve existing UI, or proactively
before shipping front-end work.

1. Identify the surface type (landing, dashboard, form/checkout, onboarding,
   detail page, settings, empty/error/loading state).
2. Run the relevant sections of `knowledge/checklist.md`. Always run:
   **Cognitive load · Visual hierarchy · Accessibility · Happy-path friction.**
   Add the surface-specific sections.
3. For each finding report: **severity** (Critical / Important / Minor) →
   **what** → **the threshold or law it violates** → **the fix**.
   - *Critical* = broken/illegal/blocks task (a11y failure, lost input on
     error, contrast fail, dead happy path, dark pattern).
   - *Important* = measurably hurts (>75-char lines, no inline validation,
     surprise costs, weak hierarchy).
   - *Minor* = polish (spacing rhythm, easing curve, microcopy tone).
4. End with a one-line **verdict**: PASS / PASS WITH NOTES / FAIL, and the top
   3 fixes by impact.
5. If a dev server + browser are available, pair with the **visual-qa** skill to
   verify rendered states; this skill supplies the *criteria*, visual-qa supplies
   the *method*.

### Mode 2 — DESIGN (build a new flow)
Use when creating a new feature, page, or flow from scratch.

1. Read `knowledge/happy-path.md` and design the spine first:
   **Entry → Core task → Completion → Confirmation.** Name the one action that
   defines success and remove everything off that path.
2. Apply `knowledge/visual-system.md` for the concrete tokens (type scale,
   spacing grid, color ramp, motion timings) so the build starts from numbers,
   not guesses.
3. If it's a first-run/signup flow, layer in the activation section of
   `knowledge/happy-path.md` (lead with value, define the aha metric, TTV
   targets).
4. If retention/engagement is a goal, consult `knowledge/engagement.md` and
   pick mechanics — each with its boundary noted.
5. Bake accessibility in from the start (`knowledge/accessibility.md`), not as a
   retrofit. Run the REVIEW checklist on your own design before declaring done.

### Mode 3 — CHECKLIST (focused pull for one concern)
Use when the question is narrow ("are these forms good?", "is the color system
right?", "is this accessible?"). Jump straight to the matching section of
`knowledge/checklist.md` and return only those criteria, each with its number.

## Knowledge files (load the ones the task needs)

| File | Use it for |
|------|-----------|
| `knowledge/checklist.md` | The full scored checklist — ~90 checkable criteria across 11 domains. The backbone of REVIEW and CHECKLIST modes. |
| `knowledge/principles.md` | The cognitive/behavioral laws and the studies behind them (the "why" + origins). Cite these in findings. |
| `knowledge/visual-system.md` | Hard numbers for typography, color, spacing, hierarchy, motion timings. The DESIGN-mode token reference. |
| `knowledge/happy-path.md` | The stage-by-stage flow playbook, form design, friction table, and onboarding/activation. |
| `knowledge/engagement.md` | Engagement & retention mechanics (Hook model, streaks, variable rewards, social proof) — each lightly flagged with its ethical boundary. |
| `knowledge/accessibility.md` | WCAG 2.2 thresholds, the new 2.2 criteria, the 2025–2026 legal landscape, and a mapped a11y checklist. |
| `knowledge/2026-context.md` | What's durable vs hype for 2026: AI-native/agentic UX, the AI trust stack, theming, bento, the craft backlash. |

## Scaling effort

- **Quick check** ("does this look ok?") → run the 5 always-on checklist
  sections, return top issues. Don't dump all 90 criteria.
- **Full audit** ("thoroughly review this") → every applicable section, scored,
  with the verdict and prioritized fix list.
- **Design from scratch** → happy-path spine + visual-system tokens +
  accessibility baseline, then self-review.

Match the depth to the ask. Always lead with the highest-impact, threshold-
backed findings — never bury a contrast failure under a spacing nitpick.
