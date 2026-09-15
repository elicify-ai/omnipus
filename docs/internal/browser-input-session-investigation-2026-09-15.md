# Latest Amsterdam input-session investigation — 2026-09-15

## Scope and verified runtime

Read-only investigation of Mia session `session_01M2F8FXJ61CGWEYMXAED5VF2H`, operator browser `ws:01M01TTSDZBFGM28NPHGTFZ17T/operator`. Logs cover the latest interaction around 22:56–22:59 UTC on September14 (05:56–05:59 Jakarta on September15). Installed and running binaries were verified as source `3f978959c19ead2466b9d82e7d5d126e1e74b64b`; machine configuration is unchanged. No runtime change, restart or user-browser interaction was performed.

## Timeline

| UTC | Confirmed event | Meaning |
| --- | --- | --- |
| 22:56:41 | dedicated input `connection_closed` | Input connection closed. Previous recorded mouse events completed in about1–2ms. The log does not identify who initiated closure; viewer count subsequently became0 at22:56:44. Closure during handover/reload versus network failure remains unresolved. |
| 22:57:07–08 | initial viewport1426×577 instead of1426×720, then video recovered | Initial143px deficit occurred again, but recovery followed; no convergence-failure log. This warning alone does not establish the previous inset defect returned. |
| 22:57:58.252–59 | wheel ordinal446 spent1037.524ms in Chrome dispatch, then canceled; `queue_expired` | The wheel reached Chrome dispatch within0.158ms of server receipt. Subsequent reliable inputs aged behind this in-flight call, triggering the one-second queue expiry and pausing control. |
| 22:58:46 | `browser-ws: input dispatch failed`: waiting for tab operation, deadline exceeded | A navigation/control command timed out waiting for the shared browser-tab gate. This is not evidence of WebSocket gesture fallback. |
| 22:58:51 | new target A525624A1A88CF24CCC15E11C7E5731D attach timed out after20s | Adoption holds the same tab-operation gate while waiting for Chrome to attach. This code path can prevent existing-tab commands from proceeding. Correlation strongly implicates this attach in the preceding wait, though the log does not record the gate owner explicitly. |
| 22:58:51–22:59:04 | target no longer found; retry wrappers eventually gave up | Repeated errors refer to the same target. Three identical messages can arise from one attachment plus two coalesced waiters; they are not proof of three independent Chrome attaches. |

## Root cause and uncertainty

There are at least two distinct blocking mechanisms. A scroll dispatch stalled inside Chrome and our serial queue policy paused control when later actions waited too long. Separately, new-tab adoption performs a potentially20second first attachment while owning a gate shared with ordinary browser commands. Neither observation proves deficient WebRTC transport speed or host CPU starvation. The underlying cause of Chrome’s scroll stall and failed attachment is not established by these logs.

The earlier short reproduction passed the sizing fix but did not establish general session input stability. The current session is contrary evidence to treating that bounded result as overall stability certification.

## Evidence and code correlation

Evidence file: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/latest-input-session-server.log`.

- Line1194: `browser dedicated input failed`, `reason=connection_closed`.
- Line1725: wheel ordinal446, `queue_started=0.112922`, `cdp_start=0.157833`, `cdp_done=1037.681882`, `finished=1037.731793`, `outcome=canceled` (offsets in milliseconds from server receipt).
- Line1741: `browser dedicated input failed`, `reason=queue_expired`.
- Line1769: `input canceled while waiting for tab operation: context deadline exceeded`.
- Lines1770–1772:20second new-target attach timeout; lines1788–1790: adoption abandoned after target disappearance.

`pkg/tools/browser/live_input_context.go` brackets the actual Chrome protocol action with the timing stages. Wheel uses `Input.dispatchMouseEvent`. `pkg/tools/browser/webrtc/dedicated_input_queue.go` expires waiting reliable input while the serial sink remains blocked. `pkg/tools/browser/manager.go` acquires the legacy tab command gate before createTab/first attachment (around2837–2861); InputContext waits on the same tab gate before its input gate. TargetCreated and TargetInfoChanged can start independent retry wrappers sharing pendingAdopt, explaining duplicate messages. `pkg/gateway/browser_ws.go` rejects gestures at inbound dispatch; its generic failure line1514 can describe navigate, navigate_back, reload or stop_loading, but omits the exact kind.

Timing failure windows repeat recent summaries, so raw timing-line counts are not counts of unique inputs. Media stream-closed warnings also occur during successful capture replacements and cannot all be counted as independent user failures.

## Next fixes to prioritize

1. Move slow new-target attachment outside the shared existing-tab command critical section, with short validated publication and cancellation/target-identity checks. Retain coalescing across the full retry lifecycle and stop retrying targets already known gone.
2. Reproduce slow wheel acknowledgement with subsequent mixed input, then improve bounded scroll/backlog handling without replaying uncertain gestures or silently dropping meaningful scroll deltas. Merely increasing the queue timeout hides the stall and increases lag.
3. Correlate input connection closure with viewer detach, replacement and transport state so expected handover is distinguishable from an unexplained disconnect. The current logs do not settle this first event.

No fix is claimed by this investigation.

## Fresh log recheck after renewed user report

Fetched the latest 4,000 persistent gateway log entries directly from Amsterdam machine `784041ef9ed398`. The newest entry is September14 at23:21:24 UTC. The latest identifiable manual Mia failure window remains22:56–22:59 UTC (September15 at05:56–05:59 Jakarta). It confirms the same scroll queue expiry and new-tab attachment blockage described above; no newer manual failure window appears in this capture. The23:18:56 and23:21:24 connection closures coincide with our calibration/endurance test teardown and must not be counted as new user disconnects.

Fresh evidence: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/current-session-recheck-server.log`.

The automated endurance baseline independently failed after approximately104 active seconds: resizing during a held arrow key suppressed four subsequent key presses in the viewer before transport. Separate targeted fixes for queue handling, tab-attachment admission, and held-input resize deferral now exist on `browser-improvements`; focused tests pass, but these fixes have not yet been deployed or passed the full20-minute live acceptance run. Do not represent the running instance as fixed.
