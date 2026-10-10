# Navigation Wave-1 production allowance — 2026-10-08

## Authorization

The 2026-10-08 CI-fix brief requested a deliberate final-head allowance increase for Wave-1, subject to fresh measurement and the existing design-system margin. The squad lead subsequently confirmed **482 KiB** after reviewing the evidence below. This approval supersedes AB1's 430 KiB current allowance; it does not change the frozen baseline, waive other checks, or authorize landing.

The amendment follows the governance requirement in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/nav-wave1-fe1/docs/internal/design/design-system-definition.md` (Non-goals and governance): record the decision, date, evidence and updated enforcement. No scanner debt or reviewed-boundary exception is being introduced, so `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/nav-wave1-fe1/design-system/enforcement/ledger.json` remains unchanged.

## Measured cause, not a blanket increase

| Measurement | Revision | Total raw bytes | Growth over frozen baseline |
|---|---|---:|---:|
| Frozen design-system baseline | `92aeb4d5dcb0d545e2370ddd1c57c099a03392ba` | 26,131,305 | 0 |
| Release base already on Wave-1 | `641491cfb2f9454ceae54273c959020e143a4133` | 26,570,995 | 439,690 |
| Wave-1 head that failed CI | `04b9906366232d0f9215dbdabbcf97cd345a095d` | 26,620,796 | 489,491 |

The release base had only **630 bytes** remaining under the old 440,320-byte allowance. Wave-1 adds **49,801 bytes**: 48,178 bytes of JavaScript and 1,623 bytes of CSS. Dependency files are unchanged between the compared revisions; the package-lock SHA-256 is `2a272a0ae3dac45cd9d0dc06d712971fc55ab6e5b030932f0aaaad6c8c5b4f62`. Static images, HTML and PDF runtime assets contribute zero to this increase. The source diff contains the intended shared agent identity/icon/editor, inline indicator and Sessions changes, not a new runtime dependency.

Both revisions were built on this Mac with the same installed dependencies. `npm run build` built the current head; the release-base attribution build used its archived source and Vite configuration with the identical dependency lock. This comparison attributes growth; it does not replace CI's frozen-baseline build with the original lock on the same runner.

GitHub PR #1238 run [37785172811](https://github.com/elicify-ai/omnipus/actions/runs/37785172811), bound to the head above, independently reports the **same** baseline/head totals and cumulative delta. All four non-raw-budget checks passed: output exclusion, module exclusion, JavaScript asset/provenance hash agreement, and initial compressed-download limit.

## Exact allowance and margin

| Policy | Before | After |
|---|---:|---:|
| Cumulative raw-growth allowance | 430 KiB / 440,320 bytes | **482 KiB / 493,568 bytes** |
| Additional allowance | — | 52 KiB / 53,248 bytes |
| Headroom over measured Wave-1 candidate | −49,171 bytes | **4,077 bytes** |
| Headroom as a share of Wave-1's 49,801-byte addition | — | **8.19%** |
| Initial gzip-growth allowance | 25 KiB / 25,600 bytes | **Unchanged** |

The existing dated budget commentary records approximately 8% feature-addition margin for the Mail live-access increase (and approximately 7.4% for its predecessor). Applying 8% to this addition and rounding up to a whole KiB gives `ceil((489491 + 0.08 * 49801) / 1024) = 482`. This is the smallest whole-KiB allowance meeting that margin; 481 KiB leaves only 3,053 bytes / 6.13%.

The candidate's initial gzip is 347,881 bytes: **64,027 bytes below** the frozen baseline's 411,908, and only 186 bytes above the release-base build. The raw allowance is not an initial-download allowance. No baseline reset, measurement omission, provenance bypass, Storybook exception or gzip-ceiling change is permitted.

## Enforcement and verification

The production allowance lives in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/nav-wave1-fe1/scripts/design-system/bundle-audit.mjs` (`TOTAL_RAW_BUDGET_BYTES`). The two literal current-policy boundary tests in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/nav-wave1-fe1/tests/design-system/bundle-measure.test.mjs` are rebound to the approved **493,568** and **493,569** byte deltas. They still assert exact allowance, delta and all five check results; the one-byte-over candidate must fail specifically the raw-budget check. Expected values remain literals derived from the confirmed policy, never from the production constant.

Required checks: TypeScript project gate; design-system unit, lock-fixture and package checks; production measurement followed by the real bundle audit; and the narrow channel-instance browser file. Deliberate raw-budget mutations must be killed before hand-back. Full end-to-end and whole-branch acceptance remain GitHub CI and independent CHECK/review responsibilities.

No user-facing documentation change is required for this repair: completing an E2E fixture and changing a build-time allowance do not alter product UI or behavior.

## Verification receipt — 2026-10-08

| Check | Direct exit and result |
|---|---|
| `npm run typecheck` | 0; project build-mode typecheck completed |
| `npm run test:design-system:unit` | 0; 553 passed, 0 failed/skipped |
| `npm run test:design-system:locks` | 0; 1,869 passed, 0 failed/skipped |
| `npm run test:design-system:package` | 0; fresh library build and 9 package checks passed |
| `npm run lint:design-system-locks` | 0; audit PASS, 0 errors |
| `npm run build` | 0; fresh SPA and Mail bundle check passed |
| `npm run measure:design-system-bundle` | 0; 26,620,796 raw bytes, 347,881 initial gzip bytes |
| Real `bundle-audit.mjs` invocation | Red 1 → green 0; raw delta 489,491, allowance 493,568; all five checks true |
| Audit of original CI candidate/baseline/provenance | 0 with the approved cap; all five checks true |
| Literal boundary test file | Red 1 against the old cap → green 0; 8 tests passed |
| Comparator mutation probes | Cap −1, cap +1, and raw-check-always-true each exited 1 with named assertion failures; 3 caught, 0 survived, source restored before final gates |
| Original channel-instance E2E file | Binding case red 1 (`Sales → mia`) → whole-file green 0; 6 passed, 0 skips, original assertions unchanged |

All **917 emitted asset hashes** in the final local candidate match the original head-bound CI candidate. This binds the allowance repair to the artifact that actually failed, rather than a different or smaller build. The local E2E check serves the fresh static SPA, retains the repository's original Playwright configuration/global setup, and uses the installed pre-Wave-1 gateway only for real authentication/bootstrap; agents/channels/workspaces under test are intercepted by the original spec's network fixtures. It is not a full exact-commit gateway acceptance claim.

## Reproduction receipts

Saved commands, direct exits, candidate/base reports, source/provenance records and mutation receipts are under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/nav-wave1-fe1/test-results/nav-wave1-ci-fix2-20261008/`. The original CI evidence is retained separately under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/nav-wave1-fe1/dist/nav-wave1-ci-fix2-20261008-ci-evidence/`, downloaded from the head-bound `design-system-evidence` artifact. Generated full provenance is run evidence, not a hand-edited or replaced baseline.
