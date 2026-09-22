// Adversarial reviewed-boundary authorization tests.
//
// The shared enforcement contract permits centrally enumerated tests, stories,
// and canonical generated token definitions. A ledger kind is evidence about
// the named path; it must not turn an application file into an exemption.

import assert from 'node:assert/strict'
import { mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import test, { after } from 'node:test'

import { audit } from '../../scripts/design-system-locks/audit.mjs'

const SCRATCH_PARENT = resolve('dist/design-system-baseline/cli-lanes/audit-boundaries-adversarial')
const roots = []

after(() => {
  for (const root of roots) rmSync(root, { recursive: true, force: true })
})

function fixture(name) {
  const root = resolve(SCRATCH_PARENT, name)
  rmSync(root, { recursive: true, force: true })
  mkdirSync(resolve(root, 'src'), { recursive: true })
  roots.push(root)
  return root
}

function write(root, path) {
  const absolute = resolve(root, path)
  mkdirSync(dirname(absolute), { recursive: true })
  writeFileSync(absolute, 'export const governed = true\n')
}

function boundary(path, kind) {
  return { path, kind, reason: `reviewed ${kind} fixture` }
}

function scanner() {
  return {
    label: 'reviewed-boundary-fixture',
    extensions: ['.css', '.ts', '.tsx'],
    scan({ path }) {
      return [{
        ruleId: 'design-system/raw-colour',
        path,
        syntax: '#FF0000',
        message: 'fixture governed finding',
      }]
    },
  }
}

async function run(name, paths, reviewedBoundaries) {
  const root = fixture(name)
  for (const path of paths) write(root, path)
  return audit({
    root,
    checkpoint: 'B',
    baseline: { version: 1, fingerprints: [] },
    ledger: { version: 1, entries: [], exceptions: [], reviewedBoundaries },
    policy: { tokenCssNames: [], resolvedTokens: {} },
    scanners: [scanner()],
  })
}

test('exact test, story, and canonical generated-token boundaries are accepted', async () => {
  const paths = [
    'src/components/Button.test.tsx',
    'src/components/Button.stories.tsx',
    'src/styles/tokens.generated.css',
    'src/design-system/tokens.ts',
  ]
  const report = await run('legitimate-boundaries', paths, [
    boundary(paths[0], 'test'),
    boundary(paths[1], 'story'),
    boundary(paths[2], 'generated-tokens'),
    boundary(paths[3], 'generated-tokens'),
  ])

  assert.equal(report.ok, true, JSON.stringify(report.errors))
  assert.deepEqual(report.errors, [])
  assert.deepEqual(report.appliedBoundaries.map(({ path }) => path).sort(), [...paths].sort())
})

test('application files disguised as reviewed boundary kinds are rejected and remain governed', async () => {
  const paths = [
    'src/components/App.tsx',
    'src/components/Settings.tsx',
    'src/components/Dashboard.tsx',
  ]
  const report = await run('disguised-application-files', paths, [
    boundary(paths[0], 'test'),
    boundary(paths[1], 'story'),
    boundary(paths[2], 'generated-tokens'),
  ])

  assert.equal(report.ok, false)
  for (const path of paths) {
    assert.equal(
      report.errors.some((error) => error.code !== 'new-debt' && error.path === path),
      true,
      `${path} needs a blocking reviewed-boundary validation error`,
    )
  }
  assert.deepEqual(
    report.errors.filter(({ code }) => code === 'new-debt').map(({ path }) => path).sort(),
    [...paths].sort(),
  )
  assert.deepEqual(report.appliedBoundaries, [])
})

test('a legitimate test path cannot be registered under the story kind', async () => {
  const path = 'src/components/Button.test.tsx'
  const report = await run('mismatched-kind', [path], [boundary(path, 'story')])

  assert.equal(report.ok, false)
  assert.equal(report.errors.some((error) => error.code !== 'new-debt' && error.path === path), true)
  assert.deepEqual(report.errors.filter(({ code }) => code === 'new-debt').map(({ path: errorPath }) => errorPath), [path])
  assert.deepEqual(report.appliedBoundaries, [])
})

test('generated-looking application paths are not canonical generated-token boundaries', async () => {
  const path = 'src/components/tokens.generated.ts'
  const report = await run('generated-lookalike', [path], [boundary(path, 'generated-tokens')])

  assert.equal(report.ok, false)
  assert.equal(report.errors.some((error) => error.code !== 'new-debt' && error.path === path), true)
  assert.deepEqual(report.errors.filter(({ code }) => code === 'new-debt').map(({ path: errorPath }) => errorPath), [path])
  assert.deepEqual(report.appliedBoundaries, [])
})
