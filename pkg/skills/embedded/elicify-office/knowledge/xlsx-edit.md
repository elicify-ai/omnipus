# Edit a workbook

Before editing, inventory sheets (including hidden), formulas and cached values,
names, tables, charts, validation, conditional formatting, protection, external
links, macros, drawings and print settings relevant to the change; run
`scripts/inspect_workbook.py` for the formula, cache, error, macro and link facts
([contract](inspector-contract.md)). Keep the original and write a new output.
Edited formulas calculate on open like new ones (see [xlsx-create](xlsx-create.md));
do not try to recalculate the file. After saving, reopen it and
use `preview_document` ([verify](verify.md)).

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

