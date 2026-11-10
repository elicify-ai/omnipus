# Feature Specification: Email — HTML signatures, workspace Mail panel, agent draft approval

**Created**: 2026-09-25
**Status:** Approved
**Revision**: security sign-off absorption, 2026-09-26 — absorbs security-lead's PRE-IMPLEMENTATION SIGN-OFF WITH CONDITIONS (F1–F11 = C1–C11, dated 2026-09-26, source `coordination/logs/email-security-signoff.md`): **C1** resolves the §2.3 ↔ MC-10(1) route-prefix contradiction to the non-API `/mail-preview/…` prefix everywhere — the two serve routes + the image proxy move to §2.3a (token-only, outside `/api/v1`), the mint stays session-authenticated under `/api/v1` and gains 429 (C10); **C2** pins the D33/FR-038 attachment wording to what `pkg/tools/resolvepath.go::ResolvePath` actually enforces (open-read rule — unconditional secret carve-outs refused at resolve **and** re-verified at I/O time; **no outside-workspace gate**, matching D33's "no extra guard rails"); **C3–C11** land as binding constraints **MC-37–MC-45** with tests **T72–T80**, and the sign-off's normative header set + iframe attributes are copied verbatim as the MC-10 subsection in §5.3; prior revision: founder-directed correction, 2026-09-26 — applies founder decisions D35–D38: D35 adds the UI-prototype gate before the frontend build (§17), D36 replaces the "E2E has no mail server" stance with the built-in fake IMAP/SMTP server (§7), D37 moves UAT to a separate instance against GreenMail in Docker plus a post-landing live smoke (T44, §17), D38 adds the "read by agent" tag via the `$OmnipusAgentRead` IMAP keyword (US-6, FR-039/MC-36, B-53..B-55, T68..T71); prior revision: founder-directed correction, 2026-09-26 — applies D31–D34 (attachments ride exactly the send policy — D33; nothing about mail is automatic — D31; drainer-test outcome recorded — D32; work-branch move — D34); prior revision: fix round 2, 2026-09-25 — folds grill round-2 findings (40) and founder decisions D27–D30; D27 supersedes D20's agent-turn switch (removed — the switch never ships)
**Input**: `docs/internal/specs/spec-email-mail-view.md` (interview-me output, Decisions Log D1–D38; later IDs override earlier ones)
**Round-1 review**: `docs/internal/specs/email-mail-view-spec-review.md` (BLOCK, 32 findings — every finding is dispositioned in §20)
**Round-2 review**: `docs/internal/specs/email-mail-view-spec-review-round2.md` (BLOCK, 40 findings — every finding is dispositioned in §21)
**Work branch**: `feature/email-mail` (D34 — cut from `release/v0.1.1` @ b6a6f8c87, carrying the spec history of `feat/email-mail-view`) · Integration branch: `release/v0.1.1` (D1 — ships in v0.1.1)
**Canonical mailbox model**: ADR-033 — *Per-(Agent, Workspace) Email Mailboxes* — **amended inbound clause** (D29/R2-7): the architect writes the dated amendment in this branch; content specified in §19
**Related issues**: see §19 — closes #629, closes #631; delivers part of #42 (D21)

---

## 1. Summary and scope

The founder asked for three things (interview, "Request" section, verbatim): a configurable **HTML signature**
per mailbox, a **workspace Mail panel** ("mini outlook view", live from the mail server — nothing stored
locally) reusing the Library's look and components, and an **agent draft-approval flow**: the agent writes a
draft into the mailbox's Drafts folder and posts a link in chat that opens the draft in a preview side panel.

In scope (all on the existing ADR-033 pair model):

- One HTML signature **per (agent, workspace) mailbox**, edited in the existing mailbox panel with a live
  preview; a plain-text part is derived automatically (D2). The signature is **sanitized on save** with a
  dedicated signature policy (§5.3 MC-2) — operator HTML never ships raw into outbound mail.
- Agents write **Markdown**; the backend renders sanitized HTML + plain text (multipart/alternative) and
  appends the signature. Agents never touch raw HTML or the signature itself (D3).
- Recipients: **To/CC/BCC and recipient lists for humans and agents** — `send_email`, `reply` and the new
  draft tool all take address lists (D26).
- Every sent message is APPENDed to the mailbox's Sent folder (D7, D18 — always, on every provider, with no
  provider detection; see the duplication note under FR-006).
- A **Mail tab inside the workspace** (D4) opening a docked Mail panel beside chat plus a fullscreen pop-out
  (D11 — the Library's exact hosting shape), showing exactly **Inbox, Sent, Drafts** (D5), read **live over
  IMAP** with **no local copy of mail content** (D6, scoped per D21: session transcripts keep email text as
  every tool does; user docs say so plainly); the only reference metadata stored is the watcher UID state
  (FR-033) — nothing links Message-IDs to tasks or sessions anymore (round-2 MIN-010: the drainer, the only
  writer, is deleted).
- New agent tool `create_email_draft` (APPEND with `\Draft`) plus a **chat link** that opens the draft in a
  preview side panel (D8). The panel supports **view, edit, send and discard** (D12); To, subject and body
  are all editable (D23) and externally-created drafts are fully editable too, with the formatting loss
  stated (D24). Panel Send **is the approval** (D12). `send_email`/`reply` keep today's configurable policy
  (D15 — correction to D8's "unchanged approval" premise).
- The **mailbox drainer is deleted and replaced by a new-mail watcher** (D20, closes #631): the watcher never
  changes flags, never creates Board tasks, and — **D27 — never starts an agent turn**. **An email never
  starts an agent turn**: the mail tools are used actively by the agent inside turns started by humans, tasks
  or heartbeats; mail is not a trigger. **D31: agents handle mail only when asked, or through a heartbeat /
  scheduled task the operator configures ("check your inbox") — nothing about mail is automatic.** D20's
  per-mailbox switch "Let the agent handle new mail" is **removed** — it never ships. The watcher only
  advances its UID state and feeds the unread badge and the "last checked" state, refreshed every 60 s from
  the saved watcher state with no IMAP login (D29/R2-5).
- **"Read by agent" tag (D38)**: an agent reading a message with `read_message` marks it read (`\Seen`) and
  sets the `$OmnipusAgentRead` IMAP keyword in the same flag store; the Mail panel shows a small
  "read by agent" tag on messages the agent handled, so the human still sees what it did. When a mail server
  rejects custom keywords, the message is still marked read and simply shows no tag — never an error
  (FR-039). Nothing is marked read automatically; a human opening a message in the panel marks it read.
- **Attachments** (D28 for humans, **D33 for agents**): download attachments from any message; attach files
  when composing and when editing drafts — including drafts started in other mail programs, whose
  attachments are carried over unchanged, listed explicitly in the panel before send. **D33**: the agent
  tools (`send_email`, `reply`, `create_email_draft`) gain an optional `attachments` parameter of
  **workspace files**, governed by **exactly the same tool policy as the send itself** — no separate ask
  gate, no special caps beyond the general message limits already in this spec (§5.3 MC-32/MC-22/MC-27).
- Humans can **compose and send mail manually** from the Mail panel (D9), with To/CC/BCC and a Reply action
  (round-2 MIN-009) that pre-fills from the open message.
- The Mail panel refreshes every **30 seconds while the panel is open**, never in the background from the
  panel (D25); the watcher is the separate background poller; identical concurrent refreshes coalesce so
  N tabs cost one IMAP login per tick (D29/R2-9).
- Incoming HTML renders in a **sandboxed frame without scripts**, remote images **blocked by default** with a
  per-message "Load images" action (D13, D17) — the frame reuses the **Library-preview isolation posture**
  (path-confined prefix, no `'self'`, CSP `sandbox` directive, `base-uri 'none'`; D29/round-2 CRIT-001 —
  FR-019/MC-10). **Security-lead sign-off delivered 2026-09-26 — SIGN-OFF WITH CONDITIONS; C1–C11 absorbed in
  this revision (MC-10 normative block, MC-37–MC-45).**
- **Connection failures are never silent**: every mail-backed surface shows them in the panel, and they are
  logged with a clear message (D22) — with **bounded name-resolution retry, context-aware dials and
  per-mailbox backoff with jitter** (FR-037) so the dominant real failure mode cannot take the panel down
  silently. Per D30 the *cause* of the observed 2026-09-25 DNS-failure windows is unknown and outside this
  spec: it states resilience requirements, never a cause claim.

Out of scope for this spec:

- Any mailbox-model change (ADR-033 stands; cap, pairing, credentials, move semantics all unchanged).
- Server-side search UI in the Mail panel (the agent's `search_email` tool is unchanged).
- WebSocket push of new mail to the SPA — the panel polls per D25; the watcher drives the badge via its
  summary endpoint (A2).
- The remainder of #42 (threads view, Gmail setup wizard, "Test connection" action) — stays open with a note
  (D21); see §19.
- Agent folder tools beyond INBOX: agents keep reading INBOX only; Drafts/Sent reading stays a human-panel
  capability in this wave.

---

## 2. Contracts (wire-first — Hard Constraint #8)

Every byte below crosses the gateway/SPA boundary and is added to `contracts/` **before** any Go/TS code,
following the 5-step procedure (`CLAUDE.md`, "Contract regeneration"). Nothing in this section is written
until the schemas exist in `contracts/components/schemas/` and are referenced from `contracts/openapi.yaml`.

### 2.1 Changed schemas

| Schema | Change | Reason |
|---|---|---|
| `contracts/components/schemas/Mailbox.yaml` | New optional properties `signature_html` (string, ≤ 16,384 chars, default `""`), `sent_folder_name`, `drafts_folder_name` (string, default `""`) | D2 (signature), A1 (folder overrides — wired end-to-end per round-1 MIN-001) |
| `contracts/components/schemas/MailboxConfigureRequest.yaml` | The same three properties | Round-1 MIN-001: overrides must be settable (the schema is `additionalProperties: false`, so absent = not settable) |

`signature_html` is **configuration, not mail content** — storing it in `config.json` does not violate D6
(D6 scopes "mail content"; the signature is the user's own setting). The plain-text signature part is derived
at compose time, never stored (D2). The watcher's last-seen UID is **runtime state, not config** — it lives
in a per-mailbox state file under the data dir (reference metadata only: UIDs, counts, an error class; never
bodies — D6-permitted; see FR-033).

### 2.2 New schemas (all in `contracts/components/schemas/`)

| Schema | Purpose | Fields (types and nullability explicit — round-1 MIN-003) |
|---|---|---|
| `MailFolder` | One of the three D5 folders for one mailbox | `slug` (enum `inbox`\|`sent`\|`drafts`), `display_name` (string — the server's real folder name), `total` (int), `unread_count` (int \| null — **Inbox only, null elsewhere**, round-1 MIN-005) |
| `MailFolderList` | Folders for one mailbox | `folders: []MailFolder` |
| `MailMessageSummary` | Envelope row (list results) | `message_id` (string \| null — some inbound mail lacks one), `uid` (int), `uidvalidity` (int), `folder` (slug), `subject` (string), `from` (string), `from_name` (string \| null), `to` (string[]), `cc` (string[] — **D26**), `date` (RFC 3339), `seen` (bool), `is_draft` (bool — `\Draft` flag), `is_omnipus_draft` (bool — `X-Omnipus-Draft` header present, FR-029), `read_by_agent` (bool — the `$OmnipusAgentRead` IMAP keyword is present in the message's flag list, FR-039) |
| `MailMessagePage` | One page of envelopes | `messages: []MailMessageSummary`, `truncated` (bool), `next_before_uid` (int \| null — mirrors `SearchResult`'s explicit-truncation contract, `pkg/email/transport.go::SearchResult`) |
| `MailMessage` | Full message (read path) | All summary fields, plus `reply_to` (string \| null), `in_reply_to` (string \| null), `references` (string \| null), `body_text` (string — decoded plain text), `has_html` (bool), `bcc` (string[] \| null — returned only on the owner's own copies: drafts and Sent; §2.3 note), `attachments` ([]MailAttachment — D28), `body_markdown` (string \| null — the draft's editable source: the stored `text/markdown` part for an Omnipus draft whose `X-Omnipus-Render-Hash` matches the rendered text part; otherwise the **server-derived** Markdown (foreign draft, FR-030) with `markdown_lossy=true`), `markdown_lossy` (bool) |
| `MailAttachment` | Attachment descriptor on `MailMessage` (inbound + drafts, D28) | `part_index` (int), `filename` (string — sanitized for download: no path separators, edge case 21), `content_type` (string), `size_bytes` (int) |
| `MailAttachmentInput` | An attachment supplied for an outbound message (compose, draft build, panel send) | `filename` (string), `content_type` (string), `data_base64` (string, `format: byte`) |
| `CreateEmailDraftResult` | The `create_email_draft` tool result (crosses the gateway→SPA boundary — the chat tool-result renderer consumes the generated type; round-2 MIN-013) | `created` (bool), `message_id` (string), `uid` (int), `uidvalidity` (int), `chat_link` (string \| null), `chat_link_reason` (string \| null) — property names exactly as the tool emits them |
| `MailSendRequest` | Human manual send (D9) | `to` (string[], minItems 1), `cc`, `bcc` (string[], optional) — **maxItems 50 per list and ≤ 50 total across To/Cc/Bcc** (D29/R2-6, MC-27), `subject` (string), `body_markdown` (string), `in_reply_to` (string \| null — set by the panel Reply action; In-Reply-To/References carried through, MIN-009), `attachments` ([]MailAttachmentInput — ≤ 10 files, ≤ 25 MiB total, MC-32) |
| `MailSendResponse` | Send outcome | `message_id` (string), `sent_saved` (bool), `save_warning` (string \| null — set when the Sent APPEND failed after a successful SMTP send; never silent, D7), `draft_cleanup_warning` (string \| null — set when the draft could not be removed after a successful panel send, MAJ-009/MC-28; the `sent` field is dropped — it was always true on a 200, round-2 MIN-006) |
| `MailDraftUpdateRequest` | Panel edit of a draft (D12/D23) — now carries the staleness precondition (round-2 MAJ-008) | `to`, `cc`, `bcc` (address lists — D23: recipients editable), `subject`, `body_markdown`, `uidvalidity` (int), `uid` (int) of the viewed draft, `attachments` ([]MailAttachmentInput — new files added in the panel), `keep_attachment_parts` (int[] — part indices from the current copy's `MailMessage.attachments` to carry over; carry-over is server-side by part reference, FR-035) |
| `MailDraftSendRequest` | Panel send (D12) — **carries the exact content the human saw/edited plus a staleness precondition** (round-1 MAJ-002) | `to`, `cc`, `bcc`, `subject`, `body_markdown` (the displayed/edited content), `uidvalidity` (int), `uid` (int) of the viewed draft, `keep_attachment_parts` (int[], optional — default **carry all** of the current copy's attachments; the UI lists exactly what will be carried, D28) |
| `MailHtmlPreviewTokenRequest` | Mint a short-lived token for one message's HTML body | `workspace_id`, `agent_id`, `folder` (slug), `message_ref` (see §2.3 refs), `load_remote` (bool, default `false` — **D17**: blocked by default, "Load images" re-mints with `true`) |
| `MailHtmlPreviewTokenResponse` | Token payload | `token`, `expires_in_seconds` (mirrors Library `LibraryPreviewTokenResponse`) |
| `MailboxNewMailSummary` | Watcher-driven badge state per mailbox | `agent_id`, `unseen_total` (int — the mailbox's live IMAP UNSEEN count at cycle time; A11), `watcher_state` (`ok`\|`error`\|`backoff`), `last_error_class` (string \| null), `last_success_at` (RFC 3339 \| null), `last_seen_uid` (int \| null), `next_attempt_at` (RFC 3339 \| null — set when backing off; the badge shows "retrying at hh:mm", D29/R2-8) |
| `MailboxNewMailSummary` — note | Round-2 MAJ-019: `watcher_state` `ok` never lies about blindness | `last_success_at` + "last checked" UI make a silently-blind watcher visible; `last_seen_uid` makes UID advance observable |
| `MailSummaryList` | Workspace-scoped badge summary | `items: []MailboxNewMailSummary` |
| `ErrorResponse` | Reused for all 4xx/5xx bodies (existing schema) | — |

Not built: no `MailDraftCreateRequest` REST schema — humans do not create drafts in v1 (compose offers Send;
"save as draft" for humans is out of scope). Agent draft creation is a **tool**, not a REST endpoint (§2.4).

**Agent attachments (D33) — contract story.** The agent path needs **no new `contracts/` schema**: tool
parameters are not gateway/SPA wire bytes (§2.4 precedent — `send_email`/`reply` parameters carry no schema
either), and every SPA-consumed shape already exists. The parameter is defined in the tools' JSON schemas
(§2.4): an optional list of **workspace-file references** (strings), which the backend resolves server-side
through the same workspace-path resolution the generic file tools use
(`pkg/tools/resolvepath.go::ResolvePath`) into the same internal attachment inputs the human paths already
carry (`MailAttachmentInput` over the REST routes; `MailAttachment` descriptors already render on
`MailMessage.attachments`, so an agent-attached draft is listed in the approval panel with no UI change).
`CreateEmailDraftResult` is unchanged.

**Agent read marker (D38) — contract story.** `read_by_agent` is **derived, never stored by Omnipus**: on
every envelope or full-message fetch the mail server reports the message's flags, and the gateway sets the
field to `true` only when that flag list carries `$OmnipusAgentRead` (FR-039). The keyword is set by agent
`read_message` alone — never by the panel's seen endpoint, never by the watcher — and the mail server holds
it, so no Omnipus state is added and D6 is untouched. When the server rejects custom keywords, the gateway
reports `false`: no tag renders, and nothing errors (MC-36). Since `MailMessage` carries all summary fields,
the full-message path inherits the same field.

### 2.3 New REST endpoints (all session-authenticated, versioned under `/api/v1` — except the HTML-preview serve routes, which are token-only on the non-API `/mail-preview/` prefix, §2.3a)

Path convention follows the existing workspace-scoped pattern (`/workspaces/{id}/media`,
`contracts/openapi.yaml`): `{id}` = workspace, `{agentId}` = the mailbox-owning agent — the ADR-033 pair
rides in the path exactly as `GET /agents/{id}/mailboxes/{workspaceId}` does, mirrored. **Messages are
addressed folder-scoped** (round-1 MAJ-005, adopted as D21):

- `ref` = `uid:<uidvalidity>:<uid>` (what every list row carries — always resolvable) or
  `mid:<Message-ID>` (what chat links carry — stable across UID renumbering).
- `mid:` resolution searches **only the addressed folder**, among **non-`\Deleted` messages only**
  (round-2 MIN-005: a no-UIDPLUS server keeps the old copy of an edited draft in the folder with `\Deleted`
  and the same Message-ID), resolving multiple hits to the **highest UID** — never by INTERNALDATE, which
  APPEND may set from the message's own Date header, so "newest INTERNALDATE" can pick the stale copy.
  Message-ID validation: `<…@…>` shape, ≤ 998 bytes, no CR/LF (the value feeds an IMAP
  SEARCH string — bounds on shape and length are the actual defense; percent-encoded in the URL).
- Draft actions resolve only inside `drafts` (MC-13).

| Endpoint | Purpose | Status codes |
|---|---|---|
| `GET /workspaces/{id}/mail/{agentId}/folders` | Three D5 folders + counts (live IMAP) | 200 / 401 / 404 (workspace, agent, or no mailbox for the pair — `getAgentMailbox` precedent) / 502 (mail server unreachable, error class in body — MC-8) / 500 |
| `GET /workspaces/{id}/mail/{agentId}/folders/{folder}/messages?limit=&before_uid=` | Envelope page. The round-1 `unseen_only` query param is **dropped** (round-2 OBS-001: no UI or tool ever called it; the watcher's `unseen_total` is the only unread surface). `unread_count` is Inbox-only (§2.2) | 200 / 401 / 400 (unknown folder slug, limit out of range) / 404 / 502 / 500 |
| `GET /workspaces/{id}/mail/{agentId}/folders/{folder}/messages/{ref}` | Full message by folder-scoped ref. **Never writes flags** — fetch is `BODY.PEEK` (see notes) | 200 / 401 / 400 (malformed ref / Message-ID validation) / 404 (not found, includes "pair has no mailbox") / 502 / 500 |
| `POST /workspaces/{id}/mail/{agentId}/folders/{folder}/messages/{ref}/seen` | Mark `\Seen` — the panel calls it **once on open** (FR-020). Round-2 MAJ-003: a GET never writes flags, so the flag write is its own endpoint. Idempotent: already-seen is a no-op 204 | 204 / 401 / 400 (malformed ref) / 404 / 502 / 500 |
| `GET /workspaces/{id}/mail/{agentId}/folders/{folder}/messages/{ref}/attachments/{partIndex}` | Download one attachment part (D28): served with `Content-Disposition: attachment` and the **sanitized** filename (§2.2 `MailAttachment.filename` — no path separators, never trusted raw from the MIME part); header discipline per **MC-42** (always `attachment`, nosniff, extension-typed, RFC 6266 filename — never inline HTML on this origin) | 200 (the part's bytes, extension-derived `content_type` — MC-42) / 400 / 401 / 404 (message or part absent) / 502 / 500 |
| `POST /workspaces/{id}/mail/{agentId}/messages` | Human manual send (D9): renders Markdown → multipart/alternative, signature, SMTP, Sent APPEND. **Audit event + rate limit** (FR-025/FR-026) | 200 (`MailSendResponse`) / 400 (validation) / 401 / 404 / **429** (rate limited — MC-20, dedicated limiter per round-2 MIN-004) / 502 (SMTP/IMAP upstream, error class) / 500 |
| `PUT /workspaces/{id}/mail/{agentId}/folders/drafts/messages/{ref}` | Panel edit (D12/D23): APPEND updated draft (same Message-ID), `\Deleted` the old copy (FR-032). The body carries the viewed draft's `uidvalidity`/`uid`; when the path ref is a `uid:` ref, body and path must agree — mismatch is 400, not silently accepted (round-2 MAJ-008.1/.6). **Audit event + rate limit** | 200 (`MailMessage`) / 400 / 401 / 404 (draft gone) / **409** (stale precondition — body code `stale_draft`; round-2 MAJ-008.1) / **429** / 502 / 500 |
| `POST /workspaces/{id}/mail/{agentId}/folders/drafts/messages/{ref}/send` | Panel send = the approval (D12): transmits the request's content (never re-reads the draft as truth — MAJ-002), APPENDs to Sent (D18), `\Deleted` the draft (FR-032). **Idempotent** (round-2 MAJ-009): keyed on the draft's Message-ID, a repeat submit after a completed send returns the recorded first outcome — never a second transmission (FR-021). **Audit event + rate limit** | 200 (`MailSendResponse`) / 400 / 401 / 404 (draft gone) / **409** (stale precondition — body code `stale_draft`; or an already-sent repeat whose recorded outcome is unavailable) / **429** / 502 / 500 |
| `DELETE /workspaces/{id}/mail/{agentId}/folders/drafts/messages/{ref}` | Discard a draft (D12): `\Deleted` + `UID EXPUNGE` when the server supports UIDPLUS, else `\Deleted` only (deferred expunge — FR-032). **Audit event + rate limit** | 204 / 401 / 404 / **429** / 502 / 500 |
| `POST /api/v1/mail/html-preview-token` | Mint HTML-body preview token (D13/D17) — **session-authenticated, stays in the API namespace** (security sign-off C1: the serve routes moved to §2.3a). **The one live-IMAP fetch of the preview flow**: the message is fetched once, sanitized, and its body plus inline (`cid:`) parts and the `load_remote` remote-image URL list are held in the in-memory token store for the TTL — the serve routes in §2.3a never dial IMAP (round-2 MAJ-008.3, MAJ-003). **Rate-limited** (MC-44) | 200 / 400 / 401 / 404 / **429** (rate limited — MC-44) / **502** (upstream mail failure, error class in body) |
| `GET /workspaces/{id}/mail/summary` | Watcher-driven badge summary (`MailSummaryList`) for the workspace's mailboxes | 200 / 401 / 404 |

Notes binding the whole table:

- **502 for upstream mail failures.** The mail server is a third party; a timeout or auth failure there is not
  a client error and not a gateway bug. The body is the standard `ErrorResponse`; its **`code` field carries a
  sanitized error class** from the closed enum `timeout | dns | connect_refused | auth_failed | tls |
  folder_missing | server_error` (round-2 MIN-014/MAJ-008.5: `dns` = name resolution failed, `connect_refused`
  = dial refused — the instant failures a `timeout` label would misroute to the mail server; `*net.DNSError`
  maps to `dns`), plus a generic message; raw upstream text is logged server-side only (round-1 MIN-011).
  Per D30 the *cause* of the observed DNS-failure windows is unknown and outside this spec — these classes
  exist so the failure is visible and correctly classified whatever the cause turns out to be (FR-037).
- **Read-only vs mutating.** Reading folders/messages performs IMAP reads only; nothing is marked \Seen by
  the list/read path — every list/read/preview fetch uses **`BODY.PEEK[]`** (or `EXAMINE`), because today's
  non-peek fetch marks `\Seen` implicitly (round-2 MAJ-003; verified `pkg/email/transport.go::Client.ReadMessage`
  fetches `BODY[]` without peek). The **only** \Seen writers are: the `POST …/seen` endpoint (panel open,
  once per open — FR-020), agent `read_message` (D38: `BODY.PEEK` fetch plus one explicit STORE of `\Seen`
  **and** `$OmnipusAgentRead` — FR-039), and — never — the watcher (FR-023).
- **One IMAP session per REST request** (round-1 MAJ-012): every STATUS/LIST/SEARCH/fetch of one request runs
  on one IMAP connection; the gateway caps concurrent mail operations (A8) — excess requests **queue** under
  the FR-027 deadline, then fail **503 + error class** (round-2 MIN-003). Identical concurrent refreshes for
  one mailbox **coalesce** (singleflight-style merge), so N open tabs cost one IMAP login per refresh tick;
  automatic panel refreshes **do not dial at all** while that mailbox's watcher is in backoff — they return
  the last error class + `next_attempt_at` immediately; only the human Retry dials (D29/R2-9, round-2 MAJ-018).

### 2.3a HTML-preview serve routes — token-only, on the non-API `/mail-preview/` prefix (outside `/api/v1`)

**Why token-only, why this prefix (C11/F11 + C1/F1 — security sign-off, 2026-09-26).** These routes are
**token-only by design**: browser engines differ on cookies for sandboxed-frame requests — WebKit *attaches*
the session cookie (the measured Library Defect-2 exposure: `'self'` spans the whole gateway incl.
`/api/v1/*`), while Chromium/Firefox **withhold** cookies there, so cookie auth would either reintroduce the
Defect-2 exposure or break rendering. The token is the only credential. Expired/unknown/revoked are
deliberately indistinguishable — **404 only** (no 401 ever, no distinguishable bodies; MC-43/MC-45). No
`frame-ancestors` directive and no `X-Frame-Options` header (the token in the URL is the capability; both
would fight the SPA embedding). The no-redirect tripwire covers the whole prefix: nothing under
`/mail-preview/` may redirect, and the `/api/` sentinel never matches under it (MC-10(1), T62).

| Endpoint | Purpose | Status codes |
|---|---|---|
| `GET /mail-preview/html/{token}` | Serve the sanitized, sandboxed HTML body with the MC-10 header set — from the token store only, no IMAP (the mint in §2.3 is the only fetch) | 200 / 404 (expired/unknown/revoked — deliberately indistinguishable, MC-43/MC-45) |
| `GET /mail-preview/part/{token}/{index}` | Serve one inline (`cid:`) image part within the same token scope (so inline images render without any remote host) — from the token store only, no IMAP | 200 / 404 |
| `GET /mail-preview/img/{token}/{index}` | "Load images" proxy (D17/MC-10(6)): fetches **only** a remote URL **recorded in the token store at mint** (token bound with `load_remote=true`), streams it back under the **MC-41 SSRF pins** — never a fetch of caller-supplied URLs | 200 (`image/*`, `nosniff`, `Cache-Control: no-store`) / 404 (unknown token/index, upstream fetch failed, bounds or image-check failure — indistinguishable, MC-45's 404-only discipline) |

Notes binding this table:

- **Sanitize → store → serve ordering**: the sanitizer runs at mint; the serve routes serve stored bytes
  only — no IMAP at serve time (MC-10(5), the sign-off's negative test list).
- The `/mail-preview/` prefix is **never redirected from**: the tripwire mirrors
  `pkg/gateway/library_preview_no_redirect_test.go::TestLibraryPreview_NothingUnderThePrefixRedirects`
  (including its stdlib-mux positive control) over `/mail-preview/` with the `/api/` sentinel (MC-10(1), T62).

### 2.4 Tool-surface changes (registered via `pkg/agent/email_tools.go::registerEmailToolsForAgent`)

| Tool | Change |
|---|---|
| `send_email` | `body` becomes **Markdown** (D3). `to` becomes a **recipient list** (minItems 1) plus optional `cc`/`bcc` lists (D26). **Recipient cap on every send path** (round-2 MAJ-015, D29/R2-6): ≤ **50 recipients total across To/Cc/Bcc** after de-duplication; each address parsed with `net/mail.ParseAddress` (display names kept, RFC 2047-encoded per MC-4); 51st recipient = tool error before any SMTP connection. **Attachments** (D33): optional `attachments`
parameter — a list of **workspace-file references** (relative paths in the agent's workspace), resolved
server-side through the same workspace-path resolution the generic file tools use
(`pkg/tools/resolvepath.go::ResolvePath`) **under its open-read rule** — the unconditional secret carve-outs
are refused **at resolve time and re-verified when the file is read** (`recheckUnrestrictedCarveOut`), so
secrets are never attachable, and **every other readable file is attachable, including files outside the
workspace root** (D33 "no extra guard rails" — the send_file precedent rejected a path gate as bypassable;
security sign-off C2/F5). A nonexistent file fails when read at compose time — a tool error before any SMTP
connection.
Governed by **exactly the same tool policy as the send itself** — one call, one policy resolution, **no
separate ask gate**; only the general message limits already in this spec apply (≤ 10 files / ≤ 25 MiB
total, MC-32; 1 MiB body, MC-22; 50 recipients, MC-27), all enforced before dialing. **Description text**
must document the parameter and state that attaching files carries no separate approval — the tool's own
policy governs the whole call. Description updated; rendering, signature and Sent APPEND happen server-side |
| `reply` | **Defined** (round-2 MAJ-014): the primary recipient stays **derived** from the original's Reply-To/From (never a required `to` — existing reply prompts keep working); optional `cc`/`bcc` lists are **added**; optional `reply_all: bool` adds the original's To/Cc **minus the mailbox's own address**; body becomes Markdown (D3). The ≤ 50 cap applies to the merged recipient set. **Attachments** (D33): the same optional workspace-file `attachments` parameter as `send_email` — same policy rule (the tool's own policy governs the whole call, no separate gate), same general caps pre-dial (MC-32/MC-22/MC-27), same resolution semantics (`ResolvePath` open-read rule; carve-outs refused at resolve + I/O time; no outside-workspace gate — sign-off C2). Rendering, signature, attachment MIME assembly and Sent APPEND as `send_email` (D26) |
| `create_email_draft` | **New.** Params: `to` (list, minItems 1), `cc`/`bcc` (optional lists), `subject`, `body` (Markdown), optional `in_reply_to`. APPENDs to the mailbox's Drafts folder with `\Draft` (D8). Never sends. Generates the Message-ID itself: `<random-128-bit@domain-of-the-mailbox's-From-address>` (fallback `omnipus.invalid` when no domain is derivable); the same ID is kept through panel edits and the final send (round-1 MIN-004). Marks itself with an `X-Omnipus-Draft` header (FR-029). Returns `{"created":true,"message_id":"...","uid":123,"uidvalidity":456,"chat_link":"…" or null,"chat_link_reason":string|null}`; `chat_link` is built from `gateway.public_url` exactly like `serve_web` derives its origin — **http allowed**; when no origin is derivable (unset `public_url`, wildcard bind) the tool still succeeds and returns `chat_link: null` with the stated reason (round-1 MAJ-013; `pkg/tools/web_serve.go` precedent, `TestServeWebPublicURL`). **Recipient cap** ≤ 50 across To/Cc/Bcc after de-dup, same parsing as `send_email` (round-2 MAJ-015). **Attachments** (D33): optional workspace-file `attachments` parameter, same rules as `send_email` — the tool's own policy governs the whole call (no separate gate), general caps enforced pre-APPEND (MC-32/MC-22/MC-27), same resolution semantics (carve-outs refused at resolve + read; no outside-workspace gate — sign-off C2); a nonexistent file fails when read, pre-APPEND. Attached files are APPENDed as MIME parts of the draft, so the human approval panel lists exactly what would be sent (`MailMessage.attachments`) and the panel send carries them unchanged (FR-034/FR-035 machinery). Result values are the `CreateEmailDraftResult` schema (§2.2 — round-2 MIN-013: the SPA's "Open draft" action consumes the generated type, not a hand-written shape) |

### 2.5 Config keys

`MailboxConfig` (`pkg/config/config.go::MailboxConfig`) gains `SignatureHTML string
json:"signature_html,omitempty"` (**≤ 16,384 bytes**, enforced at configure time), `SentFolderName`/
`DraftsFolderName` (advanced overrides, §2.1). D27 **removed** the round-1 `NewMailAgentEnabled` key —
mail never starts an agent turn, so there is nothing for the key to switch; it never ships. Legacy configs
load unchanged (absent fields default empty = "no signature", auto-resolve folders). No new top-level config
keys; no env vars. Watcher runtime state is **not** config (FR-033).

### 2.6 Contract regeneration

One atomic commit carries: the schema files, the `openapi.yaml` references, regenerated `pkg/api/generated/`
and `src/lib/api/generated/` artifacts, per the 5-step procedure. `make verify-contracts` must be green before
any handler or UI consuming these types is reviewed.

### 2.7 Policy wiring (Hard Constraint #6 — every touch point, round-1 MAJ-003)

`create_email_draft` must be touched in **all seven** of these places or it is unreachable or boot-breaking for
real agents:

1. `pkg/config/defaults.go::defaultToolPolicyCeiling` — shipped ceiling entry `"create_email_draft": "allow"`
   (writes only a Drafts copy; cannot send), next to the five existing entries (`defaults.go` ~299–303).
2. `pkg/gateway/rest_mailbox.go::emailToolNames` — **replaced per D19**: configuring a mailbox writes
   `ask` for `send_email`/`reply` and `allow` for `read_inbox`, `search_email`, `read_message` and
   `create_email_draft` into the agent's own policy map **only where the agent has no explicit entry**
   (absent key = never set). The current `grantEmailToolAllows` deny→allow overwrite is deleted.
3. `pkg/coreagent/role_policies_adr090.go::ADR090RolePolicyInventory` — every role whose inventory grants
   `send_email` also grants `create_email_draft` as `allow` (drafting is the safe path; unlisted tools are
   `deny` for seeded roles, so without this the tool is denied for Mia and every seeded agent).
4. `pkg/coreagent/seed.go` tool-name literal — `create_email_draft` added, or `validateOverrideKeys`
   **panics at boot** once any policy override references it.
5. `pkg/tools/auto_approve.go` — `create_email_draft` classified **AutoRuns** (corrected 2026-09-26 per
   decision D45: a draft only ever appends to the mailbox's own Drafts folder and never sends, so it
   runs under Auto like the other non-sending email tools).
6. `pkg/tools/email.go::EmailToolset` — so registration, permissions screen and tests come from the same list
   the five existing tools use.
7. `pkg/coreagent/prompts_adr090.go` — the agent-prompt text enumerating the email tools (**owned by
   `prometheus-prompt-engineer`**, round-2 MIN-008: verified the prompt lists only the five existing tools).
   The prompt gains a sentence pointing agents to `create_email_draft` as the preferred path when
   `send_email`/`reply` resolve to `ask` — draft first, human approves by sending (D8's intent). `Mailbox.yaml`'s
   `enabled` description and `pkg/agent/loop_wire.go`'s tool enumeration comment are updated in the same
   change (they also list only five).

Reachability is asserted **behaviorally**, not by grep: for a seeded core agent and an operator-created
agent, both with an enabled mailbox, the effective policy of `create_email_draft` resolves to `allow` and the
tool is present in the agent's registry (§9 SC-007).

**D33 adds no touch point.** The attachment parameter is governed by exactly the same policy entries as the
tool itself: no new ceiling entry (point 1), no new D19-fill entry (point 2), no new inventory/seed literal
(points 3–4), no new auto-approve classification (point 5 — the whole call, attachments included, carries the
tool's own classification; the send tools stay `AutoAsks`, so auto-approve never silently runs them, Hard
Constraint #6). A per-attachment gate, policy entry or cap would contradict the founder decision (D33).
MC-15's schema-change scope covers the new parameter (§5.3).

---

## 3. Existing Codebase Context

> GitNexus MCP tools were **not connected in the writing session** and this worktree has no `.gitnexus/`
> index — per `omnipus-shared-rules` rule 9 the analysis below is a first-hand Read/Grep exploration, and the
> impact rows are labelled **Inferred**. Every cited symbol was re-verified in the working tree on
> 2026-09-25 during fix round 1.

### 3.1 Symbols involved

| Symbol | Role | Verified context |
|---|---|---|
| `pkg/config/config.go::MailboxesConfig` / `::MailboxConfig` | extends | The ADR-033 pair map `map[agentID]map[workspaceID]MailboxConfig`; `MailboxConfig` today has Enabled/WorkspaceID/IMAP*/SMTP*/Username/PasswordRef — no signature field |
| `contracts/components/schemas/Mailbox.yaml`, `MailboxConfigureRequest.yaml` | extends | Wire pair-config contract; `configured` flag never returns the password; the request schema is `additionalProperties: false` (why MIN-001's fields must be added there) |
| `pkg/gateway/rest_mailbox.go::getAgentMailbox` (+ set/delete/list) | extends | Pair-addressed config handlers; 404 when agent or pair missing; credential key `mailboxCredKey` |
| `pkg/gateway/rest_mailbox.go::grantEmailToolAllows` + `::emailToolNames` | **deletes/replaces** | Today converts any `deny` on all five email tools to `allow` when a mailbox is enabled (verified: `emailToolNames` literal at the five names, `grantEmailToolAllows(sm.a.homePath, sm.agentID)` on enable). D19 replaces it with the no-overwrite ask/allow fill (§2.7) |
| `pkg/config/defaults.go::defaultToolPolicyCeiling` | extends | Shipped ceiling entries for the five email tools (`read_inbox`/`send_email` … `allow`, verified ~299–303); `config.ReconcileToolPolicyCeiling` self-heals new entries |
| `pkg/coreagent/role_policies_adr090.go::ADR090RolePolicyInventory` | extends | Seeded roles default every unlisted tool to `deny`; `send_email`/`reply` granted `ask` per role (verified) — `create_email_draft` must be added per §2.7 |
| `pkg/coreagent/seed.go` tool-name literal | extends | `read_inbox, search_email, read_message, send_email, reply` (verified ~141); an override key absent from this literal panics boot via `validateOverrideKeys` |
| `pkg/tools/auto_approve.go` | extends | Email classification verified: send tools `AutoAsks`, read tools `AutoRuns`; `create_email_draft` joins as `AutoRuns` (corrected 2026-09-26 per decision D45) |
| `pkg/email/transport.go::Transport` | extends | Interface: `ReadInbox / Search / ReadMessage / Send / MarkSeen` — **no folder parameter, no APPEND**; `Client.dialIMAP` always SELECTs INBOX |
| `pkg/email/transport.go::buildEmailBody` | modifies | Builds `text/plain`-only RFC 5322 messages; bare `From`; header injection guard `sanitizeHeader` |
| `pkg/email/transport.go::Client.Send` | modifies | `func (c *Client) Send(_ context.Context, req SendRequest) error` — **the context is discarded** (verified); `sendSMTPWithSTARTTLS` uses `smtp.Dial`, `sendSMTPS` uses `tls.Dial`, neither with a deadline; the `dialTimeout`/`commandTimeout` constants apply only on the IMAP side. SMTP today can hang forever (#629) — absorbed by FR-027 |
| `pkg/email/transport.go::decodeBody` / `::htmlToText` / `::capBody` | calls | Inbound MIME decode prefers text/plain, strips HTML→text for the agent, `maxBodyBytes` = 256 KB cap, loud degrade markers. This cap also bounds the HTML the preview token serves |
| `pkg/tools/email.go::EmailTransports.resolve` | calls | Structural workspace resolution — no model-supplied mailbox/workspace parameter (ADR-033) |
| `pkg/tools/email.go::SendEmailTool` / `::ReplyTool` / `::EmailToolset` | modifies | One-recipient, plain-text bodies today; `read_message` marks `\Seen`; `EmailToolset` constructs the full set (verified ~472) |
| `pkg/agent/email_tools.go::registerEmailToolsForAgent` | extends | Registers all five email tools for **every** agent unconditionally; per-pair transports built from env-resolved passwords |
| `pkg/email/drainer.go::Drainer` + `pkg/heartbeat/mailbox_drain.go` + wiring | **deletes** | Verified wiring: `pkg/gateway/gateway_boot.go` and `pkg/gateway/gateway_reload.go` both call `heartbeat.NewMailboxDrainService(drainer, 0)` (1-minute interval). #631 orders deletion; D20 replaces it with the watcher (FR-023). After this spec lands, **no code or test references `Drainer`** |
| `pkg/config/config.go::GatewayConfig.PublicURL` / `::Users` | constraint | `PublicURL` is optional (`omitempty`, verified) — default local installs are `http://localhost:<port>` (why FR-015 allows http); `Users` is "single-user model: holds at most one entry" (why MC-13 tests 404 shapes, not second sessions) |
| `pkg/gateway/rest.go::listMailboxes` (`GET /api/v1/mailboxes`) | calls | Existing endpoint (verified ~675) that lists all configured mailboxes — drives the picker (FR-010) |
| `pkg/gateway/rest_auth.go::withRateLimit` + `::apiRateLimiter` | calls | Existing per-IP sliding-window limiter + wrapper (verified) — wraps the mutating mail routes (FR-026) |
| `pkg/audit` (`audit.go::Logger.Log`, `argshash.go`) + `pkg/gateway/rest_preview_audit.go` | calls | Audit logger, argument-hash helper, and the gateway audit precedent (verified) — emits the FR-025 events |
| `src/components/workspaces/WorkspaceTabBar.tsx::WORKSPACE_TABS` | extends | Workspace tab strip (`chat`, `board`, `calendar`, `media`, `team`); adding `mail` is one array entry plus route wiring; badge dot/count on the Mail entry (FR-023's badge surface) |
| `src/routes/_app/workspaces.$workspaceId.media.tsx` | pattern | The Library "tab" is a **redirect stub**: opens the docked Library panel and navigates back to Chat — the exact pattern D11 chooses for Mail |
| `src/components/library/LibraryPanel.tsx` | pattern | Docked `<aside>` in `AppShell.tsx` (next to `BrowserLivePanel`); state in `src/store/ui.ts::libraryPanel` (`openLibraryPanel`/`closeLibraryPanel`) |
| `src/components/library/LibraryExplorer.tsx` + `LibraryPreviewPane.tsx` | pattern | List + "PREVIEW/EDIT PANE PLACEHOLDER" slot; `LibraryPreviewPane` dispatches per kind via an exhaustive switch |
| `src/components/library/LibraryPreviewPane.tsx::LibraryHtmlFrame` | pattern | Sandboxed iframe (`sandbox` attribute + gateway CSP `sandbox` directive) over `/library/preview-token/{token}` — the isolation precedent for mail HTML. Mail differs: **no** `allow-scripts` (D13) |
| `pkg/gateway/library_isolation_policy.go` (+ `library_preview_no_redirect_test.go`) | pattern | The isolation **policy** behind that precedent: no `'self'` (Defect 1: WebKit `'self'` stops matching under an iframe `sandbox` — Safari loses inline images; Defect 2: `'self'` spans the whole gateway including `/api/v1/*`, and WebKit's SameSite=Strict cookie turns `<img src="/api/v1/…">` in untrusted HTML into a logged-in GET); path-confined sources, preview outside the API prefix, no redirect under the prefix. MC-10 (§5.3) mandates this exact posture for mail HTML — round-2 CRIT-001, D29 |
| `pkg/email/imapserver_test.go::startMemIMAP` | test harness | Real-protocol IMAP harness already in-tree: drives the real `Client` against `go-imap/v2/imapserver/imapmemserver` — no new dependency. §7 moves all flag/APPEND/EXPUNGE/UIDVALIDITY/peek/verify-server-side-end-state tests onto it (round-2 MAJ-004); fakes stay only for tool-level shape tests |
| `pkg/coreagent/prompts_adr090.go` (email-tool enumeration) | extends | The agent-prompt text listing the five email tools (verified — no `create_email_draft` mention); gains the draft-first sentence per §2.7 touch point 7 (round-2 MIN-008) |
| `src/main.tsx::createHashHistory` | constraint | The SPA router uses hash history (verified) — deep links carry `#/…`, and in-place navigation is detected by **hash-route pattern, not origin match** (round-1 MAJ-013) |
| `src/components/chat/markdown-shared.tsx::createLinkRenderer` | extends | Single shared link renderer for live + historical chat markdown; `src/lib/url-safe.ts::isSafeHref` allow-lists **http/https/mailto/tel only** — a mail deep link must be an absolute http(s) URL |
| `src/components/connectors/EmailMailboxPanel.tsx::EmailMailboxPanel` | extends | The mailbox config dialog (mounted from `src/components/screens/ConnectorsScreen.tsx`), form validation + move semantics — hosts the D2 signature editor and the folder-override advanced fields (D27 removed the D20 switch — no mail-trigger UI exists) |

### 3.2 Impact assessment (Inferred — no GitNexus in this worktree)

| Symbol modified | Risk | d=1 dependents | d=2 dependents |
|---|---|---|---|
| `pkg/email/transport.go::Transport` (new methods) | MEDIUM | `pkg/tools/email.go` (all five tools hold it), test fakes in `pkg/tools` + `pkg/email` | `pkg/agent/email_tools.go` (wiring), new watcher + gateway mail handlers |
| `pkg/email/transport.go::Client.Send` (+ context threading, #629) | MEDIUM | `SendEmailTool`/`ReplyTool` result texts; every existing `pkg/email` send test | panel send endpoint, manual send endpoint |
| `pkg/config/config.go::MailboxConfig` | LOW | `rest_mailbox.go` raw-map write path (must preserve unknown fields — ADR-033 §5 negative note), `EmailMailboxPanel.tsx` form state | config migration tests (mixed-shape strictness must keep passing) |
| `pkg/gateway/rest_mailbox.go::grantEmailToolAllows` (delete/replace) | MEDIUM | mailbox enable path (`setAgentMailbox`); any test asserting the deny→allow fill | operator configs whose tools were allow-filled by the old behavior — **greenfield ruling: no migration; fresh behavior applies on next configure** (memory: greenfield-no-upgrade-path) |
| `src/components/workspaces/WorkspaceTabBar.tsx::WORKSPACE_TABS` | LOW | `TabSegment`/`SEGMENT_LABELS` are derived from the array (compile-enforced completeness) | Playwright tab tests at 1280px |
| `src/store/ui.ts::libraryPanel` pattern → new `mailPanel` | LOW | `AppShell`, media redirect stub | sidebar entries |

No HIGH/CRITICAL blast radius found: `Transport` gains methods (implementations: one production `Client`,
test fakes — all in-tree, compile-breaks loudly); the drainer deletion is verified-wired in exactly two
gateway sites; nothing else re-keys.

### 3.3 Verified — transcripts and D6 (Q2 → answered by D21)

Verified first-hand (2026-09-25): `pkg/memory/jsonl.go::addMsg` marshals the full `providers.Message` into
the per-session archive line and fsyncs it; `pkg/providers/protocoltypes/types.go::FunctionCall.Arguments` is
a serialized JSON string — the model's full argument text (the whole email body passed to `send_email`/
`reply`/`create_email_draft`) lands on disk, and `read_message` results persist the same way. Transcripts are
append-only by design (`pkg/session/jsonl_backend.go::Save` is a no-op; the retention sweep is the sole
deleter — `pkg/memory/CLAUDE.md`). **D21 resolves Q2**: transcripts keep email text as every tool does; the
user docs say so plainly; D6 means no dedicated mailbox store (no local mirror, cache, or index of mail).

### 3.4 Execution flows

| Flow | Relevance |
|---|---|
| Agent email tool call (`read_inbox` → `read_message` → `reply`) | Every new mail tool rides the same `EmailTransports.resolve` turn-context stamp; no new resolution machinery |
| New-mail watcher cycle (replaces `Drainer`) | Never mutates flags or creates tasks; updates the per-mailbox state file + badge summary (FR-023); connection behavior bounded per FR-027 and revisited after D22's diagnosis lands |
| Mailbox config save (`rest_mailbox.go::setAgentMailbox`) | Gains `signature_html` + folder-name overrides + the D20 switch in the same raw-map round-trip that must preserve unknown fields; the D19 policy fill replaces `grantEmailToolAllows` |
| Library deep link → docked panel (`library.tsx` route + `ui` store) | The exact handoff pattern the mail deep link (D8 chat link) reuses |

### 3.5 Cluster placement

This feature spans two clusters — **email transport/tools** (`pkg/email`, `pkg/tools/email.go`,
`pkg/agent/email_tools.go`, watcher + drainer deletion in `pkg/heartbeat`) and **SPA workspace surfaces**
(Library-style panel + tab wiring, Connectors dialog). Backend-only changes compile in one tree; the UI slice
is confined to the workspace shell, chat markdown link renderer, and the Connectors dialog — no new product
area.

### 3.6 Available reference patterns

`docs/reference/go-implementation/` **does not exist in this repository** (checked 2026-09-25) — Phase 1.7 of
the skill is N/A. In-repo patterns used instead are cited inline throughout (Library panel trio, Library HTML
frame isolation, `web_serve` public-URL construction, `SearchResult` truncation contract,
`rest_preview_audit.go` audit precedent, `withRateLimit`/`apiRateLimiter`).

---

## 4. User stories and acceptance criteria

### US-1 — Per-mailbox HTML signature (P0) — D2

The operator wants each (agent, workspace) mailbox to carry its own HTML signature — the assistant signs
differently in the client-work workspace than the analyst does in the research workspace — so that every
outgoing message from that mailbox looks like it comes from a real, consistent role. Today
`MailboxConfig` has no signature concept and every message goes out as bare `text/plain`.

**Why this priority**: it is the one founder ask that no other story delivers, and US-2's render pipeline
consumes it — signature plumbing must exist before rich sending lands.

**Independent test**: configure a signature on one mailbox in the Connectors dialog, send any message from
that mailbox through any path (agent tool or manual), observe the signature on both the HTML and the
plain-text part; a second mailbox without a signature stays bare.

**Acceptance scenarios**:

1. **Given** a configured mailbox without a signature, **When** the operator opens the mailbox panel in
   Connectors and saves a signature (HTML) with the live preview showing the rendered result, **Then** the
   signature is stored with the mailbox pair and survives a gateway restart.
2. **Given** a mailbox with a saved signature, **When** any message is sent from that mailbox, **Then** the
   message carries the signature appended to both the HTML and the derived plain-text part.
3. **Given** a mailbox whose signature is edited or cleared, **When** the next message is sent, **Then** the
   new/empty signature applies (no stale copy is sent).
4. **Given** hostile signature HTML (script / event-handler / `javascript:` payloads), **When** the operator
   saves it, **Then** the stored value is the sanitized form (allowed formatting kept — inline `style`,
   tables, `img` over https/data — dangerous content stripped), and a message composed with a hostile body
   **and** a hostile signature still contains none of the dangerous content (MC-2).
5. **Given** an operator-created agent with **no explicit email-tool entries**, **When** its mailbox is
   configured, **Then** `send_email`/`reply` resolve to `ask` and the read tools + `create_email_draft`
   resolve to `allow` — and an agent with an explicit `deny` keeps it (D19; round-2 MAJ-013 — the fill is
   tested, not assumed).

### US-2 — Markdown out: multipart/alternative + Sent folder + recipient lists (P0) — D3, D7, D18, D26

Agents write message bodies as Markdown; the backend renders a sanitized HTML part plus a plain-text part
(multipart/alternative), appends the mailbox signature, and saves the sent message into the mailbox's Sent
folder over IMAP APPEND — always, on every provider, with no provider detection (D18: "we use IMAP only").
Agents and humans address **To/CC/BCC recipient lists** (D26). Agents never author raw HTML and never write
the signature.

**Why this priority**: without rendering + Sent APPEND, the Mail panel's Sent folder (D5/D7) has nothing to
show and D3's safety promise ("no model-authored raw HTML") is unmet.

**Independent test**: call `send_email` with a Markdown body and a To/CC list against a test transport that
records the message; assert multipart/alternative structure, sanitized HTML, appended signature, recipients
on the right header lines, and an APPEND of the same content to the Sent folder.

**Acceptance scenarios**:

1. **Given** an agent whose effective policy allows `send_email`, **When** it calls `send_email` with a
   Markdown body, **Then** the recipient receives a `multipart/alternative` message whose HTML part is the
   sanitized render (no script content) and whose plain part is readable text, both ending with the mailbox
   signature.
2. **Given** a successful SMTP send, **When** the send completes, **Then** the exact sent message (headers +
   both parts) is APPENDed to the mailbox's Sent folder and visible in the Mail panel.
3. **Given** an SMTP success but a Sent APPEND failure, **When** the tool result is produced, **Then** the
   result explicitly warns that the sent copy could not be saved — it never reports a silent partial success.
4. **Given** `to`, `cc` and `bcc` lists passed to any send path (agent tool or manual compose), **When** the
   message is composed, **Then** each list lands on its own RFC 5322 header line, BCC recipients receive the
   message but the BCC header is absent from the transmitted copy stored in Sent.
5. **Given** a UTF-8 subject and display name, **When** the message is composed, **Then** non-ASCII headers
   are RFC 2047-encoded and the composed body is within the outbound size bound (FR-031).

### US-3 — Workspace Mail tab: live Inbox / Sent / Drafts (P0) — D4, D5, D6, D11, D13, D17, D22, D25

The operator wants a "mini outlook" inside the workspace: a Mail surface that looks and behaves like the
Library (D4) — the tab entry opens a docked Mail panel beside chat plus a fullscreen pop-out (D11) — showing
exactly Inbox, Sent, Drafts (D5), reading the mailbox **live over IMAP** with no local copy of mail content
(D6 as scoped by D21). A workspace may hold several mailboxes (one per agent); the surface scopes to one
mailbox at a time with a picker. The panel refreshes every 30 s while open (D25); incoming HTML renders in a
sandboxed frame without scripts, remote images blocked by default with per-message "Load images" (D13, D17);
upstream failures are always visible (D22).

**Why this priority**: this is the visibility half of the founder's ask; it also carries the draft preview
surface that US-4's approval flow opens.

**Independent test**: with one configured mailbox, open the workspace Mail panel, browse all three folders,
open a message; verify envelope fields and body against the real mailbox, verify the 30 s refresh fires only
while the panel is visible, and verify (by inspecting the data directory) that no message bodies were written
locally.

**Acceptance scenarios**:

1. **Given** a workspace whose agent owns an enabled mailbox, **When** the operator opens the workspace Mail
   tab, **Then** the Inbox, Sent and Drafts folders are listed with unread/total counts fetched live from the
   mail server.
2. **Given** the Mail panel open on Inbox, **When** the operator clicks a message, **Then** the full message
   (headers + body) is fetched live and displayed; the URL/address does not depend on data stored locally.
3. **Given** a workspace with more than one mailbox, **When** the operator opens the Mail panel, **Then** a
   mailbox picker is offered (fed by `GET /api/v1/mailboxes`, filtered to the workspace) and the selection is
   remembered for the browser session (sessionStorage per workspace).
4. **Given** the mail server is unreachable, **When** the Mail panel loads folders or messages, **Then** an
   explicit error state names the sanitized error class with a retry affordance — never a silent empty list —
   and the failure is logged server-side with the raw cause (D22).
5. **Given** any Mail panel usage, **When** the data directory is inspected afterwards, **Then** no new file
   contains message bodies (D6 as scoped by D21; watcher state files hold UIDs/counts only).
6. **Given** the Mail panel open, **When** 30 seconds elapse, **Then** the open folder list and badge state
   refresh; **and when the panel is closed**, **Then** no panel-initiated polling continues.
7. **Given** an HTML message with remote images and inline (`cid:`) images, **When** the human opens it,
   **Then** HTML renders in a sandboxed frame without scripts, remote images stay blocked until the human
   clicks "Load images" (D17), and inline images render through the token-scoped part route without any
   remote host.
8. **Given** the Mail panel closed but the workspace view visible, **When** the watcher state updates (new
   mail, or an error/backoff), **Then** the tab badge refreshes from `GET …/mail/summary` every **60 s** —
   reading the saved watcher state, never dialing IMAP (D29/R2-5, round-2 MAJ-011); **and when the workspace
   tab is hidden**, **Then** the badge poll pauses.

### US-4 — Agent drafts with chat-link approval (P0) — D8, D12, D15, D23, D24

The founder wants agents to *propose* mail, not just send it: the agent composes a draft via the normal
provider mechanism (IMAP APPEND with `\Draft`), then posts a link in chat; clicking it opens the draft in a
preview side panel where the human can see, **edit, send or discard** it (D12); To, subject and body are all
editable (D23); externally-created drafts are fully editable too, with the formatting loss stated (D24).
Panel Send **is the approval** (D12). The send tools keep today's configurable policy (D15): they ride the
global ceiling (`allow`), built-in roles carry `ask` (ADR-090 inventory), and the operator configures per
agent — an agent *can* skip the draft flow, and that is an accepted, documented posture (D15, D19).

**Why this priority**: "definitely a way to approve an email draft" is the strongest sentence in the founder's
request; the draft tool + link + editable panel is the core of it.

**Independent test**: as an agent with a mailbox, call `create_email_draft`; assert the draft exists in the
mailbox's Drafts folder (with `\Draft`), the result carries a `message_id` (+ `chat_link` or its null
reason), and opening the link in the SPA shows the draft in the panel with edit/send/discard available.

**Acceptance scenarios**:

1. **Given** an agent with an enabled mailbox, **When** it calls `create_email_draft` with to/subject/body,
   **Then** a message APPENDed with the `\Draft` flag exists in the Drafts folder and the tool result includes
   its `message_id` (+ `uid`/`uidvalidity`) and `chat_link` (or a stated null reason).
2. **Given** the agent posted the chat link, **When** the human clicks it, **Then** the app navigates to the
   workspace and opens the draft in the preview side panel — same tab, not a browser popup.
3. **Given** a draft link for a draft that was already deleted from the Drafts folder, **When** the human
   clicks the link, **Then** the panel shows an explicit "draft no longer exists" state (Message-ID lookup
   miss), not a blank pane.
4. **Given** a draft that was already sent from the panel, **When** its link is clicked, **Then** the panel
   shows the sent copy ("Sent on \<date\>") found in the Sent folder — not a bare "no longer exists" (the
   link keeps working after approval).
5. **Given** the agent never calls `send_email`, **When** it only creates drafts, **Then** no outbound message
   is sent — draft creation alone is not a send.
6. **Given** a draft the owner started in their own mail client (no `X-Omnipus-Draft` header), **When** the
   human opens it in the panel, **Then** the panel offers full edit/send (D24), shows a plain statement that
   formatting (styling, images, table layout) may be lost and what is preserved (text content, headings,
   lists, links), and sending re-renders through the Markdown pipeline.

### US-5 — Manual send from the Mail panel (P1) — D9, D26

Humans can compose and send mail directly from the Mail panel — same mailbox, same rendering pipeline,
signature applied, To/CC/BCC supported (D26) — so the operator can answer mail without leaving Omnipus.

**Why this priority**: the founder's "maybe" ("possible also to send emails manually maybe") — wanted, but
weighted below the agent flows it reuses.

**Independent test**: open the Mail panel compose action, fill to/cc/subject/body, send; assert
`MailSendResponse` reports success + Sent APPEND, and the recipient-side content matches the agent pipeline
(multipart, signature).

**Acceptance scenarios**:

1. **Given** the Mail panel open on a mailbox, **When** the human opens compose, enters recipients, subject
   and a Markdown body, and sends, **Then** the message goes out through the same multipart + signature
   pipeline as agent mail and the compose dialog closes with a confirmation.
2. **Given** the compose dialog with an empty recipient or body, **When** the human clicks send, **Then**
   validation blocks the send with field-level errors and nothing is transmitted.
3. **Given** a message open in the panel, **When** the human clicks Reply, **Then** compose opens with the
   recipient pre-filled from the original sender, the subject `Re: …`, and `in_reply_to` set — so the sent
   message threads (round-2 MIN-009: gives `MailSendRequest.in_reply_to` its caller).
4. **Given** the compose dialog, **When** the human attaches up to 10 files (≤ 25 MiB total, D28) and sends,
   **Then** the recipients receive them as MIME attachments and the Sent copy carries them too (FR-034).

### US-6 — Read state that stays honest (P1) — D20 (as amended by D27), D38

Read state is IMAP `\Seen`, shared with the owner's own mail client. The Mail panel's list never marks
anything read; **opening** a message in the panel marks it `\Seen` via the dedicated seen endpoint (FR-020,
round-2 MAJ-003). Agent `read_message` marks the message read **and** sets the `$OmnipusAgentRead` IMAP
keyword in the same flag store (D38, FR-039) — the mailbox belongs to that agent, so its reads are reads.
The Mail panel shows a small **"read by agent"** tag on messages whose flag list carries the keyword, so the
human still sees what the agent handled — the answer to the interview's open point O5. Nothing is marked
read automatically; a human opening a message in the panel marks it read (plain `\Seen`, no keyword — the
two states stay distinguishable). When a mail server rejects custom keywords, the fallback marks `\Seen`
only: no tag renders, and nothing errors (D38, MC-36). **D27: an email
never starts an agent turn.** The watcher never mutates flags, never creates Board tasks, and never starts
an agent turn — it feeds only the unread badge and the "last checked" state; the email tools are used
actively by the agent inside turns that humans, tasks or heartbeats started (D31: agents handle mail only
when asked, or through an operator-configured heartbeat / scheduled task — nothing automatic). D20's per-mailbox switch
"Let the agent handle new mail" is **removed** and never ships — no code, no UI, no config key (it never
existed in code; the round-2 CRIT-002 attack path is closed by removal, and the R2-1 tool-restriction
question dies with it). (The story's tag replaces the round-1 draft's "handled by agent" badge, which was
built on the drainer that #631 deletes — round-1 CRIT-001; the D38 tag survives the drainer's deletion
because it rides an IMAP keyword the tool itself sets, not the drainer's task records.)

**Why this priority**: display correctness, not capability — the panel works without it, but a founder whose
Inbox always shows zero unread, and who cannot tell what the agent already handled, will distrust it.

**Independent test**: with one unseen message, open it in the panel and confirm the seen endpoint was called
and the flag flipped (no keyword); let the agent read another message with `read_message` and confirm `\Seen`
**and** the keyword land together and the panel shows the tag, then repeat on a keyword-rejecting server and
confirm the same read outcome with no tag and no error; let the watcher cycle over new mail and confirm the
message's flags are unchanged, no Board task exists, and no turn was started (there is no trigger to start
one).

**Acceptance scenarios**:

1. **Given** a message unseen by anyone, **When** the human opens it in the Mail panel, **Then** the panel
   calls the seen endpoint once, the message is marked `\Seen`, and the Inbox unread count drops on the next
   refetch.
2. **Given** new mail arriving while the watcher runs, **When** the watcher cycle completes, **Then** the
   message's flags are unchanged (peek reads only — no agent turn exists to read it) and no Board task exists
   for it (D20 as amended by D27).
3. **Given** any inbound email, **When** it arrives, **Then** no agent turn is started by the mail system —
   no switch, no trigger, no path from mail to a turn (D27; round-2 CRIT-002 is closed by removal, §14).
4. **Given** a message unseen by anyone, **When** the agent reads it with `read_message`, **Then** exactly one
   flag store adds `\Seen` **and** `$OmnipusAgentRead`, the envelope row carries `read_by_agent=true`, and the
   Mail panel shows the small "read by agent" tag (D38, FR-039).
5. **Given** a mail server that rejects custom keywords, **When** the agent reads a message with
   `read_message`, **Then** the message is still marked `\Seen`, the read result is normal, no error appears
   on any surface, and the message simply carries no tag (D38, MC-36).

### US-7 — Draft panel actions: view, edit, send, discard (P0) — D12, D23, D24

The draft preview panel is the approval surface: view the draft, edit To/subject/body (D23), send (the
approval — D12), or discard. Editing APPENDs an updated draft keeping the Message-ID and marks the old copy
`\Deleted`; sending transmits the exact content the human saw/edited (guarded against staleness by the
uidvalidity+uid precondition) and moves the draft out of Drafts; discarding deletes the draft. Deleting uses
`UID EXPUNGE` where the server supports UIDPLUS and `\Deleted`-only (deferred expunge) elsewhere — Omnipus
never triggers a blind `EXPUNGE` that could purge other `\Deleted` messages in the owner's folders, and
listings always exclude `\Deleted` messages.

**Why this priority**: it is the founder's approval act — the point of the whole draft flow.

**Independent test**: create a draft, open the panel, edit its body, save (draft updated, same Message-ID),
send (precondition matches → sent + Sent APPEND + draft removed), and verify a stale-precondition send (draft
replaced elsewhere in between) returns 409 and sends nothing.

**Acceptance scenarios**:

1. **Given** an Omnipus draft open in the panel, **When** the human edits To/subject/body and saves,
   **Then** the Drafts folder holds the updated draft under the **same Message-ID** and the old copy is
   `\Deleted` (excluded from listings).
2. **Given** the panel showing a draft, **When** the human clicks Send, **Then** the submitted content (exactly
   what was displayed/edited) is transmitted through the full pipeline (multipart + signature + Sent APPEND),
   the draft is removed from Drafts, and an audit event records the approval (FR-025).
3. **Given** a draft replaced by another writer between view and send, **When** the human clicks Send,
   **Then** the gateway returns 409 and nothing is transmitted — the human re-views and re-approves.
4. **Given** a draft the human discards, **When** Discard is clicked, **Then** the draft is `\Deleted` (+
   `UID EXPUNGE` under UIDPLUS), the panel closes, and an audit event records the discard.
5. **Given** an edit whose APPEND succeeds but whose old-copy delete-flag fails, **When** the panel reports
   the result, **Then** it carries an explicit warning that a duplicate draft may exist — never a silent
   partial edit.
6. **Given** a draft the owner edited in their own mail client between view and save, **When** the panel save
   lands, **Then** the panel warns that the owner's version was replaced (the same 409/precondition
   discipline as sending, applied to editing).

### US-8 — Attachments: download and send (P1) — D28

Humans can download attachments from any message in the Mail panel and attach files to messages they send —
from compose, and when editing drafts, including drafts started in other mail programs (their attachments are
carried over unchanged, listed explicitly in the panel before send). **D33 resolves the open point**: agent
tools gain the same capability for **workspace files** — `send_email`, `reply` and `create_email_draft` take
an optional `attachments` parameter governed by exactly the same tool policy as the send itself (no separate
gate; general message limits only).

**Why this priority**: D28's explicit ask; it extends the send/approve flows rather than blocking them, which
is why it sits at P1.

**Independent test**: receive a message with two attachments, download both from the panel (bytes match the
MIME parts, names sanitized); compose a message with one attached file and send (recipient + Sent copy carry
it); edit a foreign draft carrying an attachment and send — the attachment arrives on the sent message.

**Acceptance scenarios**:

1. **Given** a message with attachments, **When** the human opens it in the panel, **Then** each attachment
   is listed with name, size and type, and its download action serves the stored MIME part bytes under the
   sanitized filename (§2.3 attachments endpoint).
2. **Given** an attachment whose declared filename carries a path separator or control character, **When**
   it is downloaded, **Then** the served filename contains neither (edge case 21) — the name can never
   escape the downloads directory.
3. **Given** compose or draft editing, **When** the human adds files (≤ 10, ≤ 25 MiB total) and sends,
   **Then** the transmitted message carries them as MIME attachments and the Sent copy keeps them (FR-034).
4. **Given** a foreign draft with attachments, **When** the human edits and sends it from the panel, **Then**
   the attachments are carried over **server-side by part reference** (FR-035), the panel lists exactly what
   will be carried before send, and removal is an explicit action — the round-2 MAJ-010 data-loss path
   (silent attachment drop) is closed, not merely warned about.
5. **Given** an agent with an enabled mailbox and a file in its workspace, **When** the agent sends or drafts
   with that file attached (D33), **Then** the message/draft carries it as a MIME part (the approval panel
   lists it before send), the call resolves under **exactly the tool's own policy** — one approval for the
   whole call, no separate attachment gate (T65) — the general caps reject the 11th file / > 25 MiB before
   any dial, a secret-carve-out reference is refused at resolve and re-verified at read time (T67), and the
   audit/transcript records carry the attachment names and sizes (MC-19).

---

## 5. Behavioral contract (quick reference)

### 5.1 When/Then summary

- When a mailbox has a saved signature, any message sent from it (agent tool, draft sent, manual) carries the
  signature on both MIME parts.
- When a mailbox has no signature, outgoing messages are still multipart/alternative (D3); only the signature
  block is absent.
- When an agent calls `send_email` or `reply`, the body is Markdown, the recipients are lists (D26), and the
  sent message is multipart/alternative with sanitized HTML.
- When an agent calls `create_email_draft`, a `\Draft`-flagged message appears in Drafts and no mail is sent.
- When a human clicks a draft chat link, the draft opens in the preview side panel in the same tab.
- When the human edits/sends/discards in the panel, the action is the founder's approval act (D12) and is
  audit-logged (FR-025).
- When the Mail panel lists a folder, the data is fetched live from the mail server and nothing is persisted
  except reference metadata (watcher UID state included — D6 as scoped by D21).
- When the mail server is unreachable, every mail-backed surface shows an explicit, class-named error with
  retry — never a silent empty state — and the raw cause is in the server log (D22).
- When a sent message's Sent APPEND fails after SMTP success, the caller sees an explicit warning.
- When a chat link targets a Message-ID that no longer resolves in Drafts but exists in Sent, the panel shows
  the sent copy with its date.
- When the watcher observes new mail, it changes no flag, creates no task, **starts no agent turn** (D27 —
  no trigger exists), and only advances its UID state and the badge summary (D20 as amended by D27).
- When any message in the panel has attachments, the human can download each one under its sanitized
  filename; when compose or draft editing adds files (≤ 10, ≤ 25 MiB), the sent and Sent copies carry them,
  and a foreign draft's attachments carry over by part reference (D28, FR-034/FR-035).
- When an agent calls `send_email`, `reply` or `create_email_draft` with workspace-file attachments, the call
  resolves under exactly the tool's own policy — no separate attachment gate (D33, MC-35) — the attachments
  ride the message as MIME parts (a draft included, so the approval panel lists exactly what would be sent),
  within the general message limits (MC-32), with the attachment names and sizes recorded in the audit and
  transcript like any send (MC-19).
- When an agent reads a message with `read_message`, the message is marked read (`\Seen`) **and** carries the
  `$OmnipusAgentRead` keyword set in the same flag store; the Mail panel shows its small "read by agent" tag.
  When the server rejects custom keywords, the message is still marked read and simply shows no tag — never
  an error (D38, FR-039, MC-36).

### 5.2 Explicit non-behaviors

- The system must not give agents a raw-HTML authoring path, because D3 promises model-authored HTML never
  reaches recipients (Markdown + server-side sanitize only). The signature is operator HTML but is sanitized
  on save (FR-003) — raw operator HTML never ships either.
- The system must not store message bodies locally (beyond session transcripts, which D21 explicitly keeps),
  because D6 promises the mailbox stays the single source of truth; watcher state files hold UIDs/counts/an
  error class only (FR-033).
- The system must not let the Mail panel's list/read path mutate mailbox flags; the only \Seen writers are
  the panel's once-per-open call to the seen endpoint (FR-020, round-2 MAJ-003) and agent `read_message`
  (D38: one explicit `\Seen` + `$OmnipusAgentRead` STORE) — the watcher never writes flags (D20), because
  flags are shared with the owner's own mail client. The system must not turn a keyword-rejecting server
  into a failed agent read: the fallback marks `\Seen` only and omits the tag (D38, MC-36).
- The system must never start an agent turn from an email — no switch, no trigger, no config key (D27; the
  round-2 CRIT-002 path is removed, not hardened), because unattended attacker-triggered turns are the exact
  prompt-injection shape CRIT-002 described. **D31**: agents handle mail only when asked (inside a
  human/task-started turn) or through a heartbeat / scheduled task the operator configures — nothing about
  mail is automatic; user docs state the same.
- The system must not add a separate approval gate, policy entry or special cap for the agent tools'
  attachment parameter (D33) — attaching workspace files rides exactly the tool's own policy resolution and
  the general message limits (MC-32/MC-22/MC-27); a per-attachment ask prompt, a separate ceiling/fill entry
  or attachment-only caps would contradict the founder decision. The only path property is `ResolvePath`'s
  own open-read rule: unconditional secret carve-outs refused at resolve and re-verified at I/O time —
  **no outside-workspace gate** (MC-35, security sign-off C2). The human attach/download paths (D28) are
  unchanged.
- The system must not serve the mail HTML frame from the API prefix or put `'self'` in its CSP — the
  Library-preview defects (CRIT-001) are measured repo history, not theory (`pkg/gateway/library_isolation_policy.go`).
  The mail CSP is its **own named builder**, never the Library's (MC-37); the no-origin case **omits** host
  sources with a loud WARN — never a `'self'` fallback (MC-38).
- The system must not change the send tools' policy resolution beyond D19's configure-time no-overwrite fill
  (D15 keeps the ceiling as shipped; built-in roles keep `ask`); silently widening approvals is out.
- The system must not let a chat-posted draft link expose another pair's mail: links are pair- and
  folder-scoped and resolve only inside the addressed folder of the addressed pair (MC-13).
- The system must not add IMAP folders, rename folders, or show folders outside Inbox/Sent/Drafts (D5).
- The system must not reintroduce a standalone preview route for mail content (Library CLAUDE.md rule: preview
  is framed inside its panel; `/preview/` is agent `web_serve` only).
- The system must not create Board tasks from mail (D20 — the drainer's behavior class is retired with #631).
- The system must not trigger a non-UID `EXPUNGE` (it would purge other `\Deleted` messages in the owner's
  folders) — UIDPLUS `UID EXPUNGE` or deferred expunge only (FR-032).
- The system must not run scripts, load remote images by default, or allow forms inside the mail HTML frame
  (D13/D17, MC-10).
- The system must not let a panel send re-read the draft as the source of truth for what to transmit — the
  request carries the exact displayed/edited content and a staleness precondition (MAJ-002, MC-16).

### 5.3 Machine-verifiable constraints

| ID | Constraint | Test hook |
|---|---|---|
| MC-1 | `signature_html` longer than 16,384 chars → HTTP 400 `ErrorResponse` on `PUT /agents/{id}/mailboxes/{workspaceId}`; the panel blocks longer input | gateway handler unit test |
| MC-2 | Rendered HTML part contains no `<script>` element, no `on*=` handler attribute, and no `javascript:` href — verified on a compose of hostile **body** `<script>alert(1)</script><img src=x onerror=alert(1)><a href="javascript:alert(1)">x</a>` **and** a hostile **signature**; and the signature's stored value is the sanitized form (inline `style`, tables, https/data `img` preserved). **Outbound body policy named** (round-2 MIN-011): links `http`/`https`/`mailto` only, images `https`-only, no `style` attribute from agent Markdown (the signature keeps its sanitized `style`); schemes outside the allowlist are dropped, not mangled | unit tests on the render pipeline and on signature save |
| MC-3 | **Every** outbound message is `multipart/alternative` with exactly one `text/plain` and one `text/html` part, signature or not. The `text/plain` part is the plain-text rendering of the Markdown (goldmark text rendering of the same parse — links as `text (href)`, no HTML) followed by `-- ␊` and the plain-text-derived signature when a signature exists; the `text/html` part is the sanitized render followed by the HTML signature | unit test on the composer |
| MC-4 | `\r\n` or `\n` in a subject/recipient never reaches a header (existing `sanitizeHeader` behavior holds); non-ASCII subjects/display names are RFC 2047-encoded and parse back to the original | unit tests (header injection + RFC 2047) |
| MC-5 | Folder slugs are exactly `inbox` \| `sent` \| `drafts`; any other value → HTTP 400 | contract + handler test |
| MC-6 | `limit` ≤ 0 → default 20; `limit` > 100 → clamped to 100 (mirrors `clampLimit`, `pkg/email/transport.go`) | unit test |
| MC-7 | Ref not found in the addressed folder (unknown `uid:` or no `mid:` hit) → HTTP 404 `ErrorResponse`; multiple `mid:` hits → **highest UID among non-`\Deleted` messages** (round-2 MIN-005 — never INTERNALDATE, which APPEND may set from the message's own Date header) | handler + transport tests |
| MC-8 | Mail server dial/command failure on **any** mail endpoint (IMAP and SMTP) → HTTP 502 with a sanitized error class from the closed enum `timeout\|dns\|connect_refused\|auth_failed\|tls\|folder_missing\|server_error` in `ErrorResponse.code` (round-2 MIN-014) and a generic message; raw upstream text appears only in the server log; SMTP **and IMAP** return within the bounded timeouts of FR-027/FR-037 (never hangs) | handler tests with black-hole/failing transports; DNS-injected resolver tests (MC-33) |
| MC-9 | SMTP success + Sent APPEND failure → `MailSendResponse.sent_saved=false` + `save_warning` non-empty (tool result carries the same warning) | unit test on the send path |
| MC-10 | The mail HTML preview **reuses the Library-preview isolation posture** (`pkg/gateway/library_isolation_policy.go` — round-2 CRIT-001, D29): (1) served under a dedicated non-API prefix (`/mail-preview/…`), **outside** `/api/v1` — **the §2.3a routes carry this** (mint stays `POST /api/v1/mail/html-preview-token`, session-authenticated) — with a no-redirect tripwire mirroring `library_preview_no_redirect_test.go` (including its stdlib-mux positive control) over `/mail-preview/` with the `/api/` sentinel; (2) the CSP **contains no `'self'`** — every source is path-confined to the mail-preview prefix (frame + `cid:` part + proxy URLs), plus `data:` for inline images; (3) CSP `sandbox` **mirrors the iframe attribute token-for-token**: `sandbox allow-popups allow-popups-to-escape-sandbox` — nothing more (a bare `sandbox` directive would intersect to *no popups* and kill D13's link UX; loosening the *attribute* toward scripts/same-origin is the wrong direction — security sign-off C5/F3), sent **as a directive as well as the iframe attribute**; the effective sandbox is the **intersection** of the two layers — WebKit drops `'self'` matching once the attribute is layered on (Library Defect 1) and `'self'` spans the whole gateway incl. `/api/v1/*` with the SameSite=Strict cookie attached (Library Defect 2), so `'self'` would re-admit authenticated API GETs from untrusted HTML on Safari; (4) `base-uri 'none'; connect-src 'none'; object-src 'none'`; (5) the **inbound sanitizer is named**: strips `<meta http-equiv>`, `<base>`, forms, scripts, event handlers; rewrites `cid:` to the part URLs; hardens every surviving anchor per **MC-40**; (6) "Load images" (D17) routes remote images **through a gateway proxy** — https-only, private/loopback/link-local refused, bounded size, `image/*` only — so the CSP never gains `https:` (which would re-admit the gateway origin) and the frame never talks to the remote host directly; the proxy's properties are pinned mechanically by **MC-41**; (7) served HTML bounded by the existing 256 KB inbound body cap (`::capBody`); token bound to (pair, folder, ref, load_remote) with the Library token-hygiene set (**MC-43**: 256-bit entropy, named TTL, per-session cap, indistinguishable 404s, logout revocation); `Referrer-Policy: no-referrer` (the token rides the URL path); `X-Content-Type-Options: nosniff`; `Cache-Control: no-store` on every served response; iframe sandbox attribute **without** `allow-scripts`/`allow-same-origin`/`allow-forms`/`allow-top-navigation`, **with** `allow-popups allow-popups-to-escape-sandbox` (D13 "feels normal" via link clicks, not scripts) | gateway handler tests, one per directive, plus a WebKit-cookie case (untrusted `<img src="/api/v1/…">` loads nothing) and the mail no-redirect test (T62); plus the sign-off constraint rows **T72–T80**; **security-lead sign-off delivered 2026-09-26 — SIGN-OFF WITH CONDITIONS; C1–C11 absorbed in this revision (normative block below + MC-37–MC-45)** |
| MC-11 | `create_email_draft` result JSON parses with `created=true`, a non-empty `message_id` matching the APPENDed message's Message-ID header, `uid`/`uidvalidity`, and `chat_link` either null-with-reason or an absolute http(s) URL derived per FR-015 | unit test with fake transport |
| MC-12 | After exercising all Mail panel endpoints against a temp data dir, a sweep of the data dir shows no file containing message body text (watcher state files excepted — they contain UIDs/counts only); transcripts excluded per D21 | integration test assertion |
| MC-13 | A draft deep link for an agent/workspace that does not exist, or a pair with no mailbox → HTTP 404 with no body or folder leakage (single-user model — `GatewayConfig.Users` holds at most one entry; there is no second session to test) | handler test |
| MC-14 | Drafts listed with `is_draft=true` and `is_omnipus_draft` reflecting `X-Omnipus-Draft`; APPEND to Drafts always sets the `\Draft` flag; listings exclude `\Deleted` messages | transport fake assertion |
| MC-15 | The five existing email tools' schemas change only per D26 (recipient lists), D3 (body = Markdown description text) and D33 (the optional workspace-file `attachments` parameter); no other parameter changes | contract review check |
| MC-16 | Panel send with a stale precondition (draft at `uidvalidity:uid` no longer matches) → HTTP 409, nothing transmitted, no flag or folder mutated | handler test |
| MC-17 | Discard/edit old-copy deletion: with UIDPLUS, `UID EXPUNGE` removes exactly the target UID; without UIDPLUS, only `\Deleted` is stored and listings (ours) exclude it — a non-UID `EXPUNGE` is never issued by Omnipus | transport fake tests (both server shapes) |
| MC-18 | A full watcher cycle over a mailbox with new mail: no `\Seen`/flag STORE issued, no Board task created, **no agent turn started** (D27 — nothing exists to start one), state file gains only `last_seen_uid`/`unseen_total`/`uidvalidity`/error fields (D20 as amended by D27) — asserted against the **`startMemIMAP` real-protocol harness** (round-2 MAJ-004), not a hand-written fake, because a fake cannot observe the absence of a flag STORE from a non-peek fetch | `startMemIMAP` integration test + state-file assertion |
| MC-19 | Human send, panel send, panel edit and panel discard each emit an audit event carrying pair, folder, Message-ID, recipient addresses (incl. Bcc — MAJ-005's audit-trail requirement, D29/R2-3), origin (`human`\|`agent-draft`\|`owner-draft` — round-2 MIN-006: a panel send of a foreign draft), argument hash, outcome — **and, when the message carries attachments, their filenames and sizes (D33: names/sizes recorded like any send)**; agent tool sends remain covered by existing transcript/policy records (the full tool arguments — attachment references included — land in the transcript, §3.3) | gateway audit tests (`pkg/gateway/rest_preview_audit.go` precedent, `pkg/audit::argshash`) |
| MC-20 | The mutating mail routes (manual send, panel send/edit/discard) are wrapped in `withRateLimit` with a **dedicated `mailMutationLimiter` instance** (round-2 MIN-004 — never a shared limiter, which would couple the budgets) at **10 requests/minute** per IP; the 11th within the window → HTTP 429 | handler test |
| MC-21 | SMTP dial black-hole returns within the dial bound; a stall after connect returns within the command bound; caller-context cancellation aborts the send — all three via the FR-027 mechanisms (#629) | unit tests on `Client.Send` |
| MC-22 | Outbound body beyond **1 MiB** → HTTP 400 on the REST send routes and a tool error on the agent path (nothing transmitted) | unit + handler tests |
| MC-23 | `GET /workspaces/{id}/mail/summary` returns per-mailbox `unseen_total`, `watcher_state` (`ok\|error\|backoff`), `last_error_class`, `last_success_at`, `last_seen_uid` and `next_attempt_at` matching the watcher state files (`watcher_enabled` is dropped — D27); a mailbox in error/backoff reports its class and `next_attempt_at` so the badge can say "retrying at hh:mm" (D29/R2-8); `ok` never lies — `last_success_at` is null when no cycle has ever succeeded (round-2 MAJ-019) | handler + watcher tests |
| MC-24 | The seen endpoint (§2.3) marks exactly the addressed message `\Seen`, idempotent (repeat → 204), and a GET of folders/messages/preview **never** changes flags — asserted server-side via `startMemIMAP` (round-2 MAJ-003) | `startMemIMAP` integration test |
| MC-25 | Every list/read/preview fetch uses `BODY.PEEK[]` or `EXAMINE` — a `startMemIMAP` harness captures the FETCH commands issued and asserts no non-peek `BODY[]` fetch occurs in the list/read/preview paths (round-2 MAJ-003/MAJ-004) | `startMemIMAP` integration test |
| MC-26 | Configure-time policy fill (D19, round-2 MAJ-013): absent key → `ask` for `send_email`/`reply`, `allow` for the read tools + `create_email_draft`; an explicit `allow`/`ask`/`deny` is never changed; a seeded Admin's inventory `deny` stays `deny`. `grantEmailToolAllows` is gone | unit test table over the three key states; effective-policy assertion for a seeded agent + an operator-created agent |
| MC-27 | A 51st recipient (any mix of To/Cc/Bcc, after de-dup) → tool error / HTTP 400 **before any SMTP connection** on every send path (`send_email`, `reply`, `create_email_draft`, manual/panel send); addresses parsed with `net/mail.ParseAddress`, display names preserved, de-duplicated (round-2 MAJ-015, D29/R2-6) | unit + handler tests (50 passes, 51 fails pre-dial) |
| MC-28 | Panel send is idempotent per draft Message-ID (round-2 MAJ-009): a repeat submit after a completed send returns the recorded outcome (`draft_cleanup_warning` set if the draft-delete had failed) — **no second transmission**; verified by a transport that counts SMTP DATA events | `startMemIMAP` + counting-transport test |
| MC-29 | Draft Markdown source: an Omnipus draft stores its Markdown as a **`text/markdown` MIME part** inside a `multipart/mixed` wrapper (round-2 MAJ-006 — no Markdown-in-a-header); `X-Omnipus-Render-Hash` carries the hash of the rendered text part. A draft whose stored hash no longer matches its text part is treated as **foreign** (FR-030 path, loss statement shown) — the owner's mail-client edits are never silently discarded | unit tests on draft APPEND/parse; stale-hash round-trip test |
| MC-30 | The plain-text part is derived from the **same goldmark AST** as the HTML (custom AST text renderer — links as `text (href)`, lists indented, code kept literal; round-2 MAJ-007) with **hard wraps** enabled so single newlines survive (agents write line-per-item plain text today — verified `pkg/tools/email.go::ReplyTool.Parameters`); DS-2 covers multi-line plain text | unit test on the composer (hostile multi-line input) |
| MC-31 | Watcher UID state (round-2 MAJ-001): state file carries `uidvalidity`; on UIDVALIDITY change or first run (no state), the watcher **baselines to `UIDNEXT-1`** without flagging the backlog as new, and logs once; state file keyed by `(agent, workspace)` and deleted with the mailbox | watcher unit/integration tests (`startMemIMAP`) |
| MC-31a | The deep link's redirect stub copies `mailbox`/`folder`/`message` query params into `openMailPanel({agentId, folder, ref})` before navigating back to Chat (round-2 MIN-012) — the parameters survive the stub | E2E: link with params → panel opens on the draft |
| MC-31b | Watcher failure logging follows the FR-036 rate rule (round-2 MIN-015/007): first failure + every state change at WARN, ≤ 1 summary line per mailbox per backoff step, one INFO on recovery; request-path failures log once per request. State file deleted with the mailbox | unit test with a scripted failure sequence |
| MC-32 | Attachment caps on **every** attach path (compose, draft edit, panel send, **and the agent tools' `attachments` parameter — D33/MC-35**; round-2 MAJ-015/D28): ≤ 10 files, ≤ 25 MiB total decoded; filename sanitized on serve **and** on attach; `data_base64` (human paths) and workspace-file references (agent path) capped + carve-out-checked before any SMTP connection (resolution semantics per MC-35 — sign-off C2) | unit + handler tests (11th file, > 25 MiB, path-y filename) |
| MC-33 | Connection resilience (D29/R2-8, R2-9; round-2 MAJ-016..018): context-aware dial (FR-037) retries **only** name-resolution failures, bounded (3 attempts, 250 ms → 1 s) inside the overall dial bound; never retries auth/TLS; per-mailbox backoff 60 s → 2 → 4 … cap 15 min with ±20% jitter, first cycles randomly offset 0–60 s so same-host mailboxes never align; `auth_failed` backs off to the cap; manual Retry bypasses backoff for that one request; identical concurrent refreshes coalesce (N tabs = 1 login); no automatic dial while backing off | injected-resolver unit tests (fails twice → succeeds once; always-fails → `dns` class within the bound); fake-clock backoff/jitter tests; `startMemIMAP` LOGIN-count test with 3 tabs |
| MC-34 | FR-036 logging-rate rule holds (round-2 MIN-015): a 13-mailbox failure window produces bounded log volume (first failure + state changes, not per cycle) | unit test with a scripted multi-mailbox failure window |
| MC-35 | Agent tool attachment parameter (D33): an attachment-bearing call resolves under **exactly the same effective policy** as the same call without it — no separate ask gate, no separate ceiling/fill/inventory/auto-approve entry (send tools stay `AutoAsks`, so auto-approve never silently runs them, Hard Constraint #6); references resolve through the tools' workspace-path resolution (`pkg/tools/resolvepath.go::ResolvePath`) **under its open-read rule**: the unconditional secret carve-outs are refused **at resolve time and re-verified at I/O time** (`recheckUnrestrictedCarveOut`) — secrets are never attachable — and **every other readable file is attachable, including files outside the workspace root** (D33 "no extra guard rails"; the send_file precedent rejected a path gate as bypassable; security sign-off C2/F5 — the earlier "outside the workspace = invalid input" claim was **not** what `ResolvePath` enforces and is deleted); a nonexistent file fails when read at compose time — a tool error before any SMTP connection or APPEND, naming the offending reference; only the general message limits apply (MC-32 caps, MC-22 body bound, MC-27 recipient cap); audit/transcript records carry attachment filenames and sizes | unit tests (T64/T65/T67): policy identical with/without the parameter; caps pre-dial; **carve-out refused at resolve AND re-verified at I/O time**; the outside-workspace **attachable** verdict pinned so the gap cannot false-green; nonexistent read failure pre-dial; audit/transcript carries names + sizes |
| MC-36 | **Agent read marker (D38).** `read_message` fetches with `BODY.PEEK` (never the implicit-`\Seen` non-peek `BODY[]` — verified `pkg/email/transport.go::Client.ReadMessage` fetches `BODY[]` without peek today) and issues **one** STORE adding `\Seen` **and** `$OmnipusAgentRead`; when the server rejects the keyword, the fallback STORE sets `\Seen` only — the read result is unaffected, no error surfaces on any surface, the tag is absent, and the keyword is not re-attempted until the process restarts (one WARN per process per mailbox); `read_by_agent` is derived from the fetched flag list — no Omnipus-persisted state (D6 untouched); a server that rejects even `\Seen` makes the read fail visibly (the same failure class as the panel's seen endpoint on that server, FR-018) — a read-only agent mailbox is not a supported configuration; keyword comparisons in tests are case-insensitive (the in-tree `imapmemserver` lowercases keywords — go-imap v2 `imapmemserver/message.go::canonicalFlag`; real servers preserve case per RFC 3501) | `startMemIMAP` command-capture integration test (one STORE, both flags; fallback forced by a keyword-rejecting transport wrapper); envelope-derivation unit test |
| MC-37 | **The mail CSP is its own named builder** (security sign-off C3/F10, MEDIUM structural): built in its own file (e.g. `pkg/gateway/mail_isolation_policy.go`) — the Library policy/builder (`libraryIsolationPolicy` and its template, which carries `sandbox allow-scripts`, script-src host sources and `'unsafe-inline'` in script-src) is **never** called, imported or copied for mail; the shipped Library-string tripwire still guards the Library file itself. Mail-builder tripwires assert on the built policy string: **no `'self'`**, **no `allow-scripts`**, no script-src host sources, **no `'unsafe-inline'` in script-src**, **no `https:` source in `img-src`**, and every host source path-confined to `/mail-preview/` | gateway unit tests on the builder output (one negative assertion per clause), plus a source-level check that the mail builder does not reference the Library builder |
| MC-38 | **No-origin degradation omits, never falls back** (security sign-off C4/F2, MEDIUM conditional): when no canonical gateway origin is derivable (unset `gateway.public_url`, wildcard bind), the mail CSP **omits the host sources** — inline/`data:` images and origin-referencing subresources simply fail to load, the safe direction — and logs a **loud WARN**; a `'self'` or bare `https:` source is **never** emitted as a fallback (the Library's `'self'` fallback, `libraryIsolationUnconfinedSource`, is exactly the Defect-2 shape for mail: WebKit attaches the session cookie to framed subresource requests) | unit test: no-origin build → no host sources in the policy string + the WARN recorded; property assertion: the policy never contains `'self'`/`https:` in any configuration state |
| MC-39 | **CSP `sandbox` mirrors the iframe attribute token-for-token** (security sign-off C5/F3, MEDIUM): the directive is exactly `sandbox allow-popups allow-popups-to-escape-sandbox` — nothing more; the effective sandbox is the **intersection** of the two layers (a capability exists only if BOTH grant it), so a bare `sandbox` directive silently kills D13's link UX, and the tempting fix — loosening the attribute toward scripts/same-origin — is the wrong direction. Negative tests in **both** layers: no `allow-scripts`, `allow-same-origin`, `allow-forms`, `allow-top-navigation`, `allow-downloads`, `allow-modals`, `allow-pointer-lock` | gateway header test + SPA-side frame test asserting both layers token-exact (the seven banned tokens absent in each) |
| MC-40 | **Anchor hardening in the inbound sanitizer** (security sign-off C6/F4, MEDIUM): every `<a href>` surviving sanitization is rewritten to carry `rel="noopener noreferrer"` and `target="_blank"` **before** the HTML is stored in the token store (escaped popups carry `window.opener` — reverse tabnabbing of the operator's authenticated SPA; the sanitizer is the only place the opener link can be cut, since the sandbox escape token is what D13 needs) | sanitizer unit test: `<a href="https://evil/">x</a>` renders with `rel="noopener noreferrer"` + `target="_blank"`; the rewritten HTML is what the token store holds |
| MC-41 | **Image-proxy SSRF pins, dial-time** (security sign-off C7/F6, MEDIUM): https-only; private/loopback/link-local/metadata IPs refused **at dial time** — the resolved IP is pinned into the dial context so validate and dial cannot disagree (no DNS-rebinding gap); **zero redirects followed**; response ≤ 5 MiB and ≤ 10 s; only `image/*` re-emitted, with `X-Content-Type-Options: nosniff` and `Cache-Control: no-store`; only URLs recorded in the token store at mint (addressed by index) are ever fetched — never caller-supplied URLs; the endpoint lives under `/mail-preview/` (so the CSP `img-src` confinement covers it) and requires the preview token bound with `load_remote=true` | proxy unit/integration tests: rebinding attempt (validator pass + dial-time refusal), zero redirects followed, oversize/overtime cut, non-`image/*` refused, no-token/`load_remote=false` refused, non-store URL refused |
| MC-42 | **Attachment download header discipline** (security sign-off C8/F7): every download from the §2.3 attachment endpoint carries **always** `Content-Disposition: attachment` (an `.html` attachment is never inline — this endpoint is session-authenticated on the gateway origin; inline HTML here would be Library-Defect-2-shaped), `X-Content-Type-Options: nosniff`, a content type from the **extension-derived** map (extension decides — never the bytes, never the host registry, never the MIME part's self-declared type when it disagrees with a dangerous extension; the `applyLibraryByteHeaders` discipline), and the filename RFC 6266 dual-encoded (ASCII fallback + `filename*`, the `contentDispositionAttachment` discipline) from the MC-32-sanitized name; no CSP on attachment responses (the Library MV-13 second half) | handler tests: `.html` part → always-`attachment` + extension-typed + nosniff; hostile/unicode filenames round-trip through the RFC 6266 dual encoding |
| MC-43 | **Preview-token hygiene = the Library precedent, mail-bound** (security sign-off C9/F8): 256-bit `crypto/rand` tokens, fail-closed on entropy error or short read; a **named** TTL constant (Library: `PreviewTokenTTL` = 15 min); a per-session live-token cap that **refuses, never evicts** (Library: 8); expired/unknown/revoked **deliberately indistinguishable — 404 only**; in-memory store keyed by a session digest (never the raw credential); revocation wired into logout; token bound to (pair, folder, ref, load_remote); `Cache-Control: no-store` on every served response | token-store unit tests: entropy failure fails closed; TTL constant asserted; the over-cap mint refused (never evicting); expired == unknown == revoked → byte-identical 404s; logout revokes; served responses carry `no-store` |
| MC-44 | **The mint route is rate-limited** (security sign-off C10/F9): `POST /api/v1/mail/html-preview-token` is wrapped in a **dedicated per-IP limiter instance** (own instance — never the shared API limiter; the `mailMutationLimiter` precedent, round-2 MIN-004; 10 requests/minute, 429 with `Retry-After`) — the mint does the one live-IMAP fetch + sanitize + store insert per call, so over-budget calls never reach IMAP. The constant is reviewer-challengeable with A10's standing. (The Library mint path has its own limiter — `pkg/gateway/preview_token_ratelimit_test.go`.) | handler test: the over-budget mint → 429 + `Retry-After`, zero IMAP dials for the refused call |
| MC-45 | **Serve routes are token-only, 404-only — the reason documented** (security sign-off C11/F11): the §2.3a routes accept **only** the preview token — no cookie/session auth (browser engines differ on cookies for sandboxed-frame requests: WebKit attaches them — the measured Defect-2 exposure; Chromium/Firefox withhold them — cookie auth would break rendering there); expired/unknown/revoked are 404 only (no 401, no distinguishable bodies); **no** `frame-ancestors` directive and **no** `X-Frame-Options` header (the URL token is the capability; both would fight the SPA embedding); the reason is documented in the handler comment and §2.3a | header/negative handler tests: a valid session cookie without token → 404 (never 200/401); expired/unknown/revoked byte-identical 404s; no `frame-ancestors`/`X-Frame-Options` ever emitted |

#### MC-10 normative header set and iframe attributes (verbatim — security-lead sign-off, 2026-09-26)

Source (dated 2026-09-26): `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/logs/email-security-signoff.md`,
section "Required header set and iframe attributes (normative for implementation)". Copied verbatim; normative
for implementation.

**HTML body response** (`GET /mail-preview/html/{token}`; same posture for the §16 signature
mini-frame):

```
Content-Type: text/html; charset=utf-8
Content-Security-Policy: sandbox allow-popups allow-popups-to-escape-sandbox; default-src 'none'; script-src 'none'; style-src 'unsafe-inline'; img-src <origin>/mail-preview/ data:; font-src data:; media-src data:; connect-src 'none'; form-action 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'
Referrer-Policy: no-referrer
X-Content-Type-Options: nosniff
Cache-Control: no-store
```

**Rules:**

- `<origin>` = each canonical browser-facing gateway origin, path-confined to `/mail-preview/`;
  **never** `'self'`, never a bare `https:` source (MC-10(2), Library Defect 2).
- No-origin degradation: **omit** host sources + loud WARN (C4) — never a `'self'` fallback.
- `style-src 'unsafe-inline'` is the one `unsafe-inline` allowed and it is required — real
  email is style-heavy; scripts stay `'none'`.
- `style-src` keeping `unsafe-inline` with `default-src 'none'` is fine; if the team wants
  belt-and-braces, scope it `style-src 'unsafe-inline'` only (no host sources for styles —
  remote stylesheets are not needed for rendering and are blocked by `default-src 'none'` with
  no style-src host sources. Keep host sources only on `img-src`.
- No `frame-ancestors` (see F11); no `X-Frame-Options` (subsumed; and it would fight the SPA
  embedding — do not add it).

**iframe attributes** (SPA side, mirroring `LibraryHtmlFrame` minus scripts):

```
<iframe src="{token URL}"            -- never srcdoc (Library rule: srcdoc resolves relative
                                             URLs against the EMBEDDER, no response of its
                                             own to carry the isolation policy)
        sandbox="allow-popups allow-popups-to-escape-sandbox"
        referrerPolicy="no-referrer"
        allow="" />
```

**Negative test list (qa-lead's pack):** in BOTH the CSP directive and the attribute: no
`allow-scripts`, `allow-same-origin`, `allow-forms`, `allow-top-navigation`, `allow-downloads`,
`allow-modals`, `allow-pointer-lock`. And: no `'self'` anywhere in the policy string; no
`https:` source in `img-src`; every host source path-confined to `/mail-preview/`; sanitize →
store → serve ordering (sanitizer runs at mint, serve route serves stored bytes only, no IMAP
at serve time); 256 KB cap enforced at mint (`::capBody`); error classes enum-only (no upstream
text in bodies).

### 5.4 Integration boundaries

| External system | Data in/out | Contract | Failure behavior |
|---|---|---|---|
| IMAP server (per mailbox) | Folder list/counts, envelope pages, full messages, APPEND (drafts, sent copies), flag stores, UID EXPUNGE, BODY.PEEK reads | `emersion/go-imap/v2` over IMAPS (993) — existing `Client` patterns (`pkg/email/transport.go`); UIDPLUS probed for FR-032 | Every UI surface maps failures to sanitized-class error states (MC-8); the watcher logs and degrades its badge state, never silently (D22) |
| SMTP server (per mailbox) | Outbound RFC 5322 message | STARTTLS 587 / SMTPS 465 — **context-threaded, bounded** (FR-027, #629) | Send failure → tool error / compose dialog error, nothing APPENDed |
| SPA (Mail panel, compose) | The §2.3 REST endpoints; the §2.2 schemas | Generated types only (`src/lib/api/generated/`) | TanStack Query error states; retry affordances; 30 s visible-only refresh (D25) |
| Chat (link → panel) | The `chat_link` URL scheme: `…/#/workspaces/{wsId}/mail?mailbox={agentId}&folder=drafts&message=mid%3A%3C…%3E` (hash-history pattern match — `src/main.tsx::createHashHistory`) | Absolute http(s) URL derived like `serve_web` (passes `isSafeHref`); `chat_link` null-with-reason when no origin is derivable (FR-015) | Unknown/deleted target → panel not-found state (US-4 AS-3); sent draft → "Sent on" state (US-4 AS-4) |

Development uses the in-memory transport fake pattern that already exists for the five tools
(`pkg/tools/email.go` header note: "fully unit-testable against an in-memory fake"). **Testing is
three-tier (D36/D37):** logic and server-side flag/APPEND semantics live at unit + integration level
against the in-tree real-protocol harness `pkg/email/imapserver_test.go::startMemIMAP` (round-2 MAJ-004);
every main mail flow runs in a real browser on every CI run against the **built-in fake IMAP/SMTP server**
started by the E2E fixture (§7 E2E note, T71); and live-server acceptance happens once, in the **D37 UAT
campaign** (T44 — separate instance vs GreenMail in Docker), followed by the post-landing live smoke. D18
(generic IMAP only) means **no provider-specific behavior anywhere in the pipeline** — including no
Sent-copy dedication logic; the duplicate-Sent-copy consequence on providers that auto-file sent mail is
documented as accepted (FR-006).

---

## 6. BDD scenarios

#### Scenario B-1: Save a signature with live preview
**Traces to**: US-1, AS-1 · **Category**: Happy Path
- **Given** a configured mailbox with no signature, and the mailbox panel open in Connectors
- **When** the operator enters signature HTML and the live preview renders it, then saves
- **Then** `GET /agents/{id}/mailboxes/{workspaceId}` returns the saved `signature_html`
- **And** after a gateway restart the signature is still returned

#### Scenario B-2: Signature applied to both parts on send
**Traces to**: US-1, AS-2 · **Category**: Happy Path
- **Given** a mailbox with a saved signature
- **When** an agent sends a message via `send_email`
- **Then** the recipient-visible message ends with the signature in both the HTML and the plain-text part

#### Scenario B-3: Cleared signature stops applying
**Traces to**: US-1, AS-3 · **Category**: Alternate Path
- **Given** a mailbox with a saved signature
- **When** the operator clears the signature and saves, then an agent sends a message
- **Then** the outgoing message carries no signature content (and remains multipart/alternative, MC-3)

#### Scenario B-4: Hostile signature is sanitized on save
**Traces to**: US-1, AS-4 · **Category**: Edge Case
- **Given** signature input containing `<script>`, `onerror=` and a `javascript:` link, plus legitimate inline `style`, a table and an https logo
- **When** the operator saves the signature
- **Then** the stored value keeps the style/table/https-image content and drops the script/handler/`javascript:` content (MC-2)
- **And** a subsequent compose with a hostile body **and** this signature contains no dangerous content in either MIME part

#### Scenario B-5: Signature too long is rejected
**Traces to**: US-1 (edge of AS-1) · **Category**: Edge Case
- **Given** signature input longer than 16,384 characters
- **When** the panel attempts to save
- **Then** the panel blocks it, and a direct PUT returns HTTP 400 (MC-1)

#### Scenario B-6: Markdown renders to sanitized multipart with signature
**Traces to**: US-2, AS-1 · **Category**: Happy Path
- **Given** a mailbox with a signature, and an agent allowed to use `send_email`
- **When** the agent sends a Markdown body containing a heading, a link and a raw `<script>` tag
- **Then** the composed message is `multipart/alternative`
- **And** the HTML part renders heading + link and contains no `<script>` element (MC-2, MC-3)
- **And** the plain-text part contains the readable text and the signature

#### Scenario B-7: Sent message is APPENDed to Sent
**Traces to**: US-2, AS-2 · **Category**: Happy Path
- **Given** a mailbox whose Sent folder resolves (A1)
- **When** a send completes successfully
- **Then** a message identical to the one transmitted exists in the Sent folder
- **And** it appears in the Mail panel's Sent listing on next fetch

#### Scenario B-8: Sent APPEND failure is surfaced, never silent
**Traces to**: US-2, AS-3 · **Category**: Error Path
- **Given** a mailbox whose Sent APPEND fails (transport fake configured to fail APPEND)
- **When** a send completes over SMTP
- **Then** the tool result / `MailSendResponse` carries `save_warning` (MC-9) and `sent_saved=false`
- **But** the recipient still receives the message (the SMTP success is not rolled back or hidden)

#### Scenario B-9: Recipient lists land on the right header lines
**Traces to**: US-2, AS-4 · **Category**: Happy Path
- **Given** a send with `to=["a@x.test"]`, `cc=["b@x.test","c@x.test"]`, `bcc=["d@x.test"]` (D26)
- **When** the message is composed and sent
- **Then** the To/Cc/Bcc header lines carry exactly those addresses
- **And** the copy stored in Sent contains no Bcc header, and `d@x.test` still received the message

#### Scenario B-10: UTF-8 headers encode and the size bound holds
**Traces to**: US-2, AS-5 · **Category**: Edge Case
- **Given** a UTF-8 subject and display name, and a body at 1 MiB and at 1 MiB + 1 byte
- **When** the messages are composed
- **Then** the subject/display name are RFC 2047-encoded and decode back to the original (MC-4)
- **And** the 1 MiB body sends; the 1 MiB + 1 byte body is rejected with 400 / tool error, nothing transmitted (MC-22)

#### Scenario B-11: Folders listed live with counts
**Traces to**: US-3, AS-1 · **Category**: Happy Path
- **Given** a workspace whose agent owns an enabled mailbox with mail in Inbox and Sent
- **When** the Mail panel requests the folder list
- **Then** exactly `inbox`, `sent`, `drafts` return (D5) with `total` counts matching the server and `unread_count` set for Inbox, null elsewhere
- **And** the response is built from live IMAP data (fake transport records the fetch)

#### Scenario B-12: Open a message in the Mail panel
**Traces to**: US-3, AS-2 · **Category**: Happy Path
- **Given** a message in the Inbox with plain-text and HTML parts
- **When** the human opens it from the list
- **Then** the full envelope fields and decoded `body_text` render in the panel
- **And** `has_html` reports the HTML part's presence, driving the sandboxed frame (B-17)

#### Scenario B-13: Multiple mailboxes get a picker
**Traces to**: US-3, AS-3 · **Category**: Alternate Path
- **Given** a workspace where two agents each own a mailbox
- **When** the human opens the Mail panel
- **Then** a mailbox picker offers both (agent-named) mailboxes — fed by `GET /api/v1/mailboxes` filtered to this workspace — and the chosen one drives folder/message queries
- **And** the selection survives in-session navigation (sessionStorage per workspace)

#### Scenario B-14: Mail server unreachable shows a classified, visible error
**Traces to**: US-3, AS-4 · **Category**: Error Path · **Scenario Outline**
- **Given** a mailbox whose IMAP host fails in the manner of `<failure>`
- **When** the Mail panel loads folders
- **Then** the surface shows an explicit error with the sanitized class `<class>` and a retry control (MC-8)
- **But** no empty-folder state is presented as if the mailbox were empty
- **And** the gateway log carries the raw upstream cause (D22)

  | Example | failure | class |
  |---|---|---|
  | name resolution fails (round-2 MIN-014) | resolver error | `dns` |
  | black-holed address | no answer until the dial bound | `timeout` |
  | port closed | dial refused | `connect_refused` |

#### Scenario B-14a: DNS hiccup inside one operation retries bounded and succeeds
**Traces to**: US-3, AS-4 · **Category**: Alternate Path
- **Given** a resolver that fails name resolution **twice**, then succeeds, inside one panel load
- **When** the panel loads folders
- **Then** the load **succeeds** — the context-aware dial retried the resolution failure within its bound
  (round-2 MAJ-016, FR-037) — and no error state is shown
- **But** an always-failing resolver still returns the `dns` class within the bound (never retries auth/TLS)

#### Scenario B-14b: Backing-off mailbox refreshes show the saved class, no dial
**Traces to**: US-3, AS-4, AS-6 · **Category**: Error Path
- **Given** a mailbox whose watcher is in backoff after repeated failures
- **When** the panel's automatic 30 s refresh fires while the panel is open
- **Then** the panel shows the watcher's last error class and `next_attempt_at` immediately — **no IMAP dial
  occurs** (D29/R2-9, round-2 MAJ-018)
- **And** the human Retry button bypasses the backoff for that single request (D29/R2-8)

#### Scenario B-15: No local mail store is created
**Traces to**: US-3, AS-5 · **Category**: Edge Case
- **Given** a fresh gateway with a configured mailbox
- **When** folders are listed, two messages read, one manual send performed, and one watcher cycle runs
- **Then** a sweep of the data directory finds no new file containing message body text (MC-12; watcher state files contain UIDs/counts only)

#### Scenario B-16: 30-second refresh only while the panel is open
**Traces to**: US-3, AS-6 · **Category**: Happy Path
- **Given** the Mail panel open on Inbox
- **When** 30 seconds elapse
- **Then** the folder list and badge state refetch (D25)
- **And** when the panel is closed, no further panel-initiated refetch fires (TanStack Query with `refetchIntervalInBackground: false`, `refetchInterval: 30000` while mounted)
- **And** identical concurrent refreshes coalesce — with N tabs open, one refresh tick costs **one** IMAP login per mailbox (D29/R2-9, round-2 MAJ-018, MC-33)

#### Scenario B-17: HTML mail renders sandboxed; remote images need a click
**Traces to**: US-3, AS-7 · **Category**: Happy Path
- **Given** an HTML message carrying a remote `<img>` (tracking pixel), an inline `cid:` logo, and a `<form>`
- **When** the human opens it
- **Then** the HTML renders in a frame whose CSP/sandbox matches MC-10 (no scripts, no forms, no same-origin)
- **And** the remote image loads zero third-party requests until "Load images" is clicked (D17), which re-mints the token with `load_remote=true`
- **And** the inline `cid:` image renders through `/mail-preview/part/{token}/{index}` without any remote host

#### Scenario B-18: Watcher badge summary reflects mailboxes
**Traces to**: US-3, AS-1, AS-8 · **Category**: Happy Path
- **Given** two enabled mailboxes with unseen mail, one in a watcher error state and one backing off
- **When** the SPA fetches `GET /workspaces/{id}/mail/summary` every 60 s with the panel closed (D29/R2-5 — from saved state, no IMAP)
- **Then** each mailbox returns `unseen_total`, `watcher_state` (`ok|error|backoff`), `last_error_class`, `last_success_at` and `last_seen_uid` (MC-23) — `watcher_enabled` no longer exists (D27)
- **And** the workspace tab strip's Mail entry shows the unseen badge while the panel is closed (A9, round-2 MAJ-011 closed)
- **And** the backing-off mailbox reports `next_attempt_at` ("retrying at hh:mm", D29/R2-8)

#### Scenario B-19: Draft created with \Draft flag and chat link
**Traces to**: US-4, AS-1 · **Category**: Happy Path
- **Given** an agent with an enabled mailbox
- **When** it calls `create_email_draft` with to, cc, subject and Markdown body
- **Then** the Drafts folder contains the message with the `\Draft` flag (MC-14)
- **And** the tool result reports `created=true`, the draft's `message_id`, `uid`/`uidvalidity` and a `chat_link` (MC-11)

#### Scenario B-20: Chat link opens the draft preview panel in place
**Traces to**: US-4, AS-2 · **Category**: Happy Path
- **Given** the agent posted the `chat_link` in the workspace chat
- **When** the human clicks the link
- **Then** the SPA opens the draft in the preview side panel in the same tab (hash-route pattern match, not origin match)
- **And** the panel shows the draft's to/cc/subject/body as stored in Drafts

#### Scenario B-21: Deleted draft's link shows not-found
**Traces to**: US-4, AS-3 · **Category**: Error Path
- **Given** a draft whose chat link was posted, and the draft has since been removed from Drafts
- **When** the human clicks the link
- **Then** the panel shows an explicit "draft no longer exists" state
- **But** no other folder's or mailbox's mail is exposed (MC-13)

#### Scenario B-22: Sent draft's link shows the sent copy
**Traces to**: US-4, AS-4 · **Category**: Alternate Path
- **Given** a draft whose link was posted, and the draft was sent from the panel (it now lives in Sent)
- **When** the human clicks the link
- **Then** the panel resolves the Message-ID in Sent and shows "Sent on \<date\>" with the sent content
- **And** the panel's action set for a sent copy is read-only

#### Scenario B-23: Draft creation alone sends nothing
**Traces to**: US-4, AS-5 · **Category**: Edge Case
- **Given** an agent that calls only `create_email_draft`
- **When** the call completes
- **Then** no SMTP session is opened and no message leaves the mailbox

#### Scenario B-24: Foreign draft opens with the formatting-loss statement
**Traces to**: US-4, AS-6 · **Category**: Alternate Path
- **Given** a draft the owner started in their own mail client (multipart, no `X-Omnipus-Draft` header)
- **When** the human opens it in the panel
- **Then** the panel offers edit/send (D24), shows the plain statement that styling/images/table layout may be lost and what is preserved, and derives an editable Markdown from the HTML or plain part
- **And** sending goes through the standard Markdown pipeline, keeping the draft's Message-ID

#### Scenario B-25: Compose validation blocks empty sends
**Traces to**: US-5, AS-2 · **Category**: Error Path
- **Given** the compose dialog with an empty recipient
- **When** the human clicks send
- **Then** field-level validation errors render and no request is made

#### Scenario B-26: Manual send through the same pipeline
**Traces to**: US-5, AS-1 · **Category**: Happy Path
- **Given** the Mail panel open with compose available
- **When** the human sends to/cc/subject/Markdown body
- **Then** `MailSendResponse` reports `sent=true` with `message_id` and `sent_saved=true`
- **And** the transmitted message is multipart/alternative with the mailbox signature

#### Scenario B-27: Panel-open read marks \Seen via the seen endpoint
**Traces to**: US-6, AS-1 · **Category**: Happy Path
- **Given** a message unseen by anyone
- **When** the human opens it in the Mail panel
- **Then** the panel calls the seen endpoint **once** (§2.3; round-2 MAJ-003), `\Seen` is stored, and the
  Inbox unread count drops on the next refetch
- **And** re-opening the same message does not re-write the flag (idempotent — no-op 204)

#### Scenario B-28: Watcher reads nothing and starts nothing
**Traces to**: US-6, AS-2 · **Category**: Happy Path
- **Given** new mail arriving, watcher running (no switch exists — D27)
- **When** a watcher cycle completes
- **Then** the messages remain unseen (the cycle issues only peek reads), no Board task exists, and **no
  agent turn was started** — there is no trigger to start one (D27, MC-18)

#### Scenario B-29: No path from mail to an agent turn exists
**Traces to**: US-6, AS-3 · **Category**: Alternate Path
- **Given** any inbound email (read, unread, hostile, or a burst of 30 messages)
- **When** the watcher processes it
- **Then** no agent turn starts — no switch, no config key, no trigger exists in the shipped system (D27;
  round-2 CRIT-002 is closed by removal), and the email tools remain available for the agent to use
  actively inside human/task/heartbeat-started turns

#### Scenario B-30: Panel edit keeps the Message-ID and retires the old copy
**Traces to**: US-7, AS-1 · **Category**: Happy Path
- **Given** an Omnipus draft open in the panel
- **When** the human edits subject and body and saves
- **Then** the Drafts folder holds the updated draft under the same Message-ID, the old copy is `\Deleted` and absent from listings (MC-14, MC-17)
- **And** the chat link still resolves to the updated draft (D23)

#### Scenario B-31: Panel send is the approval and moves the draft
**Traces to**: US-7, AS-2 · **Category**: Happy Path
- **Given** the panel showing a draft whose precondition matches
- **When** the human clicks Send
- **Then** the request's content (exactly what was displayed/edited) is transmitted through the full pipeline (multipart + signature + Sent APPEND, D18)
- **And** the draft is `\Deleted` from Drafts, and an audit event records the approval with origin `agent-draft` (MC-19)

#### Scenario B-32: Stale precondition blocks the send
**Traces to**: US-7, AS-3 · **Category**: Error Path
- **Given** a draft replaced by another writer (e.g. edited in the owner's own client) between panel view and send
- **When** the human clicks Send with the stale `uidvalidity:uid`
- **Then** the gateway returns 409 (MC-16), nothing is transmitted, and the panel offers a re-view

#### Scenario B-33: Discard removes exactly the target draft
**Traces to**: US-7, AS-4 · **Category**: Happy Path
- **Given** a draft the human discards
- **When** Discard is clicked
- **Then** the target draft is gone from listings, an audit event records the discard (MC-19)
- **And** under a non-UIDPLUS server, only `\Deleted` was stored — no other message was expunged (MC-17)

#### Scenario B-34: Edit with a failed delete-flag warns of a possible duplicate
**Traces to**: US-7, AS-5 · **Category**: Error Path
- **Given** a panel edit whose APPEND succeeds but whose old-copy `\Deleted` store fails (fake transport)
- **When** the panel reports the result
- **Then** it carries an explicit "a duplicate draft may exist" warning — never a silent partial edit

#### Scenario B-35: Stale precondition blocks the edit save
**Traces to**: US-7, AS-6 · **Category**: Error Path
- **Given** a draft replaced by another writer between panel view and edit-save
- **When** the human saves the edit with the stale precondition
- **Then** the panel warns that the owner's version was replaced and offers a re-view; nothing was overwritten silently

#### Scenario B-36: UIDVALIDITY change does not break the deep link
**Traces to**: US-4, AS-2 · **Category**: Edge Case
- **Given** a posted draft link, and the mailbox's UIDVALIDITY changed since (UIDs renumbered)
- **When** the human clicks the link
- **Then** the draft still resolves by `mid:` Message-ID and renders (D21 adopts the MAJ-005 addressing)

#### Scenario B-37: Unknown folder slug is rejected
**Traces to**: US-3 (edge of AS-1) · **Category**: Edge Case
- **Given** a request to `GET …/folders/archive/messages`
- **When** the gateway handles it
- **Then** it returns HTTP 400 naming the valid folder slugs (MC-5)

#### Scenario B-38: Double submit sends once
**Traces to**: US-7, AS-2 · **Category**: Error Path
- **Given** a draft open in the panel, and a client or network that retries
- **When** panel Send fires twice for the same draft (double-click or retry after a slow 200)
- **Then** exactly **one** SMTP transmission occurs — the second submit returns the recorded first outcome
  (idempotency keyed on the draft's Message-ID, round-2 MAJ-009, MC-28), and if the draft-delete had failed
  the response carries `draft_cleanup_warning`
- **But** no draft is left in Drafts that could pass a second send's precondition

#### Scenario B-39: Attachments download and compose attach
**Traces to**: US-8, AS-1, AS-3 · **Category**: Happy Path
- **Given** an inbound message with two attachments, and the compose dialog open
- **When** the human downloads each attachment, then composes a new message with one attached file and sends
- **Then** the downloads serve the stored part bytes under sanitized filenames (no path separators — US-8
  AS-2), and the sent message carries the file as a MIME attachment on the transmitted and Sent copies
  (D28, FR-034)

#### Scenario B-40: Foreign draft's attachments carry over
**Traces to**: US-8, AS-4 · **Category**: Happy Path
- **Given** a foreign draft carrying two attachments, open in the panel
- **When** the human edits the body and sends
- **Then** both attachments ride the sent message — carried **server-side by part reference** (FR-035),
  listed explicitly in the panel before send; nothing is dropped silently (round-2 MAJ-010 closed)

#### Scenario B-41: 51st recipient rejected before dialing
**Traces to**: US-2 (edge of AS-4) · **Category**: Edge Case
- **Given** a send with 50 recipients across To/Cc/Bcc and a 51st added
- **When** any send path (`send_email`, `reply`, draft panel, compose) processes it
- **Then** it fails with a recipient-count error **before any SMTP connection** (round-2 MAJ-015, MC-27);
  50 recipients after de-dup succeed

#### Scenario B-42: Bounded DNS retry recovers a name-resolution hiccup
**Traces to**: US-3, AS-4 · **Category**: Alternate Path
- **Given** a resolver failing resolution twice then succeeding, watcher and panel both active
- **When** a cycle or refresh dials
- **Then** the bounded in-operation retry succeeds (round-2 MAJ-016, FR-037) — one successful dial, no error
  surfaced; with an always-failing resolver the class is `dns` within the bound, and auth/TLS failures are
  never retried

#### Scenario B-43: Backoff with jitter; auth failure waits the cap; Retry is immediate
**Traces to**: US-3, AS-4 · **Category**: Error Path
- **Given** a mailbox whose watcher failed twice consecutively (round-2 MAJ-017, D29/R2-8)
- **When** further cycles run
- **Then** attempts space out exponentially 60 s → 2 → 4 … capped 15 min with ±20% jitter, the first cycles
  randomly offset 0–60 s so same-host mailboxes never align; an `auth_failed` class waits at the cap and
  never retries faster
- **And** the human Retry button dials immediately once, bypassing the backoff for that request only;
  recovery resets the schedule and logs one INFO with the outage duration

#### Scenario B-44: N tabs coalesce; a backing-off mailbox never auto-dials
**Traces to**: US-3, AS-6 · **Category**: Happy Path
- **Given** three browser tabs showing the same mailbox's panel for five minutes (round-2 MAJ-018, D29/R2-9)
- **When** the refresh ticks run
- **Then** the IMAP LOGIN count is ≤ 1 per 30 s per mailbox (identical refreshes coalesce) plus the
  watcher's own count (MC-33), and while the watcher backs off the panel performs **zero** automatic dials

#### Scenario B-45: UIDVALIDITY change baselines without flagging the backlog
**Traces to**: US-6, AS-2 · **Category**: Edge Case
- **Given** watcher state stored, and the server resets UIDVALIDITY (folder recreated)
- **When** the next cycle runs
- **Then** the watcher re-baselines to `UIDNEXT-1`, logs once, flags nothing as new, starts nothing (D27)
  and resumes normally (round-2 MAJ-001, MC-31)

#### Scenario B-46: Configure mailbox on an operator-created agent fills ask/allow
**Traces to**: US-1, AS-5 · **Category**: Happy Path
- **Given** an operator-created agent with no explicit email-tool entries
- **When** its mailbox is configured
- **Then** `send_email`/`reply` resolve `ask`, read tools + `create_email_draft` resolve `allow`; an agent
  with an explicit `deny` keeps it (round-2 MAJ-013, MC-26, D19)

#### Scenario B-47: A real message advances the badge within two cycles
**Traces to**: US-3, AS-8 · **Category**: Happy Path
- **Given** the watcher running against the UAT instance's real IMAP/SMTP server (GreenMail — D37;
  D29/R2-10's live test proved the mechanism on the live drainer)
- **When** a real email arrives and two cycles elapse
- **Then** `last_seen_uid` advances and `unseen_total` rises, and the badge moves — the blindness check
  round-2 MAJ-019 demanded (last_checked + UID advance observable, MC-23)

#### Scenario B-48: Mail marked read by another client still advances the watcher
**Traces to**: US-6, AS-2 · **Category**: Edge Case
- **Given** a new message that another mail client marks `\Seen` before the watcher's cycle
- **When** the cycle runs
- **Then** the watcher still advances `last_seen_uid` and the badge reflects the state — the watcher keys on
  **UID, not UNSEEN** (round-2 MAJ-019, MC-18)

#### Scenario B-49: Agent send with a workspace-file attachment
**Traces to**: US-8, AS-5 · **Category**: Happy Path
- **Given** an agent whose effective `send_email` policy allows it, and a file in the agent's workspace
- **When** the agent calls `send_email` with that file attached
- **Then** the transmitted message carries the file as a MIME attachment (name and bytes match the workspace
  file) and the Sent copy carries it too (MC-32)
- **And** the call's policy resolution is identical to the same call without the parameter (D33, MC-35, T65)

#### Scenario B-50: Agent draft carries attachments into the approval panel
**Traces to**: US-8, AS-5 · **Category**: Happy Path
- **Given** an agent with a workspace file, allowed `create_email_draft`
- **When** it calls `create_email_draft` with the file attached
- **Then** the draft is APPENDed with the file as a MIME part, and the approval panel lists it via
  `MailMessage.attachments` before the human sends
- **And** the panel send transmits the attachment unchanged (FR-034/FR-035 machinery)

#### Scenario B-51: No separate gate for the attachment parameter
**Traces to**: US-8, AS-5 · **Category**: Alternate Path
- **Given** a mailbox whose configure-time fill set `send_email` to `ask` (D19), auto-approve off
- **When** the agent calls `send_email` with an attachment
- **Then** exactly one approval covers message **and** attachments — the tool's own ask; no second
  attachment prompt, no separate policy entry exists (D33, MC-35)
- **And** with the policy resolved to `allow`, the same call (attachments included) runs with no new prompt —
  attaching changes nothing about resolution (MC-35)

#### Scenario B-52: Caps and invalid references reject pre-dial
**Traces to**: US-8, AS-5 · **Category**: Error Path
- **Given** attachment lists at the MC-32 boundaries (10 files pass, 11th fails; ≤ 25 MiB total passes, over
  fails), a reference to an unconditional secret carve-out (rejected at resolve, re-verified at read),
  and a reference to a nonexistent file
- **When** the agent calls any of the three tools with such a list
- **Then** the invalid list fails as a tool error **before any SMTP connection or APPEND** — nothing is
  transmitted, nothing drafted (B-41's pre-dial discipline, MC-32/MC-35); **a reference that merely
  resolves outside the workspace does NOT fail** — it attaches, per the MC-35 open-read rule
- **And** the failure names the offending reference

#### Scenario B-53: Agent read marks read and tags "read by agent"
**Traces to**: US-6, AS-4 · **Category**: Happy Path
- **Given** a message unseen by anyone
- **When** the agent reads it with `read_message`
- **Then** exactly one flag store adds `\Seen` **and** `$OmnipusAgentRead` together (D38, MC-36)
- **And** the envelope reports `read_by_agent=true` and the Mail panel row shows the small "read by agent"
  tag; a human-opened message shows `\Seen` but never the tag

#### Scenario B-54: Keyword-rejecting server degrades to \Seen only, never an error
**Traces to**: US-6, AS-5 · **Category**: Error Path
- **Given** a mail server that rejects custom keywords on STORE
- **When** the agent reads a message with `read_message`
- **Then** the fallback store sets `\Seen` only, the read result is unaffected, and no error surfaces on any
  surface (D38, MC-36)
- **And** the message carries no tag; the keyword is not re-attempted until the process restarts (one WARN
  per process per mailbox)

#### Scenario B-55: The tag never lies about who read
**Traces to**: US-6, AS-4 · **Category**: Edge Case
- **Given** a mailbox holding one agent-read message (keyword set), one human-opened message (`\Seen` via the
  seen endpoint), and one untouched message
- **When** the panel lists the Inbox
- **Then** exactly the agent-read row shows the tag; the human-opened row is read but untagged; the untouched
  row stays unread and untagged
- **And** after the agent reads a message the human had already opened (plain `\Seen`), the store adds only
  the keyword — no duplicate flag write, and the tag appears

---

## 7. TDD plan (tests designed before implementation)

E2E note (D36): Playwright E2E runs the real gateway against the **built-in fake IMAP/SMTP server** — a
small in-tree helper (same go-imap v2 `imapmemserver` module `pkg/email/imapserver_test.go::startMemIMAP`
already uses — no new dependency) plus a minimal SMTP sink, started by the E2E fixture: the fixture binds it
on dynamic ports (`tests/e2e/setup.ts::getFreePort`), points a throwaway gateway's mailbox config at
`127.0.0.1` (the `tests/e2e/fixtures/gateway-process.ts::GatewayProcess` pattern — own port, own
OMNIPUS_HOME), and drives the Mail panel in a real browser on every CI run. The test seeds mail state
through the fake server (APPEND a draft with `X-Omnipus-Draft`, STORE the `$OmnipusAgentRead` keyword)
rather than driving live agent turns — live-LLM flows stay in the conformance lanes, and the
`read_message`→keyword mechanism is asserted server-side by T68. Covered flows (D36): **read**, **open marks
read**, **edit agent draft**, **send with attachment**, **signature**, **Sent copy** (the fake SMTP sink
records the transmission; the fake IMAP holds the Sent APPEND), and the **read-by-agent tag**. IMAP-observable
behavior (flags, APPEND, EXPUNGE, UIDVALIDITY, peek) is asserted against the in-tree real-protocol harness
`startMemIMAP` (round-2 MAJ-004) — a hand-written fake cannot model the server's flag semantics, so
server-side end states are asserted there; fakes remain only for tool-level shape tests. Live-mail
acceptance belongs to the **UAT campaign** (D37 — separate instance against GreenMail, T44) and the
post-landing live smoke (D37).

| Order | Test name (indicative) | Level | Traces to BDD | Description |
|---|---|---|---|---|
| 1 | `TestComposeMultipart_SignatureAndSanitize` | Unit | B-2, B-6 | Markdown → multipart/alternative; sanitizer kills script/on*/javascript: in body **and** signature (MC-2, MC-3); signature on both parts |
| 2 | `TestComposeMultipart_NoSignature` | Unit | B-3 | Empty signature → still multipart (MC-3), no signature block |
| 3 | `TestComposeMultipart_HeaderInjectionGuard` + `TestComposeHeaders_RFC2047` | Unit | B-10, (MC-4) | CRLF never reaches headers; UTF-8 subject/display name encode and round-trip |
| 4 | `TestSignatureSanitizedOnSave` | Unit | B-4 | Signature policy keeps style/tables/https+data img, strips script/handlers/javascript:/forms/iframes; stored value is sanitized |
| 5 | `TestClientSend_AppendsToSent` | `startMemIMAP` | B-7 | SMTP-success path APPENDs the exact message to the resolved Sent folder (always — D18); **server-side end state asserted against the real-protocol harness** (round-2 MAJ-004) |
| 6 | `TestClientSend_AppendFailureSurfaces` | Unit | B-8 | APPEND failure → explicit warning, send not hidden (MC-9) |
| 7 | `TestSendEmailTool_RecipientLists` | Unit (fake) | B-9 | **Three-copy BCC rule** (round-2 MAJ-005, D29/R2-3): BCC addresses go into the SMTP envelope only (RCPT TO); the transmitted message carries **no** Bcc header; the Sent copy **keeps** it; drafts keep it (still editable) |
| 8 | `TestComposeSizeBound` | Unit | B-10 | 1 MiB passes, 1 MiB+1 → 400/tool error, nothing transmitted (MC-22) |
| 9 | `TestClientAppendDraft_SetsDraftFlag` | `startMemIMAP` | B-19, B-23 | Draft APPEND sets `\Draft` + `X-Omnipus-Draft`; no SMTP session opened; **flags asserted server-side** (round-2 MAJ-004) |
| 10 | `TestResolveSpecialUseFolders_OverridesWin` | Unit | B-11 | Special-use (RFC 6154) resolution with config-override precedence (A1) |
| 11 | `TestReadByRef_FoundAndNotFound` | `startMemIMAP` | B-12, B-21 | `uid:`/`mid:` resolution folder-scoped; miss → 404 (MC-7); multi-hit → **highest UID among non-`\Deleted`** (round-2 MIN-005) |
| 12 | `TestReadByMessageID_AfterUIDValidityChange` | `startMemIMAP` | B-36 | `mid:` resolution survives UIDVALIDITY renumbering (recreate the folder in `imapmemserver` to simulate the reset — round-2 MAJ-004's named method) |
| 13 | `TestSendEmailTool_MarkdownBodyPipeline` (+ signature assertion) | Unit (fake) | B-2, B-6 | Tool-level: body treated as Markdown, signature on both parts of the transmitted message; result text unchanged in shape |
| 14 | `TestCreateEmailDraftTool_ResultContract` | Unit (fake) | B-19 | Result JSON contract incl. nullable `chat_link` + reason (MC-11, FR-015) |
| 15 | `TestMailboxSignature_ConfigRoundTrip` | Unit | B-1 | `signature_html` + folder overrides persist via config load/save; legacy config loads unchanged (§2.5) — **no D20 switch exists to persist** (D27) |
| 16 | `TestPutAgentMailbox_SignatureLimit` | Integration (httptest) | B-5 | 16,384-char bound → 400 (MC-1) |
| 17 | `TestMailFoldersEndpoint_PairMissing` | Integration | B-11, B-37 | 404 when agent/pair missing (ADR-033 precedent); 400 unknown slug (MC-5) |
| 18 | `TestMailMessagesEndpoint_PagingHandlesRemovedParam` | Integration | (MC-6, OBS-001) | limit clamp/before_uid pass-through; truncated flag mirrors `SearchResult`; the dropped `unseen_only` param is **rejected-or-ignored explicitly** (round-2 OBS-001) |
| 19 | `TestMailEndpoints_UpstreamTimeout` | Integration | B-14 | Failing/black-hole transport → 502 with sanitized class, raw cause in log only (MC-8, D22) |
| 20 | `TestMailSendEndpoint_MultipartAndSent` | Integration | B-26 | Manual send → same pipeline; `sent_saved` reporting; To/CC/BCC headers (D26) |
| 21 | `TestClientSend_DialBlackhole_ReturnsWithinBound` / `TestClientSend_StallAfterConnect_ReturnsWithinBound` / `TestClientSend_ContextCancel_Aborts` | Unit | (MC-21) | #629 absorbed: dial bound, post-connect stall bound, context cancellation |
| 22 | `TestMailSendEndpoint_SMTPTimeout_502` | Integration | (MC-8) | SMTP failure surfaces as 502 + class within the bound — never a hang |
| 23 | `TestDraftEditEndpoint_SameMessageID` | Integration | B-30 | Edit APPENDs updated draft (same Message-ID), `\Deleted` old copy, link still resolves |
| 24 | `TestDraftSendEndpoint_ApprovalFlow` | Integration | B-31 | Panel send transmits the request's content; Sent APPEND; draft removed; audit event `origin=agent-draft` |
| 25 | `TestDraftSend_StalePrecondition_409` / `TestDraftEdit_StalePrecondition_409` | Integration | B-32, B-35 | Mismatched `uidvalidity:uid` → 409, nothing transmitted — send and edit-save variants (MC-16) |
| 26 | `TestDraftDiscard_DeferredExpunge` | `startMemIMAP` | B-33 | UIDPLUS: UID EXPUNGE removes exactly the target; non-UIDPLUS: `\Deleted` only, listings exclude it (MC-17) — both server shapes against the real-protocol harness (round-2 MAJ-004) |
| 27 | `TestDraftEdit_DeleteFlagFailWarns` | Unit | B-34 | APPEND ok + delete-flag fail → explicit duplicate warning |
| 28 | `TestForeignDraftConversion` | Unit | B-24 | Non-Omnipus draft → editable Markdown derived, loss statement data, Message-ID kept through send |
| 29 | `TestPanelOpenMarksSeen` | `startMemIMAP` | B-27 | Panel open → exactly one seen-endpoint call → `\Seen` server-side; repeat open is a no-op (idempotent); list/read paths never write flags (round-2 MAJ-003, MC-24) |
| 30 | `TestWatcher_NeverMutatesFlags` + `TestWatcher_KeyedOnUIDNotUnseen` | `startMemIMAP` | B-28, B-48, B-15 | Watcher cycle: no flag STORE, no Board task, **no agent turn** (D27), state file UIDs/counts only (MC-18); a message marked `\Seen` by another client still advances the UID state (MAJ-019's named check) — real-protocol harness (round-2 MAJ-004) |
| 31 | `TestMailSummaryEndpoint` | Integration | B-18 | Badge summary matches watcher state; error-state mailbox reports class (MC-23) |
| 32 | `TestMailAuditEvents` | Integration | (MC-19) | Four human actions emit audit events with pair/folder/Message-ID/recipients/origin/hash; **sends carrying attachments record their filenames and sizes in the audit event (D33, MC-19)** |
| 33 | `TestMailRateLimit` | Integration | (MC-20) | 11th mutating mail request in the window → 429 |
| 34 | `TestEffectiveDraftToolPolicy_BothAgentKinds` | Unit | (FR-013) | Seeded core agent **and** operator-created agent, both with enabled mailbox: `create_email_draft` effective policy = `allow` and present in the registry (replaces the round-1 SC-007 grep) |
| 35 | `TestDeepLink_CrossPairDenied` | Integration | B-21 | Unknown agent/workspace or pair without mailbox → 404, no leakage (MC-13) |
| 36 | `TestNoLocalMailStore` | Integration | B-15 | Data-dir sweep after full exercise finds no bodies (MC-12) |
| 37 | `MailTab.spec.ts` — tab opens, picker, badge, error state | E2E (no mail server) | B-13, B-14, B-18 | Tab strip entry, mailbox picker, unreachable-mailbox error surface, badge render |
| 38 | `MailSignatureEditor.spec.ts` | E2E (no mail server) | B-1, B-5 | Signature editor in Connectors: preview, save, bound; **preview shows the https logo without any "Load images" step** (round-2 MIN-001 — operator's own content) |
| 39 | `MailCompose.test.tsx` | Component | B-25, US-5 AS-3 | Field-level validation blocks empty sends; **Reply prefill** (recipient from original, `Re:` subject, `in_reply_to` set — round-2 MIN-009) |
| 40 | `MailPanelRefresh.test.tsx` | Component (fake timers) | B-16 | 30 s refetch while mounted; none when unmounted (D25) |
| 41 | `MailHtmlFrame.spec.ts` | Component | B-17 | Sandboxed frame posture; "Load images" re-mint; `cid:` part route used |
| 42 | `DraftDeepLink.spec.ts` | E2E (stubbed route state) | B-20, B-21, B-22 | Link → panel navigation; not-found state; "Sent on" state |
| 43 | `MailDraftMarkdownRender.test.tsx` | Component | (MIN-009) | Raw HTML in draft Markdown stays inert (`skipHtml`; hostile `<img onerror>` renders nothing) |
| 44 | UAT campaign — separate instance vs GreenMail (D37) + post-landing live smoke | UAT | B-1..B-55 (sample) | **D37: a separate instance built from the feature branch against GreenMail in Docker** (real IMAP/SMTP, several test accounts that mail each other) — **no live passwords, no overlap with the live instance; 2 uat-tester lanes + 2 independent uat-validators**. Coverage sample: signature on received mail, Sent APPEND visible in a mail client pointed at GreenMail (duplicates accepted per D18 on auto-filing providers — the check is "appears", not "appears exactly once"), draft link → panel, edit, send, discard, watcher badge moves on real mail (B-47), **agent send/draft with a workspace-file attachment arrives with the attachment (D33, B-49/B-50)**, **agent read sets the read-by-agent tag through a real server that accepts keywords (B-53)**. **Post-landing (D37): one short live smoke on the live instance with the Dmitri/Aisha mailboxes covers provider-specific behaviour** (keyword support case included; a keyword-rejecting provider exercises the D38 fallback, B-54). Historical note: D30/D32's live test email proved the old drainer (Board task 38 s after the send; its verified gap was success-logging, closed by the watcher's `last_success_at`/"last checked" state, MC-23) |
| 45 | `TestSeenEndpoint_IdempotentNoop204` | `startMemIMAP` | B-27 | Repeat seen call → 204 no-op, no second flag STORE (MC-24) |
| 46 | `TestFetchCommands_PeekOnly` | `startMemIMAP` (command capture) | B-12, B-17 | Captured FETCH commands on list/read/preview: **no non-peek `BODY[]` fetch** (MC-25; round-2 MAJ-003) |
| 47 | `TestMailboxConfigurePolicyFill` | Unit | B-46 | The MC-26 table: absent → ask/allow per tool; explicit `allow`/`ask`/`deny` unchanged; a seeded Admin's `deny` stays (round-2 MAJ-013, D19) |
| 48 | `TestRecipientCap_PreDial` | Unit + handler | B-41 | 50 recipients pass, 51st fails **pre-SMTP** on every send path; de-dup; `net/mail.ParseAddress` (MC-27) |
| 49 | `TestPanelSend_IdempotentDoubleSubmit` | Integration (counting transport) | B-38 | Two submits → one SMTP DATA event; the recorded outcome replays; `draft_cleanup_warning` on the failed-delete path (MC-28, round-2 MAJ-009) |
| 50 | `TestDraftMarkdownPart_RenderHashStale` | Unit | B-24 | `text/markdown` part stored; render-hash match → lossless source served; mismatch → foreign path + loss statement (MC-29, round-2 MAJ-006) |
| 51 | `TestPlainTextPart_ASTDerivation_HardWraps` | Unit | (MC-30, DS-2) | Custom AST text renderer (links `text (href)`, lists indented, code literal); single newlines survive (round-2 MAJ-007) |
| 52 | `TestWatcher_UIDValidityBaseline` | `startMemIMAP` | B-45 | UIDVALIDITY reset → baseline to `UIDNEXT-1`, log once, nothing flagged new, nothing started (MC-31, round-2 MAJ-001) |
| 53 | `TestWatcher_BackoffJitterAuthCap` | Unit (fake clock) | B-43 | 60 s → 2 → 4 … 15 min cap with ±20% jitter; first cycles offset 0–60 s; `auth_failed` at cap; Retry immediate; recovery resets + logs (MC-33, round-2 MAJ-017) |
| 54 | `TestPanelRefresh_CoalescedLogins` | `startMemIMAP` (LOGIN count) | B-44 | 3 tabs / 5 min → ≤ 1 LOGIN per 30 s per mailbox + watcher's own count; **zero** auto dials during backoff (MC-33, D29/R2-9) |
| 55 | `DraftDeepLinkStub.spec.ts` (extends T42) | E2E | B-20 | The redirect stub copies `mailbox`/`folder`/`message` into `openMailPanel` before navigating (MC-31a, round-2 MIN-012) |
| 56 | `TestAttachmentEndpoints_SanitizeAndCaps` | Integration | B-39 | Download serves the part bytes under the sanitized filename; 11th file / > 25 MiB rejected **before any SMTP connection** (MC-32, D28) |
| 57 | `TestForeignDraft_CarryOver` | `startMemIMAP` | B-40 | `keep_attachment_parts` carries the exact parts server-side; the panel listed exactly those; removal is explicit (FR-035, round-2 MAJ-010) |
| 58 | `TestOutboundBodyAllowlist` | Unit | (MC-2) | Links `http`/`https`/`mailto` only; images https-only; `style` stripped from agent Markdown; unknown schemes dropped (round-2 MIN-011) |
| 59 | `TestDial_DNSErrorBoundedRetry` | Unit (injected resolver) | B-14, B-42 | Fails-twice-then-succeeds → one success; always-fails → `dns` class within the bound; auth/TLS never retried (MC-8, MC-33, round-2 MAJ-016) |
| 60 | `TestWatcherLog_RateRule` | Unit (scripted failure window) | (MC-34) | A 13-mailbox failure window yields bounded log volume — first failure + state changes, never per cycle (FR-036, round-2 MIN-015) |
| 61 | `TestMailSummary_NeverLies` | Integration | B-18 | New summary fields present; `last_success_at` null until a cycle succeeds; `next_attempt_at` set in backoff (MC-23, round-2 MAJ-019) |
| 62 | `TestMailPreview_NoRedirectAndWebKitCookie` | Integration | B-17 | Nothing under `/mail-preview/` ever redirects — mirroring `library_preview_no_redirect_test.go` incl. its stdlib-mux positive control, `/api/` sentinel (MC-10(1), sign-off C1); an untrusted `<img src="/api/v1/…">` loads nothing (round-2 CRIT-001) |
| 63 | `TestHtmlPreview_MintOnceNoIMAP` | Integration | B-17 | The `/mail-preview/` serve/part/proxy routes never dial IMAP after mint — one fetch per preview (round-2 MAJ-003, MAJ-008) |
| 64 | `TestSendEmailTool_WorkspaceAttachment` | Unit (fake) | B-49 | Attachment resolves through the tools' workspace-path resolution and rides the MIME structure on the transmitted and Sent copies; MC-32 caps enforced pre-dial (MC-35) |
| 65 | `TestAgentAttachment_SamePolicyResolution` | Unit | B-51 | Effective policy identical with/without the `attachments` parameter; no separate gate, ceiling/fill/inventory entry or auto-approve classification (MC-35, D33) |
| 66 | `TestCreateEmailDraftTool_AttachmentsToPanel` | Unit (fake) + Integration | B-50 | Draft carries attachments as MIME parts; `MailMessage.attachments` lists them in the panel; panel send re-attaches unchanged (FR-034/FR-035) |
| 67 | `TestAgentAttachment_CapsAndInvalidRefPreDial` | Unit | B-52 | 11th file / > 25 MiB / outside-workspace or nonexistent reference → tool error pre-dial, nothing transmitted; the failure names the reference (MC-32, MC-35) |
| 68 | `TestAgentRead_SeenAndKeywordOneStore` | `startMemIMAP` (command capture) | B-53 | Agent `read_message` captures exactly one flag STORE adding `\Seen` **and** `$OmnipusAgentRead` (case-insensitive compare — the memserver lowercases keywords via go-imap v2 `imapmemserver/message.go::canonicalFlag`); the fetch itself is `BODY.PEEK` (MC-36) |
| 69 | `TestAgentRead_KeywordRejectedFallback` | Unit (scripted IMAP server that answers NO to the keyword STORE) | B-54 | Keyword STORE rejected → exactly one `\Seen`-only STORE follows, the read still succeeds with the full message, no error surfaces, the keyword is not re-attempted for the rest of the process (one WARN per process per mailbox), `read_by_agent` stays false (MC-36) |
| 70 | `TestEnvelope_ReadByAgentDerived` | Unit | B-53, B-55 | `read_by_agent` on the envelope/summary is derived from the fetched flag list only — keyword present (any case variant) → true; `\Seen` alone → false; no persisted Omnipus state involved (MC-36, FR-039) |
| 71 | `mail-panel.spec.ts` — fake IMAP/SMTP server (D36) | E2E | B-1, B-7, B-12, B-27, B-30, B-31, B-39, B-53 | Every main mail flow in a real browser per CI run against the built-in fake IMAP/SMTP server: read (B-12), open marks read (B-27), panel edit of an agent draft (B-30) and panel send (B-31), compose send with attachment (B-39) — the fake SMTP sink records the transmission carrying the signature HTML (B-1) and the fake IMAP holds the Sent APPEND (B-7) — plus the read-by-agent tag (B-53). Seeded via APPEND/STORE on the fake server, never live agent turns; new spec file assigned to exactly one shard in `tests/e2e/shards.json` (`scripts/e2e-shards.sh check` fails CI on a missing or double assignment) (MC-36) |
| 72 | `TestMailIsolationPolicy_OwnBuilderTripwires` | Unit (builder output) | (MC-37) | Mail CSP built by its own named builder in its own file; negative assertions per clause: no `'self'`, no `allow-scripts`, no script-src host sources, no `'unsafe-inline'` in script-src, no `https:` in `img-src`, every host source path-confined to `/mail-preview/`; the Library builder is not referenced from the mail path (sign-off C3) |
| 73 | `TestMailIsolationPolicy_NoOriginOmitWarn` | Unit | (MC-38) | No derivable origin → host sources omitted + loud WARN; the policy never contains `'self'`/`https:` in any configuration state (sign-off C4) |
| 74 | `TestMailPreview_SandboxTokenExactBothLayers` | Integration + Component | (MC-39) | CSP directive and iframe attribute each exactly `allow-popups allow-popups-to-escape-sandbox`; the seven banned tokens absent in BOTH layers (sign-off C5) |
| 75 | `TestInboundSanitizer_AnchorNoopenerForced` | Unit | (MC-40) | `<a href="https://evil/">x</a>` renders with `rel="noopener noreferrer"` + `target="_blank"` before token-store insertion; `window.opener` cut after the cross-origin click (sign-off C6) |
| 76 | `TestMailImageProxy_SSRFPinsDialTime` | Integration | (MC-41) | Rebinding: validator-passing hostname resolving private at dial → refused at dial; zero redirects followed; > 5 MiB / > 10 s cut; non-`image/*` refused; `nosniff` + `no-store` re-emitted; no token or `load_remote=false` → refused; non-store URL refused (sign-off C7) |
| 77 | `TestAttachmentDownload_HeaderDiscipline` | Integration | B-39 | Always `attachment` disposition (incl. an `.html` part), `X-Content-Type-Options: nosniff`, extension-derived content type (dangerous-extension disagreement wins), RFC 6266 dual-encoded sanitized filename, no CSP header (MC-42, sign-off C8) |
| 78 | `TestMailPreviewToken_HygieneLibraryPrecedent` | Unit | (MC-43) | 256-bit fail-closed entropy; named TTL; per-session cap refuses (never evicts); expired/unknown/revoked byte-identical 404s; digest-keyed store; logout revocation; `Cache-Control: no-store` on served responses (sign-off C9) |
| 79 | `TestMailPreviewMint_RateLimited` | Integration | (MC-44) | Dedicated per-IP limiter: the over-budget mint → 429 + `Retry-After`, zero IMAP dials for the refused call (sign-off C10) |
| 80 | `TestMailServeRoutes_TokenOnly404Only` | Integration | B-17 | Serve routes accept only the token: session cookie without token → 404 (never 401); expired/unknown/revoked byte-identical 404s; no `frame-ancestors`/`X-Frame-Options` emitted (MC-45, sign-off C11) |

### 7.1 Test datasets

| Dataset | Rows (boundary → edge → error → happy) | Traces to |
|---|---|---|
| DS-1 Signature values | `""` (no signature), 16,384 chars (max), 16,385 (reject), valid HTML, hostile HTML (script/on*/javascript:), **real-world signature with inline `style` + https logo (must survive sanitization intact)** | B-1, B-3, B-4, B-5 |
| DS-2 Markdown bodies | empty body (reject), heading+link+code block, raw HTML/script injection, body at 1 MiB and 1 MiB+1, unicode subject/display name (RFC 2047), CRLF injection attempt, **multi-line plain text (single newlines survive hard wraps — round-2 MAJ-007)** | B-6, B-10, (MC-2, MC-4, MC-22, MC-30) |
| DS-3 Folder paging | `limit=0` (default 20), `limit=1`, `limit=101` (clamp 100), `before_uid=1` (empty), `before_uid=<min>` (page boundary), empty folder, **removed `unseen_only` param (explicit ignore/reject — round-2 OBS-001)** | B-11, (MC-6) |
| DS-4 Message addressing | existing Message-ID, missing Message-ID (404), message without Message-ID header (listed `message_id: null`), percent-encoded angle brackets, UIDVALIDITY-changed mailbox, **duplicate Message-ID across folders (folder-scoping resolves)**, **spoofed inbound Message-ID matching a draft (drafts resolve only inside `drafts`)**, **Message-ID > 998 bytes / malformed (400)** | B-12, B-21, B-36 |
| DS-5 Send outcomes | SMTP ok + APPEND ok, SMTP ok + APPEND fail, SMTP fail (no APPEND attempted), SMTP dial black-hole (bounded), SMTP stall after connect (bounded), invalid recipient (reject client-side), To/CC/BCC combinations, **exactly 50 recipients (pass) and 51 (reject pre-dial — round-2 MAJ-015)** | B-7, B-8, B-9, B-26, B-41 |
| DS-6 Draft lifecycle | create ok, create with missing to (reject), draft deleted before link click, draft in nonexistent pair (404), **foreign (owner-authored) draft**, **stale-precondition send (409)**, **edit: APPEND ok + delete-flag fail (warning)**, **discard under non-UIDPLUS server**, **foreign draft with attachments (carry-over listed before send — round-2 MAJ-010)**, **stale `X-Omnipus-Render-Hash` (foreign path + loss statement)** | B-19, B-21, B-23, B-24, B-30..B-40 |
| DS-7 Mailbox roster | 0 mailboxes (empty state), 1 mailbox (no picker), 2 mailboxes (picker), mailbox enabled but password unresolvable (skip + explicit state) | B-11, B-13, B-14 |
| DS-8 Watcher cycles | no new mail (expected: state unchanged), new mail (expected: `last_seen_uid`/`unseen_total` advance), 30+ new messages in one cycle (**expected: state advances once, badge shows one count — no per-message work**), mailbox in error (expected: class stored, backoff starts per D29/R2-8), **UIDVALIDITY reset (expected: silent re-baseline, log once — round-2 MAJ-001)**, **mail read by another client (expected: UID state still advances — UID-keyed, round-2 MAJ-019)**, **backoff schedule with fake clock (expected: 60s→2→4…15min ±20% jitter, auth_failed at cap)**, **3-tab coalescing (expected: ≤1 LOGIN/tick, zero auto-dials in backoff)** | B-18, B-28, B-29, B-43..B-45, B-48 |
| DS-9 Agent attachment inputs | 1 workspace file (happy), 10 files at the cap (pass), 11th (reject), 25 MiB total boundary (pass / over rejects), reference to a **secret carve-out** (rejected at resolve, re-verified at read), reference to a file **outside the workspace** (attaches — open-read rule, MC-35; the attachable verdict is pinned), nonexistent file (read-time failure pre-dial), unicode filename, filename with path separators (sanitized per MC-32) | B-49, B-50, B-52 |
| DS-10 Agent read flag states | keyword supported (STORE succeeds → `\Seen` + keyword in one store → tag), keyword rejected (fallback `\Seen`-only STORE, in-process suppression until restart, one WARN per process per mailbox), `\Seen` also rejected (visible failure — a read-only agent mailbox is unsupported per MC-36), case-variant keyword (`$omnipusagentread` on the server vs `$OmnipusAgentRead` compared → still derived true), the three reader states (agent-read = both flags; human-opened = `\Seen` only, no keyword → no tag; untouched = neither flag) | B-53, B-54, B-55 |

### 7.2 Regression impact

Existing behaviors that MUST be preserved (or are deliberately deleted):

- **The drainer is deleted, not protected** (#631, D20): `pkg/email/drainer.go`, `pkg/heartbeat/mailbox_drain.go`
  and both wiring sites (`gateway_boot.go`, `gateway_reload.go`) go, together with their suites — after this
  spec lands, no code or test references `Drainer`. The drainer's *task-creation* behavior class is retired
  by decision (D20), and the replacement watcher is covered by MC-18/DS-8. **D32 (2026-09-25 live test): the
  old drainer works** — Dmitri's test mail (17:05:02Z) became Board task "Email: Omnipus mail test
  20260925T170502Z" at 17:05:40Z, and the agent handled it to done — the agent mailboxes had simply received
  no mail before. Its verified defect is observability: it logs nothing on success. The watcher's per-mailbox
  "last checked" state (`last_success_at`, MC-23/T61, B-47) closes exactly that gap; the deletion is by
  decision (#631/D20/D27 — no task path, no turn path), never by failure.
- `read_inbox` / `search_email` / `read_message` semantics unchanged (folder=INBOX only, mail read by
  `read_message` **is still marked \Seen**, envelope-only lists, pagination cursors) — `pkg/tools` email
  suites stay green; only recipient-list (D26), description-text (D3) and attachment-parameter (D33)
  assertions change (MC-15). **D38 changes the mechanism, not the semantics**: `read_message` switches from
  the implicit `\Seen` of a non-peek `BODY[]` fetch to `BODY.PEEK` plus one explicit STORE of `\Seen` and
  `$OmnipusAgentRead` (T68/T69, MC-36) — the mailbox owner (the reading agent, ADR-033) still finds the
  message marked read either way.
- Send-policy resolution is **unchanged** (D15): the ceiling ships as it ships today; built-in roles keep
  `ask` via the ADR-090 inventory; the only behavior change is D19's configure-time fill (replacing
  `grantEmailToolAllows`), which writes **only** where the agent has no explicit entry. T34 asserts both agent
  kinds; the greenfield ruling means no migration of previously allow-filled entries.
- Mailbox config round-trip including the strict legacy/nested shape rule
  (`pkg/config` mailbox migration tests) must keep passing with the new fields present.
- The SPA embed/CSP surface is untouched except the new `/mail-preview/` routes (§2.3a), which carry their own
  headers and no new `script-src` relaxation — `pkg/gateway/embed.go` policy untouched. **The Library
  isolation policy and its tests are untouched** — the mail CSP is built by its own named builder in its own
  file (MC-37, sign-off C3), and the shipped Library-string tripwire keeps guarding the Library file.
- `smtp.Dial`/`tls.Dial` replacement by context-dialers is behavior-preserving on the happy path — existing
  `pkg/email` send tests stay green unmodified (MC-21 adds the bounded-failure tests).
- **The `startMemIMAP` migration is additive**: rows moved onto the real-protocol harness (T5, T9, T11, T12,
  T26, T29, T30 + the new MC-18/24/25/28/31/33 rows) keep their scenario traces and their suite names;
  existing `pkg/tools` fake-based shape tests stay green. The harness already exists in-tree
  (`pkg/email/imapserver_test.go::startMemIMAP`), so no new dependency enters the tree (round-2 MAJ-004).
- **The D19 fill gets its own regression table** (T47, round-2 MAJ-013): absent → ask/allow per tool; explicit
  allow/ask/deny unchanged; seeded Admin `deny` stays. A regression that keeps writing `allow` for
  `send_email` (today's `grantEmailToolAllows` behavior for a missing key) must fail this table.

---

## 8. Functional requirements

- **FR-001**: The system MUST store one optional HTML signature per (agent, workspace) mailbox, configurable
  through the existing mailbox panel and validated to ≤ 16,384 chars (MC-1). [D2, ADR-033]
- **FR-002**: The signature editor MUST show a live rendered preview of the signature as it is edited. [D2]
- **FR-003**: The signature MUST be sanitized on save with a dedicated signature policy (keeps inline
  `style`, tables, `img` over https/data; strips script, event handlers, `javascript:`, forms, iframes); the
  stored value is the sanitized one and the PUT returns what was stored. Agents MUST NOT author the signature
  or raw HTML; signature application happens server-side at compose time (MC-2). [D3; round-1 MAJ-008;
  **security-lead review of both sanitizer policies is a pre-implementation gate — the 2026-09-26
  sign-off-with-conditions is that review's delivery to date; its inbound-sanitizer conditions are absorbed
  as MC-40/T75, and any residual signature-policy condition would come back through the same gate**]
- **FR-004**: The system MUST render agent message bodies (`send_email`, `reply`, `create_email_draft`) from
  Markdown into sanitized `text/html` plus derived `text/plain` (multipart/alternative, MC-3) — the plain
  part derived from the same goldmark AST as the HTML with hard wraps, so single newlines survive (MC-30).
  [D3; round-2 MAJ-007]
- **FR-005**: The system MUST append the mailbox signature to both parts of every outgoing message from that
  mailbox (agent send, reply, draft-when-sent, panel send, manual send). [D2, D3, D9, D12]
- **FR-006**: The system MUST APPEND every successfully sent message to the mailbox's Sent folder — always,
  on every provider, with no provider detection (D18) — and MUST surface an explicit warning when that APPEND
  fails after SMTP success (MC-9). Accepted consequence (documented, D18): on providers that auto-file sent
  mail (e.g. Gmail), the owner sees two Sent copies. [D7, D18; round-1 MAJ-004 disposition: rejected per D18]
- **FR-007**: The Mail surface MUST be a workspace tab entry opening a docked Mail panel beside chat plus a
  fullscreen pop-out (the Library's hosting shape). [D4, D11]
- **FR-008**: The Mail surface MUST present exactly the folders Inbox, Sent, Drafts, addressed by stable slugs
  (`inbox`/`sent`/`drafts`), with server-folder resolution and per-mailbox overrides settable through the
  configure request and the panel's advanced fields (A1, MC-5). [D5; round-1 MIN-001]
- **FR-009**: The Mail surface MUST read all message content live from the mail server at request time and
  MUST NOT persist message bodies (MC-12); the only persisted mail-adjacent state is the watcher state file
  of FR-033 (UIDs/counts/error class — no content). The drainer's Message-ID ↔ Board-task mapping is retired
  with the drainer itself (#631, D20 — no task mapping is recreated: round-2 MIN-010). Session transcripts
  keep email text as every tool does; user docs state it plainly. [D6 as scoped by D21; round-2 MIN-010]
- **FR-010**: The Mail surface MUST offer a mailbox picker when a workspace has more than one mailbox, fed by
  `GET /api/v1/mailboxes` filtered to the current workspace, with the selection kept in per-workspace
  sessionStorage. [D4; round-1 MIN-008]
- **FR-011**: New REST endpoints and schemas MUST match §2 exactly, contract-first per Hard Constraint #8.
  [D4–D9, D12, D17–D26]
- **FR-012**: A new `create_email_draft` tool MUST APPEND a `\Draft`-flagged message carrying `X-Omnipus-Draft`
  to the mailbox's Drafts folder, generate its own Message-ID (`<random-128-bit@from-domain>`, kept through
  edits and send), never send, and return `message_id` + `uid`/`uidvalidity` + `chat_link` (nullable with
  reason) (MC-11, MC-14). [D8, D12; round-1 MIN-004, MAJ-013]
- **FR-013**: `create_email_draft` MUST be wired at all **seven** touch points of §2.7 (ceiling default, D19
  fill set, role inventory grants, seed literal, auto-approve classification, `EmailToolset`, and the #7
  prompt/documentation touch points — `pkg/coreagent/prompts_adr090.go` prompt text and the `Mailbox.yaml`
  `enabled` field description), and reachability is asserted behaviorally for a seeded core agent and an
  operator-created agent (T34). [Hard Constraint #6; round-1 MAJ-003; round-2 MIN-008]
- **FR-014**: Clicking a draft chat link MUST open the draft in the preview side panel in the same tab; the
  panel MUST show an explicit not-found state when the draft no longer resolves in Drafts, and a read-only
  "Sent on \<date\>" state when the Message-ID resolves in Sent (MC-13). [D8, D12; round-1 MIN-006]
- **FR-015**: The `chat_link` MUST be an absolute http(s) URL derived exactly like `serve_web` derives its
  origin (http allowed; `pkg/tools/web_serve.go` precedent); when no origin is derivable the tool MUST still
  succeed and return `chat_link: null` with a stated reason. The SPA matches deep links by hash-route
  pattern, not origin. Chat MUST also render a deterministic "Open draft" action from the tool result, so the
  click path does not depend on a model-reproduced URL. [D8; round-1 MAJ-013, OBS-003]
- **FR-016**: `send_email`/`reply` policy resolution MUST remain today's configurable model (D15): they ride
  the global ceiling as shipped, built-in roles carry `ask` via the ADR-090 inventory, and the operator
  configures per agent. Enabling a mailbox MUST fill `ask` for the send tools and `allow` for the read tools
  and `create_email_draft` — only where the agent has no explicit entry (D19); the deny→allow overwrite of
  `grantEmailToolAllows` is deleted. Accepted risk (documented): an agent whose policy resolves `allow` can
  send without a prompt; the draft flow is the designed safe path. The fill's regression table is T47 (B-46).
  [D15, D19; round-1 CRIT-002 disposition: rejected per founder decision; round-2 MAJ-013 — fill pinned by
  T47/B-46]
- **FR-017**: Humans MUST be able to compose and send a message from the Mail surface through the same
  multipart + signature pipeline (FR-004/FR-005 apply). [D9]
- **FR-018**: Every mail-backed surface MUST render upstream mail failures as explicit, class-named error
  states with a retry affordance (MC-8) and MUST log the raw upstream cause server-side with structured
  fields (pair, folder, operation, duration, error class) — failures are never silent in the panel or the
  logs. Recurring identical failures follow the FR-036 rate rule (one line per mailbox per backoff step with
  a suppressed counter — MC-31b, T60): a 13-mailbox DNS window produces bounded log volume, not a per-cycle
  storm. [D22; round-1 MIN-010, MIN-011; round-2 MIN-015]
- **FR-019**: Incoming-message HTML MUST render in a sandboxed frame without script execution, forms or
  same-origin; remote content MUST be blocked by default with a per-message "Load images" action that re-mints
  the token with `load_remote=true` (D17); the response MUST carry the full MC-10 header set (incl.
  `Referrer-Policy: no-referrer`, `form-action 'none'`, token binding/TTL); inline `cid:` images render via
  the token-scoped part route. **The security-lead pre-implementation review of the exact header set was
  delivered 2026-09-26 as a SIGN-OFF WITH CONDITIONS; its eleven conditions C1–C11 are absorbed in this
  revision as binding constraints (MC-10 normative block, MC-37–MC-45) with tests T72–T80.**
  [D13, D17; round-1 MAJ-007; security sign-off 2026-09-26]
- **FR-020**: Read state is IMAP `\Seen`, and the writers are exactly two, both explicit: the panel's
  mark-read endpoint (§2.3 `POST …/messages/{ref}/seen` — once per panel open per message, idempotent,
  MC-24) and agent `read_message` (existing behavior unchanged). The panel's list never marks read; every
  list/read/preview fetch runs `BODY.PEEK[]` or `EXAMINE` (MC-25). There is no watcher-triggered turn to
  keep unread — mail stays unread until a human (or the agent, actively) reads it. [D20 as amended by D27;
  round-1 CRIT-001; round-2 MAJ-012]
- **FR-021**: The draft panel MUST support view, edit (To/subject/body — D23; foreign drafts included, with
  the formatting-loss statement — D24), send (the approval — D12), and discard; sending MUST carry the exact
  displayed/edited content plus a uidvalidity+uid precondition and MUST return 409 on staleness (MC-16).
  [D12, D23, D24; round-1 MAJ-001, MAJ-002]
- **FR-022**: All send paths (agent tools and human compose) MUST accept To/CC/BCC recipient lists (D26).
  BCC follows the **three-copy rule** (round-2 MAJ-005, resolved by D29): non-BCC recipients' transmitted
  copies never carry a `Bcc` header — the BCC visibility is envelope-only for them; the Sent APPEND copy
  **keeps** the `Bcc` header, so the owner's own record shows who was BCC'd; drafts keep their `Bcc` header
  while editing. No recipient of the transmitted copy — including any "show original" view on a non-BCC
  recipient's side — can see the BCC list. [D26; round-1 Q4 disposition; D29/R2-3 (round-2 MAJ-005)]
- **FR-023**: The mailbox drainer must be deleted (closes #631) and replaced by a new-mail watcher that MUST
  NOT change any flag, MUST NOT create Board tasks, and MUST NOT start any agent turn (D27). Watch state is
  **UID-keyed** (round-2 MAJ-019): the watcher tracks the highest-seen UID per mailbox, not unread flags, so
  a message another client already marked read still counts as new to the human's badge. First run on a
  mailbox baselines at the folder's `UIDNEXT-1` (existing mail is never flagged as a backlog), a `UIDVALIDITY`
  change re-baselines silently and logs once (MC-31); it advances the per-mailbox state file (FR-033) and
  drives the Mail tab unread badge via the summary endpoint. Cycles run every 60 s (today's cadence, A8)
  under the FR-027 bounds and FR-037's backoff. [D16, D20; round-1 CRIT-001; round-2 MAJ-001, MAJ-019]
- **FR-024**: (Superseded by D27.) No path from an email to an agent turn exists: the watcher MUST NOT
  dispatch, queue, or enqueue work into any agent session, task system or heartbeat, and an email MUST NOT
  appear as a trigger source anywhere (MC-18, B-28, B-29). The agent uses the email tools actively inside
  turns started by humans/tasks/heartbeats — mail is not a trigger, and **agents handle mail only when
  asked, or through a heartbeat / scheduled task the operator configures — nothing about mail is automatic
  (D31)**. The D20 switch "Let the agent handle new
  mail" and its `MailboxConfigureRequest` wiring are removed outright (greenfield — no deprecated field
  survives; D27). [D27, D31; round-2 CRIT-002 closed by removal]
- **FR-025**: Human send, panel send, panel edit and panel discard MUST each emit an audit event carrying
  pair, folder, Message-ID, recipient addresses (incl. Bcc), origin (`human`\|`agent-draft`\|`owner-draft` —
  round-2 MIN-006: a panel send of a foreign draft), argument hash and outcome.
  [round-1 MAJ-011; round-2 MIN-006; `pkg/audit::argshash`, `rest_preview_audit.go` precedent]
- **FR-026**: The mutating mail routes MUST be rate-limited per IP (10 requests/minute sliding window,
  MC-20 — reviewer-challengeable constant, A10) through a **dedicated `mailMutationLimiter` instance**, never
  a shared one — a shared bucket would let mail-mutation bursts (attachment uploads, bulk discard) starve
  every other API endpoint (round-2 MIN-004). [round-1 MAJ-011; round-2 MIN-004]
- **FR-027**: `Client.Send` MUST honour the caller's context; SMTP dial MUST use bounded context dialers and
  every SMTP command phase MUST run under a deadline bounded by `commandTimeout`; the Sent APPEND runs under
  the same context; IMAP dial gets the same treatment — **context-aware dial** with a bounded dial deadline
  plus the FR-037 bounded name-resolution retry (round-2 MAJ-016), and the late-connection path closes its
  late connection (all of #629). MC-21/MC-8 test the bounds. [D21 closes #629; round-1 MAJ-015; round-2
  MAJ-016]
- **FR-028**: Messages MUST be addressed folder-scoped: `ref` = `uid:<uidvalidity>:<uid>` or
  `mid:<Message-ID>` (chat links); `mid:` resolution searches only the addressed folder among **non-`\Deleted`**
  messages, **highest UID** on multi-hit, never INTERNALDATE; Message-IDs validated as `<…@…>`, ≤ 998 bytes,
  no CR/LF (MC-7). [D21 adopts round-1 MAJ-005; round-2 MIN-005]
- **FR-029**: Omnipus drafts MUST be APPENDed as fully rendered multipart/alternative (rendered body +
  signature at draft time, so the owner's own client sees a real draft — round-1 MAJ-006 option 1) with the
  Markdown source stored as a **`text/markdown` MIME part** (multipart/mixed wrapper) plus
  `X-Omnipus-Render-Hash` binding it to the rendered text part — never Markdown-in-a-header, which a mail
  client can fold, strip or rewrite (round-2 MAJ-006); a hash mismatch marks the draft foreign (FR-030) so
  owner edits are never silently discarded; panel send always re-renders from the (possibly edited)
  Markdown. [D12, D24; round-2 MAJ-006]
- **FR-030**: Drafts without `X-Omnipus-Draft` (or with a stale `X-Omnipus-Render-Hash`) MUST still be fully
  editable and sendable (D24): the panel derives editable Markdown from the HTML (or plain) part, states
  plainly in the UI what may be lost (styling, images, table layout) and what is preserved (text, headings,
  lists, links), and sending re-renders through the Markdown pipeline keeping the Message-ID. A foreign
  draft's **attachments carry over by part reference** (listed before send, FR-034/FR-035) and its threading
  headers (`In-Reply-To`/`References`) are preserved on send (round-2 MAJ-010). [D24; round-1 MAJ-006;
  round-2 MAJ-010]
- **FR-031**: Outbound bodies MUST be bounded at 1 MiB (400/tool error beyond, MC-22); non-ASCII subjects and
  display names MUST be RFC 2047-encoded (MC-4). [round-1 MIN-007]
- **FR-032**: Draft/old-copy deletion MUST use `UID EXPUNGE` where the server supports UIDPLUS, else store
  `\Deleted` only (deferred expunge); Omnipus MUST never issue a non-UID `EXPUNGE`; listings MUST exclude
  `\Deleted` messages (MC-17). [round-1 MAJ-001]
- **FR-033**: Watcher state MUST live in per-mailbox state files under the data dir (atomic writes) holding
  only `uidvalidity`, `last_seen_uid`, `unseen_total`, `last_ok`/`last_success_at`, `last_error_class` and
  `next_attempt_at` — never message content (D6, MC-12, MC-23); the state file is **deleted when the mailbox
  is deleted** (round-2 MIN-007); the summary endpoint reports `ok` honestly — `last_success_at` is null when
  no cycle has ever succeeded. [D20; round-2 MIN-007, MAJ-019]

- **FR-034** (D28): Attachments are supported on every message: **download** from any message via
  `GET …/messages/{ref}/attachments/{partIndex}` (§2.3; 200 part bytes / 404 unknown part / 502 upstream,
  `Content-Disposition` filename sanitized); **attach on compose and draft edit** (incl. externally started
  drafts — carry-over per FR-035); caps per MC-32: ≤ 10 files and ≤ 25 MiB total decoded, enforced
  pre-dial. Agent-tool attachments are resolved by **D33 → FR-038** (no longer open).
  [D28; round-2 MAJ-010, MAJ-015]

- **FR-035**: A foreign draft's attachments MUST carry over by **part reference** (`partIndex` into the
  fetched structure), listed in the panel before send; sending re-attaches the referenced parts byte-for-byte
  where they resolve and **fails closed** — naming a part that no longer resolves is a 400, never a silent
  drop or a truncated attachment (round-2 MAJ-010). Threading headers (`In-Reply-To`/`References`) of the
  foreign draft are preserved on send. [D28; round-2 MAJ-010]

- **FR-036**: Watcher/failure logging MUST follow a rate rule (round-2 MIN-015/007): first failure of a
  class per mailbox at WARN with full detail; while the class persists, at most one summary line per backoff
  step with a suppressed-count; one INFO on recovery; request-path failures log once per request. A
  13-mailbox failure window (D22's live shape) produces bounded log volume (MC-34, T60). [D22; round-2
  MIN-015]

- **FR-037**: Mail connections MUST be resilient without masking failures (D29/R2-8, R2-9; round-2
  MAJ-016..018): context-aware dial (FR-027) whose bounded name-resolution retry (3 attempts, 250 ms → 1 s)
  fires **only** on resolution failures — never auth or TLS; per-mailbox exponential backoff **60 s → 2 → 4
  … cap 15 min with ±20% jitter**, first cycles randomly offset 0–60 s so same-host mailboxes never align;
  `auth_failed` backs off to the cap; the manual Retry bypasses backoff for that one request; identical
  concurrent refreshes **coalesce** (N tabs = 1 login); no automatic dial while a mailbox is backing off.
  No cause is asserted for D22's DNS windows (D30) — the schedule is specified independent of cause.
  [D29; D30; round-2 MAJ-016, MAJ-017, MAJ-018, OBS-004]

- **FR-038** (D33): The agent mail tools (`send_email`, `reply`, `create_email_draft`) MUST accept an optional
  `attachments` parameter of **file references**, resolved server-side by the same path resolution the generic
  file tools use (`pkg/tools/resolvepath.go::ResolvePath`) **under its open-read rule**: the unconditional
  secret carve-outs are refused **at resolve time and re-verified at I/O time**
  (`recheckUnrestrictedCarveOut`) — secrets are never attachable — and **every other readable file is
  attachable, including files outside the workspace root** (D33 "no extra guard rails"; no outside-workspace
  gate — the send_file precedent rejected a path gate as bypassable; security sign-off C2/F5). A reference to
  a nonexistent file fails when read at compose time — a tool error before any SMTP connection or APPEND,
  naming the reference. The parameter MUST be governed by **exactly the tool's own policy resolution**: no
  separate approval gate, no separate ceiling/fill/inventory entry, no attachment-only cap — only the general
  message limits already in this spec apply (MC-32 caps, MC-22 body bound, MC-27 recipient cap), all enforced
  before dialing. Attachments MUST ride the message as MIME parts — a draft included, so the approval panel
  lists exactly what would be sent — and audit/transcript records MUST carry attachment filenames and sizes
  like any send (MC-19, MC-35). **D31 context**: these tools are used when the agent is asked, or inside an
  operator-configured heartbeat/scheduled task — mail is never the trigger. [D33, D31; security sign-off
  C2, 2026-09-26]
- **FR-039** (D38): Agent `read_message` MUST mark the message read by fetching with `BODY.PEEK` and issuing
  exactly **one** flag STORE that adds `\Seen` **and** the `$OmnipusAgentRead` keyword. If the server rejects
  the keyword, the fallback MUST be exactly one `\Seen`-only STORE with the read still succeeding — no error
  to the agent, no tag in the panel, and the keyword not re-attempted until process restart (one WARN per
  process per mailbox). If the server rejects `\Seen` too, that is a visible failure (a read-only agent
  mailbox is unsupported). `read_by_agent` MUST be **derived** from the fetched flag list at read time —
  compared case-insensitively, since real servers differ in keyword case handling — and never persisted by
  Omnipus (D6). A human opening a message via the panel MUST set `\Seen` only, no keyword. [D38, D20, D6]

## 9. Success criteria

- **SC-001**: With a signature configured, 100% of outgoing messages from that mailbox (all five send paths:
  `send_email`, `reply`, panel send, manual send, and a sent draft's Sent copy) carry the signature on both
  MIME parts — verified by unit + integration suites, the D36 E2E fake server, and the D37 UAT campaign.
- **SC-002**: Zero occurrences of `<script`, `on*=` handlers or `javascript:` URIs survive the renderer or
  the signature-save policy across DS-1's and DS-2's hostile rows.
- **SC-003**: Every send with successful SMTP results in a Sent-folder APPEND success or an explicit warning —
  zero silent partial sends in DS-5.
- **SC-004**: With the Mail panel open, the folder list and badge state refetch within **30 seconds** of the
  last fetch (D25) and no panel-initiated refetch fires while the panel is closed; **the Mail-tab badge
  refreshes every 60 s from the saved watcher state — zero IMAP logins attributable to badge refreshes**
  (D29/R2-5, B-18, T54); no message body bytes are written under the data dir during the DS-3/DS-4 exercise
  (MC-12).
- **SC-005**: A draft created by `create_email_draft` is openable from its chat link or the tool-result
  "Open draft" action in ≤ 2 clicks (click → panel shows draft), including after a UIDVALIDITY change (DS-4).
- **SC-006**: With the mail server unreachable, all mail surfaces (folders, messages, sends) show an explicit
  class-named error within one bounded request round-trip — zero silent-empty renders in the DS-7 rows and
  zero hangs in the DS-5 SMTP rows; the **`dns` class is among the visible, named states** (D30 — a DNS
  window is a named error, never a silent hang) (MC-8, MC-21, T59).
- **SC-007**: Behavioral reachability (T34): for a seeded core agent **and** an operator-created agent, both
  with an enabled mailbox, `create_email_draft`'s effective policy resolves to `allow` and the tool is
  present in the agent's registry; additionally `grep -rl '"create_email_draft"' pkg/coreagent/ pkg/config/
  pkg/gateway/ pkg/tools/` returns ≥ 4 files — the Definition-of-Done grep made stronger (registration,
  ceiling, fill set, inventory/seed) and exempt from test files (`*_test.go` excluded).

- **SC-008** (D28): Attachments work end to end: a download serves the exact part bytes under a sanitized
  filename (T56); an 11th attachment or > 25 MiB total is rejected **before any SMTP connection** (T56);
  a foreign draft's attachments are listed before send and arrive with the message (T57); nothing is
  silently dropped or truncated (FR-034/FR-035).

- **SC-009** (D29/D30): Resilience is measurable: with DNS resolution forced to fail twice then succeed, the
  panel recovers within the bounded retry window (T59); with the server down for 20 minutes, the watcher
  backs off 60 s → 2 → 4 … 15 min cap with ±20% jitter, the badge shows `next_attempt_at`, log volume stays
  bounded (T60), and a manual Retry succeeds the moment the server answers (T53); `ok` never lies (T61).

- **SC-010** (D33): Agent attachment capability works end to end: an agent can attach a workspace file on
  `send_email`/`reply`/`create_email_draft` (B-49/B-50); the approval panel lists an agent-attached draft's
  files before send; the 11th file / > 25 MiB / secret-carve-out reference fails **pre-dial** (T67); a
  reference outside the workspace **attaches** (open-read rule pinned, MC-35); the
  effective policy of an attachment-bearing call is identical to the attachment-less call (T65); audit and
  transcript records carry attachment names and sizes (T32/T64).

- **SC-011** (D38): Agent reads are distinguishable from human reads everywhere the state is shown: agent
  `read_message` sets `\Seen` + `$OmnipusAgentRead` in one STORE (T68), the keyword-rejecting fallback keeps
  the read working with `\Seen` only and no tag (T69), `read_by_agent` is derived from the flag list
  (T70), the Mail panel shows the "read by agent" tag exactly on agent-read rows (T71/E2E, T44/UAT), and a
  human panel open never produces the tag (B-55).

## 10. Traceability matrix

| Requirement | User story | BDD scenario(s) | Test(s) |
|---|---|---|---|
| FR-001 | US-1 | B-1, B-5 | T15, T16, T38 |
| FR-002 | US-1 | B-1 | T38 (editor preview), UAT T44 |
| FR-003 | US-1, US-2 | B-4, B-6 | T1, T4 |
| FR-004 | US-2, US-5, US-7 | B-6, B-26, B-31 | T1, T13, T20, T24 |
| FR-005 | US-1, US-2, US-5, US-7 | B-2, B-6, B-26, B-31 | T1, T2, T13, T20, T24 |
| FR-006 | US-2, US-5, US-7 | B-7, B-8, B-26, B-31 | T5, T6, T20, T24 |
| FR-007 | US-3 | B-11, B-18 | T37 (tab), UAT T44 |
| FR-008 | US-3 | B-11, B-37 | T10, T17 |
| FR-009 | US-3 | B-12, B-15 | T36, T44 |
| FR-010 | US-3 | B-13 | T37 |
| FR-011 | US-3..US-7 | all endpoint scenarios | T16–T26, T31–T33 |
| FR-012 | US-4 | B-19, B-23, B-36 | T9, T12, T14 |
| FR-013 | US-4 | (policy resolution — no user-visible scenario; asserted behaviorally) | T34, SC-007 |
| FR-014 | US-4 | B-20, B-21, B-22, B-36 | T35, T42 |
| FR-015 | US-4 | B-19, B-20, B-36 | T14, T42, DS-4 |
| FR-016 | US-4 | B-23, B-29, B-46 | existing send approval suites (green) + T34, T47 |
| FR-017 | US-5 | B-26, B-25 | T20, T39 |
| FR-018 | US-3, US-5, US-7 | B-14, B-14a, B-14b, B-32 | T19, T22, T37, T60 |
| FR-019 | US-3 | B-17 | T41, plus the MC-10 handler tests + **T62, T63, T72–T80 (sign-off C1/C3–C11)** — sign-off delivered 2026-09-26, conditions absorbed |
| FR-020 | US-6 | B-27, B-28, B-29 | T29, T30, T45, T46 |
| FR-021 | US-7 | B-30, B-31, B-32, B-33, B-34, B-35, B-38, B-24 | T23, T24, T25, T26, T27, T28, T49 |
| FR-022 | US-2, US-5 | B-9, B-26 | T7, T20 |
| FR-023 | US-6, US-3 | B-15, B-18, B-28, B-29, B-45, B-48 | T30, T31, T36, T52, T61 |
| FR-024 | US-6 | B-28, B-29 | T30 |
| FR-025 | US-5, US-7 | B-31, B-33 | T24, T32 |
| FR-026 | US-5, US-7 | (route-level) | T33 (dedicated `mailMutationLimiter` — MIN-004) |
| FR-027 | US-2, US-5, US-7 | B-14 (SMTP leg) | T21, T22 |
| FR-028 | US-3, US-4 | B-12, B-21, B-36 | T11, T12, T35 |
| FR-029 | US-4, US-7 | B-19, B-24, B-30 | T9, T23, T28, T50 |
| FR-030 | US-4, US-7 | B-24 | T28, T50 |
| FR-031 | US-2, US-5 | B-10 | T3, T8 |
| FR-032 | US-7 | B-30, B-33, B-34 | T23, T26, T27 |
| FR-033 | US-6, US-3 | B-15, B-18, B-45 | T30, T36, T52, T61 |
| FR-034 | US-8 | B-39 | T56, T77 (attachment download header discipline — MC-42) |
| FR-035 | US-8, US-7 | B-40 | T57 |
| FR-036 | US-6, US-3 | B-43 | T60 |
| FR-037 | US-3, US-6 | B-14a, B-14b, B-42, B-43, B-44 | T53, T54, T59 |
| FR-038 | US-8 | B-49, B-50, B-51, B-52 | T64, T65, T66, T67 |
| FR-039 | US-6 | B-53, B-54, B-55 | T68, T69, T70, T71 |

Every FR-001..FR-039 appears above; every scenario B-1..B-55 plus B-14a/B-14b appears at least once; every
scenario traces to a US+AS pair in §6, and every US AS maps to ≥ 1 scenario (US-1: B-1..B-5, B-46;
US-2: B-6..B-10, B-41; US-3: B-11..B-18, B-14a/b, B-42..B-44, B-47; US-4: B-19..B-24, B-36; US-5: B-25,
B-26; US-6: B-27..B-29, B-45, B-48, B-53..B-55; US-7: B-30..B-35, B-38; US-8: B-39, B-40, B-49..B-52). FR-013, FR-026
and FR-036 have no user-visible BDD scenario (policy resolution, route throttling and log rate are not
observable in the UI) — their assertion is behavioral at the test level (T34/T47, T33, T60), recorded here
to keep the matrix honest.

---

## 11. Assumptions (explicit, reviewer-challengeable)

- **A1 — Folder resolution**: the three stable slugs (`inbox`/`sent`/`drafts`) resolve to the server's real
  folder names via IMAP special-use attributes (RFC 6154, `\Sent`/`\Drafts`), falling back to conventional
  names, with the §2.1 per-mailbox override fields winning when set (overrides wired end-to-end per MIN-001).
  Rationale: server folder layouts vary (e.g. Gmail's `[Gmail]/Sent Mail`); special-use is the standard,
  pure-Go-discoverable answer.
- **A2 — No push**: the panel does not receive WebSocket push for new mail; the 30 s visible-only refresh
  (D25) plus the watcher-driven badge summary cover freshness. Adding WS frames is a later wave.
- **A3 — Humans compose Markdown too**: the manual compose editor's body is rendered by the same D3 pipeline
  (one rendering path, one sanitization story). The compose UI labels the field accordingly.
- **A4 — New Go deps, pinned** (round-1 MIN-012): `github.com/yuin/goldmark v1.8.6` (zero transitive
  dependencies; keeps raw HTML omitted by default — never `html.WithUnsafe()`) and
  `github.com/microcosm-cc/bluemonday v1.0.27` (transitive: `aymerick/douceur v0.2.0`,
  `gorilla/css v1.0.1`; `golang.org/x/net` already required in-tree at v0.58.0 — no downgrade). Both pass
  Hard Constraints #1–#3 (round-1 dependency assessment). **Both sanitizer policies (body, signature) are
  package-level values built once** (footprint HC #3); the CI `vuln` gate covers the new modules. MIME
  building reuses `github.com/emersion/go-message` v0.18.2 already in `go.mod` (round-1 OBS-001) — no
  hand-built multipart writer.
- **A5 — Mailbox picker**: a workspace with multiple mailboxes gets a picker (agent-named); the single-mailbox
  case hides it (US-3 AS-3).
- **A6 — Signature is configuration**: `signature_html` lives in `config.json` with the mailbox pair and is
  not "mail content" under D6.
- **A7 — (retired)** The round-1 "drafts store raw Markdown text" assumption is **replaced by FR-029**
  (rendered multipart + `text/markdown` MIME part with `X-Omnipus-Render-Hash`, never Markdown-in-a-header)
  — round-1 MAJ-006, re-shaped by round-2 MAJ-006.
- **A8 — Mail operation budget**: one IMAP session per REST request (all STATUS/LIST/SEARCH on that
  connection, round-1 MAJ-012); the gateway caps concurrent mail operations at 2 per mailbox (overflow
  queues then 503 + class — MIN-003); the watcher cycles at 60 s per mailbox (today's cadence, carried
  forward from the drainer D16 records) with a single in-flight cycle per mailbox; identical concurrent
  refreshes **coalesce** (N tabs = 1 login — D29/R2-9); the panel refetches every 30 s while open (D25).
  **Constants are implementation-tunable; the D22 failure-triage dispatch may revise them** — the
  requirement is the bound and the visibility, not the number.
- **A9 — Badge placement**: while the panel is closed, the unread badge renders on the Mail entry of the
  workspace tab strip; while open, the panel's folder list and the badge agree (D20's "Mail tab unread
  badge" concretely).
- **A10 — Rate-limit constant**: 10 mutating-mail requests/minute/IP (MC-20) — chosen to be far above any
  human clicking pattern and far below spam-cannon territory; tunable without spec change.
- **A11 — (retired by D27)** The round-1 "peek enforcement is turn-scoped" assumption described the
  watcher-triggered turn, which no longer exists (FR-024): peek enforcement is now **fetch-path-wide** —
  every list/read/preview fetch uses `BODY.PEEK[]`/`EXAMINE` and the only `\Seen` writers are the seen
  endpoint and `read_message` (FR-020, MC-25) — an implementation property, not a turn property.

- **A12 — Attachment caps are reviewer-challengeable constants**: ≤ 10 files and ≤ 25 MiB total decoded per
  message (MC-32) — chosen above normal correspondence and below common provider limits; tunable without
  spec change (same standing as A10).

- **A13 — D30's cause stays out of the spec**: the resilience requirements (FR-037, MC-33) are written
  cause-independent — bounded DNS retry, backoff with jitter, coalescing, visible `dns` class — because
  D30/OBS-004 leave the DNS-window cause unknown. The D30 live test (B-47/T44; outcome recorded as **D32** —
  the old drainer works, its verified gap was success-logging) validates behavior, not
  cause; the D22 failure-triage dispatch owns the diagnosis and may revise the A8 constants after it.

## 12. Edge cases (consolidated)

| # | Case | Expected behavior |
|---|---|---|
| 1 | Inbound message without a Message-ID header | Listed with `message_id: null`; not deep-linkable; readable via its `uid:` ref from the list (FR-028 gives every row a working address) |
| 2 | UIDVALIDITY changed since a link was posted | `mid:` addressing still resolves (B-36); `uid:` links would miss — links are `mid:`-based |
| 3 | Draft deleted before its link is clicked | Panel not-found state (B-21, MC-13) |
| 4 | Draft already sent when link clicked | "Sent on \<date\>" read-only state resolved from Sent (B-22) |
| 5 | IMAP server without special-use support | Conventional-name fallback; per-mailbox override wins when set (A1); if resolution fails, folders endpoint 502s with the class |
| 6 | Mailbox enabled but password unresolvable | Surface shows explicit "mailbox unavailable" state (mirrors the registration skip-WARN, `pkg/agent/email_tools.go::registerEmailToolsForAgent`) — never an empty folder; watcher reports `watcher_state=error` (MC-23) |
| 7 | Signature whitespace-only | Treated as empty (no signature applied) |
| 8 | HTML-only inbound mail (no text part) | `body_text` uses the existing loud-degrade rendering (`pkg/email/transport.go::noHTMLTextMarker`); `has_html=true` drives the sandboxed frame |
| 9 | Message-ID containing slashes/special chars in a link | Percent-encoded in the URL; decoded server-side; validated as `<…@…>` ≤ 998 bytes no CR/LF (FR-028 — the IMAP SEARCH string is the actual injection surface, not the URL path) |
| 10 | Human and agent touch flags concurrently | Last IMAP STORE wins; no local cache to go stale (D6) |
| 11 | SMTP on port 465 vs 587 | Existing `Client.Send` selection logic applies unchanged to all send paths, now under the FR-027 bounds |
| 12 | Body > 256 KB on read | Existing `capBody` truncation marker applies (`pkg/email/transport.go::capBody`); the preview token serves HTML within that same cap |
| 13 | Two drafts share a Message-ID (spoofed inbound vs real draft) | Folder-scoped resolution (FR-028): draft links search only `drafts`; a spoofed copy elsewhere never shadows the draft |
| 14 | Owner edits a draft in their own client between panel view and save/send | Precondition (uidvalidity+uid) mismatch → 409 / explicit replace warning; the human re-views and re-approves (B-32, B-35) |
| 15 | Server without UIDPLUS | Deletion = `\Deleted` only, listings exclude it; no blind EXPUNGE ever (FR-032, MC-17) |
| 16 | Two panels/tabs open on the same mailbox | Identical concurrent refreshes **coalesce** (N tabs = 1 login — D29/R2-9, MC-33/T54); overflow beyond the A8 cap queues then 503 + class (MIN-003); flag writes remain last-wins (edge 10) |
| 17 | `create_email_draft` with no derivable public origin | Tool succeeds; `chat_link: null` + reason; the chat's "Open draft" action still navigates in-app (FR-015) |
| 18 | DNS resolution fails for a window | Named `dns` class surfaced on affected surfaces; bounded resolution retry inside the dial bound; on exhaustion the watcher enters backoff and the badge shows `next_attempt_at` — never a silent hang (B-42, B-43, FR-037) |
| 19 | Watcher state exists but UIDVALIDITY changed | Silent re-baseline to `UIDNEXT-1`, logged once, nothing flagged new, nothing started (B-45, MC-31) |
| 20 | 51st recipient on any send path | Pre-SMTP 400/tool error naming the offending address; nothing transmitted (B-41, MC-27) |
| 21 | Attachment filename path traversal / hostile name (`../../`, control chars) | Sanitized on **serve** and on **attach**; only the sanitized name is ever offered to disk or header (US-8 AS-2, MC-32, T56) |
| 22 | Agent attachment reference to a **secret carve-out**, or to a nonexistent file | Secret carve-out: refused **at resolve and re-verified at read time** — never attachable; nonexistent: tool error **before any SMTP connection or APPEND**, naming the offending reference; nothing transmitted, nothing drafted (D33, MC-35, T67 — the open-read rule; a reference outside the workspace **attaches**) |
| 23 | IMAP server that rejects custom keywords (STORE `$OmnipusAgentRead` answered NO) | Agent read still succeeds: exactly one `\Seen`-only STORE follows the rejected combined STORE, no error to the agent, no "read by agent" tag in the panel, the keyword is not re-attempted until process restart (one WARN per process per mailbox) (B-54, FR-039, T69) |

## 13. Holdout evaluation scenarios (post-implementation, NOT in the traceability matrix)

Evaluated by the founder or an external evaluator against a real mailbox after development completes.

- **H-1 (happy)**: Compose and send a mail from the Mail panel to an external account; open it in a mainstream
  client (Apple Mail / Gmail): formatting, link and signature all render; the plain-text fallback reads
  correctly when HTML is disabled in that client.
- **H-2 (happy)**: Ask an agent to draft a reply to a real received mail; click the chat link (or the tool
  result's "Open draft"); the panel shows the draft; the same draft is visible and legible in the Drafts
  folder of the founder's own mail client (FR-029 — rendered, not raw Markdown).
- **H-3 (happy)**: After an agent sends mail, the message appears in the founder's own client's Sent folder
  without any local Omnipus copy involved. (On providers that auto-file sent mail, two copies appear — the
  accepted D18 consequence; the pass condition is "appears", not "appears exactly once".)
- **H-4 (error)**: With the network to the mail server cut, open the Mail panel: every surface shows an
  explicit error with retry, the class is named, and the gateway log carries the raw cause (D22) — nothing
  silently renders as an empty mailbox.
- **H-5 (error)**: Delete a draft in the founder's own mail client, then click its chat link in Omnipus: the
  panel reports the draft no longer exists.
- **H-6 (edge)**: Open a marketing email with tracking pixels; with browser devtools network tab open, confirm
  zero outbound requests to third-party hosts until "Load images" is clicked (D17).
- **H-7 (edge)**: Break the mailbox password deliberately: the Mail panel and tools degrade with explicit
  unavailability states; the badge summary reports the error class; nothing silently pretends the mailbox is
  empty.
- **H-8 (edge, D27)**: Receive a real message while the workspace chat is open: **no agent turn starts** —
  nothing appears in chat, no approval prompt, no task; the badge advances instead. Confirm in the founder's
  own client the message stays unread until a human (panel/seen endpoint) or the agent (actively) reads it.

- **H-9 (error, D29/D30)**: Cut network to the mail server for ~10 minutes: the badge shows the named error
  class and "retrying at hh:mm" from `next_attempt_at`; log volume stays bounded (no per-cycle storm); after
  the network returns, the next backoff step succeeds and the badge returns to `ok` — recovery needs no
  restart and no manual action beyond, at most, the one-click Retry.

## 14. Founder questions

**All questions are resolved; no founder question is open.** Mapping: Q1 → D12
(+ D23, D24 for edit scope and foreign drafts); Q2 → D21 (transcripts keep email text; docs state it); Q3 →
D13 + D17 (sandboxed no-scripts frame; images blocked with "Load images"); Q4 → D26 (CC/BCC for humans and
agents); Q5 → D20 (the drainer question dissolved — unread is plain `\Seen`; the watcher never touches
flags); Q6 → D21 (folder-scoped `uid:`/`mid:` addressing per MAJ-005); Q7 → D11 (Library-style docked panel
+ pop-out). Round-2: R2-1/R2-2 → D27; R2-3/R2-5/R2-6/R2-7/R2-8/R2-9 and CRIT-001 → D29; R2-4 → D28;
R2-10 → D30. The fix-round open point **FQ-1 is resolved by D33** (agent tool attachments — below). Founder-directed
correction, 2026-09-26: **D35–D38 are applied** (D35 → the §17 prototype gate; D36 → the §7 fake-server E2E;
D37 → the T44 UAT campaign on a separate GreenMail instance + the post-landing live smoke; D38 → US-6 /
FR-039, the read-by-agent tag) — **D38 closes open point O5** (agent-read vs human-read is the
`$OmnipusAgentRead` keyword with derived `read_by_agent`, B-53..B-55).

Points that could have become questions are recorded as reviewer-challengeable assumptions instead, because
each is an obvious default carried from today's behavior or explicitly delegated by a decision:

- Watcher cadence and the mail-operation budget (A8): 60 s watcher / 30 s panel / 2-per-mailbox cap, plus
  D29's backoff/coalescing — today's cadence plus D25's answer; D22's pending diagnosis may revise the
  constants, not the requirement.
- Badge placement (A9): the Mail tab-strip entry — the direct reading of D20's "Mail tab unread badge".
- Attachment caps (A12): ≤ 10 files / 25 MiB — stated, tunable, same standing as A10.
- D30's cause-independence (A13): the spec specifies resilience, not diagnosis; the D30 live check validates
  behavior and the failure-triage dispatch owns the cause.
- Outbound size bound (FR-031) and rate-limit constant (A10): 1 MiB and 10/min — stated, tunable.

### FQ-1 — May agent tools attach files to outgoing mail? (RESOLVED 2026-09-25 by D33)

**Resolution (D33).** Founder verbatim: *"yes agents need full email capability no extra guard rails, same
as send email"* (against the written recommendation). Agents get full email capability, including
**workspace-file attachments**, on `send_email`/`reply`/`create_email_draft`: the attachment parameter is
governed by **exactly the same tool policy as sending** — no separate ask gate; no special caps beyond the
general message limits already in this spec (MC-32 caps, MC-22 body bound, MC-27 recipient cap).
Specified as FR-038/MC-35 with US-8 AS-5, B-49..B-52, T64–T67, DS-9, SC-010 and the §15 compose/MIME note.
The exfiltration-surface trade-off recorded under this question (a compromised agent could mail workspace
files out) was weighed and **accepted by the founder as part of the decision** — it is governed by the same
policy, caps and audit as any send. The human attach/download paths (D28) are unchanged.
**Security sign-off C2 (2026-09-26) pinned the resolution semantics to what
`pkg/tools/resolvepath.go::ResolvePath` actually enforces** — open-read rule, no outside-workspace gate —
which is D33's own posture ("no extra guard rails", send_file precedent); the earlier "outside the workspace
= invalid input" wording was this spec's wording, not the founder's, and its deletion raises **no new
founder question**.

---

## 15. Implementation notes — backend (Phase 4+; implementation guidance, not new policy)

- **New file** for mail read/send gateway handlers (e.g. `pkg/gateway/rest_mail.go`) — `rest_mailbox.go`
  (803 lines) stays config-only plus the D19 policy fill; one file, one job. Same `loadWorkspace`-idiom 404s
  and `ErrorResponse`.
- **`pkg/email`** gains: a compose/render unit (goldmark render → bluemonday sanitize → `go-message`
  multipart builder → signature on both parts; Message-ID generation per FR-012), a Sent-APPEND after SMTP
  success inside the send path, an APPEND primitive (`\Draft` flag, `X-Omnipus-Draft` header and the
  `text/markdown` part with `X-Omnipus-Render-Hash` — FR-029), folder listing/counts with special-use
  resolution, UIDPLUS probing, and folder-scoped
  `uid:`/`mid:` resolution. The `Transport` interface grows accordingly; the in-memory test fake grows with
  it. The connectionless dial-per-operation pattern (`Client.dialIMAP`) is preserved for IMAP; SMTP is
  context-threaded per FR-027.
- **Folder resolution** (A1) lives in `pkg/email`; per-mailbox overrides ride `MailboxConfig`.
- **Agent attachments (D33)**: the compose unit takes attachment inputs from both sources — the human paths'
  `data_base64` parts (REST) and the agent path's file references, resolved through
  `pkg/tools/resolvepath.go::ResolvePath` **under its open-read rule** and read at compose time — into one
  shared MIME builder (the FR-029 `multipart/mixed` wrapper already exists; attachments join it). Caps (MC-32)
  and the open-read-rule checks (MC-35: carve-outs refused at resolve **and** re-verified at I/O time; a
  nonexistent file fails when read) run before any dial or APPEND; there is **no outside-workspace gate**
  (sign-off C2). No new policy surface anywhere (§2.7 note).
- **Signature plumbing**: `EmailMailboxPanel` PUT carries `signature_html` through the existing raw-map write
  path — which must keep preserving unknown fields (ADR-033 §5 negative note). Sanitization happens
  server-side on save (FR-003).
- **`create_email_draft`** joins `tools.EmailToolset` (`pkg/tools/email.go`) and the six §2.7 touch points.
- **D19 fill**: the replacement for `grantEmailToolAllows` writes `ask`/`allow` only into absent keys of the
  agent's own policy map (`rest_mailbox.go`); the fill set is `emailToolNames` + `create_email_draft`.
- **HTML preview token** flow mirrors the Library pair (`/library/preview-token`) including token expiry,
  bound to (pair, folder, ref, load_remote): the **mint** stays session-authenticated under `/api/v1`
  (`POST /api/v1/mail/html-preview-token`, §2.3); the **serve routes** are token-only on the non-API
  `/mail-preview/` prefix (§2.3a — sign-off C1), each handler setting the MC-10 headers; `cid:` parts serve
  under the same token scope. The served HTML is bounded by the existing 256 KB inbound cap.
- **Audit + rate limit**: the four mutating routes wrap `withRateLimit` around a **dedicated
  `mailMutationLimiter`** (never the shared `rest_auth.go::apiRateLimiter` instance — MIN-004) and
  emit via `pkg/audit` (hash via `argshash.go`), following `rest_preview_audit.go`.
- **Watcher**: `pkg/email/watcher.go::Watcher` (poll logic: UID search → state-file advance → badge state —
  **no enqueue path exists**: the watcher cannot start a turn, per D27/FR-024) + `pkg/heartbeat/mail_watch.go::MailWatchService`
  wired where `NewMailboxDrainService` sits today (`gateway_boot.go`, `gateway_reload.go`). The drainer files
  and suites are deleted in the same change (#631).
- **#629**: `Client.Send` gains context threading (`net.Dialer`/`tls.Dialer` context dials, per-phase
  deadlines bounded by `commandTimeout`); `dialIMAP`'s timeout path closes the late connection.
- **Structured logs** on every mail endpoint: pair, folder, operation, duration, sanitized error class; raw
  upstream text server-side only (FR-018).
- **Deps** (A4) are the only new third-party imports; `go.mod` diff reviewed in the contract commit.

## 16. Implementation notes — frontend (concrete components to reuse)

Design-system skill (`omnipus-design-system`) is mandatory before any file under `src/components/` is touched.

| Need | Reuse | Notes |
|---|---|---|
| Tab-strip entry + badge | `src/components/workspaces/WorkspaceTabBar.tsx::WORKSPACE_TABS` | One entry `{ segment: 'mail', label: 'Mail', Icon: Tray }` (Phosphor; no emoji); badge dot/count on the entry from the summary endpoint (A9); `TabSegment`/`SEGMENT_LABELS` derive automatically |
| Panel hosting | `src/components/library/LibraryPanel.tsx` pattern + `src/store/ui.ts::libraryPanel` | New `mailPanel` state + `openMailPanel/closeMailPanel`; docked `<aside>` sibling in `AppShell.tsx` next to `LibraryPanel` (D11); fullscreen pop-out mirrors the Library's |
| Route | `src/routes/_app/workspaces.$workspaceId.media.tsx` | Redirect-stub pattern for the `mail` segment (D11) — opens the panel scoped to the workspace, navigates back to Chat |
| List + preview split | `src/components/library/LibraryExplorer.tsx` placeholder-slot structure | `MailExplorer` hosts `MailPreviewPane` in the slot; virtualization per Library's list conventions |
| Message/draft preview | `src/components/library/LibraryPreviewPane.tsx` (structure), `LibraryMarkdownPreview` (draft Markdown render) | Exhaustive dispatch per kind, per the "no `&&` chain" rule documented in `LibraryPreviewPane.tsx`. Draft Markdown renders with raw HTML inert — no `rehype-raw` / `skipHtml` (MIN-009, T43) |
| Edit form | The panel's edit mode uses `MailDraftUpdateRequest` fields | To/CC/BCC lists, subject, Markdown body (D23); foreign drafts pre-filled from the derived Markdown + the D24 loss statement shown above the editor |
| HTML mail body | `LibraryHtmlFrame` pattern (sandboxed iframe) | Mail variant: **no** `allow-scripts`/`allow-same-origin`/`allow-forms`; `allow-popups` for links (D13 "feels normal"); "Load images" button re-mints the token with `load_remote=true` (D17); `cid:` images via the part route |
| Upstream error states | `src/components/library/LibraryErrorBanner.tsx`, `src/components/shared/QueryErrorState` | FR-018's explicit, class-named error + retry |
| Compose dialog | shadcn `Dialog` + the validation patterns of `src/components/connectors/EmailMailboxPanel.tsx` (`FieldRow`, `validate`) | Field-level errors; Markdown-labeled body (A3); To/CC/BCC fields (D26); **attachment picker — MC-32 caps enforced client-side, sanitized names displayed, removal explicit** (D28, US-8 AS-3) |
| Signature editor | Section inside `src/components/connectors/EmailMailboxPanel.tsx::EmailMailboxPanel` | Textarea + live preview rendered in a sandboxed mini-frame with the MC-10 posture; 16,384-char bound enforced client-side too (MC-1). Advanced fields for the folder overrides (MIN-001); **no mail-trigger switch exists** (D27) |
| Chat link → panel + "Open draft" | `src/components/chat/markdown-shared.tsx::createLinkRenderer` + the tool-result renderer | Same-origin mail deep links navigate in-place via **hash-route pattern match** (not origin equality) instead of `target="_blank"`; `isSafeHref` gating unchanged; deterministic "Open draft" action from the `create_email_draft` result (FR-015, OBS-003) |
| Data fetching | TanStack Query over generated types (`src/lib/api/generated/`) | Generated types only (shared rule 4); `refetchInterval: 30000` while the panel is mounted, `refetchIntervalInBackground: false` (D25, B-16) |
| Picker | `GET /api/v1/mailboxes` | Filtered client-side to the current workspace, `enabled && configured`; selection in `sessionStorage` per workspace (FR-010) |

Agent attachments (D33) add **no new UI component**: agent-attached files are ordinary MIME parts, so the
existing attachment listing and carry-over UI (the compose/edit rows above, `MailMessage.attachments`)
renders them; the approval panel already lists what will be sent.

## 17. Reachability (Definition of Done)

- **Operator**: Connectors screen → mailbox panel shows the Signature section and the folder-override advanced
  fields (US-1) — **no mail-trigger switch exists (D27)**; workspace tab strip shows **Mail** and opens the
  docked panel (US-3); compose action reachable from the panel header (US-5).
- **Human via chat**: a posted draft link — or the tool result's "Open draft" action — opens the preview
  panel in the same tab (US-4), exercised in the D37 UAT campaign on the GreenMail instance.
- **Agent**: `create_email_draft` is registered for every agent via
  `pkg/agent/email_tools.go::registerEmailToolsForAgent` (so it appears on the per-agent permissions screen),
  wired at all **seven** §2.7 touch points, and behaviorally asserted for both agent kinds (T34 / SC-007).
  All three agent mail tools accept the D33 `attachments` parameter (workspace files) on the tools' own
  policy — no separate policy surface; the D37 UAT campaign exercises an agent send/draft with a
  workspace-file attachment on the GreenMail instance (T44, SC-010).
- **Endpoints the UI depends on are all in the contract** (§2.3): the folder/message/summary reads, the seen
  endpoint, the attachment download endpoint, and the mail HTML preview served under the **non-API
  `/mail-preview/` prefix** (never the API prefix — MC-10/CRIT-001 posture).
- **User docs** (D31, D21) state plainly: agents handle mail only when asked — inside a human/task-started
  turn — or through a heartbeat / scheduled task the operator configures ("check your inbox"); nothing about
  mail is automatic. No mail surface ships that implies otherwise.
- **Mail panel reachable from two surfaces**: the workspace tab strip entry (docked panel, D11) and chat
  links ("Open draft" / `chat_link`) — both exercised by T37/T42 and the D37 UAT campaign.
- **Prototype gate (D35, process step)**: before the frontend build, `frontend-lead` delivers Wave 1 —
  Mail panel, draft preview/edit, compose and signature editor — as design-system stories with sample data
  (no backend, Library look); the founder approves or adjusts from screenshots (dark theme, desktop +
  narrow) plus a usability-heuristics review before the real build starts.
- **Delivery statement (two lines, never merged)**: *code correct and tested* — unit + gateway-integration
  suites green, the D36 fake-server E2E green, contract verification green; *reachable by a user and an
  agent* — tab entry, docked panel, signature editor, compose, draft link, panel actions, and the agent
  tool all invocable as listed above, confirmed by the D37 UAT campaign and the post-landing live smoke (D37).

## 18. Assembly summary (plan-spec Phase 6)

| Measure | Count |
|---|---|
| User stories | 8 (US-1 … US-8) |
| BDD scenarios | 57 — Happy Path 26 · Alternate Path 8 · Error Path 12 · Edge Case 11 (B-1..B-55 plus B-14a/B-14b) |
| Machine-verifiable constraints | 47 (MC-1 … MC-45 plus MC-31a/MC-31b; MC-37–MC-45 added by the 2026-09-26 security sign-off absorption) |
| Test datasets | 10 (DS-1 … DS-10) |
| TDD plan rows | 80 (orders 1–80; T-number = order; T72–T80 added by the 2026-09-26 security sign-off absorption) |
| Functional requirements | 39 (FR-001 … FR-039) |
| Success criteria | 11 (SC-001 … SC-011) |
| Founder questions open | 0 (FQ-1 resolved by D33 — §14) |
| Round-1 findings dispositioned | 32 of 32 (§20) |
| Round-2 findings dispositioned | 40 of 40 (§21) — 0 CRITICAL still open |

## 19. Related issues (D14, D21)

### 19.1 ADR-033 amendment — specified content (D29/R2-7; the architect writes the dated amendment in this branch)

ADR-033 (*Per-(Agent, Workspace) Email Mailboxes*) stays canonical (D10). This spec requires a **dated
amendment** to it — not a new ADR — replacing its inbound-handling consequence (drainer → Board tasks) with:

1. **Inbound mail never starts an agent turn** (D27): the email tools are used actively inside turns started
   by humans/tasks/heartbeats — **agents handle mail only when asked, or through a heartbeat / scheduled
   task the operator configures; nothing about mail is automatic (D31)**; the drainer is deleted (#631); no
   Board-task path from mail exists.
2. The replacement watcher only advances per-pair watch state (UIDs/counts/error class — FR-033) and feeds
   the workspace badge; it never mutates flags and has no enqueue path.
3. Mail content is never stored locally; the panel reads live (D6); preview isolation follows the
   Library-preview posture — non-API prefix, no `'self'`, CSP `sandbox` (MC-10) — with **security-lead
   sign-off before implementation** (D29 closes round-2 CRIT-001).
4. The mailbox pair model, credentials, per-operator cap, pairing and move semantics are unchanged.

| Issue | Relationship | Note |
|---|---|---|
| [#629](https://github.com/elicify-ai/omnipus/issues/629) — email send can hang forever | **Closed by this work** (D21) | FR-027 absorbs it: context-threaded, bounded SMTP + Sent APPEND; MC-21/MC-8 tests. The spec touches `Client.Send` anyway (Sent APPEND), so the fix ships with it |
| [#631](https://github.com/elicify-ai/omnipus/issues/631) — delete the mailbox drainer | **Closed by this work** (D20, D21) | Drainer files, wiring and suites deleted; the new-mail watcher (never flags, never tasks, **never starts a turn** — D27) replaces it; no opt-in agent handling exists |
| [#42](https://github.com/elicify-ai/omnipus/issues/42) — webmail view, send-gating, Gmail setup | **Partially delivered; stays open with a note** (D21) | Delivered here: inbox/sent/drafts webmail view, manual send, HTML rendering. Superseded by decision: send "intervention modes" → D12 (panel Send is the approval) + D15/D19 (configurable policy + configure-time ask fill). Deferred (stays in #42): threads view (D5 limits folders), Gmail app-password setup wizard, "Test connection" action (round-1 MIN-010) |

## 20. Round-1 disposition (every finding, one row)

Verdicts: **fixed** (folded into the spec body), **rejected-with-reason** (founder decision or recorded
rationale), **deferred-with-issue** (tracked in §19's #42 remainder).

| Finding | Verdict | Where addressed |
|---|---|---|
| CRIT-001 — spec protected the drainer that #631 deletes | **fixed** | §1, US-6, FR-020/FR-023, §3.1 drainer row (deletes), §7.2 first bullet rewritten, B-27..B-29 replace B-18/B-19; closes #631 via D20 |
| CRIT-002 — "send_email already needs approval" false for non-core agents | **rejected-with-reason** | Founder decision D15: keep today's configurable policy ("keep today, user can configure as it is now"). FR-016 rewritten honestly (ceiling as shipped; built-in roles keep `ask`; accepted risk stated); D19 replaces the deny→allow overwrite; T34 asserts both agent kinds |
| MAJ-001 — D11–D13 not folded in | **fixed** | D11 → FR-007/§16; D12/D23/D24 → US-7 (six AS), FR-021/029/030/032, edit endpoint + `MailDraftUpdateRequest` (§2.2/§2.3), B-24/B-30..B-35, T23–T28 |
| MAJ-002 — panel send can transmit unseen content | **fixed** | `MailDraftSendRequest` carries the displayed/edited content + uidvalidity/uid precondition; 409 on staleness (MC-16, FR-021, B-32, T25) |
| MAJ-003 — draft-tool reachability wiring incomplete; SC-007 greppable false green | **fixed** | §2.7 lists all six touch points (incl. role inventory, seed literal, auto_approve, `emailToolNames`); SC-007 replaced with behavioral T34 + hardened grep (adds `pkg/gateway/`, excludes test files) |
| MAJ-004 — Sent APPEND duplicates on Gmail/Outlook.com | **rejected-with-reason** | Founder decision D18: generic IMAP only, no provider detection ("we use IMAP only"). The duplication is documented as the accepted consequence in FR-006 and §5.4; the UAT check asserts "appears", not "exactly once" (T44) |
| MAJ-005 — Message-ID addressing inconsistent/unsound | **fixed** | FR-028: folder-scoped `uid:`/`mid:` refs; resolution order newest-INTERNALDATE; `<…@…>` ≤ 998 bytes no-CRLF validation; §2.3 endpoints re-keyed; DS-4 rows; adopted by D21 |
| MAJ-006 — raw-Markdown drafts break the "normal provider" promise | **fixed** | FR-029 (option 1): drafts APPENDed rendered + `X-Omnipus-Markdown` source header; foreign drafts per D24 in FR-030; B-24; A7 retired |
| MAJ-007 — HTML-preview headers under-specified | **fixed** | MC-10 carries the full directive set, sandbox flags, Referrer-Policy, token binding/TTL, `cid:` part route; security-lead pre-implementation gate in FR-019 — **delivered 2026-09-26 (sign-off with conditions, C1–C11 absorbed)**; T41 + per-directive handler tests |
| MAJ-008 — signature sanitization/ordering undefined | **fixed** | FR-003: sanitized on save with a named signature policy; stored value is sanitized; MC-2 composes hostile body **and** hostile signature; DS-1 adds the real-world signature row |
| MAJ-009 — MC-3 contradicts FR-004 | **fixed** | MC-3 rewritten: every outbound message multipart/alternative with exactly one plain + one HTML part; plain-part derivation rule defined |
| MAJ-010 — issue #42 not reconciled | **fixed** | §19 Related issues maps delivered/superseded/deferred per item; closes #629/#631 per D21 |
| MAJ-011 — no audit trail or rate limit on human sends | **fixed** | FR-025 (audit events, `argshash`, `rest_preview_audit.go` precedent), FR-026 + MC-19/MC-20 (10/min/IP), T32/T33 |
| MAJ-012 — polling interval and IMAP budget undefined | **fixed** | D25: 30 s while open, never in background (B-16, T40, SC-004 now measurable); one IMAP session per request + 2-per-mailbox cap + 60 s watcher (A8) |
| MAJ-013 — chat link assumes https public_url; model copies URL verbatim | **fixed** | FR-015: serve_web origin derivation, http allowed, `chat_link` null-with-reason; hash-route pattern matching (A: `createHashHistory` verified); deterministic "Open draft" action (OBS-003) |
| MAJ-014 — traceability/structural gaps | **fixed** | B-2/B-17/B-22-analogues and every scenario have TDD rows (§7); B-3 in the matrix; US-7 has six AS; FR-013/FR-026's absence of UI-level scenarios is stated honestly in §10 |
| MAJ-015 — Client.Send hangs (#629) unabsorbed; MC-8 false for SMTP | **fixed** | FR-027 + MC-21: context threading, bounded dial + per-phase deadlines, `dialIMAP` leak fix, endpoint 502 test (T21/T22); closes #629 (D21) |
| MIN-001 — folder overrides unsettable | **fixed** | Added to `MailboxConfigureRequest` (§2.1) and the panel's advanced fields (§16, FR-008) |
| MIN-002 — MC-13 untestable in single-user model | **fixed** | MC-13 rewritten to 404 shapes (agent/workspace/pair-mailbox missing, no leakage); single-user constraint cited (§3.1) |
| MIN-003 — `MailMessage` schema garbled | **fixed** | §2.2 gives an explicit field table with types/nullability (incl. cc/reply_to/in_reply_to/references); embedded `Mailbox` dropped from `MailFolderList` |
| MIN-004 — draft Message-ID rationale wrong | **fixed** | FR-012: `<random-128-bit@from-domain>` (fallback `omnipus.invalid`), kept through edits and send |
| MIN-005 — `unseen_only`/`unread_count` undefined | **fixed** | `unread_count` Inbox-only (null elsewhere, §2.2); `unseen_only` defined with FR-008 + T18 |
| MIN-006 — sent draft shows "no longer exists" | **fixed** | FR-014 + B-22 + edge 4: "Sent on \<date\>" read-only state resolved from Sent |
| MIN-007 — outbound limits and header encoding unspecified | **fixed** | FR-031 (1 MiB bound, MC-22) + RFC 2047 (MC-4); DS-2 rows; T3/T8 |
| MIN-008 — picker data source/persistence unspecified | **fixed** | FR-010 + §16: `GET /api/v1/mailboxes` filtered, sessionStorage per workspace |
| MIN-009 — draft Markdown may render raw HTML in SPA | **fixed** | §16 + T43: no `rehype-raw`/`skipHtml`; hostile-img test row |
| MIN-010 — no operator diagnostics | **deferred-with-issue** (half fixed) | Structured log fields fixed into FR-018; "Test connection" deferred to the #42 remainder (§19, D21) |
| MIN-011 — 502 bodies may echo raw upstream text | **fixed** | Sanitized error-class enum + generic message (MC-8, FR-018); raw text server-side only |
| MIN-012 — A4 lacks versions/transitives/build-once | **fixed** | A4 pinned: goldmark v1.8.6, bluemonday v1.0.27 (+ douceur, gorilla/css), package-level policies built once, vuln gate noted |
| OBS-001 — reuse go-message for MIME | **fixed** | A4/§15: `emersion/go-message` v0.18.2 (already in `go.mod`) builds the multipart; no hand-rolled writer |
| OBS-002 — open questions unanswered | **fixed** | All resolved by D11–D26; §14 records the mapping and the zero-open count |
| OBS-003 — structured draft action instead of model-typed URL | **fixed** | FR-015/§16: deterministic "Open draft" action from the tool result; text link kept as convenience |

**Counts**: 32 findings — **fixed 29 · rejected-with-reason 2 (CRIT-002 per D15, MAJ-004 per D18) ·
deferred-with-issue 1 (MIN-010's "Test connection", tracked in the #42 remainder, D21)**.

## 21. Round-2 disposition (every finding, one row)

Verdicts: **fixed** (folded into the spec body in this fix round), **rejected-with-reason** (recorded
rationale; the founder may override). **Counts: 40 findings — fixed 38 · rejected-with-reason 2 ·
deferred 0.** No CRITICAL finding remains open: CRIT-001 is closed by the MC-10 Library posture +
security-lead gate; CRIT-002 is **closed by removal** under D27 — the switch and the turn path no longer
exist in the spec.

| Finding | Verdict | Where addressed |
|---|---|---|
| CRIT-001 — MC-10's CSP reintroduces both documented Library-preview defects | **fixed** | MC-10 rewritten to the Library posture: non-API `/mail-preview/` prefix + no-redirect test (T62), no `'self'`, CSP `sandbox` directive + iframe attribute, `base-uri`/`connect-src`/`object-src 'none'`, named inbound sanitizer (strips `<meta http-equiv>`, `<base>`, forms, scripts; rewrites `cid:`), gateway-proxy remote images (https-only, private-refused, bounded, image-only), 256 KB cap, WebKit cookie test case; `library_isolation_policy.go` both defects cited (§3.1); security-lead sign-off gate (D29); FR-019 carries the gate — **delivered 2026-09-26 (sign-off with conditions, C1–C11 absorbed)** |
| CRIT-002 — "Let the agent handle new mail" is a zero-click prompt-injection path | **fixed by removal** | D27: an email never starts an agent turn. FR-024 replaced (no enqueue path exists); switch + `MailboxConfigureRequest` wiring removed outright (greenfield, no deprecated field); §2.5, §2.7, US-6, §5.2 non-behaviors, B-28/B-29, MC-18, T30, H-8, §17 all state the removal |
| MAJ-001 — watcher state has no UIDVALIDITY, no first-run baseline, no turn-count rule | **fixed** | MC-31 + FR-023: state carries `uidvalidity`; first run and UIDVALIDITY change baseline to `UIDNEXT-1`, log once; B-45, T52, DS-8, edge 19 |
| MAJ-002 — watcher agent-turn mechanism and "peek" scope undefined | **fixed by removal** | Superseded by D27: the turn mechanism no longer exists (FR-024); peek is fetch-path-wide (FR-020/MC-25) |
| MAJ-003 — "opening marks \Seen" has no endpoint; contradiction with today's code | **fixed** | New `POST …/messages/{ref}/seen` endpoint (§2.3; 204, idempotent, panel-once-per-open); FR-020 + MC-24 + T29/T45; B-27; the read paths use peek/EXAMINE (MC-25/T46) |
| MAJ-004 — planned tests cannot see the IMAP behaviors they claim to verify | **fixed** | §7 intro rewritten: IMAP-observable behavior asserted against the in-tree real-protocol harness `startMemIMAP` (§7, T5/T9/T11/T12/T26/T29/T30/T45/T46/T52/T54/T57); fakes kept only for tool-level shape tests; §7.2 additive-migration note |
| MAJ-005 — BCC contradictory, can leak recipients | **fixed** | FR-022 three-copy rule (envelope-only for non-BCC copies; Sent **keeps** Bcc; drafts keep); MC-19 audits Bcc; T7; DS-5 |
| MAJ-006 — Markdown-in-header fragile, silently discards owner edits | **fixed** | FR-029: `text/markdown` MIME part + `X-Omnipus-Render-Hash`; stale hash → foreign path + loss statement (FR-030); MC-29; T50; A7 re-retired |
| MAJ-007 — plain-text part relies on non-existent goldmark feature; line breaks unspecified | **fixed** | FR-004 + MC-30: custom AST text renderer (links `text (href)`, lists indented, code literal) + hard wraps; T51; DS-2 multi-line row |
| MAJ-008 — contract gaps the 5-step procedure turns into guesses | **fixed** | §2.2/§2.3 completed: seen + attachment endpoints, `CreateEmailDraftResult`, `ErrorResponse.code` enum, MC-23 summary fields, PUT draft 409/429, preview mint-is-the-only-fetch + serve-never-dials (T63); D30 cause-unknown sentence |
| MAJ-009 — panel send not idempotent | **fixed** | §2.3 send row idempotency; MC-28 + B-38 + T49 (counting transport); FR-021 matrix row carries B-38/T49 |
| MAJ-010 — foreign draft silently drops attachments and threading headers | **fixed** | FR-030/FR-035: carry-over by part reference, listed before send, fail-closed 400 on unresolvable part; threading headers preserved; B-40; T57; US-8 AS-4; DS-6 |
| MAJ-011 — unread badge has no refresh rule while panel closed | **fixed** | D29/R2-5: badge refreshes every 60 s from saved watcher state, no IMAP login; US-3 AS-8, B-18, SC-004, MC-23, T61 |
| MAJ-012 — "ADR-033 unchanged" is false — inbound clause deleted | **fixed** | §19.1 specifies the dated amendment's content (D29/R2-7 — the architect writes the amendment; §1 header states it); §3.1 drainer deletion acknowledged |
| MAJ-013 — D19 fill has no test and a mis-traced requirement | **fixed** | MC-26 fill table + T47 + B-46; FR-016 cites T47/B-46; matrix FR-016 row corrected (was the MAJ-013-named mis-trace) |
| MAJ-014 — `reply` under D26 undefined | **fixed** | §2.4: `reply` derives the recipient from the original, optional cc/bcc, `reply_all` minus own address; T39 Reply prefill; US-5 AS-3 |
| MAJ-015 — no bound on recipient count or address format | **fixed** | MC-27 + §2.4: cap 50 across To/Cc/Bcc, de-dup, `net/mail.ParseAddress`, 51st → pre-SMTP error; B-41; T48; DS-5; edge 20 |
| MAJ-016 — no DNS retry, no context-aware IMAP dial | **fixed** | FR-027/FR-037 + MC-33: context-aware dial, bounded resolution-only retry (3 × 250 ms→1 s), `dns` class surfaced; B-42; T59 |
| MAJ-017 — no backoff; lockstep cycles | **fixed** | FR-037 + MC-33: 60 s → 2 → 4 … 15 min cap, ±20% jitter, 0–60 s per-mailbox offset, `auth_failed` to cap, manual Retry bypass; B-43; T53; MC-34 bounded logs |
| MAJ-018 — login budget unbounded (watcher + refresh + tabs) | **fixed** | FR-037 + MC-33: coalescing (N tabs = 1 login), zero auto-dials in backoff, per-mailbox offset; T54; B-44; SC-004 |
| MAJ-019 — watcher can be silently blind like the drainer | **fixed** | UID-keyed watcher (FR-023/MC-18) + "ok never lies" summary (MC-23/T61); B-47 (real mail advances badge within two cycles — D30 live check) + B-48 (another client's read still advances); T30 |
| MIN-001 — signature live preview cannot show https logos under MC-10 | **fixed** | §16 signature-editor row: operator's own signature content renders in the mini-frame — https images display without any "Load images" step (T38) |
| MIN-002 — traceability errors | **fixed** | Matrix rows corrected (FR-016/018/020/021/023/026/029/030/033); closing paragraph re-derived per actual trace lines; §18 counts re-derived by grep (50 scenarios, 63 TDD rows, 37 FR, 9 SC) |
| MIN-003 — cap overflow behavior undefined | **fixed** | §2.3 session note + A8 + edge 16: overflow queues then 503 + class (MIN-003); coalescing bounds the concurrency pressure (D22) |
| MIN-004 — rate limiter must be a dedicated instance | **fixed** | FR-026 + MC-20 + §15 audit bullet: dedicated `mailMutationLimiter`, never the shared API limiter |
| MIN-005 — `mid:` multi-hit ignores `\Deleted`, INTERNALDATE unreliable | **fixed** | FR-028: resolution among non-`\Deleted` only, highest-UID tie-break, never INTERNALDATE; T11 |
| MIN-006 — audit origin enum lacks owner-drafts | **fixed** | FR-025 + MC-19: origin enum `human\|agent-draft\|owner-draft`; recipients incl. Bcc audited |
| MIN-007 — failure logging floods; state cleanup unstated | **fixed** | FR-036 + MC-31b/MC-34 + T60: first-failure WARN + per-backoff-step summary + recovery INFO; state file deleted with the mailbox (FR-033/MC-31) |
| MIN-008 — "all six touch points" not exhaustive | **fixed** | FR-013 + §2.7 #7: seven touch points incl. `pkg/coreagent/prompts_adr090.go` prompt text and `Mailbox.yaml` `enabled` description (prometheus-prompt-engineer owns #7) |
| MIN-009 — humans cannot reply from the panel | **fixed** | US-5 AS-3 Reply action (prefill, `Re:` subject, `in_reply_to` set) + §2.4 `reply` semantics + T39; §16 compose row |
| MIN-010 — FR-009 permits dead "Message-ID ↔ task/session" metadata | **fixed** | FR-009 rewritten: the watcher state file is the only persisted mail-adjacent state; no task mapping is recreated (#631/D20) |
| MIN-011 — outbound body sanitizer allowlist unstated | **fixed** | MC-2 extended: links http/https/mailto only, images https-only, no `style` from agent Markdown; T58; §5.3 |
| MIN-012 — deep-link stub parameter handoff unstated | **fixed** | MC-31a + T55: the stub copies `mailbox`/`folder`/`message` into `openMailPanel` before navigating back |
| MIN-013 — "Open draft" parses a non-contract tool result | **fixed** | `CreateEmailDraftResult` schema in §2.2 (contract-first, generated types only); MIN-013 cited there |
| MIN-014 — error enum lacks `dns`; B-14 named unreachable as `timeout` | **fixed** | MC-8 enum now includes `dns` (+`server_error`); B-14 → Scenario Outline with `dns`/`timeout`/`connect_refused` examples; SC-006 names `dns`; T59 |
| MIN-015 — failure logging has no rate rule | **fixed** | FR-036 + MC-31b/MC-34 + T60 (also closes MIN-007's flood half; MIN-007's cleanup half → FR-033) |
| OBS-001 — speculative surface (`unseen_only`) | **fixed** | Param dropped: §2.3 list row, DS-3, T18 (explicit ignore/reject row) |
| OBS-002 — add an approval preview for `send_email`/`reply` | **rejected-with-reason** | Scope, not defect: the draft panel (view/edit before send) IS the approval surface for drafts (D12); direct sends keep today's text-based policy prompt consistent with every other tool approval (ADR-090 approval UX; D15 keeps send policy as-is). An HTML preview inside the approval flow is a UI enhancement — founder may override in one line |
| OBS-003 — consider shipping in slices | **rejected-with-reason** | Team-lead's planning domain (dev-team design: decomposition is planning, the spec defines the whole feature); the spec already provides the slicing inputs (US priorities P0/P1 and independent test statements); no requirement blocks a staged delivery |
| OBS-004 — diagnosis's resolver premise disputed; do not write the cause into the spec | **fixed** | FR-037/MC-33 written cause-independent (A13); the spec states requirements only — bounded retry, backoff, visible `dns` class; D30's live check (B-47/T44) validates behavior, not cause |

**Counts**: 40 findings — **fixed 38 · rejected-with-reason 2 (OBS-002, OBS-003) · deferred 0 · CRITICAL
open 0**.
