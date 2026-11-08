/**
 * #986 — the SPA build must stop dirtying the tree with the provenance file.
 *
 * `vite.config.ts`'s designSystemProductionProvenance plugin writes
 * docs/internal/design/evidence/design-system-production-provenance.json on
 * every `dist/spa` build with absolute Rollup module ids. The file is git-
 * TRACKED today, so every build dirties the working tree.
 *
 * Contract §3 #986 narrowest test: `git ls-files --error-unmatch <path>`
 * must FAIL (untracked) and `git check-ignore -q <path>` must SUCCEED
 * (ignored). The fix is `git rm --cached <path>` + the same path in
 * `.gitignore`; the path itself must not move (four consumers read it as is).
 *
 * RED on 69aa07d9a: the file is tracked and not ignored.
 *
 * Deliberately NOT asserted here (stated in the report): that a full
 * `npm run build` leaves `git status --porcelain` clean — that needs a full
 * production build and stays a CI check (§3: "After `npm run build`, run
 * twice, `git status --porcelain` lists no tracked file changes").
 */
import test from 'node:test'
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { resolve } from 'node:path'

const root = resolve(import.meta.dirname, '../..')
const PROVENANCE = 'docs/internal/design/evidence/design-system-production-provenance.json'

function gitOk(args) {
  try {
    execFileSync('git', args, { cwd: root, stdio: 'pipe' })
    return true
  } catch {
    return false
  }
}

test('#986: the design-system production provenance evidence file is NOT git-tracked', () => {
  const tracked = gitOk(['ls-files', '--error-unmatch', PROVENANCE])
  assert.equal(
    tracked,
    false,
    `#986: ${PROVENANCE} is still git-tracked — every dist/spa build dirties the tree. ` +
      'Fix per contract §3: git rm --cached the file (path must not move).',
  )
})

test('#986: the design-system production provenance evidence file IS git-ignored', () => {
  const ignored = gitOk(['check-ignore', '-q', PROVENANCE])
  assert.equal(
    ignored,
    true,
    `#986: ${PROVENANCE} is not matched by .gitignore — a post-fix build would ` +
      'immediately re-dirty the tree as untracked. Add the exact path to .gitignore.',
  )
})

test('#986: the provenance path is unchanged — the four consumers still read the same path', () => {
  // The contract forbids moving the file: package.json's audit:design-system-bundle,
  // .github/workflows/pr.yml (bundle audit, artifact upload, evidence copy) and
  // the mail editor bundle check all reference this exact path. Guard against a
  // "fix" that relocates it instead of untracking it.
  const pkg = JSON.parse(execFileSync('git', ['show', 'HEAD:package.json'], { cwd: root, maxBuffer: 4 * 1024 * 1024, encoding: 'utf8' }))
  assert.ok(
    String(pkg.scripts['audit:design-system-bundle']).includes(PROVENANCE),
    'audit:design-system-bundle must keep reading the provenance file at its existing path',
  )
})
