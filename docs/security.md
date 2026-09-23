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
2. Under **Agent tool access**, choose **Must ask first (safer)** or **Run freely**. This sets the default behavior.
3. Set **Shell command approval** to **Auto-allow**, **Ask each time**, or **Always deny**.
4. Open **Advanced / technical details** to set global rules for individual tools. Choose Allow, Ask, or Deny for each tool.
5. Open an agent's **Tools** tab when you need a rule for that agent. A global Deny cannot be loosened there.
6. Review each approval request before you respond. Choose **Approve**, **Always Allow**, or **Deny**. Always Allow remembers the exact arguments for that call.

These controls answer different questions.

| Control | What it decides | Use it when |
|---|---|---|
| Tool policy | Whether an agent may call a tool | You want Allow, Ask, or Deny for a specific capability |
| Shell command approval | How shell commands are handled | You want a separate default for commands |
| Process sandbox | What started programs may reach | You want operating-system isolation where the platform supports it |
| Filesystem model | What agents may read and run | You want open access or a confined list of locations |
| Shell workspace limit | Whether commands may name paths outside the working folder | You want a command-text check separate from the sandbox |

The process sandbox offers three modes. **Enforce** blocks violations. **Permissive** records violations without blocking them. **Off** removes operating-system protection, but it does not disable the shell workspace limit or blocked command patterns.

The **Confined** filesystem model limits reads and execution to listed locations. The **Open** model lets agents read and run anything your account can reach, apart from Omnipus secret files. Writes remain limited to the workspace and mounted folders. Changes to the filesystem model take effect after a gateway restart.

The Security screen reports the protection available on your current platform. If kernel-level protection is unavailable, Omnipus falls back to checks in the application. A program that ignores those checks is not contained by the operating system.

## How to manage saved secrets

1. Open **Settings**, then select **Security**.
2. Find **Credential Vault** and select **Add key**.
3. Enter a key name and its value, then select **Save**.
4. Re-type your password if Omnipus asks you to confirm the change.
5. To remove a saved secret, select its remove button and confirm **Remove**.
6. To replace the vault's master protection, select **Rotate master key**, enter a new passphrase, and select **Rotate**.

Omnipus stores credential values in an encrypted file. Settings refer to credentials by name instead of storing their plain values. On a fresh installation, Omnipus creates a master key and warns you to back it up. If you lose that key, existing credentials cannot be recovered.

Rotation re-encrypts the whole vault with the new passphrase. Back up the new passphrase because Omnipus needs it to unlock the vault later.

Each stored value is encrypted together with the name it is stored under, so a value cannot be moved from one entry to another and still open.

Installations from an earlier release are upgraded automatically, once. The first time the upgraded gateway (or any `omnipus` command) opens the vault, it re-encrypts every entry with its name, using the same master key, and saves the whole file in one step. You do not re-enter anything. The upgrade is recorded in the audit log as `credentials.store_migrated`, with the number of entries and no names or values. After it, the vault never reads the old format again.

What the upgrade cannot protect against. The old format did not record which name a value belonged to, so a swap made to an old-format vault file cannot be detected. For example, someone who can write to your Omnipus home folder swaps your model provider key with another key; the upgrade cannot tell, and keeps the swap.

This risk does not end at the first start. It lasts as long as your vault is still opened by the same master key or passphrase it had before the upgrade. Anyone who can write to your Omnipus home folder and still holds a copy of the pre-upgrade vault file can, at any later time:

1. swap values inside that old copy,
2. delete the upgrade record `credentials.json.migrated` (it sits in the same folder, so the same person can delete it), and
3. put the old copy back.

The next start then upgrades it again and keeps the swap. The upgrade record only stops accidental or careless restores.

What closes it:

| How your vault is opened | What to do after upgrading |
|---|---|
| Passphrase typed at start-up | Run `omnipus credentials rotate` and choose a new passphrase. The upgrade already re-keys the vault under fresh random data, so an old copy cannot be mixed with anything saved after the upgrade. Only changing the passphrase stops a whole old copy from opening. |
| Key file (`master.key`, `OMNIPUS_KEY_FILE`) or `OMNIPUS_MASTER_KEY` | Change the master key. There is no command yet that does this while keeping your stored values. The supported way is to stop the gateway, back up and remove the old key, create a new one, remove `credentials.json`, start again, and re-enter each credential. Until then, protect old copies of `credentials.json` (backups, sync folders) as carefully as the key itself. `omnipus credentials rotate` also works, but it switches the installation to a passphrase you must type at every start, which does not suit an unattended server. |

A later release will remove the upgrade step entirely. From then on an old-format vault is refused outright, and this risk ends for everyone.

If any entry cannot be opened during the upgrade, nothing is changed and the gateway does not start. The error names each entry at fault. See [troubleshooting](troubleshooting.md#credentials-stop-working-after-an-upgrade).

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
- Omnipus overwrites the master key in memory at shutdown and when a command finishes, best-effort only — nothing in this programming environment can guarantee that every copy of a value in memory is erased, so a memory dump taken while the key was in use may still contain it.
- **God-mode** under **Settings**, **Gateway** disables the kernel sandbox, outbound-network restrictions, and shell guard for every agent. For tools, it sets the **global** policy to Allow for every tool and removes the permission prompts that come from that global policy. It does **not** override an agent's own tool policy: a tool an agent denies itself stays denied, and a tool that agent asks about still asks. Audit logging, prompt protection, and rate limiting remain active. Enabling it requires your password and may require a gateway restart.

## Related pages

- [tools](tools.md) — understand the capabilities controlled by tool policies
- [agents](agents.md) — set restrictions for one agent
- [connectors](connectors.md) — add services whose tokens are stored as credentials
- [settings](settings.md) — manage the rest of the application settings
- [sandbox limitations](operations/sandbox-limitations.md) — read platform-specific operator guidance
