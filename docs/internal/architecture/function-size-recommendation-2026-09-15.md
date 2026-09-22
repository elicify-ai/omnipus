# Function-size recommendation: Go and TypeScript

> **Status note (added on import, 2026-09-15):** this is the sizing study as delivered. Its numeric proposal (fail at 120 lines or 40 statements) was **superseded the same day by the founder's ruling**: files warn at 2,000 and fail at 3,000 lines (fail limit set 2026-09-22); functions warn at 120 and fail at 240 lines. The ruling and the resulting test design live in `draft-module-map.md`, sections "Size budgets" and "How we enforce it". Kept here for the statement-count and nesting data, the norms table with sources, and the first-ten ordering, which the plan reuses.

**Tree measured:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release`, branch `release/v0.1.1`, tip `d9a0c6941eb0d02d470a60e3d89916ea648689dc` (read-only). All Go numbers in this report were regenerated against that exact commit with the tools described below; TS numbers reuse the pre-existing full-distribution scan at the same commit (`results/funlen-ts-all-d9a0c6941.txt`).

A related same-day document, `docs/internal/architecture/function-length-baseline-2026-09-15.md`, already measured a **lines-only** baseline against a slightly different commit (`release/v0.1.1` @ `1f996b01d`, one merge earlier). Its counts (Go production 315 >120, TS production 184 >120) match this report's exactly, and its own header says "Status: measurement, not a decision." This report is the decision it was waiting on: it adds the statement/nesting/classification axes that document doesn't have, and turns the measurement into one specific, numbered recommendation.

"Jargon, defined once": **funlen** is the name of a Go lint check (and its line/statement thresholds) that flags long functions. **Statement count** means how many individual instructions (an `if`, a `return`, an assignment, a loop) a function body contains — two functions can have the same line count and wildly different statement counts if one is a big data table and the other is a nest of decisions. **Nesting depth** is how many `if`/`for`/`switch` layers deep the code goes — a proxy for how hard a function is to hold in your head. **Cognitive complexity** is a scoring method (used by the SonarQube and gocognit tools) that tries to approximate that same "hard to hold in your head" feeling with a formula, rather than a raw line or statement count. A **ratchet** is a rule that can only get stricter over time, never looser — like a grandfather clause that is allowed to shrink but never grow.

---

## Recommendation

| Set | Target (aim for in new code) | Ceiling (CI hard-rejects a new violation) | Exempt by construction | Functions on the initial grandfather list |
|---|---|---|---|---|
| **Go production** | 60 lines / 40 statements | 120 lines **or** 40 statements | Explicitly annotated data/config literals (`//nolint:funlen // data literal, see <ref>`); generated code (`pkg/api/generated/**`) | 640 (315 by line count alone; +325 more caught only by the statement count — see "Why a pair" below) |
| **Go tests** | 60 lines / 40 statements | 200 lines **or** 60 statements | Table-driven `Test*`/`Benchmark*` functions whose bulk is a case-table literal (their statement count already stays low — see Appendix) | 282 |
| **TS production** | 60 lines (plain function) / 150 lines (React component) | 200 lines (plain function) **or** 300 lines (React component, detected as a `.tsx` top-level PascalCase function returning JSX) | Generated wire types (`src/lib/api/generated/**`), Zod schema declarations | ≤103 today at a single 200-line line count (component/non-component split not yet automated — see "What I did not measure") |
| **TS tests** | 60 lines | 200 lines | Fixture/mock setup objects; a `describe`/`it` body that is a single case table | 56 |

**The gate is a pair — lines OR statements — not lines alone and not cognitive complexity.** Lines alone lets a dense, hard-to-follow function through if it's packed tight (few blank lines, long expressions). Statements alone lets a genuinely enormous but simple function (a 1,000-line config literal) get flagged as if it were logic. Cognitive complexity is the theoretically "best" single number, but this repo has never actually run it (the `gocognit`/`gocyclo` settings in `.golangci.yaml` are configured *and disabled*, same as `funlen* itself — see below), there's no equivalent tool wired up for TypeScript, and standing up and validating a new complexity metric is a bigger lift than turning on two numbers the repo already wrote down. Recommendation: ship the lines/statements pair now; treat cognitive complexity as a second-phase signal once `gocognit` has actually been run against this codebase even once.

---

## Why

### The repository already tried to answer this question and then muted the answer

`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release/.golangci.yaml` line 96-98 configures:

```yaml
funlen:
  lines: 120
  statements: 40
```

But `funlen` is listed in `linters.disable` (line 61) — so this setting does nothing. It is excluded a **second** time, redundantly, in an `issues.exclude-rules` block scoped to `_test.go` files (lines 313-317) — redundant because a globally disabled linter needs no path exclusion. `gocognit` (min-complexity 25) and `gocyclo` (min-complexity 20) are configured the same way: present in `settings`, present in `disable`. All three checks for "is this function too big/too tangled" are written down and turned off. This is the exact failure pattern `docs/internal/false-green-patterns.md` warns about generally, playing out specifically here: a control that looks live in the config file and does nothing.

The **120 lines / 40 statements** numbers already in that file are not arbitrary — they turn out to match SonarQube's own Go-specific default for the equivalent rule (see the norms table below) — so re-enabling rather than re-inventing the ceiling is both cheaper and independently corroborated.

### Our own shape (measured this session)

I extended the existing line-count scanner (`cmd/funlen`) into a new tool, `cmd/funstats` (`/Users/danielpiatkowski/AI-Agent-Workspace/loop-split-bench/cmd/funstats/main.go`), that walks the same Go AST and additionally reports, per function: statement count (every `ast.Stmt` node except bare blocks, counted recursively — this deliberately counts nested statements, so an `if` inside a `for` inside a `for` counts once at whatever depth it sits), max nesting depth (increments only on entering the body of an `if`/`for`/`range`/`switch`/`type switch`/`select`; a bare block does not add depth), and a test-helper heuristic (a `_test.go` function that is not itself a `Test*`/`Benchmark*`/`Fuzz*`/`Example*` entry point, and either takes a `*testing.T/B/F/TB` parameter or calls `.Helper()` in its body — this is a heuristic, not a certainty).

Full output: `/Users/danielpiatkowski/AI-Agent-Workspace/loop-split-bench/results/funstats-go-all-d9a0c6941.txt` (32,378 functions). Re-running it reproduced the given line-count percentiles exactly (Go production mean 24.9, median 12, p90 55, p95 85, p99 191, max 4,364 — a useful sanity check that the new tool agrees with the old one).

**Statement percentiles (new data):**

| Set | n | mean | median | p75 | p90 | p95 | p99 | max |
|---|---|---|---|---|---|---|---|---|
| Go production | 11,723 | 12.2 | 6 | 15 | 28 | 42 | 87 | 1,242 |
| Go tests | 20,655 | 14.3 | 11 | 19 | 29 | 39 | 66 | 216 |

**Nesting-depth percentiles (new data):**

| Set | n | mean | median | p75 | p90 | p95 | p99 | max |
|---|---|---|---|---|---|---|---|---|
| Go production | 11,723 | 1.1 | 1 | 2 | 2 | 3 | 4 | 8 |
| Go tests | 20,655 | 0.8 | 1 | 1 | 2 | 2 | 3 | 6 |

**Why a pair, not lines alone — the correlation the task asked for:**

- Of the 315 Go production functions over 120 lines, **288 (91%) are also over 40 statements.** The two signals mostly agree — a long function is usually also a busy one.
- But 325 functions are **under** 120 lines and **over** 40 statements — dense functions a lines-only gate would miss entirely (example: `pkg/tools/browser/pool.go:698 BrowserPool.Acquire`, 117 lines / 84 statements / depth 4; `pkg/agent/loop.go:5634 AgentLoop.logEvent`, 93 lines / 80 statements). A statement-only or paired gate catches these; a lines-only gate (like the file-budget ratchet's `wc -l`) does not.
- Only **27 functions are over 120 lines but under 40 statements** — the "long-line-few-statement" shape the task predicted would be data literals. Reading them shows the prediction is half right: some genuinely are (`pkg/config/defaults.go:52 DefaultConfig`, 1,003 lines / 3 statements; `pkg/records/knowledgefind/tool.go:196 Parameters`, 137 lines / 1 statement — both a single `return` of a composite literal). Others are real logic that just happens to be statement-light per line, most often because of extremely dense inline comments (`pkg/audit/audit.go:134 IsValidEventName`, 240 lines / 4 statements, is a validation table with a long comment block, not really "logic" either — see the full 27-row list in the Appendix). **Neither axis alone reliably tells "data" from "logic"** — `coreAgentSeed` (1,060 lines) has 76 statements despite being conceptually a policy lookup table, because its map is built with individual keyed assignments rather than one literal. This is why the exemption mechanism recommended below is an explicit, reviewed annotation rather than an automatic threshold on either axis.

**Our own codebase compared to a mature Go standard-library package**, using the same tool: `net/http` (excluding the vendored `h2_bundle.go`, which is generated code from a different project) has 654 production functions, median 8 lines / 5 statements, p90 38 lines, p99 159 lines, max 279 lines. Omnipus production code has a similar *shape* at the middle of the distribution (our median 12 vs. stdlib's 8; our p90 55 vs. stdlib's 38) but a completely different *tail*: stdlib's longest function anywhere in `net/http` is 279 lines; Omnipus has 106 functions longer than that, topping out at 4,364. The middle of the distribution is healthy; the tail is the actual problem, and it's a small number of functions, not a systemic one.

### The norms, with sources

| Source | Number | What it counts | Hard or soft |
|---|---|---|---|
| golangci-lint `funlen` upstream default | 60 lines / 40 statements | Function body span / statement count | Soft — a configurable lint warning, opt-in per repo |
| This repo's own `.golangci.yaml` (currently disabled) | 120 lines / 40 statements | Same | Configured, enforces nothing today |
| revive `function-length` rule, verified against the rule source | 50 statements / 75 lines (rule takes `(maxStmt, maxLines)`, default `50, 75`) | Statement count and line count, either trips it | Soft, and off by default in revive itself — must be explicitly enabled |
| Google Go Style Guide | No fixed number | Principle: refactor when something feels too long, rather than enforce a threshold | Soft, prose-only |
| Uber Go Style Guide | No function-length rule found | Idioms and patterns, not size gates | N/A |
| Linux kernel coding style | "One or two screenfuls" (~48-96 lines at a 24-line screen); explicitly: "maximum length of a function is inversely proportional to the complexity and indentation level" | Function body; explicitly excuses a large flat `switch`-dispatch function as fine even when long | Soft, prose-only, deliberately non-numeric |
| Clean Code (Robert C. Martin) | "Should hardly ever be 20 lines"; "ideal function is two to four lines" | Function length, as a style ideal | Soft opinion — widely cited, also widely disputed in practice (e.g. the qntm.org essay "It's probably time to stop recommending Clean Code") |
| ESLint `max-lines-per-function` | 50 lines (when the rule is turned on — not in any ESLint recommended preset) | Lines, with options to skip blank/comment lines | Soft, opt-in, not in this repo's `eslint.config.js` today |
| ESLint `max-statements` | 10 statements (when turned on) | Statements per function block | Soft, opt-in |
| `eslint-plugin-react-func`'s `max-lines-per-function` | 50 lines | Same idea, React-focused fork; no dedicated "React component size" rule exists in mainline `eslint-plugin-react` | Soft, opt-in, third-party |
| SonarQube rule S138 ("Functions should not have too many lines") | Go default 120 lines; C# 80; PHP 150; JS/TS default ~200 (per SonarSource's own published rule parameters, `rules.sonarsource.com/go/rspec-138/` — I could not re-fetch that page directly in this sandbox (DNS blocked) and am relying on search-engine-cached content of it; treat the exact JS/TS figure as approximate, the Go figure as corroborated by two independent search results) | Function/method line count | Soft — a "Major" code smell by default, configurable, not a hard build failure unless the project's own quality gate says so |
| This repo's own `gocognit`/`gocyclo` settings (currently disabled) | min-complexity 25 / 20 | Cognitive / cyclomatic complexity | Configured, enforces nothing today |
| Hatton's "Goldilocks" defect-density study | Optimum **component/file** size ~200-400 lines of code; both smaller and larger components show higher fault density (a U-shaped, i.e. curvilinear, curve) | Defects per line vs. **component/file** size, not function size | Empirical finding — caution: this is measured at file/module granularity, not function granularity; applying its literal numbers to a function-length rule would be a category error, but the *shape* (both extremes are worse than the middle) is the transferable lesson |
| El Emam, Benlarbi, Goel, Rai (2001), *IEEE TSE* 27(7) | No single number; the finding is that class/module **size** is a confound behind several complexity-metric-to-defect correlations | Whether "complexity metric predicts defects" survives controlling for raw size | Empirical caution: some of the folklore that "high complexity causes bugs" is really "big things have bugs," restated |

### The cost table — what each candidate ceiling costs today

Go production, lines only (from the full distribution, `results/funstats-go-all-d9a0c6941.txt`):

| Candidate ceiling | 60 | 80 | 100 | 120 | 150 | 200 |
|---|---|---|---|---|---|---|
| Functions over it | 1,010 | 661 | 438 | 315 | 197 | 106 |
| Share of 11,723 | 8.6% | 5.6% | 3.7% | 2.7% | 1.7% | 0.9% |

TS production, lines only (from `results/funlen-ts-all-d9a0c6941.txt`):

| Candidate ceiling | 40 | 50 | 60 | 80 | 100 | 120 | 150 | 200 |
|---|---|---|---|---|---|---|---|---|
| Functions over it | 551 | 448 | 387 | 288 | 230 | 184 | 135 | 103 |
| Share of 6,850 | 8.0% | 6.5% | 5.6% | 4.2% | 3.4% | 2.7% | 2.0% | 1.5% |

**Why 120 for Go, not 60, 100, or 200.** 60 (the funlen upstream default) would immediately grandfather 1,010 production functions — 8.6% of the codebase — on day one, an unmanageable starting list for a ratchet that's supposed to visibly shrink. 200 leaves 106 genuine giants uncaught but also lets nearly three times as many oversized functions (315 vs. 106) go completely ungated for a full release cycle. 120 is the smallest ceiling whose day-one list (315, 2.7%) is realistically small enough to work down on the same cadence as the file-size ratchet already running in `docs/internal/architecture/draft-module-map.md`, and it costs nothing to justify to the team because it's the number already sitting, disabled, in `.golangci.yaml` — and it happens to be SonarQube's own independent default for Go, which is a second, unrelated source landing on the same number.

**Why 200 for TS, not 120.** Unlike Go, a large share of TS "functions" over 120 lines are whole React components (see the top of `results/funlen-ts-all-d9a0c6941.txt`: 15 of the top 19 offenders are components, hooks, or a store, by name). A component that owns several hooks and a branching form legitimately runs 150-250 lines without being unreasonable. Gating at 120 would flag ordinary components alongside genuine outliers (184 vs. 103 at 200), diluting the list until reviewers start ignoring it — the same "everything is flagged, so nothing is" failure this exercise is trying to avoid. 200 also matches SonarQube's own JS/TS default (with the sourcing caveat above), keeping the number externally corroborated rather than invented.

---

## Ratchet path

Same model as the file-size ratchet already running (`docs/internal/architecture/draft-module-map.md`, "How we enforce it"): a checked-in list, shrink-only, new violations never join it.

1. **Grandfather list**, one entry per `file:FunctionName` (using the receiver-qualified name the tools already print, e.g. `pkg/tools/task.go:TaskUpdateTool.Execute`), recorded with its **current** size (lines and statements) at the moment it's grandfathered.
2. **A function already on the list** fails CI only if it has **grown** past its recorded size. Shrinking it — even by one line or one statement — updates the recorded number down; once it's under the ceiling, it drops off the list entirely.
3. **A function not on the list** that is born over the ceiling fails immediately — there is no path to add it to the list from inside the same change. (Mirrors `check-file-budget.sh`'s "new files never join the list.")
4. **Annotated data literals** are removed from the grandfather list by tagging, not by shrinking: an explicit `//nolint:funlen // data literal, see <doc/ADR>` comment at the function, reviewed once. This is deliberately a human, reviewed decision, not an automatic classifier, because (as shown above) statement count alone misclassifies `coreAgentSeed`.

**First ten to work down, in this order, and why:**

| # | Function | Lines / Stmts | Why first |
|---|---|---|---|
| 1 | `pkg/config/defaults.go:52 DefaultConfig` | 1,003 / 3 | Zero-risk: tag as an exempt data literal (single `return` of a struct literal), not a refactor. Cheapest possible win — removes the #9 longest function from the list with one comment. |
| 2 | `pkg/coreagent/core.go:798 coreAgentSeed` | 1,060 / 76 | Same move as #1 — a policy table, not logic, despite the statement count. Do this and #1 first, before touching any real control flow. |
| 3 | `pkg/agent/loop.go:9740 AgentLoop.runTurn` | 4,364 / 1,242 | The single largest function in the repo (3x the next-largest), and the *only* one in this list with an already-designed extraction plan (`draft-module-map.md`'s "`runTurn` — divide the function, not only the file" section names the exact helper functions to pull out: `prepareTurnWorkDir`, `runPreTurnGates`, `assembleTurnMessages`, etc.). Executing an existing plan is lower-risk than designing a new one. |
| 4 | `pkg/agent/subturn.go:632 spawnSubTurn` | 1,489 / 253 | Second largest; also the function carrying the ADR-032/ADR-057 delegation-identity contract, which means it gets heavy review traffic today regardless — making it navigable pays off repeatedly. |
| 5 | `pkg/agent/loop.go:2414 registerSharedTools` | 1,346 / 380 | Pure wiring (a per-agent loop registering tools) — low risk to split by tool family; the seams are already visible as consecutive `tools.New*Tool(...)` blocks. |
| 6 | `pkg/gateway/gateway.go:4370 setupAndStartServices` | 1,258 / 416 | Same shape as #5 — sequential service setup, low nesting (depth 2), splits cleanly into one function per service. |
| 7 | `pkg/gateway/websocket.go:4000 WSHandler.eventForwarder` | 1,231 / 444 | Already has named local closures (`matchesChatID`, `matchesEvent`) inside it — the extraction seams are already drawn by the current author, just not lifted to top-level functions. |
| 8 | `pkg/gateway/gateway.go:2134 RunContextWithOptions` | 1,090 / 285 | Boot sequence; aligns with `draft-module-map.md`'s own planned `gateway.go` → `gateway_boot.go` split, so this is again executing a plan rather than inventing one. |
| 9 | `pkg/gateway/rest.go:6439 restAPI.HandleProviders` | 1,041 / 427 | Already dispatches via `switch { case method+path }` (confirmed by reading it) — each `case` is a natural extraction into its own handler function. |
| 10 | `pkg/gateway/rest.go:3435 restAPI.updateAgent` | 999 / 346 | Frequently-touched REST handler (the agent-config surface changes often per recent release notes); most day-to-day value from making it navigable soon. |

**Bonus, not size-ranked but worth flagging alongside this work:** `pkg/tools/task.go:1683 TaskUpdateTool.Execute` (397 lines) and `pkg/sysagent/tools/task.go:813 TaskUpdateTool.Execute` (392 lines) are near-duplicate implementations of the same tool in two packages. Deduplicating removes two grandfather-list entries by eliminating one of them, rather than shrinking either.

---

## Enforcement mechanics

**Two candidate mechanisms exist; recommend using both, in different roles, because of a lesson this exact repo already learned the hard way (`.golangci.yaml`'s `funlen` is configured and silently disabled right now).**

1. **golangci-lint `funlen`, re-enabled at 120/40 (Go production only)** — cheapest to turn on: remove `funlen` from the `disable:` list (line 61); the `settings.funlen` block is already correct. Cost: it will immediately fail on all 315+ current violators the moment it's turned on unless each one gets a per-function `//nolint:funlen // grandfathered, tracked in <ratchet doc>` comment. That per-function comment is actually the point, not a workaround: unlike a name buried in a 60-entry YAML `disable:` list (which is exactly how this check went silent the first time), a `//nolint:funlen` comment sits at the violation itself, shows up in the diff that adds it, and is trivially greppable (`grep -rn "nolint:funlen"`) to audit that the count only goes down. Keep `_test.go` excluded from this instance (already configured) — Go tests get their own, looser numbers from mechanism 2 instead of a second golangci profile.

2. **A custom ratchet script — the actual CI gate, mirroring `check-file-budget.sh` and `check-no-jpeg-screencast.sh`.** Extend `cmd/funstats` into a small checker mode (or add `cmd/funbudget`) that applies the four target/ceiling numbers above, reads a checked-in `function-budget-allowlist.txt` (`file:FunctionName<TAB>lines<TAB>stmts`), and fails on: (a) a violator not on the list, or (b) a listed function whose current size exceeds its recorded size. Do the same for TypeScript by extending `scripts/tsfunlen.cjs` into `scripts/check-ts-function-budget.cjs` with the same allowlist format and the component/non-component split. Wire both into `.github/workflows/pr.yml` next to the existing `check-file-budget.sh` and `check-no-jpeg-screencast.sh` calls.

**Which is cheaper, which cannot lie.** golangci-lint is cheaper — it's already running in CI, so enabling `funlen` is a one-line YAML edit with no new tooling. But golangci-lint has no memory across runs: it cannot express "this function may not grow," only "this function is currently over/under N" — so it cannot implement the ratchet property (ratchet requires state: a recorded prior size) on its own. The custom script is a few more seconds of Go/Node execution per CI run, but it is the only one of the two that can hold that state, and — the more important property, per this project's own `false-green-patterns.md` — disabling it requires an editable, reviewable change to the CI workflow file itself, not a one-line addition to a list that already has 60 entries in it and has already hidden one dead check. Recommend: golangci `funlen` as a fast, developer-facing signal (an IDE or pre-commit warning catching new violations early); the custom ratchet script as the actual CI gate that decides pass/fail.

**TypeScript, secondarily:** `eslint.config.js` is a genuinely minimal, correctness-only config today (its own header explicitly refuses to "downgrade, disable, or warn-ify" rules to reach a false green — the opposite instinct from the `.golangci.yaml` disable list). Adding `max-lines-per-function` there as a non-blocking signal, with `// eslint-disable-next-line max-lines-per-function` on today's violators (same greppable-annotation logic as `//nolint:funlen`), would be consistent with that file's stated philosophy. It is not a substitute for the custom TS ratchet script, for the same state-across-runs reason as golangci-lint above.

---

## What I did not measure

- **TypeScript statement counts and nesting depth.** `scripts/tsfunlen.cjs` reports lines only; a `funstats`-equivalent for TS (statement/nesting walk over the TypeScript AST) was not built this session. The TS recommendation above is lines-only as a result — a real gap given the Go data shows lines alone catches only 315 of the true 640 "genuinely oversized" functions.
- **Automated React-component classification for TS.** The 150/300-line component carve-out in the recommendation is justified by manually reading the *names* of the top 30 TS offenders (most are components or hooks by naming convention: PascalCase, `use*`), not by a script that checks "does this function return JSX." Building that check (needed before the TS ratchet script can apply two different ceilings correctly) is follow-up work, not done here.
- **Cognitive complexity, for either language.** `gocognit`/`gocyclo` were read out of `.golangci.yaml` (min-complexity 25/20) but never run — they are disabled, and running them was out of scope for this session. No TypeScript-side cognitive-complexity tool was evaluated.
- **The exact SonarQube JS/TS default for rule S138.** I could not reach `rules.sonarsource.com` directly from this environment (DNS resolution failed); the ~200-line figure cited comes from a search engine's cached/aggregated summary of that page, corroborated by two independent search queries returning the same number, not by reading the primary source myself. The Go default (120) has the same sourcing method but was cross-checked against a second, differently-worded search and against this repo's own already-chosen 120, which is independent corroboration.
- **Whether the `.golangci.yaml`'s `funlen` re-enablement would break the current build.** I did not run `golangci-lint` with `funlen` turned on against the tree to get an exact current pass/fail count with statements included — the "640" and "315" figures come from the `funstats`/`funlen` AST scanners, which use the same span definition golangci-lint uses but were not cross-validated by actually invoking `golangci-lint` itself in this session (CLAUDE.md's own testing rules also caution against running heavy tooling locally in this environment).
- **The 27-function "long-line-few-statement" list's classification is a fast read, not exhaustive analysis** — see the Appendix table; several entries (e.g. `assembleBPFMode`, `DeriveKernelPolicy`) sit in a genuine gray zone between "data/instruction table" and "logic" and would benefit from a second read before being tagged as exempt.

---

## Appendix

### Top 30 Go production functions, classified

Classification method: read each function's body (or, for the largest, its opening ~30 lines plus a grep-based structural scan for `switch`/`case`/`Register(` counts) and judge whether its bulk is genuine control-flow logic, a data/config literal, mechanical wiring (registration/construction), or a dispatch table keyed by a `switch`.

| Rank | Function | Lines | Stmts | Depth | Classification |
|---|---|---|---|---|---|
| 1 | `pkg/agent/loop.go AgentLoop.runTurn` | 4,364 | 1,242 | 7 | Logic (turn state machine; heavy internal switch/select dispatch, but the bulk is orchestration control flow, not a lookup table) |
| 2 | `pkg/agent/subturn.go spawnSubTurn` | 1,489 | 253 | 3 | Logic (delegation setup / identity resolution) |
| 3 | `pkg/agent/loop.go registerSharedTools` | 1,346 | 380 | 4 | Wiring (per-agent tool registration loop) |
| 4 | `pkg/gateway/gateway.go setupAndStartServices` | 1,258 | 416 | 2 | Wiring (sequential service construction) |
| 5 | `pkg/gateway/websocket.go WSHandler.eventForwarder` | 1,231 | 444 | 5 | Switch dispatcher (event-type routing loop, named local closures) |
| 6 | `pkg/gateway/gateway.go RunContextWithOptions` | 1,090 | 285 | 3 | Wiring (boot orchestration) |
| 7 | `pkg/coreagent/core.go coreAgentSeed` | 1,060 | 76 | 3 | Data literal (tool-policy table; mostly comments) |
| 8 | `pkg/gateway/rest.go restAPI.HandleProviders` | 1,041 | 427 | 6 | Switch dispatcher (method + subpath routing) |
| 9 | `pkg/config/defaults.go DefaultConfig` | 1,003 | 3 | 0 | Data literal (single struct literal return) |
| 10 | `pkg/gateway/rest.go restAPI.updateAgent` | 999 | 346 | 4 | Logic |
| 11 | `pkg/gateway/replay.go streamReplay` | 687 | 223 | 5 | Logic |
| 12 | `pkg/gateway/websocket.go WSHandler.handleChatMessage` | 595 | 191 | 4 | Logic |
| 13 | `pkg/gateway/rest_tasks.go restAPI.handleTaskPatch` | 587 | 244 | 4 | Logic |
| 14 | `pkg/agent/cancel.go AgentLoop.RequestCancel` | 584 | 117 | 2 | Logic |
| 15 | `pkg/gateway/rest.go restAPI.createAgent` | 534 | 230 | 3 | Logic |
| 16 | `pkg/gateway/sandbox_apply.go applySandbox` | 504 | 134 | 3 | Logic (heavily commented sequential decisions) |
| 17 | `pkg/gateway/rest_onboarding.go restAPI.HandleCompleteOnboarding` | 500 | 216 | 4 | Logic |
| 18 | `pkg/agent/loop.go NewAgentLoop` | 427 | 100 | 3 | Wiring (constructor) |
| 19 | `pkg/gateway/rest_workspaces.go restAPI.handleWorkspacePut` | 409 | 185 | 7 | Logic (deepest nesting in the top 30) |
| 20 | `pkg/tools/task.go TaskUpdateTool.Execute` | 397 | 181 | 3 | Logic |
| 21 | `pkg/sysagent/tools/task.go TaskUpdateTool.Execute` | 392 | 184 | 3 | Logic (near-duplicate of #20 — dedup candidate) |
| 22 | `pkg/gateway/rest.go restAPI.HandleUpload` | 389 | 185 | 5 | Logic |
| 23 | `pkg/gateway/websocket.go WSHandler.handleAttachSession` | 386 | 97 | 3 | Logic |
| 24 | `pkg/agent/session_end.go AgentLoop.runRecap` | 383 | 134 | 3 | Logic |
| 25 | `pkg/agent/verifier_adjudication.go AgentLoop.runVerifierAdjudication` | 378 | 128 | 4 | Logic |
| 26 | `pkg/gateway/rest.go restAPI.setChannelRouting` | 375 | 201 | 4 | Logic |
| 27 | `pkg/gateway/rest.go restAPI.updateAgentTools` | 373 | 130 | 4 | Logic |
| 28 | `pkg/agent/goal_triggers.go AgentLoop.runGoalAdjudication` | 367 | 99 | 3 | Logic |
| 29 | `pkg/agent/instance.go NewAgentInstance` | 354 | 112 | 4 | Wiring (28 `.Register(...)` calls in the body) |
| 30 | `pkg/tools/message_parent.go MessageParentTool.Execute` | 345 | 164 | 3 | Logic |

**Tally:** 21 logic, 5 wiring, 2 switch dispatcher, 2 data literal (plus #1, `runTurn`, is a logic/dispatcher hybrid). Takeaway: the giants are overwhelmingly real control-flow logic that needs splitting, not data that just needs an exemption tag — only 2 of the top 30 (and a handful more further down the list, see below) are "free" wins.

### The 27 functions over 120 lines but under 40 statements (candidates for the data-literal exemption)

| Lines | Stmts | Depth | Function | Quick read |
|---|---|---|---|---|
| 1,003 | 3 | 0 | `pkg/config/defaults.go DefaultConfig` | Data literal |
| 240 | 4 | 1 | `pkg/audit/audit.go IsValidEventName` | Validation/dispatch table, comment-heavy |
| 182 | 1 | 0 | `pkg/knowledge/knowledge_edit.go EditTool.Parameters` | Data literal (JSON-schema literal) |
| 181 | 32 | 1 | `pkg/sandbox/seccomp_linux.go assembleBPFMode` | Gray zone: BPF instruction table — data-shaped but order-sensitive |
| 174 | 30 | 2 | `pkg/agent/session_end.go AgentLoop.CloseSession` | Logic |
| 168 | 21 | 1 | `pkg/agent/loop.go AgentLoop.processTaskDirectExternalCLI` | Logic |
| 166 | 39 | 2 | `pkg/agent/goal_triggers.go AgentLoop.maybeSettleGoalIdle` | Logic |
| 162 | 10 | 1 | `pkg/coreagent/core.go systemAgentSeed` | Data literal (policy table, like `coreAgentSeed`) |
| 158 | 35 | 3 | `pkg/gateway/websocket.go wsStreamer.Update` | Logic |
| 149 | 31 | 2 | `pkg/agent/context.go ContextBuilder.BuildMessages` | Logic |
| 145 | 37 | 3 | `pkg/sandbox/derive_from_fspolicy.go DeriveKernelPolicy` | Gray zone: derives a policy from data, some branching |
| 141 | 28 | 3 | `pkg/agent/session_messaging_wire.go AgentLoop.wireSessionMessagingForAgent` | Wiring |
| 141 | 33 | 2 | `pkg/sandbox/hardened_exec_linux.go applyPostStartHardening` | Logic/wiring hybrid |
| 137 | 1 | 0 | `pkg/records/knowledgefind/tool.go Parameters` | Data literal (JSON-schema literal) |
| 134 | 39 | 1 | `pkg/agent/loop.go AgentLoop.wirePlanToolsForAgent` | Wiring |
| 134 | 28 | 2 | `pkg/records/filter.go Filter.Validate` | Logic |
| 130 | 34 | 0 | `pkg/providers/oauth_token_source.go NewStoreOAuthTokenSource` | Wiring (constructor) |
| 130 | 31 | 0 | `cmd/omnipus/internal/onboard/onboard.go NewOnboardCommand` | Wiring (cobra command assembly) |
| 130 | 23 | 3 | `pkg/gateway/rest_tasks.go toWireCriteria` | Logic (conversion/mapping) |
| 130 | 34 | 4 | `pkg/gateway/gateway.go buildEnabledRefMap` | Logic |

(20 of the 27 shown; the remaining 7 follow the same pattern of mostly-logic-a-few-data. Full 27-row detail reproducible via `python3 scripts/analyze_funstats.py results/funstats-go-all-d9a0c6941.txt`.)

### Test-set breakdown (heuristic)

Of 20,655 Go test functions: 15,310 are entry points (`Test*`/`Benchmark*`/`Fuzz*`/`Example*`), 2,495 are heuristically-detected helpers (take a `*testing.T/B/F` or call `.Helper()`), and 2,850 are neither (subtests run via `t.Run`, mocks, fixtures). Of the 281 test functions over 120 lines, 271 are entry points and only 10 are helpers — i.e. today's long test functions are almost all `Test*` functions whose bulk is an inline table of cases, not undisciplined helper code. This supports treating `Test*` length as a softer signal than helper length: a helper over budget is doing too much; a `Test*` function over budget is usually just testing a lot of cases in one table.

### Tools and raw data produced this session

- `/Users/danielpiatkowski/AI-Agent-Workspace/loop-split-bench/cmd/funstats/main.go` — new Go AST scanner (lines, statements, nesting depth, test-helper heuristic).
- `/Users/danielpiatkowski/AI-Agent-Workspace/loop-split-bench/scripts/analyze_funstats.py` — percentile/cross-tab analysis over `funstats` output.
- `/Users/danielpiatkowski/AI-Agent-Workspace/loop-split-bench/results/funstats-go-all-d9a0c6941.txt` — full per-function output, Omnipus tree.
- `/Users/danielpiatkowski/AI-Agent-Workspace/loop-split-bench/results/funstats-go-nethttp-stdlib.txt` — same scan against `$(go env GOROOT)/src/net/http`, current Go toolchain (1.26.6 darwin/amd64) shipped via the Go module cache.
