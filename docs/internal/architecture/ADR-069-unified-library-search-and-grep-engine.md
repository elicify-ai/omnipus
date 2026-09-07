# ADR-069 — Unified Library search, general file search, and the grep engine

- **Status:** Accepted (founder-directed, 2026-09-07). Core decisions ratified in the
  design conversation of 2026-09-07; three sub-decisions are marked OPEN below and do
  not block the shape of the design.
- **Date:** 2026-09-07
- **Supersedes/relates:** the C1 "human vault search" from
  `docs/internal/specs/library-b-c-design-2026-09-07.md` (which shipped a *second* search
  bar — the defect this ADR corrects); ADR-067 (knowledge base + `knowledge_find`).
- **Constraints in force:** Hard Constraints #1 (single Go binary, no new heavy runtime
  deps), #2 (pure Go, no CGo, no external C libs, no shelling out for security-critical
  paths), #3 (minimal footprint), #4 (graceful degradation), kernel sandbox
  (Landlock/seccomp).

---

## 1. Context

Three problems, discovered during the B+C UAT:

1. **Search was built twice.** Inside a vault a person sees two boxes: the pre-existing
   `KnowledgeSearch` (`/knowledge/search`, notes only, rich "searched X of Y" honesty) and
   the new C1 `LibrarySearchBar` (`/knowledge/find`, notes+records+views, coarser honesty).
   Two endpoints over the same index, overlapping scope. Owned grounding miss: the existing
   search should have been found and extended, not duplicated.
2. **Search only exists inside vaults.** Plain workspace folders and mounted folders have
   no search at all.
3. **Agents lack a proper recursive content grep.** They have `knowledge_find` (vault
   index only); they cannot grep arbitrary files across folders and mounts.

Verified facts the decisions rest on:
- `/knowledge/find` and the agent's `knowledge_find` tool (`pkg/agent/knowledge_tools.go`)
  run over the **same** engine (`vaultprops.OpenFindEnv` + `knowledgefind.Find`) — it is
  already the shared agent+human mechanism.
- `/knowledge/search` (`pkg/gateway/rest_knowledge.go::handleKnowledgeSearch`) is a
  **separate, human-only** path; **no agent tool uses it** (verified by grep of
  `pkg/sysagent`, `pkg/agent`, `pkg/coreagent`, `pkg/tools`).
- There is **no** recursive/name/content file-search anywhere today — only per-folder
  listing (`pkg/library/entries.go::Root.List`, one level).
- The knowledge index (`knowledgefind`) is **vault-only** (requires the `.omnipus-vault/`
  marker); it cannot cover plain folders or mounts.

---

## 2. Decisions

**D1 — One search surface, two engines (federate, don't merge).**
An index over structured notes and a live filesystem walk are different jobs. We do NOT
merge engines. We present **one search** to each audience that federates two backends and
returns unified, grouped results. Context decides which engine(s) run; the caller never
picks.

**D2 — Knowledge search consolidates on `/knowledge/find`; retire `/knowledge/search`.**
The human Library UI stops calling `/knowledge/search`. `KnowledgeSearch` +
`useKnowledgeSearch` are deleted from the human panel (`KnowledgePanel`), and
`LibrarySearchBar` becomes the single knowledge search. Because `/knowledge/find` already
backs the agent tool, humans and agents then share one knowledge engine. The two note-
honesty signals `/knowledge/search` had and `/knowledge/find` lacks — a per-note
**total-known / searched-count** and a **capped-at-limit** flag — are folded into
`VaultSearchResponse`'s notes group (contract-first) so nothing is lost. `/knowledge/search`
is then removed entirely (nothing agent-side uses it); if a future agent need appears it too
reads `/knowledge/find`.

**D3 — General file search is a live, NO-INDEX, ripgrep-model engine.**
A new pure-Go recursive file search (name/path + content) over any Library location.
**No trigram index.** `google/codesearch` (and `zoekt`) are explicitly **descoped**: an
index costs a build step, staleness/maintenance, and disk/RAM ~= corpus size, is useless on
binary/office files, and is redundant with the vault index — the wrong default for
arbitrary, volatile, possibly-mounted trees. The best everyday grep (ripgrep) is itself
index-free; we copy that model.

**D4 — File search covers workspace folders AND mounts, confined and hard-bounded.**
It walks the same confined `Root` the rest of the Library uses (`library.OpenRoot` /
`os.OpenRoot` — the sandbox's path boundary is preserved; this is why we do NOT shell out).
Because a mount can point at a huge, volatile host folder, the walk is **bounded, always**:
caps on files-visited, bytes-scanned, matches-returned, directory depth, a per-file size
skip, and a `context` wall-clock deadline. On any cap it returns partial results **plus an
explicit `truncated` + reason**, rendered with the same honest partial-results UX the vault
search uses. An uncapped walk of a synced host folder is a forbidden state.

**D5 — Engine composition (pure Go, no CGo). Ripgrep is the design to copy, not a dep.**
- Parallel recursive walk: `charlievieth/fastwalk`.
- Ignore/junk pruning at the directory level: `.gitignore`/`.ignore` (via
  `boyter/gocodewalker` or the small `sabhiram/go-gitignore`).
- Include/exclude globs (`*.md`, `**/x/**`): `bmatcuk/doublestar`.
- Binary skip: NUL-byte check on the first ~8 KB (no library; ripgrep's own heuristic).
- Regex: **RE2** — linear-time, ReDoS-proof, which is a **security property** for agent-
  supplied patterns. Engine = `grafana/regexp` (drop-in stdlib fork, modest speedup, same
  semantics); `wasilibs/go-re2` (real RE2 via WASM/wazero, no CGo) is a **measured
  fallback** only if pure-regex-over-large-content proves a bottleneck (it costs binary size
  and is slower on small inputs).
- Speed levers (build from day one): **literal pre-filter** (scan for the pattern's literal
  with `bytes.Index` first, run the regex only on lines that contain it), a **plain-literal
  fast path** (no regex at all), **parallel scanning** (worker pool feeding off the walk),
  **zero-alloc byte scanning** (pooled buffers, work on `[]byte`), memchr-style line
  handling (`bytes.IndexByte`).

**D6 — No ripgrep binary (shell-out, embed, or CGo-FFI all rejected).**
- Shell out to `rg`: violates "no shelling out for security-critical paths" (searching
  user/mount files is security-sensitive), bypasses our per-search confinement, may be
  blocked by seccomp/Landlock, and requires `rg` on every host (breaks single-binary +
  graceful degradation).
- Embed the `rg` binary: still runtime `exec` (same sandbox/security problem), needs one
  native binary per OS/arch (breaks single-binary + minimal-footprint), and writing an
  executable to disk to run it is what the audit/sandbox model exists to stop.
- CGo/FFI to Rust regex or ripgrep-as-lib: direct violation of #2.
Even an optional "use `rg` if on PATH" fast path would still require the pure-Go engine as
the sandbox/absent-`rg` baseline, so it adds risk for a speedup we do not need.

**D7 — Two front doors on the two engines.**
- **Human:** one unified Library search bar, shown in **every** location. In a vault it runs
  the knowledge engine (Notes · Records · Views) and may also run file search; in a plain
  folder or mount it runs file search (Files). The segmented filter shows only the kinds
  that exist where you are.
- **Agent:** a dedicated **`grep` / `file_search`** tool (the file engine — the missing
  capability) **plus** the existing knowledge search, both over the same engines the human
  bar uses. Humans and agents are never on different search mechanisms.

**D8 — Contract-first (Constraint #8).**
New schemas for the file-search request/response and the `VaultSearchResponse` note-honesty
extension go in `contracts/` first, regenerate, then handlers/consumers use the generated
types only.

**D9 — Performance target: "feels as fast as ripgrep," not benchmark parity.**
For literal-bearing patterns (the common case) the literal pre-filter puts us at effective
parity; for pure-regex-over-large-content we accept somewhat slower — RE2's linear time is a
safety win there anyway. Acceptance criteria: literal pre-filter, parallel scan, zero-alloc
scanning, and the bounds are all present. Precedent that this is achievable in pure Go:
`boyter/cs`/`scc` (Linux-kernel-scale in seconds, no index).

**D10 — Content search is text-only; name/path search is universal.**
Both index and live grep can only search **text** content; binaries are skipped. Name/path
search matches all files (including PDFs/binaries by filename). Making PDF/office *content*
searchable needs text extraction — out of scope here, noted as future.

---

## 3. OPEN sub-decisions (do not block the design)

- **O1 — Dependency posture.** Recommended: take `fastwalk` + `doublestar` + `grafana/regexp`
  (small, pure-Go, high value); gitignore + fuzzy libs optional; `go-re2` only on measured
  need. Alternative: strict stdlib-only (hand-roll walk/ignore; lose parallelism polish).
- **O2 — v1 scope.** Content grep from the start, or name/path match first then content.
- **O3 — Agent surface.** Dedicated `grep` tool AND unified `search` (recommended), or only
  the unified `search`.

---

## 4. Consequences

- Delete `KnowledgeSearch` + `useKnowledgeSearch` from the human panel; one knowledge bar.
- Remove `/knowledge/search`; extend `VaultSearchResponse` with the honesty fields.
- New `/library/{ws}/files/search` endpoint (bounded live grep) + a new agent grep tool.
- New small pure-Go deps (per O1); RE2 retained for ReDoS-safety on agent patterns.
- The unified bar renders in every Library folder, kind-switching by context.
- No index, no background indexer, no staleness machinery — search always reflects the files
  as they are now.

## 5. Alternatives rejected

- **Keep two searches** — the defect being fixed.
- **Trigram index as the default** — upkeep/staleness, disk/RAM cost, useless on binaries,
  redundant with the vault index. (If ever needed: opt-in per-folder, gated, never auto,
  never on volatile mounts; vendor `google/codesearch`, not `zoekt`.)
- **ripgrep as a binary/CGo dependency** — violates #1/#2 and the sandbox confinement (D6).
- **`sourcegraph/zoekt`** — heavyweight service/library; wrong for a single binary.
