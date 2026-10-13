# RED-B evidence — session-core U2 (archive) + U3 (ordinary intake)

Author: qa-lead (RED), worktree `session-core-red-b-20261009`,
branch `work/session-core-red-b-20261009` (cut from `work/session-core-build-20261008` @ `da47c59b4`).
Every test below was **observed failing on the current production code, for the right
reason** — a saved log + exit code per run in `red-b/logs/`. Local runs are the one
narrow run the shared rule permits, one exact-name test at a time under
`/tmp/omnipus-go-heavy.lock`; CI remains the authority.

Run shape: `CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^<Name>$' ./pkg/<pkg>/`

| # | Test | FR / spec ref | Log (exit 1) | Observed failure (expected vs actual) |
|---|---|---|---|---|
| 1 | `TestSessionCoreU2_RollbackNeverRewritesRetainedArchiveBytes` (pkg/memory) | FR-006, DEL-12 | `logs/u2-rollback-memory.log` | Rollback truncated the archive 4→2 lines: `after` is a prefix of `before`. FR-006 requires retained bytes byte-identical (append-only). |
| 2 | `TestSessionCoreU2_ArchiveEntryCarriesViewMembership` (pkg/session) | FR-004, C-ARCHIVE | `logs/u2-viewmembership-session.log` | Persisted entry has no `view_membership` key: `{"id":"m-1",...,"agent_id":"mia"}`. |
| 3 | `TestSessionCoreU2_ArchiveIsDayPartitionedNotOneMonolithicFile` (pkg/session) | FR-005 | `logs/u2-dayfiles-session.log` | Two UTC days produced **1** archive data file (monolithic), expected ≥2 day files. |
| 4 | `TestSessionCoreU3_OrdinaryBodyCapDefaultIs65536Bytes` (pkg/config) | FR-010, C-LIMIT | `logs/u3-bodycap-config.log` | `EffectiveSteerBodyBytes()` = 16384, spec default = 65536. |
| 5 | `TestSessionCoreU3_OrdinaryRateDefaultIsSixtyPerMinute` (pkg/config) | FR-010, C-LIMIT | `logs/u3-rate-config.log` | `EffectiveSteerRatePerMinute()` = 6, spec default = 60. |
| 6 | `TestSessionCoreU3_AggregateBodySettingExistsAndDefaultsTo1048576` (pkg/config) | FR-010, C-LIMIT | `logs/u3-aggregate-config.log` | No `steer_aggregate_body` field; decoded struct has no such json key. |
| 7 | `TestSessionCoreU3_CompetingIntakeDeclarationsAreGone` (pkg/agent) | FR-009, DEL-03/04 | `logs/u3-deletions-agent.log` | `parseSteeringMode` still declared (must be deleted); positive controls (`prepareOrdinarySessionExecution`, `awaitPreviousOrdinaryExecution`) present. |

**Instrument controls** (inside each test, they passed before the failing assertion —
proving the check could have seen the failure): persisted id/content readable (#2);
both entries recallable before the day-file count (#3); struct json keys enumerated (#6);
settlement-fence declarations present + fabricated name absent (#7).

**Deferred to CHECK** (never claimed here): see-it-green, mutation kills, and the
Proof-of-failability checklist belong to the CHECK pass under `test-integrity-audit`.

**Not RED'd here — deliberately deferred** (open spec questions, reported to the
RED-B dispatcher / architect, not assumed):
- FR-005 bounded cross-day read and FR-007 retention mark-repair have no spec-named
  symbol to anchor an oracle; test #3 covers only the observable "day files exist".
- FR-008 atomic message+provenance already exists at baseline
  (`UnifiedStore.AppendTranscriptWithProvenance` rolls the pair back), so it is not a
  RED behaviour.
- FR-009's behavioural half (all entry paths land in the one FIFO at safe boundaries)
  and FR-011's live-config wiring (#1216) need the running runner, not a unit seam —
  left to GREEN with the deletion proof in #7 as the K-class half.
- DEL-09 `migrateLegacy`/`MigrateFromJSON`: **not** asserted (F3 — deletion held until
  CONV exists).
