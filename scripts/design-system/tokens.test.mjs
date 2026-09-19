import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { describe, it } from 'node:test'

import { generateArtifacts, validateTokenSources } from './tokens.mjs'

const primitive = (id, css, value, kind = 'color') => ({
  id,
  css,
  layer: 'primitive',
  kind,
  value,
})

const semantic = (id, css, ref, kind = 'color') => ({
  id,
  css,
  layer: 'semantic',
  kind,
  ref,
})

const component = (id, css, ref, kind = 'color') => ({
  id,
  css,
  layer: 'component',
  kind,
  ref,
  owner: 'Status',
  purpose: 'Render the status foreground.',
  states: ['default'],
  consumers: ['status chrome'],
})

const source = (...tokens) => ({ version: 1, tokens })

describe('token graph validation', () => {
  it('resolves a primitive to semantic to component graph', () => {
    const result = validateTokenSources([
      source(
        component('component.status.done.foreground', '--status-done-fg', 'color.status.done'),
        semantic('color.status.done', '--color-status-done', 'primitive.color.green'),
        primitive('primitive.color.green', '--primitive-color-green', '#10B981'),
      ),
    ])

    assert.deepEqual(result.resolved, {
      'color.status.done': '#10B981',
      'component.status.done.foreground': '#10B981',
      'primitive.color.green': '#10B981',
    })
  })

  it('rejects every malformed source with a path-specific diagnostic', () => {
    const cases = [
      [null, 'source[0] must be an object'],
      [{ version: 2, tokens: [] }, 'source[0].version must be 1'],
      [{ version: 1 }, 'source[0].tokens must be an array'],
      [{ version: 1, tokens: [], extra: true }, 'source[0] has unsupported property "extra"'],
      [source({}), 'source[0].tokens[0].id must be a dot-separated stable key'],
      [source(primitive('Bad key', '--good', '#fff')), 'source[0].tokens[0].id must be a dot-separated stable key'],
      [source(primitive('primitive.color.good', 'color-good', '#fff')), 'source[0].tokens[0].css must be a CSS custom property beginning with --'],
      [source({ ...primitive('primitive.color.good', '--good', '#fff'), layer: 'other' }), 'source[0].tokens[0].layer must be primitive, semantic, or component'],
      [source({ ...primitive('primitive.color.good', '--good', '#fff'), kind: 'other' }), 'source[0].tokens[0].kind is unsupported: other'],
      [source({ ...primitive('primitive.color.good', '--good', '#fff'), ref: 'primitive.color.bad' }), 'source[0].tokens[0] must define exactly one of value or ref'],
      [source({ id: 'primitive.color.good', css: '--good', layer: 'primitive', kind: 'color' }), 'source[0].tokens[0] must define exactly one of value or ref'],
      [source(primitive('primitive.color.empty', '--empty', '   ')), 'primitive token "primitive.color.empty" has an empty CSS value'],
      [source(primitive('primitive.color.nonfinite', '--nonfinite', Number.POSITIVE_INFINITY, 'number')), 'primitive token "primitive.color.nonfinite" has a non-finite numeric value'],
      [source(primitive('primitive.color.hidden-ref', '--hidden-ref', 'var(--undefined)')), 'primitive token "primitive.color.hidden-ref" must not contain var(); use ref for graph dependencies'],
    ]

    for (const [input, message] of cases) {
      assert.throws(() => validateTokenSources([input]), new Error(message))
    }
  })

  it('rejects duplicate ids and CSS custom properties across sources', () => {
    assert.throws(
      () => validateTokenSources([
        source(primitive('primitive.color.gold', '--primitive-gold', '#D4AF37')),
        source(primitive('primitive.color.gold', '--primitive-gold-2', '#D4AF37')),
      ]),
      new Error('duplicate token id "primitive.color.gold" at source[1].tokens[0]; first declared at source[0].tokens[0]'),
    )

    assert.throws(
      () => validateTokenSources([
        source(
          primitive('primitive.color.gold', '--primitive-gold', '#D4AF37'),
          primitive('primitive.color.green', '--primitive-gold', '#10B981'),
        ),
      ]),
      new Error('duplicate CSS custom property "--primitive-gold" at source[0].tokens[1]; first declared at source[0].tokens[0]'),
    )
  })

  it('rejects missing references, kind mismatches, and non-lower-layer edges', () => {
    assert.throws(
      () => validateTokenSources([source(semantic('color.status.done', '--done', 'primitive.color.missing'))]),
      new Error('token "color.status.done" references undefined token "primitive.color.missing"'),
    )

    assert.throws(
      () => validateTokenSources([source(
        primitive('primitive.duration.fast', '--fast', '100ms', 'duration'),
        semantic('color.status.done', '--done', 'primitive.duration.fast'),
      )]),
      new Error('token "color.status.done" (color) cannot reference "primitive.duration.fast" (duration)'),
    )

    assert.throws(
      () => validateTokenSources([source(
        semantic('color.status.done', '--done', 'component.status.done.foreground'),
        component('component.status.done.foreground', '--done-fg', 'color.status.done'),
      )]),
      new Error('token graph cycle: color.status.done -> component.status.done.foreground -> color.status.done'),
    )

    assert.throws(
      () => validateTokenSources([source(
        semantic('color.status.done', '--done', 'color.status.success'),
        semantic('color.status.success', '--success', 'primitive.color.green'),
        primitive('primitive.color.green', '--green', '#10B981'),
      )]),
      new Error('token "color.status.done" in semantic layer must reference a lower layer; "color.status.success" is semantic'),
    )
  })

  it('rejects incomplete component metadata and primitive references', () => {
    const incomplete = component('component.status.done.foreground', '--done-fg', 'color.status.done')
    delete incomplete.owner
    assert.throws(
      () => validateTokenSources([source(incomplete)]),
      new Error('component token "component.status.done.foreground" requires non-empty owner metadata'),
    )

    assert.throws(
      () => validateTokenSources([source({
        id: 'primitive.color.alias',
        css: '--alias',
        layer: 'primitive',
        kind: 'color',
        ref: 'primitive.color.green',
      }, primitive('primitive.color.green', '--green', '#10B981'))]),
      new Error('primitive token "primitive.color.alias" must define a raw value'),
    )
  })
})

describe('token artifact generation', () => {
  it('sorts tokens deterministically and emits exact CSS and readonly TypeScript maps', () => {
    const input = [source(
      component('component.status.done.foreground', '--status-done-fg', 'color.status.done'),
      primitive('primitive.color.green', '--primitive-color-green', '#10B981'),
      semantic('color.status.done', '--color-status-done', 'primitive.color.green'),
    )]

    const first = generateArtifacts(input)
    const second = generateArtifacts([source(...[...input[0].tokens].reverse())])

    assert.deepEqual(first, second)
    assert.equal(first.css, `/* Generated by scripts/design-system/tokens.mjs. Do not edit. */\n:root {\n  --color-status-done: var(--primitive-color-green);\n  --primitive-color-green: #10B981;\n  --status-done-fg: var(--color-status-done);\n}\n`)
    assert.equal(first.theme, `/* Generated by scripts/design-system/tokens.mjs. Do not edit. */\n@theme inline {\n  --color-status-done: var(--primitive-color-green);\n}\n`)
    assert.equal(first.typescript, `// Generated by scripts/design-system/tokens.mjs. Do not edit.\nexport const tokens = {\n  "color.status.done": "var(--color-status-done)",\n  "component.status.done.foreground": "var(--status-done-fg)",\n} as const\n\nexport const resolvedTokens = {\n  "color.status.done": "#10B981",\n  "component.status.done.foreground": "#10B981",\n  "primitive.color.green": "#10B981",\n} as const\n\nexport type TokenId = keyof typeof resolvedTokens\n`)
  })
})

// ---------------------------------------------------------------------------
// D3/D4 palette lock on the real committed source (design-system/tokens/colors.json)
//
// Every expected hex below is transcribed from the primitive and status tables
// in docs/internal/design/design-system-definition.md §D3 ("Tokens have one
// directed dependency graph": "The graph must reproduce these computed values
// exactly") and §D4 ("Status is a complete, distinguishable presentation
// contract") — never copied from the generated artifacts. Token ids and CSS
// custom-property names are pinned as the app-compat surface (they preserve
// today's application variables in src/styles/globals.css per D3's
// value-preservation mandate), so a rename surfaces here too.
// ---------------------------------------------------------------------------

const COLORS_SOURCE_PATH = new URL('../../design-system/tokens/colors.json', import.meta.url)

function loadColorsSource() {
  return JSON.parse(readFileSync(COLORS_SOURCE_PATH, 'utf8'))
}

// §D3 primitive table. Comment on each row names the spec's primitive so a
// future rename of the graph id stays reviewable against the constitution.
const D3_PRIMITIVES = [
  ['primitive.color.surface-0', '--primitive-color-surface-0', '#0A0A0B'], // surface-0 / primary — page shell
  ['primitive.color.surface-1', '--primitive-color-surface-1', '#111113'], // surface-1 — raised panel
  ['primitive.color.surface-2', '--primitive-color-surface-2', '#141416'], // surface-2 — card / composer fill
  ['primitive.color.surface-3', '--primitive-color-surface-3', '#222228'], // surface-3 — higher fill
  ['primitive.color.silver', '--primitive-color-silver', '#E2E8F0'], // secondary / text — Liquid Silver
  ['primitive.color.grey', '--primitive-color-grey', '#9CA3AF'], // muted — secondary text
  ['primitive.color.border', '--primitive-color-border', '#2D3748'], // border — default border
  ['primitive.color.gold', '--primitive-color-gold', '#D4AF37'], // accent — Forge Gold actions and live work
  ['primitive.color.gold-hover', '--primitive-color-gold-hover', '#C49E2F'], // accent-hover — gold hover
  ['primitive.color.green', '--primitive-color-green', '#10B981'], // success — done / success
  ['primitive.color.amber', '--primitive-color-amber', '#EAB308'], // warning — cancelled (D4) and warning
  ['primitive.color.red', '--primitive-color-red', '#EF4444'], // error — failed / danger
  ['primitive.color.red-hover', '--primitive-color-red-hover', '#DC2626'], // error-hover — danger hover
  ['primitive.color.blue', '--primitive-color-blue', '#3B82F6'], // info — next / information
  ['primitive.color.orange', '--primitive-color-orange', '#F97316'], // blocked / orange — blocked
  ['primitive.color.mount', '--primitive-color-mount', '#8EA3BD'], // mount — library mount icon
]

// The 14 status tint/hover primitives are the §D3 base hex with a one-byte
// alpha channel appended: 0x1A = round(255 × 0.10) is today's Tailwind
// alpha-modifier /10 tint and 0x26 = round(255 × 0.15) the /15 hover (§D4:
// "tinted task pills may stay tinted"). The bases are locked absolutely by
// D3_PRIMITIVES, so together these pin the absolute tint hexes.
const STATUS_TINTS = [
  ['primitive.color.grey-tint', 'primitive.color.grey-hover', 'primitive.color.grey'],
  ['primitive.color.blue-tint', 'primitive.color.blue-hover', 'primitive.color.blue'],
  ['primitive.color.gold-tint', 'primitive.color.gold-status-hover', 'primitive.color.gold'],
  ['primitive.color.orange-tint', 'primitive.color.orange-hover', 'primitive.color.orange'],
  ['primitive.color.green-tint', 'primitive.color.green-hover', 'primitive.color.green'],
  ['primitive.color.red-tint', 'primitive.color.red-status-hover', 'primitive.color.red'],
  ['primitive.color.amber-tint', 'primitive.color.amber-hover', 'primitive.color.amber'],
]

// Semantic layer: values derive from the §D3 "Used as" roles; CSS names are
// today's application variable names. color.cancelled is #EAB308 amber, NOT
// the legacy orange #F97316 still live in globals.css: §D4's approved
// normalization unifies cancelled onto amber and this lock guards that
// approval against an accidental revert.
const D3_SEMANTICS = [
  ['color.surface.page', '--color-primary', '#0A0A0B'], // surface-0 / primary — page shell
  ['color.surface.zero', '--color-surface-0', '#0A0A0B'],
  ['color.surface.raised', '--color-surface-1', '#111113'],
  ['color.surface.card', '--color-surface-2', '#141416'],
  ['color.surface.high', '--color-surface-3', '#222228'],
  ['color.text.primary', '--color-secondary', '#E2E8F0'], // secondary / text — Liquid Silver
  ['color.text.muted', '--color-muted', '#9CA3AF'],
  ['color.border.default', '--color-border', '#2D3748'],
  ['color.accent.default', '--color-accent', '#D4AF37'], // accent — Forge Gold
  ['color.accent.hover', '--color-accent-hover', '#C49E2F'],
  ['color.success', '--color-success', '#10B981'],
  ['color.warning', '--color-warning', '#EAB308'],
  ['color.error', '--color-error', '#EF4444'],
  ['color.error.hover', '--color-error-hover', '#DC2626'],
  ['color.info', '--color-info', '#3B82F6'],
  ['color.blocked', '--color-blocked', '#F97316'],
  ['color.cancelled', '--color-cancelled', '#EAB308'], // approved D4 normalization (amber)
  ['color.mount', '--color-mount', '#8EA3BD'],
]

// §D4 status map — the task-palette winner. cancelled = amber #EAB308 is the
// explicitly approved normalization; do not "fix" it back to orange.
const D4_STATUS = [
  ['color.status.inbox', '--color-status-inbox', '#9CA3AF'], // Grey — quiet circle
  ['color.status.next', '--color-status-next', '#3B82F6'], // Blue — ready / info
  ['color.status.in-progress', '--color-status-in-progress', '#D4AF37'], // Forge Gold — live work
  ['color.status.blocked', '--color-status-blocked', '#F97316'], // Orange — prohibit
  ['color.status.done', '--color-status-done', '#10B981'], // Green — check
  ['color.status.failed', '--color-status-failed', '#EF4444'], // Red — X
  ['color.status.cancelled', '--color-status-cancelled', '#EAB308'], // Amber — stopped by user
]

// §D4 component chrome: "Calendar chips, list cells, graph nodes, and any
// other status chrome use these hexes." Roles resolve to the state hue, its
// /10 tint (background), its /15 hover, or Deep Space Black #0A0A0B for
// filled foregrounds (color.text.on-status-fill → surface-0, §D3).
const ON_STATUS_FILL = '#0A0A0B'

function expectResolved(tokens, resolved, id) {
  const token = tokens.find((candidate) => candidate.id === id)
  if (!token) {
    throw new Error(`D3 palette violation: token "${id}" is missing from the colour graph (docs/internal/design/design-system-definition.md §D3/§D4)`)
  }
  return token
}

function checkResolvedValue(tokens, resolved, id, expected, section) {
  expectResolved(tokens, resolved, id)
  if (resolved[id] !== expected) {
    throw new Error(`D3 palette violation: token "${id}" must resolve to "${expected}" but resolves to "${resolved[id]}" (${section})`)
  }
}

function checkCssName(tokens, resolved, id, expectedCss) {
  const token = expectResolved(tokens, resolved, id)
  if (token.css !== expectedCss) {
    throw new Error(`D3 palette violation: token "${id}" must publish CSS custom property "${expectedCss}" but publishes "${token.css}" (app-compat surface)`)
  }
}

function assertD3PaletteConformance(result) {
  const { tokens, resolved } = result

  for (const [id, css, hex] of D3_PRIMITIVES) {
    checkResolvedValue(tokens, resolved, id, hex, 'docs/internal/design/design-system-definition.md §D3 primitive table')
    checkCssName(tokens, resolved, id, css)
  }

  for (const [tintId, hoverId, baseId] of STATUS_TINTS) {
    const base = resolved[baseId]
    checkResolvedValue(tokens, resolved, tintId, `${base}1A`, `Tailwind /10 alpha tint of ${baseId} (§D4 tinted pills)`)
    checkResolvedValue(tokens, resolved, hoverId, `${base}26`, `Tailwind /15 alpha hover of ${baseId} (§D4 tinted pills)`)
  }

  for (const [id, css, hex] of D3_SEMANTICS) {
    checkResolvedValue(tokens, resolved, id, hex, 'docs/internal/design/design-system-definition.md §D3 "Used as" roles')
    checkCssName(tokens, resolved, id, css)
  }

  for (const [id, css, hex] of D4_STATUS) {
    checkResolvedValue(tokens, resolved, id, hex, 'docs/internal/design/design-system-definition.md §D4 status map')
    checkCssName(tokens, resolved, id, css)
  }
  checkResolvedValue(tokens, resolved, 'color.text.on-status-fill', ON_STATUS_FILL, 'docs/internal/design/design-system-definition.md §D3 surface-0 page shell')

  for (const [stateId] of D4_STATUS) {
    const state = stateId.replace('color.status.', '')
    const hue = resolved[stateId]
    for (const role of ['foreground', 'border', 'icon', 'label', 'focus']) {
      checkResolvedValue(tokens, resolved, `component.status.${state}.${role}`, hue, `docs/internal/design/design-system-definition.md §D4 status map`)
    }
    checkResolvedValue(tokens, resolved, `component.status.${state}.background`, `${hue}1A`, 'Tailwind /10 alpha tint (§D4 tinted pills)')
    checkResolvedValue(tokens, resolved, `component.status.${state}.hover`, `${hue}26`, 'Tailwind /15 alpha hover (§D4 tinted pills)')
    checkResolvedValue(tokens, resolved, `component.status.${state}.filled-foreground`, ON_STATUS_FILL, 'docs/internal/design/design-system-definition.md §D3 surface-0 page shell')
  }
}

describe('D3/D4 palette lock on the committed colour source', () => {
  it('resolves the real colours.json to the exact §D3 primitive palette and app-compat semantic surface', () => {
    assertD3PaletteConformance(validateTokenSources([loadColorsSource()]))
  })

  it('bites: every in-memory drift mutation of the colour source is caught with an exact diagnostic', () => {
    // Each mutation keeps the graph structurally valid (existing refs, legal
    // layers, matching kinds) so validateTokenSources accepts it and the
    // palette lock itself is what fails — proving this lock, not the graph
    // validator, is the instrument that would catch the drift.
    const mutations = [
      {
        name: 'primitive hex edit',
        mutate: (colors) => {
          colors.tokens.find((token) => token.id === 'primitive.color.surface-0').value = '#0A0A0C'
        },
        message: 'D3 palette violation: token "primitive.color.surface-0" must resolve to "#0A0A0B" but resolves to "#0A0A0C" (docs/internal/design/design-system-definition.md §D3 primitive table)',
      },
      {
        name: 'semantic ref repointed to the wrong primitive',
        mutate: (colors) => {
          colors.tokens.find((token) => token.id === 'color.warning').ref = 'primitive.color.orange'
        },
        message: 'D3 palette violation: token "color.warning" must resolve to "#EAB308" but resolves to "#F97316" (docs/internal/design/design-system-definition.md §D3 "Used as" roles)',
      },
      {
        name: 'D4 cancelled repointed back to legacy orange',
        mutate: (colors) => {
          colors.tokens.find((token) => token.id === 'color.status.cancelled').ref = 'primitive.color.orange'
        },
        message: 'D3 palette violation: token "color.status.cancelled" must resolve to "#EAB308" but resolves to "#F97316" (docs/internal/design/design-system-definition.md §D4 status map)',
      },
      {
        name: 'app-compat CSS custom property renamed',
        mutate: (colors) => {
          colors.tokens.find((token) => token.id === 'color.accent.default').css = '--color-gold'
        },
        message: 'D3 palette violation: token "color.accent.default" must publish CSS custom property "--color-accent" but publishes "--color-gold" (app-compat surface)',
      },
      {
        name: 'status tint alpha changed',
        mutate: (colors) => {
          colors.tokens.find((token) => token.id === 'primitive.color.gold-tint').value = '#D4AF3733'
        },
        message: 'D3 palette violation: token "primitive.color.gold-tint" must resolve to "#D4AF371A" but resolves to "#D4AF3733" (Tailwind /10 alpha tint of primitive.color.gold (§D4 tinted pills))',
      },
      {
        name: 'component status token repointed to another state',
        mutate: (colors) => {
          colors.tokens.find((token) => token.id === 'component.status.done.foreground').ref = 'color.status.failed'
        },
        message: 'D3 palette violation: token "component.status.done.foreground" must resolve to "#10B981" but resolves to "#EF4444" (docs/internal/design/design-system-definition.md §D4 status map)',
      },
    ]

    for (const { name, mutate, message } of mutations) {
      const mutated = structuredClone(loadColorsSource())
      mutate(mutated)
      assert.throws(() => assertD3PaletteConformance(validateTokenSources([mutated])), new Error(message), `mutation "${name}" was not caught`)
    }
  })
})

describe('Badge foreground contrast', () => {
  it('keeps error text readable over the existing tint on every D3 surface', () => {
    const { resolved } = validateTokenSources([loadColorsSource()])
    const foreground = resolved['component.badge.error.foreground']
    assert.match(foreground ?? '', /^#[0-9A-F]{6}$/, 'Badge requires a resolved contrast foreground')
    assert.equal(resolved['color.error'], '#EF4444', 'the canonical error colour must not change')
    const rgb = (hex) => [1, 3, 5].map((offset) => Number.parseInt(hex.slice(offset, offset + 2), 16) / 255)
    const luminance = (channels) => channels.reduce((sum, channel, index) => sum + [0.2126, 0.7152, 0.0722][index]
      * (channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4), 0)
    // D3's four surfaces and the established 20% error tint are the oracle;
    // the label token may evolve only while retaining small-text AA contrast.
    for (const surface of ['#0A0A0B', '#111113', '#141416', '#222228']) {
      const background = rgb(surface).map((channel, index) => 0.8 * channel + 0.2 * rgb('#EF4444')[index])
      const contrast = (luminance(rgb(foreground)) + 0.05) / (luminance(background) + 0.05)
      assert.ok(contrast >= 4.5, `Badge error contrast ${contrast} on ${surface} must reach 4.5:1`)
    }
  })
})
