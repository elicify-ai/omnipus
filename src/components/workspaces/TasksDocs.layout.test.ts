// T10 tests documentation content, not production source as a proxy for behavior.
import { expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

it('T10 documents the two-row toolbar, Board widening/fallback/restoration, checkbox header and Actions order', () => {
  const tasks = readFileSync(resolve(process.cwd(), 'docs/tasks.md'), 'utf8')
  const plans = readFileSync(resolve(process.cwd(), 'docs/plans.md'), 'utf8')
  expect(tasks).toContain('Board needs more room — showing list')
  expect(tasks).toMatch(/maximum docked width/i)
  expect(tasks).toMatch(/(?:restore|return)[^.\n]*previous width/i)
  expect(tasks).toMatch(/(?:automatically|automatic)[^.\n]*(?:returns?|switches? back)[^.\n]*Board/i)
  expect(tasks).toMatch(/Agent[^.\n]*Tags[^.\n]*New Task[^.\n]*one row/i)
  expect(tasks).toContain('Pri, Title, Status, Actions, Tags, Agent, Updated')
  expect(tasks).not.toMatch(/groups stack|stacks its status groups vertically/i)
  expect(plans).toMatch(/Show done[^.\n]*checkbox[^.\n]*Plans header/i)
  expect(plans).toMatch(/New Plan[^.\n]*(?:same|header)/i)
})
