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
2. Open **Advanced / technical details**, then **Tool Access — Global Policies**. Choose Allow, Ask or Deny for each tool. This applies to every agent. Saving asks you to re-type your password.
3. To tighten one agent, open that agent's **Tools & Permissions** panel. An agent's setting can only be stricter than the global one, never looser. See [tools](tools.md) for how the two combine.

On a new installation the shell tool, `bash`, is set to **Ask**.

### Turn Auto-approve on or off

Auto-approve can be set in three places. On a new installation it is **on** globally.

| Where | What it does | Example |
|---|---|---|
| **Settings → Security → Auto-approve** | The default for every agent and chat. Changing it asks you to confirm and re-type your password. | You turn it off. Every tool set to Ask now prompts every time, in every chat. |
| An agent's **Tools & Permissions** panel → **Never auto-approve for this agent** | Turns Auto-approve off for that one agent. It can only turn it off, never on. | A finance agent should always ask. You tick the box; its Ask tools always prompt. |
| The **Auto** switch next to the message box in a chat | Turns Auto-approve on or off for that chat. You can flip it before typing anything — a brand-new chat has no conversation yet, so the switch records your choice and applies it from your very first message, before any tool call in that first turn runs. In a chat that is already running, a flip applies from the agent's very next tool call, not just your next typed message — even mid-turn, while the agent is still working. A prompt that is already open when you flip the switch is not resolved for you; it has already asked, and stays open until you answer it. | Auto-approve is off globally, but you are watching this chat closely. You switch it on before typing your first message, and that first message already runs under it. In a chat that is already going, you switch it on mid-turn and the agent's next tool call already runs under it. |

On the Security screen, the switch carries this description:

> Tools set to “Ask” run without a prompt when it's safe: they stay inside your workspace and the sandbox.

Below the switch, **Still asks every time** is a collapsed list — click it to expand. It groups the fixed list of tools that always ask, even with Auto-approve on:

> Sending email · Deleting tasks, agents or workspaces · Installing skills, setting up an environment or publishing a web preview · Changing or testing settings, providers, channels, agents or skills · Running diagnostics · Adding or removing connected (MCP) servers, and MCP tools not marked safe · Browser scripts and uploads · Mounting a folder, or files outside your workspace

And a small note underneath *(2026-09-24: the note's wording changed — see "Auto-approve and the kernel sandbox" below)*:

> Without a kernel sandbox (for example on Windows), shell commands are checked by reading the command text only.
<!-- verify-ui-string -->

That is a summary. The full list of tools that still ask, one row per tool, is under "What Auto-approve runs and what still asks" below.

The chat switch is the one place that can turn Auto-approve **on** when the agent or the global default has it off, because you are present in that conversation. It has one limit: when the chat's agent hands work to another agent (a delegate), and that other agent has **Never auto-approve** ticked, the delegate's own switch wins. Your chat switch covers the agent you are talking to, not a delegate that was set to always ask.

### Auto-approve and the kernel sandbox (changed 2026-09-24)

**Auto-approve no longer needs a kernel sandbox to work.** A kernel sandbox is protection enforced by the operating system itself: Landlock on Linux, Seatbelt on macOS, with the Process Sandbox set to **Enforce**. Until 2026-09-24, Auto-approve had no effect at all without one — every Ask tool prompted, the same as if Auto-approve were off, and there was no Auto-approve on Windows at all, because Windows has no kernel sandbox in Omnipus. That turned out to make Auto-approve effectively useless on Windows and on any machine where the sandbox failed to start or was deliberately left in Permissive or Off mode, so it was changed.

**What Auto-approve does now, with and without a kernel sandbox:**

- **Every tool except the shell (`bash`)** is unaffected by whether a kernel sandbox is active. The kernel sandbox was never what kept `write_file`, `read_file` and the rest inside your workspace — an application-level check inside Omnipus itself always did that (see "What Auto-approve runs and what still asks" below), and that check runs the same way with or without a kernel sandbox.
- **The shell (`bash`) is different.** With a kernel sandbox active (Linux or macOS, Process Sandbox set to Enforce), a shell command that tries to reach outside the workspace or the network is caught two ways: Omnipus checks the command's text before running it, **and** the operating system itself blocks anything that check missed. Without a kernel sandbox — on Windows, or with the Process Sandbox set to Permissive or Off — only the first check runs. Omnipus still reads the command's text and still asks before anything that looks like it reaches outside the workspace or the network, but there is no second, operating-system-level check behind that first one.
  - **Concrete risk:** a command that has been deliberately disguised so its text does not look like what it actually does — for example, splitting a program name across quotes, or using shell substitution to build the real command at run time — can slip past a text-based check. With a kernel sandbox, the operating system still blocks it regardless of how the text was disguised. Without one, nothing else is checking, and the command runs.

This is a deliberate, founder-approved trade: Auto-approve being usable everywhere, in exchange for a real but scoped gap on machines with no kernel sandbox. If that risk matters for a given agent or workspace, keep the Process Sandbox set to **Enforce** where your platform supports it, or turn Auto-approve off for that agent.

### What the chat header shows

The badge at the top of a chat shows which state applies to that chat right now.

| Badge | Meaning |
|---|---|
| **God Mode** | God Mode is on. No approval prompts, no sandbox. |
| **Ask** | Auto-approve is off for this chat. Every tool set to Ask prompts. |
| **Auto** | Auto-approve is on and a kernel sandbox is active. Safe calls run; the rest ask. |
| **Auto — no sandbox** | Auto-approve is on, but there is no active kernel sandbox. Safe calls still run; the shell's checks are text-only (see above). The badge's tooltip explains this. |
<!-- verify-ui-string: badge label "Auto — no sandbox" and its tooltip text -->

*(Before 2026-09-24 this badge read "Auto → Ask" and Auto-approve had no effect at all in that state. That is no longer how it works — see "Auto-approve and the kernel sandbox" above.)*

In an agent's **Tools & Permissions** panel and in **Tool Access — Global Policies**, each tool set to Ask carries a small marker showing what Auto-approve does with it:

| Marker | Meaning |
|---|---|
| **Auto: runs** | Runs without a prompt. |
| **Auto: runs inside workspace** | Runs without a prompt when its file is inside the workspace or a mounted folder; otherwise asks. |
| **Auto: asks** | Always asks. |

Tools set to Allow or Deny show no marker, because Auto-approve does not affect them. The shell tool, `bash`, shows no marker either, because it has its own checks (below). Tools from connected servers get a marker too, based on how their server labels them.

### What Auto-approve runs and what still asks

Auto-approve only matters for a tool set to **Ask**. Omnipus judges each call on its own, not the tool as a whole.

**Most tools run without a prompt.** That includes reading and searching your workspace, web search and opening web pages, the browser (clicking, typing, navigating, screenshots), memory and the knowledge base, tasks, plans and goals, handing work to another agent, and read-only listings of settings, agents and workspaces. For example, with Auto-approve on, Mia editing a knowledge-base note set to Ask no longer shows an approval card.

**File tools run only inside the workspace or a mounted folder.** `read_file`, `list_directory`, `write_file`, `edit_file` and `append_file` run without a prompt when the path they touch is inside the agent's workspace or a folder you mounted into it. Anything outside asks, **including reads**. For example, `write_file` to `notes/plan.md` runs; `read_file` of `/etc/hosts` asks. The same rule applies to the file `send_file` sends. `browser_screenshot` saves its picture into the workspace under a name Omnipus picks, so it normally runs. Omnipus secret files, such as the master key, are never covered.

This is stricter than the shell. Under Auto-approve, the shell command `cat /etc/hosts` runs, because the sandbox lets commands read outside the workspace, while `read_file /etc/hosts` asks. That difference is deliberate.

**Messages and files sent to chat channels go out with no prompt.** `send_message` runs under Auto-approve, and so does `send_file` for a file inside the workspace or a mounted folder. A message, or a file from the workspace, can leave the machine to Telegram, Slack or another connected channel without anyone approving it. If that is not acceptable for an agent, set those two tools to Ask and tick **Never auto-approve** for that agent, or set them to Deny. Email is different: `send_email` and `reply` always ask.

**These 28 tools always ask, even with Auto-approve on:**

| Group | Tools |
|---|---|
| Widening file access | `request_mount` |
| Installing and publishing | `install_skill`, `environment_setup`, `serve_web` |
| Email | `send_email`, `reply` |
| Deleting | `delete_task`, `delete_task_in_workspace`, `delete_workspace`, `delete_agent` |
| Browser scripts and uploads | `browser_evaluate`, `browser_upload_file` |
| Settings and diagnostics | `set_config`, `run_doctor` |
| Providers | `configure_provider`, `test_provider` |
| Channels | `enable_channel`, `disable_channel`, `configure_channel`, `test_channel` |
| Connected servers | `add_mcp_server`, `remove_mcp_server` |
| Agents and workspaces | `create_agent`, `update_agent`, `update_workspace` |
| Skills | `create_skill`, `edit_skill`, `remove_skill` |

**Tools from connected servers (MCP) mostly still ask.** A connected server can label each of its tools. Under Auto-approve, a server tool runs only when its server labels it read-only or explicitly not destructive. Every other server tool asks, including one with no label at all. Most servers send no labels today, so in practice most server tools still ask. Omnipus trusts these labels because you chose to connect the server, and adding a server is on the always-ask list.

**The shell tool, `bash`, has its own checks.** Under Auto-approve, a command runs without a prompt when Omnipus's own checks (see "Auto-approve and the kernel sandbox" above) find nothing that needs asking about, and — where a kernel sandbox is active — the sandbox could also contain it. It still asks when the command:

- writes outside the workspace and its mounted folders. For example, `cp report.pdf /Users/you/Desktop/` asks; `cp report.pdf out/` runs;
- may need the network. For example, `curl https://example.com`, `git push` or `npm install` ask. Omnipus recognises this from the program (such as `git`, `curl`, `wget`, `ssh`, `npm`, `pip`, `docker`, `gh` and the main cloud command-line tools) or from a web address in the command. Because it goes by the program, even `git status` asks the first time in a chat;
- matches an `ask` command rule you wrote (see "Shell command rules" below).

Under Auto-approve, a command that would touch an Omnipus secret file, such as the master key, is refused outright and never offered for approval. **Approve Once** on one of these prompts lets that one command through. **Always Allow** on a network prompt lets the shell reach the network for the rest of that chat, on the usual web and name-lookup ports (80, 443 and 53) and to any host. **Always Allow** on a file prompt opens that one path, for the access the command needed, for the rest of that chat. Denying either prompt refuses the command; it never runs with less access instead.

Auto-approve does not check what a command means. A command that deletes files inside your workspace, or stops a process, runs without a prompt, because it stays inside the sandbox. Use a `deny` or `ask` command rule for commands like that.

### Scheduled and unattended runs

A scheduled task runs with nobody present to answer a prompt. The same rule applies there as in a chat: anything Auto-approve would run in a chat also runs in a scheduled run. Anything that would need a person is refused straight away, with a clear error in the task's transcript:

```
Not run: this is a headless scheduled run with no operator available to approve ask-policy tools.
```

A shell command that would need a prompt (for example one that needs the network) is refused the same way; its error ends with `auto-denied: no operator attached to this headless/scheduled run to approve it`.

For example, with Auto-approve on, a scheduled agent can write its report into the workspace, but a `delete_task` call is refused. If a scheduled agent must use a tool that still asks, set that tool to Allow for that agent. See [tools](tools.md).

### Approve a request

When an approval card appears, read what it says, then choose:

| Button | What it does |
|---|---|
| **Approve Once** | Runs this one call. Nothing is remembered; the next identical call asks again. |
| **Always Allow** | Runs the call and remembers it for the rest of this chat, so an identical call does not ask again. |
| **Deny** | Refuses the call. |
| **Cancel** | Closes the request without running it. |

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

The process sandbox offers three modes. **Enforce** blocks violations. **Permissive** records violations without blocking them. **Off** removes operating-system protection, but it does not disable the shell workspace limit. Only Enforce counts as an active kernel sandbox. *(Changed 2026-09-24 — see "Auto-approve and the kernel sandbox" above.)* In Permissive or Off, the chat header shows **Auto — no sandbox**: Auto-approve still runs, but shell commands are checked by their text only, with no operating-system check behind that.

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

A command like `rm -rf /tmp/build` matches both rules — the narrower `/tmp`-scoped allow rule and the broader deny rule. The deny rule wins and the command is refused, even though the allow rule looks more specific to this exact case. Writing a narrower allow rule is never a way to carve an exception past a broader deny rule; if you want that exception, the deny rule itself has to be narrowed instead.

### Chained commands

A command joined with `&&`, `||`, `;`, `|`, or a line break is checked one part at a time. Each part is matched against your rules on its own, and the command as a whole gets the strictest result among its parts. One denied part refuses the whole command. Failing that, one part with an `ask` rule makes the whole command ask. Parts with no rule at all fall back to the tool policy and Auto-approve as usual.

Worked example: with the `ask`-before-`npm publish` rule above and no rule at all for `git`, the chained command `git pull && npm publish` asks before running, because its second part needs to ask — even though `git pull` on its own would not have.

### How a rule interacts with Auto-approve and God Mode

The columns below match the badge at the top of the chat. **Auto** also covers **Auto — no sandbox**: a command rule's `allow`/`ask`/`deny` decision applies the same whether or not a kernel sandbox is active — it is a check on the command's text, not something the kernel sandbox does or does not add. *(Before 2026-09-24, this row said "Ask also covers Auto → Ask" — Auto-approve had no effect without a sandbox back then, so that state behaved like Ask. It no longer does.)*

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

## How to manage saved secrets

1. Open **Settings**, then select **Security**.
2. Find **Credential Vault** and select **Add key**.
3. Enter a key name and its value, then select **Save**.
4. Re-type your password if Omnipus asks you to confirm the change.
5. To remove a saved secret, select its remove button and confirm **Remove**.
6. To replace the vault's master protection, select **Rotate master key**, enter a new passphrase, and select **Rotate**.

Omnipus stores credential values in an encrypted file. Settings refer to credentials by name instead of storing their plain values. On a fresh installation, Omnipus creates a master key and warns you to back it up. If you lose that key, existing credentials cannot be recovered.

Rotation re-encrypts the whole vault with the new passphrase. Back up the new passphrase because Omnipus needs it to unlock the vault later.

## How to review security activity

1. Open **Settings**, select **Security**, then open **Advanced / technical details**.
2. Find **Audit Log** and select **View Log**.
3. Filter entries by event or decision to narrow the list.
4. Check the chain status. **Chain verified** means the displayed log passed its integrity check.

The viewer refreshes every 30 seconds. The log includes security events, policy decisions, and tool executions. It can show security-setting changes with their previous and new values.

## Limits and things to watch

- Security controls reduce risk but do not make every agent action safe. Read approval details before allowing a request.
- **Permissive** sandbox mode observes violations but does not stop them. **Off** removes operating-system protection.
- Kernel-level protection varies by operating system and kernel capability. Trust the status shown on your Security screen for this installation.
- On Linux, the kernel version decides which sandbox rights you get. Kernels 5.19 and newer give kernel-level file protection; 5.13–5.18 run application-level checks only and are not recommended. When Omnipus falls back, it reports the fallback on the Security screen and logs a `sandbox.degraded` warning. See [sandbox limitations](operations/sandbox-limitations.md).
- The Open filesystem model allows agents to read anything your account can read, except protected Omnipus secret files.
- Secret filtering is best-effort and can be switched off. It only knows registered credential values. Rotate a secret if you think it was exposed.
- Removing a credential is permanent. Services that refer to it may stop working.
- Losing the master key makes the encrypted credential store permanently inaccessible.
- **God-mode** under **Settings**, **Gateway** disables the kernel sandbox and outbound-network restrictions for every agent. For tools, it sets the **global** policy to Allow for every tool and removes the permission prompts that come from that global policy — including Auto-approve's: Auto-approve only ever matters for a tool set to Ask, and God Mode leaves none set to Ask globally, and is also explicitly switched off in God Mode regardless *(as of 2026-09-24, this is no longer "because there is no sandbox left" — Auto-approve does not need a sandbox any more; God Mode turns it off by its own design, D1)*. It does **not** override an agent's own tool policy: a tool an agent denies itself stays denied, and a tool that agent asks about still asks. Audit logging, prompt protection, and rate limiting remain active. Enabling it requires your password and may require a gateway restart.

## Related pages

- [tools](tools.md) — understand the capabilities controlled by tool policies
- [agents](agents.md) — set restrictions for one agent
- [connectors](connectors.md) — add services whose tokens are stored as credentials
- [settings](settings.md) — manage the rest of the application settings
- [sandbox limitations](operations/sandbox-limitations.md) — read platform-specific operator guidance
