# Fixtures — codemod-third-party-markdown-align

Pre-apply snapshot of the real markdown table-cell renderer `codemod-third-party-markdown-align.mjs`
targets, taken **before** commit `8c58a0e7e` (`refactor(design-system): apply
codemod-third-party-markdown-align (Stage B exact-source repair)`) applied the codemod to the live
tree. The file is the exact output of `git show 8c58a0e7e~1:src/components/chat/markdown-shared.tsx`
and is committed verbatim (not hand-edited).

This exists because `codemod-third-party-markdown-align.test.mjs`'s mutation-proof test used to read
the live file under `src/` directly. Once the codemod was applied and committed, the pre-apply
`style={style}` passthrough that test asserted on no longer existed in `src/`, so the test failed
forever after — and would break again on any unrelated later edit to that file. Pinning to a
fixture makes the test deterministic and independent of the live tree's current state.

The `.fixture` extension (not `.tsx`) keeps this out of the app source scanners (spacing.mjs,
typography.mjs, ts-colors.mjs scan `src/` and `packages/ui/src/` only), `tsc`, and `eslint`.

| Fixture file | Source commit | Source path |
|---|---|---|
| `markdown-shared.tsx.fixture` | `8c58a0e7e~1` | `src/components/chat/markdown-shared.tsx` |
