# False greens: how this suite lies, and how to catch it

**Status:** operating guidance for every agent working in this repo. Written 2026-08-20
after a session that found four real defects — three of them invisible to code review and
all of them sitting behind a green pipeline.

The standing rule from the operator is short: **"the pipeline must not pass if not true."**
No baselines, no ratchets, no allowlists that manufacture green, no suppressing a rule to
reach it. This document is the practical version of that rule — the specific ways green has
been wrong here, and the cheapest check that exposes each one.

Read the two lists first. Everything after is evidence.

---

## The rule that would have saved the most time

**Reproduce a reported failure yourself before dispatching anyone to fix it.**

In the session that produced this document, three of the five "must fix" items did not
exist. They were artifacts of stale checkouts and machine load. Each cost an agent hours.
The check that would have caught all three is one command against the working branch.

Conversely, **every real defect that session came from a measurement, not from reading
code**: a runtime trace, a torn-read count, a coverage tally. Reasoning about the code
predicted the wrong answer three times out of three.

Weight your effort accordingly. Measure first.

---

## Checklist: before you claim something passes

1. **Capture exit codes without a pipe.** `cmd > log 2>&1; echo "exit=$?"` — never
   `cmd | tail`, which reports `tail`'s status. This has produced a false green here.
2. **Confirm the test actually ran.** `go test -run 'Pattern'` prints `ok` when the pattern
   matches *nothing*. Use `-v` and count `--- PASS` lines by name.
3. **Use the build tags.** `-tags "goolm stdjson"`. Without them `pkg/agent` does not
   compile, and the error (`build constraints exclude all Go files`) reads like a broken
   package rather than a missing flag.
4. **Bind the verdict to a tree hash.** A result from a tree that moved mid-run tells you
   nothing. Never run agents against a checkout while a verdict is in flight.
5. **Mutation-test any test you wrote or tightened.** Break the code deliberately, confirm
   the test fails, restore. A test that cannot fail proves nothing.
6. **For a *test* fix, prove the old version was broken.** Run your mutation against the
   *previous* assertion too. If it also fails, you changed nothing that mattered.

## Checklist: signals that a green is lying

- A test asserts on **elapsed wall-clock time** to prove a logic property.
- A test asserts on **file contents as text** (`includes(...)`) to prove behaviour.
- A `for` loop `continue`s past a condition with **no assertion after the loop**.
- `errors.As` / `errors.Is` where the contract is "returns exactly this type".
- A **hardcoded allowlist** decides what CI runs.
- `ok` with **zero** `--- PASS` lines.
- A package `FAIL` with **zero** `--- FAIL` lines → a hang or global fault, not a test failure.
- A linter reports a **suspiciously small, round** number (see the `--max-same-issues` trap).

---

## The traps, with evidence

### 1. `--max-same-issues` silently caps linter output at 3

golangci-lint groups findings by identical message text and, by default, reports only the
first **3** of each. Every baseline count measured without it was wrong:

| linter | measured | actual |
|---|---|---|
| forcetypeassert | 6 | **190** |
| errcheck | 64 | **253** |
| gosec | 41 | **143** |

Three separate agents hit this independently. Always pass **both**:

```
--max-issues-per-linter=0 --max-same-issues=0
```

A small count is the symptom. If a linter you expect to be dirty reports single digits,
assume the cap before you believe the number.

### 2. A substring scan is not a behavioural test

A guard test asserted a component still called `shouldRenderToolCall` by checking the file
text contained that identifier. Deleting the entire gate and leaving the name in a comment
kept **673/673 passing**.

Replaced with a test that mounts each registration and asserts the rendered output is empty
when the gate says hidden. Mutation-proven against a component the author hadn't used.

**Rule:** assert on behaviour, never on source text.

### 3. A stopwatch is not a proof of logic

Two tests used elapsed time as a proxy for a discrete property:

- `TestUpdateToolCallStatusWithRetry_...` — `73.02ms is not less than 67.57ms` (a ~10ms
  margin) to prove "no retry happened".
- `TestEvidenceGate_NeverEmittedMarker_...` — a 2-second deadline to prove "the loop is
  bounded". It was the **sole failure in an 869-second run** and passed in isolation.

Both lie in both directions: spuriously red on a loaded runner, and green on a fast machine
even when the logic breaks. Both now assert the real property — **sleep-call count** and
**dispatch count** — and both fail *faster* than the stopwatch did, because they trip on the
signal instead of waiting out a clock.

**Rule:** if the property is discrete (attempts, dispatches, calls), count it. Widening a
threshold is never the fix.

### 4. A hardcoded allowlist decides what CI runs

The vitest job sharded across a fixed list of paths. Anything outside it was skipped
silently while the job reported green:

- **116 of 422 test files (27%) never ran** — all 57 under `src/components/workspaces/`
  (the v0.3 flagship), all 11 under `src/components/browser/`.
- Two patterns pointed at directories **deleted long ago**, so the matrix looked broader
  than it was.
- All 116 passed once run. They were never broken — just never executed.

Now enforced by `scripts/check-vitest-coverage.mjs`, which mirrors vitest's own filter
semantics and fails with the exact uncovered list. It caught its author's own incomplete
fix.

**Rule:** make coverage an enforced property, not a maintained list. A hand-kept allowlist
drifts the moment someone adds a directory.

### 5. Collision protection can silently discard the thing you meant to install

`ToolRegistry.Register` was hardened to reject same-name collisions: it keeps the incumbent,
discards the newcomer, and **returns `void`** — so no caller can detect it.

That silently broke a legitimate re-registration path. `browser.RegisterTools` re-runs on
every hot reload and rebuilds each tool with the operator's **current** security state.
Those rebuilt tools were thrown away — 7,777 discarded registrations in one suite run.

Consequence: turning `browser_evaluate` **off** in Settings reported success and left
arbitrary JS execution enabled. Same for screenshot workspace confinement and the SSRF
checker.

Use `RegisterReplacing` where re-registration is expected and first-party.

**How it was found:** not by reading code. A static grep predicted three colliding names;
**all three collided zero times**. Instrumenting the reject path with a stack dump and
running the suite found the real ones.

### 6. Taking a lock can create the file you were protecting

`fileutil.WithFlock` opens whatever path it is given with `O_CREATE`. `AppendRetro` locked
the **data file itself**, so acquiring the lock published a zero-byte retro before any
content was written. Anything listing the directory could see it and conclude the retro had
landed — which is exactly what the recap idempotency check does.

Measured 12 torn reads in 200 rounds; 0 in 400 after. Fixed by locking a sidecar `.lock` and
publishing via `WriteFileAtomic`.

**Rule:** lock a sidecar, publish atomically. Never lock the path readers watch.

### 7. Synchronising on a value is not synchronising on completion

`TestDurableC1_*` polled the plan store until a field appeared, then returned. But
`runPlanJudgeRound` keeps working past that point (`wakeSupervisor` issues further writes),
so `t.TempDir()` cleanup raced a live writer:
`unlinkat .../plans: directory not empty`.

Caught in the act: `could not persist plan judge PASS ... plan not found` — a write landing
after `RemoveAll`.

**Rule:** wait on the goroutine (`judgeWG`/`wakeWG`), not on a value it happens to write on
the way.

### 8. The generated validator can be weaker than the contract

`AcceptanceCriterion.yaml` declares `additionalProperties: false`, but the generated Zod
schema accepts unknown fields **and** accepts `max_count: 2, min_count: 5`.
`openapi-zod-client` emits no `.strict()` for nested inline objects, and no schema language
expresses the cross-field rule at all. The `STRICT_SCHEMAS` fix-up in `scripts/_gen-ts.sh`
only string-matches **top-level** declarations.

This is a live Constraint #8 fidelity gap. Two `it.fails()` markers in
`src/lib/__adr052__wireContracts.test.ts` document it honestly — vitest reports them as
"expected fail" rather than hiding them.

**Rule:** the contract being right does not mean the generated validator enforces it. Probe
it — **with a control**. Two attempts to verify this produced false results: one used a
wrong payload shape (rejected for unrelated reasons), one used `typeof` assertions that can
never fail. Only a probe with a passing control proved anything.

### 9. A project-references root checks only what its sub-projects include

`npm run typecheck` is the TypeScript gate, and CI runs it as the job "TypeScript Type
Check". It ran `tsc -b --noEmit` against `tsconfig.json` — which is a references root with
no `include`/`files` of its own. It delegated to exactly two sub-projects:
`tsconfig.app.json` (`include: ["src"]`) and `tsconfig.node.json` (`include:
["vite.config.ts", "vite.lib.config.ts"]`).

Everything else was checked by **nothing**: all 79 TypeScript files under `tests/e2e/`
(every Playwright spec, every fixture, global setup/teardown), `playwright.config.ts`, and
`packages/ui/src/index.ts`. `-b` was correct — the well-known trap in that file's own header
— and it still checked nothing there, because `-b` builds the referenced projects and those
projects did not reference the code.

Measured with a control:

| injected `const x: number = "not a number"` in | exit | `error TS` lines |
|---|---|---|
| `tests/e2e/about.spec.ts` | **0** | **0** |
| `src/lib/utils.ts` (control) | 2 | 1 |

Turning the gate on surfaced **16 pre-existing errors across six specs**, several of them
real defects rather than type noise: seven `selectOption({ label: /regex/ })` calls that can
never match (the option is typed `string` and compared with `===` in Playwright's injected
script), five `expect(x).toBe(y, 'message')` calls whose message was silently discarded (the
message argument belongs on `expect()`, not the matcher), and two `locator(..., { exact:
false })` options that are not part of `LocatorOptions` at all.

Fixed by adding `tsconfig.tests.json` as a third referenced sub-project, with
`tsconfig.app.json`'s strictness flags copied verbatim, plus `packages/ui/src` on
`tsconfig.app.json`'s include. `playwright.config.ts` and `packages/**` were also added to
the `spa` path filter in `.github/workflows/pr.yml` — the `typecheck` job is
path-filtered, so a gate that covers a file but never runs for it is the same false green.

**Rule:** for any references root, the question is never "does the gate run?" but "what is
in some sub-project's `include`?" Anything outside every `include` is invisible, and the
gate reports green over it forever. Prove coverage the same way every time — inject an
error, run the gate, and inject the control.

---

## Environment traps that masquerade as bugs

These produced three phantom "failures" in one session. Check them before believing a report.

### Stale worktrees

Agent worktree branches start far behind (observed 1650–1822 commits). Resetting them to the
**base** branch discards the working branch — pairing old production code with current test
files. Symptoms are convincing: deterministic failures, missing files, "this tool does not
exist".

Point agents at the **working branch**:

```
git fetch <main-checkout-path> <working-branch>
git reset --hard FETCH_HEAD
```

Then have them verify a known-recent file exists before starting.

### Build tags are a cache namespace

Go derives its build-cache key from the **full active tag set**. A nested `go build` without
`-tags "goolm stdjson"` shares **zero** cache with the tagged build around it and recompiles
~858 dependencies from scratch. This presented as a 13–17 minute deadlock in
`TestInterruptScope_RequiredByCompiler` that killed the entire package run.

### Do not use a private `GOCACHE`

There is a large warm shared cache and Go's cache is concurrency-safe by design (lock files).
Private caches turn N-way parallelism into N× cold compiles. Several agents independently
chose isolated caches and drove load to 335 with 19 concurrent compiles.

### Machine load corrupts timing measurements

One session's load readings were dominated by **8 orphaned `while :; do :; done` processes
from an unrelated project**, 33 hours old, pegging every core (load average 548). Any
timing-sensitive result taken under that is meaningless. Check `uptime` and `pgrep` before
trusting a duration.

**How to tell contention from a real failure:** contention causes **timeouts**; it does not
cause assertion failures. A `--- FAIL` line is real signal under any load. A bare trailing
`FAIL` with zero `--- FAIL` lines is the contention/hang signature — inconclusive, not a
finding.

---

## Related

- `CLAUDE.md` — Hard Constraint #7 (release responsibility) and #8 (contract-first wire formats).
- `scripts/check-vitest-coverage.mjs` — the coverage guard described above.
- `scripts/check-no-jpeg-screencast.sh` — prior art: a mechanical guard, because a note
  cannot stop `git merge`.
