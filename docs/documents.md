# Documents in chat

<!-- Draft sections: "Page previews in the chat" is drafted by the frontend
lead; the remaining sections of this page are drafted by the backend lead
("How agents preview documents" added with office-preview wave 2).
Still to draft with packaging: the installer components. docs-verifier
audits the page against the code before landing. -->

## Page previews in the chat

When an agent previews a Word document (`.docx`), spreadsheet (`.xlsx`), slide deck (`.pptx`) or PDF with the `preview_document` tool, you see small images of the pages in the web chat, directly in the agent's reply. Each preview call gets its own group of page images, shown in page order. Every image has a caption such as "report.docx · page 3 of 12".

Page images appear only in the web chat. Replies sent through other channels (for example a messaging connector) do not include them.

| You do | What happens |
|---|---|
| The agent previews a file | While the tool runs you see the usual running tool card. When it finishes, the group of page images appears in the reply |
| The agent previews the same file again (for example after fixing it) | A new group appears. Earlier groups stay, so you can compare before and after |
| You reload the page or reopen the chat | The same groups come back. Nothing is rendered again |
| You delete the chat | The page images are deleted with it |

### Looking at a page up close

Select a page image (click it, or move to it with Tab and press Enter or Space) to open the enlarged page viewer.

| In the viewer | Key or control |
|---|---|
| Next page / previous page of the same group | Right arrow / Left arrow, or the **Next page** / **Previous page** buttons. On the first or last page of the group the key does nothing and the button is shown as unavailable |
| Download the page shown | **Download page** |
| Close the viewer | Escape, or the **Close** button |

The viewer only moves between the pages of the group you opened it from. When you close it, the keyboard focus goes back to the page image you opened, or to its group if that image is no longer on screen, or to the message box.

If one page cannot be loaded in the viewer, the viewer shows "Page n could not be loaded." with a **Retry** button. You can still move to the other pages.

### When some or all page images are missing

| What you see | What it means | What you can do |
|---|---|---|
| Some page images and the note "thumbnails stored: k of N" | Only k of the N requested page images could be saved | Ask the agent to preview the file again |
| A page image replaced by "Image unavailable" | That image could not be loaded into your browser (for example a dropped connection) | Use **Retry** on that image. A failure is reported once for the group, not once per image |
| The card "Page previews unavailable — the PDF renderer is not installed" | The program that turns pages into images is not installed. The agent still received the document or its text; you just get no page images | Follow the instructions on the card: re-run the Omnipus installer with the PDF renderer component, or register it with `omnipus pdf-renderer register <folder>`. This card has no Retry button |
| The card "Page previews unavailable — the PDF renderer is damaged", "… rendering the pages failed" or "… the page images could not be stored" | The page images could not be made or saved this time. The agent may still have received the document itself, but no pages were rendered, so nothing was shown or checked visually | Select **Retry** on the card |

After you select **Retry**:

| Card shows | Meaning |
|---|---|
| "Retrying — rendering the pages again…" | The retry started. The page images replace the card when they are ready; if the retry fails again, the card comes back with Retry |
| "The file changed — ask the agent to preview again." | The file was changed after the preview, so the old preview cannot be retried. Ask the agent to preview the current file |
| "Retry failed: …" | The retry could not start, with the reason. For example "Retry failed: the PDF renderer is not available." You can try again unless you no longer have access to the chat or the file |

### Screen readers

Page images are buttons named by their caption. When a new group arrives, a screen reader announces it once, for example "Preview of report.docx: 4 pages", or "Page previews unavailable". Groups that were already there when you opened the chat are not announced again. In the viewer, each page change is announced as "Page n of N".

## How agents preview documents

An agent looks at a Word document (`.docx`), spreadsheet (`.xlsx`), slide deck
(`.pptx`) or PDF with the `preview_document` tool. It names the file in its
workspace and, optionally, the pages: `"3"` for one page or `"11-20"` for a
range of at most 20 pages. Without pages it gets the first 10.

What the agent's model receives depends on what the model reads, decided from
the model catalog:

| The model | It receives |
|---|---|
| Reads PDFs, and the PDF fits the media size limit | The whole PDF (for an Office file: the PDF converted from it). No page images |
| Sees images, but does not read PDFs or the PDF is too large | Images of the requested pages, with a note when the PDF was too large |
| Reads text only, or the pages could not be made into images | The text of the requested pages (at most 4 MiB), with the reason |

The model gets this content for the current turn only. In later turns the
conversation keeps the tool's result text, which says to call
`preview_document` again. The result text names the file and the first 12
characters of its fingerprint (so an edited file shows a new value), says
"pages a–b of N", names the next batch when there are more pages, and reports
whether the page images for you were stored.

Refusals, each starting with `preview_document:`:

| Message | Meaning |
|---|---|
| `unsupported file type; supported: .docx, .xlsx, .pptx, .pdf` | Other formats, including macro-enabled (`.docm`), template and older Office formats, are not previewed |
| `file content does not match its .<ext> extension` | The file is not what its name says |
| `file exceeds the 50 MiB preview limit` | Larger files are not previewed |
| `password-protected documents cannot be previewed` | Encrypted documents are refused |
| `the document has N pages` | The requested pages are all past the end. A range that runs past the end is shortened instead, for example "pages 25–30 of 30 (requested 25–40)" |
| `timed out after 120 s` or `stopped` | One preview may take at most 120 seconds, including waiting for its turn; Stop ends it at once |

The agent reads the file with exactly the access `read_file` has: a file the
agent may not read is refused with the same answer `read_file` gives.

When an agent opens a PDF with `read_file` (without asking for a line range),
its model receives the PDF in the same three forms as the table above: the
whole PDF, images of pages 1–10 with a note naming the next batch to request
with `preview_document`, or the text. `read_file` shows you no page images in
the chat; only `preview_document` does. A file that ends in `.pdf` but is not
a PDF, or a PDF whose text cannot be extracted, is read the way `read_file`
reads any other file.

### What previews need

- **PDF files** need nothing extra to reach the model. Page images need the PDF
  renderer component. Without it the agent gets the PDF or its text, and the
  result says `page images unavailable: PDF renderer not installed`.
- **Office files** need LibreOffice 26.8. Register an installed copy with
  `omnipus office register <folder>` (on macOS the folder is `LibreOffice.app`);
  `omnipus office unregister` removes it. Without it, Office previews are
  refused with a message that says so; PDF previews still work.
- Register an unpacked PDF renderer release with
  `omnipus pdf-renderer register <folder>`. Registration refuses a folder an
  agent can write, and Omnipus checks the fingerprints of both components
  before every use: a changed file disables the component until it is
  reinstalled or registered again.
- Registering LibreOffice never runs it: Omnipus reads the version from the
  installation's own files. A copy whose version files are missing is refused
  with `LibreOffice version unreadable`.
- "A folder an agent can write" covers these kinds of write access: agent and
  workspace folders, workspace mounts, the sandbox's allowed paths, the write
  allowances in `tools.allow_write_paths`, write access you approved for the
  rest of a chat, and the previewing agent's own access. A path that is a
  link into the component's folder counts as that folder. Omnipus checks this
  again before every preview, so access added later (a settings change or a
  new approval) turns the component off until that access is removed; the
  preview then says which access is in the way.
- A write allowance that names the start of a path rather than a whole folder
  (for example `^/srv/data`, which also matches `/srv/data2`) counts as
  covering the folder it sits in (`/srv`). A pattern Omnipus cannot reduce to
  a folder (for example one without a leading `^/…` folder, one with
  alternatives at the top level, or one that ignores letter case) counts as
  covering every folder, so previews stay off while it is set. The same
  applies when Omnipus cannot read a source of write access: an unreadable
  settings file refuses registration, and an unreadable or partly invalid
  mount record keeps previews off.
- `omnipus doctor` reports each problem with these components as a warning:
  `PDF renderer not installed`, `LibreOffice not installed`, `… damaged
  (<reason>)`, or `… is in a folder an agent can write (<access>)`, each with
  what it means for previews and how to fix it. On Linux it also warns
  `Document preview memory limit not enforced: <reason>` when the kernel does
  not apply the memory limit (see "Limits and isolation"). Like every doctor
  warning, these make `omnipus doctor` finish with exit code 1, so an
  installation without LibreOffice reports a warning until LibreOffice is
  registered. Doctor never starts either program.
- What the check does not cover yet: an **Allow once** approval for a single
  command that is still running in another chat, access granted while a
  preview is already running (it is seen by the next preview), and links
  inside the component's own folder that point to a folder an agent can
  write.

### Limits and isolation

LibreOffice and the PDF renderer run as separate programs, never inside
Omnipus. At most two renderer runs and one LibreOffice conversion run at a
time; other previews wait. Each run is limited to 60 seconds and, for memory,
512 MiB (renderer) or 1 GiB (LibreOffice). On Linux the memory limit counts
the memory a program actually uses (the kernel's data limit), not address
space it only reserves; on kernels older than 4.7, or with the boot option
`ignore_rlimit_data`, the kernel does not enforce it, and the result says
`memory limit not enforced: <reason>`. On macOS the memory limit is best
effort: Omnipus checks memory use every 100 ms and stops a program above its
limit, so use may briefly exceed it.

LibreOffice runs with macros off, links never updated and formulas recalculated
when the file opens. On macOS and Linux, when the kernel sandbox is active, the
programs can write at most their own temporary folder; on macOS they have no
network; on Linux they cannot open or accept TCP connections where the kernel supports that rule (UDP is not
blocked). When
that sandbox cannot be set up, they still run with the limits above and the
result says `converted without sandbox: <reason>`. On Windows there is no file
isolation: only the time and memory limits apply.

Every completed preview is written to the audit log as the event
`preview_document.run`, with the file's path, the result (`validity`) and,
when they apply, `converted_without_sandbox` and `memory_limit_not_enforced`
with their reasons, so you can find runs that were not fully confined later.

Previews are for agents and the chat. This adds no Office preview to the
Library panel.

## PDFs you attach to a message

What the agent's model receives depends on whether the model reads PDFs. Omnipus
decides this from the model catalog alone: a model whose catalog entry lists PDF
input gets the PDF itself; every other model gets the extracted text.

| The model | It receives |
|---|---|
| Reads PDFs (the catalog says so) | The PDF document itself, so it can see layout, tables and images on the pages |
| Reads PDFs, but the file is larger than the media size limit (`agents.defaults.max_media_size`, 20 MB unless set, or the provider's own PDF limit when the model catalog records a smaller one) | The extracted text of the PDF instead |
| Does not read PDFs, but sees images | Images of pages 1–10 and the extracted text. Where the chat has a workspace, the file is also saved there, and a note gives the agent that path and the next pages to request, for example `call preview_document with file "<path>" and pages "11-20"`. Without the PDF renderer, the note says `page images unavailable` with the reason and the agent gets the text |
| Reads text only | The extracted text of the PDF. Where the chat has a workspace, the file is also saved there so the agent can open it with its tools |

When the model catalog records how many PDF documents the provider accepts in
one request, and a message carries more, the newest attachments are sent as
documents and the older ones as text, with a note saying so.

How the PDF travels depends on the provider, and you do not need to set anything:
Anthropic and Amazon Bedrock receive it as a document, and OpenAI's Responses API
and providers that use the Chat Completions format (for example OpenRouter) as a
file attached to your message.

## The Office documents skill

Mia and General Purpose come with one skill for Word, Excel, PowerPoint and PDF
files, shown as **Office documents** (skill id `elicify-office`). It replaces the
four separate document skills earlier versions shipped (`elicify-docx`,
`elicify-xlsx`, `elicify-pptx`, `elicify-pdf`). The agent creates and edits files
with Python libraries, checks the package structure with the bundled inspector
scripts, and uses `preview_document` to look at the result. Excel formulas are
not recalculated by Omnipus: they calculate when the workbook is opened.
