---
name: playwright-mcp-browser-is-shared
description: The Playwright MCP browser is ONE instance shared by all parallel agents — never parallelise UI testing on it
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 9a5cc9d5-94c8-4246-b11e-938e082e3387
  modified: 2026-08-03T22:22:41.613Z
---

The **playwright MCP server is a single shared browser** — one process, one context,
one cookie jar, shared tabs — across every agent in a parallel wave. It is NOT
per-agent.

Discovered 2026-08-03 when four parallel UAT agents tested one deployment. Two
independently observed: an onboarding wizard advancing 1→2→3 with no input from the
observing agent, a login form filling itself in, a tab arriving already-authenticated
without logging in, and both tabs bouncing to `/#/login` with 401s on
`/auth/validate` for ~20s before recovering.

**Every one of those reads as a product defect and none of them is.** Left
uncorrected, the UAT report would have been full of confident, fabricated findings —
worse than no UAT, because they'd have cost a debugging cycle each.

**How to apply:**
- Do NOT parallelise browser-driven UI testing. Either serialize the browser across
  agents, or give each agent REST as its primary instrument.
- `curl` IS properly isolated — separate process, own cookie jar, own bearer token.
  Have each agent log in and hold its own token.
- Brief every parallel UI agent: *never report a UI observation you did not
  personally cause; reproduce it via REST before calling it a defect.*
- Require a "Methodology & Limitations" section separating REST-backed results from
  browser-backed ones.
- Symptoms that are especially deceptive: spontaneous logout, session invalidation,
  form state changing, unexplained 401. These mimic auth/approval/cancel bugs
  exactly.

Related: [[parallel-wave-ownership-gap]] and [[parallel-agents-git-index-race]] — the
same underlying lesson, that parallel agents silently share more state than the
wave design assumes (git index, /tmp, and now the browser).
