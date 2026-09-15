# Browser behavior under shared CPU pressure

Implements the approved graceful-overload changes on `browser-improvements`. Amsterdam, CPU allocation, binary input transport, tool policy, and the installed Mac application stay unchanged.

## Behavior

- Interactive Chrome calls taking at least 100 ms request less video work. Navigation is excluded because its duration includes page loading. The signal measures Chrome-call elapsed time, not exclusively overhead introduced by Omnipus.
- Video source and encoder ceilings decrease from the existing 30 FPS to 20 and then 15. Downward steps are at least one second apart. After ten seconds without a pressure signal, restore one step; allow another ten quiet seconds before restoring full rate.
- At most one pressure send and one constraint-application worker can be in flight. Capture replacement retains the requested reduction. Original geometry constraints are preserved.
- Compatible pending scroll can merge at queue capacity instead of failing the input connection. Direction, sequence, held-input boundaries and the original one-second freshness deadline remain enforced.
- The first queue expiry on a healthy peer may recover automatically after the exact release acknowledgment. It requires no locally held input, unchanged confirmed capture, focused and visible remote text input, no composition, no competing agent activity, and no intervening gesture/control/visibility change. It does not retake control or replay discarded actions. Repeated expiry on that peer requires explicit Resume.
- Automatic recovery displays a notice that some recent actions were not sent and were not replayed. The release acknowledgment proves cleanup, not that a slow page is now fast.

## Deliberate limits

Generic key-repeat dropping is unsafe: the current packet does not identify editing versus game context, and dropping arrow/backspace/space repeats can change text editing or game behavior. Every meaningful key event remains preserved until the existing explicit overload policy cancels pending work. Existing latest-only hover and scroll accumulation are retained.

Adaptation starts after a slow call finishes or is canceled. It cannot prevent the first completely blocked call or guarantee processor time from a shared host. The frame-rate thresholds are conservative starting values, not a measured optimum.

## Verification plan and evidence

Expected behavior derives from the approved conversation and the invariants above, rather than observed implementation output.

| Boundary | Required evidence |
| --- | --- |
| Queue admission | Compatible scroll at capacity succeeds with exact summed distance; incompatible action at capacity fails; sequence and oldest deadline remain valid. |
| Pressure signal | 100 ms and one-second boundaries, interactive-only classification, stopped/unbound sessions, one in-flight send, real dispatch wiring. |
| Video adaptation | Actual encoder implementation exercises 20/15 floor, ten-second restoration, bounded pending work, stale-peer fences, track replacement and geometry preservation. Real Chrome checks actual track and sender settings separately. |
| Automatic recovery | Exact acknowledgment and capture identity, focus/visibility/composition/held-key exclusions, one attempt, no replay, native channel closure and manual fallback. |
| Sustained workload | Twelve rounds each of eight held-key downs plus one release, 30 scroll increments, 60 hover positions, click, two Unicode characters and drag. Exact results read back from streamed pixels. Repeat with 120 ms deliberate key-handler and 75 ms wheel-handler work. |
| Severe stall | Keep keys held to verify explicit recovery; release them locally before expiry to verify eligible automatic recovery. Both preserve the original peer and discard stale actions. |

The performance workload is a comparative experiment; a passing baseline is not a failed reproduction test. Its elapsed gesture-to-picture measurements include intentional page work and test pacing. Do not label that whole duration harness overhead.

Private root evidence: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/`, files prefixed `pressure-`, `automatic-pressure-`, and `input-pressure-`.

Live evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/`.

## Independent review

One read-only grill round found and resolved: missing scroll pressure classification, nil test logging, capture-binding retirement before asynchronous send, lost source cap on same-peer track replacement, and insufficient independent rate-limit boundary coverage. No remaining blocking code finding was reported. Performance claims remain dependent on the real-machine results, not that review verdict.

## Real-browser findings during verification

The first Mac capture check failed during browsing-tab startup, before the encoder; it is not a passing adaptation result. The isolated Linux check used the actual Amsterdam Chromium binary and a separate test browser. Its original reproduction helper omitted the current offer/capture identity fields; the encoder correctly rejected the stale handshake. The helper now uses the generated current protocol and binding checks.

After that correction, real Linux capture exposed a production issue hidden by the mocked constraint tests: copying `getConstraints()` reapplied the one-time tab-capture acquisition token as `deviceId.exact`. The already-bound track reports a different device identity, so constraint application failed before either source or sender FPS changed. A disposable clone with standard frame-rate constraints immediately reported 20 FPS; waiting 200 ms on the original made no difference. The narrow correction omits acquisition identity when reconfiguring an existing track, while retaining original geometry and frame-rate constraints. The corrected real-Chrome test passed on Amsterdam Chromium 152.0.7977.82: actual source/sender settings were 20/20, 15/15, 20/20, and 30/uncapped, with 1344 × 672 dimensions throughout. Test timestamps were advanced only for the two restoration boundaries; the Node harness independently verifies the real ten-second policy. This proves applied limits, not a quantified reduction in processor use.

## Results

The Amsterdam candidate is source `7767f749933455c85fc0bee10f8bb963ca2a1e20`, extension 1.0.24. Installed and running binary SHA256 both matched `3755c6c0b8471437ff3e3aa184c514c051af5b26285897ab0cf8f65bb9a74fbe`. The machine remains in Amsterdam with four shared CPUs and 4096 MB memory; its environment, services, storage, initialization and restart settings are unchanged.

The focused frontend suite passed 48 checks, with held-state, capture-identity and once-only faults deliberately caught. Queue admission, pressure boundaries and encoder behavior passed focused tests and deliberate-fault checks. The consumed-token regression failed before the fix and passed afterward; exact real-Linux cap/restore integration also passed. Production frontend and Linux builds passed. Full CI was not rerun for each fix.

Two live severe-stall checks passed on this candidate: explicit recovery with held input (25.0 s), and eligible automatic recovery (21.8 s). Both retained the original peer and media, released held state, discarded stale actions, and accepted only fresh input. These are test durations, not latency measurements.

Both sustained workloads passed: twelve rounds each on a normal page and a page with 120 ms deliberate key-handler work plus 75 ms wheel-handler work. Each ended with 12 clicks, 132 downs, 48 ups, zero held inputs, 3600 scroll units, 12 drags, zero errors, and exactly `@é` repeated twelve times. Sent routes included all 96 keydowns, 12 keyups and 12 text commits per workload, and used binary v1 only with no WebSocket gesture traffic. Replaceable mouse movement counts legitimately varied.

The international-input regression passed in 25.5 s with exact text `a@日本é🙂🙂`, zero held keys and zero page errors. This verifies the exercised composition cases, not every physical keyboard/OS layout.

| Workload | Gesture-to-picture category | Baseline median / maximum (ms) | Candidate median / maximum (ms) |
| --- | --- | --- | --- |
| Normal | Key batch | 2762 / 4855 | 2204 / 2412 |
| Normal | Scroll batch | 4589 / 7146 | 3661 / 5731 |
| Normal | Click | 1104 / 2645 | 566 / 662 |
| Normal | Text | 1057 / 2899 | 615 / 743 |
| Slow handlers | Key batch | 3384 / 5987 | 2182 / 3374 |
| Slow handlers | Scroll batch | 5341 / 8491 | 3985 / 6933 |
| Slow handlers | Click | 1422 / 4694 | 614 / 889 |
| Slow handlers | Text | 1434 / 4900 | 622 / 887 |

There are twelve samples per category; nearest-rank p95 therefore equals maximum. These durations include deliberate pacing, page work, automation and picture verification. The candidate's measured workload spans were 09:40:34.750–09:42:49.642 UTC (134.89 s) and 09:43:14.870–09:45:53.172 UTC (158.30 s), on 2026-09-14.

No dedicated-input failure, queue expiry or dispatch deadline error appeared within either workload span. The two deliberate expiry events belonged to the earlier recovery tests, and connection closure events were outside the workloads. Media setup and OpenH264 initialization warnings remain visible in the raw logs; they did not produce page errors or failed input assertions in these runs.

**Performance limit:** candidate CPU sampling covered 99.17%/99.72% of the workloads, with 0.85%/0.62% CPU steal (time the virtual CPUs could not run). The baseline normal overlap had 25.04% steal but covered only 18.23%; there are no overlapping CPU samples for the corrected slow baseline. These uncontrolled runs observed faster responses, but cannot attribute the improvement to this code or quantify CPU savings. The original incomplete slow-baseline attempt is retained separately; its first missing key was never sent by the viewer, and explicit focus/readiness checks corrected the harness before the valid baseline.

Candidate live artifacts are under `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/pressure-stress-candidate-7767f7499/`, with recovery and international artifacts in `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/queue-pause-candidate-7767f7499/` and `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/international-keyboard-candidate-7767f7499/`. The comparison report is `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/pressure-stress-comparison-7767f7499.json`.

No full-session endurance, audible A/V synchronization or complete OS/keyboard matrix is certified by these bounded tests. Successful pressure signals use Debug logging, so their absence in Amsterdam's Info logs is not evidence of failed delivery; failures remain warnings. The dispatch wiring test and real encoder integration prove the two sides independently.
