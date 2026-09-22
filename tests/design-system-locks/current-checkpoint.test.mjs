// Contract: the checkpoint audit.mjs enforces comes from exactly one place
// -- design-system/enforcement/contract.json's currentCheckpoint field, read
// by scripts/design-system-locks/current-checkpoint.mjs -- never a literal
// buried in package.json or CI YAML. This suite proves three things: (1)
// the reader is strict (missing/invalid values fail loudly, never silently
// default), (2) advancing the checkpoint is what actually makes a ledger
// entry's expiryCheckpoint start blocking the audit, and (3) neither
// package.json nor pr.yml can silently pin an older checkpoint than the
// source of truth.
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import test, { after } from 'node:test'
import { fileURLToPath } from 'node:url'
import { CHECKPOINTS, audit } from '../../scripts/design-system-locks/audit.mjs'
import { readCurrentCheckpoint } from '../../scripts/design-system-locks/current-checkpoint.mjs'

const repoRoot = resolve(fileURLToPath(new URL('../..', import.meta.url)))
const cliScript = resolve(repoRoot, 'scripts/design-system-locks/current-checkpoint.mjs')
const fixtureRoots = []

after(() => {
  for (const directory of fixtureRoots) rmSync(directory, { recursive: true, force: true })
})

function fixtureRoot() {
  mkdirSync(resolve(repoRoot, 'dist/design-system-baseline/cli-lanes'), { recursive: true }) // a fresh checkout has no dist/
  const directory = mkdtempSync(resolve(repoRoot, 'dist/design-system-baseline/cli-lanes/current-checkpoint-'))
  fixtureRoots.push(directory)
  mkdirSync(resolve(directory, 'src'), { recursive: true })
  mkdirSync(resolve(directory, 'design-system/enforcement'), { recursive: true })
  return directory
}

function writeContract(root, contract) {
  writeFileSync(resolve(root, 'design-system/enforcement/contract.json'), JSON.stringify(contract))
}

function fingerprint(ruleId, path, syntax) {
  return createHash('sha256').update(JSON.stringify([ruleId, path, syntax])).digest('hex')
}

function ledgerAndBaselineAt(expiryCheckpoint) {
  const item = { ruleId: 'spacing/off-scale', path: 'src/example.tsx', syntax: 'p-[7px]' }
  const fp = fingerprint(item.ruleId, item.path, item.syntax)
  const baseline = { version: 1, fingerprints: [{ ...item, fingerprint: fp, count: 1 }] }
  const ledger = {
    version: 1,
    entries: [{
      ...item,
      fingerprint: fp,
      owner: 'test-owner',
      replacement: 'registered spacing token',
      expiryCheckpoint,
    }],
    exceptions: [],
  }
  return { baseline, ledger }
}

test('readCurrentCheckpoint returns the declared value from a valid contract', () => {
  const root = fixtureRoot()
  writeContract(root, { currentCheckpoint: 'C3' })
  assert.equal(readCurrentCheckpoint(root), 'C3')
})

test('readCurrentCheckpoint fails loudly, never silently, when currentCheckpoint is missing', () => {
  const root = fixtureRoot()
  writeContract(root, { mode: 'audit' })
  assert.throws(() => readCurrentCheckpoint(root), /currentCheckpoint must be one of/)
})

test('readCurrentCheckpoint fails loudly on a value outside the ordered checkpoint sequence', () => {
  const root = fixtureRoot()
  writeContract(root, { currentCheckpoint: 'C7' })
  assert.throws(() => readCurrentCheckpoint(root), /currentCheckpoint must be one of/)
})

test('readCurrentCheckpoint fails loudly when contract.json is missing entirely', () => {
  const root = fixtureRoot()
  assert.throws(() => readCurrentCheckpoint(root), /cannot read checkpoint source of truth/)
})

test('the real repository contract.json declares a checkpoint on the ordered sequence', () => {
  const checkpoint = readCurrentCheckpoint(repoRoot)
  assert.ok(CHECKPOINTS.includes(checkpoint), `${checkpoint} must be one of ${CHECKPOINTS.join(', ')}`)
})

test('the CLI entrypoint prints exactly the value package.json will substitute', () => {
  const result = spawnSync('node', [cliScript], { encoding: 'utf8' })
  assert.equal(result.status, 0)
  assert.equal(result.stdout, readCurrentCheckpoint(repoRoot))
})

test('advancing the checkpoint to a ledger entry\'s own expiry makes it expired-ledger', async () => {
  const root = fixtureRoot()
  const { baseline, ledger } = ledgerAndBaselineAt('C5')

  const notYetDue = await audit({
    root,
    baseline,
    ledger,
    checkpoint: 'C4',
    policy: { tokenCssNames: [], resolvedTokens: {} },
    scanners: [],
  })
  assert.ok(
    !notYetDue.errors.some((error) => error.code === 'expired-ledger'),
    'a C5 entry must not be expired while the enforced checkpoint is still C4',
  )

  const due = await audit({
    root,
    baseline,
    ledger,
    checkpoint: 'C5',
    policy: { tokenCssNames: [], resolvedTokens: {} },
    scanners: [],
  })
  assert.ok(
    due.errors.some((error) => error.code === 'expired-ledger'),
    'advancing the enforced checkpoint to C5 must make the C5 entry expire',
  )
})

test('package.json audit:design-system derives --checkpoint from current-checkpoint.mjs, never a hardcoded literal', () => {
  const packageJson = JSON.parse(readFileSync(resolve(repoRoot, 'package.json'), 'utf8'))
  const script = packageJson.scripts['audit:design-system']
  assert.ok(script, 'audit:design-system script must exist')

  // A hardcoded checkpoint token directly after the flag, e.g. `--checkpoint C1`
  // or `--checkpoint B` -- this is exactly the shape of the bug under review
  // (package.json hardcoded --checkpoint C1). Prove the detector actually
  // catches that shape before trusting it finds nothing in the fixed script.
  const hardcodedCheckpointFlag = /--checkpoint\s*\\?"?\s*(A|B|C[1-6])\b/
  assert.match('--checkpoint C1', hardcodedCheckpointFlag, 'sanity check: detector must catch the known-bad shape')
  assert.doesNotMatch(
    script,
    hardcodedCheckpointFlag,
    'audit:design-system must not hardcode a checkpoint literal; CI could then silently pin an old checkpoint forever',
  )
  assert.match(
    script,
    /current-checkpoint\.mjs/,
    'audit:design-system must derive --checkpoint from scripts/design-system-locks/current-checkpoint.mjs',
  )
})

test('.github/workflows/pr.yml does not hardcode a --checkpoint flag that could disagree with the source of truth', () => {
  const workflow = readFileSync(resolve(repoRoot, '.github/workflows/pr.yml'), 'utf8')
  const hardcodedCheckpointFlag = /--checkpoint\s*\\?"?\s*(A|B|C[1-6])\b/
  assert.doesNotMatch(
    workflow,
    hardcodedCheckpointFlag,
    'pr.yml must not pass its own --checkpoint literal; it must run through npm run audit:design-system, which derives the value',
  )
})
