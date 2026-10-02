# Feature Specification: Mail live access — W3 Mail panel and settings surface

**Created**: 2026-10-02
**Status**: Proposed — the one prescribed spec-correction round applied (2026-10-02), then the **final
Round-2 fix round applied (2026-10-02)**. This is the W3
implementation specification for the founder-approved design in
`docs/internal/architecture/ADR-20261001-mail-live-access-pooling-folder-discovery-and-cache.md`
(cited below as "the ADR"). The correction round applies the grill report
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-2.md` (findings F-1…F-12) and the
landing-order interface register (`docs/internal/specs/mail-live-access-landing-order.md`, §2) row by
row; the correction record and its evidence table are §18. The Round 2 grill
(`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill2-w3.md`) returned **PASS WITH
FINDINGS** (3 Important, 4 Minor, 0 Critical); this final fix round applies all seven and re-aligns the
contract sections to the landed W0 wave (commit `5f23ae8a0` on this branch, verified in this checkout) —
the Round-2 record is also §18. Still not gated (the feature-size 5-reviewer gate) or approved for
implementation.
**Input**: the ADR (one prescribed grill round applied — findings I-01…I-06, M-01, M-02; founder answers
Q1=A, Q2=B, Q3=A, Q4=A, Q5=A recorded 2026-10-02) and its review
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-report.md`; the spec-set grill
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-2.md`; the founder rulings of
2026-10-02 in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/mail-feature-decisions.md`
(Q-A cache exclusion in product terms, Q-B watcher purge, Q-C refresh stale-gating, Q-D search fields,
Q-E saved-file resource policy).
**Package mapping** (landing-order register §2 preamble — one line per file, binding for every
dispatch): this file is **w3 = ADR-W3 (panel/settings)**. The sibling files are: **w1** =
`mail-live-access-w1-read-runtime-spec.md` (ADR-W1 read runtime), **w2** =
`mail-live-access-w2-discovery-and-cache-spec.md` (ADR-W2 discovery/cache), **w4** =
`mail-live-access-w4-attachments-and-rendering-spec.md` (ADR-W7–W10 features — the Save service, the
agent tools and the Library renderer/resource-policy work of the ADR's W8 rows), **w5** =
`mail-live-access-w5-integration-and-privacy-spec.md` (ADR-W4 integration/privacy), **w6** =
`mail-live-access-w6-proof-spec.md` (ADR-W5 proof). **W0 is a role, not a spec file**: backend-lead in
the ADR's W0 contracts wave (register §5) — no `mail-live-access-w0-*` file exists or is required.
Where this spec says "W4/W7/W8/W9/W10" in ADR-letter terms, the owning spec file is w4 unless the row
names w5 or w6; ownership boundaries stay as the ADR's package table draws them. Interface ownership
authority is the landing-order register §2; this spec never re-implements another row's interface.
**Sibling specs this one must not edit**: w1, w2, w4, w5 and w6 as listed above, plus
`mail-live-access-landing-order.md` itself.
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
validates them with an explicit live refresh **only when the data is absent or older than five minutes**
(at most one live request per eligible event; manual Refresh and the panel's own successful actions always
refresh — founder ruling Q-C, restoring the ADR's P1.1 stale-gating), distinguishes the
four data sources honestly, pages 25 rows at a time to a 200-row ceiling with a reachable search path
beyond it, exposes the existing per-mailbox Sent/Drafts folder-name settings, tells the gateway when a
panel is open through authenticated presence frames, and hands attachments to the Library viewer as
temporary, read-only, resource-constrained previews with a fully specified keyboard and screen-reader
contract.

In scope (all frontend, in the ADR's W3-owned files unless a boundary says otherwise):

- Cache-first display and event-driven freshness for the folder rail, the message list and the reading
  pane — replacing the D25 30-second panel refetch (the ADR's Authority table supersedes it).
- Freshness labelling and semantics for the four sources (`live`, `memory`, `encrypted_disk`, `none`),
  each with a nullable last-validated time and a stale flag — in Phase 1 `encrypted_disk` reaches the
  panel only as `MailFolder.mapping_metadata` (the landed `contracts/components/schemas/MailReadMetadata.yaml`
  scoping: headers and counts are memory-only in Phase 1); unknown count never renders as zero.
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
  acceptance criteria; the renderer-side implementation belongs to the ADR-W8 renderer owner's files,
  specified in the w4 file (header package mapping).
- Every user-visible state of the above with its exact text.

Out of scope for this spec (owned elsewhere; boundaries in §3):

- Everything behind the gateway: pooling, budgets, the cache itself, discovery, the publication revision,
  the preview byte endpoint, the Save service — the w1 and w2 files for runtime/discovery/cache; the w4
  file's Save-service and agent-tool rows (ADR-W7/W10); the w5-integration file's gateway rows (ADR-W4).
- The Library viewer's internal temporary-source implementation, renderer adapters and resource resolver
  (ADR-W8's renderer work, specified in the w4 file per the header's package mapping) — this spec
  publishes the handoff descriptor and the interaction contract both sides honour.
- Contract YAML edits and regeneration (backend-lead in the ADR's W0 role, Wave B — register rows 1–8;
  Hard Constraint #8) — **that wave has landed** on this branch (commit `5f23ae8a0`, verified in this
  checkout). This spec names the landed generated types W3 consumes by their schema names; the two
  scheduled nullability amendments it still depends on (`MailFolder.total`,
  `MailMessageSummary.date`/`MailMessage.date`) are named where they bite, never hand-written around.
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
| `src/lib/api/mail.ts` :: `fetchMailFolders`, `fetchMailMessages`, `fetchMailMessage`, `markMailSeen`, `fetchMailAttachment`, `fetchMailSummary`, `sendMailMessage`, `saveMailDraft`, `sendMailDraft`, `discardMailDraft`, `mintMailHtmlPreviewToken`, `mintMailSignaturePreviewToken`, `mailUidRef` | The single mail REST client. Every response validates through a generated Zod schema (`./generated/schemas`); query params today are `limit`, `before_uid`, `retry` (the human-dial marker). | Add `mode=cache_first\|live`, `observer_id` (folder/list reads — register row 6), `refresh_mapping` (folder reads), the search param (register row 4; fields per founder Q-D), and the new generated calls (`MailAttachmentPreviewRequest`, `MailAttachmentSaveRequest`, `MailReplyContextRequest`). Response shapes stay generated-only. | Consumers: `MailPanel.tsx`, `MailPreviewPane` consumers via props, `EmailMailboxPanel.tsx` (signature mint only), and the `@/lib/api` barrel re-export. Guarded by `src/lib/api/mail.message-ref.test.ts` and `mail.retry.test.ts` — both must keep passing; `retry` semantics do not change. |
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
| `MailFolderList`, `MailFolder` | `src/lib/api/generated/`, from `contracts/components/schemas/MailFolder.yaml` | Landed (`5f23ae8a0`, re-verified this checkout): `folders[]` of exactly three slugs (`inbox\|sent\|drafts`); `unread_count` nullable, Inbox only; `availability` (`present\|absent\|unknown`), nullable `uidvalidity`, five-value `mapping_source`, and the two `MailReadMetadata` objects `mapping_metadata`/`count_metadata` all landed (optional on the wire until the discovery producer emits them). **`total` is still required non-nullable** at the landed commit — the unknown-count nullability is the wave author's scheduled amendment (contracts-wave-check F2), so `total: null` has no wire representation until it lands. |
| `MailMessagePage`, `MailMessageSummary` | from `MailMessagePage.yaml` / `MailMessageSummary.yaml` | Landed: the page gains `next_cursor` (nullable opaque string), `has_more`, `view_limit_reached` and `metadata` (a `MailReadMetadata`), each optional on the wire until the cursor/cache producers land; the legacy `truncated`/`next_before_uid` are retained and marked superseded until the panel's consumer migration removes them. The summary gains `has_attachments` and `message_ref`; **`date` is still required non-nullable** at the landed commit — the nullable-`date` amendment is scheduled with the `total` one (contracts-wave-check F3), so the F6 "No date" state cannot be served until it lands. The messages-read `limit` param's prose still reads default-20/clamp-100 while `search` carries the 25/200 bound (contracts-wave-check F4) — the panel therefore always sends `limit=25` explicitly and never relies on the server default. |
| `MailMessage`, `MailAttachment` | from `MailMessage.yaml` / `MailAttachment.yaml` | Landed: the detail carries `attachments[]` (`part_index`, `filename`, `content_type`, `size_bytes` non-null) — the reading pane's rows — plus `message_ref` and `has_attachments` (register row 8's fields). The detail carries **no** metadata object of its own: the ADR gives mapping, counts and page headers their own and no others, so the reading pane's freshness context rides the page metadata of the list read that produced the view. |
| `MailSummaryList`, `MailboxNewMailSummary` | from the summary schema | Watcher banner: `watcher_state`, `last_error_class`, `next_attempt_at` — stays as is. |
| `Mailbox`, `MailboxConfigureRequest` | from `Mailbox.yaml` / `MailboxConfigureRequest.yaml` | `sent_folder_name` / `drafts_folder_name` optional strings; backend `restAPISetAgentMailbox.persistConfig` preserves omitted values and clears explicitly-empty ones (the ADR's E-Overrides correction) — the frontend just needs the fields visible. |
| `MailUnavailableError` (503) | from `MailUnavailableError.yaml` | Landed: `code: busy\|backoff`, `last_error_class`, `next_attempt_at`, plus `reason` (`pool_busy\|account_busy\|server_connection_limit\|backoff`) — `contracts/components/schemas/MailUnavailableError.yaml::reason`, regenerated. |
| `LibraryEntry` | from `LibraryEntry.yaml` | Landed: requires a real workspace-relative `path` — the reason the temporary source must not fabricate one (the ADR's F1 viewer-input row) — and now carries `preview_profile: workspace\|mail_restricted` and the per-file `preview_scripts_allowed` field (founder Q5=A) — `contracts/components/schemas/LibraryEntry.yaml::preview_profile`, `::preview_scripts_allowed`. |
| WS `ClientFrame` / `ServerFrame` | `src/lib/ws.ts` re-exports from generated | Landed: `contracts/asyncapi.yaml` carries `MailPanelObserverFrame` (`action: open\|close`, `observer_id`, `workspace_id`), the `mail_panel_observer_ack` and `mail_panel_observer_error` frames, and three new `WsFrameType` entries; `library_changed` remains the save-notification reuse path. |

### 2.4 Contract shapes the W0 wave landed (register §2 rows; commit `5f23ae8a0` on this branch)

There is no W0 spec file: **W0 is backend-lead in the ADR's contracts wave (Wave B)** — the register
rows were its queue, and the wave has landed on this branch (commit `5f23ae8a0`; `make verify-contracts`
green, independently re-run by the wave CHECK
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/receipts/contracts-wave-check.md`). The rows
below name the landed shapes W3 consumes; W3 never creates a stub or a second declaration of any of
them. Two nullabilities the register rows name are **not yet in the landed shapes** — the wave author's
disclosed amendment schedule carries them, and this spec flags each where it bites.

**Which metadata instance feeds which panel surface (the freshness model; grill Round 2 R2-I3).** The
landed shapes carry **separate** `MailReadMetadata` instances — a validation of one surface is not a
validation of another (the ADR's freshness-metadata row; the landed
`contracts/components/schemas/MailReadMetadata.yaml` description). W3 renders exactly three of them,
and no others exist for it:

| Landed instance | Feeds |
|---|---|
| `MailFolder.mapping_metadata` | The folder-name/role states (§11 S-7/S-8) and the rail's role display — **the only surface that can carry `source=encrypted_disk` in Phase 1** |
| `MailFolder.count_metadata` | The rail's count markers and their unknown "—" presentation |
| `MailMessagePage.metadata` | The message-list freshness line (§11 S-3…S-6) and the reading pane's freshness context — the detail read (`MailMessage`) carries no metadata object in the landed shapes; the ADR gives mapping, counts and page headers their own and no others |

In Phase 1 `encrypted_disk` reaches the panel only through `mapping_metadata` (the landed
`MailReadMetadata.source` description scopes it so: headers are memory-only in Phase 1, counts are
memory-only per ADR P1.1). A list or count fixture carrying `encrypted_disk` is not constructible from
the landed shapes and no producer sends it — D-1 row 4 and U11 are scoped accordingly (§8.6/§8.3). The
`source` enum's *definition* stays W0's (register row 2); W3 consumes the generated union
(`MailReadMetadataSource`) and tests only the values this table names per surface.

| Landed generated shape (register row) | Producer / owner | W3 consumes it for |
|---|---|---|
| `MailReadMetadata` — **landed** at `contracts/components/schemas/MailReadMetadata.yaml`: `source` (generated 4-value union `MailReadMetadataSource`: `live\|memory\|encrypted_disk\|none`), nullable `last_validated_at` (RFC 3339), `stale: boolean`, `refresh_needed: boolean`, nullable closed-enum `notice_code` (`cache_unavailable`), nullable opaque `publication_revision`; all six required | W0 defined (row 2); **w2 is the only value producer**; w5-integration attaches/advances in responses | Freshness labels (US-2); superseded-response dropping (US-1); consumed through the three instances named above |
| `MailFolder` role fields — **landed** at `contracts/components/schemas/MailFolder.yaml`: `availability: present\|absent\|unknown`, nullable `uidvalidity`, `mapping_source: override\|special_use\|fallback\|saved\|none`, `mapping_metadata` + `count_metadata`. **`total` is still required non-nullable** at the landed commit; the unknown-count nullability (the ADR's "make `total` nullable") rides the wave author's scheduled amendment (contracts-wave-check F2) — until it lands the `total: null` rail state (US-2 AS-5, MC-W3-4) has no wire representation, and the panel work that renders it gates on that amendment, never on an improvised sentinel | W0 defined (row 3). **Five `mapping_source` values**: the register adopts w2's producer enum, superseding the ADR's four-value proposal — the fifth value `saved` is the unflagged contract delta the first grill missed | Rail states (US-2, US-4); the `saved` value renders as an ordinary override (dataset **D-3**, the register row 3 five-value enum) |
| Paging/search — **landed**: `contracts/components/schemas/MailMessagePage.yaml` (`next_cursor` nullable opaque string, `has_more`, `view_limit_reached`), the messages-read operation's `search` and `cursor` params, and the typed 409 at `contracts/components/schemas/MailStaleReferenceError.yaml` (`code: stale_cursor\|stale_reference`, wired on the list/detail/seen/download/save operations). Panel default/max 25; search fields per founder Q-D=A: subject plus sender/recipient substring, server-side header search, within the 25/200 bounds. Two landed prose items await their scheduled amendments and W3 reads them accordingly: the messages-read `limit` description still reads default-20/clamp-100 while `search` carries the 25/200 bound (contracts-wave-check F4) — **the panel always sends `limit=25` explicitly**, never relying on the server default; and the `search` description's "founder decision Q-D pending" sentence predates the Q-D answer — Q-D=A is decided (2026-10-02, `mail-feature-decisions.md`), this spec's fields stand, and the stale prose is not an open question (contracts-wave-check F5) | Shapes: W0 (row 4). **Implementation**: w2 implements the IMAP SEARCH and cursor issuance in its view file (§3.13 of its spec) — W3 builds neither | US-3 |
| Preview/save/reply — **landed**: `contracts/components/schemas/MailAttachmentPreviewRequest.yaml` (`workspace_id`, `agent_id`, `folder`, `message_ref`, `part_index`) / `MailAttachmentPreviewResponse.yaml` (`kind: mail_attachment`, `preview_id`, `subject`, `attachment`, `text_readable`, `content_source` with `byte_url`, `read_only` const-true — metadata-only, no payload, the I-05 correction); `MailAttachmentSaveRequest.yaml` (required `save_operation_token`, M-02) / `MailAttachmentSaveResponse.yaml` (`saved` const-true, real `entry: LibraryEntry`, `path`, `absolute_path`, `size_bytes`, `audit_status: recorded\|disabled\|failed`, nullable `warning_code`); `message_ref` landed on `MailMessageSummary.yaml` and `MailMessage.yaml` (I-03) | W0 (rows 8/22). `message_ref` is **minted by w5-integration's gateway handlers**; same-lease epoch/generation validation is w1's published capability plus w2's normative validation rules (row 16) — W3 carries it opaquely. The bounded prior-receipt reconciliation service is the w4 file's ADR-W7 implementation | US-6 (Open handoff and Save branch), §11 states S-15/S-16/S-17. The server-side part fetch behind the mint is w2's targeted single-part reader (row 14) — invisible to W3 |
| `MailReplyContextRequest` / `MailReplyContextResponse` — **landed** (`to`, `cc`, `bcc`, `subject`, `body_markdown`, `in_reply_to`) | W0 (row 8) | F5 reply/quote prefill |
| `MailMessageSummary.has_attachments: boolean` — **landed**, required, on the summary and the detail. **`date` is still required non-nullable** on both shapes at the landed commit; the F6 "No date" state rides the same scheduled amendment as `total` (contracts-wave-check F3) — until it lands the formatter change gates on the amendment, never on a sentinel date | W0 (row 8). The flag is **derived by w2's MIME structure classifier** (row 15) — W3 renders it and never derives it panel-side; date normalization is w1/w2's | Paperclip indicator; **No date** (post-amendment) |
| AsyncAPI — **landed**: `contracts/asyncapi.yaml` `MailPanelObserverFrame` (`action: open\|close`, `observer_id`, `workspace_id`), `mail_panel_observer_ack`, `mail_panel_observer_error`, three new `WsFrameType` entries, bound to the authenticated socket | W0 (row 5, step 3); the gateway handlers that bind/tear down observers are **w5-integration's** (rows 5/11) | US-5 (presence) |
| `MailUnavailableError.reason` (`pool_busy\|account_busy\|server_connection_limit\|backoff`) — **landed**; `LibraryEntry.preview_profile: workspace\|mail_restricted` + `preview_scripts_allowed` (Q5=A) — **landed** | `reason`: W0 (row 7, step 4); w5-integration maps the distinct values w1 supplies. `preview_profile`/allowance: W0 (row 8); the marker/allowance storage and its survival proof is the w4 file's ADR-W7 work | Busy copy; US-9 |

**Sequencing rule (Hard Constraint #8, as ruled by the register §5 deadlock ruling) — satisfied for
W3's inputs**: W3 consumes generated types only. The contracts wave ran and landed on this branch
(commit `5f23ae8a0`): schemas and the regenerated `src/lib/api/generated/` / `pkg/api/generated/`
artifacts committed atomically, `make verify-contracts` green. W3's wire-consuming code consumes the
regenerated types from here on. Two branch-level items remain outstanding and are tracked, not worked
around: the `total`/`date` nullability amendment (rows 2 and 6 above), and the five gateway
consumer-compile sites the wave disclosed — the branch stays unpushed until the owning waves adapt
those sites (wave CHECK finding F1; the gateway Mail routes are w5-integration's). Neither is W3's to
fix, and neither licenses a hand-written parallel type. The one inline-discriminated-union exception
(OpenAPI-hosted `oneOf`) is W0's concern, not W3's.

---

## 3. Interfaces published and consumed (parallel-build freeze)

These are the boundaries the sibling packages build against **without editing each other's files**. A
change to any Published row goes back through this spec and W0's contract set, not through a side edit.

### 3.1 W3 publishes

Per the landing-order register, W3 is the single publisher of exactly two interfaces (register rows 20
and the panel half of row 21's alignment). Each row below states the exact shape and the freeze point; a
consumer binds to the frozen shape and never re-implements it.

| Interface | Consumer | Frozen shape | Freeze point |
|---|---|---|---|
| **SPA presence lifecycle hooks** (register row 20 — W3 is the single publisher) | w5-integration (gateway binding), w1 (socket retention) | The SPA emits a `mail_panel_observer` frame with exactly the keys `{ type, action, observer_id, workspace_id }` — `action: open` when a Mail panel becomes visible with a resolved workspace, `close` on §4 US-5's lifecycle events; `observer_id` is a fresh opaque value per panel instance per connection. REST folder/list reads that opt into panel semantics carry the same `observer_id` as the query param of register row 6 (the param's schema is W0's, not re-declared here). Until the gateway acknowledges an open, W3 assumes nothing is retained (request-scoped behaviour is always correct). | Presence frames regenerated (register row 5, Wave B, landed `5f23ae8a0`); ships in Wave C. |
| **Cache-view semantics** (register row 21 — the panel-side alignment; w2 owns the normative freshness rule) | w2 (rule owner), w6 (measurement arms), qa-lead (panel tests) | The panel renders cache-first rows immediately and performs **at most one** `mode=live` request per eligible event, **and on panel open and folder switch it issues that live request only when the folder's data is absent or its last validation is older than five minutes** (the ADR P1.1 stale-gating; founder ruling Q-C — the design's conditional rule stands, superseding this spec's former "exactly one live per event, even when fresh" wording). **Precedence (grill Round 2 R2-M3): the panel-local absent-or-older-than-five-minutes computation governs *issuing*; a response's `stale`/`refresh_needed` flags govern *presentation* — the label and the checking indicator — never issuance. If the server flags and the local computation ever disagree (for example clock skew), the local rule decides whether a request is sent and the flags decide what the label says.** Manual Refresh always refreshes; the panel's own successful actions always refresh the affected folder. Every refresh is keyed by `publication_revision`: a response older than the newest revision the panel has applied is dropped unrendered. Eligible events: panel open with a mailbox resolved, folder switch, manual Refresh, own successful action (mark-seen, send, draft save/discard), one rediscovery after missing-folder. No repeating panel timer exists. | Recorded in this correction round; built in Wave C against w2's Wave-C freshness rows. |
| **Handoff trigger** — Mail → Library temporary viewer | ADR-W8's renderer work (w4 file) | W3 calls one seam in the Library mount with a payload of generated types only: `{ source: MailAttachmentPreviewResponse, context: { subject: string, returnFocus: { kind: 'attachment-action' \| 'message-row' \| 'folder', id: string } } }`. W3 guarantees the descriptor is freshly minted per Open, carries no workspace path, and is dropped on exit. The Library side guarantees the context bar (exact strings §11) and the focus/announcement contract of §4 US-7. | Frozen here (the ADR's "freeze the handoff callback with W3" row); changes return through this spec and the W0 wave, never a side edit. |
| **Return path** — Library temporary viewer → Mail | W3 (consumed by the Library side via callback) | `onBack(): void` — the Library side calls it for Back/Escape; W3 then decides the landing surface per §4 US-7 AS-3 (fresh live read of the originating message if still available; changed/deleted notice otherwise). The Library side never re-opens Mail's queries itself. | Same as the handoff trigger. |
| **Save handoff** | ADR-W8's renderer work (w4 file) | W3's handoff module exposes `saveToLibrary(descriptor)` (wraps the generated `MailAttachmentSaveRequest` + `save_operation_token`) and a status channel (`loading \| saved \| failed(reason) \| unknown`) the context bar renders. Only a `saved` status enables **Open in Library**. The token and response shapes are W0's (register row 22, landed `5f23ae8a0`); the bounded prior-receipt reconciliation service is the w4 file's ADR-W7 implementation — W3 publishes only this SPA-side wrapper and status contract, never a second lookup. | Shapes landed in Wave B (register row 22); this SPA contract frozen here. |

### 3.2 W3 consumes

Every row names the **single publisher** the landing-order register assigns. W3 consumes frozen shapes;
it never re-implements, parallel-types, stubs, or quietly extends any of them (register rule R-1/R-4).

| Interface | Publisher (register row) | Dependence |
|---|---|---|
| Generated REST/WS types listed in §2.3/§2.4 | backend-lead in the ADR's W0 role, Wave B (row 1) — **landed, commit `5f23ae8a0`** | Wave B's regeneration has merged on this branch; W3's frontend work consumes the regenerated types and lands in parallel with the backend consumers of the same types. The branch's disclosed red gateway-build window (five compile sites) stays w5-integration's consumer adaptation (wave CHECK finding F1) — never W3's, and never a reason to hand-write a type. |
| `mode=cache_first\|live`, `search`, `cursor`, `next_cursor`/`has_more`/`view_limit_reached`, typed stale-cursor 409 (`MailStaleReferenceError`) | Shapes: W0, landed (row 4). Implementation of the folder-scoped server search and cursor issuance: **w2**, in its view file (§3.13 of its spec) | §4 US-3 behaviour; an omitted `mode` stays live for existing callers (backward compatible by contract). Search fields per founder Q-D=A: subject plus sender/recipient substring, server-side header search, within the 25/200 bounds — decided; the landed `search` description's "Q-D pending" prose predates the answer and is not an open question (contracts-wave-check F5). The panel always sends `limit=25` explicitly; it never relies on the server default while the landed `limit` prose still reads default-20/clamp-100 pending its amendment (contracts-wave-check F4). W3 builds none of this; it consumes the shapes and renders the states. |
| REST `observer_id` query param on folder/list reads | W0 schemas it; w5-integration's §8 step 2 requested it (row 6) — **landed on both read operations** | §4 US-5's panel-semantics reads. Without the param every panel read stays request-scoped and the founder's warm-pool behaviour never activates — W3 sends it on every read that opts into panel semantics. |
| `MailReadMetadata` on the folder/list responses — through the three landed instances (`MailFolder.mapping_metadata`, `MailFolder.count_metadata`, `MailMessagePage.metadata`; §2.4's mapping table) | W0 defined (row 2); **w2 is the only value producer**; w5-integration attaches/advances in responses | §4 US-2 labels. The landed instances are optional on the wire until their producers land (the is_knowledge_base precedent, stated on each landed field): a response arriving without metadata in that window renders as the unknown/stale presentation (US-2 AS-7's rule — never "just checked", never a fabricated zero), and once the producers land, a missing metadata object is a build-order violation again — no hand-written compat type either way. The `source` enum scoping is W0's (register row 2, adr-grill-4 m4); W3 consumes the generated union and tests only the per-surface values the §2.4 mapping table names. |
| Temporary-source mount + context bar + resource policy enforcement | ADR-W8's renderer work, specified in the w4 file (header mapping) | §4 US-6/US-7/US-8 acceptance criteria are joint; W3's journey tests treat the mount as the system under test's counterpart. |
| Targeted single-part reader (`pkg/email/attachment_parts.go`) and the MIME structure classifier deriving `has_attachments` | **w2** (rows 14/15 — new owner per the register; the ADR's feature-extension row assigns both to W2) | W3 consumes only the generated outputs: `has_attachments` on summaries and the minted preview response on Open. It derives neither, stubs neither, and asserts them only as generated inputs. |
| `message_ref` on list/read results | **w5-integration mints it** in the gateway handlers; same-lease epoch/generation validation is w1's published capability plus w2's normative validation rules in its view file (row 16) | §4 US-6's Open handoff carries the reference opaquely from a list/read result into the mint request. W3 never constructs, validates, or re-issues it. |
| Instrumentation emitter (per-operation record) | Shape frozen by w6; emission obligations on w5-integration (envelope), w1 and w2 (sub-fields) (row 17) | **None.** W3 defines no emitter and asserts no instrument records; W3's tests observe request counters and DOM state only. This row exists so no panel test is ever written against an emitter no package built. |
| Raw-error redaction (`pkg/email/watcher.go::recordFailure`; the gateway's `mailErr502`) | **w1** fixes `recordFailure` in its own file; **w5-integration** fixes `mailErr502` (row 19) | W3 renders only sanitized class strings (§11 S-6/S-10) and never raw server error text; no panel surface depends on the raw payload surviving. |
| Disk-cache exclusion / provenance gate | w5-integration publishes the gate decision; **w2 enforces it at the first write**; condition unified to the stricter form (row 18) | W3's only surface is the `cache_unavailable` notice (§11 S-12). Founder ruling Q-A: the **product owns** its cache directory's exclusion from data-directory staging and from the application's own backups/archives on every install, by its own means — no personal machine-setup dependency exists anywhere in this feature; the notice reports a genuine runtime failure only and never excuses a missing product-owned exclusion. |
| `library_changed` notification | existing | Save success lets the normal Library change notification refresh lists; no second refresh protocol. |
| Authenticated `WsConnection` + gateway presence handlers | Socket client: existing. Presence frames: W0 (row 5) — **landed**. Gateway handlers that bind/tear down observers: **w5-integration** (rows 5/11 — w1 publishes the registry, w5 consumes it) | Presence frames only; no new heartbeat, no cache-push frame (the ADR forbids one). The shared socket adapter hookup is traced with w5-integration — one writer — before the presence wave dispatches. |

### 3.3 Ownership guard-rails (from the ADR's package table, binding here)

- The ADR-W8 renderer owner never edits `MailPanel.tsx`, `src/lib/api/mail.ts`, or any mail-owned file;
  W3 never edits `LibraryExplorer.tsx`, `LibraryPreviewPane.tsx`, or `src/components/library/**`.
- The socket adapter file (`src/lib/ws.ts`) gets one writer — traced with w5-integration before the
  presence wave dispatches.
- qa-lead owns every test-file edit, including rewriting the D25-era oracle tests this spec retires.

---

## 4. User stories and acceptance criteria

Priorities: P0 = the feature is dishonest or unusable without it; P1 = required for the founder-approved
scope; P2 = required for coherence, deferrable only with a tracked issue.

### US-1 — Cached rows immediately, refreshed only when stale (P0)

A user opening Mail should see their messages straight away from the panel's cached view instead of
staring at a spinner, and the panel should check the server once for fresher data **when the cached data
is absent or older than five minutes** — not repeatedly, not again seconds after a check, and not at all
while the panel is closed (founder ruling Q-C: the ADR P1.1 stale-gating stands; a prior draft's
"exactly one live per event, even when fresh" wording is superseded).

Why this priority: instant labelled display is the ADR's central promise (its "Visible cached refresh
shape" recommendation); without the stale-gated refresh rule the panel can either hammer the server or show a
finished-looking refresh that returned the same stale page.

Independent test: with the gateway's cache endpoints stubbed, opening the panel issues exactly one
cache-first read per event, plus **exactly one live read when the cache is absent or older than five
minutes and zero live reads when it is younger**, and renders the cached rows before a live read
settles.

Acceptance scenarios:

1. **Given** a mailbox whose folder metadata and headers are cached with a last-validated time older
   than five minutes (or no cache at all), **When** the user opens the Mail panel on that mailbox,
   **Then** rows render from the cache-first response immediately (no full-list skeleton), the panel
   issues exactly one `mode=live` refresh for that open event, and the rows update in place when it
   settles.
2. **Given** the panel is open on a folder, **When** the user switches to another folder whose cached
   data is absent or older than five minutes, **Then** the cache-first-then-one-live sequence runs for
   the new folder (US-1 AS-8's zero-live rule applies when that folder's cache is younger), and the
   previous folder's in-flight live refresh, if any, is dropped rather than rendered into the wrong list.
3. **Given** the panel is closed, **When** the watcher cycles and new mail arrives, **Then** the panel
   performs no folder, count or header request until the next eligible event (badge polling continues
   against the saved-state summary endpoint only).
4. **Given** a cache-first response returned `source=none` (no cache), **When** the panel renders,
   **Then** it shows the loading state and the one live refresh populates the list; `source=none` is
   never displayed as an empty mailbox.
5. **Given** the live refresh for an event is still in flight, **When** the user triggers another
   eligible event for the same folder (for example presses Refresh again), **Then** the newest event's
   request supersedes the old one and only the newest response is rendered — a response captured under
   an older `publication_revision` is dropped even if it arrives last.
6. **Given** the user performs a successful own action (marks a message read, sends, saves a draft),
   **When** the action settles, **Then** exactly one refresh for the affected folder follows, and no
   other folder refreshes.
7. **Given** any panel state, **When** 30 seconds elapse repeatedly, **Then** the folder rail and the
   message list issue no timer-driven requests (the D25 cadence is gone); the watcher banner's
   saved-state summary poll is unchanged.
8. **Given** a folder whose cached data was validated less than five minutes ago, **When** the user
   opens the panel on it or switches to it, **Then** no `mode=live` request is issued for that event,
   the rows render immediately with their fresh label, and a subsequent manual Refresh still refreshes.

### US-2 — Honest freshness: four sources, stale labels, unknown never zero (P0)

A user must be able to tell whether the rows on screen were just confirmed with the server, came from a
cache, or are not cache-backed at all — and a folder whose size the panel does not know must never look
empty.

Why this priority: the ADR makes "a cache hit is never presented as a settled server check and an unknown
count is never shown as zero" a hard display rule; the folder rail's current code renders
`unread ?? 0` and a bare `total`, which violates both halves today (Verified:
`src/components/workspaces/mail/MailFolderRail.tsx::MailFolderRail`).

Independent test: feed the view state the four `MailReadMetadata.source` values and null/non-null
counts; assert the exact label text and that null counts render the unknown marker.

Acceptance scenarios:

1. **Given** a response with `source=live`, **When** rows render, **Then** the freshness line reads
   "Checked just now" (or the validated time) with no stale marker.
2. **Given** a response with `source=memory` or `source=encrypted_disk` and `stale=false`,
   **When** rows render, **Then** the freshness line reads "Checked <relative time>" — for example
   "Checked 2 minutes ago" — without implying a live check.
3. **Given** `stale=true` and `refresh_needed=true`, **When** the cache-first rows render and the one
   live refresh runs, **Then** the freshness line reads "Last checked <relative time> · Checking…",
   the existing rows stay visible (no skeleton replaces them), and the line resolves to US-2 AS-1 or
   US-2 AS-4 when the refresh settles.
4. **Given** the live refresh fails while cached rows are displayed, **When** the failure settles,
   **Then** the stale rows remain, the line reads "Couldn't refresh — showing messages as of <time>.",
   a Retry control is present, and the last-validated time did not advance.
5. **Given** a folder response whose `total` is `null` (unknown), **When** the rail renders, **Then**
   the count slot shows "—" and never `0`; the same applies to a null inbox `unread_count`, which
   renders "—" instead of the current bare zero (the legitimate zero count keeps rendering `0`).
6. **Given** metadata carrying `notice_code=cache_unavailable`, **When** rows render, **Then** a
   dismissible notice reads "Mail cache unavailable; using live access." and the rows are live-sourced.
7. **Given** `last_validated_at` is `null` in any source, **When** rows render, **Then** the panel
   treats freshness as unknown (the stale presentation), never as "just checked".

### US-3 — Paging to 200 with a reachable search path (P0)

A user with a large folder reads the newest messages immediately, loads more in steps, and can always
reach older messages through search — never a dead-end instruction.

Why this priority: today's panel renders one default page with no way forward (Verified:
`MailPanel.tsx` passes no paging params and `MailMessageList` renders one array); the ADR names the
dead-end "search instead" instruction as explicitly not completion.

Independent test: stub paginated responses of known sizes; walk 25 → 50 → 200 and assert the Load more
control, the ceiling behaviour, and that the search affordance appears and returns rows.

Acceptance scenarios:

1. **Given** a folder with more than 25 messages, **When** the first page renders, **Then** exactly 25
   rows appear and a "Load more" control is present below the list.
2. **Given** 50 rows displayed with more available, **When** the user activates "Load more", **Then**
   25 more rows append (75 total) and previously loaded rows are preserved unchanged.
3. **Given** 200 rows displayed, **When** the ceiling is reached, **Then** "Load more" is replaced by
   the message "You're viewing the newest 200 messages. Search to find older ones." with the search
   control focused or directly adjacent, and no further browse request is issued.
4. **Given** the ceiling state, **When** the user searches, **Then** a folder-scoped search runs live
   (never from cache — matching subject plus sender/recipient substrings, server-side, within the
   25/200 bounds; founder Q-D=A), results render in the same 25-per-page / 200-ceiling discipline with
   their own "Load more", and an exit ("Back to <folder>") restores the browse view.
5. **Given** a search with no matches, **When** it settles, **Then** the list area reads
   `No messages match "<query>".` and the browse view remains one control away.
6. **Given** a stale or foreign cursor (409 typed result), **When** any paging action settles with it,
   **Then** the panel resets the view to the folder's first page with the notice
   "The folder changed. Showing the newest messages." — it does not spin, retry silently, or replay the
   cursor.
7. **Given** search or older-page results, **When** the user leaves the view (folder switch, panel
   close, message navigation that discards the view), **Then** the working set beyond the reusable
   newest-page cache is released and the next open starts from the cache-first read again.

### US-4 — Folder names: visible overrides and honest "automatic" (P1)

A user whose server names its folders unusually can set the Sent/Drafts folder name per mailbox, and
"automatic" means exactly what it does.

Why this priority: the configuration seam already exists end-to-end except for the UI (Verified in
§2.2); the ADR requires the override to be visible and "automatic" to be spelled out.

Independent test: open the mailbox settings for a configured mailbox; assert the two fields, the
automatic semantics on empty, and that a saved name round-trips through the existing save path.

Acceptance scenarios:

1. **Given** a configured mailbox in the Connectors email panel, **When** the user opens its settings,
   **Then** "Sent folder name" and "Drafts folder name" fields are present, optional, and show the
   saved value or the placeholder "Automatic".
2. **Given** either field, **When** the user leaves it empty and saves, **Then** the override is
   cleared (the backend's existing clear-to-automatic behaviour applies — no extra checkbox is added)
   and the helper text explains it: "Leave empty to find the folder automatically."
3. **Given** a non-empty saved name, **When** the settings render, **Then** the name is treated as a
   deliberate override and the helper text says so: "Uses this exact folder name on your mail server."
4. **Given** the folder rail with `mapping_source=override` and `availability=unknown` (the named
   folder cannot be found), **When** the role renders, **Then** the rail shows the unresolved state
   (§11 state S-8) and the settings panel shows an actionable warning naming the field — the override
   is never silently ignored.
5. **Given** a mailbox whose stored name predates this feature and whose intent is unprovable,
   **When** settings render, **Then** the stored name shows as the field value (an override) until the
   user clears it to Automatic — the panel never discards it on the user's behalf.
6. **Given** a confirmed-absent role (`availability=absent`), **When** the rail renders, **Then** the
   role stays visible with "No messages" and the explanation of §11 state S-7; it is never reported as
   an error, and an unknown role (S-8) is never reported as "the server has no such folder".

### US-5 — Panel presence: the gateway knows when Mail is open (P0)

The gateway may keep ready connections only while a real panel is open; the panel must therefore report
open and close on the authenticated connection, survive tab churn honestly, and never let one tab's
state leak into another's.

Why this priority: socket-retention rules (two-minute idle, last-observer close) depend on presence;
without the frames the gateway must assume the panel is always closed — correct but slower — or worse,
retain sockets for a closed panel.

Independent test: run the presence adapter against a stub socket; drive open/close/navigation/logout
events; assert frame sequences and observer identity.

Acceptance scenarios:

1. **Given** the user opens the Mail panel with a workspace resolved, **When** the panel mounts,
   **Then** one `mail_panel_observer` open frame is sent with a fresh opaque `observer_id` and the
   workspace id — and nothing else: no user id, session id or mailbox id travels as identity.
2. **Given** an open observer, **When** the user closes the panel (tab-strip close, shell close,
   navigation away from the workspace), **Then** a close frame for that same `observer_id` is sent.
3. **Given** an open observer, **When** the browser tab closes or navigates away without a close frame,
   **Then** the connection drops and the gateway reaps the observer by socket loss — the SPA sends
   best-effort close on `pagehide` but correctness never depends on it.
4. **Given** an open observer, **When** the user logs out, **Then** the socket teardown (existing
   logout behaviour) removes the observer; after re-login the new connection starts with no observers.
5. **Given** the socket drops and reconnects, **When** the panel is still open, **Then** the adapter
   re-sends the open frame with a fresh `observer_id` after the connection re-authenticates.
6. **Given** two browser tabs each with Mail open, **When** one tab closes, **Then** only that tab's
   observer closes; the other tab's retained state is unaffected, and each tab's observer id is distinct.
7. **Given** the panel open without an acknowledged observer (frame lost, socket down), **When** the
   user reads mail, **Then** all reads still work as ordinary request-scoped work — presence is an
   optimization signal, never an authorization or correctness dependency.
8. **Given** the workspace changes while the panel stays mounted, **When** the adapter observes the
   switch, **Then** it closes the observer for the old workspace and opens one for the new workspace
   with the same panel instance's fresh `observer_id` semantics per open.

### US-6 — Attachment rows and the Open handoff (P1)

A user can see which messages carry attachments before opening them, open an attachment straight into
the Library viewer without saving anything, and save it deliberately when they want to keep it.

Why this priority: F1/F7 are founder-settled scope (issues #1170/#1174); the panel is their frontend
half.

Independent test: with the mint endpoint stubbed, clicking Open produces the descriptor handoff with
the exact context bar; Save performs the save call and enables Open in Library only on success.

Acceptance scenarios:

1. **Given** a list response where a row has `has_attachments=true`, **When** the row renders, **Then**
   a paperclip indicator appears with accessible text "Has attachments"; rows with `false` show none.
2. **Given** an open message with attachments, **When** the reading pane renders the attachment rows,
   **Then** each row lists filename and size (nullable size shows "Size unknown", never "0 B") and
   carries three actions: Open, Save to Library, Download — each with a distinct accessible name
   including the filename (the focus-return target for I-06).
3. **Given** the user activates Open on an attachment, **When** the mint settles, **Then** the Library
   viewer shows the temporary preview with the context bar reading exactly
   "From mail: <subject> · Back to mail · Save to Library", outside the rendered content, and nothing
   was written to disk (the no-write proof is W8/W5's; W3's contract is that Open only mints).
4. **Given** a temporary preview open, **When** the user activates "Back to mail" (or Escape),
   **Then** the temporary source is disposed (W8) and Mail shows again with the focus rules of US-7.
5. **Given** a temporary preview open, **When** the user activates "Save to Library" and it succeeds,
   **Then** the context bar announces success (US-7 AS-5), "Open in Library" becomes available, and the
   saved file is a real Library entry (real path, numbered-suffix name) — the temporary view never
   pretends it was already saved.
6. **Given** an attachment over the 25 MB cap, **When** the rows render, **Then** Open and Save are
   unavailable with the cap explanation accessible in place (§11 state S-13), while Download remains
   the browser action.
7. **Given** the save response is lost after a possible commit (transport error), **When** the panel
   shows the outcome, **Then** it shows the "Save result unknown" state (§11 state S-17); it never
   re-sends Save on its own, and an explicit user retry of the same save carries the same
   `save_operation_token` and resolves from the prior receipt.
8. **Given** an Open/mint that fails — the transfer discovers real bytes over the 25 MB cap (the typed
   late-failure error of the ADR's I-05 correction), the message reference went stale between list and
   Open (the typed 409), or the mint call is refused busy (503) — **When** the failure settles, **Then**
   the attachment row shows the matching pinned failure state (§11 states S-26/S-27/S-28), the Open
   control is re-enabled so the user can retry or use Download, no partial preview mounts, and the
   panel never spins silently or dumps a raw error string.

### US-7 — Handoff accessibility: focus, announcements, keyboard, reflow (P0)

A keyboard or screen-reader user can open an attachment, work in the viewer, and come back to exactly
where they were — or be told clearly where they landed.

Why this priority: grill finding I-06; `docs/internal/design/design-system-definition.md::D16` makes
focus restoration, status announcement, keyboard operation and zoom/reflow release requirements, not
preferences.

Independent test: keyboard-only and virtual-focus journeys over the handoff (open → viewer → save
success/failure → back, including the deleted-source case), plus 320 px / 200 % zoom passes.

Acceptance scenarios:

1. **Given** the user activates Open on an attachment row, **When** the viewer mounts, **Then** focus
   moves to the context bar's trusted heading ("From mail: <subject>") — never into the rendered
   content — and a screen-reader announcement states the viewer opened with the attachment's name.
2. **Given** the user returns to mail via Back, **When** the originating attachment action still
   exists, **Then** focus returns to that exact action and the announcement names it; **Given** it no
   longer exists (list refreshed, message moved/deleted, folder changed), **When** the return settles,
   **Then** focus lands on the message's list row if present, else the containing folder tab — each
   with an explicit announcement of where focus landed and why.
3. **Given** a save in progress, an error, or a success inside the viewer, **When** the outcome
   settles, **Then** it is announced through a polite live region without moving focus; success does
   not steal focus to the Library panel.
4. **Given** any disabled stored-file action in the viewer (edit, rename, move, Library download,
   fill & sign), **When** a keyboard or screen-reader user reaches it, **Then** the control is
   discoverable and its explanation "Save to Library first" is readable in place (associated text, not
   a tooltip-only hint) and visible without hover.
5. **Given** keyboard-only use, **When** the user works the context bar, **Then** Back, Save and any
   Retry are reachable in a sensible tab order, Escape returns to mail, and focus never escapes into
   browser chrome or gets trapped in the viewer.
6. **Given** a narrow viewport (320 px width) or 200 % zoom, **When** the viewer renders, **Then** the
   context bar wraps or stacks, and every control and explanation remains reachable and readable —
   nothing depends on hover or a wide viewport.

### US-8 — Temporary-source resource policy (P0)

Content inside a temporary mail attachment may only load resources that belong to that preview —
sender-authored same-origin URLs are not trusted merely because they are local — while ordinary
workspace rendering is untouched.

Why this priority: grill finding I-04; `isDisplayableImageSrc` resolves relative URLs against the
current origin and passes any http/https/data URL (Verified), so a mail Markdown image pointing at a
same-origin Library path would render today's renderer straight into an authenticated workspace
resource.

Independent test: a mail Markdown attachment whose image syntax targets a same-origin Library/API
path, a workspace embed, and a remote URL; request counters assert zero loads, with an ordinary
workspace file as the positive control.

Acceptance scenarios:

1. **Given** a temporary mail Markdown attachment containing `![x](<same-origin Library download URL>)`,
   **When** the viewer renders it, **Then** zero requests are issued to that URL — the resolver never
   yields an authorized target (structural refusal, not a hidden attribute or CSS cover).
2. **Given** the same content patterns aimed at a workspace embed/wikilink target or a remote image,
   **When** the viewer renders, **Then** zero requests issue to any of them.
3. **Given** the preview's own minted resources (its byte/representation responses, CID inline parts of
   the same message routed through the Mail preview prefix), **When** the viewer renders, **Then** they
   load normally.
4. **Given** an eligible remote image and the user's explicit "Load images" consent, **When** the
   consented load runs, **Then** it goes through the existing token-scoped proxy and nothing else.
5. **Given** the identical Markdown content as an ordinary workspace Library file, **When** the normal
   viewer renders it, **Then** today's behaviour is unchanged (images and embeds resolve as before) —
   the policy scopes the temporary mail source only. **A saved mail file is an ordinary workspace file**
   (founder ruling Q-E): the mail-restricted resource policy applies to the temporary preview only,
   while saved HTML keeps the decided scripts-off default with its per-file checkbox (US-9).
6. **Given** any reused renderer (markdown, media, HTML iframe) in the temporary view, **When** it
   resolves a resource, **Then** it resolves through the source-scoped policy passed with the
   handoff — never through the workspace/Library resolver.

### US-9 — Saved mail-derived HTML: scripts off by default, per-file choice (P1)

A user who saves an HTML attachment gets the original bytes in the Library, marked as mail-derived,
rendering without scripts unless they explicitly allow them for that one file.

Why this priority: founder Q5=A is settled; the frontend half is the visible checkbox and honest
profiles. The provenance marker itself is W7's — this spec states the dependency, not the storage.

Independent test: save an HTML attachment; reopen it in the Library viewer; assert scripts-off
presentation, the marker-driven notice, and that the per-file checkbox changes only that file.

Acceptance scenarios:

1. **Given** a saved HTML attachment with `preview_profile=mail_restricted`, **When** the Library
   viewer opens it, **Then** it renders with scripts off by default and a visible notice saying
   scripts are disabled because the file came from mail.
2. **Given** that file, **When** the user enables the per-file "Allow scripts" checkbox, **Then** only
   that file switches to the ordinary isolated script-permitting workspace profile; the choice is
   visible, per file, and never global.
3. **Given** an ordinary workspace HTML file, **When** the viewer renders it, **Then** its existing
   isolated scripts-allowed profile is unchanged and no mail-derived notice or checkbox appears.
4. **Given** a mail-derived file whose marker is missing or unreadable, **When** the viewer opens it,
   **Then** it fails safe (scripts stay off) rather than silently upgrading a known mail-derived file.
5. **Dependency (stated, not owned here)**: the marker's survival across move, copy, rename and
   restore-from-backup is the cache-and-provenance work's proof obligation (W7, founder Q5=A). W3/W8
   render from the generated `preview_profile` field and the per-file allowance field only; neither
   package may infer provenance by scanning bytes or filenames.

### US-10 — Refresh and Retry keep their distinct meanings (P2)

Manual Refresh forces a folder-list revalidation; Retry on a failure re-dials past backoff — and
neither is ever copied into an automatic request.

Why this priority: the ADR's failure table and the existing `retry=true` human-marker mechanism
(Verified in `src/lib/api/mail.ts::retryQs` and `MailPanel`'s human refs) must survive the rewrite
without merging.

Independent test: stub the folder endpoint; assert Refresh sends `refresh_mapping=true` (and
`mode=live`), Retry sends `retry=true` only from a failure surface, and automatic paths send neither.

Acceptance scenarios:

1. **Given** the panel open, **When** the user presses Refresh, **Then** one folder-list request with
   `mode=live` and `refresh_mapping=true` follows, plus the folder's one live list refresh — and no
   other folder is rediscovered.
2. **Given** a failed surface (folders, list, detail), **When** the user presses Retry, **Then** the
   affected query's next fetch carries `retry=true` (bypasses backoff only — never capacity or
   security) and no automatic refresh ever carries it.
3. **Given** a 503 with `reason=pool_busy`/`account_busy`/`server_connection_limit`, **When** the
   panel renders the failure, **Then** the busy copy of §11 state S-5 shows with its specific cause
   text where the reason is known.

---

## 5. Behavioral contract (quick reference)

When/Then statements summarising §4 — observable behaviour only:

**Cache-first and freshness**

- When a panel eligible event occurs, the system renders cache-first rows immediately and issues at most
  one live refresh for that event — none at all when the event is an open or folder switch whose data was
  validated within the last five minutes, exactly one when the data is absent or older; manual Refresh
  and the panel's own successful actions always refresh.
- When a live refresh settles under an older publication revision than the newest applied one, the
  system drops it unrendered.
- When the panel is closed, the system issues no folder/count/header requests of any kind.
- When cached rows are stale, the system keeps them visible, labels their age, and shows a checking
  indicator until the one refresh settles.
- When the refresh fails, the system keeps the stale rows labelled with their original checked time and
  shows an error with Retry — timestamps never advance on failure.
- When a source or count is unknown, the system displays unknown ("—", stale presentation) and never a
  fabricated zero or a fake "just checked".
- When the folder rail and message list are idle, no repeating timer refetches them; the watcher banner's
  saved-state poll is the only cadence and never dials mail.

**Paging and search**

- When a folder view opens, the system shows 25 rows and a Load more control when more exist.
- When Load more is activated, the system appends exactly 25 rows, preserving prior rows.
- When 200 rows are displayed, the system removes Load more, explains the ceiling, and offers a working
  search control.
- When search runs, the system fetches live results under the same 25/200 discipline with its own exit
  back to the browse view.
- When a stale cursor is refused, the system resets to the folder's first page with a visible notice —
  never a spin, a silent retry, or a replay.
- When the view is left, the system releases the working set beyond the reusable cache.

**Folder overrides**

- When mailbox settings render, the Sent/Drafts name fields show the saved override or "Automatic".
- When a field is saved empty, the system clears the override and discovery applies.
- When an override names an unfound folder, the system shows the unresolved state in the rail and an
  actionable warning in settings — never silent use of another folder, never "no such folder" for an
  unknown.

**Presence**

- When a Mail panel becomes visible with a resolved workspace, the system sends one observer open frame
  carrying only an opaque observer id and the workspace id on the authenticated socket.
- When the panel closes, the workspace changes, or the connection drops and returns, the system closes
  or re-opens the observer accordingly — each tab's observer is its own.
- When no observer is acknowledged, reads still work as ordinary request-scoped work.

**Attachment handoff**

- When a row has attachments, the system shows the paperclip indicator with accessible text.
- When Open is activated, the system mints a preview and hands the generated descriptor to the Library
  viewer with the exact context bar; focus lands on the context bar heading, announced.
- When Back is activated, the system returns focus to the originating action, or to the message row,
  or to the folder tab — each fallback announced.
- When Save settles, the system announces the outcome without moving focus and enables Open in Library
  only on a real saved receipt; a lost response shows "Save result unknown" until explicitly retried
  with the same operation token.
- When a temporary view renders, only resources belonging to that preview may load; the identical
  workspace content keeps today's behaviour.

**Saved mail-derived HTML**

- When a mail-derived HTML file opens in the Library viewer, scripts are off by default with a visible
  notice; a per-file checkbox allows scripts for that one file; a missing marker fails safe.

---

## 6. Explicit non-goals and safeguards (what this spec must NOT do)

Qualitative prohibitions:

- The system must not add any repeating panel refresh timer (folder, count or header) because the ADR's
  founder-set refresh rules are event-only and the D25 30-second cadence is explicitly superseded; the
  watcher's independent 60-second probe is the only background cadence and belongs to W1, not the panel.
- The system must not treat a cache-first response as a completed refresh: a `mode=live` request is a
  different request from a cache read, by contract, so the same stale page can never be mistaken for a
  finished refresh.
- The system must not render unknown counts as zero or absent folders as errors: `total: null` renders
  "—"; `availability=unknown` renders the unresolved state; only structurally confirmed absence renders
  "No messages" with the absence explanation; missing INBOX remains an account error.
- The system must not fabricate a workspace path, `LibraryEntry`, or modification time for a temporary
  attachment preview, because the viewer's stored-file machinery keys on real paths and a fabricated
  entry would enable edit/move/download actions on something that does not exist.
- The system must not let sender-authored markup inside a temporary preview reach any resource the
  preview was not minted for — even same-origin ones — and must not weaken ordinary workspace rendering
  to achieve it.
- The system must not introduce a second discard guard, a second save protocol, a byte cache for
  previews, a local mail index, a mail-push WebSocket frame, or an attachment approval/type scanner —
  each is either already retired, owned by another package, or founder-rejected.
- The system must not send any user, session or mailbox identity in presence frames: identity comes
  from the authenticated connection; the frame carries an opaque observer id and workspace only.
- The system must not implement the saved-HTML provenance marker, its survival proofs, or any byte- or
  filename-based provenance heuristic: that is W7's storage proof; the frontend renders generated fields
  only.
- The system must not redesign layout, brand, or the compose flow beyond the named changes (reply
  context, quote prefill); "Sovereign Deep" components as shipped.

Machine-verifiable constraints:

| # | Constraint | Testable form |
|---|---|---|
| MC-W3-1 | Page size is 25; Load more adds exactly 25; ceiling is 200 rows per folder per view | With stubbed pages, row counts after 0/1/7/8 loads are 0/25/175/200; the 8th Load more click is impossible — the control is gone at 200 and the search prompt is present. |
| MC-W3-2 | At most one `mode=live` list request per eligible event, and none on an open/switch whose cache is younger than five minutes | Network-log assertion per event across §4 US-1's scenarios: exactly 0 or 1 live requests (0 for the fresh-cache events of US-1 AS-8, 1 for stale/absent), never 2+. |
| MC-W3-3 | Superseded responses never render | A delayed response whose `publication_revision` is older than the newest applied renders nothing and mutates no view state. |
| MC-W3-4 | Null counts render "—" | `total: null` and `unread_count: null` each render the unknown marker; `0` renders `0`. |
| MC-W3-5 | Presence frames carry only `type`, `action`, `observer_id`, `workspace_id` | Serialized frame assertion on every emitted frame (no extra keys). |
| MC-W3-6 | Context bar text is exact | The bar renders `From mail: <subject>` + `Back to mail` + `Save to Library` as named controls; snapshot/string assertions pin them. |
| MC-W3-7 | Zero unauthorized resource loads from a temporary view | Request counters observe 0 requests to Library/API/embed/remote targets in the I-04 dataset; the positive control observes >0 for the same content as a workspace file. |
| MC-W3-8 | Focus return order is attachment-action → message row → folder tab | Keyboard journey assertions over the three cases, each with its announcement text. |
| MC-W3-9 | Automatic requests never carry `retry=true` | Contract-level assertion on the request builder: the retry marker is only settable from a failure-surface code path. |
| MC-W3-10 | Timer removal is real | A 35-second advanced-clock test asserts zero folder/list requests after mount, and `FOLDERS_REFETCH_MS` no longer exists in `MailPanel.tsx`. |

---
## 7. BDD scenarios

Conventions: one action per When; multiple assertions in Then/And; every scenario carries its category
and a `Traces to:` line naming the US and acceptance-scenario number from §4.

### US-1 — Cached rows, refreshed only when stale

**Scenario 1.1 — Fresh open renders cached rows without dialling** *(Happy)*
**Given** the folder cache for mailbox M holds headers validated 40 seconds ago with `stale=false`
**And** the live data would return one newer message
**When** the user opens the Mail panel on M
**Then** the list renders the cached rows immediately with the freshness line "Checked 40 seconds ago"
**And** exactly one `mode=cache_first` request and **no** `mode=live` request were issued
**And** the newer message appears after the next stale-gated refresh or manual Refresh, not during this
open event.
*Traces to:* US-1 AS-8, US-2 AS-2.

**Scenario 1.7 — Stale open dials exactly once** *(Happy)*
**Given** the folder cache for mailbox M holds headers validated 7 minutes ago (`stale=true`,
`refresh_needed=true`)
**When** the user opens the Mail panel on M
**Then** the cached rows render immediately and exactly one `mode=live` request follows the cache-first
read
**And** the rows update in place when the refresh settles, without a skeleton flash.
*Traces to:* US-1 AS-1, MC-W3-2.

**Scenario 1.2 — Folder switch starts a fresh event** *(Happy)*
**Given** the panel is open on Inbox with a live refresh in flight
**When** the user selects Sent
**Then** Sent renders from its own cache-first response
**And** the in-flight Inbox refresh is discarded (its response, whenever it arrives, renders nothing).
*Traces to:* US-1 AS-2, US-1 AS-5.

**Scenario 1.3 — Closed panel never refreshes** *(Alternate)*
**Given** the Mail panel is closed and the watcher detects new mail
**When** five watcher cycles elapse
**Then** no folder, list or header request is issued by the panel
**And** the badge summary endpoint may be polled exactly on its existing 30-second cadence.
*Traces to:* US-1 AS-3.

**Scenario 1.4 — Cold cache falls through to one live read** *(Alternate)*
**Given** no cached data exists for mailbox M (`source=none`)
**When** the user opens Mail on M
**Then** the loading state shows, exactly one `mode=live` request follows the cache-first read, and the
list populates from it
**And** `source=none` is never rendered as an empty folder.
*Traces to:* US-1 AS-4.

**Scenario 1.5 — Own action triggers exactly one affected-folder refresh** *(Happy)*
**Given** the user marks an Inbox message read
**When** the seen mutation settles successfully
**Then** exactly one live refresh of Inbox follows
**And** Sent and Drafts issue no request.
*Traces to:* US-1 AS-6.

**Scenario 1.6 — No timer refetches the list** *(Edge)*
**Given** the panel stays open on Inbox
**When** the clock advances 35 seconds, then 70 seconds
**Then** no folder or list request fired after the open event's requests
**And** the watcher banner refreshed on its 30-second cadence without dialling mail.
*Traces to:* US-1 AS-7, MC-W3-10.

### US-2 — Freshness semantics

**Scenario 2.1 — Stale-and-checking keeps rows visible** *(Happy)*
**Given** cached rows validated 7 minutes ago (`stale=true`, `refresh_needed=true`)
**When** the folder opens
**Then** the rows render immediately labelled "Last checked 7 minutes ago · Checking…"
**And** no skeleton replaces them at any point
**And** when the live refresh settles successfully the line becomes "Checked just now".
*Traces to:* US-2 AS-3, US-2 AS-1.

**Scenario 2.2 — Failed refresh preserves stale rows and the checked time** *(Error)*
**Given** stale cached rows displayed with "Last checked 7 minutes ago · Checking…"
**When** the live refresh fails with a timeout
**Then** the rows remain unchanged
**And** the line reads "Couldn't refresh — showing messages as of <original time>." with a Retry control
**And** the displayed checked time equals the original last-validated time.
*Traces to:* US-2 AS-4.

**Scenario 2.3 — Unknown count never renders zero** *(Edge)*
**Given** a folder response with `total=null` and an inbox `unread_count=null`
**When** the rail renders
**Then** both count slots show "—"
**And** no screen shows `0` for either folder.
*Traces to:* US-2 AS-5, MC-W3-4.

**Scenario 2.4 — Cache-unavailable notice with live rows** *(Alternate)*
**Given** metadata carries `notice_code=cache_unavailable` and `source=live`
**When** rows render
**Then** the notice "Mail cache unavailable; using live access." shows once and is dismissible
**And** the freshness line reflects the live source.
*Traces to:* US-2 AS-6.

**Scenario 2.5 — Null last-validated time, refresh in flight** *(Edge)*
**Given** rows arrive with `source=memory`, `last_validated_at=null`, and a refresh in flight
(`refresh_needed=true`)
**When** the list renders
**Then** the freshness line reads exactly "Last checked unknown · Checking…" (§11 S-5's unknown-time
variant — the string is a pin, not an improvisation)
**And** no "just checked" wording appears.
*Traces to:* US-2 AS-7.

**Scenario 2.6 — Unknown time, no refresh in flight** *(Edge)*
**Given** rows arrive with `source=live`, `last_validated_at=null`, and `refresh_needed=false` (no
refresh follows)
**When** the list renders
**Then** the freshness line reads exactly "Last checked unknown." with no "Checking…" indicator
**And** the rows and counts render normally otherwise.
*Traces to:* US-2 AS-7.

### US-3 — Paging and search

**Scenario 3.1 — First page and Load more** *(Happy)*
**Given** a folder with 130 messages whose cache was validated moments ago (the open event issues no
live refresh — scenario 1.1)
**When** the folder view opens and the user activates "Load more" once
**Then** the list shows 50 rows in stable newest-first order
**And** the open event's cache-first read was the only page-1 request and "Load more" issued exactly one
additional request carrying page 2 — no page-1 re-fetch on Load more.
*Traces to:* US-3 AS-1, US-3 AS-2.

**Scenario 3.2 — Ceiling replaces Load more with search** *(Happy)*
**Given** 200 rows displayed and `view_limit_reached=true`
**When** the list renders
**Then** "Load more" is absent
**And** the message "You're viewing the newest 200 messages. Search to find older ones." shows with the
search control adjacent
**And** no further browse request fires.
*Traces to:* US-3 AS-3, MC-W3-1.

**Scenario 3.3 — Search reaches older messages** *(Happy)*
**Given** the ceiling state
**When** the user searches for a term matching a message older than the loaded 200
**Then** live search results render under the same 25-per-page discipline with their own "Load more"
**And** an exit "Back to Inbox" returns to the browse view with its loaded rows intact.
*Traces to:* US-3 AS-4.

**Scenario 3.4 — Search empty state** *(Alternate)*
**Given** a search term matching nothing
**When** the search settles
**Then** the list area reads `No messages match "<term>".`
**And** the browse view remains one control away.
*Traces to:* US-3 AS-5.

**Scenario 3.5 — Stale cursor resets visibly** *(Error)*
**Given** a folder whose UIDVALIDITY changed after the panel captured a cursor
**When** the next Load more settles with the typed 409
**Then** the view resets to the folder's first page
**And** the notice "The folder changed. Showing the newest messages." shows once
**And** no automatic retry of the old cursor fires.
*Traces to:* US-3 AS-6.

**Scenario 3.6 — Working set released on exit** *(Edge)*
**Given** 200 browse rows loaded and a search sequence run
**When** the user switches to another folder and back
**Then** the view starts again from the cache-first read
**And** the previously loaded older rows are not silently merged into the new view.
*Traces to:* US-3 AS-7.

### US-4 — Folder overrides

**Scenario 4.1 — Settings fields round-trip** *(Happy)*
**Given** a configured mailbox with `sent_folder_name=""` and `drafts_folder_name="Odchozí"`
**When** the user opens the mailbox settings
**Then** "Sent folder name" shows the "Automatic" placeholder and "Drafts folder name" shows "Odchozí"
**And** saving with Sent left empty clears the override while Drafts keeps its value.
*Traces to:* US-4 AS-1, US-4 AS-2.

**Scenario 4.2 — Unresolvable override shows unresolved, not absent** *(Error)*
**Given** an override naming a folder the server does not have (`mapping_source=override`,
`availability=unknown`)
**When** the rail renders
**Then** the role shows the unresolved state with the settings prompt
**And** no screen claims the server has no such folder
**And** the settings panel shows the warning on the exact field.
*Traces to:* US-4 AS-4, US-4 AS-6.

**Scenario 4.3 — Confirmed absence explains, never errors** *(Alternate)*
**Given** discovery completed and every probe returned structural not-found
(`availability=absent`)
**When** the user selects Drafts
**Then** the list shows "No messages" with the explanation
"Your mail server has no Drafts folder. You can set the folder name in mailbox settings."
**And** no error state or Retry appears.
*Traces to:* US-4 AS-6, US-4 AS-3.

**Scenario 4.4 — Legacy stored name survives as an override** *(Edge)*
**Given** a mailbox whose stored `sent_folder_name` is the literal default "Sent" saved by an older
build
**When** settings render
**Then** the field shows "Sent" as a deliberate override
**And** no save, migration or render clears it without the user choosing Automatic.
*Traces to:* US-4 AS-5.

### US-5 — Presence

**Scenario 5.1 — Open sends a minimal observer frame** *(Happy)*
**Given** the user is authenticated and opens Mail in workspace W
**When** the panel mounts with a resolved workspace
**Then** one frame `{ type: "mail_panel_observer", action: "open", observer_id: <opaque>,
workspace_id: W }` is sent on the authenticated socket
**And** no user id, session id or mailbox id appears in the frame.
*Traces to:* US-5 AS-1, MC-W3-5.

**Scenario 5.2 — Close on every exit path** *(Happy)*
**Given** an acknowledged observer
**When** the user closes the panel via the tab strip, then reopens and navigates to another workspace
with the panel open
**Then** a close frame for the first observer was sent on the panel close
**And** the workspace switch produced a close for the old workspace's observer and an open for the new.
*Traces to:* US-5 AS-2, US-5 AS-8.

**Scenario 5.3 — Tab close relies on socket loss** *(Edge)*
**Given** Mail open in a browser tab
**When** the tab is closed without a close frame
**Then** the SPA made a best-effort `pagehide` close attempt
**And** the design treats the observer as removed by socket teardown — no retained state depends on the
frame arriving.
*Traces to:* US-5 AS-3.

**Scenario 5.4 — Logout clears observers** *(Alternate)*
**Given** an open observer
**When** the user logs out and later logs back in and reopens Mail
**Then** the old connection's observers died with the socket at logout
**And** the new session opens a fresh observer with a fresh id.
*Traces to:* US-5 AS-4.

**Scenario 5.5 — Reconnect re-opens with a fresh id** *(Error)*
**Given** an acknowledged observer and the socket dropping
**When** the connection re-authenticates with the panel still open
**Then** a new open frame with a fresh `observer_id` is sent
**And** the stale id is never reused.
*Traces to:* US-5 AS-5.

**Scenario 5.6 — Two tabs are independent** *(Edge)*
**Given** Mail open in two browser tabs of the same workspace
**When** one tab closes
**Then** only that tab's observer closes
**And** the surviving tab's reads and observer are unaffected.
*Traces to:* US-5 AS-6.

**Scenario 5.7 — Unacknowledged presence degrades safely** *(Error)*
**Given** the open frame was lost (socket down) and the user opens a folder
**When** the folder reads run
**Then** they execute as ordinary request-scoped work and succeed on their own merits
**And** the panel neither blocks nor retries because presence is missing.
*Traces to:* US-5 AS-7.

### US-6 — Attachment rows and the handoff

**Scenario 6.1 — Paperclip indicator** *(Happy)*
**Given** a list page where rows 2 and 5 have `has_attachments=true` and the rest `false`
**When** the list renders
**Then** exactly rows 2 and 5 show the paperclip with accessible text "Has attachments"
**And** `has_attachments=false` renders no indicator at all.
*Traces to:* US-6 AS-1.

**Scenario 6.2 — Attachment rows name their actions** *(Happy)*
**Given** an open message with attachments `report.pdf` (2.1 MB) and `data.bin` (size unknown)
**When** the reading pane renders
**Then** each row shows filename and size ("2.1 MB", "Size unknown")
**And** each row carries Open, Save to Library and Download with accessible names
"Open report.pdf attachment", "Save report.pdf to Library", "Download report.pdf" (likewise for
data.bin).
*Traces to:* US-6 AS-2.

**Scenario 6.3 — Open mints and hands off, writing nothing** *(Happy)*
**Given** the user activates Open on `report.pdf`
**When** the mint settles
**Then** the Library viewer shows the temporary preview with the exact context bar
**And** the handoff payload was the generated descriptor only — no path, no `LibraryEntry`
**And** Mail's panel state shows the message unchanged.
*Traces to:* US-6 AS-3, MC-W3-6.

**Scenario 6.4 — Over-cap attachment** *(Alternate)*
**Given** an attachment whose actual size exceeds the 25 MB cap
**When** the rows render
**Then** Open and Save render unavailable with the explanation
"This attachment is larger than the 25 MB preview limit. Use Download."
**And** Download remains enabled and performs the browser download.
*Traces to:* US-6 AS-6.

**Scenario 6.5 — Save succeeds → Open in Library** *(Happy)*
**Given** a temporary preview of `report.pdf`
**When** the user activates "Save to Library" and the response confirms a real saved entry
**Then** the status region announces "Saved to Library as report.pdf."
**And** "Open in Library" becomes enabled and opens the real file
**And** the temporary view never claimed a saved path before this point.
*Traces to:* US-6 AS-5.

**Scenario 6.6 — Lost save response shows unknown, retry reuses the token** *(Error)*
**Given** a save whose response was lost after a possible commit
**When** the panel renders the outcome
**Then** the state reads "Save result unknown — checking whether it saved." (§11 state S-17) with an
explicit retry control, and no automatic retry fires
**And** when the user retries, the same `save_operation_token` is sent and the prior receipt resolves
the state to saved (or a visible failure) without a second file.
*Traces to:* US-6 AS-7.

**Scenario 6.7 — Failed Open shows a pinned row state** *(Error)*
**Given** the user activated Open on an attachment whose real bytes exceed the 25 MB cap while its
reported metadata said smaller
**When** the mint aborts with the typed late-failure error
**Then** the attachment row shows "This attachment is larger than the 25 MB preview limit. Use
Download." (§11 state S-26) and the Open control is re-enabled
**And** no partial preview mounted and the reading pane is unchanged
**And** a stale-reference (typed 409) mint failure shows S-27 and a busy (503) mint failure shows S-28
likewise — never a spinner, a raw error string, or a silent return to the list.
*Traces to:* US-6 AS-8.

### US-7 — Handoff accessibility

**Scenario 7.1 — Focus lands on the context bar heading** *(Happy)*
**Given** a keyboard user activated Open on "Open report.pdf attachment"
**When** the viewer mounts
**Then** document focus is on the "From mail: <subject>" heading
**And** the live region announced "Opening report.pdf from mail."
*Traces to:* US-7 AS-1, MC-W3-8.

**Scenario 7.2 — Back restores the originating action** *(Happy)*
**Given** the viewer opened from "Open report.pdf attachment" and the message still exists
**When** the user activates Back (or presses Escape)
**Then** focus returns to "Open report.pdf attachment"
**And** the announcement names it: "Returned to report.pdf in Inbox."
*Traces to:* US-7 AS-2.

**Scenario 7.3 — Back falls back when the source is gone** *(Alternate)*
**Given** the viewer open and the message deleted server-side during viewing
**When** the user activates Back
**Then** the fallback order applies: the message's list row if present, else the folder tab
**And** the announcement states where focus landed and why
("report.pdf's message is no longer in this folder. Focus moved to the Inbox folder tab.").
*Traces to:* US-7 AS-2.

**Scenario 7.4 — Save outcomes announce without moving focus** *(Happy)*
**Given** focus resting on the context bar's Save control
**When** the save settles (success, audit-warning success, or failure)
**Then** the polite live region announces the outcome text from §11 (states S-15/S-16/S-17)
**And** document focus did not move.
*Traces to:* US-7 AS-3.

**Scenario 7.5 — Disabled stored-file actions explain in place** *(Edge)*
**Given** the temporary viewer
**When** a keyboard user tabs to the disabled Edit control
**Then** the control is discoverable and its associated text reads "Save to Library first."
**And** the explanation is visible without hover and exposed to screen readers (not tooltip-only).
*Traces to:* US-7 AS-4.

**Scenario 7.6 — Keyboard loop and narrow width** *(Edge)*
**Given** the viewer at 320 px width or 200 % zoom
**When** the user tabs through the context bar and presses Escape
**Then** Back, Save and Retry were each reachable in tab order
**And** Escape returned to mail
**And** every control and explanation remained visible and readable without horizontal clipping of
interactive targets.
*Traces to:* US-7 AS-5, US-7 AS-6.

### US-8 — Resource policy

**Scenario 8.1 — Same-origin Library path refused in temporary Markdown** *(Error)*
**Given** a temporary mail Markdown attachment containing `![x](/api/v1/workspaces/W/library/download?path=secret.txt)`
**When** the viewer renders the Markdown
**Then** zero requests were issued to that URL
**And** no `<img>` was mounted with that source (structural refusal).
*Traces to:* US-8 AS-1, MC-W3-7.

**Scenario 8.2 — Embed and remote targets refused** *(Error)*
**Given** temporary Markdown with a workspace-embed syntax and a remote `https://` image
**When** the viewer renders
**Then** zero requests issued to the embed target and zero to the remote host.
*Traces to:* US-8 AS-2.

**Scenario 8.3 — Own resources and consented proxy load** *(Happy)*
**Given** the temporary view of an attachment with a CID inline image, plus an eligible remote image
**When** the CID image renders and the user clicks "Load images"
**Then** the CID image loads through the preview's own minted resource
**And** the remote image loads only through the token-scoped consent proxy.
*Traces to:* US-8 AS-3, US-8 AS-4.

**Scenario 8.4 — Workspace rendering unchanged (positive control)** *(Edge)*
**Given** the identical Markdown saved as an ordinary workspace Library file
**When** the normal Library viewer renders it
**Then** images and embeds resolve exactly as they do today
**And** the temporary-view refusals did not touch the workspace code path.
*Traces to:* US-8 AS-5.

**Scenario 8.5 — Every renderer resolves through the policy** *(Edge)*
**Given** the temporary view exercising markdown, media and HTML renderers
**When** each resolves a resource reference
**Then** each resolved through the source-scoped policy passed with the handoff
**And** none consulted the workspace/Library resolver.
*Traces to:* US-8 AS-6.

### US-9 — Saved mail-derived HTML

**Scenario 9.1 — Scripts off by default with notice** *(Happy)*
**Given** a saved HTML attachment with `preview_profile=mail_restricted`
**When** the Library viewer opens it
**Then** it renders with scripts off and the notice "Scripts are disabled because this file came from
mail."
*Traces to:* US-9 AS-1.

**Scenario 9.2 — Per-file checkbox switches only that file** *(Happy)*
**Given** that file and a second, ordinary workspace HTML file
**When** the user enables "Allow scripts" on the mail-derived file
**Then** only that file renders with the ordinary isolated script-permitting profile
**And** the workspace file's rendering is unchanged and shows no checkbox.
*Traces to:* US-9 AS-2, US-9 AS-3.

**Scenario 9.3 — Missing marker fails safe** *(Error)*
**Given** a mail-derived file whose marker is missing or unreadable
**When** the viewer opens it
**Then** scripts stay off (the restricted profile) and a safe warning shows
**And** the file is never silently upgraded to script-permitting.
*Traces to:* US-9 AS-4.

### US-10 — Refresh and Retry

**Scenario 10.1 — Refresh forces mapping validation once** *(Happy)*
**Given** the panel open with a valid mapping
**When** the user presses Refresh
**Then** the folder-list request carries `mode=live` and `refresh_mapping=true`
**And** the current folder gets exactly one live list refresh
**And** no other folder was contacted.
*Traces to:* US-10 AS-1.

**Scenario 10.2 — Retry is human-only** *(Error)*
**Given** a failed list fetch showing Retry
**When** the user activates Retry
**Then** that request carries `retry=true` and bypasses backoff only
**And** the subsequent automatic event refreshes (if any) carry no retry marker.
*Traces to:* US-10 AS-2, MC-W3-9.

**Scenario 10.3 — Busy causes are distinguishable** *(Alternate)*
**Given** a 503 with `reason=server_connection_limit`
**When** the failure renders
**Then** the panel shows "The mail server reached its connection limit. Try again shortly." with Retry
**And** a generic `busy` shows "Mail is busy. Try again."
*Traces to:* US-10 AS-3.

---
## 8. TDD plan (tests designed from this spec, before implementation)

qa-lead owns every file in this section (W5; the frontend rules forbid implementers from editing test
files). Levels: Unit (vitest, node/DOM), Component (vitest + Testing Library), E2E (Playwright,
`tests/e2e/`, 24-shard plan). New files land in CI groups automatically by path
(`scripts/check-vitest-coverage.mjs` is the tripwire): `src/components/workspaces/**` rides
`components-workspaces`, `src/components/connectors/**` rides `components-misc`.

### 8.1 Unit — the cache-view adapter (`mailCacheView.ts`)

| Order | Test (file `src/components/workspaces/mail/mailCacheView.test.ts`) | Proves |
|---|---|---|
| U1 | `live refresh is stale-gated` — drive open/switch with a fresh cache (expect 0 live) and with an absent or 5-minutes-stale cache (expect exactly 1), then refresh/own-action events, against a stubbed fetch; count `mode=live` calls | MC-W3-2: 0 or 1 live request per event per the conditional, never 2+; a fresh-cache open or switch never dials (US-1 AS-8). |
| U2 | `superseded revision is dropped` — resolve an older-`publication_revision` response after a newer one applied | MC-W3-3: no view mutation, no label change from the stale response. |
| U3 | `revision advances on own mutation and invalidation` — mark-seen/send/refresh events advance the local revision | The ordering rule the panel obeys is the one W2/W4 define; the adapter never invents revisions. |
| U4 | `failure preserves last-validated time` — fail the live refresh | The displayed time stays at the cache's value; retry state set; no timestamp advance. |
| U5 | `source=none falls through once` | Exactly one follow-up live request; no loop. |

### 8.2 Unit — presence adapter (`mailPanelPresence.ts`)

| Order | Test (file `src/components/workspaces/mail/mailPanelPresence.test.ts`) | Proves |
|---|---|---|
| U6 | `open emits a minimal frame` — stub `WsConnection.send`; open the panel | MC-W3-5: exactly the four keys; fresh opaque id; workspace id present. |
| U7 | `close on panel close, workspace switch, pagehide` | §7 scenario 5.2/5.3 sequences. |
| U8 | `reconnect re-opens with a fresh id` — simulate drop + re-auth | §7 scenario 5.5; stale id never reused. |
| U9 | `logout tears down with the socket` | §7 scenario 5.4; no frame after teardown. |
| U10 | `unacknowledged presence does not block reads` | §7 scenario 5.7 — reads proceed; no presence-dependent gating. |

### 8.3 Unit — freshness formatting (`mail-format.ts`)

| Order | Test (file `src/components/workspaces/mail/mailFreshness.test.ts`) | Proves |
|---|---|---|
| U11 | `four sources label correctly` — live/memory/encrypted_disk/none × fresh/stale × null/valid `last_validated_at` | §7 scenarios 2.1/2.5; US-2's exact strings. |
| U12 | `relative time vocabulary` — 30 s / 2 min / 7 min / 3 h / 2 d boundaries | Label rounding is stable and honest ("40 seconds ago", "2 minutes ago"). |
| U13 | `nullable date renders No date everywhere` — list row, detail header, sent view, reply attribution input | F6: null/year-one/zero → "No date"; never "1 Jan 1", never blank in one surface and dated in another. |

### 8.4 Component — the panel

| Order | Test (file) | Proves |
|---|---|---|
| C1 | `MailPanel.cacheFirst.test.tsx` — fresh-cache open (zero live), stale open (exactly one live), stale-and-checking label, failed-refresh, cache-unavailable | §7 scenarios 1.1, 1.7, 2.1, 2.2, 2.4 end-to-end through the DOM, with request-count assertions on a stubbed `fetch`. |
| C2 | `MailPanel.paging.test.tsx` — first page, Load more, ceiling, search reachable, empty search, stale-cursor reset, exit-releases | §7 scenarios 3.1–3.6; MC-W3-1 row-count ladder (0/25/175/200). |
| C3 | `MailPanel.folderAvailability.test.tsx` — present/absent/unknown rail states; unknown ≠ 0; override warning mapping | §7 scenarios 2.3, 4.2, 4.3. |
| C4 | `MailPanel.attachmentRows.test.tsx` — paperclip true/false; action names; over-cap copy; size unknown | §7 scenarios 6.1, 6.2, 6.4. |
| C5 | `MailPanel.attachmentHandoff.test.tsx` — Open mints and hands the generated descriptor; Back focus order (action → row → folder) with announcements; save announcements without focus movement; failed-Open row states (S-26/S-27/S-28) | §7 scenarios 6.3, 6.7, 7.1–7.4; MC-W3-6/8. Assert the descriptor is the generated type shape — a hand-rolled parallel object fails the test. |
| C6 | `MailPanel.noTimer.test.tsx` — advanced fake timers, 35 s + 70 s | MC-W3-10: zero folder/list timer requests; summary cadence unchanged. |
| C7 | `MailPanel.refreshRetry.test.tsx` — Refresh vs Retry markers; busy reason copy | §7 scenarios 10.1–10.3; MC-W3-9 (request-builder assertion). |
| C8 | `MailPanel.states.test.tsx` (existing file — qa-lead rewrites the D25 oracle, keeps every still-valid state assertion) | The retired 30 s cadence assertions become the §7 1.6 assertions; all other state texts keep their pins. |
| C9 | `EmailMailboxPanel.folderOverrides.test.tsx` — fields, automatic semantics, round-trip through `saveAgentMailbox`'s generated request, legacy name shown as override | §7 scenarios 4.1, 4.4. |
| C10 | `MailComposeDialog.replyContext.test.tsx` — Reply (sender only) and Reply all (To/Cc per F5) prefill from the generated reply-context response; quote escaped and editable; No date attribution | F5/F6 panel half. |

### 8.5 E2E (Playwright, against the built SPA + gateway with the fake-IMAP fixture)

| Order | Test (file) | Proves |
|---|---|---|
| E1 | `tests/e2e/mail-cache-first.spec.ts` — open panel (cached then live), folder switch, manual Refresh, retry on a forced failure | User-observable freshness behaviour; the fake server's command counters prove request counts, not stubs. |
| E2 | `tests/e2e/mail-paging-search.spec.ts` — 130-message folder to the ceiling; search finds an older message | US-3 reachability with a real gateway. |
| E3 | `tests/e2e/mail-attachment-open.spec.ts` — Open → viewer → Back (focus), Save success → Open in Library, over-cap Download-only | US-6/US-7 journeys with real focus/announcement observation (`aria-live` text, `document.activeElement`). |
| E4 | `tests/e2e/mail-temporary-source-policy.spec.ts` — the I-04 dataset with browser request interception counters; workspace positive control | MC-W3-7 in a real browser. |
| E5 | `tests/e2e/mail-presence.spec.ts` — open/close across two pages, reload, logout | §7 scenarios 5.1–5.6 against the real socket lifecycle. |
| E6 | `tests/e2e/mail-saved-html-scripts.spec.ts` — save an HTML attachment, reopen it in the Library viewer: scripts-off with the mail-derived notice, the per-file "Allow scripts" checkbox flips only that file, an ordinary workspace HTML file is unaffected, a missing marker fails safe | US-9 scenarios 9.1–9.3 / FR-W3-18 executed end to end — the saved-HTML leg the first draft cited but never defined (grill finding F-7). |

### 8.6 Test datasets (boundary / error / happy rows; every row traces to a scenario)

**D-1 — Freshness metadata matrix** (U11, C1; traces US-2):

| Row | source | last_validated_at | stale | refresh_needed | total | Expected |
|---|---|---|---|---|---|---|
| 1 | live | now | false | false | 42 | "Checked just now"; count 42 |
| 2 | memory | −2 min | false | false | 42 | "Checked 2 minutes ago" |
| 3 | memory | −7 min | true | true | 40 | stale label + Checking… |
| 4 | encrypted_disk | −26 h | true | true | null | stale label; count "—" |
| 5 | none | null | false | true | null | loading → live fill; never "empty" |
| 6 | live | null | true | true | 7 | "Last checked unknown · Checking…" (unknown time, refresh in flight — scenario 2.5's pin) |
| 7 | memory | −2 min | false | false | 0 | legit zero renders `0` (distinct from row 4) |
| 8 | live | null | false | false | 7 | "Last checked unknown." with no "Checking…" indicator (scenario 2.6's pin) |

**D-2 — Paging ladder** (U-C2, E2; traces US-3): folders of 0, 1, 24, 25, 26, 199, 200, 201 messages.
Boundaries: 24 → no Load more; 25 → exactly one page, Load more present iff `has_more`; 200 → ceiling
copy + search; 201 → still 200 max rows ever displayed. Error rows: 409 stale cursor on page 2; cursor
from a different folder replayed at the same view (must 409, not render).

**D-3 — Folder availability** (C3; traces US-2/US-4): {present, override, special_use, fallback, saved} ×
{absent-confirmed, unknown-no-candidates, unknown-network-failure}; INBOX missing (account error —
never an empty rail); inbox unread_count 0 vs null. `mapping_source=saved` renders as an ordinary
override (the register row 3 five-value enum).

**D-4 — Attachment descriptors** (C4/C5; traces US-6): sizes {0?, unknown(null), 1 B, 25 MiB − 1,
25 MiB exactly, 25 MiB + 1, misreported-small-with-large-actual}; filenames {plain, spaces, unicode,
hostile `../x`, empty-after-sanitize → `attachment`}; `has_attachments` true/false across 25 rows;
`message_ref` present/absent (absent must never occur on a W0-regenerated build — the I-03 contract).
The misreported-small-with-large-actual size row expects the typed late-failure abort with §11 state
S-26 on the attachment row (scenario 6.7), never a mounted preview; the 409/503 mint-failure rows live
in D-7 and scenario 6.7.

**D-5 — Handoff focus dataset** (C5, E3; traces US-7): originating action present / message present
row gone / folder changed / panel workspace switched while viewing; announcements asserted per §7 7.2
and 7.3 texts.

**D-6 — Resource-policy dataset** (U-C5, E4; traces US-8): image targets {same-origin Library
download URL, same-origin API path, workspace embed, wikilink, remote https, data: URL, blob: URL, the
preview's own byte URL, CID of same message, consented remote via proxy}; content {markdown img,
markdown embed, HTML body img}; controls {same content as workspace file → requests observed}.

**D-7 — Save outcomes** (C5/E3; traces US-6): {success, saved-with-audit-warning, refused-permission,
refused-cap, over-cap-download-only, lost-response-unknown, retry-same-token → prior receipt,
retry-different-token → new save, automatic-retry attempt → must never fire}.

**D-8 — Presence lifecycle matrix** (U6–U10, E5; traces US-5): {open, close, workspace-switch,
pagehide, logout, socket-drop-reconnect, two-tabs-one-closes, frame-lost-unacknowledged}.

### 8.7 Counterexamples — tests a careless implementation must fail

At least these mutations; each names the check that kills it:

1. **Mutation: reuse the cache response as the refresh result.** An implementation that lets
   `mode=live` fall through to the cache, or renders a second cache read as the refresh, survives all
   happy paths. Killed by: C1's request-shape assertion (the live request URL/query differs) plus
   scenario 2.2's timestamp-freeze assertion — a cache-as-refresh cannot produce a *new* failure
   while keeping the old timestamp.
2. **Mutation: keep a 30 s refetchInterval "just for safety".** Killed by C6 (MC-W3-10) asserting zero
   timer requests and the absence of `FOLDERS_REFETCH_MS`.
3. **Mutation: render `total ?? 0`.** Survives every success path. Killed by D-1 row 4 / C3 (the "—"
   assertion) and D-3's null-unread row.
4. **Mutation: `page_size=25` but append the same page twice** (cursor ignored). Killed by D-2's row
   ladder (duplicates fail the newest-first + count assertions).
5. **Mutation: focus returns to "somewhere in the list"** (e.g. `container.focus()`). Killed by C5/E3
   asserting `document.activeElement` equals the specific originating control's accessible name, and
   the two fallback announcements.
6. **Mutation: resource policy enforced by CSS hiding or `loading="lazy"`** (requests still fire).
   Killed by E4's interception counters (zero network events) — the observer sees requests CSS hides.
7. **Mutation: presence frame carries the session id** (helpfully "for authorization"). Killed by U6's
   exact-keys assertion (MC-W3-5).
8. **Mutation: Save retry mints a fresh token.** Killed by D-7's retry-same-token row (prior receipt
   expected; a fresh token produces a second file and fails the one-file assertion).
9. **Mutation: unknown folder availability rendered as "No messages".** Killed by C3's unknown-vs-absent
   copy assertions (M-01's distinction).
10. **Mutation: paperclip derived from `attachments.length > 0` on cached rows lacking the flag.**
    Killed by C4 asserting the indicator against `has_attachments` (not list length), including a row
    with `has_attachments=true` and a cache row lacking an attachments array.
11. **Mutation: live refresh on every open/switch regardless of freshness.** A panel that dials the
    server even when the cache is seconds old survives every render assertion — it only shows in request
    counts. Killed by U1/C1's fresh-cache zero-live assertion and scenario 1.1 (US-1 AS-8, MC-W3-2).
12. **Mutation: a failed Open/mint left spinning or silently dismissed.** Survives every success-path
    journey. Killed by C5/E3's failed-mint rows asserting the pinned S-26/S-27/S-28 row states and that
    no partial preview mounted (US-6 AS-8, FR-W3-23).

### 8.8 Regression plan

Preserved unchanged (existing tests keep passing): `src/lib/api/mail.message-ref.test.ts`,
`mail.retry.test.ts` (the `retry=true` builder semantics do not change);
`MailPanel.unsavedLeave.test.tsx` (discard guard); `MailPanel.markSeenLoop.test.tsx` (seen-once
behaviour); `MailPanel.mailboxChooser.test.tsx` and the deep-link/intent tests (selection semantics are
untouched by this spec except where a test asserts the retired cadence).

qa-lead rewrites (legacy oracles contradicting this spec): the D25 cadence assertions inside
`MailPanel.states.test.tsx` (becomes C8), and any `MailPanel.endlessLoading.test.tsx` /
`MailPanel.listSkeleton.test.tsx` expectations that pin a full-list skeleton where the new design
requires stale rows to stay visible — each rewrite cites the section of this spec that supersedes it.
Also in the rewrite list: **`MailPanel.staleDraft.test.tsx` and `MailPanel.staleDraftRetry.test.tsx`**
(grill finding F-4). They pin verbatim, five times over, the drafts-only string `This draft was changed
or deleted elsewhere. The list has been refreshed.` (Verified:
`src/components/workspaces/mail/MailPanel.tsx::MailPanel`) — §11 S-11 generalizes that surface to all
folders with new text, so these oracles change with it; qa-lead re-pins them to the S-11 string with
that citation. They were absent from this section's first-draft preserve and rewrite lists — the exact
surprise-red the regression plan exists to prevent.

New regression seam: the workspace positive control in D-6/E4 (ordinary Library Markdown rendering)
must be recorded once before the W8 renderer changes land, so a later regression has a baseline.

---
## 9. Functional requirements and success criteria

### Functional requirements

- **FR-W3-1**: The panel MUST render a cache-first response's rows before any live request settles, for
  the folder rail, the message list and the reading pane's metadata, on every eligible event.
  (MUST; US-1)
- **FR-W3-2**: The panel MUST issue at most one `mode=live` request per eligible event per surface — on
  panel open and folder switch it MUST issue one only when the folder's data is absent or its last
  validation is older than five minutes, while manual Refresh and the panel's own successful actions
  always refresh (founder Q-C) — and MUST drop any response whose `publication_revision` is older than
  the newest applied revision. (MUST; US-1)
- **FR-W3-3**: The panel MUST NOT contain a repeating timer that fetches folders, counts or headers;
  the watcher banner's 30-second saved-state summary poll MAY remain. (MUST; US-1)
- **FR-W3-4**: Every folder/list/detail response's `MailReadMetadata` MUST be rendered as its exact
  freshness state; a cache hit MUST NOT be presented as a settled server check; a null
  `last_validated_at` MUST present as unknown/stale. (MUST; US-2)
- **FR-W3-5**: Unknown counts (`total: null`, `unread_count: null`) MUST render "—"; zero MUST render
  only when the count is genuinely zero. (MUST; US-2)
- **FR-W3-6**: A failed live refresh MUST preserve stale rows, their original checked time, and a
  visible Retry; timestamps MUST NOT advance on failure. (MUST; US-2)
- **FR-W3-7**: List paging MUST be 25 rows per page, +25 per Load more, with a hard ceiling of 200 rows
  per folder per view; beyond the ceiling the search control MUST be offered and MUST work. (MUST; US-3)
- **FR-W3-8**: Search MUST run live (never cache-served), matching subject plus sender/recipient
  substrings server-side within the same 25/200 discipline (founder Q-D=A), with a visible exit back to
  the browse view. (MUST; US-3)
- **FR-W3-9**: A typed stale-cursor refusal MUST reset the view to the folder's first page with a
  visible notice and no automatic replay. (MUST; US-3)
- **FR-W3-10**: The mailbox settings surface MUST expose Sent/Drafts folder-name fields bound to the
  existing wire fields; empty MUST clear to automatic with on-screen wording saying so; a saved value
  MUST show as a deliberate override. (MUST; US-4)
- **FR-W3-11**: The rail MUST distinguish present, confirmed-absent, and unknown folder availability;
  unknown MUST offer the settings override and MUST NOT claim absence. (MUST; US-4)
- **FR-W3-12**: The panel MUST emit `mail_panel_observer` open/close frames carrying only an opaque
  `observer_id` and `workspace_id` on the authenticated socket, on the §4 US-5 lifecycle events. (MUST;
  US-5)
- **FR-W3-13**: Reads MUST succeed as ordinary request-scoped work without an acknowledged observer.
  (MUST; US-5)
- **FR-W3-14**: Message rows MUST carry `has_attachments` as a paperclip indicator with accessible
  text; attachment rows MUST offer Open, Save to Library and Download with distinct accessible names.
  (MUST; US-6)
- **FR-W3-15**: Open MUST mint a preview and hand only the generated descriptor to the Library viewer;
  context-bar text MUST be exactly "From mail: <subject> · Back to mail · Save to Library". (MUST; US-6)
- **FR-W3-16**: The handoff MUST implement the I-06 focus/announcement/keyboard/reflow contract of §4
  US-7, including the three-step focus fallback. (MUST; US-7)
- **FR-W3-17**: Every reused renderer in a temporary view MUST resolve resources through the
  source-scoped policy; unauthorized targets MUST be refused structurally (zero requests). (MUST; US-8)
- **FR-W3-18**: Saved mail-derived HTML MUST render scripts-off by default with a visible per-file
  scripts checkbox; the frontend MUST consume only the generated `preview_profile` and allowance
  fields. (MUST; US-9)
- **FR-W3-19**: Manual Refresh MUST set `refresh_mapping` (and `mode=live`); the human Retry marker
  MUST appear only on failure-surface retries; automatic requests MUST never carry it. (MUST; US-10)
- **FR-W3-20**: The panel SHOULD render the 503 `reason`-specific busy copy when the gateway provides
  it, else the generic busy copy. (SHOULD; US-10)
- **FR-W3-21**: The panel MAY keep the existing "Load images" consent affordance for HTML bodies
  unchanged this phase (the broader F4 styling work is W9/W4's). (MAY)
- **FR-W3-22**: An explicit retry of a save whose result is unknown MUST carry the same
  `save_operation_token` and MUST resolve from the prior receipt; the panel MUST never re-send Save
  automatically and MUST NOT mint a fresh token on retry. (MUST; US-6 AS-7; the M-02 correction — this
  requirement now owns the behaviour the first draft left only in scenarios and datasets, grill finding
  F-8)
- **FR-W3-23**: A failed Open/mint MUST render its matching pinned failure state (§11 S-26/S-27/S-28) on
  the attachment row, MUST NOT mount a partial preview or leave the row spinning, and MUST leave the
  user a visible next action (retry, or Download where applicable). (MUST; US-6 AS-8; the I-05
  late-failure ordering, grill finding F-3)

### Success criteria

- **SC-W3-1**: On a warm cache, rows render before the live refresh settles in a scripted run, with
  exactly one live request per event — measured by request-count assertions, not perceived speed.
- **SC-W3-2**: Zero occurrences of unknown-as-zero across the D-1/D-3 matrices (each null row renders
  "—").
- **SC-W3-3**: The paging ladder D-2 passes: exactly 25/25/…/200 rows, ceiling copy at 200, search
  returns the older synthetic message.
- **SC-W3-4**: The presence matrix D-8 passes with frames carrying exactly the four allowed keys.
- **SC-W3-5**: The I-04 dataset (D-6) shows zero unauthorized requests in a real browser, with the
  workspace positive control showing the observer works.
- **SC-W3-6**: The handoff journeys D-5/E3 pass with exact focus targets and announced announcements at
  320 px and 200 % zoom.
- **SC-W3-7**: All exact-text pins in §11 hold in component tests (one assertion per state).

## 10. Traceability matrix

| Requirement | User story | BDD scenario(s) | Test(s) |
|---|---|---|---|
| FR-W3-1 | US-1 | 1.1, 1.4 | U5, C1, E1 |
| FR-W3-2 | US-1 | 1.2, 1.5 | U1, U2, U3 |
| FR-W3-3 | US-1 | 1.3, 1.6 | C6, C8 |
| FR-W3-4 | US-2 | 2.1, 2.5 | U11, U12, C1 |
| FR-W3-5 | US-2 | 2.3 | C3 (D-1 row 4, D-3) |
| FR-W3-6 | US-2 | 2.2 | U4, C1 |
| FR-W3-7 | US-3 | 3.1, 3.2 | C2, E2 |
| FR-W3-8 | US-3 | 3.3, 3.4 | C2, E2 |
| FR-W3-9 | US-3 | 3.5, 3.6 | C2 |
| FR-W3-10 | US-4 | 4.1, 4.4 | C9 |
| FR-W3-11 | US-4 | 4.2, 4.3 | C3 |
| FR-W3-12 | US-5 | 5.1, 5.2, 5.4, 5.5 | U6–U9, E5 |
| FR-W3-13 | US-5 | 5.7 | U10 |
| FR-W3-14 | US-6 | 6.1, 6.2, 6.4 | C4 |
| FR-W3-15 | US-6 | 6.3, 6.5 | C5, E3 |
| FR-W3-16 | US-7 | 7.1–7.6 | C5, E3 |
| FR-W3-17 | US-8 | 8.1–8.5 | C5 (D-6), E4 |
| FR-W3-18 | US-9 | 9.1–9.3 | E6 (saved-HTML E2E), joint ADR-W8 viewer component tests |
| FR-W3-19 | US-10 | 10.1, 10.2 | C7 |
| FR-W3-20 | US-10 | 10.3 | C7 |
| FR-W3-21 | US-8 | 8.3 | existing Load-images tests (unchanged) |
| FR-W3-22 | US-6 | 6.6, 7.4 | C5, E3 (D-7 retry rows) |
| FR-W3-23 | US-6 | 6.7 | C5, E3 |

Every FR appears above; every scenario traces to at least one FR through its US. Datasets D-1…D-8 are
referenced from §8 and inherit their rows' traces.

---
## 11. Every user-visible state, with its exact text

These strings are pins: component tests assert them verbatim; copy changes come back through this spec.
`<x>` marks a substituted value. Where a string already exists in the code it is marked *(existing —
verified)* and must not drift.

| # | State | Surface | Exact text / presentation |
|---|---|---|---|
| S-1 | Loading, no cache | Message list | Skeleton rows (three, existing `ListSkeleton`), `aria-label="Loading messages"`. No text claim of freshness. |
| S-2 | Loading detail | Reading pane | Existing spinner, `aria-label="Loading message"` *(existing — verified)*. |
| S-3 | Fresh live rows | List + rail | Freshness line: `Checked just now` (or `Checked <relative time>`); no stale marker. |
| S-4 | Cached rows, fresh | List + rail | `Checked <relative time>` — e.g. `Checked 2 minutes ago`. |
| S-5 | Stale-and-checking | List header line | `Last checked <relative time> · Checking…` — rows stay visible; a subtle inline spinner rides the line; no skeleton. Unknown-time variants (nullable `last_validated_at`): `Last checked unknown · Checking…` while a refresh is in flight; `Last checked unknown.` when none is (scenarios 2.5/2.6 — both strings are pins). |
| S-6 | Refresh failed, stale rows kept | List header line + notice | `Couldn't refresh — showing messages as of <time>.` + a `Retry` button. Connection-failure classes additionally show `Can't connect to this mailbox · <class>` in the watcher banner's existing shape. |
| S-7 | Folder confirmed absent | Rail + list | Rail: role visible with count `0`; list: `No messages` + `Your mail server has no <Sent|Drafts> folder. You can set the folder name in mailbox settings.` with `Open mailbox settings` link. Never an error surface. |
| S-8 | Folder unresolved (unknown) | Rail + list | Rail: role visible with count `—`; list: `Couldn't confirm the <Sent|Drafts> folder on this server.` + `Set the folder name in mailbox settings.` + the settings link. Never claims absence; never `No messages`. |
| S-9 | Busy (capacity) | Failure surface | Generic: `Mail is busy. Try again.` + `Retry`. With reason: `pool_busy`/`account_busy` → `Mail is busy. Try again.`; `server_connection_limit` → `The mail server reached its connection limit. Try again shortly.`; `backoff` → existing banner text *(existing — verified: "Can't connect to this mailbox · <class> — retrying at <time>")*. |
| S-10 | Connection failure, no cache | List / detail | `Can't connect to this mailbox` + `Error class: <class>` + `Retry` *(existing — verified)* + existing `Open mailbox settings` affordance. |
| S-11 | Message changed or deleted | Reading pane | `This message changed or was deleted. Refresh the list.` + `Refresh list` button — **new text** (correction F-4: the first draft mislabelled this as existing; the real drafts-era string is `This draft was changed or deleted elsewhere. The list has been refreshed.`, Verified at `src/components/workspaces/mail/MailPanel.tsx::MailPanel`, which S-11 replaces and generalizes to all folders; the `MailPanel.staleDraft*.test.tsx` oracles are re-pinned by qa-lead, §8.8). A stale row click can never render as a successful open. |
| S-12 | Cache unavailable | Dismissible notice | `Mail cache unavailable; using live access.` Reports a genuine runtime failure of the product-owned exclusion or cache path only (founder Q-A: the product owns its cache directory's exclusion from data-directory staging and from the application's own backups/archives on every install, by its own means; this notice never excuses a missing product-owned exclusion — register row 18). |
| S-13 | Over-cap attachment | Attachment row | Open and Save unavailable with `This attachment is larger than the 25 MB preview limit. Use Download.` (associated text, not tooltip-only). |
| S-14 | Unsaved-Compose discard prompt | ConfirmDialog | Title `Discard unsaved changes?` · body `You have unsaved changes in Mail. Leaving now will discard them. Continue?` · confirm `Discard` *(existing — verified; unchanged)*. |
| S-15 | Save succeeded | Viewer status region (announced, no focus move) | `Saved to Library as <final name>.` + `Open in Library` enabled; audit-warning variant prepends the safe warning code text from the response. |
| S-16 | Save failed | Viewer status region | `Could not save to Library. <safe reason>` + `Retry` + `Back to mail`; stored-file controls stay disabled; the temporary view stays open. |
| S-17 | Save result unknown | Viewer status region | `Save result unknown — checking whether it saved.` + explicit `Retry save` (same `save_operation_token`); never an automatic resend; resolves to S-15/S-16 only via the receipt. |
| S-18 | Handoff opened | Context bar + live region | Bar: `From mail: <subject>` heading + `Back to mail` + `Save to Library` controls; announcement `Opening <filename> from mail.`; focus on the heading. |
| S-19 | Returned to mail | Live region | `Returned to <filename> in <folder>.` / fallback: `<filename>'s message is no longer in this folder. Focus moved to the <message list | folder tab>.` |
| S-20 | Disabled stored-file action | Viewer | Control rendered disabled with associated text `Save to Library first.` — keyboard-discoverable, visible without hover. |
| S-21 | Ceiling reached | Below list | `You're viewing the newest 200 messages. Search to find older ones.` + adjacent search control; `Load more` absent. |
| S-22 | Search empty | List area | `No messages match "<query>".` |
| S-23 | Stale cursor reset | Notice (once) | `The folder changed. Showing the newest messages.` |
| S-24 | Scripts-off mail HTML | Library viewer notice | `Scripts are disabled because this file came from mail.` + per-file `Allow scripts` checkbox with helper `Applies to this file only.` |
| S-25 | Unknown attachment size | Attachment row | `Size unknown` (never `0 B`). |
| S-26 | Open failed — over cap at transfer | Attachment row | Same text as S-13: `This attachment is larger than the 25 MB preview limit. Use Download.`; the Open control re-enables; no preview mounts (the ADR I-05 typed late-failure error — correction F-3). |
| S-27 | Open failed — message reference stale | Attachment row | `This message changed or was deleted. Refresh the list.` (S-11's text, row-scoped) + `Refresh list`; no preview mounts; no silent return to the list. |
| S-28 | Open failed — busy | Attachment row | S-9's busy copy for the returned `reason` + `Retry`; no preview mounts. |
| S-29 | Open in flight | Attachment row | The row's Open control is disabled reading `Opening <filename>…` with `aria-busy="true"`; a second mint cannot start from the same row while one is in flight. |

## 12. Components: reuse the catalogue, justify anything new

The design-system skill is loaded for this spec (rule: load before proposing components). Mapping:

| Job | Component (catalogued, `src/components/ui/`) | Notes |
|---|---|---|
| All actions (Retry, Refresh, Load more, Open, Save to Library, Download, Back, search submit) | `Button` | Variants as shipped; no raw buttons anywhere (controls lock). |
| Mailbox chooser | `Select` *(existing in MailPanel)* | Unchanged. |
| Discard prompt | `ConfirmDialog` *(existing)* | S-14 strings pinned. |
| Loading skeleton | `Skeleton` *(existing)* | S-1; never replaces stale rows (S-5). |
| Unread badge | `Badge` *(existing)* | Unknown count renders "—" as text, not a Badge. |
| Settings text fields (folder names) | `Input` + `Label` + `FormError` | The Connectors form's existing field row pattern (`FieldRow`, verified in `EmailMailboxPanel.tsx`). |
| Search field | `Input` + `Button` | Panel-local composite; no new catalogued component needed. |
| Per-file scripts checkbox | `Checkbox` | Rendered in W8's surface (S-24); named here because the acceptance criteria are joint. |
| Empty/error states | `empty-state.tsx` / `error-state.tsx` / `QueryErrorState` (existing) | Used for S-7/S-8/S-10/S-22 where the current panel renders ad-hoc `div`s — reuse, don't add a fourth pattern. |
| Notices (S-12, S-23) | Existing inline banner pattern (`role="status"`/`role="alert"` banners already in MailPanel) | Same visual language; no new component. |

**New components proposed: none.** The freshness line, context bar, paging footer and availability
states are compositions of the above inside W3/W8-owned files. Any implementation that reaches for a new
`*EmptyState`/`*ErrorState`/`*Skeleton` local copy first greps for the catalogue and the existing
shared components (design-system rule 14); a justified exception would need a stated reason in code and
reviewer sign-off. No visual redesign; tokens only (spacing/type/colour from
`src/styles/tokens.generated.css`; no raw values; the spacing scale's non-linear steps apply).

Accessibility requirements cite `docs/internal/design/design-system-definition.md::D16` (focus
restoration, status announcement, keyboard operation, zoom/reflow as release requirements); focus
management uses the existing panel-shell focus utilities (`src/components/panel-shell/panelFocus.ts`)
where applicable rather than bespoke focus code.

---
## 13. User-facing documentation TODOs (same change, drafted by the implementing lead, audited by docs-verifier)

| Page | Section | What must be said |
|---|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/mail.md` | *Reading mail* (or equivalent) | Cached rows appear instantly and are labelled with when they were last checked; on open or folder switch the panel checks the server only if the data is missing or older than five minutes, while a manual Refresh or an action on a message always checks; closing the panel stops all checking. Stale rows stay visible with their age while a check runs, and a failed check says so with Retry. |
| `docs/mail.md` | *Folders* | Inbox/Sent/Drafts are roles; Omnipus finds the real folders automatically; you can set exact names per mailbox in Connectors (empty = automatic). A folder the server truly doesn't have shows as empty with an explanation; one we couldn't confirm shows "couldn't confirm" and the setting prompt — not "no such folder". |
| `docs/mail.md` | *Long folders* | 25 rows per page; Load more adds 25; the view caps at the newest 200 per folder; Search finds older messages live on the server — there is no offline copy of your mailbox. |
| `docs/mail.md` | *Attachments* | The paperclip marks messages with attachments; Open previews in the Library viewer without saving anything (context bar "From mail: <subject>", Back to mail, Save to Library); over 25 MB only Download works; a saved file lands in mail → mailbox → month with a numbered name if one exists; if a save's result is lost the panel says "result unknown" and an explicit retry resolves it without duplicating. |
| `docs/mail.md` | *If something goes wrong* | Busy vs connection-limit vs backoff copies (S-9); "This message changed or was deleted" (S-11); cache-unavailable notice (S-12); unknown counts show "—"; a failed attachment Open names the cause on its row (S-26–S-28) and never leaves a spinner. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/library.md` | *Preview / Mail attachments* | Opening from mail is temporary: no file, no path, vanishes on exit/reload; stored-file actions explain "Save to Library first"; saved mail-derived HTML keeps original bytes, renders scripts-off with a per-file "Allow scripts" checkbox (that file only). |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/connectors.md` | *Email mailbox settings* | Sent/Drafts folder names: empty = automatic (we discover), a value = use exactly this folder; a name we can't find warns in settings; legacy saved names stay until you clear them. |

Dependency note: the `docs/mail.md` attachment wording and the `docs/library.md` saved-HTML wording
depend on the cache-and-provenance work's landed proof (W7's marker survival, W0's generated fields);
docs-verifier audits them against executed behaviour before landing, per the root Definition of Done.

## 14. Holdout evaluation scenarios (post-implementation only; excluded from §8 and §10)

Evaluated by a human or an external script against a built instance — not referenced by any development
test:

1. *(Happy)* Open Mail on a warm mailbox, then pull the network cable; the panel shows rows with their
   checked time and a clear "couldn't refresh" — nothing spins, nothing claims freshness.
2. *(Happy)* In a 300-message folder, click Load more six times; the eighth attempt is impossible; search
   for a message you know is old; it appears; press "Back to Inbox"; the newest 200 are still there.
3. *(Happy)* Open an attachment, save it, reopen it from the Library; it is a real file with a real name
   in mail → mailbox → month.
4. *(Error)* Delete a message in another mail client, click its stale row in Omnipus; the panel says it
   changed or was deleted — it does not open something else silently.
5. *(Error)* In an **unsaved** temporary preview of a mail attachment, point its markdown at a Library
   file path you own; no request for it leaves the browser (browser dev tools network tab as the
   oracle). The same content **saved** as a real workspace file loads per ordinary workspace rules —
   founder ruling Q-E; the mail-restricted policy scopes the temporary view only (correction F-5).
6. *(Edge)* Open Mail in two tabs, close one; the other keeps working; a gateway operator observing
   sockets sees retention match the surviving tab only.
7. *(Edge)* Set your Sent folder name to something your server doesn't have; the panel tells you it
   couldn't confirm it and points at the setting — it never shows an empty folder or an error.

## 15. Assumptions (explicit, challengeable)

- **A-1**: The gateway's generated `MailReadMetadata` rides the existing 200 responses (no new endpoint
  for cached reads) — the ADR's `mode` parameter proposal, assumed unchanged by W0.
- **A-2**: `observer_id` is generated client-side per panel instance (`crypto.randomUUID()`), opaque to
  the gateway's authorization logic (identity = authenticated connection). Authorization for mailbox
  work stays exactly where it is today (pair + session); presence never grants anything.
- **A-3**: Each browser tab runs its own `WsConnection`, so "several tabs" means several authenticated
  connections, each with its own observers (consistent with the existing per-tab socket and the
  cross-tab duplicate-panel prevention in `src/lib/panelTabPresence.ts`, which stays browser-local).
- **A-4**: The summary endpoint's 30-second SPA poll is untouched this phase (saved-state only).
- **A-5 (decided)**: Search matching fields are subject plus sender/recipient substring, server-side
  IMAP header search, within the 25/200 bounds — **decided, founder Q-D=A (2026-10-02)**; no longer an
  assumption or a recommendation. W0's schema text is authoritative once landed (register row 4).
- **A-6**: The existing `retry=true` wire marker keeps its current meaning (bypass backoff only) —
  verified in `src/lib/api/mail.ts::retryQs` and the gateway's budget wrapper; this spec adds nothing to
  it.
- **A-7**: qa-lead, not the implementer, rewrites the retired D25 oracle tests (frontend rules; §8.8).

## 16. Open questions — all resolved (no silent choices; each names its decider)

**Q1 — Search matching fields.** **DECIDED — founder Q-D=A (2026-10-02).** Subject plus
sender/recipient substring, server-side IMAP header search, within the 25/200 bounds. Option C (defer
search and ship the dead-end) was forbidden by the dispatch and never reconsidered. The empty-query and
over-long-query bounds are W0's schema rows (register row 4, via w5-integration's queue). Recorded as
A-5 (decided); applied in US-3 AS-4 and FR-W3-8.

**Q2 — Freshness line placement.** **DECIDED — option A; owner: this spec** (a w3 design choice, no
founder decision required). One status line above the list plus rail count markers, per folder; the pins
are §11 S-3…S-6. Per-row badges rejected as noisy and duplicative.

**Q3 — Search view model.** **DECIDED — option A; owner: this spec.** Search results replace the list
with a "Back to <folder>" exit, browse rows preserved underneath (§7 scenario 3.3 pins it). Appending
results to the browse list rejected: it conflates sources and breaks the ceiling discipline.

**Q4 — Observer id on workspace switch.** **DECIDED — option A; owner: this spec.** Close the old
observer and open a new one with fresh ids (§7 scenario 5.2 pins it). One observer per panel instance
forever rejected: it leaks across workspaces and complicates the gateway's per-workspace retention.

**Q5 — Panel search input vs the agent's `search_email` tool.** **No decision needed (informational).**
The panel control is folder-scoped UI over the REST search param; the agent tool is unchanged; no shared
client code beyond the generated types. Stated to pre-empt a "reuse the tool" misread.

## 17. Reachability — Definition of Done (the two never-merged lines)

**Code correct and tested** — evidence required before this line may be stated:
- Every test file in §8 exists, ran red on the pre-change code (tests-only commit through CI, or the
  single dispatcher-owned narrow local run the root rules permit) and green after; qa-lead's independent
  CHECK (mutation audit) passed on the panel suites; the §8.7 counterexample mutations were each shown
  to kill at least one test.
- `npm run typecheck` green (the only meaningful TS gate); `npm run lint:design-system-locks` green for
  the touched trees; generated types only — **`scripts/check-no-handwritten-wire-types.sh` green proves
  no hand-written parallel wire type slipped in, and `make verify-contracts` green proves the committed
  generated artifacts are not stale** (correction F-12: verify-contracts catches staleness, not
  hand-written types — the dedicated guard is the instrument for that class, and both gates run).
- Wave order per the landing-order register §4: this spec's wire-consuming code lands only after Wave B
  (the W0 contracts wave, backend-lead) merges its regenerated artifacts; this file is Wave A's
  correction for w3 and merges first.
- The D25-era oracle rewrites (§8.8) landed through qa-lead with citations to this spec.

**Reachable by a user/agent** — evidence required before this line may be stated:
- A real user opening the built SPA reaches every §11 state through real clicks: cached-then-live open
  (E1), paging to 200 and search (E2), attachment Open → Library viewer → Back/Save (E3), the
  scripts-off saved HTML with its per-file checkbox (E6), Refresh/Retry/Busy (E1) — executed, with
  screenshots, not written-only plans.
- No new tool registration is implicated (this package is panel UI; the Hard Constraint #6 catalog check
  applies to W10's tools, not here) — stated so the check is not silently skipped: the reachable-by-an-
  agent half of this feature lives in W3's sibling packages and their own DoD.
- The §13 documentation pages updated in the same change and audited by docs-verifier against the
  executed behaviour; `docs/mail.md` and `docs/library.md` claims match what a user actually sees.
- Presence verified end-to-end on the real socket (E5): open/close/reconnect/logout observed from the
  gateway side, not only the SPA's send calls.

---

## 18. Correction-round record (2026-10-02)

The one prescribed spec-correction round, applied from the grill report
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-2.md` and the landing-order interface
register (`docs/internal/specs/mail-live-access-landing-order.md`, §2/§4/§5). Every finding that names
this spec is applied in the text above; the founder rulings of 2026-10-02
(`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/mail-feature-decisions.md`) are applied and
none is reopened.

| Finding / ruling | Resolution in this spec |
|---|---|
| F-1 (Critical — paging/search/409 consumed by nobody's implementation) | Publishers named per register row 4 (§2.4/§3.2): W0 schemas via w5-integration's amended §8 queue; **w2 implements the IMAP SEARCH and cursor issuance in its view file**; W3 consumes and builds neither. No stub, no second declaration. |
| F-2 (Important — refresh contradiction) | Founder Q-C=A applied: the ADR's stale-gating stands. US-1 rewritten (AS-1 stale→one live; new AS-8 fresh→zero live), scenarios 1.1/1.7, §3.1 cache-view row, §5, MC-W3-2, FR-W3-2, and the §13 docs wording all aligned to the conditional rule. |
| F-3 (Important — failed Open/mint stateless) | US-6 AS-8, scenario 6.7, §11 states S-26/S-27/S-28/S-29, FR-W3-23, C5/D-4 rows and counterexample mutation 12 added (grill Q5 adopted as recommended). |
| F-4 (Important — S-11 text wrong; two oracle tests unlisted) | §11 S-11 corrected with the real drafts string cited (`src/components/workspaces/mail/MailPanel.tsx::MailPanel`); `MailPanel.staleDraft.test.tsx` / `MailPanel.staleDraftRetry.test.tsx` added to §8.8's qa-lead rewrite list with the citation. |
| F-5 (Important — holdout 5 contradicted the policy scope) | Founder Q-E=A applied: §14 holdout 5 rewritten to target the unsaved temporary view; US-8 AS-5 states the saved-file rule; saved-HTML Q5=A wording in US-9 unchanged. |
| F-6 (Minor — save-state miscitations) | §2.4 Save row, US-6 AS-7 and scenario 7.4 now cite S-15/S-16/S-17 (S-12/S-13 keep their own meanings). |
| F-7 (Minor — FR-W3-18's named test did not exist) | E6 (`tests/e2e/mail-saved-html-scripts.spec.ts`) added to §8.5; §10 and §17 cite E6. |
| F-8 (Minor — no FR owned the M-02 retry) | FR-W3-22 added (same token, prior receipt, never automatic); §10 row added; scenario 6.6 cites S-17. |
| F-9 (Minor — mixed numbering schemes) | Header package-mapping line (register row 24); §1/§3/§3.3 name real files (w1–w6); ADR letters W7–W10 mapped to the w4 file; W0 stated as a role, not a spec. |
| F-10 (Minor — ambiguous request-count oracle) | Scenario 3.1 rewritten: the open event's cache-first read is the only page-1 request; Load more issues exactly the page-2 request. |
| F-11 (Minor — unknown-time presentation unpinned) | §11 S-5 gains the two pinned variants; scenarios 2.5/2.6 and D-1 rows 6/8 pin each string. |
| F-12 (Minor — wrong DoD instrument) | §17 cites `scripts/check-no-handwritten-wire-types.sh` for hand-written types and `make verify-contracts` for staleness; both run. |
| Register rows 1–24 (w3-relevant) | Rows 1–8 in §2.4/§3.2 with single publishers; rows 14/15/16/17/19 as consumption contracts (no stub, no second implementation); row 18 with the founder-Q-A product terms; rows 20/21 in §3.1 (W3 as publisher, with freeze points and the conditional); row 22 via FR-W3-22; row 24 in the header. |
| Founder Q-A | The product owns its cache-directory exclusion from staging and backups on every install, by its own means; no machine-setup dependency appears anywhere in this spec (verified by grep, evidence below); S-12 restricted to genuine runtime failure. |
| Founder Q-B | Out of this spec's surfaces (w1/w2/w5 files own the watcher state file); no w3 text contradicts exclude-and-purge. |
| Founder Q-C | Applied throughout (US-1, §1, §3.1, §5, MC-W3-2, FR-W3-2, §13 docs row). |
| Founder Q-D | Applied (US-3 AS-4, FR-W3-8, A-5 decided, §16 Q1). |
| Founder Q-E | Applied (US-8 AS-5, §14 holdout 5; saved-HTML scripts-off + per-file checkbox stands per Q5=A). |

Evidence table (this correction round):

| Claim | Evidence (command + exit code + key output, or file::symbol) | Certainty |
|---|---|---|
| Real drafts-era string location | `grep -rn "changed or deleted" src/components/workspaces/mail/MailPanel.tsx` → exit 0; `MailPanel.tsx:342: throw new Error('This draft was changed or deleted elsewhere. The list has been refreshed.', ...)` — cited in-text as `src/components/workspaces/mail/MailPanel.tsx::MailPanel` | Verified |
| staleDraft oracle tests exist | `ls src/components/workspaces/mail/ \| grep -i staleDraft` → exit 0; `MailPanel.staleDraft.test.tsx`, `MailPanel.staleDraftRetry.test.tsx` | Verified |
| Hand-written-type guard exists | `ls scripts/ \| grep -i handwrit` → exit 0; `check-no-handwritten-wire-types.sh` (+ `.test.sh`) | Verified |
| No machine-setup dependency in this spec | `grep -n -i "omnipus-agent-os\|launchd\|gitleaks\|backup remote\|state-backup\|machine-setup\|personal ignore" docs/internal/specs/mail-live-access-w3-panel-and-settings-spec.md` → no matches (exit 1), run before and re-run after all edits | Verified |
| Sibling spec file names | `ls docs/internal/specs/ \| grep -i mail` → exit 0; `mail-live-access-w1-read-runtime-spec.md` … `w6-proof-spec.md` + `mail-live-access-landing-order.md`; no `w0` file | Verified |
| ADR stale-gating rule (founder Q-C basis) | `docs/internal/architecture/ADR-20261001-mail-live-access-pooling-folder-discovery-and-cache.md` Counts/Headers rows: "Refresh on panel open if absent or **older than 5 minutes** … No repeating timer" (read this round) | Verified |
| Five-value `mapping_source` supersedes the ADR's four | ADR "Missing versus unknown folder/count" row (four values) vs landing-order register row 3 ("W0 adopts the producer's five-value enum … ADR's four-value proposal is superseded") — both read this round | Verified |
| Founder rulings applied, none reopened | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/mail-feature-decisions.md` read in full this round (Q-A…Q-E table) | Verified |
| **Self-check** | Re-read the corrected file end-to-end after the final edit: every §11 cross-reference in §2.4/§4/§7 resolves to a defined state (S-15/S-16/S-17 for save outcomes; S-26–S-29 now exist; scenario 2.5's string is pinned in S-5); every register row touching W3 names its single publisher in §2.4/§3.2; §10's traceability covers all 23 FRs including the two new ones; §16 has no open question left unmarked. Forbidden moves checked: no test weakened (the only test-plan changes add oracles — E6, scenario 6.7, U1's fresh-cache case, mutations 11–12, D-1 row 8); no limit widened (25/25/200, 25 MB, 5-minute threshold, four frame keys, 30-minute retention all restated at founder-set values); no stub type created (every consumed interface names its publisher instead); no silent scope change (the only scope wording changes restate founder rulings; the one superseded behaviour — refresh-even-when-fresh — is founder-ruled, not quietly dropped). The only file in every commit of this round is this spec, authored as Daniel Piatkowski with no co-author trailer. | Verified |

---

*Spec ends. Prepared by the architect for team-lead's grill dispatch; corrected once per the
spec-process rule; nothing in this file is implementation approval.*
