# Settings

Settings is where you connect the model providers your agents run on, set the limits they work under, tell them who you are, and watch what they spend. Three screens do this job, all reached from the user menu in the sidebar: Settings, Profile, and Usage.

## What it is

The Settings screen is a row of tabs: Providers, Models, Integrations, Security, Gateway, Data, Memory, Devices, Performance, Chat, and About. The Devices tab appears only when your install enables device pairing, so most people never see it.

Most settings save themselves the moment you change them, and a small indicator next to each one confirms the save. Two exceptions behave differently: anything that touches a secret (an API key, a credential) asks you to re-type your password before it is accepted, and gateway listening changes ask for a restart.

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
2. Click **Connect a provider**. A panel lists every provider Omnipus knows about, grouped by company, with a search field.
3. Pick a provider. Some companies offer more than one way in, shown as variants: a pay-as-you-go API, a coding plan, or sign-in with an account you already have. Where sign-in exists it is the pre-selected choice.
4. If the variant wants an API key, paste the key and click **Connect**. The row appears with its status.
5. If the variant uses sign-in, one of two flows follows, and the dialog tells you which:
   - You get a link and a code. Open the link, enter the code, approve. The dialog updates on its own; a code is valid for 15 minutes.
   - You get a command to run in a terminal on this machine, such as `copilot login`, and then click **Check sign-in**. The vendor's own tool keeps the credential; Omnipus never sees or stores it.
6. Set the **default model** on the card at the top of the tab. That is the model new chats use unless an agent has its own.
7. The row actions: **Test** re-checks the connection, **Check with my account** lists the models your account can genuinely use, and **Remove provider** deletes it. Removing the provider behind your default model asks you to pick a replacement default as part of the removal.

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
| Settings, Performance | How many agents may run at once |
| Settings, Chat | Chat display, including the verbose view of tool calls |
| Settings, About | Version and build information |
| Profile | Your name, timezone, font size, password, and workspace context |
| Usage | Token totals by period, agent, model, and session |

Usage answers "what did we spend". It counts tokens, not money: totals for the day, week, month, or all time, split into cached and uncached, with breakdowns by agent and by model, and a per-session list you can open. Cached tokens are part of the total, not added on top. Your provider's invoice is where tokens become dollars; Omnipus has no prices to multiply by.

The rate limits live in Settings, Security, under its advanced section. Two numbers, both per agent: model calls per hour, and tool calls per minute. Leave either blank for no limit.

Profile holds one setting your agents read every turn: **Workspace Context**. It is a free-text page about you — your role, your preferences, how you like answers — saved automatically and shared with all agents. Your display name, timezone, and font size are personal display choices stored in this browser, so they do not follow you to another machine.

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
