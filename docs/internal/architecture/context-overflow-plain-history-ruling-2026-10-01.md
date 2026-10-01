# Context overflow: plain-history eligibility and recovery-fixture ruling

**Status:** Interim implementation ruling under existing decisions; requested baseline comparisons are still running. Not an implementation, accepted new product ADR, or feature-gate verdict.

**Date:** 2026-10-01 (UTC)

**Scope:** Overflow relief only, #1081. Source tree: `380af72061a3b0c798f1a58b1fe9c71d71cf1228`; branch `investigate/1081-overflow-relief-ruling`. Session provisioning, restart recovery, and the separate breadcrumb investigation are outside this dispatch.

## Bottom line

Fix production eligibility for older completed plain-assistant steps, not the intentional no-progress termination. Also repair the recovery fixtures: some seed the wrong session, and a recovery-success fixture must contain enough legally removable source content to become smaller after required breadcrumb framing. The final test-specific disposition remains pending the requested three-pin executions. [E1–E6, E8–E10]

## Context

### Existing decisions, not a new product choice

The existing ADR titled **Context overflow — the sliding window extended mid-turn, tool results emptied with a recall mark, and a per-result cap at the door**, MAJ-CW-005/012, and its amended specification FR-030/032 already require oldest legal completed-step relief and genuinely smaller retries. MAJ-CW-005 defines a completed step as an assistant message and exactly its declared matching results, and permits completed ordinary narration to leave with the prefix. A zero-call assistant has zero declared results; requiring a tool call merely to recognize its completed narration is an implementation restriction, not the product rule. This interpretation is an engineering judgment grounded in those decisions. [E1, E2]

The floor is unchanged: pinned instructions, one initiating user/media anchor with archive identity, newest assistant structure, incomplete call/result groups, and unconsumed controls remain protected. Do not manufacture independently evictable user messages, truncate the current user's text, loosen correlation validation, or make context rejection consume a protected control. [E1, E2, E3–E6]

### Verified mechanism

```text
no tool-call assistant in retained history
  -> windowSteps returns no steps
  -> slideOldest cannot select a cut
  -> shortenNext finds no tool result
  -> absent removable injected recall, forced checkpoint reports no progress
  -> retryContextOverflow stops with the actual provider rejection
```

The eligibility gap is in relief selection. Stopping on no progress remains correct. The forced checkpoint separately requires original content removal and a strictly smaller normalized serialized retained payload, counting required breadcrumbs/marks and excluding the transient model-only notice. Source removal alone is insufficient. [E3–E6]

### Fixture identity: do not group failures by their names

| Existing test | Source observation at the pinned feature tree | Runtime comparison status |
|---|---|---|
| `TestAgentLoop_EmitsContextCompressEventOnRetry` | Seeds plain history in `session-1` and directly runs that same key. It is a real plain-history eligibility fixture, but uses very short old content. | Prior investigation has a named feature FAIL; this dispatch is not rerunning the independently bisected pair. [E8, E12] |
| `TestContextOverflowRetryDoesNotPublishNormalChatNotice_Q33` | Seeds plain history and directly runs the identical Q33 key. Preserve its ordinary outbound-bus, answer-delivery and retry-event assertions. | Three-pin QA comparison pending completion. [E9, E13] |
| `TestAgentLoop_ContextExhaustionRetry` | Seeds bare `test-session-context`. `ProcessDirectWithChannel` routes through `processMessage`; non-`agent:` keys do not survive `resolveScopeKey`. In this fixture the direct default route is `agent:mia:main`, not the seed bucket. Its unchanged seed length can satisfy the legacy `len < 7` assertion without any relief. | Three-pin QA comparison pending completion. [E7, E10, E13] |
| `TestProcessMessage_ContextOverflowRecovery` | Seeds `agent:main:test-session`, then invokes bare `test-session` with fixture agent Mia. The routed key is `agent:mia:main`; the seeded old messages are not the executed history. | Prior investigation has a named feature FAIL; do not misattribute this to seeded plain-history relief. [E7, E10, E12] |
| `TestProcessMessage_ContextOverflow_AnthropicStyle` | Seeds no retained history; submits `hello` in a fresh fixture, then expects two calls after one context rejection. This is not the reported seeded-key mismatch. | Three-pin QA comparison pending completion. [E10, E13] |

The routing evidence is the complete chain: `ProcessDirectWithChannel` passes the raw input key, `processMessage` resolves its scope before channel-session creation, `resolveScopeKey` preserves only an explicit `agent:` key, the default direct route constructs `agent:mia:main`, and `prepareTurn` uses that resolved scope. This ruling does not change routing or provisioning. [E7]

## Decision

### 1. Extend eviction endpoints without changing tool-group semantics

Backend-lead should add plain completed-assistant endpoints to the existing staged `slideOldest` selection. Prefer a **separate eviction-only enumerator inside the existing checkpoint implementation**, reusing the current tool-group ownership/completeness logic. Keep `windowSteps` tool-only for group validation, pre-turn group-cut checks and tool-result shortening. A proposed helper is a new implementation detail, not a new store, runtime or public API. [E3–E6, E11]

| Selection rule | Required behavior |
|---|---|
| Candidate types | A complete assistant/tool-result group, or an older plain assistant with zero calls/results. User text may leave only as part of a legally removed completed prefix; a user message is not its own eviction endpoint. |
| Ordering | Merge those endpoints in original message/archive order and consider the oldest first. Do not prioritize a later tool group over an earlier completed plain exchange. |
| Plain endpoint | End immediately after that assistant. Existing attachment of consecutive completed plain narration may remain, but never absorb the newest assistant. |
| Group endpoint | End only after exactly all declared matching results; preserve current local call ownership and correlation checks. A malformed/incomplete group is not made evictable by adding text endpoints. |
| Prefix legality | Use the existing source-line mapping, anchor exclusion, protected-control checks, and complete-prefix validation. An unmapped or protected message before an endpoint blocks crossing it; do not jump around it to remove a later island. |
| Floor | Keep the newest assistant even if it is ordinary text from prior history, as the current slide algorithm does. Do not relax this floor just to make a tiny fixture recover. |
| State | Stage the cut and corresponding slice in the same checkpoint; preserve source archive bytes and turn-start restore metadata. No separate plain-text trimming path with its own persistence. |
| Result fallback | `shortenNext` must retain its current older-result/newest-tool-group classification and declared-call order. Appending plain steps to its enumeration must not silently turn newest tool results into older mark-only results. |

Evidence for these constraints is FR-030/031/032, existing `slideOldest` prefix checks, strict `validateWindowGroups`, and the four current `windowSteps` consumers. The implementation shape is recommended to avoid coupling a text-eligibility fix to those other consumers. [E1–E6, E11]

### 2. Preserve the forced checkpoint and retry contract

```text
provider rejects request N
  -> drop injected recall, else oldest legal complete text/tool prefix,
     else oldest eligible older result, else newest result in declared order
  -> rebuild required framing and measure retained serialized payload
  -> if not smaller, stage another operation; do not send or spend a retry
  -> if smaller with real source loss, persist, install, validate and send N+1
  -> if no possible progress or ceiling reached, expose last actual rejection
```

Do not restore unconditional retries, change the byte-progress test, discard required breadcrumbs from its measurement, reset the retry allowance, summarize history, learn a model window from error prose, or invent a local size-only terminal error. The first structurally valid provider attempt still proceeds when immutable estimated residue remains. Persistence failure is a genuine storage failure, not permission to send a divergent in-memory view. [E1, E2, E5, E6, E11]

### 3. Repair fixtures without weakening their behavioral oracles

These are implementation instructions for QA, not claims that any repaired test has run. Final disposition of the three requested comparisons will be added after their receipts are verified. [E13]

| Fixture purpose | Required preparation/oracle |
|---|---|
| Successful text-only recovery | Seed at least two completed old plain exchanges in the exact active scope, with deterministic sufficiently long removable text. Keep local estimates fitting so the actual mock-provider rejection, not proactive trimming, triggers forced relief. Assert the first recorded provider request actually contains seed markers. |
| Entry-path recovery | For `ProcessDirectWithChannel`/`processMessage`, use one explicit `agent:<actual fixture agent ID>:<test suffix>` key for both seed and invocation, or resolve and seed the real routed scope. Do not modify production key semantics to rescue a fixture. |
| Serialization instrument | Record every actual provider attempt; require real seed-source removal and strictly smaller retained serialization, not just `calls == 2`, message count, notice changes or an unchanged unrelated history length. Preserve final answer and event/outbound delivery assertions. |
| Minimal/no-progress request | Preserve a fresh-history Anthropic-style rejection as a separate negative case: one attempt, no recovery/compression event, actual context rejection terminal. A positive Anthropic-style recovery case must separately seed genuinely reducible active history; the provider's error spelling is not a reason to resend unchanged content. |
| Short-history framing | Keep a negative variant where removing eligible short source does not overcome required breadcrumb growth. It must stop without installing staged non-progress changes or sending unchanged content. Do not delete the breadcrumb or weaken the progress check. |
| Archive/state | Check unchanged admitted archive source bytes on a committed relief success; identical reduced live view after reload; actual turn-start restoration on abort. Do not infer archive preservation from live-history length. |

**Important fixture-size caveat (source-derived, not a fixed-code execution):** the eventbus first old user/assistant pair occupies about 90 serialized bytes; Q33's occupies about 93. The current breadcrumb header alone adds 127 JSON string-payload bytes, and `rebuildBreadcrumb` installs it in both system content and system parts. Even before its range/snippet framing, that is at least 254 bytes. Only the first of their two plain assistant endpoints is older than the newest assistant floor. Consequently, adding the missing eligible endpoint does **not** establish that either existing tiny success fixture will satisfy the preserved smaller-payload guard. QA must prove fixture net progress from recorded requests, not assume it. These sizes use the actual `Message` JSON fields and literal fixture/header text; final post-fix payloads remain unexecuted. [E3–E6, E8, E9, E14]

### 4. Acceptance matrix for backend-lead and qa-lead

| Case | Must prove | Source |
|---|---|---|
| Plain-only, locally fitting rejection | Oldest legal completed plain prefix disappears; initiating user/media and newest assistant survive; next retained payload strictly smaller; answer recovered. | FR-030/032; B-54 |
| Mixed plain/tool history | Endpoint ordering follows source order; all retained call IDs, arguments, results and their order remain exact. | FR-030/031; B-57 |
| Consecutive narration | A completed older narration run may leave, but newest assistant cannot be swept into the cut. | MAJ-CW-005; FR-030 |
| No prior completed assistant | User-only/fresh history cannot authorize a text cut or unchanged retry. | FR-030/032; B-56 |
| Tiny text and framing growth | Further staged operations may continue; if no smaller candidate exists, no commit/send/retry event and actual rejection remains. | MAJ-CW-012; B-56 |
| Protected control/unmapped prefix | No cut crosses the blocker; a result fallback may operate only where otherwise eligible. Failed context send does not consume controls. | FR-030/032; B-61 |
| Invalid tool structures | Missing, duplicate, undeclared and orphan result cases remain rejected before repair/serialization; no text cut hides invalidity. | FR-030/031; B-55/57 |
| Result fallback with trailing plain assistant | Plain endpoints do not change older-result emptying versus newest-tool-group halving; Unicode source-rune limits and declared-call order remain exact. | FR-031/032; B-56 |
| Repeat rejection | Every sent retry strictly shrinks retained serialization; existing ceiling remains; terminal cause is the last actual rejection. | FR-032; B-54/56 |
| Metadata write failure/reload/abort | No divergent request on failed write; reduced reload equality; actual start-state restoration; no archive-source rewrite. | FR-030; B-58 |
| Events and Q33 | Retry/compress events follow real progress, normal answer still delivered, no ordinary outbound context notice, classified Verbose behavior retained. | MAJ-CW-008/009; B-59 |

QA should add focused tests in sibling files rather than grow the already budget-pinned monolithic test file. Successful named comparisons of old fixtures alone will not certify these new oracles; RED/GREEN/CHECK and the feature gate remain the squad's responsibility. [E15]

## Alternatives considered

| Alternative | Disposition and reason |
|---|---|
| Restore unchanged retries | Rejected: directly contradicts FR-032/MAJ-CW-012 and masks floor-only fixtures. [E1, E2] |
| Fixture migration only | Rejected as the complete fix: it cannot make valid retained plain narration discoverable to `slideOldest`. Some fixture migration is independently necessary. [E3, E4, E7–E10] |
| Treat every user/assistant message as an independent step | Rejected: opens cuts through the initiating user/control floor and disconnects complete-group ownership. Use completed assistant endpoints and whole-prefix legality instead. [E1–E4] |
| Broaden `windowSteps` everywhere | Not preferred: it has validation, pre-turn and shortening consumers. It could be equivalent only with explicit filtering and regression proof that those semantics do not change; the eviction-only enumerator makes the boundary clearer. [E3, E4, E11] |
| A second plain-text relief subsystem/store | Rejected: duplicates cut/persistence/rollback responsibility; existing checkpoint is the required ownership boundary. [E1, E2, E5, E11] |

## Consequences and affected components

| Type | Consequence |
|---|---|
| Positive | Correctly mapped old text-only conversations can offer legitimate forced relief without requiring prior tools. Eligibility no longer depends on whether the user happened to invoke a tool. [E1–E4] |
| Positive | Existing no-progress protection, archive identity and atomic state remain the same source of truth. [E5, E6, E11] |
| Negative | Tiny or floor-only requests may correctly remain terminal after one provider rejection. A success fixture needs demonstrably net-removable bytes, not arbitrary old messages. [E2, E14] |
| Neutral | No public contract, provider adapter, extension interface, tool policy or runtime dependency change is needed by this ruling. It remains inside existing checkpoint ownership; final adapter coverage remains required. [E1, E2, E5] |
| Delivery | Backend-lead owns staged eviction selection; QA owns fixtures/new oracles. No production or test fix is made by this architecture dispatch. [E3–E11, E15] |

**Founder-level decision:** none is required to restore existing completed-step eligibility and enforce existing progress rules. Changing the newest/user/control floor, deleting framing from the guard or allowing unchanged retries would be a different product decision; this ruling does not authorize it. [E1, E2]

**Public documentation TODOs for the implementing lead:** update `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/docs/memory.md::How long conversations stay in view` to explain that complete older plain-assistant exchanges can leave even without tools; update `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/docs/troubleshooting.md::The provider rejects your model requests` to make clear that short/floor-only history does not guarantee a changed retry. Preserve full-archive/abort caveats, the existing retry ceiling and Verbose-only diagnostic. `docs-verifier` must audit the implementing change; this internal ruling does not fulfill that update. [E16, E17]

Code correct and tested: **Not claimed**; no production/test implementation supplied.

Reachable by a user or agent: **Not certified**; the acceptance matrix requires the existing real provider-send entry paths, not a helper-only green.

Skills: `omnipus-shared-rules`, `gitnexus-exploring`, `claude-api` (loaded earlier; no provider-SDK change in this ruling).

## Evidence table

| ID / Claim | Evidence read in this task | Certainty |
|---|---|---|
| E1 — Binding completed-step/floor/progress decisions | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/docs/internal/architecture/ADR-066-context-budget-and-tool-result-routing.md::Context overflow — the sliding window extended mid-turn, tool results emptied with a recall mark, and a per-result cap at the door`, MAJ-CW-005/006/012 | Verified (high); zero-call interpretation is Inferred engineering judgment |
| E2 — Amended FRs and acceptance | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/docs/internal/specs/adr-066-context-overflow-spec.md::FR-029–032`, `::B-54–61` | Verified (high) |
| E3 — Tool-only enumeration and strict group validation | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/pkg/agent/window_groups.go::windowSteps`, `::validateWindowGroups`, `::mapWindowMessages` | Verified source (high) |
| E4 — Oldest-prefix selection and result classification | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/pkg/agent/window_relief.go::windowCheckpoint.slideOldest`, `::shortenNext`, `::shortenResult` | Verified source (high) |
| E5 — Staged source identity, serialized progress and commit | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/pkg/agent/window_checkpoint.go::newWindowCheckpoint`, `::retainedPayloadSize`, `::AgentLoop.checkpointWindow` | Verified source (high) |
| E6 — No-progress stop and retry events | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/pkg/agent/loop_run_turn_response.go::agentLoopRunTurnResponseCallLLMWithRetries.retryContextOverflow` | Verified source (high) |
| E7 — Actual scope key used by direct/inbound turns | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/pkg/agent/loop.go::ProcessDirectWithChannel`, `::processMessage`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/pkg/agent/loop_inbound.go::resolveScopeKey`, `::extractPeer`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/pkg/routing/route.go::RouteResolver.ResolveRoute`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/pkg/routing/session_key.go::BuildAgentPeerSessionKey`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/pkg/agent/loop_process_message.go::agentLoopProcessMessage.prepareTurn` | Verified source chain (high); runtime key receipts pending |
| E8 — Eventbus fixture and event oracles | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/pkg/agent/eventbus_test.go::TestAgentLoop_EmitsContextCompressEventOnRetry` | Verified source (high) |
| E9 — Q33 fixture and real outbound assertions | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/pkg/agent/loop_context_notice_q33_test.go::TestContextOverflowRetryDoesNotPublishNormalChatNotice_Q33` | Verified source (high) |
| E10 — Other fixture seeds/expectations | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/pkg/agent/loop_test.go::TestAgentLoop_ContextExhaustionRetry`, `::TestProcessMessage_ContextOverflowRecovery`, `::TestProcessMessage_ContextOverflow_AnthropicStyle`, `::newTestAgentLoop` | Verified source (high) |
| E11 — Other enumeration consumer and atomic metadata ownership | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/pkg/agent/window_trim_checked.go::windowCheckpoint.canTrimPrefix`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/pkg/agent/window_projection_effects.go::AgentLoop.commitWindowProjections` | Verified source (high); proposed edit blast radius Inferred — GitNexus unavailable |
| E12 — Prior two named feature failures | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-investigations/cw-cluster-20261001-09c818d8/reproduction-receipts.jsonl`: Recovery/Eventbus exact named FAIL, exit 1 at `380af72061a3b0c798f1a58b1fe9c71d71cf1228` | Verified receipt observations (high); prior bisect not independently rerun |
| E13 — Comparisons are not complete | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-investigations/cw-overflow-provisioning-ruling-20261001/comparison-receipts.jsonl`: read e129 Q33 absent/no named RUN/PASS, exit 0; ContextExhaustionRetry and AnthropicStyle named RUN/PASS, exit 0. Other pins still running on existing QA lane. | Partial verified receipts; final three-pin matrix Unknown |
| E14 — Framing-growth caution | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/pkg/agent/breadcrumb.go::buildBreadcrumb`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/pkg/agent/window_relief.go::windowCheckpoint.rebuildBreadcrumb`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/pkg/agent/window_normalize.go::normalizeWindowMessages`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/pkg/providers/protocoltypes/types.go::Message`; literal-size calculation: header 127, two placements ≥254, removed fixture pair 90/93 bytes | Inferred (high), based on current source/literals; not post-fix runtime evidence |
| E15 — Sibling test-file and delivery rules | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/pkg/agent/CLAUDE.md::Size ceiling`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/CLAUDE.md::Change sizes and the review gate` | Verified instructions (high) |
| E16 — Memory page to update with implementation | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/docs/memory.md::How long conversations stay in view` | Verified current page (high); update not delivered |
| E17 — Troubleshooting page to update with implementation | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-overflow-ruling/docs/troubleshooting.md::The provider rejects your model requests` | Verified current page (high); update not delivered |
| Self-check | Interim artifact checked against read FRs, all five fixture bodies, direct/inbound scope chain, all current `windowSteps` consumers and progress/commit code. Baseline completion and raw-log verification remain explicit prerequisites for the final test ruling; no implementation or feature-gate green is claimed. | Source self-check complete; final artifact/diff/receipts check pending |
