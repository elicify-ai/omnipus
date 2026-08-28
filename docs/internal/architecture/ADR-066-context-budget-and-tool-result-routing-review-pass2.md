# Adversarial Review (pass 2): ADR-066 — Context overflow: sliding window mid-turn, emptied tool results, cap at the door

**Document reviewed**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-context-budget/docs/internal/architecture/ADR-066-context-budget-and-tool-result-routing.md` (510 lines, Proposed, restructured 2026-08-22, branch `feat/context-budget-and-tool-result-routing` @ `ab2eb246`)
**Review date**: 2026-08-22
**Review mode**: generic-markdown (ADR — no formal requirement IDs; §17 "Exit proof" is the only acceptance mechanism)
**Verdict**: **BLOCK**

> **File naming.** The first-pass review lives at `ADR-066-context-budget-and-tool-result-routing-review.md` and is cited by the ADR's own History line as the record of why the earlier design was retired. Overwriting it would destroy evidence the ADR depends on, so this pass is written to `…-review-pass2.md`.
>
> **Scope of this pass.** Per the commission, findings the restructure resolved are not re-raised (spill-to-disk, the reducer, refetch recipes, the second budget, the `antigravity` exemption, the fabricated ADR-028 quote, the missing mid-turn check, the missing ingest bound as a *concept*, the seed-vs-catalog key space, `maxTokens*4`). Three first-pass findings are carried forward because the current text still does not address them: **MAJ-021** (prompt caching), **MAJ-011** (a window-independent cap on small-window models — now reappearing as the D3 floor), and **OBS-001** (scope — the document got larger, not smaller). Everything else below is against the current text, verified on this branch.

---

## Executive Summary

The restructure fixed the first pass's central objection: the window is now checked mid-turn and the mechanism that keeps call/answer pairs intact (empty in place) is the right one. But the core of the new design — "the full result is appended to the archive while a truncated/emptied version enters the window" — is written as if the window were a separate store. It is not: `GetHistory` is `archive[Skip:]` read from disk, and mid-turn the LLM request is built from an in-memory `messages` slice that never consults the meta file. The ADR specifies a projection for D5 but not for D4, never says how the in-memory mid-turn slice is projected, and its recovery path (`recall_conversation` by `tool_call_id`) is contradicted three ways by numbers elsewhere in the same document — the page it promises (62,500 chars) is larger than the builtin result cap it also mandates (30,000) and roughly three times the recall span budget it says the page counts against (8,000 tokens). The D7 guarantee "nothing size-related is turn-fatal" is false for oversized user messages and tool-call arguments, which D4 does not touch and the thrash guard turns into a typed turn death.

Around that core the ADR has grown into a provider-platform programme (registry-fed catalog, signed feed, provider-id rename across 1,241 literals, subscription policy, `antigravity` deletion, provider deletion, onboarding redesign at 190 providers) whose decisions D9–D14 have **no exit proof at all**.

| Severity | Count |
|----------|-------|
| CRITICAL | 4 |
| MAJOR | 17 |
| MINOR | 9 |
| OBSERVATION | 4 |
| **Total** | **34** |

---

## Findings

### CRITICAL

#### [CRIT-001] "Full result in the archive, truncated in the window" — there is no window store to hold the truncated copy

- **Lens**: Incorrectness / Infeasibility
- **Affected**: §5 "Over-cap behaviour", §6.1, §6.2, §7 "Order of operations"
- **Description**: §5 says an over-cap result "enters the window truncated head-and-tail with a marker … The full result is still appended to the archive (§6.2)." §6.1 correctly observes that the window is re-read from disk on every `GetHistory` — and that is exactly why this cannot work as written: `pkg/memory/jsonl.go::JSONLStore.GetHistory` reads `meta.Skip` and returns the archive lines after it. The window **is** the archive from `Skip` onward. There is no second store for a truncated copy. The ADR gives D5 an assembly-time projection (emptied-set in meta, substituted in `assembleMessages`) but gives D4 none: as written, either the archive line holds the full 1.18 MB and the window therefore also holds it (cap does nothing), or the archive line holds the truncated text and §6.3's recall "returns that one `role: "tool"` message" in pages of a 62,500-char stub.
- **Impact**: D4 — the decision marked "shippable first" and "correct even where D1–D3 resolve badly" — has no implementable semantics. Whichever way an implementer resolves it silently breaks either the cap or the recall path.
- **Recommendation**: State that D4 is *also* an assembly-time projection: the archive line always holds the full content; the meta file carries, per `tool_call_id`, a state of `full | capped | emptied`; `assembleMessages` renders `capped` as head+tail+mark and `emptied` as mark-only. One projection, one meta field, one recall path. Then rewrite §7's order of operations to say "append full to archive → record cap state in meta → budget check → record emptied state → assemble".

#### [CRIT-002] The mid-turn LLM request is built from an in-memory slice the projection never touches

- **Lens**: Incorrectness / Incompleteness
- **Affected**: §6.1 ("applied at assembly time"), §7 D6 "When"
- **Description**: §6.1 argues the projection must live at assembly time because `GetHistory` re-reads from disk. But inside the tool loop the request is **not** assembled from `GetHistory`. `pkg/agent/loop.go::runTurn` appends every tool result to an in-memory `messages` slice (`messages = append(messages, toolResultMsg)` and the seven `deniedMsg` sites) and each subsequent LLM call uses `callMessages = messages` (the sites at the top of the iteration loop). The only place `assembleMessages` runs from fresh `GetHistory` is the post-proactive-trim path before the first call — and that path passes `ts.userMessage` in again, because at that point the user message has not yet been persisted (it is saved at "Save user message to session" *after* the trim block). Re-running that same assembly mid-turn would duplicate the user message, which by then *is* on disk. The ADR never says how the emptied-set is applied to the in-memory slice, whether the mid-turn check rebuilds from disk, or how that rebuild avoids the duplicate.
- **Impact**: An implementer following §6.1 literally persists the emptied-set, substitutes the mark in `assembleMessages`, and the next mid-turn request still carries the full content because it came from the in-memory slice. The incident reproduces with the fix "in place". Live and reload then disagree — the hazard §6.1 itself flags.
- **Recommendation**: Specify the mid-turn path explicitly: after each tool-result append, run the budget check against the in-memory slice; when D5 empties `tool_call_id`s, (a) persist them to meta and (b) apply the same pure projection function to the in-memory slice in place. Name the function (`applyResultProjection(messages, meta)`) and require that `assembleMessages` and the mid-turn path call the same one. Add an exit-proof item: after a mid-turn empty, the bytes sent to the provider and the bytes assembled on reload are identical.

#### [CRIT-003] The recall page can never be returned: it exceeds both the builtin result cap and the recall span budget the ADR binds it to

- **Lens**: Inconsistency
- **Affected**: §6.3 vs §5 caps table vs §6.3 "Counts against the recall span budget"
- **Description**: Three numbers in the ADR collide on the same object.
  1. §6.3: a recall page is "bounded by the D4 cap **for that surface** (62,500 / 30,000 chars)". `recall_conversation` is a **builtin** tool; its result therefore passes the D4 choke point at the builtin success cap of **30,000 chars** (§5). A 62,500-char page of an MCP result is cut in half at the door by the ADR's own rule — every time, with no opt-out ("No per-server opt-out").
  2. §6.3: the page "counts against the recall span budget like every other mode". Those budgets are `recallDefaultTokens = 4000` / `recallRangeTokens = 8000` (`pkg/agent/recall_conversation.go`, verified). 62,500 chars is ≈ 25,000 estimated tokens; even 30,000 chars is ≈ 12,000. Under FR-019 "the span may be dropped alone to fit", the span that carries the page is over budget on arrival and is the first thing `windowTrim` drops.
  3. The mark says "returns it in pages" — the model is told a retrieval path exists that, by the arithmetic above, yields at most ~10,000 chars per call against a 1,178,522-char result, i.e. 118+ round-trips.
- **Impact**: The entire justification for emptying instead of summarising — "lossless, one recall away" — does not hold. In practice the emptied content is unreachable at useful granularity.
- **Recommendation**: Decide one number for a recall page and derive the others from it: e.g. page = 30,000 chars; exempt the `tool_call_id` mode from the 4k/8k span budget and give it its own budget equal to one page; state that the page passes the D4 choke point unmodified (it is already capped by construction). Recompute the "about six MCP results" arithmetic in §7 if caps change.

#### [CRIT-004] "Nothing size-related is turn-fatal" is false — user messages and tool-call arguments are uncapped, and the thrash guard kills the turn

- **Lens**: Incorrectness / Incompleteness
- **Affected**: §7 "Thrash guard", §8 D7, §13 "Positive"
- **Description**: §7: "If the glass is still over budget after every eligible result is emptied — only possible if a non-tool message is itself oversized — stop, log, and surface a typed error… With D4 in place this should be unreachable." D4 caps **tool results**. It does not cap (a) the user message — a pasted 600 KB document is an ordinary chat action; (b) an assistant message's tool-call **arguments** — `write_file` with a 200 KB body, `bash` with a long heredoc; (c) `ReasoningContent`, which `estimateMessageTokens` counts and which is persisted via `AddFullMessage`; (d) injected notes (`injectManifestNote`, workspace instructions). Any of these, on a 128k-floor model, makes the guard reachable on the first tool call of the turn, and D7 then returns a typed error — a dead turn, exactly the incident's user-facing outcome with a better sentence.
- **Impact**: §8's "Remaining turn-fatal conditions" list and §13's "no turn exceeds the window regardless of length" are both wrong for realistic inputs. The exit-proof thrash-guard test (§17.4) *asserts* this behaviour as correct.
- **Recommendation**: Either extend D4 to user content and tool-call arguments (a cap with a typed *tool* failure for oversized arguments — the model can retry smaller — and a pre-turn rejection for an oversized user message, with the reason shown), or rewrite D7/§13 to state honestly which size conditions remain turn-fatal and what the user sees. Do not claim unreachability without enumerating what D4 does not cover.

### MAJOR

#### [MAJ-001] The D6 trigger is denominated in characters, the window it is compared against in tokens, and it coexists with an unrelated existing budget formula

- **Lens**: Inconsistency / Ambiguity
- **Affected**: §7 "Trigger"
- **Description**: `trigger = min(absoluteBudget, 0.9 × resolvedWindow)` with `absoluteBudget = 400,000 characters` and `resolvedWindow` a token count (1,048,576, 128,000). `min()` of a char count and a token count is undefined. Separately, `windowTrim` already computes `budget = contextWindow − maxTokens − headroom(5%) − pinnedCoreOverhead` in **tokens** and `isOverContextBudget` uses `ContextWindow`/`MaxTokens` in tokens; the timeout-recovery site uses a third threshold, `ContextWindow × SummarizeTokenPercent/100` (`pkg/agent/loop.go`, verified — a `summarize_token_percent` setting that survives the summariser's deletion). The ADR says "three paths currently give three answers" for the *window* but then adds a fourth *trigger* without stating how it relates to the three that exist.
- **Recommendation**: Pick one unit (tokens, via the existing estimator) for every budget quantity; express `absoluteBudget` as 160,000 tokens; define the mid-turn check as the same `budget` formula `windowTrim` uses with the absolute term applied as `min(contextWindow, absoluteBudget)` *before* the subtraction; and state what happens to `SummarizeTokenPercent` (delete it under the greenfield rule, or fold it in).

#### [MAJ-002] D3's 128,000-token floor overflows the whole local/self-hosted tier — by the ADR's own criterion that is "the bug"

- **Lens**: Incorrectness (carries first-pass MAJ-011)
- **Affected**: §4 D3, §11b.3 item 2
- **Description**: D3 justifies 128,000 as "what nearly every current model holds at minimum" and states the design principle: "a higher guess would overflow a smaller model, which is the bug." Ollama serves models at a default `num_ctx` of 4,096 unless the operator raises it; `vllm`/LM Studio/`custom` endpoints commonly run 8k–32k. D2's ladder has a live rung for Ollama only; `vllm`, LM Studio and `custom` have no catalog entry and no live rung, so they land on the floor — eight to thirty times their real window. The ADR's §11b.3 mentions `max_model_len` for vLLM in passing but D2's ladder does not include it.
- **Recommendation**: Make the floor provider-class-aware: 128,000 for catalog-known hosted providers only; for `custom`/self-hosted with no live answer, a low floor (e.g. 8,192) with the WARN, plus a required `context_window` field on the custom-endpoint form (it is the one place the operator actually knows the number). Add vLLM `/v1/models` `max_model_len` as a D2 live rung or say it is not.

#### [MAJ-003] Lower-only ratchets (D2 override, D8 learned) with no expiry or reset

- **Lens**: Incompleteness / Inoperability
- **Affected**: §4 D2, §9 D8
- **Description**: The effective window is `min(override, catalog, learned)` and a learned value "may only LOWER the current belief, never raise it." Nothing says when a learned value expires, what clears it, or how an operator sees that it bit. One mis-parsed number from a provider error (they are "observed in the wild, not documented — match loosely") permanently shrinks every agent on that model; a provider that *raises* a model's window (routine) is never believed. D9 shows the source as "learned" but offers no reset.
- **Recommendation**: Give learned values a TTL (e.g. 7 days) and a Settings "clear learned limits" action; store the raw error string beside the number for audit; when the catalog value changes, discard learned values older than the catalog update.

#### [MAJ-004] Catalog schema version is stated as both 1.1.0 and 2.0.0; §13 still describes the retired seed design

- **Lens**: Inconsistency
- **Affected**: §4 D1 "Omnipus-side changes" ("seed schema gains the new fields (1.0.0 → 1.1.0)"), §4 D1 item 5 and §12 ("`schema_version` 2.0.0 … the binary reads 2.0.0 only"), §13 Negative ("The seed acquires two fields to maintain")
- **Description**: The new nested providers-with-models shape is incompatible with the 1.x flat model list, so 2.0.0 is right and "1.0.0 → 1.1.0" is a leftover from the retired draft. §13's negative consequence is likewise the old design's.
- **Recommendation**: Delete the 1.1.0 sentence; rewrite §13's negatives for the registry-fed design (new external dependency on a daily job and a second repository; feed outages leave installs on the embedded snapshot; registry errors propagate within 24 h).

#### [MAJ-005] "Embedded snapshot generated from the feed at build time" makes the build non-hermetic

- **Lens**: Inoperability / Infeasibility
- **Affected**: §4 D1 "Omnipus-side changes", §13
- **Description**: If `go:embed`'s input is produced by fetching the feed during the build, then a release build needs network access, two builds of the same commit differ, the Fly CI worker and any offline/air-gapped build break, and `make verify-contracts`-style drift detection has nothing to compare against. Every surveyed harness ships a *committed* snapshot for this reason.
- **Recommendation**: Commit the snapshot (`pkg/providers/catalog/data/providers_catalog.json`) and refresh it by a scripted, reviewed PR (or a bot PR from the assembly repo). The build embeds whatever is committed. State the staleness bound that implies.

#### [MAJ-006] Signing is "required" but undecided between two options with very different consequences

- **Lens**: Ambiguity / Insecurity
- **Affected**: §4 D1 "Signing — required, not optional"
- **Description**: "sigstore/cosign or a pinned public key compiled into the binary." Cosign verification in Go pulls in the sigstore library tree — a large new dependency set with its own transitive vulnerability surface (Constraint #1 / `govulncheck` gate). A pinned key is stdlib (`crypto/ed25519`) but then key rotation, compromise response, and where the private key lives for an unattended daily job are unspecified. The fallback on bad signature is "embedded snapshot with a WARN" — which, combined with MAJ-005, may be a snapshot months old, silently.
- **Recommendation**: Decide: pinned ed25519 key, private key in the assembly repo's CI secret store, public key embedded; rotation = new binary release; signature failure logs at ERROR (not WARN) and surfaces in Settings → Providers as "catalog not refreshed since <date>: signature invalid".

#### [MAJ-007] D4's "align the shipped per-tool caps" contradicts itself on `read_file` and silently halves three shipped tools

- **Lens**: Inconsistency
- **Affected**: §5 caps table and "Align the shipped per-tool caps"
- **Description**: The builtin success cap is 30,000 chars. `read_file`'s 64 KB is offered as "corroboration the magnitude is right" for the *MCP* figure (62,500) — but `read_file` is a builtin, so alignment puts it at 30,000, less than half of today. `fetch_url` (50,000) and `browser_get_text` (100 KiB) are likewise cut to 30,000, and `read_file`'s `offset`/`length` paging semantics (cited in §6.3 as the model for recall paging) change underneath. No consequence is recorded; agents' existing skills and prompts that assume a 64 KB page break.
- **Recommendation**: Either put `read_file`/`fetch_url`/`browser_get_text` in the 62,500 class explicitly (a "bulk-read builtin" surface), or accept 30,000 and say so in §13 with the affected tools named.

#### [MAJ-008] D10 gives no numbers, and the exit proof's 2 MB test depends on them

- **Lens**: Incompleteness / Inconsistency
- **Affected**: §11 D10, §17.1, §6.2
- **Description**: D10 says every network/subprocess read is bounded at ingest and that exceeding it is a tool failure — but states no bound for MCP results or the three search providers. §17.1 feeds "a ~2 MB tool result through the loop" and expects a marked result and a completed turn; if the MCP ingest bound is below 2 MB the test gets a tool failure instead. §6.2 relies on the 10 MB `maxLineSize`; an ingest bound above 10 MB would write archive lines the reader cannot read back.
- **Recommendation**: State the bounds: MCP result 8 MiB, search responses 1 MiB (matching the two existing `LimitReader` sites), subprocess stdout per existing `shell.go`; require every ingest bound < `maxLineSize`; make §17.1's payload explicitly under the MCP bound.

#### [MAJ-009] Prompt caching is still never mentioned; D5 invalidates the cached prefix on every empty

- **Lens**: Incorrectness (carries first-pass MAJ-021, unaddressed)
- **Affected**: §2 (cost argument), §6, §7
- **Description**: `pkg/providers/anthropic/provider.go` sends `cache_control` (verified). Cached prefixes are keyed on exact prefix bytes; emptying the *oldest* tool result changes the earliest bytes of the request, so every D5 operation — and every D4 cap-state change — invalidates the entire cache for the rest of the turn. §2's "quadratic" cost claim assumes no caching; with caching the unmodified re-sent prefix is ~10% of price, and D5's "run down to a target" (good: batches the invalidation) is the only mitigation, left implicit.
- **Recommendation**: Add a paragraph: emptying is batched to the target precisely to amortise cache invalidation; set the target so that empties happen at most once per N calls; note that on cached providers the cost of *not* emptying is lower than §2 implies and tune the 0.9/target accordingly.

#### [MAJ-010] The greenfield provider-id rename has no defined failure surface on an existing install

- **Lens**: Incompleteness / Inoperability
- **Affected**: §11a.3, scope note "Greenfield rule", §11c.4 "Backward compatibility: none"
- **Description**: After the rename, every existing install whose `config.json`/agent entities use `z-ai-coding`, `moonshot-cn-anthropic`, `qwen-intl`, etc. "simply does not work." The ADR does not say *where* it stops working: at boot (`instance.go` "provider %q not found in configured providers" is reached when building agent instances) or per turn. If boot aborts, the operator cannot reach Settings to fix it — the same "no UI path to fix" trap §10.1 diagnoses for provider deletion. The operator's own install is affected (it runs `z-ai-coding_API_KEY`).
- **Recommendation**: Specify: boot succeeds; each agent with an unresolvable provider is marked `degraded: unknown provider "<id>"` in the Agents list and its turns return a typed error; Settings → Providers is reachable. Add this to the exit proof ("greenfield test" currently only checks that the rename does not happen).

#### [MAJ-011] D14 provider deletion leaves dependent agents in an unspecified state, and the Undo toast implies retaining a deleted secret

- **Lens**: Incompleteness / Insecurity
- **Affected**: §10.1 D14, §10.2 item 6
- **Description**: The confirm dialog lists "3 agents … use OpenRouter. Remove anyway?" — and then what do those three agents do? Nothing says. The default-model case is guarded; the per-agent case is not. Separately, "remove immediately with a 5-second Undo toast" requires that the API key survive the `DELETE` for 5 s somewhere — either the SPA holds the plaintext key in memory to re-`PUT` it (it does not have it; keys are write-only refs), or the backend soft-deletes. Neither is specified; the first would be a credential-handling regression against SEC-23.
- **Recommendation**: Define: agents bound to a deleted provider fall back to the default model with a WARN and a badge; if the deleted provider *is* the default, the inline re-pick is mandatory (already stated). Make delete hard and confirm when in use; drop the Undo toast for the in-use case, or specify server-side soft-delete with a 5 s grace and no key leaving the store.

#### [MAJ-012] D13 rule 4 contradicts rule 3 for OpenAI; the xAI path depends on an external party's consent

- **Lens**: Inconsistency / Incompleteness
- **Affected**: §11c.1 (OpenAI row), §11c.3 items 3–4
- **Description**: Rule 4: "Never collect, store, proxy or refresh a vendor's consumer credential where the vendor prohibits it." §11c.1: OpenAI's ToS "still prohibits … with no carve-out"; the permission is a tweet. Rule 3 ships it anyway "documented as practice-based". Both cannot be policy. For xAI, rule 3 says "ask xAI to list Omnipus, as the five named agents are"; nothing says what ships if xAI declines or does not answer — the same AUP "bans unauthorised bots".
- **Recommendation**: Amend rule 4 to "where the vendor's terms prohibit it *and* no official vendor statement permits it", and record the OpenAI statement as the permitting source with a re-verification date. For xAI, state the gate: the Sign-in option is shipped disabled until the listing exists, or shipped on the strength of the published OAuth flow with the risk accepted in writing.

#### [MAJ-013] "Prefer the `codex_cli_provider.go` subprocess path where both exist" does not say which one the `codex-cli` id dispatches to

- **Lens**: Ambiguity
- **Affected**: §11c.2 `codex-cli` bullet, §11c.3 item 5
- **Description**: The ADR establishes that `case "codex-cli"` is token reuse (`NewCodexProviderWithTokenSource`) and that a real subprocess provider exists in `codex_cli_provider.go`. "Prefer" is not a dispatch rule. After D11 collapses the switch to protocol dispatch, `cli` is one protocol — which implementation does it name for OpenAI?
- **Recommendation**: Decide: the canonical `codex-cli` maps to the subprocess provider; the token-reuse provider is either deleted (greenfield) or kept under a distinct id (`chatgpt-login`) with the §11c.1 citation on its Settings row.

#### [MAJ-014] D1's "open an issue on disagreement" leaves the published value during the disagreement unspecified

- **Lens**: Ambiguity / Incompleteness
- **Affected**: §4 D1 assembly job step 4
- **Description**: When models.dev and LiteLLM disagree on a window, the job "opens an issue … rather than silently choosing." It must still publish a file that day. Which value? If it publishes nothing for that model, the binary falls to the 128k floor (MAJ-002); if it publishes the higher one, §1.1 recurs.
- **Recommendation**: Publish the **lower** of the two pending adjudication (consistent with D2/D8's lower-wins rule) and mark the entry `disputed: true` so D9 can show the source as "catalog (disputed)".

#### [MAJ-015] Scope: D9–D14 have no exit proof, and the document now carries a second programme

- **Lens**: Overcomplexity (carries first-pass OBS-001, which the restructure inverted)
- **Affected**: §10–§11c, §17
- **Description**: §17's six proofs cover D4–D8 and the greenfield grep. Nothing tests the assembly repo, signing, the 24 h refresh, the provider rename, the protocol-dispatch factory, subscription sign-in, provider deletion, the shared picker, or the default-model card. The ADR's scope note says it "adds no summarisation, deletes nothing from disk, introduces no new storage" — and then introduces a new repository, a signed feed, a rename touching 1,241 literals, three OAuth flows and an onboarding redesign. An implementer cannot tell what "done" means for more than half the decisions, and a reviewer cannot ratify "three focused edits" (§13) that are demonstrably not three.
- **Recommendation**: Split into ADR-066 (D2–D8, D10: the incident fix, ratifiable now) and ADR-067 (D1 feed + D11/D12 identity/tiers) and ADR-068 (D13/D14/§10 subscription, deletion, UX), each with its own exit proof. If the operator prefers one document, add exit-proof items for every decision.

#### [MAJ-016] The mid-turn floor protects one result; an assistant message with N parallel calls has N results the model is reasoning about

- **Lens**: Incompleteness
- **Affected**: §7 table ("the most recent tool result — never")
- **Description**: The incident's assistant message issued three tool calls; results are appended sequentially. When the third result arrives and the check fires, results 1 and 2 of the *same* assistant message are "older" and eligible — the model then reasons over a reply to a three-call message with two of its answers blanked. Under D4 caps this is ≤187,500 chars and unlikely to trigger at the 400k absolute, but at a 128k-floor model (budget ≈ 320k chars minus system/tools) it is reachable.
- **Recommendation**: Define the floor as "every result of the most recent assistant message", and state that if that set alone exceeds the budget the thrash guard applies (or that such a message's *earlier* results are capped harder).

#### [MAJ-017] §6.3 "rolled back on abort" — `RollbackAppended(targetLines, targetSkip)` has no emptied-set parameter, and a *successful* turn that emptied has no restore-point update

- **Lens**: Incompleteness
- **Affected**: §6.3 last paragraph, §17.2b
- **Description**: `memory.Store.RollbackAppended` restores line count and `Skip` (verified in `pkg/memory/store.go`); the emptied-set is new meta state and needs both the interface change and the `refreshRestorePointFromSession` update after every D5 operation (the proactive-trim site calls it; the ADR does not say the mid-turn site does). Without the refresh, a later abort in the same turn would roll the emptied-set back to turn-start while `Skip`… also goes to turn-start — consistent, but the *archive* still holds the full content with no cap state, which loops back to CRIT-001.
- **Recommendation**: Extend the `Store` interface explicitly (`RollbackAppended(targetLines, targetSkip, targetEmptied)`), state that the restore point is refreshed after every mid-turn projection change, and add that to §17.2b.

### MINOR

#### [MIN-001] §6's literal mark template vs §12's "must not be hand-rolled with `fmt.Sprintf`"
§6 shows a free-text template including "turn 6" (turn numbering source undefined — `parseTurnBoundaries` index? session stats?). Define the mark as a typed struct with a single renderer, and name the turn-number source.

#### [MIN-002] Order of the sensitive-data filter relative to the D4 cap is unspecified
`cfg.FilterSensitiveData(contentForLLM)` runs before the message is built. Head-and-tail truncation after filtering is fine; filtering after truncation could split a secret across the cut and miss it. State: filter first, then cap; the archive copy is also filtered (it is today).

#### [MIN-003] Exit proof 6's `grep -E 'deprecat'` will match the catalog's own `status: deprecated` field
D1 step 2 carries "deprecation status" per model. Either rename the field (`status: retired`) or narrow the grep.

#### [MIN-004] Catalog payload size is unstated
models.dev `api.json` is multi-megabyte; embedding ~7,000 models in the binary and serving them to the SPA on every Settings open needs a number and a cache rule (ETag). Also: unauthenticated GitHub API calls are limited to 60/hour/IP — many installs behind one NAT will hit it at the same daily time; rely on the raw-asset URL path first.

#### [MIN-005] The mark embeds the tool name, which for MCP is server-chosen text
A hostile server can name a tool `ignore_previous_instructions_and_…`; the mark then renders that in harness voice. Tool names already reach the model in definitions, so the marginal risk is small — but sanitise/length-limit the name in the mark.

#### [MIN-006] D7 does not name the event kind or payload for the four silent exits
Constraint #8: the WS frame must exist in `asyncapi.yaml`. Say whether it reuses `EventKindTurnTimeout`/an existing turn-end payload or adds one.

#### [MIN-007] D4 "Warn threshold (metric) 25,000 chars" has nowhere to go
No metrics sink is named. State the log line + counter name, or drop the row.

#### [MIN-008] §11b.2 Popular list has 8 names; the text says "~6–8"; §10.2 says "8 Popular tiles"
Pin it at 8 and say which.

#### [MIN-009] §15.1's audit table claims 89 tools; CLAUDE.md says 41 `system.*` tools plus general/browser — reconcile the count and cite the policy map revision it was read from.

### OBSERVATION

- **[OBS-001]** Third-party PII in committed session archives (first-pass CRIT-002) is now *acknowledged* (§14.1 "committed by design") rather than addressed. It is pre-existing and out of this ADR's scope, but §14.1's parenthetical should point at the tracking issue rather than leave it as an aside.
- **[OBS-002]** `max_tool_iterations` default 200 vs operator's 50 (§14.7) is noted as a runaway guard, not a context control. With D6 in place that is true; consider lowering the seed anyway in `pkg/config/defaults.go` as a separate one-line change.
- **[OBS-003]** §16's first [UNVERIFIED] (Anthropic rejects a non-user first message) is documented Anthropic behaviour and can be cited from the Messages API reference rather than left open.
- **[OBS-004]** The ADR's evidence discipline (searches cited as absences, `[CORRECTED]` markers, the retired-decisions section) is the right pattern and should be kept through the split recommended in MAJ-015.

---

## Structural Integrity (narrative — generic-markdown mode)

- **Scope clarity**: The scope note describes D2–D8. The document's actual scope (D1–D14, §10.2 UX) is several times larger; the mismatch is itself a defect (MAJ-015).
- **Actors**: Operator, model, MCP servers, the assembly job, registries, vendors — present. Missing: the *existing-install operator* on upgrade (MAJ-010).
- **Success criteria**: §17 covers D4–D8 only.
- **Failure modes**: Good for the window; absent for the feed (outage, bad signature beyond "WARN"), for provider deletion, and for unknown-provider boot.
- **Implementation detail**: Sufficient for D4–D7 *except* the projection mechanics (CRIT-001/002), which are the load-bearing part.
- **Assumptions**: "A window is a separate store" is implicit and false; "D4 makes the thrash guard unreachable" is explicit and false.
- **Constraints**: Constraints #1 and #8 are cited; #1 is at risk under the cosign option (MAJ-006).

## Test Coverage Assessment

§17 is a reasonable behavioural suite for D4–D8 but (a) item 1 depends on an unstated ingest bound (MAJ-008), (b) item 2's recall assertion cannot pass under the document's own numbers (CRIT-003), (c) item 4 asserts a behaviour that contradicts D7 (CRIT-004), (d) there is no live-vs-reload byte-equality test for the projection (CRIT-002), no prompt-cache-hit regression test (MAJ-009), no test for the multi-call floor (MAJ-016), no test for learned-value expiry (MAJ-003), and nothing for D9–D14.

## STRIDE Summary

| Component | Threat | Status |
|---|---|---|
| Catalog feed (assembly repo → installs) | Tampering (supply chain) | Mitigated only if MAJ-006 is decided; fallback is a possibly stale snapshot |
| Catalog feed | DoS (GitHub rate limits, feed outage) | Degrades to snapshot; rate-limit path unstated (MIN-004) |
| D5 recall mark | Spoofing harness voice via MCP tool name | Low; sanitise (MIN-005) |
| D8 learned limits from error text | Tampering (a proxy/aggregator can feed a tiny number and shrink every agent's window — availability attack) | Lower-only ratchet with no reset makes it sticky (MAJ-003) |
| Provider deletion + Undo | Information disclosure (key retained client-side) | Unspecified (MAJ-011) |
| Subscription sign-in (xAI/OpenAI/Copilot) | Repudiation / ToS exposure | Policy contradiction (MAJ-012); token storage path unspecified for the OAuth flows the UI adds |
| D7 typed exits | Repudiation | Addressed — log + event + transcript |

## Unasked Questions

1. Where, exactly, does the byte stream that reaches the provider get built mid-turn, and which function applies the projection to it?
2. What does an agent do whose provider id no longer resolves after the rename — at boot, at turn, in the UI?
3. What is the recall page size, and is the `tool_call_id` mode subject to the 4k/8k span budget or not?
4. What does the model see when the *user's* message alone exceeds the budget?
5. Who holds the signing key, and what does an install show when the feed has not verified for 30 days?
6. Is the 400,000-char absolute budget per request, or does it also bound the D4-capped *archive* growth per turn (disk)?
7. Which of the 23 current `catalog.Entries` have no models.dev counterpart, and what happens to a configured install that uses one?
8. How does the SPA learn a catalog refresh happened mid-session (cache invalidation for the picker)?
