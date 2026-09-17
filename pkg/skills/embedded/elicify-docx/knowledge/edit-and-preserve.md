# Edit, populate, and preserve

## Inventory before touching the source

Keep the original file and a package inventory. Read the main body in order,
tables including nested cells, headers/footers, and any relevant notes/comments.
Check revisions, content controls, bookmarks, field instructions, external links,
embedded files, and custom XML. The bundled inspector covers only a subset;
its counts are prompts for inspection, not a full fidelity report.

For a high-value template or complex legal document, first perform a **no-op
round trip** to a separate file, compare package parts, and render both. ZIP
container bytes may differ without a content change: compare decoded XML and
relationships plus binary payload hashes rather than whole-file hashes alone.
Unexpectedly removed parts or changed relationships need investigation before
applying the requested edits.

## Focused text changes

Replacing `paragraph.text` rebuilds its run content and can remove local
formatting, links, fields, and revision markup. For a verified plain run whose
formatting should remain, change only `run.text`:

```python
from docx import Document

def replace_plain_run(source, output, old, new):
    doc = Document(source)
    matches = [run for paragraph in doc.paragraphs for run in paragraph.runs
               if run.text == old]
    if len(matches) != 1:
        raise ValueError(f'expected one whole-run match, found {len(matches)}')
    matches[0].text = new
    doc.save(output)
```

This deliberately limited recipe does not search tables, headers, hyperlinks,
or revisions. Use it only after confirming the target is a plain body run.
Tokens often cross run boundaries. If a match spans runs, map character offsets
to runs and define which formatting the replacement should inherit; do not
silently flatten the paragraph. Preserve surrounding runs and review the edited
region. Do not perform broad replacement of dates or numbers without checking
all matches and their meanings.

## Template handling

- Open a supplied `.docx` template as the source and preserve styles, sections,
  branding, headers/footers, and existing relationship targets.
- Inventory placeholder locations before filling them. Confirm whether repeated
  placeholders should repeat the same value. Detect tokens split across runs,
  table cells, headers, and content controls. A text search of body paragraphs
  alone is not an adequate template-completion check.
- A `.dotx` has a template content type that python-docx may reject. Use Word or
  an approved office converter to create a new `.docx` from it, then inventory
  and render the result. Do not rename the extension and call it converted.
- Do not assume macro-enabled `.docm`/`.dotm`, legacy `.doc`, protected files,
  or embedded objects can be round-tripped safely. Keep the original and use
  an approved application route or explain the specific unsupported feature.

## Revisions, comments, and fields

python-docx's paragraph/table lists omit some content inside revision marks.
Its normal text-editing API is not a general tracked-changes editor. Do not
silently accept/reject existing edits, reconstruct a clean document, or generate
fake redlining by coloring text.

If revisions must be retained, inspect actual `w:ins`, `w:del`, move markers,
property changes, author/date metadata, and relationships. Restrict the change
to a verified safe range; compare untouched revision XML before/after and
verify visible markup in Word. If that cannot be established, keep the source
and provide a proposed edit list or use an approved Word-native route. Explain
that the artifact is a proposal, not an applied tracked-change document.

Recent python-docx versions expose comment creation on run ranges. Check the
installed API and test anchors: a comment is not a tracked edit. Preserve
existing comment ranges and relationships. Fields can contain multiple runs
and cached display values; do not replace them as ordinary text. Record any
fields that require refresh in Word and verify refreshed results before claiming
page numbers, cross-references, or the table of contents are final.

## Preservation acceptance

Record which features must remain, how they were checked, and any limitations.
Useful evidence includes unchanged binary media hashes, equivalent untouched
XML subtrees, relationship targets, style IDs, section count/settings, comments,
and before/after rendered pages. A reviewer should be able to tell precisely
what changed. High-fidelity claims require representative application checks;
the inspector alone cannot establish them.

Reference: [python-docx Document API](https://python-docx.readthedocs.io/en/latest/api/document.html)
(the explicit revision-extraction limits and comment API), checked 2026-09-17.
