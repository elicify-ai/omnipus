// T5-3 / FR-070 / SC-009: actual inline links, token-sized coarse-pointer areas.
// Structural CSS proof only: physical 390px/14px-root boxes, overlap and sentence
// presentation still require the independent browser lane. No synthetic geometry.
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { parse } from 'postcss'
import type { DefaultModel } from '@/lib/api/generated/openapi-types'
import { catalogG1, jsonG1, providersG1, sectionG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'
import { compiledCssU5, inlineTouchU5 } from '../../../tests/helpers/settings-u5-css'

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

async function expectLimitsTouch(link: HTMLElement, href: string, label: string) {
  expect(link.tagName, `${label}: guidance is still a real anchor`).toBe('A')
  expect(link).toHaveAccessibleName('Set it in Limits')
  expect(link).toHaveAttribute('href', href)
  const contract = inlineTouchU5(await compiledCssU5([link]), link)
  console.info(`T5-3 ${label}: ${JSON.stringify(contract)}`)
  expect(contract.pass, `FR-070/T5-3 ${label}: actual Limits link needs both token-sized coarse dimensions on an inline box, or its OWN matching coarse pseudo hit area`).toBe(true)
}

it('T5-3 — the real default-model Set it in Limits link has a token-sized coarse touch area and exact prefill', async () => {
  const t = transportG1()
  const unknown: DefaultModel = { provider: 'openrouter', model: 'acme/m1', context_window: 0, window_unknown: true }
  t.on('GET', '/providers/default-model', () => jsonG1(unknown))
  sectionG1('providers')
  expect(await screen.findByTestId('default-model-window')).toHaveTextContent(/^No context length$/)
  const link = screen.getByTestId('default-model-window-unknown-link')
  expect(t.writes()).toEqual([])
  await expectLimitsTouch(link, '/#/settings?tab=models&provider=openrouter&model=acme%2Fm1', 'default-model card')
})

it('T5-3 — a real expanded local-model Set it in Limits link has its own coarse touch area and exact prefill', async () => {
  const t = transportG1()
  t.on('GET', '/providers', () => jsonG1([...providersG1, { ...providersG1[0],
    id: 'u5-local', name: 'U5 local', display_name: 'U5 local', backs_default: false,
  }]))
  t.on('GET', '/providers/catalog', () => jsonG1({ ...catalogG1, providers: [...catalogG1.providers, {
    ...catalogG1.providers[0], id: 'u5-local', name: 'U5 local', company: 'U5 local', locality: 'local',
    api: 'http://127.0.0.1:3210/v1',
    models: catalogG1.providers[0].models.map((model) => ({ ...model, context_window: 0, window_unknown: true })),
  }] }))
  sectionG1('providers')
  const row = await screen.findByTestId('provider-row-u5-local')
  const expand = row.querySelector<HTMLElement>('[aria-expanded="false"]')
  expect(expand, 'reachability: the real local-model disclosure exists').not.toBeNull()
  await userEvent.click(expand!)
  const link = await screen.findByTestId('model-limit-window-unknown-link-u5-local-g1-model-a')
  expect(screen.getByTestId('model-limit-window-u5-local-g1-model-a')).toHaveTextContent(/^No context length$/)
  expect(t.writes()).toEqual([])
  await expectLimitsTouch(link, '/#/settings?tab=models&provider=u5-local&model=g1-model-a', 'expanded local-model row')
})

it('T5-3 GREEN control — the actual Connect a provider action has the coarse contract and opens the real sheet', async () => {
  const t = transportG1()
  sectionG1('providers')
  const action = await screen.findByRole('button', { name: 'Connect a provider' })
  expect(inlineTouchU5(await compiledCssU5([action]), action).pass).toBe(true)
  await userEvent.click(action)
  expect(await screen.findByRole('dialog', { name: 'Connect a provider' })).toBeVisible()
  expect(t.writes()).toEqual([])
})

const token = ':root { --target-touch-minimum:44px }'
const direct = '.link { display:inline-flex } @media (pointer:coarse) { .link { min-width:var(--target-touch-minimum); min-height:var(--target-touch-minimum) } }'
const pseudo = '.link { position:relative } .link::before { content:""; position:absolute; pointer-events:auto } @media (pointer:coarse) { .link::before { width:max(100%, var(--target-touch-minimum)); height:max(100%, var(--target-touch-minimum)) } }'

it.each([
  ['direct inline box', direct, true],
  ['matching pseudo area', pseudo, true],
  ['matching pseudo with touch-token alias', ':root { --icon-target-touch:var(--target-touch-minimum) }' + pseudo.replaceAll('var(--target-touch-minimum)', 'var(--icon-target-touch)'), true],
  ['raw-pixel direct minima without token', direct.replaceAll('var(--target-touch-minimum)', '44px'), false],
  ['rem direct minima without token', direct.replaceAll('var(--target-touch-minimum)', '2.75rem'), false],
  ['wrong-token direct minima', direct.replaceAll('--target-touch-minimum', '--target-pointer-minimum'), false],
  ['inline A minima without box display', direct.replace('display:inline-flex', 'display:inline'), false],
  ['unrelated Switch pseudo', pseudo.replaceAll('.link::before', '[role="switch"][data-ds-action]::before'), false],
  ['fine-only pseudo', pseudo.replaceAll('pointer:coarse', 'pointer:fine'), false],
  ['noninteractive pseudo', pseudo.replace('pointer-events:auto', 'pointer-events:none'), false],
  ['mouse-only direct minimum', direct.replace('@media (pointer:coarse)', '@media (pointer:fine)'), false],
])('T5-3 instrument — %s is classified without borrowing another control\'s rules', (_, source, expected) => {
  render(<a href="#limits" className="link" data-testid="u5-instrument">Set it in Limits</a>)
  const contract = inlineTouchU5(parse(token + source), screen.getByTestId('u5-instrument'))
  expect(contract.pass, JSON.stringify(contract)).toBe(expected)
})

it.each([43, 44, 45])('T5-3 instrument — independent 44px minimum rejects/accepts token value %ipx exactly', (pixels) => {
  render(<a href="#limits" className="link" data-testid="u5-size">Set it in Limits</a>)
  const css = parse(`:root { --target-touch-minimum:${pixels}px }` + direct)
  expect(inlineTouchU5(css, screen.getByTestId('u5-size')).pass).toBe(pixels >= 44)
})
