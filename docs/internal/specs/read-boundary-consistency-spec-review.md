# Adversarial Review: Read boundary consistency — search, read and list follow one rule

**Document reviewed**: `docs/internal/specs/read-boundary-consistency-spec.md`, plus its dated pointers in `unified-search-and-grep-spec.md`
**Mode**: Spec
**Round**: 1 of 2 (fixed)
**Review date**: 2026-09-26
**Reviewer**: `architect` running `grill-spec`, on Sonnet — a different model from the spec's author. GitNexus was unavailable, so Read/Grep was used.
**Reviewed head**: 2136001
**Verdict**: **REVISE**

Squad-lead spot-check of MAJ-001 (2026-09-26): in `.github/workflows/cross-platform.yml`, `windows-compile` runs on `ubuntu-latest` and `windows-daemon-tests` runs on `windows-latest`. Confirmed.

| Severity | Count |
|---|---|
| CRITICAL | 0 |
| MAJOR | 2 |
| MINOR | 5 |
| OBSERVATION | 4 |

## Findings

### [MAJ-001] FR-031's "Windows job" is ambiguous; one of the two Windows jobs cannot run tests at all

- **Where**: FR-031, test order row 28, SC-004.
- **Failure scenario**: `cross-platform.yml` has two Windows-named jobs.
  - `windows-compile` runs on `ubuntu-latest` and only cross-compiles. A `_windows_test.go` file is excluded there by its filename build constraint, so `go test -run '^TestReadBoundary_Windows' ./pkg/tools/` reports "no tests to run" and exits 0 — a false green.
  - `windows-daemon-tests` runs on `windows-latest`, but its name and the workflow header comment scope it to `pkg/daemon`.
- **Evidence**:
  - The header of `cross-platform.yml`, and the two job bodies.
  - `GOOS=windows go vet -tags goolm,stdjson ./pkg/tools/...` exits 0, so the package builds for Windows. The risk is which job the step lands in.
- **Recommendation**: Name the exact job in FR-031. Either rename `windows-daemon-tests` to `windows-tests` and add a `pkg/tools` step, or add a new `windows-tools-tests` job on `windows-latest`. Also require the workflow's header comment to be updated in the same commit, and add that comment to the spec's documents-to-correct list.

### [MAJ-002] DS-2 never tests deny with Auto on for a location inside the workspace

- **Where**: DS-2, FR-019 ("Deny MUST never be overridden").
- **Failure scenario**: A D8 short-circuit that wrongly skips the policy check, instead of only the re-check, would slip through. Every tested deny row either has Auto off or is outside the workspace.
- **Recommendation**: Add the DS-2 row `| deny | on | inside <WS> | refused, no card | S-3.1 |`.

### [MIN-001] Ambiguity Warning #1: the name-visibility asymmetry is a product decision

- grep hides metadata files and registry skill instruction files by name, but `list_directory` of the same folder still lists them. The tools disagree again, this time on a different axis.
- Verified: `pkg/tools/filesystem.go::ListDirTool.Execute` never calls `guardMetadataPath`.
- **Recommendation**: Take it to the founder (Q1).

### [MIN-002] Dispatch between the existing lexical scope path and the new ResolvePath-based walker is unspecified

- `pkg/tools/grep.go::grepRoots` opens the workspace-relative scope via `os.OpenRoot(WorkDir)` and then `resolveScopedRoot`. `os.Root` handles a `..` that stays inside the root, but refuses an escaping `..`.
- The spec should state which algorithm GREEN uses: try the lexical path first and fall through to the new mechanism on an escape error or an absolute scope, or send every non-shorthand scope uniformly through ResolvePath.
- Either reading gives the same security outcome, so this is a build ambiguity, not a security gap.

### [MIN-003] No case distinguishes `path: ""` from an omitted `path`
- **Recommendation**: Add a row or scenario asserting that the two give identical results.

### [MIN-004] No test asserts `root_lost` honesty for the new absolute-path root type
- **Recommendation**: Add a case where the `<EXT>` root is removed after resolution but before the walk starts. The result must be `truncated` + `root_lost`, not a hard error.

### [MIN-005] The emission point in Ambiguity Warning #3 is implicit
- `GrepTool.Execute` runs `grepRoots`, then `filegrep.TryAcquire`, then `filegrep.Search`.
- **Recommendation**: FR-021 should say the roots-searched emitter fires only after `filegrep.Search` returns without error.

### Observations
- **OBS-001:** infeasibility — none found.
- **OBS-002:** accessibility — not applicable, correctly stated.
- **OBS-003:** design system — not applicable, correctly stated.
- **OBS-004:** overcomplexity — none found. The fresh `os.OpenRoot` at the resolved parent is justified.

## Structural integrity
Every check passes:
- the `Status:` header
- the ADR links
- contracts ("None", verified against `AuditEntry.yaml` and `ToolRegistryEntry.yaml`)
- API and data
- UI states
- the user journey
- accessibility
- design system
- security (the doc excerpts match byte for byte)
- BDD oracles
- traceability (cross-checked in both directions and complete)
- reachability

## STRIDE
- **Widened grep:** information-disclosure risk is accepted (D4 / #921).
- **D6 mount fix:** fine, with the SL-1..SL-4 checks.
- **Reclassification:** fine. The D8 chain was re-traced.
- **Audit row:** fine; it carries no search term or content.
- **Plan Supervisor (D10):** accepted risk, stated plainly.

## Reachability
- **Registered with policy:** `defaults.go`, `role_policies_adr090.go` and `seed_system.go` were verified.
- **UI:** `AuditLogViewer.tsx` and `ToolPolicyEditor.tsx`.
- **Test plan:** it describes execution.

## Unasked questions for the author
1. Does GrepTool need a `SetAuditLogger`-style field? If so, name it in FR-020/FR-021.
2. Is the new root handle closed by the existing `closeAll` when the search is later refused, for example as busy?
3. Does the rewritten cross-workspace half of `TestGrepTool_OwnWorkspaceOnly` still assert reason `carve_out`?

## Questions for the founder
1. **grep-list-directory-name-asymmetry**: (A) accept the asymmetry as out of scope for #920, or (B) apply the name filter to list_directory as well. The reviewer recommends (A).
2. **windows-ci-scope-acceptance**: (A) the scoped Windows step for the named tests is sufficient, or (B) require a broader Windows smoke pass first. The reviewer recommends (A).

## Evidence (condensed)
Checked against the code at 2136001:
- `grep.go::validateGrepScope`, `grepRoots`, `Execute`
- `resolvepath.go::resolveValidatedPath`, the skills-gate primitives, and the stale comment on `WithReadConfined`
- `metadata_guard.go::metadataFileMatch`
- `auto_approve.go` (no `Class` field yet)
- `audit.go::validEventNames`
- `AuditEntry.yaml`, `ToolRegistryEntry.yaml`
- `rest_library_files_search.go`
- the `WithReadConfined` callers (one production call)
- `seed_system.go`, and `docs/tools.md` / `docs/security.md` for the excerpts
- `cross-platform.yml`
- `GOOS=windows go vet`: exit 0

Self-check: re-read the full spec, the interview, both ADRs and the ADR review. Re-ran the lenses, the structure checks, traceability and reachability. Corrected one false alarm of the reviewer's own (a second `WithReadConfined` hit passes `false`).
