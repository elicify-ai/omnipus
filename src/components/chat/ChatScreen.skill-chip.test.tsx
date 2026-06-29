// R2 chip rendering — FR-013 compliance.
// Tests the shared renderSkillAwareContent helper that is used by BOTH the live
// UserMessage and the VirtualUserMessageRow (historical/reloaded path).
//
// Coverage:
//   F1  — shared helper: chip renders identically live + on reload
//   F7  — builtin command tokens do NOT produce a chip (D3 precedence)
//   F9  — multi-line skill messages still match + show the chip
//   R2  — "skill: <name>" indicator for messages with body; "⚡ <name>" alone

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { renderSkillAwareContent } from './ChatScreen'
import type { Skill } from '@/lib/api'

// ── Fixtures ──────────────────────────────────────────────────────────────────

const SKILLS: Skill[] = [
  {
    id: 'web-research',
    name: 'Web Research',
    version: '1.0',
    description: 'Web search and extraction',
    verified: true,
    status: 'active',
  },
  {
    id: 'summarize',
    name: 'Summarize',
    version: '1.0',
    description: 'Text summarization',
    verified: true,
    status: 'active',
  },
]

// Command labels as they arrive from GET /commands?surface=web
const COMMAND_LABELS = ['/clear', '/help', '/model', '/agents', '/cancel', '/skills']

// Helper: render the content and return the container
function renderContent(
  content: string | null,
  skills = SKILLS,
  commandLabels = COMMAND_LABELS,
) {
  const fallback = () => <p data-testid="plain-text">{content}</p>
  const el = renderSkillAwareContent(content, skills, commandLabels, fallback)
  const { container } = render(<>{el}</>)
  return container
}

// ── R2 chip — message WITH body ───────────────────────────────────────────────

describe('renderSkillAwareContent — R2 chip with message body (F1/R2)', () => {
  it('renders "skill: <name>" indicator when content starts with a known skill slug', () => {
    renderContent('/web-research summarize this document')
    // The body text must appear
    expect(screen.getByText('summarize this document')).toBeInTheDocument()
    // The "skill: X" chip must appear
    expect(screen.getByText(/skill:\s*Web Research/i)).toBeInTheDocument()
    // The raw /web-research token must NOT be the visible text
    expect(screen.queryByText('/web-research')).not.toBeInTheDocument()
  })

  it('renders "skill: <name>" chip for a multi-word message body', () => {
    renderContent('/web-research find recent papers on AI safety')
    expect(screen.getByText('find recent papers on AI safety')).toBeInTheDocument()
    expect(screen.getByText(/skill:\s*Web Research/i)).toBeInTheDocument()
  })
})

// ── R2 chip — slug ONLY (no body) ─────────────────────────────────────────────

describe('renderSkillAwareContent — R2 chip slug-alone (F1/R2)', () => {
  it('renders "⚡ <name>" indicator when content is just the skill slug (no message)', () => {
    renderContent('/web-research')
    expect(screen.getByText('Web Research')).toBeInTheDocument()
    // The plain-text fallback must NOT be used (no chip test-id from the helper)
    expect(screen.queryByTestId('plain-text')).not.toBeInTheDocument()
    // The raw slug must NOT appear as visible text
    expect(screen.queryByText('/web-research')).not.toBeInTheDocument()
  })
})

// ── F9 — multi-line skill messages ────────────────────────────────────────────

describe('renderSkillAwareContent — multi-line body (F9)', () => {
  it('matches and shows chip when the body spans multiple lines', () => {
    // Without the dotAll / [\s\S]* fix, the regex would fail to match across
    // newlines and fall through to the plain-text fallback.
    const container = renderContent('/web-research first line\nsecond line')
    // The chip container must appear (not the plain-text fallback)
    expect(screen.queryByTestId('plain-text')).not.toBeInTheDocument()
    // Body text is in a <p> — use a function matcher that checks textContent
    const body = container.querySelector('p.whitespace-pre-wrap')
    expect(body).not.toBeNull()
    expect(body?.textContent).toContain('first line')
    expect(body?.textContent).toContain('second line')
    // The "skill:" chip must appear alongside the body
    expect(screen.getByText(/skill:\s*Web Research/i)).toBeInTheDocument()
  })

  it('falls back to slug-alone chip when body is only whitespace/newlines (correct F9 behavior)', () => {
    // "/summarize \n" — capture group 2 is "\n" (just whitespace).
    // The helper renders the slug-alone form: "⚡ Summarize".
    // The chip IS shown (the fix works); whether the body is blank is a
    // display detail, not a regression.
    renderContent('/summarize \n')
    // Either form of chip renders — the plain-text fallback must NOT appear
    expect(screen.queryByTestId('plain-text')).not.toBeInTheDocument()
    // The skill name appears (slug-alone form or "skill: Summarize" chip)
    expect(screen.getByText('Summarize')).toBeInTheDocument()
  })
})

// ── F7 — builtin command tokens do NOT produce a chip ─────────────────────────

describe('renderSkillAwareContent — builtin precedence (F7)', () => {
  it('renders plain text (no chip) when /clear is a known builtin', () => {
    // /clear matches the command labels list — must NOT produce a chip
    const container = renderContent('/clear')
    expect(screen.getByTestId('plain-text')).toBeInTheDocument()
    expect(container.querySelector('[class*="Lightning"]')).toBeNull()
  })

  it('renders plain text (no chip) when /help is a known builtin', () => {
    renderContent('/help me please')
    expect(screen.getByTestId('plain-text')).toBeInTheDocument()
  })

  it('renders plain text (no chip) when /agents is a known builtin', () => {
    renderContent('/agents open this')
    expect(screen.getByTestId('plain-text')).toBeInTheDocument()
  })

  it('still renders chip when /clear is NOT in the commandLabels list', () => {
    // Edge case: if commands list is empty (not yet loaded), a skill named
    // "clear" CAN produce a chip. This is acceptable — the gate only applies
    // when commands are known.
    const skillsWithClear: Skill[] = [
      { id: 'clear', name: 'Clear Skill', version: '1.0', verified: true, status: 'active' },
    ]
    renderContent('/clear', skillsWithClear, [])
    expect(screen.getByText('Clear Skill')).toBeInTheDocument()
  })
})

// ── Non-skill `/word` renders normally ────────────────────────────────────────

describe('renderSkillAwareContent — unknown /token falls back (D4)', () => {
  it('renders plain text when /token is not a skill and not a builtin', () => {
    renderContent('/zzz hello')
    expect(screen.getByTestId('plain-text')).toBeInTheDocument()
    expect(screen.queryByText(/skill:/i)).not.toBeInTheDocument()
  })

  it('renders plain text for a prefix that is not an exact slug match', () => {
    // /web is a prefix of web-research but not exact — D4 applies
    renderContent('/web do something')
    expect(screen.getByTestId('plain-text')).toBeInTheDocument()
  })

  it('renders plain text for null content', () => {
    const fallback = () => <p data-testid="plain-text">empty</p>
    const el = renderSkillAwareContent(null, SKILLS, COMMAND_LABELS, fallback)
    render(<>{el}</>)
    expect(screen.getByTestId('plain-text')).toBeInTheDocument()
  })

  it('renders plain text for regular message with no slash prefix', () => {
    renderContent('Hello world this is a normal message')
    expect(screen.getByTestId('plain-text')).toBeInTheDocument()
  })
})
