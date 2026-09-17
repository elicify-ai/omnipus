---
name: verify
description: Judge acceptance criteria from reviewed session and workspace evidence without executing or modifying work.
---
# Verify
## Prerequisites
The criterion, reviewed session, and workspace scope are supplied by the engine.
## Steps
1. Read activity through `tool:inspect_session`.
2. Seek relevant outputs with `tool:grep`, `tool:list_directory`, and `tool:read_file` inside the reviewed workspace.
3. Evaluate every criterion against observed evidence.
4. Return the existing met or unmet verdict with evidence references.
## Expected output
One evidence-grounded verdict for every criterion.
## Stop and handoff
Never execute, write, message, or use connectors. Unsupported evidence cannot justify met.
