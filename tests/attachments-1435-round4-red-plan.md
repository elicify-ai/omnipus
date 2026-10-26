# Issue #1435 — Round 4 RED plan

Base `ee03fefe80194a1a794fab9cfdfb5491c9d2d859` on `fix/1435-attachments` (pulled, clean). Same sole test author. Test files only; rounds 1–3 tests and expected values untouched.

## Oracles (fixed before assertions)

Sources: chief round-4 items 1–4; round-3 reviews (SFH R2-SF-3, PRT N1/F4, architect F4/F5); the #1435 requirements (accepted attachments are reported honestly and separately; a notice never becomes the reply; budgets are per provider; attachments live as long as their conversation). Frame shapes come from real gateway output only.

| Item | Expected behaviour (oracle) | Real boundary |
|---|---|---|
| 1a — replayed notice vs earlier entry | A persisted `media_unavailable` notice delivered as `replay_error` is its OWN entry with the generated catalogue wording; the earlier empty truncated assistant entry keeps exactly its prior content/status and gains no errorCode | Go producer: real loop+store persist a truncated-empty assistant entry, then a real turn with a missing ref persists the notice; a second WS attach captures the real replay window (`replay_message` ×2, `replay_error`, `catch_up_complete`) into a paired fixture; SPA test feeds the real frames through `parseFrameSafe` + the generated `ClientFrameTypes` direction filter into the real reducer |
| 1b — replayed notice vs running reply | While a reply streams, the replayed notice leaves identity/content/isStreaming/active-turn untouched; later real tokens still reach the same reply and only the real done ends it | Same fixture; live continuation frames (`token`, `token`, `done`) captured by the same producer after the provider resumes (seq-ascending hybrid, so the SPA seq gate admits them) |
| 2 — deleted workspace-library attachment | Missing workspace-library entry (`library.ErrNotFound` forwarded by `resolveWorkspaceRef`) produces the SAME typed notice once per ref per turn plus the content marker; a library mismatch error (`ErrWorkspaceMismatch`) must produce NO notice | Real workspace `Library` (`UploadFixture`, `Delete`) wired through `SetWorkspaceLibraryProvider`; real `assembleInitialContext` site on the wu22 harness; real media store; no notifier calls |
| 3 — patch-limit-only cache difference | Same bytes/model/bytes/edge; loose ceiling → tight ceiling → tight again: the tight result must be respected both times and tight reuse must be safe (identical bytes) | Real `encodeImageToDataURLCached` + its real normalize cache; 64×64 PNG (4 patches) so the tight 1-patch ceiling must actually change the output |
| 4 — retention with cleaner disabled | After the real `RetentionSweep` removes a session and cascades its upload files, the pinned media mapping is released on the live store (resolve → `ErrNotFound`); no reload may be needed | Real upload endpoint, real UnifiedStore retention sweep, media store constructed WITHOUT the cleaner; assertion on the live store only |

## Expected colours (honest)

Items 1a, 1b, 2, 4 are expected RED at `ee03fefe8` for the stated defects. Item 3 is expected GREEN at base: the production cache key already carries `BudgetMaxPatches` (`loop_media.go` cacheKey includes it), so this is the PRT F4 coverage pin — it kills the "drop patches from the key" mutant. Its colour is reported as observed, never manufactured.

## RED execution

Foreground `slotrun.sh`, one anchored `-run` per test, one package, `CGO_ENABLED=0`, `goolm,stdjson`, `-count=1 -v -p 1`, Go 1.27.3, load ≤20 gate before each start; vitest single file. Saved logs + raw exit codes under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/omnipus-l1/logs/t4-1435/r4`. GREEN, mutations, and the Proof-of-failability checklist are deferred to independent CHECK.

## Deliberate gaps

No rendered-browser replay (store/reducer level, as round 3); no `ErrEntryStranded` case (distinct defect class, not named by the chief); cache pin does not cover eviction (existing LRU tests own that); retention fix wiring shape (hook vs direct) is the developer's choice — the test pins the observable only.
