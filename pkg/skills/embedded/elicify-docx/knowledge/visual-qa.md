# Render and inspect every page

Word layout depends on renderer, fonts, page size, and field evaluation. Record
the renderer and versions; do not describe a LibreOffice preview as a Microsoft
Word compatibility test.

## Conversion route

Use an already approved Word or LibreOffice renderer. With LibreOffice available,
run the executable via an argument list from Python, supply a dedicated project
profile directory, and render into a separate project preview directory:

```python
from pathlib import Path
import subprocess

# source, preview_dir, profile_dir are resolved absolute project paths.
preview_dir.mkdir(parents=True, exist_ok=True)
profile_dir.mkdir(parents=True, exist_ok=True)
subprocess.run([
    libreoffice_executable,
    '-env:UserInstallation=' + profile_dir.as_uri(),
    '--headless', '--convert-to', 'pdf', '--outdir', str(preview_dir),
    str(source),
], check=True, timeout=120)
pdf = preview_dir / (source.stem + '.pdf')
if not pdf.is_file() or pdf.stat().st_size == 0:
    raise RuntimeError('renderer did not produce the expected PDF')
```

Use a fresh preview directory for each revision so a stale PDF cannot pass the
existence check. LibreOffice is a separate prerequisite, not installed by
python-docx. Missing renderer/fonts are blockers; follow the already-loaded
harness runtime notes. Never claim rendering happened if it did not. An approved
application export is also valid.

Rasterize the PDF with the Elicify PDF skill or an approved PDF renderer into
numbered PNG pages at readable resolution (start around 150–200 DPI). Open each
PNG with a tool that supplies visible image content to the model. A contact
sheet helps navigation but does not replace full-page checks of small text.

## Review criteria

Check all pages for:

- missing glyphs, substituted fonts, clipped lines, collisions, and bad wraps;
- stranded headings, huge white gaps, blank pages, and uneven spacing;
- tables extending outside margins, split labels, unreadable narrow columns,
  missing repeated headers, and values visually associated with the wrong row;
- stretched/cropped pictures, low-resolution assets, and captions separated from
  their figures;
- correct header/footer, page numbering, section orientation, and field values.

Compare the visible content to the verified fact table, including dates, signs,
units, and totals. Fix the source and repeat. Do not patch only the preview.

## Receipt and known gaps

State: output path; source/template used; semantic checks; renderer; number of
pages actually inspected; unresolved problems. If only structure was checked,
say “structure checked; rendered appearance not verified” and identify the
missing dependency or image capability. Do not automatically send inspection
images externally. The final editable file is the deliverable; preview images
are internal evidence unless the user asks for them.

Sources: [LibreOffice PDF export command parameters](https://help.libreoffice.org/latest/en-US/text/shared/guide/pdf_params.html),
[LibreOffice startup parameters](https://help.libreoffice.org/latest/en-US/text/shared/guide/start_parameters.html).
