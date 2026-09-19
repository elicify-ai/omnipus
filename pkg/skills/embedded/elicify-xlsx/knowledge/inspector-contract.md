# Workbook inspector contract and test plan

`inspect_workbook(path)` is read-only. It accepts `.xlsx`, `.xlsm`, `.xltx`,
`.xltm` (case insensitive) and returns a JSON-compatible dictionary with ordered
`sheets` (name, rows, columns), `formulas`, `missing_formula_caches`, `errors`
(each identified by sheet and cell; errors also have a value), and package
booleans `has_macros`, `has_external_links`. Missing formula caches are unknown,
never zero. Stored error values and formula cached errors both count. It does
not execute formulas, macros or links, evaluate correctness, or assert fidelity.
Dimensions derive from actual stored cells, with 1x1 for an empty sheet;
producer-supplied dimensions must not hide stored cells.
A clean report does not certify that populated caches are fresh.

Unsupported extension raises `ValueError('unsupported workbook extension')`;
missing file raises FileNotFoundError; a broken package raises
`ValueError('invalid workbook package')`. Inspection must not modify bytes.

Tests use real tiny workbooks; one fixture's cached value is inserted into its
OOXML worksheet to independently model an Excel-produced cache. Expected sheet
order, coordinates and arithmetic derive from the fixture recipe, not inspector
output. Cases: empty sheet (1x1), formula without cache, cached zero, stored and
cached errors, two sheets, unsupported extension, missing and corrupt files, and a valid ZIP with no workbook part.
Additional cases cover undersized declared dimensions and inert macro/external-link
package presence. These marker fixtures verify inventory, not macro usability.
No mocks. Mutations: erase missing-cache report; erase errors; reverse sheet
order. Real spreadsheet recalculation, unsupported OOXML preservation and visual
layout are outside this helper's scope and require the full skill evaluations.
