# ADR-086 — A goal is its own entity, with a definition phase and an active phase

- **Status:** Proposed (awaiting ratification) — 2026-09-09
- **Relates to:** ADR-081 (work-first goal flow), ADR-084 (the Judge as an active reviewer), ADR-049/ADR-052/ADR-055 (task and plan adjudication), ADR-080 (criterion types and DoD provenance)
- **Changes the ground under:** `docs/internal/specs/judge-active-reviewer-spec.md`, which currently assumes a chat goal lives on the session
- **Spec:** `docs/internal/specs/goal-entity-spec.md` *(to be written)*

## 1. Operator direction (verbatim, 2026-09-09)

> plan might be slightly different but task and chat must be identical — the only difference is that the task makes the goal visible in the ui, the chat not. that must be the only difference

> no the chat does not become a task, that has more implications. we need only to separate the goal. what they have in common is from the time the goal is set until end of judgement — that part is the same

> that is right, but on task level when the task is started it has its own session, so here it becomes again the same. the task holds / references the goal definition until the session is started, so it is really identical — only how the goal is set is different

## 2. Evidence: today there are two implementations of one concept

A chat goal is **fourteen fields hung on a session** (`pkg/session/unified_meta_files.go`); a task is a stored entity with its own record (`pkg/task/task.go`). They express the same ideas twice:

| Concept | Task | Chat goal |
|---|---|---|
| Identity | `id` | `goal_id` |
| What is wanted | `title`, `description` | `goal_condition` |
| Criteria | `criteria []AcceptanceCriterion` | `goal_criteria` — **a JSON string** |
| Attempt budget | `attempt_count` | `goal_rounds_used`, `goal_max_rounds` |
| Status | `status` | derived, plus `goal_latest_reason` |
| Owner | `agent_id` | `goal_route_agent_id` |
| Workspace | `workspace_id` | absent; resolved elsewhere |
| Where it lives | its own record | session metadata |
| Reachability | not needed | `goal_route_channel`, `goal_route_chat_id`, `goal_route_session_key` |

Three of today's defects are consequences of that split, not independent bugs:

- **The routing fields exist only because a goal has no identity.** It must remember which chat connection to address, which is precisely what failed on reconnect (ADR-082 §2): the stored `goal_route_chat_id` is a per-connection ephemeral id, so every keeper follow-up after a reconnect targeted a dead connection.
- **The criteria are a serialised blob**, which is why superseded-criteria history had to be added by hand (ADR-084 §6, fix-wave GX-B "fix 5b") and why a verdict has nowhere natural to attach.
- **The judging engine already treats the two as one.** `pkg/agent/judge.go::JudgeCriteria` takes a scope of `task` / `plan` / `goal` and one criteria list; `runGoalAdjudication`, `TaskExecutor.adjudicateClaim` and the plan engine all call it. Only the *storage* diverges — and the input type's own validation forbids carrying a `TaskID` and a `GoalSessionID` together, which under this ADR becomes wrong (a running task's goal legitimately has both).

**A task run already mints a session** (`pkg/agent/task_executor.go` sets `t.SessionID`). So the operator's model is already half-true in the code; it is simply not expressed as a concept.

## 3. Decisions

### D1 — A goal is an entity, owned by exactly one owner

A `Goal` is stored in its own right, not as fields on a session and not as fields on a task. It carries **everything that is identical from the moment the goal is set until judgement ends**:

- identity, and the owner reference
- the condition (what is wanted)
- `criteria []AcceptanceCriterion` and `dod []AcceptanceCriterion` as **real lists**, using the existing shared type (ADR-080), never a serialised string
- the attempt budget and attempts used — **one pair of counters**, replacing today's `attempt_count` on a task and `goal_rounds_used`/`goal_max_rounds` on a session
- status, the latest reason, the claim, the verdict, and the superseded-criteria history
- `started_at`, `last_activity_at`
- the session it is active in, once it is active

### D2 — Two phases: definition, then active

- **Definition.** The goal exists with its criteria and budget, and is not running. A task holds a goal in this phase from creation until the task starts.
- **Active.** The goal is bound to a session and the loop runs: work → claim → judgement (ADR-084 revision 7).

**In chat, the two phases collapse:** `/goal` creates the definition and activates it in the current session in one step (ADR-081 D1's instant activation is unchanged). **On a task, they are separated in time**: the definition is authored up front, dormant, and activates when the task starts and mints its session.

After activation the two are **identical** — same loop, same claim tool, same Judge, same budget accounting, same verdict. This is the operator's stated invariant and it is the point of the ADR.

### D3 — The only difference is how the goal is set, and therefore who can see it before it runs

A task's goal is authored ahead of time and is therefore visible in the UI as part of the task. A chat goal is created at the moment of use and has no pre-run existence to display. **Visibility is a property of the owner, not of the goal**, and no other behavioural difference is permitted. Any future divergence between the two paths after activation is a defect, and the spec MUST carry a test that fails if one appears.

### D4 — The owner keeps what is genuinely its own

A task keeps its dependencies, assignee, board placement, plan membership and lifecycle. A chat session keeps its transcript and its connections. Neither becomes the other; the operator ruled that out explicitly.

### D5 — Criteria live on the goal, not duplicated on the owner

A task's `Criteria` field becomes a reference to its goal rather than a second list. Two lists of criteria on one task is exactly the duplication this ADR removes. **Plan is deliberately out of scope**: a plan's `DoD` sits above tasks and may stay as it is (operator: "plan might be slightly different").

### D6 — Most routing fields dissolve

With an identity to address, a goal no longer needs to remember a channel, a chat id and a session key. ADR-082 already made webchat delivery session-addressed; what remains is the owner reference plus the active session. The spec MUST enumerate which of the four `goal_route_*` fields survive and why, rather than assuming all four go.

### D7 — Greenfield

Consistent with the operator's standing directive (ADR-084 §D6, "assume greenfield, remove migrations"): no migration of existing goals is built. Goals in flight at upgrade are not carried across. This MUST be stated in the spec's deployment section rather than discovered.

### D8 — Goal progress becomes visible: the verdict is projected onto criterion status (operator-approved)

> *"yes to make goal progress visible"* — operator, 2026-09-09

`pkg/task/criterion.go` defines and validates three statuses — `CritPending`, `CritMet`, `CritUnmet` — and **only the first is ever written**. A repository-wide search finds 14 writers of `CritPending` and **zero** non-test assignments of `CritMet` or `CritUnmet`. Every criterion therefore renders as pending in the UI regardless of what the Judge decided, at **task and plan level as well as chat**. The verdict is produced, persisted and logged; it simply never reaches the tick marks anyone looks at.

This is a pre-existing defect, not one this ADR introduces, and it is the reason goal work has felt opaque: even a correct verdict was invisible.

- When an adjudication records a verdict, each criterion's `status` MUST be set from that criterion's outcome — `met` → `CritMet`, `unmet` → `CritUnmet` — and `unable_to_verify` (ADR-084 D2a) MUST be representable rather than collapsed into `unmet`.
- The writer runs where the verdict is recorded, so it covers **all three scopes at once**; it is not a chat-only fix.
- The goal entity (D1) is the natural home for this: today a chat goal's criteria are a serialised string with nowhere to write a per-criterion result back to.
- Constraint #8 applies: `AcceptanceCriterion.status` is a wire type with **four** copies — `AcceptanceCriterion.yaml`, `AcceptanceCriterionInput.yaml`, and two hand-synced inline duplicates inside `contracts/asyncapi.yaml` for `GoalStatusFrame.criteria[]` and `.dod[]`, which generate `status: z.enum(["pending","met","unmet"])` under `.strict()` (verified at `src/lib/api/generated/_asyncapi-zod-schemas.generated.ts:837` and `:867`). `GoalStatusFrame.yaml` warns in its own description that edits must be mirrored there. **Missing either inline copy drops every goal-status frame at the SPA edge — the goal card blanks in production with a green `make verify-contracts`.**

## 4. Consequences

- One counter pair, one criteria list, one status vocabulary across chat and task.
- A verdict has somewhere to live. This is the natural home for the fix to the defect found on 2026-09-09: `task.CritMet` / `CritUnmet` are defined and validated but **never written** — 14 writers set `CritPending` and nothing else — so every criterion renders as pending regardless of any verdict, at task and plan level too.
- `JudgeCriteriaInput`'s rule that `TaskID` and `GoalSessionID` are mutually exclusive becomes wrong and must be revisited.
- ADR-084's spec must be re-based: it currently assumes the chat goal's session-meta storage, including where the claim, the verdict and the superseded-criteria history are written.
- Blast radius is the whole goal surface, not just chat. That is the argument for doing it before implementing ADR-084, not after.

## 5. Open questions for the spec

1. Where a goal is stored — its own entity store (the `pkg/entity` precedent, ADR-054 D3) or alongside tasks — and what its id namespace is.
2. Which `goal_route_*` fields survive D6, field by field, with the reason.
3. Whether a task may hold more than one goal over its lifetime (a re-run after failure: new goal, or same goal with a fresh attempt count?).
4. What happens to a goal whose owning task is deleted, and to one whose session is swept by retention.
5. Whether the plan's `DoD` should later converge on the same entity, or stay as it is (D5 defers this deliberately).
