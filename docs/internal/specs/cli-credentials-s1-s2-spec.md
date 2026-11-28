Status: In review

ADR: "Agents run command-line programs (git, gh, fly …) with stored credentials and guided logins" — proposed, revision 2, `/workspace/logs/1960/adr-cli-tools-credentials.md` (not yet in `docs/internal/architecture/`; its grill: `/workspace/logs/1960/adr-cli-tools-credentials-review.md`)

# Feature Specification: Connected programs — agents run command-line programs with stored logins (slices S1 and S2)

**Created**: 2026-11-22. **Revision 8** (2026-11-22): revision 6 plus the founder's answers of 22 Nov 09:02Z and his correction of 09:08Z (`/workspace/omnipus-coordination/decisions/2026-11-22-founder-cli-spec-and-export-answers.md`). Revision 7's per-agent list is withdrawn: a channel is already bound to one workspace and one main agent, so revision 6's one switch per channel is already per workspace and agent. Revisions are kept at `/workspace/logs/1960/spec-s1/spec-rev1.md` to `spec-rev7.md`. The delta against revision 6 is in `response-7.md`. **Amended 2026-11-24** (architect rulings, no scope change): S-20 wording per `ruling-D10-S20text.md`; FR-239 Windows allowlist row per `ruling-WU19-win.md`; S-50 Then and FR-021 check-result rows per `ruling-H50.md`; FR-021 reason-class note per `ruling-check-delta.md` (2026-11-25); S-106 scan-scope clarification per the same file, "S-83 path rows" (2026-11-25). Unamended revision 8 kept at `spec-rev8.md`; exact lines in `spec-edits-20261124.md`.
**Input**: ADR revision 2 and its grill (above); founder answers Q1–Q4 of 22 Nov 2026 (Discord, quoted in the brief `/workspace/logs/1960/brief-spec-s1.md`). No interview-me output exists for this feature; the founder answers in the brief are the decision record (see Clarifications).
**Code baseline**: `origin/release/v0.1.1` at `d5d1acdd3` (merge of PR #1966, S0), read in `/workspace/wt/a-1960-spec`. Every code citation is `file::symbol` at that commit; those used by revision 3 were re-read for it.
**Epic**: not named in the brief. The matching open feature request is **#1045** ("Sandboxed shell tools cannot authenticate (gh, fly, ...): need a general, tool-agnostic credential mechanism"); related: **#48**. The chief confirms or creates the Epic (Stage: Spec). Not verified on GitHub in this task.
**Riders** (candidates; the chief confirms with the architect):
- **#1044** (HIGH, open): on macOS under Auto, an approved network command could not reach the loopback egress proxy. S1 starts by re-checking it on the current release (H-00) and carries its fix if it still fails.
- **#859** (ADR-092 follow-ups): touches `pkg/tools/shell_permission_mode.go`, which S1 changes.

**Private security note for the chief (not for a public issue):** the existing `/api/v1/secrets` and `/api/v1/secrets/{reference}/options` routes are wrapped with `withAuth` only (`pkg/gateway/rest.go::registerCoreRoutes`), although their comment says "authenticated web user only". The existing `/api/v1/secret-requests/` submit route (`withAuthAndBodyLimit`, `pkg/gateway/rest_secret_requests.go::HandleSecretRequests`) also accepts the person's own API bearer token. For program cards this spec closes that route (FR-028); for other card kinds it does not change policy. Under the founder's rule this goes to the founder first as one plain note.

**Slice marking**: every requirement, story, scenario, dataset row and hands-on row carries **[S1]** or **[S2]**; where a table mixes both, each row says which. S1 can be built, reviewed and landed without any [S2] item. S2 depends on S1.

## The three invariants (closing the class of defect)

Review rounds 1 and 2 each found paths that the previous fix missed. Revision 3 therefore states each security property once as an **invariant**: a rule that holds for every path, current and future. Each invariant has two parts:
- a **structural enforcement**, so that a path which forgets the rule either cannot do the harmful thing or fails closed;
- a **guard test** that fails when a new path is added without the rule.

| # | Invariant | Structural enforcement | Guard test (fails on a forgotten path) |
|---|---|---|---|
| **I-1** Masking at the source (C1 r2, M2 r3) [S1] | Every byte that a child process given a program token writes to stdout or stderr passes through the **program masking writer** before Omnipus buffers, caps, drains, encodes, truncates, stores or derives anything from it. The writer replaces **every run of 8 or more bytes that is a substring of a current or retired program token** (a whole token is one such run). So no fragment of 8 or more bytes leaves it, whether the token is whole, split across writes, or cut off by exit, kill, timeout or a read error. Values derived from the output (account label, sign-in code and link, messages) come only from the masked stream and then pass the label rule. | One function, `programs.Attach(cmd, set)`, is the **only** way to add program variables to a child. In the same call it installs one masking writer per output stream. The writer holds back at most 7 bytes, so its close is always safe: 7 bytes cannot be a protected fragment. | **S-106** `TestProgramSpawnGuard`: (a) a source check lists every `programs.Attach` call and every child-start site in the packages that compose agent environments, and fails on any unlisted site; (b) a runtime check runs every child kind in the **current slice's** inventory with a sentinel and asserts the sentinel reaches no sink. **S-130** interruption cases. |
| **I-2** Provenance only ever adds restrictions, and travels with every handoff, fail closed (C2 r2, C1 r3) [S1] | Provenance is a **set of origins**, each one of `web`, `independent` or `connector:<id>`, or the absorbing value `unresolved`. Joining two provenances is the union of their origin sets. Work is eligible only if **every** origin in its set is allowed by that origin's own setting. Therefore, with settings unchanged, **joining can never turn withheld work into eligible work** (monotone restriction). Every message that starts or feeds a turn, and every **lineage** record that later starts a turn (child session, task, plan, schedule, goal, peer request, background callback), carries a provenance; its zero value is `unresolved`. **Identified standing conversations** (`root`: origin kind `chat`, `channel` or `main`, no parent, no task evidence, not plan-owned) store **no** provenance: a turn in a root is the join of the inputs it actually consumes (R4-M2). Every work session, parentless or not, is `lineage` (R5-M1). An agent write that changes a durable record's instructions joins the writer's provenance into the record in the same write. A person's UI edit **preserves** the record's provenance unchanged (no join). Provenance on a record never shrinks. | `Provenance` is a value type whose zero value is `unresolved`. Only three producers create a resolved value from nothing: channel adapters (`{connector:<id>}`), the web-chat handler (`{web}`) and the independent schedulers (`{independent}`). Every other producer copies or joins. Durable writers take provenance as a required field of their write request. Every lifecycle record has an explicit `provenance_mode`: `root` (no stored lineage) or `lineage` (stored provenance required; missing → `unresolved`). Eligibility is one function over the origin set. | **S-126** `TestProgramProvenance_JoinAlgebra`: exhaustive over all origin sets drawn from {web, independent, connector:a, connector:b} plus `unresolved`, and all 16 setting assignments. It asserts `eligible(join(x, y)) ⇒ eligible(x) ∧ eligible(y)`, that join is commutative, associative and idempotent, and that `unresolved` absorbs. A planted join that drops an origin fails it. **S-111** producer guard; **S-83** origin-kind table, which also maps each kind to `root` or `lineage`; **S-137**/**S-138** root versus lineage. |
| **I-3** One writer for program credentials (C3 r2, M1 r3) [S1] | A program-owned credential entry is created, replaced or removed **only** by the program-scoped store methods (`CommitProgramCredential`, `RemoveProgramCredential`), which only `programs.Publish` and `programs.Disconnect` call. Publishing requires a person-session proof or a gated attempt authorisation. **Every other exported mutating method of `credentials.Store` refuses a program-owned name** with `ErrProgramOwned` and leaves the file unchanged. A program-owned entry is a card secret with recipient kind `program`, or a name equal to one. **And every store mutation, by any method, is one serialized read-modify-write transaction over the latest file**, so an unrelated writer can never write back a stale snapshot of program state (R4-M1). | The guard lives **in the store**, the lowest shared layer, so every existing and future caller (REST, CLI command, onboarding, agent tools, boot clean-up, OAuth refresh) is covered without relying on a list of callers. The existing writers, enumerated by grep, are in the table "Existing raw credential writers". All mutating methods go through one private helper, `s.transact(fn)`: (1) the process-wide per-path mutex, shared by every `Store` object for that path; (2) the cross-process lock on the sidecar `credentials.json.lock`; (3) a **re-read** of the file inside the lock; (4) the mutation; (5) the atomic write, synchronous, with its outcome read back from the file; (6) unlock. Acquisition of (1) and (2) is **bounded and cancellable** (R5-M3): the in-process lock is taken with a deadline, and the cross-process lock is taken non-blocking (`LOCK_EX|LOCK_NB`; on Windows `LockFileEx` with `LOCKFILE_FAIL_IMMEDIATELY`), retried every 10 ms until the deadline. Ordinary writes use a 5 s bound and then fail with `ErrStoreBusy` and no file change. This reuses the existing `fileutil.WithFlock` and `WriteFileAtomic`, with the lock widened from "save only" to the whole read-modify-write. On Windows, `WithFlock` today locks nothing; rev 5 requires a real exclusive lock of the sidecar file there (`LockFileEx` via `golang.org/x/sys/windows`, pure Go). The rename concern in its comment applies to locking the store file itself, not the sidecar. | **S-127** `TestCredentialStore_ProgramOwnedGuard`: (a) reflection over the exported method set of `*credentials.Store`: each method must have a row in the classification table (read-only, guarded mutator, program-scoped, re-encrypt-only, receipt-only); an unclassified method fails the test, which catches new mutators; (b) every guarded mutator is called with a real program-owned reference, and the test asserts `ErrProgramOwned` and an unchanged decrypted store and program metadata; (c) a source check that the program-scoped methods are called only from `pkg/programs`; (d) a source check that every exported mutating method changes the file only inside `s.transact` (no direct `loadFileInternal` … `saveFileNoLock` pair outside it). **S-135** two-`Store` stale-writer barrier and **S-136** cross-process barrier. **S-113** route sweep with route-specific payloads, plus **S-128** and **S-129** (generic REST and agent tool). |

### Existing raw credential writers (I-3 inventory, enumerated by grep at `d5d1acdd3`)

How the list was found:
1. **Mutating methods:** every exported `func (s *Store)` in `pkg/credentials/*.go` (non-test) that calls `saveFileNoLock`.
2. **Callers:** a `grep -rnE '\.(Set|Delete|CommitCard|RemoveReference|SelectPrivateVersion|SetPrivateChannelAvailability|PruneCardReceipts|PruneOrphanCardReceipts|Rotate[A-Za-z]*)\('` over `pkg` and `cmd` (non-test), filtered to credential stores by hand.
3. **Scope:** 39 non-test files hold a `credentials.Store`. That is why the guard sits in the store and does not rely on this caller list being complete. The list exists so RED can assert each known path.

| Store method | Kind check today | Rev 4 classification | Known non-test callers |
|---|---|---|---|
| `Set(name, value)` | none | guarded: refuses a program-owned name | `pkg/gateway/rest_settings.go::setCredential` (`PUT /api/v1/credentials`, `withAuth` + local re-auth); `pkg/gateway/rest_config.go` (provider key save); `pkg/app/internal/credentials/command.go` (CLI `credentials set`); `pkg/app/internal/onboard/onboard.go`; `pkg/providers/oauth_token_source.go` |
| `Delete(name)` | none (removes the card row too) | guarded | `rest_settings.go` (`DELETE /api/v1/credentials/{key}`); `rest_config.go::removeStoredCredential`; `pkg/gateway/gateway_boot_credentials.go` orphan sweep (must treat `ErrProgramOwned` as "skip"); CLI `credentials delete`; `oauth_token_source.go`; `pkg/sysagent/tools/mcp.go` (`remove_mcp_server` env credentials) |
| `RemoveReference(ref)` | none for removal (extra clean-up for private only) | guarded | `pkg/sysagent/tools/secret_remove.go` (agent tool `remove_secret`) |
| `SelectPrivateVersion(ref)` | refuses non-private (`ErrNotPrivateSecret`) | guarded (already refuses; asserted) | `pkg/sysagent/tools/secret_version.go` |
| `SetPrivateChannelAvailability(ref, …)` | refuses non-private | guarded (already refuses; asserted) | `pkg/gateway/rest_secrets.go` |
| `CommitCard(...)` | per recipient | guarded for recipient kind `program`: only `CommitProgramCredential` may write it | `pkg/secretrequest/registry.go::Submit`; for program cards it delegates to `programs.Publish` |
| `PruneCardReceipts`, `PruneOrphanCardReceipts` | receipts only | receipt-only (allowed; never touches values) | `pkg/secretrequest/maintenance.go`, `recovery_inventory.go` |
| `Rotate`, `RotateWithPassphrase` | re-encrypts all | re-encrypt-only (allowed; values unchanged, asserted) | `rest_settings.go` (passphrase change), CLI |
| **new** `CommitProgramCredential`, `RemoveProgramCredential` | — | program-scoped; callers only in `pkg/programs` | `programs.Publish`, `programs.Disconnect` |

## Changes in revision 8 (summary, against revision 6)

| Item | Change |
|---|---|
| Founder S-Q2 and correction | Semantics unchanged: one `programs_allowed` per channel. A channel is bound to one workspace and one main agent (`ChannelInstanceConfig.WorkspaceID` + `Identity{agent}`), so the switch is already per channel, workspace and agent. New: the switch is shown and set on the **Connectors page**, in each channel's panel, which also says which workspace and agent the channel is bound to (read from the existing `GET /channels/{id}/routing`). An unbound channel reaches no agent (its messages are dropped; unbound routing is retired), and the panel says the switch has no effect until it is bound. Rebinding a channel turns its switch off. FR-009; US-5 AS-9; UI row; S-45; new S-148. |
| Founder Q1, Q3, Q4 = A | Recorded as decided: `independent_work_allowed` removed (Q1 = A); FR-037 required (Q3 = A); the card stays as specified (Q4 = A). S-124's setup uses the web-chat setting. |

## Changes in revision 6 (summary)

| Finding | Change |
|---|---|
| R5-M1 | `root` is now only an **identified standing conversation**: origin kind `chat`, `channel` or `main`, no parent, no task evidence, not plan-owned. Every work session, parentless or not (task, isolated scheduled, heartbeat, verifier, plan owner or supervision), is `lineage`, stamped from its work record. Unknown or ambiguous identity is `lineage`, so missing provenance is `unresolved`. A classification table covers every origin and session kind (S-83). FR-050 and S-138 corrected together; new S-143 (parentless task session re-entered from the web, and after restart) and S-144 (every work kind, old records, ambiguous identity). |
| R5-M2 | Conversation Stop and card cancellation are now separate cases at the commit. A card **stopped by its conversation's Stop** while its attempt was bound still accepts that attempt's commit: credential and receipt are written with a continuation veto, the card becomes `submitted` with the existing `ResumeStopped` veto, and no continuation runs. A cancelled, refused, deleted or otherwise ended card refuses the commit. Recovery covers a crash between the receipt write and the card settlement. FR-040; S-98 rewritten; new S-145. |
| R5-M3 | Store-lock acquisition is bounded and cancellable at both layers: the in-process lock and a non-blocking cross-process lock, retried. Ordinary writes wait at most 5 s and then fail with "busy" and no change. A verified commit waits at most until its verifying deadline. After admission it re-checks everything inside the transaction, then claims the attempt `verifying → committing` in one step. The timeout, cancel and supersession make the same claim, so exactly one wins. The write is synchronous inside the held transaction, and its outcome is read back before unlock, so no writer is left running in the background. FR-049, FR-034, the state table and the performance bounds; new S-146 and S-147. |
| R5-N1 | S-135, S-136 and H-17 pause the first writer **inside** its transaction and assert that the second cannot commit until release. The negative control is a mutant that reads outside the lock. |
| R5-N2 | S-142's probe reads the card itself (`GET /api/v1/secret-requests/{id}`, 200 and `pending`) while the login and check barriers are held, plus entry-lock and config-lock probes, each with a planted mutant that must fail. |
| Reviewer note | The security promise "no token written into files" is qualified with its two intended exceptions (encrypted store, S2's protected temporary sign-in folder). |

## Changes in revision 5 (summary)

| Finding | Change |
|---|---|
| R4-M1 | I-3 now also requires every store mutation to be one serialized read-modify-write transaction over the latest file (`s.transact`): a process-wide per-path mutex, then the sidecar file lock, then a re-read inside the lock. Windows gets a real sidecar lock. FR-049; S-135 (two `Store` objects, stale writer), S-136 (two processes); S-127 adds a source check that all mutators use `transact`. |
| R4-M2 | Root versus lineage provenance. Ordinary root conversations store no provenance; each turn there is the join of the inputs it consumes. Child sessions and work records carry lineage provenance; missing lineage is `unresolved`. Explicit `provenance_mode` on lifecycle records. FR-050; S-137 (Telegram→web and web→Telegram in one standing chat, with restart), S-138 (delegated-child control and missing lineage). |
| R4-M3 | Check admission is reserved **under the entry lock in the same step that commits the change** (publication, re-check, check-relevant edit). A launch whose reservation is no longer current exits without stopping anything; only a reservation supersedes. FR-047; S-139 (the exact delayed-launch sequence); D-6 row 6 qualified as check-relevant. |
| R4-M4 | Program-card contract and timing. The card carries a typed `program_id` and, once submitted, the `program_generation` it created (live and restored). The continuation is admitted immediately (existing timing) and says honestly "stored; account check running". The card shows the check result **only while the entry's generation equals its own**; otherwise "Replaced by a newer token". The S2 card-bound sign-in continuation reports the verified account. FR-051; S-54 revised; S-140, S-141. |
| R4-M5 | Card cancellation cancels the bound sign-in attempt. The verified commit for a card-bound attempt takes the registry lock (after the gateway config lock, the existing order), then the entry lock, re-checks that the card is still pending and bound, and only then writes the credential and receipt. The lock is never held over login or check. FR-040; S-142 (cancel after start, cancel at the barrier before commit, commit before cancel). |
| R4-N1 | S-103 observes the pre-cap boundary directly (instrumented capture sink, 80-byte test cap); final delivery is tested separately inside downstream limits. The false "must leak 10 bytes" expectation is removed. |
| R4-N2 | S-124 uses an achievable path: Dev's own heartbeat-created task, later edited by Dev from Telegram. A separate regression proves Dev still cannot edit a person-created task's prompt. |
| R4-N3 | S-81 and D-8 state the body-read expectation per row. |

## Changes in revision 4 (summary)

| Finding | Change |
|---|---|
| R3-C1 | I-2 restated as **monotone restriction**. Provenance is a set of origins (`web`, `independent`, `connector:<id>`); join is union; eligibility requires every origin to be allowed. A person's UI edit preserves provenance and does not join. Exhaustive algebra guard S-126; instance scenarios S-123 to S-125. |
| R3-M1 | I-3 moves into the store: every exported mutating method refuses program-owned names, and program-scoped methods are the only writers. Grep inventory of existing writers (above). Reflection-based classification guard S-127; route sweep S-113 with route-specific valid payloads, a real program reference, re-auth and both edition modes; S-128 (generic REST set/delete), S-129 (`remove_secret`). |
| R3-M2 | The masking writer replaces every run of 8 or more bytes that is a substring of a token and holds back only 7 bytes, so close, kill, timeout and read error can never release a protected fragment. S-130 (interruption after 8, 10 and L − 1 bytes, on stdout and stderr, through read, completion and timeout sinks). |
| R3-M3 | Checks are fenced by `check_revision`, which changes only when check-relevant fields change (account check, token variables). Display-name, words, sites or git edits do not invalidate a running check. A check-relevant edit with a saved credential always starts a new check. S-131. |
| R3-M4 | `programs.Publish` has two forms: **unverified** (paste, card submit: starts one check) and **verified** (S2: carries its check result and preconditions, and commits credential, label, state and optional card receipt in one write, with **no second check**). The commit point is stated; "previous credential kept" applies only before it. S-132. |
| R3-M5 | The claim "exactly once" is withdrawn. The credential and card receipt are committed exactly once; continuation is **at most once**. A crash after the resume claim and before the turn runs leaves the existing restart stop (`recoverOrdinaryRoot`) and no automatic continuation; the person continues the chat. This is the same as every secret card today. S-121 corrected. |
| R3-M6 | FR-028 split: dedicated Programs routes and program-card submissions refuse before reading the body. `PUT /agents/{id}` decodes its bounded body as today and refuses a non-browser caller **only when `programs` is present**, before any mutation. S-134. |
| R3-N1 | S-103 leaves 10 token bytes inside the capture cap, so masking after the cap fails the test. |
| R3-N2 | S-120 releases read-back before its 15 s deadline; the hung case expects the read-back timeout; the 60 s verifying deadline has its own scenario, S-133. |
| R3-N3 | S-106 uses per-slice child inventories: S1 children are mandatory in S1; S2 adds the sign-in children. |

## Changes in revision 3 (summary)

| Finding | Change |
|---|---|
| R2-C1 | I-1: masking moves from returned results to the child's output stream, before capture, cap, draining reads, JSON encoding and truncation. Fragmented tokens are caught by holding back a possible token prefix. Account-label rule (FR-046). Later boundaries B2–B6 stay as defence in depth and gain the JSON-escaped spelling. New S-102–S-106. |
| R2-C2 | I-2: provenance is a required, fail-closed field on every turn-feeding message: peer requests, steering, async completion, goal follow-ups and the system-message path. Provenance on tasks, plans, schedules and goals is joined on every instruction-changing write. New S-107–S-111. |
| R2-C3 | I-3: program-card submissions go through the person-session gate, decided from the **stored** request kind before the body is read. Route-sweep guard test. New S-112, S-113; D-8 rows 10–11. |
| R2-M1 | Crash-safe sign-in launch protocol: durable launch intent, attempt marker in the child's environment, Linux parent-death signal, identity receipt, marker scan at start-up. Windows sign-in stays unsupported. New S-117. |
| R2-M2 | Every recipe subprocess is bounded (read-back 15 s, version probe 10 s, verifying phase 60 s). Cancellation is defined from every active state. One supersession result, `cancelled`, everywhere (S-92 corrected). New S-118–S-120. |
| R2-M3 | Each entry has at most one current check, identified by a check id; a newer check supersedes. A restart or entry edit while `checking` starts a fresh check. New S-114–S-116 (FR-047). |
| R2-M4 | A card-bound sign-in publishes the credential **and the card receipt in one store write**, reusing `CommitCard`'s receipt, so existing recovery turns the card `submitted` after a crash. New S-121. |
| R2-N1 | Typed `cleanup` field on the attempt and `sign_in_warning` on the connection, persisted until a successful sweep. New S-122. |
| R2-N2 | Success criteria and datasets are qualified by slice; mixed test rows are split. |
| R2-N3 | Every scenario has explicit Given/When/Then or uses the named Background. S-81 now takes each row's expected answer from D-8. |

## Agent tool field table

| Object | Property | User can set (UI / REST) | Agent can set (tool) | Reason if different | Parity test (placeholder; qa-lead names it in RED) |
|---|---|---|---|---|---|
| Agent | `programs` (ids switched on) [S1] | Yes: agent profile → Programs; `PUT /agents/{id}` field `programs`, only through the person-session gate | Yes: `set_agent_permissions.programs` (ask-gated; self-change refused), **not** `update_agent` | Same split as tool policies and connectors (AG-07, `pkg/agentmutation/policy.go::agentFieldRules`) | Extend `pkg/tools/t8_parity_tables_test.go` via `agentmutation.MappingRows` / `ExcludedRows` |
| Agent | `programs` on create [S1] | No (all off) | No | Access only through the permission path | Same test, exclusion row |
| Agent | `programs` (read) [S1] | Yes | Yes: `get_agent` | — | `get_agent` output includes `programs` |
| Secret card request | recipient form `program` [S1] | Not applicable | Yes: `request_secret` with `program: "<id>"` | — | request_secret recipient test |
| Program entry, credential, web-chat setting, channel setting [S1]; sign-in attempt [S2] | — | Yes, person-session only | No tool | Person-only on purpose (I-3) | `TestProgramsREST_PersonSessionOnly` (Dataset D-8) |

---

## Overview

Agents can already run command-line programs. What they cannot do today is **log in**. `gh`, `git` over HTTPS, `fly` and similar programs fail or use the wrong account because the sandboxed command has no token, cannot read the macOS keychain, and often cannot reach the program's network sites (ADR §1, C5–C11).

This feature adds a **Programs** page in Settings. There a person connects a program once, by pasting a token into the secure card (**S1**) or by signing in through the program's own browser login (**S2**), and switches the program on for the agents that may use it. When a switched-on agent works in an allowed conversation, Omnipus:
- puts the token into its commands' environment under the program's own variable names;
- makes `git` use that token for the program's site, ignoring other saved git logins;
- lets the program's commands reach its network sites;
- hides the token wherever Omnipus shows or stores output.

The mechanism is **generic**. GitHub and Fly are the first two **presets**, and a preset is a data file. A **Custom program** is first-class: program names, token variable names, network sites, an optional git site, and an optional account check and (S2) sign-in recipe. A setting per channel decides whether agents may use connected programs for work that came from there, including work handed on to helpers, other agents, tasks and continuations.

Hosted and local editions use the same code.

## Existing Codebase Context

> GitNexus MCP tools were not connected in this session. Symbols were read with Read/Grep at `d5d1acdd3`. Caller counts come from a grep sweep (Inferred). The implementing lead runs `impact` on each modified symbol before editing.

### Symbols Involved

| Symbol | Role | Context (read in this task) |
|---|---|---|
| `pkg/credentials/private.go::PrivateEnvironment` | extend / model | Selected private variables per (workspace, agent); channel opt-in per variable. |
| `pkg/credentials/card.go::CardRecipient`, `CardReceipt`, `Store.CommitCard`, `SensitiveValues` | extend / reuse | A `CardReceipt` "is published in the SAME credentials.json write as its values". New recipient kind `program`. Card-bound sign-in reuses the receipt (FR-040). |
| `pkg/secretrequest/recipient.go::ResolveRecipient` | extend | New recipient form `program`. |
| `pkg/secretrequest/registry.go::Registry.Create`, `Registry.Submit` | extend | `Create` is chat-session-bound. `Submit` validates 4–16,384 bytes, commits, marks `submitted` and starts the continuation. For the `program` kind it delegates to `programs.Publish` (FR-044). |
| `pkg/secretrequest/recovery_inventory.go::Registry.recoverPendingLocked`; `pkg/secretrequest/recovery.go::Registry.ClaimResume` | reuse | A receipt restores `submitted`; no receipt yields `interrupted`. `ClaimResume` **admits** a submitted request's continuation at most once and honours Stop. It does not guarantee that the admitted turn runs (R3-M5). |
| `pkg/gateway/rest_secret_requests.go::restAPI.HandleSecretRequests` | extend (C3) | Reads the submit body **before** looking up the stored request and its owner; no cookie-session requirement. |
| `pkg/gateway/auth.go::resolveBearerIdentity`, `matchUserBearer` | read (C3) | A configured person's API bearer resolves to that person's account. |
| `pkg/credentials/store.go::Store.Set`, `Store.Delete`; `pkg/credentials/private.go::RemoveReference` | extend (R3-M1) | Write or remove any name in the shared credential map with no recipient-kind check. Rev 4 adds the program-owned guard. |
| `pkg/gateway/rest_settings.go::setCredential` (and the delete handler); `pkg/gateway/rest_integrations_auth.go::requireReAuth` | read (R3-M1) | `/api/v1/credentials*` use `withAuth`. Writes need a local re-auth token, which `requireReAuth` skips in platform mode; it is not a browser-session proof. |
| `pkg/sysagent/tools/secret_remove.go::RemoveSecretTool` | read (R3-M1) | Agent tool that calls `RemoveReference`. |
| `pkg/agent/revive_inbound.go::runInboundTurnWithRevival`; `pkg/secretrequest/recovery.go::resumeLocked`; `pkg/agent/boot_ordinary_recovery.go::recoverOrdinaryRoot` | read (R3-M5) | `ClaimResume` durably appends the resume entry **before** `runAgentLoop`. An existing entry suppresses re-dispatch. Boot recovery "never dispatches a turn"; it stops the interrupted root. |
| `pkg/gateway/rest_agents_update.go::validateRequest` | read (R3-M6) | Reads the bounded body (`readAgentWriteBody`) before field inspection. |
| `pkg/tools/request_secret.go::RequestSecretTool.Execute` | extend | Parks the turn until the card is settled. |
| `pkg/agent/private_env.go::privateEnvForTurn` | extend | Per-turn resolution from the trusted turn context. |
| `pkg/tools/private_env.go::PrivateEnvSource`, `withPrivateVariable` | extend | Seam by which bash and environment_setup receive extra variables. |
| `pkg/tools/shell.go::ExecTool.executeRun`, `foregroundResultFromSandbox`, `truncateOutput` | extend (C1) | Foreground output is truncated before the `ToolResult` is built. |
| `pkg/sandbox/hardened_exec.go::cappedBuffer.Write` | extend (C1) | Bytes beyond the cap are discarded at capture time. |
| `pkg/tools/shell_bg.go::ExecTool.runBackground`, `executeRead`, `backgroundCompletionResult` | extend (C1, M1) | Background pipes feed `appendOutput`; `executeRead` JSON-encodes the drained output; the PID is known only after start. |
| `pkg/tools/session_activity.go::ProcessSession.appendOutput`; `pkg/tools/session.go::ProcessSession.Read` | extend (C1) | Bounded retention at `maxOutputBufferSize`; `Read` drains the buffer. |
| `pkg/agent/loop_run_turn_tools.go::handleAsyncResult`, `toolResultUserContent`, `asyncToolResultUserContent`, `persistAsyncToolErrorNotice` | extend (defence in depth) | User feedback, sync and async; persisted async error notice. |
| `pkg/agent/async_notifier.go::asyncNotifierImpl.Notify` | extend (C1, C2) | Truncates before the switchable filter; emits `Channel: "system"` with destination metadata and **no origin**. |
| `pkg/agent/loop_inbound.go::AgentLoop.processSystemMessage` | extend (C2) | Builds `processOptions` without `InputOrigin`, then runs an ordinary turn or `runOrdinaryGoalFollowUp`. |
| `pkg/agent/tool_result_admit.go::admitToolResult` | extend | Admitted-result filter (defence in depth). |
| `pkg/config/security.go::sensitiveReplacementPairs`, `RegisterSensitiveValues`, `EnsureSensitiveValues` | extend | Replaces the original and Latin-1-escaped spellings; values ≤ 3 bytes skipped. |
| `pkg/tools/turn_origin.go::TurnOrigin`; `pkg/agent/turn_origin.go::inboundInputOrigin` | extend (C2) | Records only the first channel connection today. |
| `pkg/agent/address_router.go::requestWakeMessage` | extend (C2) | Peer wake as `Channel: "webchat"`, no instance id. |
| `pkg/agent/steer_reconstruct.go::reconstructSteeredTurn`; `pkg/agent/loop_run_turn.go::registerTurnContext` | extend (C2) | Steered turns get a new origin; attendance is carried. |
| `pkg/tools/task_update_fields.go::checkDefinitionAuthority`, `applyOrganisationFields`; `pkg/tools/task.go::persistUpdate`; `WriteTaskReconciled` | extend (C2) | The creator agent may change `prompt` and other definition fields; one reconciled write. |
| `pkg/session/lifecycle.go::LifecycleRecord.Unattended`; `pkg/agent/turn_attendance.go::classifySessionChain` | model (C2) | Durable-at-launch precedent; 16-hop walk, fail toward refusal. |
| `pkg/session/lifecycle_edge.go` `OriginKind*` | inventory (C2) | `delegate`, `task`, `chat`, `channel`, `main`, `scheduled`, `heartbeat`, `verifier`, `plan`, `human`. |
| `pkg/gateway/rest.go::registerCoreRoutes` | correct (C3) | Secrets and secret-request routes are not person-gated; third-party settings uses `withAuth(middleware.RequirePersonSession(requireSessionCookieAuth(h)))`. |
| `pkg/gateway/middleware/person_session.go::RequirePersonSession`; `pkg/gateway/rest_third_party_settings.go::requireSessionCookieAuth`; `pkg/gateway/middleware/csrf.go` | reuse (C3) | Person gate; bearer refusal; CSRF. |
| `pkg/config/config.go::ChannelInstanceConfig` | extend | Per-instance setting with a dedicated writer. |
| `pkg/tools/preflight.go::ClassifyNetworkNeed`, `networkCapableBinaries`, `resolveShellHead` | extend / read | No `fly`; `bash -c` head is not flagged. |
| `pkg/tools/shell_path_guard.go::pathUseClassifier.commandWordRoles` | reuse | S0 wrapper-aware program-word finder. |
| `pkg/tools/shell_permission_mode.go::enforceShellPermissionMode`, `enforceNetworkPreflight`, `grantEgressHosts`; `pkg/tools/shell.go::turnKernelPolicy` | extend / read | Network grant and kernel posture only in Auto. |
| `pkg/tools/shell_env.go::composeExecutionEnv` | read (M4 r1) | Runtime bins precede the host PATH (confirmed by review round 2, lines 251–281). |
| `pkg/fspolicy/secretset.go::SecretEntriesAlways` | reuse | `system` is always denied to agents. |
| `pkg/childenv/private_names.go::CheckPrivateVariableName` | extend | Grammar and protected names. |
| `pkg/childenv/allow_windows.go` (child allowlist, FR-239 of `docs/internal/specs/omnipus-system-agent-spec.md`) | extend (WU-19 ruling, 2026-11-24) | FR-239 on Windows: on Windows the allowlist also admits the Windows system variables (`SystemRoot`, `SystemDrive`, `windir`, `ComSpec`, `PATHEXT`, `TEMP`, `TMP`, `USERPROFILE`), case-insensitively; Linux and macOS are unchanged (`allow_other.go`). |
| `pkg/omnpkg/export.go::cleanRecordForExport`; `pkg/omnpkg/format.go::NeedsSecret` | extend | Export. |
| `pkg/agentmutation/policy.go`; `pkg/sysagent/tools/agent_permissions.go::AgentPermissionsTool` | extend | Parity; AG-07; self-change refusal. |
| `pkg/thirdpartycli/path.go::CheckProgramPath` | reuse | Validates gateway-run program paths. |
| `pkg/gateway/rest_sign_in.go::deviceCodeSession`; `src/components/providers/SignInDialog.tsx` | model [S2] | 15-minute cap; dialog phases. |
| `src/components/chat/SecretRequestCard.tsx` | reuse | Card value rules and promise text. |
| `scripts/check-no-cli-model-providers.sh` | constraint | Bans the retired subprocess-backed model-provider names and routes (see the guard script for the list) |

### Impact Assessment

| Symbol Modified | Risk Level | d=1 Dependents (grep, Inferred) | d=2 |
|---|---|---|---|
| Child output capture (`cappedBuffer`, `appendOutput`, background pipes) | **HIGH** | every bash and environment_setup child | all tool output |
| `enforceShellPermissionMode` / `enforceNetworkPreflight` | **HIGH** | `executeRun` | every agent's bash |
| Turn-feeding messages (`bus.InboundMessage`, async notify, steer, peer request) and durable work writers (task, plan, schedule, goal, lifecycle) | **HIGH** | all turn starts | session restore, schedulers |
| `HandleSecretRequests` ordering | **HIGH** | every secret card submit | chat cards |
| `credentials.Store` mutators (program-owned guard) | **HIGH** | 39 holder files; known writers in the I-3 inventory | provider keys, channel tokens, MCP env, OAuth, onboarding, CLI, boot sweep |
| Async delivery (`handleAsyncResult`, `Notify`, `toolResultUserContent`) | **HIGH** | user feedback, continuations | all channels |
| `CheckPrivateVariableName`, `ResolveRecipient`, `Registry.Submit` | MEDIUM | request_secret, reference tools | cards |
| `admitToolResult` filter | MEDIUM | every tool result | transcript |
| `AgentPermissionsTool` / `agentmutation` rows | LOW–MEDIUM | parity test | Omnipus agent |
| `cleanRecordForExport` | LOW | `omnpkg.Export` | export |

HIGH rows need sign-off before GREEN, and security-lead covers them. Because the change touches the agent engine, exact-commit hands-on UAT applies.

---

## Contract Changes (contract-first — Hard Constraint #8)

| Schema | New / Changed | Slice | Shape |
|---|---|---|---|
| `ProgramEntry` | new | S1 | `id`, `kind` (`preset` \| `custom`), `display_name`, `program_words[]`, `token_variables[]`, `network_sites[]`, `git?` (`ProgramGit`), `identity_check?` (`ProgramIdentityCheck`), `sign_in_recipe?` (`ProgramSignInRecipe`, S2, full for custom entries, read-only for presets), `sign_in` (`available`, `reason`; S2), `connection` (`ProgramConnection`), `enabled_agent_ids[]`, `revision` |
| `ProgramGit` | new | S1 | `host` (canonical: lower-case, no trailing dot, no port or scheme), `username` (`^[A-Za-z0-9._@-]{1,64}$`, default `x-access-token`) |
| `ProgramIdentityCheck` | new | S1 | `program_word` (one of the entry's words), `args[]` (0–16 literals, each ≤ 256 chars, never shell-parsed), `timeout_seconds` (1–30, default 20), `max_output_bytes` (fixed 4,096). Label rule FR-046. |
| `ProgramSignInRecipe` | new | S2 | `program_word`; `platforms[]` (`linux` \| `darwin`; `windows` refused until proven, FR-039); `min_version?` (`args[]` ≤ 16, `pattern` RE2 one group, `minimum` `major.minor`, fixed timeout 10 s); `config_dir_variable`; `extra_env` (0–8 name → literal ≤ 256); `login_args[]` (≤ 16); `readback_args[]` (≤ 16; fixed timeout 15 s); `link` (`scheme` fixed `https`, `host` canonical, `path_prefix` starting `/`); `code_pattern` (RE2 ≤ 256, one group, code 4–32 chars); `denied_patterns[]` (0–4 RE2 ≤ 256); `expiry_seconds` (60–900, default 900); fixed: start 15 s, verifying 60 s, output 65,536 bytes |
| `ProgramConnection` | new | S1 | `state` (`not_connected` \| `checking` \| `connected` \| `saved_not_checked` \| `check_failed`), `generation` (≥ 0), `account_label`, `checked_at`, `message` (value-free, ≤ 500), `sign_in_warning?` (S2: `{ message, since }`, present while an attempt's cleanup has not been confirmed) |
| `ProgramList` | new | S1 | `entries[]`, `web_chat_allowed`, `revision` (founder Q1 = A: independent work follows `web_chat_allowed`) |
| `ProgramCustomRequest` | new | S1 | `display_name`, `program_words[]`, `token_variables[]`, `network_sites[]`, `git?`, `identity_check?`, `sign_in_recipe?` (S2; founder Q3 = A), `revision` (edit) |
| `ProgramCredentialRequest` | new | S1 | `{ value, expected_generation? }`; 8–16,384 bytes; `additionalProperties: false` |
| `ProgramSettingsUpdate` | new | S1 | `web_chat_allowed`, `revision` |
| `ChannelProgramsSetting` | new | S1 | `allowed`, `revision` |
| Agent read/update schemas of `/agents/{id}` | changed | S1 | add `programs: string[]` (≤ 32) |
| `SecretRecipientDisplay.kind`, `SecretNameEntry.kind` (and asyncapi mirror) | changed | S1 | enum gains `program` |
| `SecretRecipientDisplay` (REST schema and asyncapi mirror) | changed | S1 | adds `program_id` (string): **required when `kind` is `program`, absent otherwise**. The card's program identity is never inferred from display lines or reference names (R4-M4) |
| `SecretRequestState` (REST) and `SessionStateSecretRequest` (asyncapi) | changed | S1 | adds `program_generation` (integer ≥ 1): present when the request is `submitted` and its recipient kind is `program`; it is the generation the submission (or card-bound sign-in) committed, taken from the `CardReceipt` so it survives restart. `CardReceipt` gains `program_generation` in the same store write |
| `POST /secret-requests/{id}/submit` responses | changed | S1 | adds 403 `this action requires a signed-in person` when the **stored** request kind is `program` and the caller is not a person browser session |
| `PUT /credentials`, `DELETE /credentials/{key}` responses (existing generic routes) | changed | S1 | add 409 `<reference> belongs to the program <name>. Change it in Settings → Programs.` when the key is program-owned; no store change |
| `remove_secret` tool result | changed | S1 | same refusal text for a program-owned reference |
| `ProgramSignInStart` | new | S2 | `{ secret_request_id? }` |
| `ProgramSignInAttempt` | new | S2 | `attempt_id`, `state` (transition table), `generation_at_start`, `verification_url?`, `user_code?`, `expires_at`, `account_label?`, `message`, `secret_request_id?`, `cleanup` (`pending` \| `done` \| `failed`; required) |

**New REST paths.** All are tag `Programs`, wrapped `withAuth(RequirePersonSession(requireSessionCookieAuth(h)))` behind the existing CSRF middleware, and audited value-free.

| Path | Method | Slice | Purpose | Main responses |
|---|---|---|---|---|
| `/programs` | GET | S1 | List | 200 `ProgramList`; 401 no identity; 403 not a person session; 503 store unavailable |
| `/programs` | POST | S1 | Create custom | 201; 400 rule; 409 name taken or 33rd entry |
| `/programs/{id}` | PUT / DELETE | S1 | Edit / delete custom | 200 / 204; 404; 409 stale revision; 409 conflict for an agent it is on for; 409 delete while on; 422 preset |
| `/programs/{id}/credential` | PUT | S1 | Save a token; starts a check | 200 `ProgramEntry` (`checking`, new generation); 400 value rule; 409 stale `expected_generation`; 413; 500 save failed, previous kept |
| `/programs/{id}/credential` | DELETE | S1 | Disconnect | 200 (new generation); 404 |
| `/programs/{id}/check` | POST | S1 | Re-check (supersedes a running check) | 202 `ProgramEntry` (`checking`) |
| `/programs/settings` | PUT | S1 | Web-chat setting | 200; 409 |
| `/channels/{id}/programs` | GET / PUT | S1 | Channel setting | 200; 404; 409 |
| `/programs/{id}/sign-in` | POST | S2 | Start attempt | 202; 409 card not pending / not this program / not this person; 422 no recipe for this platform |
| `/programs/{id}/sign-in/{attemptId}` | GET | S2 | Poll | 200 (a superseded attempt reads `cancelled` for 10 min); 404 unknown |
| `/programs/{id}/sign-in/{attemptId}/cancel` | POST | S2 | Cancel (any active state) | 200 |

No asyncapi frame is added.

## API and Data

- **Catalog [S1].**
  - Presets are data files shipped in the binary, one per preset; adding one needs no Go change.
  - Custom entries and the web-chat setting live in a `programs` section written only by Programs endpoints. The generic config writer and `set_config` refuse it.
- **Credential [S1].**
  - One credential per entry per installation (founder Q1 A): a card-secret version with recipient kind `program` in the encrypted store.
  - It is saved only by `programs.Publish` (I-3), through the program-scoped store method `CommitProgramCredential`. Publishing has two forms (R3-M4):
    - **Unverified** (Settings paste, chat-card submit):
      1. register masking for the new value;
      2. take the per-entry lock;
      3. check `expected_generation` if given;
      4. write the value, plus a `CardReceipt` when card-bound, and `state: checking` with the new `generation` in **one** store write (the commit point);
      5. reserve the check for this generation (FR-047), still under the lock;
      6. release the lock and launch that reserved check.
    - **Verified** (S2 sign-in): the attempt has already registered masking for the candidate and checked it. Then:
      1. take the per-entry lock (for a card-bound attempt, first the gateway config lock and the registry lock, FR-040);
      2. enter the store transaction, waiting **at most until the verifying deadline** (R5-M3); if admission fails, nothing is written and the attempt ends `failed` (deadline);
      3. **inside the transaction**, verify the preconditions: the attempt is still current and inside its verifying deadline, `generation` is unchanged since the attempt started, `check_revision` is unchanged since its check and, when card-bound, the card admits the commit (FR-040);
      4. claim the attempt `verifying → committing` in one atomic step. The deadline timer, a cancel and a supersession make the same claim from `verifying`, so exactly one of them wins. If the claim fails, nothing is written;
      5. write the value, `state: connected` with the checked label, the new `generation` and, when card-bound, the `CardReceipt`, in **one** synchronous store write (the commit point). Its outcome is read back from the file before unlock: the new generation present → `connected`; absent → `failed` with the previous credential kept;
      6. **no further check runs**.

      If a precondition fails, nothing is written and the attempt ends `cancelled` (superseded) or `failed` (deadline).
    - **Before the commit point** a failure keeps the previous credential. **After it** the new credential is in use; later steps (card settlement, continuation) are recovered from the receipt (FR-040). The UI never reports "previous token kept" for a committed change.
- **Credential generation [S1].**
  - `generation` increments on every publication and on disconnect, under the per-entry lock. Every custom-entry edit increments the entry `revision` (used for optimistic concurrency of edits only).
  - **`check_revision`** increments only when a **check-relevant** field changes: `identity_check` or `token_variables` (R3-M3). Display name, program words, network sites and git do not change how the check runs, so they leave it unchanged.
  - A check result or sign-in publication is accepted only for the `generation` and `check_revision` it started with, and only from the entry's current check id (FR-047).
  - Paste or disconnect cancels a running sign-in attempt. A lost response is never resent automatically; the UI re-reads.
- **Check lifecycle [S1] (FR-047).**
  - Each entry has at most one **current check reservation** `{check_id, generation, check_revision}`, held in memory and persisted with the entry's state.
  - **Admission (R4-M3):** a reservation is made only **under the entry lock, in the same step that commits the change that needs it**. That means the unverified publication's store write, `POST …/check`, a check-relevant edit, or the start-up recovery of a persisted `checking`. Making a reservation supersedes the previous one (its process group is stopped, its result discarded, audited as `superseded`).
  - **Launch:** the check process is started **after** the lock is released, carrying its reservation. If the reservation is no longer current when the launch starts, or when its result arrives, the launch exits without starting a process, or the result is discarded. **A launch never stops or supersedes anything**; only making a reservation does.
  - These start a check, whenever a credential is saved: an unverified publication; `POST …/check`; any edit that increments `check_revision` (whatever the current state, `checking` included); and gateway start for every entry persisted as `checking`. Edits that do not change `check_revision` never invalidate a running check, so its result still publishes and no state is stranded.
  - A check always ends within its timeout + 5 s, with `connected`, `check_failed` or `saved_not_checked`.
- **Agent switch [S1].** The agent record gains `programs`. A REST change is accepted only through the person-session gate; the tool path is `set_agent_permissions`.
- **Channel setting [S1].** Each channel instance gains `programs_allowed` (absent = false).
  - A channel instance is one connected bot or number, bound to **one workspace and one main agent** (`ChannelInstanceConfig.WorkspaceID` and `Identity{kind: agent, id}`, `IsWorkspaceBound`). Its messages reach only that agent (`RouteResolver.ResolveRoute`). So the one switch already means "this channel, this workspace, this agent", and work that agent hands on keeps the channel's origin (I-2).
  - An unbound instance has no agent: `ResolveRoute` drops its messages (unbound routing is a retired surface), so its switch has no effect until the channel is bound.
  - Rebinding a channel to another workspace or agent (`PUT /channels/{id}/routing`) sets `programs_allowed` to false in the same config write, so a switch never carries over to an agent the person did not choose (revision 8).

### Program provenance (I-2) [S1]

**Provenance** is either `unresolved` (the zero value) or a non-empty **set of origins**. Each origin is one of:
- `web`: a person typing in the web chat;
- `independent`: work with no starting message (founder Q1);
- `connector:<id>`: a channel connection, such as Telegram or Discord.

It is server-derived only; model text never sets it.

**Join** (R3-C1):
- `join(a, b)` = `unresolved` if either side is `unresolved`;
- otherwise the **union** of the two origin sets.

No origin is ever dropped or converted: `web` stays `web` and `independent` stays `independent` next to a connector.

**Eligibility (FR-010):** a turn is eligible only if its provenance is not `unresolved` **and every origin in its set is allowed now**:
- `web` → the web-chat setting;
- `independent` → the web-chat setting (founder Q1 = A, decided 22 Nov);
- `connector:<id>` → that connection's setting (an unknown connection counts as not allowed).

The agent's own switch is always required as well.

**Monotone restriction (the I-2 property):** for any provenances x and y and any fixed settings, `eligible(join(x, y))` implies `eligible(x)` and `eligible(y)`. Adding an origin can only take eligibility away. Example from round 3: web chat off and Telegram on gives `join({web}, {connector:telegram}) = {web, connector:telegram}`, which is **withheld** because `web` is off, in either order.

**Who creates a value from nothing (the only three):**

| Producer | Value |
|---|---|
| Channel adapter delivering an inbound message | `{connector:<id>}` |
| Web-chat message handler (a person typing) | `{web}` |
| Heartbeat scheduler; a task, plan or schedule a person creates in the UI; their runs | `{independent}` |

**Everyone else copies or joins.** Each handoff carries the provenance of the turn (or record) that produced it:

| Handoff | Where provenance is carried | Read by the next turn |
|---|---|---|
| Helper, delegated or steered session launch; any work session (task, scheduled, heartbeat, verifier, plan) | `LifecycleRecord.Provenance` with `provenance_mode: lineage`, stamped at creation from the launching turn or work record (like `Unattended`) | every turn of that session joins it (wake, restart) |
| Steering message into an existing session | the steer message's provenance | joined into the receiving turn |
| Peer request (`requestWakeMessage`) | request ledger record | joined into the receiving turn, also after restart |
| Background command started by a tool call | the `ProcessSession` and the async callback | `AsyncNotifyEvent` → `bus.InboundMessage.Provenance` → `processSystemMessage` → `processOptions` → `registerTurnContext` |
| Goal follow-up (`runOrdinaryGoalFollowUp`, `dispatchGoalAsyncFollowUp`) | the goal record | joined into the follow-up turn |
| Task, plan, schedule, goal created by a tool call | the record, at creation | every run joins it |
| **Any agent write that changes a durable record's instructions** (task `prompt`, `description`, `criteria`, `dod`, `trigger`, `agent_id`; plan steps; schedule command or prompt; goal definition) | `record.Provenance = join(record.Provenance, writer turn's provenance)` **in the same reconciled write** | every later run |
| The same instruction change by a person in the UI | `record.Provenance` **preserved unchanged**: no join, no reset (R3-C1) | every later run |
| Judge (`verifier`) run | the verified work's provenance | — |

**Root versus lineage (R4-M2).** Every lifecycle record has an explicit `provenance_mode`:
- **`root`**: an **identified standing conversation** that accepts separate turns: a web chat, an agent's main chat, or a per-person channel conversation. A root stores **no** provenance, so nothing accumulates across separate turns.
- **`lineage`**: everything else: every child, delegated or steered session, **every work session whether or not it has a parent** (task, isolated scheduled, heartbeat, verifier, plan owner and supervision sessions), and every durable work record (task, plan, schedule, goal, peer request, background callback). Its stored provenance is required. Missing, unreadable or empty lineage is `unresolved`, never a permissive fallback.

**Classification (R5-M1).** "No parent" alone does not identify a standing conversation: task and isolated scheduled sessions are created without a parent too (`createTaskSessionSyncAs`, `NewScheduledSession`). The mode is decided by the same work-identity evidence the approval classification already uses (`classifySessionChain`), in this order; the first matching row decides:

| # | Evidence (session metadata and lifecycle record) | Mode | Where the stored provenance comes from |
|---|---|---|---|
| 1 | metadata unreadable, or lifecycle origin missing | `lineage` | none → `unresolved` |
| 2 | `ParentSessionID` set, or origin kind `delegate` | `lineage` | stamped at launch from the launching turn |
| 3 | metadata `TaskID` set (any type, including a task run as a child of a main chat) | `lineage` | the task record's provenance at dispatch (`mintTaskLifecycleRecordFor`) |
| 4 | the plan engine owns the session (owner or supervision) | `lineage` | the plan record's provenance |
| 5 | origin kind `task`, `plan`, `scheduled`, `heartbeat` or `verifier` (the existing `unattendedOrigin` set) | `lineage` | the work record (task, plan, schedule) at creation; heartbeat `{independent}`; verifier = the verified work's provenance |
| 6 | origin kind `chat`, `channel` or `main` | `root` | none stored |
| 7 | any other kind (for example `human`, or a kind added later) | `lineage` | none unless a producer row exists → `unresolved` |

The mode is written once, when the record is created, and is never changed afterwards. A record written before this feature is classified by the same table when it is loaded. A pre-feature `lineage` record has no stored provenance, so it is `unresolved` (fail closed). The S-83 table test lists every `session.OriginKind` and every `UnifiedSessionType`, each with exactly one mode row; a kind without a row fails the test.

**Re-entry.** A later input into a `lineage` session, such as a person's web message into a task session, a system wake or a restart revival, joins the session's stored provenance. It never replaces it. This matches `applyTurnAttendance`, which keeps a work classification on re-entry.

**A turn's provenance:**
- **in a root**: the join of the provenance of every input the turn consumes (inbound messages, the heartbeat trigger, steer messages, async completion messages, consumed peer requests). Nothing from an earlier, separate turn of the same root is carried forward. Example: in one main chat, a Telegram turn is `{connector:telegram}` and a later turn started by the person typing on the web is `{web}`.
- **in a lineage session**: the join of the session's stored provenance and every input the turn consumes.

A lineage chain that is unreadable, cyclic or longer than 16 hops is `unresolved`. A turn with no consumed input and no lineage (impossible by construction) is `unresolved`.

**Never shrinks:** only deleting a record and creating a new one in the UI starts fresh (the new record is `{independent}`).

**Declared limit (not a hole in the invariant):** provenance follows **dispatch handoffs**, not conversation history. In a person's later web-chat turn in a session that also holds an earlier Telegram message, the earlier text is history, not a handoff. The person's turn is `{web}`. The review asked not to build general content tainting (grill-2, R2-C2 "Concrete fix"). DOC-005 states this.

### Preset data (initial values) [S1]

Site lists, checks and recipes are **[ASSUMPTION]** until the G2/G3 spikes; they are data.

| Field | GitHub | Fly |
|---|---|---|
| id | `github` | `fly` |
| program words | `gh`, `git` | `fly`, `flyctl` |
| token variables | `GH_TOKEN`, `GITHUB_TOKEN` | `FLY_API_TOKEN` |
| network sites | `github.com`, `api.github.com`, `uploads.github.com`, `codeload.github.com`, `objects.githubusercontent.com`, `raw.githubusercontent.com` | `api.fly.io`, `api.machines.dev` |
| git | host `github.com`, username `x-access-token` | none |
| identity check | `gh` `["api","user","--jq",".login"]` | `fly` `["auth","whoami"]` |
| sign-in recipe [S2] | `gh`; platforms `darwin`, `linux` (each only if G2 passes); `GH_CONFIG_DIR`; login `["auth","login","--web","--hostname","github.com","--git-protocol","https","--insecure-storage"]`; read-back `["auth","token","--hostname","github.com"]`; link `https` / `github.com` / `/login/device`; code `one-time code: ([A-Z0-9]{4}-[A-Z0-9]{4})` | none |

### Where program tokens are hidden (I-1) [S1]

**Primary, at the source (B0).** `programs.Attach` wraps each output stream of the child in a masking writer. The writer:
- **protected fragments**: every substring of 8 or more bytes of any current or retired program token. Tokens are at least 8 bytes, so a whole token is one.
- **Rule**: every maximal run of bytes covered by protected fragments is replaced by a single `[FILTERED]`. A whole token, a token cut short, a token's tail and overlapping repeats each become one `[FILTERED]`. Matching uses a multi-pattern automaton over all 8-byte windows of all tokens, in linear time.
- **Hold-back**: at most **7 bytes** at the end of the stream, and only while they could still start a protected fragment.
- **Close semantics (R3-M2)**: on EOF, exit, kill, timeout, cancellation or a read error, the writer first applies the rule to everything it has, then releases the remaining ≤ 7 bytes **as they are**. Seven bytes are below the 8-byte protection threshold, so a close can never release a protected fragment. There is no raw "flush" of a longer held prefix, because the writer never holds one.
- **Accepted false positive**: an innocent run of 8 or more bytes that happens to equal part of a token is masked. For real tokens with random characters this is negligible. A low-variety token (for example `aaaaaaaa1`) can mask matching output, which is stated in DOC-003.
- **Accepted residual**: a fragment shorter than 8 bytes is not protected, matching the promise "no part of 8 or more characters".
- is attached directly to the pipe, **before** `cappedBuffer`, `appendOutput`, draining `Read`, `json.Marshal` in `executeRead`, `truncateOutput` and every later step.

As a result, an encoded, capped, truncated or drained form of a token can only be made from bytes that never contained it. B0 covers every child kind that `Attach` serves:
- bash foreground, background and God Mode;
- environment_setup;
- the account check;
- the git version probe;
- the sign-in program and its read-back.

The sign-in parser reads the masked stream, so the code and link come from masked text. The read-back output is the one deliberate exception: the parser reads it only to publish it to the store, and it is never shown or logged (FR-034).

**Defence in depth (kept from revision 2), not relied on:**

| # | Boundary | Symbol |
|---|---|---|
| B1 | `ToolResult` producers of bash and environment_setup | `executeRun`, `runBackground`, `backgroundCompletionResult`, `executeRead` |
| B2 | Sync user feedback | `toolResultUserContent` |
| B3 | Async user feedback and persisted async error notice | `asyncToolResultUserContent`, `persistAsyncToolErrorNotice` |
| B4 | Async continuation input, before truncation | `asyncNotifierImpl.Notify` |
| B5 | Admitted result | `admitToolResult` |
| B6 | Gateway log | published replacer, plus retired program values |
| B7 | Audit rows | value-free by construction |

B1–B6 replace the original spelling, the Latin-1-escaped spelling (existing) and the **JSON-escaped spelling** (new). They ignore the general switch and `filter_min_length`.

**Accepted residual (unchanged):** a deliberately transformed token (base64, split by the agent, reversed) is not caught, and files an agent writes may contain the token.

---

## User Stories & Acceptance Criteria

### User Story 1 — Connect a program by pasting a token (Priority: P0) [S1]

A person opens Settings → Programs, chooses GitHub → "Paste a token", pastes it into the secure card and sees "Connected as octocat".

**Why this priority**: without a stored credential nothing else works.

**Independent Test**: with `gh` installed, paste a valid token; the row shows the real login; `GET /programs` never contains the value.

**Acceptance Scenarios**:
1. **Given** GitHub not connected, **When** a valid token is saved, **Then** "Checking the account…" and then "Connected as <login>".
2. **Given** a token shorter than 8 characters, **When** submitted, **Then** nothing is saved.
3. **Given** `gh` is not installed, **When** saved, **Then** saved and "not checked because gh was not found".
4. **Given** the check is rejected, **When** saved, **Then** "check failed" with the program's message and no account.
5. **Given** connected as A, **When** saving a new token fails in the store, **Then** A stays in use.
6. **Given** a check is running, **When** another check or token replaces it, **Then** only the newest check's result is shown.
7. **Given** a check printed the token itself, **When** it finishes, **Then** no account name is shown and the row explains why.
8. **Given** the gateway restarted during a check, **When** it is back, **Then** a fresh check runs and the row never stays "Checking…".

### User Story 2 — Switch a program on per agent (Priority: P0) [S1]

**Independent Test**: GitHub on for Dev only; Dev's commands have `GH_TOKEN`, Mia's do not.

**Acceptance Scenarios**:
1. **Given** a new agent, **When** created, **Then** every program is off.
2. **Given** GitHub connected, **When** switched on for Dev, **Then** Dev's next commands get the token, others do not.
3. **Given** Dev has a private variable `GH_TOKEN`, **When** GitHub is switched on for Dev, **Then** refused naming it.
4. **Given** GitHub on for Dev, **When** Dev asks for a private `GITHUB_TOKEN`, **Then** refused naming GitHub.
5. **Given** two entries with the same git host, **When** both are switched on for Dev, **Then** the second is refused.
6. **Given** Omnipus holds `set_agent_permissions`, **When** approved, **Then** Dev shows GitHub on; self-change is refused.
7. **Given** a CLI token or the person's own API token, **When** it changes `programs`, **Then** 403.

### User Story 3 — Agent work uses the connected login (Priority: P0) [S1]

**Independent Test**: on macOS and Linux, with another git login saved, `git push` to a repository only account A can write succeeds; the saved login is unchanged.

**Acceptance Scenarios**:
1. **Given** GitHub on, **When** `gh api user --jq .login`, **Then** the connected login.
2. **Given** another saved git login, **When** pushing over HTTPS, **Then** git authenticates as A; nothing is saved.
3. **Given** Auto with kernel sandbox, **When** `gh api user`, **Then** no network prompt.
4. **Given** Auto, **When** `gh api user && curl https://example.org`, **Then** asked about `example.org` as today.
5. **Given** Ask, **When** the person approves `gh api user`, **Then** no second prompt.
6. **Given** God Mode, **When** `gh api user`, **Then** the token is present.
7. **Given** full path or wrappers, **When** run, **Then** sites granted as for the plain name.
8. **Given** the git the command will run is older than 2.31, **When** run, **Then** no managed git login and a notice.
9. **Given** a script calls `gh`, **When** run, **Then** the script's `gh` sees the token; automatic site grants apply only to visible program words.
10. **Given** a program with no entry, **When** run, **Then** network approval as today; switched-on tokens are still present.

### User Story 4 — Custom program (Priority: P1) [S1]

**Independent Test**: create `Acme` (word `acme`, variable `ACME_TOKEN`, site `api.acme.example`), paste, switch on; `printenv ACME_TOKEN | wc -c` → length; site reachable in Auto without a prompt.

**Acceptance Scenarios**:
1. **Given** valid values, **When** saved, **Then** listed "Not connected".
2. **Given** a protected variable name, **When** saved, **Then** refused naming the rule.
3. **Given** a custom "GitLab" with word `git` and host `gitlab.com`, **When** saved, **Then** accepted.
4. **Given** an entry on for an agent, **When** deleted, **Then** refused naming the agents.
5. **Given** no account check, **When** a token is saved, **Then** "Saved, not checked", and the token is available.
6. **Given** an entry on for Dev, **When** an edit would conflict for Dev, **Then** refused naming Dev and the conflict.

### User Story 5 — Program use per channel, including handed-on and later work (Priority: P0) [S1]

**Independent Test**: GitHub on for Dev and Ops; Telegram off. A Telegram message makes Dev delegate to a helper, ask Ops, create a task, edit an existing web-created task, and start a background command whose completion continues the turn. None of the resulting commands gets `GH_TOKEN`. All of them get it after Telegram is switched on.

**Acceptance Scenarios**:
1. **Given** a connection never changed, **When** its message triggers a command, **Then** no token and a notice.
2. **Given** the web-chat setting unchanged, **When** the person asks in the web chat, **Then** programs are given.
3. **Given** the web-chat setting off, **When** a web command runs, **Then** no token.
4. **Given** a turn consumed messages from two connections, one not allowed, **When** a command runs, **Then** no token.
5. **Given** a running background command with the token, **When** the setting is turned off, **Then** it keeps its environment; the next command does not get it.
6. **Given** a Telegram-started turn (Telegram off), **When** it hands work to a helper, a peer, a new task, an **edited existing task**, a **steered existing session**, or a **background completion or goal continuation**, **Then** none of that work gets a token.
7. **Given** a Telegram-started helper, **When** the gateway restarts, **Then** it keeps Telegram's provenance.
8. **Given** an unreadable chain or a handoff with no provenance, **When** a command runs, **Then** no token and a notice.
9. **Given** the Connectors page, **When** the person opens a channel, **Then** its panel shows the workspace and agent the channel is bound to and the channel's program switch (off until switched on); an unbound channel says the switch has no effect until it is bound.

### User Story 6 — The token stays hidden (Priority: P0) [S1]

**Independent Test**: with the general filter off, a long unique token containing a quotation mark and a backslash is printed by foreground and background commands (success and error, across capture limits and read boundaries). It appears in no chat message, transcript entry, continuation input, gateway log, audit row, export bundle or account label.

**Acceptance Scenarios**:
1. **Given** the general filter off, **When** a command prints the token, **Then** `[FILTERED]`.
2. **Given** `filter_min_length` larger than the output, **When** printed, **Then** `[FILTERED]`.
3. **Given** a replaced or disconnected token, **When** an older background command prints it, **Then** `[FILTERED]`.
4. **Given** a background command prints the token and fails, **When** its completion is shown and persisted, **Then** both show `[FILTERED]`.
5. **Given** an exported agent with GitHub on, **When** imported, **Then** GitHub is listed under needed secrets and arrives switched off.
6. **Given** a token with `"` and `\`, **When** a background read returns it inside JSON, **Then** neither the raw nor the escaped spelling appears.
7. **Given** a token split across output writes, capture limits or reads, **When** output is shown, **Then** no part of 8 or more characters of the token appears.

### User Story 7 — Replace, re-check, disconnect (Priority: P1) [S1]

**Acceptance Scenarios**:
1. **Given** connected, **When** a new token is saved, **Then** it is used from the next command and its account shown after its check.
2. **Given** connected, **When** "Check again", **Then** a fresh check of the current token decides the state.
3. **Given** connected, **When** "Disconnect" is confirmed, **Then** the local copy is removed and the dialog says the token stays valid at the vendor.

### User Story 8 — An agent asks for the token on the secure card (Priority: P2) [S1]

**Acceptance Scenarios**:
1. **Given** a web chat, **When** the agent requests a program card, **Then** the card names the program and says it is shared.
2. **Given** a channel-triggered or unattended turn, **When** requested, **Then** refused as today.
3. **Given** the card is submitted from the person's browser, **When** the agent continues, **Then** it is told the token is stored and its account check is running (the card shows the result when it arrives); no switch changed.
4. **Given** a 6-character value, **When** submitted on the card, **Then** refused with the program token rules.
5. **Given** the person's own API token (not the browser), **When** used to submit the card, **Then** 403 and the card stays pending.

### User Story 9 — Sign in with the program's own browser login (Priority: P1) [S2]

**Acceptance Scenarios**:
1. **Given** a verified recipe on this platform, **When** "Sign in" is clicked, **Then** a link and a code within 15 s.
2. **Given** approval on the vendor page, **When** Omnipus finishes, **Then** "Connected as <login>" and the attempt folder is gone.
3. **Given** no recipe for this platform, **When** opened, **Then** no "Sign in" button and an explanation.
4. **Given** the person's own login exists, **When** they sign in through Omnipus, **Then** their own login is unchanged.
5. **Given** an agent's program card, **When** the person uses the card's "Sign in" and approves, **Then** the card shows "Stored" and the agent continues once. If the gateway crashes in between, the credential and card stay stored, and the agent continues at most once; a continuation interrupted by the crash is shown as stopped, as for any card today.

### User Story 10 — Sign-in ends cleanly in every case (Priority: P1) [S2]

**Acceptance Scenarios**:
1. **Given** an active attempt in any state, **When** cancelled, **Then** `cancelled`, the program stopped, the folder deleted.
2. **Given** an attempt waiting, **When** it expires, **Then** `expired`.
3. **Given** the vendor denies, **When** the program exits, **Then** `denied`.
4. **Given** an attempt, **When** a second attempt, paste or disconnect happens, **Then** the first is `cancelled` and cannot publish.
5. **Given** verifying, **When** read-back hangs or fails or the store fails, **Then** `failed` within 60 s, previous login kept.
6. **Given** the gateway dies at any point of the launch, **When** it starts again, **Then** the old program is stopped and the folder removed before requests are served; if that fails, the Programs row warns.
7. **Given** an attempt running, **When** an agent tries to read its folder, **Then** refused.

---

## UI Screens and States

| Screen / Component | Loading | Empty | Error | Partial | Success |
|---|---|---|---|---|---|
| Settings → **Programs** tab [S1] | Skeleton rows | Presets listed; "No custom programs" + "Add a custom program" | "Programs could not be loaded." + Retry | Failed refresh keeps rows + "Could not refresh — this list may be out of date." + Retry | Rows: name, words, state badge, account, actions |
| Row state badge [S1] | "Checking the account…" (always ends: FR-047) | "Not connected" | "Check failed" + message | "Saved, not checked" + reason | "Connected as <account>" |
| Row sign-in warning [S2] | — | — | Warning callout with the cleanup message while `sign_in_warning` is present | — | — |
| Paste-a-token dialog [S1] | Busy, field locked | — | Inline rule error; save failure keeps old token; lost response: "The result of saving is unknown. The list shows the current state." | — | Closes; row "Checking the account…"; focus returns |
| Custom program form [S1] | Busy | — | Field errors; 409 names the conflict | — | Row appears |
| Disconnect confirm [S1] | Busy | — | "Could not disconnect. Try again." | — | "Not connected" |
| Web-chat setting [S1] | Busy | — | "Not confirmed" + Retry | — | Saved |
| Agent profile → Programs [S1] | Skeleton | "No programs yet. Connect one in Settings → Programs." | ErrorState + Retry; conflict text | "Not connected yet" per row | Saved |
| Connectors page → channel panel → **Programs** section [S1] | Busy | — | "Not confirmed" + Retry | Unbound channel: "This channel is not bound to a workspace and agent, so no agent receives its messages. This switch takes effect once it is bound." | Switch "Let <agent> use connected programs for messages from this channel" with the line "Bound to <workspace> · <agent>" (from `GET /channels/{id}/routing`); hint "Programs themselves are connected in Settings → Programs."; saved |
| Chat program card [S1/S2] | Existing states | — | Existing states; 403 shows "Open Omnipus in your browser to enter this token." | "Stored — checking the account…" while the entry's generation equals the card's `program_generation` and the state is `checking`; S2: sign-in in progress | "Stored" + the check result for **its own** generation; when the entry's generation has moved on: "Stored — replaced by a newer token" (never the newer token's account) |
| Sign-in dialog [S2] | "Starting sign-in…" | — | expired / denied / failed / unsupported, each one message + one action; `cleanup: failed` adds the cleanup message | "Checking the account…" (verifying, with Cancel) | "Connected as <account>" |

## User Journey

1. **[S1]** Settings → Programs → GitHub → "Paste a token" → Save → "Checking the account…" → "Connected as …".
2. **[S1]** Agent profile → Programs → switch GitHub on, or ask Omnipus (approved `set_agent_permissions`).
3. **[S1]** Chat with the agent → `gh`, `git push` work.
4. **[S1]** Optional: channel panel → Programs setting.
5. **[S1]** Optional: add a custom program.
6. **[S2]** Programs → GitHub → "Sign in" → copy code, open link → approve → "Connected as …". From chat: the card's "Sign in" → same dialog → card "Stored" → agent continues.
7. Exit: Replace / Check again / Disconnect.

## Accessibility and Keyboard

- Every control is a catalogued component with a visible label: "Token for <program>"; "<program> for <agent>"; "Agents may use connected programs for work that comes from this connection".
- Tab order follows reading order. Dialogs trap focus and return it to the opener. Escape closes (S2: offers to cancel the attempt, in every active state).
- State changes are announced through a polite live region. The code has a `CopyButton` "Copy code". The link is announced as opening a new tab.
- Focus-visible from the catalogued components; design-system touch targets; the table becomes a list on narrow widths.

## Design-System Components

From `design-system/catalog.json`: `dialog`, `confirm-dialog`, `button`, `switch`, `field`, `input`, `label`, `FormError`, `badge`, `callout`, `copy-button`, `skeleton`, `error-state` / `query-error-state`, `list-group`, `table`, `tooltip`, `spinner`. No new component. The secure-card body is extracted from `SecretRequestCard.tsx` into a shared part; it follows the four-part publication contract if it moves into `src/components/ui/`.

## Security and User Promises

- **Focus areas**: `pkg/credentials`, `pkg/sandbox`, shell rules, child output capture, turn provenance, `pkg/audit`, gateway auth.
- **Invariants I-1, I-2, I-3** (top of this spec) with their guard tests.
- **Promises kept**:
  - deny rules first;
  - ADR-092 approvals unchanged except the per-run site grant;
  - no token written by Omnipus into files, git configuration or command lines, **except** the encrypted credential store and, in S2, the vendor's own files in the protected temporary sign-in folder (deleted on every end state, FR-035). Output an agent transforms on purpose, and files an authorized agent writes itself, remain the accepted residuals below;
  - other secrets keep the documented switchable filter.
- **Accepted residual risks** (ADR D7):
  - an allowed agent can act as the account;
  - transformed values are not caught;
  - files an agent writes may contain the token;
  - God Mode passes the wider gateway environment;
  - provenance follows handoffs, not conversation history;
  - on Linux, the parent-death signal is tied to the launching OS thread (the launcher locks its thread; the start-up marker scan is the backstop);
  - Windows has no S2 sign-in.
- **S2 runs a program outside the sandbox with gateway authority**:
  - path validated with `CheckProgramPath`;
  - environment from scratch;
  - config folder inside the always-denied `system` directory;
  - output bounded and masked;
  - crash-safe launch protocol;
  - person-only custom recipes (Q3).

---

## Behavioral Contract

Primary flows:
- **Saving a token [S1]:** when a person saves a valid token from the browser, the system publishes it as a new generation, reports `checking`, runs exactly one current check, and shows "Connected as <account>" only if that check succeeded, its label passes FR-046, and the generation, `check_revision` and check id are still current.
- **Environment [S1]:** when a switched-on agent runs a command in an eligible turn, the system adds every token variable of every switched-on entry that has a saved credential, through `programs.Attach`.
- **Git login [S1]:** when such an entry has a git host and the git the command will run supports it, the system makes git use the stored token for that host.
- **Site grants [S1]:** when the mode is Auto and a visible program word belongs to switched-on entries, the system grants the union of their sites for that run without a prompt. In Ask, it grants them after the person permits the command.
- **Handoffs [S1]:** when work is handed on, edited, steered or continued, the system carries and joins provenance (I-2).
- **Sign-in [S2]:** when a person signs in on a verified recipe, the system shows a link and code. After approval it publishes the token, and for a chat card the card receipt, in one write.

Error flows:
- **Failed save [S1]:** the previous credential stays in use.
- **Check fails or cannot run [S1]:** the credential stays saved and available; the row shows the reason.
- **Conflicts [S1]:** a change that would make two sources set one variable or git host for an agent is refused. If such a conflict exists at run time, neither value or helper is set and a notice is added.
- **No store or no confirmed origin [S1]:** when the store is unavailable or provenance is `unresolved`, the command runs without program tokens and with a notice.
- **Attempt ends without connecting [S2]:** the program is stopped, the folder deleted, and the previous login kept. If cleanup is not confirmed, the attempt and the row say so.

Boundary conditions:
- Several connections in provenance → all must allow. [S1]
- Settings affect new commands only. [S1]
- Only the current check id and generation publish. [S1/S2]

## Edge Cases

- Token with surrounding whitespace → trimmed; inner line break refused. [S1]
- Token containing `"` or `\` → masked at the source, so JSON encoding never sees it (S-102). [S1]
- Output that contains the first part of a token, whether the command keeps running, exits, is killed or times out → every run of 8 or more bytes of it is `[FILTERED]`; at most 7 trailing bytes are released raw (S-104, S-130). [S1]
- A generic credential write or delete (Settings credentials API, CLI `credentials`, `remove_secret`, provider or MCP clean-up) aimed at a program's stored reference → refused with `ErrProgramOwned`; the boot orphan sweep skips it (S-127 to S-129). [S1]
- Program missing on the gateway but present in the workspace → check says not found; the token is still available. [S1]
- `./bin/gh` → matched by its last path part. [S1]
- God Mode with inherited `GIT_CONFIG_*` → managed entries replace them for that command, with a notice. [S1]
- Two entries share `git` → union of sites; one helper per host. [S1]
- Two browser tabs save at once → lock orders them; each response reports its own generation. [S1]
- Entry deleted while a background command runs → it keeps its environment. [S1]
- A peer request answered after the sender's channel was switched on → current setting of the carried connections. [S1]
- A person edits, in the UI, a task an agent changed from Telegram → the task keeps Telegram in its provenance; deleting and re-creating it clears it. [S1]
- Vendor output with no recognised code or link → `unsupported`; nothing within 15 s → `failed`. [S2]

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

- Must not show, return, log or audit a token value, including as an account label.
- Must not add program variables to a child except through `programs.Attach` (I-1).
- Must not create a resolved provenance outside the three named producers, nor shrink a record's provenance (I-2).
- Must not save a program credential except through `programs.Publish` with a person-session proof or gated attempt authorisation (I-3).
- Must not change or remove a program-owned store entry through any store method other than the program-scoped ones (I-3).
- Must not run a second account check when publishing an already-verified sign-in candidate.
- Must not claim that a continuation is guaranteed to run exactly once after a crash; it is admitted at most once.
- Must not write a token into a git configuration file, remote URL, command line or workspace file.
- Must not choose silently between two values for one variable, or two helpers for one git host.
- Must not switch a program on as a side effect of saving, submitting, signing in or importing.
- Must not run a program with gateway authority from an agent-writable location; the git version probe runs inside the agent's sandbox.
- Must not relabel a program's own error output.
- Must not add a program allow-list.
- Must not change the person's own login, keychain or config folder.
- Must not show raw login output.
- Must not offer "use my existing login" (S3), and must not block it.
- Must not change card-submit policy for card kinds other than `program`.
- New names avoid the spellings banned by `scripts/check-no-cli-model-providers.sh`.
- No hosted-specific code.

### Machine-Verifiable Constraints

**Error Codes / Messages** (omnipus-voice):
- < 8 bytes → 400 `The token is too short. A token has at least 8 characters.` [S1]
- inner line break → 400 `The token contains a line break. Paste the token on one line.` [S1]
- NUL → 400 `The token contains a character that cannot be stored.`; empty → 400 `Enter a token.` [S1]
- > 16,384 bytes → 400 `The token is too long. A token has at most 16,384 bytes.`; body > 65,536 → 413 [S1]
- store failure → 500 `The token could not be saved. The previous token is still in use. Try again.` [S1]
- stale generation → 409 `The token changed since this page was loaded. Check the current state before saving again.` [S1]
- protected variable → `CheckPrivateVariableName`'s text; reserved git name → `GIT_CONFIG_COUNT is a protected name: Omnipus sets it for connected git logins` [S1]
- variable conflict → 409 `GitHub cannot be switched on for Dev: Dev already has a private variable GH_TOKEN. Remove that variable, or leave GitHub off.` [S1]
- git host conflict → 409 `GitLab Mirror cannot be switched on for Dev: GitHub already supplies the git login for github.com to Dev. Switch one of them off.` [S1]
- private variable conflict → `GH_TOKEN is used by the program GitHub, which is switched on for this agent. Switch GitHub off first, or use another name.` [S1]
- not a person session → 403 `this action requires a signed-in person` (existing); no identity → 401 (existing); missing CSRF → existing CSRF refusal [S1]
- program card from a non-browser caller (UI text) → `Open Omnipus in your browser to enter this token.` [S1]
- program-owned reference through a generic writer → 409 / tool error `<reference> belongs to the program <name>. Change it in Settings → Programs.` [S1]
- `PUT /agents/{id}` with `programs` from a non-browser caller → 403 `this action requires a signed-in person`; the same request without `programs` is unaffected [S1]
- withheld by channel → `Connected programs were not used for this command: programs are off for work that came from <connection>. A person can switch them on for this channel on the Connectors page.` [S1]
- withheld by web chat → `Connected programs were not used for this command: programs are off for the web chat. A person can switch them on in Settings → Programs.` [S1]
- unresolved → `Connected programs were not used for this command: where this work came from could not be confirmed.` [S1]
- store unavailable → `Connected programs were not loaded for this command: the credential store is unavailable.` [S1]
- run-time conflicts → `GH_TOKEN was not set for this command: two sources set it. A person can fix this in Settings → Programs.` / `The connected git login for github.com was not used: two programs supply it. A person can fix this in Settings → Programs.` [S1]
- old git → `Connected git logins were not used: git <version> is too old. Update git to 2.31 or later.` [S1]
- check: missing → `Saved, not checked: gh was not found on this computer.`; rejected → `The account check failed. GitHub said: "<first error line, masked, ≤ 300 chars>".`; none → `Saved, not checked: this program has no account check.`; timeout → `The account check did not finish in 20 s.`; label carries token → `The account check printed the token instead of an account name. Fix the account check for <program>.` [S1]
- sign-in: unsupported → `Sign-in is not available for <program> on this computer. Paste a token instead.`; start timeout → `The sign-in did not start in 15 s. Try again, or paste a token instead.`; unrecognised → `Sign-in could not be completed for <program>. Paste a token instead.`; denied → `Access was denied on <vendor site>. Start the sign-in again if this was a mistake.`; verifying timeout → `The sign-in for <program> did not finish in 60 s after you approved it. Try again, or paste a token instead.`; cleanup → `The sign-in for <program> could not be fully cleaned up. Restart Omnipus; if this message stays, report it to the Omnipus.ai team.` [S2]

**Performance Bounds**:
- **Account check:** timeout per recipe (default 20 s, max 30 s), ending within timeout + 5 s.
- **Sign-in phases:** start 15 s; version probe 10 s; read-back 15 s; verifying phase 60 s; expiry ≤ 900 s.
- **Store:** one store read per command for program credentials. Transaction admission (both lock layers together): at most 5 s for ordinary writes, then `ErrStoreBusy` (REST 503 `The credential store is busy. Try again.`, CLI non-zero exit) with no change; for a verified commit, at most until its verifying deadline. No lock in the order config → registry → entry is held longer than that admission bound plus one synchronous write.
- **Git version probe:** at most once per (path, size, mtime) per gateway run.
- **Masking hold-back:** ≤ 7 bytes per stream; matching is linear in the output length.

**Scope Boundaries**:
- **Counts:** ≤ 32 entries; ≤ 8 words, ≤ 4 variables and ≤ 32 sites per entry; ≤ 32 programs per agent.
- **Sites:** host names or `*.` wildcards only.
- **Program words:** must not be shells or wrappers (`sh`, `bash`, `zsh`, `dash`, `fish`, `env`, `sudo`, `doas`, `command`, `exec`, `nohup`, `timeout`, `xargs`, `nice`, `time`, `setsid`, `stdbuf`).
- **Regular expressions:** RE2 only.

### Crash-safe sign-in launch protocol [S2] (R2-M1)

1. **Intent.** Before starting anything, write `intent.json` (attempt id, random 128-bit marker, executable path, folder, time) into `$OMNIPUS_HOME/system/program-sign-in/<attempt-id>/` and fsync it and its directory. If this fails → `failed`; nothing is started.
2. **Start.** Start the program in a new process group with `OMNIPUS_SIGN_IN_MARKER=<marker>` in its environment. On Linux, use `SysProcAttr.Pdeathsig = SIGKILL` from a thread locked with `runtime.LockOSThread` for the lifetime of the attempt.
3. **Identity receipt.** Write `owner.json` (PID, process start time from the OS, marker) and fsync it. If writing fails → kill the process group at once, then follow the end-state cleanup.
4. **Start-up sweep (before the first request is served).** For every attempt folder:
   - if `owner.json` exists and the PID's start time matches, kill that process group;
   - in all cases, scan the processes of the gateway's own user for the marker in their environment (Linux `/proc/<pid>/environ`; macOS `sysctl` `KERN_PROCARGS2`) and kill those that match;
   - then delete the folder.

   If the scan cannot run, or a match cannot be killed, or the folder cannot be deleted, the folder is kept, `sign_in_warning` is set on the entry, an audit row is written, and the sweep retries at the next start.
5. **Windows**: sign-in is unsupported (FR-039), so no protocol is needed there.

Fault barriers in tests (S-117): before step 2, between steps 2 and 3, and after step 3.

### Sign-in attempt state transitions [S2]

| From | Event | To | Note |
|---|---|---|---|
| — | start accepted | `starting` | an earlier active attempt of this entry → `cancelled` |
| `starting` | valid link and code | `waiting_for_person` | |
| `starting` | 15 s without link and code | `failed` | start timeout |
| `starting`, `waiting_for_person` | output > 65,536 bytes, invalid link, or code-pattern mismatch | `unsupported` | |
| `starting`, `waiting_for_person` | non-zero exit with a `denied_patterns` match | `denied` | |
| `starting`, `waiting_for_person` | non-zero exit otherwise | `failed` | |
| `waiting_for_person` | `expires_at` reached | `expired` | expiry applies only while waiting for the person |
| `waiting_for_person` | program exits 0 | `verifying` | 60 s verifying deadline starts |
| `verifying` | read-back (≤ 15 s) → candidate masking registered → **one** check of the candidate → verified `programs.Publish` (preconditions hold; one write of credential, label, state and card receipt) | `connected` | commit point = that write; no second check |
| `verifying` | read-back fails or exceeds 15 s, check fails, label rule fails, store admission not obtained by the deadline, or the 60 s verifying deadline passes **before the claim** | `failed` | previous credential kept (nothing committed) |
| `verifying` | a precondition fails at commit (superseded) | `cancelled` | nothing committed |
| `verifying` | store transaction admitted and preconditions hold → claim | `committing` (internal; shown as `verifying`) | the claim is the single winner point (R5-M3) |
| `committing` | synchronous write read back with the new generation | `connected` | the deadline, cancel and supersession can no longer win; a cancel gets 409 |
| `committing` | write failed (new generation not in the file) | `failed` | previous credential kept |
| **any active state** (`starting`, `waiting_for_person`, `verifying`; not `committing`) | person cancels (attempt or bound card), newer attempt, paste or disconnect of this entry, gateway shutdown | `cancelled` | the **single** supersession outcome; publication is impossible after it |
| every end state | — | — | `cleanup` = `pending` until the process group is gone and the folder removed (`done`), or `failed` |

End states: `connected`, `expired`, `cancelled`, `denied`, `failed`, `unsupported`. That is six, and each has its own scenario.

## Integration Boundaries

### Encrypted credential store [S1]
- **Data**: token and, when card-bound, its `CardReceipt`, in one write; value-free metadata.
- **Concurrency (R4-M1, FR-049)**: every mutation is one `s.transact` read-modify-write over the latest file, serialized across `Store` objects (process-wide per-path mutex) and across processes (sidecar lock, real on Windows too).
- **On failure**: no tokens plus a notice; a failed save keeps the previous version.
- **Development**: real store in a temp folder.

### Vendor services [S1/S2]
- **Contract**: recipe data.
- **On failure**: the program's own masked message.
- **Development**: fake programs; real CLIs only in hands-on rows.

### Egress proxy and kernel network posture [S1]
- **Contract**: `GrantRunHosts` / `RevokeRunToken`.
- **On failure**: the program's own connection error (#1044 rider).

### Sessions, message bus, request ledger, work records [S1]
- **Data**: `Provenance` on every turn-feeding message and durable record.
- **On failure**: zero or unreadable → `unresolved` → withheld.
- **Development**: existing tests extended; restart cases with persisted records.

### Gateway authentication [S1]
- **Contract**: person-session gate and CSRF; the program-card branch decided from the stored kind.
- **Development**: real middleware (D-8).

---

## Ambiguity Warnings

| # | What's Ambiguous | Likely Agent Assumption | Question to Resolve |
|---|---|---|---|
| 1 | Independent work | Follows web chat (A-1) | Founder Q1 |
| 2 | Preset sites and checks (G3) | Treat as fact | G3 spike before S1 GREEN |
| 3 | `gh` sign-in per platform (G2) | Ship it | G2 spike before S2 GREEN |
| 4 | Windows `!` helper with `$VAR` | Works | H-07; on failure Windows gets tokens without the managed helper, with a notice |
| 5 | #1044 status | Fixed | H-00 first |
| 6 | Scripts calling another git by absolute path | Covered | Not covered; DOC-002 |
| 7 | macOS marker scan permission for own processes via `KERN_PROCARGS2` | Works | H-13 checks it; if it fails, the sweep reports `sign_in_warning` rather than claiming cleanup |

---

## BDD Scenarios

### Feature: Connected programs

#### Background (applies to every scenario unless the scenario says otherwise)
- **Given** a fresh install with an unlocked credential store in a temp folder, agents "Dev", "Mia", "Ops" in workspace "W", presets GitHub and Fly listed
- **And** a fake `gh` on the gateway PATH and in Dev's sandbox PATH that prints `octocat` for `api user --jq .login` when `GH_TOKEN=tok_valid_12345`, and exits 1 with `HTTP 401: Bad credentials` otherwise
- **And** "connected" below means `tok_valid_12345` was saved through `PUT /programs/github/credential` and its check returned `octocat`
- **And** REST calls are made from a signed-in person's browser session with a CSRF token, unless the scenario names another caller
- **And** agent commands are issued from Dev's web-chat turn, in Auto with an enforcing kernel sandbox and a configured egress proxy, unless the scenario says otherwise

#### US-1 — Connect by pasting [S1]

##### Scenario: S-01 Valid token connects
**Traces to**: US-1 AS-1 · **Category**: Happy Path
- **Given** GitHub is `not_connected`
- **When** the person saves `tok_valid_12345`
- **Then** the response has `state: checking`, `generation: 1`, and a later `GET /programs` has `state: connected`, `account_label: octocat`
- **And** no response, log line or audit row contains `tok_valid_12345`

##### Scenario: S-02 Too short
**Traces to**: US-1 AS-2 · **Category**: Error Path
- **Given** GitHub is `not_connected`
- **When** the person submits `abc1234`
- **Then** 400 `The token is too short. A token has at least 8 characters.` and GitHub stays `not_connected`

##### Scenario: S-03 Program missing
**Traces to**: US-1 AS-3 · **Category**: Alternate Path
- **Given** no `gh` on the gateway PATH
- **When** a token is saved
- **Then** the state ends `saved_not_checked` with `Saved, not checked: gh was not found on this computer.`

##### Scenario: S-04 Check rejected
**Traces to**: US-1 AS-4 · **Category**: Error Path
- **Given** GitHub is `not_connected`
- **When** the person saves `tok_wrong_999999`
- **Then** the state ends `check_failed` with `The account check failed. GitHub said: "HTTP 401: Bad credentials".` and no account label

##### Scenario: S-05 Store failure keeps the previous token
**Traces to**: US-1 AS-5 · **Category**: Error Path
- **Given** connected (generation 1) and the next store write is set to fail
- **When** `tok_other_67890` is saved
- **Then** 500 with the keep-previous message, generation stays 1, the row still shows `octocat`, and Dev's next `printenv GH_TOKEN | wc -c` (GitHub on for Dev) outputs `16`, the length of `tok_valid_12345` plus newline

##### Scenario: S-06 Check timeout
**Traces to**: US-1 AS-4 · **Category**: Edge Case
- **Given** the fake `gh` sleeps 30 s
- **When** a token is saved
- **Then** within 25 s the state is `check_failed` with `The account check did not finish in 20 s.` and the process group is gone

##### Scenario: S-07 Check isolation
**Traces to**: US-1 AS-1 · **Category**: Edge Case
- **Given** the gateway environment has `GH_TOKEN=gateway_ambient_tok`, `HOME=/home/person`, and the fake `gh` records its environment
- **When** `tok_valid_12345` is saved
- **Then** the recorded environment has `GH_TOKEN=tok_valid_12345`, a temporary `HOME` that no longer exists afterwards, and no `gateway_ambient_tok`

##### Scenario: S-08 Program in an agent-writable folder is not run
**Traces to**: US-1 AS-3 · **Category**: Error Path
- **Given** the only `gh` on the gateway PATH lies in a workspace folder
- **When** a token is saved
- **Then** the state ends `saved_not_checked` with a message about where the program is installed, and the program was not started

##### Scenario Outline: S-09 Token value rules
**Traces to**: US-1 AS-2 · **Category**: Edge Case · **Examples**: Dataset D-1
- **Given** GitHub is `not_connected`
- **When** the person submits `<input>`
- **Then** `<expected>`

##### Scenario: S-90 A stale-generation check never publishes
**Traces to**: US-1 AS-6 · **Category**: Edge Case
- **Given** the check of token A is held at a barrier after the program printed `account-a`
- **When** token B is saved (generation 2), its check returns `account-b`, then the barrier releases
- **Then** the row shows `account-b`, `account-a` is never stored, and an audit row records A's check as `superseded`

##### Scenario: S-114 Two checks of one generation finish in reverse order
**Traces to**: US-1 AS-6 · **Category**: Edge Case
- **Given** connected (generation 1); check A (`POST …/check`) is held at a barrier after printing `octocat`
- **When** check B (`POST …/check`) starts, the fake `gh` now exits 1 with `HTTP 401: Bad credentials`, B finishes, then A's barrier releases
- **Then** the state is `check_failed` from B, A's process group was stopped when B started, and A's result is audited `superseded` and never published

##### Scenario: S-115 Restart while checking
**Traces to**: US-1 AS-8 · **Category**: Edge Case
- **Given** a token was saved and its check is held at a barrier, so the persisted state is `checking`
- **When** the gateway is killed and started again
- **Then** a fresh check for the current generation runs at start, and the state ends `connected` within the check timeout + 5 s

##### Scenario: S-116 Check-relevant edit while checking starts a new check
**Traces to**: US-1 AS-6, US-4 AS-6 · **Category**: Edge Case
- **Given** custom Acme connected and its check held at a barrier
- **When** the person edits Acme's account check args (`check_revision` +1), then the barrier releases
- **Then** the old result is discarded, a new check with the new args runs automatically, and the row ends in that check's state

##### Scenario: S-131 Unrelated edit while checking does not strand the row
**Traces to**: US-1 AS-6, AS-8 · **Category**: Edge Case
- **Given** custom Acme with a newly saved credential; its check held at a barrier after printing `acme-user`
- **When** the person edits only Acme's display name, then only its network sites, then releases the barrier
- **Then** `check_revision` is unchanged by both edits, the held result publishes, and the row ends `connected` as `acme-user` with no restart and no second check
- **And when** the person edits `token_variables` instead, **then** the held result is discarded, one new check runs, and the row ends in that check's terminal state

##### Scenario: S-105 Account check echoes the token
**Traces to**: US-1 AS-7 · **Category**: Error Path
- **Given** custom Acme with word `printenv`, variable `ACME_TOKEN` and account check `printenv` `["ACME_TOKEN"]`
- **When** `acme_tok_123456` is saved
- **Then** the state is `check_failed` with the label-carries-token message, `account_label` is empty, and neither the value nor `[FILTERED]` is stored as a label

#### US-2 — Per-agent switch [S1]

##### Scenario: S-10 New agents start off
**Traces to**: US-2 AS-1 · **Category**: Happy Path
- **Given** GitHub connected
- **When** agent "New" is created
- **Then** its `programs` is `[]`

##### Scenario: S-11 Only the switched-on agent gets the token
**Traces to**: US-2 AS-2 · **Category**: Happy Path
- **Given** GitHub connected, on for Dev only
- **When** Dev and Mia each run `printenv GH_TOKEN | wc -c`
- **Then** Dev's output is `16`, Mia's is `0`

##### Scenario: S-12 Switch-on refused by a private variable
**Traces to**: US-2 AS-3 · **Category**: Error Path
- **Given** Dev has a selected private variable `GH_TOKEN` in W
- **When** GitHub is switched on for Dev
- **Then** 409 with the variable-conflict message, and `programs` stays `[]`

##### Scenario: S-13 Private variable refused while on
**Traces to**: US-2 AS-4 · **Category**: Error Path
- **Given** GitHub on for Dev
- **When** Dev calls `request_secret` with `variable: "GITHUB_TOKEN"`
- **Then** refused with the private-variable conflict message

##### Scenario: S-85 Git host conflict refused at switch-on and on edit
**Traces to**: US-2 AS-5, US-4 AS-6 · **Category**: Error Path
- **Given** custom "GitLab Mirror" with git host `GitHub.com.` (stored `github.com`), and GitHub on for Dev
- **When** "GitLab Mirror" is switched on for Dev
- **Then** 409 with the git-host conflict message
- **And given** "GitLab Mirror" with host `gitlab.example` is on for Dev, **when** its host is edited to `github.com`, **then** 409 naming Dev and `github.com`, and the entry is unchanged

##### Scenario: S-14 set_agent_permissions after approval
**Traces to**: US-2 AS-6 · **Category**: Alternate Path
- **Given** GitHub connected, Dev's `programs` is `[]`
- **When** Omnipus calls `set_agent_permissions` with `id: "dev"`, the current revision and `programs: ["github"]`, and the person approves
- **Then** Dev's `programs` is `["github"]`

##### Scenario: S-15 Self-change refused
**Traces to**: US-2 AS-6 · **Category**: Error Path
- **Given** Dev holds `set_agent_permissions` in a test policy
- **When** Dev calls it for `id: "dev"` with `programs: ["github"]`
- **Then** the existing self-change refusal; unchanged

##### Scenario: S-16 update_agent refuses programs
**Traces to**: US-2 AS-6 · **Category**: Error Path
- **Given** Omnipus may call `update_agent`
- **When** it calls `update_agent` for Dev with `programs: ["github"]`
- **Then** refused naming `set_agent_permissions`; unchanged

##### Scenario: S-17 Switch on before connecting
**Traces to**: US-2 AS-2 · **Category**: Alternate Path
- **Given** Fly not connected
- **When** Fly is switched on for Dev and Dev runs `printenv FLY_API_TOKEN | wc -c`
- **Then** the switch is saved, the row says "Not connected yet", and the output is `0`

##### Scenario Outline: S-81 Callers of person-only endpoints
**Traces to**: US-2 AS-7, US-8 AS-5 · **Category**: Error Path · **Examples**: Dataset D-8
- **Given** a fake `gh` that records whether it was started
- **When** `<caller>` sends `<request>`
- **Then** the status and error source are exactly the row's `<expected>`; the request body is read or not read exactly as the row's `<body read>` column says (observed with a counting body reader); for every refused row no state changes and the fake program was not started; the positive-control rows are processed

#### US-3 — Work uses the login [S1]

##### Scenario: S-18 gh uses the token
**Traces to**: US-3 AS-1 · **Category**: Happy Path
- **Given** GitHub connected and on for Dev
- **When** Dev runs `gh api user --jq .login`
- **Then** the output is `octocat`

##### Scenario: S-19 git uses the stored token and ignores an existing helper
**Traces to**: US-3 AS-2 · **Category**: Happy Path
- **Given** GitHub connected and on for Dev; the global git config has a helper for `https://github.com` answering `old-account` / `old_pw_123456` and logging only operation names
- **When** Dev runs `git credential fill` for `protocol=https host=github.com`
- **Then** git receives username `x-access-token` and the token (shown as `[FILTERED]`), and the old helper logged no `get`, `store` or `erase`

##### Scenario: S-20 No token in config or arguments
**Traces to**: US-3 AS-2 · **Category**: Edge Case
- **Given** GitHub connected and on for Dev; Dev's workspace has an operator-approved write mount holding a git project whose `origin` is a local HTTPS git receiver that accepts only `x-access-token:tok_valid_12345`; and a second git project folder on the same machine with **no operator write grant** (not mounted)
- **When** Dev runs `git push` through `bash` with the mounted project as its working directory
- **Then** the push succeeds, the receiver recorded `x-access-token`, and no git config file, remote URL or observed argument list contains the token
- **And** (control) for the project with no operator write grant, reading its files works, but `bash` with it as the working directory is refused, so no push reaches the receiver; and a `git push` from Dev's workspace folder is still refused by the evidence-repository guard

##### Scenario: S-21 Auto grants profile sites
**Traces to**: US-3 AS-3 · **Category**: Happy Path
- **Given** GitHub connected and on for Dev
- **When** Dev runs `gh api user`
- **Then** no approval; the proxy allows `api.github.com` for that run's token; the token is revoked after the run

##### Scenario: S-22 Other hosts still asked
**Traces to**: US-3 AS-4 · **Category**: Alternate Path
- **Given** GitHub connected and on for Dev
- **When** Dev runs `gh api user && curl https://example.org`
- **Then** exactly one approval, naming `example.org` only

##### Scenario: S-23 Deny wins
**Traces to**: US-3 AS-3 · **Category**: Error Path
- **Given** GitHub on for Dev and rule `{action: deny, binary: gh}`
- **When** Dev runs `gh api user`
- **Then** refused with the existing deny message; not run

##### Scenario: S-24 Ask grants after approval
**Traces to**: US-3 AS-5 · **Category**: Happy Path
- **Given** Ask mode, GitHub on for Dev
- **When** the person approves `gh api user`
- **Then** no second prompt; GitHub sites allowed for that run only

##### Scenario: S-25 God Mode
**Traces to**: US-3 AS-6 · **Category**: Alternate Path
- **Given** God Mode, GitHub on for Dev
- **When** Dev runs `printenv GH_TOKEN | wc -c`
- **Then** `16`; no egress token is created

##### Scenario Outline: S-26 Program-word spellings
**Traces to**: US-3 AS-7 · **Category**: Edge Case · **Examples**: Dataset D-3
- **Given** GitHub on for Dev
- **When** Dev runs `<command>`
- **Then** `<expected>`

##### Scenario: S-27 Old git in the child's PATH
**Traces to**: US-3 AS-8 · **Category**: Error Path
- **Given** GitHub on for Dev; the gateway's `git` reports 2.43; a workspace runtime `git` (first on the child's PATH) reports `git version 2.30.2`
- **When** Dev runs `git ls-remote https://github.com/o/r`
- **Then** no `GIT_CONFIG_COUNT` in the environment, the result contains the old-git message with `2.30.2`, and the probe ran inside Dev's sandbox (recorded by the fake git)

##### Scenario: S-87 Explicit git path is the one probed
**Traces to**: US-3 AS-8 · **Category**: Edge Case
- **Given** GitHub on for Dev; `/opt/old/git` reports 2.30.2 and the PATH `git` reports 2.43
- **When** Dev runs `/opt/old/git push`
- **Then** no managed git entries are set and the notice names 2.30.2

##### Scenario: S-88 New git gets the helper
**Traces to**: US-3 AS-2 · **Category**: Happy Path
- **Given** GitHub on for Dev; the child's PATH `git` reports 2.43
- **When** Dev runs `git push`
- **Then** `GIT_CONFIG_COUNT=2` and the two managed entries are present

##### Scenario: S-28 Unmatched program: no grant, token still available
**Traces to**: US-3 AS-10 · **Category**: Alternate Path
- **Given** GitHub on for Dev; no entry has the word `aws`
- **When** Dev runs `aws s3 ls`
- **Then** the network approval is requested exactly as before and names no GitHub site
- **And** `GH_TOKEN` is in that command's environment

##### Scenario: S-29 environment_setup children
**Traces to**: US-3 AS-1 · **Category**: Alternate Path
- **Given** GitHub on for Dev
- **When** Dev runs `environment_setup` with a script that runs `printenv GH_TOKEN | wc -c`
- **Then** the script output is `16`

##### Scenario: S-30 Run-time variable conflict fails closed
**Traces to**: US-3 AS-1 · **Category**: Error Path
- **Given** a hand-edited config: GitHub on for Dev and a selected private `GH_TOKEN`
- **When** Dev runs `printenv GH_TOKEN | wc -c`
- **Then** `0` with the run-time conflict notice

##### Scenario: S-86 Run-time git host conflict: no managed helper
**Traces to**: US-3 AS-2 · **Category**: Error Path
- **Given** a hand-edited config where GitHub and "GitLab Mirror" (host `github.com`) are both on for Dev, in both entry orders
- **When** Dev runs `git credential fill` for github.com
- **Then** no managed helper answers for `github.com`, the notice is added, and the result is the same in both orders

##### Scenario: S-31 Store unavailable
**Traces to**: US-3 AS-1 · **Category**: Error Path
- **Given** GitHub on for Dev and the credential store set to fail on read
- **When** Dev runs `echo ok`
- **Then** the output is `ok` plus the store-unavailable notice, and no program variable is set

##### Scenario: S-32 Program error passed through
**Traces to**: US-3 AS-1 · **Category**: Edge Case
- **Given** GitHub on for Dev; the fake `gh` prints `HTTP 403: Resource not accessible by integration` and exits 1
- **When** Dev runs `gh api repos/o/r`
- **Then** exactly that line and exit code 1

##### Scenario: S-89 Script calling gh internally
**Traces to**: US-3 AS-9 · **Category**: Alternate Path
- **Given** GitHub on for Dev; `./deploy.sh` runs `gh api user --jq .login`
- **When** Dev runs `./deploy.sh` in God Mode
- **Then** the output is `octocat`
- **And when** Dev runs it in Auto, **then** the GitHub sites are not auto-granted and network is handled as today

#### US-4 — Custom program [S1]

##### Scenario: S-33 Create
**Traces to**: US-4 AS-1 · **Category**: Happy Path
- **Given** no custom entries
- **When** the person creates Acme with word `acme`, variable `ACME_TOKEN`, site `api.acme.example`
- **Then** 201, listed `not_connected`

##### Scenario Outline: S-34 Validation
**Traces to**: US-4 AS-2 · **Category**: Error Path · **Examples**: Dataset D-2
- **Given** the presets only
- **When** the person sends a create or edit with `<input>`
- **Then** `<expected>`

##### Scenario: S-84 Shared program word
**Traces to**: US-4 AS-3 · **Category**: Happy Path
- **Given** custom "GitLab" (word `git`, variable `GITLAB_TOKEN`, site `gitlab.com`, git host `gitlab.com`) connected; GitHub connected; both on for Dev
- **When** Dev runs `git ls-remote https://gitlab.com/o/r`
- **Then** no approval, the managed helper for `gitlab.com` answers with the GitLab token, and the one for `github.com` with the GitHub token

##### Scenario: S-35 Delete refused while on
**Traces to**: US-4 AS-4 · **Category**: Error Path
- **Given** Acme on for Dev
- **When** the person deletes Acme
- **Then** 409 naming Dev; still listed

##### Scenario: S-36 No check defined, token still available
**Traces to**: US-4 AS-5 · **Category**: Alternate Path
- **Given** Acme without account check, on for Dev
- **When** `acme_tok_123456` is saved and Dev runs `printenv ACME_TOKEN | wc -c`
- **Then** `saved_not_checked` with the no-check message, and `16`

##### Scenario: S-37 A preset is data
**Traces to**: US-4 AS-1 · **Category**: Happy Path
- **Given** a test build that adds one preset data file `example.json` and nothing else
- **When** `GET /programs` is called
- **Then** "Example" is listed with its words, variables and sites

##### Scenario: S-93 A second data-only recipe runs
**Traces to**: US-4 AS-1 · **Category**: Happy Path
- **Given** `example.json` has an account check for word `exampletool` with args `["whoami"]`, and a fake `exampletool` printing `ex-user`
- **When** a token is saved for Example
- **Then** `connected` with `ex-user`, with no Go code specific to Example

##### Scenario: S-38 Presets are read-only
**Traces to**: US-4 AS-1 · **Category**: Error Path
- **Given** the presets
- **When** `DELETE /programs/github`
- **Then** 422; still listed

#### US-5 — Per channel, handed-on and later work [S1]

##### Scenario: S-39 Connector default off
**Traces to**: US-5 AS-1 · **Category**: Happy Path
- **Given** GitHub on for Dev; connection `telegram` never changed
- **When** a Telegram message makes Dev run `printenv GH_TOKEN | wc -c`
- **Then** `0` and the withheld-by-channel notice naming `telegram`

##### Scenario: S-40 Channel switched on
**Traces to**: US-5 AS-1 · **Category**: Alternate Path
- **Given** GitHub on for Dev; `/channels/telegram/programs` set `allowed: true`
- **When** a Telegram message makes Dev run the same command
- **Then** `16`

##### Scenario: S-41 Web chat default on
**Traces to**: US-5 AS-2 · **Category**: Happy Path
- **Given** GitHub on for Dev; web-chat setting never changed
- **When** the person asks Dev in the web chat to run the command
- **Then** `16`

##### Scenario: S-42 Web chat off
**Traces to**: US-5 AS-3 · **Category**: Alternate Path
- **Given** GitHub on for Dev; `web_chat_allowed: false`
- **When** the person asks Dev in the web chat
- **Then** `0` with the web-chat notice

##### Scenario: S-43 Mixed connections
**Traces to**: US-5 AS-4 · **Category**: Edge Case
- **Given** `telegram` allowed, `discord` not; GitHub on for Dev
- **When** one Dev turn consumes a message from each and runs the command
- **Then** `0`, notice names `discord`

##### Scenario: S-44 Running command keeps its environment
**Traces to**: US-5 AS-5 · **Category**: Edge Case
- **Given** `telegram` allowed; a Telegram-started background command of Dev prints `${#GH_TOKEN}` every second
- **When** Telegram's setting is turned off and Dev runs a new command from the next Telegram message
- **Then** the running command keeps printing `15`; the new command gets `0`

##### Scenario: S-45 Channel setting has one writer
**Traces to**: US-5 AS-1 · **Category**: Error Path
- **Given** Omnipus may call `set_config`
- **When** it sets `channels.telegram.programs_allowed: true`
- **Then** refused; the setting is unchanged
- **And** when the person rebinds `telegram` to another agent through `PUT /channels/telegram/routing` while the switch is on, the switch is off after that same write

##### Scenario: S-75 Telegram → delegated helper
**Traces to**: US-5 AS-6 · **Category**: Error Path
- **Given** Telegram off; GitHub on for Dev
- **When** a Telegram message makes Dev delegate "run `printenv GH_TOKEN | wc -c`" to a helper
- **Then** the helper's output is `0` with the telegram notice
- **And after** Telegram is switched on, the same delegation outputs `16`

##### Scenario: S-76 Telegram → peer request
**Traces to**: US-5 AS-6 · **Category**: Error Path
- **Given** Telegram off; GitHub on for Ops
- **When** a Telegram message makes Dev send a peer request to Ops, and Ops runs the command
- **Then** Ops's output is `0` with the telegram notice

##### Scenario: S-77 Telegram → new board task; UI task is independent
**Traces to**: US-5 AS-6 · **Category**: Error Path
- **Given** Telegram off; GitHub on for Ops
- **When** a Telegram message makes Dev create a task for Ops, and the task runs
- **Then** the run's command outputs `0` with the telegram notice
- **And** a task with the same text created by the person in the UI follows A-1 and outputs `16`

##### Scenario: S-107 Telegram edits a web-created task
**Traces to**: US-5 AS-6 · **Category**: Error Path
- **Given** Telegram off; GitHub on for Ops; Dev created task T for Ops during a web-chat turn (provenance `{web}`)
- **When** a Telegram message makes Dev change T's `prompt` to "run `printenv GH_TOKEN | wc -c`", and T runs
- **Then** T's stored provenance contains `telegram` (written in the same reconciled write as the prompt), and the run outputs `0` with the telegram notice
- **And when** the gateway restarts between the edit and the run, **then** the result is the same

##### Scenario: S-108 Background completion continues a Telegram turn
**Traces to**: US-5 AS-6 · **Category**: Error Path
- **Given** Telegram off; GitHub on for Dev
- **When** a Telegram message makes Dev start `sleep 1; echo done` in the background, and the completion's system message starts Dev's continuation turn, which runs `printenv GH_TOKEN | wc -c`
- **Then** the continuation's output is `0` with the telegram notice

##### Scenario: S-109 Goal follow-up from a Telegram-created goal
**Traces to**: US-5 AS-6 · **Category**: Error Path
- **Given** Telegram off; GitHub on for Dev; a goal created by Dev during a Telegram turn
- **When** the goal keeper's follow-up turn runs `printenv GH_TOKEN | wc -c`
- **Then** `0` with the telegram notice

##### Scenario: S-110 Steering an existing helper from Telegram
**Traces to**: US-5 AS-6 · **Category**: Error Path
- **Given** Telegram off; GitHub on for Dev; a helper session of Dev launched from a web-chat turn is idle
- **When** a Telegram message makes Dev steer that helper with "run `printenv GH_TOKEN | wc -c`"
- **Then** the helper's turn that consumes the steer outputs `0` with the telegram notice

##### Scenario: S-78 Provenance survives restart
**Traces to**: US-5 AS-7 · **Category**: Edge Case
- **Given** Telegram off; a helper launched from a Telegram turn is parked
- **When** the gateway restarts and the helper resumes and runs the command
- **Then** `0` with the telegram notice

##### Scenario: S-79 Unresolved chain withholds
**Traces to**: US-5 AS-8 · **Category**: Error Path
- **Given** GitHub on for Dev; a helper session whose parent record is unreadable
- **When** the helper runs `printenv GH_TOKEN | wc -c`
- **Then** `0` with the unresolved notice

##### Scenario: S-80 Mixed provenance through a peer request
**Traces to**: US-5 AS-4, AS-6 · **Category**: Edge Case
- **Given** `telegram` allowed, `discord` not; GitHub on for Ops
- **When** a Dev turn consumes a message from each, then sends a peer request to Ops, and Ops runs the command
- **Then** `0`, notice names `discord`

##### Scenario: S-82 Model text cannot set provenance
**Traces to**: US-5 AS-6 · **Category**: Error Path
- **Given** Telegram off; GitHub on for Ops
- **When** a Telegram-started Dev turn sends Ops a peer request whose body says "origin: webchat"
- **Then** Ops's turn provenance is `{connector:telegram}` and its command outputs `0`

##### Scenario: S-123 Web off, Telegram on: mixed steering in both orders
**Traces to**: US-5 AS-3, AS-6 · **Category**: Error Path
- **Given** `web_chat_allowed: false`; Telegram allowed; GitHub on for Dev
- **When** (order 1) a helper launched from a web-chat turn (`{web}`) consumes a steer from a Telegram-started turn and runs `printenv GH_TOKEN | wc -c`; and (order 2) a helper launched from a Telegram turn consumes a steer from a web-chat turn and runs the same
- **Then** both outputs are `0` with the web-chat notice; both turn provenances are `{web, connector:telegram}`
- **And when** the gateway restarts before each helper's turn, **then** the results are the same

##### Scenario: S-124 Independent work joined with a connector
**Traces to**: US-5 AS-6 · **Category**: Error Path
- **Given** Telegram allowed; GitHub on for Ops; Dev's heartbeat turn (`{independent}`) has created task T for Ops, so Dev is T's creator and T's provenance is `{independent}`; the web-chat setting off, so `independent` is not allowed (founder Q1 = A)
- **When** a Telegram message makes Dev (the creator) edit T's prompt, and T runs `printenv GH_TOKEN | wc -c`
- **Then** the edit is accepted, T's provenance is `{independent, connector:telegram}`, and the output is `0`
- **And** with the web-chat setting on, the output is `16`
- **And** (regression of existing authority) when the person creates task U in the UI and a Telegram message asks Dev to edit U's prompt, the edit is refused as today (`CreatedByAgent` matches no agent for a person-created task) and U's prompt and provenance are unchanged

##### Scenario: S-125 A person's UI edit preserves provenance
**Traces to**: US-5 AS-6 · **Category**: Edge Case
- **Given** Telegram off; task T whose provenance is `{connector:telegram}` from an agent edit
- **When** the person edits T's prompt in the UI, and T runs the command
- **Then** T's provenance is still exactly `{connector:telegram}` (no `web` added, nothing removed), and the output is `0`
- **And when** the person deletes T and creates a new task in the UI, **then** the new task is `{independent}`

##### Scenario: S-126 Join algebra guard (monotone restriction)
**Traces to**: US-5 AS-4, AS-6 (FR-045) · **Category**: Edge Case
- **Given** all provenances over the origins {web, independent, connector:a, connector:b} (15 non-empty sets plus `unresolved`) and all 16 on/off assignments of their settings
- **When** the test evaluates every pair (x, y)
- **Then** `eligible(join(x, y))` implies `eligible(x)` and `eligible(y)`; `join` is commutative, associative and idempotent; `unresolved` absorbs
- **And** (instrument check) a planted join that maps `{web} ∪ {connector:a}` to `{connector:a}` fails the test

##### Scenario: S-83 Every origin kind and start path has a provenance row
**Traces to**: US-5 AS-6 (FR-041, FR-045) · **Category**: Edge Case
- **Given** the provenance source table in code
- **When** the test enumerates `session.OriginKind` values, `session.UnifiedSessionType` values and the registered turn-start paths
- **Then** each has exactly one row, and each kind's row names its `provenance_mode` (`root` only for `chat`, `channel` and `main`) and its provenance producer
- **And** a test-only added kind with no row makes the test fail (instrument check)

##### Scenario: S-111 Producer guard and fail-closed default
**Traces to**: US-5 AS-8 (FR-045) · **Category**: Edge Case
- **Given** the source guard over constructions of `bus.InboundMessage`, the steer message, the peer request record, `AsyncNotifyEvent`, and the task, plan, schedule, goal and lifecycle write requests
- **When** it runs on the tree, and again on the tree plus a test-only file that builds a `bus.InboundMessage` without `Provenance`
- **Then** the first run passes and the second fails naming that file
- **And when** that test-only producer is used at run time to start a Dev turn, **then** the turn's command gets no program token and the unresolved notice

#### US-6 — Hidden token [S1]

##### Scenario Outline: S-46 Masking regardless of filter settings
**Traces to**: US-6 AS-1, AS-2 · **Category**: Edge Case · **Examples**: Dataset D-4
- **Given** GitHub connected with the row's token and on for Dev, filter settings as in the row
- **When** Dev runs the row's command
- **Then** the row's expected output

##### Scenario: S-47 Replaced token stays masked
**Traces to**: US-6 AS-3 · **Category**: Edge Case
- **Given** a background command started with `tok_valid_12345` prints `$GH_TOKEN` every second
- **When** the token is replaced and later disconnected
- **Then** every later line in read output and in the completion shows `[FILTERED]`

##### Scenario: S-72 Background success with the general filter off
**Traces to**: US-6 AS-1 · **Category**: Edge Case
- **Given** `filter_sensitive_data: false`; sentinel `sentinel_9f3c2a7e5b1d` connected and on for Dev
- **When** Dev starts `sleep 1; echo $GH_TOKEN` in the background and it completes
- **Then** the published chat message, the continuation input and the transcript contain `[FILTERED]` and not the sentinel

##### Scenario: S-73 Background error notice persisted
**Traces to**: US-6 AS-4 · **Category**: Edge Case
- **Given** S-72's setup with tool feedback suppressed
- **When** `sleep 1; echo $GH_TOKEN; exit 3` completes in the background
- **Then** the persisted error notice and its event payload contain `[FILTERED]` and not the sentinel

##### Scenario: S-74 Truncation at the notifier edge
**Traces to**: US-6 AS-1 · **Category**: Edge Case
- **Given** S-72's setup
- **When** a background command prints 1 MB of `a` minus 10 bytes followed by the sentinel, and completes
- **Then** the continuation input contains no substring of 8 or more characters of the sentinel

##### Scenario: S-102 Token with a quotation mark and a backslash through a JSON read
**Traces to**: US-6 AS-6 · **Category**: Edge Case
- **Given** `filter_sensitive_data: false`; token `q"uo\te_tok_7731` connected and on for Dev
- **When** Dev starts `echo "$GH_TOKEN"; sleep 5` in the background and reads its session output
- **Then** the read result contains neither `q"uo\te_tok_7731` nor its JSON spelling `q\"uo\\te_tok_7731`, and does contain `[FILTERED]`

##### Scenario: S-103 Token across the foreground capture cap
**Traces to**: US-6 AS-7 · **Category**: Edge Case
- **Given** the sentinel connected and on for Dev
- **When** a test build sets the capture cap to **80 bytes** and wraps the input of `cappedBuffer` in a recording sink, and Dev runs a foreground command that prints 70 bytes of `a` followed by the sentinel, so the first **10** sentinel bytes fall inside the cap
- **Then** every byte that entered `cappedBuffer` (as recorded by the sink) contains no substring of 8 or more characters of the sentinel, and contains `[FILTERED]`
- **And** (instrument check) with the masking writer removed from `programs.Attach` in a test build, the recording sink contains the 10-byte fragment and this scenario fails
- **And** separately, with the normal cap, a command printing 1,000 bytes of `a` then the sentinel then `END` gives a delivered result (inside the 64,000-character result limit) that contains `[FILTERED]` and `END` and no 8-character substring of the sentinel

##### Scenario: S-104 Token split across two writes with a read in between
**Traces to**: US-6 AS-7 · **Category**: Edge Case
- **Given** the sentinel connected and on for Dev
- **When** a background command prints the first 10 bytes of the sentinel, flushes, sleeps 3 s, prints the rest; Dev reads its output after 1 s and again after completion
- **Then** neither read nor the completion contains a substring of 8 or more characters of the sentinel, and the completion shows `[FILTERED]`

##### Scenario Outline: S-130 Output interrupted inside a token
**Traces to**: US-6 AS-7 · **Category**: Edge Case · **Examples**: Dataset D-9
- **Given** the sentinel `sentinel_9f3c2a7e5b1d` (L = 21 bytes) connected and on for Dev; general filter off
- **When** a command writes the first `<n>` bytes of the sentinel to `<stream>` and is then ended by `<interruption>`, and the output is delivered through `<sink>`
- **Then** the delivered output contains no substring of 8 or more bytes of the sentinel; for n ≥ 8 it contains `[FILTERED]`; for n = 7 the 7 bytes may appear (below the protection threshold)

##### Scenario: S-106 Spawn guard: no child gets program variables without masking
**Traces to**: US-6 AS-1 (FR-045) · **Category**: Edge Case
- **Given** the spawn-site table with per-slice inventories: **S1 mandatory**: bash foreground, background and God Mode; environment_setup; account check; git version probe. **Added in S2**: sign-in program, version probe and read-back.
- **When** the source guard runs on the tree, and again with a test-only file that adds `GH_TOKEN` to an `exec.Cmd` environment without `programs.Attach`
- **Then** the first run passes and the second fails naming that file; any child-start site not in either inventory fails the guard in every slice
  - *Clarification (ruling-check-delta.md "S-83 path rows" item 3, 2026-11-25):* "any child-start site" means any child-start site in the scanned roots `pkg/agent`, `pkg/tools`, `pkg/programs` and `pkg/channels/weixin`. The check for a program variable or program-token read reaching a child environment without `programs.Attach` covers all of `pkg/`, `cmd/` and `internal/`. Scrubbing of every other child is FR-239's inventory (system-agent spec), not this scenario.
- **And** the runtime part runs every child kind of the **current** slice's mandatory inventory with the sentinel and finds it in no sink (S1 runs six kinds; S2 runs all nine)

##### Scenario: S-48 Sentinel absent from every sink
**Traces to**: US-6 AS-1 · **Category**: Happy Path
- **Given** the sentinel connected and on for Dev, filter off
- **When** Dev runs `echo $GH_TOKEN` in the foreground and background (success and error), the token is rotated, and Dev is exported
- **Then** transcript, model request record, published chat messages, gateway log, audit file, account labels and export bundle contain no sentinel

##### Scenario: S-49 Export lists the program; import arrives off
**Traces to**: US-6 AS-5 · **Category**: Happy Path
- **Given** GitHub connected and on for Dev
- **When** Dev is exported and the bundle imported as "Dev2"
- **Then** `needs_secrets` has `{name: "github", kind: "program"}` and Dev2's `programs` is `[]`

##### Scenario: S-50 Audit rows value-free
**Traces to**: US-6 AS-1 · **Category**: Happy Path
- **Given** GitHub not connected
- **When** the person connects, re-checks, switches on for Dev, changes the Telegram setting and disconnects
- **Then** one action row per action (connect, check, switch, setting, disconnect), named by its event, plus one check-result row for each finished check (the check after connect and the re-check), none containing the token or any part of it

#### US-7 — Replace, check, disconnect [S1]

##### Scenario: S-51 Replace
**Traces to**: US-7 AS-1 · **Category**: Happy Path
- **Given** connected (generation 1); the fake `gh` prints `hubot` for `tok_new_654321`
- **When** `tok_new_654321` is saved
- **Then** "Connected as hubot", generation 2, and Dev's next `printenv GH_TOKEN | wc -c` is `15`

##### Scenario: S-52 Check again
**Traces to**: US-7 AS-2 · **Category**: Alternate Path
- **Given** connected; the fake `gh` now exits 1 with `HTTP 401: Bad credentials`
- **When** "Check again"
- **Then** `check_failed`; generation unchanged

##### Scenario: S-53 Disconnect
**Traces to**: US-7 AS-3 · **Category**: Happy Path
- **Given** connected and on for Dev
- **When** the person confirms "Disconnect"
- **Then** `not_connected`, generation +1, Dev's next command has no `GH_TOKEN`, Dev's switch stays on, and the dialog states the token stays valid at GitHub until revoked there

#### US-8 — Secure card in chat [S1]

##### Scenario: S-54 Program card in web chat
**Traces to**: US-8 AS-1, AS-3 · **Category**: Happy Path
- **Given** GitHub not connected; Dev in a web chat
- **When** Dev calls `request_secret` with `program: "github"` and the person submits `tok_valid_12345` from the browser
- **Then** the card says `GitHub (gh, git)` and `Used by every agent you switch GitHub on for`, and its display carries `program_id: "github"`; GitHub becomes `checking` with generation N and the submitted request state carries `program_generation: N`; Dev continues at once, and its continuation says the token is stored for GitHub and the account check is running (it does not claim a result); when the check ends the card shows `Connected as octocat`; Dev's `programs` is unchanged

##### Scenario: S-55 Refused in a channel turn
**Traces to**: US-8 AS-2 · **Category**: Error Path
- **Given** a Telegram-started Dev turn
- **When** Dev calls `request_secret` with `program: "github"`
- **Then** refused with the existing channel-turn card text

##### Scenario: S-56 Unknown program id
**Traces to**: US-8 AS-1 · **Category**: Error Path
- **Given** a web-chat Dev turn
- **When** Dev calls `request_secret` with `program: "nope"`
- **Then** `nope is not a program in Settings → Programs.`

##### Scenario: S-99 Chat card uses program token rules
**Traces to**: US-8 AS-4 · **Category**: Error Path
- **Given** a pending GitHub program card
- **When** the person submits `abc123` from the browser
- **Then** 400 with the too-short message; the request stays pending; nothing stored

##### Scenario: S-112 Same-owner API token on a program card
**Traces to**: US-8 AS-5 · **Category**: Error Path
- **Given** a pending GitHub program card owned by the person
- **When** a client uses the person's own API bearer token to `POST /secret-requests/{id}/submit` with `tok_valid_12345`
- **Then** 403 `this action requires a signed-in person`, decided from the stored request kind before the body is read; the card stays pending; nothing is stored; no check runs
- **And when** the person submits the same value from the browser session, **then** the card is submitted (positive control)
- **And** a pending **non-program** card submitted with the same bearer token behaves exactly as today (regression)

##### Scenario: S-134 Agent update with and without programs from a bearer token
**Traces to**: US-2 AS-7 · **Category**: Error Path
- **Given** GitHub connected; Dev's `programs` is `[]`; the person's API bearer token
- **When** it sends `PUT /agents/dev` with `{"description":"x","programs":["github"]}`, and separately with `{"description":"x"}`
- **Then** the first returns 403 `this action requires a signed-in person` after the bounded body decode and **before any mutation** (description and programs both unchanged); the second succeeds exactly as today
- **And** the same first request from the browser session succeeds

##### Scenario: S-135 A second Store object cannot write back stale program state
**Traces to**: US-1 AS-1, US-7 AS-1 (FR-049) · **Category**: Edge Case
- **Given** a temp credential file; two independent `credentials.Store` objects A and B on the same path, both unlocked; GitHub connected through A with `tok_valid_12345` under reference `R` (generation 1) and a card receipt `CR`
- **When** B calls `Set("UNRELATED", "x")` and a test seam pauses B **inside its transaction, after its in-lock read**; then A starts replacing the token with `tok_new_67890` (generation 2) through `CommitProgramCredential`; after A reports "waiting for admission" and 200 ms pass, B is released
- **Then** while B is paused, A has not returned and the file still holds `tok_valid_12345` (A cannot enter); after release both complete, and the file holds `R` = `tok_new_67890`, GitHub's generation 2 metadata, `CR`, and `UNRELATED` = `x`
- **And** the disconnect variant (A calls `RemoveProgramCredential` while B is paused) leaves `R` absent and `UNRELATED` present
- **And** (instrument check) in a test-build mutant where `s.transact` reads the file **before** taking the locks, the seam pauses B after that outside read; A commits while B is paused; B's write then restores `tok_valid_12345`, and this scenario fails

##### Scenario: S-136 The CLI writer and the gateway do not lose each other's writes
**Traces to**: US-1 AS-1 (FR-049) · **Category**: Edge Case
- **Given** a running test gateway on a temp home, and the `omnipus credentials set` command in a separate process (Unix)
- **When** the CLI runs `credentials set OTHER y` with a test-build seam that pauses it **inside its transaction, after its in-lock read**, until a signal file appears; the person pastes a GitHub token in Settings; after 1 s the signal file is created (within the 5 s bound)
- **Then** while the CLI is paused the paste request has not returned and GitHub's generation is unchanged; after release both complete, and the GitHub token (with its generation and metadata) and `OTHER` are in the file
- **And** H-17 checks the same on Windows by hand

##### Scenario: S-137 One standing chat, two origins, separate turns
**Traces to**: US-5 AS-1, AS-6 (FR-050) · **Category**: Edge Case
- **Given** web chat on, Telegram off; GitHub connected and on for Dev; Dev's main chat is reached both from the web and from Telegram (same standing session, a `root` record)
- **When** a Telegram message makes Dev run `printenv GH_TOKEN | wc -c`, then the person types the same request in the web chat; the gateway restarts; then the order is reversed (web first, then Telegram)
- **Then** the Telegram turns print `0` and the web turns print `16`, in both orders and after the restart; the root record stores no provenance
- **And** (instrument check) storing and joining the root's provenance in a test build makes the web turn after the Telegram turn print `0`, and this scenario fails

##### Scenario: S-138 Delegated child control keeps its restriction
**Traces to**: US-5 AS-2, AS-6 (FR-050) · **Category**: Error Path
- **Given** web chat on, Telegram off; GitHub on for Ops; a Telegram turn of Dev delegates to Ops (child record `provenance_mode: lineage`, `{connector:telegram}`)
- **When** the person steers that child from the web chat and it runs `printenv GH_TOKEN | wc -c`; and separately a child record of the same shape without stored provenance (as written before this feature) is woken
- **Then** both print `0`: the steered child is `{connector:telegram, web}` and the record without provenance is `unresolved`
- **And** a top-level record with origin `main`, no parent, no task evidence and not plan-owned is classified `root` on load; a top-level record of any work kind is `lineage` (S-143, S-144)

##### Scenario: S-139 A delayed check launch cannot stop the newer check
**Traces to**: US-7 AS-1 (FR-047) · **Category**: Edge Case
- **Given** GitHub not connected; a fake `gh` whose check waits on a barrier; test barriers between "commit and reserve" and "launch"
- **When** paste A commits generation 1 and reserves its check, then pauses before launch; paste B commits generation 2, reserves and launches its check; then A's launch resumes
- **Then** A's launch starts no process and stops nothing; B's check runs to a terminal state (`connected` with B's label when released); the audit shows A's reservation `superseded` once
- **And** (instrument check) letting a launch supersede the current check in a test build leaves generation 2 in `checking` with no live check, and this scenario fails

##### Scenario: S-140 Program card while the check is slow, fails, or the page reloads
**Traces to**: US-8 AS-3 (FR-051) · **Category**: Edge Case
- **Given** GitHub not connected; Dev in a web chat; a fake `gh` whose check waits on a barrier
- **When** Dev calls `request_secret` with `program: "github"` and the person submits a token; the page is reloaded while the check is running; then the check is released with exit 1
- **Then** Dev continues once, at once, saying the token is stored and being checked; after the reload the card still shows "Stored — checking the account…" from `program_id` and `program_generation`; after release the card shows the failed-check message and Settings → Programs shows the same; Dev's continuation never claimed a result

##### Scenario: S-141 Replaced before the card's check finished
**Traces to**: US-8 AS-3, US-7 AS-1 (FR-051) · **Category**: Edge Case
- **Given** a program card submitted with generation N whose check waits on a barrier
- **When** the person pastes a different token in Settings (generation N+1), and both checks are released
- **Then** the card shows "Stored — replaced by a newer token" and never shows generation N+1's account; Settings shows N+1's result; the request state still carries `program_generation: N`

##### Scenario: S-143 A parentless task session keeps its restriction on re-entry and restart
**Traces to**: US-5 AS-3, AS-6 (FR-050) · **Category**: Error Path
- **Given** web chat on, Telegram off; GitHub on for Ops; a Telegram turn of Dev creates task T for Ops (`{connector:telegram}`); T runs in its own task session (no parent, `TaskID` set)
- **When** T's run executes `printenv GH_TOKEN | wc -c`; then the person sends a web message into T's task session asking for the same; then the gateway restarts and the task session is revived
- **Then** the session's record is `lineage` with `{connector:telegram}`; every run prints `0`; the web re-entry turn is `{connector:telegram, web}`, still withheld
- **And** (instrument check) a test-build classifier that returns `root` for any parentless non-delegate record makes the web re-entry print `16`, and this scenario fails

##### Scenario: S-144 Every work kind is lineage; unknown identity fails closed
**Traces to**: US-5 AS-6, AS-8 (FR-050) · **Category**: Edge Case
- **Given** Telegram off; GitHub on for Dev; one session of each kind created through its real producer: isolated scheduled (from a Telegram-created schedule), heartbeat, verifier of Telegram-started work, plan owner of a Telegram-created plan; plus three stored records written without `provenance_mode`: a parentless task session, a `main` session, and a parentless record whose origin is missing
- **When** each is woken and runs `printenv GH_TOKEN | wc -c`
- **Then** the four work sessions are `lineage` and print `0` (heartbeat follows founder Q1 via A-1: `16` with web on); the old task session and the record with no origin are `lineage` and `unresolved` and print `0`; the old `main` session is `root`, and a web turn in it prints `16`

##### Scenario: S-146 A busy store fails ordinary writes truthfully
**Traces to**: US-1 AS-1 (FR-049) · **Category**: Error Path
- **Given** GitHub connected with `tok_valid_12345` (generation 1); a writer held inside its transaction by a test seam, once in the same process (second `Store` object) and once in another process (sidecar lock held)
- **When** for each holder the person pastes `tok_new_67890` and the holder is kept for 6 s; and separately the holder is released after 1 s
- **Then** with the 6 s holder the paste returns 503 `The credential store is busy. Try again.` after 5 s (± 0.5 s), GitHub is still generation 1 with the old token in use, no check started, and nothing is written after the 503 (file unchanged 2 s after the holder is released); with the 1 s holder the paste succeeds as generation 2
- **And** the CLI `credentials set` against a 6 s holder exits non-zero with the busy message and no change

##### Scenario: S-148 Program switch on the Connectors page
**Traces to**: US-5 AS-9 (FR-009) · **Category**: Happy Path
- **Given** the browser on the Connectors page; channel `telegram` bound to workspace "Home" and agent Dev; channel `telegram.eu` not bound
- **When** the person opens `telegram`, goes to its Programs section and switches it on using only the keyboard; then opens `telegram.eu`
- **Then** the `telegram` panel shows "Bound to Home · Dev" and the switch off before the change; after it the switch is on and announced, and a reload shows the same; the `telegram.eu` panel shows the unbound message
- **And** when the PUT fails, the switch returns to off with "Not confirmed" and Retry

##### Scenario: S-113 Route sweep: only the gated paths change a program credential
**Traces to**: US-8 AS-5, US-2 AS-7 (FR-045) · **Category**: Edge Case
- **Given** a **real** temp store where GitHub is connected with `tok_valid_12345` under reference `R` (generation 1); a fake `gh` that records starts; a valid local re-auth token minted before each call; the test runs once in local and once in platform edition mode
- **When** every registered HTTP route is called with the person's API bearer token, using the **route-specific valid payload** from the sweep table when the route has one (for `PUT /api/v1/credentials`: `{"key":"R","value":"tok_evil_999999"}`; for `DELETE /api/v1/credentials/R`; for secret-request submit: a pending program card; for Programs routes: their schemas), and otherwise GET, POST, PUT and DELETE with `{"key":"R","value":"tok_evil_999999"}`
- **Then** after the sweep the decrypted store still holds `tok_valid_12345` under `R`, GitHub's `generation` is 1 and its metadata unchanged, and the fake program started 0 times
- **And** the sweep table is checked against the registered route list: a route that reaches a credential store mutator (from the I-3 inventory) without a payload row fails the test

##### Scenario: S-127 Store guard covers every mutating method
**Traces to**: US-8 AS-5 (FR-045, FR-048) · **Category**: Edge Case
- **Given** a temp store with GitHub connected under reference `R`, and the classification table of `*credentials.Store` methods
- **When** the test enumerates the exported method set by reflection, and calls every **guarded** method (`Set`, `Delete`, `RemoveReference`, `SelectPrivateVersion`, `SetPrivateChannelAvailability`, `CommitCard` with recipient kind `program`) on `R`
- **Then** every method has exactly one classification row; each guarded call returns `ErrProgramOwned` (or the existing `ErrNotPrivateSecret` for the two private-only methods); the decrypted store and program metadata are byte-identical afterwards
- **And** `Rotate` leaves `R`'s decrypted value unchanged; receipt pruning leaves `R` unchanged; the boot orphan sweep skips `R` without failing start-up
- **And** a test-only exported mutating method added to `Store` without a classification row makes the test fail (instrument check)
- **And** a source check finds `CommitProgramCredential` and `RemoveProgramCredential` called only from `pkg/programs`
- **And** (d) a source check finds every exported mutating method changing the file only inside `s.transact`; a test-only mutator that pairs `loadFileInternal` and `saveFileNoLock` outside it makes the test fail (instrument check)

##### Scenario: S-128 Generic credentials API cannot overwrite or delete a program token
**Traces to**: US-8 AS-5 (FR-048) · **Category**: Error Path
- **Given** GitHub connected under reference `R`; the person's browser session; a valid re-auth token; local and platform edition modes
- **When** the person sends `PUT /api/v1/credentials` with `{"key":"R","value":"tok_evil_999999"}`, and then `DELETE /api/v1/credentials/R`
- **Then** both return 409 `R belongs to the program GitHub. Change it in Settings → Programs.`; the stored value and generation are unchanged
- **And** the same calls for an ordinary provider key behave as today (regression)

##### Scenario: S-129 Agent tool remove_secret cannot remove a program token
**Traces to**: US-8 AS-5 (FR-048) · **Category**: Error Path
- **Given** GitHub connected under reference `R`; Omnipus may call `remove_secret` (approved)
- **When** it calls `remove_secret` with reference `R`
- **Then** the tool returns the program-owned refusal; GitHub stays connected; a private-variable reference is still removable as today

#### US-9 — Sign-in [S2]

##### Scenario: S-57 Link and code
**Traces to**: US-9 AS-1 · **Category**: Happy Path
- **Given** a fake `gh` that prints `! First copy your one-time code: ABCD-1234` and `Open this URL to continue in your web browser: https://github.com/login/device`, then waits
- **When** the person clicks "Sign in" for GitHub
- **Then** within 15 s `waiting_for_person`, `user_code: ABCD-1234`, `verification_url: https://github.com/login/device`

##### Scenario: S-58 Approval connects; program copy deleted
**Traces to**: US-9 AS-2 · **Category**: Happy Path
- **Given** S-57's attempt waiting; the fake then writes `tok_signin_123456` into its config folder and exits 0, and read-back prints it
- **When** the program exits
- **Then** `connected` with the checked account, generation +1, `cleanup: done`, the attempt folder gone, the stored token `tok_signin_123456`

##### Scenario: S-132 Verified publication runs one check and commits once
**Traces to**: US-9 AS-2, US-10 AS-5 · **Category**: Edge Case
- **Given** S-57's attempt waiting; the fake `gh` counts its account-check invocations; read-back prints `tok_signin_123456`
- **When** the program exits 0 and verification completes
- **Then** the account check ran exactly **1** time, the state is `connected` immediately after the single commit write, and no `checking` state is observed
- **And when** the store write fails, **then** the attempt is `failed`, the previous credential and label are unchanged, and the UI says the previous token is still in use
- **And when** a newer paste lands after the check and before the commit, **then** the verified publication's precondition fails, nothing is written, and the attempt is `cancelled`

##### Scenario: S-59 No recipe, no button
**Traces to**: US-9 AS-3 · **Category**: Alternate Path
- **Given** Fly has no recipe
- **When** `GET /programs` and `POST /programs/fly/sign-in`
- **Then** Fly `sign_in.available: false` with the unsupported message; the POST returns 422

##### Scenario: S-60 Person's own login untouched
**Traces to**: US-9 AS-4 · **Category**: Edge Case
- **Given** checksums of `~/.config/gh/hosts.yml` and the keychain item `gh:github.com` (attributes and modification date)
- **When** an attempt completes `connected`
- **Then** both are unchanged

##### Scenario: S-61 Link fails validation
**Traces to**: US-9 AS-1 · **Category**: Error Path
- **Given** the fake prints a valid code and the link `<link>` (each of `https://github.com.evil.example/login/device`, `http://github.com/login/device`, `https://github.com/settings/tokens`)
- **When** an attempt starts
- **Then** `unsupported` with the unrecognised-output message and no link shown

##### Scenario: S-62 Environment from scratch
**Traces to**: US-9 AS-4 · **Category**: Edge Case
- **Given** the gateway environment has `GH_TOKEN`, `GITHUB_TOKEN`, `GH_HOST`, `GH_ENTERPRISE_TOKEN`; the fake records its environment
- **When** an attempt starts
- **Then** none of them reaches the fake; `GH_CONFIG_DIR` points inside `$OMNIPUS_HOME/system/program-sign-in/<attempt-id>/`; `OMNIPUS_SIGN_IN_MARKER` is present

##### Scenario: S-71 Chat card → sign-in → settled card → continuation
**Traces to**: US-9 AS-5 · **Category**: Happy Path
- **Given** Dev's GitHub program card is pending in a web chat and Dev's turn is parked
- **When** the person uses the card's "Sign in", approves, and the attempt reaches `connected`
- **Then** the credential and the card's `CardReceipt` were written in one store write; the request is `submitted`; Dev continues once (a replayed settle starts no second turn); the continuation states the account
- **And when** the attempt instead ends `cancelled` or `failed`, **then** the card stays pending and offers "Paste a token" and "Sign in"

##### Scenario: S-121 Crash between credential write and card settlement
**Traces to**: US-9 AS-5 · **Category**: Edge Case
- **Given** S-71's setup with barriers (a) after the combined credential-and-receipt write, before the registry marks `submitted`; (b) after `submitted`, before `ClaimResume`; (c) after `ClaimResume` has durably appended the resume entry, before `runAgentLoop`
- **When** the gateway is killed at each barrier and started again
- **Then** in all three cases the card is `submitted` (never `interrupted`, because the receipt exists) and GitHub is connected with the new credential
- **And** at (a) and (b) the existing recovery dispatches the continuation, and Dev runs **one** continuation turn
- **And** at (c) Dev runs **zero** continuation turns: the resume entry suppresses re-dispatch (`resumeLocked`), and boot recovery stops the interrupted conversation (`recoverOrdinaryRoot`) with the existing restart-stop state. The person can continue the chat by sending a message. Across all three cases Dev never runs **more than one** continuation
- **And** with Stop pressed before the crash, the credential stays stored and no continuation starts at any barrier

##### Scenario: S-98 Stop before sign-in completes
**Traces to**: US-9 AS-5 · **Category**: Edge Case
- **Given** S-71's attempt is `waiting_for_person`, bound to a pending GitHub card
- **When** the person presses Stop on Dev's chat (the card becomes `stopped`, reason `session_stopped`) and the attempt then reaches `connected`
- **Then** the credential is stored with generation N+1; the receipt is marked `resume_vetoed`; the card is `submitted` with `ResumeStopped` and `program_generation` N+1; no continuation starts, also after a restart
- **And** when instead Stop comes **after** the commit, the card is already `submitted` and Stop applies the existing veto to the continuation (existing behaviour, unchanged)
- **And** (instrument check) a commit that requires `pending` only (the revision 5 fence) refuses here and the scenario fails

##### Scenario: S-94 Sign-in from a card not owned
**Traces to**: US-9 AS-5 · **Category**: Error Path
- **Given** a request id that is not pending, is for Fly, or belongs to another account
- **When** `POST /programs/github/sign-in` names it
- **Then** 409 and no attempt starts

#### US-10 — Sign-in ends cleanly [S2]

##### Scenario: S-63 Cancel while waiting
**Traces to**: US-10 AS-1 · **Category**: Alternate Path
- **Given** an attempt `waiting_for_person`
- **When** the person cancels
- **Then** `cancelled`, process group gone, folder deleted, `cleanup: done`

##### Scenario: S-119 Cancel while verifying
**Traces to**: US-10 AS-1 · **Category**: Edge Case
- **Given** an attempt in `verifying` with read-back held at a barrier
- **When** the person cancels, then the barrier releases
- **Then** `cancelled`, nothing published, previous credential kept, folder deleted

##### Scenario: S-64 Expiry
**Traces to**: US-10 AS-2 · **Category**: Edge Case
- **Given** a test recipe with `expiry_seconds: 60`, a test clock, the attempt `waiting_for_person`
- **When** the clock passes 60 s
- **Then** `expired`; folder deleted

##### Scenario: S-120 Expiry time passes while verifying
**Traces to**: US-10 AS-2, AS-5 · **Category**: Edge Case
- **Given** a test recipe with `expiry_seconds: 60` and a test clock; the program exits 0 at 55 s; read-back held at a barrier
- **When** the clock passes 60 s and the barrier releases at 65 s (10 s into read-back, inside its 15 s limit)
- **Then** the attempt is not expired (expiry applies only while waiting) and ends `connected`
- **And when** instead the barrier is never released, **then** at 55 + 15 = 70 s the read-back is stopped and the attempt ends `failed` (read-back timeout), not `expired`

##### Scenario: S-133 The 60 s verifying deadline
**Traces to**: US-10 AS-5 · **Category**: Edge Case
- **Given** a test clock; the program exits 0 at t = 0; read-back returns at once; the check returns `octocat` at t = 5 s; the verified commit is held at a barrier **before it takes the entry lock**
- **When** the clock passes t = 60 s and then the barrier releases
- **Then** at 60 s the attempt is `failed` with the verifying-timeout message; after release the precondition "inside its verifying deadline" fails, nothing is written, and the previous credential is in use

##### Scenario: S-65 Denied
**Traces to**: US-10 AS-3 · **Category**: Error Path
- **Given** an attempt waiting; the fake prints a `denied_patterns` match
- **When** it exits 1
- **Then** `denied` with the denied message

##### Scenario: S-66 Overlapping attempts
**Traces to**: US-10 AS-4 · **Category**: Edge Case
- **Given** attempt 1 held at a barrier after its program exits 0 with `tok_stale_123456`
- **When** attempt 2 starts, then the barrier releases
- **Then** attempt 1 reads 200 `cancelled`; the stored token is not `tok_stale_123456`

##### Scenario: S-91 Paste during sign-in supersedes it
**Traces to**: US-10 AS-4 · **Category**: Edge Case
- **Given** an attempt `waiting_for_person`
- **When** the person pastes `tok_valid_12345`
- **Then** the attempt is `cancelled`; its later completion cannot replace the pasted token

##### Scenario: S-92 Disconnect during verifying
**Traces to**: US-10 AS-4 · **Category**: Edge Case
- **Given** an attempt held at a barrier in `verifying`
- **When** the person disconnects, then the barrier releases
- **Then** `cancelled` (the single supersession outcome); the entry stays `not_connected`

##### Scenario: S-67 Read-back fails
**Traces to**: US-10 AS-5 · **Category**: Error Path
- **Given** connected as `octocat`; an attempt whose program exits 0 and whose read-back exits 1
- **When** verifying runs
- **Then** `failed`; token and account unchanged

##### Scenario: S-118 Read-back hangs
**Traces to**: US-10 AS-5 · **Category**: Error Path
- **Given** an attempt whose program exits 0 and whose read-back sleeps 60 s
- **When** verifying runs
- **Then** after 15 s the read-back is stopped and the attempt is `failed`; folder deleted; the UI never stays "Checking the account…"

##### Scenario: S-68 Store write fails
**Traces to**: US-10 AS-5 · **Category**: Error Path
- **Given** connected as `octocat`; the attempt's read-back prints `tok_signin_123456` and the next store write fails
- **When** verifying runs
- **Then** `failed`; "Connected" is never reported; the previous token is in use

##### Scenario: S-100 Start timeout
**Traces to**: US-10 AS-5 · **Category**: Error Path
- **Given** the fake prints nothing for 16 s
- **When** an attempt starts
- **Then** `failed` with the 15 s message; process stopped; folder deleted

##### Scenario: S-101 Unrecognised output
**Traces to**: US-10 AS-5 · **Category**: Error Path
- **Given** the fake prints a valid link and a code that does not match `code_pattern`
- **When** an attempt starts
- **Then** `unsupported`

##### Scenario: S-69 Restart sweep stops the old program
**Traces to**: US-10 AS-6 · **Category**: Edge Case
- **Given** a gateway killed with SIGKILL while a fake program waits (Linux: kill the process with Pdeathsig disabled in the test build, to simulate a missed signal)
- **When** the gateway starts
- **Then** before the first request is served, the program is stopped and the folder deleted

##### Scenario: S-117 Launch protocol crash barriers
**Traces to**: US-10 AS-6 · **Category**: Edge Case
- **Given** fault barriers (a) after `intent.json`, before start; (b) after start, before `owner.json`; (c) after `owner.json`
- **When** the gateway is killed at each barrier and started again
- **Then** at (a) no program exists and the folder is deleted; at (b) the marker scan finds and stops the program, then deletes the folder; at (c) the owner receipt stops it; in all three no process carrying the marker remains

##### Scenario: S-96 Ownership mismatch
**Traces to**: US-10 AS-6 · **Category**: Edge Case
- **Given** `owner.json`'s PID now belongs to an unrelated process with a different start time and no marker
- **When** the gateway starts
- **Then** that process is not signalled; the folder is deleted

##### Scenario: S-97 Cleanup failure is reported
**Traces to**: US-10 AS-6 · **Category**: Error Path
- **Given** an attempt ending `cancelled` whose folder cannot be deleted
- **When** cleanup runs
- **Then** the attempt reads `cancelled` with `cleanup: failed`, an audit row records it, and the entry's `sign_in_warning` is set

##### Scenario: S-122 Cleanup warning stays visible after restart
**Traces to**: US-10 AS-6 · **Category**: Error Path
- **Given** S-97's state, then the gateway restarts and the attempt id is no longer known
- **When** `GET /programs` and `GET …/sign-in/{old attemptId}`
- **Then** GitHub's `sign_in_warning` still shows the cleanup message (the old attempt reads 404); after a sweep that succeeds, the warning is cleared

##### Scenario: S-95 Agent cannot read the attempt folder
**Traces to**: US-10 AS-7 · **Category**: Error Path
- **Given** an attempt `waiting_for_person` with a token file in its folder
- **When** Dev runs `cat` on that file and `read_file` on it
- **Then** both are refused by the existing protection of `$OMNIPUS_HOME/system/`

##### Scenario: S-142 Cancelling a card ends its sign-in
**Traces to**: US-9 AS-5, US-10 AS-1 (FR-040) · **Category**: Edge Case
- **Given** a pending GitHub program card in Dev's web chat; the fake `gh` sign-in; test barriers at "attempt started" and "before the verified commit"
- **When** (a) the person cancels the card after the attempt started; (b) the attempt reaches the commit barrier, the person cancels the card, then the commit resumes; (c) the attempt commits, then the person cancels
- **Then** (a) the attempt ends `cancelled`, its process group is stopped and no credential or receipt is written; (b) the commit re-checks the card under the registry lock, finds it cancelled, writes nothing and ends `cancelled`, previous credential kept; (c) the cancel gets the existing 409 `submitted` and Dev continues exactly once
- **And** no lock is held while the vendor login or the account check runs. With the login barrier held, and again with the check barrier held: an authenticated `GET /api/v1/secret-requests/{id}` for this card returns 200 with state `pending` within 2 s (registry lock); a test seam's `TryLock` of the GitHub entry lock and of the gateway config lock each succeed within 100 ms
- **And** (instrument check) three test-build mutants, each holding one of those locks over the login subprocess, make the matching probe fail

##### Scenario: S-145 Stop, crash and recovery around a card-bound commit
**Traces to**: US-9 AS-5, US-10 AS-1 (FR-040) · **Category**: Edge Case
- **Given** S-98's setup; a test seam that crashes the gateway after the store write and before the card settlement is persisted
- **When** (a) Stop, then the attempt commits, and the crash seam fires, then the gateway restarts; (b) the conversation is deleted (card `stopped`, reason `session_deleted`) before the commit; (c) the card is cancelled before the commit
- **Then** (a) after restart the card is `submitted` with `ResumeStopped`, the credential is in use, and no continuation starts; (b) and (c) write nothing, the attempt ends `cancelled`, and the previous credential is kept
- **And** a stored `stopped` card with a receipt **not** marked `resume_vetoed` (planted) stays `stopped`, is audited once as an inconsistency, and starts no continuation

##### Scenario: S-147 Store contention across the verifying deadline
**Traces to**: US-10 AS-5 (FR-034, FR-049) · **Category**: Edge Case
- **Given** a test clock; a card-bound attempt whose check returned `octocat` at t = 5 s; a store writer held inside its transaction by a test seam, in one run in-process and in another run cross-process
- **When** (a) the holder is kept until t = 61 s; (b) the holder is released at t = 58 s and a seam pauses the synchronous write until t = 62 s; (c) as (b), and the person cancels the card at t = 59 s; (d) the holder is kept until t = 61 s and the person cancels at t = 30 s
- **Then** (a) at t = 60 s the attempt is `failed` with the verifying-timeout message, nothing is written after release, and the registry and entry locks are free from t = 60 s; (b) the commit claims at t = 58 s and ends `connected` at t = 62 s, and the timer at t = 60 s changes nothing; (c) the cancel waits for the commit and gets 409 `submitted`; the result is `connected` and Dev continues once; (d) the cancel wins the claim, the commit's admission is abandoned, and nothing is written
- **And** (instrument check) a test build that re-checks the deadline only before admission publishes in (a) after release, and this scenario fails

##### Scenario: S-70 Raw output never shown
**Traces to**: US-10 AS-1 · **Category**: Edge Case
- **Given** the fake also prints `secret_line_77aa`
- **When** attempts are driven to each of the six end states
- **Then** no response, log line or audit row contains it

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit | catalog/recipe validation; value rules; conflicts; env composition; git entries; word matching; masking writer (fragmentation); label rule; provenance join and eligibility; drift and source guards; generation and check-id rules | logic |
| Integration | REST with real person gate; card submit route; check and sign-in with fake programs and barriers; bash env per mode; background delivery and reads; continuation, goal, steer, peer, task update and restart with persisted records; route sweep; audit; export/import | components together |
| Browser (targeted Playwright) | Programs page, custom form, agent switches, channel setting, chat card, sign-in dialog | user view |
| Hands-on (exact cut commit) | real `gh`, `git`, `fly` on macOS and Linux; Windows for S1 | vendor and platform truth |

### Hands-on UAT rows

| Row | Acceptance criterion | Steps | Expected result | Affected area | Validator critical path | Automation candidate |
|---|---|---|---|---|---|---|
| H-00 [S1] | Rider #1044 | macOS, Auto, proxy on: approve `curl -sI https://github.com` | Reaches github.com | proxy | Re-run | Yes |
| H-01 [S1] | US-1 AS-1 | Paste a real fine-grained token | "Connected as <real login>" | Programs page | `GET /programs` value-free | Partly |
| H-02 [S1] | US-3 AS-1, AS-3 | GitHub on for Dev; Auto; `gh api user --jq .login` | Real login; no prompt | env, network | New chat | No |
| H-03 [S1] | US-3 AS-2 | Repo `R` writable only by A; GitHub connected as A; the machine's own git helper holds B's login; a logging wrapper records operation names only; Dev pushes to `R` | Push succeeds; wrapper logged no `store`/`erase`; B's keychain item or `~/.git-credentials` hash unchanged; author metadata not used; no token in evidence | git helper | Re-run with GitHub off: push fails 403 | No |
| H-04 [S1] | US-3 AS-5 | Ask; approve `gh api user` | One prompt | Ask network | Count prompts | Yes |
| H-05 [S1] | US-3 AS-6 | God Mode; `gh api user` | Works | God env | — | Yes |
| H-06 [S1] | US-4 | Fly token: `fly auth whoami`, `flyctl apps list` in Auto | Real account; no prompt | Fly sites (G3) | Proxy audit | No |
| H-07 [S1] | US-3 AS-2 Windows | Windows: paste; `git ls-remote <private repo>`; `gh api user` | Both work, else recorded unsupported for the helper | Windows | Re-run | No |
| H-08 [S1] | US-5 AS-6 | Telegram off: message Dev to run `gh api user` directly, via a helper, via a task, by editing a web-created task, and via a background completion | Withheld in all five; after switching Telegram on, all work | provenance | Repeat | No |
| H-09 [S1] | US-6 | Filter off; real token containing no special chars plus a custom test entry with `"` and `\`; foreground and background prints; open chat, transcript, log, audit, export | `[FILTERED]`; grep on disk finds neither spelling | masking | grep | Yes |
| H-10 [S1] | US-7 AS-3 | Disconnect | No token next command; revoke note | disconnect | — | Yes |
| H-16 [S1] | US-8 AS-5 | Submit a program card with the person's API token via curl | 403; card pending | card gate | Browser submit works | Yes |
| H-17 [S1] | US-1 AS-1 Windows | Windows, test build: start `omnipus credentials set OTHER y` with the in-transaction pause seam; paste a GitHub token in Settings; observe the paste waiting; release within 5 s. Then hold the pause for 10 s and paste again | First: both values present, GitHub connected. Second: paste shows "The credential store is busy. Try again." and nothing changed | store lock (FR-049) | Re-read the file after a restart | Partly |
| H-11 [S2] | US-9 AS-1, AS-2 | macOS sign in | Connected; folder gone | sign-in | H-12 | No |
| H-12 [S2] | US-9 AS-4 | Before/after: `gh auth status` in a normal terminal; keychain item | Unchanged | isolation | Independent terminal | No |
| H-13 [S2] | US-10 | Start/cancel; start/expire; `kill -9` the gateway mid-attempt on macOS and restart | No `gh` with the marker left (`ps eww`); no folder | lifecycle, marker scan | `ps`, `ls` | Partly |
| H-14 [S2] | US-9 Linux | As H-11 on Linux | Same, or marked unsupported with the reason | platform | — | No |
| H-15 [S2] | US-9 AS-5 | Chat card "Sign in" | Card "Stored"; Dev continues once | chat bridge | Reload: no second continuation | Partly |

### Test Implementation Order

| Order | Test Name (placeholder) | Level | Traces to BDD Scenario(s) |
|---|---|---|---|
| 1 | TestCredentialStore_ProgramOwnedGuard [S1] | Unit + Integration | S-127 |
| 2 | TestCredentialStore_Transactions [S1] | Unit + Integration | S-135, S-136, S-146 |
| 3 | TestProgramsREST_PersonSessionOnly [S1] | Integration | S-81, S-134 |
| 4 | TestProgramCredential_RouteSweep [S1] | Integration | S-113, S-128 |
| 5 | TestProgramCard_PersonGate [S1] | Integration | S-112 |
| 6 | TestSecretTools_ProgramOwned [S1] | Unit | S-129 |
| 7 | TestProgramMaskingWriter_Fragmentation [S1] | Unit | S-102, S-103, S-104, S-130 |
| 8 | TestProgramSpawnGuard [S1] | Unit + Integration | S-106 |
| 9 | TestProgramProvenance_JoinAlgebra [S1] | Unit | S-126 |
| 10 | TestProgramProvenance_ProducerGuard [S1] | Unit + Integration | S-111 |
| 11 | TestProgramProvenance_DriftTable [S1] | Unit | S-83 |
| 12 | TestProgramProvenance_RootLineage [S1] | Integration | S-137, S-138, S-143, S-144 |
| 13 | TestProgramCatalog_PresetIsData [S1] | Unit | S-37, S-38, S-93 |
| 14 | TestProgramToken_ValueRules [S1] | Unit | S-02, S-09, S-99 |
| 15 | TestCustomProgram_Validation [S1] | Unit | S-33, S-34, S-35, S-84 |
| 16 | TestProgramCredential_SaveCheckStates [S1] | Integration | S-01, S-03, S-04, S-05, S-06, S-36 |
| 17 | TestProgramCheck_Isolation [S1] | Integration | S-07, S-08 |
| 18 | TestProgramAccountLabel [S1] | Unit | S-105 |
| 19 | TestProgramCheck_Ordering [S1] | Integration | S-90, S-114, S-115, S-116, S-131, S-139 |
| 20 | TestAgentPrograms_ParityRows [S1] | Unit | S-10, S-14, S-15, S-16, S-17 |
| 21 | TestAgentPrograms_ConflictRefusals [S1] | Unit | S-12, S-13, S-30, S-85, S-86 |
| 22 | TestProgramEnv_PerModeAndTool [S1] | Integration | S-11, S-18, S-25, S-28, S-29, S-31, S-89 |
| 23 | TestProgramGitHelper_ResetChain [S1] | Integration | S-19, S-20, S-88 |
| 24 | TestProgramGitVersion_ChildExecutable [S1] | Integration | S-27, S-87 |
| 25 | TestProgramNetwork_AutoAsk [S1] | Integration | S-21, S-22, S-23, S-24, S-26, S-84 |
| 26 | TestProgramTurnEligibility [S1] | Unit | S-39, S-40, S-41, S-42, S-43, S-44, S-45 |
| 27 | TestProgramProvenance_HandOn [S1] | Integration | S-75, S-76, S-77, S-78, S-79, S-80, S-82 |
| 28 | TestProgramProvenance_Updates [S1] | Integration | S-107, S-108, S-109, S-110, S-123, S-124, S-125 |
| 29 | TestProgramMasking_Boundaries [S1] | Integration | S-46, S-47, S-72, S-73, S-74 |
| 30 | TestProgramSentinel_AllSinks [S1] | Integration | S-48, S-50 |
| 31 | TestProgramExportImport [S1] | Integration | S-49 |
| 32 | TestProgramReplaceCheckDisconnect [S1] | Integration | S-51, S-52, S-53 |
| 33 | TestRequestSecret_ProgramRecipient [S1] | Unit | S-54, S-55, S-56, S-99 |
| 34 | TestProgramCard_Contract [S1] | Integration | S-140, S-141 |
| 35 | TestProgramError_PassThrough [S1] | Integration | S-32 |
| 36 | programs.spec.ts [S1] | E2E | S-01, S-12, S-33, S-53, S-81 |
| 37 | connectors-programs.spec.ts [S1] | E2E | S-148 |
| 38 | TestProgramSignIn_HappyPath [S2] | Integration | S-57, S-58, S-62, S-132 |
| 39 | TestProgramSignIn_EndStates [S2] | Integration | S-61, S-63, S-64, S-65, S-67, S-68, S-70, S-100, S-101 |
| 40 | TestProgramSignIn_Supersession [S2] | Integration | S-66, S-91, S-92 |
| 41 | TestProgramSignIn_Verifying [S2] | Integration | S-118, S-119, S-120, S-133 |
| 42 | TestProgramSignIn_CommitDeadline [S2] | Integration | S-147 |
| 43 | TestProgramSignIn_LaunchProtocol [S2] | Integration | S-69, S-96, S-117 |
| 44 | TestProgramSignIn_Cleanup [S2] | Integration | S-95, S-97, S-122 |
| 45 | TestProgramSignIn_ChatCard [S2] | Integration | S-71, S-94, S-98, S-121 |
| 46 | TestProgramSignIn_CardCancel [S2] | Integration | S-142, S-145 |
| 47 | TestProgramSignIn_Availability [S2] | Unit | S-59 |
| 48 | hands-on H-12 [S2] | Hands-on | S-60 |
| 49 | program-sign-in.spec.ts [S2] | E2E | S-57, S-63, S-71 |

### Test Datasets

#### Dataset D-1: Token value rules [S1] (Settings and chat card)

| # | Input | Boundary Type | Expected Output | Traces to |
|---|---|---|---|---|
| 1 | `` | empty | 400 `Enter a token.` | S-09 |
| 2 | `abc1234` (7 B) | min − 1 | 400 too short | S-02 |
| 3 | `abcd1234` (8 B) | min | saved | S-09 |
| 4 | 16,384 B | max | saved | S-09 |
| 5 | 16,385 B | max + 1 | 400 too long | S-09 |
| 6 | `  tok_valid_12345\n` | surrounding whitespace | saved as `tok_valid_12345` | S-09 |
| 7 | `tok_a\ntok_b_123` | inner break | 400 line break | S-09 |
| 8 | `tok\u0000valid123` | NUL | 400 cannot be stored | S-09 |
| 9 | `tökén_ünïcode` | UTF-8 | saved (bytes counted) | S-09 |
| 10 | body 65,537 B | body max + 1 | 413 | S-09 |
| 11 | `abc123` on a chat card | today's card min 4 | 400 too short; pending | S-99 |
| 12 | `expected_generation: 1` while current is 2 | stale | 409 stale generation | S-90 |
| 13 | `q"uo\te_tok_7731` | JSON-special characters | saved | S-102 |

#### Dataset D-2: Custom entry validation (rows 1–27, 31 [S1]; rows 28–30 [S2])

| # | Input | Boundary Type | Expected Output | Traces to |
|---|---|---|---|---|
| 1 | display_name `` | empty | 400 | S-34 |
| 2 | display_name 65 chars | max + 1 | 400 | S-34 |
| 3 | display_name `github` | duplicate | 409 names GitHub | S-34 |
| 4 | program_words `[]` | empty | 400 | S-34 |
| 5 | 9 words | max + 1 | 400 | S-34 |
| 6 | `["/usr/bin/acme"]` | path | 400 | S-34 |
| 7 | `["bash"]` | shell | 400 | S-34 |
| 8 | `["git"]` | shared word | accepted | S-84 |
| 9 | token_variables `[]` | empty | 400 | S-34 |
| 10 | 5 variables | max + 1 | 400 | S-34 |
| 11 | `["acme_token"]` | lowercase | 400 | S-34 |
| 12 | `["PATH"]` | protected | 400 | S-34 |
| 13 | `["OMNIPUS_ACME"]` | reserved prefix | 400 | S-34 |
| 14 | `["GIT_CONFIG_COUNT"]` (and `_KEY_0`, `_VALUE_0`, `GIT_CONFIG_PARAMETERS`) | reserved git | 400 | S-34 |
| 15 | `["GH_TOKEN"]` | shared variable | accepted; conflict only per agent | S-85 |
| 16 | 33 sites | max + 1 | 400 | S-34 |
| 17 | `https://api.acme.example` | scheme | 400 | S-34 |
| 18 | `10.0.0.5` | IP | 400 | S-34 |
| 19 | `*.acme.example` | wildcard | accepted | S-33 |
| 20 | `*` | bare wildcard | 400 | S-34 |
| 21 | sites `[]` | empty | accepted | S-33 |
| 22 | git.host `GitHub.com.` | normalisation | stored `github.com` | S-85 |
| 23 | git.host `github.com:443` | port | 400 | S-34 |
| 24 | git.username `x access` | grammar | 400 | S-34 |
| 25 | identity_check.program_word not among the words | selection | 400 | S-34 |
| 26 | identity_check.args 17 items | max + 1 | 400 | S-34 |
| 27 | identity_check.timeout_seconds 31 | max + 1 | 400 | S-34 |
| 28 [S2] | code_pattern with 2 groups | grammar | 400 | S-34 |
| 29 [S2] | link.scheme `http` | scheme | 400 | S-34 |
| 30 [S2] | expiry_seconds 901 | max + 1 | 400 | S-34 |
| 31 | 33rd entry | collection max + 1 | 409 | S-34 |

#### Dataset D-3: Program-word recognition and site grant (Auto) [S1]

| # | Command | Boundary Type | Expected Output | Traces to |
|---|---|---|---|---|
| 1 | `gh api user` | plain | granted | S-26 |
| 2 | `/usr/local/bin/gh api user` | absolute | granted | S-26 |
| 3 | `env GH_DEBUG=1 gh api user` | wrapper | granted | S-26 |
| 4 | `nohup timeout 30 gh api user` | nested wrappers | granted | S-26 |
| 5 | `X=1 gh api user` | assignment | granted | S-26 |
| 6 | `echo gh` | argument | not granted; unchanged | S-26 |
| 7 | `ghx api` | lookalike | not granted | S-26 |
| 8 | `GH api user` | case | not granted | S-26 |
| 9 | `gh api user \| jq .` | pipeline | granted for `gh` | S-26 |
| 10 | `bash -c "gh api user"` | nested shell | not flagged today; no grant; `gh` fails with its own connection error | S-26 |
| 11 | `gh api user && curl https://example.org` | mixed | one prompt for `example.org` | S-22 |
| 12 | `aws s3 ls` | no entry | unchanged prompt | S-28 |
| 13 | `git ls-remote https://gitlab.com/o/r` with GitHub and GitLab on | shared word | union; no prompt | S-84 |

#### Dataset D-4: Always-on masking [S1]

| # | Input | Boundary Type | Expected Output | Traces to |
|---|---|---|---|---|
| 1 | switch on, min 8, foreground `echo $GH_TOKEN` | default | `[FILTERED]` | S-46 |
| 2 | switch off, same | filter off | `[FILTERED]` | S-46 |
| 3 | min 100000, same | below min | `[FILTERED]` | S-46 |
| 4 | `echo $GH_TOKEN $GH_TOKEN` | repetition | both masked | S-46 |
| 5 | `echo $GH_TOKEN \| base64` | transform | not masked (accepted residual) | S-46 |
| 6 | retired token, read and completion | retired | `[FILTERED]` | S-47 |
| 7 | other secret, switch off | regression | unchanged | S-46 |
| 8 | background success, switch off | async | `[FILTERED]` in feedback, continuation, transcript | S-72 |
| 9 | background error, suppressed feedback | async error | `[FILTERED]` in notice and event | S-73 |
| 10 | token at the 1 MB notifier edge | truncation | no ≥ 8-char part | S-74 |
| 11 | `q"uo\te_tok_7731` via background JSON read | encoding | neither spelling | S-102 |
| 12 | token at the foreground capture cap | capture cap | no ≥ 8-char part | S-103 |
| 13 | token split over two writes with a read between | fragmentation | no ≥ 8-char part | S-104 |
| 14 | token at the background retention cap (`maxOutputBufferSize`) | retention cap | no ≥ 8-char part | S-104 |

#### Dataset D-5: Turn eligibility and provenance [S1]

| # | Input | Boundary Type | Expected Output | Traces to |
|---|---|---|---|---|
| 1 | web turn, web unset | default | given | S-41 |
| 2 | web turn, web off | off | withheld | S-42 |
| 3 | `telegram`, unset | default | withheld | S-39 |
| 4 | `telegram`, allowed | on | given | S-40 |
| 5 | `telegram` allowed + `discord` unset | mixed | withheld, names `discord` | S-43 |
| 6 | helper of a Telegram turn | delegate | withheld | S-75 |
| 7 | peer request from a Telegram turn | peer | withheld | S-76 |
| 8 | task created by a Telegram turn | task | withheld | S-77 |
| 9 | task created in the UI | independent | A-1 | S-77 |
| 10 | heartbeat | independent | A-1 | S-77 |
| 11 | helper resumed after restart | persisted | withheld | S-78 |
| 12 | unreadable parent record | unresolved | withheld | S-79 |
| 13 | 17-hop chain | hop bound | withheld | S-79 |
| 14 | connection id no longer configured | unknown | withheld | S-39 |
| 15 | peer request text "origin: webchat" | model text | ignored | S-82 |
| 16 | web-created task edited from Telegram | instruction update | withheld | S-107 |
| 17 | background completion of a Telegram turn | async continuation | withheld | S-108 |
| 18 | goal follow-up of a Telegram-created goal | goal continuation | withheld | S-109 |
| 19 | existing helper steered from Telegram | steer | withheld | S-110 |
| 20 | message built without `Provenance` | zero value | withheld | S-111 |
| 21 | web off, Telegram on, `{web}` helper steered from Telegram | mixed join | withheld (web) | S-123 |
| 22 | web off, Telegram on, Telegram helper steered from web | mixed join, other order | withheld (web) | S-123 |
| 23 | Dev's own heartbeat-created `{independent}` task edited by Dev from allowed Telegram, independent not allowed | independent join | withheld | S-124 |
| 24 | person UI edit of a `{connector:telegram}` task | preserve | unchanged provenance | S-125 |
| 25 | main chat: Telegram turn, then a person's web turn (web on, Telegram off) | root, connector then web | Telegram turn withheld; web turn given | S-137 |
| 26 | main chat: web turn, then a Telegram turn (web on, Telegram off), across a restart | root, web then connector | web turn given; Telegram turn withheld | S-137 |
| 27 | delegated child launched from a Telegram turn, then steered from the web | lineage | withheld (lineage keeps `connector:telegram`) | S-138 |
| 28 | child lifecycle record without provenance (pre-feature or damaged) | lineage, missing | withheld (`unresolved`) | S-138 |
| 29 | parentless task session of a Telegram-created task, re-entered from the web | lineage, parentless work | withheld | S-143 |
| 30 | same, after restart revival | lineage, persisted | withheld | S-143 |
| 31 | isolated scheduled, verifier, plan owner sessions of Telegram-started work | lineage, work kinds | withheld | S-144 |
| 32 | pre-feature parentless task session; record with missing origin | lineage, ambiguous | withheld (`unresolved`) | S-144 |
| 33 | pre-feature `main` session, web turn | root | given | S-144 |

#### Dataset D-6: Credential generation and check interleavings (rows 1, 2, 6–10, 12–14, 18 [S1]; rows 3–5, 11, 15–17, 19–22 [S2])

| # | Sequence (barriers, not timing) | Expected Output | Traces to |
|---|---|---|---|
| 1 | check(A) started → save B → check(A) finishes | A `superseded`; B decides | S-90 |
| 2 | save A ‖ save B | lock orders; generations 1 and 2; final = later commit | S-90 |
| 3 [S2] | sign-in waiting → paste | attempt `cancelled`; paste stays | S-91 |
| 4 [S2] | sign-in verifying → disconnect → release | attempt `cancelled`; `not_connected` | S-92 |
| 5 [S2] | attempt 1 exits 0 → attempt 2 starts → attempt 1 releases | attempt 1 `cancelled` | S-66 |
| 6 | **check-relevant** entry edit (`identity_check` or `token_variables`) during its check | old result discarded; new check reserved and started automatically | S-116 |
| 7 | PUT credential response lost | UI re-reads; no resend | S-01 |
| 8 | check A, check B same generation; B finishes, then A | B's result stands | S-114 |
| 9 | restart with persisted `checking` | fresh check at start | S-115 |
| 10 | display-name or sites edit during check | `check_revision` unchanged; held result publishes | S-131 |
| 11 [S2] | paste after the sign-in check, before verified commit | precondition fails; attempt `cancelled`; paste stays | S-132 |
| 12 | A commits gen 1, pauses before launch → B commits gen 2 and launches → A's launch resumes | A's launch exits without starting or stopping anything; gen 2's check reaches a terminal state | S-139 |
| 13 | card submit gives gen N → check still running → Settings paste gives gen N+1 | card shows "Stored — replaced by a newer token"; never N+1's account | S-141 |
| 14 | two `Store` objects: B loads, A commits a program credential, B saves an unrelated key | A's credential, receipt and metadata survive | S-135 |
| 15 [S2] | card-bound attempt started → card cancelled | attempt `cancelled`; no credential, no receipt | S-142 |
| 16 [S2] | card-bound attempt at the commit barrier → card cancelled → commit resumes | commit refuses (card no longer pending); previous credential kept | S-142 |
| 17 [S2] | card-bound attempt commits → card cancel | cancel gets 409 `submitted`; one continuation | S-142 |
| 18 | store held by another writer for 6 s → paste | 503 busy after 5 s; nothing written later | S-146 |
| 19 [S2] | Stop while waiting → attempt commits | credential stored; card `submitted` with veto; no continuation | S-98 |
| 20 [S2] | Stop → commit → crash before card settlement → restart | card `submitted` with veto; no continuation | S-145 |
| 21 [S2] | store held across t = 60 s → verified commit | `failed` at 60 s; nothing written | S-147 |
| 22 [S2] | admitted and claimed at 58 s, write ends at 62 s; cancel at 59 s | `connected`; cancel 409 | S-147 |

#### Dataset D-7: Sign-in output parsing [S2]

| # | Output | Expected | Traces to |
|---|---|---|---|
| 1 | valid code + `https://github.com/login/device` | `waiting_for_person` | S-57 |
| 2 | host `github.com.evil.example` | `unsupported` | S-61 |
| 3 | `http://github.com/login/device` | `unsupported` | S-61 |
| 4 | path `/settings/tokens` | `unsupported` | S-61 |
| 5 | code `ABCD1234` | `unsupported` | S-101 |
| 6 | nothing for 16 s | `failed` | S-100 |
| 7 | 70,000 bytes | `unsupported` | S-101 |
| 8 | denied marker + exit 1 | `denied` | S-65 |

#### Dataset D-8: Callers of person-only endpoints (rows 1–8, 10–14 [S1]; row 9 [S2])

| # | Caller | Request | Expected (status and source) | Body read | Traces to |
|---|---|---|---|---|---|
| 1 | browser session cookie + CSRF token | `PUT /programs/github/credential` | 200 processed (positive control) | yes (processed) | S-81 |
| 2 | CLI token | same | 403 `this action requires a signed-in person` (RequirePersonSession); fake `gh` not started | no (refused before the body) | S-81 |
| 3 | person's own API bearer token | same | 403 same text (requireSessionCookieAuth) | no | S-81 |
| 4 | agent/tool bearer token | `PUT /agents/dev` with `programs` | 403 same text; unchanged | yes: bounded decode, no mutation (shared route, FR-028) | S-81 |
| 5 | development bypass mode | `POST /programs/github/check` | 403 same text; nothing started | no | S-81 |
| 6 | no identity | `GET /programs` | 401 (withAuth's existing text) | no | S-81 |
| 7 | legacy `OMNIPUS_BEARER_TOKEN` | `PUT /channels/telegram/programs` | 403 same text | no | S-81 |
| 8 | browser cookie **without** CSRF token | `PUT /programs/settings` | refused by the existing CSRF middleware with its own status and text | no (existing middleware) | S-81 |
| 9 [S2] | CLI token | `POST /programs/github/sign-in` | 403; no process | no | S-81 |
| 10 | person's own API bearer token | `POST /secret-requests/{id}/submit`, pending **program** card | 403; card pending; body not read | no | S-112 |
| 11 | browser session cookie + CSRF token | same | 200 submitted (positive control) | yes (processed) | S-112 |
| 12 | person's own API bearer token | `PUT /agents/dev` **with** `programs` | 403 after bounded decode, before any mutation | yes: bounded decode, no mutation | S-134 |
| 13 | person's own API bearer token | `PUT /agents/dev` **without** `programs` | 200 as today (regression) | yes (processed) | S-134 |
| 14 | browser session + CSRF + re-auth token | `PUT /api/v1/credentials` with a program-owned key | 409 program-owned (store guard) | yes: decoded, store unchanged (409 from the store guard) | S-128 |

#### Dataset D-9: Output interrupted inside a token [S1] (sentinel L = 21 bytes)

| # | n (bytes written) | Stream | Interruption | Sink | Expected | Traces to |
|---|---|---|---|---|---|---|
| 1 | 7 | stdout | process exits 0 | foreground result | 7 bytes may appear; no ≥ 8-byte fragment | S-130 |
| 2 | 8 | stdout | process exits 0 | foreground result | `[FILTERED]`; no ≥ 8-byte fragment | S-130 |
| 3 | 10 | stderr | killed (SIGKILL) | foreground result | `[FILTERED]` | S-130 |
| 4 | 10 | stdout | foreground timeout | "Partial output before timeout" text | `[FILTERED]` | S-130 |
| 5 | 20 (L − 1) | stdout | process exits 1 | background completion (published, continuation, transcript) | `[FILTERED]` | S-130 |
| 6 | 20 (L − 1) | stderr | killed by `bash` session kill | background read after kill | `[FILTERED]` | S-130 |
| 7 | 10 | stdout | pipe read error injected | background read | `[FILTERED]` | S-130 |
| 8 | 10 (bytes 5–14, a middle fragment) | stdout | exits 0 | foreground result | `[FILTERED]` | S-130 |

### Regression Test Requirements

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|---|---|---|---|
| Private variables with no programs | private env tests | Yes | |
| Output of children with no program variables | shell output tests | Yes: byte-identical output, no hold-back delay | I-1 must not touch other children |
| Auto prompts for unprofiled programs | `enforceNetworkPreflight` tests | Yes: D-3 rows 6–8, 10, 12 | |
| deny > ask > allow | shell-rule tests | Yes: S-23 | |
| General filter for other secrets | filter tests | Yes: D-4 row 7 | |
| Async delivery without program tokens | notifier tests | Yes | |
| `TurnOrigin.Label` | turn origin tests | Yes | first person still named |
| Attendance (#1263) | attendance tests | Yes | unaffected |
| Non-program card submit with a person bearer | secret request tests | Yes: S-112 last clause | policy unchanged |
| request_secret forms; 4-byte rule for other kinds | `ResolveRecipient`, `Submit` tests | Yes | |
| Task update authority | task update tests | Yes: unchanged authority; provenance added | |
| Agent parity tables | `t8_parity_tables_test.go` | Extended | |
| Export of executor env names | omnpkg tests | Yes | |

---

## Functional Requirements

**S1**

- **FR-001** [S1]: System MUST provide a program catalog of built-in presets and custom entries. GitHub and Fly ship as presets, each a data file; account checks (and S2 recipes) run from data with no per-preset code.
- **FR-002** [S1]: System MUST show every entry on Settings → Programs with words, state and account label, with no configuration-file edit needed.
- **FR-003** [S1]: System MUST accept a token through a dialog that reuses the secure card's field and promise text, with program rules: 8–16,384 bytes, surrounding whitespace trimmed, inner line breaks and NUL refused.
- **FR-004** [S1]: System MUST, after each publication, report `checking` and run the current check. It MUST report `connected` only if the check succeeded, FR-046 holds, and generation, `check_revision` and check id are current; otherwise `saved_not_checked` or `check_failed` with the reason. This applies to **unverified** publications only; a verified S2 publication follows FR-034 and runs no further check.
- **FR-005** [S1]: System MUST run the account check as a gateway-owned process:
  - executable = the recipe's `program_word` on the gateway PATH, checked with `CheckProgramPath`;
  - literal args; environment = `PATH`, a fresh temporary `HOME` and the entry's token variables, attached through `programs.Attach` (I-1);
  - stdin closed; timeout per recipe; output capped at 4,096 bytes **after** masking;
  - process group stopped at timeout or supersession; temporary folder deleted.
- **FR-006** [S1]: System MUST keep one credential per entry per installation. A failed save leaves the previous credential and generation in use.
- **FR-007** [S1]: System MUST store a per-agent `programs` list, empty at creation. It is set through the agent profile and `PUT /agents/{id}` (person-session only) or `set_agent_permissions.programs` (self-change refused), refused by `update_agent`, and readable by `get_agent`.
- **FR-008** [S1]: System MUST refuse, naming the conflict:
  - switch-on when the agent has a private variable with one of the entry's names;
  - such a private variable while the entry is on;
  - switch-on or edit that makes two entries on for one agent share a token variable.
- **FR-009** [S1]: System MUST provide a per-connection setting (default off) and a web-chat setting (default on), each with its own person-only writer that `set_config` cannot change. The per-connection setting MUST be shown and set on the Connectors page in the channel's panel, which states the workspace and agent the channel is bound to, or that it is unbound and the switch has no effect until it is bound. Rebinding a channel MUST turn its switch off in the same write (founder S-Q2 and correction, 22 Nov).
- **FR-010** [S1]: System MUST decide eligibility from the turn's provenance as defined in "Program provenance". `unresolved` → withheld. Otherwise eligible only if **every** origin in the set is allowed now: `web` by the web-chat setting, `independent` by the web-chat setting (founder Q1 = A), `connector:<id>` by that connection's setting. Joining provenances is a union and MUST NOT drop or convert origins (monotone restriction, I-2).
- **FR-011** [S1]: System MUST, for an eligible turn, add every token variable of every switched-on entry that has a saved credential (any check state) to every `bash` child (foreground, background, God Mode) and `environment_setup` child of that agent, only through `programs.Attach`.
- **FR-012** [S1]: System MUST add a value-free notice when a switched-on entry was withheld, naming the connection, the web chat or the unresolved origin.
- **FR-013** [S1]: System MUST, for each switched-on entry with a git host, add environment-only git configuration:
  - an empty `credential.https://<host>.helper`, then a fixed get-only helper that reads the token from the entry's first variable and ignores `store` and `erase`;
  - no token in configuration text, files or command lines;
  - the `GIT_CONFIG_*` names and `GIT_CONFIG_PARAMETERS` reserved.
- **FR-014** [S1]: System MUST decide git support for the git the command will run (explicit path among its program words, else the child PATH's `git`). It probes `--version` as an ordinary child of the same turn and sandbox, cached per (path, size, mtime). Older than 2.31 → no managed entries plus a notice; no git → entries added.
- **FR-015** [S1]: System MUST, in Auto, treat a segment whose visible program word belongs to switched-on eligible entries as a known-host need, and grant the union of their sites for that run without a prompt. Other needs are classified as today, and deny rules come first.
- **FR-016** [S1]: System MUST, in Ask, grant that union for a permitted command's run without another prompt.
- **FR-017** [S1]: System MUST, in God Mode, attach variables and git entries and create no site grant.
- **FR-018** [S1]: System MUST match program words by the exact, case-sensitive last path part of each word the shell runs as a program, as found by `commandWordRoles`. Programs with no entry behave as before.
- **FR-019** [S1]: System MUST let a person create, edit and delete custom entries per D-2. Words and variables may be shared. Deleting an entry that is on is refused; presets are read-only.
- **FR-020** [S1]: System MUST mask current and retired program token values at the source per I-1:
  - a masking writer on every output stream of every child that `programs.Attach` serves, before capture caps, retention, draining reads, encoding and truncation;
  - replacing every maximal run covered by substrings of 8 or more bytes of any current or retired token with one `[FILTERED]`, holding back at most 7 bytes, and on any close (EOF, exit, kill, timeout, cancellation, read error) releasing only those ≤ 7 bytes, never a longer held prefix;
  - B1–B6 kept as defence in depth with the original, Latin-1-escaped and JSON-escaped spellings;
  - all of this regardless of the general switch and `filter_min_length`.
- **FR-021** [S1]: System MUST write value-free audit rows for connect, check (including `superseded`), replace, disconnect, switch change, setting changes, custom entry change, and refused program-card submissions, and one value-free row with the outcome of each finished check (connected, failed, timed out).
  - *Note (ruling-check-delta.md item 5, 2026-11-25):* each `program.check_result` row carries one outcome and one reason class from this set: `connected` with `account_named`; `timed_out` with `timeout`; `failed` with one of `store_unreadable`, `program_not_found`, `program_location_refused`, `not_started`, `run_error`, `exit_status`, `label_carries_token`, `no_account_name`. A superseded or replaced check writes one `program.check_superseded` row and no `program.check_result` row.
- **FR-022** [S1]: System MUST list switched-on entries under `needs_secrets` (kind `program`) on export; an imported agent's `programs` is empty.
- **FR-023** [S1]: System MUST let a person disconnect (local copy removed, switches kept, revoke-at-vendor note), replace and re-check.
- **FR-024** [S1]: System MUST, at run time, set neither value when two sources set one variable, and add no managed helper when two switched-on entries supply one git host. Each case adds its notice, and the result does not depend on entry order.
- **FR-025** [S1]: `request_secret` MUST accept a `program` form in web-chat turns, with the existing refusals elsewhere. The card says the token is shared. Submitting changes no switch.
- **FR-026** [S1]: System MUST pass a program's own output and exit status through unchanged except for masking.
- **FR-027** [S1]: System MUST, when the store is unavailable, run commands without program tokens and add the notice.
- **FR-028** [S1]: System MUST apply the person-session gate (`RequirePersonSession` + `requireSessionCookieAuth`, behind CSRF) to:
  - every Programs endpoint;
  - `/channels/{id}/programs`;
  - changes of `programs` through `PUT /agents/{id}`;
  - **submissions of a secret request whose stored recipient kind is `program`**.

  Ordering differs by route (R3-M6):
  - **Dedicated Programs routes, `/channels/{id}/programs`, and program-card submissions**: refuse **before reading the body**. For the submit route the handler looks up the stored request and owner first; when the stored kind is `program` and the caller is not a person browser session, it answers 403 without reading the body. Other card kinds are unchanged.
  - **`PUT /agents/{id}`** (shared with ordinary agent fields): the handler keeps its existing bounded body read and decode. If the body contains `programs` and the caller is not a person browser session, it answers 403 **before any mutation, config write or process start**. Requests without `programs` are unchanged.

  Contracts first.
- **FR-041** [S1]: System MUST carry provenance per I-2 and the "Program provenance" tables:
  - required on turn-feeding messages (`bus.InboundMessage`, steer messages, peer request records, `AsyncNotifyEvent`, goal follow-ups) and on durable records (lifecycle, task, plan, schedule, goal), with zero value = `unresolved`;
  - joined into a record in the same write as any agent change to its instructions (lineage records; root records follow FR-050);
  - restored on every rebuild and restart; recording every consumed connection;
  - more than 16 hops or unreadable → `unresolved`.
- **FR-042** [S1]: System MUST refuse switch-on or edits that give two entries on for one agent the same canonical git host.
- **FR-043** [S1]: System MUST keep a per-entry generation, changed under one per-entry lock by every publication and disconnect. `PUT …/credential` MAY carry `expected_generation`, and MUST answer 409 when it is stale.
- **FR-044** [S1]: A chat program card submission MUST go through `programs.Publish` with the same value rules, generation, check and audit as Settings.
- **FR-045** [S1]: The repository MUST contain the guard tests of the invariants, all running in the normal test suite, and each MUST fail on its planted violation:
  - I-1: spawn guard S-106 (per-slice inventory);
  - I-2: join-algebra guard S-126, producer guard S-111, origin-kind table S-83;
  - I-3: store method-classification guard S-127 (including (d), every mutation inside `s.transact`) and route sweep S-113.
- **FR-046** [S1]: System MUST derive the account label only from the masked first non-empty stdout line (1–128 printable characters). If that line contains `[FILTERED]`, or is a substring of 8 or more characters of any current or retired program token, the check ends `check_failed` with the label-carries-token message and no label.
- **FR-047** [S1]: System MUST keep at most one current check reservation per entry (`check_id`, generation, `check_revision`). A reservation MUST be made under the entry lock in the same step that commits the change needing it, and making one supersedes the running check (process group stopped, result discarded). A launch MUST start its process only if its reservation is still current and MUST never stop or supersede anything (R4-M3, S-139). These make a reservation:
  - an unverified publication;
  - `POST …/check`;
  - any edit that increments `check_revision` (changes to `identity_check` or `token_variables`, in any state including `checking`);
  - gateway start for a persisted `checking`.

  Other entry edits (display name, words, sites, git) MUST NOT change `check_revision` or invalidate a running check. Only the current check id, with an unchanged generation and `check_revision`, may publish. Every check ends within its timeout + 5 s in a terminal state.
- **FR-048** [S1]: `credentials.Store` MUST refuse, with `ErrProgramOwned` and no file change, any mutation of a program-owned entry through every exported mutating method except the program-scoped `CommitProgramCredential` and `RemoveProgramCredential`. A program-owned entry is a card secret with recipient kind `program`, or a name equal to one. The exceptions:
  - `Rotate` and `RotateWithPassphrase` may re-encrypt without changing values;
  - receipt pruning may remove receipts only.

  Existing callers MUST surface the refusal as the program-owned message (REST 409, tool error). The boot orphan sweep MUST skip program-owned names. The program-scoped methods MUST be called only from `pkg/programs`.
- **FR-049** [S1]: Every mutating `credentials.Store` method MUST run as one transaction `s.transact(fn)`: process-wide per-path mutex shared by all `Store` objects, then the sidecar `credentials.json.lock` lock, then a re-read of the file, the mutation, the atomic write, unlock. On Windows the sidecar lock MUST be a real exclusive lock (`LockFileEx`). Acquisition of both layers MUST be bounded and cancellable (non-blocking cross-process attempts retried every 10 ms): 5 s for ordinary writes, the verifying deadline for a verified commit. A failed admission MUST return `ErrStoreBusy` with no file change. The write MUST be synchronous inside the held transaction, with its outcome read back before unlock; no write may continue after its caller has been told it failed (R4-M1, R5-M3, S-127(d), S-135, S-136, S-146, S-147, H-17).
- **FR-050** [S1]: Every lifecycle record MUST carry `provenance_mode` (`root` or `lineage`). The mode MUST follow the classification table in "Root versus lineage": `root` only for an identified standing conversation (origin `chat`, `channel` or `main`, no parent, no task evidence, not plan-owned); every work session, parentless or not, and every unknown or ambiguous identity is `lineage`. A `root` stores no provenance and each of its turns is the join of the inputs it consumes. A `lineage` record requires stored provenance, stamped from its launching turn or work record; missing or unreadable is `unresolved`, and later inputs join it, never replace it. Records written before this feature MUST be classified by the same table on load (R4-M2, R5-M1, S-83, S-137, S-138, S-143, S-144).
- **FR-051** [S1]: A program card MUST carry `program_id` in its recipient display and, once submitted, `program_generation` in its request state, both restored after restart from the stored request and `CardReceipt`. The continuation MUST be admitted at once and MUST say the token is stored for that program and its account check is running, never a result it does not have. The card MUST show a check result only while the entry's generation equals its `program_generation`; otherwise "Stored — replaced by a newer token" (R4-M4, S-54, S-140, S-141). A card-bound S2 sign-in continuation MAY state the verified account.

**S2**

- **FR-029** [S2]: System MUST offer "Sign in" only when the recipe lists this platform and its `program_word` passes `CheckProgramPath`; otherwise `sign_in.available` is false with the reason.
- **FR-030** [S2]: System MUST run the login as a gateway-owned process:
  - follows the crash-safe launch protocol;
  - stdin closed; folder under `$OMNIPUS_HOME/system/program-sign-in/<attempt-id>/`;
  - environment from scratch: `PATH`, `HOME` = folder, `config_dir_variable`, `extra_env`, the marker; no token variables or gateway credential variables;
  - output masked and capped at 65,536 bytes.
- **FR-031** [S2]: System MUST show only a link whose parsed scheme, host and path prefix match the recipe, and a code captured by `code_pattern`.
- **FR-032** [S2]: An attempt MUST follow the transition table exactly, including cancellation from every active state, expiry only while waiting, and the bounds: start 15 s, version probe 10 s, read-back 15 s, verifying 60 s, expiry ≤ 900 s.
- **FR-033** [S2]: System MUST keep one current attempt per entry. A newer attempt, paste, disconnect, person cancel or shutdown ends an active attempt `cancelled` (the single supersession outcome). A superseded attempt reads 200 `cancelled` for 10 minutes.
- **FR-034** [S2]: System MUST verify and publish in this order:
  1. read-back (≤ 15 s);
  2. register masking for the candidate;
  3. **one** account check of the candidate;
  4. **verified** `programs.Publish`. Under the entry lock it enters the store transaction (waiting at most until the verifying deadline), checks the preconditions **inside the transaction** (attempt current and inside its 60 s verifying deadline, generation and `check_revision` unchanged, card admits the commit when card-bound), claims the attempt `verifying → committing` (the single winner against the deadline timer, cancel and supersession), then writes credential, label, `state: connected`, new generation and, when card-bound, the `CardReceipt` in one synchronous store write (the **commit point**), read back before unlock, with no further check (R5-M3, S-146, S-147).

  Read-back output is never shown or logged. A failure **before** the commit point ends `failed` (or `cancelled` when a precondition shows supersession) with the previous credential kept. After the commit point the new credential is in use and the outcome is `connected`.
- **FR-035** [S2]: System MUST stop the process group and delete the folder on every end state and at shutdown, and run the start-up sweep before serving requests. `cleanup` and `sign_in_warning` report unconfirmed cleanup until a sweep succeeds. "No copy remains" is claimed only after confirmed exit and removal.
- **FR-036** [S2]: System MUST NOT change the person's own login state.
- **FR-037** [S2]: System MUST accept a `sign_in_recipe` on custom entries, set only by the person, with a confirm step stating it runs outside the sandbox (founder Q3 = A, decided 22 Nov).
- **FR-038** [S2]: System MUST NOT show, return, log or audit raw login output.
- **FR-039** [S2]: Each recipe MUST list its platforms; Windows is unsupported.
- **FR-040** [S2]: The chat program card MUST offer "Sign in" when FR-029 holds:
  - the attempt is bound to the card's request (`secret_request_id`: same person, program and pending request);
  - on `connected`, the credential and the request's `CardReceipt` are written in one store write (the verified commit, FR-034). The request is then marked `submitted`, and the continuation is admitted through the existing delivery path (`ensureDeliveryLocked` → `ClaimResume`), with no continuation after Stop;
  - the credential and receipt are committed **exactly once**. The continuation runs **at most once**. After a crash, the existing receipt recovery restores `submitted` and re-dispatches if no resume entry exists. A crash after the resume entry is written and before the turn runs leaves the conversation in the existing restart-stop state with no automatic continuation (R3-M5, S-121);
  - other attempt end states leave the card's own state unchanged: a pending card stays pending, and a card that was cancelled, stopped or refused meanwhile keeps that state;
  - cancelling the card MUST cancel its bound attempt (R4-M5);
  - the verified commit of a card-bound attempt MUST take locks in the order gateway config lock → secret-request registry lock → program entry lock → store transaction, then decide from the card's state (R5-M2):
    - `pending` and bound to this attempt → write credential and receipt, mark the card `submitted`, admit the continuation;
    - `stopped` **by its conversation's Stop** (reason `session_stopped`, set by `Registry.Stop`) while this attempt was already bound, and the session still exists → write credential and a receipt marked `resume_vetoed`, mark the card `submitted` with the existing `ResumeStopped` veto, no continuation (S-98);
    - any other state (`cancelled`, `refused`, `interrupted`, `stopped` because the session was deleted, missing request, or bound to another attempt) → write nothing; the attempt ends `cancelled`.
    No lock is held over the vendor login or the account check. A cancel after the commit gets the existing 409 `submitted` (S-142);
  - recovery: a `stopped` card whose receipt is marked `resume_vetoed` is restored to `submitted` with `ResumeStopped` and is never dispatched. A `stopped` card with an unmarked receipt (impossible by construction) stays `stopped`, is audited as an inconsistency and is never dispatched (S-145). The existing recovery of pending cards is unchanged.

## Success Criteria

- **SC-001** [S1]: H-02 passes on macOS and Linux with 0 prompts.
- **SC-002** [S1]: H-03 passes, and its validator re-run with GitHub off fails with 403.
- **SC-003** [S1]: The sentinel and its JSON spelling appear 0 times in every sink, including account labels, with the filter off, across D-4 rows 1–4 and 6–14 and every D-9 row (no fragment of 8 or more bytes) (S-48, S-72–S-74, S-102–S-104, S-130, H-09).
- **SC-004** [S1]: With Telegram off, 0 of the nine handoff paths (direct, helper, peer, new task, edited task, background completion, goal follow-up, steer, restart) receive a token; with web chat off and Telegram on, 0 of the mixed web/Telegram handoffs in either order receive a token (S-123).
- **SC-005** [S1]: The 13 [S1] rows of D-8 give exactly their expected answers; the fake program starts 0 times for refused rows. [S2]: row 9 likewise.
- **SC-006** [S1]: Every [S1] row of D-1 to D-6, D-8 and D-9 passes RED then GREEN; one data-only preset is listed and its check runs with 0 Go changes (S-37, S-93). [S2]: every [S2] row of D-2, D-6, D-7, D-8 passes RED then GREEN.
- **SC-007** [S1]: The five guard tests (S-106, S-111 with S-83, S-113, S-126, S-127) pass on the tree **and** each fails on its planted test-only violation; after the S-113 sweep the program credential's decrypted value and generation are unchanged in both edition modes.
- **SC-010** [S1]: S-135, S-136 and S-146 pass, and S-135 fails on its read-outside-the-lock mutant; H-17 passes on Windows or is recorded as a release blocker for Windows.
- **SC-008** [S2]: H-11, H-12, H-13 and H-15 pass on macOS.
- **SC-009** [S2]: Each of the six end states is reached in tests with 0 leftover processes carrying the marker and 0 leftover folders; at each of the three launch barriers (S-117) and three card barriers (S-121) the stated outcome holds (one continuation at barriers a and b, zero at c, never more than one); the verified publication runs exactly 1 account check (S-132).

---

## Reachability

- **Tool registration** [S1]: no new tool.
  - `set_agent_permissions` gains `programs`. It is already `ask` (`pkg/config/defaults.go`), granted to Omnipus (`pkg/coreagent/role_policies_adr090.go`), and on the Auto ask-list (`pkg/tools/auto_approve.go`).
  - `request_secret` gains `program` (already `allow`, `AutoRuns`).
  - Description texts change, and `prometheus-prompt-engineer` reviews the wording.
- **Tool registration** [S2]: none. The chat card's own "Sign in" replaces the ADR's `request_program_login`.
- **Screens:**
  - Settings → Programs (new tab in `src/routes/_app/settings.tsx::SETTINGS_TABS`);
  - agent profile → Programs (`src/components/agents/AgentProfile.tsx`);
  - channel panel → Programs (`src/components/skills/ChannelConfigPanel.tsx`);
  - chat secure card;
  - S2 sign-in dialog.
- **Test plan execution:**
  - RED (qa-lead), GREEN (backend-lead, frontend-lead), CHECK (a different qa-lead instance);
  - at the cut: the 5-reviewer gate plus hands-on H-00 to H-10 and H-16 (S1), or H-11 to H-15 (S2), on the exact cut commit;
  - after landing: Test Lab.

## User-facing documentation

| TODO | UI / behaviour change | User-facing page | Matching update |
|---|---|---|---|
| DOC-001 [S1] | Settings → Programs: connect, custom entries, replace, check, disconnect; cards must be filled in the browser | `docs/settings.md` | New "Programs" section |
| DOC-002 [S1] | Credentials for the agent's commands; git per site and version; sites per mode; scripts; another git by absolute path not covered | `docs/tools.md` | "Connected programs" |
| DOC-003 [S1] | Program tokens always hidden at the source, including parts of 8 or more characters; limits (transformed values, files, fragments under 8 characters, rare false matches for low-variety tokens); program tokens cannot be changed through the general credentials page or tools | `docs/security.md` | Masking section |
| DOC-004 [S1] | Per-agent switch; conflicts; import arrives off | `docs/agents.md` | Programs section |
| DOC-005 [S1] | Channel switch on the Connectors page (per bound channel; unbound has no effect; rebinding turns it off) and the web-chat setting; restriction follows helpers, other agents, tasks (also edited), continuations, restarts; independent work; history is not tracked | `docs/connectors.md`, `docs/settings.md` | Programs setting |
| DOC-006 [S2] | Sign in from Settings or a chat card; platforms; own login untouched; cleanup warning | `docs/settings.md` | Sign-in subsection |

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) | Documentation TODO(s) |
|---|---|---|---|---|
| FR-001 | US-4 | S-37, S-38, S-93 | TestProgramCatalog_PresetIsData | DOC-001 |
| FR-002 | US-1 | S-01, S-37 | programs.spec.ts | DOC-001 |
| FR-003 | US-1 | S-02, S-09 | TestProgramToken_ValueRules | DOC-001 |
| FR-004 | US-1 | S-01, S-03, S-04, S-06, S-36, S-90 | TestProgramCredential_SaveCheckStates | DOC-001 |
| FR-005 | US-1 | S-06, S-07, S-08 | TestProgramCheck_Isolation | DOC-003 |
| FR-006 | US-1, US-7 | S-05, S-51 | TestProgramCredential_SaveCheckStates | DOC-001 |
| FR-007 | US-2 | S-10, S-11, S-14, S-15, S-16, S-17 | TestAgentPrograms_ParityRows | DOC-004 |
| FR-008 | US-2 | S-12, S-13 | TestAgentPrograms_ConflictRefusals | DOC-004 |
| FR-009 | US-5 | S-39, S-40, S-41, S-42, S-45, S-148 | TestProgramTurnEligibility, connectors-programs.spec.ts | DOC-005 |
| FR-010 | US-5 | S-39, S-40, S-41, S-42, S-43, S-44, S-75, S-76, S-77, S-79, S-80, S-123, S-124, S-126 | TestProgramTurnEligibility, TestProgramProvenance_HandOn, TestProgramProvenance_JoinAlgebra | DOC-005 |
| FR-011 | US-3 | S-11, S-18, S-25, S-28, S-29, S-36, S-89 | TestProgramEnv_PerModeAndTool | DOC-002 |
| FR-012 | US-5 | S-39, S-42, S-43, S-79 | TestProgramTurnEligibility | DOC-005 |
| FR-013 | US-3 | S-19, S-20, S-88 | TestProgramGitHelper_ResetChain | DOC-002 |
| FR-014 | US-3 | S-27, S-87 | TestProgramGitVersion_ChildExecutable | DOC-002 |
| FR-015 | US-3 | S-21, S-22, S-23, S-26, S-84 | TestProgramNetwork_AutoAsk | DOC-002 |
| FR-016 | US-3 | S-24 | TestProgramNetwork_AutoAsk | DOC-002 |
| FR-017 | US-3 | S-25 | TestProgramEnv_PerModeAndTool | DOC-002, DOC-003 |
| FR-018 | US-3 | S-26, S-28, S-89 | TestProgramNetwork_AutoAsk | DOC-002 |
| FR-019 | US-4 | S-33, S-34, S-35, S-38, S-84 | TestCustomProgram_Validation | DOC-001 |
| FR-020 | US-6 | S-46, S-47, S-48, S-72, S-73, S-74, S-102, S-103, S-104, S-130 | TestProgramMasking_Boundaries, TestProgramMaskingWriter_Fragmentation | DOC-003 |
| FR-021 | US-6 | S-50, S-90, S-112 | TestProgramSentinel_AllSinks, TestProgramCard_PersonGate | DOC-003 |
| FR-022 | US-6 | S-49 | TestProgramExportImport | DOC-004 |
| FR-023 | US-7 | S-51, S-52, S-53 | TestProgramReplaceCheckDisconnect | DOC-001 |
| FR-024 | US-3 | S-30, S-86 | TestAgentPrograms_ConflictRefusals | DOC-004 |
| FR-025 | US-8 | S-54, S-55, S-56 | TestRequestSecret_ProgramRecipient | DOC-001 |
| FR-026 | US-3 | S-32 | TestProgramError_PassThrough | DOC-002 |
| FR-027 | US-3 | S-31 | TestProgramEnv_PerModeAndTool | DOC-003 |
| FR-028 | US-2, US-8 | S-81, S-112, S-134 | TestProgramsREST_PersonSessionOnly, TestProgramCard_PersonGate | DOC-001 |
| FR-041 | US-5 | S-75, S-76, S-77, S-78, S-79, S-80, S-82, S-107, S-108, S-109, S-110, S-123, S-124, S-125 | TestProgramProvenance_HandOn, TestProgramProvenance_Updates | DOC-005 |
| FR-042 | US-2, US-4 | S-85, S-86 | TestAgentPrograms_ConflictRefusals | DOC-004 |
| FR-043 | US-1 | S-90, S-114 | TestProgramCheck_Ordering | DOC-001 |
| FR-044 | US-8 | S-54, S-99 | TestRequestSecret_ProgramRecipient | DOC-001 |
| FR-045 | US-5, US-6, US-8 | S-83, S-106, S-111, S-113, S-126, S-127 | TestProgramSpawnGuard, TestProgramProvenance_ProducerGuard, TestProgramProvenance_DriftTable, TestProgramProvenance_JoinAlgebra, TestProgramCredential_RouteSweep, TestCredentialStore_ProgramOwnedGuard | No user-visible change (guard tests) |
| FR-046 | US-1 | S-105 | TestProgramAccountLabel | DOC-001 |
| FR-047 | US-1 | S-90, S-114, S-115, S-116, S-131, S-139 | TestProgramCheck_Ordering | DOC-001 |
| FR-048 | US-8 | S-127, S-128, S-129 | TestCredentialStore_ProgramOwnedGuard, TestProgramCredential_RouteSweep, TestSecretTools_ProgramOwned | DOC-001 |
| FR-049 | US-1, US-7, US-10 | S-127, S-135, S-136, S-146, S-147 | TestCredentialStore_ProgramOwnedGuard, TestCredentialStore_Transactions, TestProgramSignIn_CommitDeadline | No user-visible change (store safety); H-17 |
| FR-050 | US-5 | S-83, S-137, S-138, S-143, S-144 | TestProgramProvenance_DriftTable, TestProgramProvenance_RootLineage | DOC-005 |
| FR-051 | US-8 | S-54, S-140, S-141 | TestRequestSecret_ProgramRecipient, TestProgramCard_Contract | DOC-001 |
| FR-029 | US-9 | S-59 | TestProgramSignIn_Availability | DOC-006 |
| FR-030 | US-9, US-10 | S-62, S-95, S-117 | TestProgramSignIn_HappyPath, TestProgramSignIn_LaunchProtocol, TestProgramSignIn_Cleanup | DOC-006 |
| FR-031 | US-9 | S-57, S-61, S-101 | TestProgramSignIn_EndStates | DOC-006 |
| FR-032 | US-10 | S-63, S-64, S-65, S-100, S-118, S-119, S-120, S-133, S-147 | TestProgramSignIn_EndStates, TestProgramSignIn_Verifying, TestProgramSignIn_CommitDeadline | DOC-006 |
| FR-033 | US-10 | S-66, S-91, S-92 | TestProgramSignIn_Supersession | DOC-006 |
| FR-034 | US-9, US-10 | S-58, S-67, S-68, S-132 | TestProgramSignIn_HappyPath, TestProgramSignIn_EndStates | DOC-006 |
| FR-035 | US-10 | S-63, S-69, S-96, S-97, S-122 | TestProgramSignIn_LaunchProtocol, TestProgramSignIn_Cleanup | DOC-006 |
| FR-036 | US-9 | S-60 | hands-on H-12 | DOC-006 |
| FR-037 | US-9 | none until Q3 is answered | none until Q3 is answered | DOC-006 (if Q3 = A) |
| FR-038 | US-10 | S-70 | TestProgramSignIn_EndStates | DOC-006 |
| FR-039 | US-9 | S-59 | TestProgramSignIn_Availability | DOC-006 |
| FR-040 | US-9 | S-71, S-94, S-98, S-121, S-142, S-145 | TestProgramSignIn_ChatCard, TestProgramSignIn_CardCancel | DOC-006 |

**Completeness check**: FR-001 to FR-051 each appear. Every scenario S-01 to S-148 appears in the matrix and in the Test Implementation Order (checked by a script that counts table rows only; see response-7.md). FR-037 is required (founder Q3 = A). FR-045 has no user-visible effect.

---

## Assumptions

- **A-1** (Q1, **decided A** 22 Nov): independent work follows the web-chat setting. Channel-originated work, including later edits and continuations, keeps its channel restriction (I-2, not a question).
- **A-2** (Q2, **decided** 22 Nov): off for every connector channel, on for the web chat; the switch is on the Connectors page, per bound channel (one workspace, one agent).
- **A-3**: preset sites, checks and the GitHub recipe are corrected as data after G2/G3.
- **A-4**: one credential per entry per installation (single-person product; hosted is the same code).
- **A-5**: a custom entry's git host is added to that run's sites when the entry matches.
- **A-6**: S2 ships the GitHub recipe only on platforms where G2 passes; Fly is paste-only; Windows has no sign-in.

## Clarifications

### 2026-11-22 (revision 1)

- **C-1** Founder Q1 A: one login per person, switched on per agent; generic; Custom first-class; presets as data.
- **C-2** Founder Q2 A: paste first (S1), sign-in next (S2).
- **C-3** Founder: paste as a credential card. Architect: shared card body; the Settings dialog saves through `PUT /programs/{id}/credential`.
- **C-4** Founder Q3 A: existing-login import is S3.
- **C-6** ADR departure: Ask approval cards unchanged; sites granted after the person permits. The review raised no blocker.
- **C-7** ADR fact corrected: wrapper unwrapping is S0's `commandWordRoles`.
- **C-8** Founder Q4: setting per channel; defaults A-2.

### 2026-11-22 (revision 2)

- **C-5 (revised)** No `request_program_login`; the chat card's own "Sign in" gives the outcome.
- **C-9** to **C-19**: see `/workspace/logs/1960/spec-s1/response-1.md`. In summary:
  - C1 boundaries; C2 provenance at creation; C3 person gate;
  - one injection rule; shared words; git-host conflicts; child-executable probe;
  - generation; typed recipes; protected folder; chat-card sign-in; H-03 oracle;
  - N1/N2.

### 2026-11-22 (revision 3, after spec review round 2)

- **C-20** R2-C1: masking moved to the child's output stream (I-1). Revision 2's claim "B1 alone covers the bash paths" was wrong, because capture caps, draining reads and JSON encoding come before B1 (`cappedBuffer.Write`, `ProcessSession.Read`, `executeRead`). Account labels are a sink (FR-046).
- **C-21** R2-C2: revision 2 attached provenance only at creation. Instruction updates and the system-message continuation path (`processSystemMessage` builds options without an origin) lost it. I-2 makes provenance a required fail-closed field and joins it on every instruction change.
- **C-22** R2-C3: revision 2's person gate did not cover the existing card submit route, which accepts the person's API bearer. I-3 closes it for program cards, decided from the stored kind.
- **C-23** R2-M1: crash-safe launch protocol; Windows sign-in stays unsupported.
- **C-24** R2-M2: all recipe subprocesses bounded; cancel from every active state; one supersession outcome (`cancelled`); S-92 corrected from `failed`.
- **C-25** R2-M3: check id; restart and edit recovery.
- **C-26** R2-M4: card-bound sign-in reuses `CardReceipt` in the same write.
- **C-27** R2-N1 to N3: cleanup field, slice-qualified criteria, complete scenarios, per-row S-81 oracle.

### 2026-11-22 (revision 4, after spec review round 3)

- **C-28** R3-C1: revision 3's join turned `{}` (web) into nothing and let `independent` disappear next to a connector, so joining could make withheld work eligible. Provenance is now a set of explicit origins; join is union; eligibility needs every origin allowed (monotone restriction, S-126). Person UI edits preserve provenance.
- **C-29** R3-M1: revision 3's "one writer" guard counted calls to the new writer only. The existing generic `Store.Set`, `Store.Delete` and `RemoveReference` could overwrite or remove a program token. The guard now lives in the store (FR-048, S-127), with the grep inventory of existing writers.
- **C-30** R3-M2: revision 3 flushed a held token prefix raw on close. The writer now protects every substring of 8 or more bytes and holds back at most 7 bytes.
- **C-31** R3-M3: `check_revision` separates check-relevant edits from cosmetic edits.
- **C-32** R3-M4: verified (S2) publication runs no second check; commit point stated.
- **C-33** R3-M5: ⚠️ Correction. Revisions 2 and 3 said `ClaimResume` resumes "exactly once". It admits at most once; a crash after the claim and before the turn runs yields zero continuations, shown as the existing restart stop.
- **C-34** R3-M6: `PUT /agents/{id}` refuses non-browser callers only when `programs` is present, after its existing bounded decode and before any mutation.
- **C-35** R3-N1 to N3: S-103 oracle can now fail; S-120 timing consistent with the 15 s read-back limit, plus S-133; per-slice spawn inventory.

### 2026-11-22 (revision 5, after spec review round 4)

- **C-36** R4-M1: revision 4 said the writer class was closed. That was too strong: the method guard stopped direct calls but not a stale snapshot written back by an unrelated `Set` from a second `Store` object or another process, because the file lock covered only the write. Every mutation is now one locked read-modify-write transaction (FR-049), with a real lock on Windows.
- **C-37** R4-M2: one standing chat can be reached from the web and from a channel. Storing provenance on that root made separate later turns inherit earlier origins (a false withhold of a web turn, and unclear restart behaviour). Roots now store nothing; lineage records keep theirs (FR-050).
- **C-38** R4-M3: "starting a check supersedes" let a delayed launch of an older check stop the newer one. Reservation now happens under the lock with the commit; launches never supersede (FR-047).
- **C-39** R4-M4: the continuation is admitted at card submit, before the check ends, so "told the check outcome" was not achievable. It now says what is true (stored, checking); the card shows its own generation's result (FR-051).
- **C-40** R4-M5: a card cancel could race a card-bound sign-in commit. Cancel now ends the attempt, and the commit re-checks the card under the registry lock (FR-040).
- **C-41** R4-N1 to N3: S-103 watches the bytes entering the cap directly; S-124 uses a task Dev may edit, with a regression for a person-created task; D-8 states per row whether the body is read.

### 2026-11-22 (revision 6, after spec review round 5, the final round)

- **C-42** R5-M1: revision 5 classified every parentless, non-delegate record as `root`. That was too broad: task and isolated scheduled sessions have no parent either, so a Telegram-created task re-entered from the web would have lost its restriction. `root` now requires an identified standing conversation; everything else is `lineage`.
- **C-43** R5-M2: revision 5's "card still pending" fence would have refused the commit that S-98 requires after a conversation Stop. Stop and card cancellation are now distinct at the commit, with a durable continuation veto and recovery.
- **C-44** R5-M3: the store-lock wait had no bound and was not tied to the verifying deadline. Admission is now bounded at both layers; a single claim decides between commit, timeout and cancel; the write is synchronous and read back.
- **C-45** R5-N1 and N2: the stale-writer tests now pause inside the transaction; the lock probes read real state and have planted mutants.

### 2026-11-22 (revision 8, founder answers and correction)

- **C-46** S-Q2 and the founder's correction (09:08Z, "the connection is per workspace and agent already"): verified in code. Revision 6's one switch per channel instance is kept; it moves onto the Connectors page, and the panel names the binding. Revision 7's per-agent list and endpoints are withdrawn.
- **C-47** An unbound channel reaches no agent (retired unbound routing; `ResolveRoute` drops), so "several agents behind one unbound channel" does not occur today; the panel says the switch has no effect until the channel is bound. Rebinding turns the switch off.
- **C-48** Q1 = A, Q3 = A, Q4 = A recorded: `independent_work_allowed` removed; FR-037 required; card unchanged.

## Questions for the founder

**All four questions were answered on 22 Nov 2026: Q1 A, Q2 in the founder's own words plus his correction (C-46), Q3 A, Q4 A.** The text below is kept for the record. Revision 8 raises no new question.

**Q1 — Work that nobody started from a chat or channel**
Context and impact: work that starts from a Telegram or Discord message now keeps that channel's setting wherever it goes: helpers, other agents, tasks (also when an existing task is changed from there), and follow-up turns. The open question covers only work with no such starting message: heartbeats, and tasks or schedules you created yourself in the app. Whichever you choose, work that mixes origins needs every one of its origins to be allowed. It changes FR-010 and D-5 rows 9–10 and 23.
- **A (recommended):** it follows the web-chat setting (on by default), so your own scheduled git or deploy work keeps working.
- **B:** it never gets connected programs.
- **C:** it gets its own on/off setting on the Programs page.
Answer "Q1 A", "Q1 B" or "Q1 C".

**Q2 — Confirm the channel defaults**
Context and impact: the spec uses off for every connector channel and on for the web chat. It changes FR-009 only.
- **A (recommended):** confirm.
- **B:** on everywhere by default; you switch channels off where you do not want it.
Answer "Q2 A" or "Q2 B".

**Q3 — Sign-in recipes for custom programs (S2 only)**
Context and impact: a custom recipe is a command you type that Omnipus runs outside the sandbox, with its full rights, to log in and read the token back. It decides FR-037; S1 is unaffected.
- **A (recommended):** allow it in S2, set only by you in Settings, with a clear "runs outside the sandbox" note and a confirm step.
- **B:** custom programs stay paste-only; sign-in only for tested presets.
Answer "Q3 A" or "Q3 B".

**Q4 — Agents asking for a program token in chat**
Context and impact: an agent can raise the existing secure card in the web chat. You fill it in from the browser only. The token is shared with every agent you switch the program on for, and the card never switches anything on. It decides FR-025, FR-040 and FR-044.
- **A (recommended):** yes, in S1 (and the card's "Sign in" in S2).
- **B:** no; tokens only in Settings → Programs.
Answer "Q4 A" or "Q4 B".

---

## Evidence table

| Claim | Evidence | Certainty |
|---|---|---|
| Revision 2 kept as reviewed | `sha256sum spec-rev2.md` → `ea2d86440ab0340b…`, equal to grill-2's hash | Verified |
| Baseline unchanged | `git -C /workspace/wt/a-1960-spec status --short` empty; HEAD `d5d1acdd3` | Verified |
| R2-C1: read JSON-encodes drained output | `pkg/tools/shell_bg.go::executeRead` (`session.Read()` → `json.Marshal(resp)` → `ForLLM`) | Verified |
| R2-C1: capture cap discards at write time | `pkg/sandbox/hardened_exec.go::cappedBuffer.Write` | Verified |
| R2-C1: bounded retention; draining read | `pkg/tools/session_activity.go::appendOutput` (`maxOutputBufferSize`); `pkg/tools/session.go::Read` (`Reset`) | Verified |
| R2-C1: foreground truncation before result | `pkg/tools/shell.go::foregroundResultFromSandbox` (`truncateOutput`) | Verified |
| R2-C1: replacer spellings | `pkg/config/security.go::sensitiveReplacementPairs` (original + Latin-1) | Verified |
| R2-C2: creator may change `prompt` | `pkg/tools/task_update_fields.go::checkDefinitionAuthority`, `applyOrganisationFields` | Verified |
| R2-C2: one reconciled write | `pkg/tools/task.go::persistUpdate` (`WriteTaskReconciled`) | Verified |
| R2-C2: async completion → system message without origin | `pkg/agent/async_notifier.go::Notify` (`Channel: "system"`); `pkg/agent/loop_inbound.go::processSystemMessage` (`processOptions` without `InputOrigin`; `runOrdinaryGoalFollowUp`) | Verified |
| R2-C3: submit route not person-gated; body read before lookup | `pkg/gateway/rest.go::registerCoreRoutes` (`withAuthAndBodyLimit`); `pkg/gateway/rest_secret_requests.go::HandleSecretRequests` (`io.ReadAll` before `reg.Get`) | Verified |
| R2-C3: person bearer resolves to the person | `pkg/gateway/auth.go::resolveBearerIdentity`, `matchUserBearer` | Verified |
| R2-M1: PID known only after start | `pkg/tools/shell_bg.go::runBackground` (`session.PID = cmd.Process.Pid` after start) | Verified |
| R2-M4: receipt in the same write; recovery by receipt | `pkg/credentials/card.go::CardReceipt` comment; `pkg/secretrequest/recovery_inventory.go::recoverPendingLocked` | Verified |
| Child PATH order | `pkg/tools/shell_env.go::composeExecutionEnv` lines 251–281, confirmed by grill-2 | Verified (by reviewer; not re-read by me) |
| Linux Pdeathsig is per thread; macOS `KERN_PROCARGS2` exposes own processes' environment | not run | Inferred (platform knowledge); H-13 and S-117 check outcomes |
| Vendor sites, checks, recipes | not run | Unknown (G2/G3) |
| Traceability and scenario completeness | script over this file (see response-2.md) | Verified |
| Revision 3 kept as reviewed | `sha256sum spec-rev3.md` → `0e66fe8aaf99c51e…`, equal to grill-3's hash | Verified |
| R3-M1: generic writers have no kind check | `pkg/credentials/store.go::Store.Set` (writes `sf.Credentials[name]`), `Store.Delete` (also removes the card row); `pkg/credentials/private.go::RemoveReference` (no kind refusal); `SelectPrivateVersion`, `SetPrivateChannelAvailability` (refuse non-private, `ErrNotPrivateSecret`); `pkg/gateway/rest_settings.go::setCredential` (`requireReAuth`, then `store.Set(req.Key, req.Value)`); `pkg/gateway/rest.go` registers `/api/v1/credentials*` with `withAuth` | Verified |
| R3-M1: inventory of mutators and callers | grep commands in "Existing raw credential writers": 9 exported mutating methods (plus 2 new program-scoped ones), about 20 non-test call sites, 39 holder files | Verified (grep, filtered by hand; completeness is enforced by S-127 reflection, not by this list) |
| R3-M1: re-auth skipped in platform mode | `pkg/gateway/rest_integrations_auth.go::requireReAuth` | Verified |
| R3-M5: claim precedes the turn; claim suppresses re-dispatch; boot recovery never dispatches | `pkg/secretrequest/recovery.go::ClaimResume` (`AppendTranscriptOnce`), `resumeLocked` (returns if the resume entry exists); `pkg/agent/revive_inbound.go::runInboundTurnWithRevival` (`ClaimResume` then `runAgentLoop`); `pkg/agent/boot_ordinary_recovery.go::recoverOrdinaryRoot` ("never dispatches a turn") | Verified |
| R3-M5: recovery re-dispatches a submitted card without a resume entry | `pkg/secretrequest/recovery.go::ensureDeliveryLocked` called from the recovery retry loop | Verified |
| R3-M6: agent update reads the body before field checks | `pkg/gateway/rest_agents_update.go::validateRequest` (`readAgentWriteBody`) | Verified |
| Revision 4 kept as reviewed | `sha256sum spec-rev4.md` → `3c3b1e04…`, equal to grill-4's hash | Verified |
| R4-M1: file lock covers only the write; load is separate; `Set` holds only its own object's mutex | `pkg/credentials/store.go::saveFileNoLock` (`fileutil.WithFlock(SidecarLockPath)` around the write only), `loadFileInternal`, `Store.Set` (`s.mu`) | Verified |
| R4-M1: CLI opens an independent `Store` | `pkg/app/internal/credentials/command.go::openStore`, `newSetCommand` | Verified |
| R4-M1: Windows lock is a no-op | `pkg/fileutil/flock_windows.go::WithFlock` ("NO cross-process write protection") | Verified |
| R4-M2: roots are records without parent and not a delegate; never rewritten | `pkg/agent/ordinary_root_record.go::ensureOrdinaryRootRecord` | Verified |
| R4-M2: turn origin is the set of consumed inputs | `pkg/tools/turn_origin.go::TurnOrigin` | Verified |
| R4-M4: continuation admitted at submit; resume text is reference-only; display schema closed | `pkg/secretrequest/registry.go::Submit` (`CommitCard` → submitted → `ensureDeliveryLocked`); `recovery.go::ResumeMessage`; `contracts/components/schemas/SecretRecipientDisplay.yaml` (`additionalProperties: false`), `SecretRequestState.yaml` | Verified |
| R4-M5: cancel handled under the registry mutex | `pkg/secretrequest/registry.go` (`r.mu` around cancel and submit) | Verified |
| R4-N1: capture cap and result limit | `pkg/sandbox/hardened_exec.go::outputCap` (4 MiB); `pkg/config/context_settings.go::DefaultBuiltinSuccessCap` (64,000) | Verified |
| R4-N2: a person-created task matches no agent | `pkg/task/task.go::CreatedByAgent` | Verified |
| Revision 5 kept as reviewed | `sha256sum spec-rev5.md` → `f23f47fbb1f5ba52…`, equal to grill-5's hash | Verified |
| R5-M1: work sessions are created without a parent; work identity comes from origin kind, `TaskID` and the plan witness | `pkg/agent/task_executor.go::createTaskSessionSyncAs` (`NewSession(SessionTypeTask, …)`, no parent); `pkg/session/unified.go::newSessionMeta`, `NewScheduledSession`; `pkg/agent/turn_attendance.go::unattendedOrigin`, `classifySessionChain`; `pkg/agent/ordinary_root_record.go::initializeOrdinaryRootRecord` (origin = session type); `pkg/session/lifecycle_edge.go` origin kinds | Verified |
| R5-M2: Stop turns pending cards `stopped`; vetoes only submitted ones; recovery converts only pending cards | `pkg/secretrequest/recovery.go::Registry.Stop`; `pkg/agent/secret_request_stop.go::persistSecretStopIntent`; `pkg/secretrequest/recovery_inventory.go::recoverPendingLocked`; `pkg/secretrequest/reconcile.go` (`session_deleted`) | Verified |
| R5-M3: the reused file lock blocks without a deadline | `pkg/fileutil/flock_unix.go::flockExclusive` (`LOCK_EX`), `WithFlock` (no context) | Verified |
| R5-N2: the missing-ID request never reaches `Registry.Get` | `pkg/gateway/rest_secret_requests.go::HandleSecretRequests`; `pkg/secretrequest/registry.go::Registry.Get` (`r.mu`) | Verified |
| Revision 7 kept (withdrawn) | `sha256sum spec-rev7.md` → `551b7fe7189e8330…` | Verified |
| A channel is bound to one workspace and one main agent | `pkg/config/config.go::ChannelInstanceConfig` (`WorkspaceID`, `Identity`); `pkg/config/config_channels_instance.go::IsWorkspaceBound` | Verified |
| Unbound channels reach no agent; `Bindings` is not used for inbound routing | `pkg/routing/route.go::RouteResolver.ResolveRoute` (drops when not bound); `scripts/check-no-unbound-channel-routing.sh`; remaining `cfg.Bindings` uses are `pkg/config/validate.go::RepairStaleChannelWildcardBindings` and `pkg/gateway/schedules.go::resolveAgentDefaultChannel` (outbound default channel) | Verified |
| The panel can read the binding without a new contract | `contracts/openapi.yaml` `/channels/{id}/routing` GET → `ChannelRouting` (`bound`, `workspace_id`, `default_agent_id`); `src/components/skills/ChannelConfigPanel.tsx` already loads it | Verified |
| **Self-check** | Revision 8 = revision 6 plus the founder-asked changes only: Connectors-page placement with the binding shown, the unbound case, rebinding resets the switch, and Q1/Q3/Q4 = A. No per-agent list, no new endpoint, no agent-lifecycle rule. response-7.md lists every changed FR and scenario against revision 6. No repository file edited; nothing committed or pushed. | Verified |
