// Oracle: T3, amended by founder steering: full wrapping is preferred; Tooltip is frozen.
// Real compiled utility rules, adapted only because jsdom does not cascade @layer.
import { expect, it, vi } from 'vitest'
import { screen, within, fireEvent } from '@testing-library/react'
import { compile } from '@tailwindcss/node'
import postcss from 'postcss'
import { TaskCard } from './TaskCard'
import { PlansFilterBand } from './PlansFilterBand'
import { ListView } from './ListView'
import { GraphView } from './graph/GraphView'
import { buildTaskGraph } from './graph/taskGraph'
import { layoutAgent, layoutPlan, layoutTask, renderLayout } from './tasksLayoutFixtures'

it('T3 wraps titles at word boundaries, contains long URLs, and reserves measured Graph heights', async () => {
  const compiler = await compile('@import "tailwindcss";', { base: process.cwd(), onDependency: () => {} })
  const style = document.createElement('style')
  const utilities: string[] = []
  postcss.parse(compiler.build(['whitespace-nowrap', 'whitespace-normal', 'max-w-full', 'w-full', 'min-w-0', 'wrap-anywhere', 'wrap-break-word', 'break-normal', 'hyphens-none', 'truncate', 'line-clamp-2', 'flex-1', 'block'])).walkRules((rule) => {
    if (rule.selector.startsWith('.') && !rule.selector.includes(':') && !rule.selector.includes(' ')) utilities.push(rule.toString())
  })
  style.textContent = utilities.join('\n')
  document.head.appendChild(style)
  const control = document.createElement('span')
  control.className = 'whitespace-nowrap'
  document.body.appendChild(control)
  expect(getComputedStyle(control).whiteSpace, 'the CSS instrument can detect nowrap').toBe('nowrap')
  control.remove()
  vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockReturnValue(800)
  vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(600)
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  vi.stubGlobal('DOMMatrixReadOnly', class { m22 = 1 })
  try {
    for (const title of ['Omnipus marketing + docs website in a new private GitHub repo', 'Post-publish build verification: pushed omnipus-site repo builds cleanly', 'Diagnose delegated session interruption: gateway restarted', `https://example.com/${'x'.repeat(180)}`, 'X'.repeat(200)]) {
      const mounted = renderLayout(<>
        <section aria-label="Plan titles"><PlansFilterBand plans={[layoutPlan({ title })]} tasks={[]} agents={[layoutAgent()]} selectedPlanId={null} onSelectPlan={vi.fn()} onNewPlan={vi.fn()} onEditPlan={vi.fn()} onClearPlan={vi.fn()} /></section>
        <section aria-label="Board titles"><TaskCard task={layoutTask({ title })} onClick={vi.fn()} showActions={false} /></section>
        <section aria-label="List titles"><ListView tasks={[layoutTask({ title })]} agents={[layoutAgent()]} onTaskClick={vi.fn()} /></section>
        <section aria-label="Graph titles"><GraphView tasks={[layoutTask({ title })]} agents={[]} onTaskClick={vi.fn()} /></section>
      </>)
      const plan = within(screen.getByRole('region', { name: 'Plan titles' }))
      fireEvent.click(plan.getByRole('button', { name: 'Plans' }))
      expect(getComputedStyle(plan.getByRole('button', { name: title })).whiteSpace).toBe('normal')
      const titles = [
        plan.getByText(title),
        within(screen.getByRole('region', { name: 'Board titles' })).getByText(title),
        within(screen.getByRole('region', { name: 'List titles' })).getByRole('button', { name: `${title}, status Inbox` }),
        await within(screen.getByRole('region', { name: 'Graph titles' })).findByText(title),
      ]
      for (const element of [titles[0], titles[1], titles[3]]) {
        expect(element.textContent).toBe(title)
        expect(getComputedStyle(element).whiteSpace).toBe('normal')
        expect(getComputedStyle(element).overflowWrap).toBe('break-word')
        expect(getComputedStyle(element).wordBreak).toBe('normal')
        expect(element).not.toHaveClass('break-all', 'wrap-anywhere')
        expect(getComputedStyle(element).minWidth).toBe('0px')
        expect(element).not.toHaveClass('truncate')
      }
      expect(titles[0]).not.toHaveClass('line-clamp-2') // T3 still governs plan tile wrapping.
      expect(titles[1]).toHaveClass('line-clamp-2') // T13 supersedes full task-title wrapping.
      expect(titles[3]).toHaveClass('line-clamp-2')
      expect(titles[2]).toHaveClass('truncate')
      expect(titles[2].textContent).toBe(title)
      expect(titles[1].closest('[role="button"]'), 'T15 title and info/actions share the task card without replacing its main action').toHaveAttribute('role', 'button')
      expect(titles[1]).toHaveClass('flex-1', 'max-w-full', 'min-w-0')
      expect(titles[1]).not.toHaveClass('pr-[var(--space-4)]')
      expect(screen.queryByRole('tooltip'), 'full titles do not depend on a clipped overlay').not.toBeInTheDocument()
      mounted.unmount()
      mounted.client.clear()
    }
    // First-principles geometry: the next root cannot occupy the 300px box
    // measured for a fully wrapped first card. The preserved dagre gap is 36px.
    const graph = buildTaskGraph([
      layoutTask({ id: 'long', title: 'X'.repeat(200) }),
      layoutTask({ id: 'short', title: 'Short task' }),
    ], [], {}, new Map([['long', 300], ['short', 96]]))
    const first = graph.nodes.find((node) => node.id === 'long')!
    const second = graph.nodes.find((node) => node.id === 'short')!
    expect(second.position.y - first.position.y).toBe(336)
  } finally {
    style.remove()
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  }
})
