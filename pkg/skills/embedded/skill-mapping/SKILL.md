---
name: skill-mapping
description: Assign, revoke, install, or remove skills while distinguishing agent assignment from shared package deletion.
---
# Skill Mapping
## Prerequisites
Use list_skills/find_skills and inspect origin and shared use.
## Steps
1. Read the target's current assignments and package origin.
2. State whether the proposal changes an assignment or deletes a shared package.
3. Confirm once; use install_skill, remove_skill, or the agent update operation as proposed.
4. Read back assignments and verify revoked execution is blocked.
## Expected output
The exact assignment change without unintended shared deletion.
## Stop and handoff
Hidden role assignments are fixed. Report missing or denied package operations.
