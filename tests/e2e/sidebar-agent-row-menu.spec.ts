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
 * LANDED-STATUS (2026-10-11, second pass): frontend-lead's component and
 * backend-lead's server work are now merged on
 * work/nav-agent-row-menu-20261011 (AgentRowMenu.tsx, agentRowActions.ts,
 * WorkspaceAgentList.tsx, WorkspaceMemberConfig.hidden_from_sidebar, the D8
 * built-in-roster guard). Every locator below was re-derived from the REAL
 * component and its vitest suite (WorkspaceAgentList.rowMenu.test.tsx), not
 * from guesses: trigger "More actions for <Agent>" (AgentRowMenu.tsx —
 * matches selectors.ts::agentRowMenuButtonLabel exactly); items
 * History / Hide from sidebar / Edit / Remove in D2 order; the disabled
 * Remove's reason rides title + aria-describedby + a visible caption whose
 * span is aria-hidden so the item's accessible NAME stays exactly "Remove"
 * (AgentRowMenu.tsx D8 comment); the hidden line is the exact text
 * "<N> hidden" plus a button named exactly "Show" (WorkspaceAgentList.tsx
 * WorkspaceBlock); the Remove confirm is ConfirmDialog on the alert-dialog
 * primitive — role="alertdialog", accessible name
 * "Remove <agent> from <workspace>?", Cancel label "Cancel"
 * (confirm-dialog.tsx / alert-dialog.tsx); History opens the Sessions search
 * modal (openSearchModal → SearchModal, role="dialog"); Edit opens the
 * store-driven AgentProfile slide-over (data-testid="agent-name-input").
 *
 * SERVER BEHAVIOUR the spec now matches: hide = PUT member_configs
 * {id:{hidden_from_sidebar:true}} (agentRowActions.setHiddenFromSidebar);
 * removing a built-in from the DEFAULT workspace is refused 409
 * ("Built-in agents cannot leave the default workspace",
 * agentRowActions.removeDisabledReason branch iii) — so on the seeded
 * workspace EVERY built-in row (Mia default, Jim/Ava locked) has Remove
 * disabled, and the D4 confirm flow is exercised the only way a user can
 * reach it: a custom (non-default, non-locked) agent created via the public
 * REST API and added to the workspace team, then removed for real — which
 * also closes the D4-success gap the first pass named.
 *
 * NOT EXECUTED LOCALLY: this spec drives a real gateway + SPA (no LLM
 * needed) and runs in CI; locally it is typechecked and linted only.
 *
 * NO LLM: no test here sends a chat message or starts a turn. The D4
 * custom agent carries E2E_MODEL purely as create-contract plumbing
 * (agents.spec.ts precedent) and is never messaged; the D8 rows are the
 * built-in Mia/Jim rows, exercised without any turn.
 *
 * STATE LEAKAGE: the gateway persists across tests in a run (fresh-seeded
 * per run). The D5 test hides Jim and therefore RESTORES him at its end, so
 * later tests (and later specs on the same gateway) see the seeded roster.
 */

import { expect, type Locator, type Page } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { E2E_MODEL } from './fixtures/e2e-model'
import {
  AGENT_ROW_MENU_ITEMS,
  agentRowLocator,
  agentRowMenuButtonLabel,
  agentRowGroup,
  DEFAULT_WORKSPACE_NAME,
} from './fixtures/selectors'

const WORKSPACE = DEFAULT_WORKSPACE_NAME // oracle: ensureDefaultWorkspace seed, "My Workspace"

/** CSRF double-submit headers for `page.request` state-changing calls — the
 * same pattern as agents.spec.ts::csrfHeaders and
 * session-setup.ts::apiHeaders: `page.request` shares the browser context's
 * cookie jar (the omnipus-session auth cookie rides along) and the gateway
 * additionally requires the csrf cookie's value echoed back as X-CSRF-Token. */
async function csrfHeaders(page: Page): Promise<Record<string, string>> {
  const cookies = await page.context().cookies()
  const csrf = cookies.find((c) => c.name === 'csrf' || c.name === '__Host-csrf')
  return csrf ? { 'X-CSRF-Token': csrf.value } : {}
}

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
    // missing, renamed or reordered item all fail this one assertion. The
    // patterns, not plain strings, because on this workspace Jim's Remove is
    // DISABLED (D8) and its textContent carries the visible reason caption
    // ("Remove" + reason — the caption span is aria-hidden, so the item's
    // accessible NAME stays exactly "Remove"); /^Remove$/ would reject the
    // very reason D8 requires.
    const items = menu.getByRole('menuitem')
    await expect(items, 'menu items must match D2 exactly, in order').toHaveText([
      new RegExp(`^${AGENT_ROW_MENU_ITEMS[0]}$`),
      new RegExp(`^${AGENT_ROW_MENU_ITEMS[1]}$`),
      new RegExp(`^${AGENT_ROW_MENU_ITEMS[2]}$`),
      new RegExp(`^${AGENT_ROW_MENU_ITEMS[3]}`),
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
    // affordance. Copy confirmed against the landed component
    // (WorkspaceAgentList.tsx WorkspaceBlock): the exact text "<N> hidden"
    // and a button named exactly "Show" (exact — a substring /Show/i would
    // also catch the workspace disclosure's "Show My Workspace agents").
    const group = workspaceGroup(page)
    await expect(group.getByText('1 hidden', { exact: true })).toBeVisible({ timeout: 15_000 })
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
    await expect(workspaceGroup(page).getByText('1 hidden', { exact: true })).toBeVisible({
      timeout: 15_000,
    })

    // Show brings the agent back.
    await workspaceGroup(page).getByRole('button', { name: 'Show', exact: true }).click({ timeout: 15_000 })
    await expect(
      await jimRow(page),
      'Show must restore the hidden agent row (D5)',
    ).toBeVisible({ timeout: 15_000 })
    // And the quiet line goes away with the last hidden agent.
    await expect(workspaceGroup(page).getByText('1 hidden', { exact: true })).toHaveCount(0, {
      timeout: 15_000,
    })

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

  test('D4: Remove confirms with agent + workspace; Cancel keeps the team; confirming removes only from THIS workspace', async ({ page }) => {
    // On the seeded DEFAULT workspace every built-in row has Remove DISABLED
    // (D8: default agent / locked built-in reasons — Jim included), so the
    // confirm flow is reachable only through a removable member. This test
    // creates one the way the public API allows — POST /agents + a
    // core_team PUT (the same mutation shape the Team tab saves,
    // agentRowActions.removeWorkspaceMember) — exercises the FULL D4 flow on
    // it (cancel, then real removal), and cleans the agent up. No test-only
    // hook: every step rides a documented REST surface.
    const headers = await csrfHeaders(page)
    const agentName = `MenuRemove-${Date.now()}`

    const createResp = await page.request.post('/api/v1/agents', {
      headers,
      data: {
        name: agentName,
        soul: 'Temporary e2e agent for the D4 remove flow; never chats.',
        type: 'Main',
        // Config plumbing only — this agent never sends a chat turn; the
        // create contract needs a model (agents.spec.ts precedent).
        model: E2E_MODEL,
      },
    })
    expect(
      createResp.ok(),
      `create removable agent failed: ${createResp.status()} ${await createResp.text()}`,
    ).toBeTruthy()
    const created = (await createResp.json()) as { id?: string }
    const agentId = created.id
    expect(agentId, 'create agent returned no id').toBeTruthy()

    // Add it to the default workspace's team (revision-guarded PUT — the
    // same shape WorkspaceTeamTab saves). Read the workspace fresh so the
    // revision is current.
    const wsListResp = await page.request.get('/api/v1/workspaces', { headers })
    expect(wsListResp.ok(), 'GET /workspaces failed').toBeTruthy()
    const workspaces = (await wsListResp.json()) as Array<{
      id: string
      name?: string
      revision?: string
      core_team?: string[]
    }>
    const ws = workspaces.find((w) => w.name === WORKSPACE)
    expect(ws?.id, `no workspace named "${WORKSPACE}"`).toBeTruthy()
    const putResp = await page.request.put(`/api/v1/workspaces/${ws!.id}`, {
      headers,
      data: {
        revision: ws!.revision,
        core_team: [...(ws!.core_team ?? []), agentId],
      },
    })
    expect(
      putResp.ok(),
      `add agent to workspace team failed: ${putResp.status()} ${await putResp.text()}`,
    ).toBeTruthy()

    // Fresh roster, then the row.
    await page.reload({ waitUntil: 'domcontentloaded' })
    const row = await agentRowGroup(page, agentName, WORKSPACE)

    try {
      await rowMenuButton(row, agentName).click({ timeout: 15_000 })
      const removeItem = page
        .getByRole('menu')
        .getByRole('menuitem', { name: 'Remove' })
      // Contrast with D8: this row is neither the default agent nor a locked
      // built-in, so Remove is ENABLED (removeDisabledReason returns null).
      await expect(removeItem, 'a removable member must have Remove enabled (D4/D8)').toBeEnabled({
        timeout: 15_000,
      })
      await removeItem.click({ timeout: 15_000 })

      // D4: a CONFIRM dialog (role="alertdialog" — ConfirmDialog rides the
      // alert-dialog primitive) whose accessible NAME names BOTH the agent
      // and the workspace: "Remove <agent> from <workspace>?".
      const confirm = page.getByRole('alertdialog', {
        name: `Remove ${agentName} from ${WORKSPACE}?`,
      })
      await expect(confirm, 'Remove must ask for confirmation (D4)').toBeVisible({
        timeout: 15_000,
      })

      // Cancelling is a no-op: the row stays on the team.
      await confirm.getByRole('button', { name: 'Cancel' }).click({ timeout: 15_000 })
      await expect(confirm).toBeHidden({ timeout: 15_000 })
      await expect(row, 'cancelling Remove must leave the team intact (D4)').toBeVisible({
        timeout: 15_000,
      })

      // Confirming removes the member from THIS workspace only.
      await rowMenuButton(row, agentName).click({ timeout: 15_000 })
      await page
        .getByRole('menu')
        .getByRole('menuitem', { name: 'Remove' })
        .click({ timeout: 15_000 })
      const confirm2 = page.getByRole('alertdialog', {
        name: `Remove ${agentName} from ${WORKSPACE}?`,
      })
      await expect(confirm2).toBeVisible({ timeout: 15_000 })
      await confirm2.getByRole('button', { name: 'Remove' }).click({ timeout: 15_000 })
      await expect(confirm2).toBeHidden({ timeout: 15_000 })

      // The row leaves the roster…
      const goneRow = await agentRowLocator(page, agentName, WORKSPACE)
      await expect(goneRow, 'removing must take the agent off this workspace roster (D4)').toHaveCount(
        0,
        { timeout: 15_000 },
      )
      // …but the agent SURVIVES in the Agents list (D4: not a global delete).
      await page.goto('/#/agents')
      await expect(
        page.getByRole('button', { name: `View agent ${agentName}` }),
        'the removed agent must survive in the Agents list (D4: workspace-membership removal, not agent deletion)',
      ).toBeVisible({ timeout: 20_000 })
    } finally {
      // Cleanup is best-effort (this is a per-run fresh gateway; a leaked
      // custom agent off-team is inert) — never mask a test failure. DELETE
      // is revision-guarded, so read the agent's current revision first.
      try {
        const get = await page.request.get(`/api/v1/agents/${agentId}`, { headers })
        if (get.ok()) {
          const revision = ((await get.json()) as { revision?: string }).revision
          if (revision) {
            const del = await page.request.delete(`/api/v1/agents/${agentId}?revision=${encodeURIComponent(revision)}`, { headers })
            if (!del.ok()) {
              console.warn(`[D4] cleanup: delete ${agentName} failed: ${del.status()}`)
            }
          }
        }
      } catch (cleanupErr) {
        console.warn(`[D4] cleanup: could not delete ${agentName}: ${String(cleanupErr)}`)
      }
    }
  })

  test('D8: neither the default agent nor a locked built-in can leave the default workspace — Remove disabled WITH a reason', async ({ page }) => {
    // Confirmed against the landed removeDisabledReason
    // (agentRowActions.ts): Mia — the seeded default agent
    // (TestSeedConfig_MiaIsDefault; the Team panel refuses her too,
    // WorkspaceTeamTab::handleRemoveMember) — is disabled as "the default
    // agent"; Jim — a locked built-in on the DEFAULT workspace — is disabled
    // because the server re-adds built-ins at every boot
    // (ensureBuiltinRosterPresent) and refuses the removal 409. D8: the menu
    // shows the same truth — DISABLED, with the reason visible, never a
    // silent no-op. The reason rides title + aria-describedby + a visible
    // caption (AgentRowMenu.tsx); disabledReason reads whichever exists.
    for (const agent of ['Mia', 'Jim'] as const) {
      const row = await agentRowGroup(page, new RegExp(`^${agent}$`, 'i'), WORKSPACE)
      await rowMenuButton(row, agent).click({ timeout: 15_000 })

      const remove = page
        .getByRole('menu')
        .getByRole('menuitem', { name: 'Remove' })
      await expect(
        remove,
        `${agent} must not be removable from the default workspace (D8)`,
      ).toBeDisabled({ timeout: 15_000 })

      const reason = await disabledReason(page, remove)
      expect(
        reason,
        `D8 forbids a silent no-op: ${agent}'s disabled Remove must carry a visible ` +
          'reason (title/tooltip or aria-describedby text)',
      ).toBeTruthy()

      // Close this row's menu before the next iteration so the lookup below
      // always targets exactly one open menu.
      await page.keyboard.press('Escape')
      await expect(page.getByRole('menu')).toBeHidden({ timeout: 15_000 })
    }
  })
})
