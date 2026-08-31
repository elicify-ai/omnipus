# Feature Specification: Tool Manifest Tier Redesign

**Created**: 2026-08-28
**Revision**: 2 — first adversarial review (BLOCK: 1 CRITICAL, 6 MAJOR, 3 MINOR, 2 OBSERVATION) resolved in full, plus four further errors found by this pass's own re-verification against ADR-071 and source. See the second dated block under Clarifications.
**Status**: Draft
**Input**: [`docs/internal/architecture/ADR-071-tool-manifest-tier-redesign.md`](../architecture/ADR-071-tool-manifest-tier-redesign.md) (revision 5, 2026-08-27). Five decisions D1–D5, five workstreams W-D1/W1/W2/W3/W4 (+W5 docs), ratification section §11 with 8 open items.

**Release-phase routing**: out of band. Not v0.1, not v0.2, not v0.3 — per the ADR's own routing note it neither blocks nor is blocked by any of the three phases. Ships on its own branch.

---

## Available Reference Patterns

`docs/reference/go-implementation/` **does not exist in this repository**. Verified: neither `docs/reference/` nor `docs/reference/go-implementation/00-overview.md` is present at `release/v0.1.1`.

Independently, the reference library that path describes (auth/sessions, Stripe Connect, email notifications, S3 presigned URLs, webhook handling, organization RBAC, subscription lifecycle, middleware composition, config management) has **no overlap** with this feature. This is tool-catalog classification, in-process policy resolution, BM25 ranking and prompt-cache block ordering — none of which the reference library covers.

**No reference patterns apply. This section is retained empty rather than deleted so a reader does not assume it was skipped.**

The patterns this feature *does* reuse are all in-repo precedents, recorded under Existing Codebase Context below rather than here:

| In-repo precedent | What it supplies |
|---|---|
| ADR-036 §3.6 | The convert-and-clean-once policy-key migration shape D4 follows verbatim |
| The `toolMetricsRecorder` / `nopToolMetrics` / `SetToolMetricsRecorder` trio in `pkg/tools/compositor.go` | The zero-import-cycle path from `pkg/tools` to the gateway's `/metrics` endpoint |
| `TestCatalog_MatchesGlobalCeilingEntryForEntry` | The drift-test shape both new name-set drift tests mirror |
| ADR-057's `manifestSessionID` doc comment | The bucket-key invariant ("derive from the two ids you are GIVEN, never widen either") the re-key must preserve |

---

## Existing Codebase Context

> **GitNexus was unavailable for this spec.** The MCP tools (`query`, `context`, `impact`, `trace`, `explain`) are not exposed in this session, and `~/.gitnexus/registry.json` shows this checkout's index was last built 2026-08-23 — five days stale against the current branch. Per the plan-spec skill's fallback instruction, the table below was built by **direct verification against source** at `release/v0.1.1`, not from the graph. Every row was read, not recalled.

### Symbols Involved

| Symbol | Role | Verified state |
|---|---|---|
| `pkg/tools/manifest.go::ToolManifestTier` | extends | Pure name lookup, no per-tool state, no visibility axis. Gains a sibling classifier, not a fourth constant |
| `pkg/tools/manifest.go::fullManifestToolNames` | modifies | **18 entries confirmed** by count. Becomes the 17-name always-listed set. **The membership is ADR-071 §4.1's literal Tier 1 list and nothing else** — see "Tier membership: one source of truth" below for the full 6-out/5-in diff and the reason this row does not restate it |
| `pkg/tools/manifest.go::infraManifestToolNames` | modifies | Sole member is the loader tool's name |
| `pkg/tools/manifest.go::BuildCompressedManifest` | modifies | Renders the block. Gains one visibility filter line |
| `pkg/tools/tools_tool.go::ToolsTool.Name()` | modifies | Returns `"load_tool"` at line 79 |
| `pkg/tools/tools_tool.go::execSearchAndLoad` | modifies | **CRIT-201 confirmed verbatim**: `matches := make([]ToolSearchResult, len(ranked))` is built from the full ranked list **before** the `canLoad` loop, and returned in `resp["matches"]` on the success path and as `json.Marshal(matches)` on the branch whose own comment reads `// No resolver or all results denied by policy.` |
| `pkg/tools/registry.go::SnapshotSearchableTools` | reads (must not change) | Admits core tools on `ToolManifestTier(name) == ManifestLazy`, with no policy input |
| `pkg/tools/registry.go::ExcludedHandoff` | modifies | `ExcludedTool = "hand_off"`, consumed by `CloneExcept` |
| `pkg/agent/tool_manifest.go::manifestSessionID` | replaces | Derives the bucket from transcript id / session key, **no agent component** |
| `pkg/agent/loop.go::registerSharedTools` registration guard | modifies | **Confirmed**: `if _, alreadyTools := agent.Tools.Get("load_tool"); !alreadyTools` — a raw literal, not `InfraManifestToolNames()`. Cited by `file::symbol` rather than `file:line` because CLAUDE.md records `loop.go` as an ~11k-line file under constant churn whose line numbers go stale within days (it was at `:2552` when this spec was written) |
| `pkg/gateway/websocket.go` frame branches | modifies | **Both confirmed**: `p.Tool == "hand_off"` at `:3878`, `p.Tool == "return_to_default"` at `:3899` |
| `pkg/gateway/replay.go` | **no edit** | Matches a content prefix, not a tool name. Confirmed |
| `pkg/coreagent/core.go::allStaticToolNames` | modifies | **90 names confirmed** by exact count over lines 351–415 (a naive count returns 92; two of those are the quoted words `"ask"` and `"allow"` inside comments). 90 − 2 + 1 = **89** post-D4, matching the ADR's arithmetic |
| `src/lib/toolVisibility.ts` | modifies | **Both sites confirmed**: `case 'load_tool'` at line 104, `tool !== 'load_tool'` at line 242, `default: return true` at line 179 |
| `src/test/canonicalToolNames.test.ts` | modifies | **Tautology confirmed verbatim** at lines 124–125: `const canonical = 'load_tool'` then `expect(canonical).toBe('load_tool')` — a local variable compared to itself, referencing no production symbol |

### Tier membership: one source of truth

> **Rule for this whole document: `ADR-071 §4.1`'s four literal name lists are the *only* authority on which tool sits at which exposure level.** No table, dataset row, scenario or test in this specification may re-derive that membership independently. Where a table below names tools, it is quoting §4.1, and the drift tests (`TestVisibility_TierArithmetic`, `TestVisibility_PreviewedSetIsExactlyEight`, `TestAdministrativeToolNames_Drift`) MUST be seeded from §4.1's lists, never from a count and never from a spec-side restatement.
>
> This rule exists because the first draft of this section broke it. It stated the always-listed diff as *"`bash` out, `hand_off`+`return_to_default` out, `switch_agent`+`recall_conversation` in"* — 3 out, 2 in. That is wrong, and it was invisible to every gate in the plan: 18 − 3 + 2 and 18 − 6 + 5 both equal **17**, so a count-only check passes on either. The two diffs produce entirely different sets, and the wrong one keeps the task-mutation verbs at permanent visibility — defeating the stated purpose of User Story 4 while CI stays green.

**The always-listed diff, verified against ADR-071 §4.1's literal Tier 1 list and against the current `fullManifestToolNames` map in `pkg/tools/manifest.go` (18 entries) — 6 leave, 5 join, 12 stay, net 17:**

| | Names | Where they go |
|---|---|---|
| **Leave the always-listed set (6)** | `bash`, `navigate`, `create_task`, `update_task`, `hand_off`, `return_to_default` | `bash`, `navigate`, `create_task`, `update_task` → **previewed** (Tier 2). `hand_off` and `return_to_default` are **deleted**, merged into `switch_agent` |
| **Join the always-listed set (5)** | `switch_agent`, `list_mounts`, `send_file`, `message_parent`, `recall_conversation` | `switch_agent` is net-new (the merge target); the other four are promoted from today's lazy set |
| **Stay (12)** | `read_file`, `write_file`, `edit_file`, `list_directory`, `search_web`, `fetch_url`, `send_message`, `remember`, `recall_memory`, `set_todos`, `list_tasks`, `delegate` | unchanged |

18 − 6 + 5 = **17**. Set arithmetic re-verified in session against both sources: the union of §4.1's Tier 1 (17) + Tier 2 (8) + Tier 3 (63) is exactly the 88-name post-merge static catalog, plus 1 infrastructure name = **89**.

> **Reader calibration — "moves down a tier" understates what happens to `bash`, `navigate`, `create_task` and `update_task`.** Previewed (Tier 2) is not a cosmetic dimming of always-listed (Tier 1); per FR-030 it is a subdivision of the pre-existing *lazy* tier, which means callable-schema-every-turn becomes **not callable until a discovery round trip has happened**. In a fresh conversation the first use of any of these four costs one extra request/response cycle before any work is done — and `create_task`/`update_task` are routine, high-frequency verbs, so that cost lands on ordinary task management rather than on an exotic path. It is paid **once per conversation per tool**, because FR-037 makes the promotion permanent for the conversation, and the four tools remain fully registered and fully permission-governed throughout. This is the intended, deliberate trade recorded in ADR-071 §4.1/§4.2 and this specification does not re-argue it; the note exists because a reviewer reading only the reclassification table above sees a visibility change and can miss the latency one.

### Pinned Identifiers

> Every load-bearing identifier this specification refers to by description, with its literal value as ADR-071 pins it. The specification deliberately describes *behaviour* in product language everywhere else; this table is where the concrete engineering names live, so no implementer has to infer one or re-derive it from the ADR.

| Concept as described in this spec | Literal identifier | Source |
|---|---|---|
| "the discovery capability", its new name | `ToolSearch` (replacing `load_tool`) | ADR §2, D1 |
| "the switching capability" | `switch_agent` (replacing `hand_off` and `return_to_default`) | ADR §5.1, D4 |
| "the three retired names" | `load_tool`, `hand_off`, `return_to_default` | ADR §2.1, §5.2.2c |
| "the reserved destination word" | the literal string `"default"`, compared **exactly** (see FR-056) | ADR §5.1, §5.1.3 |
| "the revert control" | `PreviewAllLazy bool` on `ManifestConfig` (`pkg/config/config.go`), JSON/YAML key `preview_all_lazy`, **default `false`**; read inside `ToolManifestVisibility` so both manifest builders inherit it with no second branch | ADR §4.3.1(b), §4.4 |
| "the exposure-level classification" (new second axis) | `ManifestVisibility` with `ManifestPreviewed` / `ManifestSearchOnly`; resolver `ToolManifestVisibility(name)` | ADR §4.4 |
| "the previewed set" backing data | `previewedLazyToolNames` (exactly the 8 Tier 2 names) | ADR §4.4 |
| "the destructive-and-install-wide list" | `administrativeToolNames` in `pkg/tools/manifest.go`, unexported, adjacent to `previewedLazyToolNames`; 13-name seed; plus `administrativeExemptNames` for adjudicated exemptions | ADR §3.2.1 |
| "kind", in the closeness comparison | `Tool.Category()` equality — the `ToolCategory` enum in `pkg/tools/base.go`. **Not** the same mechanism as the destructive list (see FR-021/FR-025) | ADR §3.2 rule 2, §3.2.1 |
| "the closeness thresholds and the cap" | `searchAmbiguityRatio = 0.80`, `searchCrossCategoryRatio = 0.50`, `searchMaxAutoLoad = 3`, unexported in `pkg/tools` | ADR §3.2 |
| "the bound on permission checks" | `searchCanLoadScanCap = 50` | ADR §3.2.2 |
| "the empty-result counter" | `omnipus_toolsearch_zero_result_total` | ADR §4.3.1(a) |
| "the unused-discovery counter" | `omnipus_toolsearch_no_followup_total` | ADR §4.3.1(a) |
| "the recorded content prefix" | `"Handoff: "` — FROZEN | ADR §5.2.2a |
| "the moved block" and "the volatile record" | `BuildStaticToolCatalog(lazyTools []Tool)` and `BuildLoadedToolsDelta(loaded map[string]bool)`, split out of `BuildCompressedManifest` | ADR §6.1 |
| "the moved block's own estimated size" | `B = estimateMessageTokens(BuildStaticToolCatalog(...))` — a **token** count | ADR §6.6 |
| "cache-read volume" | `providers.Usage.CacheReadTokens` — **tokens**, populated by the Anthropic adapter only | ADR §6.5, §6.6 |
| "the usable-set record" and "the pending-discovery record" | the session `loadedTools` bucket (re-keyed per ADR §4.6) and `pendingSearchPromotions` | ADR §4.3.1(a), §4.6 |
| "made usable", for a **static catalog** tool — **the new, permanent mechanism this specification builds** | `al.loadedTools[<agent, conversation>][name] = true`, written by `pkg/agent/loop.go::markToolsLoaded`, read by `buildCompressedToolDefs` and `buildToolManifestNote` (`pkg/agent/tool_manifest.go`) gating on `loaded[name]`. **No TTL, no cap, no eviction.** The only removal is `forgetSession` at conversation close (`pkg/agent/session_end.go`) | ADR §1.1.1, §3.3, §4.6 |
| "the discovery lifetime" — **the pre-existing, unrelated MCP mechanism, untouched by this work** | `cfg.Tools.MCP.Discovery.TTL`, **default 5**. Applied by `pkg/tools/registry.go::PromoteTools` (stamps `entry.TTL`), decremented once per turn by `TickTTL`, read as an admission test by `GetAll`/`Get`/`ToProviderDefs` (`entry.IsCore \|\| entry.TTL > 0`) | ADR §1.1.1 |
| "an externally-provided tool" | a tool registered by a connected MCP server via `RegisterHidden`/`RegisterHiddenMCP` — **non-core (`IsCore: false`), `TTL 0` at registration**. Never a member of the 89-name static catalog, and never reaches the per-turn listing at all | ADR §1.1.1, §1.2 F3 |

> **The two promotion mechanisms — read this before implementing FR-037, and before reading any "must not evict" sentence in this document.**
>
> A tool becomes callable after a discovery call by **one of two structurally independent mechanisms**, selected by whether the tool is core (static) or non-core (MCP). ADR-071 §1.1.1 records the conflation of these two as the single most load-bearing error in its own five-revision history (its `CRIT-101`); this specification inherits that correction rather than re-deriving it.
>
> | | **Static catalog tools** — all 89, i.e. everything this specification is about | **Externally-provided (MCP) tools** |
> |---|---|---|
> | Registered by | `ToolRegistry.Register` → `IsCore: true` | `RegisterHidden`/`RegisterHiddenMCP` → `IsCore: false` |
> | What makes it callable | `markToolsLoaded` sets `loadedTools[<agent, conversation>][name]` | `PromoteTools` stamps `entry.TTL` |
> | **Decays?** | **No.** No TTL, no cap, no eviction | **Yes.** `TickTTL` decrements once per turn |
> | Removed when | conversation close, by `forgetSession` | `TTL` reaches 0 |
> | Governed by FR-037's prohibition? | **Yes** — FR-037 forbids building decay *here* | **No** — FR-037a preserves this behaviour unchanged |
>
> The two are wired together at one call site — `execLoad`/`execSearchAndLoad` call `PromoteTools` **and** `markLoaded` on every load — which is what made the conflation easy to miss. But `PromoteTools` mutates `entry.TTL` only `if !entry.IsCore`, `TickTTL` decrements only `if !entry.IsCore && entry.TTL > 0`, and `GetAll` admits a core entry **unconditionally** whatever its TTL says. **For every static tool, the entire TTL path is a literal no-op.** The registry TTL governs MCP discovery and nothing else.
>
> Consequently: `TestStaticPromotion_SurvivesBeyondDiscoveryLifetime` and `TestExternalPromotion_DecaysWithDiscoveryLifetime` are **not** in tension. They assert opposite outcomes because they exercise two different mechanisms, and the pair exists precisely so the split is documented by the test file itself.

### Impact Assessment

| Symbol Modified | Risk | Direct dependents that MUST be updated | Indirect dependents that SHOULD be tested |
|---|---|---|---|
| `ToolsTool.Name()` | **CRITICAL** | Classifier map key, static catalog, global ceiling, 4 per-agent seed maps, the `loop.go:2552` guard, contract schema, 2 SPA visibility sites | 12 Go test files, 7 TS test files, 4 E2E specs, 1 UAT probe script |
| `execSearchAndLoad` | **CRITICAL** | The `matches` construction, both response branches, the `canLoad` loop's `break` | Ambiguity band (D2) consumes its output list |
| `ExcludedHandoff` | **HIGH** (security) | `CloneExcept`, the `subturn.go` slog literal, 3 loud-failing tests, 1 vacuous test | Every delegated sub-turn |
| `websocket.go` frame branches | **HIGH** | Both branches, collapsed to one | SPA active-agent indicator. **Zero test coverage exists today** |
| `manifestSessionID` | **MEDIUM** | 1 writer, 2 readers, `forgetSession`, 1 ADR-057 test | Every turn's callable tool array |
| `allStaticToolNames` | **HIGH** | Seed maps (ordering is strict — reversing panics at boot), global ceiling, the one-for-one invariant test | Boot-time coverage validator on every install |
| `contentBlocks` ordering (D5) | **MEDIUM** | Block index 1 placement | Every Anthropic request's cache prefix |

**Two HIGH/CRITICAL items whose failure mode is a green build**, flagged per the skill's instruction to surface them prominently:

1. `toolVisibility.ts` left unrenamed → every discovery call becomes visible in every chat thread and panel, for every user. The 7 TS test files pass either way, because they pass the legacy string *as an argument* into a `case` arm that still exists.
2. The legacy-policy-key migration ordered after the coverage repair instead of before it → the repair backfills the two new names to explicit `deny` on every agent, and **boot succeeds**.

### Sites found beyond the ADR's own tables

The ADR's §10 states its surface tables "should still be re-verified by grep before W-D1 and W1 are called done, not treated as complete because they say they are," and records that every prior pass found something. **This pass found six more.**

| # | Site | Class | Why it matters |
|---|---|---|---|
| 1 | `tests/sandbox-uat/probe-t.sh` | **FUNCTIONAL** | Sends the literal instruction *"Use the load_tool tool to load the tool named …"* to a live agent as a UAT prompt, at 3+ call sites, and pass/fail-reports on the response. Post-rename the probe instructs the agent to call a tool that does not exist, so T.14/T.15 report FAIL for a reason unrelated to what they test. Not in the ADR's §2.1 table — the ADR's grep scope named `tests/` but the table has no row for `tests/sandbox-uat/` |
| 2 | `pkg/gateway/spa/assets/BrowserTool-*.js` | **FUNCTIONAL, build-artifact** | The **embedded** SPA bundle. Per CLAUDE.md's SPA Embed Pipeline the binary embeds from `pkg/gateway/spa/`, *not* `dist/spa/`. Fixing `toolVisibility.ts` in source without re-running the embed sync ships the **old** visibility behaviour in the binary while the source and the SPA suite are both correct. This is the ADR's §2.1(a) regression surviving its own fix. The ADR does not mention the embed pipeline at all |
| 3 | `pkg/tools/handoff_adr057_test.go` | Test (message string) | Asserts a failure message naming `return_to_default`. Not in the ADR's 11-file D4 test list |
| 4 | `pkg/agent/subturn_target_identity_test.go` | Comment | Two references. Not in the ADR's 12-file D1 Go test list |
| 5 | `pkg/agent/tool_surface_budget_test.go` | Comment | One reference. Not in the ADR's D1 list |
| 6 | `tests/e2e/{cancel-cross-channel,conformance-design-replan-e2e,subagent,handoff}.spec.ts` | Comment | Four E2E specs carrying explanatory `load_tool` prose. The ADR lists `handoff.spec.ts` for D4 only, and no E2E spec for D1 |

Items 1 and 2 are functional and are folded into the requirements below (FR-007, FR-008). Items 3–6 are prose/message-only and belong to the blanket pass (FR-009).

### Relevant Execution Flows

| Flow | Relevance |
|---|---|
| Turn build → system-context assembly | Where the manifest block is injected (as a separate system message at index 1 of the message list) and where D5 moves the catalog into the cached system parts |
| Tool discovery (query path) | The single function all of D1, D2 and the policy-filter fix land in |
| Tool discovery (exact-name path) | Explicitly unchanged by D2. Shares the rename and the policy resolver |
| Delegated sub-turn spawn | Consumes the handoff exclusion; carries the live fabricated-success defect this spec declines to fix |
| Agent handoff / return-to-default | Merged into one tool; drives the active-agent frame and transcript replay |
| Boot: repair → validate tool-policy coverage | Where the legacy-key migration must run first |
| Session close | Where both loaded-tool maps are swept |

### Cluster Placement

This feature spans **four** functional areas — tool catalog and classification, the agent turn-build path, the gateway's wire/frame surface, and install-time configuration. That breadth is the reason the ADR splits it into five workstreams with a strict partial order rather than one change, and it is why the sequencing constraints in the TDD plan are load-bearing rather than advisory.

---

## User Stories & Acceptance Criteria

### User Story 1 — Discovery results respect the caller's own permissions (Priority: P0)

An agent asks the discovery capability to find a tool by describing what it needs. Today the capability answers with the name **and the full description** of every ranked match, regardless of whether that agent is permitted to use them — the permission check is applied only when deciding which single tool to make callable. So an agent whose permissions deny a destructive capability can still learn that the capability exists, what it is called, and what it does, from an unrelated query that merely ranks it highly.

Until now this leaked nothing new, because every rarely-used tool was already named in the per-turn catalog every agent sees. User Story 4 removes that catalog listing for most of them — at which point discovery becomes the channel through which those tools are supposed to be *reachable but not advertised*, and an unfiltered answer defeats the whole point. This story makes the discovery answer obey the same permission boundary the rest of the system already obeys.

**Why this priority**: P0. It is the only story here that closes a live disclosure gap rather than adding or reshaping a capability, and User Story 4's central property is false without it. It must ship **before or with** Story 4, never after.

**Independent Test**: Give one agent permission to use a tool and a second agent a denial for that same tool. Issue a query from each that ranks the tool highly. The permitted agent sees it in the answer; the denied agent sees no trace of its name or description, and the answer's remaining entries appear in the same relative order for both.

**Acceptance Scenarios**:

1. **Given** an agent whose permissions deny a particular tool, and a query that ranks that tool among the top results but not first, **When** the agent runs the query, **Then** neither the tool's name nor its description appears anywhere in the answer, and the permitted tools around it are still present in their original relative order.
2. **Given** an agent whose permissions deny every tool a query ranks, **When** the agent runs the query, **Then** the answer is the same "nothing matched" response the system already gives for a query with no results, **and** no listing of any kind is returned.
3. **Given** the discovery capability is running in a configuration with no permission resolver attached, **When** any query is run, **Then** the answer discloses nothing.
4. **Given** two agents with different permissions, **When** both run the same query, **Then** the results each is permitted to see are ranked identically relative to one another — one agent's denials never change another agent's ordering or scores.
5. **Given** a query whose permitted results are empty, **When** it completes, **Then** the operator-visible counter for empty discovery results increases by exactly one.
6. **Given** an agent probes for a tool by a near-miss misspelling of a name it is not permitted to use, **When** the system offers a "did you mean" suggestion, **Then** the denied name is not suggested.

---

### User Story 2 — The discovery capability carries a name that matches the harness operators already know (Priority: P1)

Operators work across this system and a second agentic harness with an equivalent capability under a different name. The mismatch costs them a translation step every time they move between the two. Renaming the capability removes that friction. Nothing about what it does changes.

The rename is deceptively large. The capability's name is a literal string in a boot-time permission catalog, in per-agent permission seeds, in a published interface description, in a guard that prevents double-registration, and — most consequentially — in two places that decide whether the capability's calls are shown to the user in chat. Miss the last two and every discovery call becomes visible in every conversation, which reverses a documented interface rule at the exact moment Story 4 makes discovery the most frequently used capability in the system. The automated checks pass either way.

**Why this priority**: P1. It is operator convenience rather than a defect fix, so it does not outrank the P0 stories — but it carries a user-visible regression risk that ships green, so it is sequenced **first and alone**, as one isolated change reviewed on its own, before any behavioural work begins.

**Independent Test**: Rename nothing else. Confirm the capability answers to its new name, that its calls remain hidden from the chat thread and the activity panel by default, that a user who has enabled verbose output still sees them, and that the boot-time permission validator still finds an explicit entry for it on every agent.

**Acceptance Scenarios**:

1. **Given** an agent with permission to use the discovery capability, **When** it invokes the capability by its new name, **Then** the capability behaves exactly as it did under its old name for both the by-name and by-description paths.
2. **Given** a completed discovery call, **When** a user views the conversation with default settings, **Then** the call is not shown in the chat thread and is not shown in the activity panel.
3. **Given** the same call, **When** a user has enabled verbose output, **Then** the call is shown.
4. **Given** a stored conversation recorded before the rename, **When** a user views it, **Then** the old calls still render with a readable label rather than a raw identifier.
5. **Given** the system is asked to rebuild its tool registry while already running, **When** the registration path executes a second time, **Then** the discovery capability is registered exactly once.
6. **Given** the published interface description of a tool's exposure level, **When** it is read, **Then** it names the capability by its current name and does not name any capability that no longer exists.
7. **Given** the regression guard that exists specifically to pin this capability's name across renames, **When** it runs, **Then** it fails if the name in the production code and the name it asserts have diverged.

---

### User Story 3 — An underdetermined search returns the plausible alternatives, not one guess (Priority: P1)

When an agent describes what it needs and the description genuinely matches two or three tools, the system today picks the single best-ranked one and makes only that one usable. If it picked wrong, the agent must issue a second discovery call before it can do any work — a full round trip and a fresh round of reasoning, paid for a guess the system already had enough information to know was uncertain. The ranking scores that would reveal the uncertainty are computed and then discarded.

This story makes the system act on what it already knows: when the top results are close, or when they straddle two different kinds of tool, make the top few usable together so the agent can pick correctly on its first attempt.

**Why this priority**: P1. It is a latency and quality improvement on the path Story 4 makes central, not a defect fix. It depends on Story 1 having already produced a permission-respecting result list, so it is sequenced after it.

**Why this story gets no revert control while Story 4 does — the asymmetry is deliberate, not an oversight.** Both stories ship a judgement made without usage data, so the question is fair. Three differences decide it, and they are recorded here rather than left implicit:

1. **What is at stake differs in kind.** Story 4 changes what *every* agent on *every* upgraded install can see, on every turn, all at once — an agent that cannot find a capability has no recourse and the failure reads as incompetence rather than as a missing tool. Story 3 changes only how many schemas one deliberate search returns: at most two extra, on the subset of searches that are genuinely underdetermined. Its bad case is a wasted schema, not an unreachable capability.
2. **It cannot widen what an agent may do.** Every promoted candidate passes the same permission check first, and execution still runs the full policy gate including the confirmation path. What widens is the *reachability of a schema*, never the reachability of a capability — and the speculative half of the rule is additionally narrowed so it never surfaces a confirmation-gated or state-destroying tool. Story 4's bet has no comparable containment: it removes information outright.
3. **A documented, non-code first response already exists, and Story 4's does not.** A rising unused-discovery counter has a recorded remedy ladder — tighten the cross-kind ratio, then the score-band ratio, then the cap — and the honest statement is that it needs a release. That is acceptable *because* the failure it responds to is a cost signal on a metric, not a capability outage, and because reverting Story 3 to single-promotion is the same one-line change as tightening it. Story 4's failure mode is an operator discovering mid-incident that their agents cannot do their jobs, which is exactly the case where waiting for a binary is not acceptable.

If the operator disagrees with this reasoning, the cheap answer is a second boolean alongside Story 4's revert control that forces single-promotion. It is deliberately **not** specified here, because an untriggered escape hatch is surface that accretes, and the three points above say this one is not earned. Recorded as an OPERATOR row in the Ambiguity Warnings table so the decision is reviewed rather than assumed.

**Independent Test**: Issue a query engineered to produce two near-tied results and confirm both become usable in one call. Issue a query with one dominant result and confirm exactly one becomes usable, byte-for-byte as before.

**Acceptance Scenarios**:

1. **Given** a query whose permitted results include a runner-up scoring at least 80% of the top result's score, **When** the query runs, **Then** both are made usable in the same answer, and the answer explains that more than one was made available.
2. **Given** a query whose top result is dominant and whose runners-up all score below both thresholds, **When** the query runs, **Then** exactly one tool is made usable and the answer is indistinguishable from today's.
3. **Given** a query whose second result scores at least 50% of the top result's score **and** belongs to a different kind of tool, **When** the query runs, **Then** both are made usable.
4. **Given** the same conditions as scenario 3 but where the runner-up is one the agent would be asked to confirm before use, **or** is on the list of tools that destroy state or change install-wide settings, **When** the query runs, **Then** the runner-up is **not** made usable and only the top result is.
5. **Given** a query where four or more results qualify, **When** it runs, **Then** at most three tools are made usable, chosen as the highest-ranked qualifiers.
6. **Given** a query with exactly one permitted result, **When** it runs, **Then** that one is made usable with no comparison performed.
7. **Given** an agent asks for tools by exact name rather than by description, **When** the request runs, **Then** nothing about its behaviour changes.
8. **Given** the list of tools excluded from the speculative band, **When** a tool is added to or removed from the catalog, **Then** the build fails unless someone has recorded a deliberate decision about whether it belongs on that list.

---

### User Story 4 — Rarely-used tools stop consuming attention and context on every turn (Priority: P1)

Every turn, the system spends context describing roughly seventy tools an ordinary conversational agent will never touch — workspace and channel administration, provider configuration, the browser primitives, the planning verbs. That listing is rebuilt and re-sent uncached each turn, and it competes for the model's attention with the tools it actually needs. Meanwhile a general-purpose shell tool sits permanently visible alongside narrower tools that each cost a discovery round trip, which reliably biases the model toward shelling out instead of using the purpose-built tool.

This story introduces a third exposure level. A small set of tools stays listed by name each turn; the large remainder stays fully registered, fully permission-governed and fully findable, but is not advertised until an agent goes looking. The shell tool moves down one level so it no longer holds a permanent visibility advantage, and the memory-recall tools move up, because under the current history model a recall tool the agent cannot see reads to a user as the agent having forgotten.

The cost is real and was accepted deliberately: an agent that does not already suspect a capability exists has no free way to notice it. Two things make that acceptance honest rather than blind — a way for an operator to see, from live signals, that the split is wrong, and a way to put it back without waiting for a new binary.

**Why this priority**: P1. It is the largest behavioural change here and the source of the headline saving, but it is a deliberate, reversible product bet rather than a defect fix. It **must not ship** until Story 1 has landed.

**Independent Test**: Start a fresh conversation and confirm the per-turn listing names exactly the small previewed set and nothing else, while every unlisted tool remains findable by description and usable once found. Then set the revert control and confirm the full listing returns.

**Acceptance Scenarios**:

1. **Given** a fresh conversation with an agent permitted everything, **When** the first turn is built, **Then** the per-turn listing names exactly the eight previewed tools and renders as 22 lines.
2. **Given** the same conversation, **When** the agent describes work only an unlisted tool can do, **Then** that tool is found and made usable.
3. **Given** an unlisted **static catalog** tool made usable earlier in a conversation, **When** later turns are built, **Then** it remains usable for the rest of that conversation without a second search — permanently, with no decay path built for it. *(An externally-provided MCP tool is a different mechanism and keeps its pre-existing discovery lifetime; see FR-037a.)*
4. **Given** one agent has made an unlisted tool usable, **When** the conversation switches to a second agent whose permissions also allow that tool, **Then** the second agent does **not** have it usable and must find it itself.
5. **Given** the conversation switches back to the first agent, **When** its next turn is built, **Then** the tool it found earlier is still usable — the switch away did not reset it.
6. **Given** a conversation that made tools usable under two different agents, **When** the conversation ends, **Then** no record of either agent's usable set is retained.
7. **Given** an agent runs a search and is given a tool it never uses, **When** five further turns pass without it being used, **Then** the operator-visible counter for unused discoveries increases by exactly one, and does not increase again on later turns for the same discovery.
8. **Given** the same discovery is used on the very next turn, **When** many further turns pass, **Then** that counter does not increase for it.
9. **Given** an operator sets the revert control, **When** the next turn is built, **Then** every findable tool is listed by name again, exactly as before this change.
10. **Given** a new tool is added to the catalog, **When** the build runs, **Then** it fails unless someone has recorded which exposure level the tool belongs to.
11. **Given** an operator requests the metrics endpoint, **When** the response is read, **Then** both counter series are present by name.
12. **Given** the per-turn listing, **When** the model reads its header, **Then** the header states that more tools exist than are listed and that they can be found by describing what is needed.

---

### User Story 5 — One capability for changing which agent holds the conversation (Priority: P0)

Two separate tools do one thing today: one hands the conversation to a named agent, the other returns it to the default agent. They take differently-named parameters with opposite meanings — one is a forward-looking brief for the incoming agent, the other a backward-looking report on work finished — and their internals diverge in four further ways that were never reconciled. This story merges them into one capability whose destination is a parameter, continuing the pattern this codebase has already applied twice.

The merge is security-relevant. A delegated sub-task is deliberately prevented from seizing the user's live conversation, and that prevention works by matching the handoff tool's exact name. Rename the tool without moving the exclusion and a delegated sub-task regains the ability to reassign the user's conversation. Separately, the signal that tells the interface which agent is now active is produced by two branches that each match an exact tool name — and the more common of the two has **no test coverage anywhere in the repository**, so its most likely regression would ship green.

**Why this priority**: P0. It touches a live security boundary and an untested user-visible signal. Both need coverage written before the change, not after.

**Independent Test**: Hand a conversation to a named agent and confirm the interface's active-agent indicator follows it. Return to default and confirm the indicator clears. Then delegate a sub-task and confirm it cannot reassign the parent conversation.

**Acceptance Scenarios**:

1. **Given** an agent in a live conversation, **When** it switches the conversation to another agent by that agent's identifier, **Then** the conversation's active agent becomes that agent and the interface is told which one.
2. **Given** an agent in a live conversation, **When** it switches to the reserved destination meaning "the default agent", **Then** the conversation returns to the configured default agent and the interface is told the default is active.
3. **Given** a switch that fails, **When** it returns, **Then** the interface is told nothing — no active-agent signal is produced.
4. **Given** a delegated sub-task, **When** it attempts to switch the parent conversation's agent, **Then** the capability is not available to it.
5. **Given** a switch to a named agent, **When** it succeeds, **Then** the incoming agent receives a budgeted brief drawn from the conversation so far.
6. **Given** a switch to the default agent, **When** it succeeds, **Then** no such brief is produced, because that agent was already in the conversation.
7. **Given** a switch whose destination identifier does not name any agent, **When** it runs, **Then** it fails with a message naming the identifier it could not find.
8. **Given** a switch whose destination names a delegation-only worker, **When** it runs, **Then** it fails — including when that worker is what the default-agent setting points at.
9. **Given** a switch performed without the explanatory note, **When** it runs, **Then** it succeeds, exactly as the equivalent omission does today.
10. **Given** an install where some agent's identifier is literally the reserved destination word, **When** the system starts, **Then** it starts successfully, warns about that agent by name, and the reserved destination still reaches the *configured default agent* rather than the colliding one.
11. **Given** a stored conversation containing an earlier handoff, **When** it is replayed, **Then** the active-agent signal is produced exactly as it is today.
12. **Given** the forward reservation is ratified, **When** an agent is created or updated with an identifier or a display name equal to the reserved destination word in any letter case, **Then** the request is rejected with a client error naming the reserved word, and no agent is written. *(Gated — see FR-058. This is a **creation-time rejection**, a different path and a different outcome from Acceptance Scenario 10's **startup warning** for an install that already carries such an agent; scenario 10 holds regardless of how the ratification lands.)*

---

### User Story 6 — Upgrading an existing install keeps every agent able to switch and to search (Priority: P0)

Existing installs carry per-agent and install-wide permission records keyed by the old tool names. Nothing in the system infers a permission from a name it does not recognise — by design, every decision is an explicit recorded entry, and a missing entry is repaired by writing an explicit denial before startup validation runs.

That repair is what makes this dangerous. On the first start after this change, the two new names exist and have no recorded permission anywhere. If the old records are not converted **before** the repair runs, the repair writes an explicit denial for both, on every agent, and startup **succeeds** — leaving every agent unable to hand off and, because the discovery capability itself is denied, unable to reach any of the tools Story 4 has just made findable-only. That is roughly seven in ten of the catalog, silently, with a clean startup and a green build.

**Why this priority**: P0. It is the highest-severity item in the source ADR, its failure mode is a successful start, and it affects every upgraded install rather than an edge case.

**Independent Test**: Take a permission file written before this change that permits both old capabilities, start the system, and confirm both new names are permitted and neither old name remains. Then start it again and confirm the file is byte-identical.

**Acceptance Scenarios**:

1. **Given** a stored permission record permitting the old handoff capability, **When** the system starts, **Then** the new switching capability is permitted, the old entry is gone, and no denial was written for either new name.
2. **Given** stored records where one old switching capability is denied and the other permitted, **When** the system starts, **Then** the merged capability is **denied** and both old entries are gone.
3. **Given** a record already carrying a denial for a new name alongside a permission for an old one, **When** the system starts, **Then** the denial survives.
4. **Given** an already-converted record, **When** the system starts again, **Then** the stored file is byte-identical to what it was.
5. **Given** the conversion is interrupted before it completes, **When** the system starts again, **Then** it converts normally from the unmodified original.
6. **Given** records exist both install-wide and per-agent, **When** the system starts, **Then** both are converted.
7. **Given** a converted install, **When** startup validation runs, **Then** it finds an explicit entry for every agent-and-tool pair and does not abort.

---

### User Story 7 — The stable part of the catalog is paid for once, not every turn (Priority: P2)

What remains of the per-turn listing after Story 4 is identical from turn to turn for a given agent. It is nonetheless re-sent and re-charged every turn, outside the region of the request the provider will cache. This story moves the stable part inside that region and leaves only the short volatile part — which tools are already usable — outside it, so the recurring charge becomes a one-off per cache window.

The benefit is real but modest and accrues to one provider. The implementation carries the single riskiest detail in this work: placed anywhere other than immediately after the existing cached block, it caches nothing at all while looking entirely correct. This story is therefore explicitly conditional — it ships last, and a measurement that can genuinely fail decides whether it ships at all.

**Why this priority**: P2, and **gated**. It is the smallest benefit, it is provider-specific, and the source ADR is openly ambivalent about its size. It sequences last so that dropping it costs nothing else.

**Independent Test**: Run a multi-turn conversation in which no new tools are made usable, and compare the provider-reported cache-read volume before and after. A correct implementation raises it by close to the moved block's size; the characteristic failure raises it by nothing.

**Acceptance Scenarios**:

1. **Given** a multi-turn conversation in which no new tool is made usable, **When** cache-read tokens are compared before and after this change, **Then** they rise by at least 80% of the moved block's own token count. Both figures are token counts: the block is sized with the same estimator the agent package already uses for context budgeting, and the rise is read from the provider's reported cache-read token figure. There is no upper bound.
2. **Given** the same comparison, **When** the rise is below that floor, **Then** this story does not ship and the remaining stories ship without it.
3. **Given** the request is assembled, **When** its stable region is inspected, **Then** the moved block sits immediately after the existing stable block and before the first per-turn-varying block, and carries a cache marker.
4. **Given** a turn on which a tool is newly made usable, **When** the request is assembled, **Then** it still assembles correctly and the record of which tools are usable is accurate.
5. **Given** a provider that does not honour explicit cache markers, **When** a request is assembled, **Then** it is assembled correctly and nothing regresses.

---

## Behavioral Contract

**Primary flows**

- When an agent searches by description and one result clearly dominates, the system makes exactly that one usable.
- When an agent searches by description and two or three results are genuinely close, the system makes them all usable in the same answer and says why.
- When an agent searches by exact name, the system behaves exactly as it does today.
- When a turn is built, the system lists the eight previewed tools by name and no others.
- When an agent needs an unlisted tool, one search makes it usable for the remainder of that conversation. This is a property of the **static catalog** only. An externally-provided (MCP) tool made usable by the same search continues to be governed by the pre-existing discovery lifetime and stops being usable when that lifetime elapses — unchanged by this work, and deliberately not covered by the permanence rule (FR-037 / FR-037a).
- When an agent switches the conversation to another agent, the destination is a parameter of one capability, and the interface is told which agent is now active.
- When the system starts on an upgraded install, old permission records are converted to the new names before anything else reads or repairs them.

**Error flows**

- When a search's permitted results are empty, the system returns its existing "nothing matched" response and increases the empty-result counter by one.
- When a search has no permission resolver available, the system discloses nothing.
- When a switch names an unknown agent, the system fails with a message naming that identifier.
- When a switch names a delegation-only worker, the system fails.
- When a switch fails for any reason, the system produces no active-agent signal.
- When a delegated sub-task attempts to switch the parent conversation, the capability is not present in its registry.

**Boundary conditions**

- When a search's permitted results number fewer than two, no comparison is performed and the single result, if any, is made usable alone.
- When more results qualify than the limit admits, the highest-ranked qualifiers are taken and the limit is three.
- When a qualifying runner-up would require user confirmation, or destroys state, or changes install-wide settings, the speculative comparison does not admit it; the confident comparison still does.
- When an agent's identifier collides with the reserved switching destination, the reserved destination wins and the system warns at startup.
- When a conversation ends, every record of what each of its agents had made usable is discarded.
- When two stored permission entries for the same merged capability disagree, the strictest of them wins, and an existing entry under the new name participates in that comparison.

---

## Edge Cases

- **A denied tool ranks first.** Expected: it is absent from the answer entirely, and the highest-ranked *permitted* result becomes the reference point against which the closeness comparison is made.
- **Every ranked result is denied.** Expected: the existing "nothing matched" response, not a listing, and the empty-result counter increases by one.
- **A query matches nothing at all.** Expected: identical to the above, and identical to today.
- **A very large connected tool set makes the permission walk long.** Expected: the walk is bounded; a bound that trips returns fewer permitted results, never a denied one.
- **An operator has narrowed the number of results returned to one.** Expected: no comparison runs; behaviour is today's.
- **An operator has set the number of results returned to zero or a negative value.** Expected: no results, the "nothing matched" response, and the empty-result counter increases — the same as any query with no permitted results.
- **The same tool is surfaced by a second search while already usable.** Expected: it stays usable; no new unused-discovery record is created and any existing one is not refreshed.
- **An ambiguous search makes three tools usable and the agent correctly uses one.** Expected: the two unused ones each count toward the unused-discovery counter once the horizon passes. This counter therefore has a non-zero floor under correct operation; its trigger is a change in rate, not an absolute value.
- **A conversation ends before the unused-discovery horizon elapses.** Expected: pending discoveries are still counted, once each, before the records are discarded.
- **A conversation hands off away from an agent and never returns.** Expected: that agent's pending discoveries are counted at conversation end.
- **A tool is added to the catalog with no exposure level recorded.** Expected: the build fails.
- **A tool that destroys state is added under a name that does not look destructive.** Expected: **not detected.** The name-shape check is a backstop, not a guarantee; the real control is that adding any tool forces a recorded decision on both its exposure level and its destructive status.
- **A candidate's score is zero or negative relative to a positive top score.** Expected: it is below both thresholds and is never promoted. The ranking function in use cannot emit a negative score, so this is a defensive property of the comparison rather than a reachable input today; it is stated so a later ranking change cannot make it reachable silently.
- **An install already has an agent whose identifier is the reserved destination.** Expected: startup succeeds, warns, and the reserved destination reaches the configured default agent.
- **A switch is attempted with the reserved word in a different letter case.** Expected: **not** treated as the sentinel. The sentinel comparison is exact; any other casing is an ordinary identifier lookup, which fails as not-found unless an agent genuinely bears that identifier. The case-insensitivity in this feature applies only to the *creation and update rejection* — the two are deliberately different, because a rejection that misses a casing variant admits the very collision it exists to prevent, whereas a resolution that matched loosely would silently shadow a legitimately-named agent.
- **An unused discovery is made by an agent that is then handed off away from and returns much later.** Expected: the horizon is counted in **that agent's own turns**, not the conversation's global turn count. A turn can only compute its own agent-and-conversation key, so turns held by another agent neither advance nor fire this agent's horizon. An agent that returns after eight of another agent's turns still needs five of its own before its pending discovery counts; if the conversation ends first, the close sweep counts it once.
- **A switch is performed with no explanatory note.** Expected: success, and the recorded conversation entry keeps today's exact shape including its empty trailing segment.
- **A switch's destination resolves but the operation exceeds the 10-second ceiling.** Expected: **treated as a failed switch** — an error naming the timeout, no active-agent signal, and the conversation's active agent unchanged. This holds on both destination branches, including the reserved one where the ceiling is newly imposed by this work.
- **A permission file is converted, then the system is rolled back to a build predating this change.** Expected: **the old build does not recognise the new names and will write denials for the old ones.** See the prohibition and the assumption recorded below — this is a one-way conversion.
- **Two processes start against the same install directory simultaneously on a platform without cross-process file locking.** Expected: both compute identical output from identical input, so a lost write is byte-identical; the only divergent ordering re-runs the conversion against already-converted content, which is a no-op.
- **An externally-provided (MCP) tool is made usable and then goes unused past the discovery lifetime.** Expected: **it stops being usable**, exactly as it does today. This is the pre-existing MCP registry TTL, not something this work builds and not something FR-037's permanence rule reaches — the two mechanisms are structurally independent and only one of them is in this work's scope. An implementer who reads "must not evict" as covering this case will either delete a live behaviour or fail a required test; the two-mechanism note under Pinned Identifiers is the disambiguation.
- **A conversation runs long enough to make many tools usable.** Expected: bounded by the agent's own permitted findable catalog — at most 71 tool descriptions, which is exactly the volume the system sent every turn before any of this optimisation existed.
- **The revert control is set.** Expected: the per-turn listing returns in full. The permission filtering on search results and the per-agent scoping of usable sets are **not** reverted — they are corrections, not part of the bet being unwound.

---

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

- The system **must not** remove a tool from the searchable index in order to hide it, because the index is the only route by which an unlisted tool is reachable at all — hiding it there makes it permanently unusable rather than merely unadvertised.
- The system **must not** filter the searchable index by the caller's permissions, because one index serves every calling agent, its cached form has no signal that changes when a permission changes, and removing entries alters the ranking scores of every surviving entry — which would make the closeness comparison mean something different for each agent.
- The system **must not** express the new exposure level as another value of the existing exposure-level classification, because the searchable index admits entries by testing that classification for equality and a new value would silently empty the index of every affected tool.
- The system **must not** expire, evict, or otherwise withdraw a **static catalog** tool that has been made usable during a conversation, because the conversation's own record still shows the tool working and withdrawing it produces a failure several turns after its cause with nothing in the conversation explaining it. **This prohibition is scoped to the new, permanent usable-set record (`loadedTools`) that this work introduces for the 89 static tools, and to it alone.** It does **not** apply to — and this work does not alter — the pre-existing MCP discovery lifetime (`cfg.Tools.MCP.Discovery.TTL`, applied by `PromoteTools`, decremented by `TickTTL`), which already withdraws externally-provided tools today and must continue to do so unchanged. Reading this bullet as a ban on *all* decay everywhere is the specific misreading ADR-071 §1.1.1 exists to prevent; see FR-037 and FR-037a, and the two-mechanism note under Pinned Identifiers.
- The system **must not** find and execute a tool in a single call, because the model would have to invent arguments for a shape it has never seen, forfeiting the argument validation the provider performs against a declared shape, and because one call would carry two distinct permission questions resolved against one identity.
- The system **must not** keep the old switching capabilities as aliases alongside the new one, because that is the permanent-dual-name pattern this project has already refused once and it would leave three identities for one capability.
- The system **must not** make the closeness thresholds operator-configurable, because no operator can tune them without usage data that does not yet exist.
- The system **must not** add a wire field expressing the new exposure distinction, because nothing consumes the existing exposure field today and a field for an interface that does not exist is surface for its own sake.
- The system **must not** rewrite the recorded text prefix that conversation replay matches on, because the producer and the consumer share no constant and the coupling is invisible.
- The system **must not** retro-edit the bodies of prior decision records to use the new names, because a decision record states what was decided on its date with the evidence available then.
- The system **must not** fix the known defect where a delegated sub-task is told a structurally-absent tool loaded successfully, because that is a caller-resolution defect with its own blast radius across every delegated sub-task and folding it into a rename is the scope creep that made this work hard to review. It **must** be filed as its own tracked item.
- The system **must not** treat the destructive-tool name-shape check as a definition of which tools are destructive, because it cannot detect a destructive tool with a benign name and treating it as a guarantee is a detector masquerading as a mitigation.
- The system **must not** silently start with the new capabilities denied, because a successful start is indistinguishable from a healthy one and the denial disables roughly seven in ten of the catalog.
- The system **must not** treat the conversion of stored permission records as reversible. Rolling back to a build predating this change leaves the new names unrecognised and the old ones absent, which the older build repairs by writing denials — the same failure this story exists to prevent, mirrored.
- An implementer **must not** treat this specification's site lists as complete. Every prior pass over this surface disproved a completeness claim made in good faith; the lists are the best available starting point and must be re-derived by search before any workstream is called done.

### Machine-Verifiable Constraints

**Discovery answer contents**

- When a query's permitted result set is empty, the system MUST return the exact response text `No tools found matching the query.` and MUST NOT return any list of tool names or descriptions.
- When no permission resolver is available, the system MUST return that same exact text.
- For any query and any calling agent, the set of tool names appearing in the answer MUST be a subset of the tools that agent is permitted to make usable.
- For any query, the relative order of the permitted results in the answer MUST equal their relative order in the unfiltered ranking.
- The number of permission checks performed per query MUST NOT exceed 50.
- The number of results returned per query MUST NOT exceed the configured result limit, whose default is 5.

**Closeness comparison**

- A candidate at rank `i ≥ 2` MUST enter the usable set when its score is `≥ 0.80 ×` the top permitted result's score.
- A candidate at rank `i ≥ 2` MUST enter the usable set when its score is `≥ 0.50 ×` the top permitted result's score **and** its `Tool.Category()` differs from the top result's `Tool.Category()` **and** it is neither confirmation-gated nor on the destructive-and-install-wide list.
- The total number of tools made usable by one query MUST NOT exceed 3.
- When more than 2 candidates qualify, the 2 highest-ranked qualifiers MUST be chosen.
- The thresholds `0.80`, `0.50` and `3` MUST NOT be exposed as configuration.
- A candidate whose score is not strictly positive MUST NOT be promoted by either rule. The ranking function cannot produce a negative score — its inverse-document-frequency term is `log((N − df + 0.5)/(df + 0.5) + 1)`, whose argument exceeds 1 for every `df ≤ N`, so every term contribution is positive — but the comparison MUST be written so that a negative or zero candidate score fails both tests rather than relying on that property holding after a future ranking change.

**Exposure levels**

- The always-listed set MUST contain exactly 17 names.
- The previewed set MUST contain exactly 8 names.
- The search-only set MUST contain exactly 63 names.
- The always-callable infrastructure set MUST contain exactly 1 name.
- Their union MUST equal the full static catalog and MUST contain exactly 89 names.
- The rendered per-turn listing for an agent permitted everything with nothing yet usable MUST be exactly 22 lines.
- Every search-only tool MUST remain present in the searchable index.
- Every name in the destructive-and-install-wide list MUST be present in the static catalog and MUST resolve to the search-only level.
- Every catalog name matching `^(delete|remove|disable|purge|wipe|revoke|drop|reset|destroy)_`, and the install-wide settings-writing tool, MUST appear either on the destructive-and-install-wide list or on an explicit exemption list carrying a written reason.

**Operator-visible signals**

- A request to the metrics endpoint MUST return a body containing the series name `omnipus_toolsearch_zero_result_total` with type `counter`.
- A request to the metrics endpoint MUST return a body containing the series name `omnipus_toolsearch_no_followup_total` with type `counter`.
- `omnipus_toolsearch_zero_result_total` MUST increase by exactly 1 per query whose permitted result set is empty.
- `omnipus_toolsearch_no_followup_total` MUST increase by exactly 1 per by-description discovery that goes unused for more than 5 turns or until the conversation ends, whichever comes first, and MUST NOT increase again for that same discovery.

**Agent switching**

- The switching capability MUST declare exactly one required parameter, its destination.
- The whole operation MUST complete within a 10-second ceiling, on both destination branches.
- When the destination names no known agent, the operation MUST fail with a message containing that destination value.
- When the destination resolves to a delegation-only worker, the operation MUST fail, on both branches.
- When the destination is exactly the literal string `"default"`, it MUST resolve to the configured default agent regardless of whether an agent bears that identifier. The comparison MUST be exact: any other casing is not the sentinel and MUST be treated as an ordinary agent identifier, which for an install with no such agent means the not-found failure above.
- A successful switch MUST produce exactly one active-agent signal; a failed switch MUST produce none.
- A successful switch whose resulting active agent equals the configured default MUST produce that signal with no agent identifier; otherwise it MUST carry the identifier.
- The recorded conversation entry for a switch to a named agent MUST begin with the exact prefix `Handoff: `.

**Upgrade conversion**

- The conversion MUST execute before any repair writes a permission entry.
- Where two stored entries map to one new name, the resulting value MUST be the strictest of them, ordered `deny` > `ask` > `allow`.
- An entry already stored under a new name MUST participate in that comparison.
- After conversion, no stored entry MUST remain under any of the three retired names.
- Running the conversion twice against the same input MUST produce byte-identical stored output.
- After conversion, startup validation MUST find an explicit entry for every agent-and-tool pair and MUST NOT abort.

**Cache boundary**

- The moved block MUST occupy index 1 of the request's stable region, immediately after the existing stable block and before the first per-turn-varying block, and MUST carry an ephemeral cache marker.
- The measured rise in cache-read volume on a turn with no new usable tools MUST be `≥ 0.8 ×` the moved block's own estimated size, or this story does not ship. **Both quantities are token counts and MUST be measured as such**: the block's size is `B = estimateMessageTokens(BuildStaticToolCatalog(...))`, the same estimator the agent package already uses for context-budget accounting; the rise is `ΔC`, the increase in `providers.Usage.CacheReadTokens` on the same turn measured before and after the change. Neither is a byte count and neither is a character count. There is deliberately **no upper bound** — a `ΔC` exceeding `B` means the cache boundary is covering text beyond the moved block, which is a better result, not an anomaly.
- The request MUST use no more than 2 of the provider's 4 available cache breakpoints.

**Build and release gates**

- `gofmt -l . | wc -l` MUST be 0.
- `golangci-lint run --build-tags=goolm,stdjson` MUST exit 0.
- `CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 ./...` MUST exit 0.
- `govulncheck ./...` MUST report 0 vulnerabilities.
- `npm run typecheck` MUST exit 0.
- `npx vitest run` MUST exit 0.
- `make verify-contracts` MUST exit 0.
- A search across `pkg/`, `src/`, `tests/`, `contracts/` and `docs/` for the three retired names MUST return zero hits outside dated point-in-time records and the front-matter naming notes.

### Conservative Type Design

The new exposure classification is introduced as a distinct enumerated type rather than a boolean, for two reasons and only two: it mirrors the existing name-keyed exposure lookup it sits beside, and it reads correctly at the point of use. It carries no invariants a boolean could not, and no third value is introduced in anticipation of a deferred feature — the per-agent variant remains explicitly undesigned.

No other new nominal type is warranted. The bucket key, the discovery-age record and the two name sets are all built from existing types.

---

## Prerequisites

- **Hardware / OS**: Linux x86_64 or macOS arm64 for development. The Go test suite must run on the project's CI worker (16 GB), never in a constrained dev pod — the gateway test binary under the `goolm` tag will OOM.
- **Required runtimes**: Go (module requires 1.26.4; targets 1.22+), Node with npm for the SPA and contract tooling.
- **Required services**: None. Storage is file-based. One exception applies only to the conditional cache story — see below.
- **Network assumptions**: Offline for all development and all automated verification. The conditional cache story's one-off acceptance measurement requires outbound HTTPS to the Anthropic API.
- **Accounts / credentials**: None for any automated gate. A funded Anthropic API key is required **once**, by the operator, for the conditional cache story's manual pre-merge measurement. It must not be added to CI.

---

## Development Setup

1. `git checkout -b <branch> release/v0.1.1`
2. `export PATH=/usr/local/go/bin:$HOME/go/bin:$PATH`
3. `go mod download`
4. `npm install`
5. `make gen-contracts` — confirm it is idempotent on a clean tree before making any change
6. `make verify-contracts` — must exit 0 as a baseline

**Building a binary that serves the current SPA** (required whenever any SPA source changes — see the prohibition on stale embeds):

7. `npm run build`
8. `rm -rf pkg/gateway/spa`
9. `cp -r dist/spa/* pkg/gateway/spa/`
10. `CGO_ENABLED=0 go build -tags goolm,stdjson -o /tmp/omnipus ./cmd/omnipus/`

**Expected first-run behaviour**: `make verify-contracts` exits 0 with no diff; the built binary starts and its startup permission validator reports no coverage gaps.

**Common first-run failures**:
- `build constraints exclude all Go files in .../pkg/channels/matrix` → missing build tags. Use `make test` / `make build`, or pass `-tags goolm,stdjson`. This is never a flake and never a real defect.
- `tsc --noEmit` silently exiting 0 without checking anything → use `npm run typecheck`, which is wired to `tsc -b --noEmit`.
- A rebuilt binary still serving old SPA behaviour → the embed directory was not re-synced. Verify with `grep -c "<a new string>" pkg/gateway/spa/assets/index-*.js`.

---

## Tech Stack

| Category | Choice | Version / Pin | Source |
|---|---|---|---|
| Language | Go | module 1.26.4, targets 1.22+ | CLAUDE.md, Tech Stack |
| Build tags | `goolm,stdjson` | — | Makefile `GO_BUILD_TAGS` |
| Frontend | TypeScript, React 19, Vite 6 | — | CLAUDE.md, Tech Stack |
| Datastore | File-based JSON/JSONL under the install home | — | CLAUDE.md, Storage |
| External APIs | Anthropic (conditional cache story's measurement only) | — | ADR §6.5 |
| Build tool | `make` (never raw `go test ./...`) | — | CLAUDE.md, Testing & building |
| Test framework | `go test` + testify; vitest; Playwright | — | CLAUDE.md, Quality Gates |
| Contract tooling | oapi-codegen, openapi-typescript, openapi-zod-client via `scripts/gen-contracts.sh` | — | CLAUDE.md, Contract regeneration |
| Metrics format | Hand-rolled Prometheus text on the existing metrics endpoint | — | ADR §4.3.1(a) |

---

## Deployment / Runtime

- **Target environment**: Single Go binary, all three deployment variants (embedded SPA, desktop subprocess, hosted service).
- **Online / offline**: Fully offline-capable. No new outbound dependency is introduced by any shipped code path.
- **Resource limits**: Two new in-memory maps keyed per conversation-and-agent, swept at conversation close, plus two atomic counters. No measurable change against the project's security-overhead budget.
- **Start / stop commands**: Unchanged (`omnipus gateway`).
- **Health check**: The existing startup permission-coverage validator. After this change it must find explicit entries for the two new names on every agent and must not abort.
- **Logs / telemetry**: Two new counter series on the existing metrics endpoint. One new startup warning for a colliding agent identifier. One existing warning for a missing exclusion name becomes relevant during the rename.
- **Rollback**: Setting the revert control — `PreviewAllLazy` on the manifest configuration block, JSON key `preview_all_lazy`, default `false` — restores the previous per-turn listing without a new binary. It is a per-install dial in the stored configuration, not an environment variable and not a settings-screen control. **Rolling the binary back is not sufficient** and is not safe on its own — see Assumptions.

---

## Integration Boundaries

### Anthropic API (conditional cache story only)

- **Data in**: A request whose stable region carries two cache markers.
- **Data out**: Per-response usage figures including cache-read and cache-write volumes.
- **Contract**: Prefix-match caching. A block caches everything up to and including itself; any earlier byte change invalidates it. Render order places the tool array before the system region, so any change to the usable tool set invalidates the whole stable prefix regardless. Minimum cacheable prefix is roughly 1024 tokens, applied cumulatively — already cleared by the existing stable block. Default cache lifetime is 5 minutes. Maximum 4 breakpoints per request.
- **On failure**: A rate limit, an outage, or an unfunded key makes the one-off measurement impossible, not wrong. The measurement is manual and pre-merge; a failure to obtain it blocks only this one story.
- **Development**: Real service, once, operator-run. Every automated check for this story is offline and structural.

### Providers that do not honour explicit cache markers

- **Data in**: The same assembled request.
- **Data out**: Usage figures without cache-read volume.
- **Contract**: One provider family discards the structured stable region entirely and receives only its concatenated text; another never reads it. Different mechanisms, identical outcome.
- **On failure**: Not applicable — there is nothing to fail. These providers still benefit from stable-before-volatile ordering if they perform automatic prefix caching.
- **Development**: Real, via existing tests. No new handling.

### The metrics endpoint

- **Data in**: An authenticated HTTP request.
- **Data out**: Prometheus-format text including the two new counter series.
- **Contract**: The existing hand-rolled exposition, extended by two unlabelled counters. Reached from the tool layer through the existing recorder indirection, which defaults to a no-op so the tool layer works standalone.
- **On failure**: A no-op recorder means counters read zero rather than crashing. **This is itself a risk**: a counter that is unreachable is indistinguishable from a healthy one, so reachability is asserted against the endpoint's output, never against the in-process accessor.
- **Development**: Real, in-process.

### Stored install configuration

- **Data in**: A permission file possibly written by any prior version.
- **Data out**: The same file, converted, written atomically.
- **Contract**: Read-check-before-write keyed on the presence of a retired name. Atomic replace, so no partial file is observable.
- **On failure**: A crash before the write leaves the original intact and the next start converts normally. Two concurrent starts compute identical output from identical input.
- **Development**: Real, with fixture files representing each pre-migration shape.

---

## BDD Scenarios

### Feature: Tool Manifest Tier Redesign

#### Background

- **Given** a running system with the static tool catalog registered
- **And** every agent has an explicit recorded permission for every tool in that catalog

---

### User Story 1 — Permission-respecting discovery results

#### Scenario: Denied tool ranked mid-list is absent from the answer

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Error Path

- **Given** an agent whose permission for the workspace-deletion tool is a denial
- **And** a query whose ranking places that tool second among five results
- **When** the agent runs the query
- **Then** the answer contains neither that tool's name nor its description
- **And** the four permitted results are all present
- **And** their relative order matches their order in the unfiltered ranking

---

#### Scenario: Every ranked result is denied

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Error Path

- **Given** an agent whose permissions deny every tool a particular query ranks
- **When** the agent runs that query
- **Then** the answer is exactly `No tools found matching the query.`
- **And** no tool name or description appears anywhere in the answer
- **But** no listing of denied results is produced

---

#### Scenario: Discovery with no permission resolver discloses nothing

**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Edge Case

- **Given** a discovery capability constructed without a permission resolver
- **When** any query is run against it
- **Then** the answer is exactly `No tools found matching the query.`
- **And** no tool name or description is disclosed

---

#### Scenario: One agent's denials do not alter another agent's ranking

**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Edge Case

- **Given** agent A permitted every tool and agent B denied three of them
- **When** both agents run an identical query
- **Then** the results B is permitted to see appear in the same relative order as they do for A
- **And** the score of each permitted result is identical for both agents

---

#### Scenario: Empty permitted result set increments the operator counter

**Traces to**: User Story 1, Acceptance Scenario 5
**Category**: Error Path

- **Given** an agent whose permissions deny every tool a query ranks
- **And** the empty-result counter is at a known value
- **When** the agent runs that query
- **Then** a request to the metrics endpoint reports that counter increased by exactly 1

---

#### Scenario: A near-miss probe does not suggest a denied tool's real name

**Traces to**: User Story 1, Acceptance Scenario 6
**Category**: Error Path

- **Given** an agent whose permission for the workspace-deletion tool is a denial
- **When** the agent requests a tool by a misspelling within the suggestion similarity threshold of that tool's name
- **Then** no suggestion naming that tool is returned

---

#### Scenario: The permission walk is bounded on a large tool set

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Edge Case

- **Given** a corpus large enough that more than 50 ranked results precede the first permitted one
- **When** a query is run
- **Then** at most 50 permission checks are performed
- **And** the answer contains no denied tool's name

---

#### Scenario Outline: Discovery answers are a subset of the caller's permitted set

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** an agent whose permission for the subject tool is `<permission>`
- **When** the agent runs a query that ranks the subject tool first
- **Then** the subject tool's presence in the answer is `<present>`

**Examples**:

| permission | present |
|---|---|
| allow | yes |
| ask | yes |
| deny | no |

---

### User Story 2 — The renamed discovery capability

#### Scenario: The capability answers to its new name on the by-name path

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path

- **Given** an agent permitted to use the discovery capability
- **When** it invokes the capability under its new name asking for a tool by exact name
- **Then** that tool becomes usable
- **And** its schema is returned

---

#### Scenario: The capability answers to its new name on the by-description path

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path

- **Given** an agent permitted to use the discovery capability
- **When** it invokes the capability under its new name describing work a single tool does
- **Then** that tool becomes usable

---

#### Scenario: Discovery calls stay hidden from the chat thread by default

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a completed discovery call under the new name
- **And** a user with default display settings
- **When** the conversation is rendered
- **Then** the discovery call is not shown in the chat thread
- **And** it is not shown in the activity panel

---

#### Scenario: Verbose output reveals discovery calls

**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** a completed discovery call under the new name
- **And** a user who has enabled verbose output
- **When** the conversation is rendered
- **Then** the discovery call is shown

---

#### Scenario: A failed discovery call is still shown

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Error Path

- **Given** a discovery call under the new name that returned an error
- **And** a user with default display settings
- **When** the conversation is rendered
- **Then** the call is shown, because an error still forces visibility

---

#### Scenario: A conversation recorded before the rename still renders readably

**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Edge Case

- **Given** a stored conversation containing calls under the old capability name
- **When** it is rendered
- **Then** those calls display a human-readable label rather than a raw identifier

---

#### Scenario: The registration guard prevents a second registration

**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Edge Case

- **Given** a running system whose registry has already registered the discovery capability
- **When** the manifest-compression setting is toggled at runtime, re-entering the registration path
- **Then** the capability is present exactly once in the registry

---

#### Scenario: The published exposure-level description names only live capabilities

**Traces to**: User Story 2, Acceptance Scenario 6
**Category**: Happy Path

- **Given** the published per-tool exposure-level description
- **When** it is read
- **Then** it names the discovery capability by its current name
- **And** it names no capability that has been retired

---

#### Scenario: The canonical-name guard can fail

**Traces to**: User Story 2, Acceptance Scenario 7
**Category**: Edge Case

- **Given** the regression guard that pins the discovery capability's canonical name
- **When** the production display-label mapping is changed to a different name
- **Then** the guard fails

---

#### Scenario: The startup permission validator finds the renamed capability on every agent

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a freshly seeded install
- **When** the system starts
- **Then** startup succeeds
- **And** every agent has an explicit recorded permission for the discovery capability under its new name

---

### User Story 3 — Underdetermined search returns alternatives

#### Scenario: Two near-tied results are both made usable

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a query whose top permitted result scores 10.0 and whose runner-up scores 8.5
- **When** the query runs
- **Then** both tools become usable
- **And** the answer states that more than one was made available

---

#### Scenario: A dominant top result is made usable alone

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a query whose top permitted result scores 10.0 and whose runners-up all score below 4.0 in the same kind
- **When** the query runs
- **Then** exactly one tool becomes usable
- **And** the answer names that one tool

---

#### Scenario: A cross-kind near-miss is made usable

**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** a query whose top permitted result scores 9.1 in one kind and whose runner-up scores 5.2 in a different kind
- **When** the query runs
- **Then** both tools become usable

---

#### Scenario: The cross-kind rule is decided by the tools' own category values

**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the query "send a file to the user"
- **And** it ranks `send_file` first at 9.1, whose `Tool.Category()` is `communication`
- **And** it ranks `write_file` second at 5.2, whose `Tool.Category()` is `filesystem`
- **When** the query runs
- **Then** both become usable, because 5.2 is at least 0.50 × 9.1 **and** the two `Tool.Category()` values differ
- **And** the same pair with both categories equal would promote only the top result

---

#### Scenario: A destructive cross-kind near-miss is excluded

**Traces to**: User Story 3, Acceptance Scenario 4
**Category**: Error Path

- **Given** a query whose top permitted result scores 9.1 in one kind
- **And** whose runner-up scores 5.2 in a different kind and is on the destructive-and-install-wide list
- **When** the query runs
- **Then** only the top result becomes usable
- **But** the destructive tool does not become usable

---

#### Scenario: A confirmation-gated cross-kind near-miss is excluded

**Traces to**: User Story 3, Acceptance Scenario 4
**Category**: Error Path

- **Given** a query whose top permitted result scores 9.1 in one kind
- **And** whose runner-up scores 5.2 in a different kind and resolves to a permission requiring user confirmation
- **When** the query runs
- **Then** only the top result becomes usable

---

#### Scenario: A destructive result inside the confident band is still made usable

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Edge Case

- **Given** a query whose top permitted result scores 10.0
- **And** whose runner-up scores 8.5 and is on the destructive-and-install-wide list
- **When** the query runs
- **Then** both become usable, because the exclusion narrows only the speculative comparison

---

#### Scenario: The number made usable is capped at three

**Traces to**: User Story 3, Acceptance Scenario 5
**Category**: Edge Case

- **Given** a query with five permitted results scoring 10.0, 9.5, 9.2, 9.0 and 8.8
- **When** the query runs
- **Then** exactly three tools become usable
- **And** they are the three highest-ranked

---

#### Scenario: A single permitted result skips the comparison

**Traces to**: User Story 3, Acceptance Scenario 6
**Category**: Edge Case

- **Given** a query with exactly one permitted result
- **When** the query runs
- **Then** that tool becomes usable
- **And** no comparison against a runner-up is performed

---

#### Scenario: The by-name path is unaffected by the comparison

**Traces to**: User Story 3, Acceptance Scenario 7
**Category**: Happy Path

- **Given** an agent requesting three tools by exact name
- **When** the request runs
- **Then** exactly those three become usable
- **And** no scores are computed

---

#### Scenario: Adding a tool without adjudicating its destructive status fails the build

**Traces to**: User Story 3, Acceptance Scenario 8
**Category**: Edge Case

- **Given** a new tool named with a destructive-looking prefix added to the catalog
- **And** the tool is on neither the destructive-and-install-wide list nor the exemption list
- **When** the build runs
- **Then** it fails, naming that tool

---

#### Scenario: Renaming a listed destructive tool without updating the list fails the build

**Traces to**: User Story 3, Acceptance Scenario 8
**Category**: Edge Case

- **Given** a tool on the destructive-and-install-wide list
- **When** that tool is renamed in the catalog without updating the list
- **Then** the build fails, because a listed name no longer appears in the catalog

---

#### Scenario Outline: The closeness comparison at its boundaries

**Traces to**: User Story 3, Acceptance Scenarios 1 and 3
**Category**: Edge Case

- **Given** a query whose top permitted result scores 10.0
- **And** a runner-up scoring `<score>` whose kind is `<kind>` relative to the top result
- **When** the query runs
- **Then** the number of tools made usable is `<count>`

**Examples**:

| score | kind | count |
|---|---|---|
| 8.0 | same | 2 |
| 7.99 | same | 1 |
| 8.0 | different | 2 |
| 5.0 | different | 2 |
| 4.99 | different | 1 |
| 5.0 | same | 1 |

---

### User Story 4 — Three exposure levels

#### Scenario: The per-turn listing names only the previewed set

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a fresh conversation with an agent permitted every tool
- **And** no tool has been made usable
- **When** the first turn is built
- **Then** the listing names exactly the 8 previewed tools
- **And** the listing renders as exactly 22 lines
- **But** no search-only tool is named

---

#### Scenario: An unlisted tool is found by description

**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a fresh conversation with an agent permitted every tool
- **When** the agent describes work only a search-only tool performs
- **Then** that tool becomes usable
- **And** its schema is present in the next turn's callable set

---

#### Scenario: A static tool made usable stays usable for the conversation

**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Happy Path

- **Given** an agent that made a **static catalog** search-only tool usable on turn 1
- **When** turns 2 through 10 are built without any further discovery call
- **Then** that tool is present in the callable set on every one of those turns

> This is the plain multi-turn case. The scenario immediately below is the same property asserted **past the discovery-lifetime boundary**, which is a strictly stronger claim and therefore subsumes this one; both are listed because the weaker one is what a reader looks for first and the stronger one is what actually stops the claim drifting back.

---

#### Scenario: A static tool made usable does not decay with the discovery lifetime

**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Edge Case
**Mechanism**: the new, permanent usable-set record (`loadedTools`) — FR-037

- **Given** an agent that made a **static catalog** search-only tool usable
- **And** more turns pass than the configured MCP discovery lifetime, with that lifetime ticked on each
- **When** the next turn is built
- **Then** the tool is still usable

> The tick is a no-op here by construction: `TickTTL` is guarded by `!entry.IsCore` and a static tool is core. The scenario asserts the *observable* consequence so that anyone who later wires the static usable-set record into a TTL path breaks a test rather than a session.

---

#### Scenario: A discovered external tool does decay with the discovery lifetime

**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Edge Case
**Mechanism**: the **pre-existing, untouched** MCP registry TTL (`cfg.Tools.MCP.Discovery.TTL`, `PromoteTools`/`TickTTL`) — FR-037a

- **Given** an agent that made an **externally-provided (MCP)** tool usable
- **And** more turns pass than the configured discovery lifetime, with that lifetime ticked on each
- **When** the next turn is built
- **Then** the tool is no longer usable

> **This is not a violation of FR-037's "no eviction" rule, and it is not a decay path this work builds.** It is today's behaviour, on a different mechanism, asserted here only so the pair of tests documents the split. FR-037's prohibition governs the static usable-set record; this scenario governs the MCP registry TTL. See FR-037a and the two-mechanism note under Pinned Identifiers.

---

#### Scenario: A second agent does not inherit the first agent's usable set

**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Happy Path

- **Given** agent A made a search-only tool usable in a conversation
- **And** agent B is permitted that same tool
- **When** the conversation is switched to agent B and B's turn is built
- **Then** that tool is not in B's callable set

---

#### Scenario: Switching back restores the first agent's usable set

**Traces to**: User Story 4, Acceptance Scenario 5
**Category**: Happy Path

- **Given** agent A made a search-only tool usable, then the conversation switched to agent B
- **When** the conversation switches back to agent A and A's turn is built
- **Then** that tool is in A's callable set

---

#### Scenario: Ending a conversation discards every agent's usable set

**Traces to**: User Story 4, Acceptance Scenario 6
**Category**: Edge Case

- **Given** a conversation in which two different agents each made tools usable
- **When** the conversation ends
- **Then** no record of either agent's usable set remains
- **And** no record of either agent's pending discoveries remains

---

#### Scenario: An unused discovery is counted once after the horizon

**Traces to**: User Story 4, Acceptance Scenario 7
**Category**: Happy Path

- **Given** an agent that made a tool usable via a by-description discovery on turn 1
- **When** turns 2 through 7 pass without that tool being used
- **Then** the unused-discovery counter reports exactly 1 on the metrics endpoint
- **And** it still reports 1 after turn 8

---

#### Scenario: A used discovery is never counted

**Traces to**: User Story 4, Acceptance Scenario 8
**Category**: Happy Path

- **Given** an agent that made a tool usable via a by-description discovery on turn 1
- **And** it used that tool on turn 2
- **When** turns 3 through 8 pass
- **Then** the unused-discovery counter reports 0 for that discovery

---

#### Scenario: A by-name load never creates a pending discovery

**Traces to**: User Story 4, Acceptance Scenario 7
**Category**: Alternate Path

- **Given** an agent that made a tool usable by naming it exactly on turn 1
- **When** turns 2 through 8 pass without that tool being used
- **Then** the unused-discovery counter reports 0

---

#### Scenario: Another agent's use does not clear the first agent's pending discovery

**Traces to**: User Story 4, Acceptance Scenario 7
**Category**: Edge Case

- **Given** agent A made tool T usable via a by-description discovery
- **And** the conversation switched to agent B, which used tool T within the horizon
- **When** the horizon elapses for agent A's discovery
- **Then** the unused-discovery counter reports exactly 1, because A's discovery was still wasted

---

#### Scenario: A conversation shorter than the horizon still counts its unused discoveries

**Traces to**: User Story 4, Acceptance Scenario 7
**Category**: Edge Case

- **Given** an agent that made a tool usable via a by-description discovery on turn 1
- **When** the conversation ends on turn 3 without that tool being used
- **Then** the unused-discovery counter reports exactly 1

---

#### Scenario: The revert control restores the full listing

**Traces to**: User Story 4, Acceptance Scenario 9
**Category**: Alternate Path

- **Given** an install with the revert control set
- **When** a turn is built for an agent permitted every tool
- **Then** every findable tool is named in the listing
- **And** the listing renders as approximately 101 lines

---

#### Scenario: The revert control does not undo permission filtering

**Traces to**: User Story 4, Acceptance Scenario 9
**Category**: Edge Case

- **Given** an install with the revert control set
- **And** an agent whose permission for a tool is a denial
- **When** that agent runs a query ranking the denied tool
- **Then** the denied tool's name and description are still absent from the answer
- **And** the denied tool is still absent from that agent's listing

---

#### Scenario: The revert control does not undo per-agent scoping

**Traces to**: User Story 4, Acceptance Scenario 9
**Category**: Edge Case

- **Given** an install with the revert control set
- **And** agent A made a tool usable, then the conversation switched to agent B
- **When** B's turn is built
- **Then** that tool is not in B's callable set

---

#### Scenario: Adding a tool without recording its exposure level fails the build

**Traces to**: User Story 4, Acceptance Scenario 10
**Category**: Edge Case

- **Given** a new tool added to the static catalog
- **And** it appears in neither the always-listed set, the previewed set, nor an explicit search-only record
- **When** the build runs
- **Then** it fails, naming that tool

---

#### Scenario: Both counter series are reachable on the metrics endpoint

**Traces to**: User Story 4, Acceptance Scenario 11
**Category**: Happy Path

- **Given** a running gateway
- **When** the metrics endpoint is requested
- **Then** the body contains the empty-result counter series name declared as a counter
- **And** it contains the unused-discovery counter series name declared as a counter

---

#### Scenario: The listing header tells the model more tools exist

**Traces to**: User Story 4, Acceptance Scenario 12
**Category**: Happy Path

- **Given** a turn built with the three-level split active
- **When** the listing header is read
- **Then** it states that more tools exist than are listed
- **And** it states that they can be found by describing what is needed

---

#### Scenario: Every search-only tool remains findable

**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Edge Case

- **Given** the three-level split is active
- **When** the searchable index is enumerated
- **Then** all 63 search-only tools are present in it

---

#### Scenario Outline: Reclassified tools land at the intended level

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the three-level split is active
- **When** the exposure level of `<tool>` is resolved
- **Then** it is `<level>`

**Examples** — every tool whose level *changes*, plus three that must not:

| tool | level | why it is here |
|---|---|---|
| bash | previewed | leaves always-listed |
| navigate | previewed | leaves always-listed |
| create_task | previewed | leaves always-listed |
| update_task | previewed | leaves always-listed |
| switch_agent | always listed | joins — the net-new merge target |
| list_mounts | always listed | joins |
| send_file | always listed | joins |
| message_parent | always listed | joins |
| recall_conversation | always listed | joins |
| delegate | always listed | must NOT move — it must stay at least as visible as the task tools |
| list_tasks | always listed | must NOT move |
| read_file | always listed | must NOT move — one of the 12 that stay |
| append_file | search-only | representative of the 63 |
| browser_open_tab | search-only | representative of the 63 |
| set_config | search-only | representative of the 63, and on the destructive-and-install-wide list |

`hand_off` and `return_to_default` appear at no level because they no longer exist; the drift test's catalog assertion is what enforces that. This table is derived from ADR-071 §4.1 and is not an independent statement of membership — see "Tier membership: one source of truth".

---

### User Story 5 — One agent-switching capability

#### Scenario: Switching to a named agent emits the active-agent signal

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a live conversation held by one agent
- **And** a second agent exists and is not a delegation-only worker
- **When** the switching capability is invoked with the second agent's identifier and it succeeds
- **Then** the conversation's active agent is the second agent
- **And** exactly one active-agent signal is emitted carrying that agent's identifier

---

#### Scenario: Returning to default emits the signal with no identifier

**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a live conversation held by a non-default agent
- **When** the switching capability is invoked with the reserved destination and it succeeds
- **Then** the conversation's active agent is the configured default agent
- **And** exactly one active-agent signal is emitted with no agent identifier

---

#### Scenario: A failed switch emits no signal

**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Error Path

- **Given** a live conversation
- **When** the switching capability is invoked and returns an error
- **Then** no active-agent signal is emitted

---

#### Scenario: Switching to the agent that happens to be the default emits no identifier

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Edge Case

- **Given** a live conversation held by a non-default agent
- **When** the switching capability is invoked with the configured default agent's own identifier and it succeeds
- **Then** exactly one active-agent signal is emitted with no agent identifier
- **And** the interface shows the default agent as active

---

#### Scenario: The active agent cannot be resolved after a successful switch

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Error Path

- **Given** a switch that reports success
- **And** the conversation's active agent cannot subsequently be resolved
- **When** the signal would be emitted
- **Then** a warning is logged
- **But** silence is not the only observable outcome

---

#### Scenario: A delegated sub-task cannot switch the parent conversation

**Traces to**: User Story 5, Acceptance Scenario 4
**Category**: Error Path

- **Given** a parent registry containing the switching capability
- **When** a delegated sub-task's registry is derived from it
- **Then** the switching capability is absent from the sub-task's registry under its new name

---

#### Scenario: A nested delegated sub-task also cannot switch

**Traces to**: User Story 5, Acceptance Scenario 4
**Category**: Edge Case

- **Given** a delegated sub-task that itself delegates
- **When** the grandchild registry is derived
- **Then** the switching capability is absent under its new name
- **And** the parent registry demonstrably contained it before the derivation

---

#### Scenario: A switch to a named agent transfers a budgeted brief

**Traces to**: User Story 5, Acceptance Scenario 5
**Category**: Happy Path

- **Given** a conversation with substantial history
- **When** the conversation is switched to a named agent that has not participated in it
- **Then** the incoming agent receives a brief drawn from that history, bounded by half of its own context window

---

#### Scenario: A return to default transfers no brief

**Traces to**: User Story 5, Acceptance Scenario 6
**Category**: Happy Path

- **Given** a conversation with substantial history held by a non-default agent
- **When** the conversation is switched to the reserved destination
- **Then** no history read and no budget split are performed

---

#### Scenario: Switching to an unknown identifier fails

**Traces to**: User Story 5, Acceptance Scenario 7
**Category**: Error Path

- **Given** a live conversation
- **When** the switching capability is invoked with an identifier naming no agent
- **Then** the operation fails with a message containing that identifier

---

#### Scenario: Switching to a delegation-only worker fails

**Traces to**: User Story 5, Acceptance Scenario 8
**Category**: Error Path

- **Given** a live conversation and a registered delegation-only worker
- **When** the switching capability is invoked with that worker's identifier
- **Then** the operation fails

---

#### Scenario: Returning to a default that points at a worker fails

**Traces to**: User Story 5, Acceptance Scenario 8
**Category**: Edge Case

- **Given** an install whose default-agent setting points at a delegation-only worker
- **When** the switching capability is invoked with the reserved destination
- **Then** the operation fails
- **But** it does not pin the conversation to that worker

---

#### Scenario: A switch without the explanatory note succeeds

**Traces to**: User Story 5, Acceptance Scenario 9
**Category**: Alternate Path

- **Given** a live conversation
- **When** the switching capability is invoked with only a destination
- **Then** the operation succeeds
- **And** the recorded conversation entry retains its existing shape

---

#### Scenario: An agent whose identifier is the reserved word is warned about at startup

**Traces to**: User Story 5, Acceptance Scenario 10
**Category**: Edge Case

- **Given** an install seeded with an agent whose identifier is exactly the reserved destination word
- **When** the system starts
- **Then** startup succeeds
- **And** a warning naming that agent is logged
- **And** invoking the switching capability with the reserved destination reaches the configured default agent, not the colliding one
- **But** the agent is **not** rejected and startup is **not** aborted — this is the pre-existing-install path, and it is deliberately the opposite outcome from the creation-time path below

---

#### Scenario: Creating an agent named with the reserved word is rejected

**Traces to**: User Story 5, Acceptance Scenario 12
**Category**: Error Path
**Gated**: on the ratification of FR-058. If the reservation is not ratified, this scenario and its two tests are dropped and the startup-warning scenario above is the whole posture.

- **Given** a running system with the forward reservation active
- **When** an agent is created with an identifier equal to the reserved destination word
- **Then** the request is rejected with a client error naming the reserved word
- **And** no agent is written to the stored configuration
- **And** the same rejection occurs for a display name equal to the reserved word, and for either field in any letter case

---

#### Scenario: Updating an agent to the reserved word is rejected

**Traces to**: User Story 5, Acceptance Scenario 12
**Category**: Error Path
**Gated**: on the ratification of FR-058, as above.

- **Given** an existing agent with an ordinary identifier
- **When** an update request sets its identifier or display name to the reserved destination word
- **Then** the request is rejected with a client error naming the reserved word
- **And** the stored agent is unchanged

---

#### Scenario: Replay of a stored handoff still signals the active agent

**Traces to**: User Story 5, Acceptance Scenario 11
**Category**: Happy Path

- **Given** a stored conversation containing an entry recorded by a switch to a named agent
- **When** the conversation is replayed
- **Then** the active-agent signal is emitted exactly as it is today

---

#### Scenario: The switching operation completes within its ceiling on both branches

**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Edge Case

- **Given** a live conversation
- **When** the switching capability is invoked with the reserved destination
- **Then** the whole operation is bounded by a 10-second ceiling

---

#### Scenario: A switch that exceeds the ceiling emits no active-agent signal

**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Error Condition

- **Given** a live conversation and a destination that resolves normally
- **When** the switching operation exceeds the 10-second ceiling on either destination branch
- **Then** the switch fails with an error naming the timeout
- **And** no active-agent signal is produced
- **And** the conversation's active agent is unchanged

> A timeout is a failed switch, and FR-061's "no signal" rule covers it. Stated as its own scenario because every other failure path in this story has one, and an outcome that is only inferable is an outcome nobody writes a test for.

---

### User Story 6 — Upgrade conversion of stored permissions

#### Scenario: A permitted legacy handoff entry converts to a permitted new entry

**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a stored permission file permitting the legacy handoff capability
- **When** the system starts
- **Then** the merged switching capability is permitted
- **And** no legacy entry remains
- **And** no denial was written for the switching capability or the discovery capability

---

#### Scenario: Disagreeing legacy entries resolve to the strictest

**Traces to**: User Story 6, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a stored file denying the legacy handoff capability and permitting the legacy return capability
- **When** the system starts
- **Then** the merged switching capability is denied
- **And** neither legacy entry remains

---

#### Scenario: An existing strict entry under the new name survives

**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Edge Case

- **Given** a stored file denying the switching capability and permitting the legacy handoff capability
- **When** the system starts
- **Then** the switching capability remains denied

---

#### Scenario: Conversion is byte-identical on a second start

**Traces to**: User Story 6, Acceptance Scenario 4
**Category**: Edge Case

- **Given** a stored file already converted by a previous start
- **When** the system starts again
- **Then** the stored file is byte-identical to what it was

---

#### Scenario: An interrupted conversion leaves the original intact

**Traces to**: User Story 6, Acceptance Scenario 5
**Category**: Error Path

- **Given** a start that is interrupted before the converted file is written
- **When** the system starts again
- **Then** it converts normally from the unmodified original
- **And** no partially-converted file is ever observable

---

#### Scenario: Both install-wide and per-agent records convert

**Traces to**: User Story 6, Acceptance Scenario 6
**Category**: Happy Path

- **Given** a stored configuration with legacy entries in the install-wide permission map and in two agents' own permission maps
- **When** the system starts
- **Then** all three are converted
- **And** no legacy entry remains in any of them

---

#### Scenario: Startup validation finds no coverage gap after conversion

**Traces to**: User Story 6, Acceptance Scenario 7
**Category**: Happy Path

- **Given** a converted install
- **When** startup validation runs
- **Then** it finds an explicit entry for every agent-and-tool pair
- **And** it does not abort

---

#### Scenario: The discovery capability is never denied by the coverage repair

**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Error Path

- **Given** a pre-upgrade permission file permitting the legacy discovery capability on every agent
- **When** the system starts for the first time after the upgrade
- **Then** every agent permits the renamed discovery capability
- **And** no agent has it denied
- **And** search-only tools remain reachable

---

#### Scenario: Two concurrent starts against one install converge

**Traces to**: User Story 6, Acceptance Scenario 4
**Category**: Edge Case

- **Given** two processes starting against the same install directory with an unconverted file
- **When** both complete conversion
- **Then** the resulting stored file is byte-identical to a single-process conversion

---

#### Scenario Outline: The strictest-wins fold

**Traces to**: User Story 6, Acceptance Scenario 2
**Category**: Edge Case

- **Given** a stored file with legacy handoff at `<legacy_a>`, legacy return at `<legacy_b>` and an existing new entry at `<existing>`
- **When** the system starts
- **Then** the merged switching capability's value is `<result>`

**Examples**:

| legacy_a | legacy_b | existing | result |
|---|---|---|---|
| allow | allow | absent | allow |
| allow | deny | absent | deny |
| ask | allow | absent | ask |
| allow | absent | deny | deny |
| deny | deny | deny | deny |
| absent | absent | allow | allow |

---

### User Story 7 — Cached stable catalog (conditional)

#### Scenario: Cache-read volume rises on a turn with no new usable tools

**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a multi-turn conversation against a cache-honouring provider
- **And** no tool is made usable during it
- **When** the provider-reported cache-read **token** count on a mid-conversation turn is compared with the same turn measured before this change
- **Then** it rises by at least 80% of the moved block's estimated **token** count, sized with the agent package's own context-budget estimator
- **And** no upper bound applies — a larger rise means the cache boundary covers text beyond the block, which is a better result

---

#### Scenario: A rise below the floor drops this story

**Traces to**: User Story 7, Acceptance Scenario 2
**Category**: Error Path

- **Given** the same comparison, both sides in tokens
- **When** the rise is below 80% of the moved block's estimated token count
- **Then** this story does not merge
- **And** the remaining stories merge without it

---

#### Scenario: The moved block sits at the required position

**Traces to**: User Story 7, Acceptance Scenario 3
**Category**: Happy Path

- **Given** an assembled request
- **When** its stable region is inspected
- **Then** the moved block is at index 1
- **And** the block before it is the existing stable block
- **And** the block after it is the first per-turn-varying block
- **And** the moved block carries an ephemeral cache marker

---

#### Scenario: A misplaced moved block fails the structural check

**Traces to**: User Story 7, Acceptance Scenario 3
**Category**: Error Path

- **Given** the moved block placed after the first per-turn-varying block
- **When** the structural check runs
- **Then** it fails

---

#### Scenario: A moved block without a cache marker fails the structural check

**Traces to**: User Story 7, Acceptance Scenario 3
**Category**: Error Path

- **Given** the moved block at index 1 with no cache marker
- **When** the structural check runs
- **Then** it fails

---

#### Scenario: A turn that makes a new tool usable still assembles correctly

**Traces to**: User Story 7, Acceptance Scenario 4
**Category**: Edge Case

- **Given** a conversation on a turn where a search-only tool is newly made usable
- **When** the request is assembled
- **Then** the volatile record of which tools are usable reflects the new tool
- **And** the stable moved block is unchanged

---

#### Scenario: A provider that discards the structured stable region still works

**Traces to**: User Story 7, Acceptance Scenario 5
**Category**: Alternate Path

- **Given** a provider that receives only concatenated system text
- **When** a request is assembled
- **Then** it contains the full system content
- **And** no cache marker is required for correctness

---

#### Scenario: No more than two cache breakpoints are used

**Traces to**: User Story 7, Acceptance Scenario 3
**Category**: Edge Case

- **Given** an assembled request with this change active
- **When** its cache markers are counted
- **Then** there are exactly 2

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit | Single package, no gateway, no provider | Classification, ranking-band arithmetic, name-set drift, fold arithmetic, bucket-key composition |
| Integration | Two or more packages wired together, in-process gateway where needed | Permission filtering end to end, turn-build output, frame emission, metrics endpoint reachability, startup conversion |
| E2E | Built binary or browser | Chat-thread visibility, active-agent indicator, UAT probe scripts |

### Workstream ordering (from the ADR's §10, with the resolutions this spec adds)

```
W-D1 (rename, alone, its own reviewed unit)
   ↓
W1 (+W5 docs, same commit)  ∥  W3 (visibility + policy filter + bucket key + counters)
   ↓
W4 (ambiguity band)
   ↓
W2 (cache boundary, gated — may be developed in parallel, merges last)
```

Three orderings are **load-bearing, not advisory**:

1. Within W1, the static catalog edit precedes the seed edits. Reversed, startup panics on a seed override naming a tool absent from the catalog.
2. W3 owns the permission-filter rewrite of the discovery loop, not W4 — otherwise W3 ships into a window where its own headline property is false on the discovery channel.
3. W6 (the upgrade conversion) is part of W1's commit and its conversion step precedes the coverage repair at startup. Reversed, the repair writes denials and startup succeeds.

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|---|---|---|---|---|
| **W-D1 — rename** | | | | |
| 1 | `TestCanonicalDiscoveryToolName_PinsProductionSymbol` | Unit (TS) | Scenario: The canonical-name guard can fail | Re-points the existing tautological guard at the production display-label mapping so it can actually fail |
| 2 | `TestToolVisibility_DiscoveryHiddenInThreadByDefault` | Unit (TS) | Scenario: Discovery calls stay hidden from the chat thread by default | Asserts the new name positively, not inherited from a fallback arm |
| 3 | `TestToolVisibility_DiscoveryHiddenInPanelByDefault` | Unit (TS) | Scenario: Discovery calls stay hidden from the chat thread by default | The second visibility site, asserted separately |
| 4 | `TestToolVisibility_DiscoveryVisibleWhenVerbose` | Unit (TS) | Scenario: Verbose output reveals discovery calls | The override still works |
| 5 | `TestToolVisibility_DiscoveryVisibleOnError` | Unit (TS) | Scenario: A failed discovery call is still shown | Error forces visibility, preserved |
| 6 | `TestHumanizeToolName_LegacyAliasRetained` | Unit (TS) | Scenario: A conversation recorded before the rename still renders readably | Old transcripts keep a readable label |
| 7 | `TestInfraManifestToolNames_ContainsRenamedTool` | Unit (Go) | Scenario: The capability answers to its new name on the by-name path | Classifier map key |
| 8 | `TestRegistrationGuard_DerivesFromInfraNames` | Unit (Go) | Scenario: The registration guard prevents a second registration | Guard reads the name set, not a literal |
| 9 | `TestToolsTool_NameIsRenamed` | Unit (Go) | Scenario: The capability answers to its new name on the by-name path | Tool identity |
| 10 | `TestStaticCatalog_ContainsRenamedDiscoveryTool` | Unit (Go) | Scenario: The startup permission validator finds the renamed capability on every agent | Catalog membership |
| 11 | `TestContractDescription_NamesOnlyLiveTools` | Integration | Scenario: The published exposure-level description names only live capabilities | Regenerated schema names the new tool and no retired one |
| 12 | `TestDiscoveryTool_ByNameAndByDescriptionUnchanged` | Integration | Scenarios: capability answers to its new name (both) | Behaviour parity across the rename |
| 13 | `TestRegistrationGuard_SurvivesLiveConfigToggle` | Integration | Scenario: The registration guard prevents a second registration | The scenario the guard's own comment cites |
| 14 | `sandbox-uat/probe-t.sh` prompt strings updated | E2E | Scenario: The capability answers to its new name on the by-name path | The probe instructs a live agent by tool name; a stale name makes T.14/T.15 report FAIL for an unrelated reason |
| 15 | Embedded SPA re-sync verification | E2E | Scenario: Discovery calls stay hidden from the chat thread by default | Confirms the built binary serves the fixed visibility logic, not a stale embedded bundle |
| **W1 — agent switching** | | | | |
| 16 | `TestSwitchAgent_TargetIsOnlyRequiredParameter` | Unit (Go) | Scenario: A switch without the explanatory note succeeds | Declared parameter shape |
| 17 | `TestSwitchAgent_SentinelAlwaysWins` | Unit (Go) | Scenario: An agent whose identifier is the reserved word is warned about at startup | Resolution by rule, not lookup order. Also asserts the comparison is **exact**: a differently-cased spelling of the reserved word is an ordinary identifier lookup and fails as not-found |
| 18 | `TestSwitchAgent_UnknownTargetFails` | Unit (Go) | Scenario: Switching to an unknown identifier fails | Error text contains the identifier |
| 19 | `TestSwitchAgent_WorkerTargetRejectedOnBothBranches` | Unit (Go) | Scenarios: Switching to a delegation-only worker fails; Returning to a default that points at a worker fails | The check becomes unconditional |
| 20 | `TestSwitchAgent_TimeoutCeilingOnBothBranches` | Unit (Go) | Scenario: The switching operation completes within its ceiling on both branches | The wrapper becomes unconditional |
| 20a | `TestAgentSwitchedFrame_TimedOutSwitchEmitsNothing` | Integration | Scenario: A switch that exceeds the ceiling emits no active-agent signal | **Net-new.** Drives a destination that resolves normally but whose operation exceeds the ceiling, on both branches; asserts an error naming the timeout, **zero** active-agent signals, and an unchanged active agent. Distinct from test 29 (`TestAgentSwitchedFrame_FailedSwitchEmitsNothing`), which fails at resolution time and never enters the timed region |
| 21 | `TestSwitchAgent_TranscriptTransferSkippedForDefault` | Unit (Go) | Scenario: A return to default transfers no brief | The one deliberately conditional step |
| 22 | `TestSwitchAgent_TranscriptTransferRunsForNamedTarget` | Unit (Go) | Scenario: A switch to a named agent transfers a budgeted brief | Budget split at half the target's window |
| 23 | `TestSwitchAgent_AuditPrefixFrozen` | Unit (Go) | Scenario: Replay of a stored handoff still signals the active agent | The recorded prefix must not drift |
| 24 | `TestExcludedSwitchAgent_AbsentFromChildRegistry` | Unit (Go) | Scenario: A delegated sub-task cannot switch the parent conversation | Asserts the **new** name is absent, after registering the real tool |
| 25 | `TestDelegateGrandchild_SwitchAgentAbsent` | Unit (Go) | Scenario: A nested delegated sub-task also cannot switch | **Repairs a live vacuous assertion**: the existing test never registers the tool it asserts is absent |
| 26 | `TestSubturnLog_ExcludedFieldDerived` | Unit (Go) | Scenario: A delegated sub-task cannot switch the parent conversation | The operator-facing debug log must not carry a hand-copied stale name |
| 27 | `TestAgentSwitchedFrame_NamedTargetEmitsWithIdentifier` | Integration | Scenario: Switching to a named agent emits the active-agent signal | **Net-new coverage — this path has none today** |
| 28 | `TestAgentSwitchedFrame_DefaultTargetEmitsWithoutIdentifier` | Integration | Scenario: Returning to default emits the signal with no identifier | Positive assertion, not absence of the old name |
| 29 | `TestAgentSwitchedFrame_FailedSwitchEmitsNothing` | Integration | Scenario: A failed switch emits no signal | |
| 30 | `TestAgentSwitchedFrame_DefaultByIdentifierEmitsWithoutIdentifier` | Integration | Scenario: Switching to the agent that happens to be the default emits no identifier | The one accepted behaviour delta |
| 31 | `TestAgentSwitchedFrame_UnresolvableActiveAgentWarns` | Integration | Scenario: The active agent cannot be resolved after a successful switch | Silence must not be the only outcome |
| 32 | `TestReplay_HandoffEntryStillEmitsFrame` | Integration | Scenario: Replay of a stored handoff still signals the active agent | Replay is untouched; this pins that |
| 33 | `TestBoot_CollidingDefaultAgentIdentifierWarns` | Integration | Scenario: An agent whose identifier is the reserved word is warned about at startup | Startup succeeds, warns, sentinel still resolves correctly. **Covers FR-057 only** — this is the pre-existing-install path, not the creation path |
| 33a | `TestAgentCreate_RejectsReservedIdentifier` | Integration | Scenario: Creating an agent named with the reserved word is rejected | **Gated on FR-058's ratification.** Client error, nothing written; identifier and display name, in mixed case. Net-new — no test in this plan previously exercised FR-058's own behaviour |
| 33b | `TestAgentUpdate_RejectsReservedIdentifier` | Integration | Scenario: Updating an agent to the reserved word is rejected | **Gated on FR-058's ratification.** The update path is a separate handler and needs its own assertion |
| **W1/W6 — upgrade conversion** | | | | |
| 34 | `TestPolicyKeyFold_StrictestWins` | Unit (Go) | Scenario Outline: The strictest-wins fold | All six rows of the fold table |
| 35 | `TestPolicyKeyMigration_LegacyKeysDeleted` | Unit (Go) | Scenario: Disagreeing legacy entries resolve to the strictest | No retired name survives |
| 36 | `TestPolicyKeyMigration_Idempotent` | Unit (Go) | Scenario: Conversion is byte-identical on a second start | Byte comparison, not semantic |
| 37 | `TestPolicyKeyMigration_CoversGlobalAndPerAgent` | Unit (Go) | Scenario: Both install-wide and per-agent records convert | Both map shapes |
| 38 | `TestBoot_MigrationPrecedesCoverageRepair` | Integration | Scenario: The discovery capability is never denied by the coverage repair | **The regression that otherwise ships green** — startup succeeds either way |
| 39 | `TestBoot_LegacyAllowSurvivesUpgrade` | Integration | Scenario: A permitted legacy handoff entry converts to a permitted new entry | No denial backfilled |
| 40 | `TestBoot_ValidationPassesAfterConversion` | Integration | Scenario: Startup validation finds no coverage gap after conversion | No abort |
| 41 | `TestPolicyKeyMigration_InterruptedWriteLeavesOriginal` | Integration | Scenario: An interrupted conversion leaves the original intact | Atomic replace |
| **W3 — permission filtering, visibility, bucket key, counters** | | | | |
| 42 | `TestSearch_DeniedResultAbsentFromMatchList` | Unit (Go) | Scenario: Denied tool ranked mid-list is absent from the answer | Name **and** description absent |
| 43 | `TestSearch_AllDeniedReturnsSilentNoMatch` | Unit (Go) | Scenario: Every ranked result is denied | The listing branch is deleted, not repurposed |
| 44 | `TestSearch_NilResolverDisclosesNothing` | Unit (Go) | Scenario: Discovery with no permission resolver discloses nothing | Fails closed |
| 45 | `TestSearch_RankingInvariance` | Unit (Go) | Scenario: One agent's denials do not alter another agent's ranking | **Protects the decision, not the behaviour** — fails if anyone converts this to corpus exclusion |
| 46 | `TestSearch_PermissionScanCapped` | Unit (Go) | Scenario: The permission walk is bounded on a large tool set | At most 50 checks; a tripped cap truncates, never admits |
| 47 | `TestSearch_ZeroResultCounterIncrements` | Integration | Scenario: Empty permitted result set increments the operator counter | Asserted on the endpoint body |
| 48 | `TestFuzzySuggestion_ReadsPermissionFilteredSet` | Unit (Go) | Scenario: A near-miss probe does not suggest a denied tool's real name | The second unfiltered surface |
| 49 | `TestRegistrySearchBM25_Removed` | Unit (Go) | Scenario: Denied tool ranked mid-list is absent from the answer | The unfiltered exported search method is deleted, not documented |
| 50 | `TestVisibility_PreviewedSetIsExactlyEight` | Unit (Go) | Scenario: The per-turn listing names only the previewed set | Drift test, count and contents |
| 51 | `TestVisibility_EveryCatalogNameHasRecordedLevel` | Unit (Go) | Scenario: Adding a tool without recording its exposure level fails the build | Forces a decision on every added tool |
| 52 | `TestVisibility_SearchOnlyToolsRemainInSearchIndex` | Unit (Go) | Scenario: Every search-only tool remains findable | The failure mode a fourth tier value would have caused. **Static index membership only** — it asserts what the index contains, and asserts nothing about what a search *does* with a hit. Test 52a is the other half |
| 52a | `TestVisibility_SearchOnlyToolFoundByDescriptionBecomesUsable` | Integration | Scenario: An unlisted tool is found by description | **Net-new, and it closes an orphaned scenario**: "An unlisted tool is found by description" had no test of its own in this plan, and FR-031 cited test 52 for it — a test that only proves index membership. Drives a real by-description search for a **search-only** tool and asserts (a) the search makes it usable and (b) its full schema is present in the next turn's callable set. Distinct from test 12 (`TestDiscoveryTool_ByNameAndByDescriptionUnchanged`, W-D1), which is rename-parity on the pre-existing tiering and never exercises the search-only level, and from test 73 (`TestAmbiguity_DominantHitPromotesAlone`, W4), which pins promotion cardinality rather than exposure level |
| 53 | `TestVisibility_TierArithmetic` | Unit (Go) | Scenario Outline: Reclassified tools land at the intended level | 17 + 8 + 63 + 1 = 89. **Expected data MUST be transcribed from ADR-071 §4.1's four literal name lists**, never from a count and never from a table in this spec. A count-only assertion cannot distinguish the correct 6-out/5-in diff from the wrong 3-out/2-in one — both total 17. Assert membership name by name, in both directions |
| 54 | `TestAdministrativeToolNames_Drift` | Unit (Go) | Scenarios: Adding a tool without adjudicating its destructive status; Renaming a listed destructive tool | Four assertions: exact set, all present in catalog, all resolve search-only, name-shape tripwire |
| 55 | `TestManifest_RenderedBlockIsTwentyTwoLines` | Unit (Go) | Scenario: The per-turn listing names only the previewed set | Line count, not tool count |
| 56 | `TestManifest_RevertControlRestoresFullListing` | Unit (Go) | Scenario: The revert control restores the full listing | ~101 lines |
| 57 | `TestManifest_RevertControlDoesNotUndoPermissionFilter` | Integration | Scenario: The revert control does not undo permission filtering | The revert unwinds the bet, not the corrections |
| 58 | `TestManifest_RevertControlDoesNotUndoPerAgentScoping` | Integration | Scenario: The revert control does not undo per-agent scoping | Same principle, second correction |
| 59 | `TestManifest_HeaderStatesMoreToolsExist` | Unit (Go) | Scenario: The listing header tells the model more tools exist | Prose is the cheapest mitigation for the accepted risk |
| 60 | `TestBucketKey_CrossAgentIsolation` | Integration | Scenario: A second agent does not inherit the first agent's usable set | The property the headline number depends on |
| 61 | `TestBucketKey_RoundTripPreservation` | Integration | Scenario: Switching back restores the first agent's usable set | Distinct buckets, not a reset |
| 62 | `TestBucketKey_NoLeakAfterSessionClose` | Integration | Scenario: Ending a conversation discards every agent's usable set | **Ships green if missed** — a leak has no symptom until memory growth |
| 63 | `TestPendingDiscoveries_NoSideTableLeak` | Integration | Scenario: Ending a conversation discards every agent's usable set | The second map, swept by the same scan |
| 64 | `TestStaticPromotion_SurvivesBeyondDiscoveryLifetime` | Integration | Scenarios: A static tool made usable stays usable for the conversation; A static tool made usable does not decay with the discovery lifetime | **FR-037 — the new, permanent `loadedTools` mechanism.** Load a static `ManifestLazy` tool, drive more turns than `cfg.Tools.MCP.Discovery.TTL` calling `TickTTL` on each, assert the tool is still in the usable-set record and still in `buildCompressedToolDefs`'s output. **Stops the documented claim drifting back**, and covers the plain multi-turn scenario as a strict subset |
| 65 | `TestExternalPromotion_DecaysWithDiscoveryLifetime` | Integration | Scenario: A discovered external tool does decay with the discovery lifetime | **FR-037a — the pre-existing, untouched MCP registry TTL.** The mirror assertion; **the two tests MUST live in one file** so that file documents the split. This test asserts a decay path deliberately: it is not the eviction FR-037 forbids, because it exercises `PromoteTools`/`TickTTL` on a non-core entry, not the `loadedTools` record |
| 66 | `TestNoFollowUp_CountsOnceAfterHorizon` | Integration | Scenario: An unused discovery is counted once after the horizon | Exactly 1, and no climb on turn 7 |
| 67 | `TestNoFollowUp_UsedDiscoveryNeverCounts` | Integration | Scenario: A used discovery is never counted | The negative case |
| 68 | `TestNoFollowUp_ByNameLoadNeverCounts` | Integration | Scenario: A by-name load never creates a pending discovery | Avoids a false-positive floor |
| 69 | `TestNoFollowUp_CrossAgentIsolation` | Integration | Scenario: Another agent's use does not clear the first agent's pending discovery | Reads 1, not 0 |
| 70 | `TestNoFollowUp_CountedAtSessionClose` | Integration | Scenario: A conversation shorter than the horizon still counts its unused discoveries | Count before delete, in that order |
| 71 | `TestMetricsEndpoint_BothCounterSeriesPresent` | Integration | Scenario: Both counter series are reachable on the metrics endpoint | **Asserted on the endpoint, never the accessor** |
| **W4 — ambiguity band** | | | | |
| 72 | `TestAmbiguity_ScoreBandPromotesRunnerUp` | Unit (Go) | Scenario: Two near-tied results are both made usable | The 0.80 rule |
| 73 | `TestAmbiguity_DominantHitPromotesAlone` | Unit (Go) | Scenario: A dominant top result is made usable alone | Today's behaviour, byte for byte |
| 74 | `TestAmbiguity_CrossKindNearMissPromotes` | Unit (Go) | Scenario: A cross-kind near-miss is made usable | The 0.50 rule |
| 74a | `TestAmbiguity_KindIsToolCategory` | Unit (Go) | Scenario: The cross-kind rule is decided by the tools' own category values | Pins "kind" to `Tool.Category()` using **real** tools with real categories (`send_file`/`communication` vs `write_file`/`filesystem`), so a reimplementation on an invented grouping fails. The synthetic same/different scenarios cannot catch that |
| 75 | `TestAmbiguity_DestructiveExcludedFromSpeculativeBand` | Unit (Go) | Scenario: A destructive cross-kind near-miss is excluded | The narrowing |
| 76 | `TestAmbiguity_ConfirmationGatedExcludedFromSpeculativeBand` | Unit (Go) | Scenario: A confirmation-gated cross-kind near-miss is excluded | Second half of the narrowing |
| 77 | `TestAmbiguity_DestructiveStillPromotedInConfidentBand` | Unit (Go) | Scenario: A destructive result inside the confident band is still made usable | The narrowing applies to rule 2 only |
| 78 | `TestAmbiguity_CappedAtThree` | Unit (Go) | Scenario: The number made usable is capped at three | |
| 79 | `TestAmbiguity_TieBreakIsRankOrder` | Unit (Go) | Scenario: The number made usable is capped at three | Which three, not just how many |
| 80 | `TestAmbiguity_SingleCandidateSkipsComparison` | Unit (Go) | Scenario: A single permitted result skips the comparison | Degenerate low end |
| 81 | `TestAmbiguity_BoundaryTable` | Unit (Go) | Scenario Outline: The closeness comparison at its boundaries | Six boundary rows |
| 82 | `TestAmbiguity_ByNameFlowUnchanged` | Unit (Go) | Scenario: The by-name path is unaffected by the comparison | |
| 83 | `TestAmbiguity_ResponseCardinalityOnly` | Unit (Go) | Scenario: Two near-tied results are both made usable | The result carries an array and a map already; only cardinality changes |
| **W2 — cache boundary (gated)** | | | | |
| 84 | `TestContextBlocks_CatalogAtIndexOne` | Unit (Go) | Scenario: The moved block sits at the required position | **The offline guard that outlives the one-off measurement** |
| 85 | `TestContextBlocks_MisplacedCatalogFails` | Unit (Go) | Scenario: A misplaced moved block fails the structural check | Proves the guard can fail |
| 86 | `TestContextBlocks_CatalogCarriesCacheMarker` | Unit (Go) | Scenario: A moved block without a cache marker fails the structural check | |
| 87 | `TestContextBlocks_AtMostTwoCacheMarkers` | Unit (Go) | Scenario: No more than two cache breakpoints are used | Headroom is finite |
| 88 | `TestContextBlocks_LoadedDeltaStaysOutside` | Unit (Go) | Scenario: A turn that makes a new tool usable still assembles correctly | The volatile part is never stale |
| 89 | `TestProviderWithoutMarkers_AssemblesCorrectly` | Integration | Scenario: A provider that discards the structured stable region still works | Two providers, one outcome |
| 90 | **Manual cache-read measurement** | E2E (manual, operator-owned) | Scenarios: Cache-read volume rises; A rise below the floor drops this story | **Not in CI.** Two numbers recorded in the merge request body as the merge artifact |
| **Cross-cutting** | | | | |
| 91 | `TestBlanketGrep_NoLiveRetiredNames` | Integration | (Behavioral contract, blanket pass) | Zero hits for the three retired names across the live surface, excluding dated records and front-matter notes |

### Test Datasets

#### Dataset: Permission filtering of the discovery match list

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | Ranked `[allow, deny, allow, allow, allow]` | Denied mid-list | 4 names, original relative order | Scenario: Denied tool ranked mid-list is absent | The r4 review's named dataset gap |
| 2 | Ranked `[deny, allow, allow]` | Denied at rank 1 | 2 names; the reference score becomes the first allowed result's | Scenario: Denied tool ranked mid-list is absent | The band's reference point shifts |
| 3 | Ranked `[deny, deny, deny]` | All denied | Exactly `No tools found matching the query.` | Scenario: Every ranked result is denied | **Not a listing** |
| 4 | Ranked `[]` | Empty ranking | Same exact text | Scenario: Every ranked result is denied | Pre-existing path, unchanged |
| 5 | No resolver attached | Nil dependency | Same exact text | Scenario: no permission resolver discloses nothing | Fail closed |
| 6 | Ranked list of 60, first allowed at position 55 | Scan cap | At most 50 checks; 0 names returned | Scenario: The permission walk is bounded | Truncates, never admits |
| 7 | Ranked list of 60, first allowed at position 3 | Below cap | Normal result | Scenario: Denied tool ranked mid-list is absent | The cap does not bind in normal operation |
| 8 | Result limit configured to 1 | Min | 1 name, no comparison | Scenario: A single permitted result skips the comparison | |
| 9 | Result limit configured to 0 | Zero | Empty result, `No tools found matching the query.`, counter +1 | Scenario: Every ranked result is denied | **Resolved ambiguity** — no special-casing |
| 10 | Result limit configured to −1 | Negative | Identical to row 9 | Scenario: Every ranked result is denied | **Resolved ambiguity** |
| 11 | Same query, agent A (all allow) vs agent B (3 deny) | Cross-agent | B's permitted results in A's relative order, identical scores | Scenario: One agent's denials do not alter another's ranking | Fails if converted to corpus exclusion |
| 12 | Misspelling within similarity threshold of a denied tool | Fuzzy near-miss | No suggestion naming it | Scenario: A near-miss probe does not suggest a denied name | Name-only channel, still a leak |
| 13 | Misspelling within threshold of an allowed tool | Fuzzy near-miss | Suggestion returned | Scenario: A near-miss probe does not suggest a denied name | Confirms the fix does not over-narrow |
| 14 | Misspelling shorter than the minimum input length | Below min | No suggestion, as today | Scenario: A near-miss probe does not suggest a denied name | Pre-existing guard |
| 15 | Permission value `ask` | Middle value | Present in the answer | Scenario Outline: Discovery answers are a subset | `ask` is loadable; only `deny` is filtered |

#### Dataset: The closeness comparison

| # | Input (top score, runner-up score, kind, status) | Boundary Type | Expected made-usable count | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | 10.0, 8.0, same, ordinary | Exact 0.80 | 2 | Scenario Outline: boundaries | Inclusive lower bound |
| 2 | 10.0, 7.99, same, ordinary | Just below 0.80 | 1 | Scenario Outline: boundaries | |
| 3 | 10.0, 10.0, same, ordinary | Exact tie | 2 | Scenario: Two near-tied results | |
| 4 | 10.0, 5.0, different, ordinary | Exact 0.50 | 2 | Scenario Outline: boundaries | Inclusive lower bound |
| 5 | 10.0, 4.99, different, ordinary | Just below 0.50 | 1 | Scenario Outline: boundaries | |
| 6 | 10.0, 5.0, same, ordinary | Cross-kind rule needs a kind change | 1 | Scenario Outline: boundaries | Rule 2 requires both conditions |
| 7 | 10.0, 5.0, different, destructive | Narrowing | 1 | Scenario: A destructive cross-kind near-miss is excluded | |
| 8 | 10.0, 5.0, different, confirmation-gated | Narrowing | 1 | Scenario: A confirmation-gated cross-kind near-miss is excluded | |
| 9 | 10.0, 8.5, same, destructive | Confident band, destructive | 2 | Scenario: A destructive result inside the confident band | Narrowing applies to rule 2 only |
| 10 | 10.0, 8.5, same, confirmation-gated | Confident band, gated | 2 | Scenario: A destructive result inside the confident band | Same principle |
| 11 | 10.0 / 9.5 / 9.2 / 9.0 / 8.8, all same, ordinary | Above cap | 3, the highest-ranked | Scenario: The number made usable is capped at three | Rank order breaks the tie |
| 12 | 10.0 only | Single candidate | 1 | Scenario: A single permitted result skips the comparison | No `i = 2` exists |
| 13 | Empty permitted set | Zero candidates | 0, and the no-match response | Scenario: Every ranked result is denied | Degenerate low end |
| 14 | 0.0, 0.0, same, ordinary | Zero scores | 1 | Scenario Outline: boundaries | A ratio against zero must not promote or divide by zero |
| 15 | 10.0, 8.0, different, ordinary | Both rules qualify | 2 | Scenario: Two near-tied results | Rules are alternatives, not additive |
| 16 | 9.1 `send_file` (`communication`), 5.2 `write_file` (`filesystem`), ordinary | **Real tools, real categories** | 2 | Scenario: The cross-kind rule is decided by the tools' own category values | Pins "kind" to `Tool.Category()`. The ADR's own worked example |
| 17 | 9.1 `list_agents` (`agents`), 5.2 `read_agent_metadata` (`agents`), ordinary | Real tools, same category | 1 | Scenario: The cross-kind rule is decided by the tools' own category values | The negative half of row 16 — same `Tool.Category()`, so rule 2 does not fire even though the ratio clears 0.50 |
| 18 | 10.0, −0.5, different, ordinary | Negative runner-up | 1 | Scenario Outline: boundaries | Below both thresholds. Not reachable with the current ranking function (its IDF term cannot go negative) — asserted so a later ranking change cannot make it reachable silently |

#### Dataset: Exposure-level classification and drift

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | Full catalog | Total | 89 names | Scenario Outline: Reclassified tools land at the intended level | 90 today, −2 merged, +1 new |
| 2 | Always-listed set | Exact count **and exact membership** | 17, matching ADR-071 §4.1's Tier 1 list name for name | Scenario Outline: Reclassified tools | 18 today; **6 leave** (`bash`, `navigate`, `create_task`, `update_task` → previewed; `hand_off`, `return_to_default` deleted), **5 join** (`switch_agent`, `list_mounts`, `send_file`, `message_parent`, `recall_conversation`), 12 stay. 18 − 6 + 5 = 17. **A count-only assertion is insufficient**: an incorrect 3-out/2-in diff also totals 17 |
| 3 | Previewed set | Exact count | 8 | Scenario: The per-turn listing names only the previewed set | |
| 4 | Search-only set | Exact count | 63 | Scenario: Every search-only tool remains findable | |
| 5 | Infrastructure set | Exact count | 1 | Scenario: The startup validator finds the renamed capability | |
| 6 | Rendered listing, permitted-everything agent, nothing usable | Max | **exactly 22 lines** | Scenario: The per-turn listing names only the previewed set | `2 header + 6 categories × 2 + 8 entries = 22`. **Verified in this session** against `BuildCompressedManifest`'s real emission shape (`pkg/tools/manifest.go`), not carried over from the ADR. Method: the renderer emits 2 unconditional header lines, then per category a blank line plus a `## <category>` line (the literal `"\n## %s\n"`), then one `  - name — desc` line per entry — so lines = `2 + 2C + N`. `C` was derived by reading the real `Category()` method of each of ADR §4.1's 8 Tier 2 names rather than assuming: `list_agents`→`agents`, `list_jobs`/`create_task`/`update_task`→`tasks`, `serve_web`→`web`, `navigate`→`platform`, `get_workspace`→`workspaces`, `bash`→`shell`. **6 distinct categories**, `N = 8` → 22 |
| 7 | Same, with the revert control set | Max reverted | **exactly 101 lines** | Scenario: The revert control restores the full listing | `2 + 14 × 2 + 71 = 101`. **Verified in this session**, same method: with the revert control set every lazy tool is previewed, so `N = 71` (the post-change lazy set, 89 − 17 always-listed − 1 infrastructure) and `C = 14` — `agents`, `browser`, `channels`, `communication`, `filesystem`, `mcp`, `memory`, `platform`, `providers`, `shell`, `skills`, `tasks`, `web`, `workspaces` — each confirmed to have at least one member in ADR §4.1's Tier 2 + Tier 3 lists by reading the real `Category()` methods (`pkg/sysagent/tools/category.go` and the per-tool files). **SC-001's "approximately 101" refers to a different quantity** — today's *pre-change* listing, whose lazy membership differs by four swapped names and whose category count was not separately re-derived. This row's 101 is exact; SC-001's is a baseline estimate and is deliberately left approximate |
| 8 | A new catalog name on no level list | Missing decision | Build failure naming it | Scenario: Adding a tool without recording its exposure level | |
| 9 | A previewed name removed from the set | Silent deletion | Build failure | Scenario: Adding a tool without recording its exposure level | Drift in either direction |
| 10 | An externally-provided tool | Default direction | Search-only | Scenario: Every search-only tool remains findable | Correct, and a no-op — these never reached the listing |
| 11 | Destructive-and-install-wide list | Exact count | 13 | Scenario: Adding a tool without adjudicating its destructive status | The seed; see the ambiguity table |
| 12 | A listed destructive name renamed in the catalog | Rename drift | Build failure | Scenario: Renaming a listed destructive tool | Catches a dead exclusion string |
| 13 | A listed destructive name promoted out of search-only | Level drift | Build failure | Scenario: Adding a tool without adjudicating its destructive status | The narrowing is meaningless above search-only |
| 14 | New catalog name `revoke_credential` | Name-shape tripwire | Build failure unless adjudicated or exempted | Scenario: Adding a tool without adjudicating its destructive status | The ADR's own worked example |
| 15 | New catalog name `purge_workspace_data` | Name-shape tripwire | Build failure unless adjudicated or exempted | Scenario: Adding a tool without adjudicating its destructive status | |
| 16 | New catalog name `archive_everything` | **Benign-looking destructive** | **Not detected** | Scenario: Adding a tool without adjudicating its destructive status | **Documented residual gap** — the tripwire is a backstop, not a definition |

#### Dataset: Usable-set bucket keys

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | Agent A, conversation C1 | Happy path | A distinct bucket | Scenario: A second agent does not inherit | |
| 2 | Agent B, conversation C1 | Same conversation, different agent | A different bucket from row 1 | Scenario: A second agent does not inherit | The whole point |
| 3 | Agent A, conversation C2 | Same agent, different conversation | A different bucket from row 1 | Scenario: Switching back restores | Pre-existing isolation preserved |
| 4 | Empty conversation identifier | Empty | No bucket created | Scenario: Ending a conversation discards | Preserves the deliberate no-op key |
| 5 | A delegated sub-task under agent A | Delegation | Its own bucket, distinct from the parent's | Scenario: A second agent does not inherit | The parent/child isolation invariant, preserved |
| 6 | Close conversation C1 with buckets under two agents | Sweep | Zero buckets remain for C1 | Scenario: Ending a conversation discards | An exact-key delete would match nothing |
| 7 | Close C1 while C2 has buckets | Selective sweep | C2's buckets survive | Scenario: Ending a conversation discards | The suffix scan must not over-delete |
| 8 | Close C1 with pending discoveries under two agents | Sweep, second map | Zero pending records remain, and both were counted first | Scenario: A conversation shorter than the horizon still counts | Count-then-delete ordering |

#### Dataset: Agent-switch destination resolution

| # | Input `target` | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | A registered non-worker agent's identifier | Happy path | Switch succeeds, signal carries the identifier | Scenario: Switching to a named agent emits the signal | |
| 2 | The reserved word | Sentinel | Switch to configured default, signal carries no identifier | Scenario: Returning to default emits the signal | |
| 3 | The configured default agent's own identifier | Overlap | Switch succeeds, signal carries **no** identifier | Scenario: Switching to the agent that happens to be the default | The one accepted delta |
| 4 | An identifier naming no agent | Not found | Failure naming the identifier, no signal | Scenario: Switching to an unknown identifier fails | |
| 5 | A delegation-only worker's identifier | Rejected role | Failure, no signal | Scenario: Switching to a delegation-only worker fails | |
| 6 | The reserved word, with the default setting pointing at a worker | Rejected role, sentinel branch | Failure, no signal | Scenario: Returning to a default that points at a worker fails | The check becomes unconditional |
| 7 | The reserved word, uppercase (`DEFAULT`) | Case | **Failure naming the identifier, no signal** — unless an agent genuinely bears that identifier, in which case it is an ordinary switch to that agent | Scenario: Switching to an unknown identifier fails | **The sentinel comparison is exact.** An earlier draft of this row asserted case-insensitive sentinel resolution; that is not what the ADR pins (`target == "default"`). Case-insensitivity applies only to the creation/update rejection (FR-058) — see the edge case explaining why the two differ deliberately |
| 8 | The reserved word, on an install where an agent bears that identifier | Collision | Configured default, not the colliding agent; startup warned | Scenario: An agent whose identifier is the reserved word is warned about | Rule, not lookup order |
| 9 | Empty string | Empty | Failure, no signal | Scenario: Switching to an unknown identifier fails | Required parameter |
| 10 | Omitted | Missing required | Failure, no signal | Scenario: Switching to an unknown identifier fails | |
| 11 | A registered agent, note omitted | Optional parameter absent | Success, recorded entry keeps today's shape | Scenario: A switch without the explanatory note succeeds | Including its empty trailing segment |
| 12 | A registered agent, note present | Optional parameter present | Success, note carried into the recorded entry | Scenario: A switch to a named agent transfers a budgeted brief | |
| 13 | Very long note (10 KB) | Very long string | Success; the brief is budget-bounded | Scenario: A switch to a named agent transfers a budgeted brief | Split at half the target's window |
| 14 | Unicode and emoji in the note | Unicode | Success, content preserved | Scenario: A switch without the explanatory note succeeds | No emoji is stored in system chrome; a user note is content |
| 15 | Agent **created** with identifier `default` | Reserved word, creation path | Client error naming the reserved word; nothing written | Scenario: Creating an agent named with the reserved word is rejected | **Gated on FR-058.** A different path and a different outcome from row 8's startup warning |
| 16 | Agent created with identifier `Default` / display name `DEFAULT` | Reserved word, creation path, mixed case | Same rejection | Scenario: Creating an agent named with the reserved word is rejected | **Gated on FR-058.** The rejection is case-insensitive — deliberately unlike row 7's exact sentinel comparison |
| 17 | Existing agent **updated** to identifier `default` | Reserved word, update path | Same rejection; stored agent unchanged | Scenario: Updating an agent to the reserved word is rejected | **Gated on FR-058.** Separate handler, separate assertion |
| 18 | A registered non-worker agent's identifier, operation exceeding the 10-second ceiling | **Timeout, named-target branch** | Failure naming the timeout, **no signal**, active agent unchanged | Scenario: A switch that exceeds the ceiling emits no active-agent signal | The destination resolves; the *operation* is what fails. Distinct from row 4, which fails before the timed region is entered |
| 19 | The reserved word, operation exceeding the 10-second ceiling | **Timeout, sentinel branch** | Same: failure, no signal, active agent unchanged | Scenario: A switch that exceeds the ceiling emits no active-agent signal | The ceiling is new on this branch (regression row 10) and its failure outcome must be asserted, not assumed to mirror the other branch |

#### Dataset: Stored permission conversion

| # | Input (legacy handoff / legacy return / legacy discovery / existing new) | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | allow / allow / allow / — | Happy path | switch: allow, discovery: allow, no legacy keys | Scenario: A permitted legacy handoff entry converts | The mainline upgrade |
| 2 | allow / deny / allow / — | Disagreement | switch: **deny** | Scenario: Disagreeing legacy entries resolve to the strictest | |
| 3 | ask / allow / allow / — | Middle value | switch: **ask** | Scenario Outline: The strictest-wins fold | deny > ask > allow |
| 4 | allow / — / allow / deny | Destination present | switch: **deny** | Scenario: An existing strict entry under the new name survives | Makes the fold monotone |
| 5 | — / — / — / allow | No legacy keys | Unchanged, byte-identical | Scenario: Conversion is byte-identical on a second start | Idempotent by construction |
| 6 | deny / deny / deny / deny | All strict | All deny, no legacy keys | Scenario Outline: The strictest-wins fold | deny → deny is harmless |
| 7 | Install-wide only | Single map | Converted | Scenario: Both install-wide and per-agent records convert | |
| 8 | Per-agent only, two agents | Multiple maps | Both converted | Scenario: Both install-wide and per-agent records convert | |
| 9 | Both, disagreeing between the two maps | Independent maps | Each folded independently | Scenario: Both install-wide and per-agent records convert | No cross-map fold |
| 10 | An unrelated permission entry | Untouched | Preserved exactly | Scenario: Conversion is byte-identical on a second start | The conversion is keyed, not wholesale |
| 11 | Conversion run twice in one process | Re-run | Byte-identical | Scenario: Conversion is byte-identical on a second start | |
| 12 | Crash before write | Interrupted | Original file intact; next start converts | Scenario: An interrupted conversion leaves the original intact | Atomic replace |
| 13 | Two processes, unconverted input | Concurrency | Byte-identical to a single-process run | Scenario: Two concurrent starts against one install converge | Deterministic output from identical input |
| 14 | Two processes, one already converted | Concurrency, mixed | The converted content survives | Scenario: Two concurrent starts against one install converge | Re-running on output is the no-op case |
| 15 | Conversion ordered **after** the coverage repair | **Ordering regression** | switch: deny and discovery: deny on every agent, **startup succeeds** | Scenario: The discovery capability is never denied by the coverage repair | **This row is the test that must fail before the fix** |

#### Dataset: Unused-discovery counting

| # | Input | Boundary Type | Expected counter delta | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | By-description discovery, unused, 6 turns pass | Above horizon | +1 | Scenario: An unused discovery is counted once | |
| 2 | Same, 7th turn passes | Repeat | +0 | Scenario: An unused discovery is counted once | Delete on fire |
| 3 | By-description discovery, used on turn 2, 8 turns pass | Used | +0 | Scenario: A used discovery is never counted | |
| 4 | By-description discovery, unused, exactly 5 turns pass | Exact horizon | +0 | Scenario: An unused discovery is counted once | "More than 5" is strict |
| 5 | By-name load, unused, 8 turns pass | Wrong path | +0 | Scenario: A by-name load never creates a pending discovery | Avoids a false-positive floor |
| 6 | Agent A discovers, agent B uses within the horizon | Cross-agent | +1 | Scenario: Another agent's use does not clear the first agent's | Reads 0 under a conversation-only key |
| 7 | Discovery on turn 1, conversation ends turn 3 | Short conversation | +1 | Scenario: A conversation shorter than the horizon still counts | Sweep before delete |
| 8 | Ambiguous discovery makes 3 usable, agent uses 1 | Multi-promote | +2 | Scenario: An unused discovery is counted once | **The counter has a non-zero floor under correct operation** — see the ambiguity table |
| 9 | The same tool re-surfaced by a second search while already usable | Duplicate | +0, and no horizon refresh | Scenario: A used discovery is never counted | **Resolved ambiguity** — records only the first promotion |
| 10 | Conversation with no discoveries at all | Zero | +0 | Scenario: Both counter series are reachable | The series must still be exposed |
| 11 | Agent A discovers on its turn 1, hands off to B for 8 of B's turns, then A returns and takes 2 more turns | **Horizon basis while inactive** | +0 at the moment A returns and +0 after A's 2 turns; A has taken 3 of its own turns, below the horizon | Scenario: An unused discovery is counted once after the horizon | **The horizon is agent-local, not global.** A sweep runs on the current turn's own agent-and-conversation key, so B's 8 turns neither advance nor fire A's horizon |
| 12 | Same as row 11, but A then takes 4 further turns without using the tool | Horizon reached after return | +1, on A's 6th own turn | Scenario: An unused discovery is counted once after the horizon | The clock resumes rather than restarts — it was never running while B held the conversation |
| 13 | Same as row 11, but the conversation ends while B holds it | Terminal case while inactive | +1, at conversation close | Scenario: A conversation shorter than the horizon still counts | The close sweep is what covers an agent that never becomes active again |

### Regression Test Requirements

This feature **modifies existing functionality** in five places. Every behaviour below must be preserved and is covered by a named test above.

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|---|---|---|---|
| A dominant search result auto-loads exactly one tool | `pkg/tools/load_tool_test.go` | **Yes** — `TestAmbiguity_DominantHitPromotesAlone` | Must be indistinguishable from today |
| The by-name load path | `pkg/tools/load_tool_test.go`, `pkg/tools/load_tool_f4_test.go` | **Yes** — `TestAmbiguity_ByNameFlowUnchanged` | D2 must not touch it |
| Discovery calls hidden from the chat thread | `src/lib/toolVisibility.test.ts` | **Yes** — tests 2–5 | Existing tests keep passing against a dead arm; the new ones assert positively |
| The handoff exclusion from delegated registries | `pkg/tools/sprint_h_registry_test.go`, `pkg/agent/sprint_h_subturn_test.go`, `pkg/agent/sprint_h_scenario_test.go` | **Yes** — `TestExcludedSwitchAgent_AbsentFromChildRegistry` | Must assert the **new** name is absent |
| The same exclusion, at grandchild depth | `pkg/tools/delegate_grandchild_test.go` | **Yes** — `TestDelegateGrandchild_SwitchAgentAbsent` | **The existing test is vacuous today**: it never registers the tool it asserts is absent, so its assertion has never tested anything, and the file contains no reference a compiler-guided rename would visit |
| Transcript replay emitting the active-agent signal | none | **Yes** — `TestReplay_HandoffEntryStillEmitsFrame` | Replay needs no edit; the test pins that |
| Startup permission-coverage validation | `pkg/coreagent/constructor_seed_test.go`, the one-for-one catalog invariant test | **Yes** — `TestBoot_ValidationPassesAfterConversion` | The catalog shrinks by one |
| The parent/child usable-set isolation invariant | `pkg/agent/tool_manifest_adr057_test.go` | **Update, not replace** | Its assertions stay valid — two conversations still yield two buckets — but it must pass agent identifiers |
| Provider system-content assembly for non-marker providers | `TestSerializeMessages_StripsSystemParts` | **Yes** — `TestProviderWithoutMarkers_AssemblesCorrectly` | D5 must not change what they receive |
| The four tools that leave the always-listed set but are **not** deleted — `bash`, `navigate`, `create_task`, `update_task` | none pins their tier | **Yes** — `TestVisibility_TierArithmetic` covers all four by name | They remain fully registered, fully permission-governed and one preview line away. This is the intended behaviour change, and it is a *regression* row rather than a feature row because a reader checking "did anything break" needs to see that these four still work, only less visibly. Their movement is also where the CRITICAL finding hid |

#### Regression Dataset: behaviour that must not change

| # | Input | Previous Behaviour | Must Still Produce | Traces to |
|---|---|---|---|---|
| 1 | A query with one dominant permitted result | One tool made usable, one schema returned | Identical | Regression: single-promotion path |
| 2 | A by-name request for three tools | Three made usable | Identical | Regression: by-name path |
| 3 | A discovery call, default display settings | Hidden in thread and panel | Identical, under the new name | Regression: chat visibility |
| 4 | A discovery call that errored | Shown | Identical, under the new name | Regression: chat visibility |
| 5 | A delegated sub-task's registry | No handoff capability present | No switching capability present | Regression: delegation exclusion |
| 6 | A switch to a named agent | Signal carries the agent identifier | Identical | Regression: active-agent signal |
| 7 | A return to default | Signal carries no identifier | Identical | Regression: active-agent signal |
| 8 | Replaying a stored handoff entry | Signal emitted | Identical | Regression: replay |
| 9 | Replaying a stored return-to-default entry | **No signal emitted** | **Identical — still none** | Regression: replay. A live defect this work found and deliberately did not fix |
| 10 | A return to default | No 10-second ceiling | **Now bounded by 10 seconds** | Regression: **deliberate delta**, judged harmless — that branch performs no I/O |
| 11 | A switch with the note omitted | Succeeded despite a declared requirement | Succeeds; the requirement is now honestly declared optional | Regression: **deliberate delta** in the declared shape only |
| 12 | A non-marker provider's system content | Full concatenated text | Identical | Regression: provider assembly |

---

## Functional Requirements

### Discovery result filtering (User Story 1)

- **FR-001**: The system MUST apply the caller's tool permissions to the discovery answer's list of matched names and descriptions, not only to the tool whose schema it makes usable.
- **FR-002**: The system MUST apply that filter **after** ranking. It MUST NOT filter the searchable index, because the index is shared across calling agents, its cached form carries no signal that changes when a permission changes, and removing entries alters every surviving entry's score.
- **FR-003**: The relative order and the scores of the results a given agent is permitted to see MUST be identical to their order and scores in the unfiltered ranking.
- **FR-004**: When the permitted result set is empty, the system MUST return the exact text `No tools found matching the query.` and MUST NOT return any listing.
- **FR-005**: When no permission resolver is available, the system MUST return that same text and MUST disclose no tool name or description.
- **FR-006**: The system MUST bound the number of permission checks performed per query to 50. A bound that is reached MUST truncate the permitted list, never admit an unpermitted entry.
- **FR-007**: The system MUST NOT retain an exported search method that returns unfiltered names and descriptions. It MUST be deleted rather than documented as unsafe.
- **FR-008**: The "did you mean" name suggestion MUST be drawn from the caller's permitted set, not from its full registered set.
- **FR-009**: The system MUST NOT return the full ranked listing on the branch reached when every ranked result is denied. That branch MUST be removed.

### Rename (User Story 2)

- **FR-010**: The tool-discovery capability MUST be renamed, with identical parameters, identical mechanics, and identical behaviour on both its by-name and by-description paths.
- **FR-011**: Discovery calls MUST remain hidden by default from both the chat thread and the activity panel, and MUST remain visible when verbose output is enabled or when the call errored.
- **FR-012**: The regression guard that pins this capability's canonical name MUST assert against a production symbol, not against a local variable. It MUST fail when the production name and the asserted name diverge.
- **FR-013**: The double-registration guard MUST derive the capability's name from the infrastructure name set rather than from a repeated literal.
- **FR-014**: The published per-tool exposure-level description MUST be updated through the contract-first process, with generated artifacts committed in the same change, and MUST NOT name any retired capability.
- **FR-015**: The display-label mapping MUST retain the legacy key as an alias so conversations recorded before the rename still render readably, and MUST add the new key.
- **FR-016**: The user-acceptance probe scripts that instruct a live agent by tool name MUST be updated, because a stale name makes them report failure for a reason unrelated to what they test.
- **FR-017**: The embedded single-page-application bundle MUST be re-synced from a fresh build in the same change as any visibility source edit, because the binary embeds from the sync directory rather than the build output.

### Closeness comparison (User Story 3)

*(FR-018 and FR-019 belong to this story despite their numbers preceding FR-020; they were added after the first review and are placed in ascending order rather than renumbering the block.)*

- **FR-018**: This story MUST NOT introduce an install-level revert control of its own, and its absence MUST be recorded as a reasoned decision rather than left as an omission. The three grounds are stated in User Story 3: the failure mode is a wasted schema rather than an unreachable capability; every promoted candidate has already passed the caller's permission check and execution still runs the full policy gate, so what widens is schema reachability and never capability reachability; and the documented first response to the signal that would trigger a revert is a constant-tightening change, which is the same size of change as a revert would be. If the operator rejects this reasoning at ratification, the remedy is a second boolean alongside FR-042's control that forces single-promotion — specified nowhere else in this document, deliberately.
- **FR-019**: The three closeness constants MUST be declared together, adjacent and unexported, in one place, carrying a doc comment that records the operator response to a rising unused-discovery counter and the order it is applied in: the cross-kind ratio first, then the score-band ratio, then the cap. The reason for that order MUST be recorded with it — the cross-kind rule is the speculative one, and lowering the cap last because it discards a candidate that qualified rather than correcting the test that qualified it. This is what makes FR-018's "the remedy is a code change" an actionable statement rather than an excuse.
- **FR-020**: On the by-description path, a candidate at rank two or later MUST be made usable alongside the top result when its score is at least 0.80 times the top permitted result's score.
- **FR-021**: A candidate at rank two or later MUST also be made usable when its score is at least 0.50 times the top permitted result's score **and** its kind differs from the top result's kind. **"Kind" is the existing `Tool.Category()` classification** (the `ToolCategory` enum in `pkg/tools/base.go`): two candidates are of a different kind exactly when their `Category()` values differ. No new grouping may be invented for this rule.
- **FR-022**: The requirement in FR-021 MUST NOT admit a candidate whose resolved permission requires user confirmation, nor one on the destructive-and-install-wide list. FR-020 MUST NOT carry that exclusion. **The destructive-and-install-wide list MUST NOT be derived from `Tool.Category()`** — see FR-025. FR-021 and FR-022 therefore use two different mechanisms on purpose, and conflating them breaks both.
- **FR-023**: The system MUST make at most 3 tools usable per by-description call, choosing the highest-ranked qualifiers when more qualify.
- **FR-024**: When fewer than two permitted results exist, the system MUST perform no comparison and MUST make the single result, if any, usable alone.
- **FR-025**: The system MUST maintain an explicit list of tools that destroy or overwrite state no other tool can reconstruct, or that alter install-wide settings, seeded at exactly 13 names. It MUST NOT derive that list from a tool's kind, nor from a name prefix. **This is deliberately a different mechanism from FR-021's, and the reason is checkable**: `Tool.Category()` is a closed *domain* enum with no destructive value, and destructive tools share a category with their read-only siblings — `delete_agent` with `list_agents`, `set_config` with `get_config`, `disable_channel` with `list_channels`, `remove_mcp_server` with `list_mcp_servers`. A category-based exclusion would drop every read tool in four whole domains out of the speculative band while still missing a destructive tool whose siblings are benign. `Tool.Category()` is the right axis for "are these two candidates different kinds of thing" (FR-021) and the wrong axis for "is this candidate dangerous" (FR-022/FR-025).
- **FR-026**: The build MUST fail when that list's contents or count change without a recorded decision, when any listed name is absent from the catalog, when any listed name resolves above the search-only level, or when a catalog name matching a destructive name shape is neither listed nor explicitly exempted with a written reason.
- **FR-027**: The thresholds and the cap MUST NOT be exposed as operator configuration.
- **FR-028**: The by-name path MUST be unchanged.
- **FR-029**: The discovery answer's structure MUST change only in cardinality. Its human-readable preamble SHOULD name every tool made usable and say why more than one was.

### Exposure levels (User Story 4)

- **FR-030**: The system MUST express the search-only distinction as a second classification axis, independent of the existing exposure-level classification. It MUST NOT express it as a further value of that classification.
- **FR-031**: Every search-only tool MUST remain present in the searchable index, fully permission-governed, and addressable by exact name and by description. *(This is a **static membership** property of the index: what the index contains, asserted without running a search.)*
- **FR-031a**: A search-only tool found by a by-description search MUST be **made usable** by that search, and its full callable schema MUST be present in the next turn's callable set. *(This is the **dynamic promotion** behaviour: what a search does, asserted by running one end to end. It is split from FR-031 because a test that proves a tool is in the index proves nothing about whether searching for it promotes it — the two halves were previously bundled under one requirement citing one test that only covered the first.)*
- **FR-032**: The always-listed set MUST contain exactly 17 names, the previewed set exactly 8, the search-only set exactly 63, and the infrastructure set exactly 1, totalling the 89-name catalog. **The membership of each set is ADR-071 §4.1's corresponding literal name list, transcribed rather than re-derived** — see "Tier membership: one source of truth". Counts alone MUST NOT be treated as verification of this requirement: the always-listed set's real diff from today is 6 removals and 5 additions, and an incorrect 3-removal/2-addition diff produces the same total of 17 with entirely different membership.
- **FR-033**: The per-turn listing MUST omit search-only tools and MUST render as exactly 22 lines for an agent permitted everything with nothing yet usable. The count decomposes as `2 + 2C + N` where `C` is the number of distinct categories among the previewed tools and `N` their number — **2 header lines, 6 categories, 8 entries**, verified in this session against `BuildCompressedManifest`'s real emission shape (see Exposure-level dataset row 6 for the method and the per-tool category derivation). Two consequences an implementer must not trip over: **the header MUST stay at exactly 2 lines** — FR-044's requirement that the header advertise the existence of unlisted tools MUST be satisfied by rewording the existing second line, never by adding a third, or this requirement's own number is wrong the day it ships; and a category regrouping that changes `C` changes the count without changing a single tool's exposure level, so the drift test MUST assert the rendered line count, not the tool count.
- **FR-034**: The build MUST fail when a catalog name has no recorded exposure level, and when the previewed set's contents or count change without a recorded decision.
- **FR-035**: The record of which tools an agent has made usable MUST be keyed by agent **and** conversation, not by conversation alone.
- **FR-036**: Conversation close MUST discard every such record for that conversation across all its agents, and MUST discard the pending-discovery records under the same scan.
- **FR-037**: A **static catalog** tool made usable MUST remain usable for the remainder of the conversation. The system MUST NOT build any expiry, eviction, unload or decay path **against the usable-set record** (`loadedTools`) — no TTL, no turn cap, no reclamation. The only removal is the conversation-close sweep required by FR-036. **This prohibition is scoped to that one record and to the 89 static catalog tools it governs. It does not reach, and this work does not change, the pre-existing MCP discovery lifetime** (`cfg.Tools.MCP.Discovery.TTL`, default 5, applied by `PromoteTools` and decremented by `TickTTL`) — see FR-037a and the two-mechanism note under Pinned Identifiers. The two are structurally independent: both TTL mutators are guarded by `!entry.IsCore` and are literal no-ops on every static tool, while `GetAll` admits a core entry unconditionally, so a static tool cannot decay however the TTL is configured. Source: ADR-071 §1.1.1 and §3.3.
- **FR-037a**: The pre-existing MCP discovery lifetime MUST be left **unchanged** by this work. An externally-provided tool made usable by a discovery call MUST continue to stop being usable once `cfg.Tools.MCP.Discovery.TTL` turns have been ticked, exactly as it does today. Building or removing decay for externally-provided tools is out of scope here; FR-037's prohibition explicitly does not apply to them, and no requirement in this specification may be read as forbidding this pre-existing behaviour. The two mechanisms MUST be pinned by a **pair** of tests in one file, so the split is documented where an implementer will meet it: `TestStaticPromotion_SurvivesBeyondDiscoveryLifetime` (FR-037) and `TestExternalPromotion_DecaysWithDiscoveryLifetime` (FR-037a).
- **FR-038**: The system MUST increment an operator-visible counter once per by-description discovery that goes unused for more than 5 turns, or until conversation close, whichever comes first, and MUST NOT increment again for that same discovery. **The 5 here is a new, independent literal and MUST NOT be derived from, coupled to, or merged with `cfg.Tools.MCP.Discovery.TTL`, whose default is also 5.** The equal value is a coincidence of two separate choices, not a shared constant: the MCP TTL decides *when an externally-provided tool stops being callable*, while this horizon decides only *when an unused static discovery is counted* and withdraws nothing from anyone. They are also not operator-equivalent — the MCP TTL is already operator-configurable and an operator who has tuned it away from 5 **must not** see this horizon move with it. This requirement introduces no new operator-facing dial of its own; the horizon is a fixed literal. An implementer who notices the duplicate 5 and "fixes" it by pointing this horizon at the MCP TTL config key has introduced the exact conflation ADR-071 §1.1.1 records as its own worst defect — the constant MUST carry a doc comment saying so.
- **FR-038a**: The 5-turn horizon MUST be counted in the **discovering agent's own turns**, not the conversation's global turn count. A per-turn sweep examines only the current turn's own agent-and-conversation key (it can compute no other), so turns held by a different agent neither advance nor fire a pending discovery. An agent handed off away from and never returned to is covered by the conversation-close sweep instead.
- **FR-039**: Pending-discovery records MUST use the same agent-and-conversation key as the usable-set records, MUST be written only on the by-description path, MUST be cleared when the tool is invoked, and MUST be counted before they are deleted at conversation close. The pending-discovery record and the usable-set record MAY remain two structures, matching the source ADR's own naming of them as separate maps — but because they share a key, a creation moment and a sweep, **both MUST be swept by one function taking the conversation identifier**, never by two independent scans that could diverge. A test MUST assert that closing a conversation with pending records under two different agents leaves both structures empty. **The two-structure shape is a fidelity-to-source-document choice, not a technical constraint, and this requirement does not bind a future refactor to it.** A single map keyed by agent-and-conversation whose per-tool value carries the usable flag, the promotion turn and the counted flag would satisfy every behavioural requirement here with one fewer moving part and no two-structure-sweep discipline to maintain; it is not chosen now only because the source ADR names them as separate maps and its own required test asserts on the second by name. The load-bearing part of this requirement is the **one-sweep-function rule**, which closes the desync risk under either shape — anyone collapsing the two maps later must keep the single sweep and the both-empty assertion, and may drop the two-structure language without an amendment.
- **FR-040**: The system MUST increment an operator-visible counter once per by-description call whose permitted result set is empty.
- **FR-041**: Both counters MUST be exposed by name on the existing metrics endpoint as Prometheus-format counters, reached through the existing recorder indirection with no new wiring at the gateway's construction site.
- **FR-042**: The system MUST provide an install-level control that restores the previous behaviour by listing every findable tool. It MUST be exposed as **`PreviewAllLazy bool` on the manifest configuration block** (`ManifestConfig`, `pkg/config/config.go`), serialised as `preview_all_lazy`, **defaulting to `false`** so the three-level split is active on a fresh install and on every upgrade. It MUST be read **inside `ToolManifestVisibility`**, which returns the previewed level for every lazy tool when the flag is set — so both manifest builders inherit the revert with no second branch of their own. It MUST NOT revert the permission filtering of FR-001 nor the per-agent keying of FR-035. It is a stored-configuration dial, not an environment variable and not a settings-screen control, and it MUST NOT be reachable through the existing `Compressed` flag, which disables the manifest optimisation entirely and is a far larger change with its own token cost.
- **FR-043**: That control MUST be time-boxed. A tracked item for its removal MUST be filed before this work ships, and its removal trigger MUST be recorded in the flag's own doc comment: once the two counters have produced enough data to validate the split or motivate a wider previewed set, the flag is deleted in the same change that acts on that data.
- **FR-044**: The per-turn listing's header SHOULD state that more tools exist than are listed and that they can be found by describing what is needed.
- **FR-045**: No cross-boundary field expressing the search-only distinction MAY be added, because nothing consumes the existing exposure field.

### Agent switching (User Story 5)

- **FR-050**: One capability MUST replace both existing agent-switching capabilities, taking the destination as a parameter. Both existing capabilities MUST be deleted, with no alias retained.
- **FR-051**: The destination MUST be the only required parameter. The explanatory note MUST be declared optional, and its description MUST state both the forward-looking obligation (for a named destination) and the backward-looking obligation (for the reserved destination). **The "strongly recommended" encouragement applies to the named-target branch only, not to the return-to-default branch**, and the description MUST say so rather than leave the scope to the reader. The asymmetry is deliberate and matches what each branch does: a named destination hands the conversation to an agent that was not in it, for whom the note is the only context beyond the transcript, whereas the reserved destination returns it to an agent that was already there and needs no brief — which is also why FR-054 runs the budgeted history transfer on the first branch and not the second. On both branches the note remains optional and its omission MUST succeed (Acceptance Scenario 9, dataset row 11). ADR-071 §5.1.1 specifies the description string normatively — *"Optional. When switching to a named agent: context or instructions for that agent about this conversation. When returning to the default agent (`target: "default"`): a summary of what was accomplished. Strongly recommended when handing off to another agent — it is the only context the incoming agent gets beyond the transcript."* — and that string, whose final sentence names the handing-off branch explicitly, is what MUST ship.
- **FR-052**: The operation MUST be bounded by a 10-second ceiling on both destination branches. **An operation that exceeds the ceiling MUST be treated as a failed switch for every purpose FR-061 governs**: it MUST produce no active-agent signal, MUST surface an error to the caller naming the timeout, and MUST leave the conversation's active agent unchanged. This is stated rather than inferred because every other failure mode in this story — unknown identifier, worker target, unresolvable active agent — carries its own scenario and its own stated outcome, and a timeout is the one path where "it failed, so FR-061 applies" was left to the reader.
- **FR-053**: The rejection of a delegation-only worker MUST run on both branches.
- **FR-054**: The budgeted history transfer MUST run for a named destination and MUST NOT run for the reserved destination.
- **FR-055**: The recorded-entry identity stamping MUST remain asymmetric between the two branches, and the recorded content prefix for a named destination MUST remain exactly `Handoff: `.
- **FR-055a**: The audit asymmetry FR-055 preserves MUST be **recorded as a known, pre-existing, deliberately-unchanged property**, and MUST be filed as its own tracked item in the same way FR-066 and FR-067 file the other two known defects on this surface. No behavioural change is required or permitted here. The reason to name it now: only the named-destination branch is guaranteed a recorded conversation entry, so a return to default leaves a thinner trail of who moved the conversation and why. While that was the behaviour of *two* tools it read as an artifact of two separately-evolved implementations; once one capability owns both branches it reads to a future reader as a defect in that capability, and an unrecorded known asymmetry gets re-filed, re-investigated, or silently "fixed" mid-rename — which is the scope creep this story's own prohibitions forbid. Filing it makes it a visible candidate for symmetric audit coverage as its own change, on its own evidence. Given User Story 5's framing that the merge is security-relevant, the repudiation-side cost of the asymmetry is the part worth being explicit about.
- **FR-056**: The reserved destination word is the **literal string `"default"`**, matching today's return-to-default semantics. It MUST resolve to the configured default agent unconditionally, regardless of whether any agent bears that identifier — by rule, never by lookup order. The comparison MUST be **exact**: a differently-cased spelling is not the sentinel and MUST fall through to ordinary identifier resolution.
- **FR-057**: When an agent's identifier is the reserved word at startup, the system MUST start successfully and MUST log a warning naming that agent. It MUST NOT abort. The asymmetry with the tool-policy coverage validator, which does abort, is deliberate: a coverage gap is a security invariant, a name collision is a usability defect on a rare install whose only consequence is one shadowed addressing path.
- **FR-058**: The system SHOULD reject an agent identifier or display name equal to the reserved word `"default"`, **case-insensitively**, at both creation and update, returning a client error and writing nothing. *(Gated on ratification item 1 — see Ambiguity Warnings.)* This is a **creation-time rejection on the agent-CRUD path**, distinct in path and in outcome from FR-057's startup warning, and it MUST have its own tests rather than borrowing FR-057's. The two case rules differ on purpose: a rejection that missed a casing variant would admit the collision it exists to prevent, while a resolution that matched loosely would silently shadow a legitimately-named agent. If ratification declines this requirement, FR-056 and FR-057 alone are a complete posture and no test for FR-058 is written.
- **FR-059**: The exclusion that keeps this capability out of a delegated sub-task's registry MUST be updated to the new name in the same change as the rename, and the operator-facing debug log naming the excluded tool MUST derive that name rather than repeat it.
- **FR-060**: The active-agent signal MUST be produced from a single branch keyed on the new capability's name plus the resolved post-switch active agent, never on a tool name alone. It MUST carry no agent identifier when the resolved agent equals the configured default, and MUST carry the identifier otherwise.
- **FR-061**: A failed switch MUST produce no active-agent signal — including a switch that failed by exceeding FR-052's 10-second ceiling, on either destination branch. A successful switch whose active agent cannot be resolved MUST log a warning rather than silently produce nothing.
- **FR-062**: Transcript replay MUST be left unmodified.
- **FR-063**: The cross-boundary schema descriptions naming the retired capabilities MUST be updated through the contract-first process with generated artifacts committed in the same change.
- **FR-064**: The live reference documentation naming the retired capabilities MUST be updated in the same commit as the rename. Dated point-in-time records MUST NOT be rewritten.
- **FR-065**: Prior decision records naming the retired capabilities MUST receive a dated front-matter naming note. Their bodies MUST NOT be edited.
- **FR-066**: The known defect by which a delegated sub-task is told a structurally-absent tool loaded successfully MUST NOT be fixed here, MUST have its describing comment updated to the new names, and MUST be filed as its own tracked item.
- **FR-067**: The existing grandchild-exclusion test's vacuous assertion MUST be repaired by registering the real capability before the derivation, and MUST be filed as its own tracked item.

### Upgrade conversion (User Story 6)

- **FR-070**: Stored permission entries under the three retired names MUST be converted to the two new names before any coverage repair writes a permission entry.
- **FR-071**: Where two retired entries map to one new name, the result MUST be the strictest of them, ordered deny above ask above allow.
- **FR-072**: An entry already stored under a destination name MUST participate in that comparison, so the operation is monotone in the strict direction and safe to re-run against its own output.
- **FR-073**: Every retired entry MUST be deleted in the same operation.
- **FR-074**: The conversion MUST cover both install-wide and per-agent permission maps, folding each independently.
- **FR-075**: The conversion MUST be idempotent by construction, keyed on the presence of a retired name, with no version marker.
- **FR-076**: After conversion, startup validation MUST find an explicit entry for every agent-and-tool pair and MUST NOT abort.
- **FR-077**: The conversion MUST be documented as one-way. Rolling back to a build predating this work leaves the new names unrecognised and the retired ones absent, which that build repairs by writing denials. The release notes MUST state that a rollback requires restoring a pre-conversion configuration backup.
- **FR-078**: The system SHOULD write a timestamped copy of the pre-conversion configuration before converting, so a rollback has something to restore.

### Cache boundary (User Story 7, conditional)

- **FR-080**: The manifest builder MUST be split along the stable-versus-per-turn seam, with the stable catalog independent of conversation state and the record of which tools are usable kept separate.
- **FR-081**: The stable catalog block MUST be placed immediately after the existing stable block and before the first per-turn-varying block, and MUST carry an ephemeral cache marker.
- **FR-082**: The record of which tools are usable MUST remain outside the cached region.
- **FR-083**: The exposure-level filter MUST remain inside the builder. The builder MUST NOT expect a pre-filtered set from its caller.
- **FR-084**: An offline structural check MUST assert the block's position and its cache marker, and MUST run on every change thereafter.
- **FR-085**: This story MUST NOT merge unless a measured rise in cache-read volume on a turn with no newly usable tools is at least 0.8 times the block's own estimated size. **Both quantities are token counts.** The block's size is `B = estimateMessageTokens(BuildStaticToolCatalog(...))` — the same estimator the agent package already uses for context-budget accounting — and the rise is `ΔC`, the increase in `providers.Usage.CacheReadTokens` on the same turn measured before and after the change. Neither is a byte count and neither is a character count; the two bases can differ by a large factor for identical text, and a floor expressed against the wrong one is not a gate. **PASS is `ΔC ≥ 0.8 × B` on at least one no-tool-load turn of a multi-turn session against the one cache-honouring provider; FAIL is `ΔC < 0.8 × B`.** There is deliberately no upper bound: a `ΔC` exceeding `B` means the cache boundary covers text beyond the moved block, which is a better result. The 0.8 floor exists to leave room for disagreement between the local estimator and the provider's own tokenizer while still separating a working boundary from the characteristic failure, whose signature is `ΔC = 0`. The measurement is manual, operator-owned, and both numbers are recorded in the merge request body as the merge artifact. It MUST NOT be added to continuous integration.
- **FR-085a**: The accepted posture for this story after merge MUST be stated rather than left open: **the benefit is measured once, at merge, and is not rechecked automatically thereafter.** The offline structural checks (`TestContextBlocks_CatalogAtIndexOne` and its siblings) guard only against a *code* change moving the block or dropping its marker; they cannot detect a provider-side change — a different tokenizer, a raised minimum cacheable prefix, a shortened cache lifetime — silently reducing the benefit to zero while every gate stays green. That residual exposure is accepted, and it is accepted explicitly rather than by omission. The system **SHOULD** additionally sample the figure it already reads for the one-off measurement (`providers.Usage.CacheReadTokens`) into the existing metrics or logging path, giving passive ongoing visibility at effectively no cost and with **no new continuous-integration requirement** — FR-085's prohibition on adding the measurement to CI stands unchanged. If that sampling is not implemented, "measured once at merge, never rechecked" is the recorded posture and a future operator investigating a cache regression has the one-off numbers in the merge request body as their only baseline.
- **FR-086**: This story MUST sequence last, so that a failed measurement blocks nothing else.

### Cross-cutting

- **FR-090**: Every static builtin tool MUST resolve from an explicit, literal, wildcard-free permission entry for every agent, both before and after this change. No default fallback MAY be introduced.
- **FR-091**: Every change crossing the gateway boundary MUST follow the contract-first process, with generated artifacts committed atomically with the schema change.
- **FR-092**: Each rename MUST end with a search across the code, tests, contracts and documentation trees, with every hit checked against this specification's site lists — and the lists corrected, not the search narrowed.
- **FR-093**: Every function this work depends on MUST be read end to end before the requirement relying on it is signed off, not only at the lines supporting the claim being made.

---

## Success Criteria

- **SC-001**: The rendered per-turn listing for an agent permitted every tool with nothing yet usable is exactly **22 lines**, down from approximately **101**.
- **SC-002**: The static catalog contains exactly **89** names — the 17 always-listed, the 8 previewed, the 63 search-only and the 1 infrastructure name, which together partition it — and startup validation finds an explicit permission entry for every agent-and-tool pair and does not abort on a freshly seeded install.
- **SC-002a**: The always-listed set's **membership** equals ADR-071 §4.1's Tier 1 list name for name, in both directions — `0` names present that §4.1 omits, `0` names absent that §4.1 lists. Measured by set comparison, not by count: relative to today's 18-name set that is exactly **6** removals (`bash`, `navigate`, `create_task`, `update_task`, `hand_off`, `return_to_default`) and exactly **5** additions (`switch_agent`, `list_mounts`, `send_file`, `message_parent`, `recall_conversation`). A count of 17 alone satisfies at least two different and incompatible sets and is therefore not evidence for this criterion.
- **SC-003**: Across a suite of at least 20 queries issued by an agent with at least 5 denied tools, **zero** denied tool names and **zero** denied tool descriptions appear in any answer.
- **SC-004**: For the same suite, the relative order of the permitted results is **identical** to their relative order in the unfiltered ranking, for **every** query.
- **SC-005**: A request to the metrics endpoint returns a body containing both counter series names, each declared as a counter.
- **SC-006**: A by-description discovery that goes unused increments the unused-discovery counter by exactly **1** after 6 turns and by **0** on the 7th.
- **SC-007**: An upgrade from a stored configuration permitting both retired switching capabilities on every agent produces the new capability permitted on **every** agent, with **zero** denials backfilled for either new name.
- **SC-008**: Running the conversion twice against the same stored configuration produces **byte-identical** output.
- **SC-009**: A successful switch to a named agent emits exactly **1** active-agent signal carrying that identifier; a successful return to default emits exactly **1** carrying none; a failed switch emits **0**.
- **SC-010**: After one agent makes a search-only tool usable and the conversation switches to a second agent permitted the same tool, that tool appears in **0** of the second agent's callable sets.
- **SC-011**: After a conversation with usable tools under two different agents closes, **0** records remain for that conversation in either the usable-set store or the pending-discovery store.
- **SC-012**: With default display settings, discovery calls appear in **0** chat threads and **0** activity panel rows; with verbose output enabled, they appear in both.
- **SC-013**: A search across the code, test, contract and documentation trees for the three retired names returns **0** hits outside dated point-in-time records and the front-matter naming notes.
- **SC-014**: The delegated sub-task registry contains the switching capability **0** times, asserted against the new name, at both child and grandchild depth, with the parent registry demonstrably containing it beforehand.
- **SC-015**: The measured rise in cache-read **tokens** on a turn with no newly usable tools is at least **0.8 ×** the moved block's estimated **token** count — `ΔC ≥ 0.8 × B`, where `B = estimateMessageTokens(BuildStaticToolCatalog(...))` and `ΔC` is the increase in `providers.Usage.CacheReadTokens` for the same turn before and after the change. Both are tokens, never bytes and never characters. No upper bound. Otherwise the cache story does not merge and the rest ships without it.
- **SC-016**: An assembled request uses exactly **2** of the provider's **4** cache breakpoints.
- **SC-017**: Adding a tool to the catalog without recording its exposure level fails the build; adding one with a destructive name shape without adjudicating it fails the build.
- **SC-018**: All project quality gates pass: `gofmt -l . | wc -l` is 0; `golangci-lint run --build-tags=goolm,stdjson` exits 0; `CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 ./...` exits 0; `govulncheck ./...` reports 0 vulnerabilities; `npm run typecheck` exits 0; `npx vitest run` exits 0; `make verify-contracts` exits 0.
- **SC-019**: A binary built through the embed pipeline serves the corrected visibility behaviour, verified by searching the embedded bundle for a string unique to the change.
- **SC-020**: Startup on an install where an agent's identifier is the reserved word succeeds, logs exactly **1** warning naming that agent, and resolves the reserved destination to the configured default agent.

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|---|---|---|---|
| FR-001 | US-1 | Denied tool ranked mid-list is absent; Discovery answers are a subset | `TestSearch_DeniedResultAbsentFromMatchList` |
| FR-002 | US-1 | One agent's denials do not alter another agent's ranking | `TestSearch_RankingInvariance` |
| FR-003 | US-1 | One agent's denials do not alter another agent's ranking; Denied tool ranked mid-list is absent | `TestSearch_RankingInvariance`, `TestSearch_DeniedResultAbsentFromMatchList` |
| FR-004 | US-1 | Every ranked result is denied | `TestSearch_AllDeniedReturnsSilentNoMatch` |
| FR-005 | US-1 | Discovery with no permission resolver discloses nothing | `TestSearch_NilResolverDisclosesNothing` |
| FR-006 | US-1 | The permission walk is bounded on a large tool set | `TestSearch_PermissionScanCapped` |
| FR-007 | US-1 | Denied tool ranked mid-list is absent | `TestRegistrySearchBM25_Removed` |
| FR-008 | US-1 | A near-miss probe does not suggest a denied tool's real name | `TestFuzzySuggestion_ReadsPermissionFilteredSet` |
| FR-009 | US-1 | Every ranked result is denied | `TestSearch_AllDeniedReturnsSilentNoMatch` |
| FR-010 | US-2 | Capability answers to its new name (by-name); (by-description) | `TestToolsTool_NameIsRenamed`, `TestDiscoveryTool_ByNameAndByDescriptionUnchanged` |
| FR-011 | US-2 | Discovery calls stay hidden by default; Verbose output reveals them; A failed call is still shown | `TestToolVisibility_DiscoveryHiddenInThreadByDefault`, `TestToolVisibility_DiscoveryHiddenInPanelByDefault`, `TestToolVisibility_DiscoveryVisibleWhenVerbose`, `TestToolVisibility_DiscoveryVisibleOnError` |
| FR-012 | US-2 | The canonical-name guard can fail | `TestCanonicalDiscoveryToolName_PinsProductionSymbol` |
| FR-013 | US-2 | The registration guard prevents a second registration | `TestRegistrationGuard_DerivesFromInfraNames`, `TestRegistrationGuard_SurvivesLiveConfigToggle` |
| FR-014 | US-2 | The published exposure-level description names only live capabilities | `TestContractDescription_NamesOnlyLiveTools` |
| FR-015 | US-2 | A conversation recorded before the rename still renders readably | `TestHumanizeToolName_LegacyAliasRetained` |
| FR-016 | US-2 | Capability answers to its new name (by-name) | `sandbox-uat/probe-t.sh` prompt strings updated |
| FR-017 | US-2 | Discovery calls stay hidden by default | Embedded SPA re-sync verification |
| FR-018 | US-3 | (User Story 3's revert-asymmetry rationale; no behaviour to test) | **No test — this is a recorded non-decision.** Verified at ratification, alongside FR-043's tracked item. Its counterpart FR-042 *is* tested; the absence here is the point |
| FR-019 | US-3 | (Constant placement and documented tightening order) | `TestAmbiguity_BoundaryTable` (the constants are the values it exercises); doc-comment content verified in review, as with FR-043 |
| FR-020 | US-3 | Two near-tied results are both made usable; boundaries | `TestAmbiguity_ScoreBandPromotesRunnerUp`, `TestAmbiguity_BoundaryTable` |
| FR-021 | US-3 | A cross-kind near-miss is made usable; The cross-kind rule is decided by the tools' own category values; boundaries | `TestAmbiguity_CrossKindNearMissPromotes`, `TestAmbiguity_KindIsToolCategory`, `TestAmbiguity_BoundaryTable` |
| FR-022 | US-3 | A destructive cross-kind near-miss is excluded; A confirmation-gated cross-kind near-miss is excluded; A destructive result inside the confident band is still made usable | `TestAmbiguity_DestructiveExcludedFromSpeculativeBand`, `TestAmbiguity_ConfirmationGatedExcludedFromSpeculativeBand`, `TestAmbiguity_DestructiveStillPromotedInConfidentBand` |
| FR-023 | US-3 | The number made usable is capped at three | `TestAmbiguity_CappedAtThree`, `TestAmbiguity_TieBreakIsRankOrder` |
| FR-024 | US-3 | A single permitted result skips the comparison | `TestAmbiguity_SingleCandidateSkipsComparison` |
| FR-025 | US-3 | Adding a tool without adjudicating its destructive status fails the build | `TestAdministrativeToolNames_Drift` |
| FR-026 | US-3 | Adding a tool without adjudicating its destructive status; Renaming a listed destructive tool | `TestAdministrativeToolNames_Drift` |
| FR-027 | US-3 | A dominant top result is made usable alone | `TestAmbiguity_DominantHitPromotesAlone` |
| FR-028 | US-3 | The by-name path is unaffected by the comparison | `TestAmbiguity_ByNameFlowUnchanged` |
| FR-029 | US-3 | Two near-tied results are both made usable | `TestAmbiguity_ResponseCardinalityOnly` |
| FR-030 | US-4 | Every search-only tool remains findable | `TestVisibility_SearchOnlyToolsRemainInSearchIndex` |
| FR-031 | US-4 | Every search-only tool remains findable | `TestVisibility_SearchOnlyToolsRemainInSearchIndex` — **static index membership only**, matching this row's now-narrowed claim |
| FR-031a | US-4 | An unlisted tool is found by description | `TestVisibility_SearchOnlyToolFoundByDescriptionBecomesUsable` (test 52a) — the **dynamic promotion** half, previously claimed by FR-031 and cited to a membership-only test. Supporting, non-substituting coverage: `TestAmbiguity_DominantHitPromotesAlone` (promotion cardinality, not exposure level) and the `pkg/tools/load_tool_test.go` regression row |
| FR-032 | US-4 | Reclassified tools land at the intended level | `TestVisibility_TierArithmetic` |
| FR-033 | US-4 | The per-turn listing names only the previewed set | `TestManifest_RenderedBlockIsTwentyTwoLines`, `TestVisibility_PreviewedSetIsExactlyEight` |
| FR-034 | US-4 | Adding a tool without recording its exposure level fails the build | `TestVisibility_EveryCatalogNameHasRecordedLevel`, `TestVisibility_PreviewedSetIsExactlyEight` |
| FR-035 | US-4 | A second agent does not inherit; Switching back restores | `TestBucketKey_CrossAgentIsolation`, `TestBucketKey_RoundTripPreservation` |
| FR-036 | US-4 | Ending a conversation discards every agent's usable set | `TestBucketKey_NoLeakAfterSessionClose`, `TestPendingDiscoveries_NoSideTableLeak` |
| FR-037 | US-4 | A static tool made usable stays usable for the conversation; A static tool made usable does not decay with the discovery lifetime | `TestStaticPromotion_SurvivesBeyondDiscoveryLifetime` — **the static / `loadedTools` mechanism only**. One test covers both scenarios deliberately: surviving past the discovery-lifetime boundary is a strict superset of surviving the plain multi-turn case, and the subset relationship is stated at the scenarios themselves rather than left for the reader to infer |
| FR-037a | US-4 | A discovered external tool does decay with the discovery lifetime | `TestExternalPromotion_DecaysWithDiscoveryLifetime` — **the pre-existing MCP registry TTL only** (`cfg.Tools.MCP.Discovery.TTL`, `PromoteTools`/`TickTTL`), asserted unchanged. This row and the FR-037 row above assert opposite outcomes on purpose, because they exercise two structurally independent mechanisms; see ADR-071 §1.1.1 |
| FR-038 | US-4 | An unused discovery is counted once; A used discovery is never counted | `TestNoFollowUp_CountsOnceAfterHorizon`, `TestNoFollowUp_UsedDiscoveryNeverCounts` |
| FR-038a | US-4 | An unused discovery is counted once after the horizon; Another agent's use does not clear the first agent's pending discovery | `TestNoFollowUp_CrossAgentIsolation`, `TestNoFollowUp_CountsOnceAfterHorizon` (extended with the hand-off-and-return rows 11–13 of the unused-discovery dataset) |
| FR-039 | US-4 | A by-name load never creates a pending discovery; Another agent's use does not clear the first agent's; A conversation shorter than the horizon still counts | `TestNoFollowUp_ByNameLoadNeverCounts`, `TestNoFollowUp_CrossAgentIsolation`, `TestNoFollowUp_CountedAtSessionClose` |
| FR-040 | US-1, US-4 | Empty permitted result set increments the operator counter | `TestSearch_ZeroResultCounterIncrements` |
| FR-041 | US-4 | Both counter series are reachable on the metrics endpoint | `TestMetricsEndpoint_BothCounterSeriesPresent` |
| FR-042 | US-4 | The revert control restores the full listing; does not undo permission filtering; does not undo per-agent scoping | `TestManifest_RevertControlRestoresFullListing`, `TestManifest_RevertControlDoesNotUndoPermissionFilter`, `TestManifest_RevertControlDoesNotUndoPerAgentScoping` |
| FR-043 | US-4 | The revert control restores the full listing | `TestManifest_RevertControlRestoresFullListing` (tracked-item filing verified at ratification) |
| FR-044 | US-4 | The listing header tells the model more tools exist | `TestManifest_HeaderStatesMoreToolsExist` |
| FR-045 | US-4 | Reclassified tools land at the intended level | `TestVisibility_TierArithmetic` |
| FR-050 | US-5 | Switching to a named agent emits the signal; Returning to default emits the signal | `TestSwitchAgent_TargetIsOnlyRequiredParameter`, `TestAgentSwitchedFrame_NamedTargetEmitsWithIdentifier` |
| FR-051 | US-5 | A switch without the explanatory note succeeds | `TestSwitchAgent_TargetIsOnlyRequiredParameter` |
| FR-052 | US-5 | The switching operation completes within its ceiling on both branches; A switch that exceeds the ceiling emits no active-agent signal | `TestSwitchAgent_TimeoutCeilingOnBothBranches` (the ceiling exists on both branches), `TestAgentSwitchedFrame_TimedOutSwitchEmitsNothing` (test 20a — the **outcome** when it trips) |
| FR-053 | US-5 | Switching to a delegation-only worker fails; Returning to a default that points at a worker fails | `TestSwitchAgent_WorkerTargetRejectedOnBothBranches` |
| FR-054 | US-5 | A switch to a named agent transfers a budgeted brief; A return to default transfers no brief | `TestSwitchAgent_TranscriptTransferRunsForNamedTarget`, `TestSwitchAgent_TranscriptTransferSkippedForDefault` |
| FR-055 | US-5 | Replay of a stored handoff still signals the active agent | `TestSwitchAgent_AuditPrefixFrozen`, `TestReplay_HandoffEntryStillEmitsFrame` |
| FR-055a | US-5 | A return to default transfers no brief | `TestSwitchAgent_TranscriptTransferSkippedForDefault` (the branch where the asymmetry is observable; **the requirement itself is a filing obligation, verified at ratification** the way FR-043's and FR-066's tracked items are — there is no behavioural change to assert) |
| FR-056 | US-5 | Returning to default emits the signal with no identifier; An agent whose identifier is the reserved word is warned about at startup | `TestSwitchAgent_SentinelAlwaysWins` (rule-not-lookup-order, **and** the exact-case comparison) |
| FR-057 | US-5 | An agent whose identifier is the reserved word is warned about at startup | `TestBoot_CollidingDefaultAgentIdentifierWarns` — the **startup-warning** path: boot succeeds, warns, sentinel still resolves |
| FR-058 | US-5 | Creating an agent named with the reserved word is rejected; Updating an agent to the reserved word is rejected | `TestAgentCreate_RejectsReservedIdentifier`, `TestAgentUpdate_RejectsReservedIdentifier` *(both gated on ratification item 1 — see Ambiguity Warnings; if it is declined, FR-058 is dropped and neither test is written)*. **These are the creation/update-rejection path.** FR-058 previously cited `TestBoot_CollidingDefaultAgentIdentifierWarns`, which tests the opposite outcome on a different path — a pre-existing agent that must be *warned about and admitted*, not rejected. That citation is corrected here and the boot test now maps only to FR-057, where it belongs |
| FR-059 | US-5 | A delegated sub-task cannot switch; A nested delegated sub-task also cannot switch | `TestExcludedSwitchAgent_AbsentFromChildRegistry`, `TestDelegateGrandchild_SwitchAgentAbsent`, `TestSubturnLog_ExcludedFieldDerived` |
| FR-060 | US-5 | Switching to a named agent emits with identifier; Returning to default emits without; Switching to the agent that happens to be the default | `TestAgentSwitchedFrame_NamedTargetEmitsWithIdentifier`, `TestAgentSwitchedFrame_DefaultTargetEmitsWithoutIdentifier`, `TestAgentSwitchedFrame_DefaultByIdentifierEmitsWithoutIdentifier` |
| FR-061 | US-5 | A failed switch emits no signal; A switch that exceeds the ceiling emits no active-agent signal; The active agent cannot be resolved after a successful switch | `TestAgentSwitchedFrame_FailedSwitchEmitsNothing` (fails at resolution), `TestAgentSwitchedFrame_TimedOutSwitchEmitsNothing` (fails inside the timed region — a different path to the same rule), `TestAgentSwitchedFrame_UnresolvableActiveAgentWarns` |
| FR-062 | US-5 | Replay of a stored handoff still signals the active agent | `TestReplay_HandoffEntryStillEmitsFrame` |
| FR-063 | US-5 | The published exposure-level description names only live capabilities | `TestContractDescription_NamesOnlyLiveTools` |
| FR-064 | US-5 | The published exposure-level description names only live capabilities | `TestBlanketGrep_NoLiveRetiredNames` |
| FR-065 | US-5 | The published exposure-level description names only live capabilities | `TestBlanketGrep_NoLiveRetiredNames` |
| FR-066 | US-5 | A delegated sub-task cannot switch the parent conversation | `TestExcludedSwitchAgent_AbsentFromChildRegistry` (defect deferred; tracked item verified at ratification) |
| FR-067 | US-5 | A nested delegated sub-task also cannot switch | `TestDelegateGrandchild_SwitchAgentAbsent` |
| FR-070 | US-6 | The discovery capability is never denied by the coverage repair | `TestBoot_MigrationPrecedesCoverageRepair` |
| FR-071 | US-6 | Disagreeing legacy entries resolve to the strictest; The strictest-wins fold | `TestPolicyKeyFold_StrictestWins` |
| FR-072 | US-6 | An existing strict entry under the new name survives; The strictest-wins fold | `TestPolicyKeyFold_StrictestWins` |
| FR-073 | US-6 | Disagreeing legacy entries resolve to the strictest | `TestPolicyKeyMigration_LegacyKeysDeleted` |
| FR-074 | US-6 | Both install-wide and per-agent records convert | `TestPolicyKeyMigration_CoversGlobalAndPerAgent` |
| FR-075 | US-6 | Conversion is byte-identical on a second start; Two concurrent starts converge | `TestPolicyKeyMigration_Idempotent` |
| FR-076 | US-6 | Startup validation finds no coverage gap after conversion | `TestBoot_ValidationPassesAfterConversion` |
| FR-077 | US-6 | An interrupted conversion leaves the original intact | `TestPolicyKeyMigration_InterruptedWriteLeavesOriginal` (rollback statement verified in release notes) |
| FR-078 | US-6 | An interrupted conversion leaves the original intact | `TestPolicyKeyMigration_InterruptedWriteLeavesOriginal` |
| FR-080 | US-7 | The moved block sits at the required position | `TestContextBlocks_CatalogAtIndexOne` |
| FR-081 | US-7 | The moved block sits at the required position; A misplaced block fails; A block without a marker fails | `TestContextBlocks_CatalogAtIndexOne`, `TestContextBlocks_MisplacedCatalogFails`, `TestContextBlocks_CatalogCarriesCacheMarker` |
| FR-082 | US-7 | A turn that makes a new tool usable still assembles correctly | `TestContextBlocks_LoadedDeltaStaysOutside` |
| FR-083 | US-7 | The per-turn listing names only the previewed set | `TestManifest_RenderedBlockIsTwentyTwoLines` |
| FR-084 | US-7 | A misplaced moved block fails the structural check | `TestContextBlocks_MisplacedCatalogFails` |
| FR-085 | US-7 | Cache-read volume rises; A rise below the floor drops this story | Manual cache-read measurement |
| FR-085a | US-7 | Cache-read volume rises on a turn with no new usable tools | Manual cache-read measurement (the one-off gate) + `TestContextBlocks_CatalogAtIndexOne` (the ongoing *structural* guard, which is explicitly **not** a benefit check). **The posture statement itself is verified at ratification**; the optional passive sampling is a SHOULD with no test gate, by design |
| FR-086 | US-7 | A rise below the floor drops this story | Manual cache-read measurement |
| FR-090 | US-2, US-5, US-6 | The startup validator finds the renamed capability; Startup validation finds no coverage gap | `TestStaticCatalog_ContainsRenamedDiscoveryTool`, `TestBoot_ValidationPassesAfterConversion` |
| FR-091 | US-2, US-5 | The published exposure-level description names only live capabilities | `TestContractDescription_NamesOnlyLiveTools` |
| FR-092 | US-2, US-5 | (Behavioral contract, blanket pass) | `TestBlanketGrep_NoLiveRetiredNames` |
| FR-093 | US-1 | One agent's denials do not alter another agent's ranking | `TestSearch_RankingInvariance` (process requirement; this test is its mechanical residue) |

**Completeness check**: every FR-xxx above appears with at least one BDD scenario and one test, with four deliberate exceptions recorded in their own rows — FR-018, which requires the *absence* of a control and therefore has no behaviour to assert; FR-019, whose testable half is the constants themselves and whose doc-comment half is verified in review the way FR-043's tracked item is; and FR-055a and FR-085a, which are **filing and posture obligations with no behavioural delta to assert** — the first requires a known asymmetry be tracked rather than changed, the second requires an accepted residual exposure be stated rather than mitigated. Both are verified at ratification, on the same footing as FR-043's and FR-066's tracked items. A requirement whose whole content is "record this" cannot be discharged by a test, and inventing one for matrix symmetry would be the coverage theatre this matrix's own caution warns about. Every BDD scenario in the Phase 3 section appears in at least one row. Two scenarios are not named against an FR in the rows above, and both are covered:

- *A provider that discards the structured stable region still works* — traces to **FR-082** (the volatile record stays outside the cached region) and to the Regression Test Requirements row for non-marker provider assembly, via `TestProviderWithoutMarkers_AssemblesCorrectly`.
- *No more than two cache breakpoints are used* — traces to **SC-016**, not to any FR, via `TestContextBlocks_AtMostTwoCacheMarkers`. An earlier draft cited this as `FR-016`, which is the user-acceptance probe requirement and has nothing to do with cache breakpoints; the resemblance of the two identifiers is exactly why it went unnoticed.

**A caution about this matrix specifically.** The first review of this specification found FR-058 mapped to a test that verified a *different* behaviour on a *different* code path with the *opposite* outcome, and the row read as covered. The second review found the same failure mode again, in a row this specification had itself added while stating that caution — FR-031 claimed both a static index-membership property and a dynamic promotion behaviour, and cited one test that covers only the first. A test name that mentions the same nouns as a requirement is not evidence that it exercises that requirement. When checking this matrix, read each cited test's own row in the Test Implementation Order and confirm the path and the outcome match, not just the subject.

**Twice is a pattern, and sampling is what found both.** Neither review cross-checked all ~87 rows; both found their instance by reading a handful. **Before this specification is next re-reviewed or signed off, one mechanical pass MUST be run over the whole matrix**, cross-referencing every row's cited test against that test's own "Traces to BDD Scenario" column in the Test Implementation Order and flagging any row whose FR claims coverage the cited test's own definition does not trace to. It is script-assisted work of a few minutes against a document whose two known defects of this exact shape were both found by hand, and it is the only check that turns "no further instances were sampled" into "no further instances exist". Two known-good asymmetries must be allowed rather than flagged: a row citing one test for two scenarios where one is a strict superset of the other (FR-037), and a row citing supporting non-substituting coverage alongside its primary test (FR-031a).

---

## Ambiguity Warnings

> The source ADR survived five revisions and four adversarial reviews, and the fifth review still found a critical gap — in a mechanism the document had cited accurately every time it mentioned it, but had only ever read at the lines supporting the claim being made. This audit was therefore run on the assumption that the ADR is **not** ambiguity-free.
>
> **Every row below has been resolved into the specification above** using the most conservative reading consistent with the ADR's own decisions. The rows marked **OPERATOR** are resolved but should still be reviewed, because the resolution is a product judgement or a safety posture rather than a mechanical consequence.

| # | What's ambiguous | Likely agent assumption | Resolution applied | Status |
|---|---|---|---|---|
| 1 | **Rollback is never mentioned.** Converting stored permissions to the new names is one-way. An operator who rolls the binary back to a build predating this work has a configuration whose new names that build does not recognise and whose retired names are gone — so its own coverage repair writes denials for the retired names on every agent. That is the exact failure the conversion exists to prevent, mirrored, and the ADR does not discuss it | Ship the conversion and treat a binary rollback as a supported undo, because the revert control is presented as the rollback story | Documented as **one-way** (FR-077). Release notes must state that rolling back requires restoring a pre-conversion backup. A timestamped pre-conversion copy is required as a SHOULD (FR-078). The revert control is scoped in the spec to the exposure split only, never presented as an undo for the conversion | **OPERATOR** — highest-value review item |
| 2 | **The unused-discovery counter has a non-zero floor under correct operation.** An ambiguous search makes up to three tools usable; a correct choice uses one, so the other two count as unused once the horizon passes. The ADR's operator guidance reads the counter as if a rise means something went wrong | Treat any non-zero value as a signal to tighten the thresholds, including the value produced by the feature working exactly as designed | Recorded as an explicit edge case and dataset row 8. The counter's operator trigger is specified as a **change in rate against a stable turn count**, never an absolute value. Multi-promotion is counted per the ADR's literal text — all promoted-but-unused names — because that is what the ADR's stated tightening ladder needs to be actionable | **OPERATOR** — affects how the ADR's own trigger is read |
| 3 | **The 11 borderline destructive-tool names are an open operator question** (ADR §11 item 8). The mechanism is decided; the membership is not. The ADR recommends including the two irreversible-communication verbs | Either quietly ship the 13-name seed as final, or quietly add the recommended two | Specification pins the **13-name seed** and the drift test asserts exactly that count. The ADR frames this as a product call in the same genre as the exposure placements, which it states are the operator's own and are not re-derived — an architect adding names silently is precisely what that framing forbids. **Adding a name only narrows the speculative band further, so nothing is blocked either way** | **OPERATOR** — ADR's own open item, recommendation is yes to the two communication verbs |
| 4 | **Reserving the destination word at agent creation is an open operator question** (ADR §11 item 1). It forbids a name someone might legitimately want | Implement the rejection as decided, since the ADR specifies it in detail | Specified as **SHOULD** (FR-058), not MUST. The unconditional sentinel resolution and the startup warning are specified as MUST, because the ADR states both hold regardless of how this lands. A "no" leaves the collision **warned about** rather than prevented, which is a complete posture on its own | **OPERATOR** — ADR's own open item |
| 5 | **The configured result limit changes meaning.** Today it bounds how many ranked results are considered; after the filter it bounds how many *permitted* results are returned, while the ranking now walks the whole corpus. The ADR does not name this as a semantic change to an operator-facing setting | Change the call and not notice the setting's meaning shifted | Specified explicitly: the setting bounds the returned permitted list, with a separate hard bound of 50 on the permission checks performed. Both stated as machine-verifiable constraints so the shift is recorded rather than inferred | Resolved |
| 6 | **A zero or negative result limit is unspecified.** Both are reachable through a hand-edited configuration | Add validation or clamping, which is a behaviour change nobody asked for | Resolved conservatively as **no special-casing**: the query yields no permitted results, returns the existing no-match response, and increments the empty-result counter — identical to any query with no permitted results. Dataset rows 9 and 10 pin it | Resolved |
| 7 | **Re-surfacing an already-usable tool.** A second search that ranks a tool already made usable — does it create a new pending-discovery record, or refresh an existing one's horizon? | Record on every promotion call, which lets repeated searching defer the count indefinitely | Resolved as **record only on the first promotion**. A re-surfacing of an already-available tool is not a new wasted discovery, and refreshing would make the horizon unreachable under repeated searching. Dataset row 9 pins it | Resolved |
| 8 | **Which pending records a per-turn sweep examines.** With agent-and-conversation keys, a turn could sweep its own key or every key for the conversation | Sweep everything for the conversation, firing one agent's counter on another agent's turn index | Resolved as **the current turn's own key only**. A turn owns one agent and can compute only that key; an agent handed off away from is covered by the conversation-close sweep, which the ADR already requires. This keeps every count attributable to the turn index that produced it | Resolved |
| 9 | **What the revert control reverts.** It is introduced as the undo for the accepted exposure risk, but three changes ship together | Revert everything introduced alongside the exposure split, including the permission filter | Resolved as **the exposure split only**. The permission filter and the per-agent keying are corrections to live defects, not part of the bet being unwound; reverting a security fix through a usability dial would be a regression. Two dedicated scenarios and two tests pin this | Resolved |
| 10 | **The lost suggestion for externally-provided tools.** Filtering the "did you mean" suggestion through the permitted set drops externally-provided tools, which are not in that set. The ADR offers two options and picks neither | Implement the union of both sets, adding a new enumeration path | Resolved as **accept the loss**, the simpler option. Losing a suggestion is a usability cost; naming a denied tool is the defect being fixed, and the ADR's own principle on this surface is to fail closed. Recorded so the cost is a decision rather than a side effect | Resolved |
| 11 | **An empty note in the recorded conversation entry.** The note becomes optional, and the recorded text embeds it after a fixed prefix that this work freezes | Tidy the formatting when the note is absent, breaking the frozen prefix that replay matches on | Resolved as **preserve today's formatting exactly, including the empty trailing segment**. Any change to that text risks the replay coupling, whose producer and consumer share no constant. Dataset row 11 and a dedicated test pin it | Resolved |
| 12 | **Behaviour when the sandbox is disabled.** Exposure tiering is a context-budget mechanism, but the permission filter routes through the same resolver the sandbox governs | Treat search-only as a security boundary and add sandbox-mode branching | Resolved as **no change**. Tiering is independent of sandbox mode; the filter behaves exactly as the existing resolver behaves under any mode. The spec states the property as bounded by **permission**, never by exposure level | Resolved |
| 13 | **The embedded bundle is not in the ADR at all.** The binary embeds the single-page application from a sync directory, not from the build output | Fix the two visibility sites, watch the suite pass, and ship a binary carrying the old behaviour | Resolved as an explicit requirement (FR-017) and success criterion (SC-019), with the sync steps in Development Setup. **This is the ADR's own flagged regression surviving its own fix** | Resolved — spec-added, not in the ADR |
| 14 | **The user-acceptance probe scripts instruct a live agent by tool name.** They are outside every site table in the ADR | Leave them; they are scripts, not code | Resolved as an explicit requirement (FR-016). Post-rename they instruct the agent to call a tool that does not exist, so two probes report failure for a reason unrelated to what they test — a false red that will be read as a real one | Resolved — spec-added, not in the ADR |
| 15 | **Whether the conversion covers per-agent permission maps as well as the install-wide one.** The ADR names both map shapes in one clause without stating they are folded independently | Convert one and not the other, or fold across them | Resolved as **both, folded independently** (FR-074), with dataset rows 7–9 pinning each case including disagreement between the two maps | Resolved |
| 16 | **Whether the exposure filter's owner moves when the cache story lands.** The ADR decides it stays in the builder, but the cache story is conditional — so the builder it stays in depends on whether that story ships | Push the filter to the caller during the cache work, giving it two owners | Resolved as **inside the builder in both cases** (FR-083). If the cache story is dropped, the filter simply stays where the exposure work put it. Two owners is how an unlisted tool eventually leaks a listing line on one of two paths | Resolved |
| 17 | **A zero top score.** A ratio against a zero reference is undefined | Divide, producing an error or promoting everything | Resolved as **promote the top result alone**, matching the degenerate single-candidate path. Dataset row 14 pins it | Resolved |
| 18 | **Whether the two comparison rules are alternatives or must both hold.** The ADR says "either", but the narrowing applies to only one of them | Apply the narrowing to both, or require both conditions | Resolved as **alternatives**, with the narrowing on the second rule only. Three dedicated scenarios pin all three combinations | Resolved |
| 19 | **Story 3 has no revert control while Story 4 has a mandatory one**, and both ship judgements made without usage data. The ADR gives one an explicit flag and the other nothing, and never explains the difference | Read the asymmetry as an oversight and add a second flag "for symmetry", or ship Story 3 with no recourse and no reasoning on record | Resolved as **no control for Story 3, with the reasoning stated** (FR-018, and the three-point argument in User Story 3): the failure mode is a wasted schema not an unreachable capability; the permission check and the full execution gate still bound it, so schema reachability widens and capability reachability does not; and the documented first response is a constant-tightening change the same size as a revert would be. FR-019 makes that response discoverable at the one site that must change. The rejected alternative — a second boolean forcing single-promotion — is named so the operator can overturn this with one line | **OPERATOR** — a judgement, not a mechanical consequence |
| 20 | **Whether the sentinel comparison is case-sensitive.** The ADR pins the *rejection* at agent creation as case-insensitive and the *resolution* as `target == "default"`, in two different sections, without ever noting they differ | Make both case-insensitive "for consistency", which silently shadows any agent legitimately named in another casing | Resolved as **exact for resolution, case-insensitive for rejection**, matching the ADR's two literal statements, with the reason each is right recorded as an edge case and in FR-056/FR-058. An earlier draft of this spec asserted case-insensitive sentinel resolution in a dataset row; that row is corrected | Resolved — spec-side error found and fixed |
| 21 | **Whether the unused-discovery horizon ticks while the discovering agent is inactive.** Warning 8 resolves *which key* a sweep examines but not what advances the clock | Count the conversation's global turns, so an agent handed off away from fires a counter it never had a turn to act on | Resolved as **agent-local turns** (FR-038a), which is the only reading consistent with warning 8 — a sweep runs on one agent's turn and can compute no other agent's key, so no other agent's clock can advance. The never-returning case is covered by the close sweep. Dataset rows 11–13 pin all three | Resolved |
| 22 | **Two side tables with one key, one creation moment and one sweep.** They could be one record with a richer value, and two parallel maps can desync | Build two independent scans, so an entry present in one and absent from the other has no symptom until a count is wrong | Resolved as **two structures, one sweep function** (FR-039): the ADR names them as separate maps and its own required test asserts on the second by name, so collapsing them would diverge from the source without cause — but the desync risk the review identified is real and is removed by requiring a single sweep taking the conversation identifier, with a test asserting both are empty afterwards. FR-039 now also states explicitly that the two-structure shape is a **fidelity-to-source choice rather than a technical constraint**, so a later refactor may collapse the maps provided it keeps the single sweep and the both-empty assertion — the load-bearing half — without amending this specification | Resolved |
| 23 | **Which decay mechanism the "no eviction" rule governs.** Two structurally independent promotion mechanisms exist — the new, permanent `loadedTools` record this work builds for the 89 static tools, and the pre-existing MCP registry TTL (`cfg.Tools.MCP.Discovery.TTL`, `PromoteTools`/`TickTTL`) that already decays externally-provided tools. An earlier revision of this specification stated the prohibition **unconditionally** while its own required test suite demanded the MCP decay path keep working | Read "must not build any expiry, eviction or unload path" literally and either delete the live MCP TTL behaviour, or build a *new* decay mechanism to satisfy the external-tool test and wire it against the static record too — the exact eviction the prohibition exists to forbid. Both readings pass roughly half the required tests and neither is detectable from the spec text alone | Resolved by **scoping every statement of the prohibition to the static usable-set record**, naming both mechanisms with their literal identifiers in the Pinned Identifiers table plus a dedicated two-mechanism note, and splitting the requirement into FR-037 (static: no decay, permanent for the conversation) and FR-037a (MCP TTL: preserved unchanged, explicitly outside FR-037's reach). The scoping also disambiguates FR-038's 5-turn horizon, which shares the MCP TTL's default value by coincidence and is now required to stay decoupled from it. This inherits ADR-071 §1.1.1's own `CRIT-101` resolution rather than re-deriving it | Resolved — spec-side error found and fixed, second review |
| 24 | **Whether any path other than the switching destination and agent CRUD can collide with the reserved word.** FR-056 to FR-058 cover the sentinel resolution and the creation/update rejection thoroughly; no requirement states whether some other pre-existing agent-identifier lookup — a route addressing an agent literally by id, for instance — could also be shadowed by an agent named `default` | Assume the two covered paths are the whole surface, because they are the only ones this work touches | Resolved as **almost certainly pre-existing and out of scope**: the word was already reserved in the return-to-default sense before this merge, so any such collision predates this work and is not introduced by it. **Recorded rather than assumed away** — it needs a one-line confirmation at ratification, not an investigation, and if one is found it is its own tracked item on the same footing as FR-066/FR-067, never a late addition to this change | **OPERATOR** — one-line confirmation at ratification |

---

## Evaluation Scenarios (Holdout)

> **These are for post-implementation evaluation only.** They must not be visible to the implementing agent during development, and they are deliberately absent from the TDD plan and the traceability matrix. They are written from outside the implementation — each is checkable by an operator driving the running system, without reading any source file, and none can be satisfied by pattern-matching against the scenarios above.

### Scenario: A new operator finds an administrative capability without being told it exists

- **Setup**: A fresh install. An operator who has never used this system opens a conversation with the default agent and has read no documentation about the tool catalog.
- **Action**: Ask the agent, in ordinary language, to list the workspaces on this install and then create a new one — a capability that is search-only and never listed.
- **Expected outcome**: The agent completes both without the operator naming a tool, without asking the operator which tool to use, and within one additional round trip beyond what it would take if the tools were listed.
- **Category**: Happy Path

### Scenario: A long working session does not degrade

- **Setup**: One conversation with one agent, driven for at least 40 turns of varied work touching filesystem, web, task and workspace capabilities.
- **Action**: Track the wall-clock time to first token and the reported prompt token count on turns 5, 20 and 40.
- **Expected outcome**: Neither figure grows in a way attributable to accumulated tool schemas. The prompt token count rises as tools are found, plateaus, and does not exceed the volume the system sent per turn before any of this optimisation existed.
- **Category**: Happy Path

### Scenario: A conversation passed between three agents ends up where it started

- **Setup**: Three agents with different permitted tool sets, one conversation.
- **Action**: Hand the conversation from the first to the second, have the second do work requiring a tool it must find, hand to the third, then return to the default agent. Observe the interface's active-agent indicator at each step, including after reloading the page.
- **Expected outcome**: The indicator follows every step and is correct after the reload. No agent begins a turn already able to call a tool it never looked for.
- **Category**: Happy Path

### Scenario: An agent with a restricted permission set cannot learn what it is missing

- **Setup**: An agent configured with denials for every destructive capability. An operator with a list of those denied capability names, kept off-screen.
- **Action**: Over at least fifteen conversational turns, ask the agent in varied phrasings to describe every capability it can find, to search for ways to remove, delete or reconfigure things, and to report anything it finds but cannot use.
- **Expected outcome**: No denied capability's name or description appears anywhere in the agent's output or in any tool result shown in verbose mode. The agent's account of what it cannot do is generic, not enumerated.
- **Category**: Error

### Scenario: An upgraded install is fully functional on first start

- **Setup**: A copy of a real pre-upgrade install directory, with its existing permission configuration untouched. The upgraded binary.
- **Action**: Start it. Without editing any configuration, open a conversation, ask the agent to hand off to another agent, and ask it to perform work requiring a search-only tool.
- **Expected outcome**: Both succeed on the first attempt. Startup logs contain no permission-coverage error and no unexpected denial. The stored configuration afterwards contains no retired capability name.
- **Category**: Error

### Scenario: A wrong search guess costs less than it used to

- **Setup**: Two builds — one predating this work, one after it. The same agent, the same fresh conversation on each.
- **Action**: Issue the same deliberately underdetermined request on both — one whose plain-language description plausibly matches two capabilities of different kinds. Count the round trips before the agent performs the requested work.
- **Expected outcome**: The newer build completes in strictly fewer round trips at least as often as the older one, and never in more.
- **Category**: Edge Case

### Scenario: The general-purpose shell stops being the path of least resistance

- **Setup**: Two builds as above. A set of ten requests each of which a narrow purpose-built capability handles better than shelling out — appending a line to a file, serving a directory, reading a stored message.
- **Action**: Issue all ten on each build and record which capability the agent reaches for first.
- **Expected outcome**: The newer build reaches for the purpose-built capability materially more often. This is the behavioural change the exposure split exists to produce, and it is the one thing no automated test in this specification measures.
- **Category**: Edge Case

---

## Assumptions

- The static catalog is exactly 90 names before this work and 89 after. **Verified in this session** by exact count over the catalog literal; a naive count returns 92 because two quoted words appear inside its comments.
- The always-listed set contains 18 names today. **Verified in this session** by count.
- The permission model's no-default-fallback rule holds throughout: every static tool resolves from an explicit, literal entry for every agent, validated at startup and at every agent write. Nothing here introduces a fallback.
- Externally-provided tools never reached the per-turn listing before this change, so making them default to search-only is a no-op rather than a behavioural change.
- The conversation-close path is cold, not on the per-turn hot path, so a linear scan there is acceptable — it already performs one for a sibling structure.
- Configuration persistence is atomic, so no partially-converted configuration file is ever observable.
- **Rolling the binary back after conversion is not supported without restoring a pre-conversion backup.** This is an assumption about operator behaviour that the release notes must convert into an instruction.
- The cache story's benefit accrues to one provider family only. Two other provider paths receive concatenated system text — one strips the structured region, one never reads it — and both still benefit from stable-before-volatile ordering if they cache prefixes automatically.
- The measurement gating the cache story is run once, manually, by the operator, against a real funded account. It is not automated and must not be added to continuous integration.
- The exposure placements themselves are the operator's product decisions, recorded rather than re-derived. This specification pins them so they cannot drift silently; it does not re-argue them. **ADR-071 §4.1's four literal name lists are the sole authority on membership** — every table here that names a tool is quoting them, and the "defer, do not restate" discipline is applied uniformly, including to the Existing Codebase Context section. The one place the first draft broke that discipline and stated a diff independently is the one place it was wrong.
- This specification's site lists are the best available starting point and are **not** claimed complete. Every prior pass over this surface disproved a completeness claim made in good faith, including this one — which found six sites beyond the source ADR's own tables.

## Clarifications

### 2026-08-28

- Q: Do any Go reference patterns apply? → A: No. `docs/reference/go-implementation/` does not exist in this repository, and the library that path describes has no overlap with tool classification, permission resolution, ranking or prompt-cache ordering. Section retained empty rather than deleted.
- Q: Was the code graph used for the codebase-context section? → A: No. The graph tools are not exposed in this session and this checkout's index is five days stale. The Existing Codebase Context section was built by direct verification against source, and every claim in it was read rather than recalled.
- Q: Did direct verification contradict the source ADR anywhere? → A: No — every load-bearing claim checked against *source* held exactly, including the unfiltered result-list construction, both frame branches, the raw literal in the registration guard, both visibility sites, the tautological name guard, and the catalog arithmetic. The ADR's factual accuracy is high; its **completeness** claims are what needed extending. **But this specification contradicted the ADR twice, in its own text** — see the second dated block below.
- Q: Did direct verification find anything the ADR missed? → A: Yes, six sites. Two are functional and are now requirements — the user-acceptance probe scripts that instruct a live agent by tool name, and the embedded bundle that the binary actually serves. Four are prose-only and belong to the blanket pass.
- Q: How were the ADR's own open ratification items handled? → A: Left open, resolved conservatively, and surfaced in the Ambiguity Warnings table marked OPERATOR. The destructive-tool list is pinned at its 13-name seed; the reserved-name rejection is specified as SHOULD rather than MUST; both hold whichever way the operator decides, and nothing is blocked on either.
- Q: What is the single item most worth escalating? → A: Rollback. The stored-permission conversion is one-way and the source ADR does not discuss it. An operator who rolls the binary back gets the exact failure the conversion exists to prevent, mirrored — silently, with a successful start.

### 2026-08-28 — first adversarial review, fix pass

The first `/grill-spec` review returned **BLOCK** (1 CRITICAL, 6 MAJOR, 3 MINOR, 2 OBSERVATION). Every finding is resolved above. What changed, and what the blanket re-verification found beyond the review's own list:

- Q: What was the CRITICAL? → A: The Existing Codebase Context table stated the always-listed diff as 3 removals and 2 additions. The real diff, per ADR-071 §4.1, is **6 removals and 5 additions** — `navigate`, `create_task` and `update_task` also leave; `list_mounts`, `send_file` and `message_parent` also join. Both diffs total 17, so no count-based gate could catch it, and the wrong one keeps the task-mutation verbs permanently visible — defeating User Story 4's stated purpose with a green build. The table now defers to §4.1 rather than restating a diff, the correct diff is stated once in its own subsection, and the drift test is required to transcribe §4.1's lists name for name rather than assert a total.
- Q: What was the shared root cause of three of the six MAJORs? → A: The specification was precise about prose facts and had dropped concrete engineering identifiers the ADR already pinned. Fixed structurally rather than case by case: a **Pinned Identifiers** table now carries every literal in one place — the reserved word `"default"`, the revert flag `PreviewAllLazy`, `Tool.Category()` as the meaning of "kind", and eleven more the review did not name.
- Q: Did the blanket re-verification find anything beyond the review's list? → A: **Yes, four things.** (1) The specification never stated the literal new name of the discovery capability (`ToolSearch`) or of the merged switching capability (`switch_agent`) anywhere in its prose — the same omission class as the three the review found, undetected because the metric names imply the first. (2) A dataset row asserted **case-insensitive sentinel resolution**; the ADR pins `target == "default"` exactly, and case-insensitivity applies only to the creation-time rejection. That row is corrected and the deliberate asymmetry is now explained. (3) The Exposure-level dataset repeated the wrong 3-out/2-in framing in a second place, exactly as the review predicted such an error would. (4) A `file:line` citation for `pkg/agent/loop.go` violates CLAUDE.md's own instruction to cite `file::symbol` in that file, whose line numbers go stale within days; it now names `registerSharedTools`.
- Q: Did the review itself contain an error? → A: One, minor. It located `PreviewAllLazy` on `ManifestVisibility`. It is a field on `ManifestConfig` in `pkg/config/config.go`, *read inside* `ToolManifestVisibility` — which is what makes the revert reach both manifest builders without a second branch. The Pinned Identifiers table and FR-042 state it correctly.
- Q: How was the Story 3 / Story 4 revert asymmetry resolved? → A: As a deliberate decision with three stated grounds (FR-018), not by adding a flag. The rejected alternative is named so the operator can overturn it in one line, and it is marked OPERATOR in the Ambiguity Warnings table.
- Q: What unit gates the cache story? → A: **Tokens, on both sides.** `B = estimateMessageTokens(BuildStaticToolCatalog(...))` and `ΔC` from `providers.Usage.CacheReadTokens`. Never bytes, never characters. No upper bound.

### 2026-08-28 — second adversarial review, fix pass (final; the operator capped review at two passes)

The second `/grill-spec` review returned **BLOCK** (1 CRITICAL, 1 MAJOR, 6 MINOR, 4 OBSERVATION), and independently re-confirmed every round-1 finding as resolved. All twelve are resolved below. This is the last fix pass before implementation; there is no round 3.

- Q: What was the CRITICAL? → A: **A textual self-contradiction on FR-037.** The requirement and the matching Qualitative Prohibition were worded as absolute — "MUST NOT build any expiry, eviction or unload path" — while FR-037's own traceability row cited `TestExternalPromotion_DecaysWithDiscoveryLifetime`, a test that *requires* the eviction the sentence forbids. Two competent engineers could read this and build two different, both-defensible, both-wrong things, each passing about half the required tests.
- Q: What is the actual model? → A: **Two structurally independent mechanisms, and the prohibition governs only one.** (1) The **new, permanent `loadedTools` record** this work builds for the 89 static catalog tools: written by `markToolsLoaded`, read by `buildCompressedToolDefs`/`buildToolManifestNote`, no TTL, no cap, no eviction, removed only by `forgetSession` at conversation close. FR-037's prohibition is about not adding decay *here*. (2) The **pre-existing MCP registry TTL** — `cfg.Tools.MCP.Discovery.TTL` (default 5), stamped by `PromoteTools`, decremented by `TickTTL`, admitted by `IsCore || TTL > 0` — which already decays externally-provided tools and continues to, untouched. Both TTL mutators are guarded by `!entry.IsCore` and are literal no-ops on every static tool, so a static tool cannot decay however the TTL is set. This inherits ADR-071 §1.1.1's resolution of its own `CRIT-101`; it is not re-derived here.
- Q: How was it fixed? → A: Every statement of the prohibition is now scoped to the static record — FR-037, the Qualitative Prohibition bullet, User Story 4's Acceptance Scenario 3, the Behavioral Contract's permanence line, and the three BDD scenarios, each of which now carries the mechanism it exercises. **FR-037a** is new and states the MCP TTL is preserved unchanged and explicitly outside FR-037's reach. The review's grep finding was correct — `TTL`, `IsCore`, `PromoteTools` and `TickTTL` had **zero** occurrences in this document — so three rows were added to **Pinned Identifiers** (the permanent static mechanism, the MCP discovery lifetime, and a definition of "an externally-provided tool"), followed by a two-mechanism comparison note stating in a table which mechanism FR-037's prohibition governs and which it does not. Ambiguity Warning 23 records the whole thing.
- Q: What was the MAJOR, and was it split or re-cited? → A: **Split.** `FR-031` bundled a static claim ("remains present in the searchable index") with a dynamic one ("found by description and made usable"), citing one test — `TestVisibility_SearchOnlyToolsRemainInSearchIndex` — whose own Test-Implementation-Order row traces only to the static scenario. The plan was checked for an existing test covering the dynamic half before adding one: there is none. Test 12 is rename-parity on the pre-existing tiering and never touches the search-only level; test 73 pins promotion cardinality, not exposure level; the `pkg/tools/load_tool_test.go` regression row is supporting coverage, not a substitute. So FR-031 keeps the static half, **FR-031a** is new for the dynamic half, and **test 52a** (`TestVisibility_SearchOnlyToolFoundByDescriptionBecomesUsable`) is added for it — which also closes an orphaned scenario, since "An unlisted tool is found by description" previously had no test row of its own anywhere in the plan.
- Q: Was the 22 / 101 line arithmetic real? → A: **Yes, both, and both are now marked verified with the method.** Re-derived from `BuildCompressedManifest`'s actual emission shape rather than from the ADR: the renderer emits `2 + 2C + N` lines. `C` was obtained by reading the real `Category()` method of each named tool, not assumed — the 8 previewed tools span **6** categories (agents, tasks, web, platform, workspaces, shell) → 22; the 71 reverted tools span **14** → exactly 101. One consequence is now a requirement in its own right: FR-044's "more tools exist" header wording MUST reword the existing second header line rather than add a third, because a 3-line header makes FR-033's own number wrong on the day it ships.
- Q: Were the two "5"s connected? → A: **No, and FR-038 now says so explicitly.** The unused-discovery horizon and `cfg.Tools.MCP.Discovery.TTL` share the default value 5 by coincidence of two separate choices. They must stay decoupled: the MCP TTL decides when an externally-provided tool stops being callable and is already operator-configurable; the horizon decides only when an unused static discovery is counted and withdraws nothing. An operator who has tuned the TTL must not see the horizon move. The constant carries a doc comment saying so, because "fixing" the apparent duplication would reintroduce the CRITICAL's conflation in a second place.
- Q: What about the four minors on Story 5 and Story 7? → A: FR-051 now states that "strongly recommended" governs the **named-target branch only**, pinning ADR §5.1.1's normative description string whose final sentence names that branch explicitly; the asymmetry matches FR-054's, where the budgeted transfer also runs on one branch only. FR-052 now states that exceeding the ceiling **is** a failed switch for FR-061's purposes, with a dedicated scenario, two dataset rows, an edge case and test 20a. **FR-055a** records the audit-trail asymmetry as known, pre-existing and deliberately preserved, and files it as a tracked item alongside FR-066/FR-067 — visible for a future symmetric-audit change rather than re-discovered as a fresh defect now that one capability owns both branches. **FR-085a** states the accepted posture for the cache story as "measured once at merge, never rechecked", with a SHOULD to sample the figure already read for the measurement into an existing metrics path — no new CI requirement, FR-085's prohibition intact.
- Q: And the four observations? → A: All four addressed. A reader-calibration note at the reclassification table records that Tier 1 → Tier 2 is a **round-trip-latency** change, not only a visibility one, paid once per conversation per tool. Ambiguity Warning 24 records the reserved-word question on other identifier-lookup paths as almost certainly pre-existing and needing a one-line confirmation at ratification. FR-039 now states the two-map shape is a fidelity-to-source choice a future refactor may collapse, provided it keeps the one-sweep rule. And the matrix caution now **requires** a mechanical cross-check of all ~87 rows before sign-off — two reviews found this same failure mode by sampling, which makes "no further instances were sampled" the wrong thing to conclude from.
