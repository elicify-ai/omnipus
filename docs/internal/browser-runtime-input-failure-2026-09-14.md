# Input failure during the latest manual Amsterdam session

## Finding

The latest logged input failure is a runtime backlog failure on deployed binary
input candidate `027306f0952e7752f5c1494063e29b740ca6f437`, distinct from the
previous empty-protocol upgrade rejection. Browser commands reached the backend
and were admitted. Time spent waiting for Chrome command responses increased;
the one-second reliable-queue limit then canceled the input source and closed
its dedicated peer.

This identifies the immediate shutdown mechanism. It does not identify how much
of the Chrome-response wait came from page work, Chrome processing, CDP transport
or host scheduling. Legitimate page work must not be mislabeled as harness-added
latency. There is no evidence that starting a screen recording caused the stall.

## Timeline (UTC)

| Time | Evidence | Interpretation |
| --- | --- | --- |
| 06:08:43–06:10:11 | First capture recorded 512 completed inputs; keyboard Chrome-response median about 25 ms, maximum 64 ms | Early interaction was healthy in this sample. The routine timing quota then exhausted. |
| 06:11:52 and 06:11:58 | Media transitions and closed-stream warnings followed by recovery; viewport applied at 06:11:58 | Activity near the first failure, but no established causal link. |
| 06:12:01 | Dedicated input failure `connection_closed`, control epoch 5 | Earlier distinct disconnection. Its immediate cause is unobserved; detailed timing ended about 110 seconds earlier. |
| 06:13:49 | Second capture begins; 422 timing records reach its failure | 421 completed actions and one canceled action in the available records. |
| 06:14:51–06:14:55 | Five mouse-move Chrome-response waits about 723, 719, 964, 1,193 and 963 ms | Slowdown began before the keyboard backlog; this was not just a typing burst. |
| Before 06:14:59 | Keyboard reliable sequences 92–94 waited about 330, 472 and 723 ms for Chrome; sequence 95 waited 967 ms in the queue | The serial dispatch path accumulated waiting reliable input. |
| 06:14:59 | `queue_expired`; sequence 104 canceled after about 322 ms queue wait plus 799 ms awaiting Chrome | Oldest pending input reached the one-second age limit while another command was executing. |
| 06:16:52 and 06:17:52 | Further media closed-stream/transport warnings | Later events; not evidence of the earlier queue-expiry cause. |

The canceled command entered Chrome dispatch with roughly 2,000 ms of its own
execution budget remaining. It was canceled by source retirement, not by its
own two-second deadline. The expired pending frame's identity is not logged;
it must not be inferred to be a specific key-up or sequence number.

For the second capture, measured tab/input gate waits were at most 0.034 ms.
The observed delay is concentrated in the Chrome-command response stage, not
those admission locks. The Fly machine remained started in Amsterdam, with its
last machine update at 05:34:54 UTC and four shared CPUs / 4 GiB configured.
That is not a historical CPU-utilization measurement or proof of resource health.

## Why the UI says only “Input connection failed”

The queue's oldest-entry timer can cancel the source while the serial sink is
blocked. `DedicatedInputPeer.fail` closes the peer before sending its detailed
state notification. A client data-channel error can therefore win the race and
show the generic frontend message. The generic UI error alone does not establish
a network failure; the backend log provides the stronger queue-expiry evidence.

## Next investigation and correction targets

1. Reproduce the 0.7–1.2 second Chrome-response stalls with renderer, capture and
   host measurements around the same interval. Separate legitimate page work
   from delays introduced by the remote-browser implementation.
2. Revisit the interaction between the one-second pending-input limit and the
   two-second active-operation budget. Preserve order, release held keys and
   avoid replay; do not hide the problem by accumulating a larger stale queue.
3. Preserve the actual failure reason in the UI and record bounded failure-window
   diagnostics even after the routine sample quota is exhausted. This is needed
   to resolve the first connection closure as well.

This turn was read-only diagnosis of the runtime. No production changes,
configuration changes, browser navigation or deployment were performed.

## Evidence

Persistent gateway JSON log excerpt, retrieved read-only:
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/input-session-persistent.log`.
The shorter Fly buffer excerpt is
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/input-session-latest.log`. Independent read-only review confirmed the timing
calculations and distinguished the two failures. Source paths:
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/pkg/tools/browser/webrtc/dedicated_input_queue.go`,
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/pkg/tools/browser/webrtc/dedicated_input_peer.go`,
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/src/lib/browserInputWebRTC.ts`.
