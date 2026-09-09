# ADR-071 — Tool manifest tier redesign: `ToolSearch`, a search-only third tier, `switch_agent`, and a cached catalog boundary

- **Status:** Proposed — **revision 5** (2026-08-27). Every decision below was made by the operator
  in session on 2026-08-26/27; this document records them with rationale, blast radius, and the
  mechanisms they require. Awaiting ratification before `/plan-spec`.
- **Date:** 2026-08-27
- **Revision history:**
  - **r1** (2026-08-27) — initial draft.
  - **r5** (2026-08-27) — revised in response to the **fourth** adversarial review at
    [`ADR-071-tool-manifest-tier-redesign-r4-review.md`](ADR-071-tool-manifest-tier-redesign-r4-review.md)
    (verdict **REVISE**; 1 CRITICAL, 2 MAJOR, 1 MINOR, 1 OBSERVATION). The review re-verified r4's
    highest-stakes claims — the `IsCore`/TTL gate, the 89-name partition, the `toolVisibility.ts`
    regression, the migration ordering — and found every one accurate; its yield came from asking a
    structurally different question, *"what does the mechanism do that the document doesn't
    discuss?"*, and the answer is the one **CRITICAL**:
    **`ToolSearch`'s own match list has never been policy-filtered.**
    `execSearchAndLoad` builds `matches` — `{name, description}` for every BM25-ranked hit up to
    `maxSearchResults` — **before** any `canLoad` runs, and returns it in full on every path
    including the one whose own comment reads *"all results denied by policy."* Only the single
    auto-loaded tool's *schema* was ever gated. So an unrelated query could disclose the name and
    full description of a policy-**denied** Tier 3 tool — `delete_workspace`, `set_config` — merely
    by ranking in the top 5. D3's headline property was therefore only ever true of **one** of the
    two channels it depends on: the manifest block was policy-scoped, the discovery channel was not.
    **Decided: filter the match list after ranking** — new **§3.2.2**, which also states why
    filtering the *corpus* instead is the wrong fix (it is verifiably a cross-agent leak, a cache
    key it cannot compute, and a change to BM25's own scores). §4.3 is corrected to name both
    prerequisites rather than one.
    **r5's own blanket grep over the discovery surface found two further instances of the same
    unfiltered-results shape** — `ToolRegistry.SearchBM25` and the `canLoad` closure's fuzzy
    name-suggestion fallback — plus three surfaces that are *correctly* filtered and are recorded so
    the next reader does not re-derive them. Per-finding map in **§16**. All four prior review files
    are retained unmodified and none is superseded.
  - **r4** (2026-08-27) — revised in response to the **third** adversarial review at
    [`ADR-071-tool-manifest-tier-redesign-r3-review.md`](ADR-071-tool-manifest-tier-redesign-r3-review.md)
    (verdict **BLOCK**; 2 CRITICAL, 2 MAJOR, 1 MINOR, 1 OBSERVATION). **This is the first revision to
    change a decision rather than tighten one.** Both CRITICAL findings are real and were verified
    independently against source for r4:
    **(1)** r1–r3 all described `ToolSearch` promotion as TTL-decayed. That is true **only for MCP
    tools**. Every one of the 89 static tools this ADR is about is registered `IsCore: true`, and
    `PromoteTools`/`TickTTL` are explicit no-ops for core entries — what actually makes a static lazy
    tool callable is a **session-level map with no decay at all**. Three sections built cost bounds on
    a mechanism that never fires for their own subject, and one "hard prerequisite of D3 shipping"
    (§4.3.1's `no_followup` counter) was specified against a hook that could only ever read zero.
    §1.1 now documents both mechanisms; §3.3 is rewritten as an explicit decision (**permanent for
    the session — accepted, with the real bound stated**); §4.3.1(a)'s counter is redesigned around a
    hook that can actually fire.
    **(2)** the loaded-tools bucket is keyed by session id with **no agent component**, and
    `switch_agent` (D4) changes the active agent *within* a session — so D3's headline "71% invisible
    by default" was not per-agent and eroded on every handoff. **Decided: scope the bucket to
    (agent, session)** — new **§4.6**.
    **r4's own blanket grep found five further gaps beyond the review's named locations**, including
    one that would have bricked tool discovery on every upgraded install (§5.3 item 5 — the policy
    repair backfills unknown names to **`deny`**, so an un-ordered migration silently denies
    `ToolSearch` on every agent). Per-finding map in **§15**. All three prior review files are
    retained unmodified and none is superseded.
  - **r3** (2026-08-27) — revised in response to the **second** adversarial review at
    [`ADR-071-tool-manifest-tier-redesign-r2-review.md`](ADR-071-tool-manifest-tier-redesign-r2-review.md)
    (verdict REVISE; 0 CRITICAL, 4 MAJOR, 3 MINOR, 3 OBSERVATION). That review re-derived r2's
    central factual claims from source — the 89-tool arithmetic name-by-name, both `websocket.go`
    line numbers, `ToolExecEndPayload`'s field list, both `Execute` bodies, `SnapshotSearchableTools`,
    `SanitizeToolName`, and the Anthropic/OpenAI/Bedrock `CacheControl` handling — and every one held
    up. Its findings are net-new. Per-finding map in **§14**; both prior review files are retained
    unmodified as historical records and neither is superseded.
    **r3 also acts on the review's own closing instruction** — that §5.2.2's completeness claim
    "should be re-verified again after the fixes below are applied, not assumed fixed by inspection
    alone." A blanket `grep -rn 'hand_off\|return_to_default\|load_tool'` across `pkg/`, `src/`,
    `tests/`, `contracts/` and `docs/` was re-run from scratch for r3 and cross-checked hit-by-hit
    against the tables below. **It found four further gaps the r2 review did not**, all on the D1
    (`load_tool`) axis the review did not grep: a **contract file** (§9), a **user-visible SPA
    regression** (§2.1), a **tautological false-green test** (§2.1), and a **production
    double-registration guard** (§2.1) — plus two factual corrections to r2's own text (§5.2's
    "silently", and §5.2.2's claim about `pkg/gateway/replay.go`, which was **wrong**). The pattern
    is recorded honestly in §2.1's preamble: r1 and r2 both scoped D4 carefully and left D1 as a
    two-line prose scope, and D1 turns out to have the larger blast radius of the two.
  - **r2** (2026-08-27) — revised in response to the adversarial review at
    [`ADR-071-tool-manifest-tier-redesign-review.md`](ADR-071-tool-manifest-tier-redesign-review.md)
    (verdict REVISE; 1 CRITICAL, 6 MAJOR, 5 MINOR, 3 OBSERVATION). That review independently
    re-verified r1's code citations against `release/v0.1.1` @ `aa97bcea` and found the tier
    arithmetic, TTL mechanics and `SnapshotSearchableTools` break-risk all correct — **the "TTL
    mechanics" half of that verdict was itself wrong, and r4 corrects it**: the r1 review confirmed
    that `PromoteTools`/`TickTTL`/`GetAll` behave as described, which they do, without noticing that
    the entire path is gated on `!entry.IsCore` and therefore never touches a static tool. Two
    reviews and three revisions read the same three functions and none asked whether the tools this
    ADR is about are core. See §1.1's boxed note; its findings
    concentrate on D4 being under-specified. Every finding is resolved in this revision — the
    per-finding map is in **§13**. The review file is retained as a historical record and is **not**
    superseded: read it for the reasoning behind each change.
    Three r1 claims were **wrong** and are corrected here, not merely expanded: §5.1's
    "`context` … preserved unchanged" (it was `required`, r1 marked it optional), §5.2's
    `websocket.go` blast radius (it named one of **two** exact-string branches), and §5.2's
    prescribed fix for that branch (inspecting `switch_agent`'s `target` at the gateway call site is
    **not implementable** — see §5.2.2).
- **Deciders:** Operator (Daniel Piatkowski) — all tier placements and tool-identity decisions.
  Architect — mechanism, thresholds, migration surface.
- **Release-phase routing:** **out of band.** This is tool-exposure / agent-DX infrastructure. It
  is not v0.1 (`feature/iframe-preview-tier13` stabilization), not v0.2 (pentest quick wins,
  [#155](https://github.com/elicify-ai/omnipus/issues/155)), and not v0.3 (Workspaces redesign,
  [#156](https://github.com/elicify-ai/omnipus/issues/156)). Per CLAUDE.md's routing rule this is
  the "flag the scope question" case, and the answer is: it neither blocks nor is blocked by any of
  the three phases. It touches `pkg/tools`, `pkg/agent`'s turn-build path, `pkg/coreagent`'s seeds
  and `pkg/config`'s defaults — none of which the three phases contend for. It should ship on its
  own branch and merge whenever it is green.
- **Related, all still in force:** [ADR-036](ADR-036-consolidate-shell-and-subagent-tools.md) (one
  tool per capability; §3.6's policy-key migration is the precedent D4 follows verbatim);
  [ADR-053](ADR-053-unified-goal-plan-subagent.md) (why `delegate` must be as visible as the task
  tools — Tier 1 keeps it); [ADR-040](ADR-040-fr-h-006-nested-delegation-reversal.md) (the
  `CloneExcept` exclusion D4 must rename); [ADR-066](ADR-066-context-budget-and-tool-result-routing.md)
  (the tool surface is part of the context budget — `estimateToolSurfaceTokens` already warns when
  it exceeds half the window); `docs/internal/design/tool-manifest-optimization-2026-06.md` (the
  original two-tier design — **this ADR supersedes its tier table and its §"Classification"**).
- **Related, explicitly OUT of scope:**
  [#653](https://github.com/elicify-ai/omnipus/issues/653) (remove `library_list`/`library_read`),
  [#654](https://github.com/elicify-ai/omnipus/issues/654) (broad per-provider prompt-caching audit).
  Both are referenced below and neither is implemented here.
- **Evidence level:** every code claim in §1 was read in this session at `release/v0.1.1` @
  `aa97bcea` and is cited `file::symbol` (CLAUDE.md's rule — `loop.go`/`turn.go` line numbers go
  stale). Claims about the Anthropic API's caching semantics are marked **[EXTERNAL]** and sourced
  from the bundled `claude-api` reference, not from this codebase. r1's two **[UNVERIFIED]** items
  were both checked during the r2 revision and are now **resolved** — see §12.
  **No claim in this document is currently unverified — but "verified" has now twice meant "verified
  where someone looked."** r2 opened with the same assurance and still carried three factually wrong
  claims (§7's "Corrections" list) plus an incomplete surface table. r3 corrects those and re-derives
  the tables by fresh grep, and the honest statement of confidence is: the code citations here have
  survived two independent adversarial re-derivations from source, and the *completeness* claims have
  failed one each time they were made. §10's blanket-grep step is therefore an instruction to
  re-verify, not a record that verification is finished.

---

## 1. Context

### 1.1 What exists today (verified)

Omnipus already ships a two-tier tool-exposure mechanism, gated on
`cfg.Tools.Manifest.Compressed` (`pkg/config/config.go::ManifestConfig`, default `true`):

| Tier | Mechanism | Count |
|---|---|---|
| **Full** (`ManifestFull`) | Sent as complete callable defs every turn | 18 names, hardcoded in `pkg/tools/manifest.go::fullManifestToolNames` |
| **Lazy** (`ManifestLazy`) | One line of `name — description` in a text block injected into the system context every turn; callable only after `load_tool` promotes it | everything else |
| **Infra** (`ManifestInfra`) | Always callable, never in the block | `load_tool` only |

The moving parts, all confirmed by reading:

- `pkg/tools/manifest.go::ToolManifestTier` is the single classification authority. It is a pure
  name lookup with **no per-tool state** and no notion of visibility separate from tier.
- `pkg/tools/manifest.go::BuildCompressedManifest` renders the block. It includes **every**
  `ManifestLazy` tool that is not already loaded, grouped by `Category()`, with no
  visibility sub-distinction — this is the exact place the third tier has to be introduced.
- `pkg/agent/tool_manifest.go::injectManifestNote` inserts the block as a **separate
  `role: "system"` message at index 1**, with its text in `Message.Content` — *not* as a
  `ContentBlock` in `messages[0].SystemParts`. This matters for D5 (§6).
- `pkg/tools/tools_tool.go::ToolsTool` is `load_tool`. `names` loads by exact name; `query` runs
  BM25 over the searchable corpus and auto-loads **the single top-scoring loadable hit**
  (`execSearchAndLoad` walks the ranked list only to skip policy-denied entries, then `break`s on
  the first success).
- **There are TWO independent promotion mechanisms, and only one of them decays.** This is the
  single most load-bearing correction in r4 — see the boxed note in §1.1.1 immediately below.
  `MaxSearchResults` defaults to 5.
- `pkg/coreagent/core.go::allStaticToolNames` is the hardcoded 90-name static catalog. It is
  load-bearing three times over: `validateOverrideKeys` **panics** on a seed override naming a tool
  absent from it; `denyAllThenOverride` emits one literal policy entry per name; and a test asserts
  it stays one-for-one with `config.DefaultConfig().Sandbox.ToolPolicies`. A boot-time validator
  (`config.ValidateToolPolicyCoverage`, plus a gateway-side drift check) aborts boot on any
  `agent × tool` coverage gap. This is CLAUDE.md Hard Constraint #6 in mechanical form.

### 1.1.1 The two promotion mechanisms — READ THIS BEFORE REASONING ABOUT COST

> **r1, r2 and r3 all got this wrong in the same way, and three sections were built on the error.**
> Recorded at the top of the mechanism section, in full, so no future revision repeats it.

A tool becomes callable after a `ToolSearch` load by **one of two entirely separate mechanisms**,
selected by whether the tool is core (static) or non-core (MCP). The two behave differently in the
one respect this ADR keeps reasoning about — **whether the promotion ever goes away**.

| | **Static / builtin tools** (all 89, i.e. everything this ADR is about) | **MCP tools** |
|---|---|---|
| Registered by | `ToolRegistry.Register` → `IsCore: true` | `RegisterHidden`/`RegisterHiddenMCP` → `IsCore: false` |
| What makes it callable | `pkg/agent/loop.go::markToolsLoaded` sets `al.loadedTools[sessionID][name] = true` | `pkg/tools/registry.go::PromoteTools` stamps `entry.TTL` |
| Read by | `buildCompressedToolDefs` / `buildToolManifestNote` gate on `loaded[name]` (`pkg/agent/tool_manifest.go`) | `GetAll`/`Get`/`ToProviderDefs` admit on `IsCore \|\| TTL > 0` |
| **Decays?** | **NO.** No TTL, no cap, no eviction | **Yes.** `TickTTL` decrements once per turn |
| Removed when | `forgetSession` on **session close** only (`pkg/agent/session_end.go`) | `TTL` reaches 0 |

The two are wired together at one call site and that is what made the conflation easy to miss:
`execLoad`/`execSearchAndLoad` (`pkg/tools/tools_tool.go`) call `PromoteTools` **and** `markLoaded`
on every load, and the file's own comment at the call site says why — *"PromoteTools is a no-op for
already-visible tools… markLoaded is harmless for already-promoted tools. Together they make both
hidden-MCP and in-process-lazy tools callable."* Reading only that line, a `ToolSearch` promotion
looks TTL-governed. It is not, for any static tool:

- `PromoteTools` mutates `entry.TTL` **only `if !entry.IsCore`** (`pkg/tools/registry.go`). For a
  static tool it is a literal no-op.
- `TickTTL` decrements **only `if !entry.IsCore && entry.TTL > 0`**. Same.
- `GetAll` admits an entry when `entry.IsCore || entry.TTL > 0` — a static tool is admitted
  **unconditionally**, whatever its TTL says.

So the registry TTL — default 5, `cfg.Tools.MCP.Discovery.TTL` — governs MCP discovery and nothing
else. Its config key says so; the name simply stopped being read literally.

**The consequence that matters: once a static Tier 2 or Tier 3 tool is loaded, its full callable
schema stays in the array for the REST OF THE SESSION.** Not five turns. §3.3 states the decision
this forces; §4.3.1(a) redesigns the counter that was specified against the wrong hook; §6.4 records
the one place where the correction lands in the design's *favour*.

**One thing that already said this correctly, and is worth crediting rather than quietly reconciling:**
§3.5's rejection of collapsing search-and-execute rests on *"a loaded tool stays loaded for the rest
of the session, so every subsequent use already costs nothing extra."* That sentence has been right
since r1 and it directly contradicted §3.3 two paragraphs earlier. Both survived three revisions and
two adversarial reviews. When one document says two incompatible things about the same mechanism,
the reviewable failure is not that one of them is wrong — it is that nobody noticed they disagreed.

### 1.2 Findings from the 2026-08-26 review

The review compared this design against the equivalent mechanism in another agentic coding harness.
Five findings; two of them needed correcting before they could be acted on.

**F1 — `bash` sat in Tier 1 next to narrower tools that sat in Tier 2.** `bash` is in
`fullManifestToolNames` today; `append_file`, `serve_web` and similar single-purpose tools are lazy.
A universal, zero-friction shell tool that is *always* visible, competing against purpose-built
tools that each cost a discovery round trip, is a gradient pointing the wrong way — the model
reaches for what it can see. This is the same failure mode ADR-053 measured for `delegate` vs.
`create_task` (304 s vs. 20–80 s) and fixed by *promoting* `delegate`; here the fix is the mirror
image, demoting `bash`.

**F2 — a wrong top BM25 guess costs a full round trip with no partial mitigation.**
`execSearchAndLoad` loads exactly one tool. If the top hit is wrong the model must issue a second
`load_tool` call before it can do any work. The ranked results already carry a
`Score float32` (`pkg/utils/bm25.go`) which `execSearchAndLoad` **currently discards** — it reads
only `r.Document`. The information needed to detect an ambiguous result is already in hand and
thrown away.

**F3 — the manifest block is re-sent, uncached, every turn. Real, but the "unbounded growth"
framing is wrong.** The per-turn cost is real: the block is rebuilt each turn and injected as a
plain system message with no `cache_control`, deliberately, per the 2026-06 design doc's own
reasoning ("never double-counted into the cached system prompt"). But it does **not** grow with
connected MCP servers. MCP tools are registered via `RegisterHidden` (non-core, `TTL 0`), and
`GetAll` admits an entry only when `entry.IsCore || entry.TTL > 0` — so hidden MCP tools never
reach `BuildCompressedManifest`'s input at all, and a *promoted* MCP tool is excluded again by the
`loaded[n]` check. The block is bounded by the **static** lazy catalog: ~71 tools. The cost is a
fixed recurring tax, not a scaling one. D5 addresses the tax; nothing here needs to address
unbounded growth, because there isn't any.

**F4 — `library_list`/`library_read` are path-scoped facades over the same machinery as
`list_directory`/`read_file`.** Added to fix a discoverability bug (the agent could not find
chat-uploaded files because it did not know about the workspace's `.library/` folder). Documenting
`.library/` in `read_file`/`list_directory`'s own descriptions would have solved that without two
extra tools. **Tracked separately as [#653](https://github.com/elicify-ai/omnipus/issues/653) and
deliberately not implemented here** — both names stay in the catalog and in Tier 3 below, so this
ADR and #653 can land in either order without conflicting.

**F5 — `append_file` vs. `edit_file`: reviewed, NOT redundant, no change.** Recorded so it is not
re-litigated. `edit_file` requires an exact `old_text` match, which means the agent must read the
file first; `append_file` appends blind with no prior read. For the common "add a line to a log /
notes file" shape that is one call instead of two, and it is robust to a file whose tail the agent
has not seen. Different preconditions, not a duplicate capability. It moves to Tier 3 for
frequency reasons only, not because it is redundant.

### 1.3 What this ADR decides

Five decisions: **D1** rename `load_tool`; **D2** widen its ambiguous-search behavior; **D3**
introduce a third, search-only visibility tier; **D4** merge `hand_off` + `return_to_default` into
`switch_agent`; **D5** move the static part of the catalog inside a prompt-cache boundary.

---

## 2. D1 — `load_tool` is renamed `ToolSearch`

Same tool, same mechanics, same parameters. The name changes to match the equivalent tool in the
harness this design was compared against, for operator familiarity across the two systems.

### 2.1 D1's blast radius — larger than D4's, and under-scoped in both r1 and r2

**Read this section's history before trusting any table in this document.** r1 and r2 both gave D1
a two-sentence prose scope (`ToolsTool.Name()`, `infraManifestToolNames`, the `allStaticToolNames`
entry, the `defaults.go` ceiling, the seed maps, `BuildCompressedManifest`'s header prose, the
`execLoad`/`execSearchAndLoad` error strings, and `src/lib/humanizeToolName.ts`) while spending
§5.2's several hundred lines on D4. Both adversarial reviews then scrutinised D4 and neither grepped
`load_tool`. The r3 blanket grep shows the asymmetry was backwards: **D1 touches more files than D4,
including a contract file, a user-visible SPA behaviour, and a test that cannot fail.** The list
below is the corrected scope. Every row was produced by `grep -rn 'load_tool'` over `pkg/`, `src/`,
`tests/`, `contracts/` and `docs/` at `release/v0.1.1` @ `aa97bcea` and read individually — this is
a mechanically-derived list, not a recalled one.

#### The four functional sites a compiler will not catch

**(a) `src/lib/toolVisibility.ts` — the fail-open direction is the OPPOSITE of D4's, and it is a
user-visible regression.** §5.2.2 reasons carefully about `toolVisibility.ts`'s `default:` arm for
D4 and concludes it "fails open into the correct outcome for `switch_agent` — visible, which is what
is wanted." That reasoning is right for D4 and **inverts for D1**, which neither r1 nor r2 checked.
Two sites:

- `shouldRenderToolCall`'s `case 'load_tool': return isError` (`src/lib/toolVisibility.ts`)
- `shouldRenderToolCallInPanel`'s `return verboseChatEnabled || tool !== 'load_tool'`

Rename the backend and leave these, and `ToolSearch` matches neither — it falls through to
`default: return true` in the first and to the `!==` in the second. **Every `ToolSearch` call becomes
visible in every chat thread and every ActivityPanel row, for every user, by default.** That
directly reverses CLAUDE.md's own documented UI rule (`load_tool` is the first-named member of the
"closed, narrow set of infra-only calls with no standalone meaning to a reader" that is hidden by
default) and it is the noisiest possible tool to un-hide, since D3 makes 71% of the catalog reachable
only through it. No error, no crash, and — see (c) — no failing test. **Both sites must be renamed in
the same commit as the backend.**

**(b) `src/test/canonicalToolNames.test.ts` — a canonical-name guard whose core assertion cannot
fail.** This file exists specifically to pin the loader tool's canonical name across renames (it was
written for the 2026-06-26 `tools` → `load_tool` rename). Its assertion is
`const canonical = 'load_tool'` followed by `expect(canonical).toBe('load_tool')` — a tautology over
a local variable, with no reference to any production symbol. It passes identically whether the tool
is called `load_tool`, `ToolSearch`, or nothing at all. Post-D1 it stays green while purporting to
guard a name that no longer exists: precisely the shape
[`docs/internal/false-green-patterns.md`](../false-green-patterns.md) catalogues, in the one test
written to prevent this exact class of drift. **Required in W-D1:** rename it *and* re-point the
assertion at a real imported symbol (the `EXPLICIT_LABELS` key in `humanizeToolName.ts`, or the
`toolVisibility.ts` case), so the guard can actually fail the next time.

**(c) `pkg/agent/loop.go`'s double-registration guard uses a raw literal.** The registration block
guards with `if _, alreadyTools := agent.Tools.Get("load_tool"); !alreadyTools` — a hardcoded string,
not `tools.InfraManifestToolNames()`. Post-rename the lookup never matches, so the guard silently
stops guarding and the tool is re-registered on every path that reaches this block (including a live
`Compressed` false→true `SwapConfig` toggle, which is the exact scenario the guard's own comment says
it exists for). **Fix by deriving from `tools.InfraManifestToolNames()`** rather than re-hardcoding
the new literal — the same "derive, don't re-copy" instruction §5.2.2 gives for `subturn.go`.

**(d) `contracts/components/schemas/AgentToolEntry.yaml` — a contract file names `load_tool`
twice.** Full treatment in §9. Constraint #8's 5-step process applies; this is a second contract
edit for D1, where r1/r2 recorded only D4's `AgentSwitchedFrame`.

#### Full D1 surface

| Area | Sites |
|---|---|
| Tool identity | `pkg/tools/tools_tool.go::ToolsTool.Name()` |
| Classifier | `pkg/tools/manifest.go::infraManifestToolNames` (the map key; `InfraManifestToolNames()`'s doc comment names the literal too) |
| Catalog | `pkg/coreagent/core.go::allStaticToolNames` |
| Global ceiling | `pkg/config/defaults.go` (`sandbox.tool_policies`) |
| Per-agent seeds | `pkg/coreagent/core.go` — the seed policy maps |
| **Production literal** | **`pkg/agent/loop.go`'s `agent.Tools.Get("load_tool")` registration guard — see (c); derive from `InfraManifestToolNames()`** |
| Model-facing prose | `pkg/tools/manifest.go::BuildCompressedManifest`'s block header ("call `load_tool` with its exact name…") **and the identical string in `pkg/tools/tools_tool.go`'s `execSearchAndLoad` success preamble** — the latter is a *result* string, not an error string, so r1/r2's "error strings in `execLoad`/`execSearchAndLoad`" scope did not cover it |
| Error strings | `pkg/tools/tools_tool.go` — `execLoad`/`execSearchAndLoad` (~12 sites, all `load_tool(...)`-prefixed) |
| **Contracts** | **`contracts/components/schemas/AgentToolEntry.yaml` (2 sites in `manifest_tier`'s description) → regenerates `pkg/api/generated/openapi_types.gen.go` (4 sites), `src/lib/api/generated/openapi-types.ts`, and the `pkg/gateway/inboundschemas/` mirror. See §9** |
| **SPA (behavioural)** | **`src/lib/toolVisibility.ts` — `shouldRenderToolCall`'s `case 'load_tool'` AND `shouldRenderToolCallInPanel`'s `tool !== 'load_tool'`. See (a)** |
| SPA (labels) | `src/lib/humanizeToolName.ts` — the `load_tool: 'Find & load tools'` entry in `EXPLICIT_LABELS`; keep the legacy key as an alias for old transcripts and add `ToolSearch` |
| Comments / doc comments | `pkg/tools/fuzzy.go`, `pkg/tools/compositor.go` (6), `pkg/tools/general_builtin_catalog.go`, `pkg/agent/tool_manifest.go` (8), `pkg/agent/loop.go` (~10), `pkg/agent/loop_mcp.go`, `pkg/agent/subturn.go` (7 — see §5.2.2), `pkg/gateway/websocket.go` (2), `pkg/config/config.go` (2), plus the SPA's `ToolCallBadge.tsx`, `ChatScreen.tsx`, `GenericToolCall.tsx`, `ActivityPanel.tsx`. Non-functional, in scope for §10's blanket pass |
| **Tests (Go, literal `"load_tool"`)** | `pkg/tools/manifest_test.go`, `pkg/tools/load_tool_f4_test.go`, `pkg/tools/load_tool_test.go`, `pkg/tools/effective_tool_policy_test.go`, `pkg/agent/tool_manifest_test.go`, `pkg/agent/load_tool_e2e_test.go`, `pkg/agent/load_tool_repro_test.go`, `pkg/agent/subturn_delegate_nesting_test.go`, `pkg/agent/subturn_transcript_nesting_test.go`, `pkg/gateway/ws_approval_test.go`, `pkg/gateway/ws_approval_parity_test.go`, `pkg/gateway/rest_tool_registry_test.go` — **12 files** |
| **Tests (TS, literal `'load_tool'`)** | **`src/test/canonicalToolNames.test.ts` (see (b) — assertion must be re-pointed, not merely renamed)**, `src/lib/toolVisibility.test.ts`, `src/components/chat/ToolCallBadge.test.tsx`, `src/components/chat/ActivityPanel.test.tsx`, `src/components/chat/tools/GenericToolCall.test.tsx`, `src/components/chat/ChatScreen.f4-ghost-thinking-indicator.test.tsx`, `src/store/chat.delegate-text-boundary.test.ts` — **7 files** |
| Tests (comment-only) | `src/lib/toolVisibility.registry.test.ts`, `src/components/chat/SubagentBlock.test.tsx`, `pkg/tools/scope_catalog_test.go` |

**The Go tests fail loudly; the TS tests do not, and that asymmetry is the whole risk.** The 12 Go
files assert against registry lookups and policy maps that stop resolving — CI catches them. The 7 TS
files pass the string `'load_tool'` *as an argument* into `shouldRenderToolCall` /
`shouldRenderToolCallInPanel` / a `ToolCallBadge` prop. If `toolVisibility.ts` is left unrenamed
(the (a) regression), every one of those tests keeps passing — they are still exercising a real,
still-present `case 'load_tool'` arm, just one no tool reaches any more. **So the (a) regression ships
green with a fully green SPA suite.** Required in W-D1: rename `toolVisibility.ts`'s two sites and
add a positive `ToolSearch`-is-hidden-by-default case to `src/lib/toolVisibility.test.ts`, asserted
directly rather than inherited from a fallback — the same instruction §5.2.2 already gives for D4's
`switch_agent`-is-visible case, applied to the direction that actually regresses.

**`ToolSearch` is the one tool name in this codebase that is not `snake_case`.** Every other tool
is (`read_file`, `browser_open_tab`, `create_task_in_workspace`). Accepting the inconsistency is the
operator's call and is defensible — it is an infra tool, never in the manifest block, and the visual
distinction from the catalog it searches is arguably a feature. Recorded explicitly so a future
reviewer does not "fix" it.

**Mixed case is safe — verified, not assumed** (r1 left this open in §12; the review was right that
a self-identified two-minute check should not survive to ratification). `SanitizeToolName`
(`pkg/tools/registry.go`) is `strings.ReplaceAll(name, ".", "_")` — it rewrites dots and nothing
else, and never touches case. Its neighbouring comment states the constraint it exists to satisfy:
Anthropic/Azure require `^[a-zA-Z0-9_-]{1,128}$`, which admits mixed case explicitly.
`pkg/tools/fuzzy.go`'s lowercasing is confined to fuzzy-match *suggestion* text and never reaches
the canonical registry lookup. **D1 is unblocked on this axis.**

The same migration obligation as D4 applies: a persisted `load_tool` policy key must be rewritten,
not silently orphaned (§5.3).

---

## 3. D2 — `ToolSearch` promotes the ambiguous band, not just the top hit

### 3.1 Decision

On the `query` path, compute the ambiguity of the ranked, **policy-loadable** result set and branch:

- **Confident top hit → unchanged.** Auto-load exactly one tool, exactly as today. This is the
  common case and it must not regress.
- **Ambiguous → auto-load the top 2–3.** Return their full schemas together, so the model can pick
  the right one and call it without a second round trip.

### 3.2 The ambiguity test, precisely

Let `s₁ ≥ s₂ ≥ …` be the BM25 scores of the ranked results **after** the existing `canLoad` filter
(computing the band over the raw ranked list would let a policy-denied high scorer distort the
ratio for tools the agent can actually use). Candidate `i` (`i ≥ 2`) enters the promotion set when
**either**:

1. **Score band:** `sᵢ ≥ 0.80 × s₁`, or
2. **Cross-category near-miss:** `sᵢ ≥ 0.50 × s₁` **and** `toolᵢ.Category() != tool₁.Category()`.

Promote `tool₁` plus every candidate that qualifies, capped at **3 total**. If no candidate
qualifies, promote `tool₁` alone — today's behavior, byte for byte.

**The two degenerate ends of the set, stated rather than left to inference:**

- **Fewer than 2 entries after the `canLoad` filter.** No ambiguity test runs at all — there is no
  `i = 2` to evaluate. `tool₁`, if there is one, is promoted alone; an empty filtered set is the
  existing "nothing loadable matched" path and is unchanged. (This is implied by "if no candidate
  qualifies" above, but a reader had to infer that an *empty* candidate set counts as "none
  qualified", and an implementer should not have to.)
- **More qualifying candidates than the cap admits.** When 3 or more candidates beyond `tool₁`
  qualify, **rank order breaks the tie**: take the two highest-scoring qualifiers in the order the
  ranked list already presents them. The input is rank-sorted before the filter, so this needs no
  extra sort — but it is stated because "capped at 3" alone does not say *which* 3, and a reader
  could reasonably guess category diversity or score band instead.

**Implementation consequence: the `canLoad` loop changes from short-circuit to full-scan.** Today
`execSearchAndLoad` walks the ranked `matches` list and `break`s on the first candidate that passes
`canLoad`. The ambiguity test above is defined over "the ranked, **policy-loadable** result set",
which cannot be built without evaluating `canLoad` against *every* ranked candidate up to
`MaxSearchResults` (default 5). That is a real control-flow change to today's loop, called out here
because the algorithm description above does not imply it. Risk is low — `canLoad` is a policy
lookup with no side effects (it resolves the caller via `al.registry.GetAgent` and reads the agent's
policy map) — and the bound is a small number of lookups per search, not an unbounded scan. But an
implementer should change the loop deliberately rather than discover mid-implementation that the
`break` has to go.

**r5: that loop is now built by §3.2.2, not by D2.** §3.2.2 (CRIT-201) requires the *same*
short-circuit→full-scan change, for a different and more urgent reason — the match list itself must
be policy-filtered before it is returned. Rather than have two workstreams rewrite one loop, **W3
builds the filtered loadable list and W4 computes the ambiguity band on top of it** (§10). D2's net
change then becomes purely the ratio test and the multi-promote, over a list §3.2.2 has already
produced. This also removes the only textual conflict W3 and W4 had inside `execSearchAndLoad`.

**Schema-exposure widening: considered, bounded, and accepted — with one narrowing.** Today one
`ToolSearch(query)` puts at most one tool's full schema into context. After D2 an ambiguous query
can put up to three there, and the cross-category clause is *specifically designed* to fire when a
query straddles two kinds of tool. Tier 3 (§4.1) contains destructive verbs — `delete_agent`,
`delete_workspace`, `delete_task`, `disable_channel`, `remove_mcp_server`, `set_config`. So a
plausible-sounding query can surface a destructive tool's callable schema without the model, or an
instruction injected later into the same context, ever having named that tool.

This is **not** a privilege-escalation path: every promoted candidate passes `canLoad` first, so
nothing enters the callable set that the calling agent's own policy would not already have allowed
on an exact-name request, and execution still runs the full policy gate (including `ask`). The
delta is *reachability of the schema*, not of the capability. Recorded rather than left silent
because §3.5 already rejects execute-on-search partly on an argument about what a free-text search
call should be allowed to put into play, and this is the same axis.

**One narrowing is adopted, cheaply:** the **cross-category clause** (rule 2, the 0.50 floor) does
**not** promote a candidate whose resolved policy is `ask` or **whose name is in the explicit
`administrativeToolNames` set defined in §3.2.1**. (r1–r4 wrote this as *"whose category is
administrative (`delete_*`/`remove_*`/`disable_*`/`set_config`)"*. That phrasing named two
mechanisms that do not exist — `Tool.Category()` never returns `administrative`, and there is no
prefix predicate anywhere in `pkg/tools`. §3.2.1 replaces it with a real, drift-tested one.)
The tighter score-band clause (rule 1, 0.80) is
unrestricted — a candidate scoring within 20% of the top hit is genuinely a plausible reading of the
query, and excluding it would defeat the feature. Rule 2 is the speculative one (it fires at half
the top score, on a category-boundary heuristic), so it is the one that carries the narrowing.
Cost: one extra predicate in the candidate filter. This keeps the "seemingly benign query surfaces
`delete_workspace`" case out of the *speculative* band while leaving the confident band alone.

Proposed as unexported constants in `pkg/tools` (`searchAmbiguityRatio = 0.80`,
`searchCrossCategoryRatio = 0.50`, `searchMaxAutoLoad = 3`), **not** new config keys — this is a
tuning parameter for a heuristic, not an operator-facing policy, and CLAUDE.md Constraint #1's
spirit (no gratuitous surface) applies to config as much as to dependencies.

**Why a ratio and not an absolute threshold.** BM25 scores are unnormalized and depend on corpus
size, term rarity, and document length. An absolute cutoff would mean something different for a
12-tool agent than for a 71-tool one, and would drift every time a tool description is edited. The
ratio to the top score is the only formulation stable across corpora.

**Why the second, cross-category clause exists.** Score proximity alone misses the case the review
actually complained about: a query like *"send a file to the user"* scoring `send_file`
(communication) at 9.1 and `write_file` (filesystem) at 5.2 is not "close" numerically, but the
second is a plausible enough reading of the query that a wrong first guess is expensive. A
category boundary between the top two is independent evidence that the query was underdetermined.
The tighter 0.50 floor keeps it from firing on a genuinely dominant hit.

Both ratios are starting values chosen by reasoning, not by measurement — still open at ratification
(§11, "Still open" item 3), with tuning deferred until §4.3.1's counters produce data.

### 3.2.1 The "administrative" exclusion: canonical source, and a drift test — DECIDED (r5)

r4 wrote the narrowing above as *"whose category is administrative"* with a parenthetical prefix
list, and named neither a mechanism nor a drift test. The r4 review was right on both counts, and
the first half is worse than ambiguous — it is **factually unimplementable**.

**`Tool.Category()` cannot express this, verified.** `ToolCategory` (`pkg/tools/base.go`) is a
closed **domain** enum: `filesystem`, `shell`, `web`, `browser`, `communication`, `delegation`,
`memory`, `tasks`, `skills`, `tool_discovery`, `agents`, `workspaces`, `channels`, `providers`,
`platform`, `mcp`, plus the legacy `core`/`system`. There is no `administrative` value, and adding
one would not help, because **the destructive tools share a category with their read-only
siblings** — checked name by name in `pkg/sysagent/tools/category.go`:

| Destructive tool | Its category | Benign siblings in the same category |
|---|---|---|
| `delete_agent` | `CategoryAgents` | `list_agents`, `read_agent_metadata` |
| `set_config` | `CategoryPlatform` | `get_config`, `run_doctor`, `get_usage` |
| `disable_channel` | `CategoryChannels` | `list_channels`, `test_channel` |
| `remove_mcp_server` | `CategoryMCP` | `list_mcp_servers` |

A category-based narrowing would therefore exclude every *read* tool in four whole domains from the
cross-category band — gutting rule 2 exactly where it is most useful — and would still miss a
destructive tool whose domain siblings happen to be benign. It is the wrong axis, not a
mis-specified one.

**A name-prefix predicate is the wrong fix too**, and for the reason the review gives: it is the
drift hazard itself. `revoke_credential`, `purge_workspace_data`, `wipe_session` all escape
`delete_*`/`remove_*`/`disable_*`, silently, at the moment they are added.

**Decided: an explicit name set with the same drift discipline as `previewedLazyToolNames`.**
`administrativeToolNames` lives in `pkg/tools/manifest.go`, unexported, directly adjacent to
`previewedLazyToolNames` — deliberately, so the two sets that a new tool must be adjudicated
against sit in one place and are read together.

**Membership rule (so the set is decidable, not a taste judgement).** A Tier 3 tool belongs in
`administrativeToolNames` when invoking it **destroys or overwrites state that no other tool in the
catalog can reconstruct, or alters install-wide configuration.** Seed, applying that rule to the
current §4.1 Tier 3 list — **13 names**:

`delete_agent`, `delete_task`, `delete_task_in_workspace`, `delete_workspace`, `remove_mcp_server`,
`remove_skill`, `disable_channel`, `enable_channel`, `add_mcp_server`, `configure_channel`,
`configure_provider`, `set_config`, `stop_plan`.

**Deliberately left out of the seed, and named so the omission is a decision rather than an
oversight:** the in-place *update* verbs (`update_agent`, `update_workspace`,
`write_agent_metadata`, `edit_skill`), the *create* verbs (`create_agent`, `create_workspace`,
`install_skill`), the execution verbs (`execute_plan`, `run_task`), and the irreversible-external
communication verbs (`send_email`, `reply`). Each has a real argument for inclusion — the last pair
especially, since an email cannot be unsent. The **mechanism** is the architect's call and is
decided here; the **membership of these 11 borderline names** is a product call in the same genre as
§4.1's tier placements, and is filed as an operator question at ratification (§11, "Still open"
item 8). The narrowing works with the 13-name seed either way; adding a name only narrows rule 2
further.

**Required drift test — `TestAdministrativeToolNames_Drift`**, mirroring §4.4's pattern and the
existing `TestCatalog_MatchesGlobalCeilingEntryForEntry`:

1. `administrativeToolNames` is **exactly** the documented set (count and contents), so a silent
   addition or deletion fails the build.
2. Every name in it appears in `allStaticToolNames` — this is the assertion that catches a **rename**
   (a D1/D4-shaped change) turning a live exclusion into a dead string with no other symptom.
3. Every name in it resolves to `ManifestSearchOnly` (Tier 3). The narrowing is meaningless for a
   Tier 1/2 tool, so promoting one out of Tier 3 must force a re-decision rather than leave a stale
   entry.
4. **Coverage tripwire:** every name in `allStaticToolNames` matching
   `^(delete|remove|disable|purge|wipe|revoke|drop|reset|destroy)_` — or equal to `set_config` — is
   either in `administrativeToolNames` or in an explicit `administrativeExemptNames` set carrying a
   one-line reason. A future `revoke_credential` then fails the build until someone adjudicates it.

**The residual gap, stated rather than papered over.** No test can catch a destructive tool whose
*name* reads benign — assertion 4 is a tripwire, not a definition, and treating it as one would be
the "detector that looks like a mitigation" failure §4.3.1 opens by rejecting. The structural
mitigation is that §4.4's drift test **already** forces a deliberate tier decision on every newly
added static tool; r5 extends that same forced checklist to two questions instead of one —
*which tier?* and *is it administrative?* — asked at the one moment a human is guaranteed to be
looking. That is a process guarantee, and it is the honest one; the regex is the cheap backstop
under it.

### 3.2.2 The match list must be policy-filtered — CRIT-201, and it is a prerequisite of §4.3

**The defect, verified against source.** `pkg/tools/tools_tool.go::execSearchAndLoad` builds

```go
matches := make([]ToolSearchResult, len(ranked))   // {Name, Description} for EVERY ranked hit
for i, r := range ranked { matches[i] = ToolSearchResult{r.Document.Name, r.Document.Description} }
```

**before any `canLoad` call**, and returns it in `resp["matches"]` on the success path and as the
whole payload on the failure path — whose own comment reads `// No resolver or all results denied
by policy.` `canLoad` is applied only to decide which single candidate gets **auto-loaded with a
schema**. The match list is never filtered. `SnapshotSearchableTools` (`pkg/tools/registry.go`)
confirms the corpus is built purely on `!entry.IsCore || ToolManifestTier(name) == ManifestLazy`,
with **no policy input at all**, so a policy-denied tool is in the corpus and can rank.

**Why this matters more after D3 than before it.** Pre-D3, all 71 lazy tools are named in the
manifest block every turn, so an unfiltered match list discloses nothing new — the gap is real but
moot. Post-D3, 63 tools are hidden *specifically* to reduce their visibility, and the mechanism the
ADR relies on to gate that visibility does not respect policy. §4.3's "an agent that does not
already suspect a capability exists has no zero-cost way to discover it" was true of the manifest
block and false of the search path: **any** query, on any topic, could return `delete_workspace`'s
name and full description to an agent whose policy denies it, purely on a BM25 top-5 placement.
§3.2's own risk-acceptance ("every promoted candidate passes `canLoad` first… the delta is
reachability of the schema, not of the capability") is correct about the *loaded set* and does not
apply to the *match list* — two disclosure channels in one tool result, only one of them bounded.

**Decided: filter after ranking. Do not filter the corpus.** Both were considered; the corpus
option is not merely the larger change, it is **wrong on three independent grounds**, each checked
against source:

1. **It is a cross-agent leak — the same class §4.6 exists to fix.** The `canLoad` closure
   (`pkg/agent/loop.go`'s `SetResolver`) resolves the caller **per call**, from
   `tools.ToolAgentID(ctx)`, falling back to the captured agent id. One `ToolsTool` therefore serves
   different calling agents. Baking one agent's policy into the corpus would make the next caller's
   results a function of whoever searched first.
2. **The engine cache cannot express the key.** `getOrBuildEngine` stores a `bm25CachedEngine` keyed
   **only** on `t.registry.Version()`. A policy-filtered corpus would need `(agentID,
   policy-generation)` in that key — and the registry version counter **does not move when a policy
   changes**, so there is no existing signal to invalidate on. That is a net-new invalidation
   problem with a silent-staleness failure mode.
3. **It changes BM25's own scores, and §3.2 already rejected that.** `BM25Engine.Search`
   (`pkg/utils/bm25.go`) computes `N := len(e.corpus)`, the document frequencies `df`, the IDF
   `log((N−df+0.5)/(df+0.5)+1)` and `avgDocLen` from the corpus **on every call**. Remove documents
   and every surviving document's score changes — so `s₁` changes, so §3.2's `sᵢ/s₁` ratios mean
   something different **for every agent**. §3.2's "Why a ratio and not an absolute threshold"
   argues the ratio is "the only formulation stable across corpora"; a per-agent corpus reintroduces
   precisely the instability that paragraph rejects. Filter-after-rank keeps one corpus, one score
   set, and one ratio semantics for every caller.

**The mechanism, specified to implement.** All of it is inside `execSearchAndLoad`; nothing else
changes.

1. **Rank over the whole corpus, not the top `maxSearchResults`.** Replace
   `cached.engine.Search(query, t.maxSearchResults)` with `cached.engine.Search(query,
   cached.docCount)`, adding a `docCount int` field to `bm25CachedEngine` set from `len(docs)` where
   `getOrBuildEngine` already has it. **This costs essentially nothing**, and the reason is in
   `BM25Engine`'s own doc comment — *"All indexing work is performed inside Search() on every call"*:
   the O(N×L) index build happens whether `topK` is 5 or `N`, and `topK` only sizes the final
   min-heap extraction. `Search` already returns only documents that share a query term, so the
   ranked list is normally far shorter than the corpus.
2. **Filter and truncate in one pass, in rank order.** Walk `ranked`; for each hit call
   `canLoad`; append the loadable ones (name, description **and score**, since §3.2 needs the
   scores) to `loadable`; **stop as soon as `loadable` reaches `maxSearchResults`.** A denied tool
   costs one extra policy lookup and never occupies a result slot. Bound the walk at
   `searchCanLoadScanCap = 50` lookups so a large MCP corpus cannot produce an unbounded policy
   scan; if the cap trips, return the loadable hits found so far. **The cap can never leak** — it
   only ever truncates the list earlier, never admits a denied name.
3. **`matches` is then built from `loadable`, not from `ranked`.** Every existing consumer —
   `resp["matches"]`, both `len(matches)` counts in the human-readable preamble, and the
   `json.Marshal(matches)` on the failure path — reads the filtered list with no further change.
4. **An empty filtered set returns the existing "nothing matched" path**, not a listing:
   `SilentResult("No tools found matching the query.")` — the same string the zero-ranked branch
   already emits, so there is no new response shape to design. The current `else` branch that
   prints a full listing when everything was denied is **deleted**.
5. **No resolver ⇒ no disclosure.** With (4), "all denied" is no longer the `else` branch's job, so
   the branch survives only for `t.canLoad == nil`. That case must **fail closed** — return the
   same "No tools found matching the query." rather than an unfiltered list. This is free: the only
   nil-resolver construction in production is `NewToolsTool(nil, 0, 0)`
   (`pkg/tools/general_builtin_catalog.go`), a catalog-shape stub whose nil registry makes
   `execSearchAndLoad` return before it ever reaches here.

**One property falls out for free, and it is worth naming.** §4.3.1(a) defines
`omnipus_toolsearch_zero_result_total` as firing "when the policy-loadable result set is empty."
Pre-fix, that set was never materialised — the counter's definition and the tool's own response
could disagree (a response listing 5 matches while the loadable set was empty). Post-fix the
counter fires on exactly `len(matches) == 0`, so the detector and the disclosure agree by
construction rather than by two implementers making the same judgement call.

**What this does *not* change, stated so the property is not over-claimed.** A Tier 3 tool the
agent's policy **allows** remains discoverable by one search — that is the design, and §4.3 accepts
it explicitly. This fix draws the line at policy, not at tier: **a tool the calling agent could
never load must not be nameable through the channel that exists to find loadable tools.**

**Required tests (four; the first is the r4 review's own dataset gap):**

| # | Case | Assert |
|---|---|---|
| 1 | A query BM25-ranks a policy-`deny` Tier 3 tool inside the top `maxSearchResults` but **not** at rank 1 | The denied tool's **name and description** are both absent from `matches`; the allowed tools around it are present, in the same relative order |
| 2 | Every ranked result is denied | `matches` is empty, the result is the "No tools found matching the query." silent result (**not** a listing), and `omnipus_toolsearch_zero_result_total` increments by exactly 1 **on `GET /metrics`** |
| 3 | **Ranking invariance** | Membership and relative order of the *allowed* results are identical to the pre-filter ranking |
| 4 | `canLoad == nil` | No matches disclosed |

Test 3 is the one that protects the decision rather than the behaviour: it is what fails if a later
optimisation quietly converts this into corpus-exclusion, which is the fix this section rejects on
three grounds and which would otherwise pass tests 1, 2 and 4.

### 3.3 There is no reclamation — static promotions are permanent for the session. DECIDED: accept, and state the real bound

**r1–r3 said: "Reclamation is the existing TTL, unchanged… at most `TTL` turns (default 5), after
which `TickTTL` drops it."** That is false for every tool D2 can promote, for the reason §1.1.1
sets out: `PromoteTools` and `TickTTL` are no-ops on core entries, and every static tool is core.
A promoted static tool leaves the callable set only when the session closes.

**The fork, and the decision.** Either (a) build real decay against the session-level `loadedTools`
map — a genuinely new mechanism, since the registry TTL structurally cannot do this job — or
(b) accept permanent-for-session as the design and correct every claim that assumed otherwise.

**Decided: (b). Accept it. Do not build decay.** Four reasons, in the order that decides it:

1. **Decay would introduce a failure mode that does not exist today.** The model's own context still
   contains the earlier `ToolSearch` result and any successful calls it already made to that tool.
   Silently removing the schema from the callable array while the transcript still shows the tool
   working produces a call to a tool the provider no longer knows about — a hard API error or a
   fabricated call, arriving several turns after the cause, with nothing in the conversation
   explaining it. "The tool I used two turns ago vanished" is a worse agent experience than carrying
   a schema, and it is much harder to diagnose.
2. **Decay would make D5 *worse*, not better.** §6.4 establishes that any change to the callable
   tool array invalidates the whole cached system prefix. Under permanent-for-session, each distinct
   tool costs exactly **one** invalidation per session, at the moment it is loaded. Adding eviction
   would add a **second** invalidation per tool, at an arbitrary later turn, for no benefit the model
   asked for. The mechanism proposed to bound cost would roughly double the cost that actually
   dominates.
3. **The cost is bounded — just not by a turn count.** The ceiling is the agent's own
   policy-allowed lazy catalog: **at most 71 schemas**, which is exactly the tool surface the system
   sent every turn before the manifest optimisation existed and still sends whenever
   `Compressed: false`. This is a monotonically-growing set with a hard, already-tolerated ceiling,
   not an unbounded one. In practice a session loads single digits.
4. **It is what the document already believed where it mattered.** §3.5 rejects search-plus-execute
   *on the strength of* permanent-for-session loading. Building decay now would retroactively
   invalidate a rejection this ADR has already made.

**So the honest cost statement for D2, replacing the "≤5 turns" claim wherever it appeared:** an
ambiguous query promotes up to 2 extra tools whose schemas are then carried for the remainder of the
session. Each such tool costs its schema in every subsequent request **plus one prompt-cache
invalidation at the turn it is loaded** (§6.4). Against that: a wrong top-hit costs a full
request/response round trip *and* the model's re-reasoning, immediately. D2 is still the right
trade — but it is a trade against a session-long cost, not a five-turn one, and that is why §3.2's
narrowing of the speculative cross-category band (excluding `ask`-policy and administrative tools)
carries more weight under the corrected model than it did under the wrong one.

**No new expiry, cleanup, or unload path is built** — that sentence survives r1 unchanged. What
changes is that it is now a decision with reasons, rather than a description of a mechanism that
was assumed to already exist.

**Required test (this is what stops the claim drifting back).** A regression test asserting the
**documented** behaviour: load a static `ManifestLazy` tool for a session, drive more turns than
`cfg.Tools.MCP.Discovery.TTL` (and call `TickTTL` on each), and assert the tool is **still** in
`sessionLoadedTools` and still present in `buildCompressedToolDefs`'s output. The r3 review named
this gap precisely — no test anywhere would currently catch the difference between the two models,
in either direction. Pair it with the mirror assertion for an MCP tool, which **does** decay, so the
test file itself documents the split.

### 3.4 D2 requires no response-shape change — but §3.2.2 removes one branch

`execSearchAndLoad` already builds `resp["loaded"]` as a JSON **array** and `resp["schemas"]` as a
**map**, even though both currently hold exactly one entry. Multi-load changes cardinality, not
shape. Only the human-readable preamble changes (`"Loaded the best match 'X'"` → a form that names
all promoted tools and says why more than one was loaded). This is a tool *result* string, not a
gateway wire type, so Constraint #8 does not apply.

**r5 narrows this heading, because §3.2.2 makes the unqualified version false.** D2 alone changes no
shape — that claim stands exactly as written. But §3.2.2 **deletes** the `else` branch that today
returns a bare `matches` listing when nothing was auto-loaded, folding the "everything was denied"
case into the existing `SilentResult("No tools found matching the query.")` path and failing the
no-resolver case closed the same way. That is a response-shape change, it is on the same function,
and it lands in the same release — so the two claims are reconciled here rather than left to
contradict each other two sections apart. It is still not a Constraint #8 matter for the same reason
the paragraph above gives: this is a tool result string, not a gateway wire type.

### 3.5 REJECTED: collapsing search and execute into one call

Proposed in session and **explicitly rejected**. The idea: for a policy-`allow` tool, let
`ToolSearch` find it *and* invoke it in the same call.

- **The benefit is bounded and one-off.** It saves at most one round trip, once per tool per
  session — a loaded tool stays loaded for the rest of the session, so every subsequent use already
  costs nothing extra. There is no compounding win. *(This clause is correct and always was — it is
  the one place in r1–r3 that described the real mechanism. §1.1.1 and the rewritten §3.3 bring the
  rest of the document into line with it, rather than the reverse.)*
- **The cost is real and recurring.** The model would have to guess the argument shape of a tool
  whose parameter schema it has never seen. Malformed arguments mean a failed or — worse — a
  *wrong-but-successful* execution, and the recovery path (read the error, re-read the schema,
  retry) is strictly more expensive than the two-step flow it was meant to replace.
- **It discards schema-constrained generation.** When a real parameter schema is declared up front,
  the provider's function-calling protocol constrains argument generation against it. Arguments
  invented inside a free-text search call get none of that.
- **It would break the policy model's shape.** A single call would carry two distinct policy
  decisions (may I discover X, may I execute X) resolved against one tool name — precisely the
  "capability fragmented across tool identities" problem ADR-036 exists to prevent, inverted.

Do not re-propose without new evidence that overturns at least the second and third points.

---

## 4. D3 — Three visibility tiers

### 4.1 The tiers

| Tier | Callable defs each turn? | Preview line in the manifest block? | Findable by `ToolSearch`? | Count |
|---|---|---|---|---|
| **1 — Full** | yes | n/a | n/a (already callable) | 17 |
| **2 — Lazy, previewed** | no | **yes** | yes | 8 |
| **3 — Lazy, search-only** *(new)* | no | **no** | yes | 63 |
| Infra | yes | never | n/a | 1 (`ToolSearch`) |

Tier 2 is exactly today's lazy behavior applied to a much narrower set. Tier 3 is new: registered,
policy-governed, and fully loadable by exact name or by query, but **invisible until the agent goes
looking**. Total 17 + 8 + 63 + 1 = **89**, which is the post-D4 catalog (90 today, −2 for
`hand_off`/`return_to_default`, +1 for `switch_agent`). Arithmetic verified against
`allStaticToolNames` in session.

**Baseline for a future revisit: this partition is "as of 2026-08-27" against the 89-name post-D4
catalog.** §4.3's revisit trigger and §4.5's per-agent idea both need a clean "as of" to diff
against, and a tier table with no date silently becomes un-diffable the first time a tool is added.
The drift test required in §4.4 is what keeps that date meaningful — it fails the build when the
partition changes without a deliberate decision, so any later table carries a real, dated delta
rather than an accumulation of unrecorded drift.

**Tier 1 (17):** `read_file`, `write_file`, `edit_file`, `list_directory`, `list_mounts`,
`search_web`, `fetch_url`, `send_message`, `switch_agent`, `send_file`, `message_parent`,
`remember`, `recall_memory`, `recall_conversation`, `set_todos`, `list_tasks`, `delegate`.

**Tier 2 (8):** `list_agents`, `list_jobs`, `serve_web`, `navigate`, `get_workspace`, `bash`,
`create_task`, `update_task`.

**Tier 3 (63):** `append_file`, `library_list`, `library_read`, `request_mount`, `find_skills`,
`install_skill`, `browser_navigate`, `browser_click`, `browser_type`, `browser_screenshot`,
`browser_get_text`, `browser_wait`, `browser_evaluate`, `browser_list_tabs`, `browser_switch_tab`,
`browser_close_tab`, `browser_open_tab`, `create_workspace`, `update_workspace`, `delete_workspace`,
`list_workspaces`, `read_agent_metadata`, `write_agent_metadata`, `configure_provider`,
`list_providers`, `test_provider`, `list_models`, `run_doctor`, `get_usage`, `add_mcp_server`,
`remove_mcp_server`, `list_mcp_servers`, `create_skill`, `edit_skill`, `create_task_in_workspace`,
`update_task_in_workspace`, `delete_task_in_workspace`, `list_tasks_in_workspace`, `remove_skill`,
`list_skills`, `enable_channel`, `configure_channel`, `disable_channel`, `list_channels`,
`test_channel`, `get_config`, `set_config`, `create_agent`, `update_agent`, `delete_agent`,
`create_plan`, `execute_plan`, `run_task`, `inspect_session`, `plan_correct`, `stop_plan`,
`run_retrospective`, `read_inbox`, `search_email`, `read_message`, `send_email`, `reply`,
`delete_task`.

### 4.2 Why these placements (the categories, not the individual names)

Individual placements are the operator's own calls and are not re-derived here. The reasoning
behind each *category* of move, which is what a future reader needs:

- **`bash` Tier 1 → Tier 2 (F1).** The point is not to make `bash` hard to reach — one
  `ToolSearch` call, once per session, and it is callable for the rest of it. The point is to
  remove its *permanent* visibility advantage over the narrower tools it competes with, so that
  "shell out" stops being the lowest-friction answer to a question a purpose-built tool answers
  better. It stays in Tier 2 rather than Tier 3 precisely because it is a legitimate general-purpose
  tool that should remain one preview line away, not a search away.
- **The memory trio is now fully Tier 1** (`remember`, `recall_memory`, `recall_conversation`; the
  first two were already Full, `recall_conversation` is promoted). Under ADR-028, `windowTrim` is
  the only compaction path and it *evicts whole turns with zero LLM calls* — recall is the agent's
  only route back to evicted history. A recall tool that costs a discovery round trip is a recall
  tool the agent will skip, and the failure mode is invisible: it reads as the agent having
  forgotten, not as a tool it could not see. Cheap insurance against ADR-066's warning case.
- **`delegate` and `list_tasks` stay Tier 1; `create_task`/`update_task` drop to Tier 2.** ADR-053
  measured that `delegate` must be *at least* as visible as the task tools or the model routes work
  to tasks because that is what it can see. Keeping `delegate` in Tier 1 while demoting task
  mutation preserves that ordering with a wider margin than before. `list_tasks` stays because it is
  a read the agent needs to orient itself, not a route it can be lured down.
- **`switch_agent`, `send_file`, `message_parent`, `list_mounts` in Tier 1.** All are
  conversational-control or addressing primitives with no natural discovery moment — an agent that
  needs to hand off, attach a file, report to its parent, or find out which folders it may write to
  needs that answer *while it is answering*, not one turn later.
- **Admin/management, browser, planning, and email → Tier 3.** 63 of 71 lazy tools are things a
  general chat agent touches rarely or never: workspace/agent/channel/provider CRUD, MCP server
  management, the 11 browser primitives, the ADR-052/055 planning and supervision verbs. They are
  precisely what makes the per-turn block expensive, and precisely what an agent only wants when it
  has *already decided* to do that class of work — at which point a query describing that work is a
  natural way to find them.

### 4.3 The accepted risk, stated plainly

**Tier 3 is now the largest tier: 63 of the 71 lazy tools, 71% of the whole catalog, become
invisible by default.** Before this change every one of those 63 was one free preview line away and
cost the agent nothing to notice. After it, an agent that does not already suspect a capability
exists has no zero-cost way to discover it. The compressed block shrinks from **~101 rendered lines
to 22** (see the size note below), which is the win; the loss is that most of the catalog's
discoverability now depends on the model formulating a `ToolSearch` query it has no prompt to
formulate.

This is a genuine tradeoff, the operator was shown it, and the operator made this call
deliberately. It is accepted, not overlooked.

**The 71% has TWO prerequisites, not one, and neither was true when r1–r3 asserted the number
flatly.** "Invisible by default" is a property of **two independent channels**, and each needed a
fix this ADR did not originally budget for:

1. **The manifest block** — scoped per `(agent, session)` only because **§4.6** re-keys the
   loaded-tools bucket. The bucket is keyed by session id with no agent component, and D4's own
   `switch_agent` changes the active agent *within* a session, so every handoff let the incoming
   agent inherit whatever Tier 3 tools the outgoing one had found.
2. **The `ToolSearch` match list** — scoped by policy only because **§3.2.2** filters it. Until r5
   the list was returned unfiltered, so **any** query could disclose a policy-**denied** Tier 3
   tool's name and full description on a top-5 BM25 placement alone. That is the channel this
   sentence's own next clause depends on, and it was the unbounded one.

Both are **prerequisites of this paragraph being true**, not optimisations. Read both before relying
on the number. And read the claim precisely: after §3.2.2 the property is *"a tool the agent's
policy denies is nameable through neither channel, and a tool it allows costs one deliberate search
to find"* — invisibility is bounded by **policy**, not by tier.

**The sentence above needs one qualification even so.** "An agent that does not already suspect a
capability exists has no zero-cost way to discover it" holds for *denied* tools absolutely, and for
*allowed* Tier 3 tools it means one `ToolSearch` call — which is the accepted design, not a leak.
What it never meant, and what §3.2.2 had to make true, is that a **denied** tool stays unnamed.

**Size note — "8" is a tool count, not a line count, and the rendered figures are 22 and ~101.**
`BuildCompressedManifest` writes two fixed header lines, then for each category present a blank
line, a `## <category>` heading, and one line per tool. Verified against the eight real Tier 2 names:
`list_agents` (agents), `navigate` (platform), `bash` (shell), `create_task`/`list_jobs`/
`update_task` (tasks), `serve_web` (web), `get_workspace` (workspaces) — **six** distinct
categories, so `2 + (6 × 2) + 8 = 22` lines. The pre-D3 block spans **14** categories across all 71
lazy tools, so `2 + (14 × 2) + 71 ≈ 101` lines. Both figures are ceilings for an agent whose policy
allows everything and which has loaded nothing. **Quote 22 and ~101 wherever a line count is meant,
and "8 Tier 2 tools" wherever a tool count is meant** — the two were used interchangeably throughout
r1–r3 and the mixed usage understated the win by a factor of about four while overstating the
remaining block by none. It does not change any decision; it changes the numbers a reader is asked
to trust.

Two things mitigate the discoverability loss and neither is a full answer. First, the block's header
prose survives and should be rewritten to say plainly that *more tools exist than are listed* and
that `ToolSearch` finds them by description — an agent that knows the search exists is far more
likely to use it than one inferring it from an eight-entry list. Second, an agent's own
`SOUL.md`/`AGENT.md` can name the capabilities it is expected to have, which is a per-agent version
of the same hint.

### 4.3.1 Rollback and detection — DECIDED, both, and neither is optional

r1 left this open: it named a revisit trigger with no data source behind it and no way to undo the
change short of a binary revert. The review was right that this is not a tenable posture for a
change that alters what every agent on every upgraded install can see, all at once, with a
self-acknowledged unmeasured behavioral risk. "Revisit if agents can't find things" with no
telemetry is unfalsifiable, and an unfalsifiable trigger is not a mitigation — it is the *appearance*
of one, which is worse, and is exactly the failure shape `docs/internal/false-green-patterns.md`
catalogues.

**Both mechanisms below are hard prerequisites of D3 shipping.** The review offered them as an
either/or and recommended instrumentation alone as the cheaper, more CLAUDE.md-consistent choice.
That reasoning is right about cost and wrong about sufficiency: instrumentation tells you the split
is wrong, and a kill switch is what lets you *act* on that within minutes instead of a release
cycle. They answer different questions and neither substitutes for the other.

**(a) Detection — two counters, wired to the `/metrics` endpoint that already exists.**

**r2 got the mechanism wrong and the r3 review was right to call it.** r2 specified these as
package-level `atomic.Uint64` counters with an exported accessor and explicitly "**no metrics
backend**", modelled on `handoffTranscriptWriteFailures` (`pkg/tools/handoff.go`). Checked: that
precedent has exactly one consumer in the entire repository — `pkg/tools/handoff_adr057_test.go`, a
test file — despite its own doc comment naming a Prometheus metric
(`omnipus_handoff_transcript_write_failures_total`) that is registered with nothing. Copying it
would have produced counters no operator could read: no log line, no endpoint, no query. That is
**falsifiable in principle, unobservable in practice**, which is a worse posture than r1's
unfalsifiable trigger, not a better one — it *looks* like a detector. This document spends §6.6
arguing that a detector nobody can see is not a detector; leaving its own detector in that state
would have been the document contradicting itself.

**This codebase already has the right mechanism, in the right domain, with a proven wiring
pattern.** `pkg/gateway/metrics.go` serves a hand-rolled Prometheus-text `/metrics` endpoint
(`restAPI.HandleMetrics`) whose existing series are **tool-registry counters** —
`omnipus_tool_filter_total`, `omnipus_tool_mcp_collision_total`,
`omnipus_tool_approval_latency_seconds`, `omnipus_tool_approval_pending`. It is reached from
`pkg/tools` with zero import-cycle risk via a small unexported recorder interface in
`pkg/tools/compositor.go`:

```go
// pkg/tools/compositor.go — the EXISTING pattern (FR-039), verbatim in shape
type toolMetricsRecorder interface {
    IncFilterTotal(agentType, effectivePolicy string)
    IncCollisionTotal(conflictWith string)
}
type nopToolMetrics struct{}
func (nopToolMetrics) IncFilterTotal(_, _ string) {}
func (nopToolMetrics) IncCollisionTotal(_ string) {}
var activeToolMetricsRecorder toolMetricsRecorder = nopToolMetrics{}
func SetToolMetricsRecorder(m toolMetricsRecorder) { /* … */ }
```

`pkg/gateway/gateway.go` wires the real implementation at boot with a single line —
`tools.SetToolMetricsRecorder(globalToolMetrics)` — and `*toolMetrics` (`pkg/gateway/metrics.go`)
satisfies the interface. Nothing else is needed: the no-op default means `pkg/tools` works
standalone in tests and in any non-gateway embedding, and the gateway swaps in the real recorder
without `pkg/tools` importing `pkg/gateway`.

**Decision — extend that interface; do not invent a second, weaker mechanism.** Concretely:

1. **`pkg/tools/compositor.go`** — add two methods to `toolMetricsRecorder` and to `nopToolMetrics`:
   `IncToolSearchZeroResult()` and `IncToolSearchNoFollowUp()`.
2. **`pkg/tools/tools_tool.go`** — call `activeToolMetricsRecorder.IncToolSearchZeroResult()` on the
   `query` path when the policy-loadable result set is empty. **r5: that set is now materialised by
   §3.2.2's filter, so the fire condition is literally `len(matches) == 0`** — before §3.2.2 the
   policy-loadable set existed only as a transient inside the auto-load loop, and this counter's
   definition could disagree with the response the same call returned. The no-follow-up counter is
   incremented from **`pkg/agent`**, not from here — see the r4 redesign below; `pkg/tools` has no
   visibility into the session-level state this counter has to observe.
3. **`pkg/gateway/metrics.go`** — add two `atomic.Uint64` fields to `toolMetrics`, the two
   `Inc…` methods, and two exposition blocks in `HandleMetrics`:
   `omnipus_toolsearch_zero_result_total` and `omnipus_toolsearch_no_followup_total`, both
   `# TYPE … counter`, both unlabelled.
4. **`pkg/gateway/gateway.go`** — **no change.** The existing
   `tools.SetToolMetricsRecorder(globalToolMetrics)` call already carries the widened interface;
   this is the reason the pattern is worth reusing rather than paralleling.
5. **Keep exported package-level accessors too** (`ToolSearchZeroResultQueries()`,
   `ToolSearchNoFollowUpCalls()`), backed by the same `atomic.Uint64`s in `pkg/tools`, so unit tests
   can assert on the counters without standing up a gateway. This is the *only* part of r2's
   `handoffTranscriptWriteFailures` framing that survives — as a test affordance, not as the
   operator surface.
6. **(r4) `pkg/tools` gains one exported recorder wrapper,
   `tools.RecordToolSearchNoFollowUp()`**, because the no-follow-up increment now originates in
   `pkg/agent` (see the redesign below) and `activeToolMetricsRecorder` is unexported. `pkg/agent`
   already imports `pkg/tools`, so this adds no dependency and no cycle; the zero-result counter
   still increments from inside `pkg/tools` directly and needs no wrapper. This is the only change
   r4 makes to the wiring — the recorder interface, the `/metrics` exposition and the
   `gateway.go`-needs-no-change property all stand.

**Required test (this is what makes the prerequisite real rather than declared):** an assertion that
`GET /metrics` contains both series names after a zero-result `ToolSearch` and after an abandoned
promotion. The r3 review's Test Coverage table names this gap explicitly — "no test proposed
anywhere that asserts [the counters] are reachable via `/metrics` or any other operator surface" —
and a counter whose observability is untested is one refactor away from being unobservable again.
Assert on the endpoint output, not on the accessor.

The two signals:

- **`omnipus_toolsearch_zero_result_total`** — incremented when a `query` path returns no
  policy-loadable hit. The direct signal for "the agent went looking for a capability and the search
  could not find it," which is precisely the failure mode §4.3 accepts risk on.
- **`omnipus_toolsearch_no_followup_total`** — incremented when a `query`-path `ToolSearch`
  promotion is **still uncalled 5 turns later** (or at session close, whichever comes first).
  Nothing expires — see immediately below for why the r3 wording ("expires unused") described a
  mechanism that does not exist for static tools.

**MIN-001 correction (r3), and why r4 had to redesign it — the counter as specified could only ever
read zero.** r2 defined this counter as firing "when a `ToolSearch` promotes one or more tools and
**the turn ends** without any of them being called." That conflates two unlike cases: a wrong guess,
and a *correct* guess the agent acts on one or two turns later after a prerequisite step (reading a
file, asking a clarifying question, finishing an unrelated sub-task). The second case costs nothing
and is normal behaviour; counting it would put a permanent, unquantifiable false-positive floor
under the exact number the revisit trigger reads. **That diagnosis is right and r4 keeps it.**

r3's fix was to *"increment when a promoted entry is dropped by `TickTTL` having never been called —
i.e. hook the existing TTL-expiry path in `pkg/tools/registry.go`."* **That is unbuildable as
specified.** Per §1.1.1, `TickTTL` never decrements a core entry and `GetAll` never drops one, so
for the entire static catalog — the only population this counter exists to monitor — the hook never
fires. It would fire only for MCP promotions, which §1.2/F3 already puts outside D2/D3's scope.
A counter that reads a permanent, structural zero is worse than no counter: §4.3.1's own opening
argument is that an *unfalsifiable* trigger is worse than none because it looks like a mitigation,
and a flat zero reads as "healthy" to the operator who is supposed to act on it.

**Redesigned (r4) — a promotion-age horizon in `pkg/agent`, where the state actually lives.** The
signal r3 wanted is preserved exactly; only the mechanism changes, and it changes because the old
one does not exist.

- **State.** One new map on `AgentLoop`, alongside `loadedTools` and under the same
  `loadedToolsMu`: `pendingSearchPromotions map[string]map[string]int` — bucket key → tool name
  → the turn index at which `ToolSearch` promoted it. This is a **side table**; `loadedTools` and
  `sessionLoadedTools`'s signature are untouched, so the hot read path in `buildCompressedToolDefs`
  does not change at all.
- **Key — DECIDED (r5): `manifestBucketKey(agentID, transcriptID, sessionKey)`, the same composite
  key `loadedTools` uses post-§4.6. Not the legacy `manifestSessionID`.** r4 wrote "session bucket"
  and left an implementer to guess, while §4.6 was re-keying the sibling map in the same workstream
  from the same closure at the same call sites. Two adjacent new mechanisms with silently different
  key shapes is exactly the defect class this document has now corrected in itself four times.
  Three reasons the composite key is the right one, in the order that decides it:
  1. **The counter's meaning requires it.** `no_followup` means *"this agent searched, was given a
     promotion, and never used it."* Under a session-only key, Agent A's pending entry for a tool is
     cleared when **Agent B** calls that tool after a `switch_agent` — A's wasted promotion goes
     uncounted, and the counter under-reports precisely the handoff-heavy sessions. In the other
     direction, B promoting a same-named tool overwrites A's recorded turn index and shifts A's
     horizon. Both corrupt the number §4.3's revisit trigger reads.
  2. **The clear-on-dispatch step can compute it.** Dispatch runs inside a turn owned by one agent
     and has `ts.agent.ID`, so it derives the same key the write did. Under a session-only key it
     would necessarily clear across agents.
  3. **It is a side table of `loadedTools`** — written from the same `markLoaded` closure, for a
     subset of the same names, at the same moment, and swept by the same `forgetSession`. Two maps
     written and cleared together must be addressed the same way or one of them is eventually swept
     by a key that matches nothing.

  §4.6's three id-sourcing guarantees carry over unchanged and are not re-derived here: both sides
  use `ts.agent.ID` / `tools.ToolAgentID(ctx)` (verified identical on every path including
  delegation), and the key uses the caller id **string**, so it is immune to the §5.2.2b resolver
  defect.
- **Write.** In the `markLoaded` closure (`pkg/agent/loop.go`, the same place that already calls
  `al.markToolsLoaded(sessionID, loadedOK)`), record each newly-promoted name against the current
  turn index — **only on the `query` path**. An exact-name `names` load is the model deliberately
  naming a tool it already knows about; a non-use there is a different phenomenon and would
  reintroduce a false-positive floor of its own. The counter is named
  `omnipus_toolsearch_no_followup_total` and should mean *search* follow-up.
- **Clear.** On tool dispatch, delete the entry for the invoked name. The invocation path already
  resolves the tool by name inside the turn, so this is a map delete on an existing branch.
- **Fire.** At the same per-turn point that already calls `ts.agent.Tools.TickTTL()`
  (`pkg/agent/loop.go` — one existing call site), sweep the current session's side table: any entry
  whose recorded turn index is more than **`searchPromotionHorizonTurns = 5`** turns old increments
  the counter **once** and is deleted. Deleting on fire is what makes it fire exactly once per
  wasted promotion rather than every turn thereafter.
- **Also fire at session close.** `forgetSession` drops the whole bucket; sweep it first so a
  promotion abandoned in a session shorter than the horizon is still counted. Without this the
  counter under-reports exactly the short, unsuccessful sessions that are the strongest signal.
  **r5 — three ordering details, because "sweep it first" is not tight enough to implement:**
  (i) `forgetSession`'s suffix sweep (§4.6 point 4) must delete matching keys from **both**
  `loadedTools` **and** `pendingSearchPromotions`. Sharing `loadedToolsMu` does **not** sweep the
  second map — the mutex protects, it does not enumerate — and §4.6 point 4 as written names only
  `al.loadedTools`, so left alone the side table becomes the leak the suffix sweep exists to
  prevent. This answers the r4 review's Unasked Question 3 as **no, not implicitly**.
  (ii) **Count, then delete, in that order**, in the same critical section — a sweep that runs after
  the delete reads an emptied map and silently reports zero.
  (iii) **Tally under the lock; increment after releasing it.** Collect the count while holding
  `loadedToolsMu`, then call `tools.RecordToolSearchNoFollowUp()` outside it, so no cross-package
  call is made under a lock (the discipline CLAUDE.md's FR-049 states for `pkg/session`, applied
  here for the same reason).

**Why 5, and why a horizon rather than an eviction.** 5 keeps r3's stated intent — a one- or
two-turn delay before acting is normal, five turns of silence is a wasted promotion — and it lands
on the same number the old TTL claim used, so the metric's meaning is unchanged from what §4.3.1
already argues for. Crucially the horizon is **purely observational: nothing is evicted, nothing
leaves the callable set, no behaviour changes.** That is the whole difference between this and
§3.3's rejected option (a), and it is why the counter can be built without reopening that decision.

**Required tests (r5 adds the third and fourth; the first two are r4's):** promote a static tool via
the `query` path, drive 6 turns without calling it, assert `omnipus_toolsearch_no_followup_total`
reads exactly 1 **on `GET /metrics`** and does not climb on turn 7. Plus the negative: promote, call
it on turn 2, drive to turn 8, assert the counter stays 0. Assert on the endpoint output, not the
accessor.

| # | Case | Assert |
|---|---|---|
| 3 | **Cross-agent isolation for the counter** (mirrors §4.6's `loadedTools` test) | A promotes tool T via `query`; `switch_agent` to B; **B** calls T inside the horizon; drive past the horizon — the counter reads exactly **1**, because A's promotion was still wasted. Under a session-only key it reads 0 |
| 4 | **No side-table leak** | After `forgetSession(sessionID)` on a session with pending entries under **two** different agent ids, `len(al.pendingSearchPromotions)` is 0 |

Test 4 is §4.6's "ships green if missed" case applied to the second map: a leaked side table has no
user-visible symptom until memory growth in a long-lived process.

Together these make the revisit trigger falsifiable **and readable** — and r5 states the second
counter's operator response as concretely as the first, because r4 gave one an action and the other
only a restatement of its own symptom:

- **`omnipus_toolsearch_zero_result_total` rising against a stable turn count** → **Tier 2 is too
  narrow and should widen** (§8-A is the designed fallback; widening is a data change, not a
  redesign).
- **`omnipus_toolsearch_no_followup_total` rising** → **tighten §3.2's ambiguity constants first, in
  this order: `searchCrossCategoryRatio` (0.50) → `searchAmbiguityRatio` (0.80) →
  `searchMaxAutoLoad` (3). Only reconsider Tier 3 *membership* if tightening all three does not
  bring it down.** The order is not arbitrary: rule 2 is the speculative clause by §3.2's own
  account (it fires at half the top score on a category-boundary heuristic), so it is the first
  suspect; `searchMaxAutoLoad` is last because lowering it discards a *qualifying* candidate rather
  than fixing the test that qualified it. A no-followup count that survives all three is evidence
  the promotions are being chosen correctly and the **placements** are wrong — a Tier 3 question,
  not a ratio one.
- **Both rising at once → act on zero-result first, then re-read no-followup.** They are not
  independent: widening Tier 2 removes searches that would have happened, which mechanically lowers
  no-followup too. Chasing both simultaneously means neither adjustment can be attributed.

**Committed, not recommended** — this resolves §11 Q3 as **yes** (see §11).

**(b) Rollback — one bool on an existing config struct.** `ManifestConfig`
(`pkg/config/config.go`) today holds exactly one field, `Compressed`. Note that `Compressed: false`
is **not** a usable rollback for D3: it disables the manifest optimization *entirely* and sends every
tool full every turn, which is a far larger hammer than "go back to the two-tier split" and carries
its own token cost. So D3 needs its own switch:

```go
// PreviewAllLazy reverts D3 (ADR-071): when true, ToolManifestVisibility
// returns ManifestPreviewed for EVERY ManifestLazy tool, restoring the
// pre-ADR-071 behavior in which the whole lazy catalog appears as preview
// lines. Default: false (the three-tier split is active).
PreviewAllLazy bool `json:"preview_all_lazy,omitempty" yaml:"preview_all_lazy,omitempty"`
```

Blast radius is one branch at the top of `ToolManifestVisibility` — the classifier is already the
single chokepoint for this decision (§4.4), which is what makes the switch this cheap. It is a
per-install dial, so an operator who hits the failure mode recovers without waiting for a binary.

**On CLAUDE.md's aversion to gratuitous config surface (Constraint #1's spirit, and §8-F's own
rejection of configurable ambiguity ratios): this flag is not gratuitous, and the distinction is
principled.** §8-F rejects config for a *tuning* parameter no operator can set intelligently without
data. This flag is not a tuning parameter — it is a binary revert of a deliberate, risk-accepted
behavioral change, which is precisely the case where operator control is warranted and where the
alternative is a binary rollback.

**It is explicitly time-boxed.** The flag exists to survive the observation window, not forever.
Removal trigger: once (a)'s counters have produced enough data to either validate the split or
motivate a widened Tier 2, `PreviewAllLazy` is deleted in the same change that acts on that data.
File it as a follow-up issue at ratification so it cannot quietly become permanent surface — an
un-time-boxed escape hatch is how config surface accretes, and this ADR should not be the one that
starts that.

**Revisit trigger, now falsifiable — and the two counters do not share an action.** If
`toolSearchZeroResultQueries` rises materially against a stable turn count, the split is wrong and
**Tier 2 should widen** (§8-A is the designed fallback, and widening is a data change, not a
redesign). If `toolSearchNoFollowUpCalls` rises, the response is the **ratio-tightening ladder
above**, not widening — r1–r4 pointed both counters at the same action, which would have had an
operator widening Tier 2 in response to a signal that Tier 2 is not the problem. Until those
counters exist, there is no trigger — which is why (a) ships with D3 rather than after it.

### 4.4 Mechanism: visibility is a second axis, orthogonal to tier

The distinction **cannot** be expressed as a fourth `ManifestTier` value, and this is not a style
preference — it would silently break search. `pkg/tools/registry.go::SnapshotSearchableTools` builds
the BM25 corpus by admitting core tools where `ToolManifestTier(name) == ManifestLazy`. A new
`ManifestSearchOnly` tier constant would fail that equality test and **remove all 63 Tier 3 tools
from the search index** — making them unreachable by the exact mechanism that is supposed to be
their only route. Tier 3 tools must remain `ManifestLazy`.

**This and §3.2.2 are the same lesson from two directions, and they reinforce each other.** Here:
do not encode a *visibility* decision in the corpus, because the corpus is what makes a tool
findable at all. There: do not encode a *policy* decision in the corpus, because the corpus is
shared across callers and its size determines every document's score. **The BM25 corpus is the set
of tools that exist to be found — not the set a given agent may see, and not the set a given agent
may load.** Both of those are decided downstream of ranking.

Decision — a second, independent classifier alongside `ToolManifestTier`:

```go
// ManifestVisibility controls whether a ManifestLazy tool appears as a preview
// line in the compressed manifest block. It is a SECOND axis, orthogonal to
// ManifestTier: it is only meaningful for ManifestLazy tools, and callers must
// resolve the tier first. Full and Infra tools have no manifest presence at all,
// so visibility does not apply to them.
//
// Do NOT fold this into ManifestTier as a fourth constant: SnapshotSearchableTools
// admits a core tool into the BM25 corpus on `ToolManifestTier(name) == ManifestLazy`,
// so a separate tier value would silently delete every search-only tool from the
// search index — the one mechanism by which it is reachable at all.
type ManifestVisibility int

const (
    // ManifestPreviewed — Tier 2: one `name — description` line in the block.
    ManifestPreviewed ManifestVisibility = iota
    // ManifestSearchOnly — Tier 3: no line in the block; reachable only via
    // ToolSearch (exact name or query). Still policy-governed and still in the
    // BM25 corpus. Once loaded it stays loaded for the rest of the session:
    // static tools are IsCore, and PromoteTools/TickTTL are no-ops on core
    // entries, so the registry TTL never applies here (ADR-071 §1.1.1).
    ManifestSearchOnly
)

// ToolManifestVisibility returns the visibility of a lazy-tier tool. Defined
// only for ManifestLazy names; callers must check the tier first.
func ToolManifestVisibility(name string) ManifestVisibility
```

An enum rather than a `ManifestVisible bool`, for two reasons: it mirrors the existing name-keyed
`ToolManifestTier` lookup, and it reads correctly at the call site (`if vis == ManifestSearchOnly
{ continue }` rather than a negated bool).

r2 gave a third reason — "it leaves room for a third value if the per-agent idea in §4.5 is ever
taken up" — and it is **deliberately removed here**. §4.5 is explicitly "not designed, not scoped,
not implemented," so that clause is the "someday we might need…" justification pattern, and the two
reasons above already carry the decision on their own. The enum is cheap and correct regardless;
what is not free is the precedent, since the same reasoning appears in ADRs where the speculative
option *is* costly. Recorded rather than silently dropped so a reader diffing r2 against r3 does not
read the deletion as an oversight.

Backing data is an explicit `previewedLazyToolNames` set holding exactly the 8 Tier 2 names;
everything else lazy resolves to `ManifestSearchOnly`. Two consequences of that direction, both
intended:

- **MCP tools default to search-only** — correct, and also a no-op, since §1.2/F3 established they
  never reached the block anyway.
- **A newly added static tool defaults to invisible.** That is a discoverability trap, and it must
  be converted into a build failure rather than left as a silent default. **Required:** a drift test
  asserting `previewedLazyToolNames` is exactly the 8 documented names and that every name in
  `allStaticToolNames` resolves to a *deliberately recorded* tier — mirroring the existing
  `TestCatalog_MatchesGlobalCeilingEntryForEntry` pattern, which is how this codebase already
  prevents exactly this class of silent omission. Adding a tool must force a tier decision.

`BuildCompressedManifest` gains one filter line (skip `ManifestSearchOnly`). Nothing else in the
turn-build path changes: `buildCompressedToolDefs` already sends a lazy tool only when it is in the
session's loaded set, which is correct for both Tier 2 and Tier 3.

**Where the filter lives once D5 lands — stated, not left to the W3→W2 sequencing note.** §6.1
splits `BuildCompressedManifest`'s manifest-block role into `BuildStaticToolCatalog(lazyTools []Tool)`
and `BuildLoadedToolsDelta(loaded map[string]bool)`. The visibility filter **stays inside the
builder**: `BuildStaticToolCatalog` applies `ToolManifestVisibility` itself and skips
`ManifestSearchOnly`, exactly as `BuildCompressedManifest` does pre-D5. It does **not** expect an
already-filtered `lazyTools` slice from its caller. Reason: the filter is a property of the manifest
block, not of the caller's tool set, and pushing it to the caller would give it two owners — the
turn-build path and the builder — which is how a Tier 3 tool eventually leaks a preview line on one
of the two paths. §10's W3-before-W2 sequencing makes the conflict small in practice, but "small in
practice" is not the same as specified, and an implementer doing W2 should not have to infer this
from a sequencing note. Same reasoning applies to `PreviewAllLazy` (§4.3.1b): the flag is read
inside `ToolManifestVisibility`, so both builders inherit the revert automatically and neither has a
second branch for it.

### 4.5 Future consideration, not decided here: per-agent tier assignment

Tier assignment is global. It arguably should not be: an Orchestrator-type agent whose entire job
is workspace and agent administration would rationally want much of Tier 3 promoted to Tier 2 *for
itself*, while a chat colleague would not. The data model already supports the idea in principle —
per-agent tool policy is already per-agent — and `ToolManifestTier`/`ToolManifestVisibility` would
need to take an agent identity rather than a bare name.

**Recorded as a future consideration; not designed, not scoped, not implemented here.** It should
be reconsidered together with the §4.3 revisit trigger, since both are answers to the same question
(is the global split right?) and a per-agent split is the better answer if the data says the global
one is wrong for some agents but right for others.

### 4.6 The loaded-tools bucket must be scoped to (agent, session) — DECIDED, and it is a prerequisite of §4.3

**The defect, verified.** `pkg/agent/tool_manifest.go::manifestSessionID` derives the loaded-tools
bucket key from `ts.opts.TranscriptSessionID`, falling back to `ts.sessionKey`. With transcripts on
— the default — the key is the transcript session id and **carries no agent identity at all**.
D4's `switch_agent` changes the active agent *inside* one session: `HandoffTool.Execute` calls
`sessionStore.SwitchAgent(sessionID, newAgentID)`, which reassigns the session's agent and leaves
the session and its transcript id untouched. (This is exactly why `delegate` is *not* affected —
ADR-057 gives each delegated child its own `TranscriptSessionID`, so a child gets its own bucket.)

So: Agent A runs `ToolSearch`, finds and loads a Tier 3 tool. The conversation hands off to Agent B.
On B's first turn, `buildCompressedToolDefs` reads the *same* bucket, and any Tier 3 tool that B's
own policy also permits lands in B's callable array — **no search, no discovery step, no visibility
barrier**. The more agents hand off within one conversation, the less D3 hides. This is a genuine
D3 × D4 interaction that r1–r3 never stated, and it silently weakened the risk the operator was
asked to accept in §4.3.

**Decided: scope the bucket to `(agent, session)`.** The alternative — accept and document the leak,
correcting §4.3 to "decaying toward less than 71% as agents hand off" — was considered and rejected:
D3's whole value proposition is that a given agent does not see the 63 tools it has no business
reaching for, and a property that erodes on an event the same ADR is introducing is not a property
worth shipping. The fix is small, and it is the *narrowing* direction.

**It does not conflict with ADR-057 — checked against `manifestSessionID`'s own doc comment, which
is where that reasoning lives.** The invariant that comment states is: *"it derives a bucket from the
two ids it is GIVEN and never widens the scope of either."* The concrete harm it guards against is a
delegated child resolving to its **parent's** bucket and starting pre-loaded with tools it never
asked for. Adding an agent component **strictly narrows** the key; it cannot reintroduce a shared
bucket. Better: the harm ADR-057 describes for delegation is *precisely* the harm described above
for handoff — an agent inheriting another agent's loaded set and paying its token cost on a turn
that may need none of it. §4.6 applies ADR-057's own reasoning to the one path it did not reach.

**The mechanism, specified to implement:**

1. **Replace `manifestSessionID(transcriptID, sessionKey)` with
   `manifestBucketKey(agentID, transcriptID, sessionKey)`** in `pkg/agent/tool_manifest.go`,
   returning `agentID + "\x1f" + manifestSessionID(...)`, and `""` when the session part is `""`
   (preserving the existing deliberate no-op key — `markToolsLoaded`/`sessionLoadedTools` already
   reject `""` rather than creating a shared unkeyed bucket). Keep and update the doc comment; it is
   load-bearing.
2. **Both sides must derive the agent id from the same value, and that value is `ts.agent.ID`.**
   Readers (`buildCompressedToolDefs`, `buildToolManifestNote`) have `ts.agent.ID` directly. The
   writer — the `markLoaded` closure in `pkg/agent/loop.go` — reads `tools.ToolAgentID(ctx)`, which
   `runTurn` stamps as `tools.WithAgentID(turnCtx, ts.agent.ID)`. **Verified identical on every
   path, including delegation:** `spawnSubTurn` sets the child's `agent.ID` from `execSource.ID`
   (ADR-032/ADR-057 identity rule), and the child's own `runTurn` stamps that same id. A mismatch
   here is the one failure mode `manifestSessionID`'s doc comment names explicitly — *"a mismatch
   causes loaded tools to become invisible to the model"* — so the two sources are named here rather
   than left for an implementer to pick.
3. **This is immune to the §5.2.2b resolver defect, and that is worth stating because it looks like
   it should not be.** That bug is `al.registry.GetAgent(callerID)` returning the wrong
   `AgentInstance`; the `callerID` **string** it resolves from is correct. The bucket key uses only
   the id string, never the resolved instance, so it is unaffected whether §5.2.2b is fixed or not.
4. **`forgetSession` must become a prefix sweep, or the map leaks.** It currently does
   `delete(al.loadedTools, sessionID)` with the bare transcript id — an exact-key delete that will
   match nothing once keys are composite, silently reintroducing the unbounded growth the function
   exists to prevent. Change it to delete every key with the `"\x1f" + sessionID` suffix. This is
   the same `O(n)` scan the function **already performs** for `recallSpans` two lines below, with
   the same justification its own comment gives: session close is a cold path, not the hot turn
   path. Session close is the only caller.
   **r5: that one sweep must cover BOTH maps.** §4.3.1(a)'s `pendingSearchPromotions` side table
   uses this same composite key (stated there, and it is the reason the keys were reconciled), and
   it is **not** swept by virtue of sharing `loadedToolsMu` — the mutex protects the maps, it does
   not enumerate them. So the suffix scan deletes from `loadedTools` **and**
   `pendingSearchPromotions`, and §4.3.1(a)'s close-time counter sweep runs **before** the delete,
   in the same critical section. The §4.6 test table's third row ("No bucket leak") gains the
   matching assertion for the side table — see §4.3.1(a)'s test 4.

**Required tests (three, because two of the three failure modes are silent):**

| Case | Assert |
|---|---|
| Cross-agent isolation | A loads a Tier 3 tool; switch to B (B's policy permits the same tool); B's `buildCompressedToolDefs` output does **not** contain it |
| Round-trip preservation | A loads a tool; switch to B; switch back to A; A's output **does** contain it (distinct buckets, not a reset) |
| No bucket leak | After `forgetSession(sessionID)`, `len(al.loadedTools)` is 0 for a session that had loads under **two** different agent ids |

The third is the one that ships green if missed: a leaked bucket has no user-visible symptom until
memory growth in a long-lived process, which no test in this ADR would otherwise observe.

**Blast radius beyond the above: none found.** `manifestSessionID` has exactly three production
callers (the two readers and the one writer) plus `pkg/agent/tool_manifest_adr057_test.go`, which
asserts the ADR-057 parent/child isolation property directly and must be updated to pass agent ids —
its assertions remain valid, since two different transcripts still yield two different buckets.

**Relationship to §4.5.** This is *not* the per-agent tier assignment §4.5 defers. Tier membership
stays global — the same 8 names are previewed for every agent. §4.6 only makes the *loaded* set,
which was always meant to be per-conversation, actually per-agent as well.

---

## 5. D4 — `switch_agent` replaces `hand_off` and `return_to_default`

### 5.1 Decision

One tool, `switch_agent`, replaces both. It follows the precedent this codebase has now set twice:
ADR-036 merged `exec`/`workspace_shell`/`workspace_shell_bg` into `bash` and
`spawn`/`run_subagent`/`check_spawn_status` into `delegate`, on the principle that one capability
gets one tool identity and the variation becomes a parameter.

```
switch_agent(target: string, note?: string)
```

- `target` — another agent's id (equivalent to today's `hand_off`'s `agent_id`), or the literal
  string `"default"` (equivalent to today's `return_to_default`). **Required.**
- `note` — see §5.1.1. Declared optional; conditionally expected in prose.

`hand_off` and `return_to_default` are deleted. No permanent dual-name compatibility, matching
ADR-036 §3.6's "convert and clean, once" rule.

r1 described this merge as a parameter-shape unification "in the spirit of ADR-036" and stopped
there. The review's central objection is correct and is the reason §5.1.1–§5.1.3 exist: the two
tools being merged differ in **required-ness**, in **parameter semantics**, and in **execution
logic**, and an implementer reading r1 had to make three consequential product decisions the ADR
should have made. They are made below.

### 5.1.1 The note parameter — named `note`, declared optional, conditionally expected

**The r1 text was factually wrong.** It wrote `context?: string` and claimed the parameter was
"preserved unchanged from `hand_off`'s existing parameter." Verified against source:
`HandoffTool.Parameters()` declares `"required": []string{"agent_id", "context"}` — `context` is
**required** today. r1 asserted a relaxation had not happened while its own signature showed that it
had. Corrected here.

**Three facts drive the decision, and the third is the one that actually settles it:**

1. `hand_off.context` is schema-`required` and described as *"Context or instructions to give the
   target agent about this conversation"* — **forward-looking**, addressed to the agent taking over.
2. `return_to_default`'s equivalent is not called `context` at all. It is `summary`, `"required":
   []string{}`, described as *"Optional summary of what was accomplished before returning"* —
   **backward-looking**, a report on work already done.
3. **`hand_off`'s `required` is a prompt-engineering device, not an enforced invariant.**
   `HandoffTool.Execute` reads it as `contextMsg, _ := args["context"].(string)` and proceeds on a
   missing or empty value — no validation, no error. The declared requirement exists to make the
   model write a brief, and nothing downstream depends on one being present.

**Decision — name it `note`.** Neither legacy name is correct for both directions: `context` imports
forward-looking framing into the return case, `summary` imports backward-looking framing into the
handoff case, and r1's silent merge into `context` left the model with no guidance for half of its
own call sites. `note` is semantically neutral across both. Two further reasons: `context` is a
badly overloaded word in this codebase (context window, `ContextBuilder`, `dynamicCtx`,
`getContextWindow`), and since both tools are deleted outright there is no back-compat cost to
paying for the better name once.

**Decision — declared optional (`"required": ["target"]`), with the obligation carried in the
description.** The honest statement of what this changes:

- For the `target: "default"` branch this matches today exactly (`summary` is already optional).
- For the agent-target branch this **is** a relaxation of the declared schema — stated plainly
  rather than, as r1 did, denied. It is deliberate, and the cost is near zero because of fact 3:
  the requirement was never enforced at runtime, so no behavior that exists today stops working.

**Why not a conditional `required`** (the review's alternative: required iff `target != "default"`).
JSON Schema cannot express that in a `required` array; it needs `if`/`then` or `oneOf`. Tool
parameter schemas here are bare `map[string]any` handed straight to each provider, and **no tool in
this codebase uses a conditional schema construct today** — so this would be the first, across three
provider adapters (Anthropic, OpenAI-compatible, Bedrock) whose handling of `if`/`then` in tool
schemas is not uniform and was not verified. Paying that for an obligation the current code does not
enforce anyway is a bad trade. The alternative — enforcing it in `Execute` — would be a *new* hard
failure where today there is none, which is a behavior regression introduced by a consolidation that
is not supposed to change behavior.

**Description (normative — this string is the entire mitigation, so it is specified here rather
than left to the implementer):**

> Optional. When switching to a named agent: context or instructions for that agent about this
> conversation. When returning to the default agent (`target: "default"`): a summary of what was
> accomplished. Strongly recommended when handing off to another agent — it is the only context the
> incoming agent gets beyond the transcript.

**No counter is added for how often `note` is omitted (r4, declining the r3 review's OBS-101).** The
consistency argument — §4.3.1 instruments one soft behavioural expectation, so why not this one — is
fair on its face and fails on what the two counters are *for*. §4.3.1's exist because D3 accepts a
risk provisionally and needs a **falsifiable revisit trigger** with a stated action behind it
(widen Tier 2). `note`'s optionality accepts no new risk: `hand_off.context` was schema-`required`
and never enforced at runtime, so the observable behaviour is identical to today's and no decision
is waiting on the number. A counter with no trigger behind it is exactly the surface-for-its-own-sake
§9 rejects for the `manifest_visibility` wire field. Recorded rather than silently dropped, per the
review's own request.

### 5.1.2 Reconciling the two `Execute` bodies — step by step

The review is right that this is not a mechanical merge, and r1 was silent on it. Both bodies were
read in full (`pkg/tools/handoff.go`). `HandoffTool.Execute` does agent-existence, worker-rejection,
session resolution, atomic switch, a **token-budget-aware transcript transfer**
(`ReadTranscript` → `splitByTokenBudget` at 50% of the target's context window → a truncation
summary line), an audit entry, a frontend notify, and a result. `ReturnToDefaultTool.Execute` does
session resolution, default-agent resolution, the switch, an audit entry, a frontend notify, and a
result — **no worker check, no transcript read, no budget split**.

| Step | `switch_agent` behavior | Conditional? |
|---|---|---|
| **10-second `context.WithTimeout` wrapper** | **Wraps the whole `Execute`, unconditionally.** See below. | **unconditional (was conditional)** |
| Target resolution | `target == "default"` → `getDefaultAgent()`, error `"no default agent configured"` if empty. Otherwise → `GetAgentName(target)`, error `"agent %q not found"` if absent. | **branches** |
| Worker rejection (`reg.IsWorker`) | Runs. | **unconditional** — see below |
| Session-key resolution | Runs; error if absent. | unconditional |
| Atomic switch (`SwitchAgent`, idempotent via `ErrAlreadyActive`) | Runs. | unconditional |
| Token-budget transcript transfer | **Skipped when `target == "default"`.** | **conditional** |
| Audit transcript entry | Runs. `AgentID` stamping differs by branch — see below. | unconditional |
| Frontend notify (`onHandoff`) | Runs. | unconditional |
| Result string | Branch-specific prose, identical shape. | branches |

**The 10-second timeout becomes unconditional — a behaviour difference r2's table missed despite
claiming both bodies were read in full.** `HandoffTool.Execute` opens with
`ctx, cancel := context.WithTimeout(ctx, 10*time.Second)`, wrapping the entire operation; it exists
because of the `ReadTranscript` → `splitByTokenBudget` work in steps 5–6.
`ReturnToDefaultTool.Execute` has **no timeout wrapper at all**. r2's seven-row table enumerated
every other divergence and omitted this one, which is exactly the class of thing a merge silently
decides by whichever body the implementer starts from.

**Decision: `switch_agent.Execute` takes the 10-second wrapper unconditionally.** For the
`target: "default"` branch this *is* a new constraint where today there is none — stated plainly
rather than left implicit. It is judged harmless: that branch does no transcript I/O and no
token-budget split, so its work is a handful of map lookups, a `SwitchAgent` call and one
`AppendTranscriptStrict` append — orders of magnitude inside 10 seconds. A branch-conditional
wrapper was considered and rejected: it would make the merged body carry two context lifetimes for
no measurable benefit, and an unconditional ceiling on a tool that mutates live session state is the
safer default. If the default-return path ever *does* start doing I/O, having the ceiling already in
place is the outcome we want.

**Worker rejection becomes unconditional, and that is a small deliberate improvement, not an
oversight.** It is moot in the common default case — but only *usually*. `getDefaultAgent`
(`pkg/agent/loop.go`) returns `cfg.Agents.Defaults.DefaultAgentID` verbatim when set, with no worker
check; a hand-edited config can point the default-agent singleton at a worker. Today
`return_to_default` would happily pin the session to it. Running the check on both branches costs
one map lookup and converts a silent misconfiguration into the clear error `hand_off` already
produces.

**The token-budget transfer stays conditional, and this is the question the review said nothing in
the ADR picked.** Picked now: **it does not run for `target: "default"`.** The transfer exists to
give a *cold* agent — one that has not been in this conversation — a budgeted brief it could not
otherwise afford to read. The default agent is not cold: it is the agent that was in the
conversation before any handoff, its own entries are in the same transcript, and it hydrates them on
its next turn regardless. Paying a full `ReadTranscript` plus a token-budget split to re-brief the
agent that already has the context is pure cost for no information. This preserves today's
`return_to_default` behavior exactly, so the merge remains behavior-preserving on this axis.

**The audit entry's `AgentID` stamping stays asymmetric, deliberately.** `hand_off` stamps the
**target** agent, with a load-bearing reason recorded in its own comment: transcript hydration on a
fresh turn then surfaces the brief under the *incoming* agent's history, so the target reads it on
its first turn. `return_to_default` stamps the **current** (outgoing) agent, because that entry is a
record of what the outgoing agent did. Unifying these is a separate question with its own hydration
blast radius, and a consolidation ADR is the wrong place to change it. `switch_agent` keeps both
behaviors under a branch, and this paragraph is why — so a future reader does not "clean up" what
looks like an inconsistency.

### 5.1.3 The `default` collision — forward reservation *and* upgrade-time handling

r1 flagged the forward case and stopped. The review is right that this leaves the upgrade-time data
case unhandled, and that it is a real one. Verified:

- New agents created through the API get `ID: uuid.New().String()` — so the API path cannot
  *currently* mint an id of `default`.
- But literal ids are ordinary in this system: the core roster is seeded with `"mia"`, `"jim"`,
  `"ava"`, `"ray"` (`pkg/coreagent/core.go`), and the agent list is operator-editable file-based
  config. A hand-edited `config.json` with `"id": "default"` is reachable today.
- **Nothing reserves the string anywhere.** `grep -rn 'ID == "default"\|reservedAgentID\|isReservedAgent'
  --include='*.go' pkg/` returns zero hits.
- `switch_agent` resolves its target by **id only** (`GetAgentName(agentID)` is a map lookup on
  `r.agents[agentID]`), so the collision is on the id, not the display name — though `GetAgentName`
  falls back to returning the id when `Name` is empty, which is why the two read as interchangeable
  in the UI.

**Decision, three parts:**

1. **The sentinel always wins.** `target == "default"` means the default agent, unconditionally. No
   fallback to an id-matched agent, no ordering subtlety — the ambiguity is resolved by rule, not by
   lookup order.
2. **Forward: reject at the boundary.** Agent create/update rejects an id or name of `default`
   (case-insensitive) with a 400. Cheap, and it makes the collision impossible going forward rather
   than merely documented.
3. **Upgrade-time: a boot-time WARN, not a boot abort.** If an existing agent's id is exactly
   `default` at boot, log a startup WARN naming the agent and stating plainly that it is unreachable
   via `switch_agent`'s literal path and should be renamed. It stays fully reachable by every other
   route — routing bindings, the UI agent picker, `delegate`, direct `agent_id` addressing — so the
   consequence is bounded and the WARN is proportionate.

**Why WARN and not abort, given this codebase aborts boot on tool-policy coverage gaps.** The
comparison is worth making explicitly because the precedent points the other way. A Constraint #6
coverage gap is a *security* invariant: booting past it means an agent runs with an unresolved
policy, so aborting is correct. A name collision is a usability defect on a rare install, and the
degraded behavior — one addressing path shadowed, everything else working — is not a security
failure. Bricking an operator's upgrade over it is disproportionate. Stated here so the asymmetry
with `ValidateToolPolicyCoverage` reads as a decision rather than an inconsistency.

Test coverage this requires: a case that seeds an agent with id `default`, asserts boot succeeds,
asserts the WARN fires, and asserts `switch_agent(target: "default")` reaches the *configured
default agent* and not the shadowed one.

Part 2 forbids a name someone might want and so is still the operator's call to ratify (§11 Q1) —
but parts 1 and 3 hold regardless of how Q1 lands, since part 3 is precisely what covers the
installs part 2 cannot reach.

### 5.2 Blast radius — this rename is security-relevant, not cosmetic

`pkg/tools/registry.go` declares `ExcludedHandoff ExcludedTool = "hand_off"`, used by `CloneExcept`
to strip the tool from a delegated sub-turn's child registry. The purpose (ADR-040, FR-H-006) is
that a delegated child **must not be able to hijack the active user-facing session** by handing it
to another agent. The exclusion matches by **exact tool name**.

Rename the tool and forget the constant, and the exclusion stops matching — **a delegated sub-turn
regains the ability to switch the user's active agent.** The exclusion constant and every test
asserting on it must be updated in the same commit as the rename, and those tests must assert the
**new** name is absent, not merely that the old one is. That instruction is unchanged from r2.

**But r2's characterisation of *how* this fails was wrong in two ways, and r3 corrects it rather
than leaving an overstated security claim standing.** r2 wrote "**silently** … No error, no failing
test in the obvious place," naming `pkg/tools/sprint_h_registry_test.go` and
`pkg/tools/delegate_grandchild_test.go` as tests that "would keep passing." Traced through source at
`aa97bcea`, that is not what happens. An ADR that overstates a risk to justify care damages trust in
exactly the same way as one that understates it, so:

1. **There *is* a warning.** `CloneExcept` (`pkg/tools/registry.go`) opens with an existence check
   that emits, for every named tool absent from the base registry,
   `slog.Warn("CloneExcept: tool not in base registry", "tool", …, "hint", "check for renamed or
   unregistered tool")`. Its own comment states the purpose: *"The check prevents silent no-ops
   (e.g., a renamed tool that should still be excluded)."* This guard was built for precisely this
   scenario. It is a WARN on every sub-turn spawn, in a log nobody watches during a refactor — weak,
   but it is not silence, and the ADR should not claim otherwise.

2. **Three of the four named tests fail loudly, and the fourth fails for a worse reason than r2
   gave.** The distinction matters because it changes which file actually carries the risk:

   | Test | Behaviour on a forgotten `ExcludedHandoff` | Why |
   |---|---|---|
   | `pkg/tools/sprint_h_registry_test.go` | **FAILS loudly** | Registers `&HandoffTool{}` (so the struct rename is a *compile* error that forces a visit) and asserts the pre-condition `require.True(t, hasHandoff, "hand_off must be in the parent registry")`. Whether the implementer updates the literal or not, one of the pre/post assertions breaks |
   | `pkg/agent/sprint_h_subturn_test.go` | **FAILS loudly** | Same shape — `parentRegistry.Register(&tools.HandoffTool{})` plus `require.True(t, hasHandoffBefore, …)` |
   | `pkg/agent/sprint_h_scenario_test.go` | **FAILS loudly** | Same — registers the real struct, then `require.False` per forbidden name over a `CloneExcept("delegate", "hand_off")` child |
   | `pkg/tools/delegate_grandchild_test.go` | **PASSES — and is vacuous TODAY, not merely post-rename** | It registers **only** `&DelegateTool{}`. `HandoffTool` is never registered, so `childRegistry.Get("hand_off")` has always returned false and `assert.False(t, childHasHandoff, "hand_off must NOT be in the child registry")` has **never tested anything**. Worse: because the file contains no `HandoffTool` reference, the struct rename produces **no compile error**, so a compiler-guided rename never visits it at all |

**So the corrected statement of the risk is narrower and more useful than r2's.** A bare "rename the
tool, forget the constant" is caught by CI — three tests break. What is *not* caught, and what this
section should actually be warning about, is:

- **`delegate_grandchild_test.go` is a live vacuous assertion**, independent of this ADR. It reads as
  coverage of the `hand_off` exclusion and provides none. Fix it in W1 by registering the real tool
  (post-rename, `&SwitchAgentTool{}`) before cloning, so the `assert.False` has something to be false
  *about*. **This is a pre-existing defect this ADR surfaces, not one it creates** — flagged here
  because W1 is the moment someone is looking at the file.
- **The production log line in `pkg/agent/subturn.go` (§5.2.2) is the genuinely fail-open site**, and
  it is not a test at all.

This remains the same *family* as the fail-open shape ADR-036 §3.6 identified for tool policy. The
mechanism is real; only r2's account of the detection surface was wrong.

### 5.2.1 `websocket.go` has TWO exact-string branches, not one — and the untested one is the worse regression

**r1 described only half of this bug.** It named the `return_to_default` test and prescribed a fix
for it. There is a **second, sibling branch three lines above it**, gating on `hand_off`, which r1
never mentioned. Both were re-verified at `aa97bcea`:

```
pkg/gateway/websocket.go:3878:  if p.Tool == "hand_off" && status == "success" {
pkg/gateway/websocket.go:3899:  if p.Tool == "return_to_default" && status == "success" {
```

(Line numbers are given because the review cited them and they were re-confirmed; cite the
**condition text** when working, per CLAUDE.md's `file::symbol` rule — this region of `websocket.go`
churns.)

Both gate on an exact tool-name string. After D1+D4 the tool name is `switch_agent` in both cases,
so **both conditions become permanently false** — not just the one r1 discussed. The consequence of
fixing only what r1 narrated:

- The `:3899` branch gets fixed → return-to-default keeps clearing the indicator.
- The `:3878` branch keeps testing a string no tool produces → **every successful
  `switch_agent(target: <agent>)` stops emitting `AgentSwitchedFrame` at all.** The SPA's
  active-agent indicator freezes on whatever agent was active before the switch. Silently. No error.

That is the *common* case — handing off to a named agent is far more frequent than returning to
default — so the branch r1 missed carries the larger regression. It is the same "looks right,
measures as a no-op" failure shape §6.2 warns about for D5, missed in the one section already
looking for it.

**Test-coverage callout — the `hand_off`-success branch has zero test coverage anywhere in the
repository.** This is not "thin coverage"; it is none, and it is why the omission survived r1. There
is no `AgentSwitchedFrame` success-path assertion in `pkg/gateway/*_test.go`;
`tests/integration/handoff_agent_id_test.go` drives a `hand_off` tool-call stream but never asserts
on `agent_switched`; `tests/e2e/handoff.spec.ts`'s only `hand_off` mention is unrelated prose about
`SubagentBlock`. **So the regression above would ship green.** New tests are therefore a required
deliverable of W1, not a nice-to-have — see the acceptance test in §5.2.3.

### 5.2.2 r1's prescribed fix is not implementable — the payload carries no arguments

r1 said the condition "must be re-expressed as an inspection of `switch_agent`'s `target`." **It
cannot be, at that call site.** `agent.ToolExecEndPayload` (`pkg/agent/events.go`) carries
`ToolCallID`, `ChatID`, `SessionID`, `Tool`, `Duration`, `ForLLMLen`, `ForUserLen`, `IsError`,
`Async`, `Result`, `ParentSpawnCallID`, `AgentID`, `ProducingSessionID` — and **no tool-arguments
field**. `target` is simply not in scope where the frame is built. An implementer following r1
literally would discover this only after starting.

Two implementable options; **(A) is the decision**, (B) is recorded as the fallback.

**(A) — DECIDED: derive the semantic from the post-switch active agent.** Collapse both branches
into one `if p.Tool == "switch_agent" && status == "success"`, then:

- Read `h.agentLoop.GetSessionActiveAgent(evtSID)` — the `:3878` branch already does exactly this,
  so no new call is introduced.
- Compare it against the registry's default agent id.
- **Equal** → emit with `AgentId` nil (the frame's existing "returned to default" semantic, §9).
  **Not equal** → emit with `AgentId` set, as the `:3878` branch does today.

This works because `ReturnToDefaultTool` already routes through the *same* `SwitchAgent` call and
the same `onHandoff` callback with `AgentID: defaultAgentID`, so the session's active agent is the
default's id after a return — it is not cleared. Verified in `onHandoffFrontend`
(`pkg/agent/loop.go`), which stores the id under the session key and only deletes the override on an
empty `AgentID` — a branch `return_to_default` never takes.

One behavior delta, judged acceptable and recorded so it is not mistaken for a bug: an explicit
handoff **to the agent that happens to be the default, by id** now emits `AgentId` nil where today
it emits it set. The frame's contract is that nil means "the default agent is active," which is true
in that case, so the SPA renders correctly either way.

Two guard requirements that today's code gets away with and the merged branch must not:

- `:3878` wraps its emission in `if activeAgent, ok := GetSessionActiveAgent(evtSID); ok` and emits
  **nothing** when `!ok`. After a *successful* switch, `!ok` is an invariant violation, not a normal
  path. The merged branch must log a WARN rather than silently emitting no frame — silence here is
  indistinguishable from the CRIT-001 regression itself.
- `:3899` has no such guard today. The merged branch must not lose the emission for the
  return-to-default case if the lookup is somehow unavailable.

**(B) — fallback, only if the (A) delta is judged unacceptable at ratification:** add an explicit
field to `ToolExecEndPayload` (e.g. `SwitchedToDefault bool`) populated where the payload is built.
`ToolExecEndPayload` is an internal Go event type, not a gateway wire type, so Constraint #8 does
not apply and no contract regeneration is needed. It is exact rather than inferred, at the cost of
touching the event producer as well as the consumer. Not chosen because (A) needs no new plumbing
and its single delta is semantically defensible.

### 5.2.2a `pkg/gateway/replay.go` does NOT have the same shape — r2's claim was wrong

r2 wrote: "`pkg/gateway/replay.go` has the same exact-string shape for replayed system entries and
needs the equivalent treatment." **It does not match on a tool name at all.** Read at `aa97bcea`,
the condition is:

```go
if entry.Type == session.EntryTypeSystem && entry.AgentID != "" &&
    strings.HasPrefix(entry.Content, "Handoff:") {
```

It matches the **transcript entry's content prefix**, written by
`HandoffTool.Execute`'s audit step as
`fmt.Sprintf("Handoff: %s → %s. Context: %s", currentAgentID, agentName, contextMsg)`. Three
consequences, all different from what r2 implied:

1. **The tool rename alone does not break replay.** Renaming `hand_off` → `switch_agent` changes no
   string this condition reads. Left entirely alone, `replay.go` keeps working.
2. **But changing the audit-entry text does break it, silently** — and rewriting that text is the
   most natural thing in the world to do while renaming the tool ("Handoff:" reads stale once the
   tool is called `switch_agent`). The coupling is invisible: the producer is in `pkg/tools/handoff.go`
   and the consumer is a `HasPrefix` in `pkg/gateway/replay.go`, with no shared constant between
   them. **Decision: the audit-entry content prefix `"Handoff: "` is FROZEN by this ADR.** Do not
   rewrite it while merging the tools. If a future change wants to, it must introduce a shared
   exported constant first so the two ends move together. Recorded because a mechanical prose pass
   (§10's blanket grep) would otherwise reach straight for it.
3. **A pre-existing asymmetry surfaces, and D4 must decide it.** `ReturnToDefaultTool` writes
   `"Returned to default agent (%s)."` — which does **not** start with `"Handoff:"`. So
   **return-to-default has never emitted a replayed `agent_switched` frame**; on transcript replay
   the SPA's active-agent indicator shows the handoff but not the return. That is a live defect
   today, independent of this ADR, and `replay.go`'s own comment documents it as intentional
   ("`ReturnToDefaultTool` writes AgentID = returning agent (not target), so we only emit the switch
   frame for entries whose content starts with `Handoff:`").

   **Decision: out of scope for this ADR, recorded and left alone.** Fixing it means deciding what
   `AgentId` a replayed return-to-default frame should carry, which is the same
   `AgentID`-stamping-asymmetry question §5.1.2 already declines to reopen ("a separate question
   with its own hydration blast radius; a consolidation ADR is the wrong place to change it"). Being
   consistent with that, D4 preserves today's replay behaviour exactly: `switch_agent`'s
   agent-target branch keeps writing the `"Handoff: "` prefix and replays; its `target: "default"`
   branch keeps writing `"Returned to default agent (…)"` and does not. **File it as a follow-up
   issue at ratification** — it is a real UI gap and this ADR is the document that found it, so it
   should not evaporate into the surface table.

`replay.go`'s row in the table below is corrected accordingly: it needs **no edit**, and saying so
explicitly is the point — r2's version would have sent an implementer looking for an exact-string
branch that does not exist.

### 5.2.2b `pkg/agent/subturn.go` — the production `CloneExcept` call site, absent from r2's table

r2's table cited `pkg/tools/registry.go` for the `ExcludedHandoff` constant but **not the file that
calls `CloneExcept` with it**. That file carries two distinct problems, and the second sits exactly
where D1 and D4 intersect.

**(1) A hardcoded `"hand_off"` literal in an operator-facing debug log, independent of the constant.**
Immediately after the `CloneExcept(tools.ExcludedHandoff)` call, `spawnSubTurn` logs:

```go
agent.Tools = execSource.Tools.CloneExcept(tools.ExcludedHandoff)
// Log the constructed registry so operators can debug "my subagent has no tools" issues.
slog.Info("subturn: child registry constructed",
    "excluded", []string{"hand_off"},   // ← a SECOND, hand-copied literal
    "remaining_count", agent.Tools.Count(),
    "child_id", childID,
)
```

The `excluded` field is a hand-copied string, not derived from `tools.ExcludedHandoff`. Renaming the
constant does not touch it **by construction** — no compiler error, no test, nothing. Post-D4 this
line prints `"excluded": ["hand_off"]` forever, in the one log an operator consults to work out
*why* a subagent is missing a tool. Low blast radius, maximally misleading placement.

**Required fix — derive it, do not re-copy it:**

```go
"excluded", []string{string(tools.ExcludedHandoff)},
```

`ExcludedTool` is a defined string type (`type ExcludedTool string`), so a plain conversion is the
idiom — there is no `.String()` method on it. Fixing the literal once and leaving it hand-copied
would just reset the same clock; deriving it makes the drift impossible. This is the same
"derive, don't re-copy" instruction §2.1(c) gives for `loop.go`'s registration guard, and the two
should be done in the same pass.

**(2) A documented, still-open bug comment sitting directly on the D1 × D4 intersection.** The lines
immediately above the `CloneExcept` call read (abridged):

> *Known residual gap (not fixed here, documented only): unlike "delegate" above, "hand_off" is
> unconditionally excluded from EVERY child sub-turn's registry, and the SAME `load_tool`
> fabricated-success-then-`permission_denied` bug just cured for "delegate" is still live for
> "hand_off" — `canLoad`/`markLoaded` (`pkg/tools/tools_tool.go`) resolve the caller via
> `al.registry.GetAgent(callerID)`, the PERSISTENT top-level agent, not this ephemeral child's own
> registry, so `load_tool` can still report a fabricated success for "hand_off" here even though it
> is structurally absent from `agent.Tools`. Root-caused but out of scope for this fix.*

This names **both** tools this ADR renames: `load_tool` (D1) and `hand_off` (D4). It is the only
place in the repository where the two renames meet on a live, acknowledged defect, and neither r1
nor r2 mentioned it.

**Decision — three parts, none of which is "fix the bug here":**

- **The defect's mechanism is unaffected by either rename, and this ADR states that explicitly so
  nobody has to re-derive it.** The bug is a *caller-resolution* error: `canLoad`/`markLoaded` look
  the caller up in `al.registry` (the persistent top-level `AgentInstance`) instead of the ephemeral
  child's own cloned registry. That is a wrong-registry bug, not a wrong-name bug. Renaming
  `load_tool` → `ToolSearch` changes which tool *reports* the fabricated success; renaming
  `hand_off` → `switch_agent` changes which tool it fabricates success *for*. Neither touches
  `GetAgent(callerID)`. **The bug survives both renames, identically.** This sentence is the
  deliverable — the re-validation the r3 review asked for, done once, here, rather than left to an
  implementer mid-rename.
- **Update the comment's tool names to `ToolSearch` / `switch_agent` in the same commit** as the
  rename, and keep the "still live, out of scope" framing. A comment describing a live bug in terms
  of two tools that no longer exist is worse than no comment: the next reader cannot tell whether
  the bug was fixed along with the rename or merely mis-described.
- **Do not fix the bug in this ADR's workstreams.** It is a change to `pkg/tools/tools_tool.go`'s
  resolver contract (threading the child's own registry into `canLoad`), it has its own blast radius
  across every delegated sub-turn, and folding it into a rename is exactly the scope creep that made
  D4 hard to review. **File it as its own issue at ratification** — it has now been root-caused twice
  and deferred twice, which is how a documented defect becomes a permanent one.

**Also in this file, for §10's blanket pass:** `subturn.go` carries seven further `load_tool`
comment references and two more `hand_off` ones (the `~:910` *"needs the delegate/hand_off exclusion,
not a plain copy"* note and the `~:1040` *"hand_off remains excluded"* rationale). All prose, all in
scope for the same commit.

### 5.2.2c Full D4 surface

**Re-derived for r3 by a fresh blanket grep over `pkg/`, `src/`, `tests/`, `contracts/` and `docs/`
— not carried over from r2.** r2 labelled this table "Full surface, all confirmed by search in
session" and it was not complete; that phrasing is deliberately not repeated. Treat this as the best
available list and re-run the grep before W1 is called done (§10).

D1's surface is a separate, larger table — see **§2.1**.

| Area | Sites |
|---|---|
| Tool impl | `pkg/tools/handoff.go` (`HandoffTool`, `ReturnToDefaultTool` → one `SwitchAgentTool`) |
| Sub-turn security (constant) | `pkg/tools/registry.go::ExcludedHandoff` + `CloneExcept` (incl. `CloneExcept`'s own doc comment, which names `hand_off` twice) |
| **Sub-turn security (call site)** | **`pkg/agent/subturn.go::spawnSubTurn` — §5.2.2b. (1) the hand-copied `[]string{"hand_off"}` slog literal → `[]string{string(tools.ExcludedHandoff)}`; (2) the live `load_tool`/`hand_off` bug comment, renamed + re-validated + filed as its own issue; (3) two further prose references** |
| Catalog | `pkg/coreagent/core.go::allStaticToolNames` |
| Per-agent seeds | `pkg/coreagent/core.go` — 4 policy maps, 4 worker-exclusion lists |
| Global ceiling | `pkg/config/defaults.go` (`sandbox.tool_policies`) |
| Manifest | `pkg/tools/manifest.go::fullManifestToolNames` |
| Gateway/wire | `pkg/gateway/websocket.go` — **BOTH** exact-string branches (`p.Tool == "hand_off"` at `:3878` and `p.Tool == "return_to_default"` at `:3899`), collapsed into one per §5.2.2. Also two `load_tool` doc-comment references (`:49`, `:53`) for §10's prose pass |
| **Gateway/replay** | **`pkg/gateway/replay.go` — NO EDIT. It matches `strings.HasPrefix(entry.Content, "Handoff:")`, not a tool name; r2's "same exact-string shape" claim was wrong. The `"Handoff: "` audit-entry prefix is FROZEN by this ADR. See §5.2.2a** |
| Contracts | `contracts/components/schemas/AgentSwitchedFrame.yaml`, `contracts/asyncapi.yaml` — description prose names both tools; §9 |
| SPA | `src/lib/toolVisibility.ts` (the `case 'hand_off': case 'return_to_default':` arms), `src/lib/humanizeToolName.ts` (the `hand_off: 'Hand off'` entry in `EXPLICIT_LABELS`; keep the legacy `handoff` alias for old transcripts and add `switch_agent`) |
| Comments | Non-functional but in scope for the same pass — e.g. `pkg/agent/loop.go`'s *"Inject session key so handoff/return_to_default tools can address the session"*. See §10's blanket grep step. |
| Docs | `docs/tools-reference.md`, `docs/routing.md`, `docs/protocol/websocket-protocol.md` — see §10 W5 |
| Tests (existing, must be updated) | **11 files.** Go: `pkg/coreagent/constructor_seed_test.go`, `pkg/tools/sprint_h_registry_test.go`, `pkg/tools/delegate_grandchild_test.go`, `pkg/tools/manifest_test.go`, `pkg/agent/refactor_tool_enablement_test.go`, **`pkg/agent/sprint_h_subturn_test.go`**, **`pkg/agent/sprint_h_scenario_test.go`**, **`pkg/agent/subturn_delegate_nesting_test.go`** (r3 additions — see below), `tests/integration/handoff_agent_id_test.go`. TS: `tests/e2e/handoff.spec.ts`, `src/lib/toolVisibility.test.ts`, `src/lib/humanizeToolName.test.ts` |
| Tests (new, required) | Gateway/integration `AgentSwitchedFrame` coverage for **both** switch directions (§5.2.3); the reserved-`default`-agent boot case (§5.1.3) |

**On the three `pkg/agent` test files r2 omitted, and why the omission mattered less than r2's TS
omission but still mattered.** All three hardcode the literal `"hand_off"`:

- `pkg/agent/sprint_h_subturn_test.go` — `parentRegistry.CloneExcept("hand_off")` (the **raw string**,
  not `tools.ExcludedHandoff`, in a test whose stated purpose is to mirror the production wiring),
  plus a `require.True(t, hasHandoffBefore, …)` pre-condition and several `NotContains`/`Nil`
  assertions.
- `pkg/agent/sprint_h_scenario_test.go` — `CloneExcept("delegate", "hand_off")` at three call sites,
  `forbiddenTools := []string{"delegate", "hand_off"}`, and an assertion message naming
  "delegate+hand_off".
- `pkg/agent/subturn_delegate_nesting_test.go` — a comment reference only ("`hand_off` remains
  excluded"), in a test that drives the real `spawnSubTurn` path.

**These fail LOUDLY post-rename, which is the good outcome** — unlike §2.1's SPA case and unlike
`delegate_grandchild_test.go`'s vacuous assertion, nothing here ships green. The gap is a *planning*
gap, not a correctness one: `/taskify`'s W1 breakdown works from this table, so an unlisted file
becomes a CI failure the author discovers after opening the PR and patches in a follow-up commit.
That is avoidable friction, and — as the r3 review put it — a second instance of a "full surface"
claim that a one-second grep disproves.

**Test-hygiene item, independent of this ADR:** `sprint_h_subturn_test.go`'s
`CloneExcept("hand_off")` uses the raw literal where `tools.ExcludedHandoff` exists precisely so
call sites do not. Fix it while the file is open — it is the test-side twin of §5.2.2b's production
log literal, and the same argument applies.

**On the two TS test files, which r1 omitted from this row.** `src/lib/toolVisibility.test.ts`
asserts on the literals `'hand_off'`/`'return_to_default'`, and `src/lib/humanizeToolName.test.ts`
asserts on `'system.return_to_default'`. Their runtime risk is genuinely lower than the Go-side
risk r1 did analyze: `toolVisibility.ts`'s `default:` arm returns `true` for any unrecognized name,
so an unrenamed switch fails **open into the correct outcome** for `switch_agent` — visible, which
is what is wanted. But the tests themselves would go stale in the precise way §5.2 spends its
argument on: still green, now asserting the behavior of tool names that no longer exist and
silently not testing the tool that replaced them. Both files get the rename **and** an explicit
`switch_agent`-is-visible case, so the outcome is asserted rather than inherited from a fallback.

### 5.2.3 Required acceptance test for the frame emission

Non-negotiable deliverable of W1, because §5.2.1 establishes there is nothing in the repository
today that would catch this class of break. Gateway-level or integration, asserting on the emitted
`AgentSwitchedFrame`:

| Case | Setup | Assert |
|---|---|---|
| Switch to a named agent | `switch_agent(target: "<agent-id>")` succeeds | An `agent_switched` frame **is emitted**, with `agent_id` **set** to that agent |
| Return to default | `switch_agent(target: "default")` succeeds | An `agent_switched` frame **is emitted**, with `agent_id` **absent/nil** |
| Failed switch | `switch_agent` returns an error | **No** `agent_switched` frame is emitted |

The test must key off `p.Tool == "switch_agent"` plus the resolved active agent — **never on the
tool name alone**, which is the mechanism that broke. Per §5.2's own fail-open argument, the
assertions must be positive ("a frame with these contents was emitted"), not merely "the old name is
gone": a test that only checks absence passes against a code path that emits nothing at all, which
is exactly the regression being defended against.

### 5.3 Constraint #6: the catalog shrinks by one and the seeds must move with it

Removing two names and adding one takes `allStaticToolNames` from 90 to 89. Per CLAUDE.md Hard
Constraint #6 there is **no default-policy fallback anywhere** — every static builtin needs an
explicit, literal, wildcard-free policy entry for every agent, enforced at boot and at every
agent write. Concretely:

1. `allStaticToolNames`: drop both, add `switch_agent`. (Do this first — `validateOverrideKeys`
   **panics** on a seed override naming a tool absent from the literal, so a seed edit that lands
   before the catalog edit crashes at boot.)
2. Every per-agent seed policy map: replace both keys with `switch_agent`, same verdict (`allow` in
   all four core-agent seeds today).
3. `config.DefaultConfig().Sandbox.ToolPolicies`: same, keeping the one-for-one invariant its test
   asserts.
4. Worker seeds: the 4 exclusion lists name both tools, for the deliberate reason that a worker has
   no user-facing session to hand off — replace with the single new name, unchanged intent.
5. **Migration for persisted operator configs**, following ADR-036 §3.6 exactly: any persisted
   `Policies`/`ToolPolicies` key named `hand_off`, `return_to_default`, or `load_tool` (D1) is
   rewritten to `switch_agent` / `ToolSearch`, taking the **strictest** value where two legacy keys
   for the same target disagree (`deny` > `ask` > `allow`), and the legacy key is **deleted** on
   that same boot.

   **5a. Where it runs — and the failure this ordering exists to prevent, which is far worse than
   r1–r3 described.** r1–r3 said the risk was *"an explicit deny quietly becoming whatever the
   repair writes."* Read against the actual repair, the direction is inverted and the blast radius is
   every install. `pkg/gateway/gateway.go::repairAndValidateToolPolicyCoverage` runs
   `config.RepairIncompleteToolPolicyCoverage` **before** `ValidateToolPolicyCoverage`, and that
   repair backfills every uncovered `(agent, tool)` pair with an explicit **`deny`** — the
   fail-closed direction, correct for its own purpose. On the first boot after this ADR ships,
   `switch_agent` and `ToolSearch` are new names in `knownTools` with no policy entry anywhere.
   If the migration has not already run, the repair writes **`deny` for both, on every agent**, boot
   succeeds with no gap and no abort, and the config is persisted that way. Every agent silently
   loses the ability to hand off — and, because `ToolSearch` is denied, **loses the ability to load
   any lazy tool at all**, which after D3 is 71% of the catalog. An operator's explicit
   `hand_off: deny` migrating to `switch_agent: deny` is harmless; it is the `allow` that flips, and
   it flips everywhere.

   **Therefore: the migration runs as the FIRST statement inside
   `repairAndValidateToolPolicyCoverage`, before `RepairIncompleteToolPolicyCoverage`** — not merely
   "before the validator". Naming the validator alone would leave the real hazard wide open, since
   the repair sits between them and is the thing that writes. That helper is also the correct home
   for a second reason it already documents: it is called identically from boot
   (`RunContextWithOptions`) and hot-reload (`executeReload`), and it exists precisely "so the two
   can no longer silently diverge on what *repair then validate* means." A migration placed anywhere
   else would have to be duplicated at both call sites, which is the divergence that helper was
   created to end.

   **5b. Idempotency — by construction, no version marker.** The migration is a
   read-check-before-write keyed on the presence of a legacy key; with no legacy key present it
   writes nothing and returns. Three concrete cases, since CLAUDE.md documents that Windows has no
   cross-process file locking anywhere in this file-store family:

   - **Second run, same process or a later boot.** No legacy keys remain → no-op. Idempotent.
   - **Crash between rewrite and delete.** Not reachable as a partial *file*: config persistence is
     `fileutil.WriteFileAtomic` (temp file + rename), so the on-disk config is either wholly
     pre-migration or wholly post-migration. A crash before the write leaves a pre-migration file the
     next boot migrates normally. There is no torn intermediate state to be idempotent *against*.
   - **Two gateway processes racing the same `$OMNIPUS_HOME` on Windows.** Both read the same
     pre-migration input and compute the same deterministic output, so a lost update writes
     byte-identical content. The only divergent interleaving is one process reading pre-migration
     while the other has already written post-migration — and re-running on a post-migration config
     is the no-op case above. (This does **not** make concurrent gateways on one home directory safe
     in general — CLAUDE.md's Windows caveat still stands for every other writer. It makes *this*
     migration not the thing that breaks.)

   **One non-obvious rule makes the fold idempotent as well as terminating: a pre-existing
   `switch_agent` / `ToolSearch` value participates in the strictest-wins fold.** Without it, an
   operator config carrying both `switch_agent: deny` (hand-added, or written by a prior repair per
   5a) and `hand_off: allow` would have the deny *weakened* to allow by the migration. Including the
   destination key in the fold makes the operation monotone in the strict direction and therefore
   safe to re-run against its own output.

   **Required tests:** (i) a config with only `hand_off: allow` boots with `switch_agent: allow` and
   no `deny` backfill — the 5a regression, which otherwise ships green because boot succeeds;
   (ii) running the migration twice over the same config produces identical bytes; (iii)
   `hand_off: deny` + `return_to_default: allow` → `switch_agent: deny`, both legacy keys absent;
   (iv) `switch_agent: deny` + `hand_off: allow` → `switch_agent: deny` (the fold rule above).

---

## 6. D5 — Move the static catalog inside a prompt-cache boundary

### 6.1 Decision

Split `BuildCompressedManifest` into two functions along the **stable / per-turn** seam:

- `BuildStaticToolCatalog(lazyTools []Tool) string` — the preview lines for the Tier 2 catalog,
  grouped by category, sorted. **Independent of session state**, so byte-identical across turns for
  a given agent and policy set. Moves into `messages[0].SystemParts` as a `ContentBlock`
  **immediately after** the existing `staticPrompt` block, carrying its own
  `CacheControl{Type: "ephemeral"}`.
- `BuildLoadedToolsDelta(loaded map[string]bool) string` — one short line naming which catalog
  entries are already loaded and directly callable. Stays **outside** the cached boundary, as the
  index-1 system message `injectManifestNote` already produces, so the loaded set is never stale.

### 6.2 The ordering constraint is the whole decision

Anthropic prompt caching is a **prefix match**: a block caches everything up to and including
itself, and any byte change anywhere earlier invalidates it. Render order is `tools` → `system` →
`messages`. **[EXTERNAL]**

Today `pkg/agent/context.go`'s `contentBlocks` is `[staticPrompt (cached), dynamicCtx, skills,
breadcrumb]`, and `dynamicCtx` changes every turn (it carries the time). The catalog block must be
inserted at **index 1**, between `staticPrompt` and `dynamicCtx`. Placed anywhere after
`dynamicCtx` it would cache nothing at all — the prefix it depends on changes every single turn.
This is the one detail an implementer can get wrong while producing something that looks right and
measures as a no-op.

Result: two breakpoints (after `staticPrompt`, after the catalog) out of Anthropic's maximum of
**4 per request**. **[EXTERNAL]**

### 6.3 The minimum-size concern resolves favorably

Anthropic will not cache a prefix below roughly **1024 tokens**; shorter prefixes silently fail to
cache. **[EXTERNAL]** The threshold applies to the **cumulative prefix**, not to the individual
block — and the catalog block's prefix already contains the whole static system prompt, which is
far larger than 1024 tokens on any real agent. So the 22-line Tier 2 catalog does **not** need to
clear 1024 tokens on its own. The concern that motivated this check does not bind.

Two honest caveats. The default Anthropic cache TTL is **5 minutes**; **[EXTERNAL]** a conversation
with gaps longer than that pays the write cost repeatedly. (This is the provider's *cache* TTL and
is unrelated to `cfg.Tools.MCP.Discovery.TTL` — §1.1.1's correction does not touch it.) And the
block is only worth ~22 lines of text after D3 shrinks Tier 2 — this decision is worth far less
*after* D3 than it would have been before it, and that should be stated rather than quietly
enjoyed. D3 is where the token win is; D5 makes the remainder free rather than recurring.

### 6.4 The finding that matters more than D5 itself

Because `tools` renders **before** `system`, **any change to the callable tool array already
invalidates the entire system-prompt cache** — including the `staticPrompt` block that has carried
`cache_control` since issue #607. Every `ToolSearch` promotion changes the array.

**r4 correction, and it lands in D5's favour.** r1–r3 added *"So does every TTL expiry, since
`GetAll` drops an entry once `TTL <= 0`."* Per §1.1.1 that is true only for MCP tools. A **static**
tool never leaves the array before session close, so it churns the array **once** — at the turn it is
loaded — and never again. The practical consequence is the opposite of what the old sentence
implied: the population of stable-prefix turns is **larger** than r1–r3 assumed, because there is no
second, deferred invalidation trailing every promotion by five turns. Churn events per session are
bounded by the number of *distinct* tools loaded, not by twice that number, and they cluster early
in a session rather than recurring throughout it. This directly strengthens §6.6's case for shipping
D5, and §3.3 cites it as one of the four reasons for rejecting a decay mechanism: adding eviction
would restore precisely the second invalidation this correction removes.

This means the 2026-06 design doc's stated rationale for keeping the manifest outside the cached
block — "so it can be rebuilt fresh every turn without invalidating the cache when the loaded-tools
set changes" — **was already defeated**: on exactly those turns the whole prefix is cold regardless
of where the manifest sits. D5 is still correct (it makes every *other* turn cheaper, which is
most of them), but the larger prize is reducing how often the tool array churns at all. That is a
cross-cutting caching question spanning the whole agent loop, which is precisely what
[#654](https://github.com/elicify-ai/omnipus/issues/654) tracks. **Out of scope here**; recorded so
#654 starts from this finding rather than rediscovering it.

One thing that is *not* a problem, checked: tool ordering is deterministic.
`ToolRegistry.GetAll` iterates `sortedToolNames()`, not the map, so the array is stable across
requests for a stable tool set. Had it been map-ordered, nothing in this system would ever have
cached anything and D5 would be moot.

### 6.5 Anthropic-only, and it is measurable with what already exists

`providers.CacheControl` is honored in exactly one place: `pkg/providers/anthropic/provider.go`'s
`buildParams`. The OpenAI-compatible path **strips** `SystemParts` entirely
(`pkg/providers/common/common.go`, asserted by `TestSerializeMessages_StripsSystemParts`), so those
providers see only the concatenated string and get no explicit marker — though they still benefit
from the *reordering*, since providers that do automatic prefix caching reward stable-before-
volatile regardless of markers. **Bedrock reads neither `SystemParts` nor `CacheControl`** — it
builds system content from the concatenated `Content` string alone, so it lands in the same place as
the OpenAI-compatible path by a different mechanism (never reads them, vs. strips them). Verified in
r2; full evidence in §12.

The win is measurable without new telemetry: `providers.Usage` already carries `CacheReadTokens`
and `CacheWriteTokens`, and the Anthropic adapter already populates them. The acceptance test is a
before/after on `CacheReadTokens` across a multi-turn session with no tool loads — it should rise by
approximately the catalog block's token count per turn, and `estimateMessageTokens` can measure
that count directly.

### 6.6 Ship D5 now, or defer it into #654? — DECIDED: ship, last, behind a gate that can veto it

The review declines to let this sit implied, and it is right to: §6.4 is this ADR's own finding that
*any* tool-array change already invalidates the entire cached prefix, and §6.3 already concedes D5
"is worth far less after D3 than it would have been before it." Read together, they say D5's benefit
accrues only on turns where nothing is promoted (§6.4's r4 correction removes the "and nothing
expires" half, which materially widens that set) — while §6.2 says D5 carries the
single riskiest implementation detail in the document, one whose failure mode is a silent no-op.
A fragile mechanism for a benefit the document itself questions is a fair thing to challenge.

**Decision: D5 ships, sequenced last, with §6.5's acceptance test as a hard merge gate that is
empowered to reject it.**

The reasoning, in the order that actually decides it:

1. **The fragility argument is neutralized by measurement that already exists.** OBS-001's real
   worry is not "D5 might be small" — it is "D5 might be fragile *and* nobody would know." The
   second half does not hold. `providers.Usage.CacheReadTokens`/`CacheWriteTokens` are populated by
   the Anthropic adapter today, and `estimateMessageTokens` can size the catalog block directly. The
   index-1 ordering error §6.2 warns about produces exactly zero cache reads, which this measurement
   sees immediately. A silent no-op that is trivially observable is not a silent no-op.
2. **Deferring saves nothing.** D5's edit is the block ordering in `pkg/agent/context.go`'s
   `contentBlocks` — the exact structure #654 must touch anyway. Moving it to #654 transfers the
   work rather than avoiding it, and #654 then inherits an unverified hypothesis instead of a
   measured baseline.
3. **It makes #654 strictly better-informed.** #654's own sizing question is "how often does the
   tool array actually churn per session." Shipping D5 with a measured cache-read delta answers the
   adjacent half of that empirically: it establishes what a stable-prefix turn is worth, so #654 can
   reason about churn frequency against a real number rather than estimating both sides.

**The gate, stated so it can genuinely fail — with a numeric tolerance, because "approximately" is
a judgment call, not a measurement.** r2 wrote "rises by approximately the catalog block's token
count," which puts the pass/fail reading back in the hands of whoever wants the merge. Pinned:

Let `B = estimateMessageTokens(BuildStaticToolCatalog(...))` — the catalog block's own token count —
and let `ΔC` be the increase in `providers.Usage.CacheReadTokens` on a no-load turn versus the same
turn measured before D5.

- **PASS: `ΔC ≥ 0.8 × B`** on at least one no-load turn of a multi-turn Anthropic session.
- **FAIL: `ΔC < 0.8 × B`.** D5 does not merge; it is dropped into #654 *with the measurement
  attached*, and D1–D4 merge without it.

**Why a one-sided 80% floor and no upper bound.** The failure this gate exists to catch is §6.2's
index-1 ordering error, whose signature is `ΔC = 0` — not a near miss. Any threshold between "zero"
and "the full block" separates the two outcomes; 0.8 leaves room for tokenizer disagreement between
`estimateMessageTokens`'s estimate and Anthropic's actual count without admitting a no-op. There is
deliberately **no upper bound**: `ΔC` legitimately exceeding `B` means the cache boundary is also
covering text beyond the catalog block, which is a better result, not an anomaly.

**Where the gate runs, and who owns it — DECIDED (r4), because r3 specified a merge blocker with no
home.** The measurement needs a live call to the real Anthropic API (`CacheReadTokens` is populated
by the Anthropic adapter alone, §6.5) and a before/after comparison across two builds.

**Decision: it is a MANUAL, one-off, pre-merge acceptance measurement, owned by the operator
(Daniel Piatkowski), with the two numbers — `B` and `ΔC` — pasted into the W2 pull request
description as the merge artifact. It is NOT added to CI.** Three reasons:

1. **It would be the only credentialed, network-dependent gate in the suite.** CLAUDE.md's
   Testing & Building section describes a Go suite that runs on an ephemeral CI worker with no
   external API credentials anywhere. Adding a funded Anthropic key plus egress to that worker is a
   real posture change — a live secret in CI, a per-run cost, and a gate that can fail from a rate
   limit or a provider outage rather than from a defect. That trade might be worth making for a
   permanent regression test. It is not worth making for a check that runs **once**.
2. **CI structurally cannot run it as specified.** `ΔC` is defined as "versus the same turn measured
   **before** D5" — a comparison across two builds. A single CI job has one build; automating this
   would mean orchestrating a paired run and persisting a baseline, which is more machinery than the
   decision it gates.
3. **A named owner is what makes a manual gate real.** An unowned manual check is how a gate becomes
   a formality. The operator runs it, records both numbers in the PR body, and the PR does not merge
   without them — the same shape as the human-approval rule CLAUDE.md already applies to `main`.

**What guards D5 *after* it merges, since the manual gate runs only once — an offline structural
test, required in W2.** §6.2 identifies the failure as a specific, checkable structural error: the
catalog block landing anywhere other than index 1 of `pkg/agent/context.go`'s `contentBlocks`, or
landing there without `CacheControl{Type: "ephemeral"}`. Assert exactly that — the block is at
index 1, immediately after `staticPrompt` and before `dynamicCtx`, and carries the cache marker.
It needs no network, no credential and no provider, it runs on every PR forever, and it fails on the
one mistake whose live signature is `ΔC = 0`. **This is a better answer than either option the
review offered:** the live measurement proves the mechanism works once, and the offline test stops
it silently regressing thereafter. Neither substitutes for the other, for the same reason §4.3.1
gives about detectors and undos.

D5 is independent of the other four (§10 W2), so this veto costs nothing but D5 itself.

This is what turns "is D5 worth it?" from a judgment call into a measurement, which is the right
resolution for a decision whose own ADR is ambivalent about its size.

---

## 7. Consequences

### Positive

- The per-turn manifest block drops from **~101 rendered lines (71 tools across 14 categories) to
  22 (8 tools across 6)**, and what remains moves inside a cache boundary — the recurring cost of
  tool exposure goes from "~101 lines every turn, uncached" to "22 lines, paid once per cache
  window." (r1–r3 quoted "71 → 8"; those are tool counts, not line counts. §4.3's size note derives
  both figures from `BuildCompressedManifest`'s actual output.)
- `bash` no longer has a permanent visibility advantage over the narrower tools it competes with,
  removing the gradient identified in F1.
- The memory trio is uniformly Tier 1, closing an invisible failure mode: under ADR-028 the sliding
  window is the only history, and a recall tool the agent cannot see reads as amnesia.
- A wrong BM25 top guess is mitigated rather than paid for in full, using scores the engine already
  computes and currently discards.
- One tool identity for agent switching instead of two, continuing ADR-036's pattern; and the
  migration closes the fail-open policy-key gap that a bare rename would have opened.
- §6.4's finding gives #654 a concrete, evidence-backed starting point.

### Negative / accepted

- **71% of the catalog becomes invisible by default** (§4.3). Accepted deliberately by the
  operator. No longer accepted *blind*: §4.3.1 makes two counters and a `PreviewAllLazy` revert flag
  hard prerequisites of D3 shipping, so the risk now has both a detector and an undo.
- **Two new pieces of surface exist only to service that risk** — two `atomic.Uint64` counters and
  one config bool. The flag is explicitly time-boxed for deletion once the counters produce data
  (§4.3.1); if that follow-up is not filed at ratification it will quietly become permanent.
- **Ambiguous searches promote up to 2 extra tools that may go unused, and a promoted static tool
  is carried in the callable set for the REST OF THE SESSION** — there is no reclamation and none is
  being built (§3.3, decided). r1–r3 claimed "up to 5 turns, reclaimed by existing TTL"; the TTL
  path is a structural no-op for every static tool (§1.1.1). The cost is still bounded — the ceiling
  is the agent's own policy-allowed lazy catalog, at most 71 schemas, which is exactly what the
  system sent every turn before the manifest optimisation existed — but it is a session-long cost,
  not a five-turn one, and each promotion also costs one prompt-cache invalidation at the turn it
  lands (§6.4).
- **The loaded-tools bucket has to be re-keyed to `(agent, session)` for D3's "invisible by default"
  to be true at all** (§4.6). Today it is keyed by session id alone, and D4's own `switch_agent`
  changes the active agent inside a session — so without this fix a Tier 3 tool found by one agent
  becomes callable by the next agent in the same conversation with no search of its own. Net-new
  work this ADR did not budget for in r1–r3, and a prerequisite of §4.3's accepted-risk framing
  rather than a refinement of it.
- **The `no_followup` counter had to be redesigned, not merely rewired** (§4.3.1a). r3 specified it
  against `TickTTL`, which cannot fire for static tools; it now needs a small side table and a
  per-turn sweep in `pkg/agent`. Still a hard prerequisite of D3 shipping — the mechanism changed,
  the commitment did not.
- D4 touches a security-relevant exclusion (`ExcludedHandoff`) whose failure mode is silent and
  whose current tests would not catch it. This is real risk that must be paid down with tests
  asserting the *new* name's absence, not merely the old name's.
- **D4 also touches a UI-visible frame emission that has no test coverage whatsoever today**
  (§5.2.1). The `hand_off`-success → `AgentSwitchedFrame` path is untested anywhere in the
  repository, so the rename's most likely regression would ship green. W1 must add that coverage
  (§5.2.3); this is net-new test debt being paid, not merely preserved.
- **`switch_agent` declares its note parameter optional where `hand_off` declared `context`
  required** (§5.1.1). Deliberate, and near-costless because the requirement was never enforced at
  runtime — but it is a real relaxation of the declared schema, recorded as such rather than
  described as unchanged.
- A second `cache_control` breakpoint consumes one of four. Ample headroom, but it is finite and
  #654 may want breakpoints of its own.
- D5's benefit accrues to Anthropic only, and is materially smaller *because* D3 already shrank the
  block it caches.
- The `ToolSearch` name breaks this codebase's otherwise-uniform `snake_case` tool naming (§2).
- **D1's blast radius is larger than D4's and was under-scoped through two full revisions**
  (§2.1). It touches a contract file, a user-visible SPA behaviour, 19 test files and a production
  registration guard. It is now its own workstream (§10 W-D1) rather than a mechanical pass, which
  is more planning cost than r1/r2 budgeted for.
- **D1 carries one user-visible regression risk that ships green if missed**: `toolVisibility.ts`
  un-hiding every `ToolSearch` call in every chat thread (§2.1(a)). The SPA suite passes either way,
  which is why it is called out at the top of §2.1 rather than buried in a table row.
- **D2's multi-promotion widens per-query schema exposure** from one tool to up to three, including
  destructive Tier 3 verbs. Bounded by `canLoad` (no privilege escalation) and narrowed by excluding
  `ask`-policy and administrative tools from the speculative cross-category band, but the confident
  band is unrestricted by design (§3.2).
- **`ToolSearch`'s match list was never policy-filtered, and D3 is what made that matter** (§3.2.2).
  The list of `{name, description}` results is built before any `canLoad` runs and returned in full
  on every path, so a policy-**denied** Tier 3 tool's name and full description could reach any
  agent on a top-5 BM25 placement from an unrelated query. Pre-D3 the gap was moot (all 71 lazy
  tools are named in the block anyway); post-D3 it is the failure of the exact property D3 exists to
  create. Net-new work, and a **second** prerequisite of §4.3's headline number alongside §4.6 —
  which means "71% invisible by default" depended on two fixes this ADR did not budget for in r1–r4,
  not one.
- **The "administrative" exclusion named a mechanism that does not exist** (§3.2.1). r1–r4 wrote
  "whose category is administrative"; `Tool.Category()` is a **domain** enum with no such value, and
  the destructive tools share their category with their read-only siblings (`delete_agent` with
  `list_agents`, `set_config` with `get_config`). It is now an explicit 13-name set with a four-part
  drift test — real surface where there had been a phrase, and one more list a new tool must be
  adjudicated against.
- **The `administrativeToolNames` drift test cannot be complete, and says so** (§3.2.1). No test
  catches a destructive tool whose name reads benign; the regex tripwire is a backstop and the real
  guarantee is the human decision §4.4's drift test already forces. That is an honest process
  guarantee, not a mechanical one, and it is weaker than every other guard in this document.
- **`pendingSearchPromotions` adds a second map to keep in sync with `loadedTools`** — same
  composite key, same closure, same sweep (§4.3.1a, §4.6 point 4). Correct, specified, and tested,
  but it is a second thing that must be re-keyed and re-swept every time the first one is, and
  `forgetSession` now sweeps two maps where it swept one.
- **`switch_agent` applies `hand_off`'s 10-second timeout to the `target: "default"` path**, which
  has none today (§5.1.2). Judged harmless — that branch does no I/O — but it is a real new
  constraint, not a merge no-op.
- **Two follow-up issues must be filed at ratification or they evaporate**: the
  `load_tool`/`hand_off` fabricated-success resolver bug (§5.2.2b — now root-caused and deferred
  *twice*), and replayed `agent_switched` frames never firing for return-to-default (§5.2.2a). Both
  are pre-existing defects this ADR surfaced rather than created; both are exactly the kind that
  disappear when the ADR that found them ships without filing them.

- **The legacy-policy-key migration is a boot-order-critical change, not a tidy-up** (§5.3 item 5a).
  Placed after `RepairIncompleteToolPolicyCoverage` instead of before it, the repair backfills the
  two new names to explicit **`deny` on every agent** and boot succeeds — silently disabling agent
  handoff and, because `ToolSearch` is denied, all lazy-tool loading, i.e. 71% of the catalog. It
  ships green. This is the highest-severity single item r4 adds, and it was found by r4's own grep,
  not by any of the three reviews.

### Corrections this revision makes to r3's own text

- §3.3's *"Reclamation is the existing TTL, unchanged"* — **wrong for every tool this ADR is
  about.** `PromoteTools`/`TickTTL` are no-ops on `IsCore` entries and every static tool is core.
  Rewritten as an explicit decision (§3.3); the underlying mechanism is now documented once, at
  §1.1.1, rather than assumed at three separate call sites.
- §4.3.1(a)'s *"hook the existing TTL-expiry path in `pkg/tools/registry.go`"* — **unbuildable.**
  The hook cannot fire for the population it exists to measure. Redesigned (§4.3.1a).
- §7's *"Bounded and reclaimed by existing TTL; no new mechanism"* — corrected above.
- §6.4's *"So does every TTL expiry"* — wrong, and wrong in D5's **favour**: static promotions churn
  the tool array once, not twice. Corrected in §6.4.
- §4.4's `ManifestSearchOnly` doc comment (*"still promoted with the same TTL"*) — would have
  shipped the same error into production source. Corrected.
- §4.3/§4.1/§6.3/§7's *"~71 lines to 8"* — those are tool counts. The rendered block is ~101 lines
  and 22 lines. Corrected everywhere, with the derivation in §4.3.
- §5.3 item 5's *"an explicit deny quietly becoming whatever the repair writes"* — the direction is
  inverted; the repair writes `deny`, so it is the **allow** that flips. Corrected in §5.3 5a.
- The r2 revision-history line crediting the r1 review with confirming *"TTL mechanics… correct"* —
  it confirmed three functions behave as written without asking whether they apply here. Annotated
  rather than deleted, because the failure is the interesting part.

### Corrections this revision makes to r2's own text

Recorded separately from the consequences because a reader diffing r2 → r3 needs to know which
changes are *new decisions* and which are *r2 being wrong*:

- §5.2's "**silently** … no failing test" — wrong twice. `CloneExcept` emits a purpose-built
  `slog.Warn` on a missing exclusion name, and three of the four named tests fail loudly. The one
  that does not (`delegate_grandchild_test.go`) is vacuous *today*, for a different reason than r2
  gave. Corrected in §5.2.
- §5.2.2's "`pkg/gateway/replay.go` has the same exact-string shape" — wrong. It matches a transcript
  **content prefix**, not a tool name, and needs no edit. Corrected in §5.2.2a.
- §9's "tier assignment is entirely server-side" — wrong. `manifest_tier` is a wire enum on
  `AgentToolEntry`, whose description names `load_tool` twice. Corrected in §9.
- §4.3.1(a)'s "no metrics backend," modelled on `handoffTranscriptWriteFailures` — a working
  `/metrics` endpoint and recorder pattern already exist for exactly this. Corrected in §4.3.1(a).
- §5.2.2's "Full surface, all confirmed by search in session" — not complete. Corrected, and the
  claim itself is no longer made: §5.2.2's table is now labelled "re-derived for r3 by a fresh
  blanket grep," and §10 instructs re-verification rather than trust.

---

## 8. Alternatives considered

**A. Widen Tier 2 instead of introducing Tier 3 — e.g. keep ~30 tools previewed.** Rejected by the
operator in favor of the sharper split. Worth naming because it is the natural fallback if §4.3's
revisit trigger fires: the mechanism D3 builds supports any Tier 2/Tier 3 partition, so widening
later is a data change, not a redesign. That is a deliberate property of the design.

**B. Express Tier 3 as a fourth `ManifestTier` constant.** Rejected on evidence, not taste: it
would break `SnapshotSearchableTools`'s `== ManifestLazy` test and delete every Tier 3 tool from
the BM25 corpus — removing the only way to reach them. See §4.4.

**C. Collapse search and execute into one `ToolSearch` call.** Rejected — full rationale in §3.5.

**D. Keep `hand_off`/`return_to_default` and add `switch_agent` as an alias.** Rejected: it is the
permanent-dual-key pattern ADR-036 §3.6 explicitly refused, and it would leave three tool identities
for one capability — worse than the two this ADR is consolidating.

**E. Fold the loaded-set delta into the cached block too.** Tempting, since the delta changes only
when the loaded set changes, which is exactly when the tool array has already invalidated the whole
prefix anyway (§6.4) — so it would cost nothing. Rejected as a false economy: it makes cache
correctness depend on a subtle two-step argument about `tools`-before-`system` render order, for a
saving of roughly one line of text. Keeping the delta outside is trivially, locally correct.

**F. Make the ambiguity ratios operator-configurable.** Rejected: new config surface for a heuristic
no operator can meaningfully tune without the usage data nobody currently collects. If §4.3's data
collection ever lands, revisit.

---

## 9. Contract impact (Constraint #8)

Small but non-zero, and it is not zero the way it first appears.

- **No new or changed wire *type*.** `ToolSearch`'s result is a tool-result string, not a gateway
  wire format. Tool-policy maps are `additionalProperties`-shaped with no fixed enum, so renaming a
  policy key is not a schema change — the same conclusion ADR-036 §6 reached.
- **CORRECTION (r3): "tier assignment is entirely server-side" was wrong, and D1 has a second
  contract edit r1/r2 both missed.** `contracts/components/schemas/AgentToolEntry.yaml` — the
  per-tool entry returned by `GET /api/v1/agents/{id}/tools` — carries a **`manifest_tier` wire
  enum** (`full` / `compressed` / `infra`), populated server-side by
  `pkg/gateway/rest_tool_registry.go::manifestTierToWire` from `tools.ToolManifestTier`. Its
  `description` **names `load_tool` twice**:

  > *"…"compressed" = listed by name only in the system context, schema fetched on demand via
  > **load_tool**; "infra" = always-callable discovery tool (**load_tool** / search_tools_\*) that
  > drives the manifest mechanism itself…"*

  This is a Constraint #8 single-source-of-truth file. D1's rename requires the 5-step process on
  it exactly as D4 requires it for `AgentSwitchedFrame`: edit the schema, run
  `scripts/gen-contracts.sh`, commit the generated diff atomically. The regeneration propagates to
  `pkg/api/generated/openapi_types.gen.go` (**4 sites** — the description is duplicated onto both
  `AgentToolEntry` and `AgentToolsResponse`, each with a type alias), `src/lib/api/generated/openapi-types.ts`,
  and the `pkg/gateway/inboundschemas/AgentToolEntry.yaml` mirror. `make verify-contracts` fails
  otherwise. **Fix the second staleness in the same edit:** `search_tools_*` refers to
  `search_tools_bm25`/`search_tools_regex`, which were retired when they were merged into `load_tool`
  on 2026-06-26 (`pkg/agent/loop.go`'s registration comment records the merge). The description has
  been describing two non-existent tools since then.
- **D3 and the `manifest_tier` enum: no wire change, deliberately — recorded rather than left
  unasked.** D3 keeps Tier 3 tools at `ManifestLazy` (§4.4 — they must stay there or
  `SnapshotSearchableTools` drops them from the BM25 corpus), so `manifestTierToWire` keeps mapping
  them to `compressed` and **the enum does not change**. The consequence is that the wire cannot
  express the Tier 2 / Tier 3 distinction D3 creates: all 71 lazy tools report `compressed`
  identically, with no field saying which 8 are previewed. **Decision: accept, do not add a field.**
  Grepped and verified: `manifest_tier` has **no consumer in the SPA today** (zero non-generated
  hits in `src/`), so nothing renders a distinction it would now be missing, and adding a wire field
  for a UI that does not exist is surface for its own sake. If the agent-tools panel ever surfaces
  tier, the right shape is a separate `manifest_visibility` field mirroring §4.4's second axis —
  **not** a fourth `manifest_tier` enum value, which would repeat §4.4's rejected mistake on the
  wire. Noted here so that decision starts from the right place.
- **`AgentSwitchedFrame` description prose does change.** Both
  `contracts/components/schemas/AgentSwitchedFrame.yaml` and `contracts/asyncapi.yaml` name
  `return_to_default` in their descriptions. Those files are the single source of truth, and
  description-only edits still regenerate. Per the 5-step process: edit the schema, run
  `scripts/gen-contracts.sh`, commit the generated diff atomically with the spec change. `make
  verify-contracts` fails otherwise.
- **`pkg/gateway/inboundschemas/AgentSwitchedFrame.yaml` is a generated sync target, not a second
  hand-maintained copy — do not edit it directly.** `scripts/gen-contracts.sh` mirrors
  `contracts/components/schemas/*.yaml` into that directory, and `make verify-contracts` diffs it
  too, so `make gen-contracts` picks it up automatically and the 5-step process above already covers
  it. Named explicitly because it surfaces in any plain grep for `return_to_default` right alongside
  the canonical schema, and a reader doing that grep will otherwise pause to work out whether it is a
  second edit site. It is not.
- The frame's **semantics** are unchanged — a null/absent agent still means "returned to default."
  Only what produces that condition changes (a `target` value instead of a tool name), and that
  logic is server-side in `pkg/gateway/websocket.go`.

---

## 10. Implementation approach — parallelizable workstreams

Input for `/plan-spec` and `/taskify`; not a final task breakdown. The operator has asked for
parallel execution. The real dependency graph:

**Genuinely independent — start together:**

- **W1 = D4 (`switch_agent`).** Touches `pkg/tools/handoff.go`, `registry.go`'s exclusion constant,
  `pkg/coreagent/core.go`, `pkg/config/defaults.go`, the config migration, `pkg/gateway`, the
  contracts, and the SPA. It intersects the others in exactly one place: `fullManifestToolNames`,
  where it adds one name. That is a one-line collision, not shared design.
- **W2 = D5 (cache boundary).** Touches `pkg/agent/context.go` (block ordering) and adds two
  builder functions to `pkg/tools/manifest.go`. Its only real coupling to W3 is that both edit
  `manifest.go`.
- **W3 = D3 (visibility mechanism).** Adds `ManifestVisibility` + `ToolManifestVisibility` +
  `previewedLazyToolNames` to `pkg/tools/manifest.go`, one filter line in
  `BuildCompressedManifest`, the drift test, and the Tier 1 list rewrite.
  **r4 adds two items to W3, both prerequisites of D3's stated properties rather than extras:**
  - **§4.6's bucket re-key** — `manifestSessionID` → `manifestBucketKey(agentID, …)` in
    `pkg/agent/tool_manifest.go`, the matching change at the one writer and two readers,
    `forgetSession`'s exact-delete → suffix sweep, and the three tests in §4.6. Without it §4.3's
    "71% invisible by default" is not true per agent. **This must land with D3, not after it** — a
    per-session-only bucket is a weaker security property than the one the operator accepted.
  - **§4.3.1(a)'s redesigned `no_followup` counter** — the `pendingSearchPromotions` side table, the
    write in the `markLoaded` closure, the clear on dispatch, the sweep at the existing `TickTTL`
    call site and in `forgetSession`, and `tools.RecordToolSearchNoFollowUp()`. Note this now sits
    in `pkg/agent`, not `pkg/tools` — a scope shift from r3's plan.
  - **§3.3's permanence regression test** (static tool survives >TTL turns; MCP tool does not).

  **r5 adds two more items to W3, and moves one boundary between W3 and W4:**
  - **§3.2.2's match-list policy filter** in `pkg/tools/tools_tool.go::execSearchAndLoad` — the
    whole-corpus rank, the `docCount` field on `bm25CachedEngine`, the filter-and-truncate pass, the
    fail-closed empty and nil-resolver paths, and the four tests. **This lands in W3, not W4, even
    though the file is W4's**, and the reason is a sequencing hazard rather than tidiness: §10
    sequences W3 → W4, so leaving the filter in W4 would ship D3 into a window where its stated
    property is false on the discovery channel. It is the same "prerequisite, not refinement"
    treatment §4.6 already gets, for the same reason.
  - **§3.2.1's `administrativeToolNames` set and its drift test** in `pkg/tools/manifest.go` —
    adjacent to `previewedLazyToolNames`, which W3 is already adding. The set is *consumed* by D2
    (W4); defining it in W3 keeps both name-set drift tests in one commit and one reviewer's head.
  - **Boundary move:** because §3.2.2 already converts `execSearchAndLoad`'s `canLoad` loop from
    short-circuit to full-scan and produces the policy-loadable list with scores, **W4's D2 work
    reduces to the ambiguity ratios and the multi-promote on top of that list.** This removes the
    textual conflict W3 and W4 previously had inside one function, and it means the r5 fix is not
    additive cost to D2 — it is D2's loop change, done earlier and for a stronger reason.
  - **Two same-class defects found by r5's own grep** (§16): delete the unfiltered, test-only
    `ToolRegistry.SearchBM25`, and pass the policy-filtered slice to `FindClosestToolName` in the
    `canLoad` closure. Both are small; both are on this surface; neither is in scope for W4.

- **W5 = documentation.** New in r2; r1 had no documentation workstream at all, which the review
  correctly called a self-inflicted instance of the exact drift CLAUDE.md is full of warnings about.
  Three user/developer-facing reference docs go stale from D4 and none of them appears in §5.2's
  blast radius:

  | File | Current content | Required change |
  |---|---|---|
  | `docs/tools-reference.md:67` | `` `handoff` `` — **already stale today**: the tool has been named `hand_off` for some time, and the row still says `handoff` | Replace both rows with one `switch_agent` row |
  | `docs/tools-reference.md:68` | `` `return_to_default` `` row, citing `pkg/tools/handoff.go:312` | (as above; drop the stale line-number citation) |
  | `docs/routing.md:143` | "any agent in the chain can call `return_to_default`" | Name `switch_agent(target: "default")` |
  | `docs/routing.md:155` | "documents `handoff`, `return_to_default`, and the rest" | Name `switch_agent` |
  | `docs/protocol/websocket-protocol.md:484` | `agent_switched` "(handoff or return_to_default)" | Describe it as `switch_agent` in either direction |

  That `docs/tools-reference.md:67` is *already* wrong before this ADR touches anything is the
  argument for the workstream, not a reason to widen it: there is no `make verify-contracts`
  equivalent for prose, so the only thing preventing drift here is doing it in the same commit.
  **W5 lands in the same commit as W1's rename** — a separate follow-up commit is how this row
  became stale in the first place.

  Two scoping notes: D1's `load_tool` → `ToolSearch` rename needs **no** *external* doc changes —
  `grep -rn 'load_tool' docs/*.md docs/protocol/ docs/operations/` returns zero hits, so the tool is
  undocumented outside `docs/internal/`. (It is **not** undocumented on the wire — see §9's
  `AgentToolEntry.yaml` correction.) And the many `docs/internal/{specs,uat,reviews}/` hits are
  **deliberately out of scope**: those are dated point-in-time records (UAT runs, spec reviews,
  superseded design drafts) whose value is that they describe the system as it was on their date.
  Rewriting them would falsify the record. Only live reference documentation is in scope.

#### W5's ADR boundary — DECIDED, because r2 left it undrawn

r2's scoping note named `docs/internal/{specs,uat,reviews}/` and said nothing about
`docs/internal/architecture/`. The r3 review is right that this is a real gap: a grep finds ADRs that
describe `hand_off` in the **present tense, as a currently-true invariant**, which is not the same
genre as a dated UAT log. Verified at `aa97bcea`:

| File | The line | Status |
|---|---|---|
| `ADR-040-fr-h-006-nested-delegation-reversal.md:27` | *"`hand_off` remains excluded — a nested sub-turn hijacking the active parent session's agent identity is a distinct concern…"* | **Accepted** |
| `ADR-040:15`, `:41` | Two `load_tool` references describing the fabricated-success failure mode | **Accepted** |
| `ADR-057-session-parent-child-parity.md:462` | *"**FIXED** — `hand_off` is structurally excluded from a child registry… so a child can never hand off"* — present tense, with `file:line` citations | **Proposed (v4)** |
| `ADR-057:465` | *"`hand_off`'s `resolveSessionID` … **UNAFFECTED** — unreachable in a child"* | **Proposed (v4)** |
| `ADR-065-channel-ownership-per-agent-workspace.md:46` | *"**The bypass:** `hand_off` pins another agent to a live conversation, and that pin is consulted before `ResolveRoute`…"* — an active, security-relevant routing bypass | **Accepted** |
| `ADR-032-external-agent-workspace-execution.md:11`, `ADR-052:162` | `load_tool` references in prose describing live mechanisms | **Accepted / Accepted** |

**Decision: ADRs are immutable decision records. Their bodies are NOT rewritten. Each affected ADR
gets a dated superseded-name note at the top instead.** Both halves of that matter:

- **Why not rewrite the bodies.** An ADR records a decision *as taken, on its date*, with the
  evidence available then. ADR-057's line 462 is not documentation of how sessions work — it is a
  cell in an evidence table justifying a specific design choice, carrying `file:line` citations that
  were true at `edd3a112` and are already stale by ~1.2k lines (CLAUDE.md warns about exactly this
  for `loop.go`/`subturn.go`). Editing the tool name inside it would make the sentence read as
  current while everything around it stayed historical — the worst of both. This is the same
  reasoning W5 applies to `specs/uat/reviews`, extended consistently rather than abandoned at the
  `architecture/` directory boundary.
- **Why a note is nonetheless required, where the specs/uat/reviews exclusion needs none.** The r3
  review's substantive point stands: CLAUDE.md cites ADR-057 as authoritative for how `pkg/session`
  works *today*, and ADR-065 documents a live security bypass. Engineers read these as reference
  material regardless of genre. A reader hitting `hand_off` in ADR-065 post-rename has no way to
  tell whether the bypass was removed, renamed, or is being described wrongly. A dated pointer
  resolves that in one line without falsifying anything.

**Concretely — W5 adds one line to the front matter of ADR-032, ADR-040, ADR-052, ADR-057 and
ADR-065**, in the same commit as W1:

> **Naming note (2026-08-27, ADR-071):** the tools named `hand_off` / `return_to_default` in this
> document were merged into `switch_agent`, and `load_tool` was renamed `ToolSearch`, by
> [ADR-071](ADR-071-tool-manifest-tier-redesign.md). The mechanisms described here are unchanged —
> only the tool names are superseded.

(Include only the clause that applies to each file.) **The "mechanisms unchanged" claim is verified,
not asserted:** §5.2.2b establishes it for the `load_tool`/`hand_off` resolver defect, §5.1.2 for the
`Execute`-body reconciliation, and §5.2 for the `CloneExcept` exclusion — the exclusion mechanism
ADR-040/057 describe is the same mechanism under a new constant value.

**This is now the project's stated convention for the ADR family**, not a one-off: an accepted ADR's
body is never retro-edited for a downstream rename; a dated front-matter pointer is added instead.
Recorded here because the boundary genuinely was undrawn, and the next ADR to rename something
shipped should not have to re-litigate it.

**Ordered — do not parallelize:**

- **W4 = D2 (search behavior)** should follow **W3**, not run beside it. Both touch
  `pkg/tools/tools_tool.go` and `manifest.go`, and more importantly D2's promotion logic is only
  worth tuning once Tier 3 exists — before D3, ambiguity barely matters because everything is
  already previewed. Sequence W3 → W4. **r5 makes this ordering load-bearing rather than merely
  sensible:** W3 now owns the `execSearchAndLoad` loop rewrite (§3.2.2) and the
  `administrativeToolNames` set (§3.2.1), both of which W4's ambiguity test consumes. Running them
  in parallel means W4 writing a ratio test against a list that does not exist yet.
- **W1's step ordering is internal and strict** (§5.3): `allStaticToolNames` first, then the seeds.
  Reversing it panics at boot via `validateOverrideKeys`.
- **W2 and W3 both edit `manifest.go`.** They are separable by function — W2 adds
  `BuildStaticToolCatalog`/`BuildLoadedToolsDelta`, W3 adds the visibility classifier and one line
  inside the builder — but they will conflict textually. Either land W3 first (it is the smaller
  edit to the existing builder) or agree the split up front so W2 writes against the post-W3 shape.
- **W-D1 = D1 (the rename) IS its own workstream — upgraded in r3.** r1 and r2 both called it "not a
  workstream… a mechanical pass over W3's and W4's files." §2.1 shows why that was wrong: D1 touches
  a **contract file** (5-step regeneration, `make verify-contracts` gate), a **user-visible SPA
  behaviour** whose failure mode is a green suite plus a real regression, a **tautological test** that
  needs its assertion re-pointed rather than renamed, a **production registration guard**, 12 Go test
  files and 7 TS test files. That is not a `sed` pass riding along inside another workstream; it is
  the largest single surface in this ADR. **Still sequenced first**, as one isolated rename commit
  before any behavioural work — but planned, reviewed and gated as its own unit, with §2.1's table as
  its definition of done. It is the one workstream that must **not** be threaded through three
  parallel branches.
- **"Mechanical pass" includes comments, doc comments, contracts and prose — not just identifiers,
  and not just `pkg/` and `src/`.** Both the D1 and D4 renames must end with a blanket
  `grep -rn 'load_tool\|hand_off\|return_to_default'` across **`pkg/`, `src/`, `tests/`, `contracts/`
  and `docs/`**, with every hit cross-checked against §2.1's and §5.2.2's tables — *not* the reverse.
  r2 restricted this step to `pkg/` and `src/`; the r3 grep found a contract file and five ADRs
  outside that scope.

  **Take this step seriously: it has now caught a real gap on every pass it has been run.** r1's
  review found a missing `websocket.go` branch; r2's review found `subturn.go` and three test files;
  r3's own grep found the contract file, the `toolVisibility.ts` regression, the tautological test,
  the `loop.go` guard, and two factual errors in r2's text. Three passes, three sets of finds. The
  tables in this document are the best available list and should still be re-verified by grep before
  W-D1 and W1 are called done, not treated as complete because they say they are.

  **r4 adds a second, different blanket pass — over this ADR, not over the code.** Both CRITICAL
  findings in the r3 review were *documentation* defects: the code was consistent, the document
  described it wrongly in three places and correctly in a fourth. Before any workstream is called
  done, grep **this file** for `TTL`, `PromoteTools`, `TickTTL`, `loadedTools`, `markToolsLoaded`
  and every line-count figure, and check each against §1.1.1 and §4.3's size note. r4's own run of
  exactly that found five claims beyond the ones the review named — including the §5.3 5a migration
  hazard, which is the most severe item in the document. **A prose claim about a mechanism is as
  capable of shipping a defect as a line of code, and nothing in CI checks it.**

  **r5 adds a third pass, and it is neither of the first two.** r4's CRITICALs were claims the
  document got *wrong*; r5's is a mechanism the document never *mentioned* — `execSearchAndLoad`'s
  `matches` array, described accurately everywhere it was cited and read only at the lines that
  supported the claim being made. So the rule is: **for every function this ADR depends on, read it
  end to end before signing off the section that relies on it.** Concretely, before W3 and W4 are
  called done, re-grep the discovery surface — `matches[`, `execSearchAndLoad`, `execLoad`,
  `SnapshotSearchableTools`, `SearchBM25`, `ToolSearchResult`, `HiddenToolDoc`, `canLoad`,
  `FindClosestToolName` — and confirm every path that emits a tool **name or description** to a
  model is policy-filtered. r5's own run of exactly that found two more instances (§16) after four
  adversarial reviews had read the same file. **"We cited it correctly" is not "we read it".**

  Known examples a compiler-guided rename will not catch, one per class: `pkg/agent/loop.go`'s
  *"Inject session key so handoff/return_to_default tools can address the session"* (comment);
  `pkg/agent/subturn.go`'s `[]string{"hand_off"}` slog field (§5.2.2b — a *functional* literal that
  looks like a comment's cousin); `contracts/components/schemas/AgentToolEntry.yaml` (§9 — outside
  the grep scope r2 specified); `src/lib/toolVisibility.ts` (§2.1(a) — a `case` label the compiler is
  happy to leave unmatched forever).
- **W2 (D5) sequences last and is gated.** Per §6.6 its merge is conditional on the
  `CacheReadTokens` acceptance test passing; if it fails, W2 is dropped into #654 and the rest ships
  without it. Sequencing it last means that veto never blocks anything else. This supersedes r1's
  placement of W2 in the parallel group — it may still be *developed* in parallel, but it merges
  last.

**Suggested shape:** **W-D1** alone (its own reviewed unit, §2.1's table as DoD, including the
contract regeneration) → then W1 (+W5, same commit) ∥ W3 → then W4 → then W2 (gated by §6.6).

Every workstream needs the full gate battery from CLAUDE.md before it merges, and CI is the
authority for the Go suite — do not run it locally (`pkg/gateway` under `goolm` will OOM this pod).

---

## 11. Ratification status

### Resolved in r2, r3 and r4 — no longer open

*(A struck-through row is a resolution a later revision superseded. It is kept, struck, rather than
deleted: the r3 `no_followup` row named a mechanism that cannot exist, and a reader who saw it
needs to find out it was withdrawn — not find it silently absent.)*

| # | Question | Resolution |
|---|---|---|
| Q3 | **Does the §4.3 revisit trigger get instrumentation?** | **YES — committed, and a hard prerequisite of D3 shipping**, not an optional extra. Two counters exposed on the existing `/metrics` endpoint (r3 corrected the mechanism away from r2's unobservable `handoffTranscriptWriteFailures` framing; r4 corrected where the second one is incremented from), plus a `PreviewAllLazy` revert flag. Full rationale, including why both and not either/or, in §4.3.1. The risk is now accepted *provisionally with a detector* — and, after r4, with a detector that can actually fire. |
| — | **Is the note parameter required or optional?** | Optional at the schema level, conditionally expected in prose. §5.1.1 — and it is named `note`, not `context`. |
| — | **What does the merged parameter's description say?** | Specified normatively in §5.1.1; it is the whole mitigation for serving two directions, so it is not left to the implementer. |
| — | **Does `switch_agent(target: "default")` get the token-budget transcript transfer?** | **No.** Step-by-step reconciliation table in §5.1.2. |
| — | **How is `AgentSwitchedFrame` emission re-expressed?** | Option (A) in §5.2.2 — derived from the post-switch active agent, because the payload carries no arguments and r1's prescribed fix is not implementable. |
| — | **Ship D5 now or defer to #654?** | **Ship, sequenced last, behind a veto-capable acceptance gate.** §6.6. |
| — | **`SanitizeToolName` mixed-case safety** | Verified safe (§2). Was §12's first unverified item. |
| — | **Bedrock `CacheControl` mapping** | Verified (§12). Was §12's second unverified item. |
| **r3** | **How are the §4.3.1 counters actually observed?** | Via the **existing** `/metrics` endpoint, by extending `pkg/tools/compositor.go`'s `toolMetricsRecorder` interface — `omnipus_toolsearch_zero_result_total`, `omnipus_toolsearch_no_followup_total`. No new mechanism, no gateway wiring change. §4.3.1(a). |
| ~~**r3**~~ | ~~**What does `no_followup` actually count?**~~ | ~~A promotion **dropped by `TickTTL` having never been called**~~ — **SUPERSEDED IN r4.** That hook can never fire for a static tool (§1.1.1), so the counter would have read a permanent zero. r3's *intent* (do not count a correct guess acted on two turns later) is preserved; the mechanism is now a 5-turn promotion-age horizon over a `pkg/agent` side table. §4.3.1(a). |
| **r4** | **Does a static `ToolSearch` promotion ever get reclaimed?** | **No, and none will be built.** Permanent for the session, accepted deliberately, with the real bound (≤71 schemas, the pre-optimisation surface) and the real per-promotion cost (one prompt-cache invalidation) stated. Decay was rejected on four grounds, chiefly that it would double the cache churn it was meant to bound and would make an already-used tool vanish mid-conversation. §3.3. |
| **r4** | **Is D3's "invisible by default" per-agent?** | **Only after §4.6.** The loaded-tools bucket is re-keyed from `session` to `(agent, session)`; `forgetSession` becomes a suffix sweep. Verified compatible with ADR-057 (the change strictly *narrows* the key, and applies ADR-057's own delegation reasoning to the handoff path it never reached) and immune to the §5.2.2b resolver defect (it uses the caller id **string**, never the resolved instance). §4.6. |
| **r4** | **Where does §6.6's D5 cache gate run?** | **Manual, pre-merge, owned by the operator**, with `B` and `ΔC` recorded in the W2 PR body. Not in CI: it would be the only credentialed, network-dependent gate in the suite, and CI structurally cannot do a cross-build before/after. Backed by a new **offline** structural test (block at index 1, carries `CacheControl`) that guards it on every PR thereafter. §6.6. |
| **r4** | **When does the legacy-policy-key migration run, and is it idempotent?** | **First statement inside `repairAndValidateToolPolicyCoverage`, before `RepairIncompleteToolPolicyCoverage`** — not merely before the validator, because the repair is what writes `deny`. Idempotent by construction (read-check-before-write + atomic file writes + a destination-key-inclusive strictest-wins fold); no version marker needed. §5.3 5a/5b. |
| **r4** | **Does the manifest block really shrink "71 lines to 8"?** | **No — those are tool counts.** Rendered: **~101 lines → 22**, derived from `BuildCompressedManifest`'s actual header/category/entry writes against the eight real Tier 2 names (6 categories) and the 71 lazy names (14 categories). §4.3's size note. |
| **r4** | **Does `switch_agent` get a `note`-omission counter (OBS-101)?** | **No — declined, with the reason recorded.** §4.3.1's counters exist to make an *accepted risk* falsifiable and feed a *stated revisit trigger*. `note`'s optionality accepts no new risk: `hand_off.context` was schema-`required` and never enforced (§5.1.1), so the runtime behaviour is unchanged from today and no decision is waiting on the number. A third counter with no trigger behind it is the "surface for its own sake" §9 rejects. |
| **r5** | **Is `ToolSearch`'s match list policy-filtered?** | **It was not — and now must be, as a hard prerequisite of D3.** `execSearchAndLoad` built `matches` from the raw ranked list before any `canLoad`, so a policy-**denied** Tier 3 tool's name and full description could reach any agent on a top-5 BM25 placement from an unrelated query. **Decided: filter after ranking, never the corpus** — corpus-filtering is a cross-agent leak, needs a cache key the registry version cannot supply, and changes BM25's own IDF/avgDocLen so §3.2's ratios would mean something different per agent. §3.2.2, with four required tests including a ranking-invariance test that fails if anyone later converts it to corpus-exclusion. |
| **r5** | **Is "71% invisible by default" true?** | **Only after TWO fixes, not one.** §4.6 scopes the manifest-block channel to `(agent, session)`; §3.2.2 scopes the search channel to policy. r1–r4 asserted the number flatly, then r4 named one prerequisite. The property is now stated precisely: a **denied** tool is nameable through neither channel; an **allowed** Tier 3 tool costs one deliberate search. Invisibility is bounded by **policy**, not by tier. §4.3. |
| **r5** | **What is the canonical source of §3.2's "administrative" exclusion?** | **An explicit 13-name `administrativeToolNames` set in `pkg/tools/manifest.go`, with a four-part drift test.** Not `Tool.Category()` — that is a **domain** enum with no `administrative` value, and `delete_agent`/`list_agents` share `CategoryAgents`, `set_config`/`get_config` share `CategoryPlatform`, so a category rule would gut rule 2 across four domains *and* still miss destructive tools with benign siblings. Not a name-prefix predicate either — prefixes are the drift hazard (`revoke_credential` escapes). §3.2.1. |
| **r5** | **How is `pendingSearchPromotions` keyed?** | **`manifestBucketKey(agentID, transcriptID, sessionKey)` — the same composite key `loadedTools` uses post-§4.6**, not the legacy `manifestSessionID`. Under a session-only key, Agent B calling a tool clears Agent A's pending entry across a `switch_agent`, so the counter under-reports exactly the handoff-heavy sessions it is meant to observe. §4.3.1(a), with a cross-agent isolation test mirroring §4.6's. |
| **r5** | **Does `forgetSession`'s suffix sweep cover the side table?** | **No, not implicitly — it must be made to.** Sharing `loadedToolsMu` protects the maps; it does not enumerate them. The one suffix scan now deletes from `loadedTools` **and** `pendingSearchPromotions`, with the close-time counter sweep running **before** the delete and the metric incremented **after** the lock is released. §4.6 point 4, §4.3.1(a). |
| **r5** | **What does a rising `no_followup` count tell an operator to DO?** | **Tighten §3.2's constants in order — `searchCrossCategoryRatio` → `searchAmbiguityRatio` → `searchMaxAutoLoad` — and reconsider Tier 3 *membership* only if all three fail to bring it down.** Explicitly **not** "widen Tier 2": that is the *other* counter's action, and r1–r4 pointed both at it. If both rise, act on zero-result first, since widening Tier 2 mechanically lowers no-followup too. §4.3.1(a). |
| **r3** | **Are ADRs in W5's scope?** | **No — bodies are never retro-edited.** Five ADRs (032, 040, 052, 057, 065) get a dated front-matter naming note instead. Now the stated project convention. §10 W5. |
| **r3** | **Does `switch_agent(target: "default")` get the 10-second timeout?** | **Yes, unconditionally.** §5.1.2. |
| **r3** | **Does D3 change the `manifest_tier` wire enum?** | **No** — Tier 3 stays `ManifestLazy` → still reports `compressed`. No SPA consumer exists, so nothing renders the lost distinction. §9. |
| **r3** | **What tolerance makes §6.6's D5 gate a measurement?** | `ΔCacheReadTokens ≥ 0.8 × B`, one-sided, no upper bound. §6.6. |
| **r3** | **Is D2's widened schema exposure accepted?** | Accepted, `canLoad`-bounded, with the speculative cross-category band narrowed to exclude `ask`-policy and administrative tools. §3.2. |

### Still open — needs the operator's yes

1. **Reserving the agent id/name `default` (§5.1.3, part 2).** Recommended yes: reject `default`
   (case-insensitive) at agent create/update with a 400. It forbids a name someone might want, so it
   is a product decision. **Parts 1 and 3 of §5.1.3 hold either way** — the sentinel always wins, and
   the boot-time WARN covers pre-existing installs regardless of how this lands. A "no" here does not
   leave the collision unhandled; it leaves it *warned about* rather than *prevented*.
2. **Strengthening the manifest block's header prose** to say explicitly that more tools exist than
   are listed and `ToolSearch` finds them by description. Recommended yes (§4.3) — the cheapest
   available mitigation for the accepted risk, and prose rather than mechanism.
3. **The 0.80 / 0.50 / 3 ambiguity constants** (§3.2) as reasoned starting values, with tuning
   deferred until there is data. Reasoned, not measured — flagged as such deliberately.
4. **Filing the `PreviewAllLazy` removal follow-up** as a tracked issue at ratification (§4.3.1). Not
   a design question, but it is the step that keeps a deliberately temporary flag from becoming
   permanent config surface, and it needs an owner.

**New in r3 — three issues to file at ratification.** None is a design question; all three are
pre-existing defects this ADR surfaced and deliberately declined to fix inside a rename. Each has
now been root-caused in writing, which means the only remaining failure mode is nobody filing it:

5. **The `load_tool` / `hand_off` fabricated-success resolver bug** (§5.2.2b). `canLoad`/`markLoaded`
   resolve the caller against the persistent top-level agent instead of the ephemeral child's own
   registry, so a sub-turn is told a structurally-absent tool loaded successfully. **Root-caused and
   deferred twice already** — once in the ADR-040 work that wrote the comment, once here. Verified
   unaffected by both renames (§5.2.2b), so deferring again is defensible; deferring again *without
   an issue* is not.
6. **Replayed `agent_switched` frames never fire for return-to-default** (§5.2.2a). `replay.go` gates
   on `HasPrefix(entry.Content, "Handoff:")` and `ReturnToDefaultTool` writes `"Returned to default
   agent (…)"`, so on transcript replay the SPA's active-agent indicator shows the handoff but never
   the return. A live UI gap, independent of this ADR, found by it.
7. **`pkg/tools/delegate_grandchild_test.go`'s `hand_off` assertion is vacuous** (§5.2). It never
   registers `HandoffTool`, so `assert.False(childHasHandoff)` has always been trivially true. It
   reads as coverage of a security-relevant exclusion and is none. W1 should fix it in passing, but
   it deserves an issue in its own right — `docs/internal/false-green-patterns.md` is the register
   this belongs in.

**New in r5 — one design question for the operator.**

8. **The 11 borderline names in `administrativeToolNames`** (§3.2.1). The *mechanism* is decided —
   an explicit set with a four-part drift test — and the 13-name seed follows the stated membership
   rule ("destroys or overwrites state no other tool in the catalog can reconstruct, or alters
   install-wide configuration"). What needs the operator is whether the in-place update verbs
   (`update_agent`, `update_workspace`, `write_agent_metadata`, `edit_skill`), the create verbs
   (`create_agent`, `create_workspace`, `install_skill`), the execution verbs (`execute_plan`,
   `run_task`) and the irreversible-external communication verbs (`send_email`, `reply`) join them.
   This is the same genre of call as §4.1's tier placements — the architect should not make it
   silently. **Nothing blocks on it:** the narrowing works with the 13-name seed, and adding a name
   only narrows the speculative band further. The last pair has the strongest case (an email cannot
   be unsent) and is the recommended yes.

---

## 12. Verification of previously-unverified items — both now RESOLVED

r1 listed two items as **[UNVERIFIED]**. Both were checked at `release/v0.1.1` @ `aa97bcea` during
the r2 revision. Neither changes a decision; both remove a "must check before starting" from the
implementation path.

- **`SanitizeToolName` and mixed case — RESOLVED, D1 is safe.**
  `pkg/tools/registry.go::SanitizeToolName` is `strings.ReplaceAll(name, ".", "_")`. It rewrites
  dots and nothing else; it never touches case. The adjacent comment states the constraint it exists
  to satisfy — Anthropic/Azure require `^[a-zA-Z0-9_-]{1,128}$`, which admits mixed case explicitly.
  `pkg/tools/fuzzy.go`'s lowercasing is confined to fuzzy-match suggestion text and does not reach
  the canonical registry lookup. Also recorded at §2, where an implementer will actually look.

- **Bedrock's `SystemParts`/`CacheControl` mapping — RESOLVED: it maps neither.**
  `grep -rn 'SystemParts\|CacheControl' pkg/providers/bedrock/` returns **zero hits**.
  `convertMessages` (`pkg/providers/bedrock/provider_bedrock.go`) builds system content from
  `msg.Content` alone, emitting `types.SystemContentBlockMemberText` with no cache-point block.
  `pkg/agent/context.go` populates `Content` as the concatenated fallback alongside the structured
  `SystemParts`, so Bedrock receives the full system prompt as plain text and simply never sees the
  cache markers.

  So the accurate operator-facing statement is: **`CacheControl` is honored by the Anthropic adapter
  only.** The OpenAI-compatible path *strips* `SystemParts`; Bedrock *never reads* them. Different
  mechanisms, identical outcome — both get concatenated text with no explicit cache marker, and both
  still benefit from D5's stable-before-volatile **reordering** on any provider doing automatic
  prefix caching. This slightly narrows §6.5's claim (r1 said "Bedrock is unverified"; it is now
  verified as a non-consumer) and does not alter D5, which was justified on Anthropic alone.

---

## 13. Review traceability — r1 → r2

Maps every finding in
[`ADR-071-tool-manifest-tier-redesign-review.md`](ADR-071-tool-manifest-tier-redesign-review.md) to
where r2 resolves it. The review file is retained unmodified as a historical record.

| Finding | Resolved in |
|---|---|
| **CRIT-001** — §5.2 named one of two `websocket.go` branches | §5.2.1 (both branches, plus the zero-test-coverage callout), §5.2.2 (r1's prescribed fix was not implementable — corrected), §5.2.3 (required acceptance test), §5.2 surface table |
| **MAJ-001** — `context` required→optional contradiction | §5.1.1 — r1's claim corrected as factually wrong; decided optional, with the reason the relaxation is near-costless |
| **MAJ-002** — two semantics merged into one field | §5.1.1 — renamed `note`, normative dual-purpose description specified |
| **MAJ-003** — divergent `Execute` bodies unreconciled | §5.1.2 — step-by-step table; transcript transfer conditional, worker check unconditional, audit stamping deliberately asymmetric |
| **MAJ-004** — no documentation workstream | §10 W5 (five doc sites, same-commit requirement, explicit scoping of what is *not* in scope) |
| **MAJ-005** — no rollback or kill switch | §4.3.1 — both counters **and** `PreviewAllLazy`, both hard prerequisites, flag time-boxed; §11 Q3 resolved yes |
| **MAJ-006** — pre-existing agent named `default` | §5.1.3 part 3 — boot-time WARN, with the reasoning for WARN-not-abort against the `ValidateToolPolicyCoverage` precedent, plus required test |
| **MIN-001** — `SanitizeToolName` left unverified | §2 and §12 — verified, closed |
| **MIN-002** — stale comments outside the blast radius | §5.2 surface table (Comments row), §10 (blanket grep step naming the `loop.go` example) |
| **MIN-003** — TS test files not in the Tests row | §5.2 surface table + the note beneath it, including the required explicit `switch_agent`-is-visible case |
| **MIN-004** — degenerate single-candidate case | §3.2 |
| **MIN-005** — no tie-break above the cap | §3.2 |
| **OBS-001** — §6.4 undercuts D5's ROI | §6.6 — explicit call: ship last behind a veto-capable gate, with the reasoning for shipping over deferring |
| **OBS-002** — tier placement reasoned, not measured | §4.1 — dated "as of" baseline, tied to §4.4's drift test |
| **OBS-003** — `inboundschemas/` is a generated copy | §9 |
| *Structural: "no explicit acceptance criteria"* | §5.2.3 (frame emission), §6.6 (D5's gate), §4.3.1 (falsifiable revisit trigger), §5.1.3 (reserved-name test) — the four places r1 asserted an outcome without a way to check it |

---

## 14. Review traceability — r2 → r3

Maps every finding in
[`ADR-071-tool-manifest-tier-redesign-r2-review.md`](ADR-071-tool-manifest-tier-redesign-r2-review.md)
to where r3 resolves it, then lists what r3's own independent grep found beyond that review. Both
review files are retained unmodified as historical records; neither is superseded.

### The r3 review's findings

| Finding | Resolved in |
|---|---|
| **MAJ-001** — `pkg/agent/subturn.go` absent from the surface table | **§5.2.2b** (new section) — the hand-copied `[]string{"hand_off"}` slog literal, fixed by *deriving* it via `string(tools.ExcludedHandoff)` rather than re-copying; the live `load_tool`/`hand_off` bug comment, with an explicit statement (the review's Unasked Question 1) that **the defect's mechanism is a wrong-*registry* bug and survives both renames unchanged**; seven further prose refs. New surface-table row. Filed as §11 item 5 rather than fixed inside a rename |
| **MAJ-002** — three `pkg/agent` test files missing from the Tests row | **§5.2.2 table + the note beneath it** — `sprint_h_subturn_test.go`, `sprint_h_scenario_test.go`, `subturn_delegate_nesting_test.go`, each with what it actually asserts. Explicitly marked **fail-loudly** (a planning gap, not a silent regression), and contrasted with the two cases that *do* ship green. The review's test-hygiene note (`CloneExcept("hand_off")` bypassing the constant) is carried as its own item |
| **MAJ-003** — W5 silent on ADR-057/065 | **§10 W5's new "ADR boundary" subsection** — option (b), reasoned: ADR bodies are **never** retro-edited; five ADRs (032, 040, 052, 057, 065) get a dated front-matter naming note. Stated as the project's convention going forward, with the specs/uat/reviews precedent extended rather than abandoned at the directory boundary, and the "mechanisms unchanged" claim tied to §5.1.2/§5.2/§5.2.2b rather than asserted (the review's Unasked Question 3) |
| **MAJ-004** — counters never wired to an operator surface | **§4.3.1(a), rewritten** — extends `pkg/tools/compositor.go`'s existing `toolMetricsRecorder` interface (`IncFilterTotal`/`IncCollisionTotal` pattern) with two methods, adds two counters + exposition rows to `pkg/gateway/metrics.go` (`omnipus_toolsearch_zero_result_total`, `omnipus_toolsearch_no_followup_total`), and needs **zero** change to `gateway.go`'s existing `tools.SetToolMetricsRecorder(globalToolMetrics)` call. Required `/metrics` assertion added. r2's `handoffTranscriptWriteFailures` framing survives only as a test affordance, with the reason it was the wrong operator surface stated (the review's Unasked Question 2) |
| **MIN-001** — `no_followup` conflates wrong-guess with acted-on-later | **§4.3.1(a)** — redefined to fire on TTL expiry without invocation, hooked into `TickTTL`, not at end-of-turn. ⚠️ **The diagnosis was right and stands; the prescribed hook was SUPERSEDED IN r4** — `TickTTL` cannot fire for a static tool. See §15 CRIT-101 |
| **MIN-002** — timeout wrapper missing from the reconciliation table | **§5.1.2** — new first table row + a paragraph deciding it: unconditional, with the reasoning for not making it branch-conditional |
| **MIN-003** — D2 widens schema exposure, no STRIDE pass | **§3.2** — accepted and `canLoad`-bounded, **with one narrowing**: the speculative cross-category band excludes `ask`-policy and administrative (`delete_*`/`remove_*`/`disable_*`/`set_config`) tools; the confident 0.80 band stays unrestricted. Carried into §7 |
| **OBS-001** — enum justified partly on a deferred feature | **§4.4** — third clause deleted, with a note saying it was deleted deliberately so the diff does not read as an oversight |
| **OBS-002** — `canLoad` short-circuit → full-scan | **§3.2** — stated as an explicit implementation consequence, with the side-effect-free/bounded-at-5 risk assessment |
| **OBS-003** — who owns the `ManifestVisibility` filter after D5 | **§4.4** — `BuildStaticToolCatalog` applies it itself; does not expect a pre-filtered slice. Same for `PreviewAllLazy` |
| *Unasked Q4 / Dataset gap* — §6.6 has no tolerance | **§6.6** — `ΔCacheReadTokens ≥ 0.8 × B`, one-sided, no upper bound, with the reasoning for both |

### Found by r3's own grep — beyond the r3 review

The r3 review closed by warning that §5.2.2's completeness claim "should be re-verified again after
the fixes below are applied, not assumed fixed by inspection alone." That re-verification was run as
a from-scratch blanket grep over `pkg/`, `src/`, `tests/`, `contracts/` and `docs/`. **It found five
more gaps and two factual errors**, all on the D1 axis neither review grepped:

| # | Found | Resolved in |
|---|---|---|
| 1 | `contracts/components/schemas/AgentToolEntry.yaml` names `load_tool` **twice** — a Constraint #8 contract file. D1 needs a second 5-step regeneration (4 generated Go sites, 1 TS, 1 mirror). Its `search_tools_*` reference is *also* already stale | **§9**, **§2.1** |
| 2 | `src/lib/toolVisibility.ts`'s `case 'load_tool'` and `tool !== 'load_tool'` — post-rename `ToolSearch` becomes **visible in every chat thread and panel by default**, reversing CLAUDE.md's own UI rule. The 7 TS test files keep passing, so **it ships green** | **§2.1(a)**, **§7** |
| 3 | `src/test/canonicalToolNames.test.ts` — the dedicated canonical-name guard asserts `expect(canonical).toBe('load_tool')` against a **local variable**. A tautology; it cannot fail. Must have its assertion re-pointed at a real symbol, not merely renamed | **§2.1(b)**, **§11 item 7's sibling** |
| 4 | `pkg/agent/loop.go`'s double-registration guard uses the raw literal `Get("load_tool")` — post-rename it never matches and silently stops guarding | **§2.1(c)** |
| 5 | D1's full test surface is **12 Go + 7 TS files**; r1/r2 listed none | **§2.1's table** |
| 6 | **r2 was wrong**: `pkg/gateway/replay.go` matches a transcript **content prefix**, not a tool name. It needs no edit — and the `"Handoff: "` prefix must be **frozen**. Surfaces a live UI defect (return-to-default never replays an `agent_switched` frame) | **§5.2.2a**, **§11 item 6** |
| 7 | **r2 was wrong**: §5.2's "silently, no failing test." `CloneExcept` emits a purpose-built `slog.Warn`; three of four named tests **fail loudly**; the fourth (`delegate_grandchild_test.go`) passes because its assertion is **vacuous today** — it never registers `HandoffTool`, and contains no reference the compiler would flag | **§5.2**, **§11 item 7** |

**The pattern, stated plainly, because it has now recurred three times.** r1's review found a missing
`websocket.go` branch. r2's review found `subturn.go` and three test files. r3's own grep found the
seven above. Every pass has disproved a "full surface" claim, and each time the claim was made in
good faith after real work. r3's response is not another assurance: §5.2.2's table is labelled
"re-derived by fresh blanket grep" rather than "confirmed complete", the front-matter evidence note
says what "verified" has actually meant here, and §10 makes the grep a **gate before W-D1 and W1 are
called done** rather than a step someone remembers to run.

---

## 15. Review traceability — r3 → r4

Maps every finding in
[`ADR-071-tool-manifest-tier-redesign-r3-review.md`](ADR-071-tool-manifest-tier-redesign-r3-review.md)
(verdict **BLOCK**) to where r4 resolves it, then lists what r4's own grep found beyond that review.
All three review files are retained unmodified as historical records; none is superseded.

*(Naming, since §13/§14 use the same phrase for a different file: the table below maps the findings
**in** `…-r3-review.md` — the third review, which read r3 and returned BLOCK. §14's identically-named
table maps `…-r2-review.md`.)*

### Findings in the r3 review file

| Finding | Resolved in |
|---|---|
| **CRIT-101** — the TTL reclamation §3.3/§4.3.1(a)/§7 depend on does not apply to static tools | **§1.1.1** (new — both mechanisms documented side by side, with the `IsCore` gate that separates them and the `tools_tool.go` call-site comment that made the conflation easy to miss); **§3.3** rewritten as an explicit **option (b)** decision — permanent-for-session, accepted, decay rejected on four grounds including that it would *double* the cache churn it was meant to bound; **§4.3.1(a)** counter redesigned around a promotion-age horizon in `pkg/agent` that can actually fire; **§7**, **§6.4**, **§4.4**'s code comment and **§6.3** all corrected. Required tests added in §3.3 and §4.3.1(a). The review's Unasked Question 1 is answered explicitly, with reasons, not left open |
| **CRIT-102** — the `loadedTools` bucket is keyed by session, not `(session, agent)`, so `switch_agent` leaks Tier 3 reachability across agents | **§4.6** (new) — **option (a)**: re-key to `(agent, session)`. `manifestSessionID`'s ADR-057 doc comment was read first as the review instructed; the change **narrows** the key and therefore cannot reintroduce the shared bucket ADR-057 forbids, and it applies ADR-057's own delegation reasoning to the handoff path that reasoning never reached. Both id sources named (`ts.agent.ID` / `tools.ToolAgentID(ctx)`, verified identical on every path including delegation), the `forgetSession` leak the re-key would otherwise cause is specified as a suffix sweep, and three tests are required. §4.3 now states the dependency explicitly rather than presenting 71% as flat |
| **MAJ-101** — §6.6's D5 merge gate has no stated home | **§6.6** — **manual, pre-merge, operator-owned**, numbers recorded in the W2 PR body; explicitly **not** CI, with the posture reasoning (only credentialed/networked gate in the suite; a cross-build before/after a single CI job cannot do). Plus a net-new **offline structural test** so the mechanism is guarded on every PR after the one-time measurement |
| **MAJ-102** — §5.3 item 5's migration has no trigger point or idempotency guarantee | **§5.3 5a/5b** — ordered as the **first statement inside `repairAndValidateToolPolicyCoverage`, before the repair** (the review said "before the validator"; that is not tight enough — the repair sits between them and is what writes). Idempotency argued case-by-case for re-run, crash, and the Windows two-process race, plus the destination-key-inclusive fold rule that makes it monotone. Four tests required. **r4's grep found the actual failure is far worse than r3 described** — see item 1 below |
| **MIN-101** — the "8-line" claim is unverified against Tier 2's real entries | **§4.3's size note** — verified against `BuildCompressedManifest`'s exact writes and the eight real tool names: **6** categories, so **22** lines, not 8. The pre-D3 figure is **~101**, not ~71. Corrected at all five reader-facing sites (§4.3 ×2, §6.3 ×2, §7 ×1); the derivation lives once, in §4.3's size note, so the numbers have a single source |
| **OBS-101** — no counter/telemetry for `note` omission | **Declined, with the reason stated in one line** rather than silently dropped — §11's r4 row. `note`'s optionality accepts no new risk and feeds no revisit trigger, unlike §4.3.1's counters; a third counter with nothing behind it is the surface-for-its-own-sake §9 rejects |

### Found by r4's own grep — beyond the r3 review

The review named §3.3, §4.3.1(a) and §7 as the sites depending on the TTL error. A blanket grep of
this document for `TTL`, `PromoteTools`, `TickTTL`, `loadedTools`, `markToolsLoaded` and every
line-count figure found **five more**, one of which is the most severe item in the document.

| # | Found | Resolved in |
|---|---|---|
| 1 | **§5.3 item 5 stated the migration hazard backwards, and the real one bricks tool discovery.** `RepairIncompleteToolPolicyCoverage` backfills unknown `(agent, tool)` pairs to explicit **`deny`** before validation. Un-ordered, the first post-upgrade boot writes `switch_agent: deny` **and `ToolSearch: deny` on every agent** — disabling handoff and *all* lazy-tool loading, i.e. 71% of the catalog — and boot **succeeds**. r1–r3 described the opposite direction ("an explicit deny becoming whatever the repair writes"); a deny→deny is harmless, it is the **allow** that flips | **§5.3 5a**, **§7** |
| 2 | **§1.1's mechanism bullet was the root of the error and the review did not name it.** It described TTL as *the* promotion mechanism, correctly citing the `!IsCore` guard while never saying that every tool in this ADR is core. Every downstream claim inherited the gap from here | **§1.1**, **§1.1.1** (new) |
| 3 | **§4.4's `ManifestSearchOnly` doc comment says "still promoted with the same TTL".** This is a code block destined to be copied into production source — the error would have shipped *into* the codebase, where it would outlive the ADR | **§4.4** |
| 4 | **§6.4's "So does every TTL expiry" is wrong in D5's FAVOUR.** Static promotions churn the tool array **once**, not twice, so the stable-prefix turn population is larger than r1–r3 assumed. This strengthens §6.6's ship-D5 argument and supplies §3.3 with its second reason for rejecting decay | **§6.4**, **§6.6** |
| 5 | **§3.5 already stated the correct model** — *"a loaded tool stays loaded for the rest of the session"* — and has since r1, in direct contradiction with §3.3 two paragraphs earlier. Both survived three revisions and two adversarial reviews | **§1.1.1**, **§3.5** (cross-referenced rather than silently reconciled) |

**The pattern has now recurred four times, and r4 changes what it says about itself.** r1→r3's
finds were all *scope* misses — a file nobody grepped. **r4's two CRITICALs are different in kind:
the code was consistent and correct; the document described it wrongly.** No compiler, no test and
no `make verify-contracts` catches that, and three of the four wrong claims sat next to accurate
`file::symbol` citations of the very functions that disprove them — which is precisely why two
adversarial reviews read past them. §10's blanket-grep gate is therefore extended in r4 from a pass
over the *code* to a pass over *this document's own mechanism claims*, checked against §1.1.1. The
honest statement of confidence remains what the front matter says, with one addition: **"verified"
has meant "verified where someone looked", and until r4 nobody had looked at whether the mechanism
three sections were built on applies to their own subject.**

---

## 16. Review traceability — r4 → r5

Maps every finding in
[`ADR-071-tool-manifest-tier-redesign-r4-review.md`](ADR-071-tool-manifest-tier-redesign-r4-review.md)
(verdict **REVISE**) to where r5 resolves it, then lists what r5's own grep found beyond that
review. All four review files are retained unmodified as historical records; none is superseded.

### Findings in the r4 review file

| Finding | Resolved in |
|---|---|
| **CRIT-201** — `ToolSearch`'s match list is not policy-filtered; the discovery channel discloses denied Tier 3 tool names and descriptions | **§3.2.2** (new). Confirmed against source before writing: `matches` is built from `ranked` before any `canLoad`, returned on both paths including the one commented `// … all results denied by policy.`, and `SnapshotSearchableTools` takes no policy input. **Decided: filter after ranking, not the corpus** — with the corpus option rejected on three verified grounds (per-call caller resolution via `tools.ToolAgentID(ctx)` makes it a cross-agent leak; `bm25CachedEngine` is keyed on `registry.Version()`, which does not move on a policy change, so there is no invalidation signal; and `BM25Engine.Search` derives `N`/`df`/IDF/`avgDocLen` from the corpus on every call, so excluding documents changes every surviving score and makes §3.2's ratios agent-dependent — the instability §3.2 explicitly rejects). Mechanism specified in five numbered steps against the real function, including the fail-closed empty and nil-resolver paths. Four tests required, one of them (ranking invariance) written specifically to fail if a later change converts this to corpus-exclusion. §4.3's framing corrected to name **two** prerequisites and to restate the property as bounded by **policy**, not tier |
| **MAJ-201** — the "administrative" exclusion has no canonical source and no drift test | **§3.2.1** (new). The review asked which of `Tool.Category()` or a prefix list it was; the answer is **neither is usable**, verified: `ToolCategory` (`pkg/tools/base.go`) is a domain enum with no `administrative` value, and `pkg/sysagent/tools/category.go` puts `delete_agent` with `list_agents`, `set_config` with `get_config`, `disable_channel` with `list_channels`, `remove_mcp_server` with `list_mcp_servers` — a category rule would gut rule 2 across four whole domains and still miss destructive tools with benign siblings; a prefix rule is the drift hazard itself. Replaced with an explicit 13-name set carrying a stated membership rule and a four-part drift test mirroring §4.4. §3.2's wording corrected from the mechanism that does not exist. The residual gap (a destructive tool with a benign name) is stated plainly rather than papered over, with §4.4's forced tier decision extended to two questions as the real mitigation |
| **MAJ-202** — `pendingSearchPromotions`'s outer key is unspecified relative to §4.6's rekey | **§4.3.1(a)** — **`manifestBucketKey(agentID, transcriptID, sessionKey)`**, stated explicitly, with three reasons led by the one that decides it: under a session-only key, Agent B calling a tool clears Agent A's pending entry across a `switch_agent`, so the counter under-reports exactly the handoff-heavy sessions it exists to observe. Cross-agent isolation test added as §4.3.1(a) test 3, mirroring §4.6's. The review's **Unasked Question 3** is answered explicitly as **no, not implicitly** — §4.6 point 4's suffix sweep must be made to cover both maps, with count-before-delete ordering and the metric incremented outside the lock |
| **MIN-201** — the `no_followup` counter's revisit action is unspecified, unlike its sibling | **§4.3.1(a)** — a concrete ordered ladder (`searchCrossCategoryRatio` → `searchAmbiguityRatio` → `searchMaxAutoLoad`, then Tier 3 membership), with the order justified rather than asserted, plus the interaction rule for when both counters rise. The closing "Revisit trigger" paragraph is also corrected: r1–r4 pointed **both** counters at "widen Tier 2", which would have had an operator widening Tier 2 in response to a signal that Tier 2 is not the problem |
| **OBS-201** — the observability apparatus is substantial for a provisional risk | **Recorded, not acted on**, consistent with the review's own framing of it as a ratification gut-check rather than a finding. r5 adds one more piece of state to it (the `pendingSearchPromotions` key discipline and the dual sweep), which the observation anticipated; the §7 negative-consequences list now names the two-maps-to-keep-in-sync cost explicitly so the gut-check has the full total in front of it |

### Found by r5's own grep — beyond the r4 review

The review's closing action asked for the §10 blanket-grep discipline to be pointed at the discovery
channel itself. Done, over `matches[`, `execSearchAndLoad`, `SnapshotSearchableTools`, `canLoad`,
`ToolSearchResult`, `HiddenToolDoc` and every `Description()` emission in `pkg/tools`, `pkg/agent`
and `pkg/gateway`. **It found two further instances of the same unfiltered-results shape**, and three
surfaces that are correctly filtered — recorded so the next reader does not re-derive them.

| # | Found | Disposition |
|---|---|---|
| 1 | **`ToolRegistry.SearchBM25` (`pkg/tools/search_tool.go`) has the identical unfiltered shape** — it builds `[]ToolSearchResult{Name, Description}` from `SnapshotSearchableTools` with no policy input, and it **structurally cannot be fixed in place**: a `*ToolRegistry` holds no resolver and no caller identity. Its sole caller today is `pkg/tools/search_tools_test.go`, so it is **not live-reachable** — but it is an exported method whose only effect is to offer a future implementer an unfiltered shortcut past §3.2.2 | **Delete it in W3** (§10), migrating its test to the filtered path. A doc-comment warning would be the weaker option: this document's own §4.4 argues that the way to prevent a silent wrong default is to make it impossible, not to describe it |
| 2 | **`FindClosestToolName(allAgentTools, name)` in the `canLoad` closure (`pkg/agent/loop.go`, the `SetResolver` body) suggests from the PRE-policy set.** `allAgentTools := callerAgent.Tools.GetAll()`; `policyFiltered` is derived from it separately and is **not** what the fuzzy fallback reads. So a near-miss probe on an unknown name (`delete_wrkspace`) can have a policy-denied tool's real name suggested back. Same class as CRIT-201, materially smaller: name only, no description, and it needs both `len(unknown) ≥ minFuzzyInputLen` and a ≥60% similarity hit | **Fix in W3** (§10) by passing `policyFiltered`. It does not weaken the ladder: the exact-match-in-`allAgentTools` branch immediately above already returns the correct "denied by this agent's policy" message, so the fuzzy branch only ever needed the allowed set. **One real cost, stated:** `policyFiltered` excludes hidden MCP tools (they are not in `GetAll()`), so hidden-tool name suggestions are lost. Either accept that, or suggest from `policyFiltered` ∪ {policy-allowed hidden tools}. Scoped, not hand-waved |
| 3 | **The manifest block is already policy-scoped — not a finding.** `pkg/agent/tool_manifest.go::buildToolManifestNote` passes `policyFiltered` to `BuildCompressedManifest` at the real call site | No change. Recorded because it is the channel §4.3's claim was true of, and the contrast with (1)/(2) is the whole shape of CRIT-201 |
| 4 | **The one `BuildCompressedManifest` call that passes unfiltered `all` is the token ESTIMATOR** (`pkg/agent/loop.go`'s tool-surface estimate, ADR-066's `estimateToolSurfaceTokens` path). Its output is an integer, never model-visible text | **No change, and deliberately so.** Stated here because it looks like the same defect and is not — "fixing" it would make the estimate disagree with the surface it estimates |
| 5 | **`pkg/gateway/rest_tool_registry.go::HandleToolsRegistry` and `pkg/gateway/rest.go`'s tool listing return the full catalog with descriptions, unfiltered** | **Out of scope, by trust boundary.** These are authenticated HTTP endpoints serving the operator their own install's catalog — the operator is entitled to see it; the *agent* is the party CRIT-201 is about. Recorded so a future reader does not mistake the asymmetry for an oversight |

**What r5 changes about how this document describes its own confidence.** r4 wrote that its two
CRITICALs were "different in kind" from r1–r3's: the code was correct, the document described it
wrongly. **r5's CRITICAL is a third kind.** The document described `execSearchAndLoad` accurately
everywhere it mentioned it — §3.2's `canLoad` reasoning, §4.4's `SnapshotSearchableTools` warning
and §1.1.1's TTL mechanics are all correct — but it only ever described the **half of the function
it had a reason to look at**. The `matches` array was never wrong in this document; it was never
mentioned. Four adversarial reviews and five revisions read past it because every claim near it was
true. The generalisable lesson, and the one thing §10's grep gate is extended to cover for r5, is
that **a mechanism the document relies on must be read end to end, not only at the lines that
support the claim being made** — "we cited it correctly" is not the same as "we read it".
