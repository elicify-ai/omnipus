---
name: Plan
description: Break a complex or multi-step goal into an ordered, trackable plan before acting. Use when a request has several steps, dependencies, or unknowns, or when the user asks you to plan, scope, or sequence work.
context: global
---

# Plan

Turn a fuzzy or multi-step goal into a concrete, ordered plan before doing the work.
This is the Orchestrator's default reflex: think first, sequence, then execute.

## When to use (trigger phrases)

- "plan ...", "how should we approach ..."
- "break this down", "scope this out", "sequence the work"
- Any request with multiple steps, dependencies, or unknowns.

## Method

1. **Clarify the goal.** Restate the desired end state in one sentence. If it is
   ambiguous, ask at most 2–3 sharp questions before planning.
2. **Scope the workspace.** If the work is non-trivial or long-running, create a
   dedicated workspace for it (`system.workspace.create`) so artifacts, sessions,
   and tasks stay scoped. Reuse an existing workspace when one fits.
3. **Decompose.** List the concrete steps. Keep each step a single, verifiable unit
   of work.
4. **Order by dependency.** Put steps that unblock others first. Flag anything that
   can run in parallel.
5. **Identify risks & unknowns.** Note what could fail, what needs a decision, and
   what information is missing.
6. **Assign / delegate.** For each step, decide who does it — yourself, a tool, or a
   delegated agent (`spawn` / `subagent` / `handoff`).
7. **Track.** Materialise the plan as tasks (`task_create`) so progress is visible
   and nothing is dropped. Link dependencies with `blocked_by` to form a DAG the
   task system can sequence — a task with an open `blocked_by` will not be picked
   up until its predecessor completes.

## Output shape

Present the plan as a numbered list. For each item give: the action, who/what
performs it, and its dependency (if any). End with the single next action to take.

## Anti-patterns

- Do not start executing before the plan is stated.
- Do not produce a plan with steps you cannot verify are done.
- Do not bury the critical path under low-value busywork.
