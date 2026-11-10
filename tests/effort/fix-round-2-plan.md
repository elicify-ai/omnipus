# Effort RED repair — round 2

Date: 2026-11-01. Author owns all test-side changes for this feature. Base: `1d53b345c9775ec27ad6f1b3b85a542240d2d2fd`, branch `landing/effort`. No production changes, no push, serial foreground checks only.

## Independent expectations (recorded before assertion edits)

Source: `/workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md`.

| Finding / behavior | Real boundary and Given | Expected outcome and oracle | Fault the test must catch |
|---|---|---|---|
| C2-01 / S-58, SC-007 | Production tool registry → `update_agent` → durable agent/config store. Establish an existing entity's Off directly in its canonical JSON fixture, not through the still-missing effort writer. Change model without an effort key. | Target with `none`: save model with stored `off`. Target with low/medium/high and no switch: refuse with exactly `{model} can't turn reasoning off. Send reasoning_effort with one of: provider_default, low, medium, high.` Entity bytes, revision, config bytes and valid sibling stay unchanged on refusal. | REST-only implementation; silently retaining/dropping Off; partial sibling save. |
| C2-02 / S-60 | Real `MemorySection`, query client and picker; mock API/process edges only. A recap model and fallback with effort absent (then explicit Off). | Each chip starts Off; exact ASCII-apostrophe sentence `We recommend Off for the recap: it keeps recaps cheap, and a recap doesn't need reasoning.` occurs once. DOM order: model and chip, fallbacks and their chips, sentence, collapsed Recap limits. No save on hydration. | Missing/duplicate recommendation; wrong order; missing fallback chip; treating absence as Provider default. |
| C2-03 / CHECK-03 residual / S-17, S-48/S-49/S-51/S-53 | Actual captured OpenRouter HTTP request bodies from loop tests. Keep a broad collector only for the negative primary-value-leak check. | Exact `reasoning: {effort: value}`; forbid flat `reasoning_effort`, `thinking`, `output_config`, Bedrock fields and other effort carriers. Test the helper itself against flat, contradictory and foreign encodings. | Flat ignored OpenRouter key or contradictory dialect passes loop assertions. |
| C2-04 / S-02 | Own saved versioned config with old channel key plus old environment binding; real session-worker turn, loop/message bus and registered messenger, provider returns distinct visible answer plus reasoning marker in both carriers. Connector intake/admission selection is outside this outbound boundary. | Exactly ordinary visible answer delivered; collect all outbound destinations after the completed turn for the package's existing 10-second observation budget. No marker or separate reasoning message; provider-call and normal-answer positive controls. Work detached beyond that window is an explicit limitation, not proven absent. | Differently named reasoning publication bypasses static name sweep. |
| INFO-1 | Existing timing workflow RED tests and static type/design gates; do not edit scaffolds. | Preserve loud missing-wiring evidence and explicitly retain production ownership. | Calling scaffolding GREEN or suppressing compiler/scanner failures. |
| INFO-2 / CHECK-08 residual / S-88/S-95 | Existing actual HTTP fallback workflows; inspect production dispatcher and re-drive the two narrow workflows. Strengthen the accounting-boundary test by forcing the real adapter's `ChatStream` against an SSE error with 300 output / 120 reasoning tokens, then the real adapter's successful model-B response with 80 output and no reasoning. | Adapter-produced `StreamProviderError` retains the actual billed usage; production attempt booking and stats keep A=120, B absent, sum=120. This test drives the two attempts at the adapter/accounting boundary, NOT automatic fallback. Preserve loud `BLOCKED` in both full workflows if the loop uses plain Chat. Explain the exact dispatch branch. Downstream live/history assertions remain unreached. | Hand-built error incorrectly passes while real parser drops failed-stream usage; false claim of automatic streamed fallback or completed history workflow. |

The new cases include refusal/atomicity and invalid-wire negative controls (at least 30% of the new case table). No new bounded numeric input is introduced. Existing range/boundary suites remain unchanged.

## Execution and evidence

- GitNexus tools unavailable at discovery; run saved caller/reference sweeps before existing-symbol edits. Report impact as Inferred, not a graph result.
- Run only affected named Go tests, one process at a time: `/usr/local/bin/heavy env CGO_ENABLED=0 go test -tags goolm,stdjson -p 1 -count=1 -v -run '^TestName$' ./pkg/<touched>/` (or explicit named alternatives covering the helper's actual callers). Never an unfiltered agent/gateway suite.
- Run `/usr/local/bin/heavy npx vitest run src/components/settings/effort-red-recap-limits.test.tsx` only for the touched frontend file.
- Save commands, raw exits and first failing assertions under `/workspace/logs/effort-red/fix2-*`; bind a separate round-2 receipt index to final source fingerprints. Preserve round-1 receipts as historical evidence.
- Run relevant touched-package vet, frontend eslint/typecheck, size checks and required design scanner through plain heavy. Production-owned red gates are reported, not repaired or suppressed.
- Update `/workspace/wt-effort/docs/internal/specs/effort-levels-red-traceability.md` with direct test symbols, resolution rows and precise limitations.
- Final source/diff/receipt/no-skip/no-deletion audit; stage intended tests and commissioned trace artifacts only; commit with requested human author and committer, no AI trailer. Leave `/workspace/wt-effort/omnipus-effort-red` untouched (this author's self-hosted browser-test gateway executable).

## Known gaps and deferred checks

This is RED repair, not GREEN or independent CHECK. Step 4.1 only: observe right-reason failures on unchanged production. Step 4.2 (GREEN), step 4.3 (scratch-clone implementation mutations) and the skill's proof-of-failability completion are deferred to a different CHECK instance. Helper negative controls test the instrument; they are not production mutation receipts. Full suites belong in cut CI. A written downstream assertion is not executed coverage. User/agent reachability is not established by this pack.
