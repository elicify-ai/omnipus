---
name: gitnexus-replaces-graphify
description: "graphify is retired for omnipus — GitNexus is the code-intelligence tool; its index is per-checkout, not per-branch"
metadata: 
  node_type: memory
  type: project
  originSessionId: 90a5dcd1-4156-4d4a-9727-bab20b4a9cf8
  modified: 2026-07-27T14:37:06.976Z
---

**graphify is retired.** For the omnipus repo, code intelligence is **GitNexus** (MCP
tools: `query`, `context`, `impact`, `trace`, `explain`). There is no `graphify-out/`;
`graphify query` / `explain` / `path` / `update` do not work. Confirmed and written into
`CLAUDE.md` + `.claude/CLAUDE.md` on 2026-07-27.

**Why this matters beyond one repo:** the sibling checkout `/home/dev/omnipus`
(branch `sendfile-fix`, used by another session) may still carry the old graphify
instructions in its own `CLAUDE.md`. Subagents dispatched there — and several dispatched
here on 2026-07-27 — burned tool calls discovering `graphify-out/graph.json` does not
exist before falling back to grep.

**Non-obvious: the GitNexus index is per-CHECKOUT, not per-branch.**
`~/.gitnexus/registry.json` holds one entry per working tree, each with its own
`.gitnexus/` storage (~478 MB) and its own branch — e.g. `/home/dev/omnipus`
(`sendfile-fix`) and `/home/dev/omnipus3` (`feature/plan-swimlane-board`) are separate,
independent graphs. **Both are registered under the same name (`omnipus`)**, so a tool
resolving by name rather than path can silently read the other checkout's graph. Check the
registry when a result looks like it came from the wrong branch. Re-index with
`node .gitnexus/run.cjs analyze` from that checkout's root.

Related: [[devpod-gh-token-resource]] — same class of shared-machine gotcha.
