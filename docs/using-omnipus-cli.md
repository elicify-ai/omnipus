# Using the Omnipus command line

Omnipus ships as a single binary called `omnipus`. This page covers the terminal half: install, setup, one-shot tasks, secrets, and running it as a server. Prefer a graphical interface? Start with [Using Omnipus from the web UI](using-omnipus-ui.md).

## What it is

One binary, a small set of commands. `omnipus <agent> "<prompt>"` runs a one-shot task. The named commands: `onboard` (first-time setup), `start` and `stop` (the server), `credentials` (secrets), `doctor` and `audit` (health and the security log), `browser` (managed Chrome), `records` with `import-obsidian` and `stamp-ids` (notes — see [the knowledge base](knowledge.md)), `export` and `import` (move one custom agent — see [below](#how-to-move-an-agent-to-another-install)), `package inspect` and `package verify` (look inside a package file), and `version`.

## How to install the binary

1. Run the installer:

   ```bash
   curl -sSL https://raw.githubusercontent.com/elicify-ai/omnipus/main/scripts/install.sh | sh
   ```

   The script downloads the binary into `/usr/local/bin`.

2. No write access there? Install into your home directory instead:

   ```bash
   OMNIPUS_INSTALL_DIR="$HOME/.local/bin" curl -sSL https://raw.githubusercontent.com/elicify-ai/omnipus/main/scripts/install.sh | sh
   ```

3. Confirm with `omnipus version`, which prints the build version.

Other platforms: see the [project README](../README.md#install).

## How to set up without the web wizard

The first run needs an AI provider, an API key, and a default model. When you run Omnipus yourself, it also needs an admin account. The command collects those:

1. Start it:

   ```bash
   omnipus onboard
   ```

2. Pick a provider from the menu:

   ```text
   Select your LLM provider:
     1) OpenRouter
     2) Anthropic
     3) OpenAI
     4) Google Gemini
     5) Groq
     6) DeepSeek
     7) Other (enter provider id)
   Choice [1]:
   ```

3. Enter your API key. Input is hidden, and the wizard checks the key against the provider, asking again if it is rejected.

4. Enter a model, or press Enter to accept the provider's default: the first model in its catalog that can use tools.

5. When you run Omnipus yourself, enter an admin username and a password of at least 8 characters. A hosted or desktop install does not ask — you sign in with your Omnipus account in the app instead. When the command prints `Onboarding complete.`, you are done.

On an already-configured system it prints `Onboarding is already complete; nothing to do.` A fresh install can also be finished in the browser. That wizard collects the same provider and, when you run Omnipus yourself, the same admin account. It also asks for your name, tone, and level of detail, which this command does not.

### Headless setup — no prompts

Pass every answer as a flag:

```bash
omnipus onboard --non-interactive \
  --provider openrouter \
  --api-key 'sk-or-v1-...' \
  --model 'z-ai/glm-5v-turbo' \
  --admin-username admin \
  --admin-password 'choose-a-strong-one'
```

To keep secrets out of your shell history, feed them through stdin instead:

```bash
printf 'sk-or-v1-...\nchoose-a-strong-one\n' | omnipus onboard --non-interactive \
  --provider openrouter \
  --api-key-stdin \
  --admin-username admin \
  --admin-password-stdin
```

## How to start and stop the server

1. Start Omnipus:

   ```bash
   omnipus start
   ```

   The web app, the API, and agent previews all load from **one port, 5000 by default**. There is no second port. Before the server comes up, it prints where to connect:

   ```text
   Open the dashboard / log in as admin:
     This machine:   http://localhost:5000
     Hint: bound to localhost — set gateway.host=0.0.0.0 to allow other devices
   ```

2. To reach Omnipus from other devices, set `gateway.host` to `0.0.0.0`; the banner then lists them too.

3. Stop it from another terminal:

   ```bash
   omnipus stop
   ```

   You see `Omnipus gateway stopped.` — or `No Omnipus gateway is running.`

`omnipus start` runs in the foreground, which suits a systemd service. The older spelling `omnipus gateway` still works as an alias. For HTTPS behind nginx or Caddy, see [Reverse proxy setup](operations/reverse-proxy.md); for containers, [Running with Docker](docker.md).

Useful flags:

| Flag | What it does |
|---|---|
| `-d`, `--debug` | Verbose logs |
| `-T`, `--no-truncate` | Stop long values being cut in debug logs; requires `-d` |
| `--sandbox <mode>` | Override the sandbox mode for this run: `enforce`, `permissive`, or `off` |

## How to run a one-shot task from the terminal

One-shot tasks need the server running.

1. Send a task to a named agent:

   ```bash
   omnipus jim "Summarize the open GitHub issues in elicify-ai/omnipus"
   ```

2. To use a different model for this one turn, add `--model`:

   ```bash
   omnipus mia --model openrouter/glm-5.2 "Draft a welcome email"
   ```

The reply streams to stdout; tool activity and progress go to stderr, so you can redirect cleanly:

```bash
omnipus jim "List the top 5 files by size" > files.txt
```

Run `omnipus` with no arguments to list the agents you can address, by ID and name. On a fresh install that list is `mia`, `jim` and `omnipus`. Ava and Admin are retired and are not installed. A worker is not in that list: addressing one prints `error: "<id>" is a worker agent and cannot be run directly`.

Some tools, such as an agent's shell, require approval. Without `--yes` they are denied and the run continues; with it, approvals are granted for this run only:

```bash
omnipus jim --yes "Run the test suite and fix failures"
```

## How to manage API keys and secrets

Secrets live in an encrypted credential vault. Values are never written in plain text and never printed back.

```bash
omnipus credentials set OPENAI_API_KEY sk-...
omnipus credentials set TELEGRAM_BOT_TOKEN 123456:ABC...
omnipus credentials list          # names only
omnipus credentials delete OPENAI_API_KEY   # asks to confirm
omnipus credentials rotate        # new passphrase
```

The credential store is locked with a master key. Keep a backup of it — without it, the secrets cannot be recovered. See [Security for users](security.md). On unattended servers, supply the master key as an environment variable instead of a passphrase:

```bash
export OMNIPUS_MASTER_KEY=<64-hex-char-key>
omnipus start
```

## How to check configuration health

```bash
omnipus doctor        # common security and safety issues, in plain language
omnipus audit         # the security and activity log
omnipus audit verify  # verify the log has not been tampered with
```

Run `omnipus doctor` after any big configuration change, and before exposing the server to the internet.

## How to move an agent to another install

You can pack **one custom agent** into a single file, copy that file to another Omnipus you run yourself, look inside it, and add the agent there. The file is a package (`.omnpkg`). The agent on the new install is a new agent: it gets a new id, and it does not replace anyone who is already there.

There is no screen for this, and no agent can do it for you. You use the commands below, or the three endpoints named on the [Agents](agents.md#how-to-create-your-own-agent) page. What is inside the file, and what changes on the way in, is on [Agents](agents.md#how-to-move-an-agent-to-another-install).

These commands work only when you run Omnipus yourself, the same limit as Backup and Restore. A hosted or desktop install does not offer them.

Exports, imports and start-up recovery are recorded in the audit log while audit logging is on, which is the default (`sandbox.audit_log`). If you turned that off, they are not recorded and they are not refused.

A stopped server is started for you, the same way as for a one-shot task, when the master key is already available (see [Limits and things to watch](#limits-and-things-to-watch)). `export` and `import` talk to that server. They do not edit the data folder. The only local reads are the CLI token and the gateway address; the server does the export or the import. `package inspect` and `package verify` read only the file you name: they need no server and they write nothing.

### Export one agent

```bash
omnipus export --agent Writer -o writer.omnpkg
```

`--agent` takes an id or a name. An exact id is used first. A name matches ignoring letter case. If the name matches more than one agent, the command refuses and lists their ids. If it matches none, it names what you typed and writes no file.

| Flag | What it does |
|---|---|
| `--agent <id\|name>` | Required. The one custom agent to pack. |
| `-o <file>` | Required. Where to write the package. |
| `--force` | Replace an existing file at `-o`. Without it, an existing file is left byte for byte and the command stops. |

You see a progress line, for example `Exporting Writer…`. When that export finishes, the success line is:

```text
Exported "Writer" (1 agent, 3 files, 4.2 KB) to writer.omnpkg
```

That is the success line for this example: one agent, three files, that size. Every package in this version holds one agent. Every export then prints:

```text
SOUL.md and AGENT.md are copied as they are; check them for pasted secrets before sharing.
```

That notice is the limit: a key or password pasted into those files travels with them. Export does not open `credentials.json`, `master.key`, `cli.token`, `auth.json` or `config.json`, and a value that lives only in those files is not in the package. It also does not contain an external worker's program path, environment values or extra arguments: an external worker is packed with those removed, and only the names of its environment overrides are listed as secrets to type again. An external worker has no prompt, so its package holds no `SOUL.md`.

With `--force`, the old file is replaced only when the new one is complete. A transfer that stops halfway leaves the previous file as it was.

A refusal prints `error:` and a reason, and leaves no new file (an existing `-o` file is untouched). Built-in agents cannot be exported: the refusal says they cannot be exported. That covers the shipped agents `mia`, `jim`, `omnipus`, `planner`, `researcher`, `worker` (General Purpose), `judge` and `plansupervisor`. A custom agent that is merely *named* like one of them, for example a custom agent named Mia, can be exported. A native agent with a blank prompt is refused (`agent_prompt_missing`). A worker with an empty description is refused (`element_not_creatable`). A prompt or instructions file that is a link, not a normal file, unreadable, or over 1 MiB is refused (`agent_file_unreadable`); a link is not followed.

### Look inside a package without installing it

Neither command needs a running server, and neither writes anything.

```bash
omnipus package inspect writer.omnpkg
omnipus package verify writer.omnpkg
```

`inspect` reads only the manifest. It shows the format version, the build that wrote the file, when it was created, the elements, each file with its size and checksum, and any secrets the package says must be typed again. It ends with:

```text
Checksums not verified; run `omnipus package verify` to check them.
```

It does not claim the files are intact. A package whose manifest is intact still inspects even if a later file has been changed, and it exits 0.

`verify` checks every file. For an unchanged package of three files the success line is `OK: 3 files verified` and the exit code is 0. A changed file fails with `FAILED: <file>: <reason> (<code>)` and exit 1, and the message names that file (for example `omnipus/agents/<id>/SOUL.md`).

A package from a newer Omnipus (`format_version` higher than this install knows) is refused by both commands, exit 1. The message tells you it was made by a newer Omnipus and to update. A compressed file that asks for a decoder window over 8 MiB is refused as too large, without allocating that window.

### Import: preview first, then apply

```bash
omnipus import writer.omnpkg
```

The command uploads the file, prints a **provisional** preview, then asks:

```text
Import 1 agent as new? [y/N]
```

The preview lists the agent that would be added and the name it would get, anything skipped, warnings (a skill or a provider the new install does not have, a connector assignment that will be dropped), and anything you must type again. It is provisional: confirming checks the install again, and applies what is true then. You see `Uploading <file>…` while the file is sent. Nothing on the install changes at preview: no agent, workspace, skill, connector or configuration file. The upload is remembered for 30 minutes from the moment it is ready. At exactly 30 minutes it is gone. A restart forgets a preview you have not confirmed; upload the file again. At most five previews can wait at once. Only one import can be applied at a time.

`y` or `yes`, in any letter case, confirms. Anything else, including Enter alone, cancels. Cancelling prints `Import cancelled.`, exits 1, and imports nothing. If the terminal is not interactive and you did not pass `--yes`, the command cancels the same way.

| Flag | What it does |
|---|---|
| `<file>` | Required. The package to import. |
| `--workspace <id\|name>` | Join this workspace instead of the default one. An id is used first, then a name that matches exactly one workspace. Two matches are refused and both ids are listed. An unknown workspace is refused (`workspace_not_found`). |
| `--yes` | Skip the confirm prompt. |
| `--mode new` | Optional. New is the only mode. Leaving the flag out also means New. |

`--mode replace` and `--mode merge` are refused (`import_mode_not_available`, exit 1). Nothing is imported. Replace is planned to become the default later; it is not available now, and there is no password prompt on New.

If you name no workspace and this install has no default workspace, the import is refused (`default_workspace_missing`) and the message asks you to name a workspace (`there is no default workspace; name a workspace to import into`). On the command line, that is `--workspace`.

When the name had to change, the success line is:

```text
Imported "Writer (imported)" as <new-id> into workspace "<name>"
```

The name in quotes is the final name. The id is new: it is never the id from the package, and it is never an id that already exists. For a worker the command also prints:

```text
"<name>" is a worker: it is reached by delegation. Draw a delegation line to it from a Main agent in the Team graph.
```

The import result sent by the server carries its own sentence in `activation_message`, when the reload was confirmed: "This agent is a worker: it is not a chat partner. It is reached by delegation once you draw a delegation line to it from a Main agent in the Team graph." When activation is pending, that sentence is not set and the pending sentence is sent instead. The command still prints the worker line above from the package itself.

A Main agent gets its own main chat in that workspace once the live list has confirmed the import. No other member's chat is created or changed. A worker gets no main chat, and no existing agent gains a delegation line to it. You draw that line yourself. When the live list has confirmed the import, an open Team tab shows the new member without a page reload. Warnings (a dropped skill, a provider that is not configured here, a main chat that could not be created yet) still exit 0. The agent is imported.

**A name that is already taken.** Matching ignores letter case and spaces at either end. The existing agent is not changed. Whitespace at both ends is removed from the incoming name. A clash adds ` (imported)`, then ` (imported 2)`, and so on up to ` (imported 99)`. When adding a suffix would take the name over 100 characters, the base is shortened and trimmed again, so the final name is at most 100 characters. For example, one hundred letter A's becomes eighty-nine A's plus ` (imported)`. If ` (imported 99)` is taken as well, the import is refused (`name_suffix_exhausted`) and nothing is written. This includes a custom agent with the same name as a built-in one: importing "Mia" where Mia already exists creates a new custom agent "Mia (imported)" and does not change the built-in Mia. The name "Omnipus", in any letter case, is treated as a clash the same way.

**If the install changed after the preview.** A name taken in the meantime, a skill removed or installed, a provider configured since: the import goes ahead on the current install and lists each difference (`changed_since_preview`). If a check now refuses (the workspace is gone, or a create rule is now broken), it refuses before writing.

**Activation pending.** After the agent has been saved, Omnipus reloads the live list. If that reload returns an error or times out, the import has still succeeded: the agent is stored and is not reported as missing. The result says `activation: "pending"`. Pending means the reload was not confirmed. It does not mean a partial import. The command exits 0 and prints:

```text
Imported "<name>" as <id>, but the gateway did not confirm that it is live yet. Restart the gateway, or wait and check the agent list, before chatting with it.
```

Until you do that, the Team tab is not refreshed for this import, and the main chat is not created yet. Restart the gateway, or wait and check the agent list, before you chat with the agent.

If the record was renamed into place but the folder could not be flushed to disk, the import is still committed. Apply answers 200. The result is success, with the warning `publish_not_durable`: the agent is imported, and a power cut now could undo it. Do not import the file again and do not delete the agent. A surviving published record is committed, but it does not guarantee that recovery can finish. If an unfinished journal remains, the next start tries to restore the workspace join and can roll the import back if that fails. Check the recovery result and errors for this import.

**Warnings you can expect, with the import still succeeding.**

| What the package refers to | What is stored | Listed as |
|---|---|---|
| A skill that is installed here | Kept, and bound to the copy of that skill on this install | `skill_bound_local` |
| A skill that is not installed here, or whose name is not a valid skill name | Dropped | `skill_dropped` |
| Connector assignments | Always dropped. They are not matched by name | `mcp_binding_dropped` |
| A tool permission for a tool this install does not have | Dropped | `tool_policy_dropped` |
| Allow on a tool that must always ask | Stored as ask, which is stricter | `tool_policy_tightened` |
| A provider name for a provider that is not configured here | The name is kept | `provider_not_configured` |
| An old-style fallback list (`model.fallbacks`) | Left out. The current fallback list is kept when it fits the create rules (at most two, each with a provider) | `field_not_carried` |
| The main chat could not be created | The agent is kept. The chat is created when you open it | `main_chat_pending` |
| The audit record could not be written after the agent was saved | The agent is kept | `audit_failed` |

The last column is the code on the result. The sentence printed next to that code is not a fixed phrase.

### What a failed import does

Until the import has fully succeeded, the new agent is invisible. It is not in the agent list, not in chat, not in the sessions API, and not available to other screens or to agent tools. A failed import removes its own writes — the pending record, the agent's folder, the team entry and the delegation row — and prints:

```text
Import failed; nothing was imported: <reason>.
```

Nothing else can have used the agent, because nothing else could see it. The exit code is 1.

If the undo itself cannot finish, the agent was still never visible. The command prints:

```text
Import failed and could not be fully undone. The agent was never visible. Agent <id>, staging <staging_id>. Restart the gateway to finish the clean-up; see 'Recover from a failed import' in the CLI guide.
```

Restarting the gateway is the normal fix. The next start finishes an import whose save had completed, or undoes one whose save had not, before any agent is loaded. Recovery removes the staging folder as part of finishing. If that removal fails, the gateway logs the failure and the folder can remain. A later upload sweep retries removal of terminal staging folders. A `finished` or `rolled_back` summary alone does not prove the folder was removed. Delete the folder yourself only in step 2 of [Recover from a failed import](#recover-from-a-failed-import), with the gateway stopped, after recovery for this import keeps failing. Look for the gateway log line `packages: start-up recovery`. While audit logging is on, the audit log message is `import rolled back at start-up` or `import completed at start-up`.

**If the connection drops after you confirm,** the command cannot know which of those happened. It prints the following and exits 1:

```text
The import may or may not have completed (staging <staging_id>). Check the agent list for "<name>" before importing again. If the gateway stopped, it finishes or rolls back the import when it starts.
```

Check the agent list before you import the file again.

**What is not guaranteed.**

- **During the last step, a workspace list can show the new id.** The workspace list and the delegation read do not take the lock that the import holds. For the length of that last step, normally a few milliseconds, they can show the new agent's id on the team. If the step then fails, the id disappears again. Nothing can chat with it, open a session for it, or start work for it in that moment: there is no readable agent behind the id, and every save to that workspace waits. An open Team tab is not told to refresh, but a refresh that lands in exactly that moment can show one unknown-member card briefly.
- **Power-cut safety depends on the platform and storage.** On Linux and macOS, the prompt files are flushed when written, then the agent's prompt folder and its parent are flushed before the record is published. A failure in either folder flush stops publication and starts rollback. After publication, a failed flush of the record folder leaves the import committed, with `publish_not_durable`; do not delete or re-import that agent as recovery. These flushes depend on the filesystem and storage honouring them. On Windows, the prompt-folder and record-folder syncs do nothing, so a power cut can lose a published record or leave a record whose prompt files are missing, even after a success response. Storage that does not honour flushes can cause similar losses on any platform. Restart the gateway and check this import's recovery errors, agent, prompt and instructions. If the agent is listed or its published record exists, keep the agent, its working folder and any remaining recovery journal, and investigate. Do not delete the agent or import the package again as recovery.
- **An undo that cannot write the workspace file can leave the id listed.** The agent itself stays invisible. The id can sit on that workspace as an unknown member until a later start succeeds. Restarting is how it is cleared.
- **A committed import whose recovery cannot finish.** If the next start can neither restore the agent's team entry nor undo the import, the saved agent can load without its team entry. The next upload warns `unfinished_import_found` and names the agent and staging ids. Treat that agent as committed. Do not import it again or use **Delete agent** as a recovery step. Keep the recovery journal and the matching gateway errors for investigation.

After a successful undo, the install matches what it was, apart from harmless internal markers: a skill may have gained a generation stamp, an internal lock file for the unused id may remain, the workspace's revision may have moved (a save you had open will ask you to reload that workspace), and the audit log records the attempt.

### Recover from a failed import

Restarting is the normal fix. Recovery runs before the agent list loads. It finishes or rolls back unfinished imports; it can also roll back a saved import if its team entry cannot be restored. The gateway can still start when a recovery step fails, so a running gateway does not prove that recovery finished. Check the recovery result and errors for this import's agent and staging ids. Do not retry or manually delete a committed agent.

How to tell which happened:

| What you find | What it means | What to do |
|---|---|---|
| The agent is in the agent list, or `$OMNIPUS_HOME/entities/agents/<id>.json` exists | The import is committed. Activation pending (above) is this case too: the record is saved, and the live list had not confirmed the reload. | Stop. Do not import the file again. A second import creates a second agent. Do not delete the record or the agent's folder. |
| Only `$OMNIPUS_HOME/entities/agents/<id>.pending` exists, and there is no `<id>.json` | The import is not committed. The agent was never visible. | Restart first. Delete files only if recovery for this import keeps failing (step 2). |
| The gateway log shows `packages: start-up recovery` with this staging id under `finished` or `rolled_back` | The agent operation reached the finished or rolled-back result. Staging removal was attempted, but may have failed. | Check the accompanying recovery errors for this staging id. Do not delete an agent because staging files remain. |

If you do not see the recovery line, set `gateway.log_level` to `info` and restart, or read the audit log entry `import rolled back at start-up` / `import completed at start-up` (when audit logging is on).

While audit logging is on (the default), the audit log also has one entry whose message is `import completed at start-up` or `import rolled back at start-up`. That sentence is in the audit log, not in the gateway's ordinary log. If you turned `sandbox.audit_log` off, recovery still runs and is not refused, and there is no audit entry. A missing audit sentence does not mean recovery failed.

The agent id is in the incomplete-undo message, in the `unfinished_import_found` warning (the two ids after the message), in the gateway log, or as `new_agent_id` in `$OMNIPUS_HOME/backups/.staging/<staging_id>/journal.json`.

Manual cleanup is only for this import's recovery that keeps failing after you have fixed the cause reported by the gateway. Match the agent and staging ids to the warning or journal before removing anything. A running gateway is not proof that recovery succeeded. Stop the gateway immediately before any file cleanup.

1. Fix the cause reported by the gateway, then restart it. Check the recovery result and any recovery errors for this import. If the agent is listed or its published record exists, stop manual cleanup. Do not import it again.
2. Only if recovery for this import still fails and the agent is not listed: stop the gateway again. With it stopped, check that this agent's published record does not exist. If it exists, stop manual cleanup. Otherwise, remove only this import's pending record, agent working folder and staging folder, using the matching ids identified above. Start the gateway. Never remove a published record or the working folder of a listed or published agent as recovery.
3. If the workspace still lists the id as an unknown member and there is no agent record, remove that card on the Team tab. Or send `PUT /api/v1/workspaces/<id>` with the current `revision`, the team list without that id, and the delegation graph without edges that name it.
4. If the published record exists or the agent is listed, do not delete the agent, its record or its working folder as recovery. A missing team entry does not change that rule. Stop manual cleanup and keep the staging journal and the matching gateway errors for investigation.

### Exit codes

| Code | When |
|---|---|
| 0 | Success, including an import that finished with warnings, and including activation pending. |
| 1 | Refused or failed. This includes a verify failure, a cancelled import, a gateway that stays unreachable, an unknown or ambiguous agent or workspace, an output file that already exists, and any of the refusals above. |
| 2 | Usage. An unknown flag, a missing `--agent` or `-o`, a missing import file, or an input path that is missing, unreadable or a folder. `package inspect` on a folder, and `package` with an unknown subcommand, are usage errors. |

Refusals and errors go to standard error. The `Import cancelled.` line goes to standard output, and the exit code is still 1. A result is never indicated by colour alone.

## CLI or web app?

Most jobs can be done from either place.

| Want to… | Command line | Web app |
|---|---|---|
| Start or stop the server | `omnipus start` / `omnipus stop` | — |
| Run a one-off task | `omnipus <agent> "<prompt>"` | Chat |
| Pick a model for one run | `--model <slug>` | The model picker in chat |
| Manage API keys | `omnipus credentials …` | Settings → Providers |
| Review the audit log | `omnipus audit` | Settings → Security |
| Create and edit agents | — | Ask Omnipus (**Add agent** in the sidebar or **Team → Add agent**); edit from the workspace's **Team** |
| Move one custom agent to another install | `omnipus export`, `omnipus import`, `omnipus package inspect`, `omnipus package verify` | No separate screen. After a confirmed import the agent shows on the workspace **Team** |
| Connect Telegram, Discord, Slack and more | — | The Connectors screen, Configure |
| Add MCP servers | — | The Skills screen |
| Follow task work | — | A workspace's Board and Calendar |
| Set your preferences | — | Settings → Profile |

## Limits and things to watch

- **A stopped server is started for you when the master key is already available.** A one-shot task, `export` and `import` all do this. The key counts as available when `OMNIPUS_MASTER_KEY` or `OMNIPUS_KEY_FILE` is set, or when a `master.key` file is already in the data folder. You then see `Omnipus is not running — starting gateway...` and the command continues. You do not have to run `omnipus start` first. If none of those is set, the command does not start the server. It prints `` no gateway running and no non-interactive key configured; run `omnipus start` ``. If the connection is still refused after a start was attempted, a one-shot task prints `omnipus isn't running — start it with: omnipus start`. `export` and `import` print `error: the gateway is not reachable at <address> after starting it: <reason>`, and exit 1. A one-shot task stops waiting for the answer after five minutes by default; change it with `--timeout <duration>`. That only ends the terminal's wait: the agent's work has no time limit and carries on in the server, so you can read its result in the web app.
- **Talking to a remote server is not supported yet.** The `--url` flag exists but is reserved.
- **Command names shadow agent names, and worker agents cannot be addressed directly.** An agent named `start`, `export`, `import` or `package` cannot be run from the terminal, because the command is chosen first. There is no check at create time that refuses those names. Rename the agent or use the web app. `omnipus` with no arguments lists the agents you can run.
- **A package holds one custom agent, and nothing over the size caps.** The upload can be at most 64 MiB. The decoded archive, headers and padding included, can be at most 256 MiB, with at most 10,000 entries. The manifest can be at most 1 MiB, and each agent file (the record, the prompt, the instructions) can be at most 1 MiB. The compressed file must use a decoder window of at most 8 MiB. Upload, `verify` and `inspect` use the same decoder limits. Sparse files, whose holes expand when read, are not supported. Upload and `verify` refuse GNU sparse entries, including PAX `GNU.sparse.*` entries, before reading their expanded file contents. This applies to the manifest and to later entries, whether or not the manifest lists them. `inspect` checks only the first manifest entry; it does not check later entries or the size of the whole decoded archive. Each reader decodes synchronously. Separate uploads can decode at the same time; only apply is limited to one at a time. The 256 MiB archive limit is not a cap on the application's total memory use. A body of 64 MiB is accepted and then checked as a package. One byte over 64 MiB is refused as too large (`package_too_large`), whether or not the request declares its length. The export and apply requests themselves stay on the ordinary 1 MiB JSON limit; that limit does not apply to the package upload.
- **New is the only import mode.** `--mode replace` and `--mode merge` are refused. Replace is planned to become the default in a later version; it does not work now.
- **Built-in agents never travel, and external workers travel without their runtime settings.** Export refuses a built-in, system or shipped worker agent, and the message says it cannot be exported. An external worker is exported and imported without its program path, environment overrides and additional CLI arguments. The program comes from **Settings → Third-party agents** on the new install. Re-enter the environment overrides and arguments in the worker's **Edit → Runtime** (the preview and result warn `runtime_settings_removed`). If no program is set there for that tool, the result warns `program_not_set` and says the worker cannot run yet: a completed import does not mean the worker can run. A package part of a type this version does not know, such as a skill, is checked and then skipped; the preview lists it and its files are never installed. Skills, connectors, memory, workspaces and chats do not travel. Details, the name-suffix rule and how a failed import is undone are in [How to move an agent to another install](#how-to-move-an-agent-to-another-install).

## Related pages

- [Getting started](getting-started.md) — zero to your first chat.
- [Using Omnipus from the web UI](using-omnipus-ui.md) — the graphical half of this guide.
- [Concepts](concepts.md) — the agents, sessions, tools, and how routing works.
- [Troubleshooting](troubleshooting.md) — for when something misbehaves.
- [The knowledge base](knowledge.md) — where imported notes and records live.
- [Agents](agents.md#how-to-move-an-agent-to-another-install) — what a package contains, and what changes on import.
