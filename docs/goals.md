# Goals

A goal is a piece of work with its own definition of done. You state it, work starts in the same turn, and a separate Judge agent reads the work and decides when it is finished.

## What it is

A goal is its own thing, separate from a task and from the chat it arrived in. It owns a record: a restated statement of what you want, acceptance criteria ("Done when"), and a Definition of Done — standing quality checks for any piece of work. The Judge holds the work against that record.

Goals come from two places:

- **Chat.** Type `/goal` and what you want. The goal activates and work starts in the same turn. No form, nothing to confirm.
- **Tasks.** Every task on the Board carries a goal: the task form's required **Goal** field, criteria and Definition of Done become the record when the task runs. See [tasks](tasks.md).

An agent can also start a goal itself with `set_goal`; it then appears like any goal.

From there the two are one machine: same loop, same claim, same Judge. Only visibility differs.

The working agent writes the goal record itself, as its opening move, and shows it in the chat as working assumptions. You do not approve them; if they are wrong you say so, and the agent updates the record and shows what changed. If the agent is unsure it understands you, it asks — one round of questions per goal. On the web app that is a question card; on a connector, a plain question in chat.

When the working agent believes it is done, it claims completion. Only then does the Judge run — after the answer reaches you, so you never wait on a verdict.

### The Judge, in plain language

The Judge is its own agent, edited in **Settings → System agents**. You do not chat with it and cannot delegate to it. Its tools are read-only — open files, list folders, search files, read session records — and it cannot write, run commands or go online. During a review it reads only the workspace under review and the folders mounted into it; anything outside them is refused. Only its model and its written instructions are yours to change.

It reads work like a human reviewer: opens the artifact a criterion names, looks at what the agent did, forms a view. A criterion is met when the Judge is persuaded — never because a machine check passed. Each criterion gets its own verdict, met or unmet, with a reason.

One consequence is deliberate: unproven is not done. A criterion the Judge could not verify counts as unmet, even if the work was finished. The reason names the missing evidence.

## When you would use it

- Work you want verified, not only attempted. The agent keeps working until the Judge is satisfied or the try budget runs out.
- Work you want to walk away from. A quiet agent is nudged back to work; a verdict that lands later waits for you.

For a quick answer, plain chat is the right tool. A goal adds verification, and it spends tries to get it.

## How to set and steer a goal

1. In a workspace chat, type `/goal` followed by what you want, for example `/goal build a game portal with tetris and snake`.
2. Press Enter. The goal activates and the reply starts streaming in the same turn; a pill labelled **active** appears bottom-right.
3. Watch for the goal card early in the run: the restated goal, with criteria and the Definition of Done behind collapsed headings such as "Done when · 3 criteria".
4. Answer the question card if one appears.
5. To change direction, send a normal message such as "make it dark-themed only". The agent updates the record and shows what changed. Nothing asks you to confirm.
6. To restate the goal outright, type `/goal` with the new wording; the new intent replaces the working prompt.
7. To check on progress, type `/goal` alone (or `/goal status`) for elapsed time, tries used, the latest goal or Judge reason and the record.
8. To stop it, type `/goal clear`; the words `stop`, `off`, `reset`, `cancel` and `none` do the same.

A goal runs as a loop:

```mermaid
flowchart LR
  You[You set the goal] -->|starts instantly| Agent[Agent works]
  Agent -->|claims completion| Judge[Judge reads the work]
  Judge -->|every criterion met| Done[Goal done]
  Judge -->|not met, with reasons| Agent
```

The agent works, claims completion, and the Judge either ends the goal or sends the agent back with the list of what is missing.

## What you see while a goal runs

Each goal shows as a labelled pill at the bottom-right of the chat:

| Pill | What it means | What you can do |
|---|---|---|
| active | Work is under way | Steer by chatting |
| waiting on you | Paused, waiting for you | Reply in chat |
| judge unavailable | Judging is paused because the Judge cannot run | Wait for the Judge to become available; if it still cannot run, you get a **Not verified** conclusion (see below) |
| re-planning | A plan needs your correction before work continues | Review the request and reply |
| judging | The Judge is reading the work | Wait |
| verifying elsewhere | Another judgment of the same work is already running | Wait for that verdict |
| blocked | Something stands in the way | Remove the obstacle |
| claim overturned | The agent said done; the Judge disagreed | Read the reasons |
| done | Every criterion was judged met | Read the verdict |
| failed | Tries ran out with work unmet | Restate or raise the budget |
| cleared | You stopped it | Restart if needed |
| expired | Quiet for seven days with nothing running — a goal whose agent or helper is still working never expires | Restart it |

### Automatic reminders

An idle goal normally waits **60 seconds** without activity before another automatic reminder is eligible. In **Settings → Performance**, the reminder quiet window accepts a whole number from **30 to 3600 seconds**. It applies only when the goal is idle; it has no off switch and zero is not an off value. Busy-work check-ins use their own independent interval and switch, described below.

When an automatic reminder starts another work turn for a goal in your own chat—not a helper or task run—the web app shows **Goal reminder: continuing work toward the goal** as a system event line, not a message from you. The line is saved in that chat's history and remains after you reload it.

If that line cannot be saved, the live chat shows a generic turn error. The goal stays active, and this failed start leaves the reminder count unchanged. If chat history is unwritable, the explanation may not be saved either. A later reminder can continue the same goal once saving works again.

The same system line also appears when the Judge sends the agent back to keep working in that chat. Those Judge follow-ups are not automatic idle reminders and do not add to the no-progress count.

There is no lifetime limit on reminders. Instead, Omnipus counts consecutive automatic idle-reminder turns that **finish without progress**. Sending a reminder does not itself use up one of these outcomes, and a turn that is still running is not counted as finished. Progress resets the count to zero.

| Counts as progress | Does not count by itself |
|---|---|
| New goal evidence; a new saved change snapshot (commit) in the goal's workspace since the last check; an executed tool call other than `set_todos` or a claim-only `goal_claim` call; activity in a delegated helper; output or completion from background work owned by the goal session | A text-only answer, a prose completion claim, a claim-only message, or a turn that only updates `set_todos` |

A saved task check counts only when its recorded time sits strictly inside one run that belongs to this goal: after the clock second that run started and, if the run has finished, before the clock second it finished. A check written after the run closed, a check from a run started outside this goal, a check with no saved run, and a check whose time could belong to more than one run do not count. Those times are stored only to the second, so a check that lands in the same second a run starts or finishes is left out on purpose. An executed tool, helper output, background work, and a finished run still count on their own.

The file-change check reads committed workspace history, not uncommitted edits. Another chat's commit in the same workspace can count too. A file-writing tool that actually runs still counts under the tool-call rule, without needing a commit.

After **five consecutive completed reminders without progress**, automatic reminders pause. The chat shows this system line:

> No progress after 5 reminders — goal paused. Reply to continue.

The pause message is a goal status reason, not a Judge verdict. A normal reply clears it.

After three failed automatic reminder turns in a row, the same active goal also pauses; a successful reminder resets this failure count, failed turns never add to the separate five-reminder no-progress count, and a normal reply resumes it.

For this pause, the chat shows:

> 3 reminder turns failed in a row — goal paused. Reply to continue.

`/goal status` is read-only: it does not resume the goal or reset either reminder count. Rebuilding reminder tracking from saved state normally keeps the same failure-pause notice rather than adding another one; this is not a guarantee against duplicates at every possible crash moment. A normal reply starts a new cycle, so three later failed automatic reminders earn a fresh pause notice.

If Omnipus detects a failure while saving a reminder's final conversation state, it does not assess that reminder or add to either pause count. It reports that storage problem separately. Correct the underlying storage problem, then send a normal reply to start a new recovery turn on the same active goal. Repairing storage does not automatically retry the old turn's final save.

A reply on its own is no progress: if an automatic reminder completes without a real tool call (other than `set_todos` or `goal_claim`), helper or background activity, new evidence, or a goal-scoped committed file change, five such turns in a row pause the goal.

Send a normal reply to resume the goal and reset that count. This is a pause, not a failed or ended goal. The same rule applies before and after the agent writes its working goal record; there is no separate two-nudge fallback.

Ordinary automatic reminders wait while the goal session's background commands, dispatched tasks or plans, or delegated helpers are still working. Their output or completion counts as progress. An ordinary reminder that has already finished also waits for that work before its outcome is assessed. The stalled-work check-in below is the one exception. A Stop, an unanswered question or a blocked or waiting-on-you claim still holds automatic reminders, including check-ins. If a Stop is accepted before a waiting reminder is admitted, that reminder does not start. An ended goal receives none.

### A bell notice when a goal pauses

The notification bell shows a warning when the goal first pauses after five completed reminders without progress, after three failed automatic reminders in a row, or when the agent claims it is blocked or waiting for your reply. The warning names the chat and a short reason. Clicking it marks it read and opens that chat. It is not a Judge verdict.

There is at most one **unread** warning for the same goal in each recipient's saved bell history. A later pause updates that unread item rather than stacking another one. After you mark it read, a genuinely later pause can create a new item. Replying to resume the goal does **not** mark the warning read or remove it; read it or use the bell's mark-as-read control separately.

For a chat with a recorded owner, only that owner receives the warning. A genuinely shared or unowned chat notifies the configured accounts. With no accounts configured, it is a live-only broadcast to connected clients: there is no per-account saved item to recover after reload. If the chat's ownership metadata cannot be read, Omnipus logs the delivery problem instead of treating the chat as shared.

Saving a bell item can fail independently of pausing the goal. Omnipus logs that failure and still sends a live warning, but that live-only item cannot be recovered from saved history. Pausing and saving the bell item are not one transaction: a crash between them can leave a pause without a bell warning, and retrying an already-saved brake pause does not recreate its warning. Use the chat and goal status to check the goal itself; the bell is a notification, not its authoritative state.

### One check-in for stalled background work

While the goal's own background work is still busy, Omnipus observes the same progress signals listed above. By default, after **30 minutes without new progress**, measured from the first busy observation or the last fresh progress observation, it can start one system check-in on the same goal. **Settings → Performance** accepts a whole-number interval from **5 to 1440 minutes**. Stalled-work check-ins are **on by default**; their switch is the only way to turn them off. Turning them off retains the chosen minutes, and those minutes must stay within range even while off. It does not turn off ordinary idle reminders.

The two timers are independent. For example, a **5-minute** busy-work check-in can become due while a **3600-second** idle quiet window has not elapsed. Busy progress is observed on each sweep, not held behind idle quiet. Merely reserving or sending a check-in does not restart the separate idle quiet clock; real activity and the admitted turn keep their usual activity rules.

Both timers use the existing fixed **30-second** sweep. Successfully applied settings take effect on the next sweep without a restart, including for an already-active goal. A due reminder or check-in can therefore be noticed up to **30 seconds late**; this is scheduling delay, not a promise about when the agent will answer or finish. A save reported as not applied yet is not a successful live update; see [Goal timers in Settings](settings.md#goal-timers).

A check-in is not a message from you and grants no extra permissions. The agent is asked to inspect the listed work's actual status and available output, then choose whether to keep waiting, fix it or stop it—not start replacement work in parallel or replace the goal.

The check-in lists at most ten owned work identifiers, plus a count of any remaining items. It does not copy command lines, tool output, questions or approval requests, and preparing the list does not consume a background command's buffered output. The list is not proof that every helper or job was detected.

A successfully sent check-in is not repeated for the same observed stall. Fresh real progress re-arms it; an old retained progress signal does not restart the clock on every poll. If sending the check-in actually fails, a later check can try again. This tracking is in memory only: after a server restart the observation clock starts again, which can delay a check-in and can allow an extra one for work that had already been checked.

The check-in is a real reminder turn, not just a notification. Only after the whole turn has finished is it assessed under the ordinary progress and failure rules—even if the background work is still busy. A completed check-in without progress adds to the five-reminder count; a failed check-in uses the separate failure count. A real tool call during the check-in counts as progress under the same rule as any other reminder. Sending it or merely finishing its answer does not itself spend an outcome.

A helper still in the middle of a live turn is **not** covered by this check-in. The existing suppression remains even if that turn appears wedged: Omnipus warns in the server log after its stall grace, but does not start a check-in, cancel it or start replacement work from that branch. Stop, pending questions, claim pauses and either reminder brake continue to take precedence.

## What controls a goal

These numbers shape a goal's life:

| Control | Value | Where it lives |
|---|---|---|
| Tries per goal | 20 by default | Settings, Performance, "Goal completion budget" |
| Task attempts | 3 by default | On the task |
| Question rounds | 1 per goal | Fixed |
| Automatic-reminder pause | 5 consecutive completed reminders without progress, or 3 consecutive failed reminder turns; no lifetime reminder limit | Fixed |
| Idle reminder quiet window | 60 seconds by default; whole numbers from 30 to 3600 seconds; no off value | Settings, Performance; live without restart |
| Stalled-background-work check-in | On by default; 30 minutes without new progress by default, adjustable from 5 to 1440 minutes; one per observed stall, re-armed by fresh progress | Settings, Performance; independent switch and live interval; off retains minutes |
| Idle expiry | 7 days, never while work is running | Server setting |
| Active chat goals | 16 at a time | Server setting; tasks count separately |

A goal ends one of four ways — met, tries exhausted, expired, cleared by you — and the record is kept in each: what was wanted, and how it was judged, stays readable.

## Limits and things to watch

- The try budget is global. One setting covers chat goals and task goals alike; there is no per-goal override. A running goal keeps the limit it started with.
- On a task, tries bound the work inside one run; attempts bound how many runs the task gets. The task card shows both, for example "attempt 1 of 3 · try 5 of 20".
- Unproven counts as unmet. Finished work can be sent back when the Judge could not verify it; the reason names the evidence that was missing.
- Verdict speed follows the Judge's model — a fast one keeps verdicts quick.
- If the Judge's model is not offered by its named provider, or its named connection cannot be set up, the check reports the provider/model and the settings to fix rather than backing off and repeating the unchanged configuration. This also stops the helper's outer check loop after one adjudication, without a retry wait. An expired provider sign-in likewise needs an operator fix, not another unchanged check. Correct the Judge agent's model/provider pair, its provider's API key and endpoint settings, or sign in again as directed before checking again.
- The work itself has no wall-clock lifetime limit. The Judge's verification turn does: `judge.timeout_sec`, 420 seconds by default and at most 900. When that limit ends the verification turn, the message names the Judge's own check time limit, not a model provider failure.
- If checking is abandoned because the Judge is unavailable, chat, helper, task and plan paths produce **Not verified — the check could not complete**, with the reason and the worker's claimed result labelled **The work result, unverified:**. The embedded result is limited to 8,000 bytes; longer results end with **[truncated]**. Do not treat that conclusion as a complete copy of a long result.
  - In a chat goal, the conclusion is added to the chat and the goal stays active, so the agent can claim completion again later.
  - For a helper working on a goal, its parent agent receives one final conclusion with the helper's claimed result, not a generic provider-error replacement, and the parent chat shows **Not verified — the check could not complete** as a lasting line. A permanent configuration refusal includes the corrective action above. The helper is marked failed, while its goal stays active with no try used. Temporary Judge outages still receive the bounded retries before the final conclusion.
  - For a task, an unavailable-Judge exit ends the task **Failed**, with the conclusion in its result text and no attempt used. Re-run it after fixing the Judge or once it is available.
  - For a plan, the plan's handover carries the same **Not verified — the check could not complete** conclusion with the members' results, and the engine retries the round or parks the plan for a correction. See [Plans](plans.md).
- For a helper working on a goal, the parent chat shows the Judge's conclusion as a lasting row: **Goal met** when the goal ends met, a not-met-yet notice when the helper continues, or **Not verified — the check could not complete** when the check cannot finish. The helper keeps its own existing outcome/check entry. An unmet continuing round and a Not verified conclusion do not become a Goal met card; the goal stays active. One ending starts at most one parent turn, with the verdict condition, Judge reason and hand-back retained together. A Stop or an unanswered question can hold that turn without losing the report.
- An unverified criterion is different from an unavailable Judge. A review that ran but still could not verify a criterion can produce an **unmet** verdict, including after repeated failures following real review progress. That can consume a goal try, send the worker back to work, and eventually consume a task attempt if the run fails. Storage or delivery failures are reported separately; a helper conclusion is not routed to a guessed parent when its saved records cannot be read or do not agree about its parent.
- For an interactive chat goal, a detected failure to save the conclusion in the chat transcript or goal record, resolve the saved chat route, or queue the conclusion for that route produces a separate notice. A helper whose records cannot establish a safe audience uses the same notice: **Omnipus could not fully save or deliver the Judge's conclusion. Check disk space, file permissions and the chat's channel connection, then reload the chat. Open Verbose chat for details.** The notice is not a verdict on the work. It does not end the goal, use a try, or stop a reply that is still running. It is not a guarantee that every later connector-send failure or every plan/helper storage failure is reported this way. Reloading does not itself retry conclusion delivery.
- Ending a goal never erases it, but goal records are swept by the same retention rule as sessions. Keep what you need somewhere durable.

## Related pages

- [tasks](tasks.md) — the visible Board card; its run is judged by this machinery.
- [plans](plans.md) — chains tasks together toward a larger outcome.
- [agents](agents.md) — the roster, plus the System section where the Judge lives.
- [workspaces](workspaces.md) — where chats, boards and goals are scoped.
- [tools](tools.md) — what agents can do while they work toward a goal.
