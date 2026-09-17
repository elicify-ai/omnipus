# Spreadsheet workflows

## Create a small model

Use `Workbook()`, choose meaningful sheet names, append headings and records,
and apply number formats after choosing actual data types. This example's
arithmetic is independently checkable: two units at 12.50 imply 25.00.

```python
from openpyxl import Workbook
from openpyxl.styles import Font, PatternFill, Alignment
from openpyxl.worksheet.table import Table, TableStyleInfo

wb = Workbook()
ws = wb.active
ws.title = 'Sales'
ws.append(['Item', 'Units', 'Unit price (USD)', 'Revenue (USD)'])
ws.append(['Notebook', 2, 12.50, '=B2*C2'])
for cell in ws[1]:
    cell.font = Font(bold=True, color='FFFFFF')
    cell.fill = PatternFill('solid', fgColor='204C60')
    cell.alignment = Alignment(wrap_text=True)
for column in ('C', 'D'):
    ws[f'{column}2'].number_format = '#,##0.00;[Red](#,##0.00)'
for column, width in {'A': 24, 'B': 12, 'C': 22, 'D': 22}.items():
    ws.column_dimensions[column].width = width
ws.freeze_panes = 'B2'
table = Table(displayName='SalesData', ref='A1:D2')
table.tableStyleInfo = TableStyleInfo(name='TableStyleMedium2', showRowStripes=True)
ws.add_table(table)
ws.print_options.horizontalCentered = True
ws.sheet_properties.pageSetUpPr.fitToPage = True
ws.page_setup.fitToWidth = 1
ws.page_setup.fitToHeight = 0
ws.print_title_rows = '1:1'
ws.print_area = 'A1:D2'
wb.save(output_path)  # Caller chooses a new absolute path; save overwrites.
```

Extend the example to the actual task; do not ship example data. For financial
work, document currency, taxes, rounding point, accrual/cash basis and rate units
where relevant. Derive expected totals independently (for example with Python
Decimal), then compare engine-calculated values using the declared rounding
rule. Never use a formula's own output as its independent oracle.

## Edit without casually losing features

Before editing, retain an untouched original and a feature inventory. For
ordinary existing files:

```python
from pathlib import Path
from openpyxl import load_workbook

macro_enabled = Path(source_path).suffix.lower() in {'.xlsm', '.xltm'}
wb = load_workbook(source_path, data_only=False, keep_vba=macro_enabled,
                   keep_links=True, rich_text=True)
# Make the smallest requested cell/style change; keep the correct file suffix.
wb['Inputs']['B4'] = 0.075
wb.save(output_path)
wb.close()
```

Load a separate `data_only=True` instance only to inspect cached results; never
save that instance as the edited formula workbook. Keep template versus normal
workbook status and macro extension consistent. Preservation flags do not
execute or author macros. `keep_links=True` preserves link information; do not
refresh it as part of validation without user authorization.

Excel has features that openpyxl does not fully preserve. Especially inspect
drawings/shapes, slicers, pivots, advanced charts, rich text and unsupported
extensions. If a source contains features whose preservation cannot be shown,
use a capable application or report the limitation before promising fidelity.
Do not silently rebuild a complex workbook from cell values. After editing,
compare package parts and relevant feature properties, not ZIP bytes (metadata
and serialization can legitimately change). Check macro binary hashes and
external link targets when these must remain unchanged.

Inserting/deleting rows or moving formulas can leave names, chart ranges,
validation, tables or formula references pointing to old locations. Inventory
and update all affected references explicitly. Formula text equality alone
cannot prove the model still refers to the intended data.

## Recalculate and export

Choose an available real spreadsheet engine. Use an isolated application
profile and output folder for LibreOffice batch conversion; for Excel use a
supported automation route with macros and external-link refresh disabled.
Do not execute source-supplied macros merely to get a number. Probe engine
version and capabilities; do not assume an executable name is installed.

A typical available LibreOffice CLI workflow is:

```text
libreoffice -env:UserInstallation=file:///ABSOLUTE/ISOLATED/PROFILE \
  --headless --convert-to xlsx --outdir /ABSOLUTE/RECALCULATED /ABSOLUTE/INPUT.xlsx
libreoffice -env:UserInstallation=file:///ABSOLUTE/ISOLATED/PROFILE \
  --headless --convert-to pdf --outdir /ABSOLUTE/PREVIEW /ABSOLUTE/RECALCULATED/INPUT.xlsx
```

These are placeholders, not runnable paths. Derive the profile URI correctly,
use separate input/output directories, check exit status AND expected output
existence, and reopen the exact engine-produced output. Inspect formula caches
again and compare known calculations. LibreOffice and Excel can disagree on
functions and preserve different features: conversion is not fidelity proof.
Do not use this route for a sensitive macro workbook without preservation
checks. Missing caches after the run mean recalculation remains unverified.

Render the PDF pages and inspect actual images. For a large workbook, inspect
all summary/decision sheets and every print page in the delivery scope; record
any sampled remainder explicitly. Check totals at page breaks, repeated
headings and legend readability. A screenshot is evidence only for what it
shows, not unseen sheets.

## Import untrusted CSV and export safely

Use `csv.reader`/`csv.writer` with the known delimiter and encoding; open with
`newline=''`. Confirm schema and locale before converting strings to dates or
numbers. Values like `00123`, `1,234` and `03/04/2026` are ambiguous until the
column contract is known. Reject malformed rows or report exact repairs rather
than dropping them unnoticed.

A string beginning with `=`, `+`, `-`, `@`, tab or carriage return may be
interpreted as a formula by a spreadsheet application. For untrusted textual
input assigned to XLSX, set `cell.value = value` and then `cell.data_type = 's'`;
verify after reload that it remains a string. Keep genuinely validated numeric
negative values numeric. Never promote imported strings to formulas merely
because they start with `=`.

CSV quoting does not neutralize formulas. For a human-opened CSV export, prefix
risky untrusted text with an apostrophe under an explicit export policy,
including control characters or leading whitespace before a formula marker.
Document that this changes the raw field. If exact bytes must be retained,
deliver a text-safe XLSX or non-spreadsheet transport plus the unchanged raw
source; do not claim a universally safe CSV dialect. Test the target spreadsheet
application's import behavior. TSV has the same risk.

## References and provenance

Original workflow authored from public library interfaces; no third-party
skill prompt, script, fixture or asset was copied. API references checked
2026-09-17:

- [openpyxl tutorial](https://openpyxl.readthedocs.io/en/stable/tutorial.html)
  — creation, loading options and preservation limits.
- [openpyxl formula documentation](https://openpyxl.readthedocs.io/en/stable/simple_formulae.html)
  — formulas are stored, not calculated.
- [openpyxl tables](https://openpyxl.readthedocs.io/en/stable/worksheet_tables.html)
  — tables require unique names and string headers.
- [Python CSV module](https://docs.python.org/3/library/csv.html)
  — dialects and newline handling.
- [LibreOffice command-line parameters](https://help.libreoffice.org/latest/en-US/text/shared/guide/start_parameters.html)
  — conversion and isolated user installation parameters.
- [OWASP CSV injection](https://owasp.org/www-community/attacks/CSV_Injection)
  — spreadsheet formula interpretation and export mitigations.
