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
