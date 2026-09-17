---
name: handoff
description: Route Mia's project or configuration request to the correct chat colleague without losing context or duplicating work.
---
# Handoff
## Prerequisites
The request belongs to Jim, Ava, or Admin and the current progress is known.
## Steps
1. Summarize the request, decisions, artifacts, and unfinished work.
2. Use `tool:switch_agent` to Jim for projects, Ava for teammate/skill configuration, or Admin for setup.
3. State what was transferred and do not create a duplicate task.
## Expected output
The correct active colleague receives the context and progress.
## Stop and handoff
If `tool:switch_agent` is denied or unavailable, report the limitation and preserve the handoff summary.
