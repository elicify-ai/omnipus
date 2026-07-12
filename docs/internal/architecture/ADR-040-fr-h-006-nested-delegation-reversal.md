# ADR-040: Reverse FR-H-006's Registry-Level "One Level Only" Delegation Block

**Status:** Proposed
**Date:** 2026-07-12
**Deciders:** architect (recording); requires product-owner ratification (Daniel Piatkowski) — see "Why this needs ratification" below
**Relates to:** [ADR-036 — Consolidate shell and subagent tools](./ADR-036-consolidate-shell-and-subagent-tools.md) (renamed spawn/run_subagent → `delegate`, carried the exclusion forward unchanged), [ADR-037 — Remove the global per-agent delegation policy](./ADR-037-remove-global-delegation-policy.md) (made the per-workspace trust graph the sole delegation authority — this ADR relies on that authority being sufficient on its own)
**Superseded requirement:** `docs/internal/_archive/plan/sprint-h-subagent-block-spec.md` FR-H-006 ("one level only for general subagents", dated as an owner decision 2026-04-20)

## Context

FR-H-006 was an explicit, dated product decision: delegation chains stop at one hop. `spawnSubTurn` (`pkg/agent/subturn.go`) enforced it structurally — every delegated child's tool registry was built via `execSource.Tools.CloneExcept(tools.ExcludedDelegate, tools.ExcludedHandoff)`, so the `delegate` tool object was never present in a sub-turn's own registry. A delegated agent could not call `delegate` again no matter how permissive its configuration.

Since that decision was recorded, a different, independent enforcement system was built and shipped: the per-workspace delegation trust graph (`workspace.DelegationEdge`), made the **sole runtime authority** for delegation by ADR-037, with per-edge `Modes` and `Depth`, a global `SubTurn.MaxDepth` ceiling, and a fail-closed gate (`buildDelegationDenyChecker` → `findDelegationEdge` → `enforceEdgeModeAndDepth`, `pkg/agent/loop.go:1895-2025`). This system already supports and correctly enforces multi-hop delegation chains up to a configurable depth (default `defaultMaxSubTurnDepth == 3`), gated per-edge by an operator-authorized trust relationship — it was simply never allowed to run, because FR-H-006's registry-level block sat underneath it and pre-empted every case, including an explicitly wired, fully-unrestricted edge.

Live UAT (2026-07-12) reported this as a bug: `jim` (await) → `ray` (await) → `ray` attempting to delegate onward to `planner` via an explicitly authorized, unrestricted `ray → planner` edge failed with a generic `permission_denied`, never reaching `DelegateTool.Execute`'s real trust-graph gate. The failure mode was confusing — `load_tool` reported delegate as successfully loaded (because it resolves the caller's identity against the wrong registry — the persistent top-level `AgentInstance`, not the ephemeral child's own clone), masking the true cause.

**BRD grounding:** FUNC-* multi-agent delegation requirements and Appendix D's system-agent tool model assume agents can compose delegation chains; there is no BRD requirement mandating a one-hop ceiling — FR-H-006 was a local implementation spec (`sprint-h-subagent-block-spec.md`), not a BRD-sourced constraint. `[INFERRED]` FR-H-006 was likely a conservative initial guardrail adopted before the trust-graph/depth-cap system existed, to bound blast radius while delegation was new and unaudited.

## Decision

Reverse FR-H-006's registry-level exclusion. `spawnSubTurn` now constructs a delegated child's tool registry via `CloneExcept(tools.ExcludedHandoff)` only — `delegate` is retained. Nested delegation is governed exclusively by the pre-existing trust-graph/mode/depth gate:

- **Trust set:** an edge `caller → target` must exist in the effective workspace's delegation graph, or the call is denied (`DenyTrustSet`), fail-closed on any graph-read error.
- **Mode:** the edge's `Modes` must permit the call's mode (background/await/task); empty `Modes` = all allowed.
- **Depth:** the delegation-chain depth (`turnState.depth`, incremented per hop — `childTS.depth = parentTS.depth + 1`, tracked independently of tool-registry contents) must be below the tighter of the edge's own `Depth` and the global `SubTurn.MaxDepth`, both resolved through the single shared function `resolveEffectiveDelegationDepth` so the deny-check and `spawnSubTurn`'s own backstop check can never diverge (the #477 bug this shared function already closed).

`hand_off` remains excluded — a nested sub-turn hijacking the active parent session's agent identity is a distinct concern (session takeover), not a task-delegation-depth concern, and this ADR does not touch it.

**This decision does not reopen ADR-032 (delegation identity: a sub-turn runs as the target's own real instance, execSource-sourced) or ADR-037 (delegation trust is exclusively the per-workspace graph).** It operates one layer beneath both: whether the `delegate` tool *object* is present in a child's registry at all. Identity-sourcing and trust-authority-location are unchanged.

## Why this needs ratification

FR-H-006 was recorded as a dated **owner decision**, not a default or an oversight. This ADR is a de-facto reversal of that decision, currently justified and shipped as part of a "bug fix" PR on the strength of an architecture review, not a fresh product sign-off. The trust-graph/depth-cap system is judged sufficient as the sole safety boundary (see Consequences), but the change in *product-level delegation topology* — general subagents can now form chains of depth up to 3 by default, not just one hop — is a decision with cost/autonomy/compute-exposure implications an operator may want to weigh in on explicitly, per this repo's own convention that decisions "high-cost, irreversible, or needing justification later" get an ADR rather than being absorbed silently into a bug-fix diff. Recommend: product owner reviews and flips Status to Accepted (or requests changes) before this ships past the current hotfix branch.

## Consequences

### Positive
- Restores the trust-graph/depth-cap system to being the actual, sole delegation gate it was designed to be — closing a gap where an operator's explicit, wired, unrestricted edge was silently overridden by unconditional code, which is precisely the class of implicit code-branch override CLAUDE.md's Hard Constraint #6 exists to eliminate elsewhere in tool-policy resolution.
- Removes a confusing failure mode: `load_tool` previously reported fabricated success for `delegate` inside a child sub-turn while the child's real tool registry never gained it, so denial happened generically and unhelpfully instead of through `DelegateTool.Execute`'s real, informative trust-graph denial.
- Verified sufficient as a standalone safety boundary: `enforceEdgeModeAndDepth` is fail-closed at every stage (unreadable graph, no edge, mode mismatch, depth exceeded), and depth tracking (`turnState.depth`) is structurally independent of tool-registry presence, so this reversal cannot be used to bypass the depth cap even if some other bug later re-excludes `delegate` from a registry incorrectly.

### Negative
- Genuine increase in the space of possible agent behavior: general subagents can now form delegation chains up to `defaultMaxSubTurnDepth` (3) by default, where before they structurally could not exceed one hop regardless of configuration. This is a real autonomy/cost/blast-radius increase for any workspace whose trust graph has chained edges, even though each hop remains individually authorized.
- The depth-cap default (3) and per-edge overrides were tuned/reasoned about as *theoretical* protections before this change (nothing could actually reach depth 2 for general subagents); this is their first time as the *only* protection in practice for this agent class. Recommend a follow-up: confirm `defaultMaxSubTurnDepth` and the seeded per-edge depths in `coreagent.SeedDelegationEdges` still reflect an intentional choice now that they are live, not just a backstop under a stricter block.

### Neutral
- No wire-format, contract, or persisted-schema change (Hard Constraint #8 not implicated) — this is purely a Go-internal registry-construction change plus documentation.
- `ExcludedDelegate` remains defined in `pkg/tools/registry.go` and is still exercised by tests as a `CloneExcept` primitive capability; it is simply no longer passed at the production call site.

## Alternatives Considered

### Keep the registry-level block, add an explicit "chain-delegation" opt-in flag
- Pros: preserves the one-hop default as an even more conservative posture; a chain is only possible where an operator flips a new, purpose-built flag.
- Cons: duplicates enforcement the trust-graph/depth-cap system already provides; adds a second policy surface for the same concern (echoes the exact divergence ADR-037 eliminated between the global delegation policy and the workspace graph — two representations of "who may delegate" drift apart over time). Rejected: the existing graph already IS the purpose-built opt-in mechanism (an edge must be explicitly wired); a second flag would be redundant, not additive.

### Leave FR-H-006 in place; treat the UAT report as expected behavior, not a bug
- Pros: zero code change, no ratification needed, no cost/autonomy increase.
- Cons: leaves a wired, explicitly-authorized trust edge permanently unusable, which is itself a "Saved but does nothing" trust gap in the wired-edge UI — the same defect class ADR-037 §1 flagged for the old global delegation policy screen. Rejected by the live UAT finding: an explicitly configured edge silently not working is a bug, not a feature.

## Affected Components

- Backend: `pkg/agent/subturn.go` (`spawnSubTurn`'s `CloneExcept` call site), `pkg/tools/registry.go` (stale doc comments — see review findings, not yet corrected as of this ADR), `pkg/agent/delegation_depth.go` / `pkg/agent/loop.go` (unchanged — this ADR relies on, does not modify, that gate).
- Frontend: none.
- Variants: all three (Open Source, Desktop, SaaS) — the trust-graph/depth-cap system is deployment-mode-agnostic.

## Integration Contract

Not applicable — no wire type introduced or changed by this decision.
