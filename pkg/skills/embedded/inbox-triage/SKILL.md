---
name: inbox-triage
description: Classify permitted inbox messages into summaries, tasks, and drafts while keeping external sends approval-gated.
---
# Inbox Triage
## Prerequisites
Inbox access is permitted and the requested time/sender scope is clear.
## Steps
1. Read with read_inbox, search_email, and read_message.
2. Classify urgency, required action, and ownership.
3. Create requested tasks and prepare draft replies.
4. Use send_email or reply only after its Ask approval succeeds.
## Expected output
A prioritized inbox summary with intended tasks and drafts.
## Stop and handoff
Denied access or an unconfirmed send means no external message is sent; report the limitation.
