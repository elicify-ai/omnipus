# Unified Library Search & Grep Engine — Implementation Spec

- **Source ADR:** `docs/internal/architecture/ADR-081-unified-library-search-and-grep-engine.md`.
- **Codebase:** branch `integrate/library-improvements-v0.1.1`. Pin lineage (one
  first-parent line): ADR validated @ `f37346338`; draft @ `f23f18ffb`; round-1 review
  @ `6a4492fb3`; round-2 review @ `a9f8b38a5`.
- **Status:** **FINAL** — grill round 1 (27 findings) and round 2 (22 findings) both
  addressed; this document is self-contained (R2-MAJ-001): no content lives only in
  git history. Reviews: `unified-search-and-grep-spec-review-round1.md`, `-round2.md`.
- **Founder rulings:** (a) best-in-class agent grep — performance levers and agent
  ergonomics are MUSTs; (b) grep is allowed for **ALL agents — explicitly including
  the Worker and the specialist tier** — every seed carries an explicit `allow`.
- **Spec discipline:** §§1–5 behavioral; implementation detail from §6 onward.

---

## 1. Discovery summary

- **Actors:** a person in the Library UI; an agent using tools; the operator
  governing policy.
- **Problem:** vault search was built twice (two bars, two endpoints, one index);
  plain folders and mounts have no search; agents have no recursive content grep —
  and the bar for the fix is best-in-class, not minimum-viable.
- **Scope — 3 workstreams (stage A before B; A is independently shippable):**
  - **A — Knowledge-search consolidation:** one human bar over the existing shared
    engine; retire the human-only duplicate; preserve ALL its honesty signals and its
    attachment search.
  - **B — File-search/grep engine:** pure-Go, no-index, bounded search over file
    names/paths and text content, across workspace folders and mounts, inside the
    Library confinement boundary.
  - **C — Surfaces:** the bar in every workspace location (kinds by context); a new
    agent `grep` tool; a REST endpoint for the bar.
- **Out of scope (explicit):** indexes; ripgrep as binary/CGo; PDF/office content
  extraction; ⌘K palette; Obsidian import; progressive/streamed results (v1 = one
  bounded response, recorded); operator-config keys for bounds (per-request override
  only); ignored-entries surfacing toggle; unified agent search façade (agents
  compose `knowledge_find` + `grep`).
- **Constraints:** Hard Constraints #1–#4; contract-first #8; sandbox confinement;
  ADR-071 tier + ADR-077 policy governance (ADR-081 D11).
- **Resolved sub-decisions:**
  - **O1 — deps (all verified NEW to go.mod):** `charlievieth/fastwalk`,
    `bmatcuk/doublestar/v4`, `grafana/regexp`, `sabhiram/go-gitignore` (pure-Go,
    MIT/BSD; FR-015's pin-and-justify applies); `wasilibs/go-re2` only on measured
    need (A7).
  - **O2:** name/path AND bounded content match both ship in v1.
  - **O3:** dedicated `grep` tool; façade deferred.
  - **A4 (closed):** grep allowed for ALL agents — roster in FR-009.

## 2. Existing codebase context (validated 2026-09-07; GitNexus N/A — direct verification)

| Symbol / surface | Role | Verified fact |
|---|---|---|
| `knowledgefind.Find` + `vaultprops.OpenFindEnv` | shared knowledge engine (kept) | agent tool `vaultprops.FindTool` (ADR-068 D15.3) + human `rest_knowledge_find.go` both drive it; enum `VaultFindRequestKindAttachment` already generated; text index stores attachment name tokens (`pkg/knowledge/index.go::indexAttachment`) |
| `POST /library/{ws}/knowledge/find` | surviving endpoint | queries note+record kinds today (`rest_knowledge_find.go:165,237`); attachment coverage added by this spec |
| `POST /library/{ws}/knowledge/search` (`rest_knowledge.go:151/156/461`) | retired | engine is `pkg/knowledge`'s search over the SAME text index (not knowledgefind) — parity well-posed; hit kinds include `attachment`; `textOnlyServable` is note-only (`find.go:1066-1077`) ⇒ attachment queries need propindex, absent on `records_no_sqlite`/mipsle/netbsd/freebsd-arm (`propindex_stub_unavailable.go`) — see MV-9 carve-out |
| `LibrarySearchBar.tsx` + `useVaultSearch.ts` | the ONE bar | segmented filter, `N+` badges, disabled state at Library virtual root (`LibrarySearchBar.tsx:81`) |
| `KnowledgeSearch.tsx` + `useKnowledgeSearch.ts` + `KnowledgeSearch.attachment.test.tsx` | deleted after porting | mounted at `KnowledgePanel.tsx:333`; `KnowledgePanel.searchFn` prop typed off the component (`:198`) — removal is a prop-API change |
| `api.ts::searchKnowledge` + `api.knowledge.test.ts` | retired client + its test file | direct usages `:22,156` |
| `inboundschemas/KnowledgeSearch*.yaml` | vanish via regen Step 5 | never hand-delete |
| `library.OpenRoot` / `Root.List(rel, includeHidden)` | confinement + hidden rule (caller parameter, dot-prefix) | single-level; no recursive walk exists |
| `pkg/tools/manifest.go::ToolManifestTier` | tier governance | absent-from-lists ⇒ Lazy+SearchOnly; grep gets its OWN sibling pin test |
| `gateway.go::buildKnownBuiltinToolNames` (`:1261-1286`, knowledge union `:1321`) + `registerKnowledgeBuiltinMetadata` (`knowledge_tools_wire.go:112`) | coverage universe + metadata catalog | grep needs both equivalent touches |
| `catalog_count_test.go::catalogSizeToday` | load-bearing pin | 101 ⇒ 102 |
| `defaults.go` ceiling + `core.go` seeds + `tool_policy_catalog_drift.go` | ADR-077 governance | `core.go:854` documents the Worker's historic deny-for-knowledge — **grep diverges from that precedent by founder ruling**; the drift backfill's generic baseline writes deny (`tool_policy_catalog_drift.go:56-72`) — grep overrides it to allow (FR-009); the specialist tier (Planner/Explorer/Researcher, `core.go:98-109`) is seeded via `denyAllThenOverride` and MUST carry the grep override |
| `shell.go::truncateOutput` (const @ `shell.go:1855`, 64,000 chars) | tool output-cap precedent | grep follows it |
| `allowKnowledgeRetrieval` limiter + 429 contract | rate-limit precedent | new endpoint joins that class |
| e2e `shards.json` + `e2e-shards.sh check` | new spec must be assigned | 65/65 today |

**Impact assessment:**

| Modified surface | Risk | Direct dependents |
|---|---|---|
| `VaultSearchResponse` (honesty fields + `attachments[]`) | MEDIUM | `rest_knowledge_find.go`+tests, generated Go/TS/zod, `useVaultSearch`, `LibrarySearchBar` (zod is non-strict `z.object` — additive-safe, verified `schemas.ts:4408`) |
| `KnowledgePanel.tsx` (unmount + `searchFn` prop retype) | MEDIUM | `KnowledgePanel.test.tsx`; deleted suites ported first |
| `rest_knowledge.go` search-case removal + contracts + client + `api.knowledge.test.ts` + inboundschemas (regen) | MEDIUM | US-5 |
| `LibraryExplorer.tsx` (bar everywhere) | MEDIUM | 30+ SPA suites |
| catalog/ceiling/seeds/backfill + 2 gateway wiring sites; **the Worker sparse-map doc comments (`core.go:840-852`, `tool_policy_catalog_drift.go:76-83`) go stale with the first at-ceiling entry — update them** (R2-MIN-011) | HIGH (boot-abort class) | pins, seed tests, drift tests, `TestBoot_ZeroToolPolicyGaps` |

**Reference patterns:** endpoint — `rest_knowledge_find.go`; tool adapter —
`vaultprops.FindTool`; output cap — `shell.go::truncateOutput`. (No
`docs/reference/go-implementation/` library in this repo.)

---

## 3. User stories & acceptance criteria

### US-1 (P0) — One knowledge search in a vault (notes, records, views, attachments)
One bar covers everything both old surfaces covered together, with all the honesty.
**Why P0:** the shipped duplication is a live defect; attachment parity (round-1
CRIT-001) is a release condition.
**Independent test:** open a vault; exactly one search input; attachment filename
search hits; index-building shows the coverage notice.
1. **Given** an up-to-date vault, **When** searching a term matching all kinds,
   **Then** grouped results including an Attachments group; exactly one search input.
2. **Given** an index still building, **Then** a partial-results notice carries the
   server's own statement — "X of Y" when the total is known, "X so far" when not,
   never an invented denominator.
3. **Given** more note matches than the limit, **Then** the count reads "N+"; **and**
   a clamped request discloses the clamp and the applied limit.
4. **Given** a hit whose excerpt cannot be produced, **Then** it renders
   (title/path) with an explicit excerpt-unavailable marker — never dropped, never a
   fabricated excerpt.
5. **Given** any prior UI state, **Then** the old box is gone and the UI cannot call
   the retired endpoint.

### US-2 (P0) — File search in plain folders and mounts
**Why P0:** these locations have no search at all today; mounts are daily reality.
**Independent test:** name and content searches hit in a folder; a huge mount stops
at its bounds with an honest notice.
1. **Given** `Q3 report.md` in a folder, **When** searching "report", **Then** it
   appears as a name match with its path.
2. **Given** a text file containing "meeting", **Then** a content match with a
   bounded excerpt.
3. **Given** a mount beyond the scan bounds, **Then** partial results + an explicit
   stopped-early notice within the interactive deadline; never presented complete.
4. **Given** a binary whose NAME matches, **Then** a name match; its bytes are never
   content-scanned.
5. **Given** the confinement boundary, **Then** nothing outside the work-tree or its
   mounts is read or listed.
6. **Given** the walk/mount root becomes unreadable mid-search, **Then** a
   request-level error or `root_lost` truncation — never a bare "0 matches".
7. **Given** regex metacharacters typed in the bar, **Then** they match literally;
   no parse error can surface from the bar.
8. **Given** the person navigates away or keeps typing, **Then** the superseded
   search is cancelled server-side and at most one search per client is in flight.

### US-3 (P0) — Best-in-class agent grep tool
Ripgrep-class for agents: literal or full RE2, case-mode control with smart-case
default, globs, context lines, per-file and total caps, line numbers, structured
output — over the calling agent's OWN workspace root and mounts only.
**Why P0:** the founder's ruling — the missing capability, built best-in-class.
**Independent test:** real agent session greps a seeded workspace with context
lines; engineered cap produces `truncated:true`.
1. **Given** a file containing "needle", **When** grepping `needle`, **Then** path +
   line number + matching line return.
2. **Given** `regex:true` and `ne{2}dle`, **Then** matches return (full RE2).
3. **Given** a 64 KiB pathological pattern, **Then** reject-at-compile or bounded
   completion within the deadline — never a hang.
4. **Given** `context_lines: 2`, **Then** each hit carries up to 2 lines before and
   after, distinguished from the match line.
5. **Given** policy deny, **Then** the standard refusal, audited.
6. **Given** more matches than caps, **Then** the bounded set + `truncated:true` +
   reason.
7. **Given** any scope argument, **Then** only the calling agent's workspace root and
   mounts are reachable — no cross-workspace parameter exists.
8. **Given** output beyond the tool cap, **Then** truncation at the cap with an
   explicit marker and a narrowing hint.
9. **Given** `case: "sensitive"` with pattern `todo`, **Then** only lowercase `todo`
   matches; `insensitive` matches any casing; default `smart` derives the mode from
   the pattern.

### US-4 (P1) — One bar, every workspace location, kind-aware
**Why P1:** the unification promise; depends on US-1/US-2 primitives.
1. **Given** any folder inside a workspace, **Then** the bar is present and enabled.
2. **Given** the Library virtual root, **Then** the bar renders in its existing
   disabled state with its explanatory placeholder.
3. **Given** a plain folder, **Then** file-oriented kinds; a vault, knowledge kinds.
4. **Given** an active query, **When** navigating, **Then** results re-scope or
   clear; an empty query restores the tree.

### US-5 (P1) — Endpoint retirement is clean
**Why P1:** hygiene consequence of ADR-081 D2; nothing else consumes the endpoint.
1. **Given** the shipped build, **Then** the retired path returns 404.
2. **Given** the SPA bundle, **Then** no code path can call it.
3. **Given** regen, **Then** `verify-contracts` is green (inboundschemas copies gone
   via Step 5 sync, not hand-deletion).

### Edge cases
- Empty/whitespace query ⇒ no search; tree unchanged.
- Unicode (CJK/Cyrillic/emoji) ⇒ codepoint-correct; excerpts valid UTF-8.
- Symlinks ⇒ never followed across the boundary; the entry may name-match.
- File deleted between walk and read ⇒ skipped + counted in stats.
- Unreadable file ⇒ skipped + counted; root loss ⇒ US-2 AS-6.
- Extremely long lines ⇒ excerpt windowed to the cap.
- Concurrent searches ⇒ independent; client discards out-of-order; server cancels
  abandoned walks.
- `.gitignore` (any tree, git or not) ⇒ subtrees pruned from BOTH name and content
  search; nested files and `!` negations honored (library semantics); pruned count
  visible in stats. The walker reads `.gitignore` files even when hidden files are
  excluded from RESULTS.
- Hidden files ⇒ `include_hidden` flag, default false. **Always-pruned set
  regardless of the flag:** `.git/`, `.library/`, `.omnipus-vault/` — the flag
  governs USER dotfiles only; Omnipus internals and git object noise are never
  content-scanned (R2-MIN-006).

---

## 4. Behavioral contract & constraints

**Contract (quick reference):** vault ⇒ knowledge kinds + full honesty; plain
folder/mount ⇒ files (names always, content bounded); literal smart-case from the
bar; regex only where explicitly requested; every bound explicit; root loss visible;
cleared query restores the tree; disconnect cancels the walk; grep = structured,
context-capable, capped, own-workspace, policy-governed, audited; retired path 404.

### Explicit non-behaviors
- No file-search index, ever, in this feature (ADR-081 D3).
- No shelling out to / embedding / linking external search binaries (D6).
- No symlink-following or any read outside the confined Root.
- No content scan of binaries (NUL heuristic) or beyond the per-file cap.
- No silent truncation; no silent limit clamps (both surfaces disclose).
- No name-shape validation re-added to reads (ADR-067 Stage 0).
- No policy/audit/tier bypass for the grep tool.
- No honesty regression: every retired-surface signal exists in the surviving bar.
- No regex parse errors from the human bar (it does not speak regex).
- No cross-workspace grep scope.
- No ordering regression: knowledge hits keep engine relevance order.
- No operator-config keys for bounds in v1; no progressive delivery in v1 (recorded).

### Machine-verifiable constraints
- **MV-1** REST errors: 400 invalid body / invalid regex (only when `regex:true`),
  401 unauthenticated, 403 outside-root, 404 unknown workspace, 429
  rate-limited/busy (with `Retry-After`) — sibling-route taxonomy.
- **MV-2** Retired path ⇒ 404.
- **MV-3** Bounds (server defaults; per-request downward override; clamps
  DISCLOSED via `limits_applied` echo; not operator-configurable): max files
  50,000 · max bytes scanned 256 MiB · max matches 1,000 · **max matches per file
  50** · max depth 32 · per-file content cap 4 MiB (a per-file skip-remainder,
  counted in stats, not a request truncation) · deadline 10 s default / SPA sends
  3 s · accumulated-output budget 1 MiB. Request-level `truncated_reason` enum:
  `max_files | max_bytes | max_matches | max_depth | deadline | max_output |
  root_lost`.
- **MV-3a Truncation layering (R2-MIN-004):** the ENGINE enforces the accumulated
  output-byte budget (reason `max_output`); the tool's 64,000-char serialization cap
  applies AFTER engine truncation, appends the explicit marker, and sets
  `max_output` only if no engine reason is already present (an engine reason is
  preserved in the structured body). REST's 1 MiB IS the engine budget — there is no
  separate post-hoc serialization check.
- **MV-4** RE2 semantics; a 64 KiB pathological pattern ⇒ compile-reject or bounded
  completion; never super-linear.
- **MV-5** Response arrays always `[]`, never null (asserted on the zero-hit path).
- **MV-6** Excerpts valid UTF-8, ≤ 512 bytes around the match.
- **MV-7** `catalogSizeToday` = 102; `ToolManifestTier("grep")` = Lazy + SearchOnly
  via its own sibling pin test.
- **MV-8** Policy (founder roster): ceiling `allow`; **explicit `allow` seeds for
  every tier — Jim/Mia/Ava/Ray, the Worker, the specialist tier
  (Planner/Explorer/Researcher via their `denyAllThenOverride` maps), and system
  agents**; the drift backfill writes **`allow`** (the ceiling posture) for `grep`
  on pre-existing agents — a recorded exception to its generic deny baseline,
  justified by the founder ruling on a read-only confined tool (R2-MAJ-002); fresh
  install validates zero gaps; the upgrade path is test-pinned.
- **MV-9** VaultSearch honesty additions (additive; old zod valid — verified
  non-strict `z.object`): notes searched-count + total-known (optional, omitted when
  unknown — FR-036's never-invent-a-denominator rule rides along), capped-at-limit,
  server-authored coverage `statement` (composed by the HANDLER, mirroring the
  retired `knowledgeStatement`), `limit_clamped` + `limit_requested`; per-hit
  `excerpt_unavailable` **boolean** (deliberate reduction from the retired 5-reason
  enum — the find path cannot attribute the old re-read reasons; recorded,
  R2-MIN-010); `attachments[]` group (path, name). **Platform carve-out
  (R2-MIN-001):** on propindex-less builds (`records_no_sqlite`, mipsle, netbsd,
  freebsd-arm) the Attachments group returns empty WITH `complete:false` and the
  engine's refusal reason surfaced — never a bare empty group; SC-009 is scoped to
  propindex-capable builds.
- **MV-10** Exactly one search input per Library view.
- **MV-11** Concurrency & cancellation (R2-MAJ-003): ONE shared 2-slot walk
  semaphore covers **both** REST and agent-tool walks. REST over-cap ⇒ 429 +
  `Retry-After: 1`; the SPA keeps previous results, auto-retries once after 500 ms,
  and never error-flashes a typist; the SPA holds ≤1 in-flight search (a new
  keypress cancels the previous request, which cancels the server walk). The agent
  tool waits up to 2 s for a slot, then returns a structured busy error with a
  retry hint. The endpoint additionally sits behind the knowledge-retrieval-class
  rate limiter; the grep tool passes the existing tool-side limiter exactly as the
  knowledge tools do.
- **MV-12** Grep tool output ≤ 64,000 chars with explicit marker (shell precedent).
- **MV-13** Best-in-class levers, testable as specified (R2-MAJ-004 semantics):
  - **case modes:** `case ∈ {smart, sensitive, insensitive}`, default smart
    (lowercase pattern ⇒ insensitive; any uppercase ⇒ sensitive); applies to name
    AND content matching (closes round-1 MAJ-003's name-case residue).
  - **literal fast path:** for **case-sensitive** literal patterns the regex engine
    is not invoked at all (seam-asserted); case-insensitive literals use an
    ASCII-case-folded scanner with regex fallback only when the pattern contains
    non-ASCII letters (recorded mechanism, honestly testable).
  - **required-literal prefilter** before regex; **parallel scanning** workers;
    **bounded-alloc hot path:** `testing.AllocsPerRun` gate ≤ 2 allocs per scanned
    line on the sensitive-literal path (R2-MIN-008 closes the ungated MUST).
- **MV-14** Hit granularity (R2-MIN-009): a hit is ONE matching line (first match
  position reported); `max matches` counts hits; a name match is one hit with
  `match_kind: name`.

### Integration boundaries
| System | In/out | Contract | Failure | Dev |
|---|---|---|---|---|
| Knowledge engine | query in; grouped hits + completeness out | in-process | not-ready ⇒ `complete:false`+reason; propindex-less ⇒ MV-9 carve-out | real |
| Filesystem via `library.Root` | walk/read in root | `os.Root` confinement | per-file skip+count; root loss ⇒ `root_lost`; escape impossible | real (temp dirs) |
| SPA ↔ gateway | REST per generated contracts | openapi → Go/TS/zod | zod-invalid dropped+counter | real |
| Agent runtime | tool call in; structured out | `tools.Tool` + policy + audit + tool limiter | deny ⇒ refusal; busy ⇒ structured busy; errors ⇒ tool error | real |
| Rate limiter | request in; allow/deny | retrieval-class | 429 + Retry-After | real |
| Agent turn context | grep output into transcript | 64k cap bounds windowTrim cost | — | real |

---

## 5. BDD scenarios

**A — consolidation**
```gherkin
Scenario: one bar in a vault returns grouped results            # Happy Path
  Traces to: US-1 AS-1
  Given a vault whose index is current
    And notes, records, views and an attachment filename each match "acme"
  When the person types "acme" into the Library search bar
  Then results render grouped: Notes, Records, Views, Attachments
    And each group tab shows its count
    And the DOM contains exactly one search input

Scenario: index-building vault states its coverage              # Alternate Path
  Traces to: US-1 AS-2
  Given a vault index reporting 4120 of 5600 notes searched
  When the person searches
  Then a partial notice shows the server statement with "4120 of 5600"
  And with an unknown total it shows "4120 so far" and no denominator

Scenario: capped note results read as a lower bound             # Edge Case
  Traces to: US-1 AS-3
  Given more note matches than the per-kind limit
  Then the Notes count renders with a "+" suffix

Scenario: clamped limit is disclosed                            # Alternate Path
  Traces to: US-1 AS-3
  Given a request limit above the server cap
  Then the response reports the clamp and the applied limit

Scenario: excerpt-unavailable hit still renders                 # Edge Case
  Traces to: US-1 AS-4
  Given a note hit whose content moved since indexing
  Then the hit shows title and path with an excerpt-unavailable marker

Scenario: attachment search on a propindex-less build is honest # Edge Case
  Traces to: US-1 AS-1; MV-9 carve-out
  Given a build without the properties index
  When the person searches an attachment filename
  Then the Attachments group is empty
    And complete is false with the engine's refusal reason surfaced

Scenario: retired endpoint answers 404                          # Error Path
  Traces to: US-5 AS-1
  When a client POSTs to the retired knowledge-search path
  Then the response status is 404
```

**B — engine**
```gherkin
Scenario: name match in a plain folder                          # Happy Path
  Traces to: US-2 AS-1
  Given "Q3 report.md" in a workspace folder
  When a file search for "report" runs
  Then it appears with its workspace-relative path and match_kind name

Scenario: content match with excerpt                            # Happy Path
  Traces to: US-2 AS-2
  Given a text file containing "quarterly meeting notes"
  When a file search for "meeting" runs
  Then the file returns with a valid-UTF-8 excerpt containing "meeting"

Scenario: human bar treats metacharacters literally             # Happy Path
  Traces to: US-2 AS-7
  Given a file containing the literal text "f(x)"
  When the person searches "f(x)"
  Then the file matches and no error is shown

Scenario Outline: engineered bounds truncate honestly           # Edge Case
  Traces to: US-2 AS-3; US-3 AS-6
  Given a tree engineered (bounds injected) to exceed the <bound> bound
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

Scenario: per-file cap is a counted skip, not a truncation      # Edge Case
  Traces to: MV-3
  Given a 5 MiB text file whose match sits beyond the per-file cap
  Then the remainder is skipped, the skip appears in stats,
    And the request-level truncated flag is not set by this alone

Scenario: per-file match cap bounds a noisy file                # Edge Case
  Traces to: US-3 AS-6; MV-3 (R2-MAJ-005)
  Given one file with 200 matching lines and a per-file cap of 50
  Then that file contributes exactly 50 hits
    And the per-file-cap count appears in stats

Scenario: binary files are name-matched only                    # Edge Case
  Traces to: US-2 AS-4
  Given "invoice.pdf" whose bytes contain "meeting"
  Then "invoice" finds it by name and "meeting" does not find it by content

Scenario: confinement holds under adversarial layout            # Error Path
  Traces to: US-2 AS-5
  Given a symlink pointing outside the work tree
  When a search runs
  Then nothing outside the confined root appears and no error is raised

Scenario: unreadable file is skipped and counted                # Error Path
  Traces to: US-2 edge; FR-021
  Given one unreadable file among readable ones (injected FS error)
  Then readable matches return and the problem count is non-zero

Scenario: lost mount root is a visible error                    # Error Path
  Traces to: US-2 AS-6
  Given a search over a mount whose root becomes unreadable mid-walk
  Then the response is an error or truncated with reason "root_lost"

Scenario: RE2 pathological pattern cannot hang                  # Error Path
  Traces to: US-3 AS-3
  Given a 64 KiB alternation bomb with regex true
  Then the call returns within the deadline (reject or bounded) — never a hang

Scenario: abandoned search is cancelled server-side             # Edge Case
  Traces to: US-2 AS-8
  Given a long-running search
  When the client disconnects
  Then the server-side walk stops before its deadline

Scenario: concurrent walk cap answers busy                      # Error Path
  Traces to: US-2 AS-8; MV-11
  Given two walks already running
  When a third REST search arrives
  Then it receives 429 with Retry-After
  And an agent grep in the same state receives a structured busy error
```

**C — surfaces**
```gherkin
Scenario: agent grep returns structured matches                 # Happy Path
  Traces to: US-3 AS-1
  Given an agent with grep allowed and "notes.md" containing "needle"
  When it calls grep with pattern "needle"
  Then the result lists path, line number and the matching line

Scenario: grep returns context lines                            # Happy Path
  Traces to: US-3 AS-4
  Given a match on line 10 and context_lines 2
  Then the hit carries lines 8-9 and 11-12 as context, distinct from line 10

Scenario: case modes behave as commanded                        # Happy Path
  Traces to: US-3 AS-9 (R2-MAJ-006)
  Given files containing "todo" and "TODO"
  Then case sensitive "todo" matches only the lowercase file,
    case insensitive matches both,
    and smart-case "todo" matches both while smart-case "TODO" matches one

Scenario: grep output cap truncates with a marker               # Edge Case
  Traces to: US-3 AS-8
  Given a query matching megabytes of lines
  Then serialized output is at most the tool cap
    And ends with an explicit truncation marker and a narrowing hint

Scenario: grep cannot leave its own workspace                   # Error Path
  Traces to: US-3 AS-7
  Given two workspaces
  When the calling agent greps any scope expression
  Then results only come from its own workspace root and mounts

Scenario: policy denies grep like any tool                      # Error Path
  Traces to: US-3 AS-5
  Given an agent whose policy sets grep to deny
  Then the standard refusal is returned and audited

Scenario: bar present and kind-aware everywhere                 # Happy Path
  Traces to: US-4 AS-1/2/3
  Given a workspace folder, the bar is enabled with file kinds
  Given a vault, the bar offers knowledge kinds
  Given the Library virtual root, the bar renders disabled with its hint

Scenario: navigation does not keep stale results                # Alternate Path
  Traces to: US-4 AS-4
  Given an active query with results in folder A
  When the person navigates to folder B
  Then results re-scope or clear, and an empty query restores B's tree
```

---

## 6. TDD plan

Engine **`pkg/filegrep`**; REST **`POST /api/v1/library/{workspace_id}/files/search`**
(`case "files"` in `rest_library.go`); tool **`grep`** via a `tools.Tool` adapter
(pattern `vaultprops.FindTool`) + the two gateway governance touches
(`buildKnownBuiltinToolNames` union; metadata-catalog registration).

**Wire contract sketch (final shapes in `contracts/`, Constraint #8):**
- `FileSearchRequest`: `path?` (scope), `query` (required), `regex` (bool, default
  false — the bar never sets it), `case` (`smart|sensitive|insensitive`, default
  smart), `include_hidden` (bool, default false; `.git/.library/.omnipus-vault`
  always pruned regardless), `include_globs[]`, `exclude_globs[]`, `context_lines`
  (0–5; carried on REST for API parity with the tool — the SPA does not use it in
  v1, recorded R2-MIN-005), `limits?` (downward overrides: files, bytes, matches,
  matches_per_file, depth, deadline_ms, output_bytes).
- `FileSearchHit`: `path`, `match_kind` (`name|content`), `line?`, `excerpt?`
  (≤512 B), `context_before[]?`, `context_after[]?`.
- `FileSearchResponse`: `hits[]` (always present), `truncated`, `truncated_reason?`
  (MV-3 enum), `limits_applied` (echo of effective limits; discloses clamps,
  R2-MIN-007), `stats` { files_visited, bytes_scanned, files_skipped_problems,
  files_pruned_ignored, files_skipped_per_file_cap, hits_capped_per_file }.
- `VaultSearchResponse` additions per MV-9 (incl. `attachments[]`).

| # | Test | Level | Traces to | Notes |
|---|---|---|---|---|
| 1 | `TestFileGrep_SensitiveLiteralFastPath` | Unit | MV-13 | regex engine NOT invoked for case-sensitive literals (seam counter); + `testing.AllocsPerRun` ≤2/line gate |
| 2 | `TestFileGrep_CaseModesAndSmartCase` | Unit | US-3 AS-9 | smart/sensitive/insensitive over content AND names; folded-scan vs regex-fallback equivalence |
| 3 | `TestFileGrep_RE2Semantics` | Unit | US-3 AS-2 | |
| 4 | `TestFileGrep_LiteralPrefilterEquivalence` | Unit | MV-13 | prefiltered == plain scan |
| 5 | `TestFileGrep_BoundsMatrix` | Unit | bounds outline | injected bounds; the 6 engineered-bound reasons (`root_lost` is test 13's, R2-MIN-003); + per-file-cap-as-skip + per-file MATCH cap |
| 6 | `TestFileGrep_DeadlineAndContextCancel` | Unit | US-2 AS-8 | deadline stops walk; context cancel stops walk |
| 7 | `TestFileGrep_UTF8SafeExcerpts` + fuzz seed | Unit | MV-6 | 512 B cap; multibyte fuzz |
| 8 | `TestFileGrep_GitignoreSemantics` | Unit | FR-007 | nested, negation, non-git tree, pruned counts; gitignore read even with hidden excluded |
| 9 | `TestFileGrep_HiddenAndAlwaysPruned` | Unit | R2-MIN-006 | include_hidden on/off; `.git/.library/.omnipus-vault` never scanned either way |
| 10 | `TestFileGrep_GlobIncludeExclude` | Unit | US-3 | doublestar `**` |
| 11 | `TestFileGrep_SymlinkConfinement` | Integration | US-2 AS-5 | real temp dirs; mirrors path_traversal idioms |
| 12 | `TestFileGrep_UnreadableSkippedCounted` | Integration | FR-021 | injected FS-error seam + dangling-symlink variant (never chmod-000 — void under root CI) |
| 13 | `TestFileGrep_RootLostMidWalk` | Integration | US-2 AS-6 | root removed after walk start ⇒ error/`root_lost` |
| 14 | `TestFileGrep_ParallelScanDeterministicSet` | Unit | engine | result set stable; ordering rule asserted |
| 15 | `TestFileGrep_PathologicalPattern64KiB` | Unit | MV-4 | |
| 16 | `TestFileGrep_ContextLines` | Unit | US-3 AS-4 | file-boundary clamps |
| 17 | `TestFileGrep_HitGranularity` | Unit | MV-14 | two matches on one line = one hit; name match = one hit |
| 18 | `TestLibraryFilesSearch_HandlerHappy` | Integration | US-2 AS-1/2 | + `[]`-not-null zero-hit assert + `limits_applied` echo/clamp assert |
| 19 | `TestLibraryFilesSearch_ErrTaxonomy` | Integration | MV-1 | 400/401/403/404/429(+Retry-After) |
| 20 | `TestLibraryFilesSearch_ConcurrencyCapAndCancel` | Integration | MV-11 | 3rd walk ⇒ 429; disconnect cancels; TOOL walk shares the semaphore (busy path) |
| 21 | `TestLibraryFilesSearch_MountScope` | Integration | US-2 AS-3 | injected bounds over a mount |
| 22 | `TestGrepTool_ExecuteAndPolicy` | Integration | US-3 AS-1/5 | registered, executes, deny refusal audited |
| 23 | `TestGrepTool_OwnWorkspaceOnly` | Integration | US-3 AS-7 | two workspaces; no crossover |
| 24 | `TestGrepTool_OutputCapTruncates` | Integration | US-3 AS-8; MV-3a | layered-reason rule asserted |
| 25 | `TestVisibility_GrepIsSearchOnly` | Unit | MV-7 | own sibling pin test (cites ADR-081 D11) |
| 26 | `TestCatalog_SizeIsPinned` 101→102 | Unit | MV-7 | the test's documented procedure |
| 27 | `TestGrep_PolicyAllTiersAndDriftBackfill` | Unit | MV-8 | explicit allow: 4 core + Worker + specialists + system agents; backfill writes ALLOW for grep on pre-existing agents |
| 28 | `TestVaultSearch_HonestyAndAttachments` | Integration | MV-9; US-1; SC-009 | additive compat, attachments group, clamp disclosure, excerpt_unavailable, handler-authored statement; propindex-less carve-out via build-tag variant |
| 29 | `TestKnowledgeSearchRouteRetired404` | Integration | US-5 | + handler/wire types deleted |
| 30 | `useVaultSearch` honesty-port tests | vitest | US-1 AS-2/3/4 | ALL signals incl. statement/clamp/excerpt_unavailable/attachments |
| 31 | `LibrarySearchBar` context/kind/busy tests | vitest | US-4; MV-11 | root-disabled state; literal metacharacters; 429 auto-retry without error flash; single in-flight |
| 32 | `KnowledgePanel` prop-retype tests | vitest | US-5 | `searchFn` prop removal |
| 33 | Files-kind UI tests | vitest | US-2 | name/content rendering, truncated banners incl. root_lost |
| 34 | e2e `library-file-search.spec.ts` | E2E | US-2/US-4 | shards.json assigned; checker 66/66 |
| 35 | e2e agent-grep flow | E2E | US-3 | real turn with context lines |
| 36 | `BenchmarkFileGrep_*` | Bench | SC-008; FR-022 | recorded, non-gating, FIXED committed corpus (R2-OBS-004) |

**Test datasets.**
- **DS-1 pattern matrix** (US-3, MV-4, MV-6, FR-016) — WITH `regex` column
  (R2-MIN-002): | # | pattern | regex | content | expect | ⇒ `needle`/false/hit;
  `Needle`/false vs "needle"/0-hits (smart-case); `ne{2}dle`/true/hit;
  64 KiB bomb/true/bounded; `日本語`/false/UTF-8 excerpt hit; empty/–/400;
  `(`/**true**/400 parse error; `f(x)`/false/literal hit; `todo` under all three
  case modes.
- **DS-2 bounds** (MV-3, MV-3a) — all via injected options: files, bytes, matches,
  per-file matches, depth, deadline (1 ms context), output budget; per-file content
  cap as counted skip; each asserting its exact reason token / stats field.
- **DS-3 walker hygiene** — nested-gitignore + negation rows; non-git tree row;
  root-lost row; include_hidden on/off × always-pruned rows; symlink loop; deleted
  mid-walk; Windows-illegal mounted name (Stage 0: still matchable).
- **DS-4 consolidation honesty** (US-1, MV-9) — index {current, building+known
  total, building+unknown total} × counts {0, <limit, =limit, >limit} ⇒ notice
  text/suffix; clamped-limit row; excerpt_unavailable row; attachment row;
  propindex-less carve-out row.

**Regression requirements.** This modifies existing functionality; preserved and
guarded: `/knowledge/find` behaviors (existing `rest_knowledge_find_test.go` +
snippet tests unchanged); Library route taxonomy (existing `rest_library*` tests
unchanged); governance invariants (updated pins; `TestBoot_ZeroToolPolicyGaps`,
drift tests green); SPA library suites green; **ported-coverage review diff** against
the deleted `KnowledgeSearch*`/`useKnowledgeSearch`/`api.knowledge` suites (every
behavioral assert ported or retired-with-reason); ordering guard (engine relevance
order, OBS-003); e2e shard checker green at 66/66.

---

## 7. Requirements & success criteria

**Functional requirements**
- **FR-001** Exactly one search bar in every Library location (enabled in workspace
  locations; the documented disabled state at the virtual root).
- **FR-002** Vault search MUST cover notes, records, views AND attachment filenames
  via the shared engine, with the full honesty set: server statement, X-of-Y,
  unknown-total, capped, clamp disclosure, excerpt-unavailable (MV-9, incl. the
  platform carve-out).
- **FR-003** Plain folders and mounts MUST get name/path + bounded content search
  through the confined root.
- **FR-004** The engine MUST be pure Go, index-free, enforcing every MV-3 bound with
  explicit truncation reasons and the MV-3a layering rule.
- **FR-005** Binary files (NUL heuristic) and per-file-cap remainders MUST be
  content-skipped and counted.
- **FR-006** Matching MUST be RE2-class linear-time; agent patterns can never cause
  super-linear blowup.
- **FR-007** `.gitignore`d subtrees MUST be pruned from BOTH name and content search
  (nested + negations honored, git-ness irrelevant, counts observable); no
  surfacing toggle in v1.
- **FR-008** A `grep` tool MUST expose the engine (pattern, regex flag, case mode,
  scope, globs, context lines, caps) returning structured matches under policy and
  audit.
- **FR-009** Policy roster (founder): ceiling `allow`; explicit `allow` for EVERY
  agent tier — Jim/Mia/Ava/Ray, the Worker, specialists
  (Planner/Explorer/Researcher), system agents; the drift backfill writes `allow`
  for grep on pre-existing agents. No posture is left to silent inheritance.
- **FR-010** VaultSearch additions are contract-first and additive (MV-9).
- **FR-011** The retired endpoint's full inventory (route, handler, wire types,
  inboundschemas via regen, client fn, client test file, `searchFn` prop) MUST be
  removed; the path returns 404.
- **FR-012** `KnowledgeSearch`/`useKnowledgeSearch` deleted only after their
  behaviors are ported (review-diff checklist).
- **FR-013** All new wire shapes contract-first (Constraint #8).
- **FR-014** Search MUST NOT read or reveal anything outside the confined root;
  symlinks never followed across it; `.git/.library/.omnipus-vault` never scanned.
- **FR-015** New deps limited to O1's four; pinned and justified in the PR.
- **FR-016** The human bar is literal-with-smart-case; regex only via the explicit
  flag (REST/tool).
- **FR-017** The endpoint MUST be rate-limited (retrieval class); the 2-slot walk
  semaphore MUST cover REST + tool paths; busy answers carry Retry-After (REST) or
  a structured busy (tool); client disconnect cancels the walk.
- **FR-018** Output MUST be size-bounded: 512 B excerpts; 1 MiB engine output
  budget; 64,000-char tool serialization cap — every truncation explicit and
  layered per MV-3a.
- **FR-019** The grep tool MUST support context lines (0–5), per-file (50) and total
  (1,000) match caps, glob filters, line numbers, case modes, structured output.
- **FR-020** grep is scoped to the calling agent's own workspace root and mounts.
- **FR-021** Mid-walk root/mount loss MUST surface as an error or `root_lost` —
  never an unqualified empty result; per-file failures are skipped and counted.
- **FR-022** Performance levers are MUSTs with teeth: sensitive-literal fast path
  (regex engine bypassed, alloc-gated), insensitive-literal folded scan with
  recorded regex fallback, required-literal prefilter, parallel scan.

**Success criteria**
- **SC-001** DOM audit: exactly 1 search input across vault/plain/mount/root views.
- **SC-002** Every engineered bound ⇒ correct reason, wall-clock ≤ deadline + 1 s.
- **SC-003** (non-gating, recorded) 10k-file/≤64 MiB grep measured in tests/perf;
  target ≤ 2 s solo on the worker.
- **SC-004** 64 KiB pathological pattern ≤ 10 s bounded CPU.
- **SC-005** Full quality gates green incl. new pins (catalog 102, tier, policy,
  drift, contracts).
- **SC-006** e2e file-search spec green; shard checker 66/66.
- **SC-007** Every DS-4 honesty state renders its specified notice.
- **SC-008** (recorded, fixed committed corpus) sensitive-literal path ≥ 5× regex
  path on plain strings; prefilter ≥ 2× plain regex on literal-bearing patterns —
  thresholds re-baselined with first real numbers (corpus-dependent, R2-OBS-004).
- **SC-009** Attachment parity on propindex-capable builds: every filename findable
  via the retired surface is findable via the surviving bar (scripted corpus in
  test 28); propindex-less builds show the MV-9 honest carve-out instead.

**Traceability matrix**

| Req | Story | BDD | Tests |
|---|---|---|---|
| FR-001 | US-1/US-4 | one-bar; bar-everywhere | 30, 31, 34 |
| FR-002 | US-1 | grouped; coverage; capped; clamped; excerpt-unavailable; attachment; carve-out | 28, 30, DS-4, SC-009 |
| FR-003 | US-2 | name; content | 18, 21, 33, 34 |
| FR-004 | US-2/3 | bounds outline; per-file skips | 5, 6, DS-2 |
| FR-005 | US-2 | binary name-only; per-file cap skip | 4-adjacent (NUL test), 5, DS-2 |
| FR-006 | US-3 | pathological | 3, 15, DS-1 |
| FR-007 | edge | gitignore | 8, DS-3 |
| FR-008 | US-3 | grep happy; context; case modes | 22, 16, 2, 35 |
| FR-009 | US-3 | policy deny (+roster) | 27, 22 |
| FR-010 | US-1 | (contract) | 28, verify-contracts |
| FR-011 | US-5 | retired 404 | 29 |
| FR-012 | US-1 | one-bar port | 30, 32, regression diff |
| FR-013 | all | — | verify-contracts (SC-005) |
| FR-014 | US-2 | confinement; always-pruned | 11, 9, DS-3 |
| FR-015 | — | — | PR go.mod gate |
| FR-016 | US-2 AS-7 | literal metacharacters | 31, DS-1 |
| FR-017 | US-2 AS-8 | cancel; busy | 6, 20, 31 |
| FR-018 | US-3 AS-8 | output cap; layering | 7, 24, DS-2 |
| FR-019 | US-3 | context; case; per-file cap | 16, 2, 5, 35 |
| FR-020 | US-3 AS-7 | own-workspace | 23 |
| FR-021 | US-2 AS-6 | root lost; unreadable skip | 13, 12, 33 |
| FR-022 | — | (engine property) | 1, 2, 4, 14, 36, SC-008 |

---

## 8. Ambiguity audit (all closed or recorded)

| # | Item | Resolution |
|---|---|---|
| A1 | Files kind inside vaults | v1: knowledge kinds only; fast-follow |
| A2 | Ignored-entries toggle | deferred; FR-007 is the single scope statement |
| A3 | Ordering | knowledge: engine relevance; files: deterministic path-lexicographic |
| A4 | grep roster | CLOSED — allow for ALL tiers incl. Worker & specialists (FR-009) |
| A5 | unified façade | deferred (O3) |
| A6 | SC-003 | non-gating, recorded |
| A7 | go-re2 trigger | only if SC-008 shows pure-regex large-content dominating; decision recorded with numbers |

## 9. Holdout evaluation scenarios (excluded from traceability; post-implementation evaluation only)

- **H1 (happy)** Fresh install: 3 nested text files; search a word in exactly one —
  right file + excerpt, under 2 s, from the UI, no dev tools.
- **H2 (happy)** Mount a real Mac folder (thousands of files); search a deep
  filename fragment — found; UI never freezes.
- **H3 (happy)** Ask Jim to "grep the workspace for TODO and list files" — grep tool
  visible in the activity panel; real hits reported.
- **H4 (error)** Search a huge mount — within ~10 s, partial results + visible
  stopped-early notice; app responsive.
- **H5 (error)** An agent with grep denied reports the refusal; nothing hangs.
- **H6 (edge)** CJK word found in a vault note AND a plain-folder txt; excerpts
  render correctly.
- **H7 (edge)** Vault: one search box; type → grouped results; clear → tree
  restored exactly.
- **H8 (happy)** Search a vault for an attachment filename visible in the tree — it
  appears under Attachments.
- **H9 (error)** Start a big-mount search, navigate away instantly — no lingering
  CPU burn, app responsive.
- **H10 (edge)** Ask Jim to grep with 2 context lines — surrounding lines shown; a
  "narrow it" follow-up works.
