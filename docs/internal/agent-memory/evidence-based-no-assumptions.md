---
name: evidence-based-no-assumptions
description: Standing rule — never act or advise from assumptions; reproduce and verify everything with direct evidence before stating it as fact
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 9a5cc9d5-94c8-4246-b11e-938e082e3387
  modified: 2026-07-30T20:22:50.671Z
---

Never act on, or advise based on, an assumption. Every claim about system behavior or root cause must be reproduced/verified directly (logs, on-disk records, code read, or a re-run) before being stated as fact. If verification isn't possible, say so explicitly and label the claim as unverified/inferred rather than presenting it with unearned confidence.

**Why:** during the 2026-07-30 omnipus3 session (plan-swimlane-board epic + live UAT debugging with the user's "Jim"/Mia conversation), I repeatedly stated conclusions from partial or stale evidence and had to walk them back after the user pushed back:
- Diagnosed a "stopped mid-turn, orphaned by dropped connection" root cause from a transcript file that turned out to be a **stale, incomplete snapshot** (907 of 3411 actual lines) — the real conversation had continued and successfully concluded hours later.
- Asserted "the server-side turn stops when the client connection drops" without checking the actual code path; when the user asked "what do you say the turn stops server side when the client connection drops?", checking the code showed the "no active connection" error was only a failed *delivery* of an already-computed message, not a cause of turn cancellation.
- Landed on a final root-cause writeup that was still honest about one remaining gap (exact trigger for a batch turn hard-abort) rather than asserting it — the user's [[fablize-strict-evidence-mode]] directive formalizes that this honesty about gaps must be the norm, not a courtesy.

**How to apply:** before any diagnostic/root-cause statement — re-fetch current data rather than trusting an earlier fetch if time has passed or the system could have changed; trace the actual code path making a causal claim, don't infer it from timing/log correlation alone; when a gap in the causal chain can't be closed, say so explicitly and offer to keep digging rather than papering over it with confident language. Applies session-wide, not just to debugging tasks. See [[omnipus-diagnostic-traps]] for the specific traps (stale reads, keyword greps, WARN-only logs) already learned in this same project.
