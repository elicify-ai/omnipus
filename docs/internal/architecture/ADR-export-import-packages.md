# ADR — Export / Import packages (.omnpkg), epic elicify-ai/omnipus#1094

- **Status:** Proposed (architect draft, 2026-11-19). Waiting on the founder's answers in `QUESTIONS.md`; then `/grill-spec`, then the BDD spec. **Amended 2026-11-19** for S1 after grill round 1: see "Amendments 2026-11-19" at the end (A1–A8; A7–A8 after grill round 2, revised after grill round 3 for spec revision 4, after grill round 4 for spec revision 5 and after grill round 5 for the final spec revision 6).
- **Code base read:** `origin/release/v0.1.1` at `7faff5291` (the integration branch). The `/workspace/omnipus` checkout is on an old `main` from July, so it was **not** used as evidence.
- **Locked rules (from the issue, not reopened here):** secrets never exported; IDs regenerated on import; Replace is the default import mode, with a warning and explicit confirmation; session history only when asked for; import is all-or-nothing.
- **Confidence labels:** ✅ **Verified** (I read the code or ran the command, cited) · 🔶 **Inferred** (follows from code, not tested) · ❓ **Unknown** (has to be found out).

---

## 0. What the code tells us first (facts that change the issue's picture)

| # | Fact | Evidence | Label |
|---|---|---|---|
| F1 | Agent records are **not** in `config.json` any more. Each agent is its own file `entities/agents/<id>.json`; its prompt is `agents/<id>/SOUL.md` (plus an optional `AGENT.md` beside it). | `pkg/agentstore/store.go` (`RecordsDir`), `pkg/agentstore/state.go` (`soulPath`, `CreateState`) | ✅ |
| F2 | An agent record holds an **absolute folder path** (`Home`, JSON name `workspace`). Copying it to another machine would point at a folder that does not exist. | `pkg/config/config.go:629` | ✅ |
| F3 | Agents point at other agents by ID in `subagents.allow_agents`. | `pkg/config/config.go:876` | ✅ |
| F4 | Workspace records are `workspaces/<id>.json`; team members (`core_team`) and per-member settings (`member_configs`) are keyed by agent ID. Delegation edges and mounts were **moved out** to `entities/delegation/<id>.json` and `entities/mounts/<id>.json` on purpose (an agent could otherwise give itself permissions). | `pkg/workspace/workspace.go`, `pkg/workspace/delegationstore.go`, `pkg/workspace/mountstore.go` | ✅ |
| F5 | Memory is plain Markdown files in two "rooms": `agents/<id>/.omnipus/memories/` (private) and `workspaces/<id>/.omnipus/memories/` (shared). Each memory file names its `author` (an agent ID) and `born_in` (a session ID). The search index next to them is rebuilt automatically. | `pkg/memrooms/rooms.go`, `pkg/memrooms/memory_file.go:88-92` | ✅ |
| F6 | Each workspace also keeps one end-of-chat summary file per agent, **named after the agent ID**: `workspaces/<ws>/.omnipus/carry-forward/<agentID>.md`. | `pkg/agent/memory.go:986` | ✅ |
| F7 | A "knowledge base" is a normal folder of notes with a `.omnipus-vault/` marker, living inside the workspace's `work/` folder (or in a folder mounted from the host). `$OMNIPUS_HOME/knowledge/<hash>/` is only a **rebuildable search index**, not the knowledge itself. | `pkg/knowledge/index.go` (`indexHomeSubdir`, "Derived data: safe to delete"), `pkg/knowledge/detect.go:327` (`CreateInWorkspace`) | ✅ |
| F8 | MCP server settings can still hold **plain-text secrets** in `env` and `headers` (old installs, older API paths). Newer entries keep only references (`env_refs`, `header_refs`) to the encrypted store. | `pkg/config/config.go:2421-2470` | ✅ |
| F9 | Skills already have one safe install path with versions and undo: `skills.ApplyChange` with `KindInstall` / `KindInstallReplace` from a staged folder. Built-in skills refuse changes. | `pkg/skills/pipeline.go:23-31, 117`, `pkg/skills/identity.go` (`IsBuiltin`) | ✅ |
| F10 | Built-in ("core") agents have **fixed IDs** (`mia`, `jim`, `omnipus`, `worker`, `planner`, …) and their prompts are compiled into the program. | `pkg/coreagent/core.go:30-93, 176` | ✅ |
| F11 | Session history is spread over several places: `sessions/<id>/` (four meta files plus day files), per-agent session folders, `session_lifecycle/`, `session_messages/`, `tool_results/`. Session files carry `agent_id`, `agent_ids`, `workspace_id`, `parent_session_id`. | `pkg/session/unified_meta_files.go:66-91`, `pkg/gateway/gateway_boot.go:1062,1082`, `pkg/agent/loop_construct.go:47-69` | ✅ |
| F12 | The command line is deliberately a **thin client** of the running gateway; it must not edit the data folder behind the gateway's back (ADR-024 D1/D2). Only setup commands (`onboard`, `start`, `credentials`) work offline. | `docs/internal/architecture/ADR-024-cli-minimization.md` §D1, D2, D5; `pkg/app/app.go:503-512` | ✅ |
| F13 | Agents cannot read or write some top-level folders of the data dir; `backups/` is one of them. A new top-level folder would need to be added to that list. | `pkg/fspolicy/secretset.go` (`SecretEntriesAlways`), `pkg/fspolicy/inventory.go` (`ProtectedInventoryNames`) | ✅ |
| F14 | zstd compression is **already compiled into the binary** (through the Telegram library); it is only marked "indirect" in `go.mod`. | `go list -tags stdjson -deps ./cmd/omnipus` lists `github.com/klauspost/compress/zstd`; `go.sum` has `v1.20.2 h1:` | ✅ |
| F15 | The old Backup/Restore (`tar.gz`, Settings → Data) copies `credentials.json` and `master.key` and works only in local mode. It already has good path-safety checks we can reuse. | `pkg/gateway/rest_settings.go:349-560, 563-660, 886`; routes `pkg/gateway/rest.go:936-940` | ✅ |
| F16 | Imports that destroy data already use a "type your password again" gate. | `pkg/gateway/rest_settings.go` (`HandleRestore` → `requireReAuth`), `pkg/gateway/rest_integrations_auth.go:208` | ✅ |

---

## D1 — Where the engine lives and how it is called

**Problem.** Export and import must be reachable from the command line, the Settings screen, and (later) plugins, without two copies of the logic.

| Option | What it means | For | Against |
|---|---|---|---|
| A. Engine inside `pkg/gateway` handlers | Like today's backup code | Least new code | Not reusable by the CLI or plugins; `rest_settings.go` is already large |
| B. **New package `pkg/omnpkg`, run inside the gateway; CLI and Settings call the gateway's REST API** | Engine is a plain Go library; gateway owns the locks and the live agent list | One engine; live agents appear immediately (store notifier); obeys ADR-024; same path for UI and CLI | Import from CLI needs a running gateway (the CLI already auto-starts one) |
| C. Engine run offline by the CLI with the gateway stopped | Like the old `omnipus restore` idea in #49 | Works without a gateway | Breaks ADR-024 D1; the gateway's in-memory agent list goes stale; two writers on one folder |

**Decision: B.** New Go package `pkg/omnpkg` with four entry points: `Export(ctx, Selection) → file`, `Inspect(file) → Manifest + preview`, `Verify(file) → report`, `Import(ctx, file, Mode, Confirm) → ImportResult`. The engine talks to storage through small interfaces that the gateway fills with the real stores (`agentstore`, `workspace`, `skills.ApplyChange`, MCP config writer), so it never writes around them.

- Name: the issue suggests `pkg/package`, but `package` is a reserved word in Go and cannot be a package name. `pkg/omnpkg` matches the file extension.
- `Inspect` and `Verify` only read the file, so the CLI may run them **offline** (allowed: they touch no data).
- Export and import go through new REST endpoints (contract first, in `contracts/openapi.yaml`, as Hard Constraint 8 requires): `POST /api/v1/packages/export`, `POST /api/v1/packages/import` (upload → returns a preview with a `staging_id`), `POST /api/v1/packages/import/{staging_id}/apply` (mode + confirmation). Two steps give the "warn, then confirm" flow for free, for both Settings and CLI.
- Local mode only for now, exactly like today's backup routes (`pkg/gateway/rest.go:936`). 🔶 Inferred that this is wanted; easy to open later.

```
CONFIDENCE: High (verified from code) — ADR-024 D1/D2 and the store notifier (pkg/agentstore/store.go Notifier) make B the only option that keeps the live agent list correct.
Open: how the CLI's own key (cli.token) passes the "type your password again" gate for import (❓ Unknown — the CLI principal may have no password; to be settled in the spec, see R3).
```

---

## D2 — File format and manifest versioning

**Decision.** `.omnpkg` = a `tar` archive compressed with zstd. First entry is `manifest.json`. Layout:

```
manifest.json                      <- always first
skills/<slug>/SKILL.md ...         <- same shape as a Claude Code plugin
.mcp.json                          <- MCP server definitions, secrets as ${NAME} placeholders
omnipus/agents/<id>/agent.json     <- agent record, cleaned (no Home, no timestamps, no skill stamps)
omnipus/agents/<id>/SOUL.md, AGENT.md
omnipus/agents/<id>/memories/...   <- private memory room
omnipus/workspaces/<id>/workspace.json, delegation.json, AGENT.md
omnipus/workspaces/<id>/memories/..., carry-forward/<agentID>.md
omnipus/workspaces/<id>/work/...   <- work files, includes knowledge-base folders
omnipus/sessions/<id>/...          <- only when session history was opted in
```

IDs inside the package are the **source install's IDs**, untouched. New IDs are made only at import (D5). This keeps a package readable and lets `Inspect` show real names and links.

**Manifest fields:** `format: "omnpkg"`, `format_version` (whole number, starts at 1), `scope` (`workspace` | `system` | `custom`), `elements` (list, each with type, source ID, name, file list), `counts`, `files` (path → SHA-256 and size), `source_build` (the gateway `Version`, `pkg/gateway/rest.go:45`), `created_at`, `includes_sessions` (true/false), `needs_secrets` (list of secret names the importer must re-enter).

**Versioning rule.** A reader refuses a package whose `format_version` is **higher** than it knows ("made by a newer Omnipus — update first"). It accepts the same or lower versions and ignores unknown extra fields. Each element type also carries its own small `schema` number so, for example, the agent record can change without bumping the whole format. The manifest shape is defined once in `contracts/` and the Go type is generated (Hard Constraint 8), because `Inspect` returns it to the Settings screen.

| Option for versioning | Verdict |
|---|---|
| Semantic version string ("1.2.0") | Rejected: needs a comparison rule nobody uses; one number is enough |
| **One whole number + per-element schema numbers** | **Chosen** |
| No version, sniff the content | Rejected: no safe way to refuse a newer package |

**Compression.** zstd from `github.com/klauspost/compress/zstd`: pure Go, already linked into the binary (F14), so binary size does not grow; the only change is moving it from "indirect" to a direct line in `go.mod`. Alternative stdlib `gzip`: no `go.mod` change at all, but the issue fixed `tar.zst`, and zstd packs session-heavy exports much smaller.

**Checksums.** SHA-256 per file in the manifest. Export builds the payload into a staging folder first, computes the hashes, then writes the tar with the manifest first (so `Inspect` reads one small entry and stops). No signing in this epic (proportionate; packages are moved by the owner).

```
CONFIDENCE: format and compression — High (verified: zstd present, F14). Layout — Medium (🔶 inferred; the Claude Code-shaped parts are checked against the plugin docs in slice S10).
```

---

## D3 — Which library for tar and zstd

| Option | Verdict |
|---|---|
| **Go stdlib `archive/tar` + `klauspost/compress/zstd`** | **Chosen.** `archive/tar` is already used by backup (`pkg/gateway/rest_settings.go`); zstd is already linked (F14). |
| `mholt/archiver` or similar | Rejected: new dependency for no gain. |
| Shelling out to `tar`/`zstd` | Rejected: Hard Constraint 2 (pure Go, no shelling out). |

Reuse, by moving into `pkg/omnpkg`: the path checks of `extractTarGz` (no absolute paths, no `..`, no writing through symlinks, per-file size cap) and the "never carry" list of `isProtectedRestoreNamespace` (`pkg/gateway/rest_settings.go:886`). Add a total-size cap and a file-count cap for uploads (decompression-bomb guard). ✅ Verified the existing checks; caps are 🔶 inferred values to set in the spec.

---

## D4 — How import is made all-or-nothing ("transactional")

The gateway is running and other code reads the data folder all the time, so we cannot swap the whole folder in one move. The trick that makes this simple: **because every imported item gets a brand-new ID (locked rule), importing never overwrites anything until the very last step.**

```
 upload ──► 1. STAGE    unpack into $OMNIPUS_HOME/backups/.staging/<staging_id>/
                        check: manifest version, every SHA-256, paths safe, size caps,
                        no secrets present (scanner), every reference resolvable
            2. PREVIEW  return what will be added, what collides by name, which
                        secrets must be re-entered  ──► person confirms (Replace warning)
            3. SNAPSHOT if Replace: export the items about to be replaced into
                        backups/packages/pre-import-<time>.omnpkg   (reuses Export)
            4. ADD      create everything new with fresh IDs, in dependency order:
                        skills → MCP servers → agents → workspaces → delegation →
                        memory → work files → sessions.  Every created ID is written
                        to a journal file in the staging folder.
            5. SWITCH   (Replace only) retire the old same-named items
            6. CLEAN    delete staging folder, write audit entry
 failure in 3 or 4 ──► delete every ID listed in the journal (they are all new),
                       nothing old was touched, report the error.
 failure in 5      ──► re-import the step-3 snapshot (same engine), report.
 gateway crash     ──► on next start, a leftover journal is rolled back (step-4 rule).
```

- Staging and snapshots live under `backups/`, which agents already cannot touch (F13). No security-policy change needed. (Alternative: a new top-level `packages/` folder — clearer name, but needs edits in `pkg/fspolicy` and its drift test.) ✅ Verified the protected list.
- Writes go through the existing stores, never raw file copies, so the live agent list updates and existing checks run: `agentstore.Store.CreateState` / `DeleteState` (`pkg/agentstore/state.go:413, 487`), the tool-policy coverage check `withToolPolicyCoverageGuard` (`pkg/gateway/rest_agents.go:977`), `workspace.SaveRecord` and `workspace.SaveDelegation`, `skills.ApplyChange`. The agent-create validation now lives inside the HTTP handler (`pkg/gateway/rest_agents_create.go`, `validateAndBuildConfig`, `persistAgent`); slice S1 pulls the non-HTTP part out into a function both can call. 🔶 Inferred size of that refactor: moderate.
- One import at a time (a single import lock).

| Option | Verdict |
|---|---|
| **Stage → add with new IDs → switch → journal rollback** | **Chosen**: simple because new IDs never collide. |
| Copy whole data folder, import, swap folders | Rejected: needs the gateway stopped (breaks D1), doubles disk use. |
| Write everything, hope, no rollback | Rejected: issue requires all-or-nothing. |

```
CONFIDENCE: Medium-High (🔶 inferred). The logic is sound; the unknown is how many side effects agent delete has (mailboxes, sessions — pkg/gateway/rest_agents.go:1233 deleteAgent) that rollback must reuse rather than copy.
```

---

## D5 — How new IDs are given out and every reference is fixed

**Rule.** At import, build one table `old ID → new ID` for every agent, workspace (and session, if included) in the package. Then rewrite references **field by field using the real Go types**, never by find-and-replace in text (a prompt that mentions an ID by accident must not change).

| Where an ID appears | Field / place | Action | Evidence |
|---|---|---|---|
| Agent record | `id` | new UUID (same generator as create, `rest_agents_create.go:645`) | ✅ |
| Agent record | `workspace` (Home path) | **dropped**; the default per-agent folder is used | ✅ F2 |
| Agent record | `subagents.allow_agents[]` | remapped; IDs not in the package and not core agents are dropped, listed in the result | ✅ F3 |
| Agent record | `skill_grants` | dropped; rebuilt by the normal save path from `skills` names (`pkg/agentstore/skill_grants.go`) | ✅ |
| Agent record | `tools.mcp.servers[].id` | kept (MCP servers are named, not ID'd); renamed only if a name collides in New mode | ✅ |
| Agent folder | `agents/<id>/` | written at the new ID's folder | ✅ |
| Workspace record | `id`, `core_team[]`, `member_configs` keys | remapped | ✅ F4 |
| Workspace record | `is_default`, `owner`, `setup_pending` | `is_default` forced false (never steal the default); `owner` = importing user | 🔶 |
| Delegation | `from_agent`, `to_agent` | remapped; any edge with an unknown end is dropped (delegation is deny-by-default, so dropping is the safe direction); kept edges re-checked with `DelegationEdge.Validate` | ✅ `pkg/workspace/delegation.go:83` |
| Mounts | `entities/mounts/<id>.json` | **not exported** — they are host paths and write permissions; see Q4 | ✅ F4 |
| Memory file | `author` | remapped; unknown → kept as text "imported" | ✅ F5 |
| Memory file | `born_in` | remapped if sessions imported, else cleared | ✅ F5 |
| Carry-forward | file name `<agentID>.md` | renamed to the new agent ID | ✅ F6 |
| Session meta | `id`, `agent_id`, `agent_ids[]`, `workspace_id`, `parent_session_id` | remapped; session folder renamed | ✅ F11 |
| Core agents | `mia`, `jim`, … | **not** given new IDs (fixed IDs, compiled prompts); see Q1 | ✅ F10 |
| Not covered | channel routing bindings, tasks, goals, plans, rooms | not part of a package in this epic; imported agents start with no channel bindings | ✅ (not in the six element types) |

A single `remap` component in `pkg/omnpkg` owns this table and is tested with a property test: "after import, every reference in the imported set points at an imported or core ID, and no imported ID equals any ID that existed before".

```
CONFIDENCE: High for the field list (verified from the types above). Medium for sessions (several stores, F11 — the session slice will re-survey before building).
```

---

## D6 — Secrets never leave

| Item | Treatment | Label |
|---|---|---|
| `credentials.json`, `master.key`, `cli.token`, `config.json` | never read by export | ✅ |
| MCP `env_refs`, `header_refs` | exported as **names only**: `.mcp.json` gets `${NAME}` placeholders; manifest `needs_secrets` lists them | ✅ F8 |
| MCP literal `env`, `headers` (old plain-text values) | **values removed**, names kept as placeholders, listed in `needs_secrets` | ✅ F8 |
| MCP `env_file` | path dropped (it is a host path that may hold secrets) | 🔶 |
| Agent model/provider | provider **name** kept, no keys (keys are in the credential store, never in the record) | ✅ |
| Free text (memories, SOUL.md, work files, sessions) | cannot be proven secret-free; export runs the existing sensitive-value redactor if it can be reused, and the export screen says plainly "files and chats are copied as they are" | ❓ Unknown whether the boot-time sensitive-value register can be reused offline |

On import, an MCP server that still needs secrets is added **switched off** with a "needs secret" note, and the import result lists what to enter (Settings → Credential Vault).

---

## D7 — Import modes

| Mode | Meaning (proposal) |
|---|---|
| **Replace** (default) | For each imported item whose **name** matches an existing item of the same type: take a pre-import snapshot of the old one, add the new one with a new ID, then remove the old one. Needs the warning screen and explicit "Replace" confirmation (and the password gate, like Restore). Exact fate of the old item: Q2. |
| **Merge** | Add only items whose name does not exist yet; skip the rest; never remove anything. |
| **New** | Add everything; on a name clash, add a suffix ("Writer (imported)"). Never removes anything. Used for the first demo. |

Names, not IDs, decide a clash, because IDs are always new. Skills clash by slug (`skills.KindInstallReplace` with its own undo history handles Replace for skills). ✅ for the skill path (F9); 🔶 for the rest.

---

## D8 — Claude Code plugin interop: how far in this epic

| Option | Verdict |
|---|---|
| Full both ways now (agents, skills, hooks, MCP, commands) | Rejected for now: hooks and commands have no Omnipus runtime yet (the issue puts plugins out of scope). |
| **Plugin-compatible layout from day one; import of a plugin's `skills/`, `agents/*.md` and `.mcp.json` as a late slice; plugin-shaped export as an option in the same slice** | **Recommended** (founder confirms in Q5) |
| Nothing now | Rejected: layout changes later would break old packages. |

Mapping (🔶 inferred from the published Claude Code plugin layout; to be re-checked against current docs with context7 at slice S10):

```
Claude Code plugin                Omnipus
.claude-plugin/plugin.json   ─►   manifest.json name/version (both written on plugin export)
skills/<name>/SKILL.md       ─►   skill (same SKILL.md format; Omnipus already reads .claude/skills)
agents/<name>.md             ─►   custom agent: front-matter name/description → record,
                                  body → SOUL.md, model/tools mapped where possible
.mcp.json                    ─►   MCP servers (${VAR} placeholders → needs_secrets)
hooks/, commands/            ─►   skipped with a notice (no runtime yet)
```

---

## D9 — What "Whole system" means

Whole system = every custom agent, every workspace, every registry skill, every MCP server definition, their memory and work files, plus sessions if opted in. It is **not** a disaster-recovery backup: no credentials, channels, provider settings, tasks, goals or plans. That is why Q3 asks whether the old Backup/Restore stays. ✅ follows from the locked "secrets never exported" rule.

---

## Risks

| # | Risk | Mitigation |
|---|---|---|
| R1 | Agent-create rules live inside an HTTP handler; copying them would drift | S1 extracts them into one shared function (no duplicate) |
| R2 | Agent delete has many side effects; rollback must not leave orphans | Rollback calls the same delete path the REST API uses |
| R3 | CLI key may not pass the password gate for import | Spec decides: terminal password prompt, or CLI key counts as owner on a local install; security-lead reviews (proportionate) |
| R4 | Very large exports (work files, sessions) | Streaming tar writer, size shown in preview, caps on upload |
| R5 | Free text can contain secrets the person pasted | Plain warning on the export screen; sessions off by default (locked) |
| R6 | Mounted knowledge bases live outside the data folder | Q4 |

---

## Amendments 2026-11-19 (after grill round 1 of the S1 spec; team-lead rulings, no founder decision needed)

These notes narrow or correct the decisions above for S1. The S1 spec (`SPEC-S1.md`, now revision 6, final) holds the detail; where this ADR and the spec differ, these amendments apply. Each amendment row names the review round and revision it came from; earlier wording is kept as history only where a row says so.

| # | Decision touched | Amendment |
|---|---|---|
| A1 | CLI Minimization (`docs/internal/architecture/ADR-024-cli-minimization.md`: the CLI is "a thin one-shot task-runner over the engine") | **Dated amendment, 2026-11-19:** besides chat and `records`, the CLI may run the owner commands `export`, `import` and `package inspect`/`package verify`. `export` and `import` are thin gateway clients: they call the REST endpoints with the CLI token and never read or write the data folder themselves. `package inspect` and `package verify` read only the package file the owner names, need no gateway and write nothing. No other engine logic moves into the CLI. Precedent: the `records` owner command. |
| A2 | D4 (all-or-nothing) | S1's automatic rollback covers failures the **running** gateway handles, up to a commit point (after the workspace membership and self-delegation row are written). Live publish, `agent_created`, main chat and audit come after the commit point; a failure there is a warning, never "nothing imported". Rollback promises the **logical** state (the set of agents, the pre-existing agents' files, every workspace's team and delegation edges, `config.json` byte-identical), not a byte-identical data folder: a skill generation stamp, an entity lock file, the workspace `updated_at` and the logs may differ. If the gateway stops during an apply, S1 keeps the journal and warns on the next import; **automatic start-up recovery is S6**. Import never writes `config.json` (the coverage check runs under the config lock without the config writer). R2 changes accordingly: rollback uses a short list of compensations, not the broad agent-delete cascade. |
| A3 | D5 (new IDs, remapping) | `subagents.allow_agents` is **not remapped**. It was retired by "Remove the Global Per-Agent Delegation Policy" (`docs/internal/architecture/ADR-037-remove-global-delegation-policy.md`) and has no effect, so export removes it. `subagents.model` is unused and is also removed. Connector (MCP) assignments are always dropped in S1 and listed, until S3 brings connector definitions ("Built-in agent configuration, skills, and visual file reading", ADR-090, requires explicit assignment). Import is a normal create, so the create rules apply (for example the prompt is stored trimmed). The S1 spec's "Field mapping" table records what is kept, changed or left out. |
| A4 | D6 (secrets) | Besides `env_overrides`, an external worker's `cli_args` and `cli_path` are also removed at export. The promise covers **structured** sources (credential files, keys, tokens, `config.json`, the listed runtime fields). Free text (`SOUL.md`, `AGENT.md`) is copied as it is and is **not** redacted; the CLI shows a notice on every export. A redactor is deferred. |
| A5 | R3 (CLI and password gate) | Settled: New-mode import needs no password re-entry. It only adds items, agent create needs none today, and the CLI identity has no password. The re-entry step comes with Replace in S6. No separate security sign-off for S1. |
| A6 | D3 (reused checks) | Only the yes/no path and entry-type checks move from restore into `pkg/omnpkg`; restore keeps **skipping** such entries, while package upload **refuses** them. zstd decoding is bounded (8 MiB window, 256 MiB memory, one goroutine) for upload, verify and inspect. |
| A7 | D4, A2 (after grill round 2 of the S1 spec, revision 3; revised after grill rounds 3, 4 and 5, revision 6) | **Publication and rollback use the existing full reload.** The live agent list changes only by a reload from the store (the store notifier is not wired in production). Import publishes with that reload after the commit point; `agent_created` and the imported Main's chat follow only when that reload is **confirmed**, otherwise the answer says `activation: "pending"` (an unconfirmed reload) and tells the owner to restart the gateway or check the agent list. The rollback ends with the same reload; it reports confirmed, error or timeout, and only a confirmed reload guarantees the live list no longer shows the agent. **No isolation is promised, and rollback is defined by the places and records it removes, not by who wrote them**: the agent record, its home folder `agents/<id>/` with everything inside it (including content other requests saved there while the agent was visible, for example a private memory note), its team entry and every delegation edge involving it in the import's workspace (only that workspace). Outside that boundary, what other requests created stays: ordinary sessions and chats in the shared session store (with their messages), a main chat, membership or edges for the agent in other workspaces, task or audit entries. A turn already running is not stopped by the reload and can recreate home content after the folder was deleted; the documented final clean-up (stop the gateway, delete the folder, start, repair other workspaces with the existing revision-checked workspace update, then delete every session for the ID found over all pages with other session activity paused and confirmed by a fresh verification scan; without a stable session list the clean-up is reported unverified) removes it. The final clean-up covers these named places only; it is not an all-data guarantee. A protected main chat cannot be deleted and is not removed by normal retention, so in S1 it can remain indefinitely. S6 brings the automatic clean-up. No new lock, notifier or transaction is added in S1. Compensation order: membership and edges (C1), then record (C2), then folder (C3), then reload (C4). A failed C1, including a partial C1 after the membership was removed, stops C2 and C3 so the record stays as the recovery handle; a partial C2 still goes on to C3. Manual recovery is documented in S1a: Delete agent (which also cleans team entries, edges and the agent's own sessions in every workspace), delete a leftover folder with the gateway stopped, remove the agent from other workspaces with the existing workspace update when the record is already gone, delete remaining sessions through the sessions API under a stable list with a verification scan, delete the staging folder to free the upload slot. The import creates the main chat only for the imported Main agent; native workers are reached by delegation as today. **Founder acceptance of this narrower guarantee is still pending** (with the chief). |
| A8 | R1 (shared create rules; revised after grill round 3) | REST create's behaviour is **unchanged** by S1, including that it accepts `voice` without storing it today. Import stores `voice`, `memory_enabled` and `context_window_override` with the `create_agent` tool's existing rules: first its per-runtime check (`agentmutation.ValidateFields`, which refuses `memory_enabled` and `context_window_override` for external workers, relevant from S1b), then the shared applier (`applySharedConfigArgs`). A field-coverage test ties the import field list to the REST and agent-tool field lists; control and alias names (`revision`, `workspace_id`, `env_overrides_remove`, `id`, `soul`, `agent_md`, `agent_type`, `tools_cfg`, `executor`, `model`, `provider` and similar) each have a written decision, with no unlisted ignore set. Fixing REST create's `voice` is a separate issue. |

When these drafts are promoted into the repository, A1 is carried into the canonical "CLI Minimization" ADR (`docs/internal/architecture/ADR-024-cli-minimization.md`) as a dated amendment, so the canonical decision is not left stale.
