# Adversarial Review: ADR-096 — Web search: a default, a fallback, and an honest tool

**Document reviewed**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus2-adr091/sq-race/docs/internal/architecture/ADR-096-web-search-provider-model.md`
**Companion spec read for traceability**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus2-adr091/sq-race/docs/internal/specs/web-search-provider-model-spec.md`
**Review date**: 2026-09-26
**Baseline verified against**: `07a75c104` (= this worktree's HEAD; `git diff 07a75c104 -- pkg/tools/web.go pkg/credentials/inject.go` is **empty**)
**Verdict**: **BLOCK**

## Executive Summary

The ADR's stated evidence baseline is wrong in a way that invalidates part of its own work plan: `07a75c104` **is** the credential-injection hotfix the ADR says it is waiting for, and that same commit already made the D14 warning fire. Independently of that, the boot-order contract in `pkg/gateway/gateway_boot_credentials.go` makes FR-030/D11 unsatisfiable at the location D11 specifies — the migration would record `duckduckgo` as the default on **every** install, permanently and idempotently, which is the exact catastrophe AC-14 says is forbidden. The R1–R9 resolution table re-creates the original defect through R1, and its most-cited scenario (an unusable default handing off to DuckDuckGo) is not derivable from the table. D13 and D15 do not close the "UI said tavily, DuckDuckGo ran" defect, because the Integrations write path neither re-injects credentials nor rebuilds the tool.

| Severity | Count |
|----------|-------|
| CRITICAL | 8 |
| MAJOR | 22 |
| MINOR | 12 |
| OBSERVATION | 3 |
| **Total** | **45** |

Both judgement calls the founder flagged are wrong in part: **D6**'s stated justification is refuted by D16 in the same document, and **D9** has an undefined interaction with R3 on the most common upgrade path. **D7**'s word cap is placed on the wrong field and so does not control the cost it exists to control. **D15** does not close the defect it was written to close.

---

## Findings

### CRITICAL

#### [CRIT-001] The evidence baseline is false; D14 / WP-6 / AC-13 is already-shipped work

- **Lens**: Incorrectness
- **Affected section**: "Evidence baseline" (status block), Context ¶4, D14, WP-6, AC-13, D11 "Precondition", AC-14
- **Description**: Four verifiable claims about `07a75c104` are false.

  | ADR claim | Ground truth at `07a75c104` |
  |---|---|
  | "the uncommitted edits sitting in `pkg/credentials/inject.go` and `pkg/tools/web.go`. Those edits belong to another lane" | No such edits exist. `git diff 07a75c104 -- pkg/tools/web.go pkg/credentials/inject.go` is empty. The only modified files are `pkg/tools/web_test.go` and an unrelated evidence JSON. |
  | "`InjectFromConfig` … publishes provider keys and mailbox passwords into the environment and then returns" (Context ¶4) | `07a75c104` **is** the fix: its subject is *"fix(credentials): web-search and voice keys never reached the process"*, and it makes both `InjectFromConfig` and `ResolveAll` walk one shared enumeration, `nonChannelRefsFor`, which includes all five keyed web-search providers. |
  | "A hotfix that closes this injection gap is landing separately. **This ADR assumes that hotfix has landed.**" | The hotfix *is* the declared evidence baseline. The ADR is written as if waiting for its own baseline. |
  | "At `07a75c104` the warning is inside the final `else` of `NewWebSearchTool` … With the shipped default it is unreachable." (D14) | False. `enabledButKeylessSearchProviders(opts)` (`pkg/tools/web.go:996–1014`) fires **unconditionally at lines 1029–1044, before the chain**, covering all five keyed providers, and logs `"search provider enabled but no resolved key; selection will skip it"` with provider names, refs, and a hint. The commit message says so explicitly: *"Also makes the misconfiguration warning reachable."* |

- **Impact**: WP-6 dispatches a lane to build something that exists. The likely outcome is that the lane "fixes" the warning by moving it again, regressing the two tests already sitting uncommitted in `pkg/tools/web_test.go` (`TestNewWebSearchTool_EnabledButKeyless_WarnsRegardlessOfSelection`, `TestNewWebSearchTool_EnabledWithKey_NoKeylessWarn`). Worse, D11's "This feature ships after the injection fix, or it does not ship" reads as a future gate on a landed change, so the team may hold the feature waiting for nothing. Most seriously: the ADR states its verification method as `git show HEAD:<file>`, and that method demonstrably did not produce the content of that commit for the two files where it can be checked. Citation *existence* was verified upstream; citation *content* was not, and two content claims are false. Every remaining behavioural claim in the ADR is now unverified by association.
- **Recommendation**: Re-baseline the whole document against `07a75c104` before any other edit. Specifically: delete the "uncommitted edits" sentence from the status block; rewrite Context ¶4 to say injection **is fixed** at the baseline via `nonChannelRefsFor`; delete D14, WP-6, AC-13 and replace them with a one-line "already satisfied at `07a75c104`; Exa must be added to `enabledButKeylessSearchProviders`"; rewrite D11's Precondition and AC-14 from a release gate into a statement that the precondition is met. Then re-verify every other `file::symbol` claim's *content*, not just its existence.

---

#### [CRIT-002] The boot order makes FR-030 / D11 unsatisfiable: migration records `duckduckgo` for every install

- **Lens**: Infeasibility / Incorrectness
- **Affected section**: D11 step 1 and "Precondition"; AC-14; spec FR-030; spec "Config shape" ("a one-shot migration beside `migrateCLITokenOutOfUsers`")
- **Description**: D11 requires the migration to compute the winner with the tool predicate, i.e. `APIKey()`. FR-030 makes this a MUST and explicitly forbids the alternative. `APIKey()` reads `os.Getenv(c.APIKeyRef)` (`pkg/config/config_defaults_apply.go:220–258`). The only production writer of those variables is `credentials.InjectFromConfig`. The boot order is fixed by ADR-004 and pinned by `boot_order_test.go` (`pkg/gateway/gateway_boot_credentials.go:400–412`):

  ```
  1. NewStore → Unlock
  2. LoadConfigWithStore        ← every load-time migration runs here
  3. EditionMisbuild tripwire
  4. InjectFromConfig           ← os.Setenv happens here, for the first time
  5. ResolveBundle
  6. RegisterSensitiveValues
  ```

  `migrateCLITokenOutOfUsers` — the sibling D11 says to sit beside — is called from `pkg/config/config.go:2598`, inside `loadConfigInternal`, i.e. step 2. So on the **first load of the process**, which is the upgrade load, every `APIKey()` returns `""`. The winner is therefore always the final DuckDuckGo branch, for every install, including one with a perfectly resolvable Tavily key in the vault.
- **Impact**: This is not a release-ordering risk that the hotfix mitigates — it is unconditional, and the hotfix does not help. `default_provider: duckduckgo` is written to disk for every operator, and D11 makes migration idempotent on that field's presence, so it never self-corrects. The founder's Tavily install silently becomes a DuckDuckGo install, permanently, and the feature's headline safety property ("migration must not silently change who answers") is violated on 100% of upgrades by construction. There is no repair path (see MAJ-014).
- **Recommendation**: Decide the winner from the **credential store**, not the environment. The store is already unlocked at step 1 and is already threaded into the load for exactly this purpose — `LoadConfigWithStore`'s doc comment says "threading store through to callers that need it for credential-store-backed operations during load". Use the same check `credentialRefResolves` uses (`pkg/gateway/rest_config.go:266–293`), reading `cfg.Tools.Web.<provider>.APIKeyRef` rather than the catalogue constant. Rewrite FR-030, which currently presents a false dichotomy ("the environment, not the key-name string") and forbids the only correct option. If the store is genuinely unavailable at that point, the alternative is to move the migration into `bootCredentials` as a new step between 4 and 5, mirrored in `executeReload` (`pkg/gateway/gateway_reload.go:383`) — but then it is no longer "beside `migrateCLITokenOutOfUsers`" and the ADR must say so. Either way, add the guard in CRIT-002b below.
- **CRIT-002b (same finding, separable fix)**: Whatever predicate is used, **do not migrate** while any keyed provider is `enabled: true` with a non-empty `api_key_ref` but an unresolvable key — leave `default_provider` absent and retry on the next load, exactly as `migrateCLITokenOnDisk` already does on write failure. AC-14 dismisses this with a non-sequitur ("There is no runtime detector that distinguishes 'hotfix not landed' from 'the key is really missing'"). Distinguishing them is unnecessary: in **both** cases recording `duckduckgo` is wrong for that operator, and deferring is safe because the pre-migration path still works. That state is the same one `enabledButKeylessSearchProviders` already detects.

---

#### [CRIT-003] R1 re-creates the exact defect the ADR exists to remove, and AC-2 forbids its own table

- **Lens**: Inconsistency
- **Affected section**: D4 case table R1; AC-2; D3; spec Resolution R1 and US-1 acceptance 5
- **Description**: R1 reads: "Exactly one provider is usable | **That provider.** No second try. A failure is a failure." The condition counts usability across all providers; the outcome names no role. Take the configuration `default_provider: tavily`, `fallback_provider` absent, Tavily's key unresolvable, DuckDuckGo enabled, nothing else on. Exactly one provider is usable — DuckDuckGo — so R1 fires and DuckDuckGo is called *as the provider*. That is verbatim "DuckDuckGo ran because nothing else matched", which AC-2 forbids ("DuckDuckGo is not selected by 'nothing else matched'") and D3 abolishes ("It does not run because nothing else matched").

  The same configuration also breaks the spec's flagship scenario. US-1 acceptance 5 / BDD "An unusable default does not pretend to have run" expects R6. R6's precondition is "R3 or R4 produced a usable fallback". R4 needs an explicit fallback id — absent. R3 needs "two or more providers are usable" — only DuckDuckGo is. So R3 and R4 both fail, R6 is unreachable, and the scenario the ADR advertises as its correctness proof is **not derivable from the resolution table**.
- **Impact**: An implementer working from the table ships the original bug for the single most likely failure state (the operator's chosen provider lost its key). An implementer working from the scenarios ships something the table does not describe. The two lanes disagree and both can cite the spec.
- **Recommendation**: Restate R1 in terms of roles, not a global count: "R1 — the resolved default is usable and no fallback resolves: the default only. No second try." Then make the unusable-default cases R6/R7 the *only* path when the default is not usable, and drop the "two or more providers are usable" clause from R3 (it is redundant given R3's other conditions — see MIN-002). Add an explicit statement that R1–R9 are evaluated **in order, first match wins**, and add a fixture per row that asserts *which hosts were contacted*, not just the outcome string.

---

#### [CRIT-004] The R-table has no rule for a configured-but-unusable fallback, and no evaluation order

- **Lens**: Incompleteness
- **Affected section**: D4 case table R1–R9; spec Resolution
- **Description**: Two structural holes.

  1. **Undefined case.** `default_provider: tavily` (usable), `fallback_provider: brave` (present, not `none`, currently unusable), DuckDuckGo usable. R2 needs `none` — no. R3 needs the fallback key **absent** — no. R4 needs "that provider is usable" — no. R8 needs absent — no. R1 needs exactly one usable — no. **No row matches.** The spec's save-time rule ("Fallback points at a provider that is not enabled | Rejected") does not cover this, because D4 states usability is evaluated at call time and a key can vanish, a flag can be hand-edited, and injection can fail non-fatally (`reportInjectionErrors` leaves a provider degraded rather than aborting boot).
  2. **No stated precedence.** R1, R2 and R3 can all be true at once, and R5's outcome differs from R3's for overlapping inputs. Nowhere does either document say the rows are ordered or that the first match wins.
- **Impact**: Two implementers produce two different products, and the test plan cannot catch it: SC-001 says "A fixture for each row R1–R9 resolves to the default and fallback in that row", which presumes a unique matching row.
- **Recommendation**: Add the missing row — "R4b: the fallback id is a different, known id that is not currently usable → the default only; the result lists the fallback as 'not called', with the reason" — and add a one-line precedence statement above the table. Add a total-coverage test that enumerates the cross product of (default usable/unusable/unknown) × (fallback absent/`none`/same/other-usable/other-unusable) × (DuckDuckGo usable/not) and asserts every combination maps to exactly one row.

---

#### [CRIT-005] D13 + D15 do not close the reported defect: the Integrations write path neither re-injects nor rebuilds the tool

- **Lens**: Incorrectness / Incompleteness
- **Affected section**: D13, D15, AC-12, spec "Settings screen" (Usable, Badge rows), spec save-time rule "Default points at a provider that is not usable yet | Rejected"
- **Description**: D15 asserts that one shared resolver removes the disagreement. But the resolver reads process-global mutable state (`os.Getenv`) and a snapshot baked into the tool, and the write path updates neither. Verified:

  | Step in the operator's journey | What actually happens |
  |---|---|
  | UI "Save & activate" → `PUT /integrations/providers/{id}` | `handleIntegrationProviderUpdate` → `storeCredential` → `safeUpdateConfigJSON` |
  | `storeCredential` (`pkg/gateway/rest_config.go:188–205`) | `store.Set(refName, apiKey)` and nothing else. **No `os.Setenv`.** |
  | `safeUpdateConfigJSON` → `refreshConfigAndRewireServices` (`rest_config.go:481–568`) | ends at `a.agentLoop.SwapConfig(newCfg)` + `return nil`. **No `InjectFromConfig`. No `registerSharedTools`.** |
  | The file watcher that *would* trigger a full reload | deliberately suppressed: `updateConfigJSONLocked` registers the written bytes in `selfWriteReg` precisely so the watcher does not reload |
  | `GET /integrations/providers` afterwards | `configured: true` (vault has the key), `active: <provider>` (config has the ref) |
  | `os.Getenv(ref)` in this process | still empty |
  | The live `WebSearchTool` | still the instance built at boot from the old config |

  So after a successful save the UI is truthful about config and false about the running tool — *the same class of gap as the incident*, one layer out. `07a75c104` fixed **which** refs `InjectFromConfig` enumerates, not **when** it runs.

  D13 makes this worse rather than better. Switching the badge to `APIKey()` means the row for a key the operator just saved successfully reads "key not reaching search" until a restart — a confusing false negative replacing a false positive. And the spec's save-time rule "Default points at a provider that is not usable yet → Rejected" then makes it **impossible to set that provider as the default through the UI at all**, because `APIKey()` is empty at save time and stays empty.

  (The separate credential endpoint in `pkg/gateway/rest_settings.go:193–216` *does* call `triggerReloadAndWaitOutcome` for exactly this reason, with a comment citing "UAT batch3 finding #6". The Integrations path does not.)
- **Impact**: The primary user journey the feature exists to enable — "make Tavily my default from Settings" — fails on a fresh install. The headline defect is not closed. AC-12 as written would pass a unit test and fail a real operator.
- **Recommendation**: Add a decision (D18) that the Integrations PUT must re-inject and re-wire: call `credentials.InjectFromConfig` and then the tool-rewire path (`ReloadProviderAndConfig`, as `gateway_reload.go:540` does), or route the write through `triggerReloadAndWaitOutcome` the way the credential endpoint already does. Add an AC: "after a single `PUT` that stores a key and sets a default, with no restart, a search calls that provider, and the row reads ready." Add `pkg/gateway/rest_config.go::refreshConfigAndRewireServices` and `storeCredential` to Affected components — both are missing. Until that lands, the save-time "not usable yet → reject" rule must not ship.

---

#### [CRIT-006] "Checked at call time" is unimplementable against the described construction

- **Lens**: Infeasibility
- **Affected section**: D4 ("Checked at **call** time. A key can appear after unlock."), D7 ("Execution still re-checks usability at call time (D4)"), R9, D5
- **Description**: The entire R1–R9 table, D5's usable-id check and D7's call-time re-check all depend on the tool being able to evaluate usability when `Execute` runs. It cannot. `registerCoreTools` (`pkg/agent/loop_wire.go:551–584`) evaluates `APIKey()` **once, at construction**, and passes the resolved plaintexts by value:

  ```go
  BraveAPIKeys:      braveKeys(rw.cfg.Tools.Web.Brave.APIKey()),
  GLMSearchAPIKey:   rw.cfg.Tools.Web.GLMSearch.APIKey(),
  ```

  `WebSearchToolOptions` carries no `*config.Config`, no accessor and no closure, and `WebSearchTool` holds only `{BaseTool; provider SearchProvider; maxResults int}`. There is no live handle to re-check anything against. The ADR says the *capability set* is a snapshot and then says usability is not — but both read the same baked values.
- **Impact**: An implementer either (a) silently reduces call-time checks to build-time checks, which makes R6/R7/R9 dead code and re-introduces stale-key behaviour, or (b) discovers mid-implementation that the tool needs a config handle, which is a change to `WebSearchToolOptions`, `WebSearchTool`, `NewWebSearchTool` and `registerCoreTools` that no work package owns and no component list mentions.
- **Recommendation**: State the mechanism explicitly in D4 and add it to Affected components and WP-2: the tool must hold a resolver (for example `usableProviders func() []ProviderID`, or a `*config.Config` plus the `APIKey()` methods) rather than baked key strings. Note the knock-on: the resolved plaintext currently lives in the tool instance, so a resolver changes where the secret is read and must not widen its exposure. Add a test that a key appearing after the tool was built makes that provider callable without re-registration — that is the assertion D4 actually claims.

---

#### [CRIT-007] D16's chat card requirement rests on a false claim about which tool name is registered

- **Lens**: Incorrectness
- **Affected section**: D16 ("The live tool name is `search_web`; the card also matches the legacy name `web_search`"); spec "Symbols involved" ("Both names are registered to this card"); spec Assumptions; test 27
- **Description**: The statement is inverted. The live tool's `Name()` returns `"search_web"` (`pkg/tools/web.go:1194–1195`). The only production registration in the SPA is `toolName: 'web_search'` (`src/components/chat/tools/WebSearchResult.tsx:172`), and the visibility gate is `shouldRenderToolCall('web_search', …)` (line 78). A repository-wide search for a production `toolName: 'search_web'` returns **only two test files**. `src/components/chat/OmnipusRuntimeProvider.tsx:292–293` documents the intended two registrations in a comment, but only one component is mounted. So the card is wired to the *legacy* alias and not to the tool that actually runs.
- **Impact**: Every user-visible half of D16 — "the chat card reads those facts from the result", "it does not hardcode a provider name", the hop line, test 27 — targets a component that does not render for `search_web`. The deliverable most likely to be demoed ("the operator can see who answered") would be built, tested at the fixture level, and invisible in the product. Note also that `WebSearchResult.tsx:10–14` already declares `args.provider` and never renders it, so the UI cannot show the provider today even when it does fire.
- **Recommendation**: Correct D16's sentence. Add an explicit requirement that `search_web` gains a production tool-UI registration, and add it to WP-2 or a new WP with `src/components/chat/OmnipusRuntimeProvider.tsx` in Affected components. Replace test 27 with a test that asserts the card renders for a tool call named `search_web` (the name the backend emits), not only that the parser handles a text fixture.

---

#### [CRIT-008] D5 hands the agent an uncontrolled spend surface, with no ceiling, no policy hook and no rate limit

- **Lens**: Insecurity (Denial of Service / cost amplification)
- **Affected section**: D5, D7, D12, "Explicit non-goals" (no operator ceiling), spec Follow-up ("Per-agent default and fallback | Issue #47. Not this spec")
- **Description**: The ADR gives the model two new powers and no controls over either: choose **which third party** receives the query and spends the operator's key (D5), and choose **how hard** that provider searches (D12). D12 has no operator ceiling — an agent sending `depth: high` overrides an operator's `basic` or even `ultra-fast` and sends Tavily `advanced`, which the ADR itself names as the most expensive setting. There is no per-turn call cap, no per-agent provider restriction, no spend limit, and no audit of provider choice beyond free text in the tool result. `search_web` is `allow` by default (`defaultToolPoliciesGeneral`) and is granted to the built-in roles.
- **Impact**: A prompt-injected or merely looping agent can route every search to the most expensive configured provider at maximum depth. The operator's only lever — the default — is the one the agent bypasses. This is a new surface created by this ADR: today the provider and the depth are both fixed by config, so neither is reachable from a prompt.
- **Recommendation**: Add a decision: `depth` is capped by the operator's configured value (the agent may ask for less, never more), and `provider` is honoured only within an operator-visible allow-set. Both are config, both default to today's behaviour. At minimum, state a per-turn search-call cap and require a structured audit record per search call naming the resolved provider and depth. If the founder wants the agent to be able to escalate, that should be an explicit, recorded decision rather than a silent consequence of D5 + D12.

---

### MAJOR

#### [MAJ-001] D6's justification is refuted by D16 in the same document

- **Lens**: Incorrectness
- **Affected section**: D6 ¶2 ("Hopping to DuckDuckGo would return a different product and **hide the failure**. That hide-and-substitute is the defect this feature exists to stop"); Alternatives ("Let an agent-chosen provider fall back … Rejected. See D6.")
- **Description**: D6's load-bearing sentence is that a hop hides the failure. D16 makes that impossible: a hop "also includes the default's failure", names the provider that answered and its role, and the spec fixes the exact text (`Search provider: duckduckgo (fallback)` / `Note: tavily (default) failed …`). Under this ADR a hop cannot hide anything. The Tavily incident was not "substitution" — it was *silent* substitution with a *lying* error message, and D16 removes both properties. The Alternatives entry then rejects the hop by pointing at D6, so the rejection is circular: D6's reason is a property D16 has already eliminated.
- **Impact**: The strongest argument for D6 does not survive contact with D16, which means D6 is currently unjustified — and D6 is a behaviour change with wide blast radius (see MAJ-002). A reviewer or implementer who notices this has no stated reason to keep it.
- **Recommendation**: Either re-argue D6 on a ground D16 does not neutralise, or reverse it. The defensible narrow version is capability-based: hard-fail only when the call **used** a capability specific to the named provider (a site filter, `depth`, or Perplexity's prose), and hop otherwise with D16's honest report. Record the real trade-off — one wasted model turn versus a user-visible failure — instead of an appeal to the incident.

#### [MAJ-002] Naming the operator's own default silently disables the operator's fallback

- **Lens**: Incorrectness
- **Affected section**: D6 ¶4 ("Naming a provider, including naming the fallback itself, is one try")
- **Description**: For an identical request — same provider, same query, same network failure — behaviour differs depending on whether the model filled in an argument that carried no information. `provider: tavily` when Tavily is already the default disables the operator's configured fallback; omitting it does not. Models routinely populate optional enum arguments gratuitously, so this is not a rare path.
- **Impact**: Across the product, an agent can unilaterally turn a resilient search into a single point of failure by filling in a redundant argument, and the operator has no way to prevent it. The ADR states the rule but never justifies why naming the default should override the operator's spare.
- **Recommendation**: Exempt the case where the named provider equals the resolved default: treat it identically to omitting `provider`. If D6 survives MAJ-001 in its capability-based form, this falls out automatically.

#### [MAJ-003] D9 × R3 is undefined on the most common upgrade configuration

- **Lens**: Incompleteness
- **Affected section**: D9 ("If the provider that would run does not honour a list the agent set, the call is refused before any request"); D4 R3
- **Description**: "The provider that would run" is ambiguous when two providers have roles and only one honours the filter. Configuration: default Tavily (honours filters), fallback resolved to DuckDuckGo (does not) — which is precisely what R3 produces automatically. The agent sets `include_domains`. Tavily fails with a network error. Three behaviours are all consistent with the text: hop and silently drop the filter (the lie D9 forbids); refuse the hop and return a compound error; or pre-refuse the whole call because the fallback cannot honour it. The ADR states none of them, and D17 classifies a D9 refusal as `rejected` (no hop) without saying whether the refusal can occur *mid-call*.
- **Impact**: Undefined behaviour on the default post-upgrade shape, in the interaction between the two decisions the founder singled out.
- **Recommendation**: State it in D9: when the agent set a capability argument and did not name a provider, the fallback is only eligible if it also honours that capability; otherwise the hop is skipped and the result reports the default's failure plus "fallback not eligible: cannot honour include_domains". Add a BDD scenario and a test row.

#### [MAJ-004] D9 rejects the capability-directed alternative by mis-citing D6

- **Lens**: Incorrectness
- **Affected section**: D9 ("Do not hop to a provider that could honour the filter either: that would be a silent change of provider (D6)")
- **Description**: D6 governs providers the **agent named**. When the agent did not name one, choosing a provider is not a "silent change" — it is exactly what D4 and R3 do on every call. The cross-reference does not apply, so the only alternative to refusal is dismissed on a false premise. The alternative itself is reasonable and never considered: when the agent sets a site filter and names no provider, resolve to a usable provider that honours it, and say so in the result per D16.
- **Impact**: A strictly better behaviour (answer the question, honestly) is excluded by a mis-citation. The Alternatives section has no entry for it.
- **Recommendation**: Remove the D6 citation. Either adopt capability-directed resolution for the no-`provider` case, or reject it on its own merits and record why in Alternatives.

#### [MAJ-005] D9 treats `exclude_domains` as a hard constraint when it is a preference

- **Lens**: Incorrectness
- **Affected section**: D9 ("Why refuse, not warn-and-continue. … Returning the open web with a note still returns the wrong pages")
- **Description**: The justification holds for `include_domains`, where the caller has restricted the answer to a set of sites and anything else is off-target. It does not hold for `exclude_domains`, where the caller is removing noise. Refusing the entire search because the provider cannot exclude three spam domains returns **zero** pages instead of mostly-right pages. D9 applies one rule to both and argues it only for the first.
- **Impact**: A common, low-stakes use of `exclude_domains` turns every search into a hard failure on Brave, Baidu, SearXNG and DuckDuckGo — including every new install, where DuckDuckGo is the only provider.
- **Recommendation**: Split the rule. `include_domains` on a provider that cannot honour it: refuse (D9 as written). `exclude_domains`: proceed and state in the result that the exclusion was not applied and by which provider — D16 already carries a "Note:" line for exactly this shape. If the founder prefers symmetry, record that choice explicitly.

#### [MAJ-006] D7 offers arguments that are guaranteed to fail in common configurations

- **Lens**: Incorrectness
- **Affected section**: D7 (`depth` / domain arguments offered "if **at least one** usable provider honours it") vs D9 (refusal based on the provider that **would run**)
- **Description**: With Tavily and Brave both usable and Brave as the operator's default, the schema advertises `include_domains` and every use of it refuses. The spec acknowledges this and accepts it ("The argument is offered because **some** usable provider honours it. It is not a promise that every usable provider does").
- **Impact**: The model is invited by the schema to use an argument that cannot work, burns a turn discovering it, and — since the schema is unchanged next turn — is invited again. A permanent per-install turn tax, in a feature whose stated purpose is to stop the tool doing something other than what was asked.
- **Recommendation**: Gate the argument on the **resolved default's** capabilities, not on "any usable provider", and let the refusal message name providers the agent can pass explicitly. Alternatively, keep the current gating but require the argument's own description to name which providers honour it, and add a test that the named set matches the refusal logic.

#### [MAJ-007] D7's 80-word cap is placed on the wrong field and does not control the cost it exists to control

- **Lens**: Incorrectness / Infeasibility
- **Affected section**: D7 ("This text is sent to the model on every turn. A long description is a permanent cost. Cap: the description is at most 80 words. Each argument description is one short sentence."); AC-6; test 33
- **Description**: What is actually sent every turn is the whole tool definition: name, description, and a JSON Schema with up to seven properties, each with its own description, plus an eight-value enum for `provider`. The cap binds only `Description`. "Each argument description is one short sentence" is not a bound — seven of them are uncapped in aggregate, and on today's tool the argument descriptions already outweigh the description (`Parameters()` at `pkg/tools/web.go:1205–1230`). So the majority of the per-turn bytes sit outside the cap.

  Two further problems. The cap is expressed in **words** while the cost is billed in **tokens**, which is the wrong unit for the stated purpose. And the cap is asserted only where it cannot fail: AC-6 does not say for which configuration, and test 33 measures it for the DuckDuckGo-only case, which is trivially short. The eight-provider case — the one that could breach it — is untested, and no behaviour is defined for overflow (truncate? drop the capability markers? fail the build?).
- **Impact**: The one decision written specifically to control per-turn cost does not measure or bound per-turn cost. Nobody will notice until a prompt-size regression.
- **Recommendation**: Cap the **rendered tool definition** (description + parameters, serialised) in tokens, measure it in a test for the maximal configuration (all eight usable, all capabilities present), and state what happens on overflow. Keep the 80-word guidance as prose style, not as the control.

#### [MAJ-008] D7 analyses the wrong cost: prompt caching is never mentioned, and the staleness it accepts is already solved

- **Lens**: Incompleteness
- **Affected section**: D7 final paragraph; "What this ADR could not establish" ("Whether `registerCoreTools` runs again after the credential store unlocks | Not traced")
- **Description**: Two omissions.

  1. **Caching.** The dominant token economics of a per-turn tool definition is not its length but whether it sits in a stable, cacheable prefix. A definition that is constant within a process caches fine; one that is **rebuilt mid-session** invalidates the cached prefix for the whole conversation, which costs far more than 80 words ever could. D7 makes the definition config-dependent and then discusses only word count. Caching is not mentioned anywhere in either document.
  2. **The untraced gap is two greps deep, and the answer changes the decision.** `registerCoreTools` is called only from `registerSharedTools` (`pkg/agent/loop_wire.go:354`), which has three production call sites: boot (`loop_construct.go:387`), `AgentLoop.ReloadProviderAndConfig` (`loop_config.go:77`, called from `gateway_reload.go:540`), and the agent-upsert path (`registry.go:750`). So a full reload **does** rebuild the definition — meaning D7's "accepted staleness" is narrower than stated, and the staleness that does remain is the Integrations-PUT path of CRIT-005, which is a defect rather than a tolerable limit.
- **Impact**: The cost argument that motivates D7's complexity is unexamined in its most expensive dimension, and a gap the ADR declares unknowable is cheaply knowable and points at a different decision.
- **Recommendation**: Add a sentence to D7 on cache behaviour: the definition must be stable for the lifetime of an agent instance, and any future rebuild-on-unlock must be weighed against prefix invalidation. Replace the "not traced" row with the traced answer and re-derive the staleness posture from it.

#### [MAJ-009] D7 is a second enforcement layer; a static schema was never considered

- **Lens**: Overcomplexity
- **Affected section**: D7; Alternatives (no entry for D7)
- **Description**: D5 and D9 already enforce, at call time with an explanatory refusal, everything D7's dynamic schema enforces. D7's only added value is saving one wasted turn when the model guesses. Its costs are a new snapshot concept, a documented staleness bug, a word cap that needs its own test, a divergence between the catalogue instance and the live instance that needs another test, and per-turn tokens forever. `GeneralBuiltinMetadata` construction makes this concrete: it passes exactly one field (`DuckDuckGoEnabled: true`) and has **no config in scope at all** (`pkg/tools/general_builtin_catalog.go:94–105`), yet it is what `GET /api/v1/tools` serves and what `buildKnownBuiltinToolNames` walks — and the comment chain there warns that a tool missing from that catalogue "ships denied-by-default on every install". A provider-dependent description therefore means the API and the agent are shown different text by construction.
- **Impact**: The largest piece of new machinery in the ADR, justified by a cost it does not measure (MAJ-007), duplicating enforcement that exists anyway.
- **Recommendation**: Add an Alternatives entry for "static schema with the full provider enum, plus D5's refusal" and argue against it explicitly, or adopt it. If D7 stays, keep the dynamic **enum** (cheap, high value) and drop the dynamic **description** (expensive, low value) — that keeps "a model that honours the schema cannot name a provider that is off" while removing the word cap, the catalogue divergence test, and most of the token delta.

#### [MAJ-010] R3's entire automatic-fallback mechanism serves only hand-edited config files

- **Lens**: Overcomplexity
- **Affected section**: D4 R3 and the "Automatic fallback" label; AC-3; spec `fallback_automatic`, `fallback_ignored_reason`, tests 2 and 23
- **Description**: An absent `fallback_provider` is the trigger for R3. But migration writes `none` for every existing install (D11 step 3), and a new install ships `none` (D4). D11 explicitly says that later removing only the fallback key is R3 rather than a second migration — so the **only** way to reach R3 is a hand-edited file. Serving that state: R3 itself, the R3 save rule, the "Automatic fallback" UI label, the `fallback_automatic` contract field, AC-3's automatic clause, and tests 2 and 23.
- **Impact**: A substantial mechanism, a contract field and two tests exist for a state the product never produces. R3 is also the source of CRIT-003's broken count and MAJ-003's undefined interaction — removing it simplifies the R-table and closes both.
- **Recommendation**: Delete R3 and `fallback_automatic`. Treat an absent fallback as `none`. If the founder wants a keyless spare attached by default, do it honestly in migration for installs where DuckDuckGo is usable and the default is not DuckDuckGo — a visible, one-time, logged decision rather than an implicit rule with a UI label.

#### [MAJ-011] D11 row (d) applies a rule derived from row (c) where its premise does not hold

- **Lens**: Incorrectness
- **Affected section**: D11 table row (d); Consequences → Positive ("an existing Tavily install does not get a surprise cheaper (or more expensive) depth")
- **Description**: The "preserve what the code sent" rule is correct for row (c), where Tavily is genuinely running at `advanced` and changing it would alter a live bill. Row (d) is the opposite: Tavily is **not** running, DuckDuckGo is, and search currently costs nothing. Migration writing `advanced` moves that install from free to Tavily's most expensive depth with no notice. The ADR categorises this as "**This cost change belongs to the hotfix**", which is an attribution, not a mitigation — and the Positive section's claim is simply false for row (d), which is explicitly the founder's own instance.
- **Impact**: The install the founder cares most about gets the largest possible cost increase, defended by a rule whose premise (a bill to preserve) does not apply to it.
- **Recommendation**: Write `basic` for any Tavily object that was **not** the live provider before migration, and `advanced` only where Tavily actually ran. Both are knowable from the same predicate the migration already computes. Correct the Positive section, and log the depth chosen with the reason.

#### [MAJ-012] Migration "follows the tool" cements a UI bug as operator intent

- **Lens**: Incorrectness
- **Affected section**: D11 ("If Tavily's key name is set but Tavily is switched **off**, the tool today skips it and DuckDuckGo runs. Migration follows the tool: default stays `duckduckgo`.")
- **Description**: `applySearchIntegration` (`pkg/gateway/rest_integrations_auth.go:552–600`) **never sets a keyed provider's `enabled: true`** — it writes `api_key_ref` and toggles only `searxng.enabled`. Defaults ship all five keyed providers `Enabled: false`. So "ref set, enabled false" is precisely the state the UI's "Set active" button produces for Tavily, Brave, Perplexity, GLM and Baidu. The operator pressed the button, the UI said "Active", and the config says disabled.
- **Impact**: For every operator who configured a keyed provider through the UI, migration permanently records `duckduckgo` — and presents that as honouring their configuration. Combined with CRIT-002 this is doubly wrong. It also means the ADR's root-cause story is incomplete: the incident had a third cause (activation never enabling the provider) that the Context section does not name, so the hotfix alone would not have fixed a UI-configured Tavily.
- **Recommendation**: Add the third cause to Context. In migration, treat "enabled false, ref set, key resolves" as the operator having chosen that provider: set `default_provider` to it, switch it on, and log that the flag was corrected — the same shape D11 already uses for the final-DuckDuckGo branch. D4's "Setting a default switches that provider on" fixes this going forward; migration must fix it going backward.

#### [MAJ-013] No single source of truth for provider ids; Exa adds a tenth hand-maintained list

- **Lens**: Overcomplexity / Inconsistency
- **Affected section**: D2, D15, D4 (catalogue ids), Affected components
- **Description**: D15's principle is "Two lists that can disagree are the bug". It is applied only to the two *selection* lists. The *id enumeration* is duplicated across at least: `integrationCatalogue`, `searchRefKeyByID` (which omits `duckduckgo` and `searxng`), `activeSearchProviderID`'s switch, `nonChannelRefsFor` in `inject.go`, `NewWebSearchTool`'s builder, `enabledButKeylessSearchProviders`, `defaultToolsConfig`, `WebToolsConfig`'s fields, and the contract's id documentation (a free-form string, **not** an enum, in `contracts/components/schemas/IntegrationProvider.yaml`). There is no shared constant — the gateway catalogue's own comment says it "mirrors the providers wired in pkg/tools/web.go and pkg/voice". The commit this ADR is baselined on exists *because two of those lists drifted*.
- **Impact**: Exa must be added in roughly ten places plus the new resolver's table and D7's "good for" table. Miss one and Exa reproduces the Tavily failure — which spec test 29 tries to prevent for exactly one of the ten. The ADR adds a provider and a list without applying its own anti-drift principle.
- **Recommendation**: Make WP-1 deliver one `searchProviderCatalogue` — id, config-section name, credential ref, keyed/keyless, capabilities — and require every consumer to derive from it, including the contract's `id` enum. Add an AC: adding a provider to the catalogue and nothing else makes it storable, injectable, selectable, warnable and listable, proven by a test that walks the catalogue rather than naming ids.

#### [MAJ-014] No migration marker and no repair path; the config version cannot express one

- **Lens**: Incompleteness
- **Affected section**: D11 ("Runs once, on load, when `default_provider` is absent. Does not run again once `default_provider` is present")
- **Description**: The migration keys off the presence of a data field, so no later corrective migration can tell whether it ran, or with what predicate. That is a one-way door, and the escape hatch does not exist: `CurrentVersion` has been `1` forever, the load switch accepts **only** `CurrentVersion` and hard-fails everything else (`pkg/config/config.go:2450–2463`), and there is no `case 0` branch — the comment states a config predating the current schema "has no migration path". Combined with CRIT-002 and MAJ-012, both of which write a wrong-but-valid value, there is no way to find or fix the affected installs.
- **Impact**: If the migration ships with either defect, the damage is permanent and undetectable at the population level.
- **Recommendation**: Write a marker the migration can key off (for example `tools.web.roles_migrated_at`, or a nested `migrations` object) so a corrective pass is possible, and say in D11 that the marker exists for that reason. Note in the ADR that `Version` is unavailable for this and why.

#### [MAJ-015] `prefer_native: true` is the shipped default and hides `search_web` entirely

- **Lens**: Incompleteness / Incorrectness
- **Affected section**: Consequences → Neutral ("native model search (`tools.web.prefer_native`) are unchanged"); AC-2
- **Description**: `defaultToolsConfig` ships `PreferNative: true` (`pkg/config/defaults.go`), and its own doc comment says that when the active LLM supports native search "the client-side web_search tool is **hidden** to avoid duplicate search surfaces, and the provider's built-in search is used instead". So on a new install with a native-search-capable model, `search_web` does not exist and none of this feature is reachable — while the Settings screen would still display "Default: DuckDuckGo".
- **Impact**: AC-2 ("A new install calls DuckDuckGo and nobody else") is false for those installs. More to the point, this is the single largest "who actually searches" switch in the product, and the ADR — whose entire purpose is making that question answerable honestly — files it under Neutral. The redesigned screen would confidently name a provider that is not being used.
- **Recommendation**: Address it in D13: when `prefer_native` is in effect for the active model, the search group must say so and must not present a provider as the one that answers. Add an AC. Restate AC-2 with the `prefer_native` condition made explicit.

#### [MAJ-016] No latency budget for the hop

- **Lens**: Incompleteness
- **Affected section**: D17 (one extra try), D4 R3/R4, "Integration boundaries"
- **Description**: Neither document states a timeout for a search, a budget for the fallback attempt, or that the pair fits inside the turn's deadline. The existing values are `searchTimeout = 10s` (Brave, Tavily, DuckDuckGo, GLM, the final DuckDuckGo) and `perplexityTimeout = 30s` (Perplexity, Baidu), and the key-pool loop retries **per key** inside a provider before the hop even starts. A Perplexity default plus a DuckDuckGo fallback is a 40-second worst case inside one tool call, more with multiple keys.
- **Impact**: If the agent's tool-call budget is shorter than the combined worst case, the fallback never completes and the feature's headline behaviour is dead on the slowest providers — silently, and only under the failure conditions nobody tests locally.
- **Recommendation**: State a total budget for one `search_web` call, and derive the per-attempt deadline from it (for example: the fallback gets the remaining budget, and is skipped with a "not called: no time budget remaining" reason if it cannot fit). Add that reason to D16's vocabulary and an edge case to the spec.

#### [MAJ-017] No observability: the most likely failure of the default install is indistinguishable from "no answer"

- **Lens**: Inoperability
- **Affected section**: D17 (DuckDuckGo 200-with-no-anchors stays `empty`); "What this ADR could not establish"; Consequences
- **Description**: `extractResults` returns `"No results found or extraction failed. Query: %s"` with a **nil error** when no anchors match, and `DuckDuckGoSearchProvider.Search` never checks the HTTP status. D17 keeps 200-with-no-anchors as `empty`, declines to fingerprint a block page, and adds no signal of any kind. On a new install DuckDuckGo is the only provider, so the single most likely real-world failure — DuckDuckGo blocking a datacentre IP — renders as a successful empty search, forever, with no warning, no counter and no alert.

  More broadly the ADR specifies zero monitoring for everything it adds: hop rate, refusal rate (D6 and D9 are new, agent-visible failure modes), migration write failures, "key not reaching search". There is no feature flag for the resolver change and no rollback for a one-way data write.
- **Impact**: On-call has nothing at 3 a.m. except the D14 warning, which fires at construction and says nothing about calls. An operator whose searches silently return nothing has no way to tell that from a quiet web.
- **Recommendation**: Require a structured log per search call carrying resolved default, resolved fallback, provider that answered, failure class, and hop/refusal/skip — with the existing correlation fields. Add a counter or warning for consecutive `empty` results from DuckDuckGo, which is the cheap version of the fingerprint the ADR declines to build. State the rollback posture for D15 and the resolver.

#### [MAJ-018] Provider error messages flow verbatim into the tool result, and Tavily's key travels in the request body

- **Lens**: Insecurity (Information Disclosure)
- **Affected section**: D16 ("A hop also includes the default's failure"); spec result formats ("the provider, the class, and the message")
- **Description**: The design mandates that upstream provider messages be embedded in a string returned to **both** the model and the user, and thence into transcripts and logs. `TavilySearchProvider.Search` sends `"api_key": apiKey` in the JSON body. An upstream 4xx from a provider, proxy or WAF that echoes the submitted payload therefore has a path to the model's context and the transcript. Neither document requires redacting provider messages, bounding their length, or routing them through the existing mechanism — `cfg.RegisterSensitiveValues`, which boot step 6 already populates with every resolved plaintext.
- **Impact**: A credible secret-leak path created by a new requirement, with an existing mitigation that the ADR does not invoke.
- **Recommendation**: Require every provider message included in a tool result or log to pass through the registered-sensitive-values redactor and to be truncated to a stated length. Add an AC and a test with a fake provider that echoes the request body.

#### [MAJ-019] Domain-list validation is undefined

- **Lens**: Ambiguity / Insecurity
- **Affected section**: D9 ("A value that is not a hostname is a rejection", at most 10)
- **Description**: "Hostname" is never defined. No maximum length per entry, no IDN or punycode rule, no statement on wildcards (`*.example.com`), ports, userinfo, paths, trailing dots, uppercase, or duplicates — and no rule for a domain appearing in both lists (the spec says "excluded", the ADR is silent). Ten entries of unbounded length is a trivially large agent-controlled payload. The repository already has a validator for this exact problem: `gateway.ResolveVideoEmbedHosts`, which is documented as "the single validator both surfaces use" and drops syntactically invalid hostnames with a warning.
- **Impact**: Two implementers write two different validators; one of them accepts `*.evil.com/path?x=1`. Boundary tests cannot be written from the text.
- **Recommendation**: Define hostname by reference to the existing validator, state per-entry and total length caps, decide the wildcard question explicitly, and state the both-lists precedence in the ADR. Add boundary cases to the test plan.

#### [MAJ-020] D8's temperature rationale overclaims, and AC-7's assertion is fragile and near-vacuous

- **Lens**: Incorrectness / Infeasibility
- **Affected section**: D8 Temperature; Consequences → Positive ("the same query stops varying because a default temperature was left implicit"); AC-7; spec test 35
- **Description**: The ADR's correction of the brief is right on the facts — the committed payload sends no `temperature` (verified; the Perplexity payload is `model`, `messages`, `max_tokens`, plus `search_recency_filter`). But the benefit claimed is overstated: the dominant source of run-to-run variation for a live web search is the retrieved set, not the sampler. Setting `temperature: 0` makes the prose stable *given identical retrieved context*, which is not the common case. The Positive section states the stronger claim as fact.

  Separately, AC-7's "does not send `0.2`" and the spec scenario's "the request JSON does not contain `0.2`" are a substring assertion over a serialised body. It passes vacuously today, would break if any unrelated numeric field ever contained that substring, and tests the absence of a value no code has ever written. It encodes a misreading of the brief as a permanent test.
- **Recommendation**: Soften the Positive claim to what is true ("removes sampler variance; retrieval variance remains"). Replace the negative assertion with a positive one: the decoded request body has `temperature` present and equal to `0`. Keep the unresolved-vendor-default note in the gaps table.

#### [MAJ-021] SearXNG's unbounded body and SSRF-bypassing client are touched but not flagged

- **Lens**: Insecurity (DoS / SSRF)
- **Affected section**: D10, WP-3 ("SearXNG through the existing ingest bound"), D17 ingest-bound row
- **Description**: SearXNG is the one provider that decodes straight off `resp.Body` with `json.NewDecoder` and **no ingest bound**, while every other keyed provider goes through `readIngestBounded`. It also constructs its own client — `&http.Client{Timeout: 10 * time.Second}` — when no SSRF checker is present, bypassing the SSRF-safe path `makeSearchClient` provides via `ssrf.SafeClient()`. The base URL is operator-controlled and the request is server-side. The ADR puts the ingest bound in scope as a tidy-up and does not mention either property as a security fix, and no AC covers it.
- **Impact**: An unbounded response can exhaust memory; an operator-set base URL on the non-SSRF path is a server-side request to an arbitrary host. Framed as housekeeping, it is likely to be dropped as low priority in a descoped provider.
- **Recommendation**: Label it as the security fix it is in D10 or a new decision, require SearXNG to use `makeSearchClient` like every other provider, and add an AC with a test that an oversized SearXNG body is refused by the bound.

#### [MAJ-022] The Settings contract and UI cannot express the save flows the spec describes

- **Lens**: Infeasibility
- **Affected section**: spec "Settings screen" and "Contract shape"; ADR D4 save rules, D13, WP-5
- **Description**: `IntegrationProviderUpdateRequest` has `additionalProperties: false` and exactly three properties: `kind`, `api_key`, `active`. The handler computes `makeActive := body.Active != nil && *body.Active`, so `active: false` is accepted and silently does nothing. The UI offers exactly two actions — "Set active" and "Save & activate" — so there is **no way to save a key without activating**, no way to deactivate, and no `base_url` field (the component's own comment says so). The spec's save-rule table assumes the operator can add a key and separately choose a default, and assumes "No fallback" is expressible.
- **Impact**: WP-5's rules describe interactions the surface cannot perform. The contract work is larger than "add `fallback`": it needs a key-without-activation path, a defined meaning for `active: false`, and the new role fields — all breaking changes under `additionalProperties: false`, all requiring `make gen-contracts` and a regenerated `pkg/gateway/inboundschemas/` copy (the runtime validator reads the embedded YAML, so an un-regenerated schema edit validates against stale rules).
- **Recommendation**: Enumerate the required request shape in the ADR: separate "store key" from "set role", define `active: false`, add `fallback`. State that `pkg/gateway/inboundschemas/` regeneration is part of the contract step. Add an AC that a key can be stored without changing any role.

---

### MINOR

| ID | Lens | Section | Finding | Recommendation |
|---|---|---|---|---|
| MIN-001 | Inconsistency | D4 R5 | "or R3 would pick the default" is unreachable: R3's own conditions already exclude the default (`and the default is not DuckDuckGo`). A dead clause, repeated verbatim in the spec. | Delete the clause. Its presence in both documents suggests the table was not walked. |
| MIN-002 | Inconsistency | D4 R3 | "two or more providers are usable" is redundant when the default is usable, and harmful when it is not — it is the clause that makes CRIT-003 reachable. | Delete it; R3's other conditions are sufficient. |
| MIN-003 | Incorrectness | D4 (`omitempty` rationale) | `VideoEmbedHosts` is cited as the precedent but uses the **opposite** technique: `*[]string` **with** `omitempty`, a pointer for exactly the three-state problem. The ADR's plan (plain string, no `omitempty`) is workable but the precedent does not support it as described. | Either cite it accurately and adopt the pointer shape, or cite `migrateCLITokenOutOfUsers`'s package doc, which states the trap directly. |
| MIN-004 | Incompleteness | D4 config keys | `WebToolsConfig` embeds `ToolConfig` with `envPrefix:"OMNIPUS_TOOLS_WEB_"` and every sibling field carries an `env:` tag. The ADR specifies JSON only — no `env` and no `yaml` decision for the two new keys. If `default_provider` is env-settable, an env value makes it non-empty and **permanently suppresses the migration** on that install. | State the `env` and `yaml` tags explicitly, and state whether an env override suppresses migration. |
| MIN-005 | Infeasibility | AC-2 | "DuckDuckGo is not selected by 'nothing else matched'" is not observable from outside. | Restate as a differential assertion: a config where the old chain would pick DuckDuckGo but `default_provider` names another usable provider calls that provider. |
| MIN-006 | Ambiguity | D4 / D13 / spec | "Usable" is defined four times with drift. D13's version drops SearXNG's trim and reads as though a base URL is an alternative to a key for *all* providers. | Define it once, name it, and reference it everywhere. |
| MIN-007 | Inconsistency | WP table | AC-1 ("Exa can be stored, injected, and chosen") is assigned to WP-3 (provider bodies), while storage is WP-1 and injection is Credentials. | Split AC-1, or reassign it to WP-1. |
| MIN-008 | Incompleteness | AC-14, D2, spec test 29 | The injection enumeration is `pkg/credentials/inject.go::nonChannelRefsFor`, and it is never named. Test 29 ("the injection enumeration the hotfix walks") points at an unnamed symbol. | Name `nonChannelRefsFor` in D2, AC-14 and Affected components. Note Exa must also join `enabledButKeylessSearchProviders`. |
| MIN-009 | Incompleteness | D2, D9 (Exa row) | Verified externally: Exa's `POST https://api.exa.ai/search` accepts `Authorization: Bearer`, and the domain fields exist as **camelCase** `includeDomains` / `excludeDomains`. So the ADR's open gap is closeable, and Exa **does** honour site filters. The camelCase/snake_case split against Tavily is a live trap — snake_case keys would be ignored and return unfiltered results. | Close the gap in the ADR, put Exa in the honours-site-filters set, and record the camelCase names in D9's mapping table so no implementer copies Tavily's casing. |
| MIN-010 | Ambiguity | D4 (`searxng` value) | `searxng` is a legal config value but an illegal save value, and no behaviour is stated for a hand-typed `default_provider: searxng` on a file that never resolved to it. | State it: treated as a known id, usable only under D4's SearXNG test; R6/R7 otherwise. |
| MIN-011 | Incorrectness | D4, spec Config shape | `migrateCLITokenOnDisk` is a JSON **map** read/patch/write via `json.MarshalIndent`, not a byte patch — whitespace, key order and number formatting are re-emitted. The ADR and spec both say "patch the bytes". | Describe the technique accurately: round-trip through `map[string]any` to preserve unmodelled keys, which is what actually avoids the `omitempty` trap. |
| MIN-012 | Incompleteness | D16 | "A success names the provider that answered" is already true for Tavily, Perplexity, GLM, Baidu, DuckDuckGo and SearXNG, which embed `(via X)` in their result header — but **Brave does not**. No work package mentions it. | Note the existing behaviour and list Brave's header as a change in WP-3. |
| MIN-013 | Incorrectness | Context ¶2 | "That list does not look at the on/off flag" is true for the five keyed providers but not for SearXNG, whose branch does check `Enabled`. | Add the exception. |

---

### Observations

| ID | Section | Observation |
|---|---|---|
| OBS-001 | D12 | **Verified, do not re-open**: Tavily's `search_depth` really does accept `basic`, `advanced`, `fast` and `ultra-fast`, with `basic` as the vendor default and `advanced` the most expensive. The founder's recollection is correct and D12's mapping is monotonic. Recording this so no lane spends time doubting it. |
| OBS-002 | D17, spec edge cases | "No shared 'provider is down' latch" is the right call under a simplicity principle, but its consequence is unanalysed: a rate-limited default means every concurrent call pays a 429 and then a fallback call, so total request volume roughly doubles across both providers at exactly the moment one is throttling. Worth one sentence in Consequences rather than a circuit breaker. |
| OBS-003 | Whole document | The ADR is unusually good at recording what it could not establish, and that habit is what makes CRIT-001 surprising: the gaps table is careful about GLM and Exa while two checkable claims about the baseline commit are wrong. Consider making "re-read the cited content, not just the symbol name" an explicit step for future ADRs — content verification is what failed here, not citation hygiene. |

---

## Structural Integrity

Reviewed as a **structured spec** (numbered decisions D1–D17, acceptance criteria AC-1–AC-14, work packages WP-1–WP-6).

| Check | Result | Notes |
|-------|--------|-------|
| Every decision has acceptance criteria | **FAIL** | D3, D15 and D16 have no AC. D15 is the decision that closes the headline defect; AC-12 tests only that the badge uses the tool's test, never that the screen and the tool agree on the same inputs. D16's entire result-text contract — which the SPA parses — has no AC. |
| Cross-references are consistent | **FAIL** | D9 cites D6 for a case D6 does not govern (MAJ-004). R5 cites an unreachable R3 outcome (MIN-001). D14 and WP-6 reference a code state that does not exist at the stated baseline (CRIT-001). |
| Scope boundaries are explicit | **PASS** | In/out is unusually clear, and the non-goals table in the spec is strong. |
| Success criteria are measurable | **FAIL** | AC-2's second sentence is unobservable (MIN-005). AC-6's 80-word cap has no stated configuration and is tested only where it cannot fail (MAJ-007). AC-7's "does not send `0.2`" is a vacuous substring assertion (MAJ-020). |
| Error/failure scenarios addressed | **PARTIAL** | D17's class table is thorough and matches the code's real key-pool behaviour. But the R-table has an uncovered case and no precedence (CRIT-004), and D9's mid-call refusal is undefined (MAJ-003). |
| Dependencies between requirements identified | **FAIL** | The dependency on the credential hotfix is stated but mis-dated (CRIT-001). The dependency on boot ordering is not identified at all and is violated (CRIT-002). The dependency on the Integrations write path re-injecting is not identified and is violated (CRIT-005). The dependency on the tool holding a live config handle is not identified (CRIT-006). |
| Every AC maps to a work package | **PASS** | AC-1–AC-14 all appear in the WP table, though AC-1's assignment is wrong (MIN-007). |
| Assumptions and constraints explicit | **PARTIAL** | The gaps table is genuinely good. It omits the three constraints that actually block the design: boot order, the Integrations write path, and the tool's baked-key construction. |

---

## Test Coverage Assessment

The companion spec's TDD plan (42 tests) is well shaped for the logic it names. The gaps are where the plan tests the design's *claims* rather than its *bindings*.

| Category | Gap | Affected |
|---|---|---|
| Resolution completeness | SC-001 presumes each row R1–R9 matches uniquely. No test enumerates the cross product of default × fallback × DuckDuckGo usability, so CRIT-003 and CRIT-004 pass the suite. | Tests 1–6, SC-001 |
| Migration predicate | Test 42 asserts the migration follows `APIKey()` — it would pass a unit test with a pre-seeded environment and still ship CRIT-002, because the defect is *when* the migration runs, not *what* it reads. No test exercises the real boot order. | Test 42, AC-14 |
| End-to-end configure flow | No test covers "PUT stores a key and sets a default → a search calls that provider, with no restart". This is the gap CRIT-005 lives in; tests 21/26/40 all stop at the payload. | Tests 20–23, 26, 40 |
| Call-time usability | No test makes a key appear *after* the tool was built and asserts the provider becomes callable — the assertion D4 actually claims (CRIT-006). | D4, D7 |
| Schema size | Test 33 measures the 80-word cap only for DuckDuckGo-only. The maximal configuration is untested, and the argument descriptions and enum are outside the cap entirely. | Test 33, AC-6 |
| Chat card | Test 27 is a text fixture for the parser. It cannot catch that the card is registered for `web_search` while the tool emits `search_web` (CRIT-007). | Test 27 |
| Negative assertions | Test 35's "does not contain `0.2`" passes vacuously. Decode the body and assert `temperature == 0`. | Test 35 |
| Timeouts | No test for the combined default+fallback budget, and none for a fallback skipped because the budget is exhausted (MAJ-016). | D17 |
| Secret redaction | No test that a provider error echoing the request body is redacted before reaching the tool result (MAJ-018). | D16 |
| Boundary values | Domain-list boundaries (11 entries, empty string, wildcard, port, path, 10 KB entry, same domain in both lists) are not enumerated (MAJ-019). | Tests 24, 25 |
| Regression | The regression table's "Brave / Tavily / Perplexity try the next key in the pool" is **correct** — the pool exists and rotates on 401/403/429/5xx. Worth noting that it rotates on 5xx too, which D17 mentions only for `auth` and `rate_limit`. | D17 |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Key concern |
|---|---|---|---|---|---|---|---|
| `search_web` tool arguments (`provider`, `depth`, domain lists) | ok | risk | **risk** | risk | **risk** | **risk** | Agent-controlled provider and depth with no ceiling, no per-agent policy and no audit record: cost amplification and unlogged spend (CRIT-008). Domain lists unvalidated (MAJ-019). |
| Tool result / error text | ok | ok | risk | **risk** | ok | ok | Provider messages embedded verbatim, with Tavily's key in the request body and no redaction requirement (MAJ-018). Refusals enumerate the operator's configured providers to the model. |
| Provider HTTP calls | ok | ok | ok | ok | risk | ok | SearXNG has no ingest bound and bypasses the SSRF-safe client (MAJ-021). No total latency budget (MAJ-016). |
| Migration (one-shot config write) | ok | **risk** | **risk** | ok | ok | ok | Silently rewrites who answers, with no marker and no repair path (CRIT-002, MAJ-012, MAJ-014). |
| Integrations `PUT` | ok | risk | ok | risk | ok | risk | Stores the secret before the config write with no rollback on config failure (orphan vault entry). Does not re-inject or re-wire, so reported state diverges from running state (CRIT-005). |
| Settings read (`GET /integrations/providers`) | ok | ok | ok | risk | ok | ok | `configured` is computed from the catalogue-constant ref name, not the config's `api_key_ref`, so it is true whenever a conventionally named vault entry exists. |
| D14 warning / logs | ok | ok | ok | ok | ok | ok | Correctly names refs, never secrets. Already shipped at the baseline. |

**Legend**: risk = identified threat not mitigated in the document; ok = adequately addressed or not applicable.

---

## Unasked Questions

1. When, exactly, does the new migration run relative to `credentials.InjectFromConfig`, and what does it read? The document assumes an ordering the boot contract does not provide (CRIT-002).
2. After an operator saves a key and a default in Settings, what makes that key live in the running process? Nothing in the write path does (CRIT-005).
3. What does the tool re-check usability *against* at call time, given that `registerCoreTools` passes resolved plaintexts by value (CRIT-006)?
4. Which rule applies when `fallback_provider` names a known provider that is not currently usable, and in what order are R1–R9 evaluated (CRIT-004)?
5. If a hop cannot hide a failure — because D16 requires it to be reported — what is left of D6's argument (MAJ-001)?
6. When the default honours a site filter and the resolved fallback does not, does the call refuse up front, hop and drop the filter, or refuse the hop (MAJ-003)?
7. Which installs will ever have an absent `fallback_provider`, given that both migration and the shipped defaults write `none` (MAJ-010)?
8. What is the total per-turn token cost of the new tool definition, and does the 80-word cap bind any material part of it (MAJ-007)?
9. What is the worst-case wall-clock time of one `search_web` call after this change, and does it fit the turn deadline (MAJ-016)?
10. On a default install where `prefer_native` hides `search_web`, what does the Settings screen say, and is it true (MAJ-015)?
11. What stops an agent from routing every search to the most expensive provider at maximum depth (CRIT-008)?
12. How would we find and repair installs whose `default_provider` was migrated wrongly (MAJ-014)?
13. Who owns the single provider-id catalogue, given that the incident this ADR is baselined on was caused by two id lists drifting (MAJ-013)?
14. Does an operator-configured Tavily that the UI marked "Active" but never switched on represent operator intent, or a bug to be preserved (MAJ-012)?

---

## Verdict Rationale

**BLOCK.**

Three of the eight critical findings are independent, verified reasons why the feature as specified cannot work: the migration runs before the environment it reads is populated (**CRIT-002**), the Settings write path neither injects credentials nor rebuilds the tool, so D13 and D15 leave the reported defect in place and make configuring a default impossible through the UI (**CRIT-005**), and the tool has no live handle to perform the call-time usability check that the whole R1–R9 table depends on (**CRIT-006**). Each of these is a binding the document did not trace; together they mean the resolver, the migration and the screen would all be built against assumptions the codebase does not satisfy.

Two more are internal soundness failures: the resolution table re-creates the original defect through **R1** and cannot derive its own flagship scenario (**CRIT-003**), and it has an uncovered case with no stated precedence (**CRIT-004**). Two are false factual premises that would send lanes at the wrong work: the evidence baseline is wrong about the commit it names, making D14/WP-6/AC-13 already-shipped work and inverting the release gate (**CRIT-001**), and D16's user-visible half targets a chat card that is not registered for the live tool name (**CRIT-007**). One is a new, uncontrolled agent-driven spend surface (**CRIT-008**).

On the two judgement calls the founder flagged: **D6** should not stand as written. Its justification — that a hop hides the failure — is refuted by D16 in the same document, and its blast radius includes the case where naming the operator's own default silently disables the operator's fallback (MAJ-001, MAJ-002). The capability-based version is defensible; the blanket version is not. **D9** is right in principle for `include_domains` and wrong in three details: it has no rule for the default-honours/fallback-does-not case that R3 creates by default (MAJ-003), it dismisses the better alternative by mis-citing D6 (MAJ-004), and it treats `exclude_domains` — a preference — as a hard constraint (MAJ-005).

On **D7**: the token concern is real but the cap is on the wrong field, so it does not control the cost, and the analysis omits prompt caching entirely, which is where the real money is (MAJ-007, MAJ-008). The dynamic *enum* earns its keep; the dynamic *description* does not, and a static schema was never considered (MAJ-009). On **D15**: no, a single resolver does not close the defect. The two lists were only one of three causes. The second — three different usability tests — is addressed by D13. The third — that the write path never makes a saved key live, and never rebuilds the tool — is untouched, and D13 makes its symptom more confusing rather than less (CRIT-005).

### Recommended Next Actions

- [ ] Re-baseline the entire document against `07a75c104`; delete D14/WP-6/AC-13 as already shipped; re-verify the *content* of every remaining citation — CRIT-001
- [ ] Change the migration predicate to the credential store and add the defer-if-unusable guard; rewrite FR-030 — CRIT-002, CRIT-002b
- [ ] Rewrite R1 in terms of roles, add the missing R4b row, state first-match-wins precedence, delete R3 — CRIT-003, CRIT-004, MAJ-010, MIN-001, MIN-002
- [ ] Add a decision requiring the Integrations `PUT` to re-inject and re-wire; do not ship the "not usable yet → reject" save rule until it does — CRIT-005, MAJ-022
- [ ] Specify the mechanism for call-time usability (resolver, not baked keys) and add it to WP-2 and Affected components — CRIT-006
- [ ] Correct D16's tool-name claim and require a production `search_web` tool-UI registration — CRIT-007
- [ ] Add an operator ceiling on `depth`, an allow-set for `provider`, and a structured audit record per search — CRIT-008, MAJ-017
- [ ] Re-argue or reverse D6 on a ground D16 does not neutralise; exempt the named-equals-default case — MAJ-001, MAJ-002
- [ ] Define the D9 × R3 interaction, remove the D6 mis-citation, and split include/exclude behaviour — MAJ-003, MAJ-004, MAJ-005, MAJ-006
- [ ] Move the size cap onto the rendered tool definition in tokens, test it at the maximal configuration, add the caching note, and add an Alternatives entry for a static schema — MAJ-007, MAJ-008, MAJ-009
- [ ] Fix D11 row (d)'s depth, make migration honour a UI-configured-but-disabled provider, and add a migration marker — MAJ-011, MAJ-012, MAJ-014
- [ ] Make WP-1 deliver a single provider catalogue every consumer derives from — MAJ-013
- [ ] Address `prefer_native`, the hop latency budget, provider-message redaction, domain validation, and SearXNG's bound and SSRF client — MAJ-015, MAJ-016, MAJ-018, MAJ-019, MAJ-021
- [ ] Add acceptance criteria for D15 and D16; fix AC-2, AC-6 and AC-7's assertions — Structural Integrity

---

**Review written to**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus2-adr091/sq-race/docs/internal/architecture/ADR-096-web-search-provider-model-review.md`

Address the findings above, then re-run:

```
/grill-spec docs/internal/architecture/ADR-096-web-search-provider-model.md
```

---

## Round-2 disposition (2026-09-26)

The corrections above were applied to `ADR-096-web-search-provider-model.md` and, where a decision change forced a requirement change, to `docs/internal/specs/web-search-provider-model-spec.md`. This section records the disposition of every finding so the audit trail lives with the findings. It does not re-grill; the ADR's own changelog carries the same list from the author's side.

| Disposition | Findings |
|---|---|
| **Applied as recommended** | CRIT-001, CRIT-003, CRIT-004, CRIT-005, CRIT-006, CRIT-007, MAJ-001, MAJ-002, MAJ-003, MAJ-004, MAJ-005, MAJ-006, MAJ-007, MAJ-008, MAJ-012, MAJ-013, MAJ-014, MAJ-015, MAJ-016, MAJ-017, MAJ-018, MAJ-019, MAJ-020, MAJ-021, MAJ-022, MIN-001, MIN-002, MIN-003, MIN-004, MIN-005, MIN-006, MIN-007, MIN-008, MIN-009, MIN-010, MIN-011, MIN-012, MIN-013, OBS-002, and the structural gaps |
| **Applied, with a different mechanism than recommended** | **CRIT-002** — the recommendation was to decide the migration winner from the credential store during load. That is not available: `pkg/config/config.go::CredentialStore`, the interface `LoadConfigWithStore` threads through, has exactly one method, `Set(name, value string) error`. It cannot read. The migration moves **after** `credentials.InjectFromConfig` instead — into `bootCredentials`, mirrored in `executeReload` — which is the finding's own stated alternative. CRIT-002b's defer guard is applied verbatim. **CRIT-008** — the depth ceiling and the per-call audit record are applied; the provider allow-set and the per-turn call cap are raised as a founder question (ADR Q2) rather than adopted, because both add operator policy surface that was not asked for |
| **Disputed, and deliberately not applied** | **MAJ-011.** The finding says migration writing `advanced` for a Tavily that was not the live provider moves that install from free to the most expensive depth. `TavilySearchProvider.Search` **hardcodes** `search_depth: "advanced"` on every call today, so once the key resolves that install pays `advanced` with or without this migration; writing `basic` would be this feature silently cheapening a search nobody asked us to cheapen. What the finding is right about is that the ADR's Positive section claimed row (d) gets no cost change, and it does — from the key starting to work. That claim is withdrawn, the depth written is logged with its reason, and Settings now shows the inherited depth. Raised as founder question Q4 |
| **Recorded, not acted on — needs a founder decision** | **MAJ-010.** R3's automatic-DuckDuckGo fallback is reachable only through a hand-edited file, so the mechanism, its UI label, its contract field and its two tests serve a state the product never produces. Deleting it would reverse a decision the founder settled and did not reopen, so it is recorded in the ADR's "Known narrow paths" and raised as Q3 rather than removed. **MAJ-009** is applied in the form the founder directed — the dynamic enum is kept, the dynamic description is dropped — rather than as a full static schema |

Four questions are now open for the founder, listed in the ADR under "Open questions for the founder": whether `prefer_native` keeps its `true` default (Q1), whether the agent's provider choice needs an allow-set and a per-turn cap (Q2), whether R3 survives (Q3), and whether a migrated Tavily keeps `advanced` depth (Q4).

No code, test, or other document was touched. No commit was made.
