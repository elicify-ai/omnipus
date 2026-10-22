# Tools

Tools let an agent read and write files, search the web, run commands, and send email. This page covers where tools come from and how you control access.

## What it is

A tool is one capability an agent can call while it works. When an agent uses a tool, the call appears in the chat as a collapsible entry, so you can see what it did.

Tools come from two places:

- **Built-in tools** ship with Omnipus — a large catalog; the **Built-in Tools** tab under Skills & Tools lists the current set.
- **Tools from connected servers.** Omnipus speaks MCP (Model Context Protocol), an open standard many services publish tools to. Connect a server and its tools appear next to the built-in ones.

Every tool, built-in or from a server, has a policy with three values: **Allow**, **Ask** or **Deny**. You set it in two places — once for the whole installation, once per agent.

The built-in groups you will meet in practice are below. For every current name and description, use the generated [built-in tool catalog](reference/built-in-tools.md). The **Built-in Tools** tab is the live list for your installation.

| Group | Tools you will notice | What they do |
|---|---|---|
| Files | `read_file`, `write_file`, `edit_file` | Read and change workspace files |
| Shell | `bash` | Run shell commands |
| Web & Search | `search_web`, `fetch_url` | Search the web and open a page |
| Browser | `browser_navigate`, `browser_screenshot` | Drive a live browser, capture what it shows |
| Communication | `send_email`, `read_inbox`, `send_message` | Send email and chat messages |
| Delegation | `delegate` | Hand work to another agent |

**The `delegate` tool always names its target.** A call with `action="run"` must give an `agent_id` (the other actions address an existing worker by `session_id` and do not need one); one that omits it, passes null, or passes a blank value is refused before any work starts — there is no default target and no fallback to the asking agent. Naming the caller itself is allowed and forks a new session running that same agent; it needs a self-line in the workspace, exactly like any other handoff (see [workspaces](workspaces.md#how-to-set-who-may-delegate-to-whom)). The check cannot be switched off: a handoff that does not come from an identified agent is refused, and no configuration setting changes that.

When an agent calls `list_jobs`, completed and failed jobs are hidden by default, not erased. If any are hidden, `notes.terminal_suppressed` gives counts by job type and a `total`. The counts follow kind, label, and draft filtering but precede status filtering and the result limit: even a call for `status="running"` can count hidden completed jobs. If `notes.scan_truncated` or `notes.errors` appears, the count may be incomplete. Set `include_terminal=true` to make terminal rows eligible for results, or filter for `status="failed"` or `status="completed"`, which includes them automatically.

**Where reading tools can look.** `read_file`, `list_directory` and `grep` follow one rule: they reach any file your account can read, except Omnipus's protected files (such as the master key, saved credentials and `config.json`) and other agents' and other workspaces' files. `grep` given no folder searches the workspace and its mounted folders. Given a folder, it searches there instead — a workspace-relative path, a mounted folder's name, or an absolute path such as `/var/log`; a path containing `..` works too. It skips the Linux system folders `/proc`, `/sys` and `/dev`. Writing tools stay inside the workspace and mounted folders. See [security](security.md#what-reading-searching-and-listing-can-reach) for the details.

An agent sees common tools directly and searches the rest when a task calls for one. If it does not reach for a tool, name it in your request.

## When you would use it

- An agent is using a tool you do not want it to use — deny the tool.
- A tool is risky enough that you want to confirm each use — set it to Ask.
- You connected a new server and need to decide who may use its tools.
- You scheduled recurring work and nobody will be present to answer approval prompts.

## How to change an agent's tool access

1. Open the workspace's **Team** panel, find the agent's card and choose **Edit** from its **⋯** menu (for Judge, Plan Supervisor and Omnipus: **Settings → System agents → Edit**). The agent's editor opens.
2. Open its **Tools** tab, with the **Tools & Permissions** panel.
3. Optional: start from a preset — **Cautious**, **Balanced** or **Full access**. A preset sets the agent's requested policy values. The effective policy still combines them with the global ceiling: a preset cannot override a global Ask or Deny. Auto-approve and tool-specific checks can still affect whether an allowed call asks or is refused.
4. Expand a group and click **Allow**, **Ask** or **Deny** on the tool you care about. Changes save on their own.

You can also ask Omnipus in its chat to change an agent's permissions. Outside God Mode, it is instructed to show one plan summary and ask **Go / Change / Cancel** before its first change. Go does not replace any tool approval required by the effective policy; `set_agent_permissions` is on the Auto-approve ask-list. In God Mode, Omnipus shows its summary as a message and skips approval questions and global tool-policy prompts. Stricter per-agent policies remain in force.

**Omnipus itself.** Omnipus takes no per-agent **Ask**: setting one of its tools to Ask is refused with "Omnipus takes no per-agent Ask; set Ask globally or use Deny". **Deny** works for it like for any agent. Which of its tools always ask, and how God Mode changes that, are described in [security](security.md).

The installation-wide setting lives in **Settings → Security**, under **Advanced / technical details**, in **Tool Access — Global Policies**. What you set there applies to every agent at once. Saving a change there asks you to re-type your password.

Saving a global policy and applying it to running agents are separate steps. If
Omnipus reports that the settings were saved but could not be applied to running
agents, the saved change has **not** been rolled back. Running agents may still
use the previous policies. Retry applying the change, or restart Omnipus before
relying on the new permissions.

## Replacing tool policies through the API

When creating an agent or replacing its built-in tool policy map through the API,
include every implemented built-in tool returned by `GET /api/v1/tools`, using
its exact name and an `allow`, `ask` or `deny` value. A missing implemented tool,
an unknown name or a wildcard is refused; connected-server tools keep their
separate `mcp_` policy rules.

A tool name reserved for a later implementation does not have to appear in that
map before the tool is available. If an existing agent already has a saved policy
for such a name, omitting it from a replacement leaves that saved value unchanged.
The replacement does not invent a policy for a reserved name that was never saved.
Supplying the reserved name explicitly follows the normal replacement rules;
on the dedicated tool-settings endpoint, `override_names` controls which supplied
values are saved as per-agent overrides.

For the global-policy endpoint, `PUT /api/v1/security/tool-policies`, check the
save result before assuming the new permissions are active:

| Response | Meaning and recovery |
|---|---|
| HTTP 200 | The policies were saved and the running-agent reload completed successfully. |
| HTTP 503 with an `error` saying the settings were saved but could not be applied | The save remains in place, but the reload failed or could not be confirmed. Retry the same update, or restart Omnipus. Do not assume running agents are using the saved policies yet. |

## Allow, ask and deny

What each setting does, and what you see when it fires:

| Setting | What the agent can do | What you see |
|---|---|---|
| Allow | Runs the tool without checking | The call runs and shows in the chat |
| Ask | Stops and waits for your approval, unless Auto-approve lets the call through | An approval card with Approve Once, Deny and Always Allow |
| Deny | Cannot run the tool | The tool disappears from the agent's view |

On the approval card, **Approve Once** runs the call and remembers nothing. **Always Allow** remembers that exact call for the rest of the chat, so an identical one does not ask again. For a shell command, Always Allow can instead remember a whole family of similar commands. See [security](security.md) for the exact wording and when that choice is offered.

**Auto-approve** sits on top of Ask. It never changes a tool set to Allow or Deny. When it is on, a tool set to Ask skips the card for calls Omnipus can judge safe and still shows it for the rest — this now works with or without a kernel sandbox (changed 2026-09-24). Without a kernel sandbox, shell commands ask first, except read-only ones and commands an operator rule allows; see [security](security.md#auto-approve-and-the-kernel-sandbox-changed-2026-09-24) for what that means in practice. It is on by default, and you can turn it off globally, for one agent, or for one chat.

Under Auto-approve, most tools run without a card. Reading, listing and searching files (`read_file`, `list_directory`, `grep`) run anywhere outside Omnipus's protected files, so `read_file` of `/etc/hosts` runs. Writing keeps the workspace rule: `write_file`, `edit_file` and `append_file` run only when every path is inside the workspace or a mounted folder, so `write_file` to `notes/plan.md` runs while `write_file` to your Desktop asks. The registered tools on the always-ask list keep asking when their effective policy is Ask, even with Auto-approve on, including `set_agent_permissions`, `delete_task`, `send_email`, `set_config`, `browser_evaluate` and `install_skill`. God Mode removes global Ask; a per-agent Ask or Deny still applies. `send_message` and `send_file` are **not** on that list: messages, and files from inside the workspace, go out to chat channels with no card. The shell has its own checks: it asks for writes outside the workspace and for network access. See [security](security.md) for the full list and the shell details.

The **Under Auto** column in the [built-in tool catalog](reference/built-in-tools.md) shows, for every built-in tool, **Runs**, **Runs if inside workspace** or **Asks**. The same answer appears as a small marker — **Auto: runs**, **Auto: runs inside workspace** or **Auto: asks** — next to each tool set to Ask in the **Tools & Permissions** panel and in **Tool Access — Global Policies**. `bash` has no marker, because it has its own checks.

The two layers — global and per agent — combine by one rule: **the stricter of the two wins** (Deny beats Ask, Ask beats Allow). A setting on one agent can only tighten the global setting, never loosen it.

```mermaid
flowchart LR
  G[Global setting] -->|bounds| W[Stricter of the two wins]
  A[Agent setting] -->|tightens or matches| W
  W -->|decides| T[What the agent may do with the tool]
```

An agent's setting can make a tool stricter than the global one, never looser.

Concrete combinations:

| Global setting | Agent setting | What happens |
|---|---|---|
| Allow | Ask | The agent asks first |
| Allow | Deny | The tool is blocked for that agent |
| Ask | Allow | Still asks — an agent setting cannot loosen this |
| Deny | Allow | Blocked — a global Deny cannot be overridden |

A tool blocked globally is greyed out in each agent's tool list, so you can see where the ceiling sits while you work.

**God-mode** (Settings → Gateway → Danger zone) overrides every tool's global policy to Allow at once, for every agent — see [security](security.md#god-mode) for what that does and does not change.

## Tools from connected servers

A connected server adds tools Omnipus was not born with — a company wiki, a design app, a database. One server can add several tools.

To connect one:

1. Open **Skills & Tools** in the sidebar.
2. Go to the **MCP Servers** tab and click **Add Server**.
3. Choose **Local program** (a command that runs on your machine) or **Network address** (an https address, for hosted services). A local program asks you to confirm you trust it.
4. Save. The server appears with its connection status, and its tools join the catalog.

Server tools carry the server's name, so you can tell where a tool came from. In both policy editors they sit in their own group under the server's name. One Allow, Ask or Deny control covers every tool from that server. The stricter-wins rule applies to them like any other tool.

From a script, `GET /api/v1/mcp-servers` answers to the gateway's command-line token (sent as `Authorization: Bearer <token>`) as well as to a signed-in session; without either it answers 401.

### Loaded tools stay with the conversation

When an agent loads a tool on demand, its definition stays advertised for that
session even after the discovery lifetime expires. Another session of the same
agent does not inherit that load. This keeps the request's tool list stable; it
does not send every allowed tool from the first turn.

Retention is not permission. A current Deny, removed server/tool assignment,
unregistered tool or disconnected server still removes access, including to a
tool loaded earlier. An unrelated configuration reload does not expire a valid
session load. A request that must offer only goal-setting tools can deliberately
narrow the list; the next ordinary request uses the session's loaded list again.

Under Auto-approve, a server tool set to Ask runs without a card only when its server labels it read-only or explicitly not destructive. A tool with no label asks. Most servers send no labels today, so most server tools still ask. Each server tool set to Ask shows its own **Auto: runs** or **Auto: asks** marker.

## What a change tool reports

When Omnipus (or any agent allowed to) changes something through a tool — an agent, a workspace, a skill, a provider, a connected server or a channel — the result says plainly how far the change got. It is one of four outcomes:

| Outcome | What it means |
|---|---|
| `working` | Saved, and the tool's own check passed |
| `saved but not working` | Saved, but the check failed or could not run; `reason` says why |
| `partly saved` | Some parts are saved and others are not; `missing_parts` lists what is missing and `reason` says why |
| `failed: nothing saved` | Nothing changed — used only when the tool knows no part was written |

Next to the outcome, `check_scope` says what the check actually proved and `not_tested` lists what it did not prove. For example, after a provider is configured, `working` means the saved endpoint answered a request for its model list with the saved key; `not_tested` then names model execution, quota and tool support. A channel change is saved at once but connects or disconnects only on the next configuration reload, so it reports `saved but not working` with that reason. `environment_setup` gives its outcome when the installation finishes: an exit code of 0 is `working` — the command succeeded, but whether the installed program works inside the agent's sandbox is listed under `not_tested` — and any other ending is `partly saved`, because the installer may already have changed files. Omnipus only reports a part as done when the tool's outcome for that part says so.

### When a shared setting changed in the meantime

Providers, connected servers and channels are shared by the whole installation, and both you (in Settings) and an agent can change them. The tools that list them return a `revision` for each entry (for channels, each connection — such as `telegram.eu` — has its own) — a short code that changes whenever the entry's settings change (it never contains a key or password). A missing entry has the revision `absent`. The tools that change these entries — `configure_provider`, `add_mcp_server`, `remove_mcp_server`, `enable_channel`, `configure_channel` and `disable_channel` — must send the revision they planned against (`expected_revision`). If the entry changed since, for example because you saved it in Settings while Omnipus was preparing its summary, the change is refused with `CONFLICT`, nothing is saved, and Omnipus re-reads the entry and asks you again. A tool change and a Settings save of these entries never run at the same time: they take turns.

## Limits and things to watch

- **Built-in agents keep their identity, but their tool access is editable.** The ordinary built-in agents keep their identity and base instructions, but their supported tool policies, connector assignments and skills are editable. Judge and Plan Supervisor retain fixed capability surfaces. Open a built-in agent's Tools tab to tighten its access; creating a custom agent is not required for that change — see [agents](agents.md).
- **Policies do not reach external runners.** An agent on an external command-line tool manages its own tool access; Omnipus settings have no effect.
- **Task and plan runs never ask for approval (changed 2026-10-13).** This covers every task and plan run whatever started it — **Start Task**, **Create & Run now**, the Calendar's **Run now**, an agent's `run_task` or `execute_plan`, a Calendar trigger, the queue, an auto-advance, a plan's member tasks — and every helper they start, plus recurring schedules and heartbeats. Auto-approve, an operator `command_rules` rule or an "Always Allow" grant still run a call there exactly as in a chat. A call that would need a person, such as `delete_task` or a file write outside the workspace, is refused straight away and the task's conversation shows "Not run: needs approval, and task and plan runs never ask." — never an approval card, even if you are watching or write into the task's conversation. An unattended call must either clear Auto-approve, match a rule or grant, or have an effective Allow policy. Setting one agent to Allow cannot override a global Ask or Deny. If a required tool still resolves to Ask, review both policy layers. Changing the global ceiling affects every agent; use per-agent restrictions to retain tighter access elsewhere. See [security](security.md#task-plan-scheduled-and-other-unattended-runs).
- **The shell can touch files directly.** Denying `write_file` does not stop an agent with `bash` from changing files with a command. To block file access, deny `bash`.
- **Denying `read_file` alone does not stop reading.** `grep` returns the matching lines of any file it can reach, whatever `read_file` is set to. To keep a custom agent from reading files, deny `read_file`, `grep` and `bash` for it.
- **Auto-approve no longer needs a kernel sandbox (changed 2026-09-24).** On Windows, or with the process sandbox not set to Enforce, Auto-approve still runs — the composer **Auto** button stays pressed, and its tooltip adds the no-sandbox caution. There is no separate badge. Every tool except the shell behaves exactly the same either way. For the shell, without a kernel sandbox Omnipus has no operating-system check behind its own text-based one, so *(changed again 2026-09-24)* shell commands ask first, except a read-only command (listing files, printing a file's contents, `git status`/`log`/`diff`/`show`, and similar) or one an operator's `command_rules` allow rule explicitly covers — even one that only touches files inside the workspace. A deliberately disguised command can still slip past the read-only check either way. See [security](security.md#auto-approve-and-the-kernel-sandbox-changed-2026-09-24) for the concrete risk and how to avoid it.
- **Email tools need a mailbox.** They exist only for an agent that owns an enabled mailbox, configured on the [Connectors](connectors.md) screen — one mailbox per agent and workspace.
- **Web search works without setup**, on a default provider that needs no key. To use another provider, add its key under **Settings → Integrations**.

## Related pages

- [agents](agents.md) — the built-in roster and how to create your own agents.
- [skills](skills.md) — the other half of the Skills & Tools screen: reusable playbooks agents pick up.
- [connectors](connectors.md) — where email mailboxes and chat connectors are configured.
- [settings](settings.md) — the Settings screen, including the global tool policies under Security.
- [security](security.md) — the sandbox that contains what tools are allowed to do.
- [browser](browser.md) — the browser tools and the live browser panel.
- [built-in tool catalog](reference/built-in-tools.md) — the generated list of every built-in tool in this release.
