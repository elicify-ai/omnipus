import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchDoctorResults: vi.fn(),
    runDoctor: vi.fn(),
  }
})

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast: vi.fn() })),
}))

import { fetchDoctorResults, runDoctor } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { DiagnosticsSection } from './DiagnosticsSection'
import type { DoctorResult } from '@/lib/api'

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function renderSection() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <DiagnosticsSection />
    </QueryClientProvider>
  )
}

const mockAddToast = vi.fn()

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(useUiStore).mockReturnValue({ addToast: mockAddToast } as never)
})

// =====================================================================
// Scenario: Empty state — no diagnostics run yet
// =====================================================================

describe('DiagnosticsSection — empty state', () => {
  it('shows empty state message when no diagnostics have been run', async () => {
    vi.mocked(fetchDoctorResults).mockResolvedValue(null)

    renderSection()

    await waitFor(() => {
      // US-B4: updated copy "No security scan yet"
      expect(screen.getByText(/no security scan yet/i)).toBeInTheDocument()
    })
  })

  it('shows check-now button in all states', async () => {
    vi.mocked(fetchDoctorResults).mockResolvedValue(null)

    renderSection()

    // US-B4: button now says "Check now" (plain language)
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /check now/i })).toBeInTheDocument()
    })
  })
})

// =====================================================================
// TestDiagnostics_ScoreDisplay_HigherIsBetter
// =====================================================================

describe('TestDiagnostics_ScoreDisplay_HigherIsBetter', () => {
  it('renders score 90 as 90/100 Excellent with success color', async () => {
    vi.mocked(fetchDoctorResults).mockResolvedValue({
      score: 90,
      issues: [],
      checked_at: '2026-01-01T00:00:00Z',
    })

    renderSection()

    await waitFor(() => {
      expect(screen.getByText('90')).toBeInTheDocument()
      expect(screen.getByText('/100')).toBeInTheDocument()
      expect(screen.getAllByText('Excellent').length).toBeGreaterThan(0)
    })

    const label = screen.getByText('Excellent', {
      selector: 'span.text-xs',
    })
    expect(label).toHaveStyle({ color: 'var(--color-success)' })
  })
})

// =====================================================================
// TestDiagnostics_ScoreLabel_ByBucket
// =====================================================================

describe('TestDiagnostics_ScoreLabel_ByBucket', () => {
  it.each([
    { score: 100, expectedLabel: 'Excellent', colorVar: '--color-success' },
    { score: 90, expectedLabel: 'Excellent', colorVar: '--color-success' },
    { score: 89, expectedLabel: 'Good', colorVar: '--color-success' },
    { score: 67, expectedLabel: 'Good', colorVar: '--color-success' },
    { score: 66, expectedLabel: 'At risk', colorVar: '--color-warning' },
    { score: 34, expectedLabel: 'At risk', colorVar: '--color-warning' },
    { score: 33, expectedLabel: 'Critical', colorVar: '--color-error' },
    { score: 0, expectedLabel: 'Critical', colorVar: '--color-error' },
  ])('score=$score → $expectedLabel ($colorVar)', async ({ score, expectedLabel, colorVar }) => {
    const client = makeClient()
    vi.mocked(fetchDoctorResults).mockResolvedValue({
      score,
      issues: [],
      checked_at: '2026-01-01T00:00:00Z',
    })

    const { unmount } = render(
      <QueryClientProvider client={client}>
        <DiagnosticsSection />
      </QueryClientProvider>
    )

    await waitFor(() => {
      expect(screen.getAllByText(expectedLabel).length).toBeGreaterThan(0)
    })

    const label = screen.getAllByText(expectedLabel)[0]
    expect(label).toHaveStyle({ color: `var(${colorVar})` })

    unmount()
  })
})

// =====================================================================
// TestDiagnostics_ToastAfterRun
// =====================================================================

describe('TestDiagnostics_ToastAfterRun', () => {
  it('shows toast containing "security score: 90/100" after run completes', async () => {
    vi.mocked(fetchDoctorResults).mockResolvedValue(null)
    vi.mocked(runDoctor).mockResolvedValue({
      score: 90,
      issues: [],
      checked_at: new Date().toISOString(),
    })

    renderSection()

    await waitFor(() => screen.getByRole('button', { name: /check now/i }))
    fireEvent.click(screen.getByRole('button', { name: /check now/i }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({
          message: expect.stringContaining('security score: 90/100'),
        })
      )
    })
  })
})

// =====================================================================
// TestDiagnostics_ProgressBarWidth
// =====================================================================

describe('TestDiagnostics_ProgressBarWidth', () => {
  it('progress bar width is 75% when score is 75', async () => {
    vi.mocked(fetchDoctorResults).mockResolvedValue({
      score: 75,
      issues: [],
      checked_at: '2026-01-01T00:00:00Z',
    })

    renderSection()

    await waitFor(() => {
      const bar = screen.getByTestId('progress-bar')
      expect(bar).toHaveStyle({ width: '75%' })
    })
  })
})

// =====================================================================
// Scenario: Display last results
// =====================================================================

describe('DiagnosticsSection — results display', () => {
  const fullResult: DoctorResult = {
    score: 42,
    checked_at: '2026-03-29T10:00:00Z',
    issues: [
      {
        id: 'issue-high-1',
        severity: 'high',
        title: 'Landlock not enabled',
        description: 'Kernel filesystem sandbox is disabled.',
        recommendation: 'Enable Landlock in your kernel configuration.',
        action_link: 'https://docs.omnipus.ai/security',
        action_label: 'Learn more',
      },
      {
        id: 'issue-med-1',
        severity: 'medium',
        title: 'seccomp filter loose',
        description: 'The seccomp filter allows some risky syscalls.',
        recommendation: 'Tighten the seccomp profile.',
      },
      {
        id: 'issue-low-1',
        severity: 'low',
        title: 'Audit log rotation not configured',
        description: 'Logs may grow unbounded.',
        recommendation: 'Configure log rotation.',
      },
    ],
  }

  it('renders security score when results are loaded', async () => {
    vi.mocked(fetchDoctorResults).mockResolvedValue(fullResult)

    renderSection()

    await waitFor(() => {
      expect(screen.getByText('42')).toBeInTheDocument()
    })
  })

  it('displays the last checked timestamp', async () => {
    vi.mocked(fetchDoctorResults).mockResolvedValue(fullResult)

    renderSection()

    // US-B4: updated label "Last checked:" (was "Last run:")
    await waitFor(() => {
      expect(screen.getByText(/last checked:/i)).toBeInTheDocument()
    })
  })

  it('groups issues by severity: high, medium, low', async () => {
    vi.mocked(fetchDoctorResults).mockResolvedValue(fullResult)

    renderSection()

    await waitFor(() => {
      expect(screen.getByText(/high — 1 issue/i)).toBeInTheDocument()
      expect(screen.getByText(/medium — 1 issue/i)).toBeInTheDocument()
      expect(screen.getByText(/low — 1 issue/i)).toBeInTheDocument()
    })
  })

  it('displays issue titles in the issues list', async () => {
    vi.mocked(fetchDoctorResults).mockResolvedValue(fullResult)

    renderSection()

    await waitFor(() => {
      expect(screen.getByText('Landlock not enabled')).toBeInTheDocument()
      expect(screen.getByText('seccomp filter loose')).toBeInTheDocument()
      expect(screen.getByText('Audit log rotation not configured')).toBeInTheDocument()
    })
  })

  it('shows all clear message when no issues (score=100)', async () => {
    vi.mocked(fetchDoctorResults).mockResolvedValue({
      score: 100,
      issues: [],
      checked_at: '2026-01-01T00:00:00Z',
    })

    renderSection()

    // US-B4: all-clear panel content
    await waitFor(() => {
      expect(screen.getByTestId('all-clear-panel')).toBeInTheDocument()
    })
    expect(screen.getByTestId('all-clear-panel')).toHaveTextContent(/you're protected/i)
  })

  it('card title reads "Security Score"', async () => {
    vi.mocked(fetchDoctorResults).mockResolvedValue(fullResult)

    renderSection()

    await waitFor(() => {
      expect(screen.getByText('Security Score')).toBeInTheDocument()
    })
  })

  // US-B4: reassurance message
  it('shows reassurance with issue count when issues exist', async () => {
    vi.mocked(fetchDoctorResults).mockResolvedValue(fullResult)

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('score-reassurance')).toHaveTextContent(/3 things could be stronger/i)
    })
  })

  it('shows "protected" reassurance with score=100 and no issues', async () => {
    vi.mocked(fetchDoctorResults).mockResolvedValue({
      score: 100,
      issues: [],
      checked_at: '2026-01-01T00:00:00Z',
    })

    renderSection()

    await waitFor(() => {
      expect(screen.getByTestId('score-reassurance')).toHaveTextContent(/you're protected\./i)
    })
  })
})

// =====================================================================
// Scenario: Issue card expand/collapse
// =====================================================================

describe('DiagnosticsSection — issue card interaction', () => {
  const resultWithIssue: DoctorResult = {
    score: 60,
    checked_at: '2026-03-29T12:00:00Z',
    issues: [
      {
        id: 'landlock-disabled',
        severity: 'high',
        title: 'Landlock not enabled',
        description: 'Kernel filesystem sandbox is disabled.',
        recommendation: 'Enable Landlock in kernel config.',
        action_link: 'https://docs.omnipus.ai/landlock',
        action_label: 'Enable Landlock',
      },
    ],
  }

  it('clicking an issue card expands it to show description and recommendation', async () => {
    vi.mocked(fetchDoctorResults).mockResolvedValue(resultWithIssue)

    renderSection()

    await waitFor(() => screen.getByText('Landlock not enabled'))

    expect(screen.queryByText(/kernel filesystem sandbox is disabled/i)).not.toBeInTheDocument()

    fireEvent.click(screen.getByText('Landlock not enabled'))

    await waitFor(() => {
      expect(screen.getByText(/kernel filesystem sandbox is disabled/i)).toBeInTheDocument()
      expect(screen.getByText(/enable landlock in kernel config/i)).toBeInTheDocument()
    })
  })

  it('shows action link when present', async () => {
    vi.mocked(fetchDoctorResults).mockResolvedValue(resultWithIssue)

    renderSection()

    await waitFor(() => screen.getByText('Landlock not enabled'))
    fireEvent.click(screen.getByText('Landlock not enabled'))

    await waitFor(() => {
      expect(screen.getByRole('link', { name: /enable landlock/i })).toBeInTheDocument()
    })
  })

  it('clicking expanded card collapses it', async () => {
    vi.mocked(fetchDoctorResults).mockResolvedValue(resultWithIssue)

    renderSection()

    await waitFor(() => screen.getByText('Landlock not enabled'))

    fireEvent.click(screen.getByText('Landlock not enabled'))
    await waitFor(() => screen.getByText(/kernel filesystem sandbox is disabled/i))

    fireEvent.click(screen.getByText('Landlock not enabled'))
    await waitFor(() => {
      expect(screen.queryByText(/kernel filesystem sandbox is disabled/i)).not.toBeInTheDocument()
    })
  })

  // US-B4: action link navigates to in-place fix (data-testid)
  it('action link has correct href for in-place fix navigation', async () => {
    vi.mocked(fetchDoctorResults).mockResolvedValue(resultWithIssue)

    renderSection()

    await waitFor(() => screen.getByText('Landlock not enabled'))
    fireEvent.click(screen.getByText('Landlock not enabled'))

    await waitFor(() => {
      const link = screen.getByTestId('issue-action-link-landlock-disabled')
      expect(link).toHaveAttribute('href', 'https://docs.omnipus.ai/landlock')
      expect(link).toHaveTextContent(/enable landlock/i)
    })
  })
})

// =====================================================================
// Scenario: Run diagnostics button (US-B4: "Check now" label)
// =====================================================================

describe('DiagnosticsSection — run diagnostics', () => {
  it('clicking "Check now" calls runDoctor API', async () => {
    vi.mocked(fetchDoctorResults).mockResolvedValue(null)
    vi.mocked(runDoctor).mockResolvedValue({
      score: 85,
      issues: [],
      checked_at: new Date().toISOString(),
    })

    renderSection()

    await waitFor(() => screen.getByRole('button', { name: /check now/i }))

    fireEvent.click(screen.getByRole('button', { name: /check now/i }))

    await waitFor(() => {
      expect(runDoctor).toHaveBeenCalledOnce()
    })
  })

  it('shows toast with score after successful run', async () => {
    vi.mocked(fetchDoctorResults).mockResolvedValue(null)
    vi.mocked(runDoctor).mockResolvedValue({
      score: 25,
      issues: [],
      checked_at: new Date().toISOString(),
    })

    renderSection()

    await waitFor(() => screen.getByRole('button', { name: /check now/i }))
    fireEvent.click(screen.getByRole('button', { name: /check now/i }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({ message: expect.stringContaining('25') })
      )
    })
  })

  it('updates displayed results after run completes', async () => {
    const newResult: DoctorResult = {
      score: 80,
      issues: [],
      checked_at: new Date().toISOString(),
    }

    vi.mocked(fetchDoctorResults).mockResolvedValue(null)
    vi.mocked(runDoctor).mockResolvedValue(newResult)

    renderSection()

    await waitFor(() => screen.getByRole('button', { name: /check now/i }))
    fireEvent.click(screen.getByRole('button', { name: /check now/i }))

    await waitFor(() => {
      expect(screen.getByText('80')).toBeInTheDocument()
    })
  })

  it('shows error toast if run diagnostics fails', async () => {
    vi.mocked(fetchDoctorResults).mockResolvedValue(null)
    vi.mocked(runDoctor).mockRejectedValue(new Error('doctor unavailable'))

    renderSection()

    await waitFor(() => screen.getByRole('button', { name: /check now/i }))
    fireEvent.click(screen.getByRole('button', { name: /check now/i }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({ message: 'doctor unavailable', variant: 'error' })
      )
    })
  })

  it('"Check now" button is disabled while running', async () => {
    vi.mocked(fetchDoctorResults).mockResolvedValue(null)
    vi.mocked(runDoctor).mockReturnValue(new Promise(() => {}))

    renderSection()

    await waitFor(() => screen.getByRole('button', { name: /check now/i }))
    fireEvent.click(screen.getByRole('button', { name: /check now/i }))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /checking/i })).toBeDisabled()
    })
  })
})
