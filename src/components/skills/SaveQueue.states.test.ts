// SaveQueue.states.test.ts — order 44 (S-83, S-117; Dataset D12, all 11
// rows; C-16). Oracle: skills-tools-redesign-spec.md C-16 (the state table
// and the queue rules) and Dataset D12 — every row is driven through the
// app-level queue with a controlled transport, never by calling the row
// component directly. Every expected string below is quoted from the spec's
// state table and toast wordings, never from an implementation.
import { describe, it, expect } from 'vitest'

interface SaveInteraction {
  method: 'PATCH' | 'GET'
  path: string
  body?: Record<string, unknown>
}
interface SaveOutcome {
  status: number
  body?: unknown
}
type SaveTransport = (interaction: SaveInteraction) => Promise<SaveOutcome>

// The driver the queue module must expose for these rows: clicks enqueue one
// PATCH per click naming only its own keys; the row state is observable per
// key; toasts arrive for outcomes after the page was left; settle() resolves
// when the queue is idle (every outcome landed and every confirm GET
// answered or failed).
interface SaveQueueDriver {
  click(change: { key: string; value: string }): void
  rowText(key: string): string
  rowValue(key: string): string | null
  hasRetry(key: string): boolean
  retry(key: string): void
  leavePage(): void
  toasts(): readonly string[]
  settle(): Promise<void>
}

// The seam: C-16 Ownership — "The queue lives in an app-level store, not in
// the page". GREEN assigns this factory to the real app-level queue module;
// null keeps the RED run loud (BLOCKED), never a skip.
let newSaveQueue: ((transport: SaveTransport) => SaveQueueDriver) | null = null

function driverOf(transport: SaveTransport): SaveQueueDriver {
  if (!newSaveQueue) {
    throw new Error(
      'BLOCKED: the C-16 app-level save queue for Skills & Tools is not implemented — ' +
        'required by skills-tools-redesign-spec C-16 (Ownership: the queue lives in an ' +
        'app-level store, not in the page) and Dataset D12 (S-83, S-117). ' +
        'GREEN assigns newSaveQueue to the real queue module.',
    )
  }
  return newSaveQueue(transport)
}

function deferred(): { promise: Promise<void>; resolve: () => void } {
  let resolve!: () => void
  const promise = new Promise<void>((res) => {
    resolve = res
  })
  return { promise, resolve }
}

// Let queued microtasks (the first request's issue) land before asserting.
const flush = (): Promise<void> => new Promise((resolve) => setTimeout(resolve, 0))

const PATCH = '/api/v1/security/permissions'

function ok(body?: unknown): SaveOutcome {
  return { status: 200, body }
}

describe('SaveQueue — Dataset D12, all 11 rows through the app-level queue (order 44)', () => {
  const rows: { name: string; run: () => Promise<void> }[] = [
    {
      name: 'D12#1: a 200 ends "Saved" (S-01)',
      run: async () => {
        const calls: SaveInteraction[] = []
        const q = driverOf(async (i) => {
          calls.push(i)
          return ok()
        })
        q.click({ key: 'bash', value: 'deny' })
        await q.settle()
        expect(q.rowText('bash')).toBe('Saved')
        expect(calls).toHaveLength(1)
        expect(calls[0].method).toBe('PATCH')
        expect(calls[0].path).toBe(PATCH)
      },
    },
    {
      name: 'D12#2: a 503 shows saved-not-applied with Retry; Retry re-sends the same keys and values (S-08)',
      run: async () => {
        const calls: SaveInteraction[] = []
        let attempt = 0
        const q = driverOf(async (i) => {
          calls.push(i)
          attempt++
          return attempt === 1 ? { status: 503 } : ok()
        })
        q.click({ key: 'plan', value: 'ask' })
        await q.settle()
        expect(q.rowText('plan')).toBe(
          'Saved, but not applied to running agents yet. Retry applying the change, or restart Omnipus.',
        )
        expect(q.hasRetry('plan')).toBe(true)
        q.retry('plan')
        await q.settle()
        expect(q.rowText('plan')).toBe('Saved')
        expect(calls).toHaveLength(2)
        expect(calls[1].body).toEqual(calls[0].body)
      },
    },
    {
      name: 'D12#3: a 400 is refused — "Not saved: <reason>." and the stored value from a fresh GET (S-07)',
      run: async () => {
        const q = driverOf(async (i) =>
          i.method === 'PATCH'
            ? { status: 400, body: { code: 'invalid_input', message: 'invalid value' } }
            : ok({ skill_policies: { bash: 'deny' } }),
        )
        q.click({ key: 'bash', value: 'allow' })
        await q.settle()
        expect(q.rowText('bash')).toBe('Not saved: invalid value.')
        expect(q.hasRetry('bash')).toBe(true)
        expect(q.rowValue('bash')).toBe('deny')
      },
    },
    {
      name: 'D12#4: a dropped response is never shown as Not saved — "Couldn\'t confirm the save. Checking…" then the stored value (S-83)',
      run: async () => {
        const getGate = deferred()
        const q = driverOf(async (i) => {
          if (i.method === 'PATCH') throw new TypeError('network down')
          await getGate.promise
          return ok({ skill_policies: { plan: 'deny' } })
        })
        q.click({ key: 'plan', value: 'allow' })
        await flush()
        expect(q.rowText('plan')).toBe("Couldn't confirm the save. Checking…")
        getGate.resolve()
        await q.settle()
        expect(q.rowValue('plan')).toBe('deny')
        expect(q.rowText('plan')).toBe('Showing what is stored now.')
      },
    },
    {
      name: 'D12#5: a slow first save holds the next request but not its Saving mark (S-117)',
      run: async () => {
        const calls: SaveInteraction[] = []
        const gate = deferred()
        const q = driverOf(async (i) => {
          calls.push(i)
          if (calls.length === 1) await gate.promise
          return ok()
        })
        q.click({ key: 'bash', value: 'deny' })
        await flush()
        q.click({ key: 'plan', value: 'ask' })
        // Marked Saving… immediately, showing the clicked value, while the
        // first change is still in flight.
        expect(q.rowText('plan')).toBe('Saving…')
        expect(q.rowValue('plan')).toBe('ask')
        expect(calls).toHaveLength(1)
        gate.resolve()
        await q.settle()
        expect(calls).toHaveLength(2)
        expect(q.rowText('plan')).toBe('Saved')
        // Each click is its own PATCH naming only its own keys.
        expect(Object.keys(calls[0].body ?? {})).toEqual(['skill_policies'])
        expect((calls[0].body as { skill_policies: Record<string, string> }).skill_policies).toEqual({
          bash: 'deny',
        })
      },
    },
    {
      name: 'D12#6: after an unknown first save the next click starts only after the fresh GET answers (S-117)',
      run: async () => {
        const calls: SaveInteraction[] = []
        const getGate = deferred()
        let patches = 0
        const q = driverOf(async (i) => {
          calls.push(i)
          if (i.method === 'PATCH') {
            patches++
            if (patches === 1) throw new TypeError('network down')
            return ok()
          }
          await getGate.promise
          return ok({ skill_policies: { plan: 'deny' } })
        })
        q.click({ key: 'plan', value: 'ask' })
        await flush()
        q.click({ key: 'bash', value: 'deny' })
        await flush()
        expect(calls.filter((c) => c.method === 'PATCH')).toHaveLength(1)
        getGate.resolve()
        await q.settle()
        expect(calls.filter((c) => c.method === 'PATCH')).toHaveLength(2)
      },
    },
    {
      name: 'D12#7: a 503 after leaving the page toasts saved-but-not-applied (S-117)',
      run: async () => {
        const q = driverOf(async () => ({ status: 503 }))
        q.click({ key: 'plan', value: 'ask' })
        q.leavePage()
        await q.settle()
        expect(q.toasts()).toContain(
          'A change on Skills & Tools was saved but is not applied to running agents yet.',
        )
      },
    },
    {
      name: 'D12#8: a 400 after leaving the page toasts the refusal with its reason (S-83)',
      run: async () => {
        const q = driverOf(async () => ({
          status: 400,
          body: { code: 'invalid_input', message: 'unknown key' },
        }))
        q.click({ key: 'plan', value: 'ask' })
        q.leavePage()
        await q.settle()
        expect(q.toasts()).toContain('A change on Skills & Tools was not saved: unknown key.')
      },
    },
    {
      name: "D12#9: a dropped response whose fresh GET fails too — \"Couldn't load what is stored.\" + Retry; the clicked value is not shown as stored (S-83)",
      run: async () => {
        const q = driverOf(async (i) => {
          if (i.method === 'PATCH') throw new TypeError('network down')
          throw new TypeError('check failed')
        })
        q.click({ key: 'bash', value: 'allow' })
        await q.settle()
        expect(q.rowText('bash')).toBe("Couldn't load what is stored.")
        expect(q.hasRetry('bash')).toBe(true)
        expect(q.rowValue('bash')).not.toBe('allow')
      },
    },
    {
      name: 'D12#10: a 500 is not stored — "Not saved: the settings file could not be written." + Retry (S-117)',
      run: async () => {
        const q = driverOf(async () => ({ status: 500 }))
        q.click({ key: 'bash', value: 'deny' })
        await q.settle()
        expect(q.rowText('bash')).toBe('Not saved: the settings file could not be written.')
        expect(q.hasRetry('bash')).toBe(true)
      },
    },
    {
      name: 'D12#11: two queued clicks on the same key send only the later value (S-117)',
      run: async () => {
        const calls: SaveInteraction[] = []
        const q = driverOf(async (i) => {
          calls.push(i)
          return ok()
        })
        q.click({ key: 'bash', value: 'ask' })
        q.click({ key: 'bash', value: 'deny' })
        await flush()
        await q.settle()
        const patches = calls.filter((c) => c.method === 'PATCH')
        expect(patches).toHaveLength(1)
        expect((patches[0].body as { skill_policies: Record<string, string> }).skill_policies).toEqual({
          bash: 'deny',
        })
      },
    },
  ]
  expect(rows).toHaveLength(11)

  let ran = 0
  for (const row of rows) {
    it(row.name, async () => {
      await row.run()
      ran++
    })
  }
  it('D12: every row of the dataset ran — no dimension is fixed by the test itself', () => {
    expect(ran).toBe(11)
  })
})
