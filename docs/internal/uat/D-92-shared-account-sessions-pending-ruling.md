# D-92 — Two sessions on one account share a chat (product characteristic, pending ruling)

**Status:** documented, not changed. Needs a founder ruling before any code moves.
**Source:** UAT 2026-09-13, defect D-92 (S3) in
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/evidence/2026-09-13/DEFECTS.md`
(lanes OP2 "Lane-integrity caveat", T1-j, U-67, GAP-PLAN-11).

## What happens today

One account is one operator. Two browsers signed in to the same account and working in the
same workspace chat share **one** chat session:

- both browsers see, and send into, the same conversation — a message from browser B can land
  in the middle of a turn browser A started (the agent treats it as a new instruction);
- a message sent while the other browser's turn is in flight can be dropped;
- each browser evicts the other's live connection (single live session per account);
- a pending approval raised by one browser's turn can be answered from the other.

None of this is a bug in the sense of code disagreeing with itself. It is the consequence of
the model the product was built on: **an account is a single person, and a chat session
belongs to the account, not to a browser tab.**

## Why it is not patched here

Every "fix" changes the product:

| Option | What it means | Cost |
| --- | --- | --- |
| Keep as-is, say so | Document: one account = one person; use separate accounts for separate testers. | None in code; UAT lanes must not share an account. |
| One live browser per account, hard | Second sign-in signs the first out (instead of evicting only the socket). | Loses the "same person on laptop and phone" case. |
| Per-tab chat sessions | Each browser tab gets its own session in the workspace; approvals routed to the tab that started the turn. | Session model, approval routing (D-16 work touches this), sidebar and history all change. |
| Multi-user accounts | Real shared accounts with per-user identity inside them. | A new identity concept; out of v0.1 scope. |

The security half of the observation (an approval answered by someone who did not raise it)
is already narrowed by D-16: approval prompts now reach only the account that owns the acting
session. Two browsers on **the same** account remain able to answer each other's prompts,
because to the product they are the same person.

## What the founder is asked to decide

1. Is "one account = one person, one live conversation" the intended model for v0.1?
   If yes: record it in the release notes and the UAT runbook (testers get one account each),
   and close D-92 as a characteristic.
2. If not: which row of the table above, and in which release phase (v0.2 hardening or the
   v0.3 Workspaces redesign, where session ownership is already being redrawn).

Until ruled, the UAT re-run should give each lane its own account.
