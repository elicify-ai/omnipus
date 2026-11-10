import { expect, type Page } from '@playwright/test'

// not-wire-format: native DOM measurements local to regression tests. No API,
// component, style, layout or browser/account state is mocked by this helper.
export type DockPanel = 'tasks' | 'calendar'
export type DockRect = { left: number; top: number; right: number; bottom: number; width: number; height: number }
export type DockSnapshot = {
  panel: DockPanel
  viewport: { width: number; height: number }
  route: string
  takeover: boolean
  frame: DockRect
  header: DockRect
  body: DockRect
  content: DockRect
  toolbar: DockRect
  detail: DockRect
  chat: DockRect
  controls: { name: string; rect: DockRect; onTop: boolean; hit: string | null }[]
}

/** Measure the REAL routed AppShell -> SidePanelShell -> registered content.
 * The content root is the actual ancestor of its semantic marker immediately
 * under the shell body, not an independently mounted screen or a CSS class.
 * Deliberate Sheet/portal editors are excluded; callers assert none is open. */
export async function readDockSnapshot(page: Page, panel: DockPanel): Promise<DockSnapshot> {
  return page.evaluate((panelId) => {
    const frame = document.querySelector<HTMLElement>('[data-testid="side-panel"]')
    const header = frame?.querySelector<HTMLElement>('[data-testid="side-panel-header"]')
    const body = header?.nextElementSibling
    const markerId = panelId === 'tasks' ? 'tasks-heading' : 'calendar-toolbar'
    const marker = body?.querySelector<HTMLElement>(`[data-testid="${markerId}"]`)
    const chat = document.querySelector<HTMLElement>('[data-testid="chat-column"]')
    if (!frame || !header || !(body instanceof HTMLElement) || !marker || !chat) {
      throw new Error(`BLOCKED: ${panelId} real shell/body/content/chat not rendered — FR-001`)
    }
    let content: HTMLElement = marker
    while (content.parentElement !== body) {
      if (!content.parentElement || !body.contains(content)) throw new Error('Content is outside its registered dock body')
      content = content.parentElement
    }
    const toolbar = panelId === 'tasks' ? marker.parentElement?.parentElement : marker
    const detail = panelId === 'tasks'
      // T12 uses the kit Accordion's h3; the always-visible header row is
      // measured even while its tiles are deliberately collapsed.
      ? content.querySelector('[aria-label="Plans filter"] h3')?.parentElement
      : content.querySelector<HTMLElement>('[data-testid="calendar-grid"]')
    if (!toolbar || !detail) throw new Error(`BLOCKED: ${panelId} real toolbar/Plans-or-grid not rendered — FR-001`)
    function rect(el: Element) {
      const r = el.getBoundingClientRect()
      return { left: r.left, top: r.top, right: r.right, bottom: r.bottom, width: r.width, height: r.height }
    }
    const controls = ['panel-close', 'panel-expand'].map((id) => {
      const button = header.querySelector<HTMLElement>(`[data-testid="${id}"]`)
      if (!button) throw new Error(`BLOCKED: shell ${id} not rendered — FR-001`)
      const r = rect(button)
      const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2)
      return {
        name: button.getAttribute('aria-label') ?? id, rect: r, onTop: hit !== null && button.contains(hit),
        hit: hit ? `${hit.tagName}[${hit.getAttribute('data-testid') ?? hit.getAttribute('aria-label') ?? ''}] ${(hit.textContent ?? '').slice(0, 100)}` : null,
      }
    })
    return {
      panel: panelId, viewport: { width: window.innerWidth, height: window.innerHeight }, route: window.location.hash,
      takeover: frame.getAttribute('data-takeover') === 'true' || frame.closest('[data-takeover="true"]') !== null,
      frame: rect(frame), header: rect(header), body: rect(body), content: rect(content),
      toolbar: rect(toolbar), detail: rect(detail), chat: rect(chat), controls,
    }
  }, panel)
}

/** Independent FR-001/FR-003 containment oracle. One CSS pixel is the maximum
 * rounding allowance for browser subpixel/border measurement, not an overflow
 * allowance. Actual rectangles and actual hit targets are always checked. */
export function dockViolations(snapshot: DockSnapshot): string[] {
  const errors: string[] = []
  const tolerance = 1
  const positive = (name: string, rect: DockRect) => {
    if (!(rect.width > 0 && rect.height > 0)) errors.push(`${name} must have non-zero native bounds: ${JSON.stringify(rect)}`)
  }
  const contained = (name: string, rect: DockRect, owner: DockRect) => {
    positive(name, rect)
    if (rect.left < owner.left - tolerance) errors.push(`${name}.left ${rect.left} < body.left ${owner.left}`)
    if (rect.top < owner.top - tolerance) errors.push(`${name}.top ${rect.top} < body.top ${owner.top}`)
    if (rect.right > owner.right + tolerance) errors.push(`${name}.right ${rect.right} > body.right ${owner.right}`)
    if (rect.bottom > owner.bottom + tolerance) errors.push(`${name}.bottom ${rect.bottom} > body.bottom ${owner.bottom}`)
  }
  positive('frame', snapshot.frame)
  positive('header', snapshot.header)
  contained('body', snapshot.body, snapshot.frame)
  if (snapshot.body.top < snapshot.header.bottom - tolerance) errors.push('dock body overlaps its own header')
  contained('content', snapshot.content, snapshot.body)
  contained('toolbar', snapshot.toolbar, snapshot.body)
  contained('Plans-or-grid', snapshot.detail, snapshot.body)
  for (const control of snapshot.controls) {
    positive(control.name, control.rect)
    if (!control.onTop) errors.push(`${control.name} is intercepted by ${control.hit}`)
  }
  if (snapshot.viewport.width >= 680) {
    positive('chat', snapshot.chat)
    if (snapshot.takeover) errors.push('desktop incorrectly took over chat')
    if (snapshot.chat.width < 360 - tolerance) errors.push(`desktop chat width ${snapshot.chat.width} < 360`)
    if (snapshot.chat.right > snapshot.frame.left + tolerance) errors.push('desktop panel frame overlaps chat')
    if (snapshot.content.left < snapshot.chat.right - tolerance) errors.push('registered content paints across chat')
  }
  return errors
}

/** Ordinary visible entry points only. Compact mode is a legitimate real UI
 * path, not a hidden/force-click escape hatch. Tasks retains its board segment. */
export async function toggleWorkspacePanel(page: Page, panel: 'tasks' | 'calendar' | 'team'): Promise<void> {
  const segment = panel === 'tasks' ? 'board' : panel
  const stripEntry = page.getByTestId(`workspace-tab-${segment}`)
  if (await stripEntry.isVisible()) {
    await stripEntry.click()
  } else {
    // R44 keeps the menu accessible name but moves its trigger onto the
    // visible workspace-name button; there is no second hamburger.
    const nameMenu = page.getByRole('button', { name: 'Open panels menu', exact: true })
    await expect(nameMenu).toHaveAttribute('data-testid', 'workspace-name-button')
    await nameMenu.click()
    await page.getByTestId(`workspace-view-switcher-${segment}`).click()
  }
}
