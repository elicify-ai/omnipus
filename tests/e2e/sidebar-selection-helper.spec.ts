import { expect, test, type Page } from '@playwright/test'
import { selectAgent } from './fixtures/selectors'

test.describe.configure({ retries: 0 })

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
  await page.setContent(`
    <button aria-label="Show sidebar" id="show">Show sidebar</button>
    <div id="drawer"></div>
    <script>
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
    </script>
  `)
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
