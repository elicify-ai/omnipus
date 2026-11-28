Status: Approved

ADR: none — no open design decision. The founder settled the design in the wireframe answers (S-Q1 to S-Q14, #1096 Q1 to Q4). The contract shapes this spec needs, including the B4 choice, are architect decisions recorded in "Contract Changes" below. One existing ADR needs a dated correction because of S-Q11 B: [ADR-022 — Re-auth Consent Gate: Credential-Vault Scope Expansion to v0.1.0](../architecture/ADR-022-credential-vault-reauth.md) (see FQ-2 and DOC-012).

# Feature Specification: Settings redesign (page G)

**Created**: 2026-11-20
**Input**: founder-approved wireframe round 4 and design notes (`/workspace/omnipus-coordination/handoffs/ui-redesign/settings-wireframe/design-notes.md`, `questions.md`, `review-response.md`, `index.html`, `app.js`); Settings inventory (`/workspace/logs/ui-1519/settings/inventory.md`); founder answers S-Q1 to S-Q14 and #1096 Q1 to Q4 (`/workspace/omnipus-coordination/handoffs/founder-questions-20261119/team-b-open-questions.md`). No interview-me output file exists for this feature; the wireframe answers take its place (team-lead's brief).
**Code baseline**: release `c2f8f8e735` (after #1919, PR #1927, removed Backup and Restore). Every `file::symbol` below was read at that commit.
**Epic**: #1519 (Stage: Spec; Horizon: Now — founder 2026-11-19, "UI redesign is now P0").
**Moved out**: the signed-in browsers block (#1096) was cut from this package by the chief on 2026-11-20 at 05:08Z (founder answer pending; the founder can bring it back). It is now `docs/internal/specs/signed-in-browsers-spec.md`, with its requirement numbers kept. Settings ships with the Sign-in section hidden.
**Riders** (chief-confirmed 2026-11-20): #1154, #346, #1153, #1596, #1940, #1265 (touched, not resolved), #1560, #1641, #1742, #853 (narrow, security-lead look), #936, #995 (Settings endpoints only). Moved with #1096: #689, #1595, #1146, #598. Cut: #1145, #1917. Details: "Riders" below.
**Agent tool field table**: no agent-managed object (plan, task, goal, scheduled task, agent) changes. Settings values are user settings; the round-1 review showed the old "no tool changes" claim was too broad, because the #936 rider changes what `set_config` refuses. The field-by-field table is C-7 (Performance values; its second table: Context, Active transcriber, Sandbox, egress proxy and CLI runner values) plus: `session_days`, `memory_retros_days`, `memory_retros_forever` — user-only, because they delete data on a schedule; `set_config` cannot write `storage.*` (not in `knownConfigPrefixes`), refusal pinned by `TestSettingsAgentWriteParity` (FR-101); `keep_everything_forever` / `disabled` — config-file only, no write path at all. No new agent write path is added.

---

## Overview

Settings today is 13 tabs in one long row, with six different ways of saving, six different ways of saying "restart needed", the same retention value editable in two places with different rules, and a device-pairing tab that promises things that do not exist.

This feature rebuilds Settings as **11 sections in 3 labelled groups** (AI & agents, Security, System) listed down the left side, one section at a time on a phone. It applies **one save rule** everywhere (values wait for **Save changes**; buttons act at once), **one password prompt per Save**, and **one restart pattern** (a "Needs restart" badge, a "Restart now?" window right after such a Save, then one banner). It splits retention into two settings in one place — chat history and memory, with "Keep forever" for memory — and moves God Mode, voice input, verbose chat and the old Models and Performance values to where people look for them. It removes what no longer exists (backup and restore) and hides what does not work (device pairing) or is not in this package (Sign-in).

The signed-in browsers list (#1096) is not in this package: it moved to `signed-in-browsers-spec.md`, and Settings ships with its Sign-in section hidden.

The server changes are small and named: **B2** (one complete list of saved changes that wait for a restart), **B3** (one write path per retention setting), **B4** (one password prompt per Save — decided below, no server change), **B6** ("Keep forever" for memory), **B7** (deleting a stored key needs a confirm, not a password — S-Q11 B; not in the design notes, found in the code). Round 1 adds: a retention guard on `PUT /config` by effect, separate read and write retention schemas, failed hot applies in the restart list, and the #936 Performance guard (C-7).

---

## Existing Codebase Context

GitNexus MCP tools are not connected in this session, so every fact below comes from Grep and Read at `c2f8f8e735` (the fallback the repo rules allow). Risk levels are therefore **Inferred** from a Grep caller sweep, not from a GitNexus `impact` run; backend-lead runs `impact` before editing.

### Symbols Involved

| Symbol | Role | Context |
|---|---|---|
| `src/routes/_app/settings.tsx::SETTINGS_TABS`, `::settingsSearchSchema` | modifies | 13-value `?tab=` enum; unknown values fall back to Providers; `focus` accepts only `god-mode` |
| `src/components/screens/SettingsScreen.tsx::SettingsScreen` | replaces | Tab row; Devices only when pairing is enabled |
| `src/components/settings/*Section.tsx` | modifies / splits | One component per old tab (inventory §1–13) |
| `src/components/settings/useStepUp.tsx` | modifies | Password-prompt flow used by Security, Sandbox, Performance, God Mode, Providers, Integrations |
| `src/components/settings/GatewayRestartModal.tsx` | reuses | Restart window; also the "Restart now?" window after a Save |
| `src/lib/api/config.ts` (`frontendToRawConfig` mapping at lines 321–327) | modifies | Today writes `data.session_retention_days` → `storage.retention.session_days` through `PUT /config` |
| `pkg/gateway/rest_pending_restart.go::RestartGatedKeys`, `::HandlePendingRestart` | modifies (B2) | The only aggregated restart list; lacks the filesystem model and the shell network proxy |
| `pkg/gateway/rest_memory_settings.go` (validation at 117–122, `setRetentionFields`) | modifies (B3) | Writes `session_days` and `memory_retros_days`; accepts any value ≥ 0 |
| `pkg/gateway/rest_retention.go` | deletes GET/PUT (B3) | `/security/retention`; no SPA caller (`src/lib/api/config.ts:349` comment) |
| `pkg/gateway/rest_config.go::updateConfig`, `::protectedAgentDefaultsFingerprint`, `::checkProtectedAgentDefaultsUnchanged` | modifies (B3, #936) | Merges one level deep; the by-effect guard pattern the retention and Performance guards copy (C-3, C-7) |
| `pkg/gateway/blocked_paths.go::collectPaths`, `::matchBlockedPath` | unchanged | Leaf-path matcher; cannot see a replaced parent (why C-3 guards by effect) |
| `pkg/gateway/rest_context_settings.go::putContextSettings` | modifies (FR-021) | Copies `cfg.Context` before the write lock, then writes every field |
| `pkg/gateway/rest_third_party_settings.go::putThirdPartyAgentSettings` | reuses | Whole-section replace; `saved_not_refreshed` after a refresh failure |
| `pkg/gateway/rest_prompt_guard.go::putPromptGuard`, `rest_rate_limits.go` | modifies (FR-062) | Answer `requires_restart: true` when the hot apply fails |
| `src/components/settings/GatewayRestartModal.tsx::pollUntilOnline` | modifies (FR-063) | Accepts the first 2xx from `/health` as "restarted" |
| `pkg/sysagent/tools/config.go::performanceOnlyConfigKeys`, `::knownConfigPrefixes` | modifies (#936) | `tools.` is writable; `performance.` and `planning.` are not known prefixes |
| `pkg/config/config.go::OmnipusRetentionConfig`, `pkg/config/config_retention.go` | modifies (B6) | `SessionDays`, `Disabled`, `MemoryRetrosDays`; no memory "forever" |
| `pkg/gateway/retention_goroutine.go` (`IsDisabled` checks at 114 and 267; `RetentionMemoryRetrosDays` at 271) | modifies (B6) | `disabled` stops BOTH sweeps today |
| `pkg/agent/memory_adapter.go::SearchRetros`, `pkg/agent/memory.go::MemoryStore.ReadRetros` | modifies only if FQ-1 = A | Recall reads at most 365 days back (hard-coded `ReadRetros(365)`, clamp in `ReadRetros`) |
| `pkg/gateway/rest_settings.go::deleteCredential` | modifies (B7) | Calls `requireReAuth` today |
| `pkg/gateway/rest_integrations_auth.go::reauthStore`, `::requireReAuth` | unchanged (B4) | Single-use 5-minute consent token |

### Impact Assessment

| Symbol Modified | Risk Level | d=1 Dependents | d=2 Dependents |
|---|---|---|---|
| `RestartGatedKeys` (B2) | MEDIUM | `HandlePendingRestart`; mirrored by `pkg/sysagent/tools/config.go::isRestartRequired` | Gateway restart banner, `set_config` result |
| `updateConfig` guards (B3, #936) | MEDIUM | `PUT /config` | every SPA `PUT /config` caller (`src/lib/api/config.ts`); a wrong comparison refuses ordinary saves |
| `retention_goroutine.go` sweep (B6) | MEDIUM | nightly sweeps | data deletion — a wrong condition deletes memory |
| `MemoryStore.ReadRetros` (FQ-1) | MEDIUM | `SearchRetros` → `recall_memory` | agent recall; touches `pkg/agent`, so hands-on UAT on the cut commit is mandatory |
| `deleteCredential` (B7) | LOW | `DELETE /credentials/{key}` | Keys & secrets; security-lead review (focus area `pkg/credentials` neighbour) |

No HIGH row remains after the #1096 cut. security-lead still reviews B7, the `PUT /config` guards and the #853 health change (focus areas).

---

## Contract Changes (contract-first — Hard Constraint #8)

Every new or changed shape below goes into `contracts/` before any Go or TypeScript is written; backend-lead edits the files and regenerates with `scripts/gen-contracts.sh`. This spec proposes the shapes; it does not hand-write generated code.

### C-1 — moved

The signed-in browsers contract (old C-1, [#1096]) moved to `docs/internal/specs/signed-in-browsers-spec.md` on 2026-11-20 (chief's cut at 05:08Z; founder answer pending). This package changes no auth route, no cookie and no WebSocket handler.

### C-2 B2 — one combined pending-restart list

**Decision:** extend the existing `GET /config/pending-restart` (`operationId: getPendingRestart`); no new endpoint. Its entries already carry the saved value (`persisted_value`) and the running value (`applied_value`) — `contracts/components/schemas/PendingRestartEntry.yaml`.

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `PendingRestartEntry` | changed (description only) | `contracts/components/schemas/PendingRestartEntry.yaml` | `key` description lists the restart-gated keys, adding `sandbox.filesystem_model` and `tools.exec.enable_proxy` |
| `/config/pending-restart` | changed (description only) | `contracts/openapi.yaml` | Names the full key list; states that God Mode and hot-reloaded keys never appear |

Code facts that correct the design notes (code wins):

| Design-notes claim (§5 B2, wireframe badges) | Code at `c2f8f8e735` | This spec |
|---|---|---|
| The Gateway list misses sandbox mode | `sandbox.mode` is already in `RestartGatedKeys` (`rest_pending_restart.go:35`) | No change for sandbox mode |
| The list misses the filesystem model | Not in `RestartGatedKeys`; `PUT /security/sandbox-config` does not set `requires_restart` for it (`rest_sandbox_config.go::respond`, only mode, allowed paths and workspace guard); the policy is built at boot (`sandbox_apply.go`, `DefaultPolicyForModel`) | Add `sandbox.filesystem_model` (Inferred restart-gated; RED proves the running value does not change on a reload) |
| The list misses the network proxy | `tools.exec.enable_proxy` is read when the agent loop is built (`pkg/agent/loop_construct.go:354`) and is not in the list | Add `tools.exec.enable_proxy` (Inferred; RED proves it) |
| Prompt injection defense needs a restart (wireframe "Needs restart" badge) | Hot-reloaded: `PUT` answers `requires_restart: false` unless the reload fails (`rest_prompt_guard.go`), and the key is deliberately excluded (`rest_pending_restart.go:20`) | **No "Needs restart" badge** on Defense level. A failed reload still says so through the write response (FR-034) |
| — | God Mode is deliberately excluded (`rest_pending_restart.go:39-50`, O14) | Stays excluded; God Mode keeps its own restart window |


**Addition (round 1, review MAJ-006): failed hot applies join the same list.** A setting that applies without a restart can still fail to apply after it was saved (for example `rest_prompt_guard.go::putPromptGuard` answers `saved: true, requires_restart: true` when the reload fails). Today such a key never appears in `/config/pending-restart`, so the warning vanishes after the first window. Decision:

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `PendingRestartEntry` | changed | `contracts/components/schemas/PendingRestartEntry.yaml` | New required `reason`: `restart_required` (a `RestartGatedKeys` key whose saved value differs from the boot value — today's entries) or `apply_failed` (a hot-reloaded key whose last save did not apply). For `apply_failed`, `applied_value` is the value still in force, or `null` when unknown |

The gateway keeps, in memory, the keys whose last hot apply failed since boot, with the saved value. A later successful apply of that key, or a restart, removes the entry. Every Settings-section writer that answers `requires_restart: true` after a failed apply adds its key (today `rest_prompt_guard.go` and `rest_rate_limits.go`); the #995 rider makes `PUT /config` (on a `configRefreshError`), `PUT /settings/context` (on a `configRefreshError` or a failed `triggerReloadAndWait`) and `PUT /settings/third-party-agents` (its existing `saved_not_refreshed`) record it the same way, with the response contract of C-6 (round 2, review R2-MAJ-006), and `PUT /performance` records its existing `PerformancePendingApply` state. **The record is made inside the config write lock, before the response is sent** (so a lost response can never leave a saved, unapplied key unlisted), and the entry's `persisted_value` is read from `config.json` at `GET` time, as today's entries are (`HandlePendingRestart` loads the file with `config.LoadConfig`), never from a copy kept in memory. The entry key is the dotted config path of each stored field (for example `gateway.log_level`, `context.mcp_result_cap`, `sandbox.third_party_agents.clis.codex.path`). God Mode stays out. The banner therefore survives Later, a section switch and a page reload, and its Restart now button is always the focus target after Later (FR-035).

**Restart completion (round 1, review MAJ-007; round 2, review R2-MAJ-003).** No contract change. Every check the page makes is a **same-origin** request: the page's Content Security Policy allows `connect-src 'self'` only (`embed.go::spaBaseContentSecurityPolicy`) and `rest.go::setCORSHeaders` grants credentialed access only to the configured origin, so the page cannot read a different port. Neither policy is widened for this. When the page's own address keeps serving the gateway (no host or port change, or a proxy), the screen proves a restart finished by reading its own `GET /about` (`AboutResponse.uptime_seconds`, `pid`): done means the gateway answers and its `uptime_seconds` is smaller than the seconds since Restart now was pressed (so an old process that is still answering never counts), and then `GET /config/pending-restart` no longer lists the restarted keys. `GatewayRestartModal.tsx::pollUntilOnline` (first 2xx from `/health`) is not enough and changes. When the restart moves the gateway away from the page's own address, the page cannot see the new process; the hand-over is a top-level navigation chosen by the person, and the check runs on the new address after it loads (FR-063).

### C-3 B3 + B6 — retention: one write path, protected against every other writer, and "Keep forever" for memory

**Decision.** One dedicated endpoint owns all three editable retention values: **`/settings/retention`**. The design notes keep chat history on the generic `PUT /config` (no range check) and memory on `PUT /settings/memory` (any value ≥ 0, `rest_memory_settings.go:117-122`). Moving both gives: (1) the server enforces 1–365 and 1–730 itself; (2) the Data & retention Save is one request; (3) one write path per setting at the server, not only in the screen. The unused `GET/PUT /security/retention` is deleted, as the design notes say. `POST /security/retention/sweep` stays unchanged.

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `RetentionSettings` | new | `contracts/components/schemas/RetentionSettings.yaml` | **Read** shape: response of GET and PUT. Every field required. No range on the day counts, so a hand-edited value can be shown and repaired (review MAJ-003) |
| `RetentionSettingsUpdate` | new | `contracts/components/schemas/RetentionSettingsUpdate.yaml` | **Write** shape: PUT body. Ranges enforced; read-only member absent; at least one field |
| `MemorySettings` | changed | `contracts/components/schemas/MemorySettings.yaml` | Remove `session_days` and `memory_retros_days` |
| `RetentionConfig`, `RetentionUpdateRequest`, `RetentionUpdateResponse` | deleted | `contracts/components/schemas/` + `openapi.yaml` refs at lines 1512, 1797, 1995 | Only `/security/retention` GET/PUT used them (verified by Grep over `contracts/`) |
| `/security/retention` GET and PUT | deleted | `contracts/openapi.yaml` | handlers in `rest_retention.go` removed |
| `PUT /config` description | changed | `contracts/openapi.yaml` | States that a write changing the effective retention values is refused with 400 |

Proposed `RetentionSettings.yaml` (read):

```yaml
title: RetentionSettings
type: object
description: >
  How long chat history and memory are kept (Settings → Data & retention), as
  stored. Day counts are returned as stored, even outside the ranges that
  RetentionSettingsUpdate enforces, so the screen can show and repair them.
  0 or a negative stored value means "use the default" (the existing
  config_retention.go accessors) and is returned as the effective default.
additionalProperties: false
required: [session_days, memory_retros_days, memory_retros_forever, keep_everything_forever]
properties:
  session_days:
    type: integer
    description: Days chat transcripts are kept. Default 90. storage.retention.session_days.
  memory_retros_days:
    type: integer
    description: Days the agents' retrospectives (reflections on past chats) are kept. Default 180. storage.retention.memory_retros_days.
  memory_retros_forever:
    type: boolean
    description: Keep memory forever; the nightly sweep deletes no retrospective. Default false. storage.retention.memory_retros_forever (new key).
  keep_everything_forever:
    type: boolean
    description: >
      True when config.json sets storage.retention.disabled, an older,
      config-file-only switch that stops BOTH sweeps.
```

Proposed `RetentionSettingsUpdate.yaml` (write):

```yaml
title: RetentionSettingsUpdate
type: object
description: Partial update of the retention settings. At least one field.
additionalProperties: false
minProperties: 1
properties:
  session_days:     { type: integer, minimum: 1, maximum: 365 }
  memory_retros_days: { type: integer, minimum: 1, maximum: 730, description: Validated whenever sent, also while memory_retros_forever is true. }
  memory_retros_forever: { type: boolean }
```

**Endpoint.** `GET /settings/retention` (`getRetentionSettings`) returns `RetentionSettings`. `PUT /settings/retention` (`updateRetentionSettings`) takes `RetentionSettingsUpdate`, applies the sent fields to the stored values read **inside** the config write lock, and answers 200 with `RetentionSettings`. Auth: `withAuth` + `RequireNotBypass`, like `/settings/memory`; no password. **Validation is unconditional** (review MAJ-004): the handler decodes strictly (unknown fields refused) and checks every range, type and the empty body itself, whether or not `gateway.validate_inbound` is on; schema validation, when on, is a second layer, not the only one. 400 names the field and its range (messages in Machine-Verifiable Constraints).

**Every other writer is refused, by effect** (review CRIT-002). Adding `storage.retention` to `blockedPaths` would not catch `{"storage": null}` or `{"storage": {"other": 1}}`: `blocked_paths.go::collectPaths` records only `storage`, and `rest_config.go::updateConfig` replaces a top-level key's value (it merges only one level deep). So the guard compares **effective values**, the pattern `updateConfig` already uses for the tool-iteration limit (`protectedAgentDefaultsFingerprint` / `checkProtectedAgentDefaultsUnchanged`): before the merge, decode the stored map into `config.Config` and take the four effective retention values (`RetentionSessionDays()`, `RetentionMemoryRetrosDays()`, `memory_retros_forever`, `IsDisabled()`); after the merge, decode again the same way (so every key spelling encoding/json accepts is covered); if any of the four differs, return a `requestRefusalError` → **400** `retention settings can only be changed through PUT /api/v1/settings/retention`, and nothing is written. A body that leaves the effective values as they are (an unrelated sibling, a permitted multi-section write that echoes the stored retention values) is accepted. If the merged result does not decode into `config.Config` at all (for example `{"storage":"x"}`), the guard refuses with 400 `this write does not decode: <cause>` and nothing is written. `storage.retention` is **not** added to `blockedPaths`. `set_config` already cannot write `storage.*` (not in `knownConfigPrefixes`).

**The existing generic-config fences stay exactly as they are** (round 2, review R2-MAJ-001). `updateConfig` refuses a body carrying `providers` or a credential-like key with 403 before any merge, then `blockedPaths` (`sandbox`, `security`, `credentials`, `gateway.users`, …) with 403. A raw echo of `GET /config` carries `providers` (`getConfig` serializes `config.Config` and does not strip it), so it is refused with 403 by the credential fence **before** the retention guard runs; it still leaves Keep forever on, but it is evidence for that fence, not for the retention guard. No requirement in this spec asks for a whole-`GET` echo to answer 200, and none may be met by loosening a fence. The guard sits **after** those fences and next to `checkProtectedAgentDefaultsUnchanged`. The bytes it decodes are the merged map that `writeConfigJSONLocked` / `prepareConfigJSONLocked` serialize with `json.MarshalIndent` (sorted keys), so the guard sees exactly what a fresh load of the written file will see.

Server meaning of the stored values (B6):

| Stored state | Chat sweep | Memory sweep | Source |
|---|---|---|---|
| `disabled: true` (legacy, config-file only) | off | off | unchanged — `retention_goroutine.go:114,267` |
| `memory_retros_forever: true` | by `session_days` | **off** | new |
| `session_days` 0, negative or absent | 90 days | — | unchanged default (`config_retention.go::RetentionSessionDays`) |
| `memory_retros_days` 0, negative or absent | — | 180 days | unchanged default (`::RetentionMemoryRetrosDays`) |
| a stored day count above the new range (hand-edited) | applied as stored | applied as stored | GET returns it; the screen shows it with the range error and blocks Save until fixed (FR-046) |

Recall window: see **FQ-1**. Today recall reads at most 365 days of retrospectives (`memory_adapter.go::SearchRetros` calls `ReadRetros(365)`; `ReadRetros` clamps to 365). FR-050 is written for the recommended answer and is marked pending.

### C-4 B4 — one password prompt per Save: **no server change** (option 2 of design-notes §5)

**Decision: the screen asks the server for one consent token per Save, from the one typed password, and every Save needs exactly one.** The single-use 5-minute token (`rest_integrations_auth.go::reauthStore.consume`) stays as it is.

**Reason.** The design notes assumed one Save could touch two password-protected endpoints ("Auto-approve and Performance limits"). In the approved wireframe a Save covers **one section** (leaving a section with unsaved changes asks first — `app.js::go` → `guard`), and in the code every section has **at most one** password-protected endpoint:

| Section | Password-protected write (code) | Other writes in the same Save (no password) |
|---|---|---|
| Providers | `PUT /integrations/providers/{id}` for **Active transcriber** (`rest_integrations_roles.go:365` calls `requireReAuth`) | `PUT /providers/default-model` (no `requireReAuth` in `rest_default_model.go`) |
| Limits | `PUT /performance` (`rest_performance.go:203`) | `PUT /settings/context` |
| Protection | `PUT /security/sandbox-config` for Auto-approve (`rest_sandbox_config.go:367`) | prompt guard, rate limits |
| Sandbox & network | `PUT /security/sandbox-config` (mode, filesystem model, workspace limit, allowed folders, internal addresses) | `PUT /config` for the network proxy |
| Every other section | none | — |

So the Save sends all of a section's password-protected values in **one** request to that one endpoint, and one token covers it. A multi-use token (option 1) would weaken the one-consent-one-write property every gated endpoint has today, for a case that does not occur. Option 2's weakness — keeping the typed password in browser memory to mint a second token — does not arise either: the password is used for one `POST /auth/reauth` and dropped (FR-028). A guard test pins the "one password-protected endpoint per section" fact (FR-030), so a later section that needs two fails loudly and reopens this decision. The reauth rate limit (10 per minute per IP, `rest_auth.go:222`) is never stressed by a Save.

Wireframe correction (code wins): **Active transcriber** needs the password (`rest_integrations_roles.go:365`); the wireframe row (`app.js:122`) has no Password badge. It gets one.

One edge stays: the max-tool-calls lowering can still answer 409 after the token is used (`rest_performance_max_tool_iterations.go::freshDriftError` runs inside the locked write). The drift check before the token (`preCheckMaxToolIterationsDrift`) catches the usual case. When the late 409 happens, the screen shows the fresh list of affected agents and asks for the password again. A changed list is a new consent, so a second prompt is correct (FR-031).

No contract change.

### C-5 B7 — deleting a stored key asks for a confirm, not a password (S-Q11 B)

**Code fact the design notes miss:** `DELETE /credentials/{key}` calls `requireReAuth` (`rest_settings.go::deleteCredential`), under an accepted ADR ([ADR-022](../architecture/ADR-022-credential-vault-reauth.md), whose reason is "revoke a live provider/channel key mid-session"). S-Q11 B (confirm only) therefore needs a **server change**, not just a screen change.

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `DELETE /credentials/{key}` description | changed | `contracts/openapi.yaml` | No `X-Reauth-Token`; still `withAuth` + the existing admin and bypass guards; still audited |

Set (`POST /credentials`) and rotate (`POST /credentials/rotate`) keep the password. ADR-022 gets a dated correction at landing (DOC-012). FQ-2 asks the founder to confirm S-Q11 B now that this server-side effect is known; the spec is written for B.

### C-6 Save-path facts the screen must follow (round 1, review MAJ-001, MAJ-002, MAJ-005)

**Third-party agents is a whole-section write; it stays one.** `ThirdPartyAgentSettings` replaces the whole section ("a CLI key absent from `clis` is saved as {path: "", preset_args: ""}… No revision and no 409: the last save wins"), and `CliPreset` requires both fields. No contract change: the Save for this section sends **all three rows**, each built from the values loaded when the section opened plus this tab's drafts. The universal "send only changed fields" rule (FR-015) does not apply here, and two tabs saving this section at once is last-save-wins for the whole section, as the schema already states. After a Save the section re-reads its values. This is written down, not hidden: FR-022, Non-Behaviors, DOC-003.

**Section writers merge under the lock.** `rest_context_settings.go::putContextSettings` builds `next := cfg.Context` **before** `safeUpdateConfigJSON` and writes every context field from that early copy, so two Saves that change different fields can lose one change. Decision: every writer the Save bar uses MUST apply only the fields present in the request to the stored values read **inside** the write lock (FR-021). Known to need the change: `putContextSettings` (verified). backend-lead checks the other Save-bar writers (`/performance`, `/settings/memory`, `/security/sandbox-config`, `/security/prompt-guard`, `/security/rate-limits`, `/providers/default-model`, `/settings/retention`) and records each as "already merges under the lock" or "changed", with the FR-021 test. Same-field saves: last writer wins. A list value (Allowed folders, Model overrides, internal network addresses) is one field and is replaced as a whole.

**A failed response does not prove nothing was saved.** `rest_config.go::updateConfigJSONLocked` can fail with `configRefreshError` **after** `config.json` is written ("the write stands on disk"); `putThirdPartyAgentSettings` already answers `saved_not_refreshed` for that case; `PUT /performance` has `PerformancePendingApply`. So the screen tracks four outcomes per write (FR-020): not saved, saved and in use, saved but not in use, and unknown.

**Write outcome contract (round 2, review R2-MAJ-006).** Contract change, descriptions only: no new schema. `ErrorResponse.code` already defines `saved_not_refreshed` ("a 500 whose write reached disk but whose running settings could not be refreshed") for Third-party agents; this package extends the same code to the two #995 writers and adds one optional detail. Exact responses:

| Endpoint | Stage | Status | Body (generated type) | `code` | Screen outcome (FR-020) |
|---|---|---|---|---|---|
| `PUT /config` | refused before any write: credential field or blocked path | 403 | `ErrorResponse` | none (unchanged) | not saved |
| `PUT /config` | refused before any write: retention or Performance guard (C-3, C-7), decode failure, protected agent defaults | 400 | `ErrorResponse` | none | not saved |
| `PUT /config` | failed before `config.json` was written (read, parse, serialize, atomic write) | 500 | `ErrorResponse` | none | unknown → saved-state check |
| `PUT /config` | written, then `configRefreshError` | **500** (was a generic 500) | `ErrorResponse` | **`saved_not_refreshed`**, `details.keys`: the dotted keys the body changed | saved, not in use |
| `PUT /config` | written and refreshed | 200 | the `GET /config` map (unchanged) | — | saved and in use |
| `PUT /settings/context` | validation refusal | 400 | `ErrorResponse` | none (unchanged) | not saved |
| `PUT /settings/context` | failed before `config.json` was written | 500 | `ErrorResponse` | none | unknown → saved-state check |
| `PUT /settings/context` | written, then `configRefreshError`, or `triggerReloadAndWait` fails | **500** (today "failed to update context settings" or "context settings written but the reload failed") | `ErrorResponse` | **`saved_not_refreshed`**, `details.keys` (`context.<field>` paths) | saved, not in use |
| `PUT /settings/context` | written and reloaded | 200 | `ContextSettings` (unchanged, closed schema) | — | saved and in use |
| `PUT /settings/third-party-agents` | refusal (`cli_path_invalid`, `cli_preset_invalid`, schema) | 400 | `ErrorResponse` (unchanged) | as today | not saved |
| `PUT /settings/third-party-agents` | written, refresh failed | 500 (unchanged) | `ErrorResponse` | `saved_not_refreshed` (unchanged) + new `details.keys` | saved, not in use |
| `PUT /security/prompt-guard`, `PUT /security/rate-limits` | written, hot apply failed | 200 (unchanged) | their existing response with `requires_restart: true` | — | saved, not in use |
| `PUT /performance` | written, apply pending | 200 (unchanged) | `PerformanceSettings` with `pending_apply` | — | saved, not in use |

Every `saved_not_refreshed` answer and every saved-but-not-applied 200 above also adds `apply_failed` entries for its keys to `GET /config/pending-restart` (C-2 addition), inside the write lock, before answering. Files changed: `contracts/components/schemas/ErrorResponse.yaml` (`code` description names the three endpoints; `details` description adds `keys`, an array of dotted config keys, for `saved_not_refreshed`) and `contracts/openapi.yaml` (the `PUT /config` and `PUT /settings/context` operations list their 400/403/500 responses with `ErrorResponse` and say when `saved_not_refreshed` is used). The generated client is regenerated; the screen reads `code` and `details.keys` only through the generated `ErrorResponse` type. A recognized saved-not-applied signal takes precedence over the HTTP status: a 500 with `saved_not_refreshed` is **saved, not in use**, never "unknown".

**Where the saved value is read (round 2, review R2-MAJ-002).** `GET /config`, `GET /settings/context` and `thirdPartySettingsView` read the **running** settings (`agentLoop.GetConfig()`), which lag behind `config.json` after a failed refresh, so they alone can never prove that a value was not saved. The saved-state check therefore uses one source that reads the file: `GET /config/pending-restart`. Rule, which holds because of the recording promise above and because a restart loads `config.json`: **for a key the gateway lists, the saved value is the entry's `persisted_value` (read from `config.json`); for a key it does not list, the saved value equals the running value.** The screen applies this rule only to keys written by the Save-bar writers in the table above (all of which record failed applies after this package); it never concludes "not saved" for any other writer. No new endpoint is added.

### C-7 #936 rider — Performance values have one write path

Issue #936 (accepted as a rider): the Performance values can be written through `PUT /config` and `set_config` without the password. Decision, by effect like C-3:

| Value (config key) | Settings path | `PUT /config` | `set_config` |
|---|---|---|---|
| Max parallel agents (`performance.max_parallel_agents`) | Limits → `PUT /performance` (password) | refused when the effective value would change (400, names `PUT /api/v1/performance`) | refused today: `performance.` is not in `knownConfigPrefixes`; pinned by test |
| Tries per goal (`planning.goal_max_rounds`) | same | same | refused today (`planning.` not a known prefix); pinned by test |
| Idle reminder quiet window (`planning.goal_reminder_quiet_seconds`) | same | same | same |
| Check-in switch (`planning.goal_stalled_checkin_enabled`) | same | same | same |
| Check-in interval (`planning.goal_stalled_checkin_minutes`) | same | same | same |
| Load tools on demand (`tools.manifest.compressed`) | same | same | **newly refused**: added to `performanceOnlyConfigKeys` (the `tools.` prefix is writable today) |
| Max tool calls per turn (`agents.defaults.max_tool_iterations`) | same, with lowering consent (#904) | refused (existing `checkProtectedAgentDefaultsUnchanged`) | refused (existing `performanceOnlyConfigKeys`) |

Security reason for "user only": these values bound how much an agent may run; an agent or a stolen token must not raise its own limits without the person's password. The `set_config` tool description gains one sentence naming these values and pointing to Settings → Limits. Guard test `TestSettingsAgentWriteParity` (FR-101) enumerates every value this spec lets the person edit and fails unless each has either an agent path or a listed, tested refusal.

**Other values a person edits that agents cannot write (team-lead ruling 2026-11-21, closes CHECK R3-02).** `set_config` refuses each of these today; no agent write path is added, and `TestSettingsAgentWriteParity` (FR-101) pins each refusal through the real tool and checks the stored value is unchanged:

| Value (config key) | Settings path | `set_config` today | User-only reason |
|---|---|---|---|
| The seven Context values (`context.mcp_result_cap`, `context.builtin_success_cap`, `context.builtin_failure_cap`, `context.tool_result_share_fraction`, `context.ingest_bound_bytes`, `context.default_context_window`, `context.model_overrides`) | Context → `PUT /settings/context` (FR-074) | refused, "unknown config key" (`context.` is not a known prefix) | They bound how much tool output and history an agent itself receives; like the Performance values, an agent must not change its own limits |
| Active transcriber (`integrations.active_transcriber`) | Providers → `PUT /integrations/providers/{id}` (password) | refused, "unknown config key" | It chooses which outside service receives the person's voice recordings; that is the person's choice, behind the password |
| Sandbox values (`sandbox.auto_approve`, `sandbox.prompt_injection_level`, `sandbox.mode`, `sandbox.filesystem_model`, `sandbox.workspace_path_guard`, `sandbox.allowed_paths`, `sandbox.ssrf.allow_internal`, `sandbox.rate_limits.*`, `sandbox.god_mode`, `sandbox.tool_policies`) | Sandbox section | refused, "not writable via this tool" (the `sandbox.` namespace) | The sandbox is the boundary that confines agents; an agent must never loosen its own confinement |
| Egress proxy switch (`tools.exec.enable_proxy`) | Sandbox section | refused, "not writable via this tool" | Part of the same boundary: it controls how an agent's shell commands reach the network |
| CLI runner paths and preset arguments (`sandbox.third_party_agents.clis.<codex, claude-code, opencode>.path` and `.preset_args`) | Third-party agents → `PUT /settings/third-party-agents` | refused, "not writable via this tool" | They name the program the app starts for a worker; an agent that could change them could run any program outside its tool policy |

### Collisions checked

New names `RetentionSettings` and `RetentionSettingsUpdate` match no existing file in `contracts/components/schemas/` (562 files listed). The new `reason` enum values of `PendingRestartEntry` are new. On screen, "Clear all sessions" (Data & retention) means chat sessions; its wording changes to "Clear all chat history" (FR-108).

## API and Data

| Endpoint | Change | Contract |
|---|---|---|
| `GET /config/pending-restart` | behaviour (B2) + changed schema | two more keys; `reason` (`restart_required` / `apply_failed`) |
| `GET/PUT /settings/retention` | new (B3, B6) | `RetentionSettings` (read), `RetentionSettingsUpdate` (write) |
| `GET/PUT /settings/memory` | changed (B3) | no retention fields; retired fields refused unconditionally |
| `GET/PUT /security/retention` | deleted (B3) | — |
| `PUT /config` | behaviour (B3, #936, #995) | refuses any write that changes effective retention or Performance values (400); a failed refresh after the write answers 500 `saved_not_refreshed` with `details.keys` (C-6 outcome table) |
| `PUT /settings/context` | behaviour (FR-021, #995) | merges under the lock; a failed refresh or reload after the write answers 500 `saved_not_refreshed` with `details.keys` |
| `PUT /settings/third-party-agents` | behaviour (#995 recording only) | whole-section replace, used as such (C-6); its existing `saved_not_refreshed` gains `details.keys` and an `apply_failed` record |
| `ErrorResponse` | changed (descriptions) | `saved_not_refreshed` used by three endpoints; `details.keys` |
| `DELETE /credentials/{key}` | behaviour (B7) | no password token |
| `GET /audit-log` handler | behaviour (#1742) | reads the writer's directory |
| `set_config` tool | behaviour (#936) | refuses `tools.manifest.compressed` |

Stored data, behavioural level: one new retention switch (`storage.retention.memory_retros_forever`, default false). In gateway memory only: the list of failed hot applies since boot. Nothing else new is stored. The screen keeps unsaved drafts in page memory only; nothing is written to browser storage except what exists today (verbose chat, now shown in Profile).

---

## User Stories & Acceptance Criteria

### User Story 1 — Find any setting quickly, and keep old links working (Priority: P1)

A person running Omnipus wants to find a setting by the job it does ("my models", "what agents may do", "the server") instead of scanning 13 tabs in a row. Today the tabs give no hint of what belongs together, a reload jumps back to the first tab (#1154), and links in other screens point at tab names that will no longer exist.

**Why this priority**: every other story happens inside this structure; without it nothing else is reachable.

**Independent Test**: open Settings with no address parameters, with each old `?tab=` value, with the God Mode link and on a 390 px wide screen; compare what opens against the tables in FR-001 and FR-003.

**Acceptance Scenarios**:

1. **Given** a signed-in person, **When** they open Settings with no section named, **Then** the list shows 11 sections in three groups (AI & agents: Providers, Memory & recaps, System agents, Third-party agents, Limits; Security: Protection, Sandbox & network, Keys & secrets; System: Gateway, Data & retention, About) and Providers is open; Sign-in is hidden in this package.
2. **Given** Sandbox & network is open, **When** the page is reloaded, **Then** Sandbox & network is open again.
3. **Given** a link made for the old tabs (for example `?tab=performance`), **When** it is opened, **Then** the section that now holds those settings opens (Limits).
4. **Given** the "No context length" link for a provider and model, **When** it is followed, **Then** Limits opens with Model overrides expanded and a new row filled with that provider and model.
5. **Given** God Mode is on and the sidebar pill shows, **When** the pill is clicked, **Then** Protection opens with the God Mode button focused.
6. **Given** an old backup or restore link, **When** it is opened, **Then** Data & retention opens with the notice "Backup and restore were removed."
7. **Given** an old link to the Chat tab, **When** it is opened, **Then** Profile opens at Chat display.
8. **Given** the search box, **When** the person types "web search", **Then** the result says it moved to Skills & Tools and links there.
9. **Given** a 390 px wide screen, **When** the person taps Gateway in the list, **Then** only Gateway shows, with a "Settings" back button.
10. **Given** the search box, **When** the person types a word no setting contains, **Then** "No settings match" shows with the typed word.
11. **Given** the person moved from Gateway to Limits, **When** they press the browser's Back button, **Then** Gateway opens.

### User Story 2 — Change several values and save them together (Priority: P0)

A person wants to know whether a change is saved. Today six save models coexist: silent autosave, a Save button, a delayed confirm, instant clicks, a restart dialog and password dialogs. With this story every value waits for **Save changes**, and every button acts at once.

**Why this priority**: it is the fix for the inventory's problem 2 and the rule every section depends on.

**Independent Test**: change values in one section, then Save, Discard, leave, fix an invalid value and force one write to fail; check the Save bar text and the stored values after each step.

**Acceptance Scenarios**:

1. **Given** Gateway shows saved values, **When** the person changes Port, **Then** a Save bar appears saying "1 unsaved change" with Discard and Save changes, and nothing is stored yet.
2. **Given** one unsaved change, **When** the person presses Save changes, **Then** the value is stored, the bar says "Saved at" and the time, and the value is still there after opening another section and coming back.
3. **Given** two unsaved changes, **When** the person presses Discard, **Then** both fields show their last saved values and the bar disappears.
4. **Given** Port holds 80, **When** the person looks at the Save bar, **Then** Save changes is unavailable, the bar says "Fix 1 value before saving", and the field says "Enter a whole number from 1,024 to 65,535."
5. **Given** the person changed Port and then typed the old value back, **When** they look at the page, **Then** the Save bar is gone.
6. **Given** one unsaved change in Limits, **When** the person clicks another section, **Then** they are asked whether to leave without saving; choosing to stay keeps them in Limits with the change.
7. **Given** two changes going to two different places on the server, **When** one is refused, **Then** the other is shown as saved, the refused one keeps its value and shows the server's own message under its field, and the bar says "1 change could not be saved".
   - In the sandbox section, a refused path that was a new draft in this Save follows FR-015a instead.
8. **Given** Memory & recaps, **When** the person picks a different recap model, **Then** nothing is stored until Save changes.
9. **Given** the person lowered Max tool calls per turn, **When** they press Save changes, **Then** the list of affected agents is shown before anything is stored.
10. **Given** the person chose All interfaces for Bind address, **When** they press Save changes, **Then** they are asked to confirm before anything is stored.
11. **Given** a Save whose write was stored but not put in use, or whose answer never arrived, **When** the result comes back, **Then** the value is shown as "saved, but not in use yet" or checked by reading it back, and is never shown as unsaved while the server holds it.
12. **Given** Third-party agents, **When** the person changes one program and saves, **Then** the other two programs keep their stored values.
13. **Given** two browser tabs change different Limits values, **When** both save, **Then** both values are stored.

### User Story 3 — Type the password once per Save (Priority: P1)

A person saving a protected value wants to type the password once, not once per value.

**Why this priority**: without it the single save rule doubles the friction it was meant to remove.

**Independent Test**: in Sandbox & network change two password-marked values and Save; count password prompts and server writes.

**Acceptance Scenarios**:

1. **Given** two password-marked changes in Sandbox & network, **When** the person presses Save changes, **Then** one password prompt lists both settings by name, and after the right password both are stored.
2. **Given** the password prompt, **When** a wrong password is entered, **Then** the prompt says the password is incorrect, stays open, and the changes stay unsaved.
3. **Given** the password prompt, **When** the person cancels, **Then** nothing is stored and every change, including a typed key in a side panel, is still on the page.
4. **Given** a hosted or desktop install (sign-in through omnipus.ai), **When** a password-marked change is saved, **Then** a plain confirmation appears instead of a password prompt.
5. **Given** Providers, **When** the person changes Active transcriber and saves, **Then** the password is asked once.
6. **Given** a lowering of Max tool calls per turn whose affected agents changed after the password was given, **When** the server reports the new list, **Then** the new list is shown and the password is asked again before saving.
7. **Given** changes that carry no Password badge, **When** they are saved, **Then** no password prompt appears.

### User Story 4 — Know which saved changes wait for a restart (Priority: P1)

A person wants one clear signal when a saved change only takes effect after a gateway restart, and one place to restart.

**Why this priority**: today the signal appears in six shapes and misses some settings, so people believe a security setting is in force when it is not.

**Independent Test**: save each restart-marked setting in turn; check the window, the banner, its list, and that a restart clears it.

**Acceptance Scenarios**:

1. **Given** the person changed Port, **When** they save, **Then** a "Restart now?" window lists Port and offers Later and Restart now.
2. **Given** that window, **When** the person chooses Later, **Then** a banner on every section says "1 saved change waits for a gateway restart" and focus moves to its Restart now button.
3. **Given** the banner, **When** the person chooses Show changes, **Then** each waiting setting is listed with its running value and its saved value.
4. **Given** the person saved a new Filesystem model, **When** the banner is shown, **Then** Filesystem model is in its list.
5. **Given** the person turned the network proxy for shell commands on or off, **When** the banner is shown, **Then** that setting is in its list.
6. **Given** Protection, **When** the person changes Defense level and saves, **Then** no restart badge, window or banner appears, because the change applies at once.
7. **Given** the banner, **When** the restart finishes, **Then** the banner disappears and focus moves to the section heading.
8. **Given** a saved Port change waiting for a restart, **When** the person saves the old Port again, **Then** the banner disappears without a restart.
9. **Given** the person changed Port and opened Settings directly at the gateway's address, **When** they choose Restart now, **Then** they are told the new address first, can open it with one button once the old address stops answering, and Settings at the new address confirms the restart.
10. **Given** a setting that applies at once could not be applied after it was saved, **When** the person chooses Later and later reloads the page, **Then** the banner still lists it as "saved, not applied".

### User Story 5 — Decide how long chats and memory are kept (Priority: P1)

A person wants chat history cleaned up sooner than memory, set in one place, with the option to keep memory forever (S-Q5 B, S-Q13 A). Today the same chat value can be edited in two tabs with different rules.

**Why this priority**: these values delete data on a schedule; a wrong value or a second writer deletes what the person wanted kept.

**Independent Test**: set each value at and beyond its limits through the screen and through the server; run the nightly sweep and check what was deleted.

**Acceptance Scenarios**:

1. **Given** a fresh install, **When** Data & retention opens, **Then** "Keep chat history for" shows 90 days, "Keep memory for" shows 180 days and Keep forever is off.
2. **Given** Data & retention, **When** the person saves 30 days of chat history and 365 days of memory, **Then** both are stored and shown after a reload.
3. **Given** the person turned Keep forever on and saved, **When** the nightly sweep runs, **Then** no memory is deleted, and the memory day field stays switched off.
4. **Given** "Keep chat history for", **When** the person types 366, **Then** the field says "Enter a whole number from 1 to 365." and Save changes is unavailable.
5. **Given** Memory & recaps, **When** it opens, **Then** it shows no retention fields, only a link row to Data & retention.
6. **Given** an install whose config file says to keep everything forever, **When** Data & retention opens, **Then** a notice says chat history and memory are both kept forever because of that setting, and that the day counts apply once it is removed.
7. **Given** a script, **When** it tries to change retention through the general configuration write, **Then** it is refused with a message naming the retention address, and nothing changes.
8. **Given** a script, **When** it sends a retention value to the memory settings, **Then** it is refused, and nothing changes.
9. **[Pending FQ-1]** **Given** memory kept for 730 days and a reflection 400 days old, **When** an agent recalls something that reflection holds, **Then** the reflection can be found.
10. **Given** Keep forever is on, **When** a script replaces `storage.retention`, `storage` or the whole configuration through the general configuration write, **Then** it is refused (a raw echo of the whole configuration by the existing credential fence, every other shape by the retention guard) and Keep forever stays on; a write that leaves retention as it is still saves.

### User Story 6 — Find moved controls, and see nothing that does not work (Priority: P1)

A person who knew the old tabs wants every control in a predictable new place: God Mode beside Auto-approve, voice input beside the model providers, verbose chat in Profile. They should see no control that does nothing (device pairing) or no longer exists (backup and restore).

**Why this priority**: an unmapped control is a lost feature; a dead control is a false promise.

**Independent Test**: walk the 88-row map (design-notes §2) and confirm each control is where its row says.

**Acceptance Scenarios**:

1. **Given** Protection, **When** it opens, **Then** God Mode shows as a "Turn on God Mode…" button next to Auto-approve, and Gateway has no God Mode control.
2. **Given** the God Mode button, **When** the person presses it, **Then** it asks for the password and acts at once, and the Save bar is not involved.
3. **Given** Providers, **When** it opens, **Then** a Voice input group shows Active transcriber and the key for each voice service.
4. **Given** Profile, **When** it opens, **Then** Chat display holds the Verbose chat switch, and Settings has no Chat section.
5. **Given** Protection, **When** it opens, **Then** a notice says tool permissions and skill trust are in Skills & Tools, with a link there.
6. **Given** an install with device pairing switched on in its config file, **When** Settings opens, **Then** no device pairing control appears anywhere.
7. **Given** a stored key in Keys & secrets, **When** the person deletes it, **Then** they confirm once and the key is gone; no password is asked.
8. **Given** Gateway, **When** the person presses Rotate for the gateway token, **Then** they are asked to confirm first, because every app and program using the token is signed out.
9. **Given** Limits, **When** it opens, **Then** it holds three groups: Context and tool results, Model overrides, and Agents and goals.
10. **Given** the 88-row map, **When** each row's control is looked for, **Then** it is in the place the row names, or absent for the rows marked Removed, Hidden or Moved out.

### User Story 9 — Every section loads, fails and reads the same way (Priority: P2)

A person, including one using only a keyboard, a screen reader or a phone, wants every section to behave the same way when it loads, fails or is empty.

**Why this priority**: consistency is most of the redesign's value, but each item is small.

**Independent Test**: force loading, error, empty and offline on every section; drive the page by keyboard only and at 390 px.

**Acceptance Scenarios**:

1. **Given** a section is still loading, **When** it is shown, **Then** it says "Loading" with the section name and shows placeholder cards.
2. **Given** a section's data cannot be loaded, **When** it is shown, **Then** an error notice says what failed, the observed cause and one thing to do, offers Retry, and Save is unavailable.
3. **Given** the gateway does not answer, **When** Settings opens, **Then** one error notice above the sections says values may be out of date.
4. **Given** keyboard only, **When** the person presses Tab once on Settings, **Then** "Skip to settings content" is focused.
5. **Given** a 390 px wide screen, **When** any section is shown, **Then** every control is at least 44 by 44 px and nothing scrolls sideways.
6. **Given** a screen reader, **When** the Save bar appears or a save finishes, **Then** the change is announced.
7. **Given** any section with grouped lists, **When** it opens, **Then** every group starts collapsed.
8. **Given** one of a section's reads fails, **When** the section opens, **Then** only the groups that need that read show an error and are blocked; the rest of the section works and keeps its drafts.

### User Story 10 — Rider fixes on the same screens (Priority: P2)

A person using Settings wants the known small defects on these screens fixed while the screens are rebuilt (chief-confirmed riders).

**Why this priority**: each is small, but each sits in a file this package rewrites.

**Independent Test**: one check per rider, listed in "Riders".

**Acceptance Scenarios**:

1. **Given** About, **When** it opens, **Then** it shows the build commit (#1560).
2. **Given** Protection → Approvals, **When** the always-asks list is read, **Then** it names today's skill tools only (#1596).
3. **Given** an audit row for a delegated run, **When** it is expanded, **Then** its details show (#1940).
4. **Given** the gateway wrote an audit entry, **When** the audit log opens, **Then** the entry is listed (#1742).
5. **Given** a fresh install with an enforcing sandbox, **When** Protection's health opens, **Then** it does not say the sandbox is disabled (#853).
6. **Given** a script or the system agent, **When** it tries to change a Limits value outside Settings, **Then** it is refused (#936).
7. **Given** a Settings save whose live rebuild failed, **When** it answers, **Then** it says the value is saved but not in use (#995).
8. **Given** Data & retention → Danger zone, **When** it opens, **Then** the clear button says it clears chat history; nothing else about it changes (#1265 stays open).
9. **Given** an agent edit panel was open, **When** the person moves to another section, **Then** nothing covers the page (#1641).

### Edge Cases

- **Two browser tabs editing the same section.** Each tab saves only the fields it changed and the server merges them under its lock, so neither overwrites the other's untouched fields; on the same field the later save wins. Third-party agents is the exception: it saves all three rows, last save wins (C-6).
- **The gateway restarts while the Save bar shows.** Drafts stay on the page; the next Save goes to the restarted gateway.
- **A Save during a running restart.** The write fails with a connection error; the result is unknown, so the screen runs the saved-state check of C-6 once the gateway answers (FR-020); the draft stays until then.
- **Keep forever turned off again.** The memory day count applies again from the next nightly sweep, and reflections older than it are then deleted. The switch's description says so before saving.
- **A day count stored outside the new range by hand-editing the config file.** It is shown as stored, marked invalid, and Save is unavailable until it is fixed.
- **A side panel (Configure provider, Add key) is open with a typed key when the password prompt is cancelled.** The panel and the typed key stay (#1153).
- **Device pairing switched on in the config file.** Still hidden in Settings (#1916). The old `?tab=devices` link opens Providers while Sign-in is hidden.

---

## UI Screens and States

| Screen / Component | Loading | Empty | Error (whole section) | Partial (one group) | Success |
|---|---|---|---|---|---|
| Settings shell (list + search) | List shows at once; open section shows its loading state | — (11 sections always exist) | Gateway offline: one error Callout above the sections (on a phone, above the open section) | Some sections failed: only those show their error | Section open, address names it |
| Providers | Spinner "Loading Providers…" + 3 Skeleton cards | Kit Empty "No providers connected" with Connect a provider | Provider list read failed: Callout with cause, one action, Retry | Voice list failed: the Voice input group shows its own error + Retry and cannot be saved; Default model still saves | Default model, providers, Voice input |
| Memory & recaps | Spinner + Skeleton | — (always has values) | Memory settings read failed | Providers list failed: the model picker accepts a typed model (today's behaviour) | Recaps, models, Recap limits (collapsed), link row to Data & retention |
| System agents | Spinner + Skeleton rows | — | Agent list read failed | One agent missing: "Not found on this install." row | Three rows with Edit |
| Third-party agents | Spinner + Skeleton | — | Read failed | A refused field: inline error under it | Three programs |
| Limits | Spinner + Skeleton | — | Both reads failed | Context read failed: Context and tool results and Model overrides show the error, Agents and goals still saves; Performance read failed: the reverse | Three groups; "Some saved values are not in use yet" info Callout when `pending_apply` |
| Protection | Spinner + Skeleton | — | Sandbox-config read failed | God Mode status failed: God Mode notice with Retry, the rest saves; prompt guard or rate limits read failed: only that group is blocked | Health, Approvals, God Mode, Prompt injection defense, Rate limits, Audit log, moved-items Callout |
| Sandbox & network | Spinner + Skeleton | — | Sandbox-config read failed | Proxy status failed: Network proxy group shows the error; warning Callouts for proxy failed to start, shell limit set on the server, no kernel sandbox | Sandbox, Allowed folders, Internal network addresses, Network proxy |
| Keys & secrets | Spinner + Skeleton | Kit Empty "No keys stored" with Add key | Credential list read failed | — | Master key, key rows |
| Gateway | Spinner + Skeleton | — | Config read failed | Unauthenticated access on: error Callout with the fix | Network, token, Other, Restart, Remote access (collapsed) |
| Data & retention | Spinner + Skeleton | — | Retention read failed: the retention group shows the error and cannot be saved | Storage counts failed: counts show the error, retention still saves | Counts, How long data is kept, Danger zone |
| About | Spinner | — | "System info could not be loaded" Callout + Retry | — | Version, build sha (#1560), system info + Copy, repository link |
| Save bar | — | hidden with no changes and no recent save | "N changes could not be saved" / "N changes may not have been saved" (unknown) | "Fix N values before saving" | "Saved at HH:MM" (FR-018: until the next edit, a section change, or 8 s) |
| Restart banner | — | hidden with nothing waiting | list could not be loaded: says so with Retry | — | "N saved changes wait for a gateway restart"; Show changes marks each as "needs a restart" or "saved, not applied" |

**Sign-in is hidden in this package.** The signed-in browsers list moved to `signed-in-browsers-spec.md` (chief, 2026-11-20 05:08Z). The section list has no Sign-in entry, so the Security group holds Protection, Sandbox & network and Keys & secrets. The design notes give no landing place for a hidden *section*; the nearest rule they give is the unknown-value fallback ("unknown values fall back to Providers"), so `?tab=signin` and `?tab=devices` open Providers with no error (D-3 rows 9 and 17), and searching "signed in" or "browsers" says "Signed-in browsers are not in Settings yet." When the #1096 package lands, Sign-in and the `devices` → Sign-in link come back.

**Read dependencies** (review MAJ-009). A failed read blocks only the groups that need it; drafts in other groups stay and still save; Retry re-reads only the failed read and never replaces drafts elsewhere.

| Section | Read | Groups that need it |
|---|---|---|
| Providers | `GET /providers`, `GET /providers/default-model` | provider rows, Default model |
| Providers | `GET /integrations/providers` (voice services) | Voice input |
| Limits | `GET /settings/context` | Context and tool results, Model overrides |
| Limits | `GET /performance` | Agents and goals |
| Protection | `GET /security/sandbox-config` | Approvals (Auto-approve), Health |
| Protection | `GET /gateway/god-mode` | God Mode button |
| Protection | `GET /security/prompt-guard`, `GET /security/rate-limits` | Prompt injection defense; Rate limits |
| Data & retention | `GET /settings/retention` | How long data is kept |
| Data & retention | `GET /storage/stats` | Counts |
| every other section | its one read | the whole section |

Wording follows the wireframe (`app.js`) and `omnipus-voice`. Where this table and the wireframe differ, the wireframe wins on wording and this table wins on states.

**Badge corrections from the code** (code wins; the wireframe is otherwise followed):

| Row | Wireframe | Code fact | Spec |
|---|---|---|---|
| Active transcriber (Providers) | no badge (`app.js:122`) | password-protected (`rest_integrations_roles.go:365`) | **Password** badge |
| Defense level (Protection) | Needs restart (`app.js:230`) | hot-reloaded (`rest_prompt_guard.go`; excluded in `rest_pending_restart.go:20`) | no badge; a failed apply still shows in the banner (C-2 addition) |
| Shell workspace limit (Sandbox & network) | Password only (`app.js:248`) | `sandbox.workspace_path_guard` is restart-gated (`rest_pending_restart.go:51`) | **Needs restart** + Password |
| Add a folder / allowed folders (Sandbox & network) | Password only (`app.js:254`) | `sandbox.allowed_paths` is restart-gated (`rest_pending_restart.go:37`) | **Needs restart** + Password |
| Delete key (Keys & secrets) | confirm only | password today (`rest_settings.go::deleteCredential`) | confirm only, after B7 |
| Clear all sessions (Data & retention) | "Clear all sessions" | deletes chat sessions (`DataSection.tsx`) | wording names chat history ("Clear all chat history"); behaviour unchanged; #1265 not resolved (Q5 A) |

## User Journey

1. Entry: account menu → Settings, the header gear on the Omnipus chat, a link from another screen, or an old bookmark (US-1 AS1–AS7).
2. The person finds the section in the grouped list or by search (US-1 AS8, AS10). On a phone the list comes first (AS9).
3. They change values; the Save bar counts them and says which need a restart or the password (US-2 AS1, AS5).
4. They press Save changes. Pre-save confirms run first (US-2 AS9, AS10), then one password prompt if needed (US-3), then the writes. Each write ends as saved, saved but not in use, not saved, or unknown (US-2 AS7, AS11).
5. If something needs a restart: "Restart now?" (US-4 AS1); Later leaves the banner (AS2). A changed port or address shows where the gateway will answer before the restart (US-4 AS9).
6. Buttons act at once with their own confirm (US-6 AS2, AS7, AS8).
7. Leaving with unsaved changes asks first: the app's question inside the app, the browser's own prompt when leaving the page (US-2 AS6).

## Accessibility and Keyboard

- One focus ring everywhere: the product's single `:focus-visible` rule (1 px gold outline, 2 px offset); no component draws its own (`omnipus-design-system` skill, focus-visible ownership).
- "Skip to settings content" is the first Tab stop; it moves focus without changing the address (R2-O1).
- The section list is a list of links with `aria-current` on the open one. Radio groups have one Tab stop and arrow keys. Switches are `role="switch"` with `aria-checked`. Collapsible groups use `aria-expanded` and `aria-controls`.
- Invalid Save keeps the approved pattern (`review-response.md::BC-D1`): Save changes carries `aria-disabled="true"`, stays focusable, and pressing it moves focus to the first invalid field.
- Dialogs stack: Escape or Cancel closes only the top one and returns focus to the control that opened it. After "Restart now?" closes with Later or Escape, focus goes to the banner's Restart now button, which always exists then (C-2 addition). After a restart completes, focus goes to the section heading. When an opener no longer exists, focus goes to the section heading.
- Every control is named by its visible label. Number fields state range and unit in their description. Program fields name the program ("Codex program path"); effort pickers name their model ("Effort for the default model"); icon buttons name their target ("Remove /home/you/projects").
- The Save bar and the saved signal are live regions; errors use `role="alert"`; search results update a polite live region.
- At 390 px every control is at least 44 × 44 px (`--target-touch-minimum`; a switch keeps its 36 × 20 track inside a 44 × 44 tap area); nothing scrolls sideways.
- Colour is never the only signal: Needs restart and Password badges carry an icon and a word.
- Skeleton pulse, spinner and row highlight stop under reduced motion.

## Design-System Components

Every element uses a catalogued component, checked in `design-system/catalog.json` at `c2f8f8e735` (the round-1 review re-checked all 32 named exports): PageHeader, SearchField, Button, IconButton, Tooltip, Field, Card, Input, Select, ModelSelector, ModelEffortPicker, Switch, RadioGroup, SegmentedControl, Badge, Callout, ListGroup, DisclosureRow, Empty, Skeleton, Spinner, Dialog, AutoSaveIndicator, CopyButton and the existing `ReAuthDialog` and `GatewayRestartModal`.

- `Item` (R41) is **not** in the catalog. Rows for providers and keys use a `Card` row layout until `Item` exists.
- The Save bar is a screen layout (a floating `Card` with two `Button`s and the `AutoSaveIndicator`), composed once in the Settings screen. If another page later needs it, it becomes a catalogued composite then.
- The restart banner is a `Callout` (warning) with two `Button`s. The "Restart now?" window is the existing `GatewayRestartModal`, whose completion check changes (C-2). The password prompt is the existing `ReAuthDialog`.
- Inline styles in the wireframe files are drawing shortcuts and must not be copied.

## Security and User Promises

- **Focus areas touched (security-lead on-demand review regardless of size):** the credential vault's delete gate (B7); the reauth consent rule (B4, unchanged server side); `PUT /config` refusals for retention and Performance values (C-3, C-7); `set_config` refusal for `tools.manifest.compressed` (C-7); the Security health check (#853 rider, narrow).
- **B7 removes a password gate set by an accepted ADR** (ADR-022). The founder answered S-Q11 B; FQ-2 asks for confirmation now that the server effect is known. Delete stays behind sign-in, the existing admin and bypass guards, a confirm, and the audit log.
- **God Mode** keeps its own password prompt and restart signal and never travels in a Save batch (FR-051).
- **Auto-approve** keeps its password; the help text says each chat can still turn it off with its Auto button (ADR-092).
- **Tool permissions and skill trust** leave Settings because Skills & Tools lands first (S-Q3 A, FR-060); the two-layer policy (Hard Constraint #6) is not changed by this package.
- **Retention deletes data.** The server enforces the ranges unconditionally (FR-042); every other writer is refused by effect, including a replace of `storage` or a permitted multi-section write, and a raw whole-config echo still meets the existing credential fence (FR-044); the screen never sends a day count while Keep forever is on (FR-040); the legacy keep-everything switch is honoured, never silently dropped (FR-048).
- **Performance values** can no longer be raised without the password through `PUT /config` or `set_config` (#936, C-7).
- **Signed-in browsers** are not in this package (`signed-in-browsers-spec.md`).

---

## Behavioral Contract

Primary flows:
- When Settings opens with no section named, the system shows Providers.
- When Settings opens with a section named (new or old name), the system shows the section that holds those settings now.
- When a value is changed, the system shows the Save bar with the number of changed values, how many need a restart and how many need the password.
- When Save changes is pressed with valid values, the system runs the pre-save confirms, then at most one password prompt, then stores every changed value and reports each as saved, saved but not in use, not saved, or unknown.
- When a stored value needs a restart, or a saved value could not be applied, the system offers "Restart now?" at once and keeps a banner on every section until the restart, or until the value is saved back to its running value or applied.
- When a button is pressed (Test, Check, Rotate, God Mode, Delete, Clear), the system acts at once, after a confirm when it destroys or signs out something.
- When Keep forever is on for memory, the system deletes no reflection.

Error flows:
- When a value is outside its range, the system names the range under the field and keeps Save unavailable until it is fixed.
- When one of several writes fails, the system keeps the failed value as a change, shows the server's message under it, and marks the others saved.
- When a write's result is unknown, the system reads the saved value from `config.json` (through `GET /config/pending-restart`, C-6) and shows what the server holds; a read of the running settings alone never marks a value as not saved; Discard never claims to undo a stored value.
- When the password is wrong, the system says so in the prompt and keeps every change.
- When the password prompt is cancelled, the system stores nothing and keeps every change, including keys typed in side panels.
- When one read of a section fails, the system blocks only the groups that need it.
- When any writer other than `/settings/retention` would change the effective retention values, the system refuses the whole write and changes nothing.

Boundary conditions:
- When chat history is set to 1 or 365 days, the system accepts it; 0 or 366 is refused.
- When memory is set to 1 or 730 days, the system accepts it; 0 or 731 is refused.

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

- The system must not save any value without Save changes being pressed (S-Q2 A). This includes recap model slots, Gateway fields, rate limits and the old delayed-confirm Performance fields.
- The system must not put God Mode in a Save batch.
- The system must not show device pairing or the Sign-in section in Settings in this package (#1916; Sign-in moved to `signed-in-browsers-spec.md`).
- The system must not show issue numbers, requirement tags (R30, S-Q8) or "contact support" in any on-screen text (design-notes §10; omnipus-voice).
- The system must not ship a temporary layout that keeps tool permissions, web search or skill trust in Settings; the package is cut only after Skills & Tools has landed (S-Q3 A, FR-060).
- The system must not keep the typed password after the one consent request of a Save (B4).
- The system must not tell the person a value was not saved when the server may have stored it, and Discard must not suggest it undoes a stored value (FR-020).
- The system must not promise per-field merging for Third-party agents; that section saves all three rows and the last save wins (C-6).
- The system must not delete memory while Keep forever is on, nor chat history or memory while the legacy keep-everything switch is on.
- The system must not add a new kit component; the catalog already covers every job.
- The system must not show a "Needs restart" badge on a setting that applies without a restart (Defense level).
- The system must not change what "Clear all sessions" does (#1265 stays open; Q5 A); only its wording names chat history.

### Machine-Verifiable Constraints

**Error Codes / Messages**:
- `PUT /settings/retention` with `session_days` outside 1–365 or not an integer → 400 `ErrorResponse`, message `session_days must be a whole number from 1 to 365`; `memory_retros_days` outside 1–730 → 400, `memory_retros_days must be a whole number from 1 to 730`; `memory_retros_forever` not a boolean → 400 `memory_retros_forever must be true or false`; a body with `keep_everything_forever` or any unknown field → 400 naming the field; `{}` → 400 `at least one field required`. All with `gateway.validate_inbound` on and off.
- `PUT /config` whose merged result changes any effective retention value → 400 `retention settings can only be changed through PUT /api/v1/settings/retention`; `config.json` byte-identical.
- `PUT /config` whose merged result changes any effective Performance value → 400 `performance settings can only be changed through PUT /api/v1/performance`; `config.json` byte-identical.
- `PUT /settings/memory` carrying `session_days` or `memory_retros_days` → 400 naming the field; `config.json` unchanged; with `validate_inbound` on and off.
- `GET` or `PUT /security/retention` → 404 (route removed). `POST /security/retention/sweep` unchanged.
- `DELETE /credentials/{key}` with no `X-Reauth-Token`, by a signed-in admin → today's success status; the key is gone.
- `set_config` with `tools.manifest.compressed` → refused with a message naming Settings → Limits.

**Performance Bounds**:
- The Save bar MUST appear within 100 ms of the change that triggers it (no server round trip).

**Scope Boundaries**:
- The system MUST NOT add an agent tool or a new `set_config` write path for any value in this spec.
- The system MUST NOT change Skills & Tools, Tasks and Team, device pairing or browser sign-in (out of scope).

## Integration Boundaries

### Gateway REST API (SPA ↔ gateway)
- **Data in / out**: section reads and writes; the endpoints in "API and Data".
- **Contract**: `contracts/openapi.yaml` and the schemas named in Contract Changes.
- **On failure**: group or section error with Retry; per-field error on a refused write; "may not have been saved" with read-back on an unknown result; gateway-offline Callout when nothing answers.
- **Development**: real gateway (`./omnipus gateway` on a test home).

### Config file and nightly sweeps (B3, B6)
- **Data in / out**: `storage.retention.*`, read by the sweeps on each tick.
- **Contract**: `RetentionSettings` / `RetentionSettingsUpdate` on the wire; the `config.json` keys named there.
- **On failure**: a failed write is a 500 from `/settings/retention` with the cause; the sweep keeps using its last good config.
- **Development**: the real sweep functions (`retention_goroutine.go::executeRetroSweepTick` and the session sweep) with a controlled clock in tests.

### Gateway restart (B2)
- **Data in / out**: `POST` restart, `GET /about` (uptime, pid), `GET /config/pending-restart`.
- **Browser boundary**: every check is a same-origin request (`connect-src 'self'`, `rest.go::setCORSHeaders`); after a port or host change the hand-over to the new address is a top-level link the person follows (FR-063). Neither policy is widened.
- **On failure**: the window says the gateway did not come back within 60 s, keeps Restart now, and names one thing to check (FR-063).

### Skills & Tools page (prerequisite, S-Q3 A)
- **Data in / out**: links only.
- **Contract**: the Skills & Tools route (owned by that package).
- **Order**: Settings is cut and landed only after Skills & Tools has landed with the tool permission grid, web search and skill trust (FR-060). Preparation may start earlier.

---

## Ambiguity Warnings

| # | What's Ambiguous | Likely Agent Assumption | Question to Resolve |
|---|---|---|---|
| 1 | Exact text of each error and notice not drawn in the wireframe (for example the legacy keep-everything notice, "may not have been saved") | Invent wording | Use this spec's text; frontend-lead applies omnipus-voice; reviewers check the pattern |
| 2 | How a hidden section's old links behave | Show an error or an empty Sign-in | Providers, no error (the unknown-value rule); search explains |
| 3 | How search ranks and which words match moved items | A fuzzy-search library | Plain case-insensitive substring match over label, description and the moved-items list; no new dependency |
| 4 | Recall window if FQ-1 is answered B | Leave FR-050 in | FR-050 and US-5 AS9 are dropped if the founder picks B |
| 5 | Whether drafts survive switching sections | Keep drafts across sections | No: the wireframe asks before leaving; drafts are per section (this is also what makes B4 hold) |
| 6 | Whether a 5xx means "not saved" | Yes | No: it is "unknown" until a read-back says otherwise (FR-020) |

---

## BDD Scenarios

Expected values below come from this spec, the cited contract schemas, the wireframe and the founder answers — never from today's implementation. "the gateway" is a real gateway on a test home; "signed in" means a local-mode account unless the scenario says platform mode.

### Feature: Settings structure and links (User Story 1)

#### Scenario: Person opens Settings and sees 11 sections in three groups
**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path
- **Given** a signed-in person on a fresh install
- **When** they open `/settings` with no parameters
- **Then** the section list shows, in order, the group "AI & agents" with Providers, Memory & recaps, System agents, Third-party agents, Limits; the group "Security" with Protection, Sandbox & network, Keys & secrets; the group "System" with Gateway, Data & retention, About
- **And** Providers is open and marked current in the list
- **But** no Sign-in, Devices, Chat, Integrations, Models, Performance or Security entry exists

#### Scenario: Reload keeps the open section
**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Happy Path
- **Given** Sandbox & network is open and the address carries `tab=sandbox`
- **When** the page is reloaded
- **Then** Sandbox & network is open

#### Scenario Outline: Old and new section links land in the right place
**Traces to**: User Story 1, Acceptance Scenarios 3, 6, 7
**Category**: Alternate Path
- **Given** a signed-in person
- **When** they open `/settings?tab=<tab>`
- **Then** `<lands>` is shown, with `<extra>`

**Examples**: Dataset D-3 (all rows).

#### Scenario: Unknown section name falls back to Providers
**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Edge Case
- **Given** a signed-in person
- **When** they open `/settings?tab=notasection`
- **Then** Providers is open and no error is shown

#### Scenario: The "No context length" link pre-fills a model override
**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Alternate Path
- **Given** a provider `openrouter` whose model `acme/m1` reports no context length
- **When** the person follows `/settings?tab=models&provider=openrouter&model=acme/m1`
- **Then** Limits is open, the Model overrides group is expanded, and a new unsaved row shows provider `openrouter` and model `acme/m1` with an empty context length
- **And** the Save bar shows "1 unsaved change"

#### Scenario: The God Mode pill lands on the God Mode button
**Traces to**: User Story 1, Acceptance Scenario 5
**Category**: Alternate Path
- **Given** God Mode is on and the sidebar pill is visible
- **When** the person clicks the pill
- **Then** Protection is open and the God Mode button has keyboard focus
- **And** the address no longer carries `focus=god-mode` after focus moved

#### Scenario: Searching for a moved setting says where it went
**Traces to**: User Story 1, Acceptance Scenario 8
**Category**: Happy Path
- **Given** Settings is open
- **When** the person types "web search" in Search settings
- **Then** a result reads that web search moved to Skills & Tools, with a link that opens Skills & Tools
- **And** the result count is announced in a polite live region

#### Scenario Outline: Search results
**Traces to**: User Story 1, Acceptance Scenarios 8, 10
**Category**: Alternate Path
- **Given** Settings is open
- **When** the person types `<query>` in Search settings
- **Then** the results show `<result>`

**Examples**: Dataset D-13.

#### Scenario: Search with no match shows an empty state
**Traces to**: User Story 1, Acceptance Scenario 10
**Category**: Error Path
- **Given** Settings is open
- **When** the person types "zzqx" in Search settings
- **Then** the kit Empty state says no settings match "zzqx"

#### Scenario: On a phone the list comes first
**Traces to**: User Story 1, Acceptance Scenario 9
**Category**: Alternate Path
- **Given** a 390 px wide window showing the Settings list
- **When** the person taps Gateway
- **Then** only the Gateway section shows, with a "Settings" back button at the top
- **And** tapping that button shows the list again

#### Scenario: Browser Back moves to the previous section
**Traces to**: User Story 1, Acceptance Scenario 11
**Category**: Alternate Path
- **Given** the person opened Gateway, then Limits, with no unsaved changes
- **When** they press the browser's Back button
- **Then** Gateway is open; Forward opens Limits again

#### Scenario Outline: Links from other screens keep working
**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Alternate Path
- **Given** the person is on `<screen>`
- **When** they activate `<control>`
- **Then** `<lands>` is open

**Examples**:

| screen | control | lands |
|---|---|---|
| Omnipus chat header | Omnipus settings gear | System agents |
| Agent profile of a third-party worker | Change in Settings | Third-party agents |
| Skills screen | permissions link | Skills & Tools, permissions area (Settings no longer holds the grid; FR-009, FR-060) |
| Default model card | No context length → Set it in Limits | Limits, Model overrides expanded |
| `/app/policies` | (redirect) | Settings, Providers |

### Feature: One save rule (User Story 2)

#### Scenario: A changed value shows the Save bar and stores nothing
**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path
- **Given** Gateway shows Port 8080 as saved
- **When** the person types 8081 in Port
- **Then** the Save bar says "1 unsaved change", "1 needs a gateway restart", with Discard and Save changes
- **And** `GET /config` still returns port 8080

#### Scenario: Save stores the value and it stays after leaving the section
**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Happy Path
- **Given** Port changed from 8080 to 8081 and not saved
- **When** the person presses Save changes (and Later in the restart window)
- **Then** `GET /config` returns port 8081, and the Save bar says "Saved at" followed by the time as HH:MM
- **And** after opening Limits and coming back, Port shows 8081

#### Scenario: Discard returns every change to its saved value
**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Alternate Path
- **Given** Port changed to 8081 and Log level changed to Debug, unsaved
- **When** the person presses Discard
- **Then** Port shows 8080, Log level shows Info, the Save bar is hidden, and nothing was written

#### Scenario Outline: An invalid value blocks saving and names the range
**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Error Path
- **Given** `<section>` is open
- **When** the person enters `<input>` in `<field>`
- **Then** the field shows `<message>`, Save changes carries `aria-disabled="true"` and stays focusable, and the bar says "Fix 1 value before saving"
- **And** pressing Save changes moves focus to `<field>` and sends nothing

**Examples**: Dataset D-10, rows marked **Rejected** only.

#### Scenario Outline: A value inside its range can be saved
**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Edge Case
- **Given** `<section>` is open
- **When** the person enters `<input>` in `<field>` and saves
- **Then** no field error shows and the value is stored

**Examples**: Dataset D-10, rows marked **Accepted** only.

#### Scenario: A value changed back by hand is no longer counted
**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Edge Case
- **Given** Port was changed from 8080 to 8081
- **When** the person types 8080 back into Port
- **Then** the Save bar is hidden

#### Scenario: Typing in an Add field is not a change until Add is pressed
**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Edge Case
- **Given** Sandbox & network with no unsaved changes
- **When** the person types `/srv/data` in "Add a folder" without pressing Add
- **Then** the Save bar is hidden; after pressing Add it says "1 unsaved change"

#### Scenario: Leaving with unsaved changes asks first, and Stay keeps them
**Traces to**: User Story 2, Acceptance Scenario 6
**Category**: Alternate Path
- **Given** Limits with 1 unsaved change
- **When** the person clicks Gateway in the section list
- **Then** a confirm asks whether to leave without saving 1 change
- **And** choosing Stay keeps Limits open with the change, and the address still names Limits

#### Scenario Outline: Every way out inside the app asks with the app's question
**Traces to**: User Story 2, Acceptance Scenario 6
**Category**: Edge Case
- **Given** a section with 1 unsaved change
- **When** the person uses `<exit>`
- **Then** `<asks>`

**Examples**:

| exit | asks |
|---|---|
| another section in the list | the leave confirm |
| a search result | the leave confirm; Stay returns focus to the search field |
| the Profile link | the leave confirm |
| an app sidebar item | the leave confirm |
| browser Back | the leave confirm; Stay leaves history as it was (no extra entry) |
| browser Forward (after an earlier Back) | the leave confirm; Stay leaves history as it was |
| a typed change of the part after `#` only (same page) | the leave confirm |
| the phone's "Settings" back button | the leave confirm |

#### Scenario Outline: Leaving the page uses the browser's own prompt
**Traces to**: User Story 2, Acceptance Scenario 6
**Category**: Edge Case
- **Given** a section with 1 unsaved change
- **When** the person `<exit>`
- **Then** the browser's own leave prompt appears (its wording, not the app's), and choosing to stay keeps the change

**Examples**:

| exit |
|---|
| reloads the page |
| types a different site's address and presses Enter |
| closes the tab |

#### Scenario: Leave without saving drops the change
**Traces to**: User Story 2, Acceptance Scenario 6
**Category**: Alternate Path
- **Given** Limits with 1 unsaved change and the leave confirm open
- **When** the person chooses Leave
- **Then** Gateway opens and returning to Limits shows the saved value

#### Scenario: One of two writes fails
**Traces to**: User Story 2, Acceptance Scenario 7
**Category**: Error Path
- **Given** Limits with a changed Default context window (32000) and a changed Tries per goal (8), and a declared failure seam: the test intercepts `PUT /settings/context` and answers 400 `default_context_window must be ≥ 1 (send null to clear it)`, while `PUT /performance` reaches the real gateway
- **When** the person saves (with the right password)
- **Then** Tries per goal shows as saved and `GET /performance` returns 8; Default context window keeps 32000 with the seam's message under it; the bar says "1 change could not be saved"

#### Scenario: A real server refusal is shown under its field
**Traces to**: User Story 2, Acceptance Scenario 7
**Category**: Error Path
- **Given** Third-party agents, and the person sets the Codex program path to `/opt/omnipus-test/no-such-codex`, an absolute path that passes every check the screen makes but names no file
- **When** the person saves
- **Then** the real `PUT /settings/third-party-agents` answers 400 with `code: cli_path_invalid`, `field: clis.codex.path` and `details.check: not_found`; the server's message (`thirdpartycli.CheckError::SaveRefusal`) shows under the Codex path row; the draft stays; `config.json` is unchanged
- **And** the screen's own validation is not loosened for this: a Model overrides context length of 0 is still refused by the screen (D-10 row 11), and the server's own refusal of 0 is tested at the handler (`TestContextSettings_RefusesZeroOverride`)

#### Scenario: A Save while the gateway is down keeps every change
**Traces to**: User Story 2, Acceptance Scenario 7
**Category**: Error Path
- **Given** Gateway with 1 unsaved change and the gateway process stopped
- **When** the person presses Save changes
- **Then** the change stays, the result is unknown (the read-back fails too), the bar says "1 change may not have been saved" with Retry, and the error says the gateway is not answering and to check that it is running

#### Scenario: A recap model choice waits for Save
**Traces to**: User Story 2, Acceptance Scenario 8
**Category**: Happy Path
- **Given** Memory & recaps with a saved primary recap model
- **When** the person picks a different primary model
- **Then** `GET /settings/memory` still returns the old model and the Save bar says "1 unsaved change"

#### Scenario: Lowering the tool-call limit previews affected agents before saving
**Traces to**: User Story 2, Acceptance Scenario 9
**Category**: Alternate Path
- **Given** Max tool calls per turn saved as 50 and one agent with its own value 40
- **When** the person enters 30 and presses Save changes
- **Then** a confirm lists that agent before the password prompt and before any write

#### Scenario: Choosing All interfaces asks before saving
**Traces to**: User Story 2, Acceptance Scenario 10
**Category**: Alternate Path
- **Given** Bind address saved as Localhost only
- **When** the person selects All interfaces and presses Save changes
- **Then** "Listen on all interfaces?" opens before any write; Cancel stores nothing and keeps the change

#### Scenario: Only one gold button while the Save bar shows
**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Edge Case
- **Given** Providers with no changes shows Connect a provider as the gold button
- **When** the person changes the default model
- **Then** Save changes is the only gold button and Connect a provider is outlined

#### Scenario: A write that was stored but not applied says so
**Traces to**: User Story 2, Acceptance Scenario 11
**Category**: Error Path
- **Given** Third-party agents with a changed Codex program path, and a failure seam that makes the live refresh fail after `config.json` is written
- **When** the person saves
- **Then** the answer is 500 with `code: saved_not_refreshed` and `details.keys` naming the Codex path; the row shows the new path as saved with the warning "Saved, but not in use yet. Restart the gateway to apply it.", the restart banner lists it as "saved, not applied", and Discard is not offered for it
- **And** the section does **not** replace the shown rows with a re-read of the running settings (which still hold the old path)

#### Scenario: An unknown result is checked by reading back
**Traces to**: User Story 2, Acceptance Scenario 11
**Category**: Error Path
- **Given** Gateway with Log level changed from Info to Debug, and a seam that drops the response of `PUT /config` after the server has stored and applied it
- **When** the person saves
- **Then** the screen runs the saved-state check (C-6): `GET /config/pending-restart` does not list `gateway.log_level` and `GET /config` shows Debug, so the value is shown as **saved and in use**; if either read fails, the bar says "1 change may not have been saved" with Retry, the field keeps Debug, and Discard's confirm says the server may already hold the new value

#### Scenario: A stored write whose refresh failed and whose answer was lost is found by the saved-state check
**Traces to**: User Story 2, Acceptance Scenario 11
**Category**: Error Path
- **Given** Gateway with Log level changed from Info to Debug, a seam that makes the live refresh fail after `config.json` is written, and a second seam that drops the response
- **When** the person saves
- **Then** `GET /config` still shows Info (running settings), but `GET /config/pending-restart` lists `gateway.log_level` with `reason: apply_failed` and `persisted_value` holding the stored Debug value; the screen shows Debug as **saved, not in use**, the banner lists it, and the value is never shown as "not saved" and never reset to Info

#### Scenario: A value changed elsewhere while the answer was lost
**Traces to**: User Story 2, Acceptance Scenario 11
**Category**: Edge Case
- **Given** Gateway with Log level changed from Info to Debug, a seam that drops the response, and another tab that saved Warn in between
- **When** the saved-state check finds Warn (neither Info nor Debug)
- **Then** the field keeps the draft Debug as an unsaved change, the saved value shown for Discard is Warn, the message under the field says "This setting was changed somewhere else while you were saving. Saved value: Warn.", and the bar counts 1 unsaved change

#### Scenario: Third-party agents saves all three rows
**Traces to**: User Story 2, Acceptance Scenario 12
**Category**: Alternate Path
- **Given** stored rows for Claude Code, Codex and OpenCode, each with a path and parameters
- **When** the person changes only Codex parameters and saves
- **Then** one `PUT /settings/third-party-agents` carries all three rows, the Claude Code and OpenCode rows are unchanged in `config.json`, and Codex has the new parameters

#### Scenario: Two tabs saving different Limits fields keep both changes
**Traces to**: User Story 2, Acceptance Scenario 13
**Category**: Edge Case
- **Given** two `PUT /settings/context` requests read the same stored values, held at a test barrier inside the handler: tab 1 changes the MCP result cap, tab 2 the built-in success cap
- **When** both are released, in each order
- **Then** `config.json` holds both new values; with both changing the same field, the later write's value is stored

#### Scenario: The saved signal stays long enough to read
**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Edge Case
- **Given** one unsaved change
- **When** the person saves
- **Then** the bar shows "Saved at" with the time for 8 seconds, then hides; a new edit within those 8 seconds replaces it with the change count at once; leaving the section hides it; the live region announces "Changes saved." once

### Feature: One password prompt per Save (User Story 3)

#### Scenario: Two password-marked changes need one password
**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Happy Path
- **Given** local mode, Sandbox & network with Filesystem model and Shell workspace limit changed
- **When** the person presses Save changes and enters the right password
- **Then** exactly one password prompt opened, listing "Filesystem model" and "Shell workspace limit" and nothing else
- **And** exactly one `POST /auth/reauth` and one `PUT /security/sandbox-config` were sent, and both values are stored

#### Scenario: The typed password is not kept
**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Edge Case
- **Given** a Save whose password prompt was answered correctly
- **When** the prompt closes
- **Then** the password field's value is empty and no later request carries the password

#### Scenario: Wrong password
**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Error Path
- **Given** the password prompt for a Save
- **When** the person enters a wrong password
- **Then** the prompt stays open with "The password is incorrect." under the field, focus is in the field, and nothing was stored

#### Scenario: Too many password tries
**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Error Path
- **Given** the reauth limit (10 per minute) is used up
- **When** the person submits the password
- **Then** the prompt says too many tries were made and to wait a minute, and every change stays

#### Scenario: Cancelling the password prompt keeps every change and any typed key
**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Alternate Path
- **Given** Providers → Configure with a typed API key and the password prompt open over the panel
- **When** the person presses Cancel
- **Then** only the prompt closes; the panel stays open with the typed key, focus returns to its Save button, and nothing was stored

#### Scenario: Platform mode asks for a confirmation instead
**Traces to**: User Story 3, Acceptance Scenario 4
**Category**: Alternate Path
- **Given** a platform-mode install (omnipus.ai sign-in)
- **When** the person saves a change to Auto-approve
- **Then** a confirmation listing "Auto-approve" opens with no password field, and no `POST /auth/reauth` is sent

#### Scenario: Active transcriber needs the password
**Traces to**: User Story 3, Acceptance Scenario 5
**Category**: Happy Path
- **Given** Providers, Voice input, Active transcriber "OpenAI Whisper" saved
- **When** the person picks "Groq", presses Save changes and enters the right password
- **Then** the row carried a Password badge, the prompt listed "Active transcriber", and the new transcriber is stored

#### Scenario: A late change in the affected agents asks again
**Traces to**: User Story 3, Acceptance Scenario 6
**Category**: Edge Case
- **Given** a lowering of Max tool calls per turn was confirmed and the password given, and the server answers 409 with a different list of affected agents
- **When** the 409 arrives
- **Then** the new list is shown for confirmation, the password is asked again, and nothing was stored before that

#### Scenario: A Save with no password-marked value has no prompt
**Traces to**: User Story 3, Acceptance Scenario 7
**Category**: Happy Path
- **Given** Gateway with Log level changed
- **When** the person presses Save changes
- **Then** no password prompt opens and no `POST /auth/reauth` is sent

#### Scenario Outline: Each section sends its password-marked values to at most one protected endpoint
**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Edge Case
- **Given** `<section>` with every password-marked value changed at once
- **When** the person saves with the right password
- **Then** exactly `<n>` protected request(s) are sent, to `<endpoint>`

**Examples**: Dataset D-8.

### Feature: One restart pattern (User Story 4)

#### Scenario: A Save that needs a restart offers it at once
**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path
- **Given** Port changed from 8080 to 8081
- **When** the person saves
- **Then** the "Restart now?" window lists "Port" and offers Later and Restart now

#### Scenario: Later leaves a banner on every section
**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Alternate Path
- **Given** the "Restart now?" window after saving Port
- **When** the person chooses Later
- **Then** the banner "1 saved change waits for a gateway restart" shows with Restart now and Show changes, focus is on its Restart now button, and the banner also shows on Providers and About

#### Scenario: Show changes lists running and saved values
**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Happy Path
- **Given** Port saved as 8081 while the gateway runs on 8080
- **When** the person chooses Show changes
- **Then** the list reads Port, running 8080, saved 8081

#### Scenario Outline: The server lists every restart-gated setting that was saved
**Traces to**: User Story 4, Acceptance Scenarios 4, 5
**Category**: Happy Path
- **Given** a running gateway
- **When** `<key>` is saved as `<saved>` while it runs with `<running>`
- **Then** `GET /config/pending-restart` `<appears>`

**Examples**: Dataset D-4.

#### Scenario: Defense level applies at once
**Traces to**: User Story 4, Acceptance Scenario 6
**Category**: Edge Case
- **Given** Protection with Defense level Medium
- **When** the person selects High and saves
- **Then** the row carries no Needs restart badge, no "Restart now?" window opens, and no banner appears

#### Scenario: A failed reload still says a restart is needed
**Traces to**: User Story 4, Acceptance Scenario 6
**Category**: Error Path
- **Given** a write whose response says `requires_restart: true` for a setting not marked Needs restart (for example Defense level when the reload failed)
- **When** the response arrives
- **Then** the "Restart now?" window opens and names that setting

#### Scenario: The restart clears the banner
**Traces to**: User Story 4, Acceptance Scenario 7
**Category**: Happy Path
- **Given** the banner for Port
- **When** the person chooses Restart now and the gateway comes back
- **Then** the banner is gone and focus is on the open section's heading

#### Scenario: Saving the running value again clears the banner
**Traces to**: User Story 4, Acceptance Scenario 8
**Category**: Edge Case
- **Given** Port saved as 8081 while running on 8080
- **When** the person saves 8080
- **Then** `GET /config/pending-restart` returns an empty list and the banner is gone

#### Scenario: The restart list cannot be loaded
**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Error Path
- **Given** `GET /config/pending-restart` answers 500
- **When** a section opens
- **Then** the banner area says the list of waiting changes could not be loaded, with Retry

#### Scenario: A failed hot apply stays in the banner after Later
**Traces to**: User Story 4, Acceptance Scenario 10
**Category**: Error Path
- **Given** Protection with Defense level changed, and a seam that makes the prompt-guard reload fail
- **When** the person saves, chooses Later, opens Gateway, and reloads the page
- **Then** `GET /config/pending-restart` lists `sandbox.prompt_injection_level` with `reason: apply_failed`; the banner shows on every section and after the reload; focus after Later is on the banner's Restart now button

#### Scenario: A later successful apply clears a failed one
**Traces to**: User Story 4, Acceptance Scenario 10
**Category**: Edge Case
- **Given** the failed apply of the previous scenario, and the seam removed
- **When** the person saves Defense level again
- **Then** the entry is gone from `/config/pending-restart` and the banner hides

#### Scenario: A changed port shows where the gateway will answer
**Traces to**: User Story 4, Acceptance Scenario 9
**Category**: Alternate Path
- **Given** the page is opened directly at `http://127.0.0.1:8080` (production-served page, real response headers) and Port is saved as 8081
- **When** the person chooses Restart now
- **Then** before restarting, the window says the gateway will answer at `http://127.0.0.1:8081` and that the current address will stop working, and adds "If you reach Omnipus through a proxy, point it at port 8081 instead."
- **And** the page makes no request to port 8081 (its policy allows only its own address); it polls only its own `/health`, and once that stops answering it shows Open the new address, a plain link to `http://127.0.0.1:8081/settings?tab=gateway&restarted=1` that the person follows
- **And** if the old address still answers after 60 s with an `uptime_seconds` older than the restart, the window says the gateway did not restart (FR-063)

#### Scenario: The new address confirms the restart on its own origin
**Traces to**: User Story 4, Acceptance Scenario 9
**Category**: Alternate Path
- **Given** the person followed Open the new address to `http://127.0.0.1:8081/settings?tab=gateway&restarted=1`
- **When** the page loads there (asking the person to sign in first if that address has no session, then returning to the same link)
- **Then** with same-origin requests only, it reads `GET /config/pending-restart`; with `gateway.port` no longer listed it says "The gateway restarted on port 8081" and shows no banner; with `gateway.port` still listed it shows the banner with Restart now
- **And** if the new address does not load at all, the browser's own error page shows; the window had already shown the address and the instruction to check that the gateway is running and read its log (no in-page claim of success was made)

#### Scenario: Behind a proxy the window gives an instruction instead of a new address
**Traces to**: User Story 4, Acceptance Scenario 9
**Category**: Alternate Path
- **Given** the page is opened at an address whose port differs from the gateway's running port (a reverse proxy in front of the gateway), and Port is saved as 8081
- **When** the person chooses Restart now
- **Then** the window says the gateway will listen on port 8081 and that the proxy must point there, offers no new address, and keeps polling only its own address; when its own `/about` shows a fresh `uptime_seconds` and `gateway.port` is no longer pending, it reports the restart as done

#### Scenario: An old process still answering does not count as restarted
**Traces to**: User Story 4, Acceptance Scenario 7
**Category**: Edge Case
- **Given** Restart now was pressed at T, and the old process keeps answering `/health` and `/about` for 3 seconds with `uptime_seconds` larger than the time since T
- **When** the window polls
- **Then** it keeps waiting, and reports success only when `/about` shows `uptime_seconds` smaller than the time since T and `/config/pending-restart` no longer lists the restarted keys

#### Scenario: The gateway does not come back
**Traces to**: User Story 4, Acceptance Scenario 7
**Category**: Error Path
- **Given** Restart now was pressed and no new process answers within 60 seconds
- **When** the time runs out
- **Then** the window says the gateway did not come back, to check that it is running and read its log, keeps Restart now, and the banner stays

### Feature: Retention (User Story 5)

#### Scenario: Fresh-install defaults
**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Happy Path
- **Given** a fresh install
- **When** Data & retention opens
- **Then** "Keep chat history for" shows 90, "Keep memory for" shows 180, and Keep forever is off
- **And** `GET /settings/retention` returns `session_days` 90, `memory_retros_days` 180, `memory_retros_forever` false, `keep_everything_forever` false

#### Scenario: Saving both day counts
**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Happy Path
- **Given** Data & retention with defaults
- **When** the person sets 30 and 365 and saves
- **Then** one `PUT /settings/retention` carrying both values is sent, and after a reload the fields show 30 and 365

#### Scenario Outline: The server enforces the ranges
**Traces to**: User Story 5, Acceptance Scenario 4
**Category**: Edge Case
- **Given** a signed-in script
- **When** it sends `PUT /settings/retention` with `<body>`
- **Then** the answer is `<status>` `<message>` and the stored values are `<stored>`

**Examples**: Datasets D-1 and D-2.

#### Scenario: Keep forever stops memory deletion, through the real sweep
**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Happy Path
- **Given** `memory_retros_forever` true, `memory_retros_days` 180, `session_days` 30, a reflection dated 200 days ago, a chat transcript dated 40 days ago, and a controlled clock
- **When** the test runs `retention_goroutine.go::executeRetroSweepTick` and the session sweep tick once each and waits for both to report completion
- **Then** the reflection still exists, and the transcript was deleted

#### Scenario: The memory sweep deletes when Keep forever is off (positive control)
**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Happy Path
- **Given** the same fixture with `memory_retros_forever` false
- **When** the same sweep ticks run to completion
- **Then** the reflection is deleted (proving the instrument can see a deletion)

#### Scenario: With Keep forever on the day field is off and not sent
**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Alternate Path
- **Given** "Keep memory for" shows 999 (invalid)
- **When** the person turns Keep forever on and saves
- **Then** the day field is disabled with no error, Save was available, and the `PUT /settings/retention` body is `{"memory_retros_forever": true}` with no `memory_retros_days`

#### Scenario: Turning Keep forever off again resumes deletion
**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Edge Case
- **Given** Keep forever on, `memory_retros_days` 180, and a reflection dated 200 days ago
- **When** Keep forever is turned off and saved, and the real retro sweep tick runs to completion
- **Then** that reflection is deleted, and the switch's description had said older reflections are deleted at the next nightly clean-up

#### Scenario: Memory & recaps holds only a link to retention
**Traces to**: User Story 5, Acceptance Scenario 5
**Category**: Alternate Path
- **Given** Memory & recaps
- **When** it opens
- **Then** it shows no day-count field, and a row "How long memory is kept" opens Data & retention when activated

#### Scenario: The legacy keep-everything switch is shown, not hidden
**Traces to**: User Story 5, Acceptance Scenario 6
**Category**: Edge Case
- **Given** `config.json` has `storage.retention.disabled: true`, a 200-day-old reflection and a 400-day-old transcript
- **When** Data & retention opens, and both real sweep ticks run to completion
- **Then** an info notice says chat history and memory are both kept forever because of a setting in the configuration file, and that the day counts below apply once it is removed; both day fields stay editable
- **And** neither the reflection nor the transcript was deleted

#### Scenario Outline: Keep forever survives any replace of its parent block
**Traces to**: User Story 5, Acceptance Scenario 10
**Category**: Error Path
- **Given** `config.json` holding fixture `<fixture>` from Dataset D-12 (F1: legacy `disabled: true`; F2: `memory_retros_forever: true`; both with `session_days: 30` and `memory_retros_days: 400`)
- **When** a signed-in script sends `<body>` to `PUT /config`
- **Then** the answer is 400 with `<message>`, `config.json` is byte-identical to before, and after a fresh config load all four effective retention values (`RetentionSessionDays()`, `RetentionMemoryRetrosDays()`, `memory_retros_forever`, `IsDisabled()`) equal the fixture's
- **And** no other key in the body is written
- **And** for rows marked RED, the same test run against `c2f8f8e735` with fixture F1 fails (the change is stored); that failing run is saved during the failing-test step

**Examples**: Dataset D-12, rows marked **Refused** only.

#### Scenario Outline: Writes that leave retention unchanged still save
**Traces to**: User Story 5, Acceptance Scenario 7
**Category**: Edge Case
- **Given** `config.json` holding fixture `<fixture>` from Dataset D-12
- **When** a signed-in script sends `<body>` to `PUT /config`
- **Then** the answer is 200, a fresh config load shows the intended change of `<body>`, and all four effective retention values equal the fixture's (no byte-equality check: the file changed on purpose)

**Examples**: Dataset D-12, rows marked **Accepted** only.

#### Scenario: A raw echo of the whole configuration meets the existing credential fence first
**Traces to**: User Story 5, Acceptance Scenario 10
**Category**: Edge Case
- **Given** fixture F2, and the body is the object returned by `GET /config`, unchanged or with `storage.retention` replaced by `{}`
- **When** a signed-in script sends it to `PUT /config`
- **Then** the answer is 403 from the existing credential-field fence (the body carries `providers`), the retention guard is not reached, `config.json` is byte-identical, and Keep forever is still on
- **And** this result counts as evidence for the credential fence only, not for the retention guard

#### Scenario Outline: The memory settings refuse retention fields whatever the validation setting
**Traces to**: User Story 5, Acceptance Scenario 8
**Category**: Error Path
- **Given** `gateway.validate_inbound` is `<flag>`
- **When** a signed-in script sends `PUT /settings/memory` with `<body>`
- **Then** the answer is 400 naming the retention field, `config.json` is unchanged, and the recap setting in the body was **not** applied

**Examples**:

| flag | body |
|---|---|
| false | `{"session_days": 30}` |
| false | `{"memory_retros_days": 365}` |
| false | `{"auto_recap_enabled": false, "session_days": 30}` |
| true | `{"session_days": 30}` |
| true | `{"auto_recap_enabled": false, "memory_retros_days": 365}` |

#### Scenario: The old retention route is gone
**Traces to**: User Story 5, Acceptance Scenario 7
**Category**: Error Path
- **Given** a signed-in script
- **When** it sends `GET /security/retention` and `PUT /security/retention`
- **Then** both answer 404, and `POST /security/retention/sweep` still answers as before

#### Scenario: Saving Gateway never sends retention
**Traces to**: User Story 5, Acceptance Scenario 7
**Category**: Edge Case
- **Given** Gateway with Port changed
- **When** the person saves
- **Then** the `PUT /config` body contains no `storage` key and the answer is 200

#### Scenario: A hand-edited value outside the range is shown and blocks Save
**Traces to**: User Story 5, Acceptance Scenario 4
**Category**: Edge Case
- **Given** `config.json` has `storage.retention.session_days: 500`
- **When** Data & retention opens through the generated API client (its response validator on)
- **Then** the response is accepted, "Keep chat history for" shows 500 with "Enter a whole number from 1 to 365.", and Save changes stays unavailable until it is fixed; after entering 365 and saving, `GET` returns 365

#### Scenario: [Pending FQ-1] An old reflection kept by the setting can be recalled
**Traces to**: User Story 5, Acceptance Scenario 9
**Category**: Edge Case
- **Given** `memory_retros_days` 730 and a reflection 400 days old containing the word "zeppelin"
- **When** an agent calls `recall_memory` with "zeppelin"
- **Then** the result includes that reflection

#### Scenario: The retention write accepts each range end, whatever the validation setting
**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Edge Case
- **Given** `gateway.validate_inbound` false
- **When** a script sends `PUT /settings/retention` with each Dataset D-1 and D-2 row
- **Then** each row gets the stated answer exactly as with validation on

### Feature: Moved, removed and hidden controls (User Story 6)

#### Scenario: God Mode sits in Protection
**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Happy Path
- **Given** God Mode is available and off
- **When** Protection opens
- **Then** a "Turn on God Mode…" button shows after Approvals, and Gateway contains no God Mode control

#### Scenario: The God Mode button acts alone
**Traces to**: User Story 6, Acceptance Scenario 2
**Category**: Happy Path
- **Given** Protection with 1 unsaved Auto-approve change
- **When** the person presses "Turn on God Mode…" and enters the right password
- **Then** God Mode is turned on, the Auto-approve change is still unsaved, and the Save bar still says "1 unsaved change"

#### Scenario: Voice input lives in Providers
**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Happy Path
- **Given** voice services with and without keys
- **When** Providers opens
- **Then** a Voice input group shows Active transcriber and a key row per service; a key row opens a side panel with the key and show/hide only (no Test, no Remove)

#### Scenario: Verbose chat lives in Profile
**Traces to**: User Story 6, Acceptance Scenario 4
**Category**: Happy Path
- **Given** Verbose chat was on in this browser before the update
- **When** Profile opens
- **Then** Chat display shows Verbose chat on, and Settings has no Chat section

#### Scenario: Moved items are announced in Protection
**Traces to**: User Story 6, Acceptance Scenario 5
**Category**: Happy Path
- **Given** Skills & Tools has landed
- **When** Protection opens
- **Then** an info notice says tool permissions and skill trust are set in Skills & Tools, with a link that opens it

#### Scenario: Device pairing stays hidden when switched on
**Traces to**: User Story 6, Acceptance Scenario 6
**Category**: Edge Case
- **Given** `sandbox.experimental.device_pairing_enabled: true`
- **When** each of the 11 sections opens, and search runs for "pair"
- **Then** no pairing control appears; search says device pairing is not available in Settings at the moment

#### Scenario: Deleting a stored key needs one confirm
**Traces to**: User Story 6, Acceptance Scenario 7
**Category**: Happy Path
- **Given** Keys & secrets with the key `GITHUB_TOKEN`
- **When** the person presses Delete on that row and confirms
- **Then** no password prompt opened, the row is gone, and `GET /credentials` no longer lists it

#### Scenario: The server deletes a key without a password token
**Traces to**: User Story 6, Acceptance Scenario 7
**Category**: Happy Path
- **Given** a signed-in admin and a stored key `GITHUB_TOKEN`
- **When** `DELETE /credentials/GITHUB_TOKEN` is sent with no `X-Reauth-Token`
- **Then** the answer is today's success status, the key is gone, and an audit entry records the deletion

#### Scenario: Adding a key still needs the password
**Traces to**: User Story 6, Acceptance Scenario 7
**Category**: Error Path
- **Given** a signed-in admin
- **When** `POST /credentials` is sent with no `X-Reauth-Token`
- **Then** the answer is 403 and nothing is stored

#### Scenario: Rotating the gateway token asks first
**Traces to**: User Story 6, Acceptance Scenario 8
**Category**: Alternate Path
- **Given** Gateway
- **When** the person presses Rotate
- **Then** a confirm says every app and program using the token is signed out; Cancel keeps the old token

#### Scenario: Limits holds three groups
**Traces to**: User Story 6, Acceptance Scenario 9
**Category**: Happy Path
- **Given** Limits
- **When** it opens
- **Then** it shows the groups Context and tool results, Model overrides, and Agents and goals, each collapsed

#### Scenario Outline: Every inventory control is where the map says
**Traces to**: User Story 6, Acceptance Scenario 10
**Category**: Happy Path
- **Given** the 88-row map of design-notes §2 with the corrections in "Badge corrections from the code"
- **When** the control of row `<row>` is looked for
- **Then** it is `<place>`

**Examples**: Dataset D-9 (the qa-lead imports all 88 rows; D-9 lists the rows whose place changes behaviour).

#### Scenario: Sign-in is hidden in this package
**Traces to**: User Story 6, Acceptance Scenario 6
**Category**: Edge Case
- **Given** a signed-in person
- **When** they open `/settings?tab=signin`, `/settings?tab=devices`, and search "browsers"
- **Then** the first two open Providers with no error, the section list has no Sign-in, and search says "Signed-in browsers are not in Settings yet."

### Feature: States and accessibility (User Story 9)

#### Scenario Outline: Every section has a loading state
**Traces to**: User Story 9, Acceptance Scenario 1
**Category**: Happy Path
- **Given** the reads of `<section>` have not answered yet
- **When** `<section>` opens
- **Then** it shows "Loading `<section>`…" with a spinner and skeleton cards

**Examples**: the 11 section names in US-1 AS1.

#### Scenario: A section that cannot load
**Traces to**: User Story 9, Acceptance Scenario 2
**Category**: Error Path
- **Given** both Limits reads fail: `GET /settings/context` answers 500 with "context settings unreadable" and `GET /performance` answers 500 with "performance settings unreadable"
- **When** Limits opens
- **Then** each group shows an error Callout naming its failed read, quoting its cause, naming one thing to do and offering Retry, and no value in Limits can be saved (FR-066: every group's read failed)

#### Scenario: The gateway does not answer
**Traces to**: User Story 9, Acceptance Scenario 3
**Category**: Error Path
- **Given** `GET /about` fails with connection refused
- **When** Settings opens
- **Then** one error Callout between the page title and the sections says the gateway information could not be loaded, that values may be out of date, with Retry; on a phone it stays above the open section

#### Scenario: The skip link is the first Tab stop
**Traces to**: User Story 9, Acceptance Scenario 4
**Category**: Happy Path
- **Given** Settings just opened
- **When** the person presses Tab
- **Then** "Skip to settings content" has focus; Enter moves focus to the section content without changing the address

#### Scenario: Stacked dialogs return focus to their opener
**Traces to**: User Story 9, Acceptance Scenario 4
**Category**: Edge Case
- **Given** the Configure provider panel with the "Discard key?" confirm open over it
- **When** the person presses Escape
- **Then** only the confirm closes and focus returns to the control inside the panel that opened it

#### Scenario Outline: Touch targets and width on a phone
**Traces to**: User Story 9, Acceptance Scenario 5
**Category**: Edge Case
- **Given** a 390 px wide window
- **When** `<section>` opens
- **Then** every visible control measures at least 44 × 44 px and the page has no horizontal scroll

**Examples**: the 11 section names in US-1 AS1.

#### Scenario: Saving is announced
**Traces to**: User Story 9, Acceptance Scenario 6
**Category**: Happy Path
- **Given** a screen reader and 1 unsaved change
- **When** the person saves
- **Then** the live region announces "Changes saved." (and "Some need a gateway restart." when one does)

#### Scenario: Grouped lists start collapsed
**Traces to**: User Story 9, Acceptance Scenario 7
**Category**: Happy Path
- **Given** any section with a grouped list (Recap limits, Model overrides, Remote access, Always asks list, provider picker groups)
- **When** it opens
- **Then** each group's header has `aria-expanded="false"`

#### Scenario: Reduced motion stops animations
**Traces to**: User Story 9, Acceptance Scenario 5
**Category**: Edge Case
- **Given** the system asks for reduced motion
- **When** a section loads
- **Then** the skeleton does not pulse and the spinner does not rotate

#### Scenario: On-screen text carries no tags or support line
**Traces to**: User Story 9, Acceptance Scenario 2
**Category**: Edge Case
- **Given** every section in each of its states
- **When** its visible text is collected
- **Then** it contains no `#` followed by digits, no "R30", "S-Q" or similar tag, and no "contact support"

### Feature: Rider fixes on the same screens (User Story 10)

#### Scenario: About shows the build
**Traces to**: User Story 10, Acceptance Scenario 1
**Category**: Happy Path
- **Given** a gateway whose `GET /version` returns `build_sha` "abc1234"
- **When** About opens
- **Then** the build row shows "abc1234" and Copy includes it (#1560)

#### Scenario: The approval list names today's skill tools
**Traces to**: User Story 10, Acceptance Scenario 2
**Category**: Happy Path
- **Given** Protection → Approvals
- **When** the always-asks list is expanded
- **Then** it names `edit_skill` and `install_skill` and never `create_skill` or `remove_skill` (#1596)

#### Scenario: Delegated-run audit rows open their details
**Traces to**: User Story 10, Acceptance Scenario 3
**Category**: Happy Path
- **Given** an audit entry `external_cli.run` whose payload is under `fields` with `preset_args`
- **When** the person opens the audit log from Protection and expands that row
- **Then** "Show details" is offered and the details show `preset_args`; a row with `details` still opens as before (#1940)

#### Scenario: The audit viewer reads where the gateway writes
**Traces to**: User Story 10, Acceptance Scenario 4
**Category**: Happy Path
- **Given** a home whose default workspace is not `<home>/system`, and one audit entry written by the gateway
- **When** the audit log opens
- **Then** the entry is listed (#1742)

#### Scenario: A fresh install does not claim the sandbox is off
**Traces to**: User Story 10, Acceptance Scenario 5
**Category**: Edge Case
- **Given** a fresh install where `sandbox.mode` is empty in `config.json` and `GET /security/sandbox-status` reports `mode=enforce`, `kernel_sandbox_active=true`
- **When** Protection's health opens
- **Then** no "Sandbox is disabled" finding shows; with `sandbox-status` reporting `mode=off`, the finding shows (positive control) (#853)

#### Scenario Outline: Performance values cannot be changed around Settings
**Traces to**: User Story 10, Acceptance Scenario 6
**Category**: Error Path
- **Given** stored Performance values
- **When** `<caller>` tries to change `<value>`
- **Then** it is refused with a message naming Settings → Limits or `PUT /api/v1/performance`, and `config.json` is byte-identical

**Examples**: Dataset D-16.

#### Scenario: Every editable value has an agent path or a tested refusal
**Traces to**: User Story 10, Acceptance Scenario 6
**Category**: Edge Case
- **Given** the list of values this spec lets a person edit
- **When** `TestSettingsAgentWriteParity` runs
- **Then** each value has either an agent setter or a listed reason with a passing refusal through the real `set_config` tool; a value added to Settings without either fails the test

#### Scenario: Settings saves report a failed rebuild
**Traces to**: User Story 10, Acceptance Scenario 7
**Category**: Error Path
- **Given** a seam that makes the live rebuild fail after `PUT /config` or `PUT /settings/context` wrote `config.json` (for Context also a failing `triggerReloadAndWait`)
- **When** the person saves Gateway or Limits
- **Then** the response is 500 `ErrorResponse` with `code: saved_not_refreshed` and `details.keys` listing exactly the changed keys, decoded through the generated client; `GET /config/pending-restart` already lists those keys with `reason: apply_failed` when the response arrives; the screen shows them as "saved, not in use" and the banner lists them as "saved, not applied" (#995, Settings endpoints only)
- **And** a failure before `config.json` is written answers 500 without that code, and the screen treats it as unknown (C-6 outcome table)

#### Scenario: The chat-history clear button says what it clears
**Traces to**: User Story 10, Acceptance Scenario 8
**Category**: Happy Path
- **Given** Data & retention → Danger zone
- **When** it opens and the person presses the button
- **Then** the row and confirm say "Clear all chat history", not "sessions"; the action, its confirm and who may see it are unchanged (#1265 stays open)

#### Scenario: The agent edit panel does not cover Settings
**Traces to**: User Story 10, Acceptance Scenario 9
**Category**: Edge Case
- **Given** the person opened Edit on a System agent and then followed a link to another section
- **When** the new section shows
- **Then** no panel overlay remains and every control can be clicked (#1641; if it does not reproduce, the test stays as a guard)

---

#### Scenario: One failed read blocks only its group
**Traces to**: User Story 9, Acceptance Scenario 8
**Category**: Error Path
- **Given** `GET /settings/context` answers 500 and `GET /performance` works, Limits is open, and the person changed Tries per goal from 6 to 8 without saving
- **When** the Context read recovers and the person presses Retry on the Context group
- **Then** before Retry, Context and tool results and Model overrides showed the error and offered no saveable field; after Retry, only the Context groups were refilled, and Tries per goal still shows the unsaved 8 with the bar saying "1 unsaved change"

#### Scenario: A group whose read worked still saves while another group's read failed
**Traces to**: User Story 9, Acceptance Scenario 8
**Category**: Error Path
- **Given** `GET /settings/context` answers 500, `GET /performance` works, and the person changed Tries per goal from 6 to 8
- **When** the person presses Save changes and enters the right password
- **Then** exactly one write is sent, `PUT /performance`; `GET /performance` returns `goal_max_rounds: 8`; no `PUT /settings/context` is sent; the Context groups still show their error

#### Scenario: Data & retention keeps working when storage counts fail
**Traces to**: User Story 9, Acceptance Scenario 8
**Category**: Error Path
- **Given** `GET /storage/stats` answers 500 and `GET /settings/retention` answers 200
- **When** the person changes "Keep chat history for" and saves
- **Then** the counts show their error with Retry and the retention value is stored

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit (Go) | retention validation and sweep conditions (real sweep ticks, controlled clock), `RestartGatedKeys` and the apply-failed list, the effective-value guards (retention, Performance), `deleteCredential` gate, `set_config` refusals, the section-writer merge | rules in isolation, each with a positive control that proves the check can fail |
| Integration (Go, `httptest` against the gateway's handlers) | `/settings/retention`, `/settings/memory`, `PUT /config` refusals, `/config/pending-restart`, `/settings/context` concurrency (test barrier), `saved_not_refreshed` and refresh-failure seams, audit viewer path, sandbox health | the server contract end to end inside one process; no sleeps as the only oracle |
| Unit (Vitest + Testing Library) | Save bar counting, outcomes and success state, validation, leave guard, password flow, restart banner and completion check, legacy link mapping, search, group-level errors, Data & retention | screen rules with a mocked API (the API layer is the boundary; the components stay real); every mocked failure has a real-handler counterpart named in "Test Implementation Order" |
| E2E (Playwright, one targeted spec file) | the hands-on rows marked "stable" | the whole journey in a real browser against a real gateway; network interception only where a row names a declared seam |
| Gates before hand-back (review MIN-003) | `heavy npm run typecheck` (`tsc -b --noEmit`), `heavy npm run lint:design-system-locks`, `scripts/gen-contracts.sh` then `git diff --exit-code pkg/api/generated src/lib/api/generated`, `heavy golangci-lint run` on the touched packages | type, design-system, contract-drift and lint evidence, each saved with its exit code; separate from behaviour tests. Full suites and the design-system CI gate run at the cut |

### Hands-on UAT rows (written at spec time)

Rows G-19 to G-24 moved to `signed-in-browsers-spec.md`.

| Row | Acceptance criterion | Steps | Expected result | Affected area | Validator critical path | Automation candidate |
|---|---|---|---|---|---|---|
| G-01 | US-1 AS1, AS2 | Open Settings; open Sandbox & network; reload | 11 sections in 3 groups, no Sign-in; Sandbox & network still open after reload | shell, routing | open `/settings?tab=sandbox` directly | stable |
| G-02 | US-1 AS3–AS7 | Open each D-3 link | each lands per D-3 | routing | spot-check 4 D-3 rows incl. `devices` | stable |
| G-03 | US-1 AS8–AS10 | Search "web search", "backup", "pair", "browsers", "zzqx" | results per D-13 | search | repeat 2 queries | stable |
| G-04 | US-1 AS9, US-9 AS5 | 390 px: open each section | list first; back button; no sideways scroll; controls ≥ 44 px | layout | 3 sections | stable |
| G-05 | US-2 AS1–AS5 | Gateway: change Port, Discard; change, type back; type 80; then 1024 and save | bar counts follow the values; 80 blocks Save with the range text; 1024 saves | Save bar | Port 80 only | stable |
| G-06 | US-2 AS6 | Change a value; try every exit in both "way out" tables (in-app and leaving the page) | in-app exits show the app's question and Stay keeps the change and history; reload, another site and tab close show the browser's prompt | leave guard | section switch, Back, reload | stable for in-app rows; browser-prompt rows manual (the prompt cannot be scripted reliably in every browser) |
| G-07 | US-2 AS7 | (a) Limits: change Default context window to 32000 and Tries per goal to 8 (both valid on screen); the declared seam answers `PUT /settings/context` with 400; save. (b) Third-party agents: set the Codex path to `/opt/omnipus-test/no-such-codex`; save. (c) Limits: type 0 as a Model overrides context length | (a) Tries per goal saved, Default context window keeps 32000 with the seam's message, bar "1 change could not be saved"; (b) the real server's `cli_path_invalid` message under the Codex path; (c) the screen refuses 0 itself and sends nothing | Save, Limits, Third-party agents | (a) and (b) | stable: (a) uses the declared seam (Playwright route interception of `PUT /settings/context`); (b) and (c) need none |
| G-08 | US-3 AS1–AS3 | Sandbox & network: change two password rows; save with wrong, then right password; repeat with Cancel | one prompt listing both; wrong keeps it open; Cancel keeps drafts | password flow | right-password path | stable |
| G-09 | US-3 AS5, US-6 AS3 | Providers: change Active transcriber; save | Password badge; one prompt | Providers voice | same | stable |
| G-10 | US-4 AS1–AS3, AS7, AS9 | Production-served build (embedded SPA, real headers). Open the page directly on port 8080; change Port to 8081; save; Later; open About; Show changes; Restart now; follow Open the new address. Repeat behind a small forwarding proxy started by the spec file (proxy port fixed, gateway port changed, proxy re-pointed by the test) | window names `http://<host>:8081` and the proxy instruction before restarting; banner everywhere; running/saved values; the browser console shows no blocked request to port 8081; Open the new address appears only after port 8080 stops answering; on 8081 the page says the gateway restarted and shows no banner. Proxy run: no address offered; done reported from the proxy address | restart | Later + banner on About + new address | stable (direct and proxy) |
| G-11 | US-4 AS4, AS5 | Save Filesystem model; then the network proxy | both appear in Show changes as "needs a restart" | B2 | Filesystem model | stable |
| G-12 | US-4 AS6 | Protection: change Defense level; save | no badge, no window, no banner | restart | same | stable |
| G-13 | US-5 AS1–AS4 | Data & retention: check defaults; set 30/365; save; reload; type 366; Keep forever on | defaults 90/180/off; values persist; 366 blocked; day field off | retention UI | 366 + Keep forever | stable |
| G-14 | US-5 AS3 (server) | Not a hands-on row: the nightly memory sweep has no trigger a tester can press. Evidence is the Go test `TestRetroSweep_*` (order 7), which runs the real retro sweep tick with a controlled clock and includes the switch-off positive control. The tester only checks that Keep forever is stored and shown after a reload | stored and shown | B6 | read the Go test log and exit code | automated only (Go) |
| G-15 | US-5 AS5, AS6 | Open Memory & recaps; then set `storage.retention.disabled: true` and reload Data & retention | link row only; legacy notice | retention UI | notice | stable |
| G-16 | US-6 AS1, AS2 | Protection: God Mode button with an unsaved Auto-approve change | God Mode acts alone; draft stays | Protection | same | stable |
| G-17 | US-6 AS4–AS8 | Profile Chat display; Protection moved notice; pairing flag on; delete a key; rotate token | per scenarios | moved items | delete key | stable |
| G-18 | US-6 AS10 | Walk all 88 map rows | each in its mapped place | whole page | 15 random rows | reason not yet: one-off walk; D-9 rows become stable checks |
| G-25 | US-9 AS1–AS4, AS6, AS8 | Throttle network; stop gateway; make one Limits read fail (stop nothing else); keyboard-only and screen-reader pass | loading, error, offline and group-only error states; skip link; announcements | states, a11y | offline + group error + skip link | partly: states stable; screen-reader pass manual |
| G-26 | US-5 AS10 (chief, CRIT-002) | With Keep forever on (fixture F2) and an old reflection planted, record the `config.json` checksum, then send `PUT /config` with (a) `{"storage":{"retention":{}}}`, (b) `{"storage":null}`, (c) the permitted multi-section write `{"gateway":{"port":8081},"storage":{"retention":{"session_days":30,"memory_retros_days":400}}}`; (d) the raw `GET /config` object with `storage.retention` replaced by `{}` (existing fence); then (e) `{"gateway":{"port":8080},"storage":{"retention":{"session_days":30,"memory_retros_days":400,"memory_retros_forever":true}}}` (positive control, retention echoed as stored); reload Data & retention | (a)–(c) answer 400 with the retention message and the checksum is unchanged after each; (c) leaves Port at 8080; (d) answers 403 from the credential fence (counted for that fence only), checksum unchanged; (e) answers 200; Keep forever is still on in the screen after every step | retention guard | (b), (c) | stable (scripted requests) |
| G-28 | US-10 AS1–AS5, AS8 | About build row; Approvals list text; open an `external_cli.run` audit row; audit log lists a fresh entry; fresh-install health; Danger zone wording | per scenarios | riders | audit row + health | stable |
| G-29 | US-2 AS11, US-4 AS10 | Use the test build's apply-failure switch (named by backend-lead, test builds only) to fail the Defense level apply; save; Later; reload | banner lists Defense level as "saved, not applied" after the reload | failed apply | same | reason not yet: needs the test-only switch; covered by the Go and Vitest tests |

### Test Implementation Order

Rows 1–12 and 29 of the round-0 order moved to `signed-in-browsers-spec.md`.

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|---|---|---|---|---|
| 1 | TestUpdateConfig_RetentionGuardByEffect (subtests per D-12 row and fixture) | Integration (Go) | Keep forever survives any replace of its parent block; Writes that leave retention unchanged still save; A raw echo of the whole configuration meets the existing credential fence first | D-12. Real `updateConfig` through `httptest`, fresh `config.LoadConfig` after each request. RED: rows marked RED with fixture F1 (which exists at the baseline) fail on `c2f8f8e735`; that run is saved as a log with its exit code before any guard code is written. F2 subtests are added once `memory_retros_forever` exists. Mutation check: deleting the guard call makes every Refused row fail while Accepted rows still pass |
| 2 | TestRetentionSettings_Ranges (subtests validate_inbound on/off) | Integration (Go) | The server enforces the ranges; The retention write accepts each range end, whatever the validation setting | D-1, D-2 |
| 3 | TestRetentionSettings_ReadsOutOfRangeStoredValue | Integration (Go) | A hand-edited value outside the range is shown and blocks Save | GET returns 500 as stored |
| 4 | TestMemorySettings_RefusesRetentionFields (flag on/off, mixed body) | Integration (Go) | The memory settings refuse retention fields whatever the validation setting | MAJ-004 |
| 5 | TestSecurityRetentionRoute_Removed | Integration (Go) | The old retention route is gone | |
| 6 | TestUpdateConfig_PerformanceGuardByEffect / TestSetConfig_RefusesPerformanceValues | Integration (Go) | Performance values cannot be changed around Settings | D-16, #936 |
| 7 | TestRetroSweep_MemoryForeverKeepsRetros / _ForeverOffDeletes / _ForeverOffResumes / _LegacyDisabledKeepsAll | Unit (Go) | Keep forever stops memory deletion; positive control; resumes; legacy | real `executeRetroSweepTick`, controlled clock, completion signal |
| 8 | TestSearchRetros_FollowsRetentionWindow [pending FQ-1] | Unit (Go) | An old reflection kept by the setting can be recalled | real `SearchRetros` with 400-day, 730-day and older content; no stub |
| 9 | TestPendingRestart_IncludesFilesystemModelAndProxy | Integration (Go) | The server lists every restart-gated setting that was saved | D-4 |
| 10 | TestPendingRestart_ApplyFailedEntries | Integration (Go) | A failed hot apply stays in the banner after Later; A later successful apply clears a failed one | `reason: apply_failed` |
| 11 | TestContextSettings_ConcurrentDisjointEditsBothSurvive | Integration (Go) | Two tabs saving different Limits fields keep both changes | barrier, both orders |
| 12 | TestSaveBarWriters_MergeUnderLock (one subtest per writer) | Integration (Go) | same | backend-lead's writer list (C-6) |
| 13 | TestThirdPartySettings_OneRowEditKeepsOthers | Integration (Go) | Third-party agents saves all three rows | MAJ-001 |
| 14 | TestSettingsWrites_RefreshFailureReported / TestContextSettings_RefusesZeroOverride | Integration (Go) | Settings saves report a failed rebuild; A write that was stored but not applied says so; A stored write whose refresh failed and whose answer was lost is found by the saved-state check; A real server refusal is shown under its field | #995 seam: every row of the C-6 outcome table, asserted through the generated `ErrorResponse` (status, `code`, `details.keys`) and `GET /config/pending-restart` read right after; the zero-override refusal at the real handler |
| 15 | TestDeleteCredential_NoReauthNeeded / TestSetCredential_StillNeedsReauth | Integration (Go) | B7 scenarios | replaces ADR-022's `TestDeleteCredential_RequiresReAuth` |
| 16 | TestSettingsAgentWriteParity | Unit (Go) | Every editable value has an agent path or a tested refusal | FR-101 |
| 17 | TestHandleAuditLog_ReadsWriterDirectory / TestSecurityHealth_FreshInstallSandbox | Integration (Go) | audit viewer; fresh install health | #1742, #853 |
| 18 | settingsRoute.legacyTabs.test | Unit (Vitest) | Old and new section links land in the right place; Sign-in is hidden in this package | D-3 |
| 19 | saveBar.counting.test | Unit (Vitest) | Save bar scenarios; The saved signal stays long enough to read | D-10 |
| 20 | saveBar.leaveGuard.test | Unit (Vitest) | Every way out inside the app asks; Leaving the page uses the browser's own prompt (`beforeunload` registered) | |
| 21 | saveBar.outcomes.test | Unit (Vitest) | One of two writes fails; An unknown result is checked by reading back; A write that was stored but not applied says so; A stored write whose refresh failed and whose answer was lost is found by the saved-state check; A value changed elsewhere while the answer was lost | outcome classification in the order of FR-020 (recognized code before status); real-handler counterparts: orders 14 and E2E G-07 |
| 22 | passwordOncePerSave.test | Unit (Vitest) | Password scenarios | D-11 |
| 23 | sectionPasswordEndpoints.guard.test | Unit (Vitest) | Each section sends its password-marked values to at most one protected endpoint | runs the real save planner and counts requests at the network mock; an injected second protected endpoint must fail it |
| 24 | restartBanner.test / restartCompletion.test | Unit (Vitest) | Restart scenarios incl. old process still answering, port change, The new address confirms the restart on its own origin, proxy, no return | asserts the page requests only its own origin; the browser-policy proof is E2E G-10 on a production-served build, not this mock |
| 25 | dataRetention.section.test | Unit (Vitest) | Retention UI scenarios | uses the generated response validator |
| 26 | settingsSearch.test | Unit (Vitest) | Search scenarios | D-13 |
| 27 | settingsMap.test | Unit (Vitest) | Every inventory control is where the map says; rider UI rows | D-9 |
| 28 | sectionGroupErrors.test | Unit (Vitest) | A section that cannot load; One failed read blocks only its group; A group whose read worked still saves while another group's read failed; Data & retention keeps working when storage counts fail | |
| 29 | settings-redesign.spec.ts | E2E (Playwright) | rows marked stable | targeted file only |

### Test Datasets

#### Dataset D-1: Keep chat history for (screen field and `session_days`)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | (empty) | Empty | Screen: "This cannot be empty." then the range; Save off | BDD Scenario: An invalid value blocks saving and names the range | required field (design-notes §6) |
| 2 | 0 | Min − 1 | Screen: "Enter a whole number from 1 to 365."; server 400 `session_days must be a whole number from 1 to 365` | BDD Scenario: The server enforces the ranges | 0 meant "default 90" before; no longer accepted on write |
| 3 | 1 | Min | stored 1 | BDD Scenario: The server enforces the ranges | |
| 4 | 90 | Default | stored 90 | BDD Scenario: Fresh-install defaults | S-Q13 A |
| 5 | 365 | Max | stored 365 | BDD Scenario: The server enforces the ranges | |
| 6 | 366 | Max + 1 | screen range text; server 400 | BDD Scenario: The server enforces the ranges | |
| 7 | -1 | Negative | screen range text; server 400 | BDD Scenario: The server enforces the ranges | |
| 8 | 30.5 | Float | screen "Enter a whole number…"; server 400 | BDD Scenario: The server enforces the ranges | |
| 9 | "30" (JSON string) | Wrong type | server 400 | BDD Scenario: The server enforces the ranges | |
| 10 | 500 stored by hand | Out of range at rest | GET returns 500 (read schema has no range); shown with range error; Save off | BDD Scenario: A hand-edited value outside the range is shown and blocks Save | FR-046 |
| 11 | `null` | Null | server 400 | BDD Scenario: The server enforces the ranges | |
| 12 | 366 with `validate_inbound` false | Validation off | server 400, same message | BDD Scenario: The retention write accepts each range end, whatever the validation setting | handler check, not schema (MAJ-004) |
| 13 | 0 or -5 stored by hand | Legacy default at rest | GET returns 90 (effective default) | BDD Scenario: Fresh-install defaults | existing accessor rule |

#### Dataset D-2: Keep memory for and Keep forever

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | days 0 | Min − 1 | server 400 `memory_retros_days must be a whole number from 1 to 730` | BDD Scenario: The server enforces the ranges | |
| 2 | days 1 | Min | stored 1 | BDD Scenario: The server enforces the ranges | |
| 3 | days 180 | Default | stored 180 | BDD Scenario: Fresh-install defaults | S-Q13 A |
| 4 | days 730 | Max | stored 730 | BDD Scenario: The server enforces the ranges | new bound |
| 5 | days 731 | Max + 1 | server 400 | BDD Scenario: The server enforces the ranges | |
| 6 | forever true, no days | Alternate | stored forever true, days unchanged | BDD Scenario: With Keep forever on the day field is off and not sent | |
| 7 | forever true, days 999 | Invalid while forever | server 400 (days are validated whenever sent) | BDD Scenario: The server enforces the ranges | the screen never sends this (FR-040) |
| 8 | forever "true" (string) | Wrong type | server 400 | BDD Scenario: The server enforces the ranges | |
| 9 | `keep_everything_forever: true` | Read-only field | server 400 | BDD Scenario: The server enforces the ranges | config-file only |
| 10 | `{}` | Empty body | server 400 `at least one field required` | BDD Scenario: The server enforces the ranges | |
| 11 | `{"extra": 1}` | Unknown field | server 400 | BDD Scenario: The server enforces the ranges | `additionalProperties: false` |
| 12 | days 731 with `validate_inbound` false | Validation off | server 400 | BDD Scenario: The retention write accepts each range end, whatever the validation setting | |
| 13 | `{"memory_retros_forever": null}` | Null | server 400 | BDD Scenario: The server enforces the ranges | |

#### Dataset D-3: Section links (`/settings?tab=…`)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | (none) | Default | Providers | BDD Scenario: Person opens Settings and sees 11 sections in three groups | |
| 2 | providers, memory, system-agents, third-party-agents, limits, protection, sandbox, keys, gateway, data, about | New ids | that section | BDD Scenario: Old and new section links land in the right place | ids per wireframe `app.js::SECTIONS`, minus `signin` (hidden) |
| 3 | models | Legacy | Limits | BDD Scenario: Old and new section links land in the right place | |
| 4 | performance | Legacy | Limits | same | |
| 5 | integrations | Legacy | Providers (Voice input) | same | web search is in Skills & Tools |
| 6 | security | Legacy | Protection | same | |
| 7 | memory | Same id | Memory & recaps | same | |
| 8 | data | Same id | Data & retention | same | |
| 9 | devices | Legacy, target hidden | Providers, no error | same | Sign-in hidden in this package; returns with `signed-in-browsers-spec.md` |
| 10 | chat | Legacy | Profile → Chat display | same | also the chat leave-search link `/#/settings?tab=chat` |
| 11 | backup, backups, restore | Removed | Data & retention + "Backup and restore were removed." | same | #1919 |
| 12 | gateway&focus=god-mode | Legacy focus | Protection, God Mode button focused | BDD Scenario: The God Mode pill lands on the God Mode button | any tab with `focus=god-mode` |
| 13 | models&provider=openrouter&model=acme/m1 | Pre-fill | Limits, Model overrides expanded, filled row | BDD Scenario: The "No context length" link pre-fills a model override | |
| 14 | provider=openrouter&model=acme/m1 (no tab) | Pre-fill only | same as row 13 | same | today's rule (`settings.tsx::SettingsRoute`) kept |
| 15 | notasection | Unknown | Providers | BDD Scenario: Unknown section name falls back to Providers | |
| 16 | PROTECTION | Case | Providers | BDD Scenario: Unknown section name falls back to Providers | ids are lower-case exact |
| 17 | signin | Hidden section | Providers, no error | BDD Scenario: Sign-in is hidden in this package | unknown-value rule |

#### Dataset D-4: Restart-gated keys in `GET /config/pending-restart`

| # | Input (key saved ≠ running) | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `sandbox.mode` | Existing | listed | BDD Scenario: The server lists every restart-gated setting that was saved | already gated |
| 2 | `sandbox.filesystem_model` | New (B2) | listed with running and saved values | same | Inferred restart-gated; RED proves |
| 3 | `tools.exec.enable_proxy` | New (B2) | listed | same | Inferred; RED proves |
| 4 | `sandbox.workspace_path_guard` | Existing | listed | same | badge correction |
| 5 | `sandbox.allowed_paths` | Existing | listed | same | badge correction |
| 6 | `gateway.port` | Existing | listed | same | |
| 7 | `gateway.host` | Existing | listed | same | Bind address |
| 8 | `sandbox.prompt_injection_level` | Hot-reload | **not** listed | BDD Scenario: Defense level applies at once | |
| 9 | `sandbox.auto_approve` | Hot-reload | not listed | same | |
| 10 | `sandbox.god_mode` | Excluded by design | not listed | same | O14 |
| 11 | `gateway.port` saved back to running | Revert | not listed (empty list) | BDD Scenario: Saving the running value again clears the banner | |

#### Datasets D-5, D-6, D-7 — moved

Moved to `signed-in-browsers-spec.md` with the #1096 block (numbers kept).

#### Dataset D-8: Sections and their one password-protected endpoint (B4 guard)

| # | Input (section, all password rows changed) | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | Providers (Active transcriber) | One endpoint | 1 × `PUT /integrations/providers/{id}` | BDD Scenario: Each section sends its password-marked values to at most one protected endpoint | |
| 2 | Limits (six Agents-and-goals rows) | One endpoint | 1 × `PUT /performance` | same | |
| 3 | Protection (Auto-approve) | One endpoint | 1 × `PUT /security/sandbox-config` | same | |
| 4 | Sandbox & network (mode, filesystem model, workspace limit, allowed folders, internal addresses) | One endpoint | 1 × `PUT /security/sandbox-config` | same | |
| 5 | Memory & recaps, System agents, Third-party agents, Keys & secrets, Gateway, Data & retention, About | No password values | 0 | same | Keys' buttons act alone |

Counts are taken at the network mock while the **real** save planner runs (not a copy of this table); an injected second protected endpoint in one section must make the guard fail.

#### Dataset D-9: Inventory map rows whose place changes behaviour

| # | Input (design-notes §2 row) | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | 14–19 Models caps and window | Moved | Limits → Context and tool results | BDD Scenario: Every inventory control is where the map says | |
| 2 | 20 Model overrides | Moved | Limits → Model overrides (collapsed, S-Q12 A) | same | design notes row text says "open by default"; S-Q12 A wins |
| 3 | 21, 26, 74 old Save buttons | Merged | Save bar | same | |
| 4 | 27–31 Web search | Moved out | absent; search points to Skills & Tools | same | Skills & Tools lands first (FR-060) |
| 5 | 32–33 Voice | Moved | Providers → Voice input | same | Password badge on row 32 |
| 6 | 38, 39 Skill trust, tool grid | Moved out | absent; Protection notice | same | Skills & Tools lands first (FR-060) |
| 7 | 51 Delete credential | Kept | confirm only | same | B7 |
| 8 | 59 God Mode | Moved | Protection, button | same | |
| 9 | 61, 72, 73 retention | Kept / Removed / Moved | Data & retention, two settings; Memory link row | same | |
| 10 | 62, 63 Backup, Restore | Removed | absent; notice | same | #1919 |
| 11 | 75–77 Devices | Hidden | absent | same | #1916 |
| 12 | 79 Tool loading switch | Removed later | stays in Limits → Agents and goals until R31 ships | same | S-Q3 |
| 13 | 85 Per-control hidden Save + debounce | Removed | absent | same | |
| 14 | 86 Verbose chat | Moved out | Profile → Chat display | same | |
| 15 | 55 Rotate gateway token | Kept | confirm first | same | |

#### Dataset D-10: Field validation messages

The screen's own checks. "Accepted" rows feed "A value inside its range can be saved"; "Rejected" rows feed "An invalid value blocks saving and names the range". Ranges come from the wireframe and the server validators named in Notes, never from a mock.

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | Gateway → Port 80 | Min − | **Rejected**: "Enter a whole number from 1,024 to 65,535." | BDD Scenario: An invalid value blocks saving and names the range | |
| 2 | Port 65536 | Max + 1 | **Rejected**: same message | same | |
| 3 | Port 1024 | Min | **Accepted** | BDD Scenario: A value inside its range can be saved | |
| 4 | Port 65535 | Max | **Accepted** | same | |
| 5 | Limits → Tries per goal 0 | Min − 1 | **Rejected**: "Enter a whole number of at least 1." | BDD Scenario: An invalid value blocks saving and names the range | no upper bound |
| 6 | Idle reminder quiet window 29 / 3601 | Out of range | **Rejected**: "Enter a whole number from 30 to 3,600." | same | |
| 7 | Check-in interval 4 / 1441 | Out of range | **Rejected**: "Enter a whole number from 5 to 1,440." | same | validated even while check-ins are off |
| 8 | Max tool calls per turn 0 / 1001 | Out of range | **Rejected**: "Enter a whole number from 1 to 1,000." | same | |
| 9 | Max parallel agents (empty) | Empty allowed | **Accepted** (no fixed limit) | BDD Scenario: A value inside its range can be saved | empty means automatic |
| 10 | Default context window 512 | Inside server range | **Accepted** | same | server accepts any value ≥ 1 (`validateContextSettingsUpdate`); round-0 row 9 ("server-refused 512") was false and is removed |
| 11 | Model override context length 0 | Min − 1 | **Rejected**: "Context length must be a whole number of at least 1." | BDD Scenario: An invalid value blocks saving and names the range | today's screen check (`ContextSection.tsx::validate`) and the wireframe's `min="1"` stay; the server's own refusal of 0 is tested at the handler only (order 14) |
| 12 | Keep chat history for (empty) | Required | **Rejected**: "This cannot be empty." | BDD Scenario: An invalid value blocks saving and names the range | |
| 13 | Port "80a" | Not a number | **Rejected**: "Enter a whole number from 1,024 to 65,535." | same | |

#### Dataset D-11: Password prompt outcomes

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | right password | Happy | one reauth, one protected write, stored | BDD Scenario: Two password-marked changes need one password | |
| 2 | wrong password | Error | 401 → "The password is incorrect."; prompt open | BDD Scenario: Wrong password | |
| 3 | empty password, press Confirm | Empty | "Enter your password to continue."; no request | BDD Scenario: Wrong password | wireframe `reauth-ok` |
| 4 | 11th try within a minute | Rate limit | 429 → wait message | BDD Scenario: Too many password tries | limit 10/min |
| 5 | Cancel | Alternate | nothing sent; drafts kept | BDD Scenario: Cancelling the password prompt keeps every change and any typed key | #1153 |
| 6 | platform mode | Alternate | confirm only, no reauth | BDD Scenario: Platform mode asks for a confirmation instead | |
| 7 | 73-character password | Max + 1 | 400 from `ReAuthRequest` (maxLength 72) shown as the server's message | BDD Scenario: Wrong password | bcrypt limit |
| 8 | late 409 with a changed affected-agents list | Second consent | first: 1 reauth + 1 protected write (409); then confirm, 1 more reauth + 1 more write; no other write of the Save before the second consent | BDD Scenario: A late change in the affected agents asks again | FR-031 |
| 9 | platform mode, Cancel on the confirm | Platform cancel | 0 reauth, 0 writes | BDD Scenario: Platform mode asks for a confirmation instead | |

#### Dataset D-12: Writers other than `/settings/retention` (CRIT-002; rebuilt in round 2, review R2-MAJ-001)

Fixtures, both valid `config.json` files that differ only in `storage.retention`:

- **F1 (legacy, exists at the baseline):** `{"session_days": 30, "memory_retros_days": 400, "disabled": true}`. Effective values: 30, 400, Keep forever off, legacy keep-everything on.
- **F2 (new switch, usable once `memory_retros_forever` exists):** `{"session_days": 30, "memory_retros_days": 400, "memory_retros_forever": true}`. Effective values: 30, 400, Keep forever on, legacy off.

Each flag is tested in its own fixture, so one flag being true can never hide the loss of the other. After every request the test loads `config.json` fresh (`config.LoadConfig`) and reads all four effective values.

Three kinds of row, each consumed by its own scenario:

- **Refused** ("Keep forever survives any replace of its parent block"): 400 with the stated message, `config.json` byte-identical, and the four effective values equal to the fixture's.
- **Accepted** ("Writes that leave retention unchanged still save"): 200, the body's intended change is stored, and the four effective values equal the fixture's. No byte-equality check.
- **Fence** ("A raw echo of the whole configuration meets the existing credential fence first"): the existing fence's status. This is evidence for that fence only.

**RED** marks a row whose body passes every fence on today's code (`c2f8f8e735`: no credential key, no blocked path, protected agent defaults untouched, and the merged file decodes). With fixture F1 such a row stores the change today, so the test fails there. This is a source-derived expectation (`rest_config.go::updateConfig`, `pairing_config_write.go::prepareConfigJSONLocked`, `config_retention.go::IsDisabled`). The proof is the run saved during the failing-test step, not this table.

Message R = `retention settings can only be changed through PUT /api/v1/settings/retention`.

| # | Input (`PUT /config` body; fixtures) | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `{"storage":{"retention":{"session_days":90}}}`; F1, F2 | Leaf change (the one-level merge replaces the whole `retention` object) | **Refused**: 400 R | BDD Scenario: Keep forever survives any replace of its parent block | RED (F1): today `disabled` and `memory_retros_days` are dropped |
| 2 | `{"storage":{"retention":{}}}`; F1, F2 | Replace of `storage.retention` | **Refused**: 400 R | same | RED (F1); chief's case 1 |
| 3 | `{"storage":null}`; F1, F2 | Replace of `storage` (null) | **Refused**: 400 R | same | RED (F1); chief's case 2; `collectPaths` sees only `storage` |
| 4 | `{"storage":"x"}` and `{"storage":[]}`; F1 | Replace of `storage` with a wrong type | **Refused**: 400 `this write does not decode: <cause>` | same | Pins how decode failures are handled. Not RED evidence: today these end in a 500 whose outcome depends on the write path |
| 5 | `{"gateway":{"port":8081},"storage":{"retention":{"session_days":30,"memory_retros_days":400}}}`; F1, F2 | Permitted multi-section write (chief's whole-config case, inside the fences) | **Refused**: 400 R; Port stays 8080 | same | RED (F1) |
| 6 | F1: `{"storage":{"retention":{"session_days":30,"memory_retros_days":400,"DISABLED":false}}}`; F2: the same with `"MEMORY_RETROS_FOREVER":false` | Key casing that changes the decoded value | **Refused**: 400 R | same | RED (F1); encoding/json matches field names without regard to case |
| 7 | `{"gateway":{"port":8081},"storage":null}`; F1, F2 | Mixed: an allowed change plus a retention change | **Refused**: 400 R; Port stays 8080 | same | RED (F1); refused before any write |
| 8 | `{"storage":{"retention":{"session_days":30,"memory_retros_days":400}}}`; F1, F2 separately | Day counts echoed, one flag silently lost | **Refused**: 400 R | same | RED (F1). F1 loses the legacy flag, F2 loses Keep forever |
| 9 | `{"storage":{"other":1}}`; F1, F2 | Sibling key only | **Accepted**: 200; `config.json` holds `storage.other: 1` | BDD Scenario: Writes that leave retention unchanged still save | the merge keeps `retention` |
| 10 | `{"gateway":{"port":8081}}`; F1, F2 | Unrelated section | **Accepted**: 200; a fresh load shows Port 8081 | same | also the body shape of "Saving Gateway never sends retention" |
| 11 | F1: `{"gateway":{"port":8081},"storage":{"retention":{"session_days":30,"memory_retros_days":400,"disabled":true}}}`; F2: the same with `"memory_retros_forever":true` instead of `disabled` | Permitted multi-section write that echoes retention as stored | **Accepted**: 200; Port 8081 | same | the positive control for row 5 |
| 12 | `{"storage":{"retention.memory_retros_forever":false}}`; F2 | Dotted key name (no config field has that name) | **Accepted**: 200; the four effective values are unchanged | same | Inferred: the decoder ignores an unknown member. The effect decides, not the spelling |
| 13 | `{"Storage":{"Retention":{"memory_retros_forever":false}}}`; F2 | Harmless alternate casing | **Accepted**: 200; the four effective values are unchanged | same | Inferred (high confidence): the file is written with sorted keys, so `Storage` comes before `storage`, and the later canonical member, which holds `memory_retros_forever: true`, wins on decode. The guard decodes the same bytes |
| 14 | the raw object from `GET /config`, unchanged; F2 | Whole-config echo | **Fence**: 403 from the credential-field fence (the body carries `providers`); byte-identical | BDD Scenario: A raw echo of the whole configuration meets the existing credential fence first | Not retention-guard evidence; the fence must stay |
| 15 | the raw object from `GET /config` with `storage.retention` replaced by `{}`; F2 | Whole-config echo that removes retention | **Fence**: 403 as row 14; Keep forever still on | same | Chief's case 3. Keep forever survives, but because of the fence |

#### Dataset D-13: Search queries

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | "port" | Label | Gateway → Port | BDD Scenario: Search results | |
| 2 | "PORT" | Case | same as row 1 | same | case-insensitive |
| 3 | "web search" | Moved out | moved to Skills & Tools, with link | BDD Scenario: Searching for a moved setting says where it went | |
| 4 | "backup" / "restore" | Removed | "Backup and restore were removed." | same | |
| 5 | "pair" | Hidden | device pairing is not available in Settings at the moment | BDD Scenario: Device pairing stays hidden when switched on | |
| 6 | "retention" | Description | Data & retention → both settings | BDD Scenario: Search results | |
| 7 | "zzqx" | No match | Empty "No settings match" | BDD Scenario: Search with no match shows an empty state | |
| 8 | "   " | Whitespace | full section list, no results panel | BDD Scenario: Search results | |
| 9 | `<script>` | Special chars | treated as text; no match | BDD Scenario: Search results | |
| 10 | "browsers" | Hidden section | "Signed-in browsers are not in Settings yet." | BDD Scenario: Sign-in is hidden in this package | |

#### Dataset D-16: Performance values outside `PUT /performance` (#936)

| # | Input (caller, value) | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `PUT /config` `{"performance":{"max_parallel_agents":64}}` | REST leaf | 400 naming `PUT /api/v1/performance`; file unchanged | BDD Scenario: Performance values cannot be changed around Settings | |
| 2 | `PUT /config` `{"performance":null}` | REST parent replace | 400 when a stored value would change | same | by effect, like D-12 |
| 3 | `PUT /config` `{"planning":{"goal_max_rounds":99}}` | REST leaf | 400 | same | |
| 4 | `PUT /config` `{"tools":{"manifest":{"compressed":false}}}` | REST leaf | 400 | same | |
| 5 | `set_config` `performance.max_parallel_agents` = 64 | Tool | refused (unknown section, pinned) | same | already refused today |
| 6 | `set_config` `planning.goal_max_rounds` = 99 | Tool | refused (pinned) | same | already refused today |
| 7 | `set_config` `tools.manifest.compressed` = false | Tool | refused, message names Settings → Limits | same | **new** refusal |
| 8 | `set_config` `tools.manifest` = `{}` | Tool, parent | refused | same | at or under the key |
| 9 | `PUT /config` `{"performance":<stored performance object>,"planning":<stored planning object>}`, unchanged values | Positive control | 200; effective values unchanged | same | a permitted payload, not a raw `GET /config` echo (that meets the credential fence: 403) |

### Regression Test Requirements

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|---|---|---|---|
| Delete credential requires reauth | `TestDeleteCredential_RequiresReAuth` (ADR-022) | Replaced by `TestDeleteCredential_NoReauthNeeded` | B7, founder S-Q11 B; FQ-2 |
| Pending-restart excludes hot keys and God Mode | `rest_pending_restart_test.go` | No — keep; add D-4 rows 2–3 | |
| Memory settings round trip | `rest_memory_settings` tests | Yes — retention fields removed | |
| `/security/retention` GET/PUT | `rest_retention_test.go`, `api_e2e_test.go`, `routes_admin_test.go` | Yes — those cases now expect 404; sweep cases stay | |
| Max tool calls lowering flow (#904) | `PerformanceSection.maxToolIterations.test.tsx`, Go #904 tests | No — must stay green under the Save bar | |
| Chat leave-search link `/#/settings?tab=chat` | `-workspaces.$workspaceId.chat.leaveSearch.test.tsx` | Yes — it now lands on Profile → Chat display | |
| Whole-config `PUT /config` round trip | `rest_config` tests | Yes — D-12 row 9 and D-16 row 9 stay 200 | the guards must not refuse unchanged values |
| #904 protected agent defaults guard | `rest_config` #904 tests | No — must stay green | the new guards reuse its pattern |
| Third-party agents whole-section save | `rest_third_party_settings` tests | No — must stay green | C-6 relies on it |
| Restart modal success toast | `GatewayRestartModal` tests | Yes — success now needs a fresh `/about` uptime | FR-063 |

---

## Functional Requirements

Numbers are grouped by area; gaps are left on purpose. Numbers never move: requirements that left with the signed-in browsers block are listed as moved. "MUST" is the requirement; on-screen wording follows the wireframe (`app.js`) and `omnipus-voice`.

### Structure, links and search (User Story 1)

- **FR-001**: The Settings screen MUST show 11 sections in three groups, in this order: AI & agents (Providers, Memory & recaps, System agents, Third-party agents, Limits); Security (Protection, Sandbox & network, Keys & secrets); System (Gateway, Data & retention, About). With no section named in the address it MUST open Providers. Sign-in is hidden in this package (moved to `signed-in-browsers-spec.md`). The old tabs Models, Performance, Integrations, Security, Context, Credential vault, Devices, Chat and Backup MUST NOT appear.
- **FR-002**: The open section MUST be named in the address as `tab=<id>` with the ids of D-3 row 2; a reload MUST reopen it, and each section switch MUST add one browser history entry so Back and Forward move between sections (#1154).
- **FR-003**: Every old `tab=` value MUST open the section listed in Dataset D-3; an unknown or differently cased value, and the hidden section's `signin` and `devices`, MUST open Providers without an error; `backup`, `backups` and `restore` MUST show the notice "Backup and restore were removed." in Data & retention; `chat` MUST open Profile at Chat display.
- **FR-004**: `provider=<p>&model=<m>`, with or without `tab=models` or `tab=limits`, MUST open Limits with Model overrides expanded and one new unsaved row filled with that provider and model (today's pre-fill rule in `src/routes/_app/settings.tsx::SettingsRoute`, kept).
- **FR-005**: `focus=god-mode` with any `tab` MUST open Protection and move keyboard focus to the God Mode button, then remove `focus` from the address without a new history entry.
- **FR-006**: Search settings MUST match the typed text case-insensitively as a plain substring over each setting's label and description (no new dependency), list results with their section, announce the count in a polite live region, show the kit Empty state "No settings match" with the typed text when nothing matches, and treat typed markup as text.
- **FR-007**: Search MUST also answer for moved, removed and hidden items: web search, tool permissions and skill trust → "moved to Skills & Tools" with a link; backup and restore → "Backup and restore were removed."; device pairing → "Device pairing is not available in Settings at the moment."; signed-in browsers → "Signed-in browsers are not in Settings yet."
- **FR-008**: Below 768 px wide, the list MUST show first; choosing a section MUST show only that section with a "Settings" back button that returns to the list.
- **FR-009**: Every in-app link into Settings MUST land per the table in "Links from other screens keep working": Omnipus chat gear → System agents; third-party agent profile → Third-party agents; Skills permissions link → Skills & Tools (Settings no longer holds the grid); default model card "No context length" → Limits per FR-004; `/app/policies` → Settings.

### One save rule (User Story 2)

- **FR-010**: A changed value MUST NOT be stored until Save changes is pressed (S-Q2 A) — including recap model slots, Gateway fields, rate limits and the Limits fields that today save after a delay (this removes `useAutoSave` from Settings, #346). Buttons (Test, Check, Rotate, Restart, God Mode, Delete, Clear, Add key, Connect a provider) MUST act at once and MUST NOT enter the Save batch.
- **FR-011**: The Save bar MUST appear within 100 ms of a change and show "N unsaved change(s)", "N need(s) a gateway restart" and "N need(s) your password" for the changed values, with Discard and Save changes. A value typed back to its saved value MUST stop counting; text in an "Add …" field MUST count only after Add is pressed. With zero changes the bar MUST be hidden, except during the saved signal of FR-018.
- **FR-012**: Discard MUST return every unsaved draft of the section to the last **saved** value known to the screen (from a successful write's answer or the saved-state check of C-6, never from a running-settings read that disagrees with a known saved value) and store nothing. It acts only on actual drafts: a value whose outcome is saved (in use or not) is not a draft and is not offered for Discard. For a write whose result is still unknown, its confirm says the server may already hold the new value (FR-020).
- **FR-013**: The screen MUST validate each field with the range and message of Dataset D-10 (and D-1, D-2 for retention); while any value is invalid, Save changes MUST carry `aria-disabled="true"` and stay focusable, the bar MUST say "Fix N value(s) before saving", and pressing Save changes MUST move focus to the first invalid field and send nothing (`review-response.md::BC-D1`).
- **FR-014**: With unsaved changes, every exit **inside the app** (section list, search result, Profile link, app sidebar, browser Back and Forward, a typed change after `#` only, the phone's back button) MUST ask "Leave without saving N change(s)?" with Stay and Leave; Stay MUST keep the section, its changes, the address and the history unchanged. Every way of **leaving the page** (reload, a typed address of another page, closing the tab) MUST register the browser's own leave prompt (`beforeunload`), whose wording the browser decides.
- **FR-015**: Save MUST send each changed value to its own endpoint as a partial update carrying only the changed fields, except Third-party agents (FR-022). When some writes fail, each outcome is shown per FR-020; a refused write keeps its typed value with the server's message under its field, and the bar says "N change(s) could not be saved". A connection failure is an unknown result (FR-020).
- **FR-015a**: In the sandbox section, when the server refuses a path in allowed_paths or ssrf.allow_internal that was a new draft in this Save, that path MUST leave the list and go back into the add input of its own list (for ssrf.allow_internal, the network editor's add box, whose advanced part opens) with the server's message under that add input, so a later Save does not send it again; every other draft stays staged and nothing the person typed is lost. A path that was already saved before this Save and is refused stays in its row with the message under it. If the add-path input holds text typed after the Save was pressed, or the refused row no longer holds what was sent, the refused path is not moved: the message shows under the whole sandbox configuration and the lists and the input stay as they are. A refusal from an older Save never moves, removes or relabels a newer draft (release B09, 2784dec34 and 4a7d2f269; founder Q-SB1 B, 2026-11-28). A refused path moved back into its add input is no longer an unsaved change: it is not counted in the Save bar's "N change(s) could not be saved", and if no other draft is left the bar closes; the message under the input remains.
- **FR-016**: Pre-save confirms MUST run before the password prompt and before any write: the affected-agents list when Max tool calls per turn is lowered (#904 flow, kept), and "Listen on all interfaces?" when Bind address becomes All interfaces. Cancel MUST store nothing and keep the changes.
- **FR-017**: While the Save bar shows, Save changes MUST be the only gold (primary) button on the screen; other primary buttons MUST render outlined. An open modal surface (any dialog or side panel that blocks the page behind it, for example the password prompt, the restart dialog, Rotate, Add credential, the sign-in Done step and the voice key side panel) keeps its own single gold primary action; while it is open, the page behind it is not counted. A panel that opens inside the page without blocking it (for example the search key panel) is part of the page and is outlined (team-lead ruling 2026-11-21).
- **FR-018**: After a Save with no change left, the bar MUST show "Saved at HH:MM" (local time, 24-hour) for 8 seconds, then hide; a new edit replaces it at once; leaving the section hides it. The live region MUST announce "Changes saved." once, plus "Some need a gateway restart." when one does.
- **FR-019**: Drafts MUST belong to one section; leaving it resolves them (FR-014). Two tabs editing the same section keep each other's untouched fields because only changed fields are sent (FR-015) and the server merges them under its lock (FR-021); Third-party agents is the stated exception (FR-022).
- **FR-020**: Each write MUST end in one of four outcomes, shown per value, classified in this order (C-6 outcome table): (1) **saved, not in use** — a recognized signal, whatever the status: 500 with `code: saved_not_refreshed` (keys from `details.keys`), 200 with `requires_restart: true`, or 200 `PerformanceSettings` with `pending_apply`; shown as saved with "Saved, but not in use yet. Restart the gateway to apply it." and listed in the banner; (2) **saved and in use** — any other 2xx; (3) **not saved** — 4xx refusal: draft kept, message under the field; (4) **unknown** — any other 5xx, connection failure or lost response: the screen runs the saved-state check of C-6 (`GET /config/pending-restart` plus the section's read): saved value equal to the draft → saved (in use when the key is not listed, not in use when it is listed); equal to the value before the Save → not saved; equal to neither → the draft stays as an unsaved change, the newly read value becomes the saved value, and the field says "This setting was changed somewhere else while you were saving. Saved value: <value>."; if either read fails, the bar says "N change(s) may not have been saved" with Retry, and the draft stays. A running-settings read alone never turns a value into "not saved".
- **FR-021**: Every writer the Save bar uses MUST apply only the fields present in the request to the stored values read inside the config write lock; `rest_context_settings.go::putContextSettings` changes accordingly, and backend-lead records the check for each writer named in C-6. Two Saves changing different fields MUST both survive in either order; on the same field the later write wins; a list value is one field.
- **FR-022**: Third-party agents MUST save all three rows in one `PUT /settings/third-party-agents`, built from the values loaded when the section opened plus this tab's drafts; two tabs saving this section at once is last-save-wins for the whole section (C-6). After a 200 the section shows the response body; after `saved_not_refreshed` it shows the rows it sent as saved, not in use, and MUST NOT replace them with a read of the running settings; after an unknown result it runs the saved-state check (FR-020).

### Password once per Save (User Story 3, B4)

- **FR-025**: The Password badge MUST be on exactly these rows: Active transcriber (Providers); the six Agents-and-goals rows (Limits); Auto-approve (Protection); Sandbox mode, Filesystem model, Shell workspace limit, Allowed folders and Internal network addresses (Sandbox & network); the God Mode button. Each badge carries an icon and the word.
- **FR-026**: In local mode, a Save containing any password-marked change MUST open exactly one password prompt that lists, by visible label, every password-marked setting in the Save and nothing else.
- **FR-027**: After the right password, the screen MUST send exactly one `POST /auth/reauth` and then all password-marked values of the section in exactly one request to the section's one protected endpoint (Dataset D-8), carrying the token in `X-Reauth-Token`. No server change (C-4).
- **FR-028**: The typed password MUST be cleared from the prompt and from page state as soon as the one `POST /auth/reauth` returns, whatever its result; no later request may carry it.
- **FR-029**: Wrong password (401) MUST keep the prompt open with "The password is incorrect." under the field and focus in it; an empty submit MUST say "Enter your password to continue." without a request; rate-limited (429) MUST say too many tries were made and to wait a minute; Cancel MUST store nothing, close only the prompt, keep every change and keep any side panel with its typed key open (#1153), returning focus to the control that opened the prompt.
- **FR-030**: A guard test MUST run the real save planner and fail when any section's password-marked values reach more than one protected endpoint; an injected second protected endpoint must make it fail (positive control).
- **FR-031**: If the protected write answers 409 with a changed affected-agents list (max tool calls lowering), the screen MUST send no other write of that Save, show the new list for confirmation and ask for the password again (a second `POST /auth/reauth`); nothing may be stored before that.
- **FR-032**: In platform mode (sign-in through omnipus.ai) a Save with password-marked changes MUST show a confirmation listing them, with no password field and no `POST /auth/reauth`.

### Restart (User Story 4, B2)

- **FR-033**: After a Save that stored at least one restart-gated value, the existing "Restart now?" window MUST open listing those settings by label, with Later and Restart now.
- **FR-034**: A write response with `requires_restart: true` MUST open the same window naming that setting, even when the row has no Needs restart badge (for example a failed prompt-guard reload).
- **FR-035**: While `GET /config/pending-restart` lists entries, every section MUST show the banner "N saved change(s) wait(s) for a gateway restart" with Restart now and Show changes; Show changes MUST list each entry's label, running value (`applied_value`, "unknown" when null) and saved value (`persisted_value`), and mark it "needs a restart" or "saved, not applied" from `reason`. After Later or Escape, focus MUST move to the banner's Restart now; after a finished restart, to the open section's heading.
- **FR-036**: `pkg/gateway/rest_pending_restart.go::RestartGatedKeys` MUST add `sandbox.filesystem_model` and `tools.exec.enable_proxy` (`pkg/sysagent/tools/config.go::isRestartRequired` mirrors `tools.exec.enable_proxy`), and the `PendingRestartEntry` and `/config/pending-restart` descriptions MUST list the full key set (C-2). God Mode stays excluded.
- **FR-037**: The Needs restart badge MUST be on exactly the rows whose keys are in `RestartGatedKeys` after FR-036 (including Shell workspace limit and Allowed folders), and MUST NOT be on Defense level or any hot-reloaded row.
- **FR-038**: The banner MUST disappear when the list becomes empty — after a restart, after a key is saved back to its running value, or after a failed apply succeeds on a later save.
- **FR-039**: When `GET /config/pending-restart` fails, the banner area MUST say the list of waiting changes could not be loaded, with Retry.
- **FR-062**: `GET /config/pending-restart` MUST also list, with `reason: apply_failed`, every hot-reloaded key whose last save did not apply since boot (C-2 addition); the entry survives page reloads and section changes and is removed by a later successful apply of that key or by a restart.
- **FR-063**: Every restart check MUST be a same-origin request; the page's connection policy and the gateway's cross-origin rules MUST NOT be widened for it. When the page's own address keeps serving the gateway, completion MUST be proved by the page's own `GET /about` showing `uptime_seconds` smaller than the seconds since Restart now was pressed, then `GET /config/pending-restart` no longer listing the restarted keys; the first answer from the old process never counts. When `gateway.port` or `gateway.host` is among the restarted keys: if the page's own port equals the running gateway port, the window MUST say before restarting the derived new address **and** the proxy instruction ("If you reach Omnipus through a proxy, point it at port N instead"), because equal ports suggest but do not prove a direct connection; it polls only its own `/health`, and once that stops answering it MUST show Open the new address, a plain link to the derived address with `/settings?tab=gateway&restarted=1`, which the person follows; Settings at that address (after sign-in if needed) MUST check `GET /config/pending-restart` there and say the gateway restarted, or show the banner if the key is still listed. A new address is derived only when it needs no guess (a changed port with the page's own host; a changed host only when it is all interfaces or the page's own host); otherwise the window shows the listen address and the instruction only. If the page's port differs from the running port (a proxy), it MUST say the new port and that the proxy must point there, offer no address, and check completion on its own address. With no change within 60 s (no new process on its own address, or the old address still answering with an old uptime) it MUST say the gateway did not come back or did not restart, to check that it is running and read its log, keep Restart now, and keep the banner.

### Retention (User Story 5, B3 and B6)

- **FR-040**: While Keep forever is on, "Keep memory for" MUST be disabled, not validated by the screen, and not sent; the Save body MUST carry `memory_retros_forever` and omit `memory_retros_days`.
- **FR-041**: `GET /settings/retention` MUST return `RetentionSettings` with all four fields: the stored day counts (0 or negative stored → the default 90 / 180), `memory_retros_forever` (default false), and `keep_everything_forever` mirroring `storage.retention.disabled`.
- **FR-042**: `PUT /settings/retention` MUST accept only `RetentionSettingsUpdate`: `session_days` an integer 1–365, `memory_retros_days` an integer 1–730 (validated whenever sent, also while Keep forever is on), `memory_retros_forever` a boolean; it MUST answer 400 naming the field and range for anything else, for `keep_everything_forever`, for an unknown field, and for `{}`. These checks MUST run in the handler whatever `gateway.validate_inbound` is (review MAJ-004).
- **FR-043**: `PUT /settings/retention` MUST apply the sent fields to the stored values read inside the config write lock, write once, answer 200 with `RetentionSettings`, and use `withAuth` plus `RequireNotBypass` with no password (C-3).
- **FR-044**: `PUT /config` MUST refuse, with 400 `retention settings can only be changed through PUT /api/v1/settings/retention` and before writing anything, any body that passes the existing fences and whose merged result changes any of the four effective retention values, whatever the shape — a leaf, a replaced `storage.retention`, a replaced or nulled `storage`, a permitted multi-section write, a key casing that changes the decoded value (Dataset D-12 Refused rows). A merged result that does not decode MUST be refused with 400 `this write does not decode: <cause>`, nothing written. A body that leaves the four values unchanged is accepted and its other changes are stored (D-12 Accepted rows). The existing credential-field and `blockedPaths` fences stay unchanged and run first; a raw `GET /config` echo stays a 403. `storage.retention` is not added to `blockedPaths`.
- **FR-045**: `MemorySettings` MUST drop `session_days` and `memory_retros_days`, and `PUT /settings/memory` carrying either MUST answer 400 naming it and change nothing — including the other fields of that body — whatever `gateway.validate_inbound` is.
- **FR-046**: A stored day count outside the new range (hand-edited) MUST be returned as stored by `GET` (the read schema has no range), applied as stored by the sweeps, and shown by the screen with the range error, with Save unavailable until fixed.
- **FR-047**: `GET` and `PUT /security/retention`, their handlers and the schemas `RetentionConfig`, `RetentionUpdateRequest`, `RetentionUpdateResponse` MUST be removed; `POST /security/retention/sweep` MUST keep working unchanged.
- **FR-048**: The legacy `storage.retention.disabled: true` MUST keep stopping both sweeps, MUST be readable only (config-file only; FR-044 refuses changing it through `PUT /config`), and Data & retention MUST show an info notice that chat history and memory are both kept forever because of a setting in the configuration file and that the day counts apply once it is removed; the day fields stay editable.
- **FR-049**: `storage.retention.memory_retros_forever: true` (new config key) MUST stop the memory sweep (`executeRetroSweepTick`) and only the memory sweep; turned off again, the next memory sweep MUST apply `memory_retros_days`. The switch's description MUST say that older reflections are deleted at the next nightly clean-up when it is turned off.
- **FR-050** *(pending FQ-1, recommended answer A)*: Memory recall (`pkg/agent/memory_adapter.go::SearchRetros`) MUST read retrospectives back as far as the memory retention keeps them: `memory_retros_days` (up to 730), or with no age limit while Keep forever or the legacy switch is on, with a bounded read so recall time does not grow without limit (backend-lead states the bound with its measurement). Dropped if FQ-1 is answered B.
- **FR-052**: Memory & recaps MUST show no retention field, only a row "How long memory is kept" that opens Data & retention.

### Moved, removed and hidden controls (User Story 6)

- **FR-051**: God Mode MUST be a "Turn on God Mode…" / "Turn off God Mode…" button in Protection after Approvals; it MUST act alone with its own password prompt and its own restart window, never enter or clear the Save batch, and Gateway MUST have no God Mode control.
- **FR-053**: Providers MUST hold a Voice input group: Active transcriber (a Save-batch value, Password badge) and one key row per voice service; a key row MUST open a side panel with the key field and show/hide only.
- **FR-054**: Verbose chat MUST move to Profile → Chat display, reading and writing the same per-browser preference it uses today; Settings MUST have no Chat section.
- **FR-055**: No device pairing control and no Sign-in section MUST appear in Settings in this package, even when `sandbox.experimental.device_pairing_enabled` is true (#1916; Sign-in moved).
- **FR-056**: `DELETE /credentials/{key}` MUST no longer call `requireReAuth` (S-Q11 B, C-5); it keeps `withAuth`, the existing admin and bypass guards and the audit entry. `POST /credentials` and `POST /credentials/rotate` MUST keep requiring the token. The screen MUST ask one confirm before deleting.
- **FR-057**: Rotate (gateway token) MUST ask a confirm saying every app and program using the token is signed out; Cancel keeps the token.
- **FR-058**: Limits MUST hold three collapsible groups: Context and tool results, Model overrides, Agents and goals.
- **FR-059**: Every row of the 88-row map (design-notes §2), as corrected by "Badge corrections from the code", MUST be in the place its row names, or absent for rows marked Removed, Hidden or Moved out (Dataset D-9).
- **FR-060**: Settings MUST be cut and landed only after the Skills & Tools page has landed with the tool permission grid, web search and skill trust (S-Q3 A: "Settings is built after Skills & Tools lands; no temporary moving block"). Readiness check at the cut: the Skills & Tools route exists on the landing base and renders those three controls. These controls are absent from Settings, and Protection shows an info notice linking to Skills & Tools. No transitional layout is built.
- **FR-061**: No backup or restore control MUST appear (#1919); old links show the FR-003 notice.

### States, accessibility and wording (User Story 9)

- **FR-065**: Each section MUST show "Loading <section>…" with a spinner and Skeleton cards until its reads answer.
- **FR-066**: When a section's reads fail, only the groups that need the failed read (the "Read dependencies" table) MUST show an error Callout naming what failed, quoting the server's cause, giving one thing to do and offering Retry, and only those groups MUST be blocked from saving; other groups keep their drafts and still save, and Retry re-reads only the failed read and never replaces a draft. A section is blocked as a whole only when every one of its reads failed (a section with one read, or Limits with both reads failing).
- **FR-067**: When the gateway does not answer, one error Callout MUST show between the page title and the sections (above the open section on a phone) saying values may be out of date, with Retry.
- **FR-068**: "Skip to settings content" MUST be the first Tab stop and move focus to the section content without changing the address.
- **FR-069**: Stacked dialogs MUST close one at a time on Escape or Cancel, returning focus to the control that opened each; when that control no longer exists, focus goes to the section heading. When the inner opener is gone but the dialog it sat in is still open, focus goes to that dialog (its first focusable control). When one action closes several stacked dialogs at once (for example Discard closes the confirm and its side panel), focus goes to the control that opened the outermost dialog that closed (for example Configure); the section heading applies only when that control no longer exists either (team-lead ruling 2026-11-21, CHECK R4-06; clarified the same day after the test and the build read the first wording differently).
- **FR-070**: At 390 px every visible control MUST measure at least 44 × 44 px and the page MUST NOT scroll sideways.
- **FR-071**: The Save bar and saved signal MUST be live regions; errors MUST use `role="alert"`.
- **FR-072**: Every grouped list MUST start collapsed (`aria-expanded="false"`), including Model overrides (S-Q12 A).
- **FR-073**: No on-screen text MUST contain an issue number, a requirement tag (R30, S-Q8 and similar) or "contact support".
- **FR-074**: *Moved to `signed-in-browsers-spec.md`.*
- **FR-075**: Under reduced motion, Skeleton pulse, spinner rotation and row highlight MUST stop. The row highlight is the short flash on the setting row a person reaches from a Settings search result or a deep link: the section opens, the row scrolls into view and flashes once (normal motion); under reduced motion the row is still scrolled into view and marked, without animation. Focus does not move to the row: it stays where opening the section puts it, and the mark is exposed to assistive technology as the current item. A row inside a group that is closed opens that group first; every setting the search index names must be reachable this way (team-lead clarification 2026-11-21, CHECK R4-05, R5-01).

### Moved to `signed-in-browsers-spec.md`

- **FR-076** to **FR-091**: *Moved to `signed-in-browsers-spec.md`* (same numbers, corrected there for review findings CRIT-001, MAJ-011, MAJ-015 to MAJ-018 and MIN-002).

### Riders (User Story 10)

- **FR-100** (#936): `PUT /config` MUST refuse, with 400 naming `PUT /api/v1/performance` and before writing anything, any body whose merged result changes the effective value of `performance.max_parallel_agents`, `planning.goal_max_rounds`, `planning.goal_reminder_quiet_seconds`, `planning.goal_stalled_checkin_enabled`, `planning.goal_stalled_checkin_minutes` or `tools.manifest.compressed` (Dataset D-16). `set_config` MUST refuse `tools.manifest.compressed` (added to `performanceOnlyConfigKeys`) and keep refusing the `performance.` and `planning.` keys; its description names Settings → Limits for these values.
- **FR-101** (#936): `TestSettingsAgentWriteParity` MUST enumerate every value this spec lets a person edit and fail unless each has an agent setter or a listed user-only reason with a passing refusal through the real `set_config` tool (C-7 tables; retention values: user-only, "deletes data on a schedule"; Context, Active transcriber, Sandbox, egress proxy and CLI runner values: the user-only reasons in the second C-7 table). A refusal the test observes but no C-7 table lists is a parity failure, not a pass.
- **FR-102** (#995, Settings endpoints only): `PUT /config` and `PUT /settings/context` MUST answer a failed live refresh or reload after `config.json` was written with 500 `ErrorResponse`, `code: saved_not_refreshed` and `details.keys` (the dotted keys written), and MUST record those keys per FR-062 inside the write lock before answering; a failure before the write answers 500 without that code. `PUT /settings/third-party-agents` adds `details.keys` and the record to its existing answer. `ErrorResponse.yaml` and `openapi.yaml` describe this before any code (C-6 outcome table). `rest_tool_policies.go` and `rest_agents.go` stay open on #995.
- **FR-103** (#1742): `HandleAuditLog` MUST read the audit files from the directory the gateway's audit writer uses.
- **FR-104** (#853, narrow): Protection's health check MUST read the running sandbox state (as `GET /security/sandbox-status` reports it) instead of the empty persisted `sandbox.mode`; `ResolvedMode` itself is not changed. security-lead reviews.
- **FR-105** (#1560): About MUST show the build commit from `GET /version` (`build_sha`) and include it in Copy.
- **FR-106** (#1596): The always-asks list (`src/lib/autoApproveAskGroups.ts`) MUST name the current skill tools and no retired ones.
- **FR-107** (#1940): An audit row MUST offer "Show details" when its payload is under `details` or `fields`.
- **FR-108** (#1265, wording only — Q5 A): The Danger zone row and confirm MUST say "Clear all chat history"; the action, its confirm step and who may see it stay as they are. #1265 is **not** resolved by this package.
- **FR-109** (#1641): After following a link from an open agent edit panel to another section, no overlay MUST remain over Settings.

---

## Riders

Chief-confirmed on 2026-11-20 (fix-round-1 brief). Riders that moved with #1096: #689, #1595, #1146, #598 (`signed-in-browsers-spec.md`). Riders cut by the chief's rule (new design or a new test layer): #1145 (build stamping is outside Settings; About shows whatever `GET /version` returns), #1917 (cause unknown, needs investigation).

| Issue | Title (short) | Disposition | Acceptance | Test | Docs |
|---|---|---|---|---|---|
| #1154 | Reload reopens Providers | Fixed | FR-002 | settingsRoute.legacyTabs.test; G-01 | DOC-001 |
| #346 | `useAutoSave` unmount flush swallows save errors | Fixed by removal | FR-010 (no autosave in Settings), FR-020 | saveBar.counting.test; a grep guard that Settings imports no `useAutoSave` | — (no user-visible change beyond DOC-003) |
| #1153 | Cancelling the password prompt loses the typed key | Fixed | FR-029 | passwordOncePerSave.test; G-08 | DOC-003 |
| #1596 | Ask-list names retired skill tools | Fixed | FR-106 | its vitest; G-28 | — (text matches the tools) |
| #1940 | Audit viewer cannot open `external_cli.run` | Fixed | FR-107 | audit row vitest; G-28 | — |
| #1265 | Clear sessions: admin-only, re-confirm, honest wording | **Touched, not resolved** (Q5 A): wording only | FR-108 | settingsMap.test (text); G-28 | DOC-004 |
| #1560 | About shows no build sha | Fixed | FR-105 | About vitest; G-28 | — |
| #1641 | Agent edit overlay stays over Settings | Fixed if it reproduces; guard test either way | FR-109 | settingsMap.test | — |
| #1742 | Audit viewer reads the wrong folder | Fixed | FR-103 | TestHandleAuditLog_ReadsWriterDirectory; G-28 | — |
| #853 | False "Sandbox is disabled" on a fresh install | Fixed, narrow; security-lead look | FR-104 | TestSecurityHealth_FreshInstallSandbox; G-28 | — |
| #936 | Performance values writable without the password | Fixed | FR-100, FR-101 | orders 6 and 16 | DOC-013 |
| #995 | Saves report success after a failed rebuild | Fixed for Settings endpoints only | FR-102 | TestSettingsWrites_RefreshFailureReported | DOC-003 |

---

## Success Criteria

- **SC-001**: 100 % of Dataset D-3 links open the listed target (automated, Vitest + Playwright).
- **SC-002**: 0 values stored without Save changes across every section (integration test counting writes per change).
- **SC-003**: In local mode, every Save with password-marked changes makes exactly 1 password prompt, 1 `POST /auth/reauth` and 1 protected write; a late 409 adds exactly one more of each after the new confirm; in platform mode 0 `POST /auth/reauth`; a cancelled prompt makes 0 writes (Dataset D-8, D-11, guard FR-030).
- **SC-004**: Every key of D-4 rows 1–7 appears in `/config/pending-restart` after a save that differs from the running value; rows 8–10 never appear as `restart_required`; a failed hot apply appears as `apply_failed`.
- **SC-005**: 100 % of Datasets D-1, D-2, D-12 and D-16 rows produce the stated status with `validate_inbound` on and off. Refused rows leave `config.json` byte-identical and the four effective values unchanged after a fresh load; Accepted rows store their intended change and leave the four effective values unchanged; Fence rows give the existing fence's status. D-12 rows marked RED fail on `c2f8f8e735` with fixture F1 (saved RED log), and removing the guard makes every Refused row fail again (mutation check).
- **SC-006**: 88 of 88 map rows found in their mapped place (hands-on G-18, D-9 automated).
- **SC-009**: 0 serious or critical axe violations on each of the 11 sections; every control ≥ 44 × 44 px at 390 px.
- **SC-012**: 0 lost changes in the two-tab test (both orders), and 0 "not saved" labels on values the server holds (unknown-result and saved-not-applied tests).
- SC-007, SC-008: moved to `signed-in-browsers-spec.md`.

---

## Reachability

| Deliverable | How a person or agent reaches it | Check |
|---|---|---|
| Settings screen (11 sections) | Route `/settings` (`src/routes/_app/settings.tsx`), from the account menu, the Omnipus chat gear and the in-app links of FR-009 | Playwright opens it from the account menu, not only by URL |
| Profile → Chat display | Route `/profile` (`src/routes/_app/profile.tsx`) | G-17 |
| `/settings/retention` | Called by Data & retention; registered next to `/settings/memory` | route-table test asserts the path and its middleware |
| `RestartGatedKeys` additions, `apply_failed` entries | Read by `GET /config/pending-restart`, shown by the restart banner | G-11, order 10 |
| Agent tools | No tool added. `set_config` gains one refusal (`tools.manifest.compressed`) and its description one sentence (C-7). Retention and Performance values are user-only with tested refusals (FR-101) | order 16 |
| Prerequisite | Skills & Tools landed on the landing base before the cut (FR-060) | checked by the chief at the cut |

**Where tests run.** RED (qa-lead): the Go tests of "Test Implementation Order" rows 1–17 in `pkg/gateway`, `pkg/config`, `pkg/sysagent/tools` and (FQ-1 A only) `pkg/agent`; Vitest rows 18–28 next to the Settings components; Playwright row 29 as one targeted spec file. GREEN (backend-lead, frontend-lead) runs only those targeted tests and the hand-back gates of the Test Hierarchy through `heavy` under the own-tests rule. CHECK (a different qa-lead instance) audits them. Full suites run once, at the cut.

**Hands-on UAT on the exact cut commit** is required if FQ-1 is answered A (it changes `pkg/agent`). Without that, this package touches no agent-engine code; the hands-on rows still run on the cut commit as the train's campaign.

---

## User-facing documentation

| ID | Page | TODO |
|---|---|---|
| DOC-001 | `docs/settings.md` | Rewrite the overview: 11 sections in three groups, what each holds, the search box |
| DOC-002 | `docs/settings.md` | Rewrite the old-tab → new-place map table from FR-003 and the 88-row map (`devices` → Providers for now) |
| DOC-003 | `docs/settings.md` | The save rule (Save bar, Discard, leave prompts), the four save outcomes and what "Saved, but not in use yet" means, the password once per Save, the Needs restart badge, the restart banner (including "saved, not applied"), and what the restart window does when the port changes (it shows the new address and the proxy instruction, and the person opens the new address once the old one stops answering; it cannot check the new address from the old page); what "This setting was changed somewhere else" means; Third-party agents saves all three programs together |
| DOC-004 | `docs/settings.md` | Data & retention: two settings with ranges and defaults, Keep forever for memory, the legacy config-file switch and its notice, that `/settings/retention` is the only write path and `PUT /config` refuses retention changes; "Clear all chat history" wording |
| DOC-005 | `docs/memory.md` | Replace the retention lines (today around lines 121–122) with a pointer to Data & retention, the 1–730 range, Keep forever, and the recall window per FQ-1 |
| DOC-006 | `docs/settings.md`, `docs/providers-and-models.md` | Limits groups, Voice input under Providers, Active transcriber needs the password |
| DOC-007 | `docs/security.md` | God Mode now in Settings → Protection; Auto-approve and its password; tool permissions and skill trust are in Skills & Tools |
| DOC-008 | `docs/using-omnipus-ui.md` | Verbose chat in Profile → Chat display; device pairing and signed-in browsers not in Settings |
| DOC-009 | — | *Moved to `signed-in-browsers-spec.md`* |
| DOC-010 | `docs/settings.md` | Keys & secrets: deleting a key asks a confirm, not the password; adding and rotating still ask the password |
| DOC-011 | the 19 user pages that name a Settings path (`grep -rcE 'Settings (→|>) \|tab=' docs` outside `docs/internal`: connectors, custom-endpoint, security, tutorial, goals, tools, using-omnipus-cli, settings, reference/built-in-tools, troubleshooting, agents, using-omnipus-ui, memory, providers-and-models, operations/sandbox-config, migration/model-list-migration, operations/sandbox-limitations, connectors/whatsapp_native, connectors/discord) | Update every "Settings → <old tab>" path and `?tab=` link to the new section name |
| DOC-012 | `docs/internal/architecture/ADR-022-credential-vault-reauth.md` | Dated correction: delete no longer needs the password (S-Q11 B, FQ-2), set and rotate still do |
| DOC-013 | `docs/tools.md` | `set_config` cannot change the Limits values or retention; they are changed in Settings with the password (Limits) or in Data & retention |

docs-verifier audits DOC-001 to DOC-011 and DOC-013 against the landed behaviour; DOC-012 is an internal record the architect writes at landing.

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) | DOC |
|---|---|---|---|---|
| FR-001 | US-1 | Person opens Settings and sees 11 sections in three groups | settingsRoute.legacyTabs.test; G-01 | DOC-001 |
| FR-002 | US-1 | Reload keeps the open section; Browser Back moves to the previous section | settingsRoute.legacyTabs.test | DOC-001 |
| FR-003 | US-1 | Old and new section links land in the right place; Unknown section name falls back to Providers; Sign-in is hidden in this package | settingsRoute.legacyTabs.test (D-3) | DOC-002, DOC-011 |
| FR-004 | US-1 | The "No context length" link pre-fills a model override | settingsRoute.legacyTabs.test (D-3 rows 13–14) | DOC-006 |
| FR-005 | US-1 | The God Mode pill lands on the God Mode button | settingsRoute.legacyTabs.test (D-3 row 12) | DOC-007 |
| FR-006 | US-1 | Search results; Search with no match shows an empty state | settingsSearch.test (D-13) | DOC-001 |
| FR-007 | US-1, US-6 | Searching for a moved setting says where it went; Device pairing stays hidden when switched on; Sign-in is hidden in this package | settingsSearch.test | DOC-001 |
| FR-008 | US-1 | On a phone the list comes first | settings-redesign.spec.ts; G-04 | DOC-001 |
| FR-009 | US-1 | Links from other screens keep working | settingsRoute.legacyTabs.test; chat leaveSearch test (regression) | DOC-011 |
| FR-010 | US-2 | A changed value shows the Save bar and stores nothing; A recap model choice waits for Save | saveBar.counting.test | DOC-003 |
| FR-011 | US-2 | A value changed back by hand is no longer counted; Typing in an Add field is not a change until Add is pressed | saveBar.counting.test | DOC-003 |
| FR-012 | US-2 | Discard returns every change to its saved value; An unknown result is checked by reading back | saveBar.counting.test; saveBar.outcomes.test | DOC-003 |
| FR-013 | US-2 | An invalid value blocks saving and names the range; A value inside its range can be saved | saveBar.counting.test (D-10) | DOC-003 |
| FR-014 | US-2 | Leaving with unsaved changes asks first; Every way out inside the app asks with the app's question; Leaving the page uses the browser's own prompt; Leave without saving drops the change | saveBar.leaveGuard.test; G-06 | DOC-003 |
| FR-015 | US-2 | One of two writes fails; A real server refusal is shown under its field; A Save while the gateway is down keeps every change | saveBar.outcomes.test; G-07 | DOC-003 |
| FR-016 | US-2 | Lowering the tool-call limit previews affected agents; Choosing All interfaces asks before saving | saveBar.counting.test; PerformanceSection.maxToolIterations.test (regression) | DOC-003 |
| FR-017 | US-2 | Only one gold button while the Save bar shows | saveBar.counting.test | — (visual only) |
| FR-018 | US-2, US-9 | The saved signal stays long enough to read; Saving is announced | saveBar.counting.test | DOC-003 |
| FR-019 | US-2 | Two tabs saving different Limits fields keep both changes | TestContextSettings_ConcurrentDisjointEditsBothSurvive | DOC-003 |
| FR-020 | US-2 | A write that was stored but not applied says so; An unknown result is checked by reading back; A Save while the gateway is down keeps every change; A stored write whose refresh failed and whose answer was lost is found by the saved-state check; A value changed elsewhere while the answer was lost | saveBar.outcomes.test; TestSettingsWrites_RefreshFailureReported | DOC-003 |
| FR-021 | US-2 | Two tabs saving different Limits fields keep both changes | TestContextSettings_ConcurrentDisjointEditsBothSurvive; TestSaveBarWriters_MergeUnderLock | — (no user-visible change) |
| FR-022 | US-2 | Third-party agents saves all three rows; A write that was stored but not applied says so | TestThirdPartySettings_OneRowEditKeepsOthers; saveBar.outcomes.test | DOC-003 |
| FR-025 | US-3 | Active transcriber needs the password | passwordOncePerSave.test | DOC-006 |
| FR-026 | US-3 | Two password-marked changes need one password | passwordOncePerSave.test (D-11) | DOC-003 |
| FR-027 | US-3 | Each section sends its password-marked values to at most one protected endpoint | sectionPasswordEndpoints.guard.test (D-8) | DOC-003 |
| FR-028 | US-3 | The typed password is not kept | passwordOncePerSave.test | DOC-003 |
| FR-029 | US-3 | Wrong password; Too many password tries; Cancelling the password prompt keeps every change | passwordOncePerSave.test (D-11) | DOC-003 |
| FR-030 | US-3 | Each section sends its password-marked values to at most one protected endpoint | sectionPasswordEndpoints.guard.test | — (internal guard) |
| FR-031 | US-3 | A late change in the affected agents asks again | passwordOncePerSave.test (D-11 row 8) | DOC-003 |
| FR-032 | US-3 | Platform mode asks for a confirmation instead | passwordOncePerSave.test | DOC-003 |
| FR-033 | US-4 | A Save that needs a restart offers it at once | restartBanner.test | DOC-003 |
| FR-034 | US-4 | A failed reload still says a restart is needed | restartBanner.test | DOC-003 |
| FR-035 | US-4 | Later leaves a banner on every section; Show changes lists running and saved values | restartBanner.test | DOC-003 |
| FR-036 | US-4 | The server lists every restart-gated setting that was saved | TestPendingRestart_IncludesFilesystemModelAndProxy (D-4) | DOC-003 |
| FR-037 | US-4 | Defense level applies at once | restartBanner.test; TestPendingRestart_IncludesFilesystemModelAndProxy (D-4 rows 8–10) | DOC-003 |
| FR-038 | US-4 | The restart clears the banner; Saving the running value again clears the banner; A later successful apply clears a failed one | restartBanner.test; TestPendingRestart_ApplyFailedEntries | DOC-003 |
| FR-039 | US-4 | The restart list cannot be loaded | restartBanner.test | DOC-003 |
| FR-040 | US-5 | With Keep forever on the day field is off and not sent | dataRetention.section.test | DOC-004 |
| FR-041 | US-5 | Fresh-install defaults | TestRetentionSettings_Ranges; dataRetention.section.test | DOC-004 |
| FR-042 | US-5 | The server enforces the ranges; The retention write accepts each range end, whatever the validation setting | TestRetentionSettings_Ranges (D-1, D-2) | DOC-004 |
| FR-043 | US-5 | Saving both day counts | TestRetentionSettings_Ranges | DOC-004 |
| FR-044 | US-5 | Keep forever survives any replace of its parent block; Writes that leave retention unchanged still save; A raw echo of the whole configuration meets the existing credential fence first; Saving Gateway never sends retention | TestUpdateConfig_RetentionGuardByEffect (D-12); G-26 | DOC-004 |
| FR-045 | US-5 | The memory settings refuse retention fields whatever the validation setting | TestMemorySettings_RefusesRetentionFields | DOC-005 |
| FR-046 | US-5 | A hand-edited value outside the range is shown and blocks Save | TestRetentionSettings_ReadsOutOfRangeStoredValue; dataRetention.section.test (D-1 row 10) | DOC-004 |
| FR-047 | US-5 | The old retention route is gone | TestSecurityRetentionRoute_Removed | DOC-004 |
| FR-048 | US-5 | The legacy keep-everything switch is shown, not hidden | TestRetroSweep_LegacyDisabledKeepsAll; dataRetention.section.test | DOC-004 |
| FR-049 | US-5 | Keep forever stops memory deletion, through the real sweep; The memory sweep deletes when Keep forever is off; Turning Keep forever off again resumes deletion | TestRetroSweep_* | DOC-004, DOC-005 |
| FR-050 | US-5 | [Pending FQ-1] An old reflection kept by the setting can be recalled | TestSearchRetros_FollowsRetentionWindow | DOC-005 |
| FR-051 | US-6 | God Mode sits in Protection; The God Mode button acts alone | settingsMap.test; G-16 | DOC-007 |
| FR-052 | US-5 | Memory & recaps holds only a link to retention | settingsMap.test | DOC-005 |
| FR-053 | US-6 | Voice input lives in Providers | settingsMap.test | DOC-006 |
| FR-054 | US-6 | Verbose chat lives in Profile | settingsMap.test | DOC-008 |
| FR-055 | US-6 | Device pairing stays hidden when switched on; Sign-in is hidden in this package | settingsMap.test | DOC-008 |
| FR-056 | US-6 | Deleting a stored key needs one confirm; The server deletes a key without a password token; Adding a key still needs the password | TestDeleteCredential_NoReauthNeeded; TestSetCredential_StillNeedsReauth | DOC-010, DOC-012 |
| FR-057 | US-6 | Rotating the gateway token asks first | settingsMap.test | DOC-001 |
| FR-058 | US-6 | Limits holds three groups | settingsMap.test | DOC-006 |
| FR-059 | US-6 | Every inventory control is where the map says | settingsMap.test (D-9); G-18 | DOC-002 |
| FR-060 | US-6 | Moved items are announced in Protection | settingsMap.test; cut checklist | DOC-007 |
| FR-061 | US-6 | Old and new section links land in the right place (D-3 row 11) | settingsRoute.legacyTabs.test | DOC-002 |
| FR-062 | US-4 | A failed hot apply stays in the banner after Later; A later successful apply clears a failed one | TestPendingRestart_ApplyFailedEntries; restartBanner.test; G-29 | DOC-003 |
| FR-063 | US-4 | A changed port shows where the gateway will answer; The new address confirms the restart on its own origin; Behind a proxy the window gives an instruction; An old process still answering does not count as restarted; The gateway does not come back | restartCompletion.test; G-10 | DOC-003 |
| FR-065 | US-9 | Every section has a loading state | settings-redesign.spec.ts; G-25 | — (states; no doc change needed) |
| FR-066 | US-9 | A section that cannot load; One failed read blocks only its group; A group whose read worked still saves while another group's read failed; Data & retention keeps working when storage counts fail | sectionGroupErrors.test; G-25 | DOC-001 |
| FR-067 | US-9 | The gateway does not answer | settings-redesign.spec.ts | DOC-001 |
| FR-068 | US-9 | The skip link is the first Tab stop | settings-redesign.spec.ts | — (a11y) |
| FR-069 | US-9 | Stacked dialogs return focus to their opener | settings-redesign.spec.ts | — (a11y) |
| FR-070 | US-9 | Touch targets and width on a phone | settings-redesign.spec.ts; G-04 | — (a11y) |
| FR-071 | US-9 | Saving is announced | saveBar.counting.test | — (a11y) |
| FR-072 | US-9 | Grouped lists start collapsed | settingsMap.test | — (a11y) |
| FR-073 | US-9 | On-screen text carries no tags or support line | settingsMap.test (text sweep) | — (wording rule) |
| FR-075 | US-9 | Reduced motion stops animations | settings-redesign.spec.ts | — (a11y) |
| FR-100 | US-10 | Performance values cannot be changed around Settings | TestUpdateConfig_PerformanceGuardByEffect; TestSetConfig_RefusesPerformanceValues (D-16) | DOC-013 |
| FR-101 | US-10 | Every editable value has an agent path or a tested refusal | TestSettingsAgentWriteParity | DOC-013 |
| FR-102 | US-10 | Settings saves report a failed rebuild; A stored write whose refresh failed and whose answer was lost is found by the saved-state check | TestSettingsWrites_RefreshFailureReported | DOC-003 |
| FR-103 | US-10 | The audit viewer reads where the gateway writes | TestHandleAuditLog_ReadsWriterDirectory | — (bug fix; no doc change) |
| FR-104 | US-10 | A fresh install does not claim the sandbox is off | TestSecurityHealth_FreshInstallSandbox | — (bug fix) |
| FR-105 | US-10 | About shows the build | settingsMap.test (About) | — (bug fix) |
| FR-106 | US-10 | The approval list names today's skill tools | autoApproveAskGroups test | — (text fix) |
| FR-107 | US-10 | Delegated-run audit rows open their details | audit row vitest | — (bug fix) |
| FR-108 | US-10 | The chat-history clear button says what it clears | settingsMap.test | DOC-004 |
| FR-109 | US-10 | The agent edit panel does not cover Settings | settingsMap.test | — (bug fix) |

FR-074 and FR-076 to FR-091 moved to `signed-in-browsers-spec.md`. Check: 81 active FRs (FR-074 and FR-076 to FR-091 are moved stubs), each with a story, at least one scenario and one named test; every acceptance scenario of US-1 to US-6, US-9 and US-10 is the "Traces to" of at least one BDD scenario.

---

## Assumptions

- GitNexus MCP tools were not available; every code fact was read with Grep and Read at `c2f8f8e735`. Impact rows are **Inferred**; backend-lead runs `impact` on `updateConfig`, `putContextSettings` and `RestartGatedKeys` before editing.
- No interview-me output exists for this feature; the founder's wireframe answers (S-Q1 to S-Q14) take its place (team-lead's brief).
- `sandbox.filesystem_model` and `tools.exec.enable_proxy` need a restart to take effect (**Inferred**, medium confidence, from where they are read); RED proves it before FR-036 is implemented. If either turns out hot-reloaded, it is not added and its badge is dropped.
- The hidden Sign-in section's links fall back to Providers. The design notes give no rule for a hidden section, so this applies their unknown-value rule (stated decision, not a founder answer).
- The 8-second saved signal (FR-018) and the 60-second restart wait (FR-063) are architect choices; frontend-lead may tune them with a recorded reason.

## Clarifications

### 2026-11-20

- Q: Where do chat-history and memory retention get written? → A: one new endpoint `/settings/retention`; every other writer is refused by effect; `/security/retention` GET/PUT are deleted (architect decision, C-3; round 1 replaced the `blockedPaths` idea).
- Q: B4 — multi-use token or one token per Save? → A: one token per Save, no server change (C-4; guard FR-030).
- Q: Is #1096 in this package? → A: no; the chief cut it at 05:08Z into `signed-in-browsers-spec.md` (founder answer pending). Sign-in is hidden here.
- Q: Settings or Skills & Tools first? → A: Skills & Tools lands first; no transitional layout (S-Q3 A, restored in round 1).
- Q: #1265 Clear sessions? → A: Q5 A — behaviour unchanged, wording names chat history, #1265 stays open (team-lead brief, matching the founder's "deal with this later").

## Questions for the founder

Both are open with the founder; the spec is written for the recommended answer A.

- **FQ-1 — Recall window for kept memory.** Today agents can recall at most 365 days of reflections, whatever is kept. With memory kept up to 730 days or forever, older reflections would be stored but never found. **A (recommended)**: recall reaches as far back as memory is kept (touches `pkg/agent`, needs hands-on testing). **B**: keep the 365-day recall limit and say so on screen. Answer "FQ-1 A" or "FQ-1 B".
- **FQ-2 — Deleting a stored key without the password.** S-Q11 B (confirm only) removes a password check an accepted decision record (ADR-022) added on purpose: someone at an unlocked, signed-in screen could then delete a provider key. **A (recommended, as answered)**: confirm only, audited. **B**: keep the password for delete. Answer "FQ-2 A" or "FQ-2 B".

FQ-3 (sign-in lifetime) moved to `signed-in-browsers-spec.md`.

---

## Round-1 fix log

Review: `docs/internal/specs/settings-redesign-spec-review.md` (BLOCK; 2 CRITICAL, 18 MAJOR, 3 MINOR).

| Finding | What changed | Section |
|---|---|---|
| Scope (chief 05:08Z) | [#1096] block cut: US-7, US-8, FR-074, FR-076–091, C-1, their scenarios, D-5–D-7, G-19–G-24, SC-007/008, DOC-009, FQ-3 moved to `signed-in-browsers-spec.md`; numbers kept; Sign-in hidden, its links fall back to Providers | header; C-1; UI Screens and States; FR-001, FR-003, FR-055; D-3 |
| CRIT-001 | Moved to `signed-in-browsers-spec.md` (fixed there: FR-092) | — |
| CRIT-002 | Retention guard by effect inside `updateConfig` (covers replace of `storage.retention`, `storage`, whole config, casing), 400 before any write; `blockedPaths` idea dropped; scenario, D-12 (11 rows, RED on today's code), hands-on G-26 (chief's addition) | C-3; FR-044; D-12; order 1; G-26; SC-005 |
| MAJ-001 | Third-party agents saves all three rows; partial-save promise withdrawn for that section, stated | C-6; FR-015, FR-019, FR-022; Non-Behaviors |
| MAJ-002 | Writers merge requested fields under the lock; `putContextSettings` named; barrier test both orders | C-6; FR-021; order 11–12 |
| MAJ-003 | Separate read (`RetentionSettings`, no range, all required) and write (`RetentionSettingsUpdate`) schemas; repair tested through the generated validator | C-3; FR-041, FR-046; order 3 |
| MAJ-004 | Unconditional handler checks for retention and retired memory fields, both flag states, mixed bodies | C-3; FR-042, FR-045; memory scenario outline; order 4 |
| MAJ-005 | Four write outcomes; read-back on unknown; Discard never claims to undo | C-6; FR-012, FR-015, FR-020; scenarios; order 21 |
| MAJ-006 | `PendingRestartEntry.reason` with `apply_failed`; durable in the running gateway; focus target always exists | C-2 addition; FR-035, FR-038, FR-062; scenarios; G-29 |
| MAJ-007 | Restart completion by `/about` uptime and the pending list; direct vs proxy journeys; no-return error | C-2; FR-063; scenarios; G-10 |
| MAJ-008 | In-app exits (app confirm) split from page exits (browser prompt) | FR-014; two scenario outlines; G-06 |
| MAJ-009 | Read-dependency table; group-only blocking; Retry keeps other drafts | UI Screens and States; FR-066; scenarios; G-25 |
| MAJ-010 | S-Q3 A restored: Skills & Tools lands first; transitional layout and its scenario removed | FR-060; FR-007, FR-009; Integration Boundaries; Assumptions |
| MAJ-011 | Moved to `signed-in-browsers-spec.md` (fixed there) | — |
| MAJ-012 | D-10 split into Accepted/Rejected; false 512 refusal removed; partial failure uses a declared seam plus a real refusal (override 0); SC-003 conditional on mode and consent | D-10; scenarios; G-07; SC-003 |
| MAJ-013 | Chief-confirmed rider table with acceptance, test and docs; #936 field matrix, `set_config` refusal and parity test; #1265 touched not resolved (Q5 A); #1146/#598 moved | C-7; FR-100–FR-109; Riders; US-10; D-16; DOC-013 |
| MAJ-014 | G-14 replaced by real retro-sweep Go tests with a switch-off positive control and chat/legacy checks; recall test uses the real path | scenarios; order 7–8; G-14 |
| MAJ-015, MAJ-016, MAJ-017, MAJ-018 | Moved to `signed-in-browsers-spec.md` (fixed there) | — |
| MIN-001 | Saved signal lasts 8 s, ends on edit or section change, announced once | FR-011, FR-018; scenario |
| MIN-002 | Moved to `signed-in-browsers-spec.md` (fixed there) | — |
| MIN-003 | Typecheck, design-system locks, contract drift and lint named as hand-back gates | Test Hierarchy; Reachability |
| Review Q1 / FQ-1, Q2 / FQ-2 | Kept open, written for A | Questions for the founder |
| Review Q5 | A: wording only, #1265 not resolved | FR-108; Riders |
| Source clarification: invalid Save `aria-disabled` | Kept and cited | FR-013; Accessibility |

## Round-2 fix log

Review: `docs/internal/specs/settings-redesign-spec-review-round2.md` (verdict REVISE, 6 MAJOR, 1 MINOR). This was the final fix round. FQ-1 and FQ-2 are still written for answer A.

| Finding | What changed | Section |
|---|---|---|
| R2-MAJ-001 | D-12 rebuilt with 15 rows in three kinds (Refused, Accepted, Fence), each consumed by its own scenario. Two fixtures, F1 (legacy `disabled`, exists at the baseline) and F2 (Keep forever), so each flag is checked separately. RED rows (1–3 and 5–8) use F1, pass every current fence, and store the change on `c2f8f8e735`; the run is saved during the failing-test step, and a mutation check removes the guard. The raw `GET /config` echo is now a Fence row: 403 from the credential fence, which stays. The chief's "whole-config" case is covered twice: by the largest permitted multi-section write (refused by the guard) and by the raw echo (refused by the fence). The casing row now changes the decoded value, and harmless casing and dotted spellings are Accepted rows. A merged result that does not decode is refused with 400 before any write. Accepted rows assert the intended change, not byte equality. SC-005 corrected. D-16 row 9 now uses a permitted payload instead of the raw echo | C-3; FR-044; US-5 AS10; scenarios "Keep forever survives any replace of its parent block", "Writes that leave retention unchanged still save", "A raw echo of the whole configuration meets the existing credential fence first"; D-12; D-16; G-26; SC-005; order 1 |
| R2-MAJ-002 | The saved-state check reads the file through `GET /config/pending-restart`. For a listed key, the saved value is its `persisted_value`, read from `config.json`; for an unlisted key, the saved value equals the running value. This holds because every Save-bar writer records failed applies inside the write lock before answering. A recognized saved-not-applied signal outranks the HTTP status. A running-settings read alone never means "not saved". Added a third-value outcome ("changed somewhere else"). Discard acts only on actual drafts, against the last known saved value. Third-party agents no longer re-reads running settings after `saved_not_refreshed` | C-2 addition; C-6 "Where the saved value is read"; FR-012, FR-020, FR-022; scenarios "An unknown result is checked by reading back", "A write that was stored but not applied says so", "A stored write whose refresh failed and whose answer was lost is found by the saved-state check", "A value changed elsewhere while the answer was lost"; orders 14, 21 |
| R2-MAJ-003 | Every restart check is same-origin. The connection policy (`connect-src 'self'`) and the cross-origin rules are not widened. After a port change the page polls only its own `/health`; once that stops answering, the person follows Open the new address, and Settings there checks `/config/pending-restart` on its own origin (signing in first if needed). Equal ports count only as a hint: the window always adds the proxy instruction. A new address is derived only when no guess is needed. G-10 runs on a production-served build with real headers, in a direct case and a proxy case | C-2 "Restart completion"; FR-063; US-4 AS9; scenarios "A changed port shows where the gateway will answer", "The new address confirms the restart on its own origin", "Behind a proxy…"; G-10; order 24; DOC-003 |
| R2-MAJ-004 | "A section that cannot load" now fails both Limits reads. The group-only scenario now checks only that Retry keeps the unsaved draft. A new scenario saves the Performance group explicitly while the Context read is failing. FR-066 states when a whole section is blocked | scenarios "A section that cannot load", "One failed read blocks only its group", "A group whose read worked still saves while another group's read failed"; FR-066; order 28 |
| R2-MAJ-005 | The screen's minimum of 1 for the override context length stays, and D-10 row 11 is now a Rejected row with today's message. The real server refusal is now the Third-party agents path check (`cli_path_invalid`, `not_found`) on a path the screen accepts. The server's own refusal of 0 is tested at the handler (`TestContextSettings_RefusesZeroOverride`). G-07 uses valid input plus the declared seam for the partial failure, and its "no seam needed" claim is removed | scenario "A real server refusal is shown under its field"; D-10 row 11; G-07; order 14 |
| R2-MAJ-006 | Write outcome contract added: for each endpoint and stage, the status, the generated envelope (`ErrorResponse`) and the `code`. `saved_not_refreshed` (500) is extended from Third-party agents to `PUT /config` and `PUT /settings/context`, with `details.keys`. Only descriptions change, in `ErrorResponse.yaml` and `openapi.yaml`, and they come before any code. FR-020 follows the table | C-2 addition; C-6 outcome table; API and Data; FR-020, FR-102; scenario "Settings saves report a failed rebuild"; order 14 |
| R2-MIN-001 | The live Skills permissions link now lands on Skills & Tools, matching FR-009. The old `tab=security` bookmark keeps its own D-3 mapping | scenario "Links from other screens keep working" |

**Escalation to the founder:** none. Every finding was fixed without a product decision. Two choices made here are the architect's and are recorded for the record. (1) After a port change, the person opens the new address with one click; the page does not open it on its own, because the old page cannot see the new port. (2) The chief's "whole-config PUT" proof is split: a raw `GET /config` echo is refused by the existing credential fence, and the largest permitted multi-section write is refused by the retention guard.
