# Security Considerations — Operator Guide

## Single-listener preview boundary

The web app, API, WebSocket connection, and `/preview/` routes share the gateway listener on port `5000`. Preview is a link that opens in a browser tab, not a second listener or embedded frame. Set `gateway.public_url` to the public HTTPS origin so preview links and origin checks use the address users actually reach. See [reverse proxy](reverse-proxy.md) for the routing and header contract.

## Bearer-token contract for `/preview/`

Preview URLs contain a time-limited bearer token embedded in the path. Anyone who has the URL can load the content until the token expires — the gateway does not require a logged-in session to serve preview responses. Both static-served directories (formerly `serve_workspace`) and dev-server processes (formerly `run_in_workspace`) are now reachable through the unified `/preview/<agent>/<token>/` route.

Operators who want tighter access control have two levers:

1. **Shorten the token lifetime** — lower `tools.serve_workspace.max_duration_seconds` from the default `86400` (24 hours) to a value appropriate for the deployment, for example `3600` (1 hour). Tokens issued after the change use the new duration; existing tokens are not revoked. (The config key retains the pre-unification name for back-compat with existing operator configs; the `serve_workspace` key controls both static and dev-mode preview durations.)

2. **Treat preview URLs as secrets** — avoid sharing a preview URL outside the trusted user who triggered the agent turn that generated it. The URL itself is the credential for that preview.

There is no per-token revocation endpoint in the current release. To invalidate all outstanding tokens, restart the gateway (tokens are stored in memory and are not persisted).

---

## Kernel-enforced bind-port allow-list

On Linux kernels with Landlock ABI v4 (kernel 6.8 and later; see
`pkg/sandbox/sandbox_linux.go:67` — "Landlock ABI v4 network access rights
(kernel 6.8+)"), the gateway and every child process it spawns are restricted
to binding TCP ports inside `cfg.Sandbox.DevServerPortRange` (default
`[18000, 18999]`) plus the gateway listener on port `5000`. Any `bind(2)` to a
port outside that allow-list returns `EACCES` from the kernel — including
`bind(0.0.0.0:5173)` from a shell-spawned dev server.

This means an agent calling `bash` with `npx vite --host 0.0.0.0 --port 5173` will
fail at the bind syscall, regardless of the agent's tool policy. The only
legal way for an agent to expose a website is through the `web_serve` tool,
which auto-picks a port from the allow-listed range and routes traffic
through `/preview/<agent>/<token>/` with the bearer-token contract above.

**Graceful degradation:** on kernels with Landlock ABI < 4 (no `NET_BIND_TCP`),
this enforcement is silently inactive. The gateway probes runtime capability
at boot via `probeLandlockABIPlatform()` (`pkg/sandbox/sandbox_linux.go:132`)
and reports `landlock_enforced: false` on the `/health` endpoint when the
feature is absent. Operators on such kernels still get tool-layer port-range
validation (the `web_serve` tool refuses out-of-range ports), but a
shell-spawned process can technically bind anywhere. Plan to upgrade to kernel
6.8+ for the full enforcement story.

---

## Kernel-enforced outbound port allow-list (raw TCP egress, #155)

On Linux kernels with Landlock ABI v4 (kernel 6.8 and later for the gateway's tested baseline; see `pkg/sandbox/sandbox_linux.go:67`), `connect(2)` from the gateway and every forked child is restricted to the union of:

- **`{53, 80, 443}`** — DNS, HTTP, HTTPS. The minimal set required by the gateway's outbound LLM/provider calls and the egress proxy's upstream connections. Defined as `sandbox.DefaultConnectPorts` in `pkg/sandbox/sandbox.go`.
- **`cfg.Sandbox.DevServerPortRange`** — every port in the configured range (default `[18000, 18999]`). Lets children connect back to gateway-owned dev servers, the egress proxy, and other agents' running web_serve sessions.

Any `connect(2)` to a destination port outside that union returns `EACCES` from the kernel. This closes the raw-TCP-egress hole documented as threat **C4** in the insider-pentest report: a child issuing `socket() + connect()` directly (bypassing `HTTP_PROXY` env vars) can no longer reach unauthorised ports — including SSH (22), MySQL (3306), Redis (6379), or arbitrary backdoor channels on uncommon ports such as the redteam test target `127.0.0.1:1`.

**Limitations of this control — read carefully.** The kernel mechanism (Landlock `NET_CONNECT_TCP`) is **port-level only**. It cannot filter by destination IP or CIDR. Specifically:

- A child can still `connect()` to **`192.168.1.1:443`**, **`10.0.0.5:443`**, or **`169.254.169.254:80`** (cloud metadata, on AWS/GCP) — the destination port is allowed for legitimate HTTPS traffic, and the kernel does not inspect the IP.
- For HTTP clients the **gateway** controls (`pkg/web` search clients, MCP fetches, the skills installer), CIDR-level blocking is enforced by `pkg/security/SSRFChecker` when `cfg.Sandbox.SSRF.Enabled = true`. Operators with a legitimate internal-service requirement add CIDRs to `cfg.Sandbox.EgressAllowCIDRs` (or the SSRF allow_internal list) to bypass the deny.
- For compiled binaries spawned via `bash` (ADR-036 merged the former `exec`/`workspace_shell`/`workspace_shell_bg` into `bash`; e.g. `curl`, `wget`, custom Go programs), the SSRFChecker does not apply — only the kernel port allow-list does. CIDR-level enforcement for these binaries would require eBPF cgroup `BPF_CGROUP_INET4_CONNECT` and is deferred. Operators concerned about RFC1918 access from compiled children should set `bash`'s tool-policy to `deny` or `ask` (CLAUDE.md hard constraint 6 — `bash` has no feature-flag gate, only an explicit per-agent tool-policy entry) on agents that handle untrusted content.

**Graceful degradation:** on kernels with Landlock ABI < 4 (no `NET_CONNECT_TCP`), the connect rules are computed but ignored by the kernel. The `pkg/security/SSRFChecker` Go-side defence still runs for HTTP clients the gateway controls; raw-TCP egress is unrestricted on these older kernels. Plan to upgrade to kernel 6.8+ for full kernel enforcement.

---

## `gateway.public_url` sets the canonical origin

Set `public_url` in `~/.omnipus/config.json` when users reach the gateway through a reverse proxy:

```json
{
  "gateway": {
    "public_url": "https://omnipus.example.com"
  }
}
```

The gateway uses this value for preview URLs, Cross-Origin Resource Sharing, WebSocket origin checks, and Content Security Policy. A change requires a restart.

---

## Master key backup

The credential store (`~/.omnipus/credentials.json`) is encrypted with a 256-bit key. Losing that key makes every stored secret — API keys, channel tokens, webhook credentials — permanently inaccessible.

Key provisioning priority, rotation procedure, and the auto-generate first-boot behavior are documented in [ADR-004](../internal/architecture/ADR-004-credential-boot-contract.md#master-key-provisioning). Follow the key rotation steps there before decommissioning a server or moving the data directory.

### Master key provisioning

The gateway tries master-key sources in this order and stops at the first one that succeeds:

| Priority | Source | Operator requirement |
|---|---|---|
| 1 | `OMNIPUS_MASTER_KEY` | Supply a 64-character hexadecimal key. |
| 2 | `OMNIPUS_KEY_FILE` | Point to a regular file containing that key with mode `0600`. |
| 3 | `$OMNIPUS_HOME/master.key` | Keep the generated or installed file at mode `0600`. |
| 4 | Fresh-install generation | Available only when neither a key nor `credentials.json` already exists. |
| 5 | Interactive passphrase | Requires an attached terminal. |

The encrypted store is `$OMNIPUS_HOME/credentials.json`. Omnipus uses AES-256-GCM and writes the store with mode `0600`. An existing store with no usable key causes startup to fail. Omnipus never generates a replacement key over existing encrypted data.

For unattended deployments, provision `OMNIPUS_MASTER_KEY` or `OMNIPUS_KEY_FILE`. Back up the corresponding key separately from `credentials.json`. A copy of the encrypted store without its key cannot be recovered.

## Sensitive-value filtering

Resolved credential values are registered at startup and again whenever the credential set changes (configuration reload, settings save, provider sign-in). Omnipus uses that complete current set to scrub tool results and other selected content before it reaches a model. It is not applied to the audit log; see "Audit log redaction" below, which matches credential formats, not your registered values.

Filtering is best-effort and can be switched off. Configure it under `tools` in `config.json`:

```json
{
  "tools": {
    "filter_sensitive_data": true,
    "filter_min_length": 8
  }
}
```

`filter_sensitive_data` defaults to `true`. `filter_min_length` defaults to `8`; shorter content bypasses this filter. The matching environment variables are `OMNIPUS_TOOLS_FILTER_SENSITIVE_DATA` and `OMNIPUS_TOOLS_FILTER_MIN_LENGTH`.

This filter only covers credential values Omnipus has registered. It does not recognise credential formats, and it does not discover every secret in arbitrary text. Treat any suspected exposure as real and rotate the affected credential.

### Audit log redaction

Separately from the filter above, the audit logger redacts every entry before it writes it: the command, parameters and details of tool-call and shell entries, and the fields of structured event records, pass through the credential patterns (including nested maps and lists of any type). The patterns cover `sk-…` and `key-…` keys, `Bearer …` tokens (any letter case), GitHub `ghp_…`/`gho_…` tokens, Slack `xoxb-…`/`xoxp-…` tokens, AWS access-key IDs (`AKIA…`/`ASIA…`), Google `ya29.…` access tokens, JSON Web Tokens, and the password in a URL such as `postgres://user:PASSWORD@host` or `redis://:PASSWORD@host` (only the password is replaced; a raw `/` or `@` inside the password, and scheme-less `user:pw@tcp(…)` strings, are not caught). A key prefix directly after a letter or digit is not treated as a key, so names like `project-task-…` or `risk-assessment` are left intact; after any other character, including an encoded one such as `%3D`, `\u0022` or a literal `\n`, it is. A path segment or skill slug that itself starts like a key (for example `/sk-learn-…`) is therefore redacted, so last-writer and last-invoked lookups return not-found for it. A value whose field name is exactly one of a short list (`password`, `token`, `api_key`, `secret`, `authorization` and similar; case, `_` and `-` are ignored) becomes `[REDACTED]`; a field such as `github_token` is not caught by name. There is no setting to turn it off. Redaction runs before the entry is signed, so the tamper-evident chain covers the redacted text and still verifies. Security-setting-change records keep their own, broader name-based redaction (`***redacted***`) and also pass through the credential patterns. Email addresses are deliberately not redacted in the audit log, so mail recipients and Message-IDs are recorded in full.

When an audit write fails, any gateway log line that includes the command carries it with the same credential patterns applied; most such lines do not include the command at all.

Audit files written before this was switched on (issue #914) are not rewritten — rewriting them would break the signature chain. They may contain credentials typed into `bash`, `web_serve` or `environment_setup` commands; rotate those credentials if that matters.

---

## Run by trusted users only

The gateway executes tool calls on behalf of the active agent and the user directing it. A user with chat access can instruct agents to read files, run shell commands (subject to tool policy), and make outbound HTTP requests. This is by design — the product is an agentic runtime.

Operators should extend chat access only to users they trust with shell-level capabilities on the host. T-06 in the [Threat Model](../internal/specs/chat-served-iframe-preview-spec.md#threat-model) covers the trusted-prompt boundary and what happens when an agent receives instructions from untrusted content (for example, an HTML file fetched from the web).

---

## Shell access from connectors

Shell access is governed by the `bash` tool policy, regardless of whether a request arrives in the web app or through a connector. Set `bash` to Deny for agents that must not run commands, or Ask when a person will be present to approve each call.
