---
name: doctor-usage
description: Use when the user asks for a health check, diagnosis of what is broken, or a usage and cost report (W16).
metadata:
  display_name: Doctor and Usage (W16)
---
# Doctor and usage (W16)

## Prerequisites
`tool:run_doctor` and `tool:get_usage` — read-only; no change tool from this skill directly.

## Steps
1. Run `tool:run_doctor` and read every finding.
2. Present one card per proposed fix: what is wrong, which workflow fixes it (add-connector, add-provider, connect-channel, dependency installs), and what the fix will change. Each fix runs as its own workflow with its own summary and Go — never bundle fixes into one silent batch.
3. For cost and usage questions, report `tool:get_usage` as read: numbers with their period, no fixes implied.

## Expected output
Findings plus, per accepted fix, that fix workflow's own truthful report. Nothing is reported fixed because a fix was merely proposed.

## Stop and handoff
A finding with no matching workflow is reported as a finding with a suggested manual path (Settings forms), not acted on. Never re-run fixes that are already proven working — re-read the durable configuration first.
