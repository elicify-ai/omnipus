# Adversarial review — Browser ownership: workspace-scoped browsing contexts (ADR-072 D1)

- **Spec reviewed:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf/docs/internal/specs/browser-workspace-ownership-spec.md` (649 lines, Status: Draft for grill-spec → implementation)
- **Source ADR:** `docs/internal/architecture/ADR-072-workspace-scoped-browser-sessions.md` (862 lines, Status: Proposed, HEAD `5a67157f`)
- **Round-1 ADR review folded in:** `docs/internal/architecture/ADR-072-workspace-scoped-browser-sessions-review.md`
- **Sibling spec cross-checked:** `docs/internal/specs/browser-agent-capability-spec.md` (D2)
- **Worktree:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf` · branch `feat/browser-streaming-performance`
- **Review date:** 2026-08-31
- **Mode:** `plan-spec` (BDD scenarios, FR-xxx ids, traceability matrix, SC-xxx success criteria — full structural checks apply)

---

## 1. Executive summary

Twenty-nine findings: **6 CRITICAL, 11 MAJOR, 9 MINOR, 3 OBSERVATION.**

The spec's central guarantee — "the isolation primitive is unchanged, only its
key changes" — does not hold in the configuration this product ships.
`tools.browser.capture_shared_context` is seeded **`true`**
(`pkg/config/defaults.go:671`), and in that mode `BrowserCoordinator.Register`
returns an **empty** `browserCtxID` (`coordinator.go:349-359`): there is no CDP
browser context per agent today and there would be none per workspace
tomorrow. Every isolation requirement in this spec (FR-003, FR-004, FR-011,
US-3, US-5, SC-002, SC-004, and ADR criteria 5b and 17) is unsatisfiable on a
default install, and the word `capture_shared_context` appears **zero** times
in the spec, the ADR, or the sibling D2 spec.

Three further defects share the same shape the spec was written to eliminate —
a change that looks complete and fails invisibly. Browser tools hold a manager
bound at *registration* time, so re-keying `AgentLoop.browserMgrs` alone leaves
handover broken while every named test passes. The reload prune tests map
membership against `registry.ListAgentIDs()`, so a workspace-keyed map loses
every context — and every login — on every Settings save. `controlledResult`
resolves the human control lock against the literal `defaultSessionID`, so the
take-the-wheel lock stops being honoured and the regression file the spec
nominates to catch it does not exercise that function at all.

The write lease is claimed by **both** specs with **incompatible APIs**. §12 A7
says "if the D2 spec also specs a lease, one of the two must be deleted before
implementation starts." It does. It has not been.

**Verdict: BLOCK.**

| Severity | Count |
|---|---|
| CRITICAL | 6 |
| MAJOR | 11 |
| MINOR | 9 |
| OBSERVATION | 3 |
| **Total** | **29** |

---

## 2. Findings

### CRITICAL

---

#### CRIT-001 — The isolation primitive the whole spec preserves is OFF by default in shipping config

- **Lens:** Incorrectness / Insecurity
- **Affected:** §1 "Solution", §2 impact table (`BrowserManager.browserCtxID`), FR-003, FR-004, FR-011, US-3/AC1, US-5/AC1, SC-002, SC-004, tests 19 and 21, ADR D1.0's comparison table, ADR criteria 5b and 17

**Description.** The spec's foundation is: "The isolation primitive is unchanged
— one CDP browser context per unit, created and owned by `BrowserCoordinator` —
only its key changes." Verified against the code, that is false in the default
configuration.

`pkg/config/defaults.go:671` seeds `CaptureSharedContext: true`. In that mode
`BrowserCoordinator.Register` short-circuits before any context is created:

```go
if c.captureSharedContextResolved() {
    c.mu.Lock(); c.managers[agentID] = mgr; rootCtx = c.rootCtx; c.mu.Unlock()
    logger.WarnCF("browser",
        "coordinator: shared default-context capture mode is ON ... per-agent browser-context isolation is OFF", ...)
    return rootCtx, "", nil          // ← empty browserCtxID
}
```

An empty `browserCtxID` makes `bootstrapBrowserCtx` omit
`WithExistingBrowserContext` (`manager.go:1360-1370`), so **every** manager's
session lands in Chrome's single default browser context — one cookie
partition for the whole process. The config field's own doc comment
(`config.go:3813-3822`) states this in terms: "enabling this REVERSES ADR-043
D2's per-agent cookie/localStorage isolation for every agent … all such agents
share ONE cookie/storage partition. **Default is TRUE**."

**Impact.**
1. FR-003/FR-004 ("login in X invisible in Y") and FR-011 ("the unattended jar
   is a separate CDP browser context") are **not achievable** on a default
   install. `TestBrowsingContext_CrossWorkspaceIsolation` and
   `TestUnattended_HasOwnBrowserContextID` can only pass by forcing
   `capture_shared_context=false` (or `OMNIPUS_BROWSER_CAPTURE_DEFAULT_CONTEXT=0`),
   at which point they assert a property of a non-default configuration and
   prove nothing about the shipped product. SC-002 and SC-004 read as proof and
   are not.
2. ADR criterion 17 ("unattended work is signed out") fails **silently** on a
   default install — the unattended sub-turn shares the default partition and
   is fully signed in. This is exactly the failure D1.2a was added to prevent,
   one layer deeper than D1.2a looked.
3. The escape hatch the config documents no longer exists. Its own comment says
   operators who want real isolation "should set this false … the JPEG
   `browser_screencast` fallback keeps working either way, ADR-047 D3." That
   fallback was **deleted in full** by ADR-061 and is mechanically prevented
   from returning (`scripts/check-no-jpeg-screencast.sh`, present in this
   worktree). So `capture_shared_context=false` now costs the operator the
   entire live video panel. **The product cannot currently offer both
   workspace-scoped browser isolation and a live browser panel**, and no
   document in this stack says so.

**Recommendation.** This is a decision the spec cannot make on its own — take
it back to the ADR. At minimum the spec must:
(a) state the interaction in §1 and §6 explicitly;
(b) add an FR: *"When `tools.browser.capture_shared_context` is true, workspace
isolation is not in effect; `ResolveBrowsingKey` still keys the manager and tab
set, and the operator is warned once per boot that browsing contexts are not
partitioned"* — or the opposite decision, that D1 flips the default and accepts
losing WebRTC capture;
(c) rewrite SC-002/SC-004 to name the configuration under which they hold, and
add a test asserting the **default-config** behaviour so the gap is visible
rather than assumed away;
(d) correct ADR D1.0's comparison table, whose "Partitions cookies /
localStorage / indexedDB — yes — unchanged" row is false for the default
install today, before as well as after this change.

---

#### CRIT-002 — Every browser tool holds a manager bound at registration; the spec never says how a tool reaches the manager for the turn's key

- **Lens:** Incompleteness / Infeasibility
- **Affected:** §2 "Symbols involved" (omits `register.go`), §2 impact table row "session-id argument at every tool", §3 Stream A and Stream C, FR-001, FR-002, US-11, test 14, test 15

**Description.** The spec models the change as *"every browser tool passes a
session id today and every one passes the constant"* — i.e. a parameter
change. It is not. `browser.RegisterTools` (`pkg/tools/browser/register.go:41-84`)
constructs a manager and **binds it into eleven tool structs**:

```go
mgr, err := NewBrowserManager(cfg, ssrf)
...
registry.RegisterReplacing(&NavigateTool{mgr: mgr})
registry.RegisterReplacing(&ListTabsTool{mgr: mgr})   // × 11
```

and `pkg/agent/loop.go` calls it **inside the per-agent registration loop**
against that agent's own registry (`agent.Tools`), then
`mgr.AttachSharedChrome(coordinator, agentID)`. So: one manager per agent,
created before any turn exists, before any workspace is known, permanently
captured in each tool instance's `mgr` field.

Under D1, a *single* tool instance belonging to a *single* agent must reach
*different* managers depending on the turn's resolved key — US-11 makes this
explicit (agent `ray` on workspaces A and B). A bound `t.mgr` cannot do that,
and `pkg/tools/browser` cannot import `pkg/agent` to ask `BrowserManagerForKey`
(that is the import direction). The spec introduces
`AgentLoop.BrowserManagerForKey` and never connects it to a tool's `Execute`.
`register.go`, `RegisterTools`, and the `mgr` field on the eleven tool structs
appear **nowhere** in §2's symbol table, §3's stream ownership, or §9's
traceability.

**Impact.** This is a silent failure with a green test suite. An implementer who
re-keys `al.browserMgrs`, adds `ResolveBrowsingKey`, and changes the session-id
argument satisfies:
- FR-001 (`TestLoop_BrowserManagerForKey_OnePerKey` — asserts the loop's map);
- FR-002 (`TestTools_UseResolvedKeyNotConstant` — asserts no `defaultSessionID`
  reference survives);
- SC-003 (asserts `BrowsingKey` has no other constructor);

…while two agents on one workspace still hold **two different managers**, each
with its own `sessions["ws:W"]` entry and its own browser context. Handover —
the reported defect, SC-001, the entire reason for the ADR — remains broken,
and nothing in §10 catches it, because every unit test resolves through a
manager the test itself supplies.

**Recommendation.** Add the seam explicitly, own it in Stream A, and give it a
requirement and a test:
- Define the interface `RegisterTools` receives instead of a constructed
  manager — e.g. `type ManagerResolver interface { ManagerFor(ctx context.Context) (*BrowserManager, error) }`,
  implemented in `pkg/agent` over `ResolveBrowsingKey` + `BrowserManagerForKey`,
  injected once per agent registry; each tool calls it at the top of `Execute`.
- Add **FR-002a:** *"No browser tool holds a `*BrowserManager` captured at
  registration time; every tool resolves its manager per `Execute` call from
  the turn's context."* Structural test: no `mgr *BrowserManager` field on any
  tool struct in `pkg/tools/browser`.
- Add **an end-to-end test that is red today**: two agents, one workspace, one
  tab opened by agent A, listed by agent B, resolving through the real
  registration path (`registerSharedTools`) rather than a hand-built manager.
  Without that, SC-001 is untested.

---

#### CRIT-003 — The reload prune keys off agent ids; a workspace-keyed map loses every login on every Settings save

- **Lens:** Incorrectness
- **Affected:** §3 Stream A (`loop.go:2857-2866`), §3 Stream F (FR-026 "via the `loop.go:2866` `RemoveAgent` hook"), §5 non-behavior "must not dispose a browsing context on hot reload", FR-028, BDD "reload preserves the workspace login", ADR-043 CRIT-002

**Description.** The spec cites `loop.go:2857-2866` twice as the hook FR-026
reuses for disposal, and never states that its **membership predicate** must
change. Verified (`pkg/agent/loop.go:2849-2871`):

```go
registeredAgentIDs := registry.ListAgentIDs()
stillPresent := make(map[string]bool, len(registeredAgentIDs))
for _, id := range registeredAgentIDs { stillPresent[id] = true }
...
for id := range al.browserMgrs {
    if !stillPresent[id] { removedAgentIDs = append(...); delete(al.browserMgrs, id) }
}
...
for _, id := range removedAgentIDs { coord.RemoveAgent(id) }
```

`RemoveAgent` is, per the spec's own §2 table, "sole disposal path" and calls
`disposeBrowserContextRaw`. Once `al.browserMgrs` is keyed by `ws:<id>` /
`un:<id>`, **no key is ever in `stillPresent`** — that set contains agent ids.
So the first reload after the re-key deletes every manager and disposes every
browsing context.

A second, independent defect in the same block: `RegisterTools` +
`AttachSharedChrome` + the `prior := al.browserMgrs[agentID]` / `coord.Release`
/ `prior.Shutdown()` sequence runs **once per agent**. With N agents on one
workspace, one reload performs N create-and-replace cycles against the *same*
key, each tearing down the manager the previous iteration installed.

**Impact.** "Save a Setting → every workspace is logged out" — the precise
ADR-043 CRIT-002 regression §5 forbids and FR-028 claims to preserve. Its
failure mode is quiet: no error, no crash, just cookies gone. FR-028's test
(`TestReload_PreservesWorkspaceContextAndLogin`) will only catch the second
defect if it uses **two or more agents on the workspace**, and the BDD scenario
specifies neither an agent count nor a real `registerSharedTools` pass.

**Recommendation.**
- Add **FR-026a:** *"The reload prune's liveness predicate is the set of live
  browsing keys — a workspace key is live while the workspace exists and has ≥1
  browser-policy-allowed agent on its CoreTeam; an unattended key is live while
  its sub-turn is running. It is never `registry.ListAgentIDs()`."* Assign it
  to Stream A, not Stream F.
- Add **FR-026b:** registration is **idempotent per key** — N agents on one
  workspace produce exactly one `Register`/`Release` pair per reload. State
  where the per-agent registration loop becomes per-key.
- Amend the FR-028 BDD scenario to specify **two agents on W** and a real
  `ReloadProviderAndConfig`, and add an explicit assertion that
  `disposeBrowserContextRaw` was called **zero** times during the reload.

---

#### CRIT-004 — The write lease is specified twice, with incompatible APIs, and §12 A7's own tripwire has already fired

- **Lens:** Inconsistency
- **Affected:** §1 header ("This spec owns the write lease; the D2 spec must not re-specify it"), §3 Stream D, FR-019…FR-024, §12 A7; and D2 spec §3 Stream F, FR-023, US-14, test 23

**Description.** §12 A7 records the resolution: *"this spec owns it (FR-019…FR-024,
Stream D). The D2 spec must reference these FRs, not restate them. If the D2
spec also specs a lease, one of the two must be deleted before implementation
starts."* The sibling spec does spec a lease, in full and independently:

| | D1 spec | D2 spec (`browser-agent-capability-spec.md`) |
|---|---|---|
| Requirement | FR-019…FR-024 (6 FRs) | FR-023 (1 FR) |
| Owner | Stream D | Stream F |
| API | `func (m *BrowserManager) acquireWrite(ctx context.Context, sessionID, holderAgentID string) (release func(), ok bool, holder string)` | `func leaseWrite(mgr *BrowserManager, sessionID, toolName string) (deferred *tools.ToolResult, release func())` |
| Bounded wait | yes (FR-023, `leaseWaitTimeout`) | not specified |
| Cancellable | yes (`ctx`) | no `ctx` parameter |
| Holder identity | `holderAgentID`, returned as `holder` | `toolName` |
| Return shape | `(release, ok, holder)`, caller builds the deferral | pre-built `*tools.ToolResult` |
| Tests | 6 (`TestWriteLease_*`) + E2E | 1 (`TestLeaseWrite_SecondWriterDeferred`) |
| Call sites | the same 7 action tools | the same 7 + 5 new ones |

These are not two descriptions of one thing; they are two different functions,
in the same package, over the same call sites.

**Impact.** If both land, the 7 action tools acquire two unrelated mutexes and
mutual exclusion is lost for whichever tool takes only one — the failure is
nondeterministic interleaving, which ADR §5 itself calls "the most expensive
kind for an agent". If neither team notices, both merge cleanly: nothing in
either spec's gates detects a duplicate lease. If one is deleted late, whichever
team coded against it rewrites its call sites.

**Recommendation.** Resolve **before** either spec is implemented, and record
the resolution in both files plus ADR D2.10:
- Move the lease out of both specs into its own small spec (or a D1 §-numbered
  annex both reference), with **one** signature. If it stays in D1, the D2
  spec's Stream F, FR-023, US-14, its BDD scenario and test 23 must be deleted
  and replaced with a one-line reference to D1's FR-019…FR-024.
- The surviving API must be the D1 one (it is strictly stronger: cancellable,
  bounded, and names the holder), but it must adopt D2's pre-built-`ToolResult`
  convenience or D2's new tools will hand-roll the deferral shape.
- Add a structural test: exactly one lease primitive exists in
  `pkg/tools/browser` (`grep -c "sync.Mutex" lease.go` is not sufficient — assert
  a single exported/unexported acquire symbol).

---

#### CRIT-005 — `controlledResult` and ~15 gateway call sites still address the literal `defaultSessionID`; the human control lock silently stops working

- **Lens:** Incompleteness / Insecurity
- **Affected:** §2 impact table ("session-id argument at every tool — 9 call sites in `tools.go`, all of `tabs.go`"), §3 Stream D ("composition with `controlledResult`"), §3 Stream E (names three gateway call sites), FR-022, US-9/AC2, US-12/AC2, the "regression: must keep passing" list

**Description.** Two enumerations in the spec are materially short.

1. `controlledResult` resolves the lock against a hardcoded constant
   (`tools.go:962-964`):
   ```go
   func controlledResult(mgr *BrowserManager, toolName string) *tools.ToolResult {
       if !mgr.Live().IsControlled(defaultSessionID) { return nil }
   ```
   The spec assigns Stream D "composition with `controlledResult`" and lists it
   as **extends** in §2 — but never requires its session key to be re-keyed. If
   the live-view registry is keyed by the browsing key and this call is not,
   `IsControlled` returns `false` forever.

2. `browser.DefaultSessionID` is referenced at **~15 non-comment call sites in
   `pkg/gateway/browser_ws.go` alone** — `Live().Attach` (`:1266`),
   `Live().Input` (`:1396`), `TakeControl` (`:1549`), `ReleaseControl` (`:1564`),
   `Controller` (`:1622`, `:1724`), `Detach` (`:1725`), `SwitchTab` (`:1637`),
   `CloseTab` (`:1647`), `OpenTab` (`:1652`) — plus `pkg/config/config.go:3892`.
   Stream E's scope names exactly three call sites, all
   `BrowserManagerForAgent`. None of the above appears anywhere in the spec.

**Impact.**
- **FR-022 and US-9/AC2 fail silently.** A human holding the wheel is no longer
  detected; agent action tools proceed while the operator is typing into the
  same tab. This is a *regression of a shipped safety property* (ADR-038 D6),
  introduced by a spec whose §5 lists "must not change ADR-038 D6 behaviour" as
  a non-behavior.
- **The live panel and the tools address different session entries.** The panel
  attaches, takes control and switches tabs under `"default"` while tools work
  under `"ws:W"` — a panel showing a tab strip that no agent can act on, or
  (with invariant M-2 enforced) a hard error from the panel on every attach.
- **The nominated regression test cannot catch it.** The spec pins
  `shared_control_test.go` — "all nine (`:35`–`:186`). ADR-038 D6 human-control
  behaviour must be **unchanged** by the lease; FR-022 exists precisely so these
  stay green." That file contains **8** tests, and **none of them calls
  `controlledResult`**; they exercise `LiveView` input dispatch, rate limiting
  and viewport failure streaks. The only direct coverage of `controlledResult`
  is `tools_control_test.go` (3 tests: `TestExecute_ControlLock_InteractiveToolsDeferWhileControlled`,
  `_ReadOnlyToolsAreNotGated`, `_ReleaseUngatesInteractiveTools`) — **absent
  from this spec's regression list**, though the round-1 ADR review named it and
  the sibling D2 spec lists it. A green `shared_control_test.go` is a false
  green for exactly this defect.

**Recommendation.**
- Add **FR-002b:** *"Every consumer of `browser.DefaultSessionID` addresses the
  resolved browsing key instead. The constant is deleted, not deprecated."*
  Enumerate the consumers in §2: 11 tools, `controlledResult`, the ~15
  `browser_ws.go` live-view/tab-strip call sites, and `pickWarmBrowserManager`'s
  warm path (see MAJ-006). Make `TestTools_UseResolvedKeyNotConstant` a
  repository-wide structural assertion (zero non-test references to the
  identifier), not a tool-path one.
- Move `controlledResult`'s re-key into Stream A (it is on the resolution path,
  not the lease path) and give it its own AC.
- Correct the regression list: `shared_control_test.go` is **8** tests and is
  *not* the FR-022 guard. Add `tools_control_test.go` and state that its three
  tests must be re-run against the re-keyed control lock.

---

#### CRIT-006 — Unattended browsing contexts have no disposal path; every delegated sub-turn leaks a CDP browser context and a manager

- **Lens:** Incompleteness / Inoperability
- **Affected:** FR-025, FR-026, US-12/AC3, US-12/AC4, §6 "Integration boundaries" ("the sub-turn axis is new and is what FR-025's reaping must actually bound"), §3 Stream F

**Description.** §6 correctly identifies that "the count of live contexts now
scales with **workspaces + concurrently-unattended sub-turns**" and asserts
that FR-025's reaping bounds it. It does not. Verified:

- `ReapIdleSessions` (`manager.go:2986-3060`) deletes entries from
  `m.sessions` and cancels tab contexts. It never touches the manager, never
  calls the coordinator, and never disposes a browser context. A manager whose
  only session was reaped stays registered in `c.managers` with its
  coordinator-owned context intact.
- FR-026's disposal trigger is "workspace deletion / roster removal", routed
  through the `loop.go:2866` prune. An unattended key (`un:<transcriptID>`) is
  not an agent, is not in any workspace's roster, and is never enumerated by
  that prune under any predicate the spec names.
- US-12/AC4 says "when the sub-turn ends and its tabs go idle, it is reaped like
  any other" — conflating **tab** reaping (which happens) with **context and
  manager** disposal (which does not).

**Impact.** Every delegated unattended sub-turn that touches the browser mints a
new `BrowserManager` and a new CDP browser context that persist for the
gateway's lifetime. ADR-043 sized the hybrid at ~10 browser-using contexts
≈ 1.5-2 GB. An install where an Orchestrator delegates research tasks
repeatedly reaches that in an afternoon. The failure mode is memory growth with
no error and no log line — nothing in §10 or §11 measures live context count
over time.

**Recommendation.**
- Add **FR-026c:** *"An unattended browsing key's manager and CDP browser
  context are disposed when its sub-turn terminates (success, failure, or
  cancellation), via the same `RemoveAgent`/`disposeBrowserContextRaw` path.
  Disposal is driven by sub-turn lifecycle, not by tab reaping."* Own it in
  Stream B (which creates the key) rather than Stream F.
- Add a test to §10: run K delegated unattended sub-turns to completion and
  assert `coordinator.contextCount()` and `managerCount()` return to their
  pre-run values. This is the only assertion in the plan that would fail on a
  leak.
- State the bound in SC: *"live browsing contexts ≤ (workspaces with a live
  context) + (currently-running unattended sub-turns)"*, and make it an
  assertion, not prose.

---

### MAJOR

---

#### MAJ-001 — `ResolveBrowsingKey`'s ladder cannot be evaluated in the order it specifies

- **Lens:** Infeasibility / Ambiguity
- **Affected:** §3 shared-interface contract (`resolve.go` doc block), FR-007, FR-009, FR-010, tests 1/3/4, §10 dataset rows 4-7

**Description.** The ladder is declared "evaluated in order", with step 1:

> 1. UNATTENDED DELEGATED SUB-TURN → `BrowsingKeyUnattended(transcriptSessionID)`
>    Conditions, BOTH required: `tools.ToolSubTurn(ctx)` is true, AND **the
>    candidate workspace jar** has zero attached viewers (`Viewers()==0`).

The "candidate workspace jar" is the output of steps 2-3, and `ViewerCounter`'s
signature is `Viewers(k BrowsingKey) int` — it requires a `BrowsingKey`, which
by SC-003 only `ResolveBrowsingKey` may construct. So step 1 cannot run first;
the real algorithm is *resolve the workspace key, then decide whether to
discard it*. The spec's own dataset makes the gap concrete: row 6 is
`sub-turn, workspace_id="", agent on no CoreTeam, Viewers==0 → un:<transcriptID>` —
here there is **no candidate jar at all**, so "the candidate jar has zero
viewers" has no defined truth value, yet the expected output is the unattended
key rather than `ErrNoBrowsingContext`.

**Impact.** Two competent engineers implement two different functions. One
returns `ErrNoBrowsingContext` for row 6 (no workspace resolves, therefore
refuse — consistent with FR-008 and §5's "no fallback"); the other returns the
unattended key (consistent with the dataset). `TestResolveBrowsingKey_Ladder`
is described as "table-driven over all four ladder rungs" and would be written
to whichever reading the author held.

**Recommendation.** Rewrite the contract as the algorithm it actually is:
```
1. candidate, err := resolveWorkspaceKey(ctx, home)      // steps 2-3 of today's ladder
2. if ToolSubTurn(ctx):
     if err != nil || viewers.Viewers(candidate) == 0:
         return unattendedKey(ToolTranscriptSessionID(ctx))   // empty transcript id -> ErrNoBrowsingContext
3. if err != nil { return zero, ErrNoBrowsingContext }
4. return candidate
```
and state explicitly that a sub-turn with **no** resolvable workspace is
unattended-keyed (not refused), since that is what the dataset requires.

---

#### MAJ-002 — "Attended" is a proxy that does not implement the operator ruling it cites

- **Lens:** Incorrectness / Insecurity
- **Affected:** FR-010, US-5/AC3, §12 A3, BDD "attended delegated sub-turn is NOT isolated", test 4, ADR D1.0 consequence 2

**Description.** The operator ruling the spec repeats is: *"a background agent
starts signed out, so it cannot act as the operator on a live site with no
human present — no purchase, post or message sent as them by a process nobody
is watching."* The implemented discriminator is `Viewers(ws:W) == 0` — a count
of viewers attached to **the workspace's** browsing context, in **any** chat,
for **any** reason.

The two are not the same predicate. If the operator has the live panel open in
chat C1 watching Mia browse, and Jim in chat C2 delegates a research sub-turn
to Ray, then `Viewers(ws:W) == 1`, the sub-turn is classified **attended**, and
it browses the operator's signed-in session — while nobody is watching *it*.
That is the exact scenario the ruling forbids, produced by the mechanism
adopted to enforce it.

The failure is also trivially operator-triggerable in the opposite direction:
closing the panel between delegation and the sub-turn's first browser call
flips the classification, and §12 A3 accepts that attendance is evaluated once,
so the same delegated task is signed in or signed out depending on panel state
at an instant the operator has no reason to think about.

**Impact.** A security-relevant classification with a documented rationale is
implemented by a proxy that is false in both directions, and both BDD scenarios
(`Viewers(W)==0` and `Viewers(W)==1`) test the proxy rather than the property.
`TestResolveBrowsingKey_AttendedSubTurnUsesWorkspace` is called out in §10 as
"the boundary that makes FR-010 non-vacuous"; it is the boundary of the proxy,
not of the ruling.

**Recommendation.** Either
(a) tighten the predicate to something that means what the ruling means — a
viewer attached **to the delegating chat's live panel**, i.e. keyed by the
sub-turn's inherited `routingSessionID`, not by the workspace; or
(b) record explicitly that `Viewers(ws:W)` is a deliberate over-approximation,
state the false-attended case in §12 A3 in the same terms used here, and get it
ruled on — it is a security decision, not a spec detail.
Either way add the missing BDD scenario: *"a viewer is attached in chat C1;
a sub-turn is delegated from chat C2; assert the classification the ruling
requires."*

---

#### MAJ-003 — A fresh install has no workspaces, so every browser tool and the live panel fail; the spec never assesses this

- **Lens:** Incompleteness
- **Affected:** FR-008, US-6/AC2, §5 first non-behavior, §13 holdout 5, ADR D1.4's operator ruling

**Description.** The spec correctly refuses to fall back. It never asks how
often the refusal fires. Nothing seeds a workspace on a fresh install —
workspaces are created by the operator through `POST /api/v1/workspaces`
(`pkg/gateway/rest_workspaces.go:813`), and `coreagent.SeedConfig`'s
`isFreshInstall` path seeds agents, `AutoRecap` and the default-agent singleton,
not a workspace. `ts.opts.WorkspaceID` is set only for a channel-bound
workspace turn (`loop.go:7988`), and `UnifiedMeta.WorkspaceID` is
`json:"workspace_id,omitempty"`. So on a fresh install:

- `ToolWorkspaceID(ctx)` is empty for an ordinary chat;
- `FindForAgentPreferring(home, agentID, "")` scans a directory with no
  workspace files and returns `("", false)`;
- **every browser tool returns `ErrNoBrowsingContext`**, and
  `BrowserManagerForAgent` returns `ok=false`, so the live panel cannot attach
  either.

The ADR's own §1.1 repro — an operator chatting with Mia, the default agent,
opening the browser panel — is precisely this shape, and neither document
establishes that the reporting install had a workspace.

**Impact.** D1 converts "Jim wrongly reports zero tabs" into "the browser does
not work at all until you create a workspace and put this agent on its
CoreTeam", with no onboarding step that tells anyone. The gateway's existing
message for `ok=false` compounds it: `"no browser manager for agent %q
(browser tools may not be registered for this agent)"`
(`browser_inspect.go:75-77`) — a misleading reason for a workspace-resolution
failure, and the only thing the operator sees.

**Recommendation.**
- Add a first-class user story: **US-14 — a fresh install can browse.** Decide
  and state the answer: seed a default workspace at first boot, or auto-add
  browser-policy-allowed agents to a workspace, or accept the refusal and
  require onboarding to create a workspace before the browser panel is
  reachable.
- Add **FR-008a:** the gateway's `ok=false` reason must distinguish
  "no browsing context — this agent is not on a workspace" from "browser tools
  not registered". Two indistinguishable causes with one message is the §1.1
  defect in a new place.
- Add a holdout scenario for the un-onboarded install; move today's holdout 5
  (heartbeat, no workspace) from "edge" to the ordinary path it now is.

---

#### MAJ-004 — The "no wire change" claim passes only because SC-007 measures shape, not meaning

- **Lens:** Inconsistency / Incorrectness
- **Affected:** FR-016, FR-017, SC-007, US-10/AC1, §12 A6, BDD "no wire-schema change holds"

**Description.** Verified in `contracts/components/schemas/`. `BrowserAttachFrame.yaml`
does not merely *fail to mention* the workspace — it **forbids** the semantics
FR-017 introduces:

> `session_id` … "carried for context/correlation and logging only … It does
> NOT select which browser tab the live view attaches to: the server binds to
> the active tab in the attaching agent's OWN browser context …, **regardless of
> the value sent here**. agent_id is the binding key."

`BrowserWebRTCOfferFrame.yaml` carries the same guarantee. FR-017 makes
`session_id` the input that selects the workspace and therefore the browsing
context — a reversal of a documented wire contract. SC-007 checks that "the
`contracts/` diff contains no `properties:`, `required:`, `enum:` or `type:`
change", which this reversal passes cleanly. **The acceptance criterion cannot
fail for the change it exists to police.**

Separately, `BrowserInspectRequest.session_id` is documented as
`"Browser session id (context/correlation; the live tab is the agent's default)"`
— a *browser* session id, not a chat session id. FR-017's mechanism ("read
`workspace_id` off the attaching chat session's meta") is therefore undefined
for one of the three surfaces Stream E owns, and `TestGateway_ResolvesManagerWithoutWireChange`
("all three gateway surfaces") has no specified expectation for it.

**Impact.** The spec's headline compliance claim is technically true and
substantively misleading, and its guard is incapable of catching the substantive
change. A client that today sends an arbitrary `session_id` (permitted — "regardless
of the value sent here") will, after this change, be routed to a different
browsing context or refused.

**Recommendation.**
- Restate the claim honestly: *"No field is added, removed or retyped. The
  **meaning** of `session_id` changes from correlation-only to
  workspace-resolving on two frames; this is a behavioural contract change
  recorded here and reflected in the schema descriptions."*
- Strengthen SC-007: the diff must be reviewed for **semantic** reversals, and
  the two description edits must be listed verbatim in the spec so a reviewer
  can check them.
- Decide `BrowserInspectRequest` explicitly: either it gains the same chat-session
  semantics (and its description is corrected), or Stream E resolves its
  workspace from the agent alone and the spec says which tie-break applies.
- Confirm and record the tool-result question the spec does not raise: changing
  `browser_list_tabs`' payload from `browser_started: bool` to `state: enum`
  alters JSON persisted in `sessions/<id>/<YYYY-MM-DD>.jsonl`. (Verified: the
  SPA does not read `browser_started` — `grep -rn "browser_started" src/`
  returns nothing — so this is safe, but the spec asserts "no wire change"
  without ever having checked.)

---

#### MAJ-005 — The behaviour ADR criterion 3b actually requires has no automated coverage

- **Lens:** Incompleteness
- **Affected:** US-8, FR-014, test 16, §4 contract item 10, §13 holdout 4, ADR criterion 3b

**Description.** ADR criterion 3b — the one that exists so §1.1 does not recur
with a new cause — is a statement about what the *agent says*: "It says it is
**not permitted** to see the browser — never 'there are no tabs'." The spec's
coverage is `TestListTabs_DeniedAgentNeverReachesTool`, which asserts the
policy-denial message is produced and that `Execute` was not entered. That is
the tool layer. Whether the model then tells the operator "I'm not permitted"
rather than "the browser is empty" depends on `ModelMessage`, verified as the
bare string `"Tool execution denied by policy."` (`tool_denial.go:206-210`) — which
does not name the browser, the tool, or the permission, and is the same string
used for every denied tool in the system.

§4's own contract item 10 hedges to "the answer the model **can** produce";
§13's holdout 4 covers the real observable but holdouts are explicitly "NOT in
the TDD plan or traceability".

**Impact.** The spec can be fully implemented, all 30 tests green, and the
operator can still be told "there are no tabs" by a denied Mia — the verbatim
§1.1 symptom, which the ADR calls the reason 3b exists. The gate that would
catch it is deliberately outside the test plan.

**Recommendation.** Either
(a) strengthen the artefact so the tool layer carries the meaning — add an FR
requiring the denial surfaced for a `browser_*` tool to name the surface
("this agent's policy does not allow the browser tools"), and test that string;
or
(b) promote holdout 4 into the acceptance criteria as a required manual UAT
step with a recorded transcript, and say plainly in §11 that no automated test
covers ADR criterion 3b.
Silence is the one option that reproduces the original defect.

---

#### MAJ-006 — The boot warm-tab path is broken by the re-key and is not mentioned

- **Lens:** Incompleteness / Inoperability
- **Affected:** §2 symbol table (omits it), §3 Stream ownership, §5, FR-002

**Description.** `gateway.go:3562` calls `pickWarmBrowserManager(cfg, agentLoop.BrowserManagers())`,
which per `config.go:3892-3896` "warms the SAME session the live panel and the
agent's own browser tools use (`browser.DefaultSessionID`) on **ONE** agent —
the default agent (`agents.defaults.default_agent_id`), or, when that is
unset/has no browser manager, the lexicographically-first agent that has one."

Both halves break: the session id is the constant this spec deletes, and the
selection predicate is an **agent id** applied to a map that is no longer keyed
by agent. The spec's symbol table, stream ownership and non-behaviors do not
mention `BrowserWarmTab`, `pickWarmBrowserManager`, or `Preprovision`.

**Impact.** The warm tab silently stops working or warms an unowned key. Its own
contract is "best-effort and never blocks or fails boot … a failure is logged
at WARN" — so it degrades invisibly, and the cost it was added to remove
(1.0-2.2 s on the operator's first panel open, per the same doc comment) comes
back with no signal.

**Recommendation.** Add `pickWarmBrowserManager` and the warm path to §2's
symbol table as **modifies**, assign it to Stream A or E, and add an FR: *"boot
warm-tab warms the browsing context of the default agent's resolved workspace;
when no workspace resolves it is skipped with a single INFO, not a WARN."* Add
one test asserting the warmed session id equals the resolved key.

---

#### MAJ-007 — The WebRTC capture registry and ADR-048 capture-conflict rules are assigned to a stream with no requirement governing them

- **Lens:** Incompleteness
- **Affected:** §3 Stream E ("the WebRTC capture registry's keying (`browser_webrtc.go:77`, keyed by agent id today)"), FR-016, §9 traceability

**Description.** Stream E's scope names the capture registry, and no FR, AC,
BDD scenario, test or success criterion mentions it. FR-016 covers only
"gateway resolves agent→workspace server-side; no wire field added". Verified,
the registry is `sessions map[string]*browser.CaptureSession // keyed by agentID`
(`browser_webrtc.go:70-78`), and the surrounding ADR-048 logic
(`config.go:3826-3844`) is built on the assumption that agents have **disjoint
tab sets**: it brings "the REQUESTING agent's tab to front before the encoder
resolves its target", denies a new capture when "another agent's capture session
is still ACTIVELY VIEWED", and supersedes viewerless leftovers.

Under D1, two agents on one workspace share one tab set and one context. "Bring
this agent's tab to front" and "deny against another agent's session" are no
longer well-defined operations on the object they act upon.

**Impact.** Scope assigned with no contract is scope that gets implemented by
guess and reviewed against nothing. The observable failure — the panel showing
a different agent's tab, or refusing to start capture for the second agent on a
workspace — is exactly the class of confusion this ADR exists to remove.

**Recommendation.** Add **FR-016a** stating whether the capture registry is
re-keyed to the browsing key or stays agent-keyed, and what the ADR-048
conflict rule means once two agents share a context (most likely: one capture
session per browsing context; the "requesting agent" concept collapses). Add an
AC and a test. If the answer is "unchanged, deliberately", say so in §5 with the
reasoning — silence is not a decision.

---

#### MAJ-008 — FR-019 fixes the leased set at "the 7 action tools"; D2 adds five more

- **Lens:** Incompleteness / Inconsistency
- **Affected:** §3 Stream D, FR-019, FR-021, §5 "Non-goal: read-only tools stay ungated"

**Description.** Stream D enumerates its call sites as a closed list —
`tools.go:119,232,429,879` and `tabs.go:113,171,239` — and FR-021 defines the
ungated set as exactly `browser_screenshot`/`browser_get_text`/`browser_wait`.
The sibling D2 spec adds `browser_select_option`, `browser_press_key`,
`browser_hover`, `browser_upload_file` and `browser_handle_dialog`, all of
which are action tools that must hold the lease (D2's own §3 Stream F and its
A-10 assumption say so).

Nothing in D1 requires a *new* action tool to take the lease. `TestWriteLease_OneWriterPerContext`
tests two goroutines against a fake action; `TestWriteLease_ReadOnlyToolsUngated`
tests the three named read-only tools. A twelfth tool added later that forgets
the lease passes both.

**Impact.** After D2 lands, one or more action tools drive Chrome without
mutual exclusion, intermittently, on a shared context — the nondeterministic
failure ADR §5 calls the most expensive kind.

**Recommendation.** Replace the enumeration with a **rule plus a structural
test**: *"every tool in `pkg/tools/browser` that mutates page or tab state
acquires the write lease; the read-only exemption is a closed, named list of
three. A tool that is neither leased nor on the exemption list fails
`TestWriteLease_EveryActionToolIsLeased`."* That test must enumerate the
registry, not a hand-written list.

---

#### MAJ-009 — `browserCtxIDs` as a map is dead structure, and its stated justification contradicts FR-001

- **Lens:** Overcomplexity / Inconsistency
- **Affected:** §3 shared-interface contract (`manager.go` block), invariants M-1/M-2, §12 A1 ("the highest-value single item in this spec"), ADR D1.2a change 1

**Description.** The spec replaces `BrowserManager.browserCtxID` (a single
`cdp.BrowserContextID`, `manager.go:381`) with a map, justified as:

> "One manager owns one key, so this is a **one-entry map in production**; it is
> a map rather than a field so the CRIT-003 invariant … stays checkable and so a
> manager can be proven to refuse a session id that is not its own key."

Both halves fail their own test. FR-001 already establishes one manager per
key, and invariant M-2 ("`Session(id)` errors if `id != m.key.String()`")
already forbids a second entry — so the map is provably always length 1, and
neither property it claims to enable requires a map: storing `m.key` alongside
the existing single field gives M-2 directly, and CRIT-003 checkability is
about `WithNewBrowserContext` call sites, not about the field's type.

Worse, the map is actively hazardous in the direction §12 A1 warns about: it is
a data structure whose whole shape invites a second entry, which is precisely
the defect (a second jar sharing one CDP context) A1 was written to prevent.

**Impact.** The item the spec nominates as its highest-value contribution is
either redundant (if FR-001 holds) or FR-001 is wrong (if a manager can serve
two keys) — and the spec asserts both without noticing.

**Recommendation.** Pick one and say it:
- **Preferred:** keep the single `browserCtxID` field, add `m.key BrowsingKey`,
  and state M-2 as "`Session(id)` errors unless `id == m.key.String()`". The
  structural change ADR D1.2a actually requires is *"the unattended key gets a
  manager of its own, which Registers with its own key and therefore gets its
  own coordinator-owned context"* — a keying change, not a container change.
  Amend D1.2a change 1 accordingly.
- Or, if a manager must serve multiple keys, delete FR-001 and M-2 and respec
  the whole ownership model — a much larger change than the spec currently
  contemplates.

---

#### MAJ-010 — D1 creates the unattended-browsing scenario that makes #659 dangerous, and never mentions it

- **Lens:** Incompleteness (cross-spec dependency)
- **Affected:** US-5, FR-009, FR-012, §1 out-of-scope ("the D2 policy seeding (D2.9)"), ADR D2.9's amended text

**Description.** The ADR was amended after this spec was written (commit
`bee906f5`) to record an operator ruling: `browser_upload_file` is **`ask` in
the global tool policy for every agent** — and to record that *"issue #659
(open) records that `AutoDenyAsk` is not inherited by delegated subagents, so a
delegated worker that tries to upload a file today blocks on an approval nobody
can answer. **#659 is therefore a hard prerequisite … shipping the seed without
it converts a clean refusal into a hung turn.**"*

D1 is what makes that dangerous: it is D1 that establishes *unattended
delegated sub-turns that browse*, by definition with no human to answer an
`ask`. The D1 spec files "the D2 policy seeding (D2.9)" as out of scope and
never records the dependency running the other way.

**Impact.** D1 and D2 ship independently by design. If D1 ships first and D2
second without #659, the first unattended sub-turn to reach an `ask`-policy
browser tool hangs indefinitely — a wedged delegation, which is the same class
of failure (nothing answers, nothing errors) the ADR treats as unacceptable
elsewhere.

**Recommendation.** Add to §6 Integration boundaries: *"Delegated sub-turns
created by this spec have no operator by construction. Any `ask`-policy tool
they reach must be auto-denied. `AutoDenyAsk` (`loop.go:594-599`) is not
inherited by delegated subagents (#659), so #659 is a prerequisite for any
`ask`-policy browser tool reaching an unattended sub-turn."* Add an AC asserting
that an `ask`-policy tool invoked from an unattended sub-turn is denied, not
queued.

---

#### MAJ-011 — Sorted-first tie-break selects a cookie jar, and the spec records it without assessing it

- **Lens:** Insecurity / Incompleteness
- **Affected:** §10 dataset row "agent on workspaces A and B; session meta empty → `ws:A` (documented sorted-first tie-break)", FR-007, FR-018, US-11, §6 "Workspace store"

**Description.** `FindForAgent` resolves multi-membership by "the first (in
sorted-id order, for determinism)" workspace (`find_for_agent.go:45-48`), and
`FindForAgentPreferring`'s fast path deliberately **suppresses** the
ambiguity WARN (documented at `find_for_agent.go:168-176`). For the filesystem
re-rooting this ladder was built for, an arbitrary-but-deterministic choice is
defensible: the worst case is files in the wrong project directory.

For a browsing context it selects **which set of live logins the turn acts
with**. A heartbeat or scheduled turn for an agent on workspaces A and B will
act as the operator's signed-in identity on workspace A, for no reason other
than alphabetical order, with the ambiguity signal suppressed on the fast path
and a WARN nobody reads on the slow one.

**Impact.** A credential-selection decision is inherited from a
path-resolution helper without being re-examined for its new consequence. §6
notes the mechanics; no FR, AC, holdout or security consideration addresses it.

**Recommendation.** Add an explicit decision and an AC. Options worth stating:
refuse (`ErrNoBrowsingContext`) when an unattended/workspace-less turn is
ambiguous across ≥2 workspaces, rather than picking one; or keep sorted-first
and require a WARN that is emitted on **both** paths whenever the tie-break
actually arbitrates for a *browsing* key. Either way, say which and test it —
"documented" is not the same as "assessed".

---

### MINOR

| ID | Lens | Affected | Finding | Recommendation |
|---|---|---|---|---|
| **MIN-001** | Incorrectness | §10 regression list | `shared_control_test.go` is described as "all **nine** (`:35`–`:186`)". It contains **8** test functions (`:35, :55, :74, :92, :109, :138, :157, :186`). | Correct to eight. See also CRIT-005 — the file is also the wrong guard for FR-022. |
| **MIN-002** | Incompleteness | §10 regression list | `tools_control_test.go` — the **only** direct coverage of `controlledResult` (3 tests) — is absent, though the round-1 ADR review named it and the sibling D2 spec lists it. | Add it to "must keep passing", and state whether its assertions survive the re-key unchanged. |
| **MIN-003** | Incorrectness (ADR drift) | §1 out-of-scope; §5 non-behaviors | The spec says `web_serve` twice. ADR D2.5 was amended (commit `bee906f5`) to record that the registered name is **`serve_web`** (`pkg/tools/web_serve.go:46`) and that shipping the wrong string sends agents to a nonexistent tool. | Change both occurrences to `serve_web`. |
| **MIN-004** | Inconsistency | §2 symbol table vs §3 interface block | Viewer-counter lines are given as `manager.go:2812,2830` in §2 and `2818/2836` in §3; the actual increments/decrements are `:2818` and `:2834`. The spec's own two citations disagree with each other. | Cite `BrowserManager.ViewerAttached`/`ViewerDetached` by symbol, per the root `CLAUDE.md` rule that line numbers in churning files go stale. |
| **MIN-005** | Incompleteness | FR-015, test 6 | FR-015 requires the five "shared browser session" strings be "corrected" and its test only asserts the old phrase is **gone**. No replacement text is specified — and the correct text is now context-dependent: for an unattended sub-turn the jar is private, so "shared across this workspace" would be a new false claim aimed at the sub-turn's own model. | Specify the replacement literal(s) in the spec, and require that the description be accurate for both key kinds (or that it not assert sharing at all). |
| **MIN-006** | Ambiguity | §12 A10 | A10 is marked **UNRESOLVED** ("who is the operator of record for the 2026-08-31 rulings") and the spec proceeds to implement six rulings on that authority. No gate stops implementation while it is open. | Either attribute the rulings in ADR-072 (the ADR now names "Daniel Piatkowski" for the two D2.9/D2.11 rulings — do the same for D1's six), or record A10 as a blocking prerequisite in §11, not a footnote. |
| **MIN-007** | Ambiguity / Infeasibility | §12 A8, FR-023, test 12 | `leaseWaitTimeout` "assume 2s, tunable" has no stated relationship to action-tool CDP timeouts, and test 12 requires a "fake clock" seam that is never named (the manager has `m.now()`; the lease is new code). | State the config key (or that it is a compile-time constant), state the relationship to the action-tool timeout, and name the clock seam in the interface contract. |
| **MIN-008** | Inconsistency | §2 symbol table (`AgentLoop.browserMgrs`) | `loop.go:275-279` carries a standing instruction: *"Do NOT reintroduce a single shared field — the gateway's live-view WS handler needs a specific agent's manager, not 'whichever agent registered last.'"* The spec re-keys the map without acknowledging or updating that comment. | Note it in §2 and specify the replacement comment, so the next reader does not treat the re-key as the regression the comment forbids. |
| **MIN-009** | Incompleteness | SC-003 | SC-003 claims "zero browsing contexts are created with a key that did not come from `ResolveBrowsingKey`", asserted structurally via "`BrowsingKey`'s field is unexported and `key.go` exposes no other constructor". That constrains *key construction*, not *context creation* — a caller in `pkg/agent` can hold a legitimately-constructed key and pass it to `BrowserManagerForKey` at the wrong time (e.g. the parent's key inside a sub-turn). | Add a behavioural assertion to match the claim: count `Coordinator.Register` calls in a run and assert each one's key came from a `ResolveBrowsingKey` return in the same turn. |

---

### OBSERVATIONS

| ID | Lens | Affected | Observation |
|---|---|---|---|
| **OBS-001** | Overcomplexity | FR-013, US-7, D1.5 | The three-state `TabState` is largely a rename. The code already distinguishes the two states a *running tool* can produce, via `browser_started` (`tabs.go:58,66`) — the "B-8 fix", with a description that already spells out the distinction at length. The genuinely new content is (a) `TabStateEmpty` becoming reachable, which §12 A5 establishes, and (b) the third state being an *end-to-end* property, which §12 A4 correctly rules is not a `TabState` at all. The remaining delta is boolean→enum. The §1.1 defect is fixed by the re-key plus the description corrections, not by the enum. Worth asking whether the enum, its wire-shape change and its two tests earn their place, or whether keeping `browser_started` and adding one `tabs_empty` discriminator is the whole job. |
| **OBS-002** | Overcomplexity | §3 shared-interface contract | Three new concepts arrive together where one would do: `BrowsingKeyKind` (explicitly "for audit + logging only. Never a branch in the isolation logic"), the `browserCtxIDs` map (MAJ-009), and the `ViewerCounter` interface (a one-method seam over one implementation). Each is individually defensible; together they are ~4 new types on a change whose essential content is "the map key is a workspace, and the manager is shared". Removing `BrowsingKeyKind` costs a `strings.HasPrefix` in the audit event. |
| **OBS-003** | Overcomplexity / process | §12 A7, CRIT-004 | The lease is the only part of this spec that is not about *ownership*: it is a concurrency control that D1's re-key makes necessary. It is filed under D2.10 in the ADR, claimed by D1's §12 A7, and independently specced by D2. That is three homes for one decision. Giving it a fourth — its own short spec, referenced by both — would resolve CRIT-004 mechanically and let D1 and D2 keep their stated property of shipping independently. |

---

## 3. Structural integrity results (`plan-spec` mode)

| Check | Result | Note |
|---|---|---|
| Every user story has ≥1 acceptance scenario | **PASS** | US-1…US-13 all carry ACs. |
| Every acceptance scenario has ≥1 BDD scenario | **PARTIAL** | US-12/AC1, AC2, AC4 and US-13/AC2 have no dedicated BDD scenario; §8 lists 16 scenarios against 30 ACs. |
| Every BDD scenario has a `Traces to:` back-reference | **PASS** | Each §8 scenario names its US/AC and FRs inline. |
| Every BDD scenario has a corresponding test | **PARTIAL** | "human browses first, then delegates" maps to FR-016/FR-017 but no §10 row exercises the *combined* path (panel attach → agent list) end to end. |
| Every FR appears in the traceability matrix | **PASS** | FR-001…FR-030 all present. |
| Every BDD scenario appears in the matrix | **PARTIAL** | "reload preserves the workspace login" and "disposal on workspace deletion" appear via FR rows only; FR-030 has no US and no BDD (`— / —`). |
| Test datasets cover boundaries, edges, errors | **PARTIAL** | Strong on resolution and lease. **Missing:** zero-workspace install (MAJ-003), `capture_shared_context=true` (CRIT-001), reload with 2 agents on one workspace (CRIT-003), unattended-context disposal (CRIT-006), a viewer attached in a *different* chat (MAJ-002). |
| Regression impact explicitly addressed | **PARTIAL** | A real, well-constructed list — with two errors (MIN-001, MIN-002) and one nominated guard that cannot detect the defect it guards (CRIT-005). |
| Success criteria measurable, no subjective language | **PARTIAL** | SC-001, SC-005, SC-007, SC-009 are measurable. **SC-002 and SC-004 are unsatisfiable in the default configuration** (CRIT-001). **SC-007 cannot fail for the change it polices** (MAJ-004). SC-003's assertion does not match its claim (MIN-009). |
| Numeric/code claims reproduce | **MOSTLY PASS** | Verified correct: `manager.go:381` single field; `:1369` `WithExistingBrowserContext`; `subturn.go:1323` `WorkspaceID: parentTS.opts.WorkspaceID`; `base.go:200/250/292`; `ToolDelegationDepth` set only at `task_executor.go:874,2693`; `controlledResult` at `tools.go:962` with 7 callers at the exact lines given; `tool_denial.go` `"Tool execution denied by policy."`; the 8 "shared browser session" occurrences split exactly 5/2/1 as claimed; `tab_adoption_e2e_test.go` = 9 tests; all seven `coordinator_test.go` line references. Wrong: `shared_control_test.go` count (MIN-001), viewer-counter lines (MIN-004). |

---

## 4. Test coverage assessment

The plan is unusually strong on the resolution ladder and the lease, and blind
in exactly the places where a green result would be false.

**Tests that cannot fail for the defect they name**
1. `TestBrowsingContext_CrossWorkspaceIsolation` / `TestUnattended_HasOwnBrowserContextID`
   — pass only under a non-default `capture_shared_context` (CRIT-001).
2. `make verify-contracts` as the FR-029/SC-007 guard — structurally incapable
   of detecting the semantic reversal FR-017 performs (MAJ-004).
3. `shared_control_test.go` as the FR-022 guard — does not exercise
   `controlledResult` (CRIT-005).
4. `TestLoop_BrowserManagerForKey_OnePerKey` + `TestTools_UseResolvedKeyNotConstant`
   as the FR-001/FR-002 guards — both pass while tools still hold per-agent
   managers and handover is still broken (CRIT-002).

**Missing tests, in priority order**
1. **Two agents, one workspace, through the real registration path** — the
   headline defect (SC-001) currently has no test that exercises
   `registerSharedTools` rather than a hand-built manager.
2. **Reload with ≥2 agents on one workspace** — CRIT-003's second defect; assert
   `disposeBrowserContextRaw` count is zero and `Register`/`Release` pairs are 1.
3. **Context/manager count returns to baseline after K unattended sub-turns** —
   the only assertion that would catch CRIT-006.
4. **Default-configuration isolation** — assert what actually happens with
   `capture_shared_context=true`, whatever the decision is, so CRIT-001 is
   visible in CI rather than assumed away.
5. **Fresh install, zero workspaces** — every browser tool and the panel; assert
   the failure text is the intended one, not "browser tools may not be registered".
6. **Viewer attached in chat C1, sub-turn delegated from chat C2** — MAJ-002's
   false-attended case.
7. **`ask`-policy tool from an unattended sub-turn** — MAJ-010; assert denial,
   not a hang.
8. **Every action tool is leased** (registry-driven, not list-driven) — MAJ-008.

**On the rewritten tests.** The three files nominated for rewrite are the right
three, and the rewrites are correctly specified as rewrites rather than
extensions. `TestCoordinator_TwoAgents_OneChrome_TwoContexts` (`coordinator_test.go:154`),
`TestCoordinator_Register_SharedContextMode_ReturnsRootCtxAndEmptyBrowserCtxID`
(`:328`) and `TestFiveAgents_ConcurrentStress` (`stress_5agents_test.go:267`)
all exist at the cited lines and all encode the per-agent model. Two notes:
- `TestCoordinator_Register_SharedContextMode_…` is the test that **proves
  CRIT-001** — it asserts Register returns an empty `browserCtxID` in the
  default mode. Re-keying its Register argument (all the spec asks) leaves it
  asserting that the isolation this spec promises does not exist. That is a
  finding, not a rename.
- SC-008's mutation discipline ("every rewritten test is confirmed to fail
  against the pre-change code and pass after") is the right gate and should be
  extended to the four false-green tests above, which would each fail that
  check today.

---

## 5. STRIDE summary

| Component | Threat | Addressed? |
|---|---|---|
| Workspace browsing context (cookies/localStorage) | **Information disclosure** — cross-workspace leak | **NO, in the shipping default.** FR-003/FR-004 assume a CDP browser context per key; `capture_shared_context=true` means one shared partition (CRIT-001). |
| Workspace browsing context | **Elevation of privilege** — joining a team grants every live login on it | Named in ADR D2.11 with a decision (team-editing UI must disclose at the point of adding). **The D1 spec drops it** — no FR, no AC, no §5 entry. See "Unasked questions" Q3. |
| Unattended delegated sub-turn | **Spoofing** — a background agent acts as the signed-in human | Mitigation specified (FR-009…FR-012) but its discriminator does not implement the ruling (MAJ-002), and in the default config the sub-turn is signed in regardless (CRIT-001). |
| Browser action tools | **Tampering** — interleaved agent writes to one tab | Specified (FR-019…FR-024) but double-specified with incompatible APIs (CRIT-004) and with a set that D2 grows without a rule (MAJ-008). |
| Human control lock (ADR-038 D6) | **Tampering** — an agent drives while a human holds the wheel | **NO.** `controlledResult`'s hardcoded `defaultSessionID` silently disables it (CRIT-005). |
| Any browsing-context use | **Repudiation** — "which agent acted as the signed-in user" | Addressed: FR-027, US-13, test 27. Note the audit event is on *first* cross-agent use only, so a later action by the same agent is not re-recorded — acceptable but worth stating. |
| Unattended context lifecycle | **Denial of service** — unbounded context/manager growth | **NO.** No disposal path exists (CRIT-006). |
| Workspace-less / multi-workspace turn | **Elevation of privilege** — acting with an arbitrarily-chosen workspace's logins | **NO.** Sorted-first tie-break inherited unexamined (MAJ-011). |
| Live-panel frames | **Spoofing** — `session_id` becomes a workspace selector | Partially: FR-017 resolves server-side from session meta, which is the right shape. But the frame's own contract declares the field non-binding, and nothing validates that the caller owns the named session (MAJ-004). |

---

## 6. Unasked questions

1. **What is the browsing key when `capture_shared_context` is true?** The spec
   has no answer because it does not know the setting exists. Is the resolution
   still performed (for tab-set ownership) while isolation is absent, or is the
   whole feature inert?
2. **Can an operator have both workspace-scoped browser isolation and the live
   video panel?** On the current code the answer appears to be **no**
   (CRIT-001). If that is true it is a product decision, not an implementation
   detail, and it belongs in the ADR.
3. **Where is D2.11's elevation-of-privilege disclosure?** The ADR *decides*
   that "the team-editing UI must state this at the point of adding, not only in
   release notes". The D1 spec's out-of-scope list excludes only "the D2.11
   information-disclosure bullet" — so the elevation bullet is in scope, and it
   has no FR, no AC, and no SPA work item. Which spec owns it?
4. **What does an operator see when resolution fails?** FR-008 defines the
   *tool's* text. The live panel's path returns `ok=false`, rendered as "browser
   tools may not be registered for this agent". Nothing specifies the panel's
   user-facing message for a workspace-resolution failure.
5. **Does the workspace's browsing context outlive the last agent that can use
   it?** If every browser-policy-allowed agent is removed from W's CoreTeam but
   W still exists, is W's context disposed? FR-026 says "roster change" without
   defining the predicate.
6. **What happens to a running unattended sub-turn when its parent turn is
   cancelled?** ADR-057's `routingSessionID` inheritance makes chat-wide Stop
   cascade to sub-turns. Does the cancel dispose the unattended context, leave
   it for the (nonexistent) reaper, or leak it?
7. **Is `ResolveBrowsingKey` evaluated once per turn or once per tool call?**
   §12 A3 says attendance is "evaluated once, at key resolution" but never says
   when key resolution happens. Per-call re-resolution would let a workspace
   change mid-turn; per-turn resolution needs a place to cache it that the spec
   does not name.
8. **Does anything prevent two gateway processes on one `$OMNIPUS_HOME` from
   both driving one workspace's context?** The lease is explicitly in-process
   only (FR-030, and correctly so given `WithFlock`'s Windows no-op). The
   coordinator has a launch lock; the browsing context does not. Out of scope is
   a fine answer; silence is not.
9. **Are the existing per-agent browser profile directories actually on disk?**
   §12 A9 decides "discard, do not merge" for per-agent state and the round-1
   review cites `~/.omnipus/browser/profiles/`. With `capture_shared_context`
   defaulting true, most installs have no per-agent CDP context to discard —
   which may make A9 a non-event, or may mean it is describing state that does
   not exist. Verify before writing the release note it mandates.

---

## 7. Next action

```
Verdict: BLOCK

Review written to:
  /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf/docs/internal/specs/browser-workspace-ownership-spec-review.md

Address in this order:
  CRIT-001  — take back to the ADR; it is a product decision, not a spec fix
  CRIT-004  — resolve lease ownership across both specs before either is implemented
  CRIT-002, CRIT-003, CRIT-005, CRIT-006 — structural gaps, all with silent failure modes
  then MAJ-001…MAJ-011

To revise:
  /plan-spec --revise docs/internal/specs/browser-workspace-ownership-spec.md docs/internal/specs/browser-workspace-ownership-spec-review.md
```
