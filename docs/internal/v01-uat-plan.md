# Omnipus v0.1.0 "Foundation" — User Acceptance Test Plan

**Version:** 1.0  
**Date:** 2026-06-13  
**Authors:** QA Lead (qa-lead agent)  
**Source specs:** ADR-019, Spec-1 through Spec-6  
**Release tag:** v0.1.0 (Foundation — Workspaces redesign)  

---

## 1. Preamble

### 1.1 Purpose

This document defines the human-executable (or Playwright-automatable) acceptance criteria for the Omnipus v0.1.0 "Foundation" release. Every UAT scenario traces directly to a BDD scenario or functional requirement in one of the six feature specs (Spec-1 through Spec-6) and the governing ADR-019.

A tester — human or a Playwright-driven browser agent — runs each numbered scenario against the built binary. All scenarios must reach PASS status for the release to be accepted. Scenarios listed in section 8 (Holdout Gates) are hard blockers.

### 1.2 Scope

**In scope (v0.1.0 only):**
- Workspace scoping key and `project → workspace` rename end-to-end
- Connections-as-instance migration (Connectors UI, IMAP/SMTP email)
- 4-base agent roster (Mia · Jim · Ray · Ava), delegation policy, Orchestrator
- External agent runners — executor field + external-cli dispatch (worktree-isolated, CLI self-sandbox; consent best-effort) via the Claude Code / Codex / opencode drivers
- Memory rooms (private + shared), remember/recall/retrospective tools, bleve FTS
- Task fields (start/due/recurrence/blocked_by DAG), Calendar shell
- Skills wiring + create/edit, plugin manifest shape, marketplace list
- Integrations provider-picker + composer mic
- Single-user/one-password auth, Profile vs Settings, 3-step onboarding

**Out of scope for UAT:**
- Memory ranking / graph / Dreamcatcher / weights (v0.2.0 behaviour)
- Recurrence execution / automations engine (calendar shell only)
- Plugin installer / Marketplaces UI (shape pinned, installer deferred)
- ACP / A2A protocol drivers (interface shape reserved, not resolved)
- OAuth flows (v1.0.0)
- Multiple user accounts / RBAC (single-user product)

### 1.3 Environment Setup

**Step 1 — Build the binary.**

```bash
cd /path/to/omnipus
npm run build
rm -rf pkg/gateway/spa && cp -r dist/spa/* pkg/gateway/spa/
CGO_ENABLED=0 go build -tags goolm,stdjson -o /tmp/omnipus-v01 ./cmd/omnipus/
```

**Step 2 — Provision a clean home directory.**

```bash
export OMNIPUS_HOME=/tmp/omnipus-uat-$(date +%s)
mkdir -p "$OMNIPUS_HOME"
```

**Step 3 — Provide a tool-capable model key.** The model MUST support tool use. Valid examples: `z-ai/glm-5-turbo`, `google/gemini-2.5-flash`, `anthropic/claude-3.5-haiku`. Set via the onboarding wizard or `config.json` prior to boot. A model that does not support tools will cause every agent turn to fail — confirm the key is valid before running UAT.

**Step 4 — Boot the gateway.**

```bash
OMNIPUS_BEARER_TOKEN="" /tmp/omnipus-v01 gateway \
  --host 0.0.0.0 --port 8080
```

Wait until the log shows `gateway ready on :8080`.

**Step 5 — Open the browser.**

Navigate to `http://localhost:8080` (or `$DEVPOD_PREVIEW_URL` in a devpod). The onboarding wizard should appear on first boot.

**Proxy note for Playwright sessions:** configure Playwright to target `http://localhost:8080`.

### 1.4 Pass/Fail Criteria

- **PASS:** the expected observable result matches the actual result for every numbered step in the scenario.
- **FAIL:** any step's actual result diverges from the expected result, or an unhandled error/exception occurs.
- **BLOCKED:** a prerequisite step cannot be completed (e.g., the binary will not start). Treat as FAIL with notes.
- **N/A:** the step requires hardware/config not present in this test environment (e.g., a physical email server). Mark N/A with explanation — the scenario is counted as conditionally passing only if all non-N/A steps pass.

**Console error policy:** after each scenario, check the browser DevTools console. WebSocket reconnect warnings (`WebSocket reconnecting…`) are acceptable. Any `[Error]` or `[Uncaught]` entry that is not a reconnect warning is a FAIL. Record the message in the Notes column of the results matrix.

**WS frame drop policy:** the backend emits a `zod-schema-invalid` counter (or equivalent dev-mode toast) when a WebSocket frame fails schema validation. Any such toast during a scenario is a FAIL — it indicates a contract drift.

### 1.5 How to Record Results

Fill in the Results Matrix (section 9) as you run each scenario. Use:
- `P` — Pass
- `F` — Fail (add brief note)
- `B` — Blocked (add reason)
- `N/A` — Not applicable (add reason)

---

## 2. Suite A — Onboarding

**Scope:** Spec-6 FR-12.3, US-8 (3-step wizard → auto-provision Mia · Assistant in My Workspace).

---

### UAT-ON-01 — First-boot wizard appears on a clean install

**Traces to:** Spec-6 US-8 AC-2; ADR-019 FR-12 (3-step onboarding)  
**Preconditions:** Fresh `OMNIPUS_HOME` (no existing `config.json`). Binary started.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Navigate to `http://localhost:8080` | The onboarding wizard is displayed — NOT the main chat UI. |
| 2 | Observe the number of distinct steps shown in the wizard | Exactly 3 steps are shown: Name, Password, Model Key. |
| 3 | Observe the current step | Step 1 "Name" is active. |

**Pass/Fail:** ______

---

### UAT-ON-02 — Complete the 3-step onboarding: name → password → model key

**Traces to:** Spec-6 US-8 AC-2; BDD "Onboarding auto-provisions Mia"  
**Preconditions:** UAT-ON-01 passed. Wizard is open on Step 1.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | In the Name field, type `TestOperator`. Click Next or equivalent. | Step 2 (Password) becomes active. |
| 2 | In the Password field, type a password (e.g., `S3cure!UAT`). In the Confirm Password field, repeat the same password. Click Next. | Step 3 (Model Key) becomes active. |
| 3 | In the model key / API key field, paste a valid tool-capable model key. Click Finish or equivalent. | The wizard closes. The main application UI loads. |
| 4 | Observe the page header or sidebar. | "My Workspace" is visible as the active workspace name. |
| 5 | Navigate to the Agents screen (sidebar or top navigation). | The agents list is displayed. |
| 6 | Observe the listed agents. | Exactly 4 base agents are present: Mia · Assistant (marked as default with a star ⭐), Jim · Orchestrator, Ray · Scout, Ava · Builder. No agent named "Max" appears. |
| 7 | Confirm that Mia · Assistant has the default ⭐ marker. | The ⭐ or "Default" label appears next to Mia only. |

**Pass/Fail:** ______

---

### UAT-ON-03 — Onboarding is NOT re-presented on subsequent boots

**Traces to:** Spec-6 US-8; Spec-1 FR-1.6 (seed exactly once)  
**Preconditions:** UAT-ON-02 passed.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Quit and restart the gateway (Ctrl+C, then reboot with the same `OMNIPUS_HOME`). | The gateway starts successfully. |
| 2 | Navigate to `http://localhost:8080`. | The main chat UI loads directly — the onboarding wizard does NOT appear. |
| 3 | Observe the workspace shown. | "My Workspace" is still the active workspace (persisted from step 2 of UAT-ON-02). |

**Pass/Fail:** ______

---

### UAT-ON-04 — Onboarding rejects a mismatched password confirmation

**Traces to:** Spec-6 US-8 (single-password setup); onboarding confirm validation — `src/routes/onboarding.tsx:284` (`adminPassword !== adminPasswordConfirm` → "Passwords do not match", submit blocked)  
**Preconditions:** Fresh `OMNIPUS_HOME` with the wizard on Step 2 (Password), OR re-run onboarding on a clean home.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | On the Password step, type a password (e.g., `S3cure!UAT`) in the Password field and a **different** value (e.g., `S3cure!UAX`) in the Confirm Password field. | The "Next"/submit control is **disabled** while the two fields differ (the SPA guards on `password === passwordConfirm`). |
| 2 | If the UI permits a submit attempt anyway, submit. | The submission is rejected with the inline error **"Passwords do not match"**. Onboarding does NOT advance and no admin account is created. |
| 3 | Correct the Confirm field to match the Password field. | The submit control becomes enabled and onboarding advances to Step 3 (Model Key). |

**Pass/Fail:** ______

---

## 3. Suite B — Workspaces

**Scope:** Spec-1 (Workspace key rename, seed, delete-protection), ADR-019 FR-1.

---

### UAT-WS-01 — Default "My Workspace" is seeded exactly once on a fresh install

**Traces to:** Spec-1 US-6; BDD "Fresh install seeds exactly one default workspace"  
**Preconditions:** Fresh `OMNIPUS_HOME`. Binary started and onboarding completed (UAT-ON-02).

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Navigate to the Workspaces section (sidebar workspace name or dedicated Workspaces screen at `/workspaces`). | The workspace list is displayed. |
| 2 | Count the workspaces shown. | Exactly one workspace exists: "My Workspace". It is marked as default. |
| 3 | Inspect the workspace entry or its detail view for an `owner` field. | The `owner` field is populated with the username entered during onboarding (`TestOperator`). |
| 4 | Verify the URL in the browser. | The URL contains `/workspaces` (not `/projects`). |

**Pass/Fail:** ______

---

### UAT-WS-02 — Default workspace cannot be deleted (delete protection)

**Traces to:** Spec-1 US-6 (Inbox protection retained); BDD "Default workspace cannot be deleted"  
**Preconditions:** UAT-WS-01 passed.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Open the Workspaces screen. Click the options menu or delete button on "My Workspace". | A delete confirmation dialog appears, OR the delete action is visibly disabled/greyed out. |
| 2 | If a dialog appeared: confirm the deletion. | The deletion fails. An error message is displayed indicating the default workspace cannot be deleted (the API returns 409). "My Workspace" remains in the list. |
| 3 | Confirm "My Workspace" is still listed and still marked as default. | It persists unchanged. |

**Pass/Fail:** ______

---

### UAT-WS-03 — Create a new workspace

**Traces to:** Spec-1 US-1 (Workspace is the scoping entity)  
**Preconditions:** UAT-WS-01 passed.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | On the Workspaces screen, click "New Workspace" or equivalent. | A creation dialog or form appears. |
| 2 | Type `UAT Workspace Alpha` in the name field. Click Create. | The new workspace is created and appears in the list. |
| 3 | The new workspace should appear with the name typed in step 2. | The name displays as `UAT Workspace Alpha`. |

**Pass/Fail:** ______

---

### UAT-WS-04 — Rename an existing workspace

**Traces to:** Spec-1 US-1; ADR-019 FR-1  
**Preconditions:** UAT-WS-03 passed. "UAT Workspace Alpha" exists.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Open the workspace options for "UAT Workspace Alpha". Click Rename or Edit. | A rename input field appears pre-populated with the current name. |
| 2 | Clear the field and type `UAT Workspace Beta`. Confirm the rename. | The workspace name updates to "UAT Workspace Beta" in the list. |
| 3 | Refresh the page and navigate back to the Workspaces screen. | "UAT Workspace Beta" persists — the rename survived a page reload. |

**Pass/Fail:** ______

---

### UAT-WS-05 — Switch between workspaces

**Traces to:** Spec-1 US-1 (scoping entity context switch)  
**Preconditions:** UAT-WS-04 passed. Both "My Workspace" and "UAT Workspace Beta" exist.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | In the sidebar workspace switcher, click on "UAT Workspace Beta". | The active workspace context switches. The header/sidebar shows "UAT Workspace Beta" as the active workspace. |
| 2 | Click on "My Workspace" in the switcher. | The active workspace switches back. The header shows "My Workspace". |

**Pass/Fail:** ______

---

### UAT-WS-06 — Tasks are scoped per workspace (no cross-workspace bleed)

**Traces to:** Spec-1 US-5 (task behaviour preserved under workspace_id); Spec-5 FR-8.1  
**Preconditions:** UAT-WS-05 passed. Both workspaces exist.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Switch to "UAT Workspace Beta". Navigate to Tasks. | The task list for "UAT Workspace Beta" is empty. |
| 2 | Create a task titled "Beta task only" in "UAT Workspace Beta". | The task appears in "UAT Workspace Beta"'s task list. |
| 3 | Switch to "My Workspace". Navigate to Tasks. | The task list for "My Workspace" does NOT contain "Beta task only". The task is scoped to its workspace. |

**Pass/Fail:** ______

---

### UAT-WS-07 — The `/projects` route is gone; `/workspaces` serves correctly

**Traces to:** Spec-1 US-7 (SPA under `/workspaces`); FR-1.5  
**Preconditions:** Application running. (No `projects.*` route file exists under `src/routes/_app/` — only `workspaces.*` — so `/projects` resolves to the SPA not-found state, never a working screen.)

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Navigate directly to `http://localhost:8080/workspaces` in the browser. | The Workspaces screen loads correctly — no 404. |
| 2 | Navigate directly to `http://localhost:8080/projects`. | The router shows its **not-found / 404 state** — there is no `projects.*` route. (NOT a redirect, and NOT a working "projects" screen.) |

**Pass/Fail:** ______

---

## 4. Suite C — Connections (Connectors)

**Scope:** Spec-2 (Connection-as-instance migration, Connectors UI, IMAP/SMTP email).

---

### UAT-CN-01 — Connectors screen is accessible

**Traces to:** Spec-2 US-8 (Connectors UI)  
**Preconditions:** Application running, logged in.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Navigate to the **Connectors** screen (sidebar → **Connectors**, i.e. `/#/connectors`; the screen was renamed from "Channels" in `1b95ced6` and `/#/channels` now 404s). | A Connectors UI is displayed showing available channel types. |
| 2 | Observe the list of channel types. | Channel types include at minimum: Telegram, Slack, Discord, WhatsApp (native), and Email. No "Max" channel type appears. |

**Pass/Fail:** ______

---

### UAT-CN-02 — Add a channel instance (Telegram example)

**Traces to:** Spec-2 US-1 (instance-map config); US-6 (per-instance secret refs)  
**Preconditions:** UAT-CN-01 passed. A Telegram bot token is available for testing.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | In the Connectors UI, click "Add" or "Configure" on the Telegram channel type. | A configuration form appears for a new Telegram instance. |
| 2 | Enter an instance name (e.g., `tg-test`) and a valid Telegram bot token. Click Save. | The instance is saved. The Connectors list shows a Telegram entry with the instance name. |
| 3 | Open the saved Telegram instance configuration. Inspect the stored token field. | The token field shows a reference placeholder (`token_ref=…`) rather than the plaintext token. The actual token is NOT visible in the config. |

**Pass/Fail:** ______

---

### UAT-CN-03 — Only one instance per channel type (cap-1 enforcement)

**Traces to:** Spec-2 US-2 (cap of one per type); BDD "Second instance of a type rejected"  
**Preconditions:** UAT-CN-02 passed. One Telegram instance already exists.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | In the Connectors UI, attempt to add a second Telegram instance. | Either the "Add Telegram" button is disabled/hidden, OR clicking it shows an error. |
| 2 | If the UI allows the attempt: fill in a second instance name and token, click Save. | The operation fails with an error message indicating one-per-type is enforced (e.g., "only one Telegram instance allowed in v0.1.0"). The second instance is NOT created. |
| 3 | Confirm only one Telegram instance appears in the list. | One Telegram entry. |

**Pass/Fail:** ______

---

### UAT-CN-04 — Configure email (IMAP + SMTP) — connection test

**Traces to:** Spec-2 US-7 (basic email channel); FR-2.7; channel test endpoint `POST /api/v1/channels/{id}/test` (`pkg/gateway/rest.go::testChannel`, returns `ChannelTestResponse {success, message}`)  
**Preconditions:** A test mailbox with IMAP and SMTP access is available (e.g., a local greenmail container or a real test account). Mark N/A if no test mailbox is available.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | In the Connectors UI, click "Add" on the Email channel type. | An email configuration form appears with fields for IMAP host, IMAP port, SMTP host, SMTP port, username, and password. |
| 2 | Fill in valid IMAP and SMTP connection details. Save, then click "Test Connection" — this invokes `POST /api/v1/channels/{id}/test`. | The endpoint returns the `ChannelTestResponse` JSON shape `{"success": true, "message": "channel \"<id>\" is configured"}`. (A misconfiguration returns `{"success": false, "message": "missing required fields: …"}`; an unavailable credential store returns `{"success": false, "message": "credential store unavailable — unlock it…"}`.) |
| 3 | Confirm the email instance is saved. | The Connectors list shows an Email entry. |
| 4 | Inspect the saved email configuration. | The password field is stored as a `password_ref` (not plaintext). |

**Pass/Fail:** ______

---

### UAT-CN-05 — IMAP unreachable shows a graceful error, not a crash

**Traces to:** Spec-2 BDD "IMAP down degrades, not crash"; FR-2.7  
**Preconditions:** UAT-CN-04 passed. Optionally: stop the test mail server.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Stop or block the IMAP server used in UAT-CN-04 (or provide an unreachable IMAP host). | — |
| 2 | Restart the gateway with the same `OMNIPUS_HOME`. | The gateway starts successfully — it does NOT crash at boot. |
| 3 | Navigate to the Connectors screen. | The email connection shows a degraded/error status (e.g., "IMAP unreachable"). The gateway continues to operate. |

**Pass/Fail:** ______

---

### UAT-CN-06 — Email route delivers inbound message to the bus

**Traces to:** Spec-2 US-7 AC-1; BDD "Email round-trips one mailbox"  
**Preconditions:** UAT-CN-04 passed with a working test mailbox. Email channel enabled.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Send a test email to the mailbox configured in UAT-CN-04. | — |
| 2 | Wait up to 60 seconds (IMAP poll interval). | A new conversation or message from the email channel appears in the Omnipus chat UI attributed to the email sender. |

**Pass/Fail:** ______  
**Note:** Mark N/A if no test mailbox is available.

---

### UAT-CN-07 — Hand-edited config.json with two instances of one type is rejected at LOAD

**Traces to:** Spec-2 FR-2.3 (cap-1/type enforced at config load); `pkg/config/config.go::ValidateChannelsCap1` (`maxInstancesPerType = 1`), called during `LoadConfig`  
**Preconditions:** Application stoppable; access to `$OMNIPUS_HOME/config.json`.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Stop the gateway. Hand-edit `$OMNIPUS_HOME/config.json` to add **two** channel instances of the **same** type (e.g., two `telegram` instances under `channels`). | The file now contains two instances of one type. |
| 2 | Restart the gateway with the same `OMNIPUS_HOME`. | Config load **rejects** the file with the cap-1 error: `channels: cap-1/type violated (v0.1 allows one instance per type): …`. The bad config is not silently accepted; the gateway refuses to load it (boot aborts or surfaces the load error) rather than running with two instances. |

**Pass/Fail:** ______

---

### UAT-CN-08 — Channel `identity{agent|user}` binds inbound routing

**Traces to:** Spec-2 FR-2.5 / US-5; `pkg/config/config.go::ChannelIdentity` (`Kind ∈ {"agent","user"}`, `ID`) wired to routing  
**Preconditions:** A configured, live channel instance with an `identity` block (e.g., a Telegram instance). **Mark N/A if no live channel can be exercised in this environment.**

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | In the channel instance config, set `identity` to `{kind: "agent", id: "<some-agent-id>"}`. Save and (re)enable the channel. | The identity is persisted on the instance. |
| 2 | Send an inbound message on that channel. | The inbound message is routed/attributed per the configured `identity` (bound to the specified agent), not to an unrelated default — confirming `identity` participates in inbound routing resolution. |

**Pass/Fail:** ______  
**Note:** Mark N/A if no live channel is available.

---

## 5. Suite D — Agents & Delegation

**Scope:** Spec-3 (4-base roster, delegation policy, Orchestrator, Max-parallel).

---

### UAT-AG-01 — 4-base roster is present; built-in identities are locked

**Traces to:** Spec-3 US-1; BDD "Fresh seed yields the 4-base roster, Max retired"  
**Preconditions:** Fresh install completed (UAT-ON-02 passed).

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Navigate to the Agents screen. | The agents list is shown. |
| 2 | Confirm the 4 base agents are listed: Mia · Assistant, Jim · Orchestrator, Ray · Scout, Ava · Builder. | All 4 are present. No agent named "Max" appears in the base roster. |
| 3 | Click on Mia · Assistant to open her configuration. | The detail view opens. |
| 4 | Attempt to edit Mia's name or core identity fields (name, type, persona prompt). | Either the fields are greyed out / read-only, OR an error is shown when attempting to save changes to these identity fields. The name "Mia" and role "Assistant" cannot be overwritten. |
| 5 | Confirm Mia is marked as default. | A star ⭐ or "Default" label is visible next to Mia's listing. |

**Pass/Fail:** ______

---

### UAT-AG-02 — Built-in agent prompts are not surfaced in the UI

**Traces to:** Spec-3 FR-3.1 (prompts not surfaced); ADR-019 FR-3  
**Preconditions:** Agents screen visible.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Open any built-in agent (Mia, Jim, Ray, or Ava) configuration. | The configuration view loads. |
| 2 | Look for a "Prompt", "System Prompt", or "Persona Prompt" field or text area that shows the compiled agent persona prompt. | No editable prompt field showing the built-in compiled persona is visible in the UI. (A custom-instruction or note field is acceptable; the compiled persona text itself must not be exposed for built-ins.) |

**Pass/Fail:** ______

---

### UAT-AG-03 — Create a custom agent (ungated creation)

**Traces to:** Spec-3 US-8 (custom agents, ungated); FR-3.3  
**Preconditions:** Agents screen visible.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Click "New Agent" or "Create Agent". | A creation form appears. |
| 2 | Enter the name `UAT Custom Agent` and a short description. Click Create (no password prompt at this point). | The agent is created without requiring a password confirmation. It appears in the agents list. |
| 3 | Confirm the new agent is listed alongside the 4 base agents. | `UAT Custom Agent` is visible. The total agent count is 5. |

**Pass/Fail:** ______

---

### UAT-AG-04 — Delegation policy: to + modes enforced

**Traces to:** Spec-3 US-3/US-4; BDD "Delegation policy enforces to+modes"  
**Preconditions:** UAT-AG-01 passed. At least 2 agents exist (e.g., Mia and Ray).

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Open Jim · Orchestrator's configuration. Navigate to the Delegation Policy section. | A delegation policy editor is visible showing fields: `to` (target allowlist), `modes`, and (greyed-out or absent) `accept_from`, `budget`. |
| 2 | Set `to` to include only `ray` (Ray · Scout). Set `modes` to `await`. Save. | The policy is saved. |
| 3 | Observe that `accept_from` and `budget` fields are present in the schema (they may be visible but empty/locked) but are NOT surfaced for active enforcement in the UI. | The trust-graph or policy view does not show `accept_from` as an active, configurable enforcement boundary. |

**Pass/Fail:** ______

---

### UAT-AG-05 — Handover is not gated by delegation policy

**Traces to:** Spec-3 US-4; BDD "Handover stays open"  
**Preconditions:** UAT-AG-04 passed. Jim's `to` is set to only `ray`.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | In the chat, initiate a conversation with Jim · Orchestrator. | Jim responds. |
| 2 | Ask Jim to hand over the conversation to Ava (not in his `to` allowlist). | Jim successfully hands the conversation to Ava without a delegation-policy denial. The handover completes. |

**Pass/Fail:** ______

---

### UAT-AG-06 — Max-parallel agents setting is re-auth gated (KNOWN SPEC-VS-CODE DIVERGENCE)

**Traces to:** Spec-3 US-7; FR-6.6 (Max-parallel, password-gated)  
**Preconditions:** Application running, logged in.

> **⚠️ KNOWN DIVERGENCE — DO NOT WAVE THROUGH.** Per FR-6.6 / FR-12.2 the performance / max-parallel change must require the re-auth token. In the current code the performance handler `pkg/gateway/rest_performance.go::putPerformance` is **NOT** wrapped in `requireReAuth` — `PUT /api/v1/performance` changes `max_parallel_agents` and resizes the dispatch semaphore with no re-auth gate. This is the same tracked completeness-phase fix as UAT-AUTH-03. The expected result is written to the **spec target**; until the fix lands this is a **divergence FAIL** — do not pass it on the "or directly requires a password" technicality.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Navigate to Settings → Performance (or search for "Max parallel agents"). | A "Max parallel agents" setting is visible. |
| 2 | Issue `PUT /api/v1/performance` to change `max_parallel_agents` **without** an `X-Reauth-Token` header. | The request is **rejected with HTTP 403** (re-auth required). The value is NOT changed. *(Current code: returns 200 and changes the value — record as divergence FAIL.)* |
| 3 | Re-issue the PUT with a **correct** `X-Reauth-Token` (valid password). | The request succeeds. The Max parallel agents value can now be changed. |
| 4 | Set the value to `2`. Save. | The value is saved as `2`. A recommendation (e.g., based on CPU/RAM) may be shown alongside the field. |

**Pass/Fail:** ______

---

### UAT-AG-07 — Trust-graph screen shows to + modes (not accept_from)

**Traces to:** Spec-3 US-3 AC-3; FR-6.2  
**Preconditions:** UAT-AG-04 passed.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Navigate to the trust-graph or delegation visualization screen. | A visual or list representation of agent delegation edges is shown. |
| 2 | Locate Jim · Orchestrator's entry. | His `to` target (Ray) and `modes` (await) are visible. |
| 3 | Confirm that `accept_from` is NOT displayed as an active, enforced constraint in this view. | The `accept_from` field is either absent, clearly labelled as "reserved / not yet enforced", or greyed out — not shown as a live access control boundary. |

**Pass/Fail:** ______

---

### UAT-AG-08 — Delegation policy DENIES a work-path target outside the `to` allowlist

**Traces to:** Spec-3 FR-6.3 / SC-5; `pkg/agent/registry.go::CanSpawnSubagent` (returns `false` when a `DelegationPolicy` is set and the target is not in `to`)  
**Preconditions:** UAT-AG-04 passed — Jim · Orchestrator's delegation policy has `to=[ray]` (Ray · Scout only). This is the **work-path** (sub-agent dispatch / task delegation) check, distinct from ungated handover (UAT-AG-05).

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | With Jim's `to=[ray]`, instruct Jim to **delegate a work task** (spawn a sub-agent / dispatch a task) to **Ava · Builder** — a target NOT in his `to` allowlist. | The delegation is **DENIED** — `CanSpawnSubagent` returns false because Ava is not in `to`. Jim cannot dispatch the work task to Ava; a clear delegation-policy denial is surfaced (not a silent no-op, not a native fallthrough). |
| 2 | Instruct Jim to delegate the same work task to **Ray · Scout** (a target that IS in his `to` allowlist). | The delegation is **ALLOWED** — the task is dispatched to Ray. |
| 3 | Confirm the denial in step 1 did not silently run the task anyway. | No sub-agent task ran for Ava as a result of step 1. |

**Pass/Fail:** ______

---

## 6. Suite E — External Agent Runners

**Scope:** Spec-4 (executor field + external-cli dispatch via the Claude Code / Codex / opencode drivers; worktree-isolated; CLI self-sandbox is the boundary; consent best-effort post-hoc; connection test). `remote-a2a` remains reserved.

> **Wiring note (read before running this suite):** the external-cli runner is **being wired in the completeness phase**. The scenarios below target the **wired** implementation: the `external-cli` dispatch path (`pkg/agent/runner/dispatch.go`, drivers `driver_claude.go`/`driver_codex.go`/`driver_opencode.go`), the worktree isolation module (`pkg/agent/runner/worktree.go`), the post-hoc consent router (`pkg/agent/runner/consent.go`), and the new connection-test endpoint `POST /api/v1/agents/{id}/runner/test`. If `external-cli` still surfaces `ErrExternalCLINotWired` at dispatch (i.e. `ResolveDispatch` returns the reserved sentinel), the wiring is not landed yet — **UAT-EX-01 and UAT-EX-04 are FAIL, not N/A** (do not wave through on the "CLI absent" technicality). `remote-a2a` (UAT-EX-03 / UAT-EX-03b) stays reserved either way.

---

### UAT-EX-01 — Configure an external runner (Claude Code) and run the connection test

**Traces to:** Spec-4 US-1 (executor field); US-6 (connection test); FR-4.1  
**Preconditions:** Agents screen visible. The connection test is exercised via `POST /api/v1/agents/{id}/runner/test`. For the **healthy** path, the `claude` CLI binary must be installed and authenticated on the host.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Open a sub-agent configuration (create a sub-agent or open `UAT Custom Agent` from UAT-AG-03). | The agent configuration form is shown. |
| 2 | Locate the "Executor" field. | An Executor selector is present with options: `native`, `external-cli` (Claude Code, Codex, opencode), and `remote-a2a`. |
| 3 | Select `external-cli` → `Claude Code`. | Additional fields appear for the CLI path and/or credentials. |
| 4 | Save, then click "Test Connection" — this invokes `POST /api/v1/agents/{id}/runner/test`. With the `claude` binary present + authenticated, observe the response. | The endpoint returns a **healthy** result within 30 seconds (binary found · auth OK · handshake OK). The result indicates a health check only — NO actual task was executed (no worktree task run). |
| 5 | Confirm the three distinct outcomes the test can report are distinguishable. | The test result distinguishes (a) **missing binary**, (b) **unauthenticated** (binary present, auth handshake fails), and (c) **healthy** — three different messages, not one generic "failed". (The unauthed/healthy split requires a real CLI; mark those two sub-cases N/A only if the `claude` CLI cannot be installed.) |

**Pass/Fail:** ______  
**Note:** The **healthy** and **unauthed** outcomes require a real `claude` CLI; mark only those two sub-cases N/A if the CLI cannot be installed. The **missing-binary** outcome (UAT-EX-02) is always testable. If `external-cli` is not wired (dispatch returns `ErrExternalCLINotWired`), this scenario is **FAIL**, not N/A.

---

### UAT-EX-02 — Connection test reports a distinct missing-binary result

**Traces to:** Spec-4 US-6 AC-2; BDD "Connection test validates without running work" (error path)  
**Preconditions:** Agents screen visible. Can configure a runner pointing to a non-existent binary path. Always testable without a real CLI.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Configure an external runner pointing to a non-existent binary path (e.g., `/usr/local/bin/notexist-cli`). Click "Test Connection" (`POST /api/v1/agents/{id}/runner/test`). | The connection test fails cleanly — it does NOT crash the application. |
| 2 | Read the failure message. | The message clearly and specifically states the binary was **not found** — distinct from an "unauthenticated" result and from a generic error. |

**Pass/Fail:** ______

---

### UAT-EX-03 — remote-a2a executor is accepted in the schema but not resolvable

**Traces to:** Spec-4 US-1; BDD "remote-a2a is reserved, not resolvable"; FR-5.5  
**Preconditions:** Sub-agent configuration form open.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Set the Executor to `remote-a2a`. Save the sub-agent configuration. | The configuration is saved without an error. `remote-a2a` is a valid schema value. |
| 2 | Attempt to use or dispatch the sub-agent with `remote-a2a` executor. | A clear message is shown: "remote-a2a is not available in v0.1.0" or equivalent. The agent is NOT silently dispatched as native. |

**Pass/Fail:** ______

---

### UAT-EX-03b — remote-a2a dispatch surfaces the reserved sentinel (ErrRemoteA2AReserved)

**Traces to:** Spec-4 US-1; FR-5.5; `pkg/agent/runner/dispatch.go::ErrRemoteA2AReserved` (defined dispatch.go:26, returned by `ResolveDispatch` for `remote-a2a`)  
**Preconditions:** A sub-agent saved with `executor.kind=remote-a2a` (from UAT-EX-03).

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Dispatch a task to the `remote-a2a` sub-agent. Observe the gateway response / chat error. | Dispatch is refused with the reserved-sentinel error: `executor kind "remote-a2a" is reserved and not available in v0.1.0` (or the UI-surfaced equivalent). |
| 2 | Confirm the task did NOT silently fall back to native execution. | No native turn ran for the `remote-a2a` agent — the reserved kind blocks dispatch entirely. |

**Pass/Fail:** ______

---

### UAT-EX-04 — external-cli sub-agent dispatches in a worktree, streams output, and routes a permission request to consent (best-effort post-hoc)

**Traces to:** Spec-4 US-2; FR-5.3 (worktree isolation); BDD "External run streams events and routes a permission request"  
**Preconditions:** A working Claude Code runner configured + healthy in UAT-EX-01 (the wired `external-cli` path). **If `external-cli` is not wired (dispatch returns `ErrExternalCLINotWired`), this scenario is FAIL, not N/A.** Mark N/A only if the wiring is present but the `claude` CLI cannot be installed.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Configure a sub-agent with `executor.kind=external-cli` (Claude Code) and dispatch a small task that reads/writes a file (something Claude Code's own permission model will surface). | The task dispatches via the external-cli driver. The CLI runs inside an **isolated git worktree** (`pkg/agent/runner/worktree.go`), not the live workspace tree. |
| 2 | Observe the chat/run surface while the task runs. | Output **events stream** into the UI (NDJSON-decoded text / tool calls / diffs from the driver) — not a single blocking blob. |
| 3 | When Claude Code emits a permission request, observe the Omnipus UI. | A permission-request notification/overlay appears in the Omnipus UI — routed by `pkg/agent/runner/consent.go`. **Note this is best-effort post-hoc consent:** by the time Omnipus sees the request the CLI has already started that tool call, so a DENY cancels the whole run rather than vetoing the single call. **The real boundary is the CLI's own sandbox plus the worktree, not this consent layer.** |
| 4 | Deny the permission request. | The denial cancels the run; the task ends with a permission-denied / cancelled status (it does NOT silently complete). |
| 5 | Re-dispatch and approve when the request appears. | The run proceeds and continues streaming output events to completion. |

**Pass/Fail:** ______  
**Note:** Mark N/A only if the wiring is present but the `claude` CLI is unavailable. Wiring-absent (`ErrExternalCLINotWired`) is FAIL.

---

## 7. Suite F — Memory

**Scope:** Spec-5 FR-7 (two rooms, full frontmatter, remember/recall/retrospective, bleve FTS, frozen log formats).

---

### UAT-MEM-01 — remember stores a memory in the appropriate room

**Traces to:** Spec-5 US-1 (two rooms, workspace-keyed); US-3 (3 tools replace MEMORY.md); BDD "Private vs shared room routing"  
**Preconditions:** Application running, at least one workspace exists. An agent (Ray · Scout) is available.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Open a chat with Ray · Scout in "My Workspace". | Ray responds. |
| 2 | Ask Ray to remember a specific fact: "Remember this: the UAT passphrase is omega-delta-7." | Ray calls the `remember` tool and confirms the memory was stored. |
| 3 | On the server file system, inspect the `OMNIPUS_HOME` directory structure. | A file exists under `agents/ray/.omnipus/` (private room) OR under `<workspace>/.omnipus/` (shared room), depending on Ray's scoping — NOT under a plain `MEMORY.md` file at the agent root. |

**Pass/Fail:** ______

---

### UAT-MEM-02 — recall retrieves a previously stored memory (bleve FTS)

**Traces to:** Spec-5 US-4 (bleve FTS recall); BDD "recall uses bleve BM25"  
**Preconditions:** UAT-MEM-01 passed. The memory "omega-delta-7" is stored.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | In the same chat with Ray, ask: "What UAT passphrase did you remember earlier?" | Ray calls the `recall_memory` tool with a relevant query. |
| 2 | Observe Ray's response. | Ray retrieves and cites the earlier memory containing "omega-delta-7". The recall returns bleve BM25 full-text matches — not a vector/embedding search. |
| 3 | Confirm the recalled content matches what was stored in UAT-MEM-01 step 2. | The passphrase "omega-delta-7" is correctly recalled. |

**Pass/Fail:** ______

---

### UAT-MEM-03 — Memory file carries the full frontmatter schema

**Traces to:** Spec-5 US-2 (full per-memory file format, pinned); FR-7.2  
**Preconditions:** UAT-MEM-01 passed.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | On the file system, locate the memory file written in UAT-MEM-01 (within `agents/ray/.omnipus/` or the workspace `.omnipus/` dir). | The file exists and is a `.md` file. |
| 2 | Open and read the frontmatter (YAML block at the top of the file). | The frontmatter contains ALL of the following fields (even if some are empty): `id`, `title`, `type`, `tags`, `confidence`, `status`, `supersedes`, `author`, `born_in`. No field is omitted. |
| 3 | The `type` field value is one of the pinned types. | The type is one of: `decision`, `fact`, `reference`, `lesson`, `person`, `project`, `moc`, `note`. |

**Pass/Fail:** ______

---

### UAT-MEM-04 — retrospective summarizes a session's memories

**Traces to:** Spec-5 US-3 (3 tools); BDD "3 tools replace MEMORY.md"  
**Preconditions:** UAT-MEM-01 and UAT-MEM-02 passed.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | In the chat with Ray, ask Ray to run a retrospective: "Please do a retrospective on this session." | Ray calls the `retrospective` tool. |
| 2 | Observe the response. | Ray produces a session summary / retrospective citing memories from the session. The output is non-empty and references the memory stored in UAT-MEM-01. |

**Pass/Fail:** ______

---

### UAT-MEM-05 — A SHARED memory does NOT cross workspaces

**Traces to:** Spec-5 US-1; FR-7.1 (per-workspace shared room, keyed by `workspace_id`); `pkg/memrooms/rooms.go::ResolveWorkspaceSharedRoom` (shared room path `workspaces/<workspace_id>/.omnipus/`)  
**Preconditions:** UAT-WS-06 passed. "UAT Workspace Beta" exists. In "My Workspace", store a memory into the **shared room** explicitly (e.g., ask Ray to "remember this for the whole workspace: the shared UAT marker is sigma-shared-9"), so it lands under `workspaces/<my-workspace-id>/.omnipus/`, not Ray's private room.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Confirm on the file system that the shared memory was written under "My Workspace"'s shared room (`workspaces/<my-workspace-id>/.omnipus/`), not under `agents/ray/.omnipus/`. | The `sigma-shared-9` memory file is in the workspace-keyed shared room directory. |
| 2 | Switch to "UAT Workspace Beta". Open a chat with Ray. Ask: "Recall the shared UAT marker." | Ray performs a recall scoped to "UAT Workspace Beta"'s shared room. |
| 3 | Observe the result. | The shared memory `sigma-shared-9` is **NOT returned** — it belongs to "My Workspace"'s shared room and does not cross into "UAT Workspace Beta". (Per the Spec-5 room model, shared rooms are keyed by `workspace_id`; a different workspace cannot read another's shared room.) |

**Pass/Fail:** ______

---

### UAT-MEM-06 — No MEMORY.md file is the backing store

**Traces to:** Spec-5 US-3; FR-7.3 (3 tools replace MEMORY.md)  
**Preconditions:** UAT-MEM-01 passed.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | On the file system, inspect the agent directory `OMNIPUS_HOME/agents/ray/` for a file named `MEMORY.md`. | No `MEMORY.md` file exists at the agent root as the active memory store. The memory is stored in the `.omnipus/` room structure instead. |

**Pass/Fail:** ______

---

### UAT-MEM-07 — recall survives deletion of the bleve index (rebuild from `.md`)

**Traces to:** Spec-5 FR-7.4 (the `.md` files are the source of truth; the bleve index is a derived cache, rebuildable); `pkg/memrooms/index/index.go` (`IndexSubdir = ".index/bleve"`, corruption recovery + `Rebuild()` from `MemoriesDir`)  
**Preconditions:** UAT-MEM-01 and UAT-MEM-02 passed — at least one memory exists and was recalled via bleve.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Stop the gateway. On the file system, delete the bleve index directory (`<room>/.index/bleve/`) while leaving all memory `.md` files intact. | The index dir is removed; the `.md` memory files remain. |
| 2 | Restart the gateway with the same `OMNIPUS_HOME`. | The gateway boots — it does NOT crash. The index is recreated/rebuilt from the `.md` files (`OpenOrCreate` → `Rebuild`). |
| 3 | Ask Ray to recall the earlier memory: "What UAT passphrase did you remember?" | Ray recalls "omega-delta-7" successfully — the recall works against the **rebuilt** index, proving the `.md` files are the durable source of truth and the bleve index is a derived, rebuildable cache. |

**Pass/Fail:** ______

---

### UAT-MEM-08 — Append-only logs in frozen formats (counters.jsonl + `born_in` frontmatter)

**Traces to:** Spec-5 FR-7.5 (frozen append-only log + provenance formats); `pkg/memrooms/memory_file.go` (`CounterRecord {ts, memory_id, op, by}`, op ∈ `{access|drift|cited}`, `AppendCounterRecord`; `BornIn` frontmatter `born_in`)  
**Preconditions:** UAT-MEM-01 and UAT-MEM-02 passed — a memory was stored and recalled (recall produces an `access`/`cited` counter event).

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | On the file system, locate the room's `counters.jsonl` and open the last few lines. | Each line is a JSON object with the **frozen shape** `{"ts": <RFC3339 UTC>, "memory_id": <id>, "op": <one of access\|drift\|cited>, "by": <agent id>}`. No field is renamed or dropped. The file is append-only (records are added, not rewritten). |
| 2 | Open the memory `.md` file from UAT-MEM-01 and inspect its YAML frontmatter. | The frontmatter carries a `born_in` field recording the session/provenance the memory was created in (matching the `BornIn` field, `yaml:"born_in"`). |
| 3 | Confirm the `op` value is one of the three frozen enum values. | `op` is exactly one of `access`, `drift`, or `cited` — no other value appears. |

**Pass/Fail:** ______

---

## 8. Suite G — Tasks & Calendar

**Scope:** Spec-5 FR-8 (task fields start/due/recurrence/blocked_by, cycle/orphan validator, Calendar shell).

---

### UAT-TSK-01 — Create a task with start and due dates

**Traces to:** Spec-5 US-6; FR-8.1 (additive task fields)  
**Preconditions:** Application running. "My Workspace" is the active workspace.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Navigate to the Tasks view in "My Workspace". | The task list is shown. |
| 2 | Create a new task titled "UAT Task Alpha". Set start date to today and due date to 7 days from today. Save. | The task is created with both start and due dates visible in the task entry. |
| 3 | Create a second task titled "UAT Task Beta". Leave start and due dates empty. Save. | The task is created successfully with no date fields set. |

**Pass/Fail:** ______

---

### UAT-TSK-02 — blocked_by edge: create a valid DAG dependency

**Traces to:** Spec-5 US-6; BDD "blocked_by cycle rejected at write" (positive path); FR-8.2  
**Preconditions:** UAT-TSK-01 passed. "UAT Task Alpha" and "UAT Task Beta" exist.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Open "UAT Task Beta". Add "UAT Task Alpha" to the `blocked_by` field (Beta is blocked by Alpha). | The dependency is saved. "UAT Task Beta" is shown as blocked by "UAT Task Alpha". |
| 2 | Confirm the relationship is readable in the task view. | The task detail or list view indicates the dependency (e.g., "Blocked by: UAT Task Alpha"). |

**Pass/Fail:** ______

---

### UAT-TSK-03 — blocked_by cycle is rejected at write

**Traces to:** Spec-5 US-6; BDD "blocked_by cycle rejected at write"; FR-8.2  
**Preconditions:** UAT-TSK-02 passed. Alpha → Beta dependency exists (Beta blocked_by Alpha).

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Open "UAT Task Alpha". Attempt to add "UAT Task Beta" to Alpha's `blocked_by` field (this would create a cycle: Alpha blocked_by Beta, and Beta blocked_by Alpha). | The operation is rejected with a clear error message such as "Circular dependency detected" or "Cycle rejected". |
| 2 | Confirm that Alpha's `blocked_by` field does NOT contain Beta. | No cycle exists after the failed operation. |

**Pass/Fail:** ______

---

### UAT-TSK-04 — Recurrence field is stored but not auto-executed

**Traces to:** Spec-5 FR-8.1 (recurrence field additive); FR-8.3 (engine is v0.2.0)  
**Preconditions:** UAT-TSK-01 passed.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Open "UAT Task Alpha". Set a recurrence value (e.g., `weekly` or `RRULE:FREQ=WEEKLY`). Save. | The recurrence value is stored and shown in the task detail. |
| 2 | Wait a few moments. Confirm no auto-spawned duplicate task appears. | No new task is auto-created. The recurrence field is stored but the engine that would act on it is deferred to v0.2.0. |

**Pass/Fail:** ______

---

### UAT-TSK-05 — Calendar shell renders scheduled tasks

**Traces to:** Spec-5 US-7; BDD e2e "Calendar shell renders scheduled tasks/events"; FR-8.3  
**Preconditions:** UAT-TSK-01 passed. "UAT Task Alpha" has a due date set.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Navigate to the Calendar view (within the current workspace). | A Calendar UI surface is shown — a shell/view, not a blank page or 404. |
| 2 | Confirm the Calendar is scoped to the current workspace. | The workspace name is visible in context, or the Calendar is accessible from within the workspace navigation. |
| 3 | Locate the date corresponding to "UAT Task Alpha"'s due date on the calendar. | "UAT Task Alpha" (or a representation of it) appears on or near that date. |

**Pass/Fail:** ______

---

### UAT-TSK-06 — Deleting a task cascade-cleans its blocked_by edges

**Traces to:** Spec-5 FR-8.2 (delete cascade semantics)  
**Preconditions:** UAT-TSK-02 passed. "UAT Task Beta" is blocked by "UAT Task Alpha".

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Delete "UAT Task Alpha". | The task is deleted. |
| 2 | Open "UAT Task Beta" (which was blocked by Alpha). | The `blocked_by` field no longer references "UAT Task Alpha" (the edge was cascade-cleaned). No orphan reference remains. |

**Pass/Fail:** ______

---

### UAT-TSK-07 — Completing a prerequisite advances its dependents (DAG unblock)

**Traces to:** Spec-3 US-6; BDD "Orchestrator advances a DAG on task_status_changed"; FR-6.5; `pkg/gateway/rest_board.go:646-647` (the "completed task advanced dependents" log) + the GET `/api/v1/board/tasks/{id}` `blocked_by` field (`toWireBoardTask`)  
**Preconditions:** Two tasks exist with a `blocked_by` dependency. Jim · Orchestrator is configured as the coordinator.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Create Task C ("Gated Task") blocked by Task D ("Prerequisite Task"). | The dependency is set — `GET /api/v1/board/tasks/<C>` shows `blocked_by` containing Task D. |
| 2 | Mark Task D as complete. | Task D's status changes to "done"/"complete". On completion the board logs the exact line `rest: board task: completed task advanced dependents` with `completed_id=<D>` and `advanced_ids=[<C>]` (`pkg/gateway/rest_board.go:646-647`). |
| 3 | Verify the dependent advanced — choose the deterministic check that fits the harness, **not** "logs OR UI" loosely: (a) confirm the **log line** above appears with Task C in `advanced_ids`, **or** (b) `GET /api/v1/board/tasks/<C>` shows Task D's `blocked_by` entry now satisfied / cleared. | Either the log line names Task C in `advanced_ids`, OR the GET on Task C shows its `blocked_by` dependency on Task D is now satisfied. (Pick one; the chosen check must be observed, not inferred.) |

**Pass/Fail:** ______

---

## 9. Suite H — Skills & Integrations

**Scope:** Spec-6 (skill tools wired, create/edit, default skills, per-agent allowlist, Integrations UI, marketplace list).

---

### UAT-SK-01 — skill.list returns real results (not a stub placeholder)

**Traces to:** Spec-6 US-1 (skill tools wired); BDD "Stub skill tools are wired to the real engine"; FR-9.1  
**Preconditions:** Application running, at least one default skill is installed (UAT-SK-03 covers this; run UAT-SK-03 first if the order matters).

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Open a chat with Mia · Assistant. | Mia responds. |
| 2 | Ask Mia to list available skills: "List all available skills." | Mia calls `system.skill.list`. The result is a non-empty list of skills (NOT a placeholder like "stub response" or "not implemented"). |
| 3 | All four default skills should be listed: `summarize`, `skill-authoring`, `plan`, `daily-briefing` (the exact embedded set — `pkg/skills/embedded/`, asserted by `pkg/skills/embed_test.go`). | **All 4** of the default skills appear in the list (not a subset). |

**Pass/Fail:** ______

---

### UAT-SK-02 — skill.search returns results from the real registry

**Traces to:** Spec-6 US-1; FR-9.1  
**Preconditions:** Network access to ClawHub or a configured registry. Mark N/A if no registry is reachable.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Ask Mia to search for a skill: "Search for skills related to summarization." | Mia calls `system.skill.search`. A list of matching skills from the registry is returned — NOT a stub response. |

**Pass/Fail:** ______

---

### UAT-SK-03 — Default skills are embedded and seeded on fresh install

**Traces to:** Spec-6 US-3; BDD "Default skills embedded and seeded on fresh install"; FR-9.3  
**Preconditions:** Fresh install (or verify that the default skills directory is populated).

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | On the file system, inspect the skills directory (typically `OMNIPUS_HOME/skills/` or embedded path). | The 4 default skills exist: `summarize`, `skill-authoring`, `plan`, `daily-briefing`. |
| 2 | Confirm these were not manually placed there — they should exist on a fresh `OMNIPUS_HOME` that was just booted without any manual setup. | The files are present after first boot with an empty home dir (they are seeded from `go:embed`). |

**Pass/Fail:** ______

---

### UAT-SK-04 — Authoring a skill (skill.create) requires consent

**Traces to:** Spec-6 US-2; BDD "Authoring a skill is consent-gated and versioned"; FR-9.2  
**Preconditions:** Application running.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Open a chat with Ava · Builder. | Ava responds. |
| 2 | Ask Ava to create a new skill: "Create a new skill called 'uat-test-skill' that does a simple greeting." | Ava calls `system.skill.create`. The **`ToolApprovalRequest` consent overlay** is shown — delivered over the `ws_approval` WebSocket frame and rendered by the approval block (`src/components/chat/ExecApprovalBlock.tsx`; backend `pkg/gateway/ws_approval.go`, 90s timeout). This is the primary consent path — NOT a password re-type. |
| 3 | Approve the tool-consent overlay. | The skill creation proceeds. |
| 4 | Confirm the skill was created. | Ava reports success. Listing skills now shows `uat-test-skill`. |
| 5 | Check the skill directory for a version snapshot. | A `.versions/` sub-directory or versioning artifact exists alongside or near the new skill file, indicating rollback capability. |

**Pass/Fail:** ______

---

### UAT-SK-05 — Editing a built-in skill creates an override, not an in-place mutation

**Traces to:** Spec-6 US-2 AC-2; BDD "Editing a built-in creates an override"; FR-9.2  
**Preconditions:** UAT-SK-03 passed. The `summarize` built-in skill is present.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Ask Ava to edit the built-in `summarize` skill: "Edit the summarize skill to add a note in the description." | Ava initiates `system.skill.edit` on `summarize`. The **`ToolApprovalRequest` consent overlay** (`ws_approval` frame → `ExecApprovalBlock.tsx`) appears — the primary consent path, NOT a password re-type. |
| 2 | Approve the tool-consent overlay. | The edit proceeds. |
| 3 | Inspect the skill directory on the file system. | The original `summarize` skill file in the embedded / default-skills location is unchanged. A user-override version of `summarize` exists in a separate override location (e.g., a user skills directory). The built-in was NOT mutated in place. |

**Pass/Fail:** ______

---

### UAT-SK-06 — Per-agent skill allowlist: Mia sees summarize, not plan

**Traces to:** Spec-6 US-4 (per-agent allowlist + progressive disclosure); FR-9.4  
**Preconditions:** Default skills are seeded (UAT-SK-03). The default allowlist matrix is: summarize→Mia, plan→Jim.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Open a chat with Mia · Assistant. Ask Mia to invoke the `plan` skill directly: "Use the plan skill." | Mia cannot invoke the `plan` skill. Either a "skill not allowed for this agent" error is shown, or Mia reports that the plan skill is not available to her. |
| 2 | Ask Mia to use the `summarize` skill: "Use the summarize skill to summarize our conversation." | Mia successfully invokes the `summarize` skill. |

**Pass/Fail:** ______

---

### UAT-SK-07 — Marketplace provider list fans out across multiple registries

**Traces to:** Spec-6 US-5; BDD "Marketplace search fans out across the list"; FR-10.1  
**Preconditions:** `RegistryConfig` is configured with at least 2 registries (ClawHub + GitHub). Mark N/A if only one registry is reachable.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | In Settings → Marketplace / Registries (or via skill search), configure two registries: ClawHub and a GitHub repository. | Both registries are listed and enabled. |
| 2 | Search for skills. | Results appear from both registries (indicated by source/registry metadata). A single search fans out across both. |

**Pass/Fail:** ______

---

### UAT-SK-08 — Integrations provider-picker shows search + voice providers, and the provider PUT IS re-auth gated (positive control)

**Traces to:** Spec-6 US-7; FR-12.1; FR-12.2 re-auth — `requireReAuth` at `pkg/gateway/rest_integrations_auth.go:379` (the **one** currently-gated sensitive PUT)  
**Preconditions:** Application running. Settings screen accessible. This is the **positive control** for the re-auth gate (contrast UAT-AUTH-03 / UAT-AG-06, which are not yet gated).

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Navigate to Settings → Integrations. | An Integrations screen is shown — separate from the LLM model provider section. |
| 2 | Confirm the Integrations screen shows at least two provider categories: Search (web search providers) and Voice / Transcription. | Both categories are visible with their respective available providers (e.g., a search provider like Perplexity / Tavily, and a voice transcriber). |
| 3 | Issue `PUT /api/v1/integrations/providers/{id}` to change a provider API key **without** an `X-Reauth-Token` header. | The request is **rejected with HTTP 403** — `requireReAuth` blocks it. The provider setting is NOT changed. |
| 4 | Re-issue the PUT with an **incorrect** `X-Reauth-Token` (wrong password). | The request is **rejected with HTTP 403** — the wrong token is not accepted. The setting is NOT changed. |
| 5 | Re-issue the PUT with a **correct** `X-Reauth-Token` (valid password) — or, via the SPA, complete the password re-type dialog. | The request succeeds (200). The provider setting is updated. |

**Pass/Fail:** ______

---

### UAT-SK-09 — Composer mic button is present in the chat UI

**Traces to:** Spec-6 US-7 (composer mic); FR-12.1  
**Preconditions:** Application running, chat UI open.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Navigate to the chat with Mia · Assistant. | The chat composer (message input area) is visible. |
| 2 | Locate a microphone icon or voice-input button in the composer area. | A mic/voice icon is visible in the chat composer area. |
| 3 | If a microphone/audio device is available: click the mic button. | The UI enters a voice-recording mode (e.g., a recording indicator appears). |

**Pass/Fail:** ______

---

## 10. Suite I — Auth, Profile & Settings

**Scope:** Spec-6 FR-12.2 (single-user/one-password, Profile vs Settings, sensitive setting re-auth); ADR-019 FR-12 consent primitive.

---

### UAT-AUTH-01 — Login with the correct password

**Traces to:** Spec-6 US-8; FR-12.2 (single-user/one-password)  
**Preconditions:** Onboarding completed (UAT-ON-02). The password `S3cure!UAT` was set.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | If a session is active, log out. Navigate to the login screen. | The login screen is shown with a username and password field. |
| 2 | Enter the username `TestOperator` and password `S3cure!UAT`. Click Login. | Login succeeds. The main application UI loads. |

**Pass/Fail:** ______

---

### UAT-AUTH-02 — Wrong password is rejected

**Traces to:** Spec-6 FR-12.2  
**Preconditions:** On the login screen.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Enter username `TestOperator` and an incorrect password (e.g., `wrongpassword`). Click Login. | Login fails. An error message is shown. The user is NOT logged in. |

**Pass/Fail:** ______

---

### UAT-AUTH-03 — Model-key change is re-auth gated (KNOWN SPEC-VS-CODE DIVERGENCE)

**Traces to:** Spec-6 US-8; BDD "Sensitive setting requires the one password"; FR-12.2  
**Preconditions:** Logged in.

> **⚠️ KNOWN DIVERGENCE — DO NOT WAVE THROUGH.** Per spec FR-12.2, **all** sensitive settings (model key, performance / max-parallel, Integrations providers) must require a re-auth consent token (`X-Reauth-Token`). In the current code, `requireReAuth` gates **only** the Integrations provider PUT (`pkg/gateway/rest_integrations_auth.go:379`). The **model-key PUT** (`PUT /api/v1/providers/{id}`, `pkg/gateway/rest.go:3603-3720`; also `rest_settings.go`) and the **performance PUT** (`pkg/gateway/rest_performance.go::putPerformance`) are **NOT** gated. This is a tracked completeness-phase fix. The expected result below is written to the **spec target** (gate enforced). Until the fix lands this scenario is **FAIL** — record it as a divergence FAIL, do **not** pass it on an "or saving is allowed" technicality. UAT-SK-08 covers the one currently-gated path (Integrations PUT) as the positive control.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Navigate to Settings → Model / LLM configuration (the section holding the model API key). | The settings panel is shown. |
| 2 | Via the API, issue `PUT /api/v1/providers/{id}` to change the model key **without** an `X-Reauth-Token` header (or with the SPA, attempt the change without completing the re-auth dialog). | The request is **rejected with HTTP 403** (re-auth required). The model key is NOT changed. *(Current code: returns 200 and changes the key — record as divergence FAIL.)* |
| 3 | Re-issue the PUT with an **incorrect** re-auth token (wrong password). | The request is rejected with **HTTP 403**. The setting is NOT changed. |
| 4 | Re-issue the PUT with a **correct** `X-Reauth-Token` (valid password). | The request succeeds (200). The model key is updated. |

**Pass/Fail:** ______

---

### UAT-AUTH-04 — Profile is separate from Settings

**Traces to:** Spec-6 US-8 (Profile vs Settings); FR-12.2  
**Preconditions:** Logged in.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Locate the Profile menu (typically a user icon in the header/sidebar). Click it. | A Profile menu or Profile page opens. It contains personal account items (display name, avatar, password change). |
| 2 | Navigate to Settings (usually a gear icon or Settings menu item). | A Settings page opens. It contains application-level configuration (model provider, connections, Integrations, Performance). |
| 3 | Confirm that Profile and Settings are distinct surfaces — personal config is in Profile, app config is in Settings. | The two are visually and functionally distinct. Personal items do NOT appear under Settings' app-level sections. |

**Pass/Fail:** ______

---

## 11. Cross-Cutting Checks

These checks are run ONCE at the end of a full UAT run, after all suites above.

---

### UAT-CC-01 — Zero unacceptable console errors

**Traces to:** ADR-019 cross-cutting (no schema drift, no unhandled exceptions)

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Open the browser DevTools console (F12 → Console). | The console is visible. |
| 2 | Navigate through each screen visited during UAT (Workspaces, Agents, Connections, Tasks, Calendar, Skills, Settings, Chat). | While navigating, observe the console output. |
| 3 | Count entries categorized as `[Error]` or `[Uncaught]`. WebSocket "reconnecting…" warnings are EXCLUDED from the count. | Zero `[Error]` or `[Uncaught]` entries that are not WebSocket reconnect warnings. |

**Pass/Fail:** ______

---

### UAT-CC-02 — No WS schema-validation drop events

**Traces to:** ADR-019 Constraint #8 (contract-first, Zod validation); NFR-1

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | With DevTools open, navigate through all major screens. | Observe any dev-mode toast notifications or console log entries containing text like "zod-schema-invalid", "frame dropped", or "schema validation failed". |
| 2 | Count such events. | Zero schema-validation drop events. If any appear, the failing frame's route and type must be recorded as a test failure. |

**Pass/Fail:** ______

---

### UAT-CC-03 — Dark-first UI: the application defaults to a dark theme

**Traces to:** CLAUDE.md "Chat-first, dark-first"; brand guidelines (#0A0A0B Deep Space Black)

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Load the application in a browser with NO explicit dark-mode preference set. | The application renders in a dark theme by default (dark backgrounds, light text). |
| 2 | Inspect the main background color of the chat area. | The background is dark (approximately #0A0A0B or a close dark tone). It is NOT white or light-grey by default. |

**Pass/Fail:** ______

---

### UAT-CC-04 — No emoji in UI chrome or stored data

**Traces to:** CLAUDE.md "No emoji in stored data or UI chrome"

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Navigate through the sidebar, navigation, settings labels, agent names, skill names, and task titles created during UAT. | Zero emoji characters appear in UI chrome elements (navigation labels, button text, field labels, sidebar items). |
| 2 | Open a created workspace, task, or agent and inspect the stored name. | No emoji are stored in the names or descriptions created during UAT (emoji were not used in UAT input). |

**Pass/Fail:** ______

---

### UAT-CC-05 — Header displays non-zero token/cost counter after a conversation

**Traces to:** General product functionality (token/cost observability)  
**Preconditions:** At least one multi-turn chat exchange completed during UAT (e.g., UAT-MEM-01 through UAT-MEM-04).

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | After completing a multi-turn conversation with any agent, observe the token or cost counter in the header/status bar. | A non-zero token count (and/or cost estimate) is displayed for the current session. The counter does NOT show "0" or "—" after messages have been exchanged. |

**Pass/Fail:** ______

---

### UAT-CC-06 — make verify-contracts exits 0 (contract drift check)

**Traces to:** ADR-019 NFR-3 (contract-first); Constraint #8; every spec's CI gate

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | On the build machine, run: `make verify-contracts` | The command exits with code 0. |
| 2 | If exit code is non-zero: `git diff pkg/api/generated/ src/lib/api/generated/` | Exit 0 is required. Non-zero exit = contract drift. The diff output identifies the affected generated files. |

**Pass/Fail:** ______

---

### UAT-CC-07 — Owner attribution is present but never enforces access control

**Traces to:** Spec-1 US-4; FR-1.7/FR-1.9 (owner gate removed; attribution only)

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Create a **fresh task** (`POST /api/v1/board/tasks`) and a **fresh workspace** (`POST /api/v1/workspaces`) as `TestOperator`, then inspect each. | Both stamp `owner = "TestOperator"` (the creating user's username) — task owner via `pkg/gateway/rest_board.go:586`, workspace owner via `pkg/gateway/rest_workspaces.go:511`. The `owner` field is populated on creation, not left blank. |
| 2 | Via the API (curl or browser DevTools Network tab), make a request to GET a resource that belongs to `TestOperator` while using dev-bypass or another session identity. | The resource is returned with HTTP 200 — NOT a 404 or 403. The owner field is attribution only; it is never a gate to access in the single-user configuration. |

**Pass/Fail:** ______

---

### UAT-CC-08 — Command Center / Monitor / Policies screens load with zero console errors

**Traces to:** SPA IA — these screens ship but are not visited by any other scenario (`src/routes/_app/command-center.tsx`, `monitor.tsx`, `policies.tsx`)  
**Preconditions:** Application running, logged in. DevTools console open.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Navigate to `http://localhost:8080/command-center`. | The Command Center screen renders (not a 404 / blank). No `[Error]`/`[Uncaught]` console entries (WS reconnect warnings excluded). No `zod-schema-invalid` WS-drop toast. |
| 2 | Navigate to `http://localhost:8080/monitor`. | The Monitor screen renders. Zero unacceptable console errors and zero WS-drop toasts. |
| 3 | Navigate to `http://localhost:8080/policies`. | The Policies screen renders. Zero unacceptable console errors and zero WS-drop toasts. |

**Pass/Fail:** ______

---

### UAT-CC-09 — SPA-embed freshness: the running binary serves the rebuilt SPA

**Traces to:** CLAUDE.md SPA Embed Pipeline; `pkg/gateway/embed.go:18` (`//go:embed all:spa`)  
**Preconditions:** Binary built per §1.3 (SPA synced to `pkg/gateway/spa/` before `go build`).

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Before building, inject a unique marker into the SPA build (or note the Vite-hashed `assets/index-*.js` filename produced by `npm run build`). Rebuild and re-embed per §1.3. | The marker / hashed filename is present in `pkg/gateway/spa/assets/`. |
| 2 | Boot the binary and fetch the served index: `curl -s http://localhost:8080/ \| grep -o 'index-[a-z0-9]*\.js'`. | The served `index-*.js` filename **matches** the freshly built `pkg/gateway/spa/assets/` asset — confirming the running binary serves the rebuilt SPA, not a stale embed. (`index.html` is served `no-cache`, so this reflects the current build.) |

**Pass/Fail:** ______

---

### UAT-NEG-01 — A non-tool-capable model key fails turns GRACEFULLY (not a silent hang)

**Traces to:** §1.3 Step 3 (model must support tool use); `pkg/agent/loop.go` (`providers.ClassifyError` marks a tool-unsupported/404 provider error non-retriable → emits `EventKindError` to the UI, loop.go:~4810)  
**Preconditions:** Application running. A model key for a **non-tool-capable** model (e.g., `google/gemma-2-9b-it`) configured.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | Configure the LLM provider to a non-tool-capable model. Open a chat with Mia and send a message. | The turn fails **gracefully**: a visible error frame/message appears in the chat (an `EventKindError` surfaced from the loop), e.g. "model does not support tools" / a 404-class provider error. |
| 2 | Confirm the failure mode. | The turn does NOT hang silently or spin indefinitely — the error is surfaced promptly and the UI returns to an idle, usable state. No browser crash, no unhandled `[Uncaught]` console exception. |

**Pass/Fail:** ______

---

### UAT-NEG-02 — Transcriber-absent: the composer mic degrades gracefully (no crash)

**Traces to:** Spec-6 US-7 (composer mic); `src/components/chat/MessageInput.tsx:81-90` (on 503 / missing transcriber → error toast "No voice transcriber configured. Add one in Settings → Integrations."; mic returns to idle)  
**Preconditions:** Application running, chat open. **No** voice transcriber provider configured in Settings → Integrations.

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | With no transcriber configured, locate the composer mic button. | The mic button is present (it may be disabled, or enabled-but-degraded). |
| 2 | If the mic is interactable, attempt a voice transcription (record + send). | The action degrades gracefully: an error **toast** appears (e.g., "No voice transcriber configured. Add one in Settings → Integrations.") and the mic returns to the idle state. No crash, no unhandled `[Uncaught]` console exception, no infinite spinner. |

**Pass/Fail:** ______

---

## 12. Holdout / Acceptance Gates

The following scenarios are **hard blockers for release acceptance**. Every scenario in this list must be PASS before the v0.1.0 release is accepted. A single FAIL in this list blocks the release.

| Gate ID | Scenario | Suite |
|---------|----------|-------|
| G-01 | Fresh install boots and shows onboarding wizard | UAT-ON-01 |
| G-02 | Completing onboarding provisions Mia in My Workspace (4-base roster present, Max absent) | UAT-ON-02 |
| G-03 | "My Workspace" is the only workspace seeded on first boot | UAT-WS-01 |
| G-04 | Default workspace cannot be deleted (409) | UAT-WS-02 |
| G-05 | `/workspaces` route serves correctly; `/projects` is not a live workspace UI route | UAT-WS-07 |
| G-06 | Tasks are scoped per workspace (no cross-workspace bleed) | UAT-WS-06 |
| G-07 | Cap-1 per channel type is enforced | UAT-CN-03 |
| G-08 | Channel secrets are stored as `_ref` (never plaintext) | UAT-CN-02 (step 3) |
| G-09 | 4-base roster is present; built-in agent identities are locked | UAT-AG-01 |
| G-10 | Built-in agent prompts are not surfaced in the UI | UAT-AG-02 |
| G-11 | remember → recall round-trip succeeds | UAT-MEM-01 + UAT-MEM-02 |
| G-12 | Memory file carries full frontmatter schema | UAT-MEM-03 |
| G-13 | MEMORY.md is NOT the backing store | UAT-MEM-06 |
| G-14 | blocked_by cycle is rejected at write | UAT-TSK-03 |
| G-15 | Deleting a task cascade-cleans its blocked_by edges | UAT-TSK-06 |
| G-16 | Default skills embedded and seeded on fresh install | UAT-SK-03 |
| G-17 | skill.list is wired to the real engine (not a stub) | UAT-SK-01 |
| G-18 | Authoring a skill requires consent (ToolApprovalRequest / `ws_approval` overlay) | UAT-SK-04 |
| G-19 | Editing a built-in skill creates an override, not an in-place mutation | UAT-SK-05 |
| G-20 | Per-agent skill allowlist enforces deny (Mia cannot use plan skill) | UAT-SK-06 |
| G-21 | Integrations provider PUT is re-auth gated (403 without/with-wrong `X-Reauth-Token`) — the one currently-wired sensitive PUT | UAT-SK-08 |
| G-22 | Login rejects an incorrect password | UAT-AUTH-02 |
| G-23 | Zero unacceptable console errors across all screens | UAT-CC-01 |
| G-24 | Zero WS schema-validation drop events | UAT-CC-02 |
| G-25 | `make verify-contracts` exits 0 | UAT-CC-06 |
| G-26 | Delegation policy DENIES a work-path target outside `to` | UAT-AG-08 |
| G-27 | recall survives bleve-index deletion (rebuild from `.md`) | UAT-MEM-07 |
| G-28 | Append-only logs in frozen formats (counters.jsonl + `born_in`) | UAT-MEM-08 |

---

## 13. Results Matrix

Fill this in as you run each scenario. One row per scenario ID.

| ID | Title | Result (P/F/B/N/A) | Notes |
|----|-------|--------------------|-------|
| UAT-ON-01 | First-boot wizard appears | | |
| UAT-ON-02 | Complete 3-step onboarding | | |
| UAT-ON-03 | No re-onboarding on reboot | | |
| UAT-ON-04 | Onboarding rejects mismatched password confirm | | |
| UAT-WS-01 | Default workspace seeded once | | |
| UAT-WS-02 | Default workspace delete-protected (409) | | |
| UAT-WS-03 | Create new workspace | | |
| UAT-WS-04 | Rename workspace | | |
| UAT-WS-05 | Switch between workspaces | | |
| UAT-WS-06 | Tasks scoped per workspace | | |
| UAT-WS-07 | /workspaces serves; /projects gone (404) | | |
| UAT-CN-01 | Connectors screen accessible | | |
| UAT-CN-02 | Add Telegram instance; secret as ref | | |
| UAT-CN-03 | One-per-type cap enforced | | |
| UAT-CN-04 | Email IMAP+SMTP configured (test endpoint JSON) | | |
| UAT-CN-05 | IMAP down: graceful degraded boot | | |
| UAT-CN-06 | Inbound email reaches bus | | |
| UAT-CN-07 | config.json 2-of-a-type rejected at LOAD | | |
| UAT-CN-08 | Channel identity{agent\|user} binds routing | | |
| UAT-AG-01 | 4-base roster; built-in identities locked | | |
| UAT-AG-02 | Built-in prompts not surfaced | | |
| UAT-AG-03 | Custom agent: ungated creation | | |
| UAT-AG-04 | Delegation policy: to + modes | | |
| UAT-AG-05 | Handover not gated by delegation | | |
| UAT-AG-06 | Max-parallel re-auth gated (DIVERGENCE) | | |
| UAT-AG-07 | Trust-graph shows to+modes, not accept_from | | |
| UAT-AG-08 | Delegation DENIES out-of-`to` work target | | |
| UAT-EX-01 | external-cli runner config + connection test | | |
| UAT-EX-02 | Missing binary: distinct missing-binary result | | |
| UAT-EX-03 | remote-a2a: accepted in schema, not resolvable | | |
| UAT-EX-03b | remote-a2a dispatch surfaces ErrRemoteA2AReserved | | |
| UAT-EX-04 | external-cli: worktree dispatch, stream, post-hoc consent | | |
| UAT-MEM-01 | remember stores in correct room | | |
| UAT-MEM-02 | recall returns stored memory (bleve) | | |
| UAT-MEM-03 | Memory file carries full frontmatter | | |
| UAT-MEM-04 | retrospective summarizes session | | |
| UAT-MEM-05 | Shared memory does NOT cross workspaces | | |
| UAT-MEM-06 | MEMORY.md not the backing store | | |
| UAT-MEM-07 | recall survives bleve-index deletion (rebuild) | | |
| UAT-MEM-08 | Append-only frozen logs (counters.jsonl + born_in) | | |
| UAT-TSK-01 | Create task with start/due dates | | |
| UAT-TSK-02 | blocked_by: valid DAG dependency | | |
| UAT-TSK-03 | blocked_by: cycle rejected | | |
| UAT-TSK-04 | Recurrence stored but not auto-run | | |
| UAT-TSK-05 | Calendar shell renders tasks | | |
| UAT-TSK-06 | Delete task cascades edge cleanup | | |
| UAT-TSK-07 | Completing prerequisite advances dependents (DAG unblock) | | |
| UAT-SK-01 | skill.list wired to real engine (all 4 defaults) | | |
| UAT-SK-02 | skill.search returns registry results | | |
| UAT-SK-03 | Default skills embedded and seeded | | |
| UAT-SK-04 | skill.create: consent-gated and versioned | | |
| UAT-SK-05 | skill.edit: built-in override | | |
| UAT-SK-06 | Per-agent allowlist enforced | | |
| UAT-SK-07 | Marketplace fan-out across registries | | |
| UAT-SK-08 | Integrations picker + provider PUT re-auth gated (403) | | |
| UAT-SK-09 | Composer mic present | | |
| UAT-AUTH-01 | Login with correct password | | |
| UAT-AUTH-02 | Wrong password rejected | | |
| UAT-AUTH-03 | Model-key PUT re-auth gated (DIVERGENCE) | | |
| UAT-AUTH-04 | Profile separate from Settings | | |
| UAT-CC-01 | Zero console errors | | |
| UAT-CC-02 | Zero WS schema-drop events | | |
| UAT-CC-03 | Dark-first UI | | |
| UAT-CC-04 | No emoji in chrome or stored data | | |
| UAT-CC-05 | Non-zero token/cost counter after chat | | |
| UAT-CC-06 | make verify-contracts exits 0 | | |
| UAT-CC-07 | Owner stamped on new task + workspace; never gates | | |
| UAT-CC-08 | Command Center / Monitor / Policies load clean | | |
| UAT-CC-09 | SPA-embed freshness (binary serves rebuilt SPA) | | |
| UAT-NEG-01 | Non-tool model: turns fail gracefully (no hang) | | |
| UAT-NEG-02 | Transcriber-absent: mic degrades (no crash) | | |

**Totals:**  
- Pass: ___  
- Fail: ___  
- Blocked: ___  
- N/A: ___  

**Release decision:** ☐ ACCEPTED (all G-01 through G-28 PASS)  ☐ BLOCKED (one or more hold-out gates failed)

> **Note on UAT-AUTH-03 / UAT-AG-06:** these two assert the spec target (re-auth gate on model-key and performance PUTs), which the **current code does not yet enforce** (only the Integrations PUT is gated — see each scenario's divergence callout). They are expected to **FAIL until the completeness-phase re-auth fix lands**, and are therefore **not** holdout gates; the positive re-auth control is G-21 (UAT-SK-08). The release-gate set is G-01…G-28.

---

## 14. Spec Ambiguities Noticed During UAT Plan Authoring

The following items were found ambiguous or underspecified in the source specs. These are flagged for the implementation team — they do not prevent UAT from being run, but the tester should use judgment in these areas.

| # | Area | Ambiguity | Affected Scenarios |
|---|------|-----------|-------------------|
| 1 | Memory rooms — default room of `remember` | Spec-5 US-1 places private memories in `agents/<id>/.omnipus/` (agent-global) and shared in `workspaces/<workspace_id>/.omnipus/` — but the spec does not fix whether `remember` defaults to private or shared by call context. UAT-MEM-05 now sidesteps this by storing **explicitly** into the shared room and asserting deterministically that the shared memory does not cross into another workspace (`ResolveWorkspaceSharedRoom` is workspace-keyed). | UAT-MEM-05 |
| 2 | Trust-graph UI location | No spec explicitly names the navigation path to a "trust-graph screen" — it is referenced in Spec-3 US-3 AC-3 and FR-6.2 but not placed in the IA. UAT-AG-07 uses a generic "navigate to the trust-graph or delegation visualization screen." The tester should search the Agents configuration for a delegation or trust section. | UAT-AG-07 |
| 3 | Composer mic interaction | Spec-6 US-7 says a composer mic should be present. Spec does not describe whether clicking it goes directly into recording, shows a modal, or starts an OS permission dialog. UAT-SK-09 step 3 is marked conditional ("if a microphone device is available"). | UAT-SK-09 |
| 4 | Max-parallel setting UI path | Spec-3 US-7 says "Settings → Performance" but the exact tab/path name is not locked in the SPA spec. UAT-AG-06 says "Settings → Performance (or search for 'Max parallel agents')". | UAT-AG-06 |
| 9 | Re-auth gate coverage (spec-vs-code divergence) | FR-12.2 requires **all** sensitive PUTs (model key, performance, Integrations) to require the `X-Reauth-Token`. The code currently gates **only** the Integrations provider PUT (`requireReAuth` at `rest_integrations_auth.go:379`); model-key (`rest.go`/`rest_settings.go`) and performance (`rest_performance.go`) are **NOT** gated. UAT-AUTH-03 and UAT-AG-06 are written to the spec target and will FAIL until the completeness-phase fix lands; UAT-SK-08 is the currently-passing positive control. | UAT-AUTH-03, UAT-AG-06, UAT-SK-08 |
| 10 | external-cli wiring state | The external-cli runner is being wired in the completeness phase. While `ResolveDispatch` still returns `ErrExternalCLINotWired`, UAT-EX-01/EX-04 are FAIL (not N/A). The drivers, worktree isolation, and post-hoc consent router are in-tree; the missing pieces are the production dispatch path and the `POST /api/v1/agents/{id}/runner/test` endpoint. | UAT-EX-01, UAT-EX-04 |
| 5 | Orchestrator DAG dispatch observable evidence | Spec-3 FR-6.5 advances DAGs on task completion. UAT-TSK-07 is now pinned to a **deterministic** observation: the exact board log line `rest: board task: completed task advanced dependents` (`pkg/gateway/rest_board.go:646-647`) naming the dependent in `advanced_ids`, **or** a `GET /api/v1/board/tasks/{id}` showing the dependent's `blocked_by` now satisfied — the tester picks one and must observe it (not "logs OR UI" loosely). | UAT-TSK-07 |
| 6 | accept_from UI: present-but-not-surfaced vs hidden | Spec-3 FR-6.2 says `accept_from`+`budget` are "present-but-not-enforced and not surfaced in the trust-graph UI." It is unclear whether the field appears in the agent config form in a disabled/greyed state or is completely hidden. UAT-AG-04 step 3 and UAT-AG-07 step 3 both accept either interpretation. | UAT-AG-04, UAT-AG-07 |
| 7 | Email TLS requirement | Spec-2 FR-2.7 requires TLS (IMAPS/SMTPS or STARTTLS — no plaintext auth) but the UAT environment may use a local dev mailbox (greenmail) that does not enforce TLS. The tester should verify TLS is the default if connecting to a real server. | UAT-CN-04 |
| 8 | skill versioning location | Spec-6 FR-9.2 says versioning uses a `.versions/` snapshot scheme. The exact location relative to the skill file is not fixed. UAT-SK-04 step 5 checks for a "`.versions/` sub-directory or versioning artifact" broadly. | UAT-SK-04 |
