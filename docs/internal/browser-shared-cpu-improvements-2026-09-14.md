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

## Results

Validation and deployed comparison are in progress. Do not interpret this document as a release or performance certification until the result section is completed.
