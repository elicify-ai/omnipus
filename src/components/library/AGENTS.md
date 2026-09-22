# Library (UI)

The workspace file-explorer surface over the workspace `work/` tree
(`LibraryExplorer.tsx` and the dialogs/rows around it). Backend is
`pkg/library` (path-safe text file access) — NOT `pkg/media/library`
(UUID-keyed binary media storage, ADR-051): different packages solving
different problems (module-map decision D1); do not conflate them.

## Design system

Read `.claude/skills/omnipus-design-system/SKILL.md` before adding or changing any
control, color, spacing, or type value here — it states the CI-enforced rules and cites
the script or test for each.

## Preview is framed inside the Library panel

- `LibraryExplorer.tsx` mounts `LibraryPreviewPane.tsx` into its
  "PREVIEW/EDIT PANE PLACEHOLDER" slot; there is no standalone preview tab or
  route. Do not add one.
- Render-first (ADR-067 D15): the pane shows the ARTIFACT, not its source;
  source appears only after pressing Edit.
- `/preview/` on the gateway is a different thing entirely: agent `web_serve`
  previews (ADR-044). Never point library preview UI at it.

## Knowledge base UI is nested here

`knowledge/` and `preview/knowledgeMarkdown.tsx` — see
`src/components/library/knowledge/CLAUDE.md`. A founder decision (module-map
D2) may promote it to `src/components/knowledge/` to match the backend
`pkg/knowledge` name; until it is ratified, do not move or rename it.

## Tests

CI group `components-misc` (pattern includes `src/components/library/`, which
also covers `knowledge/`). Local: `npx vitest run src/components/library/`.
A test file matching no group pattern runs in NO CI job while CI stays green;
`scripts/check-vitest-coverage.mjs` is the tripwire.
