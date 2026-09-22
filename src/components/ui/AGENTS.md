# Design System (UI)

The catalogued primitives (`Button`, `Dialog`, `SegmentedControl`, `Switch`, …) and the
machinery that enforces them: `design-system/catalog.json`, `design-system/manifests/`,
`src/styles/library.css`, `src/index.ts`. **Read
`.claude/skills/omnipus-design-system/SKILL.md` before changing anything in this folder**
— it states every rule with the script or test that enforces it. The two that bite
hardest:

## No hand-built controls

Never a raw `<button>`, `<dialog>`, `document.createElement`, or a
`role="button"|"radio"|"switch"|"tab"` on a plain tag —
`scripts/design-system-locks/controls.mjs` fails the build on all of them. Use or extend
the catalogued component instead.

## Publishing a component is a four-part contract

An export from `src/index.ts` is not enough. A published component also needs a
`design-system/catalog.json` entry, an `@source` line in `src/styles/library.css`
(Tailwind's automatic class discovery is off — a missing `@source` line ships with no
styles applied, silently), and a `design-system/manifests/<name>.json` covering all
eleven verification kinds. See the skill for the exact tests that cross-check all four.

## Tests

CI group `components-agents-settings` (pattern includes `src/components/ui/`, alongside
`agents/`, `settings/`, `skills/`, `shared/`). Local:
`npx vitest run src/components/ui/`.
