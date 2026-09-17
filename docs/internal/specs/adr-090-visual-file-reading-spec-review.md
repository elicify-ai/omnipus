# Visual file reading — initial grill-spec review

Date: 2026-09-17. Independent read-only review against ADR-090 and existing media source. Initial verdict REVISE (0 critical, 1 major, 1 minor). Both findings corrected and reread; **final verdict PASS**. No implementation or runtime tests executed.

| ID | Severity | Finding | Correction |
|---|---|---|---|
| VIS-01 | Major | Raster source-dimension rules were ambiguous for existing SVG media; its early normalization branch could bypass actual candidate budgets, and partial rendering needed disclosure. | Direct SVG reader input retains text behavior. Existing SVG tool media uses the bounded raster canvas, finite-value checks, existing default/edge/pixel caps, explicit limited-render notice and post-raster candidate resizing. Dataset A13–A15 covers SVG text, canvas boundaries and tiny budgets. No new renderer is required. |
| VIS-02 | Minor | A byte bound alone does not stop a non-seekable source waiting forever. | Snapshot acquisition honors turn cancellation/deadline, unblocks/closes its owned source, leaves no abandoned read worker, or refuses a non-cancellable source before acquisition. B15–B16 and BDD-09 cover cancellation/refusal. |

## Structural, test and eight-lens assessment

All 15 acceptance scenarios have categorized BDD groups, planned tests and requirement traceability. All 13 functional requirements occur in the matrix; regression and external evaluation protocols are present. Ambiguity/consistency of image dimensions and completeness/operability of cancellation are resolved above. Security retains bounded authorized snapshots, correlated audit and no raw-path reopen. Correctness retains actual candidate capabilities, image correlation, replay purpose, expiry and current-scope checks. Feasibility is verified by source seams, not a claim of running functionality. No additional complexity was introduced: existing media infrastructure remains the foundation. Provider request capture and live defect/correction evidence are required beyond helper tests.

The reviewer found no remaining major/minor issue after closure reread and no question requiring the founder. Independent Claude Code Opus review is recorded separately.
