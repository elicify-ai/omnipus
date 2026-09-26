---
name: omnipus-frontend-rules
description: TypeScript/React-specific operational detail for frontend-lead only — the typecheck gate, the wire-type and design-system load rules, Vite build output, and component-budget exemption. Preloaded via the `skills:` frontmatter field on frontend-lead.md alongside `omnipus-shared-rules`. No other role loads this skill — it is not general Omnipus procedure, it is frontend-lead's own.
---

# Omnipus Frontend Rules

Last reviewed: 2026-09-26 — testid sweep, demo build staleness, long-gate waits

Frontend-lead only. Read `omnipus-shared-rules` first — this skill adds
TypeScript/React-specific detail on top of it, never repeats it. Root `CLAUDE.md` stays
authoritative on the facts this restates.

## Build and test

- `npm run typecheck` (wired to `tsc -b --noEmit`) is the only TypeScript gate that
  means anything. Never bare `tsc --noEmit` # agent-guard: allow — `tsconfig.json` is a project-references
  root with no `include`/`files`, so a bare invocation is a silent no-op that always
  exits 0 (`docs/internal/false-green-patterns.md` section 9).
- Local test runs are capped by shared rule 2: one `npx vitest run <file>` or
  `npx playwright test <x>.spec.ts` at a time; never bare `npm test`, `vitest` or
  `playwright test`. A long run (an e2e area, the node tier) waits in bounded slices:
  `omnipus-shared-rules`, "Headless dispatches and long gates".
- Vite builds to `dist/spa/`, copied to `pkg/gateway/spa/` and embedded via `go:embed` (`CLAUDE.md`, "Tech stack and platforms"). # agent-guard: allow
  `pkg/gateway/spa/` is gitignored and absent until the first SPA build. # agent-guard: allow
  A missing `pkg/gateway/spa/` in a fresh worktree is backend-lead's stub trap, not a frontend defect — do not "fix" it by editing frontend build config. # agent-guard: allow

## Tests

- Never edit or rewrite test files — not even to reconcile legacy assertions with the
  current spec. Loosening an assertion to make it pass (e.g. `toEqual` → `toContain`) is
  forbidden. A legacy test that contradicts the spec goes back to qa-lead, who owns test
  files; implementers change production code, never tests.
- **Changing a testid or markup a test reads? Sweep for the old value in the same
  change.** When you rename or remove a `data-testid`, a `data-*` attribute, or the
  markup a selector targets, `grep -rn '<old-value>' src tests/e2e` before you report;
  every hit goes to qa-lead in the same change, before landing. Tests pinned to an old
  testid (`tool-call-badge`) rotted silently and caused four of five red release checks.

## Founder demos and prototypes

- A prototype or demo for founder review is interactive: the main flows are clickable
  with local state. Frozen state stories alone (e.g. Storybook) are not a demo — 11
  frozen stories were handed over where clicking a message or Compose did nothing.
- Before handover, a real-browser interaction test has passed: Playwright drives every
  advertised interaction and asserts a visible outcome, and the run fails on any
  console or page error. "Renders without errors" and screenshots are not "works".
- Served from a static build of a fixed snapshot in its own worktree — never a live dev
  server inside a worktree where a worker is active (HMR churn has produced
  "connection lost" with no components rendered).
- Before any check or link, confirm the build is not older than the commit it claims to
  show: the build output's time (e.g. its `index.html` modification time) must be later
  than the snapshot's last commit time (`git log -1 --format=%cI`). A demo was served
  from a build older than its claimed commit.

## Design system (load before touching any of these trees)

- Before touching anything under `src/components/`, `src/styles/`, `design-system/`, or
  `packages/ui/` — including adding a button, dialog, color, spacing value, type size,
  shadow, or focus style, or publishing/editing a component manifest — the
  `omnipus-design-system` skill is **preloaded** into your context already (`skills:`
  frontmatter, founder decision Round 7); its rule catalog and the exact script or test
  that enforces each rule live in `.claude/skills/omnipus-design-system/SKILL.md` — read
  it before the first edit in those trees, not after a red build.
- A recurring UI job (tooltip, inline error banner, copy-to-clipboard, …) uses a
  catalogued component, preferring a ported shadcn/ui component over a new local one
  (design-system skill rule 14) — never hand-build one.
- The UX skills (`ux-heuristics-review`, `elicify-ui-ux-design`) load on demand, not
  preloaded, when a task has a UI/UX judgement dimension.

## Contracts and wire types

- Every wire type consumed by the SPA comes only from `src/lib/api/generated/` — never
  hand-written, never copied from a curl response, never a parallel struct/interface
  next to the generated one.
- A contract change lands backend-first (architect shapes it, backend-lead edits the
  spec and regenerates) — frontend-lead consumes the regenerated
  `src/lib/api/generated/` output, never edits `contracts/` directly (cross-stack order:
  contract first, then backend and frontend in parallel).

## Size budgets

- File fails CI over 3,000 lines, function over 240 (shared rule 10) — but a React
  component only **warns**, it never fails, at either threshold. Enforcers:
  `scripts/check-file-budget.sh`, `scripts/check-function-budget.sh`
  (`make lint-budgets`).

## Platforms

- Per-OS path expectations in tests are a shared-skill rule, not a frontend-specific
  one — nothing in the frontend stack introduces its own platform branch; the SPA is
  platform-agnostic by design.
