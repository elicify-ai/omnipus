# Visual quality and delivery evidence

## Structural preflight is only the first step

Reopen the deck and check required text, slide count/order, notes, native chart
data, and source features promised as preserved. Run the bundled inspector to
find unrotated top-level boxes outside slide bounds. Investigate each result:
intentional bleed can be valid, so do not blindly move every flagged shape.

The inspector does **not** detect text overflow, rotated extents, child shapes
inside transformed groups, inherited master/layout content, overlap, or visual
contrast. A shape inside the slide can still contain clipped or unreadable text.

## Render the final editable source

Use an approved installed PowerPoint or LibreOffice export to PDF. With
LibreOffice, invoke it through a subprocess argument list, use a fresh project
preview directory and a dedicated profile, and check the expected PDF exists:

```python
import subprocess

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

Here paths are resolved absolute project paths; `libreoffice_executable` is the
approved binary. Start with an empty output directory to exclude stale files.
A separate LibreOffice installation and suitable fonts are required; python-pptx
does not include them. Missing prerequisites go to Admin in Omnipus. Do not
claim a LibreOffice export verifies PowerPoint animation/playback compatibility.

Use the Elicify PDF skill or an approved PDF renderer to create one PNG per
slide. Open every image through a tool returning actual image content. A contact
sheet can reveal pacing and consistency, but inspect each slide at readable
resolution for small labels and clipping. Check PDF page count against expected
slides; investigate hidden-slide/export-setting differences instead of silently
accepting missing slides. Review notes separately because a normal slide export
does not show speaker notes.

## Check every slide

Look for clipped text and chart labels, overlapping boxes, crowded footers,
poor contrast, tiny type, inconsistent alignment, distorted images, missing
symbols/fonts, and titles detached from their evidence. Confirm visible values,
signs, units, date ranges, axis scales, legends, and citations. Check that chart
colors or layout do not imply a different conclusion from the source data.

Fix the editable source, regenerate, and inspect the changed slides again.
After template/theme changes, inspect every slide again. Prefer rewriting or
splitting dense slides to shrinking text. If the renderer substituted fonts,
record the substitute and check the resulting wrapping.

## Deliver honestly

Provide the editable deck and a short receipt: number of slides, what remains
editable, source/template, structural checks, renderer, slides actually viewed,
and any unresolved limitation. If rendering or image input was unavailable,
state “structure checked; rendered appearance not verified” and name the blocker.
Inspection artifacts stay internal unless requested. If accessibility compliance
or application fidelity matters, include results from the appropriate target
application checker; do not imply the Python preflight certified either.

Sources: [LibreOffice PDF export parameters](https://help.libreoffice.org/latest/en-US/text/shared/guide/pdf_params.html),
[startup parameters](https://help.libreoffice.org/latest/en-US/text/shared/guide/start_parameters.html).
