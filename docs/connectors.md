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
4. Choose a **Workspace** and a **Main agent**, the agent that answers messages on this account. The **Main agent** list contains members of that workspace.
5. Select **Save & Enable**. The connected account appears with an **Enabled** status.
6. To connect another account on the same platform, select **Add another…** beside its group.

### Connecting from chat

You can also ask Omnipus in chat to connect a platform. First it confirms the proposed connection with you. For a new connection, it saves the non-secret settings before requesting credentials, so the secure card can name an existing destination. Under the shipped policy, that initial configuration save asks for approval; it does not enable a new connection. You then enter the credentials on the secure card, never in the conversation. A separate final connect approval selects the saved references, binds the connection to the named workspace and main agent, enables it and starts it. Cancelling the secret card stores no secret values, but leaves any non-secret settings already saved.

- The main agent must already be on that workspace's team, and the workspace must not be archived. Otherwise the final connect call reports **failed: nothing saved** and makes no change. Any provisional non-secret entry saved earlier in setup remains.
- The result separates what was proven from what was not tested. **Working** means only that the connection started after saving with the saved settings and credentials; incoming message delivery and outgoing replies are listed as not tested.
- A connection that did not start is reported as **saved but not working**, with the reason — for example that the platform rejected the credentials or could not be reached (Omnipus cannot tell those two apart). It is never reported as working. Fix the credential and try again, or open **Configure** on its card.
- A startup check proves only what that connector checks before it returns, not that every credential is accepted or that messages can be delivered. Telegram, LINE, Matrix, Google Chat bot mode and Feishu/Lark refuse the credential-rejection responses recognized by their startup checks. WeCom and Weixin also refuse a rejection observed during their initial wait: up to 10 seconds for WeCom and 3 seconds for Weixin. A rejection arriving later is handled by their background connection or polling loop; it does not change that startup result afterwards. Feishu/Lark's initial credential check has a 10-second limit. A timeout is not proof that credentials were accepted. A refused start is reported as **saved but not working** with that reason.
- These checks do not cover every credential. LINE's startup lookup checks its access token, not its webhook signing secret. Slack checks the bot token before returning, but starts the Socket Mode connection using its app token afterwards. IRC can fail startup during server-password or SASL registration; NickServ identification is sent after connection and does not gate startup. Keep the result's **not tested** list in view.
- A temporary failure of an initial check can leave Telegram, LINE, Matrix, Google Chat, Feishu/Lark, WeCom or Weixin started. Their next steps differ: Telegram and Matrix continue their polling or synchronization; Feishu/Lark goes on to its WebSocket connection; WeCom and Weixin keep their connection or polling loops. LINE keeps the webhook receiver running but does not repeat its bot-profile lookup, so mention detection can stay unavailable until the channel is restarted. Google Chat bot mode requests another token when it next sends a message or starts typing, rather than retrying the check in the background.
- Discord, Slack's initial bot-token check, DingTalk, QQ and IRC can also fail their startup when the platform cannot be reached. The chat connect result then reports **saved but not working**. A connection that starts is still not proof of incoming delivery or outgoing replies.
- WhatsApp needs no credentials. The connection is saved and pairing starts. The QR code and its progress appear in the same chat, below the connect step — it is the same code the WhatsApp connection shows under **Connectors**. On your phone, open WhatsApp → **Settings → Linked Devices → Link a Device** and scan it. The result says **saved but not working: pairing not finished** until the scan succeeds. If the code expires or pairing fails, select **Retry** in the chat to get a new code. Retry restarts the pairing for that chat: the new code and its progress stay linked to the chat, so **Stop** there ends it. Retry first turns the WhatsApp connection off and then on again. If the chat can no longer retry — for example a newer pairing was started from **Connectors** or from another chat — the restart is refused and no new pairing starts. The chat then shows "The channel is now disabled. This chat can no longer retry this pairing. Open the WhatsApp connector to start a new pairing." with no Retry button: the WhatsApp connection stays turned off until you start a new pairing from the WhatsApp connection on **Connectors**. If the pairing service is temporarily unavailable, the message is "The channel is now disabled. Pairing ownership service unavailable. Try again." and **Retry** is offered again. If the network failed, or the result of turning the connection back on is unknown, the chat says "The channel was disabled before the enable request, but its current state could not be confirmed." followed by "Network unavailable. Check your connection and try again." or "Could not restart pairing. Open the WhatsApp connector and try again.", and **Retry** is offered; check the WhatsApp connection on **Connectors** before pairing. If the page reconnects while you pair, the chat picks the pairing up again and shows its current state. Reconnecting only shows the pairing; it never takes over a pairing started from **Connectors** or from another chat, and **Stop** in an older chat does not end such a pairing. Reopening an older chat shows only the step's result there; to pair again later, open the WhatsApp connection on **Connectors**.
- Pressing **Stop** in the chat that started a WhatsApp pairing ends the pairing: no new codes are produced. The connection stays saved but not working.

## Choosing which agent answers

Every connector is bound to one workspace and names the agent that answers it — its main agent. Saving the binding needs both; there is no default agent to fall back to.

A bound connector sends every incoming message on that account to its main agent. The main agent must exist, must be able to answer chats (not a worker or a system agent) and must stay a member of that workspace.

When a message cannot be routed, Omnipus drops it — no other agent ever sees it. That happens when:

- the connector is not bound yet ("bind this channel to an agent in a workspace");
- its main agent no longer exists;
- its main agent is not a chat agent;
- its main agent is no longer on the workspace's team.

Until a connector is bound, its card shows **Not bound — choose a workspace and agent**. A connector that dropped a message shows the reason and the time of the last dropped message.

Each drop is written to the log as an error and to the audit log, and the connector shows as degraded on the Connectors page with the reason. Saving a valid binding clears that status, and the next message is answered normally.

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

1. Open **Connectors** and find the **Email** section. Select **Add mailbox** to open **Email Mailbox Account**.
2. Choose a **Workspace**, then an **Owning agent** from that workspace's team. Each agent can have a separate mailbox in each workspace; you cannot add a second mailbox for the same agent in the same workspace.
3. Enter **Email Address**, **Password** (your mail provider's IMAP/SMTP app password), **IMAP Host** for incoming mail, and **SMTP Host** for outgoing mail. Internet Message Access Protocol (IMAP) and Simple Mail Transfer Protocol (SMTP) are the two server connections. Open **Advanced** to change **IMAP Port** or **SMTP Port** if your provider needs nonstandard ports. The form shows 993 as the IMAP default and 587 as the SMTP default; its help text also lists 465 for SMTP.
4. Optionally enter an HTML **Signature** and check its preview. The form rejects signatures over 16,384 characters. The saved signature is appended to messages sent from this mailbox.
5. Select **Save Mailbox**. On success, you see **Mailbox account saved** and a row showing the agent, email address, workspace, and one of **Active**, **Degraded**, or **Not configured**. Required-field problems appear beside the fields; other save failures show an error notification and leave the form open.

**Active** means that the mailbox is enabled and its stored password is available to Omnipus. Saving does **not** test whether the mail server accepts the password or can be reached. **Degraded** means the mailbox is enabled but cannot start — most often because its stored password reference is missing from the credential store. The row itself shows the exact reason, for example `secret reference "MAIL_PASSWORD" is missing from the credential store — set the mailbox password to restore it`; re-enter the password with **Configure** on the row, and see [A connection shows a credential error (degraded)](troubleshooting.md#a-connection-shows-a-credential-error-degraded) for the full fix list. **Not configured** means the mailbox is disabled. An enabled mailbox with a missing or unreadable password shows **Degraded**, not Not configured. These badges are not live mail-server connection results. Open [Mail](mail.md#if-something-goes-wrong) for connection errors and retry controls.

Use **Configure** on a row to change its settings. The stored password is never shown; leave **Password** blank to keep it while editing the *same* agent and workspace. If you change either the owning agent or workspace, you must enter the password again because credentials do not transfer. A successful move can show **Mailbox saved, but the old mailbox could not be removed — delete it manually from the list.** if removal of the old row fails. **Remove Mailbox** asks for confirmation before deleting the saved configuration and credentials; a failed removal keeps the confirmation open and shows the error.

### Email mailbox settings — folder names

Under **Folder names**, **Sent folder name** and **Drafts folder name** tell Omnipus which folders to use for sent mail and drafts. Leave a field empty to let Omnipus find the folder automatically (the placeholder says **Automatic**); enter a value to use exactly that folder name on your mail server. Saving with a field cleared removes the override. A name Omnipus cannot confirm on your server shows a warning beside that field — check the spelling or clear the field back to automatic; the name is never silently ignored. A name saved by an older version stays as the field's value until you clear it yourself.

Back in the workspace, select **Mail** to open the [mailbox panel](mail.md#set-up-and-open-mail) and choose the agent's address. Email is not set up through the chat-connector **Configure** flow above.

## Limits and things to watch

- A saved secret is not shown again. Leave its field blank when editing if you want to keep the stored value.
- A connector can show **Failed to start** when its credentials or connection fail. Open **Configure** to correct it. When the cause is a missing or unreadable stored secret, the row's error names the exact credential reference to fix. Missing credential references are handled as integration-specific errors. An individual authentication failure is handled the same way when another stored entry decrypts. Every integration using that reference can be affected. Other credential failures—including malformed entry encodings—can stop startup or reject reloads and settings refreshes even if another entry decrypts. More than one integration can depend on the same credential reference. A whole-vault failure can stop startup or reject a reload or settings refresh; see [credential failures](troubleshooting.md#a-connection-shows-a-credential-error-degraded). **Doctor** is under **Settings → Security → Security health**. It flags degraded chat channels with their failure reason. For mailbox, provider and MCP credential failures, read the reason on that integration's own row or card.
- **Disable** stops a connected account without deleting its configuration. Disabling also erases the mailbox's cached mail metadata and its new-mail watching state, and revokes its open previews; re-enabling starts fresh. The configuration and stored password are kept, and the mailbox keeps the same private storage name it had.
- Deleting an account removes its configuration, credentials, cached mail metadata, and new-mail watching state. The removal result says which of the two happened: **removed** (everything succeeded) or **removed, cleanup pending** (the account is gone and can no longer be used, but erasing its cached files did not fully finish). Cleanup pending is never shown as a full success; the response carries a retry key, and the separately authorized cleanup operation (`POST /api/v1/mailboxes/cleanup` with that key) finishes the job — it keeps working after the mailbox row is gone. Deleting an agent or a workspace runs the same erase for every mailbox it held.
- Omnipus keeps the mailbox's cached mail metadata in its own private storage under the data directory, and the application makes sure its own backups and restores never copy that storage or the new-mail watching state. A **cache unavailable** warning means Omnipus could not prove that protection on your install — a genuine failure such as a disk or permission problem — not missing external setup. Mail keeps working live while the warning shows; only the cached metadata is off.
- A workspace-bound connector cannot fall back to another agent if its selected agent is later deleted or becomes ineligible. Update the connector's routing choice.
- WhatsApp ships in every build on the supported platforms; it is unavailable only on a few unsupported architectures (mipsle, netbsd, freebsd/arm) where its SQLite dependency cannot compile. There is no separate lite build.
- LINE and Google Chat webhook mode need a public HTTPS address.

## Related pages

- [Agents](agents.md) — manage the agents that can answer chats.
- [Workspaces](workspaces.md) — manage the team available to a workspace-bound connector.
- [Security](security.md) — understand how Omnipus protects connector credentials.
- [Tools](tools.md) — learn about the email tools agents use.
- [Mail](mail.md) — read, send, and manage drafts in the workspace Mail panel.
