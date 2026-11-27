# Settings

Settings connects model providers, sets agent limits, records your context, and shows usage. The sidebar user menu opens Settings, Profile, and Usage. Opening Settings stays on Settings. If the app is still choosing your workspace after you sign in, that choice does not replace a Settings page you already opened.

## What it is

Settings has 11 sections in three groups, listed down the left side (on a phone, as a list you tap into):

- **AI & agents**: Providers, Memory & recaps, System agents, Third-party agents, Limits
- **Security**: Protection, Sandbox & network, Keys & secrets
- **System**: Gateway, Data & retention, About

The open section is part of the address (`/settings?tab=gateway`), so a reload or a shared link opens the same section, and Back and Forward move between sections. **Search settings** finds indexed settings by name. For example, Verbose chat is under Profile → Chat display. Web search remains under Providers; the global permission and skill-trust editors are temporarily unavailable until the Skills & Tools update lands. Choosing an indexed row opens its section, scrolls to that setting and highlights it with a short flash (with reduced motion turned on, it is marked without the flash). A link can do the same with `row`, for example `/settings?tab=gateway&row=port`.

Personal things are not sections here. Profile is its own screen, holding your name, timezone, password, the context your agents read about you, and **Chat display** (Verbose chat). Usage is its own screen, showing token totals by agent, model, provider, and session.

### Saving changes

A changed value is not stored until you press **Save changes**. As soon as a value differs from the saved one, a Save bar appears with the number of unsaved changes, **Discard** and **Save changes**. Buttons that act at once (Test, Check, Rotate, Restart, God Mode, Delete, Clear) are not part of a Save.

- Leaving a section, or following a link elsewhere in the app, with unsaved changes asks **Leave without saving 2 changes?** (with your number) first. **Stay** keeps you where you were; **Leave** drops the changes. On a phone the **Settings** back button asks the same. Closing or reloading the tab gets the browser's own question.
- The same question appears when you use the browser's **Back** or **Forward** button, or type another Settings address into the address bar. **Stay** then puts you back on the section you left, with your changes, the same address and the same browser history. The page does not reload, and a second **Stay** later on the same page works the same way.
- Settings marked **Password** ask for your password once per Save, listing what the Save stores. If you cancel, nothing is stored and your changes stay on the page. When you sign in through omnipus.ai, a confirmation replaces the password field.
- Settings marked **Needs restart** are stored at once but used only after a gateway restart. Right after such a Save, **Restart now?** offers to restart. **Later** keeps a banner at the top of every section, "N saved changes wait for a gateway restart", with **Restart now** and **Show changes** (each change with the value running now and the saved value it gets after the restart; "saved, not applied" means the gateway could not apply it).

A Save ends in one of four ways, shown next to each setting:

| What you see | What it means |
|---|---|
| **Saved at** and the time | Stored and in use |
| **Saved, but not in use yet. Restart the gateway to apply it.** | Stored; the running gateway still uses the old value |
| An error under the setting, and "N changes could not be saved" | Nothing was stored for that setting; your change stays on the page |
| "N changes may not have been saved", with **Retry** | The answer was lost; Settings checks what was stored and tells you |

If part of a section cannot be loaded, that part names the failed read and offers **Retry** for that read. Only that part is blocked from saving; your changes elsewhere in the section stay and still save. A server failure can show the generic message "The server is unavailable. Please try again in a moment." It does not necessarily show the server's underlying cause. Read the gateway log when that message is not enough to diagnose it.

Values you edit are checked before they are saved. Stored-value handling depends on the setting: some forms show an out-of-range stored value and require a correction, while others show a resolved value with a warning. For Max tool calls per turn, see [A saved value outside 1 to 1000](#a-saved-value-outside-1-to-1000).

**This setting was changed somewhere else while you were saving** means another tab or program stored a different value at the same time. The message names the value that is stored now.

**Third-party agents** saves all three programs together, so two tabs saving that section at once keep whichever saved last.

### When the port or address changes

The page can only talk to the address it was opened from. When a restart moves the gateway to a new port, the **Restart now?** window says the new address and, if you reach Omnipus through a proxy, which port the proxy must point at. Once the old address stops answering, **Open the new address** takes you there, and Settings at the new address confirms the restart. If nothing changes within 60 seconds, the window says so: check that the gateway is running and read its log. If the gateway restarted but your changes are still listed as waiting for a restart, or that list cannot be read, the window says the changes are not confirmed in use, and you can restart again.

### Keyboard, touch screens and reduced motion

- **Keyboard.** On a fresh Settings page the first Tab stop is **Skip to settings content**. It moves the keyboard focus to the open section and leaves the address as it is. When you choose **Later** in **Restart now?**, or close it with Escape, the focus moves to **Restart now** in the banner.
- **Touch screens.** On a phone or tablet, every button and icon button can be tapped anywhere inside an area of at least 44 by 44 pixels. Every item in a menu or a pop-up list is at least 44 pixels high. In Settings the same applies to each option card in a choice and its round option button (for example in Sandbox & network), the rows of the provider list, and links such as **Set it in Limits**. With a mouse or trackpad these controls keep their normal size.
- **Reduced motion.** When your device asks for reduced motion, no loading spinner turns and no loading placeholder pulses, in Settings or anywhere else in the app. They stay in place, still.

## When you would use it

- You are setting up Omnipus for the first time and need a working model behind every chat.
- You want to switch providers, use a subscription you already pay for, or check which models your account can actually reach.
- You want to see what your agents consumed this week.
- You want agents to know your role and preferences without repeating yourself in every chat.
- You want to change how long transcripts are kept, check storage use, or cap how hard agents can call a model.

See [tool permissions](tools.md) for what agents may do and [security](security.md) for how the sandbox confines them. The installation-wide permission editor and skill-trust control are temporarily unavailable in the web app; the agent editor still lets you tighten one agent's permissions.

## How to connect a model provider

1. Open Settings from the sidebar's user menu. The Providers tab opens first.
2. Click **Connect a provider**. Search the catalog or browse it by company. Your recently connected providers sit under **Recent**, and the full list under **All providers**; both start closed, so open them to browse. The [providers and models](providers-and-models.md) page explains the choices without duplicating the live list.
3. Pick a provider and one of its available variants: API, coding plan, or account sign-in.
4. If the variant wants an API key, paste the key and click **Connect**. The row appears with its status.
5. If the variant uses sign-in, follow the dialog: it gives you a link and a code to approve on the provider's page, using any device. Sign-in is offered only on providers that can really sign in. A choice between sign-in methods appears only when a company offers more than one; today each company offers at most one, so the choice does not appear and the single method is used. OpenAI sign-in is on **ChatGPT (subscription)**, not on the API-key **OpenAI** provider. If you try to save sign-in on a provider that has none, Omnipus refuses and names the provider to use instead, for example `sign-in is not available for "OpenAI"; OpenAI sign-in lives on "ChatGPT (subscription)"`. An older install may still have a **Codex CLI** or **GitHub Copilot** row: those providers were removed, so the row shows an "Unknown provider" error and offers no sign-in or Manage action. Remove it, and use **ChatGPT (subscription)** for OpenAI sign-in.
6. Set the **default model** on the card at the top of the tab. That is the model new chats use unless an agent has its own.
7. Use **Test connection** in the provider's settings to re-check the connection, **Check with my account** (in the row's **More actions** menu, next to **Set as default model…**) to list available models, or **Remove provider** to delete it. The confirmation lists the agents that use that provider. When a default model is already set on another provider, it says those agents will fall back to that default provider and model, and it names both. If the provider you are removing is one you added yourself, or one that runs on your own computer, it also warns that their conversations will go to the default provider instead. If no default model is set, it says those agents will be left without a model. While the default model is still being checked, it says "Checking the default model…" and **Remove** stays off. If that check fails, it says "We could not check the default model, so we can't tell where these agents will go." and **Remove** stays available. Removing the provider that backs the default is refused until you choose a new default provider and model. Until you choose, it says "These agents will use the new default you choose below." After you choose, it names that provider and model as the default those agents will fall back to.

API keys are stored encrypted on this server, never in the main configuration file. See [security](security.md) for how the encrypted store works.

## Web search

In **Settings → Providers**, **Web Search** says: "Your agents search with the default service. If it fails, the fallback takes over." When the search roles are available, two cards appear:

| Card | What you can do |
|---|---|
| **Default search** | Click **Change** to choose the default service. When that service reports a depth cap, the card shows a read-only **Depth cap:** line. |
| **Fallback** | Click **Change** to choose a different service or **None**. The picker never offers the current default. When **None** is selected and DuckDuckGo is usable, the card says "DuckDuckGo works without a key — try it as your fallback." |

Only usable services can be chosen. Unusable services shown in the picker are disabled, with "Add a key first", "Key not reaching search", or "Needs configuration" beside them. If a chosen keyed service loses its key, its card warns that searches will fail or that the fallback will not run. Click **Fix** to open that service's key editor.

The service list below the cards puts ready services first. Services needing a key have **Add key** or **Edit key**. With the cards present, enter the key and click **Save key**. On a local install, you must re-type your password; on a hosted or desktop install, you confirm the change instead. A successful key save switches that service on without changing your default or fallback choice.

A failed integration save shows an error, but a gateway server failure does not guarantee a specific unlock, repair or reload explanation. Check the gateway log when the displayed message is not enough to diagnose the problem, then retry manually.

The server refuses **Remove key** and **Check connection** while development authentication bypass (`gateway.dev_mode_bypass`) is active. Turn it off and sign in before retrying. Ordinary key saves keep the confirmation rules above.

### Remove a saved key

For a service with a saved key, **Remove key** appears after **Edit key** and **Check connection**. DuckDuckGo needs no key, so it has neither removal nor connection-check actions.

Click **Remove key** to review what will happen. For example, the Tavily confirmation says: "This deletes the saved key from Omnipus and switches off Tavily. It does not revoke the key with Tavily." If the service is your default or fallback, the confirmation also explains that it will keep that role and Omnipus will not choose a replacement.

| Install | Confirmation |
|---|---|
| Local | Confirm **Remove key**, then re-type your password. |
| Hosted or desktop | Confirm **Remove key** once; no local password is requested. |

**Cancel** at either step changes nothing. While confirmation or removal is pending, conflicting actions are disabled. **Removing…** means the request is in progress; a successful removal reports, for example, "Tavily key removed." The key editor closes and the service's status is refreshed.

Removal deletes Omnipus's saved key and switches off that service. It does **not** revoke the key with the service, recall a search already sent, or change your default or fallback choices. The selected default keeps its existing "key missing — searches will fail" warning; the selected fallback warns "key missing — the fallback will not run". **Fix** opens the key editor. An available fallback may still answer when the default is missing its key. **Add key** and **Save key** can restore the service without reassigning its role.

Removal is refused without changes if search roles are still undecided, another connection shares the key, or the service uses a key reference set outside Settings. The message explains the fix: choose default and fallback services, give the other connections their own key, or remove the manually configured reference first. A later-stage failure can leave partial changes; check the refreshed row for the current state and the gateway log for the cause. Do not assume an error means the key or service was left unchanged.

### Check a connection

**Check connection** sits between **Edit key** and **Remove key**. It uses the saved key, not an unsaved edit. A key must be saved and loaded before the button is enabled; otherwise the row says "Add or save a key before checking."

"Runs one small search with your saved key. Your service may charge for it." The check searches for **Omnipus**, not conversation content, through that one service. It has a 15-second total deadline, makes no fallback search, and does not automatically retry. A valid response with no results also counts as a working connection; search results are not shown or saved by this diagnostic. Checking does not require password confirmation.

"Ready means the key is loaded. Check connection tests it with the service." The readiness badge and the connection result are separate. After every completed attempt, Omnipus refreshes the row from the server instead of turning a failed check into a disabled service.

| What you see | What to do |
|---|---|
| **Checking connection…** | Wait for this service's result. Its conflicting actions are disabled while the check runs. Other services can still be checked. |
| **Connection works.** | The service accepted this check. This is a temporary result, not a lasting health guarantee. |
| **The service rejected your key. Edit it and try again.** | Use **Edit key**, save a replacement, then check again. |
| **The service is limiting requests. Wait and try again.** | Wait for the cooldown or the service's longer retry delay, then check manually. |
| **The service did not respond in time. Try again.** | Wait for the cooldown, then check manually. |
| **Could not reach the service. Try again.** | Check network availability, then try again after the cooldown. |
| **The service could not complete the check. Try again.** | Try again after the cooldown. |
| **The service returned an unexpected response. Try again.** | Try again after the cooldown. |
| **The key changed while checking. Check again.** | The server rejected the old key's result. Wait for the cooldown, then check the current saved key. |
| A gateway server error | A specific credential-store cause is not guaranteed to appear. Check the gateway log, correct the reported problem, then check again after the cooldown. |
| **The saved key is not available to the running service. Reload the configuration, then try again.** | Reload the configuration before checking again. |
| **The server is unavailable. Please try again in a moment.** | The generic message does not mean the server supplied no cause. Check the gateway log, then retry manually after the cooldown. |
| **You can check again in {N} seconds.** | Another check was refused by the local limit. Wait for the displayed countdown; no check is scheduled automatically. |

There can be only one check in progress for each service, with a 30-second cooldown per service on this Omnipus instance, including failed checks. A service may request a longer wait. After the cooldown, click **Check connection** for a fresh attempt; an old success is not reused as a new check.

A failed check shows an error, but a gateway server failure does not guarantee a specific unlock, repair or reload explanation. The generic unavailable message does not mean the server supplied no cause. Check the gateway log, correct the reported problem there, and retry manually. These gateway errors are separate from the service diagnostic outcomes above, such as a rejected key or a timeout.

Connection results are temporary and belong to one service. Finishing another service's check does not clear them. Saving or removing a key clears only that service's old result; a changed service row also clears its result. Checks do not change your roles, switch off a service, or change ordinary fallback or native-model search behaviour.

A key change detected by the server while checking rejects the old outcome. There is a remaining short window after the check response but before the final status refresh: if a replacement key leaves the row looking identical, the previous key's result may still appear. Run a fresh check after the cooldown to verify the current key.

SearXNG is no longer offered: on upgrade, Omnipus removes its old configuration, clears it as a saved default, and sets a saved SearXNG fallback to **None**.

## Where each setting lives

Each section and neighbor screen has one job.

| Screen or section | What it is for |
|---|---|
| Settings, Providers | Connect providers, manage keys and sign-ins, set the default model; **Voice input**: the Active transcriber and keys for keyed voice services; web-search providers |
| Settings, Memory & recaps | What the team remembers: recap settings, including [Recap limits](memory.md#recap-limits). How long memory is kept is in Data & retention |
| Settings, System agents | The agents the system runs on — Judge, Plan Supervisor and Omnipus — each opened in the shared agent editor |
| Settings, Third-party agents | The program and standard parameters Omnipus uses for each external command-line tool (Claude Code, Codex, opencode) |
| Settings, Limits | Three groups, each closed until you open it: **Context and tool results** (context budget), **Model overrides** (context length per model) and **Agents and goals** (tool calls per turn, agents at once, goal timers; every value there asks for your password) |
| Settings, Protection | Security health, Auto-approve (saved with **Save changes**, Password badge), the **Turn on God Mode…** button (acts at once, never part of a Save), prompt-injection **Defense level** (Low, Medium or High, saved with **Save changes**; it applies without a restart), per-agent rate limits, the audit log |
| Settings, Sandbox & network | Sandbox mode, filesystem model, shell workspace limit, allowed folders, internal network addresses (all asking for your password, on one Save), and the network proxy for shell commands (on the same Save, no password, applies after a gateway restart) |
| Settings, Keys & secrets | The credential vault |
| Settings, Gateway | Bind address, port, gateway token, preview server, log level; restarting the gateway |
| Settings, Data & retention | How long chat history and memory are kept, storage numbers, clearing all chat history |
| Settings, About | Version and build information, including the **Build commit** (or the reason it could not be read); **Copy** puts the system information, build commit included, on the clipboard |
| Profile | Your name, timezone, font size, password, workspace context, and Chat display (Verbose chat) |
| Usage | Token totals by period for agent, model and provider; up to 50 sessions by recorded total tokens, independent of the period; cache-hit percentage by agent and provider |

Old links keep working. An old `?tab=` name opens the section that holds those settings now:

| Old link | Opens |
|---|---|
| `models`, `performance` | Limits |
| `integrations` | Providers |
| `security` | Protection |
| `chat` | Profile, Chat display |
| `backup`, `backups`, `restore` | Data & retention, with the notice "Backup and restore were removed." |
| `context`, `devices`, `signin`, or a name Settings does not know | Providers |

Device pairing and signed-in browsers are not in Settings at the moment. The installation-wide tool-permission editor and unverified-skill trust control are also temporarily unavailable: neither the new Settings nor [Skills & Tools](skills.md) has replacement controls yet. Existing configured values still apply.

**Voice input**, under **Settings → Providers**, holds the **Active transcriber**: the one service that turns your voice messages into text. Only configured services are offered. A keyed voice service needs its saved key; Audio Model is configured by its voice-model setting and does not need a separate service key here. Changing the transcriber is part of the Providers Save and asks for your password. Each voice service has its own row; for a keyed service, **Add key** or **Edit key** opens a side panel with the key field and a show/hide button, and **Save key** there asks for your password. The default model on the same page is saved by the same **Save changes**; it does not ask for the password.

**Settings → Limits** opens with its three groups closed. The **No context length** link on a model opens Limits with **Model overrides** open and a new row filled in for that model; it is stored only when you press **Save changes**. Following the same link again, for example after **Discard**, fills the row again without a reload. If one group cannot be loaded, only that group shows the error and **Retry**; the other groups still save.

In **Settings → Limits → Context and tool results**, five controls set individual result sizes and the default context window. They are separate from the combined **Tool-result share limit** below and the **Model overrides** group.

| Control | Default | Accepted values | What it changes |
|---|---|---|---|
| MCP tool result cap | 62,500 characters | Whole numbers from 1 to 150,000 | How much text from one successful connected-server result can stay in the live conversation. |
| Built-in tool result cap | 64,000 characters | Whole numbers from 1 to 150,000 | How much text from one successful built-in result can stay, including attachment text, recalled material and delegated reports. |
| Failed tool result cap | 10,000 characters | Whole numbers from 1 to 150,000 | How much text from one failed, denied or skipped result can stay, for built-in and connected-server tools alike. |
| Ingest bound | 8,000,000 bytes | Whole numbers from 1 to 8,388,607 | How much Omnipus reads from one network or subprocess source. This is a byte limit on incoming data, not a character limit on text kept in the conversation. |
| Default context window | Unset | Leave blank to unset, or enter a positive whole number of tokens | The global context-window size choice. An agent's own override takes precedence, then a matching provider/model override, then this default. A known model capability still caps the effective window. |

The three result caps count Unicode characters, not bytes or tokens. They are upper limits: a tight request budget can shorten a result further. **Model overrides** sets a window for one provider/model pair rather than changing the global default. Press **Save changes** to store edits; a successfully applied context change is used on the next turn without a gateway restart.

**Tool-result share limit**, in the same group, sets the maximum share of the active model's context window that all tool results together may occupy before trimming is needed. The context window is how much information the model can read at once. This is a percentage of the whole window, not the smaller budget left after reserving space for output and instructions. It replaces the old combined character limit, not the individual result caps. The default is **50%**. Enter a percentage **greater than 0% and no more than 100%**; fractional percentages are accepted.

For scripts, the same setting is `context.tool_result_share_fraction` in configuration and `tool_result_share_fraction` on `GET` / `PUT /api/v1/settings/context`. Send the fraction, not the percentage: `0.125` means 12.5%. An omitted field in a partial update keeps the saved value; it does not reset to 50%. Null, text, booleans, zero and values outside the interval are rejected. A successful update takes effect on the next turn without a restart.

The existing Usage screen counts recorded agent-turn-loop tokens, not money. Auxiliary provider calls such as automatic recaps, fallback goal compilation and audio-model transcription are outside that per-call accounting path; see [observability](operations/observability.md#model-call-cache-accounting) for the coverage limits. Choose a period for **By agent**, **By model** and **By provider**. **By session** is different: it lists up to 50 sessions, sorted by their recorded total tokens, and is not filtered by the selected period. Do not read those session totals as usage for the chosen day, week or month. **Cached** is cache reads plus cache writes, already included in the total; **Uncached** is the rest. **By agent** and **By provider** also show **Cache hit**, the share of all input tokens read from the provider's cache. For example, a ratio of `0.8` appears as **80%**. A row with no input tokens shows no percentage, not **0%**. These numbers are on Usage, not a separate caching page. A ChatGPT sign-in call still counts tokens but is not given a dollar cost. See the provider's invoice for costs.

For **ChatGPT (subscription)**, Omnipus uses the same conversation identifier on successive calls to help the provider reuse cached input. This routing identifier is not built from credentials or message text. Reuse is not guaranteed: **Cache hit** measures the share of input tokens actually read from cache, not the percentage of calls with any cache reuse.

There is no longer a computed default for `performance.max_parallel_agents`. Leave the field blank to let live available memory govern each new agent turn; the Limits section reports this as "automatic — bounded by available memory". Set a positive whole number when you need an explicit cap.

If the host cannot measure available memory, or reports high memory pressure, Omnipus holds agent concurrency at a floor of **two** turns. It refuses to grow, never to run: the first two turns can start, while a third concurrent turn is refused and the refusal names memory. A smaller `performance.max_parallel_agents` set explicitly still applies; a larger value does not lift this floor. Check the host's memory reporting and available memory before expecting more turns to run at once.

The rate limits live in Settings, Protection. Two numbers, both per agent: model calls per hour, and tool calls per minute. Leave either blank for no limit. A change is stored when you press **Save changes** and applies to running agents without a restart; if the save fails, the error appears under the field. For scripts, the same two numbers are `max_agent_llm_calls_per_hour` and `max_agent_tool_calls_per_minute` on `GET` / `PUT /api/v1/security/rate-limits`. A time limit for a single command and a time limit for background processes are not offered in Settings for now, because Omnipus does not apply them yet.

Profile holds one setting your agents read every turn: **Workspace Context**. It is a free-text page about you — your role, your preferences, how you like answers — saved automatically and shared with all agents. Your display name, timezone, and font size are personal display choices stored in this browser, so they do not follow you to another machine.

## System agents

**Settings → System agents** lists Judge, Plan Supervisor and Omnipus. Judge and Plan Supervisor are internal engine agents and cannot join workspace teams. Omnipus is a hidden member of the default workspace only; open its fixed sidebar entry rather than looking for it in Team. These built-in agents cannot be deleted.

**Edit** opens the shared agent editor, including Omnipus's model setting. You can repair its model here without a working model conversation. Judge and Plan Supervisor also have editable instructions; their capabilities remain fixed. There is no default-agent setting. The editor saves on its own; if the answer to a save is lost, it says **Save not confirmed — it may or may not have been stored.** with **Retry** instead of claiming it saved. The Omnipus chat's gear button opens this tab directly. While the list loads it says **Loading System agents…**; if it cannot load, the tab says **The system agents could not be loaded** with **Retry**; if a refresh fails after it loaded, the rows stay with **Could not refresh — this list may be out of date.** and **Retry**.

## Third-party agents

**Settings → Third-party agents** decides which program runs for each external command-line tool, and which standard parameters it always gets. There is one row each for **Claude Code**, **Codex** and **opencode**. Every external worker on that tool uses these values; you do not set them per worker. Only a person signed in to the web app can change this tab: agents cannot, through any tool, and `set_config` refuses the whole sandbox section. An agent can read the saved program and parameters for one worker, read-only, through `get_agent` (see below). An agent allowed to use `get_config` can read all three rows the same way. No agent can change them.

| Field | What it does |
|---|---|
| **Program path** | The full path of the tool's program on this machine, for example `/opt/homebrew/bin/codex`. An empty field (shown as **Not set**) means no program is set for that tool. When nothing is saved yet, Omnipus pre-fills the field with the program it found on this machine, and a **Found at …** line under the field shows where it was found. Finding the program only looks at files; it never starts the program. It looks only in the program search path of the Omnipus server and in a fixed list of common install folders (on macOS `/opt/homebrew/bin`, `/usr/local/bin`, `/usr/bin`, `~/.local/bin`, `~/.npm-global/bin`, the newest nvm Node.js, `~/.bun/bin`, `~/.deno/bin` and `~/.cargo/bin`; on Linux the same home folders plus `/usr/local/bin`, `/usr/bin` and `/snap/bin`; on Windows the npm folder and the tool's folder under Programs or Program Files). A tool installed anywhere else, for example opencode under `~/.opencode/bin`, is not found and nothing is pre-filled; type its full path and press **Save**, and every run uses it. A pre-filled path is not saved until you press **Save**. |
| **Parameters** | The standard parameters: one line of free text added to every run of that tool, exactly as you type it — any parameter the tool accepts, for example `--max-turns 40`. Leave it empty for none. Omnipus shows the parameters in full and adds no filter or warning, with one exception: a model option (`--model`, or `-m` for Codex and opencode) is dropped from a run whose worker has a model Omnipus passes to the tool, because the worker's own model wins. |
| **Check** | Optional. Runs the program at the path currently typed in that row to read its version, and shows the result as a notice, for example **Codex CLI works: version 1.2.3.** It saves nothing. A program that runs but prints no version number is reported as not working. The path is checked exactly as typed, spaces included, just as **Save** stores it and every run checks it. An empty path, or one made only of spaces, is reported as missing and nothing starts. A refused path is reported with the program and the name of the failed check, for example `not allowed: codex: check not_found failed`. The notice never shows the path; the server log records it. |
| **Save** | One button saves all three rows together. |

**Checks when you save.** A program path must be absolute, point at a file that exists, is a regular file and can be run by the user Omnipus runs as. The file must not sit in a folder agents can write to: the Omnipus data folder, an agent's working folder, any workspace folder, any host folder mounted into a workspace, or any extra folder the sandbox lets agents write. The list also includes the operating system's temporary folder when it is absolute and is not the shared `/tmp` or `/private/tmp`, together with its resolved spelling. On macOS this is normally the folder named by `TMPDIR`; Windows uses its own temporary-folder selection. Shared temporary folders are not added automatically by this rule, but locations there are still checked when another configured root covers them. This reflects the normal enforcing per-turn sandbox policy, which removes the blanket shared-temp write grant. It is not a promise of protection in God Mode or without an enforcing sandbox. A link is followed to the real file, and neither a link along the way nor the folder that holds it may sit in such a folder. Omnipus asks the file system whether two names are the same folder, so a different spelling of one of these folders is refused too, for example a different letter case, or an accented name such as `Café` written in its composed or decomposed form on macOS. If Omnipus cannot read the full list of these folders, for example because a workspace's list of mounted folders is unreadable, it cannot check where the program sits, so it refuses the program rather than guess and says why: "The program path for Codex was not saved: the list of folders agents can write to could not be read in full, so this location cannot be checked. Details are in the server log." Moving the program does not help; the server log names the cause, such as the workspace whose list could not be read. A workspace with no mounted folders is fine. Every run, the command preview, **Send a test message**, the runner connection check (`POST /api/v1/agents/{id}/runner/test`) and the worker's program warning refuse with the same reason, starting "This agent cannot run:" and naming the check `location_unverifiable`. Omnipus does not check who owns the file or who else may change it, so keep the program somewhere only trusted users can write. On Windows the file must end in `.exe`, `.cmd` or `.bat`. Standard parameters may be at most 4,096 characters, with no line breaks.

**What happens when you save.**

- Omnipus checks every row before it writes anything. If a field fails, the reason appears under that field, for example "The program path for Codex was not saved: it must be an absolute path.", the cursor moves to it, and **nothing is saved, the other rows included**. What you typed stays in place, so you can correct the field and save again.
- The path is stored exactly as you typed it.
- If your session has ended, the tab says to sign in again and keeps what you typed.
- A successful save uses the common **Saved at** indicator. Saving this whole section from two tabs keeps the last save.
- If the values were saved but are not yet in use, the section shows **Saved, but not in use yet. Restart the gateway to apply it.** The submitted values remain visible as saved; matching drafts are no longer counted as unsaved. Newer edits stay unsaved. Use the restart offer or banner to apply the stored values. Do not assume a clean form offers a second Save of the same values.

**Checks on every run.** The program is checked again each time a worker starts or continues, so a folder mounted into a workspace after the save counts from the next run. A continued run keeps the tool's own conversation but uses the program and parameters saved now, never the ones from its earlier run. If no program is saved for that tool, or it no longer passes the checks, the run is refused before anything starts — for a delegation, no worker session is created — and the person or agent that started it sees the reason — for example "This agent cannot run: no program path is set for Codex. A person can set it in Settings → Third-party agents → Codex." A program that fails a check is refused with the program's name and the check's name, for example "This agent cannot run: the program set for Codex in Settings → Third-party agents does not pass its checks (not_found). A person can fix it there." The refusal never contains a file path or an operating-system error; the path that failed is written to the server log. There is no fallback to a program found on the system path. The file that was checked is the file that runs. The command preview and **Send a test message** on a worker's **Runtime** tab use the same saved program and show the same refusal. They use your standard parameters too: the preview shows them at the end of the command, and a test message passes them to the tool. The runner connection check (`POST /api/v1/agents/{id}/runner/test`) uses the same saved program: when a run would be refused it returns that refusal and starts nothing. Otherwise it runs the program with `--version` and looks for its sign-in credentials on this machine; Omnipus sends no model request for it, so it spends no usage. Its result is fixed text for each outcome, for example `handshake-failed` when the program cannot start or prints nothing that looks like a version number. The program's own output and any operating-system error go to the server log only. An external worker's page does not show a Test Connection button. **Send a test message** is the real model test and does spend usage.

**Standard parameters are yours, in clear.** They come after the worker's own **Additional CLI arguments**, so your value wins when both set the same flag. A worker's own arguments go through Omnipus's argument filter; the standard parameters do not, except that a model option in them is dropped when Omnipus passes the worker's own model (the command preview lists it as dropped). They are shown back exactly as typed, and passed to the tool as typed apart from that model option. The audit log entry for a run holds them through the sensitive-data filter, which replaces only secrets Omnipus already holds. Turning `tools.filter_sensitive_data` off means the entry holds them as typed. Either way, do not put keys or passwords in them: put secrets in the worker's own environment variables instead, which are always masked. Parameters can widen what the tool may do — see [security](security.md#third-party-agent-programs-and-parameters) before adding one such as Codex's `--dangerously-bypass-approvals-and-sandbox`.

**Why agents cannot change it.** The program and its parameters decide what actually runs on your machine. Keeping them person-only means an agent cannot point a worker at a different program or add flags that lift the tool's safety settings. An agent can see the saved program and parameters (read-only) and whether a run would start.

Every change is recorded in the audit log: a changed path or changed parameters, with the old and new value and who saved it. A path is recorded as saved. Old and new parameters pass through the sensitive-data filter, or stay as typed when `tools.filter_sensitive_data` is off. Every external-worker run is recorded too, with the program, the flags Omnipus's argument filter dropped from the agent's own flags, your standard parameters under the same rule, and any model option dropped from them. A **Send a test message** that used your standard parameters records them the same way.

Each worker's **Runtime** tab shows the saved program and parameters read-only, with a link back to this tab. See [agents](agents.md) for what the worker keeps per agent.


## Tool loading

**Settings → Limits → Agents and goals → Tool loading** is on by default. It keeps each request smaller by advertising common tools first and loading additional tool definitions when needed. Turning it off sends the full available tool definitions with each request instead. See [Loaded tools stay with the conversation](tools.md#loaded-tools-stay-with-the-conversation) for how an on-demand load is retained.

This switch changes the tool definitions sent to the model, not tool permissions or connected-server assignments. It is separate from MCP discovery settings. Changing it edits a draft: press **Save changes** to store it. The Save asks for your password once on a local install, or confirmation where there is no local password. Cancelling keeps the draft without storing it.

For scripts, the switch is `tools_on_demand` on `GET` / `PUT /api/v1/performance` and `tools.manifest.compressed` in `config.json`: `true` means on-demand loading; `false` means the full available tool definitions.

## Goal timers

The goal timer controls live in **Settings → Limits → Agents and goals**, beside **Tries per goal**. They govern when the existing keeper can start another work turn, not how long that turn may run.

| Control | Default | Accepted values | What it controls |
|---|---|---|---|
| Idle reminder quiet window, in seconds | 60 seconds | Whole numbers from 30 to 3600 | Waiting before an ordinary reminder when a goal is idle. There is no quiet-off switch; zero is invalid. |
| Stalled-work check-ins | On | On or off | The only off switch for busy-work check-ins. Turning it off does not stop ordinary idle reminders. |
| Stalled-work check-in interval, in minutes | 30 minutes | Whole numbers from 5 to 1440 | Waiting without new progress while the goal's own background work remains busy. Turning check-ins off disables this input visually but retains its value; the same bounds apply even while off. |

The clocks are independent. A five-minute busy-work check-in can become due inside a 3600-second idle quiet window. Busy observation runs on each sweep, not only after idle quiet expires. Check-ins still follow the one-per-observed-stall and fresh-progress re-arm rules in [Goals](goals.md#one-check-in-for-stalled-background-work). Stop, unanswered questions, claim pauses and the three-failure/five-no-progress brakes continue to take precedence.

A successfully applied change is live for the next keeper sweep without restarting the gateway or replacing the goal. The sweep remains fixed at **30 seconds**: a due reminder or check-in can be noticed up to **30 seconds late**. That is scheduling delay, not a guarantee of response time or completion. Reserving or sending a busy check-in alone does not restart the idle quiet clock; actual activity and the admitted turn use their existing activity rules.

A timer change follows the same rules as every other Settings value: nothing is stored until you press **Save changes**. On a local install the Save asks for your password once. **Cancel** in that prompt stores nothing and keeps your changes on the page, so you can save them later or press **Discard**. No prompt opens by itself. While a saved value is waiting to be applied, the page says so in its **saved, but not applied yet** notice.

If you edit a field again while a Save is still on its way, the newer value stays on the page as an unsaved change. The older Save finishing does not replace it. Press **Save changes** again to send it.

One Save sends every changed value of the section together. A setting you did not change keeps the value it already had.

For scripts, `GET /api/v1/performance` always returns resolved values for `goal_reminder_quiet_seconds`, `goal_stalled_checkin_enabled` and `goal_stalled_checkin_minutes`. `PUT /api/v1/performance` updates only fields supplied in that request and retains every omitted setting. The endpoint still requires administrator access. In local authentication mode it also requires password re-authentication. In platform authentication mode there is no local password step; the web app asks you to confirm the change. An empty update is invalid. The numeric fields reject zero, fractions and out-of-range values, including a minutes value of zero while check-ins are off; the switch accepts a boolean, not text or a number. In the settings file, the keys live under `planning`. Missing or null keys use their defaults, whereas an explicit out-of-range number is a configuration error, not a request to restore the default.

If a save reports **saved, but not applied yet**, do not treat it as a successful live update. The error identifies the fields that request changed. At the **refresh** stage the settings file has the new values but the running configuration—and the keeper's timers—still uses the old values until a successful reload or restart. At the **reload** stage the running configuration has changed but the agent registry could not finish reloading: timer sweeps read that new running configuration, even though other agent settings may remain unapplied. Nothing is rolled back. See [When a save is not applied yet](#when-a-save-is-not-applied-yet).

## Tool calls per turn

**Max tool calls per turn**, in Settings, Limits, sets an agent's normal work budget for each turn. For native agents, it counts working rounds, not individual tool calls: a round can run several calls. Pending follow-ups or winding down can extend the current turn beyond that normal budget, up to a hard safety bound of twice the configured number of rounds. A task run can contain several turns, so this is not a total budget for the whole task.

When a native chat turn exhausts its budget without a final answer, Omnipus ends that turn and records a system notice explaining the limit. This notice is generated by Omnipus, not written by the model, and carries no model attribution. External workers use their command-line tool's turn cap and error reporting instead; see [Workers and delegation](agents.md#workers-and-delegation).

If a native chat turn ends at this limit with an accepted follow-up still unanswered, Omnipus automatically starts one fresh turn in the same conversation to handle it. This also covers an instruction that had already reached the model. The continuation uses the saved conversation, keeping the instruction and completed tool results; recovery does not resend the instruction or replay completed tool calls. You do not need to resend it merely because the previous turn reached its limit. Without new input, this recovery does not keep scheduling more turns for itself. If that continuation also runs out of budget, you can ask the agent to continue. With no follow-up to handle, this steering recovery starts no new turn. Task and goal retry rules remain separate: a per-turn cutoff is not a Stop of the whole job.

A turn can also stop before the limit when the agent repeats itself without progress: after the same tool call with the same arguments succeeds eight times in a row, or when, in a round where nothing else succeeded, it asks again for a call that already failed five times with the same arguments and was then blocked. The agent's last reply names the tool and says why it stopped; a task run in that state counts as a try that did not finish, so the task retries or ends Failed instead of staying in progress.

- The limit applies to every agent, including the built-in roster and [external workers](agents.md#workers-and-delegation).
- It accepts a whole number from **1 to 1000**. The shipped value is **200**.
- In `config.json` it is `agents.defaults.max_tool_iterations`.
- An agent can have its own lower limit on its profile. It can never have a higher one. See [agents](agents.md#how-to-lower-one-agents-tool-call-limit).
- A turn that is already running keeps the limit it started with. The new value applies from the next turn. No restart is needed.

### How to change it

1. Open Settings, then the **Limits** section.
2. Open the **Agents and goals** group and type a new number into **Max tool calls per turn**. Nothing is stored yet: the Save bar counts the change.
3. Click **Save changes**. If the new value is lower than the current one and some agents have their own higher limit, a dialog lists each of those agents as *old value → new value*. Click **Lower limits** to go ahead, or cancel to change nothing.
4. Re-type your password when asked. (Where there is no local password to re-type, you confirm the change instead.)

Every change is recorded in the audit log as a `security_setting_change` event. The global limit is recorded as `agents.defaults.max_tool_iterations`, and each lowered agent as `agents.<agent id>.max_tool_iterations`, with the old and new values.

### Lowering and raising behave differently

| You change the limit | What happens to agents with their own limit |
|---|---|
| **Lower**, for example from 200 to 50 | Every agent whose own limit is above 50 is lowered to 50, after you confirm the list in the dialog. Agents at or below 50 are not touched. After the save, a message names each agent that was lowered. |
| **Raise**, for example from 50 to 200 | No dialog opens, and no agent's own limit changes. An agent you lowered to 50 stays at 50. Raising the limit again does not restore values an earlier lowering replaced. |

If an agent's own limit changes between the dialog opening and your confirmation, nothing is saved. The dialog shows **"The list of affected agents changed — review and confirm again."** with the new list, and you confirm again.

### A save that reloads the configuration can pause

When a save makes the gateway reload its configuration, the reload first waits for a background queued-task check or mail check that is already in progress, so it is not cut off halfway. Each of the two checks gets up to 10 seconds, one after the other. Do not repeat the save while the first one is still pending. If a check is still running when its wait runs out, the reload goes ahead without it and the log shows a warning that names the poller (for example, "the mail poller was still in a mail check after its stop wait"). That check finishes on its own; if the warning repeats, check the mailbox connection or, for the queued-task poller, the task store.

A running plan does not make the reload wait. An owner or Plan Supervisor reply, or a Judge check, that is in progress keeps running through the reload and finishes normally.

### When a save is not applied yet

Rarely, a save is written but not yet in force. A warning then starts with **"Saved, but not applied yet"**. It names only the settings that save changed (for this card, "the tool-iteration limit"). The agents that save lowered are named at the moment of the save — in a status toast and inline under the field — but that naming does not survive a page reload: reload, and the list under the field is gone, even though the "Saved, but not applied yet" warning itself keeps showing. The audit log is the lasting record of which agents were lowered and when. It comes in two forms:

| The warning reads | What happened |
|---|---|
| "Saved, but not applied yet — saved to the settings file; takes effect after a restart or reload: …" | `config.json` holds the new value, but the running configuration could not be refreshed. The Limits section keeps showing the value you saved, with this notice, until the gateway reloads or restarts. |
| "Saved, but not applied yet: … the agent reload failed …" | The running configuration has the new value, but the agents could not be reloaded with it. Their next turns keep the old limit. |

Either way nothing is rolled back: the value is in `config.json`, agents it lowered are already lowered, and the change is in the audit log. The new limit applies after the next reload or a gateway restart.

### A saved value outside 1 to 1000

If `config.json` holds a value the setting does not accept, for example after a hand edit, Omnipus still starts. It runs with a corrected value in memory and does not change the file:

| Saved value | Limit in force |
|---|---|
| Missing, or below 1 (such as 0) | 200, the shipped value |
| Above 1000 (such as 5000) | 1000 |

The gateway log carries a warning at start-up, and the Limits section shows a warning naming the saved value and the one in use. Save a value from 1 to 1000 in the field to replace the saved one.

### Changing it outside Settings

Only Settings, Limits changes this limit, because it asks for your password and shows you which agents a lowering affects. Other ways in are refused:

- The general configuration endpoint, `PUT /api/v1/config`, refuses a body that includes `agents.defaults.max_tool_iterations` with status 403 and "agents.defaults.max_tool_iterations is a blocked path — use the dedicated endpoint". Nothing in that body is saved. Other changes to `agents.defaults` through this endpoint keep the saved limit as it is.
- The `set_config` tool that agents use to change configuration from a chat refuses the key and points to Settings, Limits. To limit one agent from a chat, ask for that agent's own value instead; see [agents](agents.md#how-to-lower-one-agents-tool-call-limit).

### If you are upgrading

- **The environment variable is retired.** `OMNIPUS_AGENTS_DEFAULTS_MAX_TOOL_ITERATIONS` used to override the saved value on every start. Now, on the first start after upgrading, its value is copied into `config.json` once. From then on it is ignored, and the log says so at each start while it is still set. Change the limit in Settings and remove the variable. A value outside 1 to 1000 is copied as the nearest bound, 1 or 1000, with a warning. A value that is not a whole number is not copied; the log says so.
- **Agents with an old higher limit.** Earlier releases let an agent's own limit be higher than the global one. Upgrading does not change those stored values. Such an agent now runs at the global limit, its profile says its own value has no effect, and one warning at start-up lists every such agent. See [agents](agents.md#how-to-lower-one-agents-tool-call-limit).

## Delegation limits

How much work one agent can hand to another is bounded in two ways: a Limits setting for how many delegated sessions may run at once, and a chain-depth cap set by `performance.max_delegation_depth` in `config.json` (a workspace **Team** edge can set a tighter depth for a particular agent pair). Each behaves sensibly when left blank. There is no time limit: a delegated session runs until it finishes or someone stops it (see "How long a child may run" below).

### How deep delegation may go

`performance.max_delegation_depth` caps how many levels a delegation chain may form. An agent delegates a worker, the worker delegates again, and each further hop is one more level. A chain that asks for one level more than the cap is refused with a named error — the extra work never starts, rather than starting and being cut off part-way.

- A workspace **Team** edge can set a tighter depth for a particular agent pair. When both a global value and an edge value exist, the smaller one wins.
- With nothing set, the code still caps a chain at **ten** levels.
- The depth caps how deep a chain of delegations can go, not how many helpers one agent may start — a ten-level chain can still start many helpers.

**Upgrading from the old default of 3.** If a workspace was saved while the default was still 3, the first start after upgrading raises each stored edge depth of 3 to the new default **10**, once, and writes one summary line to the log (`one-time delegation depth default migration`). A depth of 3 set on a workspace after it was raised is a deliberate choice and is kept until you change it. This upgrade is usually complete after that first start, but not always:

- **A workspace that could not be saved is retried.** If Omnipus can read a workspace's delegation settings but cannot save the raised value (for example, because of a file permission problem), the log shows an error and Omnipus still starts. Once you fix the cause, the next start raises only the workspaces that were not raised yet.
- **A workspace that could not be read at the first start is never raised.** If its delegation settings were unreadable or damaged at that start, Omnipus leaves them exactly as they are and counts them in `records_skipped` in the summary line; when it finds the damage while raising a workspace, it also logs a warning naming it (`record skipped permanently`). Later starts do not try again.
- **A workspace interrupted by a crash is left at its old value on purpose.** If Omnipus stopped during the moment it was raising one workspace, the next start cannot tell whether that 3 is the old default or a 3 you set afterwards. It keeps the 3 and logs a warning naming the workspace (`an interrupted record still holds depth 3`). It never raises it later.

**What to do.** If the summary line shows `records_skipped` above 0, or the log shows either warning, open each affected workspace's **Team** tab, check the delegation depths, and set the depth you want. A depth you set yourself is never changed by the upgrade. When it is unclear which workspace is affected, check every workspace's Team tab for depths of 3.

The protection that makes the upgrade run only once when two Omnipus instances start on the same data folder at the same time works on Linux and macOS only, not on Windows.

### How many agents run at once

`performance.max_parallel_agents` caps how many delegated sessions execute a turn at the same time. When the cap is reached, a new delegation is not refused — it is **queued**, and the delegating agent's tool result tells it its place in line. Queued sessions start in order as slots free up. Use `delegate` with `action: "stop_all"` and that child's `session_id` to stop the queued child and its descendants. `cancel` is not a delegate action; `/cancel` is the separate chat command for Stop all.

Leave the field blank and live available memory governs each new turn, reported in the Limits section as "automatic — bounded by available memory".

### How long a child may run

There is no time limit. A delegated session — like a task, a scheduled run, a plan's supervision turn or an external CLI worker — runs until it finishes or someone stops it: **Stop** in the chat, `delegate` with `action: "stop_all"`, or stopping the task or plan. Long work is never cut off part-way by a timer.

Older versions capped a delegated session at 30 minutes. An install that still has `performance.delegation_timeout_minutes`, `agents.defaults.timeout_seconds` or `schedules.run_timeout_seconds` in its `config.json`, or a `timeout_seconds` in an agent's settings file, keeps working: the values are ignored, and each is dropped the next time its file is saved. A `timeout_seconds` that an agent still passes to `delegate` is accepted and ignored.

Two limits stay, because they protect against a broken connection or command rather than limiting work:

- each request to a model provider has its own request timeout (`request_timeout` on the model, 120 seconds when unset); a provider that stops answering is retried or reported as **The model provider didn't answer this request in time** — the turn itself is not stopped by a clock. For a streamed reply the same limit applies only to waiting for the reply to start; once it is streaming, only a total silence (`stream_stall_timeout`, 300 seconds by default) ends it, so a long reply that keeps arriving is never cut off;
  - when an agent has a fallback model, each attempt on each model has its own limit. With `request_timeout` set on a model, that value is the limit for that model, longer or shorter than the default. Without it, the limit grows with the size of the request: 120 seconds for a request of up to about 100,000 tokens, 60 seconds more for every further 100,000 tokens or part of them, and never more than 10 minutes — for example about 6 minutes for a 440,000-token request. A very large conversation is therefore no longer cut off after 120 seconds on every model. The silence limit (`stream_stall_timeout`) is unchanged;
- a shell command run by an agent has its own `timeout_seconds` (default 300 seconds), and the environment-setup tool has its own job timeout.

### Which new agents get a self-line

When an agent joins a workspace team — by any route, including a save that carries a complete list of lines — Omnipus seeds it a **self-line** — a trust line from the agent to itself, which is what allows handing a fresh piece of work back to the same agent. It seeds one for every agent that joins, except the ids on an exclusion list (a Team save that carries a complete list of lines still gets the new member's self-line). That list is a private, file-only key:

- `workspace_seed_defaults.self_edge.exclude_agent_ids` in `config.json`.

There is no screen for it. It ships as `["judge", "plansupervisor"]` — the two hidden engine agents — so an ordinary install never seeds them a self-line. Three cases decide what it means:

| The key | Effect |
|---|---|
| Absent (the key, or the whole `workspace_seed_defaults` block, is missing) | The shipped list applies. A fresh install and an install that never wrote the key behave the same |
| An empty list `[]` | **Nobody is excluded by this list.** It does not make the two hidden agents team members: they cannot be added to a workspace team, so they still get no workspace self-line. Every ordinary agent that joins a team is seeded one |
| A list of ids | Exactly those ids are excluded. Adding an ordinary agent such as `"mia"` takes effect with no code change and no restart beyond the usual config reload |

Two limits on what the key does:

- **It governs future seeds only.** It is a default for the lines the product creates when a workspace is made or a team grows. It never edits the lines a workspace already has, never re-creates a line somebody removed while the agent stays on the team (an agent removed from the team and added back is a new member and is seeded again), and never affects who may currently delegate to whom. To change an agent's *current* lines, edit them in the workspace [Team](workspaces.md#how-to-set-who-may-delegate-to-whom) panel.
- **It is not editable through the settings API.** It is left out of what the settings API returns, and a generic settings write refuses it. Edit `config.json` by hand.

### Steer, respond and redirect message size

When one agent sends a message into another's turn — `delegate` with `action: steer`, `respond`, or `redirect` — the message is bounded by these keys in the `session_messaging` block of `config.json`:

- `steer_body` — the largest message body, in bytes. Default **65,536**. Group chats use it only up to 65,536 bytes: a smaller value lowers the group message limit (for people and agents alike), a larger one is clamped to 65,536 for groups ([group chats](group-chats.md#who-receives-your-message)). Messages between sessions use the value as it is.
- `steer_rate` — how many steer, respond or redirect messages one agent may send to the same target per minute. Default **60**.
- `steer_aggregate_body` — the most message bytes that may be waiting for one conversation at once, added up across its waiting ordinary messages. Default **1,048,576** (1 MiB). A message that would take the waiting total past this is refused with a named error and is never trimmed; the total comes back down as the agent reads what is waiting. At most 200 messages may wait for one conversation, whichever limit is reached first. Wake-ups and other control messages do not count.

**Messages you type into a chat while its agent is working behave differently today.** They wait in that chat's queue and reach the agent at its next step. The same 1 MiB total and 200-message limit apply to that queue, but a message that does not fit is not refused: it runs as the next turn, after the current one ends. Stop does not cancel it — Stop ends the current turn, the messages that were waiting in the queue are dropped with a "please resend" notice, and the message that did not fit then starts running. This is tracked in [#1503](https://github.com/elicify-ai/omnipus/issues/1503), which will make such a message refused visibly instead.

All are read from the live config, so editing `config.json` and letting it reload changes the real limit without a restart. Apart from the chat case above, a message beyond any bound is refused with a named error — it is never silently trimmed. Report messages a child sends up to its parent use their own separate ceilings and are not affected by these keys.

### Safety stops that are not settings

Three protections are fixed in the code on purpose. They guard against malformed data — a corrupted or looping parent chain, a gap in the live stream — not against any normal amount of use, and there is no setting for them:

- The server walks at most **4096** steps up the chain of parents when it classifies or launches a session, so a sessions file that has somehow formed a loop cannot make it walk forever.
- When it checks who may steer a session, it climbs at most **64** ancestors — again a guard against a corrupt or looping chain, far beyond any real delegation depth.
- The side panel holds at most **50** pending status updates for children whose "started" signal has not arrived yet, so a gap in the stream cannot grow memory without bound.

## Data & retention

**How long data is kept** holds two settings. Cleanup runs daily while the gateway is running, not at a fixed nighttime hour. Chat-history cleanup also runs when the gateway starts. A successful retention change is read by subsequent cleanup passes.

| Setting | Range | Default |
|---|---|---|
| Keep chat history for | 1 to 365 days | 90 |
| Keep memory for | 1 to 730 days | 180 |

**Keep forever**, next to the memory day count, stops age-based cleanup of memory (the agents' reflections on past chats); the day count is switched off and kept for later. When you turn it off again, older reflections are deleted at the next cleanup pass. If `config.json` sets the older `storage.retention.disabled` switch, nothing is deleted at all: the section says chat history and memory are both kept forever because of a setting in the configuration file, and that the day counts apply once it is removed. You can still change the day counts.

These values are written only through Settings (or `GET` / `PUT /api/v1/settings/retention`); a whole-configuration `PUT /api/v1/config` that would change them is refused.

**Clear all chat history** asks for one confirmation and attempts to delete ordinary chats. Main chats and their helper sessions are kept. A running chat is stopped first; a chat that cannot be stopped, read or removed is kept and reported in the result. Deletion cannot be undone.

## Keys & secrets

Adding a key and re-encrypting the vault ask for your password. Deleting a key asks one confirmation, **Delete this key?**, but not the password.

The gateway token, under Gateway → Access, is used by apps and programs to sign in. **Rotate…** asks first, because every app and program must then sign in again with the new token.

## Limits and things to watch

- Usage is measured in tokens. If a provider bill surprises you, the Usage screen tells you which agent and which model drove it, not what it cost.
- Session retention is 90 days by default, adjustable from 1 to 365 days in Data & retention. Older transcripts are deleted automatically, and that deletion is permanent.
- **Clear all chat history** in Data & retention attempts to delete ordinary chats. There is no undo; if you need to keep the history, copy the data folder outside the app before clearing it. Main chats are never deleted this way: a main chat (and the helper sessions that belong to it) goes only together with its agent. A chat that is still running is stopped first; if it will not stop, or it cannot be read or removed, it is kept, and the result tells you how many chats were kept and how many main chats were left alone.
- **Backup and Restore has been removed** from Settings → Data & retention and the API. Existing backup files on disk are not deleted. Use the [Export & Import](export-import.md) page (or the command line) to move agents between installations; packages are not a full backup of the data folder.
- A sign-in can expire. The provider row shows the state, and signing in again the same way restores it.
- Changes in Limits apply on the next turn, with no restart; gateway port changes do need a restart.
- Your Profile display settings stay in this browser. The workspace context, by contrast, lives on the server, so every browser and agent sees the same text.

## Related pages

- [security](security.md) — Protection, Sandbox & network and Keys & secrets: the sandbox, the encrypted credential vault, and the audit log.
- [tools](tools.md) — which tools agents may use, and the allow, ask, and deny permissions that govern them.
- [agents](agents.md) — giving one agent its own model instead of the default, the agent roster, and external workers.
- [memory](memory.md) — what the team remembers between chats, configured in Memory & recaps.
- [connectors](connectors.md) — connector accounts and their credentials, managed on the Connectors screen.
- [providers and models](providers-and-models.md) — how providers, sign-in methods, models, and defaults fit together.
