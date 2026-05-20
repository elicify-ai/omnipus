# Omnipus Roadmap

Omnipus ships as three product variants sharing a common Go agentic core: Open Source (single binary, ships first), Desktop (Electron wrapper, ships second), and Cloud/SaaS (hosted, ships third). This document tracks the release plan for the Open Source core.

---

## v0.1 — Stabilize current branch

**Branch:** `feature/iframe-preview-tier13`  
**Status:** In progress

Scope is intentionally narrow: complete the in-flight work on this branch and ship it. No memory or projects changes — those are explicitly v0.3.

- `web_serve` tool unification — consolidate HTTP-serve and preview-serve paths into a single implementation
- Kernel-enforced bind-port allow-list — sandbox policy explicitly permits only the gateway and preview ports
- Sandbox-aware `exec` — workspace shell tool passes through the active sandbox context
- Iframe preview feature — second listener on `gateway.preview_port` (default 5001) serves agent-generated HTML on a separate origin; `Content-Security-Policy` and `frame-ancestors` headers built from `gateway.public_url` / `gateway.preview_origin`

All CI gates must be green before merge: `typecheck`, `wire-types-lint`, `verify-contracts`, `lint`, `vuln_check`, `test`, `security`, `perf-smoke`, `playwright`.

---

## v0.2 — Security hardening (pentest quick wins)

**Issue:** [#155](https://github.com/elicify-ai/omnipus/issues/155) (currently closed as completed; reopen or split into per-item issues when execution starts)  
**Status:** Tracked but not started — CLAUDE.md treats v0.2 as the post-v0.1 release

Quick, targeted fixes only. No architectural changes — structural process isolation and capability-based RBAC move to v0.3.

- Env var allowlist switch — `pkg/sandbox/hardened_exec.go::sensitiveEnvKeys` becomes an explicit opt-in list instead of a denylist
- `master.key` 0600 verification — boot-time check; warn and abort if permissions are wider
- Shell-guard hardening — additional mitigations for shell metacharacter injection through tool parameters
- Internal-CIDR egress blocking — block RFC-1918 and link-local addresses in the SSRF guard
- Audit log integrity — HMAC chain linking audit log entries; detects tampering
- Rate limiting on auth endpoints — `/api/v1/auth/login` and `/api/v1/auth/register-admin` get per-IP rate limits

---

## v0.3 / 1.0 — "Rooms" redesign

**Issue:** [#156](https://github.com/elicify-ai/omnipus/issues/156)  
**Status:** Design complete; implementation not started

Fresh build. No backward compatibility guarantee. Five locked design documents in `docs/design/`:

| Design doc | What it covers |
|---|---|
| `sandbox-redesign-2026-05.md` | Two-room workspace topology — private agent rooms and shared project rooms under `.omnipus/` |
| `memory-redesign-2026-05.md` | 4-tier memory (sessions / memories / learnings / last-session.md), three tools (`remember` / `recall` / `retrospective`), Dreamcatcher consolidation, bleve + JSONL + MinHash |
| `tasks-redesign-2026-05.md` | Tasks scoped per-room, cascade-delete with project, reassignment audit trail |
| `projects-ui-2026-05.md` | Three SPA surfaces: session creation modal, Command Center pivoted to rooms, session history with grouping |
| `settings-notifications-2026-05.md` | Memory and Dreamcatcher settings tabs, tier-based retention notifications |

---

## Routing new work

| Work type | Target |
|---|---|
| Completing in-flight v0.1 items | v0.1 |
| Pentest findings (no structural change needed) | v0.2 |
| Pentest findings (require structural change) | v0.3 |
| Memory / projects / tasks / room topology | v0.3 |
| Everything else | Flag the scope question before starting |

---

## What is not on this roadmap

The following items from the upstream project are **not** current priorities for this fork:

- Edge AI / sub-64MB embedded boards
- Android device control
- AI-Native OS interaction paradigms
- OneBot protocol

These may be reconsidered after v0.3 ships.
