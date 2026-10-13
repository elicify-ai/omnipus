# RED-CONV evidence — session-core CONV (one-time saved-chat cutover)

Author: qa-lead (RED), worktree `session-core-red-conv-20261009`,
branch `work/session-core-red-conv-20261009` (cut from `work/session-core-build-20261008` @ `da47c59b4`).
Spec: `docs/internal/specs/session-core-spec.md` section **CONV — One-time saved-chat cutover only**,
FR-038 (sole exception), DEL-09/10/11, BDD-12.6, T35. ADR-20261006 (D1/D12).
Founder **Q2 = B (narrowed)**: greenfield for heartbeats only; KEEP the one-time conversion that
keeps existing saved chats reachable.

Run shape (the one narrow local run the shared rule permits, one exact-name test at a time under
`/tmp/omnipus-go-heavy.lock`; CI remains the authority):
`CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^<Name>$' ./pkg/<pkg>/`

## RED tests (observed failing on current production code, for the right reason)

| # | Test | Spec ref | Log (exit 1) | Observed failure (expected vs actual) |
|---|---|---|---|---|
| 1 | `TestSessionCoreConv_PreCutoverSavedChatIsNormalizedAndContinuable` (pkg/session) | CONV Identity, BDD-12.6 | `logs/conv-normalize.log` | 3 assertions fail: `meta.ActiveAgentID` = `"jim"` (expected `""`); on-disk `type` = absent/nil (expected `"chat"`); on-disk `active_agent_id` still present. The chat is listable in memory but never NORMALIZED on disk. |
| 2 | `TestSessionCoreConv_SecondBootIsIdempotentAndAddsNoSecondIdentity` (pkg/session) | CONV Publication/retry | `logs/conv-idempotent.log` | Precondition fails: on-disk `type` = absent/nil after the first boot (expected `"chat"`), so the idempotency+no-duplication guards it protects can't yet hold. |
| 3 | `TestSessionCoreConv_InterruptedBootRetiresTheCompletedSourceWithoutDuplicating` (pkg/session) | CONV Publication/retry | `logs/conv-interrupted.log` | `<id>.jsonl` still present after boot though the same-id session dir already held the converted content — the retry never finishes the cleanup (`migrateLegacy`'s "already migrated" branch leaves the source). |
| 4 | `TestSessionCoreConv_CorruptSavedChatSurfacesVisibleFailureAndRetainsSource` (pkg/session) | CONV Failure | `logs/conv-corrupt.log` | `NewUnifiedStoreWithHome` returned `nil` error over a corrupt saved chat (expected a visible cutover error): the corrupt chat is silently excluded (`cacheLoadFailures`) while boot proceeds — skipped-with-success. |
| 5 | `TestSessionCoreConv_Del09LegacyRuntimeReaderIsAbsent` (pkg/session) | DEL-09 | `logs/conv-del09-session.log` | `migrateLegacy` still declared in pkg/session (must be deleted once CONV is the sole legacy reader). |
| 6 | `TestSessionCoreConv_Del09MigrateFromJSONIsAbsent` (pkg/memory) | DEL-09 | `logs/conv-del09-memory.log` | `MigrateFromJSON` still declared in pkg/memory (must be deleted once CONV is the sole legacy reader). |

A combined run of all pkg/session CONV tests (isolation/ordering check) is in `logs/conv-all-session.log`:
5 RED + 2 control-PASS, no cross-test interference.

## Controls (green at baseline — must STAY green after GREEN)

| # | Test | Purpose | Log (exit 0) |
|---|---|---|---|
| C1 | `TestSessionCoreConv_FreshInstallIsANoOpWithZeroWrites` | CONV Completion/fresh install: no saved chats ⇒ zero conversion writes, no invented session. Guards a conversion that mints sessions. | `logs/conv-fresh.log` |
| C2 | `TestSessionCoreConv_ConversionMintsNoMainAndPromotesNoHeartbeat` | Destination + Heartbeat boundary: the conversion mints NO computed main and does NOT promote a heartbeat-typed record (Q2=B: no heartbeat→main conversion). | `logs/conv-nomain.log` |

**Instrument controls** (inside the tests, they pass before the failing assertion — proving the check
could have seen the failure): the chat is listable and `GetMeta` succeeds before the normalization
assertions (#1); the on-disk document parses and its `agent_id` is readable (#1); the scanner sees a
real `readUnifiedMeta` / `NewJSONLStore` declaration and a fabricated sentinel reads absent (#5/#6).

## Notes carried to the reviewer / architect (not silently assumed)

- **Seam.** CONV has no named Go entry point in the spec. These tests drive the EXISTING public boot
  surface (`NewUnifiedStoreWithHome` + the store's own methods) and assert the observable POST-BOOT
  state, which the spec pins ("Run CONV at first cutover boot before session-cache/list/attach").
  If GREEN wires CONV as a separate boot step rather than at store construction, the RED pack's seam
  assumption must be reconciled — flagged, not assumed. No new symbol is referenced.
- **Visible-failure oracle (#4):** the assertion is "boot SURFACES an error" (constructor error).
  Today the constructor is documented as never aborting over a bad session; the CONV Failure rule
  ("do not start ordinary session-serving/dispatch over incomplete conversion") overrides that for
  a saved chat that needs conversion. If GREEN surfaces the error through another channel, reconcile.
- **DEL-09 timing (#5/#6):** these two removal tests stay red until U2 lands the actual deletions
  (which are gated ON CONV) — they encode the replacement's contract, per the WC-1 RED-CONV brief.
- **Not covered here (gap, reported):** the per-agent → shared move leg (E-CONV:
  `pkg/agent/instance.go::initSessionStore`, `loop_session.go::ResolveSessionStore`/`ListAllSessions`)
  is a boot-level pkg/agent operation this `pkg/session` pack does not exercise — needs a pkg/agent
  or joint J-05 integration test. `writeUnifiedMetaDirect` (named in DEL-09) is NOT asserted absent
  because CONV may legitimately reuse it; open question for the architect.
