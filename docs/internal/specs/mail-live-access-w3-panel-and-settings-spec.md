# Feature Specification: Mail live access — W3 Mail panel and settings surface

**Created**: 2026-10-02
**Status**: Proposed — draft for the grill. This is the W3 implementation specification for the
founder-approved design in `docs/internal/architecture/ADR-20261001-mail-live-access-pooling-folder-discovery-and-cache.md`
(cited below as "the ADR"). It has not been grilled, gated or approved for implementation.
**Input**: the ADR (one prescribed grill round applied — findings I-01…I-06, M-01, M-02; founder answers
Q1=A, Q2=B, Q3=A, Q4=A, Q5=A recorded 2026-10-02) and its review
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-report.md`.
**Work package**: W3 — panel/settings (frontend-lead), per the ADR's "Affected components and parallel
work packages" table. This spec covers design work package W3, the frontend half of W4's gateway-facing
request shapes, and the frontend parts of the ADR's feature sections F1, F2 and F7.
**Sibling specs this one must not edit**: W8's Library-viewer spec (renderer/resource-policy
implementation), W0's contract change set, W1/W2 backend read-runtime specs, W7's Save-service spec.
Interfaces this spec publishes to, and consumes from, those packages are frozen in §3.

**Certainty convention used throughout**: **Verified** = the file/symbol was read in THIS checkout
(worktree `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail`, branch
`docs/adr-mail-live-access`). **Inferred** = an architectural consequence or recommendation, not a tested
result. **Unknown** = a genuine evidence gap; no number, provider or behaviour is invented to fill one.
GitNexus MCP tools were not exposed in this session (same gap the ADR and its grill record); direct
Read/Grep sweeps ground every impact row, which are therefore labelled Inferred.

---

## 1. Summary and scope

Mail today makes the user wait for a live IMAP round trip before a single row renders, re-fetches on a
30-second timer that the new design retires, shows at most one 20-row page with no way forward, renders an
unknown folder count as a bare `0`, and offers no attachment action except a browser download. This spec
defines the panel half of the founder-approved fix: the Mail panel shows labelled cached rows immediately,
validates them with **at most one** explicit live-refresh request per eligible event, distinguishes the
four data sources honestly, pages 25 rows at a time to a 200-row ceiling with a reachable search path
beyond it, exposes the existing per-mailbox Sent/Drafts folder-name settings, tells the gateway when a
panel is open through authenticated presence frames, and hands attachments to the Library viewer as
temporary, read-only, resource-constrained previews with a fully specified keyboard and screen-reader
contract.

In scope (all frontend, in the ADR's W3-owned files unless a boundary says otherwise):

- Cache-first display and event-driven freshness for the folder rail, the message list and the reading
  pane — replacing the D25 30-second panel refetch (the ADR's Authority table supersedes it).
- Freshness labelling and semantics for the four sources (`live`, `memory`, `encrypted_disk`, `none`),
  each with a nullable last-validated time and a stale flag; unknown count never renders as zero.
- Paging: 25 rows per page, Load more adds 25, a hard ceiling of 200 rows per folder per view, and a
  reachable folder-scoped search path beyond it.
- The visible folder-name override surface, reusing the existing `sent_folder_name` / `drafts_folder_name`
  configuration fields, with "automatic" spelled out on screen.
- Panel-presence frames over the authenticated gateway WebSocket: open/close, lifecycle (tab close,
  navigation, logout, socket loss) and several tabs.
- The attachment row changes (paperclip indicator in list rows, Open/Save in the reading pane) and the
  Mail-to-Library handoff: context bar, focus, announcements, keyboard and narrow-screen behaviour (grill
  finding I-06).
- The temporary-source resource policy the handoff carries (grill finding I-04) and the scripts-off
  default with per-file checkbox for saved mail-derived HTML (founder Q5=A) — as requirements and
  acceptance criteria; the renderer-side implementation belongs to W8's owned files.
- Every user-visible state of the above with its exact text.

Out of scope for this spec (owned elsewhere; boundaries in §3):

- Everything behind the gateway: pooling, budgets, the cache itself, discovery, the publication revision,
  the preview byte endpoint, the Save service (W1/W2/W4/W7).
- The Library viewer's internal temporary-source implementation, renderer adapters and resource resolver
  (W8) — this spec publishes the handoff descriptor and the interaction contract both sides honour.
- Contract YAML edits and regeneration (W0, Hard Constraint #8) — this spec names the generated types W3
  consumes and the ones W0 must add, and consumes nothing until regenerated.
- Phase 2 JMAP transport selection UI, the Connectors transport-preference control (a later wave), the
  removal/cleanup-pending UX, and the summary screen (unchanged this phase except where the ADR says it
  stays independent).
- Any layout, brand or visual redesign — "Sovereign Deep" stays as shipped; the ADR forbids one and so
  does this spec.

---

## 2. Existing Codebase Context

Every row below was read in this checkout. "Change" means what this spec's work does to the symbol; the
blast radius is the direct-consumer sweep performed for this spec (GitNexus unavailable — rows labelled
Inferred per the shared-rules fallback).

### 2.1 Files this package owns (W3)

| File :: symbol | Role today (Verified) | Change this spec requires | Blast radius of changing it (Inferred, from caller sweep) |
|---|---|---|---|
| `src/lib/api/mail.ts` :: `fetchMailFolders`, `fetchMailMessages`, `fetchMailMessage`, `markMailSeen`, `fetchMailAttachment`, `fetchMailSummary`, `sendMailMessage`, `saveMailDraft`, `sendMailDraft`, `discardMailDraft`, `mintMailHtmlPreviewToken`, `mintMailSignaturePreviewToken`, `mailUidRef` | The single mail REST client. Every response validates through a generated Zod schema (`./generated/schemas`); query params today are `limit`, `before_uid`, `retry` (the human-dial marker). | Add `mode=cache_first\|live`, `observer_id` (folder/list reads), `refresh_mapping` (folder reads), the search param, and the new generated calls (`MailAttachmentPreviewRequest`, `MailAttachmentSaveRequest`, `MailReplyContextRequest`). Response shapes stay generated-only. | Consumers: `MailPanel.tsx`, `MailPreviewPane` consumers via props, `EmailMailboxPanel.tsx` (signature mint only), and the `@/lib/api` barrel re-export. Guarded by `src/lib/api/mail.message-ref.test.ts` and `mail.retry.test.ts` — both must keep passing; `retry` semantics do not change. |
| `src/components/workspaces/mail/MailPanel.tsx` :: `MailPanel`, `AttachmentList`, `downloadMailAttachment`, `ListSkeleton`, `mailErrorCode`, `isMailConnectionFailure`, `draftMidRef`, `fileToBase64` | The panel content: mailbox resolution, folder rail + message list + reading pane + compose + watcher banner. Polls folders every 30 s (`FOLDERS_REFETCH_MS`, D25) and summary every 30 s; human-dial `retry=true` flows through `foldersHumanRef`/`messagesHumanRef`/`detailHumanRef`. Attachments render name/size and a Download button only. Body HTML renders via `MailHtmlFrame` with a minted token. | The core rewrite: cache-first view state, one live refresh per eligible event, freshness labels, paging/search, presence wiring, attachment Open/Save, folder-availability states. The 30 s folder refetch is **removed** (D25 superseded); the 30 s summary poll **stays** (reads saved watcher state, never dials — ADR P1.4 Refresh boundary). | Tests: ~30 `MailPanel.*.test.tsx` files assert current oracle text and the D25 cadence (`MailPanel.states.test.tsx` asserts "folders refetch every 30s while mounted" — a legacy oracle that **contradicts this spec**; per the frontend rules test files are qa-lead's to rewrite, flagged in §8's regression plan). Registered through `mailPanelDefinition.tsx` only. |
| `src/components/workspaces/mail/MailMessageList.tsx` :: `MailMessageList`, `ListRowTop`, `ListRowBottom` | List rows: unread dot, subject, read-by-agent tag, timestamp (year-one hidden in list rows only), sender/recipient line. No pagination, no attachment indicator. | Add the paperclip indicator (`has_attachments`, F7), the "Load more" affordance below the list, and stable accessible identity per row (focus-return fallback target for I-06). | Consumed only by `MailPanel.tsx`. |
| `src/components/workspaces/mail/MailFolderRail.tsx` :: `MailFolderRail`, `FolderIcon` | Folder tabs with counts. Renders `folder.total` directly and `unread ?? 0` for the inbox — a null count would render as `0` today (the exact unknown-equals-zero defect the ADR forbids). | Render unknown (`total: null`) as an explicit "—" marker, never `0`; add per-role availability states (confirmed-absent explanation, unresolved prompt); keep inbox badge semantics. | Consumed only by `MailPanel.tsx`. |
| `src/components/workspaces/mail/mail-format.ts` :: `formatMailDate`, `formatMailTime`, `formatMailBytes` | Display formatting. Rejects unparseable input but not year-one (the list row hides it; the detail header does not — the ADR's E-Date asymmetry). | Extend with the freshness-label formatter (relative last-checked time) and the **No date** rule for nullable dates (F6): null → `No date`; year-one/zero → `No date`; never blank and never "1 Jan 1". | Consumed by `MailPanel.tsx`, `MailMessageList.tsx`, `MailPreviewPane` (via props). `MailPanel.draftDateFormat.test.tsx` pins draft dates. |
| `src/components/workspaces/mail/MailComposeDialog.tsx` :: `MailComposeDialog` | Compose/new/reply dialog. `replyTo` carries only From/subject/Message-ID; Cc/Bcc/body start empty (the F5 panel gap the ADR verified). | Consume the generated reply-context result (To/Cc, quoted body) for Reply and Reply all; keep the existing unsaved-input preservation on failed send. | Covered by `MailCompose*.test.tsx`, `MailRecipientInput.R5.test.tsx`. |
| `src/components/workspaces/mail/mailPanelDefinition.tsx` :: `mailPanelDefinition` | The side-panel registration (id `mail`): fullscreen search codec, `beforeLeave` discard gate, lazy chunk. | Mount point for the presence adapter's lifecycle hooks (open with panel, close on unmount) — the definition already owns Mail's shell contract; no shell edit. | Shell registry `src/components/panel-shell/registry.tsx::panels` consumes it; the fullscreen codec has its own tests (`mailPanelDefinition.test.tsx`, panel-shell tests). |
| `src/components/workspaces/mail/mailUnsavedGuard.ts` + `MailPanel`'s `ConfirmDialog` | The ONE discard-unsaved-edits dialog for compose and the draft editor (exact strings verified in §11, state S-14). | Preserved unchanged; this spec adds no second guard and no new discard prompt. | `MailPanel.unsavedLeave.test.tsx` pins it. |

Proposed new files (all siblings inside the W3-owned trees, one owner each):

| Proposed file | Job | Why a new file |
|---|---|---|
| `src/components/workspaces/mail/mailCacheView.ts` | The panel's cache-view adapter: per (workspace, mailbox, folder) display state fed by a cache-first read plus at most one live refresh per eligible event; publication-revision ordering (drop superseded responses); freshness-label inputs. Pure store + functions, no JSX. | `MailPanel.tsx` is already ~960 lines; the event/revision rules are the heart of the spec and need unit tests that don't mount the panel. |
| `src/components/workspaces/mail/mailPanelPresence.ts` | Observes `useUiStore` panel open/close (`src/store/ui.ts::openPanel`, `closePanel`, `activePanel`), pagehide, logout and `WsConnection` state; sends the generated `mail_panel_observer` frames with a per-panel-instance opaque `observer_id`. | Presence is a cross-cutting adapter (ui-store + ws + lifecycle), not panel JSX; isolating it keeps the WS contract testable without mounting Mail. |
| `src/components/workspaces/mail/mailAttachmentHandoff.ts` | Builds the generated `MailAttachmentPreviewRequest` from an attachment row + open message, invokes the mint, hands the generated `MailAttachmentPreviewResponse` to the W8 mount seam, owns the return-focus identity and the status-region announcements (I-06). | The focus/announcement contract is distinct logic with its own tests; both Mail and the W8 mount need it without a second writer in either's file. |

### 2.2 Files this package touches in other owners' trees (single coordinated change each)

| File :: symbol | Owner | Boundary |
|---|---|---|
| `src/components/connectors/EmailMailboxPanel.tsx` :: `EmailMailboxPanel`, `MailboxFormState`, `EMPTY_FORM`, `validate`, `doSave` (calls `saveAgentMailbox` with the generated `MailboxConfigureRequest`) | Connectors UI is in W3's ownership column in the ADR's W3 row ("`src/components/connectors/EmailMailboxPanel.tsx`"). | Verified today: the form state has **no** `sent_folder_name`/`drafts_folder_name` fields — the wire fields exist (`contracts/components/schemas/Mailbox.yaml`, `MailboxConfigureRequest.yaml`; generated `src/lib/api/generated/schemas.ts` carries both) but no UI exposes them. This spec adds the two fields, reusing the existing save path unchanged. |
| Library handoff seam (W8-owned; consumed, not edited, by W3): `src/components/library/LibraryExplorer.tsx` :: `selectedEntry` (resolved from `sortedEntries` by `selectedPath`), `src/components/library/LibraryPreviewPane.tsx` :: `LibraryPreviewPaneProps` (requires a real `LibraryEntry` with a workspace `path` today) | W8 | W3 publishes the handoff descriptor and interaction contract; W8 adds the temporary-source mount that renders without a resolved row. Neither side edits the other's files. |
| Renderer resource seams (I-04): `src/lib/url-safe.ts::isDisplayableImageSrc` (permits any http/https/data URL resolved against the current origin — a same-origin `/api/v1/...` Library path passes), `src/components/library/preview/KbMarkdownImage.tsx::KbMarkdownImage` (mounts whatever passes), `src/lib/api/library.ts::libraryDownloadUrl` (same-origin authenticated download URL shape) | W8 | The temporary source's resolver must refuse these structurally for mail-derived renderers; ordinary workspace rendering keeps today's behaviour. W3's acceptance criteria assert the observable outcome (§4 US-8). |
| `src/lib/ws.ts` :: `WsConnection.send(frame: ClientFrame)`, `parseFrameSafe` | Shared socket client; the frame *types* are W0's (generated), the client file itself gets no Mail-specific edit from W3 unless the adapter needs a public send handle — to be traced with the integration owner (W4) before dispatch. | The `mail_panel_observer` frames ride this connection; the enum today (verified in `contracts/asyncapi.yaml::WsFrameType`) has no mail frame — `library_changed` exists and is the save-notification reuse path. |
| `src/lib/authLogout.ts` | Logout teardown already closes the WebSocket with code 1008 (verified in its header comment) | Presence close-on-logout piggybacks on socket teardown; no edit expected — listed as a consumed lifecycle event. |

### 2.3 Contract shapes this package consumes (all generated; none hand-written)

| Generated type (today, Verified) | Where | Relevant shape facts verified in this checkout |
|---|---|---|
| `MailFolderList`, `MailFolder` | `src/lib/api/generated/`, from `contracts/components/schemas/MailFolder.yaml` | `folders[]` of exactly three slugs (`inbox\|sent\|drafts`); `total` is a **required integer** (no null, no availability, no mapping source); `unread_count` nullable, Inbox only. |
| `MailMessagePage`, `MailMessageSummary` | from `MailMessagePage.yaml` / `MailMessageSummary.yaml` | Page: `messages[]`, `truncated`, `next_before_uid` (nullable int) — no cursor object, no `has_more`, no `view_limit_reached`, no metadata. Summary: `date` required non-null date-time; **no** `has_attachments`. List endpoint today defaults 20 and clamps 100 (ADR wire table). |
| `MailMessage`, `MailAttachment` | from `MailMessage.yaml` / `MailAttachment.yaml` | Detail carries `attachments[]` (`part_index`, `filename`, `content_type`, `size_bytes` non-null) — the reading pane's rows; no `message_ref` yet. |
| `MailSummaryList`, `MailboxNewMailSummary` | from the summary schema | Watcher banner: `watcher_state`, `last_error_class`, `next_attempt_at` — stays as is. |
| `Mailbox`, `MailboxConfigureRequest` | from `Mailbox.yaml` / `MailboxConfigureRequest.yaml` | `sent_folder_name` / `drafts_folder_name` optional strings; backend `restAPISetAgentMailbox.persistConfig` preserves omitted values and clears explicitly-empty ones (the ADR's E-Overrides correction) — the frontend just needs the fields visible. |
| `MailUnavailableError` (503) | from `MailUnavailableError.yaml` | `code: busy\|backoff`, `last_error_class`, `next_attempt_at`. The ADR adds a `reason` enum (`pool_busy\|account_busy\|server_connection_limit\|backoff`) — consumed when regenerated. |
| `LibraryEntry` | from `LibraryEntry.yaml` | Requires a real workspace-relative `path` — the reason the temporary source must not fabricate one (the ADR's F1 viewer-input row). No `preview_profile` yet. |
| WS `ClientFrame` / `ServerFrame` | `src/lib/ws.ts` re-exports from generated | No mail frame type exists; `WsFrameType` enum in `contracts/asyncapi.yaml` verified complete for this claim. |

### 2.4 Contract shapes W0 must add before any W3 code (proposed names from the ADR's wire-impact section)

| Proposed generated shape | W3 consumes it for |
|---|---|
| `MailReadMetadata` — `source: live\|memory\|encrypted_disk\|none`, `last_validated_at` (RFC 3339, nullable), `stale: boolean`, `refresh_needed: boolean`, `notice_code` (nullable closed enum, e.g. `cache_unavailable`), `publication_revision` (nullable opaque) | Freshness labels (US-2); superseded-response dropping (US-1). |
| `MailFolderList`/`MailMessagePage` gain metadata; `MailFolder` gains `availability: present\|absent\|unknown`, nullable `uidvalidity`, `mapping_source: override\|special_use\|fallback\|none`, `total` nullable | Rail states (US-2, US-4). |
| Paging: `next_cursor` (nullable opaque string), `has_more`, `view_limit_reached`; panel default/max 25; `search` query param; typed 409 stale-cursor result | US-3. |
| `MailAttachmentPreviewRequest` / `MailAttachmentPreviewResponse` (incl. `content_source.byte_url`, nullable `isolated_html_url`, `token`, `expires_in_seconds`, `read_only`, `text_readable`, `subject`, descriptor) and `message_ref` on list/read results (I-03) | US-6 (Open handoff). |
| `MailAttachmentSaveRequest` (with `save_operation_token`, M-02) / `MailAttachmentSaveResponse` (`saved`, real `entry: LibraryEntry`, `path`, `absolute_path`, `size_bytes`, `audit_status`, `warning_code`) | US-6 (Save branch), §11 states S-12/S-13. |
| `MailReplyContextRequest` / `MailReplyContextResponse` | F5 reply/quote prefill. |
| `MailMessageSummary.has_attachments: boolean` required; `date` nullable (F6/F7) | Paperclip indicator; **No date**. |
| AsyncAPI: `mail_panel_observer` client frame (`action: open\|close`, `observer_id`, `workspace_id`) + server acknowledgement/error frame, bound to the authenticated socket | US-5 (presence). |
| `MailUnavailableError.reason` (`pool_busy\|account_busy\|server_connection_limit\|backoff`); `LibraryEntry.preview_profile: workspace\|mail_restricted` + the per-file scripts-allowance field (Q5=A, W0/W7) | Busy copy; US-9. |

**Sequencing rule (Hard Constraint #8)**: W3 consumes generated types only. Until W0's schemas land and
`scripts/gen-contracts.sh` regenerates `src/lib/api/generated/`, no W3 production file changes. The one
inline-discriminated-union exception (OpenAPI-hosted `oneOf`) is W0's concern, not W3's.

---
