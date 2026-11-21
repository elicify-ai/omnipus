# Skills

A skill is a written playbook an agent picks up when a task calls for it. This page explains what a skill is, how to add one, and how to decide which agents may use it.

## What it is

A skill is a folder. The folder holds a file called `SKILL.md` and, if the skill needs them, other files: scripts, templates, reference notes. `SKILL.md` has a short description and the instructions, written in Markdown. Installing a skill changes nothing on its own. When Omnipus builds a session's skill menu, it lists the skills granted to that agent by name and description. The full instructions are read only when the agent decides the task matches, and they apply to that task only.

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
| Your skills folder | Skills you install or write, grantable to any agent | Lives at `~/.omnipus/skills` in your data folder, one folder per skill |
| Built-in skills | Playbooks that come with Omnipus, including the document skill `elicify-office` | Installed into your skills folder at every start. They are read-only |
| A mounted folder | Skills that belong to a repository and travel with it | Put them in `.omnipus/skills` or `.claude/skills` at the root, then [mount the folder](library.md) |
| The ClawHub registry | Community-published skills | Search and install from the Skills & Tools screen |

Omnipus ships 29 built-in skills covering setup, planning, research, and document workflows.

## Omnipus's setup workflow skills

On a fresh install, Omnipus receives these setup workflows automatically, and only Omnipus receives them. Each one starts with a single "use when" line, and Omnipus's own instructions route every setup request to exactly one of them:

| Purpose | Skill identifiers |
|---|---|
| Clarification | `interview` |
| Agents | `create-agent`, `change-agent`, `remove-agent` |
| Skills | `write-skill`, `install-skill` |
| Teams and workspaces | `team-setup`, `team-delegation`, `welcome-workspace` |
| Connectors and providers | `add-connector`, `remove-connector`, `add-provider`, `provider-config` |
| Channels | `connect-channel`, `disable-channel` |
| Health, usage and diagnosis | `doctor-usage`, `shell-diagnosis` |
| Planning steps used by the workflows above | `capability-plan`, `dependency-check` |

Mia, Jim and the other built-in agents keep their existing skill assignments. Assigning a skill does not grant tool permissions: Omnipus's tools still follow the same global and per-agent permission rules. If you change or clear an agent's skill grants, a later restart does not restore the initial list.

## What an agent sees when it loads a skill

When an agent loads a skill, it gets the whole skill in one answer: a short header, then the instructions. The header lists:

- the skill's folder on disk and its file list (at most 200 names; more are summarised);
- its version and a revision code that changes whenever any file changes;
- its metadata, such as name, version, author and `allowed-tools`;
- the programs it needs (`metadata.omnipus-requires`), and whether each one is found on the machine (an entry that is not a plain program name is listed as "declared, not checked"; see below);
- who last changed it and when;
- what this agent may do: read every file, run scripts with its normal `bash` permission, and whether it may edit.

Because the agent sees the folder and the file list, it can open the other files itself: a script, a template, a reference page. It reads them with the file tools and runs scripts with `bash`. Loading a skill does not give extra permissions. The agent's normal tool permissions still apply.

To list what a skill needs, add a line to the skill's `metadata` in `SKILL.md`:

```markdown
---
description: Use when the user asks to fill in or extract data from a PDF form.
metadata:
  omnipus-requires: "python3 pdftotext"
---
```

Omnipus looks for each program name on the same search path the agent's `bash` uses. The header then says whether it is present or missing. This is a hint for the agent, not a block: a skill with a missing program still loads.

Only a plain program name, such as `python3`, is checked. Anything else, for example a version requirement like `pypdf>=4` or a quoted phrase like `"pypdf >= 4"`, is not looked up. The header shows it in quotes as "declared, not checked", never as present or missing, so the agent knows the skill asks for it but Omnipus has not confirmed it. The skill's detail view (`get_skill`) lists only the program names that were checked, with present or missing for each; the unchecked entries appear only in the load header.

## Who can read and edit skills

- **Reading.** An agent reads, lists and runs every file of the skills it may load.
- **Editing.** An agent whose `edit_skill` permission is not Deny may also read and list every installed skill with the file tools, so it can look at a skill before changing it. It still cannot load or run a skill it was not granted.
- **Main agents start at Ask.** On a fresh install every Main agent can change skills only after you approve. The subagent tier and the hidden engine agents cannot change skills at all.

### How to allow or deny skill changes

Agents change a skill with the ordinary file tools (`write_file`, `edit_file`, `append_file`) and with `edit_skill`. `edit_skill` does four things only: `copy` a skill, `delete_file` (remove one file), `delete` (remove the whole skill) and `restore` (bring back an older version). There is no `create_skill` or `remove_skill` any more. To create a skill, an agent writes `<new-name>/SKILL.md`. To delete one, it uses `edit_skill` with `action=delete`.

One setting decides all of it: the `edit_skill` permission, in **Settings** under tool permissions (see [tools](tools.md)).

| Setting | What happens |
|---|---|
| Ask (default) | You see the standard approval card each time an agent wants to change a skill. The card shows the tool call, as for any other tool. There is no special skill card. |
| Allow | Changes go through with no card. |
| Deny | The agent cannot change skills. |

When an agent writes into a skill folder with a file tool, Omnipus uses the stricter of two permissions: the file tool's own and `edit_skill`. So `write_file` set to Allow does not let an agent change skills if `edit_skill` is Ask. To let one agent change skills without asking, the global setting must be Allow and the agent's own setting Allow too: an agent's own setting can only make things stricter than the global one.

**Auto-approve still asks for skill changes.** Even with Auto-approve on, any write inside the skills folder shows the card when the permission is Ask. Skills are instructions other agents will follow, so you see every change.

Every change is checked before it is saved, stored as a whole-folder version, and written to the audit log. A change can be refused, for example because the skill folder is too big or because it contains a stored secret; see [security](security.md#why-a-skill-save-can-be-refused).

## Built-in skills are read-only

Built-in skills belong to Omnipus. Nobody can edit or delete them, you and agents alike. A try to change one is refused with a message that says to copy it first.

To change a built-in skill:

1. Open **Skills & Tools**, then Installed Skills.
2. Click **Make a copy** on the skill. Built-in rows show "Built-in · read-only".
3. Type a name for your copy. It starts as `<name>-copy`. The dialog checks the name as you type.
4. Click **Create copy**, then grant the copy to the agents that should use it.

An agent can do the same with `edit_skill` and `action=copy`. A copy is yours. It does not update when Omnipus updates, and no agent has it until you grant it. User-owned skills have a **Make a copy** button too.

### Built-ins are put back at every start

At every start, Omnipus installs any missing built-in skill and replaces every built-in folder that differs from the shipped version. **If you edited a built-in folder by hand, your edit is overwritten at the next start.** Copy the skill first if you want to keep changes.

Omnipus also removes built-in skills it has retired, together with their names in your agents' skill lists. Only folders Omnipus itself placed are removed; a folder you made yourself with the same name is left alone.

## Skill names ignore letter case

`Plan` and `plan` are the same skill name. You cannot create, copy or install a skill whose name matches another skill in any letter case. Built-in names are reserved in any case, so `Plan` is refused just like `plan`. Names may still use capitals, so skills from the registry keep working.

## Versions, undo and restore

Every change to a skill you own is saved as a version of the whole folder. The history is kept under Omnipus's own data, outside the skill folder, where agents cannot reach it.

- **Undo.** Each change an agent makes reports the version it can go back to. An agent restores an old version with `edit_skill` and `action=restore`; it can read the list of versions with `get_skill`, asking for the `versions` field. A deleted skill can be brought back the same way.
- **How long.** The retention setting you already use for sessions (**Settings**, Data tab, 90 days by default) now covers skill versions too. The clock starts when a version stopped being the live one, not when it was created. A skill you delete stays restorable for the full window counted from the deletion, however old it was.
- **Outside changes.** If a program or your own editor changes a skill folder, Omnipus notices at the next load or change. It records the new state as a version marked "changed outside Omnipus".
- **Not every version can be restored.** A version taken from a broken state (no valid `SKILL.md`), or one that holds a stored secret value, is kept but refused on restore. The refusal says why.
- **Built-ins** have no history. They are put back at every start.

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
5. To use a file you already have, choose **Install from file** in the same window. A file install cannot be verified against a published checksum, so Omnipus shows what the skill claims to do and asks you to confirm. The file is a `SKILL.md` or a `.zip`, named after the skill (for example `my-skill.zip`). In a zip, `SKILL.md` must sit at the top level: a zip whose files are all inside one folder is refused with that reason. Files under `scripts/` that are executable in the zip stay executable.
6. Grant the skill. Open the agent, new or existing, and tick the skill in its skills list. An agent with nothing ticked has no skills.
7. Send the agent a task that matches the skill's description. You can also type `/` in chat and pick the skill to activate it for that one message.

You can also skip the screen: ask an agent in chat to find and install a skill, or to write one for you. Installing asks for your approval, even under Auto-approve. Writing or changing skill files follows the permission above.

Installing over a skill you already have replaces it. If you changed that skill since you installed it, Omnipus warns you that the install would replace your changes and asks you to confirm. Your changes stay in the version history. You cannot install a skill whose name is a built-in name.

Each card under Installed Skills shows when the skill was last used. "Never used" on a skill you granted is the sign its description is not doing its job.

## What a skill folder contains

A skill you own lives in `~/.omnipus/skills/<name>/`. `SKILL.md` is required. Everything else is optional, for example `scripts/`, `templates/` or `reference/`. These are the parts of `SKILL.md` the app reads.

| Part | Required | What it is for |
|---|---|---|
| Folder name | Yes | The skill's identifier: what you grant and what follows `/` in chat. Letters, digits, and single hyphens, up to 64 characters. Case does not matter. |
| `description` | Yes | One line saying when to use the skill. At most 1024 characters. |
| Body | Yes | The instructions, in Markdown. Read only when the agent loads the skill. |
| `name` | Yes | Must match the folder name. When Omnipus copies a skill it sets this for you. |
| `author`, `version`, `license` | No | Shown on the skill's card in Installed Skills. |
| `compatibility` | No | Free text for the agent, up to 500 characters. |
| `metadata` | No | Extra text values. `omnipus-requires` lists needed programs (see above). |
| `argument-hint` | No | A hint shown next to the skill in the chat `/` menu, such as `<query>`. |

Limits: `SKILL.md` up to 256 KB, any one file up to 10 MB, the whole folder up to 50 MB, at most 1,000 files. A change that goes over a limit is refused and nothing is saved. Paths inside a skill cannot contain a name that starts with a dot or a colon.

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

## Skills from a mounted folder

A skill that lives in a folder mounted into a workspace (`.omnipus/skills` or `.claude/skills`) is a project skill. Agents in that workspace can load it. Omnipus does not change project skills: `edit_skill` and the file-tool rules above apply to your own skills folder only. Edit a project skill in the repository itself. A project skill cannot replace a skill you granted under the same name; your grant wins.

`list_skills` with `scope=management` and a `workspace_id` shows a workspace's project skills; only Omnipus may name a workspace other than the current one. `list_skills` is now a plain list and no longer takes a skill name. To read one skill, an agent uses `get_skill` with its `id` and a `fields` list that names what it wants (`instructions`, `files`, `versions` and so on, or `all`).

A skill installed from a GitHub link takes its folder name from the link. That name must be a valid skill name: letters, digits and single hyphens, at most 64 characters, not a reserved Windows device name such as `CON`. Otherwise the install is refused before anything is written. A link that hides a slash or backslash in percent-encoding (`%2F`, `%5C`, or a double-encoded form) is refused outright, before anything is downloaded.

## Limits and things to watch

- Installing is not granting. A new skill sits unused until you grant it to an agent, and a new agent starts with no skills at all.
- If you delete a skill and later create a new one with the same name, the new skill starts with no grants. An agent that held the old skill does not get the new one. Grant it again. The agent's Skills tab marks such a skill **stale grant**; untick and tick it, then save, to grant the current skill.
- A skill that never fires looks identical to a skill nobody needed. The "Last used" row on each card is the only signal.
- Registry installs are checked against a published checksum; file installs cannot be. The setting lives in Settings, under Security: **Block unverified** refuses installs with no checksum to check, **Warn unverified** allows them with a warning (the default), and **Allow all** turns checking off.
- A `bash` command may read or run files of a skill the agent can load (for example `cat`, `sh <skill>/scripts/x.sh`, or `cd` into the folder followed only by `./` script runs and read-only commands such as `cat` or `ls` that stay inside it: an operand with a `..` segment is refused). A command that could change a skill file (an output redirect, `rm`, `cp`, `mv`, `chmod` and similar, also after a `cd` into the folder) is refused with a message that points to the file tools. A path with a wildcard (`*`, `?`, `[`) never counts as a skill path, and `rg --pre` is not treated as a read. Skills the agent cannot load, and Omnipus's own skill bookkeeping folders, stay closed to `bash`.
- Skill scripts run under the agent's normal `bash` permission. With the sandbox on (macOS and Linux), a script can read and run skill folders but cannot change them. Where there is no sandbox, only Omnipus's own checks protect them. On Linux kernels older than 6.2 a script can still empty an existing skill file, because Landlock cannot block truncation there. Landlock does not restrict `chmod` either, so on Linux a script you approved can change a skill file's permissions (mode) even with the sandbox on. See [security](security.md#skill-folders-are-protected-by-mode).
- Each skill in a session's menu adds its name and description to later requests. Omnipus captures the menu when it first builds a request for the session and reuses it. Changes made during a session may not show in that session's menu; start a new conversation to be sure. The menu is not an access grant: current permissions still govern loading a skill and running its tools.

## Related pages

- [tools](tools.md) — what tools are, and how allow, ask, and deny permissions govern them.
- [agents](agents.md) — creating agents and choosing each one's skills.
- [library](library.md) — mounting a folder into a workspace, which is how a repository's own skills arrive.
- [memory](memory.md) — what your agents remember across conversations — different from a procedure you teach once.
- [settings](settings.md) — the skill trust setting and the rest of the app's configuration.
