# ADR-084 — The Judge is an active reviewer, not a passive one

- **Status:** Proposed (revision 8 — five of §8's claims about the code corrected and the three open items resolved, see §9; no decision withdrawn. Revision 7 — claim-triggered, off the critical path, evidence-tiered for all task kinds; see §8. Revision 6 — D4's residual-risk acceptance narrowed, D10's confinement scope widened) — 2026-09-09
  - *Revision 5 — four claims about the code corrected; decisions unchanged.*
  - *Revision 4 — greenfield, migration removed by operator directive.*
- **Amends:** the un-ADR'd judge fix-wave in commit `02214f5c` (2026-09-09, "fix GX-E") — `planArtifactCheck`, the rung-1.5 dispatch, and the working-tree diff feed. *(Revision 1 wrongly attributed this to ADR-082, which is about UI-independent turns and session-bound streaming and says nothing about the Judge.)*
- **Relates to:** ADR-052 (verifier adjudication, FR-039 reproducibility), ADR-055 (PlanSupervisor, the skills-allowlist gap), ADR-057 FR-011 (delegated children own their sessions), ADR-074 D7 (`evidence_quote`), ADR-077 / Constraint #6 (tool policy), Constraint #8 (contract-first wire formats)
- **Prerequisites:** issue #688 (configurable judge timeout) **and** D9's investigation bounds, **and** D10's capability closures. None of D1 ships before all three.
- **Spec:** `docs/internal/specs/judge-active-reviewer-spec.md` — written, and implementing revision 7 as corrected by §9. **Read §9 before D12/D13/D14**: three of the symbols §8 names for removal must be kept, and "after delivery" is a reordering §8 does not locate.

## 1. Operator direction (verbatim, 2026-09-09)

> we need the judge to actually judge like a human and not like a program, that is the whole point of having the judge, common sense judgement and file reading tools

This ADR records that decision, the evidence that prompted it, and — after review — the guard rails without which it must not ship.

## 2. Evidence

A real goal run (session `session_01M20DVP62XVKNVRZXVM4CHP4B`, 2026-09-08, `z-ai/glm-5.3-flash`) rejected essentially every criterion with wording of this shape:

> "no workspace diff available; …", "no diff, file contents, or machine-check evidence verifies …", "no diff or CSS contents available".

The work existed: `neon-2048/index.html` (22.5 KB) was on disk. Three mechanisms hid it:

| # | Cause | Evidence |
|---|---|---|
| E1 | The Judge is **told not to look**. `coreagent.JudgeDefaultRubric` ends: "Do not run tools, do not request more information, do not speculate beyond what you were given." | The verifier session recorded `tool_calls: 0`. The rubric is materialised to `agents/judge/SOUL.md` per install. |
| E2 | The Judge **is granted** `read_file`, `list_directory`, `inspect_session`, `ToolSearch` and `Skill` by `coreagent::systemAgentSeed`, and `judgeCallTimeout`'s doc comment budgets 120 s *for* rubric-driven tool escalation. | Grant and prohibition contradict; the prohibition wins. |
| E3 | Nothing was committed at adjudication time, and the transcript renderer strips tool payloads. | First commit landed 112 s **after** the verdict. `renderTranscriptEntriesForWindow` renders `[tool_call] <name> -> <status>`: 170 KB of `write_file` bodies (94 % of transcript bytes) never reached the prompt. |

E3 was addressed by commit `02214f5c`. E1 and E2 are what this ADR resolves.

The deeper defect is the **stance**: the rubric is written for a passive reviewer, forbids seeking more, and makes "nothing was handed to me" a valid terminal verdict. A reviewer that may not look will reject correct work whenever the evidence pipeline is imperfect — and it never is.

## 2.1 What review corrected in revision 1

Revision 1's guard rails were **one-sided**: every control protected against rejecting good work, none against accepting bad work. Four claims were false and are corrected here:

| # | Revision 1 claimed | Verified reality |
|---|---|---|
| C1 | "The realistic worst case is a wrong verdict on one goal… it cannot execute, write, spend, or exfiltrate." | True of the Judge **process**, false of the **verdict**. At task scope `task_executor.go::adjudicateClaim` on `verdict.Met` calls `completeTaskWithResult` → `onTaskComplete` → `AdvanceBlockedDependents` + `advanceBlockedTasks`, which **dispatches** downstream tasks that hold `bash`, `write_file`, network and delegation, and `notifySourceChannel` emits outbound messages. A false `met:true` advances a plan. |
| C2 | "Its write surface stays empty." | Empty only for the **static** catalog. `compositor.go::resolveEffectivePolicyWith` short-circuits `if cfg.GodMode { return allow }` before any per-agent map. And `loop_mcp.go::registerServerTools` registers every connected MCP server's tools into **every** agent with no filter; `systemAgentSeed`'s `denyAllThenOverride` covers only `allStaticToolNames`, so an MCP tool resolves from the global ceiling. The deleted "Do not run tools" line is what currently holds both shut. |
| C3 | Tools are "`read_file`, `list_directory`, `inspect_session`". | Also `ToolSearch` and `Skill` — and `systemAgentSkills` returns `nil` for the Judge, which its own doc comment defines as **unrestricted**: every installed skill, including operator- or ClawHub-installed ones. A skill body is instruction-shaped text arriving through a tool result, bypassing `buildJudgeUserContent`'s untrusted-data framing. |
| C4 | Tools are "read-only and workspace-rooted (`WithSystemAgentWorkspaceOverride`)". | The override picks *which* workspace; **confinement** is a separate global switch. `instance.go::NewAgentInstance` derives `readRestrict` from `AgentDefaults.RestrictToWorkspace`/`AllowReadOutsideWorkspace`. With the path guard off, `fspolicy::EffectiveFSPolicy` returns `FSScopeUnrestricted`, bounded only by `appCarveOutSecretPaths` — which does **not** cover `sessions/`, `tasks/`, `plans/`, `memory/`. An unconfined Judge reads every transcript in the install, defeating the `VerifierSessionScopeAllows` lock. |

## 3. Decisions

### D1 — The Judge investigates

The prohibition is removed. The Judge is expected to use its read-only tools to obtain the evidence a criterion needs: open the artifact the criterion names, list the directory, read the session record. It ships only with D10's closures, D9's bounds and #688 in place.

### D1a — Delegated work is reachable

For goal scope `verifier_adjudication.go::resolveVerifierSessionScope` authorises exactly the chat session. Under ADR-057 a delegated child owns its own session, and `renderTranscriptEntriesForWindow`'s doc comment states child narration "is never present in the entries this function is handed". So for any delegated work — the normal case in a delegation-first product — the Judge still cannot look, while D2 tells it that wall is a starting point. That combination manufactures guesses.

The scope extends to the adjudicated session's **descendant** sessions (the ADR-057 parent index on `UnifiedStore` already exists). Absent that reach, a delegated criterion is `unable_to_verify` by construction and MUST return that (D2a) — never `met`.

### D2 — "No evidence" stops being a terminal verdict

Nothing quotable is a **starting point**, not an ending: the Judge looks first. `unmet` for absent evidence is honest only *after* a genuine attempt, and the reason must say what was looked for and where.

### D2a — The outcome is a three-state enum, not a boolean

Today `judge.go::judgeCriterionResponse` is `{id, met, reason, evidence_quote}`; every non-`met` answer collapses to `met:false`. So a Judge that genuinely looked and genuinely could not decide is indistinguishable from "the work is not done" — it burns a round and sends the worker to redo finished work. That is E1's failure relocated one layer up.

The engine already owns the taxonomy: `NonVerdictNone` / `NonVerdictUnableToVerify` / `NonVerdictCriterionUnjudgeable`, with `UnableToVerifyTracker`, `unableToVerifyEscalateFn` and round-withholding — but only the deterministic rungs populate it; the prose loop classifies everything `NonVerdictNone`, resetting the tracker.

The verdict becomes `{id, outcome, evidence_quote, reason}` with `outcome ∈ {met, unmet, unable_to_verify}`, and `unable_to_verify` maps to `NonVerdictUnableToVerify` so it flows into the existing bound, escalation and withholding. `met` remains the only satisfying value.

### D2b — A `met` with no quote is rejected in code

Revision 1's entire anti-inflation mitigation was the prose "a verdict still has to point at something real". Nothing checked it: `parseJudgeResponse` only truncates the quote, and `runVerifierAdjudication` copies it verbatim. `{"met":true,"evidence_quote":"the file exists and looks complete"}` passes today.

A `met` carrying an empty `evidence_quote` is rewritten to `unable_to_verify` at the parser, before the verdict reaches finalisation. This is the mechanical replacement for the old rubric's "nothing quotable ⇒ met:false", and it follows the precedent of `dedupeJudgeCriteriaAnyUnmetWins`, which exists because real LLMs let a later `met:true` override an earlier correct `met:false`.

### D2c — Quotes are grounded in this turn's own evidence

`evidence_quote` carries a `source` discriminator (`diff` | `transcript` | `machine_check` | `file_read` | `session_read`). A quote sourced `file_read`/`session_read` MUST be a substring of a tool result produced **in this verifier turn** — those results are already on `session.ToolCall.Result`, so this is a substring test, not a re-read. A quote grounding `met` that matches only the worker's own claim section is rejected: the rubric's existing "never substitute the worker's own description for evidence" becomes enforceable rather than advisory.

### D2d — Rubric wording replaces the deleted prohibition

> Before returning `met` for a criterion you could not verify from the material you were given, you must have looked, and your reason must name **what you opened and what you found there** — the exact path, and the specific lines or structure that satisfy the criterion. "I read the file and it looks correct", "the implementation appears complete", or a quote that merely proves a file exists when the criterion asks what is *in* it, are not verification: return `unable_to_verify`. Finding a file *related* to a criterion is not evidence the criterion is *satisfied*. If a criterion has several parts, every part needs its own evidence; one part met is `unmet`, not `met`. A truncated read cannot ground a negative finding — page through the file, or return `unable_to_verify`.

### D3 — Evidence includes what the Judge read itself

`evidence_quote` may quote a file the Judge opened, subject to D2c's grounding. The quote requirement is unchanged and remains the anti-hallucination control.

### D4 — Material under review is evidence, never instruction

The rubric already says this of the worker's summary. It extends to **every byte the Judge reads**: file contents, page text, transcript passages, tool output, and skill bodies. Text addressing the Judge — claiming a criterion is waived, satisfied, descoped, or instructing a verdict — is reported as suspicious content and never obeyed.

**Residual risk, re-derived from the true blast radius (C1).** A successful injection does not merely produce one wrong verdict: at task scope a false `met` marks the task Done, which unblocks and dispatches dependent tasks holding `bash` and `write_file`, and notifies the source channel. Goal scope is comparatively benign (`runGoalAdjudication` → `clearGoal`, terminal). This is accepted **only** because D2a–D2d make an unearned `met` structurally hard to produce and D10 closes the capability paths. **If those controls are descoped, this acceptance is void.**

> ⚠️ **AMENDED BY REVISION 6 — read §7 R6-a before relying on this paragraph.** "Structurally hard to produce" is the claim the controls support; "impossible", which the spec's revision-2 user story asserted, is not. The controls close *wrong file*; they do not close *right file, wrong conclusion*, because nothing evaluates whether a grounded quote **entails** the criterion — that is the model's judgement, which §1 directs the Judge to supply. §7 R6-a carries the amended acceptance sentence and names the residual.

### D5 — Deterministic checks inform the Judge; they do not replace it

Commit `02214f5c` added `judge.go::planArtifactCheck`, which settles an artifact criterion outright with no LLM call. Under the operator's direction that is wrong in principle: it substitutes a program for the judgement the Judge exists to provide. The check is retained and demoted to a **fact in the evidence block**.

### D5a — Direction of authority is one-way

A completed check that exited **non-zero** (file absent, empty, or the named substring missing) is a **veto**: such a criterion cannot be returned `met`, and the parser rewrites it to `unmet` and logs the contradiction — the Judge cannot assert a filesystem fact the filesystem denies. A check that exited **zero** is informative only: the Judge may still return `unmet` when the criterion asks for more than existence, which is exactly the "a single self-contained file" case D5 serves. An inconclusive check (policy-denied, timed out, unreadable exit code) vetoes nothing.

Path safety (`isSafeWorkspaceArtifactPath`) is unchanged.

### D6 — REMOVED: no migration (greenfield, operator directive 2026-09-09)

> migration is not needed assume greenfield remove migrations

Revisions 2 and 3 specified a migration that compared each install's `agents/judge/SOUL.md` against a frozen list of historical `JudgeDefaultRubric` texts and overwrote un-edited ones. That is deleted in full: no frozen list, no hash comparison, no one-shot marker, no overwrite path, no WARN-on-edited branch. Nothing in this ADR writes over an existing soul file, and the existing invariants stand untouched — `SeedSystemAgentSoulFile` never overwrites, `ensureVerifierSoul` backfills only when empty.

**The consequence, stated plainly:** the rubric is materialised to `agents/judge/SOUL.md` and read from there, so an install that already has that file **keeps the old instructions, including "Do not run tools", indefinitely.** D1 reaches fresh installs and any install whose soul file is absent or empty. Bringing an existing install onto the new behaviour is an operator action — delete or replace that file — not something this change performs. That is the accepted meaning of greenfield here.

This also retires revision 3's R3-a, which existed only to correct the migration's frozen list.

### D7 — Verdict provenance and an investigation log

Each criterion records how it was reached (`judge_read` | `deterministic_check` | `diff` | `transcript` | `session_read` | `none`). Provenance alone cannot diagnose two runs disagreeing, so each adjudication also records an **investigation log**: the ordered `(tool, target, bytes_returned, truncated)` for every tool call the verifier turn made, plus the model and resolved timeout. This is derivable from the verifier session's own `session.ToolCall` records — no new capture path.

### D8 — Contract-first (Constraint #8)

`CriterionVerdict` is a wire type (`contracts/components/schemas/CriterionVerdict.yaml`, `additionalProperties: false`) and is persisted and replayed. D2a's `outcome` and D7's `provenance` are added through the five-step process — schema first, `scripts/gen-contracts.sh`, generated Go and TS committed in the same commit. Both are optional and empty-safe so pre-existing persisted verdicts still parse, exactly as `evidence_quote` was added under ADR-074 D7.

### D9 — Budget, and the failure mode that matters

Measured bounds today: `judgeCallTimeout` 120 s per verifier turn; `judgeRetryBackoff` {60,120,300}; `goalJudgeRoundTimeout` 10 min, running **synchronously inside the operator's chat turn**; Judge `MaxIterations` 200 (hard ceiling 400) with **no cap on files read**; `read_file` 64 KB per call.

The dangerous part is classification, not size. A `judgeCallTimeout` expiry surfaces as a turn failure → `Unavailable=true` → `runGoalAdjudication` does **not** consume a round and re-arms. Tokens spent, tool calls made, zero progress, retried forever. Today that is rare because a no-tool Judge finishes in ~32 s; D1 makes 120 s the normal order of magnitude and converts a rare failure into the steady state. Separately, `checkJudgeSEC26` takes a token before dispatch and `turnLoop` takes one per iteration, so an `MaxIterations`-scale turn can exhaust the Judge's own window and leave it permanently `Unavailable`.

Three prerequisites, not just #688:
1. Make `judgeCallTimeout` operator-configurable (#688) **and** raise its default for a tool-using turn.
2. Bound the investigation explicitly — a per-adjudication cap on tool calls and total bytes read, enforced in the verifier dispatch, not left to a global chat default that happens to be 200.
3. Reclassify a timeout that occurred **after the turn made progress** as `unable_to_verify` (D2a) rather than `Unavailable`, so it consumes a round and reaches an honest failure instead of looping.

D5's cost lands here too: artifact criteria that previously cost zero tokens re-enter the prose set on every round. The ~32 s / 7.5 K-token baseline was measured with them excluded, so it does not bound the new workload.

### D10 — Capability closures (shipping preconditions)

D1 must not widen the Judge's real capability. Before it ships:

- **God mode.** `resolveEffectivePolicyWith`'s `cfg.GodMode` short-circuit floors every tool at `allow` regardless of seed. The verifier dispatch refuses to run a Judge turn under god mode rather than running one holding `bash`.
- **MCP.** The Judge's seed gains an explicit `mcp_*: deny` wildcard — the one place a wildcard is legitimate under Constraint #6's own MCP carve-out — because `registerServerTools` registers MCP tools into every agent and they resolve from the global ceiling.
- **Skills.** The allowlist becomes explicit and non-nil (`["define-goal"]`, or empty), re-enforced every boot by `seedSystemAgents`' existing `skills != nil` branch. A skill body is instruction-shaped text delivered through a tool result, outside `buildJudgeUserContent`'s framing; ADR-055 recorded this gap rather than closing it, and D1 makes closing it mandatory.
- **Filesystem confinement** *(retitled from "Read confinement" by revision 6 — see §7 R6-b: the mechanism R5-d specifies governs `FSOpRead`, `FSOpList` **and** `FSOpSend`, and narrowing it to reads would leave `list_directory $OMNIPUS_HOME/sessions/` open)*. The verifier turn pins `restrict = true` for itself regardless of the global `RestrictToWorkspace`/`AllowReadOutsideWorkspace` defaults. Confinement is a role invariant for a verifier, exactly like `MemoryEnabled=false` and its tool policy. Note `seedSystemAgents` re-enforces many fields but **not** `Workspace` — the reason `systemAgentSeed` gives for denying PlanSupervisor `read_file` at all ("a read grant would have unspecified, operator-mutable reach"); the Judge is safe only once D10 pins the confinement.

### D11 — File-not-found versus unreachable

A criterion naming a path that does not exist is `unmet` (D5a's veto). A path the Judge **cannot reach** — outside its confinement, or a delegated session before D1a — is `unable_to_verify`. Revision 1 conflated both as "nothing found".

## 4. Consequences

- Correct work stops being rejected because the evidence pipeline was imperfect.
- Adjudication costs more and takes longer, and that latency is user-visible because the goal round runs inside the chat turn. D9 bounds it.
- ADR-052 FR-039 re-enforces `MemoryEnabled=false` every boot precisely so the same evidence yields the same verdict. D1 reintroduces run-to-run variance through a different channel. FR-039's mechanism stays (memory variance is unbounded and invisible; tool variance is bounded and, under D7, recorded), but this ADR **trades reproducibility for auditability, deliberately** — D3 and D7 make a disagreement diagnosable, not preventable.
- Part of commit `02214f5c` is deliberately reversed (D5). Recorded here because that work carried no ADR of its own.
- The injection surface is opened in the narrow sense of D4 and accepted only with D2a–D2d and D10 in place.

## 5. Out of scope

- Granting the Judge any write, shell, network or delegation capability. Never.
- Changing SEC-26's budget mechanism (D9 notes its interaction; it does not change it).
- The browser control handover (ADR-085).

## 6. Revision 3 — corrections found while writing the spec

Spec authoring re-verified every claim in revision 2 and found eight further errors. They are corrected here; `docs/internal/specs/judge-active-reviewer-spec.md` implements the corrected form and records the evidence in its §0.

### R3-a — RETIRED

Concerned the migration's frozen rubric list. The migration itself is removed in revision 4 (see D6), so this correction no longer applies. Recorded rather than deleted so the history reads straight: the finding was correct — four historical rubric texts exist, not two — and it is moot only because nothing now compares against them.

### R3-b — D2c's `source` cannot live inside `evidence_quote` (blocker)

`evidence_quote` is `type: string, maxLength: 500` under `additionalProperties: false`, persisted (`pkg/task/verdict.go::CriterionVerdict.EvidenceQuote`), replayed (`pkg/gateway/replay.go`) and rendered by three existing vitest cases. Turning it into an object breaks every verdict already on disk. The discriminator is a **new sibling optional field** (`evidence_source`), added through Constraint #8's five steps alongside `outcome` and `provenance`.

### R3-c — D9.3 is wrong about round accounting (blocker)

Revision 2 said reclassifying a post-progress timeout as `unable_to_verify` makes it "consume a round". It does not: `JudgeCriteria`'s `noteNonVerdict` → `withheld` → `JudgeCriteriaResult{Unavailable: true}` → `runGoalAdjudication`'s `Unavailable` branch logs "round not consumed". The honest bound already exists and is different: `UnableToVerifyMaxRerunsDefault` = 3, so an honest failure arrives within K+1 adjudications rather than one.

Two further consequences revision 2 missed: relabelling alone does **not** break out of `runVerifierAdjudication`'s `judgeBackoffWait` + `continue` retry loop, so the loop must be exited explicitly; and a withheld **prose** criterion discards the whole adjudication including the tool-using LLM call already spent — a cost D9 must account for now that those calls are expensive.

### R3-d — Five mechanism errors

| # | Revision 2 said | Reality |
|---|---|---|
| C4 | D1a reaches descendant sessions via the ADR-057 parent index. | `UnifiedStore.parentIndex` exposes only `ChildCount`. The durable transitive walker already exists: `pkg/agent/goal_triggers.go::goalDescendantSessionIDs`. Use it. |
| C5 | D10 adds an `mcp_*: deny` wildcard to the Judge's seed. | That would **panic** through `denyAllThenOverride` → `validateOverrideKeys`. The MCP closure needs a different mechanism. |
| C6 | D10 pins read confinement for the verifier turn. | `readRestrict` is baked in at `pkg/agent/instance.go::NewAgentInstance` from global `AgentDefaults`; it cannot be a seeded per-agent field. The pin must happen where the instance is constructed for the verifier turn. |
| C7 | D5 demotes the artifact check to evidence. | As written it double-classifies every artifact criterion and silently defeats the K bound. The demotion must remove the criterion from the deterministic rung exactly once. |
| C8 | D2b, D2c and D5a are enforced "at the parser". | `parseJudgeResponse` sees neither tool results nor check outcomes. Only D2b (empty quote) can live there; D2c's grounding and D5a's veto need the enclosing adjudication, which holds both. |

None of these change the decisions. They change where and how each is enforced, and R3-a and R3-c change what the ADR claims the system does today.

## Revision 5 — corrections found while grilling the spec (2026-09-09)

An adversarial review of `docs/internal/specs/judge-active-reviewer-spec.md` re-verified this ADR's remaining claims about the code against `ab3d5b63`. Four are false. **None changes a decision** — each changes what the ADR asserts the system currently does, and therefore where a control has to be built. The spec implements the corrected form and records the evidence in its §0 (C9 – C15).

### R5-a — §2.1 C3's "`systemAgentSkills` returns `nil`, which its own doc comment defines as **unrestricted**" is stale

`pkg/agent/context.go::skillAllowed`'s doc comment now reads: *"A nil OR empty allowlist denies EVERY name … There is no 'unrestricted' state any more"* (ADR-072 D5). The registry-shelf hole C3 describes was closed before this ADR was written, so D10's skills bullet, as scoped, closes nothing — making the allowlist explicit and non-nil is documentation value.

The genuinely open path is the one that comment names next: `skillAllowed` *"governs the REGISTRY shelf only. The PROJECT shelf (a workspace mount's own skills) is gated separately, by the mount itself"*, via `cb.projectShelf` / `cb.projectShelfResolver`. Since `runVerifierAdjudication` re-roots the Judge into the workspace of the work under review, **a project-shelf skill the worker under review just wrote is loadable by the Judge** — instruction-shaped text arriving through a tool result, outside `buildJudgeUserContent`'s framing, which is exactly the risk D10's skills bullet exists to close. D10's skills closure is therefore `WithProjectShelf(nil)` / `WithProjectShelfResolver(nil)` for the verifier turn (spec FR-059a), with the non-nil allowlist retained for documentation (FR-059). `systemAgentSkills`' own stale doc comment is corrected in the same commit.

### R5-b — D1a over-claims `inspect_session`

`pkg/tools/inspect_session.go::Execute` renders each entry's tool calls as `toolCallSummary{Name, Args, Success}` — a name, a bounded args summary, and a success boolean. **No tool result payload.** That is the same defect as §2 E3 (the transcript renderer stripping tool payloads), sitting in the very tool D1a nominates to reach delegated work.

So D1a's reach is real but narrower than stated: extending the session scope to descendants makes a child's **narration** readable, and nothing more. A delegated child's `write_file` bodies are not reachable through `inspect_session` at all. Consequence for D2c: `session_read` can ground a quote about what a child *said*, never about what a file *contains* — delegated file work is verified by opening the resulting file. The spec makes this a hard rule (FR-014a): a `met` sourced `session_read` on a criterion naming a filesystem path is `unable_to_verify`.

### R5-c — D2c's and D7's shared premise about `session.ToolCall.Result` is false in both directions

D2c states the verifier turn's tool results are *"already on `session.ToolCall.Result`, so this is a substring test, not a re-read"*, and D7 concludes from the same premise that the investigation log is *"derivable from the verifier session's own `session.ToolCall` records — no new capture path"*. Two independent problems:

1. `runVerifierAdjudication` receives only `(content string, callErr error)` from `processTaskDirect`. The per-criterion mapping loop — the placement R3-c/C8 correctly identified — holds no tool results at all. Reaching them from `session.ToolCall` *is* a re-read, not the substring test D2c claims.
2. `session.ToolCall.Result` is a `map[string]any` that `pkg/agent/empty_in_place.go::recordEmptiedOnTranscript` **overwrites with a recall mark mid-turn** (ADR-066 D5). A long investigation therefore erases the results its own earlier verdicts were grounded in — genuine work becomes `unable_to_verify`, is withheld K rounds, and escalates as persistently-blocked. That is E1's failure reintroduced by E1's fix.

A new in-memory capture path, held for the duration of one adjudication, is therefore **required** rather than avoidable; grounding must not read `session.ToolCall.Result`; and the investigation log should be derived from the same capture, whose `(tool, target, bytes_returned, truncated)` fields grounding needs anyway. Spec FR-030's mechanism clause and FR-068.

### R5-d — D10's read-confinement bullet does not confine reads

D10 says the verifier turn *"pins `restrict = true` for itself"*. R3-d/C6 corrected *where* that pin lives; neither established what it still **does**. Under ADR-063 FR-2.2, `pkg/tools/resolvepath.go::ResolvePath` dispatches out-of-workdir access on the **operation**, explicitly not on `policy.Scope`: `FSOpRead`, `FSOpList` and `FSOpSend` are *"allowed anywhere outside the secret set, independent of `policy.Scope`"*. `ReadFileTool` passes `FSOpRead`, and a shipped test — `pkg/tools/filesystem_docextract_test.go::TestReadFile_DocumentSymlinkEscape_NowExtractsOpenly` — constructs the tool with `restrict=true` and asserts a symlink escape still succeeds.

So the pin as written changes nothing, and §2.1 C4's conclusion ("an unconfined Judge reads every transcript in the install, defeating the `VerifierSessionScopeAllows` lock") remains true **after** D10 as specified. Closing it needs a real mechanism in the shared filesystem gate — a new `fspolicy.FSPolicy.ReadConfined` honoured in `ResolvePath`'s `FSOpRead` branch, set only for System-Agent instances, plus the symlink case — owned by security-lead, with an explicit statement of what does not change for every other agent. Spec FR-060 / FR-060a. Two mechanical notes: `coreagent.IsSystemAgentID` takes a `CoreAgentID`, not a `string`; and confining all System Agents also confines PlanSupervisor, which is intended and must be stated.

**Unchanged by revision 5:** D1, D1a's descendant extension, D2, D2a, D2b, D2c's grounding requirement, D2d, D3, D4, D5, D5a, D6's removal, D7's provenance and log, D8, D9, D10's four closures as *goals*, and D11. Revision 5 corrects four factual premises and relocates two closures; it withdraws nothing.

## 7. Revision 6 — the risk acceptance is narrowed, and one closure is widened (2026-09-09)

A second adversarial review of `docs/internal/specs/judge-active-reviewer-spec.md`, at `ea8dffdb`,
found that **D4's residual-risk acceptance was calibrated on a claim the controls do not deliver**,
and that **D10's read-confinement bullet names one filesystem operation where the mechanism governs
three**. Both are recorded here rather than buried in §6, because the first changes what this ADR
accepts and the second changes what it requires. No decision is withdrawn. The spec's §0 carries
the code evidence for each (C16 – C21).

### R6-a — D4's residual-risk acceptance is narrowed: the controls close *wrong file*, not *wrong conclusion*

D4 accepts the injection surface "**only** because D2a–D2d make an unearned `met` structurally hard
to produce and D10 closes the capability paths", and revision 2's spec carried that forward as a
user story titled "an unearned `met` is impossible to state". **That is stronger than anything the
controls establish, and the gap is the failure class this ADR exists for.**

Trace a verdict of `{"outcome":"met","evidence_source":"file_read","evidence_target":
"neon-2048/index.html","evidence_quote":"<div id=\"game-board\" class=\"grid\"></div><script
src=\"game.js\">"}` against a **stub** `index.html`. It passes every control: the quote is
non-empty, both discriminators are present and in-enum, the quote is a genuine substring of a
genuine read of that exact path, the criterion names the path, the quote clears the length floor and
the boilerplate list, it is used once, the reason names the target, the criterion is one clause with
one entry, and the deterministic check exits zero — which D5a makes non-binding. The outcome is
`met`, and at task scope `adjudicateClaim` dispatches downstream tasks holding `bash` and
`write_file`.

**Nothing in D2a–D2d, and nothing the spec can add, evaluates whether the quote *entails* the
criterion.** That is irreducibly the model's judgement — which is exactly what §1's operator
direction asks the Judge to supply, and is not up for re-litigation. The controls make an unearned
`met` **expensive, attributable and auditable**; they do not make it impossible.

**D4's acceptance sentence is therefore amended to read:**

> This is accepted **only** because D2a–D2d make an unearned `met` costly to produce and, when
> produced, attributable to a named artifact — **not because they make it impossible.** A `met`
> grounded in a real, correctly-targeted, distinct, non-boilerplate quote from the very file the
> criterion names, whose reason names that file, is accepted by every mechanical control in the
> spec **even when the file does not satisfy the criterion**. That residual is the accepted risk,
> it is measured rather than assumed (spec SC-002b, against a real model), and D10 closes the
> capability paths that would make its consequences worse. **If any of the attribution controls is
> descoped — the reachability rule, the length floor and deny-list, the distinctness rule, the
> reason-names-target rule, or the per-clause evidence count — this acceptance is void.**

Two consequences the spec implements: the user story is retitled to what the controls deliver ("no
`met` can be stated without stating and grounding what it looked at"); and the success criterion
splits, so that the mechanical classes keep their 0-of-100 bar while the residual class is measured
against a real model with a **non-zero expected pass rate**, recorded as a baseline. A corpus built
only from the classes the controls were designed for would score perfectly with the original failure
class untouched, and the feature would be declared successful on a number that never tested it.

### R6-b — D10's read confinement governs three operations, not one

R5-d established that the confinement needs a real mechanism in the shared filesystem gate — a
`ReadConfined` flag honoured in `ResolvePath`'s out-of-workdir branch. That branch is
`case FSOpRead, FSOpList, FSOpSend:` (`pkg/tools/resolvepath.go:904`), so the flag necessarily
governs `read_file`, `list_directory` **and** `send_file`; `send_file` reaches it through a real
path (`pkg/tools/send_file.go:116` resolves the turn policy, `:126` passes `FSOpSend`).

Narrowing the flag to `FSOpRead` alone was considered and rejected: the Judge is granted
`list_directory`, so a read-only flag would leave `list_directory $OMNIPUS_HOME/sessions/` open and
close only half of §2.1 C4's hole. **D10's read-confinement bullet is therefore restated as
filesystem confinement over all three operations**, and the spec's requirement, oracle and success
criterion cover the three rather than reads alone (FR-060, FR-061, SC-006).

A second mechanical gap, recorded because it makes the mechanism buildable rather than merely
named: `fspolicy.EffectiveFSPolicy` **discards** its `ctx`, `agentID` and `workspaceID` parameters
outright, and the package has no System-Agent concept, so there was no input that could set the
flag. The spec names one, following the shipped `tools.WithVerifierSessionScope` precedent — an
engine-owned fact placed on the turn context that a tool reads but cannot set, fail-closed, with
"unset" meaning *not confined* so no other agent's policy changes (FR-060b).

### R6-c — Three consequences of D2a and D6 that this ADR states but does not bound

Recorded here because each is a property of a decision above, and in each case the spec's revision-5
form contradicted the decision it implemented.

| # | Decision | The unbounded or self-defeating consequence | Where the spec bounds it |
|---|---|---|---|
| 1 | **D2a** — `unable_to_verify` flows into the existing withholding | The existing bound is **per criterion**: `noteNonVerdict`'s no-op arm resets that criterion's counter, and `runGoalAdjudication`'s `Unavailable` branch consumes no round. Prose criteria never emitted `unable_to_verify` before, so D2a *creates* the path — after which a Judge withholding a **different** criterion each round loops without bound, each iteration a tool-using turn of up to the raised timeout inside the operator's chat turn | A per-unit consecutive-withheld counter at the same K (spec FR-020a). D9.3's "an honest failure arrives in ≤ K+1 adjudications" is true per criterion and false per goal without it |
| 2 | **D6** — greenfield; the engine never overwrites a soul | The install that keeps its old prompt is the one the new controls are calibrated against. A rubric that never asked for a quote cannot satisfy an empty-quote rewrite, so applying that rewrite unconditionally converts **100 % of that population's passing verdicts** into blocked escalations — destroying precisely the installs D6's greenfield stance leaves in place | A second, independent capability sentinel for "does this rubric instruct a quote at all", gating that one rewrite separately (spec C16, FR-024, FR-077, FR-079(c)) |
| 3 | **D6** — operator edits survive | The capability test is a **substring test for shipped strings**, so it fails on every operator who wrote their own modern rubric in their own words — the very operator D6 protects. Telling them their prompt is obsolete and offering only "reset" makes the supported way out *delete the edit D6 exists to preserve* | The report states the missing declarations and quotes the sentinels verbatim, with an add-them affordance alongside reset (spec C17, FR-078) |

**Unchanged by revision 6:** every decision D1 – D11, including D4's decision to accept the
injection surface and D10's four closures. Revision 6 narrows one acceptance to what the controls
deliver, widens one closure to what its mechanism governs, and names three bounds the decisions
imply. It withdraws nothing.

## 8. Revision 7 — the Judge is claim-triggered and runs after delivery

### Operator direction (verbatim, 2026-09-09)

> so yes the judge should run after delivery, when the working llm pauses we only have to repost the goal like the ralph loop is doing it so it continues

> yes lets make the claim a tool call and only run the judge at the end on completion

### Why: four harnesses, no counterexample

Research into how comparable systems decide a goal is done (`openclaw/openclaw` @ `4a2bc10d`, `NousResearch/hermes-agent` @ `9e6c4100`, Claude Code's shipped tool schemas @ 2.1.266, `OpenHands/software-agent-sdk`) found a sharp and unanimous distribution:

| System | Who decides completion | Verifier tools | On the user's critical path |
|---|---|---|---|
| OpenClaw | the acting agent (`update_goal`, status enum `complete`/`blocked`) | none — there is no judge | no judge at all |
| Hermes | separate judge, `DEFAULT_JUDGE_TIMEOUT = 30.0` s | **none** | **no — runs after delivery** |
| Claude Code | the acting agent (`TodoWriteInput.status`), plus the human reading it | n/a | no completion judge exists |
| OpenHands | a critic; cheap deterministic ones first, LLM one is a single `/classify` call | none | inline but one call |

- **Multi-minute verification is normal.** **Tool-using verification is normal.** A **tool-using LLM verifier on the user's synchronous critical path has zero precedent** in the sample.
- The only inline LLM judge found anywhere is OpenClaw's command-safety reviewer: `DEFAULT_EXEC_REVIEWER_TIMEOUT_MS = 30_000`, `EXEC_REVIEWER_MAX_TOKENS = 360`, temperature 0, **no tools**, and on timeout it fails open to `ask` (`buildReviewerTimeoutDecision`) rather than stalling.
- Hermes refuses to make the user wait even 10–40 s: its gateway hook is documented "Run the goal judge after a gateway turn (AFTER delivery)" with the reason inline — the call "would block Discord heartbeats".
- The heavyweight tool-using reviewers that do exist (Hermes `/review`, Claude Code's `/code-review` at its deepest tier) are **always** background, cancellable, or in the cloud. Hermes states the invariant outright: "self-improvement work must never block a user-facing turn."
- Where minutes are spent, it is on **deterministic execution of the project's own tests** (`agent/verify/runner.py`, `DEFAULT_PHASE_TIMEOUT = 600.0`), user-invoked — the user asked to wait.

Revision 6's design — a tool-using Judge, up to 420 s per attempt, synchronous inside the operator's chat turn — is the one shape nobody ships.

### D12 — Completion is claimed by a tool call, not by a text marker

Today a claim is prose: a `[goal:evidence] <what you verified>` line immediately followed by `GOAL_STATUS: met`, recovered by `pkg/agent/task_completion_signal.go::parseGoalStatusMarker` with fenced-code exclusion, last-occurrence-wins, and an unrecognised value treated as no claim. The machinery is careful precisely because the input is unreliable, and the G-4 bare-claim bounce exists to absorb the common failure of claiming without evidence.

A new tool — working name `goal_claim` — replaces detection with arrival:

- Arguments: `status` (enum: `met` | `blocked` | `waiting_on_user`) and `evidence` (required, non-empty, for `status: met`).
- A claim of `met` with no evidence is **rejected by the schema at the call**, not bounced after the fact. The G-4 bounce economics are retired for the tool path.
- Identification is no longer pattern matching: either the call arrived or it did not. No fenced-code rule, no last-one-wins, no positional adjacency requirement.
- Per Constraint #6 / ADR-077 it is a static builtin: an entry in `coreagent::allStaticToolNames`, a seeded default in `pkg/config/defaults.go`, and per-agent seeding.
- **The text markers keep working**, unchanged, as a fallback for a model that types them anyway. They are no longer the primary signal. `parseGoalStatusMarker` is not deleted.

### D13 — The Judge runs only on a completion claim, and only after delivery

- **A claim is the sole trigger.** `status: met` (by tool call or by the fallback marker) is what starts an adjudication.
- **It runs after the answer has been delivered to the operator**, not inside the turn they are waiting on. The Hermes precedent is exact.
- **Silence is not a claim.** The claimless adjudication fired by the 60 s quiet window (`goal_triggers.go`'s "goal idle settle: firing claimless adjudication after quiet window") is **removed**. A stalled or quiet agent is a *pause*, and the answer to a pause is to re-post the goal so work continues — the Ralph-loop behaviour the keeper already implements. No Judge call, no round consumed.
- **`waiting_on_user` parks with no adjudication and no round consumed**, as today.
- Because adjudication is now rare and off the critical path, D1's tool-using Judge costs the operator no latency. The operator's direction that the Judge reason with common sense and read files is **unchanged and preserved**; only its trigger and its position move.

### D14 — Evidence before judgement, for every kind of task (revised)

> **Operator correction, 2026-09-09:** *"the whole exit code mechanism again i'm not a fan of, it works only for coding — the mechanism must be primary done for all kind of tasks not only coding"*

The first draft of D14 adopted Hermes's shell gates literally: a criterion carries a command, the exit code is the verdict. **That was a bad borrow.** Hermes is a coding harness, so `pytest` and `tsc` are the natural evidence there. Omnipus is a general-purpose agent: goals are as often "email the supplier and get a quote", "book the flight", "research three competitors", "produce the board deck" as they are "make the tests pass". A shell exit code decides none of those, and making it the primary path would bias goal authoring toward coding-shaped criteria — the same incentive defect revision 6 found in the clause splitter (§7 F4), arriving through a different door.

**The general principle is not "run a command". It is: prefer evidence the system already holds over asking a model to opine.** Restated for any task:

**Tier 1 — the agent's own tool-call record (free, universal, primary).** Every tool call in the turn is already persisted with its arguments, its result and its success (`session.ToolCall{Tool, Parameters, Result, Status}`; see the ADR-066 emptying note in §7 for a caveat about mid-turn rewrites). That record is the general form of Hermes's evidence ledger, which is deliberately passive — its own docstring: *"never runs a suite, never blocks completion"* — but generalised past shell to **every** tool this product has. A criterion of the shape "the supplier was emailed" is answerable from the fact that the mail tool returned success with that recipient; "a calendar hold exists" from the event id the calendar tool returned; "the page was published" from the publish call's response. No shell, no exit codes, no new machinery — the evidence is a by-product of the work.

**Tier 2 — a cheap targeted check, where one exists and is meaningful (optional, never required).** A file exists and is non-empty; a document has the sections the criterion names; a URL returns 200; a record is present in the store the agent wrote to. `judge.go::planArtifactCheck`'s existing file test is one instance of this tier, not the definition of it. A shell command is **one possible check among several**, available when a goal genuinely is about code — never the assumed shape.

**Tier 3 — the Judge, with file access and common sense (D1, unchanged).** Everything that is genuinely a judgement: whether the deck is persuasive, whether the research is thorough, whether the tone is right, whether "cutting-edge UI" was achieved. This is the operator's stated reason for the Judge to exist and it is untouched.

**The deterministic check is optional and reserved for coding tasks (operator directive, 2026-09-09):**

> *"well we need both — omnipus is also a coding agent, but we need to make sure that exit code criteria are only used on coding tasks"*
> *"the deterministic check is optional reserved for coding tasks"*

Tier 2 as a whole — running anything to verify — is **optional, opt-in, and reserved for coding work**. It is never the default path for a goal, and nothing infers it. Three rules, with no task-type classifier anywhere (a classifier guessing "is this a coding goal?" would be a new silent failure source, and it is unnecessary):

1. **A verification command runs only when the criterion explicitly declares one.** The existing structured check (`task.KindCheck`) is that declaration. Declaring one is a deliberate authoring act, so a goal about booking a flight or writing a deck simply never carries one, and tier 2 never engages for it. That is what makes "reserved for coding tasks" true without anyone having to decide what a coding task is.
2. **A check MUST NOT be synthesised from a criterion's prose.** This retires the inference in `judge.go::planArtifactCheck`, which today builds `test -f && test -s && grep -qF` by pattern-matching criterion text. Under this directive that inference is removed as a default path: a file test may still be *declared*, but it is no longer *invented* from wording. This also closes revision 6's F1 second half, where a zero-exit file check was demoted to advisory while nothing required a `met` to be consistent with what it proved — there is now nothing inferred to be inconsistent with.
3. **Exit-code evidence the agent produced itself is tier 1, not tier 2.** If the working agent ran the test suite during its work, that `bash` call is already recorded with its command, output and status, and is admissible like any other tool-call evidence. No verification step runs it again. A non-coding goal produces no such calls, so no exit code can be consulted for it — again by construction rather than by rule.

So: **tier 1 is universal** (whatever the agent did, whatever the task), **tier 2 is optional and coding-shaped** (declared checks only, never inferred), and **tier 3 — the Judge — is universal and is the default for everything else.** For a deck, a report, an email or a booking, the path is tier 1 then the Judge, with no check in between.

**Two constraints that follow, and are binding:**

- **A criterion MUST NOT be required to carry a check.** Tiers 1 and 2 are opportunistic: the system uses them when the evidence happens to be decisive, and falls through to the Judge otherwise. Requiring a machine-checkable form would push `set_goal` toward criteria that are easy to check rather than criteria that are right — the failure this ADR exists to avoid, inverted.
- **The veto direction stays one-way (D5a).** A tier-1 or tier-2 fact that *contradicts* a `met` claim (the mail tool returned an error, the named file is absent) blocks it. A fact that is merely *consistent* with the claim does not establish it — the Judge still decides whether the criterion is satisfied.

Hermes's ordering principle is kept and generalised: cheap, certain evidence is consulted before the expensive uncertain judgement, and evidence that already disproves the claim means the Judge is never called for that criterion.

### Consequences of revision 7

- The 420 s ceiling stops being operator-visible latency and becomes background cost. **FR-086 (what the operator sees during a long adjudication) is answered by construction** — they see their answer, and a verdict arrives afterwards.
- The withholding-loop non-termination (revision 6's F22 / FR-020a) is materially reduced: adjudications now happen on claims, not on every quiet window, so the pathological "a different criterion is unverifiable each round, forever" loop loses its clock.
- D9's budget work stays, but its urgency drops: a 30 s-class inline call no longer bounds the design, because there is no inline call.
- The spec's §M legacy-rubric analysis is unaffected — it concerns what a rubric declares, not when the Judge runs.

### Open, for the spec to resolve

1. Whether a claim also runs the cheap deterministic gates first (D14) or whether gates run continuously as evidence accrues.
2. What the operator sees when a background adjudication overturns a claim — the verdict must be visible without being intrusive.
3. Whether a second claim arriving while an adjudication is in flight supersedes it or is refused.

---

## 9. Revision 8 — five corrections to §8's claims about the code, and the three open items resolved (2026-09-09)

Spec authoring for revision 7 re-verified every claim §8 makes about this codebase against
`3664137c`. **Five are wrong**, and one of them would delete nine behaviours to remove one. As with
revisions 5 and 6, **no decision is withdrawn** — D12, D13 and D14 all stand exactly as written.
What changes is what the ADR asserts the system currently does, and therefore where each decision
has to be built. `docs/internal/specs/judge-active-reviewer-spec.md` implements the corrected form
and carries the code evidence in its §0 (C22 – C28).

### R8-a — D13's removal, taken literally, deletes the Ralph loop D13 itself depends on (BLOCKER)

D13 says the claimless adjudication "is **removed**" and identifies it as
*"`goal_triggers.go`'s 'goal idle settle: firing claimless adjudication after quiet window'"*, with
`goalQuietWindowSettle` and `goalIdleQuietWindow` named alongside it. **Only the log line names the
adjudication.** The other two are the shared driver and its pacing constant for **nine** keeper
behaviours: `goalQuietWindowSettle` → `maybeSettleGoalIdle` is the single periodic pass over every
goal-bearing session, and the adjudication is one of four terminal branches at the end of it.

Removing the function or the constant also removes: the parked-`AskUserQuestion` suppression
(`goalHasParkedCard`), the `waiting_on_user` suppression (`goalIsWaitingOnUser`), the
fire-once-per-quiet-spell re-arm (`goalIsIdleSettling` / `markGoalIdleFired`), the in-flight
self-race guard (`goalAdjudicationInFlight` — **the guard open item 3 resolves onto**), the
live-turn suppression (`goalHasLiveTurn`), the token-budget brake
(`TokenBudget().Exhausted()` → `clearGoal(FailedReasonBudgetExhausted)`, the only idle-path
termination for a goal past its budget), the ADR-081 D6c recordless nudge ladder and its
`dispatchGoalFallbackCompile` backstop (without which ADR-081 FR-017's "every active goal ends up
judgeable" breaks) — and `settleZeroOutputRecordedGoal` →
`dispatchGoalAsyncFollowUp(goalContinuePushPrompt(…))`, **which is the Ralph-loop re-post D13 says
the keeper "already implements" and instructs us to keep**. It is inside the function D13 names for
removal.

**Corrected form.** The removal is one call: `settleGoalNormally`'s
`runGoalAdjudication(…, claimText: "")`. It is replaced by the re-post the keeper already performs
two branches above it. `goalQuietWindowSettle`, `goalIdleQuietWindow` and `maybeSettleGoalIdle`
stay. Spec FR-095 (the removal), FR-096 (the eight survivors, one test per row), FR-097 (the
re-post). D13's decision is unchanged; its symbol list was wrong.

*(One genuine consequence the spec draws out: FR-014b's zero-output branch and the normal branch now
have identical bodies, so `goalZeroOutputTripleHolds` and `sessionHasTranscriptOutputSince` become
dead and the two branches collapse into one push ladder — spec FR-097.)*

### R8-b — "after delivery" is a reordering in `runAgentLoop`, and today the claim path runs *before* delivery

D13 requires the adjudication to run after the answer reaches the operator and cites Hermes. The
seam is exact and worth naming, because the current order is the opposite:
`pkg/agent/loop.go::runAgentLoop` calls `al.checkGoalLoopAfterTurn(ctx, agent, opts, &result)`,
**then** publishes `result.followUps`, **then** publishes `result.finalContent` via
`bus.PublishOutbound`. The claim-path `runGoalAdjudication` is inside the first of those. So the
Judge already runs on the operator's critical path *and before their answer is published* — a
slightly worse position than §8 assumes.

Two mechanical consequences §8 does not state, both of which make the move more than a line swap:

1. **The claim path's `deliverSteer` closure stops working.** It appends to `result.followUps`,
   which `runAgentLoop` publishes immediately after `checkGoalLoopAfterTurn` returns — before a
   deferred adjudication has produced anything. The deferred path must use the tick-path deliverer,
   `idleSteerDeliverer` → `dispatchGoalAsyncFollowUp` → `asyncNotifier.Notify`, which already stamps
   `SenderCanonicalID: goalLoopFollowUpSenderID`. That sentinel is load-bearing, not cosmetic:
   `checkGoalLoopAfterTurn`'s origin gate accepts only a `UserInitiated` turn or exactly it, and a
   notify stamped with the default `"async:<kind>"` is **silently dropped** — a failure this project
   has already shipped once (ADR-081 D6b, recorded in `dispatchGoalAsyncFollowUp`'s own comment).
2. **The turn `ctx` is the wrong context.** `runGoalAdjudication` derives its timeout from the turn
   ctx, which is finished once the work is deferred. The idle path already builds
   `context.WithTimeout(context.Background(), goalJudgeRoundTimeout)`; the deferred claim path must
   do the same.

Spec FR-098 (the seam and the ordering, with an observed-order oracle rather than a source-order
one), FR-099 (the delivery path), FR-103 (cancellation, which must move to the verifier registry
because there is no longer a chat turn to interrupt).

### R8-c — "the loop loses its clock" is false; it acquires a slower one

§8's Consequences say the FR-020a withholding loop "loses its clock" because adjudications now fire
on claims rather than quiet windows. Traced: a withheld adjudication returns
`JudgeCriteriaResult{Unavailable: true}`; `runGoalAdjudication`'s `Unavailable` branch emits the
`judge_unavailable` pill and returns **without calling `deliverSteer`**, so nothing re-dispatches
from the adjudication. The goal is then quiet with an active goal and an unconsumed round — exactly
the state R8-a's re-post handles. The cycle becomes claim → withheld → quiet window → re-post →
work → claim → withheld, paced by the worker's own turn length rather than by 60 s, with each
iteration still costing a full tool-using Judge turn.

**FR-020a is therefore retained**, with this trace as its rationale. Revision 7's real gain is that
the cost is background rather than latency: the *urgency* drops, the necessity does not. Spec FR-020a
and C25.

### R8-d — D14 tier 1 is partially shipped, and the shipped part cannot answer D14's own examples

D14 tier 1 rests on the tool-call record being *"already persisted with its arguments, its result and
its success"*, and gives three examples turning on a **parameter** or a **result** value: the mail
tool returning success *with that recipient*, the calendar tool's returned *event id*, the publish
call's *response*. Verified:

- The record is real and richer than credited:
  `session.ToolCall{ID, Tool, Status, DurationMS, Parameters, Result, ParentToolCallID, Error, ContentState}`,
  `Status ∈ success|error|pending|denied`.
- **The shipped evaluator over it is count-only.** Rung 2 (`pkg/agent/behavior_scan.go`,
  `task.CriterionBehavior{Tool, MinCount, MaxCount, Scope}`) counts successful calls of a named tool
  and reads neither `Parameters` nor `Result`. It can answer "`send_email` was called at least once"
  and **none** of D14's three examples.
- **The feed into the Judge is worse.** `renderTranscriptEntriesForWindow` renders every call as
  `fmt.Sprintf("[tool_call] %s -> %s", tc.Tool, tc.Status)` — name and status, no parameters, no
  result, no error. That is §2 E3's original defect, still live in the one place tier 1 needs it not
  to be.

**Corrected form, deliberately conservative.** Closing the gap by building an engine that decides
whether `send_email(to: supplier@…)` satisfies "the supplier was emailed" is a
natural-language-to-tool-call matcher — **D14 rule 2's retired inference, relocated one tier down**,
with the same failure mode (a criterion's wording silently deciding what gets checked). The spec
does not build one. It (a) widens the feed to `{tool, parameters, status, error}`, bounded and
redacted, so the Judge reads facts rather than a name and a colour (FR-105); (b) keeps tier 1's
*deciding* power exactly where it already is, a **declared** `KindBehavior` payload (FR-107); and
(c) gives tier 1 a narrow, closed **contradiction veto** — the one direction D14's second binding
constraint names and the only one where a false positive is safe (FR-108). Widening
`CriterionBehavior` to match parameters is named as a separate decision so it is not smuggled in.

### R8-e — tier-1 evidence is erasable, and the erasure looks exactly like absence

§7 R5-c established that `session.ToolCall.Result` is overwritten in place by
`empty_in_place.go::recordEmptiedOnTranscript` (ADR-066 D5). Under revision 6 that governed only the
Judge's own grounding. Under D14 it governs **tier 1**, and the failure is worse there: a rewritten
`Result` read as "the tool returned nothing" would **contradict** a true claim and veto it — E1's
failure reintroduced through the newest control, on precisely the long turns most likely to have
done real work.

Two facts make it fixable: the update written is
`session.ToolCallProjectionUpdate{ToolCallID, ContentState, Result}`, so **`Tool`, `Parameters`,
`Status` and `Error` are never touched**; and `ContentState == "emptied"` is self-declaring. Spec
FR-106: build tier-1 facts from the durable quadruple, use `Result` only when `ContentState` is
empty, and treat `emptied`/`capped` as **inconclusive, never a contradiction**.

**A related note, because a plausible shortcut is wrong.** Tier 1 must **not** be sourced from the
spec's FR-030 in-memory capture. That capture holds the **Judge's own** results during the verifier
turn; tier 1 needs the **worker's** calls, in a different session, in a turn that under D13 ended
before the adjudication was scheduled. There is no capture spanning that boundary and, under the
spec's no-stateful-change deployment rule, there must not be one. Tier 1 reads the persisted
transcript, and R8-e is what makes that re-read safe. Spec C28, FR-105.

### The three open items, resolved

| # | Open item | Resolution | Why |
|---|---|---|---|
| 1 | gates on the claim, or continuously as evidence accrues | **Once, on the claim** (spec FR-104) | Under D13 there is exactly one moment at which anything is being decided, so facts gathered at any other moment have no consumer. A continuously-running gate is a timer, which D13 forbids on this path — and a red gate firing between claims would be a claimless verdict under another name. Tier-1 evidence is a by-product of work already done: reading it early costs the same and can only be less complete. |
| 2 | how an overturning verdict is visible without being intrusive | **A distinct `claim_overturned` `GoalStatusFrame.state`, plus the existing `judge_verdict` transcript entry and the ordinary async steer. No modal, toast, notification or navigation** (spec FR-102) | Every surface needed already ships; none of them interrupts. The one case needing more than the ordinary unmet pill is exactly this one — the operator read "done" and the system disagrees — so it gets its own state rather than being folded into an unmet round. The deliberate cost, recorded in the spec as A-23: a card state does not reach an operator who has closed the tab, and a notification is not taken. |
| 3 | a second claim during an in-flight adjudication — supersede or refuse | **Refused** (spec FR-101), reusing the two shipped layers `goalAdjudicationInFlight` and `verifier_registry.Register`'s `ErrVerifierSessionHeld`; **what changes is the reporting** — under D12 a claim is a tool call, so the refusal is returned *in the tool result* rather than logged and the claim dropped silently | Superseding means cancelling a Judge turn that has already spent tool calls and tokens, on the say-so of the party being judged — a worker could cancel a pending verdict by re-claiming. Refusal costs one redundant tool call and preserves the "exactly once" invariant the CAS was written for. The spec also resolves the item's unasked half: a **new operator message** during an in-flight adjudication is never blocked, and a verdict for a criterion id a concurrent `set_goal mode:update` removed is discarded with a WARN (FR-100). |

**Unchanged by revision 8:** D1 – D11 in full, and D12, D13 and D14 including D14's three rules and
its two binding constraints. Revision 8 corrects five factual premises, relocates two mechanisms,
and records the three resolutions. It withdraws nothing.
