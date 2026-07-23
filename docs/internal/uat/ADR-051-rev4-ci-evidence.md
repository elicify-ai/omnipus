# ADR-051 Rev 4 — CI Evidence

**Branch:** `sendfile-fix`  
**Draft PR:** https://github.com/elicify-ai/omnipus/pull/550  
**Date:** 2026-07-23  

---

## Acceptance CI (full green)

| Field | Value |
|---|---|
| **Commit** | `fc1bad78` — `fix(lint): avoid named-return err shadow in copyToWorkDir MkdirAll` |
| **CI Run** | https://github.com/elicify-ai/omnipus/actions/runs/30046828692 |
| **Result** | ✅ **SUCCESS — 22/22 jobs** |

Docs-only follow-up on branch HEAD (`c7442aef` UAT evidence refresh) does not change runtime code; acceptance code SHA is `fc1bad78`.

### Job results (`30046828692`)

| Job | Status |
|---|---|
| Wire-Types Lint | ✅ |
| Verify Contracts | ✅ |
| Linter | ✅ |
| Tests | ✅ |
| CGO_ENABLED=0 Build Gate | ✅ |
| TypeScript Type Check | ✅ |
| CLI Removed-Verb Guard | ✅ |
| E2E shard plan / plan check | ✅ |
| Vitest — components-chat | ✅ |
| Vitest — components-agents-settings | ✅ |
| Vitest — components-layout-projects | ✅ |
| Vitest — lib-store | ✅ |
| Security Check | ✅ |
| Security Tests | ✅ |
| Perf Smoke | ✅ |
| E2E — stubs | ✅ |
| E2E — ui | ✅ |
| E2E — ui-heavy | ✅ |
| E2E — llm-chat | ✅ |
| E2E — llm-light | ✅ |
| E2E — llm-agents | ✅ |

**Constraint #7 satisfied:** full CI green including previously failing pre-existing debt (openai_compat, OOXML fixtures, wire-types, tabindex, NestedDelegate).

---

## Prior full-green runs on the same PR stack

| Commit | Run | Result |
|---|---|---|
| `5f93fb0a` shared library cache | https://github.com/elicify-ai/omnipus/actions/runs/30045901449 | ✅ SUCCESS |
| `44da649e` caller-workspace fix | https://github.com/elicify-ai/omnipus/actions/runs/30045518047 | ✅ SUCCESS |
| `edec1f4c` gci import order | https://github.com/elicify-ai/omnipus/actions/runs/30043596627 | ✅ SUCCESS |
| `56093d75` Wave 4 lint closure | https://github.com/elicify-ai/omnipus/actions/runs/30029156405 | ✅ SUCCESS (22/22) |

---

## Integration fixes landed after Wave 4 (UAT-driven)

| Item | Commit(s) |
|---|---|
| Caller workspace into `resolveMediaRefsWithOffload` (FR-028a) | `44da649e` |
| Shared AgentLoop library cache for upload/resolve/REST | `5f93fb0a`, `b185e1ee` |
| github-code-quality writable Close + govet shadow | `5a9ed7d6`, `fc1bad78` |

---

## UAT evidence

- Plan: `docs/internal/uat/ADR-051-rev4-uat-test-plan.md`
- Deviations + live results: `docs/internal/uat/ADR-051-rev4-uat-deviations.md`
- SC log: `docs/internal/uat/ADR-051-rev4-sc-observations.md`
