# Security for users

Omnipus can run tools, shell commands, and programs on your machine. You control that access through permissions, the sandbox, encrypted credentials, and an audit log.

## What it is

Security in Omnipus uses several protections together. Tool policies decide whether an agent can call a tool. The sandbox limits what started programs can reach. The credential vault encrypts saved secrets. The audit log records security events, policy decisions, and tool executions.

Secret filtering adds another layer. It replaces registered credential values before selected content reaches a model. This filtering is best-effort and can be switched off. It does not make pasted or unregistered secrets safe.

## When you would use it

Review security settings before agents handle private files or untrusted content. Set stricter tool policies for actions that could change data, spend money, or contact another service.

Use the credential vault when you add an application programming interface key, connector token, or another secret. Review the audit log when you need to understand an allowed, denied, or failed action.

## How to control agent access

1. Open **Settings**, then select **Security**. You see the security health summary and the main protection settings.
2. Under **Agent tool access**, choose **Must ask first (safer)** or **Run freely**. This sets the default behavior for every tool.
3. Turn **Auto-approve** on or off. When it is on, a tool currently set to Ask skips the confirmation prompt for anything the sandbox can confirm stays contained, and still asks for everything else — see "What 'safe' means" below. On a new installation this is **on**.
4. Set **Shell command approval** to **Auto-allow**, **Ask each time**, or **Always deny**. This is the shell tool's own Allow/Ask/Deny setting, separate from the general default in step 2. On a new installation this is **Ask each time** — paired with Auto-approve being on, that combination is what lets safe commands through without a prompt while anything riskier still asks.
5. Open **Advanced / technical details** to set global rules for individual tools. Choose Allow, Ask, or Deny for each tool.
6. Open an agent's **Tools & Permissions** panel when you need a rule for that agent, including a switch to turn Auto-approve off for that one agent. A global Deny, or a global Auto-approve that is already off, cannot be loosened there — an agent-level setting can only add restriction.
7. In a conversation, use the **Auto-approve** switch next to the message box to turn it on or off for that chat only. This is the one place that can turn Auto-approve on even when the agent or global default has it off — you are watching the conversation, so Omnipus accepts that.
8. Review each approval request before you respond. Choose **Approve**, **Always Allow**, or **Deny**. Always Allow remembers that exact command and folder, so an identical call does not ask again in that chat.

**What "safe" means for Auto-approve.** A command only skips the prompt when the sandbox itself can confirm it stays inside the boundaries you have set — both which files it touches and whether it reaches the network. Auto-approve does not judge intent or guess; it only relaxes the prompt when the sandbox can make that guarantee.

For example, with Auto-approve on, an agent reading and editing files inside your project folder runs immediately, because that activity never leaves the sandbox. The same agent running `curl https://example.com` or `git push` still asks, because reaching the network is outside what the sandbox confines. Once you approve one of those, Omnipus remembers it for that chat, so the same kind of request stops asking for the rest of the conversation.

Without an active kernel-level sandbox — see the platform notes below — nothing can be positively confirmed as staying contained, so Auto-approve has no effect and every "Ask" tool prompts every time, the same as if it were off. The chat header shows this as **Auto → Ask**.

These controls answer different questions.

| Control | What it decides | Use it when |
|---|---|---|
| Tool policy | Whether an agent may call a tool | You want Allow, Ask, or Deny for a specific capability |
| Auto-approve | Whether an "Ask" tool skips the prompt for activity that stays inside the sandbox | You want fewer prompts without giving up a check on anything that reaches further |
| Shell command approval | How the shell tool's own Allow/Ask/Deny is set | You want a separate default for shell commands specifically |
| Process sandbox | What started programs may reach | You want operating-system isolation where the platform supports it |
| Filesystem model | What agents may read and run | You want open access or a confined list of locations |
| Shell workspace limit | Whether a command may name paths outside the working folder | You want a command-text check that still applies even when the sandbox is off |

The process sandbox offers three modes. **Enforce** blocks violations. **Permissive** records violations without blocking them. **Off** removes operating-system protection, but it does not disable the shell workspace limit; with the sandbox off, Auto-approve has nothing it can confirm stays contained, so every "Ask" tool prompts regardless of the Auto-approve setting.

The **Confined** filesystem model limits reads and execution to listed locations. The **Open** model lets agents read and run anything your account can reach, apart from Omnipus secret files. Writes remain limited to the workspace and mounted folders. Changes to the filesystem model take effect after a gateway restart.

The Security screen reports the protection available on your current platform. If kernel-level protection is unavailable, Omnipus falls back to checks in the application. A program that ignores those checks is not contained by the operating system.

## Shell command rules

Auto-approve and Shell command approval, above, set a default for shell commands as a whole. Command rules go further: an allow, ask, or deny decision for one specific program, or one specific program plus how it is called. There is no screen for this — you write rules directly into the configuration file. This is a deliberate choice: a rule that can quietly deny or approve real commands is a security control, and Omnipus keeps security controls in a file you own and can review, not behind a settings toggle that could be changed by mistake.

### Where rules live

Rules live in the `sandbox.command_rules` array inside your `config.json`, in the Omnipus data directory. By default that is `~/.omnipus/config.json` in your home folder; if you set the `OMNIPUS_HOME` environment variable, it is `config.json` inside that folder instead. Edit the file with any text editor while Omnipus is stopped, or see "When a rule takes effect" below for editing while it is running.

### The shape of a rule

Each rule is a small object with up to three fields:

| Field | Required | What it means |
|---|---|---|
| `action` | Yes | `allow`, `ask`, or `deny` — same three outcomes as any other tool policy. |
| `binary` | Yes | The program the rule applies to, such as `git`, `npm`, or `rm`. Omnipus matches this against the actual program the shell would run, not just the word your agent typed — so a rule on `git` still applies even if the command reaches `git` through a full path or a shell alias. |
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

A command joined with `&&`, `||`, `;`, `|`, or a line break is checked one part at a time — each part is matched against your rules independently — and the command as a whole gets the strictest result among its parts. One denied part refuses the whole command; failing that, one part that needs to ask makes the whole command ask; only if every part is allowed or matches no rule at all does the whole command proceed without a rule-driven prompt.

Worked example: with the `ask`-before-`npm publish` rule above and no rule at all for `git`, the chained command `git pull && npm publish` asks before running, because its second part needs to ask — even though `git pull` on its own would not have.

### How a rule interacts with Ask, Auto, and God Mode

| Rule action | Ask mode | Auto mode | God Mode |
|---|---|---|---|
| `deny` | Refuses the command. | Refuses the command. | Refuses the command — the one check God Mode does not turn off. |
| `ask` | No extra effect — every command already asks in Ask mode. | Forces an approval prompt for that command, even though Auto would otherwise let it through without one. | No extra effect — God Mode shows no prompts, and an `ask` rule does not create one. |
| `allow` | No extra effect — the command still follows Ask mode's own approval flow. | No extra effect beyond what Auto already does for a command with no matching rule. | No extra effect — God Mode already runs everything without asking. |

A `deny` rule is the one control that reaches into every mode, including God Mode. An `ask` rule only changes anything in Auto mode, where it adds a prompt Auto would not otherwise show. An `allow` rule documents that a command is trusted, but does not currently change what happens beyond what a `deny` or `ask` rule elsewhere would already enforce.

### Command rules compared with one-time approvals

When an approval prompt appears, choosing **Always Allow** remembers that one exact command and folder for the rest of the conversation only — it disappears when the chat ends, and nobody else's conversations are affected. A command rule in `config.json` is the opposite: it applies to every agent, every conversation, permanently, until you edit the file again.

Use a one-time approval for something you only expect to approve in this conversation. Use a command rule for a standing decision — "this team always needs `npm publish` reviewed," or "never run `rm -rf` on this machine, ever" — that should not depend on remembering to click Allow the same way every time.

### Agents cannot change these rules

The whole `sandbox` section of `config.json`, including `command_rules`, is off-limits to an agent's own configuration tool. An agent can read its own security settings so it can explain why it is refusing something, but it cannot loosen or add to them — that stays a change only you make, in the file itself or through a gateway restart.

### When a rule takes effect

*(Placeholder — confirming with the team building this: whether a `command_rules` edit applies to the next command, needs a live-reload, or needs a full gateway restart. This will be filled in once confirmed — do not assume any of the three without that confirmation.)*

### If a rule is written incorrectly

*(Placeholder — Omnipus will validate `command_rules` when it loads and reject an invalid entry with an error naming the bad rule; the exact error wording and where you will see it are still being finalized. This will be filled in once confirmed.)*

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
- **God-mode** under **Settings**, **Gateway** disables the kernel sandbox and outbound-network restrictions for every agent. For tools, it sets the **global** policy to Allow for every tool and removes the permission prompts that come from that global policy — including Auto-approve's, since there is no sandbox left for it to check anything against. It does **not** override an agent's own tool policy: a tool an agent denies itself stays denied, and a tool that agent asks about still asks. Audit logging, prompt protection, and rate limiting remain active. Enabling it requires your password and may require a gateway restart.

## Related pages

- [tools](tools.md) — understand the capabilities controlled by tool policies
- [agents](agents.md) — set restrictions for one agent
- [connectors](connectors.md) — add services whose tokens are stored as credentials
- [settings](settings.md) — manage the rest of the application settings
- [sandbox limitations](operations/sandbox-limitations.md) — read platform-specific operator guidance
