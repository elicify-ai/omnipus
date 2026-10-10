# Wave-1 identity bundle — duplicate removal, 2026-10-09

## Decision

Item 4 uses measured duplicate removal, not SVG minification. The earlier path-compaction proposal is **BLOCKED / rejected**: its exact canvas gate found 68 differing comparisons out of 620, so it was never applied. No tolerance, expected value or pixel case was changed to accept it.

The replacement preserves all original SVG path strings, including command spelling and whitespace. Fifteen badge paths already existed byte-for-byte in the Phosphor icon chunk (**6,030 duplicated bytes**). The existing catalogued `AgentIcon` reuses those public components; the other badges share one wrapper, and repeated figure masks/body fragments share exact original literals. No figure, role, palette, motion, control, dependency, asynchronous loading step or wire format is removed or added.

## Implementation

| Source | Responsibility |
|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/nav-wave1-check/src/components/ui/agent-icon.tsx::AgentIcon` | Render the shared badge while retaining public props, size, naming and motion behavior; isolate it from unrelated Phosphor context. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/nav-wave1-check/src/lib/agentBadgeIcons.ts::REUSED_AGENT_BADGES` | Reuse 15 already-shipped, exact badge shapes. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/nav-wave1-check/src/lib/agentIconArt.ts::agentFigureInner`, `agentBadgePath`, `_BadgeCoverage` | Preserve original artwork literals and complete compile-time coverage of all 31 roles. |

The component remains reachable through its existing public export and chat/identity-editor consumers. No user-facing documentation update is needed for this byte-only change: original path encodings and measured pixels are identical; names, figures, roles, controls, motion and synchronous loading remain unchanged. User documentation for the separate F4 and FR-098 behavior fixes was committed with those fixes.

## Measured production budget

| Quantity | Raw bytes |
|---|---:|
| Frozen baseline | 26,131,305 |
| Before duplicate removal | 26,632,392 |
| After duplicate removal | **26,623,138** |
| Actual net saving | **9,254** |
| Original overrun | 7,519 |
| Final growth over baseline | **491,833** |
| Unchanged growth allowance | **493,568** |
| Remaining margin | **1,735** |

The margin is **0.3515% of the allowance**, still small. It is a measured result, not the rejected minification proposal's 7,687-byte prediction.

The identity chunk shrinks **34,403 → 25,152 bytes** (9,251 saved). The existing icon chunk grows by only 9 bytes; other JavaScript references/chunks account for the net **9,254-byte** saving. CSS, static assets and HTML contribute no reduction or exclusion. Initial gzip is **348,498 bytes**, below the frozen baseline's 411,908 and the unchanged 25,600-byte growth allowance.

The real bundle audit exits **0**, `pass:true`, with all five checks true: Storybook output clean, Storybook module provenance clean, JavaScript hashes bound to fresh generated provenance, initial gzip within budget and total raw within budget. The audit script/cap, frozen baseline and enforcement ledger are unchanged.

## Verification

| Check | Result |
|---|---|
| Same exact canvas pixel gate, final production code versus pre-change commit `183a61754416995feda0acdcfb288af93fffa139` | Exit **0**; **620/620 exact matches**, zero differences, zero browser errors; 4 figures × 31 roles × 18/26/40/48/256px. |
| Original path encoding invariant | **496 path strings** across 124 real-component pairs are byte-for-byte unchanged. |
| Unrelated icon context | All 31 roles render identically under foreign colour, mirror, opacity and class context. |
| Pixel sensitivity controls | Shift figure, shift badge and remove ink: each exits **1** with the original exact-pixel assertion; in-memory inputs only. |
| Complete-role compiler control | Complete sources exit **0**; in-memory removal of the developer badge exits **1**, with `TS2344` rejecting missing coverage. |
| Project TypeScript gate | `npm run typecheck` exits **0**, `tsc -b --noEmit`. |
| ESLint and design-system locks | Exit **0**; locks PASS with zero errors. |
| Fresh SPA build, measure and real audit | Each exits **0**; the Mail editor bundle check also passes. |
| Existing AgentIcon and F4 tests, run serially without edits | Exit **0**, **12 passed** and **3 passed** respectively. |

The overview screenshot is supporting illustration only; isolated per-image canvas bytes decide pixel acceptance. No tests, manifests or budget expectations were weakened. GitNexus impact/change checks were attempted, but its available graph is stale for this checkout; direct caller sweeps and the scoped diff are the fallback, with impact labelled **Inferred**.

## Receipts and hand-back

Commands, direct exit files, before/after measurements, complete asset deltas, generated provenance, exact-pixel reports, screenshots, sensitivity controls and immutable-policy hashes are retained under:

`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/nav-wave1-check/test-results/nav-wave1-apibundle-fix-20261009/`

No lazy-load or dead-code change was needed after duplication removal cleared the required 7,519 bytes. This lane commits/pushes only `work/nav-wave1-apibundle-fix-20261009`. Independent CHECK/review and merging into `work/nav-wave1-20261008` remain the squad lead's responsibility; no integration, release or main merge is claimed.
