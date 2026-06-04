# ADR-018: Tools-System Redesign + v0.1 UX Showstopper Resolution

**Status:** Proposed — **revised after `/grill-spec` round 1** (resolves F-01/F-02/F-03 CRITICAL + folds F-04…F-11) · **Date:** 2026-06-03
**Drivers:** Manual testing of the v0.1 non-technical-UX-hardening epic (PR #344 — **ON HOLD, must not merge to `main` until this lands**) exposed a set of showstoppers, the largest of which is a broken global Tool-Access editor. The owner gave firm architectural direction to consolidate the tools system. Everything here ships in this release.
**Supersedes (in part):** the epic's `B-1` "hide `system.*` under Advanced" construct and `§2.1` `system.*`-default-deny rail (`docs/internal/specs/nontech-ux-hardening-spec.md`).
**Builds on:** `docs/internal/specs/tool-registry-redesign-spec.md` (Rev 6) — the central-registry redesign that was **only partially implemented**.
**Handoff:** `/grill-spec` this ADR, then `/plan-spec` the chosen options.

---

## 1. Problem Understanding

Three problem clusters, all shipping together:

- **A — Tools system (centerpiece).** `GET /api/v1/tools` returns **only the 41 `system.*` tools** (`category:'system'`, `scope:'core'`); the ~20–26 general builtins (`exec`, `read_file`, `browser.*`, `web_search`, …) are registered **per-agent** (`registerSharedTools`, `loop.go:672-960`) and absent from the global registry `[FACT]`. The epic's global Security editor adopted `ToolPolicyEditor`, which separates `category==='system'` into an Advanced disclosure — so with system-only data the editor is **empty in the primary grid, double-lists the 41 tools, and offers no per-tool allow/deny** (the reported bug, reproduced). The owner's direction: **one builtin registry + one MCP registry; all tools (system, general, MCP) treated uniformly; flat defaults; purpose categories; a `dangerous` flag; and per-tool configuration.**
- **B — UX showstoppers.** Spurious restart banner on fresh install; all providers shown green; WhatsApp QR not appearing; ~2–3 min session-token expiry; profile font-size slider inert; About page (placeholder logo, wrong GitHub link, `dev` version); MCP "Add server" is a centered modal vs channels' slide-out; gateway no-auth option present; gateway hot-reload toggle present.
- **C — ClawHub.** "Browse skills" is a 501 stub, though the backend multi-provider registry already exists.

## 2. Extracted Requirements

### Functional
- **FR-A1** One central builtin registry holding **all** builtins (general + `system.*`); at most two registries total (builtin + MCP). `/api/v1/tools` returns the full builtin set + MCP tools.
- **FR-A2** Tools treated uniformly across Security (global) and agent config; no `system`-special-casing. Global `deny` overrules per-agent; a denied tool is **invisible to the agent** (not in the LLM `tools[]`) — preserve the existing `compositor.go FilterToolsByPolicy` contract `[FACT]`.
- **FR-A3** Every tool carries a **purpose category** and a boolean **`dangerous`** flag in its registry metadata + wire type.
- **FR-A4** Defaults are **flat** — every tool takes the global default policy — **except** `dangerous`-flagged tools, which default to a strict policy (Ask or Deny; see D-A3).
- **FR-A5** **Per-tool configuration:** a tool declares a typed config **schema**; the registry exposes it; Security renders a **global default** config form per tool; agents may **override** specific fields. Unifies the scattered configs (`Brave`/`Tavily`/`Exec`/`Browser`/`Voice`/`SearchCache`).
- **FR-B…** Each Part-B fix (see §6, decisions D-B1…D-B8).
- **FR-C** SPA can browse/search/install skills from one or more registry providers (ClawHub first), with the **multi-provider model represented in the API and UI**, reusing the existing `SkillRegistry`/`RegistryManager` + SkillTrust hash verification.

### Non-Functional
- Contract-first (Hard Constraint #8): the tool registry wire type gains `category`, `dangerous`, and a `config_schema`/`config` shape — all via `contracts/openapi.yaml` → regenerate.
- Single Go binary, pure Go, no new runtime deps (Hard Constraint #1/#2).
- Security: flat defaults must not silently grant a new custom agent agent-management/exec — the `dangerous` rail substitutes for the retired `system.*:deny` seed (D-A3).
- No regression to the existing per-call policy filter / audit.

### Constraints
- `[FACT]` Existing assets to reuse, not rebuild: `pkg/tools/builtin_registry.go`, `compositor.go FilterToolsByPolicy` (global×agent, deny>ask>allow, wildcards), `Tool.Category()`/`RequiresAdminAsk()` (`base.go:25-92`), the `ToolCategory` enum (file/code/web/browser/communication/task/search/skills/hardware/workspace/mcp), the scattered tool configs (`config.Tools.*`), `pkg/skills` `SkillRegistry`+`RegistryManager`+`ClawHubRegistry` (→ `clawhub.ai/api/v1/{search,skills,download}`), `src/assets/logo/omnipus-logo.svg`.
- Pre-1.0, **no production users / no on-disk config migration** required (per the prior redesign spec) — simplifies the registry move and the config-shape change.

## 3. Gaps and Ambiguities
- `[FACT, grill-verified F-12]` `clawhub.ai/api/v1/search` returns HTTP 200 with a shape matching `clawhubSearchResponse` — the integration is viable. **Caveat:** `version` is `null` in live data, so the SkillTrust "source+version" refinement (§10) must tolerate a null version. The browse UI must still degrade gracefully if the registry is unreachable.
- `[UNKNOWN]` Whether the owner's instance seeded *all* providers via the web onboarding wizard vs a default catalog — irrelevant to the fix (D-B2) but noted.
- `[ASSUMPTION]` Per-tool config field kinds: `provider-select`, `credential-ref` (vault), `number`, `text`, `string-list`, `model-select`, `toggle`, `path-list`. To confirm in plan-spec.
- `[UNKNOWN]` Additional per-tool config examples beyond search-provider/voice/exec/browser/fetch — owner to add during plan-spec.

## 4. Decision Criteria
Correctness/security > consistency with existing engine > minimal blast radius (reuse > rebuild) > UI simplicity for non-technical users > extensibility.

## 5–6. Decisions, Options, Recommendation (per-decision)

### D-A1 — Central registry = metadata/policy SSOT; per-agent execution instances stay per-agent *(revised — grill F-01/F-06)*
**Critical correction (F-01):** general builtins are **NOT stateless** — `exec` = `NewExecToolWithDeps(agent.Workspace, …)` (`loop.go:1020`), `install_skill` = `NewInstallSkillTool(registryMgr, agent.Workspace)` (`loop.go:1378`); each agent gets a distinct instance bound to **its own workspace**. A single shared instance would leak one agent's workspace into another → sandbox bypass / cross-agent data leak. **Therefore the consolidation is a METADATA consolidation, not an execution one.**
**Hard invariant (must be stated in the spec):** the central `BuiltinRegistry` holds tool **metadata/templates** (name, description, **category**, **dangerous**, **config-schema**) — the single source of truth for `/api/v1/tools`, policy resolution, and the UI — while **execution instances remain per-agent, bound to `agent.Workspace`**. The boot-time `systools.AllTools(nil,nil)` already builds exactly this kind of deps-free metadata catalog; the fix is to also register the **general-builtin metadata** into it (today it is system-only by construction, `gateway.go:614`).
**Also a real code bug to fix (F-06):** the live-deps registry re-population at `gateway.go:689` is **dropped on the floor** — the registry was already passed *by value* into the restAPI (line 630), so `restAPI.builtinRegistry` keeps the nil-deps copy. Fix by passing a pointer or re-setting the field.
**Rejected:** "one shared tool instance" (B-1 of the original framing) — breaks workspace isolation; second global endpoint / API-time aggregation — re-fragments the SSOT.
> **Confidence: High** *(raised — reframed from an execution refactor to a metadata-exposure fix; blast radius is far lower)* · Basis: `[FACT]` `systools.AllTools` already produces the catalog; per-agent instancing + the policy engine already exist · Missing: the deps-free metadata-template extraction for the general builtins (mechanical) · Improve: n/a.

### D-A2 — Uniform treatment; retire the epic's system-hidden / deny-rail constructs
**Recommend:** delete the `category==='system'`→Advanced separation and the `§2.1` `system.*:deny` seed from `ToolPolicyEditor`/SecuritySection/ToolsAndPermissions; render one flat, categorized list identical in Security (global) and per-agent.
> **Confidence: High** · Basis: owner direction + removes the reproduced double-list/empty-grid bug · Missing: none.

### D-A3 — Flat defaults, with `dangerous` carrying the safety rail *(owner-chosen)*
**Decision (revised — grill F-07):** every tool defaults to the global default policy; `dangerous` tools take a **graduated strict default** rather than a uniform one — because Ask still surfaces the tool in the LLM `tools[]` and to the approver path (weaker than the retired `system.*:deny` seed for `exec`/agent-mgmt):
- **High-danger cluster → global default Deny** (invisible — matches the retired deny-seed intent): agent-management (`system.agent.*` create/delete/activate), `configuration`/`config.set`, `mcp.add`, `channel.enable`/`configure`, `skill.install`, `exec`, `web_serve`.
- **Medium-danger → global default Ask**: browser, and read-only introspection/system tools.
Operators raise/lower per tool, globally and per-agent.
**Rejected:** truly-flat (removes the rail); uniform dangerous→Ask (F-07: leaves `exec`/agent-mgmt visible-and-approvable by default — strictly weaker than today for a fresh custom agent).
> **Confidence: High** *(owner-confirmed 2026-06-03 — graduated defaults: high-cluster→Deny, medium→Ask)* · Basis: restores the prior deny-seed's intent for the high cluster while keeping medium tools usable-with-prompt · Missing: exact cluster membership per tool (mechanical, plan-spec) · Improve: ties hard to F-02 — never default-allow a tool with no explicit danger decision.

### D-A4 — Purpose categories; split `system.*` finely *(owner-chosen: finer split)*
**Decision:** extend the enum with purpose categories and **split** `system.*`: `agent-management` (create/delete/activate/deactivate), `configuration` (config.set/get), `introspection` (read/write/list metadata). Re-tag general tools off `CategoryCore` default (exec→code, read_file/write_file→file, web_search→search, browser.*→browser, memory→memory[new], task_*→task). Retire `core`/`system` as user-facing categories.
**Scope field (grill F-05):** `ToolRegistryEntry` also carries a **separate `scope` enum (`system/core/general`)** driven by `Tool.Scope()`. Retiring `system`/`core` from `category` while leaving them in `scope` would let the "system/core" labels resurface via a scope badge — the exact bug the owner wants gone. Plan-spec must decide `scope`'s fate: keep it as a **non-user-facing internal discriminator** (SPA must not render it as a grouping axis) or fold it into the category model.
> **Confidence: High** · Basis: owner chose finer split; enum already extensible · Missing: exact per-tool category assignments (mechanical) + the `scope`-field decision — plan-spec.

### D-A5 — `dangerous` flag: hand-set per tool, CI-enforced completeness *(revised — grill F-02/F-09)*
**Recommend:** add `Dangerous() bool` to the `Tool` interface and surface `dangerous` on the registry wire type. **It MUST be hand-set per tool and MUST NOT be derived from `RequiresAdminAsk()`** — `[FACT]` `RequiresAdminAsk` is uniformly `true` for all 41 system tools incl. read-only list tools (`pkg/sysagent/tools/admin_ask.go`) and `false` for `exec` (no override), so it is nearly orthogonal to danger. **Mandate a CI completeness gate:** a registry test that FAILS if any registered tool lacks an *explicit* `Dangerous()` decision — **no silent `false` default**, which is exactly how the rail leaks (F-02). Plan-spec produces the explicit **(category, dangerous, RequiresAdminAsk) truth table** for all ~65 tools and decides whether to collapse `dangerous`/`RequiresAdminAsk` (F-09); a `dangerous=true, RequiresAdminAsk=false` tool (Ask default, no runtime admin fence) is a silent gap to forbid. `dangerous` drives the UI badge + the D-A3 default; `RequiresAdminAsk` stays the runtime admin ask-fence.
> **Confidence: Medium-High** *(raised — the CI gate makes "completeness" a verifiable exit criterion, not a hope)* · Basis: grill identified the un-derivable signal + the silent-default leak · Missing: the truth table + the canonical dangerous list (mechanical, plan-spec) · Improve: the CI gate is the control.

### D-A6 — Per-tool config datamodel: declared schema, global default + per-agent override *(owner-chosen scope)*
**Recommend:** each tool optionally implements `ConfigSchema() []ToolConfigField` (typed fields). The registry exposes the schema; persisted config lives as `config.Tools.<tool>` (global default) and an optional per-agent override map (`AgentConfig.tool_config[<tool>]`). The Security UI renders the global form; the agent UI renders an override form. Migrate the scattered `Brave/Tavily/Exec/Browser/Voice/SearchCache` configs under this model (adapter, not rewrite). Credential fields use `credential-ref` (vault), never plaintext.
**Contract shape (revised — grill F-04):** the config schema MUST be a **closed discriminated union of field kinds** — `provider-select`, `credential-ref`, `number`, `text`, `string-list`, `model-select`, `toggle`, `path-list` — defined as a `ToolConfigField` schema in `contracts/components/schemas/`, so the wire type stays **generated + Zod-validated** (Hard Constraint #8). **Free-form JSON-Schema passthrough is forbidden** (it defeats the generated-type guarantee). Plan-spec must enumerate, per scattered config (Brave/Tavily/Exec/Browser/Voice/SearchCache), which fields are **global-only** vs **per-agent-overridable** — load-bearing for the persistence model.
**Options considered:** global-only (rejected — owner wants overrides); free-form JSON blob (rejected — no typed UI/validation + HC#8 violation).
> **Confidence: Medium** · Basis: owner-chosen two-layer scope + existing scattered configs prove the field kinds; the closed-union decision resolves the contract tension · Missing: the per-field global-vs-override matrix · Improve: **dedicate a `/grill-spec` pass to this datamodel during plan-spec.**

### D-A7 — UI: one `ToolPolicyEditor` (flat categorized list, dangerous badge, per-tool config drawer)
**Recommend:** Security = global policy + global tool-config; agent = per-agent policy + per-agent config overrides; same component, two modes. Per-tool config opens a drawer/sheet (consistency with D-B7).
> **Confidence: High** · Basis: reuse the epic's component, corrected.

### D-B1 — Provider "green" only when configured + connected
`[FACT]` `/providers` hard-codes `Status: Connected` for every configured provider (`rest.go:3451`). **Recommend:** Connected iff the provider's `api_key_ref` resolves to a non-empty credential (and optionally a cached successful `/test`); otherwise `Disconnected`/needs-config. Only list providers the user has touched, or list catalog with honest per-item status.
> **Confidence: High** · Basis: root-caused one-liner.

### D-B2 — Restart banner: hot-reload-on precondition + re-baseline post-onboarding *(revised — grill F-03)*
`[FACT]` `gateway.users` is ALREADY in `RestartGatedKeys` (`rest_pending_restart.go:36`) with `normalizeUsersForDiff` stripping rotating fields — yet the banner still fires on a fresh install. Two coupled facts: (1) `hot_reload` defaults **false** (`defaults.go:363`) and gates the entire reload watcher (`gateway.go:779`) — with it off, *no* key applies without restart; (2) `appliedConfig` is a **once-at-boot snapshot** (`rest.go:84-90`), so onboarding writes diff against the pre-onboarding baseline. **Recommend:** (a) D-B8 forcing `hot_reload=on` is a **precondition** for any coherent restart-key model — sequence them together; (b) re-baseline `appliedConfig` **after onboarding completes** — a new code path the spec must scope (it does not exist today); (c) verify whether `gateway.users` truly applies without restart (auth state may be boot-cached — the reason it was gated): if hot, remove from `RestartGatedKeys`; if not, keep but suppress on fresh install via the re-baseline. Define the authoritative restart-required key set (likely `bind_address`, `port`, sandbox `mode`, listener toggles) from what the gateway actually re-binds.
> **Confidence: Medium** · Basis: grill located the once-at-boot baseline + the hot_reload gate · Missing: the authoritative restart-key set + whether `gateway.users` is genuinely hot — **derive in plan-spec.**

### D-B3 — WhatsApp QR
`[FACT]` `native_available:true` on the default build — not a build issue; the `enable→channel-start→whatsapp_pairing` WS-frame flow doesn't deliver the QR. **Recommend:** reproduce on a controlled instance (enable+save WhatsApp, watch the WS), root-cause whether the channel starts on enable (vs needing reload) and whether the pairing frame is emitted/forwarded; fix the missing link.
> **Confidence: Low-Medium** · Basis: narrowed to the runtime flow, not yet root-caused · Improve: **the repro is the next concrete step.**

### D-B4 — Session token TTL
**Recommend:** investigate `pkg/gateway/auth.go` token expiry; if the TTL is genuinely ~minutes, extend to a sane session length (or add refresh). Decide in plan-spec after reading the actual TTL.
> **Confidence: Low** · Missing: the actual TTL value — read first.

### D-B5 — Profile font-size wiring
`[FACT]` `--user-font-size` is set but consumed nowhere. **Recommend:** apply it as the root font-size (e.g. `html { font-size: var(--user-font-size) }`) with `rem`-based scaling, bounded min/max.
> **Confidence: High.**

### D-B6 — About page
**Recommend:** use `src/assets/logo/omnipus-logo.svg`; GitHub → `https://github.com/elicify-ai/omnipus` (owner-chosen); inject the real build version at build time (ldflags) instead of `dev`.
> **Confidence: High.**

### D-B7 — Config surface consistency → slide-out `Sheet`
**Recommend:** convert `McpServerModal` (centered `Dialog`) → `Sheet` (slide-out), matching `ChannelConfigPanel`. Establish "config surfaces use `Sheet`" as the convention (covers the per-tool config drawer in D-A7).
> **Confidence: High.**

### D-B8 — Gateway: remove no-auth from UI (keep backend); hot-reload always-on (remove toggle) *(owner-chosen)*
**Recommend:** drop the `auth_mode:'none'` option from `GatewaySection` (token always); keep backend config-file capability for proxy-fronted deployments. Force `hot_reload` on, remove the toggle (ties to D-B2's restart-key set).
> **Confidence: High** · Basis: owner-decided.

### D-C1 — ClawHub: wire SPA↔existing RegistryManager; surface multi-provider in API + UI *(owner-chosen)*
`[FACT]` `SkillRegistry` interface + `RegistryManager.SearchAll/AddRegistry` + `ClawHubRegistry`(→`clawhub.ai/api/v1`) exist; only the SPA-facing REST browse/install endpoints are 501 stubs. **Recommend:** implement the REST search/list/install endpoints bridging to `RegistryManager`; **expose the provider list in the contract + a registry-provider management UI** (browse per-provider, see ClawHub as the first; design for adding providers); reuse SkillTrust hash verification on install; verify against the live `clawhub.ai` API.
> **Confidence: Medium-High** · Basis: backend exists; owner requires API+UI representation of multi-provider · Missing: live-API verification + the provider-management UX detail — plan-spec.

## 7. Risks and Caveats
- **Registry change (D-A1) is now LOWER blast radius after the F-01 reframe** — it is a *metadata* registration (register general-builtin descriptors into the central catalog + fix the `gateway.go:689` deps-registry bug), NOT a move of execution instances. The **hard invariant** (per-agent execution instances stay workspace-bound) must be asserted by a test; without it, a regression to shared instances is a workspace-isolation breach.
- **The `dangerous` rail (D-A3/A5) is the top residual risk** — its safety depends on *complete* per-tool tagging. The CI completeness gate (D-A5) is the control; if it is weak or skipped, a mis-tagged `exec`-class tool gives a fresh custom agent silent access. Treat the gate as a release-blocking exit criterion.
- **Per-tool config (D-A6)** is the only greenfield piece — the closed `ToolConfigField` union (F-04) keeps it contract-safe, but the per-field global/override matrix is unresolved; isolate it in Spec-2 so it cannot slip Spec-1.
- **Flat defaults (D-A3)** reduce default safety; the `dangerous→Ask` rail is the only thing standing between a new custom agent and `exec`/agent-management. If `dangerous` flags are incomplete, the rail leaks — **completeness of the `dangerous` tagging is security-critical**.
- **Per-tool config (D-A6)** introduces a dynamic, typed config schema across the wire — the hardest contract-first piece; risk of an under-specified schema shape. Recommend a dedicated grill-spec pass on D-A6.
- **Scope:** this is a large release (registry redesign + new config datamodel + ClawHub + 8 fixes) layered on the as-yet-unmerged epic. Sequencing/integration risk is real; mitigate by speccing tools (A) and clawhub (C) as separable plan-specs, fixes (B) as a batch.
- **WhatsApp QR (D-B3)** is not yet root-caused — schedule its repro before committing its fix scope.

## 8. Confidence Assessment
**Overall: Medium-High.** The two largest pieces (A, C) are *completion/correction of existing, grounded designs*, not greenfield — which raises confidence materially. The genuinely novel/uncertain piece is **D-A6 (per-tool config datamodel)**; the genuinely un-root-caused piece is **D-B3 (WhatsApp QR)**. Both are flagged for focused follow-up. Security hinges on **complete `dangerous` tagging (D-A3/A5)**.

## 9. Validation / Next Steps
1. `/grill-spec docs/internal/architecture/ADR-018-tools-system-and-v01-showstopper-resolution.md` — red-team, with explicit focus on **D-A6 (config datamodel shape)**, **D-A3 (dangerous default + tag completeness)**, and the **restart-required key set (D-B2)**.
2. `/plan-spec` as **three HARD-bounded, sequenced specs** (grill F-08 — boundaries are enforced, not "recommended"):
   - **Spec-1 — unblocks PR #344, ships first:** Part-B showstopper fixes (D-B1/B5/B6/B7/B8, and D-B2 *with* D-B8 as its precondition) **+** the **D-A1/A2 metadata-registry correction + the `gateway.go:689` deps-registry bug fix + the flat `ToolPolicyEditor`** (this alone fixes the reported Security tools showstopper). Minimal v0.1 set.
   - **Spec-2 — independent:** D-A3/A4/A5 (categories, `dangerous` flag + CI gate, graduated defaults) **+** D-A6 per-tool config datamodel. Greenfield/riskiest; MUST NOT gate Spec-1.
   - **Spec-3 — independent:** D-C1 ClawHub browse/install + multi-provider API/UI.
   - **Decouple D-B3 (WhatsApp QR) and D-B4 (token TTL):** time-box their root-cause (F-10/F-11); if either proves deep, it drops out of Spec-1 rather than holding PR #344. Note (F-11): the 2–3 min expiry was **not located in `auth.go`** — find the actual mechanism (JWT exp / session sweep / client refresh gap) before speccing a fix.
3. Resolve the open items during plan-spec: per-tool config field taxonomy + global/override per field; the authoritative restart-required keys; the WhatsApp-QR repro/root-cause; the auth token TTL value; per-tool category assignments; live clawhub.ai verification.
4. PR #344 stays unmerged to `main` until A+B+C are built and re-tested.

## 10. External best-practice validation (online research)
`[FACT, sourced]` The chosen directions align with current practice:
- **Allow/Ask/Deny three-state + fine-grained per-tool config** (e.g. shell allowed for read-only, denied for `rm -rf`) is the standard agent-permission model — matches the existing `compositor` engine and D-A6 (Cerbos "MCP Permissions"; WorkOS "agent permissions 2026"). Validates D-A2/A3/A6.
- **Multi-provider skill/plugin registries** are typically modeled as **public + private sub-registries**, each provider carrying its own metadata/policy (MCP Gateway Registry; JFrog MCP Registry; Anthropic *Skilldex* — a GitHub-repo-backed skills directory with a "verified" trust tier). Validates D-C1's provider abstraction and suggests future providers beyond ClawHub (a git/GitHub provider, a private/internal provider).
- **Trust/verification**: recording **source + version + SHA-256 content hash** per installed skill, with a **trust badge/tier**, is the norm (Azure SRE Agent Plugin Marketplace; Skilldex verified tier). **Refinement for plan-spec:** extend the existing SkillTrust hash model to also persist **source registry + version**, and surface a per-skill **trust badge** (verified/unverified) in the browse UI.

Sources: [Cerbos — MCP Permissions](https://www.cerbos.dev/blog/mcp-permissions-securing-ai-agent-access-to-tools) · [WorkOS — agent permission platforms 2026](https://workos.com/blog/best-authorization-platforms-ai-agent-permissions-2026) · [MCP Gateway Registry](https://github.com/agentic-community/mcp-gateway-registry) · [JFrog MCP Registry](https://jfrog.com/ai-catalog/mcp-registry/) · [Azure SRE Agent Plugin Marketplace](https://learn.microsoft.com/en-us/azure/sre-agent/plugin-marketplace) · [Skilldex (arXiv)](https://arxiv.org/html/2604.16911v1) · [WorkOS — MCP Registry architecture](https://workos.com/blog/mcp-registry-architecture-technical-overview)

---
*Per-decision confidence blocks above follow the canonical format. Owner-locked decisions are marked "(owner-chosen)"; remaining Low/Medium-confidence items name their improvement path.*
