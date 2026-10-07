# Mail

Mail lets you read and send messages through an agent's mailbox in a workspace. It opens as a panel next to Chat on a wide screen, rather than as a separate Mail page. On a narrow screen, the panel takes the chat's place until you go back. Each mailbox belongs to one agent in one workspace; you can choose between eligible mailboxes when a workspace has more than one.

## Set up and open Mail

First, [add an email mailbox in Connectors](connectors.md#how-to-add-an-email-mailbox). In your workspace, select **Mail** in the top bar; in the icon-only row, hover or focus the Mail icon to see its label. On a narrow top bar, select the workspace name with its downward caret (**Open panels menu**), then select **Mail**. Use the **Mailbox** selector to choose an agent's address when needed. Mail offers only enabled, configured mailboxes for that workspace.

If a Mail link opens a particular mailbox, you can still choose another eligible address using **Mailbox**. Your choice replaces the mailbox named by the link: Mail loads the chosen mailbox's folders and message list, clears the previously open message, and asks you to select a message to read. In the full-screen tab, the address updates to the mailbox you are now viewing.

When a workspace Chat link opens Mail for the first time or in a different workspace, `?panel=mail` without an `agent` value starts at **Choose a mailbox**, even if you previously used one in that workspace. Select a mailbox to load its folders and enable **Compose**; Mail does not connect to a mailbox before you choose.

If none is available, Mail says **No mailbox is configured for this workspace yet.** **Compose** is unavailable. Select **Connect mailbox** to go to the Connectors screen and add one. A mailbox saved for a different workspace will not appear here.

While docked, the message list sits above the reading area. Select **Expand Mail panel** in the panel heading for the full-screen view, where the list and reading area appear side by side. In a normal browser tab, it opens or focuses a separate full-screen Mail tab. In an installed app or standalone Chrome window, it uses the same window instead. Use **Back to chat** to return; in an installed app, Mail re-docks in that same window with its current mailbox, folder and selected message. The [shared panel guide](using-omnipus-ui.md#panels-beside-chat) also explains closing and resizing.

In a normal browser tab, when you return with **Back to chat** or close an app-opened expanded Mail tab, the original Chat tab can restore Mail while it still owns that tab and has no different panel open. Restoration uses the last mailbox, folder and selected-message context that the original tab received from the expanded tab. With working cross-tab communication, that follows your changes in the expanded tab. If those updates could not reach the original tab, it may restore an older selection; check the mailbox and folder after returning. If a separate Mail tab is still recognised for the same workspace, Omnipus offers **Switch** instead of docking another copy.

## Read messages

Choose **INBOX**, the sent-mail folder (usually **Sent**), or the drafts folder (usually **Drafts**) from the folder list, then select a message. Sent and drafts labels can differ when the mailbox uses other folder names. The reading area initially says **Select a message to read**; an empty folder says **No messages**. In INBOX, an unread count appears beside the folder and unread messages have a colored dot and stronger subject text. Opening an unread, non-draft message marks it read and updates the count. **Read by agent** is a separate label indicating that the agent read that message; it is not the unread indicator. On very narrow screens the folder list is hidden and there is no replacement folder selector; widen the window to switch folders.

### How fresh is the list

Mail shows your messages immediately from its cache and labels the line above the list with when they were last checked — for example **Checked 2 minutes ago**, or **Checked just now** after a check against the server completes. When you open Mail or switch folders, Mail checks the server only if the data is missing or older than five minutes; a manual **Refresh** or an action on a message (reading, sending, saving a draft) always checks. Closing the panel stops all checking. While a check runs, your rows stay visible with **Last checked … · Checking…** on the line above them — the list is never replaced by a loading skeleton once rows are shown. If only a message-list refresh fails, already displayed cached rows may remain visible; a failed server check following a cache read shows **Couldn't refresh — showing messages as of …** with **Retry**. If loading or refreshing the folder list fails, Mail replaces the message list with the folder error and **Retry**, even when cached message rows exist. Failed checks do not advance the checked timestamp. A count Mail does not know shows **—**, never a zero, and a message without a date shows **No date**.

Open an incoming message to see its sender, recipients, date, body, and any attachments. Use **Reply** to start a response, or **Reply all** to include the other recipients automatically. Sent messages can be opened for reading, but **Sent copies are read-only.** If an HTML message contains images from outside Omnipus, they start blocked; select **Load images** only if you want to show them.

## Long folders

The list shows 25 messages at a time. **Load more** adds the next 25. A folder view holds at most the newest 200 messages; at that point **Load more** is replaced by **You're viewing the newest 200 messages. Search to find older ones.** Use the search box above the list: it searches this folder on the mail server (subject and sender/recipient), so it reaches messages beyond the newest 200. Search results use the same 25-at-a-time paging, and **Back to \<folder\>** returns to your loaded list. If the folder changed while you were paging, Mail says **The folder changed. Showing the newest messages.** and starts the list over — it never shows a half-moved view.

## Attachments

A paperclip on a list row marks a message with attachments. In the reading area each attachment shows its filename and size (or **Size unknown** when the server does not say) with three actions: **Open \<name\> attachment**, **Save \<name\> to Library**, and **Download \<name\>**.

- **Open** previews the attachment in the Library viewer without saving anything — a bar reading **From mail: \<subject\>** stays above the preview with **Back to mail** and **Save to Library**. Nothing is written to disk; leaving the preview disposes of it.
- **Save to Library** keeps a real copy in the workspace Library under mail → mailbox → month, with a numbered name if one already exists. Only a confirmed save enables **Open in Library**. If the save's response was lost, Mail shows **Save result unknown — checking whether it saved.** with **Retry save**. The retry uses the original attempt token. While the same gateway process still has the committed save receipt, it returns that receipt instead of creating another file. A gateway restart loses those receipts, so a retry after restart may create another numbered copy. Check the Library before retrying if the gateway restarted.
- **Download** is the browser download, and the only action for attachments over the 25 MB preview limit: Open and Save are unavailable with **This attachment is larger than the 25 MB preview limit. Use Download.**

If opening fails — the file turned out larger than the limit, the message changed on the server, or mail is busy — the attachment's row names the cause and offers **Retry** or **Download**; no half-opened preview is left behind.

## Folders

INBOX, Sent, and Drafts are roles: Omnipus finds the real folders on your mail server automatically, so a server that names them unusually still works. You can pin exact names per mailbox in [Connectors](connectors.md#how-to-add-an-email-mailbox) — an empty setting means automatic. A folder your server truly does not have shows **No messages** with an explanation and a link to the mailbox settings; a folder Omnipus could not confirm shows **Couldn't confirm the … folder on this server.** with the settings link — it never claims the folder does not exist, and an unknown count never shows as a zero.

## Compose and send

1. Select **Compose**. The **From** row shows the selected mailbox; enter at least one address in **To**. **Cc** and **Bcc** are available too. Press **Enter** or type a comma to turn an address into a removable entry. Incorrect addresses are marked **Invalid** and block sending.
2. Enter a **Subject** if needed, then write a **Message**. A message body is required. The editor has formatting controls for bold, italic, headings, lists, links, and more. It produces Markdown behind the scenes; Omnipus sends formatted HTML and a plain-text copy.
3. If the mailbox has a **Signature**, check its separate preview. The saved signature is appended when the message is sent; it is not editable in the message body. If you see **Signature preview unavailable. Check it before sending.**, select **Retry** or check the signature in Connectors before sending.
4. Use **Attach files** if needed. The form allows **up to 10 files, 25 MB total**; you can remove a selected file before sending. The same limit counts attachments already on a draft when you edit it.
5. Select **Send**. A successful send shows **Message sent**, or a warning if the message went out but saving its sent copy failed. If sending fails, the compose dialog stays open with what you entered and shows an error; correct the problem and try again. **Cancel** closes the dialog without sending.

To reply instead, open a message and select **Reply**. The response starts with the sender in **To** and a `Re:` subject; you can edit either before sending.

## Work with drafts

Select your drafts folder (usually **Drafts**) and open a draft. In its preview, **Edit** opens fields for recipients, subject, message, and attachments. You can add files or remove existing attachments. While editing in the docked panel, the message list becomes smaller so the editor has more room. Select **Save** to save the edited draft; saving does **not** send it. **Back to preview** leaves edit mode without saving your latest changes.

From the draft preview, select **Send** to send the current saved draft, or **Discard** to delete it. **Send** is not available while editing: save first, then send from the preview. A successful send shows **Draft sent**; if the message went out but removing its old draft copy failed, Mail shows a warning instead. Drafts created outside Omnipus can be edited, but Mail warns that their original styling, images, or table layout may be lost.

## If something goes wrong

Mail uses a best-effort presence check to help manage its connections; Mail requests do not wait for that check. If Omnipus recognises a presence refusal, it can record technical details without showing a pop-up or notification; Mail requests can still run with per-request access. This is not a guarantee that a connection or Mail request succeeds; request errors still appear in Mail.

| What you see | What to do |
|---|---|
| **No mailbox is configured for this workspace yet.** | Select **Connect mailbox** and check the agent, workspace, and mailbox status in [Connectors](connectors.md#how-to-add-an-email-mailbox). |
| Mail shows a mailbox load error straight after you choose it | Mail no longer retries silently in the background: the first failure shows the error and **Retry** at once. A mailbox that has no Sent or Drafts folder still opens, and that folder shows **No messages**. |
| **Mail is busy. Try again.** or **The mail server reached its connection limit. Try again shortly.** | The mail pool or your mail server is at capacity. Wait a moment and select **Retry**; a human Retry bypasses the watcher's waiting period, never the capacity limits. |
| **Can't connect to this mailbox** or **Could not load (\<error class\>)**, with an **Error class** | Check the address, password, and mail-server settings in Connectors. Select **Retry** in Mail after fixing them. A mail-watcher warning may also show **Retry now** and its next retry time. |
| A selected message shows **This message changed or was deleted. Refresh the list.** | The message moved, changed, or was removed on the mail server after the list was loaded — Mail never opens a different message in its place. Select **Refresh list** to reload the folder. |
| **Mail cache unavailable; using live access.** | Mail could not use its local cache for this read and worked directly against the server; everything else behaves normally. |
| A selected message or draft will not load | The reading area shows the request error and **Retry**. A superseded or hidden old draft copy returns a 404 (not found); Mail tries to recover it automatically. If no surviving copy is found and the drafts list refresh succeeds, it shows **This message changed or was deleted. Refresh the list.** Selecting **Retry** in the reading area bypasses the mail watcher's waiting period for the draft lookup, recovery lookup, and list refresh. It does not guarantee a successful connection: if the list refresh fails, its error stays visible. There is no dedicated missing-draft recovery screen beyond this. |
| `stale_draft` after saving or sending a draft | The draft changed since you opened it: the server refused the old revision with a 409 (conflict). A failed **Save** leaves your edits in the editor and shows `stale_draft` in an error notification; it does not merge changes automatically. Keep or copy edits you need before leaving the editor, then reopen the current draft from the drafts folder. |

Leaving Mail for another panel or closing it asks **Discard unsaved changes?** when you have entered message text in Compose or changed a draft in its editor. **Discard** abandons those edits; cancel the prompt to stay. An unfinished Compose entry with *only* recipients or a subject, and no message text, does not trigger this warning. **Cancel** in Compose and **Back to preview** in a draft are separate actions; do not count on the panel-leave warning to save work from them. See [panels beside chat](using-omnipus-ui.md#panels-beside-chat) for resizing and full-screen controls.

## Related pages

- [Connectors](connectors.md#how-to-add-an-email-mailbox) — configure an email mailbox and its signature.
- [Using the Omnipus interface](using-omnipus-ui.md#panels-beside-chat) — open, resize, close, and expand a panel.
- [Workspaces](workspaces.md) — choose the workspace that owns the mailbox.
