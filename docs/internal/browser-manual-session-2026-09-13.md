# Amsterdam manual-session investigation — 2026-09-13

The reported dispatch errors are confirmed. The proximate failure is browser-command deadline expiry followed by queued-input expiry and a connection reset. The exact slow command and underlying cause are not recorded, so page work and harness-added waiting cannot yet be separated for the two prominent dispatch-failure bursts.

## Session and evidence

Latest session: `session_01M2DG7HZCRM44HKXYYK8G8CB5`, created 13:45:11 UTC (21:45:11 Bali), Browser UAT Test agent, workspace `01M01TTSDZBFGM28NPHGTFZ17T`. Metadata and filesystem ordering identify it as the latest session; its transcript is empty, so it does not identify the visited website or gestures. Runtime caller paths identify deployed `8f5c39ac5`. User reports improved stability, still-noticeable delay, and two visible brief interruptions; speed optimization can wait.

Persistent snapshot: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/manual-session-gateway.log`. Structured time-window extraction: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/manual-session-window.json`. Session metadata/selected logs: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/manual-session-selected.log`. Fly's short recent-log window initially omitted the earlier reset; persistent evidence takes precedence.

## Timeline

Times below are Bali (UTC+8); original logs are UTC.

| Time | Evidence |
| --- | --- |
| 21:49:11 | Three `input canceled: context deadline exceeded` warnings, followed by `Browser input expired; reconnect and retry.` |
| 21:49:52 | Two `browser live: input dispatch failed: context deadline exceeded` warnings and four canceled-input warnings, followed by a second reset. Snapshot lines 7978–7984. |
| 21:54:41 | Two dispatch-deadline warnings followed by a third reset. Snapshot lines 8034–8036. |
| 21:55:23–25 | Input explicitly times out `while waiting for tab operation`. At 21:55:23, viewport verification also times out (requested 633×720). Snapshot lines 8060–8062. |
| 21:55:33 | Viewport readback after browser-chrome compensation also times out; cache is invalidated. Snapshot line 8069. |

Three server-side reset bursts do not imply three separately noticed banners. Media stream-closed and negotiation warnings follow the resets; their ordering does not establish them as the initial cause. Duplicate video-frame timestamps and unhandled DOM-event warnings occur too, but their presence alone does not prove causation.

## Mechanism and limits

`browserCommandQueue` gives each command five seconds from enqueue, including waiting. Live input has at most two seconds (or a smaller remaining/configured budget), shared among tab-operation/input serialization, cleanup and the Chrome command. Exact `input dispatch failed` wording identifies failure returned from the Chrome command path; it does not measure how much of the budget was previously consumed. When a later queued command is already expired before execution, `failBrowserInput` closes the browser socket, causing the observed interruption and subsequent media reconnection.

The exact `Browser input expired` reset originates in the legacy WebSocket command queue. An established dedicated peer routes controls through a different path. Thus these affected queued commands were admitted without an established dedicated input peer; server warnings alone do not prove which URL mode the user selected. The plain home URL supplied last defaults to WebSocket, while the dedicated experiment requires its explicit workspace query URL. Do not count this session as verified dedicated-mode qualitative acceptance.

The later 21:55 errors positively identify input waiting behind a tab operation concurrent with viewport readback timeouts. The earlier two prominent bursts lack gesture kind, queue age and per-stage duration. They do not prove network loss, slow transport, a Chrome crash, or pure harness overhead. Navigation/page response time must remain separate from harness-added delay.

## Next investigation

Obtain the visited website and triggering gesture from the user. Reproduce the dispatch/resize stall with stage timing and explicit mode proof, prioritizing stability before further speed tuning. Existing optional input timing covers only down/up and is read at handler startup; enabling it alone would not measure wheel/navigation/resize sufficiently. No runtime, configuration, timeout or deployment was changed during this read-only investigation. Source-path review was independent and bounded; no tests or build were run.

## Dedicated-input follow-up

A fresh Mac-hosted viewer attached to Amsterdam source `8f5c39ac5` on 2026-09-13 at approximately 15:13 UTC. Dedicated negotiation and viewport control acknowledgment succeeded, and the viewer remained ready during a 35-second observation. A separate instrumented interaction run passed in 52.2 seconds: 13 clicks, exact Unicode text, 300 pixels of scrolling, dragging, tab return, resizing, held-key release after intentional input-peer closure, keyboard Retry, and exact post-recovery input. Observed gesture routes were only `input-reliable` and `input-hover`. The single captured failed state was the intentionally closed input channel.

This does **not** reproduce or resolve the user's intermittent dedicated-input failure. Existing server snapshots record media-peer shutdown and viewport readback timeouts, but lack the dedicated-input failure reason sent to the client. The suspected circular viewport recovery was not substantiated: the outer viewport operation measures independently, and fresh media offers refresh capture geometry. No speculative viewport fix was made.

Evidence: /Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/remote-smoke-20260913T151651Z/current-dedicated-dedicate-3d7d2-rag-and-recovery-stay-exact/input-connection-evidence.json

Mandatory dedicated-routing changes are tracked separately in ADR-081 and its specification. Their local verification includes 382 focused frontend tests, a subsequent 16-test type-correct fixture check, the focused gateway suite, and deliberate WebSocket/media-bypass faults caught by the new boundary tests. The deployment status must be read from verified build provenance, not inferred from this report.
