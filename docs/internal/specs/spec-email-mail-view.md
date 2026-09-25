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
