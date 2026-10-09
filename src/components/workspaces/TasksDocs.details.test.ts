import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { expect, it } from 'vitest'

it('documents bounded Graph framing recovery and exact-title fallback-font recovery', () => {
  const tasks = readFileSync(resolve(process.cwd(), 'docs/tasks.md'), 'utf8')
  const plans = readFileSync(resolve(process.cwd(), 'docs/plans.md'), 'utf8')
  expect(tasks).toMatch(/font-loading check fails[^.\n]*fallback font[^.\n]*without changing[^.\n]*text/i)
  expect(plans).toMatch(/tries once more at the current zoom/i)
  expect(plans).toMatch(/map stays usable[^.\n]*Couldn't open at the top[^.\n]*Retry/i)
  expect(plans).toMatch(/still pan, zoom and open tasks/i)
})

it('T15–T19 document corrected info-hover task/plan details, equal Board height, plain metadata and always-visible actions', () => {
  const tasks = readFileSync(resolve(process.cwd(), 'docs/tasks.md'), 'utf8')
  const plans = readFileSync(resolve(process.cwd(), 'docs/plans.md'), 'utf8')
  expect(tasks).toMatch(/info icon[^.\n]*full title, status, agent, tags, plan and last updated/i)
  expect(tasks).toMatch(/Hover[^.\n]*does not open/i)
  expect(tasks).toMatch(/Escape[^.\n]*outside[^.\n]*close/i)
  expect(tasks).toMatch(/all Board cards[^.\n]*same height[^.\n]*tallest/i)
  expect(tasks).toMatch(/coloured left border[^.\n]*status/i) // T27 replaces the card's status dot/word.
  expect(tasks).toMatch(/actions[^.\n]*always visible/i)
  expect(plans).toMatch(/info icon[^.\n]*full title, status, progress[^.\n]*owner agent[^.\n]*updated/i)
  expect(tasks).toMatch(/Hover over a task's[^.\n]*info icon/i)
  expect(tasks).toMatch(/Keyboard focus on the icon[^.\n]*opens/i)
  expect(tasks).not.toMatch(/one tap opens the preview|click-only info icon/i)
  expect(tasks).toMatch(/without an agent[^.\n]*no leading separator/i) // T26 replaces wrapped metadata rows.
  expect(plans).not.toMatch(/Hover or keyboard focus reveals/i)
})
