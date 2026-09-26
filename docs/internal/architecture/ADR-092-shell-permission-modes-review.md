# Adversarial Review: Dated amendments to ADR-081 D4 and ADR-092 D9 (issue #920, read-boundary consistency)

**Document reviewed**: commit `85a83ae` — `ADR-081-unified-library-search-and-grep-engine.md` (D4 amendment) and `ADR-092-shell-permission-modes.md` (D9/J2 amendment)
**Mode**: ADR — the one fixed grill round (one report covers both amendments; `ADR-081-unified-library-search-and-grep-engine-review.md` points here)
**Reviewer**: `architect` running `grill-spec`, on a different model (Sonnet) from the amendments' author
**Review date**: 2026-09-26
**Verdict**: **BLOCK** (on CRIT-001 alone)

Squad-lead spot-check of CRIT-001 (2026-09-26): confirmed that `pkg/agent/loop_run_turn_tools.go` pins every Auto-run call whose policy is `ask` (the gate is `ex.autoPin.Run && ex.toctouPolicy == "ask"`, with no class test); that `pkg/tools/auto_approve.go::resolveAutoCheckedPath` re-checks any pin through `RecheckAutoPin`; and that `pkg/tools/filesystem.go` read_file (`FSOpRead`) and list_directory (`FSOpList`) both call `resolveAutoCheckedPath`.

| Severity | Count |
|---|---|
| CRITICAL | 1 |
| MAJOR | 0 |
| MINOR | 4 |
| **Total** | **5** |

## Findings

### [CRIT-001] The stated reason `RecheckAutoPin` "no longer applies" to read_file/list_directory is false; following the amendment as written breaks Auto-approved reads in its own target scenario

- **Lens**: inconsistency with the code, plus incompleteness (a required GREEN step is never stated)
- **Affected section**: ADR-092, the 2026-09-26 revision note, the paragraph "A consequence, not a new gate: an `AutoRuns` call is never pinned..."
- **Failure scenario**: An operator sets `read_file` to `ask` and turns Auto-approve on. `read_file` is now `AutoRuns`. The agent calls `read_file` on any path, including one inside the workspace. The call hard-fails with an Auto-pin-moved error instead of running.
- **Evidence**:
  - `pkg/agent/loop_run_turn_tools.go` pins every call that Auto ran with policy `ask` (`tools.WithAutoApproved(execCtx, tools.AutoPinForVerdict(...))`). The gate is `ex.autoPin.Run && ex.toctouPolicy == "ask"`, and it never tests the tool's class.
  - `pkg/tools/auto_approve.go::ClassifyAutoApprove` — `case AutoRuns:` returns `AutoVerdict{Run: true, Class: AutoVerdictClassRuns}` and leaves `Paths` nil. An `AutoRuns` call is therefore pinned, with an empty path list.
  - `pkg/tools/filesystem.go::ReadFileTool.Execute` and `ListDirTool.Execute` still call `pkg/tools/auto_approve.go::resolveAutoCheckedPath`. That function checks only for the presence of any pin and then always calls `RecheckAutoPin`.
  - `RecheckAutoPin` works out an access value from `pin.Paths`, which is 0 when the list is empty. `PathGrantAccessRead` (1<<0) is not covered by that, so the call fails every time.
  - The defect is latent today only because `pkg/config/defaults.go` ships `read_file` and `list_directory` as `allow`.
  - The amendment's "Affected components" list names only `auto_approve.go` (the classification table) and the golden-copy test. It never names `filesystem.go`.
- **Recommendation**: Correct the mechanism claim and name the exact file and function GREEN must change. There are two options:
  - **(A) Narrow:** read_file and list_directory resolve through `ResolvePathAllowingPatterns` directly, bypassing `resolveAutoCheckedPath`.
  - **(B) General:** `RecheckAutoPin` / `resolveAutoCheckedPath` does nothing for a pin whose class is `AutoVerdictClassRuns`.
  - Choosing between them is a founder question.

### [MIN-001] "`resolveScopedRoot` already implements exactly this... reuse it" overstates how directly it can be reused
- **Affected section**: ADR-081 D4 amendment, the design for walking an absolute `path`, step 3.
- **Evidence**: `pkg/tools/grep.go::resolveScopedRoot` needs an already-open `container` and a `subPath` relative to it (it calls `container.Stat(subPath)` and then `container.OpenRoot(subPath)`).
- **Recommendation**: State the call shape. Open `os.OpenRoot(filepath.Dir(realAbs))` as the container, then call `resolveScopedRoot` with `filepath.Base(realAbs)` as `subPath`.

### [MIN-002] D11's grep policy recommendation still calls the tool "confined" without a correction pointer
- **Affected section**: ADR-081 D11, "recommended: `allow` — read-only, confined".
- **Recommendation**: Mark the sentence as amended on 2026-09-26 (#920). "Confined" now describes only the default when no `path` is given.

### [MIN-003] Per-visited-file use of the two-spelling `classifySkillsGate` / `isSkillInstructionFile`
- **Evidence**: `pkg/tools/resolvepath.go::classifySkillsGate` checks two spellings of a path, as written and fully resolved, to defend against a symlinked ancestor of a single caller-supplied path. Inside an `os.Root` walk, which already refuses symlinks, the two spellings coincide. Over-inclusive is safe, but it isn't precise.
- **Recommendation**: Either confirm this is deliberate belt-and-braces, or point GREEN at the single-spelling primitives `classifySkillsGateCandidate` / `isSkillInstructionFileLeaf`.

### [MIN-004] The named verification (golden-copy classification test) cannot catch a defect shaped like CRIT-001
- **Evidence**: `pkg/gateway/auto_approve_classification_test.go` locks only the shape of the classification table.
- **Recommendation**: The ADR should require an assertion that the call actually runs: with policy `ask` and Auto on, read_file and list_directory succeed, both inside and outside the workspace. That assertion belongs in the RED parity matrix.

## Verification of the four claims the earlier worker flagged

| # | Claim | Verdict | Evidence |
|---|---|---|---|
| 1 | The `ReadConfined` branch has no mount exception | Verified | `pkg/tools/resolvepath.go::resolveValidatedPath`: the `FSOpRead, FSOpList, FSOpSend` branch refuses outright with no `matchedAllowedRoot` call. The `FSOpWrite, FSOpServe` branch does call it. |
| 2 | `grep.go` has no audit logging | Verified | 0 matches in `grep.go` for `auditLogger`, `SetAuditLogger` or the emit functions. |
| 3 | The only Auto check for read_file/list_directory is the location check | Verified | `filesystem.go::ReadFileTool.AutoApproveVerdict` and `ListDirTool.AutoApproveVerdict` both just delegate to `autoWorkspaceVerdict`. |
| 4 | The skills gate and metadata guard live only in ResolvePath/filesystem.go | Verified | These names appear only in `resolvepath.go`, `filesystem.go` and `metadata_guard.go`, and 0 times in `grep.go`. |

The must-close list for the widened grep is complete and accurate in the ADR text:
- the skills-registry gate
- the metadata guard
- symlink containment (`pkg/filegrep/filegrep.go` skips non-regular entries, and `os.Root` cannot traverse a symlink)
- refusal and searched-root audit rows
- Windows absolute paths (`validateGrepScope`)

`pkg/gateway/rest_library_files_search.go` is untouched.

## STRIDE summary
- **Widened grep:** information-disclosure risk is accepted and tracked (D4 / #921). Every other category is fine.
- **Reclassification of read_file/list_directory:** an availability defect (CRIT-001).
- **ReadConfined mount fix (D6):** narrows to `AllowedRoots`. Needs a security-lead review.

## Unasked questions for the author
1. The widened `ReadConfined` read branch: does it return an `os.Root` anchored at the matched mount (as write/serve do), or a host-filesystem handle?
2. `send_file` stays `AutoRunsIfArgs`. Spot-check that it is unaffected.

## Questions for the founder
1. **pin-mechanism-fix-scope**: the fix for CRIT-001 — (A) narrow, or (B) general. The reviewer recommends (B).
2. **read-confined-fix-bundling**: the D6 ReadConfined fix — (A) bundle it into the #920 PR, or (B) split it and land it first. The reviewer recommends (B).

## Lens notes
- **Infeasibility:** none found.
- **Contract-first, UI, accessibility, design system:** not applicable. The change is backend-only and adds no new wire type.
- **Overcomplexity:** none found.

## Evidence (condensed)
All findings trace to a `file::symbol` read in the review session. Covered:
- `loop_run_turn_tools.go`
- `auto_approve.go` (`ClassifyAutoApprove`, `resolveAutoCheckedPath`, `RecheckAutoPin`)
- `filesystem.go`
- `fspolicy/policy.go`
- `config/defaults.go`
- `grep.go` (`resolveScopedRoot`, `validateGrepScope`)
- `resolvepath.go` (`resolveValidatedPath`, `classifySkillsGate`)
- `filegrep/filegrep.go`
- `rest_library_files_search.go`
- ADR-063 D2, ADR-072 D10.3, ADR-090

Self-check: both diffs re-read in full, the Auto-approve call chain traced end to end, and it was confirmed that no earlier review file existed.
