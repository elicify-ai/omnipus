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
      { cause: err },
    )
  }
}

// Runtime import for the Monogram escaping helper. The module already exists,
// so a static named import of a not-yet-exported symbol would fail at link
// time and drop the whole suite; load it and name the missing export instead.
async function loadAgentIconArt(): Promise<{ escapeMarkup: (s: string) => string }> {
  // The specifier is a variable (not a literal) so TypeScript resolves the
  // dynamic import as `any` — the module type would otherwise pin the shape and
  // reject the not-yet-added export at compile time. Same idiom as loadAgentIcon.
  const specifier = '@/lib/agentIconArt'
  const mod = await import(/* @vite-ignore */ specifier)
  if (typeof mod.escapeMarkup !== 'function') {
    throw new Error(
      'BLOCKED: src/lib/agentIconArt.ts exports no escapeMarkup — required by ARCH-RULING-monogram D2b / AC-19.',
    )
  }
  return mod
}

// Oracles: spec Identity / Sizes / Thinking / Working / Waiting, ARCH-DECISIONS 3.1–3.3,
// and ARCH-RULING-monogram D2b–D2e (the 5th figure, Monogram).
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
  // The 5th figure joins the four drawn figures. The art key is never supplied
  // here — the component derives it from FIGURE_ART, exactly as it does for the four.
  figure: (typeof FIGURES)[number]['figure'] | 'Monogram'
  role: 'developer' | 'security' | 'writer' | 'general' | 'researcher'
  size: (typeof SIZES)[number]
  motion?: 'none' | 'working' | 'thinking' | 'waiting'
  reducedMotion?: boolean
  /** Display name. Required by the component for every render once Monogram lands. */
  name?: string
  /** `false` renders the labelled mark (role="img"). Default is the decorative mark. */
  decorative?: boolean
}

function markFields(props: Mark) {
  return {
    figure: props.figure,
    role: props.role,
    size: props.size,
    motion: props.motion,
    reducedMotion: props.reducedMotion,
    // ARCH-RULING-monogram D2a/AC-12: `name` is now required for EVERY render,
    // not only the labelled one — Monogram derives its letter from it. The
    // decorative rows therefore still pass a name; they stay aria-hidden. The
    // four existing figure rows' assertions themselves are unchanged.
    name: props.name ?? 'Mia',
    ...(props.decorative === false ? { decorative: false as const } : {}),
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

function expectOpaqueAncestors(element: Element) {
  let effectiveOpacity = 1
  for (let ancestor: Element | null = element; ancestor; ancestor = ancestor.parentElement) {
    const computed = getComputedStyle(ancestor).opacity
    const opacity = computed === '' ? 1 : Number(computed)
    expect(opacity, `${ancestor.tagName} must not fade identity ink`).toBe(1)
    effectiveOpacity *= opacity
  }
  expect(effectiveOpacity, 'composited figure/badge opacity').toBe(1)
}

function selectedAnimationSeconds(element: Element): number {
  // Read the animation selected by the rendered component, not its source
  // text or a copied class name. Actual frame values are browser-tested.
  const selected = Array.from(element.classList).find((token) => token.startsWith('animate-['))
  expect(selected, 'rendered looping animation').toBeDefined()
  const value = selected!.slice('animate-['.length, -1)
  const duration = value.split('_').find((part) => /^\d+(?:\.\d+)?m?s$/.test(part))
  expect(duration, 'selected animation duration').toBeDefined()
  return duration!.endsWith('ms') ? Number.parseFloat(duration!) / 1000 : Number.parseFloat(duration!)
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
    expectOpaqueAncestors(ink)
    const badge = ink.querySelector('[data-role="security"]')
    expect(badge).not.toBeNull()
    expectOpaqueAncestors(badge!)
    expect(selectedAnimationSeconds(ink)).toBe(2.6)
    expect(selectedAnimationSeconds(glow!)).toBe(2.6)
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
      expectOpaqueAncestors(ink)
      const badge = ink.querySelector('[data-role="writer"]')
      expect(badge).not.toBeNull()
      expectOpaqueAncestors(badge!)
      if (motion !== 'none') {
        // Independent motion oracles: ARCH decision 3.2, spec Safeguards.
        const duration = { working: 1.6, thinking: 2.6, waiting: 3.4 }[motion]
        expect(selectedAnimationSeconds(ink)).toBe(duration)
        expect(selectedAnimationSeconds(container.querySelector('[data-glow]')!)).toBe(duration)
        if (motion === 'working') expect(selectedAnimationSeconds(container.querySelector('[data-agent-icon-sheen]')!)).toBe(2.4)
      }
      if (motion === 'none') {
        expect(container.querySelector('[data-glow]')).toBeNull()
      } else {
        expect(container.querySelector('[data-glow]')).not.toBeNull()
      }
    },
  )

  it.each([undefined, false])('selects the same 2.6-second Thinking breath and glow with reducedMotion=%s', async (reducedMotion) => {
    const AgentIcon = await loadAgentIcon()
    const { container } = render(iconOrange(AgentIcon, { figure: 'Robot', role: 'security', size: 48, motion: 'thinking', reducedMotion }))
    expect(selectedAnimationSeconds(inkOf(container))).toBe(2.6)
    expect(selectedAnimationSeconds(container.querySelector('[data-glow]')!)).toBe(2.6)
    expectOpaqueAncestors(inkOf(container))
  })

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
      iconAzure(AgentIcon, { figure: 'Robot', role: 'developer', size: 26, name: 'Mia', decorative: false }),
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

// The 5th figure, Monogram: the agent's name initial in the agent's colour.
// Oracles are the DOM seams the ruling names (ARCH-RULING-monogram D2b):
// data-figure, data-art="monogram" on the ink+glow spans, and data-initial on
// the letter <text>. Never class internals or markup snapshots.
describe('AgentIcon — Monogram (the 5th figure)', () => {
  it('draws the initial, the role badge, and the letter inside the badge mask in each layer (AC-7)', async () => {
    const AgentIcon = await loadAgentIcon()
    const { container } = render(
      iconAzure(AgentIcon, {
        figure: 'Monogram',
        role: 'developer',
        size: 40,
        motion: 'thinking',
        reducedMotion: false,
        name: 'Daniel',
      }),
    )
    expect(container.querySelector('[data-figure="Monogram"]')).not.toBeNull()
    const ink = inkOf(container)
    const glow = container.querySelector('[data-glow]') as HTMLElement
    expect(ink.getAttribute('data-art')).toBe('monogram')
    expect(glow.getAttribute('data-art')).toBe('monogram')
    expect(ink.querySelector('[data-initial]')?.getAttribute('data-initial')).toBe('D')
    // The Monogram mark carries the role badge like every other figure.
    expect(ink.querySelector('[data-role="developer"]')).not.toBeNull()
    // Position oracle: in EACH layer the letter is a descendant of the element
    // carrying the `mask` attribute, so an unmasked letter appended after the
    // badge (drawn over it) fails RED.
    for (const layer of [ink, glow]) {
      const masked = layer.querySelector('[mask]')
      expect(masked, 'mask-carrying element per layer').not.toBeNull()
      expect(masked!.querySelector('[data-initial]'), 'letter inside the masked group').not.toBeNull()
    }
  })

  // Derivation table: uppercase the first code point, then the letter/digit
  // gate (Q-F3 option A) — a first character that is not a letter or digit
  // renders '?'. ARCH-RULING-monogram D2c.
  it.each([
    ['daniel', 'D'],
    ['  anna ', 'A'],
    ['3po', '3'],
    ['_bot', '?'],
    ['', '?'],
    ['   ', '?'],
    ['🤖va', '?'],
    ['<x', '?'],
    ['&x', '?'],
    ['"x', '?'],
  ])('derives the Monogram initial of %j as %j (AC-8)', async (name, expected) => {
    const AgentIcon = await loadAgentIcon()
    const { container } = render(iconAzure(AgentIcon, { figure: 'Monogram', role: 'general', size: 40, name }))
    expect(container.querySelector('[data-initial]')?.getAttribute('data-initial')).toBe(expected)
  })

  it('carries the agent colour on the letter through currentColor, with no neutral-grey path (AC-9)', async () => {
    const AgentIcon = await loadAgentIcon()
    const { container } = render(
      iconAzure(AgentIcon, { figure: 'Monogram', role: 'general', size: 40, name: 'Daniel' }),
    )
    const mark = container.querySelector('[data-figure="Monogram"]') as Element
    // Azure is derived independently from the spec hex #3B82F6, never read off
    // the component's supplied prop.
    const channels = [1, 3, 5].map((offset) => Number.parseInt('3B82F6'.slice(offset, offset + 2), 16))
    expect(getComputedStyle(mark).color).toBe(`rgb(${channels.join(', ')})`)
    const letter = container.querySelector('[data-initial]') as Element
    expect(letter.getAttribute('fill')).toBeNull() // inherits currentColor; no fill of its own
    expect(letter.closest('svg')?.getAttribute('fill')).toBe('currentColor')
    expect(container.innerHTML).not.toContain('#9CA3AF') // the neutral grey is baked nowhere
  })

  it('hides a decorative Monogram and names a non-decorative one with the full name (AC-10)', async () => {
    const AgentIcon = await loadAgentIcon()
    const decorative = render(
      iconAzure(AgentIcon, { figure: 'Monogram', role: 'developer', size: 26, name: 'Danielle' }),
    )
    expect(decorative.container.querySelector('[aria-hidden="true"]')).not.toBeNull()
    expect(decorative.queryByRole('img')).toBeNull()
    expect(decorative.container.querySelector('[data-initial]')?.getAttribute('data-initial')).toBe('D')
    decorative.unmount()

    render(
      iconAzure(AgentIcon, { figure: 'Monogram', role: 'developer', size: 26, name: 'Danielle', decorative: false }),
    )
    expect(screen.getByRole('img', { name: 'Danielle' })).toBeInTheDocument()
    expect(screen.queryByRole('img', { name: 'D' })).toBeNull()
  })

  it('animates the Monogram letter with full figure parity and keeps the ink opaque (AC-11)', async () => {
    const AgentIcon = await loadAgentIcon()
    const { container } = render(
      iconOrange(AgentIcon, {
        figure: 'Monogram',
        role: 'security',
        size: 48,
        motion: 'thinking',
        reducedMotion: false,
        name: 'Daniel',
      }),
    )
    const ink = inkOf(container)
    const glow = container.querySelector('[data-glow]') as HTMLElement
    expect(glow).not.toBeNull()
    expect(glow.getAttribute('data-art')).toBe('monogram')
    expect(ink).toHaveStyle({ opacity: '1' })
    expectOpaqueAncestors(ink)
    expect(selectedAnimationSeconds(ink)).toBe(2.6)
    expect(selectedAnimationSeconds(glow)).toBe(2.6)
  })

  it('leaves the Monogram still under reduced motion and keeps the ink opaque (AC-11)', async () => {
    const AgentIcon = await loadAgentIcon()
    const { container } = render(
      iconCyan(AgentIcon, {
        figure: 'Monogram',
        role: 'general',
        size: 18,
        motion: 'thinking',
        reducedMotion: true,
        name: 'Daniel',
      }),
    )
    expect(inkOf(container)).toHaveStyle({ opacity: '1' })
    expect(container.innerHTML).not.toMatch(/animate-|animation\s*:/)
  })

  // User data (the agent's name) must never enter the injected markup raw:
  // '&' is escaped first so its own entity is not double-escaped. D2b/MAJ-3.
  it.each([
    ['&', '&amp;'],
    ['<', '&lt;'],
    ['>', '&gt;'],
    ['"', '&quot;'],
    ['&lt;', '&amp;lt;'],
    ['a&b<c>d"e', 'a&amp;b&lt;c&gt;d&quot;e'],
  ])('escapeMarkup maps %j to %j, escaping & first (AC-19)', async (input, expected) => {
    const { escapeMarkup } = await loadAgentIconArt()
    expect(escapeMarkup(input)).toBe(expected)
  })

  it('renders exactly one letter per layer and injects no stray elements (AC-19)', async () => {
    const AgentIcon = await loadAgentIcon()
    const { container } = render(
      iconAzure(AgentIcon, {
        figure: 'Monogram',
        role: 'general',
        size: 40,
        motion: 'thinking',
        reducedMotion: false,
        name: 'Daniel',
      }),
    )
    const ink = inkOf(container)
    const glow = container.querySelector('[data-glow]') as HTMLElement
    const allowed = new Set(['defs', 'mask', 'g', 'rect', 'circle', 'text', 'path', 'ellipse'])
    for (const layer of [ink, glow]) {
      expect(layer.querySelectorAll('[data-initial]')).toHaveLength(1)
      // Strip the letter and the badge; only the figure-mask structure may remain.
      const clone = layer.cloneNode(true) as HTMLElement
      clone.querySelectorAll('[data-initial], [data-role]').forEach((node) => node.remove())
      clone.querySelectorAll('*').forEach((el) => {
        expect(allowed.has(el.tagName.toLowerCase()), `unexpected element <${el.tagName.toLowerCase()}>`).toBe(true)
      })
    }
    expect(ink.querySelectorAll('[data-role]')).toHaveLength(1)
  })

  it('renders the Monogram letter with the pinned typography (AC-20)', async () => {
    const AgentIcon = await loadAgentIcon()
    const { container } = render(
      iconAzure(AgentIcon, { figure: 'Monogram', role: 'general', size: 40, name: 'Daniel' }),
    )
    const letter = container.querySelector('[data-initial]') as Element
    expect(letter).not.toBeNull()
    expect(letter.getAttribute('text-anchor')).toBe('middle')
    expect(letter.getAttribute('font-size')).toBe('132')
    expect(letter.getAttribute('x')).toBe('128')
    expect(letter.getAttribute('y')).toBe('128')
    expect(letter.getAttribute('dy')).toBe('0.35em')
  })
})
