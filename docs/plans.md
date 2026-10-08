# Plans

A plan is a group of related tasks with dependencies and one shared Definition of Done (DoD), which Omnipus runs for you from start to finish.

## What it is

A plan lives in a [workspace](workspaces.md) and has three parts:

- **Member tasks** arranged as a dependency graph. A task waits until the tasks it depends on have finished, then runs. A task outside any plan is covered in [tasks](tasks.md).
- **A Definition of Done**: the conditions the finished work is checked against.
- **An owner agent**, accountable for the plan and woken at its decision points.

The engine runs plans on the server: it hands each unblocked task to its assignee, checks the result against that task's acceptance criteria, and judges the DoD once every member has finished.

You see plans in the **Tasks** panel of a workspace, as tiles in the collapsible **Plans** band above all three task views showing a coloured status dot and status word, plain progress such as "4/6", the owner, and the eligible actions. Selecting a tile filters the view to that plan's tasks and switches to the Graph view, where the dependency graph is drawn.

The Plans band is open by default when plans exist, and collapsed when there are none. Click **Plans** to fold or unfold its horizontal tile strip. The compact header holds only **Plans**, its fold control and **New Plan**, and stays visible while folded. Space above and below the tiles is compact. Completed plans remain hidden by default; the unchecked **Unhide done plans** checkbox sits at the bottom-left of the tile area, below **All tasks**, when completed plans exist. Checking it reveals their tiles. When more tiles are off-screen, **scroll for more →** signals sideways scrolling, including on a phone. Long plan titles wrap fully within their tile, normally at spaces. A word or URL longer than the available width breaks only as a last resort. The whole title is visible without hovering.

Every plan tile has an **info icon** showing its full title, status, progress (done/total), owner agent and last updated time. Hover over the icon, focus it with the keyboard, or tap it on touch. You can move into the details panel to read it. The panel appears above scroll areas and closes when you move away, press **Escape**, or click/tap outside. It does not select the plan. Selecting the tile's title or body still filters the tasks and opens Graph.

Graph opens at the top of the map, with its first card fully visible. Its zoom controls sit below the map rather than on top of cards. Graph task titles use a fixed two-line slot with ellipsis. Task details use the same info-icon hover, keyboard-focus and touch-tap interaction, with full title, status, agent, tags, plan and update time, and **Open task** inside it. Hovering the rest of the task node does not open details. Metadata is plain text rather than pills; eligible actions stay visible without hovering.

A running plan tile shows a spinning arrow without a token count. In the plan's Graph view, each **In Progress** task node uses the same arrow. Board and List running-task indicators also omit token counts; Chat's existing token counter is unchanged. With reduced motion enabled, the arrow stays visible but does not spin.

A plan moves through five states. This table shows what each one means and what you can do from the tile.

| State | Status appearance | What it means | Your action |
|---|---|---|---|
| Draft | Grey | Authored, not running | Execute |
| Approved | Blue | Accepted, waiting for a free run slot | Stop |
| Running | Gold | The engine is working | Stop |
| Done | Green | Finished; the Definition of Done was met | None |
| Failed | Red, or orange "Cancelled" | Ended. Orange means you stopped it | Restart, orange only |

```mermaid
flowchart LR
  Draft[Draft] -->|Execute| Approved[Approved]
  Approved -->|slot frees| Running[Running]
  Running -->|DoD met| Done[Done]
  Running -->|DoD unmet| Parked[Awaiting supervision]
  Parked -->|corrected| Running
  Parked -->|corrections run out| Failed[Failed]
  Running -->|rounds or idle run out| Failed
  Running -->|Stop| Stopped[Cancelled]
  Stopped -->|Restart| Approved
```

A plan runs to Done, parks for supervision when its DoD is unmet, or fails; only a plan you stopped can be restarted.

## When you would use it

Use a plan when several tasks depend on each other and you can state a checkable finish line. For one job, create a task. Agents can also author and run plans from a conversation, as [agents](agents.md) describes.

## How to create and run a plan

1. Open your workspace's **Tasks** panel and press **New Plan**. A form opens beside the board.
2. Give the plan a title, an optional goal and description, and pick the owner agent from the team. Title and owner are required.
3. Add Definition of Done items and, if you want different limits, change the Bounds. Save the plan; it appears as a Draft tile.
4. Add member tasks with the **+ New Task** action: pick the plan, set what the task is blocked by, and give it at least one acceptance criterion. Two fields matter for parallel work — the paths a task creates or edits, and the merge-point marker for a task several parallel tasks feed into.
5. Press **Execute** on the plan tile and confirm. Before anything runs, the plan is checked: every member needs a criterion, parallel tasks may not write the same path, and parallel work may not converge on an unmarked merge point. A refusal names the offending tasks.
6. Watch the tile: the plan turns Approved, then Running once the engine admits it, with quiet phase chips (dispatching, judging, synthesizing) while it runs.

After Execute, nothing further needs your approval: tasks run, are checked, and are retried on their own.

## Definition of Done

The DoD uses the task criteria editor. Each item is a plain statement judged by reading the evidence, a technical check (a command and the exit code that counts as pass), or an action-count check (a tool that must be called a set number of times).

A plan created in the interface may leave the DoD empty — the work is then judged against the goal text. An agent-authored plan must carry one. Every member task also needs a criterion of its own, or the plan refuses to start.

## When a step fails: the Judge and the Plan Supervisor

Two built-in system agents — not chat colleagues — handle checking and rescue.

- **The Judge** rules each task's criteria met or unmet as the task finishes, and the plan's DoD when all members finish. It works read-only, in its own session.
- **The Plan Supervisor** is woken when a finished plan's DoD is unmet, or the graph stalls with nothing to dispatch. It applies one correction per wake.

The supervisor can apply four corrections.

| Correction | What it does |
|---|---|
| Append | Adds new tasks to the end of the graph |
| Supersede | Replaces a finished task with new work that inherits its criteria |
| Targeted retry | Re-runs one failed task |
| Abandon | Ends the plan, recording that the Definition of Done cannot be reached |

After a correction the plan returns to running. The plan fails when its configured supervision-attempt limit is exhausted; the shipped default is three attempts without a valid correction. The tile shows a warning chip — "Re-planning — awaiting supervision" or "Stalled — needs a correction" — with an explanation underneath. There are no correction buttons for you: the supervisor is the only actor, and **Stop** halts the tasks, the checks, and the supervisor's review alike.

## Limits and things to watch

- **Restart exists only for plans you stopped.** A plan that failed on its own — rounds exhausted, idle too long, DoD unreachable, supervisor unavailable — is closed for good. Write a new plan.
- **Restart re-runs only unfinished tasks**, each from scratch. Finished tasks and their evidence are kept.
- **Stop is a hard cancel** — in-flight tasks, checks, and shell commands. A stopped plan never resumes on its own.
- **Run slots follow the install-wide active-loop cap**—16 by default—shared by plans, conversation goals and recurring loops. A plan can wait at Approved and stays stoppable there.
- **Bounds are per plan** — the judge-round ceiling and idle-expiry days, defaulting to 20 rounds and 7 days. A plan idle that long ends failed.
- **A running plan can pause.** If its owner agent is disabled, the plan pauses and the tile shows the reason.
- **A running plan cannot be deleted.** The Clear action in a tile's menu works only on plans that are not running.
- **Agent-driven plan execution is permission-gated.** On a fresh install, Jim is allowed to execute and stop plans. Mia, Ava and Admin are denied those tools by their shipped role policies. Operators can configure supported per-agent permissions. [Tools](tools.md) explains the per-agent settings.

## Related pages

- [tasks](tasks.md) — single tasks, their acceptance criteria, and the board they share with plans.
- [workspaces](workspaces.md) — where the Tasks panel and its plan band live.
- [agents](agents.md) — the owner agent's role, and the Judge and Plan Supervisor as system agents.
- [goals](goals.md) — the same judging, applied to a goal set in a conversation.
- [tools](tools.md) — the permission settings that gate plan tools per agent.
