# Updating the Office preview closed-environment boundary pins

These pins protect the Q62 / AD-7 / FR-009 / MV-2 exception to the shared spawn inventory. They are not a general permission to inherit the gateway environment. Normal tests only read the pins; they never regenerate them automatically.

## When a pin fails

| Failure | Required action |
|---|---|
| A named boundary declaration changed | Ask `security-lead` to review the source change against the exact environment contract, hooks/retry and forwarding/startup requirements. Keep the review receipt with the change. Only after that review should the explicit updater record new digests. |
| A new producer, caller, declaration or launcher file appeared | The new boundary must be reviewed first. Update the approved declaration/file list and exact caller list only as that review permits. Regenerating hashes alone does not approve a new caller or startup environment write. |
| An unrelated declaration in a shared file changed | This should not fail the boundary pins. Run the scope fixtures and report the overbroad pin; do not rebaseline it to hide the problem. Launcher files are the exception: their whole content is the boundary and new declarations there require review. |
| The fixed lexical digest vector failed after a Go upgrade | Investigate the lexer/encoding change first. Do not regenerate all pins just to make the test pass. The digest does not use the Go formatter. |

## Explicit update route

The opt-in switch is `OMNIPUS_OFFICE_UPDATE_ENV_PINS=1`. Only `TestWU19_OfficePreviewClosedEnvironment` consumes it. An unset switch performs no write; any nonempty value other than `1` fails. The updater writes only the pin fixture in the test tree, by temporary file and rename, and then runs the positive/negative inventory fixtures. It logs `EXPLICIT PIN UPDATE` and the source revision. That log records bytes, **not proof that a security review happened**.

On the founder's Mac, with the working directory set to `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/office-preview-red`, the exact command is below. Pick a new absolute log filename if this example already exists. `github.com/elicify-ai/omnipus/pkg/sandbox` is the Go package name, not a filesystem path.

```bash
env CGO_ENABLED=0 OMNIPUS_OFFICE_UPDATE_ENV_PINS=1 \
  bash /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/omnipus-l1/slotrun.sh \
  office-tests /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/office-preview-env-pin-update.log \
  go test -tags goolm,stdjson -count=1 -p 1 -v \
  -run '^TestWU19_OfficePreviewClosedEnvironment$' \
  github.com/elicify-ai/omnipus/pkg/sandbox
```

On another machine, use its installed shared heavy-command runner and a new absolute evidence-log path. Do not create private parallel capacity, run recursive package patterns or launch a full local suite.

After updating, inspect and commit the pin diff at `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/office-preview-red/tests/officepreview/closed-env-source-pins.json` with the reviewed change. Run the ordinary inventory and scope fixtures **without the switch**, one command at a time through the same shared runner. Retain the commands and direct exit codes. Do not weaken a negative fixture to accept a new environment.

| Test-tree implementation on this checkout | Purpose |
|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/office-preview-red/pkg/sandbox/wu19_office_preview_pins_test.go::wu19OfficePinScope` | Approved declaration/file scope and explicit updater. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/office-preview-red/pkg/sandbox/wu19_office_preview_contract_test.go::officePreviewBoundaryReferences` | Exact production producers and caller counts. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/office-preview-red/pkg/sandbox/wu19_office_preview_contract_test.go::officePreviewStartupEnvironment` | Project initialization/global-initializer environment checks. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/office-preview-red/pkg/sandbox/wu19_office_preview_scope_test.go` | Unrelated edits remain allowed; pinned declarations remain protected; stable lexical-vector controls. |

## Scope and stable digest format

Only named declarations on the closed-environment path are pinned in shared files. In particular, the application file pins `Main` and `runHiddenMode`, and the command entry file pins `main`, not every function in those files. Relevant import bindings are included with a declaration; adding an unrelated import used only elsewhere does not change it. Dot/blank imports are included because they can affect unqualified names or startup. The launcher files keep whole-file coverage, plus per-declaration digests so a failure can name the changed declaration.

`office-preview-env-pins/go-tokens-v1` hashes this explicitly versioned encoding with SHA-256:

| Part | Encoding |
|---|---|
| Every component string | UTF-8 bytes prefixed by its unsigned 32-bit big-endian byte length. |
| Header | The version string, then `tokens`. |
| Source | Ordered Go token **spellings** and literal bytes, not numeric token IDs, source positions or formatter output. Keyword/operator literals are empty because their spelling already identifies them. Comments are omitted. |
| Semicolons | Explicit and inserted semicolons use the same value. Optional semicolons immediately before a closing brace/parenthesis or the end of the source are omitted; other semicolons remain. |
| Import bindings | `imports`, followed by alias/path pairs in sorted alias order. Declaration kind and package identity are included where needed. |

No `go/format` or AST pretty-printer output enters the digest. A formatter-only toolchain change therefore cannot rehash unchanged source. The lexer still comes from Go: a genuine lexical-language change or bug fix could require investigation, and the fixed independently encoded vector is intended to make that visible. This is not a claim of having executed the tests on every Go version.

The independent CHECK instance, not the test author, re-probes production mutants and decides the audit outcome. These instructions and a successful regeneration are not a product PASS or security approval.
