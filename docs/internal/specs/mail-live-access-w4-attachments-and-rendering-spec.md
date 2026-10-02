# Implementation Specification: Mail live access W4 — attachments, agent tools, styling, reply and dates

**Created**: 2026-10-02
**Status**: Correction round applied 2026-10-02 (the one prescribed round). Resolved here: grill findings **I-1, I-6, M-4, M-5, M-6** (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-3.md`) and **C3, I6** (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-4.md`), each as settled by the landing-order register (`mail-live-access-landing-order.md`); founder rulings of 2026-10-02 applied — **Q-A** (product-owned cache/backup exclusion; this spec depends on no personal machine configuration — sweep clean, §16) and **Q-E** (a saved mail file is an ordinary workspace file; the mail-restricted resource policy is temporary-preview-only — §5.1/§5.4). Design is settled: [ADR-20261001 — Mail live access: pooled connections, folder discovery and a bounded cache](../architecture/ADR-20261001-mail-live-access-pooling-folder-discovery-and-cache.md) (correction round applied), its review `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-report.md` (findings I-01–I-06, M-01–M-02 applied), and founder answers **Q4=A / Q5=A** recorded 2026-10-02 in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/mail-feature-decisions.md`.
**Package mapping (register row 24)**: this file is **w4** = ADR work packages **W7–W10** (features: save / viewer / sanitizer / tools). Sibling spec files: **w1** = ADR-W1 (read runtime), **w2** = ADR-W2 (discovery/cache), **w3** = ADR-W3 (panel), **w5** = ADR-W4 (integration/privacy), **w6** = ADR-W5 (proof). "W0" is the ADR's contracts work package — a **role (backend-lead), not a spec file**; no W0 spec exists and none is required (register rows 1/8, §5 deadlock ruling).
**Work package**: the "sender, attachments and rendering" half of the Mail live-access feature — design work packages **W7–W10** plus the F1–F6 feature rules in the ADR's "Sender, attachments and rendering" section. Closes #1170 (Save/Download), #1171 (agent attachment tools), #1172 (styling), #1173 (Reply all), #1174 (preview half — Open without saving), #1175 (date fallback).
**Out of this spec (other package)**: pooled sockets, folder discovery, the bounded cache and panel refresh (w1/w2/w3 — the ADR's Phase 1/2 decisions); the list paperclip indicator and its `has_attachments` metadata fetch (w2/w3); the contracts wave themselves (published by **backend-lead in the ADR-W0 role**, register rows 1/8 — this spec names shapes and never edits contract files).
**CSS authority**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/receipts/css-allowlist.md` (security-lead, 2026-10-02) — the 73-property allow-list, deny-list with named attacks, container analysis and proof obligations P1–P9 are adopted as-is; this spec does not widen them.
**Certainty labels**: **Verified** = read in this checkout during spec writing; **Inferred** = design consequence, not tested; **Unknown** = open evidence gap. No test, build or browser run was performed by this spec.

---

## Bottom line

This package makes six things real, all designed already and none implemented today: a user can **Open** a mail attachment and read it in the Library viewer without a file being written anywhere; **Save to Library** lands it as a real workspace file (with browser Download staying a separate, unchanged action); an agent can list and read attachments freely and save with the ordinary `ask` permission; email styling survives with a parsed, allow-listed CSS sanitizer instead of today's strip-everything behaviour; **Reply all** stops dropping the other recipients and quotes the original; and a message without a Date header shows its received date or "No date" — never "1 Jan 1".

Two grill findings shape the hardest parts: **I-04** (sender-authored markup in a temporary preview must not be able to fetch same-gateway resources — every reused renderer gets an explicit source-scoped resource policy) and **I-05** (the capped preview and the unlimited browser Download must be two distinct server-enforced byte resources, or one of the two promises breaks). Both are specified as testable contracts below, not good intentions.

---

## 1. Summary and scope

### 1.1 What this package delivers

| # | Feature | Issue | Design section | One-line outcome |
|---|---|---|---|---|
| 1 | Open an attachment without saving (temporary Library preview) | #1174 | F1 | View any supported attachment in the Library viewer; nothing is written to disk; Back returns to mail. |
| 2 | Save to Library, keep browser Download | #1170 | F2 | Save lands `mail/<mailbox>/<year-month>/<sanitized-name>` as a real Library file with audit + "Open in Library"; Download stays a browser download. |
| 3 | Agent attachment tools | #1171 | F3 | `list_email_attachments` / `read_email_attachment` shipped `allow`; `download_email_attachment` shipped `ask` with ordinary Auto-approve; `read_message` gains the attachment list. |
| 4 | Email styling (parsed CSS sanitizer) | #1172 | F4 | Safe colour/font/table/background/media-query styling renders; scripts, remote loads, `@import`, `@font-face`, `position` never do. |
| 5 | Reply all + quoted original | #1173 | F5 | Reply all keeps original To/Cc (minus self, de-duplicated); the compose draft quotes the original; one shared recipient rule for panel and agent. |
| 6 | Message-date fallback | #1175 | F6 | Valid Date → server internal date → "No date". Never a zero date. |

### 1.2 Explicitly in scope

- The temporary content-source seam in the Library viewer (stored entry vs temporary mail attachment) and its read-only capability rules.
- The capped preview-purpose byte resource and the distinct browser-Download streaming path (grill I-05), including late-failure ordering.
- The temporary-Mail resource policy passed to every reused renderer (grill I-04).
- The Mail→Library handoff interaction contract — focus, announcements, keyboard, reflow (grill I-06): the descriptor and the contract are **published by w3** (register R-1, one publisher; grill-2 F-1); this package consumes the frozen shape and delivers the Library-side behaviour.
- The shared attachment transfer/save service used by panel and agent alike, its save-operation-token reconciliation for a lost response (grill M-02), its audit event, and its refusal cases.
- The saved mail-derived HTML profile (founder Q5=A): original bytes, persisted mail-derived marker, scripts off by default, per-file scripts checkbox, marker provenance across move/copy/rename/restore.
- The parsed CSS policy: allow-list from the security artefact, `<style>`-block pass, `url()` pin-then-rewrite extension, and the two data:-URI paths where the sanitizer is the only gate.
- The extracted transport-neutral reply-recipient helper and the gateway reply-context operation.
- The nullable effective message date across transport, contract and display.
- Registration/policy wiring for the three new tools at every Hard Constraint #6 touch point.

### 1.3 Explicitly out of scope (non-goals — detail in §10)

No pooled sockets, no folder discovery, no header cache, no panel refresh timers (w1–w3 core). No new Office-document renderer. No attachment type blocklist, antivirus scanner, or attachment-specific approval layer. No outbound-mail or stored-signature policy widening. No byte cache of previews, bodies or parts — not even an encrypted one. No change to browser Download's destination or disposition. No gateway-file edits of any kind — w5-integration is the single gateway Mail-route writer (register Wave C; grill-3 I-1). No JMAP identity work beyond consuming the first-phase `message_ref`, which **w5-integration issues in the gateway handlers** and which **w1/w2 validate on the same lease** (register row 16) — this spec's tools consume the issued reference unchanged.

---

## 2. Existing Codebase Context

> GitNexus MCP tools were not connected in this writing session; per shared rule 9 the analysis below is first-hand Read/Grep exploration and impact rows are labelled **Inferred**. Every symbol cited below was opened in this checkout on 2026-10-02 unless marked otherwise.

### 2.1 Backend symbols this work touches

| Symbol | Role in this feature | Change / blast radius |
|---|---|---|
| `pkg/email/view.go::ReadView` | Today's whole-message reader: `BODY.PEEK[]` of the entire message, then part selection. `handleMailAttachment` rides it today. | **Consumed, not edited here** — w2 owns `view.go`. The targeted single-part reader (`pkg/email/attachment_parts.go`, part-specific PEEK) is **published by w2** (register row 14; built in Wave C, w2's correction landing before this package's attachment RED binds) and injected into the new save service. Blast radius: every Mail read path. |
| `pkg/email/view.go::MailPart`, `::maxViewPartBytes` (`25 << 20`), `::mailAddressableParts` | The stable leaf `part_index` enumeration (draft-body marker excluded) and the existing 25 MiB decoded-byte cap. `OmnipusDraftBody` marks the draft bookkeeping part. | **Consumed.** The cap number and the index semantics are reused unchanged — no new size number, no new index scheme. |
| `pkg/email/view.go::SanitizeAttachmentName` (wraps `::sanitizeMailPartName`) | Existing sanitizer: separators/colon → hyphens, control characters removed, dot/dot-dot neutralized; empty → `attachment`. **Verified this checkout; already used by the attachment Download path.** | **Consumed** by Save. Sanitization is name hygiene only — Library path validation (`CleanRelPath`, `ValidateCreateName`) is the actual filesystem authorization; both apply. |
| `pkg/email/view.go::ResolveRef`, `::MarkSeenIn`; `pkg/gateway/rest_mail_read.go::handleMailSeen` | The verified epoch hazard: `ResolveRef` returns the current epoch with a supplied UID without comparing them, and `handleMailSeen` discards that epoch before `MarkSeenIn` opens its own session. | **Not edited by this package.** The reference-validation seam (grill I-03) is owned per register row 16: **w1 publishes the same-lease validation capability** (its §3.1 lease freeze); **w2 carries the normative validation FRs in `view.go`**. Every attachment operation must compare the reference's epoch and pair/config generation **on the same selected lease** that fetches. This spec's tools consume the `message_ref` issued by w5-integration's gateway handlers unchanged; the refusal behaviour is specified in US-3. |
| `pkg/email/transport.go::Message` | Carries `UID` and an **optional** `MessageID` (`omitempty`), no `UIDVALIDITY` — the verified I-03 gap: agent output today cannot always produce an attachment reference. | **Consumed.** Reference **issuance** is w5-integration's (the gateway handlers mint it — register row 16); same-lease **validation** is w1's published capability plus w2's `view.go` FRs. This package's tools require the issued reference and consume it unchanged. |
| `pkg/gateway/rest_mail_read.go::handleMailAttachment` | Today's single byte path: whole-message `ReadView` → `mailPartByStableIndex` → **413** for `DataUnavailable` → `SanitizeAttachmentName` → extension-derived content type → browser attachment disposition. This is why "larger files: Download only" is not working today. | **Modified by w5-integration — the single gateway Mail-route writer (register Wave C; grill-3 I-1) — from this package's requirement rows (§5.2)**: the endpoint gains the streaming Download role (no 413 for over-cap when the caller asked for a download), while the new preview-purpose resource takes the capped role. Contract-first: the ADR-W0 role splits the two in Wave B (register rows 1/8) before either consumer changes; this package edits no gateway file and consumes the split endpoints. |
| `pkg/gateway/inline_serving.go::applyMailByteHeaders`, `::mailContentDisposition` | Existing header seam: always `attachment` disposition, `nosniff`, `no-store`. | **Consumed/extended by w5-integration** from this package's requirement rows — Download keeps attachment disposition; the preview-purpose resource serves inline disposition with `no-store` and the Mail isolation policy. One shared header seam, edited by w5-integration; this package consumes the behaviour and owns no gateway file. |
| `pkg/gateway/mail_isolation_policy.go::mailIsolationPolicy` | The Mail HTML CSP: `script-src 'none'`, `style-src 'unsafe-inline'`, `connect-src 'none'`, sandbox without scripts/same-origin/forms, `img-src` confined to the Mail preview prefix + `data:`. | **Consumed unchanged** for the temporary HTML preview and the saved mail-derived profile. The CSS artefact confirms: **no CSP change is needed or requested.** |
| `pkg/gateway/library_isolation_policy.go::libraryIsolationPolicyTemplate` | The ordinary workspace Library HTML profile: `allow-scripts`, no `allow-same-origin`. | **Modified by W7 — this package's own backend work package** (ADR-20261001 W7 row lists this file among its exclusive Library gateway files; grill-2 F-3): the serving side reads the persisted marker/allowance and serves a saved mail-derived HTML file under the scripts-off mail-restricted profile by default, switching to exactly this existing template only on that file's explicit per-file allowance — the template itself is unchanged and no third profile is created. (The Mail-route gateway files remain w5-integration's exclusively; see §2.5's split.) |
| `pkg/gateway/rest_library_preview.go` (saved-file preview serving) | Serves a saved Library HTML file's preview under its profile. | **Modified by W7 (grill-2 F-3)**: reads the persisted mail-derived marker and the per-file allowance and selects the serving profile — scripts-off mail-restricted default, ordinary template on explicit allowance, fail-safe to the stricter profile when the marker is unreadable (§5.4). This is the server half of the Q5=A default; without it the founder's scripts-off default silently never activates. Mail-route handlers stay w5-integration's. |
| `pkg/gateway/mail_preview_token.go::mailPreviewGrant`, `::MailPreviewTokenTTL` (15 min) | Today's grant **retains payload bytes** (HTML/inline) — the ADR retires that for mail previews. | **Extended by w5-integration from this package's requirement rows (§5.2)**: the new attachment preview grant carries authorization/reference metadata only; each byte/representation response fetches live through the shared pool/budget. No byte payload in the token store. |
| `pkg/gateway/rest_mail_preview.go::mailSanitizePreviewHTML`, `::mailRewritePreviewSources`, `::mailExtractRemoteImageURLs`, `::mailImgTagRe`, `::mailImageSrcRe`, `::mailPreviewMaxHTMLBytes` (256 KB) | Today's inbound HTML sanitizer: strips `style`/`class`/`bgcolor`/`<font>` (no style policy at all); the pin-then-rewrite remote-image pipeline scans `<img>` tags only; remote images ride the token-scoped, SSRF-screened proxy (`::fetchRemoteImage`). | **Extended by w5-integration (the single gateway Mail-route writer — grill-3 I-1) from this package's requirement rows**: `mailSanitizePreviewHTML` calls the new parsed CSS policy (this package's `pkg/email/mailhtml/`) for style attributes and `<style>` blocks; the pin-then-rewrite pipeline gains CSS `url()` extraction/rewrite so backgrounds render. `mailImageSrcRe`'s value shape (token-scoped proxy path, `data:image/(png|gif|jpe?g|webp);base64,`) is the exact shape CSS `url()` values must satisfy. |
| `pkg/email/compose.go::signaturePolicy`, `::sigStyleAttrRe` (`^[a-zA-Z0-9 #:;,.'"\-_%!]*$`), `::outboundBodyPolicy` | The outbound signature policy permits a character-class-only style attribute; the outbound body policy rejects styling outright. | **Consumed, deliberately NOT reused for inbound**: `sigStyleAttrRe` admits `position:fixed` and `z-index` (plain letters/colon/semicolon) and bans parens, so `background-image` could never be expressed. It is correct only where the author is the user. Inbound mail is attacker-authored and needs declaration-level parsing (§7). Outbound/signature policies are **not widened** as a side effect. |
| `pkg/library/root.go::CleanRelPath`, `::Root.ValidateCreateName`, `::Root.MountAt`, `::Root.resolve`, `::Root.HostPath` | Path-safety primitives of the Library leaf (`pkg/library/CLAUDE.md`: "Path safety is this package's whole job"). | **Consumed** by the save service for parent creation, name validation, mount/refusal decisions and final-path authorization. Never bypassed. |
| `pkg/library/mkdir.go::Root.Mkdir` | Creates one directory level under path-safety rules. | **Consumed** — Save deliberately creates missing `mail/<mailbox>/<year-month>` parents (today's Library upload does not). |
| `pkg/library/transfer.go::Root.CreateUnique` | Reserves a fresh exclusive file with a numbered suffix before the extension (`report (1).pdf`), case-insensitive collision treatment; never overwrites. | **Consumed** — and the reason grill M-02 matters: a lost Save response plus a naive retry creates a numbered duplicate; the save-operation-token reconciliation (US-2) prevents that. Also `::Root.Rename`, `::CopyInto`, `::MoveInto` — the operations across which the Q5=A mail-derived marker must be proven to survive. |
| `pkg/audit/audit.go::IsValidEventName`; `pkg/audit/events.go` (mail constants e.g. `mail.panel.send`) | Audit vocabulary gate and existing Mail event constants. | **Extended**: new `mail.attachment_saved` event name registered in the existing vocabulary. Fields: actor, workspace/pair, folder/message reference, part index, sanitized original/final name, final path, byte count. No bytes, subject, password or token. |
| `pkg/tools/email.go::EmailToolset`, `::ReadMessageTool.Execute` | The existing email tool set (6 tools) and `read_message` (marks Seen intentionally). | **Extended** (W10): `read_message` gains `attachments[]` descriptors + `message_ref`; the three new tools join `EmailToolset`. |
| `pkg/tools/email_compose.go::parseReplyRecipients`, `::replyAllArg` | The agent-side reply-all rule — **verified this checkout**: on `reply_all` it merges original To+Cc into Cc, drops the primary and the mailbox's own address case-insensitively, de-duplicates. The rule is correct on the agent side; the *panel* drops Cc (§2.2). | **Refactored, not rewritten**: the recipient logic extracts into `pkg/email/reply.go::BuildReplyRecipients` (proposed) so gateway and tool share one implementation; the tool adapter keeps argument coercion. |
| `pkg/tools/auto_approve.go::autoApproveClasses`, `::AutoWorkspacePath` | The existing Auto-approve classifier (read tools `AutoRuns`, send tools `AutoAsks`) and the workspace-path conditional rule. | **Extended**: the three new tools classified alongside the catalog — list/read follow the read tools; save uses the existing workspace-path conditional class (founder Q4=A: no exception, no attachment-specific mechanism). |
| `pkg/tools/resolvepath.go::ResolveTurnFSPolicy`, `::ResolvePath` | The agent filesystem policy resolver (open-read rule, write authorization). | **Consumed**: the agent save adapter authorizes the **final unique path** through it, then passes an authorized writer into the shared service — never a checked string handed to an unconfined write. |
| `pkg/config/defaults.go::defaultToolPoliciesGeneral` (verified: `read_inbox`/`read_message`/`send_email`/`reply` = `allow` at the shipped ceiling) | The shipped global ceiling family. | **Extended**: literal `list_email_attachments: allow`, `read_email_attachment: allow`, `download_email_attachment: ask`; the family joins `defaultToolPolicyCeiling`. Reconcile self-heals missing entries on old installs without overwriting operator values (Hard Constraint #6). |
| `pkg/config/validate.go::ReconcileToolPolicyCeiling`; `pkg/gateway/gateway_sandbox.go::buildKnownBuiltinToolNames`, `::repairAndValidateToolPolicyCoverage` | The additive ceiling reconciliation and its tripwires. | **Consumed** — the new names flow through the normal reconciliation/validation flow; no new mechanism. |
| `pkg/coreagent/seed.go::allStaticToolNames` (five email names verified); `pkg/coreagent/role_policies_adr090.go::ADR090RolePolicyInventory` with `commonWork` (includes `read_inbox`/`search_email`/`read_message`) and `::adr090SparseRolePolicies` | Static tool-name literal (an unlisted override key panics at boot) and the per-role inventory (unlisted tools start denied for seeded roles). | **Extended**: the three names join the literal; list/read join `commonWork` (same grants as `read_message`); save granted `ask` to mail-reading roles. Sparse tightening preserved — **no coverage-driven deny backfill** (retired surface). |
| `pkg/agent/email_tools.go::registerEmailToolsForAgent`, `::SetSharedMailBudget` | Live registration of the email tools for every agent + the shared budget injection seam. | **Consumed — wired by w5-integration, never edited by this package** (ADR-20261001 W4 row: the file is integration's exclusive file, "service injection into existing agent wiring"; "W7/W9/W10 supply services/helpers, never patch those files concurrently"; w5 §1 lists it exclusive; grill-2 F-2). w5-integration's Wave-D injection wires the new tools to this package's shared service — metadata-only registration is not execution, which is exactly why the wiring rides the same Wave-D injection as the service. This package supplies the `EmailToolset` entries and the service they need, and proves reachability (§10); unconditional registration retained — a missing mailbox is an honest data result, not an absence. |
| `pkg/email/imapserver_test.go::startMemIMAP` | The real in-memory IMAP harness (`go-imap/v2`) with a global dial seam. | **Consumed by tests** (W5): attachment/save/read tests run against real protocol traffic with command counters; tests must not blindly run in parallel. |
| `pkg/workspace/instructions.go::SafeWorkDir` | Resolves the configured workspace work root. | **Consumed**: the save hierarchy `mail/<mailbox>/<year-month>/` is derived from the resolved root; the ADR's Mac example path is illustrative, never hard-coded. |

### 2.2 Frontend symbols this work touches

| Symbol | Role | Change / blast radius |
|---|---|---|
| `src/components/library/LibraryPreviewPane.tsx::LibraryPreviewPaneProps`, `::LibraryPreviewPane`, `::LibraryTextBody`, `::LibraryHtmlFrame` (`sandbox="allow-scripts"` at the frame) | The Library viewer: today requires a real `LibraryEntry` and fetches content by `entry.path`; HTML renders via the sandboxed frame. | **Modified** (W8): accepts the new content-source union — stored entry **or** temporary mail attachment; text/code/markdown stay view-mode (never editor) for a temporary source; HTML gets the scripts-off Mail profile for temporary and unsaved-mail-derived files. Existing stored-entry behaviour unchanged. |
| `src/components/library/LibraryExplorer.tsx::LibraryExplorer` (selection: `sortedEntries` → `selectedEntry`), `::handleDownload`, `::renameMutation`, `::transferMutation` | The Explorer resolves the preview target from a real listing row; stored-file actions live here. | **Modified** (W8): a transient selection alternative mounts the pane without a listing row and never enters listing/search/address/persisted store; stored-file actions are **capability-disabled** (not merely hidden) for a temporary source. |
| `src/components/library/preview/libraryPreviewKind.ts::classifyLibraryEntry`, `::ClassifiableEntry` | Name/type classification and exhaustive renderer dispatch. | **Consumed/extended**: the temporary source feeds the same classifier from the effective extension-derived response type — not the sender's self-declared `content_type` (existing type-confusion/nosniff rules preserved); the new `text_readable` hint supplies the classifier's existing text-hint input without granting editing. |
| `src/components/library/preview/KbMarkdownImage.tsx::KbMarkdownImage`, `::KbMarkdownImageContent`; `src/lib/url-safe.ts::isDisplayableImageSrc` (protocol-only check — a same-origin `/api/v1/...` path passes); `src/lib/api/library.ts::libraryDownloadUrl` | The Markdown image path that motivates grill I-04: the SPA CSP allows same-origin images and this renderer mounts any displayable src. | **Modified** (W8): every renderer receives the temporary source's resource policy; the resolver never yields an authorized URL for non-minted resources — structural refusal, not hidden attributes. Ordinary workspace rendering unchanged. |
| `src/components/workspaces/mail/MailPanel.tsx::replyTarget` (verified: carries only `from`/`subject`/`messageId`), `::AttachmentList`, `::downloadMailAttachment` | The panel's reply handoff (From only — the Cc-drop defect) and the attachment list with Download only. | **Modified** (W3 integration): reply gains the generated reply-context call; the attachment list gains Open/Save actions with accessible names and the over-cap explanation. |
| `src/components/workspaces/mail/MailComposeDialog.tsx::MailComposeDialog` (verified: `to`/`cc`/`bcc` start `EMPTY_RECIPIENTS`; no quote input exists) | Manual compose. | **Modified** (W3 integration): Reply all fills To/Cc from the generated reply context; the quoted original lands as editable escaped Markdown attribution. |
| `src/components/workspaces/mail/mail-format.ts::formatMailDate`, `::formatMailTime` (verified: reject NaN, **not** year one); `MailMessageList.tsx::ListRowTop` (hides year one in list rows only) | Date display with the verified asymmetric year-one protection. | **Modified**: both formatters guard year-one explicitly and render "No date" from a null date — list, detail, Sent and reply attribution alike. |
| `src/components/workspaces/mail/MailHtmlFrame.tsx::MAIL_HTML_FRAME_SANDBOX` (`'allow-popups allow-popups-to-escape-sandbox'`, src always the minted token URL, never `srcDoc`) | The Mail-body HTML frame. | **Consumed**: the temporary attachment HTML preview reuses exactly this frame pattern (token-scoped URL, no `srcDoc`, no object URL) with the scripts-off policy. |
| `src/components/ui/` catalog — `button.tsx`, `icon-button.tsx`, `tooltip.tsx`, `dialog.tsx`, `FormError.tsx`, `checkbox.tsx` (all verified present) | The design-system controls. | **Consumed only** — the per-file scripts checkbox uses `checkbox.tsx`; no new component family, no visual redesign. Design-system skill loads before any `src/` edit. |

### 2.3 Contracts this package consumes (published by backend-lead in the ADR-W0 role — register rows 1/8; no W0 spec file exists)

Existing shapes read this checkout, to be changed contract-first by backend-lead in the ADR-W0 role (Wave B) before any handler/consumer lands (Hard Constraint #8). The shape list in this section is this package's contribution to the Wave B union queue (w5 §8 amended ∪ w3 §2.4 ∪ this §2.3 ∪ the ADR's wire tables; conflicts resolve to the ADR):

- `contracts/components/schemas/MailAttachment.yaml` — `part_index`/`filename`/`content_type`/`size_bytes` required; **`size_bytes` is non-null today** and becomes nullable (unknown honestly), with optional labelled `reported_size_bytes`; every output carrying the descriptor also carries the gateway-issued `message_ref` (grill I-03).
- `contracts/components/schemas/MailMessageSummary.yaml` / `MailMessage.yaml` — `date` is required non-null `date-time` today; becomes **required but nullable** (the effective date; null = neither source usable).
- `contracts/components/schemas/LibraryEntry.yaml` — `path` required; stays real-path-only. The temporary preview is **never** a manufactured `LibraryEntry`; the source union is its own generated shape.
- `contracts/openapi.yaml::getMailAttachment` — today's single shared byte operation; backend-lead in the ADR-W0 role splits the preview-purpose resource from the Download streaming role in Wave B — the preview payload-removal and the endpoint split are one atomic step (register rows 1/8; grill I-05). No W0 spec file exists; the register's Wave B union is the queue.

Proposed new shapes (names from the ADR's wire table; the ADR-W0 role defines and regenerates them in Wave B — register row 8, steps 5–6 plus the feature rows): `MailAttachmentPreviewRequest`/`MailAttachmentPreviewResponse` (with `content_source.byte_url` = the dedicated preview-purpose endpoint, `preview_id`, `text_readable`, `read_only=true`), the `save-to-library` subresource with `MailAttachmentSaveRequest` (opaque `save_operation_token`) / `MailAttachmentSaveResponse` (real `entry: LibraryEntry`, `path`, `absolute_path`, `audit_status=recorded|disabled|failed`, nullable `warning_code`), the reply context operation (`mode=reply|reply_all` → recipients + quoted `body_markdown`), `LibraryEntry.preview_profile=workspace|mail_restricted` plus the per-file scripts-allowance field, and the three tool results (the ADR-W0 role owns any tool-result schema crossing the SPA boundary).

### 2.4 Impact assessment (Inferred — no graph run in this session)

| Change | Risk | d=1 dependents | d=2 dependents |
|---|---|---|---|
| `LibraryPreviewPane` source union | **MEDIUM** — every renderer's props path | `LibraryExplorer` mount, all `preview/*` renderers, PDF load effect | deep-link/persisted-store seams (must not learn the temporary source) |
| `handleMailAttachment` role split | **MEDIUM** — existing Download consumers | `fetchMailAttachment` SPA helper, contract generated types | audit fields, e2e mail download specs |
| `EmailToolset` + ceiling additions | **MEDIUM** — every agent's policy resolution | `registerEmailToolsForAgent`, `allStaticToolNames` (boot panic if missed), role inventory | permissions screen, reconciliation tests |
| `mailSanitizePreviewHTML` style support | **MEDIUM** — the mail HTML serve path | `setMailPreviewSecurityHeaders` consumers, mint flow | remote-image proxy counters; saved/sent previews |
| `parseReplyRecipients` extraction | **LOW** — behaviour-preserving refactor | `ReplyTool.Execute`, `create_email_draft` (if it reuses) | gateway reply-context operation (new consumer) |
| Nullable `date` contract | **LOW** — compile-enforced consumer migration | list/detail/Sent renderers, agent adapter, reply attribution | cached header shapes (Phase 2 package) |

No HIGH/CRITICAL blast radius found: every modified symbol has its consumers in-tree and compile-breaks loudly. The two MEDIUM UI/contract seams get the listed regression tests in §9.

### 2.5 Cluster placement

This package spans the **email transport/tools** cluster (`pkg/email/mailhtml/`, `pkg/email/reply.go`, `pkg/tools/email_attachments.go`, `pkg/mailattachment/`), the **gateway** cluster, split by the ADR's work-package rows (grill-2 F-3): the **Mail-route** files are edited exclusively by w5-integration — the single gateway Mail-route writer (register Wave C; grill-3 I-1) — from this package's requirement rows, while the **Library gateway files** (`rest_library.go`, `rest_library_write.go`, `rest_library_preview.go`, `library_isolation_policy.go` — the saved-HTML marker/profile serving side) are **W7's own exclusive files** (ADR-20261001 W7 row); the **Library** leaf, whose safety primitives the save service consumes, never bypasses or weakens — and whose files (`root.go`, `mkdir.go`, `transfer.go`, `entries.go`) the same ADR row places under W7's exclusive ownership should the §5.4 marker-provenance proof require an edit; and the **SPA workspace surfaces** cluster (`src/components/library/preview/`, Mail panel files). It must not create an import cycle: the shared recipient helper lives in `pkg/email` (below both `pkg/tools` and `pkg/gateway`); the save service lives in `pkg/mailattachment` (below Library and tools, importing neither `pkg/tools` nor gateway HTTP).

---

## 3. Interfaces — what this package publishes and consumes

Stated so the packages can be built in parallel without editing each other's files.

### 3.1 PUBLISHES (owned and delivered by this package)

| Interface | Owning owner | Consumers |
|---|---|---|
| `pkg/mailattachment/service.go::Transfer` (proposed path) — the single application-level save/read/download service; mode selects transient viewer bytes, browser response, or explicit Library write | W7 | w5-integration's gateway preview/attachment handlers (the single gateway Mail-route writer — grill-3 I-1), `pkg/tools/email_attachments.go` (this package) |
| The save-operation-token reconciliation (prior-receipt lookup on an explicit same-token retry); token/response **shapes** are W0's (register row 22), this package implements the **bounded lookup** | W7 | w5-integration's gateway save handler; the SPA retry path (w3 owns the retry FR — register row 22) |
| The mail-derived marker + per-file scripts-allowance storage (Q5=A), surviving move/copy/rename/restore — with the provenance proof | W7 | The Library gateway preview serving policy — **owned by W7, this package** (ADR-20261001 W7 row; grill-2 F-3), and `LibraryPreviewPane` HTML profile selection (W8, this package) |
| `pkg/email/mailhtml/` — the parsed CSS policy: style-attribute filtering via bluemonday's own CSS layer, the `<style>`-block pass, the checked-in property/value table, and the CSS `url()` extraction/rewrite helpers | W9 | `rest_mail_preview.go::mailSanitizePreviewHTML`, `::mailRewritePreviewSources` — edited by w5-integration, the single gateway Mail-route writer, from this package's requirement rows (§2.1, §7); this package edits no gateway file (grill-3 I-1) |
| `pkg/email/reply.go::BuildReplyRecipients` (proposed) — the transport-neutral recipient rule (Reply-To otherwise From; reply-all merge; self-exclusion; de-duplication) | W10 | w5-integration's gateway reply-context handler; `pkg/tools/email_compose.go` adapter (this package); indirectly the SPA (which implements nothing) |
| `pkg/tools/email_attachments.go` — `list_email_attachments`, `read_email_attachment`, `download_email_attachment` + `read_message`'s attachment list | W10 | `EmailToolset`, catalog, inventory, seed, agent registration |
| `src/components/library/mailAttachmentPreviewSource.ts` (proposed) — the temporary-source adapter + resource policy the renderers consume | W8 | `LibraryPreviewPane`, `LibraryExplorer`, every reused renderer |

### 3.2 CONSUMES (delivered by others; this package edits none of their files)

| Interface | Owner | This package's obligation |
|---|---|---|
| All contract schemas and regenerated artifacts named in §2.3 | backend-lead in the **ADR-W0 role** (the only editor of `contracts/`, `pkg/api/generated/`, `src/lib/api/generated/`; register rows 1/8 — no W0 spec file exists) | Consume generated types only; never hand-write a wire shape; treat a missing Wave B start as a blocked report, never a stub (§5 of the register) |
| The targeted single-part reader (part-specific PEEK) and the stable `part_index` semantics | **w2** (`pkg/email/attachment_parts.go`; register row 14 — built in Wave C, w2's correction landing before this package's attachment RED binds) | Inject into `Transfer`; consume the frozen interface; the part reader's own end-to-end oracle lives in w2's test pack (register R-4) — this package's tests assert the seam |
| The shared pool/lease interfaces and the budget injection | **w1** (`MailSessions`, `Lease`, session-source injection; register row 9, frozen in w1 §3.1) | Inject into `Transfer`; never build a private client/pool |
| The issued `message_ref` (register row 16, split): **w5-integration issues** it — the single issuer for **every** list/detail/attachment-metadata result **including the agent's `read_message` result** (w5 US-9; grill-2 F-2) —; **w1 publishes** the same-lease validation capability; **w2 holds** the normative validation FRs in `view.go` | w5-integration (issuance) + w1/w2 (validation) | Consume the reference unchanged — the tool-path reference arrives through w5-integration's Wave-D injection, and the tools never mint one locally (register rows 8/16: a second issuer is the exact R-1 violation row 16 exists to prevent); refuse stale references (wrong pair, old generation, old UIDVALIDITY) with the typed error before any fetch |
| `has_attachments` metadata + the MIME structure classifier | **w2** (register row 15 — one MIME walker, no second) | `read_message`'s attachment list reuses the same classifier output — no second MIME walker |
| The Mail→Library handoff descriptor and interaction contract — **one publisher (register R-1; grill-2 F-1): w3 publishes both**, and the freeze lives in w3's spec (w3 §3.1 handoff rows, "Frozen here"). The ADR's W8/W3 joint-ownership sentence (correction-round ¶378) covers the joint *work* — both sides build their half — not the shape's publication | w3 | W8 consumes the frozen descriptor: mounts the temporary source from it, guarantees the context bar and the focus/announcement/keyboard/reflow behaviour of §5.3, and never re-publishes, re-types or amends the descriptor; the joint work with W3 continues (§15 step 3); any disagreement about the shape resolves under register R-2.2 to a dated register line |
| `pkg/library` path-safety primitives, `pkg/audit` logger, `pkg/tools/resolvepath` policy, `pkg/config` ceiling/inventory seams | Existing owners (Library leaf, audit, tools, config) | Call them; never bypass or weaken |
| Tool description text for the three new tools | prometheus-prompt-engineer, supplied to W10's file | Paste, not author |

**Shared interfaces this package neither consumes nor implements** (publishers named so no second implementation or stub appears here; register R-1/R-4): the paging/search/stale-409 shapes (**w2** implements IMAP SEARCH + cursor issuance; **w5-integration** requests the shapes from W0 — register row 4); the REST `observer_id` query param (**W0** schemas it; **w5-integration** requests it — row 6); presence frames and the presence registry (**W0** defines the frames; **w1** publishes the registry; **w5-integration** binds connection teardown — rows 5/11); the instrumentation emitter (**w6** freezes the record shape; **w5-integration** owns the request-scoped envelope; **w1/w2** supply pool/cache sub-fields — row 17); the raw-error redaction (**w1** fixes `recordFailure` inside its own exclusive file; **w5-integration** fixes `mailErr502` — row 19). The three "instrument" mentions elsewhere in this spec (§5.1 positive control, §8) are the observation instrument of the counterexamples, not the w6 emitter.

---

## 4. User stories and acceptance criteria

Priorities: P0 = the feature is incomplete without it; P1 = high; P2 = follows after the P0s of the same story.

### US-1 — Open an attachment without saving (P0) — F1, #1174

A user receives a message with an attachment and wants to look at it before deciding to keep it. Today the only action is browser Download, and anything over the cap fails with a 413 error. With this story, **Open** shows the attachment in the Library viewer, inside its existing panel, as a temporary, non-persisted entry: every Library renderer works (image, video, audio, PDF, markdown, code, text, static SVG), HTML renders with scripts off, and a context bar — exactly **"From mail: \<subject\> · Back to mail · Save to Library"** — sits outside the rendered content. Nothing is written to disk: no workspace file, no spool, no byte cache, no browser persistent store. The existing 25 MB per-attachment cap applies; larger attachments offer **Download only**.

**Why this priority**: it is the founder's option-B decision and the entry point for Save; without the temporary viewer there is no attachment UX beyond today's Download.

**Independent test**: with a message holding attachments of each supported kind, Open each one; assert the viewer renders, the context bar reads exactly as specified, the data directory gains no file (with a Save positive control proving the observer would have seen a write), every stored-file action is disabled with its explanation, and Back returns focus to the originating attachment action.

**Acceptance scenarios**:

1. **Given** a message with an image, video, audio, PDF, markdown, code and text attachment, **When** the user clicks Open on each, **Then** the Library viewer renders each with its existing renderer inside the Library panel, without any file being created, and the context bar reads exactly "From mail: \<subject\> · Back to mail · Save to Library".
2. **Given** a temporary preview is open, **When** the user attempts Library edit/autosave, PDF fill/sign, rename, move/copy/delete or download-to-browser from the Library, **Then** every such action is disabled with a "Save to Library first" explanation that is visible without hover and readable by screen readers — and no mutation/API call fires even if the control is invoked programmatically.
3. **Given** an HTML attachment, **When** it is opened, **Then** it renders through the token-scoped isolated Mail representation with scripts off entirely (`script-src 'none'`, sandbox without scripts), forms and event handlers dead, and never as a raw HTML object URL, `srcdoc`, or the authenticated download URL served as a document.
4. **Given** an attachment whose descriptor size — the honest, known-before-any-fetch metadata — exceeds the 25 MiB cap, **When** the user tries Open (and Save), **Then** both are unavailable with the existing cap explanation, and **Download** remains the offered browser action. A size that is absent or misreported is **not** upfront-refusable (sizing by fetching the part is forbidden); it aborts during the transfer per §5.2's late-failure ordering. The two forms are separate acceptance paths, never one scenario (grill-3 I-6).
5. **Given** an open preview, **When** the user presses Back (or closes, navigates, reloads, logs out), **Then** that view's fetches abort, media detach, PDF/render tasks destroy, object URLs and the source token revoke, and a late mint/fetch cannot resurrect the view.
6. **Given** the preview open, **When** the user navigates to a real Library file, **Then** the temporary source is disposed first and never becomes a breadcrumb, listing row or fallback file.
7. **Given** an unsupported format, **When** opened, **Then** the Library's honest unsupported-format state shows with Mail Download available — no invented Office renderer, no fake zero-byte file.

### US-2 — Save to Library; browser Download unchanged (P0) — F2, #1170

The user decides to keep the attachment. **Save to Library** lands it as a real workspace file under `mail/<mailbox>/<year-month>/`, with the sanitized name (numbered suffix on a clash), the same 25 MB cap, an audit entry, and an **Open in Library** follow-up that selects the real file. Browser **Download** stays exactly what it is today — a browser download, not another name for Save. Panel Save and the agent's save tool call **one shared server function**.

**Why this priority**: Save is the consent act that turns a preview into a user file; the audit entry and the unchanged Download are the founder's explicit conditions.

**Independent test**: save the same attachment twice (second must get a numbered suffix), over-cap (must refuse with a safe reason), with audit disabled and with audit failing after commit (must return saved-with-warning, not a failed save), and with the response lost after commit (must resolve to the prior receipt on an explicit same-token retry — exactly one file). Assert exact bytes landed at the exact path; browser Download produces no Library file.

**Acceptance scenarios**:

1. **Given** an open preview or an attachment row, **When** the user clicks Save to Library, **Then** the file lands under `mail/<mailbox>/<UTC save month>/<sanitized name>` in the workspace work root, missing directories are created through the path-safe primitives, and the response returns the real Library entry, the exact final name, the workspace-relative path and a separately resolved absolute path.
2. **Given** a file with the same name already exists (including case-folded), **When** Save runs, **Then** the name gets a numbered suffix before the extension (`report (1).pdf`) — never an overwrite — and the final suffixed candidate is validated too.
3. **Given** the attachment's declared filename carries separators, control characters or dot-names, **When** Save runs, **Then** the stored name is the `SanitizeAttachmentName` output, then passed through Library create-name validation (Windows-invalid names, path limits) — sanitization is not the authorization, validation is.
4. **Given** a successful save, **When** the audit logger is enabled, **Then** a `mail.attachment_saved` audit entry records actor, workspace/pair, folder/message reference, part index, original and final names, final path and byte count — and no attachment bytes, subject, password or token.
5. **Given** audit write failure **after** the file committed, **When** the response is built, **Then** it reports saved=true with an explicit audit warning and the real path — not a failed save, and no advice to retry (a retry would create a numbered duplicate).
6. **Given** the Save response is lost after the server committed (disconnect/abort), **When** the user explicitly retries the same save, **Then** the client first shows a visible "Save result unknown" state claiming neither success nor failure and never re-sends on its own; the explicit retry carries the same save-operation token and the server answers with the **prior receipt** — exactly one file exists, under the original numbered name, with intact audit status. A retry with a different token is a new request and saves normally. No automatic replay ever fires.
7. **Given** any refusal (missing authority, effective deny, declined ask, stale message/part, over cap, unsafe path, parent-file conflict, disk full, failed transfer), **When** Save fails, **Then** the panel shows "Could not save to Library" with a safe specific reason beside the action; permission/size refusals never masquerade as network errors; no partial file remains (incomplete output is removed, and a cleanup failure is reported explicitly).
8. **Given** any successful or failed Save, **When** the user instead clicks Download, **Then** the browser download behaves exactly as today — same destination, disposition and filename — and creates no Library file.
9. **Given** Save succeeded, **When** the user clicks Open in Library, **Then** the real file opens in the Library viewer as a stored entry (full stored-file capabilities) and the Library list refreshes through its existing change notification. The saved file is user data: mailbox deletion or cache expiry never removes it.

### US-3 — Agent attachment tools and the reference journey (P0) — F3, #1171

An agent with a permitted mailbox can list and read received attachments without an attachment-specific approval, and save one into its workspace with the ordinary `ask` shipped default — no extra approval mechanism, no file-type blocklist, no hardcoded fallback. `read_message` returns the attachment list plus the gateway-issued `message_ref`, so the whole journey runs from real tool output alone — a message without a Message-ID included (grill I-03).

**Why this priority**: reachability — without reference issuance in this phase the tools are unreachable for exactly the mail that lacks Message-IDs, whatever the tests say.

**Independent test**: end-to-end with a fake IMAP server: pick a message with no Message-ID; chain `read_message` → `list_email_attachments` → `read_email_attachment` → `download_email_attachment` using only returned values; then present references from a wrong pair, an old configuration generation and a recreated folder (new UIDVALIDITY) — each must be refused before any fetch. Policy: old config without the new keys self-heals to allow/allow/ask; an agent `allow` cannot loosen the global `ask`; Auto-on runs save through the ordinary workspace-path class; refusal performs zero transfer/write.

**Acceptance scenarios**:

1. **Given** an owned pair with a message holding attachments, **When** the agent calls `list_email_attachments` with the issued `message_ref`, **Then** it returns the descriptor list (sanitized name, content type, honest nullable size, stable `part_index`) — structure metadata only: no body bytes, no file, no Seen change just to list.
2. **Given** the same message, **When** `read_message` runs, **Then** its result carries the same `attachments[]` descriptors and the `message_ref` — the attachment list rides the existing tool.
3. **Given** a text-ish attachment, **When** `read_email_attachment` runs, **Then** the agent receives the actual content through the normal tool-result reader representation; a format/size the reader cannot represent yields an explicit unsupported/too-large outcome plus the Save option — never an empty "read" or binary disguised as text.
4. **Given** a message whose Message-ID header is absent, **When** the agent chains `read_message` → `list_email_attachments` → `read_email_attachment` → `download_email_attachment` using only actual tool output, **Then** every step succeeds with no Message-ID anywhere and no step synthesizing identity; and references from a wrong pair, an old generation, or a recreated folder (old UID against new UIDVALIDITY) are each refused with the typed stale-reference error **before** any fetch or mutation, checked on the same selected lease that would have performed the action.
5. **Given** the shipped configuration, **When** any install loads, **Then** the ceiling holds literal `list_email_attachments: allow`, `read_email_attachment: allow`, `download_email_attachment: ask`; an old config gains the missing entries additively on load without overwriting operator-set values; no per-agent deny backfill appears.
6. **Given** Auto-approve ON and a save tool call, **When** the classifier resolves it, **Then** it runs through the ordinary workspace-path conditional class exactly like any other ask-default tool (founder Q4=A) — with Auto off it asks; a declined approval performs zero transfer and zero write; an agent-level `allow` can never loosen the global `ask`.
7. **Given** a successful agent save, **When** the tool returns, **Then** it reports the actual workspace file — workspace-relative path and absolute path — plus size and audit status; the agent's ordinary file tools can read that path immediately; the same shared service, cap, naming, audit event and refusal semantics as the panel apply.
8. **Given** another agent's or workspace's mailbox, **When** the tools are invoked against it, **Then** normal pair authorization refuses — "freely" never crosses pair ownership.

### US-4 — Email styling, safely (P1) — F4, #1172

Modern marketing/transactional mail is styled with inline styles, classes, `<style>` blocks and media queries. Today the inbound sanitizer strips all of it, so legitimate mail renders as uncoloured text. This story renders safe styling — colours, fonts, table layout, backgrounds, responsive rules — using the security artefact's parsed allow-list, while scripts, remote loads, `@import`, `@font-face`, `position` and friends stay impossible. "As much as Gmail and Outlook support" is a compatibility target, not a pixel-parity promise.

**Why this priority**: quality-of-rendering with a required security review ahead of it; the founder's quick wins (reply, dates) and the attachment flow land first.

**Independent test**: render a positive fixture (colour/font/table/cell/background/media-query mail) and assert computed styles survive in the browser; render the artefact's nine counterexample classes (P1–P9) plus escaped/shorthand/custom-property cases and assert zero scripts, zero non-consented remote fetches (network counters with an uncontained positive control), and readable fallbacks.

**Acceptance scenarios**:

1. **Given** mail with inline `style` attributes, `class` attributes and a `<style>` block (including `@media` queries), **When** the message renders in the preview, **Then** allowed declarations survive — verified in the browser by computed style, not by string presence in HTML — and denied ones are dropped without dropping unrelated safe content.
2. **Given** a CSS `url()` in `background`/`background-image`, **When** the mail is minted, **Then** the URL is extracted into the same pinned remote-image grant and rewritten onto the token-scoped `/mail-preview/img/…` path — backgrounds actually render under Load-images consent, and any non-conforming URL (`http:`, protocol-relative, relative, `data:image/svg+xml`, other `data:`) has its declaration dropped.
3. **Given** `@font-face` (remote **or** `data:`-font) and `data:image/svg+xml` in a CSS `url()`, **When** sanitized, **Then** both are stripped — these are the two paths where the browser's CSP would otherwise admit the content (`font-src data:`, `img-src … data:`), so the sanitizer is the **only** gate.
4. **Given** `position`/`top`/`right`/`bottom`/`left`/`z-index`, `expression()`, `behavior`, `-moz-binding`, `filter`, `@import`, `@keyframes`/animation/transition, custom properties/`var()`, `content`, `cursor`, `list-style-image`/`border-image` — **When** sanitized, **Then** each is dropped, per the artefact's deny list with its named attack or parity reason.
5. **Given** an escape trick (`col\6fr:red`, `url("ht\74 tps://…")`, comment-spliced declarations), **When** sanitized, **Then** the parser-decoded truth decides: the decoded-safe property survives, the decoded-dangerous URL and decoded-denied property are dropped — the anti-regex property of real parsing.
6. **Given** malformed CSS (unclosed block), an oversized style attribute, or deeply nested `@media`, **When** sanitized, **Then** no panic, bounded work within the existing 256 KB HTML cap, the style attribute dropped entirely on parse error (fail-closed), and the body remains readable with one plain notice that unsupported styling was removed.
7. **Given** any styled render, **When** the browser is observed, **Then** zero script executions, zero direct remote loads (fonts, images, stylesheets), zero same-origin API calls from mail content — remote images load only after the user's explicit Load-images consent, through the existing token-scoped proxy; sender CSS never reaches SPA chrome.

### US-5 — Reply all, sender choice and quoted context (P0) — F5, #1173

Today the panel's Reply fills only the original sender and an empty body — the other recipients are silently dropped and nothing is quoted (verified: `replyTarget` carries `from`/`subject`/`messageId` only; the compose dialog starts `cc`/`bcc` empty). This story puts **Reply all** beside **Reply**, fills recipients from one shared rule, and pre-fills an editable quoted original. The agent-side rule already merges To+Cc correctly (verified in `parseReplyRecipients`); this story extracts it so panel and agent share one implementation instead of growing a second copy.

**Position on plain Reply (decision, per the ADR's recommendation): plain Reply does NOT keep the other recipients.** It fills the sender/Reply-To only, Cc/Bcc empty. Reasons: (a) making plain Reply keep everyone would make it indistinguishable from Reply all; (b) a private response to one sender would be disclosed to the whole group by a single reflexive click — the graver failure; (c) the agent-side `reply_all=false` behaviour already works this way, so one rule covers both surfaces. Dropping Cc on **plain** Reply is not the defect; dropping it on **Reply all** is.

**Why this priority**: a founder quick win with real data-loss character (silently dropped group recipients).

**Independent test**: original From A, Reply-To R, To = self+X+duplicate-R, Cc = Y+mixed-case-X+self+display-name-duplicate-R, hidden Bcc. Reply all → To=R, Cc=X and Y exactly once each, no self/primary/Bcc duplication — asserted in the final composed/send payload, not just displayed chips. Reply → R only, Cc/Bcc empty. The quote is editable escaped Markdown; context response delays and mailbox/message changes cannot overwrite another message's compose input.

**Acceptance scenarios**:

1. **Given** an open message, **When** the user clicks Reply all, **Then** compose opens with To = Reply-To (else From), Cc = original To + original Cc minus the mailbox's own address and the primary, de-duplicated case-insensitively across the set including display-name duplicates — and the original Bcc never copied.
2. **Given** the same message, **When** the user clicks plain Reply, **Then** compose opens with the primary recipient only, Cc/Bcc empty — the other recipients are not silently kept.
3. **Given** the Reply-To header is absent, **When** either reply mode runs, **Then** the primary falls back to From; if self-exclusion leaves no eligible primary, editable empty recipients show rather than sending to self or guessing a replacement; an invalid supplied address yields an actionable error, not silent omission.
4. **Given** either reply mode, **When** compose opens, **Then** the draft contains an editable quoted original — readable body with escaped attribution (sender/date, or "No date") — with the original's Markdown/image/embed syntax escaped so the quote cannot load remote images or resolve workspace embeds inside Compose; no raw HTML/CSS paste; the original's files are not auto-attached; `Re:`/signature not duplicated; the human can edit or delete the quote before Send.
5. **Given** both the panel and the agent adapter, **When** recipients are computed, **Then** both call the one shared `BuildReplyRecipients` helper — the SPA implements no recipient algorithm; the context call creates compose state only, never a server draft, body cache or send; existing validation/recipient caps/threading rules are preserved.

### US-6 — Message-date fallback: never year one (P1) — F6, #1175

A message without a usable Date header currently renders as "1 Jan 1" in detail views (the list hides it, the detail does not — verified asymmetry). This story applies one normalized date rule everywhere: valid Date header → server internal/received date → null displayed as **"No date"**. Never a zero date, epoch, "today" or a blank cell.

**Why this priority**: small, self-contained correctness fix explicitly requested by the founder; no other story delivers it.

**Independent test**: fixtures with missing/unparsable/zero Date plus valid internal date; valid Date differing from received time; neither present. Assert displayed output (list row, detail header, Sent copy, reply attribution) — not just that a formatter parsed something.

**Acceptance scenarios**:

1. **Given** a message with a missing, unparsable or zero Date header and a valid internal date, **When** it is displayed (list, detail, Sent, agent result, reply attribution), **Then** the internal date shows — normalized once in the transport layer, not re-derived per surface.
2. **Given** a message with neither a usable Date nor an internal date, **When** displayed anywhere, **Then** exactly "No date" appears — never "1 Jan 1", never the epoch, never today's date, never a silently blank cell in the detail view.
3. **Given** a message with a valid Date, **When** displayed, **Then** the Date wins over the (possibly different) internal date, and the existing local display formatting is unchanged.
4. **Given** the wire contract, **When** date is absent, **Then** `date` serializes as explicit null (required-but-nullable), the generated consumers are regenerated before the Go/TS formatting changes, and no Go zero time is ever serialized.

---

## 5. Cross-cutting contracts from the grill findings

These four are the design's hardest boundaries. Each is stated as a rule a renderer, endpoint or migration must satisfy, with its falsification test named.

### 5.1 The temporary-Mail resource policy (grill I-04) — binds every reused renderer

**The rule.** A sender-authored URL is not trusted merely because it resolves to this same gateway. A temporary mail source carries an explicit, source-scoped resource allow-list passed into **every** reused renderer; only resources minted for that preview may load:

1. the preview's own token-scoped byte/representation responses;
2. CID inline parts of the **same message**, routed through the Mail preview prefix;
3. eligible remote images **only after** the user's explicit Load-images consent, through the existing token-scoped proxy.

Everything else is refused **structurally** — the policy's resolver never yields an authorized URL — even when same-origin, even when the ordinary renderer would display it: no arbitrary Library/API/workspace paths, no `libraryDownloadUrl` targets, no workspace embeds or wikilink resolution, no direct remote resources. The HTML iframe's CSP governs only the isolated HTML document; it does **not** govern the SPA-side Markdown/renderer path — which is exactly why this renderer-side policy exists separately. Ordinary workspace Library rendering keeps today's behaviour byte-for-byte; the policy scopes the temporary mail source, never the workspace source.

**Founder settlement (2026-10-02, mail-feature-decisions.md, "Q-E — a saved mail file's resource policy": A).** This policy applies to the **temporary preview only**. After Save, a saved mail-derived file is an **ordinary workspace file** for resource-policy purposes — its Markdown, images and links follow the ordinary workspace Library behaviour; the positive control above is the same file's behaviour post-Save. The saved-HTML exception (§5.4: scripts off by default + per-file checkbox) stands as already decided and is a preview-profile rule, not an extension of this resource policy to saved files.

| Reused renderer | What must route through the policy | Refusal shape |
|---|---|---|
| Markdown body (`KbMarkdownImage` et al.) | every image src, link href target resolution | image element never mounts; link renders inert |
| Embed/wikilink handling | embed targets, wikilink resolution | never resolved against the workspace/Library index |
| Media (video/audio) | media source URL | only the minted byte URL attaches |
| Text/code (view mode) | none (transient decoded content) | — |
| PDF | PDF source URL | only the minted byte URL loads |
| HTML (isolated frame) | governed by the Mail CSP itself | script-less, connect-less, no same-origin |

**Required counterexamples** (each a mail attachment's content; asserted with browser request counters — zero unauthorized requests, and refusals are structural, not hidden by CSS or an unmounted element):

| # | Counterexample content | Must show |
|---|---|---|
| C-1 | Ordinary Markdown image syntax `![x](/api/v1/workspaces/<id>/library/download?path=<real file>)` — a same-origin Library URL | Zero requests to the Library path; the marker file behind that URL is never served |
| C-2 | A wikilink (`[[some-note]]`) targeting a workspace note | Zero workspace resolution; inert rendering |
| C-3 | An embed directive targeting a workspace/Library resource | Zero embed resolution |
| C-4 | A remote image `![x](https://attacker.example/pixel)` without consent | Zero direct remote requests; loads only via the consent+proxy path |
| **Positive control** | The **same Markdown file opened as an ordinary workspace Library file**, and one **consented** Load-images case | The observer sees those requests fire — proving the instrument could detect the failure — and the consented proxy image loads only after explicit consent |

### 5.2 The byte-path distinction (grill I-05) — preview cap vs browser Download

**The rule.** Two distinct, server-enforced wire resources; one unlimited path shared by both purposes is forbidden.

| | Preview-purpose byte resource | Browser Download (existing endpoint, new streaming role) |
|---|---|---|
| Purpose | render inside the temporary viewer | save to the user's browser |
| Cap | the existing 25 MiB cap on **actual decoded bytes**, enforced server-side before success is committed | no preview cap — streams the part to completion (today's 413 for over-cap is replaced for this role) |
| Disposition | inline rendering projection, `Cache-Control: no-store` | `attachment` disposition, sanitized filename, existing header discipline |
| Binding | server-side bound to the minted `preview_id`/token, the selected `part_index`, and the grant's ownership lifecycle (view exit, expiry, revoke, panel close all kill it) | bound to the authorized pair/folder/message/part of the request |
| Issuer | URLs are server-issued, same-gateway, source-scoped — never caller-selected | unchanged request pattern |

**Late-failure ordering (part of the contract, not an implementation detail).** A size or decode failure discovered **during** the transfer — including one caused by false or unknown reported metadata — aborts the response with a visible typed error **before any success state exists**: no completed preview, no saved file, no resolved "download complete" from a truncated stream. Reported metadata (`reported_size_bytes`) may lie or be absent; **actual decoded bytes are always the cap authority**. The frontend must not commit success until the underlying stream completes; an aborted transfer is shown as failed, and Save-after-failure re-runs the whole fetch.

### 5.3 The handoff interaction contract (grill I-06) — focus, announcements, keyboard, reflow

Per the design system's accessibility release requirement (D16): focus visibility/restoration, status announcement, keyboard operation and zoom/reflow are release requirements, not preferences. Catalogued controls only (`button.tsx`, `icon-button.tsx`, `tooltip.tsx`, `dialog.tsx`, `FormError.tsx`, `checkbox.tsx`); no visual redesign. Ownership (grill-2 F-1): the handoff descriptor and the normative interaction contract are **w3's published freeze** (register R-1 — one publisher); this section states the obligations the Library side satisfies against that freeze, never a second contract.

| Moment | Required behaviour |
|---|---|
| Open (Mail → temporary viewer) | Focus moves to the viewer's trusted context heading — the "From mail: \<subject\>" bar — never into untrusted rendered content. The transition is announced so a screen-reader user knows the viewer opened and what it is. |
| Back | Focus returns to the originating attachment action (the specific Open control). If it no longer exists (list refreshed, message moved/deleted, panel changed): the message's list row, else the containing folder — each with an explicit announcement of where focus landed and why. |
| Save outcome | Loading, error and success (including the audit-warning save and the appearance of "Open in Library") are announced through a live status region **without moving focus**; success never silently steals focus to the Library panel. |
| Disabled actions | Every disabled stored-file action stays keyboard-discoverable and carries its "Save to Library first" explanation accessibly — not a tooltip-only hint; readable by screen readers, visible without hover. |
| Keyboard | Context bar, Save, Back and Retry reachable in a sensible tab order; Back/escape works from the keyboard alone; no focus trap escapes into browser chrome. |
| Narrow screens / reflow | The context bar wraps or stacks; every control and explanation stays reachable and readable at 320 px width and 200% zoom; nothing depends on hover or a wide viewport. |

### 5.4 The saved mail-derived HTML profile (founder Q5=A) — marker provenance

**What is settled.** Save keeps the attachment's **original bytes**. The saved file is marked mail-derived (`LibraryEntry.preview_profile=mail_restricted` plus a persisted, non-byte Library origin marker written **only** on explicit Save — never by Open) and renders with **scripts off by default**. A per-file checkbox (`checkbox.tsx`) may allow scripts for that one file, switching only that file's preview to the ordinary isolated workspace profile (`libraryIsolationPolicyTemplate`); the choice is visible, per file, never silent, never global. Browser Download supplies the untouched original.

**Marker provenance — the traced-and-proved obligation.** The marker must survive **move** (`Root.Rename`, `MoveInto`), **copy** (`CopyInto`), **rename** and **restore-from-backup** — the exact Library operations verified in this checkout. The backend Library owner traces each operation's implementation and proves marker survival before the behaviour is claimed; the test pack includes a restore-from-archive case.

**Marker unavailable — fail safe.** If a file's marker cannot be read (corrupt, absent on an operation that should have preserved it, or a pre-marker file that other evidence says was mail-derived), the preview **fails safe to the stricter profile** (scripts off). It never silently upgrades a known mail-derived file to the script-permitting profile. An ordinary workspace HTML file without a marker keeps today's ordinary profile unchanged.

**No third profile exists.** The two profiles are the ordinary workspace one (`allow-scripts`, no `allow-same-origin`) and the mail-derived one (scripts off entirely, `script-src 'none'`). The checkbox switches between them for one file; nothing else.

**Scope of the profile rule (founder Q-E=A, 2026-10-02).** This rule is about the **saved HTML preview**. A saved non-HTML mail file (markdown, text, image, PDF, …) is an ordinary workspace file: the §5.1 mail-restricted resource policy never follows the file after Save, and no mail-specific restriction attaches to it beyond this HTML profile.

---

## 6. Behavioral contract (quick reference)

- When Open is clicked on a supported attachment, the Library viewer shows it as a temporary read-only entry with the exact mail context bar — and writes nothing anywhere.
- When any stored-file action is attempted on a temporary preview, it is refused before any call leaves the SPA, with the "Save to Library first" explanation.
- When a preview's owner view exits (Back, close, navigate, reload, logout), its fetches abort, its token revokes, and a late completion is discarded.
- When Save to Library succeeds, exactly one new Library file exists under `mail/<mailbox>/<UTC month>/` with a sanitized, numbered-if-clashing name, an audit entry, and Open in Library available.
- When the Save response is lost, the client shows "Save result unknown", never auto-retries; an explicit same-token retry returns the prior receipt — exactly one file.
- When Download is clicked, the browser downloads exactly as today; no Library file, no preview cap.
- When an agent lists/reads attachments, only metadata/selected-part content moves — no file, no extra Seen; when it saves, the ordinary `ask` policy and Auto classification govern, and the result carries the real absolute workspace path.
- When sender markup in a temporary preview references anything but the preview's own minted resources, the resolver refuses structurally — zero requests.
- When mail HTML/CSS is sanitized, parsed-allow-listed styling survives (browser-verified), every deny-listed construct is dropped, and the two data:-URI paths are stopped by the sanitizer alone.
- When Reply all runs, the recipient set is original To+Cc minus self/primary, de-duplicated, never Bcc; plain Reply is sender-only; the quote is editable escaped Markdown.
- When a message has no usable Date, surfaces show the internal date or "No date" — never year one.

## 7. Explicit non-goals

| Non-goal | Reason |
|---|---|
| Save-a-temporary-file-before-Open | Violates the founder's option B / no-disk Open; would expose edit/path/download actions before consent. |
| A second preview implementation or a fake `LibraryEntry` path | Duplicates renderer/security behaviour and routes untrusted mail into workspace-lookup or script-permitting code paths never audited for it. |
| A recent-preview byte cache (encrypted or not) | Conflicts with the direct no-byte-cache rule and the request-only bodies/parts decision; the current-view rendering buffer is the only transient exception. |
| An attachment type blocklist, antivirus scanner, or attachment-specific approval | Founder F3: normal-download `ask` + no blocklist/scanner; a third policy layer contradicts the two-layer tool-policy model. |
| Reusing `sigStyleAttrRe` (or any character-class regex) for inbound styling | It admits `position:fixed`/`z-index` and bans parens, so it both under- and over-blocks; inbound needs declaration-level parsing (§7 of the CSS artefact). |
| Widening `signaturePolicy`/`outboundBodyPolicy` as a side effect | Outbound mail and stored signatures have their own attacker==victim posture; inbound work must not loosen them. |
| Plain Reply keeping the other recipients | Would make Reply indistinguishable from Reply all and risks disclosing a private response to a group (§4 US-5 position). |
| A new Office-document renderer, a new preview route/screen, or any visual redesign | Founder option B reuses the Library viewer inside its existing panel; unsupported formats keep the honest unsupported state. |
| Touching W1/W2/W3-core surfaces (pool, discovery, cache, refresh, paperclip) | Other package; this spec consumes their frozen interfaces only. |
| JMAP transport identity work | Phase 2; this package consumes the first-phase `message_ref` only. |

---

## 8. BDD scenarios

Conventions: one action per When; Type ∈ Happy / Alternate / Error / Edge; each scenario carries `Traces to:` naming its acceptance criterion. Backend scenarios run against the real in-memory IMAP harness (`startMemIMAP`); UI scenarios run in the component/E2E harnesses.

### Feature: Temporary attachment preview (Open without saving)

```gherkin
Scenario: Open a PDF attachment and view it (Happy)
  Given a message in the inbox with a 2 MB PDF attachment
  When the user clicks Open on that attachment
  Then the Library viewer renders the PDF inside the Library panel using the existing SPA renderer
  And the context bar reads exactly "From mail: <subject> · Back to mail · Save to Library"
  And no file exists anywhere under the workspace work root or any temp spool
  Traces to: US-1.AC-1

Scenario: Open writes nothing while Save's positive control writes (Edge)
  Given request/write observation is active on the server data directory and temp paths
  When the user opens an image, an SVG, a video, an audio file, a PDF, a markdown file and a text file in turn, then presses Back
  Then zero attachment-file, directory, spool or persistent-byte writes are observed for all opens
  And a positive-control Save of the same payload is observed by the same instrument
  Traces to: US-1.AC-1, US-1.AC-5

Scenario: Stored-file actions are dead, not hidden (Error)
  Given a temporary preview is open
  When the user attempts Library edit, PDF fill/sign, rename, move, copy, delete and Library-side download — including programmatic invocation
  Then each control is disabled with the visible "Save to Library first" explanation
  And zero mutation or API calls fire
  Traces to: US-1.AC-2

Scenario: HTML attachment renders scriptless (Error)
  Given an HTML attachment containing a script tag, an inline event handler, a form and a same-origin API fetch
  When the user opens it
  Then it renders through the token-scoped isolated Mail representation
  And no script executes, no form submits, no API call fires (browser counters)
  And the preview is never a raw HTML object URL, srcdoc, or the authenticated download URL served as a document
  Traces to: US-1.AC-3

Scenario: Over-cap attachment with honest known size is Download-only (Edge)
  Given an attachment whose descriptor size — the metadata known before any fetch — is 30 MiB, over the 25 MiB cap
  When the user views the attachment actions
  Then Open and Save are unavailable with the cap explanation, decided without fetching any part bytes
  And Download remains available and streams the part to completion
  Traces to: US-1.AC-4 (honest-size form)

Scenario: Over-cap discovered mid-transfer is a typed failure, never a success (Error)
  Given an attachment whose reported metadata claims 1 MiB (or is absent) while its actual decoded bytes are 30 MiB
  When the user opens it
  Then the preview transfer aborts mid-flight with the typed over-cap error before any success state exists
  And the viewer shows the failure with Retry/Back — never a truncated render — and Download still streams to completion
  Traces to: US-1.AC-4 (late-failure form), §5.2 late-failure ordering

Scenario: Exit disposes everything; late completion is discarded (Edge)
  Given a preview is open and its byte stream is still in flight
  When the user presses Back, then a slow fetch completes afterward
  Then the view's fetches aborted on Back, object URLs and the source token were revoked
  And the late completion resurrects nothing (no render, no state, no socket retained)
  Traces to: US-1.AC-5

Scenario: Markdown attachment with hostile same-origin references (Error)
  Given a Markdown attachment containing the I-04 counterexamples C-1..C-4 (Library URL image, wikilink, embed, remote pixel)
  When the temporary preview renders it
  Then zero unauthorized resource requests fire (browser request counters)
  And the preview's own minted resources still load
  Traces to: US-1.AC-1, §5.1

Scenario: Resource-policy positive control (Edge)
  Given the same Markdown file stored as an ordinary workspace Library file
  When it is opened in the ordinary Library viewer
  Then its same-origin image request IS observed by the counter — proving the instrument detects such requests
  And ordinary workspace rendering is unchanged from today
  Traces to: §5.1 positive control

Scenario: Navigating to a real Library file disposes the temporary source (Edge)
  Given a temporary preview is open
  When the user navigates to a real Library file (listing click, search result or deep link)
  Then the temporary source is disposed first — fetches aborted, source token revoked, media detached
  And it never becomes a breadcrumb, a listing row, a persisted-store entry or a fallback file
  And the real file renders with its full ordinary stored-entry capabilities
  Traces to: US-1.AC-6

Scenario: Unsupported format shows the honest state with Download available (Edge)
  Given an attachment in a format no Library renderer supports (no invented Office rendering)
  When the user opens it
  Then the Library's honest unsupported-format state shows inside the mail context bar
  And Mail Download remains available; no fake zero-byte file and no spool file is created
  Traces to: US-1.AC-7
```

### Feature: Save to Library and browser Download

```gherkin
Scenario: Save lands the file with sanitized name and audit (Happy)
  Given a preview or attachment row for "Q4 report.pdf" (2 MB) in mailbox user@ex.com, October 2026
  When the user clicks Save to Library
  Then the file exists at <work-root>/mail/user_at_ex.com/2026-10/Q4 report.pdf with byte-identical content
  And a mail.attachment_saved audit entry records actor, pair, folder/message reference, part index, original and final names, path and byte count — and no bytes, subject or token
  And Open in Library opens that stored entry with full stored-file capabilities
  Traces to: US-2.AC-1, US-2.AC-4

Scenario: Name clash gets a numbered suffix, never overwrite (Alternate)
  Given "report.pdf" and "report (1).pdf" already exist in the target month directory
  When the user saves an attachment named "report.pdf" (and its case-folded twin "Report.PDF")
  Then the saved files are "report (2).pdf" and "report (3).pdf" respectively
  And the final suffixed candidates each passed Library create-name validation
  Traces to: US-2.AC-2

Scenario: Hostile declared filename is sanitized then validated (Error)
  Given an attachment whose declared filename is "../../..\\evil:name?.pdf"
  When Save runs
  Then the stored name contains no separators, colons, control characters or dot-name segments (SanitizeAttachmentName output)
  And the final candidate passes Library path validation independently — sanitization is not the authorization
  Traces to: US-2.AC-3

Scenario: Audit failure after commit is saved-with-warning (Error)
  Given audit logging is enabled and the audit write fails after the file committed
  When Save completes
  Then the response is saved=true with an explicit audit warning and the real path
  And the UI shows the warning without advising a retry that would duplicate the file
  Traces to: US-2.AC-5

Scenario: Lost response then explicit same-token retry returns the prior receipt (Error)
  Given the server committed the unique file but the response was lost before the client read it
  When the client shows "Save result unknown" and the user explicitly retries the same save carrying the same save_operation_token
  Then the server returns the prior receipt — the original numbered name, path, size and audit status
  And exactly one file exists; no second numbered file was created
  Traces to: US-2.AC-6

Scenario: Different token is a new request; automatic replay never fires (Edge)
  Given a prior save whose outcome is unknown to the client
  When the user saves the same attachment again as a deliberate new action with a fresh token — and, separately, no automatic/background retry is triggered by the unknown state
  Then the new token performs a normal save (numbered suffix if the prior file exists)
  And no automatic replay request was observed at any point
  Traces to: US-2.AC-6

Scenario: Refusals name their cause and leave no partial file (Error)
  Given in turn: effective deny, declined ask, over-cap bytes, a parent path occupied by a file, a symlink/mount escape target, and a simulated disk-full
  When Save is attempted in each case
  Then each shows "Could not save to Library" with a safe specific reason (never a generic network error for permission/size)
  And no partial or incomplete file remains in any case (a cleanup failure is itself reported)
  Traces to: US-2.AC-7

Scenario: Browser Download is unchanged by Save work (Edge)
  Given any attachment, over-cap or not
  When the user clicks Download
  Then the browser download behaves exactly as today (destination, disposition, sanitized filename)
  And no Library file, listing entry or change notification results
  Traces to: US-2.AC-8
```

### Feature: Byte-path distinction (I-05)

```gherkin
Scenario: Over-cap part previews are refused even when metadata lies (Error)
  Given an attachment with actual decoded bytes of 26 MiB whose reported size claims 1 MiB
  When the preview-purpose byte resource serves the request
  Then the transfer aborts with the typed over-cap error before any success state is committed
  And the viewer shows the failure with Retry/Back — never a truncated render
  Traces to: §5.2, US-1.AC-4

Scenario: The same part downloads fully via the Download role (Happy)
  Given the same over-cap attachment
  When browser Download streams it
  Then the browser receives all 26 MiB under attachment disposition with the sanitized filename
  And the preview cap is not applied to this role
  Traces to: §5.2, US-1.AC-4, US-2.AC-8

Scenario: Mid-stream disconnect never reads as success (Error)
  Given a preview or download transfer in flight
  When the connection drops mid-stream
  Then the client shows a failed transfer for both roles
  And no completed preview render, saved file or download-complete state exists for the aborted stream
  Traces to: §5.2 late-failure ordering

Scenario: Preview grant dies with its view (Edge)
  Given a minted preview grant bound to a part
  When the view exits / the token expires / revoke is called / the panel closes
  Then the byte resource refuses with its indistinguishable not-available response
  And a replayed byte URL yields the same refusal
  Traces to: §5.2 binding row
```

### Feature: Agent attachment tools

```gherkin
Scenario: Full journey with no Message-ID (Happy)
  Given a message whose Message-ID header is absent, with two attachments
  When the agent chains read_message → list_email_attachments → read_email_attachment → download_email_attachment using only returned values
  Then every step succeeds; the reference was issued in read_message output and consumed unchanged
  And no step required or synthesized a Message-ID
  Traces to: US-3.AC-4

Scenario: Stale references are refused before fetch (Error)
  Given in turn: a reference from a wrong pair, from an old configuration/mapping generation, and from a recreated folder (old UID vs new UIDVALIDITY)
  When each is presented to an attachment tool (and to seen)
  Then each is refused with the typed stale-reference error before any fetch or mutation
  And the check ran on the same selected lease that would have performed the action
  Traces to: US-3.AC-4

Scenario: Listing fetches no bodies and no Seen (Alternate)
  Given a message with attachments and body bytes observable via server command counters
  When list_email_attachments runs
  Then only structure/part-header metadata was fetched (no body bytes)
  And the message's Seen state is unchanged by the listing alone
  Traces to: US-3.AC-1

Scenario: Policy ceiling self-heals; operator values survive (Edge)
  Given an install whose config predates the new tools (keys absent), and a second install with an operator-set download_email_attachment: deny
  When both configs load
  Then the first gains the literal allow/allow/ask entries additively
  And the operator's deny survives untouched; no per-agent deny backfill appears anywhere
  Traces to: US-3.AC-5

Scenario: Save under every permission mode (Alternate)
  Given Auto off, then an approval granted, declined; an explicit global deny; an agent ask/allow; God Mode
  When download_email_attachment is invoked in each mode
  Then Auto-off and declined ask perform zero transfer and zero write with the normal refusal
  And granted ask / God Mode save exactly as the panel does (same service, cap, naming, audit)
  And an agent allow never loosens the global ask; Auto-on runs via the ordinary workspace-path class
  Traces to: US-3.AC-6, US-3.AC-7

Scenario: Agent save result is a real file the agent's tools can read (Happy)
  Given an allowed agent save
  When the tool returns
  Then the result carries the workspace-relative path and the absolute path of the actual saved file
  And the agent's ordinary file tools read that exact path in the same turn
  Traces to: US-3.AC-7
```

### Feature: Styling (parsed CSS policy)

```gherkin
Scenario Outline: Deny-list constructs are dropped, safe siblings survive (Error)
  Given mail HTML containing "<case>" per the artefact's deny list
  When the preview renders
  Then the denied construct produces no effect (browser-verified where applicable)
  And unrelated safe content still renders
  Examples:
    | case |
    | style attribute with position:fixed;top:0;z-index:9999 alongside color:red |
    | the same declarations inside an @media block |
    | @import url("https://evil/x.css") alongside p{color:red} |
    | @font-face with a remote src and a data:font src |
    | background-image:url(data:image/svg+xml;base64,...) |
    | url("ht\\74 tps://evil/p") (escaped URL scheme) |
    | style="col\\6fr:red" (escaped property that decodes to a safe one) |
    | p{/**/position:fixed} (comment-spliced denied property) |
    | <img style="width:10px" onerror="..."> (style kept, handler gone) |
    | unclosed <style>p{color:red |
  Traces to: US-4.AC-1..US-4.AC-6

Scenario: Positive styling fixture renders styled (Happy)
  Given the artefact's positive fixture: colour, font, table/cell, border, spacing, class + style block, responsive @media
  When the preview renders in the browser
  Then computed-style assertions prove the safe styling survived
  And the founder-probe categories (4 style attributes, 1 style block, 1 class, 1 bgcolor, 1 <font> tag) all survive sanitisation
  Traces to: US-4.AC-1

Scenario: CSS url() backgrounds render only through the pinned proxy (Alternate)
  Given mail with background-image:url("https://good/img.png")
  When the mail is minted without, then with, explicit Load-images consent
  Then without consent: zero requests for the remote URL
  And with consent: exactly one request, to the token-scoped /mail-preview/img/... path carrying the pinned token — never the raw https URL
  Traces to: US-4.AC-2

Scenario: Sanitizer-only gates hold where the CSP would admit content (Edge)
  Given a data:-font @font-face and a data:image/svg+xml CSS url() in otherwise-safe mail
  When sanitized and rendered
  Then both are stripped — the CSP alone would have admitted both (font-src data:, img-src ... data:)
  And the served artifact is safe detached from any header
  Traces to: US-4.AC-3

Scenario: Bounded work under hostile CSS (Edge)
  Given an unclosed style block, a 1 MB style attribute and 100 nested @media blocks (within the 256 KB HTML cap)
  When sanitized
  Then no panic, bounded time/output, the oversize/parse-failed attribute dropped entirely
  Traces to: US-4.AC-6
```

### Feature: Reply all, plain Reply and quote

```gherkin
Scenario: Reply all merges and cleans the recipient set (Happy)
  Given original From A, Reply-To R, To = self+X+duplicate-R, Cc = Y+mixed-case-X+self+display-name-duplicate-R, hidden Bcc
  When the user clicks Reply all
  Then To = R; Cc = X and Y exactly once each (case-insensitive, display-name aware)
  And self and the primary appear nowhere in Cc; the original Bcc is absent
  And the final composed/send payload carries exactly this set — not just the displayed chips
  Traces to: US-5.AC-1

Scenario: Plain Reply keeps only the sender (Alternate)
  Given the same original message
  When the user clicks plain Reply
  Then To = R (Reply-To preferred), Cc/Bcc empty
  Traces to: US-5.AC-2

Scenario: Quote is editable and inert (Alternate)
  Given an original HTML-only body with images and embed syntax
  When Reply or Reply all opens compose
  Then the draft contains an editable quoted projection with escaped attribution (sender/date or "No date")
  And the quote cannot load remote images or resolve workspace embeds; no raw HTML is pasted; the original's files are not auto-attached
  Traces to: US-5.AC-4

Scenario: One shared rule; stale context cannot hijack compose (Edge)
  Given the panel and the agent adapter computing recipients for the same original
  When both resolve
  Then both used BuildReplyRecipients — identical sets, one implementation
  And a delayed reply-context response arriving after a mailbox/message change does not overwrite the now-open compose for a different message
  Traces to: US-5.AC-5

Scenario: No eligible primary after self-exclusion (Edge)
  Given a message whose only recipients are the mailbox's own address
  When Reply all opens
  Then editable empty recipients are shown — no send-to-self, no guessed replacement
  Traces to: US-5.AC-3
```

### Feature: Message-date fallback

```gherkin
Scenario Outline: Date precedence across surfaces (Happy / Edge)
  Given a message fixture "<fixture>"
  When it is displayed in the list row, detail header, Sent copy, agent result and reply attribution
  Then every surface shows "<expected>"
  Examples:
    | fixture | expected |
    | valid Date 2026-03-05T10:00:00Z, internal date differs | the Date value on every surface |
    | unparsable Date, internal date 2026-03-05 | 5 Mar 2026 (the internal date) |
    | missing Date, internal date present | the internal date |
    | zero Date, internal date present | the internal date — never "1 Jan 1" |
    | neither usable | "No date" — never year one, epoch or today |
  Traces to: US-6.AC-1..US-6.AC-3

Scenario: Null date is explicit on the wire (Edge)
  Given a message with neither date source
  When its summary/detail is served
  Then date serializes as explicit null (required-but-nullable), with generated consumers regenerated before the formatting change
  And no Go zero time ever serializes
  Traces to: US-6.AC-4
```

### Feature: Handoff interaction (I-06)

```gherkin
Scenario: Keyboard journey with focus restoration (Alternate)
  Given a keyboard-only user on an attachment row
  When they activate Open, then Save (once succeeding, once failing), then Back — including the case where the source message was deleted mid-view
  Then focus landed on the context heading at Open (announced), Save outcomes were announced without focus movement
  And Back restored the originating action, or fell back to the message row else the folder — each with an explicit announcement of where focus landed and why
  Traces to: §5.3

Scenario: Context bar at narrow width and zoom (Edge)
  Given the temporary viewer open
  When the viewport is 320 px wide and zoom is 200%
  Then the context bar wraps/stacks; every control and the Save-first explanation remain reachable and readable; nothing depends on hover
  Traces to: §5.3

Scenario: Saved HTML marker survives Library operations (Edge)
  Given a saved mail-derived HTML file, scripts off by default
  When it is renamed, moved, copied and restored from a backup archive, then reopened after reload
  Then it renders scripts-off after every one of those paths (provenance traced and proved, not assumed)
  And the per-file checkbox switches only that file to the isolated script-permitting profile — visible, per file, never global
  And a corrupt/missing marker fails safe to the stricter profile
  Traces to: §5.4, US-2.AC-1
```

---

## 9. TDD plan (tests designed from the DESIGN, before implementation)

Owner: qa-lead (file **w6** = ADR-W5, the proof package — package mapping above) — RED first against this spec, independent CHECK after GREEN; production owners never weaken these tests. Harness: real in-memory IMAP (`pkg/email/imapserver_test.go::startMemIMAP`) with command/byte counters; browser scenarios in the E2E suite with request counters. Go tests run under the repo's required tags; no full local suite — CI is the authority. All filenames below are new unless noted; none exists today (checked). Per register R-4, the end-to-end oracle for a published interface lives with the publisher's package: the single-part reader's and MIME classifier's own deep tests are w2's (register rows 14/15); this package's tests assert the seam through the frozen interfaces.

### 9.0 Plan of record and the w6 mapping (grill-2 F-4)

The register's Wave C dispatches qa-lead's RED **per the w6 plan** — so **w6 §6's rows and filenames are the plan of record**, and the rows in §9.1 below are this package's **coverage requirements** (what must be proven), not a second test plan. Two rules make the two documents one plan:

1. **Where a w6 §6 row covers the same oracle as a §9.1 row, the w6 file name is the one file that exists** (its `_red_test.go` name stays after green); the §9.1 filename for that row is the oracle's coverage description and must never be created as a second file (register R-4 — one test per oracle, no silent drift between two files asserting the same thing).
2. **Where w6's plan carries no row for an oracle below, the §9.1 filename is the assignment**: the ADR's W5 rule assigns exact test filenames in the revised spec, and w6 T28 already delegates the save-service test pack to this spec ("owned by w4-features/ADR-W7"). qa-lead writes those RED rows from this spec.

Mapping (§9.1 row → w6 §6 row(s), verified against w6 §6 this round): T1 → T26 + T28 + T34 (viewer-byte PEEK-only counters beyond those rows' aspects stay here); T2 → T28; T7 → T31; T9 → T30; T10 → T29 + T33 + T36 (list metadata-only/no-Seen rides T36's command capture; the read-outcome and missing-mailbox honest-result aspects stay here); T11 → T29 (the `allStaticToolNames` boot-panic guard stays here); T12 → T29; T13 → T32; T14 → T27; T17 → T35 + T38 + T31's browser leg. **Assigned here (no w6 row exists)**: T3 (save-token reconciliation), T4 (HTML-profile provenance), T5 (mint/binding/disposition/revoke beyond T26/T34's aspects), T6 (gateway save API end-to-end), T8 (gateway style-pipeline integration), T15 (pane integration; T35 covers the e2e keyboard/focus journey), T16 (panel attachment actions). §14's test IDs reference §9.1's numbering; each resolves through this mapping to its w6 row ID — the §10 DoD audit counts coverage on both documents' names and treats the w6-named file as satisfying the §9.1 row.

### 9.1 Test files and what each proves

| Order | Test file | Level | Proves |
|---|---|---|---|
| 1 | `pkg/mailattachment/service_test.go` | Unit/Integration | The shared `Transfer` service: selected-part PEEK only (byte counters), 25 MiB decoded cap authority, mode separation (viewer bytes / browser response / Library write), Open writes nothing (fs observation + Save positive control), refusal taxonomy. |
| 2 | `pkg/mailattachment/save_test.go` | Unit | Sanitize→validate chain (`SanitizeAttachmentName` then `ValidateCreateName`), parent creation via `Root.Mkdir`, numbered collision incl. case-folded, parent-file conflict refusal, complete-file publication (no partial file on failure; cleanup failure reported). |
| 3 | `pkg/mailattachment/save_token_test.go` | Unit/Integration | M-02: commit-then-lost-response; explicit same-token retry returns the prior receipt (exactly one file); different token = new save; automatic replay never fires; unknown state stays unknown until resolved. |
| 4 | `pkg/mailattachment/html_profile_test.go` | Unit/Integration | Q5=A: original bytes stored; marker written only on Save; `preview_profile` values; marker survival across `Root.Rename`, `CopyInto`, `MoveInto`, archive-restore; corrupt/missing marker fails safe to scripts-off; per-file allowance switches only that file. |
| 5 | `pkg/gateway/mail_attachment_preview_test.go` | Integration | Mint: metadata-only grant (no payload bytes in the token store), preview-purpose byte resource bound to preview_id/part/lifecycle, inline disposition + `no-store`, cap refusal ordering (late-failure before success), revoke/expiry/panel-close kill, Download role streams over-cap with attachment disposition, no-redirect discipline on the preview prefix. |
| 6 | `pkg/gateway/mail_attachment_save_api_test.go` | Integration | The `save-to-library` subresource end-to-end: success shape (real `LibraryEntry`, final name, relative+absolute path, audit status), refusal status codes, lost-response reconciliation over HTTP, audit event vocabulary/fields (no bytes/subject/token), mailbox-removal does not delete saved files. |
| 7 | `pkg/email/mailhtml/sanitize_test.go` | Unit | The CSS policy: all 73 allowed properties + `@media` pass; every deny-list entry dropped; the artefact's P1–P9 proof obligations; escape-decode ordering; fail-closed on parse error; bounded work; style-attribute and `<style>`-block paths; the two sanitizer-only gates (data:-font, data:image/svg+xml). |
| 8 | `pkg/gateway/mail_preview_style_test.go` | Integration | `mailSanitizePreviewHTML` + rewrite pipeline integration: style/class/style-block/bgcolor/`<font>` survive; CSS `url()` pinned and rewritten to `/mail-preview/img/<token>/<i>`; extraction covers CSS urls (not just `<img>`); 256 KB cap; CSP unchanged (`style-src 'unsafe-inline'`, `script-src 'none'`). |
| 9 | `pkg/email/reply_test.go` | Unit | `BuildReplyRecipients`: Reply-To preference/absence; reply-all merge (To+Cc), self+primary exclusion (case-insensitive), display-name/case de-duplication, Bcc never copied, invalid-address actionable error, no-eligible-primary empty set. |
| 10 | `pkg/tools/email_attachments_test.go` | Unit/Integration | Tool contracts: list (metadata only, no body bytes, no Seen), read (text vs unsupported/too-large outcomes, no file), download (same service, real path result, refusal zero-write), `message_ref` consumed unchanged, wrong-pair/old-generation/old-epoch refusals, no-Message-ID journey, missing-mailbox honest result, registration unconditional. |
| 11 | `pkg/config/policy_ceiling_test.go` (extends existing config tests) | Unit | Literal allow/allow/ask ceiling entries; `ReconcileToolPolicyCeiling` adds missing keys without overwriting operator values; no per-agent backfill (guard scripts still pass); `allStaticToolNames` contains the three names (boot panic guard). |
| 12 | `pkg/tools/auto_approve_mail_test.go` (extends existing) | Unit | Auto classification: list/read follow read tools; save resolves via the existing workspace-path conditional class; Auto-off/denied/God-Mode behaviour unchanged; agent allow cannot loosen global ask. |
| 13 | `pkg/gateway/rest_mail_date_test.go` (extends gateway mail tests) | Unit/Integration | Effective-date normalization: Date → internal date → null; explicit null serialization; no zero time on the wire; fixtures for unparsable/zero/missing. |
| 14 | `src/lib/mailAttachmentPreviewSource.test.tsx` (W5 frontend) | Component | Temporary source adapter: classifier feeding (extension-derived type, `text_readable` hint), capability flags, resource-policy resolver refusals (C-1..C-4 structural), minted-resource allow, object-URL/token disposal on unmount. |
| 15 | `src/components/library/LibraryPreviewPane.mailsource.test.tsx` | Component | Viewer integration: context bar exact text, all renderers mount from the temporary source, stored actions disabled with accessible explanation, HTML scripts-off profile, per-file checkbox behaviour, focus/announcement contract (I-06), 320 px/200% reflow; **US-1.AC-6 disposal-on-navigation** (navigating to a real Library file disposes the temporary source — it never becomes a breadcrumb, listing row, persisted-store entry or fallback file). |
| 16 | `src/components/workspaces/mail/MailPanel.attachments.test.tsx` | Component | Attachment list: Open/Save/Download actions with accessible names; over-cap Open/Save unavailable with cap explanation; failed save keeps the row; "Save result unknown" state and explicit retry flow. |
| 17 | `tests/e2e/mail-attachments.spec.ts` | E2E | Real browser journeys: Open→view→Back (focus restoration), Save→Open in Library, Download unchanged, keyboard-only journey, request-counter assertions for I-04 and styling remote-load checks, screen-reader announcement smoke; **US-1.AC-7 unsupported-format honest state** (no renderer → honest state inside the mail context bar, Download available, no fake file written). |

**Order**: 1–4 (service primitives) → 5–6 (gateway) → 7–8 (CSS) → 9–12 (reply/tools/policy) → 13 (dates) → 14–16 (components) → 17 (E2E). Each RED run is proven failing on the pre-change code (tests-only CI commit or the single dispatcher-owned narrow local run), then green.

### 9.2 Test datasets

| Dataset | Rows / shape | Exercises | Traces to |
|---|---|---|---|
| DS-ATT-PARTS | Nested multipart: 2 leaf attachments + inline CID image + draft-marker part + a genuine user `message.md`; one attachment with `DataUnavailable`; encoded (base64) vs decoded size mismatch; unknown size | Stable index resolution; marker exclusion; size honesty; indicator/list agreement | US-3.AC-1/2, DS rows in ADR test strategy |
| DS-SIZE | Decoded actual: 0, 1 B, 25 MiB − 1, **25 MiB exactly**, 25 MiB + 1, 30 MiB; reported metadata: true / absent / lying-high / lying-low | Cap boundary authority (decoded, not reported); Download-only above cap | §5.2, US-1.AC-4 |
| DS-NAMES | `../../..\\evil:name?.pdf`; empty; `...`; Windows-invalid (`con`, `aux`, trailing dot/space); 200-char name; unicode + combining marks; case-clash pair | Sanitize→validate chain; suffix behaviour; refusal classes | US-2.AC-2/3 |
| DS-SAVE-PATHS | missing parents; parent occupied by file; symlink/mount escape; existing dir reuse; two concurrent saves (panel+agent) same name | Path-safety primitives; concurrency without clobber | US-2.AC-1/2/7 |
| DS-TOKEN | commit→lost response; retry same token; retry different token; unknown-state observation window; server restart between commit and retry (receipt bounded — bounded reconciliation, not a general exactly-once framework: after restart the retry is a new save attempt that must not duplicate silently — it lands numbered and says so) | M-02 boundaries | US-2.AC-6 |
| DS-HTML-PROFILE | original-bytes HTML saved; marker present/corrupt/missing; move/copy/rename/restore; checkbox on/off; ordinary workspace HTML control | Q5=A provenance + fail-safe | §5.4 |
| DS-CSS | allow-list positive fixture (73-property coverage sample per group); P1–P9 inputs; escaped/shorthand/custom-property cases; oversized/unclosed/deep-nesting; 16 KB `<style>` cap; probe mail | Parser policy correctness + boundedness | US-4.AC-* |
| DS-REPLY | From/Reply-To/To/Cc/Bcc matrix incl. mixed case, display-name duplicates, self-in-To/Cc, self-only recipients, invalid addresses, missing Reply-To, HTML-only body | Recipient rule + quote safety | US-5.AC-* |
| DS-DATE | valid Date; unparsable; zero; missing (internal present); both missing; Date≠internal; list vs detail vs Sent vs agent vs attribution | Precedence + display parity | US-6.AC-* |
| DS-REFS | issued ref happy path; wrong pair; old generation; recreated folder (new UIDVALIDITY, old UID); ref after mailbox move | I-03 refusals on the same lease | US-3.AC-4 |

### 9.3 Counterexamples and mutation probes

A careless implementation would survive ordinary happy-path tests. These must die:

| # | Mutation a careless implementation would survive | Test that kills it |
|---|---|---|
| M1 | Preview byte resource shares the Download endpoint (one unlimited path) or skips the decoded-byte cap, trusting `reported_size_bytes` | DS-SIZE lying-low row: over-cap preview must abort despite truthful-looking metadata; Download must still complete (kills both the shared-path and metadata-trust mutations) |
| M2 | Resource policy implemented as "hide the element" (CSS/unmount) instead of resolver refusal | C-1..C-4 request counters — zero requests, plus a DevTools-free structural assertion that the resolver returns no URL; positive control proves the counter works |
| M3 | Sanitizer implemented as a regex/character-class (the `sigStyleAttrRe` school) | P4 escape set: `col\6fr:red` must SURVIVE (decoded-safe) while `ht\74 tps://` is dropped — a regex fails this asymmetry in one direction or the other; comment-spliced `position` must drop |
| M4 | Save-token reconciliation "replays" the save instead of returning the prior receipt | DS-TOKEN same-token row: exactly one file, original numbered name; replay would create `(1)` |
| M5 | Audit-failure-after-commit reported as failed save (inviting duplicate retries) | Audit-failure row: saved=true + warning + path; the UI copy must not advise retry |
| M6 | Marker stored as a byte-level HTML comment (travels with copies but is user-visible content and dies with byte transformations) or as a filename convention (dies on rename) | DS-HTML-PROFILE: marker survives move/copy/rename/restore invisible to the user; a comment-based marker fails the rename/reencode rows, a filename marker fails the rename row |
| M7 | Reply-all Cc computed in the SPA from displayed chips (second implementation, display-name duplicate leakage) | Reply dataset: final send payload assertion — chips hidden state can't satisfy it; mixed-case + display-name duplicate rows kill naive Set-dedup |
| M8 | Date fallback serialized as Go zero time / epoch instead of null | DS-DATE "neither" row + wire assertion: explicit null; `1 Jan 1` string assert on every surface |
| M9 | `read_message` attachment list computed from the sender's `Content-Type` header alone (type confusion) | DS-ATT-PARTS mislabelled part rows: effective extension-derived classification disagrees with the declared type — must follow the actual part |
| M10 | Open/Save allowed to fetch via the whole-message reader "temporarily" | Byte counters: only the selected part's bytes move (DS-ATT-PARTS counters), for Open, Save, agent read and Download alike |

### 9.4 Regression impact

Existing behaviour that must keep passing unchanged: the current attachment Download UX and headers (`handleMailAttachment`'s disposition/filename discipline); ordinary workspace Library rendering (stored entries, edit/rename/move, HTML `allow-scripts` profile); the existing six email tools' policies and `read_message`'s Seen behaviour; existing Mail panel list/detail rendering for dated messages; `pkg/email/view_missing_folder_test.go` suites; existing signature/outbound policies. New regression tests: ordinary-workspace-Markdown rendering unchanged under the resource-policy work (positive control lives in the suite permanently); Download role preserved for under-cap parts; existing reply tool behaviour unchanged after the `BuildReplyRecipients` extraction (same outputs on the existing test corpus).

---

## 10. Reachability — Definition of Done

Stated in this repo's two never-merged lines, with the specific evidence each requires:

**Line 1 — code correct and tested.** Every RED test from §9 shown failing on pre-change code (tests-only CI commit or the one dispatcher-owned narrow local run), then green in CI; the mutation probes of §9.3 executed and killed — **all of M1–M10, none optional at the gate** (M-5: M6–M10 guard the marker mechanism, the SPA reply rule, the wire null date, the type-confusion rule and the no-whole-message-fetch rule; M9/M10 guard P0 stories); `make verify-contracts` green on the ADR-W0 role's Wave B contract commits; design-system and budget gates green for the frontend slices; security review passed (§12).

**Line 2 — reachable by a user/agent.** Evidence that a real user and a real agent can invoke every feature, not that a library exists:

| Reachability claim | Required evidence |
|---|---|
| A user can Open/Save/Download from the Mail panel | Executed E2E (`tests/e2e/mail-attachments.spec.ts`) + a UAT lane against the real UI; screenshots with workspace badge visible |
| A user reaches the saved file in the Library | Executed Open-in-Library journey on the real file; Library list shows it |
| An agent can list/read/save | A real agent turn invoking all three tools against a configured mailbox, with the transcript showing tool results and the file readable by ordinary file tools |
| Every new tool is invocable (Hard Constraint #6) | `grep -rl '"list_email_attachments"' pkg/coreagent/ pkg/config/ pkg/tools/` non-zero across **all** registration points (§12 table); effective policy resolved `allow`/`allow`/`ask` for a seeded role asserted behaviorally (registry + policy resolution), not by grep alone |
| The renderer seats exist | `LibraryPreviewPane` mounts the temporary source in the running SPA (component test + E2E), not only in stories |
| The test plan was **executed**, not written | CI receipts attached per gate; "written, not executed" is not testing |

---

## 11. User-facing documentation TODOs (same change, drafted by the implementing lead, audited by docs-verifier)

| Page and section (verified in this checkout) | What must be said |
|---|---|
| `docs/mail.md` → **Read messages** | The paperclip list behaviour's sibling: the attachment name/size list; **Open** opens a temporary Library view (no file created, vanishes on exit/reload); the exact context bar and Back behaviour; scriptless HTML/SVG-as-static-image behaviour; the 25 MB preview/save cap with larger-files Download-only; Save vs Download distinction; the "Save result unknown" state after a lost response and what an explicit retry returns — the prior receipt while the gateway holds it (exactly one file), while after a server restart the receipt is gone and the retry is a new save that lands under a numbered name and says so (grill-3 M-6); what "Save to Library first" means for the disabled actions. |
| `docs/mail.md` → **Compose and send** | Reply vs Reply all recipient rules (Reply all keeps original To+Cc minus you, no duplicates, never the Bcc; plain Reply goes to the sender only); the quoted original is editable; Reply-To preference; Date → received date → "No date". |
| `docs/mail.md` → **If something goes wrong** | Refused Save causes (permission, size, path); saved-but-audit-warning is still saved; over-cap Download-only; agent attachment tools: list/read freely, save asks with the normal Auto-approve caveat; stripped-styling notice and remote images blocked until "Load images". |
| `docs/library.md` → **What the preview shows** | A Mail Open is not a file/list entry, bookmark or offline copy; no workspace path; vanishes on exit/reload; renderer support and unsupported formats; temporary read-only controls; Library Download/edit/rename/move/PDF fill-sign need Save first; the mail bar. |
| `docs/library.md` → **How to add files** | The `mail → mailbox → UTC month` hierarchy from Save; sanitized, numbered saved names; saved attachments are normal shared workspace files (mailbox deletion does not remove them; ordinary backup rules apply); the 100 MB chat-upload limit stays separate from Mail's 25 MB. |
| `docs/library.md` → **How to fill in and sign a PDF** | A temporary mail PDF must be saved first. |
| `docs/library.md` → **Limits and things to watch** | Mail-derived HTML: original bytes, scripts off by default, the per-file scripts checkbox (that one file only), marker survives move/copy/rename/restore, missing-marker fails safe. |
| `docs/security.md` | Temporary Open's no-disk/no-byte-cache rule; metadata-only token revocation; the temporary source's resource policy (sender content cannot fetch same-origin gateway resources); safe CSS/remote-resource limits and explicit image consent; scripts-off mail HTML vs workspace HTML isolation plus the per-file allowance; user-saved attachments vs the disposable Mail cache; ordinary tool ask/Auto/deny — there is no attachment-specific approval. |
| `docs/troubleshooting.md` | Preview expiry/Back/reopen; over-cap Download-only vs refused Save; the "Save result unknown" state and its receipt-retry; saved-but-audit-warning must not advise re-saving; directory/path/disk errors; agent permission refusals; stripped styling and "No date" explanations. |

docs-verifier audits every claim against the actual UI/handlers; security-lead checks the security wording before landing.

---

## 12. Hard Constraint #6 registration points (exact seams) and the required security review

### 12.1 Every touch point for the three new tools (all required; any missed point = unreachable or boot-breaking)

Ownership follows the ADR's work-package rows (grill-2 F-2): points 1–6 and 8–10 are this package's files (W10/W7 columns); **point 7 is w5-integration's Wave-D injection**, consuming the service this package delivers — no W7/W9/W10 implementer patches that file.

| # | Touch point | Change |
|---|---|---|
| 1 | `pkg/config/defaults.go::defaultToolPoliciesGeneral` (+ the ceiling composition) | Literal `list_email_attachments: allow`, `read_email_attachment: allow`, `download_email_attachment: ask` |
| 2 | `pkg/config/validate.go::ReconcileToolPolicyCeiling` | Additive self-heal for old installs; never overwrites operator values; no per-agent backfill (guard: `scripts/check-no-fail-closed-backfill.sh` stays green) |
| 3 | `pkg/coreagent/seed.go::allStaticToolNames` | The three names added (an override key absent here panics at boot via `validateOverrideKeys`) |
| 4 | `pkg/coreagent/role_policies_adr090.go::ADR090RolePolicyInventory` | list/read join `commonWork` (same grants as `read_message`); save granted `ask` to mail-reading roles; admin/hidden roles keep their narrower mail authority; sparse tightening preserved |
| 5 | `pkg/tools/auto_approve.go::autoApproveClasses` | list/read alongside the read tools; save → the existing workspace-path conditional class (Q4=A — no exception, no new mechanism) |
| 6 | `pkg/tools/email.go::EmailToolset` | Tool structs + set construction (permissions screen and tests follow) |
| 7 | `pkg/agent/email_tools.go::registerEmailToolsForAgent` — **edited by w5-integration in Wave D, not by this package** (ADR-20261001 W4 row: integration's exclusive file; grill-2 F-2) | Live wiring to the shared service, performed by w5-integration's injection from the service this package delivers (metadata-only registration is not execution). This package's obligation ends at supplying the service + `EmailToolset` entries and proving reachability (§10) |
| 8 | `pkg/tools/general_builtin_catalog.go::GeneralBuiltinMetadata` | Catalog entries (policy parity with the ceiling) |
| 9 | `pkg/gateway/gateway_sandbox.go::buildKnownBuiltinToolNames` + `::repairAndValidateToolPolicyCoverage` | Normal reconciliation/validation flow (no special case) |
| 10 | Tool descriptions | Text supplied by prometheus-prompt-engineer to W10's file — not a second editor |

### 12.2 Security review this package requires (in addition to the standard 5-reviewer gate)

1. **CSS parser/policy review (security-lead, before the styling wave lands):** the checked-in property/value table against the artefact; the `<style>`-block emitter (hand-rolled from parsed nodes — the artefact flags its serialiser absence as Inferred/medium, to confirm at implementation); parser resource exhaustion; nested-rule and escape bypasses; the CSS `url()` pin-then-rewrite extension's token binding; proxy no-redirect discipline.
2. **Saved-HTML provenance proof:** the marker's survival across `Root.Rename`/`CopyInto`/`MoveInto`/restore traced and proved; the fail-safe-on-missing-marker rule reviewed.
3. **Preview grant lifecycle:** metadata-only grants, same-lease reference validation, token revocation/expiry indistinguishability on the new preview prefix; no byte payload retention anywhere.
4. **Agent save path:** final-path authorization through `ResolveTurnFSPolicy`/`ResolvePath` before the authorized writer opens; no unconfined write of a checked string.

---

## 13. Open questions (with options and recommendations)

No founder-level question remains open (Q1–Q5 answered and recorded). The founder's 2026-10-02 spec-grill rulings (mail-feature-decisions.md, "Founder answers to the spec-grill decisions") are likewise settled: **Q-A** (the product owns its cache-directory exclusion from staging and from the application's own backups/archives on every install — this spec depends on no personal machine configuration; §16 records the sweep), **Q-B** (watcher state excluded and purged on mailbox removal — w1/w5's surfaces, not touched here), **Q-C** (refresh-on-open keeps the design's stale-gating — out of this package's scope), **Q-D** (folder search: subject + sender/recipient, server-side, 25/200 bounds — w2's surface, not touched here), **Q-E** (saved mail file = ordinary workspace file; recorded in §5.1/§5.4). None is reopened by this spec. The five items below are **engineering** decisions, labelled **E-1…E-5** — deliberately not Q-*, because Q-A…Q-E above are the founder rulings' recorded labels, and an interview answer recorded as "Q-C = A" must mean the ruling, never the checkbox location (grill-2 F-7). Each has a status and owner; work proceeds on the stated default.

**E-1 — The mailbox label inside `mail/<mailbox>/`.**
Options: (a) sanitized mailbox address alone; (b) sanitized address + owning agent ID always; (c) address alone, agent-ID suffix only when two pairs would share a label.
**Recommendation: (c)** — readable by default, disambiguated only on real collision, exactly the ADR's "sanitized mailbox address disambiguated by the validated owning agent ID" intent. The save service computes it once, server-side; never agent-supplied.
**Status: open — owner: W7 implementer, architect sign-off at implementation start; default (c).**

**E-2 — What `read_email_attachment` returns for images/documents.**
Options: (a) text-only first wave — binary formats return descriptor + explicit unsupported + the Save option; (b) full visual/document reader representation in the same wave.
**Recommendation: (a) with (b) gated on the traced reader integration.** The ADR marks the visual/document tool-result adapter as Unknown-until-traced; shipping list+text+save with an honest unsupported outcome is reachable and safe immediately; the visual representation lands as a follow-up once the reader path is traced and demonstrated. No fake "read" for binary ever ships.
**Status: open — owner: W10 implementer with architect; default (a), (b) gated on the traced reader path.**

**E-3 — Where the per-file scripts checkbox lives.**
Options: (a) in the preview pane header (only visible when viewing the file); (b) in the Explorer's per-file action menu as well.
**Recommendation: (a) for the first wave** — the decision is about *viewing* this file with scripts; keeping it at the point of viewing is the smaller, clearer surface. Adding it to the action menu later is additive.
**Status: open — owner: W8 implementer under the design-system rules; default (a).**

**E-4 — `<body>`-attribute honouring for saved/preview HTML.**
Options: (a) leave body-level `bgcolor`/`background`/`text` unreachable (fragment sanitization drops them); (b) `serveHTML` wraps the fragment in fixed boilerplate carrying the sanitized body values.
**Recommendation: (b)** — the artefact holds the three attributes in the allow-list either way and names the wrapper as the only way to honour them; the boilerplate is fixed and carries only sanitized values. Small cost, visible fidelity win for real mail.
**Status: open — owner: W9 implementer, confirmed inside the §12.2 security-lead CSS review; default (b).**

**E-5 — Style-attribute and `<style>` size caps.**
Options: (a) 4 KB attribute / 16 KB block (Gmail-parity, artefact's suggestion); (b) tighter custom bounds.
**Recommendation: (a)** — parity with the reference clients is the compatibility target; both sit well inside the existing 256 KB HTML cap.
**Status: decided — the CSS authority (`receipts/css-allowlist.md`) is adopted as-is (see header), which fixes (a)'s numbers; there is no remaining choice.**

---

## 14. Traceability matrix (story → scenarios → tests)

| Story | BDD scenarios (§8 feature) | Tests (§9) |
|---|---|---|
| US-1 Open without saving | Temporary preview (all 11, incl. the I-6 two-form over-cap pair and the AC-6/AC-7 scenarios) | T1, T5, T14, T15, T17 |
| US-2 Save + Download | Save/Download (8) | T2, T3, T4, T6, T16, T17 |
| US-3 Agent tools | Agent tools (6) | T10, T11, T12, T1 (shared service), T17 |
| US-4 Styling | Styling (5 — the Outline + four Scenarios) | T7, T8, T17 |
| US-5 Reply all | Reply (5) | T9, T16 (compose integration) |
| US-6 Date fallback | Date (2) | T13 |
| §5.1 resource policy | C-1..C-4 + positive control | T14, T17 |
| §5.2 byte paths | Byte-path (4) | T1, T5 |
| §5.3 handoff | Handoff (3) | T15, T17 |
| §5.4 HTML profile | Saved-HTML marker | T4, T15 |

Every FR-level statement in §§4–7 traces to a story above; every story has scenarios; every scenario names tests. Test IDs are §9.1's numbering, resolved to their w6 §6 row IDs (and to "assigned here") through the §9.0 mapping — a §14 anchor that resolves to a w6 row is satisfied by that w6-named file, never by creating §9.1's descriptive filename as a second file (grill-2 F-4). Holdout checks (post-implementation, outside the matrix): (1) a colleague opens a real styled marketing mail and confirms it "looks like an email"; (2) a message with no Date shows "No date" everywhere including a reply quote; (3) a saved HTML attachment moved to another folder still opens scriptless with the checkbox working.

---

## 15. Implementation sequence within this package

1. **Contracts first — Wave B, owned by backend-lead in the ADR-W0 role** (preview mint/response, save subresource + token shapes, tool results, `message_ref` field on descriptor outputs, nullable date, `preview_profile`/allowance field) — regenerated and committed before any handler/consumer; there is no W0 spec file, so a Wave B that has not started is a blocked report to team-lead, never a stub (register §5).
2. **W7 service + token reconciliation + marker storage** against tests 1–4.
3. **W8 viewer source union + resource policy + handoff integration against w3's frozen descriptor** (F-1: w3 publishes the descriptor/contract, W8 consumes and honours it) against tests 14–15; I-06 interaction contract in the same slice.
4. **W10 tools + policy catalog + `BuildReplyRecipients`** against tests 9–12; `read_message` descriptor extension rides it. Dependency (grill-2 F-2): the tool-path `message_ref` (US-3.AC-2/AC-4) is issued by w5-integration — the single issuer including the agent's read result (w5 US-9) — and reaches the tools through w5's Wave-D injection; in Wave C the tools are built against the frozen interface and the full no-Message-ID journey goes green in Wave D. A missing Wave-D issuer is a blocked report to team-lead, never a local mint (register rows 8/16).
5. **W9 CSS policy** behind its security-lead review against tests 7–8 — the sequence keeps the founder's quick wins (reply, dates) and the attachment flow ahead of full styling.
6. **Dates (transport → contract consumers → display)** — small, independent, land early per the founder's ordering.
7. **E2E + docs + docs-verifier + UAT** close the package.

---

## 16. Evidence table (spec-writing claims)

| Claim | Evidence | Certainty |
|---|---|---|
| Design authority read in full | ADR-20261001 (807 lines, two reads), `adr-grill-report.md` (183 lines), `receipts/css-allowlist.md` (241 lines) — all read this session | Verified |
| `SanitizeAttachmentName` exists and behaves as described | `grep -n "func SanitizeAttachmentName" pkg/email/view.go` → line 970, wraps `sanitizeMailPartName` (line 812); body read | Verified |
| 25 MiB cap constant | `pkg/email/view.go`: `const maxViewPartBytes = 25 << 20` | Verified |
| Library primitives exist as cited | `grep -n "^func "` on `pkg/library/transfer.go`, `mkdir.go`, `root.go` → `CreateUnique` (transfer:151), `Mkdir` (mkdir:28), `CleanRelPath` (root:384), `ValidateCreateName` (root:490), `MountAt` (root:299), `resolve` (root:264), `HostPath` (root:310), `Rename` (transfer:56), `CopyInto` (transfer:196), `MoveInto` (transfer:279) | Verified |
| Today's single byte path + 413 | `pkg/gateway/rest_mail_read.go::handleMailAttachment` (line 324) read: whole-message `ReadView` → `mailPartByStableIndex` → 413 on `DataUnavailable` → `SanitizeAttachmentName` → extension-derived type | Verified |
| Epoch hazard symbols | `parseMailRef` (view:140), `ReadView` (view:486), `ResolveRef` (view:935), `MarkSeenIn` (view:849), `handleMailSeen` (rest_mail_read:265) located | Verified |
| No internal-date fallback today | `view.go:348` `Date: buf.Envelope.Date.UTC()`; `view.go:559` zero-Date omit; no `InternalDate` hit in view.go/transport.go | Verified |
| Panel drops Cc / no quote | `MailPanel.tsx:549` `replyTarget = { from, subject, messageId }`; `MailComposeDialog.tsx` `to/cc/bcc: EMPTY_RECIPIENTS` | Verified |
| Agent reply-all rule already merges To+Cc | `pkg/tools/email_compose.go::parseReplyRecipients` (line 490) read in full; `replyAllArg` (line 548) | Verified |
| `sigStyleAttrRe` is character-class-only | `pkg/email/compose.go:209` regex read | Verified |
| Sanitizer strips styling today; pin-then-rewrite is img-only | `rest_mail_preview.go`: `mailSanitizePreviewHTML` (267), `mailImageSrcRe` (287), `mailRewritePreviewSources` (297), `mailImgTagRe` (351), `mailExtractRemoteImageURLs` (358), `mailPreviewMaxHTMLBytes = 256 << 10` | Verified |
| CSP and Library template as described | `mail_isolation_policy.go::mailIsolationPolicy` (32); `library_isolation_policy.go::libraryIsolationPolicyTemplate` (136, `allow-scripts`); `MailHtmlFrame.tsx::MAIL_HTML_FRAME_SANDBOX` (27); `LibraryPreviewPane.tsx` `sandbox="allow-scripts"` (580) | Verified |
| Preview grant retains bytes / 15 min | `mail_preview_token.go`: `MailPreviewTokenTTL` (24), `mailPreviewGrant` (69) | Verified |
| I-04 frontend seams | `url-safe.ts::isDisplayableImageSrc` (32); `KbMarkdownImage.tsx` imports it (51), gates (88), mounts (97/102); `library.ts::libraryDownloadUrl` (1190); `embed.go::spaBaseContentSecurityPolicy` (188) | Verified |
| Policy/registration seams | `defaults.go::defaultToolPoliciesGeneral` (188) with email entries (300–304); `validate.go::ReconcileToolPolicyCeiling` (841); `compositor.go::resolveEffectivePolicyWith` (163); `auto_approve.go::autoApproveClasses` (140), `AutoWorkspacePath` (389); `role_policies_adr090.go::ADR090RolePolicyInventory` + `commonWork` (29); `seed.go::allStaticToolNames` (80, email names 141); `registerEmailToolsForAgent`/`SetSharedMailBudget` (email_tools.go 63/61); `gateway_sandbox.go` (123/256) | Verified |
| Audit vocabulary gate | `audit.go::IsValidEventName` (403); `events.go` mail constants (401–416); no `mail.attachment_saved` exists | Verified |
| Contract baselines | `MailAttachment.yaml` required fields (9–13); `MailMessageSummary.yaml` `date` required + `format: date-time` (10/20/81–84); `LibraryEntry.yaml` path required (12–14); `openapi.yaml::getMailAttachment` (10712) | Verified |
| Viewer/Explorer seams | `LibraryPreviewPaneProps` (108), `LibraryTextBody` (623), `LibraryHtmlFrame` (438); `LibraryExplorer` selection (429–451), `handleDownload` (894), `renameMutation` (689), `transferMutation` (721); `libraryPreviewKind.ts::classifyLibraryEntry` (74) | Verified |
| UI catalog present | `ls src/components/ui/` → button/checkbox/dialog/FormError/icon-button/tooltip (all with tests/stories) | Verified |
| Date formatter asymmetry | `mail-format.ts` read (formatters reject NaN, not year one) | Verified |
| Test harness | `imapserver_test.go::startMemIMAP` (35) | Verified |
| Proposed files do not exist yet | `mailAttachmentPreviewSource.ts`, `pkg/mailattachment/`, `pkg/email/reply.go`, `pkg/tools/email_attachments.go`, `pkg/email/mailhtml/` — each checked absent | Verified |
| docs sections named for TODOs | `grep '^## '` on `docs/mail.md`, `docs/library.md` | Verified |
| Correction round applied 2026-10-02 | Findings naming this spec: `adr-grill-3.md` I-1, I-6, M-4, M-5, M-6 and `adr-grill-4.md` C3, I6 — each corrected in text above per `mail-live-access-landing-order.md` (read in full this session); §2/§3/§4/§5/§8/§9/§10/§11/§13/§14/§15 carry the visible corrections | Verified |
| Register rows applied to this spec | rows 1/8 (W0 = backend-lead role, Wave B, no spec file), 9 (pool/lease = w1), 14/15 (part reader + MIME classifier = w2), 16 (`message_ref`: w5-integration issues, w1/w2 validate), 17 (emitter split — not consumed here), 19 (`recordFailure` = w1, `mailErr502` = w5 — not touched here), 22 (token shapes = W0; bounded lookup = W7; retry FR = w3), 24 (mapping line in header); rows 4/6/5/11 named as not-consumed in §3.2 | Verified |
| No dependency on any personal machine configuration (founder Q-A, product terms) | Case-insensitive grep over this spec for the machine-provisioning vocabulary (the personal setup-repo name, the macOS scheduler job, the auto-commit job, the secrets scanner, the personal backup remote, ignore/excludes-file mechanisms) → 0 hits, exit 1, re-run on the final text; the exact pattern is recorded in this round's final commit message so the check stays reproducible without embedding its terms here | Verified |
| Shared-interface terms absent from this spec | `grep -n -i "next_cursor\|has_more\|view_limit_reached\|observer_id\|recordFailure\|mailErr502\|duration_ms\|acquire_wait\|socket_count"` → 0 hits; the three "instrument" hits (§5.1 positive control, §8 scenarios) are the counterexamples' observation instrument, not the w6 emitter | Verified |
| Founder rulings Q-B/Q-C/Q-D do not touch this spec | `email-watch`/`watcher` → 0 hits (Q-B); "refresh" only in out-of-scope statements and Library change-notification/focus contexts (Q-C); "search" only as the existing `search_email` tool name and Library listing search (Q-D) | Verified |
| Worktree/commit state | Branch `docs/adr-mail-live-access`; the original spec plus this correction committed as spec-only commits authored `Daniel Piatkowski <10800669+daniel-piatkowski-ai@users.noreply.github.com>`, no co-author trailers (verified per commit via `git log -1 --format='%h %an <%ae>'`) | Verified |
| **Self-check** | Re-read the corrected spec end-to-end after the final commit; re-ran the personal-machine-configuration and shared-interface sweeps on the post-correction file — both clean; re-checked every register row named in §3 against the register text; every grill finding named in the header has a visible in-text correction and, where the grill required one, a test or counterexample in §8/§9 (I-6 → two-form over-cap pair; M-4 → AC-6/AC-7 scenarios + named tests in T15/T17; M-5 → DoD all-M1–M10; M-6 → mail.md restart wording). Forbidden moves NOT committed: no test weakened, deleted or loosened (tests are named, never gated to pass); no limit widened (25 MiB cap, 256 KB HTML cap, 4 KB/16 KB CSS caps restated unchanged); no stub type, parallel wire type or second implementation created (§3.2 fence); no silent scope change (edits are the dispatch's corrections; §13 records what remains open and who owns it). Only this spec file was edited; no production code written, no contract file edited, no test executed, no push performed | Verified (scope/artifact); implementation outcomes Unknown |

---

*Spec ends. Skills: omnipus-shared-rules, plan-spec, omnipus-frontend-rules (register), omnipus-backend-rules (register).*
