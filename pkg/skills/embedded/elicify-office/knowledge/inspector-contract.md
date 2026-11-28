# Inspector contracts and test plan

Four read-only scripts under `scripts/` inventory a package without changing it. Run
them with the document Python and an absolute input path, for example
`scripts/inspect_docx.py /abs/path/file.docx`. They report facts a preview cannot
show; none of them certifies appearance, preservation or validity, and a clean run is
never visual proof. Tests derive exact expectations from literal fixtures, not from
observed output; ZIP reads and parsing are real; mutations (listed per script) must turn
at least one case red.

## inspect_docx.py (stdlib only)

`inspect_docx(path)` returns `paragraphs` (all main-document `w:p`, including nested
and revision content), `tables` (all main-document `w:tbl`), `revision_marks` (all
`w:ins` and `w:del` in Word XML parts), `field_instructions` (all `w:instrText` and
`w:fldSimple` in those parts), sorted `external_relationships` entries (`part`,
`target`) and sorted `media_parts`. Counts are inventory signals. Move and
property revision forms are not counted. It does not compute pages, resolve fields,
read linked resources, execute macros or certify OOXML conformance.

Errors: malformed ZIP raises `ValueError('not a ZIP-based Word document')`; a ZIP
without `word/document.xml` raises `ValueError('missing word/document.xml')`;
malformed Word XML or relationships raise `ValueError('malformed XML: <part>')`; a
missing path keeps `FileNotFoundError`. Inputs are trusted local artifacts; this is
not an untrusted-upload boundary.

Cases: empty document; nested content, revisions, fields, media, external links;
malformed ZIP; wrong ZIP; malformed XML; missing path. Gaps: large hostile archives,
encrypted files, pagination, complete revision taxonomy. Mutations: remove revision
counting, invert external-relationship selection, suppress malformed-XML errors.

## inspect_workbook.py (openpyxl)

`inspect_workbook(path)` accepts `.xlsx`, `.xlsm`, `.xltx`, `.xltm` (any case) and
returns ordered `sheets` (name, rows, columns), `formulas`, `missing_formula_caches`,
`errors` (sheet, cell and value; stored and cached errors both count), and package
booleans `has_macros`, `has_external_links`. A missing formula cache is unknown, never
zero; under calculate-on-open every freshly written formula has one. Dimensions derive
from actual stored cells (1x1 for an empty sheet); producer-supplied dimensions must
not hide stored cells. It does not execute formulas, macros or links, and a clean report
does not show that populated caches are fresh.

Errors: unsupported extension raises `ValueError('unsupported workbook extension')`;
a missing file raises `FileNotFoundError`; a broken package raises
`ValueError('invalid workbook package')`. Input bytes must not change.

Cases: empty sheet (1x1); formula without cache; cached zero; stored and cached errors;
two sheets in order; unsupported extension; missing and corrupt files; valid ZIP without a
workbook part; undersized declared dimensions; inert macro and external-link parts. A
cached value is inserted into the worksheet XML to model an Excel-produced cache.
Mutations: erase the missing-cache report, erase errors, reverse sheet order.

## inspect_pptx.py (python-pptx)

`inspect_pptx(path)` returns `slide_count`, `width_emu`, `height_emu` and
`out_of_bounds`: ordered `{slide, shape_id}` for top-level shapes whose unrotated
bounding boxes leave the slide. Slides count from one. Touching an edge is allowed; one
EMU beyond any edge is reported; a shape is reported once however many edges it crosses.
Rotation, group child transforms, intentional bleed, inherited master content, text
overflow, contrast and overlaps need the preview.

Errors: `FileNotFoundError` for a missing path; `ValueError('not a ZIP-based
presentation')`; `ValueError('missing ppt/presentation.xml')`; other load errors propagate.

Cases: decks fixed at 1,000,000 x 1,000,000 EMU with shapes at -1/0/+1 on all four edges;
zero slides; missing file; malformed ZIP; wrong ZIP; byte non-mutation. Mutations:
inclusive edge comparison, disable the negative-x check, suppress the negative-y check.

## inspect_pdf.py (pypdf)

`inspect_pdf(path, password=None)` returns `page_count`, ordered `pages` (one-based
`number`, `width_points`, `height_points`, `rotation`), sorted `fields`, sorted
`signature_fields` (`/FT` is `/Sig`) and `encrypted` (the original file's state). Page
size is the MediaBox in points before rotation. It does not verify signatures, extract
secrets, certify rendering or show that a field has a visible widget. The command line
takes `--password-env NAME`, the name of an environment variable; never put a password in
arguments or scripts.

Errors: `FileNotFoundError` for a missing path; `ValueError('invalid PDF')`;
`ValueError('PDF password required or incorrect')` for an encrypted file without a valid
password.

Cases: zero pages; two pages of 72x144 and 144x72 points with the second rotated 90;
encrypted with correct, wrong and missing password; real AcroForm text and signature
fields; missing and corrupt files; byte preservation; missing password variable and
non-disclosure of a wrong password. Mutations: erase rotation, reverse pages, erase
signature classification. Rendering, OCR, redaction and signature validation are outside
the script.
