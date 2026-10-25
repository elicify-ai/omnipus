# Issue #1435 — Round 2 RED plan

Base: `058f1181188510c417fb382891b8e5e24f438fcb`, clean and fetched on `fix/1435-attachments`. Same test author as the original pack. Add tests only; do not change existing tests or production code.

## Independent oracles, fixed before assertions

The chief's Round 2 R1–R5 brief is the acceptance source. Read the four gate reports at /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/1435-gate-cr.md, /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/1435-gate-sfh.md, /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/1435-gate-arch.md, /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/1435-gate-sec.md and the result record in /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/omnipus-l1/logs/1435-gate-prt.stream.jsonl. Reports identify paths, not expected values copied from runtime output. Numeric patch arithmetic and optional-field validity come from the contract /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/t4-1435/contracts/components/schemas/CatalogResizeLimits.yaml.

| Requirement | Input / sequence | Required outcome / independent oracle |
|---|---|---|
| R1 selected budget at admission and every send | Fresh real browser_screenshot tool result. Edge-only fixture 6144x512 with selected edge 6000; patch-only fixture 512x512 with selected cap 128 (256 input patches), both below package defaults. Real tool admission followed by plain Chat, streaming, single-candidate chain and multi-candidate/fallback send paths | Every captured selected-provider request obeys its configured edge, bytes and patch limits. An already-safe loose candidate does not authorize an unsafe request for a tighter fallback. The original durable image must not be deleted to pass. |
| R2 never-discarded failure notice | Valid grayscale PNG above the budgeted decoder's permitted pixel band; real recordToolResult and checkpoint/finish path, then provider send, outgoing events and persisted transcript | Original tool text survives; unavailable/too-large explanation survives in model input and user-facing live/replay evidence. A scratch helper marker or server log alone is insufficient. |
| R3 ordinary missing ref | Accept a real file/ref, remove its mapping, then execute the real turn conductor (no manual translator call) with that ref | Outgoing error notice and persisted replay carry exactly `media_unavailable` and re-attach guidance, not generic/unsupported. A deterministic provider does not invent the missing-attachment explanation. |
| R4 optional patch limit round trip | Real catalog parser -> actual Served().Body -> actual generated TypeScript ProvidersCatalog validator. Missing field, positive minimum 1 and positive 30000; explicit zero mutation as a validator negative control | An omitted field stays absent or is a valid positive value; the real served envelope passes the generated validator. The instrument rejects a present zero. No copied wire interfaces or hand-written validator. |
| R5 upload ownership/lifecycle | Real upload endpoint plus real chat deletion (control), retention removal and syntactically valid nonexistent chat. Per-test home/store only | Accepted owned ref resolves before removal; deleting/retaining away its owner releases its mapping. A nonexistent owner must be rejected or its temporary upload must expire, never stay pinned. |

## Stack, isolation and instruments

Go `testing` + `testify`, matching neighbouring tests. Only external provider/browser output and stream transport are substitutes. Store, actual tool admission, checkpoints, candidate selection/send, event bus and session archive stay real. R4 runs Node 24's built-in TypeScript loader to execute the checked-in generated Zod validator against actual Go-produced bytes; Node 24 and node_modules are already provided by the Go CI jobs. No Go/vitest process is nested; the Node child is the lightweight validator, not another test runner. Instrument preflight imported the actual generated schema successfully.

R1 covers two independent numeric constraints rather than assuming the default 30,000-patch cap is the strictest. Small numeric fixtures and positive path checks establish that capture can see a real send. R2/R3/R4 missing-field/R5 orphan/retention cases exceed 30% negative coverage. Existing size-boundary cases are retained unchanged; Round 2 does not re-author them.

GitNexus CLI query and impact were attempted; it warns that the index belongs to a stale sibling clone. Direct caller/source sweeps are the fallback. Add new test symbols/files only; no existing symbol edits are planned. Impact is Inferred: agent, catalog and gateway test binaries, no production change. Pre-commit detect-changes will be attempted and checked against the actual git diff, never trusted when falsely empty.

## RED execution and handoff

Every regression group runs in the foreground through /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/omnipus-l1/slotrun.sh, one anchored `-run`, one package, serially, `CGO_ENABLED=0`, tags `goolm,stdjson`, pinned Go 1.27.3, `-p 1 -count=1 -v`. Hold before starting while load >20. Save raw exit and named assertion failures. A compile/setup failure is not accepted RED. The cleared shared build cache is expected to make the first package build slow; do not start another process alongside it.

Evidence directory: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/omnipus-l1/logs/t4-1435/r2

GREEN, implementation mutations and the Proof-of-failability checklist are **deferred to independent CHECK**. No full local suite or unqualified product PASS.

## Deliberate limits

No live browser, paid model or real user session is used. Actual screenshot bytes are generated locally. Exact-candidate UAT remains separate. Reviewer suggestions on cache-key-only mutation and broader retry-reset coverage are not silently treated as new implementation scope beyond the chief's five requirements. Missing production seams, if encountered, get explicit failing BLOCKED tests, not skips or invented APIs. User-facing documentation remains the implementer's responsibility.

CHECK mutants to kill later: remove ordinary-image candidate fitting from any of the four send paths; discard scratch Content at admission; consume missing-ref errors without publishing/persisting; serialize unknown max_patches as zero; leave upload scopes pinned after owner removal or for a nonexistent owner.

## Chief addition R6 / independent CHECK

Read the final result record in /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/omnipus-l1/logs/1435-check.stream.jsonl. M3 removed the image guard reset from `callLLMWithRetries` and survived the old suite. Add a self-contained regression driving TWO actual callLLMWithRetries sequences on ONE turn state. An external provider rejects each image-bearing request and accepts the stripped retry. Expect exactly four requests with media counts 1,0,1,0, both sequences recovered, and the PDF guard left unchanged. No fabricated calls to the downgrade helper or test-side guard reset. The reset already exists at 058f11811; an ordinary baseline pass is not RED evidence. Historical pre-reset execution, if used, is bound explicitly to its genuine pre-change commit, never disguised as a failure on 058f11811; implementation mutation remains independent CHECK's job.

R2's commissioned exact visible marker is `[attachment unavailable: browser-screenshot.png (image too large to fit the model's size limits, even after resizing)]`. Assert this entire literal in actual model input, live user output and reopened public transcript output while retaining the original tool text. This adds the missing oracle without touching any existing assertion.
