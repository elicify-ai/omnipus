# Design-system execution contract

Status: Encode contract preparation, 2026-09-17. Implements the definition and migration plan in this directory.

Worktree: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-release-session`.
Starting revision: `92aeb4d5dcb0d545e2370ddd1c57c099a03392ba`.
Concurrency: lead plus three Sol workers through the built-in agent slots. The founder subsequently authorized additional CLI workers using `claudez` (GLM) and `claudeg` (Grok). Six bounded CLI lanes are now added; file ownership and stage barriers apply equally to them. Workers may not spawn further agents without lead assignment.

## Shared token contract

Source files: `design-system/tokens/colors.json` (worker 1) and `design-system/tokens/foundations.json` (worker 2). Each contains `{ "version": 1, "tokens": [...] }`.
Each token has `id` (dot-separated stable key), `css` (custom property including `--`), `layer` (`primitive`, `semantic`, `component`), `kind` (`color`, `dimension`, `number`, `duration`, `fontFamily`, `fontWeight`, `lineHeight`, `shadow`, `easing`, `string`), and exactly one of `value` (CSS string or number) and `ref` (another token id). Optional `description`, `owner`, `purpose`, `states`, `consumers`, and `responsive` metadata. Component tokens require owner, purpose, states and consumers. References point from consumers to lower layers; raw values belong to primitives. Compose complex CSS in primitive values or propose explicit schema extensions through the lead; do not invent a second reference syntax.

Worker 1 owns generator and validation under `scripts/design-system/tokens*`, generated `src/design-system/tokens.ts` and `src/styles/tokens.generated.css`, and colour/status definitions plus tests. Generated TS exports `tokens` (semantic/component ids to CSS var references) and `resolvedTokens` (all ids to resolved values); all export objects are readonly. Generation sorts deterministically, validates before writing, and supports check-only mode. New styles are not imported application-wide until lead integration establishes value preservation. Worker 2 owns foundations JSON, supporting foundation policy document and focused tests; worker 2 never writes generated outputs.

Preserve rendered values outside approved normalizations. Foundation adoption, including root spacing activation, waits for C1. Shared loading timing: delay 400ms, minimum visible 300ms, no-progress escalation 10000ms.

## Public catalog and verification contract

Classify every catalog export as foundation, primitive, composite, domain or application. Only the first three classes may be public. App stores, shells, route hooks and domain models must not enter the reusable library dependency graph. Lead owns `src/index.ts`, `packages/ui/src/index.ts` and public export integration.

One JSON manifest per component under `design-system/manifests/`. Shape: version=1, component (primary public name), exports (all public names covered), category (`primitive` or `composite`), source (repo-relative implementation file), documentation (repo-relative markdown contract documenting anatomy, slots, composition, control model, keyboard/focus and name ownership), owner, themes, variants, sizes, states, stories (array of `{ file, exports }`), checks (array of `{ id, kind, file, test, story, applicable, reason }`). Check kinds: unit, interaction, axe, keyboard, browser, pointer, reduced-motion, forced-colors, root-size, zoom, reflow. An applicable check requires a real file and named test; a non-applicable check requires a concrete reason. Checks map to executed evidence; declarations alone never prove execution. Story references identify exported story names. Foundation exports have their own catalog classification and token verification mapping.

Worker 3 in A1 owns `.storybook/`, `tests/design-system/`, `playwright.design-system.config.ts`, and `scripts/design-system/verification*` / `scripts/design-system/bundle*`. Common harness loads component stories and checks the contract, not screenshot baselines. Story output is `dist/storybook`; production build must exclude story modules and output. Component owners later own their colocated `*.stories.tsx`, tests and manifests. Harness worker requests dependency/script changes from lead, does not edit root package files or component implementations.

## Ownership and integration

All workers read applicable AGENTS instructions and definition/plan before edits. Use GitNexus CLI from this worktree (`node .gitnexus/run.cjs query/context/impact`) when MCP is absent. Impact analysis is mandatory before existing-symbol edits; send blast radius and high-risk warnings to lead before editing. Characterize behavior before replacement. No commits by workers. No reverting others' edits. Report test commands, actual exit codes, red/green and mutation evidence, and remaining gaps honestly.

Lead owns dependency installs, all root build/lint/TypeScript configuration, CI, shared schemas/contracts, inventory integration and acceptance. Workers propose contract changes to lead. Stage checks run only while edits are paused and record the tested file hashes. A1/A2/A3 are development checkpoints; A exit is required before any screen repair.

## Evidence and acceptance

Use specification-derived assertions and mutation-prove them. Baseline production build and compressed initial/total embedded sizes are captured before application implementation. Budget: +25 KiB compressed initial JS/CSS, +250 KiB total embedded assets, zero Storybook payload. Browser matrix is Chromium/Firefox/WebKit. Human screen-reader evidence and founder visual acceptance remain explicit outstanding gates; never fabricate them.

## Founder-authorized A/B overlap

On 2026-09-17 the founder requested Stage B work with GLM/Grok while Stage A verification finishes. B implementation may run only in new isolated enforcement directories, with shared contract `design-system/enforcement/contract.json`. Existing A source, manifests, harness, dependencies and CI remain frozen during A checks. Locks stay audit-mode until integration; A acceptance is not waived. No C1 application migration starts until both A and B exit gates pass. Lead owns shared configuration, registry policy, baseline approval and CI activation.

## Founder pause after Stage B — 2026-09-18

The founder instructed: “please pause after stage b and realign with me”. Finish outstanding Stage A verification and Stage B implementation, verification and audit-mode integration, then stop before C1. Report the accepted evidence, unresolved risks and proposed application migration sequence for founder realignment. Existing authorization does not permit starting C1 or later batches before that discussion. This changes the execution stopping point, not the full program scope or stage acceptance criteria.
