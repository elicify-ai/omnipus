---
name: omnipus-failure-triage
description: Load on failure dispatches only — when a developer (backend-lead or frontend-lead) is dispatched onto a red check, broken gate, or broken behaviour anywhere in the Omnipus repo. The procedure runs reproduce-first with a defined exit proof, reads failed CI logs honestly (gh run view --log-failed; cluster logs parsed for RESULT:/GATE FAILURE), narrows the commit window, inspects what the failing test binary links, forces intermittent timings instead of calling flakes, derives per-OS paths, checks which CI workflows never ran on the integration branch, runs at most one narrow local test, and applies the false-green check before reporting any green. Every failure is fixed whatever its origin.
---

# Failure triage — Omnipus

Last reviewed: 2026-09-25 — initial authoring, design §7.2 (dev-team-setup, agent-refresh rollout).

## Standing rules for this dispatch

- **Origin is irrelevant.** Every failure is fixed, whatever its origin — a red on your
  branch, on the integration branch, or pre-existing anywhere. "Pre-existing / not mine /
  broken on main too" are never acceptable closures (root `CLAUDE.md`, Hard Constraint #7).
- **You are the one dispatch on this red check.** Exactly one failure dispatch exists per
  red check at a time; a second fixer collides in the tree. If your evidence shows someone
  else is already on it, stop and tell team-lead instead of continuing.
- The outcome is a fix on your work branch, or a blocked report to team-lead with options
  and a recommendation. Deferring anything needs a tracked issue with a target date and
  explicit founder approval.

## The procedure

### 1. Reproduce first — define the exit proof

Before touching anything, write down the exit proof: the exact command and output that will
demonstrate the bug is gone. No reproduction, no fix attempt.

- Prefer a measurement over reading code: in the session that produced this repo's
  false-green doc, reasoning about code predicted the wrong answer three times out of
  three, and every real defect came from a measurement (`docs/internal/false-green-patterns.md`).
- Verify your worktree first: on the branch the dispatch named, and a known-recent file
  actually present. Stale worktrees have been 1650+ commits behind and produced convincing
  phantom failures; the refresh procedure is in `docs/internal/false-green-patterns.md`.

### 2. Read the failed CI log properly

Get the raw log before believing any wrapper summary.

- GitHub checks: `gh run view --log-failed` prints only the failed steps — start there.
- CI cluster (`deploy/ci-worker/ci-cluster.sh`): the ssh wrapper's exit code is NOT the
  gate's. Parse the log for the final `RESULT:` line and any `GATE FAILURE` line; a failing
  gate can still surface as exit 0. Full trap list: `deploy/ci-worker/CLAUDE.md`.
- Capture exit codes directly, never through a pipe: `cmd > log 2>&1; echo "exit=$?"` —
  `cmd | tail` reports `tail`'s status and has masked a hard compile error here.

### 3. Narrow the commit window

What landed just before the check went red? (`git log --oneline -15 -- pkg/<tree>` or the
`src/` area.) A red that appeared mid-epic usually belongs to the last landing, not to the
feature under test.

Locate the fault in the code graph before editing: GitNexus `query` on the error text,
`context` on the suspect symbol, `trace` for how A reaches B; run `impact` on any symbol
before you edit it; fall back to Read/Grep where the graph lacks coverage. Deeper guide:
`.claude/skills/gitnexus/gitnexus-debugging/SKILL.md`.

Diagnose with at least two competing hypotheses and one piece of evidence per hypothesis
before committing to one — then fix the root cause, not the symptom.

### 4. Check what the failing test binary actually links

`go list -tags goolm,stdjson -deps -test ./pkg/<one>/` lists every package that package's
test binary links (verified working in this repo). A "flaky" package failure is often a
dependency that changed under it — diff the linked set against the commit window from step 3.

### 5. Intermittent? Force the timing — and never call it a flake

- Inject a sleep at the suspected race point to make an intermittent failure deterministic.
- A failure that reproduces twice under isolated re-run is a real defect. Calling a real
  defect a flake is how it survives — investigate to a mechanism (root `CLAUDE.md`).
- Read the signature before trusting any timing result:
  - A `--- FAIL` line is real signal under any load. A bare trailing `FAIL` with zero
    `--- FAIL` lines is the hang/contention signature — inconclusive, not a finding.
  - Check `uptime` before trusting durations; orphaned spin loops once drove load to 548.
  - A failure at exactly a wait deadline means a missed event, not slowness. If the
    property is discrete (attempts, dispatches, calls), count it — widening a threshold
    is never the fix.

### 6. OS path differences

Derive expected paths from the OS, never hard-code them: on macOS `/etc` resolves to
`/private/etc` (symlink — verified), so string-comparing `/etc/...` paths fails only on
macOS. A macOS-only or Windows-only failure is often a path assumption. Cross-platform
compile breaks: `GOOS=<target> go vet -tags goolm,stdjson ./pkg/<one>/` on the narrowed
package, or leave it to the cross-platform CI leg — which does not run on every branch
(step 7).

### 7. Know which CI workflows did NOT run

A green run on the integration branch (your dispatch brief names it — it is never
hard-coded here) is not full coverage. Check it yourself: read the `on:` block of every
workflow in `.github/workflows/` and compare its `push:` branch filters against the
integration branch name.

Verified 2026-09-25 as an illustration of the pattern: on a direct push to the then-current
integration branch, only the PR-check workflow and the code-scanning workflow ran; the
cross-platform matrix, the build matrix and shellcheck fire on pull requests and `main`
pushes only, and the scheduled workflows never run on branch pushes at all. Say in your
report which checks never ran — do not treat the green as full coverage.

### 8. One narrow local test only

- Go: at most ONE narrow local test process at a time, only as
  `CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestName$' -p 1 ./pkg/<one>/` —
  never multiple Go suites in parallel, never the full local suite (it OOM-kills this
  shared environment; CI is the authority). Push the branch and read the checks instead.
  One-at-a-time allows repeats: the red run in step 1 and the green run after the fix
  are two processes, run one after another, never in parallel.
- SPA: `npx vitest run <one-file>`.
- Your step-1 reproduction is the red evidence the fix's green is measured against —
  show both in the report.

### 9. The false-green check — before trusting or reporting any green

Ask: could this instrument have detected the failure at all?

- Exit codes without a pipe (step 2 shape).
- Confirm the test actually ran: `-v`, count `--- PASS` lines by name — `go test -run
  'Pattern'` prints `ok` when the pattern matches nothing.
- An empty search proves nothing: first search for something you know is present.
- Prove a test you wrote or tightened can fail: mutate the implementation, watch the test
  go red, restore. For a test fix, prove the old assertion fails against the same mutation.
- Reference for the full trap catalogue: `docs/internal/false-green-patterns.md`.

## Edges

- **Security trees route through security review.** A fix touching security-lead's focus
  areas (`pkg/auth`, `pkg/credentials`, `pkg/fspolicy`, `pkg/identity`, `pkg/pairing`,
  `pkg/pathsafe`, `pkg/shellrule`, `pkg/security`, `pkg/sandbox`, `pkg/audit`, `pkg/policy`,
  plus gateway rate limiting and gateway auth) is a security change like any other: the
  developer fixes it and security-lead reviews the diff before it lands. The failure path
  never bypasses the security review.
- **Ambiguity or a dead end:** stop and report blocked to team-lead with options and a
  recommendation — never guess silently.
- **A bug found by accident on the way** is a note to team-lead, never a side fix.
- **Reporting:** terse and technical, ending with the evidence table your discipline block
  defines — claim, evidence (command + exit code + key output line, or `file::symbol`, or
  commit SHA), certainty, and the self-check row.
