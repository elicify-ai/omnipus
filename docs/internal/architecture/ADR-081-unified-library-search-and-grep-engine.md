# ADR-081 — Unified Library search, general file search, and the grep engine

- **Status:** Accepted (founder-directed, 2026-09-07). Core decisions ratified in the
  design conversation of 2026-09-07; three sub-decisions are marked OPEN below and do
  not block the shape of the design.
- **Renumbered 2026-09-07:** originally drafted as "ADR-069" on the pre-merge feature
  branch; release/v0.1.1 already owns ADR-069 (universal live browser connectivity), so
  this document is ADR-081. Validated against the MERGED tree
  (integrate/library-improvements-v0.1.1 @ f37346338) — see the "merged-tree
  obligations" in §2 D11.
- **Date:** 2026-09-07
- **Supersedes/relates:** the C1 "human vault search" from
  `docs/internal/specs/library-b-c-design-2026-09-07.md` (which shipped a *second* search
  bar — the defect this ADR corrects); ADR-067 (knowledge base + `knowledge_find`).
- **Constraints in force:** Hard Constraints #1 (single Go binary, no new heavy runtime
  deps), #2 (pure Go, no CGo, no external C libs, no shelling out for security-critical
  paths), #3 (minimal footprint), #4 (graceful degradation), kernel sandbox
  (Landlock/seccomp).
- **Amendment 2026-09-26 (issue #920, read-boundary consistency — founder interview
  [`read-boundary-consistency-interview.md`](../specs/read-boundary-consistency-interview.md),
  D1–D7):** D4 is corrected in place, marked *[amended 2026-09-26 (#920)]* at each
  superseded sentence, original text kept as history. The as-is gap this closes: `grep`
  never called `ResolvePath` (ADR-063 D2) at all — it refused every absolute path and
  every `..` segment outright (`pkg/tools/grep.go::validateGrepScope`) and applied none of
  `read_file`/`list_directory`'s other gates. The founder ruled this a narrowing of the
  single read decision that was never reconciled with ADR-062/ADR-063, and directed one
  boundary for all three tools. Full corrected decision: the new "D4 amendment" subsection
  immediately below D4.

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
- `/knowledge/find` and the agent's `knowledge_find` tool run over the **same** engine —
  already the shared agent+human mechanism. Precisely, on the merged tree: the agent
  tool is `pkg/vaultprops.FindTool` (the ADR-068 D15.3 adapter, registered via
  `pkg/agent/knowledge_tools.go::registerKnowledgeTools`), and the human endpoint is
  `pkg/gateway/rest_knowledge_find.go`; both drive `pkg/records/knowledgefind.Find`
  through `vaultprops.OpenFindEnv`.
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
*[The confinement clause below is amended 2026-09-26 (#920) for the AGENT `grep` tool
only — see the "D4 amendment" subsection right after D5. It is UNCHANGED for the human
Library search bar (`pkg/gateway/rest_library_files_search.go`), which still walks
exactly this confined `Root` with no wider reach.]* It walks the same confined `Root` the
rest of the Library uses (`library.OpenRoot` /
`os.OpenRoot` — the sandbox's path boundary is preserved; this is why we do NOT shell out).
Because a mount can point at a huge, volatile host folder, the walk is **bounded, always**:
caps on files-visited, bytes-scanned, matches-returned, directory depth, a per-file size
skip, and a `context` wall-clock deadline. On any cap it returns partial results **plus an
explicit `truncated` + reason**, rendered with the same honest partial-results UX the vault
search uses. An uncapped walk of a synced host folder is a forbidden state.

**D4 amendment (2026-09-26, issue #920) — the agent `grep` tool joins the single read
decision; the human Library search bar is unchanged.**

Founder decisions (`read-boundary-consistency-interview.md`, cited by ID):

- **interview D1.** `grep`'s `path` argument now resolves through
  `pkg/tools/resolvepath.go::ResolvePath` — the same chokepoint `read_file`,
  `list_directory` and `send_file` already use (ADR-063 D2) — including an absolute path
  and a `..`-reentrant path. The secret set, other agents' homes and other workspaces stay
  categorically unreachable (`fspolicy.IsCarveOut`, checked unconditionally by
  `ResolvePath` regardless of the op). This retires
  `pkg/tools/grep.go::validateGrepScope`'s outright rejection of an absolute `path` and any
  `..` segment (its current `strings.HasPrefix(scope, "/")` check and its per-segment `".."`
  refusal) for the read-open case — see "Windows absolute paths" below for why the
  replacement must not reintroduce the same bug in a new shape. A bare NUL-byte pre-check
  is kept (matches `ResolvePath`'s own step-1 rejection; a pre-flight message beats a raw
  `*fs.PathError`).
- **interview D5.** With no `path` argument, the search area is unchanged: the agent's own
  workspace root plus every mount on it (`grepRoots`'s existing no-scope branch). This
  branch does not call `ResolvePath` and does not change.
- **interview D6 — read-confined agents (Judge, Plan Supervisor).** Both `grep` and
  `read_file`/`list_directory` resolve to **workspace plus its mounted folders** for a
  read-confined turn — closing the as-is inconsistency the interview recorded verbatim:
  "grep includes mounts, read_file refuses them." This is not solely a `grep`-side fix:
  `ResolvePath`'s own `ReadConfined` branch
  (`resolveValidatedPath`'s `if rp.policy.ReadConfined { return nil, ...
  (read-confined turn) }`, reached once `!isWithinWorkspace(realAbs, realWorkDir)` is
  true) today refuses **every** path outside `WorkDir` for a read-confined turn, mounts
  included — there is no mount exception in that branch, verified by reading it. The
  branch must be widened to check `realAbs` against `policy.AllowedRoots` before refusing,
  the same test `matchedAllowedRoot` already applies for `FSOpWrite`/`FSOpServe` a few
  lines below it in the same function. This is a shared `ResolvePath` change: it fixes
  `read_file`/`list_directory` for a read-confined turn at the same time it fixes `grep`,
  which is why the interview flags it for `security-lead` review.
- **interview D7.** Search capacity is unchanged: the existing 2-walk-slot semaphore
  (`filegrep.TryAcquire`/`Release`, shared with the Library search bar) and the 10 s /
  50,000-file bounds (D4 above) apply identically to a widened `grep` call.
- **Human Library search unchanged.** `pkg/gateway/rest_library_files_search.go` keeps
  its existing confinement (workspace + mounts, via `filegrep.GuardCarveOuts` over the same
  confined roots) — D1–D7 above govern the AGENT `grep` tool only.

**The gates a widened `grep` must carry (interview security notes), verified against the
code rather than assumed:**

1. **Secret set — already present, must extend to the new root.** `grep.go::guardCarveOuts`
   already wraps every existing root (workspace, each mount) in `carveOutFS`, applying
   `fspolicy.IsCarveOut`. The new root type this amendment introduces (below) must be
   wrapped the same way — nothing new to build, an existing wrapper reused.
2. **ADR-072 D10.3 skills-registry instruction-file gate — currently MISSING from `grep`
   entirely, must be added.** `ResolvePath` (`resolvepath.go::classifySkillsGate` /
   `isSkillInstructionFile`) refuses a `read_file`/`list_directory`/`send_file` of a
   registry skill's `SKILL.md`/`AGENT.md`/`AGENTS.md` under `$OMNIPUS_HOME/skills`, but it
   classifies only the ONE root path a caller names — it was never wired to judge every
   file a recursive walk visits underneath a root, and `grep` never called it at all. A
   widened `grep` must apply the equivalent per-visited-file check during the walk (both
   the name-match and the content-match hit) — reusing `classifySkillsGate`/
   `isSkillInstructionFile` against each candidate, not re-deriving the rule.
3. **Metadata guard — currently MISSING from `grep` entirely (and not inside `ResolvePath`
   either), must be added.** `pkg/tools/metadata_guard.go::metadataFileMatch` /
   `filesystem.go::guardMetadataPath` run only inside `read_file`/`list_directory`'s own
   `Execute`, AFTER `ResolvePath`, as a separate per-call check — never inside `ResolvePath`
   itself, and never in `grep` today. Without an equivalent per-visited-file check, a
   widened `grep` walking an agent's own workspace (which already contains
   `agents/<id>/`) would let both the name and the content of `SOUL.md`/`HEARTBEAT.md`/
   `AGENT.md` through — a hit `read_file` categorically refuses. A widened `grep` must
   apply `metadataFileMatch` per visited file and refuse both the name-match and the
   content-match the same way.
4. **Symlink containment inside the walk — closed by construction, not a new check.**
   `pkg/filegrep`'s engine already refuses to traverse a symlink met while walking an
   `os.Root`-backed root (`filegrep.go`'s own comment: "symlinks are entries the confined
   FS refuses to traverse"). The design below opens the new root via `os.OpenRoot` (never
   a bare `os.DirFS`/unconfined host walk), so the identical syscall-level refusal applies
   to it automatically — a symlink met during the walk cannot resolve to anywhere
   `ResolvePath` would refuse the root path itself.
5. **Audit rows for refusals and searched roots — currently MISSING from `grep`
   entirely.** Verified: `grep.go` has no `auditLogger` field and never calls
   `SetAuditLogger`, unlike every other file tool (`filesystem.go`, `shell.go`,
   `web_serve.go`, …). Two rows are needed: (a) a refusal — `path_audit.go`'s existing
   `emitPathAccessDenied` (event `path.access_denied`, `ReasonCarveOut`/`ReasonPathInvalid`/
   `ReasonOutsideWorkspace`/`ReasonSymlinkEscape`), the same event and reason vocabulary
   `read_file`/`list_directory` already emit, fired when `ResolvePath` refuses the `path`
   argument; (b) a **new** row recording which root(s) a call actually searched — neither
   existing emitter fits: `emitPathAccessDenied` is a denial-only shape, and
   `filesystem.go::emitFileReadAudit` is one row per opened FILE, which would mean one row
   per matched file for a `grep` call touching hundreds of them, an audit-volume shape
   nothing else in this codebase does for a bulk read. This ADR decides the shape — one
   row per `grep` call, `Details: {"roots": [...], "path_arg": <raw path or "">}` — and
   leaves the exact event constant name (a sibling to `PathAccessDeniedEvent` in
   `path_audit.go`, not a reuse of `audit.EventFileOp`) to backend-lead at GREEN, following
   that file's existing naming convention.
6. **Windows absolute paths.** `validateGrepScope`'s `strings.HasPrefix(scope, "/")` is not
   Windows-safe (a Windows absolute path is `C:\...` or `\\host\share`, never `/`-prefixed).
   Because item 1 above retires this check's role in the read-open case anyway (absolute
   paths are no longer rejected, they are resolved), this bug is retired along with it
   rather than patched in place — the replacement path resolves through `ResolvePath`,
   which never hand-rolls its own absolute-path test (it resolves via
   `resolveRealpathUnderWorkDir`/`filepath`, the same stdlib primitives every other
   path-taking tool already relies on for platform-correct behaviour). Mount-name matching
   (`splitGrepScopeMount`, a purely lexical, first-segment comparison with no I/O) is
   unaffected and still runs first, unchanged.

**Design decision — how an absolute (or otherwise outside-workspace-and-mounts) `path` is
walked.** `ResolvePath`'s `*PathHandle` is shaped for a single file's I/O (`ReadFile`/
`ReadDir`/`Open` against one `rel`/`abs`), not for handing off a subtree to a recursive
walker — so `grep` does not treat the handle as the walk root directly. Instead, mirroring
the exact pattern `pkg/tools/auto_approve.go::AutoWorkspacePath` already uses (resolve,
read `RealPath()` — the one documented advisory-string exception in `resolvepath.go`,
"never hand back a bare string" — then close the handle):

1. Call `ResolvePath(ctx, policy, "grep", "", FSOpList, path)` (or `FSOpRead` —
   `ResolvePath`'s dispatch treats `FSOpRead`/`FSOpList`/`FSOpSend` identically for this
   branch, so either is correct; `FSOpList` is recommended since `path` here names a
   directory to enumerate, matching `list_directory`'s own op choice).
2. Take the handle's `RealPath()` and `Close()` it immediately — `grep` needs the resolved
   absolute location, not the handle's own I/O methods.
3. Open a **fresh, independent** `os.OpenRoot` anchored at that realpath — at the realpath
   itself if it is a directory, at its PARENT if it names a regular file
   (`grep.go::resolveScopedRoot` already implements exactly this directory-vs-file
   dispatch for the mount case; reuse it, do not re-derive it).
4. Wrap the new root in `guardCarveOuts` (gate 1), the new skills-gate wrapper (gate 2) and
   the new metadata-guard wrapper (gate 3) — the same three wrappers every existing `grep`
   root already gets or must newly get, so the new root is never a second, more-permissive
   code path.

This reuses every containment primitive `ResolvePath` and `grep.go` already have; it adds
no new resolution mechanism, only a new ROOT TYPE fed through the existing wrapper chain.

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

**D11 — Merged-tree obligations for any NEW tool this ADR introduces (validated
2026-09-07 against integrate/library-improvements-v0.1.1).**
The merge brought release's tool governance; a new `grep`/`file_search` agent tool must
satisfy all of it, none of which existed when this ADR was drafted:
- **ADR-071 manifest tier:** the tool must be deliberately tiered. Default ruling here:
  **search-only/lazy** (like the knowledge family — pinned by
  `pkg/tools/manifest_test.go::TestVisibility_KnowledgeToolsAreSearchOnly`); its schema
  must not join the every-turn manifest. Add it to the tier pinning tests.
- **ADR-077 two-layer policy:** add the tool to the static catalog
  (`pkg/coreagent/core.go::allStaticToolNames`), give it a shipped GLOBAL ceiling
  default in `pkg/config/defaults.go` (recommended: `allow` — read-only, confined), and
  a posture in every core agent's seed. Update the load-bearing pin
  `pkg/coreagent/catalog_count_test.go::catalogSizeToday` (currently 101) following that
  test's own documented procedure, naming the tool in the commit message.
- **Guards that will police it:** `TestRequestPathRedaction_SourceInventory` (if it ever
  logs request paths), `TestNoUnprotectedInlineRoute` (N/A — it returns JSON, never raw
  bytes), and the pre-commit falsification-mutation gate.
- The REST endpoint pattern to follow is `rest_knowledge_find.go` (auth wrap, honest
  partial/`complete_reason`, decodeAndValidate against a generated schema).

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
- *[Added 2026-09-26, #920]* The agent `grep` tool's reach widens to match `read_file`/
  `list_directory` (D4 amendment above); the human Library search bar's reach does not
  change. `grep` gains an audit logger, an ADR-072 D10.3 skills-gate check, and a metadata
  guard it did not carry before — all three new for this tool, not merely widened.

## 5. Alternatives rejected

- **Keep two searches** — the defect being fixed.
- **Trigram index as the default** — upkeep/staleness, disk/RAM cost, useless on binaries,
  redundant with the vault index. (If ever needed: opt-in per-folder, gated, never auto,
  never on volatile mounts; vendor `google/codesearch`, not `zoekt`.)
- **ripgrep as a binary/CGo dependency** — violates #1/#2 and the sandbox confinement (D6).
- **`sourcegraph/zoekt`** — heavyweight service/library; wrong for a single binary.
