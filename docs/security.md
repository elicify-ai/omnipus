# Security for users

Omnipus agents can run shell commands, read and write files, and use the web on your machine. This page covers the four rails that keep that access in check — permissions, the sandbox, the credential vault, and the audit log — and the one switch that turns most of them off.

## What it is

- **Permissions** decide, tool by tool, whether an agent may run something, must ask you first, or may never run it: **Allow**, **Ask**, or **Deny**.
- **The sandbox** is the operating system itself policing the programs an agent starts. It still stops a program that ignores Omnipus's own rules.
- **The credential vault** keeps every secret — model provider keys, connector tokens — encrypted in one file. Plain settings files hold only a reference, never the secret.
- **The audit log** records what agents ran, what was allowed or refused, and who changed which security setting.

## When you would use it

Set permissions when you decide how much freedom your agents get; meet the Ask setting whenever an agent wants something you have not pre-approved. Add secrets when you connect a model provider or a [connector](connectors.md); check the audit log when you want to know what happened while you were away. The Security tab opens with a health score and a link from each finding to its fix.

## How to set what agents may do

1. Open **Settings** and pick the **Security** tab.
2. Under **Protection settings**, choose **Agent tool access**: "Must ask first (safer)" or "Run freely". This is the default for every tool you have not set individually. The same section has **Shell command approval**: auto-allow, ask each time, or always deny.
3. To set one [tool](tools.md) at a time, expand **Advanced / technical details** and open **Tool Access — Global Policies**. Every tool in the grid has an Allow, Ask, or Deny control.
4. To tighten one agent only, open that [agent's](agents.md) **Tools** tab. An agent-specific rule can restrict below the global setting, never loosen it. The stricter of the two wins.
5. When a tool is set to Ask, a dialog appears while the agent works. Choose **Allow** (this once), **Always Allow** (remembers these exact arguments), or **Deny**. Keyboard focus starts on Deny, and closing the dialog counts as Deny.

There is no hidden third layer: a tool resolves from the global setting unless an agent rule restricts it further. Removing a setting does not deny the tool — the global default applies again. To lock one down, set Deny explicitly.

## What the sandbox does, and what it does not

The sandbox has three modes, under Settings, Security, Advanced, Process Sandbox:

- **Enforce** — the operating system itself stops a program that reaches outside what you allowed.
- **Permissive** — every violation is written to the audit log, but nothing is blocked. Useful for seeing what enforcing would break.
- **Off** — no operating-system protection at all. Development only.

A separate setting, the **filesystem model**, decides what an agent may read and run. Neither option changes what it may write: writes stay inside the workspace and any folders you have mounted.

- **Open** (the default) — agents can read and run anything on this machine, except Omnipus's own secrets.
- **Confined** — agents can only read and run things in places you have listed. Safer, and more likely to break a tool that needs a file you did not anticipate.

What you actually get depends on your operating system:

| Operating system | Confined | Not confined |
|---|---|---|
| Linux, kernel 5.13 or newer | The gateway and every program an agent starts, at the kernel level | In the Open model, agents can read anything you can read, apart from Omnipus's own secret files |
| macOS | The programs an agent starts | The Omnipus gateway process itself |
| Windows, and older Linux kernels | Nothing at the operating-system level | Enforcement is application-level only: Omnipus refuses in its own code, but a program that ignores that is not contained |

## The credential vault and your master key

Every secret you give Omnipus is encrypted with AES-256-GCM, a standard and widely reviewed cipher, and stored in one file on your machine. When you configure a connector, its token lands in the vault automatically. Stored secrets are scrubbed from agent output and the audit log.

The vault is unlocked by a master key. On first start, Omnipus creates it as a file named `master.key` in its data folder (`~/.omnipus` by default) and prints a warning to back it up. Lose the master key and every stored credential is permanently inaccessible. There is no recovery.

In Settings, Security, Credential Vault, you can add a key, remove one, or rotate the master key. Every change asks you to re-type your password first.

## The audit log

The audit log is on by default. It records each tool call with its decision, shell commands, file operations, and security setting changes with the user and the old and new values. Open it from Settings, Security, Advanced, Audit Log, View Log. It refreshes every 30 seconds and filters by event and decision. Each entry is cryptographically sealed to the one before it, so the viewer can show "Chain verified" — and "Chain broken" at the exact entry where the file was edited. On disk, the log lives at `~/.omnipus/system/audit.jsonl`.

## God mode

God mode is one switch in Settings, Gateway, Danger zone. Turning it on removes all permission prompts — every Ask becomes Allow, for every agent — and disables the kernel sandbox, the outbound-network restrictions, and the shell guard. Audit logging, the prompt-injection defense, and rate limiting stay on.

Changing it requires re-typing your password. The first enable needs a gateway restart; until then you can cancel the authorization, and every toggle is audit-logged. While active, a red banner says so at the top of the Gateway tab.

## Limits and things to watch

- On Windows and Linux kernels older than 5.13, there is no operating-system sandbox — only the rules Omnipus enforces in its own code.
- On macOS, the programs agents start are confined, but the Omnipus gateway itself is not.
- The Open filesystem model trades safety for working tools: anything you can read, your agents can read. Permission to read is not secrecy — a misled agent can read a file and post its contents to a connector.
- Sandbox Off disables the operating-system checks but not every rule: the shell workspace limit and the blocked command patterns are separate settings and stay as configured.
- Removing a tool's setting restores the global default; it does not deny the tool.
- A lost master key cannot be reconstructed. Back it up when Omnipus first creates it.

## Related pages

- [tools](tools.md) — the catalog of built-in tools these permissions apply to
- [agents](agents.md) — each agent's Tools tab, where per-agent restrictions live
- [connectors](connectors.md) — connecting chat platforms, whose tokens are stored in the vault
- [settings](settings.md) — the rest of the Settings screen
- [sandbox limitations](operations/sandbox-limitations.md) — operator-level detail on what the sandbox cannot do
