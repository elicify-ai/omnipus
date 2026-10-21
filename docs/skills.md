# Skills

A skill is a written playbook an agent picks up when a task calls for it. This page explains what a skill is, how to add one, and how to decide which agents may use it.

## What it is

A skill is a folder that holds a single file, `SKILL.md`: a short description, and instructions written in Markdown. Installing a skill changes nothing on its own. When Omnipus first builds a session's advertised skill menu, it lists the skills granted to that agent by name and description. Later turns reuse that captured menu; granting another skill does not automatically refresh the menu of an active session. The full instructions are read only when the agent decides the task matches, and they apply to that task only.

This is what separates a skill from its neighbors. [Tools](tools.md) are the actions an agent can take. [Memory](memory.md) is what an agent remembers between conversations. A skill is how you teach a repeatable procedure once, so any agent can follow it the same way every time.

The format is the common `SKILL.md` convention other agent tools use, so a repository that already carries skills works as-is.

```mermaid
flowchart LR
  You[You] -->|install or write| Library[Skills folder]
  Library -->|grant to an agent| Menu[One line in the agent's context]
  Menu -->|task matches the description| Body[Full instructions, read on demand]
```

A skill sits in the folder until you grant it; a granted skill stays a one-line entry until a task needs the whole thing.

Skills reach your agents from four places.

| Source | What it is | How it gets there |
|---|---|---|
| Your skills folder | Skills you install or write, grantable to any agent | Lives at `~/.omnipus/skills` in your data folder |
| Shipped skills | Built-in playbooks that come with Omnipus | Seeded automatically on first boot |
| A mounted folder | Skills that belong to a repository and travel with it | Put them in `.omnipus/skills` or `.claude/skills` at the root, then [mount the folder](library.md) |
| The ClawHub registry | Community-published skills | Search and install from the Skills & Tools screen |

Omnipus ships 21 embedded skill packages covering setup, planning, research, and document workflows.

## Omnipus's initial setup skills

On a fresh install, Omnipus receives these existing setup playbooks automatically:

| Purpose | Skill identifiers |
|---|---|
| Clarification and troubleshooting | `interview`, `doctor` |
| Agents, skills and workspace teams | `agent-authoring`, `skill-authoring`, `tool-mapping`, `skill-mapping`, `delegation-graph`, `workspace-team` |
| Connectors, providers and channels | `mcp-install`, `provider-setup`, `channel-setup` |

Mia, Jim and the other built-in agents keep their existing skill assignments. Assigning a skill does not grant tool permissions: Omnipus's tools still follow the same global and per-agent permission rules. If you change or clear an agent's skill grants, a later restart does not restore the initial list.

## When you would use it

- A procedure repeats. Your release checklist, your invoice handling, your house style for customer replies. Write it once, grant it to the agents that need it.
- Someone already wrote it. The ClawHub registry carries community skills; search before you write.
- Your repository carries its own skills. Mounting the folder is enough for its skills to become available to agents in that workspace.
- An agent offers to capture a procedure it just performed. Say yes, and it writes the skill for you.

## How to add a skill and grant it

1. Open **Skills & Tools** in the sidebar. You see three tabs: Installed Skills, MCP Servers, Built-in Tools.
2. Click **Browse Skills**. A search window opens over the screen.
3. Type what you are looking for. Results come from the ClawHub registry, each with its name, version, and summary.
4. Click **Install** on the one you want. The skill appears under Installed Skills, marked Community.
5. To use a file you already have, choose **Install from file** in the same window. A file install cannot be verified against a published checksum, so Omnipus shows what the skill claims to do and asks you to confirm.
6. Grant the skill. Open the agent, new or existing, and tick the skill in its skills list. An agent with nothing ticked has no skills.
7. Send the agent a task that matches the skill's description. You can also type `/` in chat and pick the skill to activate it for that one message.

You can also skip the screen: ask an agent in chat to find and install a skill, or to write one for you. Agents ask for your approval before they write or change skill files.

Each card under Installed Skills shows when the skill was last used. "Never used" on a skill you granted is the sign its description is not doing its job.

## What a skill file contains

A skill is one folder, one file. These are the parts the app reads.

| Part | Required | What it is for |
|---|---|---|
| Folder name | Yes | The skill's identifier — what you grant and what follows `/` in chat. Letters, digits, and single hyphens, up to 64 characters. |
| `description` | Yes | One line saying when to use the skill. At most 1024 characters. Falls back to the first paragraph of the body. |
| Body | Yes | The instructions, in Markdown. Read only when the agent loads the skill. |
| `name` | No | Display name shown in the app, such as "Daily Briefing". Falls back to the folder name. |
| `author`, `version` | No | Shown on the skill's card in Installed Skills. |
| `argument-hint` | No | A hint shown next to the skill in the chat `/` menu, such as `<query>`. |

The optional `allowed-tools` metadata accepts a space-separated scalar (`Read Grep Bash`), a comma-separated scalar (`Read,Grep,Bash`), or a YAML list (`[Read, Grep, Bash]`, or one item per line). Surrounding whitespace is removed; tool-name case and declaration order are kept. Empty declarations produce no tool entries. Declaring tools here does not grant permission to use them; the agent's configured tool permissions still apply.

The description is the part that decides whether the skill is ever used. The agent picks a skill by matching the description against the task, so write it as a trigger: "Use when the user asks to cut a release or publish release notes" fires; "Release process documentation" does not. Name the phrases a task would actually contain.

### Keep the description predictable

If you omit `description` from the frontmatter, Omnipus uses the first parsed paragraph with non-empty extracted text as the skill's advertised description. Paragraphs inside `<div>` and `<details>` wrappers can now count too, when the tags are on their own lines with blank lines around the inner Markdown, as in the example below. An introductory banner inside such a wrapper can therefore become the description instead of the useful task-trigger paragraph after it.

Do not rely on an HTML wrapper to hide text from this fallback. Set an explicit frontmatter `description` to keep the task trigger stable:

```markdown
---
description: Use when the user asks for release notes.
---

<div>

Introductory banner text.

</div>

Write the release notes using the steps below.
```

The explicit description wins over body text. If you rely on the fallback instead, put the intended task-trigger paragraph before banners or wrappers.

## Skills in another workspace

When Omnipus sets up a workspace other than the one you are chatting in, its skill tools take a `workspace_id`. Leaving it out means the workspace of the current conversation — except for `create_skill`, see below:

- `edit_skill` and `remove_skill` look for a project skill (one that lives in a folder mounted into that workspace) in the named workspace first, then fall back to your installation's skills if no project skill is found. These change tools require a complete project-shelf scan: errors listing a recognised skills folder, resolving a skill's path, or reading its contents return `READ_FAILED`; nothing is changed. A genuinely missing path is not a read failure. A parent folder denying access or a skill file denying reading is an error, not proof that the project skill is absent. Restore access and read the skill again before retrying. A project skill with the same name in another workspace is not touched.
- `create_skill` with a `workspace_id` creates the skill as a project skill in that workspace's mounted folder. It reuses the project's existing recognised skills directory: the Omnipus directory if present, otherwise the Claude directory if present. If neither exists, it uses the Omnipus directory. This needs exactly one mounted folder: with none, or with several, it is refused and nothing is saved. Without a `workspace_id` it creates an installation-wide user skill, available in every workspace — the one exception to the rule above, and the only way to create such a skill.
- `list_skills` (management view) shows the named workspace's project skills. Reading one skill by name checks only that name across the workspace's mounted folders; an unreadable skill with a different name does not block it. The requested skill's own read failure still returns `READ_FAILED`, never a same-name installation-wide copy. If a mounted folder or recognised skills directory cannot be inspected, the named read also refuses because it cannot establish which copy exists. Listing the full management inventory without a name still requires a complete scan and returns `READ_FAILED` if any project skill cannot be read.

Only Omnipus may name a workspace other than the current one. Each of these tools reads the written file back and reports one of the four outcomes described in [tools](tools.md#what-a-change-tool-reports).

A skill installed from a GitHub link takes its folder name from the link. That name must be a valid skill name — letters, digits and single hyphens, at most 64 characters, not a reserved Windows device name such as `CON` — otherwise the install is refused before anything is written. A link that hides a slash or backslash in percent-encoding (`%2F`, `%5C`, or a double-encoded form) is refused outright, before anything is downloaded.

## Limits and things to watch

- Installing is not granting. A new skill sits unused until you grant it to an agent, and a new agent starts with no skills at all.
- A skill that never fires looks identical to a skill nobody needed. The "Last used" row on each card is the only signal — check it before assuming a grant worked.
- Registry installs are checked against a published checksum; file installs cannot be. The setting lives in Settings, under Security: **Block unverified** refuses installs with no checksum to check, **Warn unverified** allows them with a warning (the default), and **Allow all** turns checking off.
- Editing a shipped skill never changes the original. Your edit becomes a copy in your skills folder that overrides it, and every save keeps a snapshot of the prior version under `.versions` inside the skill's folder, so a bad edit is recoverable.
- A mounted folder's skills come with the mount, available to every agent in that workspace. They cannot replace a skill you granted under the same name — your grant wins.
- Each skill in a session's advertised menu adds its name and description to later requests. Omnipus captures that menu when it first builds a request for the session and reuses it. Changes to grants, descriptions or mounted skill collections do not automatically refresh an active session's captured menu; a new session builds a fresh one. A large collection therefore enlarges the instruction prefix when the menu is captured. Trimming or unmounting it does not refresh a menu already captured. The advertised menu is not an access grant: current permissions still govern loading a skill and running its tools.

## Related pages

- [tools](tools.md) — what tools are, and how allow, ask, and deny permissions govern them.
- [agents](agents.md) — creating agents and choosing each one's skills.
- [library](library.md) — mounting a folder into a workspace, which is how a repository's own skills arrive.
- [memory](memory.md) — what your agents remember across conversations — different from a procedure you teach once.
- [settings](settings.md) — the skill trust setting and the rest of the app's configuration.
