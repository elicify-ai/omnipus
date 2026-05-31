# Omnipus internal documentation

Documentation for people who **build or modify** Omnipus: architecture audits,
Architecture Decision Records, in-flight specs, forward-looking designs, the
business requirements that drive the roadmap, and historical working artifacts.

These documents are not written for first-time users. For installing, configuring,
and operating Omnipus, see the [user documentation](../README.md).

> When this material disagrees with the code, **the code wins.** The
> evidence-based [AS-IS architecture audit](architecture/AS-IS-architecture.md)
> is the closest thing to a source of truth, but it is dated — verify against the
> code before relying on any claim here.

---

## Architecture

- [Agent loop flow](architecture/agent-loop-flow.md) — request → response, turn engine, cancellation
- [Steering (mid-turn redirection)](architecture/steering.md), [steering spec](design/steering-spec.md)
- [Sub-turn flow](architecture/subturn.md)
- [Spawn & sub-tasks](architecture/spawn-tasks.md)
- [AS-IS architecture audit (2026-04)](architecture/AS-IS-architecture.md) — evidence-based code walkthrough, dated; a reference point, not kept in sync with day-to-day code
- [Plugin / extension landscape](architecture/plugin-extensibility-assessment.md)
- [Library refactor — impact assessment](architecture/library-refactor-impact-assessment.md), [risk analysis](architecture/library-refactor-risk-analysis.md)

## Architecture Decision Records (ADRs)

Locked decisions that explain *why* the code looks the way it does:

- [ADR-004 — Credential boot contract](architecture/ADR-004-credential-boot-contract.md)
- [ADR-005 — CI e2e gateway contract](architecture/ADR-005-ci-e2e-gateway-contract.md)
- [ADR-006 — CSRF double-submit cookie](architecture/ADR-006-csrf-double-submit-cookie.md)
- [ADR-007 — Middleware chain order](architecture/ADR-007-middleware-chain-order.md)
- [ADR-008 — Context-key aliases](architecture/ADR-008-ctxkey-aliases.md)
- [ADR-009 — Per-agent sandbox as security boundary](architecture/ADR-009-per-agent-sandbox-as-security-boundary.md)
- [ADR-010 — Remove GHSA channel block on exec](architecture/ADR-010-remove-ghsa-channel-block-on-exec.md)
- [ADR-011 — Experimental workspace-shell default false](architecture/ADR-011-experimental-workspace-shell-default-false.md)
- [ADR-012 — OpenAPI version](architecture/ADR-012-openapi-version.md)
- [ADR-013 — Inbound validation](architecture/ADR-013-inbound-validation.md)
- [ADR-014 — additionalProperties default](architecture/ADR-014-additional-properties.md)
- [ADR-015 — Decode-and-validate](architecture/ADR-015-decode-and-validate.md)
- [ADR-016 — SPA streaming resilience](architecture/ADR-016-spa-streaming-resilience.md)

## Specs (under review or in flight)

Designs being worked out. Not shipping behaviour:

- [Browser automation wiring](specs/browser-automation-wiring-spec.md)
- [Cancel across channels](specs/cancel-cross-channel-spec.md) ([review](specs/cancel-cross-channel-spec-review.md))
- [Chat-served iframe preview](specs/chat-served-iframe-preview-spec.md)
- [Device pairing](specs/device-pairing-spec.md)
- [Joined session store](specs/joined-session-store-spec.md)
- [Policy-change approval](specs/policy-change-approval-spec.md)
- [RBAC granularity](specs/rbac-granularity-spec.md)
- [Tool registry redesign](specs/tool-registry-redesign-spec.md) ([review](specs/tool-registry-redesign-spec-review.md))
- [Env-awareness & memory](specs/env-awareness-and-memory-spec.md) ([review](specs/env-awareness-and-memory-spec-review.md))

## Future designs (v0.3 "Rooms")

Forward-looking designs not in v0.1 or v0.2:

- [Sandbox topology](design/sandbox-redesign-2026-05.md)
- [Memory (Dreamcatcher consolidation)](design/memory-redesign-2026-05.md)
- [Tasks (per-room scoping)](design/tasks-redesign-2026-05.md)
- [Projects UI](design/projects-ui-2026-05.md)
- [Settings & notifications](design/settings-notifications-2026-05.md)
- [Provider refactoring](design/provider-refactoring.md) ([tests](design/provider-refactoring-tests.md))

## Business requirements (intent of record)

The original product specs. When the code disagrees with the BRD, **the code
wins** — but the BRD is still load-bearing context for understanding *why* a
feature exists:

- [Main BRD](BRD/Omnipus%20BRD.md) — 30 security + 36 functional requirements, delivery phases
- [Appendix A — Windows kernel security](BRD/Omnipus%20Windows%20BRD%20appendic.md)
- [Appendix B — Feature parity](BRD/Omnipus_BRD_AppendixB_Feature_Parity.md)
- [Appendix C — UI / UX spec](BRD/Omnipus_BRD_AppendixC_UI_Spec.md)
- [Appendix D — System agent & system tools](BRD/Omnipus_BRD_AppendixD_System_Agent.md)
- [Appendix E — File-based data model](BRD/Omnipus_BRD_AppendixE_DataModel.md)

## Working artifacts (historical)

Snapshots from past work efforts — sprint plans, code-review passes, regression
notes, and the agent-refactor working area. Kept for provenance; **not**
maintained as current documentation and may be stale.

- [Sprint & wave plans](plan/) — per-sprint implementation specs
- [Review passes & investigations](investigation/) — archived multi-agent review output
- [Regression notes](regression/) — dated regression write-ups
- [Agent refactor working area](agent-refactor/README.md)

## Brand & contributing

- [Brand guidelines](brand/brand-guidelines.md)
- [Contributing](../../CONTRIBUTING.md)
- [Roadmap](../../ROADMAP.md)
