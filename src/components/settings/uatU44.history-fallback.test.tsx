// Independent CHECK Round U5 U5-C2/C3: router-count/typed-state classes and ACCEPTED limits.
// Accepted-limit cases document the agreed defect, never certify full FR-014 compliance.
import { afterEach, beforeEach, expect, it } from 'vitest'
import { act, screen } from '@testing-library/react'
import { browserEdgesU2 } from '../../../tests/helpers/settings-u2-navigation'
import { cleanupHistoryU44, historyU44, moveU44, snapshotU44, stayU44, stayedU44 } from '../../../tests/helpers/settings-u44-history'

afterEach(cleanupHistoryU44)
beforeEach(browserEdgesU2)

it('U5-C2 RJ — without Navigation API, a two-router-entry jump uses the exact +2 undo, not a one-step fallback', async () => {
  const c = await historyU44('router', false)
  try {
    const prompt = await moveU44(c, -2)
    await stayU44(c, prompt)
    stayedU44(c, 1, 'RJ')
    expect(c.edge.requests.filter(({ kind }) => kind === 'go').map(({ delta }) => delta), 'RJ: preserve the router-owned exact distance').toEqual([-2, 2])
  } finally { c.stop() }
})

it('U5-C2 TF0 — first router Gateway then a typed address restores Gateway without go(0) or go(-0)', async () => {
  const c = await historyU44('first', false)
  try {
    expect(c.home.index, 'TF0 premise: Gateway is the first router entry').toBe(0)
    await act(async () => { c.edge.typeHash('/settings?tab=about') })
    expect(window.history.state, 'TF0 premise: the moved-to address has NO router state').toBeNull()
    // Typing deliberately adds one entry BEFORE Stay. FR-014 forbids Stay
    // adding another; it does not undo the user's typed-entry creation.
    c.home.length += 1
    expect(window.history.length, 'TF0 premise: typed address adds exactly one entry before Stay').toBe(c.home.length)
    const prompt = await screen.findByRole('alertdialog', { name: 'Leave without saving 1 change?' })
    await stayU44(c, prompt)
    stayedU44(c, 1, 'TF0')
    expect(c.edge.requests.filter(({ kind }) => kind === 'go').map(({ delta }) => delta), 'TF0: one-step substitute, never a zero-step reload').toEqual([1])
  } finally { c.stop() }
})

it.each(['typed-jump', 'typed-target-jump'] as const)('U5-C2 accepted no-API multi-entry typed limit (%s) — Stay assumes one step and leaves Memory, not original Gateway', async (shape) => {
  const c = await historyU44(shape, false)
  try {
    expect(c.home.index, 'accepted-limit premise: original Gateway is entry 3').toBe(3)
    const prompt = await moveU44(c, -2)
    await stayU44(c, prompt)
    console.info('R44_ACCEPTED_TYPED_JUMP ' + JSON.stringify({ shape, home: c.home, after: snapshotU44(c.edge), requests: c.edge.requests }))
    expect(window.location.hash, 'accepted limit: one-step undo reaches the intervening Memory address').toBe('#/settings?tab=memory')
    expect(c.edge.index, 'accepted limit: only one of the two steps is undone').toBe(2)
    expect(c.edge.requests.filter(({ kind }) => kind === 'go').map(({ delta }) => delta)).toEqual([-2, 1])
    expect(c.edge.reloads, 'accepted limitation never permits a document reload').toEqual([])
    expect(window.history.length).toBe(c.home.length)
    expect(screen.getByRole('spinbutton', { name: 'Port' }), 'draft remains on the edit screen despite the wrong address').toHaveValue(8081)
    expect(c.t.writes()).toEqual([])
  } finally { c.stop() }
})

it('U5-C2 accepted no-API typed restore later than 300ms — extra Back cancels the undo and asks a second leave question on Memory', async () => {
  const c = await historyU44('late-typed', false)
  const draftInput = screen.getByRole('spinbutton', { name: 'Port' })
  try {
    const prompt = await moveU44(c, -1, true)
    c.edge.delayGo(600) // Twice the accepted 300ms fallback window; platform input, not a speed assertion.
    await stayU44(c, prompt)
    console.info('R44_ACCEPTED_LATE ' + JSON.stringify({ home: c.home, after: snapshotU44(c.edge), questions: c.seen.size, requests: c.edge.requests, applied: c.edge.applied }))
    expect(c.edge.requests.map(({ kind, delta }) => ({ kind, delta })), 'accepted limit: late undo and extra Back both really execute').toEqual([
      { kind: 'back', delta: -1 }, { kind: 'go', delta: 1 }, { kind: 'back', delta: -1 },
    ])
    expect(window.location.hash, 'accepted limit: extra move returns to blocked Memory').toBe('#/settings?tab=memory')
    expect(c.edge.index).toBe(2)
    expect(c.seen.size, 'accepted limit: a second leave question is observable').toBe(2)
    expect(screen.getByRole('alertdialog', { name: 'Leave without saving 1 change?' })).toBeVisible()
    expect(c.edge.reloads).toEqual([])
    // The accepted second modal correctly hides background controls from the
    // accessibility tree. The real captured input must still be mounted/dirty.
    expect(draftInput).toBeInTheDocument()
    expect(draftInput).toHaveValue(8081)
    expect(c.t.writes()).toEqual([])
  } finally { c.stop() }
})

it('U5-C3 — the one-off exact +2 undo is released after Stay, so a later go(-1) remains a genuine Back', async () => {
  const c = await historyU44('router', true)
  const nativeGo = window.history.go
  try {
    await stayU44(c, await moveU44(c, -2))
    stayedU44(c, 1, 'release exact first Stay')
    expect.soft(window.history.go, 'U5-C3: one-off replacement is removed after Stay').toBe(nativeGo)
    const mark = c.edge.requests.length
    const prompt = await moveU44(c, -1)
    expect(c.edge.requests.slice(mark).map(({ kind, delta }) => ({ kind, delta })), 'U5-C3: later browser call retains its own distance/sign').toEqual([{ kind: 'go', delta: -1 }])
    await stayU44(c, prompt)
    stayedU44(c, 2, 'release exact second Stay')
    expect(window.history.go, 'U5-C3: second Stay also releases its replacement').toBe(nativeGo)
  } finally { c.stop() }
})

it('U5-C3 — the one-off typed-state fallback is released after Stay, not left over for the next browser call', async () => {
  const c = await historyU44('typed', false)
  const nativeGo = window.history.go
  try {
    await stayU44(c, await moveU44(c, -1, true))
    stayedU44(c, 1, 'release fallback')
    expect(window.history.go, 'U5-C3: typed fallback releases the exact original function').toBe(nativeGo)
  } finally { c.stop() }
})
