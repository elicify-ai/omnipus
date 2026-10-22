# Bind the two crash-recovery fault tests

Baseline merged first: 2f6deed0370e6f877b1cc59d7696c8ef9ed054b9. Chief authorizes tests only, no new public production API, and passive private hooks which remain nil in production.

Oracle: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/crash-recovery/DESIGN.md`::Decision 2 (unstable readback is Unknown; durable hold before model/dispatch; zero automatic writes) / Decision 4 (failed rollback must be visibly unconfirmed). Expected values below are derived before editing the main test assertions.

## Real boundary and cases

| Test | Real mechanism / injected edge | Independent expected result |
|---|---|---|
| Retry rollback, no-fault control | Actual initial task start/crash snapshot/boot reconciliation and Board Retry, with a genuinely unavailable executor; real store/atomic writer | HTTP 503, prior Failed status restored, StartedAt cleared, old run unchanged, no fresh binding/run |
| Retry rollback, write fault | Same path. Private pre-atomic-write observer filters the rollback's task JSON after In Progress was persisted, moves the original JSON aside and creates a directory at the destination. The unmodified atomic writer must encounter the actual filesystem failure | One correctly phased observer hit, HTTP 500 with an explicit inability-to-revert/storage error, no normal success/timer claim, exact pre-rollback In Progress bytes remain after restoration; old run unchanged and no execution admitted |
| Concurrent hash read, stable control | Native append/write/edit applied-before-result cut and genuine fsynced witness; same-home restore; actual adapter readback | effect_present and unchanged full target bytes |
| Concurrent hash read, outside writer | Install private post-hash/pre-second-stat observer only after setup. Atomically replace the actual target with divergent outside-writer bytes after its old bytes were hashed, before identity/size revalidation. Real adapter and native boot recovery, never a mock reconciler | One observer hit, Unknown (never present/absent), interrupted/reconciliation_required before provider dispatch, zero provider requests/automatic writes, outside bytes retained, truthful engine recovery observation, original_result_recovered=false |

The two private slots requested from backend are:

- `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/crash-recovery-qa/pkg/task/store.go`::taskBeforeAtomicWriteTestHook, type `func(path string, data []byte)`, nil by default; call inside Store.write's existing sidecar lock immediately before writeFileAtomicFn.
- `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/crash-recovery-qa/pkg/tools/file_effect_check.go`::fileEffectAfterHashReadTestHook, type `func(target string)`, nil by default; call in readFileEffectImage after successful io.CopyBuffer, before the second f.Stat / h.Stat validation.

Hooks have no return value and make no decisions. Test callbacks touch temporary files only. A test-only cross-package compiler alias keeps the tests buildable on a commit whose production code does not yet call these slots. A missing call fails the real-operation hit-count and outcome assertions; there is no unconditional fatal or skip. The alias's present-slot/missing-slot behaviour is verified separately in a tiny scratch instrument, not counted as product RED. Same-package aliases were rejected by the compiler; the bridge deliberately lives in a different test-support package.

## Failability and execution

Mutations for independent CHECK: omit the private callback (hit count fails); ignore atomic-writer failure (HTTP/state assertions fail); ignore post-read identity validation (present/absent/dispatch/byte assertions fail); continue an Unknown helper (provider/effect count fails). GREEN, mutations and proof-of-failability remain deferred to independent CHECK.

Only the two named tests and minimal directly impacted controls run, serially, via shared slotrun with CGO_ENABLED=0, tags goolm,stdjson, -count=1 -v -p 1. No full package/suite, lane full CI, new public production API or production source edit. Full CI remains integration-branch draft PR work.
