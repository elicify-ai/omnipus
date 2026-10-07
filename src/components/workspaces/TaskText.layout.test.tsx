// Oracle: T3, amended by founder steering: full wrapping is preferred; Tooltip is frozen.
// Real compiled utility rules, adapted only because jsdom does not cascade @layer.
import { expect, it, vi } from 'vitest'
import { screen, within } from '@testing-library/react'
import { compile } from '@tailwindcss/node'
import postcss from 'postcss'
import { TaskCard } from './TaskCard'
import { PlansFilterBand } from './PlansFilterBand'
import { ListView } from './ListView'
import { GraphView } from './graph/GraphView'
import { buildTaskGraph } from './graph/taskGraph'
import { layoutAgent, layoutPlan, layoutTask, renderLayout } from './tasksLayoutFixtures'

it('T3 fully wraps spaced/unbroken titles on tiles, cards, List and Graph, and reserves measured Graph heights', async () => {
  const compiler = await compile('@import "tailwindcss";', { base: process.cwd(), onDependency: () => {} })
  const style = document.createElement('style')
  const utilities: string[] = []
  postcss.parse(compiler.build(['whitespace-nowrap', 'whitespace-normal', 'max-w-full', 'w-full', 'min-w-0', 'wrap-anywhere', 'truncate', 'line-clamp-2', 'flex-1', 'block'])).walkRules((rule) => {
    if (rule.selector.startsWith('.') && !rule.selector.includes(':') && !rule.selector.includes(' ')) utilities.push(rule.toString())
  })
  style.textContent = utilities.join('\n')
  document.head.appendChild(style)
  const control = document.createElement('span')
  control.className = 'whitespace-nowrap'
  document.body.appendChild(control)
  expect(getComputedStyle(control).whiteSpace, 'the CSS instrument can detect nowrap').toBe('nowrap')
  control.remove()
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  vi.stubGlobal('DOMMatrixReadOnly', class { m22 = 1 })
  try {
    for (const title of ['Omnipus marketing + docs website in a new private GitHub repo', 'X'.repeat(200)]) {
      const mounted = renderLayout(<>
        <section aria-label="Plan titles"><PlansFilterBand plans={[layoutPlan({ title })]} tasks={[]} agents={[layoutAgent()]} selectedPlanId={null} onSelectPlan={vi.fn()} onNewPlan={vi.fn()} onEditPlan={vi.fn()} onClearPlan={vi.fn()} /></section>
        <section aria-label="Board titles"><TaskCard task={layoutTask({ title })} onClick={vi.fn()} showActions={false} /></section>
        <section aria-label="List titles"><ListView tasks={[layoutTask({ title })]} agents={[layoutAgent()]} onTaskClick={vi.fn()} /></section>
        <section aria-label="Graph titles"><GraphView tasks={[layoutTask({ title })]} agents={[]} onTaskClick={vi.fn()} /></section>
      </>)
      const plan = within(screen.getByRole('region', { name: 'Plan titles' }))
      expect(getComputedStyle(plan.getByRole('button', { name: title })).whiteSpace).toBe('normal')
      const titles = [
        plan.getByText(title),
        within(screen.getByRole('region', { name: 'Board titles' })).getByText(title),
        within(screen.getByRole('region', { name: 'List titles' })).getByRole('button', { name: `${title}, status Inbox` }),
        await within(screen.getByRole('region', { name: 'Graph titles' })).findByText(title),
      ]
      for (const element of titles) {
        expect(element.textContent).toBe(title)
        expect(getComputedStyle(element).whiteSpace).toBe('normal')
        expect(getComputedStyle(element).overflowWrap).toBe('anywhere')
        expect(getComputedStyle(element).minWidth).toBe('0px')
        expect(element).not.toHaveClass('line-clamp-2', 'truncate')
      }
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
    vi.unstubAllGlobals()
  }
})
