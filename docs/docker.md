# Docker — Operator Guide

> Back to [README](../README.md)

## What runs in the container

Omnipus is a **single binary** (`omnipus`) with the React SPA compiled in via `go:embed`. It opens **one** TCP listener:

| Listener | Default port | Purpose |
|---|---|---|
| Main | `5000` | SPA, REST API, WebSocket/SSE chat, and agent-generated previews under `/preview/` |

There is no second preview port. ADR-044 moved agent previews onto the main listener, and the `gateway.preview_port` / `preview_host` / `preview_origin` config keys were deleted outright. If you are following an older guide that publishes `5001`, drop it — nothing listens there.

The port comes from `config.json` (`gateway.port`) or the environment variable `OMNIPUS_GATEWAY_PORT`.

**The bind address is the one setting a container operator must not skip.** Omnipus defaults to binding loopback only — `gateway.host` is `localhost` in the seeded `config.json` (`pkg/datamodel/init.go`) and `127.0.0.1` in the compiled defaults (`pkg/config/defaults.go`). Loopback inside a container means loopback *of the container*, so a published port mapping reaches a closed door. Set `OMNIPUS_GATEWAY_HOST=0.0.0.0` (or `gateway.host` in `config.json`) whenever you publish a port. The examples below do this explicitly.

All Omnipus data — `config.json`, `master.key`, sessions, tasks, audit log — lives under `~/.omnipus/` (i.e. `/root/.omnipus/` in the published image, which runs as root, or the path set by `OMNIPUS_HOME`).

---

## Images

Two `Dockerfile`s ship in `docker/`. They are not two flavours of the same build — they are built by different things, for different reasons.

| Image | Dockerfile | How it is built | Browser tools | Python / Node MCP |
|---|---|---|---|---|
| **Published release** — `ghcr.io/elicify-ai/omnipus:latest` | [`docker/Dockerfile.goreleaser`](../docker/Dockerfile.goreleaser) | goreleaser hands it a prebuilt per-architecture binary; the Dockerfile only copies it onto `alpine:3.21` with `ca-certificates` and `tzdata` | No | No |
| **Heavy** — build it yourself | [`docker/Dockerfile.heavy`](../docker/Dockerfile.heavy) | Builds the SPA and the Go binary from source, then layers a full browser and scripting runtime | Yes — bundled Chrome-for-Testing | Yes — Node 24, Python 3, `uv`/`uvx`, `git`, `jq` |

### One image is the intent, not yet the reality

ADR-067 decided the project should ship **one** container image, the heavy one. That decision is recorded, and part of it has landed — but the consolidation is not finished, and this guide describes what exists today rather than what is planned:

- The image you **pull** is still the small Alpine one. Its contents did not change in the consolidation work.
- `Dockerfile.heavy` is still **build-it-yourself**. Nothing publishes it.
- Merging the two is genuine redesign work, not a path swap: goreleaser's container model copies an already-built binary, while the heavy image compiles its own. A single Dockerfile would have to accept goreleaser's prebuilt binary *and* carry the heavy runtime. That is tracked as follow-up work. See ADR-067 §10.1.

### What did change: `make docker-build`

`make docker-build` used to build a small Alpine image from source. **It now builds `docker/Dockerfile.heavy`**, tagged `omnipus:latest` by default (override with `DOCKER_IMAGE`):

```bash
make docker-build
```

If you script against this target, budget for the difference. The image it used to produce was on the order of 70 MB; the heavy image is on the order of a gigabyte, because it carries a full Chrome, Node 24, Python 3, `uv`, `git` and `jq`. That is a deliberate trade — a container is where a missing dependency hurts most — but it is a real jump in download size, disk footprint and installed attack surface.

The other Docker make targets:

| Target | What it does |
|---|---|
| `make docker-build` | Build the heavy image from `docker/Dockerfile.heavy` |
| `make docker-test` | Build the heavy image and smoke-test its runtime tooling (`scripts/test-docker-mcp.sh`) |
| `make docker-run` | `docker compose --profile gateway up` against the **published** image |
| `make docker-run-agent` | `docker compose run --rm omnipus-agent` (interactive) |
| `make docker-clean` | `docker compose down -v` and remove the local image tags |

The former `-full` variants of these targets were deleted along with the Dockerfiles they referenced.

### The published image and browser tools

The published image (`ghcr.io/elicify-ai/omnipus:latest`) contains **no browser**. Without one, the entire `browser.*` tool family (`browser.navigate`, `browser.screenshot`, `browser.read_content`, `browser.console_logs`, `browser.action`) will not work, nor will `web_serve` dev-server previews, nor any skill or MCP server that shells out to a system Chrome.

It also does not carry the bundled Chrome-for-Testing payload that the `.tar.gz` archives and the `.deb` / `.rpm` packages ship — the Dockerfile copies the `omnipus` binary and nothing else.

The gateway falls through to its managed-Chromium download path on first call, and on this image that download does not help: the downloaded binary is glibc-linked and Alpine is musl, so launching it fails with a misleading `no such file or directory` (the missing piece is the ELF interpreter `/lib64/ld-linux-x86-64.so.2`, not the binary). The agent degrades to `web_fetch` and surfaces the failure inline in chat.

**If you need browser tools, build the heavy image.**

### Building the heavy image

```bash
docker build -t omnipus:heavy -f docker/Dockerfile.heavy .
```

Run it from the repository root, not from `docker/` — the build context must include the whole tree.

Three stages: `node:24-alpine` builds the SPA into `dist/spa/`; `golang:1.26.6-alpine` copies that output into `pkg/gateway/spa/` (satisfying the `//go:embed all:spa` directive) and builds the binary; `node:24-bookworm-slim` is the runtime, which adds Chrome-for-Testing, Python 3, `uv`/`uvx`, `git`, `jq` and the shared libraries Chrome links against.

Both builder stages are pinned to the **build** platform and cross-compile, so a multi-architecture build does not run `npm ci` and `go build` under emulation:

```bash
docker buildx build --platform linux/amd64,linux/arm64 -f docker/Dockerfile.heavy .
```

The runtime stage deliberately has no platform pin — it installs native packages and fetches the Chrome build for the real target architecture.

The heavy image runs as a non-root user `omnipus` (UID 1000), so bind-mounts target `/home/omnipus/.omnipus`:

```bash
docker run -d \
  -p 127.0.0.1:5000:5000 \
  -e OMNIPUS_GATEWAY_HOST=0.0.0.0 \
  -v "$PWD/data:/home/omnipus/.omnipus" \
  omnipus:heavy
```

It already sets `OMNIPUS_GATEWAY_HOST=0.0.0.0` and `OMNIPUS_SANDBOX_MODE=permissive` in the image, so the `-e` flag above is belt-and-braces rather than strictly required. See [Sandbox under Docker](#sandbox-under-docker) for what the permissive default means.

Onboarding, sandbox behaviour and the rest of this guide apply identically once the container is up.

---

## Entrypoints

| Image | Entrypoint | Effective command |
|---|---|---|
| Published release | `docker/entrypoint.sh` | `exec omnipus start "$@"` |
| Heavy (local build) | `omnipus` | `omnipus start --allow-empty` |

`entrypoint.sh` is a one-line wrapper around `omnipus start` with no first-run gate. `gateway` still works as an alias for `start`, so an older `docker run ... omnipus gateway` command line keeps working.

`--allow-empty` (`-E`) is now a **hidden, deprecated no-op**. The gateway always boots into limited mode when no provider is configured, which is exactly what the flag used to request, so scripts that still pass it keep working and scripts that drop it behave identically.

---

## Before you start

> The container boots straight into the gateway on every start. Onboarding is driven from the web UI at `/onboarding` (visit it once the gateway is up) or from `docker exec -it <ctr> omnipus onboard`. The gateway comes up before any provider is configured, so the SPA wizard can add the first one. See [First-run behavior](#first-run-behavior).

---

## Quick start — docker run

```bash
# Create a local data directory first.
mkdir -p ./data

docker run -d \
  -p 127.0.0.1:5000:5000 \
  -e OMNIPUS_GATEWAY_HOST=0.0.0.0 \
  -v "$PWD/data:/root/.omnipus" \
  ghcr.io/elicify-ai/omnipus:latest

# Open the onboarding wizard
open http://localhost:5000
```

`OMNIPUS_GATEWAY_HOST=0.0.0.0` is required on the published image. Without it the gateway binds the container's own loopback interface and `http://localhost:5000` on your machine connects to nothing.

The onboarding wizard (at `/onboarding`) walks through provider selection, API key entry, model selection and account creation. Complete it before using the chat UI.

> Port `5000` is widely used by other local dev servers (the macOS AirPlay receiver, Flask defaults, and so on). If the host port collides, pick a free port (e.g. `5050`) and use it consistently in the `-p` flag, the in-container port, and the URL you open.

To move the port without editing `config.json`:

```bash
docker run -d \
  -p 127.0.0.1:5050:5050 \
  -v "$PWD/data:/root/.omnipus" \
  -e OMNIPUS_GATEWAY_HOST=0.0.0.0 \
  -e OMNIPUS_GATEWAY_PORT=5050 \
  ghcr.io/elicify-ai/omnipus:latest
```

---

## Quick start — docker compose

The compose file is at `docker/docker-compose.yml`. It is **pull-only**: both services reference the published `ghcr.io/elicify-ai/omnipus:latest` tag and neither has a `build:` section, so `docker compose build` against it builds nothing. Use `make docker-build` (or the `docker build` command above) to build an image.

Run from the repo root:

```bash
docker compose -f docker/docker-compose.yml --profile gateway up -d
```

The compose file maps `127.0.0.1:5000:5000` and sets `OMNIPUS_GATEWAY_PORT=5000` on the gateway service so the in-container listener matches the host port. It does **not** currently set `OMNIPUS_GATEWAY_HOST`, so add it before the mapping is reachable:

```yaml
    environment:
      - OMNIPUS_GATEWAY_PORT=5000
      - OMNIPUS_GATEWAY_HOST=0.0.0.0
```

Check logs:

```bash
docker compose -f docker/docker-compose.yml logs -f omnipus-gateway
```

Stop:

```bash
docker compose -f docker/docker-compose.yml --profile gateway down
```

### Agent profile (one-shot query)

The `agent` profile runs a single query and exits. The one-shot form is `omnipus <agent-name> "<prompt>"` — the agent's name comes first, then the prompt:

```bash
docker compose -f docker/docker-compose.yml run --rm omnipus-agent mia "What is 2+2?"
```

`mia` is the default assistant in the built-in roster (`mia`, `jim`, `ray`, `ava`). Run the container with no arguments to print the roster.

Two prerequisites, because the one-shot command is a **client** rather than a self-contained run:

1. **Onboarding must already be complete in the shared `./data` directory.** The command reads `cli.token` from the data directory and fails with `no CLI key found — run 'omnipus start'` if the gateway has never started.
2. **It talks to `localhost` inside its own container**, not to the separate `omnipus-gateway` container. If no gateway is listening there and a non-interactive master key is on disk, it starts one itself and retries.

> **Removed CLI verbs.** `agent`, `auth`, `status`, `cron`, `migrate`, `model` and `skills` were removed in the CLI redesign (`cmd/omnipus/main.go`'s `removedVerbs`). Typing one prints a pointer to `omnipus --help` and exits non-zero. In particular there is no `omnipus agent` subcommand any more — that is why the compose entrypoint is a bare `["omnipus"]`. `gateway` is the one survivor, kept as an alias for `start`.

---

## First-run behavior

There is **no first-run gate**. The container boots straight into the gateway on every start, on both images. The gateway comes up even before any provider is configured; the SPA onboarding wizard at `/onboarding` adds the first one. If you prefer to drive onboarding from a terminal, run `docker exec -it <ctr> omnipus onboard` against the running container.

The previous gate called `omnipus onboard` and exited before the gateway could start; the command was a print-only stub at the time. See issue #159 for the history.

On the first start the gateway calls `datamodel.Init()`, which creates the `~/.omnipus/` subdirectories and writes a minimal `config.json` — seeded host `localhost`, port `5000`, no providers, no agents. Visit `http://localhost:5000/onboarding` to complete setup.

---

## Volume layout

The data path inside the container depends on which image you use:

| Image | Runs as | Data path inside container |
|---|---|---|
| `ghcr.io/elicify-ai/omnipus:latest` (published) | `root` | `/root/.omnipus` |
| Heavy (`docker build -f docker/Dockerfile.heavy .`) | `omnipus` (UID 1000) | `/home/omnipus/.omnipus` |

The `docker run` and compose examples above use `/root/.omnipus` because they target the published image. Against a locally-built heavy image, change the right-hand side:

```bash
docker run -d \
  -p 127.0.0.1:5000:5000 \
  -v "$PWD/data:/home/omnipus/.omnipus" \
  omnipus:heavy
```

Either way, the directory layout is the same:

| Sub-path | Contents |
|---|---|
| `config.json` | Main config: providers, agents, gateway, channels |
| `master.key` | AES-256 encryption key (0600). **Back this up.** |
| `credentials.json` | Encrypted credential store (AES-256-GCM + Argon2id) |
| `cli.token` | Plaintext CLI key (0600), used by one-shot `omnipus <agent> "…"` runs |
| `sessions/` | Day-partitioned JSONL transcripts |
| `tasks/` | Per-task JSON files |
| `agents/` | Custom agent definitions (AGENT.md + SOUL.md) |
| `logs/` | `gateway.log`, `gateway_panic.log` |

Mount the entire data directory as a single named volume or bind-mount. Splitting sub-paths into separate mounts is not supported.

**Back up `master.key`.** Losing it makes every credential in `credentials.json` permanently unrecoverable. For headless deployments, inject the key via `OMNIPUS_MASTER_KEY` (64-character hex) or `OMNIPUS_KEY_FILE` (path to a 0600 file) instead of relying on the auto-generated key file.

---

## Pulling from GHCR

```bash
docker pull ghcr.io/elicify-ai/omnipus:latest

# Pin to a specific release:
docker pull ghcr.io/elicify-ai/omnipus:v0.1.0
```

Releases publish `linux/amd64` and `linux/arm64`. Nightly builds publish a `:nightly` tag instead of `:latest`.

---

## Port exposure and security

By default, both the compose file and the recommended `docker run` command bind the **host** side to `127.0.0.1`, making the gateway reachable only from the machine running Docker. This is intentional: the port serves the authenticated API and the onboarding wizard, and anyone who reaches it before onboarding completes can claim the account.

Note the two different bind addresses at work. `-p 127.0.0.1:5000:5000` restricts who on your network can reach Docker's published port. `OMNIPUS_GATEWAY_HOST=0.0.0.0` controls the interface the gateway binds *inside* the container, where `0.0.0.0` is what makes the published port work at all. Setting the second does not expose you to the network; the first is what decides that.

To expose the gateway to a LAN or public IP, change the host side:

```bash
docker run -d \
  -p 0.0.0.0:5000:5000 \
  -e OMNIPUS_GATEWAY_HOST=0.0.0.0 \
  -v "$PWD/data:/root/.omnipus" \
  ghcr.io/elicify-ai/omnipus:latest
```

Do this only after onboarding is complete and a strong admin password is set.

### Three flags every Docker operator should set deliberately

**`sandbox.mode`** (top level of `config.json`, not under `gateway`), or the environment variable `OMNIPUS_SANDBOX_MODE`, or the CLI flag `--sandbox=enforce|permissive|off`. Defaults to `enforce` when the kernel supports Landlock and seccomp, and falls back automatically otherwise — but **the heavy image ships `OMNIPUS_SANDBOX_MODE=permissive`**, so in that image kernel-level confinement is off unless you override it. See [Sandbox under Docker](#sandbox-under-docker) and [docs/operations/security-considerations.md](operations/security-considerations.md).

**`gateway.trust_xff`** — leave `false` unless you front the container with a reverse proxy. When `true`, the gateway honours `X-Forwarded-For` for audit logs and rate-limit keys. Setting it `true` without a trusted proxy lets any client spoof their IP. See [reverse-proxy.md](operations/reverse-proxy.md).

**`gateway.dev_mode_bypass`** — **never set this `true` in any Docker deployment.** It disables auth on routes that have not completed onboarding and exists for unit-test scaffolding only.

---

## Sandbox under Docker

Omnipus's kernel sandbox uses Landlock and seccomp on Linux 5.13+ to constrain the gateway process itself (see CLAUDE.md hard-constraint #4 and [security-considerations.md](operations/security-considerations.md)). Docker's defaults interact with this in non-obvious ways:

| Concern | What to do |
|---|---|
| **Permissive by default in the heavy image** | `docker/Dockerfile.heavy` sets `ENV OMNIPUS_SANDBOX_MODE=permissive`. The default unprivileged container seccomp profile blocks syscalls the hardened-exec path needs, which surfaces as `fork/exec /bin/sh: permission denied` under `enforce`. The container boundary is the confinement in that configuration — a deliberate choice, recorded in ADR-067 §9, not an accident. Set `OMNIPUS_SANDBOX_MODE=enforce` only if you have granted the container the capabilities it needs. |
| **Kernel version** | The kernel inside the container is the host's kernel. Linux 5.13+ is required for full enforcement. Docker Desktop on macOS and Windows uses a managed Linux VM that is typically new enough; on a stock Ubuntu 20.04 host (kernel 5.4) the sandbox falls back to application-level checks. |
| **Default seccomp profile** | Docker 23+ defaults allow the `landlock_*` and `seccomp(2)` syscalls. Older Docker daemons may need `--security-opt seccomp=<profile>` with a profile that permits them. |
| **`no-new-privileges`** | Not required but recommended; nested seccomp installation works either way. |
| **`--privileged`** | **Defeats the sandbox.** Don't run the container privileged. |
| **`--security-opt seccomp=unconfined`** | Disables Docker's seccomp filter; Omnipus's own filter still installs. Acceptable for debugging, not for production. If you must use it, also set `sandbox.mode` to `"off"` so the degraded posture is explicit in the config rather than implied. |
| **Read-only rootfs** | Omnipus reads `/proc/self/status` to detect capabilities; this works with `--read-only` as long as `/proc` remains mounted (the default). |

**Verifying after start.** The authoritative check is the API:

```bash
curl -fsS -H "Authorization: Bearer <token>" \
  http://localhost:5000/api/v1/security/sandbox-status
```

It reports the active backend and mode. The gateway also logs the outcome at boot as `sandbox.applied` with `backend` and `mode` fields — but that is an informational-level line and the default log level is `warn`, so raise `gateway.log_level` to `info` if you want to see it in `gateway.log`. A degraded start is logged at warning level as `sandbox.permissive` and will appear at the default level.

**Internal preview / `web_serve` ports.** Agent-generated dev servers bind temporary ports in the range `18000–18999` inside the container. You do not need to publish them — previews are served back through `/preview/` on the main port.

---

## Reverse proxy

For TLS termination, set `gateway.public_url` in `config.json` (or `OMNIPUS_GATEWAY_PUBLIC_URL`) to the fully-qualified HTTPS URL your browser reaches. The gateway uses it to build correct `Content-Security-Policy`, CORS and WebSocket origin checks, and to construct `web_serve` preview links. The value is read once at boot, so changing it needs a restart.

There is no separate `preview_origin` setting any more — ADR-044 deleted it along with the second listener.

See [docs/operations/reverse-proxy.md](operations/reverse-proxy.md) for complete nginx and Caddy examples.

---

## Health check

```bash
curl -fsS http://localhost:5000/health
```

Returns `200 OK` with a JSON body when the gateway is healthy, and `503` when it is running but degraded.

`docker/Dockerfile.heavy` wires this into a container `HEALTHCHECK` (30 s interval, 3 s timeout, 5 s start period, 3 retries). **The published image has no `HEALTHCHECK`** and does not ship `curl`, so `docker ps` will report no health status for it — poll `/health` from the host or your orchestrator instead.

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

The data directory is never modified by the image pull. **Patch and minor releases require no migration step.** Major-version upgrades (`v0.1 → v0.2 → v0.3`) may require manual `~/.omnipus/` migration — the v0.3 Workspaces redesign explicitly breaks backward compatibility. **Snapshot the data directory before pulling a new major tag.**

---

## Backup and restore

The entire state of an Omnipus deployment lives under `~/.omnipus/`. Backup is a single directory snapshot:

```bash
# Stop the container so writes settle (sessions and the audit log are
# append-only JSONL — a hot snapshot is usually safe, but stopping is the
# only way to get a transactional point-in-time view).
docker compose -f docker/docker-compose.yml --profile gateway down

# Snapshot.
tar czf omnipus-backup-$(date +%F).tgz -C ./ data/

# Restart.
docker compose -f docker/docker-compose.yml --profile gateway up -d
```

Restore is the same in reverse: stop, extract the tarball into place, restart. **`master.key` must be restored alongside the data** — without it, `credentials.json` is unrecoverable. If you back up encrypted volumes with separate key management, store the master key in your secrets vault and inject it via `OMNIPUS_KEY_FILE` instead of restoring it onto disk.

---

## Logs

The gateway writes to two files inside the data volume's `logs/` directory:

| File | Contents |
|---|---|
| `gateway.log` | Runtime log (requests, errors, sandbox events) |
| `gateway_panic.log` | Stderr capture, populated only on a startup panic |

The default log level is `warn`. Raise `gateway.log_level` to `info` or `debug` in `config.json` when you need boot-time detail such as the `sandbox.applied` line.

**The gateway does not rotate these files itself.** On a long-running deployment they will grow unbounded.

### Host-side rotation

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

### Docker logging driver

Run the gateway with the JSON file driver's built-in rotation (in `docker-compose.yml`):

```yaml
logging:
  driver: json-file
  options:
    max-size: "100m"
    max-file: "5"
```

This only captures stdout and stderr; the file-based `gateway.log` still grows. The container logs duplicate `gateway.log` for the most recent boot only.

For a production deployment, prefer host-side `logrotate` against the bind-mount.

---

## What happened to the other Dockerfiles

`docker/` used to hold five Dockerfiles and two compose files. ADR-067 consolidated them. `Dockerfile` (the old minimal image), `Dockerfile.full`, `Dockerfile.goreleaser.launcher` and `docker-compose.full.yml` were **deleted** — five ways to package one binary were four too many, and three of them were already documented here as unmaintained.

What remains is `Dockerfile.goreleaser` (the published release image), `Dockerfile.heavy` (build it yourself), `docker-compose.yml` and `entrypoint.sh`. If a merge or an old branch reintroduces any of the deleted files, resolve by keeping the deletion.
