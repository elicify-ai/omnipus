# Settings

Settings connects model providers, sets agent limits, records your context, and shows usage. The sidebar user menu opens Settings, Profile, and Usage.

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

## Where each setting lives

Each tab and neighbor screen has one job.

| Screen or tab | What it is for |
|---|---|
| Settings, Providers | Connect providers, manage keys and sign-ins, set the default model |
| Settings, Models | Context budget: how much of each tool result stays in the conversation, and which context length each model is assumed to have |
| Settings, Integrations | Web-search and voice-input providers, with their keys |
| Settings, Security | Sandbox mode, credential vault, audit log, and the per-agent rate limits |
| Settings, Gateway | The address and port the gateway listens on; restarting it; god mode in its danger zone |
| Settings, Data | Session retention, storage numbers, backups, clearing sessions |
| Settings, Memory | What the team remembers: recap and retrospective settings |
| Settings, Devices | Pairing additional devices; hidden unless enabled on your install |
| Settings, Performance | How many agents may run at once, how deep delegation may go, and how long a delegation may run |
| Settings, Chat | Chat display, including the verbose view of tool calls |
| Settings, About | Version and build information |
| Profile | Your name, timezone, font size, password, and workspace context |
| Usage | Token totals by period, agent, model, and session |

Usage counts tokens, not money. It shows totals by period, agent, model, and session, split into cached and uncached tokens. Cached tokens are included in the total. See the provider's invoice for costs.

There is no longer a computed default for `performance.max_parallel_agents`. Leave the field blank to let live available memory govern each new agent turn; the Performance tab reports this as "automatic — bounded by available memory". Set a positive whole number when you need an explicit cap.

If the host cannot measure available memory, Omnipus holds agent concurrency at a floor of **two** turns. It refuses to grow, never to run: the first two turns can start, while a third concurrent turn is refused. Set `performance.max_parallel_agents` explicitly if the host can safely support more.

The rate limits live in Settings, Security, under its advanced section. Two numbers, both per agent: model calls per hour, and tool calls per minute. Leave either blank for no limit.

Profile holds one setting your agents read every turn: **Workspace Context**. It is a free-text page about you — your role, your preferences, how you like answers — saved automatically and shared with all agents. Your display name, timezone, and font size are personal display choices stored in this browser, so they do not follow you to another machine.

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
