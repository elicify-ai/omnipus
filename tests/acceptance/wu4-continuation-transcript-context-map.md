# WU-4 ordinary continuation — transcript-session context RED

## Fixed oracle, before new assertions

A late input that the ordinary worker consumes in this conversation must execute with this conversation's transcript session id, not an empty id or a routing key. In an otherwise permitted web or peer-origin turn the real registered `request_secret` must create one pending card bound to that actual session/account, then park. The originating conversation must retain the executed tool records. A direct turn already supplies that same context and is the positive control.

Sources: the 2026-10-18 continuation-context dispatch; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-wu4-red-origin-20261017/docs/internal/specs/omnipus-system-agent-spec.md` section 6.7 (server-derived session/account binding), section 6.18 (web/peer/channel origin), FR-060/061/062/079/240; and the session/turn module's same-conversation transcript contract. Expected ids come from the fixture's actually created session, never from a turn's observed output. Channel-triggered `request_secret` admission is deliberately NOT an oracle: FR-061 still requires refusal there.

Pinned fresh developer base: `748c23d413697aa647176fb35502e1438ebc17d5`, fetched from `origin/work/omnipus-l1-wu4`. New own branch `work/l1-wu4-red-continuation-20261018`, approved by chief in the same isolated worktree. Completed branch `work/l1-wu4-red-origin-20261017` remains untouched at `72832dda8f4d61b757a6f16d309e6ea00f3b0f3c`.

## Boundary and cases

REAL: ordinary session worker processTurn/enqueue, final turn-end event boundary, production Continue/consumed-input handoff, checked queue consumption, real turn construction and policy dispatch, workload context observer, registered request_secret, registry/card persistence and transcript. Only external model responses are scripted. At the first turn's real turn-end event, while its worker still owns processTurn and before its production drain, the event observer submits the late input through that worker's real enqueue API. This uses no sleeps, private processOptions stamp, global active-map assertion or alternative executor. Queue depth, distinct actual tool turn ids and one exact input in the continuation's provider request prove that the selected path was really taken.

| Case | Expected | Exact selector |
|---|---|---|
| Genuine web follow-up arrives in the worker's closing window | Fresh continuation tool receives exact owning transcript id; one pending card belongs to that id/owner; workload and request_secret calls are persisted in that conversation; card parks without another model call | TestWU4ContinuationRetainsTranscriptContextForWebInput |
| Server-published peer follow-up arrives in the same window | Same exact transcript/card/record behavior; peer label remains `asked by noah`, not channel-triggered | TestWU4ContinuationRetainsTranscriptContextForPeerInput |
| Direct web turn, no continuation | Real observer gets exact id; real request_secret creates one correctly bound pending card, persists both calls and parks | TestWU4DirectTurnCarriesTranscriptContextAndAdmitsSecretCard |

State/negative classes: the two regression rows reject an empty/wrong transcript identity and an unavailable/misbound card; both compare against the direct positive path. No new numeric input boundary belongs to this increment; existing input-size/trust-boundary tests remain unchanged. All expected cardinalities are one tool execution/card/input per selected action, never inferred from observed faulty output.

## Evidence, execution and deferred CHECK

Run ONLY these exact anchored selectors, one tagged Go package and process at a time, through `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/omnipus-l1/slotrun.sh`, lane `wu4qad`, with `CGO_ENABLED=0 GOTOOLCHAIN=go1.26.9 GOMAXPROCS=1 GOGC=100 GOMEMLIMIT=3GiB`. Raw logs, exact commands, direct exits, pinned base, source commit/tree and file hashes go under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/origin-20261017/transcript-context-20261018`. A build/selector/instrument failure is not behavioral RED.

GREEN, mutation and independent CHECK are deferred to a different qa-lead in AUDIT mode. Proposed CHECK mutants: omit the continuation transcript id; point it at another valid session; supply the id but omit its real transcript store (the archived-tool assertions must detect that); or drop peer origin during the repaired handoff. Keep the direct control. Mutation probes run only in the independent CHECK scratch clone, never in this worktree.

Not claimed: a rendered approval-card failure/bypass, private-variable injection, browser/WebSocket UAT, full-suite/CI GREEN, or a production fix. The developer probe `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/opus-dev/wu5gw-probe5-TestWU5SecretOptions_ChannelFlag_GatesInjection.log` was read for context; it includes a dirty implementation tree and is not this increment's runtime evidence. GitNexus MCP is unavailable; current source/caller sweeps are the honest fallback (Inferred scope, no graph safety claim or reindex).

## Observed RED and direct-control receipts

Tests-only execution/published source `6db0decf7393d22bd5d0dd70544e8ff9fc2e1951` on the pinned developer base above. Each selector ran separately through the prescribed serial slot runner and exact environment. No test/assertion changed after these runs.

| Exact anchored selector | Direct exit / named result | Actual behavior |
|---|---|---|
| `^TestWU4ContinuationRetainsTranscriptContextForWebInput$` | 1 / FAIL | Production worker logs `Continuing queued steering after turn end`; real continuation tool session is empty instead of the actual main-session id; registered request_secret returns `secret request session is unavailable`; zero pending cards/snapshot entries and zero continuation tool archive records instead of one each; five model calls instead of four because it fails to park |
| `^TestWU4ContinuationRetainsTranscriptContextForPeerInput$` | 1 / FAIL | Same actual empty-id/card/archive failure through real worker drain, after the actual server-published peer input is consumed once and the peer-origin label assertion succeeds |
| `^TestWU4DirectTurnCarriesTranscriptContextAndAdmitsSecretCard$` | 0 / PASS | Actual tool has the exact main-session id; real request_secret creates one correctly session/account-bound readable pending card; both calls are archived once; the direct turn parks after exactly two model requests |

Complete commands, direct exits, base/commit/tree ids, file hashes and raw log paths: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/origin-20261017/transcript-context-20261018/run-manifest.json`. Each raw `<ExactTestName>.log` has its own `.receipt.json` in that same absolute evidence directory. These are behavioral failures, not build, selector, timeout or setup failures. The input's timing is deliberately precise: the original turn body has retired, but the real worker still owns its original `processTurn` and its steering queue before the production post-turn drain. No claim that the model call itself is still running is made.

Fresh CHECK should verify exact-implementation-head GREEN and run its own scratch-clone mutations against the id, wrong-session binding and transcript-store omission independently. A fix that supplies only ToolTranscriptSessionID but still loses durable tool recording must remain RED on the archive assertions. The direct control must continue to pass; channel-triggered cards must remain refused under FR-061. This RED author does not independently CHECK this suite.
