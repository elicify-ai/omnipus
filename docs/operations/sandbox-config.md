# Sandbox configuration

The Omnipus process sandbox is configured from the SPA at **Settings → Security → Process Sandbox** (the panel shows the active backend, mode toggle, allowed-paths editor, and SSRF policy preset in one place). For headless deployments and reverse-proxy hosts where the SPA is not the primary surface, the same fields are editable directly in `~/.omnipus/config.json` under the `sandbox` key. The CLI flag `--sandbox` is a runtime-only override that does not persist.

Note: prior to [ADR-035](../internal/architecture/ADR-035-remove-per-agent-sandbox-profile.md), this section also documented a `default_profile` field (a global fallback for a per-agent `sandbox_profile` setting). Both the per-agent field and its global fallback were removed — new agents no longer have a configurable sandbox profile, so there is nothing left for a default to apply to.

The gateway applies sandbox config exactly once, at boot. There is no hot-reload — changes to `sandbox.mode`, `sandbox.allowed_paths`, `sandbox.ssrf.*`, or any other field in this section take effect only after a gateway restart. This is documented invariant FR-J-015 (`pkg/gateway/sandbox_apply.go:32`); the SPA surfaces a "Restart required" banner whenever the saved-on-disk value diverges from the running process (`src/components/settings/SandboxSection.tsx:1015-1034`).

## Config file shape

```json
{
  "sandbox": {
    "mode": "enforce",
    "allowed_paths": [
      "/home/alice/projects"
    ],
    "ssrf": {
      "enabled": true,
      "allow_internal": [
        "127.0.0.1",
        "::1",
        "10.0.0.0/8"
      ]
    },
    "egress_allow_cidrs": [],
    "egress_allow_list": [
      "registry.npmjs.org",
      "github.com",
      "raw.githubusercontent.com"
    ],
    "max_concurrent_dev_servers": 4,
    "max_concurrent_builds": 2,
    "dev_server_port_range": [18000, 18999],
    "browser_evaluate_enabled": true,
    "skill_trust": "warn_unverified",
    "prompt_injection_level": "medium",
    "audit_log": true
  }
}
```

Field reference (defaults applied by the boot validator when the field is omitted):

| Field | Default | Notes |
|---|---|---|
| `mode` | `enforce` on a fresh install, otherwise the operator's last saved value | See "Modes" below. |
| `allowed_paths` | `[]` | Extra filesystem paths granted RW (read-write strip applied to `SystemRestrictedPaths`). |
| `ssrf.enabled` | `false` | Activates SSRF protection on gateway-controlled HTTP clients. |
| `ssrf.allow_internal` | `[]` | Exact IPs, CIDRs, or hostnames exempted from SSRF blocking (`pkg/security/ssrf.go:65-72`). |
| `egress_allow_cidrs` | `[]` | Extra CIDRs added to the SSRFChecker allow-list (`pkg/config/sandbox.go:286-314`). |
| `egress_allow_list` | `["registry.npmjs.org", "github.com", "raw.githubusercontent.com"]` | Host allow-list for the egress proxy used by Tier 2/Tier 3 children. |
| `max_concurrent_dev_servers` | `4` | Tier 3 `web_serve` dev-mode cap across all agents (`pkg/config/sandbox.go:233`). |
| `max_concurrent_builds` | `2` | Tier 2 `build_static` cap (`pkg/config/sandbox.go:237`). |
| `dev_server_port_range` | `[18000, 18999]` | Inclusive port range for Tier 3 (`web_serve` dev-mode) dev servers only. `bash` (ADR-036 unified the retired `exec`/`workspace_shell`/`workspace_shell_bg` tools into it) has no port-exposure capability — that capability was dropped, not ported, when `workspace_shell_bg` was merged (ADR-036 §3.1). Also feeds the Landlock bind/connect allow-list on ABI v4+. |
| `browser_evaluate_enabled` | `true` (seeded) | Installation-wide switch for `browser_evaluate` (arbitrary in-page JavaScript). **Seeded `true` on a fresh install**; which agents may call the tool is a separate question answered by each agent's tool policy (Jim holds the only agent-level grant on a fresh install). Setting it to `false` is an operator opt-out: the tool stays registered and visible to the model, but refuses at execution with a message naming this setting. **The only way to turn it off is to hand-edit `config.json` and restart** — neither Settings → Security nor the sandbox-config API can express this key. Note what the JavaScript runs against: a browser holding your live logins. |

Note: there is no `experimental.workspace_shell_enabled` feature flag. `bash` (ADR-036 unified the retired `exec`/`workspace_shell`/`workspace_shell_bg` tools into it) is registered for every agent regardless of sandbox mode and is governed exclusively by each agent's explicit tool-policy entry (CLAUDE.md hard constraint 6) — set the `bash` policy to `deny` or `ask` per agent to restrict it, there is no global gate to flip.

## Modes

| Mode | Boot behavior | Runtime behavior |
|---|---|---|
| `enforce` | Build Landlock ruleset, add rules, `PR_SET_NO_NEW_PRIVS`, `landlock_restrict_self`, install seccomp BPF with `SECCOMP_RET_ERRNO(EPERM)`. | Filesystem violations return `EACCES`; blocked syscalls return `EPERM`. Production default on capable kernels. |
| `permissive` | Build ruleset and add rules. Set `NNP`. **Skip** `landlock_restrict_self` on kernels < 6.12 (no native permissive Landlock). Install seccomp with `SECCOMP_RET_LOG`. | Policy is computed and audit-logged; nothing is blocked. `permissiveNagBanner` repeats every 60s. |
| `off` | No Apply, no Install. Logs `sandbox.disabled` at WARN. | No kernel enforcement. If `OMNIPUS_ENV=production`, `productionNagBanner` repeats every 60s. |

Source: `pkg/sandbox/sandbox.go:83-102`, `pkg/sandbox/sandbox_linux.go:367-407`, `pkg/gateway/sandbox_apply.go:247-263`.

A fresh install with no `sandbox.mode` written to disk and an empty `allowed_paths` is treated by `resolveMode` (`pkg/gateway/sandbox_apply.go:134-158`) as `enforce` on capable kernels. Once the operator saves any sandbox field, an empty `mode` is treated as the operator's explicit `off`.

## CLI override

```
./omnipus start --sandbox=enforce|permissive|off
```

The flag is parsed and validated in `cmd/omnipus/internal/gateway/command.go:23-49`. An invalid value (`--sandbox=of`) fails fast with a usage error (exit code 2) before any boot logic runs. The CLI flag takes precedence over the config file unconditionally — useful for one-shot debugging without persisting state. The status endpoint will report `disabled_by: "cli_flag"` when `--sandbox=off` overrode a config value.

## Status endpoints

### `GET /health`

Always returns a `sandbox` sub-object with `{applied, mode, backend}`. Additional fields are conditionally present: `disabled_by` (when `mode=off`), `audit_only` (when permissive), `landlock_enforced` and `seccomp_enforced` (when true). The closure that builds the response is in `pkg/gateway/sandbox_apply.go:471-497`; values are computed once at boot and never change.

Example payload (mode=enforce on Linux 6.8 with Landlock ABI v4):

```json
{
  "status": "ok",
  "sandbox": {
    "applied": true,
    "mode": "enforce",
    "backend": "landlock-v4",
    "landlock_enforced": true,
    "seccomp_enforced": true
  }
}
```

Example payload (mode=off in development):

```json
{
  "status": "ok",
  "sandbox": {
    "applied": false,
    "mode": "off",
    "backend": "landlock-v4",
    "disabled_by": "config"
  }
}
```

### `GET /api/v1/security/sandbox-status`

Admin-authenticated (`a.withAuth`, registered at `pkg/gateway/rest.go:2407`). Returns the full backend description plus the gateway-owned `ApplyState` and the count of installed bind-port rules. Handler: `pkg/gateway/rest_security_wave5.go:28-71`.

Example payload (mode=enforce, ABI v4, dev-server range 18000-18999):

```json
{
  "backend": "landlock-v4",
  "available": true,
  "kernel_level": true,
  "abi_version": 4,
  "blocked_syscalls": [
    "bpf", "delete_module", "finit_module", "init_module",
    "kexec_load", "mount", "perf_event_open", "pivot_root",
    "ptrace", "reboot", "swapoff", "swapon", "umount2"
  ],
  "seccomp_enabled": true,
  "landlock_features": ["truncate", "refer", "net_bind_tcp", "net_connect_tcp"],
  "policy_applied": true,
  "mode": "enforce",
  "landlock_enforced": true,
  "seccomp_enforced": true,
  "bind_ports_count": 1000
}
```

Field semantics are in `pkg/sandbox/sandbox.go:733-769`. `connect_ports_count` was removed in v0.1 (A1.3) because the kernel never enforced connect-port rules in earlier builds; the field has been deliberately not advertised.

## Boot failure modes

The exit-code contract is in `cmd/omnipus/internal/gateway/command.go:34-36` and `pkg/gateway/sandbox_apply.go:50-54`.

### Exit 78 (`EX_CONFIG`)

Apply or Install failed on a kernel that claims Landlock support (`linuxApplier` type assertion succeeded). The error path in `pkg/gateway/sandbox_apply.go:389-406` returns a `SandboxBootError` and the gateway main loop exits before any HTTP listener binds. External TCP probes see `ECONNREFUSED`, not HTTP 503.

### Exit 1

Any other boot failure (credential unlock, config load, port already in use, etc.).

### Exit 2

Usage error (invalid `--sandbox` value).

Graceful degradation is **not** a failure: an operator who asks for `enforce` on a pre-5.13 kernel gets a `FallbackBackend` and a `sandbox.degraded` log entry, and boot continues to completion.

## Restart-required UX

The SPA Sandbox section (`src/components/settings/SandboxSection.tsx`) compares each editable field to the running value reported by `/api/v1/security/sandbox-status` and renders a "Restart required" pill next to any field that diverges. The mode toggle, allowed-paths editor, and SSRF preset all participate.

Saving writes to disk via the existing config-update endpoint but does **not** trigger any in-process reload — the operator must run `systemctl restart omnipus` (or the equivalent for their deployment) for the new policy to take effect.
