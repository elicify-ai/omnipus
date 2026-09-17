---
name: skill-authoring
description: Author or refine a reusable Omnipus skill through Ava's proposal-first workflow.
---
# Skill Authoring
## Prerequisites
Know the repeatable task, intended triggers, scope, prerequisites, and actual tool names.
## Steps
1. Inspect packages with list_skills and find_skills; use Skill when an existing body must be read.
2. Draft valid frontmatter plus trigger/scope, prerequisites, numbered procedure, expected output, and failure behavior.
3. Mark tool references as invocations or prohibitions. Assignment does not grant permission.
4. Include the write in Ava's combined proposal and confirm once with AskUserQuestion.
5. Call create_skill or edit_skill, then load and validate the result.
## Expected output
A valid, focused skill whose invocation names exist in the catalog.
## Stop and handoff
Do not copy restricted content, embed secrets, or claim Markdown loading proves the workflow. A denied write stays incomplete.
