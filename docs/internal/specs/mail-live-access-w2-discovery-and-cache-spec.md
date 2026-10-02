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

## 2. Existing Codebase Context

> GitNexus MCP tools were **not connected in this writing session** and this worktree's index was not consulted; per `omnipus-shared-rules` rule 9 the blast-radius rows below are a first-hand Read/Grep exploration and are labelled **Inferred**. Every symbol in the table was read in this checkout on 2026-10-02. Certainty: **Verified** = read here; **Inferred** = architectural consequence.

### 2.1 Files and symbols this package touches

| File / symbol | Role today | What W2 does with it | Certainty |
|---|---|---|---|
| `pkg/email/view.go::folderNameFor` | Maps a folder slug to a literal IMAP name; Sent/Drafts ride `Account.withDefaults` (literal `"Sent"`/`"Drafts"` when unset), INBOX is fixed `"INBOX"`. | **Extends** (W2 exclusively owns all `view.go` lease/mapping changes, per the ADR work-package table): the slug→name lookup becomes slug→*resolved mapping* backed by discovery + override + saved metadata. | Verified |
| `pkg/email/view.go::FolderCounts` | Dials, STATUSes the three slugs, softens a structural `[NONEXISTENT]` on Sent/Drafts to zero counts (`isNonexistentFolder`), fails loudly otherwise. | **Extends**: counts come from the resolved mapping; availability states (`present/absent/unknown`) and nullable totals feed W0's contract change; missing INBOX stays fatal. | Verified |
| `pkg/email/view.go::isNonexistentFolder` | Structural `[NONEXISTENT]` (RFC 5530) detection via `imap.ResponseCodeNonExistent`. | **Consumes unchanged**: it is the *only* accepted absence evidence — the same structural response defines genuine ABSENT in §3.4. | Verified |
| `pkg/email/view.go::ReadFolderPage`, `::fetchMailRows`, `::MailRow` | Envelope page read: SEARCH (not-`Deleted`), newest-first, limit clamp (`defaultListLimit`=20 / `maxListLimit`=100 today), explicit truncation, one-session-per-request; `MailRow` carries UID, UIDValidity, Seen, IsDraft, ReadByAgent, IsOmnipusDraft, MessageID, From/FromName/ReplyTo/To/Cc/Subject/Date. | **Extends**: page reads consult the header cache for newest-unfiltered pages (display immediately, one background refresh when >5 min old); `MailRow` is exactly the envelope+flag field set the cache may store — no body field exists to over-store. | Verified |
| `pkg/email/view.go::ReadView`, `::ResolveRef`, `::MarkSeenIn` | Full-message read (whole-message `BODY.PEEK[]`), ref resolution, `\Seen` write. `ReadView`/`ResolveRef` take a supplied UID **without comparing its embedded epoch** to the live folder epoch — the grill's "highest-risk source integration note" (fix owned by W1/W4's reference work, not W2; W2's cache must not deepen the hazard by serving stale epochs). | **Consumes**: W2's UIDVALIDITY invalidation (§3.11) discards cached rows before a stale epoch can be published; the epoch-comparison replacement itself is outside W2. | Verified (hazard), Inferred (division of labour) |
| `pkg/email/transport.go::Account`, `::withDefaults`, `::sentFolder`, `::draftsFolder` | Connection parameters; `withDefaults` fills **unpersisted** literals `"Sent"`/`"Drafts"` when the config fields are empty. | **Consumes unchanged** — W1 owns `transport.go` and "W2 never edits transport.go" (ADR work-package table). The unpersisted default is the exact fallback-proposal behaviour §3.3 preserves as discovery input, never as an override. | Verified |
| `pkg/email/transport.go::dialIMAP`, `::runIMAP`, `::imapDial`, `::dialTimeout` (30 s), `::commandTimeout` (45 s) | Per-operation dial/authenticate/act/close; `imapDial` is the test seam. | **Consumed via W1's pool interface** once frozen (§4); until then W2 code keeps working against the client facade, never dialing privately. | Verified |
| `pkg/email/mail_budget.go::SharedMailBudget`, `::call`, `::CallValue`, `::TryCall`, `::MailBudgetRequest.flightKey`, `::flightContext` | Shared two-slot-per-account work gate + singleflight coalescing; `flightKey` = account + operation + JSON params **without pair or generation** (grill I-01); `flightContext` detaches shared flights from the first caller's cancellation (I-02's vehicle). | **Consumes** through W1's rewritten coalescing identity (pair/generation/purpose join the key — W1/W4's correction). W2's discovery and cache refreshes are budgeted operations like any other read; they never bypass the gate. | Verified |
| `pkg/credentials/store.go::Store.DeriveSubkey` | The sanctioned key seam: HKDF (`hkdf.New(sha256.New, masterKey, nil, info)`), 32 bytes, rejects empty info, `ErrStoreLocked` when locked, master key never leaves. | **Consumes**: the cache envelope derives purpose-separated keys here (§3.7). Its "Extract skipped" comment is misleading but the call is correct — do not change the derivation (the package CLAUDE.md forbids it; rotating would brick existing audit-chain HMACs). | Verified |
| `pkg/credentials/store.go::encrypt`, `::decrypt`, `aadFor` | **Unexported** AES-256-GCM entry helpers (12-byte random nonce, AAD = `"omnipus-credential-v1:" + name`). | **Cannot be reused** for cache files — not exported, entry-shaped, master-key keyed. Verified there is no exported arbitrary-file encryption helper anywhere in the package; W2 must build the small cache-envelope wrapper (§3.7) and security-lead reviews it. | Verified |
| `pkg/credentials/store.go::Store.Close` | Best-effort key wipe; the doc comment states honestly that copies (including subkeys handed out) cannot be erased. | **Consumes**: W2's shutdown path drops derived-key/plaintext buffers best-effort and says no more than that in docs. | Verified |
| `pkg/gateway/rest_mailbox.go::restAPISetAgentMailbox.persistConfig` | Raw-map config update: **omitted** override field keeps its stored value; **empty** string deletes the key (clear-to-automatic); non-empty trimmed value is stored. | **Consumes unchanged** (the ADR's E-Overrides correction): W2 adds no new override semantics to this handler. §3.3 defines how the *reader* interprets these fields. | Verified |
| `pkg/gateway/rest_mailbox.go::deleteAgentMailbox` | Removes the config entry + both stored credentials, best-effort with `logsafeError` + continue, then reloads. Touches **no files**. | **Consumed by W4's cascade** (§3.11): cache-file deletion hooks the removal path W4 owns; W2 supplies the delete primitive. | Verified |
| `pkg/gateway/rest_settings.go::createTarGz` / `::extractTarGz` | Backup walker skips only top-level `logs`/`backups`; restore skips only `config.json`, 256 MiB per-entry cap. | **Gate for §3.8**: `createTarGz` must gain the `mail-cache` top-level skip before the first disk write ships (W4's edit; W2's enablement is blocked on it). | Verified |
| `pkg/gateway/rest_mail_read.go::handleMailFolders`, `::handleMailList` | Today: resolve pair client → `mailBudgetWrap` (`listMailFolders` / `listMailMessages`) → live read → map to generated types. No cache, no metadata, no availability field. | **Consumed by W4** for wiring; W2's services slot behind these handlers without W2 editing them. | Verified |
| `pkg/config/config.go::MailboxConfig` (`SentFolderName`, `DraftsFolderName`, `json:"..._folder_name,omitempty"`) | Operator settings; `omitempty` means an empty value is *absent from JSON*, so stored-empty and never-set are the same stored state. | **Consumes**: the override/provenance rules (§3.3) read exactly these fields; no new config key is added for Phase 1. | Verified |
| `pkg/config/home.go::OmnipusHomeDir` | The mandated data-root resolver (`$OMNIPUS_HOME` → `$HOME/.omnipus` → user-private temp). | **Consumes**: the cache directory derives from this helper — never an inline `~/.omnipus` join (§3.8). | Verified |
| `pkg/fileutil/file.go::WriteFileAtomic` | Atomic file replacement primitive. | **Consumes**: all cache-file writes are atomic ciphertext-only replacements through it (§3.7). | Verified |
| `pkg/email/watcher.go` (state at `<StateDir>/email-watch/<pair>.json`; `recordFailure` accepts raw error text) | Independent watcher notice state; raw-text failure recording is an existing audit seam. | **Consumed/avoided**: the watcher never refreshes W2's caches (§3.6 triggers); W2's own new code never propagates raw upstream text into logs or state (§3.12). Fixing `recordFailure` itself is W1/W4's seam. | Verified |
| `pkg/email/view_missing_folder_test.go::TestFolderCounts_MissingSentFolderStillOpensMailbox`, `::TestFolderCounts_MissingDraftsFolderStillOpensMailbox`, `::TestFolderCounts_CancelledRequestStillFails` | The existing count-side negative controls: missing Sent/Drafts → zero counts, cancel → loud failure. | **Regression set** (§7.3): must keep passing unchanged; they cover counts only — the ADR is explicit that count green does not prove the list fix or the unknown/absent distinction. | Verified |
| `pkg/email/imapserver_test.go::startMemIMAP` (+ `view_test.go::startViewIMAP`, `::startViewIMAPRaw` with a capability set) | Real `go-imap/v2` in-memory IMAP server on loopback; restores the `imapDial` seam at cleanup; `startViewIMAPRaw` accepts a capability set (needed to test LIST-EXTENDED presence/absence). | **Test harness** for §7 (qa-lead's files; the global dial seam means tests must not blindly run in parallel). | Verified |
| `contracts/components/schemas/MailFolder.yaml`, `::MailFolderList.yaml`, `::MailMessagePage.yaml` | Today: `MailFolder` requires integer `total`, has no availability/UIDVALIDITY/mapping-source/metadata; `MailFolderList` is a bare folders array. | **W0's contract change** (proposed shapes in the ADR's wire-impact section); W2's services produce the values those fields carry. W2 edits no contract file. | Verified |
| `pkg/gateway/rest_mail_budget.go::mailBudgetWrap`, `::mailRetryParam` | Typed 503/502 mapping; human `retry=true` bypasses backoff only. | **Consumed unchanged**; cache-first/live `mode` and metadata fields ride W0's contract work behind these wrappers. | Verified |

### 2.2 Blast radius

| Symbol W2 changes | Risk | d=1 (WILL BREAK) | d=2 (SHOULD TEST) |
|---|---|---|---|
| `pkg/email/view.go::folderNameFor` (slug→name becomes slug→mapping) | **MEDIUM** | Every `view.go` caller of `folderNameFor`: `FolderCounts`, `ReadFolderPage`, `ReadView`, `MarkSeenIn`, `DeleteDraftStatus`, `ResolveRef` (all read in this checkout) | Gateway handlers mapping `FolderStat` (W4); the draft/seen/attachment paths that address folders (W4); the missing-folder tests (W5) |
| `pkg/email/view.go::FolderCounts` return shape | MEDIUM | `handleMailFolders` (W4's edit); generated `MailFolderList` consumers in the SPA (W3) after W0's regeneration | Summary endpoint is unaffected (reads watcher state only — verified `rest_mail_summary.go::handleMailSummary` never dials mail) |
| New files `folder_discovery.go`, `cache_file.go`, `header_cache.go` | LOW (new symbols, no renames) | Only their own tests | W4's integration; W10's agent tools if they later consume cached rows (they must not in Phase 1 — §8) |
| Cache directory + files under `~/.omnipus/mail-cache/` | **HIGH if the §3.8 gate is skipped** | The machine-level `git add -A` staging job would capture every cache file within one 15-minute job run and keep committing every later change (autocommit-trace §5, verified mechanism) | Backup archive (`createTarGz`), restore (`extractTarGz`), mailbox/agent/workspace deletion cascades |

No **CRITICAL** code blast radius: the largest code change is contained in `view.go`, which one writer owns. The critical risk in this package is the §3.8 deployment gate, not a Go symbol.

### 2.3 Cluster placement

This package sits in the **email transport/tools** cluster (`pkg/email`), with its integration edge in the **gateway** cluster (W4's files) and its encryption dependency on the **credentials** cluster (consumed via `DeriveSubkey` only — no credential-store algorithm change; the ADR forbids one and `pkg/credentials/CLAUDE.md` repeats it).

### 2.4 Available reference patterns

`docs/reference/go-implementation/` does not exist in this repository (same finding as the mail-view spec §3.6 — re-verified this session). In-repo patterns this spec reuses: the credentials package's AAD-binding discipline (`pkg/credentials/CLAUDE.md` — entry ciphertext bound to its name so a swapped entry fails authentication), the `email-watch/` state-file precedent (per-pair JSON under the data dir — including its *lesson*: it is un-ignored and never deleted, exactly what §3.8 must not repeat), and `fileutil.WriteFileAtomic` as the sole write primitive.

---
