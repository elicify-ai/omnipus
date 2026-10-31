# QA lint preservation and exact-source RED plan

Scope: the12 QA findings in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/green-be-logs/fly-codec-lint/worker/go-lint.log`: two unchecked type assertions and10 shadowed error variables. Correct only checked type assertions/error-variable names; no expectation, fixture, skip, policy, lint configuration or production edit.

The test oracles remain the paging specification and existing independently derived fixtures. Added type checks require the existing private manifest-entry object and diagnostic outcome string shapes. GitNexus is unavailable; direct helper/caller/scope review is Inferred low impact, never a claimed graph run.

| File / symbol group | Intended change | Exact behavioral entry point |
|---|---|---|
| CLI shared_core::repairRefusalMatrix | Check map assertion | TestHistoryRepairCommand_BuiltPhasesCutoverAndManifest/schema_cutover_and_exact_aggregate_exit_matrix/traversal |
| CLI manifest_vectors | Name parse/stat errors distinctly | TestHistoryRepairCommand_ManifestFingerprintFrozenVectors |
| CLI projection_parity | Name append error distinctly | TestHistoryRepairCommand_SharedProjectionEffectAndIdentityParity |
| Gateway storage diagnostic | Check outcome string assertion | TestHistoryPageDiagnostics_RawPositionStorageFailureAndPlantedLeak |
| Gateway attach canonical-copy helper | Name donor read error distinctly | TestUnavailableHistory_CanonicalAppendAndLiveOnlyControls |
| Gateway near-admission projection | Name marshal error distinctly | TestChatHistoryPage_NearAdmissionRecordsHaveNoHardByteDefense |
| Session fixture builder/driver | Name stat/path errors distinctly | TestChatHistoryFixture_DayBuilderUsesAppendDayAndRealRollover; TestChatHistoryFixtureDriver with a REAL explicit seed_days plan |
| Session recovery | Name repair-build/open errors distinctly | TestChatHistoryRepair_SharedPublicationCrashAndRepeat/lookups_staged; TestChatHistoryRecovery_CorruptSavedPrefixNeverTrimsCanonical/torn_canonical |
| Session retention | Name tombstone error distinctly | TestChatHistoryRetention_ActualRetainedContinuation |

## Required evidence

Team-lead Q3=A authorizes the exact committed test-only sources over historical production base **5addc4e7c44726d1b2f85a106bc3766db36ad7c6**, without production mutation. Record base SHA, test-source SHA, per-file SHA256 and overlay method; failures must be named assertions for the intended missing behavior, never compile/no-match/skip. Separately run the same test sources on the current GREEN-carrying branch. A current pass is behavior preservation, not renamed RED; any unavailable RED or current behavior remains an explicit gap.

The approved Fly resource is080d74db170408, serialized under the worker flock, existing ci user, one Go test process at a time. Compile locally first, no full gates, no config/chown/safe.directory changes, collect logs BEFORE any owned stop. Never stop another owner's run. The near-admission fixture writes about800MiB, so it must never run on this Mac. The utility driver receives a real owned root/plan; its deliberate no-plan utility skip is not execution proof.

No CHECK verdict, production mutation kill, native Windows certification or UAT is claimed. GREEN re-verification, mutations, restored GREEN and Proof-of-failability are deferred to a fresh CHECK instance.
