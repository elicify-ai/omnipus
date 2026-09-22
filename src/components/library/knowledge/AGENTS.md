# Knowledge Base (UI)

Knowledge views/reader UI (`KnowledgePanel`, `KnowledgeReader`,
`KnowledgeViewsList`, …). Backend family: `pkg/knowledge` + `pkg/records` +
`pkg/vaultimport` + `pkg/vaultprops` — its own package tree, while this UI is
nested under Library. The names do not match across the tree today; module-map
decision D2 (promote to `src/components/knowledge/`) is unratified — do not
move or rename unilaterally.

## `preview/knowledgeMarkdown.tsx` — leave whole, one job

It is 2,410 lines on purpose: a deliberate THIRD COMPOSITION layered by object
reference over `chat/markdown-shared.tsx` → `LibraryMarkdownPreview.tsx`
(ADR-067 FR-013a). Its header records the standing decision: what is forbidden
is duplicating the PARSER, the PLUGIN STACK, and the ELEMENT RENDERERS (those
were hand-copied once and drifted three times); what was never forbidden is
another COMPOSITION over the shared definitions via spread. Splitting the file
or hand-copying renderers both recreate the exact drift it exists to prevent.

## Tests

CI group `components-misc` (pattern `src/components/library/` covers this
subfolder). Local: `npx vitest run src/components/library/knowledge/`.
A test file matching no group pattern runs in NO CI job while CI stays green;
`scripts/check-vitest-coverage.mjs` is the tripwire.
