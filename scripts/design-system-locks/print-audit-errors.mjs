#!/usr/bin/env node
// Errors-only summarizer for a design-system-locks audit report.
//
// Usage: node scripts/design-system-locks/print-audit-errors.mjs <report.json>
//
// audit.mjs writes a JSON report (`--report <path>`) whose `errors` array holds
// the blocking findings; its `debt`, `appliedExceptions` and `appliedBoundaries`
// arrays are ACCEPTED entries — present but never blocking. A developer who
// just ran the audit locally wants exactly the errors, one compact line each
// (kind + file path + message), plus a count — not the ~700 accepted boundary
// lines. Missing or unparseable report: clear error, exit 2. Errors found:
// exit 1. None: exit 0.
//
// The audit report's error entries are `{ code, message, ...extra }` (see
// audit.mjs addError()); `code` is the error kind ("new-debt",
// "invalid-reviewed-boundary", "scanner-exception", ...). Entries carry a
// repository-relative `path` when one exists; setup errors (e.g.
// "missing-argument") may not.
import { existsSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const USAGE = 'usage: node scripts/design-system-locks/print-audit-errors.mjs <report.json>'

/**
 * Renders a report's errors as compact lines plus the single count line.
 * Exported so the output format has one source of truth and can be re-tested
 * without spawning a process.
 */
export function summarizeAuditErrors(report) {
  const errors = Array.isArray(report.errors) ? report.errors : []
  const lines = errors.map((error) => {
    const where = typeof error.path === 'string' && error.path.length > 0 ? `${error.path}: ` : ''
    return `[${error.code ?? 'unknown'}] ${where}${error.message}`
  })
  const acceptedDebt = report.debt?.acceptedCount ?? 0
  const acceptedExceptions = Array.isArray(report.appliedExceptions) ? report.appliedExceptions.length : 0
  const acceptedBoundaries = Array.isArray(report.appliedBoundaries) ? report.appliedBoundaries.length : 0
  lines.push(
    `design-system-locks: ${errors.length} error(s) — fix these; ` +
      'debt/appliedExceptions/appliedBoundaries are accepted entries and not listed ' +
      `(accepted: ${acceptedDebt} debt, ${acceptedExceptions} appliedExceptions, ${acceptedBoundaries} appliedBoundaries)`,
  )
  return lines
}

function readReportFile(reportPath) {
  if (!reportPath || !existsSync(reportPath)) {
    return { failure: `${USAGE}\nprint-audit-errors: report not found: ${reportPath ?? '<no path supplied>'}` }
  }
  try {
    return { report: JSON.parse(readFileSync(reportPath, 'utf8')) }
  } catch (error) {
    return { failure: `${USAGE}\nprint-audit-errors: report is not valid JSON (${reportPath}): ${error.message}` }
  }
}

export function runCli(argv, { stdout = process.stdout, stderr = process.stderr } = {}) {
  const read = readReportFile(argv[0])
  if (read.failure) {
    stderr.write(`${read.failure}\n`)
    return 2
  }
  const report = read.report
  if (report === null || typeof report !== 'object' || Array.isArray(report) || !Array.isArray(report.errors)) {
    stderr.write(`${USAGE}\nprint-audit-errors: report has no errors array — is this an audit.mjs --report file? (${argv[0]})\n`)
    return 2
  }
  for (const line of summarizeAuditErrors(report)) stdout.write(`${line}\n`)
  return report.errors.length > 0 ? 1 : 0
}

function isDirectCli(argv1 = process.argv[1]) {
  if (!argv1) return false
  try {
    return fileURLToPath(import.meta.url) === resolve(argv1)
  } catch {
    return false
  }
}

if (isDirectCli()) {
  process.exit(runCli(process.argv.slice(2)))
}
