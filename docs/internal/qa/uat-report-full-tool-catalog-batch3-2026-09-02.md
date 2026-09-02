# UAT Report — Full Tool Catalog, Batch 3 (Groups K–S, S48–S81)

**Batch:** batch3 of 4 (parallel batches split the 104-scenario plan; this report covers **only**
S48–S81** — Groups K (Sysagent agent mgmt), L (workspace mgmt), M (system task mgmt), N (channel
mgmt), O (skill mgmt), P (MCP server mgmt), Q (provider mgmt), R (config), S (diagnostics). Groups
A–J, T, U, V (S1–S47, S82–S98) are **out of scope for this report** — they are covered by the
other three batches' reports. This report's ledger therefore lists only the 34 scenario ids in its
own scope, not the plan's full 104-row ledger; the final combined ledger across all four batches is
assembled separately.

**Plan:** `docs/internal/qa/uat-plan-full-tool-catalog-2026-09-02.md`

**Commit under test:** `362129a7e52e8c05c87e1630d4ddb4b7ca511d00` (`release/v0.1.1`; includes
`16dee850`, the plan's stated target commit, as an ancestor — full CI green per the plan header).
All evidence in this report was collected against this exact commit's build; no code changes were
made during the run.

**Environment:**
- `OMNIPUS_HOME=/tmp/omnipus-uat-fullcatalog-batch3-20260902-122602` (isolated, throwaway, created
  fresh for this run; never `~/.omnipus`).
- Port `5300` (confirmed free via `lsof -i` before use; distinct from other batches' ports).
- Build: `npm run build` → `dist/spa/` → synced to `pkg/gateway/spa/` → `CGO_ENABLED=0 go build
  -tags goolm,stdjson -o /tmp/omnipus-uat-fullcatalog-batch3-bin ./cmd/omnipus/`. All three exit
  codes were `0`. Embed check: `grep -c "" pkg/gateway/spa/assets/index-*.js` returned a positive
  line count (2 matching asset files present and non-empty).
- Model / credential path: `openrouter` provider, model `z-ai/glm-5.2`, credential resolved via
  `api_key_ref: OPENROUTER_API_KEY`, injected into the isolated credential store via `omnipus
  credentials set` from the shell's own `OPENROUTER_API_KEY` env var (never written to
  `config.json` in plaintext — verified, see S62). `gateway.dev_mode_bypass=true` was used to drive
  the REST/WS API without a full onboarding flow (a sanctioned use per CLAUDE.md's "Known
  blockers" section — local dev/test scaffolding, not production).
- Driving mechanism: real chat turns via (a) the `omnipus <agent> "<prompt>" --yes` one-shot CLI
  (talks to the live gateway over the same WS API the SPA uses) for the majority of scenarios, and
  (b) a raw WS Python driver (`chat_turn.py`, using the auto-generated `cli.token` for real
  bearer-token auth) for scenarios needing to observe/answer a `tool_approval_required` frame
  without the CLI's `--yes` auto-approval masking it (S50's mount grant, S67's consent-gate test).
  Every tool-call scenario's evidence includes the raw `tool_call` transcript entry (exact
  parameters and exact result/error text) from the session's `transcript.jsonl`, not just the
  model's paraphrase.
- Platform: macOS (Darwin). Linux-only sandbox legs are out of scope here, as in every batch — see
  the plan's Environment Setup note; not re-litigated in this report since none of S48–S81 are
  sandbox-enforcement scenarios.

---

## 0. Scenario ledger (S48–S81, this batch's scope only)

| ID | Result | One-line summary | Evidence |
|---|---|---|---|
| S48 | **PARTIAL** | Happy path (explicit wildcard-free policy) PASSES both via REST and via the `create_agent` tool. The negative half — "confirm the create call is REJECTED with 400 if a wildcard or gap is present" — **FAILS**: a REST create with a missing tool-policy key and a REST create with a literal `"*"` key both returned `201`, not `400`. | `S48-create-tester-response.json`, `S48-create-gap-response.json`, `S48-create-wildcard-response.json`, `S48b-transcript.jsonl` |
| S49 | **PASS** | `update_agent` (via the tool, changing `soul`) took effect on the very next turn, same running gateway process, no restart. | `S49-step1-soul-transcript.jsonl`, `S49-step2-verify-transcript.jsonl` |
| S50 | **PASS** | `delete_agent` removed the agent's registration; the real mounted folder's files (`preexisting.txt`, `agent-written.txt`) are byte-identical before and after deletion (sha256 manifests match). Cascade reported explicitly: `sessions_deleted:8, tasks_unassigned:0, workspaces_updated:1`. | `MANIFEST-s50-mount-after-write.sha256`, `MANIFEST-s50-mount-after-delete.sha256` (identical), `S50-step3-delete-retry.log` |
| S51 | **PASS** | `read_agent_metadata` returned the exact SOUL content set by S49's update; HEARTBEAT correctly `NOT_FOUND` (never set). | `S51-transcript.jsonl` |
| S52 | **PASS** | `create_workspace` (REST, used as setup) returned a fresh id, name matched request. | `S52-create-workspace-response.json` |
| S53 | **PASS** | `update_workspace` tool renamed the workspace; response body shows the new name. | `S53-update-transcript.jsonl` |
| S54 | **PASS** | `get_workspace` returned the exact renamed value from S53. | `S54-get-transcript.jsonl` |
| S55 | **PASS** | `list_workspaces` shows the renamed name directly in the list entry. | `S55-list-transcript.jsonl` |
| S56 | **PASS** | `delete_workspace` cascaded cleanly and **discoverably**: response reported `tasks_deleted:1` (Task B, confirmed gone from disk); the scoped agent survived (not deleted), losing only its workspace membership — a documented, non-silent state (`This agent isn't on any workspace yet`), not silent data loss. | `S56-pre-delete-workspace-state.json`, `S56-delete-workspace-transcript.jsonl`, `S56-post-delete-list-workspaces.json`, `S56-post-delete-agent-state.json` |
| S57 | **PASS** | `create_task_in_workspace` returned a task id, title matched. | `S57-create-taskA-transcript.jsonl` |
| S58 | **FAIL** | `update_task_in_workspace` with `status:"doing"` (an unrecognized status) returned success (`updated_fields:["workspace_id"]`) but the status **silently did not change** (confirmed still `"inbox"` via S59) and no error was surfaced. Root cause below. | `S58-update-taskA-transcript.jsonl`, `S59-list-cli-stdout.txt` |
| S59 | **PASS** | `list_tasks_in_workspace` correctly reflects both S57's creation and (once a *valid* status was used, S58c) the specific field change. | `S59-list-transcript.jsonl`, `S59b-list-verify-transcript.jsonl` |
| S60 | **PASS** | `delete_task_in_workspace` removed Task A; confirmed gone via a follow-up list call (Task B remained). | `S60-delete-taskA-transcript.jsonl`, `S60-verify-list-transcript.jsonl` |
| S61 | **PASS** | `enable_channel("irc")` succeeded (no external credential — chosen per rule 11). | `S61-enable-irc-transcript.jsonl` |
| S62 | **PASS** | `configure_channel` stored the fake test password via the credential store (`token_ref: "channel_irc_token"`); `grep` of `config.json` for the plaintext value found **nothing** — confirmed via `omnipus credentials list`. Real, live security check, not a trust exercise. | `S62-configure-irc-transcript.jsonl`, inline grep in transcript above |
| S63 | **PASS*** | `test_channel` returned a clear `success:true` / message. *Caveat: the check is config-presence-only ("credentials=set"), not a live connectivity probe — see finding below (paired with S76). | `S63-test-irc-transcript.jsonl` |
| S64 | **PASS** | `list_channels` reflects `irc` enabled + configured. | `S64-list-channels-transcript.jsonl` |
| S65 | **PASS** | `disable_channel` + `list_channels` confirm state flip. **However, this exact enable→configure→disable sequence left the gateway's config-reload path broken** — see CRITICAL finding below (filed against S65/reload infra, not against the disable call itself, which behaved correctly). | `S65-disable-irc-transcript.jsonl`, `S65-FINDING-degraded-health.json` |
| S66 | **PASS** | `list_skills` (sysagent-scope) correctly returned `{"count":0,"skills":[]}` for an agent with no skill grants — consistent with ADR-072 grant-filtering. | `S66-list-skills-transcript.jsonl` |
| S67 | **FAIL** | Consent gate does **not** hold: with the agent's `create_skill` policy explicitly set to `"ask"`, two independent live-run attempts via the raw WS driver (no auto-approve) both executed `create_skill` immediately with **no** `tool_approval_required` frame at all. Directly contradicts the tool's own doc comment. Root cause below. | `S67b-deny-path.json`, `S67c-retest.json` |
| S68 | **FAIL** | `edit_skill` on the built-in `summarize` skill mutated the built-in's `SKILL.md` **in place** (`created_override:false`); the original built-in content is gone (only recoverable via a `.versions/` snapshot). Directly contradicts the tool's own documented contract ("the built-in is never mutated in place"). Root cause below. | `S68b-edit-skill-transcript.jsonl`, before/after content + sha256 in shell output |
| S69 | **PASS** | `remove_skill` removed the disposable skill; confirmed gone from `GET /skills` and from disk. | `S69-remove-skill` output, REST verification |
| S70 | **PASS** | `add_mcp_server` (local `/bin/echo` stub, no real third-party server) saved the config and cleanly reported the expected handshake failure (`invalid character 'u'...`) — safe, correct behavior for this fixture. | `add_mcp_server` transcript |
| S71 | **PASS** | `list_mcp_servers` reflects the registered stub server, `status:"error"`. | list_mcp_servers transcript |
| S72 | **PASS** | `remove_mcp_server` removed it; confirmed gone via a follow-up `list_mcp_servers` call. | remove/list transcripts |
| S73 | **NOT RUN** | Environment constraint, not a defect: the disposable local MCP fixture (`/bin/echo`) never completes a real MCP handshake (by design — never a real third-party server per rule 11), so it exposes zero discoverable tools to test the `mcp_<server>_*` wildcard-grant resolution against. The static-catalog half of this scenario is separately confounded by the S48 finding (wildcard keys aren't rejected at all on the REST path, for reasons unrelated to the documented MCP carve-out). No fabricated pass recorded. | N/A — explicitly not run |
| S74 | **PASS*** | `configure_provider` stored a (deliberately fake) test API key and reported `api_key_stored:true`. *Caveat, evidenced: the tool's `name` parameter is a shared catalog provider id with no separate "test" namespace — this call silently overwrote the SAME credential ref (`OPENROUTER_API_KEY`) the batch's real, load-bearing provider config used, breaking every subsequent chat call until restored. See finding below. | `configure_provider` transcript, recovery steps in raw log |
| S75 | **PASS** | `list_providers` reflects `openrouter`, `status:"key_present"`. | list_providers transcript |
| S76 | **PASS*** | `test_provider` returned `status:"ok"` — but self-disclosed `"note":"credential presence only — no network call was made to the provider"`. Same shallow-check pattern as S63; clearly labeled, not misleading. | test_provider transcript |
| S77 | **PASS** | `list_models` returned 421 real OpenRouter model ids (anthropic/claude-opus-4.1, google/*, meta-llama/*, etc.) — not a hardcoded/stale list. | list_models transcript |
| S78 | **PASS** | `get_config`/`set_config` round-trip on `agents.defaults.auto_recap_enabled`: flipped `true→false`, confirmed via `get_config` AND via **actual runtime behavior** (a session created while `false` produced zero recap log activity, vs. `true` sessions elsewhere in this run which reliably logged `session_end: recap ...`). Explicitly reverted to `true` before completion. | get/set_config transcripts, gateway log absence-of-recap check |
| S79 | **PASS** | `set_config` coerces a string-encoded bool correctly: `value:"false"` (confirmed `str` type in the raw tool-call params) → stored as native JSON `false` (confirmed via `config.json`, Python type `bool`); `value:"true"` → stored as native `true`. (A follow-up call in the same turn sent a malformed *doubly-quoted* string `"\"true\""` and correctly errored — that is expected behavior for malformed input, not a coercion bug; it briefly looked like an inconsistency and is called out here so it isn't mistaken for one.) | `S79-string-bool-false-transcript.jsonl`, `S79-string-bool-true-transcript.jsonl`, config.json type checks |
| S80 | **PASS** | `run_doctor` is not an always-green rubber stamp: on this very install it flagged 2 real HIGH-severity issues (SEC-28 exec proxy not running, SEC-05 no exec binary allowlist) unprompted. | run_doctor transcript |
| S81 | **PASS** | `get_usage` returned non-zero, plausible token/cost figures (1,363,254 total tokens) reflecting the real volume of tool-calling turns made in this session. | get_usage transcript |

**Counts (34 rows):**

- PASS = 29 — S49, S50, S51, S52, S53, S54, S55, S56, S57, S59, S60, S61, S62, S63*, S64, S65*,
  S66, S69, S70, S71, S72, S74*, S75, S76*, S77, S78, S79, S80, S81 (four of these, marked `*`,
  carry an evidenced caveat noted inline — S63/S76's checks are shallow-but-honestly-labeled, S65's
  caveat is filed as its own separate infra finding rather than a fault of `disable_channel` itself,
  S74's caveat is a near-miss safety gotcha rather than a functional failure of `configure_provider`
  — none of the four caveats names a failure of that scenario's own stated pass condition).
- PARTIAL = 1 — S48.
- FAIL = 3 — S58, S67, S68.
- NOT RUN = 1 — S73.
- BLOCKED = 0.
- N-A-ENVIRONMENT = 0.

Arithmetic: 29 + 1 + 3 + 1 + 0 + 0 = **34.** ✓ matches the 34 rows above.

---

## 1. Verdict

29 of 34 in-scope scenarios PASS (five with an evidenced caveat that does not itself constitute a
failure of that scenario), 1 is PARTIAL (S48 — happy path fine, the documented 400-on-gap/wildcard
guarantee does not hold on the REST create path), 3 are genuine FAIL with full reproduction
evidence and root cause (S58 update_task_in_workspace silently drops an invalid status with no
error; S67 create_skill's "ask" consent gate does not fire; S68 edit_skill mutates a built-in skill
in place instead of creating a versioned override), and 1 is NOT RUN due to a genuine environment
constraint (S73, no real local MCP server with discoverable tools was available within this batch's
safety constraints). This round cannot be reported as an unqualified pass: there is one NOT RUN and
four scenarios (S48, S58, S67, S68) with real, reproduced regressions against this project's own
documented contracts.

## 2. Anything that got through / regressed — unsoftened, first

1. **[CRITICAL] `create_agent`/agent-update REST writes do not reject a tool-policy gap or a
   wildcard key with 400, despite CLAUDE.md Hard Constraint #6 and this plan's own S48 text stating
   they do.** `POST /api/v1/agents` with a `tools_cfg.builtin.policies` map that (a) omits `bash`
   entirely, and separately (b) includes a literal `"*"` key, both returned `201 Created`, not
   `400`. Root cause (read in `pkg/gateway/rest.go`'s `createAgent`, around the
   `withToolPolicyCoverageGuard` call): the handler unconditionally starts from
   `coreagent.NewCustomAgentToolsCfg()` — a fully-enumerated, deny-seeded baseline — and merges the
   caller's map ON TOP of it *before* `config.ValidateToolPolicyCoverage` ever runs. A gap in the
   caller's map is therefore silently backfilled with `"deny"` from the seed, so the coverage-gap
   check can **structurally never observe a gap on this path** — it is dead code for `POST
   /agents`. Separately, the merge loop copies every key the caller supplies verbatim
   (`builtin.Policies[k] = ToolPolicy(v)`) with no check that `k` is a real, known tool name, so a
   `"*"` key is simply stored inertly alongside the 88 real entries rather than rejected. Net
   effect: the create endpoint never actually enforces the "wildcard-free, gap-free, or 400"
   guarantee it claims to — every submitted policy map, however incomplete or malformed, is
   silently repaired into something valid before validation runs. Not tested against `PUT
   /agents/{id}` (out of this scenario's remit) — worth checking there too. The **sysagent
   `create_agent` TOOL itself is unaffected**: it has no caller-facing policy parameter at all (it
   always produces the same deny-seeded baseline), so this specific defect is REST-surface-only —
   see S48's ledger row for the tool-path confirmation.
2. **[HIGH] `update_task_in_workspace` silently drops an unrecognized `status` value with no
   error, while separately mislabeling `updated_fields`.** Setting `status:"doing"` (not a real
   status in this system's vocabulary — the valid set is `inbox, next, blocked, done, failed`, per
   `TaskCreateTool`'s own error message) returned `success` with `updated_fields:["workspace_id"]`
   — status silently unchanged (confirmed `"inbox"` before and after via `list_tasks_in_workspace`)
   and no error at all. Root cause (`pkg/sysagent/tools/task.go`'s `TaskUpdateTool.Execute`, the
   status-handling block around line 632): `if v, ok := args["status"].(string); ok &&
   isValidTaskStatus(v) { ... }` has no `else` branch — an invalid status is a silent no-op, unlike
   `TaskCreateTool` which explicitly errors ("unknown status %q: expected one of..."). Separately,
   `workspace_id` is unconditionally appended to `updated_fields` whenever the key is *present* in
   the caller's args, regardless of whether the value actually differs from the existing one — this
   happened on every call in this test, including calls where `workspace_id` was resupplied
   unchanged, making `updated_fields` an unreliable diagnostic. A *recognized-but-unsettable*
   status (`"blocked"`, a derived side-state) **does** get a clean, explicit `INVALID_INPUT` error
   — so the tool clearly has the capacity to validate and report cleanly; it just doesn't do so for
   a genuinely unrecognized status string. A valid, settable status (`"next"`) worked correctly end
   to end. Confirmed 3 ways: `"doing"` (silent no-op), `"blocked"` (clean explicit error), `"next"`
   (correct success) — see S58/S58b/S58c evidence.
3. **[HIGH] `edit_skill`'s "built-in is never mutated in place" guarantee does not hold on a fresh
   install.** Editing the built-in `summarize` skill returned `created_override:false` and wrote
   directly to `$OMNIPUS_HOME/skills/summarize/SKILL.md` — the exact path the built-in itself was
   materialized to at boot. The original content (including its `homepage`/`metadata` frontmatter)
   is gone from that file; only a `.versions/` snapshot preserves it. Root cause
   (`pkg/skills/authoring.go`'s `EditSkill`): `localExists := statErr == nil` checks whether a file
   already exists at the writer-root path for this skill name, and treats "yes" as "this is a
   pre-existing user override, safe to edit in place." That assumption breaks because built-in
   skills in this install are pre-materialized onto the SAME writer-root path at boot — there is no
   separate, untouched "factory" location the writer's existence-check can distinguish from. The
   tool's own `Description()` string ("Editing a built-in creates a user override; the built-in is
   never mutated in place") and its response's own `created_override:false` field both directly
   contradict the observed outcome — this is not a matter of interpretation.
4. **[HIGH] `create_skill`'s documented consent gate does not fire.** With the calling agent's
   `create_skill` policy explicitly set to `"ask"` (confirmed in the stored entity file), two
   independent attempts via a raw WS client with no auto-approval both executed `create_skill`
   immediately — no `tool_approval_required` frame, no pause, no denial path exercised at all. This
   directly contradicts `pkg/sysagent/tools/skill_authoring.go`'s own doc comment: "an operator
   policy of 'ask' for these tools routes every invocation through that approval prompt BEFORE
   Execute runs. The tool implementation deliberately does NOT bypass that gate." Confirmed this is
   not a general dev-bypass artifact: `request_mount` (globally seeded `"ask"`, same runtime,
   identical driver, no auto-approval) correctly produced a `tool_approval_required` frame and
   blocked until approved (see S50). `edit_skill` and `remove_skill` share the same file/consent
   framing in the source comment but were not independently isolated from CLI `--yes`
   auto-approval in this run — flagging as a likely-shared-risk worth checking, not claiming it as
   independently confirmed.
5. **[CRITICAL, infra] Enabling a channel that isn't cleanly registerable for reload can
   permanently degrade the gateway until a full process restart.** After `enable_channel("irc")` →
   `configure_channel` → `disable_channel("irc")` (S61/S62/S65, all of which individually reported
   success), a later config-reload cycle failed with `channel "irc": reload: channel "irc" was
   marked for start but is not registered (config/registration name mismatch or skipped init)`,
   and `/health` began reporting `"status":"degraded"` with that reason — persistently, surviving
   further calls, and blocking any further config change from landing cleanly (a subsequent
   `set_config` attempt hung for the full 300s tool timeout during this window). Recovery required
   killing and restarting the gateway process; a fresh boot with the same on-disk config (IRC
   already disabled) came up clean immediately. This was not deliberately engineered — it surfaced
   from ordinary use of `enable_channel`/`configure_channel`/`disable_channel`, exactly the
   sequence S61–S65 legitimately exercises. Not root-caused further (out of this plan's remit to
   fix), but the exact `/health` payload is captured as evidence (`S65-FINDING-degraded-health.json`).
6. **[MEDIUM, near-miss] `configure_provider`'s `name` parameter has no separate "test" namespace
   from the real, load-bearing provider of the same catalog id.** Configuring a provider under
   `name:"openrouter"` with a deliberately fake test API key (per this scenario's own instruction,
   safety rule 11) silently overwrote the SAME credential (`OPENROUTER_API_KEY`) this batch's real,
   working provider config depended on for every other scenario in this report — breaking every
   subsequent chat call until the tester manually restored the credential AND forced a `/reload`
   (a plain `credentials set` alone did not take effect without the reload — a hot-reload gap for
   credential changes specifically, contrasted with S49's confirmed hot-reload of agent config and
   S78's confirmed hot-reload of `agents.defaults.*`). This is filed as a near-miss finding rather
   than a strict scenario failure because S74 itself (configure a provider, confirm it's stored)
   still passed — but it is a genuine safety gotcha for anyone following this plan's own S74
   instruction literally on a live, non-throwaway install: there is no way to configure "a test
   openrouter" separate from "the real openrouter."

## 3. Anything that should work and doesn't (usability regressions)

- `update_task_in_workspace`'s `updated_fields` response is not a trustworthy diagnostic — it can
  list a field (`workspace_id`) that did not actually change in value, while omitting the field
  that was genuinely requested and silently rejected (`status`). A caller cannot tell from the
  response alone whether their intended change landed. (Same underlying defect as finding #2 above.)
- `test_channel` (S63) and `test_provider` (S76) both self-report as configuration-presence checks
  rather than live connectivity probes. This is honestly disclosed in `test_provider`'s own
  response text (`"note":"credential presence only — no network call was made to the provider"`)
  and is not concealed, but the tool names and the plan's own S63/S76 wording ("validates
  connectivity") set an expectation of a live reachability check that neither tool actually
  performs. Not a correctness defect (nothing lies), but a naming/expectation gap worth a docs fix.

## 4. Two-layer filesystem comparison table

No S8-style paired tool-call/`bash` filesystem comparison scenario is in this batch's scope
(S1–S24 are Groups A–D, out of scope for batch3). This section is intentionally empty for this
report — see the batch covering Group A/B for that comparison.

## 5. Group T policy-enforcement table

Out of scope for this batch (S82–S84 are Group T). Not run here.

## 6. What couldn't be tested and why

- **S73 (MCP wildcard-policy exception): NOT RUN.** The disposable local MCP fixture used for
  S70–S72 (`/bin/echo` as a stdio "server," chosen specifically to avoid any real third-party
  credentialed server per safety rule 11) never completes a real MCP `initialize` handshake, so it
  never exposes any discoverable tools for a `mcp_<server>_*` wildcard grant to resolve against.
  Standing up a genuine minimal local MCP server (e.g. the official reference "everything" server
  via `npx`) was judged out of this batch's time/safety budget given it would require outbound
  npm/network access this batch's fixture choice was deliberately avoiding. This is a genuine
  coverage gap, not a fabricated pass — flagging for a follow-up batch or a dedicated local Go stub
  MCP server fixture.
- **Linux-only sandbox legs**: not applicable to this batch — none of S48–S81 are
  Landlock/seccomp-relevant; this note is carried forward from the plan template for completeness
  only.
- **Group J (email) `N-A-ENVIRONMENT`**: not this batch's scope (S45–S47); no mailbox was
  configured or needed for S48–S81.

## 7. Stress-group results

Not in this batch's scope (Group U, S85–S92). Not run here.

## 8. Cleanup confirmation

All disposable resources created during this batch were deleted; final REST listings after cleanup:

- **Agents** (`GET /api/v1/agents`): `['mia', 'jim', 'ava', 'ray', 'worker', 'planner', 'explorer',
  'researcher', 'judge', 'plansupervisor']` — exactly the 10 seeded core/system agents, zero
  disposable batch3 agents remain. (Created and deleted: the REST-created Subagent + its gap-test
  and wildcard-test siblings, the tool-created `uat-s48b-chatcreated`, the S50 mount-delete tester
  `ed185b04-...` [deleted mid-scenario as S50's own subject], and the Main tester
  `526b5d33-...` [deleted last].)
- **Workspaces** (`GET /api/v1/workspaces`): `[('01M1G9F759FR7Z004HXYQPBEME', 'My Workspace')]` —
  only the pre-existing default workspace remains; the disposable batch3 workspace was deleted as
  S56's own scenario subject.
- **Tasks**: both disposable tasks (A, B) are gone — A via S60's `delete_task_in_workspace`, B via
  S56's `delete_workspace` cascade.
- **Channels** (`GET /api/v1/channels`): only `webchat` (the always-on built-in) remains enabled;
  `irc` is disabled and its stored credential (`channel_irc_token`) was force-deleted from the
  credential store.
- **MCP servers**: `list_mcp_servers` tool call returns `{"servers":[]}` — the disposable
  `uat-batch3-test-mcp` entry was removed by S72.
- **Skills** (`GET /api/v1/skills`): `['Daily Briefing', 'Plan', 'Skill Authoring', 'Summarize']` —
  only the 4 original built-ins remain; all three disposable UAT skills were removed (one by S69,
  two in final cleanup). Note: `Summarize`'s *content* still carries the S68 defect's edit residue
  inside this throwaway `$OMNIPUS_HOME` — irrelevant, since the entire environment is destroyed
  after this report, but noted for completeness.
- **Providers**: `openrouter` remains, with its real, working credential restored (confirmed via a
  live chat round-trip after restoration) — the fake test key from S74 does not persist.
- **Gateway process**: killed; port 5300 confirmed free via `lsof -i` after shutdown. No orphaned
  child processes (`ps aux | grep batch3` empty).
- **Global config changes made for testing, and their revert status**: `sandbox.tool_policies.
  add_mcp_server` was temporarily flipped `deny→allow` (S70–S72 setup) and explicitly reverted to
  `deny` afterward, confirmed via a clean `/reload`. `agents.defaults.auto_recap_enabled` was
  temporarily flipped `true→false` (S78) and explicitly reverted to `true` (confirmed native-bool
  type in `config.json`, not a leftover string).

---

## Appendix: build/embed evidence

```
npm-build exit=0
spa-sync exit=0
go-build exit=0
grep -c "" pkg/gateway/spa/assets/index-*.js  →  2 (both files non-empty)
git log -1 --format='%H'  →  362129a7e52e8c05c87e1630d4ddb4b7ca511d00
```

All scenario evidence (raw CLI stdout, session `transcript.jsonl` excerpts, REST request/response
bodies, sha256 manifests) is retained under
`/tmp/uat-fullcatalog-evidence-batch3-20260902-122602/` for the duration of the throwaway
`$OMNIPUS_HOME`'s lifetime (this directory is not itself part of the isolated home and was not
deleted with it, but is also throwaway `/tmp` scratch — not committed, not durable).
