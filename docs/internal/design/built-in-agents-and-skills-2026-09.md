# Built-in agents and skills — requirements

**Status:** proposed (founder direction 2026-09-13; isolation rules added 2026-09-14)  
**Branch home:** `docs/internal/design/` on the release lineage (not a spec).  
**Supersedes:** the five-core README story (Mia, Jim, Ava, Ray, Max) and the compiled seed where it disagrees.  
**Related:** vault note `Feature design — Workspace packs`; ADR-082 v10 (worktree isolation, branch `feat/lsp-and-native-worktrees`).

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
- Coding steps that run at the same time on one repository never share a folder: the plan opts them into isolation (§7).
- Judge and Plan Supervisor stay hidden.

---

## 2. Roster

### 2.1 Colleagues (chat)

| Agent | Job |
|---|---|
| **Mia** ⭐ | Welcome. Personal assistant: files, library, email, tasks, light look-up, live browser with the user. Heavy / multi-step work → Jim. |
| **Jim** | Orchestrator. Interviews (including planning and isolation questions). Runs the **plan engine**. Assigns Planner, Researcher, General Purpose. Approves the plan, including which steps run isolated. Does not shell, does not author agents. |
| **Ava** | Team and skill author. Create/edit members: soul, type, **tool policy**, **skill allow-list**, team, **delegation graph**. Does **not** install MCP. Does **not** run plans. |

### 2.2 Operator (system, chat-able)

| Agent | Job |
|---|---|
| **Admin** | Harness: MCP, providers, channels, doctor, usage. Not on a workspace team. Kernel `set_config` / sandbox / tokens stay Settings UI. |

### 2.3 Staff (not chat)

| Agent | Job |
|---|---|
| **Planner** | Turn Jim’s brief into a task DAG. **define-goal on every task.** Declares each step’s files and **opts coding steps into isolation** (§7). May ask Jim if a hole blocks the DAG. Does not interview the user. |
| **Researcher** | Deep research only (search, fetch, cite, evidence bundle). No shell. Leaf. |
| **General Purpose** | Default **task runner**: files, shell, office files. Does **not** run the plan engine. Works inside its step’s isolated copy when the step is isolated; never merges and never chooses isolation. Leaf. Self-delegate is allowed (same belt, extra task). |

### 2.4 Engine (hidden)

| Agent | Job |
|---|---|
| **Judge** | Pass / revise / block against criteria. `inspect_session` only (plus structural floor). Skill allow-list **pinned to verify**. For an isolated step, scores that step’s own commit range. |
| **Plan Supervisor** | One `plan_correct` per wake. Also handles isolation refusals and merge conflicts raised as plan-correction events. |

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
| Declare each step’s files; opt coding steps into isolation | Planner (Jim when he skips Planner) |
| Check the plan (overlaps, isolation rules) | plan-lint, at approve |
| Run / stop the plan | Jim (`create_plan`, `execute_plan`, `stop_plan`) |
| Do the task | General Purpose (default) or Researcher |
| Score done | Judge |
| Merge an isolated step into the repository | Engine (automatic, after the Judge passes) |
| Correct a stalled plan, a refused isolation, or a merge conflict | Plan Supervisor |

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
| **Isolation opt-in** (`project_root`) | — | step + background delegate | — | — | step | — | — | — | via `plan_correct` |
| Delegate | — | → Planner, Researcher, GP, self | — | — | → Researcher | — | — | — | — |
| Agents + skill **authoring** | find_skills | find/list | **Allow** (delete Ask) | — | — | — | — | — | — |
| MCP / provider / channel / doctor | — | — | **Deny MCP** | **Allow** (remove/disable Ask) | — | — | — | — | — |
| `inspect_session` / `plan_correct` | — | — | — | — | — | — | — | inspect | correct |
| `set_config` (kernel) | — | — | — | — | — | — | — | — | — |

**Ava gaps to close in code:** `create_agent` / `update_agent` must set **tool policy** and **delegation graph**. Today they cannot; the REST UI can set policies only.

**General Purpose** must be **deny-by-default**, not inherit the global ceiling (today’s Worker sparse map is unsafe).

**Isolation opt-in** is not a separate tool: it is the `project_root` argument on `create_task` / `update_task` for a plan step, and on a background `delegate` (§7). General Purpose never sets it: an isolated step’s worker cannot start a second isolated helper.

**Calendar** in Omnipus = **scheduled tasks** (`create_task` / `update_task` with start, due, recurrence). No Google/Microsoft write in this requirements set. ICS read is out for now.

---

## 6. Skills

A skill is a playbook loaded with the `Skill` tool. It is **not** a standing rule (those go in the soul) and **not** a tool.

### 6.1 Ship in the default box

| Skill | Who may load it |
|---|---|
| **interview** | Jim, Mia, Ava, Admin — **same skill, four checklists** |
| **handoff** | Mia |
| **orchestrate** | Jim — includes when to isolate background helpers (§7) |
| **plan** | Jim, Planner, Plan Supervisor — includes the isolation rules (§7) |
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
| **Jim** | Open + **planning** | Outcome; done; in/out; constraints; **dependencies, parallel work, already done, risks, who should research vs make**; **which code repository, and which pieces change code at the same time** (feeds isolation, §7); context. Then write the brief → Planner. |
| **Mia** | Open, PA-scoped | What you need now; inbox vs files vs reminder vs “this is a project” (→ Jim). Few questions, not a spec grill. |
| **Ava** | Specific | Purpose; Main vs worker vs external CLI; tools allow/ask/deny; skills to grant; who they may call; model slug via `list_models`. |
| **Admin** | Specific | MCP vs provider vs channel; which server/app; where the secret lives; test vs enable; MCP leaves the sandbox. |

### 6.3 Drop / do not seed

`daily-briefing`, `summarize` (external CLI), `browser-with-user` (browser is a tool), `execute-task` (General Purpose soul), `web-research` as a skill (tools; the playbook is **deep-research**), `untrusted-content` (standing soul/tool rule, not a skill).

### 6.4 Already in the binary (keep, retarget)

`plan`, `define-goal`, `skill-authoring`. Retarget `skill-authoring` at `create_skill` / `edit_skill` (not old `system.skill.*` names). Proposal-first for skill writes. Replace the `plan` skill’s isolation paragraph with §7.4 when the field ships.

---

## 7. Isolation for parallel coding

Source of truth: **ADR-082 v10**, decisions D11 (background delegates), D12 (plan steps) and D13 (delegate contracts). This section is what Jim, Planner, the `plan` and `orchestrate` skills, and the Planner’s and Jim’s instructions must teach.

**What it is.** An isolated step works in its own copy of a git repository, on its own branch. The engine commits the step’s work, the Judge scores that commit, and the engine merges it into the repository **before** any step that depends on it starts. Without isolation, steps running at the same time write into the same folder and overwrite each other.

### 7.1 Who decides

| Role | Job |
|---|---|
| **Planner** (Jim when he skips Planner) | Opts a step in by setting `project_root` on it — the repository folder as seen from `work/`, for example `my-app`. Lists the step’s files in `write_set`, starting at `work/` (for example `my-app/src/api.go`). |
| **plan-lint** (at approve) | Rejects a plan that breaks §7.3. Nothing is inferred: a step is isolated only if the plan says so. |
| **Jim** | Asks the isolation questions in the interview, approves and runs the plan. For chat helpers outside a plan, opts a background `delegate` in with `project_root`. |
| **General Purpose** | Does the step inside its copy. Never merges, never chooses isolation, cannot start another isolated helper. |
| **Judge** | Scores the step’s own commit range, never the shared folder. |
| **Engine** | Makes the copy, commits, merges after the Judge passes, raises a plan-correction event when it cannot. |
| **Plan Supervisor** | Handles a refused isolation (for example the repository has uncommitted changes) or a merge conflict. |

### 7.2 When to opt a step in — both must be true

1. **Coding work:** the step changes files inside a git repository — a clone under `work/` or a mounted clone. Research, notes and documents never need it.
2. **Same time, same repository:** another step that changes the same repository can run at the same time — nothing in `blocked_by` puts one after the other.

Once one step on a repository is isolated, **every** step in the plan that changes that repository must be isolated too (§7.3, rule 2).

### 7.3 Rules plan-lint enforces

| # | Rule | Rejects |
|---|---|---|
| 1 | **Overlap** | Two steps that can run at the same time and list overlapping files — isolated or not. Isolation does not make a real overlap safe: split the files, or order the steps with `blocked_by`. |
| 2 | **One repository, all or nothing** | A repository with steps that can run at the same time, or with any isolated step, where some step changing that repository is not isolated. |
| 3 | **Scope** | An isolated step that lists a file outside its own repository. |
| 4 | **Valid** | A `project_root` that is not a repository top-level inside `work/` or a mount; `work/` itself; a step assigned to an agent that cannot write, or that runs on an external CLI. |

**Blind spot.** A step with no file list and no `project_root` that still edits code is invisible to lint. The Planner must opt such a step in; lint cannot catch the omission.

**What happens at run time** (engine, not agents): a repository with uncommitted changes refuses the step before it starts (no attempt used); the engine retries after one minute, up to three attempts, then parks the step as blocked and tells the Plan Supervisor (founder ruling). A merge conflict does not mark the step done: the step retries from the repository’s new state, and the Plan Supervisor is told. Pressing Stop while a judged step is being merged lets the merge finish first — no judged work is thrown away (founder ruling).

### 7.4 Plan skill text to install

Install in the embedded `plan` skill and the Planner’s instructions **only when the `project_root` field on plan steps ships** — today’s `plan` skill still says exploratory steps are isolated automatically, which was never built.

> **Isolation.** Set `project_root` on a step when it changes code in a git repository and another step on the same repository can run at the same time. Then set it on every step in this plan that changes that repository. List `write_set` paths starting from `work/` (for example `my-app/src/api.go`). Do not isolate research, notes or documents. If two steps must change the same file, order them with `blocked_by` — isolation does not make that safe. A step whose files you cannot predict but which edits code still needs `project_root`.

### 7.5 Orchestrate skill text to install (chat helpers)

> **Parallel helpers.** When you start two or more background helpers that change the same git repository, pass `project_root` on each. Do not pass it to a single helper, a blocking helper, or read-only work. Inside a plan step that is already isolated, do not pass it — the step’s copy is shared by its helpers.

---

## 8. On-the-fly labour (not Ava)

| Claude Code | Omnipus |
|---|---|
| Spawn general-purpose | `delegate` **General Purpose** |
| Spawn a research/plan preset | `delegate` **Researcher** / **Planner** |
| Fork | `delegate` **to self** (same belt) |
| Subagent with `isolation: worktree` | Background `delegate` with `project_root` (chat), or an isolated plan step (§7) |

Ava is for a **new kind of seat**, not every task. Day-to-day fan-out does not wait on `create_agent`.

---

## 9. Coding delta (harness, not roster)

Not in this roster, but required for a Software pack / serious coding:

- Merge existing **`grep`** (ADR-081, unmerged) — names + content + globs.
- First-party **`lsp` tool** (pure Go client, `go.lsp.dev/jsonrpc2` + `protocol`). Exec **host** language servers (`gopls`, etc.). Do not embed servers; do not use Tree-sitter (wrong layer + CGo).
- Claude Code LSP = plugin maps `.go` → `gopls` **and** the binary on PATH; **session start** discovery (installing `gopls` mid-session is not enough).
- Worktree isolation for parallel coding — ADR-082 v10 (§7).

Skip: NotebookEdit.

---

## 10. Packs

Default box does **not** include domain packs. See vault **Feature design — Workspace packs**. First-party candidates: Software, Data, Content, Sales, Finance, Legal, Support. Staff + skills + named MCP recipes; no extra chat colleagues.

---

## 11. Open implementation items

1. Reseed `pkg/coreagent` to this roster (retire Ray as chat; add Admin; Researcher in the box; GP deny-by-default).
2. Ava: tool policy + delegation graph on create/edit.
3. Write the new skills; drop summarize and daily-briefing from the seed.
4. Pin Judge to `verify`.
5. Merge grep; design `lsp` separately.
6. Do not compile Salesforce / Zendesk / gopls into the binary.
7. Implement ADR-082 v10 D12: `project_root` on plan steps, the plan-lint rules in §7.3, merge before dependents.
8. When item 7 ships: install §7.4 in the `plan` skill and the Planner’s instructions, §7.5 in the `orchestrate` skill and Jim’s instructions, and add the isolation question to Jim’s interview checklist. Not before — a skill must never describe a field that does not work.
