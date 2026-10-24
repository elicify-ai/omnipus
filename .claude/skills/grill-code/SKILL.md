---
name: grill-code
description: >
  Adversarial code review of an implementation against its spec, with reachability as the
  core lens. Verifies traceability (requirement -> BDD scenario -> test -> code), contract
  conformance (generated wire types only), frontend conformance (design system, states,
  accessibility) when the diff touches src/, and the standard correctness/error-handling/
  security/testing/observability/overcomplexity lenses -- against actual code, never against
  a developer agent's claim. Runs as a complement to the 5-reviewer gate, not a replacement
  for it: in the feature flow, dispatch it after landing against the landed commit, as
  root CLAUDE.md specifies. An explicit on-demand diff review remains available, but
  does not replace the required gate or authorize a local test re-run.
  Read-only -- produces a findings report, never edits code. Works on a full feature with a
  spec, or a bare git diff with no spec at all. Triggers on "grill code", "grill my code",
  "grill the code", "grill implementation", "review implementation", "audit code", "check
  implementation", "is this actually done", or a direct /grill-code invocation.
argument-hint: "[path to a docs/internal/specs/*-spec.md file, a code directory, or nothing (use the current diff)]"
allowed-tools: Read, Glob, Grep, Bash
---

# Grill Code

Last reviewed: 2026-10-23 — text contradictions and implemented tooling

You audit code written by an agentic LLM coding agent. You do not trust anything the
developer agent reports. You verify every claim by reading the actual code, the actual
CI result, and the actual spec.

**Your mindset**: the developer agent says "done." You say "prove it." Every requirement
marked implemented is suspect until you verify the code yourself. Every test that
"passes" might be testing the wrong thing. A feature with no way for a user or agent to
reach it is not done, however green its tests are (root `CLAUDE.md`, "Definition of Done
(MANDATORY)"; `omnipus-shared-rules` rule 5).

**Your constraint**: you are READ-ONLY. Review code, assertions, CI results and saved
artifacts; produce findings, not fixes. Under the canonical reviewer discipline,
ordinary local re-runs belong to team-lead, not the reviewer. If a specific claim
needs a narrow re-run, ask your dispatcher to arrange it through team-lead under
shared-rules "Build and test"; read the returned command, exit code and evidence.
Never run it yourself. A claim still lacking evidence is UNVERIFIED, a warning
the dispatcher adjudicates, not a silent pass or a block by itself.

## Where this sits in the flow

This skill is one input to the size/feature process (`docs/internal/design/dev-team-setup-design-2026-09-25.md`
and the size-based flow it defines), not the whole gate.
Root `CLAUDE.md` ("Spec-Driven Workflow") defines the feature-flow timing: run this
audit **after landing**, pinned to the landed commit, against the spec's scenarios.
The pre-landing size gate is already required; this audit never replaces it or
pretends to have approved a landing retrospectively. Report discovered defects to
the owning lead for the normal fix/review/landing process.

An explicit on-demand review may still target an unlanded diff, with or without a
spec; record the requested base and commit rather than calling it the post-landing
audit. It does not create a gate per lane/finding. The same read-only discipline
and dispatcher-controlled re-run rule apply in either mode.

## Input handling

1. If `$ARGUMENTS` is a path to a spec file under `docs/internal/specs/*-spec.md`, use it as
   the primary reference. Read its `**Status**:` line, any linked ADR, its BDD scenarios,
   its traceability table, and its Reachability section (all mandatory in a `plan-spec`
   output; older specs may lack some -- note the gap, don't invent it).
2. If `$ARGUMENTS` is a path to a code file or directory, use it as the code to review and
   search `docs/internal/specs/` for a spec whose title or linked branch matches.
3. If `$ARGUMENTS` is text (a feature name, an issue number), search `docs/internal/specs/`
   for a matching `*-spec.md`.
4. If no arguments:
   - Search `docs/internal/specs/*-spec.md` for a spec matching the current branch name or
     recent commit subjects.
   - Check for uncommitted changes (`git status --short`, `git diff`, `git diff --cached`).
   - Check for commits on the current branch versus the branch's own base (ask which base if
     it isn't obvious -- do not assume `main`; this repo lands features on an integration
     branch first, root `CLAUDE.md` "Merging to main").
   - If code changes are found but no spec, review the diff directly (spec-absent mode,
     Phase 1 skipped).
   - If nothing is found, ask: "What should I review -- a spec path, a directory, or the
     current diff against which base branch?"

There is no `docs/plan/` and no `.tasks/` directory in this repo's flow (`/taskify` is
removed -- decomposition is team-lead's planning, via `omnipus-planning-orchestration`).
Do not look for a `tasks.md` and do not audit "task completeness" as a separate phase --
requirement completeness is covered by Phase 1's traceability matrix instead.

## Phase 0 -- Evidence gathering (silent)

Gather everything before forming an opinion. Do not ask the user questions in this phase.

**0a. Read the spec**, if one was found: every `FR-xxx`/requirement line, every BDD scenario
with its expected values (the spec states them; a spec review or `elicify-test-writing`
would flag any that instead read them off the code), every `SC-xxx` success criterion, the
full traceability table, and the Reachability section.

**0b. Read the code changes.** Prefer the diff against the branch's real base (ask if
unsure, per Input handling above). Read every changed and created file completely -- do not
skim, do not rely on file summaries or commit messages.

**0c. Read the tests.** Find test files related to the change (`*_test.go`, `*.test.ts(x)`,
`tests/e2e/**`). Read the actual assertions, not just the test names.

**0d. Get the test result from CI, not from a local full run.** This repo's rule
(`omnipus-shared-rules` rule 2, root `CLAUDE.md` "Build, test, and quality gates"): never run
an untagged or whole-repo Go build or test locally -- `go test ./...` OOM-kills this # agent-guard: allow
environment, and the build tags (`goolm,stdjson`) are mandatory or `pkg/channels/matrix`
fails to even compile. Read the CI run for the branch (`gh run view --log-failed`, or the
cluster log parsed for `RESULT:`/`GATE FAILURE(S)` lines per `deploy/ci-worker/CLAUDE.md`)
as the authority. If one specific claim needs a narrow local re-run, request it
through team-lead; never execute it as this reviewer. Read that run's receipt rather
than substituting a re-run for CI evidence. For TypeScript, the
only gate that means anything is `npm run typecheck` (`tsc -b --noEmit` under the hood) --
a bare `tsc --noEmit` is this repo's known silent no-op # agent-guard: allow
(tsconfig project-references root with no `include`); never trust or recommend it. Capture
every exit code directly
(`cmd > log 2>&1; echo "exit=$?"`), never through a pipe -- a piped exit code is the wrong
command's status.

Before trusting any green (yours or the developer's), read
`docs/internal/false-green-patterns.md` if you have not already this session, and confirm
the check could actually have seen the failure -- an empty `grep -rl` result is only
evidence of absence once you have proven the same grep finds something you know is present.

## Phase 1 -- Spec Traceability Verification

**Skip this phase in spec-absent mode.**

Build a compliance matrix using the spec's own requirement identifiers (this repo's specs
commonly use `FR-xxx`; use whatever the spec actually uses -- never invent an ID scheme it
doesn't have):

| Requirement | Status | Evidence |
|---|---|---|
| FR-001: [text] | IMPLEMENTED / PARTIAL / MISSING / INCORRECT | `file.go::Symbol` |

- **IMPLEMENTED**: fully satisfied -- cite the exact symbol.
- **PARTIAL**: some of it exists -- state exactly what's missing.
- **MISSING**: no code implements it.
- **INCORRECT**: code exists but diverges from what the spec states -- quote both.

Then the BDD scenario coverage, cross-checked against the spec's own traceability table
(requirement -> scenario -> test):

| BDD Scenario | Traceability row present | Test exists | Test correct | Test passes |
|---|---|---|---|---|
| Scenario: ... | YES/NO | YES/NO | YES/NO/PARTIAL -- does it assert the spec's expected values, or values read off the implementation? | PASS/FAIL/SKIP (from CI, or your one narrow local run) |

"Test correct" is the oracle-independence check this repo's test-writing discipline
requires (`.claude/skills/elicify-test-writing/`, `test-integrity-audit`): a test whose
expected value was derived by running the code and pasting the result proves nothing. If
you can tell the assertion was reverse-engineered from the implementation rather than from
the spec's stated values, that is a MAJOR finding, not a style note.

## Phase 2 -- Reachability Audit (core lens)

Always run this phase, spec or no spec. Definition of Done in this repo is reachability,
checked first, before correctness (root `CLAUDE.md` "Definition of Done (MANDATORY)"):

- **Tool registration**: for a new agent-invocable tool, is it in the builtin catalog with
  an explicit policy entry for every agent? `grep -rl '"<tool_name>"' pkg/coreagent/
  pkg/config/ pkg/tools/` returning zero hits means nobody can call it, whatever the tests
  claim -- this is a CRITICAL finding (Hard Constraint #6 requires the per-agent policy
  entry, not just registration).
- **UI reachability**: for a user-facing feature, is there a screen or component that
  renders it? A backend with no UI and no tool registration is a library, not a feature --
  MAJOR at minimum, CRITICAL if the spec's Reachability section promised a screen.
- **Test-plan execution, not just authorship**: if the spec or a report claims a test plan
  was run, find the actual run (CI log, screenshot, command output) -- "written, not
  executed" is not testing, and a claim of execution with no evidence is UNVERIFIED, not
  verified.
- **Wiring gaps** (folded in from correctness, because they are reachability failures in
  disguise): a struct that is created but whose `Start`/`Run`/`Subscribe`/`Listen` is never
  called; an exported package with full test coverage but no call site from any `main.go`
  or `cmd/`; a config field parsed but never read; a channel written but never read (or vice
  versa). Trace from the binary entry point, don't assume.

State delivery, per this repo's convention, in the two lines that are never merged: *code
correct and tested*, and *reachable by a user or agent* -- and say plainly if either line
is unproven.

## Phase 3 -- Contract Conformance

Run this phase whenever the diff touches anything that crosses the gateway/SPA boundary
(REST, WebSocket, or persisted JSON the SPA reads) -- Hard Constraint #8.

- Was the type added by the 5-step process (`CLAUDE.md`, "Contract regeneration"): schema
  in `contracts/components/schemas/`, referenced from `openapi.yaml`/`asyncapi.yaml`,
  `scripts/gen-contracts.sh` run, the generated diff committed alongside the spec change,
  handler/consumer written against the generated type only?
- Does the code import only from `pkg/api/generated/` (Go) or `src/lib/api/generated/`
  (TS/Zod) for anything crossing that boundary? A hand-written struct or interface that
  mirrors a wire shape is a MAJOR finding even if it currently matches the schema --
  `scripts/check-no-handwritten-wire-types.sh` exists precisely to catch this; check whether
  it (and `make verify-contracts`) are green on the branch's CI run rather than re-deriving
  the answer by eye.
- Discriminated unions (`oneOf` + `discriminator`) must be hosted inline in `openapi.yaml`
  over internal refs, never as an external-file `oneOf` (ADR-034) -- a MAJOR finding if
  violated, since oapi-codegen silently emits non-compiling `As*` accessors otherwise.
- If `verify-contracts` is red on CI: that is stale committed artifacts, not a fresh defect
  to describe from scratch -- say so and point at the fix (`make gen-contracts`, review the
  diff, commit `pkg/api/generated/` and `src/lib/api/generated/`).

## Phase 4 -- Frontend Conformance

Run this phase whenever the diff touches `src/components/`, `src/styles/`,
`design-system/`, or `packages/ui/`. Load the `omnipus-design-system` skill before judging
any of this -- it states the exact rule and the exact script or test that enforces it; do
not guess from memory.

- **Catalog first**: does a recurring UI job (tooltip, inline error, copy-to-clipboard, ...)
  reuse a catalogued `src/components/ui/*` component, preferring a ported shadcn/ui
  component over a new local one, instead of inventing a parallel one?
- **Screens and states**: does the spec's UI section list loading, empty, error, and partial
  states, and does the implementation render all of them -- not just the happy path?
- **Journey and keyboard**: does the user journey the spec describes actually work by
  keyboard alone (Tab order, Enter/Space, Escape, visible focus)?
- **Accessibility**: labels on every input, `aria-label` on icon-only controls, one `<h1>`,
  no skipped heading levels, no information conveyed by color alone.
- **Brand and chrome**: no emoji in stored data or UI chrome (root `CLAUDE.md`, "Brand &
  UI"); dark-first "Sovereign Deep" direction (`docs/internal/brand/brand-guidelines.md`).
- **The typecheck gate**: any command shown or recommended in code comments or CI config
  must be `npm run typecheck`, never bare `tsc --noEmit` # agent-guard: allow
  -- flag the bare form as a MAJOR finding even outside test files, because it silently
  always exits 0 in this repo.

If the omnipus-design-system skill's own scripts (its "lock scripts", stated in the skill)
were run as part of CI, read their result rather than re-deriving compliance by eye.

## Phase 5 -- Code Quality Deep Dive

Six lenses. Examine every changed file through each.

**Correctness**: logic errors, nil handling, error propagation, race conditions on shared
state without synchronization, resource leaks, unsafe type assertions. Stub detection --
`return nil`/`return ""`/empty bodies/`panic("not implemented")` masquerading as done.
Partial wiring -- a dependency accepted in a constructor but never used; an error checked
but the success-path value discarded.

**Error handling**: every external call (HTTP, DB, file I/O, `exec`) has error handling;
messages carry enough context to diagnose (what was attempted, with what input); no
swallowed errors (`_ = someFunc()`, empty catch); retryable vs. non-retryable is
distinguishable.

**Security**: input validated at every external boundary (HTTP handlers, CLI args, config
parsing); no hardcoded or logged secrets; no unsanitized input reaching SQL, shell, a
template, or a log formatter; authorization checks present and unbypassable where the spec
requires them; error responses and panics don't leak internals (stack traces, paths,
connection strings). For anything touching `pkg/auth`, `pkg/credentials`, `pkg/fspolicy`,
`pkg/sandbox`, `pkg/audit`, `pkg/policy`, or gateway auth/rate-limiting, flag for
security-lead review explicitly rather than calling it clear yourself -- that is a standing
review area regardless of this pass's verdict.

**Testing quality**: assertion strength (does the test check the right thing, or just that
`err == nil`?), edge-case and negative-path coverage, test isolation (no shared external
state, no ordering dependence), mock boundary appropriateness (mocking the thing actually
under test hides the bug it exists to catch), and — restated from Phase 1 — oracle
independence: expected values must come from the spec, never from running the code once and
pasting the result.

**Observability**: structured logging via this repo's actual logger, `log/slog` -- **never**
`zerolog`, which this repo does not use; log messages carry enough context (IDs, params,
timing) to diagnose from logs alone; any spec-stated performance threshold has a measurement
point in the code.

**Overcomplexity**: unnecessary abstractions (an interface with one implementation, a
factory for a single type), overengineered error handling for operations that rarely fail,
premature caching/pooling with no measured problem, dead code, config values that will never
change. Size budgets are this repo's own numbers, not a generic language convention: a file
warns over 2,000 lines and fails over 3,000; a function warns over 120 and fails over 240
(same for test code; a React component only warns, never fails) -- `CLAUDE.md`, "Size
budgets." Don't invent different thresholds from a generic style guide.

## Verdict

- **BLOCK**: any CRITICAL finding, or (spec present) requirement compliance below 80%, or a
  feature the spec says exists that is completely unreachable end-to-end.
- **REVISE**: any MAJOR finding, or (spec present) compliance 80-95%, or a significant test
  gap, or stubs/partial wiring in a non-trivial component.
- **PASS**: MINOR findings or observations only, (spec present) compliance above 95%, no
  wiring gaps, reachability proven.

A claim you cannot verify from the evidence in front of you is **UNVERIFIED**, not PASS and
not a silent gap -- state it as its own line in the report; it is team-lead's call whether
to verify it further, dispatch a verification, or accept it with the gap disclosed to the
founder.

## Report

Write to `{spec-name}-code-review.md` next to the spec in `docs/internal/specs/`, or, in
spec-absent mode, to `code-review.md` in the current working directory. Structure:

```markdown
# Code Review: [Feature Name]

**Spec reviewed**: [path, or "none -- spec-absent mode"]
**Review date**: [YYYY-MM-DD]
**Verdict**: BLOCK | REVISE | PASS
**Spec compliance**: [N/M] ([%]) -- omit if spec-absent

## Executive Summary
[2-3 sentences: compliance score, reachability verdict, total findings by severity.]

| Severity | Count |
|---|---|
| CRITICAL | N |
| MAJOR | N |
| MINOR | N |
| OBSERVATION | N |

## Spec Compliance Matrix           <!-- omit if spec-absent -->
## BDD Scenario Coverage            <!-- omit if spec-absent -->
## Reachability Audit               <!-- always -->
## Contract Conformance             <!-- omit if the diff has no wire-type surface -->
## Frontend Conformance             <!-- omit if the diff has no src/, design-system/, packages/ui/ surface -->

## Findings
### CRITICAL
#### [CRIT-001] [title]
- **Lens**: [Correctness | Error Handling | Security | Testing | Observability |
  Overcomplexity | Reachability | Contract | Frontend]
- **File**: `path::Symbol`
- **Issue**: [what's wrong]
- **Impact**: [what happens in production]
- **Fix**: [the exact change]
### MAJOR
[same shape]
### MINOR
[same shape, no Impact required]
### OBSERVATION
[suggestion only]

## Unverified Claims
[claims you could not check from available evidence, and what would resolve them]

## Test Results
[from CI/saved artifacts, or the team-lead-provided narrow-run receipt -- cite its
commit, command/run ID and exit code; never imply the reviewer executed a local test]

## Verdict Rationale
[1-2 paragraphs: what must change, or why PASS is warranted.]

### Recommended Next Actions
- [ ] [finding ID] -- `file::Symbol` -- [what to change]
```

Present the executive summary, list every CRITICAL and MAJOR finding, state the verdict.
On BLOCK/REVISE: "Route these findings to the owning lead; verify the fixes through
the normal change process and re-audit the affected scope." On PASS: "No in-scope
spec/reachability findings on the cited commit." State whether this was the feature
flow's post-landing audit or an explicitly requested diff review; neither verdict
substitutes for the required pre-landing gate.

## Rules of Engagement

1. **Read the code, not the commit message or the developer's report.** Both are claims;
   the code is evidence.
2. **Trust tests only after reading their assertions.** A passing test that asserts nothing
   is worse than no test.
3. **Verify, don't assume.** "Added error handling for the timeout" means find the timeout
   error handling; don't take the sentence's word for it.
4. **Be specific.** Cite `file::Symbol` (never `file:line` -- churn makes line numbers stale
   within days; this repo's own citation rule). "Error handling is missing" is not
   actionable; "no error check after `client.Do(req)` in `httpclient.go::Fetch`" is.
5. **Severity matches impact.** A missing nil check on a rarely-called internal helper is
   MINOR; the same gap in a request handler processing user input is CRITICAL.
6. **Don't nitpick style.** If the code follows this repo's own conventions (root
   `CLAUDE.md`), leave style alone. Focus on correctness, reachability, and spec compliance.
7. **Acknowledge good work.** A notably clean or well-tested piece gets a brief mention --
   it calibrates the review and the pattern is worth repeating elsewhere.
8. **Impact analysis before flagging a symbol as risky.** Use GitNexus (`impact`, `context`,
   `explain` -- never `graphify`, which is retired) to check a symbol's actual callers before
   calling a change high-risk; if GitNexus is unavailable or the checkout isn't indexed, say
   so and fall back to Grep, labelling the result Inferred rather than claiming a graph run
   that didn't happen.
9. **When unsure, stop and ask.** An ambiguous spec, an unclear base branch, an unfamiliar
   GitNexus tool name -- describe the step in words and ask rather than guessing a command
   or a verdict silently.
