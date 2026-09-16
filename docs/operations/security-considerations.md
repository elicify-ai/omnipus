# Operator security considerations

## Single-listener preview boundary

The gateway serves the app, its application programming interface, and `/preview/` from one TCP listener. The default port is `5000`. There is no separate preview port or preview origin.

The app presents previews as links instead of embedding them in the app. An HttpOnly session cookie keeps the login token unavailable to previewed JavaScript. The preview proxy also removes reserved cookies from requests and responses.

---

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
`[18000, 18999]`) plus the gateway's own listener ports. Any `bind(2)` to a
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

## Kernel-enforced outbound port allow-list (raw TCP egress, v0.2 #155)

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

## `gateway.public_url` is required for strict embedding control

The CSP `frame-ancestors` directive controls which origins are permitted to embed the SPA in an `<iframe>`. The gateway derives this value from `gateway.public_url`.

When `gateway.host` is `0.0.0.0` and `gateway.public_url` is unset, the gateway cannot determine the canonical origin and falls back to `frame-ancestors '*'`, which allows any site to embed the SPA. This is acceptable in a trusted local network but is not recommended for internet-facing deployments.

To lock down embedding, set `public_url` in `~/.omnipus/config.json`:

```json
{
  "gateway": {
    "public_url": "https://omnipus.example.com"
  }
}
```

The gateway will then emit `frame-ancestors https://omnipus.example.com`.

---

## Master key backup

The credential store (`~/.omnipus/credentials.json`) is encrypted with a 256-bit key. Losing that key makes every stored secret — API keys, Connector tokens, webhook credentials — permanently inaccessible.

Back up the master key before decommissioning a server or moving the data directory. Follow your deployment's key rotation procedure before replacing it.

---

## Run by trusted users only

The gateway executes tool calls on behalf of the active agent and the user directing it. A user with chat access can instruct agents to read files, run shell commands (subject to tool policy), and make outbound HTTP requests. This is by design — the product is an agentic runtime.

Extend chat access only to users you trust with shell-level capabilities on the host. Treat instructions in fetched files and web pages as untrusted content.

---

## `tools.exec.allow_remote` removed

The old `allow_remote` field was removed. Shell access is now governed by each agent's `bash` tool policy: `allow`, `ask`, or `deny`. Set `bash: deny` for agents that must not execute shell commands from a Connector. The kernel boundary remains one global policy applied when the gateway starts.
