# Legacy-oracle reconciliation — #1266

## Scope and baseline

QA owns only tests and this test plan. Approved normal merges produced e73337d35387639cae91d92c63f874f4c1eecb31, containing GREEN-BE 1238a9e5343421f5cd2a53fcb2add460dd07f24a and the feature branch. Official broad RED evidence is bound to 38bdd39dcc8385c3d8542a4409875cc2dc0aa83f, not the final GREEN-BE tip. Exact inventory and classification receipts live under /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/legacy-qa-20261022/.

## Observable outcome and independent oracle

Every official failing test receives an evidence-based disposition. Tests with obsolete transport or fixture assumptions migrate without changing their underlying content, identity, ordering, side-effect, privacy, or error assertions. Production defects remain loud and are routed to GREEN-BE-A. A release-baseline reproduction is an open failure, never an exemption.

Expectations come from /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/docs/internal/specs/chat-history-paging-spec.md::D7a, D10, D15, Content-free observability and /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/seams-decision.md::R. No expected value is copied from observed implementation output.

| Case | Expected result | Source / retained meaning |
|---|---|---|
| Shared room history consumers | Decode generated SessionHistoryResponse; enumerate earlier pages with cursor progress and terminal checks when the assertion means all history; retain normalized internal Message arrays and all room assertions | D10/D15, FR-025/SC-013 |
| Classified persisted notices and frame matrix | Real canonical/index-publishing append; exact original diagnostic payload/identity/turn remains observable | D1/D10/D15; ADR-066 existing oracle |
| Attach logging | Exactly one content-free history_page summary for an authorized attempt, including preparation-only duration and outcome; no obsolete lifetime replay identity/count assertions | Content-free observability |
| Failed history attach | Explicit history error, no snapshot/wipe, independent session state and current controls, catch_up_complete.mode=live_only; canceled attach still has no complete | D7a/D10 |
| Integration reconnect | Genuine canonical/indexed fixtures, exact original source/tool ordering and identity, catch_up_complete owns attach completion rather than done-last | D1/D4/D10/D15 |
| Visible clear divider plus question | Exactly two selected groups and two drawn rows; original clear boundary, continuation and old-content exclusion retained; both actual marker and question positively present | Section R1, D3/D4 |
| C-85 legitimate schema pair | Resolve each reference in its document context and recursively compare the full wire shape; only verified HistoryEntryOrigin generator annotation at its exact origin reference is metadata | Section R2 |
| C-85 negative instruments | Detect origin constraint/member/requiredness/type/pattern/bound and closed-object drift, frame text-bound drift, unresolved references and unrelated generator annotation; cycles fail closed | Section R2; in-memory schema inputs only |
| Root search stops first at max_matches | Old oracle accepted only max_files/max_bytes/deadline. Team-lead Q1=A corrects it to also accept the normative max_matches bound, requiring exactly1000 reported total hits, at most50 visible hits per file and a separate real21×51-hit total/per-file cap probe; admission/privacy/timeout/all other bounds remain unchanged | Read-boundary spec D7/FR-011, DS-5 row5; explicit team-lead disposition2026-10-22 |
| Native kernel and candidate release failures | Only three final dispositions: deliberately changed legacy oracle, real defect for its GREEN owner, or also-failing pinned release with an actual receipt and named fixing owner. No environment label without reproduction; race failures stay real until proven otherwise | Root Hard Constraint7; current user hard rule |

## Boundary and checks

Real HTTP/WS handlers, store writers, index publication, projections, schema documents and generated carriers remain real. Only existing paid-model/network process edges are scripted. Canonical fixtures must never append raw JSONL behind a published history index. Comparator controls mutate independent in-memory YAML documents, never production or contract files.

GitNexus MCP is unavailable in this session. Direct known-control caller sweeps and diff scope inspection are the fallback; impact is **Inferred**, not graph-certified. Shared room/helper changes may affect multiple room tests; only test callers are in scope. C-85 gets a narrow resolver helper; other recursive copy/enum/delta comparisons remain unchanged.

Before editing a proven legacy case, preserve its official runtime RED excerpt or run the exact test against the unchanged merged baseline. After editing, run one exact named test at a time through /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/omnipus-l1/slotrun.sh, with CGO_ENABLED=0, goolm,stdjson, -count=1 -v -p 1. Current user override: ALL squad Fly work uses ONLY the dedicated spare d8d3977c946558, one pinned CI_CLUSTER_TIERS entry, the existing flock, no runner installs/changes, and collect-before-stop for the machine this run starts. The earlier080d preflight refused the checksum mismatch and stopped without dispatching a gate; no repair/bypass was attempted. The explicit user unblock supersedes the intervening hold and earlier performance placement for this squad. No full local suite.

The C-85 exit proof is the unchanged parent recursive-copy test plus the named in-memory control test actually running with raw exit 0; each deliberate corruption is positively detected. The clear-view exit proof is the named attach test with exact source membership and 2/2 metadata. These prove QA-oracle correction, not independent CHECK.

## Known gaps and handoff

No production edits, contract regeneration, feature-completion claim, native Windows certification, exact-commit UAT, or self-CHECK. RED skill step 4 items 2/3 and the gate's Proof-of-failability checklist are **deferred to independent CHECK**. Local post-migration execution is a source-bound reconciliation receipt, not CHECK or production mutation certification. Every unresolved row remains in the classification ledger with the precise failed assertion and next owner.
