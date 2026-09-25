# Feature Specification: Email — HTML signatures, workspace Mail panel, agent draft approval

**Created**: 2026-09-25
**Status:** Draft
**Revision**: fix round 1, 2026-09-25 — folds grill round-1 findings and founder decisions D11–D26
**Input**: `docs/internal/specs/spec-email-mail-view.md` (interview-me output, Decisions Log D1–D26)
**Round-1 review**: `docs/internal/specs/email-mail-view-spec-review.md` (BLOCK, 32 findings — every finding is dispositioned in §20)
**Work branch**: `feat/email-mail-view` · Integration branch: `release/v0.1.1` (D1 — ships in v0.1.1)
**Canonical mailbox model**: ADR-033 — *Per-(Agent, Workspace) Email Mailboxes* (unchanged; D10 — no new ADR)
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
  every tool does; user docs say so plainly); only reference metadata (Message-ID ↔ Board task / chat
  session, watcher UID state) may be stored.
- New agent tool `create_email_draft` (APPEND with `\Draft`) plus a **chat link** that opens the draft in a
  preview side panel (D8). The panel supports **view, edit, send and discard** (D12); To, subject and body
  are all editable (D23) and externally-created drafts are fully editable too, with the formatting loss
  stated (D24). Panel Send **is the approval** (D12). `send_email`/`reply` keep today's configurable policy
  (D15 — correction to D8's "unchanged approval" premise).
- The **mailbox drainer is deleted and replaced by a new-mail watcher** (D20, closes #631): the watcher never
  changes flags and never creates Board tasks; it drives the Mail tab unread badge and stores only the
  last-seen UID per mailbox. A per-mailbox switch "Let the agent handle new mail" (default **off**) starts an
  agent turn in the workspace chat; in that turn the agent reads with BODY.PEEK so mail stays unread for the
  human.
- Humans can **compose and send mail manually** from the Mail panel (D9), with To/CC/BCC.
- The Mail panel refreshes every **30 seconds while the panel is open**, never in the background from the
  panel (D25); the watcher is the separate background poller.
- Incoming HTML renders in a **sandboxed frame without scripts**, remote images **blocked by default** with a
  per-message "Load images" action (D13, D17).
- **Connection failures are never silent**: every mail-backed surface shows them in the panel, and they are
  logged with a clear message (D22 — root cause of the observed live dial timeouts is pending a
  failure-triage dispatch; the spec's requirements are written to be robust to its outcome).

Out of scope for this spec:

- Any mailbox-model change (ADR-033 stands; cap, pairing, credentials, move semantics all unchanged).
- Attachments (sending or receiving them in the Mail panel) — a later wave.
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
| `contracts/components/schemas/Mailbox.yaml` | New optional properties `signature_html` (string, ≤ 16,384 chars, default `""`), `sent_folder_name`, `drafts_folder_name` (string, default `""`), `new_mail_agent_enabled` (bool, default `false`) | D2 (signature), A1 (folder overrides — wired end-to-end per round-1 MIN-001), D20 (agent-handles-new-mail switch) |
| `contracts/components/schemas/MailboxConfigureRequest.yaml` | The same four properties | Round-1 MIN-001: overrides and the switch must be settable (the schema is `additionalProperties: false`) |

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
| `MailMessageSummary` | Envelope row (list results) | `message_id` (string \| null — some inbound mail lacks one), `uid` (int), `uidvalidity` (int), `folder` (slug), `subject` (string), `from` (string), `from_name` (string \| null), `to` (string[]), `cc` (string[] — **D26**), `date` (RFC 3339), `seen` (bool), `is_draft` (bool — `\Draft` flag), `is_omnipus_draft` (bool — `X-Omnipus-Draft` header present, FR-029) |
| `MailMessagePage` | One page of envelopes | `messages: []MailMessageSummary`, `truncated` (bool), `next_before_uid` (int \| null — mirrors `SearchResult`'s explicit-truncation contract, `pkg/email/transport.go::SearchResult`) |
| `MailMessage` | Full message (read path) | All summary fields, plus `reply_to` (string \| null), `in_reply_to` (string \| null), `references` (string \| null), `body_text` (string — decoded plain text), `has_html` (bool) |
| `MailSendRequest` | Human manual send (D9) | `to` (string[], minItems 1), `cc`, `bcc` (string[], optional), `subject` (string), `body_markdown` (string), optional `in_reply_to` (D26: CC/BCC for humans too) |
| `MailSendResponse` | Send outcome | `sent` (bool), `message_id` (string), `sent_saved` (bool), `save_warning` (string \| null — set when the Sent APPEND failed after a successful SMTP send; never silent, D7) |
| `MailDraftUpdateRequest` | Panel edit of a draft (D12/D23) | `to`, `cc`, `bcc` (address lists — D23: recipients editable), `subject`, `body_markdown` |
| `MailDraftSendRequest` | Panel send (D12) — **carries the exact content the human saw/edited plus a staleness precondition** (round-1 MAJ-002) | `to`, `cc`, `bcc`, `subject`, `body_markdown` (the displayed/edited content), `uidvalidity` (int), `uid` (int) of the viewed draft |
| `MailHtmlPreviewTokenRequest` | Mint a short-lived token for one message's HTML body | `workspace_id`, `agent_id`, `folder` (slug), `message_ref` (see §2.3 refs), `load_remote` (bool, default `false` — **D17**: blocked by default, "Load images" re-mints with `true`) |
| `MailHtmlPreviewTokenResponse` | Token payload | `token`, `expires_in_seconds` (mirrors Library `LibraryPreviewTokenResponse`) |
| `MailboxNewMailSummary` | Watcher-driven badge state per mailbox | `agent_id`, `unseen_total` (int), `watcher_enabled` (bool), `watcher_state` (enum `ok`\|`error`), `last_error_class` (string \| null) |
| `MailSummaryList` | Workspace-scoped badge summary | `items: []MailboxNewMailSummary` |
| `ErrorResponse` | Reused for all 4xx/5xx bodies (existing schema) | — |

Not built: no `MailDraftCreateRequest` REST schema — humans do not create drafts in v1 (compose offers Send;
"save as draft" for humans is out of scope). Agent draft creation is a **tool**, not a REST endpoint (§2.4).

### 2.3 New REST endpoints (all session-authenticated, versioned under `/api/v1`)

Path convention follows the existing workspace-scoped pattern (`/workspaces/{id}/media`,
`contracts/openapi.yaml`): `{id}` = workspace, `{agentId}` = the mailbox-owning agent — the ADR-033 pair
rides in the path exactly as `GET /agents/{id}/mailboxes/{workspaceId}` does, mirrored. **Messages are
addressed folder-scoped** (round-1 MAJ-005, adopted as D21):

- `ref` = `uid:<uidvalidity>:<uid>` (what every list row carries — always resolvable) or
  `mid:<Message-ID>` (what chat links carry — stable across UID renumbering).
- `mid:` resolution searches **only the addressed folder**; multiple hits resolve to the **newest by
  INTERNALDATE**. Message-ID validation: `<…@…>` shape, ≤ 998 bytes, no CR/LF (the value feeds an IMAP
  SEARCH string — bounds on shape and length are the actual defense; percent-encoded in the URL).
- Draft actions resolve only inside `drafts` (MC-13).

| Endpoint | Purpose | Status codes |
|---|---|---|
| `GET /workspaces/{id}/mail/{agentId}/folders` | Three D5 folders + counts (live IMAP) | 200 / 401 / 404 (workspace, agent, or no mailbox for the pair — `getAgentMailbox` precedent) / 502 (mail server unreachable, error class in body — MC-8) / 500 |
| `GET /workspaces/{id}/mail/{agentId}/folders/{folder}/messages?limit=&before_uid=&unseen_only=` | Envelope page. `unseen_only` filters to `\Seen` absent (defined: FR + MC test — round-1 MIN-005); `unread_count` is Inbox-only (§2.2) | 200 / 401 / 400 (unknown folder slug, limit out of range) / 404 / 502 / 500 |
| `GET /workspaces/{id}/mail/{agentId}/folders/{folder}/messages/{ref}` | Full message by folder-scoped ref | 200 / 401 / 400 (malformed ref / Message-ID validation) / 404 (not found, includes "pair has no mailbox") / 502 / 500 |
| `POST /workspaces/{id}/mail/{agentId}/messages` | Human manual send (D9): renders Markdown → multipart/alternative, signature, SMTP, Sent APPEND. **Audit event + rate limit** (FR-025/FR-026) | 200 (`MailSendResponse`) / 400 (validation) / 401 / 404 / 502 (SMTP/IMAP upstream, error class) / 500 |
| `PUT /workspaces/{id}/mail/{agentId}/folders/drafts/messages/{ref}` | Panel edit (D12/D23): APPEND updated draft (same Message-ID), `\Deleted` the old copy (FR-032). **Audit event + rate limit** | 200 (`MailMessage`) / 400 / 401 / 404 (draft gone) / 502 / 500 |
| `POST /workspaces/{id}/mail/{agentId}/folders/drafts/messages/{ref}/send` | Panel send = the approval (D12): transmits the request's content (never re-reads the draft as truth — MAJ-002), APPENDs to Sent (D18), `\Deleted` the draft (FR-032). **Audit event + rate limit** | 200 (`MailSendResponse`) / 400 / 401 / 404 (draft gone) / **409** (stale precondition — draft at `uidvalidity:uid` no longer matches) / 502 / 500 |
| `DELETE /workspaces/{id}/mail/{agentId}/folders/drafts/messages/{ref}` | Discard a draft (D12): `\Deleted` + `UID EXPUNGE` when the server supports UIDPLUS, else `\Deleted` only (deferred expunge — FR-032). **Audit event + rate limit** | 204 / 401 / 404 / 502 / 500 |
| `POST /mail/html-preview-token` | Mint HTML-body preview token (D13/D17) | 200 / 400 / 401 / 404 |
| `GET /mail/html-preview/{token}` | Serve the sanitized, sandboxed HTML body with the MC-10 header set | 200 / 404 (expired/unknown token) |
| `GET /mail/html-preview/{token}/part/{index}` | Serve one inline (`cid:`) image part within the same token scope (so inline images render without any remote host) | 200 / 404 |
| `GET /workspaces/{id}/mail/summary` | Watcher-driven badge summary (`MailSummaryList`) for the workspace's mailboxes | 200 / 401 / 404 |

Notes binding the whole table:

- **502 for upstream mail failures.** The mail server is a third party; a timeout or auth failure there is not
  a client error and not a gateway bug. The body carries a **sanitized error class** —
  `timeout|auth_failed|tls|folder_missing|server_error` — plus a generic message; raw upstream text is logged
  server-side only (round-1 MIN-011).
- **Read-only vs mutating.** Reading folders/messages performs IMAP reads only; nothing is marked \Seen by
  the list/read path. The **only** \Seen writers are: the human opening a message in the panel (FR-020),
  agent `read_message` (existing behavior, unchanged), and — never — the watcher (FR-023).
- **One IMAP session per REST request** (round-1 MAJ-012): every STATUS/LIST/SEARCH/fetch of one request runs
  on one IMAP connection; the gateway caps concurrent mail operations (A8).

### 2.4 Tool-surface changes (registered via `pkg/agent/email_tools.go::registerEmailToolsForAgent`)

| Tool | Change |
|---|---|
| `send_email` | `body` becomes **Markdown** (D3). `to` becomes a **recipient list** (minItems 1) plus optional `cc`/`bcc` lists (D26). Description updated; rendering, signature and Sent APPEND happen server-side |
| `reply` | Same body-semantics and recipient-list change as `send_email` (D3, D26) |
| `create_email_draft` | **New.** Params: `to` (list, minItems 1), `cc`/`bcc` (optional lists), `subject`, `body` (Markdown), optional `in_reply_to`. APPENDs to the mailbox's Drafts folder with `\Draft` (D8). Never sends. Generates the Message-ID itself: `<random-128-bit@domain-of-the-mailbox's-From-address>` (fallback `omnipus.invalid` when no domain is derivable); the same ID is kept through panel edits and the final send (round-1 MIN-004). Marks itself with an `X-Omnipus-Draft` header (FR-029). Returns `{"created":true,"message_id":"...","uid":123,"uidvalidity":456,"chat_link":"…" or null,"chat_link_reason":string|null}`; `chat_link` is built from `gateway.public_url` exactly like `serve_web` derives its origin — **http allowed**; when no origin is derivable (unset `public_url`, wildcard bind) the tool still succeeds and returns `chat_link: null` with the stated reason (round-1 MAJ-013; `pkg/tools/web_serve.go` precedent, `TestServeWebPublicURL`) |

### 2.5 Config keys

`MailboxConfig` (`pkg/config/config.go::MailboxConfig`) gains `SignatureHTML string
json:"signature_html,omitempty"`, `SentFolderName`/`DraftsFolderName`, and `NewMailAgentEnabled bool
json:"new_mail_agent_enabled,omitempty"`. Legacy configs load unchanged (absent fields default empty/false =
"no signature", "auto-resolve folders", "agent does not handle new mail"). No new top-level config keys; no
env vars. Watcher runtime state is **not** config (FR-033).

### 2.6 Contract regeneration

One atomic commit carries: the schema files, the `openapi.yaml` references, regenerated `pkg/api/generated/`
and `src/lib/api/generated/` artifacts, per the 5-step procedure. `make verify-contracts` must be green before
any handler or UI consuming these types is reviewed.

### 2.7 Policy wiring (Hard Constraint #6 — every touch point, round-1 MAJ-003)

`create_email_draft` must be touched in **all six** of these places or it is unreachable or boot-breaking for
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
5. `pkg/tools/auto_approve.go` — `create_email_draft` classified **AutoAsks** (conservative: if policy ever
   resolves it to `ask`, auto-approve never silently runs it).
6. `pkg/tools/email.go::EmailToolset` — so registration, permissions screen and tests come from the same list
   the five existing tools use.

Reachability is asserted **behaviorally**, not by grep: for a seeded core agent and an operator-created
agent, both with an enabled mailbox, the effective policy of `create_email_draft` resolves to `allow` and the
tool is present in the agent's registry (§9 SC-007).

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
| `pkg/tools/auto_approve.go` | extends | Email classification verified: send tools `AutoAsks`, read tools `AutoRuns`; `create_email_draft` joins as `AutoAsks` |
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
| `src/main.tsx::createHashHistory` | constraint | The SPA router uses hash history (verified) — deep links carry `#/…`, and in-place navigation is detected by **hash-route pattern, not origin match** (round-1 MAJ-013) |
| `src/components/chat/markdown-shared.tsx::createLinkRenderer` | extends | Single shared link renderer for live + historical chat markdown; `src/lib/url-safe.ts::isSafeHref` allow-lists **http/https/mailto/tel only** — a mail deep link must be an absolute http(s) URL |
| `src/components/connectors/EmailMailboxPanel.tsx::EmailMailboxPanel` | extends | The mailbox config dialog (mounted from `src/components/screens/ConnectorsScreen.tsx`), form validation + move semantics — hosts the D2 signature editor, the folder-override advanced fields, and the D20 switch |

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

### US-6 — Read state that stays honest (P1) — D20 (supersedes round-1's Q5)

Read state is IMAP `\Seen`, shared with the owner's own mail client. The Mail panel's list never marks
anything read; **opening** a message in the panel marks it `\Seen` (normal client semantics). Agent
`read_message` keeps its existing `\Seen` behavior (unchanged). The D20 watcher **never** mutates flags and
never creates Board tasks — in a watcher-triggered agent turn ("Let the agent handle new mail", default off)
the agent reads with BODY.PEEK so mail stays unread for the human. (This story replaces the round-1 draft's
"handled by agent" badge, which was built on the drainer that #631 deletes — CRIT-001.)

**Why this priority**: display correctness, not capability — the panel works without it, but a founder whose
Inbox always shows zero unread will distrust it.

**Independent test**: with one unseen message, open it in the panel (it becomes seen); with a second unseen
message, run a watcher-triggered agent turn (switch on) and confirm the message stays unseen afterwards and
no Board task was created.

**Acceptance scenarios**:

1. **Given** a message unseen by anyone, **When** the human opens it in the Mail panel, **Then** it is marked
   `\Seen` and the Inbox unread count drops on the next refetch.
2. **Given** a watcher-triggered agent turn reading new mail, **When** the turn completes, **Then** the read
   messages remain unseen and no Board task exists for them (D20: BODY.PEEK, never flags, never tasks).
3. **Given** the per-mailbox switch "Let the agent handle new mail" (default off), **When** it is off,
   **Then** new mail starts no agent turn; **when on**, **Then** new mail (UID > stored last-seen UID) starts
   exactly one agent turn in that workspace's chat.

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
- When the watcher observes new mail, it changes no flag, creates no task, and only advances its UID state and
  the badge summary (D20).

### 5.2 Explicit non-behaviors

- The system must not give agents a raw-HTML authoring path, because D3 promises model-authored HTML never
  reaches recipients (Markdown + server-side sanitize only). The signature is operator HTML but is sanitized
  on save (FR-003) — raw operator HTML never ships either.
- The system must not store message bodies locally (beyond session transcripts, which D21 explicitly keeps),
  because D6 promises the mailbox stays the single source of truth; watcher state files hold UIDs/counts/an
  error class only (FR-033).
- The system must not let the Mail panel's list/read path mutate mailbox flags; the only \Seen writers are the
  human panel open (FR-020) and agent `read_message` (unchanged) — the watcher never writes flags (D20),
  because flags are shared with the owner's own mail client.
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
| MC-2 | Rendered HTML part contains no `<script>` element, no `on*=` handler attribute, and no `javascript:` href — verified on a compose of hostile **body** `<script>alert(1)</script><img src=x onerror=alert(1)><a href="javascript:alert(1)">x</a>` **and** a hostile **signature**; and the signature's stored value is the sanitized form (inline `style`, tables, https/data `img` preserved) | unit tests on the render pipeline and on signature save |
| MC-3 | **Every** outbound message is `multipart/alternative` with exactly one `text/plain` and one `text/html` part, signature or not. The `text/plain` part is the plain-text rendering of the Markdown (goldmark text rendering of the same parse — links as `text (href)`, no HTML) followed by `-- ␊` and the plain-text-derived signature when a signature exists; the `text/html` part is the sanitized render followed by the HTML signature | unit test on the composer |
| MC-4 | `\r\n` or `\n` in a subject/recipient never reaches a header (existing `sanitizeHeader` behavior holds); non-ASCII subjects/display names are RFC 2047-encoded and parse back to the original | unit tests (header injection + RFC 2047) |
| MC-5 | Folder slugs are exactly `inbox` \| `sent` \| `drafts`; any other value → HTTP 400 | contract + handler test |
| MC-6 | `limit` ≤ 0 → default 20; `limit` > 100 → clamped to 100 (mirrors `clampLimit`, `pkg/email/transport.go`) | unit test |
| MC-7 | Ref not found in the addressed folder (unknown `uid:` or no `mid:` hit) → HTTP 404 `ErrorResponse`; multiple `mid:` hits → newest by INTERNALDATE | handler + transport tests |
| MC-8 | Mail server dial/command failure on **any** mail endpoint (IMAP and SMTP) → HTTP 502 with a sanitized error class (`timeout\|auth_failed\|tls\|folder_missing\|server_error`) and a generic message; raw upstream text appears only in the server log; SMTP returns within the bounded timeouts of FR-027 (never hangs) | handler tests with black-hole/failing transports |
| MC-9 | SMTP success + Sent APPEND failure → `MailSendResponse.sent_saved=false` + `save_warning` non-empty (tool result carries the same warning) | unit test on the send path |
| MC-10 | The HTML preview response carries: Content-Security-Policy `default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; font-src data:; form-action 'none'; frame-ancestors 'self'` — where `img-src` relaxes to `'self' data: https:` only when `load_remote=true` (D17); iframe sandbox attribute **without** `allow-scripts`/`allow-same-origin`/`allow-forms`/`allow-top-navigation`, **with** `allow-popups allow-popups-to-escape-sandbox` for normal link clicks (D13 "feels normal"); `Referrer-Policy: no-referrer` (the token rides the URL path); `X-Content-Type-Options: nosniff`; token bound to (pair, folder, ref, load_remote) with the Library token TTL; served HTML bounded by the existing 256 KB inbound body cap (`::capBody`) | gateway handler tests, one per directive |
| MC-11 | `create_email_draft` result JSON parses with `created=true`, a non-empty `message_id` matching the APPENDed message's Message-ID header, `uid`/`uidvalidity`, and `chat_link` either null-with-reason or an absolute http(s) URL derived per FR-015 | unit test with fake transport |
| MC-12 | After exercising all Mail panel endpoints against a temp data dir, a sweep of the data dir shows no file containing message body text (watcher state files excepted — they contain UIDs/counts only); transcripts excluded per D21 | integration test assertion |
| MC-13 | A draft deep link for an agent/workspace that does not exist, or a pair with no mailbox → HTTP 404 with no body or folder leakage (single-user model — `GatewayConfig.Users` holds at most one entry; there is no second session to test) | handler test |
| MC-14 | Drafts listed with `is_draft=true` and `is_omnipus_draft` reflecting `X-Omnipus-Draft`; APPEND to Drafts always sets the `\Draft` flag; listings exclude `\Deleted` messages | transport fake assertion |
| MC-15 | The five existing email tools' schemas change only per D26 (recipient lists) and D3 (body = Markdown description text); no other parameter changes | contract review check |
| MC-16 | Panel send with a stale precondition (draft at `uidvalidity:uid` no longer matches) → HTTP 409, nothing transmitted, no flag or folder mutated | handler test |
| MC-17 | Discard/edit old-copy deletion: with UIDPLUS, `UID EXPUNGE` removes exactly the target UID; without UIDPLUS, only `\Deleted` is stored and listings (ours) exclude it — a non-UID `EXPUNGE` is never issued by Omnipus | transport fake tests (both server shapes) |
| MC-18 | A full watcher cycle over a mailbox with new mail: no `\Seen`/flag STORE issued, no Board task created, state file gains only `last_seen_uid`/`unseen_total`/error fields (D20) | transport fake + state-file assertion |
| MC-19 | Human send, panel send, panel edit and panel discard each emit an audit event carrying pair, folder, Message-ID, recipient addresses, origin (`human`\|`agent-draft`), argument hash and outcome; agent tool sends remain covered by existing transcript/policy records | gateway audit tests (`pkg/gateway/rest_preview_audit.go` precedent, `pkg/audit::argshash`) |
| MC-20 | The mutating mail routes (manual send, panel send/edit/discard) are wrapped in `withRateLimit` with a per-IP sliding window of **10 requests/minute**; the 11th within the window → HTTP 429 | handler test |
| MC-21 | SMTP dial black-hole returns within the dial bound; a stall after connect returns within the command bound; caller-context cancellation aborts the send — all three via the FR-027 mechanisms (#629) | unit tests on `Client.Send` |
| MC-22 | Outbound body beyond **1 MiB** → HTTP 400 on the REST send routes and a tool error on the agent path (nothing transmitted) | unit + handler tests |
| MC-23 | `GET /workspaces/{id}/mail/summary` returns per-mailbox `unseen_total`, `watcher_enabled` and `watcher_state` matching the watcher state files; a mailbox in error state reports `watcher_state=error` with `last_error_class` | handler + watcher tests |

### 5.4 Integration boundaries

| External system | Data in/out | Contract | Failure behavior |
|---|---|---|---|
| IMAP server (per mailbox) | Folder list/counts, envelope pages, full messages, APPEND (drafts, sent copies), flag stores, UID EXPUNGE, BODY.PEEK reads | `emersion/go-imap/v2` over IMAPS (993) — existing `Client` patterns (`pkg/email/transport.go`); UIDPLUS probed for FR-032 | Every UI surface maps failures to sanitized-class error states (MC-8); the watcher logs and degrades its badge state, never silently (D22) |
| SMTP server (per mailbox) | Outbound RFC 5322 message | STARTTLS 587 / SMTPS 465 — **context-threaded, bounded** (FR-027, #629) | Send failure → tool error / compose dialog error, nothing APPENDed |
| SPA (Mail panel, compose) | The §2.3 REST endpoints; the §2.2 schemas | Generated types only (`src/lib/api/generated/`) | TanStack Query error states; retry affordances; 30 s visible-only refresh (D25) |
| Chat (link → panel) | The `chat_link` URL scheme: `…/#/workspaces/{wsId}/mail?mailbox={agentId}&folder=drafts&message=mid%3A%3C…%3E` (hash-history pattern match — `src/main.tsx::createHashHistory`) | Absolute http(s) URL derived like `serve_web` (passes `isSafeHref`); `chat_link` null-with-reason when no origin is derivable (FR-015) | Unknown/deleted target → panel not-found state (US-4 AS-3); sent draft → "Sent on" state (US-4 AS-4) |

Development uses the in-memory transport fake pattern that already exists for the five tools
(`pkg/tools/email.go` header note: "fully unit-testable against an in-memory fake") — no real IMAP/SMTP server
is a dependency of the test suite; live-mailbox acceptance belongs to the UAT campaign (§9.3). D18 (generic
IMAP only) means **no provider-specific behavior anywhere in the pipeline** — including no Sent-copy
dedication logic; the duplicate-Sent-copy consequence on providers that auto-file sent mail is documented as
accepted (FR-006).

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
**Traces to**: US-3, AS-4 · **Category**: Error Path
- **Given** a mailbox whose IMAP host is unreachable
- **When** the Mail panel loads folders
- **Then** the surface shows an explicit error with the sanitized class (`timeout`) and a retry control (MC-8)
- **But** no empty-folder state is presented as if the mailbox were empty
- **And** the gateway log carries the raw upstream cause (D22)

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

#### Scenario B-17: HTML mail renders sandboxed; remote images need a click
**Traces to**: US-3, AS-7 · **Category**: Happy Path
- **Given** an HTML message carrying a remote `<img>` (tracking pixel), an inline `cid:` logo, and a `<form>`
- **When** the human opens it
- **Then** the HTML renders in a frame whose CSP/sandbox matches MC-10 (no scripts, no forms, no same-origin)
- **And** the remote image loads zero third-party requests until "Load images" is clicked (D17), which re-mints the token with `load_remote=true`
- **And** the inline `cid:` image renders through `/mail/html-preview/{token}/part/{index}` without any remote host

#### Scenario B-18: Watcher badge summary reflects mailboxes
**Traces to**: US-3, AS-1 · **Category**: Happy Path
- **Given** two enabled mailboxes with unseen mail and one in a watcher error state
- **When** the SPA fetches `GET /workspaces/{id}/mail/summary`
- **Then** each mailbox returns `unseen_total`, `watcher_enabled` and `watcher_state` (one `error` with `last_error_class` set, MC-23)
- **And** the workspace tab strip's Mail entry shows the unseen badge while the panel is closed (A9)

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

#### Scenario B-27: Panel-open read marks \Seen
**Traces to**: US-6, AS-1 · **Category**: Happy Path
- **Given** a message unseen by anyone
- **When** the human opens it in the Mail panel
- **Then** `\Seen` is stored and the Inbox unread count drops on the next refetch

#### Scenario B-28: Watcher turn leaves mail unread and creates no task
**Traces to**: US-6, AS-2 · **Category**: Happy Path
- **Given** the per-mailbox switch "Let the agent handle new mail" on, and new mail arriving
- **When** the watcher-triggered agent turn reads the new mail
- **Then** the messages remain unseen (BODY.PEEK read path, FR-020) and no Board task exists (D20, MC-18)

#### Scenario B-29: Switch off means no agent turn
**Traces to**: US-6, AS-3 · **Category**: Alternate Path
- **Given** the per-mailbox switch off (the default)
- **When** new mail arrives and a watcher cycle runs
- **Then** the watcher advances its UID state and badge counts only — no agent turn starts

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

---

## 7. TDD plan (tests designed before implementation)

E2E note: Playwright E2E against a **live IMAP/SMTP server is not part of CI** — the suite has no mail
server. Logic coverage lives at unit + gateway-integration level (in-memory transport fakes, per the existing
`pkg/tools` fake pattern); live-mailbox behavior is accepted in the **UAT campaign** with real mailboxes, and
E2E is limited to surfaces that need no mail server (tab/panel reachability, signature editor open/save,
compose validation, empty/error states with an unreachable mailbox, refresh cadence with fake timers).

| Order | Test name (indicative) | Level | Traces to BDD | Description |
|---|---|---|---|---|
| 1 | `TestComposeMultipart_SignatureAndSanitize` | Unit | B-2, B-6 | Markdown → multipart/alternative; sanitizer kills script/on*/javascript: in body **and** signature (MC-2, MC-3); signature on both parts |
| 2 | `TestComposeMultipart_NoSignature` | Unit | B-3 | Empty signature → still multipart (MC-3), no signature block |
| 3 | `TestComposeMultipart_HeaderInjectionGuard` + `TestComposeHeaders_RFC2047` | Unit | B-10, (MC-4) | CRLF never reaches headers; UTF-8 subject/display name encode and round-trip |
| 4 | `TestSignatureSanitizedOnSave` | Unit | B-4 | Signature policy keeps style/tables/https+data img, strips script/handlers/javascript:/forms/iframes; stored value is sanitized |
| 5 | `TestClientSend_AppendsToSent` | Unit | B-7 | SMTP-success path APPENDs the exact message to the resolved Sent folder (always — D18) |
| 6 | `TestClientSend_AppendFailureSurfaces` | Unit | B-8 | APPEND failure → explicit warning, send not hidden (MC-9) |
| 7 | `TestSendEmailTool_RecipientLists` | Unit (fake) | B-9 | To/CC/BCC lists on the right header lines; Bcc absent from the Sent copy (D26) |
| 8 | `TestComposeSizeBound` | Unit | B-10 | 1 MiB passes, 1 MiB+1 → 400/tool error, nothing transmitted (MC-22) |
| 9 | `TestClientAppendDraft_SetsDraftFlag` | Unit | B-19, B-23 | Draft APPEND sets `\Draft` + `X-Omnipus-Draft`; no SMTP session opened |
| 10 | `TestResolveSpecialUseFolders_OverridesWin` | Unit | B-11 | Special-use (RFC 6154) resolution with config-override precedence (A1) |
| 11 | `TestReadByRef_FoundAndNotFound` | Unit | B-12, B-21 | `uid:`/`mid:` resolution folder-scoped; miss → 404 (MC-7); multi-hit → newest INTERNALDATE |
| 12 | `TestReadByMessageID_AfterUIDValidityChange` | Unit | B-36 | `mid:` resolution survives UIDVALIDITY renumbering (B-36's oracle) |
| 13 | `TestSendEmailTool_MarkdownBodyPipeline` (+ signature assertion) | Unit (fake) | B-2, B-6 | Tool-level: body treated as Markdown, signature on both parts of the transmitted message; result text unchanged in shape |
| 14 | `TestCreateEmailDraftTool_ResultContract` | Unit (fake) | B-19 | Result JSON contract incl. nullable `chat_link` + reason (MC-11, FR-015) |
| 15 | `TestMailboxSignature_ConfigRoundTrip` | Unit | B-1 | `signature_html` + folder overrides + D20 switch persist via config load/save; legacy config loads unchanged (§2.5) |
| 16 | `TestPutAgentMailbox_SignatureLimit` | Integration (httptest) | B-5 | 16,384-char bound → 400 (MC-1) |
| 17 | `TestMailFoldersEndpoint_PairMissing` | Integration | B-11, B-37 | 404 when agent/pair missing (ADR-033 precedent); 400 unknown slug (MC-5) |
| 18 | `TestMailMessagesEndpoint_PagingBounds` | Integration | (MC-6) | limit clamp/before_uid pass-through; truncated flag mirrors `SearchResult`; `unseen_only` filter |
| 19 | `TestMailEndpoints_UpstreamTimeout` | Integration | B-14 | Failing/black-hole transport → 502 with sanitized class, raw cause in log only (MC-8, D22) |
| 20 | `TestMailSendEndpoint_MultipartAndSent` | Integration | B-26 | Manual send → same pipeline; `sent_saved` reporting; To/CC/BCC headers (D26) |
| 21 | `TestClientSend_DialBlackhole_ReturnsWithinBound` / `TestClientSend_StallAfterConnect_ReturnsWithinBound` / `TestClientSend_ContextCancel_Aborts` | Unit | (MC-21) | #629 absorbed: dial bound, post-connect stall bound, context cancellation |
| 22 | `TestMailSendEndpoint_SMTPTimeout_502` | Integration | (MC-8) | SMTP failure surfaces as 502 + class within the bound — never a hang |
| 23 | `TestDraftEditEndpoint_SameMessageID` | Integration | B-30 | Edit APPENDs updated draft (same Message-ID), `\Deleted` old copy, link still resolves |
| 24 | `TestDraftSendEndpoint_ApprovalFlow` | Integration | B-31 | Panel send transmits the request's content; Sent APPEND; draft removed; audit event `origin=agent-draft` |
| 25 | `TestDraftSend_StalePrecondition_409` / `TestDraftEdit_StalePrecondition_409` | Integration | B-32, B-35 | Mismatched `uidvalidity:uid` → 409, nothing transmitted — send and edit-save variants (MC-16) |
| 26 | `TestDraftDiscard_DeferredExpunge` | Integration | B-33 | UIDPLUS: UID EXPUNGE removes exactly the target; non-UIDPLUS: `\Deleted` only, listings exclude it (MC-17) |
| 27 | `TestDraftEdit_DeleteFlagFailWarns` | Unit | B-34 | APPEND ok + delete-flag fail → explicit duplicate warning |
| 28 | `TestForeignDraftConversion` | Unit | B-24 | Non-Omnipus draft → editable Markdown derived, loss statement data, Message-ID kept through send |
| 29 | `TestPanelOpenMarksSeen` | Integration | B-27 | Panel message open → `\Seen` stored; list read → no flag change |
| 30 | `TestWatcher_NeverMutatesFlags` + `TestWatcherTurnPeekNoSeen` | Unit | B-28, B-29, B-15 | Watcher cycle: no flag STORE, no Board task, state file UIDs/counts only (MC-18); watcher agent turn reads BODY.PEEK |
| 31 | `TestMailSummaryEndpoint` | Integration | B-18 | Badge summary matches watcher state; error-state mailbox reports class (MC-23) |
| 32 | `TestMailAuditEvents` | Integration | (MC-19) | Four human actions emit audit events with pair/folder/Message-ID/recipients/origin/hash |
| 33 | `TestMailRateLimit` | Integration | (MC-20) | 11th mutating mail request in the window → 429 |
| 34 | `TestEffectiveDraftToolPolicy_BothAgentKinds` | Unit | (FR-013) | Seeded core agent **and** operator-created agent, both with enabled mailbox: `create_email_draft` effective policy = `allow` and present in the registry (replaces the round-1 SC-007 grep) |
| 35 | `TestDeepLink_CrossPairDenied` | Integration | B-21 | Unknown agent/workspace or pair without mailbox → 404, no leakage (MC-13) |
| 36 | `TestNoLocalMailStore` | Integration | B-15 | Data-dir sweep after full exercise finds no bodies (MC-12) |
| 37 | `MailTab.spec.ts` — tab opens, picker, badge, error state | E2E (no mail server) | B-13, B-14, B-18 | Tab strip entry, mailbox picker, unreachable-mailbox error surface, badge render |
| 38 | `MailSignatureEditor.spec.ts` | E2E (no mail server) | B-1, B-5 | Signature editor in Connectors: preview, save, bound |
| 39 | `MailCompose.test.tsx` | Component | B-25 | Field-level validation blocks empty sends |
| 40 | `MailPanelRefresh.test.tsx` | Component (fake timers) | B-16 | 30 s refetch while mounted; none when unmounted (D25) |
| 41 | `MailHtmlFrame.spec.ts` | Component | B-17 | Sandboxed frame posture; "Load images" re-mint; `cid:` part route used |
| 42 | `DraftDeepLink.spec.ts` | E2E (stubbed route state) | B-20, B-21, B-22 | Link → panel navigation; not-found state; "Sent on" state |
| 43 | `MailDraftMarkdownRender.test.tsx` | Component | (MIN-009) | Raw HTML in draft Markdown stays inert (`skipHtml`; hostile `<img onerror>` renders nothing) |
| 44 | UAT lane — live mailbox | UAT | B-1..B-36 (sample) | Real IMAP/SMTP: signature on received mail, Sent APPEND visible in the owner's client (duplicates accepted per D18 on auto-filing providers — the check is "appears", not "appears exactly once"), draft link → panel, edit, send, discard, watcher badge, 30 s refresh |

### 7.1 Test datasets

| Dataset | Rows (boundary → edge → error → happy) | Traces to |
|---|---|---|
| DS-1 Signature values | `""` (no signature), 16,384 chars (max), 16,385 (reject), valid HTML, hostile HTML (script/on*/javascript:), **real-world signature with inline `style` + https logo (must survive sanitization intact)** | B-1, B-3, B-4, B-5 |
| DS-2 Markdown bodies | empty body (reject), heading+link+code block, raw HTML/script injection, body at 1 MiB and 1 MiB+1, unicode subject/display name (RFC 2047), CRLF injection attempt | B-6, B-10, (MC-2, MC-4, MC-22) |
| DS-3 Folder paging | `limit=0` (default 20), `limit=1`, `limit=101` (clamp 100), `before_uid=1` (empty), `before_uid=<min>` (page boundary), empty folder, `unseen_only` true/false | B-11, (MC-6) |
| DS-4 Message addressing | existing Message-ID, missing Message-ID (404), message without Message-ID header (listed `message_id: null`), percent-encoded angle brackets, UIDVALIDITY-changed mailbox, **duplicate Message-ID across folders (folder-scoping resolves)**, **spoofed inbound Message-ID matching a draft (drafts resolve only inside `drafts`)**, **Message-ID > 998 bytes / malformed (400)** | B-12, B-21, B-36 |
| DS-5 Send outcomes | SMTP ok + APPEND ok, SMTP ok + APPEND fail, SMTP fail (no APPEND attempted), SMTP dial black-hole (bounded), SMTP stall after connect (bounded), invalid recipient (reject client-side), To/CC/BCC combinations | B-7, B-8, B-9, B-26 |
| DS-6 Draft lifecycle | create ok, create with missing to (reject), draft deleted before link click, draft in nonexistent pair (404), **foreign (owner-authored) draft**, **stale-precondition send (409)**, **edit: APPEND ok + delete-flag fail (warning)**, **discard under non-UIDPLUS server** | B-19, B-21, B-23, B-24, B-30..B-35 |
| DS-7 Mailbox roster | 0 mailboxes (empty state), 1 mailbox (no picker), 2 mailboxes (picker), mailbox enabled but password unresolvable (skip + explicit state) | B-11, B-13, B-14 |
| DS-8 Watcher cycles | no new mail, new mail (UID advance), 30+ new messages in one cycle, mailbox in error (dial timeout), switch off, switch on (agent turn, PEEK) | B-18, B-28, B-29 |

### 7.2 Regression impact

Existing behaviors that MUST be preserved (or are deliberately deleted):

- **The drainer is deleted, not protected** (#631, D20): `pkg/email/drainer.go`, `pkg/heartbeat/mailbox_drain.go`
  and both wiring sites (`gateway_boot.go`, `gateway_reload.go`) go, together with their suites — after this
  spec lands, no code or test references `Drainer`. The drainer's *task-creation* behavior class is retired
  by decision (D20), and the replacement watcher is covered by MC-18/DS-8.
- `read_inbox` / `search_email` / `read_message` semantics unchanged (folder=INBOX only, \Seen on
  `read_message`, envelope-only lists, pagination cursors) — `pkg/tools` email suites stay green; only
  recipient-list (D26) and description-text (D3) assertions change (MC-15).
- Send-policy resolution is **unchanged** (D15): the ceiling ships as it ships today; built-in roles keep
  `ask` via the ADR-090 inventory; the only behavior change is D19's configure-time fill (replacing
  `grantEmailToolAllows`), which writes **only** where the agent has no explicit entry. T34 asserts both agent
  kinds; the greenfield ruling means no migration of previously allow-filled entries.
- Mailbox config round-trip including the strict legacy/nested shape rule
  (`pkg/config` mailbox migration tests) must keep passing with the new fields present.
- The SPA embed/CSP surface is untouched except the new `mail/html-preview` routes, which carry their own
  headers and no new `script-src` relaxation — `pkg/gateway/embed.go` policy untouched.
- `smtp.Dial`/`tls.Dial` replacement by context-dialers is behavior-preserving on the happy path — existing
  `pkg/email` send tests stay green unmodified (MC-21 adds the bounded-failure tests).

---

## 8. Functional requirements

- **FR-001**: The system MUST store one optional HTML signature per (agent, workspace) mailbox, configurable
  through the existing mailbox panel and validated to ≤ 16,384 chars (MC-1). [D2, ADR-033]
- **FR-002**: The signature editor MUST show a live rendered preview of the signature as it is edited. [D2]
- **FR-003**: The signature MUST be sanitized on save with a dedicated signature policy (keeps inline
  `style`, tables, `img` over https/data; strips script, event handlers, `javascript:`, forms, iframes); the
  stored value is the sanitized one and the PUT returns what was stored. Agents MUST NOT author the signature
  or raw HTML; signature application happens server-side at compose time (MC-2). [D3; round-1 MAJ-008;
  **security-lead review of both sanitizer policies is a pre-implementation gate**]
- **FR-004**: The system MUST render agent message bodies (`send_email`, `reply`, `create_email_draft`) from
  Markdown into sanitized `text/html` plus derived `text/plain` (multipart/alternative, MC-3). [D3]
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
  MUST NOT persist message bodies (MC-12); reference metadata (Message-ID ↔ task/session, watcher UID state)
  is permitted. Session transcripts keep email text as every tool does; user docs state it plainly. [D6 as
  scoped by D21]
- **FR-010**: The Mail surface MUST offer a mailbox picker when a workspace has more than one mailbox, fed by
  `GET /api/v1/mailboxes` filtered to the current workspace, with the selection kept in per-workspace
  sessionStorage. [D4; round-1 MIN-008]
- **FR-011**: New REST endpoints and schemas MUST match §2 exactly, contract-first per Hard Constraint #8.
  [D4–D9, D12, D17–D26]
- **FR-012**: A new `create_email_draft` tool MUST APPEND a `\Draft`-flagged message carrying `X-Omnipus-Draft`
  to the mailbox's Drafts folder, generate its own Message-ID (`<random-128-bit@from-domain>`, kept through
  edits and send), never send, and return `message_id` + `uid`/`uidvalidity` + `chat_link` (nullable with
  reason) (MC-11, MC-14). [D8, D12; round-1 MIN-004, MAJ-013]
- **FR-013**: `create_email_draft` MUST be wired at all six touch points of §2.7 (ceiling default, D19 fill
  set, role inventory grants, seed literal, auto-approve classification, `EmailToolset`), and reachability is
  asserted behaviorally for a seeded core agent and an operator-created agent (T34). [Hard Constraint #6;
  round-1 MAJ-003]
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
  send without a prompt; the draft flow is the designed safe path. [D15, D19; round-1 CRIT-002 disposition:
  rejected per founder decision]
- **FR-017**: Humans MUST be able to compose and send a message from the Mail surface through the same
  multipart + signature pipeline (FR-004/FR-005 apply). [D9]
- **FR-018**: Every mail-backed surface MUST render upstream mail failures as explicit, class-named error
  states with a retry affordance (MC-8) and MUST log the raw upstream cause server-side with structured
  fields (pair, folder, operation, duration, error class) — failures are never silent in the panel or the
  logs. [D22; round-1 MIN-010, MIN-011]
- **FR-019**: Incoming-message HTML MUST render in a sandboxed frame without script execution, forms or
  same-origin; remote content MUST be blocked by default with a per-message "Load images" action that re-mints
  the token with `load_remote=true` (D17); the response MUST carry the full MC-10 header set (incl.
  `Referrer-Policy: no-referrer`, `form-action 'none'`, token binding/TTL); inline `cid:` images render via
  the token-scoped part route. **security-lead review of the exact header set is a pre-implementation gate.**
  [D13, D17; round-1 MAJ-007]
- **FR-020**: Read state is IMAP `\Seen`. The panel's list MUST never mark read; opening a message in the
  panel MUST store `\Seen`; agent `read_message` keeps its existing behavior; a watcher-triggered agent turn
  MUST read with BODY.PEEK so mail stays unread. [D20; round-1 CRIT-001]
- **FR-021**: The draft panel MUST support view, edit (To/subject/body — D23; foreign drafts included, with
  the formatting-loss statement — D24), send (the approval — D12), and discard; sending MUST carry the exact
  displayed/edited content plus a uidvalidity+uid precondition and MUST return 409 on staleness (MC-16).
  [D12, D23, D24; round-1 MAJ-001, MAJ-002]
- **FR-022**: All send paths (agent tools and human compose) MUST accept To/CC/BCC recipient lists (D26);
  BCC recipients receive the message but the BCC header MUST be absent from the Sent copy. [D26;
  round-1 Q4 disposition]
- **FR-023**: The mailbox drainer MUST be deleted (closes #631) and replaced by a new-mail watcher that MUST
  NOT change any flag and MUST NOT create Board tasks; it MUST advance a per-mailbox `last_seen_uid` state
  file (UIDs/counts/error-class only — FR-033) and drive the Mail tab unread badge via the summary endpoint.
  Watcher cycles run every 60 s (today's cadence, A8) under the FR-027 bounds. [D16, D20; round-1 CRIT-001]
- **FR-024**: A per-mailbox switch "Let the agent handle new mail" (default **off**, wired through
  `MailboxConfigureRequest` §2.1) MUST gate watcher-triggered agent turns in that workspace's chat; the turn
  reads via the peek path (FR-020). [D20]
- **FR-025**: Human send, panel send, panel edit and panel discard MUST each emit an audit event carrying
  pair, folder, Message-ID, recipient addresses, origin (`human`\|`agent-draft`), argument hash and outcome.
  [round-1 MAJ-011; `pkg/audit::argshash`, `rest_preview_audit.go` precedent]
- **FR-026**: The mutating mail routes MUST be rate-limited per IP (10 requests/minute sliding window,
  MC-20 — reviewer-challengeable constant, A10). [round-1 MAJ-011]
- **FR-027**: `Client.Send` MUST honour the caller's context; SMTP dial MUST use bounded context dialers and
  every SMTP command phase MUST run under a deadline bounded by `commandTimeout`; the Sent APPEND runs under
  the same context; IMAP's `dialIMAP` timeout path closes its late connection (all of #629). MC-21/MC-8 test
  the bounds. [D21 closes #629; round-1 MAJ-015]
- **FR-028**: Messages MUST be addressed folder-scoped: `ref` = `uid:<uidvalidity>:<uid>` or
  `mid:<Message-ID>` (chat links); `mid:` resolution searches only the addressed folder, newest
  INTERNALDATE on multi-hit; Message-IDs validated as `<…@…>`, ≤ 998 bytes, no CR/LF (MC-7). [D21 adopts
  round-1 MAJ-005]
- **FR-029**: Omnipus drafts MUST be APPENDed as fully rendered multipart/alternative (rendered body +
  signature at draft time, so the owner's own client sees a real draft — round-1 MAJ-006 option 1) with the
  Markdown source in an `X-Omnipus-Markdown` header for lossless panel editing; panel send always re-renders
  from the (possibly edited) Markdown. [D12, D24]
- **FR-030**: Drafts without `X-Omnipus-Draft` MUST still be fully editable and sendable (D24): the panel
  derives editable Markdown from the HTML (or plain) part, states plainly in the UI what may be lost
  (styling, images, table layout) and what is preserved (text, headings, lists, links), and sending re-renders
  through the Markdown pipeline keeping the Message-ID. [D24; round-1 MAJ-006]
- **FR-031**: Outbound bodies MUST be bounded at 1 MiB (400/tool error beyond, MC-22); non-ASCII subjects and
  display names MUST be RFC 2047-encoded (MC-4). [round-1 MIN-007]
- **FR-032**: Draft/old-copy deletion MUST use `UID EXPUNGE` where the server supports UIDPLUS, else store
  `\Deleted` only (deferred expunge); Omnipus MUST never issue a non-UID `EXPUNGE`; listings MUST exclude
  `\Deleted` messages (MC-17). [round-1 MAJ-001]
- **FR-033**: Watcher state MUST live in per-mailbox state files under the data dir (atomic writes) holding
  only `last_seen_uid`, `unseen_total`, `last_ok`, `last_error_class` — never message content (D6, MC-12).
  [D20]

## 9. Success criteria

- **SC-001**: With a signature configured, 100% of outgoing messages from that mailbox (all five send paths:
  `send_email`, `reply`, panel send, manual send, and a sent draft's Sent copy) carry the signature on both
  MIME parts — verified by unit + integration suites and the UAT lane.
- **SC-002**: Zero occurrences of `<script`, `on*=` handlers or `javascript:` URIs survive the renderer or
  the signature-save policy across DS-1's and DS-2's hostile rows.
- **SC-003**: Every send with successful SMTP results in a Sent-folder APPEND success or an explicit warning —
  zero silent partial sends in DS-5.
- **SC-004**: With the Mail panel open, the folder list and badge state refetch within **30 seconds** of the
  last fetch (D25) and no panel-initiated refetch fires while the panel is closed; no message body bytes are
  written under the data dir during the DS-3/DS-4 exercise (MC-12).
- **SC-005**: A draft created by `create_email_draft` is openable from its chat link or the tool-result
  "Open draft" action in ≤ 2 clicks (click → panel shows draft), including after a UIDVALIDITY change (DS-4).
- **SC-006**: With the mail server unreachable, all mail surfaces (folders, messages, sends) show an explicit
  class-named error within one bounded request round-trip — zero silent-empty renders in the DS-7 rows and
  zero hangs in the DS-5 SMTP rows (MC-8, MC-21).
- **SC-007**: Behavioral reachability (T34): for a seeded core agent **and** an operator-created agent, both
  with an enabled mailbox, `create_email_draft`'s effective policy resolves to `allow` and the tool is
  present in the agent's registry; additionally `grep -rl '"create_email_draft"' pkg/coreagent/ pkg/config/
  pkg/gateway/ pkg/tools/` returns ≥ 4 files — the Definition-of-Done grep made stronger (registration,
  ceiling, fill set, inventory/seed) and exempt from test files (`*_test.go` excluded).

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
| FR-009 | US-3 | B-12, B-15 | T36, T44 (live) |
| FR-010 | US-3 | B-13 | T37 |
| FR-011 | US-3..US-7 | all endpoint scenarios | T16–T26, T31–T33 |
| FR-012 | US-4 | B-19, B-23, B-36 | T9, T12, T14 |
| FR-013 | US-4 | (policy resolution — no user-visible scenario; asserted behaviorally) | T34, SC-007 |
| FR-014 | US-4 | B-20, B-21, B-22, B-36 | T35, T42 |
| FR-015 | US-4 | B-19, B-20, B-36 | T14, T42, DS-4 |
| FR-016 | US-4 | B-23, B-29 | existing send approval suites (green) + T34 |
| FR-017 | US-5 | B-26, B-25 | T20, T39 |
| FR-018 | US-3, US-5, US-7 | B-14, B-32 | T19, T22, T37 |
| FR-019 | US-3 | B-17 | T41, plus the MC-10 handler tests — security-lead gated |
| FR-020 | US-6 | B-27, B-28, B-29 | T29, T30 |
| FR-021 | US-7 | B-30, B-31, B-32, B-33, B-34, B-35, B-24 | T23, T24, T25, T26, T27, T28 |
| FR-022 | US-2, US-5 | B-9, B-26 | T7, T20 |
| FR-023 | US-6, US-3 | B-15, B-18, B-28, B-29 | T30, T31, T36 |
| FR-024 | US-6 | B-28, B-29 | T30 |
| FR-025 | US-5, US-7 | B-31, B-33 | T24, T32 |
| FR-026 | US-5, US-7 | (route-level) | T33 |
| FR-027 | US-2, US-5, US-7 | B-14 (SMTP leg) | T21, T22 |
| FR-028 | US-3, US-4 | B-12, B-21, B-36 | T11, T12, T35 |
| FR-029 | US-4, US-7 | B-19, B-24, B-30 | T9, T23, T28 |
| FR-030 | US-4, US-7 | B-24 | T28 |
| FR-031 | US-2, US-5 | B-10 | T3, T8 |
| FR-032 | US-7 | B-30, B-33, B-34 | T23, T26, T27 |
| FR-033 | US-6, US-3 | B-15, B-18 | T30, T36 |

Every FR-xxx appears above; every BDD scenario B-1..B-37 appears at least once; every scenario traces to a
US+AS pair in §6 and every US AS maps to ≥ 1 scenario (US-1: B-1..B-5; US-2: B-6..B-10; US-3: B-11..B-18;
US-4: B-19..B-24 + B-36; US-5: B-25, B-26; US-6: B-27..B-29; US-7: B-30..B-35). FR-013 and FR-026 have no
user-visible BDD scenario (policy resolution and route throttling are not observable in the UI) — their
assertion is behavioral at the test level (T34, T33), recorded here to keep the matrix honest.

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
  (rendered multipart + `X-Omnipus-Markdown` source header) — round-1 MAJ-006.
- **A8 — Mail operation budget**: one IMAP session per REST request (all STATUS/LIST/SEARCH on that
  connection, round-1 MAJ-012); the gateway caps concurrent mail operations at 2 per mailbox; the watcher
  cycles at 60 s per mailbox (today's cadence, carried forward from the drainer D16 records) with a single
  in-flight cycle per mailbox; the panel refetches every 30 s while open (D25). **Constants are
  implementation-tunable; the D22 failure-triage dispatch may revise them** — the requirement is the bound
  and the visibility, not the number.
- **A9 — Badge placement**: while the panel is closed, the unread badge renders on the Mail entry of the
  workspace tab strip; while open, the panel's folder list and the badge agree (D20's "Mail tab unread
  badge" concretely).
- **A10 — Rate-limit constant**: 10 mutating-mail requests/minute/IP (MC-20) — chosen to be far above any
  human clicking pattern and far below spam-cannon territory; tunable without spec change.
- **A11 — Peek enforcement is turn-scoped, not model-visible**: in a watcher-triggered turn the read path
  itself operates in BODY.PEEK/EXAMINE mode regardless of tool arguments — the model cannot opt out of
  peek-ness, and no new tool or policy surface is added (FR-020/FR-024).

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
| 16 | Two panels/tabs open on the same mailbox | Both read-only-identical; each refresh runs its own bounded session within the A8 cap; flag writes remain last-wins (edge 10) |
| 17 | `create_email_draft` with no derivable public origin | Tool succeeds; `chat_link: null` + reason; the chat's "Open draft" action still navigates in-app (FR-015) |

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
- **H-8 (edge)**: Turn "Let the agent handle new mail" on, receive a real message, watch the agent turn start
  in the workspace chat and read it — then confirm in the founder's own client that the message is still
  unread (D20).

## 14. Founder questions

**All seven round-1 questions are resolved and no genuinely new blocking point surfaced.** Mapping: Q1 → D12
(+ D23, D24 for edit scope and foreign drafts); Q2 → D21 (transcripts keep email text; docs state it); Q3 →
D13 + D17 (sandboxed no-scripts frame; images blocked with "Load images"); Q4 → D26 (CC/BCC for humans and
agents); Q5 → D20 (the drainer question dissolved — unread is plain `\Seen`; the watcher never touches
flags); Q6 → D21 (folder-scoped `uid:`/`mid:` addressing per MAJ-005); Q7 → D11 (Library-style docked panel
+ pop-out).

Points that could have become questions are recorded as reviewer-challengeable assumptions instead, because
each is an obvious default carried from today's behavior or explicitly delegated by a decision:

- Watcher cadence and the mail-operation budget (A8): 60 s watcher / 30 s panel / 2-per-mailbox cap — today's
  cadence plus D25's answer; D22's pending diagnosis may revise the constants, not the requirement.
- Badge placement (A9): the Mail tab-strip entry — the direct reading of D20's "Mail tab unread badge".
- Peek enforcement shape (A11): turn-scoped and model-invisible — no new tool, no policy surface (keeps D19's
  fill list and the tool catalog unchanged).
- Outbound size bound (FR-031) and rate-limit constant (A10): 1 MiB and 10/min — stated, tunable.

The founder may override any of these in one line; none blocks implementation of the rest of the spec.

---

## 15. Implementation notes — backend (Phase 4+; implementation guidance, not new policy)

- **New file** for mail read/send gateway handlers (e.g. `pkg/gateway/rest_mail.go`) — `rest_mailbox.go`
  (803 lines) stays config-only plus the D19 policy fill; one file, one job. Same `loadWorkspace`-idiom 404s
  and `ErrorResponse`.
- **`pkg/email`** gains: a compose/render unit (goldmark render → bluemonday sanitize → `go-message`
  multipart builder → signature on both parts; Message-ID generation per FR-012), a Sent-APPEND after SMTP
  success inside the send path, an APPEND primitive (`\Draft` flag, `X-Omnipus-Draft`/`X-Omnipus-Markdown`
  headers), folder listing/counts with special-use resolution, UIDPLUS probing, and folder-scoped
  `uid:`/`mid:` resolution. The `Transport` interface grows accordingly; the in-memory test fake grows with
  it. The connectionless dial-per-operation pattern (`Client.dialIMAP`) is preserved for IMAP; SMTP is
  context-threaded per FR-027.
- **Folder resolution** (A1) lives in `pkg/email`; per-mailbox overrides ride `MailboxConfig`.
- **Signature plumbing**: `EmailMailboxPanel` PUT carries `signature_html` through the existing raw-map write
  path — which must keep preserving unknown fields (ADR-033 §5 negative note). Sanitization happens
  server-side on save (FR-003).
- **`create_email_draft`** joins `tools.EmailToolset` (`pkg/tools/email.go`) and the six §2.7 touch points.
- **D19 fill**: the replacement for `grantEmailToolAllows` writes `ask`/`allow` only into absent keys of the
  agent's own policy map (`rest_mailbox.go`); the fill set is `emailToolNames` + `create_email_draft`.
- **HTML preview token** endpoints mirror the Library pair (`/library/preview-token`) including token expiry,
  bound to (pair, folder, ref, load_remote); the handler sets the MC-10 headers; `cid:` parts serve under the
  same token scope. The served HTML is bounded by the existing 256 KB inbound cap.
- **Audit + rate limit**: the four mutating routes wrap `withRateLimit` (`rest_auth.go::apiRateLimiter`) and
  emit via `pkg/audit` (hash via `argshash.go`), following `rest_preview_audit.go`.
- **Watcher**: `pkg/email/watcher.go::Watcher` (poll logic: UID search → state-file advance → badge state;
  optional agent-turn enqueue when the D20 switch is on) + `pkg/heartbeat/mail_watch.go::MailWatchService`
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
| Compose dialog | shadcn `Dialog` + the validation patterns of `src/components/connectors/EmailMailboxPanel.tsx` (`FieldRow`, `validate`) | Field-level errors; Markdown-labeled body (A3); To/CC/BCC fields (D26) |
| Signature editor | Section inside `src/components/connectors/EmailMailboxPanel.tsx::EmailMailboxPanel` | Textarea + live preview rendered in a sandboxed mini-frame with the MC-10 posture; 16,384-char bound enforced client-side too (MC-1). Advanced fields for the folder overrides (MIN-001) and the "Let the agent handle new mail" switch (D20) |
| Chat link → panel + "Open draft" | `src/components/chat/markdown-shared.tsx::createLinkRenderer` + the tool-result renderer | Same-origin mail deep links navigate in-place via **hash-route pattern match** (not origin equality) instead of `target="_blank"`; `isSafeHref` gating unchanged; deterministic "Open draft" action from the `create_email_draft` result (FR-015, OBS-003) |
| Data fetching | TanStack Query over generated types (`src/lib/api/generated/`) | Generated types only (shared rule 4); `refetchInterval: 30000` while the panel is mounted, `refetchIntervalInBackground: false` (D25, B-16) |
| Picker | `GET /api/v1/mailboxes` | Filtered client-side to the current workspace, `enabled && configured`; selection in `sessionStorage` per workspace (FR-010) |

## 17. Reachability (Definition of Done)

- **Operator**: Connectors screen → mailbox panel shows the Signature section, the folder-override advanced
  fields and the D20 switch (US-1); workspace tab strip shows **Mail** and opens the docked panel (US-3);
  compose action reachable from the panel header (US-5).
- **Human via chat**: a posted draft link — or the tool result's "Open draft" action — opens the preview
  panel in the same tab (US-4), exercised in the UAT lane with a real mailbox.
- **Agent**: `create_email_draft` is registered for every agent via
  `pkg/agent/email_tools.go::registerEmailToolsForAgent` (so it appears on the per-agent permissions screen),
  wired at all six §2.7 touch points, and behaviorally asserted for both agent kinds (T34 / SC-007).
- **Mail panel reachable from two surfaces**: the workspace tab strip entry (docked panel, D11) and chat
  links ("Open draft" / `chat_link`) — both exercised by T37/T42 and the UAT lane.
- **Delivery statement (two lines, never merged)**: *code correct and tested* — unit + gateway-integration
  suites green, contract verification green; *reachable by a user and an agent* — tab entry, docked panel,
  signature editor, compose, draft link, panel actions, and the agent tool all invocable as listed above,
  confirmed by the UAT lane on a real mailbox.

## 18. Assembly summary (plan-spec Phase 6)

| Measure | Count |
|---|---|
| User stories | 7 (US-1 … US-7) |
| BDD scenarios | 37 — Happy Path 18 · Alternate Path 5 · Error Path 7 · Edge Case 7 |
| Test datasets | 8 (DS-1 … DS-8) |
| TDD plan rows | 44 |
| Functional requirements | 33 (FR-001 … FR-033) |
| Success criteria | 7 (SC-001 … SC-007) |
| Founder questions open | 0 (all resolved by D11–D26; §14) |
| Round-1 findings dispositioned | 32 of 32 (§20) |

## 19. Related issues (D14, D21)

| Issue | Relationship | Note |
|---|---|---|
| [#629](https://github.com/elicify-ai/omnipus/issues/629) — email send can hang forever | **Closed by this work** (D21) | FR-027 absorbs it: context-threaded, bounded SMTP + Sent APPEND; MC-21/MC-8 tests. The spec touches `Client.Send` anyway (Sent APPEND), so the fix ships with it |
| [#631](https://github.com/elicify-ai/omnipus/issues/631) — delete the mailbox drainer | **Closed by this work** (D20, D21) | Drainer files, wiring and suites deleted; the new-mail watcher (never flags, never tasks) replaces it; the D20 switch is the opt-in agent handling |
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
| MAJ-007 — HTML-preview headers under-specified | **fixed** | MC-10 carries the full directive set, sandbox flags, Referrer-Policy, token binding/TTL, `cid:` part route; security-lead pre-implementation gate in FR-019; T41 + per-directive handler tests |
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
