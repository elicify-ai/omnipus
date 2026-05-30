# How Omnipus works (in plain English)

Omnipus is a small **team of AI agents that you run yourself**. It is not a single
chatbot, and it is not a website you log in to that someone else hosts. You install
it on your own machine (or in Docker), point it at an AI model, and you get five
named teammates who can talk to you, take actions, and remember what you discussed.

![The agent roster](marketing/screenshots/04-agents-roster.png)
*Your five built-in teammates, ready to go.*

## Meet the team

You always start by talking to **Mia**. She figures out what you need and brings in
the right specialist. Here is who does what:

| Agent | Role | Reach for them when… |
|-------|------|----------------------|
| **Mia** | Coach & Guide | You're not sure who to ask. She's the default and routes you to the right teammate. |
| **Jim** | General Purpose | You want code written, files created or edited, a task scoped, or general day-to-day help. |
| **Ava** | Agent Builder | You want to create your own custom agent. She interviews you, then builds it. |
| **Ray** | Researcher | You need facts from the web, with sources. He won't make things up. |
| **Max** | Automator | You want a browser driven for you, or a task scheduled to run on its own. |

## A few ideas that make it all click

**Handoff.** When you ask Mia for something, she passes you to the right teammate
inside the *same* conversation. You don't copy and paste anything or start over —
the new agent simply takes over and keeps going.

**Sessions.** A session is just one conversation. You can have as many as you like,
switch which agent you're talking to at any time, and reopen an old conversation
later to pick up where you left off.

**Memory & self-learning.** When a chat goes quiet or ends, the app quietly writes a
short recap of what happened — and records **lessons learned** (what went well, what
to improve). The recap carries into your next session automatically, and the lessons
are saved so an agent can recall them when they're relevant. You never manage files or
notes: your team remembers the important bits, and builds on past work instead of
starting cold each time.

**Your preferences.** You can tell every agent how you like to work once, and they'll
all know it. In **Settings → Profile**, the *"What should the agents know about you?"*
box is shared with every agent — use it for standing preferences (your name, your
timezone, "always answer concisely", "I prefer Python", and so on).

**Delegation & parallel work.** Agents don't only talk to you — they can **delegate**.
An agent can plan a piece of work and assign it to a teammate as a **task**; the
assignee picks it up when it's started (by you, or automatically on the next scheduled
sweep). Capable agents can also **spawn subagents** to work on several parts of a big
job at the same time, then bring the results back together. You watch all of this on
the **Command Center** task board.

**Channels.** These are the ways you can reach your agents: the **web app**, the
**command line (CLI)**, or **14 chat platforms** like Telegram, Discord, and Slack.
The same agents and memory follow you across all of them.

**Skills.** A skill is a reusable add-on capability an agent can pull in when it's
needed — think of it as a tool the agent picks up for a specific job. You can install
more skills to teach your team new tricks.

**Tools & approvals.** Agents can actually *do* things: search the web, run a command,
browse a site, create a file. When an action could be sensitive, you'll see an inline
**Allow / Deny / Always** prompt before anything happens, so you always stay in control.
(Your API keys, by the way, are encrypted on disk.)

**Tasks.** A task is a piece of background work with a title, a priority, and an agent
assigned to it. You (or an agent) can create tasks, and the **Command Center** shows
them on a board you can track from queued → running → done.

## Two ways to use Omnipus

Most people start in the **web app**, but everything also works from the **terminal**
if you prefer typing commands. New here? See **[Getting started](getting-started.md)**.
Then explore the **[web app tour](using-omnipus-ui.md)** or the
**[CLI guide](using-omnipus-cli.md)** — whichever fits how you like to work.

## Next steps

- **[Getting started — your first 10 minutes](getting-started.md)** — install and have your first chat.
- **[Using the web app](using-omnipus-ui.md)** — a full tour of the interface.
- **[Using the CLI](using-omnipus-cli.md)** — drive Omnipus from your terminal.
