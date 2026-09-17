---
name: plan
description: Decompose a multi-step goal into a dependency-aware task DAG with explicit files, criteria, and evidence.
---
# Plan
## Prerequisites
A brief, goal, criteria, Definition of Done, and relevant references exist. Load define-goal when they need authoring.
## Steps
1. Restate the outcome and resolve ambiguity through AskUserQuestion for Jim or message_parent for Planner.
2. Divide work into single-purpose tasks and identify dependency edges.
3. Declare each task's files; serialize or repartition overlapping writers.
4. Give every task observable criteria and the plan a Definition of Done.
5. Select Researcher for evidence and General Purpose for execution.
6. Jim calls create_plan then execute_plan. Planner returns the DAG to Jim and does not execute it.
7. Plan Supervisor diagnoses an unmet plan and calls plan_correct once with the supported correction.

When a plan is parked in `awaiting_supervision`, choose one correction:

| Situation | Correction |
|---|---|
| A completed member's outcome is wrong | **SUPERSEDE** — REQUIRES replacement work carrying every acceptance criterion of the superseded member. |
| A failed member should run again | **TARGETED-RETRY** that member. |
| Required work is missing | **APPEND** new tail members and dependencies. |
| The Definition of Done is unreachable | **ABANDON** with the falsified assumption. |
## Expected output
A valid DAG with explicit dependencies, owners, files, criteria, and rationale.
## Stop and handoff
Do not teach or assume worktree isolation. Stop when dependencies or file ownership cannot be made safe.
