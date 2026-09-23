#!/usr/bin/env node
/**
 * Merge per-project browser test evidence files into a single design-system-browser.json.
 *
 * The design-system audit reads a single design-system-browser.json file that aggregates
 * results from all 4 browser projects (chromium, firefox, webkit, chromium-coarse-pointer).
 * When running browser projects in parallel CI jobs, each job writes its own per-project
 * file (e.g., test-results/design-system-browser-chromium.json). This script merges them
 * into the single file the audit expects.
 *
 * Schema: each file is a Playwright JSON report with { config, suites, stats }.
 * Merge strategy:
 * - Combine all test suites from all projects
 * - Aggregate test counts and duration across projects
 * - Fail loudly if any expected project file is missing or empty
 */

import { existsSync, readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { resolve, dirname } from 'node:path'

const PROJECTS = ['chromium', 'firefox', 'webkit', 'chromium-coarse-pointer']
const TEST_RESULTS_DIR = 'test-results'
const OUTPUT_FILE = resolve(TEST_RESULTS_DIR, 'design-system-browser.json')

function readProjectFile(project) {
  const inputFile = resolve(TEST_RESULTS_DIR, `design-system-browser-${project}.json`)
  if (!existsSync(inputFile)) {
    throw new Error(`Missing evidence file for project '${project}': ${inputFile}`)
  }
  try {
    const content = readFileSync(inputFile, 'utf8')
    if (!content.trim()) {
      throw new Error(`Evidence file for project '${project}' is empty: ${inputFile}`)
    }
    return JSON.parse(content)
  } catch (error) {
    throw new Error(`Failed to parse evidence file for project '${project}': ${error.message}`)
  }
}

function mergeReports(reports) {
  if (reports.length === 0) {
    throw new Error('No reports to merge')
  }

  // Start with the structure from the first report
  const merged = {
    config: reports[0].config,
    suites: [],
    stats: {
      expected: 0,
      unexpected: 0,
      flaky: 0,
      skipped: 0,
      duration: 0,
    },
  }

  // Merge all suites and stats from each project
  for (const report of reports) {
    if (!report.suites || !Array.isArray(report.suites)) {
      continue
    }
    merged.suites.push(...report.suites)
    if (report.stats) {
      merged.stats.expected = (merged.stats.expected || 0) + (report.stats.expected || 0)
      merged.stats.unexpected = (merged.stats.unexpected || 0) + (report.stats.unexpected || 0)
      merged.stats.flaky = (merged.stats.flaky || 0) + (report.stats.flaky || 0)
      merged.stats.skipped = (merged.stats.skipped || 0) + (report.stats.skipped || 0)
      merged.stats.duration = (merged.stats.duration || 0) + (report.stats.duration || 0)
    }
  }

  return merged
}

async function main() {
  try {
    mkdirSync(TEST_RESULTS_DIR, { recursive: true })

    // Read all project files
    const reports = []
    const errors = []
    for (const project of PROJECTS) {
      try {
        const report = readProjectFile(project)
        reports.push(report)
      } catch (error) {
        errors.push(error.message)
      }
    }

    // All projects must have evidence files
    if (errors.length > 0) {
      console.error('Failed to merge browser evidence files:')
      for (const error of errors) {
        console.error(`  - ${error}`)
      }
      process.exit(1)
    }

    if (reports.length === 0) {
      throw new Error('No project reports were successfully loaded')
    }

    // Merge all reports
    const merged = mergeReports(reports)

    // Write merged file
    writeFileSync(OUTPUT_FILE, JSON.stringify(merged, null, 2))
    console.log(`Merged ${reports.length} project reports → ${OUTPUT_FILE}`)
    console.log(
      `  Tests: ${merged.stats.expected} expected, ` +
      `${merged.stats.unexpected} unexpected, ` +
      `${merged.stats.flaky} flaky, ` +
      `${merged.stats.skipped} skipped ` +
      `(${Math.round(merged.stats.duration / 1000)}s)`,
    )
  } catch (error) {
    console.error(`Merge failed: ${error.message}`)
    process.exit(1)
  }
}

await main()
