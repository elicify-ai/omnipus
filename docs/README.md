# Omnipus documentation

User and operator documentation, grouped by what you're trying to do. If a link
is missing, it's because the doc doesn't exist yet — please open an issue.

> **Building or modifying Omnipus?** Developer and architecture documentation —
> architecture audits, ADRs, in-flight specs, future designs, and the business
> requirements — lives under [`internal/`](internal/README.md).

---

## ▶ Start here

New to Omnipus? Read these in order.

- **Your first 10 minutes** — [getting-started.md](getting-started.md) — install → first conversation → handoff → build an agent
- **How Omnipus works (plain English)** — [concepts.md](concepts.md) — the agents, sessions, memory, channels, skills
- **Using the web app** — [using-omnipus-ui.md](using-omnipus-ui.md) — a task-based tour of the browser app
- **Using the terminal / CLI** — [using-omnipus-cli.md](using-omnipus-cli.md) — run and chat from the command line

## Install & run

- **Install (one-liner / Docker / source)** — [../README.md#install](../README.md#install)
- **First boot & onboarding** — [../README.md#first-boot](../README.md#first-boot)
- **Reverse-proxy & TLS** — [operations/reverse-proxy.md](operations/reverse-proxy.md)
- **Docker** — [docker.md](docker.md)
- **Platform support matrix** — [operations/platform-support.md](operations/platform-support.md)
- **Troubleshooting & debug** — [debug.md](debug.md), [troubleshooting.md](troubleshooting.md)

## Using Omnipus

- **Memory (auto-recap, idle retros, search)** — [memory.md](memory.md)
- **Channel-to-agent routing & handoff** — [routing.md](routing.md)
- **Session history & event stream** — [observability.md](observability.md)
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

For internal/contributor documentation, see [`internal/README.md`](internal/README.md).
