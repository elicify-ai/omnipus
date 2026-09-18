---
name: skill-mapping
description: Assign, revoke, install, or remove skills while distinguishing agent assignment from shared package deletion.
---
# Skill Mapping
## Prerequisites
Use `tool:list_skills` management scope/`tool:find_skills` and inspect origin, revision, and shared use.
## Steps
1. Read the target's current assignments and package origin.
2. State whether the proposal changes an assignment or deletes a shared package.
3. Present one combined proposal in the conversation and wait for the actual user's apply, change, or cancel reply. When delegated, send it through `tool:message_parent` and wait for the relayed user answer; the parent's own approval is not the user's confirmation. Use `tool:AskUserQuestion` only for a real unknown, never as permission to apply.
4. After confirmation, submit the reviewed revision for replacement, removal, or assignment updates, then use `tool:install_skill`, `tool:remove_skill`, or `tool:update_agent` as proposed. Send only advertised fields.
5. Read back assignments and verify revoked execution is blocked.
## Expected output
The exact assignment change without unintended shared deletion.
## Stop and handoff
Hidden role assignments are fixed. Report missing or denied package operations. On a conflict, stop remaining writes, reread, and reconfirm a materially changed proposal.
