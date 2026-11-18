# Knowledge base

A knowledge base is a folder of notes, records, and saved views that Omnipus indexes, so you and your agents can search it and query it like a small database. Your writing stays plain Markdown files in your own folder.

## What it is

Any folder in a [workspace](workspaces.md) Library can be a knowledge base. Omnipus recognizes one by a marker at its root: its own `.omnipus-vault/` directory, or Obsidian's `.obsidian/`. Either alone is enough; a folder with neither is an ordinary folder. Omnipus never creates `.obsidian/`; it only reads it.

Your files stay yours: notes are plain Markdown with YAML properties, and with Omnipus removed the folder still works as notes. The search index lives outside your folder, in Omnipus's own data directory, so it never pollutes a folder you sync or keep in git.

## When you would use it

- You want notes your agents can find and cite, not files they grep blind.
- You keep structured information, such as contacts, and want typed fields, saved views, and totals instead of free text.
- You already have an Obsidian vault and want to work on it from Omnipus without migrating it.

## How to create a knowledge base and add notes

1. Open the Library from the sidebar's Assets section.
2. Pick the workspace and open the folder that should hold it.
3. Open the **+** menu in the toolbar and choose **New knowledge base**.
4. Name it; the name cannot contain `/` or `\`, or start with a dot.
5. The new folder is empty; the panel offers your first note. Choose **New note** and type a name; the `.md` extension is added for you.

Or mount an existing folder from the Library's mount action; detection does the rest.

## Notes, records, and views

Everything in a knowledge base is a note. What differs is how much structure it declares.

A **record** is a note whose properties name a `type` the knowledge base declares. Each type's schema is a YAML file under `.omnipus-vault/records/`, listing every field's kind: text, enum, relation, date, integer, decimal, person, or checkbox. Records carry stable IDs such as `CO-0142`; a relation is stored once, as a wikilink, with the reverse direction computed. No record types ship built in: what a "contact" means is decided by your schemas, not the product.

A **view** is a saved query over one record type, or over every note in scope. Agents author views on request; you edit them as text. The file records filters, grouping, columns, and totals. A kind is offered only when the fields it needs exist, so an impossible view is refused, naming the missing field.

- **Table** and **List** show rows and names; they need anything.
- **Board** shows cards in status columns; it needs an enum with few values.
- **Calendar** places notes on a month grid; it needs a date field.
- **Summary** shows figures above a grouped table with subtotals; it needs a number field.
- **Trend** adds a chart over time; it needs a date and a number.
- **Breakdown** is a cross-table of two groupings with totals.
- **Tiles** is a cover grid; no image field exists yet, so it is never offered.

Numbers with units total per unit, never across them: amounts in two currencies report two totals, not one blended figure.

Saved views are stored in `.omnipus-vault/views/`. A view is shown in the folder named by its declared `source`, whether it was imported or authored, when that source is a clean relative path within the knowledge base, outside `.omnipus-vault/`, and its folder still exists. Neither that folder nor any folder between it and the knowledge base’s top folder may be a separate knowledge base. Otherwise, the view is shown in the knowledge base’s top folder. Deleting only the source `.base` file does not move the view.

When an agent lists that folder with `list_directory`, each accepted view placed there adds a line after the folder’s files and folders, for example `VIEW: Outstanding (kind summary, view invoices--outstanding, knowledge base Finance vault)`. The kind part is left out when the view file declares no kind. View lines use the same `offset` and `limit` pages as files and folders; follow `next_offset` to see the rest. Line breaks and other control characters in displayed names and labels are quoted and escaped, for example `\n` for a newline, so each view stays on one line. Agent view lines appear only inside knowledge bases found under the working folder or a folder granted by the agent’s file-access policy; folders outside a knowledge base get no `VIEW:` lines. A knowledge base inside another is not found through the outer one; it can contribute views only if it is found separately under another granted folder.

An agent’s folder listing, the Library’s folder listing and Saved views list, previews of `.base` files, opened views, Library search inside a knowledge base, `knowledge_find` and `knowledge_describe` use the same bounded load to select record types and saved views. This load accepts only ordinary files, not links or other special files. In each of `.omnipus-vault/records/` and `.omnipus-vault/views/`, it considers non-hidden `.yaml` and `.yml` entries in filename order; only the first 128 candidates can be read, and a rejected candidate still uses a place. Each file has a 256 KiB size limit. Record type files are loaded first, followed by view files, using one shared 4 MiB byte budget for that knowledge base’s load. Bytes read from files that are later rejected also use this budget. A rejected record type can also prevent views that depend on it from loading.

A view rejected by this load produces no saved-view row and cannot be opened as a view on these serving paths; its YAML file can still appear as an ordinary file. `knowledge_describe` reports rejected view files when its views section is included. If at least one view loads, the Saved views list shows the rejected-file count and the first rejection reason. If no views load, that notice is hidden; use `knowledge_describe` for the rejection details. Other reads of record type files do not apply these limits and do follow links: `knowledge_read` when it checks a note’s record type, `knowledge_configure` and `knowledge_edit` when they change the knowledge base, and the Library when it reads a record type or opens or creates a record. Opening a view, Library search inside a knowledge base, and `knowledge_find` may also rebuild their record index. That rebuild reads record type files without these limits and follows links to individual record type files. A view rejected by the bounded load remains unavailable on these serving paths.

When the Library names a view file or record type file that could not be loaded, the file name is relative to the knowledge base that owns the file, not to the workspace. It starts with that knowledge base's control folder, for example `.omnipus-vault/views/invoices--broken.yaml`. The file names in the explanation of a rejection are written the same way. A record type's source file is also named relative to its knowledge base, for example `.omnipus-vault/records/invoice.yaml`. For a knowledge base inside a subfolder, or one added from another location, look for the file inside that knowledge base's top folder.

For this bounded load, any of `.omnipus-vault/`, `.omnipus-vault/records/` and `.omnipus-vault/views/` that exists must be a real folder; missing folders are treated as empty. If either control folder is a link, an agent’s folder listing shows no `VIEW:` lines for that knowledge base, while the Saved views list, previews of `.base` files, opening a view, Library search inside the knowledge base, `knowledge_find` and `knowledge_describe` fail with an error for that knowledge base. The Library’s ordinary folder listing still returns its files and folders, without added saved-view rows. To fix the linked-folder refusal, replace the link with a real folder that holds the files.

## Reading and searching

Selecting a note opens it in the preview pane as a rendered document, not raw markup, with a side rail listing the outline and the linked mentions. Press **Edit** to see and change the source. Renaming or moving a note rewrites every link that pointed to it, in bodies and properties.

The search bar searches whatever folder you are in. Inside a knowledge base it runs the indexed engine and groups results by **All**, **Notes**, **Records**, **Views**, and **Attachments**; elsewhere it runs a live file search by name and content. Results still indexing say plainly that they are incomplete. Name search matches any file; content search skips binaries. Agents search the same engines through the knowledge tools and a grep tool.

**From chat.** When an agent reads a note for you, its reply can link to the note. Selecting the link opens the note in the Library with its side rail; a link to a `.base` file opens the base preview. [Links from chat](library.md#links-from-chat) explains how these links behave.

## Embedding rich content in a note

A note can show things that live elsewhere with the `![[...]]` embed notation. Embeds load as you scroll, so a dashboard note with many of them stays usable.

| You write | The note shows |
|---|---|
| `![[Tasks.base#Needs Daniel]]` | That saved view, live |
| `![[photo.jpg|400]]` | The image, sized |
| `![[report.pdf#page=2]]` | That page of the PDF |
| `![[Other note#Heading]]` | That section of the note |
| A fenced ```mermaid block | The rendered diagram |
| `$x$` or `$$x$$` | Rendered mathematics |
| `![](https://www.youtube-nocookie.com/...)` | The video, after you press play |

**A saved-view embed needs a real `.base` file.** Omnipus finds saved views whose `source` matches that file, then matches the text after `#` to a view's display label (or its exact saved-view name as a fallback); it does not build the view from the `.base` file's contents. An authored view saved without `source` appears in the knowledge base's **Saved views** list but cannot be embedded. To embed one, give `knowledge_configure`'s `create_view` or `write_view` a collection-relative `source` such as `Projects.base`, then use `![[Projects.base#View label]]`; Omnipus attempts to create a starter file if needed and warns if it cannot. Imported views retain their original `.base` path as `source`: keep that file and embed its path plus the view label, as in the table above.

Embedding one note inside another works one level deep; the transcluded note's own embeds appear as links. An HTML file, a plain-text file, or a diagram file such as `chart.mmd` stays a link rather than rendering inline. A missing target is named in words, and agents reading the note are told the same.

Editing a record field in a view saves to that record; changing an embed's filter or sort affects only that embed, never the saved view.

## Bringing in an existing Obsidian vault

1. Mount the vault's folder into a workspace from the Library's mount action.
2. Open the folder. The `.obsidian/` directory at its root makes it a knowledge base; notes, links, and backlinks work immediately.
3. For typed records and views, run the importer on the machine running Omnipus: `omnipus records import-obsidian --vault /path/to/vault`. It infers record types from your properties, translates `.base` view files, changes no note bodies, and reports what it could not translate.

There is no import screen in the app.

## Limits and things to watch

- **The on-disk folder name still says "vault".** The marker directory is `.omnipus-vault/`; on screen the product says knowledge base.
- **Concurrent edits are checked, not locked.** If an agent changed a note since you opened it, your save is refused and the fresh content offered for retry. Editors outside Omnipus cannot be locked out; their changes are detected the same way. On Windows, two Omnipus processes sharing one data directory have in-process protection only.
- **Some file names cannot be opened.** Names containing `:`, `?`, `"`, `|`, `<`, `>`, `*`, or a trailing dot or space are refused.
- **A first index of a large folder takes time.** Progress is stated in words; a failed index is an error, not a quiet empty result.
- **Printing a dashboard prints what has loaded.** Embeds not yet scrolled into view are absent; the reader says so.
- **No visual graph view.** Structure appears as backlinks and outlines.

## Related pages

- [library](library.md) — the file tree a knowledge base lives in, and the Media tab for binary files.
- [workspaces](workspaces.md) — where each knowledge base belongs, and who can reach it.
- [agents](agents.md) — the built-in roster and the agents that take notes on your behalf.
- [tools](tools.md) — the Allow, ask, and deny settings for each agent's knowledge tools.
- [memory](memory.md) — what the team remembers; a different store from your knowledge bases.
