# Unified Library Search & Grep Engine — Implementation Spec

- **Source ADR:** `docs/internal/architecture/ADR-081-unified-library-search-and-grep-engine.md`
  (founder-ratified 2026-09-07; renumbered from draft "ADR-069").
- **Codebase:** branch `integrate/library-improvements-v0.1.1`. Pin lineage (all on one
  first-parent line, intentionally different): ADR validated @ `f37346338`; spec draft
  @ `f23f18ffb`; round-1 review verified @ `6a4492fb3`.
- **Status:** REVISED after grill round 1 (all 27 findings addressed — see
  `unified-search-and-grep-spec-review-round1.md`); awaiting round 2.
- **Founder rulings recorded this revision:** (a) the grep tool must be **best-in-class
  for agents** — the performance levers and agent ergonomics below are MUSTs, not
  aspirations; (b) grep policy roster — founder ruling: grep is available to **ALL agents,
  explicitly including the Worker** ("who potentially work with code"); every seed
  carries an explicit `allow` entry (A4 closed, see FR-009).
- **Spec discipline:** §§1–5 behavioral; implementation detail from §6 (TDD) onward.

---

## 1. Discovery summary (from ADR-081 + founder rulings)

- **Actors:** (a) a person using the Library UI; (b) an agent working through tools;
  (c) the operator governing tool policy.
- **Problem:** search was built twice for vaults (two bars, two endpoints, one index);
  plain folders and mounts have no search at all; agents have no recursive content
  grep — and the founder's bar for the fix is a **best-in-class agent grep**, not a
  minimum viable one.
- **Scope (3 workstreams; stage A before B — A is independently shippable, OBS-002):**
  - **A — Knowledge-search consolidation:** one human search bar over the existing
    shared knowledge engine; retire the human-only duplicate path; preserve **all** of
    its honesty signals and its **attachment search** (round-1 CRIT-001).
  - **B — File-search/grep engine:** a new pure-Go, no-index, bounded search over file
    names/paths and text-file content, across workspace folders AND mounts, inside the
    Library's confinement boundary.
  - **C — Surfaces:** the one bar renders in every workspace location (kinds switch by
    context); a new agent `grep` tool over the same engine; REST endpoint for the bar.
- **Out of scope (explicit):** trigram/persistent indexes; ripgrep as
  binary/CGo (ADR-081 D6); PDF/office content extraction; ⌘K palette; Obsidian import;
  progressive/streamed search results (v1 returns one bounded response — recorded
  choice, MIN-010); operator-config keys for bounds (per-request override only,
  OBS-004); an ignored-entries surfacing toggle (A2); a unified agent search façade
  (O3 — agents compose `knowledge_find` + `grep` in v1).
- **Constraints:** Hard Constraints #1–#4; contract-first #8; sandbox confinement;
  ADR-071 tier + ADR-077 policy governance (ADR-081 D11).
- **Resolved sub-decisions:**
  - **O1 — dependencies:** `charlievieth/fastwalk`, `bmatcuk/doublestar/v4`,
    `grafana/regexp`, **`sabhiram/go-gitignore`** (MIN-004 closes the "hand-rolled"
    option: full gitignore semantics are a bug farm); `wasilibs/go-re2` only on
    measured need.
  - **O2 — v1 scope:** name/path match AND bounded text-content match both ship.
  - **O3 — agent surface:** dedicated `grep` tool; unified façade deferred.
  - **A4 (closed, founder 2026-09-07):** grep is allowed for ALL agents — explicit
    `allow` seeds everywhere, expressly including the Worker (code work is its job);
    full roster in FR-009.

## 2. Existing codebase context (validated 2026-09-07; GitNexus N/A for this worktree — direct verification)

| Symbol / surface | Role in this feature | Verified fact |
|---|---|---|
| `pkg/records/knowledgefind.Find` + `pkg/vaultprops.OpenFindEnv` | the shared knowledge engine (kept) | called by BOTH the agent tool (`pkg/vaultprops/find_tool.go`, ADR-068 D15.3 adapter, registered via `pkg/agent/knowledge_tools.go::registerKnowledgeTools`) and the human endpoint `pkg/gateway/rest_knowledge_find.go`; the engine also defines `KindAttachment` (`pkg/records/knowledgefind/request.go:70`) |
| `POST /library/{ws}/knowledge/find` | the surviving human knowledge endpoint | exists; today queries note+record kinds only (`rest_knowledge_find.go:165,237`) — **attachment coverage is added by this spec** (CRIT-001) |
| `POST /library/{ws}/knowledge/search` (`rest_knowledge.go:151` case label, `:156` dispatch, `:461` handler) | retired and removed | no agent tool consumes it (verified); **its hit kinds include `attachment`** (`KnowledgeSearchHit.yaml`) |
| `src/components/library/search/LibrarySearchBar.tsx` + `useVaultSearch.ts` | becomes the ONE bar | exists; segmented filter; `N+` badges; disabled-outside-vault state incl. at the Library virtual root (`LibrarySearchBar.tsx:81`) |
| `src/components/library/knowledge/KnowledgeSearch.tsx` + `useKnowledgeSearch.ts` (+ `KnowledgeSearch.attachment.test.tsx`) | deleted after their behaviors are ported | mounted by `KnowledgePanel.tsx:333`; **`KnowledgePanel`'s `searchFn` prop is typed off the component** (`KnowledgePanel.tsx:198`) — removal is a prop-API change (MIN-001c) |
| `src/lib/api.ts::searchKnowledge` + `src/lib/api.knowledge.test.ts` | retired client + its direct test file | test file exercises `searchKnowledge` at `:22,156` (MIN-001b) |
| `pkg/gateway/inboundschemas/KnowledgeSearch*.yaml` | vanish via regen | auto-synced by `scripts/gen-contracts.sh` Step 5; drift-gated by Makefile — do not hand-delete (MIN-001a) |
| `pkg/library/root.go::OpenRoot` / `Root.List(rel, includeHidden)` (`entries.go:19-25`) | confinement boundary + the hidden-file rule (a caller parameter, dot-prefix) | `List` is single-level; no recursive walk exists |
| `pkg/tools/manifest.go::ToolManifestTier` | tier governance | absent-from-lists ⇒ Lazy + SearchOnly; grep gets its **own sibling pin test** (MIN-002), not a graft onto the knowledge-family pin |
| `pkg/gateway/gateway.go::buildKnownBuiltinToolNames` (`:1261-1286`, knowledge union `:1321`) + `registerKnowledgeBuiltinMetadata` (`knowledge_tools_wire.go:112`) | coverage universe + metadata catalog | the knowledge family needed BOTH sites; grep needs the equivalent two touches (MIN-001d) |
| `pkg/coreagent/catalog_count_test.go::catalogSizeToday` | load-bearing pin | 101 today ⇒ 102 |
| `pkg/config/defaults.go` ceiling + `pkg/coreagent/core.go` seeds (Worker deny precedent `core.go:854`) + `tool_policy_catalog_drift.go` backfill | ADR-077 governance incl. upgrade path | drift backfill writes explicit per-agent entries for pre-existing agents on upgrade |
| `pkg/tools/shell.go::truncateOutput` (`:589,1728`, 64,000 chars) | the in-repo output-cap precedent the grep tool follows | CRIT-003 |
| `allowKnowledgeRetrieval` limiter + 429 contract | the rate-limit precedent the new endpoint follows | CRIT-002 |
| e2e `tests/e2e/shards.json` + `scripts/e2e-shards.sh check` | new spec must be assigned | 65/65 today |

**Impact assessment (manual):**

| Modified surface | Risk | Direct dependents |
|---|---|---|
| `VaultSearchResponse` (additive honesty fields + `attachments[]`) | MEDIUM | `rest_knowledge_find.go` + tests, generated Go/TS/zod, `useVaultSearch`, `LibrarySearchBar` |
| `KnowledgePanel.tsx` (unmount + `searchFn` prop retype) | MEDIUM | `KnowledgePanel.test.tsx`, deleted `KnowledgeSearch*` suites (behaviors ported first) |
| `rest_knowledge.go` (remove search case) + contracts + client + inboundschemas (regen) + `api.knowledge.test.ts` | MEDIUM | US-5 inventory |
| `LibraryExplorer.tsx` (bar everywhere) | MEDIUM | its 30+ SPA suites |
| tool catalog/ceiling/seeds/backfill (+ gateway wiring sites) | HIGH (boot-abort class) | catalog pin, seed tests, drift tests, `TestBoot_ZeroToolPolicyGaps` |

**Reference patterns:** no `docs/reference/go-implementation/` in this repo — N/A. The
endpoint pattern is `rest_knowledge_find.go`; the tool adapter pattern is
`vaultprops.FindTool`; the output-cap pattern is `shell.go::truncateOutput`.

---

## 3. User stories & acceptance criteria

### US-1 (P0) — One knowledge search in a vault (notes, records, views, **attachments**)
A person inside a vault sees exactly **one** search bar covering everything the two old
surfaces covered together — notes, records, views, **and attachments by filename** —
with ALL the honesty the retired bar had.
**Why P0:** shipped duplication is a live defect; CRIT-001 makes attachment parity a
release condition.
**Independent test:** open a vault; count search inputs (=1); search an attachment's
filename → hit; force index-building → partial notice with counts.
**Acceptance scenarios:**
1. **Given** a vault with an up-to-date index, **When** the person searches a term
   matching notes, records, views and an attachment filename, **Then** results appear
   grouped by kind — including an Attachments group — and no second search input
   exists.
2. **Given** a vault whose index is still building, **When** the person searches,
   **Then** a partial-results notice appears carrying the server's own statement,
   including an "X of Y" coverage claim when the total is known and an honest
   "X so far" when it is not.
3. **Given** more note matches than the per-kind limit, **Then** the notes count reads
   as a lower bound ("N+"), **and** when the person's requested limit was clamped by
   the server, the response says so (clamped + the limit actually applied).
4. **Given** a note hit whose excerpt cannot be produced, **Then** the hit still
   renders (title/path) with an explicit excerpt-unavailable marker — never silently
   dropped, never a fabricated excerpt.
5. **Given** any prior UI state, **Then** the old notes box is absent and the UI can
   no longer call the retired endpoint.

### US-2 (P0) — File search in plain folders and mounts
As before (name/path always; bounded text content for text files; mounts bounded and
honest). **Additions this revision:** the search is literal-with-smart-case from the
human bar (MAJ-003); a lost mount/root mid-search is a visible error, never a quiet
empty result (MIN-007).
**Acceptance scenarios:**
1. **Given** a plain folder containing `Q3 report.md`, **When** the person searches
   "report", **Then** the file appears as a name match with its path.
2. **Given** a text file containing "meeting", **When** the person searches "meeting",
   **Then** the file appears with a bounded excerpt around the match.
3. **Given** a mounted folder larger than the scan bounds, **When** the person
   searches, **Then** partial results plus an explicit stopped-early notice appear
   within the interactive deadline, and the result is never presented as complete.
4. **Given** a binary file whose NAME matches, **Then** it appears as a name match and
   its bytes are never content-scanned.
5. **Given** the confinement boundary, **Then** nothing outside the workspace work-tree
   or its mounts is ever read or listed.
6. **Given** the search's walk root or a mount root becomes unreadable mid-search
   (unplugged disk, dropped share), **Then** the person sees a request-level error or
   a truncated result whose reason names the lost root — never a bare "0 matches".
7. **Given** the person types `(` or other regex metacharacters in the bar, **Then**
   the search treats them literally and returns literal matches — no regex parse error
   ever surfaces from the human bar.

### US-3 (P0) — Best-in-class agent grep tool
An agent gets a `grep` tool that feels like ripgrep: literal or full RE2 regex,
smart-case, include/exclude globs, context lines, per-file and total match caps, line
numbers, and structured results — over its **own** workspace root and mounts only,
under hard bounds, with explicit truncation.
**Why P0:** the founder's ruling — best-in-class for agents, first prototype of that
bar.
**Independent test:** in a real agent session, grep a seeded workspace for a regex with
2 lines of context; verify structured hits, context lines, and `truncated:true` when a
cap is engineered.
**Acceptance scenarios:**
1. **Given** a workspace file containing "needle", **When** the agent greps
   `needle`, **Then** the result lists path, line number and the matching line.
2. **Given** `regex:true` and pattern `ne{2}dle`, **Then** matching lines return (full
   RE2 support).
3. **Given** a pathological pattern (64 KiB alternation bomb), **Then** the call
   completes within its deadline — reject-at-compile or bounded scan, never a hang.
4. **Given** `context_lines: 2`, **Then** each hit carries up to 2 lines before and
   after the match, clearly distinguished from the matching line.
5. **Given** an agent whose policy denies `grep`, **Then** the standard policy refusal
   is returned and audited.
6. **Given** more matches than the cap, **Then** the bounded set returns plus
   `truncated:true` and the reason.
7. **Given** any scope argument, **Then** the tool can only reach the calling agent's
   own workspace root and its mounts — there is no cross-workspace parameter.
8. **Given** a result set whose serialized size exceeds the tool output cap, **Then**
   the output is truncated at the cap with an explicit marker (shell-tool precedent),
   and the agent is told how to narrow the query.

### US-4 (P1) — One bar, every workspace location, kind-aware
The bar renders in every **workspace** location. In a vault it offers
Notes/Records/Views/Attachments; in a plain folder or mount it offers Files. At the
Library virtual root (the all-workspaces node) the bar renders in its existing
disabled state with its hint — a search needs a workspace (MAJ-005).
**Acceptance scenarios:**
1. **Given** any folder inside a workspace, **Then** the bar is present and enabled.
2. **Given** the Library virtual root, **Then** the bar is present but disabled with
   its explanatory placeholder.
3. **Given** the person stands in a plain folder, **Then** offered kinds are
   file-oriented; in a vault, knowledge kinds.
4. **Given** an active query, **When** the person navigates elsewhere, **Then** results
   re-scope or clear (no stale results), and an empty query restores the tree.

### US-5 (P1) — Endpoint retirement is clean
Route, handler, wire types (`KnowledgeSearchRequest/Response/Hit` + their
`inboundschemas/` copies via regen), the `searchKnowledge` client, its direct test file
`api.knowledge.test.ts`, and the `KnowledgePanel.searchFn` prop typing are all removed;
the path returns 404; repo-wide search finds no live references.
**Acceptance scenarios:** (1) retired path ⇒ 404; (2) SPA bundle audit ⇒ no caller;
(3) `verify-contracts` green after regen (inboundschemas copies gone via Step 5 sync,
not hand-deletion).

### Edge cases (behavioral)
- Empty/whitespace query ⇒ no search; tree unchanged.
- Unicode query (CJK/Cyrillic/emoji) ⇒ codepoint-correct matches; excerpts valid UTF-8.
- Symlinks ⇒ never followed across the boundary; the entry itself may name-match.
- File deleted between walk and read ⇒ skipped, counted in the response's problem
  stats.
- Unreadable file ⇒ skipped + counted; whole-root loss is MIN-007's request-level
  error (US-2 AS-6), never a per-file skip.
- Extremely long lines ⇒ excerpt windowed to the excerpt cap.
- Concurrent searches ⇒ independent; UI discards out-of-order responses AND the
  server cancels abandoned walks (CRIT-002).
- `.gitignore` present (any tree, git repo or not) ⇒ ignored subtrees pruned from
  BOTH name and content search in v1 (no surfacing toggle); nested `.gitignore`
  files and `!` negations honored (library semantics); the pruned count is visible in
  the response stats so hidden-by-ignore is observable (MIN-003/MIN-004).
- Hidden files ⇒ governed by an explicit `include_hidden` request flag, default
  false — which is also what keeps `.git/`, `.library/`, `.omnipus-vault/` out of
  content scans by default (a stated property, not an accident) (MIN-008).

---

## 4. Behavioral contract (quick reference)

- Vault ⇒ knowledge engine: Notes/Records/Views/Attachments + full honesty signals.
- Plain folder/mount ⇒ file engine: names always, text content bounded; literal
  smart-case from the bar; regex only where explicitly requested.
- Any bound reached ⇒ `truncated` + machine-readable reason; UI/tool says so.
- Root/mount lost mid-walk ⇒ visible error, never quiet empty.
- Query cleared ⇒ tree restored. Client gone ⇒ server walk cancelled.
- Agent `grep` ⇒ structured, context-capable, capped output, own-workspace-only,
  policy-governed, audited.
- Retired endpoint ⇒ 404.

### Explicit non-behaviors
(unchanged from draft, plus:)
- The engine must not expose bounds as operator config keys in v1 (per-request
  override only) — guarding against unnecessary configurability (OBS-004).
- The human bar must never surface a regex parse error — it does not speak regex
  (MAJ-003).
- The grep tool must not accept any cross-workspace scope (MAJ-006).
- The consolidation must not regress result ORDERING: knowledge hits keep the
  engine's relevance order, never file-path order (OBS-003).
- No progressive/streamed delivery in v1 (recorded, MIN-010).

### Machine-verifiable constraints
- **MV-1** File-search REST error taxonomy: 400 invalid body/regex (agent/REST regex
  param only), **401 unauthenticated**, 403 outside-root, 404 unknown workspace,
  **429 rate-limited/busy** — same taxonomy style as sibling Library routes
  (MIN-005, CRIT-002).
- **MV-2** Retired endpoint path ⇒ 404.
- **MV-3** Bounds (server defaults; per-request downward override; clamped upward;
  NOT operator-configurable in v1):
  max files visited 50,000 · max bytes content-scanned 256 MiB · max matches 1,000 ·
  max depth 32 · per-file content cap 4 MiB (a per-file skip-remainder, counted in
  stats — not a request-level truncation) · wall-clock deadline 10 s default with the
  SPA passing a 3 s interactive override (MIN-010) · **max response payload 1 MiB**
  (CRIT-003). Request-level `truncated_reason` enum:
  `max_files | max_bytes | max_matches | max_depth | deadline | max_output |
  root_lost` (MAJ-004, MIN-007).
- **MV-4** RE2 semantics; a 64 KiB pathological pattern is rejected at compile or
  completes within the deadline; never super-linear blowup.
- **MV-5** Response arrays always `[]`, never null — asserted on the zero-hit handler
  path (MIN-005).
- **MV-6** Excerpts: valid UTF-8, each ≤ 512 bytes around the match (CRIT-003).
- **MV-7** `catalogSizeToday` = 102; `ToolManifestTier("grep")` = Lazy + SearchOnly,
  pinned by its **own** test (MIN-002).
- **MV-8** Policy: shipped ceiling entry `allow`; explicit seeds — Jim/Mia/Ava/Ray
  `allow`, **Worker `allow`** (founder ruling: workers do code work — explicit entry,
  never silent ceiling inheritance, closing MAJ-002's trap), system agents `allow`
  (read-only, confined; no exception carved); fresh install validates with zero gaps
  AND the upgrade path is pinned: the drift backfill writes an explicit per-agent
  `grep` entry on pre-existing agents (test asserts it).
- **MV-9** VaultSearch honesty additions (all additive; old zod clients stay valid):
  notes — searched-count, total-known (both optional, omitted when unknown, never
  fabricated), capped-at-limit boolean, server-authored coverage `statement`,
  `limit_clamped` + `limit_requested`; per-note-hit `excerpt_unavailable` boolean;
  plus the new `attachments[]` hit group (path, name; filename-match kind)
  (MAJ-001, CRIT-001).
- **MV-10** Exactly one search input per Library view (DOM-assertable).
- **MV-11** Concurrency/cancellation (CRIT-002): the endpoint sits behind the
  knowledge-retrieval-class rate limiter (429 on excess); at most 2 concurrent file
  walks per gateway (excess ⇒ 429 busy, honest message); a client disconnect cancels
  the server-side walk via request context (observable: walk goroutines exit —
  test-asserted through the engine's context hook).
- **MV-12** Grep tool output: serialized result capped at 64,000 chars with an
  explicit truncation marker (shell-tool `truncateOutput` precedent); `truncated_reason
  = max_output` (CRIT-003).
- **MV-13** Grep performance levers are present and test-observable (founder's
  best-in-class ruling): literal fast path (no regex engine on plain strings),
  required-literal prefilter before regex, parallel scanning workers, zero-alloc
  line scanning on the hot path (benchmarked, see SC-003/SC-008).

### Integration boundaries
(as draft, plus:) **Rate limiter** — in: request; contract: allow/deny per the
existing retrieval limiter; failure: 429 with the sibling routes' error shape.
**Agent turn context** — grep results enter the turn transcript; the 64,000-char cap
bounds their windowTrim cost (CRIT-003 rationale).

---

## 5. BDD scenarios

(Additions/changes from draft marked ★; unchanged scenarios kept as in draft.)

```gherkin
★ Scenario: attachment filename search survives consolidation   # Happy Path
  Traces to: US-1, AS-1 (CRIT-001)
  Given a vault containing an attachment "contract-final.pdf"
  When the person searches "contract"
  Then an Attachments group lists "contract-final.pdf"

★ Scenario: clamped limit is disclosed                          # Alternate
  Traces to: US-1, AS-3 (MAJ-001)
  Given a request asking for a limit above the server cap
  When results render
  Then the response says the limit was clamped and names the applied limit

★ Scenario: excerpt-unavailable hit still renders               # Edge
  Traces to: US-1, AS-4 (MAJ-001)
  Given a note hit whose on-disk content moved since indexing
  When results render
  Then the hit shows title and path with an excerpt-unavailable marker

★ Scenario: human bar treats metacharacters literally           # Happy Path
  Traces to: US-2, AS-7 (MAJ-003)
  Given a file containing the literal text "f(x)"
  When the person searches "f(x)"
  Then the file matches
  And no error is shown

★ Scenario: lost mount root is a visible error                  # Error Path
  Traces to: US-2, AS-6 (MIN-007)
  Given a search running over a mount
  When the mount's root becomes unreadable mid-walk
  Then the response is an error or truncated with reason "root_lost"
  And it is never an unqualified empty result

★ Scenario: abandoned search is cancelled server-side           # Edge
  Traces to: MV-11 (CRIT-002)
  Given a long-running file search
  When the client disconnects
  Then the server-side walk stops before its deadline

★ Scenario: concurrent walk cap answers busy                    # Error Path
  Traces to: MV-11 (CRIT-002)
  Given two file searches already running on the gateway
  When a third arrives
  Then it receives the busy/rate-limited response, not a queued hang

★ Scenario: grep returns context lines                          # Happy Path
  Traces to: US-3, AS-4
  Given a file with a match on line 10
  When the agent greps with context_lines 2
  Then the hit carries lines 8-9 and 11-12 as context, distinguished from line 10

★ Scenario: grep output cap truncates with a marker             # Edge
  Traces to: US-3, AS-8 (CRIT-003)
  Given a query matching megabytes of lines
  When the agent greps
  Then the serialized output is at most the tool cap
  And it ends with an explicit truncation marker and a narrowing hint

★ Scenario: grep cannot leave its own workspace                 # Error Path
  Traces to: US-3, AS-7 (MAJ-006)
  Given two workspaces exist
  When the calling agent greps any scope expression
  Then results only ever come from its own workspace root and mounts

Scenario Outline: every request-level bound truncates honestly  # Edge (revised)
  Traces to: US-2 AS-3; US-3 AS-6 (MAJ-004)
  Given a tree engineered to exceed the <bound> bound (bounds injected, MAJ-008)
  When the search runs
  Then it returns within the deadline
    And truncated is true with reason "<reason>"
  Examples:
    | bound            | reason      |
    | files visited    | max_files   |
    | bytes scanned    | max_bytes   |
    | matches          | max_matches |
    | depth            | max_depth   |
    | wall clock       | deadline    |
    | response payload | max_output  |

★ Scenario: per-file cap is a counted skip, not a truncation    # Edge
  Traces to: MV-3 (MAJ-004)
  Given a 5 MiB text file whose match sits beyond the 4 MiB per-file cap
  When the search runs
  Then the file's remainder is skipped and the skip is visible in stats
  And the request-level truncated flag is not set by this alone
```

(Plus, unchanged from draft: one-bar grouped results; index-building coverage; capped
"+"; retired 404; name match; content match + UTF-8; binary name-only; confinement
symlink; unreadable-file skip; RE2 pathological; policy deny; bar-present variants —
with US-4 AS-2's Library-root disabled state replacing the draft's root-enabled claim
(MAJ-005); stale-results navigation.)

---

## 6. TDD plan

Engine package **`pkg/filegrep`**; REST **`POST /api/v1/library/{workspace_id}/files/search`**
(`case "files"` in `rest_library.go`); agent tool **`grep`** via a `tools.Tool` adapter
(pattern: `vaultprops.FindTool`), plus the two gateway governance touches
(`buildKnownBuiltinToolNames` union; metadata-catalog registration à la
`registerKnowledgeBuiltinMetadata`) (MIN-001d).

**Wire contract sketch (MAJ-004; final shapes live in `contracts/`, Constraint #8):**
- `FileSearchRequest`: `path` (scope, workspace-relative, optional ⇒ root),
  `query` (required), `regex` (bool, default false — bar never sets it),
  `include_hidden` (bool, default false), `include_globs[]`, `exclude_globs[]`,
  `limits` (optional per-request downward overrides: files, bytes, matches, depth,
  deadline_ms), `context_lines` (0–5, tool/REST both).
- `FileSearchHit`: `path`, `match_kind` (`name` | `content`), `line` (content only),
  `excerpt` (≤512B, optional), `context_before[]`/`context_after[]` (when requested).
- `FileSearchResponse`: `hits[]` (always present), `truncated` (bool),
  `truncated_reason?` (enum per MV-3), `stats` { files_visited, bytes_scanned,
  files_skipped_problems, files_pruned_ignored, files_skipped_per_file_cap }.
- `VaultSearchResponse` additions per MV-9 (incl. `attachments[]`).

| Order | Test | Level | Traces to | Notes |
|---|---|---|---|---|
| 1 | `TestFileGrep_LiteralFastPath` | Unit | US-3 AS-1, MV-13 | asserts the regex engine is NOT invoked for plain strings (seam/counter) |
| 2 | `TestFileGrep_RE2AndSmartCase` | Unit | US-3 AS-2 | + smart-case FR |
| 3 | `TestFileGrep_LiteralPrefilterEquivalence` | Unit | MV-13 | prefiltered == plain-scan result set |
| 4 | `TestFileGrep_BinarySkipNULHeuristic` | Unit | US-2 AS-4 | |
| 5 | `TestFileGrep_BoundsMatrix` | Unit | bounds outline | **bounds injected via an options struct** (MAJ-008); all 6 request-level reasons + per-file-cap-as-skip |
| 6 | `TestFileGrep_DeadlineAndContextCancel` | Unit | MV-11 | deadline stops walk; context cancel stops walk (server-side cancellation seam) |
| 7 | `TestFileGrep_UTF8SafeExcerpts` + fuzz seed | Unit | MV-6 | + 512B excerpt cap |
| 8 | `TestFileGrep_GitignoreSemantics` | Unit | MIN-003/004 | nested files, `!` negation, non-git tree, pruned-count in stats |
| 9 | `TestFileGrep_GlobIncludeExclude` | Unit | US-3 | doublestar |
| 10 | `TestFileGrep_SymlinkConfinement` | Integration | US-2 AS-5 | |
| 11 | `TestFileGrep_UnreadableSkippedCounted` | Integration | edge | **via injectable FS-error seam (and a dangling-symlink variant)** — never chmod-000, which is void under root CI (MAJ-007) |
| 12 | `TestFileGrep_RootLostMidWalk` | Integration | US-2 AS-6 | root removed after walk start ⇒ error/`root_lost` (MIN-007) |
| 13 | `TestFileGrep_ParallelScanDeterministicSet` | Unit | engine | + documented ordering rule |
| 14 | `TestFileGrep_PathologicalPattern64KiB` | Unit | MV-4 | (MIN-006) |
| 15 | `TestFileGrep_ContextLines` | Unit | US-3 AS-4 | before/after windows, file-boundary clamps |
| 16 | `TestLibraryFilesSearch_HandlerHappy` | Integration | US-2 | + zero-hit `[]`-not-null assert (MIN-005) |
| 17 | `TestLibraryFilesSearch_ErrTaxonomy` | Integration | MV-1 | 400/**401**/403/404/**429** |
| 18 | `TestLibraryFilesSearch_ConcurrencyCapAndCancel` | Integration | MV-11 | 3rd concurrent ⇒ 429; disconnect cancels (CRIT-002) |
| 19 | `TestLibraryFilesSearch_MountScope` | Integration | US-2 AS-3 | injected bounds |
| 20 | `TestGrepTool_ExecuteAndPolicy` | Integration | US-3 AS-1/5 | |
| 21 | `TestGrepTool_OwnWorkspaceOnly` | Integration | US-3 AS-7 | two workspaces; no crossover (MAJ-006) |
| 22 | `TestGrepTool_OutputCapTruncates` | Integration | US-3 AS-8, MV-12 | 64,000-char cap + marker (CRIT-003) |
| 23 | `TestVisibility_GrepIsSearchOnly` | Unit | MV-7 | **own sibling pin test** (MIN-002) |
| 24 | `TestCatalog_SizeIsPinned` 101→102 | Unit | MV-7 | documented procedure |
| 25 | `TestGrep_PolicyCeilingSeedsAndDriftBackfill` | Unit | MV-8 | roster: explicit allow for ALL agents incl. Worker + upgrade backfill assert (MAJ-002) |
| 26 | `TestVaultSearch_HonestyAndAttachments` | Integration | MV-9, US-1 | additive compat + attachments group + clamp disclosure + excerpt_unavailable (CRIT-001, MAJ-001) |
| 27 | `TestKnowledgeSearchRouteRetired404` | Integration | US-5 | |
| 28 | `useVaultSearch` honesty-port tests | vitest | US-1 AS-2/3/4 | ports ALL retired signals incl. statement/clamped/excerpt_unavailable + attachment kind |
| 29 | `LibrarySearchBar` context/kind tests | vitest | US-4 | incl. Library-root disabled state (MAJ-005) + literal-metacharacter case (MAJ-003) |
| 30 | `KnowledgePanel` prop-retype + no-mount tests | vitest | US-5 | `searchFn` prop removal (MIN-001c) |
| 31 | Files-kind UI tests | vitest | US-2 | truncated banner incl. `root_lost` wording |
| 32 | e2e `library-file-search.spec.ts` | E2E | US-2/US-4 | assigned in shards.json (checker 66/66) |
| 33 | e2e agent-grep flow | E2E | US-3 | real turn incl. context lines |
| 34 | `BenchmarkFileGrep_*` (literal, regex, prefilter) | Bench | MV-13, SC-008 | recorded (non-gating), tests/perf |

**Test datasets:** DS-1 (pattern matrix — adds `f(x)` literal row, smart-case rows);
DS-2 (bounds — **all via injected options**, incl. max_output and per-file-cap rows);
DS-3 (walker hygiene — adds nested-gitignore/negation rows, root-lost row,
include_hidden on/off rows); DS-4 (consolidation honesty — adds statement,
clamped, excerpt_unavailable, attachment rows).

**Regression requirements:** as draft, plus: the ported honesty coverage must be
review-diffed against the deleted `KnowledgeSearch*`/`useKnowledgeSearch` suites
(checklist: every behavioral assert in the deleted suites either ported or explicitly
retired with a reason); `api.knowledge.test.ts` deleted with its client; ordering
regression guarded by an engine-relevance-order assert (OBS-003).

---

## 7. Requirements & success criteria

**Functional requirements** (delta from draft)
- **FR-001..FR-015** as draft, with amendments:
  - FR-002 adds: …including **attachment filename search** and the full honesty set
    (statement, X-of-Y, unknown-total, capped, clamped+requested limit,
    excerpt-unavailable).
  - FR-007 now: prune `.gitignore`d subtrees from **both name and content** search
    (nested files + negations honored, git-ness irrelevant, pruned count observable);
    no surfacing toggle in v1.
  - FR-009 roster (founder 2026-09-07, revised same day): ceiling `allow`; explicit
    `allow` for EVERY seeded agent — Jim/Mia/Ava/Ray, the Worker (code work is its
    job), and system agents; upgrade path via drift backfill. No agent's posture is
    left to silent ceiling inheritance.
- **FR-016** The human bar MUST treat input as a literal with smart-case; regex MUST
  only be reachable via the explicit request flag (REST) / tool argument.
- **FR-017** The endpoint MUST be rate-limited (retrieval-limiter class), MUST cap
  concurrent walks (2/gateway, 429 busy), and MUST cancel the walk on client
  disconnect.
- **FR-018** Responses MUST be size-bounded: 512-byte excerpts, 1 MiB REST payload
  cap, 64,000-char tool output cap — each truncation explicit.
- **FR-019** The grep tool MUST support context lines (0–5), per-file/total match
  caps, glob filters, line numbers, and structured output — the best-in-class agent
  surface the founder ruled.
- **FR-020** The grep tool MUST be scoped to the calling agent's own workspace root
  and mounts; no cross-workspace access exists.
- **FR-021** A mid-walk loss of the walk/mount root MUST surface as an error or
  `root_lost` truncation, never an unqualified empty result.
- **FR-022** MUST-level performance levers: literal fast path, required-literal
  prefilter, parallel scan, zero-alloc hot path (bench-observed).

**Success criteria** (delta)
- SC-001, SC-002, SC-004..SC-007 as draft (SC-005 now includes the new pins).
- **SC-003 (revised, non-gating):** grep of a 10k-file / ≤64 MiB synthetic tree
  measured and RECORDED in tests/perf on the worker; target ≤ 2 s solo; regression
  tracked, not build-failing (MAJ-009).
- **SC-008 (new):** benchmarks show the literal fast path ≥ 5× the regex path on
  plain-string queries over the same corpus, and prefilter ≥ 2× plain regex scan on
  literal-bearing patterns (recorded; thresholds revisited with real numbers).
- **SC-009 (new):** attachment-search parity: every filename findable via the retired
  surface is findable via the surviving bar (scripted corpus check in test 26).

**Traceability matrix (delta rows)**

| Req | Story | BDD | Tests |
|---|---|---|---|
| FR-002(+attach/honesty) | US-1 | attachment, clamped, excerpt-unavailable | 26, 28, DS-4, SC-009 |
| FR-016 | US-2 AS-7 | literal metacharacters | 29, DS-1 |
| FR-017 | MV-11 | cancel, busy | 6, 18 |
| FR-018 | US-3 AS-8 | output cap, per-file skip | 7, 22, DS-2 |
| FR-019 | US-3 AS-4 | context lines | 15, 33 |
| FR-020 | US-3 AS-7 | own-workspace | 21 |
| FR-021 | US-2 AS-6 | root lost | 12, 31 |
| FR-022 | — (engine property) | — | 1, 3, 34, SC-008 |

(Full matrix = draft matrix + these rows; every FR row present.)

---

## 8. Ambiguity audit

| # | Item | Resolution |
|---|---|---|
| A1 | Files kind inside vaults | v1: vault = knowledge kinds only; fast-follow — unchanged |
| A2 | Ignored-entries surfacing toggle | deferred; FR-007 is the single scope statement (MIN-003) |
| A3 | Result ordering | knowledge: engine relevance order (OBS-003); files: deterministic path-lexicographic v1 |
| A4 | grep policy roster | **CLOSED** — founder 2026-09-07: allow for ALL agents incl. Worker (FR-009) |
| A5 | unified agent façade | deferred (O3) — unchanged |
| A6 | SC-003 number | now non-gating + recorded (MAJ-009) |
| A7 (new) | go-re2 adoption trigger | only if SC-008 benchmarks show pure-regex large-content dominating real usage; decision recorded with numbers |

## 9. Holdout evaluation scenarios
(H1–H7 as draft, plus:)
- **H8 (happy):** search a vault for an attachment's filename you can see in the tree
  — it appears under Attachments.
- **H9 (error):** start a search over a big mount and immediately navigate away —
  the app stays responsive and the machine shows no lingering CPU burn.
- **H10 (edge):** ask Jim to grep with 2 context lines for a function name — the
  reply shows the surrounding lines, and a follow-up "narrow it" works.
