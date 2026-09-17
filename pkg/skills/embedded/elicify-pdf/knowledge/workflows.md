# PDF workflows

## Create a paginated report

ReportLab's flow-based layout avoids manual line-position arithmetic. Use
Paragraph, Spacer and Table inside a document template. Escape untrusted text
before handing it to Paragraph: its markup parser is not a plain-text sink.

```python
from html import escape
from reportlab.lib import colors
from reportlab.lib.pagesizes import A4
from reportlab.lib.styles import getSampleStyleSheet
from reportlab.platypus import SimpleDocTemplate, Paragraph, Spacer, Table, TableStyle

styles = getSampleStyleSheet()
doc = SimpleDocTemplate(output_path, pagesize=A4, rightMargin=42, leftMargin=42,
                        topMargin=42, bottomMargin=42)
story = [Paragraph(escape(report_title), styles['Title']), Spacer(1, 14)]
story.append(Paragraph(escape(summary), styles['BodyText']))
rows = [['Measure', 'Value'], ['Completed requests', '42']]
table = Table(rows, colWidths=[330, 120], repeatRows=1)
table.setStyle(TableStyle([
    ('BACKGROUND', (0, 0), (-1, 0), colors.HexColor('#204C60')),
    ('TEXTCOLOR', (0, 0), (-1, 0), colors.white),
    ('VALIGN', (0, 0), (-1, -1), 'TOP'),
    ('BOTTOMPADDING', (0, 0), (-1, -1), 8),
]))
story.extend([Spacer(1, 14), table])
doc.build(story)
```

Replace example values with sourced content. Wrap long table strings in
Paragraph objects, keep header repetition enabled and avoid one row taller
than the available page. Add page numbering via template callbacks. Register
and embed a suitable font for Unicode text; basic built-in fonts do not cover
every script. Complex scripts may require shaping support or a different
renderer. Render and inspect glyphs rather than trusting text extraction.

## Extract content accurately

Use `PdfReader(path).pages[i].extract_text()` for a first look at text pages.
Extraction order can differ from reading order. For multi-column layouts,
inspect bounding boxes or use pdfplumber's page/table methods and compare with
images. Empty extraction does not prove an empty page: it may be a scan or
outlined text. Do not fabricate missing text.

For each table, retain its source page and header context. Check row boundaries,
merged cells, repeated headings, decimal separators and negative numbers.
Provide raw uncertain text separately from normalized values when correctness
matters. Run large or untrusted parsing/rendering in bounded processes; some
PDFs expand into far more memory than their file size suggests.

## Merge, split and rotate

```python
from pypdf import PdfReader, PdfWriter

reader = PdfReader(source_path)
writer = PdfWriter()
# User asked for pages 3 then 1: zero-based indices 2 and 0.
for index in (2, 0):
    if not 0 <= index < len(reader.pages):
        raise ValueError('requested page is outside the document')
    writer.add_page(reader.pages[index])
writer.pages[0].rotate(90)
writer.write(output_path)
writer.close()
```

This simple split recipe copies selected pages, not a guarantee of full
catalogue metadata/bookmark/form preservation. For complete-document merging,
use `PdfWriter.append()` and review its outline and field behavior. Repeated
field names across inputs can collide: group/rename fields before merging
when their values must stay independent. Reconcile page labels, bookmarks,
internal links and document metadata with the new order. Never overwrite the
source. Reopen, verify exact order using known page content, and render rotated
pages to check both appearance and geometry.

## Fill and verify standard AcroForms

```python
from pypdf import PdfReader, PdfWriter

reader = PdfReader(source_path)
fields = reader.get_fields() or {}
if 'Company' not in fields:
    raise ValueError('expected Company form field is missing')
writer = PdfWriter()
writer.append(reader)  # Retains the form structures for this workflow.
writer.update_page_form_field_values(writer.pages[0], {'Company': 'Elicify'},
                                     auto_regenerate=False)
writer.write(output_path)
writer.close()
```

Use actual field names and the pages carrying their widgets. Checkboxes and
radio buttons use allowed appearance states, not arbitrary boolean strings;
inspect those states. Field parents and widgets can be separate objects, and a
field can appear on several pages. Test all its visible instances.

When the task adds a new page rotation, fill fields and generate appearances
before applying that requested rotation. Then render and inspect the rotated
output; generating appearances after rotation can produce a wrongly oriented
text box. For an input that is already rotated, first inspect the existing
page/widget transforms and test its rendered appearance. Do not blindly undo
existing rotation or treat one operation order as correct for every PDF.

`auto_regenerate=False` avoids relying on a viewer's request to regenerate
appearances; it is not proof that every field/font renders correctly. Reopen
and compare stored values, then render in a real PDF renderer and inspect the
filled text/checkboxes. Do not report completion if text exists in `/V` but is
invisible. Flattening is a separate requested operation: use a supported route
that bakes appearance into page content and removes the appropriate interactive
widgets; verify visual equivalence and absence of editable fields. Do not
pretend clearing `/AcroForm` alone performs correct flattening. XFA requires a
compatible engine; ordinary AcroForm code is not an XFA solution.

## Searchable OCR

Probe OCRmyPDF/Tesseract and their language packs; ask Admin to provision
missing components in Omnipus. A normal OCRmyPDF command is:

```text
ocrmypdf -l eng --skip-text /ABSOLUTE/INPUT.pdf /ABSOLUTE/SEARCHABLE.pdf
```

Substitute real absolute paths and the actual document language. `--skip-text`
keeps pages already containing text; it does not guarantee OCR quality or fix
bad existing OCR. Avoid forcing OCR across mixed documents without understanding
which existing text will be replaced. Check output page count/order, text
searchability, images, rotation and file size. Inspect names, dates, totals and
other important values against the scanned original. Report remaining low
confidence segments instead of claiming perfect transcription. OCR software
can modify signed documents; preserve the original and disclose signature
consequences before treating a transformed copy as authoritative.

## Render the exact output for inspection

```python
from pathlib import Path
import pypdfium2 as pdfium

preview_dir = Path(preview_path)
preview_dir.mkdir(parents=True, exist_ok=True)
pdf = pdfium.PdfDocument(output_path)
try:
    pdf.init_forms()  # Before len(pdf) or page access, so widgets are rendered.
    for index in range(len(pdf)):
        page = pdf[index]
        try:
            bitmap = page.render(scale=1.5)
            try:
                image = bitmap.to_pil()
                try:
                    image.save(preview_dir / f'page-{index + 1:04d}.png')
                finally:
                    image.close()
            finally:
                bitmap.close()
        finally:
            page.close()
finally:
    pdf.close()
```

Initialize forms immediately after opening the document, before requesting
its length or pages. Without this step, a preview may omit widgets even though
the saved fields contain correct values. This initializes rendering support;
it does not fill fields or repair missing appearance streams.

Choose a scale suitable for the page dimensions and memory budget; enormous
pages need downscaling, not unbounded allocation. Read these images with the
harness's image-capable file reader and zoom into small text or suspect areas.
Rendering success alone is not inspection. Keep the inspected output stable;
if you edit it after inspection, regenerate and inspect affected pages again.

## Sensitive transformations

For secure redaction, obtain the exact scope and use tooling that removes
content rather than covering it. Independently search/extract the result and
inspect images; also examine metadata, attachments, hidden layers and incremental
revisions. These checks can find failures but do not make a simple inspector a
security certifier. Escalate unsupported redaction instead of supplying a black
box overlay. For signed files, use a signature verification tool before making
claims about validity; merely finding a signature field proves nothing about
cryptographic validity or trust.

## References and provenance

Original workflows based on public APIs; no external skill implementation,
prompt, fixture or asset copied. API references checked 2026-09-17:

- [ReportLab documentation](https://docs.reportlab.com/reportlab/userguide/ch5_platypus/)
  — flow-based PDF layout.
- [pypdf page merging](https://pypdf.readthedocs.io/en/stable/user/merging-pdfs.html)
  — append, page selection and form merging.
- [pypdf forms](https://pypdf.readthedocs.io/en/stable/user/forms.html)
  — fields, widgets and appearance handling.
- [pypdf text extraction](https://pypdf.readthedocs.io/en/stable/user/extract-text.html)
  — extraction limitations and scanned pages.
- [pdfplumber](https://github.com/jsvine/pdfplumber)
  — positioned text and table extraction.
- [pypdfium2 API](https://pypdfium2.readthedocs.io/en/stable/python_api.html)
  — document/page rendering and resource lifetime.
- [OCRmyPDF cookbook](https://ocrmypdf.readthedocs.io/en/latest/cookbook.html)
  — language selection and OCR workflows.
