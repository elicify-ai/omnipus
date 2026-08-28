# Wave 0 report — ADR-066 / 067 / 068 (2026-08-23)

Branch `feat/context-budget-and-tool-result-routing`, worktree
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-context-budget`.
Wave 0 = everything that had to exist before the first implementation commit (A-CONTRACT / T067-01).

## What landed (all committed, author + committer = Daniel's GitHub no-reply identity, no Co-Authored-By trailers)

| Commit | Content |
|---|---|
| `ed449a2b` | `docs/internal/specs/tasks/adr-067-registry-catalog-spec-tasks.md` — 14 tasks T067-01..14 (T067-01 = A-CONTRACT, T067-08 = atomic factory collapse) |
| `ce6459c9` | `docs/internal/specs/tasks/adr-068-providers-ux-spec-tasks.md` — 31 tasks T068-01..31 |
| `ac1d1180` | `docs/internal/specs/tasks/adr-066-context-overflow-spec-tasks.md` — 18 tasks T066-01..18 |
| `d30d8113` | `docs/internal/specs/tasks/baseline-2026-08-23.md` — CI baseline measured at `dc9bc96a` |
| `e62c7bed` | ADR-066/067/068 status lines flipped Proposed → `Accepted (operator approval 2026-08-23 — implementation plan approved)` |
| (this commit) | this report |

Assembly repository (ADR-067 B2 scaffold): **https://github.com/elicify-ai/omnipus-provider-catalog** (private; one commit on `main`: README, MIT LICENSE, `docs/schema-2.0.0.md`, `overrides/README.md`, `resize_limits.json`, skeleton `.github/workflows/assemble.yml` with 8 TODO steps — no release published yet). Local clone: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-provider-catalog`.

## Consolidation checks (performed by the Wave 0 integrator)

- **(a) Files present and committed** — verified by `git log --oneline -8`; tree clean before the ADR flip.
- **(b) FR ids** — checked exhaustively, not a 10-per-file sample: every `FR-nnn` cited in each task file exists in its spec (066: 49/49, 067: 40/40, 068: 45/45; zero ids missing).
- **(c) Cross-spec dependencies** — no task file cites a concrete foreign `T0xx-nn` id; all cross-spec edges use named placeholders, and every placeholder resolves to an existing task:

| Placeholder | Resolves to |
|---|---|
| `A-CONTRACT` | T067-01 |
| `T067-RESOLVER` | T067-02 (`catalog.Resolve`, locality) |
| `T067-CATALOG-GET` | T067-10 |
| `T067-ENTITLEMENT` | T067-11 |
| `T067-NEEDS-PROVIDER` | T067-09 |
| `T067-FACTORY` | T067-08 |
| `B2-RELEASE` | T067-05 (first real release of the assembly repo) |
| `T066-SETTINGS-CONTEXT` | T066-17 |
| `T066-RESOLVE-WINDOW` | T066-09 |
| `B5-SPA` | T068-18..T068-31 (D9 screens are T068-29 / T068-30) |

Implementers should substitute the concrete ids when editing a task file; the placeholders are consistent as they stand.

## Baseline at `dc9bc96a` (measurement only, nothing fixed)

GitHub `pr.yml` run 32610977723: **FAILURE** — every Node-dependent job died at `npm ci` (ERESOLVE: eslint 10.8.1 pinned by `df25c111` vs eslint-plugin-jsx-a11y 6.10.2 peer range ^3..^9). GitHub produced no Go/lint/race/contracts/vitest/e2e signal. Green there: #615 CI guard, #615 real-Chrome, CLI removed-verb guard, e2e shard plan, tool-error-from-status lint, wire-types lint.

Fly `ci-omnipus` `runci.sh dc9bc96a all` (exit codes from runci's own `-> exit N` lines):

| Gate | Result | Detail |
|---|---|---|
| cli-verb-guard | GREEN | |
| npm-ci | RED | same ERESOLVE |
| gofmt / go-build / go-vet | GREEN | |
| golangci-lint | RED | 12 gosec G115: `pkg/sandbox/sandbox_linux.go` :280,293,317,343,537,561,582,697,731,740,759 and `pkg/sandbox/seccomp_linux.go:334` (existing `#nosec G115` not honoured) |
| verify-contracts | GREEN | |
| typecheck | GREEN | against the worker's cached node_modules |
| vitest | GREEN | 428 files, 7007 passed, 2 expected-fail; cached node_modules |
| go-test | RED | real failure: `pkg/config TestNoAgentConfigWorkspaceIdentifier` (`rename_guard_test.go:137`) flags `.Workspace` use in `pkg/migrate/sources/openclaw/openclaw_config.go` :374,377,416,862,863,889,916 |
| go-race | RED | same test, 0 DATA RACE lines |
| e2e | RED | 8/9 shards pass; `llm-conformance` fixture error at `tests/e2e/fixtures/plan-cleanup.ts:72` ("First argument must use the object destructuring pattern") — spec never ran |

Per Constraint #7 all four RED causes are ours to fix before any PR, regardless of origin.

## Blockers

1. **CLA gate**: branch history inherits 7 `Co-Authored-By: Claude <…@anthropic.com>` trailers from `origin/release/v0.1.1` commits (2e14cf25, 03d27695, df25c111, 45b01b14, b3d31878, ba5d34f6, c7f60ef0, 487c75c5). Any PR to `main` will fail CLA Assistant — needs a human decision on a history rewrite. The 66 commits this branch added carry none.
2. **npm ci ERESOLVE** hides all Node-dependent GitHub jobs until the eslint pin is reconciled.
3. **Schedule hard edge**: T067-06/07 cannot build until the assembly repo publishes a real first release (FR-006 forbids a fixture as the embedded snapshot); the repo currently has only a skeleton workflow.
4. **GitNexus index for this worktree is NOT usable**: `gitnexus analyze` was started (753 MB written to `.gitnexus/`) but the process is no longer running and `~/.gitnexus/registry.json` still lists only the `wt-library-improvements` checkout. Whether it finished or was killed is unknown. It also skipped `pkg/agent/loop.go` (over the 512 KB cap), so `windowTrim` would be missing anyway. Re-run `GITNEXUS_MAX_FILE_SIZE=2048 gitnexus analyze --skills --skip-agents-md` from the worktree before any `impact` on loop.go symbols (T066-05/12/13, T067-08).

## Unverified

- Final GitNexus symbol counts, registry entry, and the `windowTrim` smoke query (see blocker 4).
- Fly typecheck/vitest GREEN used the worker's cached node_modules, not a clean install.
- Whether the ignored `#nosec G115` at `seccomp_linux.go:334` is a golangci-lint version difference between the Fly worker and GitHub.
- Which Go call sites break under A-CONTRACT's generated-type changes (known only after `make gen-contracts`).
- `resize_limits.json` values and the schema-2.0.0 example in the assembly repo come from the Omnipus seed / are illustrative; not re-checked against vendor docs. `assemble.yml` was not executed or actionlint-validated.
- Agent-form override (T068-30) ownership between S66 and S68 was inferred from ADR-066 D9 and ADR-068 §4, not confirmed by the operator.
- Commits `d30d8113` and later are local to the worktree; only up to `dc9bc96a` is on `origin`.
