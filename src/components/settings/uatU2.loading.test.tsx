// U2-3 oracle: settings-redesign-spec.md::reduced-motion scenario (US-9).
// Hold the two named HTTP reads, not the already-fixed shared section loaders.
// DOM + shipped CSS stop contracts only; jsdom cannot measure opacity or rotation.
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, screen, waitFor } from '@testing-library/react'
import { heldG1, jsonG1, sectionG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'
import { reducedMotionU2 } from '../../../tests/helpers/settings-u2-css'

function preferReducedMotionU2() {
  vi.stubGlobal('matchMedia', (media: string) => ({
    matches: media === '(prefers-reduced-motion: reduce)', media, onchange: null,
    addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {},
    dispatchEvent: () => false,
  }))
}

async function expectNoLoadingMotionU2(indicator: Element, name: string) {
  expect(await reducedMotionU2(indicator),
    `U2-3/US-9: ${name} must be static or ship a reduced-motion animation stop; classes=${indicator.getAttribute('class')}`).toBe(true)
}

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

it('U2-3 — reduced motion stops the Providers default-model loading bar', async () => {
  const t = transportG1()
  const pending = heldG1()
  t.on('GET', '/providers/default-model', () => pending.promise)
  preferReducedMotionU2()
  sectionG1('providers')
  try {
    const bar = await screen.findByTestId('default-model-loading')
    expect(t.calls.filter((call) => call.method === 'GET' && call.path === '/providers/default-model')).toHaveLength(1)
    expect(screen.queryByTestId('default-model-window'), 'instrument: the default-model read is still pending').not.toBeInTheDocument()
    await expectNoLoadingMotionU2(bar, 'default-model-loading bar')
  } finally {
    pending.resolve(jsonG1(t.defaults['/providers/default-model']))
  }
})

it('U2-3 — reduced motion stops the Sandbox Bash HTTP Proxy refresh spinner', async () => {
  const t = transportG1()
  const pending = heldG1()
  t.on('GET', '/security/exec-proxy-status', () => pending.promise)
  preferReducedMotionU2()
  sectionG1('sandbox')
  try {
    await screen.findByText('Bash HTTP Proxy')
    await screen.findByText('Checking...')
    const refresh = screen.getByRole('button', { name: 'Refresh exec proxy status' })
    expect(refresh, 'instrument: the named proxy read, not the section loader, is pending').toBeDisabled()
    await waitFor(() => expect(t.calls.filter((call) => call.method === 'GET' && call.path === '/security/exec-proxy-status')).toHaveLength(1))
    const spinner = refresh.querySelector('svg')
    expect(spinner, 'instrument: the real refresh control supplies its spinner').not.toBeNull()
    await expectNoLoadingMotionU2(spinner!, 'Bash HTTP Proxy refresh spinner')
  } finally {
    pending.resolve(jsonG1({ enabled: false, running: false }))
  }
})
