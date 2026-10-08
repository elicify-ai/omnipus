import { describe, expect, it, vi, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { ComponentType } from 'react'
import { AgentColor } from '@/lib/api/generated/schemas'

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

// The colour attribute has to live in a named function, and the numeric
// literal has to be written in this file. The `it` callbacks below are
// anonymous, and an anonymous callback that writes
// `color={AgentColor.options[0]}` is still ts-colors/unsupported. A call
// into src/test/agentPalette.ts prints against the wrong file. Indices are
// that module's spec order: Azure 0, Cyan 2, Violet 4, Orange 8, Grey 9.
type Mark = {
  figure: (typeof FIGURES)[number]['figure']
  role: 'developer' | 'security' | 'writer' | 'general' | 'researcher'
  size: (typeof SIZES)[number]
  motion?: 'none' | 'working' | 'thinking' | 'waiting'
  reducedMotion?: boolean
  name?: string
}

function markFields(props: Mark) {
  return {
    figure: props.figure,
    role: props.role,
    size: props.size,
    motion: props.motion,
    reducedMotion: props.reducedMotion,
    ...(props.name !== undefined ? { decorative: false as const, name: props.name } : {}),
  }
}

function iconAzure(AgentIcon: ComponentType<Record<string, unknown>>, props: Mark) {
  return <AgentIcon {...markFields(props)} color={AgentColor.options[0]} />
}

function iconOrange(AgentIcon: ComponentType<Record<string, unknown>>, props: Mark) {
  return <AgentIcon {...markFields(props)} color={AgentColor.options[8]} />
}

function iconGrey(AgentIcon: ComponentType<Record<string, unknown>>, props: Mark) {
  return <AgentIcon {...markFields(props)} color={AgentColor.options[9]} />
}

function iconCyan(AgentIcon: ComponentType<Record<string, unknown>>, props: Mark) {
  return <AgentIcon {...markFields(props)} color={AgentColor.options[2]} />
}

function iconViolet(AgentIcon: ComponentType<Record<string, unknown>>, props: Mark) {
  return <AgentIcon {...markFields(props)} color={AgentColor.options[4]} />
}

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
        iconAzure(AgentIcon, { figure: row.figure, role: 'developer', size: 40 }),
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
        iconAzure(AgentIcon, { figure: 'Woman', role: 'developer', size }),
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
      iconOrange(AgentIcon, { figure: 'Robot', role: 'security', size: 48, motion: 'thinking', reducedMotion: false }),
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
        iconGrey(AgentIcon, { figure: 'Man', role: 'writer', size: 26, motion, reducedMotion: false }),
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
      iconCyan(AgentIcon, { figure: 'Omnipus', role: 'general', size: 18, motion: 'thinking', reducedMotion: true }),
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
      iconAzure(AgentIcon, { figure: 'Robot', role: 'developer', size: 26 }),
    )
    expect(decorative.container.querySelector('[aria-hidden="true"]')).not.toBeNull()
    expect(decorative.queryByRole('img')).toBeNull()
    decorative.unmount()

    render(
      iconAzure(AgentIcon, { figure: 'Robot', role: 'developer', size: 26, name: 'Mia' }),
    )
    expect(screen.getByRole('img', { name: 'Mia' })).toBeInTheDocument()
    expect(screen.queryByRole('img', { name: 'Developer' })).toBeNull()
    expect(screen.queryByRole('img', { name: /thinking/i })).toBeNull()
  })

  it('does not fetch an agent', async () => {
    const AgentIcon = await loadAgentIcon()
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('no network'))
    render(iconViolet(AgentIcon, { figure: 'Woman', role: 'researcher', size: 40, motion: 'working' }))
    expect(fetchSpy).not.toHaveBeenCalled()
  })
})
