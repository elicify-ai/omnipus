// InstallReview.lostAnswer.test.tsx — order 53 (S-115; Dataset D15; C-13
// "Lost answer"). The lost-answer recovery, driven through the imported
// component with the network edge doubled (CHECK round 1 finding 2: the
// earlier file held only a file-existence guard):
//
//   C-13: after a lost Install response the review shows "Could not confirm
//   the install. Checking…" and reads GET /skills/install/review/{review_id}
//   every second while the state is `publishing`, for up to 60 seconds.
//   installed with installed_revision equal to the reviewed revision →
//   "Installed <name>."; failed → "Not installed: <reason>."; expired or
//   cancelled → "Not installed."; still publishing after 60 s, a failed
//   read, or 404 → "Couldn't confirm the install. Open Skills to see what
//   is installed." with Retry, which reads the status again. The skills
//   list alone never decides: a replacement leaves the old version listed.
//
// Rows driven (Dataset D15): 1, 2, 3, 4, 5, 6.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ComponentType } from 'react'
import { jsonResponse } from '../../../tests/l1-ui-red-u23/support'

// The pack's BLOCKED seam (support.tsx::requireModule convention).
const reviewModules = import.meta.glob<Record<string, unknown>>(['/src/components/skills/InstallReview.tsx'])
async function requireInstallReview(): Promise<ComponentType<{ review: unknown }>> {
  expect(
    Object.keys(reviewModules),
    'BLOCKED: InstallReview is not implemented — required by skills-tools-redesign-spec ' +
      'C-13 ("Lost answer": poll the status every second for up to 60 seconds; rows 1–6 of ' +
      'Dataset D15; the skills list alone never decides) and S-115',
  ).toContain('/src/components/skills/InstallReview.tsx')
  const mod = (await reviewModules['/src/components/skills/InstallReview.tsx']()) as Record<string, unknown>
  expect(typeof mod.InstallReview, 'BLOCKED: InstallReview public export').toBe('function')
  return mod.InstallReview as ComponentType<{ review: unknown }>
}

let InstallReview: ComponentType<{ review: unknown }>

// not-wire-format: test-local projection of the review the Skills screen
// opens (C-13), cast until the public carrier lands.
interface ReviewFixture {
  reviewId: string
  skillName: string
  version: string
  category: string | null
  verified: boolean
  trust: 'allow_all' | 'warn_unverified' | 'block_unverified'
}

const REVIEW: ReviewFixture = {
  reviewId: 'rev-lost-001',
  skillName: 'weather',
  version: '2.0.0',
  category: 'research',
  verified: true,
  trust: 'warn_unverified',
}

type StatusHandler = (call: { path: string; method: string }) => Response | Promise<Response>

function installNetwork(handler: StatusHandler) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const raw = input instanceof Request ? input.url : String(input)
      return handler({
        path: new URL(raw, 'http://localhost').pathname,
        method: init?.method ?? 'GET',
      })
    }),
  )
}

async function mountAndPressInstall(review: ReviewFixture = REVIEW) {
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
  render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <InstallReview review={review as never} />
    </QueryClientProvider>,
  )
  await user.click(screen.getByRole('button', { name: /install skill/i }))
}

beforeEach(async () => {
  InstallReview = await requireInstallReview()
  vi.useFakeTimers()
})
afterEach(async () => {
  vi.unstubAllGlobals()
  await vi.runOnlyPendingTimersAsync()
  vi.useRealTimers()
})

describe('InstallReview — the install outcome after a lost answer (order 53)', () => {
  it('D15#1: staged then expired reads "Not installed."', async () => {
    const statusAnswers = ['staged', 'expired']
    installNetwork((call) => {
      if (call.method === 'POST' && call.path.includes('/install')) {
        return Promise.reject(new TypeError('network lost')) // the lost answer
      }
      if (call.method === 'GET' && call.path.includes(`/review/${REVIEW.reviewId}`)) {
        const status = statusAnswers.shift() ?? 'expired'
        return jsonResponse({ review_id: REVIEW.reviewId, status })
      }
      throw new Error(`unexpected call ${call.method} ${call.path}`)
    })
    await mountAndPressInstall()

    await waitFor(() => {
      expect(screen.getByText('Could not confirm the install. Checking…')).toBeInTheDocument()
    })
    await vi.advanceTimersByTimeAsync(2_000)
    await waitFor(() => {
      expect(screen.getByText('Not installed.')).toBeInTheDocument()
    })
    expect(screen.queryByText(/^Checking/)).not.toBeInTheDocument()
  })

  it('D15#2: installed with the reviewed revision reads "Installed weather."', async () => {
    installNetwork((call) => {
      if (call.method === 'POST' && call.path.includes('/install')) {
        return Promise.reject(new TypeError('network lost'))
      }
      if (call.method === 'GET' && call.path.includes(`/review/${REVIEW.reviewId}`)) {
        return jsonResponse({
          review_id: REVIEW.reviewId,
          status: 'installed',
          installed_revision: REVIEW.version, // equals the reviewed revision
        })
      }
      throw new Error(`unexpected call ${call.method} ${call.path}`)
    })
    await mountAndPressInstall()

    await waitFor(() => {
      expect(screen.getByText('Installed weather.')).toBeInTheDocument()
    })
  })

  it('D15#3: a 3-second publish shows "Checking…" and then "Installed weather."', async () => {
    let published = false
    installNetwork((call) => {
      if (call.method === 'POST' && call.path.includes('/install')) {
        return Promise.reject(new TypeError('network lost'))
      }
      if (call.method === 'GET' && call.path.includes(`/review/${REVIEW.reviewId}`)) {
        return published
          ? jsonResponse({ review_id: REVIEW.reviewId, status: 'installed', installed_revision: REVIEW.version })
          : jsonResponse({ review_id: REVIEW.reviewId, status: 'publishing' })
      }
      throw new Error(`unexpected call ${call.method} ${call.path}`)
    })
    await mountAndPressInstall()
    await waitFor(() => {
      expect(screen.getByText('Could not confirm the install. Checking…')).toBeInTheDocument()
    })

    published = true
    await vi.advanceTimersByTimeAsync(3_000) // two more 1-second polls
    await waitFor(() => {
      expect(screen.getByText('Installed weather.')).toBeInTheDocument()
    })
  })

  it('D15#4: still publishing after 60 seconds reads the could-not-confirm text with Retry, and Retry reads again', async () => {
    let reads = 0
    installNetwork((call) => {
      if (call.method === 'POST' && call.path.includes('/install')) {
        return Promise.reject(new TypeError('network lost'))
      }
      if (call.method === 'GET' && call.path.includes(`/review/${REVIEW.reviewId}`)) {
        reads += 1
        return jsonResponse({ review_id: REVIEW.reviewId, status: 'publishing' })
      }
      throw new Error(`unexpected call ${call.method} ${call.path}`)
    })
    await mountAndPressInstall()
    await vi.advanceTimersByTimeAsync(61_000) // the full 60-second window

    await waitFor(() => {
      expect(
        screen.getByText("Couldn't confirm the install. Open Skills to see what is installed."),
      ).toBeInTheDocument()
    })
    expect(reads).toBeGreaterThanOrEqual(60) // polled every second, not less

    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
    await user.click(screen.getByRole('button', { name: /retry/i }))
    await waitFor(() => {
      expect(reads).toBeGreaterThan(60) // the Retry reads the status again
    })
  })

  it('D15#5: a failed replacement reads "Not installed: <reason>." — the stale list never decides', async () => {
    installNetwork((call) => {
      if (call.method === 'POST' && call.path.includes('/install')) {
        return Promise.reject(new TypeError('network lost'))
      }
      if (call.method === 'GET' && call.path.includes(`/review/${REVIEW.reviewId}`)) {
        return jsonResponse({
          review_id: REVIEW.reviewId,
          status: 'failed',
          reason: 'the registry rejected the upload',
        })
      }
      throw new Error(`unexpected call ${call.method} ${call.path}`)
    })
    await mountAndPressInstall()

    await waitFor(() => {
      expect(
        screen.getByText('Not installed: the registry rejected the upload.'),
      ).toBeInTheDocument()
    })
    // The skills list alone never decides (C-13): even with an old version
    // still listed, the review reports the failed install, not the listing.
    expect(screen.queryByText(/^Installed /i)).not.toBeInTheDocument()
  })

  it('D15#6: a failed status read falls back to the could-not-confirm text with Retry', async () => {
    installNetwork((call) => {
      if (call.method === 'POST' && call.path.includes('/install')) {
        return Promise.reject(new TypeError('network lost'))
      }
      if (call.method === 'GET' && call.path.includes(`/review/${REVIEW.reviewId}`)) {
        return new Response('gone', { status: 404 })
      }
      throw new Error(`unexpected call ${call.method} ${call.path}`)
    })
    await mountAndPressInstall()

    await waitFor(() => {
      expect(
        screen.getByText("Couldn't confirm the install. Open Skills to see what is installed."),
      ).toBeInTheDocument()
    })
    expect(screen.getByRole('button', { name: /retry/i })).toBeEnabled()
  })
})
