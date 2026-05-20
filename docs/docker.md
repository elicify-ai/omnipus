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

## Release image vs. dev image

| Image | Source | Entrypoint |
|---|---|---|
| `ghcr.io/elicify-ai/omnipus:latest` | Built by goreleaser on release | `docker/entrypoint.sh` |
| Local build (`docker build -f docker/Dockerfile .`) | Multi-stage SPA + Go build | `omnipus gateway` directly |

The release image uses `entrypoint.sh` as a first-run guard (see [First-run behavior](#first-run-behavior)). The local-build image starts the gateway directly — it self-bootstraps on the first run.

---

## Before you start

> **The release image (`ghcr.io/elicify-ai/omnipus:latest`) will not start without a pre-supplied `config.json`.** Its entrypoint runs a first-run gate that calls `omnipus onboard` (a stub that only prints instructions) and exits cleanly. You must drop a minimal `config.json` into the data volume before the gateway will ever run — both quick-starts below assume this. See [First-run behavior](#first-run-behavior) for the full explanation.

The locally-built image (`docker build -f docker/Dockerfile .`) self-bootstraps via `datamodel.Init()` and does not need a pre-supplied config.

---

## Quick start — docker run

```bash
# Create a local data directory first.
mkdir -p ./data

# Write a minimal config.json so the gateway can start.
# Pick a port and use it consistently in both the file and the host mapping.
cat > ./data/config.json <<'EOF'
{
  "version": 1,
  "gateway": { "host": "127.0.0.1", "port": 5000 },
  "agents": { "defaults": {}, "list": [] },
  "providers": []
}
EOF

docker run -d \
  -p 127.0.0.1:5000:5000 \
  -p 127.0.0.1:5001:5001 \
  -v "$PWD/data:/root/.omnipus" \
  ghcr.io/elicify-ai/omnipus:latest

# Open the onboarding wizard
open http://localhost:5000
```

The onboarding wizard (at `/onboarding`) walks through: provider selection, API key entry, model selection, and admin account creation. Complete it before using the chat UI.

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
# First: create data directory and config.json (same as above), then:
docker compose -f docker/docker-compose.yml --profile gateway up -d
```

The compose file maps `127.0.0.1:5000:5000` and `127.0.0.1:5001:5001` and sets `OMNIPUS_GATEWAY_PORT=5000` + `OMNIPUS_GATEWAY_PREVIEW_PORT=5001` on the gateway service, so the in-container listener matches the host port out of the box. The gateway still requires a pre-supplied `config.json` (same gate as the `docker run` quick-start above).

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

The release image (`ghcr.io/elicify-ai/omnipus:latest`) uses `docker/entrypoint.sh`. On every start it checks:

```
if no workspace/ dir AND no config.json → print setup instructions and exit 0
else → exec omnipus gateway
```

**The check is a gate, not a bootstrapper.** The `omnipus onboard` command is a stub that prints instructions; it does not create a `config.json`. You must supply a `config.json` via volume mount before the gateway will start. Without it, the container exits cleanly on every run.

> The `workspace/` test in the gate is vestigial — `datamodel.Init()` never creates a top-level `~/.omnipus/workspace/` directory, so in practice only the presence of `config.json` matters. Don't waste time creating a `workspace/` directory hoping to satisfy the gate; provide `config.json` instead. (Re-visit this callout when the v0.3 "Rooms" redesign lands — it reintroduces a top-level workspace concept and will change the gate semantics.)

The local-build image has no `entrypoint.sh`. It runs `omnipus gateway` directly. On the first start the gateway:

1. Calls `datamodel.Init()`, which creates `~/.omnipus/` subdirectories and writes a minimal `config.json` (seeded port `3000`, no providers).
2. Boots into onboarding mode. Visit `http://localhost:3000/onboarding` (the seeded port) to complete setup, or override via `OMNIPUS_GATEWAY_PORT`.

---

## Volume layout

The data path inside the container depends on which image you use:

| Image | Runs as | Data path inside container |
|---|---|---|
| `ghcr.io/elicify-ai/omnipus:latest` (release) | `root` | `/root/.omnipus` |
| Local build (`docker build -f docker/Dockerfile .`) | `omnipus` (UID 1000) | `/home/omnipus/.omnipus` |

The `docker run` and compose examples above use `/root/.omnipus` because they target the release image. If you bind-mount against a locally-built image, change the right-hand side accordingly:

```bash
docker run -d \
  -p 127.0.0.1:3000:3000 \
  -p 127.0.0.1:3001:3001 \
  -v "$PWD/data:/home/omnipus/.omnipus" \
  omnipus:local
```

(The local-build image self-bootstraps via `datamodel.Init()` and uses the seeded port `3000` unless overridden — that's why the example above shows `3000` instead of the release image's `5000` convention.)

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

By default, both the compose file and the recommended `docker run` command bind to `127.0.0.1`, making the gateway reachable only from localhost. This is intentional: port 5000 (or whichever port you configure) serves the authenticated admin API and the onboarding wizard. Anyone who can reach the port before onboarding completes can register as the first admin user.

To expose the gateway to a LAN or public IP, change the bind address:

```bash
docker run -d \
  -p 0.0.0.0:5000:5000 \
  -p 0.0.0.0:5001:5001 \
  -v "$PWD/data:/root/.omnipus" \
  ghcr.io/elicify-ai/omnipus:latest
```

Do this only after onboarding is complete and a strong admin password is set.

### Three flags every Docker operator should set deliberately

- **`gateway.sandbox.mode`** (config.json) or CLI `--sandbox=enforce|permissive|off`. Defaults to `enforce` when the kernel supports Landlock + seccomp; falls back automatically otherwise. Verify after first boot with `omnipus doctor` (or `gateway.log` — see [Sandbox under Docker](#sandbox-under-docker)). See [docs/operations/security-considerations.md](operations/security-considerations.md) for the full threat model.
- **`gateway.trust_xff`** — leave `false` unless you front the container with a reverse proxy. When `true`, the gateway honours `X-Forwarded-For` for audit logs and rate-limit keys. Setting it `true` without a trusted proxy lets any client spoof their IP. See [reverse-proxy.md](operations/reverse-proxy.md).
- **`gateway.dev_mode_bypass`** — **never set this `true` in any Docker deployment.** It disables auth on routes that haven't completed onboarding and is intended for unit-test scaffolding only.

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

**The gateway does not rotate these files itself.** On a long-running deployment they will grow unbounded. Two options:

1. **Host-side rotation.** Mount `logs/` as a separate bind-mount and rotate with `logrotate` on the host (`/etc/logrotate.d/omnipus`):
   ```
   /var/lib/omnipus/logs/*.log {
       daily
       rotate 14
       compress
       missingok
       copytruncate
   }
   ```
2. **Docker logging driver.** Run the gateway with the JSON file driver's built-in rotation (in `docker-compose.yml`):
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

The following files in `docker/` are inherited from upstream and are not part of the v0.1 product. Do not use them; they may be removed in a future cleanup commit:

- `Dockerfile.full`
- `Dockerfile.heavy`
- `Dockerfile.goreleaser.launcher`
- `docker-compose.full.yml`
