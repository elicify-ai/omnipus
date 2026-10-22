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
5. Select **Save & Enable**. On success, the connected account appears with an **Enabled** status. For WhatsApp, keep the panel open for pairing: **Enabled** is the gateway's currently loaded setting, not proof that a code is ready, the phone is linked, or messages can be delivered. See the recovery details below if enabling reports an error.
6. To connect another account on the same platform, select **Add another…** beside its group.

### Connecting from chat

You can also ask Omnipus in chat to connect a platform. First it confirms the proposed connection with you. For a new connection, it saves the non-secret settings before requesting credentials, so the secure card can name an existing destination. Under the shipped policy, that initial configuration save asks for approval; it does not enable a new connection. You then enter the credentials on the secure card, never in the conversation. A separate final connect approval selects the saved references, binds the connection to the named workspace and main agent, enables it and starts it. Cancelling the secret card stores no secret values, but leaves any non-secret settings already saved.

- The main agent must already be on that workspace's team, and the workspace must not be archived. Otherwise the final connect call reports **failed: nothing saved** and makes no change. Any provisional non-secret entry saved earlier in setup remains.
- The result separates what was proven from what was not tested. **Working** means only that the connection started after saving with the saved settings and credentials; incoming message delivery and outgoing replies are listed as not tested.
- A connection that did not start is reported as **saved but not working**, never working. Follow the result's cause and repair. A start can identify an unreachable platform or a problem with the connection's local session data; for a local-data problem, check access to Omnipus's data directory rather than re-entering credentials. Some status paths still report a combined credential-or-reachability reason. Correct the reported problem and try again, or open **Configure** on the connection's card.
- A startup check proves only what that connector checks before it returns, not that every credential is accepted or that messages can be delivered. Telegram, LINE, Matrix, Google Chat bot mode and Feishu/Lark refuse the credential-rejection responses recognized by their startup checks. WeCom and Weixin also refuse a rejection observed during their initial wait: up to 10 seconds for WeCom and 3 seconds for Weixin. A rejection arriving later is handled by their background connection or polling loop; it does not change that startup result afterwards. Feishu/Lark's initial credential check has a 10-second limit. A timeout is not proof that credentials were accepted. A refused start is reported as **saved but not working** with that reason.
- These checks do not cover every credential. LINE's startup lookup checks its access token, not its webhook signing secret. Slack checks the bot token before returning, but starts the Socket Mode connection using its app token afterwards. IRC can fail startup during server-password or SASL registration; NickServ identification is sent after connection and does not gate startup. Keep the result's **not tested** list in view.
- A temporary failure of an initial check can leave Telegram, LINE, Matrix, Google Chat, Feishu/Lark, WeCom or Weixin started. Their next steps differ: Telegram and Matrix continue their polling or synchronization; Feishu/Lark goes on to its WebSocket connection; WeCom and Weixin keep their connection or polling loops. LINE keeps the webhook receiver running but does not repeat its bot-profile lookup, so mention detection can stay unavailable until the channel is restarted. Google Chat bot mode requests another token when it next sends a message or starts typing, rather than retrying the check in the background.
- Discord, Slack's initial bot-token check, DingTalk, QQ and IRC can also fail their startup when the platform cannot be reached. The chat connect result then reports **saved but not working**. A connection that starts is still not proof of incoming delivery or outgoing replies.
- WhatsApp needs no secret-card credentials. Saving and starting an unpaired connection begins QR pairing. The QR code and its progress appear below the connect step in the requesting chat, using the same pairing view as **Connectors**. The on-screen instruction is to open WhatsApp on your phone, go to **Settings → Linked Devices → Link a Device**, and scan the code. The initial result reports **saved but not working: pairing not finished**; the pairing view shows the later linking status. Neither a saved connection nor a successful restart request proves that phone linking or message delivery has succeeded.
- Only one pairing of a WhatsApp connection runs at a time, and every new pairing — from a chat's connect, a chat's Retry, or turning the connection on from **Connectors** or with the `enable_channel` tool — must first be accepted; a refusal changes nothing. If another pairing of the same connection is starting, running or being stopped, a chat's connect is refused with "another WhatsApp pairing of this connection is starting, running or stopping; nothing saved", and turning the connection on from **Connectors** is refused with "Another WhatsApp pairing of this connection is starting, running or stopping, so nothing was changed. Wait for it to finish, or stop it from the chat that started it." Wait for it to finish, or stop it from the chat that started it, then try again. Retry from the chat card or the WhatsApp **Configure** panel, and turning the connection on from **Connectors**, require an existing WhatsApp connection: they never add a missing or deleted one back — use **Configure** to add it first — and a connection that is being deleted cannot be turned on. The approved `enable_channel` agent tool has a different boundary: with the revision `absent` it can create a new enabled entry for a missing connection, including one deleted earlier. Do not treat the Retry and Connectors protection as a guarantee about that tool. A disable that is refused (for example because its revision is out of date) or that fails to save changes nothing, including any pairing that is waiting to start.
- Changing the connection's settings, restarting the gateway, or reloading after a pairing was stopped or cancelled does not by itself request a new pairing code. A reload using older settings cannot start or cancel a later accepted pairing that is waiting to start, and delayed progress from an older pairing cannot replace the newer code. A newer intentional settings change can still interrupt the running pairing it affects. This does not let a later connect or Retry take over a pairing that is already starting, running or stopping. Select **Retry**, or turn the connection off and on again while the gateway is running, to request a new attempt. There is one automatic case: if WhatsApp logs out a running linked device, Omnipus can start a new unowned re-pairing attempt and show a fresh code without a chat or Connectors Retry. That attempt still has to pass the pairing checks; it does not replace an attempt being stopped or a pending reservation.
- The live chat card and the WhatsApp **Configure** panel use the same pairing view, and say what happened instead of waiting for a code that cannot come. Ordinary pairing errors keep the heading "Pairing failed — select to retry." and show the exact message below it; an already-linked device and a pairing still being stopped are not shown as failures:

  | State | Exact message | Next action |
  |---|---|---|
  | A running pairing was cut off because the connection's settings changed, or it was turned off, deleted or restarted | "This pairing was interrupted because the WhatsApp connection was changed or restarted. Press Retry to get a new code." | Select **Retry** for a new pairing. Changing the settings does not start a new pairing by itself. |
  | The local session data cannot be opened | "WhatsApp could not open its local session data. Check that the Omnipus data directory is readable and writable, then press Retry." | Correct the directory access, then select **Retry**. |
  | WhatsApp cannot be reached | "WhatsApp could not be reached. Check the network connection, then press Retry." | Correct the network connection, then select **Retry**. |
  | Another start failure | "The WhatsApp pairing could not start. Details are in the server log. Press Retry to try again." | Check the server log, then select **Retry**. |
  | A Stop is still tearing the pairing down | "Stopping this pairing has not finished yet. Omnipus keeps trying; a new pairing can start once it has stopped." | No Retry button until the pairing shows as stopped; Omnipus keeps retrying the stop. |
  | The phone was already linked | "This WhatsApp device is already linked." | The view shows the linked status, with no Retry button and no wait for a code. The chat's connect result reports the connection working instead of asking for a QR scan. |
  | Turning the connection on saved the setting, but its pairing was cancelled (for example by Stop) before it started | "The WhatsApp connection is turned on, but its pairing was cancelled before it started. Press Retry to start a new one." | The connection is on, not off. **Retry** stays available; **Enable** and **Save & Enable** on Connectors show the same message. |
  | Turning the connection on from **Connectors** while another pairing of it is starting, running or stopping | "Another WhatsApp pairing of this connection is starting, running or stopping, so nothing was changed. Wait for it to finish, or stop it from the chat that started it." | Wait, or stop it from the chat that started it. This is not the refusal that says an older chat can no longer retry its pairing. |
  | The connection is missing or was deleted | "This WhatsApp connection was deleted. Add it again from Connectors." | No Retry button. Add the connection again from **Connectors**. |

  The chat that started a pairing that could not start, was interrupted or was stopped can press **Retry**. A pairing that found the phone already linked has nothing to retry.
- If the code expires or pairing fails, select **Retry** to get a new code. Every Retry starts a new pairing; it never continues the old one. With complete chat details, Retry first turns the WhatsApp connection off, then asks to turn it on again for the original chat and connect call. A chat can Retry only while the pairing it started is the connection's most recent one and has ended (the code expired, pairing failed, it could not start, it was stopped, or it was interrupted). A pairing that found the phone already linked has nothing to retry. Once a newer pairing has started — from **Connectors**, from another chat, or by a later connect — the earlier chat can no longer Retry, even after that newer pairing ends; start a new pairing from the WhatsApp connection on **Connectors** instead. An accepted Retry links the new pairing to the chat that pressed it, so **Stop** there ends it.
- A server restart interrupts any pairing in progress: after the restart, a WhatsApp connection that is turned on but not yet linked to a phone does not show a new code on its own. The chat and the WhatsApp connection on **Connectors** both show "A server restart interrupted this pairing. Press Retry to get a new code." No chat owns a pairing after a restart. Press **Retry**: in a chat, the new pairing belongs to that chat; on **Connectors**, it belongs to no chat. A connection that is already linked to a phone starts normally after a restart, with no code.
- Retry reports failures according to which step was confirmed:

  | State | Message and recovery |
  |---|---|
  | Turning the connection off failed because the network failed | "Network unavailable. Check your connection and try again." There is no disabled-state prefix, the connection is not turned on again, and **Retry** remains available. Check the connection's current state on **Connectors**. |
  | The WhatsApp connection was deleted from **Connectors** while the pairing card was open | "This WhatsApp connection was deleted. Add it again from Connectors." whether turning it off or turning it on found it gone. There is no Retry button. Add the connection again from **Connectors**; a Retry never adds a deleted connection back. |
  | The connection was turned off, but the server refused this chat's Retry | "The channel is now disabled. This chat can no longer retry this pairing. Open the WhatsApp connector to start a new pairing." There is no Retry button. Start a new pairing from the WhatsApp connection on **Connectors**. The refusal does not undo the earlier turn-off. |
  | The connection was turned off, but the pairing service could not accept the Retry | "The channel is now disabled. Pairing ownership service unavailable. Try again." **Retry** is available. The refusal does not undo the earlier turn-off. |
  | The connection was turned off, but the answer to turning it on was lost to a network failure | "The channel was disabled before the enable request, but its current state could not be confirmed. Network unavailable. Check your connection and try again." **Retry** is available; check the WhatsApp connection on **Connectors** first. |
  | The connection was turned off, and turning it on returned a server error | "The channel was disabled before the enable request, but its current state could not be confirmed." followed by the server's own reason — for example that the connection is enabled but its restart could not be confirmed yet. **Retry** is available; check the connection on **Connectors** rather than assuming it is still off. When the restart was only slow, that pairing still belongs to this chat: a later code or status for the same chat and request replaces the error in the open card, without another Retry. A frame from another request or chat never clears it, and a deleted-connection or refusal message stays visible. The cancelled-before-start message in the table above is shown as it is, without this prefix. |
  | The connection was turned off, but the result of turning it on failed in another way | "The channel was disabled before the enable request, but its current state could not be confirmed. Could not restart pairing. Open the WhatsApp connector and try again." **Retry** is available. |
  | The chat card is missing the details a Retry needs | "This chat can no longer retry this pairing. Open the WhatsApp connector to start a new pairing." There is no Retry button. Nothing is turned off or on, and the card does not claim the connection is off. |

  **Save & Enable**, **Retry**, and the row's **Enable** action have different recovery views:

  | Action | What stays visible after an unconfirmed restart |
  |---|---|
  | **Save & Enable** in the WhatsApp **Configure** panel | After the configuration saves, the open panel starts listening for pairing progress while enabling is still pending. Whether enabling succeeds or reports an error, Connectors refreshes the connection list to read the gateway's currently loaded enabled state. If that list reports **Enabled**, the viewer stays open: a current code received before the error is retained, and later progress appears without reopening the panel or pressing Retry. The error notification is still reported as an error; a saved enabled setting is not a confirmed start or a linked phone. |
  | **Retry** in an already-open pairing view | If the restart cannot yet be confirmed, the view shows the server's reason: "The WhatsApp connection is enabled, but its restart could not be confirmed yet. The new code appears here when the pairing starts." The new pairing belongs to no chat, even if the panel previously showed an ended pairing from a chat. A later code or status with no chat or request owner replaces this Retry's temporary error automatically; an earlier chat's frame does not confirm it. |
  | **Enable** on the connection's row | The row refreshes the gateway's currently loaded state on success or error and keeps any error notification. If the list reports the connection enabled, the action changes to **Disable**. This row action does not open a pairing panel; select **Configure** to view its pairing status. |

  If the connection list reports the connection disabled after **Save & Enable**, the panel returns to the enable prompt; this describes the last successfully loaded state, not an independent check of the stored file. If the list readback itself fails, it says "Could not confirm whether WhatsApp is enabled." Its **Retry** button refreshes the connection list again; it does not enable, restart, or apply a saved change. A failed configuration save does not proceed to enabling. A deleted-connection response (404) and a competing-pairing refusal (409) remain errors, not evidence of a successful enable.

  A failure saying the setting was saved but the running configuration could not be refreshed is different from a slow restart. The connection list can still show the previously loaded setting even though the stored setting changed. In that case, an enable prompt or an **Enable** row action does not prove that the save failed or that the stored connection is off. Keep the server's error in view and resolve the reported refresh problem before retrying setup. Refreshing the list, including the state-only **Retry**, does not itself apply the saved change.

  A chat's own Retry stays linked to that chat and its original connect request (its message says the code appears "in this chat"), and only a later frame matching both clears its temporary restart error. Deleted-connection and conflict messages remain visible rather than being cleared by a later code.

  A successful Retry confirms that the restart was accepted, not that a fresh code is ready, the phone is linked, or messages can be delivered. Wait for the pairing status: a code, the linked status or a start error replaces the wait. If the page reconnects while you pair, the chat picks the pairing up again and shows its current state. Reconnecting only shows the pairing; it never turns the connection on, never makes the chat the owner of a pairing started elsewhere, and does not let an older chat's **Stop** end that pairing.
- A connect step replayed without a live view on this page — for example after a full page reload — shows its recorded result and a pointer to **Connectors** instead of a live QR code. A connect step this page showed live can keep its live pairing view when you return to the chat during the same page visit. To start pairing from a replayed result, open the WhatsApp connection on **Connectors**.
- Pressing **Stop** in the chat that started a WhatsApp pairing ends that pairing: no new codes are produced. The connection stays saved but not working. A first **Stop** ends only the chat's own pairing; Stop all (a second Stop within three seconds, or `/cancel`) also ends a pairing started by a helper working under that chat, including a helper that has already finished its work. A pairing started from **Connectors** or from another chat is not affected, and a Stop never ends a pairing that a Retry started after the Stop was pressed. If stopping takes longer than expected, the card says so (see the table above); Omnipus keeps retrying and shows the pairing as stopped only once the WhatsApp client has really finished stopping; no new pairing can start for that connection until then.

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
