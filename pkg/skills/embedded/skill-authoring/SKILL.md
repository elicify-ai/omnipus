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
4. Include the write in Ava's combined proposal and confirm it once with the actual user — directly through `tool:AskUserQuestion`, or through `tool:message_parent` when delegated so the parent relays the user's answer; the parent's own approval is not the user's confirmation.
5. For an edit, submit the reviewed revision. Call `tool:create_skill` or `tool:edit_skill`, then read back and validate the result.
## Expected output
A valid, focused skill whose named tools exist in the catalog.
## Stop and handoff
Do not copy restricted content, embed secrets, or claim Markdown loading proves the workflow. A denied write stays incomplete. On a conflict, stop remaining writes, reread, and reconfirm a materially changed proposal.
