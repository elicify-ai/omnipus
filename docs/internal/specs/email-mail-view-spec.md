# Feature Specification: Email — HTML signatures, workspace Mail tab, agent draft approval

**Created**: 2026-09-25
**Status**: Draft
**Input**: `docs/internal/specs/spec-email-mail-view.md` (interview-me output, Decisions Log D1–D10, open points O1–O6)
**Work branch**: `feat/email-mail-view` · Integration branch: `release/v0.1.1` (D1 — ships in v0.1.1)
**Canonical mailbox model**: ADR-033 — *Per-(Agent, Workspace) Email Mailboxes* (unchanged; D10 — no new ADR)

---

## 1. Summary and scope

The founder asked for three things (interview, "Request" section, verbatim): a configurable **HTML signature**
per mailbox, a **workspace Mail tab** ("mini outlook view", live from the mail server — nothing stored locally)
reusing the Library's look and components, and an **agent draft-approval flow**: the agent writes a draft into
the mailbox's Drafts folder and posts a link in chat that opens the draft in a preview side panel.

In scope (all on the existing ADR-033 pair model):

- One HTML signature **per (agent, workspace) mailbox**, edited in the existing mailbox panel with a live
  preview; a plain-text part is derived automatically (D2).
- Agents write **Markdown**; the backend renders sanitized HTML + plain text (multipart/alternative) and appends
  the signature. Agents never touch raw HTML or the signature itself (D3).
- Every sent message is APPENDed to the mailbox's Sent folder (D7) so it appears in the Mail tab and in the
  owner's own mail client.
- A **Mail tab inside the workspace** (D4) showing exactly **Inbox, Sent, Drafts** (D5), read **live over IMAP**
  with **no local copy of mail content** (D6); only reference metadata (Message-ID ↔ Board task / chat session)
  may be stored.
- New agent tool `create_email_draft` (APPEND with `\Draft`) plus a **chat link** that opens the draft in a
  preview side panel (D8). `send_email`'s existing policy-based approval is unchanged (D8).
- Humans can **compose and send mail manually** from the Mail tab, signature applied (D9).

Out of scope for this spec:

- Any mailbox-model change (ADR-033 stands; cap, pairing, credentials, move semantics all unchanged).
- Attachments (sending or receiving them in the Mail tab) — a later wave; the drainer's attachment-only
  handling stays as is (`pkg/email/drainer.go::mailToTask` behavior preserved).
- Server-side search UI in the Mail tab (the agent's `search_email` tool is unchanged).
- Push notification of new mail to the SPA (WebSocket frames) — the Mail tab polls on TanStack Query's normal
  refetch intervals (assumption A2).
- Any change to the heartbeat drainer's task-creation behavior (O5 touches only *display* of read state).

---

## 2. Contracts (wire-first — Hard Constraint #8)

Every byte below crosses the gateway/SPA boundary and is added to `contracts/` **before** any Go/TS code,
following the 5-step procedure (`CLAUDE.md`, "Contract regeneration"). Nothing in this section is written
until the schemas exist in `contracts/components/schemas/` and are referenced from `contracts/openapi.yaml`.

### 2.1 Changed schemas

| Schema | Change | Reason |
|---|---|---|
| `contracts/components/schemas/Mailbox.yaml` | New optional property `signature_html` (string, ≤ 16,384 chars, default `""`) | D2 — the signature is part of the mailbox account (ADR-033 pair) |
| `contracts/components/schemas/MailboxConfigureRequest.yaml` | New optional property `signature_html` (same bound) | Edit path for D2 |
| `contracts/components/schemas/Mailbox.yaml` | New optional properties `sent_folder_name`, `drafts_folder_name` (string, default `""`) | Assumption A1 — server folder-name overrides for non-standard IMAP layouts |

`signature_html` is **configuration, not mail content** — storing it in `config.json` does not violate D6
(D6 scopes "mail content"; the signature is the user's own setting). The plain-text signature part is derived
at compose time, never stored (D2).

### 2.2 New schemas (all in `contracts/components/schemas/`)

| Schema | Purpose | Key fields |
|---|---|---|
| `MailFolder` | One of the three D5 folders for one mailbox | `slug` (enum: `inbox` \| `sent` \| `drafts`), `display_name`, `unread_count`, `total` |
| `MailFolderList` | Folders for one mailbox | `folders: []MailFolder`, `mailbox: Mailbox` |
| `MailMessageSummary` | Envelope row (list results) | `message_id` (string, nullable — some inbound mail lacks one), `uid`, `subject`, `from`, `from_name`, `to`, `date` (RFC 3339), `seen`, `is_draft` |
| `MailMessagePage` | One page of envelopes | `messages: []MailMessageSummary`, `truncated`, `next_before_uid` (mirrors `SearchResult`'s explicit-truncation contract, `pkg/email/transport.go::SearchResult`) |
| `MailMessage` | Full message (read path) | summary fields + `body_text` (decoded plain text), `has_html` (bool), `folders` hint `folder` |
| `MailSendRequest` | Human manual send (D9) | `to`, `subject`, `body_markdown`, optional `in_reply_to`; `cc`/`bcc` **pending Q4** |
| `MailSendResponse` | Send outcome | `sent`, `message_id`, `sent_saved` (bool), `save_warning` (string, when the Sent APPEND failed after a successful SMTP send — D7 never silently drops) |
| `MailHtmlPreviewTokenRequest` | Mint a short-lived token for one message's HTML body | `workspace_id`, `agent_id`, `message_id`, `load_remote` (bool — **pending Q3**) |
| `MailHtmlPreviewTokenResponse` | Token payload | `token`, `expires_in_seconds` (mirrors Library `LibraryPreviewTokenResponse`) |
| `ErrorResponse` | Reused for all 4xx/5xx bodies (existing schema) | — |

Not built: no `MailDraftCreateRequest` REST schema — humans do not create drafts in v1 (compose offers Send;
"save as draft" for humans is out of scope). Agent draft creation is a **tool**, not a REST endpoint (§2.4).

### 2.3 New REST endpoints (all session-authenticated, versioned under `/api/v1`)

Path convention follows the existing workspace-scoped pattern (`/workspaces/{id}/media`,
`contracts/openapi.yaml` line 8027): `{id}` = workspace, `{agentId}` = the mailbox-owning agent — the
ADR-033 pair rides in the path exactly as `GET /agents/{id}/mailboxes/{workspaceId}` does, mirrored.

| Endpoint | Purpose | Status codes |
|---|---|---|
| `GET /workspaces/{id}/mail/{agentId}/folders` | Three D5 folders + counts (live IMAP) | 200 / 401 / 404 (workspace, agent, or no mailbox for the pair — `getAgentMailbox` precedent) / 502 (mail server unreachable) / 500 |
| `GET /workspaces/{id}/mail/{agentId}/folders/{folder}/messages?limit=&before_uid=&unseen_only=` | Envelope page | 200 / 401 / 400 (unknown folder slug, limit out of range) / 404 / 502 / 500 |
| `GET /workspaces/{id}/mail/{agentId}/messages/{messageId}` | Full message, addressed by RFC 5322 Message-ID (percent-encoded, angle brackets included) — **pending Q6** | 200 / 401 / 404 (not found; includes "pair has no mailbox") / 502 / 500 |
| `POST /workspaces/{id}/mail/{agentId}/messages` | Human manual send (D9): renders Markdown → multipart/alternative, signature, SMTP, Sent APPEND | 200 (`MailSendResponse`) / 400 (validation) / 401 / 404 / 502 (SMTP/IMAP upstream) / 500 |
| `POST /workspaces/{id}/mail/{agentId}/drafts/{messageId}/send` | Send an existing draft from the panel — **pending Q1** | 200 / 404 (draft gone) / 502 / 500 |
| `DELETE /workspaces/{id}/mail/{agentId}/drafts/{messageId}` | Discard a draft — **pending Q1** | 204 / 404 / 502 / 500 |
| `POST /mail/html-preview-token` | Mint HTML-body preview token (**pending Q3**) | 200 / 400 / 401 / 404 |
| `GET /mail/html-preview/{token}` | Serve the sanitized/sandboxed HTML body with CSP headers (**pending Q3**) | 200 / 404 (expired/unknown token) |

Notes binding the whole table:

- **Message-ID addressing (Q6 recommendation).** IMAP UIDs are only stable while UIDVALIDITY is unchanged;
  Message-IDs are stable per message. The read path resolves Message-ID → UID at request time (IMAP
  `SEARCH HEADER Message-ID`), so a chat link survives re-numbering. Messages without a Message-ID are listed
  with `message_id: null` and cannot be deep-linked (accepted edge, see §12 Edge cases).
- **502 for upstream mail failures.** The mail server is a third party; a timeout or auth failure there is not
  a client error and not a gateway bug. The body names the upstream cause (`ErrorResponse`).
- **Read-only vs mutating.** Reading folders/messages performs IMAP reads only; nothing is marked \Seen by the
  Mail tab's read path unless Q5's answer changes that (§14, Q5).

### 2.4 Tool-surface changes (registered via `pkg/agent/email_tools.go::registerEmailToolsForAgent`)

| Tool | Change |
|---|---|
| `send_email` | `body` becomes **Markdown** (D3). Description updated: no longer "Plain-text message body" (`pkg/tools/email.go::SendEmailTool.Parameters`). Rendering, signature and Sent APPEND happen server-side. Recipients stay exactly one — **unless Q4 adds CC/BCC**, in which case the parameter schema changes in the same contract commit |
| `reply` | Same body-semantics change as `send_email` (D3) |
| `create_email_draft` | **New.** Params: `to` (one address), `subject`, `body` (Markdown), optional `in_reply_to`. APPENDs to the mailbox's Drafts folder with `\Draft` (D8). Never sends. Generates the Message-ID itself (drafts must be addressable before any server assigns one — §2.3 note). Returns `{"created":true,"message_id":"...","uid":123,"chat_link":"https://…"}`; `chat_link` is built from `gateway.public_url` exactly as `serve_web` builds `/preview/` URLs (`pkg/tools/web_serve_test.go::TestServeWebPublicURL` precedent) |

Policy wiring (Hard Constraint #6): `create_email_draft` gets a shipped ceiling default (`allow` — it writes
nothing but a draft; it cannot send) in `pkg/config/defaults.go::defaultToolPolicyCeiling` next to the five
existing email tools (`defaults.go` lines 299–303), and joins the enable-time fill-in set
(`grantEmailToolAllows`, ADR-033 §2 "Tool policy") so an enabled mailbox makes it usable exactly like
`read_inbox`. `config.ReconcileToolPolicyCeiling` self-heals the entry onto existing installs.

### 2.5 Config keys

`MailboxConfig` (`pkg/config/config.go::MailboxConfig`) gains `SignatureHTML string
json:"signature_html,omitempty"` plus the two folder-name overrides from §2.1. Legacy configs load unchanged
(absent fields default empty = "no signature", "auto-resolve folders"). No new top-level config keys; no env
vars.

### 2.6 Contract regeneration

One atomic commit carries: the schema files, the `openapi.yaml` references, regenerated `pkg/api/generated/`
and `src/lib/api/generated/` artifacts, per the 5-step procedure. `make verify-contracts` must be green before
any handler or UI consuming these types is reviewed.

---

## 3. Existing Codebase Context

> GitNexus MCP tools were **not connected in the writing session** and this worktree has no `.gitnexus/`
> index — per `omnipus-shared-rules` rule 9 the analysis below is a first-hand Read/Grep exploration, and the
> impact rows are labelled **Inferred**. Every symbol was read in the working tree on 2026-09-25.

### 3.1 Symbols involved

| Symbol | Role | Verified context |
|---|---|---|
| `pkg/config/config.go::MailboxesConfig` / `::MailboxConfig` | extends | The ADR-033 pair map `map[agentID]map[workspaceID]MailboxConfig`; `MailboxConfig` today has Enabled/WorkspaceID/IMAP*/SMTP*/Username/PasswordRef — no signature field |
| `contracts/components/schemas/Mailbox.yaml`, `MailboxConfigureRequest.yaml` | extends | Wire pair-config contract; `configured` flag never returns the password |
| `pkg/gateway/rest_mailbox.go::getAgentMailbox` (+ set/delete/list) | extends | Pair-addressed config handlers; 404 when agent or pair missing; credential key `mailboxCredKey` |
| `pkg/email/transport.go::Transport` | extends | Interface: `ReadInbox / Search / ReadMessage / Send / MarkSeen` — **no folder parameter, no APPEND**; `Client.dialIMAP` always SELECTs INBOX |
| `pkg/email/transport.go::buildEmailBody` | modifies | Builds `text/plain`-only RFC 5322 messages; bare `From`; header injection guard `sanitizeHeader` |
| `pkg/email/transport.go::Client.Send` | modifies | SMTP only (STARTTLS 587 / SMTPS 465); never APPENDs to Sent |
| `pkg/email/transport.go::decodeBody` / `::htmlToText` | calls | Inbound MIME decode prefers text/plain, strips HTML→text for the agent, 256 KB cap, loud degrade markers |
| `pkg/tools/email.go::EmailTransports.resolve` | calls | Structural workspace resolution — no model-supplied mailbox/workspace parameter (ADR-033) |
| `pkg/tools/email.go::SendEmailTool` / `::ReplyTool` | modifies | One-recipient, plain-text bodies today; `read_message` marks `\Seen` |
| `pkg/agent/email_tools.go::registerEmailToolsForAgent` | extends | Registers all five email tools for **every** agent unconditionally; per-pair transports built from env-resolved passwords |
| `pkg/email/drainer.go::Drainer` | regression-protected | Unseen INBOX mail → Board tasks (≤25/mailbox/tick), then `MarkSeen`; task prompt embeds the UID |
| `pkg/config/defaults.go::defaultToolPolicyCeiling` | extends | Shipped ceiling entries for the five email tools (allow); `config.ReconcileToolPolicyCeiling` self-heals new entries |
| `src/components/workspaces/WorkspaceTabBar.tsx::WORKSPACE_TABS` | extends | Workspace tab strip (`chat`, `board`, `calendar`, `media`, `team`); adding `mail` is one array entry plus route wiring |
| `src/routes/_app/workspaces.$workspaceId.media.tsx` | pattern | The Library "tab" is a **redirect stub**: opens the docked Library panel and navigates back to Chat |
| `src/components/library/LibraryPanel.tsx` | pattern | Docked `<aside>` in `AppShell.tsx` (next to `BrowserLivePanel`); state in `src/store/ui.ts::libraryPanel` (`openLibraryPanel`/`closeLibraryPanel`) |
| `src/components/library/LibraryExplorer.tsx` + `LibraryPreviewPane.tsx` | pattern | List + "PREVIEW/EDIT PANE PLACEHOLDER" slot; `LibraryPreviewPane` dispatches per kind via an exhaustive switch |
| `src/components/library/LibraryPreviewPane.tsx::LibraryHtmlFrame` | pattern | Sandboxed iframe (`sandbox` attribute + gateway CSP `sandbox` directive) over `/library/preview-token/{token}` — the isolation precedent for mail HTML (Q3) |
| `src/components/chat/markdown-shared.tsx::createLinkRenderer` | extends | Single shared link renderer for live + historical chat markdown; `src/lib/url-safe.ts::isSafeHref` allow-lists **http/https/mailto/tel only** — a mail deep link must be an absolute http(s) URL |
| `src/components/connectors/EmailMailboxPanel.tsx::EmailMailboxPanel` | extends | The mailbox config dialog (mounted from `src/components/screens/ConnectorsScreen.tsx`), form validation + move semantics — hosts the D2 signature editor |
| `pkg/tools/web_serve` (public_url handling, `TestServeWebPublicURL`) | calls | Precedent for building agent-visible absolute URLs from `gateway.public_url` — reused for the draft `chat_link` (D8) |

### 3.2 Impact assessment (Inferred — no GitNexus in this worktree)

| Symbol modified | Risk | d=1 dependents | d=2 dependents |
|---|---|---|---|
| `pkg/email/transport.go::Transport` (new methods) | MEDIUM | `pkg/tools/email.go` (all five tools hold it), `pkg/email/drainer.go::Drainer`, test fakes in `pkg/tools` + `pkg/email` | `pkg/agent/email_tools.go` (wiring), gateway drain builder `pkg/gateway/rest_mailbox.go::buildMailboxes` |
| `pkg/email/transport.go::buildEmailBody` + `::Client.Send` | MEDIUM | `SendEmailTool`/`ReplyTool` result texts; every existing `pkg/email` send test | E2E mail flows (none today), drainer-independent |
| `pkg/config/config.go::MailboxConfig` | LOW | `rest_mailbox.go` raw-map write path (must preserve unknown fields — ADR-033 §5 negative note), `EmailMailboxPanel.tsx` form state | config migration tests (mixed-shape strictness must keep passing) |
| `src/components/workspaces/WorkspaceTabBar.tsx::WORKSPACE_TABS` | LOW | `TabSegment`/`SEGMENT_LABELS` are derived from the array (compile-enforced completeness) | Playwright tab tests at 1280px |
| `src/store/ui.ts::libraryPanel` | LOW | `LibraryPanel`, `ChatControls`, media redirect stub, `library.tsx` | sidebar entries |

No HIGH/CRITICAL blast radius found: `Transport` gains methods (implementations: one production `Client`,
test fakes — all in-tree, compile-breaks loudly); nothing else re-keys.

### 3.3 Verified O2 — do chat transcripts already store email bodies?

**Yes — verified first-hand.** The dispatch asked plan-spec to check this before writing up O2:

- `pkg/memory/jsonl.go::addMsg` marshals the full `providers.Message` into the per-session archive line
  (`ArchivedMessage{Message, TS}`) and fsyncs it; the path is `jsonlPath` = `{data-dir}/{sessionKey}.jsonl`.
- `pkg/providers/protocoltypes/types.go::Message` carries `ToolCalls []ToolCall` (JSON `tool_calls`), and
  `types.go::FunctionCall.Arguments` is a **serialized JSON string** (`json:"arguments"`) — the model's full
  argument text, i.e. the whole email body passed to `send_email` / `reply`, lands on disk.
- Tool results persist too: `read_message` returns the full decoded body (`pkg/tools/email.go::ReadMessageTool.Execute`),
  stored as the tool-role message of the same transcript.
- Transcript retention is append-only by design (`pkg/session/jsonl_backend.go::Save` is a no-op; the retention
  sweep is the sole deleter — `pkg/memory/CLAUDE.md`), and chat hides nothing at rest: hiding is render-only
  (`src/components/chat/CLAUDE.md`, "Tool-call visibility").

So **a local copy of sent/received mail content already exists** in `~/.omnipus` session transcripts, which is
in direct tension with a literal reading of D6 ("no local copy of mail content"). This is founder decision Q2 —
the spec does not guess.

### 3.4 Execution flows

| Flow | Relevance |
|---|---|
| Agent email tool call (`read_inbox` → `read_message` → `reply`) | Every new mail tool rides the same `EmailTransports.resolve` turn-context stamp; no new resolution machinery |
| Heartbeat drainer pass (`pkg/email/drainer.go::Drain`) | Untouched; the Mail tab is a **parallel reader** of the same mailbox (see Q5 for flag interaction) |
| Mailbox config save (`rest_mailbox.go::setAgentMailbox`) | Gains `signature_html` + folder-name overrides in the same raw-map round-trip that must preserve unknown fields |
| Library deep link → docked panel (`library.tsx` route + `ui` store) | The exact handoff pattern the mail deep link (D8 chat link) reuses |

### 3.5 Cluster placement

This feature spans two clusters — **email transport/tools** (`pkg/email`, `pkg/tools/email.go`,
`pkg/agent/email_tools.go`) and **SPA workspace surfaces** (Library-style panel + tab wiring, Connectors
dialog). Backend-only changes compile in one tree; the UI slice is confined to the workspace shell, chat
markdown link renderer, and the Connectors dialog — no new product area.

### 3.6 Available reference patterns

`docs/reference/go-implementation/` **does not exist in this repository** (checked 2026-09-25) — Phase 1.7 of
the skill is N/A. In-repo patterns used instead are cited inline throughout (Library panel trio, Library HTML
frame isolation, `web_serve` public-URL construction, `SearchResult` truncation contract).

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
that mailbox through any path (agent tool or manual), observe the signature on both the HTML and the plain-text
part; a second mailbox without a signature stays bare.

**Acceptance scenarios**:

1. **Given** a configured mailbox without a signature, **When** the operator opens the mailbox panel in
   Connectors and saves a signature (HTML) with the live preview showing the rendered result, **Then** the
   signature is stored with the mailbox pair and survives a gateway restart.
2. **Given** a mailbox with a saved signature, **When** any message is sent from that mailbox, **Then** the
   message carries the signature appended to both the HTML and the derived plain-text part.
3. **Given** a mailbox whose signature is edited or cleared, **When** the next message is sent, **Then** the
   new/empty signature applies (no stale copy is sent).

### US-2 — Markdown out: multipart/alternative + Sent folder (P0) — D3, D7

Agents write message bodies as Markdown; the backend renders a sanitized HTML part plus a plain-text part
(multipart/alternative), appends the mailbox signature, and saves the sent message into the mailbox's Sent
folder over IMAP APPEND — so the founder sees in their own mail client exactly what the agent sent, and the
Mail tab's Sent folder (US-3) shows it too. Agents never author raw HTML and never write the signature.

**Why this priority**: without rendering + Sent APPEND, the Mail tab's Sent folder (D5/D7) has nothing to show
and D3's safety promise ("no model-authored raw HTML") is unmet.

**Independent test**: call `send_email` (or `reply`) with a Markdown body against a test transport that records
the message; assert multipart/alternative structure, sanitized HTML, appended signature, and an APPEND of the
same content to the Sent folder.

**Acceptance scenarios**:

1. **Given** an agent whose effective policy allows `send_email`, **When** it calls `send_email` with a
   Markdown body, **Then** the recipient receives a `multipart/alternative` message whose HTML part is the
   sanitized render (no script content) and whose plain part is readable text, both ending with the mailbox
   signature.
2. **Given** a successful SMTP send, **When** the send completes, **Then** the exact sent message (headers +
   both parts) is APPENDed to the mailbox's Sent folder and visible in the Mail tab.
3. **Given** an SMTP success but a Sent APPEND failure, **When** the tool result is produced, **Then** the
   result explicitly warns that the sent copy could not be saved — it never reports a silent partial success.

### US-3 — Workspace Mail tab: live Inbox / Sent / Drafts (P0) — D4, D5, D6

The operator wants a "mini outlook" inside the workspace: a Mail surface that looks and behaves like the
Library (D4), showing exactly Inbox, Sent, Drafts (D5), reading the mailbox **live over IMAP** — no local copy
of mail content (D6). A workspace may hold several mailboxes (one per agent); the surface scopes to one mailbox
at a time with a picker.

**Why this priority**: this is the visibility half of the founder's ask; it also carries the draft preview
surface that US-4's approval flow opens.

**Independent test**: with one configured mailbox, open the workspace Mail tab, browse all three folders, open
a message; verify envelope fields and body against the real mailbox, and verify (by inspecting the data
directory) that no message bodies were written locally.

**Acceptance scenarios**:

1. **Given** a workspace whose agent owns an enabled mailbox, **When** the operator opens the workspace Mail
   tab, **Then** the Inbox, Sent and Drafts folders are listed with unread/total counts fetched live from the
   mail server.
2. **Given** the Mail tab open on Inbox, **When** the operator clicks a message, **Then** the full message
   (headers + body) is fetched live and displayed; the URL/address does not depend on data stored locally.
3. **Given** a workspace with more than one mailbox, **When** the operator opens the Mail tab, **Then** a
   mailbox picker is offered and the selection is remembered for the session.
4. **Given** the mail server is unreachable, **When** the Mail tab loads folders or messages, **Then** an
   explicit error state names the upstream failure with a retry affordance — never a silent empty list.
5. **Given** any Mail tab usage, **When** the data directory is inspected afterwards, **Then** no new file
   contains message bodies (D6; reference metadata aside).

### US-4 — Agent drafts with chat-link approval (P0) — D8

The founder wants agents to *propose* mail, not just send it: the agent composes a draft via the normal
provider mechanism (IMAP APPEND with `\Draft`), then posts a link in chat; clicking it opens the draft in a
preview side panel where the human can see exactly what would go out. `send_email`'s existing policy approval
is unchanged (D8).

**Why this priority**: "definitely a way to approve an email draft" is the strongest sentence in the founder's
request; the draft tool + link + preview panel is the core of it.

**Independent test**: as an agent with a mailbox, call `create_email_draft`; assert the draft exists in the
mailbox's Drafts folder (with `\Draft`), the result carries a `chat_link`, and opening that link in the SPA
shows the draft's content in the preview side panel.

**Acceptance scenarios**:

1. **Given** an agent with an enabled mailbox, **When** it calls `create_email_draft` with to/subject/body,
   **Then** a message APPENDed with the `\Draft` flag exists in the Drafts folder and the tool result includes
   its `message_id` and `chat_link`.
2. **Given** the agent posted the chat link, **When** the human clicks it, **Then** the app navigates to the
   workspace and opens the draft in the preview side panel — same tab, not a browser popup.
3. **Given** a draft link for a draft that was already deleted from the Drafts folder, **When** the human
   clicks the link, **Then** the panel shows an explicit "draft no longer exists" state (Message-ID lookup
   miss), not a blank pane.
4. **Given** the agent never calls `send_email`, **When** it only creates drafts, **Then** no outbound message
   is sent and no approval prompt appears — draft creation alone is not a send.

### US-5 — Manual send from the Mail tab (P1) — D9

Humans can compose and send mail directly from the Mail tab — same mailbox, same rendering pipeline, signature
applied — so the operator can answer mail without leaving Omnipus.

**Why this priority**: the founder's "maybe" ("possible also to send emails manually maybe") — wanted, but
weighted below the agent flows it reuses.

**Independent test**: open the Mail tab compose action, fill to/subject/body, send; assert `MailSendResponse`
reports success + Sent APPEND, and the recipient-side content matches the agent pipeline (multipart,
signature).

**Acceptance scenarios**:

1. **Given** the Mail tab open on a mailbox, **When** the human opens compose, enters a recipient, subject and
   Markdown body, and sends, **Then** the message goes out through the same multipart + signature pipeline as
   agent mail and the compose dialog closes with a confirmation.
2. **Given** the compose dialog with an empty recipient or body, **When** the human clicks send, **Then**
   validation blocks the send with field-level errors and nothing is transmitted.

### US-6 — Read state that makes sense alongside the drainer (P1) — O5 → pending Q5

The heartbeat drainer marks mail `\Seen` the moment it converts it into a Board task
(`pkg/email/drainer.go::drainMailbox`), and `read_message` marks `\Seen` when the agent reads. The Mail tab
needs an honest notion of "unread" / "handled by the agent" that doesn't lie to the human. **The exact
semantics are founder decision Q5** — this story is written for the recommended option (a per-mailbox IMAP
keyword marking agent-handled messages) and is re-keyed by the answer.

**Why this priority**: display correctness, not capability — the tab works without it, but a founder whose
Inbox always shows zero unread will distrust it.

**Independent test**: with one unseen message, run the drainer (or `read_message`), then open the Mail tab;
assert the message shows the "handled by agent" treatment chosen by Q5 and the Inbox unread count reflects it.

**Acceptance scenarios** (per the recommended option; re-keyed by Q5):

1. **Given** a message turned into a Board task by the drainer, **When** the Mail tab lists the Inbox,
   **Then** the message is visibly distinguished as handled-by-agent from a message no agent has seen.
2. **Given** a message only a human has read (Mail tab open), **When** the list refreshes, **Then** it is not
   shown as unread (normal `\Seen` semantics apply to the human path).

### US-7 — Draft actions in the preview panel (P2) — O1 → pending Q1

Beyond viewing a draft (US-4), the panel may offer edit, send and discard. Whether Send-from-panel counts as
the human approval, and whether editing is in scope at all, is founder decision Q1. This story exists so the
answer has a home; it is **not committed** until Q1 is answered.

**Acceptance scenarios**: deferred to Q1's answer (§14, Q1). The spec's contract rows for
`POST …/drafts/{messageId}/send` and `DELETE …/drafts/{messageId}` (§2.3) stay pending until then.

---

## 5. Behavioral contract (quick reference)

### 5.1 When/Then summary

- When a mailbox has a saved signature, any message sent from it (agent tool, draft sent, manual) carries the
  signature on both MIME parts.
- When a mailbox has no signature, outgoing messages are unchanged from today's shape except for D3's
  multipart rendering.
- When an agent calls `send_email` or `reply`, the body is Markdown and the sent message is
  multipart/alternative with sanitized HTML.
- When an agent calls `create_email_draft`, a `\Draft`-flagged message appears in Drafts and no mail is sent.
- When a human clicks a draft chat link, the draft opens in the preview side panel in the same tab.
- When the Mail tab lists a folder, the data is fetched live from the mail server and nothing is persisted
  except reference metadata.
- When the mail server is unreachable, every mail-backed surface shows an explicit error with retry — never a
  silent empty state.
- When a sent message's Sent APPEND fails after SMTP success, the caller sees an explicit warning.
- When a chat link targets a Message-ID that no longer resolves, the panel shows a not-found state.

### 5.2 Explicit non-behaviors

- The system must not give agents a raw-HTML authoring path, because D3 promises model-authored HTML never
  reaches recipients (Markdown + server-side sanitize only).
- The system must not store message bodies locally (beyond the existing session transcripts — Q2), because D6
  promises the mailbox stays the single source of truth.
- The system must not let the Mail tab's read path mutate mailbox state other than what Q5 decides (e.g. no
  new \Seen marking on preview), because the drainer and the owner's mail client share those flags.
- The system must not remove or weaken `send_email`'s policy approval, because D8 explicitly keeps it.
- The system must not let a chat-posted draft link expose another agent's or workspace's mail: the link's
  (workspace, agent) pair is validated server-side against the session's authorization, mirroring the
  pair-addressed config endpoints (ADR-033).
- The system must not add IMAP folders, rename folders, or show folders outside Inbox/Sent/Drafts (D5).
- The system must not reintroduce a standalone preview route for mail content (Library CLAUDE.md rule: preview
  is framed inside its panel; `/preview/` is agent `web_serve` only).

### 5.3 Machine-verifiable constraints

| ID | Constraint | Test hook |
|---|---|---|
| MC-1 | `signature_html` longer than 16,384 chars → HTTP 400 `ErrorResponse` on `PUT /agents/{id}/mailboxes/{workspaceId}`; the panel blocks longer input | gateway handler unit test |
| MC-2 | Rendered HTML part contains no `<script>` element, no `on*=` handler attribute, and no `javascript:` href — verified on a body of `<script>alert(1)</script><img src=x onerror=alert(1)><a href="javascript:alert(1)">x</a>` | unit test on the render pipeline |
| MC-3 | Outbound messages with a signature are `multipart/alternative` with exactly `text/plain` + `text/html` parts; without, `text/plain` part content is byte-identical to the Markdown-derived text | unit test on the composer |
| MC-4 | `\r\n` or `\n` in a subject/recipient never reaches a header — existing `sanitizeHeader` behavior holds for the new composer too | unit test (header injection) |
| MC-5 | Folder slugs are exactly `inbox` \| `sent` \| `drafts`; any other value → HTTP 400 | contract + handler test |
| MC-6 | `limit` ≤ 0 → default 20; `limit` > 100 → clamped to 100 (mirrors `clampLimit`, `pkg/email/transport.go`) | unit test |
| MC-7 | Message not found by Message-ID in the addressed folder → HTTP 404 `ErrorResponse` | handler test |
| MC-8 | Mail server dial/command timeout (30s/45s upstream bounds) → HTTP 502 with the upstream cause in the error message | handler test with a black-hole/failing transport |
| MC-9 | SMTP success + Sent APPEND failure → `MailSendResponse.sent_saved=false` + `save_warning` non-empty (tool result carries the same warning) | unit test on the send path |
| MC-10 | The HTML preview response carries a CSP with `sandbox` (no `allow-scripts`) and, when `load_remote=false` (default), `img-src` restricted to `'self' data:`; only `load_remote=true` relaxes it to `https:` — **pending Q3** | gateway handler test |
| MC-11 | `create_email_draft` result JSON parses with `created=true`, a non-empty `message_id` matching the APPENDed message's Message-ID header, and `chat_link` starting with the configured `gateway.public_url` | unit test with fake transport |
| MC-12 | After exercising all Mail tab endpoints against a temp data dir, `find` over the data dir shows no file containing message body text (D6; transcripts excluded per Q2) | integration test assertion |
| MC-13 | Draft deep link opened for a Message-ID in a workspace the session cannot read → HTTP 404 (never a cross-pair leak) | handler test |
| MC-14 | Drafts listed with `is_draft=true`; APPEND to Drafts always sets the `\Draft` flag | transport fake assertion |
| MC-15 | The five existing email tools' descriptions change only in body-semantics text; parameter schemas change only per Q4 | contract review check |

### 5.4 Integration boundaries

| External system | Data in/out | Contract | Failure behavior |
|---|---|---|---|
| IMAP server (per mailbox) | Folder list/counts, envelope pages, full messages, APPEND (drafts, sent copies), flag stores | `emersion/go-imap/v2` over IMAPS (993) — existing `Client` patterns (`pkg/email/transport.go`) | Every UI surface maps dial/command timeouts to explicit error states (MC-8); the drainer's per-mailbox skip-with-WARN behavior is unchanged |
| SMTP server (per mailbox) | Outbound RFC 5322 message | STARTTLS 587 / SMTPS 465 — existing `sendSMTPWithSTARTTLS`/`sendSMTPS` | Send failure → tool error / compose dialog error, nothing APPENDed |
| SPA (Mail tab, panel, compose) | The §2.3 REST endpoints; the §2.2 schemas | Generated types only (`src/lib/api/generated/`) | TanStack Query error states; retry affordances |
| Chat (link → panel) | The `chat_link` URL scheme (Q6 recommendation: `…/#/workspaces/{wsId}/mail?mailbox={agentId}&folder=drafts&message={MessageID}`) | Absolute https URL built from `gateway.public_url` (passes `isSafeHref`) | Unknown/deleted target → panel not-found state (US-4 AS-3) |

Development uses the in-memory transport fake pattern that already exists for the five tools
(`pkg/tools/email.go` header note: "fully unit-testable against an in-memory fake") — no real IMAP/SMTP server
is a dependency of the test suite; live-mailbox acceptance belongs to the UAT campaign (§9.3).

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
- **Then** the outgoing message carries no signature content

#### Scenario B-4: Markdown renders to sanitized multipart with signature

**Traces to**: US-2, AS-1 · **Category**: Happy Path

- **Given** a mailbox with a signature, and an agent allowed to use `send_email`
- **When** the agent sends a Markdown body containing a heading, a link and a raw `<script>` tag
- **Then** the composed message is `multipart/alternative`
- **And** the HTML part renders heading + link and contains no `<script>` element (MC-2)
- **And** the plain-text part contains the readable text and the signature

#### Scenario B-5: Sent message is APPENDed to Sent

**Traces to**: US-2, AS-2 · **Category**: Happy Path

- **Given** a mailbox whose Sent folder resolves (A1)
- **When** a send completes successfully
- **Then** a message identical to the one transmitted exists in the Sent folder
- **And** it appears in the Mail tab's Sent listing on next fetch

#### Scenario B-6: Sent APPEND failure is surfaced, never silent

**Traces to**: US-2, AS-3 · **Category**: Error Path

- **Given** a mailbox whose Sent APPEND fails (transport fake configured to fail APPEND)
- **When** a send completes over SMTP
- **Then** the tool result / `MailSendResponse` carries `save_warning` (MC-9) and `sent_saved=false`
- **But** the recipient still receives the message (the SMTP success is not rolled back or hidden)

#### Scenario B-7: Folders listed live with counts

**Traces to**: US-3, AS-1 · **Category**: Happy Path

- **Given** a workspace whose agent owns an enabled mailbox with mail in Inbox and Sent
- **When** the Mail tab requests the folder list
- **Then** exactly `inbox`, `sent`, `drafts` return (D5) with `total` counts matching the server
- **And** the response is built from live IMAP data (fake transport records the fetch)

#### Scenario B-8: Open a message in the Mail tab

**Traces to**: US-3, AS-2 · **Category**: Happy Path

- **Given** a message in the Inbox with plain-text and HTML parts
- **When** the human opens it from the list
- **Then** the full envelope fields and decoded `body_text` render in the panel
- **And** `has_html` reports the HTML part's presence (**pending Q3** for how HTML renders)

#### Scenario B-9: Multiple mailboxes get a picker

**Traces to**: US-3, AS-3 · **Category**: Alternate Path

- **Given** a workspace where two agents each own a mailbox
- **When** the human opens the Mail tab
- **Then** a mailbox picker offers both (agent-named) mailboxes and the chosen one drives folder/message queries

#### Scenario B-10: Mail server unreachable shows explicit error

**Traces to**: US-3, AS-4 · **Category**: Error Path

- **Given** a mailbox whose IMAP host is unreachable
- **When** the Mail tab loads folders
- **Then** the surface shows an explicit error naming the upstream failure with a retry control (MC-8)
- **But** no empty-folder state is presented as if the mailbox were empty

#### Scenario B-11: No local mail store is created

**Traces to**: US-3, AS-5 · **Category**: Edge Case

- **Given** a fresh gateway with a configured mailbox
- **When** folders are listed, two messages read, and one manual send performed
- **Then** a sweep of the data directory finds no new file containing message body text (MC-12)

#### Scenario B-12: Draft created with \Draft flag and chat link

**Traces to**: US-4, AS-1 · **Category**: Happy Path

- **Given** an agent with an enabled mailbox
- **When** it calls `create_email_draft` with to, subject and Markdown body
- **Then** the Drafts folder contains the message with the `\Draft` flag (MC-14)
- **And** the tool result reports `created=true`, the draft's `message_id` and a `chat_link` (MC-11)

#### Scenario B-13: Chat link opens the draft preview panel in place

**Traces to**: US-4, AS-2 · **Category**: Happy Path

- **Given** the agent posted the `chat_link` in the workspace chat
- **When** the human clicks the link
- **Then** the SPA opens the draft in the preview side panel in the same tab
- **And** the panel shows the draft's to/subject/body as stored in Drafts

#### Scenario B-14: Deleted draft's link shows not-found

**Traces to**: US-4, AS-3 · **Category**: Error Path

- **Given** a draft whose chat link was posted, and the draft has since been removed from Drafts
- **When** the human clicks the link
- **Then** the panel shows an explicit "draft no longer exists" state
- **But** no other folder's or mailbox's mail is exposed (MC-13)

#### Scenario B-15: Draft creation alone sends nothing

**Traces to**: US-4, AS-4 · **Category**: Edge Case

- **Given** an agent that calls only `create_email_draft`
- **When** the call completes
- **Then** no SMTP session is opened and no approval prompt for `send_email` appears

#### Scenario B-16: Manual send through the same pipeline

**Traces to**: US-5, AS-1 · **Category**: Happy Path

- **Given** the Mail tab open with compose available
- **When** the human sends to/subject/Markdown body
- **Then** `MailSendResponse` reports `sent=true` with `message_id` and `sent_saved=true`
- **And** the transmitted message is multipart/alternative with the mailbox signature

#### Scenario B-17: Compose validation blocks empty sends

**Traces to**: US-5, AS-2 · **Category**: Error Path

- **Given** the compose dialog with an empty recipient
- **When** the human clicks send
- **Then** field-level validation errors render and no request is made

#### Scenario B-18: Drainer-handled mail is distinguishable (Q5-recommended semantics)

**Traces to**: US-6, AS-1 · **Category**: Happy Path — **pending Q5**

- **Given** an unseen message that the drainer has converted to a Board task
- **When** the Mail tab lists the Inbox
- **Then** the message renders with the handled-by-agent treatment distinct from unseen-human mail

#### Scenario B-19: Human-read mail is not unread (Q5-recommended semantics)

**Traces to**: US-6, AS-2 · **Category**: Alternate Path — **pending Q5**

- **Given** a message the human has opened in the Mail tab
- **When** the folder list refreshes
- **Then** the message no longer counts as unread

#### Scenario B-20: Signature too long is rejected

**Traces to**: US-1 (edge of AS-1) · **Category**: Edge Case

- **Given** signature input longer than 16,384 characters
- **When** the panel attempts to save
- **Then** the panel blocks it, and a direct PUT returns HTTP 400 (MC-1)

#### Scenario B-21: Unknown folder slug

**Traces to**: US-3 (edge of AS-1) · **Category**: Edge Case

- **Given** a request to `GET …/folders/archive/messages`
- **When** the gateway handles it
- **Then** it returns HTTP 400 naming the valid folder slugs (MC-5)

#### Scenario B-22: UIDVALIDITY change does not break the deep link

**Traces to**: US-4, AS-2 · **Category**: Edge Case — **pending Q6**

- **Given** a posted draft link, and the mailbox's UIDVALIDITY changed since (UIDs renumbered)
- **When** the human clicks the link
- **Then** the draft still resolves by Message-ID and renders
- **But** a link carrying only the stale UID would have missed

---

## 7. TDD plan (tests designed before implementation)

E2E note: Playwright E2E against a **live IMAP/SMTP server is not part of CI** — the suite has no mail
server. Logic coverage lives at unit + gateway-integration level (in-memory transport fakes, per the existing
`pkg/tools` fake pattern); live-mailbox behavior is accepted in the **UAT campaign** with real mailboxes, and
E2E is limited to surfaces that need no mail server (tab/panel reachability, signature editor open/save, empty
and error states with an unreachable mailbox).

| Order | Test name (indicative) | Level | Traces to BDD | Description |
|---|---|---|---|---|
| 1 | `TestComposeMultipart_SignatureAndSanitize` | Unit | B-4 | Markdown → multipart/alternative; sanitizer kills script/on*/javascript: (MC-2, MC-3); signature on both parts |
| 2 | `TestComposeMultipart_NoSignature` | Unit | B-3 | Empty signature → parts without signature content |
| 3 | `TestComposeMultipart_HeaderInjectionGuard` | Unit | (MC-4) | CRLF in subject/recipient never reaches headers |
| 4 | `TestClientSend_AppendsToSent` | Unit | B-5 | SMTP-success path APPENDs the exact message to the resolved Sent folder |
| 5 | `TestClientSend_AppendFailureSurfaces` | Unit | B-6 | APPEND failure → explicit warning, send not hidden (MC-9) |
| 6 | `TestClientAppendDraft_SetsDraftFlag` | Unit | B-12, B-15 | Draft APPEND sets `\Draft`; no SMTP session opened |
| 7 | `TestResolveSpecialUseFolders_OverridesWin` | Unit | B-7 | Special-use (RFC 6154) resolution with config-override precedence (A1) |
| 8 | `TestReadMessageByMessageID_FoundAndNotFound` | Unit | B-8, B-14 | SEARCH HEADER Message-ID resolution; miss → error (MC-7) |
| 9 | `TestSendEmailTool_MarkdownBodyPipeline` | Unit (fake) | B-4, B-5 | Tool-level: body treated as Markdown; result text unchanged in shape |
| 10 | `TestCreateEmailDraftTool_ResultContract` | Unit (fake) | B-12 | Result JSON contract incl. `chat_link` from public_url (MC-11) |
| 11 | `TestMailboxSignature_ConfigRoundTrip` | Unit | B-1 | `signature_html` persists via config load/save; legacy config loads unchanged (§2.5) |
| 12 | `TestPutAgentMailbox_SignatureLimit` | Integration (httptest) | B-20 | 16,384-char bound → 400 (MC-1) |
| 13 | `TestMailFoldersEndpoint_PairMissing` | Integration | B-7, B-14 | 404 when agent/pair missing (ADR-033 precedent); 400 unknown slug (MC-5) |
| 14 | `TestMailMessagesEndpoint_PagingBounds` | Integration | (MC-6) | limit clamp/before_uid pass-through; truncated flag mirrors `SearchResult` |
| 15 | `TestMailMessageEndpoint_UpstreamTimeout` | Integration | B-10 | Transport failing → 502 with cause (MC-8) |
| 16 | `TestMailSendEndpoint_MultipartAndSent` | Integration | B-16 | Manual send → same pipeline; `sent_saved` reporting |
| 17 | `TestDraftSendAndDiscard_Endpoints` | Integration | (US-7) | **pending Q1** — only written if Q1 approves panel actions |
| 18 | `TestMailHtmlPreview_CSPAndSandbox` | Integration | (MC-10) | **pending Q3** — sandbox without allow-scripts; img-src posture |
| 19 | `TestNoLocalMailStore` | Integration | B-11 | Data-dir sweep after exercise finds no bodies (MC-12) |
| 20 | `TestDeepLink_CrossPairDenied` | Integration | B-14 | Session without access to the pair → 404 (MC-13) |
| 21 | `MailTab.spec.ts` — tab opens, picker, error state | E2E (no mail server) | B-9, B-10 | Tab strip entry, mailbox picker, unreachable-mailbox error surface |
| 22 | `MailSignatureEditor.spec.ts` | E2E (no mail server) | B-1, B-20 | Signature editor in Connectors: preview, save, bound |
| 23 | `DraftDeepLink.spec.ts` | E2E (stubbed route state) | B-13, B-14 | Link → panel navigation; not-found state |
| 24 | UAT lane — live mailbox | UAT | B-1..B-19 | Real IMAP/SMTP: signature on received mail, Sent APPEND visible in the owner's client, draft link → panel, manual send |

### 7.1 Test datasets

| Dataset | Rows (boundary → edge → error → happy) | Traces to |
|---|---|---|
| DS-1 Signature values | `""` (no signature), 16,384 chars (max), 16,385 (reject), valid HTML, hostile HTML (script/on*/javascript:) | B-1, B-3, B-4, B-20 |
| DS-2 Markdown bodies | empty body (reject), heading+link+code block, raw HTML/script injection, very long body (>256 KB cap behavior), unicode/CRLF | B-4, (MC-2, MC-4) |
| DS-3 Folder paging | `limit=0` (default 20), `limit=1`, `limit=101` (clamp 100), `before_uid=1` (empty), `before_uid=<min>` (page boundary), empty folder | B-7, (MC-6) |
| DS-4 Message addressing | existing Message-ID, missing Message-ID (404), message without Message-ID header (listed `message_id: null`), percent-encoded angle brackets, UIDVALIDITY-changed mailbox | B-8, B-14, B-22 |
| DS-5 Send outcomes | SMTP ok + APPEND ok, SMTP ok + APPEND fail, SMTP fail (no APPEND attempted), invalid recipient (reject client-side) | B-5, B-6, B-16, B-17 |
| DS-6 Draft lifecycle | create ok, create with missing to (reject), draft deleted before link click, draft in unreadable pair (404) | B-12, B-14, B-15, B-13 |
| DS-7 Mailbox roster | 0 mailboxes (empty state), 1 mailbox (no picker), 2 mailboxes (picker), mailbox enabled but password unresolvable (skip + explicit state) | B-7, B-9, B-10 |

### 7.2 Regression impact

Existing behaviors that MUST be preserved (regression tests new, existing suites must stay green):

- The drainer's task-creation and `\Seen` behavior is untouched (`pkg/email/drainer.go::drainMailbox`) — its
  suite keeps passing unmodified.
- `read_inbox` / `search_email` / `read_message` semantics unchanged (folder=INBOX only, \Seen on read,
  envelope-only lists, pagination cursors) — `pkg/tools` email suites stay green; only description-text
  assertions may change (MC-15).
- One-recipient `send_email` parameter shape **pending Q4**; if unchanged, existing schema tests stand.
- Mailbox config round-trip including the strict legacy/nested shape rule
  (`pkg/config` mailbox migration tests) must keep passing with the new fields present.
- The SPA embed/CSP surface is untouched except the new `mail/html-preview` route (**pending Q3**), which
  carries its own headers and no new `script-src` relaxation — `pkg/gateway/embed.go` policy untouched.

---

## 8. Functional requirements

- **FR-001**: The system MUST store one optional HTML signature per (agent, workspace) mailbox, configurable
  through the existing mailbox panel and validated to ≤ 16,384 chars (MC-1). [D2, ADR-033]
- **FR-002**: The signature editor MUST show a live rendered preview of the signature as it is edited. [D2]
- **FR-003**: The system MUST NOT require or permit the agent to author the signature or raw HTML; signature
  application happens server-side at compose time (MC-2). [D3]
- **FR-004**: The system MUST render agent message bodies (`send_email`, `reply`, `create_email_draft`) from
  Markdown into sanitized `text/html` plus derived `text/plain` (multipart/alternative). [D3]
- **FR-005**: The system MUST append the mailbox signature to both parts of every outgoing message from that
  mailbox (agent send, reply, draft-when-sent, manual send). [D2, D3, D9]
- **FR-006**: The system MUST APPEND every successfully sent message to the mailbox's Sent folder and MUST
  surface an explicit warning when that APPEND fails after SMTP success (MC-9). [D7]
- **FR-007**: The system MUST provide a Mail surface inside the workspace, reached from the workspace tab
  strip, visually and behaviorally modeled on the Library (panel pattern — **pending Q7**). [D4]
- **FR-008**: The Mail surface MUST present exactly the folders Inbox, Sent, Drafts, addressed by stable slugs
  (`inbox`/`sent`/`drafts`), with server-folder resolution and per-mailbox overrides (A1, MC-5). [D5]
- **FR-009**: The Mail surface MUST read all message content live from the mail server at request time and
  MUST NOT persist message bodies (MC-12); reference metadata (Message-ID ↔ task/session) is permitted. [D6]
- **FR-010**: The Mail surface MUST offer a mailbox picker when a workspace has more than one mailbox
  (assumption A5). [D4]
- **FR-011**: New REST endpoints and schemas MUST match §2 exactly, contract-first per Hard Constraint #8. [D4–D9]
- **FR-012**: A new `create_email_draft` tool MUST APPEND a `\Draft`-flagged message to the mailbox's Drafts
  folder, generate its own Message-ID, never send, and return `message_id` + `chat_link` (MC-11, MC-14). [D8]
- **FR-013**: `create_email_draft` MUST carry a shipped ceiling policy default and join the mailbox-enable
  allow-fill set, so an enabled mailbox makes it usable like the read tools (Hard Constraint #6, ADR-033). [D8]
- **FR-014**: Clicking a draft chat link MUST open the draft in the preview side panel in the same tab, and
  MUST show an explicit not-found state when the draft no longer resolves (MC-13). [D8]
- **FR-015**: The `chat_link` MUST be an absolute https URL built from `gateway.public_url` and address the
  draft by Message-ID (**pending Q6**). [D8, O6]
- **FR-016**: `send_email`'s existing policy-based approval MUST remain unchanged. [D8]
- **FR-017**: Humans MUST be able to compose and send a message from the Mail surface through the same
  multipart + signature pipeline (FR-004/FR-005 apply). [D9]
- **FR-018**: Every mail-backed surface MUST render upstream mail failures as explicit error states with a
  retry affordance (MC-8); empty results and failures MUST be visually distinct. [D6]
- **FR-019**: Incoming-message HTML rendering MUST be isolated (sandboxed frame without script execution,
  remote content blocked by default with an explicit load-remote action) — **pending Q3; security-lead review
  is a gate for this requirement's final wording** (MC-10). [O3]
- **FR-020**: Read-state presentation MUST follow Q5's decision; the drainer's flag behavior itself is
  regression-protected (§7.2). — **pending Q5** [O5]
- **FR-021**: Draft panel actions beyond viewing (edit / send / discard) exist only as decided by Q1 —
  **pending Q1**. [O1]
- **FR-022**: Recipient support (single `to` vs CC/BCC) in tools and manual compose follows Q4 — **pending
  Q4**. [O4]

## 9. Success criteria

- **SC-001**: With a signature configured, 100% of outgoing messages from that mailbox (any of the four send
  paths) carry the signature on both MIME parts — verified by unit + integration suites and the UAT lane.
- **SC-002**: Zero occurrences of `<script`, `on*=` handlers or `javascript:` URIs survive the renderer across
  DS-2's hostile rows.
- **SC-003**: Every send with successful SMTP results in a Sent-folder APPEND success or an explicit warning —
  zero silent partial sends in DS-5.
- **SC-004**: The Mail tab lists Inbox/Sent/Drafts with counts matching the live server within one refetch;
  no message body bytes are written under the data dir during the §7 DS-3/DS-4 exercise (MC-12).
- **SC-005**: A draft created by `create_email_draft` is openable from its chat link in ≤ 2 clicks (click link
  → panel shows draft), including after a UIDVALIDITY change (DS-4 row 5).
- **SC-006**: With the mail server unreachable, all three mail surfaces (folders, messages, send) show an
  explicit error within one request round-trip — zero silent-empty renders in the DS-7 rows.
- **SC-007**: `grep -rl '"create_email_draft"' pkg/coreagent/ pkg/config/ pkg/tools/` returns ≥ 3 files
  (registration, ceiling default, tool) — the Definition-of-Done reachability grep (`CLAUDE.md`).

## 10. Traceability matrix

| Requirement | User story | BDD scenario(s) | Test(s) |
|---|---|---|---|
| FR-001 | US-1 | B-1, B-20 | T11, T12, T24 |
| FR-002 | US-1 | B-1 | T22 (editor preview), UAT T24 |
| FR-003 | US-2 | B-4 | T1, T9 |
| FR-004 | US-2, US-5 | B-4, B-16 | T1, T16 |
| FR-005 | US-1, US-2, US-5 | B-2, B-4, B-16 | T1, T2, T9, T16 |
| FR-006 | US-2, US-5 | B-5, B-6, B-16 | T4, T5, T16 |
| FR-007 | US-3 | B-7 | T21 (tab), UAT T24 |
| FR-008 | US-3 | B-7, B-21 | T7, T13 |
| FR-009 | US-3 | B-8, B-11 | T19, T24 (live) |
| FR-010 | US-3 | B-9 | T21 |
| FR-011 | US-3..US-5 | all endpoint scenarios | T12–T20 |
| FR-012 | US-4 | B-12, B-15 | T6, T10 |
| FR-013 | US-4 | (policy wiring) | SC-007 grep + config reconcile tests |
| FR-014 | US-4 | B-13, B-14 | T20, T23 |
| FR-015 | US-4 | B-12, B-22 | T10, DS-4 |
| FR-016 | US-4 | B-15 | existing send approval suites (unchanged, green) |
| FR-017 | US-5 | B-16, B-17 | T16, T17(pending Q1)†, compose validation test |
| FR-018 | US-3 | B-10 | T15, T21 |
| FR-019 | US-3 | B-8 (HTML leg) | T18 — **pending Q3** |
| FR-020 | US-6 | B-18, B-19 | **pending Q5** |
| FR-021 | US-7 | (deferred) | T17 — **pending Q1** |
| FR-022 | US-2, US-5 | (schema-level) | contract tests — **pending Q4** |

† T17 exists only if Q1 approves; FR-017's compose-send leg does not depend on it.

---

## 11. Assumptions (explicit, reviewer-challengeable)

- **A1 — Folder resolution**: the three stable slugs (`inbox`/`sent`/`drafts`) resolve to the server's real
  folder names via IMAP special-use attributes (RFC 6154, `\Sent`/`\Drafts`), falling back to conventional
  names, with the §2.1 per-mailbox override fields winning when set. Rationale: server folder layouts vary
  (e.g. Gmail's `[Gmail]/Sent Mail`); special-use is the standard, pure-Go-discoverable answer.
- **A2 — No push**: the Mail tab does not receive WebSocket push for new mail; TanStack Query refetch covers
  freshness. Adding WS frames is a later wave (out of scope, §1).
- **A3 — Humans compose Markdown too**: the manual compose editor's body is rendered by the same D3 pipeline
  (one rendering path, one sanitization story). The compose UI labels the field accordingly.
- **A4 — New Go deps**: `github.com/yuin/goldmark` (Markdown) and `github.com/microcosm-cc/bluemonday`
  (HTML sanitizer) — both pure Go, CGO-free, satisfying Hard Constraint #2. Neither is in `go.mod` today
  (verified 2026-09-25); adding them is a dependency decision reviewers should confirm.
- **A5 — Mailbox picker**: a workspace with multiple mailboxes gets a picker (agent-named); the single-mailbox
  case hides it (US-3 AS-3).
- **A6 — Signature is configuration**: `signature_html` lives in `config.json` with the mailbox pair and is
  not "mail content" under D6.
- **A7 — Drafts store the Markdown source**: a draft is APPENDed as `text/plain` (the Markdown as written);
  multipart rendering and signature application happen only at send time. This avoids double-signing when a
  draft's final send path differs from its creation path, and keeps one rendering pipeline (D3). The preview
  panel renders the Markdown, so approval sees exactly the content minus signature (the signature is added by
  the system, not the agent).

## 12. Edge cases (consolidated)

| # | Case | Expected behavior |
|---|---|---|
| 1 | Inbound message without a Message-ID header | Listed with `message_id: null`; not deep-linkable; readable by UID from the list |
| 2 | UIDVALIDITY changed since a link was posted | Message-ID addressing still resolves (B-22); UID-only addressing would miss |
| 3 | Draft deleted before its link is clicked | Panel not-found state (B-14, MC-13) |
| 4 | Draft already sent (no longer in Drafts) when link clicked | Same not-found state — the panel only reads Drafts |
| 5 | IMAP server without special-use support | Conventional-name fallback; per-mailbox override wins when set (A1); if resolution fails, folders endpoint 502s with the cause |
| 6 | Mailbox enabled but password unresolvable | Surface shows explicit "mailbox unavailable" state (mirrors the registration skip-WARN, `pkg/agent/email_tools.go::registerEmailToolsForAgent`) — never an empty folder |
| 7 | Signature whitespace-only | Treated as empty (no signature applied) |
| 8 | HTML-only inbound mail (no text part) | `body_text` uses the existing loud-degrade rendering (`pkg/email/transport.go::noHTMLTextMarker`); `has_html=true` drives the Q3 frame |
| 9 | Message-ID containing slashes/special chars in a link | Percent-encoded in the URL; decoded server-side; validation rejects path-traversal-shaped values |
| 10 | Human and agent touch flags concurrently | Last IMAP STORE wins; no local cache to go stale (D6) |
| 11 | SMTP on port 465 vs 587 | Existing `Client.Send` selection logic applies unchanged to all send paths |
| 12 | Body > 256 KB on read | Existing `capBody` truncation marker applies (`pkg/email/transport.go::capBody`) |

## 13. Holdout evaluation scenarios (post-implementation, NOT in the traceability matrix)

Evaluated by the founder or an external evaluator against a real mailbox after development completes.

- **H-1 (happy)**: Compose and send a mail from the Mail tab to an external account; open it in a mainstream
  client (Apple Mail / Gmail): formatting, link and signature all render; the plain-text fallback reads
  correctly when HTML is disabled in that client.
- **H-2 (happy)**: Ask an agent to draft a reply to a real received mail; click the chat link; the panel shows
  the draft; the same draft is visible in the Drafts folder of the founder's own mail client.
- **H-3 (happy)**: After an agent sends mail, the message appears in the founder's own client's Sent folder
  without any local Omnipus copy involved.
- **H-4 (error)**: With the network to the mail server cut, open the Mail tab: every surface shows an explicit
  error with retry — nothing silently renders as an empty mailbox.
- **H-5 (error)**: Delete a draft in the founder's own mail client, then click its chat link in Omnipus: the
  panel reports the draft no longer exists.
- **H-6 (edge)**: Open a marketing email with tracking pixels; with browser devtools network tab open, confirm
  zero outbound requests to third-party hosts until "load remote content" is clicked (**pending Q3**).
- **H-7 (edge)**: Break the mailbox password deliberately: the Mail tab and tools degrade with explicit
  unavailability states; nothing silently pretends the mailbox is empty.

## 14. Questions for the founder

Each question below blocks only the spec parts marked "pending Qn". Everything else is implementable as
written. Format per house rule: context, impact, options, recommendation.

### Q1 (O1) — What may the draft preview panel do, and does panel-Send count as the approval?

**Context.** D8 fixes draft creation + a preview side panel. What the panel may *do* is open. A human clicking
"Send" in the panel is a human action — tool-policy ask prompts gate agent tool calls, not human UI — so panel
Send is effectively an approval act by construction. The open part is whether you want it to be *the*
approval (replacing an agent-side `send_email` for this draft), and whether editing belongs in the panel.

**Impact.** §2.3's `POST/DELETE …/drafts/{messageId}` endpoints, US-7, FR-021, T17, and the panel UI scope.
View-only ships the founder's "way to approve" as read-and-then-agent-sends; panel-Send ships one-click
approval.

**Options.**
- **A — View only.** The human reviews the panel and tells the agent (chat) to send; `send_email` approval
  stays the single gate. Smallest scope; approval still needs a chat round-trip.
- **B — View + Send + Discard.** Panel Send transmits the stored draft through the same pipeline (multipart +
  signature + Sent APPEND) and removes it from Drafts; Discard deletes it. One-click approval; no editing.
- **C — View + Edit + Send + Discard.** Adds editing the draft before sending (re-APPEND the updated draft).

**Recommendation.** **B.** It makes "approve an email draft" a single human action without building a compose
experience into a side panel; if editing proves wanted, C is additive (US-5's compose can absorb it).

*Pending on Q1: §2.3 (draft send/discard endpoints), US-7, FR-021, B-scenarios for panel actions, T17.*

### Q2 (O2 — verified) — Chat transcripts already store email bodies; is that compatible with D6?

**Context.** Verified during this spec's writing (§3.3): session transcripts persist the full argument text of
`send_email`/`reply` — i.e. whole email bodies — because `pkg/memory/jsonl.go::addMsg` marshals the complete
`providers.Message`, and `pkg/providers/protocoltypes/types.go::FunctionCall.Arguments` is a serialized JSON
string. `read_message` results (full received bodies) persist the same way. Transcripts are append-only by
design (recall archive, `pkg/session/jsonl_backend.go::Save`).

**Impact.** A literal D6 ("no local copy of mail content") is already false today, before this feature. Making
it true would require redacting tool-call arguments and results from transcripts — breaking the agent's own
context continuity, `recall_conversation`, and fighting the append-only transcript design.

**Options.**
- **A — Scope D6 to dedicated storage.** D6 means: no *mailbox store* (no local mirror, cache, or index of
  mail); transcripts stay the agent's working record, as they already are for every other tool surface.
- **B — Redact email bodies from transcripts at rest.** Custom marshaler redacting `send_email`/`reply`
  arguments and `read_message` results; loses recall fidelity and agent continuity; sizable new surface.
- **C — Keep transcripts but document the local-copy reality in user docs** (same as A plus an explicit doc
  note, since the founder asked "without saving the emails locally").

**Recommendation.** **A** (with C's doc note folded in). The transcripts are how every tool already works;
singling email out buys a large mechanism for a distinction users won't see. UAT/docs must state plainly that
sent/received bodies appear in session transcripts on disk.

*Pending on Q2: §1 scope wording, FR-009's "reference metadata" clause, MC-12, §3.3 disposition.*

### Q3 (O3) — How is incoming HTML rendered in the Mail tab?

**Context.** Email HTML is untrusted attacker-controlled content. The in-repo precedent
(`src/components/library/LibraryPreviewPane.tsx::LibraryHtmlFrame`) renders operator-owned HTML in a sandboxed
iframe with `allow-scripts` plus a gateway CSP `sandbox` directive — scripts allowed there because Library
files are the operator's own. Email must not run scripts; remote images are tracking pixels.

**Impact.** FR-019, MC-10, T18, the `mail/html-preview-token` endpoints, and a mandatory `security-lead`
review. This is the one place the feature can import a vulnerability.

**Options.**
- **A — Sandboxed frame, no scripts, remote content blocked, per-message opt-in.** `sandbox` **without**
  `allow-scripts`; gateway-served with CSP `sandbox` + `img-src 'self' data:` by default; a "load remote
  content" action re-mints the token with `load_remote=true`, relaxing `img-src` to `https:`.
- **B — Never render HTML; always show derived text.** Maximum safety, worst fidelity — the "mini outlook"
  promise collapses for the majority of real mail.
- **C — Render HTML with scripts in an opaque origin.** Closest to a desktop client; largest attack surface;
  contradicts the repo's no-unsafe-eval/no-script-import posture.

**Recommendation.** **A**, with `security-lead` reviewing the exact header set before implementation lands.

*Pending on Q3: FR-019, MC-10, T18, §2.3 html-preview endpoints, H-6.*

### Q4 (O4) — Recipients: keep one-recipient-only, or add CC/BCC now?

**Context.** Today `send_email` accepts exactly one recipient (`pkg/tools/email.go::SendEmailTool` — the
description says so explicitly). Manual compose (D9) realistically wants To + CC + BCC. Changing the agent
tools touches the tool schema, descriptions, and the contract in the same commit.

**Impact.** FR-022, `MailSendRequest` fields, `SendEmailTool`/`ReplyTool` parameter schemas, MC-15, DS-5.

**Options.**
- **A — One recipient everywhere.** Smallest; manual compose crippled for real correspondence (CC is table
  stakes for humans).
- **B — Manual compose gets To/CC/BCC; agent tools stay one-recipient.** Human UI affordance only; no agent
  behavior change; D8 untouched.
- **C — Agents get CC/BCC too.** Schema + description changes to both send tools; widens what an approved
  agent send can do (more recipients = wider blast radius of one approval).

**Recommendation.** **B.** Manual mail needs them; agents' one-recipient scope keeps the approval story tight
and can be revisited later without rework (the pipeline takes a recipient list internally either way).

*Pending on Q4: FR-022, §2.2 `MailSendRequest`, §2.4 tool param table, MC-15.*

### Q5 (O5) — How should the Mail tab show "read by agent" vs "read by human"?

**Context.** The drainer marks mail `\Seen` the moment it becomes a Board task
(`pkg/email/drainer.go::drainMailbox`), and `read_message` marks `\Seen` on agent read. Flags are shared with
the founder's own mail client, so without an extra signal every drainer-handled message looks "already read"
in the Mail tab and unread counts read as zero.

**Impact.** US-6, FR-020, B-18/B-19, the `unread_count` semantics in `MailFolder`.

**Options.**
- **A — Accept shared `\Seen`.** Unread = nobody (human or agent) has touched it. Simplest; the Inbox list
  loses the "not yet handled by the agent" signal.
- **B — IMAP keyword for agent handling.** The drainer (and agent reads) additionally set a custom keyword
  (e.g. `$OmnipusHandled`); the Mail tab badges those messages and excludes them from unread. Server-side,
  no local store (D6-clean); servers that reject keywords degrade to A.
- **C — Local reference metadata** (Message-ID set per mailbox, D6-permitted). No server dependency, but the
  set drifts from the server and must be reconciled.

**Recommendation.** **B**, degrading to **A** when the server refuses keywords.

*Pending on Q5: FR-020, US-6 scenarios, B-18/B-19, `MailFolder.unread_count` semantics.*

### Q6 (O6) — What should the chat link point at?

**Context.** IMAP UIDs are stable only while UIDVALIDITY is unchanged; the founder may click a posted link
days later. Message-IDs are stable per message but need a server-side lookup to resolve to a UID at read time.

**Impact.** FR-015, §2.3's `{messageId}` path semantics, B-22, DS-4, and the `chat_link` the tool returns.

**Options.**
- **A — Message-ID addressing.** Link carries (workspace, agent, folder=drafts, Message-ID); the read endpoint
  SEARCHes HEADER Message-ID at request time. Survives renumbering; costs one IMAP search per open.
- **B — UID addressing.** Cheapest; breaks whenever the server renumbers (UIDVALIDITY change or draft
  re-APPEND).
- **C — Local Message-ID→UID mapping table.** Fast resolution, but a stored index that drifts — the thing D6
  keeps out.

**Recommendation.** **A.** One bounded IMAP search per panel open is well inside the existing 45s command
budget (`pkg/email/transport.go::commandTimeout`), and it is the only option that keeps links honest without
local state.

*Pending on Q6: FR-015, §2.3 messageId semantics, B-22, MC-11's chat_link.*

### Q7 — (raised by plan-spec, not in O1–O6) What exactly is the Mail surface — Library-style panel, or a real tab page?

**Context.** D4 says "a Mail tab inside the workspace, similar to the Library, reusing components". The Library
itself is **not** a tab page: the tab-strip entry (`media`) is a redirect stub that opens a **docked side
panel** (`src/components/library/LibraryPanel.tsx`) and navigates back to Chat, with a fullscreen pop-out route
as the only other layout. "Mini outlook" could equally describe a full workspace page like Board/Calendar.

**Impact.** FR-007, the whole frontend slice (§16), the deep-link route the chat link targets (Q6), and the
E2E surface. This is the largest single shape decision left.

**Options.**
- **A — The exact Library trio.** Tab-strip entry (`mail`) → docked `MailPanel` (aside in `AppShell`, state in
  the `ui` store mirroring `libraryPanel`) + redirect-stub route + fullscreen pop-out. Reuses the docked-panel
  infrastructure and matches "similar as the library" literally.
- **B — A real tab page** (`/workspaces/{id}/mail`, like `board`/`calendar`) with the list + preview inline.
  Most Outlook-like, roomiest; but a new full-page surface, more shell churn, and it departs from the stated
  Library model.
- **C — Panel only, no tab entry.** Smallest, chat-link-first; but the founder explicitly asked for a tab.

**Recommendation.** **A.** It is what "similar to the Library, reuse components" concretely means in this
codebase, and the preview side panel D8 requires exists in that shape anyway. The spec body is written
against A; if B is chosen, §16's panel/hosting notes re-key to a page route and FR-007's wording changes —
nothing else in the spec moves.

*Pending on Q7: FR-007, §16 frontend hosting, the chat-link target route, T21/T23 E2E specifics.*

---

## 15. Implementation notes — backend (Phase 4+; implementation guidance, not new policy)

- **New file** for mail read/send gateway handlers (e.g. `pkg/gateway/rest_mail.go`) — `rest_mailbox.go`
  (803 lines) stays config-only; one file, one job. Same `loadWorkspace`-idiom 404s and `ErrorResponse`.
- **`pkg/email`** gains: a compose/render unit (goldmark render → bluemonday sanitize → multipart builder →
  signature on both parts; Message-ID generation), a Sent-APPEND after SMTP success inside the send path, an
  APPEND primitive (`\Draft` flag for drafts), folder listing/counts, and a Message-ID→UID resolution
  (`SEARCH HEADER`). The `Transport` interface grows accordingly; the in-memory test fake grows with it.
  The connectionless dial-per-operation pattern (`Client.dialIMAP`) is preserved.
- **Folder resolution** (A1) lives in `pkg/email`; per-mailbox overrides ride `MailboxConfig`.
- **Signature plumbing**: `EmailMailboxPanel` PUT carries `signature_html` through the existing raw-map write
  path — which must keep preserving unknown fields (ADR-033 §5 negative note).
- **`create_email_draft`** joins `tools.EmailToolset` (`pkg/tools/email.go`) so registration, policy ceiling
  (`pkg/config/defaults.go::defaultToolPolicyCeiling`), enable-time fill-in, and the permissions screen come
  from the same list the five existing tools use.
- **HTML preview token** endpoints mirror the Library pair (`/library/preview-token`) including token expiry;
  the handler sets the MC-10 headers (**pending Q3**).
- **Deps** (A4) are the only new third-party imports; `go.mod` diff reviewed in the contract commit.

## 16. Implementation notes — frontend (concrete components to reuse; pending Q7 for hosting)

Design-system skill (`omnipus-design-system`) is mandatory before any file under `src/components/` is touched.

| Need | Reuse | Notes |
|---|---|---|
| Tab-strip entry | `src/components/workspaces/WorkspaceTabBar.tsx::WORKSPACE_TABS` | One entry `{ segment: 'mail', label: 'Mail', Icon: Tray }` (Phosphor; no emoji); `TabSegment`/`SEGMENT_LABELS` derive automatically |
| Panel hosting | `src/components/library/LibraryPanel.tsx` pattern + `src/store/ui.ts::libraryPanel` | New `mailPanel` state + `openMailPanel/closeMailPanel`; docked `<aside>` sibling in `AppShell.tsx` next to `LibraryPanel` (same `flex-shrink-0` column contract) |
| Route | `src/routes/_app/workspaces.$workspaceId.media.tsx` | Redirect-stub pattern for the `mail` segment (Q7-A) — opens the panel scoped to the workspace, navigates back to Chat |
| List + preview split | `src/components/library/LibraryExplorer.tsx` placeholder-slot structure | `MailExplorer` hosts `MailPreviewPane` in the slot; virtualization per Library's list conventions |
| Message/draft preview | `src/components/library/LibraryPreviewPane.tsx` (structure), `LibraryMarkdownPreview` (draft Markdown render) | Exhaustive dispatch per kind, per the "no `&&` chain" rule documented in `LibraryPreviewPane.tsx` |
| HTML mail body | `LibraryHtmlFrame` pattern (sandboxed iframe + gateway CSP) | Mail variant: **no** `allow-scripts` (**pending Q3**); served via `/mail/html-preview/{token}` |
| Upstream error states | `src/components/library/LibraryErrorBanner.tsx`, `src/components/shared/QueryErrorState` | FR-018's explicit error + retry |
| Compose dialog | shadcn `Dialog` + the validation patterns of `src/components/connectors/EmailMailboxPanel.tsx` (`FieldRow`, `validate`) | Field-level errors; Markdown-labeled body (A3) |
| Signature editor | Section inside `src/components/connectors/EmailMailboxPanel.tsx::EmailMailboxPanel` | Textarea + live preview rendered in a sandboxed mini-frame with the same posture as MC-10 (**pending Q3**); 16,384-char bound enforced client-side too (MC-1) |
| Chat link → panel | `src/components/chat/markdown-shared.tsx::createLinkRenderer` | Same-origin mail deep links navigate in-place (router) instead of `target="_blank"`; `src/lib/url-safe.ts::isSafeHref` gating unchanged |
| Data fetching | TanStack Query over generated types (`src/lib/api/generated/`) | Generated types only (shared rule 4); polling per A2 |

## 17. Reachability (Definition of Done)

- **Operator**: Connectors screen → mailbox panel shows the Signature section (US-1); workspace tab strip
  shows **Mail** and opens the surface (US-3); compose action reachable from the surface header (US-5).
- **Human via chat**: a posted draft link opens the preview panel in the same tab (US-4) — exercised in the
  UAT lane with a real mailbox.
- **Agent**: `create_email_draft` is registered for every agent via
  `pkg/agent/email_tools.go::registerEmailToolsForAgent` (so it appears on the per-agent permissions screen),
  carries a ceiling default in `pkg/config/defaults.go::defaultToolPolicyCeiling` that
  `config.ReconcileToolPolicyCeiling` self-heals onto existing installs, and joins the mailbox-enable
  allow-fill set. Reachability grep (SC-007): `grep -rl '"create_email_draft"' pkg/coreagent/ pkg/config/
  pkg/tools/` must return ≥ 3 files.
- **Delivery statement (two lines, never merged)**: *code correct and tested* — unit + gateway-integration
  suites green, contract verification green; *reachable by a user and an agent* — tab entry, panel, signature
  editor, compose, and the agent tool all invocable as listed above, confirmed by the UAT lane on a real
  mailbox.

## 18. Assembly summary (plan-spec Phase 6)

| Measure | Count |
|---|---|
| User stories | 7 (US-1 … US-7) |
| BDD scenarios | 22 — Happy Path 10 · Alternate Path 3 · Error Path 4 · Edge Case 5 |
| Test datasets | 7 (DS-1 … DS-7) |
| Functional requirements | 22 (FR-001 … FR-022) |
| Success criteria | 7 (SC-001 … SC-007) |
| Open questions blocking parts of the spec | 7 (Q1–Q7, §14) |

**Gaps flagged for follow-up** (none block spec review; each is owned by a Q):

- Q1 gates the draft panel's action set (§2.3 draft endpoints, US-7, FR-021, T17).
- Q2 gates D6's literal wording vs the verified transcript reality (§3.3, FR-009, MC-12).
- Q3 gates the incoming-HTML posture and the security-lead review (FR-019, MC-10, T18, H-6).
- Q4 gates recipient fields (FR-022, §2.2, §2.4, MC-15).
- Q5 gates unread/badge semantics (FR-020, US-6, B-18/B-19).
- Q6 gates chat-link addressing (FR-015, §2.3 messageId, B-22, MC-11).
- Q7 gates the Mail surface shape (FR-007, §16 hosting, chat-link target route, T21/T23).

**Ambiguity disposition (Phase 5.5)**: every ambiguity found in the audit is either answered by an
assumption (§11 A1–A7), left pending on an explicit founder question (§14 Q1–Q7 with pending
markers at each affected spec part), or covered by a non-behavior (§5). No ambiguity was resolved
by silent guessing.
