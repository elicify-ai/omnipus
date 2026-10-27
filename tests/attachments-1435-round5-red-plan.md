# Issue #1435 — Round 5 RED plan

Base `9a340568eaf7e73e1cdaeef7e01a6b8e1816adf7` on `fix/1435-attachments` (pulled, clean). Same sole test author. Test files only; rounds 1–4 tests and expected values untouched.

## Sources read for this round

`1435-r4-review-1435-gate-sfh-r4.md` (R4-SF-1 HIGH, R4-SF-2 HIGH), `1435-r4-review-1435-check-r4.md` (survivors M3 and M4, held-out probes), `1435-r4-review-1435-docs-r4.md` (no new user-visible behaviour in this round), the PRT gate result (`1435-gate-prt-r4.stream.jsonl` → R4-T1) and the architect gate result (`1435-gate-arch-r4.stream.jsonl` → F5 PARTIAL).

## Oracles (fixed before assertions)

| Item | Expected behaviour (oracle) | Real boundary | Source of the expectation |
|---|---|---|---|
| 1 — retention releases the session's own mappings | When retention removes a conversation, that conversation's media mappings are released **by the removal itself** — observed with a non-mutating read of the store's index (no `Resolve`, no cleaner, no reload) before anything can repair the state. Both session scopes count: the accepted upload (`SessionUploadScopePrefix`) and the tool-generated inline media (`SessionInlineScopePrefix`). Repeated retention cycles must not accumulate dead mappings, and `SaveRegistry` must not re-serialize them | Real REST upload endpoint, real `UnifiedStore.RetentionSweep`, real media store constructed WITHOUT the cleaner configuration, real `RefByPath` index read, real `registry.json` on disk | SFH R4-SF-1 (retention never releases; the round-4 test's `Resolve` performed the cleanup it inspected), PRT R4-T1 ("assert that `store.RefByPath(path)` returns no ref" **before** any `Resolve`), ARCH F5 ("QA should inspect the index before resolving the deleted attachments and include an attachment that remains unqueried") |
| 2 — a stat error other than not-found is surfaced | A verification failure that is **not** a confirmed absence must not be reported as a successful resolution. The caller must receive an error it can classify: distinct from the routine "attachment unavailable" cause (`media.ErrNotFound`) and not masquerading as a confirmed deletion. The pin must survive the fault — the bytes are still on disk | Real `FileMediaStore.resolveLegacyWithMeta`, a real "not a directory" filesystem fault (the parent directory replaced by a regular file) | SFH R4-SF-2 ("the resolver detects that failure, discards it without logging or propagation, and returns success"; the recommended shape returns the wrapped stat error) |
| 3 — guard: the pin is released ONLY on a confirmed absence | A temporary storage fault must leave the pinned mapping indexed and resolvable once storage recovers; a genuinely missing file must release the mapping. Two-sided: keep on other errors, release on `fs.ErrNotExist` | Real store, real fault, real `RefByPath` + `Resolve` | The independent CHECK's survivor M4 ("Dead pin released on ANY stat error" survived the whole committed suite). The round-3 regression restored access **before** resolving, so it never exercised the resolver's fault branch |
| 4 — guard: a library workspace mismatch is not "unavailable" | A workspace mismatch is a security refusal with a different remedy, never a lost attachment: it must produce **no** typed notice (live) and **no** persisted notice (replay). It must be the **library's own** `ErrWorkspaceMismatch`, not the store's `ErrCrossWorkspaceRef` | Real `Library` + real `SetWorkspaceLibraryProvider`, the real agent turn (`assembleInitialContext`) with the notifier wired, real media store | The independent CHECK's survivor M3 (the committed round-4 control reaches the STORE's cross-workspace guard, so a mutant notifying on the library sentinel survives). The library's own guard is `library.go::authorizeWorkspaceRef` |

## Expected colours (honest, stated before the runs)

Items 1 and 2 are expected **RED** at `9a340568e` for the stated defects. Items 3 and 4 are **guards**: production is correct on both today (the CHECK proved it with held-out probes), so they are expected **GREEN** at base and must instead **kill the named mutant** (M4, M3). Their colour is reported as observed, never manufactured.

## RED / mutant execution

Foreground `slotrun.sh`, one anchored `-run` per test, one package per run, `CGO_ENABLED=0`, `goolm,stdjson`, `-count=1 -v -p 1`, Go 1.27.3, load ≤20 gate before each start. The two mutant probes run in a **scratch git worktree outside the repository** (`/tmp/mut-1435-r5`, detached at the round-5 commit); the feature worktree is never mutated, and the scratch worktree is removed afterwards. Saved logs + raw exit codes under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/omnipus-l1/logs/t4-1435/r5`.

GREEN, the remaining Proof-of-failability checklist and the test-integrity audit remain deferred to independent CHECK.

## Deliberate gaps

- No permission-denied case: on a root-run CI machine `chmod 000` does not deny access, so the "not a directory" fault is used as the deterministic non-absence error. The requirement is about *any* non-`fs.ErrNotExist` stat failure, not about one specific errno.
- Item 1 asserts the observable (the store's index and the persisted registry), not a particular release mechanism. A hook, a direct call or a reconciliation pass all satisfy it; the developer owns that choice.
- Item 4 asserts the absence of the typed notice. The model-facing marker text for a refusal is not specified by the issue and is deliberately not asserted.
- No rendered-browser or UAT coverage in this round: all four items are store/agent-level behaviours with no new user-facing surface.
