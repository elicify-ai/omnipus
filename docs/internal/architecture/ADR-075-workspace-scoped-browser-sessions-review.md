# Adversarial review — ADR-075 (Browser tools: workspace-scoped, and usable by an agent)

- **Reviewed:** `docs/internal/architecture/ADR-075-workspace-scoped-browser-sessions.md` (566 lines, Status: Proposed, dated 2026-08-31)
- **Worktree:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf` (branch `feat/browser-streaming-performance`, HEAD `87fe00ae`)
- **Review date:** 2026-08-31
- **Mode:** structured-spec (D-numbered decisions + numbered acceptance criteria; no BDD/traceability matrix)

---

## 1. Executive summary

Twenty-six findings: **5 CRITICAL, 9 MAJOR, 10 MINOR, 3 OBSERVATION**. The
document states two mutually exclusive keys as its own decision (routing
session in §2.1, workspace in D1.1) — a residue of the third reshape that was
never removed — and its acceptance table contains a criterion (4) that the
decision it accompanies makes unsatisfiable by design. Its "no leak between
unrelated work" guarantee is defeated by its own fallback rule for turns with
no workspace, which is the majority of unattended execution paths. One
load-bearing measurement in §7 does not reproduce from the commit it cites.

**Verdict: BLOCK.**

---

## 2. Findings

### CRITICAL

| ID | Lens | Section | Finding | Recommended fix |
|---|---|---|---|---|
| **C1** | Inconsistency | §2.1 vs D1 / D1.1 | **The ADR names two different keys as its decision.** D1.1 (line 197): "Key browsing contexts by the **workspace id**… `tools.ToolWorkspaceID(ctx)`". §2.1 (line 222): "So the **operator-facing browser is keyed by the routing session**". §2.1's whole table and §2.2's body are survivals of the deleted conversation-keyed draft (commit `e6136777`, "isolation moves to the conversation axis") that the workspace revision (`87fe00ae`) did not clear. An implementer reading top-to-bottom lands on the routing session, which D1.0 explicitly rejects at line 160-166. | Delete §2.1's conclusion sentence and rewrite its table as: workspace id = the browsing-context key; transcript session id = the D1.2 escape hatch. Drop the routing-session row entirely, or keep it only in the D1.0 comparison table where it is labelled "earlier draft". |
| **C2** | Inconsistency | §3 criterion 4 vs D1.0 ¶2 / D1.2 | **Acceptance criterion 4 is unsatisfiable by the decision as written.** Criterion 4 requires "the sub-agent does not hijack the tab the operator is reading", and line 410 classes it among "the regressions this design must not introduce". But D1.0 consequence 2 states "Unattended delegated work **shares the jar by default**", and D1.2 states the narrower transcript-session key is "**Not enabled by default**". A delegated sub-turn inherits the same workspace id, therefore the same browsing context, therefore the same tabs. The design introduces criterion 4's regression rather than avoiding it. | Choose one and make the document say it: either (a) demote 4 to a known, accepted consequence with an explicit operator ruling, and remove it from the acceptance table and from line 410; or (b) make the transcript-session key the default for delegated sub-turns and give D1.2 a real trigger condition, then keep 4 as a criterion. Do not ship a criterion whose failure is the design. |
| **C3** | Incompleteness / Insecurity | §2.2 | **The no-workspace fallback re-creates the exact defect §2.2 forbids in its own next sentence.** §2.2 prescribes "Those keep a context keyed by the constant, as today", then warns that "an empty key silently colliding every unattended run into one shared browsing context is the same class of defect this ADR exists to remove". Keying on the constant *is* that collision under a different name. This is not a rare path: `tools.ToolWorkspaceID` returns `""` for scheduled and heartbeat turns (`pkg/tools/resolvepath.go:695-709` records this explicitly) and for any agent not on a workspace CoreTeam — `loop.go:7988` only sets it from `ts.opts.WorkspaceID`. Today those agents are isolated from one another by the per-agent manager; under D1 they all merge into one cookie jar. **This is an isolation regression against the ADR-043 D2 guarantee that criterion 5b exists to prove.** | Decide the fallback explicitly and state it as a decision, not a leftover: the defensible option is per-agent keying (`tools.ToolAgentID`) for workspace-less turns, which preserves today's isolation exactly. Add an acceptance criterion: "two workspace-less agents log in to the same site; neither sees the other's session." Do not fall back to a shared constant. |
| **C4** | Inconsistency (with ADR-043) | D1.0 line 188-190 vs §4 "Blast radius" | **D1.0 claims ADR-043 D3 does not change; it must.** D1.0: "What does not change: … ADR-043 D1/**D3**/D4". ADR-043 D3 is the live-view binding, and its own text says "`agent_id` was *always* the binding key" with "**NO wire-schema change required**". In code that binding is live at three gateway call sites — `pkg/gateway/browser_webrtc.go:279`, `pkg/gateway/browser_ws.go:1252`, `pkg/gateway/browser_inspect.go:73`, all `BrowserManagerForAgent(frame.AgentId)`. With one manager per workspace there is no per-agent manager to resolve, and an agent on two workspaces makes `agent_id` ambiguous. §4 concedes this ("the live panel's session resolution") and D1.0 denies it. | Correct D1.0 to say ADR-043 D3 **is amended**, and add a decision covering how the gateway resolves a manager: either the frame carries a workspace id (a `contracts/` change under Hard Constraint #8, with the 5-step process) or the gateway maps agent→workspace server-side and the ADR states what happens for a multi-workspace agent. Neither is stated today. |
| **C5** | Infeasibility | D2.3 / D2.4 / §3 criteria 8-13 | **D2 adds ~6 static builtin tools and never mentions tool policy — boot will abort.** Hard Constraint #6: every static builtin tool must resolve from an explicit, literal, wildcard-free policy entry for **every** agent, enforced by boot validation that "aborts with a listed `agent × tool` report on any gap" and by 400 on every agent create/update. `browser_select_option`, `browser_press_key`, `browser_hover`, `browser_upload_file`, the dialog tool and the AX-snapshot tool each need a seeded entry in `pkg/config/defaults.go` **and** in every per-agent block of `pkg/coreagent/core.go`. The ADR contains no decision on default posture (allow for Jim/Ray? deny for Mia/Ava? `ask` for `browser_upload_file`, which reads local files?) and no acceptance criterion covering it. | Add a D2 sub-decision naming each new tool's seeded policy per core agent, with a rationale for `browser_upload_file` in particular (it crosses the filesystem boundary into a remote site — `ask` is the defensible seed). Add an acceptance criterion: "a fresh install boots with all new browser tools present and no policy-coverage abort." |

### MAJOR

| ID | Lens | Section | Finding | Recommended fix |
|---|---|---|---|---|
| **M1** | Incorrectness | §7 line 549-550 | **The measurement that retires the prior ADR-075 draft does not reproduce.** §7 claims the pixel-budget cap "halved the warm-capture cost (**57% → 29% of a core** in the identical condition)". Commit `08d21393`'s message — the cited source — contains no post-fix measurement at all. Its table reads `boot warm capture, NO viewer   chrome 57.0% of a core, machine 29.1%`: 57.0 and 29.1 are two *columns* of the same *pre-fix* row. The ADR has read the machine column as a post-fix core figure. The only genuine post-change evidence is the operator's qualitative verdict, which §7 also quotes. | Either produce the actual post-fix `/proc` sample in the identical condition and cite it, or replace the sentence with what is evidenced: the operator's verdict plus the commit's own encoder benchmark (1 fps → 18 fps at a quarter of the pixels). Per `docs/internal/false-green-patterns.md`, a number nobody can reproduce is worse than no number. |
| **M2** | Inconsistency (with ADR-043) | D1.0 | **D1 reverses a recorded ADR-043 ruling without citing it.** ADR-043 "Accepted limitations": "If an operator specifically wants agents to *share* a login (e.g., Jim logs in, Ray reuses it), that becomes a **deliberate per-context opt-in (future), not the default**." D1 makes precisely that the default while asserting "What ADR-043 was protecting still holds — and is better expressed." It does hold on the *inter*-workspace axis and is deliberately discarded on the *intra*-workspace axis. | Quote the ADR-043 sentence in D1.0 and state that this ADR **overrides** it, with the 2026-08-31 operator ruling as the authority. An amendment that silently reverses a named limitation of the ADR it amends is how the next reader concludes the reversal was accidental. |
| **M3** | Incompleteness | §4 "Concurrency" vs §6 Q1 | **The largest self-declared risk in D1 has no decision and no acceptance criterion.** §4 calls agent-vs-agent and chat-vs-chat contention "the largest open risk in D1"; §6 leaves the rule ("one workspace, one driver at a time"?) an open question. Verified in code: `controlledResult` (`pkg/tools/browser/tools.go:962`) checks only `mgr.Live().IsControlled(...)` — a *human viewer* holding the live view. Its own doc comment records two further limits: read-only tools (`browser_screenshot`, `browser_get_text`, `browser_wait`) are deliberately **not** gated, and the mechanism is "cooperative, not preemptive… no mid-tool preemption in v1". A workspace-keyed context makes interleaved agent writes to one tab the normal case, not an exception. | Decide it in this ADR, not after. Minimum: a per-browsing-context write lease held for the duration of one action tool, with the same non-error `{"deferred": true, "reason"}` shape `controlledResult` already returns so the LLM path is unchanged. Add acceptance criterion: "two agents on one workspace issue `browser_navigate` concurrently; neither observes the other's mid-navigation state, and neither errors." Ship D1 with no answer here and the failure mode is nondeterministic, which §5 itself calls "the most expensive kind for an agent". |
| **M4** | Incorrectness | §4 "Idle reaping" / §6 Q3 | **Reaping is presented as undefined; it is already implemented, and the ADR's prescription is already the design.** `BrowserManager.ReapIdleSessions` exists (`pkg/tools/browser/manager.go:2986`) with `DefaultIdleTTL = 5 * time.Minute` (`:134`). Its documented contract at `:82-96` states reaping "runs **PER TAB**, not per browsing context" and is "gated on real activity" — exactly what §4 says "must be defined". Meanwhile the two risks the re-key genuinely creates go unnamed: (a) the viewer-attach counter at `:248` ("a context with a viewer attached is NEVER idle") now pins an entire **workspace's** context whenever any one chat has the panel open; (b) the zero-tab branch keyed on session `lastActivity` (`:242-244`) now governs a context that outlives every agent that touched it. | Replace the §4 bullet and §6 Q3 with a citation of `ReapIdleSessions` and a decision on those two specific interactions. If per-tab reaping is judged sufficient unchanged, say so and add a criterion: "a workspace context with all tabs idle past `IdleTTL` and no viewer attached is reaped." |
| **M5** | Incompleteness | §3 criterion 3 | **Criterion 3 is false for most agents; tool policy is never mentioned in the ADR.** "any agent on that workspace sees it" — but seeing it requires `browser_list_tabs: allow`, and the seed grants the browser surface to Jim and Ray only (`pkg/coreagent/core.go:756-760, 910-918, 1052-1061`). Mia — the default agent, and the agent in the ADR's own §1.1 repro — is not among them. Under D1 the operator's tab is in the workspace's context and Mia still cannot enumerate it, so the reported symptom ("Jim says zero tabs") recurs verbatim for any policy-denied agent, now with a *different* cause and the same indistinguishable output. | Restate criterion 3 as "any agent on that workspace **whose tool policy allows the browser surface**", and decide whether the seed should grant `browser_list_tabs` more widely. §2.3's "no browsing context" vs "no tabs" distinction must gain a third state — "you are not permitted to see this" — or the original defect survives the fix. |
| **M6** | Incorrectness | D2.7 | **The decision to close #456 is falsified by the paragraph immediately below it.** The rationale is "Chromium is not fetched on demand in any shipping configuration" and "a gate for a state that shipping installs do not enter". The next paragraph then documents linux/arm64: no bundled `chromium/` payload, no upstream build to download, dependent on a system Chrome behind `TrustPathChrome` **seeded `false`**. That is a shipping install that enters exactly that state, permanently. On those hosts every `browser_*` tool is registered and guaranteed to fail — which is what #456 asked to prevent. Filing #665 addresses distribution; it does not make the manifest gate unnecessary in the interim. | Reopen the question or narrow the decision: "close #456 for x86-64 Linux, macOS and the Docker heavy image; the linux/arm64 case is covered by #665 and until it lands the tools remain visible-but-failing." State that trade-off rather than asserting the state does not occur. |
| **M7** | Insecurity (STRIDE) | whole document | **A credential-boundary change ships with no security analysis.** D1 changes who can act as a logged-in user. The ADR has no security section and no audit requirement. Concretely: **Elevation of privilege** — adding an agent to a team silently grants it every live session on that workspace (D1.0 ¶1 names this and files it under "Consequences", not "Decisions"). **Repudiation** — no audit event is specified for an agent's first use of a browsing context it did not establish, so "which agent acted as the signed-in user" is unanswerable after the fact. **Information disclosure** — D2.4's AX snapshot returns full page structure including, on a logged-in page, account identifiers and form values; nothing states whether it is redacted or how it interacts with `RegisterSensitiveValues`. | Add a §"Security" with: an audit event on browsing-context creation and on first cross-agent use; an explicit statement that the AX snapshot inherits `browser_get_text`'s redaction posture (or a decision that it does not, with rationale); and a decision on whether the team-editing UI must disclose the grant (D1.0 currently says "arguably", which is not a decision). |
| **M8** | Inconsistency | D2.0 vs D2.7 vs §4 | **The tool count is stated three different ways.** D2.0: "**Eleven** tools" (7 + 4 tab tools) — correct; `register.go:65-81` registers 11 and ADR-071 §4.1's Tier 3 list enumerates 11 `browser_*` names. D2.7: "all **twelve** `browser_*` tools are now Tier 3". §4: "Eleven tools become **sixteen-ish**" — D2 adds four verbs + dialog handling + an AX snapshot, which is 17, and "sixteen-ish" hides whether dialog handling is one tool or a mode. | Fix D2.7 to eleven. Replace "sixteen-ish" with the enumerated post-change list, since D2.7's manifest-cost argument and C5's policy-seeding both depend on the exact set. |
| **M9** | Incompleteness (with ADR-071) | D2.3 / D2.4 | **New tools are added to a tier list ADR-071 defines as closed.** ADR-071 §4.1 fixes Tier 3 as an enumerated 63-name list, with the stated property that "every name in it resolves to `ManifestSearchOnly`" and that "promoting one out of Tier 3 must force a re-decision rather than leave a stale entry". D2 adds ~6 names and never says which tier they enter. §4 asserts the manifest cost is "bounded" because ADR-071 puts "them all in Tier 3" — an assumption about tools that do not exist yet. | State the tier per new tool in D2, and note the ADR-071 list edit as a required change. If the AX snapshot is intended to be the *default* way an agent reads a page (D2.4's own wording), Tier 3 / search-only is arguably wrong for it — that tension deserves a sentence. |

### MINOR

| ID | Lens | Section | Finding | Recommended fix |
|---|---|---|---|---|
| m1 | Incorrectness | header line 13-15 | **Two wrong ADR citations.** "ADR-062 (WebRTC connectivity tiers)" — ADR-062 is *"Reads and execute default open; writes stay confined"*. The live-browser connectivity ADR is **ADR-069**. "ADR-044 (single listener)" — ADR-044 is *"Live-browser streaming target architecture — WebCodecs relay"*; the root `CLAUDE.md` also labels ADR-044 as the single listener, so one of the two is wrong and the ADR does not say which it means. | Change ADR-062 → ADR-069. Verify which ADR is the single-listener decision and cite it, or drop the reference — ADR-044 is never used in the body. |
| m2 | Incorrectness | D1.0 line 188 | "ADR-040's take-the-wheel model" — ADR-040 is *"Reverse FR-H-006's Registry-Level 'One Level Only' Delegation Block"*. Take-the-wheel is ADR-038 D6 / ADR-039. (ADR-043's header carries the same error, so this is inherited, not invented.) | Cite the correct ADR and, since the error is now in two files, note the correction so it stops propagating. |
| m3 | Inconsistency | D1.0 heading + line 130 | **Direction of reference is inverted.** The heading reads "read this before §1.2" and the body "§1.2 **below** describes…" — §1.2 is at line 43, D1.0 at line 128. §1.2's own note ("Read D1.0 with this section") is correct. Editing artifact from the reshape. | "§1.2 **above**", and change the heading to "read this alongside §1.2". |
| m4 | Inconsistency | §2 structure | **Numbering breaks mid-decision.** Under `## D1 — Ownership` the subsections run D1.0, D1.1, D1.2, then `### 2.1`, `### 2.2`, `### 2.3` — top-level-looking numbers nested inside `## 2. Decision` → `## D1`. §2.3 is a D1 decision ("the silent zero is removed") wearing a §2 number. | Renumber to D1.3 / D1.4 / D1.5, or hoist §2.1-2.3 above the D1/D2 split as shared context. |
| m5 | Incorrectness | §2.1 line 226-232 | **Two stated counts do not reproduce.** "**14** across `pkg/tools/browser/`" — actual non-test occurrences of `defaultSessionID`/`DefaultSessionID` are **30** (≈18 of them argument passes). "**Six** tool descriptions… contain the literal phrase" — actual: **4** tool descriptions (`tabs.go:32,86,143,206`), **1** parameter description (`tools.go:415`), plus **2** Go comments (`tabs.go:19,186`) that are not descriptions, and an 8th occurrence outside the package. ("9 call sites in `tools.go`" is correct.) | Recount or drop the numbers. They are cited as scope evidence, so a reader who checks and finds them wrong discounts the rest. |
| m6 | Inconsistency | D2.7 lines 387-390 | Duplicated sentence: "The managed download cannot rescue it for the same reason" then "The managed download cannot rescue it either — it fetches from the same upstream that has no linux-arm64 build." | Keep the second (it carries the reason), delete the first. |
| m7 | Infeasibility | §4 D2 "Per-action cost" | "It must not turn a fast click into a slow one on pages that were fine" is unmeasurable — no threshold, no baseline. §7's own measurements show the target host at 85-99% machine utilisation while a viewer is attached, so added per-click round trips land on a box the same document proves is saturated. | State a budget (e.g. "the actionability pre-check adds ≤ 150 ms p95 to a click on an already-actionable element") and make it a numbered acceptance criterion, measured on the `performance-2x` profile §7 uses. |
| m8 | Ambiguity | D1.0 line 153 | "Case 3 is not an edge case" forward-references acceptance criteria defined 250 lines later, with no pointer. | Add "(§3, case 3)". |
| m9 | Incompleteness | D2.5 | `web_serve` mints a per-**agent** preview URL (`/preview/<agent>/<token>/`) while D1 makes the browsing context per-**workspace**. Whether a preview minted by agent A is reachable from a tab another agent on the workspace drives is not stated. | One sentence in D2.5 confirming the preview token is agent-scoped and that this is intentional, or a decision to re-key it. |
| m10 | Inconsistency | D2.7 line 383 | Status is **Proposed**, yet D2.7 records "**Decision: close #456.** … Closed 2026-08-31" as a completed action. A proposed ADR has already executed an irreversible step. | Either move the ADR to Accepted for D2.7 specifically, or record it as "recommend closing #456 on acceptance". |

### OBSERVATION

| ID | Lens | Section | Finding |
|---|---|---|---|
| O1 | Overcomplexity | §2 line 116-119 | The ADR states D1 and D2 "ship independently" and are "separated here so each can be reviewed on its own" — then binds them into one Proposed document. D2 is additive, low-risk and mostly uncontested; D1 carries an unresolved concurrency question (M3), an isolation regression (C3) and a wire-contract change (C4). As one ADR, D2 cannot be accepted until D1's questions are answered. Split into ADR-075 (ownership) and ADR-073 (capability). |
| O2 | Overcomplexity | D1.2 | The escape hatch is a mechanism with no user — "not enabled by default", no trigger condition, no configuration surface named, "recorded so the mechanism is not rebuilt later". That is speculative generality by the definition in this project's own review constitution. It is also, per C2, the only thing that would satisfy criterion 4 — so either it has a user today and should be specified, or it does not and criterion 4 should go. |
| O3 | Incompleteness | §6 Q2 | "Should the live panel's WebRTC session (`browser-webrtc[<agent>]`) also re-key to the workspace?" is answerable from the code rather than left open: the label is formatted from `agentID` at `pkg/gateway/browser_webrtc.go:968-971`, derived from the same `frame.AgentId` that C4 shows must change anyway. The label is cosmetic; the resolution it sits next to is not. |

---

## 3. Structural integrity results

| Check | Result | Note |
|---|---|---|
| Every stated goal has acceptance criteria | **FAIL** | D1's concurrency decision (§4, §6 Q1) and the no-workspace fallback (§2.2) have none — M3, C3. D2's new tools have no policy-coverage criterion — C5. |
| Cross-references internally consistent | **FAIL** | m1, m2 (wrong ADRs); m3 (inverted direction); C4 (D1.0 vs §4 on ADR-043 D3). |
| Scope boundaries explicit | **PASS** | D2.6 is a genuine, well-drawn out-of-scope list. |
| Success criteria measurable | **PARTIAL** | Criteria 1-3, 5-5c, 8-11, 13-14 are testable. Criterion 4 is unsatisfiable (C2); criterion 7's "which condition was unmet" needs the condition set enumerated; §4's per-action cost bound is unmeasurable (m7). |
| Requirements referencing each other consistent | **FAIL** | C1 (two keys), C2 (criterion 4 vs D1.0/D1.2), M8 (three tool counts). |
| Error/failure scenarios addressed | **PARTIAL** | Strong on the wedged-tab case (D2.3, criterion 12). Absent for: workspace-id resolution failure, concurrent writes, an agent lacking browser policy, browsing-context creation failure. |
| Dependencies identified | **PARTIAL** | ADR-043/041/038 named. **Missing:** Hard Constraint #6 (C5), Hard Constraint #8 / `contracts/` (C4), ADR-071's closed tier list (M9), the existing `ReapIdleSessions` (M4). |
| Numeric claims reproduce from code | **FAIL** | m5 (two counts wrong, one right), M1 (§7 measurement), M8 (tool count). |

---

## 4. Test coverage assessment

The ADR's §3 doubles as its test plan, and the plan is thinner than the risk.

**Untestable as written:** criterion 4 (C2 — the design fails it). Criterion 3
(M5 — true only for policy-allowed agents). §4's per-action cost bound (m7 — no
threshold).

**Missing negative and concurrency cases — the ones that matter most here:**

1. **Two agents, one workspace, concurrent action tools** on the same tab. This
   is §4's own "largest open risk" and has no test. `pkg/tools/browser/stress_5agents_test.go`
   exists and exercises the *per-agent* model — it will need rewriting, not
   extending, and the ADR should say so.
2. **Two chats, one workspace, concurrent browsing.** Distinct from (1):
   `controlledResult` is keyed on the live view, and two chats can both have a
   viewer attached.
3. **Workspace-less turn isolation** (C3): two heartbeat/scheduled agents log in
   to the same site; assert neither sees the other's session. This is the
   criterion that would have caught C3 before implementation.
4. **Policy-denied agent** calls `browser_list_tabs` on a workspace with tabs
   open: assert the result is distinguishable from both "no context" and "no
   tabs" (§2.3's distinction needs a third state — M5).
5. **Reaping under the new key** (M4): all tabs idle past `IdleTTL`, no viewer →
   reaped; one viewer attached in one chat → the whole workspace context
   survives (assert the intended behaviour, whichever it is).
6. **Boot with the new D2 tools registered** and no seeded policy → assert the
   coverage validator aborts, then assert a fresh install boots clean (C5).

**Regression risk the ADR does not name:** `d2_spike_test.go`,
`stress_5agents_test.go`, `tab_adoption_e2e_test.go`, `shared_control_test.go`
and `tools_control_test.go` all encode the per-agent model. §4's "Blast radius"
bullet mentions call sites and the live panel but not the test suite that
currently asserts the behaviour being replaced. A green run after the change is
only meaningful if those tests were rewritten rather than left asserting a model
that no longer exists.

---

## 5. STRIDE summary

| Component | Threat | Present in ADR? |
|---|---|---|
| Workspace browsing context (cookies/localStorage) | **Elevation of privilege** — joining a team grants every live login on it | Named in D1.0 ¶1 as a consequence; no decision, no disclosure requirement (M7) |
| Workspace browsing context | **Information disclosure** — cross-workspace leak | Addressed: criterion 5b |
| Constant-keyed fallback context | **Information disclosure** — every workspace-less agent shares one jar | **No** — C3; the ADR asserts the opposite |
| Delegated sub-turn | **Spoofing** — background agent acts as the signed-in human on a live site | Named in D1.0 ¶2; mitigation exists but is off by default (C2) |
| Browser action tools | **Tampering** — interleaved agent writes to one tab | **No** — M3, open question only |
| Any browsing-context use | **Repudiation** — no audit event for cross-agent use of a context | **No** — M7 |
| `browser_upload_file` (D2.3) | **Information disclosure** — local file exfiltration to a remote site | **No** — no policy seed, no path confinement statement (C5) |
| AX snapshot (D2.4) | **Information disclosure** — account data and form values in tool output | **No** — no redaction posture stated (M7) |
| `browser_evaluate` / dialog handling | **Denial of service** — wedged tab blocks all CDP | Addressed well: D2.3 hazard note + criterion 12 |

---

## 6. Unasked questions

1. **What is the workspace id when the human browses first?** Case 3 is the
   headline scenario, and the live panel is opened from a chat via
   `BrowserManagerForAgent(frame.AgentId)`. If the operator is chatting with an
   agent that has no workspace (or is on two), which context does the human's
   own browsing land in? The ADR never says, and this is the case it exists to fix.
2. **What happens when an agent is on two workspaces?** ADR-065 makes
   agent×workspace the ownership unit elsewhere in the system. The ADR assumes
   one workspace per turn without stating it.
3. **What happens to existing browsing contexts on upgrade?** Per-agent contexts
   exist on disk in `~/.omnipus/browser/profiles/`. Are they discarded, merged
   into a workspace context, or orphaned? A merge would pool logins from agents
   that never shared them.
4. **Does a workspace deletion dispose its browsing context?** `RemoveAgent` /
   `disposeBrowserContextRaw` are the agent-lifetime hooks today; the workspace
   equivalent is not named.
5. **Which conditions exactly does criterion 7 enumerate?** D2.2 names four
   (visible/stable/enabled/hit-testable). Criterion 7 says the failure must name
   "which condition" — the test needs the closed set and the exact strings.
6. **Is `browser_upload_file` confined to the agent's workspace root?** ADR-062
   governs read/exec; uploading a local file to a remote origin is a new egress
   path the ADR does not connect to it.
7. **Does the AX snapshot count against the ADR-066 context budget?** A full
   accessibility tree on a real page is large, and D2.4 proposes it as "the
   default way an agent reads a page".
8. **Who is the operator of record for the 2026-08-31 ruling?** D1.0 cites
   "Operator ruling, 2026-08-31" for the workspace-over-conversation choice.
   ADR-043 names its deciders; this one does not.

---

## 7. Next action

```
Verdict: BLOCK

Review written to:
  /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf/docs/internal/architecture/ADR-075-workspace-scoped-browser-sessions-review.md

Address C1-C5 first — C1 and C2 are internal contradictions that make the
document's own decision unreadable, C3 is an isolation regression, C4 is a
contradiction with the ADR being amended, and C5 blocks boot. Then re-run:

  /grill-spec docs/internal/architecture/ADR-075-workspace-scoped-browser-sessions.md
```
