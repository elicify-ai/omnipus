---
name: Daily Briefing
description: Assemble a concise daily briefing — open tasks, recent activity, and what needs attention today. Use when the user asks for a daily summary, a standup, a catch-up, or "what's on my plate".
context: global
---

# Daily Briefing

Produce a short, scannable briefing of what matters right now. Optimise for signal:
the reader should know what to do next within ten seconds.

## When to use (trigger phrases)

- "daily briefing", "morning briefing", "standup"
- "what's on my plate", "catch me up", "what needs attention"
- A scheduled HEARTBEAT that asks for a status digest.

## What to gather

1. **Open tasks** — pull the current task list (`task_list`). Surface what is in
   progress and what is blocked, newest-relevant first.
2. **Upcoming deadlines** — from the task list, surface anything with a `due`
   date in the next 0–3 days (the `due` and `start` fields, plus `recurrence`
   for repeating tasks, are the calendar signal — there is no separate calendar
   tool). Flag overdue items first.
3. **Recent memory highlights** — recall salient context since the last briefing
   with `recall_memory` (e.g. "since yesterday") and surface decisions, learned
   facts, or commitments worth carrying forward.
4. **Recent activity** — note meaningful changes since the last briefing (completed
   tasks, new messages, agent handoffs).
5. **Attention items** — anything overdue, blocked, awaiting a decision, or failing.
6. **Today's focus** — the 1–3 things most worth doing today.

## Output shape

Keep it tight and skimmable:

```
## Daily Briefing — <date>

**Needs attention**
- <item> — <why>

**In progress**
- <task> (<status>)

**Today's focus**
1. <single most important action>
2. ...
```

## Rules

- Lead with what is blocked or overdue — never bury it.
- Omit empty sections rather than printing "nothing here".
- Be specific: link tasks by name, not vague references.
- If there is genuinely nothing actionable, say so in one line.
