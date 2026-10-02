# Implementation Specification — Mail live access W2: folder discovery, encrypted folder metadata and the bounded header cache

**Status:** Proposed — ready for its `grill-spec` round. Design authority: [ADR-20261001 — Mail live access: pooled connections, folder discovery and a bounded cache](../architecture/ADR-20261001-mail-live-access-pooling-folder-discovery-and-cache.md) (grill round applied, founder answers Q1=A, Q2=B, Q3=A, Q4=A, Q5=A recorded 2026-10-02).
**Date:** 2026-10-02
**Work package:** W2 (discovery/metadata) of the mail live-access feature — plus the folder, count and header rows of P1.1, and the invalidation and encryption rules of P2.2–P2.3 as they apply to Phase 1. Phase 2's on-disk header cache is **explicitly out of scope** (§8).
**Implementing lead:** backend-lead (one writer). Test files are qa-lead's (RED/CHECK), per the ADR work-package table.
**Branch:** `docs/adr-mail-live-access` — spec only; no production code, contract edits, test execution or push in this delivery.
**Inputs read in full for this spec:** the ADR above; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-report.md` (findings I-01–I-06, M-01–M-02); `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/receipts/autocommit-trace.md`; the code seams cited below, each read first-hand in this checkout (`docs/adr-mail-live-access` @ `fc4c8bf6d`).
**Register:** every code claim below was verified against the file named, in this checkout, on 2026-10-02, unless labelled **Inferred** (design consequence) or **Unknown** (genuine gap). Cite `file::symbol`, never line numbers.

---

## 1. Summary and scope

**Bottom line first:** this spec turns the ADR's folder and cache decisions into buildable behaviour for one backend writer. Mail stops assuming the server's Sent and Drafts folders are literally named "Sent" and "Drafts"; it discovers them (server role attributes first, a small candidate list second, the operator's explicit setting always winning), and it stops re-asking the server for information it already holds — by keeping one small **encrypted** folder-metadata file per mailbox on disk and at most **fifty message headers per folder in memory**. Nothing in this package stores a message body, an attachment byte, or an unencrypted folder name.

The work package has three halves:

| # | Half | One-line job |
|---|---|---|
| 1 | **Folder discovery** (`pkg/email/folder_discovery.go`, proposed) | Resolve the three logical roles — Inbox, Sent, Drafts — to real server folder names, with the operator override outranking discovery, and with *unknown* never collapsed into *absent* (grill correction M-01). |
| 2 | **Encrypted folder-metadata file** (`pkg/email/cache_file.go`, proposed) | Persist resolved names, roles, UIDVALIDITY, schema/generation and last-validated time per mailbox — AES-256-GCM sealed through the existing credential-store key derivation — after the data-directory exclusion gate is deployed (§3.8; until then the disk cache is **blocked**, not merely discouraged). |
| 3 | **Bounded header cache** (`pkg/email/header_cache.go`, proposed) | Memory-only newest-50 headers per role per mailbox, five-minute freshness, dropped thirty minutes after the panel was last open, inside a four-megabyte reusable metadata budget, invalidated by the §3.11 table. |

What the user gets, in plain words: opening Mail stops failing or hanging on servers that name their folders differently; reopening a mailbox stops re-dialing just to redraw the folder rail; and nothing sensitive lands on disk in the clear or leaks into the machine's backup repository.

**In scope** (each gets normative rules in §3, stories in §5, scenarios in §6, tests in §7):

- The three stable UI roles as **logical roles**, never literal folder names (§3.1).
- Discovery via SPECIAL-USE role attributes, LIST-EXTENDED where advertised, ordinary LIST plus probing where not; an unsupported extension is **not** a whole-mailbox failure (§3.2).
- Override semantics reusing the existing `sent_folder_name` / `drafts_folder_name` settings exactly (omitted keeps, empty clears to automatic, non-empty overrides), the legacy-provenance rule, and unpersisted built-in defaults as discovery fallbacks only (§3.3).
- The finite candidate fallback list, and why only a successful probe establishes a mapping (§3.2).
- The **UNKNOWN vs ABSENT** distinction (M-01), separate evidence for genuine absence, and missing INBOX as an account error (§3.4).
- Multiple role candidates: saved mapping preferred, ambiguity surfaced, never an arbitrary pick (§3.5).
- The encrypted per-mailbox folder file: contents, refresh triggers (exactly four, never a timer, never while closed), the encryption envelope (fresh nonce, purpose-separated keys, AAD bindings, atomic ciphertext-only replace, size/version rejection before allocation), and the fact that **no exported arbitrary-file encryption helper exists today** (§3.6–3.7).
- File location, permissions (0700 dir / 0600 file; Windows equivalent stated as current-user restrictive access), and the Git/backup exclusion gate — the exact rule, the exact file, and the blocked-until-deployed status (§3.8).
- The memory header cache: newest 50 per role, envelope + flags only, 5-minute freshness with one background refresh, 30-minute post-close retention, 4 MiB global budget, never reused for search or older pages (§3.9).
- The folder publication revision (grill correction I-02) as it binds this package's cache writes (§3.10).
- The invalidation table (§3.11) and the logging rules (§3.12).
- The interfaces this package publishes and consumes, so W1 (pool runtime), W4 (gateway integration) and W0 (contracts) can be built in parallel without editing each other's files (§4).

**Out of scope:** everything listed in §8 — including the socket pool itself (W1), the gateway handlers' new metadata fields (W0/W4), the panel UI (W3), and the agent tools (W10).

---
