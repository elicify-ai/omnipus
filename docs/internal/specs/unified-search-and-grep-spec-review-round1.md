# Adversarial Review: Unified Library Search & Grep Engine — Implementation Spec (Round 1 of 2)

**Spec reviewed**: `docs/internal/specs/unified-search-and-grep-spec.md` (@ `6a4492fb3`, worktree `wt-integrate`, branch `integrate/library-improvements-v0.1.1`)
**Source ADR**: `docs/internal/architecture/ADR-081-unified-library-search-and-grep-engine.md`
**Review date**: 2026-09-07
**Review mode**: plan-spec (BDD + FR/SC IDs + traceability matrix detected)
**Verdict**: **BLOCK**

Every code-fact claim in the spec's §2 table was re-verified against the live worktree
(commit pins `f23f18ffb` and `f37346338` both confirmed ancestors of HEAD `6a4492fb3`).
Verification results are inline per finding; the §2 table is largely accurate — the
blocking problems are in what the spec *doesn't* say, not in what it says.

## Executive Summary

The spec's grounding is good: 12 of 12 §2 code-fact rows check out against the tree
(one trivial line-number nit). But three CRITICAL gaps would ship real damage: (1) the
consolidation silently deletes vault **attachment search** — a shipped capability the
spec never mentions because it mislabels the retired box "notes-only"; (2) the new
file-search endpoint and grep tool specify **no rate limiting, no server-side
cancellation, and no concurrency cap** for an operation orders of magnitude more
expensive than the knowledge endpoints that *are* rate-limited today; (3) the engine
bounds inputs scanned but **never bounds output size**, so a single grep can inject a
multi-megabyte tool result into the agent context that windowTrim cannot evict.
Nine MAJOR findings follow, led by an under-enumerated honesty-parity clause and an
incomplete tool-policy ruling that leaves the Worker's posture to silent ceiling
inheritance — the exact hole `tool_policy_catalog_drift.go` exists to document.

| Severity | Count |
|----------|-------|
| CRITICAL | 3 |
| MAJOR | 9 |
| MINOR | 10 |
| OBSERVATION | 5 |
| **Total** | **27** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] The consolidation silently deletes vault attachment search

- **Lens**: Incorrectness + Incompleteness
- **Affected section**: US-1 AS-4 ("the old **notes-only** search box is absent"), §1 workstream A, MV-9, FR-010/FR-012, §4 non-behavior "the consolidation must not reduce honesty"
- **Description**: The retired box is **not** notes-only. The old endpoint's hit
  contract enumerates `kind: [note, attachment]`
  (`contracts/components/schemas/KnowledgeSearchHit.yaml:40-42`), the shared engine
  itself defines `KindAttachment` (`pkg/records/knowledgefind/request.go:70` — "The
  store's own kind column only ever holds note or attachment"), and the SPA has a
  dedicated invariant test for it
  (`src/components/library/knowledge/KnowledgeSearch.attachment.test.tsx`, ADR-067
  FR-039a / FR-050a(a): an attachment hit is matched on name/path, its bytes never
  read, and it carries the `attachment_not_read` disclosure). The surviving endpoint
  queries the engine with `KindNote` and `KindRecord` only
  (`pkg/gateway/rest_knowledge_find.go:165,237`) and `VaultSearchResponse` has no
  attachment group (`contracts/components/schemas/VaultSearchResponse.yaml`). The
  spec's own A1 defers the Files kind in vaults, so no other surface picks this up in v1.
- **Impact**: A person who today finds `diagram-v3.png` by typing its name in a vault
  gets **zero results** after this ships, with no notice, and the guard test that would
  have caught it is deleted with its component (spec test 24: "old component files
  deleted"). This is precisely the silent-capability-loss pattern this repo treats as
  a release blocker (the ADR-037 precedent the project cites for "looks fine, does
  nothing"). The ADR shares the defect — D2 claims only two signals separate the two
  endpoints — so the spec cannot lean on the ADR here.
- **Recommendation**: Add an explicit ruling to §3/MV-9: extend the `/knowledge/find`
  path and `VaultSearchResponse` with an attachments group (the engine already
  supports `KindAttachment`; this is one more `runVaultSearchFind` call plus one
  additive array), carry the name-only disclosure ("matched on file name — contents
  never read"), and port `KnowledgeSearch.attachment.test.tsx`'s invariant into the
  surviving bar's suite. If instead the founder deliberately drops vault attachment
  search until Files-in-vault lands, the spec must say so out loud (release-notes
  item + a dated follow-up), and US-1 AS-4's "notes-only" wording must be corrected
  either way.

---

#### [CRIT-002] No rate limit, no server-side cancellation, no concurrency cap on an expensive live walk

- **Lens**: Insecurity (STRIDE: DoS) + Incompleteness
- **Affected section**: §3 edge case "Concurrent searches … each request independent; no shared mutable state; older in-flight UI responses discarded"; §4 integration boundaries; §6 tests 13–15; MV-1
- **Description**: Three related omissions:
  1. **Rate limiting.** The *cheaper* sibling endpoints are already rate-limited:
     `/knowledge/find` calls `allowKnowledgeRetrieval` (`pkg/gateway/rest_knowledge_find.go:105`),
     backed by `knowledgeRESTLimiter` with a 429 + Retry-After contract
     (`pkg/gateway/rest_knowledge.go:93,742-744`), and the agent knowledge tools get
     their own `knowledgeToolLimiter` (`rest_knowledge.go:734`). The new endpoint —
     an uncached filesystem walk of up to 50,000 files / 256 MiB / 10 s per request —
     specifies no limiter for either surface, despite ADR-081 D11 naming
     `rest_knowledge_find.go` as "the REST endpoint pattern to follow".
  2. **Server-side cancellation.** The spec's only concurrency treatment is
     *client-side*: "older in-flight UI responses discarded (out-of-order guard)".
     Discarding a response does not stop the walk. The SPA already passes an
     `AbortSignal` (`src/components/library/search/useVaultSearch.ts:60,190-194`) and
     Go's http server cancels `r.Context()` on client disconnect — but nothing in the
     spec requires the engine to observe context cancellation *mid-walk* (test 6
     covers deadline, not client abort).
  3. **Concurrency cap.** "Each request independent" is the opposite of a cap: a
     person typing an 8-character query with a short debounce can have several
     parallel walks live at once, each with its own worker pool (D5 "parallel
     scanning"), on a gateway that is also running the agent loop and channels.
- **Impact**: Self-inflicted resource exhaustion on the operator's single binary
  (Hard Constraint #3), and a remotely drivable one on any reverse-proxied install:
  an authenticated client looping the endpoint pins CPU/disk indefinitely. On the
  2–4-core CI worker the e2e spec (test 26) will *also* be fighting its own
  keystroke-driven walks.
- **Recommendation**: (a) Wrap the endpoint in a retrieval rate limiter following
  `allowKnowledgeRetrieval` (per-workspace key, 429 contract), and give the tool path
  the `knowledgeToolLimiter` treatment; (b) add an FR: the engine MUST observe
  `ctx.Done()` between files and between chunks, and the REST handler MUST pass
  `r.Context()` so an aborted keystroke's walk stops (add a test:
  client-disconnect cancels the walk); (c) add a small global semaphore (e.g. 2
  concurrent searches per gateway) with a documented queue-or-429 behavior. Update
  the edge case so "independent" no longer reads as "unlimited".

---

#### [CRIT-003] Output size is unbounded — a grep result can blow the agent context in one turn

- **Lens**: Incompleteness + Infeasibility
- **Affected section**: MV-3 (six bounds — all input-side), US-3 AS-1 ("path, line number, line text"), §3 edge case "extremely long lines ⇒ excerpt is windowed; response stays bounded", §6 test 5
- **Description**: Every MV-3 bound caps what is *scanned* (files, bytes, matches,
  depth, per-file size, wall clock). Nothing caps what is *returned*. The excerpt
  windowing edge case speaks of "excerpt" (the human hit shape) with no window size,
  and US-3 promises the agent the raw "line text". A matched line inside a 4 MiB
  minified file can be ~4 MiB long; 1,000 matches at even a few hundred bytes each is
  hundreds of KiB of tool result. Tool results land inside the *current* turn, and
  `windowTrim` (`pkg/agent/loop.go::windowTrim`) evicts only whole *older* turns — it
  cannot save a turn whose own tool result exceeds the model's context. The repo's own
  precedent caps tool output: bash truncates at 64,000 chars
  (`pkg/tools/shell.go:589`, `truncateOutput` applied at `shell.go:1728`).
- **Impact**: One agent grep of an unlucky tree produces a failed or wildly expensive
  LLM call — an incident, not a quality nit. The REST surface has the equivalent
  problem as a multi-megabyte JSON response into the SPA.
- **Recommendation**: Add a seventh bound to MV-3: **max response payload bytes**
  (suggested defaults: 512 KiB REST, and a much lower tool-result budget in the
  bash class — e.g. 48–64 K chars, matching `truncateOutput`), plus a **per-line
  window length** (e.g. 512 bytes around the match, applied to `line`/`excerpt` on
  both surfaces). Hitting either sets `truncated: true` with reasons `max_output` /
  per-hit `line_windowed`. Extend test 5's matrix and DS-2 accordingly. Also lower
  the agent tool's *default* match cap below the shared 1,000 (e.g. 200) — an agent
  can page; a human can scroll.

---

### MAJOR Findings

#### [MAJ-001] The honesty-parity clause enumerates 3 signals; the retired surface carries at least 6

- **Lens**: Incompleteness + Inconsistency
- **Affected section**: MV-9, FR-010, §4 non-behavior "every signal the retired bar showed (partial, X-of-Y, capped) must exist in the surviving bar", DS-4, test 20/22
- **Description**: Verified against the retired contract and hook, the old surface
  carries: (1) the X-of-Y coverage pair with `total_known`
  (`KnowledgeSearchIncompleteness.yaml`: `indexed_files`/`total_files`, FR-036 "never
  invent a denominator"); (2) a **server-authored `statement` sentence** ("Server-
  authored so the client cannot phrase an incomplete answer as a complete one",
  required, minLength 1); (3) client-derived capped-at-limit
  (`useKnowledgeSearch.ts:172`); (4) **`limit_clamped` + `limit_requested`** — which
  the hook's own doc comment says is *deliberately a different question* from capped
  (`useKnowledgeSearch.ts:155-159`, FR-037 "the clamp is REPORTED, never silent");
  (5) **per-hit `excerpt`/`excerpt_unavailable`** with a 5-member reason enum
  (`KnowledgeSearchHit.yaml` — `file_unreadable`, `file_missing`, `match_moved`,
  `budget_exhausted`, `attachment_not_read`); (6) the attachment name-only
  disclosure (CRIT-001). MV-9 ports (1) partially and (3); the spec's parity
  sentence lists only three signal names. Note also `VaultSearchNoteHit` has a bare
  optional `snippet` with no absence-reason (`schemas.ts:887-891`), so the
  bare-title-over-empty-space defect FR-050a(a) fixed returns in the surviving bar.
  Additionally, FR-035's shape property — incompleteness is *required*, so results
  cannot arrive without their qualifier — is lost by making MV-9's fields optional.
- **Impact**: Implemented as written, the surviving bar is measurably less honest
  than the one it replaces — inverting the spec's own non-behavior and the founder's
  stated reason for keeping the old UX.
- **Recommendation**: Rewrite MV-9 as a complete port table: for each of the six
  signals, either the new field(s) that carry it or an explicit dated retirement
  ruling. At minimum add: the server-authored coverage statement (or state that the
  SPA composes it from counts and accept FR-036's risk explicitly), `limit_clamped`/
  `limit_requested` on `VaultSearchResponse`, and `excerpt_unavailable` (or a
  reduced enum) on `VaultSearchNoteHit`. Extend DS-4 with the clamp and
  excerpt-absent states.

#### [MAJ-002] A4 rules 4 of the seeded agents; the Worker's posture falls to silent ceiling inheritance — the documented upgrade hole

- **Lens**: Insecurity (Elevation) + Incompleteness
- **Affected section**: A4, MV-8, FR-009, test 19
- **Description**: A4 sets ceiling `allow` and per-agent postures for Jim/Mia/Ava/Ray
  only. The seeded roster is larger: the Worker, the specialists, and the System
  Agents. The knowledge-family precedent A4 itself invokes ("same class as
  `knowledge_find`") explicitly **denies** the Worker the whole knowledge family with
  a written rationale — "the Worker id is occupied by every generic delegated session
  in the installation at once, so a grant to 'the Worker' is a grant to all of them"
  (`pkg/coreagent/core.go:846-859`). The Worker's map is sparse-by-design: any tool
  it does not name inherits the global ceiling (`core.go:731-740`), so an unruled
  `grep` = a silent `allow` to every delegated session. That silent-ceiling-grant
  path is the exact hole `pkg/coreagent/tool_policy_catalog_drift.go` documents at
  length. Separately, MV-8 pins coverage on "a **fresh install**" only; the
  knowledge family needed dedicated *upgrade* pins
  (`pkg/coreagent/seed_upgrade_catalog_drift_test.go:225,289` —
  `TestUpgrade_KnowledgeTools_ResolveToTheSeededPosture_NotTheGlobalCeiling`,
  `…_EveryAgentCarriesAnExplicitEntry`), which the spec's test 19 does not mirror.
- **Impact**: An upgraded install hands every generic delegated worker session
  read access to every mounted host folder in whatever workspace it runs in, by
  omission rather than decision — and no named test would notice.
- **Recommendation**: Extend A4 to rule every seeded identity: Worker (explicit
  `allow` or `deny` — write the rationale either way; the read-only+confined argument
  supports `allow`, the knowledge precedent supports `deny`; this is the founder call
  A4 already flags), specialists (their `denyAllThenOverride` enumeration — state the
  intended value), and System Agents (`systemAgentSeed`). Add MV-8b: "on an upgraded
  install, every pre-existing agent resolves `grep` to its seed's posture, not the
  ceiling" with a test 19b modeled on `seed_upgrade_catalog_drift_test.go`.

#### [MAJ-003] Pattern semantics per surface are undefined; smart-case exists only in test rows

- **Lens**: Ambiguity
- **Affected section**: US-2/US-3, DS-1 rows 2/6/7, test 2, §4 behavioral contract
- **Description**: Nothing in Phases 1–5 says whether the *human bar's* query is a
  literal or a regex. DS-1 #7 says pattern `(` ⇒ **400 with a parse error** — if that
  applies to the bar, a person typing "(" or "C++ (draft)" gets an error banner
  mid-keystroke. Smart-case (lowercase ⇒ insensitive, uppercase ⇒ sensitive) appears
  *only* in test 2 and DS-1 #2 — no FR, no behavioral-contract line, no statement of
  whether it applies to the human bar, to name matching, or only to agent content
  grep. Name-match semantics (substring? case rules?) are similarly implied only by
  US-2 AS-1's example. Two competent engineers will build different bars.
- **Impact**: The UI either 400s on ordinary punctuation or silently diverges from
  the agent tool's semantics; smart-case ships (or doesn't) by accident of which
  test the implementer reads first.
- **Recommendation**: Add to §4: "The human bar's query is always a LITERAL
  (server-side escaped); invalid-regex 400s are an agent/API-only outcome. Content
  matching is smart-case on both surfaces; name matching is case-insensitive
  substring." Promote smart-case to an FR (or delete it from the tests). State which
  DS-1 rows apply to which surface.

#### [MAJ-004] The wire contract's field sets are never enumerated, and the truncated_reason enum is internally inconsistent

- **Lens**: Ambiguity + Inconsistency
- **Affected section**: §6 preamble (contracts list), MV-3, BDD "every bound truncates honestly" outline, FR-013
- **Description**: For a contract-first spec (Constraint #8), `FileSearchRequest`,
  `FileSearchResponse` and `FileSearchHit` are named but never fielded: nothing
  specifies the scope path field, glob include/exclude fields, the hidden-files
  flag (see MIN-008), the bounds-override fields and their clamping, kind of hit
  (name vs content — one schema or a discriminator?), or the full
  `truncated_reason` enum. What *is* stated is inconsistent: MV-3 has **six** bounds
  but the BDD outline's Examples table has **five** reasons (per-file cap missing),
  and it is undefined whether a per-file-cap skip sets response-level
  `truncated: true` (it should — content coverage was reduced — but nothing says so).
  If hits become a `oneOf` union, the discriminator-wrapper-inline-in-openapi.yaml
  rule (ADR-034 precedent) applies and should be called out before round 2.
- **Impact**: The contract gets designed ad hoc during implementation — the exact
  failure mode Constraint #8's 5-step process exists to prevent; test 5's "exact
  reason token" assertions have no authoritative token list to assert against.
- **Recommendation**: Add a schema sketch section (field name, type, required,
  default, clamp) for the three schemas plus the full reason enum
  `{max_files, max_bytes, max_matches, max_depth, per_file_cap, deadline, max_output}`,
  and a one-line ruling on flat-hit-with-kind vs discriminated union (flat with a
  `kind` field avoids the ADR-034 trap entirely — prefer it).

#### [MAJ-005] US-4 AS-1 contradicts the architecture at the Library root

- **Lens**: Inconsistency + Infeasibility
- **Affected section**: US-4 AS-1 ("Given the person is at the Library root … the search bar is present"), FR-001, SC-001
- **Description**: The endpoint is per-workspace
  (`POST /library/{workspace_id}/files/search`) and the bar's existing null-workspace
  state is *disabled by design* (`LibrarySearchBar.tsx:81`: "null = the Library
  virtual root — the bar renders disabled"). There is no cross-workspace search
  surface anywhere in the design. "Present" at the root can therefore only mean
  "present and permanently inert", which fails US-4's own independent test ("the bar
  never disappear[s]" reads as *usable* everywhere) and makes SC-001's root-view DOM
  audit assert a dead control.
- **Impact**: Either an implementer invents an unspecified cross-workspace fan-out,
  or ships a visibly dead search box at the top-level screen — both wrong.
- **Recommendation**: Rule it: at the virtual root the bar renders disabled with a
  hint ("open a workspace to search") — and say so in AS-1 — or descope the root from
  FR-001/SC-001. (Cross-workspace search is new scope; if wanted, it needs its own
  ADR note, not a side effect.)

#### [MAJ-006] The grep tool's workspace scoping is never stated

- **Lens**: Insecurity (Information Disclosure) + Ambiguity
- **Affected section**: US-3, FR-008, §4 integration boundaries (agent runtime row)
- **Description**: US-3 says "across the same confined tree" without saying *which*
  tree. The knowledge-tool precedent resolves the calling agent's own workspace
  (`pkg/vaultprops.FindTool` via the agent's context); nothing in the spec forbids a
  `workspace_id` parameter on the tool. Mounts point at the founder's real host
  folders, so a cross-workspace-capable grep would let any grep-allowed agent read
  any workspace's mounted host tree, sidestepping the workspace-scoped delegation
  trust model (ADR-037).
- **Impact**: A single convenience parameter added during implementation quietly
  becomes a cross-workspace read primitive.
- **Recommendation**: Add an FR: "The `grep` tool resolves the CALLING agent's
  current workspace root only; it accepts a path scope *within* that root and MUST
  NOT accept a workspace identifier." Assert it in test 16 (a workspace_id-shaped
  argument is rejected/ignored).

#### [MAJ-007] Test 11 (unreadable file) will never exercise its error path on CI, which runs as root

- **Lens**: Infeasibility (test plan)
- **Affected section**: §6 test 11 `TestFileGrep_UnreadableSkippedCounted`, DS-3
- **Description**: CI runs as root (`pkg/gateway/rest_agent_sessions_test.go:118`
  "…under CI, which runs as root"; CAP_DAC_OVERRIDE noted at
  `rest_plan_task_restart_test.go:603`; `os.Geteuid()` skip at
  `rest_stats_test.go:679-680`). A chmod-000 file is perfectly readable to root, so
  the test either false-passes or skips — meaning the skip+count behavior (an
  Error-Path BDD scenario and an explicit edge case) is *never verified where it
  counts*. The spec's parenthetical "(skipped as root — reuse the symlink variant
  per repo precedent)" gestures at this but leaves the canonical mechanism unnamed,
  and DS-3 still says nothing about it.
- **Impact**: A green suite that never ran the guarded path — the false-green
  pattern `docs/internal/false-green-patterns.md` exists to prevent.
- **Recommendation**: Make the *always-run* injection a dangling symlink (open fails
  ENOENT for root too) or a swappable open-error seam in the engine, and assert the
  skip-count on that. Keep the chmod-000 variant as an additional case gated on
  `os.Geteuid() != 0`. Rename/redescribe test 11 accordingly so the primary
  assertion is not the skippable one.

#### [MAJ-008] DS-2 is infeasible as written — it builds 50,001-file trees against production bounds

- **Lens**: Infeasibility (test plan)
- **Affected section**: DS-2, §6 test 5, SC-002
- **Description**: DS-2 prescribes "one engineered tree per bound — 50,001 files; a
  5 MiB text file; 1,001 matching lines; depth-33 nesting", i.e. exercising the
  *production* defaults. Creating 50k files per test run on the shared CI worker
  (whose disk has hit 96% and which runs e2e shards in parallel) is minutes of I/O
  and flake surface; SC-002's "wall-clock ≤ deadline + 1 s" with a 10 s deadline
  legitimizes 11-second unit tests.
- **Impact**: Either the suite gets slow and flaky, or the implementer quietly skips
  the bounds matrix — both bad.
- **Recommendation**: Require the engine to take an injectable `Bounds` struct and
  rewrite DS-2 to tiny injected caps (max_files=100 → 101 files; per-file cap=4 KiB
  → 5 KiB file; matches=10 → 11 lines; depth=3 → depth-4; deadline via the existing
  1 ms context). Keep ONE integration-level case at realistic scale if desired, in
  the e2e shard, not the unit matrix.

#### [MAJ-009] SC-003's 2-second gate will flake on the shared, parallel-sharded CI worker

- **Lens**: Infeasibility + Inoperability
- **Affected section**: SC-003, A6, §6 test 26
- **Description**: The e2e gate fans out parallel gateways on one Fly worker
  (`scripts/e2e-shards.sh list` → `runci.sh` parallel shards; worker is
  2–4 cores). A 2 s wall-clock assertion for a 10k-file / 64 MiB grep measured
  *during* parallel shard execution is load-dependent — the environment-artifact
  phantom-failure class `docs/internal/false-green-patterns.md` documents. The spec
  already flags the number (A6) but still lists SC-003 among gating criteria.
- **Impact**: Recurring red builds nobody trusts, then a quiet threshold bump — the
  worst version of a perf gate.
- **Recommendation**: Re-baseline at grill as A6 invites: make SC-003 a **solo-run**
  perf smoke (the shard plan already supports `solo: true`) with a generous
  threshold (e.g. p95 ≤ 5 s) or a non-gating tracked benchmark. Never measure it
  inside the parallel shard fan-out.

---

### MINOR Findings

#### [MIN-001] Retirement blast-radius inventory is incomplete

- **Lens**: Incompleteness
- **Affected section**: §2 impact table rows 1–3, US-5, FR-011
- **Description**: Verified omissions: (a) `pkg/gateway/inboundschemas/` holds an
  embedded *copy* of every contract schema, auto-synced by
  `scripts/gen-contracts.sh:117-123` (Step 5) and drift-gated by `Makefile:492` —
  the retired `KnowledgeSearch*.yaml` copies vanish on regen, but the spec should
  say the sync covers it so nobody hand-deletes; (b) `src/lib/api.knowledge.test.ts`
  tests `searchKnowledge` directly (`:22,156`) — a client-test deletion not implied
  by "handler + its tests"; (c) `KnowledgePanel`'s `searchFn` prop is *typed off the
  deleted component* (`KnowledgePanel.tsx:198`:
  `React.ComponentProps<typeof KnowledgeSearch>['searchFn']`, threaded at `:218,336`)
  — removal is a prop-API change to KnowledgePanel, not just an unmount; (d) on the
  add side, the grep tool must enter the coverage universe via
  `buildKnownBuiltinToolNames` (`pkg/gateway/gateway.go:1261-1286` — the knowledge
  family needed an explicit union at `:1321` plus
  `registerKnowledgeBuiltinMetadata`, `pkg/gateway/knowledge_tools_wire.go:112`);
  `GeneralBuiltinMetadata` contains no knowledge tools today, so "mirror the
  knowledge family" means touching those two gateway sites too.
- **Recommendation**: Extend the impact table with (a)–(d). The drift tests will
  catch (d) loudly, but the spec is the implementer's map.

#### [MIN-002] Test 17 grafts grep onto a pin test whose documented contract is "the six ADR-068 knowledge tools"

- **Lens**: Inconsistency
- **Affected section**: §6 test 17, MV-7
- **Description**: `TestVisibility_KnowledgeToolsAreSearchOnly`'s 27-line doc comment
  is entirely about the ADR-068 six ("All SIX resolve the same way…",
  `pkg/tools/manifest_test.go:845-872`). The tier *mechanism* works as the spec
  claims (absent from `fullManifestToolNames`/`infraManifestToolNames` ⇒ Lazy,
  `manifest.go:93-100`; not previewed ⇒ SearchOnly, `manifest.go:241-253`), but
  extending the knowledge-family list muddies a deliberately narrow pin and stales
  its comment.
- **Recommendation**: Write a sibling `TestVisibility_GrepIsSearchOnly` (same four
  asserts) instead of extending the family list; cite ADR-081 D11 in its comment.

#### [MIN-003] Gitignore pruning: FR-007, A2 and the edge case state three different scopes

- **Lens**: Inconsistency
- **Affected section**: FR-007, A2, §3 edge case ".gitignore present"
- **Description**: FR-007 mandates pruning "from **content** search"; A2's accepted
  default prunes "for both name+content"; the edge case says name search "MAY still
  surface ignored entries only if explicitly requested" (a toggle A2 says doesn't
  exist in v1). Three positions, one behavior.
- **Recommendation**: Make FR-007 say "from BOTH name and content search (v1: no
  surfacing toggle)"; delete the MAY from the edge case; keep A2 as the record of
  the deferral.

#### [MIN-004] Gitignore semantics and library choice left open

- **Lens**: Ambiguity
- **Affected section**: O1 ("hand-rolled or `sabhiram/go-gitignore`"), FR-007, test 8
- **Description**: Unspecified: nested `.gitignore` files vs root-only; negation
  (`!keep.md`); `.ignore` (D5 mentions it, spec doesn't); behavior in non-git trees
  (most Library workspaces and mounts are not repos — does a stray `.gitignore` in a
  mounted Documents folder silently hide files from search?). Hand-rolling full
  gitignore semantics is a known bug farm.
- **Recommendation**: Pick the library (recommend `sabhiram/go-gitignore` per O1's
  own lean), and specify: nested files honored, negations honored, applies wherever
  a `.gitignore` exists regardless of git-ness — with the skip *counted* in the
  problem/pruned summary so hidden-by-ignore is observable, not silent.

#### [MIN-005] Missing negative coverage: MV-5 has no test; MV-1 omits the 401 case

- **Lens**: Incompleteness (test plan)
- **Affected section**: MV-1, MV-5, §6 tests 13–14
- **Description**: MV-5 (arrays always `[]`, never null) maps to no test row —
  contract_test.go covers Go marshalling generally, but the zero-hit handler path
  deserves an assert in test 13. MV-1's taxonomy (400/403/404) omits 401
  unauthenticated, even though ADR-081 D11 names "auth wrap" as part of the pattern
  to copy.
- **Recommendation**: Add empty-result array asserts to test 13 and a 401 row to
  test 14.

#### [MIN-006] The pathological-pattern BDD scenario has no TDD row

- **Lens**: Incompleteness (traceability)
- **Affected section**: BDD "RE2 pathological pattern cannot hang", DS-1 #4, SC-004, MV-4
- **Description**: The scenario and dataset exist; no numbered test implements them
  (tests 2/5/6 cover regex semantics, bounds, and deadline — none feeds a 64 KiB
  alternation bomb).
- **Recommendation**: Add `TestFileGrep_PathologicalPattern64KiB` (reject at parse,
  or complete within a short injected deadline) and reference it from MV-4's row in
  the matrix.

#### [MIN-007] Whole-root/mount disappearance mid-walk is not distinguished from a per-file skip

- **Lens**: Incompleteness
- **Affected section**: §3 edge cases (deleted file, unreadable file), §4 integration boundaries
- **Description**: The per-file TOCTOU/unreadable rules are good. Unaddressed: the
  walk root itself failing mid-search (mount target unplugged, network share drops).
  Under the per-file rule, every subsequent entry "skips into the problem count" and
  the response reads as "0 matches (N problems)" — technically honest, practically
  misleading for the founder's daily-mount reality.
- **Recommendation**: One rule: an error opening/reading the *walk root or a mount
  root* mid-search yields a request-level error (or `truncated` with reason
  `root_lost`), never a quiet empty result.

#### [MIN-008] Hidden-file handling for search names a rule but not a wire default

- **Lens**: Ambiguity
- **Affected section**: §3 edge case "Hidden files ⇒ follow the Library's existing hidden-file visibility rule", DS-3
- **Description**: The existing rule is a *parameter* — `Root.List(rel,
  includeHidden)` (`pkg/library/entries.go:19-25`, dot-prefix definition) — the
  caller decides. Search therefore needs its own request flag and default. Note the
  default (false) is also what keeps `.git`, `.library`, `.omnipus-vault` out of
  content scans — worth stating as a property, not an accident.
- **Recommendation**: Add `include_hidden` (default false) to the request sketch
  (MAJ-004) and note the `.git`-pruning consequence; DS-3's "hidden file per
  visibility flag" then has a concrete flag to exercise.

#### [MIN-009] Small reference drift in §2

- **Lens**: Incorrectness (trivial)
- **Affected section**: §2 rows 3; spec header
- **Description**: All §2 claims verified except nits: `rest_knowledge.go:151` is the
  `case "search":` label; the dispatch call is at `:156` (handler at `:461` correct).
  Header pins the spec to `f23f18ffb` while the ADR validates against `f37346338`
  and the worktree HEAD is `6a4492fb3` — all three confirmed on the same
  first-parent line, but the spec should say the pins are intentionally different
  (ADR validated pre-spec-commit).
- **Recommendation**: s/151/151 (case) + 156 (dispatch)/; add one line noting the
  pin lineage.

#### [MIN-010] One 10 s deadline default for two very different callers

- **Lens**: Ambiguity + Inoperability
- **Affected section**: MV-3, US-2 AS-3, H4
- **Description**: MV-3's single 10 s default serves both a keystroke-driven UI
  (where 10 s of silence is an eternity — no progressive delivery is specified, so
  nothing renders until the walk returns) and an agent turn (where 10 s is fine).
  The spec allows downward override per request but never says the SPA should use
  one (e.g. 2–3 s interactive) — so it won't.
- **Recommendation**: State the SPA's interactive deadline override (suggest 3 s)
  and note explicitly that progressive/streamed results are out of scope for v1
  (so the choice is recorded, not accidental).

---

### Observations

#### [OBS-001] Tool name "grep" is safe

- **Lens**: Incorrectness (checked, clear)
- **Affected section**: §6 preamble
- **Suggestion**: No catalog collision — the only existing "grep" string is a shell
  allowlist literal (`pkg/tools/shell.go:1300`). Bare-verb naming matches
  `bash`/`navigate`/`delegate` precedent. Keep it.

#### [OBS-002] Workstream A is independently shippable — consider staging

- **Lens**: Inoperability
- **Affected section**: §1 scope
- **Suggestion**: The consolidation (A) touches only existing engines and could land
  before the engine (B); if B slips, US-4's "bar everywhere" is the only casualty.
  Sequencing A → B in the task decomposition de-risks the release.

#### [OBS-003] Ranking parity between old and new knowledge search is asserted nowhere

- **Lens**: Incompleteness
- **Affected section**: US-1, MV-9
- **Suggestion**: Old hits carry a relevance `score` ("best-scored first",
  `KnowledgeSearchHit.yaml`); `VaultSearchNoteHit` has none and the find path's
  ordering guarantee is unstated. Both drive the same engine, so this is likely
  fine — add one sentence stating result ordering is the engine's relevance order,
  so the port can't accidentally regress to path order.

#### [OBS-004] Bounds configurability should be an explicit non-decision

- **Lens**: Overcomplexity (guarding against it)
- **Affected section**: MV-3
- **Suggestion**: MV-3's defaults are per-request-overridable but not
  operator-configurable. That is the right v1 call — say "not config-exposed in v1"
  so nobody adds six config keys during implementation (unnecessary configurability).

#### [OBS-005] A4's "needs founder nod at grill review" cannot be satisfied by this review

- **Lens**: Process
- **Affected section**: A4
- **Suggestion**: A grill review is adversarial QA, not founder sign-off. Route A4
  (now including the MAJ-002 roster) to the founder explicitly before round 2, and
  record the ruling in the spec.

---

## Structural Integrity (plan-spec mode)

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | US-1..US-5 all have AS lists |
| Every acceptance scenario has BDD scenarios | PASS (weak spots) | US-2 AS-4/US-3 AS-2 covered; US-5 AS-2 (SPA bundle audit) has no BDD/test beyond test 21's compile-time implication |
| Every BDD scenario has `Traces to:` reference | PASS (1 nit) | "unreadable file is skipped and counted" traces to "Edge cases" rather than a US/AS ID |
| Every BDD scenario has a test in TDD plan | **FAIL** | "RE2 pathological pattern cannot hang" has no test row (MIN-006) |
| Every FR appears in traceability matrix | PASS | FR-001..FR-015 all present |
| Every BDD scenario in traceability matrix | **FAIL** | unreadable-file scenario absent; pathological maps only to SC-004 |
| Test datasets cover boundaries/edges/errors | **FAIL** | DS-2 infeasible as written (MAJ-008); DS-1 output-size boundary absent (CRIT-003); DS-4 misses clamp/excerpt-absent states (MAJ-001) |
| Regression impact addressed | PASS (gap) | §6 regression list is real, but the deleted attachment invariant has no replacement (CRIT-001) |
| Success criteria are measurable | PASS (1 risk) | SC-003 measurable but flake-prone as gated (MAJ-009) |

## Test Coverage Assessment

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Output bounding | No test caps response/tool-result size or line window | CRIT-003 |
| Concurrent requests | Test 12 covers intra-search parallelism only; no two-simultaneous-searches or cancel-on-disconnect test | CRIT-002 |
| Auth negative | No 401 case | MIN-005 |
| Upgrade-path governance | No grep analog of `TestUpgrade_KnowledgeTools_*` | MAJ-002 |
| Error injection on root CI | chmod-000 never exercises the path as root | MAJ-007 |
| Contract nullability | MV-5 unmapped | MIN-005 |

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| `POST /library/{ws}/files/search` | ok (auth wrap implied — make explicit, MIN-005) | ok (read-only) | ok | ok (confined by `os.Root`; FR-014) | **risk** (no limiter/cancel/concurrency — CRIT-002) | ok | Bounds are per-request, not per-client |
| `grep` agent tool | ok (agent identity) | ok | ok (audited, FR-008) | **risk** (workspace scoping unstated — MAJ-006) | **risk** (limiter absent; output unbounded — CRIT-002/003) | **risk** (Worker ceiling inheritance — MAJ-002) | |
| `pkg/filegrep` engine | n/a | ok | n/a | ok (symlink/no-follow specified + tested) | **risk** (regex safe via RE2, but output & concurrency unbounded) | n/a | RE2 choice is the right ReDoS control |
| Consolidated bar (SPA) | ok | ok | n/a | ok | ok (debounce + abort exist client-side) | n/a | Honesty regression is the risk here (CRIT-001/MAJ-001), not STRIDE |

**Legend**: risk = identified threat not mitigated in spec, ok = adequately addressed or not applicable

## Unasked Questions

1. What happens to **attachment** search in vaults? (CRIT-001 — the spec doesn't know the capability exists.)
2. Is the human bar's query a regex? What does a person typing `(` see? (MAJ-003)
3. What posture does the **Worker** (and each System Agent/specialist) get for `grep`, and what stops an upgraded install from granting it via the ceiling? (MAJ-002)
4. Who stops the server-side walk when the person keeps typing? (CRIT-002)
5. Why is the new, more expensive endpoint the only Library retrieval surface without a rate limiter? (CRIT-002)
6. What is the grep tool's output budget in bytes/tokens, given windowTrim cannot evict the current turn? (CRIT-003)
7. What exactly does the bar do at the Library root, where no workspace is selected? (MAJ-005)
8. Can a `grep` call name a workspace other than the calling agent's? (MAJ-006)
9. Does a per-file-cap skip set `truncated: true`? What is the complete reason-token enum? (MAJ-004)
10. Does a stray `.gitignore` in a mounted (non-git) Documents folder silently hide the founder's files from search? (MIN-004)

## Verdict Rationale

**BLOCK.** The spec's codebase grounding is unusually solid — every load-bearing §2
claim verified against the tree — but three findings are shipping-incident class:
CRIT-001 deletes a working, founder-visible capability (vault attachment search)
while deleting its guard test, in direct violation of the spec's own no-reduced-
honesty clause; CRIT-002 ships an expensive, unlimited, uncancellable walk on a
single binary whose cheaper siblings are already rate-limited; CRIT-003 lets one
tool call blow the agent's context window because every bound faces the filesystem
and none faces the response. All three have cheap, concrete fixes that belong in
the spec before decomposition. The MAJOR set is dominated by under-specification
that Constraint #8 and ADR-077 make dangerous rather than merely untidy: unruled
seed postures inherit silently, and unenumerated wire fields get designed ad hoc.

### Recommended Next Actions

- [ ] CRIT-001: rule attachments (port or explicit founder-ratified drop); fix US-1 AS-4's "notes-only"; port the attachment honesty test
- [ ] CRIT-002: add limiter (reuse the `allowKnowledgeRetrieval` pattern), ctx-cancellation FR + test, concurrency semaphore
- [ ] CRIT-003: add max-output + line-window bounds to MV-3, DS-1/DS-2 rows, and a lower agent-tool match default
- [ ] MAJ-001: rewrite MV-9 as a six-signal port table (statement, clamp pair, excerpt_unavailable included)
- [ ] MAJ-002: complete A4 for Worker/specialists/System Agents + add upgrade-path MV-8b and test 19b
- [ ] MAJ-003/MAJ-004: define per-surface pattern semantics; enumerate the three schemas' fields and the full truncated_reason enum
- [ ] MAJ-005..MAJ-009, MIN-001..MIN-010 per finding recommendations
- [ ] OBS-005: obtain the actual founder ruling on A4 before round 2
