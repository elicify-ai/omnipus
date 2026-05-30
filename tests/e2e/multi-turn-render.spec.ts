/**
 * multi-turn-render.spec.ts — T1.10
 *
 * T1.10: turn1_badges_remain_in_turn1_bubble_after_replay
 *   Multi-turn session with tool calls per turn (2 turns). Reload page.
 *   Assert that turn-1 badges have turn-1's [data-message-id] as DOM ancestor.
 *   This replaces the order-insensitive Set assertion: it requires that each
 *   badge is nested inside the correct assistant message bubble, not just that
 *   all badge names are present somewhere in the DOM.
 *
 * Regression class: T0.5 (array-equality badge ordering). A bug where tool
 * calls "teleport" from turn 1 to turn 2's bubble during replay would pass
 * a Set-based assertion (all tool names present) but fail this DOM-ancestry check.
 */

import { expect, type Page } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { seedAndOpenSession } from './fixtures/session-setup'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

// ── Helper: get data-message-id from the ancestor message bubble ──────────────

/**
 * For each [data-testid="tool-call-badge"] element on the page, find the
 * nearest ancestor that has a [data-message-id] attribute and return that ID.
 *
 * If a badge is not inside any [data-message-id] ancestor, returns null.
 * This is used to assert that badges are correctly anchored to their parent
 * assistant message and have not "teleported" to a different message bubble.
 */
async function getBadgeAncestorMessageIds(page: Page): Promise<(string | null)[]> {
  return page.evaluate(() => {
    const badges = Array.from(document.querySelectorAll('[data-testid="tool-call-badge"]'))
    return badges.map((badge) => {
      let el: Element | null = badge.parentElement
      while (el) {
        if (el.hasAttribute('data-message-id')) {
          return el.getAttribute('data-message-id')
        }
        el = el.parentElement
      }
      return null // badge has no [data-message-id] ancestor
    })
  })
}

// ── T1.10 ─────────────────────────────────────────────────────────────────────

test(
  'turn1_badges_remain_in_turn1_bubble_after_replay',
  async ({ page }) => {
    // Seed a 2-turn session: each assistant turn has one tool call.
    // Turn 1: "shell" tool call
    // Turn 2: "fs.list" tool call
    //
    // The buggy behavior: after replay, turn 1's "shell" badge appears
    // inside turn 2's assistant message bubble (its DOM ancestor is
    // turn 2's data-message-id). This test catches that exact regression.

    const { sessionTitle } = await seedAndOpenSession(page, 'multi-turn-render-t110', [
      // Turn 1
      {
        id: 'user-t110-1',
        role: 'user',
        content: 'First question',
        timestamp: new Date(Date.now() - 10000).toISOString(),
        agent_id: '',
      },
      {
        id: 'asst-t110-1',
        role: 'assistant',
        content: 'First response with a tool call.',
        timestamp: new Date(Date.now() - 9000).toISOString(),
        agent_id: 'main',
        tool_calls: [
          {
            id: 'tc-turn1-shell',
            tool: 'shell',
            status: 'success',
            duration_ms: 42,
            parameters: { cmd: 'echo turn1' },
            result: { stdout: 'turn1\n' },
          },
        ],
      },
      // Turn 2
      {
        id: 'user-t110-2',
        role: 'user',
        content: 'Second question',
        timestamp: new Date(Date.now() - 8000).toISOString(),
        agent_id: '',
      },
      {
        id: 'asst-t110-2',
        role: 'assistant',
        content: 'Second response with a different tool call.',
        timestamp: new Date(Date.now() - 7000).toISOString(),
        agent_id: 'main',
        tool_calls: [
          {
            id: 'tc-turn2-fslist',
            tool: 'fs.list',
            status: 'success',
            duration_ms: 15,
            parameters: { path: '/tmp' },
            result: { entries: ['a', 'b'] },
          },
        ],
      },
    ])

    // Step 1: Verify initial render has 2 assistant messages and 2 badges
    // Exclude data-status="running" — only count completed messages, not placeholders.
    const assistants = page.locator('[data-message-id]:not(.flex-row-reverse):not([data-status="running"])')
    await expect(assistants).toHaveCount(2, { timeout: 15_000 })

    const badges = page.locator('[data-testid="tool-call-badge"]')
    await expect(badges).toHaveCount(2, { timeout: 10_000 })

    // Capture the message IDs for the two completed assistant messages.
    // Filter out data-status="running" to exclude in-progress placeholders.
    const msgIds = await page.evaluate(() => {
      const msgs = Array.from(document.querySelectorAll('[data-message-id]'))
        .filter((el) => !el.classList.contains('flex-row-reverse') && el.getAttribute('data-status') !== 'running')
      return msgs.map((el) => el.getAttribute('data-message-id'))
    })

    expect(msgIds).toHaveLength(2)
    const [turn1MsgId, turn2MsgId] = msgIds
    expect(turn1MsgId, 'Turn 1 message must have a data-message-id').toBeTruthy()
    expect(turn2MsgId, 'Turn 2 message must have a data-message-id').toBeTruthy()

    // Step 2: Capture which message bubble each badge belongs to
    const ancestorsBefore = await getBadgeAncestorMessageIds(page)
    expect(ancestorsBefore).toHaveLength(2)

    // The shell badge (turn 1) must be inside turn 1's bubble
    const shellBadge = page.locator('[data-testid="tool-call-badge"][data-tool="shell"]').first()
    await expect(shellBadge).toBeVisible({ timeout: 5_000 })

    const shellBadgeAncestor = await page.evaluate((shellTool) => {
      const badge = document.querySelector(`[data-testid="tool-call-badge"][data-tool="${shellTool}"]`)
      if (!badge) return null
      let el: Element | null = badge.parentElement
      while (el) {
        if (el.hasAttribute('data-message-id')) return el.getAttribute('data-message-id')
        el = el.parentElement
      }
      return null
    }, 'shell')

    // The fs.list badge (turn 2) must be inside turn 2's bubble
    const fslistBadgeAncestor = await page.evaluate((fslistTool) => {
      const badge = document.querySelector(`[data-testid="tool-call-badge"][data-tool="${fslistTool}"]`)
      if (!badge) return null
      let el: Element | null = badge.parentElement
      while (el) {
        if (el.hasAttribute('data-message-id')) return el.getAttribute('data-message-id')
        el = el.parentElement
      }
      return null
    }, 'fs.list')

    expect(
      shellBadgeAncestor,
      `shell badge must be inside turn-1's message bubble (${turn1MsgId}), got: ${shellBadgeAncestor}`,
    ).toBe(turn1MsgId)

    expect(
      fslistBadgeAncestor,
      `fs.list badge must be inside turn-2's message bubble (${turn2MsgId}), got: ${fslistBadgeAncestor}`,
    ).toBe(turn2MsgId)

    // Step 3: Reload the page (simulates re-opening the session from history)
    await page.goto('/')
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })

    // Reopen the session via the sessions panel
    await page.getByRole('button', { name: 'Open sessions panel' }).click()
    const sessionBtn = page
      .getByRole('button', { name: new RegExp(`Open session: ${sessionTitle}`, 'i') })
      .first()
    await expect(sessionBtn).toBeVisible({ timeout: 10_000 })
    await sessionBtn.click()

    // Wait for replay to complete
    const input = page.locator('textarea')
    await expect(input.first()).toBeEnabled({ timeout: 30_000 })

    // Step 4: After replay, re-assert badge DOM ancestry
    await expect(assistants).toHaveCount(2, { timeout: 15_000 })
    await expect(badges).toHaveCount(2, { timeout: 10_000 })

    // Get new message IDs (they may be re-generated on replay, so we re-query)
    const msgIdsAfterReplay = await page.evaluate(() => {
      const msgs = Array.from(document.querySelectorAll('[data-message-id]'))
        .filter((el) => !el.classList.contains('flex-row-reverse'))
      return msgs.map((el) => el.getAttribute('data-message-id'))
    })

    expect(msgIdsAfterReplay).toHaveLength(2)
    const [replayTurn1MsgId, replayTurn2MsgId] = msgIdsAfterReplay

    const shellBadgeAncestorAfterReplay = await page.evaluate((shellTool) => {
      const badge = document.querySelector(`[data-testid="tool-call-badge"][data-tool="${shellTool}"]`)
      if (!badge) return null
      let el: Element | null = badge.parentElement
      while (el) {
        if (el.hasAttribute('data-message-id')) return el.getAttribute('data-message-id')
        el = el.parentElement
      }
      return null
    }, 'shell')

    const fslistBadgeAncestorAfterReplay = await page.evaluate((fslistTool) => {
      const badge = document.querySelector(`[data-testid="tool-call-badge"][data-tool="${fslistTool}"]`)
      if (!badge) return null
      let el: Element | null = badge.parentElement
      while (el) {
        if (el.hasAttribute('data-message-id')) return el.getAttribute('data-message-id')
        el = el.parentElement
      }
      return null
    }, 'fs.list')

    // Core assertion (T1.10): after reload+replay, each badge is still in the
    // correct turn's message bubble — they have NOT teleported to the other turn.
    expect(
      shellBadgeAncestorAfterReplay,
      [
        `After reload+replay: shell badge must remain inside turn-1's bubble.`,
        `Got ancestor: ${shellBadgeAncestorAfterReplay}, expected turn-1 id: ${replayTurn1MsgId}.`,
        'This failure indicates turn-1 tool calls teleported to turn-2 during replay.',
      ].join(' '),
    ).toBe(replayTurn1MsgId)

    expect(
      fslistBadgeAncestorAfterReplay,
      [
        `After reload+replay: fs.list badge must remain inside turn-2's bubble.`,
        `Got ancestor: ${fslistBadgeAncestorAfterReplay}, expected turn-2 id: ${replayTurn2MsgId}.`,
      ].join(' '),
    ).toBe(replayTurn2MsgId)
  },
)
