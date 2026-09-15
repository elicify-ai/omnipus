# Single combined design grill — 2026-09-10

Reviewer: independent review_manager agent. Scope: ADR-081, compact spec and implementation plan. One round, as requested.

Original verdict: **REVISE** — three major findings, no critical findings. Author addressed the findings below once; this is not a second independent PASS claim.

| Finding | Required correction | Author disposition |
| --- | --- | --- |
| G1 — Cross-connection control/input ordering undefined | Define linearization, retirement and readiness, including both arrival orders. | Added control epoch and authoritative acknowledgment. Server control acceptance retires old input work before applying new control; frontend pauses until acknowledgment and matching frame. Completed actions are not undone; pending actions cancel. Contract freeze includes these fields. |
| G2 — Channel cardinality and payload rules missing | One peer/two channels; reject duplicate channels and non-hover lossy messages. | Added explicit server-side cardinality, delivery-option and message-kind restrictions and tests. |
| G3 — Reliable queue overflow unspecified | Visible failure, cancellation and held release without replay. | Added fail-peer policy, independent cleanup path and saturated-queue held-button regression. |

Operational clarification: early-access smoke must run from Mac against the verified deployed Amsterdam image, not only locally before build. Implementation plan corrected. No additional grill round; implementers must prove the corrected contracts with focused tests before early handoff.
