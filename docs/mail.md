# Mail

Mail lets you read and send messages through an agent's mailbox in a workspace. It opens as a panel next to Chat on a wide screen, rather than as a separate Mail page. On a narrow screen, the panel takes the chat's place until you go back. Each mailbox belongs to one agent in one workspace; you can choose between eligible mailboxes when a workspace has more than one.

## Set up and open Mail

First, [add an email mailbox in Connectors](connectors.md#how-to-add-an-email-mailbox). In your workspace, select **Mail** in the top bar, or in the workspace switcher on a narrow screen. Use the **Mailbox** selector to choose an agent's address when needed. Mail offers only enabled, configured mailboxes for that workspace.

If none is available, Mail says **No mailbox is configured for this workspace yet.** **Compose** is unavailable. Select **Connect mailbox** to go to the Connectors screen and add one. A mailbox saved for a different workspace will not appear here.

While docked, the message list sits above the reading area. Select **Expand Mail panel** in the panel heading to open or focus Mail in a separate full-screen browser tab; there the list and reading area appear side by side. Use **Back to chat** to return. The [shared panel guide](using-omnipus-ui.md#panels-beside-chat) also explains closing and resizing.

## Read messages

Choose **INBOX**, the sent-mail folder (usually **Sent**), or the drafts folder (usually **Drafts**) from the folder list, then select a message. Sent and drafts labels can differ when the mailbox uses other folder names. The reading area initially says **Select a message to read**; an empty folder says **No messages**. In INBOX, an unread count appears beside the folder and unread messages have a colored dot and stronger subject text. Opening an unread, non-draft message marks it read and updates the count. **Read by agent** is a separate label indicating that the agent read that message; it is not the unread indicator. On very narrow screens the folder list is hidden and there is no replacement folder selector; widen the window to switch folders.

Open an incoming message to see its sender, recipients, date, body, and any attachments. Use **Reply** to start a response. Sent messages can be opened for reading, but **Sent copies are read-only.** If an HTML message contains images from outside Omnipus, they start blocked; select **Load images** only if you want to show them.

For an attachment, select **Download** beside its filename and size. An `.html` attachment is downloaded as a file, never displayed inline as a web page by the mail attachment endpoint. If the download fails, Mail shows an error notification.

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

| What you see | What to do |
|---|---|
| **No mailbox is configured for this workspace yet.** | Select **Connect mailbox** and check the agent, workspace, and mailbox status in [Connectors](connectors.md#how-to-add-an-email-mailbox). |
| **Can't connect to this mailbox** or **Could not load this mailbox**, with an **Error class** | Check the address, password, and mail-server settings in Connectors. Select **Retry** in Mail after fixing them. A mail-watcher warning may also show **Retry now** and its next retry time. |
| A selected message or draft will not load | The reading area shows the request error and **Retry**. A superseded or hidden old draft copy returns a 404 (not found); Mail tries to recover it automatically and, if that fails, shows **"This draft was changed or deleted elsewhere. The list has been refreshed."** and refreshes the drafts list for you. A different missing reference may instead be reported as a mail-server error. There is no dedicated missing-draft recovery screen beyond this. |
| `stale_draft` after saving or sending a draft | The draft changed since you opened it: the server refused the old revision with a 409 (conflict). A failed **Save** leaves your edits in the editor and shows `stale_draft` in an error notification; it does not merge changes automatically. Keep or copy edits you need before leaving the editor, then reopen the current draft from the drafts folder. |

Leaving Mail for another panel or closing it asks **Discard unsaved changes?** when you have entered message text in Compose or changed a draft in its editor. **Discard** abandons those edits; cancel the prompt to stay. An unfinished Compose entry with *only* recipients or a subject, and no message text, does not trigger this warning. **Cancel** in Compose and **Back to preview** in a draft are separate actions; do not count on the panel-leave warning to save work from them. See [panels beside chat](using-omnipus-ui.md#panels-beside-chat) for resizing and full-screen controls.

## Related pages

- [Connectors](connectors.md#how-to-add-an-email-mailbox) — configure an email mailbox and its signature.
- [Using the Omnipus interface](using-omnipus-ui.md#panels-beside-chat) — open, resize, close, and expand a panel.
- [Workspaces](workspaces.md) — choose the workspace that owns the mailbox.
