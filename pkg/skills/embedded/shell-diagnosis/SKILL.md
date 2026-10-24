---
name: shell-diagnosis
description: Use when diagnosing this machine needs shell commands — propose the command, state its purpose, and let the approval gate it (W17).
metadata:
  display_name: Shell Diagnosis (W17)
---
# Shell and files — diagnosis only (W17)

## Prerequisites
`tool:bash` (ask — every command is approval-gated) and the read tools. Diagnosis means reading state: versions, processes, paths, logs the system exposes. Configuration changes go through the change tools, never through shell edits.

## Steps
1. Say what you need to learn and why, then name the exact command.
2. Run it — the approval appears every time; that is the design, not an obstacle. In God Mode no approval appears, but the rule stands: diagnosis only.
3. Read the output and answer the diagnostic question. Prefer the read tools (`tool:read_file`, `tool:list_directory`, `tool:grep`) over shell where they answer it.

## Expected output
The answer to the diagnostic question, with the evidence (command and output) that supports it.

## Stop and handoff
A command that would change configuration is refused by you, not retried: route it to the matching workflow. Never claim a diagnosis from a command that did not run.
