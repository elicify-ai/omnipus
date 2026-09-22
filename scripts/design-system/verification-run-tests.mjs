#!/usr/bin/env node

import { mkdir } from 'node:fs/promises'
import { resolve } from 'node:path'
import { spawnSync } from 'node:child_process'

const mode = process.argv[2]
if (!['unit', 'storybook'].includes(mode)) {
  throw new Error('usage: verification-run-tests.mjs <unit|storybook>')
}

await mkdir(resolve('test-results'), { recursive: true })
const vitest = resolve('node_modules/vitest/vitest.mjs')

function run(args, environment = {}) {
  const result = spawnSync(process.execPath, [vitest, ...args], {
    stdio: 'inherit',
    env: { ...process.env, ...environment },
  })
  return result.status ?? 1
}

let exitCode = 0
if (mode === 'unit') {
  exitCode = run([
    'run', 'src/components/ui', 'src/design-system', '--maxWorkers=2', '--reporter=default', '--reporter=json',
    '--outputFile=test-results/design-system-components.json',
  ])
} else {
  for (const browser of ['chromium', 'firefox', 'webkit']) {
    const status = run([
      '--config', '.storybook/vitest.config.ts', 'run', '--reporter=default', '--reporter=json',
      `--outputFile=test-results/design-system-storybook-${browser}.json`,
    ], { DESIGN_SYSTEM_BROWSER: browser })
    if (status !== 0) exitCode = status
  }
}
process.exitCode = exitCode
