// kbMathAlreadyWorking.test.tsx — ADR-083 embedded-content spec, Step 6
// scope note (2026-09-09): "query fences, mathematics (already working)".
// This test EXISTS to prove that claim rather than merely repeat it — the
// X7 discipline this spec's own register applies elsewhere ("run every new
// test before writing any implementation; if it passes, it is not this
// work's test") applies to a claimed no-op just as much as to claimed work:
// if this failed, math would NOT be "already working" and Step 6 would need
// to actually wire it. It renders `KB_REMARK_PLUGINS`/`KB_REHYPE_PLUGINS`
// directly — the exact plugin arrays `knowledgeMarkdown.tsx`'s
// `KnowledgeBaseMarkdown` composes (see that file: "no rehype plugin is
// added, KB_REHYPE_PLUGINS is [...]" and its `remarkPlugins` spreading
// `...KB_REMARK_PLUGINS`) — rather than duplicating that whole composition,
// so a future change to knowledgeMarkdown.tsx's OWN plugin wiring cannot
// silently invalidate what this file actually measured.

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import ReactMarkdown from 'react-markdown'
import { KB_REMARK_PLUGINS, KB_REHYPE_PLUGINS } from './kbMarkdownBase'

describe('Knowledge-base markdown math (ADR-083 Step 6 scope note: "already working")', () => {
  it('renders inline math ($...$) as real KaTeX output, not literal dollar signs', () => {
    render(<ReactMarkdown remarkPlugins={KB_REMARK_PLUGINS} rehypePlugins={KB_REHYPE_PLUGINS}>{'Energy is $E = mc^2$ here.'}</ReactMarkdown>)

    // Positive: KaTeX actually ran (its own class marker on the output).
    expect(document.querySelector('.katex')).not.toBeNull()
    // Paired negative: the raw delimiter syntax is gone from the rendered
    // text — a renderer that left math untouched would still show the
    // literal dollar signs.
    expect(screen.queryByText('$E = mc^2$', { exact: false })).not.toBeInTheDocument()
  })

  it('renders block math ($$ on its own lines) as real KaTeX display output', () => {
    render(
      <ReactMarkdown remarkPlugins={KB_REMARK_PLUGINS} rehypePlugins={KB_REHYPE_PLUGINS}>
        {'$$\n\\int_0^1 x\\,dx = \\tfrac12\n$$'}
      </ReactMarkdown>,
    )

    expect(document.querySelector('.katex-display')).not.toBeNull()
    // Paired negative: the literal `$$` delimiters are gone from the
    // rendered text — KaTeX's own accessibility <annotation> legitimately
    // keeps the raw TeX source (that is not the thing being asserted here).
    expect(screen.queryByText('$$', { exact: false })).not.toBeInTheDocument()
  })

  it('leaves a dollar sign followed by whitespace alone (regression pairing: not every $ opens math)', () => {
    render(
      <ReactMarkdown remarkPlugins={KB_REMARK_PLUGINS} rehypePlugins={KB_REHYPE_PLUGINS}>
        {'This costs $ 5 in an unrelated sentence with no math at all.'}
      </ReactMarkdown>,
    )

    // remark-math requires the character immediately after an opening `$`
    // to be non-whitespace — `$ 5` cannot open a math span, so this line
    // must render as plain text, never as KaTeX output.
    expect(document.querySelector('.katex')).toBeNull()
    expect(screen.getByText(/This costs \$ 5 in an unrelated sentence/)).toBeInTheDocument()
  })
})
