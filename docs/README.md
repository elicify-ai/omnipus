# Omnipus documentation

User and operator documentation, grouped by what you're trying to do. If a link is missing, it's because the doc doesn't exist yet — please open an issue.

For deep internal context (architecture audits, design decisions, in-flight specs, business requirements), see the [For contributors](#for-contributors) section at the bottom.

---

## Get started

- **Install (one-liner / Docker / source)** — [../README.md#install](../README.md#install)
- **First boot & onboarding** — [../README.md#first-boot](../README.md#first-boot)
- **Reverse-proxy & TLS** — [operations/reverse-proxy.md](operations/reverse-proxy.md)
- **Docker** — [docker.md](docker.md)
- **Platform support matrix** — [operations/platform-support.md](operations/platform-support.md)
- **Troubleshooting & debug** — [debug.md](debug.md), [troubleshooting.md](troubleshooting.md)

## Using Omnipus

- **Memory (sessions, retros, recall)** — [memory.md](memory.md)
- **Skills (ClawHub installs, SKILL.md format)** — [skills.md](skills.md)
- **Channels (15 chat platforms)** — [../pkg/channels/README.md](../pkg/channels/README.md)
- **Chat-apps configuration** — [chat-apps.md](chat-apps.md)
- **Hooks (subprocess + in-process)** — [hooks/README.md](hooks/README.md)
- **Antigravity provider** — [ANTIGRAVITY_USAGE.md](ANTIGRAVITY_USAGE.md)

## Configure & operate

- **All configuration keys** — [configuration.md](configuration.md)
- **Tool configuration & policies** — [tools_configuration.md](tools_configuration.md)
- **Provider configuration (24 LLM providers)** — [providers.md](providers.md)
- **Sandbox: config & status** — [operations/sandbox-config.md](operations/sandbox-config.md)
- **Sandbox: known limitations** — [operations/sandbox-limitations.md](operations/sandbox-limitations.md)
- **Security: configuration reference** — [security_configuration.md](security_configuration.md)
- **Security: operator considerations** — [operations/security-considerations.md](operations/security-considerations.md)
- **Credential vault (AES-256-GCM, Argon2id)** — [credential_encryption.md](credential_encryption.md)
- **Sensitive-data redaction in audit logs** — [sensitive_data_filtering.md](sensitive_data_filtering.md)
- **Config schema migration (legacy → current)** — [migration/model-list-migration.md](migration/model-list-migration.md)
- **Config versioning** — [config-versioning.md](config-versioning.md)

## Reference

- **All built-in tools (catalog)** — [tools-reference.md](tools-reference.md)
- **All LLM providers** — [providers.md](providers.md)
- **REST API contract** — [../contracts/openapi.yaml](../contracts/openapi.yaml)
- **WebSocket / SSE async contract** — [../contracts/asyncapi.yaml](../contracts/asyncapi.yaml)
- **WebSocket protocol guide** — [protocol/websocket-protocol.md](protocol/websocket-protocol.md)

---

## For contributors

Internal artifacts: architecture audits, design decisions, in-flight specs, and the business requirements that drive the product roadmap. These documents are written for people who plan to modify Omnipus, not for first-time users.

### Architecture

- [Agent loop flow](agent-loop-flow.md)
- [Steering (mid-turn redirection)](steering.md), [steering spec](design/steering-spec.md)
- [Sub-turn flow](subturn.md)
- [Spawn & sub-tasks](spawn-tasks.md)
- [AS-IS architecture audit (2026-04)](architecture/AS-IS-architecture.md) — evidence-based code walkthrough, dated; useful as a reference point but not kept in sync with day-to-day code changes
- [Plugin / extension landscape](architecture/plugin-extensibility-assessment.md)
- [Library refactor — impact assessment](architecture/library-refactor-impact-assessment.md), [risk analysis](architecture/library-refactor-risk-analysis.md)

### Architecture Decision Records (ADRs)

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

### Specs (under review or in flight)

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

### Future designs (v0.3 "Rooms")

Forward-looking designs not in v0.1 or v0.2:

- [Sandbox topology](design/sandbox-redesign-2026-05.md)
- [Memory (Dreamcatcher consolidation)](design/memory-redesign-2026-05.md)
- [Tasks (per-room scoping)](design/tasks-redesign-2026-05.md)
- [Projects UI](design/projects-ui-2026-05.md)
- [Settings & notifications](design/settings-notifications-2026-05.md)
- [Provider refactoring](design/provider-refactoring.md) ([tests](design/provider-refactoring-tests.md))

### Business requirements (intent of record)

The original product specs. When the code disagrees with the BRD, **the code wins** — but the BRD is still load-bearing context for understanding *why* a feature exists:

- [Main BRD](BRD/Omnipus%20BRD.md) — 30 security + 36 functional requirements, delivery phases
- [Appendix A — Windows kernel security](BRD/Omnipus%20Windows%20BRD%20appendic.md)
- [Appendix B — Feature parity](BRD/Omnipus_BRD_AppendixB_Feature_Parity.md)
- [Appendix C — UI / UX spec](BRD/Omnipus_BRD_AppendixC_UI_Spec.md)
- [Appendix D — System agent & system tools](BRD/Omnipus_BRD_AppendixD_System_Agent.md)
- [Appendix E — File-based data model](BRD/Omnipus_BRD_AppendixE_DataModel.md)

### Brand & contributing

- [Brand guidelines](brand/brand-guidelines.md)
- [Contributing](../CONTRIBUTING.md)
- [Roadmap](../ROADMAP.md)
