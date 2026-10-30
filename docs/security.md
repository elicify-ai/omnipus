# Security for users

Omnipus can run tools, shell commands, and programs on your machine. You control that access through permissions, the sandbox, encrypted credentials, and an audit log.

## What it is

Security in Omnipus uses several protections together. Tool policies decide whether an agent can call a tool. The sandbox limits what started programs can reach. The credential vault encrypts saved secrets. The audit log records security events, policy decisions, and tool executions.

Secret filtering adds another layer. It replaces registered credential values before selected content reaches a model. This filtering is best-effort and can be switched off. It does not make pasted or unregistered secrets safe.

## When you would use it

Review security settings before agents handle private files or untrusted content. Set stricter tool policies for actions that could change data, spend money, or contact another service.

Use the credential vault when you add an application programming interface key, connector token, or another secret. Review the audit log when you need to understand an allowed, denied, or failed action.

## How to control agent access

Two separate things decide whether an agent's tool call runs:

1. **The tool's policy: Allow, Ask or Deny.** This is set per tool. Allow runs without asking, Ask stops and waits for you, and Deny means the agent cannot use the tool at all.
2. **Auto-approve.** This is an on/off switch on top of Ask. It never changes a tool set to Allow or Deny. For a tool set to Ask, it lets Omnipus skip the prompt for calls it can judge safe, and still ask for the rest.

For example: `write_file` is set to Ask. With Auto-approve off, every file write shows an approval card. With Auto-approve on, a write to a file inside the workspace runs without a card, and a write to your Desktop still asks.

### Set tool policies

1. Open **Settings**, then select **Security**. You see the security health summary and the main protection settings.
2. Open **Advanced / technical details**, then **Tool Access — Global Policies**. Choose Allow, Ask or Deny for each tool. Changes save immediately using your signed-in session, with no second password prompt. Changing a tool to **Allow** can enable it for every agent that follows the global setting; stricter per-agent **Ask** or **Deny** settings still apply. This exception does not remove the existing confirmation checks on Integrations, Providers, Sandbox, Credentials or Performance saves.
3. To tighten one agent, open that agent's **Tools & Permissions** panel. An agent's setting can only be stricter than the global one, never looser. See [tools](tools.md) for how the two combine.

A global policy change must also be applied to running agents. If the save result
says the settings were saved but could not be applied, the saved change has not
been undone, but running agents may still use the previous permissions. Retry
applying the change, or restart Omnipus before relying on it. The global-policy
API returns HTTP 503 for this result, rather than reporting a successful update.

The global policy auto-save indicator shows the server's recovery message for
this result, not just a generic server-unavailable message. It remains an error,
not a successful-save confirmation.

On a new installation the shell tool, `bash`, is set to **Ask**.

### Turn Auto-approve on or off

Auto-approve can be set in two places. On a new installation it is **on** globally.

| Where | What it does | Example |
|---|---|---|
| **Settings → Security → Auto-approve** | The default for every agent and chat. Changing it asks you to confirm and re-type your password. | You turn it off. Every tool set to Ask now prompts every time, in every chat. |
| The **Auto** switch next to the message box in a chat | Turns Auto-approve on or off for that chat. You can flip it before typing anything — a brand-new chat has no conversation yet, so the switch records your choice and applies it from your very first message, before any tool call in that first turn runs. In a chat that is already running, a flip applies from the agent's very next tool call, not just your next typed message — even mid-turn, while the agent is still working. A prompt that is already open when you flip the switch is not resolved for you; it has already asked, and stays open until you answer it. | Auto-approve is off globally, but you are watching this chat closely. You switch it on before typing your first message, and that first message already runs under it. In a chat that is already going, you switch it on mid-turn and the agent's next tool call already runs under it. |

On the Security screen, the switch carries this description:

> Tools set to “Ask” run without a prompt when it's safe: they stay inside your workspace.

Below the switch, **Always asks when set to Ask** is a collapsed list — click it to expand. It groups the fixed list of tools that keep asking whenever they're set to Ask, even with Auto-approve on:

> Sending email · Deleting tasks, agents or workspaces · Installing skills, setting up an environment or publishing a web preview · Changing or testing settings, providers, channels, agents or skills · Running diagnostics · Adding or removing connected (MCP) servers, and MCP tools not marked safe · Browser scripts and uploads · Mounting a folder, or files outside your workspace

The note underneath says:

> Without a kernel sandbox (for example on Windows), shell commands ask first, except read-only ones and commands an operator rule allows.
<!-- verify-ui-string -->

That is a summary. The full list of tools that still ask, one row per tool, is under "What Auto-approve runs and what still asks" below.

The chat switch is the one place that can turn Auto-approve **on** when the global default has it off, because you are present in that conversation. When the chat's agent hands work to another agent (a delegate), the delegate follows the chat's switch exactly as the agent you are talking to does: if Auto-approve is on for the chat, the delegate's Ask tools run under it too.

**Exception: external command-line workers.** A worker that runs on Claude Code, Codex or OpenCode does not use Omnipus's tool approval dialog at all. Omnipus automatically approves the permission requests the external tool reports, so turning Auto-approve off is not a way to make an external worker ask you before each action. Omnipus Allow/Ask/Deny rules govern Omnipus tools only and do not reach inside the external tool. Treat delegating to an external command-line worker as its own trust decision.

### Auto-approve and the kernel sandbox

**Auto-approve does not need a kernel sandbox to work.** A kernel sandbox is protection enforced by the operating system itself: Landlock on Linux, Seatbelt on macOS, with the Process Sandbox set to **Enforce**. Auto-approve also works on Windows, which has no Omnipus kernel sandbox, and when the sandbox is unavailable or set to Permissive or Off. Without an enforcing kernel sandbox, shell commands face the stricter approval checks below.

**What Auto-approve does, with and without a kernel sandbox:**

- **Every tool except the shell (`bash`)** is unaffected by whether a kernel sandbox is active. An application-level check inside Omnipus keeps `write_file`, `edit_file` and the other writing tools inside your workspace (see "What Auto-approve runs and what still asks" below), and that check runs the same way with or without a kernel sandbox.
- **The shell (`bash`) is different.** With a kernel sandbox active (Linux or macOS, Process Sandbox set to Enforce), a shell command that tries to reach outside the workspace or the network is caught two ways: Omnipus checks the command's text before running it, **and** the operating system itself blocks anything that check missed. Without a kernel sandbox — on Windows, or with the Process Sandbox set to Permissive or Off — only the first, text-based check exists, so Omnipus is stricter about what it will run without asking: a command runs without a prompt only if it is one of a short list of commands that can only read (listing files, printing a file's contents, `git status`/`log`/`diff`/`show`, and a few more — see the list under "What Auto-approve runs and what still asks" below), or an operator has explicitly written a rule allowing it. Every other shell command asks first, even one that only touches files already inside your workspace — the approval card explains why: *"No kernel sandbox is enforcing, so shell commands that could change files or reach the network ask first."*
<!-- verify-ui-string -->
  - **Concrete risk this still leaves open:** a command that has been deliberately disguised so its text does not look like what it actually does — for example, splitting a program name across quotes, or using shell substitution to build the real command at run time — can slip past a text-based check, INCLUDING the read-only allowlist above (which only recognizes a command by its literal, un-disguised name). With a kernel sandbox, the operating system still blocks it regardless of how the text was disguised. Without one, asking first for every non-read-only command is the remaining protection — there is no second, operating-system-level check behind it, and an unattended/scheduled run has nobody to answer, so it refuses rather than guessing.

This is a deliberate, founder-approved trade: Auto-approve being usable everywhere, in exchange for asking more often on a machine with no kernel sandbox. If that trade does not suit a given agent or workspace, keep the Process Sandbox set to **Enforce** where your platform supports it, or turn Auto-approve off for that agent.

### What the composer shows

There is no mode badge in the chat header. The composer has one labelled **Auto** button. The word stays visible. Off is plain text. On is the same word, pressed.

| What you see | Meaning |
|---|---|
| **Auto** pressed and greyed out, with the God Mode tooltip | God Mode is on. The button cannot be changed. Every tool runs without asking, except a tool that an agent itself sets to Ask, which still asks, and a tool that an agent has denied, which stays denied. |
| **Auto** not pressed | Auto-approve is off for this chat. Every tool set to Ask prompts. |
| **Auto** pressed | Auto-approve is on. Safe calls run; the rest ask. |
| **Auto** pressed, tooltip adds the no-sandbox caution | Auto-approve is on, but there is no active kernel sandbox. Every other tool's safe calls still run as usual; the shell only auto-runs a read-only command or one an operator's rule explicitly allows — everything else asks first (see above). The caution is in that same tooltip, not a second label. |
<!-- verify-ui-string: composer Auto button tooltip, including the no-sandbox caution -->

### God Mode

God Mode is Omnipus's strongest override. It is one switch for the whole gateway, not a per-agent or per-chat setting — turning it on applies to every agent and every conversation at once. Change it under **Settings**, **Gateway**, **Danger zone**.

**In practice:** God Mode removes global tool-policy prompts, ordinary per-command resource hardening and Omnipus's shell network filtering. It does not remove stricter per-agent policies, operator deny rules, protected-path application checks or the configured shell workspace-path guard. Your account's operating-system permissions and external firewalls still apply. The programs those shell commands start — and the runs of `environment_setup` — receive your full gateway environment, including saved provider API keys and mailbox passwords. Use it only when you trust the agent with that wider access.

When an enforcing kernel sandbox is active, God Mode shell commands and `environment_setup` runs retain a **protected-file floor**: a minimal filesystem profile that denies internal files while widening the rest of their kernel filesystem and network access. No floor profile is installed when the Process Sandbox is **Off**, **Permissive** or degraded/unavailable. In those states the file tools and command-text checks still refuse protected paths, but a shell command that builds a path dynamically can evade the text check without a kernel boundary behind it.

#### Two switches, not one

| Switch | Question it answers | How it is set |
|---|---|---|
| **Available** | Can this installation use God Mode at all? | Starting the gateway with the `--allow-god-mode` flag, or your first-ever enable through this screen |
| **On** | Is it active right now? | The Danger-zone toggle, once available |

The first time you turn God Mode on through this screen, on an installation that was never started with `--allow-god-mode`, Omnipus saves that choice but needs a gateway restart before God Mode actually takes effect. The toggle shows "Authorized but not yet active — restart the gateway to activate god-mode," with a "Cancel authorization" link if you change your mind first, and Omnipus opens a dialog offering to restart right away or later. After that first restart — or on any installation already started with `--allow-god-mode` — switching God Mode on or off again applies immediately, with no restart. **Turning God Mode off never needs a restart**, on any installation.

#### What changes

| Area | Under God Mode |
|---|---|
| Tool policies | Every tool's global (installation-wide) policy is treated as Allow — no prompt from that policy, for any tool. |
| Shell approval | God Mode removes the global shell approval requirement. A per-agent Ask can still require approval, and a per-agent Deny still blocks the tool. Operator deny rules and the surviving outside-workspace write guard also remain in force. |
| Kernel filesystem confinement | Widened for shell and environment-setup children. A protected-file floor remains on Linux and macOS only while an enforcing kernel sandbox is active; Off, Permissive and degraded/unavailable operation install no floor. Application path checks still apply. |
| Kernel network-port controls | Off for the shell's own commands: they can bind or connect to any port, not just an allowed list. |
| Outbound network filtering | The shell's commands are not filtered against the address block list that otherwise stops them reaching your internal network or cloud-metadata services. |
| Programs the shell starts | Commands run by `bash` (foreground or background) and the runs of the `environment_setup` tool receive your full gateway environment — including saved provider API keys and mailbox passwords — instead of the usual child allowlist. The three gateway boot values (master key, key file, web token) are still withheld. Accepted risk; see ["What child programs can see"](#what-child-programs-can-see). |
| Per-command process limits | The extra process-count and memory limits normally placed on a shell command (and the platform equivalents on macOS and Windows) are not applied. |
| After-run link check | Skipped. Normally, after each shell command, Omnipus looks for links the command left inside the workspace that point outside it, tells the agent, and writes the finding to the audit log. Under God Mode that check does not run. |
| Auto-approve | Switched off outright, everywhere — there is nothing left for it to add. |

#### What does not change

| Protection | Still applies under God Mode |
|---|---|
| Protected-path application checks | File tools and the shell command-text check still refuse protected internal paths. Kernel protection for foreground/background shell commands and environment-setup children requires an active enforcing sandbox, as described above. Approval cannot open a protected path; a general tool approval can appear before the path check runs. |
| An agent's own tool policy | A tool an agent has itself set to Ask still asks; one it has denied stays denied. An agent's own setting can only make a tool stricter than the global one, and God Mode does not remove that limit. |
| Operator deny rules | A `deny` command rule still refuses a matching command outright. |
| The shell's configured workspace-path guard | Still checks command text when enabled. A command naming an outside-workspace write can be refused even though God Mode widens kernel access. God Mode does not turn this guard off. |
| Other tools' own protected-path check | Generic file access through tools such as `read_file`, `write_file` and `grep` still refuses protected internal paths, independently of the kernel sandbox. A general tool approval may precede this check but cannot override its refusal. |
| The gateway boot values | The master-key and key-file variables and the web token are never passed to the programs your `bash` commands or `environment_setup` runs start — God Mode or not. |
| The syscall filter (Linux) | Installed once when the gateway starts and shared by every process it spawns afterward — there is no way to switch it off for one command, so it applies to a God-Mode shell command exactly as it does to any other. |
| Audit logging | Every God-Mode call is still recorded. |
| The prompt-injection guard | Unaffected. |
| Rate limiting | Unaffected. |
| Connected-server (MCP) tool assignment | A tool from a server your agent is not assigned to is still hidden, God Mode or not. |
| The block on agents changing sandbox settings | An agent can never grant itself God Mode, or widen it, by editing its own configuration — see "Agents cannot change these rules," below. |

God Mode is one flag for the whole gateway process, so a delegated or steered sub-agent inherits it automatically — there is no separate delegation setting. A scheduled or unattended run behaves the same way a live chat does: a tool call that would still ask, because that agent itself set it to Ask, has nobody present to answer it, so it is refused outright rather than left waiting on an approval card no one will see.

#### Turning it on or off

Changing God Mode always asks you to re-type your password. After that:

- Turning it on shows a warning-styled "God-mode enabled" message — or, on a boot not yet authorized, "God-mode authorized — restart the gateway to activate it."
- Turning it off shows "God-mode disabled."

While God Mode is on, the sidebar shows a small red "God Mode" pill next to the omnipus.ai wordmark — and when the sidebar is hidden, a small red dot sits at the left edge of the screen instead, just below the header bar (clear of the sidebar-open button, and above full-screen panels on phones). Clicking either opens **Settings**, **Gateway**, scrolled to and focused on the God Mode control — switching to the Gateway tab even if Settings is already open. The chat header does not repeat God Mode; the composer **Auto** button stays pressed and disabled while God Mode is on, and its tooltip says so. In every other state — off, still loading, unknown, fetch error, or development-mode bypass — neither indicator shows anything at all: there is no amber "God Mode ?" unknown variant.

The Security screen's health summary also raises a high-severity "God-mode is armed" warning whenever it is switched on, or authorized and waiting for a restart, with a link back to **Settings**, **Security**, **Danger zone**.

In an agent's **Tools & Permissions** panel and in **Tool Access — Global Policies**, each tool set to Ask carries a small marker showing what Auto-approve does with it:

| Marker | Meaning |
|---|---|
| **Auto: runs** | Runs without a prompt. |
| **Auto: runs inside workspace** | Runs without a prompt when its file is inside the workspace or a mounted folder; otherwise asks. |
| **Auto: asks** | Always asks. |

Tools set to Allow or Deny show no marker, because Auto-approve does not affect them. The shell tool, `bash`, shows no marker either, because it has its own checks (below). Tools from connected servers get a marker too, based on how their server labels them.

### What Auto-approve runs and what still asks

Auto-approve only matters for a tool set to **Ask**. Omnipus judges each call on its own, not the tool as a whole.

**Most tools run without a prompt.** That includes reading, listing and searching files (below), web search and opening web pages, the browser (clicking, typing, navigating, screenshots), memory and the knowledge base, tasks, plans and goals, handing work to another agent, and read-only listings of settings, agents and workspaces. For example, with Auto-approve on, Mia editing a knowledge-base note set to Ask does not show an approval card.

**Reading, listing and searching run anywhere outside Omnipus's protected files.** `read_file`, `list_directory` and `grep` run without a prompt inside the workspace, inside a mounted folder, and outside both. For example, `read_file` of `/etc/hosts` runs. What they can never reach is described under "What reading, searching and listing can reach" below.

**Writing and sending run only inside the workspace or a mounted folder.** `write_file`, `edit_file` and `append_file` run without a prompt when the path they touch is inside the agent's workspace or a folder you mounted into it. Anything outside asks. For example, `write_file` to `notes/plan.md` runs; `write_file` to your Desktop asks. The same rule applies to the file `send_file` sends. `browser_screenshot` saves its picture into the workspace under a name Omnipus picks, so it normally runs. Omnipus secret files, such as the master key, are never covered.

For reading, this matches the shell. Under Auto-approve, the shell command `cat /etc/hosts` runs, because the sandbox lets commands read outside the workspace, and `read_file` of `/etc/hosts` runs too. Only writing and sending keep the workspace rule above.

**Messages and files sent to chat channels go out with no prompt.** `send_message` runs under Auto-approve, and so does `send_file` for a file inside the workspace or a mounted folder. A message, or a file from the workspace, can leave the machine to Telegram, Slack or another connected channel without anyone approving it. If that is not acceptable for an agent, turn Auto-approve off, or set those two tools to Deny for that agent. Email is different: `send_email` and `reply` keep asking whenever they're set to Ask, even with Auto-approve on.

**These registered tools keep asking when their effective policy is Ask, even with Auto-approve on.** God Mode is a separate override: it removes global Ask, but a per-agent Ask or Deny still applies.

| Group | Tools |
|---|---|
| Widening file access | `request_mount` |
| Installing and publishing | `install_skill`, `environment_setup`, `serve_web` |
| Email | `send_email`, `reply` |
| Deleting | `delete_task`, `delete_workspace`, `delete_agent` |
| Browser scripts and uploads | `browser_evaluate`, `browser_upload_file` |
| Settings and diagnostics | `set_config`, `run_doctor` |
| Providers | `configure_provider`, `test_provider` |
| Channels | `enable_channel`, `disable_channel`, `configure_channel`, `connect_channel`, `test_channel` |
| Connected servers | `add_mcp_server`, `remove_mcp_server` |
| Agents and workspaces | `create_agent`, `update_agent`, `set_agent_permissions`, `update_workspace` |
| Skills | `create_skill`, `edit_skill`, `remove_skill` |

**Tools from connected servers (MCP) mostly still ask.** A connected server can label each of its tools. Under Auto-approve, a server tool runs only when its server labels it read-only or explicitly not destructive. Every other server tool asks, including one with no label at all. Most servers send no labels today, so in practice most server tools still ask. Omnipus trusts these labels because you chose to connect the server, and adding a server is on the always-ask list.

**The shell tool, `bash`, has its own checks.** Under Auto-approve, a command runs without a prompt when Omnipus's own checks (see "Auto-approve and the kernel sandbox" above) find nothing that needs asking about, and — where a kernel sandbox is active — the sandbox could also contain it. It still asks when the command:

- writes outside the workspace and its mounted folders. For example, `cp report.pdf /Users/you/Desktop/` asks; `cp report.pdf out/` runs;
- may need the network. For example, `curl https://example.com`, `git push` or `npm install` ask. Omnipus recognises this from the program (such as `git`, `curl`, `wget`, `ssh`, `npm`, `pip`, `docker`, `gh` and the main cloud command-line tools) or from a web address in the command. Because it goes by the program, even `git status` asks the first time in a chat;
- matches an `ask` command rule you wrote (see "Shell command rules" below).

Under Auto-approve, a shell pre-flight that identifies a protected internal path, such as the master key, refuses it rather than offering a path grant. Other call paths can ask for general tool approval before checking the path; that approval still cannot open it. For the ordinary workspace-access prompts described above, **Approve Once** lets that one command through. **Always Allow** on a file prompt opens that one path, for the access the command needed, for the rest of that chat. Denying either prompt refuses the command; it never runs with less access instead.

**A network prompt names the address(es) it needs, and approving covers exactly those.** When Omnipus can read the address from the command (for example the `https://example.com` in a `curl` command, or the `example.com` in `scp file.txt user@example.com:`), the card says *"Allows network access to: example.com"* and lists it. **Approve Once** lets the command reach exactly that address, this one time. **Always Allow** lets any shell command in the rest of that chat reach that address without asking again — a *different* address still asks, even in the same chat; it is not "network access in general." When Omnipus cannot read an address from the command at all (for example `npm install`, which reaches whatever registry it is configured for), the card says so — *"no specific host could be identified from the command text"* — and approving only opens the usual web and name-lookup ports (80, 443, 53) to any address, because there is no specific address to restrict to.
<!-- verify-ui-string -->

**How this is enforced, briefly.** Every shell command runs through a loopback proxy Omnipus starts for it; approving a network prompt is what tells that proxy which address(es) this chat's commands may reach. The approval is remembered for the rest of the chat only, never written to your `config.json` — that file's own `sandbox.egress_allow_list` field (a separate, permanent, operator-set list of addresses every chat can reach without ever asking) is untouched by an approval; the two lists are additive, not one replacing the other.

Auto-approve does not check what a command means. A command that deletes files inside your workspace, or stops a process, runs without a prompt, because it stays inside the sandbox. Use a `deny` or `ask` command rule for commands like that.

### What reading, searching and listing can reach

`read_file` (open one file), `list_directory` (list one folder) and `grep` (search many files by name and content) follow one rule. They reach any file your account can read, with these exceptions, which no tool policy or Auto-approve setting opens:

- **Omnipus's own files — everything in the Omnipus data directory** (by default `~/.omnipus`), except the short allow-list below. This is one rule, not a list of file names: a file the gateway creates there tomorrow is protected today. See "Omnipus's own files are protected from every agent," below.
- Other agents' own files and other workspaces.

Generic file tools retain the turn's authorized working area and these reading allowances inside the data directory:

| Part of the data directory | What agents may do |
|---|---|
| The agent's workspace work tree and folders you mounted into it | Read and write, as configured |
| The agent's own working files when the turn is rooted in its agent home | Read and write; definitions, memory, sessions and internal subfolders stay protected |
| Downloaded media and attachment staging | Read only |
| Uploaded files belonging to the acting chat or its root chat | Read only |
| Installed skills' helper files (scripts, templates — not `SKILL.md`/`AGENT.md`/`AGENTS.md`) | Read only, file tools only |

Omnipus's setup work can also reach other workspaces' work folders and their mounted folders. That does not open workspace records, private memory, or other internal files. A search of installed skill helpers with `grep` is allowed, but instruction-file names and contents are filtered out. Reading an instruction file is refused with directions to use the Skill tool; listing the registry directly is still refused.

These file-tool reading allowances do not grant registry listing, instruction-file reads or writes. Shell children use a separate kernel allowance set: the uploaded-file and installed-skill-helper allowances above are not general shell grants. Managed runtime execution has its own read/execute-only allowances when a kernel policy is active.

Internal state — sessions and transcripts, tasks and plans, memory, agent definitions (`SOUL.md` and friends), skill instructions, audit logs, gateway state, credentials, the master key and `config.json` — stays protected from generic file tools and is hidden from their search results. Shell commands use the command-text check and, when enforcing, the kernel restrictions described below. Use Omnipus's dedicated tools to access managed state.

Prompt sources are separate from these file protections. In native turns, an agent's own folder contributes no instructions from `AGENT.md`, `AGENTS.md` or `IDENTITY.md`. Its `SOUL.md` and the global `USER.md` still load. Workspace **Project Instructions** (the workspace's `AGENT.md`) and mounted projects' root `CLAUDE.md` or `AGENTS.md` continue to apply to native turns; `CLAUDE.md` wins when both project files exist. Omnipus does not automatically add the global user profile to external command-line workers' input.

A refusal carrying `protected_internal_file` is displayed as **Protected Omnipus file — refused**, explaining that approval cannot open it. Filesystem-policy denials and the shell's protected-operand guard can produce this marker. Other checks can return a different refusal first: agent-definition files can direct the agent to the metadata tools, and some shell pre-flights return an ordinary secret-set error. Read that refusal's reason; approval does not make the protected path accessible.

**Two accepted risks and one boundary are worth knowing:**

- **An operator's direct sandbox grant or a mount created outside Omnipus's checks can reopen an internal folder.** A `sandbox.allowed_paths` entry, or a mount added outside the normal mount checks, aimed **directly at an internal folder** reopens that folder in the kernel sandbox. This is an accepted configuration risk, not a grant an agent can create: `request_mount` refuses internal folders outright, even if you approve the request. `tools.allow_read_paths` and `tools.allow_write_paths` do **not** override protection for named internal files; file tools still refuse them. Aim sandbox grants and manually added mounts at work folders, not internal folders. Omnipus's own runtime and document access permissions are not covered by this accepted risk: a runtime grant cannot reopen protected files merely by naming a parent folder.
- **Shell commands can disclose the NAMES of entries directly inside the data directory on macOS.** A shell command running inside Seatbelt, macOS's kernel sandbox, can list those names. Protected contents remain inaccessible. This is an accepted disclosure of names **under the kernel sandbox**, not a command running outside it. `list_directory` of the data-directory root is still refused, and Omnipus's command-text check may also refuse a shell command that names that root directly.
- **Installed runtimes receive read/execute-only kernel grants.** With a kernel policy active, published shared toolchain generations and the document runtime are readable and executable, not writable, including under the God Mode floor. This is not a write guarantee for children running without a kernel policy.

**Where `grep` searches.** With no folder given, `grep` searches the agent's workspace and every folder mounted into it. Given a folder, it searches there instead: a path inside the workspace, a mounted folder's name, or an absolute path such as `/var/log` or `C:\Logs`. A path containing `..` works too, and reaches the same place it would for `read_file`. Matches inside the workspace are shown relative to it; everything else is shown as a full path that `read_file` can open. A search of a whole disk stops at the search limits and says it was cut short.

**What `grep` skips or hides while it searches:**

- Links (symlinks) it meets inside a folder are not followed. A link you name directly as the folder to search is followed, as `read_file` would.
- The Linux system folders `/proc`, `/sys` and `/dev` are skipped, and device files, pipes and sockets are never read. They hold no user content, and some of them can hang a search. You can use `read_file` to open a file you name in those folders.
- Agent notes files such as `SOUL.md`, `HEARTBEAT.md`, `AGENT.md` and `MEMORY.md`, and the instruction files of installed skills (`SKILL.md`, `AGENT.md`, `AGENTS.md` in Omnipus's own skills folder), never show up in results, by name or by content. `read_file` refuses their contents. An agent working from its own home can still list its ordinary working files and folders, but `list_directory` hides protected definitions, memory, sessions, installed-skill folders and internal state from that listing. Those hidden entries do not count toward listing pages. Ordinary nested folders with the same names remain visible when they are not protected paths. Listing the installed skill registry is still refused. A skill's other files, such as its helper scripts, can be read and found.

**Denying `read_file` alone does not stop an agent reading.** The policies for `read_file` and `grep` are separate, and `grep` returns the matching lines of any file it can reach (up to the usual limits and surrounding lines). A custom agent with `grep` allowed and `read_file` denied can still read that way. To confine what an agent reads, deny `read_file`, `grep` and `bash` for it.

**The Judge reads only the work under review.** While the Judge reviews a goal or a plan step, `read_file`, `list_directory` and `grep` reach only the workspace under review and the folders mounted into it. Anything else is refused, and an extra reading path an operator adds in the configuration cannot widen it.

**The Plan Supervisor is not limited this way.** It is allowed to use `grep`, and its `grep` follows the same rule as every other agent's: it can search anywhere your account can read outside Omnipus's own files, which no agent's search reaches. This is an accepted choice: all agents follow one rule, rather than the Plan Supervisor having a special limit.

**Personal secret folders outside Omnipus are not protected yet.** Folders such as `~/.ssh`, `~/.aws`, `~/.gnupg` and your browser profiles are not among Omnipus's protected files. With Auto-approve on, an agent whose reading tools are set to Ask can read and search them without a prompt, just as the shell could already read them. Protecting them for every tool is tracked in issue [#921](https://github.com/elicify-ai/omnipus/issues/921). Until then, if that matters for an agent, deny its reading tools and `bash`, or turn Auto-approve off.

Each `grep` search that runs is recorded in the audit log — see "How to review security activity" below.

### Task, plan, scheduled and other unattended runs

An unattended run has nobody present to answer a prompt. The same rule applies there as in a chat: anything Auto-approve would run in a chat also runs in an unattended run, and so does a call an operator `command_rules` rule or an existing "Always Allow" grant already covers. Anything that would still need a person is refused straight away; no approval card ever appears. In a task or plan run the agent is told:

```
Task and plan runs never ask a person for approval. This call needed approval and was refused. Do not retry; finish what you can and report what was blocked.
```

and the task's conversation shows the call as:

```
Not run: needs approval, and task and plan runs never ask.
```

The agent then reports what was blocked and the task ends **Failed** with its explanation. A scheduled run or a heartbeat shows its own wording instead:

```
Not run: this is a headless scheduled run with no operator available to approve ask-policy tools.
```

A shell command that would need a prompt (for example one that needs the network) is refused the same way; its error ends with `auto-denied: no operator attached to this headless/scheduled run to approve it`.

For example, with Auto-approve on, an unattended agent can write its report into the workspace, but a `delete_task` call is refused. An unattended call must either clear Auto-approve, match an operator rule or grant, or have an effective Allow policy. An explicit Deny always stays a deny. Setting one agent to Allow cannot override a global Ask or Deny. If a required tool still resolves to Ask, review both policy layers. Changing the global ceiling affects every agent; use per-agent restrictions to retain tighter access elsewhere.

**What counts as unattended.** Who answers an approval is decided by where the work started — the first conversation of the chain, not the helper doing the work and not whoever wrote last:

| Where the chain started | Approvals |
|---|---|
| A chat with an agent, an agent's main chat, a channel conversation | Asked inline. A helper the agent starts asks in that same chat. |
| A task — however it was started: **Start Task**, **Create & Run now**, dragging to In Progress, the Calendar's **Run now**, an agent's `run_task`, its own Calendar trigger, the queue or an auto-advance | Never asked; refused as above. |
| A plan — started from a chat with `execute_plan` or by the plan engine — including every member task and the plan's automatic wakes | Never asked; refused as above. |
| A recurring schedule, a per-agent heartbeat, the Judge | Never asked; refused with the scheduled wording. |

A helper started inside a task or plan run is unattended too, however many levels down it is. Writing into a task's conversation yourself, or reviving a finished helper of a task, does not make the run attended. A message from a task to another agent's main chat is different: the receiving agent works in its own main chat, so its approvals are asked there. If Omnipus cannot tell where a chain started (for example its saved records are damaged), it treats the run as unattended and refuses.

This means the Omnipus agent's apply workflows — creating an agent or setting up a team — work from a chat only: if a task sends Omnipus to apply a plan, the run ends at the first step that needs your approval.

On a host without an enforcing kernel sandbox (Windows, or the process sandbox not set to Enforce), Auto-approve runs only read-only shell commands and commands an operator `command_rules` allow rule covers, so more task runs end blocked there.

### Approve a request

When a tool needs your decision, an approval card appears in the chat the request belongs to, above the message box. A helper's request appears in the chat that started the helper, for example an Omnipus helper's in Omnipus's chat. Requests in one chat are shown one at a time; **+N more** indicates when more are queued. While a request waits, that main chat needs your attention: its agent's row in the sidebar shows the attention halo, and a collapsed workspace counts it among the main chats that need you (see [the sidebar](using-omnipus-ui.md#the-sidebar)). There is no separate notice; open that chat to answer. Read the named agent, tool and arguments before choosing Approve Once, Deny, Always Allow or Cancel. Some tool-specific requests use more specific button labels.

| Button | What it does |
|---|---|
| **Approve Once** | Runs this one call. Nothing is remembered; the next identical call asks again. |
| **Always Allow** | Runs the call and remembers it for the rest of this chat, so an identical call does not ask again. |
| **Deny** | Refuses the call. |
| **Cancel** | Closes the request without running it. |

The card never takes focus. Typing in the message box or pressing Escape does not count as approving or denying a request. Selecting **Stop** in the owning chat cancels that chat's pending approvals, including requests from its helpers and their helpers. The cards resolve as cancelled, the waiting helper executions stop, and the unapproved actions do not run. Another chat's pending approvals stay pending. This does not turn an ordinary Stop into Stop all for helpers that are already working without a pending approval. Work already performed is not undone. Cleanup selects requests present when Stop is received; a fresh request raised by a later resumed execution is not withdrawn by that older Stop. If Approve wins before a selected request is cancelled, its action is not revoked, and Stop reports the already-approved request. If saved helper data cannot be read, Stop reports incomplete cleanup instead of claiming that every request was cancelled. These operational Stop outcomes appear in **Notifications**. In the sidebar, select your username at the bottom, then choose **Notifications**. These notices are not failed replies and do not end or replace a reply that is still running.

An unanswered request expires without running the action. Expiry is a timeout, not a denial by you. When the server confirms that a request has resolved, its card closes. If an expiry notice remains visible, select **Dismiss**. After a page reload or reconnect, a request that is still pending returns as a complete card you can approve, deny or always allow.

**Where a request came from.** When the work being approved was triggered by someone other than you in the web app, the card names it: *"while answering <person> on <connection>"* for a channel message, *"for group <name>"* for a group-chat request, *"asked by <Agent>"* for another agent's request. A conversation that has taken in several such inputs shows the most significant one — a channel person first, then a group, then another agent — and keeps the first of each kind; it never drops back to a lesser one. A request that is already waiting keeps the label it was raised with.

**Which sidebar entry is marked.** It is the asking agent's main chat in that workspace. This also holds when the request comes from another of that agent's chats, from a per-person channel conversation, or from a helper working for one of them; a helper's request marks the main chat of the agent whose chat started the work. Known limit: when that agent has no main chat in the workspace, or the conversation belongs to no workspace, no sidebar entry is marked — the request still shows in its own chat.

What **Always Allow** remembers lasts only for that chat and ends with it. Agents that this chat hands work to inherit it. Nobody else's chats are affected, and there is no list of remembered approvals to review. For most tools it remembers the exact call, with the same arguments. For a shell command it offers a choice; see "Command rules compared with one-time approvals" below.

These controls answer different questions.

| Control | What it decides | Use it when |
|---|---|---|
| Tool policy | Whether an agent may call a tool: Allow, Ask or Deny | You want a standing decision for a specific capability |
| Auto-approve | Whether an Ask tool skips the prompt for calls Omnipus can judge safe | You want fewer prompts without giving up a check on anything that reaches further |
| Command rules | Allow, ask or deny for specific shell commands, written in the configuration file | You want a standing rule for one program, such as "always ask before `npm publish`" |
| Process sandbox | What started programs may reach | You want operating-system isolation where the platform supports it |
| Filesystem model | What agents may read and run | You want open access or a confined list of locations |
| Shell workspace limit | Whether a command may name paths outside the working folder | You want a command-text check that still applies even when the sandbox is off |

The process sandbox offers three modes. **Enforce** blocks violations. **Permissive** records violations without blocking them. **Off** removes operating-system protection, but it does not disable the shell workspace limit. Only Enforce counts as an active kernel sandbox. In Permissive or Off, the composer **Auto** button stays pressed and its tooltip adds the no-sandbox caution: Auto-approve still runs for every other tool, but the shell only auto-runs a read-only command or one an operator's rule explicitly allows — every other shell command asks first, with no operating-system check behind the text-based one.

The **Confined** filesystem model limits reads and execution to listed locations. The **Open** model lets agents read and run anything your account can reach, apart from Omnipus secret files. Writes remain limited to the workspace and mounted folders. Changes to the filesystem model take effect after a gateway restart.

The Security screen reports the protection available on your current platform. If kernel-level protection is unavailable, Omnipus falls back to checks in the application. A program that ignores those checks is not contained by the operating system.

## Shell command rules

The `bash` tool policy and Auto-approve, above, decide for shell commands as a whole. Command rules go further: an allow, ask, or deny decision for one specific program, or one specific program plus how it is called. There is no screen for this — you write rules directly into the configuration file. This is a deliberate choice: a rule that can quietly deny or approve real commands is a security control, and Omnipus keeps security controls in a file you own and can review, not behind a settings toggle that could be changed by mistake.

### Where rules live

Rules live in the `sandbox.command_rules` array inside your `config.json`, in the Omnipus data directory. By default that is `~/.omnipus/config.json` in your home folder; if you set the `OMNIPUS_HOME` environment variable, it is `config.json` inside that folder instead. Edit the file with any text editor — Omnipus is meant to be running while you do; see "When a rule takes effect" below.

### The shape of a rule

Each rule is a small object with up to three fields:

| Field | Required | What it means |
|---|---|---|
| `action` | Yes | `allow`, `ask`, or `deny` — same three outcomes as any other tool policy. |
| `binary` | Yes | The program the rule applies to: a bare name such as `git`, `npm` or `rm`, or a full path such as `/usr/bin/git`. No spaces or shell symbols. Omnipus matches this against the actual program the shell would run, not just the word your agent typed. |
| `arg_prefix` | No | Restricts the rule to calls whose arguments start with these words, matched whole word by whole word. `"test"` matches `go test ./...` but not `go testify`. Leave it out to match every call to that program, with any arguments. |

A worked set of examples, as they would appear in `config.json`:

```json
{
  "sandbox": {
    "command_rules": [
      { "action": "allow", "binary": "git",  "arg_prefix": "status" },
      { "action": "ask",   "binary": "npm",  "arg_prefix": "publish" },
      { "action": "deny",  "binary": "rm",   "arg_prefix": "-rf" },
      { "action": "allow", "binary": "go",   "arg_prefix": "test" }
    ]
  }
}
```

Read as plain sentences: always let `git status` run; ask before any `npm publish`; never allow `rm -rf`, no matter what follows it; always let `go test ...` run.

### How Omnipus decides when more than one rule matches

The order is always the same, regardless of how narrow or broad a rule is: **deny beats ask beats allow.** If any matching rule says deny, the command is refused — a broader or narrower matching allow rule does not change that.

Worked example: suppose you have both

```json
{ "action": "allow", "binary": "rm", "arg_prefix": "-rf /tmp" }
```

and

```json
{ "action": "deny", "binary": "rm", "arg_prefix": "-rf" }
```

With `allow` for `binary: "rm", arg_prefix: "-rf /tmp"` and `deny` for `binary: "rm", arg_prefix: "-rf"`, `rm -rf /tmp` matches both rules and Deny wins. `rm -rf /tmp/build` does not match the narrower allow: `/tmp` and `/tmp/build` are different argument words. `arg_prefix` is not a directory-tree matcher. Writing a narrower allow rule is never a way to carve an exception past a broader deny rule; if you want that exception, the deny rule itself has to be narrowed instead.

### Chained commands

A command joined with `&&`, `||`, `;`, `|`, or a line break is checked one part at a time. Each part is matched against your rules on its own, and the command as a whole gets the strictest result among its parts. One denied part refuses the whole command. Failing that, one part with an `ask` rule makes the whole command ask. Parts with no rule at all fall back to the tool policy and Auto-approve as usual.

Worked example: with the `ask`-before-`npm publish` rule above and no rule at all for `git`, the chained command `git pull && npm publish` asks before running, because its second part needs to ask — even though `git pull` on its own would not have.

### How a rule interacts with Auto-approve and God Mode

The columns below match the composer **Auto** button. **Auto** includes the case with no kernel sandbox: a command rule's `allow`/`ask`/`deny` decision applies the same whether or not a kernel sandbox is active — it is a check on the command's text, not something the kernel sandbox does or does not add.
| Rule action | Ask | Auto | God Mode |
|---|---|---|---|
| `deny` | Refuses the command. Nobody is asked to approve something that cannot run. | Refuses the command. | Refuses the command. This is the one check God Mode does not turn off. |
| `ask` | Asks before the command runs. | Asks before the command runs, even when Auto-approve would otherwise have let it through. | No effect. God Mode shows no prompts, and an `ask` rule does not create one. |
| `allow` | Skips the approval card, but only when **every** part of the command is a plain command that matches an allow rule (see below). | No extra effect. It does **not** skip Auto-approve's own checks: a matching command that writes outside the workspace or needs the network still asks. | No effect. God Mode already runs everything without asking. |

An `ask` rule prompts in every state except God Mode. That includes an agent whose `bash` policy you set to Allow: the rule still asks before `npm publish`, even though every other command runs freely.

When an `ask` rule triggers the prompt, you see **one** approval card for the command, not two. When the agent's `bash` policy is Ask and Auto-approve is not active, that card also names the rule, for example:

```
matches an operator rule that requires approval (binary="npm" arg_prefix="publish")
```

Choosing **Always Allow** on that card remembers the exact command for the rest of the chat, so the same command does not prompt again there. **Approve Once** remembers nothing, and the next `npm publish` asks again.

**An allow rule only covers a plain command.** It skips the card only for a part with nothing added around the program and its arguments:

- no redirection into a file (`git status > out.txt`);
- no command substitution or subshell (`git status $(cat list)`, `` `…` ``, `( … )`);
- no environment-variable prefix (`GIT_DIR=/tmp/x git status`);
- no full or relative path in front of the program (`./git status`, `/usr/bin/git status`), because that is how a look-alike program would sneak past the rule.

Any of these means the card appears as normal. A chained command skips the card only when every part passes this test. For example, with the rules above, `git status && go test ./...` runs without a card, but `git status && curl example.com` asks, because `curl` has no allow rule.

A part Omnipus cannot read reliably, such as a program name built from a variable (`$X -rf /`), a brace expansion (`{cat,/etc/passwd}`) or a program that does not exist on the agent's path, can never satisfy an allow rule.

Rules match the program by where it really is on disk, not by the word typed. A rule for `rm` also catches `/bin/rm -rf build` for `deny` and `ask`. An `allow` rule for `git` does not approve a different program that happens to be named `git` earlier on the agent's path.

**A deny or ask rule also catches the program run through a wrapper.** Omnipus looks through `env`, `sudo`, `doas`, `nice`, `ionice`, `nohup`, `setsid`, `timeout`, `stdbuf`, `time`, `command`, `builtin`, `exec` and `xargs` to the program they go on to run, including one wrapper inside another (`env sudo rm -rf x`). A rule for `rm` catches every one of the examples above the same as it catches plain `rm -rf x`.

**A command that runs another program through an interpreter is treated with extra caution**, because Omnipus cannot see inside the text it hands that interpreter: `bash -c "…"`, `sh -c "…"`, `zsh -c "…"`, `eval …`, `source …`, `python -c "…"` (and `python3`, and other Python versions), `perl -e "…"`, `ruby -e "…"`, and `node -e "…"`. When any of your deny rules names a program whose name appears as a whole word inside that text — for example a `deny` rule for `rm` and a command `bash -c "rm -rf x"` — Omnipus refuses it, the same as it would refuse plain `rm -rf x`. If none of your deny rules' names appear that way, but you have written **any** `ask` or `deny` rule at all, Omnipus asks before running the interpreter command rather than silently letting it through — an interpreter is exactly the shape that could be hiding something a rule would otherwise have caught. With no rules written at all, the shell's own Ask/Auto/God Mode handling decides whether an interpreter command runs.

**What this still cannot catch.** A wrapper Omnipus does not recognize (something other than the list above) is not looked through — if your denied program's name later appears as one of that unrecognized command's own words, Omnipus asks rather than silently allowing it (so you still see it), but it cannot always tell you that word is really about to run as a command. And inside an interpreter's text, only a whole-word name match is checked — a program built at run time from pieces, or referred to indirectly, can still slip past. The kernel sandbox (Enforce mode), not this text-based check, is what actually stops a command from reaching outside your workspace or the network regardless of how its text was written.

On Windows, a command is always judged as one unit, and `arg_prefix` must match the whole argument list exactly, not just its start.

### Command rules compared with one-time approvals

When a shell approval card appears, **Always Allow** offers a choice of how much to remember:

- **Allow this exact command.** Only the identical command, run from the same folder, skips the card again.
- **Allow commands starting with `<program and its leading words>`.** For example, approving `npm run test -- --watch` this way can remember `npm run test`, so `npm run test` with any later arguments skips the card. It does not match `npm run testfoo`.

Either way, the memory lasts for the rest of that chat only. It ends with the chat, and nobody else's chats are affected. **Approve Once** never remembers anything. A command rule in `config.json` is the opposite: it applies to every agent and every chat, permanently, until you edit the file again.

The "starting with" choice is offered only for a single plain command, where approving the prefix cannot quietly cover more than you saw. It is not offered for:

- a chained command (`a && b`), because the words after a prefix could belong to the next part;
- a bare program with no arguments (`ls`), because that would approve any use of `ls`;
- a command run through a wrapper such as `sudo`, `env`, `timeout`, `xargs` or `sh -c`, because the wrapper alone would approve whatever it goes on to run;
- a command with redirection, substitution or an environment-variable prefix;
- any command on Windows.

In those cases **Always Allow** remembers the exact command only. The card shows a chained command part by part so you can see what each part runs, and the confirmation tells you exactly what was recorded.

Use a one-time approval for something you only expect to approve in this chat. Use a command rule for a standing decision, such as "this team always needs `npm publish` reviewed" or "never run `rm -rf` on this machine", that should not depend on clicking the same way every time.

### Agents cannot change these rules

The whole `sandbox` section of `config.json`, including `command_rules`, is off-limits to an agent's own configuration tool. An agent can read its own security settings so it can explain why it is refusing something, but it cannot loosen or add to them — that stays a change only you make, directly in the file.

### When a rule takes effect

No restart needed. Omnipus checks `config.json` for changes every 2 seconds; when it changes, Omnipus reloads it, validates it, and rebuilds every agent's shell tool with the new rules. A command already running when you save the file keeps running under the rules that were in force when it started — only the next command picks up the change.

### If a rule is written incorrectly

An invalid rule rejects the **entire** reload, not just that one rule. The previous, still-valid configuration stays in force, so a typo cannot leave you with fewer rules than you intended. The error names the rule by its position in the list, counting from 0, and says what is wrong with it. For example, a capitalised action in the second rule gives:

```
config error: sandbox.command_rules[1]: action "Deny" must be one of allow, ask, deny
```

Rule actions are lower-case: `allow`, `ask`, `deny`. Other mistakes it catches: a missing `binary`, a `binary` with spaces or shell symbols, a relative path such as `./git`, control characters in `arg_prefix`, and more than 1,000 rules.

You see the error in two places, until you fix the file and save it again:

- the Omnipus server log, at the moment you saved, followed by `Using previous valid config`;
- `GET /health`, which answers with status 503 and reports the gateway as degraded. The reason field carries the same error with two prefixes in front of it:

```json
{
  "status": "degraded",
  "reason": "config reload failed: config reload rejected: config error: sandbox.command_rules[1]: action \"Deny\" must be one of allow, ask, deny"
}
```

## Omnipus's own files are protected from every agent

Omnipus keeps its own state — sessions and transcripts, tasks and plans, memory, agent definitions, the skill registry, audit logs, gateway state, `config.json`, saved credentials and the master key — inside its data directory. Generic file tools **deny internal data by default**, except authorized work areas and the reading allowances described above. Named internal files remain protected even in an agent's own working area.

Approval cannot authorize generic access to protected internal files. A general tool approval may appear before the tool checks its path; approving it does not override the later refusal. God Mode does not bypass these application checks. Shell protection also depends on the active kernel sandbox, as the platform table explains.

The application's default-deny rule also covers new internal locations before they exist. Kernel restrictions have the platform limits below. Use Omnipus's dedicated controls instead — conversations through the app, skills through the Skill tool, agent definitions through the agent editor.

### What each platform enforces

| Platform | What enforces it |
|---|---|
| Linux | With Landlock available and the Process Sandbox enforcing, shell and environment-setup children receive kernel protection for internal files, including the God Mode floor. File tools and the shell command-text check also apply. Basic Landlock file rights are available from Linux 5.13; later rights depend on the detected kernel interface. |
| macOS | With Seatbelt available and the Process Sandbox enforcing, shell and environment-setup children receive a profile denying protected internal files, including in God Mode. File tools and the shell command-text check also apply. Programs that check website certificates through macOS itself, such as `gh` and other Go-built tools, can do so inside the profile, because it allows macOS's certificate-checking service. That access also lets a command read your certificate trust settings and clear that service's caches, which costs only performance. It does not reach the secrets stored in your keychain, and it cannot permanently change which certificates your Mac trusts, because that needs an authorization this sandbox does not grant. These limits come from Apple's published source code and were not checked against macOS 26 itself. |
| Windows | There is no kernel sandbox. File tools refuse protected paths, and the command-text check refuses a shell command whose protected path it identifies. A command that builds its path at runtime can evade that text check. |

On Linux and macOS with the sandbox Off, Permissive or degraded/unavailable, the same no-kernel limitation applies: file-tool checks remain, but the command-text check alone does not contain arbitrary shell programs. Check the Security screen's actual enforcement and capabilities, not just the operating-system name or version.

An authorized `environment_setup` run can create and read new files directly inside its selected installation folder, as well as use its selected cache and temporary folders. This does not authorize writes to sibling workspaces or unrelated operator-granted folders.

### Limits of the protection

- **macOS:** a file the gateway creates in the data directory *after* a command started is not in that command's deny list, so a long-running command could reach it. The next command sees it denied. Omnipus names its lazily-created directories in the protected set, so this window is narrow in practice.
- **Linux:** the floor that denies the protected set grants everything else individually, so a God-Mode command cannot create a new file *directly inside an ancestor folder of the data directory* (for example a new file directly in your home folder). Files inside folders it can already reach are unaffected. On macOS the same operation works. Landlock blocks protected contents and directory enumeration, but does not hide a file's name when a program already names that file for a metadata lookup. The command-text check still refuses a shell command that directly names a protected file. On Linux with the kernel sandbox, shell commands for an agent that works from its own folder (for example Omnipus) cannot create new files directly at that folder's top level, only inside existing ordinary subfolders or in workspaces.
- **Runtime paths are trusted.** The sandbox trusts toolchain and runtime folders by the path supplied to it. If that path is a shortcut (symbolic link), it does not recheck where the shortcut leads against the protected-file inventory, so only use locations you trust.
- **External command-line workers are not covered.** A worker delegated to Claude Code, Codex or OpenCode runs outside Omnipus's sandbox and file policy entirely (see the God Mode section's external-worker note). Delegating to one is a decision to trust that tool with your account — Omnipus's internal-file protection does not follow it.

## Entering a secret from a chat

When an agent needs a provider key, an MCP server environment secret, or a channel credential, it can open a **secure secret card** in the web app. Type the value into that card, not into the chat. The card names the acting agent, explains why the secret is needed, and shows its destination. An Omnipus helper's card appears in Omnipus's main chat; other helpers must ask their parent.

Each value must contain 4–16,384 bytes of UTF-8 text and no NUL character, and the complete submission is limited to 65,536 bytes. A card holds at most eight fields; the server chooses them, including Slack's required bot-token and app-token pair. Only one secret request may be pending in a chat session, and at most 64 may be pending across the installation; finish or cancel an existing request before asking for another when a limit is reached.

While a secure card is waiting, the sidebar marks a main chat as needing attention: the chat where the card was raised when that is a main chat, otherwise the main chat above the helper that raised it, otherwise the asking agent's main chat in that workspace. Known limit: when that agent has no main chat in the workspace, no sidebar entry is marked; the card still shows in its own chat. When the server cannot read the waiting cards, the entry shows attention as unknown, not "nothing pending". Once the card is no longer waiting, it no longer counts toward the cue.

| Action or state | What happens |
|---|---|
| Submit | All requested fields are saved together in the encrypted vault, under new versioned reference names. The agent continues with those names, never the values. |
| Replace an integration secret | The replacement is saved as a new version. Saving the card alone does not switch the integration to it. |
| Cancel the card, or Stop before submitting | Nothing is stored. |
| Stop after submitting | The saved version remains in the vault. The stopped continuation does not restart on a later reload, explicit chat revival or gateway restart. This also applies when you press Stop after a restart has already interrupted the session. Stop does not undo an integration change already completed. |
| Gateway restarts before submission | The request becomes unavailable. Ask the agent for a new card. |
| The destination changes while the card is open | Submission is refused. Ask for a new card showing the current destination. |
| Saving fails before anything is stored | The card says the request is still pending and what to check: that the credential store is unlocked, readable and writable, and that there is free disk space. Fix that, then submit again. The gateway log records the request, the failed step and a safe cause, never the value. |
| Saving succeeds but the follow-up is not confirmed | The card says the secret is saved. Do not enter it again; reopen the chat to retry the continuation. |

Each card transition is recorded in the audit log without the values. If the gateway stopped after saving a submission but before recording it, the restart finds the saved submission and records it as `secret.request.recovered_submitted`.

The card does **not** ask you to re-type your account password: submitting that exact destination-bound card is consent for that write only. It cannot choose an arbitrary vault entry or delete one. Raw Credential Vault changes in Settings still require password confirmation where applicable.

A turn that consumed a message from a channel person cannot open this card, even when that message arrived in an agent's main chat. The agent instead asks you to open the web app and ask it there. Unattended runs cannot open a card either.

Submitted values are not included in card frames, the conversation, or the continuation message. Stored versions also join the sensitive-value filtering set, including versions an integration has not selected. A damaged, unrelated vault entry does not prevent a continuation from using its readable submitted references: readable versions remain in the filtering set, and previously registered values are retained when the complete scan reports damage. If the submitted references themselves cannot be read, the continuation waits for repair; do not submit their values again. Filtering remains best-effort and can be switched off; do not treat it as protection against a command you deliberately allow to reveal a secret.

### Saving is separate from selecting

The card saves a new version; it does not itself change a provider, connector or channel. The agent must use that integration's configuration tool, with the stored **reference name** and your approval. Those tools refuse secret values in their inputs. Plaintext entry remains available in Settings.

| Integration | What the agent selects | What is checked before saving the change |
|---|---|---|
| Model provider | `configure_provider` with `api_key_ref` | The provider, effective endpoint, protocol and API-key field match the card's destination. An endpoint-only card is restricted to that exact endpoint. Every affected provider row is checked. |
| MCP server | `add_mcp_server` with an `env_refs` map | The server name, transport, executable, ordered arguments, URL and specific environment-variable name match the card's destination. Only the named reference entries are attached. |
| Channel connection | `configure_channel` or `connect_channel` with fields such as `token_ref`, `bot_token_ref` or `app_secret_ref` | The exact connection instance, its platform account and settings, and the credential field match the card's destination, checked against the connection as it will be saved. Which workspace and agent the connection answers for, and whether it is enabled, are not part of the card's destination: they are routing, which you approve on the `connect_channel` step itself. A change of account or settings that would redirect a retained credential is refused too. |

A reference name alone is not permission to use a secret elsewhere. An unknown, unavailable, unbound or mismatched reference is refused without changing the integration or the stored value. If the destination changed, establish the new destination in Settings, then ask for a new card. Selecting a replacement leaves the earlier stored version intact.

These are shared installation settings. The agent first reads the current entry and confirms the proposed change with you. If Settings or another agent changes that entry before the write, the tool refuses the stale revision. Re-read, review and confirm the new plan before retrying; do not replay the earlier approval blindly. Saving the card is not approval for this later configuration change.

### Adding a new MCP server that needs a secret

For a new stdio MCP server that needs an environment-variable secret, the non-secret server description must exist before it can be shown on a card. This environment-secret flow does not support HTTP or SSE servers:

| Step | What you should expect |
|---|---|
| 1. Save the non-secret server description | Approve the stdio server's executable and arguments. The server entry is saved and a connection is attempted. A server that needs a secret may not connect yet: the entry remains saved, but credential setup is unfinished. |
| 2. Submit the secure card | The agent requests the environment-variable secret for that saved server. Submit saves a version and returns only its reference name to the continuation. |
| 3. Approve reference attachment | The agent re-reads the server's revision, confirms the attachment and calls `add_mcp_server` with the reference map. This attaches references to the unchanged server; it is not a general update command. |
| 4. Read the connection result | Omnipus attempts live reconciliation and reports the connection status. A saved reference is not proof that the server connected or that its tools work. If live reconciliation fails, the result is **saved but not working** with the failure reason, even when the server is still connected with its previous configuration. Tools still require their normal assignments and policies. |

Attaching references does not enable a disabled server, change the global MCP switch, replace omitted references, or alter its executable, arguments, URL or policies. Omitted description fields mean unchanged; supplied description fields must match the saved destination. A changed server revision requires a new confirmation, and a changed destination requires a new card.

Cancelling the card leaves the non-secret server entry in place and stores no secret. Stopping after submission leaves the new version stored but does not attach it automatically. You can continue the setup later with a fresh confirmation or remove the server deliberately; Omnipus does not silently roll it back or remove and recreate it.

### Private variables for one agent

An agent can also ask for a **private variable**: a named secret, such as `WEATHER_TOKEN`, that only that agent's own commands receive. It belongs to one agent in one workspace. Mia's `WEATHER_TOKEN` in Sales is not visible to Ray in Sales, and not to Mia in Marketing (Marketing can have its own value).

The card shows the owner as "Mia in Sales" and the variable name. Omnipus can request a private variable for another agent, but only for an agent that is a member of that workspace.

| What you should know | Details |
|---|---|
| Promise | Never typed into the chat, never in tool arguments, hidden in output where possible; **a command you let the agent run can still reveal it**. Output filtering hides the exact value only: a transformed copy (for example base64) is not guaranteed to be hidden. |
| Where it is available | Only as an environment variable in that agent's own commands in that workspace: `bash` (foreground and background) and `environment_setup`. It is added after Omnipus's own environment settings, so the document toolchain does not remove it. It is never placed in the gateway's own environment, and external-program workers do not receive it. |
| Names | Uppercase letters, digits and `_`, 1 to 64 characters, starting with a letter. Names Omnipus sets for commands are refused, with the reason: for example `PATH`, `HOME`, `TMPDIR`, `HTTP_PROXY`, `PIP_CACHE_DIR`, `PYTHONPATH`, `NODE_PATH`, and any name starting with `LC_`, `XDG_`, `OMNIPUS_`, `LD_` or `DYLD_`. |
| Replacing it | Submitting a new value on the owner's card takes effect at once for the next command, with no second approval. The previous version stays stored and listed as superseded. Only Omnipus can switch an agent's variable back to an older version (`select_secret_version`), and it asks for your approval first. |
| Turns started by a channel person | Private variables are **not** added to commands in a turn that read a message from someone on a connected channel, unless you marked that variable "available in channel conversations". This is off by default; only a signed-in web user can change it, and each change is recorded in the audit log. |
| If it cannot be read | The command still runs without it, and the result says which private variable could not be read. Replace it on a new card. |

### Listing and removing secrets in chat

Agents see stored secrets by **reference name**, never by value. `list_secrets` shows each entry's reference name, kind, version, destination, the integrations that use it, and whether it is in use; an ordinary agent sees only its own private variables in the current workspace, while Omnipus sees every stored reference. Entries saved through Settings (or by boot configuration) are listed too, with the kind **unbound** and the destination **Stored in Settings; no recipient recorded**: no recipient was recorded for them, which says nothing about whether they are used. Their consumer list shows which integrations use them; one with no consumers is shown as stored, not in use. Omnipus sees their names, never their values, and can remove them by name.

Only Omnipus can remove a secret, with `remove_secret`, and you approve it first. The approval card lists every integration that uses the reference before anything is deleted. Removing a reference that is still in use is allowed: after removal the configuration is reloaded, the integrations that used it stop using it and show as degraded, and the next start keeps running with them degraded until you give them a new secret. The result reports three separate facts — whether the value was durably deleted, what changed for running connections, and whether the configuration still starts. Removing an older version that a replacement superseded leaves the current version working. Each removal is recorded in the audit log as `secret.removed`, without the value.

## How to manage saved secrets

1. Open **Settings**, then select **Security**.
2. Find **Credential Vault** and select **Add key**.
3. Enter a key name and its value, then select **Save**.
4. Re-type your password if Omnipus asks you to confirm the change.
5. To remove a saved secret, select its remove button and confirm **Remove**.
6. To replace the vault's master protection, select **Rotate master key**, enter a new passphrase, and select **Rotate**.

Omnipus stores credential values in an encrypted file. Settings refer to credentials by name instead of storing their plain values. On a fresh installation, Omnipus creates a master key and warns you to back it up. If you lose that key, existing credentials cannot be recovered.

Rotation re-encrypts the whole vault with the new passphrase. Back up the new passphrase because Omnipus needs it to unlock the vault later.

If one saved entry is damaged but another entry can still be decrypted, the gateway keeps running. This also applies when the damaged entry is used by a provider, mailbox or channel, during startup, configuration reload or a Settings update. The affected integration cannot use that entry; the server log names its reference at error level so you can repair or re-enter it. Other readable entries remain usable, and detecting damage does not rewrite the vault. A locked vault, an unreadable vault file, or a nonempty vault with no decryptable entry still stops startup or rejects the update. If the master key may be wrong, restore the correct key before re-entering secrets.

Each stored value is encrypted together with the name it is stored under, so a value cannot be moved from one entry to another and still open.

Normal credential reads use the name-bound version-2 format. Unlock can convert a version-1 vault before use: it authenticates every entry, binds each value to its name and replaces the complete file atomically. Key-file mode keeps the master key; passphrase mode derives a key with fresh random salt. After replacing the credential file, Omnipus attempts to save `credentials.json.migrated`. If writing that marker fails, it logs a warning; the conversion still succeeds. It then emits `credentials.store_migrated`, with counts but no entry names or values. Without the marker, a restored version-1 file can be converted again. When the marker is present, a version-1 file is refused rather than converted again.

**Conversion cannot authenticate the names in an unbound version-1 file.** A swap inside that file is indistinguishable from its genuine layout and is carried into the name-bound format. Someone who can write to your Omnipus home folder and still holds a version-1 copy encrypted with the current master key or passphrase can:

1. swap values inside that copy,
2. delete `credentials.json.migrated` (it sits in the same folder, so the same person can delete it), and
3. restore the substituted copy.

The next unlock can then convert it and retain the swap. When present, the marker stops accidental or careless restores; it is not a defence against someone who can replace both the credential file and its marker. This risk lasts for the lifetime of the secret that opens the version-1 copy, not just one unlock.

| How your vault is opened | How to close the version-1 restore risk |
|---|---|
| Passphrase typed at start-up | Run `omnipus credentials rotate` and choose a new passphrase. Fresh salt prevents mixing entries from before and after conversion, but only changing the passphrase prevents a whole version-1 copy from opening. |
| Key file (`master.key`, `OMNIPUS_KEY_FILE`) or `OMNIPUS_MASTER_KEY` | Change the master key. No command performs that change while retaining key-file mode and all stored values. Stop the gateway, make a trusted backup, replace the key, remove `credentials.json`, start again, and re-enter each credential. Until then, protect version-1 copies of `credentials.json` in backups and sync folders as carefully as the key itself. `omnipus credentials rotate` also works, but it switches the installation to a passphrase that must be entered at every start, which does not suit an unattended server. |

If any entry cannot be authenticated during credential-file format conversion, the source file is left unchanged and the gateway does not start. The error names each entry at fault. See [troubleshooting](troubleshooting.md#credential-file-format-or-authentication-errors).

## How to review security activity

1. Open **Settings**, select **Security**, then open **Advanced / technical details**.
2. Find **Audit Log** and select **View Log**.
3. Filter entries by event or decision to narrow the list.
4. Check the chain status. **Chain verified** means the displayed log passed its integrity check.

The viewer refreshes every 30 seconds. The log includes security events, policy decisions, and tool executions. It can show security-setting changes with their previous and new values.

Before an entry is written, Omnipus replaces recognised credentials with `[REDACTED]` in the command, tool parameters and event details. Recognised formats: `sk-…` and `key-…` keys, `Bearer …` tokens (in any letter case), GitHub `ghp_…`/`gho_…` tokens, Slack `xoxb-…`/`xoxp-…` tokens, AWS access-key IDs (`AKIA…`/`ASIA…`), Google `ya29.…` access tokens, JSON Web Tokens, and the password in a web address, such as `https://user:PASSWORD@host` or `redis://:PASSWORD@host` (only the password is replaced). A value is also replaced when its field is named exactly like a secret (`password`, `token`, `api_key`, `authorization` and a few similar names). This is always on and cannot be switched off. Email addresses are kept, so mail recipients and message IDs appear in full. Anything else is not caught: a plain password typed as a command argument, a raw `/` or `@` inside a web-address password, a scheme-less `user:pw@tcp(…)` connection string, an AWS secret access key, a GitHub fine-grained token (`github_pat_…`) or a Google API key (`AIza…`).

Logs written before this redaction was switched on (issue #914) are not rewritten. They may contain credentials typed into shell commands. If that matters for your installation, rotate those credentials.

Every `grep` search that runs adds one `path.search_roots` entry, however many files it matched. Expanded, it lists the folders searched and the folder the agent asked for. It never contains the search term or any file content. A `grep` refused because of the folder it named adds a `path.access_denied` entry with the tool, the path and the reason, the same as a refused `read_file`.

## Mail metadata and temporary previews

When you add an [email mailbox](connectors.md#how-to-add-an-email-mailbox), Omnipus keeps a small private cache so the mail panel opens quickly. What it holds is narrow: folder names, roles, and counters — encrypted at rest inside Omnipus's data directory, in a storage area named after a random identifier that never contains your address or folder names. Message lists, senders, subjects, and bodies are never written to disk; they live in memory only while you are viewing them. A **cache unavailable** warning means Omnipus could not prove that its own protections hold on your install (for example a disk or permission failure) — it is not a prompt to configure anything external, and mail keeps working live while the warning shows.

The application establishes its own protections on every install: its backups and restores never copy the mail cache or the new-mail watching state, and it refuses to write the cache if it cannot prove that exclusion — including when the data directory has been placed under version control in a way that could capture it. Deleting a mailbox (or disabling it) erases that cache and the watching state; if a delete step fails, the removal reports **cleanup pending** with a retry key rather than claiming success, and the cleanup operation can finish the job afterwards. Removing the cache never deletes mail on your mail server, and it never touches files you saved from mail into a workspace — those are ordinary workspace files. Cache loss does not delete server mail; losing the credential master key can still make credentials unrecoverable, per ["How to manage saved secrets"](#how-to-manage-saved-secrets).

Message previews are metadata-only: opening a preview issues a permission token that stores no message content. Every time a preview renders — the message view, each inline image — Omnipus fetches that piece fresh from your mail server within that one request, through the same shared connection pool and rate budget as the rest of the panel; nothing is kept after the request, the response is marked no-store, and remote images load only from the addresses that fetch recorded, only when you chose **Load images**. Attachments you open are fetched piece-by-piece rather than as whole messages, previews are capped at 25 MiB (use **Download** for larger files), and the preview controls — session limits, logout revocation, identical "not found" answers — apply throughout. Files you explicitly **Save** from mail become ordinary workspace files under the normal workspace rules; the temporary-preview restrictions end at the save.

## What child programs can see

When Omnipus starts another program — the managed browser, a process hook, a git command, a connected (MCP) server, a media transcoder, or any command an agent runs — that program does **not** receive Omnipus's environment. It starts from a fixed allowlist instead: the basics a program needs to run (`PATH`, `HOME`, `USER`, terminal type, language and locale settings, temp folder, proxy settings), your `XDG_*` folder settings, and anything you explicitly forward by naming it with an `OMNIPUS_CHILD_` prefix (for example `OMNIPUS_CHILD_MYTOOL_TOKEN`). Everything else — saved provider keys, mailbox passwords, the master key, any unrecognised name — is left out. If a feature needs to give a child one specific extra value, that value is named explicitly in the code that starts the child, never passed wholesale. One override exists — **God Mode** (third exception below).

Three named exceptions:

- **Gateway restart.** When the command line (`omnipus start`) or the desktop launcher starts the gateway in the background, the new gateway receives its own data-directory location and, if you started the current process with them, the master-key or key-file value and the web token — those are its own unlock and login values, and without them a headless restart could not open your saved credentials. No other value is forwarded.
- **External CLI workers.** The external command-line workers (Claude Code, Codex, opencode runs inside an isolated workspace) also receive the provider login values their CLI needs — for example `ANTHROPIC_API_KEY` or `CODEX_HOME` — because their job is to authenticate to your model provider. This is the documented runner credential path; the general gateway secrets stay excluded there too.
- **God Mode.** While God Mode is on (Settings → Gateway → Danger zone), the programs your agent's `bash` commands start — foreground or background — and the runs of the `environment_setup` tool receive your **full gateway environment** instead of the allowlist. That includes your saved provider API keys and mailbox passwords. This is an accepted risk: God Mode means full trust, and its documentation says that God Mode programs see your keys. One limit survives even here: the three gateway boot values — the master-key and key-file variables and the web token — are still withheld from these programs. When God Mode is off, and for every other starter (browser, hooks, git, MCP servers, transcoders, the launcher), the allowlist is all a child ever gets.

## Checking an external command-line program

The check behind an external worker's **CLI path** field (`POST /api/v1/system/cli-validate`) runs the program you typed with `--version` to read its version.

Only a signed-in person can use it: the request must come from one of the configured user accounts. Requests made with the command-line token, while development-bypass mode is on, or with the legacy `OMNIPUS_BEARER_TOKEN` when no user account exists are refused with HTTP 403 before the request is read or any program starts. In the editor, the hint under the field then says the check is unavailable; saving still works.

Before starting anything, Omnipus checks the program file. The path must be absolute. Omnipus follows every link on the way to the real file and refuses the path — reason **path-not-allowed**, with a message naming the failed check, nothing started — when:

- the file, or any link on the way, is inside the Omnipus data folder, an agent's working folder, a workspace or a workspace mount (places agents can write);
- the list of those places cannot be read completely — for example a workspace's mount record is unreadable — because then the check cannot tell what is safe;
- on Linux and macOS, another user could replace the program: the file or any folder on the way to it belongs to a user other than root or the account running Omnipus, the file is writable by its group or by other users, or a folder on the way is writable by its group or by other users. Two exceptions: a shared folder with the sticky bit, such as `/tmp`, is allowed when the entry inside it belongs to root or to the account running Omnipus; and on macOS, `/Applications` itself is allowed in its standard form (owned by root, group admin, writable by administrators but not by other users), so programs installed there pass. Folders inside an app are checked normally;
- the file is not a regular program the account running Omnipus can execute.

On Windows the path must be a full drive or network (UNC) path to an `.exe`, `.cmd` or `.bat` file, without short `8.3` names (such as `PROGRA~1`) or alternate data streams. **Windows permissions and junctions are not checked yet:** a Windows program in a folder other users can write is not refused. Use the check on Windows only for programs installed in protected locations.

When the check passes, Omnipus runs the file it checked. An empty path is reported as **missing-binary**. Passing the check does not sandbox the program: external command-line workers run outside the Omnipus sandbox (see [sandbox limitations](operations/sandbox-limitations.md#external-command-line-workers-run-outside-the-omnipus-sandbox)).

## Limits and things to watch

- Security controls reduce risk but do not make every agent action safe. Read approval details before allowing a request.
- **Permissive** sandbox mode observes violations but does not stop them. **Off** removes operating-system protection.
- Kernel-level protection varies by operating system and kernel capability. Trust the status shown on your Security screen for this installation.
- On Linux, basic kernel-level file protection is available from Landlock's first interface, introduced in Linux 5.13. Later file and network rights depend on the detected interface. A kernel version alone does not prove enforcement: check the Security screen's actual status and capabilities. See [sandbox limitations](operations/sandbox-limitations.md).
- The Open filesystem model allows agents to read anything your account can read, except Omnipus's own files and other agents' work (see "Omnipus's own files are protected from every agent," above).
- Personal secret folders outside Omnipus, such as `~/.ssh` and `~/.aws`, are not protected from reading and searching yet (issue [#921](https://github.com/elicify-ai/omnipus/issues/921)). See "What reading, searching and listing can reach" above.
- Secret filtering is best-effort and can be switched off. It only knows registered credential values. Rotate a secret if you think it was exposed.
- Removing a credential is permanent. Services that refer to it may stop working.
- Losing the master key makes the encrypted credential store permanently inaccessible.
- Omnipus overwrites the master key in memory at shutdown and when a command finishes, best-effort only — nothing in this programming environment can guarantee that every copy of a value in memory is erased, so a memory dump taken while the key was in use may still contain it.
- **God-mode**, under **Settings**, **Gateway**, removes global permission prompts and widens shell access. It keeps the configured shell workspace-path guard, operator deny rules, stricter per-agent settings and protected-path application checks. Kernel protection for internal files requires an enforcing sandbox. Under God Mode the programs your shell commands and `environment_setup` runs start see your full gateway environment — provider API keys and mailbox passwords included — though the gateway boot values stay withheld from them. See "God Mode," above, for exactly what changes, the two-switch (allowed vs. on) model, and what happens when you turn it on or off.

## Related pages

- [tools](tools.md) — understand the capabilities controlled by tool policies
- [agents](agents.md) — set restrictions for one agent
- [connectors](connectors.md) — add services whose tokens are stored as credentials
- [settings](settings.md) — manage the rest of the application settings
- [sandbox limitations](operations/sandbox-limitations.md) — read platform-specific operator guidance
