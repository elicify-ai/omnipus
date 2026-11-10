# Adversarial Review: Email — HTML signatures, workspace Mail tab, agent draft approval

**Spec reviewed**: `docs/internal/specs/email-mail-view-spec.md` (Draft, 2026-09-25)
**Review mode**: SPEC mode, round 1 (grill-spec; detected format: plan-spec)
**Review date**: 2026-09-25
**Verdict**: **BLOCK**

## Executive Summary

The spec is thorough in shape but is built on two false foundations: it protects and extends the mailbox
drainer that P0 issue #631 orders deleted (founder-mandated reconciliation, D14), and it states that
`send_email` already requires approval when, for every agent that is not a seeded core agent, it does not
(the shipped global ceiling is `allow`). It also predates the founder's answers D11–D13 (it still recommends
view+send+discard where the founder chose view+**edit**+send+discard) and leaves the Message-ID addressing,
Sent-copy duplication on Gmail, the HTML-preview security headers, and the new tool's reachability wiring
under-specified. It also routes two new callers (human REST send, panel Send) through the SMTP path that
open issue #629 shows can hang forever, without fixing it (MAJ-015). The two new Go dependencies pass Hard
Constraints #1–#3 (see "Dependency assessment"), with minor gaps (MIN-012).

| Severity | Count |
|----------|-------|
| CRITICAL | 2 |
| MAJOR | 15 |
| MINOR | 12 |
| OBSERVATION | 3 |
| **Total** | **32** |

Evidence convention: "Verified" = read in this worktree on 2026-09-25 (cited `file::symbol`); "Inferred" =
reasoned, not executed.

---

## Findings

### CRITICAL Findings

#### [CRIT-001] Spec builds on, and regression-protects, the drainer that #631 orders deleted

- **Lens**: Inconsistency / Incorrectness
- **Affected section**: §1 out-of-scope ("Any change to the heartbeat drainer's task-creation behavior"),
  §3.1 row `pkg/email/drainer.go::Drainer` ("regression-protected"), §3.4 "Heartbeat drainer pass — Untouched",
  US-6 (whole story), B-18, Q5 option B ("The drainer … additionally set a custom keyword"), §7.2 first bullet
  ("The drainer's task-creation and `\Seen` behavior is untouched … its suite keeps passing unmodified"),
  FR-020 ("the drainer's flag behavior itself is regression-protected").
- **Description**: Verified — issue #631 (open, P0) "Delete mailbox drain entirely" demands removal of
  `pkg/heartbeat/mailbox_drain.go`, `pkg/email/drainer.go`, the `MailboxDrain` wiring in
  `pkg/gateway/gateway.go` and their tests, and explicitly rejects any gated/opt-in/"drop MarkSeen" variant.
  The interview's D14 (recorded after the spec was written) says the spec **must** reconcile #631. The spec
  instead makes the drainer a regression-protected dependency, designs US-6 around drainer-handled mail, and
  its recommended Q5 option makes the drainer write *more* to the human's mailbox (a keyword) — the exact
  behavior class #631 bans.
- **Impact**: Implemented as written, the team writes tests that lock in code a P0 issue deletes, builds a
  "handled by agent" badge whose only producer disappears, and ships a feature that re-flags the human's
  mailbox. Whichever lands second breaks the other.
- **Recommendation**: Absorb #631. (a) State whether the drainer deletion lands on this branch or is a
  prerequisite, and list the files. (b) Rewrite US-6 as "Unread = IMAP `\Seen`, shared with the owner's own
  client; the Mail tab's read path never sets `\Seen` unless the human opens a message" — the only remaining
  agent-side writer is `read_message` (agent-invoked, visible in the transcript). (c) Delete B-18 and Q5, or
  re-ask Q5 narrowly ("should `read_message` stop marking `\Seen`?"). (d) Replace §7.2's first bullet with
  "drainer code and suites are deleted per #631; no test references `Drainer`".

---

#### [CRIT-002] "send_email already needs approval" is false for every non-core agent

- **Lens**: Incorrectness / Insecurity
- **Affected section**: §1 ("`send_email`'s existing policy-based approval is unchanged (D8)"), US-4 intro,
  US-4 AS-4, §5.2 non-behavior 4, FR-016, B-15.
- **Description**: Verified —
  `pkg/config/defaults.go::defaultToolPolicyCeiling` ships `"send_email": "allow"` and `"reply": "allow"`.
  Only the ADR-090 core-agent inventory (`pkg/coreagent/role_policies_adr090.go::ADR090RolePolicyInventory`)
  grants `ask` for the send tools. An operator-created agent has no per-agent entry, so it rides the ceiling:
  **no approval prompt**. Worse, `pkg/gateway/rest_mailbox.go::grantEmailToolAllows` converts any `deny` on
  all five email tools (including `send_email`/`reply`) to `allow` when a mailbox is enabled. The founder's
  premise ("the send tool needs approval [already]", interview "Request") only holds for seeded core agents,
  and `gateway_boot_roster.go`'s own comment lists `send_email` among the ceiling's "allow" tools.
- **Impact**: The draft-approval flow is the founder's safety story, but an agent can skip it and call
  `send_email` directly with no human in the loop. FR-016 ("MUST remain unchanged") freezes that gap into
  the spec. Outbound email is outward-facing and irreversible.
- **Recommendation**: Raise this to the founder as a decision before revision (it is a design/risk call).
  Recommended: ship the ceiling as `ask` for `send_email` and `reply`, and change `grantEmailToolAllows` to
  fill `ask` (not `allow`) for the two send tools. Under the greenfield ruling no migration is needed;
  Reconcile never overwrites an operator-set value, so state that explicitly. Then rewrite FR-016 as
  "`send_email`/`reply` resolve to `ask` for every agent unless an operator explicitly sets `allow`", with a
  test for an operator-created agent.

---

### MAJOR Findings

#### [MAJ-001] Spec not updated for founder answers D11, D12, D13

- **Lens**: Inconsistency / Incompleteness
- **Affected section**: §2.3 draft rows ("pending Q1"), US-7 ("not committed until Q1"), FR-007, FR-019,
  FR-021, T17, T18, Q1/Q3/Q7, §18 gaps list.
- **Description**: Verified — the interview doc (`spec-email-mail-view.md`, D11–D13) records: Q7 → A
  (Library-style panel), Q1 → **C** (view + **edit** + send + discard; panel Send is the approval), Q3 → sandboxed
  HTML without scripts, remote-image default still open. The spec still marks all three pending and
  recommends Q1-B. Option C needs things the spec does not have: an edit endpoint and schema
  (`MailDraftUpdateRequest`), edit semantics (IMAP cannot edit in place: APPEND new + delete old), whether
  the edited draft keeps its Message-ID (it must, or the chat link breaks), what happens when APPEND succeeds
  and the old-draft delete fails (duplicate drafts), deleting via `UID EXPUNGE` (UIDPLUS) so a plain
  `EXPUNGE` does not purge other `\Deleted` messages in the owner's Drafts, and whether recipients are editable.
- **Impact**: US-7 has no acceptance scenarios; implementers either stall or build option B against the
  founder's decision.
- **Recommendation**: Fold D11–D13 in. Write US-7 acceptance scenarios and BDD for view, edit, send, discard,
  plus error paths (draft gone on send → 404; server lacks UIDPLUS → explicit error, no blind `EXPUNGE`;
  APPEND ok + delete fails → warning naming the duplicate). Add the edit endpoint to §2.3 and a schema to
  §2.2. Put the still-open remote-image default (D13) to the founder as a fresh question.

#### [MAJ-002] Panel Send can transmit content the human never saw

- **Lens**: Insecurity (Tampering) / Incompleteness
- **Affected section**: §2.3 `POST …/drafts/{messageId}/send`, Q1 option B/C text, US-7.
- **Description**: The send endpoint takes only a Message-ID and re-reads the draft from the server at send
  time. Between the human viewing the draft and clicking Send, the draft can change (edit in the owner's own
  mail client, an edit from a second browser tab under D12, or a new draft with the same Message-ID). The
  thing the human approved and the thing transmitted are not bound together.
- **Impact**: The human-approval act (D12: "panel Send is the approval") does not guarantee what goes out —
  a classic check-then-use gap on the one security-relevant action in the feature.
- **Recommendation**: The send request carries the exact `to`/`subject`/`body_markdown` the panel displayed
  (or edited), plus a precondition (`uidvalidity` + `uid` of the viewed draft). The server sends the submitted
  content, and returns 409 if the draft no longer matches the precondition. Add a BDD error scenario and a
  test for it.

#### [MAJ-003] `create_email_draft` reachability wiring is incomplete; SC-007 cannot detect that

- **Lens**: Incompleteness / Incorrectness
- **Affected section**: §2.4 "Policy wiring", §15 bullet "`create_email_draft` joins `tools.EmailToolset` so
  … enable-time fill-in … come from the same list", §17, SC-007.
- **Description**: Verified — the enable-time fill-in list is **not** `tools.EmailToolset`; it is a separate
  literal `pkg/gateway/rest_mailbox.go::emailToolNames`. Core agents default **every** unlisted tool to `deny`
  (`role_policies_adr090.go::ADR090RolePolicyInventory`), so the new tool is denied for Mia and every other
  seeded agent unless that inventory is updated. `pkg/coreagent/seed.go` holds a static tool-name list whose
  unknown override keys "PANIC `validateOverrideKeys` at boot". `pkg/tools/auto_approve.go` classifies every
  email tool (e.g. `"send_email": AutoAsks`). None of these four places appears in the spec. SC-007's grep
  (`pkg/coreagent/ pkg/config/ pkg/tools/` ≥ 3 files) is already satisfied today for `read_inbox` by
  `pkg/tools/email_test.go` and `auto_approve_test.go`. That is a test-file false green, and the grep skips
  `pkg/agent` (registration) and `pkg/gateway` (fill-in) entirely.
- **Impact**: A repeat of the vault-records incident in `CLAUDE.md` "Definition of Done": green CI, tool
  denied or unfillable for real agents.
- **Recommendation**: List every touch point: `defaults.go` ceiling, `rest_mailbox.go::emailToolNames`,
  `role_policies_adr090.go` grants per role (decide which roles get `allow`), `seed.go` tool list,
  `auto_approve.go` classification, and `tools.EmailToolset`. Replace SC-007 with a behavioral test: "for a
  seeded core agent and an operator-created agent, both with an enabled mailbox, the effective policy of
  `create_email_draft` resolves to `allow` and the tool is in the agent's registry".

#### [MAJ-004] Sent-folder APPEND duplicates every message on Gmail and Outlook.com

- **Lens**: Incorrectness
- **Affected section**: D7 / US-2 AS-2, FR-006, B-5, H-3, A1.
- **Description**: 🔶 Inferred (high confidence, provider behavior not tested here) — Gmail (and Outlook.com)
  automatically file a copy of any SMTP-submitted message into Sent. An unconditional IMAP APPEND then
  creates a second copy. Issue #42 names Gmail with an app password as the primary setup.
- **Impact**: On the most common provider, every message shows twice in Sent (Mail tab and the owner's
  client). H-3 would fail on first real use.
- **Recommendation**: Add a per-mailbox setting `save_sent_copy` (default true), defaulted false when the
  IMAP host is `imap.gmail.com` / Outlook.com, or detect it by searching Sent for the Message-ID after
  SMTP success and APPENDing only if absent. Add a DS-5 row and a UAT check for "exactly one Sent copy".

#### [MAJ-005] Message-ID addressing is inconsistent and unsound as specified

- **Lens**: Inconsistency / Incorrectness / Insecurity (Spoofing)
- **Affected section**: §2.3 `GET …/messages/{messageId}` (no folder), MC-7 ("in the addressed folder"),
  §12 edge 1 ("readable by UID from the list"), §5.4 chat-link scheme (`folder=drafts`), MailMessageSummary
  (`message_id` nullable).
- **Description**: (a) The read endpoint has no folder, yet MC-7 speaks of "the addressed folder"; IMAP SEARCH
  works on the selected folder only, so the server must scan up to three folders in an unspecified order.
  (b) Message-IDs are not unique: a self-addressed message sits in Inbox and Sent with the same ID; a
  panel-sent draft's Sent copy shares the draft's ID; any external sender can reuse an arbitrary Message-ID
  on inbound mail. (c) Edge 1 promises UID reading, but no UID-addressed endpoint exists, so mail without a
  Message-ID cannot be opened at all. (d) Edge 9's "path-traversal-shaped values" is the wrong threat: the
  value is fed into an IMAP SEARCH string, so the needed bounds are shape and length.
- **Impact**: The wrong message opens, an inbound spoof shadows a draft in the panel, and some real Inbox
  mail is unopenable.
- **Recommendation**: Address messages as `GET …/folders/{folder}/messages/{ref}`, where `ref` is either
  `uid:<uidvalidity>:<uid>` (list rows) or `mid:<Message-ID>` (chat links). Draft actions resolve only inside
  Drafts. Multiple hits → newest by INTERNALDATE, with a test. Validate Message-ID as `<…@…>`, ≤ 998 bytes, no
  CR/LF.

#### [MAJ-006] Storing drafts as raw Markdown text (A7) breaks the "normal provider" promise

- **Lens**: Inconsistency / Incompleteness
- **Affected section**: A7, D8 ("using the normal email provider mechanism"), H-2, FR-005
  ("draft-when-sent"), US-7.
- **Description**: With A7, the owner's own client shows the draft as raw Markdown without a signature. If
  the owner sends it from that client, the recipient receives raw Markdown with no signature, breaking FR-005.
  The Drafts list will also contain drafts the owner wrote in their own client (HTML multipart). What
  view/edit/send does with those under the Markdown pipeline is unspecified.
- **Impact**: The founder's H-2 check ("the same draft is visible in … my own mail client") passes
  technically but looks broken. Panel Send on a foreign HTML draft either mangles it or fails in an
  undefined way.
- **Recommendation**: Choose explicitly. Option 1: APPEND drafts as the fully rendered multipart (with
  signature) and keep the Markdown source in an `X-Omnipus-Markdown` header or a `text/markdown` part for
  editing. Option 2: keep A7 but mark Omnipus drafts with an `X-Omnipus-Draft` header, and make panel
  edit/send available only for marked drafts (others view-only). State the choice and add a BDD scenario for
  a foreign draft.

#### [MAJ-007] HTML-preview security headers are under-specified

- **Lens**: Insecurity (Information disclosure, Tampering)
- **Affected section**: FR-019, MC-10, §2.3 html-preview endpoints, §16 "HTML mail body", Q3/D13.
- **Description**: MC-10 fixes only `sandbox` (no `allow-scripts`) and `img-src`. Missing: a `default-src 'none'`
  baseline; `style-src` (email relies on inline CSS; `<link rel=stylesheet>` to remote hosts is a tracking
  channel); `font-src`; `form-action 'none'` and no `allow-forms`; no `allow-same-origin`;
  `Referrer-Policy: no-referrer` (the token sits in the URL path and would leak to every remote host once
  `load_remote=true`); token TTL, single-use or reuse, and what the token is bound to (pair + folder + UID);
  an HTML size cap. Also missing: link behavior (D13 "must feel normal" needs clickable links: `allow-popups`
  + `allow-popups-to-escape-sandbox` with `target=_blank rel=noopener`, or links made inert — pick one) and
  `cid:` inline images (multipart/related), which will not render at all under `img-src 'self' data:`. "Sanitized/sandboxed"
  (§2.3) leaves open whether inbound HTML also passes through a sanitizer.
- **Impact**: Tracking and exfiltration channels stay open despite "remote content blocked", and "feels
  normal" fails because links and inline logos do not work.
- **Recommendation**: Write the full header set into MC-10, and make security-lead's review of it a
  pre-implementation gate (already hinted in FR-019). Add T18 cases for each directive, the referrer leak,
  and a `cid:` rewrite to a token-scoped part URL.

#### [MAJ-008] Signature HTML: sanitization and ordering undefined; conflicts with MC-2

- **Lens**: Ambiguity / Inconsistency
- **Affected section**: FR-001, FR-003, FR-005, MC-2, DS-1 ("hostile HTML"), §16 "Signature editor".
- **Description**: MC-2 says the HTML part contains no `<script>`/`on*`/`javascript:`, but the signature is
  operator HTML appended "server-side". The spec never says whether the signature is sanitized, when (on
  save or at compose), or with which policy. A body-grade sanitizer policy strips inline `style` and images,
  which is how real signatures are built. No sanitizer at all makes MC-2 false.
- **Impact**: Either signatures render broken, or MC-2 and SC-002 are false for any hostile signature (DS-1
  already lists one).
- **Recommendation**: Specify: the signature is sanitized on save with a named signature policy (allows
  inline `style`, tables, `img` over https/data; strips script, event handlers, `javascript:`, forms,
  iframes). The stored value is the sanitized one, and PUT returns what was stored. MC-2's test composes a
  hostile body **and** a hostile signature.

#### [MAJ-009] MC-3 contradicts FR-004 on when mail is multipart

- **Lens**: Inconsistency
- **Affected section**: MC-3 vs FR-004 / §5.1 bullet 2.
- **Description**: MC-3 reads "Outbound messages **with a signature** are multipart/alternative …; without,
  text/plain part content is byte-identical to the Markdown-derived text". That implies unsigned mail may be
  single-part. FR-004 says every agent body renders to multipart/alternative. "Markdown-derived text" is
  never defined (raw Markdown? goldmark → text?).
- **Impact**: Two implementers build two different composers; the test encodes whichever one was built first.
- **Recommendation**: Rewrite: "Every outbound message is multipart/alternative with exactly one `text/plain`
  and one `text/html` part. The `text/plain` part is `<derivation rule>` of the Markdown, followed by `-- \n`
  plus the derived signature text when a signature exists." Define the derivation rule once.

#### [MAJ-010] Issue #42 not reconciled (D14)

- **Lens**: Incompleteness / Inconsistency
- **Affected section**: whole spec; D14.
- **Description**: Verified — #42 (open) specifies an Inbox/Sent/Drafts/**Threads** webmail view, send-gating
  "intervention modes", and a Gmail app-password setup UX with "Test connection". The spec neither mentions
  #42 nor states which parts it delivers, supersedes (D5 excludes Threads; D8/D12 replace intervention modes?),
  or leaves open.
- **Impact**: Duplicate or contradicting work, and #42 stays open with unclear ownership. The
  issue-convention rule "every PR closes its issues" cannot be applied.
- **Recommendation**: Add a "Related issues" section. Map each #42 item to delivered / superseded (with the
  D-number) / deferred (with a tracked issue). State "Closes #631" or "Depends on #631" per CRIT-001.

#### [MAJ-011] Human sends and draft approvals leave no audit trail and have no rate limit

- **Lens**: Insecurity (Repudiation, Denial of service)
- **Affected section**: §2.3 `POST …/messages`, draft send/discard endpoints, §5.4.
- **Description**: Agent tool calls are recorded in transcripts and policy decisions. The human REST send and
  the panel Send — which is *the approval act* under D12 — have no audit event specified. Nothing records
  that draft X, authored by agent Y, was approved and sent as Message-ID Z to recipients R. The repo has
  `pkg/audit` and a `withRateLimit` wrapper (`pkg/gateway/auth_mode.go`); neither is specified for the new
  mutating endpoints.
- **Impact**: After an incident ("who sent this?"), there is no record separating agent sends from
  human-approved drafts. A stolen bearer token becomes a spam cannon.
- **Recommendation**: FR: every human send, draft send, draft edit and draft discard emits an audit event
  (pair, folder, Message-ID, recipient list, origin `human|agent-draft`, arguments hash). Wrap the mutating
  mail routes in the existing rate limiter and state the limit. Add tests.

#### [MAJ-012] Polling interval and IMAP connection budget are undefined

- **Lens**: Inoperability / Ambiguity / Infeasibility
- **Affected section**: §1 ("TanStack Query's normal refetch intervals"), A2, SC-004 ("within one refetch"),
  §15 ("dial-per-operation pattern preserved").
- **Description**: TanStack Query has no default refetch interval (it refetches on mount and window focus
  only), so "normal refetch intervals" is undefined. Every IMAP operation is a fresh TCP + TLS + LOGIN
  (`Client.dialIMAP`); the folders view needs LIST plus three STATUS calls. Gmail caps simultaneous IMAP
  connections per account and throttles repeated logins (🔶 Inferred, provider behavior). Mail tab polling,
  agent tools and multiple open tabs share one account, and nothing bounds concurrency. SC-004's "one
  refetch" has no time value, so it is unmeasurable.
- **Impact**: Either stale folders with no refresh, or login throttling or lockout of the owner's real
  mailbox — which also breaks the agent's tools.
- **Recommendation**: State intervals (e.g. folders and list refetch every 60 s while the panel is visible,
  never when hidden). Require one IMAP session per REST request (all STATUS calls on one connection). Add a
  per-mailbox in-gateway concurrency cap (e.g. 2 concurrent IMAP sessions from the Mail tab). Rewrite SC-004
  with the interval in seconds.

#### [MAJ-013] Chat link assumes a configured https `public_url` and verbatim model copying

- **Lens**: Infeasibility / Incompleteness
- **Affected section**: FR-015 ("absolute https URL built from `gateway.public_url`"), §2.4 `chat_link`,
  MC-11, §16 "Chat link → panel".
- **Description**: Verified — `pkg/config/config.go::GatewayConfig.PublicURL` is optional (`omitempty`), and
  default local installs are `http://localhost:<port>`. The cited `serve_web` precedent returns an error when
  `public_url` is unset and the bind address is a wildcard (`pkg/tools/web_serve.go`). The spec does not say
  what `create_email_draft` returns in those cases, and "https" fails every local install. The router uses
  `createHashHistory` (`src/main.tsx`), so "same-origin → navigate in place" must compare origins; a user
  browsing via LAN IP while `public_url` names a domain gets a new tab. The model must also copy a
  percent-encoded Message-ID URL verbatim into its reply; models often alter long URLs.
- **Impact**: Draft created but no working link on default installs. Links that open a new tab, or that were
  subtly corrupted and resolve to "not found".
- **Recommendation**: Derive the origin exactly like `serve_web` (allow http). When no origin is derivable,
  the tool still succeeds and returns `chat_link: null` with a stated reason. Detect deep links by
  hash-route pattern, not by exact origin match. Also render an "Open draft" action from the
  `create_email_draft` tool result itself, so the click path does not depend on the model reproducing the
  URL (see OBS-003).

#### [MAJ-014] Traceability and structural gaps

- **Lens**: Inconsistency (CON-02)
- **Affected section**: §6, §7, §10.
- **Description**: B-3 is in no traceability-matrix row. B-2, B-17 and B-22 have no row in the §7 TDD table
  (B-17 is only a nameless "compose validation test" in §10; B-22 only a dataset row). US-7 has no acceptance
  scenarios or BDD. FR-013 and FR-022 have no BDD scenario. B-18/B-19 have no tests (and are invalidated by
  CRIT-001).
- **Impact**: Required behaviors (signature on both parts via an agent send, compose validation, UIDVALIDITY
  survival) ship untested.
- **Recommendation**: Add TDD rows: `TestSendEmailTool_SignatureBothParts` (B-2), `MailCompose.test.tsx`
  validation (B-17), `TestReadByMessageID_AfterUIDValidityChange` (B-22). Add B-3 to FR-005's row. Add
  scenarios for FR-013 (policy resolution per MAJ-003) and US-7 (per MAJ-001).

#### [MAJ-015] Spec modifies `Client.Send` but does not fix #629; MC-8 asserts bounds SMTP does not have

- **Lens**: Incorrectness / Inoperability
- **Affected section**: §3.1 row `pkg/email/transport.go::Client.Send` ("modifies"), §2.3
  `POST …/messages` and draft-send endpoint, §5 MC-8 ("30s/45s upstream bounds → HTTP 502"), §15 ("a
  Sent-APPEND after SMTP success inside the send path"), T15.
- **Description**: Verified — `pkg/email/transport.go::Client.Send` has the signature
  `func (c *Client) Send(_ context.Context, req SendRequest) error`: the context is discarded.
  `::sendSMTPWithSTARTTLS` calls `smtp.Dial(addr)` and `::sendSMTPS` calls `tls.Dial("tcp", addr, tlsCfg)`,
  neither with a deadline; the later StartTLS/Auth/Mail/Rcpt/Data commands have no deadline either. The
  `dialTimeout` (30 s) and `commandTimeout` (45 s) constants are applied only on the IMAP side (`::dialIMAP`,
  `::runIMAP`). Issue #629 (open) records exactly this, plus a goroutine/connection leak in `dialIMAP`'s
  timeout guard. The spec never mentions #629, yet: (a) it modifies `Client.Send` (adding Sent APPEND); (b) it
  adds two new callers of that path — the human REST send (D9) and the panel Send (D12), both HTTP handlers;
  (c) MC-8 claims the 30 s/45 s bounds produce a 502, which is false for SMTP today, and T15 only exercises a
  read endpoint, so it cannot catch the send hang.
- **Impact**: A black-holed SMTP host (VPN flap, captive portal, typo'd `smtp_host`) hangs the HTTP request
  and the panel's Send button indefinitely, and — because the context is discarded — a client disconnect or
  chat Stop cannot cancel it. For the agent path it hangs the turn *after* the human approved the send. The
  new Sent APPEND, if written in the same style, adds a second unbounded step.
- **Recommendation**: Absorb #629 into this spec (it touches Send, per the brief): FR — "`Client.Send`
  honours the caller's context; SMTP dial uses `net.Dialer{Timeout: dialTimeout}.DialContext` /
  `tls.Dialer{…}.DialContext`; every SMTP command phase runs under a connection deadline bounded by
  `commandTimeout`; the Sent APPEND runs under the same context; `dialIMAP`'s timeout path closes the late
  connection." Add tests: `TestClientSend_DialBlackhole_ReturnsWithinBound`, `TestClientSend_StallAfterConnect_ReturnsWithinBound`,
  `TestClientSend_ContextCancel_Aborts`, `TestMailSendEndpoint_SMTPTimeout_502`. Add "Closes #629" to the
  Related-issues section (MAJ-010).

---

### MINOR Findings

#### [MIN-001] Folder-name overrides cannot be set

- **Lens**: Inconsistency / Overcomplexity
- **Affected section**: §2.1 row 3 (only `Mailbox.yaml`), §2.5, §16.
- **Description**: `sent_folder_name`/`drafts_folder_name` are added to `Mailbox.yaml` only.
  `MailboxConfigureRequest.yaml` is `additionalProperties: false` (verified), so no request can set them, and
  §16 has no UI for them.
- **Recommendation**: Either add them to `MailboxConfigureRequest` and to the panel, or drop them until a
  server without special-use folders (RFC 6154) is actually observed (special-use plus name fallback covers
  Gmail, Fastmail, iCloud).

#### [MIN-002] MC-13 cannot be tested in the single-user model

- **Lens**: Infeasibility / Ambiguity
- **Affected section**: §5.2 last-but-two bullet ("the session's authorization"), MC-13, T20.
- **Description**: Verified — `GatewayConfig.Users` is "single-user model: holds at most one entry". There is
  no second session whose authorization could differ.
- **Recommendation**: Rewrite MC-13 as: agent does not exist, workspace does not exist, or the pair has no
  mailbox → 404 with no body leakage. Drop "session cannot read".

#### [MIN-003] `MailMessage` schema is garbled and incomplete

- **Lens**: Ambiguity / Incompleteness
- **Affected section**: §2.2 `MailMessage` ("`folders` hint `folder`"), `MailFolderList.mailbox`.
- **Description**: The field list is not parseable. The panel, reply and edit flows need `cc`, `reply_to`,
  `in_reply_to` and `references`, none of which are listed. Embedding the full `Mailbox` in `MailFolderList`
  duplicates config for no stated reason.
- **Recommendation**: Give an explicit field table with types and nullability. Drop the embedded `Mailbox`
  or justify it.

#### [MIN-004] Draft Message-ID generation rationale is wrong and incomplete

- **Lens**: Incorrectness
- **Affected section**: §2.4 `create_email_draft` ("before any server assigns one — §2.3 note").
- **Description**: No such §2.3 note exists, and IMAP servers never assign Message-IDs on APPEND. The ID's
  domain part is unspecified (it should be the mailbox's own domain, to avoid spam scoring and leaking the
  host name).
- **Recommendation**: "The tool generates `<random-128-bit@mailbox-domain>`; the same ID is kept through
  edits and the final send."

#### [MIN-005] `unseen_only` and `unread_count` semantics undefined

- **Lens**: Ambiguity
- **Affected section**: §2.3 messages endpoint query params, `MailFolder.unread_count`.
- **Description**: `unseen_only` has no FR or test. `unread_count` for Sent and Drafts is meaningless.
- **Recommendation**: Define `unread_count` for Inbox only (null elsewhere), or drop `unseen_only`.

#### [MIN-006] A sent draft shows "no longer exists"

- **Lens**: Incompleteness
- **Affected section**: §12 edge 4, US-4 AS-3.
- **Description**: After panel Send, the chat link claims the draft does not exist, which reads like
  data loss.
- **Recommendation**: On a Drafts miss, search Sent for the Message-ID and show "Sent on <date>" when found.

#### [MIN-007] Outbound limits and header encoding unspecified

- **Lens**: Incompleteness
- **Affected section**: DS-2 ("very long body (>256 KB cap behavior)", "empty body (reject)"), MC-4.
- **Description**: The 256 KB cap (`capBody`) is inbound-only. No outbound size limit is stated. The
  empty-body rejection appears in no FR. Non-ASCII subjects and display names need RFC 2047 encoding; MC-4
  covers only CR/LF.
- **Recommendation**: FR: body 1 byte to N KB (state N), otherwise 400 or tool error. Add an RFC 2047 test
  with a UTF-8 subject and display name.

#### [MIN-008] Picker data source and "remembered for the session" unspecified

- **Lens**: Ambiguity
- **Affected section**: US-3 AS-3, FR-010.
- **Description**: The existing `GET /api/v1/mailboxes` (`pkg/gateway/rest.go`, verified) can drive the
  picker; the spec does not say so. "Session" (browser tab? localStorage? chat session?) is undefined.
- **Recommendation**: Name the endpoint and filter (`workspace_id == current`, `enabled && configured`).
  Define persistence as, e.g., `sessionStorage` per workspace.

#### [MIN-009] Draft Markdown preview must not render raw HTML

- **Lens**: Insecurity
- **Affected section**: §16 "Message/draft preview" (`LibraryMarkdownPreview`).
- **Description**: Draft Markdown is agent-authored and can be steered by prompt injection. The spec does
  not state that raw HTML inside it is inert in the SPA origin.
- **Recommendation**: Require react-markdown without `rehype-raw` (or with `skipHtml`) for draft rendering,
  plus a test with `<img src=x onerror=…>` in a draft body.

#### [MIN-010] No operator diagnostics

- **Lens**: Inoperability
- **Affected section**: §5.4, §17.
- **Description**: No structured log fields (pair, folder, operation, duration, upstream error class) and no
  "Test connection" action (#42 asks for one).
- **Recommendation**: Specify log fields for each mail endpoint. Either add a test-connection endpoint or
  defer it explicitly to a tracked issue.

#### [MIN-011] 502 error bodies may echo raw upstream server text

- **Lens**: Insecurity (Information disclosure)
- **Affected section**: §2.3 note "The body names the upstream cause".
- **Description**: Raw IMAP/SMTP server responses can contain internal host names or account detail.
- **Recommendation**: Map to an error class (`timeout|auth_failed|tls|folder_missing|server_error`) plus a
  sanitized message. Log the raw text server-side only.

#### [MIN-012] A4 dependency statement lacks versions, transitive deps and a build-once rule

- **Lens**: Incompleteness
- **Affected section**: A4, §15 "Deps".
- **Description**: See "Dependency assessment" below: both libraries pass Hard Constraints #1–#3, but A4
  names no versions, omits that bluemonday pulls `github.com/aymerick/douceur` and `github.com/gorilla/css`
  (indirect) into `go.mod`, and does not require the sanitizer policy to be built once (package level) rather
  than per message — the one choice that could make footprint (HC #3) or latency drift.
- **Recommendation**: A4: "goldmark v1.8.6 (no dependencies), bluemonday v1.0.27 (+ douceur v0.2.0,
  gorilla/css v1.0.1 indirect; `golang.org/x/net` already required); both policies (body, signature) are
  package-level values built once; goldmark keeps its default (no `html.WithUnsafe`); CI `vuln` gate covers the
  new modules."

---

### Observations

#### [OBS-001] Reuse `go-message` for MIME building

- **Lens**: Overcomplexity
- **Affected section**: §15 "multipart builder".
- **Suggestion**: `github.com/emersion/go-message v0.18.2` is already in `go.mod` (verified) and writes
  multipart/alternative with correct encoding. Prefer it over a hand-built builder. goldmark already omits
  raw HTML by default; keep bluemonday as the second layer anyway.

#### [OBS-002] Open questions still unanswered

- **Lens**: Incompleteness
- **Affected section**: §14.
- **Suggestion**: Q2 (transcripts vs D6), Q4 (CC/BCC) and Q6 (link addressing) have no recorded answer, and
  Q5 is overtaken by #631. Batch them with CRIT-002 and the D13 image default into the post-grill founder
  interview.

#### [OBS-003] Structured draft action instead of relying on a model-typed URL

- **Lens**: Overcomplexity / Infeasibility
- **Affected section**: §2.4, §16.
- **Suggestion**: The chat already renders tool calls. An "Open draft" control built from the
  `create_email_draft` tool result is deterministic, while a model-reproduced URL is not. Keep the text link
  as a convenience (MAJ-013).

---

## Dependency assessment (goldmark, bluemonday) against Hard Constraints #1–#3

| Check | goldmark v1.8.6 | bluemonday v1.0.27 | Verdict |
|---|---|---|---|
| HC #1 single binary, no new runtime deps | Go library compiled in; no service, file or process at runtime | Same | PASS (Verified: module `go.mod` files read from the Go module cache) |
| HC #2 pure Go, no CGo | 0 files with `import "C"` in the module tree | 0 files with `import "C"`; deps `douceur`, `gorilla/css`, `x/net` are pure Go | PASS (Verified for the two modules; the same grep finds `import "C"` in `runtime/cgo`, so it can see CGo. Transitive modules: Inferred, widely known pure Go) |
| Transitive modules | None (`go.mod` has no `require`) | `aymerick/douceur v0.2.0`, `golang.org/x/net v0.26.0` (repo already at v0.58.0, so no downgrade), `gorilla/css v1.0.1` indirect | Two new small modules enter `go.mod` — acceptable, must be listed (MIN-012) |
| HC #3 footprint (< 10 MB security-feature RAM) | Parser allocates per render; nothing resident | A compiled policy is a few maps of allowed elements/attributes; resident only if built once | PASS if policies are package-level (🔶 Inferred, medium confidence — not measured) |
| Safe defaults | Raw HTML omitted unless `html.WithUnsafe()` is set (option exists in `renderer/html/html.go`) | Allow-list sanitizer; body and signature need two different policies (MAJ-008) | Spec must pin both (MIN-012, MAJ-008) |
| Existing alternative | — | — | MIME building should reuse `emersion/go-message` already in `go.mod` (OBS-001); no existing Markdown or sanitizer lib in `go.mod` (Verified: `grep goldmark\|bluemonday go.mod` empty) |

**Conclusion:** the two dependencies are acceptable under Hard Constraints #1–#3. The gap is documentation and
configuration (MIN-012), not the choice.

## Structural Integrity

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | FAIL | US-7 has none (deferred) |
| Every acceptance scenario has BDD scenarios | FAIL | US-7; US-6's scenarios are invalidated by #631 |
| Every BDD scenario has `Traces to:` reference | PASS | 22/22 |
| Every BDD scenario has a test in TDD plan | FAIL | B-2, B-17, B-22 missing; B-18/B-19 pending |
| Every FR appears in traceability matrix | PASS | 22/22 (FR-013, FR-022 lack BDD) |
| Every BDD scenario in traceability matrix | FAIL | B-3 absent |
| Test datasets cover boundaries/edges/errors | FAIL | No rows for Message-ID collision, foreign drafts, Gmail Sent duplication, hostile signature at compose, RFC 2047 headers, draft edit concurrency |
| Regression impact addressed | FAIL | Protects drainer that #631 deletes; no regression note for the send-policy change (CRIT-002) |
| Success criteria are measurable | FAIL | SC-004 "one refetch" has no interval; SC-007 grep passes on test files alone |

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Concurrency | Draft edited while viewed; panel Send vs owner-client edit; concurrent Mail-tab sessions against the IMAP connection cap | US-7, MAJ-002, MAJ-012 |
| Idempotency | Double-click panel Send → two SMTP sends; retry after timeout of `POST …/messages` | US-5, US-7 |
| Negative (policy) | Operator-created agent calling `send_email` must prompt | CRIT-002 |
| Negative (addressing) | Inbound mail reusing a draft's Message-ID | MAJ-005 |
| Security headers | Each CSP directive, Referrer-Policy, token TTL/reuse | MAJ-007 |
| Provider behavior | Gmail auto-saved Sent copy | MAJ-004 |
| Partial failure | Edit: APPEND ok, delete fails; server without UIDPLUS | MAJ-001 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| DS-1 Signature | Real-world signature with inline `style` + https logo | Assert it survives sanitization intact |
| DS-2 Bodies | UTF-8 subject / display name; outbound size limit ±1 | RFC 2047 encoding test; N KB and N KB+1 |
| DS-4 Addressing | Duplicate Message-ID across folders; spoofed inbound ID; ID > 998 bytes | Resolution-order and rejection tests |
| DS-5 Send | Gmail host (server-side Sent copy) | Exactly one Sent copy |
| DS-6 Drafts | Foreign (owner-authored HTML) draft; edit; stale-precondition send (409) | Per MAJ-001/002/006 |

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| Mail read endpoints | ok | ok | ok | risk | risk | ok | Raw upstream errors (MIN-011); IMAP connection exhaustion (MAJ-012) |
| Human send endpoint | ok | ok | risk | ok | risk | ok | No audit, no rate limit (MAJ-011) |
| Draft panel send/edit/discard | risk | risk | risk | ok | ok | ok | Message-ID spoof (MAJ-005); TOCTOU (MAJ-002); no audit (MAJ-011) |
| `create_email_draft` tool | ok | ok | ok | risk | ok | risk | Prompt-injected exfiltration drafts need prominent recipient display; reachability/policy (MAJ-003) |
| `send_email`/`reply` policy | ok | ok | ok | ok | ok | risk | Ceiling `allow` → no approval for non-core agents (CRIT-002) |
| HTML preview token + frame | ok | ok | ok | risk | risk | ok | CSP gaps, referrer token leak, no size cap (MAJ-007) |
| Signature config | ok | risk | ok | ok | ok | ok | Unsanitized HTML into every outgoing mail (MAJ-008) |
| Chat link → panel | ok | ok | ok | ok | ok | ok | Integrity issue is reliability, not security (MAJ-013) |
| Draft Markdown render in SPA | ok | risk | ok | ok | ok | ok | Raw HTML must stay inert (MIN-009) |

## Questions for the founder

Each question: context and impact, options, recommendation. Answer in one line, e.g. "F1 A, F2 B".

**F1 — Should sending mail always ask for approval? (CRIT-002)**
Today only the built-in agents ask before `send_email`/`reply`. Any agent you create yourself sends mail
without asking, and turning on a mailbox switches a "deny" on those tools to "allow". The draft flow is meant
to be the safe path, but an agent can simply skip it.
- A: Ship "ask" for `send_email` and `reply` for every agent; enabling a mailbox fills "ask", never "allow". An
  operator can still set "allow" deliberately. **(recommended)**
- B: Keep today's behaviour and document it.
- C: Remove direct sending for agents entirely; agents may only create drafts.

**F2 — When does the mailbox poller deletion (#631) land? (CRIT-001)**
The spec builds on the background poller that #631 orders deleted. The two cannot both ship as written.
- A: Delete it on this branch, first; the spec drops the "handled by agent" badge and relies on the normal
  read/unread flag. **(recommended)**
- B: Delete it on a separate branch that must land before this one.
- C: Keep it and close #631 as won't-fix.

**F3 — Remote images in incoming mail (D13, left open)**
Remote images let senders see when you open a message (tracking) and can leak the preview address.
- A: Blocked by default, with a "Load images" button per message (Outlook-style). **(recommended)**
- B: Blocked by default, plus "always load from this sender".
- C: Loaded by default (Gmail loads them through its own proxy; Omnipus has no proxy, so this exposes your IP).

**F4 — Can you change recipients when editing an agent's draft? (D12, MAJ-001)**
- A: Yes — To, subject and body are all editable; the draft keeps its identity so the chat link still works.
  **(recommended)**
- B: Body and subject only; recipients locked to what the agent chose.

**F5 — Drafts you wrote in your own mail client (MAJ-006)**
The Drafts list will also show drafts you started in Gmail/Outlook. Omnipus's editor is Markdown-based and
could mangle them.
- A: Show them read-only; edit/send only for drafts Omnipus created. **(recommended)**
- B: Hide them from the Mail tab.
- C: Allow full edit/send (higher risk of breaking formatting).

**F6 — Copy to Sent on Gmail/Outlook.com (MAJ-004)**
Gmail and Outlook.com already save sent mail to Sent, so Omnipus's own copy would show every message twice.
- A: Per-mailbox "save a copy to Sent" setting, turned off automatically for Gmail/Outlook.com. **(recommended)**
- B: Always save a copy (accept duplicates on those providers).
- C: Check Sent after sending and add a copy only if the provider did not.

**F7 — How fresh must the Mail tab be? (MAJ-012)**
Each refresh logs in to your mail server again; Gmail throttles frequent logins.
- A: Refresh every 60 seconds while the panel is visible, never when hidden, at most 2 connections at once.
  **(recommended)**
- B: Every 15 seconds (fresher, more risk of throttling).
- C: Manual refresh button only.

**F8 — What does this work close on GitHub? (MAJ-010, MAJ-015)**
- A: Closes #629 (send can hang) and #631; delivers part of #42 (inbox view, drafts) and leaves the rest of
  #42 (threads, Gmail setup wizard) open with a note. **(recommended)**
- B: Also absorb the rest of #42 into this spec.
- C: Leave #42 untouched.

**F9 — Still-open spec questions Q2, Q4, Q6 (OBS-002)**
Q2 (do agent-written draft bodies stay in chat transcripts), Q4 (CC/BCC support) and Q6 (link addressing,
superseded by MAJ-005's proposal) have no recorded answer. Recommendation: answer Q2 and Q4 in the same
interview; accept MAJ-005's folder + reference addressing for Q6.

## Verdict Rationale

**BLOCK.** CRIT-001 makes the spec contradict a founder-mandated P0 issue: implemented as written, it locks
in and extends code that must be deleted. CRIT-002 shows the approval model the whole draft flow is
justified by does not exist for non-core agents. Neither can be patched mechanically: both need founder
input. MAJ-001 (D12 edit scope), MAJ-002 (approval integrity), MAJ-003 (reachability), MAJ-005 (addressing)
and MAJ-007 (HTML-preview headers) each change contracts in §2, so they must be settled before the
contract-first commit. MAJ-015 absorbs #629 because the spec modifies the send path it concerns.

### Recommended Next Actions

- [ ] Founder interview (per the repo's post-grill step) on F1–F9 above.
- [ ] Rewrite US-6, §7.2 and Q5 for #631 (CRIT-001); add a Related-issues section for #42/#631 (MAJ-010).
- [ ] Fold D11–D13 in: US-7 scenarios, edit endpoint and schema, UIDPLUS delete semantics (MAJ-001, MAJ-002).
- [ ] Re-key §2.3 addressing to folder + `uid:`/`mid:` refs (MAJ-005).
- [ ] Enumerate all six policy/registration touch points; replace SC-007 with a behavioral test (MAJ-003).
- [ ] Write the full HTML-preview header set; get security-lead sign-off before implementation (MAJ-007).
- [ ] Fix MC-3, the signature sanitization rule, audit and rate limits, polling budget, chat-link origin
      (MAJ-008, MAJ-009, MAJ-011, MAJ-012, MAJ-013).
- [ ] Absorb #629: context-threaded, bounded SMTP send and Sent APPEND, with tests (MAJ-015).
- [ ] Pin dependency versions, transitive modules and build-once policies in A4 (MIN-012).
- [ ] Close the traceability gaps (MAJ-014) and the MINOR items.
