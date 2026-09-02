# Changelog

All notable changes to Omnipus will be documented in this file. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html) once 1.0 ships.

Pre-1.0 versions are tracked by the active release branch rather than by tagged release, and are subject to change without notice.

## [Unreleased] — `feat/browser-streaming-performance`

### Changed

- **There is no longer a computed default for `performance.max_parallel_agents`.** The old default divided available memory by an assumed per-agent cost (~3.5 MB) to pick a number once at startup. Concurrency is now bounded by live available memory at the moment each agent turn is admitted. `0` means *not configured*, not *auto-detect*. An explicitly configured value is unaffected and is still honoured exactly as set. Settings → Performance shows "automatic — bounded by available memory" instead of an integer when nothing is configured.
- **`browser_evaluate` is enabled by default.** `sandbox.browser_evaluate_enabled` is seeded `true` on a fresh install; which agents may call it is decided by tool policy, and Jim holds the only agent-level grant. Note what the JavaScript runs against: a browser holding your live logins. The duplicate, dead `tools.browser.evaluate_enabled` key (and its `OMNIPUS_TOOLS_BROWSER_EVALUATE_ENABLED` environment variable) is **deleted** — it had no effect on anything. An explicitly set `sandbox.browser_evaluate_enabled: false` now survives a config save; previously it was silently dropped and reverted.

### Added

- **Each workspace now has its own browser.** A separate Chrome process with its own profile directory, its own cookies and its own logins. A workspace cannot see or use another workspace's; agents on one workspace still share that workspace's browser. This replaces one Chrome shared by the whole installation, and it is what the `browser_list_tabs` and `browser_open_tab` descriptions now tell an agent.
- `tools.browser.idle_close_ttl` (default 15m) — how long a workspace's whole browser may sit with no tabs, no viewer and nothing running before the Chrome process is closed. Its profile stays on disk, so the workspace is still signed in when it comes back. There is no way to switch this off.
- `tools.browser.cache_trim_interval` (default 1h) — how often *closed* workspace profiles are swept for disposable browser cache. Cookies, saved passwords, Local Storage, Session Storage, IndexedDB and a site's own Cache Storage are never touched.
- **Deleting a workspace now deletes its browser profile**, cookies and saved sessions included. Closing a browser for any other reason — it went idle, memory ran short, the team changed, you saved settings, it crashed — keeps the profile, which is what lets the workspace come back signed in.
- `tools.browser.lease_wait` (default 2s) — how long a browser tool call waits for the single-driver lease before declaring contention. Clamped to at most half of `tools.browser.page_timeout`, with a warning naming both keys and both values whenever your configured value is lowered.
- `tools.browser.actionability_gate` (`full` | `visible_only`, default `full`) — a temporary revert switch for the stricter element-actionability check, to be removed once it has soaked.
- A startup warning when running in a container with no memory limit set: available-memory readings then reflect the node rather than the container, and the kernel may OOM-kill the process. Naming the remedy (`resources.limits.memory`). Never a refusal.
- Real memory readers for macOS (`sysctl`), so the memory mechanism is measurable on the primary development platform for the first time.

### Fixed

- **An unreadable `/proc/meminfo` no longer reports a fabricated 4 GB.** On a Linux host without a readable procfs (gVisor, distroless, a hardened seccomp profile) the process previously reported 2 GiB of available memory that nothing had measured, and every consumer treated it as a real reading. It now reports "cannot determine", and each consumer takes a documented conservative branch. A related defect is fixed with it: the fabricated figure could win a `min()` against a *real* cgroup reading and discard it, on exactly the containers where the cgroup reading was the only true one.

### Known limitations

- **Upgrading logs every workspace out of its browser, once.** No workspace inherits the old shared profile: each one starts with a fresh, empty profile directory and is signed out of everything the shared browser was signed in to. The old `profiles/default/` directory is left on disk untouched — nothing reads it any more, and you can delete it once you are satisfied nothing was in it that you wanted.
- **`tools.browser.cache_trim_interval` does not bound how large a browser profile gets.** Nothing is trimmed while a browser is running, because trimming a running browser's cache would mean closing a browser somebody is using. A workspace driven continuously, with no idle gap, keeps growing its cache for as long as it is driven, whatever the interval is set to. The gateway logs this once at startup rather than leaving you to discover it from a full disk.
- **There is no memory-derived browser limit on Windows.** This codebase has no way to read available memory there, and the browser will not start a second instance without one — so the floor is one browser whatever the machine's physical RAM. Browser support on Windows is degraded and unsupported for that reason.
- **On a host whose available memory cannot be measured, concurrency holds at a floor of two agent turns and one browser.** The system refuses to *grow*, never to *run*. Two different situations produce this and they are not the same class: a **Linux host with an unreadable `/proc/meminfo`** (gVisor, distroless, hardened seccomp) is a **supported deployment** that happens to be unmeasurable, while **Windows** is **degraded — unsupported**, because no memory reader exists for it in this codebase. On Windows no amount of physical RAM will raise the floor; browser support there is degraded-unsupported for the same reason. On either, set `performance.max_parallel_agents` explicitly.
- **Container detection has one blind spot.** A cgroup-v2 pod in its own cgroup namespace, with service links disabled and no `/.dockerenv`, is indistinguishable from bare metal from the inside, so the no-memory-limit warning above will not fire for it. Set `OMNIPUS_CONTAINERIZED=1` to declare it; that is the only coverage for that shape.

- Iframe preview (tier-13) — second gateway listener on `:5001` with isolated origin and bind-port allow-list
- Unified `web_serve` tool across preview and Tier-1/3 workspace tools
- Kernel-enforced bind-port allow-list via Landlock `NET_BIND_TCP`
- Sandbox-aware `exec` — `workspace.shell` passes through the active sandbox context
- Multi-active sessions with `session_id`-keyed routing
- Vision support — image media passed to LLM tool results with retry-without-image on rejection
- Two-stage cancel system (graceful → hard → detached) with `/cancel` on 12+ channels and CLI double-Escape shortcut
- Auto-Chromium install for browser tools on first use
- Contract-first wire formats — OpenAPI + AsyncAPI source of truth, generated Go + TS types, runtime Zod validation at SPA edge
- 3-binary release pipeline (Linux amd64 + arm64, macOS arm64), `install.sh`, Docker on GHCR
- CLI onboarding wizard — `omnipus onboard` is now interactive (was a print-only stub)
- HMAC-chained audit log with two-layer redaction
- Default-deny internal-CIDR egress on sandboxed children
- Per-agent + per-IP memory write rate limits
- Shell-guard hardening — fork-bomb regex + RLIMIT_NPROC ceiling
- Master.key path guard for secrets subtree
- New docs: `docs/memory.md`, `docs/skills.md`, `docs/routing.md`, `docs/observability.md`, `docs/operations/sandbox-config.md`, `docs/operations/sandbox-limitations.md`, `docs/operations/platform-support.md`, `docs/tools-reference.md`
- SVG architecture and roadmap diagrams under `docs/marketing/diagrams/`
- Governance scaffolding: `CLA.md`, `TRADEMARKS.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`, `SUPPORT.md`, `.github/CODEOWNERS`, `.github/FUNDING.yml`, `.github/ISSUE_TEMPLATE/config.yml`

### Changed

- `NewProcessHook` applies an unconditional 5s handshake deadline (#163)
- `EventKind` serializes as a canonical string name over `hook.event` (#164)
- Hook observer subscription buffer raised from 64 to 1024 (#165)
- Default gateway port unified to 5000 across `datamodel.Init`, `config.DefaultConfig`, SSE/WebSocket CORS fallback (#160)
- README restructured — marketing-first, docs index, brand-colored SVG diagrams
- LICENSE expanded to full attribution chain (nanobot → PicoClaw → Omnipus) under elicify.ai Pte. Ltd. copyright
- Global heartbeat retired in favor of per-agent heartbeat. **Upgrade note:** Mia's heartbeat is enabled by default on first boot after upgrade — operators who relied on the old global `heartbeat.enabled=false` opt-out must disable it per-agent (Agents → `heartbeat_enabled=false`).

### Removed

- `pkg/channels/teams/` — half-wired channel deleted; new `TestNoHalfWiredChannels` regression guard catches the class of bug (#161)
- "Three shipping variants" framing from README and CLAUDE.md (variant references retained in BRD and ROADMAP only)

### Fixed

- Real CLI onboarding wizard replaces print-only stub; docker entrypoint no longer gates boot on it (#159)
- Webhook signature verification — project-wide invariant documented + AST contract test (#162)
- `TestCancel_AbandonedAfterHardTimeout` teardown race fixed
- `TestRunRecap_HappyPath_PersistsLastSessionAndRetro` race between retro and last-session writes fixed
- 5 verified-fixed issues closed on branch: #39 (Teams E2E moot), #57 (ChannelFactory signature uniform), #78 (SSRF `allow_internal` wired), #109 (flaky test 100/100 + race), #122 (`data-testid="approval-modal"`)

---

## How this file is maintained

For now, entries are added manually as part of the PR description checklist. Once we have semver-tagged releases, we'll likely move to [release-drafter](https://github.com/release-drafter/release-drafter) which builds changelog entries from PR titles using conventional-commit prefixes.

Section conventions (per Keep a Changelog):

- **Added** — new features
- **Changed** — changes in existing functionality
- **Deprecated** — soon-to-be removed
- **Removed** — removed in this release
- **Fixed** — bug fixes
- **Security** — vulnerability fixes (with link to the GitHub Security Advisory)

---

© 2026 elicify.ai Pte. Ltd. · Singapore · https://omnipus.ai
