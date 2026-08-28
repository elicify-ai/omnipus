# Grill-Spec Review — ADR-071 (revision 3)

**Mode:** structured-spec (numbered decisions D1–D5, ratification table, traceability matrices — no BDD/FR-xxx scaffolding). Read in full: `docs/internal/architecture/ADR-071-tool-manifest-tier-redesign.md` (1,944 lines). This is the **third** pass at this document; two prior adversarial reviews (`ADR-071-tool-manifest-tier-redesign-review.md`, `ADR-071-tool-manifest-tier-redesign-r2-review.md`) already exist and are cited extensively in r3's own text.

Every claim below was checked against source at the working tree's current state (`release/v0.1.1` branch, `pkg/tools/registry.go`, `pkg/tools/tools_tool.go`, `pkg/agent/loop.go`, `pkg/agent/tool_manifest.go`, `pkg/agent/events.go`, `pkg/gateway/websocket.go`, `pkg/gateway/metrics.go`, `pkg/tools/compositor.go`), not inferred from the document's own citations.

## Executive Summary

2 CRITICAL, 2 MAJOR, 3 MINOR/OBSERVATION. Both CRITICAL findings are **new** — neither r1 nor r2 found them, and r3's own text does not mention them despite the document's explicit, repeated claim that its self-verification is thorough ("verified — where someone looked"). Both trace to the same root cause: **the document conflates the registry-level TTL/`PromoteTools` mechanism (which only governs MCP/hidden tools) with the session-level `loadedTools` map (which governs the 89-name static catalog this whole ADR is about), and those two mechanisms behave completely differently** — one decays, the other never does. That confusion undermines a "hard prerequisite of D3 shipping" (§4.3.1's no-follow-up counter), the cost bound on D2's multi-promotion (§3.3, restated in §7), and — via a second, related gap — the "71% invisible by default" risk-acceptance framing itself (§4.3), because the invisibility is not actually per-agent.

Verdict: **BLOCK**.

## Findings

### CRIT-101 — The TTL/`PromoteTools` reclamation mechanism §3.3 and §4.3.1(a) both depend on does not apply to static tools, which is what this entire ADR is about

**Where:** §3.3 ("Reclamation is the existing TTL, unchanged"), §4.3.1(a) (the `no_followup` counter design), §7 Consequences ("Bounded and reclaimed by existing TTL; no new mechanism").

**What I verified.** `pkg/tools/registry.go`: every static builtin tool (all of Tier 1/2/3 — everything in `allStaticToolNames`) is registered via `Register()`, which sets `IsCore: true` (`registerToolLocked`, `Register` at line 194). MCP tools alone go through `RegisterHidden`/`RegisterHiddenMCP`, `IsCore: false`. `PromoteTools` (line 320) only mutates `entry.TTL` `if !entry.IsCore` — for a core (static) entry it is **explicitly a no-op**, and `TickTTL` (line 340) only decrements `!entry.IsCore` entries. `GetAll` (line 867) admits an entry when `entry.IsCore || entry.TTL > 0` — so a core entry is unconditionally present regardless of TTL. `pkg/tools/tools_tool.go`'s own comment at the exact call site (lines 316–320, `execLoad`) states this plainly: *"PromoteTools is a no-op for already-visible tools... markLoaded is harmless for already-promoted tools. Together they make both hidden-MCP and in-process-lazy tools callable."* `execSearchAndLoad` (line 183) does the identical thing for the query path.

What actually makes a static lazy tool callable is `markLoaded` → `pkg/agent/loop.go::markToolsLoaded` (line 12829), which writes into `al.loadedTools[sessionID][toolName] = true` — a plain map with **no decay, no cap, and no eviction path** other than `forgetSession`, called only on session close. `sentToolSurfaceTokens` and `buildCompressedToolDefs` both gate a `ManifestLazy` tool's presence in the callable set purely on `loaded[t.Name()]` (`pkg/agent/loop.go:11319`, `pkg/agent/tool_manifest.go:157`) — no TTL check anywhere in that path.

**Consequence.** Once a static Tier 2 or Tier 3 tool is loaded — by exact name or by D2's ambiguous multi-promotion — its full callable schema stays in the array **for the rest of the session**, not "for at most TTL turns (default 5)" as §3.3 claims and §7 repeats as an accepted, bounded cost. D2 explicitly widens this exposure to up to 3 tools per ambiguous query (§3.2), including — per the ADR's own STRIDE-adjacent discussion — Tier 3 verbs. Over a long session that uses `ToolSearch` repeatedly (which D3 makes the *only* discovery path for 71% of the catalog, so heavier use is the expected outcome, not an edge case), this is a monotonically growing callable-tool array, which §6.4 already establishes invalidates the prompt cache on every growth step. This is a second, compounding unbounded-growth vector distinct from the one F3 (§1.2) rules out — F3 is about the *preview block*; this is about the *loaded/callable set*, and it is real.

This also breaks the **"hard prerequisite of D3 shipping"** mechanism in §4.3.1(a). MIN-001's redefinition says the `omnipus_toolsearch_no_followup_total` counter fires "when a promoted entry is dropped by `TickTTL` having never been called... hook the existing TTL-expiry path in `pkg/tools/registry.go`." Since `TickTTL` never decrements or evicts a core (static) entry, that hook **will never fire for a static Tier 2/3 tool** — only for MCP tool promotions, which are explicitly outside D2/D3's scope (§1.2/F3 already establishes MCP tools "never reach `BuildCompressedManifest`'s input at all"). The counter §4.3.1 calls a non-optional detector for "the split is wrong / promotions are missing" will read **zero, permanently**, for the exact class of promotion it exists to monitor.

**Fix required, not optional given the stated hard-prerequisite status:** either (a) build the "one extra bit... cleared on first invocation" against the session-level `loadedTools` map instead of the registry TTL path (a materially different, unbuilt mechanism — the current text describes it as reusing an existing hook, which it cannot), or (b) build actual TTL-based reclamation for static-tool promotions from `ToolSearch` (a new mechanism §3.3 explicitly says is not needed), and correct §3.3/§7's cost-bound language either way. This is not a wording fix; it changes what needs to be built.

### CRIT-102 — The `loadedTools` bucket is keyed by session, not by (session, agent) — D3's "invisible by default" is not actually per-agent once `switch_agent` is in play

**Where:** §4.3 ("71% of the catalog becomes invisible by default... accepted deliberately by the operator"), interacting with D4 (§5).

**What I verified.** `pkg/agent/tool_manifest.go::manifestSessionID` derives the loaded-tools bucket key from `ts.opts.TranscriptSessionID` / `ts.sessionKey` only — no agent identity component anywhere in the key. `switch_agent` (per §5.1.2's own reconciliation table) changes the *active agent* within the **same** session/transcript (it does not spawn a new session the way `delegate` does — `spawnSubTurn` gives a child its own `TranscriptSessionID`, which is why delegation *is* correctly isolated). So: if Agent A loads a Tier 3 tool via `ToolSearch` and the conversation later `switch_agent`s to Agent B, and Agent B's own tool policy also permits that same tool name (plausible for any tool shared across the core roster's seed policies, or across any two custom agents with similar grants), Agent B's callable set will include that tool **immediately, with no `ToolSearch` call of its own**, purely because it inherited the session's loaded-tools bucket.

This directly undercuts §4.3's central risk-acceptance framing. The operator was told "71% invisible by default" and shown the tradeoff explicitly; what they were not shown is that the invisibility is **session-scoped, not per-agent-scoped**, so a multi-agent conversation using D4's own new `switch_agent` tool progressively erodes Tier 3's invisibility for every agent that later becomes active in that session — the more agents hand off within one conversation, the less D3 actually hides. This is a genuine, previously-undiscussed interaction between D3 and D4, not a restatement of either workstream's already-recorded risk.

**Fix required:** either scope the loaded-tools bucket to `(sessionID, agentID)` (a real behavior change with its own blast radius — worth its own analysis, not a one-line patch, since `manifestSessionID`'s doc comment already carries load-bearing ADR-057 reasoning about what this key must and must not include), or explicitly accept and document this leak as a further negative consequence of D3+D4 together, with the "71%" framing corrected to say "per-session, decaying toward less than 71% as agents hand off."

### MAJ-101 — §6.6's D5 merge gate is specified as a measurement with no stated mechanism for running it in CI

**Where:** §6.6 ("ΔC ≥ 0.8 × B on at least one no-load turn of a multi-turn **Anthropic** session").

This gate requires a live call to the real Anthropic API and reads `providers.Usage.CacheReadTokens`, which only the Anthropic adapter populates. CLAUDE.md's own Testing & Building section (which this ADR cites elsewhere, e.g. §10's closing line "CI is the authority... do not run it locally") describes the project's Go test/build suite as running on an ephemeral, resource-constrained CI worker with no mention of external-API-credentialed test tiers. The document never states: is this gate a `go test` in the normal CI run (implying the CI worker needs a funded Anthropic API key and network egress, which is a meaningfully different CI posture than everything else in the suite), a separately-run manual acceptance check before merge, or something else? Given §6.6 explicitly says D5 "does not merge" on failure, this is an enforcement mechanism with no stated home. Recommend: state explicitly whether this is automated (and where the credential/network exception is authorized) or manual (and who is responsible for running it and recording the number before approving the W2 merge).

### MAJ-102 — §5.3 item 5's migration (rewrite persisted `hand_off`/`return_to_default`/`load_tool` policy keys) has no stated trigger point or idempotency guarantee

**Where:** §5.3, item 5.

The migration is described in prose ("rewritten... taking the strictest value... the legacy key is deleted on that same boot") but not tied to a boot-order step the way §5.1.3's WARN or §5.3's own item-1-before-item-2 ordering is. Two concrete gaps a reader would hit: (a) is this migration idempotent if it runs twice (e.g., a crash between rewrite and delete, or two gateway instances racing on the same `config.json` on a platform without cross-process locking — CLAUDE.md's own Storage section documents that Windows has no cross-process file locking anywhere in this file-store family)? (b) does it run *before* or *after* `ValidateToolPolicyCoverage`'s boot-abort check — if after, a config with only the legacy key present would abort boot before the migration ever gets to run, since Constraint #6's validator has no notion of "will be migrated momentarily." r1/r2/r3 all invoke "ADR-036 §3.6 precedent" for the mechanics but the document doesn't confirm that precedent's boot-ordering actually composes correctly with the *new* D1+D4 double-rename case (two old-key pairs converging on two new names in the same boot). Given Constraint #6 explicitly treats a coverage gap as boot-aborting, this ordering question is not cosmetic.

### MIN-101 — §4.3's "8-line" catalog-block size claim is not verified against Tier 2's actual entries

§4.1/§4.3 repeatedly state the compressed block shrinks "from ~71 lines to 8." Tier 2 has 8 *tools*, but §6.1 says the block is "grouped by category, sorted" — if any of the 8 Tier 2 tools share a category header line (as `BuildCompressedManifest`'s existing behavior does today for the full ~71), the line count is not 1:1 with tool count; it's tools + category headers. Low stakes, easy to fix: verify against `BuildCompressedManifest`'s actual grouping output for the 8 Tier 2 names before quoting "8" as a hard number anywhere reader-facing (e.g. the header-prose rewrite in §11 Q2).

### OBS-101 — The `note` parameter's optional/prose-recommended contract has no enforcement or telemetry

§5.1.1 correctly identifies that `hand_off.context`'s `required` was never enforced and decides not to newly enforce it for `switch_agent.note` either. Given §4.3.1 goes to considerable lengths to instrument a *different* soft behavioral expectation (whether `ToolSearch` promotions get followed up on) specifically because "an unfalsifiable trigger... is not a mitigation," the same reasoning would suggest at least a counter for "how often does the model omit `note` on a named-target switch." Not blocking; noted because the document applies "measure it, don't just hope" rigorously in one place (§4.3.1) and not in an adjacent place where the same principle applies (§5.1.1).

## Structural Integrity (structured-spec checks)

| Check | Result |
|---|---|
| Every stated goal has acceptance criteria | Mostly yes — D2, D4, D5 all have explicit pass/fail tests. D3's own acceptance criteria are the drift test (§4.4) plus the two counters — but see CRIT-101, the counter is not verifiably wired to what it claims to measure. |
| Cross-references consistent | Yes, extensively cross-checked by r2/r3 already; no new broken references found. |
| Scope boundaries defined | Yes — unusually well-drawn "out of scope, filed as follow-up" calls throughout. |
| Success criteria measurable | Yes for D4/D5's tests; D3's revisit trigger is measurable in principle but its measurement mechanism is broken per CRIT-101. |
| Failure/error scenarios per requirement | Strong. |
| Dependencies between requirements identified | Yes — §10's workstream dependency graph is unusually explicit. |

## Test Coverage Assessment

No test anywhere proposed would catch a static Tier 3 tool's schema staying loaded past its intended reclamation window, because the document believes no such window needs testing (it believes TTL already handles this — CRIT-101). A regression test asserting `loadedTools` entries decay (or, if the design is revised to accept permanent-for-session loading, a test asserting the *documented* behavior matches reality) is currently absent and needs to be added regardless of which fix direction is chosen for CRIT-101.

## STRIDE Threat Summary (delta from what's already covered)

| Component | New threat |
|---|---|
| Session-scoped `loadedTools` map + `switch_agent` | Information Disclosure / Elevation-of-reach: a tool's reachability leaks across an agent boundary within one session (CRIT-102). |
| `/metrics` no-follow-up counter | Repudiation-adjacent: because the counter cannot fire for the case it's meant to measure (CRIT-101), a persistent zero reads as evidence of health when it's actually evidence the counter doesn't instrument this population at all. |

## Unasked Questions

1. Given CRIT-101, does the operator want static-tool `ToolSearch` promotions to actually decay (new mechanism, contradicting §3.3's "no new expiry is built"), or is permanent-for-session loading the accepted design, with §3.3/§7's cost language corrected to match?
2. Given CRIT-102, should `switch_agent` reset (or agent-scope) the loaded-tools bucket on handoff, and if so, does that change what the incoming agent sees in its very first turn after a handoff?
3. For §6.6's gate: is it CI-automated against a funded Anthropic key, or a manual pre-merge check, and who owns running it?
4. For §5.3 item 5: does the legacy-key migration run before or after `ValidateToolPolicyCoverage`'s boot-abort check, and is it idempotent under a crash mid-migration or two racing gateway processes on Windows (no cross-process lock)?

## Verdict: BLOCK

Two CRITICAL findings undermine load-bearing claims the document itself labels as "hard prerequisites" (§4.3.1) or "accepted, bounded" risk (§3.3/§7/§4.3), and both were verifiable in the current codebase with a handful of greps and reads. Given the document's established pattern — three passes, three new sets of finds, each time after a good-faith "full surface" claim — treat this as consistent with, not exceptional to, that pattern rather than a reason to distrust the rest of the document's extensive, already-verified analysis.
