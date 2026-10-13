---
name: install-skill
description: Use when the user wants a skill installed from the catalogue (W6).
metadata:
  display_name: Install Skill (W6)
---
# Install a skill from the catalogue (W6)

## Prerequisites
`tool:find_skills` for the catalogue entry; `dependency-check` for anything the skill declares it needs.

## Steps
1. Find candidates with `tool:find_skills` and let the user choose.
2. Load `dependency-check` on the chosen skill's declared needs.
3. Present one card: source, what the skill may do, who gets it and (from the dependency check) what will be installed where, with the exact command from the skill's guidance.
4. One Go / Change / Cancel question. Never call a change tool before Go.
5. After Go: `tool:install_skill` (a malicious-flagged install is refused — report it, never override), assign it to the named agents, then run the declared installs through `tool:environment_setup` to the target workspace.

## Expected output
Activation check — working / saved but not working: reason — plus the install outcomes ("installed; readiness confirmed at the agent's first use" unless proven in the consuming agent's own sandbox).

## Stop and handoff
Cancel installs nothing. A refused or failed install is reported with its reason; the skill assignment is not claimed as done unless the activation check passed.
