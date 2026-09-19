# Adversarial review — environment setup using existing Ask

Date: 2026-09-18. Independent read-only review of the revised environment setup specification, ADR-090 §6.5, configuration FR-011 and visual-reading dependency amendment. No implementation or runtime tests executed.

## Executive summary

**Verdict: PASS.** No critical, major or minor findings in the current revision. This review supersedes the earlier review of the rejected exact-plan approval architecture: plan tokens, expiry, separate lifecycle operations and special God-mode handling are no longer requirements or review recommendations. The founder expressly selected the existing ordinary tool-policy semantics.

## Findings

None. This is specification acceptance only, not evidence that setup is implemented, secure in operation or platform-certified.

## Structural integrity

Structured-spec review applies. The document has five requirements, eight linked acceptance scenarios and four measurable success criteria.

| Check | Result |
|---|---|
| Goals have measurable acceptance criteria | Pass: ES-SC-01–04 |
| Companion requirements and approval semantics agree | Pass: ordinary Ask, existing overrides and God mode preserved |
| Scope and actors are explicit | Pass: custom native agents included; external CLI integration, library previews and Admin handoff excluded |
| Error outcomes are defined | Pass: unsupported dependencies, denial, interruption and partial workspace effects distinguished |
| Requirements have scenario coverage | Pass: ES-FR-01–05 mapped in scenario table |
| Implementation and completion claims are separated | Pass: no runtime validation claimed |

## Eight-lens assessment

| Lens | Assessment |
|---|---|
| Ambiguity | No finding. Ask is the shipped setting, not an immutable installation-specific approval policy. Ordinary Allow/God-mode behavior is expressly retained. |
| Incompleteness | No finding. Custom-agent reachability, shared versus workspace scope, installation failures, actual sandbox checks and incomplete visual validation are covered. |
| Inconsistency | No finding. Revised ADR and companion requirements agree; former Admin wiring is explicitly a replacement target, not a claim of current functionality. |
| Infeasibility | No finding. Supported recipes are bounded; unsupported platforms, licences, native libraries or privilege requirements fail explicitly. Universal installation is not promised. |
| Insecurity | No finding. Existing authorization/approval is reused; requested shared scope is visible, host paths are not caller-controlled, hooks remain sandboxed and cross-workspace writes are forbidden. |
| Inoperability | No finding. Cancellation, interrupted setup, partial workspace changes, target locking and retained working shared runtimes are addressed using existing machinery. |
| Incorrectness | No finding. Installer success, actual agent sandbox readiness and model visual inspection remain separate claims. |
| Overcomplexity | No finding. One normal tool call replaces the earlier bespoke approval lifecycle; standard package managers and existing execution/job handling are required. |

## Test coverage assessment

ES-BDD-01 verifies one existing Ask approval with no second question. ES-BDD-08 verifies normal Deny/Ask/Allow/God-mode semantics rather than introducing special rules. Other scenarios cover custom agents without delegation, workspace and shared scope, malicious hooks, failure/partial effects, concurrent installation/reuse and actual Office visual inspection. Existing shared approval regression coverage is explicitly reused for authentication, replay, restart and cancellation. These are planned tests, not executed results.

## Threat summary

| Component | Main threats | Required controls in current specification |
|---|---|---|
| Setup entrypoint | Forged approval, unauthorized workspace or path | Existing authenticated tool approval/workspace authority; structured identifiers; no arbitrary host destination |
| Package installation | Untrusted hooks, host escape, secret access | Existing sandbox/network/secret boundaries; no unrestricted shell or sudo fallback |
| Download/extraction | Tampering and traversal | Integrity checks and archive traversal protection |
| Shared runtime publication | Cross-workspace mutation, disrupted active tasks | Application-owned versioned installation, read/execute-only access, target locking and preservation of working versions |
| Office result | False claim of visual inspection | Actual generation/conversion/rasterization in caller sandbox plus real image inspection and correction |

## Open questions and next action

No additional founder decision is required by this review. Implement against the revised five-requirement specification using the existing Ask mechanism. Installer recipe details, package provenance and supported-platform evidence remain implementation deliverables; do not revive the rejected separate approval architecture.

## Subsequent permission-matrix amendment

After the review above, the founder confirmed the narrower ES-FR-01 role matrix: Ask for Mia/General Purpose/Admin, Deny for Jim/Ava/Planner/Researcher, locked hidden-role Deny, and execution-dependent native custom creation defaults. ADR, FR-011, ES-BDD-08 and implementation status were synchronized. This note records the later amendment; the prior independent verdict is not represented as a fresh review of it.
