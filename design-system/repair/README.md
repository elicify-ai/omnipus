# Repair data

Input data for the C1 repair codemods in `scripts/design-system/`. Tracked here,
not under `dist/`, because the scripts that read it are committed: a tool whose
data source is gitignored cannot run on a fresh checkout, however green its own
tests are (the tests build their own fixtures and so never notice).

| File | Read by | What it is |
|---|---|---|
| `spacing-mapping.json` | `codemod-spacing-arbitrary`, `codemod-spacing-tailwind-class`, `codemod-spacing-css` | Every distinct spacing value found in the app, with its current pixel size, the D10 scale step it maps to, and a group: `IDENTICAL`, `NORMALIZED`, or `NEEDS-DECISION`. The codemods apply only the first two and refuse the third. |
| `colour-safe-split.json` | `codemod-colour-exact` | The colour literals that are byte-identical to exactly one registered token. Anything ambiguous or merely similar is excluded here rather than judged at runtime, which is why that codemod refuses far more sites than it edits. |

Each file was generated once during C1 preparation and is an input, not an
artifact: regenerating it would re-derive values from source that the repairs
have since changed. Treat it as a record of the decisions the repairs executed.

A missing file is a hard error, not an empty run — verified by hiding
`spacing-mapping.json` and observing exit 1 rather than a zero-edit success.
