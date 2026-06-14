---
name: skill-authoring
description: Author, refine, and version reusable skills. Use when the user asks to create a new skill, improve an existing one, or capture a repeatable procedure as procedural memory.
context: global
---

# Skill Authoring

Create and refine skills — the procedural memory of an Omnipus agent. A skill is a
`SKILL.md` file with YAML frontmatter (`name`, `description`) and a markdown body
describing when and how to perform a task.

## When to use (trigger phrases)

- "create a skill for ..."
- "turn this into a reusable skill"
- "improve / refine the X skill"
- "remember how to do this for next time"

## Anatomy of a SKILL.md

```markdown
---
name: my-skill
description: One sentence on what this does and WHEN to trigger it.
---

# My Skill

## When to use (trigger phrases)
- "do the thing"

## Steps
1. First, ...
2. Then, ...
```

Rules the loader enforces (writes that violate these are rejected):

- `name` is required, alphanumeric with hyphens, max 64 characters.
- `description` is required, max 1024 characters — make it trigger-rich.
- The body is markdown; lead with a `# Heading` and a one-paragraph summary.

## Authoring workflow

1. **Draft** the frontmatter — a sharp, trigger-oriented `description` matters most;
   it is what future-you matches against.
2. **Write** the skill with `system.skill.create` (new) or `system.skill.edit`
   (existing). Both are consent-gated — you will be asked to approve the write.
3. **Editing a built-in skill** produces a user override; the shipped built-in is
   never mutated in place, so you can always fall back.
4. **Versioning** — every create/edit snapshots the prior `SKILL.md` under the
   skill's `.versions/` directory, so a bad edit is recoverable.

## Quality checklist

- Does the `description` name concrete trigger phrases?
- Are the steps imperative and ordered?
- Did you avoid embedding secrets or machine-specific paths?
- Is the skill scoped to ONE coherent task (split it otherwise)?
