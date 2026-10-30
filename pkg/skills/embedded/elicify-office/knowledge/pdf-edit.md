# Edit, extract from and fill a PDF

## Extract content accurately

Use `PdfReader(path).pages[i].extract_text()` for a first look at text pages.
Extraction order can differ from reading order. For multi-column layouts,
inspect bounding boxes and compare with the previewed pages. Empty extraction does not prove an empty page: it may be a scan or
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
source. Reopen, verify exact order using known page content, and preview rotated
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
before applying that requested rotation. Then preview the rotated
output; generating appearances after rotation can produce a wrongly oriented
text box. For an input that is already rotated, first inspect the existing
page/widget transforms and test its previewed appearance. Do not blindly undo
existing rotation or treat one operation order as correct for every PDF.

`auto_regenerate=False` avoids relying on a viewer's request to regenerate
appearances; it is not proof that every field/font renders correctly. Reopen
and compare stored values, then call `preview_document` and inspect the
filled text/checkboxes. Do not report completion if text exists in `/V` but is
invisible. Flattening is a separate requested operation: use a supported route
that bakes appearance into page content and removes the appropriate interactive
widgets; verify visual equivalence and absence of editable fields. Do not
pretend clearing `/AcroForm` alone performs correct flattening. XFA requires a
compatible engine; ordinary AcroForm code is not an XFA solution.


## What Omnipus does not provide

- **OCR.** No OCR tool is provisioned. For a scan, say searchable text is
  unavailable; install OCR through `environment_setup` only if the person asks.
- **Secure redaction.** Drawing a rectangle, cropping or deleting extracted text is
  not redaction. Report redaction as unavailable; never fake it.
- **Signature validation.** A `/Sig` field proves nothing about validity or trust.
  Editing a signed PDF may invalidate its signatures; say so before you do.
- **Flattening, XFA, tagged-PDF accessibility, PDF/A.** Not certified by pypdf or
  by a preview; do not claim them.

## References

Checked 2026-09-17: [pypdf page merging](https://pypdf.readthedocs.io/en/stable/user/merging-pdfs.html),
[pypdf forms](https://pypdf.readthedocs.io/en/stable/user/forms.html),
[pypdf text extraction](https://pypdf.readthedocs.io/en/stable/user/extract-text.html).
