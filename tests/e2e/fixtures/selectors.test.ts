// selectors.test.ts — vitest coverage for the fixtures' pure logic:
// waitForLiveSink's polling (SMALL-2 fix, external review 2026-08-13) and the
// navigation-redesign helpers' locator contracts (2026-10-11 port).
//
// This fixture lives under tests/e2e/ (Playwright, not vitest's normal scope
// per vite.config.ts's `include: ['src/**/*.test.{ts,tsx}']`) — vitest is run
// directly against this file's explicit path, not discovered via that glob.
//
// SCOPE RULE for this file: only PURE logic is unit-tested here. The
// selectAgent/freshChat orchestration (opening the sidebar, expanding a
// workspace, creating a session via REST, deep-linking, asserting the bound
// row) drives a real browser and is covered end-to-end by the Playwright
// suites — faking Playwright's Locator/expect API here would test the fake,
// not the helper. What IS pure and worth pinning: the label patterns those
// helpers build must match the component's actual accessible names, which
// are specification, derived from
// src/components/layout/sidebar/WorkspaceAgentList.tsx (NOT from the helper).

import { describe, it, expect, vi } from 'vitest'
import type { Page } from '@playwright/test'
import {
  waitForLiveSink,
  workspaceDisclosureButton,
  agentRowMenuButtonLabel,
  AGENT_ROW_MENU_ITEMS,
  DEFAULT_WORKSPACE_NAME,
} from './selectors'
import { originOf } from './session-setup'

interface FakeLocator {
  isVisible: () => Promise<boolean>
  first: () => FakeLocator
  locator: (selector: string) => FakeLocator
  textContent: () => Promise<string | null>
}

function makeFakeLocator(overrides: Partial<FakeLocator> = {}): FakeLocator {
  const loc: FakeLocator = {
    isVisible: async () => false,
    first: () => loc,
    locator: () => makeFakeLocator(),
    textContent: async () => null,
    ...overrides,
  }
  return loc
}

/** Maps a testid substring (as it appears in the CSS attribute selector
 * `waitForLiveSink`/its helpers build) to the fake locator that selector
 * should resolve to — a plain substring match is enough since every selector
 * this file builds embeds the literal testid string. */
function makeFakePage(
  byTestId: Record<string, FakeLocator>,
  waitForTimeout: () => Promise<void> = vi.fn(async () => undefined),
): Page {
  return {
    locator: (selector: string) => {
      for (const [testId, loc] of Object.entries(byTestId)) {
        if (selector.includes(testId)) return loc
      }
      return makeFakeLocator()
    },
    waitForTimeout,
  } as unknown as Page
}

describe('waitForLiveSink — SMALL-2 fix (fail fast + honest diagnosis on a reported WebRTC failure)', () => {
  it('throws immediately with the real reported reason once the Retry button is visible — never rides out the polling budget', async () => {
    // A real (if tiny) delay, not an instantly-resolving stub: if this ever
    // regresses back to the pre-fix behavior (which never returns early on a
    // Retry sighting), the loop below would otherwise spin as fast as the
    // event loop allows for the full `timeoutMs`, which reliably OOMs the
    // process rather than just failing the assertion — confirmed by actually
    // reverting the fix and observing exactly that crash while writing this
    // test. `timeoutMs` is kept small too, so an unbounded-but-real-paced
    // regression here fails in ~2s rather than 90s.
    const waitForTimeout = vi.fn(() => new Promise<void>((resolve) => setTimeout(resolve, 5)))
    const retryLoc = makeFakeLocator({
      isVisible: async () => true,
      locator: () =>
        makeFakeLocator({
          textContent: async () => '  Live video connection failed (ice-failed). Retry, or reload the page.  Retry ',
        }),
    })
    const page = makeFakePage(
      {
        'browser-live-video': makeFakeLocator({ isVisible: async () => false }),
        'browser-live-retry': retryLoc,
        'browser-live-img': makeFakeLocator({ isVisible: async () => false }),
      },
      waitForTimeout,
    )

    await expect(waitForLiveSink(page, 2_000)).rejects.toThrow(/ice-failed/)
    // FAIL FAST: the old code could only ever conclude by polling in
    // 500ms steps until the full budget elapsed. Proving this never once
    // calls the poll-delay is what proves it did NOT ride out any part of
    // that budget — it threw on the very first pass.
    expect(waitForTimeout).not.toHaveBeenCalled()
  })

  it('returns "video" the moment the video sink is visible, even with a stale Retry button also present', async () => {
    const page = makeFakePage({
      'browser-live-video': makeFakeLocator({ isVisible: async () => true }),
      'browser-live-retry': makeFakeLocator({ isVisible: async () => true }),
    })

    await expect(waitForLiveSink(page, 2_000)).resolves.toBe('video')
  })

  it('returns null after the budget elapses when neither the video sink nor a reported error ever appears (genuine capture-path stall)', async () => {
    const page = makeFakePage(
      {
        'browser-live-video': makeFakeLocator({ isVisible: async () => false }),
        'browser-live-retry': makeFakeLocator({ isVisible: async () => false }),
        'browser-live-img': makeFakeLocator({ isVisible: async () => false }),
      },
      vi.fn(async () => undefined), // stubbed so the real 500ms polling delay never actually elapses
    )

    await expect(waitForLiveSink(page, 5)).resolves.toBeNull()
  })
})

// ── Sidebar helper locator contracts (navigation redesign, 2026-10-11) ─────
//
// ORACLE: the component's own accessible-name templates, NOT the helper under
// test — WorkspaceAgentList.tsx:213 renders
//   aria-label={isExpanded ? `Hide ${project.name} agents` : `Show ${project.name} agents`}
// and the seeded default workspace is named "My Workspace"
// (pkg/gateway/rest_workspaces.go:584, ensureDefaultWorkspace).
// If the component's label changes, these tests fail and the helper must be
// re-derived — exactly the coupling the helper is supposed to have.

/** Captures the name pattern `workspaceDisclosureButton` hands to getByRole. */
function disclosurePatternFor(workspace: string): RegExp {
  let captured: { role?: string; name?: unknown } = {}
  const fakePage = {
    getByRole: (role: string, opts: { name?: unknown }) => {
      captured = { role, name: opts?.name }
      return {}
    },
  } as unknown as Page
  workspaceDisclosureButton(fakePage, workspace)
  expect(captured.role).toBe('button')
  expect(captured.name).toBeInstanceOf(RegExp)
  return captured.name as RegExp
}

describe('workspaceDisclosureButton — matches the component’s disclosure labels and nothing else', () => {
  it('matches both folded and expanded labels of its own workspace (exact, anchored)', () => {
    const pattern = disclosurePatternFor(DEFAULT_WORKSPACE_NAME)
    expect(DEFAULT_WORKSPACE_NAME).toBe('My Workspace') // oracle: rest_workspaces.go:584 seed
    expect(pattern.test('Show My Workspace agents')).toBe(true)
    expect(pattern.test('Hide My Workspace agents')).toBe(true)
  })

  it('rejects labels of other workspaces, wrong actions, and partial matches', () => {
    const pattern = disclosurePatternFor('My Workspace')
    expect(pattern.test('Show Other Workspace agents')).toBe(false)
    expect(pattern.test('Hide Other Workspace agents')).toBe(false)
    expect(pattern.test('Show My Workspace roaster')).toBe(false)
    // Not anchored → these would false-match the wrong row's label.
    expect(pattern.test('Please Show My Workspace agents now')).toBe(false)
    expect(pattern.test('Show My Workspace agents of 3')).toBe(false)
    // Double space is a different label than the component renders.
    expect(pattern.test('Show  My Workspace agents')).toBe(false)
  })

  it('escapes regex metacharacters in the workspace name — a literal-name workspace must not broaden the match', () => {
    const pattern = disclosurePatternFor('Finance.')
    expect(pattern.test('Show Finance. agents')).toBe(true)
    expect(pattern.test('Hide Finance. agents')).toBe(true)
    // An unescaped '.' would match this label of a DIFFERENT workspace.
    expect(pattern.test('Show FinanceX agents')).toBe(false)
  })
})

// ── Agent-row "⋯" menu constants (founder decisions D1/D2, 2026-10-11) ─────
//
// ORACLE: DECISIONS.md, NOT frontend-lead's component — D1 replaces the two
// icon buttons with ONE "⋯" button, whose accessible name is built by the
// agentRowMenuButtonLabel constant in selectors.ts. That name is CONFIRMED
// against the landed component (AgentRowMenu.tsx labels its trigger exactly
// `More actions for ${agentName}` — see selectors.ts's own "now CONFIRMED"
// note), not a placeholder awaiting a component. D2 fixes the menu's items
// AND their order: History · Hide from sidebar · Edit · Remove. These tests
// pin the constant so a label drift fails here, in seconds, instead of as a
// whole-shard Playwright timeout.

describe('agentRowMenuButtonLabel — the D1 "⋯" button’s parameterised accessible name', () => {
  it('builds the exact "More actions for <Agent>" label for a concrete agent', () => {
    expect(agentRowMenuButtonLabel('Jim')).toBe('More actions for Jim')
  })

  it('embeds the agent name verbatim — every agent’s label is distinct, so a row’s button never matches another row’s', () => {
    expect(agentRowMenuButtonLabel('Mia')).toBe('More actions for Mia')
    expect(agentRowMenuButtonLabel('Mia')).not.toBe(agentRowMenuButtonLabel('Jim'))
  })

  it('does not collide with the removed buttons’ labels — the D1 replacement must never re-match them', () => {
    // D7 removed "New chat with <Agent>" and "Past sessions with <Agent>".
    // If the parameterised label ever drifted back onto one of those
    // templates, the sidebar-agent-row-menu spec's absence assertions would
    // silently count the menu button itself — so the collision is pinned
    // impossible here, at the constant, not per spec.
    for (const removed of ['New chat with Jim', 'Past sessions with Jim']) {
      expect(agentRowMenuButtonLabel('Jim')).not.toBe(removed)
    }
  })
})

describe('AGENT_ROW_MENU_ITEMS — the D2 menu contract (items and order)', () => {
  it('is exactly History · Hide from sidebar · Edit · Remove, in that order', () => {
    expect([...AGENT_ROW_MENU_ITEMS]).toEqual([
      'History',
      'Hide from sidebar',
      'Edit',
      'Remove',
    ])
  })

  it('has no duplicates and no extras — four items, each named once', () => {
    expect(new Set(AGENT_ROW_MENU_ITEMS).size).toBe(AGENT_ROW_MENU_ITEMS.length)
    expect(AGENT_ROW_MENU_ITEMS.length).toBe(4)
  })
})

describe('originOf — freshChat rides the PAGE’s gateway, never a module constant', () => {
  // ORACLE: freshChat must hit whichever gateway the page is on (shared OR
  // isolated GatewayProcess), so the origin comes from the page URL; and it
  // must REFUSE to guess a server when the page is not on one
  // (g1-code-reviewer F3 / g1-silent-failure-hunter F4). Pure string logic —
  // no browser involved.

  function pageAt(url: string): Page {
    return { url: () => url } as unknown as Page
  }

  it('derives the origin from the page URL, including non-default ports (isolated GatewayProcess gateways)', () => {
    expect(originOf(pageAt('http://localhost:6060/#/workspaces/x/chat'))).toBe(
      'http://localhost:6060'
    )
    expect(originOf(pageAt('http://127.0.0.1:6139/'))).toBe('http://127.0.0.1:6139')
    expect(originOf(pageAt('https://omnipus.example.com/#/agents'))).toBe(
      'https://omnipus.example.com'
    )
  })

  it('refuses to guess a server off about:blank — the caller must goto() first', () => {
    expect(() => originOf(pageAt('about:blank'))).toThrow(/not on an app origin/)
    expect(() => originOf(pageAt(''))).toThrow(/not on an app origin/)
  })
})
