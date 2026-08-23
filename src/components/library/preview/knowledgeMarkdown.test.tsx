/**
 * knowledgeMarkdown.tsx — the KNOWLEDGE-BASE markdown composition.
 *
 * ADR-067 test 85 (chat still shows `%%secret%%`), test 86 (chat's composition
 * is unchanged, behaviourally), test 87 (the KB composition inherits the shared
 * renderers and diverges in exactly two places): FR-011, FR-013a/b/c/d, FR-060,
 * FR-061, FR-065, US-7 AS-1..AS-4, AS-8, US-10 AS-1/AS-2.
 *
 * react-markdown, remark's real parser, remark-gfm and every plugin under test
 * stay REAL — that is the point: a plugin has to run against a genuinely parsed
 * mdast, and the component overrides have to fire on genuinely parsed nodes.
 * Only leaf dependencies (Shiki's WASM tokenizer, Mermaid, the lightbox, the
 * preview shell) are stubbed, and each stub is a SENTINEL that proves which
 * branch was taken rather than a blanket silencer.
 *
 * Every `describe` block names the mutation its assertions die on.
 */

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import remarkGfm from 'remark-gfm'
import remarkMath from 'remark-math'
import rehypeKatex from 'rehype-katex'
import { rehypePhosphorEmoji } from '@/lib/rehype-phosphor-emoji'
import { commonMarkdownComponents } from '@/components/chat/markdown-shared'
import { HistoricalMessageMarkdown } from '@/components/chat/historical-markdown'
import { kbMarkdownComponents, KB_REMARK_PLUGINS } from './LibraryMarkdownPreview'
import {
  KnowledgeBaseMarkdown,
  KB_BASE_REMARK_PLUGINS,
  KB_REHYPE_PLUGINS,
  knowledgeMarkdownComponents,
  parseWikilink,
  remarkKbCallouts,
  remarkKbFrontmatter,
  remarkKbHighlights,
  remarkKbWikilinks,
  resolveCollectionPath,
} from './knowledgeMarkdown'

// Sentinels — each proves a specific branch ran.
vi.mock('@/components/chat/mermaid-renderer', () => ({
  MermaidDiagram: ({ code }: { code: string }) => <div data-testid="mermaid-diagram">{code}</div>,
}))
vi.mock('react-shiki', () => ({
  ShikiHighlighter: ({ children }: { children?: React.ReactNode }) => <pre data-testid="shiki">{children}</pre>,
}))
vi.mock('@/components/chat/ChatImage', () => ({
  ChatImage: ({ src, alt }: { src: string; alt?: string }) => <img data-testid="chat-image" src={src} alt={alt} />,
}))
vi.mock('@/store/ui', () => ({ useUiStore: { getState: () => ({ addToast: vi.fn() }) } }))
vi.mock('@tanstack/react-query', () => ({ useQuery: () => ({ data: null }) }))

// The view shell is replaced by a pass-through — LibraryTextPreview /
// LibraryPreviewPane are being changed concurrently by other work, and churn
// there must not read as a markdown failure here.
vi.mock('./LibraryTextPreview', () => ({
  LibraryTextPreview: ({
    content,
    renderView,
  }: {
    content: string
    renderView: (draft: string) => React.ReactNode
  }) => <div data-testid="text-preview-shell">{renderView(content)}</div>,
}))

/** Chat's remark list, as `historical-markdown.tsx` writes it literally. Kept
 *  here as the BASELINE the KB list is diffed against; the behavioural guard
 *  that chat did not silently gain a KB plugin is the "chat is unchanged"
 *  block below, which renders through chat's real component. */
const CHAT_REMARK_PLUGINS = [remarkGfm, remarkMath]

function bodyText(): string {
  return document.body.textContent ?? ''
}

// ─────────────────────────────────────────────────────────────────────────────

describe('chat is unchanged (FR-013d, FR-011 — spec tests 85 and 86)', () => {
  // DIES ON: adding any KB remark plugin to chat's list in
  // historical-markdown.tsx, or pointing chat's `a` slot at the KB renderer.
  // There is NO compiler check for either.
  //
  // ⚠ SCOPE. Every assertion in this block renders through
  // HistoricalMessageMarkdown — chat's FINALIZED-message renderer. Chat has a
  // SECOND composition, `chat/markdown-text.tsx` (the live/streaming one),
  // with its own literal `remarkPlugins`, its own `rehypePlugins` and its own
  // components map. Nothing here reaches it, so this block was only ever half
  // a guard; the `markdown-text.tsx` describe below covers the other half.

  it('renders %%secret%% literally — markers and content (test 85)', () => {
    render(<HistoricalMessageMarkdown content={'before %%secret%% after'} />)
    // Chat carries untrusted model and tool output. Silently deleting the text
    // between two markers hides content FROM the reader instead of protecting
    // them, so the KB strip must never reach here.
    expect(bodyText()).toContain('%%secret%%')
  })

  it('renders a wikilink as literal text, not a link', () => {
    render(<HistoricalMessageMarkdown content={'see [[Note|alias]] here'} />)
    expect(bodyText()).toContain('[[Note|alias]]')
    expect(document.querySelectorAll('a')).toHaveLength(0)
  })

  it('renders ==text== literally and produces no <mark>', () => {
    render(<HistoricalMessageMarkdown content={'a ==highlight== b'} />)
    expect(bodyText()).toContain('==highlight==')
    expect(document.querySelector('mark')).toBeNull()
  })

  it('renders a callout marker literally and keeps the blockquote a blockquote', () => {
    render(<HistoricalMessageMarkdown content={'> [!warning] Careful\n> body'} />)
    expect(bodyText()).toContain('[!warning]')
    expect(document.querySelector('blockquote')).not.toBeNull()
    expect(document.querySelector('[data-kb-callout]')).toBeNull()
  })

  it('still renders frontmatter as a rule and a heading (chat has no suppression)', () => {
    render(<HistoricalMessageMarkdown content={'---\ntitle: T\n---\n\nbody'} />)
    // Not a desirable rendering — but it IS chat's rendering today, and FR-013d
    // says chat does not change. If chat ever gains frontmatter handling it must
    // be a deliberate chat change, not a side effect of the KB reader.
    expect(document.querySelector('hr')).not.toBeNull()
    expect(bodyText()).toContain('title: T')
  })

  it("keeps chat's `a` slot: a relative link is struck through, not linked", () => {
    render(<HistoricalMessageMarkdown content={'[plan](notes/plan.md)'} />)
    const link = screen.getByTestId('markdown-link')
    expect(link.tagName).toBe('SPAN')
    expect(link.className).toContain('line-through')
  })
})

describe("chat's LIVE renderer is unchanged too (FR-013d, the other half)", () => {
  // WHY THIS ONE IS A SOURCE-TEXT GUARD AND NOT A RENDER.
  // `MarkdownText` renders `MarkdownTextPrimitive`, which reads its text from
  // AssistantUI's message context — there is no way to hand it a string and
  // look at the output without standing up a whole runtime. The property that
  // matters is nevertheless perfectly checkable: the KB plugins and the KB link
  // renderer must not appear in that module at all.
  //
  // DIES ON: importing anything from the KB composition into
  // chat/markdown-text.tsx, or naming a KB plugin in its plugin lists — which
  // is precisely the change the FR-013d block above would NOT have caught.
  // Resolved from the repo root rather than from import.meta.url: under vitest
  // the module URL is not a file: URL. A wrong path throws here and fails the
  // whole suite loudly, which is the right failure for a guard — the mode that
  // must never happen is reading an empty string and passing every assertion.
  const source = readFileSync(resolve(process.cwd(), 'src/components/chat/markdown-text.tsx'), 'utf8')

  it('imports nothing from the knowledge-base composition', () => {
    for (const forbidden of ['knowledgeMarkdown', 'kbMarkdownBase', 'LibraryMarkdownPreview', 'library/']) {
      expect(source, `chat/markdown-text.tsx must not import ${forbidden}`).not.toContain(forbidden)
    }
  })

  it('names no KB remark plugin', () => {
    for (const forbidden of [
      'remarkStripPrivateComments',
      'remarkKbWikilinks',
      'remarkKbCallouts',
      'remarkKbHighlights',
      'remarkKbFrontmatter',
      'KB_REMARK_PLUGINS',
      'KB_BASE_REMARK_PLUGINS',
    ]) {
      expect(source, `chat/markdown-text.tsx must not use ${forbidden}`).not.toContain(forbidden)
    }
  })

  it("keeps chat's own `a` slot", () => {
    // The shared factory with the shared "no rewrite" argument — not the KB
    // link renderer, which resolves collection paths and wikilinks.
    expect(source).toContain('createLinkRenderer(null)')
    expect(source).not.toContain('KnowledgeMarkdownLink')
    expect(source).not.toContain('CollectionLink')
  })

  it('self-check: the guard is reading a real file with the expected shape', () => {
    // A source-text guard that silently reads an empty string passes every
    // "not.toContain" above. This is the check that the file was found.
    expect(source.length).toBeGreaterThan(500)
    expect(source).toContain('MarkdownTextPrimitive')
    expect(source).toContain('remarkPlugins')
  })
})

describe('the composition inherits chat’s renderers (FR-013a/b — spec test 87)', () => {
  // DIES ON: re-declaring any element renderer in knowledgeMarkdown.tsx instead
  // of spreading the inherited map — the reference assertions fail immediately,
  // which is the whole point of asserting by reference rather than by rendering.

  it('uses chat’s own renderer objects for every shared element', () => {
    for (const key of Object.keys(commonMarkdownComponents)) {
      expect(
        knowledgeMarkdownComponents[key as keyof typeof knowledgeMarkdownComponents],
        `component "${key}" must be chat's object, not a copy`,
      ).toBe(commonMarkdownComponents[key as keyof typeof commonMarkdownComponents])
    }
  })

  it('diverges from the stage-1 KB map in exactly one slot: `a`', () => {
    const divergent = Object.keys(knowledgeMarkdownComponents).filter(
      (key) =>
        knowledgeMarkdownComponents[key as keyof typeof knowledgeMarkdownComponents] !==
        kbMarkdownComponents[key as keyof typeof kbMarkdownComponents],
    )
    expect(divergent).toEqual(['a'])
    // And no slot was ADDED or REMOVED — a new key is a divergence too.
    expect(Object.keys(knowledgeMarkdownComponents).sort()).toEqual(
      Object.keys(kbMarkdownComponents).sort(),
    )
  })

  it('appends only the permitted remark plugins to chat’s list', () => {
    // Chat's two parser plugins are present, by reference.
    for (const plugin of CHAT_REMARK_PLUGINS) {
      expect(KB_BASE_REMARK_PLUGINS).toContain(plugin)
    }
    // Everything else in the list is a KB plugin from the permitted set. The
    // assertion is a WHITELIST: a plugin added for any other purpose fails here.
    const permitted = new Set<unknown>([
      ...CHAT_REMARK_PLUGINS,
      ...KB_REMARK_PLUGINS, // stage 1: gfm, math, remarkStripPrivateComments
      remarkKbFrontmatter,
      remarkKbCallouts,
      remarkKbHighlights,
    ])
    for (const plugin of KB_BASE_REMARK_PLUGINS) {
      expect(permitted.has(plugin), `unexpected remark plugin: ${String(plugin)}`).toBe(true)
    }
    // The wikilink plugin is deliberately NOT in the module-scope list: it takes
    // per-collection options and is appended at render time. If it ever appears
    // here, it is being run without its resolver and embeds stop resolving.
    expect(KB_BASE_REMARK_PLUGINS).not.toContain(remarkKbWikilinks)
  })

  it('adds NO rehype plugin — the list is chat’s two, unchanged', () => {
    expect(KB_REHYPE_PLUGINS).toEqual([rehypeKatex, rehypePhosphorEmoji])
  })

  it('renders ordinary markdown byte-identically to chat', () => {
    const source = [
      'A paragraph with **bold** and `inline`.',
      '',
      '| a | b |',
      '| :-- | --: |',
      '| 1 | 2 |',
      '',
      '- one',
      '- two',
      '',
      '> plain quote',
      '',
      '![pic](https://example.test/a.png)',
    ].join('\n')

    const chat = render(<HistoricalMessageMarkdown content={source} />)
    const chatHtml = chat.container.innerHTML
    chat.unmount()

    const kb = render(<KnowledgeBaseMarkdown content={source} />)
    expect(kb.container.innerHTML).toBe(chatHtml)
  })

  it('routes fences through the same shared mechanisms as chat', () => {
    // Block code and mermaid are asserted through the SHARED sentinels rather
    // than by HTML equality: stage 1 deliberately reuses CopyCodeHeader while
    // chat's finalized path keeps its own copy header (markdown-shared.tsx
    // records that the header chrome is per-caller). The parity that matters is
    // the highlighter and the diagram, and those are shared.
    render(<KnowledgeBaseMarkdown content={'```ts\nconst a = 1\n```\n\n```mermaid\ngraph TD\n```'} />)
    expect(screen.getByTestId('shiki')).toBeTruthy()
    expect(screen.getByTestId('mermaid-diagram').textContent).toBe('graph TD')
  })

  it('renders images through chat’s image renderer', () => {
    render(<KnowledgeBaseMarkdown content={'![alt](https://example.test/a.png)'} />)
    expect(screen.getByTestId('chat-image').getAttribute('src')).toBe('https://example.test/a.png')
  })
})

describe('the components map is a module-scope constant (FR-013c — spec test 87)', () => {
  // DIES ON: moving `knowledgeMarkdownComponents` (or any single slot) inside
  // the component so it is rebuilt per render. react-markdown treats each
  // entry's REFERENCE as that node type's component type, so a fresh map makes
  // React unmount and remount every element — which is exactly what the DOM
  // identity check below detects.

  it('keeps the same DOM nodes when only the text changes', () => {
    // Every slot is checked, not just one: a map that is stable except for a
    // single per-render entry remounts only that element type, and a test that
    // samples one element would miss it.
    const { rerender } = render(<KnowledgeBaseMarkdown content={'# Title\n\n[[Note]]\n\nfirst'} />)
    const heading = document.querySelector('h1')
    const link = screen.getByTestId('markdown-link')
    expect(heading).not.toBeNull()

    rerender(<KnowledgeBaseMarkdown content={'# Title\n\n[[Note]]\n\nsecond'} />)
    expect(bodyText()).toContain('second')
    // Same element instances ⇒ React reconciled in place ⇒ the component types
    // (i.e. the map entries) were stable across renders.
    expect(document.querySelector('h1')).toBe(heading)
    expect(screen.getByTestId('markdown-link')).toBe(link)
  })
})

describe('private comments stay hidden in the KB composition (FR-011)', () => {
  // DIES ON: dropping remarkStripPrivateComments when the stage-1 list is
  // spread in — e.g. rebuilding the array by hand and forgetting it.

  it('hides an inline comment, marker and content', () => {
    render(<KnowledgeBaseMarkdown content={'before %%internal aside%% after'} />)
    expect(bodyText()).not.toContain('internal aside')
    expect(bodyText()).not.toContain('%%')
    expect(bodyText()).toContain('before')
    expect(bodyText()).toContain('after')
  })

  it('hides a wikilink that sits inside a comment', () => {
    // Ordering guard: comments are stripped BEFORE wikilinks are built, so a
    // "private" link never becomes a rendered anchor.
    render(<KnowledgeBaseMarkdown content={'keep %%[[Secret Note]]%% tail'} />)
    expect(bodyText()).not.toContain('Secret Note')
    expect(document.querySelectorAll('a')).toHaveLength(0)
  })
})

describe('frontmatter is not body content (FR-061, US-7 AS-4)', () => {
  // DIES ON: deleting remarkKbFrontmatter, or detecting frontmatter from the
  // TREE (a thematicBreak + setext heading) instead of the source — the
  // "document that legitimately opens with a rule" case below then fails.

  it('renders neither a rule nor a heading for a frontmatter block', () => {
    render(<KnowledgeBaseMarkdown content={'---\ntitle: My note\ntags: [a, b]\n---\n\n# Real heading\n\nbody'} />)
    expect(document.querySelector('hr')).toBeNull()
    expect(bodyText()).not.toContain('title: My note')
    expect(bodyText()).not.toContain('tags:')
    expect(screen.getByText('Real heading')).toBeTruthy()
    expect(bodyText()).toContain('body')
  })

  it('leaves an UNCLOSED frontmatter delimiter visible', () => {
    // Failing towards "the reader sees the raw text" is deliberate: a strip that
    // guesses would swallow the rest of the document with nothing naming why.
    render(<KnowledgeBaseMarkdown content={'---\ntitle: My note\n\nbody text'} />)
    expect(bodyText()).toContain('title: My note')
    expect(bodyText()).toContain('body text')
  })

  it('does not eat a document that legitimately begins with a horizontal rule', () => {
    render(<KnowledgeBaseMarkdown content={'---\n\n# Heading\n\nbody'} />)
    expect(document.querySelector('hr')).not.toBeNull()
    expect(screen.getByText('Heading')).toBeTruthy()
  })
})

describe('callouts and highlights render (FR-061, US-7 AS-3)', () => {
  // DIES ON: deleting remarkKbCallouts / remarkKbHighlights, or leaving the
  // `[!type]` marker in the body text.

  it('renders a callout with its type, its title and no raw marker', () => {
    render(<KnowledgeBaseMarkdown content={'> [!warning] Careful\n> the body'} />)
    const callout = document.querySelector('[data-kb-callout]')
    expect(callout).not.toBeNull()
    expect(callout?.getAttribute('data-kb-callout')).toBe('warning')
    expect(bodyText()).not.toContain('[!warning]')
    expect(bodyText()).toContain('Careful')
    expect(bodyText()).toContain('the body')
  })

  it('falls back to the callout type when no title was written', () => {
    render(<KnowledgeBaseMarkdown content={'> [!note]\n> body only'} />)
    expect(document.querySelector('[data-kb-callout]')?.getAttribute('data-kb-callout')).toBe('note')
    expect(bodyText()).toContain('note')
    expect(bodyText()).not.toContain('[!note]')
  })

  it('leaves an ordinary blockquote alone', () => {
    render(<KnowledgeBaseMarkdown content={'> just a quote'} />)
    expect(document.querySelector('blockquote')).not.toBeNull()
    expect(document.querySelector('[data-kb-callout]')).toBeNull()
  })

  it('renders ==text== as a highlight with no markers left', () => {
    render(<KnowledgeBaseMarkdown content={'a ==lit up== b'} />)
    const mark = document.querySelector('mark')
    expect(mark).not.toBeNull()
    expect(mark?.textContent).toBe('lit up')
    expect(bodyText()).not.toContain('==')
  })

  it('leaves == inside a code fence literal', () => {
    render(<KnowledgeBaseMarkdown content={'```\na ==b== c\n```'} />)
    expect(document.querySelector('mark')).toBeNull()
    expect(bodyText()).toContain('==b==')
  })
})

describe('wikilinks and embeds (FR-060, US-7 AS-1/AS-2)', () => {
  // DIES ON: deleting remarkKbWikilinks (every case renders as literal text),
  // or dropping the alias / heading branches of parseWikilink.

  const cases: Array<[string, string, string, string | undefined]> = [
    // source,                   visible text,   target,        heading
    ['[[Note]]', 'Note', 'Note', undefined],
    ['[[Note|alias]]', 'alias', 'Note', undefined],
    ['[[Note#Heading]]', 'Note#Heading', 'Note', 'Heading'],
    ['[[folder/Note]]', 'folder/Note', 'folder/Note', undefined],
    ['[[folder/Note#H|shown]]', 'shown', 'folder/Note', 'H'],
  ]

  it.each(cases)('renders %s as a working link', (source, text, target, heading) => {
    // `linkHref` is supplied here because that is what turns an in-collection
    // link into a real ANCHOR. Without an address there is nothing honest to
    // put in an href — see the no-address case below.
    render(<KnowledgeBaseMarkdown content={source} linkHref={(p) => `/#/library?path=${p}`} />)
    const link = screen.getByTestId('markdown-link')
    expect(link.tagName).toBe('A')
    expect(link.getAttribute('data-kb-target')).toBe(target)
    expect(link.textContent).toContain(text)
    const parsed = parseWikilink(source.slice(2, -2))
    expect(parsed?.heading).toBe(heading)
  })

  it('is a BUTTON, not an <a href>, when no real address is available', () => {
    // FR-012 / the middle-click hazard. An anchor whose href is a bare
    // collection path (`notes/plan.md`) and whose only defence is
    // event.preventDefault() looks and behaves like a link for a plain click —
    // and middle-click, ctrl/cmd-click and "Open link in new tab" never fire
    // onClick at all, so they navigate the browser to a relative URL that
    // resolves to nothing and take the reader out of the SPA.
    //
    // DIES ON: rendering `<a href={path}>` in the no-linkHref case.
    render(<KnowledgeBaseMarkdown content={'[[Note]]'} />)
    const link = screen.getByTestId('markdown-link')
    expect(link.tagName).toBe('BUTTON')
    expect(link.getAttribute('href')).toBeNull()
  })

  it('leaves a modified click to the browser when there IS a real address', () => {
    // The href exists precisely so ctrl/cmd-click can open a second tab.
    // Intercepting it would take away the only thing having a URL bought.
    const onNavigate = vi.fn()
    render(
      <KnowledgeBaseMarkdown
        content={'[[Note]]'}
        onNavigate={onNavigate}
        linkHref={(p) => `/#/library?path=${p}`}
      />,
    )
    const link = screen.getByTestId('markdown-link')
    fireEvent.click(link, { metaKey: true })
    expect(onNavigate).not.toHaveBeenCalled()
    fireEvent.click(link)
    expect(onNavigate).toHaveBeenCalledWith('Note', undefined)
  })

  it('navigates in-app rather than reloading the page', () => {
    const onNavigate = vi.fn()
    render(<KnowledgeBaseMarkdown content={'[[folder/Note#H]]'} onNavigate={onNavigate} />)
    fireEvent.click(screen.getByTestId('markdown-link'))
    expect(onNavigate).toHaveBeenCalledWith('folder/Note', 'H')
  })

  it('renders ![[image.png]] as an image when the collection can resolve it', () => {
    render(
      <KnowledgeBaseMarkdown
        content={'![[diagram.png]]'}
        resolveEmbedUrl={(target) => `https://example.test/${target}`}
      />,
    )
    expect(screen.getByTestId('chat-image').getAttribute('src')).toBe('https://example.test/diagram.png')
  })

  it('reports an embed it cannot resolve instead of rendering a broken image', () => {
    render(<KnowledgeBaseMarkdown content={'![[diagram.png]]'} />)
    expect(document.querySelector('img')).toBeNull()
    const link = screen.getByTestId('markdown-link')
    expect(link.getAttribute('data-kb-embed')).not.toBeNull()
    expect(link.textContent).toContain('embed shown as a link')
  })

  it('leaves [[…]] inside a code fence literal', () => {
    render(<KnowledgeBaseMarkdown content={'```\n[[Note]]\n```'} />)
    expect(document.querySelectorAll('a')).toHaveLength(0)
    expect(bodyText()).toContain('[[Note]]')
  })

  it('scrolls within the note for [[#Heading]]', () => {
    const onHeadingLink = vi.fn()
    const onNavigate = vi.fn()
    render(
      <KnowledgeBaseMarkdown content={'[[#Some Heading]]'} onHeadingLink={onHeadingLink} onNavigate={onNavigate} />,
    )
    fireEvent.click(screen.getByTestId('markdown-link'))
    expect(onHeadingLink).toHaveBeenCalledWith('Some Heading')
    expect(onNavigate).not.toHaveBeenCalled()
  })
})

describe('unresolved links are marked and cannot navigate (FR-065, US-7 AS-8)', () => {
  // DIES ON: rendering the unresolved case as an <a href> (it becomes
  // navigable), or on treating an absent resolver as "unresolved".

  it('marks a target the collection says does not exist', () => {
    render(
      <KnowledgeBaseMarkdown
        content={'[[Ghost Note]]'}
        resolveWikilink={() => ({ state: 'unresolved' })}
      />,
    )
    const link = screen.getByTestId('markdown-link')
    expect(link.tagName).not.toBe('A')
    expect(link.getAttribute('data-kb-unresolved')).toBe('true')
    expect(link.textContent).toContain('unresolved link')
  })

  it('does not navigate when an unresolved link is clicked', () => {
    const onNavigate = vi.fn()
    render(
      <KnowledgeBaseMarkdown
        content={'[[Ghost Note]]'}
        onNavigate={onNavigate}
        resolveWikilink={() => ({ state: 'unresolved' })}
      />,
    )
    fireEvent.click(screen.getByTestId('markdown-link'))
    expect(onNavigate).not.toHaveBeenCalled()
  })

  it('does NOT claim "unresolved" when the link graph has not loaded', () => {
    // Absence of evidence is not evidence of absence. Marking every link broken
    // while the graph loads is the same class of confidently-wrong answer this
    // feature refuses everywhere else — so it stays clickable.
    const onNavigate = vi.fn()
    render(<KnowledgeBaseMarkdown content={'[[Note]]'} onNavigate={onNavigate} linkHref={(p) => `/x/${p}`} />)
    const link = screen.getByTestId('markdown-link')
    expect(link.tagName).toBe('A')
    expect(link.getAttribute('data-kb-state')).toBe('unknown')
    fireEvent.click(link)
    expect(onNavigate).toHaveBeenCalledWith('Note', undefined)
  })

  it('does not claim "resolved" either — an unverified link LOOKS different', () => {
    // The three states had two renderings: `unknown` was drawn identically to
    // `resolved` — same accent colour, same solid underline, same live click —
    // and differed only by a data attribute, which no reader can see. Rendering
    // an unchecked link as a verified one is the same error as marking it
    // broken, pointed the other way, and it is the more expensive one because
    // the reader acts on it.
    //
    // The assertion is DIFFERENCE, not a specific class string: it dies the
    // moment the two branches share a rendering again, and it does not pin the
    // design to one border style.
    //
    // DIES ON: `const className = LINK_CLASS` for both states in CollectionLink.
    const href = (p: string) => `/x/${p}`
    const unknown = render(<KnowledgeBaseMarkdown content={'[[Note]]'} linkHref={href} />)
    const unknownLink = screen.getByTestId('markdown-link')
    const unknownClass = unknownLink.getAttribute('class') ?? ''
    // Stated in words too, for a reader who is not looking at the border.
    expect(unknownLink.textContent).toMatch(/not verified/i)
    unknown.unmount()

    render(
      <KnowledgeBaseMarkdown
        content={'[[Note]]'}
        linkHref={href}
        resolveWikilink={() => ({ state: 'resolved', path: 'Note.md' })}
      />,
    )
    const resolvedLink = screen.getByTestId('markdown-link')
    expect(resolvedLink.getAttribute('data-kb-state')).toBe('resolved')
    expect(resolvedLink.getAttribute('class') ?? '').not.toBe(unknownClass)
    expect(resolvedLink.textContent).not.toMatch(/not verified/i)
  })

  it('follows the resolved path, not the written target', () => {
    const onNavigate = vi.fn()
    render(
      <KnowledgeBaseMarkdown
        content={'[[Note]]'}
        onNavigate={onNavigate}
        resolveWikilink={() => ({ state: 'resolved', path: 'inbox/Note.md' })}
      />,
    )
    fireEvent.click(screen.getByTestId('markdown-link'))
    expect(onNavigate).toHaveBeenCalledWith('inbox/Note.md', undefined)
  })
})

describe('links cannot reach outside the collection (US-10 AS-1/AS-2)', () => {
  // DIES ON: resolving `..` past the collection root, or treating a leading `/`
  // as a collection path — either turns a note into a reader for the host disk.

  it.each([
    ['../../etc/passwd', 'traversal above the root'],
    ['/etc/passwd', 'absolute filesystem path'],
  ])('marks %s unresolved and does not navigate', (href) => {
    const onNavigate = vi.fn()
    render(
      <KnowledgeBaseMarkdown content={`[x](${href})`} notePath={'notes/plan.md'} onNavigate={onNavigate} />,
    )
    const link = screen.getByTestId('markdown-link')
    expect(link.getAttribute('data-kb-unresolved')).toBe('true')
    fireEvent.click(link)
    expect(onNavigate).not.toHaveBeenCalled()
  })

  it('resolves an ordinary relative link against the open note’s folder', () => {
    const onNavigate = vi.fn()
    render(
      <KnowledgeBaseMarkdown
        content={'[x](../archive/old.md#Section)'}
        notePath={'notes/2026/plan.md'}
        onNavigate={onNavigate}
      />,
    )
    fireEvent.click(screen.getByTestId('markdown-link'))
    expect(onNavigate).toHaveBeenCalledWith('notes/archive/old.md', 'Section')
  })

  it('resolveCollectionPath refuses exactly the escaping forms', () => {
    expect(resolveCollectionPath('notes/plan.md', 'a.md')).toBe('notes/a.md')
    expect(resolveCollectionPath('notes/plan.md', './a.md')).toBe('notes/a.md')
    expect(resolveCollectionPath('notes/plan.md', '../a.md')).toBe('a.md')
    expect(resolveCollectionPath('notes/plan.md', '../../a.md')).toBeNull()
    expect(resolveCollectionPath('notes/plan.md', '/a.md')).toBeNull()
    expect(resolveCollectionPath(undefined, '../a.md')).toBeNull()
  })
})

describe('everything the KB slot does not recognise falls through to chat (FR-013b)', () => {
  // DIES ON: hand-rolling the external/unsafe cases in the KB `a` slot instead
  // of delegating — the scheme allow-list would then exist in two places.

  it('renders an unsafe scheme struck through, exactly as chat does', () => {
    render(<KnowledgeBaseMarkdown content={'[click](javascript:alert(1))'} />)
    const link = screen.getByTestId('markdown-link')
    expect(link.tagName).toBe('SPAN')
    expect(link.className).toContain('line-through')
    expect(link.getAttribute('href')).toBeNull()
  })

  it('opens an external link in a new tab, exactly as chat does', () => {
    render(<KnowledgeBaseMarkdown content={'[site](https://example.test/page)'} />)
    const link = screen.getByTestId('markdown-link')
    expect(link.getAttribute('href')).toBe('https://example.test/page')
    expect(link.getAttribute('target')).toBe('_blank')
    expect(link.getAttribute('rel')).toBe('noopener noreferrer')
  })
})

describe('parseWikilink (unit)', () => {
  it('parses the four documented forms and rejects empty bodies', () => {
    expect(parseWikilink('Note')).toEqual({ target: 'Note', heading: undefined, text: 'Note', embed: false })
    expect(parseWikilink('Note|alias')?.text).toBe('alias')
    expect(parseWikilink('Note#H')?.heading).toBe('H')
    expect(parseWikilink('folder/Note')?.target).toBe('folder/Note')
    expect(parseWikilink('#H')?.target).toBe('')
    expect(parseWikilink('   ')).toBeNull()
    expect(parseWikilink('')).toBeNull()
  })
})
