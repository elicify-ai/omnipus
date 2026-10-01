import { screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// Keep the production entry and boundary real. Only the router child is controlled:
// these throws must escape RouterProvider to exercise the outermost boundary.
const fixture = vi.hoisted(() => ({
  case: 'healthy' as 'healthy' | 'undefined' | 'error',
  renderError: new Error('route render failed'),
  root: null as import('react-dom/client').Root | null,
}))

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    RouterProvider: function ControlledRouterChild() {
      if (fixture.case === 'undefined') throw undefined
      if (fixture.case === 'error') throw fixture.renderError
      return <p>Healthy route content</p>
    },
  }
})

// Route definitions do not participate in the outer boundary's behaviour.
vi.mock('./routeTree.gen', async () => {
  const { createRootRoute } = await import('@tanstack/react-router')
  return { routeTree: createRootRoute() }
})

// Preserve the real React root while retaining a handle for per-test cleanup.
vi.mock('react-dom/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-dom/client')>()
  return {
    ...actual,
    createRoot: (...args: Parameters<typeof actual.createRoot>) => {
      const root = actual.createRoot(...args)
      fixture.root = root
      return root
    },
  }
})

beforeEach(() => {
  vi.resetModules()
  fixture.case = 'healthy'
  document.body.innerHTML = '<div id="root"></div>'
})

afterEach(async () => {
  const { act } = await import('react')
  await act(async () => fixture.root?.unmount())
  fixture.root = null
  document.body.replaceChildren()
})

async function mountApp(): Promise<boolean> {
  // Dynamic import mounts the real entry afresh for each independent test.
  // Record only the injected error escaping React, then assert it did not escape.
  const { act } = await import('react')
  try {
    await act(async () => { await import('./main') })
    return false
  } catch (error) {
    if (fixture.case === 'undefined' && error === undefined) return true
    if (fixture.case === 'error' && error === fixture.renderError) return true
    throw error
  }
}

describe('SPA root error boundary', () => {
  it('shows the reload screen when the child throws undefined', async () => {
    fixture.case = 'undefined'
    const escaped = await mountApp()

    expect(screen.getByText('Something went wrong loading this page.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Reload page' })).toBeInTheDocument()
    expect(escaped).toBe(false)
  })

  it('shows the reload screen when the child throws an Error', async () => {
    fixture.case = 'error'
    const escaped = await mountApp()

    expect(screen.getByText('Something went wrong loading this page.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Reload page' })).toBeInTheDocument()
    expect(escaped).toBe(false)
  })

  it('renders a healthy child without showing the fallback', async () => {
    const escaped = await mountApp()

    expect(screen.getByText('Healthy route content')).toBeInTheDocument()
    expect(escaped).toBe(false)
    expect(screen.queryByText('Something went wrong loading this page.')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Reload page' })).not.toBeInTheDocument()
  })
})
