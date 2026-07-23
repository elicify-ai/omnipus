# ADR-051 Rev 4 — CI Evidence

**Branch:** `sendfile-fix`  
**Draft PR:** https://github.com/elicify-ai/omnipus/pull/550  
**Date:** 2026-07-23  

---

## Latest HEAD (acceptance)

| Field | Value |
|---|---|
| **Commit** | `fc1bad78` — `fix(lint): avoid named-return err shadow in copyToWorkDir MkdirAll` |
| **CI Run** | https://github.com/elicify-ai/omnipus/actions/runs/30046828692 |
| **Status** | See job table below (updated when run completes) |

### Prior full-green runs on post-integration commits (same PR stack)

| Commit | Run | Result |
|---|---|---|
| `44da649e` caller-workspace fix | https://github.com/elicify-ai/omnipus/actions/runs/30045518047 | ✅ SUCCESS (all jobs) |
| `5f93fb0a` shared library cache | https://github.com/elicify-ai/omnipus/actions/runs/30045901449 | ✅ SUCCESS (all jobs) |
| `edec1f4c` gci import order | https://github.com/elicify-ai/omnipus/actions/runs/30043596627 | ✅ SUCCESS (all jobs) |
| `56093d75` Wave 4 lint closure | https://github.com/elicify-ai/omnipus/actions/runs/30029156405 | ✅ SUCCESS (22/22) |

**Constraint #7:** pre-existing failures (openai_compat HTML tests, OOXML fixtures, llm-error.ts wire migration, MessageItem tabindex, NestedDelegate concurrency) fixed during Wave 4 and remain green on the post-integration runs above.

---

## Job Results (representative green run `30045901449` / `5f93fb0a`)

| Job | Status |
|---|---|
| Wire-Types Lint | ✅ |
| Verify Contracts | ✅ |
| Linter | ✅ |
| Tests | ✅ |
| CGO_ENABLED=0 Build Gate | ✅ |
| TypeScript Type Check | ✅ |
| CLI Removed-Verb Guard | ✅ |
| E2E shard plan check | ✅ |
| Vitest — components-chat | ✅ |
| Vitest — components-agents-settings | ✅ |
| Vitest — components-layout-projects | ✅ |
| Vitest — lib-store | ✅ |
| Security Check | ✅ |
| Security Tests | ✅ |
| Perf Smoke | ✅ |
| E2E shards | ✅ |

---

## Wave 4 + integration fix summary

| Item | Commit(s) |
|---|---|
| Wire contracts + SPA media surface + orchestrator | Waves 0–3 stack |
| Pre-existing CI debt | `98f26f49`, `64c131d0`, `9af3d75b`, lint series |
| Gateway REST + boot seams | `5d7372ab` |
| Agent-loop integration seams | `6899eca4` |
| Caller workspace into resolver (UAT D1) | `44da649e` |
| Shared library cache upload/resolve (UAT D2) | `5f93fb0a`, `b185e1ee` |
| code-quality Close + govet shadow | `5a9ed7d6`, `fc1bad78` |

---

## UAT evidence

- Plan: `docs/internal/uat/ADR-051-rev4-uat-test-plan.md`
- Deviations + live results: `docs/internal/uat/ADR-051-rev4-uat-deviations.md`
- SC log: `docs/internal/uat/ADR-051-rev4-sc-observations.md`
