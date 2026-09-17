---
name: skill-authoring
description: Author or refine a reusable Omnipus skill through Ava's proposal-first workflow.
---
# Skill Authoring
## Prerequisites
Know the repeatable task, intended triggers, scope, prerequisites, and actual tool names.
## Steps
1. Inspect packages with list_skills management scope and find_skills; request the named sanitized content when an existing body must be reviewed.
2. Draft valid frontmatter plus trigger/scope, prerequisites, numbered procedure, expected output, and failure behavior.
3. Mark tool references as invocations or prohibitions. Assignment does not grant permission.
4. Include the write in Ava's combined proposal and confirm once with AskUserQuestion.
5. For an edit, submit the reviewed revision. Call create_skill or edit_skill, then read back and validate the result.
## Expected output
A valid, focused skill whose invocation names exist in the catalog.
## Stop and handoff
Do not copy restricted content, embed secrets, or claim Markdown loading proves the workflow. A denied write stays incomplete. On a conflict, stop remaining writes, reread, and reconfirm a materially changed proposal.
