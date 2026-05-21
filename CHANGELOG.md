# Changelog

All notable changes to Omnipus will be documented in this file. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html) once 1.0 ships.

Pre-1.0 versions are tracked by the active release branch rather than by tagged release, and are subject to change without notice.

## [Unreleased] — `feature/iframe-preview-tier13`

### Added

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
