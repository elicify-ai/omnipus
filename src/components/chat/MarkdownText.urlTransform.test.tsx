/**
 * BS-1.6 / FR-002 / test 6: a valid omnipus://file href survives the chat
 * transform, and javascript: is still blanked. Finalized markdown uses that
 * transform only when chat links are opted in.
 */
import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { chatLinkUrlTransform } from '@/lib/chatLinks'
import { HistoricalMessageMarkdown } from './historical-markdown'

const OMNIPUS = 'omnipus://file/W/notes/plan.md'
const LIBRARY = '/#/library?workspace=W&path=notes%2Fplan.md'

describe('chat link url transform (BS-1.6)', () => {
  it('keeps a file href and blanks javascript and an image src', () => {
    expect(chatLinkUrlTransform(OMNIPUS, 'href')).toBe(OMNIPUS)
    expect(chatLinkUrlTransform('javascript:alert(1)', 'href')).toBe('')
    expect(chatLinkUrlTransform(OMNIPUS, 'src')).toBe('')
  })

  it('finalized chat markdown and the opted-out renderer disagree only on the Library href', () => {
    const body = `See [the plan](${OMNIPUS}).`
    const optedIn = render(<HistoricalMessageMarkdown content={body} chatLinks />)
    expect(optedIn.container.querySelector('a')?.getAttribute('href')).toBe(LIBRARY)
    optedIn.unmount()

    const plain = render(<HistoricalMessageMarkdown content={body} />)
    const library = [...plain.container.querySelectorAll('a')].filter((anchor) =>
      (anchor.getAttribute('href') ?? '').includes('/#/library'),
    )
    expect(library).toEqual([])
  })
})
