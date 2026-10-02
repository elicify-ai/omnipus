# Implementation Specification — Mail live access W2: folder discovery, encrypted folder metadata and the bounded header cache

**Status:** Corrected — the one prescribed `grill-spec` correction round applied (grill report `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-1.md`, verdict BLOCK: 1 Critical, 6 Important, 10 Minor; every finding that names this spec is resolved or rebutted in place, per the finding IDs below) together with the interface-ownership corrections of the landing-order register (`mail-live-access-landing-order.md` §2 — rows 1, 3, 4, 6, 10, 12, 13, 14, 15, 16, 17, 18, 19, 21, 23, 24 touch this package) and the founder rulings of 2026-10-02 (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/mail-feature-decisions.md`, answers Q-A/Q-B/Q-C/Q-D/Q-E). Design authority: [ADR-20261001 — Mail live access: pooled connections, folder discovery and a bounded cache](../architecture/ADR-20261001-mail-live-access-pooling-folder-discovery-and-cache.md) (founder answers Q1=A, Q2=B, Q3=A, Q4=A, Q5=A recorded 2026-10-02).
**Date:** 2026-10-02 (correction round, same day)
**Package mapping (landing-order register row 24):** this spec file is **w2** = the ADR's **W2** (discovery/metadata) package. The six-file batch maps: `mail-live-access-w1-read-runtime-spec.md` = ADR-W1 (read runtime) · **this file** = ADR-W2 (discovery/cache) · `mail-live-access-w3-panel-and-settings-spec.md` = ADR-W3 (panel) · `mail-live-access-w4-attachments-and-rendering-spec.md` = ADR-W7–W10 (features) · `mail-live-access-w5-integration-and-privacy-spec.md` = ADR-W4 (integration/privacy, "w5-integration" below) · `mail-live-access-w6-proof-spec.md` = ADR-W5 (proof, qa-lead). The ADR's **W0** is a contracts *role* — backend-lead's Wave-B work package — not a spec file: none exists and none is required (register §2 row 1, §5).
**Work package:** W2 (discovery/metadata) of the mail live-access feature — plus the folder, count and header rows of P1.1, and the invalidation and encryption rules of P2.2–P2.3 as they apply to Phase 1 — **plus the correction-round additions the register assigns to this package**: the folder-scoped server search and cursor issuance (register row 4; §3.13), the targeted single-part reader (row 14; §3.14), the MIME structure classifier and the `has_attachments` derivation (row 15; §3.15), the `message_ref` same-lease validation rules in `view.go` (row 16; §3.16), the counts freshness rule and its tests (row 21; §3.9 counts block), and this package's sub-fields of the instrument record (row 17; §3.17). Phase 2's on-disk header cache is **explicitly out of scope** (§8).
**Implementing lead:** backend-lead (one writer). Test files are qa-lead's (RED/CHECK), per the ADR work-package table and the register's Wave A.
**Branch:** `docs/adr-mail-live-access` — spec only; no production code, contract edits, test execution or push in this delivery.
**Inputs read in full for this spec:** the ADR above; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-report.md` (the ADR's grill, findings I-01–I-06, M-01–M-02); `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-1.md` (this spec's grill round); `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/internal/specs/mail-live-access-landing-order.md` (the interface-ownership register); `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/mail-feature-decisions.md` (founder decisions, including the 2026-10-02 correction-round answers); the code seams cited below, each read first-hand in this checkout (`docs/adr-mail-live-access` @ `fc4c8bf6d` for the original authoring; correction round at `a81b38921`).
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
- The interfaces this package publishes and consumes, so W1 (read runtime), w5-integration (ADR-W4, the gateway files' single writer) and the ADR-W0 contracts role can be built in parallel without editing each other's files (§4; ownership per the landing-order register §2 — one publisher per interface).
- **Correction-round additions (register-assigned to this package):** the folder-scoped server search and its cursor issuance — subject + sender/recipient substring, server-side IMAP header search, inside the 25/200 bounds (founder Q-D=A; register row 4; §3.13); the targeted single-part reader (row 14; §3.14); the one MIME structure classifier and the `has_attachments` derivation (row 15; §3.15); the `message_ref` same-lease validation rules in `view.go` (row 16; §3.16); the counts freshness rule and its tests (row 21, founder Q-C=A; §3.9 counts block); and this package's sub-fields of the instrument record (row 17; §3.17). None of these reopens a founder decision; each is a register-assigned obligation landing as spec text in this one correction round.

**Out of scope:** everything listed in §8 — including the socket pool itself (W1), the gateway handlers and their new metadata fields (w5-integration = ADR-W4, with schemas defined by the ADR-W0 contracts role in Wave B), the panel UI (W3), and the agent tools (W10).

---

## 2. Existing Codebase Context

> GitNexus MCP tools were **not connected in this writing session** and this worktree's index was not consulted; per `omnipus-shared-rules` rule 9 the blast-radius rows below are a first-hand Read/Grep exploration and are labelled **Inferred**. Every symbol in the table was read in this checkout on 2026-10-02. Certainty: **Verified** = read here; **Inferred** = architectural consequence.

### 2.1 Files and symbols this package touches

| File / symbol | Role today | What W2 does with it | Certainty |
|---|---|---|---|
| `pkg/email/view.go::folderNameFor` | Maps a folder slug to a literal IMAP name; Sent/Drafts ride `Account.withDefaults` (literal `"Sent"`/`"Drafts"` when unset), INBOX is fixed `"INBOX"`. | **Extends** (W2 exclusively owns all `view.go` lease/mapping changes, per the ADR work-package table): the slug→name lookup becomes slug→*resolved mapping* backed by discovery + override + saved metadata. | Verified |
| `pkg/email/view.go::FolderCounts` | Dials, STATUSes the three slugs, softens a structural `[NONEXISTENT]` on Sent/Drafts to zero counts (`isNonexistentFolder`), fails loudly otherwise. | **Extends**: counts come from the resolved mapping; availability states (`present/absent/unknown`) and nullable totals feed W0's contract change; missing INBOX stays fatal. | Verified |
| `pkg/email/view.go::isNonexistentFolder` | Structural `[NONEXISTENT]` (RFC 5530) detection via `imap.ResponseCodeNonExistent`. | **Consumes unchanged**: it is the *only* accepted absence evidence — the same structural response defines genuine ABSENT in §3.4. | Verified |
| `pkg/email/view.go::ReadFolderPage`, `::fetchMailRows`, `::MailRow` | Envelope page read: SEARCH (not-`Deleted`), newest-first, limit clamp (`defaultListLimit`=20 / `maxListLimit`=100 today), explicit truncation, one-session-per-request; `MailRow` carries UID, UIDValidity, Seen, IsDraft, ReadByAgent, IsOmnipusDraft, MessageID, From/FromName/ReplyTo/To/Cc/Subject/Date. | **Extends**: page reads consult the header cache for newest-unfiltered pages (display immediately, one background refresh when >5 min old); `MailRow` is exactly the envelope+flag field set the cache may store — no body field exists to over-store. **Correction-round extension (register row 4):** the same `view.go` leased-session path gains the folder-scoped server search and its cursor issuance (§3.13) — the wire shapes (`search` param, `next_cursor`, `has_more`, `view_limit_reached`, typed 409) are the ADR-W0 contracts role's, requested by w5-integration (register row 4); the SEARCH execution and cursor issuance in `view.go` are this package's one new story. | Verified |
| `pkg/email/view.go::ReadView`, `::ResolveRef`, `::MarkSeenIn` | Full-message read (whole-message `BODY.PEEK[]`), ref resolution, `\Seen` write. `ReadView`/`ResolveRef` take a supplied UID **without comparing its embedded epoch** to the live folder epoch — the grill's "highest-risk source integration note" (the same seam the register's row 16 settles). | **Extends (correction round; register row 16):** the `message_ref` same-lease validation rules land in `view.go` — this package's files — as normative FRs (§3.16): a reference is validated against the live folder epoch/generation (using W1's published same-lease validation capability, w1 §3.1 lease row) before any read or `\Seen` write, and a stale/mismatched ref is refused with a visible typed result. Previously this spec disclaimed the fix to "W1/W4's reference work" — superseded by register row 16, which splits it: **w5-integration mints** the gateway-issued reference, **W1 publishes the same-lease validation capability**, **W2 validates in `view.go`**. W2's UIDVALIDITY invalidation (§3.11) additionally discards cached rows before a stale epoch can be published. | Verified (hazard), Verified (register-settled division) |
| `pkg/email/transport.go::Account`, `::withDefaults`, `::sentFolder`, `::draftsFolder` | Connection parameters; `withDefaults` fills **unpersisted** literals `"Sent"`/`"Drafts"` when the config fields are empty. | **Consumes unchanged** — W1 owns `transport.go` and "W2 never edits transport.go" (ADR work-package table). The unpersisted default is the exact fallback-proposal behaviour §3.3 preserves as discovery input, never as an override. | Verified |
| `pkg/email/transport.go::dialIMAP`, `::runIMAP`, `::imapDial`, `::dialTimeout` (30 s), `::commandTimeout` (45 s) | Per-operation dial/authenticate/act/close; `imapDial` is the test seam. | **Consumed via W1's pool interface** once frozen (§4); until then W2 code keeps working against the client facade, never dialing privately. | Verified |
| `pkg/email/mail_budget.go::SharedMailBudget`, `::call`, `::CallValue`, `::TryCall`, `::MailBudgetRequest.flightKey`, `::flightContext` | Shared two-slot-per-account work gate + singleflight coalescing; `flightKey` = account + operation + JSON params **without pair or generation** (grill I-01); `flightContext` detaches shared flights from the first caller's cancellation (I-02's vehicle). | **Consumes** through W1's rewritten coalescing identity (pair/generation/purpose join the key — the I-01 correction, implemented by W1 and W2 in their own files per the ADR's correction-round ownership). W2's discovery and cache refreshes are budgeted operations like any other read; they never bypass the gate. | Verified |
| `pkg/credentials/store.go::Store.DeriveSubkey` | The sanctioned key seam: HKDF (`hkdf.New(sha256.New, masterKey, nil, info)`), 32 bytes, rejects empty info, `ErrStoreLocked` when locked, master key never leaves. | **Consumes**: the cache envelope derives purpose-separated keys here (§3.7). Its "Extract skipped" comment is misleading but the call is correct — do not change the derivation (the package CLAUDE.md forbids it; rotating would brick existing audit-chain HMACs). | Verified |
| `pkg/credentials/store.go::encrypt`, `::decrypt`, `aadFor` | **Unexported** AES-256-GCM entry helpers (12-byte random nonce, AAD = `"omnipus-credential-v1:" + name`). | **Cannot be reused** for cache files — not exported, entry-shaped, master-key keyed. Verified there is no exported arbitrary-file encryption helper anywhere in the package; W2 must build the small cache-envelope wrapper (§3.7) and security-lead reviews it. | Verified |
| `pkg/credentials/store.go::Store.Close` | Best-effort key wipe; the doc comment states honestly that copies (including subkeys handed out) cannot be erased. | **Consumes**: W2's shutdown path drops derived-key/plaintext buffers best-effort and says no more than that in docs. | Verified |
| `pkg/gateway/rest_mailbox.go::restAPISetAgentMailbox.persistConfig` | Raw-map config update: **omitted** override field keeps its stored value; **empty** string deletes the key (clear-to-automatic); non-empty trimmed value is stored. | **Consumes unchanged** (the ADR's E-Overrides correction): W2 adds no new override semantics to this handler. §3.3 defines how the *reader* interprets these fields. | Verified |
| `pkg/gateway/rest_mailbox.go::deleteAgentMailbox` | Removes the config entry + both stored credentials, best-effort with `logsafeError` + continue, then reloads. Touches **no files**. | **Consumed by w5-integration's removal cascade** (ADR-W4's gateway files; §3.11): cache-file deletion hooks the removal path w5-integration owns; W2 supplies the delete primitive. Per founder Q-B=A the same cascade **also purges the watcher state file** (`email-watch/`) — decided 2026-10-02, routed to the cascade owner (§3.8 rule E-5); not W2's code. | Verified |
| `pkg/gateway/rest_settings.go::createTarGz` / `::extractTarGz` | Backup walker skips only top-level `logs`/`backups`; restore skips only `config.json`, 256 MiB per-entry cap. | **Gate for §3.8 (backup half of the product-owned exclusion)**: `createTarGz` must gain the `mail-cache` top-level skip before the first disk write ships — edited in **w5-integration's wave** (the gateway files' single writer; register row 18), not by any external machine provisioning. Old archives are §3.11's restore-rejection case (OQ-5). | Verified |
| `pkg/gateway/rest_mail_read.go::handleMailFolders`, `::handleMailList` | Today: resolve pair client → `mailBudgetWrap` (`listMailFolders` / `listMailMessages`) → live read → map to generated types. No cache, no metadata, no availability field. | **Consumed by w5-integration** for wiring (register row 6 also routes the REST `observer_id` query param through its §8 queue); W2's services slot behind these handlers without W2 editing them. | Verified |
| `pkg/config/config.go::MailboxConfig` (`SentFolderName`, `DraftsFolderName`, `json:"..._folder_name,omitempty"`) | Operator settings; `omitempty` means an empty value is *absent from JSON*, so stored-empty and never-set are the same stored state. | **Consumes**: the override/provenance rules (§3.3) read exactly these fields; no new config key is added for Phase 1. | Verified |
| `pkg/config/home.go::OmnipusHomeDir` | The mandated data-root resolver (`$OMNIPUS_HOME` → `$HOME/.omnipus` → user-private temp). | **Consumes**: the cache directory derives from this helper — never an inline `~/.omnipus` join (§3.8). | Verified |
| `pkg/fileutil/file.go::WriteFileAtomic` | Atomic file replacement primitive. | **Consumes**: all cache-file writes are atomic ciphertext-only replacements through it (§3.7). | Verified |
| `pkg/email/watcher.go` (state at `<StateDir>/email-watch/<pair>.json`; `recordFailure` accepts raw error text — verified: `::Cycle` calls `recordFailure(classifyMailError(err), err.Error())`) | Independent watcher notice state; raw-text failure recording is an existing audit seam. | **Consumed/avoided**: the watcher never refreshes W2's caches (§3.6 triggers); W2's own new code never propagates raw upstream text into logs or state (§3.12). **Correction (grill C-1, register row 19):** the `recordFailure` raw-text fix is **w1's** obligation, inside this file — w1's exclusive ownership — and this spec's earlier "W1/W4" attribution is withdrawn; the gateway's `mailErr502` raw-error seam is **w5-integration's** fix (its own file). W2 owns neither file and never adds a third raw-text seam. Per founder Q-B=A the state file is additionally excluded from staging/backups and purged on mailbox removal (§3.8 rule E-5). | Verified |
| `pkg/email/view_missing_folder_test.go::TestFolderCounts_MissingSentFolderStillOpensMailbox`, `::TestFolderCounts_MissingDraftsFolderStillOpensMailbox`, `::TestFolderCounts_CancelledRequestStillFails` | The existing count-side negative controls: missing Sent/Drafts → zero counts, cancel → loud failure. | **Regression set** (§7.3): must keep passing unchanged; they cover counts only — the ADR is explicit that count green does not prove the list fix or the unknown/absent distinction. | Verified |
| `pkg/email/imapserver_test.go::startMemIMAP` (+ `view_test.go::startViewIMAP`, `::startViewIMAPRaw` with a capability set) | Real `go-imap/v2` in-memory IMAP server on loopback; restores the `imapDial` seam at cleanup; `startViewIMAPRaw` accepts a capability set (needed to test LIST-EXTENDED presence/absence). | **Test harness** for §7 (qa-lead's files; the global dial seam means tests must not blindly run in parallel). | Verified |
| `contracts/components/schemas/MailFolder.yaml`, `::MailFolderList.yaml`, `::MailMessagePage.yaml` | Today: `MailFolder` requires integer `total`, has no availability/UIDVALIDITY/mapping-source/metadata; `MailFolderList` is a bare folders array. | **The ADR-W0 contracts role's change** — there is no W0 *spec file*: the role is backend-lead's Wave-B work package, owned and scheduled (register row 1, §5; grill I-2), and the authoritative requirement set is the union of the six specs' queues with this register as the cross-check. Shapes are proposed in the ADR's wire-impact section, with the register's row-3 settlement adopted: **five-value `mapping_source`** (`override|special_use|fallback|saved|none`), superseding the ADR's four-value proposal (grill M-7, recorded register row 3). W2's services produce the values those fields carry. W2 edits no contract file. | Verified |
| `pkg/email/attachment_parts.go` (proposed) | Does not exist today (new file, new symbols only). | **New W2-owned file (register rows 14–15):** the targeted single-part reader — part-specific PEEK on the leased session (§3.14) — plus **the one MIME structure walker** and the `has_attachments` derivation (§3.15). Register rule R-4: one walker, no second; w4-features and w6 consume it, never re-implement it. | Verified (absence) |
| `pkg/gateway/rest_mail_budget.go::mailBudgetWrap`, `::mailRetryParam` | Typed 503/502 mapping; human `retry=true` bypasses backoff only. | **Consumed unchanged**; cache-first/live `mode` and metadata fields ride W0's contract work behind these wrappers. | Verified |

### 2.2 Blast radius

| Symbol W2 changes | Risk | d=1 (WILL BREAK) | d=2 (SHOULD TEST) |
|---|---|---|---|
| `pkg/email/view.go::folderNameFor` (slug→name becomes slug→mapping) | **MEDIUM** | Every `view.go` caller of `folderNameFor`: `FolderCounts`, `ReadFolderPage`, `ReadView`, `MarkSeenIn`, `DeleteDraftStatus`, `ResolveRef` (all read in this checkout) | Gateway handlers mapping `FolderStat` (w5-integration); the draft/seen/attachment paths that address folders (w5-integration); the missing-folder tests (w6-proof) |
| `pkg/email/view.go::FolderCounts` return shape | MEDIUM | `handleMailFolders` (w5-integration's edit); generated `MailFolderList` consumers in the SPA (W3) after the ADR-W0 contracts role's Wave-B regeneration | Summary endpoint is unaffected (reads watcher state only — verified `rest_mail_summary.go::handleMailSummary` never dials mail) |
| New files `folder_discovery.go`, `cache_file.go`, `header_cache.go`, `attachment_parts.go` (proposed) | LOW (new symbols, no renames) | Only their own tests | w5-integration's handlers consume the services (they never re-implement them, register R-4); w4-features' attachment rows consume the part reader and MIME classifier consume-only (register rows 14–15); W10's agent tools if they later consume cached rows (they must not in Phase 1 — §8) |
| Search + cursor issuance in `view.go` (correction round; register row 4) | MEDIUM (extends the `ReadFolderPage` family whose callers §2.2 already names) | The same six internal `view.go` callers | w5-integration's list handler maps the new `search`/cursor shapes (requested from the ADR-W0 contracts role, register row 4); w3's panel consumes the results; w6's measurement arms assert the 25/200 bounds |
| Cache directory + files under `~/.omnipus/mail-cache/` | **HIGH if the §3.8 product-owned exclusion is not enforced** | Any data-directory version-control staging that captures untracked files would stage and keep committing every later cache change; unexcluded application backups would archive them — the exposure class the §3.8 exclusion exists to make impossible on **every install**, by the product's own means (its own backup-skip edit plus its own pre-write staging-exclusion check, founder ruling Q-A 2026-10-02) | Backup archive (`createTarGz`), restore (`extractTarGz`), mailbox/agent/workspace deletion cascades |

No **CRITICAL** code blast radius: the largest code change is contained in `view.go`, which one writer owns. The critical risk in this package is the §3.8 deployment gate, not a Go symbol.

### 2.3 Cluster placement

This package sits in the **email transport/tools** cluster (`pkg/email`), with its integration edge in the **gateway** cluster (w5-integration's files — ADR-W4) and its encryption dependency on the **credentials** cluster (consumed via `DeriveSubkey` only — no credential-store algorithm change; the ADR forbids one and `pkg/credentials/CLAUDE.md` repeats it).

### 2.4 Available reference patterns

`docs/reference/go-implementation/` does not exist in this repository (same finding as the mail-view spec §3.6 — re-verified this session). In-repo patterns this spec reuses: the credentials package's AAD-binding discipline (`pkg/credentials/CLAUDE.md` — entry ciphertext bound to its name so a swapped entry fails authentication), the `email-watch/` state-file precedent (per-pair JSON under the data dir — including its *lesson*: it is un-ignored and never deleted, exactly what §3.8 must not repeat), and `fileutil.WriteFileAtomic` as the sole write primitive.

---

## 3. Design — normative behaviour

### 3.1 The three stable UI roles are logical roles

The panel shows exactly three folders — Inbox, Sent, Drafts — forever, on every server (the ADR's P1.1 folder-choices row; the closed slug set already exists as `pkg/email/view.go::FolderSlugs`). **The slug is a *role*, a job the folder does, never a folder name.** A server's Sent folder may be named `Sent Items`, `[Gmail]/Sent Mail`, `INBOX.Sent`, `Отправленные`, or anything else; the role is stable even when no folder fills it or two folders could fill it.

Normative rules:

- **R-3.1-1.** Every folder-addressed interface (REST parameter, cache key, wire `slug`) continues to use only the closed slug set `inbox | sent | drafts`. A server folder name never appears in a slug position, a log field, or a cache filename. (Citations: `pkg/email/view.go::FolderSlugs`, `::FolderInbox`; the wire schema `contracts/components/schemas/MailFolder.yaml` enum.)
- **R-3.1-2.** Resolution maps role → server folder name at runtime; the mapping is *state* (memory + the encrypted file), never a hardcoded assumption. Today's literal fallback in `pkg/email/transport.go::Account.withDefaults` (`"Sent"`/`"Drafts"`) is demoted to a *candidate proposal* (§3.2 step 4) — it is unpersisted and proves nothing about the server.
- **R-3.1-3.** INBOX is fixed: role `inbox` resolves to the IMAP name `INBOX` and participates in no discovery (today's `folderNameFor` behaviour, preserved). A failed INBOX operation is an account error (§3.4), never an empty account.
- **R-3.1-4.** Discovery never creates a server folder. No `CREATE`, ever, on any path in this package — not during discovery, not as a convenience for a missing Drafts folder (ADR P1.1 "truly absent folder" row).

### 3.2 Discovery through role attributes, with probing fallback

Discovery answers one question: *which real server folder fills each optional role (sent, drafts)?* Its inputs are the authenticated IMAP session (obtained through W1's pool/budget interfaces — W2 never dials privately), the operator's override fields (§3.3), and the saved encrypted metadata (§3.6). Its output is a **role mapping**: for each role, one of —

| Outcome | Meaning |
|---|---|
| `present(name, uidvalidity, source)` | A folder was resolved and validated to exist; `source` is one of `override`, `special_use`, `fallback`, `saved` — with `none` as the not-resolved wire value: **five values total**, the register's row-3 settlement, which supersedes the ADR's proposed four-value wire enum and must be adopted in Wave B (grill M-7). |
| `absent` | Genuine, structurally confirmed absence (§3.4 evidence bar) — only reachable for sent/drafts, never inbox. |
| `unknown(reason)` | Not resolved *and* not proven absent: discovery failed, was unsupported, or found nothing it could prove (M-01). |

Normative procedure, in order (each step only when the previous produced no result for the role):

1. **Override wins outright.** If the mailbox has a non-empty stored `sent_folder_name` / `drafts_folder_name`, that name *is* the candidate; discovery probes it for existence + UIDVALIDITY. On success: `present(name, source=override)` — no server enumeration is needed for that role. On structural `[NONEXISTENT]`: the override is reported as an actionable settings warning (ADR P1.3 "saved role now missing" row) — it is never silently replaced by another folder, and it is never silently ignored. On any other probe error: `unknown(reason)`, with the override still shown in settings.
2. **Saved mapping, still valid.** If the encrypted metadata holds a mapping for the role whose source is `special_use` or `fallback`, and validation confirms the folder still exists with the recorded UIDVALIDITY, the saved mapping stands (`source=saved`). **Validation is lazy by design (grill M-10 correction):** the confirmation is performed by the folder's own first ordinary operation on the leased session — the SELECT/STATUS epoch check W1's lease already returns to the borrower (w1 §3.1 lease row) — **never by a dedicated extra probe command issued at open time**; a valid saved list therefore opens with zero discovery commands (US-5.2), and a `saved` mapping the server no longer confirms is detected at that first ordinary use, discarded per §3.11, and rediscovery runs — exactly one immediate coalesced rediscovery while the panel is open (ADR P1.3).
3. **SPECIAL-USE role attributes (RFC 6154).** If the server advertises support (CAPABILITY contains `LIST-EXTENDED` and `SPECIAL-USE` — verified available to inspect via the go-imap client capability set, the same mechanism `pkg/email/view.go::DeleteDraftStatus` already uses for `imap.CapUIDPlus`), issue one LIST-EXTENDED command selecting folders with `\Sent` / `\Drafts` special-use attributes. A matching folder gives `present(name, source=special_use)` after an existence/UIDVALIDITY probe.
4. **Candidate fallback list.** If LIST-EXTENDED is unavailable, advertises nothing useful for a role, or no role attribute matched: probe, in this fixed deterministic order, the candidates —
   - Sent: `Sent`, `Sent Items`, `Sent Messages`, `[Gmail]/Sent Mail`
   - Drafts: `Drafts`, `Draft`, `[Gmail]/Drafts`

   The **first** candidate whose probe succeeds establishes `present(name, source=fallback)`. The list is a set of *proposals*, deliberately finite: it covers the dominant real-world namings without pretending to know every provider's layout, and its failures carry no conclusion (§3.4). The unpersisted `withDefaults` literals (`Sent`, `Drafts`) are simply the first entries of these lists — nothing else in the code path may treat them as knowledge.
5. **Nothing resolved → `unknown`.** No special-use role recognized **and** no candidate probe succeeded **and** no override exists ⇒ `unknown(reason)`. Never `absent` (§3.4). The response offers the existing per-mailbox name setting as the remedy; a safe diagnostic class is recorded (§3.12).

Normative rules:

- **R-3.2-1.** *Only a successful probe establishes a mapping.* A candidate name's presence in the list, its appearance in an unextended LIST reply, or a provider naming convention is never sufficient. The probe is the same structural existence check `isNonexistentFolder` already interprets (`[NONEXISTENT]` vs. exists), plus a UIDVALIDITY read.
- **R-3.2-2.** An unsupported extension is **not a whole-mailbox failure**: LIST-EXTENDED/SPECIAL-USE absence degrades to step 4 for that mailbox, and the mailbox otherwise works normally. Capability detection failure, malformed capability strings, and partial advertisement (e.g. `LIST-EXTENDED` without `SPECIAL-USE`) are all just "use the fallback list".
- **R-3.2-3.** Discovery is bounded and coalesced: it runs as one budgeted operation under the shared account gate (consuming W1's rewritten coalescing identity, correction I-01), inside the ordinary read-work deadline; it fetches folder names and status, never message data.
- **R-3.2-4.** Discovery is a *read*: no flag changes (probe SELECTs use the same peek/examine discipline as existing reads), no EXPUNGE, no CREATE, no SUBSCRIBE.
- **R-3.2-5.** Every discovery result — success, `unknown`, or failure — publishes only through the §3.10 publication-revision check before it may touch the cache, the encrypted file, or a response.

### 3.3 Overrides: reuse the existing settings exactly, with the provenance rule

The per-mailbox override already exists end-to-end and W2 adds **no new field, no new flag, no migration**. The stored fields are `sent_folder_name` / `drafts_folder_name` on `pkg/config/config.go::MailboxConfig`; the update semantics already live in `pkg/gateway/rest_mailbox.go::restAPISetAgentMailbox.persistConfig` (read this checkout): **omitted field keeps its stored value; empty string deletes the key (clear-to-automatic); non-empty trimmed string is stored.** The Connectors schema already round-trips them (`contracts/components/schemas/Mailbox.yaml`, `::MailboxConfigureRequest.yaml`).

Normative rules:

- **R-3.3-1.** Reading: a non-empty stored value is an **override** (mapping source `override`) for its role and outranks every discovery result. Discovery still validates it (R-3.2-1) because a stale override must be *warned about*, not obeyed blindly into failures — but validation never *replaces* it (ADR P1.3: "an explicit missing override remains an actionable settings warning, not a reason to ignore the override").
- **R-3.3-2.** An empty (or absent) stored value means **automatic**: discovery resolves the role per §3.2. The Connectors UI presents this as an explicit "Automatic" choice — clearing the field is the mechanism, exactly as `persistConfig` implements today.
- **R-3.3-3.** **Legacy provenance.** A stored name whose operator intent is unprovable is shown as an **override** until the user chooses Automatic — it is *never silently discarded, never silently reclassified*, and intent is never inferred from the string's content (`"Sent"` stored by an operator means the same as any other stored name). The product has never written these fields itself (the mail-view spec added them as operator-settable with default empty; verified in `pkg/config/config.go::MailboxConfig` and the §2.1 setter behaviour), so every non-empty stored value is treated as deliberate. Unpersisted built-in defaults (`withDefaults`' literals) are discovery fallbacks only (R-3.1-2) — they are not overrides and never enter the saved metadata as such.
- **R-3.3-4.** No discovered name is written back into operator configuration. The discovered mapping lives in the encrypted metadata file only (§3.6); `config.json` keeps holding exactly what the operator typed. This keeps the ADR's boundary: configuration is operator intent; discovery state is disposable cache.

### 3.4 UNKNOWN vs ABSENT — the M-01 correction, normative

The grill's M-01 finding and the ADR's correction make this distinction a hard requirement: **a failed candidate sweep is unresolved, not evidence of absence.**

- **`unknown` (unresolved):** the role could not be resolved and absence was not proven. This is the outcome when: discovery commands failed or timed out; authentication, TLS, DNS or permission errors occurred anywhere in the flow; LIST-EXTENDED was unsupported and no candidate probe succeeded; or LIST succeeded but the server simply holds an untagged, locally named Sent folder outside the finite candidate list — from the client's seat, that case is *indistinguishable* from "no candidates exist". The role renders as **unresolved**, the response carries `availability=unknown` (W0's proposed field), and the UI offers the existing per-mailbox name setting. The user is never told "the server has no such folder" on this evidence.
- **`absent` (confirmed):** requires **both** of these, and nothing less:
  1. discovery completed successfully — the server answered the enumeration (LIST or LIST-EXTENDED) without error; and
  2. every candidate applicable to the role — the override name when set, otherwise the full §3.2 candidate list — returned the server's **structural** not-found response, the same `[NONEXISTENT]` response code `pkg/email/view.go::isNonexistentFolder` already detects.

  Only then may the role render as genuinely absent (`availability=absent`, `total=0` plus the server-folder-absent explanation; list returns an empty array rather than a mailbox-level 502).
- **Anything else is `unknown`**: network, permission, timeout, auth and TLS failures during discovery or probing all leave the role `unknown`, never empty (ADR P1.3 "no special-use role" row and Failure-behaviour table).
- **Missing INBOX is an account error, always.** INBOX takes no discovery, has no candidate list, and has no absent state: a structural not-found or any other failure on `inbox` fails the mailbox read loudly with the safe transport class, exactly as today's `FolderCounts` treats non-inbox errors — but for INBOX even `[NONEXISTENT]` is fatal. A healthy account always has an INBOX; "empty" is a count, not an availability state.
- **Counts inherit the distinction.** An unknown/absent role's count is **not invented**: `absent` yields a real zero with the explanation; `unknown` yields `total=null` (W0's nullable field), never a fabricated 0 that would masquerade as "checked, empty".

### 3.5 Multiple role candidates

When more than one server folder could fill a role (two folders carry `\Sent`; or the saved mapping and a special-use attribute disagree; or two candidates probe successfully — the procedure stops at the first, so this primarily arises across saved-vs-fresh disagreement):

- **R-3.5-1.** A **still-valid saved mapping wins** over a fresh ambiguity: if the encrypted metadata holds a validated mapping for the role, it stands and no ambiguity is surfaced. (Rationale: the user already chose or already accepted this folder; re-asking on every open would make the saved mapping worthless.)
- **R-3.5-2.** Otherwise the response **shows the ambiguity and lets the user choose** through the visible per-mailbox name setting: the role is reported unresolved-by-ambiguity (an `unknown` variant with a distinct safe reason class), the settings path is offered, and the mail server is not touched for that role beyond the enumeration already performed.
- **R-3.5-3.** **Never send or draft into an arbitrarily picked folder.** The compose, draft-APPEND and send-save paths resolve their destination folder through this same mapping; when it is unresolved or ambiguous they fail with a visible, actionable settings error instead of picking a folder. (The ADR's P1.3 "Multiple role candidates" row, made concrete for the write paths.)
- **R-3.5-4.** The user's choice is expressed exactly one way: a non-empty value in the per-mailbox setting (R-3.3-1). There is no separate "chosen mapping" store, no second preference surface.

---

### 3.6 The encrypted per-mailbox folder-metadata file

One small encrypted file per mailbox holds the resolved folder state, so reopening a mailbox does not rediscover from scratch. It is **metadata, not mail content**: folder names, roles and server identity versions. The ADR's Phase 1 clarification is explicit that folder names are not mail content (D6 stands for this file) but *are* sensitive — names can reveal projects and people — which is precisely why the file is encrypted **and** excluded from version control and backups (§3.8).

**Payload (the plaintext inside the envelope; never on disk in this form):**

| Field | Content | Notes |
|---|---|---|
| `schema_version` | Integer, monotonically bumped on payload-shape change | An unknown version refuses the whole file (R-3.7-7) |
| `pair_identity` | Opaque pair identifier | Must equal the requesting pair's opaque ID (R-3.7-6) |
| `config_generation` | Opaque generation token supplied by the caller (**decided**, register row 12: w5-integration implements the construction — fingerprint of the canonical pair identity plus the persisted config epoch; this spec consumes the value opaquely; former open question OQ-2) | The generation active when this snapshot was written |
| `transport` | `"imap"` in Phase 1 | Room for Phase 2's JMAP without a format break |
| `roles` | Per role (`sent`, `drafts`): resolved name, mapping source, UIDVALIDITY (nullable — unknown before first validation), availability (`present`/`absent`/`unknown`), ambiguity list when applicable | `inbox` participates but stores only its last validated UIDVALIDITY |
| `last_validated_at` | RFC 3339 timestamp of the last **successful** server validation | Never advanced by a failure, a cache hit, or a superseded read (§3.10) |
| `saved_at` | RFC 3339 write time | The 24-hour trigger (below) reads this |

Boundaries already decided upstream and honoured here: **no header, subject, address or count data** enters this file (ADR P1.1 folder-list-cache row); the Phase 1 per-mailbox budget for it is **64 KiB** (founder Q1=A) — an oversized payload is a visible cache-unavailable outcome, never truncation.

**Refresh triggers — exactly four, closed list** (ADR P1.1 folder-list-cache row; the ADR's P1.4 "first-open interpretation"):

| # | Trigger | Behaviour |
|---|---|---|
| 1 | **First open of a mailbox** — the metadata file is missing, unreadable, or holds no validated mapping | One discovery populates it ("first-open" = populating a missing/never-validated mapping; a successfully saved list is *not* re-discovered on every reopen) |
| 2 | **Panel open with a saved list older than 24 hours** | One discovery refreshes it before/alongside the panel's first read |
| 3 | **Manual Refresh** (the user's explicit action; the contract's `refresh_mapping` flag — W0's proposed parameter) | One discovery, coalesced with concurrent identical refreshes |
| 4 | **One immediate rediscovery** after a missing-folder failure or an observed folder-version change (UIDVALIDITY), while the panel is open | Exactly one; a failed rediscovery is reported as the failure it is, never retried in a loop |

**Equally normative — when it must NOT refresh:**

- **Never on a repeating timer.** No ticker, no periodic job, no 15-minute anything. Local housekeeping (retention cleanup of *this* file's expired siblings) is permitted while closed but issues zero IMAP commands (ADR P1.3/test strategy "local deletion … must not issue IMAP commands").
- **Never while the panel is closed.** The watcher detecting a version change while the panel is closed marks the metadata **dirty only** (invalidation flag); the actual rediscovery waits for the next eligible panel event (ADR P1.3 "panel closed when watcher detects a version change" row).
- **Panel close defers.** A panel close marks dirty and stops; it does not schedule work.

### 3.7 The encryption envelope

**Verified starting point:** `pkg/credentials/store.go` provides `Store.DeriveSubkey` — the sanctioned reusable key helper (HKDF-SHA256 over the master key with a caller info string, 32 bytes, `ErrStoreLocked` when locked, master key never exported) — and **nothing else W2 can reuse**: `encrypt`, `decrypt` and `aadFor` are **unexported credential-entry helpers** (all read this checkout), not an exported arbitrary-file encryption API. **A small cache-envelope wrapper must therefore be built in this package** (`cache_file.go`), reusing `DeriveSubkey` for keys and `fileutil.WriteFileAtomic` for writes. security-lead reviews the wrapper (ADR work-package table); the credential-store algorithm is not touched.

Envelope rules (each is the ADR's §Security "What must be built" list, made checkable):

- **R-3.7-1. Algorithm.** AES-256-GCM, authenticated encryption only. A reader that fails authentication returns failure — it never returns partial or unauthenticated plaintext, and there is **no plaintext fallback path** in either direction (write or read).
- **R-3.7-2. Fresh nonce.** Every write draws a fresh cryptographically random nonce (the credential store's own `encrypt` uses 12 bytes from `crypto/rand` — same shape here). Two writes of identical plaintext MUST produce different ciphertexts; §7's dataset asserts this directly.
- **R-3.7-3. Purpose-separated keys.** The file key is `Store.DeriveSubkey` with a stable, namespaced purpose string dedicated to folder metadata — e.g. `"omnipus-mail-folder-cache-v1"` — distinct from the Phase 2 headers/transport-state purpose and from every other subsystem's info string (the audit-chain precedent: `DeriveSubkey(audit.AuditChainKeyInfo)`). Distinct purposes get cryptographically independent keys, so one compromised purpose does not cross into the other. Bumping the version suffix is the supported rotation path for the *derivation* — never a silent change.
- **R-3.7-4. AAD bindings.** The envelope's additional authenticated data binds, at minimum: the envelope purpose tag, the schema version, the opaque pair identity, the account/configuration generation, and the transport (`imap`). Effect: a file copied between pairs, across generations, or from another purpose fails authentication instead of decrypting into plausible-looking garbage. The reader **compares the authenticated identity against its independently resolved expected pair/generation** — the envelope's word is never accepted as its own authority (the grill's encryption disposition).
- **R-3.7-5. Atomic ciphertext-only replacement.** Writes go through `pkg/fileutil/file.go::WriteFileAtomic` with the full new ciphertext; no plaintext temporary or journal file ever exists on disk. A crash mid-write leaves either the old complete file or the new complete file (both validly sealed), never a torn one.
- **R-3.7-6. Reject before allocating.** A stored file that is oversized (beyond a small multiple of the 64 KiB payload budget), carries an unsupported schema version or envelope version, or is malformed (truncated, bad nonce length, base64 failure) is rejected **before unbounded allocation** — length caps are checked before reads that could materialize the bytes.
- **R-3.7-7. Never salvage.** A corrupt, foreign, stale-generation, or expired snapshot is rejected outright: no best-effort partial import, no "most fields looked fine" recovery, and the failure surfaces as a **cache warning** with the live path still available — never as a silently empty mailbox and never as fabricated folder data.
- **R-3.7-8. Key lifecycle.** New reads/writes require valid derived keys: a locked store yields `ErrStoreLocked` → the cache layer reports cache-unavailable and runs live-only; on unlock/rotation, in-memory keys and cached data are dropped and the file is rebuilt only after normal credential resolution succeeds. **A missing or wrong master key is never "fixed" by minting a new one over existing encrypted data** (the credential boot contract; ADR-004). A file that will not decrypt while credentials are otherwise valid is a cache warning + live rebuild, not a credential error.

### 3.8 File location, permissions, and the product-owned exclusion gate

**Location.** The cache lives under the resolved application data root, derived through `pkg/config/home.go::OmnipusHomeDir()` — never an inline `~/.omnipus` join, never a hard-coded Mac path. Shape:

```
<OmnipusHomeDir()>/mail-cache/<opaque-pair-id>/folders.enc
```

- `<opaque-pair-id>` is an unambiguous opaque identifier for the (agent, workspace) pair — **not** an email address, **not** a folder name, **not** the watcher's lossy filename sanitizer (which can collide two pairs onto one file; ADR filesystem table). The minting is **decided** (register row 12): w5-integration mints a **random 128-bit value per pair, stored beside the pair's config entry** (this spec's former open question OQ-1, option (a), now settled); the interface (§4) treats it as an opaque string supplied by the caller.
- Outside `workspaces/` and agent-visible mounts: a generic agent file tool must never become a decryption route (ADR filesystem table; the path joins existing agent filesystem/shell policy wiring — w5-integration's integration).

**Permissions.** The `mail-cache/` directory is created **0700** (owner-only) and every file **0600**, on Unix (Linux/macOS). On **Windows** there is no chmod: the equivalent is **current-user restrictive access** — the directory and files are created with ACLs granting access to the current user only (no inherited broad grants, no Everyone/Users entries). The claim in user docs is "restricted to the current user", never a chmod-only assertion; Windows behaviour needs its own executed proof (the ADR lists this as an explicit future-proof obligation — carried in §9's DoD as part of the gate evidence, not claimed here).

**The product-owned exclusion guarantee (founder ruling Q-A, 2026-10-02; register row 18).** The product must guarantee, **by its own means and on every install**, that its Mail cache directory is never captured by data-directory version-control staging or by the application's own backups/archives. No personal machine-setup repository, external deployment script or operator provisioning is a dependency, an input or a referenced owner anywhere in this design — an earlier draft of this spec cited an external machine-deployment repository and its ignore-file generator as the staging-exclusion owner; **that dependency is withdrawn in full** (the machine-setup repository is personal configuration, not product design, and appears nowhere in the corrected spec set). The `cache_unavailable` honest-state notice remains for genuine failure only — it may never be used to excuse a missing product-owned exclusion.

Two product halves carry the guarantee, both enforced before the first write on any install:

| # | Rule | Owner and location |
|---|---|---|
| E-1 | **Staging exclusion — verified by the product, never assumed.** Before the first write, the product evaluates the data directory's actual version-control ignore state for the cache path **itself, in-process** — read-and-evaluate semantics equivalent to `git check-ignore`, implemented in product code; **the product never shells out to `git` on this security-critical path** (Hard Constraint #2 — grill M-8's open mechanism is hereby decided). The evaluation has `git check-ignore`-equivalent semantics: a directory deny rule covers all nesting below it; a path that is already **tracked** is not excluded even where a deny rule matches (ignore rules do not apply to tracked files — E-3); where no version-control staging exists in the data directory there is nothing to be captured by and the staging half holds trivially. If the product cannot prove exclusion where staging exists, it does not write — the guarantee is enforced by the product's own check-and-refuse, never by trusting an external file's contents | Product code: the gate in W2's first-write path (§4.1 `FolderSnapshotStore.Save`); the gate decision is published by w5-integration (register row 18) |
| E-2 | **Backup exclusion — product code.** The application's own backup walker gains the cache-directory skip: `createTarGz`'s top-level skip becomes `logs` ∪ `backups` ∪ `mail-cache`; restore (`extractTarGz`) is then covered automatically for new archives (an old archive's restored cache file is §3.11's foreign-generation rejection, OQ-5). The application's own archives are product behaviour, so this half is fully in-product on every install | `pkg/gateway/rest_settings.go::createTarGz` — edited in w5-integration's wave (the gateway files' single writer; register row 18) |
| E-3 | **Pre-tracked paths block the gate.** A cache path ever committed to a data-directory repository is not untracked by a deny rule — the gate treats a tracked cache path as **not excluded** and refuses disk writes until it is untracked. Remediation of any such install is an operator runbook (untrack + commit; history rewrite remains a founder decision), never a product-code fix | Verified at first-write time by the E-1 evaluation |
| E-4 | **Removal deletes the cache.** The mailbox-removal cascade gains cache deletion (best-effort unlink, `logsafeError` + continue — the handler's existing orphan-tolerant pattern), so mailbox removal leaves no orphan cache files | `pkg/gateway/rest_mailbox.go::deleteAgentMailbox` — w5-integration's edit; W2 supplies the delete primitive |
| E-5 | **Watcher state files get the same treatment (founder Q-B=A, decided 2026-10-02).** The `email-watch/` state files are excluded from version-control staging and application backups under the same product-owned guarantee, **and are purged on mailbox removal** — closing their disclosure path completely. Routing: the purge rides the E-4 cascade (w5-integration owns it), the exclusion rides this §3.8 guarantee; the watcher file itself is w1's exclusive file. Not W2's code — recorded here because the founder decision closes this spec's former open question OQ-6 | Cascade: `deleteAgentMailbox` (w5-integration); exclusion: the §3.8 gate + backup-skip pair |

**The gate (hard): the first cache write happens only where both halves provably hold.** W2's first-write path runs the E-1 evaluation and requires the E-2 skip in the running build — **both must hold, checked by the product itself, failing closed**: either check failing leaves the disk cache **disabled on that install** — the mailbox runs live-only with the visible `cache_unavailable` notice (the ADR's wire metadata `notice_code`), and nothing is written to disk. Properties: the check is cheap and runs once per process per pair; it fails **visible** — never a silent "skip the check, write anyway"; the gate decision is published once by w5-integration and enforced by W2 at the write (register row 18 — one decision, one enforcer); and the disabled state is an honest product-enforced safety outcome, not a machine-setup excuse: the backup half is product code on every install, and the staging half is evaluated by product code everywhere staging exists.

---

### 3.9 The bounded in-memory header cache

Phase 1's only message-adjacent cache is **memory only**: the newest fifty headers per folder role per mailbox, so an open panel redraws the list instantly instead of re-fetching envelopes. Everything about it is bounded; nothing about it reaches disk (the Phase 2 encrypted header cache is explicitly not this package — §8).

**Content.** Exactly the envelope fields and flags the list rows already carry — the field set of `pkg/email/view.go::MailRow` (read this checkout): UID, UIDValidity, Seen, IsDraft, ReadByAgent, IsOmnipusDraft, MessageID (when present), From, FromName, To, Cc, Subject, Date. **No body text, no snippet, no MIME part, no attachment bytes, no attachment list** (the `has_attachments` indicator W0 adds to the wire is inside the allowed flag/metadata set once F7 lands; the bytes are not). There is no field in `MailRow` that could smuggle a body — verified — and the cache struct must be defined so none can be added by accident (a new field requires touching this spec's rule).

**Bounds and freshness (each number is founder-set in the ADR's P1.1):**

| Property | Rule |
|---|---|
| Cardinality | At most **50 newest headers per role per mailbox** (150 per mailbox). When a fresh snapshot has more, the *cache* keeps the newest 50 and nothing else; the live page is never silently shortened to fit (the ADR's P2.2 cardinality rule, applied to memory). |
| Paging | Display pagination stays **25 rows per page, hard ceiling 200 per folder per view**. The cache serves only **newest unfiltered pages the snapshot actually covers** — pages past the cached 50, and every search, run live. **Loaded older pages are the active view's working set, released on view exit, never merged into the reusable cache** (the ADR's "active-view results must not enlarge the reusable cache"). |
| Freshness | On folder open: display cached rows immediately (labelled); if absent, fetch once; if **older than 5 minutes**, run **one** background refresh. The stale label and `stale`/`refresh_needed` metadata ride W0's proposed `MailReadMetadata`; W2 supplies the timestamps. A failed refresh preserves labelled stale rows plus a visible error/Retry — it never resets the timestamp and never calls stale rows live (ADR P1.3 "stale cache" row). |
| Retention | Memory entries are dropped **30 minutes after the panel was last open**; panel close starts that clock; reopening resets it. No refresh of any kind while the panel is closed; the retention sweep issues zero IMAP commands. |
| Budget | The **4 MiB global reusable-metadata budget** (founder Q3=A, both phases) covers this cache plus the folder metadata held in memory. Overflow is a **visible live-only / cache-unavailable outcome** — never truncated fields, never silently omitted live rows, never evicting the current user's working response (ADR P2.2 memory row's eviction order: reusable headers first, live work last). |
| Isolation | Cache entries are keyed by the full pair + configuration/folder-mapping generation + role — never by account alone. Two pairs on one account, or one pair across a reconfiguration, never see each other's rows (the cross-pair risk row; grill I-01's cache face). |
| Purpose | Cached rows are reused **only** for newest-unfiltered page display. Search results and older pages never read from it, never write to it. |

**Folder counts — the P1.1 counts rows, normative (grill I-6 correction; register row 21; founder Q-C=A).** This package owns the counts rows of P1.1; the rule below is the missing normative text and its tests are §7's. Panel folder counts are **memory only** — never persisted, never fabricated (the §3.4 unknown-vs-absent null rule applies to every count). They carry their own freshness timestamp, **separate** from the headers' and the folder mapping's (the ADR's freshness-metadata row: a mapping, the counts and the page headers each have their own metadata).

- **R-3.9-C1. Refresh is stale-gated, never unconditional.** Counts refresh on a trigger only when they are **absent or older than 5 minutes**; fresh counts are served as-is with no live dial. This is the design's stale-gating, restored as the founder-directed behaviour (founder Q-C=A: "counts and lists should not be frequent") — the register aligns every contradicting scenario elsewhere in the spec set to this conditional rule (register row 21).
- **R-3.9-C2. Exactly four triggers, closed list** (ADR P1.1 counts row): panel open; a folder switch **alongside its list**; this client's own successful action (mark-read/send/draft); manual **Refresh** (always refreshes, stale-gate waived for the explicit human action — same rule as the headers' manual refresh).
- **R-3.9-C3. No repeating timer** — no ticker, no periodic job; counts advance only on the four triggers (the founder's no-timer rule; the I-02 test row's "no new scheduled refresh timer" assertion covers counts too).
- **R-3.9-C4.** Counts obey every §3.11 invalidation row (external move/delete, own mutation, UIDVALIDITY change) and the §3.10 publication-revision gate: a superseded read publishes no count update, and a failed refresh preserves the previous counts with the visible error/Retry — it never resets the counts' timestamp and never renders a failed refresh as checked-empty.

### 3.10 The folder publication revision (grill correction I-02)

Every mailbox/folder carries a local, monotonically advancing **publication revision**, owned by the gateway/cache service (per register row 10: w5-integration owns the counter and its advancement events; W1 publishes the `RevisionSource` capture/compare accessor; W2 owns enforcement at every cache write, taking the captured value). The rules, verbatim in obligation form:

- **R-3.10-1.** A read **captures** the revision current when it starts — before any server work.
- **R-3.10-2.** Successful mutations (this client's mark-read/send/draft) and every invalidation event in §3.11 **advance** the revision.
- **R-3.10-3.** A read whose captured revision is no longer current when it finishes **publishes nothing**: no rows into the memory cache, no disk snapshot write, no `last_validated_at`/`saved_at` advance, no count update, no frontend state. Its data is discarded; the already-scheduled post-mutation refresh supplies truth.
- **R-3.10-4.** A post-mutation refresh never joins a flight started under an older revision — the revision participates in the read-coalescing identity (I-01; W1's budget rewrite).
- **R-3.10-5.** No new polling timer exists: the revision advances only on the listed events. (The I-02 test row: "Assert no new scheduled refresh timer was introduced.")

The proposed wire exposes an opaque `publication_revision` on the response metadata so the SPA can drop an out-of-order response — W0's contract field; W2's obligation is that the value it returns is the revision the data was produced under, and that superseded values never reach the cache layers.

### 3.11 Invalidation table — what each event must discard

The ADR's P1.3 rows and P2.3 table, restricted to what exists in Phase 1 (memory headers/counts + encrypted folder metadata). "Both layers" = the memory cache and the encrypted folder file.

| Event | Required invalidation |
|---|---|
| **UIDVALIDITY change** on a folder (folder recreated, server-side rebuild) | Discard **every** cached header, page cursor and count for that folder's old epoch before any new row is published; refresh the saved folder version **once** (trigger 4, §3.6). Never reuse an old UID against a new epoch; a UIDVALIDITY that has never been validated is stored as *unknown* (nullable), never a fabricated `0`. |
| **Message moved or deleted by another client** | On the next successful refresh of membership/flags, the moved/deleted row leaves the cache and affected counts invalidate. A cached row the live check cannot find is shown as "changed or deleted" (W3's rendering of the typed 409/stale-ref result the ADR-W0 contracts role defines) — never silently kept as fresh, and never mutated against (that deeper guarantee is the register row-16 split: W1 publishes the same-lease validation capability, W2 validates in `view.go` per §3.16; W2's cache duty is not to *serve* the stale row as current). |
| **This client's own mark-read / send / draft action** | On confirmed success: patch known flags where safe, invalidate affected counts and list pages, advance the revision (R-3.10-2), and run **one** refresh while the panel is open. A failed or ambiguous action changes nothing and is never automatically repeated. |
| **Pair disabled or removed; agent/workspace deleted; mailbox moved** | Advance/bump the pair's generation **first**, revoke ownership, cancel pair-owned work, delete both memory and disk caches, and prevent late completions from recreating files (tombstone/generation guard — w5-integration owns the cascade wiring; W2 supplies the delete primitive and honours the generation check on every write). On restart, orphan files are reconciled against live configured pairs. |
| **Credential, host, port or override change** | Bump the configuration generation; **delete the disposable caches rather than migrating encrypted data under an uncertain key** (ADR P2.3 row). The old generation's file is unopenable anyway (AAD binding, R-3.7-4) — deletion makes that visible instead of leaving a decoy. |
| **Corrupt ciphertext, schema mismatch, or expired snapshot** | Reject without plaintext salvage (R-3.7-7); surface a cache warning; use the budgeted live path. Never render unauthenticated bytes as folder state; never advance "last checked" on failure. |
| **Expired retention** | Memory headers: dropped by the 30-minute post-close rule. A row's age is *displayed*, never hidden: **stale rows are shown labelled with their age, never as fresh** — "instant display" is not "fresh server data" (the ADR's staleness paragraph). |
| **Key lock / rotation** | New cache operations require valid derived keys (R-3.7-8): locked ⇒ live-only + cache-unavailable; rotation/lock clears in-memory keys and cache data and removes disposable files; rebuild only after normal credential resolution. |

Two standing rules bind the whole table: every event advances the publication revision (R-3.10-2), and none of them schedules background work while the panel is closed — the watcher may update its own metadata and mark panel caches dirty, nothing more.

### 3.12 Logging rules

New diagnostics in this package are **operationally useful and content-free**:

- **May appear:** opaque pair identifiers, opaque generation identifiers, safe closed-class error codes (the closed class enum implemented at `pkg/email/watcher.go::classifyMailError` — grill M-3 correction: this spec's earlier "§2.3 class enum family" citation pointed at this spec's own §2.3, which is Cluster placement; the enum lives in code, not in a spec section — `timeout | dns | connect_refused | auth_failed | tls | folder_missing | server_error`, plus the new safe discovery classes this package introduces — e.g. `discovery_unresolved`, `mapping_ambiguous`, `cache_unavailable`, `cache_corrupt`), durations, operation labels, and hit/miss/socket counts (§3.17).
- **Must never appear:** folder names, subjects, addresses, Message-IDs, credential values, raw upstream error text, full server responses or protocol transcripts. A raw upstream error is reduced to its safe class **at the boundary where it is caught**. **Ownership of the two known raw-text seams (grill C-1; register row 19 — this spec's earlier "W1/W4" attribution is withdrawn as wrong):** `pkg/email/watcher.go::recordFailure` is fixed by **w1**, inside its own exclusive file; the error argument in `pkg/gateway/rest_mail.go::mailErr502` is fixed by **w5-integration**, in its own file. W2 owns neither file, adds no fix for either, and its own new code simply never adds a third raw-text seam (CX-15's sweep proves that negative).
- Every log call in this package goes through the existing logging helpers with typed fields; no `fmt.Errorf`-built string carrying server data is ever logged or returned as user-visible text.

### 3.13 Folder-scoped server search (correction round; register row 4; founder Q-D=A)

Beyond the 200-row view ceiling the user must be able to reach older messages by **search** (the ADR's paging/search row: "a dead-end 'search instead' instruction is not completion"). The register splits the obligation: **the ADR-W0 contracts role** schemas the wire shapes (`search` param, `next_cursor`, `has_more`, `view_limit_reached`, the typed 409 stale-cursor result), requested through w5-integration's queue (register row 4, Wave B); **this package implements the search itself — server-side IMAP SEARCH plus cursor issuance — in `view.go`** (register row 4, one added story, Wave C, before any w3 paging code consumes it). No local index is built; the header cache is never consulted for search (§3.9 Purpose).

Normative rules:

- **R-3.13-1. Matching fields, decided (founder Q-D=A): SUBJECT + FROM/TO substring matching**, server-side, via IMAP header SEARCH on the leased session. No body text, no full-text, no date-range or flag predicates in Phase 1 — the matching surface is exactly these three headers.
- **R-3.13-2. Bounds: 25 results per page, hard ceiling 200 per folder per search view** — the same pagination discipline as browsing (ADR P1.1 display-pagination row). At 200 there is no next cursor; the response says "search to find older messages" is exhausted via `view_limit_reached` (W0's shape). Search results are **never** merged into, served from, or counted against the header cache.
- **R-3.13-3. Cursors are issued by this package** and bind pair, configuration generation, folder, folder version (UIDVALIDITY), the query, and the number of results already delivered in the sequence; a stale or mismatched cursor is refused with the visible typed 409 stale-cursor result (W0's shape) that requests one view reset — never a silent reinterpretation, never an infinite reset loop.
- **R-3.13-4.** Search runs as one budgeted, coalesced operation under W1's pool/account gates (same discipline as R-3.2-3), inside the ordinary read-work deadline, and is a *read* (R-3.2-4's no-mutation rules apply verbatim).
- **R-3.13-5.** A search whose captured publication revision is no longer current when it finishes publishes nothing into any cache (§3.10) — search results never reach a cache layer in any case, so the revision gate here protects only the response, not cache state.

### 3.14 Targeted single-part reader (correction round; register row 14)

**New W2-owned file `pkg/email/attachment_parts.go` (proposed):** part-specific fetch for attachment preview and tool reads — one reader, published once; w4-features' Open/Save/agent-tool rows and w6 consume it and never re-implement it (register rows 14, R-4).

Normative rules:

- **R-3.14-1. Lease-bound PEEK only.** The reader fetches the addressed part on the caller's already-acquired leased session (W1's lease; `pkg/email/view.go`'s existing session discipline) using the BODY.PEEK[<part>] form — flags are never touched (the same peek discipline as R-3.2-4). It never dials privately, never opens a second session for the same operation.
- **R-3.14-2. Stable leaf-index resolution.** The part address is the existing leaf enumeration (`part_index` on the attachment descriptor), resolved deterministically against the message's MIME structure with the **Omnipus draft-body marker excluded** from the leaf numbering — the same exclusion the attachment descriptors already define (ADR F1/F7 attachment-metadata row). Two readers of the same message under the same epoch resolve identical indices; a ref/epoch mismatch is refused per §3.16 before any part fetch.
- **R-3.14-3. Transfer shape (founder decision, 2026-10-02 "Transfer" row): a partial or whole fetch of just that part**, transiently — text and markdown parts may be fetched in ranges; images and PDF need the whole part. Nothing is written to the server's disk, nothing is retained in this package between operations (no byte cache of any kind — the ADR's bodies-and-attachments rule), and any viewing-session keep of recently opened previews is the preview feature's obligation, never this reader's.
- **R-3.14-4.** The reader imposes no separate size cap and serves the caller's bounded request: the endpoint's 25 MB actual-decoded-byte cap and its failure ordering are the preview endpoint's obligations (ADR F1 preview row, w5-integration's server-enforced cap/purpose) — the reader's contract is faithful part delivery, streaming to the caller, never buffering the whole part in package state.
- **R-3.14-5.** Diagnostics obey §3.12 (no filenames, no subjects; safe classes and durations only).

### 3.15 The one MIME structure classifier and `has_attachments` (correction round; register row 15)

**Register rule R-4, made concrete: one MIME walker, no second.** The MIME structure walk that classifies a message's parts — producing the attachment descriptor list and the `has_attachments` indicator — is implemented once in this package (with §3.14's file), and every consumer (w4's `read_message` list, w3's paperclip rendering, w6's T36) consumes its result (register rows 14–15; the ADR's feature-extension row: "W2 alone owns row/detail MIME metadata").

Normative rules:

- **R-3.15-1. `has_attachments` is derived, never guessed** — from the walked structure's attachment-classified parts; **`false` is established absence** (the ADR's F7 row), not "unknown". The derivation is metadata-only: the walk fetches structure/marker metadata, never attachment bytes.
- **R-3.15-2. The draft-marker part is excluded** from both the classification and the leaf numbering (R-3.14-2) — an Omnipus draft's own body part never renders as "an attachment of the message".
- **R-3.15-3.** The indicator rides the allowed flag/metadata set of the header cache (§3.9 Content) once the W0 field lands; the **bytes and the descriptor list are never stored** in any cache (§3.9, §8).
- **R-3.15-4.** The classifier is deterministic for a given structure: the same message part structure under the same epoch yields the same classification — a test oracle (§7), not an implementation flourish.

### 3.16 `message_ref` same-lease validation (correction round; register row 16, the grill I-03 seam)

The register splits this seam three ways (row 16): **w5-integration mints** the gateway-issued, pair/folder/generation-bound `message_ref` carried on list/read results; **W1 publishes the same-lease validation capability** (its §3.1 lease row / §4.3 item 4 — the lease's returned epoch/generation evidence); **this package owns the normative validation rules in `view.go`** — its files. This spec previously disclaimed the whole seam to "W1/W4's reference work"; that disclaimer is withdrawn.

Normative rules:

- **R-3.16-1.** Every `view.go` operation that receives a message reference — detail read (`ReadView`), ref resolution (`ResolveRef`), seen-write (`MarkSeenIn`), part fetch (§3.14) — **validates the reference against the live folder epoch (UIDVALIDITY) and the configuration generation before any server command uses it**, using W1's same-lease validation capability. The embedded epoch is compared to the live folder epoch **on the same lease**, in the operation's own session — the comparison the code skips today (`pkg/email/view.go::ReadView`/`::ResolveRef` take a supplied UID without comparing its embedded epoch — verified this checkout).
- **R-3.16-2.** A reference that fails validation is refused with the visible typed stale-reference result (W0's typed 409 family) requesting one view reset — never silently resolved against the new epoch, never mutated against, never an unexplained error.
- **R-3.16-3.** W2 does **not** mint references and adds no second issuance path (w5-integration is the single minter, register row 16); W2's cache never serves a row whose epoch would fail this validation (§3.11 UIDVALIDITY row).
- **R-3.16-4.** The gateway's existing unvalidated path (`pkg/gateway/rest_mail_read.go::handleMailSeen` discards the resolve epoch — verified this checkout) is w5-integration's wiring to correct on top of this validation; W2's obligation is that the `view.go` seam can no longer resolve a stale ref silently.

### 3.17 Instrument emission — this package's sub-fields (correction round; register row 17)

The per-operation instrument record (`operation`, `source`, `hit`, `duration_ms`, `acquire_wait_ms`, `socket_count`, `outcome`) is **shape-frozen by w6** (the only normative definition, register row 17); the request-scoped envelope is w5-integration's; **this package emits the cache and discovery sub-fields** its operations own. Obligations, binding:

- **R-3.17-1.** Every discovery, cache read and cache publication in this package emits its sub-fields of the w6-frozen record: `source` (the §3.9/§4.1 metadata source), `hit` (cache hit or miss), its operation's `duration_ms`, and `outcome` (the safe class on failure). `acquire_wait_ms` and `socket_count` are supplied by w1's pool layer and w5-integration's envelope — W2 never invents values for fields another layer owns.
- **R-3.17-2. Joiner rule (register row 17, mechanical):** an operation that joined a coalesced flight records `socket_count=0` plus the shared-flight marker, so the measurement's socket sum stays comparable to the server's connection counter (w6 §6.1). W2's coalesced discovery/refresh operations apply this rule whenever they ride W1's coalescing identity.
- **R-3.17-3.** The emitter exists before Wave E — the register's gate ("the row 17 emitter must exist before Wave E, else T1–T6 strand"); W2's half lands inside its Wave-C code, never as an ad-hoc log line substituted for the frozen record (the false-green shape the register's Wave-E gate exists to prevent).

---

## 4. Interfaces this package publishes and consumes

These shapes are the parallel-build freeze: W1, w5-integration and W10 build against them without editing W2's files, and W2 builds against the consumed side without editing theirs. **One publisher per interface, forever (register rule R-1):** each row below names its single publisher and freeze point; a consumer binds to the frozen shape and never re-implements, parallel-types, or quietly extends it. Method names below are the proposed Go surface in W2's new files (implementation-phase naming; the *shapes and semantics* are the binding part). Go type parameters and error semantics follow the existing `mail_budget.go` house style.

### 4.1 PUBLISHED by W2 (other packages call these; frozen here, Wave C)

| Interface (proposed home) | Consumed by | Contract |
|---|---|---|
| **Discovery service** — `pkg/email/folder_discovery.go::Discovery.Resolve(ctx, Scope, Overrides) (RoleMapping, error)` | w5-integration (gateway handlers), W1 (pool validation), W10 later if a tool ever needs a resolved folder | `Scope` carries the opaque pair ID, the configuration generation, and the injected pooled-session handle; `Overrides` carries the raw stored `sent_folder_name`/`drafts_folder_name` values (possibly empty). Returns the three-role mapping with `present/absent/unknown` outcomes, sources, UIDVALIDITY (nullable), and ambiguity lists exactly as §3.2–3.5 define. Idempotent; coalesced through the shared budget; never dials outside the injected handle; emits its §3.17 instrument sub-fields. |
| **Role mapping type** — `folder_discovery.go::RoleMapping` (per-role `RoleResolution`: name, source, uidvalidity `*uint32`, availability, ambiguity list, validated-at) | the ADR-W0 contracts role (the values behind `MailFolder.availability` / `uidvalidity` / `mapping_source`), w5-integration (response mapping), W2-internal cache | The wire fields are the ADR-W0 role's (Wave B; register row 1 — no W0 spec file exists); this type is the single producer of their values. `mapping_source` values: `override`, `special_use`, `fallback`, `saved`, `none` — **five values**, the register row-3 settlement superseding the ADR's four-value proposal, to be adopted in Wave B. |
| **Encrypted folder snapshot store** — `pkg/email/cache_file.go::FolderSnapshotStore.Load(Scope) (Snapshot, Found, error)` and `.Save(Scope, Snapshot, capturedRevision, derivedKey) error` (plus `.Delete(Scope) error`) | w5-integration (boot-time orphan reconciliation, removal cascade E-4, generation bumps), W2-internal refresh triggers | Takes **supplied** derived keys (the ADR's W2 row: "encrypted folder envelopes with supplied derived keys") — the store never touches the credential store itself; the caller (w5-integration) resolves keys via `credentials.Store.DeriveSubkey` with the §3.7 purpose string. **Signature amended in this correction (grill I-3; register row 10):** `Save` takes the **captured revision value** — captured at read start through W1's `RevisionSource` accessor (register row 10) — so the revision check at write time compares against the value the read actually started under; the bare-value/no-source shape this spec froze before is withdrawn. `Load` performs the full R-3.7-6/7 rejection ladder and the AAD/generation comparison (R-3.7-4); `Save` performs the runtime exclusion gate (§3.8, E-1 evaluation — the gate decision itself is published by w5-integration, register row 18) and the revision check (§3.10) before writing. |
| **Header cache** — `pkg/email/header_cache.go::HeaderCache.Get(Scope, role) (Page, Metadata, bool)`, `.Put(Scope, role, capturedRevision, rows) error`, `.GetCounts(Scope) (Counts, Metadata, bool)`, `.PutCounts(Scope, capturedRevision, counts) error`, `.InvalidateFolder(Scope, role)`, `.MarkPairRemoved(Scope)`, `.PanelClosed(scope)`, `.PanelOpened(scope)` | w5-integration (gateway read handlers), W3 indirectly through response metadata | All §3.9 bounds enforced inside: 50/role, envelope+flags only, 4 MiB budget (overflow → typed `ErrCacheBudgetExceeded` → visible live-only), 30-minute retention, per-pair/generation keying. `Put`/`PutCounts` refuse to publish under a stale revision (R-3.10-3) — the revision parameter is the value captured via W1's `RevisionSource` at read start (register row 10). **`GetCounts`/`PutCounts` added in this correction (grill I-6; register row 21):** the counts rows of P1.1 live in this cache with their own freshness metadata, stale-gated per §3.9's counts block. |
| **Cache metadata** — `header_cache.go::Metadata` (source `live|memory|none`, `last_validated_at *time.Time`, `stale`, `refresh_needed`, `publication_revision`, `notice_code`) | the ADR-W0 contracts role (the generated `MailReadMetadata` fields' shaper), w5-integration, W3 | One producer for the freshness fields every Phase 1 read response carries; `encrypted_disk` appears in `source` only for the folder mapping (Phase 1), never for headers. Counts carry their own separate `Metadata` (the ADR's freshness-metadata row; grill I-6). |
| **Folder search + cursor issuance** — `pkg/email/view.go` (the `ReadFolderPage` family's search path; §3.13) | w5-integration (list/search handlers), W3 (panel search), w6 (measurement arms) | **Added in this correction (register row 4):** server-side IMAP SEARCH — SUBJECT + FROM/TO substring (founder Q-D=A) — with cursor issuance in `view.go`, inside the 25/200 bounds; the wire shapes (`search` param, `next_cursor`, `has_more`, `view_limit_reached`, typed 409) are the ADR-W0 role's at w5-integration's request (register row 4, Wave B); this package's SEARCH execution and cursor issuance land in Wave C, before any w3 paging code consumes them. Never touches the header cache. |
| **Targeted single-part reader** — `pkg/email/attachment_parts.go::PartReader.Fetch(ctx, lease, ref, partIndex, sink) error` | w4-features' rows (Open, Save, agent tools — **consume-only**, register rows 14, R-4), w6 | **Added in this correction (register row 14):** lease-bound part-specific PEEK (§3.14) with stable leaf-index resolution and draft-marker exclusion; transient, streaming, no byte cache; the endpoint's cap and failure ordering remain the preview endpoint's (w5-integration's). |
| **MIME structure classifier** — `pkg/email/attachment_parts.go::ClassifyStructure(structure) (descriptors, hasAttachments)` | w4-features (`read_message` list), W3 (paperclip), w6 (T36) | **Added in this correction (register row 15):** the one MIME walker (R-4: no second anywhere); derives `has_attachments` with `false` as established absence (R-3.15-1); feeds the W0 field on list/read results; the indicator rides the §3.9 allowed flag set, the bytes and descriptor list never enter any cache. |
| **Reference validation seam** — the `view.go` operations' same-lease `message_ref` validation (§3.16) | w5-integration's handlers (which correct the epoch-discard wiring on top of it), W3 indirectly | **Added in this correction (register row 16):** W2 validates every received reference against the live epoch/generation on the same lease before use (R-3.16-1/2); **w5-integration mints** the reference (the single issuer), **W1 publishes the same-lease validation capability** — W2 neither mints nor re-implements validation of another package's half. |

### 4.2 CONSUMED by W2 (other packages own; W2 edits none of them)

| Interface / seam | Owned by (publisher and freeze point) | What W2 needs from it |
|---|---|---|
| **Pooled session + budget acquisition** — pool/budget interfaces | **W1** — frozen in w1 §3.1 (register row 9); W2 may rewire `view.go` only against that freeze | One exclusive leased session per discovery/probe/validation operation, acquired through the rewritten coalescing identity (pair + generation + operation + normalized args + purpose, I-01); W2 never dials privately and never constructs a `Client` pool. The same freeze carries the **same-lease validation capability** (register row 16 — the lease's returned epoch/generation evidence W2's §3.16 checks consume) and the pool's **instrument sub-fields** (`acquire_wait_ms`, `socket_count` — register row 17). |
| **Pair scope + configuration generation** — opaque pair ID, current generation token, generation-bump notifications | **w5-integration implements both** (register row 12); **constructions settled there**: generation = non-secret fingerprint of the canonical pair identity **plus the persisted config epoch** (w1 OQ-1 option C — catches endpoint/credential/override changes *and* invisible re-saves); pair ID = **random minted 128-bit value stored beside the pair config** (this spec's former OQ-1 option (a)); w5's MC-17 derive-from-credential-material fingerprint is **rejected** | Stable per-pair identity for cache keying and AAD binding; a notification (or a checkable token) when the generation advances, so §3.11's credential/host/override/removal rows invalidate. **W2 treats both values as opaque** — the former open question OQ-2 (plain fingerprint) is superseded by the register's row-12 construction; this spec records the decision, it does not re-derive it. |
| **Derived keys** — `credentials.Store.DeriveSubkey` with the §3.7 purpose strings; store lock/rotation notifications | **w5-integration** (injection), the credential store's own boot contract | 32 bytes per purpose at operation time; `ErrStoreLocked` handling per R-3.7-8. W2 holds keys only for the duration of an operation and wipes best-effort. |
| **Folder publication revision** — capture accessor, counter and advancement events | **Split per register row 10 (grill I-3's resolution): W1 publishes `RevisionSource`** (the capture/compare accessor — the transport from capture point to comparison point); **w5-integration owns the counter and the advancing events**; W2's `Put`/`Save` take the captured value | The captured revision value at read start (via W1's accessor) and the comparison at publish time; W2 never advances the counter and never re-implements capture. The two incompatible freezes this spec and w1 previously carried are merged here. |
| **Generated wire types** — `MailFolder`, `MailFolderList`, `MailMessagePage`, `MailReadMetadata`, the paging/search/stale-cursor shapes, the REST `observer_id` param, the `message_ref` field | **The ADR-W0 contracts role — backend-lead, Wave B** (register row 1: a role, not a spec file; none exists and none is required). The role's requirement queue is the union of the six specs' queues, with w5-integration's §8 carrying the row-4 shapes and the row-6 `observer_id` requests | The only cross-boundary types (Hard Constraint #8); W2 populates them, never hand-writes parallels (guards: `scripts/check-no-handwritten-wire-types.sh`, `make verify-contracts`). Until Wave B's artifacts merge, W2 builds below the wire boundary against the spec-frozen internal interfaces and reports any wire-dependent work as blocked (register §5.3) — never stubs. |
| **Watcher dirty-mark signal** — "folder version changed while closed" | **W1 publishes it** (register row 13 — the freeze row lands in w1's §3.1 correction; the obligation already exists in w1's tests/constraints), delivered through w5-integration's integration | The dirty-mark signal only (§3.6: watcher marks dirty, never refreshes, never fetches headers). |
| **Presence / refresh-trigger events** — panel open, folder switch, own successful action, manual Refresh; the panel's `observer_id` association | **w5-integration's handlers** (the REST `observer_id` param is row 6's W0-schemed, w5-requested shape; presence frames are register row 5) | The four §3.9 counts triggers and the §3.6 refresh triggers arrive as events from the handler wiring; W2 owns the stale-gated *decision* (refresh only when absent or >5 min old; manual Refresh always). W2 never parses the REST `observer_id` itself — `PanelOpened`/`PanelClosed` receive the association from w5-integration's handlers. |
| **`message_ref` issuance** | **w5-integration mints** in the gateway handlers (register row 16 — "gateway-issued") | The reference on list/read results; W2 validates (§3.16) and never mints or re-issues. |
| **Instrument record** — the per-operation record shape | **w6 freezes the shape** (the only normative definition, register row 17); **w5-integration owns the request-scoped envelope**; **w1 supplies the pool sub-fields** | W2 emits its cache/discovery sub-fields per §3.17 (source, hit, duration, outcome; joiner rule: `socket_count=0` + shared-flight marker); the emitter exists before Wave E. |
| **`recordFailure` / `mailErr502` raw-error redaction** | **w1 fixes `recordFailure` inside its own exclusive file; w5-integration fixes `mailErr502`** (register row 19; grill C-1) | W2 owns neither file and adds no fix — consumed purely as a boundary this package must not worsen (§3.12). |
| **Existing test harness** — `pkg/email/imapserver_test.go::startMemIMAP`, `view_test.go::startViewIMAP`/`::startViewIMAPRaw` | qa-lead's files (w6-proof) | The real in-memory IMAP server and the capability-set seam for LIST-EXTENDED tests. |

**Non-editing guarantees (the ADR's ownership rows + register rule R-1, restated as this spec's boundary):** W2 owns every `pkg/email/view.go` change in this feature so pool and metadata writers never both patch it; W2 never edits `pkg/email/transport.go` (W1), any `contracts/` file (the ADR-W0 contracts role), any `pkg/gateway/` file (w5-integration), or any test file (qa-lead/w6-proof). If a W2 obligation seems to require editing one of those, that is a rule-15 stop-and-ask, not an edit.

---

## 5. User stories and acceptance criteria

Priorities: P0 = the package's reason to exist; P1 = required for correctness/trust but not the headline. Every story names its independent test; BDD scenarios in §6 trace to these numbers.

### US-1 — Roles resolve by discovery, not by assumed names (P0)

A user connects a mailbox whose server names its folders differently — `Sent Items`, `[Gmail]/Sent Mail`, or a special-use-advertising server — and the Mail panel's Sent and Drafts tabs must work: the right folder is found and its real messages are listed, instead of today's 502 `folder_missing` (the supplied 13-mailbox evidence: all 13 Sent lists failed). The three tabs themselves never change.

**Independent test:** against the fake IMAP server configured with a `[Gmail]/Sent Mail` folder and SPECIAL-USE attributes, the sent role resolves to that folder; with attributes removed, the candidate fallback finds it; the panel's sent list returns its messages.

1. **Given** a server that advertises SPECIAL-USE and marks one folder `\Sent`, **when** the sent role resolves, **then** the resolved name is that folder, the mapping source is `special_use`, and the folder's messages list successfully.
2. **Given** a server with no SPECIAL-USE support whose folder is named `Sent Items`, **when** the sent role resolves, **then** the fallback probe finds `Sent Items` with source `fallback`.
3. **Given** a server whose Sent folder is named outside the candidate list (e.g. `Archivo-Enviados`) and that advertises no special-use role, **when** the sent role resolves, **then** the outcome is `unknown` (never `absent`, never an error page, never a false empty folder claiming absence), and the override setting is offered.
4. **Given** any resolution outcome, **when** any folder-addressed request runs, **then** only the slugs `inbox|sent|drafts` are accepted, and no server folder is ever created (zero `CREATE` commands observed).

### US-2 — The operator's explicit setting outranks discovery, and clearing it restores automatic (P0)

An operator who types a Sent folder name in the mailbox settings must get exactly that folder — even if the server advertises a different special-use folder — and must be able to return to automatic discovery by clearing the field, using the settings form that already exists.

**Independent test:** configure `sent_folder_name=Custom`, resolve (override wins over a `\Sent`-tagged folder); save with an empty value (field cleared → automatic resumes → special-use folder resolves); save with the field omitted entirely (stored value unchanged).

1. **Given** a stored non-empty `sent_folder_name` that probes successfully, **when** the sent role resolves, **then** the override folder is used with source `override`, regardless of any special-use or fallback candidate.
2. **Given** a stored override whose folder does not exist on the server (`[NONEXISTENT]`), **when** the sent role resolves, **then** the role reports the override as an actionable settings warning — the folder is not silently swapped for another candidate, and the warning names the setting (without leaking the name into logs).
3. **Given** the settings form saved with an explicitly empty `sent_folder_name`, **when** the sent role next resolves, **then** automatic discovery runs (no stored value remains — verified semantics of `persistConfig`), and the special-use/fallback folder is used.
4. **Given** a stored non-empty value of unknown provenance (any value an operator or an older flow may have saved), **when** the role resolves and the settings render, **then** the value is presented as an explicit override with an Automatic option — never silently discarded or reclassified by inspecting the string.

### US-3 — Unknown is never reported as absent, and missing INBOX is an error (P0 — the M-01 correction)

When Omnipus cannot prove where Sent lives, the user must be told *that*, honestly — not "the server has no such folder", which sends them hunting for a server setting that may not be the problem. And a mailbox whose INBOX cannot be selected is a broken account, never a healthy empty one.

**Independent test:** fake server with LIST succeeding, no special-use role, no candidate folder matching, but an unrelated folder present: the sent role reports `unknown` with the setting offered; a server without INBOX fails the mailbox read with the transport class.

1. **Given** successful LIST and zero successful candidate probes, **when** the role resolves, **then** `availability=unknown` with a `discovery_unresolved`-class reason — the UI shows the unresolved state and the override prompt, never "no such folder".
2. **Given** a network, timeout, auth, TLS or permission failure at any point in discovery or probing, **when** the role resolves, **then** the outcome is `unknown` (or the read's own failure for a whole-operation failure), never `absent` and never invented zero counts.
3. **Given** successful LIST **and** every applicable candidate probe returning the structural `[NONEXISTENT]` response, **when** the role resolves, **then** — and only then — `availability=absent` with an empty message list and the server-folder-absent explanation.
4. **Given** an INBOX whose SELECT fails for any reason including `[NONEXISTENT]`, **when** the mailbox is opened, **then** the read fails loudly with the safe transport class and no per-folder empty rendering is produced.

### US-4 — Ambiguous folders ask, never guess (P0)

A server that offers more than one plausible Sent folder (or whose saved mapping now disagrees with what discovery finds) must not have one picked for the user — mail could land in the wrong place.

**Independent test:** fake server with two `\Sent`-tagged folders and no saved mapping: the role resolves ambiguous, compose-to-sent fails visibly with the settings remedy; after the operator sets an override, sending targets the override.

1. **Given** multiple special-use `\Sent` folders and no still-valid saved mapping, **when** the role resolves, **then** the outcome is the ambiguity variant of `unknown` (reason class `mapping_ambiguous`) and no folder is auto-selected.
2. **Given** a still-valid saved mapping for the role, **when** a fresh discovery also finds candidates, **then** the saved mapping stands and no ambiguity surfaces.
3. **Given** an unresolved or ambiguous sent role, **when** a send or draft-APPEND needs the Sent/Drafts folder, **then** the operation fails with a visible actionable settings error — it never writes into an arbitrarily chosen folder.

### US-5 — Folder metadata survives restart, encrypted, and refreshes only on its four triggers (P0)

Reopening a mailbox after a gateway restart should not re-run full discovery, and the resolved folder names — which can reveal projects and people — must never sit on disk in plaintext. The saved list refreshes only on: first open; panel open with a list older than 24 hours; manual Refresh; or the single post-failure/version-change rediscovery.

**Independent test:** resolve a mapping (file appears, 0600, ciphertext-only); restart the process, reopen (no discovery commands on the wire — saved mapping used); age the file past 24h and reopen (one discovery); close the panel and let the watcher detect a version change (file marked dirty, zero IMAP commands until the next panel event).

1. **Given** a first open of a mailbox with no saved metadata, **when** the panel loads, **then** one discovery runs and the encrypted file appears with the resolved roles, UIDVALIDITY, schema/generation and validation time.
2. **Given** a valid saved mapping younger than 24 hours, **when** the panel reopens (including after a process restart), **then** no discovery command is issued; the mapping is validated lazily per §3.2 step 2 and used.
3. **Given** a saved list older than 24 hours, **when** the panel opens, **then** exactly one discovery refreshes it.
4. **Given** the panel closed, **when** any amount of time passes (including watcher version changes and retention housekeeping), **then** zero IMAP commands run and no cache file is written; the dirty flag alone is set.
5. **Given** the panel closed, **when** it reopens, **then** the deferred refresh (if any was marked) runs once as part of the eligible event.

### US-6 — The cache file is sealed: authenticated, bound, atomic, reject-before-allocate (P0)

The folder file must resist tampering, swapping, cross-pair copying and stale-generation replay, and must never yield unauthenticated plaintext — even to a crash.

**Independent test:** write a snapshot; flip one ciphertext bit (read refuses); copy another pair's file over it (read refuses via AAD); rewrite with a bumped generation (old file refuses); write twice with identical payload (ciphertexts differ); kill mid-write (old or new complete file readable, never a torn one).

1. **Given** a stored envelope with one flipped bit anywhere, **when** it is loaded, **then** authentication fails, nothing decrypts, a cache-corrupt warning surfaces, and the live path proceeds.
2. **Given** pair B's file copied onto pair A's path (or any AAD-bound field altered), **when** pair A loads it, **then** the read refuses — the reader compared the authenticated identity against its independently resolved pair/generation.
3. **Given** two consecutive writes of byte-identical payloads, **when** both ciphertexts are examined, **then** they differ (fresh random nonce per write).
4. **Given** a malformed, truncated or oversized envelope (beyond the small multiple of the 64 KiB budget), **when** it is offered for load, **then** it is rejected before any allocation proportional to its claimed contents.
5. **Given** a locked credential store, **when** any cache operation runs, **then** the package returns cache-unavailable and the mailbox runs live-only — no key is minted, no plaintext is written as fallback.
6. **Given** a write interrupted by process death, **when** the file is next read, **then** exactly one complete valid snapshot (old or new) is readable — never a torn or unauthenticated one.

### US-7 — Nothing reaches Git or backups: the exclusion gate (P0)

The product itself must make the first cache write impossible unless its own exclusion guarantee holds: the cache directory must be provably excluded from data-directory version-control staging **and** from the application's own backups on every install — the folder names are sensitive, and the guarantee is the product's, by its own means (founder Q-A; §3.8).

**Independent test:** on a build with the gate: with both product-owned exclusions provable (the in-process staging-exclusion evaluation passing and the backup-skip landed), the first write succeeds; with either unprovable, the write refuses and the mailbox runs live-only with a visible `cache_unavailable` notice; the Wave E end-to-end proof shows staging and a backup archive contain no cache file (positive control: an ordinary allowed file IS captured by the same instrument).

1. **Given** the product's own checks prove both exclusions (E-1's in-process evaluation passes; E-2's skip in the running build), **when** the first cache write runs, **then** it succeeds — the gate's own evaluation is the proof instrument, not an external tool.
2. **Given** either exclusion unprovable in the running build, **when** the first cache write runs, **then** the write is refused, zero cache files exist, and the mailbox works live-only with the visible notice.
3. **Given** cache files on disk where both exclusions hold, **when** the Wave E end-to-end proof stages the data directory and runs an application backup, **then** no cache file (plaintext marker **or** ciphertext marker) appears in the staged set, any commit, or any archive member — while a positive-control allowed file does.

### US-8 — Instant, honestly-labelled folder lists from the memory header cache (P0)

Opening a folder shows its newest messages immediately from memory when they were recently fetched, each view honestly labelled with its age, with exactly one live refresh when the data is stale — and never a background refresh while the panel is closed.

**Independent test:** open a folder (live fetch, cached); switch away and back within 5 minutes (instant display from cache, source=memory, zero new FETCH commands); age past 5 minutes (instant stale display + exactly one background live refresh); close the panel for an hour (cache dropped; zero commands while closed).

1. **Given** a folder whose newest-50 headers were fetched less than 5 minutes ago, **when** the folder is opened, **then** rows render immediately with source `memory` and the recorded validation time, and no FETCH command is issued.
2. **Given** cached rows older than 5 minutes, **when** the folder is opened, **then** the stale rows render immediately (labelled), and **exactly one** live refresh runs for that event — a failed refresh keeps the labelled stale rows plus a visible error/Retry and does not reset the timestamp.
3. **Given** no cached rows, **when** the folder is opened, **then** one live fetch populates page one and the cache.
4. **Given** the panel closed, **when** 30 minutes pass, **then** the folder's cached headers are dropped; no refresh, fetch or any IMAP command occurred while closed.
5. **Given** a fresh snapshot with more than 50 messages, **when** the cache is examined, **then** it holds exactly the newest 50 and the live list is not shortened to match.

### US-9 — Invalidation keeps the cache honest (P0)

Every §3.11 event must actually discard what it says — a stale cache that lies about freshness or identity is worse than no cache.

**Independent test:** drive each event against the fake server (UIDVALIDITY reset; external delete; own mark-read; override change; pair removal with an in-flight write; corrupt file; retention expiry) and assert the §3.11 discards.

1. **Given** a folder's UIDVALIDITY changed, **when** anything next reads it, **then** every old-epoch header, cursor and count is gone, the saved version refreshes once, and no old UID is ever used against the new epoch.
2. **Given** the user marked a message read (or sent/drafted), **when** a read that started before that action finishes afterward, **then** it publishes nothing — not into memory, not to disk, not as a timestamp — and the post-mutation refresh's data stands (the I-02 ordering rule).
3. **Given** the pair is removed while a snapshot write is in flight, **when** the write completes, **then** no file is (re)created — the generation guard refuses it.
4. **Given** the operator changes the host, port, credential or folder override, **when** the pair is next used, **then** the old generation's caches are deleted (not migrated) and rediscovery runs under the new generation.
5. **Given** stale rows within their retention window, **when** they are rendered, **then** their age is visible (labelled) — they are never presented as fresh.

### US-10 — Logs say what happened, never what the mail says (P1)

Diagnostics must let an operator debug discovery and cache behaviour without ever recording a folder name, subject, address, Message-ID or raw server text.

**Independent test:** run the full §6 suite with a leak-marker instrument (synthetic folder names/subjects containing distinctive markers); grep every log sink for the markers — zero hits — while the safe classes and counts ARE present.

1. **Given** any operation in this package (success, unknown, ambiguous, failure), **when** its diagnostics are written, **then** they carry only opaque pair/generation IDs, safe classes, durations and counts.
2. **Given** a server error containing folder names or message data, **when** it is reduced at the boundary, **then** only the safe class survives into logs and state.

### US-11 — Older messages are reachable through folder search (P1 — correction round; register row 4; founder Q-D=A)

A user browsing past the 200-row ceiling must be able to *reach* older messages, not just be told to search: the panel's search runs server-side inside the 25/200 bounds, matching subject and sender/recipient substrings, and never consults the header cache.

**Independent test:** fake IMAP server with 300 messages; a subject-substring search returns 25-row pages under a cursor up to the 200 ceiling (`view_limit_reached` beyond); a sender-substring search finds messages the subject search misses; the header cache's stored rows are neither served from nor written to by any search.

1. **Given** messages whose subjects contain the query, **when** a folder search runs, **then** results come from a server-side IMAP SEARCH (SUBJECT header match), 25 per page under an issued cursor.
2. **Given** a message whose sender (or recipient) matches while its subject does not, **when** a folder search runs on FROM/TO, **then** the message is found — sender/recipient substring matching is part of the decided surface.
3. **Given** a search sequence that has delivered 200 rows, **when** the next page is requested, **then** `view_limit_reached` is reported with no next cursor and no auto-continuation.
4. **Given** a cursor from another query, folder version or generation, **when** it is presented, **then** the typed 409 stale-cursor result requests one visible view reset — and zero search rows ever enter or leave the header cache.

### US-12 — Attachments are read part-wise and flagged truthfully (P1 — correction round; register rows 14–15)

Opening or saving an attachment must fetch only that attachment's part — transiently, on the leased session — and the message list's paperclip indicator must be derived from the message's actual structure, with `false` meaning *checked, no attachments*.

**Independent test:** a message with two attachments and an Omnipus draft marker: the part reader fetches exactly the addressed part (partial fetch for text; whole part for an image) with zero flag changes; the classifier yields two descriptors with the draft part excluded and `has_attachments=true`; a message with no attachment parts yields `has_attachments=false`; the same structure classifies identically twice.

1. **Given** an addressed `part_index` on a leased session, **when** the part reader fetches, **then** only that part's bytes are transferred (BODY.PEEK part fetch — text/markdown may be partial; image/PDF whole), flags are untouched, and nothing is written to the server's disk or kept by this package.
2. **Given** the draft-body marker part, **when** the leaf indices are resolved, **then** the marker is excluded from numbering and from classification, and the same message resolves identical indices on every read under the same epoch.
3. **Given** a message's MIME structure, **when** the classifier runs, **then** `has_attachments` reflects the attachment-classified parts with `false` as established absence — and the same structure always yields the same classification.

### US-13 — A stale message reference is refused, never silently resolved (P0 — correction round; register row 16)

The current `view.go` takes a supplied UID without comparing its embedded epoch — the reference-validation seam. With gateway-issued references, a ref minted against another folder epoch or configuration generation must fail visibly.

**Independent test:** mint a reference against epoch N; change the folder's UIDVALIDITY (or bump the generation); present the reference to read/seen/part-fetch — each is refused with the typed stale-reference result requesting one view reset; zero server commands act on the stale ref.

1. **Given** a reference whose embedded epoch differs from the live folder epoch, **when** a detail read, seen-write or part fetch receives it, **then** validation on the same lease refuses it with the visible typed result — the server is never asked to act on the stale reference.
2. **Given** a reference minted under another configuration generation, **when** any `view.go` operation receives it, **then** it is refused the same way; and this package never mints references (issuance is w5-integration's, register row 16).

### US-14 — Counts are memory-only and refresh only when stale (P1 — correction round; register row 21; founder Q-C=A)

The rail's counts must be instant (memory), honest (unknown ≠ zero), and infrequent: refreshed on a trigger only when absent or older than five minutes, with manual Refresh always current — and no timer anywhere.

**Independent test:** counts fetched 4:59 ago are served again on a trigger with zero IMAP commands; at 5:01 one refresh runs; manual Refresh refreshes despite freshness; a failed refresh keeps prior counts with a visible error and no timestamp reset; no repeating timer exists.

1. **Given** counts younger than 5 minutes, **when** a trigger event fires (panel open, folder switch, own action), **then** the stored counts are served with zero live dials.
2. **Given** counts absent or older than 5 minutes, **when** a trigger event fires, **then** exactly one live refresh runs; **manual Refresh always refreshes** regardless of age; and no repeating timer exists on any path.
3. **Given** a failed counts refresh, **when** the next render happens, **then** the previous counts stand with the visible error/Retry, the timestamp is unchanged, and a superseded refresh publishes nothing (§3.10).

### US-15 — This package's instrument sub-fields are emitted per the frozen record (P2 — correction round; register row 17)

The measurement campaign (w6) can only judge what the emitters record: this package emits its cache/discovery sub-fields of the w6-frozen record, with coalesced joiners recording `socket_count=0` plus the shared-flight marker.

**Independent test:** drive a cache hit, a cache miss and a coalesced (joined) discovery: each emits the w6-shaped record fields W2 owns (`source`, `hit`, `duration_ms`, `outcome`); the joined discovery records `socket_count=0` plus the shared-flight marker; no field owned by another layer is invented.

1. **Given** a cache read or discovery operation, **when** it completes, **then** its instrument record carries W2's sub-fields per §3.17 and no sub-fields owned by w1/w5-integration are fabricated.
2. **Given** an operation that joined a coalesced flight, **when** its record is written, **then** `socket_count=0` plus the shared-flight marker are recorded (the joiner rule), keeping the socket sum comparable to the server's connection counter.

---

## 6. BDD scenarios

Format: Given/When/Then, one action per When; each scenario is typed **Happy / Alternate / Error / Edge** and carries its `Traces to` line (US-n, acceptance-scenario number). Feature: **Mail folder discovery and bounded cache**.

### 6.1 Discovery (US-1)

- **Scenario D-1 — Special-use resolution.** **Happy.**
  **Given** a server advertising `LIST-EXTENDED` + `SPECIAL-USE` with one folder tagged `\Sent`,
  **when** the sent role resolves,
  **then** the resolved name is the tagged folder, source is `special_use`, its UIDVALIDITY is recorded, and no candidate fallback probe was needed.
  Traces to: US-1.1.
- **Scenario D-2 — Fallback probe without extensions.** **Alternate.**
  **Given** a server without SPECIAL-USE whose folder is named `Sent Items`,
  **when** the sent role resolves,
  **then** the probe of the ordered candidate list succeeds at `Sent Items`, source is `fallback`, and the later candidates were not probed after the first success.
  Traces to: US-1.2.
- **Scenario D-3 — Unsupported extension is not a mailbox failure.** **Edge.**
  **Given** a server that advertises `LIST-EXTENDED` but no `SPECIAL-USE`, or one whose capability detection fails mid-way,
  **when** the mailbox resolves its roles,
  **then** discovery degrades to the candidate list for the affected role(s), and every other role still resolves normally — the mailbox as a whole does not fail.
  Traces to: US-1.1, US-1.4.
- **Scenario D-4 — Localized folder outside the candidate list.** **Edge (the M-01 core).**
  **Given** LIST succeeds, no special-use role is advertised, and an untagged folder named `Elementos-Enviados` exists that no candidate names,
  **when** the sent role resolves,
  **then** the outcome is `unknown` (`discovery_unresolved`), the response offers the per-mailbox setting, and nothing claims absence.
  Traces to: US-1.3, US-3.1.
- **Scenario D-5 — No folder is created.** **Edge.**
  **Given** any resolution outcome including total absence,
  **when** discovery completes,
  **then** the command trace contains zero `CREATE` (and zero `SUBSCRIBE`) commands.
  Traces to: US-1.4.

### 6.2 Overrides and provenance (US-2)

- **Scenario O-1 — Override outranks special-use.** **Happy.**
  **Given** stored `sent_folder_name="Archive"` and a server whose `\Sent`-tagged folder is `Sent Items`,
  **when** the sent role resolves,
  **then** `Archive` is used (source `override`) and the special-use folder is not selected.
  Traces to: US-2.1.
- **Scenario O-2 — Stale override warns, never swaps.** **Error.**
  **Given** stored `sent_folder_name="Archive"` and the server answering `[NONEXISTENT]` for it,
  **when** the sent role resolves,
  **then** the role is not silently remapped; the result carries the override-missing warning state, the setting is named as the remedy, and no candidate is auto-substituted.
  Traces to: US-2.2.
- **Scenario O-3 — Empty clears to automatic.** **Alternate.**
  **Given** the settings form saved with `sent_folder_name=""`,
  **when** the pair's config is persisted and the role next resolves,
  **then** the stored key is gone (`persistConfig` deletes it), automatic discovery runs, and the special-use/fallback folder is used.
  Traces to: US-2.3.
- **Scenario O-4 — Omitted field preserves.** **Alternate.**
  **Given** stored `drafts_folder_name="MyDrafts"` and an update request carrying no `drafts_folder_name` field,
  **when** the config is persisted,
  **then** `MyDrafts` remains stored and remains the override at the next resolution.
  Traces to: US-2.4.
- **Scenario O-5 — Legacy name shown as override.** **Edge.**
  **Given** a stored value whose operator intent is unprovable (any non-empty value),
  **when** settings render and the role resolves,
  **then** the value is treated as an explicit override with an Automatic option — it is never silently discarded, and its content is never used to classify it.
  Traces to: US-2.4.

### 6.3 Unknown vs absent, INBOX (US-3)

- **Scenario U-1 — Confirmed absence requires both proofs.** **Alternate.**
  **Given** LIST answered successfully and every candidate probe returned `[NONEXISTENT]`,
  **when** the sent role resolves,
  **then** `availability=absent`, the list returns an empty array (not a 502), and the explanation states the server has no such folder.
  Traces to: US-3.3.
- **Scenario U-2 — Probe failure is unknown, not absent.** **Error.**
  **Given** LIST succeeded but candidate probes failed with a timeout (not `[NONEXISTENT]`),
  **when** the sent role resolves,
  **then** the outcome is `unknown` — never `absent`, never empty-with-explanation.
  Traces to: US-3.2.
- **Scenario U-3 — Discovery-level failure is unknown (or the read's own failure).** **Error.**
  **Given** auth fails (or DNS, TLS, permission, connection refused) during discovery,
  **when** the mailbox read runs,
  **then** the failure surfaces with its safe transport class; nothing renders as a healthy empty folder; the cache timestamp does not advance.
  Traces to: US-3.2.
- **Scenario U-4 — Missing INBOX is fatal.** **Error.**
  **Given** a server whose INBOX SELECT returns `[NONEXISTENT]` (or any error),
  **when** the mailbox opens,
  **then** the read fails with the safe class; no "healthy empty account" rendering exists on any surface.
  Traces to: US-3.4.
- **Scenario U-5 — Unknown counts are null, not zero.** **Edge.**
  **Given** a role in the `unknown` state,
  **when** the folder list response is built,
  **then** that role's total is `null` (W0's nullable field) — a fabricated `0` never masquerades as "checked, empty".
  Traces to: US-3.1.

### 6.4 Ambiguity (US-4)

- **Scenario A-1 — Two special-use folders, no saved mapping.** **Error.**
  **Given** two folders tagged `\Sent` and no still-valid saved mapping,
  **when** the sent role resolves,
  **then** the outcome is `unknown` with reason `mapping_ambiguous`, both candidate names are surfaced to the settings path only (never into logs), and no folder is chosen.
  Traces to: US-4.1.
- **Scenario A-2 — Saved mapping wins over fresh ambiguity.** **Alternate.**
  **Given** a still-valid saved sent mapping and a fresh discovery finding two candidates,
  **when** the role resolves,
  **then** the saved mapping stands with no ambiguity surfaced.
  Traces to: US-4.2.
- **Scenario A-3 — Writes refuse to guess.** **Error.**
  **Given** an ambiguous or unresolved sent role,
  **when** a send (or draft save needing Sent) executes,
  **then** it fails visibly with the actionable settings remedy and writes nothing anywhere.
  Traces to: US-4.3.

### 6.5 Encrypted folder file (US-5, US-6)

- **Scenario F-1 — First open populates.** **Happy.**
  **Given** no saved metadata for the pair,
  **when** the panel first opens,
  **then** one discovery runs, the file appears (0600 file in a 0700 directory), and it holds roles, sources, UIDVALIDITY, schema/generation, transport and the validation time.
  Traces to: US-5.1.
- **Scenario F-2 — Restart reuses the saved mapping.** **Alternate.**
  **Given** a valid saved list younger than 24 h and a process restart,
  **when** the panel reopens,
  **then** zero discovery commands are issued and the roles resolve from the decrypted snapshot.
  Traces to: US-5.2.
- **Scenario F-3 — 24-hour refresh.** **Alternate.**
  **Given** a saved list older than 24 h,
  **when** the panel opens,
  **then** exactly one discovery refreshes the file.
  Traces to: US-5.3.
- **Scenario F-4 — Closed panel never refreshes.** **Edge.**
  **Given** the panel closed and a watcher-detected version change (and any elapsed time),
  **when** the system is observed,
  **then** zero IMAP commands run, no file is written, and only the dirty marker is set; the deferred refresh runs once at the next eligible panel event.
  Traces to: US-5.4, US-5.5.
- **Scenario F-5 — No timer exists.** **Edge.**
  **Given** the panel open (or closed) for any duration,
  **when** the process is observed,
  **then** no repeating folder-metadata refresh timer exists; refreshes occur only at the four §3.6 triggers.
  Traces to: US-5.3, US-5.4.
- **Scenario F-6 — Bit flip refuses.** **Error.**
  **Given** a stored envelope with one flipped bit,
  **when** it loads,
  **then** authentication fails, nothing decrypts, a `cache_corrupt` warning surfaces, and the live path continues normally.
  Traces to: US-6.1.
- **Scenario F-7 — Cross-pair copy refuses.** **Error.**
  **Given** pair B's file copied to pair A's path,
  **when** pair A loads it,
  **then** the AAD/pair comparison refuses it before any plaintext exists.
  Traces to: US-6.2.
- **Scenario F-8 — Identical payloads, different ciphertexts.** **Edge.**
  **Given** two consecutive writes of byte-identical payloads,
  **when** both files are compared,
  **then** the ciphertexts differ (fresh nonce per write).
  Traces to: US-6.3.
- **Scenario F-9 — Oversized/malformed rejected before allocation.** **Edge.**
  **Given** envelopes that are truncated, bad-nonce-length, unsupported-version, or many times over the 64 KiB payload budget,
  **when** each is offered for load,
  **then** each is refused with the safe class, with no allocation proportional to its claimed size.
  Traces to: US-6.4.
- **Scenario F-10 — Locked store is live-only.** **Error.**
  **Given** a locked credential store,
  **when** any cache operation runs,
  **then** the result is cache-unavailable + live-only; no key is minted over existing data and no plaintext fallback is written.
  Traces to: US-6.5.
- **Scenario F-11 — Crash mid-write leaves a complete file.** **Edge.**
  **Given** a write interrupted by process death between the atomic write's start and completion,
  **when** the file is next loaded,
  **then** exactly one complete valid snapshot (old or new) opens.
  Traces to: US-6.6.

### 6.6 Exclusion gate (US-7)

- **Scenario G-1 — Gate passes when both product-owned exclusions provably hold.** **Happy.**
  **Given** E-1's in-process staging-exclusion evaluation passes and E-2's backup-skip is in the running build,
  **when** the first cache write runs,
  **then** it succeeds; the Wave E end-to-end proof then shows staging + a backup archive contain no cache file while a positive-control allowed file is captured.
  Traces to: US-7.1, US-7.3.
- **Scenario G-2 — Gate blocks when missing.** **Error.**
  **Given** either exclusion absent on the machine,
  **when** the first cache write runs,
  **then** the write is refused, zero cache files exist, and the mailbox runs live-only with the visible `cache_unavailable` notice.
  Traces to: US-7.2.
- **Scenario G-3 — Pre-tracked file still blocks.** **Edge.**
  **Given** a cache path already tracked by data-directory version control (ignore rules do not untrack tracked files),
  **when** the gate evaluation runs,
  **then** the gate reports not-excluded and refuses disk writes until the untrack remediation (E-3) lands.
  Traces to: US-7.2.

### 6.7 Header cache (US-8)

- **Scenario H-1 — Warm open is instant and socket-free.** **Happy.**
  **Given** newest-50 headers cached less than 5 minutes ago,
  **when** the folder opens,
  **then** rows render from memory with the recorded validation time, zero FETCH commands, and zero new socket acquisitions for the display read.
  Traces to: US-8.1.
- **Scenario H-2 — Stale open: labelled rows + exactly one refresh.** **Alternate.**
  **Given** cached rows older than 5 minutes,
  **when** the folder opens,
  **then** stale rows render immediately and are labelled, exactly one live refresh runs, and on its failure the stale rows persist with a visible error/Retry and the timestamp is unchanged.
  Traces to: US-8.2.
- **Scenario H-3 — Cold open fetches once.** **Alternate.**
  **Given** no cached rows,
  **when** the folder opens,
  **then** one live fetch populates page one and the cache.
  Traces to: US-8.3.
- **Scenario H-4 — Retention drop after 30 minutes closed.** **Edge.**
  **Given** the panel closed and 30 minutes elapsed,
  **when** the cache is inspected (and the panel reopens),
  **then** the folder's headers were dropped, zero IMAP commands ran while closed, and the reopen fetches live.
  Traces to: US-8.4.
- **Scenario H-5 — Fifty-row cap never shortens the live list.** **Edge.**
  **Given** a folder with 80 messages and a fresh 50-row snapshot,
  **when** the list renders,
  **then** the cache holds exactly the newest 50, the live page 1 (25 rows) renders normally, and no live row is omitted to fit the cache.
  Traces to: US-8.5.
- **Scenario H-6 — Older pages and search bypass the cache.** **Edge.**
  **Given** a cached newest-50 snapshot,
  **when** the user loads older pages (past 50) or runs a search,
  **then** those reads run live, are never served from the cache, and never write their rows into it.
  Traces to: US-8.5 (and the ADR's "never reused for search or older pages").

### 6.8 Invalidation and ordering (US-9)

- **Scenario V-1 — UIDVALIDITY change discards the epoch.** **Error.**
  **Given** a folder whose UIDVALIDITY changed server-side,
  **when** anything next reads it,
  **then** every old-epoch header, cursor and count is discarded before new rows publish, the saved version refreshes once, and no old UID is used against the new epoch.
  Traces to: US-9.1.
- **Scenario V-2 — Superseded read publishes nothing (I-02).** **Error.**
  **Given** a read that captured the pre-mutation revision and completed after a mark-read (and its post-mutation refresh) advanced the revision,
  **when** the stale read finishes,
  **then** it writes nothing anywhere — memory rows, disk snapshot, timestamps, counts — and the newer revision's data stands.
  Traces to: US-9.2.
- **Scenario V-3 — Late write after pair removal refuses.** **Error.**
  **Given** the pair removed/disabled while a snapshot write is in flight,
  **when** the write completes,
  **then** the generation guard refuses it and no file exists (or reappears) for the removed pair.
  Traces to: US-9.3.
- **Scenario V-4 — Reconfiguration deletes rather than migrates.** **Alternate.**
  **Given** a host, port, credential or override change,
  **when** the pair is next used,
  **then** the old generation's memory and disk caches are deleted, rediscovery runs under the new generation, and no encrypted data is migrated.
  Traces to: US-9.4.
- **Scenario V-5 — Stale rows wear their age.** **Edge.**
  **Given** cached rows within retention but past freshness,
  **when** they render,
  **then** their age is visible and they are never presented as fresh.
  Traces to: US-9.5.
- **Scenario V-6 — External move/delete reflects at next validation.** **Alternate.**
  **Given** another client moved or deleted a cached message,
  **when** the next eligible refresh validates membership,
  **then** the row leaves the cache, affected counts invalidate, and an open stale ref gets the visible changed-or-deleted outcome — never a served-as-fresh row.
  Traces to: US-9.5 (and §3.11's move/delete row).

### 6.9 Logging and instrumentation (US-10, US-15)

- **Scenario L-1 — Marker-free logs.** **Error.**
  **Given** synthetic folder names, subjects and addresses carrying distinctive leak markers, driven through every §6 scenario,
  **when** all log sinks are examined,
  **then** zero markers appear, while safe classes, durations and counts do appear.
  Traces to: US-10.1, US-10.2.
- **Scenario L-2 — Instrument sub-fields and the joiner rule.** **Edge.**
  **Given** a cache hit, a cache miss, and a discovery that joined a coalesced flight,
  **when** each operation's instrument record is written,
  **then** each carries W2's sub-fields of the w6-frozen shape (`source`, `hit`, `duration_ms`, `outcome`) and nothing owned by another layer, and the joined discovery records `socket_count=0` plus the shared-flight marker.
  Traces to: US-15.1, US-15.2.

### 6.10 Folder search (US-11)

- **Scenario S-1 — Subject substring, server-side.** **Happy.**
  **Given** messages whose subjects contain the query among 300 in the folder,
  **when** a folder search runs,
  **then** the matching command is a server-side IMAP SEARCH on SUBJECT, results return 25 per page under an issued cursor, and the header cache is neither read nor written.
  Traces to: US-11.1.
- **Scenario S-2 — Sender/recipient substring.** **Happy.**
  **Given** a message whose sender matches the query while its subject does not,
  **when** a folder search runs on FROM/TO,
  **then** the message is found (the decided matching surface is subject + sender/recipient, founder Q-D=A).
  Traces to: US-11.2.
- **Scenario S-3 — Beyond 200 the search view ends honestly.** **Edge.**
  **Given** a search sequence that has delivered 200 rows,
  **when** the next page is requested,
  **then** `view_limit_reached` is reported with no next cursor and no auto-continuation.
  Traces to: US-11.3.
- **Scenario S-4 — Stale cursor resets visibly once.** **Error.**
  **Given** a cursor bound to another query, folder version or generation,
  **when** it is presented,
  **then** the typed 409 stale-cursor result requests one visible view reset — no silent reinterpretation, no reset loop, zero cache interaction.
  Traces to: US-11.4.

### 6.11 Part reader and MIME classification (US-12)

- **Scenario P-1 — Part fetch is transient and flag-free.** **Happy.**
  **Given** an addressed `part_index` on the leased session,
  **when** the part reader fetches a text part,
  **then** only that part's bytes transfer (partial fetch permitted), zero flag changes occur, nothing is written server-side, and nothing is retained by this package.
  Traces to: US-12.1.
- **Scenario P-2 — Draft marker excluded from indices.** **Edge.**
  **Given** a message carrying the Omnipus draft-body marker plus two attachments,
  **when** leaf indices are resolved,
  **then** the marker part is excluded from numbering and classification, and two reads under the same epoch resolve identical indices.
  Traces to: US-12.2.
- **Scenario P-3 — Established absence.** **Edge.**
  **Given** a message with no attachment-classified parts,
  **when** the classifier runs,
  **then** `has_attachments=false` is established absence, and the same structure classifies identically on a second run.
  Traces to: US-12.3.

### 6.12 Reference validation (US-13)

- **Scenario R-1 — Stale-epoch reference refused.** **Error.**
  **Given** a reference minted against folder epoch N and the folder's UIDVALIDITY now N+1,
  **when** a detail read, seen-write or part fetch presents it,
  **then** the same-lease validation refuses it with the typed stale-reference result requesting one view reset, and zero server commands act on it.
  Traces to: US-13.1.
- **Scenario R-2 — Foreign-generation reference refused.** **Error.**
  **Given** a reference minted under another configuration generation,
  **when** any `view.go` operation receives it,
  **then** it is refused the same way, and no path in this package mints a reference.
  Traces to: US-13.2.

### 6.13 Counts freshness (US-14)

- **Scenario C-1 — Fresh counts are served, not re-dialed.** **Happy.**
  **Given** counts fetched 4 minutes 59 seconds ago,
  **when** a trigger event fires (panel open, folder switch, own action),
  **then** the stored counts are served with zero IMAP commands (stale-gating, founder Q-C=A).
  Traces to: US-14.1.
- **Scenario C-2 — Stale counts refresh exactly once; manual always; no timer.** **Alternate.**
  **Given** counts absent or older than 5 minutes,
  **when** a trigger fires — and separately, when manual Refresh fires on fresh counts,
  **then** the stale case runs exactly one live refresh, the manual case refreshes regardless of age, and no repeating counts timer exists on any path.
  Traces to: US-14.1, US-14.2.
- **Scenario C-3 — Failed counts refresh preserves and labels.** **Error.**
  **Given** a counts refresh that fails,
  **when** the next render happens,
  **then** the previous counts stand with the visible error/Retry, the timestamp is unchanged, and a superseded refresh publishes nothing (§3.10).
  Traces to: US-14.3.

---

## 7. TDD plan (tests designed from this specification, before implementation)

**Owner and order:** qa-lead (w6-proof) authors all test files (RED), driven by this section; production owners never edit them — **including the exclusion-gate tests, which are qa-lead/w6-owned** (register row 18; this section's former "W4-owned integration" label is corrected, grill M-4). Order: unit tests for the pure pieces (mapping/state machines, envelope codec, cache bounds) → package-integration tests against the real in-memory IMAP server → the exclusion-gate integration checks. Command shapes follow `CLAUDE.md`/`omnipus-backend-rules` (tags `goolm,stdjson`, `CGO_ENABLED=0`, one narrow local test at a time; CI owns the suites). Harness: `pkg/email/imapserver_test.go::startMemIMAP` and `view_test.go::startViewIMAP`/`::startViewIMAPRaw` (the capability-set variant drives D-1/D-3); the global `imapDial` seam forbids blind parallelism. **Oracle split (register R-4, grill I-4):** the end-to-end I-02 oracle for the cache layers is this package's V-2 test below; W1's T16/T21 assert the W1-observable seam callbacks — the two are complementary, and neither package duplicates the other's oracle. **Production seam ordered for RED (grill M-5):** `cache_file.go` routes its atomic replace through a package-local write indirection defaulting to `fileutil.WriteFileAtomic` (the `pkg/credentials` `writeFileAtomicFn` precedent, verified at `pkg/credentials/store_lock_test.go::HoldFirstWrite` usage) so the crash-consistency test can park the first write without any production edit or test hook in logic.

### 7.1 Named test files and what each test proves

| Test file (new unless named) | Test (proposed names) | Proves (oracle derived from this spec, never from the implementation) | Traces to |
|---|---|---|---|
| `pkg/email/folder_discovery_test.go` | `TestDiscovery_SpecialUseResolvesBeforeFallback` | SPECIAL-USE-tagged folder wins with source `special_use`; zero fallback probes after success (D-1) | US-1.1 |
| | `TestDiscovery_FallbackOrderedProbeStopsAtFirstSuccess` | Candidate order `Sent → Sent Items → Sent Messages → [Gmail]/Sent Mail`; the first success ends probing (D-2) | US-1.2 |
| | `TestDiscovery_UnsupportedExtensionDegradesPerRole` | LIST-EXTENDED absent / SPECIAL-USE absent / partial advertisement → fallback path, other roles unaffected, mailbox succeeds (D-3) | US-1.1, US-1.4 |
| | `TestDiscovery_NoCandidatesIsUnknownNeverAbsent` | LIST OK, zero candidate hits, unrelated folders present → `unknown` + `discovery_unresolved`, setting offered (D-4) — **the careless-implementation killer, see §11 CX-1** | US-1.3, US-3.1 |
| | `TestDiscovery_ProbeTimeoutIsUnknownNotAbsent` | Candidate probe times out (stalled fake server) → `unknown`, not `absent` (U-2) | US-3.2 |
| | `TestDiscovery_MissingInboxIsFatalEvenForNonExistent` | INBOX `[NONEXISTENT]` → whole-read failure with safe class; no per-role empty rendering (U-4) | US-3.4 |
| | `TestDiscovery_AmbiguousSpecialUseAsks` | Two `\Sent` tags, no saved mapping → `mapping_ambiguous`, nothing auto-picked (A-1) | US-4.1 |
| | `TestDiscovery_SavedMappingBeatsFreshAmbiguity` | Valid saved mapping + fresh ambiguity → saved stands, no prompt (A-2) | US-4.2 |
| | `TestDiscovery_OverrideBeatsSpecialUse` / `..._StaleOverrideWarnsNotSwaps` / `..._EmptyClearsToAutomatic` / `..._OmittedKeeps` | O-1..O-4, including that the *reader* (not the setter) honours the semantics — the setter behaviour is pinned by existing tests | US-2.1–2.3 |
| | `TestDiscovery_NeverCreatesFolders` | Command trace over every resolution outcome contains zero CREATE/SUBSCRIBE (D-5) | US-1.4 |
| | `TestDiscovery_UnresolvedWriteDestinationRefuses` | send/draft path with unresolved/ambiguous role → visible settings error, zero writes (A-3) | US-4.3 |
| `pkg/email/cache_file_test.go` | `TestCacheFile_RoundTripPreservesAllFields` | Save→load preserves roles, sources, UIDVALIDITY (incl. nil = unknown), schema/generation, transport, timestamps (F-1) | US-5.1, US-6 |
| | `TestCacheFile_BitFlipRefusesWithoutPlaintext` | Single-bit flip anywhere → auth failure, zero plaintext, `cache_corrupt` class (F-6) | US-6.1 |
| | `TestCacheFile_ForeignPairRefuses` / `..._ForeignGenerationRefuses` / `..._ForeignPurposeRefuses` | Cross-pair copy, old generation, wrong purpose string → all refuse via AAD comparison (F-7) | US-6.2 |
| | `TestCacheFile_FreshNoncePerWrite` | Identical payloads written twice → different ciphertexts **and** different nonces (F-8) | US-6.3 |
| | `TestCacheFile_RejectsBeforeAllocation` | Truncated / bad nonce length / unsupported version / oversized (claim ≫ 64 KiB budget) envelopes → refused with no allocation proportional to claimed size (F-9; dataset DT-2) | US-6.4 |
| | `TestCacheFile_LockedStoreIsLiveOnlyNoMint` | Locked store → cache-unavailable; no key file appears; no plaintext fallback written (F-10) | US-6.5 |
| | `TestCacheFile_AtomicReplaceCrashConsistency` | Injected mid-write failure (parked first write through the package-local write indirection — the `pkg/credentials` precedent, ordered in this section's owner paragraph per grill M-5) → next load opens old-or-new complete snapshot (F-11) | US-6.6 |
| | `TestCacheFile_PermissionsUnix` | Created dir 0700, file 0600 (Unix; the Windows restrictive-access evidence comes from the register row-23 instrument — w5-integration's Windows-leg workflow extension, or the fallback UAT-machine receipt — §9; a chmod-only claim is unacceptable on any platform) | US-6 (envelope rules) |
| | `TestCacheFile_TriggersExactlyFour` | Trigger matrix: first open populates; <24 h reopen = zero discovery; >24 h reopen = one; manual = one; post-failure = exactly one; **no ticker exists** (F-2–F-5) | US-5.1–5.5 |
| | `TestCacheFile_ClosedPanelNeverTouchesServerOrDisk` | Panel closed + watcher version change + elapsed time → zero IMAP commands, zero writes, dirty flag only; deferred refresh fires once at next open (F-4) | US-5.4, US-5.5 |
| `pkg/email/header_cache_test.go` | `TestHeaderCache_WarmHitServesNewestFifty` | Fresh snapshot → memory hit with recorded validation time; ≤50 rows; source `memory` (H-1, H-5) | US-8.1, US-8.5 |
| | `TestHeaderCache_StaleTriggersExactlyOneRefresh` | >5 min age → labelled rows + exactly one background live refresh; failing refresh keeps rows+error, timestamp unchanged (H-2) | US-8.2 |
| | `TestHeaderCache_RetentionDropsAfterThirtyMinutesClosed` | Fake clock: 30 min post-close → dropped; zero commands while closed; reopen fetches live (H-4) | US-8.4 |
| | `TestHeaderCache_BudgetOverflowIsVisibleNotTruncated` | Fill to the 4 MiB budget → typed budget error, live-only outcome, no truncated fields, no silently omitted rows (dataset DT-3) | US-8 (budget rule) |
| | `TestHeaderCache_SearchAndOlderPagesBypass` | Search + page-beyond-50 → live reads, never served from nor written to the cache (H-6) | US-8.5 |
| | `TestHeaderCache_CrossPairIsolation` | Two pairs, one account, different folders → neither sees the other's rows (I-01's cache face) | US-9 (isolation rule) |
| | `TestHeaderCache_UidValidityChangeDiscardsEpoch` | UIDVALIDITY bump → old-epoch rows/cursors/counts gone before new rows publish; version saved once (V-1) | US-9.1 |
| | `TestHeaderCache_SupersededReadPublishesNothing` | Pause a pre-mutation read after its server snapshot; complete mark-read + post-refresh; release the old read → nothing published anywhere; **and no new timer was introduced** (V-2 — **the end-to-end I-02 oracle for the cache layers**; W1's T16/T21 assert the W1-observable seam callbacks, register R-4/grill I-4) | US-9.2 |
| | `TestFolderCounts_FreshServedWithoutDial` / `..._StaleRefreshesOnceManualAlwaysNoTimer` / `..._FailedRefreshPreservesAndLabels` | The counts rows of §3.9's counts block: fresh counts (4:59) served with zero dials; stale (5:01) exactly one refresh, manual Refresh always, **no repeating timer exists**; failed refresh keeps prior counts + visible error, timestamp unchanged (C-1..C-3; grill I-6's missing tests) | US-14.1–14.3 |
| | `TestHeaderCache_LateWriteAfterRemovalRefuses` | Pair removed with a write in flight → generation guard refuses; no file reappears (V-3) | US-9.3 |
| | `TestHeaderCache_ReconfigDeletesNotMigrates` | Host/port/credential/override change → old caches deleted, rediscovery under new generation (V-4) | US-9.4 |
| `pkg/email/mail_search_test.go` (proposed) | `TestFolderSearch_SubjectAndAddressServerSide` / `..._Bounds25And200` / `..._StaleCursorTypedReset` / `..._NeverTouchesHeaderCache` | Server-side SUBJECT + FROM/TO substring search (founder Q-D=A); 25/page, 200 ceiling, `view_limit_reached` beyond; stale cursor → one typed 409 reset; zero cache reads or writes from any search (S-1..S-4; register row 4's added story) | US-11.1–11.4 |
| `pkg/email/attachment_parts_test.go` (proposed) | `TestPartReader_FetchesOnlyAddressedPart` / `..._DraftMarkerExcludedFromIndices` / `TestClassifier_HasAttachmentsEstablishedAbsence` / `..._DeterministicSameEpoch` | Only the addressed part transfers (partial for text, whole for image; zero flag changes; nothing server-side written or kept); the draft marker is excluded from leaf indices and classification, identical indices twice under one epoch; `false` is established absence; classification deterministic (P-1..P-3; register rows 14–15) | US-12.1–12.3 |
| `pkg/email/view_ref_validation_test.go` (proposed) | `TestRefValidation_StaleEpochRefused` / `..._ForeignGenerationRefused` | A reference from another epoch or generation is refused with the typed stale-reference result before any server command acts on it; zero mint paths exist in this package (R-1, R-2; register row 16) | US-13.1–13.2 |
| `pkg/email/instrument_emission_test.go` (proposed) | `TestInstrument_CacheAndDiscoverySubFields` / `TestInstrument_JoinerRecordsZeroSocketAndMarker` | W2's sub-fields of the w6-frozen record present on cache/discovery paths, no other layer's fields fabricated; a coalesced joiner records `socket_count=0` + shared-flight marker (L-2; register row 17) | US-15.1–15.2 |
| `pkg/email/view_missing_folder_test.go` (existing — qa-lead extends) | `TestFolderCounts_UnknownRoleHasNullableTotal` / `TestFolderCounts_ConfirmedAbsentIsEmptyNotError` | The count-side faces of U-5/U-1: unknown → `total=null`; confirmed absent → empty array, not 502 | US-3.3, US-3.1 |
| | (existing `TestFolderCounts_*` three) | **Regression**: must pass unchanged (§7.3) | — |
| `pkg/email/logging_leak_test.go` | `TestW2Diagnostics_LeakMarkerSweep` | Drive every §6 scenario with leak-marker fixture data; sweep all sinks for markers (zero) while safe classes/counts present (L-1) | US-10.1–10.2 |
| `pkg/email/` (gate unit/integration rows — **qa-lead/w6-owned**, register row 18; this table's former "W4-owned integration" label corrected, grill M-4) | `TestMailCacheExclusionGate_BlocksWhenMissing` / `..._PassesWhenProvable` / `..._DetectsPreTrackedFile` | G-1..G-3 against the product's in-process exclusion evaluator (§3.8 E-1, `git check-ignore`-equivalent semantics, no shell-out) and the backup-skip half (E-2): missing either → refuse + live-only + visible notice; both provable → write succeeds; pre-tracked path → not excluded | US-7.1–7.3 |
| | `TestMailStagingAndBackup_ContainNoCacheFiles` | **Wave E end-to-end proof, not repo CI** (grill I-5; register rows 18/23): on a machine where both exclusions provably hold, staging + the application backup archive contain no plaintext-or-ciphertext cache marker while a positive-control allowed file IS captured. Repo CI has no such machine; the evidence instrument is the Wave E gate, per the register | US-7.3 |

### 7.2 Test datasets

Each row traces to its scenario; boundary values come from this spec's numbers (50, 150, 25, 200, 5 min, 24 h, 30 min, 4 MiB, 64 KiB), never from the implementation.

**DT-1 — Discovery inputs** (for `folder_discovery_test.go`)

| Case | Server setup | Expected outcome | Traces to |
|---|---|---|---|
| special-use sent+drafts | tags `\Sent`, `\Drafts` | both `special_use` | D-1 |
| special-use inbox only | tag `\Sent` only; Drafts named `Drafts` | sent `special_use`; drafts `fallback`("Drafts") | D-1, D-2 |
| gmail naming | folders `[Gmail]/Sent Mail`, `[Gmail]/Drafts`, no tags | both `fallback` at the 4th/3rd candidate | D-2 |
| localized none-of-the-list | folder `Elementos-Enviados`, no tags | sent `unknown` (`discovery_unresolved`) | D-4 |
| genuinely empty | LIST OK; zero candidates exist | both `absent` (both proofs met) | U-1 |
| probe stall | candidates exist but SELECT stalls past deadline | `unknown`, not absent | U-2 |
| two sent tags | two `\Sent` folders | `mapping_ambiguous` | A-1 |
| override present+valid / +missing / empty / omitted | stored override variants | override wins / warns / automatic / preserved | O-1..O-4 |
| no INBOX | INBOX removed server-side | fatal safe-class failure | U-4 |

**DT-2 — Envelope codec boundaries** (for `cache_file_test.go`)

| Case | Input | Expected | Traces to |
|---|---|---|---|
| minimal payload | one role resolved, nil UIDVALIDITY | round-trips; nil stays nil (never fabricated 0) | F-1, §3.11's nullable-epoch rule (grill M-3 correction: the cited "R-3.11" rule does not exist; the rule is §3.11's UIDVALIDITY row prose) |
| max normal payload | 3 roles incl. ambiguity lists, ≈ budget boundary | round-trips under the 64 KiB budget | F-1 |
| budget+1 | payload just over 64 KiB target | refused or visible cache-unavailable — never truncated | F-9 |
| 10× oversized claim | header claims ≫ actual, or huge body | refused **before** allocation proportional to the claim | F-9 |
| truncated / bad nonce / bad version / bad base64 | mutated envelopes | each refused with its safe class | F-9 |
| flipped bit (several positions: nonce, ciphertext head/tail) | 1-bit mutations | auth failure every time, zero plaintext | F-6 |
| identical double-write | same payload ×2 | different nonce + ciphertext | F-8 |
| crash points | write interrupted at fraction f ∈ {25%, 50%, 90%} | old-or-new complete file loads | F-11 |

**DT-3 — Header-cache bounds** (for `header_cache_test.go`)

| Case | Input | Expected | Traces to |
|---|---|---|---|
| exactly 50 / 51 / 5000 messages | fresh snapshots | cache holds exactly newest 50; live list never shortened | H-5 |
| 25/200 pagination | page requests at the boundaries | page = 25 rows; 200-row view ceiling; beyond-200 → search remedy; cache unaffected | H-6 |
| freshness boundary | age 4:59 vs 5:01 (fake clock) | hit-without-refresh vs stale+one refresh | H-1, H-2 |
| retention boundary | closed 29:59 vs 30:01 | retained vs dropped; zero commands while closed | H-4 |
| budget boundary | rows sized to just-under/just-over 4 MiB total | kept vs visible budget refusal | US-8 budget rule |
| oversized subject/address envelope | one row with a pathological subject | visible per-data cache-unavailable outcome, never truncation | US-8 budget rule |
| leak-marker rows | markers in names/subjects/addresses | markers in rows OK (memory, not logs); logs clean | L-1 |
| counts freshness boundary | counts age 4:59 vs 5:01 (fake clock) | served-without-dial vs exactly one refresh; manual Refresh always; no timer | C-1, C-2 (grill I-6) |
| counts failure | refresh fails or is superseded | prior counts + visible error, timestamp unchanged; superseded publishes nothing | C-3 |

**DT-4 — Invalidation ordering** (for the V-scenarios)

| Case | Sequence | Expected | Traces to |
|---|---|---|---|
| pause-and-release (I-02) | read captures rev N → mark-read (rev N+1) + refresh → release read | nothing published by the stale read | V-2 |
| UIDVALIDITY mid-read | epoch bumps between SEARCH and FETCH | old rows discarded; new epoch saved once | V-1 |
| removal race | pair removed while write paused | write refused; no file | V-3 |
| reconfig race | override change while read paused | stale read publishes nothing under old generation | V-4 |

**DT-5 — Search, part reader, classification and reference validation** (correction round; for the S/P/R-scenarios)

| Case | Input | Expected | Traces to |
|---|---|---|---|
| subject vs address match | 300-message folder; query matching one subject and a different sender | SUBJECT search finds the subject match; FROM/TO search finds the sender match; both server-side | S-1, S-2 |
| pagination bounds | search result sets of 24 / 25 / 200 / 201 | 25-row pages; 200-row ceiling; `view_limit_reached` with no next cursor beyond | S-3 |
| stale cursor | cursor from another query / folder version / generation | typed 409 reset requested once; no silent reinterpretation | S-4 |
| cache interaction | any search while a newest-50 snapshot exists | zero cache reads, zero cache writes | S-1, US-11.4 |
| part transfer shapes | text part; image part; PDF part | partial fetch permitted for text; whole part for image/PDF; zero flag changes; nothing kept | P-1 |
| draft-marker structure | marker part + two attachments | marker excluded from indices and classification; identical resolution twice | P-2 |
| established absence | zero attachment-classified parts | `has_attachments=false`, deterministic across runs | P-3 |
| stale reference | ref minted at epoch N, live epoch N+1; ref from another generation | refused with the typed stale-reference result before any acting command | R-1, R-2 |

### 7.3 Regression impact

The feature modifies existing behaviour (`FolderCounts` semantics, `folderNameFor`), so:

1. **Must keep passing unchanged:** the three existing tests in `pkg/email/view_missing_folder_test.go` (named in §2.1), the existing `pkg/email/view_test.go` page/read tests, the mail-budget singleflight tests in `pkg/email/mail_budget_test.go` (they pin coalescing shapes W2 consumes), and the credentials package's tests (W2 touches nothing there, but the `DeriveSubkey` contract must not drift).
2. **Behaviour deliberately changed, needing NEW regression tests:** missing Sent/Drafts previously produced zero counts via a literal-name STATUS; now the count depends on the resolved mapping. The new count-side tests in §7.1 (unknown → null, absent → empty-not-error) protect the new semantics; the old tests keep passing because a literally-named missing folder remains the absent case's simplest instance.
3. **Seam tests:** the discovery service and snapshot store are consumed through interfaces (§4) — integration seam tests inject fakes for w5-integration's handlers without a live server, proving the boundary compile-breaks rather than silently drifting.

---

## 8. Explicit non-goals

| Outside this package | Boundary / reason |
|---|---|
| **Phase 2's on-disk header cache** (`headers.enc`) | Explicitly excluded by the dispatch. This package builds only the folder-metadata file; the header snapshot store, its 7-day/150-row/1 MiB/32 MiB bounds, and the D6/D21 amendment are a later wave after Phase 1's separate recorded evidence. The §3.7 envelope deliberately leaves room (purpose-separated keys, schema version, transport field) so Phase 2 never breaks the folder file's format. |
| The connection pool, budgets and leases (W1) | W2 consumes the frozen interfaces; it never dials privately, never creates sockets, never changes `transport.go` or `mail_budget.go`. |
| Gateway handlers, wire schemas, removal cascades, backup-skip edits | Owned by w5-integration (gateway files) with schemas from the ADR-W0 contracts role: W2 produces the values; the ADR-W0 role regenerates the types; w5-integration wires. W2 edits no `contracts/` or `pkg/gateway/` file. **`message_ref` issuance is w5-integration's** (register row 16) — W2 validates only (§3.16). |
| Panel UI, stale labels' rendering, settings forms (W3) | W3 renders freshness/availability from the metadata W2 produces; the connector override form already exists and is unchanged. |
| Agent tools (W10) | Phase 1 tools keep their live read paths; they consume no header cache and no discovery service in this wave (ADR: agent work never fills or extends panel caches). |
| Watcher behaviour changes (W1) | The watcher keeps its cadence and backoff; its only interaction with W2 is marking dirty. No watcher-driven refresh, no watcher fairness rewrite here. |
| Server folder creation, subscription or any mailbox mutation from discovery | Zero `CREATE`/`SUBSCRIBE`/`EXPUNGE`; discovery is read-only by rule R-3.2-4. |
| Message bodies, snippets, attachment bytes or attachment lists in any cache | The folder file and the memory header cache hold metadata only; the field set is closed (§3.9). No body field may be added without changing this spec. |
| New config keys, env vars, or credential-store changes | Overrides reuse `sent_folder_name`/`drafts_folder_name`; keys come from `DeriveSubkey`; the HKDF derivation and boot contract are untouchable (the credentials CLAUDE.md forbids "fixing" the misleading comment). |
| A local mailbox mirror, full-text index, SQLite store, or any new runtime dependency | Pure Go, single binary, file-based storage only. |
| Historical Git/backup cleanup or secure-erase guarantees | Exclusion is prevention-before-first-write (§3.8); nothing promises SSD/Git-history erasure (the ADR's stated limits). |
| The `email-watch/` state's own exclusion/purge work | **Decided (founder Q-B=A, 2026-10-02):** excluded from staging/backups **and** purged on mailbox removal (§3.8 E-5). Still not W2's code: the purge rides w5-integration's removal cascade, the watcher file is w1's — recorded here because this spec's former open question OQ-6 is closed by the decision, not bundled silently into W2. |
| `message_ref` issuance, the preview endpoint's cap/purpose enforcement, and any viewing-session preview byte-keep | w5-integration (issuance, endpoint) and the preview feature's owners; W2's part reader is transient and stateless (§3.14) and never implements a keep. |
| Saved-mail-file resource policy (ordinary-workspace-after-Save; temporary preview stricter; saved HTML scripts-off + per-file checkbox — founder Q-E=A) | w4-features / Library owners per the ADR's feature tables; no touchpoint in this package — the cache never stores saved-file state and the exclusion rules cover only the Mail cache directory. |

## 9. Definition of Done (the repo's two never-merged lines)

**Code correct and tested.** Evidence required, per claim:

- Every §7 test exists, ran red-before-green (tests-only CI commit proof, or the one dispatcher-owned narrow local run the root rules permit), and passed in CI — with exit codes captured directly, named `--- PASS` lines counted (not just `ok`), and the false-green checklist applied before any green is reported. **Instrument honesty (grill I-5):** the two named exceptions are `TestMailStagingAndBackup_ContainNoCacheFiles` and the gate's deployed-state proof — these require machine state repo CI does not have, so their evidence instrument is the **Wave E end-to-end proof on a machine where both §3.8 exclusions provably hold** (register row 18 / Wave E gate), never a weaker local check reported as "CI-verified".
- The §11 counterexamples each have a named test that demonstrably fails against a careless implementation (the mutation probe: qa-lead's CHECK runs at least the CX-1/CX-2/CX-7/CX-9 mutations and shows the suite dies).
- Crypto and permissions claims carry executed evidence: nonce-freshness, AAD-refusal, reject-before-allocate and atomicity tests (DT-2) green; Unix 0700/0600 verified in the Unix CI leg; the **Windows restrictive-access evidence comes from a named instrument (register row 23): w5-integration's wave extends the cross-platform workflow's Windows leg to run the `pkg/email` cache tests, with the fallback path a founder-run/UAT Windows machine executing the ACL checks with a signed receipt** — repo CI's existing Windows leg cannot see this evidence and "verified in CI's Windows leg" is claimable only through that instrument (a chmod-only claim is not acceptable on any platform).
- Footprint honesty: the 50-row/4 MiB/64 KiB bounds are asserted by tests (DT-3), not inferred from row caps; no claim that a bound implies a measured memory number.
- No forbidden surface introduced: guards `scripts/check-no-shell-deny-patterns.sh` etc. stay green; `make lint-budgets` passes for the new files; `make verify-contracts` passes after W0's regeneration (W2's code compiles against the generated types only).

**Reachable by a user/agent.** Evidence required:

- A real user opens the Mail panel on a configured mailbox: the folder rail resolves via discovery/override (including at least one non-`Sent`-named server in the executed test plan), folders open, stale labels and the unresolved state render, Refresh works — exercised UAT rows with independent validation, not registry checks.
- The tool-registration gate (Hard Constraint #6) is explicitly **not applicable to new tools** — W2 adds no tool — but the reachability analogue still holds: the folder/availability metadata must be observable by a real client through the regenerated wire types; a backend service nobody's response carries is a library, not a feature.
- The exclusion gate is observable: where the product's own checks prove both exclusions (its backup-skip in the running build and a provable staging exclusion), cache files exist and end-to-end staging/backup instruments capture none (G-1's Wave E run); where the gate cannot prove exclusion, the live-only notice is visible in the panel (G-2).
- The matching user-facing documentation updates (§10) are drafted by the implementing leads and audited by docs-verifier against the executed behaviour — written-but-not-audited is not done.
- Disk-cache enablement specifically: **gated by the product's own §3.8 exclusion guarantee** — the E-2 backup-skip is product code that must have landed, and the E-1 staging-exclusion check must pass in the running build, before the first write on any install. "Code complete but gate not yet proven" is the honest state until the product-owned halves are landed and evidenced; it must never be reported as done, and the gate's disabled state is never reported as a machine-setup limitation — it is the product's enforced safety outcome (founder Q-A).

## 10. User-facing documentation TODOs

Per the ADR's documentation table (drafting owners named there; docs-verifier audits each against actual behaviour):

| Page (absolute path in this checkout) | What must be said for W2's slice | Owner |
|---|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/connectors.md` | The Sent/Drafts name setting: leaving it empty means **Automatic** (Omnipus discovers the server's Sent/Drafts folders), a typed name overrides discovery, and clearing it returns to Automatic. An override the server can't find is shown as a settings warning, never silently swapped. What "unresolved" means: the folders couldn't be identified — set the name explicitly; never stated as "the server has no such folder". | w5-integration/backend-lead (ADR-W4; per ADR table) |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/mail.md` | The folder rail's resolved names and counts; immediate cached rows vs live validation; the stale-age label and Refresh; unknown vs absent roles (the unresolved state and the settings prompt — never a false "no such folder"); panel-close retention in plain words (lists revalidate after ~30 minutes away; counts/headers are not stored on disk); no offline mailbox — reopening fetches live. | W3/frontend-lead (per ADR table) |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/security.md` | Precisely what is stored: one small **encrypted** file per mailbox holding folder names, roles and server version markers — no messages, subjects, addresses or bodies; sealed with AES-256-GCM under a key derived from the same master key as the credential store (never a second password); fresh random nonce per write; tampering/swapping is detected, not merely discouraged; file and directory permissions restricted to the current user (Windows equivalent stated as current-user restrictive access); the whole cache directory is **excluded by the product itself** from data-directory version-control staging and from application backups/restore **before** any file is written — the application checks and guarantees this on every install by its own means (founder ruling, 2026-10-02), never by relying on operator machine setup; deleting a mailbox deletes its cache file; losing/unlocking the master key makes the cache rebuild live — it never contains the only copy of anything. No forensic erasure promise. | w5-integration/backend-lead (per ADR table); security-lead reviews the wording |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/troubleshooting.md` | "Your Sent/Drafts folder couldn't be identified" vs "your server has no such folder" — what each means and the fix (set the name in mailbox settings); what "Mail cache unavailable; using live access" means; busy/Refresh-vs-Retry basics as they touch cached lists; what to do when a cached list looks old (Refresh; ages are shown). No credential- or token-bearing log excerpts in instructions. | w5-integration/backend-lead (ADR-W4; per ADR table) |

No other user page is touched by W2's slice; if implementation reveals another affected page, the implementing lead adds its TODO here before landing.

## 11. Counterexamples — tests a careless implementation must fail

Each counterexample names the mutation a plausible wrong implementation survives; the §7 suite must die on each. CX-1 is the mandatory minimum of the dispatch brief.

| # | Counterexample | The careless mutation it kills | Test |
|---|---|---|---|
| CX-1 | **Untagged localized Sent folder.** LIST succeeds; an untagged `Elementos-Enviados` folder exists; no candidate matches. Expected: `unknown` + setting offered. The mutation: `absent`/empty-with-explanation whenever "LIST ran fine and nothing matched" — treating the finite candidate list as a proof of server layout. | Candidate-sweep-failure → absence | `TestDiscovery_NoCandidatesIsUnknownNeverAbsent` (DT-1 row 4) |
| CX-2 | **Override outranks special-use.** Stored `Archive` + a `\Sent`-tagged `Sent Items`. Expected: `Archive` used. The mutation: discovery result preferred because "the server knows best", silently overriding the operator. | Override demoted below discovery | `TestDiscovery_OverrideBeatsSpecialUse` |
| CX-3 | **Clear-to-automatic actually clears.** Empty string saved → next resolution is automatic. The mutation: treating the empty field as a literal folder name (impossible folder) or keeping the old value. | Reader-side semantics diverge from `persistConfig` | `TestDiscovery_EmptyClearsToAutomatic` |
| CX-4 | **UIDVALIDITY never fabricated.** A role never validated stores null epoch, not `0`. The mutation: zero-value struct serialization presenting 0 as a real epoch — and later epoch checks comparing "real" 0 against a folder whose epoch is 0. | Fabricated epoch | `TestCacheFile_RoundTripPreservesAllFields` (nil-stays-nil assertion) |
| CX-5 | **Cross-pair isolation.** Two pairs on one account with different Sent mappings; both resolve and cache. The mutation: cache/pool keyed by account key alone → pair B renders pair A's Sent. (The I-01 family.) | Account-keyed caching | `TestHeaderCache_CrossPairIsolation` + the I-01 budget tests |
| CX-6 | **Nonce freshness.** Identical payloads written twice must differ in ciphertext. The mutation: a deterministic or reused nonce (e.g. derived from pair ID) — every test still passes, the crypto is broken. | Static/reused nonce | `TestCacheFile_FreshNoncePerWrite` |
| CX-7 | **AAD actually binds.** Pair B's file at pair A's path, and a same-pair file from an older generation. The mutation: AAD omitted or bound only to a constant → foreign files decrypt happily. | Missing/constant AAD | `TestCacheFile_ForeignPairRefuses` / `..._ForeignGenerationRefuses` |
| CX-8 | **Reject-before-allocate.** A 2 GB claimed-size envelope. The mutation: reading the body into memory before checking the declared/actual length against the cap — works on every small test, OOMs on the hostile one. | Unbounded allocation on malformed input | `TestCacheFile_RejectsBeforeAllocation` (DT-2 rows 4–6) |
| CX-9 | **Superseded read publishes nothing (I-02).** The pause-and-release sequence of V-2. The mutation: invalidation-only ordering (delete-then-refill) without the revision check — the stale read repopulates unread flags and a fresh timestamp *after* the mutation. | Missing publication-revision fence | `TestHeaderCache_SupersededReadPublishesNothing` |
| CX-10 | **No background refresh while closed.** Panel closed across the 5-minute and 24-hour marks. The mutation: a `time.Ticker` "helpfully" keeping things fresh — every open-panel test passes; the closed-panel guarantee and the eight-socket world both break. | Polling timer | `TestCacheFile_ClosedPanelNeverTouchesServerOrDisk` + `TestCacheFile_TriggersExactlyFour` |
| CX-11 | **Retention is not extended by reads.** Cached rows read repeatedly at 29 minutes past close. The mutation: refreshing `lastAccessed` on read → immortal cache entries. | Read-extends-retention | `TestHeaderCache_RetentionDropsAfterThirtyMinutesClosed` (repeated-read variant) |
| CX-12 | **Budget overflow stays visible.** Rows sized past 4 MiB. The mutation: silent truncation of fields or silent eviction of live rows — the suite must require the typed budget refusal and the visible live-only outcome. | Silent truncation | `TestHeaderCache_BudgetOverflowIsVisibleNotTruncated` (DT-3) |
| CX-13 | **Unknown counts stay null.** Unknown role's total. The mutation: `int` zero-value serialization — "0" renders as a checked-empty folder. | Fabricated zero | `TestFolderCounts_UnknownRoleHasNullableTotal` |
| CX-14 | **Gate actually blocks.** Exclusion missing → first write must not happen. The mutation: the gate check implemented but its failure path logging-and-continuing. | Log-only gate | `TestMailCacheExclusionGate_BlocksWhenMissing` |
| CX-15 | **Leak markers never reach logs.** Marker-laden folder names/subjects through every path. The mutation: passing `err.Error()` (server text) into a log field "temporarily". | Raw upstream text in diagnostics | `TestW2Diagnostics_LeakMarkerSweep` |
| CX-16 | **Search never touches the cache.** A search run while a newest-50 snapshot exists. The mutation: answering search from the cached 50 rows (the "50 cached headers are not a complete server-search result" failure). | Cache-served search | `TestFolderSearch_NeverTouchesHeaderCache` |
| CX-17 | **Part fetch is part-wise and marker-free.** An addressed part on a message with a draft marker. The mutation: fetching the whole message for a part request, or numbering/exposing the draft-marker part as an attachment. | Whole-message fetch for a part request; draft part classified as attachment | `TestPartReader_FetchesOnlyAddressedPart` / `..._DraftMarkerExcludedFromIndices` |
| CX-18 | **Stale reference refused.** A ref from epoch N against a folder now at N+1 (the exact defect in today's `view.go`/`handleMailSeen` epoch-discard). The mutation: resolving the ref against the new epoch silently — today's behaviour. | Missing same-lease validation | `TestRefValidation_StaleEpochRefused` |
| CX-19 | **Counts refresh is stale-gated.** Counts 4:59 old at a trigger event. The mutation: refreshing unconditionally on every event (the w3 contradiction the register aligned away) or keeping them fresh via a repeating timer. | Unconditional/timer counts refresh | `TestFolderCounts_StaleRefreshesOnceManualAlwaysNoTimer` |
| CX-20 | **Joiner instrument honesty.** A discovery that joined a coalesced flight. The mutation: recording a socket for the joined flight (or fabricating another layer's sub-fields), inflating the socket sum w6 compares against the server counter. | Dishonest joiner record | `TestInstrument_JoinerRecordsZeroSocketAndMarker` |

---

## 12. Functional requirements and traceability

### 12.1 Functional requirements

| ID | Requirement |
|---|---|
| FR-W2-1 | The system MUST resolve the folder roles `inbox`, `sent`, `drafts` to server folder names at runtime; only these slugs are valid on any interface, and INBOX is fixed to the IMAP name `INBOX`. (R-3.1-1/3) |
| FR-W2-2 | The system MUST NOT create, subscribe, or otherwise mutate server folders during discovery. (R-3.2-4, R-3.1-4) |
| FR-W2-3 | Discovery MUST use SPECIAL-USE role attributes via LIST-EXTENDED when the server advertises them, and MUST fall back to ordinary probing when it does not; extension absence MUST NOT fail the mailbox. (R-3.2-2) |
| FR-W2-4 | Discovery MUST probe, in the fixed order `Sent, Sent Items, Sent Messages, [Gmail]/Sent Mail` (sent) and `Drafts, Draft, [Gmail]/Drafts` (drafts), and only a successful probe MAY establish a `fallback` mapping. (R-3.2-1) |
| FR-W2-5 | A non-empty stored `sent_folder_name`/`drafts_folder_name` MUST override discovery for its role, and a failing override MUST surface a settings warning rather than a silent substitution or silent ignoring. (R-3.3-1) |
| FR-W2-6 | An empty or absent stored override MUST mean automatic; omitted fields on update MUST preserve; the reader semantics MUST match `persistConfig`'s writer semantics exactly. (R-3.3-2, O-4) |
| FR-W2-7 | The system MUST treat every non-empty stored override as operator intent regardless of provenance, never discard it silently, and never classify it by inspecting its content. (R-3.3-3) |
| FR-W2-8 | The system MUST report `availability=unknown` — with the override setting offered — for any role it cannot resolve, and MUST reserve `absent` for discovery-completed-plus-all-candidates-structurally-missing; all network/auth/TLS/timeout/permission failures MUST leave roles unknown (or fail the read), never empty. (§3.4) |
| FR-W2-9 | A missing or failing INBOX MUST fail the mailbox read with a safe transport class on every surface — never render as a healthy empty account. (§3.4) |
| FR-W2-10 | With multiple candidates and no still-valid saved mapping, the system MUST surface the ambiguity and MUST refuse send/draft destinations rather than pick arbitrarily; a still-valid saved mapping MUST stand. (R-3.5-1/2/3) |
| FR-W2-11 | The system MUST persist per-mailbox folder metadata (resolved names, roles, sources, nullable UIDVALIDITY, availability, schema/config generation, transport, last-validated time) in exactly one encrypted file per mailbox, ≤64 KiB payload budget, and MUST refresh it only on the four §3.6 triggers — never on a timer, never while the panel is closed (dirty-mark and defer only). (§3.6) |
| FR-W2-12 | The cache envelope MUST use AES-256-GCM with a fresh cryptographically random nonce per write, purpose-separated keys from `credentials.Store.DeriveSubkey`, AAD binding envelope purpose + schema version + pair identity + config generation + transport, and atomic ciphertext-only replacement; it MUST reject oversized/unsupported/malformed envelopes before unbounded allocation and MUST NEVER produce or accept unauthenticated plaintext. (§3.7) |
| FR-W2-13 | Cache files MUST live under `<OmnipusHomeDir()>/mail-cache/<opaque-pair-id>/` with 0700 directory / 0600 file permissions on Unix and current-user restrictive ACLs on Windows, outside workspace/agent-visible trees. (§3.8) |
| FR-W2-14 | The first cache write MUST be gated by **the product's own exclusion guarantee** (founder Q-A): the in-process staging-exclusion evaluation (E-1 — `git check-ignore`-equivalent semantics evaluated in product code, **never** shelling out to `git` on this security-critical path) **and** the product's backup-walker skip (E-2) MUST both hold, checked by the product itself on every install; either failing MUST refuse disk writes and run live-only with the visible `cache_unavailable` notice — an honest product-enforced safety outcome, never a machine-setup excuse. A pre-tracked cache path counts as not excluded (E-3). (§3.8 gate; register row 18) |
| FR-W2-15 | The header cache MUST be memory-only, at most the newest 50 envelope+flag rows per role per mailbox, five-minute freshness with exactly one background refresh per eligible event, dropped 30 minutes after the panel was last open, within the 4 MiB global reusable-metadata budget, and MUST never serve or store search results or pages beyond the newest-50 window. (§3.9) |
| FR-W2-16 | Every cache publication (memory rows, disk snapshot, timestamps, counts) MUST be gated by the publication-revision check: a read superseded by a newer revision publishes nothing. (§3.10) |
| FR-W2-17 | Every §3.11 invalidation event MUST discard exactly what its row specifies, on both cache layers; stale rows MUST render labelled with their age, never as fresh; retention MUST NOT be extended by reads. (§3.11) |
| FR-W2-18 | Diagnostics from this package MUST carry only opaque pair/generation identifiers, safe classes, durations and hit/miss/socket counts, and MUST never carry folder names, subjects, addresses, Message-IDs, credentials, or raw upstream error text. (§3.12) |
| FR-W2-19 | W2 MUST NOT edit `transport.go`, any `contracts/` file, any `pkg/gateway/` file, or any test file; all cross-package interaction goes through the §4 interface freeze. (§4) |
| FR-W2-20 | Folder search MUST match SUBJECT and FROM/TO substrings **server-side** (IMAP header search; founder Q-D=A), MUST run inside the 25/200 bounds with `view_limit_reached` beyond 200, MUST issue cursors binding pair/generation/folder/version/query/delivered-count and MUST refuse stale cursors with the visible typed 409 reset; search MUST NOT read from or write to the header cache, MUST use no local index, and MUST obey the read-only and revision rules (R-3.2-4, R-3.13-5). (§3.13; register row 4) |
| FR-W2-21 | The single-part reader MUST fetch only the addressed part via lease-bound BODY.PEEK part fetch (partial permitted for text/markdown; whole part for image/PDF), MUST exclude the Omnipus draft-marker part from leaf numbering, MUST resolve `part_index` deterministically under one epoch, and MUST be transient — nothing written server-side, nothing retained, no package byte cache. (§3.14; register row 14) |
| FR-W2-22 | The MIME structure classifier MUST be the one walker (no second implementation anywhere), MUST derive `has_attachments` from the walked structure with `false` as established absence, MUST exclude the draft-marker part, and MUST be deterministic for a given structure under one epoch; the indicator alone rides the §3.9 allowed flag set — bytes and descriptor lists never enter any cache. (§3.15; register row 15) |
| FR-W2-23 | Every `view.go` operation receiving a message reference MUST validate it against the live folder epoch and configuration generation on the same lease (via W1's published same-lease validation capability) before any server command uses it, MUST refuse a stale/mismatched reference with the visible typed stale-reference result requesting one view reset, and MUST never mint references (issuance is w5-integration's). (§3.16; register row 16) |
| FR-W2-24 | Folder counts MUST be memory-only with their own freshness metadata, MUST refresh on a trigger only when absent or older than 5 minutes (manual Refresh always), MUST honour exactly the four triggers, MUST NOT use a repeating timer, and MUST preserve prior counts with a visible error on a failed refresh without resetting the timestamp. (§3.9 counts block; register row 21; founder Q-C=A) |
| FR-W2-25 | This package's discovery, cache-read and cache-publication operations MUST emit their sub-fields of the w6-frozen instrument record (source, hit, duration, outcome), MUST apply the joiner rule (`socket_count=0` + shared-flight marker on a coalesced join), and MUST NOT fabricate sub-fields owned by another layer. (§3.17; register row 17) |

### 12.2 Traceability matrix

| Requirement | User story | BDD scenarios | Tests (§7) |
|---|---|---|---|
| FR-W2-1 | US-1 | D-1, D-2, D-3 | `TestDiscovery_SpecialUseResolvesBeforeFallback`, fallback-order test |
| FR-W2-2 | US-1 | D-5 | `TestDiscovery_NeverCreatesFolders` |
| FR-W2-3 | US-1 | D-1, D-3 | `TestDiscovery_UnsupportedExtensionDegradesPerRole` |
| FR-W2-4 | US-1 | D-2, D-4 | `TestDiscovery_FallbackOrderedProbeStopsAtFirstSuccess`, CX-1 test |
| FR-W2-5 | US-2 | O-1, O-2 | `TestDiscovery_OverrideBeatsSpecialUse`, `..._StaleOverrideWarnsNotSwaps` (CX-2) |
| FR-W2-6 | US-2 | O-3, O-4 | `TestDiscovery_EmptyClearsToAutomatic` (CX-3), omitted-keeps test |
| FR-W2-7 | US-2 | O-5 | `TestDiscovery_OverrideBeatsSpecialUse` (provenance variant) |
| FR-W2-8 | US-1, US-3 | D-4, U-1, U-2, U-3, U-5 | CX-1 test, `TestDiscovery_ProbeTimeoutIsUnknownNotAbsent`, nullable-total test (CX-13) |
| FR-W2-9 | US-3 | U-4 | `TestDiscovery_MissingInboxIsFatalEvenForNonExistent` |
| FR-W2-10 | US-4 | A-1, A-2, A-3 | `TestDiscovery_AmbiguousSpecialUseAsks`, saved-mapping test, write-refusal test |
| FR-W2-11 | US-5 | F-1–F-5 | `TestCacheFile_TriggersExactlyFour`, closed-panel test (CX-10), round-trip test (CX-4) |
| FR-W2-12 | US-6 | F-6–F-11 | cache_file_test.go full set (CX-6, CX-7, CX-8) |
| FR-W2-13 | US-6, US-7 | F-1, G-1 | `TestCacheFile_PermissionsUnix` + Windows CI leg |
| FR-W2-14 | US-7 | G-1, G-2, G-3 | exclusion-gate tests (CX-14), staging/backup end-to-end |
| FR-W2-15 | US-8 | H-1–H-6 | header_cache_test.go full set (CX-11, CX-12) |
| FR-W2-16 | US-9 | V-2 | `TestHeaderCache_SupersededReadPublishesNothing` (CX-9) |
| FR-W2-17 | US-9 | V-1, V-3–V-6 | epoch, removal-race, reconfig, retention tests (CX-5, CX-11) |
| FR-W2-18 | US-10 | L-1 | `TestW2Diagnostics_LeakMarkerSweep` (CX-15) |
| FR-W2-19 | (process rule) | — | verified by review/`detect_changes`, not a test |
| FR-W2-20 | US-11 | S-1, S-2, S-3, S-4 | `TestFolderSearch_SubjectAndAddressServerSide`, `..._Bounds25And200`, `..._StaleCursorTypedReset`, `..._NeverTouchesHeaderCache` (CX-16) |
| FR-W2-21 | US-12 | P-1, P-2 | `TestPartReader_FetchesOnlyAddressedPart`, `..._DraftMarkerExcludedFromIndices` (CX-17) |
| FR-W2-22 | US-12 | P-2, P-3 | `TestClassifier_HasAttachmentsEstablishedAbsence`, `..._DeterministicSameEpoch` (CX-17) |
| FR-W2-23 | US-13 | R-1, R-2 | `TestRefValidation_StaleEpochRefused`, `..._ForeignGenerationRefused` (CX-18) |
| FR-W2-24 | US-14 | C-1, C-2, C-3 | `TestFolderCounts_FreshServedWithoutDial`, `..._StaleRefreshesOnceManualAlwaysNoTimer`, `..._FailedRefreshPreservesAndLabels` (CX-19) |
| FR-W2-25 | US-15 | L-2 | `TestInstrument_CacheAndDiscoverySubFields`, `TestInstrument_JoinerRecordsZeroSocketAndMarker` (CX-20) |

**Coverage check:** every FR traces to at least one story, scenario and test; every §6 scenario traces to at least one US (its `Traces to` lines) and appears in a test row; every §5 acceptance scenario number is covered by at least one scenario in §6.

## 13. Open questions (each marked **decided** or **still open**, with its owner)

- **OQ-1 — Who mints the opaque pair ID, and where does it live? — DECIDED** (register row 12, correction round 2026-10-02): **w5-integration mints a random 128-bit value per pair, stored beside the pair's config entry** — this spec's former option (a), as recommended; deleted with the pair via the removal cascade. Deterministic derivation and the watcher's filename sanitizer remain rejected. W2 consumes the ID opaquely (§3.8, §4.2). Owner of the implementation: w5-integration.
- **OQ-2 — Durable serialization of the configuration generation — DECIDED** (register row 12, correction round 2026-10-02): **generation = a non-secret fingerprint of the canonical pair identity plus the persisted config epoch** (w1 OQ-1 option C) — it catches endpoint/credential/override changes *and* invisible re-saves, matching the ADR's "reconfiguration/removal increments the generation". **This spec's former plain-fingerprint recommendation is superseded** (it could not distinguish a re-saved identity); w5's MC-17 derive-from-credential-material fingerprint is rejected (rotation orphans caches; derives a linkable value from key material). w5-integration implements; **W2 treats the value as opaque** (§4.2). Owner: w5-integration, security-lead review.
- **OQ-3 — LIST-EXTENDED/SPECIAL-USE capability detection mechanics — STILL OPEN** (owner: backend-lead at implementation; default applies). Verified available: the go-imap client exposes the server capability set (the `imap.CapUIDPlus` precedent in `view.go::DeleteDraftStatus`), and `startViewIMAPRaw` can inject arbitrary capability sets for tests. Unverified: whether specific servers advertise `SPECIAL-USE` post-auth only. Default: detect from the post-auth capability set; treat both names as required for the attribute path; no pre-auth CAPABILITY round-trip; the DT-1 dataset covers the absent/partial cases.
- **OQ-4 — Long-term owner of the data-dir ignore rule — DISSOLVED** (founder Q-A, 2026-10-02): the question presupposed an external deployment-owned ignore rule; that dependency is withdrawn with the machine-setup repository named in it — personal configuration is not a design input. **The product owns the exclusion by its own means** (§3.8: the E-1 in-process evaluation + the E-2 backup-skip), so there is no external ownership to assign. No follow-up decision is required for this feature.
- **OQ-5 — Old backup archives containing cache files — STILL OPEN** (owner: founder if the theoretical window matters; default: none). With E-2 landed, new archives exclude the directory, and the gate prevents creation before it; a hypothetically restored old cache file is §3.11's foreign-generation rejection (it simply refuses to open). No pruning mechanism is specified.
- **OQ-6 — Does `email-watch/` get the same treatment? — DECIDED** (founder Q-B=A, 2026-10-02): **exclude AND purge on mailbox removal** — this spec's former exclude-only default is superseded. Routing (§3.8 E-5, §8): the purge rides w5-integration's removal cascade (`deleteAgentMailbox`), the exclusion rides the §3.8 product-owned guarantee; the watcher file itself is w1's. Not W2's code; recorded because the decision closes the question this spec raised.

---

## 14. Assumptions (explicit, reviewer-challengeable)

- **A-1.** The ADR is the design authority and its correction round is complete; where this spec restates an ADR rule, the ADR wins on any divergence — except where the landing-order register explicitly supersedes an ADR proposal (e.g. row 3's five-value `mapping_source` over the ADR's four), in which case the register governs. This spec adds no new founder-level decision; it applies the recorded ones.
- **A-2.** W1's pool/coalescing interfaces are frozen in w1's §3.1 (register row 9); W2 may rewire `view.go` only against that freeze, built in Wave C. Until Wave B's artifacts merge, W2 builds below the wire boundary and reports any wire-dependent work as blocked (register §5.3) — never stubs.
- **A-3.** The exclusion gate (FR-W2-14) is implementable in-process: the mechanism is **decided** (register row 18; grill M-8 resolved) — the product reads and evaluates the data directory's ignore state itself with `git check-ignore`-equivalent semantics and never spawns `git` on this security-critical path (Hard Constraint #2); the gate's *refuse-and-notify* behaviour is the tested contract.
- **A-4.** The 13 supplied mailboxes are the measurement population for later performance evidence; this package's tests use only the in-memory fake server and never touch live providers.
- **A-5.** The F7/F1 wire fields (`has_attachments`, nullable `date`) land through the ADR-W0 contracts role (Wave B) independently of this package's code. **Correction-round change:** this spec no longer merely "admits" `has_attachments` — it **derives** it (FR-W2-22, register row 15); the nullable `date` is admitted into the cached field set as nullable, never a fabricated zero date (the CX-4 family).

## 15. Evidence table

Certainty applies to the claim in each row. **Verified** = read or run in this task, in this checkout (`docs/adr-mail-live-access`; original authoring @ `fc4c8bf6d`, correction round @ `a81b38921` and this round's commits); **Inferred** = architectural consequence, reasoned not run; **Unknown** = genuine gap named rather than filled.

| Claim | Evidence | Certainty |
|---|---|---|
| Branch tip and clean start for this spec | `git branch --show-current` → `docs/adr-mail-live-access`; original tip `fc4c8bf6d`; correction round started at `a81b38921` (the landing-order register's commit); `git status --porcelain` shows only a foreign untracked review file, untouched | Verified |
| The interface-ownership corrections applied (rows 1, 3, 4, 6, 10, 12, 13, 14, 15, 16, 17, 18, 19, 21, 23, 24) and the deadlock ruling | `docs/internal/specs/mail-live-access-landing-order.md` §2 register, §4 waves, §5 ruling — read in full this task; every ownership sentence in §3/§4/§7/§8 cites its row | Verified |
| This spec's grill findings (C-1, I-1..I-6, M-1..M-3, M-4, M-5, M-7, M-8, M-10) and their required corrections | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-1.md` read in full this task; each finding's resolution is visible at its cited section (see the correction-round map in §13's decided entries, §3, §4.1/4.2, §7, §9) | Verified |
| Founder rulings Q-A/Q-B/Q-C/Q-D/Q-E and the earlier Q1=A/Q2=B/Q3=A/Q4=A/Q5=A, #1170–#1175, the 2026-10-02 preview/download update | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/mail-feature-decisions.md` read in full this task; Q-A/E-1..E-5 (§3.8), Q-B/E-5 (§3.8), Q-C/§3.9 counts block, Q-D/§3.13, Q-E/§8 (no touchpoint, recorded) | Verified |
| ADR sections this spec restates: P1.1 limits, P1.3 discovery/invalidation + publication revision, P2.2/P2.3 (Phase-2 boundary), filesystem requirements, wire-format impact, work-package table, correction-round ownership paragraph | ADR-20261001 sections read this task (P1.1–P1.4, P2.2–P2.3, Security/Filesystem, Wire-format impact, Affected components + correction-round ownership paragraph); the five-value `mapping_source` supersedes the ADR's four-value proposal per register row 3 | Verified |
| W2's primary file and its exact current behaviour (`folderNameFor` literal defaults, `FolderCounts` structural-absence softening, `ReadFolderPage` search/clamp/truncation, `MailRow` field set) | `pkg/email/view.go::folderNameFor`, `::FolderCounts`, `::isNonexistentFolder`, `::ReadFolderPage`, `::fetchMailRows`, `::MailRow`, `::ReadView`, `::ResolveRef`, `::MarkSeenIn`, `::DeleteDraftStatus` — full file read in the original authoring session; the epoch-comparison gap at `::ReadView`/`::ResolveRef` is the register row-16 seam | Verified |
| The raw-text seam this spec must not worsen, and its class enum | `pkg/email/watcher.go::classifyMailError` (the closed class enum, verified this correction round; `::Cycle` calls `recordFailure(classifyMailError(err), err.Error())`) — the fix itself is w1's (register row 19) | Verified |
| The crash-consistency test's production seam precedent | `pkg/credentials/store_lock_test.go::HoldFirstWrite` on the package-local `writeFileAtomicFn` var — verified this correction round; `cache_file.go` must provide the same indirection (grill M-5) | Verified |
| Overrides persist exactly as the ADR's correction states (omitted keeps, empty deletes, non-empty stores) | `pkg/gateway/rest_mailbox.go::restAPISetAgentMailbox.persistConfig` + `pkg/config/config.go::MailboxConfig` field definitions with `omitempty` | Verified |
| No exported arbitrary-file encryption helper exists; `DeriveSubkey` is the sanctioned seam | `pkg/credentials/store.go::Store.DeriveSubkey`, `::encrypt`, `::decrypt`, `aadFor`, `::Store.Close`; `pkg/credentials/CLAUDE.md` | Verified |
| The socket/budget/coalescing seams and the I-01 flight-key gap | `pkg/email/mail_budget.go::MailBudgetRequest.flightKey`, `::call`, `::flightContext`, `::SharedMailBudget`; `pkg/gateway/rest_mail_budget.go::mailBudgetWrap`, `::mailRetryParam` | Verified |
| The backup walker and restore skip only their named exclusions today (E-2's product-code gap) | `pkg/gateway/rest_settings.go::createTarGz` (top-level `logs`/`backups` skip), `::extractTarGz` + `maxRestoreFileSize` | Verified |
| Mailbox deletion removes config + credentials only; no file deletion (E-4's gap) | `pkg/gateway/rest_mailbox.go::deleteAgentMailbox` (config-map delete, two `removeStoredCredential` calls, `logsafeError` pattern) | Verified |
| The product-owned exclusion requirement and its enforcement split (E-1 product evaluation / E-2 product skip; no external dependency) | Founder ruling Q-A in `mail-feature-decisions.md` (read this task); register row 18's unified condition and product-terms statement; the former external-repo citations in this spec are withdrawn (§3.8's correction note) | Verified (the requirement and its texts); runtime behaviour Unknown until Wave E |
| The data-root resolver is `pkg/config/home.go::OmnipusHomeDir` (env → home → private temp) | `pkg/config/home.go::OmnipusHomeDir` header + resolution order read | Verified |
| Current wire schemas carry no availability/uidvalidity/metadata fields | `contracts/components/schemas/MailFolder.yaml`, `::MailFolderList.yaml` read in full (integer `total` required; bare folders array) | Verified |
| The named existing tests exist with the described oracles; a capability-set harness variant exists | `pkg/email/view_missing_folder_test.go` (all three tests read), `pkg/email/imapserver_test.go::startMemIMAP`, `pkg/email/view_test.go::startViewIMAP`/`::startViewIMAPRaw(t, msgs, caps)` | Verified |
| Gateway handlers gate through the shared budget with the operation names this spec consumes | `pkg/gateway/rest_mail_read.go::handleMailFolders` (`listMailFolders`), `::handleMailList` (`listMailMessages`, limit/before_uid params); `::handleMailSeen` discards the resolve epoch (register row 16's wiring gap) | Verified |
| No `mail-cache` code exists anywhere yet | `grep -rn "mail-cache\|mailCache\|MailCache" pkg/ --include="*.go"` → zero hits | Verified |
| Blast-radius rows (callers of `folderNameFor`; handler/SPA consumers) | Symbol-level grep within `view.go` (six internal callers read); gateway/SPA consumers cited from the ADR's evidence table + handler reads. No GitNexus index consulted in either session — no `impact` run claimed | Inferred (medium-high confidence; one-writer-per-file ownership bounds the risk) |
| Discovery/permission behaviour on Windows (ACLs) and real-provider capability advertisement | Not examined — no Windows machine, no live provider in this task | Unknown (named in §9's DoD with its register row-23 instrument; ADR lists the same gap) |
| Pair-ID and generation constructions | **Decided**, no longer open: register row 12 (read this task) — minted 128-bit pair ID; generation = fingerprint + persisted config epoch; this spec's OQ-1/OQ-2 recommendations recorded as superseded where they differ | Verified (the decision); implementation Inferred until Wave D |
| **Self-check** | Re-read the corrected spec end-to-end after the final edit; re-ran the personal-reference sweep (`grep` for `autocommit|gitleaks|launchd|omnipus-agent-os|state-autocommit` → only this evidence row's own withdrawal note remains, no dependency/reference/assumption anywhere in scope, risk, open questions or DoD); verified every FR in §12.1 (FR-W2-1..25) appears in §12.2's matrix and every §6 scenario's `Traces to` points at a §5 acceptance-scenario number (US-1..US-15); verified §7 names only files that are new (proposed) or already exist as stated; confirmed the register's publisher assignments are each named in §4 (no second implementation, no stub, W0 stated as a role not a file); confirmed no founder decision is reopened (Q-A..Q-E applied as recorded; OQ-1/2/4/6 marked decided with their sources); fixed one duplicated DT-4 header introduced mid-edit (`grep` found exactly the duplicate; removed); commits made incrementally after each corrected section, each authored solely as Daniel Piatkowski with no co-author trailer; no production code, contract file, or test file touched; no test weakened, no limit widened, no stub type created, no silent scope change beyond the register-assigned additions | Verified (artifact and scope checks above) |

---

## 16. Skills acknowledgement

skills: omnipus-shared-rules, plan-spec (correction round, 2026-10-02; the original authoring session also loaded omnipus-backend-rules)
