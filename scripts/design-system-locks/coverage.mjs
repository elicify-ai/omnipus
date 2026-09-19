#!/usr/bin/env node
// Stage B coverage lock — the blocking public coverage chain of E1/D8:
// public export → catalog → manifest → story → executed check.
//
// Composes the frozen Stage A validators read-only (design-system
// enforcement contract: coverageApi). This module adds no detection logic of
// its own; it owns the composition, the fail-closed boundary, and the one
// documented tolerance: application surface checks that are explicitly
// `planned` await their C batches (migration plan B/C) and do not block yet.
// Everything else blocks, including every public gap.
//
// The Stage A verification validator resolves manifest-declared paths
// against the process working directory. Rather than mutating this
// process-global around the awaited validator calls (unsavable: a
// save/restore across an await interleaves with any concurrent caller), the
// composition runs in an isolated child process whose working directory IS
// the requested root. The host process never changes its own working
// directory, so concurrent calls for different roots cannot contaminate
// each other or any unrelated path resolution in the caller. An invalid
// root is rejected before the child starts, through the documented
// { errors } result shape instead of a thrown chdir error.
// (Review findings: async-process-cwd-race, invalid-root-breaks-return-contract.)

import { spawn } from 'node:child_process'
import { existsSync, statSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { validateCatalog } from '../design-system/catalog.mjs'
import { verifyCoverage } from '../design-system/verification.mjs'

// catalog.mjs reports each surface verification entry still marked `planned`
// with this exact prefix. The tolerance is intentionally coupled to it: if
// Stage A ever rewords the message, planned gaps resurface as blocking
// errors here and the integration fails loudly instead of silently widening
// the tolerance.
const PLANNED_GAP_PREFIX = 'planned verification gap:'

// Internal host→child protocol. The child is this same module started with
// the marker flag and a JSON payload ({ root, indexPath, evidencePaths },
// all absolute) in the environment variable; it runs with its working
// directory already set to the requested root, composes both validators,
// and prints exactly one { errors } JSON document. A zero exit means the
// protocol completed — error transport is the payload, not the exit code,
// which belongs to the public CLI in the host process.
const CHILD_MARKER = '--compose-child'
const CHILD_PAYLOAD_VARIABLE = 'DESIGN_SYSTEM_COVERAGE_COMPOSE'

const describeError = (error) => (error instanceof Error ? error.message : String(error))

function composeInChild(projectRoot, indexPath, evidencePaths) {
  return new Promise((settle) => {
    const child = spawn(process.execPath, [fileURLToPath(import.meta.url), CHILD_MARKER], {
      cwd: projectRoot,
      env: { ...process.env, [CHILD_PAYLOAD_VARIABLE]: JSON.stringify({ root: projectRoot, indexPath, evidencePaths }) },
      stdio: ['ignore', 'pipe', 'pipe'],
    })
    let stdout = ''
    let stderr = ''
    child.stdout.setEncoding('utf8')
    child.stdout.on('data', (chunk) => { stdout += chunk })
    child.stderr.setEncoding('utf8')
    child.stderr.on('data', (chunk) => { stderr += chunk })
    child.on('error', (error) => {
      settle({ errors: [`coverage: isolated composition failed to start: ${describeError(error)}`] })
    })
    child.on('close', (code, signal) => {
      if (code === 0 && signal === null) {
        try {
          const report = JSON.parse(stdout)
          if (report && Array.isArray(report.errors) && report.errors.every((error) => typeof error === 'string')) {
            settle({ errors: report.errors })
            return
          }
        } catch {
          // Fall through to the fail-closed result below: a crashed or
          // half-written child must never surface as an empty success.
        }
      }
      const detail = [stderr.trim(), `exit=${code ?? 'none'}${signal ? ` signal=${signal}` : ''}`].filter(Boolean).join(' ')
      settle({ errors: [`coverage: isolated composition failed: ${detail || 'no result document was produced'}`] })
    })
  })
}

export async function checkCoverage({ root, indexPath, evidencePaths } = {}) {
  const projectRoot = resolve(root ?? process.cwd())
  if (!existsSync(projectRoot)) {
    return { errors: [`coverage: invalid root ${projectRoot}: directory does not exist`] }
  }
  if (!statSync(projectRoot).isDirectory()) {
    return { errors: [`coverage: invalid root ${projectRoot}: not a directory`] }
  }
  // Artifact paths are resolved against the requested root in this process,
  // so the child receives absolute paths and never depends on its caller's
  // (or its own) working directory for them.
  return composeInChild(
    projectRoot,
    indexPath ? resolve(projectRoot, indexPath) : null,
    (evidencePaths ?? []).map((path) => resolve(projectRoot, path)),
  )
}

// Child entry: the working directory is already the requested root, so the
// composed validators resolve every manifest-declared relative path against
// it exactly as a direct Stage A invocation from that root would.
async function composeAsChild() {
  const payload = JSON.parse(process.env[CHILD_PAYLOAD_VARIABLE])
  const root = resolve(payload.root ?? process.cwd())
  const errors = []
  try {
    errors.push(...validateCatalog(root)
      .filter((error) => !error.startsWith(PLANNED_GAP_PREFIX)))
  } catch (error) {
    errors.push(`coverage: catalog validator failed: ${describeError(error)}`)
  }
  try {
    const report = await verifyCoverage({
      manifestDir: resolve(root, 'design-system/manifests'),
      storybookIndex: payload.indexPath ? resolve(payload.indexPath) : null,
      evidenceFiles: (payload.evidencePaths ?? []).map((path) => resolve(path)),
    })
    errors.push(...report.errors)
  } catch (error) {
    errors.push(`coverage: verification validator failed: ${describeError(error)}`)
  }
  process.stdout.write(`${JSON.stringify({ errors }, null, 2)}\n`)
}

function parseArgs(argv) {
  const result = {}
  for (let index = 0; index < argv.length; index += 2) {
    const key = argv[index]; const value = argv[index + 1]
    if (!key?.startsWith('--') || value === undefined) throw new Error(`invalid argument: ${key ?? ''}`)
    result[key.slice(2)] = value
  }
  return result
}

async function main() {
  if (process.argv.includes(CHILD_MARKER)) {
    await composeAsChild()
    return
  }
  const args = parseArgs(process.argv.slice(2))
  const { errors } = await checkCoverage({
    root: args.root,
    indexPath: args.index,
    evidencePaths: (args.evidence ?? '').split(',').filter(Boolean),
  })
  process.stdout.write(`${JSON.stringify({ errors }, null, 2)}\n`)
  if (errors.length) process.exitCode = 1
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) {
  main().catch((error) => { process.stderr.write(`${describeError(error)}\n`); process.exitCode = 1 })
}
