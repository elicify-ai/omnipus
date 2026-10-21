import { createRequire } from 'node:module'
import { statSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { expect, test, type Page } from '@playwright/test'
import { selectAgent } from './fixtures/selectors'

// Temporary Linux crash diagnostics: remove before final verification.
const { debug } = createRequire(import.meta.url)('playwright-core/lib/utilsBundle')
debug.enable([process.env.DEBUG, 'pw:browser'].filter(Boolean).join(','))
const inheritedTmpdir = tmpdir()
console.info('[sidebar-probe] inherited tmpdir', inheritedTmpdir, 'uid', process.getuid?.(), 'mode', (statSync(inheritedTmpdir).mode & 0o7777).toString(8), 'owner', statSync(inheritedTmpdir).uid)
test.describe.configure({ retries: 0 })
test.beforeEach(async ({ page, browser }) => {
  console.info('[sidebar-probe] context ready', process.platform, browser.version(), page.url())
  page.on('crash', () => console.error('[sidebar-probe] PAGE CRASH'))
  page.on('pageerror', error => console.error('[sidebar-probe] pageerror', error.message))
  page.on('console', message => console.info('[sidebar-probe] console', message.type(), message.text()))
})

test('diagnostic control: a minimal document without sidebar code remains usable', async ({ page }) => {
  console.info('[sidebar-probe] minimal page before setContent; no sidebar code')
  await page.setContent('<!doctype html><html><body><button>Linux control</button></body></html>')
  console.info('[sidebar-probe] minimal setContent resolved')
  await expect(page.getByRole('button', { name: 'Linux control', exact: true })).toBeVisible()
})

/**
 * Real Chromium + real Playwright Locator/expect API. The external page fixture
 * implements the sidebar's documented selection/unmount contract without a
 * gateway or model. No helper, assertion API or successful result is mocked.
 */
async function mountSidebar(
  page: Page,
  selectRequestedAgent = true,
  initialAgent: 'Mia' | 'Jim' = 'Mia',
): Promise<void> {
  console.info('[sidebar-probe] before setContent')
  await page.setContent(`
    <button aria-label="Show sidebar" id="show">Show sidebar</button>
    <div id="drawer"></div>
    <script>
      console.info('[sidebar-probe] inline script entered');
      let selected = '${initialAgent}';
      let selectedWorkspace = 'My Workspace';
      const show = document.getElementById('show');
      const drawer = document.getElementById('drawer');
      document.body.dataset.selectedAgent = selected;
      document.body.dataset.selectedWorkspace = selectedWorkspace;
      function close() { drawer.replaceChildren(); show.hidden = false; }
      show.onclick = () => {
        show.hidden = true;
        drawer.innerHTML = '<div role="dialog" aria-label="Main navigation" style="width:280px;height:500px">' +
          '<nav aria-label="Main navigation">' +
          ['My Workspace', 'Other Workspace'].map(workspace =>
            '<div role="group" aria-label="' + workspace + '">' +
            '<button aria-label="Hide ' + workspace + ' agents" aria-expanded="true">Agents</button>' +
            ['Mia', 'Jim'].map(name => {
              const isSelected = workspace === selectedWorkspace && name === selected;
              return '<div role="group" aria-label="' + name + '" data-selected="' + isSelected + '">' +
                '<div class="sidebar-agent-row"><button' + (isSelected ? ' aria-current="true"' : '') +
                ' data-agent="' + name + '" data-workspace="' + workspace + '">' + name + '</button></div></div>';
            }).join('') + '</div>'
          ).join('') + '</nav></div>';
        drawer.querySelectorAll('[data-agent]').forEach(button => {
          button.onclick = () => {
            ${selectRequestedAgent ? 'selected = button.dataset.agent; selectedWorkspace = button.dataset.workspace;' : ''}
            document.body.dataset.selectedAgent = selected;
            document.body.dataset.selectedWorkspace = selectedWorkspace;
            // No exit-animation grace: the original assertion always loses.
            close();
          };
        });
      };
      document.addEventListener('click', event => {
        if (event.clientX > 280) close();
      });
      console.info('[sidebar-probe] inline script ready', document.body.children.length);
    </script>
  `)
  console.info('[sidebar-probe] setContent resolved; initial helper lookup next')
}

test.describe('selectAgent helper after immediate sidebar unmount', () => {
  test('reopens the sidebar to verify the exact selected agent and leaves it closed', async ({ page }) => {
    await mountSidebar(page)

    await selectAgent(page, /Jim/i)

    await expect(page.locator('body')).toHaveAttribute('data-selected-agent', 'Jim')
    await expect(page.locator('nav[aria-label="Main navigation"]')).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Show sidebar', exact: true })).toBeVisible()
  })

  test('still rejects when the sidebar closes but the requested agent was not selected', async ({ page }) => {
    await mountSidebar(page, false)

    await expect(selectAgent(page, /Jim/i)).rejects.toThrow(
      'selectAgent: clicked /Jim/i\'s row but neither its aria-current nor the selected row confirmed the switch',
    )
    await expect(page.locator('body')).toHaveAttribute('data-selected-agent', 'Mia')
  })

  test('selects the requested non-default workspace rather than its same-named agent in the default workspace', async ({ page }) => {
    await mountSidebar(page)

    await selectAgent(page, /Jim/i, 'Other Workspace')

    await expect(page.locator('body')).toHaveAttribute('data-selected-agent', 'Jim')
    await expect(page.locator('body')).toHaveAttribute('data-selected-workspace', 'Other Workspace')
    await expect(page.locator('nav[aria-label="Main navigation"]')).toHaveCount(0)
  })

  test('rejects a failed target-workspace selection while the default workspace\'s same-named agent remains selected', async ({ page }) => {
    await mountSidebar(page, false, 'Jim')

    await expect(selectAgent(page, /Jim/i, 'Other Workspace')).rejects.toThrow(
      'selectAgent: clicked /Jim/i\'s row but neither its aria-current nor the selected row confirmed the switch',
    )
    await expect(page.locator('body')).toHaveAttribute('data-selected-agent', 'Jim')
    await expect(page.locator('body')).toHaveAttribute('data-selected-workspace', 'My Workspace')
  })
})
