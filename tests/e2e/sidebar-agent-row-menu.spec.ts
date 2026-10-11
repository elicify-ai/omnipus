/**
 * sidebar-agent-row-menu.spec.ts — the sidebar agent row's "⋯" menu.
 *
 * SPEC SOURCE (every expected value in this file): DECISIONS.md, squad
 * nav-agent-row-menu-20261011, founder decisions D1–D9, 2026-10-11:
 *   D1  the row's "+ New chat with <Agent>" and "Past sessions with <Agent>"
 *       icon buttons are REPLACED by ONE "⋯" button that opens a menu;
 *   D2  the menu's items are EXACTLY History · Hide from sidebar · Edit ·
 *       Remove, in that order;
 *   D3  "History" keeps its name and opens the same past-chats view the
 *       "Past sessions" button opened — the Sessions search modal
 *       (WorkspaceAgentList.tsx openSearchModal → SearchModal, DialogTitle
 *       "Sessions");
 *   D4  "Remove" removes the agent from THIS workspace's team only (agent
 *       survives elsewhere); the confirm names the agent and the workspace,
 *       and cancelling leaves the team intact;
 *   D5  "Hide from sidebar" is per-workspace, saved on the server, the agent
 *       keeps working, and the workspace group shows a quiet "N hidden ·
 *       Show" line that brings hidden agents back;
 *   D6  "Edit" opens the EXISTING unified edit flow — the agent profile
 *       sheet (AgentProfile; data-testid="agent-name-input");
 *   D7  the new-chat UI access point is gone everywhere (asserted here by
 *       the "New chat with <Agent>" absence below; the fresh-chat seam the
 *       rest of the suite uses is session-setup.ts::freshChat);
 *   D8  a member that cannot leave (the workspace's default agent — the
 *       seeded default is Mia, pkg/coreagent/seed.go, pinned by
 *       TestSeedConfig_MiaIsDefault; the existing Team panel already refuses
 *       her removal, WorkspaceTeamTab.tsx "Cannot remove … — it is the
 *       default agent") has Remove DISABLED with a visible reason, never a
 *       silent no-op.
 *
 * RED STATUS (deliberate): this spec is written from the decisions, not from
 * any implementation. The "⋯" menu does not exist in this tree yet
 * (frontend-lead implements it on its own branch; backend-lead lands the D5
 * hidden-agents persistence), so every test below is expected to FAIL until
 * both land. That is the RED half of RED/GREEN/CHECK — not a flake and not a
 * skip. The menu button's accessible name is parameterised in ONE place,
 * selectors.ts::agentRowMenuButtonLabel ("More actions for <Agent>") — when
 * frontend-lead's component lands, confirm that constant against the real
 * accessible name and adjust THERE, never here.
 *
 * NO LLM: this spec sends no messages and needs no model — it drives sidebar
 * UI only (the D4/D8 rows chosen, Jim and Mia, are exercised without any
 * turn being started).
 *
 * STATE LEAKAGE: the gateway persists across tests in a run (fresh-seeded
 * per run). The D5 test hides Jim and therefore RESTORES him at its end, so
 * later tests (and later specs on the same gateway) see the seeded roster.
 */

import { expect, type Locator, type Page } from '@playwright/test'
import { test } from './fixtures/console-errors'
import {
  AGENT_ROW_MENU_ITEMS,
  agentRowLocator,
  agentRowMenuButtonLabel,
  agentRowGroup,
  DEFAULT_WORKSPACE_NAME,
} from './fixtures/selectors'

const WORKSPACE = DEFAULT_WORKSPACE_NAME // oracle: ensureDefaultWorkspace seed, "My Workspace"

/** The row's "⋯" menu button (D1) — named through the ONE parameterisation
 * point so a label drift is fixed in selectors.ts, not per test. */
function rowMenuButton(row: Locator, agentName: string): Locator {
  return row.getByRole('button', { name: agentRowMenuButtonLabel(agentName), exact: true })
}

/** Resolve Jim's row WITHOUT waiting for it to be visible — absence-capable
 * (the D5 test must be able to assert Jim is NOT there; a visibility wait
 * would turn exactly that absence into a timeout). */
async function jimRow(page: Page): Promise<Locator> {
  return agentRowLocator(page, /Jim/i, WORKSPACE)
}

/** The workspace group container — the roster disclosure's group, which D5
 * says carries the hidden-agents line. Requires the sidebar open. */
function workspaceGroup(page: Page): Locator {
  return page.getByRole('group', { name: WORKSPACE, exact: true })
}

/** Read the REASON a disabled menu item carries (D8: "disabled with a
 * reason, never a silent no-op"). The exact surfacing is frontend-lead's to
 * choose; a reason must exist somewhere user-visible: a title/tooltip on the
 * item itself, or text behind its aria-describedby. Returns null when NEITHER
 * exists — the silent no-op D8 forbids. */
async function disabledReason(page: Page, item: Locator): Promise<string | null> {
  const title = await item.getAttribute('title', { timeout: 5_000 }).catch(() => null)
  if (title && title.trim().length > 0) return title.trim()
  const describedBy = await item.getAttribute('aria-describedby', { timeout: 5_000 }).catch(() => null)
  if (describedBy) {
    for (const id of describedBy.split(/\s+/).filter(Boolean)) {
      const text = await page
        .locator(`[id="${id}"]`)
        .textContent({ timeout: 5_000 })
        .catch(() => null)
      if (text && text.trim().length > 0) return text.trim()
    }
  }
  return null
}

test.describe('sidebar agent row: the "⋯" menu (DECISIONS.md D1–D9)', () => {
  test('D1: the row shows ONE menu button and neither removed button', async ({ page }) => {
    const row = await jimRow(page)

    // Exactly ONE "⋯" button (parameterised name — see agentRowMenuButtonLabel).
    await expect(
      rowMenuButton(row, 'Jim'),
      'the row must expose exactly one "⋯" menu button (D1)',
    ).toHaveCount(1)

    // The removed buttons are gone everywhere (D1/D7) — absence is the point.
    await expect(
      row.getByRole('button', { name: /^New chat with / }),
      'the "New chat with <Agent>" button is removed (D7) and must not come back',
    ).toHaveCount(0)
    await expect(
      row.getByRole('button', { name: /^Past sessions with / }),
      'the "Past sessions with <Agent>" button is replaced by the menu (D1)',
    ).toHaveCount(0)
  })

  test('D2: the menu lists exactly History · Hide from sidebar · Edit · Remove, in that order', async ({ page }) => {
    const row = await jimRow(page)
    await rowMenuButton(row, 'Jim').click({ timeout: 15_000 })

    const menu = page.getByRole('menu')
    await expect(menu, 'the "⋯" button must open a menu (D1)').toBeVisible({ timeout: 15_000 })

    // WHOLE-SHAPE oracle: the exact items in the exact D2 order — an extra,
    // missing, renamed or reordered item all fail this one assertion.
    const items = menu.getByRole('menuitem')
    await expect(items, 'menu items must match D2 exactly, in order').toHaveText([
      ...AGENT_ROW_MENU_ITEMS,
    ])

    // The menu is dismissable the way menus are: Escape closes it.
    await page.keyboard.press('Escape')
    await expect(menu).toBeHidden({ timeout: 15_000 })
  })

  test('D3: History opens the past-chats (Sessions) modal', async ({ page }) => {
    const row = await jimRow(page)
    await rowMenuButton(row, 'Jim').click({ timeout: 15_000 })

    // D3: same view the "Past sessions" button drove — the Sessions search
    // modal (SearchModal.tsx DialogTitle "Sessions").
    await page
      .getByRole('menu')
      .getByRole('menuitem', { name: 'History' })
      .click({ timeout: 15_000 })

    const sessionsModal = page.getByRole('dialog', { name: 'Sessions' })
    await expect(
      sessionsModal,
      'History must open the same past-chats view the "Past sessions" button opened (D3)',
    ).toBeVisible({ timeout: 15_000 })

    await page.keyboard.press('Escape')
    await expect(sessionsModal).toBeHidden({ timeout: 15_000 })
  })

  test('D5: Hide from sidebar hides the row, shows "1 hidden · Show", persists across reload, and Show restores', async ({ page }) => {
    const row = await jimRow(page)
    await rowMenuButton(row, 'Jim').click({ timeout: 15_000 })
    await page
      .getByRole('menu')
      .getByRole('menuitem', { name: 'Hide from sidebar' })
      .click({ timeout: 15_000 })

    // Jim's row is gone from the roster…
    await expect(
      row,
      'hiding must remove the agent row from its workspace roster (D5)',
    ).toHaveCount(0, { timeout: 15_000 })
    // …Mia is untouched (hide is per-agent, not a roster-wide effect).
    await expect(await agentRowGroup(page, /Mia/i, WORKSPACE)).toBeVisible({ timeout: 15_000 })

    // …and the workspace group shows the quiet hidden line with its Show
    // affordance (D5: 'a quiet "N hidden · Show" line'). The exact copy is
    // the implementation's to finalise; "1 hidden" and a control named
    // exactly "Show" are the decision's own words and both must be present.
    // (exact: true — a substring /Show/i would also catch the workspace
    // disclosure's "Show My Workspace agents" label.)
    const group = workspaceGroup(page)
    await expect(group.getByText(/1 hidden/)).toBeVisible({ timeout: 15_000 })
    const showButton = group.getByRole('button', { name: 'Show', exact: true })
    await expect(showButton, 'the hidden line must offer a way back (D5)').toBeVisible({
      timeout: 15_000,
    })

    // D5: saved on the SERVER — a full reload must still hide Jim.
    await page.reload({ waitUntil: 'domcontentloaded' })
    const reopenedRow = await jimRow(page)
    await expect(
      reopenedRow,
      'the hidden state must persist across a reload (D5: saved on the server, not just in the SPA)',
    ).toHaveCount(0, { timeout: 15_000 })
    await expect(workspaceGroup(page).getByText(/1 hidden/)).toBeVisible({ timeout: 15_000 })

    // Show brings the agent back.
    await workspaceGroup(page).getByRole('button', { name: 'Show', exact: true }).click({ timeout: 15_000 })
    await expect(
      await jimRow(page),
      'Show must restore the hidden agent row (D5)',
    ).toBeVisible({ timeout: 15_000 })

    // And the restore persists too — the roster is back to seeded for the
    // next test on this gateway (no state leakage).
    await page.reload({ waitUntil: 'domcontentloaded' })
    await expect(await jimRow(page), 'the restored row must stay after a reload').toBeVisible({
      timeout: 15_000,
    })
  })

  test('D6: Edit opens the existing unified agent editor with the agent loaded', async ({ page }) => {
    const row = await jimRow(page)
    await rowMenuButton(row, 'Jim').click({ timeout: 15_000 })
    await page
      .getByRole('menu')
      .getByRole('menuitem', { name: 'Edit' })
      .click({ timeout: 15_000 })

    // D6: the EXISTING unified create/edit flow — the agent profile sheet,
    // carrying the agent's own name in its name field (no new editor).
    const nameInput = page.getByTestId('agent-name-input')
    await expect(
      nameInput,
      'Edit must open the existing unified agent editor (D6)',
    ).toBeVisible({ timeout: 20_000 })
    await expect(nameInput).toHaveValue(/^Jim$/i, { timeout: 15_000 })
  })

  test('D4: Remove asks a confirm that names the agent and the workspace; Cancel keeps the team intact', async ({ page }) => {
    const row = await jimRow(page)
    await rowMenuButton(row, 'Jim').click({ timeout: 15_000 })
    await page
      .getByRole('menu')
      .getByRole('menuitem', { name: 'Remove' })
      .click({ timeout: 15_000 })

    // D4: a confirm dialog that names BOTH the agent and the workspace, so
    // the user knows exactly which membership is ending.
    const confirm = page.getByRole('dialog')
    await expect(confirm, 'Remove must ask for confirmation (D4)').toBeVisible({ timeout: 15_000 })
    const confirmText = (await confirm.textContent({ timeout: 15_000 })) ?? ''
    expect(
      confirmText,
      'the Remove confirm must name the agent (D4)',
    ).toContain('Jim')
    expect(
      confirmText,
      `the Remove confirm must name the workspace "${WORKSPACE}" (D4)`,
    ).toContain(WORKSPACE)

    // Cancelling is a no-op: Jim stays on the team.
    await confirm.getByRole('button', { name: /Cancel|Keep/i }).click({ timeout: 15_000 })
    await expect(confirm).toBeHidden({ timeout: 15_000 })
    await expect(
      await jimRow(page),
      'cancelling Remove must leave the team intact (D4)',
    ).toBeVisible({ timeout: 15_000 })
  })

  test('D8: the default agent cannot leave the default workspace — Remove is disabled WITH a reason', async ({ page }) => {
    // Mia is the seeded default agent (TestSeedConfig_MiaIsDefault) and the
    // existing Team panel already refuses her removal ("Cannot remove … — it
    // is the default agent", WorkspaceTeamTab.tsx). D8: the menu must show
    // the same truth — DISABLED, with the reason visible, never a silent
    // no-op.
    const row = await agentRowGroup(page, /Mia/i, WORKSPACE)
    await rowMenuButton(row, 'Mia').click({ timeout: 15_000 })

    const remove = page
      .getByRole('menu')
      .getByRole('menuitem', { name: 'Remove' })
    await expect(
      remove,
      'the default agent must not be removable from the default workspace (D8)',
    ).toBeDisabled({ timeout: 15_000 })

    const reason = await disabledReason(page, remove)
    expect(
      reason,
      'D8 forbids a silent no-op: a disabled Remove must carry a visible reason ' +
        '(title/tooltip or aria-describedby text)',
    ).toBeTruthy()
  })
})
