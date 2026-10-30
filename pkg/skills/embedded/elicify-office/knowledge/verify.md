# Verify: the preview loop and review criteria

One loop for all four formats. Create or edit with the library, then look with
`preview_document`, fix the source, look again. Structure checks (inspectors, reopening
the file) and visual checks answer different questions; report them separately.

## The loop

1. **Reopen** the saved output with the same library and compare required content
   (text, tables, chart values, form fields, notes) against the brief or fact table.
2. **Inspect the package** with the bundled script for the format
   ([inspector contract](inspector-contract.md)). A clean run is not visual proof.
3. **Preview** with `preview_document` on the exact output file. It takes `file`
   (a workspace path) and optional `pages` as `"N"` or `"N-M"`; with no `pages` you get
   the first batch. The result states which pages you received ("pages a-b of N")
   and names the next batch when more exist. Call again for each remaining batch until
   every page in scope has been seen. Do not render pages with your own tools; the
   tool is the only route.
4. **Review** each page against the criteria below. Compare visible content with the
   verified facts (dates, signs, units, totals).
5. **Fix the source**, regenerate to a new file, and preview again. After any change
   that can move pagination, preview the downstream pages and recheck the page count.
   Do not patch only the preview.

## What you actually received

What `preview_document` hands the model depends on the model: the document itself,
page images, or extracted text only. If you received only text, or the result says page
images are unavailable, you have not seen the layout. Then report "structure
checked; rendered appearance not verified", name the reason from the result, and do not
claim visual review. The person sees page thumbnails in the web chat when they can be
made; do not rely on that as your own evidence.

If the tool refuses (unsupported type, over the size limit, password-protected,
preview component not installed or damaged), report its message as the blocker. Do
not look for another way to convert or render the file. Macro-enabled, legacy
(`.doc`, `.xls`, `.ppt`), OpenDocument and template formats cannot be previewed.

## Review criteria

All formats: missing glyphs or substituted fonts, clipped lines, collisions, bad wraps,
huge white gaps, blank pages, uneven spacing, stretched or cropped pictures, low-resolution
assets, captions separated from figures. The preview uses its own fonts and layout
engine; a good preview is not a Microsoft Office, Acrobat or print-shop compatibility test.

| Format | Also check |
|---|---|
| Word | stranded headings; tables outside margins, split labels, narrow columns, missing repeated headers, values next to the wrong row; header/footer, page numbers, section orientation, field values (fields may show stale text until updated in Word) |
| Excel | `####` cells, clipped or unreadable type, broken or misleading charts, unwanted blank pages, tables split across pages, totals at page breaks, repeated headings; numbers shown are calculated on open and are not stored in the file |
| PowerPoint | text overflow or clipping, overlapping boxes, crowded footers, poor contrast, tiny type, inconsistent alignment, distorted images, titles detached from their evidence, chart labels, axis scales and legends; speaker notes are not shown in the preview, review them separately in the file |
| PDF | rotation and page order, glyph coverage, broken tables, form values actually visible (a stored field value is not proof it shows), filled checkboxes, whether content landed on the intended pages |

Do not shrink text repeatedly until unreadable to hide overflow; shorten, split or
restructure in the source. Do not flatten an editable deliverable into images to meet a
visual target.

## Receipt

State: the output path; the source or template used; the semantic checks run; the
inspector result and what it does not cover; the pages actually seen, as "pages a-b of N"
for every batch, and any pages not seen; unresolved problems. Preview output is internal
evidence; the editable file is the deliverable. Do not send intermediate or private files
automatically.
