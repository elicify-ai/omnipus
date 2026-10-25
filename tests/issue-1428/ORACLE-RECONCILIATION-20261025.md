# #1428 send ownership — oracle reconciliation

## Authority and correction

Chief ruling, 2026-10-25: the per-chat composer model is authoritative. A merely typed draft belongs to its own chat. After navigating from A to B, B's composer is empty and cannot send A's invisible draft. Returning to A restores A's intact draft.

The original ownership case contradicted that model: it typed in A, attached B, and then invoked the current composer's Send while requiring delivery to A. That scenario conflated a retained draft with an already-started send. The no-leak oracle was correct; the ownership scenario was wrong.

This reconciliation changes only WHEN the ownership case starts Send. It does not change the expected destination, message count/content, absence of delivery to B, optimistic-bucket expectations, or any other test assertion. The test now captures the real Send promise while A is current, attaches B during the acceptance-to-dispatch handoff, and awaits the captured A intent. It never invokes B's composer to send A's draft.

## Independent expectations and test plan

| User action | Required result | Source / coverage |
|---|---|---|
| Type a draft in A, navigate to B without Send | B's current composer is empty; A's draft returns intact when A is reopened | Chief per-chat ruling; existing cross-chat no-leak test unchanged byte-for-byte |
| Press Send in A, then attach B before dispatch completes | Exactly one message is delivered to A, never B; optimistic user bubble belongs to A only | Chief send-intent ruling; reconciled ownership case retains every prior wire/bucket assertion |
| Queue a message in A before navigating to B | Queue draining preserves the destination A and puts nothing into B | Chief queue-before-navigation ruling; existing queue tests unchanged |
| Receipt is missing for the ruled deadline | Recovery state appears at 15 seconds, without cancelling the running agent | Founder Q67; DELIVERY_CONFIRM_DEADLINE_MS = 15_000 and its tests unchanged |
| Send is refused or an idle /cancel is used | Existing text-retention and feedback guarantees remain in force | All other ownership cases unchanged |

Real boundary: the chat/session stores, installed AssistantUI composer, runtime adapter and acceptance-to-dispatch handoff remain real. The existing transport recorder is at the process edge; no runtime/store/composer method is mocked or spied on. No production code is changed.

The three requested original-pack files are send ownership, cross-chat draft isolation and C1 turn ownership. Each runs separately, serially, through the installed shared slot runner. This is a RED authoring/reconciliation pass, not an independent CHECK audit. GREEN, mutation checks and the CHECK proof-of-failability gate remain deferred to CHECK.

## Baseline and limits

Requested production baseline: **5fec8eb6d40f7b8d5c9ba6b1757f6194bd3880e5**. The QA branch is cut from that exact commit in its own worktree; the developer's working copy is not edited or used for test execution.

No expected value is derived from current output. The expected destination remains A even if the implementation sends to B. The note is not an authorization to weaken another test, to change the 15-second deadline, or to send hidden drafts from B.
