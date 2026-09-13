# Built-in agents and skills — requirements

**Status:** proposed (founder direction 2026-09-13)  
**Branch home:** `docs/internal/design/` on the release lineage (not a spec).  
**Supersedes:** the five-core README story (Mia, Jim, Ava, Ray, Max) and the compiled seed where it disagrees.  
**Related:** vault note `Feature design — Workspace packs`.

This is what the default install **must** be. Packs are extra. Code today does not yet match this document.

---

## 1. Intent

A new user talks to **three colleagues** and one **operator**. Labour is named staff Jim assigns. Hidden engine agents score and correct plans. Nobody else sits in the sidebar.

Success looks like:

- Mia welcomes and acts as personal assistant (files, mail, tasks).
- Jim interviews, plans, and runs work — he does not do the labour.
- Ava creates and edits teammates and skills **completely** (tools, skills, team, delegation graph).
- Admin (system, but chat-able) installs MCP, providers, and channels.
- Planner writes the DAG; Researcher does deep research; General Purpose executes tasks.
- Judge and Plan Supervisor stay hidden.

---

## 2. Roster

### 2.1 Colleagues (chat)

| Agent | Job |
|---|---|
| **Mia** ⭐ | Welcome. Personal assistant: files, library, email, tasks, light look-up, live browser with the user. Heavy / multi-step work → Jim. |
| **Jim** | Orchestrator. Interviews (including planning questions). Runs the **plan engine**. Assigns Planner, Researcher, General Purpose. Does not shell, does not author agents. |
| **Ava** | Team and skill author. Create/edit members: soul, type, **tool policy**, **skill allow-list**, team, **delegation graph**. Does **not** install MCP. Does **not** run plans. |

### 2.2 Operator (system, chat-able)

| Agent | Job |
|---|---|
| **Admin** | Harness: MCP, providers, channels, doctor, usage. Not on a workspace team. Kernel `set_config` / sandbox / tokens stay Settings UI. |

### 2.3 Staff (not chat)

| Agent | Job |
|---|---|
| **Planner** | Turn Jim’s brief into a task DAG. **define-goal on every task.** May ask Jim if a hole blocks the DAG. Does not interview the user. |
| **Researcher** | Deep research only (search, fetch, cite, evidence bundle). No shell. Leaf. |
| **General Purpose** | Default **task runner**: files, shell, office files. Does **not** run the plan engine. Leaf. Self-delegate is allowed (same belt, extra task). |

### 2.4 Engine (hidden)

| Agent | Job |
|---|---|
| **Judge** | Pass / revise / block against criteria. `inspect_session` only (plus structural floor). Skill allow-list **pinned to verify**. |
| **Plan Supervisor** | One `plan_correct` per wake. |

### 2.5 Out of the default box

Ray as a chat Scout. Max. Staff for Mia or Ava. Jim holding bash / `serve_web`. Ava holding plan tools or `add_mcp_server`. Inherit-parent-tools when the target is a **different** seat (self-delegate is not that).

---

## 3. Who talks to whom

```
User → Mia | Jim | Ava | Admin

Mia  → Jim (heavy work) · Ava (new teammate) · Admin (wire the box)
Jim  → Planner · Researcher · General Purpose
Jim  → Ava (handover: missing specialist — user joins)
Jim  → self (fork: same belt, narrower task)
Planner → Researcher (context before the DAG)
Planner → Jim only (questions), never the user
Researcher, General Purpose: leaves
```

Simple work: Jim skips Planner and assigns Researcher or General Purpose directly.

---

## 4. Interview vs plan vs execute

| Step | Who |
|---|---|
| Interview the user | **Jim** (open + planning questions), **Mia** (PA, light), **Ava** (agent/skill checklist), **Admin** (MCP/provider/channel checklist) |
| Write the brief | Jim |
| Decompose to a DAG + per-task goals | Planner |
| Run / stop the plan | Jim (`create_plan`, `execute_plan`, `stop_plan`) |
| Do the task | General Purpose (default) or Researcher |
| Score done | Judge |
| Correct a stalled plan | Plan Supervisor |

---

## 5. Tool requirements

**Floor (every seat):** `Skill` (load), `ToolSearch`.

**Send on channels and email is Ask** for every working agent who has them (Mia, Jim, Ava, Planner, General Purpose). Judge and Plan Supervisor stay mute.

| Group | Mia | Jim | Ava | Admin | Planner | Researcher | GP | Judge | Plan Supervisor |
|---|---|---|---|---|---|---|---|---|---|
| Talk / handoff / ask-user | Allow | Allow | Allow | Allow | send back | send back | send back | — | — |
| Memory | Allow | Allow | Allow | Allow | Allow | Allow | Allow | — | — |
| Files + library read **and write** | Allow | Read | Read | — | Read | Read | Allow | Read | — |
| Email + chat channels | Allow; send Ask | same | same | — | same | same | same | — | — |
| Web search / fetch | Allow | Allow | Allow | — | Allow | Allow | Allow | — | — |
| Browser (with the user) | Allow | Allow | Allow | — | — | — | — | — | — |
| Shell / `serve_web` | — | — | — | — | — | — | Allow | — | — |
| Tasks | Allow | Allow | — | — | Allow | — | list/update/todos | — | — |
| **Plans** | — | **Allow** | **Deny** | — | — | — | **Deny** | — | — |
| Delegate | — | → Planner, Researcher, GP, self | — | — | → Researcher | — | — | — | — |
| Agents + skill **authoring** | find_skills | find/list | **Allow** (delete Ask) | — | — | — | — | — | — |
| MCP / provider / channel / doctor | — | — | **Deny MCP** | **Allow** (remove/disable Ask) | — | — | — | — | — |
| `inspect_session` / `plan_correct` | — | — | — | — | — | — | — | inspect | correct |
| `set_config` (kernel) | — | — | — | — | — | — | — | — | — |

**Ava gaps to close in code:** `create_agent` / `update_agent` must set **tool policy** and **delegation graph**. Today they cannot; the REST UI can set policies only.

**General Purpose** must be **deny-by-default**, not inherit the global ceiling (today’s Worker sparse map is unsafe).

**Calendar** in Omnipus = **scheduled tasks** (`create_task` / `update_task` with start, due, recurrence). No Google/Microsoft write in this requirements set. ICS read is out for now.

---

## 6. Skills

A skill is a playbook loaded with the `Skill` tool. It is **not** a standing rule (those go in the soul) and **not** a tool.

### 6.1 Ship in the default box

| Skill | Who may load it |
|---|---|
| **interview** | Jim, Mia, Ava, Admin — **same skill, four checklists** |
| **handoff** | Mia |
| **orchestrate** | Jim |
| **plan** | Jim, Planner, Plan Supervisor |
| **define-goal** | Planner, Jim, Mia — anyone who **creates** a task or plan |
| **deep-research** | Researcher |
| **agent-authoring** | Ava |
| **skill-authoring** | Ava |
| **tool-mapping** | Ava |
| **skill-mapping** | Ava |
| **delegation-graph** | Ava |
| **workspace-team** | Ava |
| **mcp-install** | Admin |
| **provider-setup** | Admin |
| **channel-setup** | Admin |
| **doctor** | Admin |
| **verify** | Judge only (pin the allow-list) |
| **inbox-triage** | Mia |
| **author-document** | Mia, General Purpose |
| **author-spreadsheet** | Mia, General Purpose |
| **author-presentation** | Mia, General Purpose |
| **author-pdf** | Mia, General Purpose |

There is no native Office tool in the catalogue. The four authoring skills teach producing real files (via workspace files; General Purpose may use shell/Python when present).

### 6.2 Interview checklists

| Who | Kind | Checklist |
|---|---|---|
| **Jim** | Open + **planning** | Outcome; done; in/out; constraints; **dependencies, parallel work, already done, risks, who should research vs make**; context. Then write the brief → Planner. |
| **Mia** | Open, PA-scoped | What you need now; inbox vs files vs reminder vs “this is a project” (→ Jim). Few questions, not a spec grill. |
| **Ava** | Specific | Purpose; Main vs worker vs external CLI; tools allow/ask/deny; skills to grant; who they may call; model slug via `list_models`. |
| **Admin** | Specific | MCP vs provider vs channel; which server/app; where the secret lives; test vs enable; MCP leaves the sandbox. |

### 6.3 Drop / do not seed

`daily-briefing`, `summarize` (external CLI), `browser-with-user` (browser is a tool), `execute-task` (General Purpose soul), `web-research` as a skill (tools; the playbook is **deep-research**), `untrusted-content` (standing soul/tool rule, not a skill).

### 6.4 Already in the binary (keep, retarget)

`plan`, `define-goal`, `skill-authoring`. Retarget `skill-authoring` at `create_skill` / `edit_skill` (not old `system.skill.*` names). Proposal-first for skill writes.

---

## 7. On-the-fly labour (not Ava)

| Claude Code | Omnipus |
|---|---|
| Spawn general-purpose | `delegate` **General Purpose** |
| Spawn a research/plan preset | `delegate` **Researcher** / **Planner** |
| Fork | `delegate` **to self** (same belt) |

Ava is for a **new kind of seat**, not every task. Day-to-day fan-out does not wait on `create_agent`.

---

## 8. Coding delta (harness, not roster)

Not in this roster, but required for a Software pack / serious coding:

- Merge existing **`grep`** (ADR-081, unmerged) — names + content + globs.
- First-party **`lsp` tool** (pure Go client, `go.lsp.dev/jsonrpc2` + `protocol`). Exec **host** language servers (`gopls`, etc.). Do not embed servers; do not use Tree-sitter (wrong layer + CGo).
- Claude Code LSP = plugin maps `.go` → `gopls` **and** the binary on PATH; **session start** discovery (installing `gopls` mid-session is not enough).

Skip: NotebookEdit.

---

## 9. Packs

Default box does **not** include domain packs. See vault **Feature design — Workspace packs**. First-party candidates: Software, Data, Content, Sales, Finance, Legal, Support. Staff + skills + named MCP recipes; no extra chat colleagues.

---

## 10. Open implementation items

1. Reseed `pkg/coreagent` to this roster (retire Ray as chat; add Admin; Researcher in the box; GP deny-by-default).
2. Ava: tool policy + delegation graph on create/edit.
3. Write the new skills; drop summarize and daily-briefing from the seed.
4. Pin Judge to `verify`.
5. Merge grep; design `lsp` separately.
6. Do not compile Salesforce / Zendesk / gopls into the binary.
