# Sandbox limitations and known gaps

The Omnipus process sandbox is deliberately scoped. It applies Landlock (filesystem and TCP port rules) plus a seccomp BPF filter to the gateway process itself before any HTTP listener binds, and inherits both layers to every forked child via Landlock's per-thread domain and `SECCOMP_FILTER_FLAG_TSYNC`. It does **not** attempt to be a container, a hypervisor, a network firewall, or a kernel-level network packet filter — destination-IP-level egress for compiled binaries spawned through `workspace.shell` is enforced in userspace by the SSRF checker and the egress proxy, not at the kernel layer. This page lists the specific places where enforcement degrades from kernel-level to application-level checks, or where a feature appears in the config but is unimplemented.

## Platforms without LSM enforcement

On macOS, Windows, and Linux kernels older than 5.13, `sandbox.SelectBackend()` returns a `FallbackBackend` and `applySandbox` follows the graceful-degradation path (`pkg/gateway/sandbox_apply.go:271-289`). The gateway logs `sandbox.degraded` at boot and continues serving with application-level path checks only.

### No Landlock filesystem confinement

On unsupported platforms, there is no Landlock filesystem confinement on the gateway or its children. Tool path-guard checks (`pkg/tools/path_audit.go`, the per-tool allow-list inside each builtin) are still enforced in Go, but a compromised tool that bypasses the Go check has no kernel net to catch it.

### No seccomp filter

The deny-list of dangerous syscalls (`ptrace`, `mount`, `bpf`, `kexec_load`, `init_module`, etc.) is not installed on unsupported platforms — see `pkg/sandbox/seccomp_linux.go:28-42` for the canonical list.

### No kernel-level port allow-list

`cfg.Sandbox.DevServerPortRange` is honored by the gateway's own dev-server registry but not by the kernel, so a compiled child can bind any port the OS permits.

### Windows kernel sandbox unimplemented

The Windows kernel-sandbox story (Job Objects + Restricted Tokens + DACL) described in `docs/internal/BRD/Omnipus Windows BRD appendic.md` is specified but not implemented. Windows currently uses `FallbackBackend`.

`/health` and `/api/v1/security/sandbox-status` both report `backend: "fallback"` and `kernel_level: false` in this configuration, and the field `disabled_by: "kernel_unsupported"` may appear when the operator asked for `enforce` but the kernel cannot deliver it.

## Permissive mode on kernels < 6.12

Linux did not gain a native permissive Landlock semantic until 6.12. On older kernels (which covers most production hosts as of mid-2026), `sandbox.mode = "permissive"` follows the audit-only degradation path in `pkg/sandbox/sandbox_linux.go:367-383`.

The Landlock ruleset is built and rules are added, so any malformed rule still surfaces at boot. `PR_SET_NO_NEW_PRIVS` is set (required for the seccomp filter). `landlock_restrict_self` is **deliberately skipped** — calling it would enforce the policy, the opposite of what the operator asked for. The seccomp BPF program is installed with `SECCOMP_RET_LOG` instead of `SECCOMP_RET_ERRNO(EPERM)` (`pkg/sandbox/seccomp_linux.go:64,139,148-153`), so blocked syscalls proceed but emit a kernel audit entry.

The gateway logs `sandbox.permissive.downgraded` at INFO with the kernel ABI version, and `PolicyApplied()` still returns `true` so the status endpoint correctly reports "policy was computed and logged". On kernel 6.12 and newer the path is true log-then-enforce. Plan for that kernel floor if you need permissive mode to behave as a real audit-only stepping stone before enforcement.

## SSRF allowlist limitations

`cfg.Sandbox.SSRF.AllowInternal` is the operator-controlled allow-list that exempts entries from SSRF blocking. The parser in `pkg/security/ssrf.go:95-117` accepts exactly three forms:

| Form | Examples |
|---|---|
| Exact IPv4 or IPv6 address | `127.0.0.1`, `::1`, `192.168.1.5` |
| CIDR range | `10.0.0.0/8`, `fc00::/7` |
| Exact hostname (case-insensitive) | `localhost`, `internal.corp` |

Glob host patterns are **not** supported. `*.internal.corp` will be stored as a literal hostname and will match nothing at lookup time. There is no plan to add glob support in v0.1; operators with many internal hosts should put them behind a single CIDR or enumerate them explicitly.

The CIDR allow-list interacts with the kernel layer asymmetrically. Landlock `NET_CONNECT_TCP` (ABI v4+) filters by destination port only — the kernel cannot match by IP. So a compiled binary spawned through `workspace.shell` that dials `https://192.168.1.1/` on port 443 is permitted by the kernel (443 is on the connect allow-list for legitimate HTTPS) and only the gateway-controlled HTTP clients (`web_search`, `web_fetch`, the skills installer, MCP fetches) see the CIDR filter via `SSRFChecker`. This gap is documented in `pkg/config/sandbox.go:300-310`; operators concerned about it should keep `experimental.workspace_shell_enabled = false` on agents that handle untrusted content.

## Production warning banner

When `OMNIPUS_ENV=production` is set in the environment and `sandbox.mode` resolves to `off` or `permissive`, the gateway prints a multi-line banner to stderr at boot and then every 60 seconds thereafter. The banner is intentionally loud and unmissable in journald or Docker logs.

Two variants exist in `pkg/gateway/sandbox_apply.go:163-183`.

### `productionNagBanner`

Fires when `mode=off` and `OMNIPUS_ENV=production` (`pkg/gateway/sandbox_apply.go:257-263`). Reads: `SANDBOX DISABLED IN PRODUCTION ENVIRONMENT`.

### `permissiveNagBanner`

Fires whenever `mode=permissive`, regardless of `OMNIPUS_ENV` (`pkg/gateway/sandbox_apply.go:417-428`). Reads: `SANDBOX IN PERMISSIVE MODE — NOT ENFORCED. DO NOT USE IN PRODUCTION.`

The 60-second repeat is implemented by `StartNagBanner` (`pkg/gateway/sandbox_apply.go:505-540`), a goroutine started after boot and stopped at shutdown. There is no environment variable, config flag, or runtime API that silences the banner — by design. The repeat interval is hardcoded; operators who find the banner spamming their logs should fix the underlying configuration rather than try to suppress the output.
