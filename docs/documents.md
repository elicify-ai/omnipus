# Documents in chat

<!-- Draft sections: "Page previews in the chat" is drafted by the frontend
lead; the remaining sections of this page are drafted by the backend lead.
Drafted so far: "PDFs you attach to a message" and "The Office documents
skill". Still to draft with the renderer work (office-preview wave 2): how
agents preview documents and what preview_document returns per model kind,
installation components, limits (including the macOS best-effort memory
limit), unconfined and Windows exposure, and "The Library gains no Office
preview". docs-verifier audits the page against the code before landing. -->

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

## PDFs you attach to a message

What the agent's model receives depends on whether the model reads PDFs. Omnipus
decides this from the model catalog alone: a model whose catalog entry lists PDF
input gets the PDF itself; every other model gets the extracted text.

| The model | It receives |
|---|---|
| Reads PDFs (the catalog says so) | The PDF document itself, so it can see layout, tables and images on the pages |
| Reads PDFs, but the file is larger than the media size limit (`agents.defaults.max_media_size`, 20 MB unless set) | The extracted text of the PDF instead |
| Does not read PDFs | The extracted text of the PDF. Where the chat has a workspace, the file is also saved there so the agent can open it with its tools |

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
