# Crash-recovery: five QA-owned fixture corrections

Authorized baseline: merge origin/fix/crash-recovery at 46dc9ec532ba01a5312199cc39ac58e41fa9388a first. Production remains unchanged after that merge. Chief's 2026-10-23 brief and DESIGN Decisions 1–3 are the oracle; every existing assertion and its intent is retained.

| Correction | One-line justification |
|---|---|
| Tree Stop | Set Tree:true so the fixture requests the tree operation that its descendant/frozen-parent assertions already specify; plain Stop remains session-only. |
| Unchanged mount symlink | Reuse an identical destination symlink instead of recreating it; real mounts and directory identities stay intact. |
| Retirement storage failure | Corrupt only the captured snapshot so the old live turn can finish normally and the intended reboot/human-entry failure assertions are reached. |
| Missing correlation | Decode into a fresh addressing.Capture, then read it back and prove both correlation fields absent before testing context-repair refusal. |
| Notice reason type | Derive the helper parameter from the generated Session interruption_reason enum and preserve literal table types, without widening the contract or changing notice expectations. |

Run the four exact affected Go selectors serially, one per shared slotrun invocation, CGO_ENABLED=0, tags goolm,stdjson, -count=1 -v -p 1. Also run the existing exact snapshot/directory-identity control because the shared copy helper changes. Frontend: one notice test file, at most two workers, then make verify-contracts and the required design-system lock scan. Do not run the full pack; backend owns that rerun.

Before each heavy command (and again after obtaining the shared slot), sample the 1-minute load and wait while it exceeds 24. Save load samples, actual commands, source hashes, named/collected outcomes and direct exit codes. No sleeps are added to tests; an operational load hold is not a test oracle. No assertion deletion/weakening, skips, suppressions, production mutation, generated-file hand edit or full lane CI. Mutation/independent CHECK and whole-pack acceptance remain separate gates.
