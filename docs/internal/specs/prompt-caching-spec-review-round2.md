> **Correction:** The initial skill return said persistence was prohibited and described an eight-lens review. That was wrong: the current repository skill requires twelve lenses and a saved report, and this dispatch expressly authorizes this report. It also overstated two findings: the normative mixed-price correction already requires storing the unpriced portion, and Fireworks now documents the standard cached-token field as well as performance metrics. Correct is the twelve-lens, source-verified review below. Impact: mixed-price persistence is a narrower, non-blocking clarification; the Fireworks finding concerns its incorrectly specified performance-metrics branch, not every stream. This saved report replaces the initial return.

# Adversarial Review: Prompt caching — round 2

**Verdict: REVISE.** Four MAJOR findings are **BLOCKING**, four MINOR findings are **NON-BLOCKING**, and one observation is non-gating. Of the fourteen round-1 items, twelve are **CLOSED** in the written specification and two are **PARTLY** closed. The revised Usage-screen contract was checked against the existing screen, mounted tests, route, component catalog and documentation, not treated as optional backend finishing work.

| Review metadata | Value |
|---|---|
| Specification (**S** below) | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/docs/internal/specs/prompt-caching-spec.md` |
| Round-1 report | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/docs/internal/specs/prompt-caching-spec-review.md` |
| Mode / round / date | Spec mode; **2 of 2, fixed and final**; 2026-10-15 |
| Branch / reviewed commit | `work/prompt-caching-spec` at `0bb4ae257584b7ba907c97205276fd63f314da5c`, the round-1 fix and fetched origin tip |
| Baseline | The provider, agent, session, UI and contract code is unchanged from `8ba69a673`, the code baseline named by S; the intervening diff adds only S and its round-1 report. No comparison with a different main/release tip was substituted |
| Binding decisions | D1–D20, including the corrections to D8/D18, in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/prompt-caching-interview-decisions-20261015.md` |
| Reviewer independence | Independent reviewer per dispatch; runtime identifies this reviewer as GPT-6.1-sol. The spec author's model is not recorded in S, so model separation is supplied by the dispatch, not independently proved from Git authorship |
| Method / limits | Complete spec, round-1 report and fix diff read; code and official documentation checked; independent arithmetic and controlled document audit. **No Go, SPA, browser, CI or live-inference run** was performed |
| Code intelligence | GitNexus MCP `query`, `context` and `impact` tools were not exposed in this session. Direct source/caller searches and Read were used. No graph-impact or `detect_changes` run is claimed |
| Authorized change | This report only. S, its previous review, production code, tests, contracts, ADRs and provider settings remain unchanged |

| Severity | Count | Disposition |
|---|---:|---|
| CRITICAL | 0 | None demonstrated |
| MAJOR | 4 | BLOCKING before implementation is treated as ready |
| MINOR | 4 | NON-BLOCKING; correct in the final correction |
| OBSERVATION | 1 | NON-BLOCKING |
| **Total** | **9** | One separate founder question is not counted as a finding |

**Certainty:** Verified means the cited specification/source fact was inspected. Inferred means the consequence follows from that fact without an executed product test. Provider behavior not exercised live remains unverified as a runtime claim; it is not silently certified by a documentation fetch.

## 1. Round-1 closure verification — first

These are **text-closure statuses**, not implementation or test-pass claims. The normative precedence statement at the start of S's **Round-1 corrections** was honored: superseded wording elsewhere was not used to reopen D19 or D20.

| Round-1 item | Status | Exact revised section / requirement and assessment |
|---|---|---|
| MAJ-001 — Tail lost during normalization | **CLOSED** | **The volatile tail survives the send path**; FR-007; `TestPrefixStable_AfterNormalizeAndAdapter`. One trailing user-role block after completed tool groups, history mark before it, captured after normalization and adapter conversion. This addresses the source path in E2 |
| MAJ-002 — Replacement-connector parity | **CLOSED** | **The surviving connector keeps the old guarantees**; FR-004; `TestConnector_ReconstructedCallAndVerbatimModel`. Canonical reconstructed calls, verbatim dotted model ids, media and correlated parallel results must pass factory-to-wire before old tests/package deletion |
| MAJ-003 — Host-specific cache dialects | **CLOSED** | **One connector, each host's own cache rules**; D19; FR-005. Native lifetimes are conditional; Moonshot sends no marker/one-hour option; MiniMax uses its documented model mode. Older universal-lifetime sentences do not override this correction |
| MAJ-004 — Durable provider identity | **CLOSED** | **Provider identity is stored with the call**; **Contract changes**; FR-002. Actual provider/model/billing identity per accounted call, billed errors, existing transcript carrier, provider aggregates, reload and reconciliation are explicit. That is a design obligation to implement, not a claim that the current turn-total writers already do it |
| MAJ-005 — Retained tools must execute and remain isolated | **CLOSED** | **A retained tool stays callable, and only in that session**; FR-008; `TestLoadedMcpTool_ExecutesOnlyInThatSession`. Real non-core MCP expiry, invocation, simultaneous second session and revocation/disconnect are specified. Current authorization wins over prefix stability |
| MAJ-006 — Whole-input price tier and canonical precedence | **PARTLY** | **The price tier sees the whole input**; FR-018; `TestPrice_TierUsesCacheHeavyInput`. Complete input and canonical-over-alias precedence are fixed. The now-explicit inclusive equality rule still disagrees with a source price boundary: R2-MAJ-001 |
| MAJ-007 — Arbitrary provider usage in the log | **CLOSED** | **The log carries numbers, not an object**; D20; FR-001/025; `TestLogLine_DropsTextInsideUsage`. Documented numeric paths only, unknown/text/oversized extensions dropped, maximum 32 counters, bounded nesting and existing credential scrubbing; successful-response privacy fixture required |
| MAJ-008 — Live check must exercise the changed connector | **CLOSED** | **The live check hits the connector being replaced**; FR-020; SC-005. Explicit Anthropic routes for DeepSeek/Moonshot/Z.AI/MiniMax, model/endpoint/protocol/connector/commit receipts, and tester plus independent-validator moved-note check on that commit |
| MAJ-009 — Static-only reads must fail history proof | **CLOSED** | **A history hit has to include history**; SC-001; `TestHistoryHit_StaticOnlyFails`. Threshold includes tools and all front instruction blocks; fake hit depends on captured prefix and valid mark placement, not request ordinal. Q1 below concerns which live routes must meet that threshold, not the threshold's correctness |
| MAJ-010 — Mandatory frontend contract and direct tests | **CLOSED** | **The Usage screen contract**; FR-002; `TestUsageScreen_HitPercentAndProviderTab`. Provider tab on existing Tabs, `0.8 → 80%`, null not zero, loading/empty/error/partial, period updates, focus, keyboard and narrow layout, plus mounted tests and typecheck |
| MAJ-011 — Log-monitoring documentation | **CLOSED** | **Documentation**; SC-006. Both `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/docs/settings.md` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/docs/operations/observability.md` have specific same-change TODOs; implementing lead drafts and docs-verifier audits |
| MIN-001 — Mixed priced/unpriced accounting | **PARTLY** | **Mixed prices and the stored split** fixes portion arithmetic, known-dollar preservation, the one-hour exception and durable lifetime counts. Its storage sentence is meaningful and was not ignored. The later producer field list and reload test still omit an explicit captured unpriced count: narrower clarification R2-MIN-002 |
| MIN-002 — Correlate the accounting line | **CLOSED** | **The log carries numbers, not an object**; FR-001. Acting agent, session and turn/call ids required on the same line |
| OBS-001 — Independent purpose of tool breakpoint | **CLOSED** | **One connector, each host's own cache rules**; FR-005. Separate tool mark is expressly for reuse when instructions change. Both founder-approved marks remain; no removal is requested |

## 2. Findings on the revised specification

### R2-MAJ-001 — The inclusive tier rule applies some published prices one token early

| Field | Assessment |
|---|---|
| Severity / gate | **MAJOR — BLOCKING** |
| Lenses / affected section | Incorrectness and decision consistency; **The price tier sees the whole input**, FR-018, User Story 5 acceptance 4, **A length band selects the matching catalog tier** |
| Failure scenario | A Haiku 5.5 call has exactly 100,000 total input tokens. The current source catalog has `tier.size: 100000`, while Anthropic prices prompts **up to 100,000** at the base rate and **over 100,000** at the high rate. S's “highest canonical tier whose size that total has reached” and its equal-to-threshold examples choose the high rate. For 100,000 uncached tokens, the input component becomes **$0.05 instead of $0.01**. A cache-heavy split of 100 uncached plus 99,900 read tokens becomes **$0.005045 instead of $0.001009** |
| Evidence | S::FR-018 and length-band examples; E4's current models.dev entry and Anthropic pricing wording; independently calculated below/at/above cases in the input-audit receipt |
| Certainty | **Verified — high** for the source boundary, specification rule and arithmetic; no deployed price calculator was run |
| Recommendation | Preserve source boundary semantics during catalog normalization rather than treating every raw `tier.size` as inclusive. State the equality rule at the catalog/runtime seam, including the legacy-only alias rule. Keep the complete-input basis and canonical precedence already fixed. Add source-derived **99,999 / 100,000 / 100,001** tests, including cache-heavy input. Do not replace one universal assumption with a universal exclusive rule: other source bands may be inclusive |

This completes the round-1 pricing finding; it does not reopen catalog-sourced pricing or add a hand-kept dollar table.

### R2-MAJ-002 — Claude through Vercel is left without either automatic or manual markers

| Field | Assessment |
|---|---|
| Severity / gate | **MAJOR — BLOCKING** |
| Lenses / affected section | Completeness and provider reachability; **Provider table** Vercel row; **OpenAI-compatible hosts**; FR-028; D2 |
| Failure scenario | An operator selects a callable Vercel Claude model such as `anthropic/claude-opus-4.5`. S sends only a routing key and forbids `caching: auto`. It specifies no manual marker for that row. Vercel passes unmodified requests through when automatic rewriting is off, and its documentation explicitly says Anthropic then requires manual cache markers. Stable prefix and a session pin alone therefore do not enable caching on that route |
| Evidence | E5: the embedded Vercel row is standard-tier, OpenAI-compatible and contains Claude models; `CreateProviderFromConfig` routes it to the shared HTTP client. Vercel's **Automatic Caching → Default behavior** and **OpenAI Chat Completions → Manual caching** document the missing manual `cache_control` alternative |
| Certainty | **Verified — high** for the callable route, missing outbound instruction and vendor contract; resulting misses **Inferred — high**. No paid call was sent |
| Recommendation | Keep FR-028's no-automatic-rewrite decision. Specify the documented manual, model-aware marker placement for explicit-cache upstreams through Vercel, before the volatile tail, and a captured-request fixture for a Vercel Claude model. Preserve implicit-only upstream behavior. Do not add a second connector or enable the prohibited automatic rewrite. A deliberate exclusion would need an explicit D2 exception, not an assumption that a routing key covers this route |

### R2-MAJ-003 — Fireworks performance-metric names and streaming acquisition are specified incorrectly

| Field | Assessment |
|---|---|
| Severity / gate | **MAJOR — BLOCKING** for the named metrics branch, not every Fireworks stream |
| Lenses / affected section | Provider-contract correctness and false-green risk; **Provider table** Fireworks row; **Usage shapes**; FR-011/019 |
| Failure scenario | A Fireworks response exposes its cached count through performance metrics while the optional standard usage detail is absent. S asks for header `fireworks-cached-prompt-tokens` and “the same name under performance metrics.” The body actually uses **`perf_metrics["cached-prompt-tokens"]`**, without the `fireworks-` prefix. On streaming calls those metrics arrive in the final `finish_reason` chunk and the metrics-body option defaults false. A parser/test using the specified prefixed body key, or only the planned non-streaming header fixture, misses this documented nonzero read |
| Evidence | E6: saved official schema::`perf_metrics_in_response`, `ChatCompletionStreamResponse.perf_metrics`, `PromptTokensDetails`; current `ChatStream` sends `include_usage` but no metrics opt-in. The schema separately documents `usage.prompt_tokens_details.cached_tokens` and final usage chunks. Those are **not** evidence that every stream requires the metrics option |
| Certainty | **Verified — high** for the wrong body key, request default, chunk placement and optional standard field; metrics-only failure **Inferred — medium/high** from the documented optional response branches, not observed live |
| Recommendation | State the exact standard, header and body paths, their precedence and no-double-counting rule. If retaining the performance-metrics fallback required by the current table, acquire it with this host's documented `perf_metrics_in_response: true` option and consume the terminal metrics chunk. Add standard-usage-only, metrics-only and duplicate-counter fixtures, including a streaming captured-request fixture. Do not require a performance flag on unrelated providers. Alternatively, explicitly justify a standard-usage-only scope with evidence instead of silently claiming the metrics branch is implemented |

**Research correction:** the earlier research statement that Fireworks has no standard cached-token usage field is no longer supported by the reference fetched for this review. Both channels must be distinguished. This finding does not assert that ordinary standard-usage streaming currently always reports zero.

### R2-MAJ-004 — The plan misses cache-only, zero-output usage at the persistence boundary

| Field | Assessment |
|---|---|
| Severity / gate | **MAJOR — BLOCKING** |
| Lenses / affected section | Accounting completeness and testability; **Usage shapes**, **Provider identity is stored with the call**, FR-002/011/019; persisted-statistics integration tests |
| Failure scenario | An accounted response or billed stopped/error attempt has known uncached input **0**, output **0**, cache read **800**, cache write **200**, authoritative total **1,000**. The current accumulator decides that a split exists only when prompt or output is positive. It books all 1,000 as output, then adds the 1,000 cache tokens separately: session component sum **2,000**, authoritative total **1,000**. Its per-model output remains zero, so model and session output disagree. An all-read 1,000/zero-write case has the same problem. A parser-only normalization test can be green while the persisted report is wrong |
| Evidence | E3: `accumulateEntryStats`'s `hasSplit := promptTokens > 0 \|\| completionTokens > 0`, fallback to `entry.Tokens`, unconditional cache fold, and different per-model path. Existing `TestAppendTranscriptStrict_HasSplitBoundary` covers the two one-zero asymmetric cases, not both zero with positive cache. S's dataset has no such case. Independent predicate arithmetic is recorded; no Go function was executed |
| Certainty | **Verified — high** for the source predicate and missing planned boundary; failure consequence **Inferred — high**, directly from the inspected arithmetic |
| Recommendation | Add a **stored-entry → session/model/provider aggregates → report → reload** boundary fixture, not just a parser case. Require 0 input, 0 output, 800 read, 200 write and total 1,000 throughout; also an all-read fixture. Make a known zero split distinct from genuinely absent usage. Keep a no-split positive control where appropriate; do not reintroduce upgrade-only backfills. The implementing lead owns any source correction; this review changes no code |

### MINOR findings — all NON-BLOCKING

| ID / lenses / affected section | Failure scenario and evidence | Specific correction / certainty |
|---|---|---|
| **R2-MIN-001 — NON-BLOCKING** — Ambiguity; **Provider table** Alibaba rows; **Usage shapes** | The read path is qualified as `prompt_tokens_details.cached_tokens`, but creation is shortened to `cache_creation_input_tokens`. An implementer can parse only the top-level Anthropic form and miss the OpenAI-compatible explicit-write count, understating write cost. E7's current official examples use **`usage.prompt_tokens_details.cache_creation_input_tokens`** for Chat Completions and top-level **`usage.cache_creation_input_tokens`** for Messages | Print both full paths with their protocols, and use the actual nested raw JSON in the Alibaba fixture, not only normalized expected numbers. **Verified — high** for nesting/ambiguity; consequence **Inferred — high** |
| **R2-MIN-002 — NON-BLOCKING** — Carrier/test completeness; **Mixed prices and the stored split**, **Contract changes**, `TestProviderId_SurvivesReload` | The normative correction already says a mixed call stores only its unpriced portion in `unpriced_tokens`; it is therefore wrong to call reconstruction impossible under every compliant implementation. However, the later `TranscriptEntry` addition list omits that count and its reload test names provider identity, not mixed-price historical classification. Following that list alone can leave only `billing_class`, token categories and dollars; a later catalog cannot recover how many tokens were unpriced at call time. E3 identifies the existing durable carrier and accumulator | Make the per-call captured `unpriced_tokens` explicit in that field list and define how a mixed call uses `billing_class`. Fold the captured count into existing totals; do not reclassify history from a newer catalog. Add mixed priced/unpriced plus **price availability changes → reload** coverage. No new store, price snapshot service or migration. **Verified — high** for the inconsistent enumeration; consequence **Inferred — medium** because the normative storage sentence already excludes the lossy implementation |
| **R2-MIN-003 — NON-BLOCKING** — Units/test oracle; FR-009, skill-list acceptance/BDD and measurement test | D5 expressly requires **tool and skill token sizes**. S/test language requires only “size,” so a byte or character count can satisfy the test while not supplying the token-cost evidence needed for the later all-tools decision. E1::D5 and S::FR-009/skill-list scenario | Require labelled **token counts or token estimates per agent**, identify the estimator and label any approximation. Bytes may be additional, separately named data. Preserve the existing later decision about all-tools exposure. **Verified — high** for the missing unit; consequence **Inferred — high** |
| **R2-MIN-004 — NON-BLOCKING** — Contract-first/dependency consistency; **Dependency graph and implementation order**, **Contract changes** | The graph puts the log line before “usage report fields + contract regen”; step 9 includes unpriced stats before step 10's contract fields/regeneration. That order lets implementers change SPA-readable persisted statistics before their schema/generated types. The generic Constraint #8 reminder does not make the conflicting work order useful. E10 verifies that the existing session/report cells are cross-boundary schemas | Put backend-lead's schema edits and generation ahead of changes to the affected persisted-statistics writers, report and screen. Name the five-step contract-first sequence as a prerequisite, and place the contract section before implementation detail. This is a correction to the plan, not permission for this reviewer to edit contracts. **Verified — high** for the conflicting order; rework/drift consequence **Inferred — high** |

### R2-OBS-001 — Consolidate superseded statements instead of carrying a permanent correction layer

**OBSERVATION — NON-BLOCKING.** Lens: overcomplexity. The normative precedence clause resolves the older universal-lifetime statements, conditional screen-test wording and incomplete single-page documentation summaries. They are not independent blockers. In the final correction, make those sections point to the governing dialect/UI/docs contracts or consolidate the text. Otherwise a test-plan generator reading only a later scenario can reproduce a superseded rule. Retain both approved tool/instruction breakpoints and D19/D20. **Verified** for duplicated wording; future reader confusion **Inferred**.

## 3. Structural integrity and all twelve lenses

Mechanical coverage is not behavioral proof. The controlled audit found **6 user stories, 29 functional requirements, 29 matrix rows, 29 BDD scenarios and 29 back-references**. Removing FR-001 in an in-memory copy was detected before trusting the audit. No product test is represented by these counts.

| Structural check | Result | Assessment |
|---|---|---|
| Status / mode / fixed round | Present | Draft; round-1 review exists; no previous round-2 report existed before this task |
| Architectural authority | Present with required follow-through | D1–D20 decide the direction; **Tool manifest tier redesign** is linked with an explicit dated-amendment prerequisite. No new ADR is requested for a decision already made |
| Contract-first sequencing | **Needs correction** | Schema names and generated-only ownership are stated, but section placement/work order conflict: R2-MIN-004 |
| API/data ownership | Improved, narrower gap remains | Per-call producer/provider attribution, durable lifetime split, report/session aggregates and reload are specified; R2-MIN-002 clarifies historical unpriced classification |
| Changed screen states and journey | Present | Existing Usage route, selected period → agent/provider breakdown, visible percent, null, loading/empty/error/partial, no new screen |
| Keyboard / focus / narrow screens | Present | Existing Tabs reused; keyboard selection, focus preservation on period changes and percent visibility specified. This is a written requirement, not browser proof |
| Catalog/brand | Present | Existing Tabs, Card, Progress and SegmentedControl catalog entries checked; no custom widget or new visual system is needed |
| Security/user promises | Present for reviewed change | Numeric-only bounded log and live authorization/revocation rules address round-1 issues. No new exploitable security defect was verified; runtime enforcement remains for the implementation/security gate |
| BDD/traceability | Mechanically complete, data gaps remain | All 29 FR ids mapped. R2-MAJ-001/003/004 and R2-MIN-001/002 require more precise fixtures |
| Reachability/executed proof | Named, not executed | Existing route/screen/callable provider factory verified; Vercel marker gap R2-MAJ-002. Live acceptance population needs Q1 |
| User-facing docs TODOs | Present | Both named pages, log-level procedure, example/ratio/billing explanations, display semantics and docs-verifier owner now specified |

| Lens | Result of this round |
|---|---|
| 1 — Ambiguity | Tier equality R2-MAJ-001; field paths R2-MIN-001; units R2-MIN-003; live-population question Q1 |
| 2 — Incompleteness | Unmarked Vercel route R2-MAJ-002; metrics-only and cache-only boundary R2-MAJ-003/004; explicit historical count R2-MIN-002 |
| 3 — ADR/as-is/decision consistency | D19/D20 honored, not reopened; R2-MAJ-001 and R2-MIN-004. Source wins over dated walkthroughs |
| 4 — Infeasibility | Vercel's prescribed key-only request cannot enable documented explicit caching; R2-MAJ-002. No new runtime/process/database requirement was found |
| 5 — Contract-first | Existing REST and shared cells checked first-hand; R2-MIN-004 sequencing. Per-call transcript counters have no current SPA consumer in the controlled whole-tree sweep; do not invent a parallel wire type |
| 6 — Security | No additional verified gating security finding. Bounded counter selection and scrubbing, live tool assignment/Deny/revocation, and non-secret session keys were assessed against user promises; see STRIDE table |
| 7 — Reachability | Existing Usage screen and authenticated stats route verified; existing factory can call Vercel Claude, which lacks markers under S: R2-MAJ-002 |
| 8 — UI states/journey | Round-1 omission closed by an explicit additive screen contract and mounted tests. Current skeleton, error, empty and partial branches read. No unsupported new screen or journey assumed |
| 9 — Accessibility/keyboard | Existing Tabs and period control reused with required focus/keyboard behavior. No new inaccessible custom control specified. Execution remains unverified; absence of a browser run is not a PASS claim |
| 10 — Design-system/brand | Catalog entries, definition, brand and UI rules read. No new component/visual redesign is proposed; inherited controls stay. Relevant lock/typecheck obligations must remain in implementation gates |
| 11 — Testability/false greens | Header-only Fireworks fixture R2-MAJ-003, parser-only all-cache fixture R2-MAJ-004, wrong source-boundary oracle R2-MAJ-001. Positive document controls and independent arithmetic used; no runtime green claimed |
| 12 — Overcomplexity | R2-OBS-001. Keep the existing logger, transcript, session statistics, provider adapters and generated schemas; no extra cache store, rollout flag, alerting system, pricing service or cap |

### Frontend/UX assessment

The revised written contract now covers H1 (honest loading/error/partial status), H2 (a correctly labelled percent), H4 (existing controls), H7 (keyboard operation) and H10 (two concrete documentation destinations). The current screen's “Cached” figure includes reads **and writes**, whereas “Cache hit” uses **reads divided by full input**; retaining those distinct labels and the documented formula is necessary. The screen still counts tokens rather than money. No appearance, interaction or accessibility test was run by this review.

## 4. Test-plan corrections

These are requirements on the final plan and implementation, not authored or executed tests.

| Level / dataset | Required proof |
|---|---|
| Source-derived prices | Exact boundary cases and cache-heavy total; catalog normalization must preserve inclusive versus exclusive semantics |
| OpenAI-shaped router request | Vercel Claude manual mark at the reusable-history boundary with automatic rewrite absent; implicit upstream gets no inappropriate mark |
| Fireworks streaming | Exact final metrics chunk, body flag when using metrics, correct key, standard-usage alternative, duplicate counter not added twice; header-only fixture is insufficient |
| Provider raw usage shapes | Alibaba nested write JSON, rather than inventing a top-level count to make normalization pass |
| Persisted cache-only usage | Input/output both zero, positive cache and billed attempt; session/model/provider splits reconcile through append/report/reload, with absent-usage control |
| Mixed-price persistence | Captured unpriced count, known-dollar preservation, lifetime split, historical values unchanged after catalog changes and reload |
| Measurement | Labelled tool/skill token estimates per agent, estimator identified; byte-only success fails the intended requirement |
| Existing regression obligations | Keep final-wire normalization, connector reconstructed-call/model/media parity, real MCP expiry/concurrency/revocation, successful-response log privacy, and static-only-history negative control from round 1 |
| Screen | Execute mounted percent/null/provider/period/loading/empty/error/partial tests, keyboard/focus and narrow-screen checks; retain existing screen regressions, typecheck and applicable design-system gates |
| Live / hands-on | One explicit route/assertion/evidence table after Q1; exact candidate commit and independent behavior validator retained. No fake/HTTP-success substitute for cache reads |

**Independent cost oracle:** S's fixture still equals **$0.0204**. The cache-only arithmetic counterexample was calculated from the inspected predicate with a valid asymmetric-split positive control. That is evidence for a plan gap, not a Go regression-test receipt.

The Gemini include/exclude rule remains **Unknown until the required live check**. S explicitly makes that check resolve the parser before landing; this review does not invent a subtraction rule or certify a permanently double-counted total.

## 5. STRIDE threat summary

STRIDE covers identity spoofing, tampering, repudiation, information disclosure, resource exhaustion and privilege escalation. “Addressed in writing” is not a claim that implementation enforcement passed.

| Flow | Assessment / disposition |
|---|---|
| Prompt assembly → adapter | Normalization/tail placement and tool-result grouping now have final-wire requirements. Existing model-note behavior still needs exact-commit hands-on evidence |
| Retained MCP tool → execution | Session isolation, live Deny/assignment removal/server removal/disconnect are explicit. Current policy and assignment functions were read. No bypass was demonstrated or accepted |
| Provider response → usage log | D20's documented numeric allow-list, max counter count, path/nesting bounds and credential scrubbing address the prior disclosure concern. Successful-response private-text fixture remains mandatory; no arbitrary raw response is permitted |
| Provider response → stored stats/report | Historical classification ambiguity R2-MIN-002; cache-only accounting integrity R2-MAJ-004. Existing authenticated report retained; no new authentication mechanism requested |
| Catalog → price | Exact route/model sourcing retained; tier interpretation defect R2-MAJ-001. Existing catalog provenance and publication pipeline were not independently recertified here |
| Session id → external routing hint | D16 already authorizes the non-secret conversation identifier. No credentials may be used as the value. No new secrecy or cross-provider cache-sharing guarantee is made |
| Usage screen → operator | Written percent/null/partial/error/keyboard requirements now exist. Their deployed behavior remains unverified |

No new security approval, rollout service or policy layer is requested. The binding Landing 1 file-protection, immutable scope and error-returning writer decisions remain background constraints; none is relaxed to make caching work.

## 6. Reachability check

| Question | Answer / evidence |
|---|---|
| Is a new agent tool being added? | No; S changes existing provider calls, accounting and the existing report. No new tool registration or default policy entry is needed |
| Does a current user surface exist? | Yes: E9's Usage route mounts `UsageScreen`; E10's authenticated `getTokenStats` supplies it |
| Is the affected Vercel route actually callable? | Yes in the current factory/catalog, not an unsupported hypothetical. E5; its marker omission is therefore in scope |
| Does the plan require execution? | Yes: fake CI, mounted screen checks, live evidence and exact-commit moved-note behavior. These are future implementation gates, not runs performed by this reviewer |
| Is this feature delivered by this review? | No. Existing source wiring was checked; new caching and display behavior is unimplemented/unverified in this task |

## 7. Questions for the founder

This is a separate interview item, **not a duplicate counted finding**. Team-lead should ask before the final correction. D3/D4/D13/D19/D20 are settled and are not reopened.

### Q1 — Which live routes must prove reuse beyond static input? (A / B)

**Context and impact.** User Story 6's independent test asks for a history-covering read **per named provider**, while the landing BDD and SC-005 expressly require that threshold for **Anthropic and OpenAI**. D9 distinguishes four main-provider runs from Gemini/Qwen checks, and D17 adds four Anthropic-format compatibility checks. Those are not interchangeable acceptance populations. The choice affects paid proof effort and what blocks landing.

| Option | Meaning |
|---|---|
| **A — Recommended: make D9/D17's distinction explicit** | Publish one route-by-route acceptance table: the four main providers prove an actual second-call cache read; Anthropic and OpenAI additionally prove the explicit beyond-full-pre-history threshold currently named by SC-005; Gemini/Qwen record field/include semantics and their specified check result; the four D17 rows prove Anthropic-connector compatibility and correctly recorded usage. Record any stronger assertions the founder actually wants rather than implying them |
| B — Strengthen the live gate explicitly | All ten routes must prove second-call reads beyond the full pre-history prefix, not only compatibility or usage-shape observation. Accept the extra proof requirement and name models/fixtures eligible to meet it |

**Recommendation: A.** It reconciles the distinct checks already recorded in D9/D17 without silently expanding or weakening their meaning. Align User Story 6, its BDD, FR-020/021 and SC-005 to the selected table. Answer `Q1 A` or `Q1 B`; this reviewer does not make that founder decision.

## 8. Verdict and final-round escalation

**REVISE**, because four verified specification/test-contract defects can produce incorrect pricing, a callable unmarked provider route, lost metric evidence, or non-reconciling persisted token splits. There is no verified CRITICAL incident here. “BLOCKING” below is the implementation-readiness disposition requested by the dispatch; it does not change the skill's CRITICAL/MAJOR verdict definitions.

### Escalation to the founder

These are open **at review time**. The spec author's final correction has not happened in this task. Any item still open after that one correction must be put to the founder for an explicit disposition; **no third grill round**.

| Finding | Why it currently blocks | If still open after final correction |
|---|---|---|
| R2-MAJ-001 | Required tier algorithm conflicts with source boundary prices | Founder decides fix versus explicit deferral/acceptance of the specific mispricing; no implied approval |
| R2-MAJ-002 | In-scope, callable explicit-cache route receives no marker | Founder decides fix versus explicit D2 scope exception; no silent exclusion |
| R2-MAJ-003 | Named metrics channel/path cannot be implemented/tested correctly from the current table | Founder decides correct metrics support versus an evidenced, explicit measurement-scope change |
| R2-MAJ-004 | Core accounting plan can miss a verified zero-split persistence defect | Founder decides correction/required boundary proof versus explicit deferral; no passing parser fixture substitutes for stored totals |

| Priority action | Owner |
|---|---|
| Interview Q1 and record the exact acceptance population | Team-lead / founder |
| Make the **one final correction**: four blocking defects, four minor clarifications, consolidate obsolete clauses; retain every closed round-1 obligation | Spec author; architect only for genuinely unresolved contract shape; backend-lead alone edits/regenerates wire specs during implementation |
| Verify correction dispositions against this report without commissioning a third grill; escalate any remaining blocker, then plan RED/GREEN/CHECK and the feature gate | Team-lead |

**Non-gating note to team-lead:** **Token-Usage Tracking (tokens only, read-time aggregation)** has dated accounting prose that no longer matches the current explicit prompt/output split. This review used E3's code and current schemas, not that old accounting prose. No side ADR edit or issue was made.

**Code correct and tested:** Not established; this was a read-only specification/source review, not an implementation test.

**Reachable by a user/agent:** Existing callable provider paths, authenticated report and Usage route verified in source; the proposed caching behavior is not certified as delivered.

skills: omnipus-shared-rules, grill-spec, gitnexus-exploring, github-cli, jev-use:jev-use, claude-api (reference only; no migration/code generation), defuddle, ux-heuristics-review, omnipus-design-system

## Evidence references

All filesystem references are absolute. Each symbol/section below was read in this task. Official pages were fetched on 2026-10-15; saved public documentation is not live-inference evidence.

| Ref | First-hand evidence |
|---|---|
| **E1** | S in full; round-1 report in full; fix diff in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/prompt-caching-grill-r2/round1-fix.diff`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/prompt-caching-interview-decisions-20261015.md`::D1–D20 and corrections; analysis, top50 and research-1 through research-5 read under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/` |
| **E2** | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/pkg/agent/window_normalize.go`::normalizeWindowMessages; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/pkg/agent/loop_run_turn.go`::prepareCallMessages, prepareLLMRequest, callProviderOnce; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/pkg/providers/anthropic/provider.go`::buildParams; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/pkg/providers/openai_responses_common/responses_common.go`::TranslateMessages; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/pkg/providers/protocoltypes/types.go`::ToolCall, UsageInfo, CacheControl |
| **E3** | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/pkg/agent/loop.go`::debitLLMUsage, estimateLLMCallCost; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/pkg/agent/turn_transcript.go`::appendIntermediateAssistantTranscript, appendAssistantTranscriptImpl; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/pkg/gateway/websocket_streamer.go`::persistTranscript; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/pkg/session/daypartition.go`::TranscriptEntry, SessionStats, ModelTokens; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/pkg/session/entry_stats.go`::accumulateEntryStats; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/pkg/session/unified_write_test.go`::TestAppendTranscriptStrict_HasSplitBoundary; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/pkg/session/usage.go`::AggregateUsage |
| **E4** | [Anthropic prompt caching/pricing](https://platform.claude.com/docs/en/build-with-claude/prompt-caching.md)::Haiku 5.5 “up to 100,000” / “over 100,000”, cached tokens count toward length; [models.dev current API](https://models.dev/api.json)::anthropic.models.claude-haiku-5-5.cost.tiers. Saved as `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/prompt-caching-grill-r2/vendor-downloads/anthropic-caching.md` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/prompt-caching-grill-r2/vendor-downloads/modelsdev-api.json`; fetch exit receipts and independent input audit alongside |
| **E5** | [Vercel automatic/manual caching](https://vercel.com/docs/ai-gateway/models-and-providers/automatic-caching); [Vercel OpenAI Chat Completions manual caching](https://vercel.com/docs/ai-gateway/sdks-and-apis/openai-chat-completions/advanced). Saved as `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/prompt-caching-grill-r2/vendor-downloads/vercel-caching.md` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/prompt-caching-grill-r2/vendor-downloads/vercel-chat-advanced.md`. Current route: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/pkg/providers/factory_provider.go`::resolveRow, CreateProviderFromConfig; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/pkg/providers/catalog/data/providers_catalog.json`::vercel |
| **E6** | [Fireworks chat reference](https://docs.fireworks.ai/api-reference/post-chatcompletions.md)::perf_metrics_in_response, PromptTokensDetails, terminal metrics; saved `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/prompt-caching-grill-r2/vendor-downloads/fireworks-chat.md`. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/pkg/providers/openai_compat/provider.go`::buildRequestBody, ChatStream; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/pkg/providers/common/common.go`::OpenAINonStreamUsage, ToUsageInfo |
| **E7** | [Alibaba context cache](https://www.alibabacloud.com/help/en/model-studio/context-cache)::OpenAI explicit examples access usage.prompt_tokens_details.cache_creation_input_tokens, Messages examples access usage.cache_creation_input_tokens; saved `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/prompt-caching-grill-r2/vendor-downloads/alibaba-caching.md`; research-2 agrees |
| **E8** | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/pkg/tools/registry.go`::PromoteTools, TickTTL, Get; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/pkg/tools/compositor.go`::FilterToolsByPolicy; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/pkg/tools/mcp_assignment.go`::mcpAssignmentAllows; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/pkg/agent/loop_search.go`::markToolsLoaded, sessionLoadedTools; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/pkg/logger/sensitive.go`::scrubFieldValue |
| **E9** | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/src/components/screens/UsageScreen.tsx`::UsageScreen, BarList, UsageSkeleton; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/src/components/screens/UsageScreen.errorstates.test.tsx`::renderUsage and error/positive-control/Retry tests; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/src/routes/_app/usage.tsx`::Route; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/design-system/catalog.json`::Tabs, Progress, Card, SegmentedControl, ErrorState; excerpt receipt `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/prompt-caching-grill-r2/design-catalog-excerpts.json`; design definition, brand guidelines and UI module rules read |
| **E10** | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/contracts/openapi.yaml`::getTokenStats; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/contracts/asyncapi.yaml`::chat frame overview; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/contracts/components/schemas/SessionStats.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/contracts/components/schemas/TokenUsageSummary.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/contracts/components/schemas/ModelTokens.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/contracts/components/schemas/AgentTokenEntry.yaml`::properties; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/pkg/gateway/rest_stats.go`::HandleTokenStats; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/docs/settings.md`::Usage paragraph and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/docs/operations/observability.md`::Session transcript / event stream |
| **E11** | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/docs/internal/architecture/AS-IS-architecture.md`::agent loop, session, tools and skills; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/docs/internal/architecture/plugin-extensibility-assessment.md`; **Tool manifest tier redesign**, sections 1.1.1 and 6, read at `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/docs/internal/architecture/ADR-071-tool-manifest-tier-redesign.md`; **Token-Usage Tracking (tokens only, read-time aggregation)** at `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/docs/internal/architecture/ADR-023-token-usage-tracking.md`; **One registry-fed catalog — model limits and provider identity from public registries** at `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/docs/internal/architecture/ADR-067-registry-fed-catalog-and-provider-identity.md`; **Unified goal / plan / subagent system — one goal core, three bindings** and **Planning & Goals — Plan entity, evidence-ladder judge, goal loops, System Agents**::2026-09-14 budget-removal notes; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/L1-AMENDMENTS-20261013.md`::binding decisions; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/docs/security.md` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/docs/tools.md`::policy/assignment/user promises |

## Evidence table

The FR rows assess the **written contract**, not implementation completion. No standalone green product-test result is claimed. Review-only document/arithmetic checks have saved logs and directly captured exit codes.

| Claim | Evidence | Certainty |
|---|---|---|
| Latest-origin round-1 fix reviewed | `git fetch origin work/prompt-caching-spec`, exit 0; `git merge --ff-only origin/work/prompt-caching-spec`, exit 0; HEAD and origin `0bb4ae257584b7ba907c97205276fd63f314da5c`. Receipts: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/prompt-caching-grill-r2/fetch.log`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/prompt-caching-grill-r2/fast-forward.log` and adjacent exit files | Verified — high |
| Correct code baseline, not a different release tip | `git diff --exit-code 8ba69a673 HEAD -- /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/pkg /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/src /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-caching-spec/contracts`, exit 0; only two spec/review additions in the complete name-status diff. Receipt `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/prompt-caching-grill-r2/code-baseline.log` | Verified — high |
| Round-1 status of all fourteen items checked against fix text | E1; closure table above, each row cites the revised heading/FR/test obligation; twelve CLOSED, two PARTLY | Verified text closure; runtime Unknown |
| FR-001 numeric accounting/correlation/log level | S::FR-001 and normative log section; E1/E8; original arbitrary-object issue closed, Fireworks acquisition correction R2-MAJ-003 | Verified written contract; execution Unknown |
| FR-002 provider/report/screen contract now explicit | S::FR-002, normative producer/UI sections; E3/E9/E10; zero-split persistence gap R2-MAJ-004 | Verified source/plan facts; consequences Inferred |
| FR-003 no new screen/tile/setting retained | S::FR-003; E9 existing route; no new surface requested | Verified written scope |
| FR-004 one surviving connector plus parity | S::FR-004 and connector correction; E2/E5 current factory/adapter; parity remains an implementation obligation | Verified written closure; tests Unknown |
| FR-005 host lifetimes/boundary bounded by D19 | S::FR-005 and dialect section; E1::D19; E11::Tool manifest tier redesign | Verified written closure; live acceptance Unknown |
| FR-006 no second Anthropic connector retained | S::FR-006; E1::D3; recommendations remain inside existing transports | Verified written scope |
| FR-007 tail survives normalizer/adapters in required final-wire test | S::FR-007 and tail correction; E2 | Verified written closure/current source; new behavior Unknown |
| FR-008 retention includes execution/isolation/revocation | S::FR-008 and non-core MCP fixture; E8 existing TTL/authorization facts | Verified written closure; enforcement Unknown |
| FR-009 skill/tool measurement lacks token units | S::FR-009 and skill-list BDD; E1::D5; R2-MIN-003 | Verified gap; measurement not run |
| FR-010 goal narrowing retained | S::FR-010 and goal scenario; E2::prepareCallMessages / prepareLLMRequest current narrowing | Verified invariant/source; future regression test Unknown |
| FR-011 every named usage shape, no double count | S::FR-011 and dataset; E6/E7; Fireworks/Alibaba paths need correction, cache-only stored split needs coverage | Verified source/plan defects; live counts Unknown |
| FR-012 host-specific session-id keys remain scoped | S::FR-012 and provider table; E1::D16; E5/E6 current routing/client | Verified written requirement; live hint acceptance Unknown |
| FR-013 Bedrock stays out | S::FR-013; E1::D2; no new Bedrock work requested | Verified written boundary |
| FR-014 catalog prices replace hand-kept rates | S::FR-014; E3::estimateLLMCallCost current rate table, E4 source cost shape, E11 catalog decision | Verified written/source facts; catalog publication not certified |
| FR-015 no missing-price fallback | S::FR-015 and mixed-price correction; E3; narrower producer-list clarification R2-MIN-002 | Verified written requirement; implementation Unknown |
| FR-016 missing versus explicit zero; one-hour exception | S::FR-016 and mixed-price correction; E1::corrected D18 | Verified written closure |
| FR-017 no new spend/token cap | S::FR-017; E1::D13; E11 dated budget-removal decisions | Verified written boundary; no cap added |
| FR-018 full-input basis fixed, equality still wrong for a source band | S::FR-018; E4; input audit `input=100000: documented_USD=0.01 inclusive_spec_USD=0.05`; R2-MAJ-001 | Verified — high |
| FR-019 fake suite has boundary/channel gaps | S::FR-019 and test plan; E3/E5/E6/E7; R2-MAJ-002/003/004 | Verified plan gaps; no fake suite executed |
| FR-020 exact changed-connector route and commit now explicit | S::FR-020, live-route correction; E1::D9/D17; E5::factory secondary endpoint selection; Q1 distinguishes live assertions | Verified text closure; live execution Unknown |
| FR-021 zero read fails cache proof | S::FR-021, SC-001/005, history negative control; Q1 asks route population only | Verified written rule; proof not run |
| FR-022 skill allowed-tools parsing stays out | S::FR-022; E1::D11 | Verified written boundary |
| FR-023 per-area logging stays out | S::FR-023; E1::D10/D15; same-change observability TODO retains existing procedure | Verified written boundary |
| FR-024 trim/model-switch misses allowed | S::FR-024 and prefix dataset; review adds no pin/freeze requirement | Verified written boundary |
| FR-025 no arbitrary provider content in log | S::FR-025 and successful-response privacy fixture; E1::D20; E8::scrubFieldValue | Verified written closure/source; no exploit or runtime pass claimed |
| FR-026 ChatGPT subscription uses tokens/no dollars | S::FR-026; E1::D14; E3/E5 actual accounting/factory seams | Verified written requirement; implementation Unknown |
| FR-027 durable write lifetimes and corrected pricing rule | S::FR-027, normative mixed-price section; E1::corrected D18; E4 published multiplier context; R2-MIN-002 carrier clarification | Verified written/source facts; pricing implementation Unknown |
| FR-028 prohibited optional modes retained | S::FR-028; E5 manual Vercel alternative avoids prohibited auto rewrite; R2-MAJ-002 | Verified written boundary and documented alternative |
| FR-029 unsupported providers not promoted | S::FR-029; E5::resolveRow unsupported-tier guard | Verified current source/written scope |
| Official reference checks saved, not inference calls | `curl` Anthropic/Fireworks/models.dev and `defuddle parse` Vercel/Alibaba, all fetch exits 0; reference files E4–E7 and adjacent fetch log/exit receipts under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/prompt-caching-grill-r2/` | Verified fetched text; runtime Unknown |
| Structural/arithmetic instrument tested | `python3 -I /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/prompt-caching-grill-r2/audit-review-inputs.py`, exit 0: `FRs=29 matrix_rows=29 BDD_scenarios=29 backrefs=29 stories=6`, missing-FR mutation detected, `$0.0204`, cache-only component sum `2000`. Receipt `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/prompt-caching-grill-r2/input-audit.log`. Initial audit failed on an incorrect catalog-shape assumption; corrected the scratch instrument, not product code | Verified document/arithmetic checks only |
| Transcript counters have no current frontend consumer in the audited scope | Positive `fetchTokenStats` control exit 0; whole-tree `rg` of transcript prompt/write field names over frontend/contracts exit 1, no matches. Receipt `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/prompt-caching-grill-r2/transcript-field-consumer-sweep.log`; existing public stats schemas read in E10 | Verified bounded search; does not authorize future wire fields without schemas |
| Jev consulted, uncertain severity returned to reviewer | `jev-use judge` receipt `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/prompt-caching-grill-r2/jev-triage.log`, exit 3 because one verdict escalated as unsure: cache-only severity confidence 0.31. Reviewer resolved MAJOR from E3's persisted output mismatch. Mixed-price minor and narrowed Fireworks verdicts accepted after source verification | Verified tool result; reviewer owns severity |
| Documentation-only impact; no runtime symbol changed | Review-only scope; GitNexus tools absent, direct source sweeps saved at `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/prompt-caching-grill-r2/source-map.log`; code baseline diff exit 0 | Verified changed scope; runtime blast-radius assessment Inferred — high |
| **Self-check** | Re-read the full staged report diff and rechecked findings against S's normative corrections, D1–D20, E1–E11 and official references; reran the controlled input audit, report-artifact audit and staged whitespace/scope checks. Final receipts: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/prompt-caching-grill-r2/final-artifact-audit.log` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/prompt-caching-grill-r2/staged-diff-check.log`, each exit 0. Required result: 4 major/4 minor/1 observation, all gate labels, twelve lenses, fourteen closure statuses, one separate Q1, FR-001–FR-029 exactly once in this table, and S byte-identical to the reviewed SHA; no product tests or live success claimed | Verified — high |
