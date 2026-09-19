import test from 'node:test'
import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { SCANNER_FILES, COVERAGE_FILE } from '../../scripts/design-system-locks/audit.mjs'

const testDir = dirname(fileURLToPath(import.meta.url))
const repoRoot = resolve(testDir, '../..')
const scriptsDir = resolve(repoRoot, 'scripts/design-system-locks')
const contractPath = resolve(repoRoot, 'design-system/enforcement/contract.json')
const packageJsonPath = resolve(repoRoot, 'package.json')

function findAllTestFiles(dir) {
  const results = []

  function walk(current) {
    const entries = readdirSync(current, { withFileTypes: true })

    for (const entry of entries) {
      const fullPath = resolve(current, entry.name)
      const relativePath = fullPath.slice(dir.length + 1) // +1 to skip leading /

      if (entry.isDirectory()) {
        walk(fullPath)
      } else if (entry.name.endsWith('.test.mjs')) {
        results.push(relativePath)
      }
    }
  }

  walk(dir)
  return results
}

test('Test A: No nested test files in tests/design-system-locks/', () => {
  const allTestFiles = findAllTestFiles(testDir)
  const nestedFiles = allTestFiles.filter((file) => file.includes('/'))

  assert.equal(
    nestedFiles.length,
    0,
    `Found nested test files (not directly in tests/design-system-locks/): ${nestedFiles.join(', ')}`
  )
})

test('Test A: package.json test:design-system:locks uses flat glob', () => {
  const packageJsonContent = readFileSync(packageJsonPath, 'utf8')
  const packageJson = JSON.parse(packageJsonContent)
  const testScript = packageJson.scripts['test:design-system:locks']

  assert.strictEqual(
    testScript,
    'node --test tests/design-system-locks/*.test.mjs',
    'test:design-system:locks script does not match expected flat glob pattern'
  )
})

test('Test B: scripts/design-system-locks/ contains expected scanner files', () => {
  const files = readdirSync(scriptsDir)
  const msjsFiles = files.filter((f) => f.endsWith('.mjs')).sort()

  const expectedFiles = [
    ...SCANNER_FILES,
    COVERAGE_FILE,
    'audit.mjs',
    'policy.mjs',
  ].sort()

  // Check for missing files
  for (const expected of expectedFiles) {
    assert(
      msjsFiles.includes(expected),
      `Expected file "${expected}" not found in scripts/design-system-locks/`
    )
  }

  // Check for unexpected files
  for (const actual of msjsFiles) {
    assert(
      expectedFiles.includes(actual),
      `Unexpected file "${actual}" found in scripts/design-system-locks/`
    )
  }

  assert.deepStrictEqual(
    msjsFiles,
    expectedFiles,
    'Mismatch between actual and expected .mjs files in scripts/design-system-locks/'
  )
})

test('Test B: contract.json ownership map matches scripts/design-system-locks/ files', () => {
  const contractContent = readFileSync(contractPath, 'utf8')
  const contract = JSON.parse(contractContent)
  const ownership = contract.ownership || {}

  const files = readdirSync(scriptsDir)
  const msjsFiles = new Set(files.filter((f) => f.endsWith('.mjs')))

  const ownershipKeys = Object.keys(ownership)

  // Check that every ownership key exists as a file
  for (const key of ownershipKeys) {
    assert(
      msjsFiles.has(key),
      `Ownership map references "${key}" but it does not exist in scripts/design-system-locks/`
    )
  }

  // Check that every file has an ownership entry
  for (const file of msjsFiles) {
    assert(
      ownershipKeys.includes(file),
      `File "${file}" exists but is not in contract.json ownership map`
    )
  }

  assert.deepStrictEqual(
    new Set(ownershipKeys),
    msjsFiles,
    'Mismatch between contract.json ownership keys and scripts/design-system-locks/ .mjs files'
  )
})
