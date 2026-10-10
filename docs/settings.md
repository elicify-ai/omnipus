# Settings

Settings connects model providers, sets agent limits, records your context, and shows usage. The sidebar user menu opens Settings, Profile, and Usage. Opening Settings stays on Settings. If the app is still choosing your workspace after you sign in, that choice does not replace a Settings page you already opened.

## What it is

The Settings screen is a row of tabs: Providers, Models, Integrations, Security, Gateway, Data, Memory, Devices, Performance, Chat, and About. The Devices tab appears only when your install enables device pairing, so most people never see it.

Most settings save when changed, with an indicator confirming the save. Secret changes ask you to re-type your password. Gateway listening changes require a restart.

Personal things are not tabs here. Profile is its own screen, holding your name, timezone, password, and the context your agents read about you. Usage is its own screen, showing token totals by agent, model, and session.

## When you would use it

- You are setting up Omnipus for the first time and need a working model behind every chat.
- You want to switch providers, use a subscription you already pay for, or check which models your account can actually reach.
- You want to see what your agents consumed this week.
- You want agents to know your role and preferences without repeating yourself in every chat.
- You want to change how long transcripts are kept, back up your data, or cap how hard agents can call a model.

Two jobs that look like settings live on their own pages: what agents may do is [tool permissions](tools.md), and how they are confined is the [sandbox](security.md).

## How to connect a model provider

1. Open Settings from the sidebar's user menu. The Providers tab opens first.
2. Click **Connect a provider**. Search the catalog or browse it by company. The [providers and models](providers-and-models.md) page explains the choices without duplicating the live list.
3. Pick a provider and one of its available variants: API, coding plan, or account sign-in.
4. If the variant wants an API key, paste the key and click **Connect**. The row appears with its status.
5. If the variant uses sign-in, follow the dialog. It either gives you a link and code, or a local command such as `copilot login` followed by **Check sign-in**. With the command flow, the vendor's tool keeps the credential.
6. Set the **default model** on the card at the top of the tab. That is the model new chats use unless an agent has its own.
7. Use **Test** to re-check the connection, **Check with my account** to list available models, or **Remove provider** to delete it. Removing the default provider first asks for its replacement.

API keys are stored encrypted on this server, never in the main configuration file. See [security](security.md) for how the encrypted store works.

## Web search

In **Settings** → **Integrations**, **Web Search** says: "Your agents search with the default service. If it fails, the fallback takes over." When the search roles are available, two cards appear:

| Card | What you can do |
|---|---|
| **Default search** | Click **Change** to choose the default service. When that service reports a depth cap, the card shows a read-only **Depth cap:** line. |
| **Fallback** | Click **Change** to choose a different service or **None**. The picker never offers the current default. When **None** is selected and DuckDuckGo is usable, the card says "DuckDuckGo works without a key — try it as your fallback." |

Only usable services can be chosen. Unusable services shown in the picker are disabled, with "Add a key first", "Key not reaching search", or "Needs configuration" beside them. If a chosen keyed service loses its key, its card warns that searches will fail or that the fallback will not run. Click **Fix** to open that service's key editor.

The service list below the cards puts ready services first. Services needing a key have **Add key** or **Edit key**. With the cards present, enter the key and click **Save key**. On a local install, you must re-type your password; on a hosted or desktop install, you confirm the change instead. A successful key save switches that service on without changing your default or fallback choice.

If an integration save fails, its error message shows the server's reason when one is provided.

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

Removal is refused without changes if search roles are still undecided, another connection shares the key, or the service uses a key reference set outside Settings. The message explains the fix: choose default and fallback services, give the other connections their own key, or remove the manually configured reference first. A later-stage failure can leave partial changes; its error says what happened, and the refreshed row shows the current state. Do not assume an error means the key or service was left unchanged.

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
| **Could not read the saved key from the credential store. Unlock or repair it, then try again.** | Unlock or repair the credential store before checking again. |
| **The saved key is not available to the running service. Reload the configuration, then try again.** | Reload the configuration before checking again. |
| **The server is unavailable. Please try again in a moment.** | No specific server recovery message was provided. Wait, then check manually after the cooldown. |
| **You can check again in {N} seconds.** | Another check was refused by the local limit. Wait for the displayed countdown; no check is scheduled automatically. |

There can be only one check in progress for each service, with a 30-second cooldown per service on this Omnipus instance, including failed checks. A service may request a longer wait. After the cooldown, click **Check connection** for a fresh attempt; an old success is not reused as a new check.

If the server cannot complete a check, the row shows its own recovery message when one is provided, rather than replacing instructions to unlock, repair or reload with a generic unavailable message.

Connection results are temporary and belong to one service. Finishing another service's check does not clear them. Saving or removing a key clears only that service's old result; a changed service row also clears its result. Checks do not change your roles, switch off a service, or change ordinary fallback or native-model search behaviour.

A key change detected by the server while checking rejects the old outcome. There is a remaining short window after the check response but before the final status refresh: if a replacement key leaves the row looking identical, the previous key's result may still appear. Run a fresh check after the cooldown to verify the current key.

SearXNG is no longer offered: on upgrade, Omnipus removes its old configuration, clears it as a saved default, and sets a saved SearXNG fallback to **None**.

## Where each setting lives

Each tab and neighbor screen has one job.

| Screen or tab | What it is for |
|---|---|
| Settings, Providers | Connect providers, manage keys and sign-ins, set the default model |
| Settings, Models | Context budget: per-result caps, the combined share of the model's context window used by tool results, and model context-length overrides |
| Settings, Integrations | Web-search and voice-input providers, with their keys |
| Settings, Security | Sandbox mode, credential vault, audit log, and the per-agent rate limits |
| Settings, Gateway | The address and port the gateway listens on; restarting it; god mode in its danger zone |
| Settings, Data | Session retention, storage numbers, backups, clearing sessions |
| Settings, Memory | What the team remembers: recap and retrospective settings |
| Settings, Devices | Pairing additional devices; hidden unless enabled on your install |
| Settings, Performance | How many tool calls an agent may make in one turn, how many agents may run at once, how deep delegation may go, and how long a delegation may run |
| Settings, Chat | Chat display, including the verbose view of tool calls; the **Verbose chat** toggle shows or hides the context-window retry notice, "Context window exceeded. Compressing history and retrying..." |
| Settings, About | Version and build information |
| Profile | Your name, timezone, font size, password, and workspace context |
| Usage | Token totals by period, agent, model, and session |

**Tool-result share limit**, under **Settings → Models**, sets the maximum share of the active model's context window that all tool results together may occupy before trimming is needed. The context window is how much information the model can read at once. This is a percentage of the whole window, not the smaller budget left after reserving space for output and instructions, and it replaces the absolute character count. The default is **50%**. Enter a percentage **greater than 0% and no more than 100%**; fractional percentages are accepted.

For scripts, the same setting is `context.tool_result_share_fraction` in configuration and `tool_result_share_fraction` on `GET` / `PUT /api/v1/settings/context`. Send the fraction, not the percentage: `0.125` means 12.5%. An omitted field in a partial update keeps the saved value; it does not reset to 50%. Null, text, booleans, zero and values outside the interval are rejected. A successful update takes effect on the next turn without a restart.

Usage counts tokens, not money. It shows totals by period, agent, model, and session, split into cached and uncached tokens. Cached tokens are included in the total. See the provider's invoice for costs.

There is no longer a computed default for `performance.max_parallel_agents`. Leave the field blank to let live available memory govern each new agent turn; the Performance tab reports this as "automatic — bounded by available memory". Set a positive whole number when you need an explicit cap.

If the host cannot measure available memory, Omnipus holds agent concurrency at a floor of **two** turns. It refuses to grow, never to run: the first two turns can start, while a third concurrent turn is refused. Set `performance.max_parallel_agents` explicitly if the host can safely support more.

The rate limits live in Settings, Security, under its advanced section. Two numbers, both per agent: model calls per hour, and tool calls per minute. Leave either blank for no limit.

Profile holds one setting your agents read every turn: **Workspace Context**. It is a free-text page about you — your role, your preferences, how you like answers — saved automatically and shared with all agents. Your display name, timezone, and font size are personal display choices stored in this browser, so they do not follow you to another machine.

## Tool calls per turn

**Max tool calls per turn**, on the Performance tab, caps how many tool steps any agent may take in one turn. A turn is one message, one task run, or one heartbeat run. When an agent reaches the limit without a final answer, the turn stops and the agent replies that it hit its limit of tool steps for one turn. You can then ask it to continue.

- The limit applies to every agent, including the four built-in agents and [external workers](agents.md#workers-and-delegation).
- It accepts a whole number from **1 to 1000**. The shipped value is **200**.
- In `config.json` it is `agents.defaults.max_tool_iterations`.
- An agent can have its own lower limit on its profile. It can never have a higher one. See [agents](agents.md#how-to-lower-one-agents-tool-call-limit).
- A turn that is already running keeps the limit it started with. The new value applies from the next turn. No restart is needed.

### How to change it

1. Open Settings, then the **Performance** tab.
2. Find the **Tool calls per turn** card and type a new number into **Max tool calls per turn**. It saves by itself a moment after you stop typing.
3. If the new value is lower than the current one and some agents have their own higher limit, a dialog lists each of those agents as *old value → new value*. Click **Lower limits** to go ahead, or cancel to change nothing.
4. Re-type your password when asked. (Where there is no local password to re-type, you confirm the change instead.)

Every change is recorded in the audit log as a `security_setting_change` event. The global limit is recorded as `agents.defaults.max_tool_iterations`, and each lowered agent as `agents.<agent id>.max_tool_iterations`, with the old and new values.

### Lowering and raising behave differently

| You change the limit | What happens to agents with their own limit |
|---|---|
| **Lower**, for example from 200 to 50 | Every agent whose own limit is above 50 is lowered to 50, after you confirm the list in the dialog. Agents at or below 50 are not touched. After the save, a message names each agent that was lowered. |
| **Raise**, for example from 50 to 200 | No dialog opens, and no agent's own limit changes. An agent you lowered to 50 stays at 50. Raising the limit again does not restore values an earlier lowering replaced. |

If an agent's own limit changes between the dialog opening and your confirmation, nothing is saved. The dialog shows **"The list of affected agents changed — review and confirm again."** with the new list, and you confirm again.

### When a save is not applied yet

Rarely, a save is written but not yet in force. A warning then starts with **"Saved, but not applied yet"**. It names only the settings that save changed (for this card, "the tool-iteration limit"). The agents that save lowered are named at the moment of the save — in a status toast and inline under the field — but that naming does not survive a page reload: reload, and the list under the field is gone, even though the "Saved, but not applied yet" warning itself keeps showing. The audit log is the lasting record of which agents were lowered and when. It comes in two forms:

| The warning reads | What happened |
|---|---|
| "Saved, but not applied yet — saved to the settings file; takes effect after a restart or reload: …" | `config.json` holds the new value, but the running configuration could not be refreshed. The Performance tab keeps showing the value you saved, with this notice, until the gateway reloads or restarts. |
| "Saved, but not applied yet: … the agent reload failed …" | The running configuration has the new value, but the agents could not be reloaded with it. Their next turns keep the old limit. |

Either way nothing is rolled back: the value is in `config.json`, agents it lowered are already lowered, and the change is in the audit log. The new limit applies after the next reload or a gateway restart.

### A saved value outside 1 to 1000

If `config.json` holds a value the setting does not accept, for example after a hand edit, Omnipus still starts. It runs with a corrected value in memory and does not change the file:

| Saved value | Limit in force |
|---|---|
| Missing, or below 1 (such as 0) | 200, the shipped value |
| Above 1000 (such as 5000) | 1000 |

The gateway log carries a warning at start-up, and the Performance tab shows a warning naming the saved value and the one in use. Save a value from 1 to 1000 in the field to replace the saved one.

### Changing it outside Settings

Only Settings, Performance changes this limit, because it asks for your password and shows you which agents a lowering affects. Other ways in are refused:

- The general configuration endpoint, `PUT /api/v1/config`, refuses a body that includes `agents.defaults.max_tool_iterations` with status 403 and "agents.defaults.max_tool_iterations is a blocked path — use the dedicated endpoint". Nothing in that body is saved. Other changes to `agents.defaults` through this endpoint keep the saved limit as it is.
- The `set_config` tool that agents use to change configuration from a chat refuses the key and points to Settings, Performance. To limit one agent from a chat, ask for that agent's own value instead; see [agents](agents.md#how-to-lower-one-agents-tool-call-limit).

### If you are upgrading

- **The environment variable is retired.** `OMNIPUS_AGENTS_DEFAULTS_MAX_TOOL_ITERATIONS` used to override the saved value on every start. Now, on the first start after upgrading, its value is copied into `config.json` once. From then on it is ignored, and the log says so at each start while it is still set. Change the limit in Settings and remove the variable. A value outside 1 to 1000 is copied as the nearest bound, 1 or 1000, with a warning. A value that is not a whole number is not copied; the log says so.
- **Agents with an old higher limit.** Earlier releases let an agent's own limit be higher than the global one. Upgrading does not change those stored values. Such an agent now runs at the global limit, its profile says its own value has no effect, and one warning at start-up lists every such agent. See [agents](agents.md#how-to-lower-one-agents-tool-call-limit).

## Delegation limits

Three numbers on the Performance tab govern how much work one agent can hand to another. Each is a separate setting, and each behaves sensibly when left blank.

### How deep delegation may go

`performance.max_delegation_depth` caps how many levels a delegation chain may form. An agent delegates a worker, the worker delegates again, and each further hop is one more level. A chain that asks for one level more than the cap is refused with a named error — the extra work never starts, rather than starting and being cut off part-way.

- A workspace **Team** edge can set a tighter depth for a particular agent pair. When both a global value and an edge value exist, the smaller one wins.
- With nothing set, the code still caps a chain at **three** levels.

### How many agents run at once

`performance.max_parallel_agents` caps how many delegated sessions execute a turn at the same time. When the cap is reached, a new delegation is not refused — it is **queued**, and the delegating agent's tool result tells it its place in line. Queued sessions start in order as slots free up. A `delegate cancel` drops a queued one before it ever starts.

Leave the field blank and live available memory governs each new turn, reported on the Performance tab as "automatic — bounded by available memory".

### How long a child may run

`performance.delegation_timeout_minutes` caps how long a delegated session may run across its whole life — a follow-up that wakes the child does not restart the clock. The default is **30 minutes**. A delegating agent can override that for one call with `timeout_seconds`; leaving that at zero uses the default.

### Which new agents get a self-line

When an agent joins a workspace team — by any route, including a save that carries a complete list of lines — Omnipus seeds it a **self-line** — a trust line from the agent to itself, which is what allows handing a fresh piece of work back to the same agent. It seeds one for every agent that joins, except the ids on an exclusion list (a Team save that carries a complete list of lines still gets the new member's self-line). That list is a private, file-only key:

- `workspace_seed_defaults.self_edge.exclude_agent_ids` in `config.json`.

There is no screen for it. It ships as `["judge", "plansupervisor"]` — the two hidden engine agents — so an ordinary install never seeds them a self-line. Three cases decide what it means:

| The key | Effect |
|---|---|
| Absent (the key, or the whole `workspace_seed_defaults` block, is missing) | The shipped list applies. A fresh install and an install that never wrote the key behave the same |
| An empty list `[]` | **Nobody is excluded by this list.** It does not make the two hidden agents (or Admin) team members: they cannot join a workspace team, so they still get no workspace self-line. Every ordinary agent that joins a team is seeded one |
| A list of ids | Exactly those ids are excluded. Adding an ordinary agent such as `"mia"` takes effect with no code change and no restart beyond the usual config reload |

Two limits on what the key does:

- **It governs future seeds only.** It is a default for the lines the product creates when a workspace is made or a team grows. It never edits the lines a workspace already has, never re-creates a line somebody removed while the agent stays on the team (an agent removed from the team and added back is a new member and is seeded again), and never affects who may currently delegate to whom. To change an agent's *current* lines, edit them in the workspace [Team](workspaces.md#how-to-set-who-may-delegate-to-whom) panel.
- **It is not editable through the settings API.** It is left out of what the settings API returns, and a generic settings write refuses it. Edit `config.json` by hand.

### Steer, respond and redirect message size

When one agent sends a message into another's turn — `delegate` with `action: steer`, `respond`, or `redirect` — the message is bounded by two keys in the `session_messaging` block of `config.json`:

- `steer_body` — the largest message body, in bytes. Default **65,536**.
- `steer_rate` — how many steer, respond or redirect messages one agent may send to the same target per minute. Default **60**.

Both are read from the live config, so editing `config.json` and letting it reload changes the real limit without a restart. A message beyond either bound is refused with a named error — it is never silently trimmed. Report messages a child sends up to its parent use their own separate ceilings and are not affected by these two keys.

### Safety stops that are not settings

Three protections are fixed in the code on purpose. They guard against malformed data — a corrupted or looping parent chain, a gap in the live stream — not against any normal amount of use, and there is no setting for them:

- The server walks at most **4096** steps up the chain of parents when it classifies or launches a session, so a sessions file that has somehow formed a loop cannot make it walk forever.
- When it checks who may steer a session, it climbs at most **64** ancestors — again a guard against a corrupt or looping chain, far beyond any real delegation depth.
- The side panel holds at most **50** pending status updates for children whose "started" signal has not arrived yet, so a gap in the stream cannot grow memory without bound.

## Limits and things to watch

- Usage is measured in tokens. If a provider bill surprises you, the Usage screen tells you which agent and which model drove it, not what it cost.
- Session retention is 90 days by default, adjustable from 1 to 365 days on the Data tab. Older transcripts are deleted automatically, and that deletion is permanent.
- **Clear all sessions** on the Data tab deletes every transcript at once. There is no undo; make a backup first if the history matters.
- Restoring a backup overwrites current data and needs a gateway restart to take effect.
- A sign-in can expire. The provider row shows the state, and signing in again the same way restores it.
- Model changes on the Models tab apply on the next turn, with no restart; gateway port changes do need a restart.
- Your Profile display settings stay in this browser. The workspace context, by contrast, lives on the server, so every browser and agent sees the same text.

## Related pages

- [security](security.md) — the rest of the Security tab: the sandbox, the encrypted credential vault, and the audit log.
- [tools](tools.md) — which tools agents may use, and the allow, ask, and deny permissions that govern them.
- [agents](agents.md) — giving one agent its own model instead of the default, and choosing the default agent.
- [memory](memory.md) — what the team remembers between chats, behind the Memory tab.
- [connectors](connectors.md) — connector accounts and their credentials, managed on the Connectors screen.
- [providers and models](providers-and-models.md) — how providers, sign-in methods, models, and defaults fit together.
