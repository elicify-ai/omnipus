import { describe, expect, it, vi, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { ComponentType } from 'react'

// Runtime import so Vitest collects these tests before the component exists.
// ARCH-DECISIONS 3.1 requires src/components/ui/agent-icon.tsx.
async function loadAgentIcon(): Promise<ComponentType<Record<string, unknown>>> {
  try {
    const specifier = './agent-icon'
    const mod = await import(/* @vite-ignore */ specifier)
    return mod.AgentIcon
  } catch (err) {
    throw new Error(
      `BLOCKED: src/components/ui/agent-icon.tsx is not implemented — required by FR-015 and ARCH-DECISIONS 3.1. ${err}`,
    )
  }
}

// Oracles: spec Identity / Sizes / Thinking / Working / Waiting, ARCH-DECISIONS 3.1–3.3.
// The component draws the figure it is given. It does not fetch, and it does
// not assume Omnipus when a different figure is supplied.
//
// Seam the assertions read (not a second visual language): data-art, data-role
// on the badge, data-ink on the figure+badge ink, data-glow on the separate
// glow layer. Motion classes live on the glow or the motion wrapper, never as
// an opacity below 1 on the ink.

const FIGURES = [
  { figure: 'Robot', art: 'robotSolid' },
  { figure: 'Man', art: 'man' },
  { figure: 'Woman', art: 'woman' },
  { figure: 'Omnipus', art: 'octopus' },
] as const

const SIZES = [18, 26, 40, 48] as const

afterEach(() => {
  vi.restoreAllMocks()
})

function inkOf(container: HTMLElement): HTMLElement {
  const ink = container.querySelector('[data-ink]')
  expect(ink, 'ink layer').not.toBeNull()
  return ink as HTMLElement
}

describe('AgentIcon', () => {
  it('draws the supplied figure and does not substitute Omnipus', async () => {
    const AgentIcon = await loadAgentIcon()
    for (const row of FIGURES) {
      const { container, unmount } = render(
        <AgentIcon figure={row.figure} role="developer" color="#3B82F6" size={40} />,
      )
      expect(container.querySelector('[data-art]')?.getAttribute('data-art')).toBe(row.art)
      if (row.figure !== 'Omnipus') {
        expect(container.querySelector('[data-art="octopus"]')).toBeNull()
        expect(container.innerHTML).not.toContain('octopus')
      }
      unmount()
    }
  })

  it('shows the role badge at every named size and does not fall back to a role label', async () => {
    const AgentIcon = await loadAgentIcon()
    for (const size of SIZES) {
      const { container, unmount } = render(
        <AgentIcon figure="Woman" role="developer" color="#3B82F6" size={size} />,
      )
      const badge = container.querySelector('[data-role="developer"]')
      expect(badge, `badge at ${size}px`).not.toBeNull()
      const svg = container.querySelector('svg')
      expect(svg?.getAttribute('width')).toBe(String(size))
      expect(svg?.getAttribute('height')).toBe(String(size))
      expect(screen.queryByText('Developer')).toBeNull()
      expect(screen.queryByText('developer')).toBeNull()
      unmount()
    }
  })

  it('keeps ink opacity at 1 while a separate glow layer carries thinking motion', async () => {
    const AgentIcon = await loadAgentIcon()
    const { container } = render(
      <AgentIcon figure="Robot" role="security" color="#FB923C" size={48} motion="thinking" reducedMotion={false} />,
    )
    const ink = inkOf(container)
    const glow = container.querySelector('[data-glow]')
    expect(glow, 'thinking glow is a separate layer').not.toBeNull()
    expect(glow).not.toBe(ink)
    expect(ink.getAttribute('style') ?? '').not.toMatch(/opacity\s*:\s*0/)
    expect(ink.className).not.toMatch(/opacity/)
    expect(ink.parentElement?.className ?? '').not.toMatch(/opacity/)
    expect(container.querySelector('[data-ink]')).toHaveStyle({ opacity: '1' })
  })

  it.each(['none', 'working', 'thinking', 'waiting'] as const)(
    'keeps ink opacity at 1 for motion %s',
    async (motion) => {
      const AgentIcon = await loadAgentIcon()
      const { container } = render(
        <AgentIcon figure="Man" role="writer" color="#9CA3AF" size={26} motion={motion} reducedMotion={false} />,
      )
      const ink = inkOf(container)
      expect(ink).toHaveStyle({ opacity: '1' })
      if (motion === 'none') {
        expect(container.querySelector('[data-glow]')).toBeNull()
      } else {
        expect(container.querySelector('[data-glow]')).not.toBeNull()
      }
    },
  )

  it('reduced motion removes animation loops and leaves the ink opaque', async () => {
    const AgentIcon = await loadAgentIcon()
    const { container } = render(
      <AgentIcon
        figure="Omnipus"
        role="general"
        color="#22D3EE"
        size={18}
        motion="thinking"
        reducedMotion
      />,
    )
    const ink = inkOf(container)
    expect(ink).toHaveStyle({ opacity: '1' })
    expect(container.innerHTML).not.toMatch(/animate-|animation\s*:/)
    const glow = container.querySelector('[data-glow]')
    expect(glow?.getAttribute('style') ?? '').not.toMatch(/animation/)
    expect(glow?.className ?? '').not.toMatch(/animate/)
  })

  it('hides a decorative mark and names a non-decorative mark with the agent name', async () => {
    const AgentIcon = await loadAgentIcon()
    const decorative = render(
      <AgentIcon figure="Robot" role="developer" color="#3B82F6" size={26} />,
    )
    expect(decorative.container.querySelector('[aria-hidden="true"]')).not.toBeNull()
    expect(decorative.queryByRole('img')).toBeNull()
    decorative.unmount()

    render(
      <AgentIcon figure="Robot" role="developer" color="#3B82F6" size={26} decorative={false} name="Mia" />,
    )
    expect(screen.getByRole('img', { name: 'Mia' })).toBeInTheDocument()
    expect(screen.queryByRole('img', { name: 'Developer' })).toBeNull()
    expect(screen.queryByRole('img', { name: /thinking/i })).toBeNull()
  })

  it('does not fetch an agent', async () => {
    const AgentIcon = await loadAgentIcon()
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('no network'))
    render(<AgentIcon figure="Woman" role="researcher" color="#A78BFA" size={40} motion="working" />)
    expect(fetchSpy).not.toHaveBeenCalled()
  })
})
