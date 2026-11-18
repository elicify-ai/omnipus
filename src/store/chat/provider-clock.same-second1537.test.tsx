// #1537 Q12 A, spec639ec8311c: use emitted_at_ms only in [second, second+1000).
// pkg/gateway/ws_tool_approval.go::WSHandler.sessionStateBytes reads
// now := time.Now() ONCE. emitted_at = now.UTC().Format(time.RFC3339), which
// truncates (floors) to whole seconds; emitted_at_ms = now.UnixMilli(). Thus
// both come from one reading and ms lies in [sec*1000, sec*1000+1000), not
// a rounded or symmetric +/-1second window. No production producer is mocked.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { active, bucket, feed, mount, named, reset, retry } from '../../../tests/helper-model-notices/harness'

const SECOND = Date.parse('2026-11-15T12:00:05.000Z')
const CLIENT = Date.parse('2036-01-01T00:00:00.000Z') // Unrelated client/server walls.
beforeEach(() => { reset(); vi.useFakeTimers(); vi.setSystemTime(CLIENT) })
afterEach(() => { reset(); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers() })

it.each([
  [0, 0, '0:25'],
  [999, 999, '0:24'],
  [1000, 0, '0:25'],
  [-1, 0, '0:01'],
] as const)('Q12 same-second delta=%ims selects clock offset=%ims and named countdown %s', (delta, acceptedDelta, countdown) => {
  feed({ ...active('own-turn', '2026-11-15T12:00:05Z'), emitted_at_ms: SECOND + delta })
  expect.soft(bucket().providerServerClock, `Q12 delta${delta}: exact sample at the real session_state handler; lower inclusive, upper exclusive, otherwise textual fallback`).toEqual({ serverMs: SECOND + acceptedDelta, clientMs: CLIENT })
  // The old retry's sent_at cannot replace this snapshot sample. For -1,
  // deadline T+1499ms makes a wrong T-1 sample display0:02 (1500ms), while
  // the required textual T fallback displays0:01 (1499ms), nearest-second.
  const extra = delta === -1 ? { sent_at: '2026-11-15T12:00:00.499Z', retry_at: '2026-11-15T12:00:06.499Z', retry_after_seconds: 6 } : {}
  feed(retry('child-a', 'turn-a', extra))
  expect.soft(bucket().providerServerClock, `Q12 delta${delta}: the older helper retry must not conceal the handler's boundary choice`).toEqual({ serverMs: SECOND + acceptedDelta, clientMs: CLIENT })
  const { container } = mount()
  const rows = named(container, 'Researcher', 'Retrying automatically')
  expect(rows.map(row => row.textContent), `FR-008 Q12 delta${delta}: exact visible countdown proves the accepted/fallback clock, not just a stored constant`).toEqual([
    `Researcher: openrouter is rate-limiting requests right now. Retrying automatically in ${countdown} (attempt 2 of 6).`,
  ])
  expect(rows[0], `Q12 delta${delta}: rendered named row, never hidden announcement-only text`).toBeVisible()
  // At delta0, numeric and textual fallback are identical: a lower-bound+1
  // mutant with the SAME fallback is equivalent at this observable boundary.
})
