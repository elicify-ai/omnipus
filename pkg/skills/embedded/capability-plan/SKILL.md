---
name: capability-plan
description: Use inside create-agent and team-setup to map a purpose to abilities and to the tools, skills, connectors and dependencies that cover them.
metadata:
  display_name: Capability Plan
---
# Capability plan (sub-skill of W1 and W18)

## Prerequisites
Read-only look-ups only: `tool:find_skills` and `tool:list_skills`, `tool:get_agent_tools`, `tool:list_mcp_servers`, `tool:list_providers`. No change tool is called from this skill.

## Steps
1. Derive the abilities the purpose needs (one line each).
2. Map every ability to its cover: an existing tool; a skill already installed (assign); a catalogue skill (install); a skill to write; a connector; a software dependency; an AI model.
3. For each ability choose the fix in this order: assign installed → install from catalogue → write new → add connector → install dependency. Apply the target workspace to every look-up (project shelves are per workspace).
4. Mark what remains: an ability nothing covers is a **gap** — listed, never hidden, never silently dropped.

## Expected output
A coverage table the calling workflow puts into its summary: ability → cover (have / install / create / missing) → the chosen fix — plus the gap list.

## Stop and handoff
An ambiguous ability is asked about in the calling workflow's interview, never guessed. This skill never proposes; it computes the plan the caller confirms.
