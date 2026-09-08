# Gateway CI lint and fixture correction

PR #685 run `34216723190` reported 21 gateway lint findings and gateway regression failures. The sole production edit renames a shadowed local error; it changes no behavior. Test lint corrections close HTTP upgrade response bodies, check assertions, add JSON tags, and retain context and callback observations without lint suppressions.

The recovery fixture now installs the frame-qualified recapture sender used by production and establishes one accepted current media receipt before injecting stale binding/generation receipts. This clears the initial frame’s legitimate recovery grace period without changing the one-second failure oracle. Measured handler fixtures wait for the actual initial document-discovery query before registering their manually seeded capture. That prevents asynchronous initialization from retiring the test frame; generation, route, readiness, measured scale and viewport assertions remain unchanged. The two Linux-only error-classification fixtures now use measured protocol observations so they reach their intended relay failure.

Focused validation passed 121 test records (63 top-level groups) and the restored race run passed the same records in 13.868 seconds without race warnings. That run retained two historical Linux-only skips. Inspection confirmed the capability policy already supports macOS, so those two classification tests now use the current supported-host gate. A subsequent seven-group batch passed nine records with zero skips; its deliberate ingest-timeout misclassification failed the intended wire assertion while the generic-error control passed, and the exact restored race run passed all nine records in 5.844 seconds without race warnings.

Three earlier deliberate faults also failed the intended assertions: wrong answer generation, treating old binding/generation receipts as progress, and a maximum scale inconsistent with the independent contract. All temporary production faults were restored byte-for-byte. Gateway-only lint passed with zero issues using the CI-pinned golangci-lint 2.10.1, `goolm,stdjson` tags, and all configured checks. The earlier three-minute lint timeout and its two remaining context-recording findings are retained in `gateway-ci-lint-initial.log`; both findings were corrected without exclusions. Full CI rerun remains separate.

Raw CI evidence:

- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-review-evidence/ci-linter.log`
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-review-evidence/ci-go-tests.log`
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-review-evidence/ci-race.log`

Local evidence is retained alongside those files with the `gateway-ci-` prefix. Initial setup/build failures (missing ignored SPA assets and a misplaced fixture argument) were corrected before execution. The first actual focused batch exposed the missing healthy-media setup in two watchdog subcases; those failures are retained rather than relabeled as successful validation.
