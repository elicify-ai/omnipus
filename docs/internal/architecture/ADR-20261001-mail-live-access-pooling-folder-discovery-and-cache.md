# ADR-20261001 — Mail live access: pooled connections, folder discovery and a bounded cache

- **Status:** Proposed; founder's core decisions settled in the 2026-10-02 brief. Draft checkpoint — incomplete, not implementation approval.
- **Date:** 2026-10-02 (founder decision date). The filename uses the naming helper's UTC date, 2026-10-01.
- **Deciders:** Daniel Piatkowski (founder, core decisions); architect (design record and recommendations).
- **Evidence baseline:** `docs/adr-mail-live-access` at `ee936a3858c172c19623af6c18bd2f286bcf648f`, before this document.
- **Scope:** Design only. No production code, contract changes, tests or push.

## Bottom line

Keep Mail live: first reuse plain IMAP connections — IMAP is the protocol used to read mail on the server — discover the server's real folders, and keep only bounded, short-lived message headers in memory. The founder has settled the limits: two connections per mailbox, eight overall, no panel-owned connections while the panel is closed, encrypted folder metadata on disk, and no stored bodies or attachments. Measure this first, without JMAP — a newer mail protocol that batches operations — then add JMAP where supported and an explicitly scoped amendment permitting an encrypted header cache. Source: founder-approved brief, 2026-10-02.

## Context — problem and evidence

### What is wrong today

Mail makes the user wait for repeated connection setup, and assumes folder names that some servers do not have. Reusing a connection removes repeated setup, not the first connection's server delay. Discovering folders fixes the assumption; it must not turn a network failure into a false empty folder. These are design conclusions from the transport and folder code below, not measured improvements.

| Evidence | Verified current behaviour | Meaning for this decision |
|---|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/email/transport.go::Client`, `dialIMAP`, `AccountKey` | Clients are connectionless between calls. Calls dial, authenticate and select INBOX; the account budget key is **host:port\|username**, not host\|username. | Pool below the client facade, preserving account isolation and TLS hostname checks. Do not duplicate pools in each REST-created client. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/email/view.go::folderNameFor`, `FolderCounts`, `ReadFolderPage` | Sent/Drafts default to literal names. The hotfix returns zero counts for structurally absent Sent/Drafts, but the page path still fails when SELECT cannot find the folder. | Preserve the hotfix; add discovery and consistent confirmed-absence handling to reads. Missing INBOX is not a healthy empty account. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/email/mail_budget.go::SharedMailBudget`, `call`, `TryCall`, `flightContext` | Shared two-slot account budget, duplicate-call coalescing and watcher backoff already exist. Shared work can outlive its first caller. | Add the eight-socket ceiling beneath this budget; cancellation must retire unsafe sockets and respect other observers. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/email/watcher.go::Cycle`, `WatcherBackoff`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/email/watcher_set.go::CycleAll` | Watcher updates counts/state without starting turns or creating tasks. Its nominal one-minute cadence and exponential backoff are separate from the panel. | Keep closed-panel new-mail notice; do not restore the retired drainer. Pooling must not bypass its budget/backoff. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/results/lane-M.md::The 13 mailboxes (M2/M3)` | Supplied post-hotfix UAT: 13/13 folder calls succeed; folders take 2.5–8.1 s, Inbox lists 4.7–22.8 s, summary 17–25 s; all 13 Sent lists fail with `folder_missing`. | This is recorded evidence, not a new run. Folder/list timings share the mailbox-click clock origin. The Inbox samples contain only 0–8 messages. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/email-loading-rca.md::Evidence` | Supplied pre-hotfix analysis records fresh TLS/login work, slow server setup, and query retries prolonging failures. Its fail-all folder-count observation predates this checkout's hotfix. | Do not repeat the superseded count defect or assert a proven cause of slow server handshakes. Separate cached display time from live refresh time. |

The supplied reports do not contain a comparable message-open latency series, a large-folder benchmark, or a completed real connection-failure Retry exercise. Those baselines remain **Unknown** until measured. No performance, test or security result is produced by this ADR.

### Authority and explicit supersession

| Source | What stands | What this ADR changes or qualifies |
|---|---|---|
| Founder-approved task brief, 2026-10-02 | Phase 1 limits, encryption from the start, no panel timers, Phase 2 direction, and design-only scope. | Architect recommendations below are labelled separately; they are not invented founder approvals. |
| **Per-(Agent, Workspace) Email Mailboxes**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/internal/architecture/ADR-033-per-pair-mailboxes.md::Decision` | One mailbox per agent/workspace pair; structural resolution and pair-keyed credentials. | Its historical inbound-drainer/Board-task paragraph is not authority for this work; the later Mail spec and current watcher supersede it. No side edit to that ADR is included here. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/internal/specs/email-mail-view-spec.md::D6`, `D21`, `D25`, `A8` | Live mail, separate watcher, coalescing, visible failure and two operations per account. | Phase 1 expressly permits short-lived **in-memory headers**, but no disk header store. Folder metadata is not mail content. Phase 2's narrow D6/D21 amendment is below. D25/A8's repeating 30-second panel refresh and one-new-session-per-request assumption are superseded by event-driven refresh and pooling. |
| **Credential Boot Contract**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/internal/architecture/ADR-004-credential-boot-contract.md::Decision` | Existing credential key/unlock ownership; no replacement key over existing encrypted data. | Cache encryption must reuse the sanctioned key-derivation seam, not export the master key or invent a second password. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/CLAUDE.md::Hard Constraints`, `Definition of Done` | Single pure-Go binary, bounded footprint, contract-first generated boundary types, reachable UI/tools and matching user documentation. | No production code, actual contracts, tests, old ADRs or user pages are edited in this design task. Later implementation owns those changes. |

**Reading conventions:** evidence labels in the final table are references to actually read files/symbols, not new requirement IDs. **Verified** means source behaviour or supplied-report contents were checked; **Inferred** means an architectural consequence or recommendation, not a tested result; **Unknown** names a genuine evidence gap. GitNexus tools/resources were not exposed in this session; direct reads/searches ground the impact assessment. No production symbol is modified.

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
