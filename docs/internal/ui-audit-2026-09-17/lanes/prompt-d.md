You are Lane D of a design-system maturity audit of the Omnipus SPA. Work READ-ONLY except for writing your single report file at the end.

## Repository
/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session (React 19 + Vite 6 + Tailwind v4 + shadcn/ui SPA in src/). The repo rule: Phosphor Icons only — no emoji in UI chrome or stored data (emoji→Phosphor translator exists for chat output only). The UI was recently restructured into per-module folders.

## Your question
Do the screens actually CONSUME the design system, or bypass it? Assess:
1. Library consumption: ratio of screens/components using src/components/ui primitives vs hand-rolled one-off implementations of the same patterns (custom buttons, custom modals, custom cards where a library component exists). Cite the worst offenders.
2. Icon discipline: Phosphor usage vs other icon sets vs raw emoji or inline SVG one-offs. Grep for emoji in JSX/chrome strings, non-Phosphor icon imports.
3. Tailwind sprawl: long ad-hoc class strings duplicating what should be component variants; arbitrary values (w-[437px], text-[13px]) that bypass the token scale. Count and cite.
4. Inline styles & styled one-offs: style={{}} usage, one-off styled wrappers.
5. Pattern drift: same UX pattern (e.g. slide-over panels, confirmation dialogs, empty states) implemented differently across modules. Pick 2–3 patterns and compare implementations across at least 3 modules each.

## Required preparation (do this first)
Read these skill files fully — they are your evaluation criteria:
- /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/.claude/skills/web-design-guidelines/SKILL.md
- /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/.claude/skills/ux-heuristics-review/SKILL.md (H4 Consistency & Standards is your primary lens)

## Method
Grep/Glob-driven. Real counts, real file:line citations. For pattern-drift comparisons, name the files you compared side by side. Mark anything unverifiable as UNVERIFIED.

## Output
Write your report to:
/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session/docs/internal/ui-audit-2026-09-17/lane-d-consistency.md

Format:
# Lane D — Consistency of Consumption
## Maturity verdict: <1–5> (1=ad-hoc, 2=emerging, 3=defined, 4=managed, 5=best-in-class) + one paragraph justification
## Library consumption findings
## Icon discipline findings
## Tailwind sprawl findings (counts + citations)
## Inline style findings
## Pattern drift case studies (2–3 patterns, cross-module comparison)
(each finding: severity Critical/Important/Minor → what → file:line evidence → fix)
## Gaps vs best-in-class
## Top 3 fixes by impact

Then print your maturity verdict line and top 3 fixes as your final message.