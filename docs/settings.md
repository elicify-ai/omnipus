# Settings

Settings connects model providers, sets agent limits, records your context, and shows usage. The sidebar user menu opens Settings, Profile, and Usage. Opening Settings stays on Settings. If the app is still choosing your workspace after you sign in, that choice does not replace a Settings page you already opened.

## What it is

The Settings screen is a row of tabs: Providers, Models, System agents, Integrations, Security, Gateway, Data, Memory, Devices, Performance, Chat, and About. The Devices tab appears only when your install enables device pairing, so most people never see it.

Most settings save when changed, with an indicator confirming the save. Secret changes ask you to re-type your password. Gateway listening changes require a restart.

Personal things are not tabs here. Profile is its own screen, holding your name, timezone, password, and the context your agents read about you. Usage is its own screen, showing token totals by agent, model, provider, and session.

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
5. If the variant uses sign-in, follow the dialog: it gives you a link and a code to approve on the provider's page, using any device. Sign-in is offered only on providers that can really sign in. OpenAI sign-in is on **ChatGPT (subscription)**, not on the API-key **OpenAI** provider. If you try to save sign-in on a provider that has none, Omnipus refuses and names the provider to use instead, for example `sign-in is not available for "OpenAI"; OpenAI sign-in lives on "ChatGPT (subscription)"`. An older install may still have a **Codex CLI** or **GitHub Copilot** row: those providers were removed, so the row shows an "Unknown provider" error and offers no sign-in or Manage action. Remove it, and use **ChatGPT (subscription)** for OpenAI sign-in.
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
| Settings, System agents | The agents the system runs on — Judge, Plan Supervisor and Omnipus — each opened in the shared agent editor |
| Settings, Integrations | Web-search and voice-input providers, with their keys |
| Settings, Security | Sandbox mode, credential vault, audit log, and the per-agent rate limits |
| Settings, Gateway | The address and port the gateway listens on; restarting it; god mode in its danger zone |
| Settings, Data | Session retention, storage numbers, backups, clearing sessions |
| Settings, Memory | What the team remembers: recap and retrospective settings |
| Settings, Devices | Pairing additional devices; hidden unless enabled on your install |
| Settings, Performance | How many tool calls an agent may make in one turn, how many agents may run at once, the goal completion budget, and idle-reminder and stalled-work check-in timers |
| Settings, Chat | Chat display, including the verbose view of tool calls; the **Verbose chat** toggle shows or hides the context-window retry notice, "Context window exceeded. Compressing history and retrying..." |
| Settings, About | Version and build information |
| Profile | Your name, timezone, font size, password, and workspace context |
| Usage | Token totals by period, agent, model, provider, and session; cache-hit percentage by agent and provider |

**Tool-result share limit**, under **Settings → Models**, sets the maximum share of the active model's context window that all tool results together may occupy before trimming is needed. The context window is how much information the model can read at once. This is a percentage of the whole window, not the smaller budget left after reserving space for output and instructions, and it replaces the absolute character count. The default is **50%**. Enter a percentage **greater than 0% and no more than 100%**; fractional percentages are accepted.

For scripts, the same setting is `context.tool_result_share_fraction` in configuration and `tool_result_share_fraction` on `GET` / `PUT /api/v1/settings/context`. Send the fraction, not the percentage: `0.125` means 12.5%. An omitted field in a partial update keeps the saved value; it does not reset to 50%. Null, text, booleans, zero and values outside the interval are rejected. A successful update takes effect on the next turn without a restart.

The existing Usage screen counts recorded agent-turn-loop tokens, not money. Auxiliary provider calls such as automatic recaps, fallback goal compilation and audio-model transcription are outside that per-call accounting path; see [observability](operations/observability.md#model-call-cache-accounting) for the coverage limits. Choose a period and use **By agent**, **By model**, **By provider**, or **By session** to see the breakdown. **Cached** is cache reads plus cache writes, already included in the total; **Uncached** is the rest. **By agent** and **By provider** also show **Cache hit**, the share of all input tokens read from the provider's cache. For example, a ratio of `0.8` appears as **80%**. A row with no input tokens shows no percentage, not **0%**. These numbers are on Usage, not a separate caching page. A ChatGPT sign-in call still counts tokens but is not given a dollar cost. See the provider's invoice for costs.

For **ChatGPT (subscription)**, Omnipus uses the same conversation identifier on successive calls to help the provider reuse cached input. This routing identifier is not built from credentials or message text. Reuse is not guaranteed: **Cache hit** measures the share of input tokens actually read from cache, not the percentage of calls with any cache reuse.

There is no longer a computed default for `performance.max_parallel_agents`. Leave the field blank to let live available memory govern each new agent turn; the Performance tab reports this as "automatic — bounded by available memory". Set a positive whole number when you need an explicit cap.

If the host cannot measure available memory, Omnipus holds agent concurrency at a floor of **two** turns. It refuses to grow, never to run: the first two turns can start, while a third concurrent turn is refused. Set `performance.max_parallel_agents` explicitly if the host can safely support more.

The rate limits live in Settings, Security, under its advanced section. Two numbers, both per agent: model calls per hour, and tool calls per minute. Leave either blank for no limit.

Profile holds one setting your agents read every turn: **Workspace Context**. It is a free-text page about you — your role, your preferences, how you like answers — saved automatically and shared with all agents. Your display name, timezone, and font size are personal display choices stored in this browser, so they do not follow you to another machine.

## System agents

**Settings → System agents** lists Judge, Plan Supervisor and Omnipus. Judge and Plan Supervisor are internal engine agents and cannot join workspace teams. Omnipus is a hidden member of the default workspace only; open its fixed sidebar entry rather than looking for it in Team. These built-in agents cannot be deleted.

**Edit** opens the shared agent editor, including Omnipus's model setting. You can repair its model here without a working model conversation. Judge and Plan Supervisor also have editable instructions; their capabilities remain fixed. There is no default-agent setting. The editor saves on its own; if the answer to a save is lost, it says **Save not confirmed — it may or may not have been stored.** with **Retry** instead of claiming it saved. The Omnipus chat's gear button opens this tab directly. While the list loads it says **Loading system agents…**; if it cannot load, the tab says **Could not load the system agents.** with **Retry**; if a refresh fails after it loaded, the rows stay with **Could not refresh — this list may be out of date.** and **Retry**.

## Goal timers

The goal timer controls live in **Settings → Performance**, beside the goal completion budget. They govern when the existing keeper can start another work turn, not how long that turn may run.

| Control | Default | Accepted values | What it controls |
|---|---|---|---|
| Idle reminder quiet window, in seconds | 60 seconds | Whole numbers from 30 to 3600 | Waiting before an ordinary reminder when a goal is idle. There is no quiet-off switch; zero is invalid. |
| Stalled-work check-ins | On | On or off | The only off switch for busy-work check-ins. Turning it off does not stop ordinary idle reminders. |
| Stalled-work check-in interval, in minutes | 30 minutes | Whole numbers from 5 to 1440 | Waiting without new progress while the goal's own background work remains busy. Turning check-ins off disables this input visually but retains its value; the same bounds apply even while off. |

The clocks are independent. A five-minute busy-work check-in can become due inside a 3600-second idle quiet window. Busy observation runs on each sweep, not only after idle quiet expires. Check-ins still follow the one-per-observed-stall and fresh-progress re-arm rules in [Goals](goals.md#one-check-in-for-stalled-background-work). Stop, unanswered questions, claim pauses and the three-failure/five-no-progress brakes continue to take precedence.

A successfully applied change is live for the next keeper sweep without restarting the gateway or replacing the goal. The sweep remains fixed at **30 seconds**: a due reminder or check-in can be noticed up to **30 seconds late**. That is scheduling delay, not a guarantee of response time or completion. Reserving or sending a busy check-in alone does not restart the idle quiet clock; actual activity and the admitted turn use their existing activity rules.

Changing a timer asks you to confirm it first. **Cancelling that confirmation discards the pending change it was for**, so that value is not applied later by a different save. The field then shows the value Omnipus has for that setting: normally the value Omnipus is running with, or the saved value while an earlier save is still waiting to be applied — the page says so in its **saved, but not applied yet** notice.

If you edit a field again while an earlier save is still on its way, the newer value stays on screen and still needs a confirmation of its own before it is sent. The older save finishing does not replace the newer value. On a local install, if a newer valid timer edit has already settled while the earlier save is pending, a fresh password prompt opens automatically after that save finishes, whether it succeeds or fails. The newer value is not sent until you confirm again; it does not reuse the earlier approval.

Each edit queues the setting it changed, but several queued edits can be confirmed and sent together, and a setting that is not part of a request keeps the value it already had.

For scripts, `GET /api/v1/performance` always returns resolved values for `goal_reminder_quiet_seconds`, `goal_stalled_checkin_enabled` and `goal_stalled_checkin_minutes`. `PUT /api/v1/performance` updates only fields supplied in that request and retains every omitted setting. The endpoint still requires administrator access. In local authentication mode it also requires password re-authentication. In platform authentication mode there is no local password step; the web app asks you to confirm the change. An empty update is invalid. The numeric fields reject zero, fractions and out-of-range values, including a minutes value of zero while check-ins are off; the switch accepts a boolean, not text or a number. In the settings file, the keys live under `planning`. Missing or null keys use their defaults, whereas an explicit out-of-range number is a configuration error, not a request to restore the default.

If a save reports **saved, but not applied yet**, do not treat it as a successful live update. The error identifies the fields that request changed. At the **refresh** stage the settings file has the new values but the running configuration—and the keeper's timers—still uses the old values until a successful reload or restart. At the **reload** stage the running configuration has changed but the agent registry could not finish reloading: timer sweeps read that new running configuration, even though other agent settings may remain unapplied. Nothing is rolled back. See [When a save is not applied yet](#when-a-save-is-not-applied-yet).

## Tool calls per turn

**Max tool calls per turn**, on the Performance tab, sets an agent's normal work budget for each turn. For native agents, it counts working rounds, not individual tool calls: a round can run several calls. Pending follow-ups or winding down can extend the current turn beyond that normal budget, up to a hard safety bound of twice the configured number of rounds. A task run can contain several turns, so this is not a total budget for the whole task.

When a native chat turn exhausts its budget without a final answer, Omnipus ends that turn and records a system notice explaining the limit. This notice is generated by Omnipus, not written by the model, and carries no model attribution. External workers use their command-line tool's turn cap and error reporting instead; see [Workers and delegation](agents.md#workers-and-delegation).

If a native chat turn ends at this limit with an accepted follow-up still unanswered, Omnipus automatically starts one fresh turn in the same conversation to handle it. This also covers an instruction that had already reached the model. The continuation uses the saved conversation, keeping the instruction and completed tool results; recovery does not resend the instruction or replay completed tool calls. You do not need to resend it merely because the previous turn reached its limit. Without new input, this recovery does not keep scheduling more turns for itself. If that continuation also runs out of budget, you can ask the agent to continue. With no follow-up to handle, this steering recovery starts no new turn. Task and goal retry rules remain separate: a per-turn cutoff is not a Stop of the whole job.

A turn can also stop before the limit when the agent repeats itself without progress: after the same tool call with the same arguments succeeds eight times in a row, or when, in a round where nothing else succeeded, it asks again for a call that already failed five times with the same arguments and was then blocked. The agent's last reply names the tool and says why it stopped; a task run in that state counts as a try that did not finish, so the task retries or ends Failed instead of staying in progress.

- The limit applies to every agent, including the built-in roster and [external workers](agents.md#workers-and-delegation).
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

### A save that reloads the configuration can pause

When a save makes the gateway reload its configuration, the reload first waits for a background queued-task check or mail check that is already in progress, so it is not cut off halfway. Each of the two checks gets up to 10 seconds, one after the other. Do not repeat the save while the first one is still pending. If a check is still running when its wait runs out, the reload goes ahead without it and the log shows a warning that names the poller (for example, "the mail poller was still in a mail check after its stop wait"). That check finishes on its own; if the warning repeats, check the mailbox connection or, for the queued-task poller, the task store.

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

How much work one agent can hand to another is bounded in two ways: a Performance-tab setting for how many delegated sessions may run at once, and a chain-depth cap set by `performance.max_delegation_depth` in `config.json` (a workspace **Team** edge can set a tighter depth for a particular agent pair). Each behaves sensibly when left blank. There is no time limit: a delegated session runs until it finishes or someone stops it (see "How long a child may run" below).

### How deep delegation may go

`performance.max_delegation_depth` caps how many levels a delegation chain may form. An agent delegates a worker, the worker delegates again, and each further hop is one more level. A chain that asks for one level more than the cap is refused with a named error — the extra work never starts, rather than starting and being cut off part-way.

- A workspace **Team** edge can set a tighter depth for a particular agent pair. When both a global value and an edge value exist, the smaller one wins.
- With nothing set, the code still caps a chain at **three** levels.

### How many agents run at once

`performance.max_parallel_agents` caps how many delegated sessions execute a turn at the same time. When the cap is reached, a new delegation is not refused — it is **queued**, and the delegating agent's tool result tells it its place in line. Queued sessions start in order as slots free up. Use `delegate` with `action: "stop_all"` and that child's `session_id` to stop the queued child and its descendants. `cancel` is not a delegate action; `/cancel` is the separate chat command for Stop all.

Leave the field blank and live available memory governs each new turn, reported on the Performance tab as "automatic — bounded by available memory".

### How long a child may run

There is no time limit. A delegated session — like a task, a scheduled run, a plan's supervision turn or an external CLI worker — runs until it finishes or someone stops it: **Stop** in the chat, `delegate` with `action: "stop_all"`, or stopping the task or plan. Long work is never cut off part-way by a timer.

Older versions capped a delegated session at 30 minutes. An install that still has `performance.delegation_timeout_minutes`, `agents.defaults.timeout_seconds` or `schedules.run_timeout_seconds` in its `config.json`, or a `timeout_seconds` in an agent's settings file, keeps working: the values are ignored, and each is dropped the next time its file is saved. A `timeout_seconds` that an agent still passes to `delegate` is accepted and ignored.

Two limits stay, because they protect against a broken connection or command rather than limiting work:

- each request to a model provider has its own request timeout (`request_timeout` on the model, 120 seconds when unset); a provider that stops answering is retried or reported as **The model provider didn't answer this request in time** — the turn itself is not stopped by a clock. For a streamed reply the same limit applies only to waiting for the reply to start; once it is streaming, only a total silence (`stream_stall_timeout`, 300 seconds by default) ends it, so a long reply that keeps arriving is never cut off;
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

- `steer_body` — the largest message body, in bytes. Default **65,536**. Group chats use it only up to 65,536 bytes: a smaller value lowers the group message limit (for people and agents alike), a larger one is clamped to 65,536 for groups. Messages between sessions use the value as it is.
- `steer_rate` — how many steer, respond or redirect messages one agent may send to the same target per minute. Default **60**.
- `steer_aggregate_body` — the most message bytes that may be waiting for one conversation at once, added up across its waiting ordinary messages. Default **1,048,576** (1 MiB). A message that would take the waiting total past this is refused with a named error and is never trimmed; the total comes back down as the agent reads what is waiting. At most 200 messages may wait for one conversation, whichever limit is reached first. Wake-ups and other control messages do not count.

All are read from the live config, so editing `config.json` and letting it reload changes the real limit without a restart. A message beyond any bound is refused with a named error — it is never silently trimmed. Report messages a child sends up to its parent use their own separate ceilings and are not affected by these keys.

### Safety stops that are not settings

Three protections are fixed in the code on purpose. They guard against malformed data — a corrupted or looping parent chain, a gap in the live stream — not against any normal amount of use, and there is no setting for them:

- The server walks at most **4096** steps up the chain of parents when it classifies or launches a session, so a sessions file that has somehow formed a loop cannot make it walk forever.
- When it checks who may steer a session, it climbs at most **64** ancestors — again a guard against a corrupt or looping chain, far beyond any real delegation depth.
- The side panel holds at most **50** pending status updates for children whose "started" signal has not arrived yet, so a gap in the stream cannot grow memory without bound.

## Limits and things to watch

- Usage is measured in tokens. If a provider bill surprises you, the Usage screen tells you which agent and which model drove it, not what it cost.
- Session retention is 90 days by default, adjustable from 1 to 365 days on the Data tab. Older transcripts are deleted automatically, and that deletion is permanent.
- **Clear all sessions** on the Data tab deletes every ordinary chat at once. There is no undo; make a backup first if the history matters. Main chats are never deleted this way: a main chat (and the helper sessions that belong to it) goes only together with its agent. A chat that is still running is stopped first; if it will not stop, or it cannot be read or removed, it is kept, and the result tells you how many chats were kept and how many main chats were left alone.
- Restoring a backup overwrites current data and needs a gateway restart to take effect.
- A sign-in can expire. The provider row shows the state, and signing in again the same way restores it.
- Model changes on the Models tab apply on the next turn, with no restart; gateway port changes do need a restart.
- Your Profile display settings stay in this browser. The workspace context, by contrast, lives on the server, so every browser and agent sees the same text.

## Related pages

- [security](security.md) — the rest of the Security tab: the sandbox, the encrypted credential vault, and the audit log.
- [tools](tools.md) — which tools agents may use, and the allow, ask, and deny permissions that govern them.
- [agents](agents.md) — giving one agent its own model instead of the default, and the agent roster.
- [memory](memory.md) — what the team remembers between chats, behind the Memory tab.
- [connectors](connectors.md) — connector accounts and their credentials, managed on the Connectors screen.
- [providers and models](providers-and-models.md) — how providers, sign-in methods, models, and defaults fit together.
