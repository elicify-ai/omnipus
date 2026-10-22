# Crash recovery RED plan

## Behaviour and independent oracle

A crash must not lose known completed effects, replay retired room demands, revive an already-ended ceiling execution, or turn a task Retry into a timer without a run. Uncertain writes require code-owned read-only proof or an explicit uncertainty hold.

All expectations below derive from `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/crash-recovery/DESIGN.md` (2026-10-22), especially Decision sections 1–5 and Deterministic RED strategy. Founder decisions: Q13=A, Q14=Interrupted, Q15=B. The earlier TRIAGE alternatives are not the oracle. Baseline is c90310f1febd3516860c937fb8a887d7b582865e.

## Boundary

REAL: temporary-home session/lifecycle/inbox/task/room stores, ordinary admission, native loop, completion/outbox, boot recovery, gateway task PATCH, launcher, builtin file tools, context assembly/provider group validation and audit.
FAKE: scripted provider at the external model boundary; explicit channels at admission/tool/result cuts; fixed timestamps/boot epochs where exposed. No real user data, provider network, sleeps, elapsed-time oracle, mocked recovery selector, mocked launcher as the final A proof, or production edits.

The provider must capture the actual outgoing request; inspecting only a projection helper is insufficient for the principal B/D oracles. Count exact runs/admissions/effects and wait for the producing goroutine to finish. Tests use temp HOME and OMNIPUS_HOME. Narrow named Go commands run serially through the shared slot runner with CGO_ENABLED=0, tags goolm,stdjson, -p 1, -count=1 and -v.

## Case table (planned, not execution claims)

| Requirement | Inputs / equivalence classes | Exact expected result | Source |
|---|---|---|---|
| A fresh Retry | Real Board-style status PATCH after boot closes a standalone attempt but retains its binding; zero and two busy helper controls | Exactly one new manual run, different session/execution, one new effect; old run/chat unchanged | Decision 4; RED A |
| A fences | Repeated/concurrent Retry; capacity queue; launch refusal/rollback failure | No second winning attempt; queued work eventually admitted on release; visible refusal and no false In Progress success | Decision 4; RED A |
| A route controls | Repeating task; stopped plan/member | Existing separate refusal/continuation paths unchanged | Decision 4 |
| B/Q13 human admission | Restart-stopped ordinary main receives fresh server-authenticated human H, no direct-main J | One wake/execution for H; not Held; no old request execution | Decision 3; RED Q13 |
| B authority negatives | Intentional/accepted-unlanded/in-flight Stop, agent traffic, forged human prose, duplicate/old ID, pause/removal | No human revival authority; intentional Stop remains binding; deduplication intact | Decision 3; RED Q13 |
| B request retirement | Consume G, complete G1, leave derived narrative/reminder, crash/reopen, new H/J/helper entry | Actual provider input excludes G instruction/unfinished derived text/reminder, retains completed effects and independent controls/inputs; old answer refused | Decision 3; RED Q13 |
| B persistence | Snapshot, trim/reload, room context recall, old-derived helper continuation | Retirement survives every assembly; no old-helper auto-resume; raw archive/file history retained | Decision 3; RED Q13 |
| C/Q14 ceiling | Real small native ceiling; parent outcome; same-boot second ceiling | Frozen Interrupted generation, reason tool_limit, no FailedReason/NeedsInput, one nonfatal parent outcome; second outcome is distinct | Decision 1; RED Q14 |
| C boot and controls | Two reopened boot epochs; genuine in-flight native helper; task-origin/intentional Stop/completed controls | Zero ceiling helper executions; one genuine in-flight helper resume; no unrelated unwanted revival | Decision 1; RED Q14 |
| C explicit continuation | Direct human/authorized parent vs ordinary report/unauthorized input | Same session G+1, fresh execution; old outcome/outbox preserved; report cannot revive | Decision 1; RED Q14 |
| C completion races | Stop accepted first, tree Stop through ended helper, commit/publication faults | Stop wins selected execution; active descendant stopped without rewriting ended helper; failures visible/pending | Decision 1; RED Q14 |
| D siblings | Batch declares A/B; A actual append + durable result; B missing, repeated boot/assembly/reload | First actual resumed request retains A declaration/result, truthful B recovery observation; valid provider group; final bytes A then B once | Decision 5; RED D |
| D identities | Later occurrence reuses call ID; two intentional identical appends; unrelated control | Occurrence-scoped recovery; both new intentional effects allowed; original addresses/history retained | Decision 5; RED D |
| D audit | Missing result with no approval evidence; real denied approval positive control | Unknown is tool.recovery.interrupted / error, never Deny; proof observation omits Decision; actual Deny retained | Decision 5 |
| Q15 append/write/edit cuts | Each real adapter: before write, after replacement before result, after result | Exact distinct preimage => effect_absent / new authorized redo allowed; exact postimage => effect_present / never replay historical call; known result retained | Decision 2; RED Q15 |
| Q15 byte boundaries | Absent vs empty; existing-file refusal; identical text; unique vs nonunique edit; no-op | Full-file existence/length/hash proof, not substring/tail; indistinguishable state Unknown | Decision 2; RED Q15 |
| Q15 proof negatives | Missing/corrupt/unwritable witness; read denial; changed target/mount/symlink; competing writer; unsupported mutator | Interrupted/reconciliation_required; zero subsequent automatic writes even if model says checked | Decision 2; RED Q15 |
| Q15 hold persistence/authority | Reboot, unrelated room traffic, old control, fresh task Retry vs later explicit authorized continuation | Hold survives and cannot be bypassed by fresh binding; only new authorized acceptance after report allows a new execution; evidence remains Unknown | Decision 2; RED Q15 |
| Wire/reachability | Generated records/live done/state/end/REST/replay/Activity/Open | Interrupted reason parity; required reason for newly interrupted outcome, forbidden on other outcomes; no Failed/spinner/restart copy; explicit continuation reachable | Decision 1 contract table; RED Wire |

Negative/error/state-control cases exceed 30% of planned cases. Numeric boundary of the ceiling is checked around the chosen small budget (including exact exhaustion and ordinary final answer before exhaustion); task helper occupancy is 0/2/at-capacity. No duration or file-size threshold is being specified here. Unknown/corrupt/absent inputs are first-class cases, not skipped tests.

## Approved expectation replacements

The design explicitly supersedes Running at the ceiling in TestCompletion_Disposition_PersistedAndValidated and TestCompletion_IterationLimit_NonFatal_WakesParent, and successful-sibling omission in orphanACReload. Preserve their independent controls. Run caller sweeps before modifying these existing test symbols because GitNexus MCP discovery in this task returned no tools. Do not modify other expectations to fit observed output.

## CHECK mutations (deferred; never claimed in RED)

| Mutation | Oracle that must die |
|---|---|
| Restore empty-binding Retry guard / reuse dead session | A new-run/session/admission count |
| Treat every stopped room member alike / let agent text impersonate human | B fresh-H admission and intentional/agent controls |
| Leave consumed G in active input / discard all history | B provider-input exclusion and completed/unrelated preservation |
| Leave ceiling Running / boot-resume Interrupted / allow report revival | C durable state, two-boot execution count, continuation authority |
| Remove completed sibling / reuse ID globally | D first provider request and occurrence controls |
| Classify absent result as Deny | D audit classification with real-Deny control |
| Treat substring as postimage / absent as empty / trust model checked | Q15 full-image, boundary and tool-dispatch hold tests |
| Let Retry/new boot clear unresolved hold | Q15 persistence and fresh-binding tests |

## Known limits and blocked handling

The new interrupted lifecycle constant/reason fields, file-effect witnesses and reconciliation implementation do not exist at the stated baseline. Prefer existing entry points and decode serialized records where that keeps assertions runnable; never count a compile failure as RED. If a required executable boundary cannot be exercised without a missing spec element, add a loud t.Fatal("BLOCKED: ... not implemented — required by ...") test and report that gap separately from assertion-level RED. No invented production API or witness format.

This pack is RED only. GREEN and mutation proof are deferred to an independent CHECK instance. Full suite/race/platform/contracts and exact-commit browser UAT are dispatcher/CI gates, not local executions. UI documentation remains the implementing lead's work. Any uncovered design row must be explicitly reported, not silently counted as done.

## Completion record

Pending execution. Final case-to-test mapping, direct exit receipts, scope self-check and any remaining gaps will be recorded in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/crash-recovery/RED-REPORT.md`.
