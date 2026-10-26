# Issue #1435 — Round 3 RED plan

Base: `a7b5e284d78c11bb57520d4e61ed02048484eca9`, pulled with `--ff-only` on `fix/1435-attachments`. One continuing test author. Add new tests/fixtures only; every existing test remains unchanged.

## Oracles fixed before assertions

Acceptance sources are chief Round 3 items 1–5 / founder Q106=B and issue #1435's conversation-lifetime and useful-image requirements. Read all five /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/1435-gate-*-r2.md reports, /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/1435-check-r2.md and /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/1435-docs-r2.md. Their required properties are the expected values; code reads select the real seams, never the expected outcome.

| Item | Independent expected property | Cases / real boundary |
|---|---|---|
| 1: informational notice | `media_unavailable` leaves the same running turn/streaming reply intact; queued input is not drained; tokens continue; only the genuine done ends it | Real accepted/missing ref in a real loop and gateway WebSocket, provider held open, actual emitted frames paired with the real SPA parseFrameSafe + useChatStore.handleFrame. The exported frame fixture is INPUT, not the expected browser-state oracle. Controls: genuine done ends the running state. |
| 2: owner-read error | Only confirmed missing owner permits orphan-expiry. A metadata parse or filesystem read error must visibly refuse upload or preserve `forget_only`, never authorize accepted-file deletion | Real gateway upload, real owning UnifiedStore with evicted metadata cache; corrupt stats JSON and stats path replaced by a directory. Assert the actual non-not-found error, then restore metadata and drive aged real-store cleanup if accepted. Controls: readable owner stays pinned; confirmed absent owner may expire. |
| 3: temporary stat failure | A pinned mapping survives any non-not-found access/storage failure; the same ref and identical bytes resolve after recovery. Confirmed file absence may release the mapping | Real FileMediaStore and real owning conversation. Deterministic portable storage fault: move its file directory aside and temporarily put a regular file at that directory path, producing a real non-not-found os.Stat error without privilege-dependent chmod. Restore it before checking ref/bytes. Both upload and inline scopes; healthy and genuine-deletion controls. Clock uses the existing store test seam, not sleeps. |
| 4: unknown patch accounting | Omitting optional max_patches does not disable useful bounded downscaling; the full-page 2560x15424 image is retained and fully decodable within the byte/edge constraints actually supplied | Real catalog with absent patch fields, real tool result admission/checkpoint and actual provider send; compare to a small-image control. No invented patch cap for unknown accounting. |
| 5: cross-site notice dedupe | Initial plus steering resolution of the SAME missing ref in ONE turn produces exactly one live and one persisted notice; a distinct missing ref still produces its own notice | Real loop preparation and beginIteration resolution sites, actual real store and archive; never manually call notifier or replace the store. Assert the steering input was actually resolved/admitted, not skipped. |

## Test stack and isolation

Go testing/testify and one-file Vitest, matching existing tests. Only paid provider/network edges are substitutes. Media/session stores, upload handler, real loop resolution callers, gateway serialization and SPA reducer remain real. All files and faults are per-test temporary paths; no live-instance data, metadata or permissions are touched. No time threshold, parser error, empty filter or log-only warning counts as acceptance proof.

For item 1, normalize only opaque session/turn/message/time identifiers if pairing producer inputs with a committed SPA fixture; leave type, code, payload and lifecycle fields untouched. The SPA's running-state assertions come from the informational-notice requirement, not observed current behavior. Wire frames are parsed through generated validation.

Item 5 is explicitly a CHECK survivor/coverage gap: the current dedupe map already exists. Its baseline may legitimately PASS, in which case report that rather than manufacture RED. Author-side production mutation is forbidden; the independent CHECK instance must rerun its dedupe mutant with this regression. Do not mislabel a different missing-notice failure as dedupe RED.

## Execution and impact

Foreground only, explicit 600000-ms timeouts, shared /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/omnipus-l1/slotrun.sh; serial one anchored Go test / one package or one Vitest file. Go 1.27.3, CGO_ENABLED=0, goolm,stdjson, -p 1 -count=1 -v. Hold before starts at load >20. Save exact commands, source hashes, base SHA, raw exit and named assertion output.

GitNexus query/impact attempted; stale sibling index requires source/caller fallback. Add new test symbols only, no existing production/test symbol edits. Pre-commit detect_changes plus actual diff inspection. Inferred blast radius: only media, agent, gateway and SPA test execution.

Evidence: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/omnipus-l1/logs/t4-1435/r3

GREEN certification, implementation mutations and the Proof-of-failability checklist are **deferred to independent CHECK**. No full local suite. A baseline passing coverage case is not evidence a mutant dies.

## Deliberate limits

No platform-wide permission certification or peak-memory claim. Portable real filesystem read/stat failures cover failure-vs-absence semantics without root/Windows chmod exceptions. Browser state is exercised with the real SPA store/reducer and actual gateway inputs, not a rendered full browser UAT; exact-candidate UAT remains separate. Uncommissioned workspace-library error-family, disabled-cleaner retention and patch-only cache-key suggestions remain reported review items, not silently expanded work. Documentation updates belong to the implementing lead.
