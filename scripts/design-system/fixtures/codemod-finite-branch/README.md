# Fixtures — codemod-finite-branch

Pre-apply snapshots of the five real P8 sites `codemod-finite-branch.mjs` targets, taken
**before** commit `0650b3c23` (`refactor(design-system): apply codemod-finite-branch (Stage B
exact-source repair)`) applied the codemod to the live tree. Each file is the exact output of
`git show 0650b3c23~1:<path>` and is committed verbatim (not hand-edited).

These exist because `codemod-finite-branch.test.mjs`'s mutation-proof and end-to-end tests used to
read the live files under `src/` directly. Once the codemod was applied and committed, the
pre-apply pattern those tests asserted on no longer existed in `src/`, so the tests failed forever
after — and would break again on any unrelated later edit to those files. Pinning to a fixture
makes the tests deterministic and independent of the live tree's current state.

The `.fixture` extension (not `.tsx`) keeps these out of the app source scanners (spacing.mjs,
typography.mjs, ts-colors.mjs scan `src/` and `packages/ui/src/` only), `tsc`, and `eslint`.

| Fixture file | Source commit | Source path |
|---|---|---|
| `GoalIndicator.tsx.fixture` | `0650b3c23~1` | `src/components/chat/GoalIndicator.tsx` |
| `GoalPillTray.tsx.fixture` | `0650b3c23~1` | `src/components/chat/GoalPillTray.tsx` |
| `FileWriteConfirm.tsx.fixture` | `0650b3c23~1` | `src/components/chat/tools/FileWriteConfirm.tsx` |
| `TablePart.tsx.fixture` | `0650b3c23~1` | `src/components/library/preview/viewparts/TablePart.tsx` |
| `BrowserLiveToolbar.tsx.fixture` | `0650b3c23~1` | `src/components/browser/BrowserLiveView.tsx` (content now applies to `src/components/browser/BrowserLiveToolbar.tsx`; filename matches the current `SITES` registry entry in `codemod-finite-branch.mjs`, which the test harness derives the fixture name from) |
