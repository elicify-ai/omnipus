# Adversarial Review: Email — HTML signatures, workspace Mail panel, agent draft approval (round 2)

**Spec reviewed**: `docs/internal/specs/email-mail-view-spec.md` (Draft, fix round 1, commit `e2cbd0ca7`)
**Review mode**: SPEC mode, round 2 (grill-spec; detected format: plan-spec)
**Round-1 review**: `docs/internal/specs/email-mail-view-spec-review.md` (kept unchanged; this file is round 2)
**Review date**: 2026-09-25
**Verdict**: **BLOCK**

## Executive Summary

Fix round 1 closed the round-1 findings honestly and the structure is now complete (37 scenarios, 44 test
rows, a full disposition table). Round 2 finds two new critical problems: the mail-HTML security headers
(MC-10) repeat, word for word, the two browser defects this repo already hit and documented for the Library
preview (`'self'` breaks in Safari, and `'self'` lets untrusted HTML make logged-in requests to the API);
and the new "Let the agent handle new mail" switch lets any outside sender start an agent turn with no
human present and no stated limits on which tools that turn may use. Fifteen major findings cover the
watcher's missing UIDVALIDITY/first-run rules, the unspecified "mark as read on open" endpoint (the code
today marks mail read implicitly, which the planned tests cannot see), BCC leak risk, an infeasible
Markdown-in-a-header design, double-send on retry, silent attachment loss on foreign drafts, and several
contract gaps.


**Connection resilience (added from the 2026-09-25 live-instance diagnosis).** The spec still defers the
dominant real failure to a "D22 triage" that has now reported: it requires no DNS retry and no
context-aware IMAP dial (MAJ-016), no backoff or jitter after repeated failures (MAJ-017), and no login
budget, so the 30 s refresh, extra tabs and the watcher multiply logins per mailbox (MAJ-018). The watcher
can also be silently blind exactly like the drainer it replaces (MAJ-019). Failures *are* required to be
visible (FR-018, MC-8, B-14), but the error classes cannot name a DNS failure (MIN-014) and the logging has no
rate rule (MIN-015). OBS-004: on macOS the resolver premise in the diagnosis is wrong; the requirement stands.

| Severity | Count |
|----------|-------|
| CRITICAL | 2 |
| MAJOR | 19 |
| MINOR | 15 |
| OBSERVATION | 4 |
| **Total** | **40** |

Evidence convention: **Verified** = read in this worktree on 2026-09-25 (cited `file::symbol`);
**Inferred** = reasoned, not executed, with confidence stated.

---

## Findings

### CRITICAL Findings

#### [CRIT-001] MC-10's CSP reintroduces both documented Library-preview defects

- **Lens**: Insecurity / Incorrectness
- **Affected section**: §5.3 MC-10, FR-019, §2.3 (`/mail/html-preview/{token}` and `/part/{index}`), §16 "HTML mail body"
- **Description**: MC-10 specifies `img-src 'self' data:` (relaxing to `'self' data: https:` on "Load
  images") and serves the frame and its `cid:` parts from `/api/v1/mail/html-preview/...`. The repo's own
  Library preview policy records two measured defects with exactly this shape (**Verified**:
  `pkg/gateway/library_isolation_policy.go` header comment and `::libraryIsolationPolicyTemplate`):
  1. *Defect 1 (2026-08-23)*: under WebKit, `'self'` stops matching once an iframe `sandbox` attribute is
     layered on — Safari users would see no inline (`cid:`) images at all.
  2. *Defect 2 (2026-09-14)*: `'self'` spans the whole gateway including `/api/v1/*`, and WebKit attaches
     the SameSite=Strict session cookie to framed subresource requests. Untrusted inbound HTML containing
     `<img src="/api/v1/...">` becomes an **authenticated GET** on Safari. The Library fix was to drop
     `'self'`, path-confine every source to the preview prefix, keep the preview outside the API prefix,
     and guarantee no redirect under that prefix (`library_preview_no_redirect_test.go`).
  MC-10 also omits the CSP `sandbox` directive (the Library sends it as well as the attribute), `base-uri
  'none'` (not covered by `default-src`), and says nothing about redirects. `https:` in the relaxed policy
  re-admits the gateway itself when served over https. The endpoint table also calls the body "sanitized"
  without naming any inbound sanitizer policy (only the outbound body and signature policies exist, FR-003).
- **Impact**: A marketing or phishing email opened in Safari issues logged-in requests against the Omnipus
  API from inside the mail frame (any GET with a side effect — and this spec itself proposes a read path
  that may mark mail `\Seen`, MAJ-003). Inline logos silently fail on Safari. FR-019's "security-lead gate"
  would be reviewing a header set that is already known to be wrong in this repo.
- **Recommendation**: Rewrite MC-10 by reference to the Library policy: serve mail HTML and `cid:` parts
  under a dedicated non-API prefix (e.g. `/mail-preview/`), name the gateway origin **path-confined** to that
  prefix instead of `'self'`, send `sandbox` (no tokens) as a CSP directive plus the attribute, add
  `base-uri 'none'; connect-src 'none'; object-src 'none'`, relax "Load images" to `https:` **minus** the
  gateway (state the known limitation if CSP cannot express the exclusion, or proxy remote images), add a
  no-redirect test mirroring `library_preview_no_redirect_test.go`, and name the inbound HTML sanitizer
  (strip `<meta http-equiv>`, `<base>`, forms, scripts; rewrite `cid:` to part URLs). Add the WebKit
  cookie case to the per-directive test list.

---

#### [CRIT-002] "Let the agent handle new mail" is a zero-click prompt-injection path with no threat model

- **Lens**: Insecurity (Spoofing, Elevation of Privilege, Information Disclosure, Denial of Service)
- **Affected section**: FR-024, US-6 AS-3, A11, §1 bullet 6, STRIDE coverage (absent)
- **Description**: With the switch on, any internet sender who knows the address starts an agent turn in
  the workspace chat, with no human present, carrying attacker-controlled text. The spec restricts only
  one thing in that turn (reads use BODY.PEEK, A11). It says nothing about which tools the turn may call.
  **Verified** tool postures a seeded agent carries into that turn: Mia/Jim/Ava have `fetch_url` and
  `search_web` **allow** (`pkg/coreagent/role_policies_adr090.go::ADR090RolePolicyInventory`); a fresh
  install ships `sandbox.auto_approve: true` and `bash: ask`, so Auto runs confinable bash without a prompt
  (root `CLAUDE.md`, Hard Constraint #6 / ADR-092). That is the full "private data + untrusted content +
  outbound channel" combination: a crafted email can instruct the agent to read workspace files and
  `fetch_url` them to an attacker's URL. The spec also does not bound how many turns a burst of mail
  starts (see MAJ-001).
- **Impact**: Data exfiltration and LLM-cost exhaustion triggered by an unauthenticated external party,
  with no approval prompt in the loop — on a switch whose UI copy ("Let the agent handle new mail")
  suggests nothing risky.
- **Recommendation**: Add an FR with explicit security requirements for watcher-triggered turns, reviewed
  by security-lead before implementation: (a) the turn runs with a restricted tool set (e.g. email read +
  draft only; `send_email`/`reply`/`fetch_url`/`bash`/file-write forced to `ask` or `deny` for that turn,
  never auto-approved); (b) the injected turn prompt marks the mail as untrusted third-party content;
  (c) a per-mailbox turn budget (e.g. ≤ N turns/hour, one turn per cycle batching all new mail);
  (d) an audit event per triggered turn; (e) UI copy on the switch stating that outside senders can start
  agent work. Add a STRIDE row and a DS-8 row "hostile instruction in inbound mail" with the expected
  outcome.

---

### MAJOR Findings

#### [MAJ-001] Watcher state has no UIDVALIDITY, no first-run baseline, and no turn-count rule

- **Lens**: Incompleteness / Incorrectness
- **Affected section**: FR-023, FR-033, US-6 AS-3, DS-8 row "30+ new messages in one cycle"
- **Description**: FR-033 limits the state file to `last_seen_uid`, `unseen_total`, `last_ok`,
  `last_error_class` — no `uidvalidity`. IMAP UIDs are only meaningful within one UIDVALIDITY. Nothing says
  what the first cycle does when no state exists (treat the whole Inbox as "new"?). US-6 AS-3 says new mail
  "starts exactly one agent turn" without saying per message or per cycle. DS-8's 30-message row has no
  expected result. The state-file key (pair) is not tied to ADR-033 move/delete of a mailbox.
- **Impact**: A server-side UIDVALIDITY reset either silences the watcher forever (new UIDs below the stored
  one) or treats the whole mailbox as new. Enabling the switch on a mailbox with no state could start one
  agent turn per historical message. Moved mailboxes may inherit or orphan state.
- **Recommendation**: Add `uidvalidity` to FR-033; on mismatch or missing state, baseline to the current
  `UIDNEXT-1` without triggering turns and log once. State "one turn per cycle, batching all new UIDs,
  capped at N messages" in FR-024. Give DS-8's 30+ row an expected result. Key the state file by
  `(agent, workspace)` and delete it with the mailbox.

---

#### [MAJ-002] The watcher's agent-turn mechanism and the "peek" scope are undefined

- **Lens**: Incompleteness / Infeasibility
- **Affected section**: FR-024, A11, B-28, T30 (`TestWatcherTurnPeekNoSeen`, Unit)
- **Description**: "Starts an agent turn in that workspace's chat" names no mechanism, no session (new
  session? the standing heartbeat session?), no prompt text, and no behavior when a turn is already
  running. The repo has a direct precedent: workspace heartbeats are cron jobs keyed
  `heartbeat:<workspace>:<agent>` with an eager standing session and a wrapped prompt
  (**Verified**: `pkg/gateway/heartbeat_schedule.go::desiredHeartbeat`, `::buildHeartbeatMessage`). A11's
  "turn-scoped peek" needs a context stamp that every read path honours — including `reply`, which calls
  `tp.ReadMessage` on the original message before sending (**Verified**: `pkg/tools/email.go::ReplyTool.Execute`),
  and `ReadMessage` fetches `BODY[]` without peek on a read-write `SELECT INBOX` (**Verified**:
  `pkg/email/transport.go::Client.dialIMAP` selects INBOX; `::Client.ReadMessage` sets
  `FetchItemBodySection{{}}` with no `Peek`). T30 is a unit test and cannot show an agent turn happened.
- **Impact**: Implementers will invent the turn plumbing; a `reply` in a watcher turn will still mark the
  original read, breaking D20's promise that mail stays unread for the human.
- **Recommendation**: Specify the trigger as a one-shot on the existing cron/heartbeat path (named
  session, prompt template, skip-if-busy rule). State that peek mode is a context value set by the watcher
  turn and honoured by `ReadMessage` wherever it is called (including inside `reply`). Move the turn test to
  integration level.

---

#### [MAJ-003] "Opening marks \Seen" has no endpoint, and the "read path marks nothing" rule contradicts today's code

- **Lens**: Inconsistency / Incompleteness
- **Affected section**: §2.3 notes ("nothing is marked \Seen by the list/read path"), FR-020, US-6 AS-1, B-27, T29
- **Description**: §2.3 says list and read endpoints never mark `\Seen`; FR-020 says opening a message in
  the panel stores `\Seen`. The only "open" endpoint is `GET …/messages/{ref}` — the read path. No endpoint
  or parameter marks a message read. Separately, today's read primitive marks `\Seen` implicitly through
  a non-peek `BODY[]` fetch (**Verified**, MAJ-002), so "read path marks nothing" is only true if the new
  code uses `BODY.PEEK[]` or `EXAMINE`, which the spec never requires.
- **Impact**: One reading gives a GET with a side effect, fired by the 30 s refetch and by any browser
  prefetch; the other gives a panel that never marks anything read. Either way, B-27 and §2.3 cannot both
  pass.
- **Recommendation**: Add `POST …/folders/{folder}/messages/{ref}/seen` (or a `PATCH` flags endpoint) to
  §2.3/contracts, called once by the panel on open. Require every list/read/preview fetch to use
  `BODY.PEEK[]` (or `EXAMINE`), and state it in FR-020.

---

#### [MAJ-004] The planned tests cannot see the IMAP behaviors they claim to verify

- **Lens**: Infeasibility (test instrument) / Incompleteness
- **Affected section**: §7 E2E note, §5.4 ("in-memory transport fake"), T5, T9, T11, T12, T26, T29, T30, MC-17, MC-18
- **Description**: The plan runs flag, APPEND, UID EXPUNGE, UIDVALIDITY and peek behavior against a
  hand-written `Transport` fake. A fake cannot model implicit `\Seen` from a non-peek fetch. MC-18
  asserts "no `\Seen`/flag STORE issued", and the real mechanism issues no STORE, so the test cannot fail.
  The repo already has a real-protocol harness: `pkg/email/imapserver_test.go::startMemIMAP` drives the
  real `Client` against `go-imap/v2/imapserver/imapmemserver` with no new dependency (**Verified**).
- **Impact**: A textbook false green. D20's "mail stays unread" and FR-032's "never a blind EXPUNGE" could
  both be broken while the suite passes.
- **Recommendation**: Move T5, T9, T11, T12, T26, T29 and T30 onto the `startMemIMAP` harness and assert
  the server-side end state (flags after the call, messages present after deletion, UIDVALIDITY change
  simulated by recreating the folder). Keep fakes only for tool-level shape tests. If `imapmemserver`
  lacks UIDPLUS or special-use, say so and test that branch with a wire-recording fake.

---

#### [MAJ-005] BCC handling is contradictory and can leak BCC recipients

- **Lens**: Insecurity (Information Disclosure) / Inconsistency
- **Affected section**: US-2 AS-4, B-9, FR-022, FR-029 (drafts)
- **Description**: B-9 says "the To/Cc/Bcc header lines carry exactly those addresses" and then only
  requires the **Sent copy** to lack a Bcc header. Nothing requires the **transmitted** copy (SMTP DATA) to
  omit Bcc — the one place where omitting it is mandatory. Separately, dropping Bcc from the Sent copy
  is the opposite of the usual mail-client practice: Sent normally keeps Bcc so the owner can see who was
  copied, and here the audit trail covers human sends only (MC-19). Drafts must keep Bcc to stay editable,
  and FR-029 does not say so.
- **Impact**: Under the worst reading, every recipient sees the BCC list. Under the current wording, the
  owner loses any record of whom an agent BCC'd.
- **Recommendation**: Rewrite FR-022: BCC addresses go into the SMTP envelope (RCPT TO) only. The
  transmitted message MUST NOT contain a Bcc header. The Sent copy and the Drafts copy MUST keep it.
  Fix B-9 and T7 to assert all three copies.

---

#### [MAJ-006] Storing the Markdown source in an `X-Omnipus-Markdown` header is fragile and can silently discard the owner's edits

- **Lens**: Infeasibility / Incorrectness
- **Affected section**: FR-029, FR-030, B-24, B-30, MC-22 (1 MiB bodies)
- **Description**: FR-029 puts the whole Markdown body (up to 1 MiB) in one header. Headers must be folded
  (998-octet lines) and non-ASCII must be RFC 2047-encoded. Servers and clients commonly limit total
  header size (**Inferred**, medium confidence; limits vary by server). Worse, if the owner edits an
  Omnipus draft in their own client and that client keeps unknown `X-` headers on re-save, the new copy
  carries `X-Omnipus-Draft` plus a **stale** `X-Omnipus-Markdown`. The panel then shows and sends the
  agent's original text. The uidvalidity+uid precondition does not help: the panel loads the new UID
  legitimately.
- **Impact**: The owner's edits made in Apple Mail, Outlook or Thunderbird are silently thrown away on panel
  Send — the exact "silent" outcome D22 forbids.
- **Recommendation**: Add an `X-Omnipus-Render-Hash` (hash of the rendered text part) and treat a draft
  whose body no longer matches as foreign (the FR-030 path, with the loss statement). Or drop the header
  and carry the Markdown as a third MIME part (`text/markdown`) inside a `multipart/mixed` wrapper. Also
  state a maximum Markdown size for drafts that the header approach can actually carry.

---

#### [MAJ-007] The plain-text part relies on a goldmark feature that does not exist, and line breaks are unspecified

- **Lens**: Infeasibility / Ambiguity
- **Affected section**: MC-3 ("goldmark text rendering of the same parse"), FR-004, A4
- **Description**: goldmark ships an HTML renderer only. A plain-text rendering needs a custom renderer
  or AST walker, and the spec neither scopes that nor gives its rules for lists, tables, code and images
  (**Inferred**, high confidence). Separately, standard Markdown joins single newlines into one paragraph,
  and agents write plain-text email today (`body` "Plain-text reply body",
  **Verified** `pkg/tools/email.go::ReplyTool.Parameters`). Signatures, address blocks and line-per-item
  lists written by agents will be merged into one line in the HTML part.
- **Impact**: Either an unplanned renderer is built ad hoc, or the plain part is produced by
  `htmlToText` on the HTML (contradicting MC-3). Existing agent prompts produce visibly mangled mail.
- **Recommendation**: State the plain-text derivation explicitly (custom goldmark AST text renderer with
  listed rules, or `htmlToText` of the sanitized HTML) and add it to §15. Decide hard wraps: enable
  goldmark's hard-wrap option so single newlines survive, and add a DS-2 row for multi-line plain text.

---

#### [MAJ-008] The contract section has gaps the 5-step procedure will turn into guesses

- **Lens**: Incompleteness / Inconsistency (Hard Constraint #8)
- **Affected section**: §2.2, §2.3, MC-8, MC-16, MC-20, FR-029/FR-030, US-7 AS-6
- **Description**:
  1. `MailDraftUpdateRequest` has no `uidvalidity`/`uid`, and the `PUT` row lists no 409. Yet US-7 AS-6,
     B-35 and T25 require an edit precondition returning 409.
  2. MC-20 returns 429 on four routes, and no status column lists 429.
  3. The preview mint/serve routes fetch live IMAP but list no 502.
  4. `MailMessage` has no `bcc` and no Markdown field. The draft editor needs both: FR-029's lossless
     Markdown and FR-030's derived Markdown for foreign drafts. The spec does not say whether that Markdown
     is derived on the server or in the SPA.
  5. MC-8's "sanitized error class" has no named field. `ErrorResponse` has `code`, `error`, `field`,
     `details` (**Verified** `contracts/components/schemas/ErrorResponse.yaml`), and the spec does not say
     which one carries the class, or give the 409 body shape.
  6. `MailDraftSendRequest` carries `uidvalidity`/`uid` while the path already carries a `ref`, which may
     be a `uid:` ref — two sources of truth, no stated precedence.
- **Impact**: Contract authors fill these in differently from handler authors. The generated types then
  miss the fields the UI needs, and drift is caught late.
- **Recommendation**: Add the precondition fields and 409 to the `PUT`, add 429 to the four mutating rows,
  add 502 to the preview rows, and add `bcc` plus `body_markdown` (server-derived for foreign drafts,
  with a `markdown_lossy: bool`) to `MailMessage`. Bind the error class to `ErrorResponse.code` with the
  enum, and state that the body precondition must equal the path ref when the path uses `uid:`.

---

#### [MAJ-009] Panel send is not idempotent — retries and double-clicks send twice

- **Lens**: Incompleteness / Incorrectness
- **Affected section**: §2.3 draft send row, FR-021, MC-16, US-7 AS-2
- **Description**: Send order is SMTP → Sent APPEND → `\Deleted` on the draft. The precondition only checks
  that the draft still exists at `uidvalidity:uid`. If SMTP succeeds and the gateway then returns 502
  (APPEND or delete timeout), or the draft-delete fails, or the human double-clicks, the draft is still
  there and a second Send passes the precondition. There is no warning field for "sent, but the draft
  could not be removed" (only `save_warning` for the Sent APPEND).
- **Impact**: The same approved email goes to external recipients twice — the kind of mistake users
  notice.
- **Recommendation**: Require an idempotency key (the draft's Message-ID plus the precondition). The gateway
  records "sent" for that key in memory for the draft TTL and returns the first result on repeat. Or check
  Sent for the Message-ID before transmitting. Add a `draft_cleanup_warning` to `MailSendResponse`, a
  B-scenario for double submit, and a test.

---

#### [MAJ-010] Sending a foreign draft silently drops its attachments and threading headers

- **Lens**: Incompleteness (data loss)
- **Affected section**: D24/FR-030, US-4 AS-6, B-24, §1 out-of-scope ("Attachments")
- **Description**: Attachments are out of scope, yet FR-030 lets the panel fully edit and send drafts
  started in the owner's own client, re-rendering "through the Markdown pipeline". A draft with an
  attachment loses it without a word. The loss statement lists styling, images and table layout only.
  Threading headers (`In-Reply-To`, `References`) of a foreign reply draft are not said to survive either.
- **Impact**: The recipient gets the message without the attached contract or invoice; the owner believes
  it was sent with it.
- **Recommendation**: For drafts with non-inline attachments, either block panel Send ("open in your mail
  client to send drafts with attachments") or name attachment loss in the statement and require an explicit
  confirm. Require `In-Reply-To`/`References` to be carried through. Add a DS-6 row.

---

#### [MAJ-011] The unread badge has no refresh rule while the panel is closed

- **Lens**: Ambiguity / Inconsistency
- **Affected section**: A2, A9, D25, B-16, B-18, FR-023
- **Description**: A9 puts the badge on the tab strip "while the panel is closed". A2 rules out push. D25/B-16
  only define refetching while the panel is open. So nothing fetches `GET …/mail/summary` while the panel
  is closed — the one time the badge matters — unless the SPA polls in the background, which the spec
  neither specifies nor bounds.
- **Impact**: The badge never shows new mail until a page reload (useless). Or each implementer adds
  their own background poll, contradicting the spirit of D25.
- **Recommendation**: State the badge cadence explicitly (e.g. summary polled every 60 s, matching the
  watcher, while a workspace view is visible; paused when the tab is hidden). Record it as an assumption
  the founder can override, and add it to B-18/T37.

---

#### [MAJ-012] "ADR-033 unchanged" is false — its inbound clause is deleted

- **Lens**: Inconsistency
- **Affected section**: Header line 9, §1 out-of-scope ("Any mailbox-model change (ADR-033 stands)"), D10
- **Description**: ADR-033 states: "Inbound: the heartbeat drainer creates Board tasks for unhandled mail in
  the mailbox's own workspace" (**Verified** `docs/internal/architecture/ADR-033-per-pair-mailboxes.md`
  line 29). This spec deletes that drainer and replaces it with a watcher that can start agent turns — a
  change to the canonical model's inbound behavior.
- **Impact**: After landing, the ADR describes behavior that no longer exists ("code wins over docs", but
  the next reader trusts the ADR). The new inbound trust decision (CRIT-002) is recorded nowhere durable.
- **Recommendation**: Add a dated amendment to ADR-033 (inbound = watcher; no tasks; opt-in agent turn,
  with its security posture) as part of this work, and correct the header and §1 wording.

---

#### [MAJ-013] The D19 configure-time permission fill has no test and a mis-traced requirement

- **Lens**: Incompleteness (test coverage) / Inconsistency
- **Affected section**: FR-016, §2.7 item 2, §7 (no row), §10 row FR-016 → B-23, B-29
- **Description**: D19 is a behavior change on a security surface: `ask` for the send tools, `allow` for
  the read and draft tools, **only** where the agent has no entry, and it deletes `grantEmailToolAllows`.
  Today's code writes `allow` for every email tool that is missing or `deny` (**Verified**:
  `pkg/gateway/rest_mailbox.go::grantEmailToolAllows`). No TDD row tests the new fill. T34 covers
  `create_email_draft` only. The matrix traces FR-016 to B-29, which is the watcher-switch scenario.
- **Impact**: A regression that keeps writing `allow` for `send_email` (today's behavior for a missing key)
  would pass every planned test.
- **Recommendation**: Add a unit test table: absent → ask/allow per tool; explicit allow/ask/deny →
  unchanged; seeded Admin (inventory `deny` on email tools) stays deny. Add a B-scenario "configure
  mailbox on an operator-created agent → send tools resolve ask". Fix the FR-016 trace.

---

#### [MAJ-014] `reply` under D26 is undefined

- **Lens**: Ambiguity
- **Affected section**: §2.4 row `reply` ("Same … recipient-list change as `send_email`"), MC-15
- **Description**: `reply` takes `uid` + `body` and derives the single recipient from Reply-To/From
  (**Verified** `pkg/tools/email.go::ReplyTool.Execute`). "Same recipient-list change" could mean: a
  required `to` list (breaking the tool), optional extra `cc`/`bcc`, or reply-all from the original's
  To/Cc. None is stated, and MC-15 cannot check a change that is not defined.
- **Impact**: Two engineers build different tools. A required `to` breaks every existing reply prompt.
- **Recommendation**: Define it: recipient stays derived; optional `cc`/`bcc` lists are added; optional
  `reply_all: bool` adds the original To/Cc minus the mailbox's own address. Update MC-15 and T7.

---

#### [MAJ-015] No bound on recipient count or address format on any send path

- **Lens**: Incompleteness / Insecurity (Denial of Service, reputation)
- **Affected section**: D26, §2.2 `MailSendRequest`, §2.4 `send_email`/`create_email_draft`, FR-022, FR-026
- **Description**: `to`/`cc`/`bcc` have `minItems` but no `maxItems`. Accepted address forms
  (`a@b` vs `"Name" <a@b>`), per-address validation, and de-duplication are unspecified. The rate limit
  (FR-026) covers REST routes only, not agent tools.
- **Impact**: One `send_email` call with a 2,000-address BCC list turns the operator's mailbox into a spam
  source. The provider suspends the account, and the loss affects every agent on that server.
- **Recommendation**: Add `maxItems` (e.g. 50 total recipients across To/Cc/Bcc) on every schema and tool
  parameter. State that each address is parsed with `net/mail.ParseAddress` (display names allowed and RFC
  2047-encoded, MC-4), with de-duplication. Add DS-5 rows at 50 and 51.

---

#### [MAJ-016] No DNS retry, no context-aware dial: the diagnosed dominant failure mode is not addressed

- **Lens**: Incompleteness / Infeasibility (reliability) — **added by the round-2 reviewer from the 2026-09-25
  live-instance diagnosis** (dispatch brief; not re-measured here — see the evidence table)
- **Affected section**: FR-027, A8, §5 row "New-mail watcher cycle" ("revisited after D22's diagnosis lands"),
  §17 "Chosen approach" (`dialIMAP` pattern "preserved"), MC-8, MC-21
- **Description**: The diagnosis reports that 74% of 2,769 IMAP dial failures on the live instance were
  instant "no such host" errors, arriving in windows where all 13 mailboxes failed in the same second, lasting
  up to 44 minutes. FR-027 only makes SMTP context-aware and closes the late IMAP connection. The IMAP dial
  itself stays as it is: `imapDial(addr, tlsCfg)` takes **no context** and resolves the name inside
  `tls.Dial`; `dialIMAP` wraps it in a goroutine plus a `select` on `dialTimeout` (30 s), so a cancelled
  request leaves the dial goroutine running (**Verified**: `pkg/email/transport.go::imapDial`,
  `::Client.dialIMAP`, `dialTimeout = 30 * time.Second`). The spec requires no bounded DNS retry, never
  names name-resolution failure as its own case, and hands the whole topic to "the D22 failure-triage
  dispatch" (A8, §5, §13). That diagnosis now exists, so the placeholder is an open design hole, not a
  deferral.
- **Impact**: In a DNS window every panel load, send and watcher cycle fails at once for up to 44 minutes.
  The Mail panel, the one thing this spec builds, is unusable for the most common real failure. A short
  DNS hiccup (the ~0.4% per-read rate) shows the human an error that one retry 200 ms later would have
  hidden.
- **Recommendation**: Add an FR to replace `imapDial` with a context-aware dial:
  `net.Dialer{...}.DialContext` (or `tls.Dialer.DialContext`) under the caller's context, fed to
  `imapclient.New`. Within one operation, retry **only** name-resolution failures (`*net.DNSError` with
  `IsNotFound`/`IsTemporary`/`IsTimeout`) a bounded number of times (e.g. 3 attempts, 250 ms → 1 s) inside
  the overall `dialTimeout`. Never retry auth or TLS failures. Apply the same dial to the SMTP path in FR-027.
  Add MC and TDD rows: an injected resolver that fails twice then succeeds gives one success, and a resolver
  that always fails returns the `dns` class (MIN-014) within the bound. Delete the "revisited after D22"
  wording from §5, A8 and §13.

---

#### [MAJ-017] No backoff after repeated failures; watcher cycles fire in lockstep across mailboxes

- **Lens**: Incompleteness (Denial of Service against our own mail server; log flood)
- **Affected section**: FR-023 ("Watcher cycles run every 60 s"), A8, DS-8, edge-case table
- **Description**: The watcher polls every mailbox every 60 s with no rule for what happens after failures.
  The diagnosis reports 13 mailboxes on one server failing in the same second, so today's poller dials in
  lockstep. The drainer being replaced loops over every mailbox on one tick
  (**Verified**: `pkg/email/drainer.go::Drainer.Drain`; `pkg/heartbeat/mailbox_drain.go` calls
  `Drain(context.Background())` on the tick). The spec carries that cadence forward ("today's cadence") with
  no exponential backoff, no jitter and no per-host grouping. DS-8's "mailbox in error" row has no stated
  expected cadence.
- **Impact**: Across a 44-minute window, 13 mailboxes × 44 cycles ≈ 570 failed dials, each logged. After
  recovery all 13 dial the server in the same second. For auth failures (a changed password), a fixed 60 s
  re-login loop is the pattern that trips server-side blocking such as fail2ban-style rules, locking out the
  owner's own mail clients as well (🔶 Inferred, medium confidence — depends on the server's policy).
- **Recommendation**: Add an FR: after N consecutive failures (e.g. 2), the watcher backs off per mailbox
  exponentially (60 s → 2 → 4 → … capped at 15 min) with ±20% jitter. Each mailbox's first cycle is offset
  by a random 0–60 s so mailboxes on one host never align. An `auth_failed` class backs off at least to the cap
  and never retries faster. A human-initiated panel Retry bypasses the backoff for that single request only.
  The summary exposes `next_attempt_at` so the badge can say "retrying at 14:05". Add DS-8 rows (3 failures →
  backoff reached; recovery resets it; jitter spread measured with a fake clock).

---

#### [MAJ-018] Login budget per mailbox is unbounded: watcher + 30 s refresh + tabs multiply IMAP logins (D25 reuse left unspecified)

- **Lens**: Incompleteness / Unfaithful to D25 ("connection reuse/limits to be specified given D22's timeouts")
- **Affected section**: §2.3 note "One IMAP session per REST request", A8, B-16, edge case 16 ("each refresh
  runs its own bounded session"), SC-004
- **Description**: Every IMAP operation logs in afresh: `dialIMAP` does dial + LOGIN + SELECT for each
  `ReadInbox`/`ReadMessage` call (**Verified**: `pkg/email/transport.go::Client.dialIMAP`,
  `::Client.ReadInbox`). The spec keeps that ("connectionless dial-per-operation … preserved", §17). One open
  panel refreshes the folder list **and** the open folder's message page every 30 s — two REST requests, so two
  logins. The watcher adds one per 60 s. Edge case 16 states that each extra tab runs its own sessions. The only
  bound is "2 concurrent operations per mailbox" (A8): that limits **concurrency**, not **login rate**, and it
  is per mailbox, not per server (13 mailboxes share one host). D25 asked for reuse/limits to be specified; A8
  instead declares the constants "implementation-tunable" and defers them to D22.
- **Impact**: One mailbox with one open panel sees about 5 logins/minute; with three tabs about 13. During a
  DNS or server outage every one of those is a failed dial, and when the server returns they all land at once.
  That is the "login storm" the diagnosis warns about, and nothing in the spec prevents it.
- **Recommendation**: Specify a per-mailbox login budget and request coalescing:
  (a) the folder-list and message-page refresh for one mailbox are served from **one** IMAP session per refresh
  tick (a combined endpoint, or server-side coalescing of identical in-flight reads per mailbox with a
  singleflight-style merge, so N tabs cost 1 login);
  (b) while the watcher's backoff (MAJ-017) is active for a mailbox, automatic panel refreshes do **not** dial —
  they return the watcher's last error class immediately (502 + class + `next_attempt_at`); only the explicit
  Retry dials;
  (c) a per-host cap on concurrent dials across mailboxes (e.g. 4).
  Keeping any connection open between requests is a founder question (Q-R2-9), because it touches D6
  ("nothing stored") only in memory. Add an MC: with 3 tabs open for 5 minutes against `startMemIMAP`, the
  LOGIN count is ≤ 1 per 30 s per mailbox plus the watcher's count.

---

#### [MAJ-019] The watcher can be silently blind exactly like the drainer it replaces

- **Lens**: Incompleteness (observability) / Insecurity-of-assumption
- **Affected section**: FR-023, FR-018, `MailboxNewMailSummary` (§2.2), MC-18, MC-23, T30/T31, UAT row 44
- **Description**: The diagnosis reports that the #631 drainer never once found unseen mail in 36 days and the
  cause is unknown. The drainer logs only on error or on success with `created > 0`; a cycle that finds
  nothing is silent (**Verified**: `pkg/email/drainer.go::Drainer.drainMailbox`). The watcher reuses the same
  poll-and-search mechanics under a new name, and the badge summary carries only `watcher_state` `ok|error`
  and `last_error_class`. It has no "last successful check" time and no `last_seen_uid`. So a watcher that
  dials fine but never sees new mail (wrong folder, search bug, server-side filter moving mail out of INBOX,
  another client) reports `ok` forever. Only a real-message test would catch it. Competing hypotheses for the
  drainer's blindness — (1) the mailboxes rarely receive mail; (2) another client or a server filter marks mail
  read or moves it first (the drainer searched UNSEEN — `InboxOptions{UnseenOnly: true}`); (3) a search or
  sequence bug in `ReadInbox`; (4) the service was not actually ticking — are all still open (❓ Unknown; not
  tested here).
- **Impact**: The switch "Let the agent handle new mail" and the unread badge can ship dead, as the drainer
  did for 36 days, and green unit tests against fakes would not show it.
- **Recommendation**: (a) Add `last_success_at` and `last_seen_uid` to `MailboxNewMailSummary`, and have the
  UI show "last checked hh:mm" next to the badge. (b) Log one structured INFO line per cycle at DEBUG level and
  one on every state change (ok↔error, backoff entered/left) at INFO/WARN — not per failed cycle (MIN-015).
  (c) Add an integration test against `startMemIMAP`: APPEND a message after the baseline, and one cycle
  advances `last_seen_uid` and `unseen_total`; also a message that another session marks `\Seen` before the
  cycle still advances the UID state (the watcher must key on UID, not UNSEEN). (d) Make UAT row 44 concrete:
  send a real mail to a watched mailbox on the live server and see the badge move within two cycles. (e) Before
  deleting the drainer, record which hypothesis explained its blindness, or state explicitly that the
  UID-based watcher makes it irrelevant and why.

---

### MINOR Findings

#### [MIN-001] Signature live preview under the MC-10 posture cannot show https logos

- **Lens**: Inconsistency
- **Affected section**: §16 "Signature editor", DS-1 real-world row, FR-002
- **Description**: The editor preview uses "the MC-10 posture", whose default `img-src` blocks https. The
  signature policy deliberately keeps https images (FR-003).
- **Recommendation**: Preview the signature with https images allowed (it is the operator's own content),
  or state that the preview shows the logo only after "Load images".

#### [MIN-002] Traceability errors

- **Lens**: Inconsistency
- **Affected section**: §10, B-18, §7
- **Description**: B-18 (badge) traces to US-3 AS-1 (folder listing). US-3 has no badge acceptance
  scenario. The MC-10 "per-directive handler tests" have no T-number. FR-016 → B-29 (see MAJ-013).
- **Recommendation**: Add a US-3 AS for the badge, number the MC-10 tests, and fix the traces.

#### [MIN-003] The mail-operation cap has no behavior when exceeded, and connection pressure ignores D22

- **Lens**: Inoperability
- **Affected section**: A8, §2.3 "One IMAP session per REST request", D22
- **Description**: "2 per mailbox" does not say whether excess requests queue, fail with 503, or wait with a
  deadline. Each 30 s refresh issues several REST requests (folders, messages, summary), each a fresh TLS
  login. With 13 mailboxes on one server already timing out (D22), the panel adds login pressure before the
  root cause is known.
- **Recommendation**: State the overflow behavior (queue with the FR-027 deadline, then 503 + class). Make
  the release of the watcher/panel cadence depend on the D22 triage verdict, recorded in §19.

#### [MIN-004] Rate limiter must be a dedicated instance

- **Lens**: Ambiguity
- **Affected section**: FR-026, MC-20, §15
- **Description**: `withRateLimit` takes a limiter instance (**Verified** `pkg/gateway/rest_auth.go::withRateLimit`).
  Reusing a shared one changes both budgets.
- **Recommendation**: Name a new `mailMutationLimiter = newAPIRateLimiter(10, time.Minute)` used only by
  the four routes.

#### [MIN-005] `mid:` multi-hit rule ignores `\Deleted` copies and unreliable INTERNALDATE

- **Lens**: Incorrectness
- **Affected section**: FR-028, §2.3 ref rules, FR-032
- **Description**: Without UIDPLUS, the old copy of an edited draft stays in Drafts with `\Deleted` and the
  same Message-ID. FR-028 does not exclude `\Deleted` from resolution. APPEND may set INTERNALDATE from
  the message Date, so "newest INTERNALDATE" can pick the old copy.
- **Recommendation**: Resolve `mid:` among non-`\Deleted` messages; break ties by highest UID.

#### [MIN-006] Audit `origin` enum has no value for owner-started drafts; `sent` is redundant

- **Lens**: Ambiguity
- **Affected section**: FR-025/MC-19, `MailSendResponse`
- **Description**: A panel send of a foreign draft is neither `human` compose nor `agent-draft`. `sent`
  is always true on a 200.
- **Recommendation**: Add `owner-draft` (or derive from `is_omnipus_draft`). Drop `sent` or define when it
  is false.

#### [MIN-007] Watcher failure logging will flood; state cleanup unstated

- **Lens**: Inoperability
- **Affected section**: FR-018, FR-023, D22 (2,769 timeout lines already)
- **Description**: A per-cycle error log × 13 mailboxes × 60 s repeats today's log noise.
- **Recommendation**: Log on state transitions (ok→error, error class change, recovery) plus one
  periodic summary. Delete the state file with the mailbox.

#### [MIN-008] The "all six touch points" list is not exhaustive

- **Lens**: Incompleteness
- **Affected section**: §2.7, FR-013
- **Description**: `pkg/coreagent/prompts_adr090.go` lists the standard email tools in agent prompt text and
  would not mention `create_email_draft` (**Verified**, line 70). The `Mailbox.yaml` `enabled` description
  and `pkg/agent/loop_wire.go` comment also enumerate the five tools. The spec calls drafts "the designed
  safe path", yet the prompts would not point agents to it.
- **Recommendation**: Add the prompt text (owned by prometheus-prompt-engineer) as touch point 7, with a
  sentence steering agents to draft first when policy is `ask`. Update the enumerating descriptions.

#### [MIN-009] Humans cannot reply from the panel, despite `in_reply_to` on `MailSendRequest`

- **Lens**: Incompleteness
- **Affected section**: US-5 ("answer mail without leaving Omnipus"), §2.2 `MailSendRequest.in_reply_to`
- **Description**: No Reply action, quoting or `Re:` behavior is specified, so the field has no caller.
- **Recommendation**: Either add a Reply action (pre-fill To/Subject, set `in_reply_to`) or drop the field
  and the US-5 wording.

#### [MIN-010] FR-009 still permits "Message-ID ↔ task/session" metadata that nothing writes

- **Lens**: Inconsistency
- **Affected section**: FR-009, §1, D6
- **Description**: The only writer of that link was the drainer, which is deleted.
- **Recommendation**: Drop the phrase, or name the writer.

#### [MIN-011] The outbound body sanitizer allowlist is unstated

- **Lens**: Ambiguity / Insecurity
- **Affected section**: FR-003, FR-004, MC-2
- **Description**: Only the signature policy's allowlist is described. It is not said whether agent Markdown
  may produce remote `<img>` (tracking pixels in outbound mail) or which URL schemes links may use.
- **Recommendation**: Name the body policy: links http/https/mailto only; images https only or none; no
  `style`.

#### [MIN-012] The deep link lands on a redirect-stub route; parameter handoff unstated

- **Lens**: Incompleteness
- **Affected section**: §16 "Route" (media redirect-stub pattern), §5.4 chat link scheme
- **Description**: The stub navigates back to Chat. The spec does not say how `mailbox`, `folder` and
  `message` survive that redirect into `mailPanel` state.
- **Recommendation**: State that the stub copies the query into `openMailPanel({agentId, folder, ref})`
  before redirecting, and test it in T42.

#### [MIN-013] The "Open draft" action parses a tool result that is not in the contracts

- **Lens**: Inconsistency (Hard Constraint #8)
- **Affected section**: FR-015, §2.4 `create_email_draft` result, §16 "Chat link → panel"
- **Description**: The SPA must parse `message_id`/`chat_link` from the tool result to render the
  action. That result crosses the gateway→SPA boundary but has no schema. The `web_serve` precedent uses a
  hand-written `WebServeResult` (**Verified** `src/components/chat/tools/WebServeUI.tsx`), which is itself
  at odds with Hard Constraint #8.
- **Recommendation**: Add `CreateEmailDraftResult.yaml` to `contracts/` and consume the generated type.

---

#### [MIN-014] The error-class enum has no `dns` class; B-14 names unreachable as `timeout`

- **Lens**: Ambiguity / Incompleteness (visibility)
- **Affected section**: §2.3 note "502 for upstream mail failures" (`timeout|auth_failed|tls|folder_missing|server_error`), MC-8, B-14
- **Description**: A "no such host" failure returns instantly, so it is not a timeout. With this enum it lands
  in `server_error` or is mislabelled `timeout` (B-14 expects `timeout` for an "unreachable" host). The
  diagnosis says this is 74% of real failures.
- **Impact**: The panel and the logs send the operator to the wrong cause (the mail server) when the
  problem is name resolution on the gateway host.
- **Recommendation**: Add `dns` (name resolution failed) and `connect_refused` classes. Map `*net.DNSError`
  to `dns`. Split B-14 into a DNS row and a black-hole row, and add both to DS-7/DS-8.

---

#### [MIN-015] Failure logging has no rate rule — a DNS window floods the log

- **Lens**: Incompleteness (operability)
- **Affected section**: FR-018 ("MUST log the raw upstream cause … never silent"), §11 IMAP row ("the watcher logs")
- **Description**: "Never silent" is required, but no rule says how often. With 13 mailboxes failing every 60 s,
  per-failure WARN lines produce the 2,769-line pattern already seen in `gateway.log` (D22).
- **Recommendation**: Log the first failure and every state change at WARN with the raw cause, then at most
  one summary line per mailbox per backoff step ("still failing, 7 attempts, class dns"), then one INFO line on
  recovery with the outage duration. Request-path (panel) failures always log once per request.

---

### Observations

#### [OBS-001] Speculative surface

- **Lens**: Overcomplexity
- **Affected section**: §2.3 `unseen_only`, §2.1 folder-name overrides
- **Suggestion**: No UI uses `unseen_only`; drop it until a caller exists. The folder overrides are
  justified only if special-use detection fails on the founder's server — worth confirming in the D22
  triage before building the advanced fields.

#### [OBS-002] Add an approval preview for `send_email`/`reply`

- **Lens**: Insecurity (usability of the other approval path)
- **Affected section**: FR-016, D15
- **Suggestion**: With recipient lists, BCC and Markdown bodies, the generic ask prompt makes it easy to
  approve without seeing BCC recipients. The approval-preview registry exists
  (`src/components/agents/approvalPreviews/registry.ts`); a mail preview entry would show To/Cc/**Bcc**,
  subject and rendered body.

#### [OBS-003] Consider shipping in slices

- **Lens**: Overcomplexity (delivery risk)
- **Affected section**: §1 scope, D1 (v0.1.1)
- **Suggestion**: The spec now spans the renderer and signature, SMTP hardening (#629), the Mail panel,
  drafts, the watcher (#631) and a security-sensitive HTML frame. Four independently gated slices would
  let the first two land while CRIT-001/CRIT-002 are designed: (1) render + signature + Sent APPEND + #629;
  (2) watcher replacing drainer, switch off; (3) read-only Mail panel; (4) drafts + panel actions.

---

#### [OBS-004] The diagnosis's resolver premise does not hold on macOS — do not write it into the spec

- **Lens**: Evidence quality (input to the fix round)
- **Affected section**: none yet — guards the fix round against copying the premise
- **Suggestion**: The brief says the `CGO_ENABLED=0` binary "uses Go's pure DNS resolver". On macOS that is
  not the default: Go's `net` package prefers the system resolver on Darwin even without cgo, calling
  libSystem directly, unless the binary is built with the `netgo` tag or run with `GODEBUG=netdns=go`
  (**Verified**: `$(go env GOROOT)/src/net/conf.go` — "Darwin pops up annoying dialog boxes … so prefer cgo",
  and `cgo_unix_syscall.go` is `//go:build !netgo && darwin`; Go 1.26.6). The repo's build sets tags
  `goolm,stdjson` only, with no `netgo` (**Verified**: `Makefile` `GO_BUILD_TAGS`; grep for `netgo|netdns` in
  `Makefile`, `scripts/`, `deploy/` returned nothing). The live data dir is under
  `/Users/danielpiatkowski/.omnipus`, so the live gateway runs on macOS (🔶 Inferred, high confidence — unless
  that binary was built differently, which is ❓ Unknown). On Linux, the pure Go resolver **is** the default
  for a `CGO_ENABLED=0` build. The requirement in MAJ-016 (bounded DNS retry, context-aware dial) stands on
  either resolver. The *cause* of the "no such host" windows should be recorded as unknown, not attributed to
  the pure resolver. A `GODEBUG=netdns=go+2` or `netdns=cgo+2` run on the live host would show which
  resolver is in use and what it returns.

---

## Structural Integrity Results (plan-spec mode)

| Check | Result | Note |
|---|---|---|
| Every user story has ≥ 1 acceptance scenario | PASS | US-1..US-7 |
| Every acceptance scenario has ≥ 1 BDD scenario | PASS (with caveat) | Badge behavior has scenarios but no AS (MIN-002) |
| Every BDD scenario has `Traces to:` | PASS | 37/37 |
| Every BDD scenario has a TDD test | PASS (with caveat) | B-28 only at unit level (MAJ-002); B-22 backend resolution only via a stubbed E2E |
| Every FR in the traceability matrix | PASS | FR-001..FR-033 |
| Every BDD scenario in the matrix | PASS | |
| Datasets cover boundary/edge/error | FAIL | DS-8 30+ row has no expected result; no recipient-count, foreign-attachment, double-submit or hostile-inbound-instruction rows |
| Regression impact addressed | PARTIAL | D19 fill untested (MAJ-013); ADR-033 drift (MAJ-012) |
| Success criteria measurable | PASS | SC-001..SC-007 have thresholds; counts in §18 match (18/5/7/7 = 37, **Verified** by grep) |

## Test Coverage Assessment

| Area | Assessment |
|---|---|
| Test levels | IMAP flag, APPEND, EXPUNGE and peek behavior is planned against fakes that cannot observe them. Use the existing `startMemIMAP` real-protocol harness (MAJ-004) |
| Negative tests | Missing: DNS failure then success (bounded retry), backoff/jitter under repeated failure, login count with several tabs, watcher detects mail already marked `\Seen` by another client, double-submit send, BCC absent from transmitted copy, UIDVALIDITY reset in the watcher, first-run baseline, recipient over-limit, foreign draft with attachment, stale `X-Omnipus-Markdown` |
| Boundary tests | Good for size (1 MiB), signature length, paging. Missing recipient count and Markdown-header size |
| Concurrency | Two-tab edit/send is covered by the precondition. Watcher cycle vs. running agent turn, and the cap overflow, are not |
| Idempotency | Absent for panel send (MAJ-009) |
| Regression | Drainer deletion is covered. The D19 fill is not (MAJ-013). The prompt-text touch point is missing (MIN-008) |

## STRIDE Threat Summary

| Component | S | T | R | I | D | E |
|---|---|---|---|---|---|---|
| Mail HTML preview frame | — | — | — | **Logged-in API GETs from untrusted HTML on Safari; `'self'` spans the API (CRIT-001)** | — | Frame escapes if CSP `sandbox` directive missing |
| Watcher + agent-turn switch | **Any sender triggers turns** | Injected instructions | Turns not audited | **Exfil via `fetch_url`/bash under Auto (CRIT-002)** | Turn floods (MAJ-001) | Tools in the turn not restricted |
| Mail connection layer (watcher + panel) | — | — | — | — | **DNS windows, no backoff, login multiplication (MAJ-016..MAJ-018)** | — |
| Send paths (agent + human) | — | — | Human sends audited; agent BCC not recorded in Sent (MAJ-005) | **BCC leak (MAJ-005)** | No recipient cap (MAJ-015) | — |
| Draft panel | — | Stale header discards owner edits (MAJ-006) | Audited | — | Double send (MAJ-009) | — |
| Configure-time policy fill | — | — | Logged | — | — | Untested fill could widen send to allow (MAJ-013) |

## Round-1 disposition verification (spec §20)

Every row of spec §20 was checked against the spec body in this worktree (commit `e2cbd0ca7` of the spec).
"Holds" = the cited text exists and does what the row claims; "Partial" = the text exists but a round-2 finding
shows the fix is incomplete.

| Round-1 finding | Spec's verdict | Round-2 check | Evidence / note |
|---|---|---|---|
| CRIT-001 drainer protected | fixed | Holds | FR-020, FR-023, §3.1 drainer row "deletes"; watcher gaps are new findings (MAJ-001, MAJ-019) |
| CRIT-002 send approval | rejected (D15) | Holds — founder decision | FR-016 text matches D15 + D19; the separate watcher-turn risk is new (CRIT-002 of this round) |
| MAJ-001 D11–D13 folded in | fixed | Holds | FR-007, FR-021, FR-029/030/032, US-7 |
| MAJ-002 unseen content sent | fixed | Holds | `MailDraftSendRequest` carries content + `uidvalidity`/`uid`; 409 row on the send endpoint (the edit endpoint lacks one — MAJ-008) |
| MAJ-003 draft-tool reachability | fixed | Holds | §2.7 six touch points; T34 |
| MAJ-004 Sent duplicates | rejected (D18) | Holds — founder decision | FR-006, T44 "appears" |
| MAJ-005 Message-ID addressing | fixed | Holds | FR-028; DS-4 rows |
| MAJ-006 raw-Markdown drafts | fixed | **Partial** | FR-029 chosen, but the header design is fragile (MAJ-006 of this round) |
| MAJ-007 preview headers | fixed | **Partial** | MC-10 is full but repeats the Library-preview Safari/`'self'` defects (CRIT-001 of this round; `pkg/gateway/library_isolation_policy.go` header comment) |
| MAJ-008 signature sanitization | fixed | Holds | FR-003, MC-2 |
| MAJ-009 MC-3 vs FR-004 | fixed | **Partial** | Rule is consistent now; the plain-part renderer it relies on is unproven (MAJ-007 of this round) |
| MAJ-010 #42 reconciliation | fixed | Holds | §19 table |
| MAJ-011 audit + rate limit | fixed | Holds for REST | FR-025/FR-026; agent-tool recipient bound missing (MAJ-015) |
| MAJ-012 polling + IMAP budget | fixed | **Partial** | Cadence defined (D25); login rate and reuse left open, contrary to D25's "reuse/limits to be specified" (MAJ-018) |
| MAJ-013 chat link origin | fixed | Holds | FR-015; §2.4 `create_email_draft` row |
| MAJ-014 traceability | fixed | Holds, one mis-trace | FR-016 → B-29 mis-trace (MAJ-013 of this round) |
| MAJ-015 #629 hangs | fixed | **Partial** | SMTP bounded; IMAP dial is still context-free and has no DNS retry (MAJ-016) |
| MIN-001…MIN-009, MIN-011, MIN-012 | fixed | Hold | Spot-checked: FR-010, FR-012, FR-014, FR-031, §2.2 field table, A4 versions; `emersion/go-message v0.18.2` present in `go.mod` |
| MIN-010 diagnostics | deferred-with-issue (#42 remainder) | Holds, conditional | Valid only if the #42 note is actually posted at landing — track it in the plan |
| OBS-001…OBS-003 | fixed | Hold | A4/§15; §14 zero-open count; FR-015 "Open draft" action |

**Result**: 2 rejections hold on founder decisions (D15, D18); the 1 deferral holds conditionally; 5 "fixed"
rows are only partly fixed. No disposition is false.

## Decision fidelity (D11–D26)

| Decision | Reflected? | Where / gap |
|---|---|---|
| D11 Library-style panel | Yes | FR-007, §16 |
| D12 View/Edit/Send/Discard | Yes | FR-021, US-7 |
| D13 + D17 HTML, images blocked | Yes (headers defective) | FR-019, MC-10 — CRIT-001 |
| D14 reconcile #42/#631 | Yes | §19 |
| D15 configurable send policy | Yes | FR-016 |
| D16 + D20 watcher, no tasks, no flags, BODY.PEEK, opt-in turn (default off) | Yes in intent | FR-020, FR-023, FR-024, A11; gaps: turn safety (CRIT-002), turn start and session (MAJ-002), UID state (MAJ-001), blindness (MAJ-019) |
| D18 IMAP-only Sent APPEND | Yes | FR-006 |
| D19 configure-time fill (ask/allow, absent keys only) | Yes | FR-016; untested (MAJ-013). **No contradiction with D15**: D19 only writes absent per-agent keys and, under strictest-wins, only tightens |
| D21 closes #629/#631, part of #42 | Yes | §19 |
| D22 failures visible, diagnosis pending | **Partly** | Visibility yes (FR-018, MC-8, B-14); the constants and the dial behaviour are still deferred to a diagnosis that has now reported (MAJ-016..MAJ-018) |
| D23 To/subject/body editable, identity kept | Yes | FR-021, FR-012 |
| D24 foreign drafts fully editable, loss stated | Yes | FR-030 states lost (styling, images, table layout) and preserved (text, headings, lists, links); attachments/threading gap is MAJ-010 |
| D25 30 s while open | Yes for cadence | B-16, T40; the "reuse/limits" clause is not delivered (MAJ-018) |
| D26 CC/BCC for humans and agents | Yes | §2.4 `send_email`, `create_email_draft`; `reply` undefined (MAJ-014); BCC leak (MAJ-005); no recipient cap (MAJ-015) |

## Questions for the founder

Each question: context and impact, options, recommendation. Answer in one line, e.g. "R2-1 A, R2-2 B".

**R2-1 — What may an agent do when new mail starts it with nobody watching? (CRIT-002)**
With "Let the agent handle new mail" on, any outside sender can start an agent turn. Today's seeded agents may
fetch web pages without asking, and a fresh install auto-approves shell commands. A crafted email could tell
the agent to read files and send them out.
- A: Mail-started turns run with a fixed safe tool set (read mail, create drafts, board and chat tools); anything
  else is refused in that turn, and nothing is auto-approved. **(recommended)**
- B: Normal policy applies, but every `ask` in a mail-started turn waits for a human (no Auto-approve).
- C: Normal policy, Auto-approve included; the risk is documented.

**R2-2 — Where does a mail-started turn run, and what if that chat is busy? (MAJ-002)**
- A: A dedicated "Mail" session per mailbox; a new message is queued if a turn is already running. **(recommended)**
- B: The agent's most recent session in the workspace.
- C: A new session per message.

**R2-3 — Should the Sent copy keep the Bcc line? (MAJ-005)**
The copy sent to recipients must never carry Bcc. The copy saved in Sent can keep it, so you can see whom an
agent BCC'd.
- A: Sent copy keeps Bcc; transmitted copy drops it. **(recommended)**
- B: Drop Bcc everywhere; the audit log records it instead.

**R2-4 — Drafts from your own mail program that have attachments (MAJ-010)**
- A: Show them read-only in the panel with "Open in your mail program to send". **(recommended)**
- B: Allow sending, carrying the attachments over unchanged.
- C: Allow sending and warn that attachments will be dropped.

**R2-5 — How often does the unread badge update while the Mail panel is closed? (MAJ-011)**
- A: Every 60 s from the saved watcher state; this reads a small file on the gateway, not the mail server.
  **(recommended)**
- B: Only when you open the workspace.

**R2-6 — Maximum recipients per message (MAJ-015)**
- A: 50 across To/Cc/Bcc, for humans and agents. **(recommended)**
- B: 20.
- C: No limit.

**R2-7 — ADR-033 says mail becomes Board tasks; this spec removes that. (MAJ-012)**
- A: Architect adds a dated amendment to ADR-033 in this branch. **(recommended)**
- B: Leave ADR-033 as is and note the change in the spec only.

**R2-8 — What happens after repeated connection failures? (MAJ-017)**
In the live log, 13 mailboxes failed together for up to 44 minutes, retrying every minute.
- A: Back off per mailbox (1 → 2 → 4 … up to 15 minutes, with a random spread); a wrong password waits the full
  15 minutes; your Retry button always tries at once; the badge shows "retrying at hh:mm". **(recommended)**
- B: Keep a fixed 60 s retry.

**R2-9 — May the gateway keep mail connections open between refreshes? (MAJ-018)**
Each refresh logs in again. With the panel open in several tabs, logins multiply. Keeping one connection per
mailbox open in memory (nothing written to disk) avoids that, but it holds open connections to your mail server.
- A: No kept connections; instead merge identical refreshes (N tabs = 1 login), and skip automatic refreshes while
  the mailbox is backing off. **(recommended — smallest change, stays inside D6)**
- B: Keep one idle connection per mailbox for up to 5 minutes.
- C: Both.

**R2-10 — Should we find out why the old drainer never saw new mail before deleting it? (MAJ-019)**
It found no unread mail once in 36 days, cause unknown. The new watcher polls the same way.
- A: Yes, a short read-only investigation on the live host first, and the watcher ships with a "last checked" time
  plus a live-mail UAT check. **(recommended)**
- B: No, delete it; rely on the new watcher's tests.

## Escalation to the founder

This is grill round 2 of 2. Round 1's CRITICAL findings are all closed or rejected on founder decisions (see the
disposition table). **Both CRITICAL findings of this round are new**. If they are still open after the round-2
fix, they go to the founder for a decision; no third grill round runs:

| Finding | Decision needed |
|---|---|
| CRIT-001 — mail HTML preview headers repeat the Library-preview Safari/`'self'` defects | Confirm the fix reuses the Library-preview isolation (named origin, CSP `sandbox`, `base-uri 'none'`, no redirects, a named inbound sanitizer) and gets security-lead's pre-implementation sign-off (already required by FR-019) |
| CRIT-002 — watcher-started agent turns have no tool limits | Question R2-1 |

## Verdict Rationale

**BLOCK**: 2 CRITICAL findings (CRIT-001 preview isolation, CRIT-002 unattended mail-started turns), both
security-relevant and both new this round. The 19 MAJOR findings include four connection-resilience gaps
(MAJ-016..MAJ-019) that leave the live instance's dominant failure mode (instant DNS failures in windows up to 44
minutes) with no design answer. Round 1's dispositions are honest; none is false, five are partial.

## Next action

```
Verdict: BLOCK

Review written to: docs/internal/specs/email-mail-view-spec-review-round2.md

This was grill round 2 of 2 (fixed, final). Next: team-lead interviews
the founder on "Questions for the founder", then the spec author fixes
round-2 findings. Any CRITICAL finding still open after that fix is
listed under "Escalation to the founder" above for the founder to
decide — do not run a third grill round. Once resolved, team-lead plans
the implementation (RED / GREEN / CHECK, the 8-reviewer gate).
```
