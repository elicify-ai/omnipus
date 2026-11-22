# ADR — Export & Import, end to end (bundles, wizards, sidebar page), epic elicify-ai/omnipus#1094

- **Status:** Proposed, **revision 3 — final ADR round** (architect, 2026-11-21). Revision 2 was BLOCKed by grill round 2 (`/workspace/logs/1094/adr-export-import-e2e-review.md`). This revision follows the chief's (team-lead's) ruling of 2026-11-21: **no atomic all-or-nothing runtime visibility guarantee for multi-item imports**; replace it with an honest weaker guarantee plus automatic undo. Per the repo's review-cap rule there is no third ADR grill; remaining items are checked by each slice's spec grill.
- **Base:** extends `docs/internal/architecture/ADR-export-import-packages.md` (D1–D9, A1–A11). Nothing there is reopened except where a 21 Nov founder decision or the chief's ruling changes it (§0.2).
- **Deciders:** founder (product questions §8), chief/team-lead and architect (technical).
- **Owner:** Team A, end to end (backend, REST, UI, tests, docs).
- **Code base read:** `origin/release/v0.1.1` at `616261783`, read-only (`git archive` / `git show`); the `/workspace/omnipus` checkout is on an old `main` and was not used as evidence.
- **Labels:** `[FACT]` read in code/docs (not executed) · `[INFERRED]` follows from facts · `[UNKNOWN]` · `[REQ]` supplied requirement · **`[PROVE IN Bn]`** a decision that depends on code behaviour not yet verified; that slice's spec must prove it (or change the decision) before implementation. Confidence High/Medium/Low per decision.

---

## Review response — round 2 (revision 2 → 3)

| Finding | Resolution | Where |
|---|---|---|
| R2-CRIT-001 `reloadMu` is no runtime barrier; direct readers (delegation, skills, config, MCP) see disk immediately | **Barrier claim withdrawn** (chief's ruling). E10 corrected. New guarantee: items written invisible where a mechanism exists, then **published one by one** in a fixed safe order; any failure → **automatic undo** from the private undo record; preview and result screens state that a partly imported set may be visible and usable while the import runs. Effects of work done with briefly visible items are not undone (stated). Quarantine removed. | §1 FR-5a, D-E5.1–5.2 |
| R2-CRIT-002 Replace / undo race ordinary writers | **Per-entity rule:** each write takes that entity's **ordinary** lock (the one ordinary writers use), re-verifies a complete fingerprint, captures the before-image, writes, releases. Changed → `changed_since_preview`, the import stops and already-applied items are undone. Fingerprints cover every stored file the write touches (workspace = state **and** instructions). Files written without an entity lock: limit stated. | D-E5.3 |
| R2-MAJ-001 nested `LockID` self-deadlock | **Never hold two entity locks at once.** Lock order documented: import lock (outer) → at most one entity's ordinary lock (or one existing combined helper whose order is already established). No multi-workspace locking at all. | D-E5.3 |
| R2-MAJ-002 URL-path credentials | URL path travels but is **disclosed for every connector with a URL path**, not only args. Token-looking segments still replaced. Guarantee list in FR-4 no longer includes URL path. Warnings and manifest never carry removed values. | D-E4 |
| R2-MAJ-003 recovery and result lifecycle | New **operation record** `backups/.imports/<import_id>/` with a written state machine; result and journal survive staging cleanup; no "retry forward"; recovery = finish the automatic undo (same journal, idempotent, also at boot); only actions that exist in a slice are shown. Person-initiated Undo arrives in B8. | D-E3.3, D-E5.4 |
| R2-MAJ-004 selective undo orphans owned parts | Undo unit = **top-level item with all its owned parts**; keeping a child while undoing its owner is impossible by construction; the preview lists everything that goes, including parts edited since the import; optional-dependency effects shown. | D-E5.5 |
| R2-MIN-001 per-type grammar | Workspace names ≤ 200 bytes; skill slugs case preserved, clash case-insensitive; truncation without a hyphen defined; never empty. | D-E5.6 |
| R2-MIN-002 export resumption | `POST export/plan` returns a short-lived `plan_id`; URL keeps `scope`, `preselect`, `plan_id`; Review is shown only for a plan the server still holds; else back to Choose with a notice. | D-E6 |
| FACT corrections | E10 rewritten (reload does not take `reloadMu`; direct readers exist). D-E3.2: `ReadState` already reads record + SOUL.md + AGENT.md, no extension needed. | §0.1, D-E3.2 |
| Chief item (5): B1 independent | B1 uses only today's one-agent engine and S1a's existing all-or-nothing import; every B2–B9 decision resting on unverified behaviour is tagged `[PROVE IN Bn]`. | D-E7, §2 |

### Review response — round 1 (kept for history)

| Finding | Resolution (revision 2, adjusted in 3) |
|---|---|
| CRIT-001 sanitised safety bundle | Private exact undo record (D-E5.4) |
| CRIT-002 several renames not atomic | Rev 2 barrier withdrawn in rev 3; honest guarantee + automatic undo (D-E5.1) |
| MAJ-001 name targets / binding | zero/one/many rules + `plan_digest` + per-entity re-verify (D-E5.3, D-E5.6) |
| MAJ-002 connector carriers | field table (D-E4) |
| MAJ-003 extraction aliases | canonical paths, `os.Root`, no-write verify (D-E3.4) |
| MAJ-004 staging namespace | disjoint folders (D-E3.3) |
| MAJ-005 skill suffix | per-type allocator (D-E5.6) |
| MAJ-006 snapshot claim | per-element coherence only (D-E3.2) |
| MAJ-007 selection | typed refs, exclusions, ownership (D-E2) |
| MAJ-008 slice prerequisites | B1 owns plan endpoint + free-text notice (D-E7) |
| MIN-001 wizard resume | D-E6 |
| OBS-001 jobs | one execution path (D-E3.1) |

---

## 0. Starting point

### 0.1 What exists today

| # | Fact | Evidence | Label |
|---|---|---|---|
| E1 | Engine `pkg/omnpkg`: manifest v1, per-element `Schema`, tar+zstd bounded decoding, SHA-256 per file, `Inspect`, `Verify`, staging registry, journal, rollback, start-up recovery. Built and tested in S1a; no CI receipt attached here. | `format.go`, `reader.go`, `staging.go`, `journal.go`, `recovery.go` | [FACT] |
| E2 | Only element `agent`; export exactly one; reader refuses 0 or >1 agents; unknown types skipped after universal checks. | `export.go:60`, `manifest_checks.go:16-47`, `elements.go` | [FACT] |
| E3 | Reader keeps the payload in memory (256 MiB decoded, 10,000 entries, 1 MiB per agent file); export builds in a `bytes.Buffer`; upload limit 64 MiB; `Verify` calls `ReadPackage`. | `reader.go:161-209`, `format.go`, `rest_packages_export.go:33-70`, `rest_packages.go:38` | [FACT] |
| E4 | Routes `POST /api/v1/packages/export`, `POST …/import` (preview + `staging_id`, 30 min TTL, 5 slots), `POST …/import/{id}/apply` (`mode: "new"` only). Local auth mode only, authenticated, bypass refused. | `rest_packages.go:196-201`, `rest.go:937-940` | [FACT] |
| E5 | Export removes fields by deny list. | `export.go:40-52` | [FACT] |
| E6 | Preview warnings and `changed_since_preview` are warnings only. | `rest_packages_import.go:403-490`, `rest_packages_apply.go:242-263` | [FACT] |
| E7 | SPA: file routes `src/routes/_app/`; sidebar `ASSET_ITEMS` (`Sidebar.tsx:58-61`); explicit `validateSearch` URL state (`library.tsx`); Team is a panel of the workspace chat (`workspaces.$workspaceId.team.tsx:14-24`); agents edited in `AgentProfile.tsx`. | files named | [FACT] |
| E8 | No bundle UI; generated types exist; Backup & Restore removed (#1919). | `openapi-types.ts:1576-1616`, `DataSection.backup-removal-1919.test.tsx` | [FACT] |
| E9 | No generic stepper; `dialog`, `sheet`, `checkbox`, `radio-group`, `progress`, `table`, `callout`, `page-header` exist. | `src/components/ui/` | [FACT] |
| E10 | **Corrected.** The live agent list is rebuilt by reload, but reload execution does **not** hold `reloadMu` (it only guards health fields; admission uses `beginReload`/`finishReload`). Several runtime paths read disk directly between reloads: delegation checks (`loop_delegation.go::findDelegationEdge` → `workspace.ReadDelegation`), mounted skill content (`loop_wire.go::registerSkillTool`), live config refresh (`rest_config.go::applyWrittenConfigLocked`), MCP reconciliation (`loop_mcp.go::ReconcileMCP`). **There is no runtime visibility barrier to build on.** | grill round 2, R2-CRIT-001 | [FACT] |
| E11 | Start-up recovery runs before the roster loads and never fails boot. | `rest_packages_recovery.go:29-69` | [FACT] |
| E12 | Skill loader and identity scan skip `.`-prefixed folders. Skill replace moves the incumbent aside, then publishes the new folder (two steps). | `skills/loader.go:183`, `skills/identity.go:193`, `skills/pipeline_commit.go::publishFolderAndCommit` | [FACT] |
| E13 | Agent revision = 64-hex SHA-256 with conflict error, checked inside `MutateStateWithAgentMD`. Workspace has `RevisionForState` (record + delegation edges) and **separately** `RevisionForInstructions`. | `agentstore/state.go:20-40`, `workspace/state.go:46`, `workspace/instructions.go:113` | [FACT] |
| E14 | `workspace.LockID` is a 64-shard striped pool; nested acquisition is prohibited, even for different IDs. | `workspace/lock.go`, `task/lock.go::StripedLock.Get` | [FACT] |
| E15 | MCP servers live in `config.json` (`MCPServerConfig`: `enabled`, `deferred`, `command`, `args`, `env`, `env_refs`, `env_file`, `type`, `url`, `headers`, `header_refs`). | `config/config.go:2421-2469` | [FACT] |
| E16 | Go 1.27.x (`os.Root` available). Session cookie authenticates bearer routes. | `go.mod`; `gateway/auth.go:179-189` | [FACT] |
| E17 | Staging lifecycle: `BeginApply` accepts only unexpired `ready`; `NeedsCleanup` keeps the entry; `FinishApply` deletes the folder. | `omnpkg/staging.go` | [FACT] |
| E18 | `agentstore.ReadState` already reads record, SOUL.md and AGENT.md as one boundary. | `agentstore/state.go:122` (`readStateFiles`) | [FACT] |

### 0.2 Decisions that change the base `[REQ]`

**Provenance:** founder decisions of 21 Nov 2026 relayed by the chief in this ADR's dispatch; the chief's ruling on the transaction guarantee relayed on 21 Nov 2026. Team-lead attaches the founder's Discord receipt before `/plan-spec` (warning only).

| # | Base said | Now |
|---|---|---|
| C1 | Replace default (D7) | **Add as new** default; Replace and Merge later with safety copy first |
| C2 | UI in Settings → Data | **Own sidebar entry "Export & Import"** |
| C3 | "Package"; workspace/system/custom | User word **"bundle"** (`.omnpkg`); **Everything / One workspace / Chosen agents / Pick items** |
| C4 | UI last | UI early; **Quick Export** on agent, workspace, skill |
| C5 | Backup/Restore question | Closed: removed (#1919) |
| C6 | All-or-nothing import (locked rule, A9) | **Kept for single-agent imports (S1a mechanism, B1).** For multi-item imports (B2+): per-item publication with automatic undo and a plain on-screen statement (chief's ruling). |

---

## 1. Requirements (condensed)

| ID | Requirement | Label |
|---|---|---|
| FR-1 | Sidebar entry "Export & Import" → page with Export and Import wizards | [REQ] |
| FR-2 | Scopes Everything · One workspace · Chosen agents · Pick items (Agents, Skills, Connectors, Memory, Knowledge, Chat history — off unless ticked) | [REQ] |
| FR-3 | Review step from the server: contents, size, what is left out; then download | [REQ] |
| FR-4 | **Guaranteed never to travel:** credential store, keys, tokens, `auth.json`, `config.json` as a file, channel tokens, provider keys, executor `cli_path`/`cli_args`/`env_overrides`, connector env/header values and refs, `env_file`, URL user-info, URL query values. **Copied as written, disclosed on screen:** free text (prompts, instructions, memory, files, chats), connector URL path, command and arguments (pattern-checked, not guaranteed). | [REQ] narrowed per A4 |
| FR-5 | Import: file → preview (added / already exists / warnings) → mode (Add as new default; Replace, Merge later) → confirm → result | [REQ] |
| FR-5a | **Multi-item import guarantee (C6):** either every item is imported, or automatic undo removes what was applied and the result says so. While it runs, a person (and running agents) may briefly see and use part of the set. No cross-item snapshot. Both preview and result say this in plain words. | chief's ruling |
| FR-6 | Quick Export on agent (Team panel), workspace, skill → same wizard, item ticked | [REQ] |
| NFR-1 | Bundles can exceed today's caps; real sizes unknown | [INFERRED] |
| NFR-2 | Crash during an import: the next start finishes the automatic undo before agents load (power-cut limits of base A9 N2 stay) | [INFERRED] |
| NFR-3 | Old bundles import on newer installs; newer bundles refused clearly | [FACT] D2 |

---

## 2. Decisions

### D-E1 — Bundle format v2 (B2)

1. Writers emit `format_version: 2` from B2; readers accept 1 and 2; an S1 install refuses v2 with `package_too_new`.
2. v2 manifest adds: `scope` (`everything`|`workspace`|`agents`|`pick`; v1 `custom` read as `agents`); `selection` (roots, exclude); per element `ref`, `owner`, `requires[]`, `bytes`; `left_out[]` `{ref|name, type, reason}` with reasons `builtin_agent`, `secret_value`, `host_path`, `mount_not_chosen`, `chat_history_not_chosen`, `excluded_by_user`, `type_not_exportable`, `query_removed`, `arg_removed`, `path_segment_removed`. **No removed value is ever written into the manifest or a warning**; only field names and connector names.
3. Element types with own `schema`: `agent`, `skill`, `connector`, `agent_memory`, `workspace`, `workspace_memory`, `work_files`, `knowledge`, `mounted_folder`, `session`.
4. Golden fixtures: v1 single-agent bundle plus one v2 fixture per type import forever.

```
CONFIDENCE: High (versioning mechanism exists).
```

### D-E2 — Selection, ownership, closure (B2; full UI in B7)

```
Selection { scope; roots:[ElementRef]; exclude:[ElementRef]  /* always wins */;
            types:[agent|skill|connector|memory|knowledge|files|chat_history];
            mounts:[ElementRef] /* A11, default none */ }
ElementRef { type, id, scope_id? }
```

| Edge kind | Edges | Excluding it |
|---|---|---|
| **Owned part** | agent → private memory, SOUL/AGENT.md; workspace → record, instructions, delegation, shared memory, carry-forward, work files, knowledge folders; mounted folder → its mount-scoped skills | Part left out. A part is **never a root**: Memory/Knowledge/Files apply to chosen owners; UI disables them until an owner is chosen; API refuses `owner_missing`. |
| **Optional dependency** | agent → non-built-in registry skills, bound connectors; workspace → custom team members | Left out (`excluded_by_user`); import preview warns. A workspace without an excluded member imports without it and without edges to it. |

One resolver (`pkg/omnpkg/selection.go`) for plan, export and CLI. Built-ins (agents and skills) never travel (A10). Mount-scoped skills travel only inside a chosen mounted folder and keep that scope; same slug from two mounts = two refs. Knowledge = vault folders in `work/`, not listed twice when Files is ticked. Unknown owned-part type on the reader → bundle refused; unknown optional type → skipped with warning.

```
CONFIDENCE: High for edges; Medium for Files/Knowledge wording. Mount-scoped skill handling [PROVE IN B6].
```

### D-E3 — REST, capture, lifecycle, extraction

#### D-E3.1 Endpoints (`/api/v1/packages/*` kept; UI says "bundle")

Every route — including status, file, cancel, result, undo — local mode only, `withAuth`, `RequireNotBypass`, with negative auth tests. Contract first (`contracts/openapi.yaml`, generated types).

| Method & path | Purpose | Slice |
|---|---|---|
| `POST /packages/export/plan` | Dry run → `plan_id` (kept 30 min in memory), elements, `left_out`, `needs_secrets`, disclosures, size estimate, `plan_digest` | **B1** (one agent) → B2+ |
| `GET /packages/export/plan/{plan_id}` | Re-read a plan (wizard resume) | **B1** |
| `GET /packages/catalog` | Exportable items with counts and approximate sizes | B2 |
| `POST /packages/exports` · `GET …/{id}` · `GET …/{id}/file` · `DELETE …/{id}` | Export job: start (with `plan_id`), status/progress, download (cookie-authenticated link, `Content-Length`), cancel | B2 |
| `POST /packages/import` | Upload → preview (existing; B2: streamed to disk, per-item status, candidate targets) | exists |
| `GET /packages/import/{staging_id}/preview?mode=` | Re-plan for a mode, returns `plan_digest` | B2 / B8 |
| `POST /packages/import/{staging_id}/apply` | `{mode, plan_digest, decisions[], password?}` → `{import_id, state, result}` | exists; extended B2 |
| `GET /packages/imports/{import_id}` | Operation record state + result (survives staging cleanup) | B2 |
| `POST /packages/imports/{import_id}/finish-cleanup` | Re-run an unfinished automatic undo | B2 |
| `GET /packages/imports/{import_id}/undo-plan` · `POST …/undo` | Person-initiated undo (D-E5.5) | B8 |
| `DELETE /packages/import/{staging_id}` | Cancel a staged upload before apply | B2 |

From B2 the old `POST /packages/export` is a wrapper over the job path (one execution path). Jobs are justified by progress and a retryable download for large workspaces; caps keep their cost bounded.

#### D-E3.2 Capture consistency

Each element is read coherently under its own store's boundary (agent: `ReadState` already covers record + SOUL.md + AGENT.md, E18; workspace: record + delegation + instructions under its lock `[PROVE IN B6]`). **No cross-element snapshot.** The job recomputes the plan and refuses `plan_stale` if the element set, ownership, dependency edges, membership or `left_out` changed since review; content inside reviewed elements is taken as of capture. A file whose size/mtime changes during read is re-read once, then the job fails naming it.

#### D-E3.3 Folders and lifecycle (disjoint, one owner each; all under agent-protected `backups/`, F13)

| Folder | Owner | Holds | Lifecycle |
|---|---|---|---|
| `.staging/<staging_id>/` | import staging registry (existing, unchanged) | uploaded bundle, extracted payload | as today (E17): deleted at `FinishApply`, TTL, or sweep |
| `.imports/<import_id>/` | **new operation store** (B2) | journal (step log), created list, before-images (B8), post-import fingerprints, final result | created at apply start, **before** any write; kept 30 days `[ASSUMPTION]` or until deleted by the person; never deleted while state is `undoing`/`needs_attention`; mode 0700/0600; **no route serves its contents** except the result summary |
| `.exports/<export_id>/` | **new export registry** (B2) | captured payload, final file | `building` → `ready` (30 min, download leases) → deleted; all deleted at gateway start; cancel deletes |

**Operation state machine (B2):**

```
            apply (plan_digest ok)
 staged ───────────────────────────► applying ──all steps ok──► done
                                        │
             step failed / changed_since_preview / crash (boot)
                                        ▼
                                     undoing ──all undone──► undone (result: "nothing kept", reason)
                                        │
                           undo step failed (e.g. disk)
                                        ▼
                                 needs_attention ──finish-cleanup / next boot──► undoing
```

- No "retry forward": a failed import is undone; the person imports again (new plan, new IDs).
- No cancel once `applying` has started (apply is short; cancel exists only for staged uploads and export jobs).
- The result is stored in the operation record before the staging folder is deleted, so a lost HTTP response or a reload still finds it via `GET /packages/imports/{import_id}`.
- Boot: `undoing`/`needs_attention` records are undone before the roster loads (existing hook, E11); failure keeps `needs_attention` and the page shows it with "Finish clean-up".
- A slice shows only actions it implements: B2 shows "Finish clean-up"; "Undo this import" appears from B8.
- Capacity: 2 exports building, 5 ready, 5 staged imports; free disk ≥ estimate × 2 before start, else `insufficient_disk`.

#### D-E3.4 Extraction to disk (B2)

Refused names: absolute, `..`, `.` or empty segments, trailing `/` on a file, backslash, NUL, control chars, `:`, Windows reserved names, trailing dot/space. Uniqueness and ownership checks on canonical key = NFC + case-fold; file-vs-directory prefix conflicts refused. Regular files and directories only. Writes via `os.Root`, `O_CREATE|O_EXCL`, 0700/0600, hashing while writing, counters on the real stream, per-type caps. Refusal deletes the folder and frees the slot. `Verify`/`Inspect` use a hash-and-discard sink and write nothing (A1). Linux, macOS, Windows tested.

```
CONFIDENCE: Medium-High. Caps after measuring three real workspaces [PROVE IN B2].
```

### D-E4 — Secrets (structured guarantee) and disclosure

**Layer 1 — never read:** exporters use store interfaces; `credentials.json`, `master.key`, `cli.token`, `auth.json`, `config.json` (as a file), channel tokens, provider settings never opened; instrumented filesystem with a positive control.

**Layer 2 — typed allow-list per element** over the real source types including nested fields; coverage test fails on an undecided new field (replaces deny list E5). Connector fields:

| Field | Decision | Guaranteed? |
|---|---|---|
| `name`, `type`, `deferred`, `enabled` (source value; import forces **disabled** `[PROVE IN B4]` that nothing connects before enablement) | travel | — |
| `env_refs`, `header_refs`, `env`, `headers` | values never travel; names → `${NAME}`; listed in `needs_secrets` | **Yes** |
| `env_file` | dropped | **Yes** |
| `url` user-info, query values | removed → placeholders, names kept | **Yes** |
| `url` path | travels; any segment that looks like a token (≥ 16 chars of base64url/hex alphabet, or mixed letters+digits ≥ 8) → placeholder `path_segment_removed` | **No** — disclosed |
| `command` | absolute path → base name; contains `=` or whitespace → connector refused for export | **No** — disclosed |
| `args` | flagged names (`token`, `key`, `secret`, `password`, `passwd`, `auth`, `bearer`, `credential`) with `=`, the value after such a flag, or token-looking values → placeholders | **No** — disclosed |

**Disclosure (on review and import result):** for every exported connector with a URL path, command or arguments: "Some services put an access key inside the web address or start command. We remove the parts that look like keys, but please check *<connector name>* after import and re-enter its key if needed." The disclosure names connector and field only, never the value.

**Layer 3 — free text (from B1):** "Text you wrote (prompts, instructions, notes, files, chats) is copied exactly as it is. If you pasted a password or key into it, it goes with the bundle." Optional suspicious-file scanner in B7, never presented as a guarantee.

**Canaries per slice** (only the carriers that slice supports), in payload **and** manifest: credential vault, master key, CLI token, `auth.json`, provider key, channel token, executor runtime fields, connector literal/ref env and headers, URL user-info, URL query, flagged args, long URL path tokens. Known non-guaranteed cases (short path token, plain positional secret) are written as tests that document the limit, not as passing guarantees. Full "Everything + chat history" canary in B9.

```
CONFIDENCE: High for the guaranteed rows; Medium for classifier rows (disclosed); Low for free text (disclosed).
```

### D-E5 — Import: publication, locks, recovery, undo, identity

#### D-E5.1 What is guaranteed (C6, chief's ruling)

| Import kind | Guarantee |
|---|---|
| **Single agent (B1, today's engine)** | Unchanged S1a/A9: pending record, join + commit rename in one workspace-lock hold, all-or-nothing, start-up recovery. [FACT] |
| **Multi-item (B2+)** | Each item is written **invisible first where an existing mechanism exists** (agents `.pending`, E1; new skills in a `.`-folder, E12; others `[PROVE IN Bn]`), then **published one at a time**. If any step fails, or a target changed since preview, or the gateway stops, **automatic undo** removes or restores every step already published, in reverse order. While it runs, people and running agents may see and use part of the set; work they do with those items (messages, delegations, tool calls) is **not** undone. No cross-item snapshot. |

**Publication order (reduces what a half-imported set can do):**

```
1 skills (new: rename dot-folder)          4 workspaces (record, instructions)
2 connectors (always disabled)             5 memory, work files, knowledge
3 agents (pending → live, one by one)      6 team membership and delegation edges — LAST
7 sessions (B9)
```

Delegation edges are published last, so an imported agent cannot be reached by delegation until the rest is in place. Connectors stay disabled until the person enables them.

**On screen** (preview and result, B2+): "Items are added one after another. While the import runs you may briefly see some of them. If something fails, everything added is removed again automatically."

```
CONFIDENCE: High that this is implementable on existing mechanisms; Medium on per-type invisibility [PROVE IN B3/B6/B9].
```

#### D-E5.2 What joins when

| Slice | Joins |
|---|---|
| B2 | operation store, journal step log, automatic undo, multi-agent publication |
| B3 | skills (dot-folder add; Replace swap is two steps per E12, covered by undo) `[PROVE IN B3]` direct skill readers' behaviour |
| B4 | connectors (config write through the existing MCP path, disabled) `[PROVE IN B4]` live config refresh / MCP reconcile behaviour |
| B5 | agent memory (inside the pending agent folder) |
| B6 | workspaces (record, instructions, files, knowledge, mounted folders, membership, delegation) `[PROVE IN B6]` pending workspace or "publish then undo" |
| B8 | Replace/Merge before-images, person-initiated undo |
| B9 | sessions `[PROVE IN B9]` |

#### D-E5.3 Locks, re-verification, fingerprints (B2 for additions; B8 for Replace/Merge/undo)

**Lock order (documented, deadlock-free):**

```
import lock (outer, held for the whole apply or undo)
  └─ at most ONE entity's ordinary lock at a time, taken and released per step:
       agent    → the lock MutateStateWithAgentMD uses
       workspace→ workspace.LockID(id)            (never two at once: E14)
       skill    → the skills pipeline's own lock
       config   → the config write lock (connectors)
     or ONE existing combined helper whose internal order is already established
       (e.g. JoinWorkspaceTeamAndPublish, S1a)
```

No step ever holds two entity locks, so same-shard workspace collisions cannot self-deadlock and the order relative to ordinary writers is the ordinary writers' own. `[PROVE IN B2]`: list every combined helper used and its internal order.

**Per-step rule (Replace, Merge, undo):** take the entity's ordinary lock → recompute its **complete fingerprint** → compare with the previewed one → if different: release, stop with `changed_since_preview`, start automatic undo → else capture the before-image into the operation record (fsync) → write → record the post-write fingerprint → release.

**Complete fingerprint per entity** = hash over every stored part the step may overwrite:

| Entity | Fingerprint parts |
|---|---|
| Agent | agentstore revision (E13) — `[PROVE IN B8]` that it covers AGENT.md; else add its hash |
| Workspace | `RevisionForState` (record + edges) **and** `RevisionForInstructions` |
| Skill | folder tree hash |
| Connector | its `config.json` entry JSON |
| Memory / work file | content hash |

**Writers outside entity locks** (agents writing memory or work files): for files, Replace writes to a temp file and renames over the target right after the hash check; a write that lands in that sub-millisecond gap is lost and is **not** detected. Merge never overwrites files ("keep yours"). Stated in the docs. `[PROVE IN B8]`.

**Confirmation binding:** `plan_digest` = hash(package SHA-256, mode, sorted {source ref, action, target ID, target fingerprint}); checked once at apply start, and each target re-verified per step as above.

#### D-E5.4 Automatic undo

Uses the operation record's step log: for each published step in reverse, take the entity's ordinary lock, check the post-write fingerprint: unchanged → delete the addition or restore the before-image; changed since the import wrote it → leave it and list it in the result ("changed while importing; left as is"). Undo is idempotent; boot repeats it until done (D-E3.3). Before-images restore runtime fields and original IDs exactly (B8). The undo record is private (D-E3.3) and never a bundle.

#### D-E5.5 Person-initiated undo (B8)

- **Unit = top-level item** (agent, workspace, skill, connector, session) **with all its owned parts.** There is no way to keep an owned part while undoing its owner.
- **Undo plan before confirmation** (`GET …/undo-plan`): per top-level item: restore to before-image or delete addition; owned parts that go with it, flagging those **edited since the import** ("3 notes, 1 edited by you since the import"); optional-dependency effects ("agent Writer, which you keep, uses skill X; after undo X is missing").
- The person ticks top-level items only; the server rebuilds the plan with the dependency closure and shows it again if it grew. Confirmation binds to the plan's fingerprints; execution uses D-E5.3's per-step rule; a changed item is skipped and listed, the rest continue (each top-level item is independent).

#### D-E5.6 Targets and identity

| Matches (same type, case-insensitive name) | Behaviour |
|---|---|
| 0 | added |
| 1 | mode's action, target ID shown |
| many | person picks a target or "Add as new"; else `422 target_ambiguous` |

| Type | Validator (actual) | Allocation |
|---|---|---|
| Agent | name ≤ 100 (`MaxAgentNameLen`) | `Name (imported N)`, base truncated, N ≤ 99, else `name_suffix_exhausted` |
| Workspace | name ≤ 200 bytes (`handleWorkspacePost`) | same pattern within 200 bytes |
| Skill | `skillname.Valid` (letters incl. uppercase, digits, single hyphens, ≤ 64 bytes) | case preserved; clash case-insensitive; `<base>-imported-<N>`; base cut at the last hyphen that fits, or at the byte limit if none; trailing hyphen stripped; base never empty (fallback `skill`) |
| Connector | existing MCP write validator `[PROVE IN B4]` | `<name>-imported-<N>` within it |
| Memory/work file | path kept; Merge clash keeps yours | — |

All references remapped (base D5). Tests: max-length slug with and without hyphens, case-only clash, exhaustion, 200-byte workspace name.

### D-E6 — UI

```
Sidebar ASSET_ITEMS ─ "Export & Import" ─► /export-import  (src/routes/_app/export-import.tsx)
  validateSearch: { wizard?, step?, scope?, preselect?, plan_id?, export_id?, staging_id?, import_id? }
  ├ ExportImportScreen: Export · Import · Recent imports (state, result; Undo from B8) · Needs attention (B2)
  ├ ExportWizard: Scope → Choose → Review → Download
  └ ImportWizard: File → Preview → Mode → Confirm → Result
Quick Export: AgentProfile (Team panel), workspace Settings tab, skill detail
  → navigate('/export-import', { wizard:'export', scope, preselect })
```

| Step | Reload | Back |
|---|---|---|
| Scope / Choose | rebuilt from `scope` + `preselect`; other ticks reset, with a notice | previous |
| Review | `GET export/plan/{plan_id}`; expired → Choose with notice. Never shows a review the server does not hold. | Choose |
| Download | reconnect via `export_id`; expired → Review | Review |
| Import File | file chosen again | leaves |
| Preview / Mode | via `staging_id` until expiry; expired → File | previous |
| Confirm | back to Preview (password never in URL or storage) | Mode |
| Result | `GET /packages/imports/{import_id}` (B2+). **B1:** result comes from the apply response; reload shows "Import finished — find the agent in its workspace Team" without re-fetch. | does not undo |

Local `Stepper` from existing `ui/` parts; generated types; hidden in platform mode (Q3). Keyboard, focus, announcements, loading/empty/error, narrow screens: in each slice spec.

```
CONFIDENCE: High.
```

### D-E7 — Slices

**B1 is fully specified by this ADR and depends on no `[PROVE IN …]` item.**

| # | Slice | Backend | Visible | Depends | Size 🔶 |
|---|---|---|---|---|---|
| **B1** | Page + wizards, one agent | `POST /packages/export/plan` + `GET …/plan/{id}` for exactly one custom agent (contents, `left_out` incl. removed fields and dropped connector bindings, `needs_secrets`, size, free-text disclosure); existing export, upload, preview, apply (`new`), S1a all-or-nothing import unchanged; **no multi-item write, no format change** | Sidebar entry; Export wizard (Chosen agents = one) with authoritative review and free-text notice; Import wizard (Add as new) with preview and result; Quick Export in AgentProfile; canary for the agent carriers | S1a, S1b | M |
| B2 | v2, disk reader/writer, jobs, multi-agent, operation store, automatic undo | D-E1, D-E2 (agents), D-E3, D-E5.1–5.4 for agents | Several agents; progress; big bundles; cancel; FR-5a notice; Needs attention + Finish clean-up | B1 | L |
| B3 | Skills | skill element, dot-folder publish, slug allocator | Skills follow agents; Skills list in Choose; Quick Export on skill | B2 | M |
| B4 | Connectors | field table, disabled import, disclosures, canaries | Connectors arrive disabled, "needs secret"; URL/command disclosure | B2 | M |
| B5 | Agent memory | memory element | Memory tick | B2 | S–M |
| B6 | One workspace | workspace elements, membership and delegation last | Workspace scope; Quick Export on workspace | B3–B5 | L |
| B7 | Everything + full Pick items | everything scope, owner/optional rules in UI, optional scanner | All scopes, exclusions | B6 | S–M |
| B8 | Replace & Merge + Undo | targets, `plan_digest`, per-step lock/verify, before-images, undo plan | Mode choice, target picker, password, "Undo this import" | B6; Q1, Q2 | L |
| B9 | Chat history | sessions; full canary | Chat history tick | B6 | L |
| — | Claude Code plugin | parked | — | — | — |

### D-E8 — Testing

| Level | What |
|---|---|
| Engine | closure (exclude wins, owned never root), remap, allocators, canonical paths (case/Unicode/prefix) |
| Compatibility | v1 + per-type v2 fixtures; offline verify writes nothing |
| Secrets | no-open with positive control; allow-list coverage on nested source types; per-slice canaries in payload + manifest; documented-limit tests for classifier rows |
| Publication & undo | pause at every publication step while real delegation checks, skill loads, MCP reconcile and reloads run: assert the visible set is a **prefix of the publication order** (the stated guarantee) and that delegation to an imported agent is impossible before step 6; fail each step → automatic undo leaves the pre-import state except listed "changed while importing" items; crash at each step → boot undo; undo failing → `needs_attention` → finish-cleanup |
| Concurrency (B8) | ordinary edit between preview and each step (incl. workspace **instructions only**) → `changed_since_preview` before that write; two workspaces forced onto the same lock shard → no hang (no nested locking by construction; asserted with a step-completion signal, not a timer) |
| Lifecycle | export vs upload sweep; restart with jobs; TTL vs download lease; result lookup after staging cleanup and after a lost response; boot with `needs_attention` |
| Undo plan (B8) | keep-child-undo-owner impossible; edited owned parts flagged; plan growth re-shown; interleaved edit after undo preview |
| UI | Vitest per step and resume row; error codes to plain text |
| E2E | Playwright per slice through sidebar and every Quick Export; real browser download (via `heavy`) |

Checker rule: test author and CHECK on different models from the developer.

---

## 3. Options considered (one line each)

| Topic | Chosen | Rejected and why |
|---|---|---|
| Multi-item guarantee | Per-item publication + automatic undo + on-screen statement | Atomic runtime barrier: no mechanism in code (E10); building one touches every direct reader — chief ruled it out |
| Lock scheme | One entity lock at a time, ordinary locks | Lock all affected entities: nested `LockID` forbidden (E14), cross-order deadlocks with ordinary writers |
| Failure after partial publish | Undo | Roll forward: needs a barrier and a second outcome rule |
| Safety copy | Private exact before-images | Sanitised bundle: loses runtime fields and IDs |
| URL path secrets | Classify + disclose | Force re-entry of every URL path: breaks most remote connectors' import |
| Export execution | Jobs, one path | Two size-dependent paths |
| Wizard host | Route + search params + server `plan_id` | Modal per page; storing selection in URL |

---

## 4. Risks

| # | Risk | Mitigation |
|---|---|---|
| R1 | Running agents act on a briefly visible item that undo then removes | Delegation last, connectors disabled, on-screen statement, short publication |
| R2 | File write in the hash-to-rename gap lost on Replace | Merge never overwrites; stated; B8 spec measures |
| R3 | B2 reader rewrite breaks S1 guarantees | S1 tests kept; golden v1 fixture; architect review |
| R4 | Classifier misses a URL/arg secret | Disclosure per connector; documented-limit tests |
| R5 | Operation records hold secrets | Protected folder, 0600, no route, 30-day retention, delete on request |
| R6 | Disk use | free-space check; caps; TTLs |
| R7 | Sizes underestimated | B1 thin; re-estimate after B2 |

---

## 5. Gaps (each owned by a slice spec)

| # | Gap | Owner |
|---|---|---|
| G1 | Real bundle sizes → caps | B2 |
| G2 | Combined helpers' internal lock order | B2 |
| G3 | Direct skill readers / Replace swap visibility | B3 |
| G4 | Connector grammar; live config refresh and MCP reconcile with disabled connectors | B4 |
| G5 | Pending workspace or publish-then-undo; instructions read boundary | B6 |
| G6 | Agent revision covers AGENT.md; file write gap | B8 |
| G7 | Session stores (base F11) | B9 |
| G8 | Founder receipt for 21 Nov | team-lead, before `/plan-spec` |
| G9 | Retention 30 days `[ASSUMPTION]` | B2 (founder may change) |

---

## 6. Confidence roll-up

D-E1 High · D-E2 High (Medium wording) · D-E3 Medium-High · D-E4 High guaranteed / Medium classifier / Low free text (all disclosed) · D-E5 Medium-High (guarantee deliberately weaker; per-type items to prove) · D-E6 High · D-E7 High for B1, Medium-High order, Low sizes · D-E8 High.

---

## 7. Next steps

1. Team-lead attaches the 21 Nov receipt (G8).
2. **No further ADR grill** (review cap). `/plan-spec` for **B1** now; its spec grill checks B1 only.
3. B2 spec next; each later slice spec must discharge its `[PROVE IN Bn]` items and gaps before implementation.
4. Founder answers Q1–Q2 before B8; Q3 any time.
5. On promotion into the repo: amendments A12+ to `docs/internal/architecture/ADR-export-import-packages.md` (including C6 replacing the multi-item part of A9) and B1–B9 replacing S2–S10 in `docs/internal/specs/export-import-slices.md`.

---

## 8. Questions for the founder

**Q1 — When you Replace an agent or workspace, does it stay "the same one"?**
Chats with it, its place in other teams and delegation lines all point at its identity. Either way a private undo record is kept and "Undo this import" brings the old one back.
- **A (recommended):** keep the identity, replace only its contents; chats, teams and lines stay connected.
- B: create a new one and remove the old one; old chats lose their agent, other teams lose the member.

**Q2 — What does Merge mean when an item exists on both sides?**
- **A (recommended):** add what is missing inside it (notes, files, skills); where both have the same note or file, keep yours and list it. Nothing overwritten or deleted.
- B: skip existing items; only add new ones.

**Q3 — Export & Import in the hosted (online) edition in this epic?**
- **A (recommended):** local only now; hosted later with its own security review and size limits.
- B: both now.
