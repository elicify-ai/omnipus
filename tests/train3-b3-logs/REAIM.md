# Train 3 / bundle B3 — SPA approval-test re-aim ledger (stream C)

**Supersession:** Train 3, amendment section 1 / bundle B3 — `SessionStatePendingApproval`
now REQUIRES `tool_call_id`, `turn_id` and `args`; a reload/reconnect restores the FULL
approvable card (the pre-B3 Deny-only stub is retired); `INLINE_TOOL_APPROVALS_ENABLED`
ships ON (card inline in its root chat, not the app-wide `ToolApprovalModal`).
**Founder ruling 2026-10-18:** the card shows in the parent chat; attention mechanisms
and the main agent's sidebar entry trigger; NO CrossWorkspaceApprovalBanner /
"N approvals waiting — Go there" notice ("we must think about notification beyond that
but not now"). Every re-aimed file carries this note.

## Before → after (case level)

| File | Before (pre-B3 pin) | After (B3 pin) |
|---|---|---|
| `src/store/toolApproval.test.ts` | "adds a stub entry for a pending_approvals id absent from the local queue" (`toolCallId ''`, `turnId ''`, `args {}`) | "restores the full entry (toolCallId, turnId, args, segments) from the snapshot…" — real identity asserted; segments covered |
| | "adds stubs for multiple unseen approvals in the same frame" | "restores multiple unseen approvals in the same frame" (literals already B3-complete) |
| | "L1 R6 / K7 … thin snapshot hydration without deleting the B3 stub guard" (`''`/`{}` sentinels) | "optional root/name/origin survive snapshot hydration alongside the B3-required request identity" — thin literal upgraded to the required fields, identity asserted |
| | "handles a mix in one frame: refresh … AND stub a newly-reported one" | "…AND restore a newly-reported one" — restored entry asserts its real `toolCallId`/`turnId`/`args`; mix literals completed |
| `src/store/toolApproval.k7.test.ts` | "a snapshot without the details still yields the honest stub (until bundle B3)" | "a snapshot missing the required details is a contract violation — Zod rejects it at the boundary": positive control (complete frame parses) + violation dropped (`parseFrameSafe` → null; the generated schema requires the fields, `min(1)`) |
| | "a live frame upgrades a stub to the full request" (thin literal) | "a live frame for an already-restored approval keeps exactly one full entry" (literal completed; one-entry + full-identity assertions) |
| `src/store/toolApproval.resolution.test.ts` | "dequeue alone is only a local dismissal … restores it" asserting `toolCallId ''` | same scenario asserting the RESTORED identity (`call-appr-closed` / `turn-appr-closed`); the shared `snapshot` helper now derives the required fields per id |
| `ToolApprovalModal.readablePreviews.test.tsx` | 2 end-to-end stub cases RETIRED: "…renders the honest Deny-only card"; "a stub whose reported delta has already elapsed renders as expired" | RETIRED with the subject: the wire can no longer produce a stub (required fields), so `isReconnectStub` is unreachable from real traffic; the dev removes the stub UI branches in the follow-up fold. The delta-of-0 expiry rule survives in the store test. Kept-literal fix: the refreshed-not-duplicated case's snapshot gained the required fields (assertions unchanged, still green) |
| `src/lib/featureSwitches.stage1.test.ts` | "[S1-g approvals] ships INLINE_TOOL_APPROVALS_ENABLED as exactly false" | exactly **true** (card inline in the root chat) |
| `src/components/layout/AppShell.stage1.test.tsx` | 8 modal-path cases (S1-c ×2, S1-d, S1-e, S1-f, S1-q, S1-exp, S1-exp zero) asserting the app-wide dialog | re-aimed to the inline card (`tool-approval-thread-tail` → `tool-approval-panel`) at the same strength: same scenarios, same exact-POST contracts, arrival-order drain, expiry/zero-lifetime, session/workspace stability — acting from the open main chat; `visibleModalFor` → `visibleCardFor` |
| typecheck-only literals | `ToolApprovalModal.test.tsx` ×2, `chat.toolApprovalResolved.test.ts` ×1, k7/resolution/readablePreviews literals | required fields added; assertions unchanged |

## Founder-ruling pins — RED-FOR-THE-FOLLOW-UP on 29f78ee56 (expected)

| Assertion | Why red | The follow-up that turns it green |
|---|---|---|
| S1-f + S1-q: `cross-workspace-approval-banner` absent; no "approvals waiting" / "Go there" text | `AppShell.tsx` still mounts `CrossWorkspaceApprovalBanner` for a cross-workspace approval | the dev removes the banner mount (second commit, after this one) |
| S1-d: a rootless helper approval's card shows in the parent main chat, and the parent main's sidebar attention cue renders | the inline panel filters by `approvalChatId === activeSessionId`, so a rootless child never shows in the parent chat, and no approval→attention wiring exists yet | the dev routes rootless children to the parent chat and triggers `needs_attention` on the parent main (the existing sidebar cue then renders) |

## Proof

- PASS side (29f78ee56, this tree): store 12/12, k7 5/5, resolution 13/13,
  readablePreviews 17/17, featureSwitches 3/3, AppShell.stage1 15/18 — the only 3
  failures are the founder-ruling reds above. `npm run typecheck` exit 0.
- FAIL side (3ccfb2b0e, scratch clone carrying only the re-aimed test files): every
  re-aimed pin fails (pre-B3 stub store, switch OFF, no inline card, permissive Zod).
  Receipts in `tests/train3-b3-logs/` (`pass-*.log`, `fail3cc-*`, `typecheck.log`).

Kept: `ToolApprovalModal.test.tsx`'s non-literal assertions, every non-stub
readablePreviews case, the hand-built reconnect-stub rendering describe (its UI
branches still ship until the fold).
