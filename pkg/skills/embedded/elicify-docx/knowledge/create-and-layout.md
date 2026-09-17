# Create and lay out a Word document

## Plan the reading experience

Decide what the reader must learn or decide on page one. Put the recommendation,
summary, or requested action early; then evidence and detail. Match the requested
format: a letter needs a recipient and closing, minutes need decisions and
owners, a proposal needs scope and exclusions. Avoid invented names, metrics,
logos, signatures, or citations.

Use a provided template if it is suitable. Otherwise choose a modest style
system: one body font available in the rendering environment, a clear title,
two or three heading levels, a restrained accent, and consistent spacing.
Specify page size and margins explicitly when the output contract depends on
them. Do not assume an installed font merely because the XML names it.

## Executable starting point

Run this in the project's approved Python environment. Replace the example
content with the user's verified content, and pass an absolute output path.

```python
from pathlib import Path
import sys
from docx import Document
from docx.shared import Inches, Pt

out = Path(sys.argv[1])
out.parent.mkdir(parents=True, exist_ok=True)
doc = Document()
section = doc.sections[0]
section.page_width, section.page_height = Inches(8.27), Inches(11.69)
section.top_margin = section.bottom_margin = Inches(0.8)
section.left_margin = section.right_margin = Inches(0.85)
normal = doc.styles['Normal']
normal.font.name, normal.font.size = 'Arial', Pt(11)
normal.paragraph_format.space_after = Pt(6)
for name in ('Heading 1', 'Heading 2'):
    doc.styles[name].paragraph_format.keep_with_next = True

doc.add_heading('Delivery proposal', 0)
doc.add_paragraph('Prepared for review — example content')
doc.add_heading('Decision requested', 1)
doc.add_paragraph('Approve the scope and owners below.')
table = doc.add_table(rows=1, cols=3)
table.style = 'Table Grid'
for cell, text in zip(table.rows[0].cells, ['Deliverable', 'Owner', 'Due']):
    cell.text = text
for values in [('Prototype', 'Owner to confirm', 'Date to confirm'),
               ('Review', 'Owner to confirm', 'Date to confirm')]:
    for cell, text in zip(table.add_row().cells, values):
        cell.text = text
doc.core_properties.title = 'Delivery proposal'
doc.save(out)
check = Document(out)
assert check.paragraphs[0].text == 'Delivery proposal'
assert len(check.tables[0].rows) == 3
```

The assertions establish only these example facts, not visual quality. Remove
example placeholders before delivery or explicitly identify them as open input.
Use built-in English style names in the API; supplied templates may add custom
styles, so inspect their actual names first.

## Tables, figures, and pagination

- Use real table cells, headers, and numeric alignment, not tab-separated text.
  Size the table to the writable page width. Long descriptions generally need
  a wider first column; do not shrink all type to fit one oversized row.
- Tables can span pages. Check repeated header behavior in the target renderer;
  python-docx has no high-level switch for every Word table feature. If setting
  a repeated header via `w:tblHeader`, isolate that XML change and validate it.
- Set heading `keep_with_next`, consider paragraph widow/orphan control, and use
  explicit page breaks only at meaningful sections. Excessive `keep_together`
  may create large white gaps or push oversized content unpredictably.
- Add images with one dimension to preserve aspect ratio. If the request needs
  a crop, perform and review the crop explicitly. Add captions and meaningful
  alternative descriptions using verified document tooling; do not assume an
  image's filename supplies accessible alternative text.
- Use a section break for a landscape table, then restore portrait settings.
  Headers/footers may be linked to earlier sections; verify linkage before edits.
- A table of contents, page number, or cross-reference is a Word field. Inserting
  its XML does not evaluate it. Prefer a maintained template and update fields
  in the target application, then verify the displayed result.

## Final semantic checks

Read headings in order, inspect all tables, and confirm totals independently.
Search for template markers, duplicated sections, draft comments, and accidental
personal metadata. Check hyperlinks and references without following untrusted
external content automatically. For accessibility, use real heading structure,
logical reading order, meaningful links, and the target application's checker;
structural extraction alone does not prove accessibility compliance.

## Official reference

API facts were checked on 2026-09-17. Consult the installed version when behavior
is uncertain: [document API](https://python-docx.readthedocs.io/en/latest/api/document.html),
[paragraph and character formatting](https://python-docx.readthedocs.io/en/latest/user/text.html),
and [working with styles](https://python-docx.readthedocs.io/en/latest/user/styles-using.html).
