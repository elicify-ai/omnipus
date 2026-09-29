# Adversarial Review: Read boundary consistency — round 2

**Document reviewed**: `docs/internal/specs/read-boundary-consistency-spec.md` (after the round-1 corrections)
**Mode**: Spec
**Round**: 2 of 2 (fixed, final; there is no round 3)
**Review date**: 2026-09-26
**Reviewer**: `architect` running `grill-spec`, on Sonnet — a different model from the spec's author. GitNexus was unavailable, so Read/Grep was used.
**Reviewed head**: 02867d1
**Verdict**: **REVISE** — 0 CRITICAL, 1 MAJOR, 2 MINOR (MIN-006 plus a drift check that found no drift), 3 OBSERVATION
**Questions for the founder**: none. **Escalation**: none.

## Part 1 — Round-1 findings, checked against the spec text and the code

| Finding | Verdict | Evidence |
|---|---|---|
| MAJ-001 (Windows job ambiguous) | Closed | New additive job `windows-tools-tests`. `cross-platform.yml` and `platform-support.md:31` match the spec's correction tables. |
| MAJ-002 (missing deny/on/inside row) | Closed, complete | All 18 cells of policy × Auto × location enumerated (table below). |
| MIN-001 (name asymmetry) | Closed (D11) | `filesystem.go::ListDirTool.Execute` still has no `guardMetadataPath`. |
| MIN-002 (dispatch order) | Closed, no regression | See OBS-005. |
| MIN-003 (`path: ""`) | Closed | S-1.13, DS-1 row 24. |
| MIN-004 (`root_lost`) | Closed, with a residual gap | See MIN-006. |
| MIN-005 (emission point) | Closed | FR-021: the row is written only after `filegrep.Search` returns a nil error. |
| Unasked Q1 (logger wiring) | Closed | `grep.go` has no logger today. The `registry.go` `auditLoggerAware` propagation and `filesystem.go::emitPathAccessDeniedCorrelated` are confirmed. |
| Unasked Q2 (root closed when busy) | Closed | `grep.go::Execute` defers `closeRoots()` before `TryAcquire`, and `resolveScopedRoot` takes `opened`. |
| Unasked Q3 (test reason) | Closed | Re-derived independently, see below. |

Q3 re-derivation. `fspolicy/carveout.go::IsCarveOut`: `workspaces/` is a whole-subtree carve-out, and the own-tree exception applies only inside the caller's own `WorkDir`. So `../../ws-a/work` fails with `ErrCarveOut` (`resolvepath.go`), which `path_audit.go::classifyPathDenialReason` maps to `carve_out`.

The old `../ws-a/work` was also refused, but for the wrong reason: it lands inside ws-b's own subtree. The rewrite adds a real reason assertion, which the test lacked before.

DS-2 grid:

| Policy | off/inside | off/EXT | off/secret | on/inside | on/EXT | on/secret |
|---|---|---|---|---|---|---|
| allow | 1 | 2 | 15 | 14 | 3 | 16 |
| ask | 4 | 5 | 17 | 6 | 7 | 8 |
| deny | 9 | 18 | 20 | 19 | 10 | 21 |

## Part 2 — New findings

### [MAJ-003] Test 32's descriptor count has no cross-platform guard
- **Where**: FR-032, test order row 32 (`TestGrepTool_NewRootClosedOnEveryExit`).
- **Failure scenario**: The `matrix` job in `cross-platform.yml` runs `go test ... ./...` on `ubuntu-latest`, `ubuntu-24.04-arm` and `macos-latest`, and all three are required. `/proc/self/fd` does not exist on macOS, so an unguarded `os.ReadDir("/proc/self/fd")` fails the macOS leg and blocks the release (Hard Constraint #7).
- **Precedent**: `pkg/sandbox/spawn_bg_fd_test.go` uses `//go:build linux` plus a runtime `t.Skipf` when `/proc/self/fd` is unavailable.
- **Certainty**: Verified.
- **Recommendation**: FR-032 / row 32 must require `//go:build linux` or a graceful `t.Skipf`, and must never fail on the macOS or Windows legs.

### [MIN-006] FR-010's "injected seam" names no mechanism
- **Where**: FR-010, S-1.14, test 4a.
- **Evidence**: `grep.go::resolveScopedRoot` calls `container.Stat(subPath)` and then `container.OpenRoot(subPath)`. There is no precedent for a Stat-then-Open race test. The one test-hook precedent is `pkg/tools/task.go::taskGoalEndedHook`.
- **Risk**: A goroutine- or timing-based race test would be flaky.
- **Recommendation**: Name the mechanism: a package-level, test-only func var invoked between the Stat and the OpenRoot (following the `taskGoalEndedHook` pattern). The RED test sets it to remove the directory once, synchronously, with no sleep and no goroutine.

### Observations
- **OBS-005 — FR-001 dispatch (no defect)**: Routing in-workspace paths through ResolvePath and making the realpath relative to the workspace gives clean hit paths (`notes/a.md` for `notes/../notes`), which is an improvement. The ADR-081 D4 amendment requires the skills-gate and metadata-guard wrappers on every root type, so DS-1 row 17 is covered.
- **OBS-006 — busy case (no defect)**: This is deterministic. `grep_test.go::TestGrepTool_BusyReturnsStructuredError` is a direct `TryAcquire`×2 precedent.
- **OBS-007 — doc drift (none)**: `docs/tools.md:59`, `docs/security.md:162,164`, `docs/goals.md:22`, `docs/operations/platform-support.md:31`, `AuditEntry.yaml:17` and `audit.go` `validEventNames` all still match the spec.

## Twelve-lens pass
- Lenses 1, 2, 4, 5, 7 and 12: nothing beyond Part 2.
- Lenses 8, 9 and 10: not applicable, and correctly stated.
- Lenses 3, 6 and 11: produced this round's findings.

Additional feasibility checks:
- `GOOS=windows go list -deps ./pkg/tools/`: 0 hits on `pkg/gateway`, so no SPA stub is needed.
- `GOOS=windows go vet ./pkg/tools/...`: exit 0.
- `seed_system.go`: the Judge has all three tools set to allow. The Plan Supervisor has grep only, with a stale comment citing FR-020.

## Next
Fix round 2 covers MAJ-003 and MIN-006. No third grill round. Then planning, RED/GREEN/CHECK, and the 8-reviewer gate.

Self-check: the reviewer re-read the full corrected spec, the round-1 review, D1–D12, both ADRs, and every symbol file the spec names. Round-1 closure was re-verified against the code, the carve-out reason was re-derived, and two Windows commands were run with their exit codes captured.
