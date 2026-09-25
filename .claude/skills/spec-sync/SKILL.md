---
name: spec-sync
description: >
  Keeps a landed feature's spec truthful against the code that actually shipped —
  run it after landing (post RED/GREEN/CHECK, the reviewer gate, and the founder's
  yes) or on demand whenever a spec and the code are suspected to disagree. Finds
  the spec under docs/internal/specs/, compares it to the real implementation
  (GitNexus first, Read/Grep fallback), treats the code as fact and fixes the spec
  with a dated correction line, updates the Status field (Draft | In review |
  Approved | Implemented | Superseded), and flags anything that is really an open
  founder decision rather than a plain sync. Loaded on demand by team-lead or
  squad-lead — not preloaded, not run automatically on every commit.
---

# Spec sync

Last reviewed: 2026-09-25

Companion to plan-spec (writes a spec) and grill-spec (reviews a spec). spec-sync
runs after the fact: it makes sure a spec still describes what the code does, once
that code has landed. Root `CLAUDE.md` is the authority on paths and hard rules —
this skill is the procedure for using them to keep a spec honest.

## Scope — where specs live

- Specs: `docs/internal/specs/<name>-spec.md`.
- Reviews: `docs/internal/specs/<name>-spec-review.md`,
  `docs/internal/specs/<name>-spec-review-round2.md`.
- ADRs: `docs/internal/architecture/ADR-NNN-<title>.md`.
- **Never** `docs/plan/`, `docs/specs/`, or `docs/internal/plan/` — those are not
  real locations in this repo; a search that lands there is searching the wrong
  place, not finding an empty result.

The repo's spec history predates today's naming convention, so a search must
recognise the variants actually on disk (`-spec-review-2.md`, `-review-pass2.md`,
`-review-round2.md`, `-review-round3.md`, and so on) — but every file this skill
*writes* uses the canonical current convention above, never a new variant.

## Step 1 — find the spec

1. If the caller names a spec path, use it.
2. Otherwise `Glob` `docs/internal/specs/*-spec.md` for the feature name and list
   every candidate. Never guess which one silently — if more than one plausibly
   matches, ask the caller which spec this run is for.
3. Read the whole spec, not just its traceability table: requirements, BDD
   scenarios, the Reachability section, and any existing `Status:` line.

## Step 2 — find what actually shipped

1. Start from the spec's own traceability table and file-scope section — the
   files it says the feature touches.
2. Confirm against what really changed: `git diff <merge-base>..HEAD --name-status`
   against the integration branch the feature landed on (never against a stale
   local guess).
3. For each requirement or scenario, check current behaviour with GitNexus —
   `context({name: "<symbol>"})` for what a cited symbol does today, `impact` if a
   claim depends on who calls it, `trace`/`explain` where they fit the question.
   If GitNexus doesn't cover a file, or this checkout isn't indexed, say so and
   fall back to `Read`/`Grep` — that is the correct fallback, not a compliance gap.
   Never guess a GitNexus tool name that isn't one of `query`, `context`, `impact`,
   `trace`, `explain` — describe the step in words instead and fall back to grep.
4. At most one narrowly-scoped, tagged local test if a claim genuinely needs one
   (`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestName$' -p 1 ./pkg/<one>/`);
   CI is still the authority for a suite result. `npm run typecheck`, never bare
   `tsc`. Never a whole-repo build or test, never `go test ./...`.

## Step 3 — compare, and the code wins

For every requirement, scenario, and Reachability claim in the spec:

- **Matches** — nothing to do, note it as checked.
- **Spec describes something the code no longer does, or does differently** —
  this is a plain implementation-detail divergence (discovered constraint, a
  route the build actually took). The code is the fact (`CLAUDE.md`, "Code wins
  over docs on any disagreement"). Fix the spec text in place and add a dated
  correction line immediately after the passage, in this repo's own convention:

  ```
  **Correction (YYYY-MM-DD):** <what the spec said, what the code actually does,
  and why>.
  ```

  Never delete the original wrong text — the correction line is what makes the
  drift visible to the next reader. Never invent a `-v1`/`-v2`/`-vN` copy of the
  file: this repo's history is git plus the `-review-roundN` convention, not
  parallel spec versions.
- **Spec and code disagree about something nobody actually decided** — a real
  open design question, not an implementation detail (the kind of thing an ADR or
  the founder would need to rule on). Do not resolve this yourself and do not
  silently pick the code's side. List it under "Flag for founder" instead.
- **Code implements something the spec never mentioned** (new error path, added
  validation) — add it to the spec as a new scenario with a `Traces to:`
  reference, same correction-line treatment.

## Step 4 — the Status field

Update `Status:` to the value that matches reality now: `Draft | In review |
Approved | Implemented | Superseded`. Typical moves: `Approved` → `Implemented`
once the code lands and matches; any status → `Superseded` once a later spec or
ADR replaces this one (link the successor by path).

A spec with no `Status:` line at all is one of the pre-rewrite specs (this repo
has 214 of them) — do not backfill one as a drive-by on an otherwise-untouched
spec; `scripts/check-spec-status.sh` only gates specs that are new or genuinely
changed, so an untouched legacy spec without the field is not itself a finding.
If the spec this run IS syncing lacks the field, add it — it is being changed
anyway.

## Step 5 — report

End every run with:

- A findings table: spec section, divergence, evidence (`file::symbol`),
  resolution (fixed in spec / added as new scenario / flagged for founder).
- A verdict: in sync / synced with corrections / blocked on a founder decision.
- "Flag for founder" — open design disagreements from Step 3, each with the
  spec passage and the code fact that contradicts it, never a recommendation
  dressed up as a decision already made.
- An evidence table whose last row is Self-check, per this repo's reporting
  convention (`docs/internal/false-green-patterns.md`) — name what was actually
  read or run, not assumed.

## What this skill never does

- Never searches `docs/plan/`, `docs/specs/`, or `docs/internal/plan/`.
- Never invents its own versioning scheme for the spec file.
- Never rewrites an ADR's decision — an ADR that looks superseded by the code is
  a finding for team-lead/architect, not an edit spec-sync makes itself.
- Never runs a whole-repo Go build/test or bare `tsc`, and never more than one
  narrow local test process at a time.
- Never mass-updates `Status:` across specs it was not asked to sync.
