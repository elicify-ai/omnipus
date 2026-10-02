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
| `present(name, uidvalidity, source)` | A folder was resolved and validated to exist; `source` ∈ `override | special_use | fallback | saved`. |
| `absent` | Genuine, structurally confirmed absence (§3.4 evidence bar) — only reachable for sent/drafts, never inbox. |
| `unknown(reason)` | Not resolved *and* not proven absent: discovery failed, was unsupported, or found nothing it could prove (M-01). |

Normative procedure, in order (each step only when the previous produced no result for the role):

1. **Override wins outright.** If the mailbox has a non-empty stored `sent_folder_name` / `drafts_folder_name`, that name *is* the candidate; discovery probes it for existence + UIDVALIDITY. On success: `present(name, source=override)` — no server enumeration is needed for that role. On structural `[NONEXISTENT]`: the override is reported as an actionable settings warning (ADR P1.3 "saved role now missing" row) — it is never silently replaced by another folder, and it is never silently ignored. On any other probe error: `unknown(reason)`, with the override still shown in settings.
2. **Saved mapping, still valid.** If the encrypted metadata holds a mapping for the role whose source is `special_use` or `fallback`, and a validation probe (or a fresh discovery within this same operation) confirms the folder still exists with the recorded UIDVALIDITY, the saved mapping stands (`source=saved`). A `saved` entry the server no longer confirms is discarded per §3.11 and rediscovery runs — exactly one immediate coalesced rediscovery while the panel is open (ADR P1.3).
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
| `config_generation` | Opaque generation token supplied by the caller (W4's ownership; open question OQ-2) | The generation active when this snapshot was written |
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

### 3.8 File location, permissions, and the exclusion gate (disk cache is BLOCKED until exclusions deploy)

**Location.** The cache lives under the resolved application data root, derived through `pkg/config/home.go::OmnipusHomeDir()` — never an inline `~/.omnipus` join, never a hard-coded Mac path. Shape:

```
<OmnipusHomeDir()>/mail-cache/<opaque-pair-id>/folders.enc
```

- `<opaque-pair-id>` is an unambiguous opaque identifier for the (agent, workspace) pair — **not** an email address, **not** a folder name, **not** the watcher's lossy filename sanitizer (which can collide two pairs onto one file; ADR filesystem table). Who mints it is open question OQ-1; the interface (§4) treats it as an opaque string supplied by the caller.
- Outside `workspaces/` and agent-visible mounts: a generic agent file tool must never become a decryption route (ADR filesystem table; the path joins existing agent filesystem/shell policy wiring — W4's integration).

**Permissions.** The `mail-cache/` directory is created **0700** (owner-only) and every file **0600**, on Unix (Linux/macOS). On **Windows** there is no chmod: the equivalent is **current-user restrictive access** — the directory and files are created with ACLs granting access to the current user only (no inherited broad grants, no Everyone/Users entries). The claim in user docs is "restricted to the current user", never a chmod-only assertion; Windows behaviour needs its own executed proof (the ADR lists this as an explicit future-proof obligation — carried in §9's DoD as part of the gate evidence, not claimed here).

**The exclusion gate — what the autocommit trace established (all verified in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/receipts/autocommit-trace.md`):**

- The data-directory auto-commit job is **machine-level deployment, not product code**: a launchd agent (`com.omnipus.state-autocommit`, 15-minute interval) runs a bash script that stages with a **bare `git add -A`** in the data directory, commits, and pushes to a private GitHub backup remote. Nothing in the product repo installs or configures it.
- Exclusion from that job is decided **solely** by the data repo's deny-list `.gitignore` (its own header: *"Everything not listed here IS committed"*), written by an external machine-deployment repo's script.
- A new `mail-cache/` directory is **not** in that deny-list today: `git check-ignore` probes in the trace show `mail-cache/` unmatched, hence staged by the next `git add -A`.
- The application's own tar backup captures it too: `pkg/gateway/rest_settings.go::createTarGz` skips only top-level `logs` and `backups` (verified this checkout), and `extractTarGz` would re-materialize it on restore.
- Nothing deletes it: `deleteAgentMailbox` touches config + credentials only (verified this checkout); agent deletion removes entity + SOUL records only.
- Current live state (the trace's caveat): the job is **unloaded** right now and its last run was blocked by a gitleaks pre-commit hook, with 22,239 dirty entries pending — but the plist and generator are present and idempotent, so `git add -A` staging remains the operative rule the design must satisfy. Bonus risk: **encrypted blobs are high-entropy**, exactly what secret scanners false-positive on — un-ignored cache files could freeze the whole backup job the way the current 30-finding block does.

**The exact exclusion rules required (each before the first cache write ships on any install):**

| # | Rule | Exact location |
|---|---|---|
| E-1 | Add a `mail-cache/` deny rule (a directory rule covers all nesting below it) to the data repo's `.gitignore` — in practice, to the `.gitignore` heredoc inside the generator script — and re-run that script on the machine (it refreshes the file idempotently): under its "regenerable / high-churn" section, the line `mail-cache/` (adjust to the final directory name if §3.8's name changes) | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-agent-os/scripts/35-omnipus-snapshot-repo.sh` (the `GITIGNORE` heredoc), which owns `/Users/danielpiatkowski/.omnipus/.gitignore` |
| E-2 | Extend the backup walker's top-level skip to include `mail-cache`: `createTarGz`'s skip condition becomes `logs` ∪ `backups` ∪ `mail-cache`. Once new archives exclude the directory, restore is covered automatically for new archives (old archives are a §3.11 restore-restore decision, open question OQ-5) | `pkg/gateway/rest_settings.go::createTarGz` (W4's edit) |
| E-3 | Verify cache files are not already **tracked** before relying on the ignore rule — ignoring a tracked file does not untrack it (the trace demonstrates the mechanism with a throwaway repo). If any install ever committed a cache file, remediation is a runbook (`git rm -r --cached` + commit; history rewrite is a founder decision), never a product-code fix | Deployment/runbook; verified at first-write time (the gate below) |
| E-4 | The mailbox-removal cascade gains cache deletion (best-effort unlink, `logsafeError` + continue — the handler's existing orphan-tolerant pattern), so removal does not leave orphan files the way the existing `email-watch/` state does | `pkg/gateway/rest_mailbox.go::deleteAgentMailbox` (W4's edit; W2 supplies the delete primitive) |

**The gate (hard, founder-briefed): enabling the disk cache is BLOCKED until E-1 and E-2 are deployed.** Concretely, W2's first-write path performs a runtime exclusion check before the very first write on any install: probe that the cache directory resolves as ignored by the data repository's ignore rules (the `git check-ignore` mechanism the trace used), and confirm the backup skip (E-2) is present in the running build. If either fails, the disk cache stays **disabled on that install**: the mailbox runs live-only with a visible `cache_unavailable` notice (the ADR's wire metadata `notice_code`), and nothing is written to disk. The check is cheap, runs once per process per pair, and fails visible — never a silent "skip the check, write anyway".

---
