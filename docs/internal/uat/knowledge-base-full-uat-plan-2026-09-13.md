# Knowledge Base & Library — Full UAT Plan (2026-09-13)

Branch under test: `integrate/library-improvements-v0.1.1` at `f83c84223` (all six flake-derived fixes included).
Instance: `http://127.0.0.1:5177`, home `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/home`, login `founder` / `OmnipusUAT-2026`.
Provider/model for every agent in this plan: **openrouter** + **z-ai/glm-5.3-flash** (founder rule, 2026-09-11; sent as two fields, never `openrouter/z-ai/…`).

This plan supersedes nothing; it composes the existing `library-uat-plan.md` (Library pane, 10 file types, EDGE-1…15) and `knowledge-tools-uat-plan.md` (agent tools, scope trap) into one end-to-end campaign with two kinds of actor:

| Actor | Who | Door | Validates |
|---|---|---|---|
| **Builder** | an Omnipus agent (`UAT Builder`, created for this campaign with every tool allowed) | agent tools: `knowledge_base_create`, `knowledge_configure`, `knowledge_edit`, `knowledge_restructure`, `knowledge_find`, `knowledge_read`, `knowledge_list`, `knowledge_describe`, `grep`, `library_*`, `bash` | the agent door: "agents must be able to do everything in the vault" |
| **Testers T1…T6** | Claude subagents in this harness impersonating human testers, driving the real UI through the Playwright MCP browser with screenshots at every checkpoint | the web door: Library panel, previews, base views, search bar, inline editing | the human door, including visual correctness |
| **Operator** | one Claude subagent that talks to the Builder in the chat UI, reads its tool-call cards, and hands evidence to the register | the chat door | that the agent actually did what it claims |

Ground rules (inherited, non-negotiable):

1. Every claim of PASS carries evidence: a screenshot file, a tool-call card, or an API response saved under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/evidence/2026-09-13/<scenario-id>/`.
2. A tester never fixes anything. A defect is written up with reproduction steps, expected, observed, evidence path, severity.
3. Testers use the UI only. The API (`apiFetch`) is allowed solely as an **oracle** to confirm what the UI showed (e.g. read the record the UI just edited).
4. The Builder is driven only through chat prompts. If it needs a capability it lacks, that is a finding, not a workaround.
5. Nothing in this plan touches the founder's real Obsidian vault. The founder vault copy at `uat/vault` (mount `kb`) is read-mostly; the Builder creates a **new** vault inside its own workspace.

---

## 0. Environment and setup (Operator, before anything else)

| Step | Action | Oracle |
|---|---|---|
| 0.1 | Confirm instance: `GET /api/v1/state` → 200; binary from `f83c84223` | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/build-status.txt` says `SHA=f83c84223` |
| 0.2 | Create workspace **UAT Build** (Library → workspace crumb → New) | `library-workspace-node-<id>` appears |
| 0.3 | Create agent **UAT Builder** via `POST /api/v1/agents`: type `Main`, provider `openrouter`, model `z-ai/glm-5.3-flash`, workspace = UAT Build, and a **complete** `tools_cfg.builtin.policies` map with `allow` for all 8 knowledge tools, `grep`, `library_list`, `library_read`, `bash`, `request_mount`, `list_mounts`; every other catalog tool `deny`. Never `ask` (an unattended run would hang on `tool_approval_required`). A sparse map or `"*"` is a 400 by design. | `GET /api/v1/agents/<id>/tools` echoes `allow` for the listed tools |
| 0.4 | Add UAT Builder to UAT Build's core team; make it the chat target | Team tab shows it |
| 0.5 | Evidence folder created; Operator opens a chat with UAT Builder and sends the first prompt of Phase 1 | first tool-call card |
| 0.6 | Testers get their persona sheet (Section 3), the selector sheet (Appendix A), and the login | each tester's first screenshot is the Library panel after login |

Trap to state up front: all knowledge tools resolve their scope from the **turn's workspace**, never from a tool argument. A Builder chatting from the wrong workspace sees an empty knowledge base and every write "succeeds" nowhere. Step 0.4 exists for this.

---

## 1. Phase 1 — Builder creates a knowledge base from scratch (agent door)

Prompts are given to the Builder verbatim by the Operator, one at a time. Each row is PASS only when the tool-call card shows the named tool, the result is not a refusal, and the on-disk/UI oracle agrees.

### 1A. Create the vault and its structure

| ID | Prompt (paraphrased) | Expected tool | Oracle |
|---|---|---|---|
| B-01 | Create a new knowledge base called **UAT Vault** in the workspace root | `knowledge_base_create` | `.omnipus-vault/` marker exists; Library shows the folder; KnowledgePanel detects a collection |
| B-02 | Create folders `Projects`, `People`, `Meetings`, `Assets`, `Dashboards` | `bash` or `knowledge_edit create` with folder paths | rows appear in Library |
| B-03 | Define record type **project** with properties: `status` enum(active, paused, done), `priority` enum(low, med, high), `start` date, `owner` relation→person, `budget` decimal, `tags` list, `done_ratio` derived, identity prefix `PRJ` | `knowledge_configure create_record_type` | `.omnipus-vault/records/project.yaml`; `knowledge_describe` lists it with enum values |
| B-04 | Define **person** (`role` text, `email` text, `team` enum) with prefix `PER`; **meeting** (`date` date, `attendees` relation→person list, `project` relation→project) with **no** identity prefix | `knowledge_configure create_record_type` | meeting ids will be bare `0001` (FR-036b) |
| B-05 | Create 6 people, 5 projects, 8 meetings with realistic values, including one project with unicode title `Café Zürich — Phase Ⅱ`, one with a 200-char title, one with an empty optional field | `knowledge_edit create` ×19 | each note has `id:` minted `PRJ-0001…`, `PER-0001…`, `0001…` (FR-036); sequence files under `.omnipus-vault/`; `knowledge_find type:project` returns 5 |
| B-06 | Re-create a project with the same title | `knowledge_edit create` | either refused or a distinct path; never a duplicate id (FR-039 via `check_integrity`) |
| B-07 | Create a project using the `v1:absent` create-as-CAS form, then repeat identical | `knowledge_edit create` | second call is a conflict, not an overwrite |

### 1B. Edit, incremental edit, relations, refusals

| ID | Prompt | Expected | Oracle |
|---|---|---|---|
| B-08 | Set `status` of PRJ-0002 to `done` | `set_property` with `expect_version` | file frontmatter changed; version token rotated |
| B-09 | Same edit again with the **old** token | conflict error carrying the current token (FR-106 family) | file unchanged |
| B-10 | Set `status` with an empty `expect_version` | refused (empty token is never accepted) | file unchanged |
| B-11 | Set `status` to `Done` (wrong case) | accepted, written in canonical enum case (FR-011); folded form never written | frontmatter shows `done` |
| B-12 | Set `status` to `archived` (not in enum) | `invalid_value` refusal | unchanged |
| B-13 | Set `done_ratio` to 0.5 | `derived_property` refusal (FR-046) | unchanged |
| B-14 | Set `owner` via `set_property` | `relation_property` refusal steering to `op: relation` (FR-045) | unchanged |
| B-15 | Add PER-0003 to meeting 0004's `attendees` | `relation` `relation_op: add` | list grows by exactly one, source list style preserved (FR-040a) |
| B-16 | Remove PER-0001 from the same list; then **replace** the whole list | `relation` remove, then `replace` | replace must be named explicitly (FR-045); a `set_property` that would shrink a list is refused (`list_property`) |
| B-17 | Append a `## Decisions` section to meeting 0002, twice with `once: true` | `append_section` | exactly one section |
| B-18 | Replace the paragraph anchored on "Budget:" in PRJ-0001; then try an anchor that occurs twice | `replace_body` by anchor | second is refused naming every match, file unmodified (FR-047) |
| B-19 | Replace lines 3–5 of PRJ-0003 by line range | `replace_body` line_range | exact lines replaced |
| B-20 | Set the identity property `id` directly | `identity_property` refusal | unchanged |
| B-21 | Set a property that does not exist on the type | `unknown_property` refusal | unchanged |

### 1C. Links, embeds, dashboards, every file type

| ID | Prompt | Expected | Oracle |
|---|---|---|---|
| B-22 | Link PRJ-0001 → PER-0002 with alias "lead"; link to `#Decisions` section of meeting 0002 | `link` | wikilinks with alias and `#heading` form present |
| B-23 | Upload (via `bash` copy into `Assets/`) one each of: `png`, `jpg`, `svg`, `gif`, `webp`, `pdf` (multi-page), `mp3`, `mp4`, `html`, `csv`, `json`, `yaml`, `txt`, `mmd`, a zero-byte file, a 1.2 MB text file, a file named `café résumé.md` (NFD), and `../` traversal attempt | `bash` | Library lists them; attachments are indexed by path only, bytes never opened (FR-039a); traversal refused |
| B-24 | Create note `Dashboards/Overview.md` embedding: an image with `\|400`, the pdf with `#page=2`, the mp3, the mp4, the html, the csv, the mmd, a note, a note `#heading`, a note `#^block`, and the base view `![[Projects.base#Active Projects]]` | `embed` ×11 | `page` on embed is **refused** ("not supported yet") — agent must fall back to writing the notation via `create`/`replace_body`; `width` on audio refused naming the reason (H5); `.mmd` embed refused permanently (ADR-083 §15) |
| B-25 | Create `Projects.base` with all 8 view kinds: table, list, tiles, board(columns by status), calendar(by start), summary, trend, breakdown; plus a chart and a crosstab part; one view with a filter, one with a sort, one with a group | `knowledge_configure write_view` / `create_view` | `knowledge_describe include: views` lists 8+; `/knowledge/base-views` serves them; one deliberately **unservable** view (bad property) shows the server's reason |
| B-26 | Create two views with labels differing only by case; then two with the same label | `create_view` | case ladder resolves; duplicate label **refused naming both** (D5) |
| B-27 | Add a ```` ```query ```` fence to Overview.md that finds `type:project status:active` | `replace_body` | fence renders ≤5 hits with a "more" statement |
| B-28 | Add a YouTube link embed with an 11-char id plus junk params | notation written | params not forwarded; look-alike host stays a link (Dataset F) |

### 1D. Restructure, trash, restore, integrity

| ID | Prompt | Expected | Oracle |
|---|---|---|---|
| B-29 | Rename PER-0002's note; move meeting 0003 to `Meetings/2026/` | `knowledge_restructure rename/move` | inbound wikilinks rewritten; ids unchanged |
| B-30 | Trash PRJ-0004; list; restore | `trash` then `restore` | `.omnipus-vault/trash/<colon-free ts>/…` (FR-048); inbound links counted+listed, not repaired; index forgets immediately; restore keeps old id (FR-038a) |
| B-31 | Create a new project while PRJ-0004 is in trash, then restore PRJ-0004 | create, restore | new project gets PRJ-0006 (counter never lowered, FR-038); restore refuses only on a live collision, naming both paths |
| B-32 | Run `knowledge_describe check_integrity: true` | describe | zero findings, or exactly the findings the plan created |
| B-33 | Delete record type `meeting` while records exist | `delete_record_type` | refused or explicit cascade statement; nothing silently orphaned |

### 1E. Search, query, joins, index (agent door)

All through `knowledge_find` unless stated. Every response is checked for the honesty fields `complete, refused, counts{selected,evaluated,shown}, query_echo, rows, totals, problems, next`.

| ID | Query | Expected |
|---|---|---|
| Q-01 | `words: "Zürich"` | unicode hit; `counts.shown = 1` |
| Q-02 | `type: project, filter: status = active` | exact set |
| Q-03 | `type: project, sort: start desc, limit: 2, cursor` paging | stable paging, `next` present then absent |
| Q-04 | `join: owner` (relation column borrowed, FR-124) | owner's `role` visible on project rows |
| Q-05 | `type: meeting, hops: 2` from a person | 2-hop traversal; `hops: 3` clamped and reported (MaxHops=2) |
| Q-06 | `group_by: status`, then two levels, then three | third level refused/clamped (MaxGroupLevels=2) |
| Q-07 | `aggregate: sum budget` with `limit: 1` | total over the full evaluated set, not the page (FR-125a) |
| Q-08 | `view: "Active Projects"` | same rows the UI tab shows |
| Q-09 | `explain: true` on Q-04, then mutate a record, `explain` again | byte-identical plan (FR-073); no rows evaluated |
| Q-10 | `limit: 500` | clamped to 200 and reported (FR-063) |
| Q-11 | filter with 65 leaves / depth 9 | refused with the limit named (MaxFilterLeaves=64 / MaxFilterDepth=8) |
| Q-12 | `near: "budget overrun"` | ranked hits |
| Q-13 | `kind: attachment` | the uploaded files by path |
| Q-14 | edit a record, then find immediately | new value visible (incremental re-index, `IndexWarning` absent) |
| Q-15 | `grep pattern: "Budget:" path: "Projects"` | ADR-081 engine; counts match `bash grep -rc`; `.omnipus-vault/` pruned |
| Q-16 | `grep` on a binary (the pdf) | binary skip surfaced, not silently zero |
| Q-17 | `knowledge_read` of `Overview.md` `section: Decisions` | just that section |
| Q-18 | `knowledge_list type: person` | 6 |

---

## 2. Phase 2 — Human testers on the web door (Playwright MCP, visual)

Every tester logs in as `founder`, opens Library (`sidebar-library-button`), and works in **both** the docked panel and the pop-out route `/#/library?workspace=…`. Each checkpoint = one screenshot named `<id>-<step>.png` plus the accessibility snapshot. Visual validation means the tester looks at the screenshot and states what it shows, not only that a selector existed.

### T1 — "Nadia, the reader" (explorer, previews, every file type)

| ID | Steps | Expected (visual + selector) |
|---|---|---|
| U-01 | Open Library; browse UAT Vault folders via `library-row-<path>`; breadcrumbs | rows with correct icons; vault folder marked; `library-mounts-count` correct |
| U-02 | Toggle hidden files | `.omnipus-vault` appears with `library-hidden-badge-*` |
| U-03 | Preview each Asset: png/jpg/gif/webp/svg | `library-image-preview`; svg rendered as image, never inline |
| U-04 | mp3, mp4 | `library-audio-element` / `library-video-element` playable; `.aiff` → download card |
| U-05 | pdf | `library-pdf-preview`, pages lazy; first page < 45 s; zoom 0.25–4; `library-pdf-retry` appears only on error |
| U-06 | html | `library-preview-untrusted-boundary` visible; frame src `/library-preview/<token>/…`; page renders; after 15 min `-expired` + reload works |
| U-07 | csv/json/yaml/txt | code preview with highlighting; 1.2 MB file loads or states its limit |
| U-08 | mmd | mermaid preview draws; `.mmd` **embed** in Overview shows as a link (C8) |
| U-09 | zero-byte file; 200-char name; NFD name | opens or states why; names not truncated/garbled |
| U-10 | `Overview.md` | image at 400 px; pdf **page 2** mounted; audio/video mounted; html/csv/mmd as links (link-only kinds); transclusion of note/heading/block; base view embedded with caption; lazy mounts ≤ 4 in flight; `unmounted-embeds-notice` appears only when applicable |
| U-11 | Knowledge reader on PRJ-0001 | `knowledge-outline` headings; `knowledge-backlinks` lists PER-0002 with alias badge; ambiguous/unresolved rendered distinctly |

### T2 — "Marek, the editor" (create, edit, rename, move, delete, conflicts)

| ID | Steps | Expected |
|---|---|---|
| U-12 | Create menu | items: New folder, Upload, New vault, Add mount, Manage mounts. **No "New note"** — record as *expected gap*, then confirm the KnowledgePanel empty state shows `knowledge-create-note-unavailable` |
| U-13 | New folder `Q4` ; then `Q4` again; then `a/b`; then `../x` | second → `-collision`; slash → `-slash`; traversal → `-traversal` |
| U-14 | New vault `Second Vault` via dialog; names `.hidden` and `a/b` | created; `-name-dot` / `-name-slash` refusals |
| U-15 | Upload the same png twice | duplicate handled visibly (EDGE-2) |
| U-16 | Rename PRJ-0001's file; move it to `Q4`; copy it back; delete the copy with `library-delete-confirm` | wikilinks still resolve after rename/move (agent-side cascade) — verify in reader |
| U-17 | Edit note body: `library-preview-mode-edit` → change → `library-preview-save` | saved; reopen shows change; **no autosave** (navigate away without save → change lost, EDGE-11 accepted behaviour, must warn or lose visibly) |
| U-18 | Two tabs: edit same note in both, save A then B | B gets 409 handling (`isLibraryVersionConflict`), fresh token stored, second Save succeeds; no silent clobber (R-1) |
| U-19 | Edit while the Builder edits the same note (Operator triggers) | UI 409 path again; agent's write wins or loses **visibly** |
| U-20 | Delete a file that is currently previewed | preview closes cleanly (EDGE-13) |
| U-21 | Add mount to `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/vault` (founder copy) and to `/` | first: `library-mount-badge`; second: `library-add-mount-dialog-broad`/`-refused` |
| U-22 | Unmount via `library-unmount-dialog` | files disappear; nothing deleted on disk |

### T3 — "Priya, the analyst" (base views, dashboards, inline editing)

| ID | Steps | Expected |
|---|---|---|
| U-23 | Open `Projects.base` | `base-preview`, tablist with 8+ tabs; each tab renders its part: `view-part-table/list/tiles/columns/calendar/figures/chart/crosstab` |
| U-24 | Unservable view tab | `base-view-tab-unservable-*` with the server's reason; other tabs unaffected |
| U-25 | Table: click `viewpart-cell-editor-trigger` on `status` (enum), `start` (date), `owner`'s name (text on person) | enum editor lists values; date editor; text editor; save → cell updates; oracle `GET /knowledge/records/{id}` |
| U-26 | Try editing `done_ratio`, `owner`, `budget`, `tags` | no trigger / refused: derived (FR-046), relation (FR-045), decimal & list not editable — record as expected scope |
| U-27 | Edit a cell, then Builder edits same record (Operator), then save the cell | `viewpart-cell-conflict` + `-retry`; retry succeeds |
| U-28 | Clear an editable enum to empty, then set it back | empty state editable (empty-editable gate), not one-way |
| U-29 | List view: same edits; board: cards under status columns move after status edit | `viewpart-board-column` membership changes |
| U-30 | Calendar by `start`; tiles; summary/trend/breakdown/chart/crosstab | numbers match Q-07 totals; visual sanity of chart |
| U-31 | Wikilinks in cells: first 40 rows resolve link state; row 41+ show resolved-or-unknown, never "unresolved" (WL-1 cap) — Builder creates 45 projects first | `ViewCellLink` states; no false "unresolved" past 40 |
| U-32 | Embedded dashboard in `Overview.md`: view label with different case and with double space | case-insensitive resolves; double space **not** normalised (D-series); `kb-base-embed-missing-view` / `-ambiguous-view` shown when applicable |
| U-33 | `view-truncated`, `view-problems`, `view-refusal` + `-remedy` on a view whose query exceeds limits (Builder sets `limit` past 200) | honest chrome, remedy text |

### T4 — "Jonas, the searcher" (search bar, find, coverage honesty)

| ID | Steps | Expected |
|---|---|---|
| U-34 | In the vault: `library-search-bar-input` "Zürich"; filters all/notes/records/views/attachments | hits typed by kind (`vault-search-*-hit`); `-complete-statement` or `-coverage-ratio` shown; `-clamped`/`-truncated` only when true |
| U-35 | Empty query; whitespace; 4 000-char query | no search; no search; refused or clamped, stated |
| U-36 | In a plain folder: same bar | files mode, no tabs, `file-search-match-count`; grep semantics (`.git`, `.library`, `.omnipus-vault` pruned) |
| U-37 | At virtual root | bar disabled |
| U-38 | Search for a record edited by U-25 seconds ago | new value found (index freshness across doors) |
| U-39 | Search for a note the Builder trashed | absent immediately; present again after restore |
| U-40 | View hit → opens `.base` at that tab (`-view-dialog` if needed) | correct tab selected |
| U-41 | During re-index (Builder creates 45 projects) | `-not-ready` / `knowledge-index-progress-ratio` shown, then completes |

### T5 — "Lena, the sceptic" (edge cases, security, isolation)

| ID | Steps | Expected |
|---|---|---|
| U-42 | Upload `evil.svg` with `<script>`; preview | inert (`<img>`), no dialog, no console error of execution |
| U-43 | Upload `page.html` that fetches `http://127.0.0.1:5177/api/v1/state`, posts a message, opens a popup | all blocked: CSP `connect-src 'none'`, frame isolated; expect `preview-isolation` behaviours; no token in any log line |
| U-44 | Copy the `/library-preview/<token>/…` URL into a new tab after 15 min; use `POST` on it | 404 (never 410); GET/HEAD only |
| U-45 | Open 9 previews quickly | ≤ 8 live tokens per session (oldest expires); UI recovers |
| U-46 | Embed `![[../outside/secret.md]]` via Builder, preview | refused with **no path echoed** |
| U-47 | Note transcluding itself; note transcluding a note that transcludes another | A→A renders once (E11); inner embed becomes a link **with a reason** (E8) |
| U-48 | 40 embeds in one note, scroll | ≤ 4 in flight; all mount; three PDFs → ≤ 2 workers (I9) |
| U-49 | Kill network mid-save (DevTools offline) | error shown, content preserved, retry works (EDGE-15) |
| U-50 | Unicode NFC vs NFD `café.png` embed | same file or both indeterminate, never one each (A10) |

### T6 — "Operator" (drives the Builder; cross-door checks)

| ID | Steps | Expected |
|---|---|---|
| X-01 | Every Builder write from Phase 1 is followed by a UI check by the relevant tester within 60 s | UI reflects agent writes without reload beyond a panel refresh |
| X-02 | Every UI write from Phase 2 (U-17, U-25, U-16) is followed by `knowledge_read`/`knowledge_find` by the Builder | agent sees UI writes, with the rotated version token |
| X-03 | Builder asked to "do everything the UI cannot": create a note, set a relation, set an integer, set a checkbox | all succeed via agent (founder rule: UI gaps acceptable, agent gaps are defects) |
| X-04 | Builder asked to embed a whole PDF (no page) and an `.mmd` | both land as links; the agent explains why (EMB-025, ADR-083 §15) |
| X-05 | Builder asked to run `records stamp-ids --dry-run` on the founder copy via `bash` | output lists nothing to stamp (already stamped 747) or the exact delta; `--vault` path never the real Obsidian vault |
| X-06 | Audit trail: after Phase 1, `grep` the audit log for `decision=allow actor=agent:<UAT Builder>` on knowledge tools | every write present (audit ON by default) |

---

## 3. Tester personas (given verbatim to each subagent)

Each tester subagent receives: the login, the selector sheet (Appendix A), its scenario rows, the evidence folder, the Playwright MCP tool names (`mcp__playwright__browser_navigate`, `browser_snapshot`, `browser_click`, `browser_type`, `browser_fill_form`, `browser_take_screenshot`, `browser_wait_for`, `browser_console_messages`, `browser_network_requests`), and these rules:

- You are a human user. Use the UI only. Read what is on screen from the screenshot; a selector existing is not a pass.
- Screenshot before and after every state change; name files `<id>-<n>-<what>.png`.
- Record console errors per scenario (`browser_console_messages`), WS reconnect warnings excluded.
- When something is not there, say what you expected to see and where you looked. Never guess a cause.
- Report per row: PASS / FAIL / GAP (expected limitation, cite it) / BLOCKED (why), evidence path, one sentence of observation.
- Never delete outside `UAT Vault` / `Second Vault` / `Q4`. Never touch the `kb` mount's files except read.

Personas: Nadia (reader, patient, reads every page), Marek (editor, impatient, double-clicks, hits Save twice, keeps two tabs), Priya (analyst, lives in tables, edits cells fast, checks totals against her own arithmetic), Jonas (searcher, types partial words, unicode, pastes long text), Lena (sceptic, tries what should not work), Operator (methodical, copies the Builder's tool-call cards into evidence).

---

## 4. Severity, exit criteria, defect register

Severity ladder (from `library-uat-plan.md`): **S1** data loss / silent clobber / security escape; **S2** feature unusable or wrong result with no error; **S3** wrong error, poor recovery, visual defect that misleads; **S4** cosmetic.

Exit: zero S1/S2 open; every S3 has a tracked issue; every GAP matches a documented limitation (Appendix B) — an undocumented gap is an S3.

Defect register: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/evidence/2026-09-13/DEFECTS.md`, one row per finding: id, scenario, actor, severity, steps, expected, observed, evidence path, suspected area (file), status.

---

## Appendix A — Selector sheet (verified on this branch)

- Entry: `sidebar-library-button` → `library-panel-docked`; pop-out `library-popout-button` → `/#/library?workspace=…&path=…`.
- Rows: `library-row-<path>`, menu `library-row-menu-<path>` (Open / Download / Rename / Move… / Copy… / Delete; mounts: Unmount), `library-mount-badge-<path>`, `library-hidden-badge-<path>`, `library-show-hidden-toggle`, `library-mounts-count`.
- Create: `library-create-menu-trigger` → `-new-folder`, `-upload` (`library-upload-input`), `-new-vault`, `-add-mount`, `-manage-mounts`; dialogs `library-new-folder-dialog-*`, `library-new-vault-dialog-*`, `library-rename-dialog-*`, `library-transfer-dialog-*`, `library-add-mount-dialog-*`, `library-mounts-dialog-*`, `library-delete-confirm`, `library-unmount-dialog`/`-confirm`.
- Preview shell: `library-preview-pane`, `-title`, `-close`; kinds: `library-image-preview`, `library-video-element`, `library-audio-element`, `library-html-preview-frame` (+ `-loading/-retry/-reload/-expired`, `library-preview-untrusted-boundary`), `library-pdf-preview` (+ `-pages`, `-page`, `-loading`, `-queued`, `-error`, `-retry`, `-mode-view/-mode-edit`, `-save`, `-add-signature`, signature dialog `library-pdf-signature-*`), `base-preview` (+ `-tablist`, `base-view-tab-<name>`, `base-view-tab-unservable-<name>`, `-no-views`, `-raw`, `-download`, `-embed-caption`, `-link-graph-degraded`), `library-download-card`.
- Text editing: `library-preview-mode-view/-mode-edit`, `library-preview-save`, `library-code-editor`.
- View parts: `view-parts`, `view-part-<table|list|tiles|columns|calendar|figures|chart|crosstab>`, `view-empty`, `view-truncated`, `view-problems`, `view-refusal`, `view-refusal-remedy`, `viewpart-board-column`, `viewpart-board-card`, cell edit `viewpart-cell-editor-trigger` (`aria-label="Edit <property>"`), `viewpart-cell-editor-<type>`, `viewpart-cell-editor-enum`, `viewpart-cell-error`, `viewpart-cell-conflict`, `viewpart-cell-conflict-retry`.
- Markdown/embeds: `kb-transclusion`, `-empty`, `-not-found`, `kb-base-embed-missing-view`, `-ambiguous-view`, `kb-embed-mount-loading/-error/-missing`, `lazy-embed-mount`, `unmounted-embeds-notice`, `markdown-link`, `video-embed`, query fence `kb-query-fence-*`.
- Knowledge reader: `knowledge-panel`, `knowledge-reader`, `knowledge-outline-*`, `knowledge-backlinks-*`, `knowledge-create-note-unavailable`, `knowledge-index-progress-ratio`, `knowledge-state-index-failed`.
- Search: `library-search-bar-input`, `-clear`, `-results`, `-empty`, `library-search-filter-<all|notes|records|views|attachments>`, `-complete-statement`, `-coverage-ratio`, `-clamped`, `-truncated`, `-not-ready`, `-view-dialog`, `file-search-match-count`, `vault-search-<note|record|view|attachment>-hit`.
- Login form: `#login-username`, `#login-password` at `/#/login`. Scripted e2e against this instance needs `OMNIPUS_URL=http://127.0.0.1:5177`.

## Appendix B — Documented limitations (a GAP result must cite one of these)

| Area | Limitation | Source |
|---|---|---|
| Create menu | no "New note"; `knowledge-create-note` unwired | `LibraryCreateMenu.tsx`, `KnowledgeEmptyState.tsx` |
| Inline editing | only enum, date, text; no integer/decimal/checkbox; relations and derived refused | `RecordFieldEditor.tsx` FR-045/046 |
| Embeds | `.mmd` embed permanently a link; whole-document PDF a link (only `#page=N` mounts); html/text/other link-only; `page` refused on `op: embed` | ADR-083 §15, EMB-025, `knowledge_edit.go` |
| Transclusion | one level; self-transclusion once | ADR-083 D5 |
| Base views | wikilink state resolved only for the first 40 rows | WL-1 |
| PDF | no PKI signatures, no XFA, links inert | `LibraryPdfPreview.tsx` |
| Body editing | explicit Save only, no autosave; two tabs last-write-wins with 409 handling | `useLibraryFileEditor.ts`, EDGE-12 |
| CSP | non-loopback IPv6 origin collapses to `'self'` | HP-2 |
| Query | limit ≤ 200, hops ≤ 2, group levels ≤ 2, filter leaves ≤ 64, depth ≤ 8 | `knowledgefind/request.go` |
| Vault import | `records import-obsidian` is CLI-only, never an agent tool | FR-103 |
| Windows | no cross-process locking | ADR-083 §7.4 |

## Appendix C — Runbook for the harness

1. Operator runs Section 0. 2. Operator drives Phase 1 (B-01…B-33, Q-01…Q-18) sequentially, saving each tool-call card. 3. When B-25 is done, T1 and T3 start in parallel; when B-23 is done, T5 starts; T2 and T4 start after B-30. Each tester is one subagent with one persona and its rows. 4. Operator runs X-01…X-06 interleaved. 5. Orchestrator merges the six reports into `DEFECTS.md` and the summary table, and hands both to the founder.
