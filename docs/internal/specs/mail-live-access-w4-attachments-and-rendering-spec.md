# Implementation Specification: Mail live access W4 — attachments, agent tools, styling, reply and dates

**Created**: 2026-10-02
**Status**: Proposed — awaiting the prescribed grill rounds. Design is settled: [ADR-20261001 — Mail live access: pooled connections, folder discovery and a bounded cache](../architecture/ADR-20261001-mail-live-access-pooling-folder-discovery-and-cache.md) (correction round applied), its review `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-report.md` (findings I-01–I-06, M-01–M-02 applied), and founder answers **Q4=A / Q5=A** recorded 2026-10-02 in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/mail-feature-decisions.md`.
**Work package**: the "sender, attachments and rendering" half of the Mail live-access feature — design work packages **W7–W10** plus the F1–F6 feature rules in the ADR's "Sender, attachments and rendering" section. Closes #1170 (Save/Download), #1171 (agent attachment tools), #1172 (styling), #1173 (Reply all), #1174 (preview half — Open without saving), #1175 (date fallback).
**Out of this spec (other package)**: pooled sockets, folder discovery, the bounded cache and panel refresh (W1/W2/W3/W4-core — the ADR's Phase 1/2 decisions); the list paperclip indicator and its `has_attachments` metadata fetch (W2/W3); contracts themselves (W0 — this spec names shapes, never edits them).
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
- The Mail→Library handoff interaction contract: focus, announcements, keyboard, reflow (grill I-06).
- The shared attachment transfer/save service used by panel and agent alike, its save-operation-token reconciliation for a lost response (grill M-02), its audit event, and its refusal cases.
- The saved mail-derived HTML profile (founder Q5=A): original bytes, persisted mail-derived marker, scripts off by default, per-file scripts checkbox, marker provenance across move/copy/rename/restore.
- The parsed CSS policy: allow-list from the security artefact, `<style>`-block pass, `url()` pin-then-rewrite extension, and the two data:-URI paths where the sanitizer is the only gate.
- The extracted transport-neutral reply-recipient helper and the gateway reply-context operation.
- The nullable effective message date across transport, contract and display.
- Registration/policy wiring for the three new tools at every Hard Constraint #6 touch point.

### 1.3 Explicitly out of scope (non-goals — detail in §10)

No pooled sockets, no folder discovery, no header cache, no panel refresh timers (W1–W4 core). No new Office-document renderer. No attachment type blocklist, antivirus scanner, or attachment-specific approval layer. No outbound-mail or stored-signature policy widening. No byte cache of previews, bodies or parts — not even an encrypted one. No change to browser Download's destination or disposition. No JMAP identity work beyond consuming the first-phase `message_ref` that the read-runtime package issues.

---

## 2. Existing Codebase Context

> GitNexus MCP tools were not connected in this writing session; per shared rule 9 the analysis below is first-hand Read/Grep exploration and impact rows are labelled **Inferred**. Every symbol cited below was opened in this checkout on 2026-10-02 unless marked otherwise.

### 2.1 Backend symbols this work touches

| Symbol | Role in this feature | Change / blast radius |
|---|---|---|
| `pkg/email/view.go::ReadView` | Today's whole-message reader: `BODY.PEEK[]` of the entire message, then part selection. `handleMailAttachment` rides it today. | **Consumed, not edited here** — W2 owns `view.go`. This package needs a targeted single-part reader (W2's `attachment_parts.go`) injected into the new save service. Blast radius: every Mail read path. |
| `pkg/email/view.go::MailPart`, `::maxViewPartBytes` (`25 << 20`), `::mailAddressableParts` | The stable leaf `part_index` enumeration (draft-body marker excluded) and the existing 25 MiB decoded-byte cap. `OmnipusDraftBody` marks the draft bookkeeping part. | **Consumed.** The cap number and the index semantics are reused unchanged — no new size number, no new index scheme. |
| `pkg/email/view.go::SanitizeAttachmentName` (wraps `::sanitizeMailPartName`) | Existing sanitizer: separators/colon → hyphens, control characters removed, dot/dot-dot neutralized; empty → `attachment`. **Verified this checkout; already used by the attachment Download path.** | **Consumed** by Save. Sanitization is name hygiene only — Library path validation (`CleanRelPath`, `ValidateCreateName`) is the actual filesystem authorization; both apply. |
| `pkg/email/view.go::ResolveRef`, `::MarkSeenIn`; `pkg/gateway/rest_mail_read.go::handleMailSeen` | The verified epoch hazard: `ResolveRef` returns the current epoch with a supplied UID without comparing them, and `handleMailSeen` discards that epoch before `MarkSeenIn` opens its own session. | **Not edited by this package**, but the reference-validation seam (grill I-03 / the ADR's integration note) lands in the read-runtime package: every attachment operation must compare the reference's epoch and pair/config generation **on the same selected lease** that fetches. This spec's tools consume the gateway-issued `message_ref` unchanged; the refusal behaviour is specified in US-3. |
| `pkg/email/transport.go::Message` | Carries `UID` and an **optional** `MessageID` (`omitempty`), no `UIDVALIDITY` — the verified I-03 gap: agent output today cannot always produce an attachment reference. | **Consumed.** First-phase reference issuance is the read-runtime package's deliverable; this package's tools require it. |
| `pkg/gateway/rest_mail_read.go::handleMailAttachment` | Today's single byte path: whole-message `ReadView` → `mailPartByStableIndex` → **413** for `DataUnavailable` → `SanitizeAttachmentName` → extension-derived content type → browser attachment disposition. This is why "larger files: Download only" is not working today. | **Modified by this package's gateway work**: the endpoint gains the streaming Download role (no 413 for over-cap when the caller asked for a download), while the new preview-purpose resource takes the capped role. Contract-first (W0 splits the two before either consumer changes). |
| `pkg/gateway/inline_serving.go::applyMailByteHeaders`, `::mailContentDisposition` | Existing header seam: always `attachment` disposition, `nosniff`, `no-store`. | **Consumed/extended** — Download keeps attachment disposition; the preview-purpose resource serves inline disposition with `no-store` and the Mail isolation policy. One shared header seam, W4-owned files. |
| `pkg/gateway/mail_isolation_policy.go::mailIsolationPolicy` | The Mail HTML CSP: `script-src 'none'`, `style-src 'unsafe-inline'`, `connect-src 'none'`, sandbox without scripts/same-origin/forms, `img-src` confined to the Mail preview prefix + `data:`. | **Consumed unchanged** for the temporary HTML preview and the saved mail-derived profile. The CSS artefact confirms: **no CSP change is needed or requested.** |
| `pkg/gateway/library_isolation_policy.go::libraryIsolationPolicyTemplate` | The ordinary workspace Library HTML profile: `allow-scripts`, no `allow-same-origin`. | **Consumed.** The per-file scripts checkbox (Q5=A) switches one mail-derived file to exactly this existing profile — never a new third profile. |
| `pkg/gateway/mail_preview_token.go::mailPreviewGrant`, `::MailPreviewTokenTTL` (15 min) | Today's grant **retains payload bytes** (HTML/inline) — the ADR retires that for mail previews. | **Extended by this package's gateway work**: the new attachment preview grant carries authorization/reference metadata only; each byte/representation response fetches live through the shared pool/budget. No byte payload in the token store. |
| `pkg/gateway/rest_mail_preview.go::mailSanitizePreviewHTML`, `::mailRewritePreviewSources`, `::mailExtractRemoteImageURLs`, `::mailImgTagRe`, `::mailImageSrcRe`, `::mailPreviewMaxHTMLBytes` (256 KB) | Today's inbound HTML sanitizer: strips `style`/`class`/`bgcolor`/`<font>` (no style policy at all); the pin-then-rewrite remote-image pipeline scans `<img>` tags only; remote images ride the token-scoped, SSRF-screened proxy (`::fetchRemoteImage`). | **Extended by this package**: `mailSanitizePreviewHTML` calls the new parsed CSS policy (W9's package) for style attributes and `<style>` blocks; the pin-then-rewrite pipeline gains CSS `url()` extraction/rewrite so backgrounds render. `mailImageSrcRe`'s value shape (token-scoped proxy path, `data:image/(png|gif|jpe?g|webp);base64,`) is the exact shape CSS `url()` values must satisfy. |
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
| `pkg/agent/email_tools.go::registerEmailToolsForAgent`, `::SetSharedMailBudget` | Live registration of the email tools for every agent + the shared budget injection seam. | **Extended**: the new tools wired to the shared service (metadata-only registration is not execution). Unconditional registration retained — a missing mailbox is an honest data result, not an absence. |
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

### 2.3 Contracts this package consumes (W0 owns the files)

Existing shapes read this checkout, to be changed contract-first by W0 before any handler/consumer lands (Hard Constraint #8):

- `contracts/components/schemas/MailAttachment.yaml` — `part_index`/`filename`/`content_type`/`size_bytes` required; **`size_bytes` is non-null today** and becomes nullable (unknown honestly), with optional labelled `reported_size_bytes`; every output carrying the descriptor also carries the gateway-issued `message_ref` (grill I-03).
- `contracts/components/schemas/MailMessageSummary.yaml` / `MailMessage.yaml` — `date` is required non-null `date-time` today; becomes **required but nullable** (the effective date; null = neither source usable).
- `contracts/components/schemas/LibraryEntry.yaml` — `path` required; stays real-path-only. The temporary preview is **never** a manufactured `LibraryEntry`; the source union is its own generated shape.
- `contracts/openapi.yaml::getMailAttachment` — today's single shared byte operation; W0 splits the preview-purpose resource from the Download streaming role contract-first (grill I-05).

Proposed new shapes (names from the ADR's wire table; W0 defines and regenerates them first): `MailAttachmentPreviewRequest`/`MailAttachmentPreviewResponse` (with `content_source.byte_url` = the dedicated preview-purpose endpoint, `preview_id`, `text_readable`, `read_only=true`), the `save-to-library` subresource with `MailAttachmentSaveRequest` (opaque `save_operation_token`) / `MailAttachmentSaveResponse` (real `entry: LibraryEntry`, `path`, `absolute_path`, `audit_status=recorded|disabled|failed`, nullable `warning_code`), the reply context operation (`mode=reply|reply_all` → recipients + quoted `body_markdown`), `LibraryEntry.preview_profile=workspace|mail_restricted` plus the per-file scripts-allowance field, and the three tool results (W0 owns any tool-result schema crossing the SPA boundary).

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

This package spans the **email transport/tools** cluster (`pkg/email/mailhtml/`, `pkg/email/reply.go`, `pkg/tools/email_attachments.go`, `pkg/mailattachment/`), the **gateway** cluster (attachment preview/save/token handlers), the **Library** leaf (`pkg/library` consumers only — no edits to its safety primitives) and the **SPA workspace surfaces** cluster (`src/components/library/preview/`, Mail panel files). It must not create an import cycle: the shared recipient helper lives in `pkg/email` (below both `pkg/tools` and `pkg/gateway`); the save service lives in `pkg/mailattachment` (below Library and tools, importing neither `pkg/tools` nor gateway HTTP).

---

## 3. Interfaces — what this package publishes and consumes

Stated so the packages can be built in parallel without editing each other's files.

### 3.1 PUBLISHES (owned and delivered by this package)

| Interface | Owning owner | Consumers |
|---|---|---|
| `pkg/mailattachment/service.go::Transfer` (proposed path) — the single application-level save/read/download service; mode selects transient viewer bytes, browser response, or explicit Library write | W7 | Gateway preview/attachment handlers (W4 files), `pkg/tools/email_attachments.go` (W10) |
| The save-operation-token reconciliation (prior-receipt lookup on an explicit same-token retry) | W7 | Gateway save handler; SPA retry path |
| The mail-derived marker + per-file scripts-allowance storage (Q5=A), surviving move/copy/rename/restore — with the provenance proof | W7 | Library gateway preview policy, `LibraryPreviewPane` HTML profile selection (W8) |
| `pkg/email/mailhtml/` — the parsed CSS policy: style-attribute filtering via bluemonday's own CSS layer, the `<style>`-block pass, the checked-in property/value table, and the CSS `url()` extraction/rewrite helpers | W9 | `rest_mail_preview.go::mailSanitizePreviewHTML`, `::mailRewritePreviewSources` (W4-owned integration points) |
| `pkg/email/reply.go::BuildReplyRecipients` (proposed) — the transport-neutral recipient rule (Reply-To otherwise From; reply-all merge; self-exclusion; de-duplication) | W10 | Gateway reply-context handler; `pkg/tools/email_compose.go` adapter; indirectly the SPA (which implements nothing) |
| `pkg/tools/email_attachments.go` — `list_email_attachments`, `read_email_attachment`, `download_email_attachment` + `read_message`'s attachment list | W10 | `EmailToolset`, catalog, inventory, seed, agent registration |
| `src/components/library/mailAttachmentPreviewSource.ts` (proposed) — the temporary-source adapter + resource policy the renderers consume | W8 | `LibraryPreviewPane`, `LibraryExplorer`, every reused renderer |
| The handoff callback shape Mail→Library (frozen jointly with W3 before either side builds) | W8 + W3 jointly, frozen first | `MailPanel` (W3), `LibraryExplorer` (W8) |

### 3.2 CONSUMES (delivered by others; this package edits none of their files)

| Interface | Owner | This package's obligation |
|---|---|---|
| All contract schemas and regenerated artifacts named in §2.3 | W0 (only editor of `contracts/`, `pkg/api/generated/`, `src/lib/api/generated/`) | Consume generated types only; never hand-write a wire shape |
| The targeted single-part reader (part-specific PEEK), the stable `part_index` semantics, and the shared pool/budget injection | W1/W2 | Inject into `Transfer`; never build a private client/pool |
| The gateway-issued `message_ref` (first-phase issuance + same-lease epoch/generation validation) | Read-runtime package (I-03 seam) | Consume the reference unchanged; refuse stale references with the typed error |
| `has_attachments` metadata + the MIME structure classifier | W2 | `read_message`'s attachment list reuses the same classifier output — no second MIME walker |
| The handoff callback shape (frozen above) | W3 | Mail panel invokes it; Library honours it |
| `pkg/library` path-safety primitives, `pkg/audit` logger, `pkg/tools/resolvepath` policy, `pkg/config` ceiling/inventory seams | Existing owners (Library leaf, audit, tools, config) | Call them; never bypass or weaken |
| Tool description text for the three new tools | prometheus-prompt-engineer, supplied to W10's file | Paste, not author |

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
4. **Given** an attachment over 25 MB actual decoded bytes, **When** the user tries Open (and Save), **Then** both are unavailable with the existing cap explanation, and **Download** remains the offered browser action.
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

Per the design system's accessibility release requirement (D16): focus visibility/restoration, status announcement, keyboard operation and zoom/reflow are release requirements, not preferences. Catalogued controls only (`button.tsx`, `icon-button.tsx`, `tooltip.tsx`, `dialog.tsx`, `FormError.tsx`, `checkbox.tsx`); no visual redesign.

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
