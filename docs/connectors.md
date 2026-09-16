# Connectors

Connectors let you talk to your agents from chat apps such as Telegram, Discord, and WhatsApp. You choose which workspace and agent answer each connected account.

## What it is

A connector is a bridge between an outside chat app and Omnipus. A message arrives through the connector, Omnipus chooses an agent, and the reply returns to the same conversation.

The screen is called **Connectors**. Some technical settings and addresses still use the older term `channel`.

Email works differently. An email mailbox belongs to one agent in one workspace. It is not a chat connector.

## When you would use it

- You want to reach an agent from your phone or your team's usual chat app.
- You want a particular account to be answered by one workspace agent.
- You want separate personal and work accounts on the same chat platform.
- You want an agent to read and send email through its own mailbox.

## How to connect a chat app

1. Open **Connectors**. Platforms that are not connected yet show **Configure**.
2. Select **Configure** for the platform you want. A panel opens with its required fields.
3. Enter the platform credentials. Use the platform guide below to find the values you need.
4. Choose a **Workspace** and an agent. The agent list contains members of that workspace.
5. Select **Save & Enable**. The connected account appears with an **Enabled** status.
6. To connect another account on the same platform, select **Add another…** beside its group.

## Choosing which agent answers

A workspace-bound connector sends every incoming message on that account to the agent you selected. The selected agent must remain a member of that workspace and must be able to answer chats.

An unbound connector can use a connector-level default agent. If that choice is empty, Omnipus uses the starred default agent from [Agents](agents.md).

More specific rules can override the unbound connector's choice. Omnipus checks them from most specific to least specific.

| Match | What it identifies | Priority |
|---|---|---|
| Peer | A particular person or a parent conversation | First |
| Guild | A Discord guild | After peer |
| Team | A Slack team | After guild |
| Account | One account on a platform | After team |
| Connector wildcard | Any account on one platform | After account |
| Default agent | Messages with no matching rule | Last |

The first matching rule decides which agent answers. Detailed rules are set in configuration rather than on the Connectors screen.

## Connectors you can set up

Each guide explains the credentials and platform-specific setup.

| Connector | What to know | Guide |
|---|---|---|
| Telegram | Uses a bot token | [Telegram](connectors/telegram.md) |
| Discord | Connects a Discord bot | [Discord](connectors/discord.md) |
| WhatsApp | Links by scanning a QR code | [WhatsApp](connectors/whatsapp_native.md) |
| Weixin | Links a personal WeChat account | [Weixin](connectors/weixin.md) |
| Slack | Uses Slack socket mode | [Slack](connectors/slack.md) |
| Matrix | Connects to a Matrix homeserver | [Matrix](connectors/matrix.md) |
| QQ | Uses the official bot interface | [QQ](connectors/qq.md) |
| DingTalk | Uses stream mode | [DingTalk](connectors/dingtalk.md) |
| IRC | Connects to an IRC server | [IRC](connectors/irc.md) |
| LINE | Needs a public HTTPS address | [LINE](connectors/line.md) |
| WeCom | Links an AI bot | [WeCom](connectors/wecom.md) |
| Feishu | Uses a published app with bot access | [Feishu](connectors/feishu.md) |
| Google Chat | Uses a service account or a send-only webhook | [Google Chat](connectors/google-chat.md) |

## How to add an email mailbox

1. Open **Connectors** and find **Email Mailbox**.
2. Select **Add mailbox**. The **Email Mailbox Account** panel opens.
3. Choose the **Workspace**, then choose the **Owning agent**.
4. Enter the email address, password, Internet Message Access Protocol (IMAP) server, and Simple Mail Transfer Protocol (SMTP) server.
5. Select **Save Mailbox**. The mailbox appears with an **Active** status when it is enabled and configured.

Use **Configure** to change a mailbox. **Remove Mailbox** deletes it and its stored credentials after confirmation.

## Limits and things to watch

- A saved secret is not shown again. Leave its field blank when editing if you want to keep the stored value.
- A connector can show **Failed to start** when its credentials or connection fail. Open **Configure** to correct it.
- **Disable** stops a connected account without deleting its configuration.
- Deleting an account removes its configuration, credentials, and stored state.
- A workspace-bound connector cannot fall back to another agent if its selected agent is later deleted or becomes ineligible. Update the connector's routing choice.
- WhatsApp may be unavailable in a lite build.
- LINE and Google Chat webhook mode need a public HTTPS address.

## Related pages

- [Agents](agents.md) — choose the starred default agent and manage agents that can answer chats.
- [Workspaces](workspaces.md) — manage the team available to a workspace-bound connector.
- [Security](security.md) — understand how Omnipus protects connector credentials.
- [Tools](tools.md) — learn about the email tools agents use.
