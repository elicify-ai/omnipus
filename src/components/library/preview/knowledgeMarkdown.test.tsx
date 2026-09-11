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
  classifyEmbedKind,
  knowledgeMarkdownComponents,
  parseWikilink,
  remarkKbCallouts,
  remarkKbFrontmatter,
  remarkKbHighlights,
  remarkKbVideoImages,
  remarkKbWikilinks,
  resolveCollectionPath,
  type EmbedResolutionResolved,
} from './knowledgeMarkdown'

// Sentinels — each proves a specific branch ran.
vi.mock('@/components/chat/mermaid-renderer', () => ({
  MermaidDiagram: ({ code }: { code: string }) => <div data-testid="mermaid-diagram">{code}</div>,
}))
vi.mock('react-shiki', () => ({
  ShikiHighlighter: ({ children }: { children?: React.ReactNode }) => <pre data-testid="shiki">{children}</pre>,
  // markdown-shared.tsx passes Shiki its pure-JS regex engine (the SPA's CSP
  // refuses the WebAssembly default); the module is mocked here, so this only
  // has to exist.
  createJavaScriptRegexEngine: () => ({}),
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

/** A COMPLETE `resolved` embed resolution — every field the real resolver
 *  (`KnowledgeNoteView`'s `resolveEmbedAgainstGraph`) sets when it answers
 *  `resolved`. These fixtures used to omit `path`/`workspaceId`/
 *  `workspacePath`, which was only expressible because `EmbedResolution` was
 *  a bag of optionals; now the union requires them, and a fixture that
 *  cannot be constructed is a state production cannot reach either. */
function resolvedEmbed(
  url: string,
  over: Partial<EmbedResolutionResolved> = {},
): EmbedResolutionResolved {
  return {
    state: 'resolved',
    url,
    path: 'files/target',
    workspaceId: 'ws-1',
    workspacePath: 'work/files/target',
    ...over,
  }
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
      'remarkKbVideoImages',
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

  it('diverges from the stage-1 KB map in exactly two slots: `code` and `a` (ADR-083 Step 6 added the `code` divergence)', () => {
    const divergent = Object.keys(knowledgeMarkdownComponents).filter(
      (key) =>
        knowledgeMarkdownComponents[key as keyof typeof knowledgeMarkdownComponents] !==
        kbMarkdownComponents[key as keyof typeof kbMarkdownComponents],
    )
    expect(divergent).toEqual(['code', 'a'])
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
      remarkKbVideoImages,
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
        resolveEmbedUrl={() => resolvedEmbed('https://example.test/diagram.png')}
      />,
    )
    expect(screen.getByTestId('chat-image').getAttribute('src')).toBe('https://example.test/diagram.png')
  })

  it('renders ![[diagram.svg]] as an image, never as an inline <svg> (EMB-031)', () => {
    // A scripted SVG injected inline would execute; drawn inside <img> (chat's
    // image slot, secure static mode) it never does. DIES ON: routing `.svg`
    // through a different node type than other image extensions.
    render(
      <KnowledgeBaseMarkdown
        content={'![[logo.svg]]'}
        resolveEmbedUrl={() => resolvedEmbed('https://example.test/logo.svg')}
      />,
    )
    expect(screen.getByTestId('chat-image').getAttribute('src')).toBe('https://example.test/logo.svg')
    expect(document.querySelector('svg')).toBeNull()
  })

  it('reports an embed it cannot resolve instead of rendering a broken image (no resolver at all)', () => {
    // No `resolveEmbedUrl` prop — the caller has no resolution to offer
    // (e.g. outside a knowledge base). DIES ON: defaulting to a confident
    // verdict instead of `indeterminate` when the callback is absent.
    render(<KnowledgeBaseMarkdown content={'![[diagram.png]]'} />)
    expect(document.querySelector('img')).toBeNull()
    const link = screen.getByTestId('markdown-link')
    expect(link.getAttribute('data-kb-embed')).not.toBeNull()
    expect(link.getAttribute('data-kb-embed-state')).toBe('indeterminate')
    expect(link.textContent).toContain('embed shown as a link')
  })

  it('reserves space and renders NO marker while the graph is loading (EMB-015)', () => {
    // DIES ON: showing "embed shown as a link" or any unresolved styling
    // before the evidence has arrived.
    render(
      <KnowledgeBaseMarkdown content={'![[diagram.png]]'} resolveEmbedUrl={() => ({ state: 'loading' })} />,
    )
    expect(document.querySelector('img')).toBeNull()
    const placeholder = screen.getByTestId('markdown-link')
    expect(placeholder.getAttribute('data-kb-embed-state')).toBe('loading')
    expect(placeholder.textContent ?? '').toBe('')
    expect(bodyText()).not.toContain('embed shown as a link')
  })

  it('renders no marker and no badge when the whole link graph is unavailable (EMB-014)', () => {
    // Per-embed silence is the point here — the ONE statement lives at the
    // page level (KnowledgeNoteView), not per embed. DIES ON: showing the
    // "embed shown as a link" badge or a could-not-be-checked marker here.
    render(
      <KnowledgeBaseMarkdown
        content={'![[diagram.png]]'}
        resolveEmbedUrl={() => ({ state: 'graph_unavailable', reason: 'the graph request failed' })}
      />,
    )
    const el = screen.getByTestId('markdown-link')
    expect(el.getAttribute('data-kb-embed-state')).toBe('graph_unavailable')
    expect(el.textContent).not.toContain('embed shown as a link')
    expect(el.textContent).not.toMatch(/could not be checked/i)
  })

  it('marks an embed "could not be checked" — distinct from missing-file — when the graph has no matching edge (EMB-013)', () => {
    // DIES ON: reusing the missing-file marker (`data-kb-unresolved`) for a
    // state the graph never confirmed absence for.
    render(
      <KnowledgeBaseMarkdown
        content={'![[diagram.png]]'}
        resolveEmbedUrl={() => ({ state: 'indeterminate', reason: 'no reason available' })}
      />,
    )
    const el = screen.getByTestId('markdown-link')
    expect(el.getAttribute('data-kb-embed-state')).toBe('indeterminate')
    expect(el.getAttribute('data-kb-unresolved')).toBeNull()
    expect(el.textContent).toContain('embed shown as a link')
    expect(el.textContent ?? '').toMatch(/could not be checked/i)
    expect(el.textContent ?? '').not.toMatch(/nothing.*named|does not exist/i)
  })

  it('names the target on a confirmed-missing embed (EMB-016) — no badge, matching the plain-link treatment', () => {
    render(
      <KnowledgeBaseMarkdown
        content={'![[ghost.pdf]]'}
        resolveEmbedUrl={() => ({ state: 'unresolved', reason: 'no file in this collection matches "ghost.pdf"' })}
      />,
    )
    const el = screen.getByTestId('markdown-link')
    expect(el.getAttribute('data-kb-unresolved')).toBe('true')
    expect(el.textContent ?? '').toContain('ghost.pdf')
    expect(el.textContent).not.toContain('embed shown as a link')
  })

  it('renders a DIFFERENT marker for a containment refusal than for a missing file, and redacts the path (EMB-017/EMB-023/EMB-024)', () => {
    render(
      <KnowledgeBaseMarkdown
        content={'![[../../etc/passwd]]'}
        resolveEmbedUrl={() => ({
          state: 'unresolved',
          outsideRoot: true,
          reason: 'this target is outside the collection root',
        })}
      />,
    )
    const el = screen.getByTestId('markdown-link')
    expect(el.getAttribute('data-kb-embed-outside-root')).toBe('true')
    // Distinct from the ordinary missing-file marker's own attribute.
    expect(el.getAttribute('data-kb-unresolved')).toBeNull()
    // The note's own written link text is not censored (the author already
    // wrote it, and it is on the page regardless) — what EMB-017 forbids is
    // the MARKER'S OWN reason repeating the escaping path, unlike the
    // ordinary missing-file marker, whose detail interpolates the target.
    expect(el.getAttribute('title') ?? '').not.toContain('etc/passwd')
    const srOnly = el.querySelector('.sr-only')?.textContent ?? ''
    expect(srOnly).not.toContain('etc/passwd')
  })

  it('renders a resolved non-image embed as a VERIFIED link, not the unverified/unknown styling (kind dispatch, EMB-025)', () => {
    // A .pdf has no inline renderer yet (Step 1 scope: image only) — it MUST
    // fall back to the link treatment even when resolved (EMB-025), but the
    // fallback must still say "verified", because the embed resolver — not
    // the unrelated plain-wikilink resolver — answered it. DIES ON: routing
    // a resolved embed's fallback through `ctx.resolveWikilink` (which is
    // never supplied here and would default to `unknown`).
    render(
      <KnowledgeBaseMarkdown
        content={'![[report.pdf]]'}
        resolveEmbedUrl={() => resolvedEmbed('https://example.test/x', { path: 'files/report.pdf' })}
        linkHref={(p) => `/#/library?path=${p}`}
      />,
    )
    expect(document.querySelector('img')).toBeNull()
    const el = screen.getByTestId('markdown-link')
    expect(el.getAttribute('data-kb-state')).toBe('resolved')
    expect(el.getAttribute('data-kb-embed')).not.toBeNull()
    expect(el.tagName).toBe('A')
    expect(el.getAttribute('href')).toBe('/#/library?path=files/report.pdf')
    expect(el.textContent).toContain('embed shown as a link')
  })

  it('reports ambiguity and the alternatives instead of silently picking one (EMB-018)', () => {
    render(
      <KnowledgeBaseMarkdown
        content={'![[report.pdf]]'}
        resolveEmbedUrl={() =>
          resolvedEmbed('https://example.test/x', {
            path: 'files/report.pdf',
            ambiguous: true,
            candidates: ['archive/report.pdf'],
          })
        }
      />,
    )
    const el = screen.getByTestId('markdown-link')
    expect(el.getAttribute('data-kb-embed-ambiguous')).toBe('')
    expect((el.textContent ?? '').toLowerCase()).toContain('archive/report.pdf'.toLowerCase())
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

  it('splits a block anchor from a heading (ADR-083 EMB-011/EMB-036)', () => {
    // DIES ON: treating "^abc123" as heading text (the pre-CW-2 shape).
    const block = parseWikilink('Note#^abc123')
    expect(block?.block).toBe('abc123')
    expect(block?.heading).toBeUndefined()

    const heading = parseWikilink('Note#Section')
    expect(heading?.heading).toBe('Section')
    expect(heading?.block).toBeUndefined()

    // A same-note block reference, `[[#^abc]]`.
    expect(parseWikilink('#^abc')?.block).toBe('abc')
    expect(parseWikilink('#^abc')?.target).toBe('')
  })
})

describe('parseWikilink — a bar segment on an EMBED is a size or a caption, never both (ADR-083 EMB-030)', () => {
  // DIES ON: reverting to the old single `alias` field, which put EVERYTHING
  // after `|` into `text` regardless of embed-ness or digit shape — the exact
  // defect the finding named: `![[photo.png|400]]` rendering with alt="400",
  // and `![[song.mp3|400]]` losing its display text to "400" outright.

  it('reads digits after a bar on an EMBED as a width, and the display text stays the TARGET name, not the digits', () => {
    const parsed = parseWikilink('photo.png|400', true)
    expect(parsed?.width).toBe(400)
    expect(parsed?.text).toBe('photo.png')
  })

  it('reads a digitsxdigits bar segment on an EMBED as a width, using only the leading number', () => {
    const parsed = parseWikilink('photo.png|400x300', true)
    expect(parsed?.width).toBe(400)
    expect(parsed?.text).toBe('photo.png')
  })

  it('never applies the width rule to a PLAIN (non-embed) wikilink — its bar segment is always a caption, digits or not', () => {
    // The write side (knowledge_edit.go's composeEmbedNotation) only ever
    // emits a `|width` on an EMBED — a plain `[[Note|400]]` reference link
    // naming "400" as its alias is a human author's choice, not a size.
    const parsed = parseWikilink('Note|400', false)
    expect(parsed?.width).toBeUndefined()
    expect(parsed?.text).toBe('400')
  })

  it('keeps a non-digit bar segment on an EMBED as the caption it plainly is — width recognition does not swallow real aliases', () => {
    const parsed = parseWikilink('photo.png|My Caption', true)
    expect(parsed?.width).toBeUndefined()
    expect(parsed?.text).toBe('My Caption')
  })

  it('reads a size-shaped bar segment on a NON-PICTURE embed as a width too, at the notation-parsing level — never eaten as display text', () => {
    // The audio case the finding named specifically: `![[song.mp3|400]]`
    // must not show "400" as its display text. Whether that recognised
    // width is APPLIED is a later, kind-dependent decision (EMB-030: width
    // applies only to pictures) — this level only proves the notation was
    // read correctly, regardless of what the target turns out to be.
    const parsed = parseWikilink('song.mp3|400', true)
    expect(parsed?.width).toBe(400)
    expect(parsed?.text).toBe('song.mp3')
    expect(parsed?.text).not.toBe('400')
  })
})

describe('classifyEmbedKind (unit, ADR-083 EMB-034)', () => {
  it('classifies every recognised extension, extension-only', () => {
    expect(classifyEmbedKind('diagram.png')).toBe('image')
    expect(classifyEmbedKind('logo.SVG')).toBe('image')
    expect(classifyEmbedKind('clip.mp4')).toBe('video')
    expect(classifyEmbedKind('page.html')).toBe('html')
    expect(classifyEmbedKind('report.pdf')).toBe('pdf')
    expect(classifyEmbedKind('track.mp3')).toBe('audio')
    expect(classifyEmbedKind('Tasks.base')).toBe('base')
    expect(classifyEmbedKind('Note.md')).toBe('markdown')
    expect(classifyEmbedKind('diagram.mmd')).toBe('mermaid')
  })

  it('falls back to "other" for an unrecognised or missing extension — never a guessed "text" (EMB-034)', () => {
    // DIES ON: fabricating `is_text_editable` (which no embed target
    // carries) to answer 'text' the way `classifyLibraryEntry` would.
    expect(classifyEmbedKind('README')).toBe('other')
    expect(classifyEmbedKind('archive.zip')).toBe('other')
    expect(classifyEmbedKind('notes.txt')).toBe('other')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// THE NOTE RENDERER ACTUALLY USES ITS REHYPE PLUGINS
// ─────────────────────────────────────────────────────────────────────────────
//
// A SURVIVING MUTANT, found by the test-integrity audit: deleting
// `rehypePlugins={KB_REHYPE_PLUGINS}` from `KnowledgeBaseMarkdown` kills
// KaTeX *and* the Phosphor emoji translator in every knowledge-base note, and
// 132 tests across 8 files stayed green. `kbMathAlreadyWorking.test.tsx`
// renders `ReactMarkdown` and the plugin arrays DIRECTLY and never imports
// `KnowledgeBaseMarkdown` at all — so it proves the plugins work, which was
// never in doubt. It does not prove the note renderer uses them.
//
// These two render through `KnowledgeBaseMarkdown` itself, which is the
// component a reader's note actually goes through.

describe('KnowledgeBaseMarkdown applies its own rehype plugins (not just the arrays existing)', () => {
  it('renders inline math through the REAL note renderer, so removing its rehypePlugins fails here', () => {
    render(<KnowledgeBaseMarkdown content={'Energy is $E = mc^2$ here.'} />)

    // Positive: KaTeX ran inside the note composition.
    expect(document.querySelector('.katex')).not.toBeNull()
    // Paired negative: the raw delimiters are gone, so this cannot be
    // passing on a renderer that left the text untouched.
    expect(screen.queryByText('$E = mc^2$', { exact: false })).not.toBeInTheDocument()
  })

  it('renders block math through the REAL note renderer', () => {
    render(<KnowledgeBaseMarkdown content={'$$\n\\int_0^1 x\\,dx = \\tfrac12\n$$'} />)
    expect(document.querySelector('.katex-display')).not.toBeNull()
  })

  it('translates an emoji to a Phosphor icon through the REAL note renderer — the OTHER half the same mutation kills', () => {
    // The emoji translator is the second rehype plugin in the same array, so
    // one deletion takes out both. Asserting both halves means the mutation
    // cannot be half-restored and still pass.
    render(<KnowledgeBaseMarkdown content={'Shipped 🚀 today.'} />)

    // Positive: the rehype plugin emitted a `data-phosphor-icon` span and
    // the `span` slot turned it into a real Phosphor icon — an <svg>, which
    // is the only element this paragraph can contain if the translation ran.
    expect(document.querySelector('p svg')).not.toBeNull()
    // Paired negative: the raw emoji character is gone from the text (UI
    // rules: no emoji in UI chrome — it becomes a Phosphor icon), while the
    // surrounding words survive, so this cannot pass on a renderer that
    // dropped the paragraph entirely.
    expect(document.body.textContent ?? '').not.toContain('🚀')
    expect(document.body.textContent ?? '').toContain('Shipped')
    expect(document.body.textContent ?? '').toContain('today.')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// A `query` fence that cannot run says so (silent-failure audit M7)
// ─────────────────────────────────────────────────────────────────────────────

describe('KnowledgeMarkdownCode — a query fence with no workspace/collection context', () => {
  it('renders the fence text AND a stated reason, never an ordinary-looking code block', () => {
    // `KnowledgeNoteView` is the only host that supplies workspaceId and
    // collectionId. A refactor that dropped them used to turn every query
    // fence in every note back into a static code block, indistinguishable
    // from an author's deliberate one — invisible for as long as nobody
    // happened to look.
    render(<KnowledgeBaseMarkdown content={'```query\nquarterly report\n```'} />)

    const inert = screen.getByTestId('kb-query-fence-inert')
    expect(inert).toBeInTheDocument()
    // The author's own text is still shown…
    expect(inert.textContent ?? '').toContain('quarterly report')
    // …with the reason named.
    expect(screen.getByTestId('kb-query-fence-inert-reason')).toHaveTextContent(
      /can only run inside a knowledge base/i,
    )
  })

  it('gives the DELIBERATE nested-transclusion exclusion its own wording, not the missing-context one (EMB-060/N1)', () => {
    render(<KnowledgeBaseMarkdown content={'```query\nquarterly report\n```'} nestedTransclusion />)

    const reason = screen.getByTestId('kb-query-fence-inert-reason')
    expect(reason).toHaveTextContent(/inside a transcluded note is not run/i)
    expect(reason.textContent ?? '').not.toMatch(/can only run inside a knowledge base/i)
  })

  it('control — an ordinary non-query fence is untouched: no inert marker, no reason line', () => {
    render(<KnowledgeBaseMarkdown content={'```ts\nconst a = 1\n```'} />)

    expect(screen.queryByTestId('kb-query-fence-inert')).not.toBeInTheDocument()
    expect(screen.queryByTestId('kb-query-fence-inert-reason')).not.toBeInTheDocument()
    // And it still renders as code.
    expect(document.body.textContent ?? '').toContain('const a = 1')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// EmbedFallback never badges an unrecognised state "verified" (type-design F2)
// ─────────────────────────────────────────────────────────────────────────────

describe('EmbedFallback — the confident render is reachable only from a proved `resolved`', () => {
  it('treats an UNRECOGNISED embed state as "no verdict", never as a verified link', () => {
    // The state crosses the remark pipeline as a flat attribute string. When
    // `EmbedFallback` took `state: string` and matched it with an if-chain,
    // any value it did not recognise fell through to the tail — which
    // rendered `<CollectionLink … verified>`, a link BADGED as confirmed for
    // a target the graph never confirmed. That is the exact inversion
    // EMB-012/EMB-013 exist to prevent, and `npm run typecheck` stayed
    // silent throughout.
    render(
      <KnowledgeBaseMarkdown
        content={'![[report.pdf]]'}
        // A state no build knows about. The resolver type now forbids
        // constructing one, so this reaches the renderer the only way it
        // still can — as the raw attribute value.
        resolveEmbedUrl={() =>
          ({ state: 'ambiguous_skipped', reason: 'a state this build does not know' }) as unknown as ReturnType<
            NonNullable<React.ComponentProps<typeof KnowledgeBaseMarkdown>['resolveEmbedUrl']>
          >
        }
      />,
    )

    const el = screen.getByTestId('markdown-link')
    // `data-kb-state` is 'resolved' ONLY for a link CollectionLink was told
    // was verified (its own `verified ? 'resolved' : 'unknown'`). Before the
    // exhaustive switch this read 'resolved' for a state nothing confirmed.
    expect(el.getAttribute('data-kb-state')).not.toBe('resolved')
  })

  it('positive control — a genuinely RESOLVED embed with no inline renderer IS badged verified', () => {
    render(
      <KnowledgeBaseMarkdown
        content={'![[report.pdf]]'}
        resolveEmbedUrl={() => resolvedEmbed('https://example.test/x', { path: 'files/report.pdf' })}
        linkHref={(p) => `/#/library?path=${p}`}
      />,
    )

    const el = screen.getByTestId('markdown-link')
    expect(el.getAttribute('data-kb-state')).toBe('resolved')
    expect(el.tagName).toBe('A')
  })
})
