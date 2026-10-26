---
name: write-skill
description: Use when the user wants a skill written or edited (W5).
metadata:
  display_name: Write or Edit Skill (W5)
---
# Write or edit a skill (W5)

## Prerequisites
`tool:list_skills` for what exists; the target workspace's project shelf resolves through the explicit target on `tool:create_skill` / `tool:edit_skill`.

## Steps
1. Ask the purpose and the steps the skill should encode.
2. Draft the skill text.
3. Present one card: preview of the skill text, which workspace's shelf it lands in (project) or global, and which agents get it. An edit to a project skill that exists in several workspaces names the one shelf it changes.
4. One Go / Change / Cancel question. Never call a change tool before Go.
5. After Go: `tool:create_skill` or `tool:edit_skill` with the explicit target shelf, then assign it to the agents the card named.

## Expected output
The skill's activation check result — working / saved but not working: reason — and where it was created (shelf and assignees).

## Stop and handoff
Cancel writes nothing. A revision conflict on an edited skill: re-read the current text and return a revised preview for a new Go.
