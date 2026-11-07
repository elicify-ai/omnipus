---
name: write-skill
description: Use when the user wants a skill written or edited (W5).
metadata:
  display_name: Write or Edit Skill (W5)
---
# Write or edit a skill (W5)

## Prerequisites
`tool:list_skills` for what exists; `tool:get_skill` (or `tool:Skill`) to read the skill you will change, because a change needs a current read of it. A skill is a folder: a global skill lives in its own folder under the skills directory, a project skill in the target workspace's project shelf folder. Built-in skills are read-only; to change one, copy it first.

## Steps
1. Ask the purpose and the steps the skill should encode.
2. Draft the skill text.
3. Present one card: preview of the skill text, which workspace's shelf it lands in (project) or global, and which agents get it. An edit to a project skill that exists in several workspaces names the one shelf it changes.
4. One Go / Change / Cancel question. Never call a change tool before Go.
5. After Go: create a new skill by writing its `SKILL.md` first with `tool:write_file` in a new folder of the target shelf, then any helper files; edit an existing skill's files with `tool:write_file` or `tool:edit_file`; use `tool:edit_skill` to copy a skill (built-ins included), delete a file, delete the skill or restore an earlier version. Then assign it to the agents the card named — a new skill or copy starts with no agent granted.

## Expected output
The skill's activation check result — working / saved but not working: reason — and where it was created (shelf and assignees).

## Stop and handoff
Cancel writes nothing. A skill changed since you read it: re-read it with `tool:get_skill` and return a revised preview for a new Go.
