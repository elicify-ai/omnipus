# Omnipus documentation

The README is the marketing front door. This index is the working operator's table of contents — everything in `docs/` grouped by reader intent.

If a document name uses a bracketed link, the file exists today. Plain text indicates a doc that is on the roadmap but not yet written.

## Get started

- **Install** — [README §Install](../README.md#install) (one-liner, Docker, source)
- **First boot & onboarding** — [README §First boot](../README.md#first-boot)
- **Reverse-proxy & TLS** — [operations/reverse-proxy.md](operations/reverse-proxy.md)
- **Troubleshooting** — [troubleshooting.md](troubleshooting.md)

## How it works

- **Architecture overview (current)** — [architecture/AS-IS-architecture.md](architecture/AS-IS-architecture.md)
- **Plugin / extension landscape** — [architecture/plugin-extensibility-assessment.md](architecture/plugin-extensibility-assessment.md)
- **The agent loop** — [agent-loop-flow.md](agent-loop-flow.md)
- **Memory (sessions, retros, recall)** — [memory.md](memory.md)
- **Channels (15 chat platforms)** — [../pkg/channels/README.md](../pkg/channels/README.md)
- **Hooks (subprocess + in-process)** — [hooks/README.md](hooks/README.md)
- **Tools registry & MCP** — [tools_configuration.md](tools_configuration.md), [specs/tool-registry-redesign-spec.md](specs/tool-registry-redesign-spec.md)
- **Skills (ClawHub)** — [skills.md](skills.md)
- **Spawn & sub-tasks** — [spawn-tasks.md](spawn-tasks.md)
- **Steering (mid-turn redirection)** — [steering.md](steering.md), [design/steering-spec.md](design/steering-spec.md)
- **Sub-turn flow** — [subturn.md](subturn.md)

## Security

- **Sandbox: config & status** — [operations/sandbox-config.md](operations/sandbox-config.md)
- **Sandbox: known limitations** — [operations/sandbox-limitations.md](operations/sandbox-limitations.md)
- **Credential vault (AES-256-GCM, Argon2id)** — [credential_encryption.md](credential_encryption.md)
- **Sensitive-data redaction (audit logs)** — [sensitive_data_filtering.md](sensitive_data_filtering.md)
- **Security configuration reference** — [security_configuration.md](security_configuration.md)
- **Security considerations for operators** — [operations/security-considerations.md](operations/security-considerations.md)
- **Architecture decisions (ADRs, 16 entries)** — [architecture/](architecture/) — search for `ADR-*`

## Operations

- **Reverse-proxy & TLS** — [operations/reverse-proxy.md](operations/reverse-proxy.md)
- **Platform support matrix** — [operations/platform-support.md](operations/platform-support.md)
- **Docker** — [docker.md](docker.md)
- **Sandbox limitations** — [operations/sandbox-limitations.md](operations/sandbox-limitations.md)
- **Debug & log spelunking** — [debug.md](debug.md)
- **Troubleshooting common issues** — [troubleshooting.md](troubleshooting.md)
- **Migration (older config schemas)** — [migration/model-list-migration.md](migration/model-list-migration.md)

## Reference

- **All built-in tools (catalog)** — [tools-reference.md](tools-reference.md)
- **All LLM providers** — [providers.md](providers.md)
- **Configuration keys** — [configuration.md](configuration.md)
- **Config versioning** — [config-versioning.md](config-versioning.md)
- **Tool configuration** — [tools_configuration.md](tools_configuration.md)
- **WebSocket protocol** — [protocol/websocket-protocol.md](protocol/websocket-protocol.md)
- **REST API contract** — [../contracts/openapi.yaml](../contracts/openapi.yaml)
- **WebSocket / SSE async contract** — [../contracts/asyncapi.yaml](../contracts/asyncapi.yaml)
- **Chat-apps integration** — [chat-apps.md](chat-apps.md)
- **Antigravity provider usage** — [ANTIGRAVITY_USAGE.md](ANTIGRAVITY_USAGE.md)

## Architecture decisions (ADRs)

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

Plus [library-refactor-impact-assessment.md](architecture/library-refactor-impact-assessment.md) and [library-refactor-risk-analysis.md](architecture/library-refactor-risk-analysis.md) for the cross-cutting library refactor.

## Active specs & future designs

What's being designed, reviewed, or queued — not yet implemented:

- **Specs (under review or in flight)** — [specs/](specs/)
  - [Browser automation wiring](specs/browser-automation-wiring-spec.md)
  - [Cancel across channels](specs/cancel-cross-channel-spec.md) ([review](specs/cancel-cross-channel-spec-review.md))
  - [Chat-served iframe preview](specs/chat-served-iframe-preview-spec.md)
  - [Device pairing](specs/device-pairing-spec.md)
  - [Joined session store](specs/joined-session-store-spec.md)
  - [Policy-change approval](specs/policy-change-approval-spec.md)
  - [RBAC granularity](specs/rbac-granularity-spec.md)
  - [Tool registry redesign](specs/tool-registry-redesign-spec.md) ([review](specs/tool-registry-redesign-spec-review.md))
  - [Env-awareness & memory](specs/env-awareness-and-memory-spec.md) ([review](specs/env-awareness-and-memory-spec-review.md))
- **Future designs (v0.3 "Rooms")** — [design/](design/)
  - [Sandbox topology](design/sandbox-redesign-2026-05.md)
  - [Memory (Dreamcatcher consolidation)](design/memory-redesign-2026-05.md)
  - [Tasks (per-room scoping)](design/tasks-redesign-2026-05.md)
  - [Projects UI](design/projects-ui-2026-05.md)
  - [Settings & notifications](design/settings-notifications-2026-05.md)
  - [Provider refactoring](design/provider-refactoring.md) ([tests](design/provider-refactoring-tests.md))

## Business requirements (intent of record)

Original product specs. When the code disagrees with the BRD, the code wins — but the BRD is still load-bearing context for understanding *why* a feature exists.

- [Main BRD](BRD/Omnipus%20BRD.md) — 30 security + 36 functional requirements, delivery phases
- [Appendix A — Windows kernel security](BRD/Omnipus%20Windows%20BRD%20appendic.md) — Job Objects, Restricted Tokens, DACL
- [Appendix B — Feature parity](BRD/Omnipus_BRD_AppendixB_Feature_Parity.md)
- [Appendix C — UI / UX spec](BRD/Omnipus_BRD_AppendixC_UI_Spec.md)
- [Appendix D — System agent & system tools](BRD/Omnipus_BRD_AppendixD_System_Agent.md)
- [Appendix E — File-based data model](BRD/Omnipus_BRD_AppendixE_DataModel.md)

## Brand & contributing

- **Brand guidelines** — [brand/brand-guidelines.md](brand/brand-guidelines.md)
- **Contributing** — [../CONTRIBUTING.md](../CONTRIBUTING.md)
- **Roadmap** — [../ROADMAP.md](../ROADMAP.md)
- **Code of conduct** — *(planned)*
