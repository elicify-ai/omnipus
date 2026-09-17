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
3. Confirm once with the actual user — `tool:AskUserQuestion` directly, or `tool:message_parent` relay when delegated; the parent's own approval is not the user's confirmation. Submit the reviewed revision for replacement, removal, or assignment updates, then use `tool:install_skill`, `tool:remove_skill`, or `tool:update_agent` as proposed.
4. Read back assignments and verify revoked execution is blocked.
## Expected output
The exact assignment change without unintended shared deletion.
## Stop and handoff
Hidden role assignments are fixed. Report missing or denied package operations. On a conflict, stop remaining writes, reread, and reconfirm a materially changed proposal.
