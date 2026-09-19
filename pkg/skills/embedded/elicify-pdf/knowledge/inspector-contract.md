# PDF inspector contract and test plan

`inspect_pdf(path, password=None)` reads without changing the file and returns
`page_count`, ordered `pages` (one-based number, width_points, height_points,
rotation), `fields` (sorted names), `signature_fields` (sorted signature field
names), and `encrypted` (original file state). It does not verify a signature,
extract secrets, certify rendering, or claim that a field has a visible widget.
Page dimensions use MediaBox coordinates before rotation, in PDF points.

Missing file raises FileNotFoundError. Broken PDF raises
`ValueError('invalid PDF')`. Encrypted PDF without a valid password raises
`ValueError('PDF password required or incorrect')`. User-provided passwords
are function inputs; CLI passwords come from an environment variable, not argv.

Tests create real pypdf files with exact geometry: zero pages; two pages of
72x144 and 144x72 points; second rotated 90 degrees; encrypted input correct,
wrong and missing passwords; real AcroForm text/signature fields; missing and
corrupt files; original byte preservation; CLI missing password-variable errors
and non-disclosure of an incorrect password. No mocks. Mutations: erase rotation;
reverse pages; erase signature classification. Rendering, OCR accuracy, secure
redaction and digital signature validation are outside this helper's scope.
