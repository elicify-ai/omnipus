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

## Reading and searching

Selecting a note opens it in the preview pane as a rendered document, not raw markup, with a side rail listing the outline and the linked mentions. Press **Edit** to see and change the source. Renaming or moving a note rewrites every link that pointed to it, in bodies and properties.

The search bar searches whatever folder you are in. Inside a knowledge base it runs the indexed engine and groups results by **All**, **Notes**, **Records**, **Views**, and **Attachments**; elsewhere it runs a live file search by name and content. Results still indexing say plainly that they are incomplete. Name search matches any file; content search skips binaries. Agents search the same engines through the knowledge tools and a grep tool.

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
- [agents](agents.md) — the four base agents and how they take notes on your behalf.
- [tools](tools.md) — the Allow, ask, and deny settings for each agent's knowledge tools.
- [memory](memory.md) — what the team remembers; a different store from your knowledge bases.
