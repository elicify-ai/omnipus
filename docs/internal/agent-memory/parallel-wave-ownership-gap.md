---
name: parallel-wave-ownership-gap
description: "Parallel waves fix callers inside their file list and silently miss cross-package ones; sweep by symbol, not by ownership"
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 9a5cc9d5-94c8-4246-b11e-938e082e3387
  modified: 2026-08-03T20:37:54.501Z
---

File-ownership boundaries are what let parallel agent waves run without collisions,
but they systematically produce a second defect: a unit changes a contract, fixes
every caller **inside its own file list**, and leaves callers in other packages
broken. Nobody owns the seam, so nobody looks.

Three instances in the ADR-057 wave alone (2026-08-03):
- W22 named 12 test files to invert; the two agents owned only `pkg/agent`, so both
  `pkg/gateway` files were never touched — one became a hard CI failure.
- U18 changed `GET /api/v1/sessions` to return the `SessionPage` envelope and fixed
  three `pkg/gateway` tests; `tests/integration`'s own HTTP helper still decoded a
  bare array and died before reaching its real assertions.
- U5 landed a compile shim for U18 to remove; verifying removal needed a cross-unit
  check no single owner would have run.

Each surfaced far from its cause, so CI reported a misleading symptom (a "replay
break" that was a JSON decode; an "LLM call never started" that was a refusal).

**How to apply:** when a wave changes a shared contract (wire type, function
signature, enum, response shape), close it by **grepping the whole tree for the
symbol** — never by trusting the owning unit's file list. Do this as an explicit
post-wave sweep step, and make cross-package callers someone's stated
responsibility. The compiler catches Go signature breaks; it does NOT catch JSON
shape changes, test fixtures, or other languages.

Related: [[mechanism-not-property-defect-class]] (the other recurring class here),
[[parallel-agents-git-index-race]] (the commit-time counterpart of the same
ownership discipline).
