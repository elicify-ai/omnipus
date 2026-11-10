import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { expect, it } from 'vitest'

it('T20–T27 document the populated/empty default, compact tiles footer checkbox, dedicated card rows and timestamp-backed duration', () => {
  const tasks = readFileSync(resolve(process.cwd(), 'docs/tasks.md'), 'utf8')
  const plans = readFileSync(resolve(process.cwd(), 'docs/plans.md'), 'utf8')
  for (const page of [tasks, plans]) {
    expect(page).toMatch(/open by default[^.\n]*plans[^.\n]*collapsed[^.\n]*none/i)
    expect(page).toMatch(/Unhide done plans[^.\n]*(?:bottom-left|below[^.\n]*All tasks)/i)
    expect(page).not.toMatch(/Show done \(N\)|collapsed[^.\n]*each time/i)
  }
  expect(tasks).toMatch(/first row[^.\n]*priority[^.\n]*info[^.\n]*actions/i)
  expect(tasks).toMatch(/full[^.\n]*width[^.\n]*two-line/i)
  expect(tasks).toMatch(/elapsed[^.\n]*started_at[^.\n]*completed_at/i)
  expect(tasks).toMatch(/missing[^.\n]*duration[^.\n]*omitted/i)
  expect(tasks).toMatch(/coloured left border/i)
  expect(tasks).toMatch(/approval[^.\n]*warning[^.\n]*icons/i)
  expect(tasks).toMatch(/tags[^.\n]*info[^.\n]*List/i)
})
