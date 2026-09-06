# ADR-076 — Reconcile the global tool-policy ceiling against shipped defaults on every load

- **Status:** Accepted (2026-09-06), implemented same day (`pkg/config/validate.go`, `pkg/gateway/gateway.go`). Superseded-in-part by [[ADR-077]]: `ReconcileToolPolicyCeiling` (D3/D5 additive-only, in-memory-only) stands unchanged and becomes load-bearing, but the "true backstop… fires only for a genuine drift" framing this ADR gave `RepairIncompleteToolPolicyCoverage` (see the Background section below) is retired — ADR-077 deletes that function and its boot-time sibling `ValidateAgentOwnToolPolicyCoverage` outright, ratifying the reconciled global ceiling as the sole default with no fail-closed per-agent backfill layer.
- **Deciders:** Daniel Piatkowski (operator)
- **Extends:** [[ADR-071]] §5.3.5 (legacy tool-policy key migration) — this ADR adds the sibling reconciliation step that migration cannot cover. Also touches the coverage/repair contract established alongside `config.RepairIncompleteToolPolicyCoverage` and `config.ValidateAgentOwnToolPolicyCoverage` (2026-07-07/2026-09-02).
- **Motivation:** verified on a real upgraded install. `pkg/config/defaults.go`'s `DefaultConfig().Sandbox.ToolPolicies` — the seeded, install-time GLOBAL ceiling CLAUDE.md hard constraint 6 describes — only ever gets written onto a config.json at first install. Eleven static builtin tools added to `defaults.go` after that install's config.json was written (`AskUserQuestion`, `Skill`, `ToolSearch`, `browser_handle_dialog`, `browser_hover`, `browser_press_key`, `browser_select_option`, `browser_snapshot`, `browser_upload_file`, `list_mounts`, `switch_agent`) had no entry in that install's on-disk ceiling, and nothing reconciled it forward on upgrade.

## Background: why this was not a total resolution failure, but was still a real bug

Two existing mechanisms already prevent a literal "no policy entry anywhere" hole for a tool missing from an old config.json's ceiling:

1. `pkg/config/migration.go`'s `loadConfig` unmarshals the operator's JSON onto a `DefaultConfig()`-seeded `*Config`. Go's `encoding/json` reuses an existing non-nil map on unmarshal, keeping entries the JSON doesn't mention — so a tool absent from the on-disk `sandbox.tool_policies` object is, in the common case, still present in the **in-memory** `cfg.Sandbox.ToolPolicies` at its shipped default value immediately after `LoadConfig`.
2. `config.RepairIncompleteToolPolicyCoverage` (2026-07-07) backfills any `(agent, tool)` pair with no entry on *either* side to an explicit `"deny"` on the **agent's own** map, so `config.ValidateToolPolicyCoverage` never finds a genuine gap and boot never aborts on this.

Neither mechanism gives the right *answer*, only an answer:

- Mechanism 1 is an accident of Go's map-merge semantics, not a documented or tested contract, and it never touches the file on disk — `config.json` stays permanently missing the entry, so anything that reads the file directly (backup/restore, an operator diffing their config, a future tool that re-marshals from raw JSON) sees an incomplete ceiling indefinitely.
- Mechanism 2 actively produces the **wrong value** for a newly-added tool that ships `"allow"` (or, for `browser_upload_file`, `"ask"`): it fail-closes to `"deny"` on the **per-agent** map for every agent, because from `RepairIncompleteToolPolicyCoverage`'s point of view a global-ceiling gap and a genuinely-uncovered tool look identical. A brand-new tool like `AskUserQuestion` — shipped `"allow"`, load-bearing for basic interactive flows — silently becomes unusable on every pre-existing install the first time its gap gets backfilled, with only a WARN log (easy to miss) as the trace of what happened. `gateway.go`'s `ValidateAgentOwnToolPolicyCoverage` soft-ERR log (the one CLAUDE.md's line about "gateway.go:1381" and constraint 6 refers to) additionally fires for this and every other tool that resolves from the ceiling alone — expected background noise by design (per-agent maps are deliberately not force-filled), but it made this specific, real regression harder to distinguish from routine "riding the ceiling" logging.

## Decision

### D1 — Add `config.ReconcileToolPolicyCeiling`, run between key migration and per-agent repair

A new pure function, `pkg/config/validate.go`'s `ReconcileToolPolicyCeiling(cfg *Config, knownTools map[string]struct{}) []string`, backfills `cfg.Sandbox.ToolPolicies` — and *only* the global ceiling, never a per-agent map — with the shipped default value for every name in `knownTools` that the ceiling has no entry for yet. The default value for each name comes from `DefaultConfig().Sandbox.ToolPolicies`, which `defaults.go`'s own doc comment states mirrors `pkg/coreagent/core.go`'s `allStaticToolNames` literal-for-literal — the same static catalog `knownTools` represents when the caller passes `buildKnownBuiltinToolNames()`'s output (a `TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog` regression test keeps the two enumerations from drifting apart).

Wired into `pkg/gateway/gateway.go`'s `repairAndValidateToolPolicyCoverage` — the single helper both boot (`RunContextWithOptions`) and hot-reload (`executeReload`) already funnel through for `MigrateLegacyToolPolicyKeys` and `RepairIncompleteToolPolicyCoverage` — in this exact order:

1. `config.MigrateLegacyToolPolicyKeys` (unchanged, ADR-071) — rename retired keys (`load_tool`→`ToolSearch`, `hand_off`/`return_to_default`→`switch_agent`) forward first.
2. **`config.ReconcileToolPolicyCeiling` (this ADR)** — backfill the global ceiling with real shipped defaults for anything still missing.
3. `config.RepairIncompleteToolPolicyCoverage` (unchanged) — the fail-closed per-agent backstop for anything step 2 could not close (e.g. `knownTools` itself empty, or a name with no `defaults.go` entry — a drift case, not the normal path).
4. `config.ValidateToolPolicyCoverage` (unchanged) — the hard backstop; should find zero gaps in the ordinary case now.

Step 2 must run strictly between steps 1 and 3: after migration, so a just-renamed key is never treated as "missing" and reconciled to a possibly-different default under its old name; before the per-agent repair, so a global-ceiling gap is closed with the tool's real shipped posture instead of every agent individually fail-closing to `"deny"`.

### D2 — Never overwrite an existing entry, on either side

`ReconcileToolPolicyCeiling` only ever ADDS a `sandbox.tool_policies` key that is completely absent. An operator-set value — including one the operator deliberately tightened to `"deny"` for a tool that ships `"allow"` — is never touched, at any point, regardless of when it was set relative to this reconciliation running. This is the same non-negotiable rule CLAUDE.md hard constraint 6 already states for the seed itself ("that seed is data an operator can edit on their own installation afterward, not a fallback branch baked into the binary") — reconciliation extends the seed forward on upgrade, it does not re-assert it.

### D3 — Additive-only against the CURRENT static catalog; never re-adds a retired key

Reconciliation iterates `knownTools`, not `cfg.Sandbox.ToolPolicies`. A key already on disk that is *not* in the current static catalog — a retired/renamed tool no longer enumerated by `buildKnownBuiltinToolNames()`, an MCP-namespaced key, any operator custom entry — is left exactly as-is. This function is not a cleanup pass and must never delete or rewrite a key outside the "add a missing, currently-known static tool" case. (Deleting a stale key is explicitly out of scope; nothing in the runtime resolver treats an unrecognized ceiling key as harmful, and removing operator data unprompted is a bigger risk than leaving it inert.)

### D4 — Per-agent maps stay intentionally sparse; this is a ceiling-only fix

This ADR does not force-fill any `AgentConfig.Tools.Builtin.Policies` map with the new entries, and must not be extended to do so. Per-agent maps exist only to *tighten* below the ceiling (CLAUDE.md hard constraint 6: "no default-policy fallback... every static builtin tool must resolve from an explicit... entry — either globally... and/or an agent's..."); an agent with no opinion on a tool is meant to ride the ceiling, and `config.ValidateAgentOwnToolPolicyCoverage`'s soft-ERR log at boot (`gateway.go`, "agent has no explicit tool-policy entry of its own...") is the existing, correct, by-design mechanism for surfacing that state to an operator who wants to audit it — not a defect this ADR needs to fix. Coverage completeness is the ceiling's job; per-agent completeness is a separate, deliberately softer contract.

### D5 — In-memory only; matches `MigrateLegacyToolPolicyKeys`'s existing persistence model

Like the legacy-key migration it runs alongside, `ReconcileToolPolicyCeiling`'s mutation is applied to the in-memory `*config.Config` at every boot and every hot-reload — it is not separately flushed to `config.json` by `repairAndValidateToolPolicyCoverage` itself. This is a deliberate scope limit, not an oversight: `repairAndValidateToolPolicyCoverage` is a free function called from two other free functions (`RunContextWithOptions`, `executeReload`) that hold neither a `*Gateway` receiver nor the `configMu`-guarded `safeUpdateConfigJSON` write path, and CLAUDE.md is explicit that `config.SaveConfig()` (a whole-struct re-marshal) must never be used for config persistence because it can corrupt credential fields. Because the reconciliation re-runs identically, idempotently, on every boot and every hot-reload, the practical effect is the same as if it were persisted: the running gateway's resolved policy — and the coverage/repair validators that run immediately afterward in the same call — always see the reconciled ceiling, every time. The one gap this leaves is that `config.json` on disk can continue to omit an entry the ceiling nonetheless resolves from correctly at runtime; closing that (writing the reconciled ceiling back through `safeUpdateConfigJSON` from an authenticated gateway context, or a dedicated one-time CLI migration) is left as explicit follow-up work, not bundled into this fix.

## Consequences

**Positive:** a static builtin tool added to `defaults.go` after an install's config.json was last written now resolves, at runtime, from the SAME shipped value a fresh install would seed for it (e.g. `AskUserQuestion: allow`, `browser_upload_file: ask`) — not a fail-closed per-agent `"deny"` an operator has to notice and manually undo per agent. `RepairIncompleteToolPolicyCoverage`'s deny-backfill becomes a true backstop again (fires only for a genuine drift or corrupt config), rather than the routine mechanism silently deciding new-tool posture on every upgrade.

**Accepted limitation:** the reconciled ceiling is not written back to `config.json` by this change (D5) — an operator inspecting the file directly, or a tool that reads it without going through `config.LoadConfig`, can still see a stale ceiling even though the running gateway resolves correctly. Tracked as explicit follow-up, not silently deferred.

**Out of scope:** per-agent map backfill (D4), retired-key cleanup (D3), and disk persistence (D5) are all deliberately not addressed here.

## Verification

`pkg/config/validate_test.go` — new tests alongside the existing `MigrateLegacyToolPolicyKeys`/`RepairIncompleteToolPolicyCoverage` coverage:

- `TestReconcileToolPolicyCeiling_AddsMissingStaticToolsAtShippedDefaults` — a ceiling missing `AskUserQuestion` and `browser_upload_file` ends up with `allow` and `ask` respectively after one call.
- `TestReconcileToolPolicyCeiling_PreservesOperatorSetValue` — an operator-set `AskUserQuestion: deny` survives unchanged.
- `TestReconcileToolPolicyCeiling_LeavesUnrelatedAndCustomKeysUntouched`
- `TestReconcileToolPolicyCeiling_Idempotent_SecondCallIsNoOp`
- `TestReconcileToolPolicyCeiling_DoesNotReAddRetiredKey` — a name outside the current `knownTools` set is never (re-)added even if `DefaultConfig()` no longer carries it either.
- `TestReconcileToolPolicyCeiling_RunsAfterMigration_NoDuplicateOrStaleName` — `load_tool` present, `ToolSearch` absent → migration first, reconciliation second → ends with exactly one `ToolSearch` entry (the migrated value), no `load_tool` left, and no double-write.
- `TestReconcileToolPolicyCeiling_NilOrEmptyInputs_NoOp`

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestReconcileToolPolicyCeiling' -p 1 ./pkg/config/` — run locally per this project's OOM-avoidance rule (never the full suite); CI is authoritative for the rest.
