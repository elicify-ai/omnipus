// Contract: the checkpoint audit.mjs enforces (design-system/enforcement/
// contract.json's currentCheckpoint) is read from exactly one file, never
// hardcoded as a literal in package.json, CI YAML, or any other caller.
// Advancing the enforced checkpoint is therefore a single reviewed edit to
// that field -- not a value that can silently stay pinned to an old
// checkpoint forever because nobody remembered to bump a shell string.
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { CHECKPOINTS } from './audit.mjs'

export function readCurrentCheckpoint(root = fileURLToPath(new URL('../..', import.meta.url))) {
  const contractPath = resolve(root, 'design-system/enforcement/contract.json')
  let contract
  try {
    contract = JSON.parse(readFileSync(contractPath, 'utf8'))
  } catch (error) {
    throw new Error(`cannot read checkpoint source of truth ${contractPath}: ${error.message}`)
  }
  const checkpoint = contract?.currentCheckpoint
  if (typeof checkpoint !== 'string' || !CHECKPOINTS.includes(checkpoint)) {
    throw new Error(
      `${contractPath} currentCheckpoint must be one of ${CHECKPOINTS.join(', ')}, got ${JSON.stringify(checkpoint)}`,
    )
  }
  return checkpoint
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    process.stdout.write(readCurrentCheckpoint())
  } catch (error) {
    process.stderr.write(`${error.message}\n`)
    process.exit(1)
  }
}
