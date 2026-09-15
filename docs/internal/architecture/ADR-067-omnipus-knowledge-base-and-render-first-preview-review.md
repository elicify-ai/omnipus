# ADR-067 review — adversarial spec grill

- **Target:** `docs/internal/architecture/ADR-067-omnipus-knowledge-base-and-render-first-preview.md` (identical copy in the session scratchpad)
- **Companion reviewed alongside:** `docs/internal/specs/library-improvements-requirements-2026-08-21.md`
- **Reviewer mode:** structured-spec (decision IDs D1–D16, open questions O-1–O-7, residuals 1–5; no BDD scenarios, no FR-xxx, no traceability matrix)
- **Code verified against:** worktree `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-library-improvements` at `6acd378e` (branch `feat/library-improvements`)
- **Date:** 2026-08-22

---

## 1. Executive summary

Thirty-nine findings: **5 critical, 18 major, 9 minor, 7 observations.** Three of the five
critical findings are in D15 (render-first preview) alone, where the chosen delivery path —
the existing `/preview/` handler — cannot serve Library files at all, and where its existing
Content-Security-Policy blocks exactly the "self-contained bundle" case D15 promises to
render. The remaining two are a cross-workspace data-exposure hole in multi-vault search and
an unaddressed Hard Constraint #6 obligation that aborts gateway boot.

**Verdict: BLOCK.**

The ADR is well-argued and unusually honest about its own residuals; the problem is not
reasoning quality, it is that several load-bearing decisions rest on properties of existing
code that the code does not have.

---

## 2. Findings

### CRITICAL

| ID | Lens | Section | Finding | Required fix |
|---|---|---|---|---|
| **C-1** | Incorrectness / Incompleteness | D15, §2.3 | **The `/preview/` path's own CSP blocks the bundle case D15 promises.** `buildWorkspaceCSP` (`pkg/gateway/rest_workspace.go:75-83`) emits `default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; object-src 'none'`. `script-src` does **not** include `'self'`, so an external `<script src="app.js">` is blocked; `style-src` likewise blocks `<link rel="stylesheet">`; `font-src` and `media-src` fall through to `default-src 'none'`. D15's row "HTML, and self-contained bundles (`index.html` + assets) — Rendered as a live page" is therefore false as specified, and it fails in precisely the ADR-061 shape the ADR invokes in A10: the page loads, looks broken, and nothing says why. | State the exact CSP the Library preview response will carry, and prove the bundle case against it. Either extend the directive set to `'self'` for script/style/font/media on this route only, or serve bundles through a route with its own CSP. Add a fixture bundle (`index.html` + external `.css` + external `.js` + a webfont) to the acceptance criteria and assert all four load. |
| **C-2** | Insecurity (Elevation of Privilege / Information Disclosure) | D15 last paragraph, §2.4 | **Switching `rest_library.go`'s `Content-Disposition` to inline creates stored XSS on the gateway origin.** Line 592 sets `attachment` today; that header is the *only* thing stopping an agent-authored or downloaded `.html` file in a workspace from executing as a first-party document. The proposed mitigation — "a sandboxed iframe without `allow-same-origin`" — is an attribute of the **embedder**, not of the response. The endpoint is registered at `/api/v1/library/` behind `withUploadAuth` (`rest.go:5216-5217`), so a top-level navigation to that URL (operator "open in new tab", a link inside another rendered page, a URL an agent puts in chat) loads attacker-controlled HTML *with* the `omnipus-session` cookie — which is `SameSite=Strict`, i.e. sent on exactly this same-site navigation (`middleware/session_cookie.go:148`). | The defence must travel with the response, not with the embedder. Require the inline mode to set `Content-Security-Policy: sandbox; default-src 'none'; …` on the response itself (the `sandbox` CSP directive binds the document regardless of how it is loaded), and require an explicit test that a top-level GET of an inline `.html` yields an opaque-origin document that cannot read `document.cookie`. Serving from a distinct origin is the stronger alternative and should be recorded as considered. |
| **C-3** | Infeasibility | D15 "Served via the hardened `/preview/` path" | **`/preview/` cannot serve Library or mount files.** `HandlePreview` (`pkg/gateway/rest_preview.go:84-148`) resolves `/preview/{agent_id}/{token}/…` against `DevServerRegistry` then `ServedSubdirs` only; nothing else can mint a token. `ServedSubdirs.Register` (`pkg/agent/served_subdirs.go:110-172`) is **agent-scoped, one active registration per agent** — registering a different `absDir` "atomically replaces the previous registration and invalidates its token" — and TTL-bounded with a `maxTokenLifetime` ceiling and a janitor. So reusing it means: (a) the Library has no agent to register under; (b) previewing a file would kill whatever live `web_serve` preview that agent had handed the operator; (c) the preview iframe silently 404s on TTL expiry. And the path is **token-only, unauthenticated by design** (`rest_preview.go:13` "TOKEN-ONLY. The path token IS the credential") — minting tokens for arbitrary mount paths would open an unauthenticated read channel onto the operator's real disk. The ADR simultaneously specifies the *other* endpoint (`rest_library.go`'s inline mode), so which one actually serves the bytes is undecided. | Choose one delivery path and specify it fully. If `/preview/`: specify token minting, ownership (not agent-scoped), lifetime, revocation, and how it does not evict `web_serve` registrations — and justify an unauthenticated path onto mounted host files. If the Library endpoint: C-2's response-borne sandbox is mandatory and the "hardened `/preview/` path" sentence must be deleted, along with the ADR-044 departure note, which then applies to nothing. |
| **C-4** | Insecurity (Information Disclosure) | D7, requirements §3.2 "one search spanning every mounted vault" | **Multi-vault search crosses the workspace isolation boundary.** Mounts are per-workspace (`pkg/workspace/mount.go`, `AllowedMountRoots` filters by workspace id), and ADR-037 makes workspace membership the *only* delegation trust boundary. A `knowledge_search` that spans "every mounted vault" lets an agent whose workspace mounts nothing read the full text of a vault mounted only into a different workspace. The ADR states the capability and never scopes it. | State explicitly that `knowledge_search` and `knowledge_graph` are scoped to the calling agent's workspace's mounts, enumerate the resolution path (agent → workspace → `AllowedMountRoots`), and add a negative test: an agent in workspace A gets zero hits from a vault mounted only in workspace B. If cross-workspace search is genuinely wanted, it needs its own ADR section and an explicit operator grant. |
| **C-5** | Incompleteness (violates Hard Constraint #6) | D7, §4.2 costs | **~10 new `knowledge_*` builtin tools with no tool-policy seeding plan — the gateway aborts boot on the gap.** `config.ValidateToolPolicyCoverage` (`pkg/config/validate.go:448`) is enforced at boot (`pkg/gateway/gateway.go:2029-2032`, "aborting boot"), at reload (`gateway.go:3504-3507`) and at every agent write (`rest.go:2135`). On a **fresh install** the tools must be enumerated explicitly in `pkg/config/defaults.go` and per-agent in `pkg/coreagent/core.go` or boot fails. On an **existing install** the load-path backfill (`validate.go:577`) silently backfills every unknown tool to `deny` — so every existing operator upgrades into a knowledge base whose tools are all denied, with a WARN log as the only signal (`gateway.go:941`). The ADR never mentions Constraint #6. | Add a decision covering: the exact tool list, the explicit `allow`/`ask`/`deny` seed per core agent (Mia/Jim/Ava/Ray) in `defaults.go` and `coreagent.SeedConfig`, and — separately — the upgrade path for existing agents, which the deny-backfill otherwise breaks silently. Add a boot test asserting zero coverage gaps with the new tools registered. |

### MAJOR

| ID | Lens | Section | Finding | Required fix |
|---|---|---|---|---|
| **M-1** | Incorrectness | D1, D7, D10, whole KB feature | **The Library refuses filenames that real Obsidian vaults contain.** `library.CleanRelPath` runs `pathsafe.ValidateComponent` on **every** path segment (`pkg/library/root.go:386-396`), and `pathsafe` rejects `< > : " | ? *`, trailing dot/space, and Windows device names **unconditionally on every OS** by deliberate design (`pkg/pathsafe/pathsafe.go` package doc). Obsidian note titles routinely contain `:` and `?` on macOS and Linux. Those notes cannot be listed, read, indexed, linked, renamed or previewed through the Library at all — on a feature whose entire premise is mounting an operator's pre-existing real vault. | Decide and document the posture before implementation: (a) the KB indexes and reads such files but they are not addressable via the Library REST surface (state the operator-visible consequence), (b) `pathsafe` gains a read-only relaxation for mounted host trees, or (c) the KB reports them as an explicit "unaddressable file" class in `doctor`. Silence here means an operator's vault is partly invisible with no error. |
| **M-2** | Infeasibility | D3, D7, §4.2 | **The `pkg/memrooms/index` reuse is understated.** `Search` sets `req.Fields = []string{}` ("scores only") and returns `[]SearchHit{ID, Score}` — no stored fields, no highlighting, no fragments. It also hard-caps `limit` at 50 and defaults to 20 (`pkg/memrooms/index/index.go`, `Search`). D7 promises "path + title + matched excerpt" and "top-N"; neither exists. §4.2 reduces the whole cost to "must be generalised… its index location default must become injectable". | Expand §4.2 to name the real work: stored fields or a highlight-capable mapping for excerpts, a raised/parameterised result cap, path→doc-ID mapping, and the manifest. State whether excerpts come from bleve highlighting (needs stored fields, grows the index) or from re-reading the file at query time (needs a match locator). |
| **M-3** | Infeasibility (scale) | D3, D4, §1.2 | **The reused rebuild path loads the entire corpus into memory — the exact shape the ADR criticises Obsidian for.** `rebuildLocked` calls `memrooms.ScanMemories(dir)` into a slice and then builds **one** `idx.NewBatch()` over every document before a single `idx.Batch(batch)` commit. At the stated 100k-note design target this is a multi-gigabyte in-memory batch, defeating §1.2's own argument. `OpenOrCreate` also triggers a full rebuild whenever `DocCount()==0`. | Specify batched, bounded-memory indexing (chunk size, commit cadence, peak-RSS budget) as a requirement, not an implementation detail — Hard Constraint #3 caps security-feature overhead at 10 MB and this feature has no stated memory budget at all. Add a measured acceptance criterion at 100k documents. |
| **M-4** | Infeasibility | D6 "Deleting the index and reindexing reproduces it identically. Asserted in CI as a property test" (requirements §3.11: "byte-identical") | **A bleve scorch index is not byte-reproducible.** Segment file names, internal segment ids, merge scheduling and timestamps vary between runs. A CI property test asserting byte equality will fail non-deterministically, or will be quietly weakened to something that asserts nothing — the false-green pattern this project documents. | Restate the property as observable behaviour: for a fixture vault, a rebuilt index returns the identical ranked result set for a fixed query corpus, and the identical `links`/`backlinks`/`unresolved`/`orphans` sets. Assert on the *graph and query outputs*, never on index bytes. |
| **M-5** | Inconsistency | D10 vs D14 vs D11.1 vs residual 4 | **"Atomically" is used for an operation the same document says is not atomic.** D10: rename "rewrites every inbound link to it, atomically." D14: a write journal exists specifically "so a crash mid-rename is detected by `doctor` rather than leaving a half-rewritten graph" — which is an admission that it is not atomic. D11.1 and residual 4 say the move is copy-then-delete and not atomic. | Delete "atomically" from D10 and replace it with the real guarantee: journalled, crash-detectable, and recoverable via `doctor`. Two readers will otherwise build two different things — one with a journal, one assuming a filesystem primitive that does not exist across files. |
| **M-6** | Incompleteness | D3, D13, O-7 | **The same host folder can be mounted into several workspaces, and even twice into one.** `CreateMount` (`pkg/workspace/mount.go:437`) checks *name* collisions only (`checkMountNameAvailable`) — never `HostPath`. So one vault can have N mount records across N workspaces. Consequences the ADR does not address: N indexes over one corpus (or one index with N owners); D14 tier-1 mutual exclusion is broken if the lock key is workspace-scoped; and **D13's "Revoke deletes the derived index" destroys a sibling workspace's index**. | Decide the index identity key explicitly — realpath of the KB root, not workspace+mount — and specify reference counting for D13's revoke (delete only when the last mount referencing that realpath goes away). O-7 asks the narrower question ("does a KB span mounts"); this is the harder one and is not asked at all. |
| **M-7** | Incorrectness / Insecurity | D16 third bullet | **The "relative links render struck through" bug is a deliberate, test-asserted security control.** `isSafeHref` (`src/lib/url-safe.ts`) is documented as rejecting relative URLs, and `src/components/chat/MarkdownText.test.tsx:97` explicitly asserts `isSafeHref('/relative/path') === false`. It is consumed by `markdown-shared.tsx:64,79` (chat markdown, which renders **untrusted model and tool output**) and by `IframePreview.tsx:69,327`. D16 calls it a defect and says the fix "affects all markdown in the product, not only KBs". A blanket change makes model-authored relative hrefs live links on the gateway origin. | Reframe as a scoped change, not a bug fix. Specify: which render contexts gain relative-link resolution (KB reader only, or also chat), what base each resolves against, that the `isDisplayableImageSrc` precedent (which *does* resolve relatively, with a narrower allow-list) is the model to follow, and which existing assertions in `MarkdownText.test.tsx` are being deliberately changed and why. |
| **M-8** | Incompleteness (violates Hard Constraint #8) | whole ADR | **No contract-first plan.** KB detection, search results, outline, backlinks, index progress, and inline-preview metadata all cross the gateway/SPA boundary. Constraint #8 requires each to be defined in `contracts/components/schemas/` and referenced from `openapi.yaml`/`asyncapi.yaml` **before** any Go/TS code, with generated types the only legal cross-boundary types. The ADR never mentions contracts. Note also the index-progress banner (D5) is a *streaming* state and therefore an AsyncAPI frame, not a REST field. | Add a section enumerating every new wire type, whether it is REST or WS, and stating that the 5-step process in CLAUDE.md is followed. Without it the first implementer writes hand-rolled structs that the lint gate rejects. |
| **M-9** | Ambiguity / Infeasibility | D4 "a `doctor` operation… MUST be runnable without an agent" | **"Without an agent" is undefined, and the strict reading is impossible.** If it means "without an LLM turn", fine. If it means a separate CLI process (`omnipus knowledge doctor`) while the gateway runs, it cannot work: scorch holds a **process-exclusive bbolt lock** on `root.bolt`, and this codebase deliberately bounds the wait at `boltOpenTimeout = "5s"` so contention "surfaces as an ERROR" (`index.go`). A CLI doctor would reliably error out on any running install. | Define the surface: REST endpoint, SPA button, CLI subcommand, or all three — and if CLI, state how it reads index state without opening the scorch index (e.g. manifest-only drift check, or the CLI proxies to the running gateway). |
| **M-10** | Infeasibility | D5 "Indexing — 1,240 of 98,000 notes" | **The denominator requires a completed walk before the first document is indexed.** The ADR specifies a precise progress string with real counts but never says how the total is known, what it costs on a 100k-file cloud-synced tree, or what the banner reads during the walk itself. The incremental-reconcile case (D4) has no natural denominator at all. | Specify the two phases separately: an enumeration phase (with its own indeterminate-progress state) and an indexing phase (with the ratio). State what the banner says when the total is not yet known — it must not read "0 of 0" or vanish. |
| **M-11** | Incompleteness | D14 tier 3, residual 2 | **"Detect-before-write (content hash / mtime) and refuse loudly" has no defined baseline.** For `knowledge_append_section` or `knowledge_set_property`, what is the hash compared *against*? The agent never read the file; nothing in the ADR establishes a read-then-write token. Separately, mtime is a weak detector here: Syncthing preserves source mtimes on replication, and several filesystems have 1-second granularity, so a sub-second external write is invisible. | Specify a concrete optimistic-concurrency mechanism: every KB read returns an opaque version token (content hash), every write requires it, and a mismatch is a typed, actionable error. State that mtime alone is insufficient and why. |
| **M-12** | Insecurity (path traversal) / Incompleteness | D6, D7 | **No containment rule for link and embed resolution, or for symlinks inside a vault.** D6 gives a resolution algorithm (exact path → unique basename → tie-break) with no statement that a resolved target must remain inside the KB root. `[[../../../.ssh/id_rsa]]`, an absolute-path link, or a symlink inside the mounted vault pointing at `/etc` are all unaddressed. The Library's own `os.Root` confinement protects the REST surface; a KB indexer that walks the tree with `filepath.WalkDir` is outside that protection unless the ADR says otherwise. | Add an explicit invariant: every resolved link target and every walked path must resolve (realpath) inside the KB root; anything else is reported as `unresolved`, never followed. State the symlink policy (skip, or resolve-and-contain) and the symlink-loop guard. |
| **M-13** | Incompleteness (UX) | D12, D2 | **`.omnipus-vault/` is a dotfile, so the Library hides it by default.** `pkg/library/entries.go:41` defines hidden as `strings.HasPrefix(de.Name(), ".")` and filters it out unless `includeHidden`. D12 puts the KB's templates there and D2 puts its identity and settings there. Neither states how the operator sees, authors or edits them. | Specify the surface: a KB settings panel that reads/writes the marker's contents, or an explicit "show hidden" affordance in this context. "Edit the template" must be a reachable action, not a `includeHidden=true` URL trick. |
| **M-14** | Incompleteness | D3, §4.2 first bullet | **Generalising `pkg/memrooms/index` is a change to a package memory rooms depend on, with no regression plan.** `OpenOrCreate` takes a `memrooms.Room`; the process-wide registry key is derived from the index path (`registryKey(idxPath)`); the rebuild source is `room.MemoriesDir`. Making the location injectable and the types generic touches all three. The ADR's cost line does not mention memory-room regression risk at all. | Decide and record: generalise in place (and state the memrooms regression tests that must stay green) versus build a sibling package on the same pattern (and accept the duplication). A10-style "we chose X over Y" reasoning is warranted here and is missing. |
| **M-15** | Inconsistency | D1 vs O-3 | **The marker name is simultaneously decided and open.** D1 states the rule in normative form ("A folder is a knowledge base if its root contains `.omnipus-vault/`") and D11/D2/D12 all build on it; O-3 says the name is a working name whose change "means migrating operators' folders". An ADR cannot ship a normative detection rule whose identifier is undecided. | Resolve O-3 before the ADR is accepted, or demote D1's name to a placeholder and state that no build writes a marker until it is resolved. |
| **M-16** | Insecurity (Repudiation) | D7, D10, D14 | **No audit requirement for `knowledge_*` writes.** Every Library mutation routes through `a.logLibraryAudit` (`pkg/gateway/rest_library.go`). The new authoring tools write to the operator's **real disk** outside that path, unattended, and the ADR's §4.1 explicitly celebrates that agents can now author. There is no audit-event requirement anywhere in the document. | Require an audit event per KB mutation (create, link, set_property, append_section, move/rename, and the multi-file rewrite set), naming the agent, the KB root, the paths touched, and the outcome — including refusals from D14 tier 3, which are the security-relevant ones. |
| **M-17** | Incorrectness | D15 audio row, O-5 | **Audio will not play over the chosen path regardless of which formats are "claimed".** `contentTypeForPath` maps 13 extensions (`pkg/gateway/rest_workspace.go:87-102`) and contains **no audio types**; unknown extensions return `application/octet-stream`, and the response sets `X-Content-Type-Options: nosniff`. `<audio>` will not play an octet-stream. The same table also governs the `/preview/` path C-3 discusses. Note too that `media-src` is absent from the CSP, so it falls to `default-src 'none'`. | Fix the framing: O-5 is not "which formats do we claim" but "extend the MIME table and the CSP". List the exact extensions and MIME strings to add, and add a test that each returns a playable `Content-Type`. |
| **M-18** | Overcomplexity / process | whole ADR vs requirements §6 | **The requirements document's top risk was dropped in the ADR.** Requirements §6.1 states the §4 walkthrough "added back: a create-vault flow, a New-note action with templates, indexing progress UX, an empty-vault first run, broken-mount recovery, evicted-file handling, and a three-tier concurrency model. **Net scope is larger than before the graph was cut.** Sequencing is now the main lever left." The ADR carries every one of those items forward and **removes the sequencing risk entirely** — §4.2/§4.3 discuss costs but never scope or staging, and §5 open questions do not include it. | Add a sequencing decision: which decisions constitute a shippable stage 1 (the ADR's own §1.4/D15/D16 preview and markdown fixes are independently shippable and touch none of the KB machinery), and which gate on O-2/O-3. #632 itself says the parts are "useful independently and best shipped in this order". |

### MINOR

| ID | Lens | Section | Finding | Required fix |
|---|---|---|---|---|
| **m-1** | Incorrectness (citation) | §1.4 | `libraryPreviewKind.ts` is cited bare; it is at `src/components/library/preview/libraryPreviewKind.ts`, not `src/lib/`. A reviewer following the citation finds nothing. | Use the full path. |
| **m-2** | Incorrectness (rationale) | D1 blockquote | The stated collision is weaker than described: `.omnipus/` is the workspace-level memory room at `workspaces/<id>/.omnipus/` (`pkg/workspace/instructions.go:32`), a **sibling of** `work/` — not a name that would collide with a marker inside `work/<vault>/`. The conclusion (pick a distinct name) is right; the reason given is not. | Restate the rationale as "avoid one name meaning two things across the product", dropping the false structural-collision claim. |
| **m-3** | Ambiguity | D9 | "operator direction, 2026-08-04" is an undated, unlinked source for a binding layout constraint. | Cite the decision record or the vault note. |
| **m-4** | Infeasibility (unmeasurable) | §1.2, D3 | The ADR argues at length that Obsidian's ceiling can be cleared but states **no target of its own**. The requirements document had one ("sub-second query at 100k+, no multi-GB heap"); the ADR dropped it. A claim with no threshold cannot be verified or falsified. | Restore a measurable target: p95 query latency at N documents, peak RSS during initial index, and time-to-first-usable-result. |
| **m-5** | Inconsistency | D5 vs D13 "Empty KB" | D5's persistent progress banner and D13's empty-KB first run can both be true at once (a freshly created KB is empty *and* indexing). Which surface wins is unspecified. | State the precedence. |
| **m-6** | Incompleteness | D13 "Evicted / placeholder files" | "MUST fail loudly and MUST NOT be indexed as an empty note" specifies the negative but not the detection mechanism. iCloud stubs are `.icloud` sidecar files; OneDrive/Dropbox use reparse points or extended attributes with the *same* logical filename. The rule is unimplementable as written on non-Apple providers. | Name the detection per provider, or narrow the requirement to the providers actually supported and record the rest as a residual. |
| **m-7** | Ambiguity | D6 "a defined tie-break" | The tie-break is referred to but never defined, in the one decision whose whole point is that resolution is written down and testable. | Write the tie-break rule in the ADR. |
| **m-8** | Ambiguity | D14 "bounded lock waits" | No bound is given. `pkg/memrooms/index` sets a precedent (`boltOpenTimeout = "5s"`), and `ev` presumably has its own. | State the timeout value, or state that it is configurable and give the default. |
| **m-9** | Incompleteness | D15 PDF row | "Rendered by the browser's native viewer" is not specified as a mechanism. `object-src 'none'` in the current CSP blocks `<object>`/`<embed>`; only an `<iframe src>` navigation works, and only if the response `Content-Type` is `application/pdf` (which `workspaceContentType` does provide). | Name the element and confirm it against the CSP that will actually ship (see C-1). |

### OBSERVATIONS

| ID | Lens | Section | Note |
|---|---|---|---|
| **o-1** | Overcomplexity | D14 tier 2, O-2, §4.2 | Tier 2 is a lock-protocol shim to `ev`, described in the requirements as "deletable the day `ev` retires", **gated on a contract that must first be agreed on another project's side** (O-2), for a reference vault the ADR itself measures at 748 notes — ~1% of the design target. Tiers 1 and 3 alone cover Omnipus's own writers and every uncoordinated writer. Consider deferring tier 2 until O-2 is actually closed; it is the one part of D14 that cannot be built unilaterally. |
| **o-2** | Overcomplexity | D2 | The only stated *need* for KB identity in the marker is "cross-KB search a stable name to attribute results to" — which the mount's own `Name` already provides. "Travels with the folder" is a real property but no requirement in the document depends on it. The template location (D12) is the genuine reason to have the directory; the identity/settings payload may be speculative generality. Justify the settings payload with a concrete setting, or reduce the marker to a template directory plus an empty sentinel. |
| **o-3** | Overcomplexity | D4 + D14 + D13 + `doctor` | Manifest, journal, three lock tiers, drift detection, index eviction on revoke and re-rooting on move are six interacting mechanisms, each with its own failure surface, shipped simultaneously. §4.2's own list of "new failure modes that must be surfaced, not swallowed" runs to five UI states. That is a strong signal for staging (M-18). |
| **o-4** | Insecurity | D15 | Beyond C-2's XSS: rendering agent-authored or web-downloaded HTML **inside the application chrome** is a phishing surface. A page can draw a convincing Omnipus login prompt in the preview pane. An opaque origin stops it reading the session; it does not stop the operator typing into it. Worth an explicit visual boundary requirement (a persistent "untrusted content" chrome on the preview frame). |
| **o-5** | Incompleteness | D7 | No rate limiting or result-size bound is specified for `knowledge_search`. The Library REST surface is already behind `withRateLimit(configLimiter, …)` (`rest.go:5216`); the agent-facing tools are not, and a loop of full-corpus queries at 100k documents is a plausible self-DoS. |
| **o-6** | Incompleteness | D13 revoke | "Revoke deletes the derived index" has no stated cost bound. On a 100k-note KB the index is large; deleting it means the next re-mount pays a full cold rebuild, which D4 spends a whole decision avoiding. Reference counting (M-6) plus a grace period may be cheaper than deletion. |
| **o-7** | Process | header | The ADR is `Proposed`, untracked in git (`?? docs/internal/architecture/ADR-067-…`), and O-2 depends on another project. Per the design-first rule the founder-visible vault design note should exist and be cited before this is ratified; the header cites the requirements doc but no vault design note. |

---

## 3. Structural integrity (structured-spec checks)

| Check | Result | Note |
|---|---|---|
| Every stated goal has acceptance criteria | **FAIL** | No decision in D1–D16 carries a testable acceptance criterion. D6 comes closest and its criterion is infeasible as written (M-4). |
| Cross-references within the document are consistent | **FAIL** | D1 vs O-3 (M-15); D10 vs D14/D11.1/residual 4 (M-5); D15 names two mutually exclusive delivery paths (C-3). |
| Scope boundaries explicitly defined | **PASS** | Office out of scope (D15/A8/A9), graph view out (D8/A7), Librarian out (O-1). Clearly stated. |
| Success criteria measurable | **FAIL** | The central scalability claim carries no threshold (m-4). "Sub-second at 100k" survives only in the requirements doc. |
| Requirements referencing each other are consistent | **FAIL** | See M-5, M-6, C-3. |
| Error/failure scenarios addressed per decision | **PARTIAL** | §4.2 enumerates five new failure modes — genuinely good — but D7's authoring tools, D5's enumeration phase and D14's baseline (M-11) have none. |
| Dependencies between requirements identified | **PARTIAL** | O-2 and O-3 are correctly flagged as blocking. The dependency of everything on `pkg/memrooms/index` generalisation (M-14) and on Constraint #6 seeding (C-5) is not. |
| External constraints honoured | **FAIL** | Hard Constraint #6 unaddressed (C-5), Hard Constraint #8 unaddressed (M-8). Constraints #1/#2 are addressed well (A8). |

---

## 4. Test coverage assessment

The ADR names exactly one test (D6's property test), and that one is infeasible as specified (M-4).
The following are testable-as-written gaps, in the order they would bite:

1. **Untestable as written:** D6 byte-identical rebuild (M-4); D13's evicted-file rule, which has no
   stated detection mechanism to test (m-6); D5's progress string, whose denominator is undefined (M-10).
2. **Negative scenarios missing:** D14 tier-3 refusal (the security-relevant path) has no test named;
   C-4's cross-workspace isolation has none; C-5's boot-coverage assertion has none; D6's *ambiguous*
   link case ("reported as ambiguity — never silently resolved") has none.
3. **Boundary conditions missing:** vault at 0 / 1 / 100k notes; a note filename containing `:` (M-1);
   a wikilink resolving outside the root (M-12); a symlink loop; a 200 MB markdown file; the `limit>50`
   cap (M-2).
4. **Concurrency:** D14 claims the `pkg/entity` pattern, which is proven by real cross-process tests
   (`pkg/entity/store_crossprocess_test.go`, `flock_isolation_test.go`). The ADR must state that KB
   writes inherit that same test shape — the claim "already proven by cross-process tests" is about a
   *different* package and does not transfer for free.
5. **Regression risk not identified:** the ADR never states what existing behaviour could break.
   At minimum: memory-room search (M-14), chat markdown link rendering (M-7), and `web_serve` preview
   token lifetime (C-3) are all at risk and none is named.
6. **False-green exposure:** given `docs/internal/false-green-patterns.md`, D6's property test is the
   highest-risk item in the document — a test that is expensive to make honest and trivial to weaken
   into one that passes unconditionally.

---

## 5. STRIDE summary

| Component | S | T | R | I | D | E | Highest finding |
|---|---|---|---|---|---|---|---|
| Inline Library file serving (D15) | — | — | — | ✔ | — | ✔ | **C-2** — stored XSS on the gateway origin; `Content-Disposition: attachment` is the control being removed |
| `/preview/` token path (D15/C-3) | ✔ | — | — | ✔ | — | — | **C-3** — token-only unauthenticated read; minting for mount paths exposes the operator's real disk |
| `knowledge_search` / `knowledge_graph` (D7) | — | — | — | ✔ | ✔ | ✔ | **C-4** — crosses the workspace isolation boundary; **o-5** — unbounded query cost |
| `knowledge_*` authoring tools (D7/D10) | — | ✔ | ✔ | — | — | — | **M-16** — no audit trail for writes to the operator's real disk; **M-11** — no write baseline |
| Link/embed resolver (D6) | — | ✔ | — | ✔ | ✔ | ✔ | **M-12** — no containment invariant; traversal and symlink escape unaddressed |
| Index store under `$OMNIPUS_HOME` (D3) | — | ✔ | — | ✔ | ✔ | — | **M-3** — unbounded memory on rebuild; index contains full note bodies, so its file permissions matter and are unstated |
| Tool policy surface (C-5) | — | — | — | — | ✔ | ✔ | **C-5** — boot abort on fresh install, silent deny-backfill on upgrade |
| KB marker `.omnipus-vault/` (D1/D2) | ✔ | ✔ | — | — | — | — | An attacker-writable folder placing a marker makes any mounted directory a KB with attacker-supplied templates and settings — unaddressed |

---

## 6. Unasked questions

1. **Which endpoint actually serves preview bytes** — `/preview/` or `/api/v1/library/…/download`? The
   ADR specifies both. (C-3)
2. **What is the KB's identity key?** Realpath, mount id, or workspace+mount? Everything in D3/D13/D14
   depends on the answer and none of them state it. (M-6)
3. **What happens to a note whose filename `pathsafe` refuses?** (M-1)
4. **Are `knowledge_*` tools workspace-scoped?** (C-4)
5. **Which agents get which policy on the new tools, and what happens on upgrade?** (C-5)
6. **What is the memory budget for indexing?** Hard Constraint #3 caps security overhead at 10 MB; this
   feature has no stated budget. (M-3)
7. **What are the index directory's file permissions?** It contains the full text of the operator's
   private notes, stored outside the vault under `$OMNIPUS_HOME`. `pkg/memrooms/index` uses `0o700` on
   the parent; the ADR does not say this is a requirement.
8. **What is the write baseline for detect-before-write?** (M-11)
9. **What does the KB do about `.git`, `.stfolder`, `.stignore`, `.obsidian`, `node_modules`?** §1.3
   names the reference vault as a git repo and Syncthing folder; nothing says these are excluded from
   the walk. On the researched 104k-file vault this is the difference between indexing notes and
   indexing a repository's object store.
10. **What is stage 1?** (M-18)
11. **Does the KB writer respect the sandbox/tool-policy layer at all?** Agent writes into a mounted
    host path already pass through the ADR-063 file-access engine; whether `knowledge_*` tools go
    through that chokepoint or open files directly is unstated — and it determines whether Landlock
    and the policy engine see these writes.
12. **What happens on index-schema change?** D3 makes the index a cache with no authority, but nothing
    versions the mapping. A mapping change in a later release must invalidate every existing index;
    no version field is specified.

---

## 7. What the ADR gets right

Recorded so the revision does not lose it: A1–A13 is a genuinely strong rejected-alternatives
table; the D5 partial-results decision and the A10/A11 rejections correctly apply the ADR-061
silent-degradation lesson; §4.3 "Explicitly worse than before" and §6 residuals are the kind of
honest self-accounting most ADRs omit; and the D10 frontmatter-rewriting decision is backed by a
real measurement (87% of 748 notes) rather than an assertion.

---

## Verdict

**BLOCK** — 5 critical findings.

C-1, C-2 and C-3 mean D15 as written cannot be built on the path it names. C-4 is a
cross-workspace data-exposure hole. C-5 breaks gateway boot on fresh installs and silently
disables the feature on upgrades.

Address the findings above, then re-run:

```
/grill-spec /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-library-improvements/docs/internal/architecture/ADR-067-omnipus-knowledge-base-and-render-first-preview.md
```
