# Gateway current-route fixture and notice correction — 2026-09-08

Scope: the scoped WebRTC offer route now emits one combined UDP/TCP/TURN notice within the BrowserStatusFrame message limit. Retired unscoped input/error/state senders, their private error throttle fields, the requested-scale cache, the fallback panel resolver and epoch-only WebRTC attachment commit helper were removed after callsite inventory. Tests use the actual contextual input route and immutable attachment request admission.

Viewport clamp tests derive boundaries from the wire contract, attach an external-CDP test browser, observe the real metrics command, then assert refreshed measured capture geometry. Their previous unattached-cache assertion no longer described product behavior. Cold refresh now starts from an actual conflicting viewport request. The recapture scale regression now installs a measured frame transition and reads the actual qualified control frame.

Successful offer fixtures now attach the live view where required. Sticky-start failure assertions inspect and retry the same panel capture. Linux E2E/QA fixtures now send browser_attach and a positive offer_id, retain the exact panel capture, and pass the original relay context through the production contextual sink. These retain packet/data-channel plumbing coverage; they do not prove decoded video, synchronized audible output, or successful page input.

Evidence before correction:
- Combined failure notice: actual successful offer emitted 830 characters against the schema maximum of 512 (retained run 45844, exit 1).
- Old unattached scale assertion expected 3, observed 1. This is an obsolete fixture, not a new viewport product defect.
- First migrated run 96204 compiled and completed in 19.030s. Combined notice/schema/scoping and qualified recapture scale passed. Input fixture admission and viewport observation assumptions were then corrected.
- Exactly eight Linux-gated entries skipped on Darwin in run 96204: end-to-end media; QA payload/latency; sticky-start failure; ingest timeout classification; generic viewer failure classification; detach-during-negotiation; slow-stop fence; media fallback after offer. Their execution remains required on Linux; local compilation does not establish Linux behavioral success.

GitNexus: initial 87-symbol impact batch and additional reason-detail callers resolved LOW where indexed. Newly introduced fixture functions absent from the index were manually traced to test-only consumers. Epoch-only commit helper resolved LOW; current test helpers absent from the index were manually traced. No change was made to the installed application on port 10994 or its profile.

Closeout evidence:
- The corrected current-route input, registration and publication cases passed in run 57734. Its remaining failures were protocol-fixture errors: CDP's width=0/height=0 device scale override preserves native page dimensions, and scale 1 uses clearDeviceMetricsOverride. The fixture now observes native window bounds separately and implements those protocol semantics.
- Run 25309's corrected seven-leaf clamp prerequisite passed (package 5.555s).
- Both deliberate notice faults failed at their intended assertions: omitting TURN failed the required-cause check; retaining all causes but adding 513 bytes failed the contract check (804 > 512). Both temporary source edits were restored immediately. The corrected combined message is 291 bytes for this simultaneous-failure fixture, versus the observed pre-fix 830.
- The final Linux-only response reader distinguishes the positively identified answer from subsequent active state, which has no offer_id field. It tolerates initial attachment availability/health without weakening current-offer error detection. This change still requires Linux execution.

Initial handoff scope at b2f52e361: old single-cause notice rendering helpers remained with direct unit-test callers only. They are no longer used by the successful production offer path. Their removal was recorded separately rather than expanding the final approved closeout. This component correction does not certify the full release-base diff or final application runtime.

Final local result: retained driver 25309 terminated with exit 0. Both faults were caught; the restored selected gateway race/shuffle run passed 44 test/subtest entries, zero failures, skips or race warnings, in 36.792s. No broader suite was run. The final Linux response-reader correction was made after this race run; those files are excluded by its CGO=1 build and must be compiled/executed by final CGO=0/Linux verification.

Pre-commit detect_changes was attempted against this isolated worktree (90761, exit 1), but GitNexus has no registered index for its path. Manual staged scope and whitespace checks passed: 22 assigned gateway/test/evidence files. The parent must perform the indexed integrated-branch scope check.

## Finite formatter followup

The four unused single-cause formatter methods are now removed. Their existing tests call the production combined formatter instead. This exposed and preserved two existing requirements: a changed bound port must name the consequence for remote viewers, and exhausted fallback ports must explicitly identify the random UDP port. The concise combined message still satisfies the wire limit with all three causes present.

The two migrated old oracles reproduced those exact copy omissions in 2752 (exit 1, 5.879s), before the formatter change. The affected notice selection, including actual UDP/TCP handling, healthy/off controls, contract/translator checks and the actual all-causes offer/schema regression, then passed in 33580 (gateway selection 11.594s). No additional faults or broader suite were needed for this small followup. The same serial driver subsequently rejected a new live-switch fixture prerequisite in the separate browser package; that uncommitted later-lane test is excluded from this formatter commit and is not claimed as a product failure.

Impact: the four removed helpers resolved LOW; the new combined formatter is absent from the index and was manually traced to the real offer route and its tests. The stale requested-scale setter comment was also corrected after the constant's LOW/zero-caller impact check.
