# Browser candidate CI follow-up — 2026-09-13

Consolidated checks on `808ae4f1f397c1cc46d726784e558e8b138231b8` are terminal: 44 passed, 11 failed, 2 skipped. This is not green. The PR remains a draft targeting `release/v0.1.1`; main is unchanged.

## Demonstrated browser-branch corrections

- Six Vitest groups stopped at the unchanged coverage guard because the local Playwright fixture was named `.test.ts`. Running it under Vitest also failed collection. It is now correctly named `.spec.ts`, with an isolated Playwright configuration and explicit execution in the existing lib-store job. The real four cases passed, with zero skipped/flaky cases; the coverage guard passed unchanged. No guard or test was disabled.
- SPA lint rejected the recovery harness's bare `this` alias. A peer holder preserves the same identity checks; targeted ESLint and strict TypeScript passed.
- Go lint reported five variable-name/shadowing/error-format issues. The minimal corrections passed focused lint with zero issues before the subsequent logging change.
- New browser logging sites could pass control characters to a console formatter that supports multiline values. A real ingest WebSocket regression failed before escaping and passed afterward; boundary cases cover quotes, backslashes, Unicode and terminal controls. An isolated removal of escaping made those tests fail again. Only the new browser logging fields changed; the global logger, tool system and policy code were not modified. Independent bounded review found no concrete defect. Final focused Go lint passed with zero issues; the consolidated follow-up remains pending.

## Remaining findings kept separate

The Linux cross-platform security test `TestCSRFCORSReflectionGate` failed during temporary-directory removal, with `directory not empty`; its security assertions did not report a failure. A test-only forensic overlay reproduced the failure and identified a detached live model-limit cache write (`LiveLimits.fetchAndStore` → `saveLocked` → `WriteFileAtomic`) after shutdown. The security harness now selects a supported custom provider without credentials, preventing this unrelated metadata lookup. The identical 20-repeat forensic command passed afterward (33.253 seconds), with no model-limit cache writes. This is configuration isolation for security tests, not a production worker-shutdown fix; security assertions and runtime source remain unchanged. The full security package passed (31.204 seconds); final focused security-package lint also passed with zero issues after correcting a shadowed error variable. Independent read-only review checked the test consumers and found no actionable issue: explicit onboarding/credential tests retain their own provider setup.

The CodeQL check reported 497 new alerts and explicitly warned that large diffs can include alerts not introduced by the PR. Of 25 annotations on changed files, seven concerned the new browser logging sites (six distinct callsites), two concerned a prior credential-sweep change, and sixteen flagged sink lines already present in `release/v0.1.1`. This attribution does not dismiss older alerts or prove that changed inputs cannot reach older sinks. The wider security check remains open; no alerts are suppressed. Full branch alerts and raw annotations are retained for further scope-specific analysis.

## Runtime evidence remains distinct from CI

Amsterdam now runs verified runtime `eb3206c03`, including the diagnostic-field escaping follow-up; its focused dedicated live smoke passed (54.1 seconds), including exact interactions and forced-loss release/Retry. Earlier runtime `ef76f7536` supplied the following measurements: Six clean alternating remote comparisons passed. Its separate unchanged controlled Linux gate passed 100 clicks/300 exact events at p95 182.6 ms against 200 ms, with dedicated-route and same-peer proof. Those results do not prove the follow-up source is deployed, long-duration acceptance, or remote-network latency below 200 ms.

[Runtime evidence](https://github.com/elicify-ai/omnipus/blob/browser-improvements/docs/internal/browser-input-candidate-2026-09-13.md).

Raw CI and focused-check logs: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/`. Controlled Linux artifacts: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-input-controlled-linux/`.


## Combined follow-up in progress

Checks on `bcc192e78b7ee15b5b361a36b459f36cef979075` are still running/queued. The Linux ARM cross-platform job failed in `TestLoopCommand_Stop_Interval_EmitsSchemaValidStoppedFrame` during temporary `media` directory cleanup in `pkg/agent`; no failure of its schema assertion was reported. This is a different test and path from the corrected security harness. This separate production agent-lifecycle issue remains outside the compact browser experiment work queue; no such source change was made. CodeQL remains failed (195 high, 299 medium, one warning and one note); remaining browser annotations are undergoing bounded source assessment, with no suppression. Terminal totals are pending.


Bounded source review found no raw-string bypass at the new escaped browser diagnostic sites. `browserLogValue` keeps control characters, quotes and backslashes escaped through the existing console formatter. Persisting annotations are consistent with an unmodeled sanitizer, although annotation JSON cannot prove the CodeQL model. Older raw relay and schema-error logging remain real separate concerns: both existed unchanged in `release/v0.1.1` (relay commit `9fd6c3c1df`, schema commit `32fb66c2d8`). They are not silently marked fixed. No security-check suppression or global logger change was made.
