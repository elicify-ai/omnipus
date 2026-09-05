# Round-2 review — `browser-workspace-ownership-spec.md`

- **Spec reviewed:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf/docs/internal/specs/browser-workspace-ownership-spec.md` (1112 lines)
- **Prior review:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf/docs/internal/specs/browser-workspace-ownership-spec-review.md` (29 findings, BLOCK)
- **Source ADR:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf/docs/internal/architecture/ADR-075-workspace-scoped-browser-sessions.md`
- **Worktree / branch:** `wt-browser-perf` @ `feat/browser-streaming-performance`, HEAD `335d56fe`
- **Mode:** `plan-spec`
- **Date:** 2026-08-31

---

## 1. Executive summary

Twenty-nine findings, four criticals. **Verdict: BLOCK.**

The round-1 dispositions are, with the exceptions below, **real rather than
asserted**. Every code citation I re-checked reproduces: `register.go:41-84`'s
eleven `mgr:`-bearing structs, `controlledResult`'s hardcoded
`defaultSessionID` at `tools.go:962-963`, the reload prune's
`registry.ListAgentIDs()` at `loop.go:2849`, the sixteen `coordinator.go`
symbols, `defaults.go:671`, `exec_resolver.go:385`, `tool_denial.go:205-210`,
and the **37** `DefaultSessionID` usage lines at exactly the line numbers §2.2
lists — all 37 verified individually, none wrong, none missing. The MAJ-003
rejection is correct and well-evidenced: `ensureDefaultWorkspace`
(`rest_workspaces.go:468`) does run on every boot (`gateway.go:5013`) and does
seed Jim and Ray via `defaultWorkspaceTeam` → `coreagent.All()`. The deleted
attended/unattended design has been scrubbed thoroughly; I found only one piece
of structural residue in the spec (the `"ws:"` prefix and `WorkspaceID()`
accessor, MIN-103) and one in the ADR (MIN-108).

**Two dispositions are not real.**

**CRIT-102.** §2.1 and §15 (CRIT-006) both state, with the word *verified*, that
`ReapIdleSessions` "deletes `m.sessions` entries only; it never touches the
coordinator and never closes a browser… the body's only removal is
`delete(m.sessions, sessionID)`." That is false. The body collects
`se.browserCancel` at `manager.go:3027-3032` and `:3073-3078` and executes
`cancelBounded(rb.cancel, …)` at `:3123-3125`, cancelling the **browser-owning**
chromedp context; and it reaches the coordinator through `releaseGlobalTab()`
(`:3117-3119` → `:3358-3366` → `coord.ReleaseTab(agentID)`). This is the
evidentiary basis for CRIT-006's disposition and for FR-040 being "new
behaviour", and it means the reaper/pool interaction is materially more
entangled than specified.

**CRIT-101.** §10.1 requires four reaper and tab-adoption test files to "keep
passing, **unmodified**" while FR-002b **deletes** the constant those files
reference. `tab_adoption_e2e_test.go` alone carries 41 references,
`idle_reaper_test.go` 33, `reaper_edge_test.go` 26, `reaper_lifecycle_test.go`
15. They will not compile. The two requirements cannot both hold.

**The pool brought four unspecified lifecycle problems with it.** The cap has no
edge semantics and no relationship to the existing global tab budget; the
whole-Chrome idle window is named five times and defined nowhere; nothing
reconciles N ownership markers at boot, so orphan Chromes from a crashed gateway
sit outside the cap; and the per-workspace profile directory has a creation path
but no deletion path — deleting a workspace leaves that client's logins on disk
indefinitely. Worse, `ErrBrowserPoolFull`'s own remedy text ("close a browser
panel") does not free a slot, because idle close requires zero tabs *and* zero
viewers.

**Both gates are declared, and one of them is decorative.** G-2's test 37 is
gated behind `skipIfNoBrowser` (`browser_e2e_test.go:57`), which skips unless
`OMNIPUS_BROWSER_E2E=1` — and a skip reports green. Nothing in SC-014's gate
list runs it. The only real enforcement for either gate is SC-012's "recorded in
the PR body", a human review step with no failing check behind it.

**The write-lease annex is internally inconsistent after the 3→5 widening.** The
count appears as *five* (§14 rule 3), *three* (§15 MAJ-008 disposition), *five
new D2 action tools* (§14.1 doc comment) and *four remaining D2 action tools*
(§14 rule 3) in one document. And the closed set of five **omits
`browser_list_tabs`**, which is registered, read-only, and is the exact call the
headline defect scenario makes.

| Severity | Count |
|---|---|
| CRITICAL | 4 |
| MAJOR | 14 |
| MINOR | 8 |
| OBSERVATION | 3 |
| **Total** | **29** |

---

## 2. Findings

### CRITICAL

| ID | Lens | Section | Finding | Recommended fix |
|---|---|---|---|---|
| **CRIT-101** | Inconsistency / Infeasibility | §10.1 "Must keep passing, unmodified" vs FR-002b, SC-013 | FR-002b deletes `DefaultSessionID` **and** its alias `defaultSessionID` (`tools.go:63`, `:69`). §10.1 then requires `tab_adoption_e2e_test.go`, `idle_reaper_test.go`, `reaper_edge_test.go` and `reaper_lifecycle_test.go` to keep passing **unmodified**. Those four files hold **115** references to the deleted constants (41/33/26/15); repo-wide the test surface is **364 references across 25 `_test.go` files**, ~357 of them executable. They will not compile, so "unmodified" is unsatisfiable. The only way to honour both clauses is a test-only alias — which re-creates the exact `"default"` constant SC-013 counts to zero, and leaves the reaper suite asserting against a key nothing in production uses. | Replace "unmodified" with "**semantics unmodified; the session-id argument is mechanically re-pointed at the browsing key, and no assertion is weakened**", and add a §10.1 line item for the 364-reference test migration. State explicitly that no `defaultSessionID` alias survives in test code either, or SC-013 is unmeasurable. |
| **CRIT-102** | Incorrectness | §2.1 (`ReapIdleSessions` row), §15 CRIT-006 disposition, FR-040 | Both places assert, marked *verified*: "It deletes `m.sessions` entries only; it never touches the coordinator and never closes a browser (verified: the body's only removal is `delete(m.sessions, sessionID)`)." **False.** `ReapIdleSessions` collects `se.browserCancel` into `reapedBrowsers` at `manager.go:3027-3032` (stranded-empty-session branch) and `:3073-3078` (all-tabs-idle branch) and executes `cancelBounded(rb.cancel, …)` at `:3123-3125` after unlocking — cancelling the browser-owning chromedp context. It also cancels per-tab contexts at `:3106-3107` and calls `m.releaseGlobalTab()` at `:3117-3119`, which reaches `coord.ReleaseTab(agentID)` at `:3358-3366`. Only the narrower statement survives: it never calls `RemoveAgent` or `disposeBrowserContextRaw`, so the coordinator-owned CDP context is not disposed. The consequence is not cosmetic: the reaper can already cancel a manager's browser context while the pool still believes that key's Chrome is live, producing a live OS process with a dead root context that `LiveKeys()` still counts against the cap. | Correct both statements to the narrow true one. Then specify the reaper↔pool contract in FR-040: who calls `pool.Close`, whether it runs before or after `cancelBounded(se.browserCancel)`, what happens to the `browserMgrs` entry and the `BrowserManager` itself, and how a subsequent tool call for that key relaunches. Add a test dataset row for "reaper cancels `browserCancel` while pool entry is live". |
| **CRIT-103** | Incompleteness / Incorrectness | §3.1 `ErrBrowserPoolFull`, FR-039, US-15, holdout 12 | The refusal text tells the operator: *"close a browser panel or finish work in another workspace, then retry."* Neither action frees a slot. Closing a **panel** detaches a viewer (`ViewerDetached`); FR-040's idle close requires **zero tabs and zero viewers past the idle window**, so a workspace with tabs open never closes no matter how many panels shut. "Finish work in another workspace" has no mechanism at all. The spec specifies no operator-facing close action, no LRU, and explicitly forbids eviction (§5). With N workspaces holding tabs, the (N+1)th workspace is refused **permanently**, and the error tells the operator to do something ineffective. | Either (a) add an explicit operator-facing "close this workspace's browser" action (REST + UI) and make `ErrBrowserPoolFull` name it, or (b) specify a bounded eviction of a browser with zero viewers and no in-flight lease, with the logout stated as accepted. Correct the error text either way — a remedy that does not work is worse than no remedy. |
| **CRIT-104** | Inconsistency / Incorrectness | FR-019a, US-9/AC4+AC5, §14 rule 3, test 18 | The closed exempt set is **five**: `browser_screenshot`, `browser_get_text`, `browser_wait`, `browser_snapshot`, `browser_handle_dialog`. `browser_list_tabs` (`tabs.go:28`) is registered (`register.go:76`), read-only, and **in neither category**. Test 18 enumerates the registry and requires every `browser_*` tool to be leased or exempt, so `browser_list_tabs` must acquire the **write** lease. That makes Jim's `browser_list_tabs` — the literal call in behavioural contract 1, US-1/AC1 and the headline BDD scenario — return `{"deferred": true}` whenever another agent holds the lease for a long `browser_navigate`. The feature's headline demo defers behind an unrelated agent. | Add `browser_list_tabs` to the exempt set (six), and re-derive the set from the actual registry rather than from memory: classify all eleven shipped tools explicitly in §14 rule 3's table. Add `browser_list_tabs` to FR-021's BDD scenario and test 15 alongside screenshot/get_text/wait. |

### MAJOR

| ID | Lens | Section | Finding | Recommended fix |
|---|---|---|---|---|
| **MAJ-101** | Inconsistency | §14 rule 3, §14.1 `leaseWrite` doc, §15 MAJ-008 | The exempt-set size and the D2 tool count are stated four different ways in one document: §14 rule 3 "a closed, named set of **five**"; §15 MAJ-008's disposition "the exemption is a closed set of **three**"; §14.1's `leaseWrite` comment "so D2's **five** new action tools do not hand-roll the deferral body"; §14 rule 3's closing sentence "D2's **four** remaining new action tools". Only "five exempt / four remaining leased" is consistent with ADR criterion 15's six D2 tools. §15 MAJ-008 is stale text from before the widening — i.e. the disposition table asserts a fix that the annex has since changed. | Make §15 MAJ-008 say five, and make §14.1's comment say four. Add a single normative count sentence and reference it from the other three places. |
| **MAJ-102** | Inconsistency / Infeasibility | US-9/AC5, FR-019a, §14 rule 3 | The rule is *"every tool that mutates page or tab state acquires the lease; the exemption list is exactly the five."* `browser_handle_dialog` **mutates page state** (it accepts or dismisses a modal), so the rule and its exemption contradict each other. Consequently test 18 cannot classify by "does it mutate" — it can only check membership of a hand-written list, which is the list-driven test MAJ-008 was raised to eliminate. The list has already been widened once (3→5, same day, under pressure from the sibling D2 spec) and the spec gives no criterion distinguishing a legitimate widening from a convenient one. Separately, the interaction is unspecified: after `browser_handle_dialog` clears the modal, the still-blocked `browser_click` resumes holding the lease and acts on a changed page. | Restate the rule as *"every tool takes the lease unless it appears in the exempt set"*, and give each exemption a stated **reason class** (read-only / recovery) so a future addition has to argue one. Specify what the unblocked leaseholder does after a dialog is cleared, or state that its result is undefined and it must re-verify. |
| **MAJ-103** | Incompleteness | FR-037, FR-042, FR-043, §3.1 `pool.go` | The per-key profile dir is specified as `<cfg.ProfileDir>/ws/<workspaceID>/`. `cfg.ProfileDir` is itself the current Chrome **user-data-dir** (default `~/.omnipus/browser/profiles/default/`, `manager.go:125`), so this nests a user-data-dir inside a user-data-dir. More concretely: `cleanStaleSingletons` (`coordinator.go:1488-1498`) removes `SingletonLock`/`SingletonCookie`/`SingletonSocket` and is called with `c.cfg.ProfileDir` only (`:1235`). Each per-workspace profile gets its **own** singleton files, and nothing cleans them. After a gateway crash or kill, that workspace's next Chrome launch fails on a stale singleton — which defeats FR-043's whole claim that "a profile is on disk, so the login survives". `cleanStaleSingletons` appears **zero** times in the spec. | State whether `cfg.ProfileDir` becomes a parent directory (a meaning change for an existing config key — note `InstallRootForProfileDir`, `exec_resolver.go:50`, derives the managed-Chromium install root from it) or whether per-key dirs live in a new sibling. Add an FR: `cleanStaleSingletons` runs against each per-key profile dir before launch. Add a test dataset row: "stale singleton in W's profile dir → W relaunches". |
| **MAJ-104** | Incompleteness | §2.1 (`BrowserManagers()` row), FR-016b | `AgentLoop.BrowserManagers()` is listed as **reuses** — *"Snapshot slice for reaping/Close; membership changes."* That omits its shipped boot role: `gateway.go:2286` iterates it to call `BrowserManager.Preprovision`, which starts the managed-Chromium download at boot instead of at the first browser tool call (`loop.go:4880-4886` documents exactly this). Under a lazily-created per-workspace pool the snapshot is **empty at boot**, so Preprovision never fires and a fresh install's first browser call blocks on a multi-hundred-megabyte download. FR-016b covers `pickWarmBrowserManager` but not this. `Preprovision` appears **zero** times in the spec. | Add an FR for the boot preprovision path: either eagerly create one manager for the default agent's resolved workspace, or decouple Preprovision from `BrowserManagers()` (it resolves an exec path and does not need a live key). Add a test asserting the download still starts at boot. |
| **MAJ-105** | Incompleteness | FR-002a, test 4, §2.1 | Two omissions in FR-002a's blast radius. (a) `metadata.go:36-51` (`BrowserBuiltinMetadata`) constructs **all eleven tool structs a second time** with a nil manager; the `RegisterTools`/struct-shape change must update it and the spec never names the file. (b) Three **exported non-tool** structs in `pkg/tools/browser` hold a `*BrowserManager` field — `CaptureSession` (`capture_session.go:258`), `LiveViewRegistry` (`live.go:322`), `LiveView` (`live.go:1324`) — so test 4 as described ("reflects over every exported tool struct… fails on any `*BrowserManager` field") will either trip on them or needs a `tools.Tool` filter the spec does not state. | Add `metadata.go:36-51` to §2.1 as **modifies**. Specify test 4's predicate precisely: *every type in the package that implements `tools.Tool`* — and note the three legitimate non-tool holders so nobody "fixes" them. |
| **MAJ-106** | Infeasibility | FR-045, SC-012, test 37, §0.3 | G-2 is declared blocking but is not enforceable. Test 37 (`TestSpike_CaptureAgainstSecondChrome`) is specified as *"real Chrome, `skipIfNoBrowser`"*. `skipIfNoBrowser` (`browser_e2e_test.go:57-65`) **skips unless `OMNIPUS_BROWSER_E2E=1`** — and a skipped test reports green, the exact stale-green shape `docs/internal/false-green-patterns.md` catalogues. SC-014's gate list does not include it and does not set that env var. The same applies to G-1: SC-012's failure condition is *"the default is a round number with no measurement behind it"*, which is a human reading a PR body. Both gates are therefore decorative in CI. | Make the gates mechanical: require test 37 to run with `OMNIPUS_BROWSER_E2E=1` in the implementing PR and paste the receipt (`exit=0`, not a pipe), and make `max_browsers`' default a value read from a committed measurement file that a unit test asserts is non-empty and dated. If neither is practical, say plainly in §0.3 that the gates are review gates, not CI gates — do not let a skip stand in for a proof. |
| **MAJ-107** | Incorrectness | §3.2 parallelization, FR-015, FR-034, Stream C vs Stream P | §3.2 sequences Stream C (the model-visible description corrections) ahead of Stream P, and justifies the intermediate state as *"until [P] lands the product has one Chrome and therefore no cookie partitioning — which is exactly today's behaviour, so the intermediate state is not a regression."* True for cookies, false for the descriptions. FR-034's replacement literal asserts the browser is *"shared across this workspace"*, which implies **not** shared beyond it — while `capture_shared_context: true` (`defaults.go:671`) still means one partition across **all** workspaces until P lands. That ships a false ownership claim to the model and the operator, which is the precise defect class ADR-075 §1.1 exists to fix. | Move FR-015/FR-034 into Stream P, or specify an interim literal that asserts only what is true before P lands (tab-set sharing, not cookie isolation). Add this to §5 as an explicit non-behaviour: no description may assert isolation before FR-037 ships. |
| **MAJ-108** | Ambiguity | FR-040, US-12/AC4, §5, behavioural contract 19, §8 idle-close scenario | The whole-Chrome idle close is specified five times as happening "past the idle window" and the window is never defined: no config key, no default value, no relationship to the existing per-tab `DefaultIdleTTL` (5 min, `manager.go:134`) or `cfg.IdleTTL`, and no named caller (the reaper? a new sweeper? on what tick?). Nor is the post-close state specified: is the `BrowserManager` destroyed, is its `browserMgrs` entry removed, and does the next tool call for that key relaunch or error? FR-026a's liveness predicate says the key stays live while the workspace exists, which implies the map entry survives a closed Chrome — a state the spec never describes. | Name the config key and default (or state it reuses `tools.browser.idle_ttl_sec`), name the caller, and add a §5 sentence and a test dataset row for the post-idle-close relaunch path: "W idle-closed, then a tool call arrives → Chrome relaunches from W's profile, login intact, `LiveKeys()` grows by one." |
| **MAJ-109** | Incompleteness | FR-038, §12 A15, §2.1 `maxTotalTabs` row | `tools.browser.max_browsers` is specified as "a positive integer" with reload-without-restart behaviour, and nothing else. Undefined: (a) the meaning of `0` or a negative value — the sibling key `max_total_tabs` treats `<=0` as **unlimited** (`coordinator.go:785-788`, and `TestCoordinator_UnlimitedDefault_AllowsPastOldCap` guards it), so an operator will reasonably expect the same and the spec never says; (b) the relationship between the browser cap and the existing **global** tab budget — is `maxTotalTabs` still global across N Chromes (one workspace can starve another) or now per-Chrome (total tabs become cap × budget)? §2.1 says only "joined by a browser cap"; (c) the default is measured on **one UAT box** (§0.3) and shipped to every host regardless of its RAM. | Specify the 0/negative semantics explicitly and make it match `max_total_tabs` or state why it differs. Decide and state whether the tab budget is global or per-Chrome, and add a test. Make the default a function of host memory, or state plainly that it is a fixed conservative constant and that operators on larger hosts must raise it. |
| **MAJ-110** | Incompleteness / Inoperability | FR-042, §5, §6 host memory | FR-042 replaces the single ownership marker with one per key (`$OMNIPUS_HOME/browser/ws-<id>.pid`). Nothing specifies **boot-time reconciliation** of those markers. Today there is one marker with a known adoption/cleanup story; with N, a crashed or `kill -9`'d gateway leaves N marker files and N orphan Chrome processes. On restart the pool's in-memory `LiveKeys()` starts empty, so the cap bounds only the Chromes *this process* launched — the orphans consume host memory outside the cap entirely, which defeats the cap's stated purpose (bounding host memory). | Add an FR: at boot, scan `$OMNIPUS_HOME/browser/ws-*.pid`, and for each marker either adopt the process or kill it and remove the marker; log the count. Add a test dataset row: "gateway restarts with 3 stale markers → 0 orphan Chromes and 0 stale markers remain." |
| **MAJ-111** | Incompleteness / Insecurity (Information Disclosure) | §5, FR-026, FR-040, ADR D2.11 | The profile directory's lifecycle is specified only for **creation** (FR-037) and for **not** being deleted on idle close (§5, FR-040, FR-043). Nothing specifies deletion, ever. Workspace deletion — the operator's "this client is gone" action — closes the Chrome (FR-026) and silently leaves that client's cookies, tokens and local storage on disk indefinitely. There is also no quota, no retention policy and no total-size bound for N Chrome profiles (each routinely hundreds of MB), on a product whose stated storage model is file-based with a 90-day session retention. | Add an FR deciding profile-directory deletion on workspace deletion (delete, with the logout stated; or retain, with an operator-facing purge action and a release-note line). State the directory mode explicitly — the existing code creates profile dirs `0700` (`coordinator.go:1232`, `manager.go:799`) and these now hold per-client session cookies, so the constraint should be written down rather than inherited. |
| **MAJ-112** | Ambiguity / Infeasibility | §14.1 `leaseWaitTimeout`, FR-023, test 17 | §14.1 requires `leaseWaitTimeout` to be *"strictly less than the shortest action-tool timeout"* and never names that timeout. It is `BrowserConfig.PageTimeout` (default **30s**, `manager.go:35` and `:123`, operator-settable via `tools.browser.page_timeout_sec`). Because both values are operator-configurable and nothing validates the relationship, an operator can set `tools.browser.lease_wait` above `page_timeout_sec` and turn every contended call into a CDP timeout **error** where the contract (FR-020) requires a non-error deferral. `TestWriteLease_BoundedWait` asserts the relationship for the defaults only. | Name `BrowserConfig.PageTimeout` in §14.1. Add a config-validation requirement rejecting (or clamping, with a WARN) `lease_wait >= page_timeout_sec` at load and on reload, and a test for the rejection — this is a live gate, not a comment. |
| **MAJ-113** | Incorrectness / Overcomplexity | FR-007 rung 2, FR-033, US-6, §6 | Rung 2 and FR-033's refusal are designed against the premise that scheduled and heartbeat turns carry an empty `ToolWorkspaceID`. The shipped code contradicts that as the normal case: the scheduled-run path reads `meta.WorkspaceID` from the session meta and stamps it onto `processOptions.WorkspaceID` (`loop.go:6933-6957`), and its own comment names the resolvable sources — *"no heartbeat identity, no channel binding"* — as the conditions under which it is empty. The spec cites neither that write path (`stampScheduledSessionWorkspace`) nor `resolveWorkspaceIDForContinuation`, and never weighs the cheaper alternative (stamp the workspace on the turn, as scheduling already does) against FR-033's accepted cost. That cost is concrete and near-certain: Ray is seeded onto the default workspace by `defaultWorkspaceTeam`, so the first time an operator adds Ray to a second workspace, **every Ray heartbeat permanently loses the browser** — a silent capability loss triggered by an unrelated action. | Cite the existing stamping path in §6 and rung 1's rationale. Then either (a) extend that stamping to heartbeat turns so rung 1 covers them, and keep FR-033 as the genuine last resort; or (b) keep FR-033 and add a per-agent "browsing home workspace" so a multi-workspace agent's unattended turns resolve deterministically. Record the choice in §12 and the ADR. |
| **MAJ-114** | Insecurity (Elevation of Privilege) | §1 out-of-scope, ADR D2.11 | ADR D2.11 **decides**: *"Adding an agent to a workspace grants it every live session on that workspace. Decision: the team-editing UI must state this at the point of adding, not only in release notes."* §1's out-of-scope list excludes only "the D2.11 **information-disclosure** bullet", so the **elevation-of-privilege** bullet is in scope for this spec — and it has no FR, no AC, no US, no SPA work item and no §15 row. The round-1 review raised it as unasked question Q3 and it is unaddressed. D1.2 makes it strictly worse than when the ADR wrote it: unattended delegated work now inherits those logins too, and §0.2 records that risk while §5 forbids any mitigation. | Add an FR for the team-editing disclosure (Workspace → Team, at the point of adding an agent), traced to ADR D2.11, or amend §1 to place the elevation bullet explicitly out of scope with a named owning spec and issue. Silence on a decided security control is the gap. |

### MINOR

| ID | Lens | Section | Finding | Recommended fix |
|---|---|---|---|---|
| **MIN-101** | Incorrectness | §2.2 | The comment count is wrong and the quoted grep does not produce the quoted number. The command §2.2 names returns **57** lines, not 37: 37 usages + **2** declarations (`tools.go:63`, `:69`) + **18** comments — the spec says 12. The 6 uncounted comments are `tools.go:50,62,65,68`, `capture_session.go:708` and `config.go:3892`. `pkg/config/config.go` is missing from the file inventory entirely (one comment-only hit). Every one of the 37 cited line numbers is correct. | Restate as "57 grep hits = 37 usages + 2 declarations + 18 comments", fix the comment count, and add `pkg/config/config.go:3892` to the table. |
| **MIN-102** | Incompleteness | FR-002b, SC-013, test 5 | The test-side surface is unbudgeted: **364** references across **25** `_test.go` files (~357 executable; 312 exported / 52 unexported). `tabs_test.go` alone has 100. SC-013 counts only the 37 non-test usages, so the stated edit surface understates the real one by roughly 10×. See also CRIT-101. | Add the number to §2.2 and a §10.1 migration line item, so the estimate and the regression list agree. |
| **MIN-103** | Overcomplexity (residue) | §3.1 `key.go` | `BrowsingKey`'s value is `"ws:<workspaceID>"` and `WorkspaceID()` must strip a prefix that, by §0.2's own ruling, can never vary. Both the prefix and the accessor are residue of the withdrawn `BrowsingKeyKind`. Keeping them is defensible as future-proofing, but §0.2 explicitly deletes the second key shape and §5 forbids adding one. | Either drop the prefix (making `WorkspaceID()` a field read) or add one sentence saying the prefix is retained deliberately as a namespace marker despite there being one shape. Do not leave it unexplained next to a non-behaviour that forbids a second shape. |
| **MIN-104** | Incorrectness | §0.1, §6 sandbox | `pkg/gateway/sandbox_apply.go:405-417` is cited for the removed DevTools-port allow-rule. The removal note is at **:412-417**; `:405-409` is the `enforcePortRules` computation (`:407` `KernelChildConfiner`, `:408` the ABI check). The substance of the claim is correct. | Cite `:412-417`. |
| **MIN-105** | Incompleteness | FR-037, §12 A15 | `BrowserCoordinator.ApplyRuntimeConfig` branches on `oldCfg.ProfileDir != newCfg.ProfileDir` (`coordinator.go:681`). Under N per-key directories derived from `cfg.ProfileDir`, a runtime ProfileDir change relocates every workspace's profile and therefore every login. The spec does not say what happens. | Add a sentence: a `ProfileDir` change closes every pooled Chrome and re-derives every key's path (with the logout stated), or is rejected at reload. |
| **MIN-106** | Insecurity (defence in depth) | FR-037, FR-042 | `<cfg.ProfileDir>/ws/<workspaceID>/` interpolates a workspace id into a filesystem path with no stated constraint. It happens to be safe — ids are server-minted ULIDs (`rest_workspaces.go:495`, `:848`) — but the spec never records the property it depends on, so a future id-format change (an operator-chosen slug, an import) silently becomes a path-traversal surface. | State the invariant: workspace ids are server-generated ULIDs and the path segment must be validated (`filepath.Base`-equal, no separators) before use. Add it to §5 as a non-behaviour. |
| **MIN-107** | Incompleteness | US-14, FR-008a | The residual custom-agent case already has a shipped boot-time surface the spec does not reuse: `logWorkspacelessAgents(homePath, cfg)` (`gateway.go:5026`) exists precisely to surface, once at boot, agents that "silently cannot execute at all until manually added via a workspace's Team tab" (`gateway.go:5015-5025`). | Reference it in US-14 and FR-008a so the panel message and the boot log say the same thing, rather than inventing a second vocabulary for the same condition. |
| **MIN-108** | Inconsistency (source ADR) | ADR-075 D1.3 vs spec §2.1 | ADR-075 D1.3's key table still reads: *"Transcript session · `tools.ToolTranscriptSessionID(ctx)` · … · Used for: **Unattended delegated work — D1.2**"* — residue of the design D1.2 deleted. The spec correctly marks it "not used" (§2.1), so the ADR and its spec now disagree, and the ADR is the source of record. | Amend ADR-075 D1.3's table row to say the transcript session id is **not** a browsing key, per the superseding D1.2 ruling. |

### OBSERVATION

| ID | Lens | Section | Observation |
|---|---|---|---|
| **OBS-101** | Test integrity | SC-008, tests 4/6/32/38 | The four "must be red today" tests are all **expressible** against current code — test 4 structurally (all eleven structs hold `mgr`), test 6 by taking the control lock under a non-`"default"` id, test 32 by seeding `al.browserMgrs` with a `"ws:W"`-shaped key and running the prune, test 38 through `registerSharedTools`. So the claim is plausible. But no receipt is offered for any of them, and SC-008 is phrased as an intention ("must be confirmed to fail"). Per the project's own false-green rules, the cheapest thing that would make this real is four `exit=` receipts pasted into the PR before the implementing commits, not after. |
| **OBS-102** | Disposition audit | §15 MAJ-003 | The rejection is sound and reproduces exactly as claimed: `ensureDefaultWorkspace` (`rest_workspaces.go:468`) is called on every boot (`gateway.go:5013`), returns early only when a default workspace already exists (and then back-fills the built-in roster), and otherwise seeds "My Workspace" with `defaultWorkspaceTeam(cfg)` = `coreagent.All() ∩ configured agents` (`rest_workspace_delegation.go:359-379`), which includes Jim and Ray. Recording it here so a later reader does not re-litigate: this is the one rejected finding and the rejection is correct. Note the scope: it holds for the **default** workspace only, which is why the residual custom-agent case (US-14) is the right remaining concern. |
| **OBS-103** | Overcomplexity | §3.1, §0.2 | The round-1 OBS-002 complaint has genuinely been acted on — `BrowsingKeyKind`, the `browserCtxIDs` map and the `ViewerCounter` seam are all not built, and §15 says so. What remains (`BrowsingKey`, `TabState`, `BrowserResolveOutcome`, `ManagerResolver`, `BrowserPool`) is defensible: each is load-bearing, and `ManagerResolver`'s existence is forced by the import direction rather than chosen. `BrowserResolveOutcome` is the weakest of the five — four values whose only consumer is a panel string — but FR-008a's requirement that the four reasons be distinguishable is real, so it earns its place. No action. |

---

## 3. Structural integrity results (`plan-spec` mode)

| Check | Result | Note |
|---|---|---|
| Every user story has ≥1 acceptance scenario | **PASS** | US-1…US-17 all carry ACs; the round-1 PARTIALs on US-12/US-13 are closed. |
| Every acceptance scenario has ≥1 BDD scenario | **PASS (with two carried inside others)** | US-2/AC2 and US-11/AC1 are asserted inside neighbouring scenarios rather than having their own. Acceptable; §9's nine-FR exemption table is honest about the rest. |
| Every BDD scenario has a back-reference | **PASS** | Every §8 scenario names its US/AC and FRs inline. |
| Every BDD scenario has a corresponding test | **PASS** | Each maps to a §10 row. |
| Every FR appears in the traceability matrix | **PASS** | FR-001…FR-045, with FR-011/FR-012 retained as explicitly withdrawn rows. |
| Every BDD scenario appears in the matrix | **PASS** | |
| Test datasets cover boundaries, edges, errors | **PARTIAL** | Strong on resolution, lease and reload. **Missing:** `max_browsers` = 0 / negative (MAJ-109); the cap reached with **all N pinned by viewers** (CRIT-103's deadlock); idle-close → re-acquire → relaunch (MAJ-108); profile directory after workspace deletion (MAJ-111); stale Chrome singleton after a crash (MAJ-103); stale ownership markers at boot (MAJ-110); concurrent `Acquire` for two different keys at the cap boundary (which one wins). |
| Regression impact explicitly addressed | **PARTIAL** | A genuinely strong list — the `tools_control_test.go` correction and the "must be DELETED" entry for `TestCoordinator_Register_SharedContextMode_…` are both right and both non-obvious. Defeated by CRIT-101 (unmodified vs deleted constant) and by two missing call sites (MAJ-104 Preprovision, MAJ-105 `metadata.go`). |
| Success criteria measurable, no subjective language | **PARTIAL** | SC-001…SC-011, SC-013 and SC-014 are measurable and their failure modes are stated — a real improvement, and SC-007's third behavioural condition genuinely closes MAJ-004. **SC-012 has no failing check** (MAJ-106). **SC-015** is a documentation prerequisite with no mechanical gate; it will be satisfied by someone remembering. |
| Numeric/code claims reproduce | **MOSTLY PASS** | Verified correct: all 37 `DefaultSessionID` line numbers; the eleven `mgr:`-bearing structs at `register.go:65-81`; `controlledResult` at `tools.go:962-963`; the prune at `loop.go:2849`/`:2866`; `ListTabs`' `nil,0,nil` at `manager.go:1608-1611`; **all sixteen** `coordinator.go` symbols including the `:149` `pipeLauncher` seam, `:1424` lock path and `:1527` marker path; `defaults.go:671`; `exec_resolver.go:385` and `:60`; `tool_denial.go:205-210`; `ensureDefaultWorkspace`/`defaultWorkspaceTeam`. **Wrong:** the `ReapIdleSessions` behaviour claim (CRIT-102); the comment count and the quoted grep's total (MIN-101); the `sandbox_apply.go` span (MIN-104). |

---

## 4. Test coverage assessment

**What improved.** The four round-1 unfalsifiable gates are addressed with real
substance, not restatement: test 38 goes through `registerSharedTools` rather
than a hand-built manager; SC-007 gains a behavioural third condition (test 46)
that can actually fail for the semantic reversal; `tools_control_test.go` is
correctly identified as the `controlledResult` guard and `shared_control_test.go`
correctly disqualified; and the "must be DELETED" ruling on
`TestCoordinator_Register_SharedContextMode_ReturnsRootCtxAndEmptyBrowserCtxID`
is exactly right — a rename would have left a green test asserting the absence
of the product's headline guarantee.

**Tests that still cannot fail for the defect they name**

1. **Test 37 / G-2** — `skipIfNoBrowser` skips unless `OMNIPUS_BROWSER_E2E=1`,
   and no gate sets it. The load-bearing assumption of the entire D1.1a design
   is guarded by a test that reports green without running (MAJ-106).
2. **Test 18 (`TestWriteLease_EveryActionToolIsLeased`)** — it *can* fail, and
   in fact will fail today for `browser_list_tabs` (CRIT-104). But its only
   possible predicate is membership of a hand-written list living in the same
   document, and that list has already been widened once to stop it failing
   (MAJ-102). It polices discipline, not correctness.
3. **SC-012** — no executable form at all.

**Missing tests, in priority order**

1. **Reaper × pool interaction** — `ReapIdleSessions` cancels `se.browserCancel`
   while the pool still holds a live entry for that key (CRIT-102). Nothing in
   §10 exercises the two together.
2. **Pool full with every browser pinned by a viewer** — CRIT-103's permanent
   refusal. The current cap test uses idle browsers, which is the easy case.
3. **Relaunch after idle close** — the BDD scenario asserts the profile survives
   and "when W is used again… still logged in", but no §10 row owns the
   relaunch path or the manager/`browserMgrs` state across it (MAJ-108).
4. **Stale Chrome singleton and stale ownership marker recovery** — MAJ-103 and
   MAJ-110; both are crash-path behaviours that FR-043's "profile on disk"
   promise depends on.
5. **Boot Preprovision under a lazy pool** — MAJ-104; assert the managed-Chromium
   download still starts at boot on a fresh install.
6. **`metadata.go`'s second construction of all eleven tools** — MAJ-105; a
   compile-level test would do.
7. **`lease_wait >= page_timeout_sec` rejected at config load** — MAJ-112.
8. **Test-surface migration** — 364 references; CRIT-101 makes this a build gate,
   not a nice-to-have.

**On the regression list.** The corrections it makes (8 not 9 tests in
`shared_control_test.go`; `tools_control_test.go` as the real FR-022 guard; the
delete-don't-rename ruling) are all verified correct and are the strongest part
of the round-2 rewrite. The list is defeated only by CRIT-101's "unmodified"
clause, which is a wording problem with a compile-level consequence.

---

## 5. STRIDE summary

| Component | Threat | Addressed? |
|---|---|---|
| Per-workspace Chrome profile (cookies, tokens, local storage) | **Information disclosure** — cross-workspace leak | **YES, by design** — FR-037's profile-directory isolation, asserted by distinct pids and distinct `--user-data-dir` paths under the shipped default (SC-002). This closes round-1 CRIT-001 properly. **Conditional on gate G-2**, which is currently unenforced (MAJ-106). |
| Per-workspace Chrome profile | **Information disclosure** — data at rest after deletion | **NO.** No deletion path on workspace deletion; a departed client's logins persist on disk indefinitely, with no quota and no purge (MAJ-111). Directory mode is inherited (`0700`) but unstated. |
| Workspace team membership | **Elevation of privilege** — adding an agent grants every live login | **NO.** Decided in ADR D2.11, in scope per §1, owned by nobody, no FR (MAJ-114). Made worse by D1.2: unattended delegated work now inherits those logins. |
| Unattended delegated sub-turn | **Spoofing** — a background agent acts as the signed-in human | **Accepted, once, explicitly** (§0.2). The single remaining gate is `browser_upload_file`'s `ask` seed, and FR-032 correctly promotes #659 to a D1 prerequisite. Honest treatment; the residual risk is real and stated. |
| Human control lock (ADR-038 D6) | **Tampering** — an agent drives while a human holds the wheel | **YES.** FR-002c re-keys `controlledResult`, is assigned to Stream A, and SC-009 nominates the correct guard (`tools_control_test.go`). Round-1 CRIT-005 properly closed. |
| Browser action tools | **Tampering** — interleaved writes to one tab | **PARTIAL.** §14 is a single normative definition (CRIT-004 closed), but the exempt set is internally inconsistent (MAJ-101), self-contradictory (MAJ-102), and omits a registered read-only tool (CRIT-104). |
| Browsing-key use | **Repudiation** — "which agent acted as the signed-in user" | **YES.** FR-027 plus FR-035's provenance assertion; the first-use-only limitation is stated rather than implied. |
| Browser-process pool | **Denial of service** — resource exhaustion / permanent refusal | **PARTIAL.** The cap exists (FR-038) and refusal-not-eviction is a sound choice (FR-039), but the cap does not bound orphan Chromes across a restart (MAJ-110), the edge semantics are undefined (MAJ-109), and the refusal can become permanent with an ineffective remedy (CRIT-103). |
| Workspace-less / multi-workspace turn | **Elevation of privilege** — acting with an arbitrary workspace's logins | **YES, and with the stronger option.** FR-033 refuses rather than arbitrating. But the cost is understated and a cheaper path exists in shipped code (MAJ-113). |
| Live-panel frames | **Spoofing** — `session_id` becomes a workspace selector | **PARTIAL.** FR-016/FR-017 resolve server-side and SC-007's third condition makes the reversal falsifiable (round-1 MAJ-004 closed). Still nothing states that the caller must be **authorised** for the session it names — resolution is by session meta, and ownership of that session is not asserted anywhere. |

---

## 6. Unasked questions

1. **When the reaper cancels `se.browserCancel`, what is the pool's view of that
   key?** The spec believes the reaper does nothing; it cancels the browser
   context and calls `coord.ReleaseTab` (CRIT-102). Is `pool.Close` called, is
   the entry left live, or does the key now name a Chrome nobody can drive?
2. **How does a slot ever become free while people are working?** With N
   workspaces holding tabs, nothing in the spec closes a browser, and the
   refusal text names two actions that do not (CRIT-103). What is the operator
   supposed to actually do?
3. **Is `cfg.ProfileDir` still a user-data-dir, or does it become a parent
   directory?** The answer changes `InstallRootForProfileDir`'s arithmetic
   (`exec_resolver.go:50`), `cleanStaleSingletons`' target, and what an existing
   operator's configured `tools.browser.profile_dir` means after upgrade.
4. **What deletes a workspace's profile directory, ever?** And what is the
   release-note answer to "I deleted the client's workspace; are their logins
   gone?"
5. **Does the cap bound host Chrome count, or only this process's?** After a
   `kill -9`, N orphan Chromes and N stale markers exist and the fresh pool
   counts zero.
6. **Who owns ADR D2.11's elevation-of-privilege disclosure?** It is decided, it
   is in scope by §1's wording, and it appears in neither spec.
7. **Why is FR-033's refusal cheaper than stamping the workspace on the turn?**
   The scheduled path already stamps it (`loop.go:6933-6957`). The refusal costs
   a multi-workspace agent its browser on every heartbeat, permanently.
8. **What does `browser_list_tabs` do under contention?** It is not exempt and
   not discussed; under FR-019a it must take a write lease, which makes the
   headline handover call defer.
9. **What happens to a blocked leaseholder after `browser_handle_dialog` clears
   its modal?** It resumes holding the lease and acts on a page that changed
   underneath it.
10. **If G-2 fails, what is the rollback?** §0.4 has already landed by then,
    including FR-015/FR-034's description changes asserting workspace-scoped
    sharing (MAJ-107). Is the plan to revert them, or to ship a false claim
    while D1.1a is re-opened?

---

## 7. Next action

```
Verdict: BLOCK

Review written to:
  /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf/docs/internal/specs/browser-workspace-ownership-spec-review-round2.md

Address in this order:
  CRIT-102  — correct the ReapIdleSessions claim, then specify the reaper x pool contract
  CRIT-101  — resolve "unmodified" vs "constant deleted"; budget the 364 test references
  CRIT-103  — give the pool a way to free a slot, and fix the refusal text
  CRIT-104  — add browser_list_tabs to the exempt set; re-derive the set from the registry
  MAJ-101/102 (lease annex self-consistency), MAJ-106 (make the gates real),
  MAJ-103/108/109/110/111 (pool + profile lifecycle), MAJ-104/105 (missing call sites),
  MAJ-107 (description sequencing), MAJ-112, MAJ-113, MAJ-114

To revise:
  /plan-spec --revise docs/internal/specs/browser-workspace-ownership-spec.md docs/internal/specs/browser-workspace-ownership-spec-review-round2.md
```
