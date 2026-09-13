# Scroll backlog follow-up

## Observed failure

The Amsterdam user session on candidate `8165a714f` at 2026-09-13
18:00:13–18:01:08 UTC produced 424 input timing records. The last scrolling
burst arrived roughly every 30–40 ms while Chrome dispatch took about 75 ms.
Waiting time grew to 986 ms; the final dispatch was canceled after 53 ms.
This strongly implicates the one-second reliable queue limit, rather than the
two-second active Chrome deadline. That candidate did not log the terminal
queue reason directly. The earlier successful fixture dispatched wheels at
about 15 Hz and did not reproduce this pressure.

## Change

Combine compatible wheel movements already waiting at the queue tail. Preserve
their total delta and oldest arrival time. Every incoming sequence is still
validated. Never merge across a click, key, held input, direction reversal,
modifier, coordinate or capture change. The active dispatch is unchanged.
Neither timeout is increased; user input remains exclusively on WebRTC.

Timing diagnostics record the first and last sequence and number of represented
inputs. Terminal input failures gain a fixed-label server diagnostic independent
of whether the client notification can still be delivered.

This deliberately changes the number of wheel callbacks a page receives.
Accumulated distance is preserved, but arbitrary custom handlers and scroll
snapping may respond differently to fewer, larger events.

## Validation so far

- Behavioral RED: pending wheels dispatched separately and sustained 30 ms
  arrivals against a 75 ms consumer expired the queue before the fix.
- Focused queue tests passed after the fix, including exact accumulated distance,
  held-state ordering, invalid sequences, capture boundaries and oldest expiry.
- Queue race checks passed.
- Gateway diagnostics behavioral RED confirmed missing failure and sequence logs.
- Gateway checks passed on a focused recheck (14.666 s). The initial wider
  dedicated-input selection had four channel-readiness setup timeouts in the
  existing payload test; the same payload test passed on recheck unchanged.
- Live old-candidate reproduction at 18:21:02–18:21:04 UTC: trusted native
  wheels scheduled every 35 ms produced 46 dedicated sends after viewer
  coalescing, with a median interval around 46 ms. The page reached only 375
  of 900 intended scroll units and displayed the same Retry input failure.
  The initial assertion sampled the last viewer flush too early (885); final
  evidence confirmed all 900 were sent. The probe now waits for that flush.
- Updated-candidate live comparison and final binary verification are pending.

Only targeted checks are required for this follow-up; this is not a full CI claim.
