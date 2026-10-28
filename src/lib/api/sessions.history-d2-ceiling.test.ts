// T64/A2.3: actual generated+semantic page validation at the HTTP boundary.
import { expect, it } from 'vitest'
import { validateHistoryPage } from './history-validation'
import { ApiSchemaError } from './http'
import { SID, entry, page } from '../../../tests/chat-history-paging/fixtures'

it('T64: accepts a valid byte-ceiling underfill with exact continuation', () => {
  const p = page([entry(1)], { earlier: true, info: { limit: 100, min_rows: 100, stop_reason: 'byte_ceiling', projected_bytes: 1024 } })
  expect(validateHistoryPage(p, SID, 'T64')).toEqual(p)
})
it('T64: rejects an advertised multi-group page above its hard byte ceiling', () => {
  const p = page([entry(1), entry(2)], { info: { byte_ceiling: 8388608, projected_bytes: 8388609, byte_budget_exceeded: true, budget_override: 'height_floor' } })
  expect(() => validateHistoryPage(p, SID, 'T64')).toThrow(ApiSchemaError)
})
it('T64: the one-whole-group exception remains valid above8MiB', () => {
  const p = page([entry(1)], { info: { byte_ceiling: 8388608, projected_bytes: 8388609, byte_budget_exceeded: true, budget_override: 'atomic_group' } })
  expect(validateHistoryPage(p, SID, 'T64')).toEqual(p)
})
