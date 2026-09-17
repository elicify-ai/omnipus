# Claude Code Opus review dispositions — ADR-090

The [original report](ADR-090-built-in-agents-skills-and-visual-reading-opus-review.md) is preserved as a historical snapshot. This register describes the author corrections submitted for independent closure review. It does not claim runtime implementation or executed tests.

| Finding | Correction / disposition |
|---|---|
| C-1 | Bounded self-helpers for seeded Jim/GP require explicit edges and existing mode/depth limits; other cycles remain forbidden. |
| C-2 | Connector assignment is enforced at discovery and execution, intersecting the two existing policy layers; omitted and empty bindings have distinct meanings. |
| M-1 | Fresh roster defines stable IDs, runtime types and Admin; obsolete fresh seeds are excluded without migration. |
| M-2 | Accepted Admin installer Allow is made reachable through shipped global Allow and explicit restrictions for other roles; operator policy is preserved. |
| M-3 | Ava confirms in the owning chat through switch_agent; delegated Ava returns a proposal without mutations. |
| M-4 | Mandatory expected revision, shared locking for settings/instructions, validation before writes and explicit partial I/O outcomes replace an unsupported atomicity claim. |
| M-5 | Sparse authored overrides are distinguished from complete catalog readback and replacement API input; no automatic deny backfill. |
| M-6 | Judge evidence scope is the reviewed workspace; task relevance is an instruction, not a new file allowlist. |
| M-7 | Plan Supervisor retains both plan and define-goal. |
| M-8 | Each new role skill has a content contract, required workflow/tool references and substantive validation. |
| M-9 | Optional document execution prerequisites explicitly except the root dependency wording; shared installation and worker probes are specified. |
| M-10 | Inspection bytes remain transient through live requests/retries; all durable history uses text markers and requires re-read for later vision. |
| M-11 | Added acceptance scenarios, concrete data and regression coverage for the corrected runtime contracts. |
| m-1 | API error contract distinguishes malformed request 400 from existing domain validation 422. |
| m-2 | External send_email/reply gain explicit Ask overrides; internal agent communication stays separate. |
| m-3 | Current editable-field inventory is classified, including hidden-agent memory and previously dropped fields. |
| m-4 | Default deletion/install/removal policies are named so Ava has one proposal confirmation; operator restrictions remain authoritative. |
| m-5 | Remaining preview tier, uncompressed mode and pinned visibility tests are covered. |
| m-6 | Root AGENTS.md and CLAUDE.md annotate Jim’s planned Deny while identifying current implementation accurately. |
| m-7 | Adapter inventory includes Bedrock and CLI limits. Source correction: native ClaudeProvider uses the Anthropic SDK; it is not a Claude CLI adapter. |
| m-8 | Image acquisition accepts regular files only with bounded guarded opening; no new non-seekable streaming protocol. |
| m-9 | Saved-but-inactive REST result is specified separately from activation success. |
| m-10 | Management views name the exact effective permission gates. |
| m-11 | Boundary dataset values are literal and testable. |
| m-12 | ADR supersession names self-delegation, installer defaults, Judge skill assignment and dependency exceptions. |
| o-1 | Catalog-shaped configuration readback is consolidated in get_agent_tools. |
| o-2 | Pixel-boundary test memory cost and sequential execution are documented. |
| o-3 | Corroborating source observation; no change required. |

## Decisions and remaining scope

The review’s installer-policy question is already answered by the approved Admin Allow matrix. Its connector question is resolved by the approved assignment model: unassigned connectors are ineligible. Neither calls for a new user approval. Existing global restrictions still win. The original C-2 statement that every agent can reach every connector is too broad where independent tool policy already denies access; the verified missing binding enforcement is nevertheless corrected.

**L1 remains open:** the verified upstream document-skill license restricts redistribution. No restricted package has been copied. The founder must choose independently authored/permissively licensed equivalents or supply separate permission before direct bundling can proceed. All other corrections retain the agreed agents-and-skills scope; no worktree isolation engine is introduced.

## Independent closure

The [targeted Claude Code Opus closure](ADR-090-built-in-agents-skills-and-visual-reading-opus-closure.md) returned PASS for all three engineering documents, with no critical or major findings remaining. It raised eight minor follow-ups; all are corrected below. A review pass describes specification quality only; L1 remains a document-package implementation dependency.

| Follow-up | Final correction |
|---|---|
| N-1 | Removed stale connector-inheritance wording; absent binding denies access. |
| N-2 | Admin matrix now distinguishes connector management/discovery from Ava's agent assignment. |
| N-3 | FR-005 consistently uses activation_status. |
| N-4 | FR-006 traces BDD-15 to I4 and D32 helper-limit coverage. |
| N-5 | Retained existing configurable depth ceiling; 3 is a fresh edge/default value, not a new immutable hard maximum. D32 covers 2/3/4. |
| N-6 | Removed blocked-source cancellation-worker wording; bounded regular-file read cancellation is consistent. |
| N-7 | Provider request builder owns later-request access checks; denial replaces image with correlated refusal text, verified in captured requests. |
| N-8 | Stale global-ceiling echoes return 409 CONFLICT and trigger fresh readback, with concrete D33 coverage. |
| Notes | Clarified custom-agent default map preservation and added Jim-to-Admin dependency handover to ADR routing. |

Documentation checks verify links, unique global tool names and requirement references. Runtime code correctness and actual user/agent reachability remain unimplemented and untested; these files define their future acceptance gates.

Final independent verification confirmed N-1–N-8 and both notes closed. A final sentence-level ambiguity in FR-003 was fixed with the reviewer’s exact wording: the request carries the complete policy map, revision, and override_names, and override_names identifies stored local overrides. See the appended independent reports in the closure record.
