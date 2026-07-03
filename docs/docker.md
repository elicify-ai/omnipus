# Docker — Operator Guide

> Back to [README](../README.md)

## What runs in the container

Omnipus is a **single binary** (`omnipus`) with the React SPA compiled in via `go:embed`. It opens two TCP listeners:

| Listener | Default port | Purpose |
|---|---|---|
| Main | `5000` | SPA, REST API, WebSocket/SSE chat |
| Preview | `5001` (main + 1) | Agent-generated HTML previews (isolated origin) |

Ports are read from `config.json` (`gateway.port`, `gateway.preview_port`) or the environment variables `OMNIPUS_GATEWAY_PORT` / `OMNIPUS_GATEWAY_PREVIEW_PORT`. The preview port auto-derives to `gateway.port + 1` when not set explicitly.

All Omnipus data — `config.json`, `master.key`, sessions, tasks, audit log — lives under `~/.omnipus/` (i.e. `/root/.omnipus/` in the container as root, or the path set by `OMNIPUS_HOME`).

---

## Image variants

Three `Dockerfile`s ship in `docker/`. Pick the variant that matches the agent tools you intend to run.

| Variant | Dockerfile | Built size | Browser tools | Python MCP | Use case |
|---|---|---|---|---|---|
| **Minimal** (published) | [`docker/Dockerfile`](../docker/Dockerfile) | ~71 MB | ❌ | ❌ | Chat, channels, file/exec tools. The image published as `ghcr.io/elicify-ai/omnipus:latest`. |
| **Heavy** (build-it-yourself) | [`docker/Dockerfile.heavy`](../docker/Dockerfile.heavy) | ~1.08 GB | ✅ apk Chromium | ✅ uv/uvx + python3 | `browser.*` tools, `web_serve` dev-server preview, `agent-browser`, Python MCP servers. |
| **Full** (vestigial) | `docker/Dockerfile.full` | — | — | — | Inherited from upstream, not maintained as a v0.1 product. See [Vestigial files](#vestigial-files). |

All variants share the same three-stage build internally: `node:24-alpine` compiles the SPA → `golang:1.26.3-alpine` embeds it and builds the binary → a runtime stage layers on what's needed (`alpine:3.23` for minimal, `node:24-alpine3.23` + chromium/python/uv for heavy). The Go binary itself is identical across variants.

### Minimal image — what it can and can't do

The minimal image (`ghcr.io/elicify-ai/omnipus:latest`) **deliberately omits Chromium** to stay small. Without Chromium, the entire `browser.*` tool family (`browser.navigate`, `browser.screenshot`, `browser.read_content`, `browser.console_logs`, `browser.action`) will not work, nor will `web_serve` dev-server previews (the iframe-preview feature on the chat surface) or any custom skill or MCP server that shells out to a system chromium.

The gateway falls through to its managed-Chromium download path on first call, but the downloaded binary is glibc-linked and Alpine is musl — `exec` returns a misleading `no such file or directory` (the missing ELF interpreter is `/lib64/ld-linux-x86-64.so.2`, not the binary itself). The Max agent gracefully degrades to `web_fetch` and surfaces the failure inline in chat.

**If you need browser tools, use the heavy image.** Adding `apk add chromium` to a derived `FROM ghcr.io/elicify-ai/omnipus:latest` works too — the gateway's PATH lookup (`pkg/tools/browser/manager.go::resolveExecPath`) picks up `/usr/bin/chromium-browser` automatically once installed.

### Building the heavy image

```bash
docker build -t omnipus:heavy -f docker/Dockerfile.heavy .
```

Then run it like the minimal image, but bind the data volume to `/home/omnipus/.omnipus` (heavy runs as UID 1000 like the local-build minimal):

```bash
docker run -d \
  -p 127.0.0.1:5000:5000 \
  -p 127.0.0.1:5001:5001 \
  -v "$PWD/data:/home/omnipus/.omnipus" \
  omnipus:heavy
```

Onboarding flow, sandbox behaviour, and the rest of this guide apply identically once the container is up.

---

## Release image vs. dev image

| Image | Source | Entrypoint |
|---|---|---|
| `ghcr.io/elicify-ai/omnipus:latest` | Built by goreleaser on release | `docker/entrypoint.sh` |
| Local build (`docker build -f docker/Dockerfile .`) | Multi-stage SPA + Go build | `omnipus start` directly |
| Local build (`docker build -f docker/Dockerfile.heavy .`) | Same SPA + Go build, chromium + python + uv runtime | `omnipus start` directly |

The release image uses `entrypoint.sh`, which is now a thin `exec omnipus gateway "$@"` wrapper (no first-run gate). Locally-built images (minimal and heavy) start the gateway directly — they self-bootstrap on the first run.

---

## Before you start

> The release image (`ghcr.io/elicify-ai/omnipus:latest`) starts the gateway directly on every boot. The previous first-run gate that called `omnipus onboard` and exited was removed (see `docker/entrypoint.sh`); onboarding is now driven from the web UI at `/onboarding` (visit once the gateway is up) or from `docker exec -it <ctr> omnipus onboard`. The default `CMD` includes `--allow-empty` so the gateway comes up before any provider is configured — the SPA onboarding wizard adds the first one. See [First-run behavior](#first-run-behavior) for the full explanation.

---

## Quick start — docker run

```bash
# Create a local data directory first.
mkdir -p ./data

docker run -d \
  -p 127.0.0.1:5000:5000 \
  -p 127.0.0.1:5001:5001 \
  -v "$PWD/data:/root/.omnipus" \
  ghcr.io/elicify-ai/omnipus:latest

# Open the onboarding wizard
open http://localhost:5000
```

The onboarding wizard (at `/onboarding`) walks through: provider selection, API key entry, model selection, and account creation. Complete it before using the chat UI.

> Port `5000` is widely used by other local dev servers (macOS AirPlay receiver, Flask defaults, etc.) and `3000` is the Next.js default. If the host port collides, pick a free port (e.g. `5050`) and use it consistently in `config.json`, the `-p` flag, and the URL you open.

To override the port via environment instead of editing `config.json`:

```bash
docker run -d \
  -p 127.0.0.1:5050:5050 \
  -p 127.0.0.1:5051:5051 \
  -v "$PWD/data:/root/.omnipus" \
  -e OMNIPUS_GATEWAY_PORT=5050 \
  -e OMNIPUS_GATEWAY_PREVIEW_PORT=5051 \
  ghcr.io/elicify-ai/omnipus:latest
```

---

## Quick start — docker compose

The compose file is at `docker/docker-compose.yml`. Run from the repo root:

```bash
docker compose -f docker/docker-compose.yml --profile gateway up -d
```

The compose file maps `127.0.0.1:5000:5000` and `127.0.0.1:5001:5001` and sets `OMNIPUS_GATEWAY_PORT=5000` + `OMNIPUS_GATEWAY_PREVIEW_PORT=5001` on the gateway service, so the in-container listener matches the host port out of the box. The gateway self-bootstraps on first run (seeded port `5000`, no providers) — the onboarding wizard at `/onboarding` adds the first provider.

Check logs:

```bash
docker compose -f docker/docker-compose.yml logs -f omnipus-gateway
```

Stop:

```bash
docker compose -f docker/docker-compose.yml --profile gateway down
```

### Agent profile (one-shot query)

The `agent` profile runs a single query and exits:

```bash
docker compose -f docker/docker-compose.yml run --rm omnipus-agent -m "What is 2+2?"
```

---

## First-run behavior

The release image (`ghcr.io/elicify-ai/omnipus:latest`) uses `docker/entrypoint.sh`, which has been simplified to a single line:

```sh
exec omnipus gateway "$@"
```

There is **no first-run gate**. The container boots straight into the gateway on every start. The image's `CMD` includes `--allow-empty` so the gateway comes up even before any provider is configured — the SPA onboarding wizard at `/onboarding` adds the first one. If you prefer to drive onboarding from the terminal, run `docker exec -it <ctr> omnipus onboard` against the running container.

The previous first-run gate (which called `omnipus onboard` and exited before the gateway could start) was removed; that command was a print-only stub. See issue #159 for the history.

The local-build images (`docker/Dockerfile`, `docker/Dockerfile.heavy`) don't use `entrypoint.sh` — they `exec` the `omnipus` binary directly with `CMD ["gateway", "--allow-empty"]`. On the first start the gateway:

1. Calls `datamodel.Init()`, which creates `~/.omnipus/` subdirectories and writes a minimal `config.json` (seeded port `5000`, no providers).
2. Boots into onboarding mode. Visit `http://localhost:5000/onboarding` (the seeded port) to complete setup, or override via `OMNIPUS_GATEWAY_PORT`.

---

## Volume layout

The data path inside the container depends on which image you use:

| Image | Runs as | Data path inside container |
|---|---|---|
| `ghcr.io/elicify-ai/omnipus:latest` (release) | `root` | `/root/.omnipus` |
| Local build (`docker build -f docker/Dockerfile .`) | `omnipus` (UID 1000) | `/home/omnipus/.omnipus` |
| Local build (`docker build -f docker/Dockerfile.heavy .`) | `omnipus` (UID 1000) | `/home/omnipus/.omnipus` |

The `docker run` and compose examples above use `/root/.omnipus` because they target the release image. If you bind-mount against a locally-built image, change the right-hand side accordingly:

```bash
docker run -d \
  -p 127.0.0.1:5000:5000 \
  -p 127.0.0.1:5001:5001 \
  -v "$PWD/data:/home/omnipus/.omnipus" \
  omnipus:local
```

(The local-build image self-bootstraps via `datamodel.Init()` and uses the seeded port `5000` unless overridden.)

Either way, the directory layout is the same:

| Sub-path | Contents |
|---|---|
| `config.json` | Main config: providers, agents, gateway, channels |
| `master.key` | AES-256 encryption key (0600). **Back this up.** |
| `credentials.json` | Encrypted credential store (AES-256-GCM + Argon2id) |
| `sessions/` | Day-partitioned JSONL transcripts |
| `tasks/` | Per-task JSON files |
| `agents/` | Custom agent definitions (AGENT.md + SOUL.md) |
| `logs/` | `gateway.log`, `gateway_panic.log` |

Mount the entire data directory as a single named volume or bind-mount. Splitting sub-paths into separate mounts is not supported.

**Back up `master.key`.** Losing it makes every credential in `credentials.json` permanently unrecoverable. For headless deployments, inject the key via `OMNIPUS_MASTER_KEY` (64-char hex) or `OMNIPUS_KEY_FILE` (path to a 0600 file) instead of relying on the auto-generated key file.

---

## Pulling from GHCR

```bash
docker pull ghcr.io/elicify-ai/omnipus:latest

# Pin to a specific release:
docker pull ghcr.io/elicify-ai/omnipus:v0.1.0
```

---

## Building the image locally

The `docker/Dockerfile` is a multi-stage build that compiles the SPA and the Go binary in one step. Run from the repo root (not from `docker/`):

```bash
docker build -f docker/Dockerfile -t omnipus:local .
```

The build is three stages: a Node stage that builds the React SPA into `dist/spa/`, a Go stage that copies the SPA output into `pkg/gateway/spa/` before `go build` (satisfying the `//go:embed all:spa` directive), and a minimal Alpine runtime. Check `docker/Dockerfile` for current toolchain versions — they move with upstream.

Build arguments are not required. The image installs `ca-certificates`, `tzdata`, and `curl` for the health check.

The local-build image runs as a non-root user `omnipus` (UID 1000), so bind-mounts must target `/home/omnipus/.omnipus` rather than `/root/.omnipus` — see [Volume layout](#volume-layout).

---

## Port exposure and security

By default, both the compose file and the recommended `docker run` command bind to `127.0.0.1`, making the gateway reachable only from localhost. This is intentional: port 5000 (or whichever port you configure) serves the authenticated API and the onboarding wizard. Anyone who can reach the port before onboarding completes can claim the account.

To expose the gateway to a LAN or public IP, change the bind address:

```bash
docker run -d \
  -p 0.0.0.0:5000:5000 \
  -p 0.0.0.0:5001:5001 \
  -v "$PWD/data:/root/.omnipus" \
  ghcr.io/elicify-ai/omnipus:latest
```

Do this only after onboarding is complete and a strong admin password is set.

### Three Flags Every Docker Operator Should Set Deliberately

**`gateway.sandbox.mode`** (config.json) or CLI `--sandbox=enforce|permissive|off`. Defaults to `enforce` when the kernel supports Landlock + seccomp; falls back automatically otherwise. Verify after first boot with `omnipus doctor` (or `gateway.log` — see [Sandbox under Docker](#sandbox-under-docker)). See [docs/operations/security-considerations.md](operations/security-considerations.md) for the full threat model.

**`gateway.trust_xff`** — leave `false` unless you front the container with a reverse proxy. When `true`, the gateway honours `X-Forwarded-For` for audit logs and rate-limit keys. Setting it `true` without a trusted proxy lets any client spoof their IP. See [reverse-proxy.md](operations/reverse-proxy.md).

**`gateway.dev_mode_bypass`** — **never set this `true` in any Docker deployment.** It disables auth on routes that haven't completed onboarding and is intended for unit-test scaffolding only.

---

## Sandbox under Docker

Omnipus's kernel sandbox uses Landlock + seccomp on Linux 5.13+ to constrain the gateway process itself (see CLAUDE.md hard-constraint #4 and [security-considerations.md](operations/security-considerations.md)). Docker's defaults interact with this in non-obvious ways:

| Concern | What to do |
|---|---|
| **Kernel version** | The kernel inside the container is the host's kernel. Linux 5.13+ is required for full enforcement. Docker Desktop on macOS/Windows uses a managed Linux VM that is typically supported; on a stock Ubuntu 20.04 host (kernel 5.4) the sandbox falls back to app-level checks. |
| **Default seccomp profile** | Docker 23+ defaults allow the `landlock_*` and `seccomp(2)` syscalls. Older Docker daemons may need `--security-opt seccomp=<profile>` with a profile that permits these. |
| **`no-new-privileges`** | Not required but recommended; nested seccomp installation works either way in current Omnipus. |
| **`--privileged`** | **Defeats the sandbox.** Don't run the container privileged. |
| **`--security-opt seccomp=unconfined`** | Disables Docker's seccomp filter but Omnipus's own seccomp filter still installs. Acceptable for debugging, not for production. If you must use it, also set `gateway.sandbox.mode = "off"` so the degraded posture is explicit in the config. |
| **Read-only rootfs** | Omnipus reads `/proc/self/status` to detect capabilities; this works with `--read-only` as long as `/proc` remains mounted (the default). |

**Verifying after start.** Tail `gateway.log` (in the `logs/` subdirectory of the data volume) for the boot line:

```
sandbox initialized: mode=enforce backend=landlock+seccomp
```

If you see `backend=fallback`, kernel features are unavailable on this host and only tool-layer enforcement is active. The gateway also surfaces this at `/health` and `/api/v1/security/sandbox-status`.

**Internal preview / web_serve ports.** Agent-generated previews bind temporary ports in the range `18000–18999` inside the container. You only need to publish `5000` and `5001` to the host — these internal ports route back through `5001` (the preview listener) and never need direct exposure.

---

## Reverse proxy

For TLS termination, set `gateway.public_url` and `gateway.preview_origin` in `config.json` (or `OMNIPUS_GATEWAY_PUBLIC_URL` / `OMNIPUS_GATEWAY_PREVIEW_ORIGIN`) to the HTTPS URLs your browser reaches. The gateway uses these values to build correct `Content-Security-Policy` and `frame-ancestors` headers.

See [docs/operations/reverse-proxy.md](operations/reverse-proxy.md) for complete nginx and Caddy configuration examples covering the two-port topology.

---

## Health check

```bash
curl -fsS http://localhost:5000/health
```

Returns `200 OK` with a JSON body when the gateway is healthy. The Dockerfile includes a built-in `HEALTHCHECK` using this endpoint (30 s interval, 3 s timeout).

---

## Updating

```bash
docker compose -f docker/docker-compose.yml pull
docker compose -f docker/docker-compose.yml --profile gateway down
docker compose -f docker/docker-compose.yml --profile gateway up -d
```

Or for `docker run` deployments:

```bash
docker pull ghcr.io/elicify-ai/omnipus:latest
docker stop omnipus-gateway
docker rm omnipus-gateway
# Re-run the original docker run command with the same volume mount.
```

The data directory is never modified by the image pull. **Patch and minor releases require no migration step.** Major-version upgrades (`v0.1 → v0.2 → v0.3`) may require manual `~/.omnipus/` migration — the v0.3 "Rooms" redesign explicitly breaks backward compatibility. **Snapshot the data directory before pulling a new major tag.**

---

## Backup and restore

The entire state of an Omnipus deployment lives under `~/.omnipus/`. Backup is a single directory snapshot:

```bash
# Stop the container so writes settle (sessions and audit log are append-only
# JSONL — a hot snapshot is usually safe, but stopping is the only way to get
# a transactional point-in-time view).
docker compose -f docker/docker-compose.yml --profile gateway down

# Snapshot.
tar czf omnipus-backup-$(date +%F).tgz -C ./ data/

# Restart.
docker compose -f docker/docker-compose.yml --profile gateway up -d
```

Restore is the same in reverse: stop, extract the tarball into place, restart. **`master.key` must be restored alongside the data** — without it, `credentials.json` is unrecoverable. If you back up encrypted volumes with separate key management, store the master key in your secrets vault and inject via `OMNIPUS_KEY_FILE` instead of restoring it onto disk.

---

## Logs

The gateway writes to two files inside the data volume's `logs/` directory:

| File | Contents |
|---|---|
| `gateway.log` | Runtime log (requests, errors, sandbox events) |
| `gateway_panic.log` | Stderr capture, populated only on startup panic |

**The gateway does not rotate these files itself.** On a long-running deployment they will grow unbounded.

### Host-Side Rotation

Mount `logs/` as a separate bind-mount and rotate with `logrotate` on the host (`/etc/logrotate.d/omnipus`):

```
/var/lib/omnipus/logs/*.log {
    daily
    rotate 14
    compress
    missingok
    copytruncate
}
```

### Docker Logging Driver

Run the gateway with the JSON file driver's built-in rotation (in `docker-compose.yml`):

```yaml
logging:
  driver: json-file
  options:
    max-size: "100m"
    max-file: "5"
```

Note: this only captures stdout/stderr; the file-based `gateway.log` still grows. The container logs are a duplicate of `gateway.log` for the most recent boot only.

For a production deployment, prefer host-side `logrotate` against the bind-mount.

---

## Vestigial files

The following files in `docker/` are inherited from upstream and are not part of the v0.1 product. Do not use them; they may be removed in a future cleanup commit.

`Dockerfile.full` is the same broken Go-only builder stage that `Dockerfile.heavy` had before its fix. It is untested; the v0.1 path for chat and MCP without browser tools is the minimal image plus a user-supplied MCP server. `Dockerfile.goreleaser.launcher` and `docker-compose.full.yml` are similarly vestigial.

(`Dockerfile.heavy` was previously listed here but has been rewritten with a working three-stage build and is now a supported image variant — see [Image variants](#image-variants).)
