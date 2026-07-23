# ADR-051 Rev 4 — CI Evidence

**CI Run URL:** https://github.com/elicify-ai/omnipus/actions/runs/30029156405
**Branch:** `sendfile-fix`
**Commit:** `56093d75` — `fix(lint): store_test if-init shadow + labelled→labeled (Wave 4 Linter closure)`
**Date:** 2026-07-23

## Result: ✅ SUCCESS

**Constraint #7 satisfied:** full CI green INCLUDING pre-existing failures (openai_compat HTML error tests, pkg/tools OOXML tests, llm-error.ts wire-type migration, MessageItem tabindex). All fixed during Wave 4 fix-everything.

## Job Results (22 of 22 passed)

| Job | Status | Notes |
|---|---|---|
| Wire-Types Lint | ✅ | Fixed via Wave 0 generator repair + Wave 2 lib-route fix |
| Verify Contracts | ✅ | Fixed via Wave 0 regenerate + Wave 2 lib-route contract update |
| Linter | ✅ | Fixed via Wave 4 lint closure (misspell + golines + govet shadow + gofumpt + godoc) |
| Tests | ✅ | Fixed via Wave 4: openai_compat test assertions, pkg/tools OOXML fixtures, NestedDelegate background concurrency |
| CGO_ENABLED=0 Build Gate | ✅ | pure-Go build verified |
| TypeScript Type Check | ✅ | tsc -b --noEmit clean |
| CLI Removed-Verb Guard | ✅ | |
| E2E shard plan / shard plan check | ✅ | |
| Vitest — components-chat | ✅ | |
| Vitest — components-agents-settings | ✅ | |
| Vitest — components-layout-projects | ✅ | |
| Vitest — lib-store | ✅ | fixed via Wave 2 H MessageItem tabindex |
| Security Check | ✅ | |
| Security Tests | ✅ | |
| Perf Smoke | ✅ | |
| E2E — stubs / ui / ui-heavy / llm-chat / llm-light / llm-agents | ✅ | |

## Wave 4 Fix-Everything Summary

| Pre-existing failure | Fix commit | Fix type |
|---|---|---|
| `src/lib/llm-error.ts` wire-type migration | `90837961` (Wave 0) + `cd06...` | Contract-first migration |
| `src/components/chat/MessageItem.tsx` tabindex | `724cf001` (Wave 2 H) | accessibility fix |
| `pkg/providers/openai_compat` HTML error tests | `98f26f49` | test assertion update to structured ProviderError |
| `pkg/tools` OOXML tests (invalid fixtures) | `64c131d0` | fixture fix (add `[Content_Types].xml`) |
| `TestNestedDelegate_Background` flaky | `9af3d75` | real concurrency fix (spawnSubTurn ctx) |
| golangci-lint: 23 findings | `19aae475` + `47f7155e` + `ddf21ef2` + `56093d75` | British→American spellings, golines, govet shadow, godoc, unconvert, dead-code removal |
