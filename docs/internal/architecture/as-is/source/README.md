# Source of the as-is architecture document

`docs/internal/architecture/AS-IS-architecture.md` and the diagrams in `docs/internal/architecture/as-is/*.svg` are generated from this folder. Edit the sources, then regenerate; never edit the generated files by hand.

| Path | What it holds |
|---|---|
| `flows/*.yaml` | One traced flow each: lanes, steps with code references, stores, failures, key facts (format: `flow-format.md`) |
| `sections/*.md` | The written sections after the flows, in file-name order |
| `extra.yaml` | Scope, open points, method and maintenance sections |
| `tools/` | The generator: `build_formal.py` (document), `seq.py` (flow diagrams), `runtime_map.py` (server picture) |

Regenerate from the repo root (Python 3 with PyYAML):

    python3 docs/internal/architecture/as-is/source/tools/build_formal.py docs/internal/architecture/as-is/source docs/internal/architecture/AS-IS-architecture.md as-is -
