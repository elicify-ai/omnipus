import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { expect, it } from 'vitest'

it('T15–T19 document corrected info-hover task/plan details, equal Board height, plain metadata and always-visible actions', () => {
  const tasks = readFileSync(resolve(process.cwd(), 'docs/tasks.md'), 'utf8')
  const plans = readFileSync(resolve(process.cwd(), 'docs/plans.md'), 'utf8')
  expect(tasks).toMatch(/info icon[^.\n]*full title, status, agent, tags, plan and last updated/i)
  expect(tasks).toMatch(/Hover[^.\n]*does not open/i)
  expect(tasks).toMatch(/Escape[^.\n]*outside[^.\n]*close/i)
  expect(tasks).toMatch(/all Board cards[^.\n]*same height[^.\n]*tallest/i)
  expect(tasks).toMatch(/status dot[^.\n]*plain text/i)
  expect(tasks).toMatch(/actions[^.\n]*always visible/i)
  expect(plans).toMatch(/info icon[^.\n]*full title, status, progress[^.\n]*owner agent[^.\n]*updated/i)
  expect(tasks).toMatch(/Hover over a task's[^.\n]*info icon/i)
  expect(tasks).toMatch(/Keyboard focus on the icon[^.\n]*opens/i)
  expect(tasks).not.toMatch(/one tap opens the preview|click-only info icon/i)
  expect(tasks).toMatch(/wrapped line never starts with a dot/i)
  expect(plans).not.toMatch(/Hover or keyboard focus reveals/i)
})
