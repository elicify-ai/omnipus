# Email: HTML signatures, workspace Mail tab, agent drafts — interview output

- **Status:** Interview output (input to `plan-spec`; not the final spec)
- **Date:** 2026-09-25
- **Interviewer:** team-lead (chief session `session_01T5RGTNve11wAMUcQa4aL41`)
- **Founder:** Daniel Piatkowski
- **Integration branch:** `release/v0.1.1` · **Work branch:** `feat/email-mail-view`
- **Canonical model this extends:** ADR-033 — Per-(Agent, Workspace) Email Mailboxes (unchanged; no new ADR — see D10)

## Request (founder, verbatim)

> "we need to be able to configure a html signature, also we should give the possibility to see the emails agent receive and send"
>
> "i would like to have a mini outlook view and possible also to send emails manually maybe and definitely a way to approve an email draft, is this possible without saving the emails locally?"
>
> "the send tool needs approval [already], the idea is that an agent can create draft emails using the normal email provider functionality and add it to the draft folder and over in the chat a link to the user that he can click to view the email in a preview sidepanel"
>
> Mail view: "workspace mail but should be similar as the library and maybe reuse components"

## As-is (read by team-lead 2026-09-25, cite `file::symbol`)

| Area | Today |
|---|---|
| Ownership | Mailbox per (agent, workspace) pair — ADR-033; `pkg/config/config.go::MailboxesConfig` |
| Configuration UI | `src/components/connectors/EmailMailboxPanel.tsx::EmailMailboxPanel` (Connectors screen) |
| Wire | `contracts/components/schemas/Mailbox.yaml`, `MailboxConfigureRequest.yaml`; `pkg/gateway/rest_mailbox.go` |
| Outbound | `pkg/email/transport.go::buildEmailBody` — `text/plain` only, bare `From`, one recipient, no CC/BCC; `Client.Send` never APPENDs to Sent |
| Agent tools | `pkg/tools/email.go` — `read_inbox`, `search_email`, `read_message`, `send_email`, `reply`; INBOX only; workspace resolved structurally (`EmailTransports.resolve`) |
| Inbound | `pkg/email/drainer.go::Drainer` — unseen mail becomes Board tasks, marked \Seen; "no inbox UI in 0.1.0" |
| Registration | `pkg/agent/email_tools.go::registerEmailToolsForAgent` |

## Decisions Log

| ID | Topic | Decision | Rationale | Source | Date |
|---|---|---|---|---|---|
| D1 | Release | Ships in v0.1.1 on `release/v0.1.1` | Founder: v0.1.1 is nearly feature-complete to the old v0.3 scope | Founder Q1 | 2026-09-25 |
| D2 | Signature scope | One HTML signature **per mailbox** (per agent+workspace pair), edited in the mailbox panel with a live preview; a plain-text version is derived automatically | Matches ADR-033 ownership (different role per workspace) | Founder Q2 | 2026-09-25 |
| D3 | Outgoing format | Agents write **Markdown**; the tool renders it to sanitized HTML plus a plain-text part (multipart/alternative) and appends the mailbox signature. Agents never write the signature themselves | Safe formatting; no model-authored raw HTML | Founder Q4 | 2026-09-25 |
| D4 | Mail view placement | A **Mail tab inside the workspace**, similar in look and behaviour to the Library, **reusing Library components** where possible | Founder Q7 | Founder Q7 | 2026-09-25 |
| D5 | Folders | Inbox, Sent, Drafts only (humans and agents) | Founder Q8 | Founder Q8 | 2026-09-25 |
| D6 | Storage | **No local copy of mail content** — the Mail tab reads the mailbox live from the mail server (IMAP). Only reference metadata (e.g. Message-ID ↔ Board task / chat session) may be stored | Founder asked for "without saving the emails locally"; team-lead confirmed feasible | Founder Q3 | 2026-09-25 |
| D7 | Sent folder | Every agent- or human-sent message is saved to the mailbox's Sent folder (IMAP APPEND) so it appears in the Mail tab and in the owner's own mail client | Required for D5/D6 to show sent mail without a local store | Team-lead, accepted with Q3 | 2026-09-25 |
| D8 | Approval | `send_email` keeps its existing policy-based approval (unchanged). New: an agent can **create a draft** in the mailbox's Drafts folder using the normal provider mechanism (IMAP APPEND with `\Draft`) and post a **link in chat** that opens the draft in a **preview side panel** | Founder Q6 verbatim | Founder Q6 | 2026-09-25 |
| D9 | Manual send | Humans can compose and send mail manually from the Mail tab (signature applied) | Founder Q3 ("possible also to send emails manually") | Founder Q3 | 2026-09-25 |
| D10 | ADR | No new ADR — ADR-033 stays the canonical mailbox model; these decisions extend it. plan-spec escalates if it finds a genuinely open design decision | Team-lead judgement; flag if wrong | Team-lead | 2026-09-25 |
| D11 | Mail surface (spec Q7) | Library-style: Mail tab entry opens a docked Mail panel beside chat plus a full-screen pop-out; reuses the Library panel machinery | Founder chose recommended option A | Founder S7 | 2026-09-25 |
| D12 | Draft panel actions (spec Q1) | **View + Edit + Send + Discard** — the human can edit the agent's draft before sending; panel Send is the approval | Founder chose option C (not the recommendation) | Founder S1 | 2026-09-25 |
| D13 | Incoming HTML (spec Q3) | "Like Outlook and Gmail are doing it — it must feel normal." Neither client runs scripts in mail, so: rendered HTML in a sandboxed frame without scripts. Remote-image default (Gmail loads via proxy; Outlook asks for unknown senders) still to confirm | Founder verbatim; image default re-asked in the next round | Founder S3 | 2026-09-25 |
| D14 | Related issues | Spec must reconcile #42 (email send-gating + inbox UI + Gmail setup) and #631 (delete the mailbox drainer — it marks a human's unread mail \Seen); both found in the 2026-09-25 issue triage | Avoid duplicate/contradicting work | Team-lead | 2026-09-25 |
| D15 | Send approval (grill F1) | Keep today's configurable policy: `send_email`/`reply` ride the global ceiling (`allow`); built-in roles keep `ask`; the operator configures per agent. Correction to D8: `send_email` does NOT universally require approval — only where policy says `ask` | Founder: "keep today, user can configure as it is now" | Founder F1 | 2026-09-25 |
| D16 | Background mail job (grill F2, #631) | #631 verified still true 2026-09-25 (`pkg/gateway/gateway_boot.go` and `gateway_reload.go` wire `heartbeat.NewMailboxDrainService(drainer, 0)`, 1-minute interval, creates Board tasks, marks mail `\Seen`). Founder: it must NOT create Board tasks; a regular sync job "makes sense — that is how Outlook works", but its purpose and behaviour must be thought through before specifying | Founder verbatim; design open — see follow-up question | Founder F2 | 2026-09-25 |
| D17 | Remote images (grill F3) | Blocked by default with a one-click "Load images" per message | Founder chose recommended | Founder F3 | 2026-09-25 |
| D18 | Sent copy (grill F6) | Mailboxes are generic IMAP/SMTP only (no Gmail/Outlook-online special cases): every send is saved to the Sent folder via IMAP APPEND, no provider detection | Founder: "we use IMAP only" | Founder F6 | 2026-09-25 |
| D19 | Permissions on mailbox configure (grill F1b) | Configuring a mailbox sets `send_email` and `reply` to **ask** and the other email tools (`read_inbox`, `search_email`, `read_message`, plus any new draft/Sent-reading tools) to **allow** — written into the agent's own policy map only where that agent has **no explicit entry yet** (absent key = never set); an entry the operator already set (allow/ask/deny) is never changed. Replaces `pkg/gateway/rest_mailbox.go::grantEmailToolAllows`, which today turns a `deny` into `allow` | Founder: "configuring the mailbox must set sending to ask and the other email tools to allow"; team-lead's no-overwrite rule accepted | Founder F1b | 2026-09-25 |
| D20 | Background mail job (grill F2b) | Option C: replace the #631 drainer with a **new-mail watcher** that never changes flags and never creates Board tasks; it drives the Mail tab unread badge and stores only the last-seen UID per mailbox. Plus a per-mailbox switch "Let the agent handle new mail" (default **off**) that starts an agent turn in that workspace's chat; the agent reads with BODY.PEEK so mail stays unread for the human. Closes #631 | Founder chose recommended C | Founder F2b | 2026-09-25 |
| D21 | Defaults taken by team-lead (grill F8, F9) | F8: this work closes #629 and #631 and delivers part of #42 (the rest of #42 stays open with a note). Spec Q2: transcripts keep email text as every tool does; user docs say so plainly. Spec Q6: link addressing per grill MAJ-005 (folder + Message-ID reference) | Obvious defaults, stated to the founder; founder may override | Team-lead | 2026-09-25 |
| D22 | Live-instance observation | Founder: Board tasks from mail never appear on the live instance. Team-lead read-only check: `gateway.log` holds 2,769 `email transport: dial ... mail.elicify.ai:993` timeouts since 2026-08-20 while `nc` from the same Mac connects in 0.2s; 13 mailboxes on one server polled every 60s. Root cause not yet known — failure-triage dispatch | Evidence from `/Users/danielpiatkowski/.omnipus/logs/gateway.log` | Team-lead | 2026-09-25 |
| D23 | Draft editing scope (grill F4) | To, subject and body are all editable in the draft panel; the draft keeps its identity so the chat link keeps working | Founder chose recommended | Founder F4 | 2026-09-25 |
| D24 | Drafts started in other mail programs (grill F5) | **Full edit and send** from the panel, accepting possible formatting loss (the spec must state the loss plainly in the UI and say what is preserved) | Founder chose C (not the recommendation) | Founder F5 | 2026-09-25 |
| D25 | Mail panel refresh (grill F7) | Every **30 seconds while the panel is open**; never in the background from the panel (the D20 watcher is separate); connection reuse/limits to be specified given D22's timeouts | Founder: "30s when open" | Founder F7 | 2026-09-25 |
| D26 | Recipients (spec Q4) | To/CC/BCC and multiple recipients for **humans and agents** — `send_email`/`reply`/draft tools gain CC/BCC and recipient lists | Founder: "agents too" (not the recommendation) | Founder Q4 | 2026-09-25 |
| D27 | Mail never starts an agent turn (grill r2 R2-1, R2-2) | **An email never starts an agent turn.** The email tools are used actively by the agent inside turns started by humans/tasks/heartbeats — mail is not a trigger. D20's per-mailbox switch "Let the agent handle new mail" is **removed**; the new-mail watcher only feeds the unread badge / "last checked" state. Resolves CRIT-002 by removing the path | Founder verbatim: "an email never starts an agent turn, the email tools are intended to be actively used by the agent not be a trigger" | Founder R2-1/R2-2 | 2026-09-25 |
| D28 | Attachments (grill r2 R2-4) | **Download attachments** from any message in the Mail panel, and **send attachments** from compose and when editing drafts (incl. drafts started in other mail programs — attachments carried over). Whether agent tools may attach files (e.g. from the workspace) is an open question for the spec author to raise, not assume | Founder: "we need to be able to download attachments and send attachments" | Founder R2-4 | 2026-09-25 |
| D29 | Defaults taken by team-lead after grill r2 | R2-3 Sent copy keeps Bcc, transmitted copy never; R2-5 badge refresh every 60 s from saved watcher state (no IMAP login); R2-6 max 50 recipients To/Cc/Bcc; R2-7 architect adds a dated amendment to ADR-033 in this branch; R2-8 per-mailbox exponential backoff 1->2->4... max 15 min with jitter, manual Retry immediate, badge shows next retry; R2-9 no kept-open connections, identical concurrent refreshes coalesced (N tabs = 1 login), no auto-refresh while backing off; CRIT-001 fix reuses the Library-preview isolation with security-lead sign-off before implementation | Review recommendations, stated to the founder as defaults | Team-lead | 2026-09-25 |
| D30 | Old drainer investigation (grill r2 R2-10) | Founder sends one test email to one agent mailbox; team-lead watches the live gateway log read-only. Result recorded here before the fix round finalises the watcher design. Correction: the 2026-09-25 diagnosis's claim that the CGO_ENABLED=0 binary bypasses the macOS resolver is disputed (grill r2 OBS-004); the DNS-failure windows themselves are verified, their cause is unknown | Founder chose the test email | Founder R2-10 | 2026-09-25 |
| D31 | Mail checks without a trigger (founder 2026-09-25) | Spec and user docs state: agents handle mail when asked, or through a heartbeat / scheduled task the operator configures ('check your inbox'); nothing about mail is automatic | Founder chose recommended | Founder | 2026-09-25 |
| D32 | Drainer test result (D30) | Test 2026-09-25: Dmitri sent a mail to Aisha at 17:05:02Z; the drainer created Board task "Email: Omnipus mail test 20260925T170502Z" at 17:05:40Z; Aisha handled it and marked it done; message uid 1 = first mail ever in that mailbox. The old drainer works; the agent mailboxes had simply received no mail. It logs nothing on success (observability gap the watcher's "last checked" state closes) | Verified on the live instance (task JSON, staging note, session transcript) | Team-lead | 2026-09-25 |
| D33 | Agent attachments (spec FQ-1) | **Agents get full email capability, including attachments** (e.g. files from the workspace) on send_email / reply / draft tools. **No extra guard rails beyond the normal tool policy** — the attachment parameter follows exactly the same permission as sending (send_email / reply policy); no separate ask gate, no special caps beyond the general message limits already in the spec | Founder verbatim: "yes agents need full email capability no extra guard rails, same as send email" (not the recommendation) | Founder FQ-1 | 2026-09-25 |
| D34 | Branching (founder) | Build on a feature branch cut from the release branch: `feature/email-mail` from `release/v0.1.1` @ b6a6f8c87, carrying the spec history of `feat/email-mail-view` | Founder: "start building on a feature branch taking from release" | Founder | 2026-09-25 |

## Open points — plan-spec must ask the founder, not guess

| # | Question |
|---|---|
| O1 | Draft preview side panel: which actions — view only, edit, Send, Discard? Does "Send" from the panel count as the approval (bypassing the `send_email` ask prompt), or go through it? |
| O2 | Chat transcripts likely already store the text of every email an agent sent (tool-call arguments). Is that compatible with D6, or must email bodies be redacted from transcripts? (team-lead: unverified — check `pkg/session` persistence first) |
| O3 | Incoming HTML rendering in the Mail tab: sandboxed frame, remote images blocked by default with a "load images" click? (security-relevant; `security-lead` review mandatory) |
| O4 | Recipients: keep one-recipient-only for agents, or add CC/BCC/multiple To now (manual compose likely needs them)? |
| O5 | Drainer interaction: the drainer marks mail \Seen — how should the Mail tab show "read by agent" vs "read by human"? |
| O6 | Where the chat link points: a route for `(workspace, mailbox, folder, uid)`; IMAP UIDs change when UIDVALIDITY changes — Message-ID based addressing? |

## Constraints that bind the spec

Hard Constraint #8 (contract-first: every new endpoint/schema in `contracts/` first), #6 (new tools — e.g. a draft tool and Sent/Drafts reading — get ceiling policy defaults via `config.ReconcileToolPolicyCeiling`; Definition of Done reachability grep), pure Go (#2), design-system skill for all UI, size budgets, `security-lead` on-demand review (HTML sanitization, credential use in gateway endpoints).
