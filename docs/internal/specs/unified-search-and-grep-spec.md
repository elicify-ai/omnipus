# Unified Library Search & Grep Engine — Implementation Spec

- **Source ADR:** `docs/internal/architecture/ADR-081-unified-library-search-and-grep-engine.md`
  (founder-ratified 2026-09-07; renumbered from draft "ADR-069"; validated against the
  merged tree the same day).
- **Codebase:** branch `integrate/library-improvements-v0.1.1` @ `f23f18ffb` — the merged
  release/v0.1.1 + vault/library tree. Every code fact below was verified against it.
- **Status:** DRAFT for adversarial review (2 grill rounds planned).
- **Spec discipline:** Phases 1–2.5 use behavioral language only; implementation detail
  appears from the TDD plan onward.

---

## 1. Discovery summary (from ADR-081 + founder rulings)

- **Actors:** (a) a person using the Library UI; (b) an agent (any seeded or custom
  agent) working through tools; (c) the operator governing tool policy.
- **Problem:** search was built twice for vaults (two bars, two endpoints, one index);
  plain folders and mounts have no search at all; agents have no recursive content grep.
- **Scope (3 workstreams):**
  - **A — Knowledge-search consolidation:** one human search bar over the existing
    shared knowledge engine; retire the human-only duplicate path; preserve its
    superior honesty signals.
  - **B — File-search/grep engine:** a new pure-Go, no-index, bounded search over file
    names/paths and text-file content, across workspace folders AND mounts, inside the
    Library's confinement boundary.
  - **C — Surfaces:** the one bar renders in every Library location (kinds switch by
    context); a new agent grep tool over the same engine; REST endpoint for the bar.
- **Out of scope:** trigram/persistent indexes (descoped); ripgrep as a binary/CGo
  dependency (rejected, ADR-081 D6); PDF/office **content** extraction (name/path
  match only for binaries); ⌘K command palette (founder chose the persistent bar);
  Obsidian import.
- **Constraints:** Hard Constraints #1–#4 (single pure-Go binary, no CGo, minimal
  footprint, graceful degradation); contract-first #8; sandbox confinement (search must
  never read outside the Library root's boundary); ADR-071 manifest-tier and ADR-077
  two-layer policy governance for the new tool (ADR-081 D11).
- **Resolved sub-decisions** (defaults per founder direction 2026-09-07):
  - **O1 — dependencies:** minimal-dep — `charlievieth/fastwalk`, `bmatcuk/doublestar/v4`,
    `grafana/regexp`; gitignore handling hand-rolled or `sabhiram/go-gitignore`;
    `wasilibs/go-re2` only if benchmarks later prove a pure-regex bottleneck.
  - **O2 — v1 scope:** name/path match **and** bounded text-content match both ship in
    v1. Rationale: the agent grep is the point of workstream B, and a grep without
    content match is not a grep; the human bar shows content matches for text files.
  - **O3 — agent surface:** a dedicated **`grep`** tool ships in v1. A unified agent
    `search` façade (knowledge + files in one tool) is deferred as follow-up; agents
    compose `knowledge_find` + `grep` meanwhile.

## 2. Existing codebase context (validated 2026-09-07; GitNexus N/A for this worktree — direct verification used)

| Symbol / surface | Role in this feature | Verified fact |
|---|---|---|
| `pkg/records/knowledgefind.Find` + `pkg/vaultprops.OpenFindEnv` | the shared knowledge engine (kept) | called by BOTH the agent tool (`pkg/vaultprops/find_tool.go`, ADR-068 D15.3 adapter, registered via `pkg/agent/knowledge_tools.go::registerKnowledgeTools`) and the human endpoint `pkg/gateway/rest_knowledge_find.go` |
| `POST /library/{ws}/knowledge/find` | the surviving human knowledge endpoint | exists; grouped notes/records/views; `complete`/`complete_reason`; dedups record notes from Notes |
| `POST /library/{ws}/knowledge/search` (`handleKnowledgeSearch`, `rest_knowledge.go:151/461`) | to be retired from the human UI and removed | exists on merged tree; no agent tool consumes it (verified: zero non-test references outside the gateway + SPA) |
| `src/components/library/search/LibrarySearchBar.tsx` + `useVaultSearch.ts` | becomes the ONE bar | exists; segmented All/Notes/Records/Views; `N+` overflow badges; disabled-outside-vault state |
| `src/components/library/knowledge/KnowledgeSearch.tsx` + `useKnowledgeSearch.ts` | deleted from the human panel | exists; mounted by `KnowledgePanel.tsx:333`; carries the richer note-honesty UX (partial/capped/"searched X of Y", `role="status"`) |
| `pkg/library/root.go::OpenRoot` / `Root.List` (`entries.go:25`) | the confinement boundary the walker must live behind | `List` is single-level; **no recursive walk/search exists anywhere** |
| `Root.HostPath`, `CleanRelPath` (addressing-safety only, ADR-067 Stage 0) | path handling for the walker | name-shape checks live ONLY in `ValidateCreateName` (create-time); search must not re-add them to reads |
| `pkg/tools/manifest.go::ToolManifestTier` | tier governance for the new tool | absent-from-both-lists ⇒ Lazy + SearchOnly (the default); knowledge family pinned there by `TestVisibility_KnowledgeToolsAreSearchOnly` |
| `pkg/coreagent/catalog_count_test.go::catalogSizeToday` | load-bearing catalog pin | **101** today; adding `grep` ⇒ 102, following the test's documented procedure |
| `pkg/config/defaults.go` global ceiling + `pkg/coreagent/core.go` per-agent seeds | ADR-077 two-layer policy | every static tool needs a shipped ceiling entry + explicit per-agent seed postures |
| `repairAndValidateToolPolicyCoverage` (`pkg/gateway/gateway.go:1378`) | boot-time coverage gate | migrate → reconcile ceiling → hard-validate; no deny-backfill |
| `TestRequestPathRedaction_SourceInventory` (`preview_path_redact_test.go`) | guard if the endpoint logs request paths | inventory currently 7 sites |
| e2e shards (`tests/e2e/shards.json`) + `scripts/e2e-shards.sh check` | any new e2e spec must be assigned | checker enforces 65/65 today |

**Impact assessment (manual):**

| Modified surface | Risk | Direct dependents to update/test |
|---|---|---|
| `VaultSearchResponse` contract (additive fields) | MEDIUM | `rest_knowledge_find.go`, generated Go/TS/zod, `useVaultSearch.ts`, `LibrarySearchBar.tsx`, `rest_knowledge_find_test.go` |
| `KnowledgePanel.tsx` (remove `KnowledgeSearch` mount) | MEDIUM | `KnowledgePanel.test.tsx`, `KnowledgeSearch*.test.tsx` (deleted with their component), `LibraryExplorer` layout |
| `rest_knowledge.go` route switch (remove `case "search"`) | MEDIUM | `handleKnowledgeSearch` + its tests + `KnowledgeSearchRequest/Response` contracts + `searchKnowledge` client fn |
| `LibraryExplorer.tsx` (bar shown in every location) | MEDIUM | its 30+ test files in `src/components/library` |
| tool catalog + seeds (add `grep`) | HIGH (boot-abort class) | `catalog_count_test.go`, `knowledge_tool_policy_seed_test.go`-family, `defaults.go`, every agent seed map, drift backfill |

**Reference patterns:** repo has no `docs/reference/go-implementation/` library — N/A.
The in-repo pattern to follow for the endpoint is `rest_knowledge_find.go`; for the
engine's cross-process safety notes, `pkg/library`'s confinement docs.

---

## 3. User stories & acceptance criteria

### US-1 (P0) — One knowledge search in a vault
A person inside a vault sees exactly **one** search bar. It searches notes, records and
views, and carries ALL the honesty the old bar had: a partial/index-building notice with
a "searched X of Y" statement, and a capped-at-limit signal. The duplicate "Search
notes" box is gone.
**Why P0:** the shipped duplication is a live UX defect the founder flagged.
**Independent test:** open a vault; count search inputs (=1); force an index-building
state and observe the partial notice with counts.
**Acceptance scenarios:**
1. **Given** a vault with an up-to-date index, **When** the person searches a term
   matching notes, records and views, **Then** results appear grouped by kind with
   per-kind counts, and no second search input exists anywhere in the Library view.
2. **Given** a vault whose index is still building, **When** the person searches,
   **Then** results shown are accompanied by a visible partial-results notice that
   states how much of the collection was searched (an "X of Y" style statement, with Y
   omitted honestly when unknown).
3. **Given** a query whose note matches exceed the per-kind limit, **When** results
   render, **Then** the notes count is presented as a lower bound (e.g. "N+"), never as
   an exact total.
4. **Given** any prior release UI state, **When** this feature ships, **Then** the old
   notes-only search box is absent and its endpoint is no longer called by the UI.

### US-2 (P0) — File search in plain folders and mounts
A person browsing a plain workspace folder or a mounted folder gets the same bar; there
it searches **files** — by name/path always, and by text content for text files. A huge
mount cannot hang it: the search stops at its bounds and says so.
**Why P0:** plain folders and mounts have no search at all today; mounts are the
founder's daily reality.
**Independent test:** search a folder for a filename fragment and for a word inside a
text file; both hit. Point a mount at a large tree; observe the bounded, honest stop.
**Acceptance scenarios:**
1. **Given** a plain folder containing `Q3 report.md`, **When** the person searches
   "report", **Then** the file appears as a name match with its path.
2. **Given** a text file containing the word "meeting" in a subfolder, **When** the
   person searches "meeting", **Then** the file appears with a content-match excerpt
   around the term.
3. **Given** a mounted folder with more files than the scan bound, **When** the person
   searches, **Then** results returned so far appear plus an explicit truncated notice
   naming that the search stopped early and roughly why (bound reached), and the UI
   never presents the result set as complete.
4. **Given** a binary file (e.g. a PDF) whose NAME matches, **When** the person
   searches, **Then** it appears as a name match; **and** its bytes are never content-
   scanned.
5. **Given** the Library's confinement boundary, **When** any search runs, **Then** no
   file outside the workspace work-tree or its mounts is ever read or listed.

### US-3 (P0) — Agent grep tool
An agent invokes a `grep` tool with a pattern (literal or regex) and an optional path
scope and glob filters, and receives structured matches (path, line number, line text)
across the same confined tree, under the same bounds, with an explicit truncated flag.
**Why P0:** the missing agent capability that motivated workstream B.
**Independent test:** through a real agent session, grep a known string in a seeded
workspace; verify structured hits and that a bound produces `truncated: true`.
**Acceptance scenarios:**
1. **Given** a workspace with a file containing "needle", **When** the agent calls
   `grep` with pattern "needle", **Then** the result lists that file with the matching
   line and its line number.
2. **Given** a regex pattern with no literal (e.g. character-class-only), **When** the
   agent calls `grep`, **Then** matching lines are returned (the engine accepts full
   RE2 syntax).
3. **Given** a pathological-looking pattern (deep nesting/alternation), **When** the
   agent calls `grep`, **Then** the call completes within its deadline (linear-time
   matching; no catastrophic backtracking) — possibly with zero matches, never a hang.
4. **Given** an agent whose policy denies `grep`, **When** it attempts the call,
   **Then** the call is refused by policy like any other denied tool.
5. **Given** a match bound of N, **When** more than N lines match, **Then** exactly the
   bounded set returns plus `truncated: true` and a reason.

### US-4 (P1) — One bar, every location, kind-aware
The bar renders in every Library location. In a vault it offers Notes/Records/Views
(and MAY offer Files per D7); in a plain folder or mount it offers Files. The filter
row only ever shows kinds that can exist where the person is standing.
**Why P1:** the unification promise; depends on US-1/US-2 primitives.
**Independent test:** walk vault → plain folder → mount; observe the filter kinds
switch and the bar never disappear.
**Acceptance scenarios:**
1. **Given** the person is at the Library root or any workspace folder, **When** the
   view renders, **Then** the search bar is present (never vault-only).
2. **Given** the person stands in a plain folder, **When** they focus the bar, **Then**
   the offered kinds are file-oriented (no Notes/Records/Views tabs).
3. **Given** an active query, **When** the person navigates to a different folder,
   **Then** stale results are not silently kept: the query either re-scopes to the new
   location or clears, and the file list is restored when the query is empty.

### US-5 (P1) — Endpoint retirement is clean
The human-only knowledge-search endpoint is removed entirely — route, handler, wire
types, SPA client — with no orphaned surface left behind.
**Why P1:** hygiene consequence of D2; nothing else consumes it (verified).
**Independent test:** the retired path returns the Library dispatcher's standard
unknown-route response; repo-wide search finds no live references.
**Acceptance scenarios:**
1. **Given** the shipped build, **When** a client calls the retired knowledge-search
   path, **Then** it receives a 404 (not a handler response).
2. **Given** the SPA bundle, **When** it is audited, **Then** no code path can issue a
   request to the retired path.

### Edge cases (behavioral)
- Empty query ⇒ no search runs; tree view unchanged.
- Query of only whitespace ⇒ treated as empty.
- Unicode query (CJK/Cyrillic/emoji) ⇒ matches by codepoint content; never crashes;
  snippets remain valid UTF-8 (no split runes).
- Symlink inside a workspace/mount ⇒ never followed out of the confinement boundary;
  the symlink entry itself MAY appear as a name match.
- File deleted between walk and read ⇒ skipped silently into the problem count, never
  an error page.
- Unreadable file (permissions) ⇒ skipped, counted, surfaced in the truncated/problem
  summary — never fails the whole search.
- A file with extremely long lines ⇒ excerpt is windowed; response stays bounded.
- Concurrent searches (person types fast; agent greps simultaneously) ⇒ each request
  independent; no shared mutable state; older in-flight UI responses discarded
  (out-of-order guard).
- `.gitignore` present ⇒ ignored trees are pruned from CONTENT search by default;
  name search MAY still surface ignored entries only if explicitly requested.
- Hidden files ⇒ follow the Library's existing hidden-file visibility rule.

---

## 4. Behavioral contract (quick reference)

- When the person is in a vault, the bar searches the knowledge engine and shows
  Notes/Records/Views with per-kind counts and honesty signals.
- When the person is in a plain folder or mount, the bar searches files (name/path +
  bounded text content) and shows Files.
- When any bound (files, bytes, matches, depth, per-file size, deadline) is reached,
  the response carries `truncated` + a reason, and the UI says the search stopped early.
- When the index is building (vault), the response says how much was searched.
- When a query is cleared, the normal file tree returns.
- When an agent calls `grep`, it receives structured matches under the same bounds and
  confinement, or a policy refusal.
- When the retired endpoint is called, the server answers 404.

### Explicit non-behaviors
- The system must not build or maintain any search index for file search (no
  background indexer, no staleness machinery) — ADR-081 D3.
- The system must not shell out to, embed, or link ripgrep or any external search
  binary — D6; the sandbox confinement is the reason, not just style.
- The system must not follow symlinks or any path outside the confined Root — the
  boundary is the security property.
- The system must not content-scan binary files (NUL-byte heuristic) or files above
  the per-file size cap.
- The system must not present a bounded result as complete — no silent truncation,
  ever.
- The system must not re-add name-shape validation to read paths (ADR-067 Stage 0
  ruling: mounted files with Windows-illegal names must stay searchable/readable).
- The agent tool must not bypass tool policy, auditing, or the manifest-tier rules.
- The consolidation must not reduce honesty: every signal the retired bar showed
  (partial, X-of-Y, capped) must exist in the surviving bar.

### Machine-verifiable constraints
- **MV-1** File-search REST: invalid body ⇒ HTTP 400 with a JSON error; unknown
  workspace ⇒ 404; path outside the root ⇒ 403/400 per the Library's existing
  `mapLibraryErr` taxonomy (same codes as sibling Library routes).
- **MV-2** Retired endpoint: exact old path returns HTTP 404.
- **MV-3** Bounds defaults (server-side, overridable downward per request, clamped
  upward): max files visited 50,000; max bytes content-scanned 256 MiB; max matches
  1,000; max depth 32; per-file content cap 4 MiB; wall-clock deadline 10 s. Hitting
  any bound sets `truncated: true` + machine-readable `truncated_reason`.
- **MV-4** Regex engine: RE2 semantics; a 64 KiB pathological pattern must be rejected
  or complete within the deadline — never >10 s CPU.
- **MV-5** Response arrays are always present (empty ⇒ `[]`, never null) — matches the
  Library contract style.
- **MV-6** UTF-8 safety: every returned excerpt is valid UTF-8 (fuzz-checkable).
- **MV-7** Catalog: `catalogSizeToday` = 102 with `grep` present exactly once;
  `ToolManifestTier("grep")` = Lazy + SearchOnly.
- **MV-8** Policy: `grep` has a shipped global-ceiling entry and an explicit posture in
  every seeded agent's map; boot coverage validation reports zero gaps on a fresh
  install.
- **MV-9** VaultSearchResponse additive honesty fields: for notes — searched-count and
  total-known (both optional, omitted when unknown, never fabricated) and a
  capped-at-limit boolean; existing consumers keep validating (additive-only change).
- **MV-10** SPA: exactly one search input rendered per Library view (DOM-assertable).

### Integration boundaries
| System | In/out | Contract | Failure behavior | Dev approach |
|---|---|---|---|---|
| Knowledge engine (`knowledgefind` via `vaultprops`) | query in; grouped hits + completeness out | in-process Go call | engine not-ready ⇒ `complete:false` + reason (existing) | real |
| Filesystem via `library.Root` | walk/read within root | in-process, confinement enforced by `os.Root` | unreadable/deleted files ⇒ skip+count; boundary escape impossible by construction | real (temp dirs in tests) |
| SPA ↔ gateway | REST JSON per generated contracts | openapi.yaml → generated Go/TS/zod | zod-invalid payload dropped + counter (existing edge rule) | real |
| Agent runtime | tool call in; structured result out | `tools.Tool` interface + policy + audit | policy deny ⇒ standard refusal; engine errors ⇒ tool error result, never a crash | real |

---

## 5. BDD scenarios

Format per repo convention; each traces to (US-n, AS-m).

**Workstream A — consolidation**

```gherkin
Scenario: one bar in a vault returns grouped results   # Happy Path
  Traces to: US-1, AS-1
  Given a vault whose index is current
    And notes, records and views each contain the term "acme"
  When the person types "acme" into the Library search bar
  Then results render in three groups: Notes, Records, Views
    And each group's tab shows its count
    And the DOM contains exactly one search input

Scenario: index-building vault states its coverage     # Alternate Path
  Traces to: US-1, AS-2
  Given a vault whose index reports partial coverage of 4120 of 5600 notes
  When the person searches "acme"
  Then a partial-results notice is visible
    And it states 4120 of 5600 (or "4120 so far" when the total is unknown)

Scenario: capped note results read as a lower bound    # Edge Case
  Traces to: US-1, AS-3
  Given more note matches than the per-kind limit
  When results render
  Then the Notes count renders with a "+" suffix

Scenario: retired endpoint answers 404                 # Error Path
  Traces to: US-5, AS-1
  Given the shipped gateway
  When a client POSTs to the retired knowledge-search path
  Then the response status is 404
```

**Workstream B — engine**

```gherkin
Scenario: name match in a plain folder                 # Happy Path
  Traces to: US-2, AS-1
  Given a workspace folder containing "Q3 report.md"
  When a file search for "report" runs scoped to that folder
  Then the results include "Q3 report.md" with its workspace-relative path

Scenario: content match with excerpt                   # Happy Path
  Traces to: US-2, AS-2
  Given a text file whose body contains "quarterly meeting notes"
  When a file search for "meeting" runs
  Then the file is returned with an excerpt containing "meeting"
    And the excerpt is valid UTF-8

Scenario Outline: every bound truncates honestly       # Edge Case
  Traces to: US-2, AS-3; US-3, AS-5
  Given a tree engineered to exceed the <bound> bound
  When the search runs
  Then it returns within the deadline
    And truncated is true with reason "<reason>"
  Examples:
    | bound          | reason        |
    | files visited  | max_files     |
    | bytes scanned  | max_bytes     |
    | matches        | max_matches   |
    | depth          | max_depth     |
    | wall clock     | deadline      |

Scenario: binary files are name-matched only           # Edge Case
  Traces to: US-2, AS-4
  Given a PDF named "invoice.pdf" whose bytes contain the word "meeting"
  When a file search for "invoice" runs
  Then "invoice.pdf" appears as a name match
  When a file search for "meeting" runs
  Then "invoice.pdf" does not appear as a content match

Scenario: confinement holds under adversarial layout   # Error Path
  Traces to: US-2, AS-5
  Given a workspace containing a symlink pointing outside the work tree
  When a file search runs
  Then no file outside the confined root appears in results
    And the search completes without error

Scenario: unreadable file is skipped and counted       # Error Path
  Traces to: Edge cases
  Given a folder containing one unreadable file among readable ones
  When a content search runs
  Then matches from readable files return
    And the response's problem/skip count is non-zero
```

**Workstream C — surfaces**

```gherkin
Scenario: agent grep returns structured matches        # Happy Path
  Traces to: US-3, AS-1
  Given an agent with grep allowed and a workspace file "notes.md" containing "needle"
  When the agent calls grep with pattern "needle"
  Then the tool result lists path "notes.md", a line number, and the matching line

Scenario: RE2 pathological pattern cannot hang         # Error Path
  Traces to: US-3, AS-3
  Given a deeply-alternated 64 KiB pattern
  When the agent calls grep
  Then the call returns within the deadline with an error or empty result
    And the process shows no runaway CPU afterwards

Scenario: policy denies grep like any tool             # Error Path
  Traces to: US-3, AS-4
  Given an agent whose policy sets grep to deny
  When it calls grep
  Then the standard policy refusal is returned and audited

Scenario: bar present outside vaults with Files kind   # Happy Path
  Traces to: US-4, AS-1/AS-2
  Given the person browses a plain workspace folder
  Then the search bar is rendered
  When they focus it
  Then the offered kinds are file-oriented (no Notes/Records/Views)

Scenario: navigation does not keep stale results       # Alternate Path
  Traces to: US-4, AS-3
  Given an active query with rendered results in folder A
  When the person navigates to folder B
  Then either results re-scope to B or the query clears
    And clearing the query restores B's file tree
```

---

## 6. TDD plan (implementation detail permitted from here)

New engine package: **`pkg/filegrep`** (name final unless grill objects), driven through
`library.OpenRoot`. REST: **`POST /api/v1/library/{workspace_id}/files/search`**
(`case "files"` subtree in `rest_library.go`'s dispatcher). Agent tool: **`grep`** in
`pkg/tools` (or `pkg/filegrep` with a `tools.Tool` adapter, mirroring
`vaultprops.FindTool`). Contracts: `FileSearchRequest.yaml`, `FileSearchResponse.yaml`,
`FileSearchHit.yaml` (+ VaultSearch additive fields), regen per Constraint #8.

| Order | Test | Level | Traces to | Description |
|---|---|---|---|---|
| 1 | `TestFileGrep_LiteralFastPath` | Unit | US-3 AS-1 | literal pattern uses `bytes.Index` path; hits with line numbers |
| 2 | `TestFileGrep_RE2AndSmartCase` | Unit | US-3 AS-2 | regex matching; lowercase pattern ⇒ case-insensitive; uppercase ⇒ sensitive |
| 3 | `TestFileGrep_LiteralPrefilterEquivalence` | Unit | US-3 AS-1 | for patterns with a required literal, prefiltered result set == plain-scan result set |
| 4 | `TestFileGrep_BinarySkipNULHeuristic` | Unit | US-2 AS-4 | first-8KiB NUL ⇒ content-skipped; still name-matchable |
| 5 | `TestFileGrep_BoundsMatrix` | Unit | Bounds outline | table-driven per bound ⇒ `Truncated` + exact reason token |
| 6 | `TestFileGrep_DeadlineCancels` | Unit | US-3 AS-3 | context deadline stops the walk; partial results returned |
| 7 | `TestFileGrep_UTF8SafeExcerpts` (+fuzz seed) | Unit | MV-6 | excerpts never split runes; fuzz over multibyte content |
| 8 | `TestFileGrep_GitignorePruning` | Unit | Edge cases | ignored dirs pruned from content scan; whole-subtree skip observed |
| 9 | `TestFileGrep_GlobIncludeExclude` | Unit | US-3 | doublestar `**` include/exclude honored |
| 10 | `TestFileGrep_SymlinkConfinement` | Integration | US-2 AS-5 | symlink escape attempt yields nothing outside root (real temp dirs, mirrors `path_traversal_test.go` idioms) |
| 11 | `TestFileGrep_UnreadableSkippedCounted` | Integration | Error path | chmod-000 file (skipped as root — reuse the symlink variant per repo precedent) |
| 12 | `TestFileGrep_ParallelScanDeterministicSet` | Unit | Engine | result SET stable across runs; ordering rule documented + asserted |
| 13 | `TestLibraryFilesSearch_HandlerHappy` | Integration | US-2 AS-1/2 | REST round-trip against temp workspace; generated types |
| 14 | `TestLibraryFilesSearch_ErrTaxonomy` | Integration | MV-1 | 400/403/404 cases via `mapLibraryErr` parity |
| 15 | `TestLibraryFilesSearch_MountScope` | Integration | US-2 AS-3 | search spanning a mount; truncation on engineered big tree |
| 16 | `TestGrepTool_ExecuteAndPolicy` | Integration | US-3 AS-1/4 | tool registered, executes, deny-policy refusal audited |
| 17 | `TestGrepTool_ManifestTierPinned` | Unit | MV-7 | extend `manifest_test.go` knowledge-family pin to include `grep` |
| 18 | `TestCatalog_SizeIsPinned` update (101→102) | Unit | MV-7 | follow the test's documented procedure |
| 19 | `TestGrep_PolicyCeilingAndSeeds` | Unit | MV-8 | ceiling entry + every seed map posture present; `TestBoot_ZeroToolPolicyGaps` stays green |
| 20 | `TestVaultSearchResponse_HonestyFieldsAdditive` | Integration | MV-9 | old clients' zod still validates; new fields round-trip |
| 21 | `TestKnowledgeSearchRouteRetired404` | Integration | US-5 | retired path ⇒ 404; handler/wire types deleted |
| 22 | `useVaultSearch` honesty-port tests | Unit (vitest) | US-1 AS-2/3 | X-of-Y + capped states rendered; ports `useKnowledgeSearch.test.ts`'s honesty cases |
| 23 | `LibrarySearchBar` context-switch tests | Unit (vitest) | US-4 | kinds by location; single-input DOM assert (MV-10); stale-result guard |
| 24 | `KnowledgePanel` no-search-mount test update | Unit (vitest) | US-1 AS-4 | panel renders without the old box; old component files deleted |
| 25 | Files-kind UI tests | Unit (vitest) | US-2 | name/content hit rendering, excerpt, truncated banner |
| 26 | e2e `library-file-search.spec.ts` | E2E | US-2/US-4 | real gateway: folder+mount search, truncation banner, one-bar walk; assigned in `shards.json` (checker must stay green) |
| 27 | e2e agent-grep flow (extend an existing llm shard spec) | E2E | US-3 | agent greps a seeded file through a real turn |

### Test datasets (per template; excerpts)

**DS-1 — pattern/content matrix** (traces: US-3 AS-1/2/3, MV-4, MV-6)
| # | pattern | content | expect | Traces to |
|---|---|---|---|---|
| 1 | `needle` | line with `needle` | 1 hit, correct line no | US-3 AS-1 |
| 2 | `Needle` | `needle` | 0 hits (smart-case: uppercase ⇒ sensitive) | Unit 2 |
| 3 | `ne{2}dle` | `needle` | 1 hit (RE2) | US-3 AS-2 |
| 4 | 64 KiB alternation bomb | anything | reject or ≤ deadline | US-3 AS-3 |
| 5 | `日本語` | CJK body | 1 hit; UTF-8 excerpt | Edge/MV-6 |
| 6 | `""` (empty) | any | request rejected 400 | MV-1 |
| 7 | `(` (invalid regex) | any | 400 with parse error message | MV-1 |

**DS-2 — bounds** (traces: bounds outline, MV-3): one engineered tree per bound —
50,001 files; a 5 MiB text file (per-file cap); 1,001 matching lines; depth-33 nesting;
sleepy-FS deadline case (use a context with 1 ms budget) — each asserting the exact
`truncated_reason` token.

**DS-3 — walker hygiene** (traces: edge cases): `.gitignore`d dir with a match inside
(pruned); hidden file per visibility flag; symlink loop (terminates); file deleted
mid-walk (TOCTOU skip); Windows-illegal name on a mount (still matchable — Stage 0).

**DS-4 — consolidation honesty** (traces: US-1, MV-9): index states {current, building
with known total, building unknown total} × note-match counts {0, <limit, =limit,
>limit} ⇒ expected notice text and count suffix.

### Regression requirements
This feature **modifies existing functionality**; preserved behaviors and their guards:
1. `/knowledge/find` grouped results + dedup + `complete_reason` — existing
   `rest_knowledge_find_test.go` + snippet tests must pass unchanged.
2. Library route taxonomy — existing `rest_library*` tests unchanged.
3. Tool governance invariants — `TestCatalog_*` (updated pin), tier pins,
   `TestBoot_ZeroToolPolicyGaps`, drift-backfill tests all green.
4. SPA: all `src/components/library` suites (591+ tests) green; `KnowledgeSearch`'s
   deleted suites are replaced by the ported honesty tests (no net coverage loss for
   honesty behaviors — reviewer checklist item).
5. e2e: `scripts/e2e-shards.sh check` green with the new spec assigned; existing
   shards untouched.

---

## 7. Requirements & success criteria

**Functional requirements**
- **FR-001** The Library MUST render exactly one search bar in every Library location.
- **FR-002** In a vault, the bar MUST search notes, records and views via the existing
  shared knowledge engine and MUST surface partial-coverage (X-of-Y), unknown-total,
  and capped-at-limit states.
- **FR-003** In plain folders and mounts, the bar MUST search file names/paths and
  bounded text content through the confined Library root.
- **FR-004** The file-search engine MUST be pure Go, index-free, and MUST enforce all
  MV-3 bounds with an explicit truncated flag + machine-readable reason.
- **FR-005** The engine MUST skip binary files for content (NUL heuristic) and MUST
  cap per-file scan size.
- **FR-006** The engine MUST use linear-time (RE2-class) matching; agent-supplied
  patterns MUST NOT be able to cause super-linear blowup.
- **FR-007** The engine MUST prune `.gitignore`d subtrees from content search.
- **FR-008** A `grep` agent tool MUST expose the engine with pattern, scope, globs,
  and bounds, returning structured matches, honoring tool policy and audit.
- **FR-009** `grep` MUST be catalogued (pin→102), ceiling-defaulted, seeded per agent,
  and manifest-tiered search-only (ADR-081 D11).
- **FR-010** `VaultSearchResponse` MUST gain the note-honesty fields additively;
  existing clients MUST remain valid.
- **FR-011** The human-only knowledge-search endpoint, handler, wire types and SPA
  client MUST be removed; the path MUST return 404.
- **FR-012** `KnowledgeSearch`/`useKnowledgeSearch` MUST be removed from the human
  panel; their honesty UX MUST be ported into the surviving bar before deletion.
- **FR-013** All new wire shapes MUST be contract-first generated (Constraint #8).
- **FR-014** File search MUST NOT read or reveal anything outside the confined root;
  symlinks MUST NOT be followed across the boundary.
- **FR-015** New dependencies are limited to O1's list; each MUST be pinned in go.mod
  and justified in the implementing PR.

**Success criteria**
- **SC-001** DOM audit across vault/plain/mount views shows exactly 1 search input
  (automated in vitest + e2e).
- **SC-002** Engineered-bound trees each return `truncated:true` with the correct
  reason and total wall-clock ≤ deadline + 1 s.
- **SC-003** Grep of a 10k-file synthetic tree (≤ 64 MiB text) completes ≤ 2 s on the
  CI worker (measured in the e2e or a perf test; number revisited at grill).
- **SC-004** 64 KiB pathological pattern: request completes ≤ 10 s with bounded CPU.
- **SC-005** Full quality gates green: gofmt, golangci-lint, verify-contracts,
  typecheck, vitest, scoped Go suites; catalog/tier/policy pins updated exactly once.
- **SC-006** e2e: new file-search spec passes on its shard; shard checker 66/66.
- **SC-007** Honesty parity: every DS-4 state renders the specified notice (vitest
  snapshot-free assertions on text/count).

**Traceability matrix**

| Req | Story | BDD | Tests |
|---|---|---|---|
| FR-001 | US-1, US-4 | one-bar, bar-present | 22, 23, MV-10 assert, 26 |
| FR-002 | US-1 | grouped, coverage, capped | 20, 22, DS-4 |
| FR-003 | US-2 | name match, content match | 1–3, 13, 15, 25, 26 |
| FR-004 | US-2, US-3 | bounds outline | 5, 6, DS-2, 15 |
| FR-005 | US-2 | binary name-only | 4, DS-1 |
| FR-006 | US-3 | pathological pattern | 2, DS-1#4, SC-004 |
| FR-007 | edge | gitignore pruning | 8, DS-3 |
| FR-008 | US-3 | agent grep, policy deny | 16, 27 |
| FR-009 | US-3 | (governance — MV-7/8) | 17, 18, 19 |
| FR-010 | US-1 | coverage notice | 20, DS-4 |
| FR-011 | US-5 | retired 404 | 21 |
| FR-012 | US-1 | one-bar + honesty port | 22, 24 |
| FR-013 | all | (contract gate) | verify-contracts in SC-005 |
| FR-014 | US-2 | confinement scenario | 10, DS-3 |
| FR-015 | — | (dependency review) | PR checklist + go.mod diff |

---

## 8. Ambiguity audit

| # | Ambiguity | Default the spec takes | Status |
|---|---|---|---|
| A1 | Does the vault context ALSO offer Files (D7 "may")? | v1: vault offers Notes/Records/Views only; Files kind arrives in vaults as fast-follow once the engine ships | accepted default — revisit at grill |
| A2 | Name-search over `.gitignore`d entries "only if explicitly requested" — is the toggle in v1? | v1: no toggle; ignored trees pruned for both name+content; toggle deferred | accepted default |
| A3 | Ordering of file results (path-lexicographic vs match-density ranking) | v1: deterministic path-lexicographic; ranking deferred (research doc's ranking signals noted as follow-up) | accepted default |
| A4 | `grep` default posture per agent (ceiling allow; but e.g. Mia?) | ceiling `allow`; per-agent: Jim `allow` (holds bash anyway), Mia/Ava/Ray `allow` (read-only, confined — same class as `knowledge_find`) | needs founder nod at grill review |
| A5 | Does the agent `grep` also cover the vault knowledge kinds? | No — agents compose `knowledge_find` + `grep` (O3 resolution) | resolved by O3 |
| A6 | SC-003's 2 s perf number | provisional; grill may re-baseline against worker hardware | flagged |

## 9. Holdout evaluation scenarios (NOT for development use; excluded from traceability)

- **H1 (happy):** On a fresh install, create a workspace, drop 3 nested text files,
  search a word present in exactly one — the right file appears with an excerpt in
  under 2 seconds, from the UI, no dev tools open.
- **H2 (happy):** Mount a real folder from the Mac (a few thousand files), search a
  filename fragment you know exists deep inside — it's found; the UI never freezes.
- **H3 (happy):** In chat, ask Jim to "grep the workspace for TODO and list files" —
  he uses the grep tool (visible in the activity panel) and reports real hits.
- **H4 (error):** Search a mount pointed at a huge directory — within ~10 s you get
  partial results and a visible "stopped early" notice; the app stays responsive.
- **H5 (error):** Ask an agent whose grep is denied to grep — it reports the refusal;
  nothing hangs.
- **H6 (edge):** Search for a CJK word inside a note in a vault and a txt in a plain
  folder — both paths find it; excerpts render correctly.
- **H7 (edge):** Open a vault: exactly one search box; type, watch grouped results;
  clear — the tree returns exactly as before.
