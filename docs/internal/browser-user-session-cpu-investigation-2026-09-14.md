# Recent browser session investigation — 2026-09-14

The latest input pause was caused by input accumulating while Chrome responded slowly. The new recovery mechanism paused input instead of immediately closing the healthy input connection. The underlying slowdown remains unresolved. Shared CPU availability is a significant new suspect, but has not been proven as the cause at the exact failure times.

## Scope and evidence

- Amsterdam app `uat-omnipus`, machine `784041ef9ed398`: four shared CPUs, 4 GiB memory.
- Running candidate source `6be7ce9f07fce3626a34bb453f0f88ba6625bb3e`; installed/running binary SHA-256 `3116abc2d25a8c00570402fdbe1452c9e6a2313ebdb166788afdf13b6a7b2c0b`.
- Latest Mia session `session_01M2FDYAYWMT5GBE88Y3SRW7KZ` in workspace `01M01TTSDZBFGM28NPHGTFZ17T`.
- Reviewed persistent gateway logs through 07:53:57 UTC, Fly console logs, Chrome stderr, panic log, session metadata, machine state and a subsequent 61-sample host CPU capture. This covers available logs, not unrecorded client console activity.
- Private evidence directory: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/`. Files use prefix `recent-user-0753-`; primary evidence is `persistent.log`, `window.json` and `host.jsonl` under that prefix.
- Read-only runtime investigation: no deployment, browser navigation, CPU-plan change or application-code change.

## Error timeline

| UTC | Recorded error | Finding |
| --- | --- | --- |
| 07:47:01 | Dedicated input `queue_expired` | Mouse responses had reached 153–228 ms. Eight consecutive keydowns arrived a median 85 ms apart while Chrome responses took a median 156 ms; retained queue wait grew to 511 ms. The final canceled operation was not retained. |
| 07:48:39 | `browser-ws: input dispatch failed: context deadline exceeded` | Separate navigation/control command timeout. This path accepts navigate, back and reload; it does not carry mouse or keyboard fallback. The log omits the command kind, so the exact action and contribution from page loading cannot be identified. |
| 07:52:30 | Dedicated input `queue_expired` | Matches “Browser fell behind. Input paused. Resume input.” Chrome responses slowed sharply, pending input aged out, and the queue paused. |

Persistent-log lines for these events are 11729, 11767 and 11991 respectively in the captured file. These are capture-specific line numbers.

## Final pause: where time went

The first 68 events in the final 80-event sequence were fast: median Chrome response 4.27 ms, maximum 27.43 ms. From 07:52:27.585, individual Chrome waits rose to approximately 73, 155, 561, 79, 161, 79, 403, 242, 154, 237 and 4 ms. Queue waits reached approximately 960 ms.

Gateway queue-start to Chrome-dispatch overhead stayed below 0.04 ms in this slow burst. The measured backlog therefore accumulated while waiting for Chrome, rather than in gateway admission work. This does not measure every part of end-to-end transport or visual latency.

The last active keydown was canceled after approximately 804 ms queued and 236 ms waiting for Chrome. Another pending event reaching the one-second freshness limit can trigger cancellation; the active event's own queue time need not exceed one second. The logs do not identify the exact pending event that triggered expiry.

No later input or Resume sequence appears before the end of the captured logs. Recovery after this particular user pause is therefore not verified. Earlier controlled tests established that explicit Resume works, but are separate evidence.

## New finding: processor time withheld from the virtual machine

The subsequent host capture lasted about 63 seconds. Across the four virtual CPUs, approximately 39.74% of accounted time was CPU steal: processor time the guest was waiting to receive from its host. Actual execution accounted for approximately 4.91%. This is substantial, despite low apparent process CPU usage.

Fly documents a shared-CPU baseline of 6.25% per virtual CPU, with temporary burst credits. Four shared CPUs therefore provide roughly one quarter of a CPU in aggregate sustained baseline capacity; four shared CPUs are not equivalent to four continuously available CPU cores. Fly also explains that steal can reflect quota throttling or competing virtual machines. See [Fly CPU performance](https://fly.io/docs/machines/cpu-performance/).

This sample was taken after the errors. It establishes a current CPU-availability problem, not its exact cause or temporal coincidence with either input failure. Historical monitoring API access returned HTTP 401, so burst-credit balance and quota-throttle history could not be checked. The evidence cannot yet distinguish exhausted CPU credits from host contention. Relevant metric definitions are in [Fly metrics](https://fly.io/docs/monitoring/metrics/).

## Other log findings

- Chrome reported GPU stalls during pixel readback around 07:46:21–28, before the first pause. This implicates graphics work as another candidate, but does not prove it caused either queue expiry.
- Repeated duplicate video-frame timestamps and encoder warnings warrant investigation for uneven video. Their relationship to input latency is unproven.
- Media health messages were transitional or recovered; none reported a terminal failed health state in the reviewed window. Several connection-close and negotiation warnings accompanied transitions.
- No fresh panic, browser crash or out-of-memory event was found. Approximately 3.1 GiB of memory remained available at the later check; machine uptime showed no intervening restart.
- Agent navigation around 07:50 took 11.7 seconds. That duration includes navigation/page work and must not be treated as harness-added latency.
- Timing logs repeat retained diagnostic windows. Deduplication must include receive timestamp as well as capture/ordinal because ordinals restart across attachments.

## Recommended next actions

1. Compare the same workload in Amsterdam with sustained performance CPU capacity, holding application version, page and input scenario constant. Record Chrome response times, queue age, steal time and provider throttle/burst metrics. This is the highest-value experiment, not a proven fix or an instruction to change paid resources automatically.
2. If pauses persist with adequate CPU time, isolate graphics/capture load from page/input handling using a controlled comparison. Inspect pixel readback and software encoding costs before changing transport again.
3. Add the navigation command kind and relevant stage durations to timeout diagnostics so page-loading time can be separated from delay introduced by our code.
4. Preserve the bounded queue and explicit Resume behavior. Raising the timeout would allow older actions to accumulate and would conceal the responsiveness problem.

The next success criterion is a repeatable session without queue expiry under the same interaction load, backed by stage timings. A changed error message alone is not evidence that the underlying slowdown is fixed.
