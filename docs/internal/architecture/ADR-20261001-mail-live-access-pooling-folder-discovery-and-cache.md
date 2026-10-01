# ADR-20261001 — Mail live access: pooled connections, folder discovery and a bounded cache

- **Status:** Proposed; founder's core decisions settled in the 2026-10-02 brief. Draft checkpoint — incomplete, not implementation approval.
- **Date:** 2026-10-02 (founder decision date). The filename uses the naming helper's UTC date, 2026-10-01.
- **Deciders:** Daniel Piatkowski (founder, core decisions); architect (design record and recommendations).
- **Evidence baseline:** `docs/adr-mail-live-access` at `ee936a3858c172c19623af6c18bd2f286bcf648f`, before this document.
- **Scope:** Design only. No production code, contract changes, tests or push.

## Bottom line

Keep Mail live: first reuse plain IMAP connections — IMAP is the protocol used to read mail on the server — discover the server's real folders, and keep only bounded, short-lived message headers in memory. The founder has settled the limits: two connections per mailbox, eight overall, no panel-owned connections while the panel is closed, encrypted folder metadata on disk, and no stored bodies or attachments. Measure this first, without JMAP — a newer mail protocol that batches operations — then add JMAP where supported and an explicitly scoped amendment permitting an encrypted header cache. Source: founder-approved brief, 2026-10-02.

## Context — problem and evidence

Pending source verification.

## Decision — Phase 1: plain IMAP

Pending detailed decision tables.

## Decision — Phase 2: JMAP and encrypted headers

Pending detailed decision tables and D6 amendment text.

## Alternatives considered

Pending comparison of A–E.

## Failure behaviour

Pending visible failure, Retry and budget rules.

## Security and privacy

Pending encryption-helper verification, storage boundaries and JMAP URL protection.

## Wire-format impact

Pending proposed contract shapes; no contract files are changed by this ADR.

## Affected components and parallel work packages

Pending file ownership and dependencies.

## Test strategy

Pending future proof obligations; no tests are run or written for this design task.

## Measurement plan

Pending cold/warm comparisons and recommended pass bars.

## Consequences and risks

Pending positive, negative and neutral consequences, each risk with a failure scenario.

## Non-goals

Pending explicit scope boundaries.

## Features pending founder decisions

Issue placeholders only: #1170, #1171, #1172, #1173, #1174, #1175. A separate later pass owns these features; this ADR makes no decision about them.

## Open questions for the founder

Pending evidence review; settled decisions will not be reopened.

## Evidence table

| Claim | File or command | Certainty |
|---|---|---|
| The assigned branch starts at the Mail hotfix baseline | `git branch --show-current` exit 0: `docs/adr-mail-live-access`; `git rev-parse HEAD` exit 0: `ee936a3858c172c19623af6c18bd2f286bcf648f` | Verified (high confidence) |
| The helper supplied this date-based ID | `scripts/new-adr-id.sh "Mail live access: pooling, folder discovery and cache"` exit 0: `ADR-20261001-mail-live-access-pooling-folder-discovery-and-cache`; `date -u` exit 0: `UTC 2026-10-01 18:44:40` | Verified (high confidence) |
| Core design direction is settled; code evidence remains to be checked | Founder-approved task brief, 2026-10-02; headings above explicitly mark incomplete sections | Verified direction; Unknown implementation evidence |
| **Self-check** | Initial artifact checked against the robustness requirement: correct helper ID, founder's bottom line and all requested major section headings; still an incomplete draft checkpoint | Verified (high confidence); not a completed ADR |
