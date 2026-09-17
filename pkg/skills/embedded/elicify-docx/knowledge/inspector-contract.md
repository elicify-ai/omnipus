# Inspector contract and test plan

`inspect_docx(path)` reads a ZIP-based Word package without changing it. Its JSON
object contains: `paragraphs` (all main-document `w:p`, including nested and revision
content), `tables` (all main-document `w:tbl`), `revision_marks` (all `w:ins` and
`w:del` in Word XML parts), `field_instructions` (all `w:instrText` and `w:fldSimple`
in those parts), sorted `external_relationships` entries (`part`, `target`), and
sorted `media_parts`. Counts are inventory signals, not a preservation guarantee.
Move/property revision forms are not counted. It does not compute pages, resolve
fields, read linked resources, execute macros, or certify OOXML conformance.

Malformed ZIP raises `ValueError('not a ZIP-based Word document')`. A ZIP missing
`word/document.xml` raises `ValueError('missing word/document.xml')`. Malformed
Word XML or relationships raises `ValueError('malformed XML: <part>')`.
A missing filesystem path keeps `FileNotFoundError`. Inputs are trusted local
working artifacts; the helper is not an untrusted-upload security boundary.

Tests derive exact counts from literal XML fixtures, not observed inspector
output. ZIP reads and XML parsing are real, no mocks. Cases: empty document;
nested content/revisions/fields/media/external links; malformed ZIP; wrong ZIP;
malformed XML; missing path. Negative cases: four of six. Deliberate gaps:
large hostile archives, encrypted Office files, pixel/pagination correctness,
complete revision taxonomy. Mutation checks remove revision counting, invert
external relationship selection, and suppress malformed XML errors.
