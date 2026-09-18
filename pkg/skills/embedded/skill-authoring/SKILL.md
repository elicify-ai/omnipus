---
name: skill-authoring
description: Author or refine a reusable Omnipus skill through Ava's proposal-first workflow.
---
# Skill Authoring
## Prerequisites
Know the repeatable task, intended triggers, scope, prerequisites, and actual tool names.
## Steps
1. Inspect packages with `tool:list_skills` management scope and `tool:find_skills`; request the named sanitized content when an existing body must be reviewed.
2. Draft valid frontmatter plus trigger/scope, prerequisites, numbered procedure, expected output, and failure behavior.
3. Write portable content a reader can follow in any harness: name tools by their exact catalog names, and word permissions and prohibitions plainly — say the role may call a tool, must confirm before using it, or does not hold it at all. Assignment does not grant permission.
4. Include the write in Ava's combined proposal. Present that proposal in the conversation and wait for the actual user's apply, change, or cancel reply. When delegated, send it through `tool:message_parent` and wait for the relayed user answer; the parent's own approval is not the user's confirmation. Use `tool:AskUserQuestion` only for a real unknown that changes the proposal, never as permission to apply.
5. For an edit, submit the reviewed revision. Call `tool:create_skill` or `tool:edit_skill`, then read back and validate the result.
## Expected output
A valid, focused skill whose named tools exist in the catalog.
## Stop and handoff
Do not copy restricted content, embed secrets, or claim Markdown loading proves the workflow. A denied write stays incomplete. On a conflict, stop remaining writes, reread, and reconfirm a materially changed proposal.
