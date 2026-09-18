---
name: plan
metadata:
  display_name: Plan
description: Decompose a multi-step goal into a dependency-aware task DAG with explicit files, criteria, and evidence.
---
# Plan
## Prerequisites
A brief, goal, criteria, Definition of Done, and relevant references exist. Load define-goal when they need authoring.
## Steps
branch:plansupervisor Skip steps 1–8 and go directly to Plan correction; return one `tool:plan_correct` call instead of authoring or executing a new plan.
1. branch:jim Restate the outcome and resolve a real unknown.
   branch:planner Restate the outcome and resolve a real unknown.
   branch:jim Ask conversationally, using `tool:AskUserQuestion` only when two readings would produce different tasks, never as permission to start the plan.
   branch:planner Ask Jim through `tool:message_parent`.
2. branch:jim Divide work into single-purpose tasks and identify dependency edges.
   branch:planner Divide work into single-purpose tasks and identify dependency edges.
3. branch:jim Declare each task's files; serialize or repartition overlapping writers.
   branch:planner Declare each task's files; serialize or repartition overlapping writers.
4. branch:jim Give every task observable criteria and the plan a Definition of Done.
   branch:planner Give every task observable criteria and the plan a Definition of Done.
5. branch:jim Select Researcher for evidence and General Purpose for execution.
   branch:planner Select Researcher for evidence and General Purpose for execution.
6. branch:jim Jim calls `tool:create_plan` then `tool:execute_plan`.
   branch:planner Planner returns the DAG to Jim and does not execute it.
7. branch:jim Jim does not correct a parked plan; leave scoring and correction to the engine.
8. branch:planner Planner does not correct a parked plan; return the DAG to Jim.
## Plan correction
branch:plansupervisor Plan Supervisor diagnoses an unmet or stalled plan and calls `tool:plan_correct` once with the supported correction.

branch:plansupervisor When a plan is parked in `awaiting_supervision`, choose one correction through `tool:plan_correct`:

| Situation | Correction |
|---|---|
| A completed member's outcome is wrong | **SUPERSEDE** — REQUIRES replacement work carrying every acceptance criterion of the superseded member. |
| A failed member should run again | **TARGETED-RETRY** that member. |
| Required work is missing | **APPEND** new tail members and dependencies. |
| The Definition of Done is unreachable | **ABANDON** with the falsified assumption. |
## Expected output
Jim and Planner: a valid DAG with explicit dependencies, owners, files, criteria, and rationale.
branch:plansupervisor Return exactly one `tool:plan_correct` call with the correction and supporting diagnosis.
## Stop and handoff
Do not teach or assume worktree isolation. Stop when dependencies or file ownership cannot be made safe.
