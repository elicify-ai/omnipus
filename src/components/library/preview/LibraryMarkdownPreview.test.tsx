/**
 * LibraryMarkdownPreview.tsx — the knowledge-base markdown COMPOSITION.
 *
 * ADR-067 STAGE 1: FR-011 (`%%…%%` hidden in the KB reader and nowhere else),
 * FR-013a/b (a second composition over the shared pipeline, diverging in exactly
 * two places), FR-013c (module-scope components map), FR-013d (chat unchanged),
 * US-3 AS-1, and the relative-link defect recorded in §2.4.
 *
 * react-markdown, remark's parser and `remarkStripPrivateComments` stay REAL —
 * that is the point: the plugin has to run against a genuinely parsed mdast and
 * the component overrides have to fire on genuinely parsed nodes. Only leaf
 * dependencies (Shiki's WASM tokenizer, Mermaid, the lightbox) are stubbed, and
 * each stub is a SENTINEL that proves which branch was taken rather than a
 * blanket silencer.
 *
 * Every `describe` block names the mutation its assertions die on.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import {
  KnowledgeMarkdown,
  LibraryMarkdownPreview,
  kbMarkdownComponents,
  KB_REMARK_PLUGINS,
  KB_REHYPE_PLUGINS,
  remarkStripPrivateComments,
} from './LibraryMarkdownPreview'
import { commonMarkdownComponents } from '@/components/chat/markdown-shared'
import { HistoricalMessageMarkdown } from '@/components/chat/historical-markdown'
import type { LibraryEntry } from '@/lib/api'

// remark-gfm / remark-math / rehype-katex are inert here: none of the assertions
// below depend on GFM or maths, and the parity checks are made by REFERENCE
// (see "inherits chat's element renderers") rather than by rendering a table.
vi.mock('remark-gfm', () => ({ default: () => {} }))
vi.mock('remark-math', () => ({ default: () => {} }))
vi.mock('rehype-katex', () => ({ default: () => {} }))
vi.mock('katex/dist/katex.min.css', () => ({}))
vi.mock('@/lib/rehype-phosphor-emoji', () => ({ rehypePhosphorEmoji: () => {} }))

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

vi.mock('@/store/ui', () => ({
  useUiStore: { getState: () => ({ addToast: vi.fn() }) },
}))
// The stage-2 reading view this pane now mounts fetches through react-query
// (outline, link graph, detection). Both hooks are stubbed to "nothing has
// arrived", which is the state this file is asserting the pane in: the rails
// have no data, so what is proven is the reading COLUMN and its composition.
vi.mock('@tanstack/react-query', () => ({
  useQuery: () => ({ data: null }),
  useQueries: () => [],
}))

// The view shell is replaced by a pass-through that renders whatever
// `renderView` produces. This keeps the composition assertions independent of
// LibraryTextPreview / LibraryPreviewPane, which other work is changing
// concurrently — a churn failure there must not read as a markdown failure here.
vi.mock('./LibraryTextPreview', () => ({
  LibraryTextPreview: ({
    content,
    renderView,
  }: {
    content: string
    renderView: (draft: string) => React.ReactNode
  }) => <div data-testid="text-preview-shell">{renderView(content)}</div>,
}))

const entry: LibraryEntry = { name: 'plan.md', path: 'notes/plan.md' } as LibraryEntry

// ─────────────────────────────────────────────────────────────────────────────

describe('KB markdown — %% private comments are hidden (FR-011, US-3 AS-1)', () => {
  // DIES ON: dropping `remarkStripPrivateComments` from KB_REMARK_PLUGINS
  // (verified red), or replacing the strip with a no-op transformer.

  it('hides an inline comment — marker and content both', () => {
    render(<KnowledgeMarkdown content={'before %%internal aside%% after'} />)
    const body = document.body.textContent ?? ''
    expect(body).not.toContain('internal aside')
    expect(body).not.toContain('%%')
    // Positive control: the surrounding prose must still be there, or "nothing
    // rendered at all" would pass the two assertions above.
    expect(body).toContain('before')
    expect(body).toContain('after')
  })

  it('hides a multi-line comment and leaves no empty paragraph behind', () => {
    // DIES ON: resetting the strip state per node instead of carrying
    // `state.inside` across blocks; and on keeping emptied paragraphs.
    render(<KnowledgeMarkdown content={'visible one\n\n%%\nsecret line\n%%\n\nvisible two'} />)
    const body = document.body.textContent ?? ''
    expect(body).not.toContain('secret line')
    expect(body).not.toContain('%%')
    expect(body).toContain('visible one')
    expect(body).toContain('visible two')
    // Exactly the two surviving paragraphs — a third, empty one means the
    // emptied container was kept.
    expect(document.querySelectorAll('p')).toHaveLength(2)
  })

  it('swallows non-text nodes that sit between the markers', () => {
    // DIES ON: deleting the `if (state.inside) continue` leaf drop in
    // stripCommentsFromChildren — the bold run and the image survive and the
    // reader sees the "private" content.
    render(<KnowledgeMarkdown content={'keep %%hidden **bold** ![x](https://e.test/a.png) tail%% keep2'} />)
    const body = document.body.textContent ?? ''
    expect(body).not.toContain('bold')
    expect(body).not.toContain('hidden')
    expect(body).not.toContain('tail')
    expect(screen.queryByTestId('chat-image')).toBeNull()
    expect(body).toContain('keep')
    expect(body).toContain('keep2')
  })

  it('leaves %% inside a fenced code block alone', () => {
    // DIES ON: running the text scanner over `code`/`inlineCode` node values.
    // A knowledge base full of shell snippets would silently lose source.
    render(<KnowledgeMarkdown content={'```sh\necho %%not a comment%%\n```'} />)
    expect(screen.getByTestId('shiki').textContent).toContain('%%not a comment%%')
  })

  it('applies to the plugin itself, not only through the renderer', () => {
    // Direct unit check on the exported plugin, so a future caller that composes
    // it elsewhere is covered too. DIES ON: the plugin returning the tree
    // untouched.
    const tree = {
      type: 'root',
      children: [
        { type: 'paragraph', children: [{ type: 'text', value: 'a %%b%% c' }] },
        { type: 'paragraph', children: [{ type: 'text', value: '%%all of it%%' }] },
      ],
    }
    remarkStripPrivateComments()(tree)
    expect(tree.children).toHaveLength(1)
    expect(tree.children[0].children[0].value).toBe('a  c')
  })
})

describe('Chat markdown still shows %% literally (FR-011 scope, FR-013d)', () => {
  // The regression this feature would create if the strip were implemented in
  // the only markdown renderer that existed before it. Chat renders untrusted
  // model and tool output: deleting the text between two markers there hides
  // content FROM the reader rather than protecting them.
  // DIES ON: adding remarkStripPrivateComments to historical-markdown.tsx's
  // remarkPlugins (verified red by temporarily adding it there).
  it('renders the marker and the content', () => {
    render(<HistoricalMessageMarkdown content={'chat %%secret%% text'} />)
    const body = document.body.textContent ?? ''
    expect(body).toContain('%%secret%%')
  })
})

describe('KB markdown — the `a` slot resolves relative links (§2.4, FR-013b)', () => {
  // DIES ON: setting `a: createLinkRenderer(null)` (chat's slot) in
  // kbMarkdownComponents — every relative link goes back to being struck
  // through as "Link removed: unsafe URL scheme".

  it.each([
    ['a sibling note', '[plan](notes/plan.md)', 'notes/plan.md'],
    ['an explicitly relative note', '[plan](./plan.md)', './plan.md'],
    ['a root-relative path', '[plan](/vault/plan.md)', '/vault/plan.md'],
    ['a heading link', '[top](#overview)', '#overview'],
  ])('renders %s as a real anchor', (_label, markdown, expectedHref) => {
    render(<KnowledgeMarkdown content={markdown} />)
    const link = screen.getByTestId('markdown-link')
    expect(link.tagName).toBe('A')
    expect(link.getAttribute('href')).toBe(expectedHref)
    // The struck-through treatment must be gone, not merely re-styled.
    expect(link.getAttribute('title')).not.toBe('Link removed: unsafe URL scheme')
    expect(link.className).not.toContain('line-through')
  })

  it('does NOT open an in-collection link in a new tab', () => {
    // A link to another note means "go there", not "open the app twice".
    // DIES ON: copying chat's target="_blank" onto the relative branch.
    render(<KnowledgeMarkdown content={'[plan](notes/plan.md)'} />)
    expect(screen.getByTestId('markdown-link').getAttribute('target')).toBeNull()
  })

  it.each([
    ['javascript:', '[x](javascript:alert(1))'],
    ['data:', '[x](data:text/html;base64,PHNjcmlwdD4=)'],
    ['vbscript:', '[x](vbscript:msgbox(1))'],
    ['file:', '[x](file:///etc/passwd)'],
  ])('still refuses a %s href', (_label, markdown) => {
    // DIES ON: making KbMarkdownLink render an anchor unconditionally, or
    // dropping the delegation to chat's renderer for schemed hrefs.
    render(<KnowledgeMarkdown content={markdown} />)
    const link = screen.getByTestId('markdown-link')
    expect(link.tagName).toBe('SPAN')
    expect(link).not.toHaveAttribute('href')
    expect(link.getAttribute('title')).toBe('Link removed: unsafe URL scheme')
  })

  it('refuses a protocol-relative href', () => {
    // `//evil.test/x` has no scheme, so a naive "resolve it and see" check would
    // wave it through — but it is not in-collection, and chat rejects it today.
    // The divergence being corrected is relative PATHS, nothing wider.
    // DIES ON: removing the `href.startsWith('//')` guard.
    render(<KnowledgeMarkdown content={'[x](//evil.test/steal)'} />)
    const link = screen.getByTestId('markdown-link')
    expect(link.tagName).toBe('SPAN')
    expect(link.getAttribute('title')).toBe('Link removed: unsafe URL scheme')
  })

  it('hands an absolute http(s) link to chat’s own renderer, unchanged', () => {
    // DIES ON: the relative branch swallowing absolute links too — the
    // new-tab + rel="noopener noreferrer" treatment chat applies would be lost.
    render(<KnowledgeMarkdown content={'[site](https://example.test/a)'} />)
    const link = screen.getByTestId('markdown-link')
    expect(link.tagName).toBe('A')
    expect(link.getAttribute('href')).toBe('https://example.test/a')
    expect(link.getAttribute('target')).toBe('_blank')
    expect(link.getAttribute('rel')).toBe('noopener noreferrer')
  })
})

describe('KB markdown — inherits chat’s pipeline (FR-013a, FR-013b)', () => {
  it('takes every shared element renderer BY REFERENCE', () => {
    // A copy that merely looks the same is exactly the drift this codebase has
    // already suffered three times. Reference identity is the only assertion
    // that cannot be satisfied by a duplicate.
    // DIES ON: redefining any of p/h1/h2/h3/ul/ol/li/strong/em/blockquote/
    // table/th/td/hr locally in kbMarkdownComponents.
    const shared = commonMarkdownComponents as unknown as Record<string, unknown>
    const kb = kbMarkdownComponents as unknown as Record<string, unknown>
    for (const key of Object.keys(shared)) {
      expect(kb[key], `components.${key} must be the shared renderer, not a copy`).toBe(shared[key])
    }
    expect(Object.keys(shared).length).toBeGreaterThan(10)
  })

  it('diverges from chat in exactly the two permitted slots', () => {
    // FR-013b: the `a` slot and appended remark plugins. `pre`, `code`, `span`
    // and `img` are chat's own arrangement of shared parts, and are the only
    // other keys allowed to exist beyond the shared map.
    // DIES ON: adding any further override (e.g. a KB-only `h1`).
    const extraKeys = Object.keys(kbMarkdownComponents).filter(
      (k) => !(k in (commonMarkdownComponents as unknown as Record<string, unknown>)),
    )
    expect(extraKeys.sort()).toEqual(['a', 'code', 'img', 'pre', 'span'])
  })

  it('appends exactly one remark plugin to chat’s list and no rehype plugin', () => {
    // DIES ON: adding a second KB-only remark plugin without a spec change, or
    // slipping one into the rehype list.
    expect(KB_REMARK_PLUGINS).toHaveLength(3)
    expect(KB_REMARK_PLUGINS[2]).toBe(remarkStripPrivateComments)
    expect(KB_REHYPE_PLUGINS).toHaveLength(2)
    expect(KB_REHYPE_PLUGINS).not.toContain(remarkStripPrivateComments)
  })

  it('routes a mermaid fence to the shared diagram', () => {
    // DIES ON: dropping the `language === 'mermaid'` branch — the diagram
    // reverts to highlighted source, the first of the three historical drifts.
    render(<KnowledgeMarkdown content={'```mermaid\ngraph TD; A-->B\n```'} />)
    expect(screen.getByTestId('mermaid-diagram').textContent).toBe('graph TD; A-->B')
    expect(screen.queryByTestId('shiki')).toBeNull()
  })

  it('routes a non-mermaid fence through the shared Shiki block', () => {
    // DIES ON: rendering block code as a plain <pre><code> — the third
    // historical drift (highlighting lost outside chat).
    render(<KnowledgeMarkdown content={'```ts\nconst x = 1\n```'} />)
    expect(screen.getByTestId('shiki').textContent).toContain('const x = 1')
    expect(screen.queryByTestId('mermaid-diagram')).toBeNull()
  })

  it('keeps a bare, languageless fence a BLOCK', () => {
    // classifyFence detects block-ness by the newline, not by a `language-`
    // class. DIES ON: keying block-ness on the className alone — the second
    // historical drift, which collapses the fence to inline code.
    render(<KnowledgeMarkdown content={'```\nline one\nline two\n```'} />)
    expect(screen.getByTestId('shiki').textContent).toContain('line one')
  })

  it('renders inline code inline', () => {
    // DIES ON: treating every `code` node as a block.
    render(<KnowledgeMarkdown content={'use `graph TD` inline'} />)
    expect(screen.queryByTestId('shiki')).toBeNull()
    expect(screen.queryByTestId('mermaid-diagram')).toBeNull()
    expect(document.body.textContent).toContain('graph TD')
  })

  it('renders an image through the shared image renderer', () => {
    // DIES ON: replacing `img: MarkdownImage` with a plain <img>, which loses
    // the src allow-list and the lightbox.
    render(<KnowledgeMarkdown content={'![alt](https://example.test/a.png)'} />)
    expect(screen.getByTestId('chat-image').getAttribute('src')).toBe('https://example.test/a.png')
  })
})

describe('KB markdown — the components map is module scope (FR-013c)', () => {
  // react-markdown keys each ENTRY by object reference and treats it as that node
  // type's component type. The entries that are arrow functions written into the
  // map literal — `pre` and `code` — get a fresh identity every time that literal
  // is evaluated, so a map built inside the component makes React unmount and
  // remount the whole block on every render. This slot re-renders on every
  // keystroke, because LibraryTextPreview renders the live draft.
  //
  // The oracle has to be an element rendered by one of those literal arrow
  // functions. Asserting on a paragraph does NOT work and was a false green in an
  // earlier draft of this test: `p` comes from the imported shared map, so its
  // reference survives the literal being rebuilt and nothing remounts.
  //
  // DIES ON: moving the `kbMarkdownComponents` literal (and the plugin arrays)
  // inside KnowledgeMarkdown — verified red.
  // Does NOT die on merely spreading the map at the call site
  // (`components={{ ...kbMarkdownComponents }}`), and correctly so: that copies a
  // container whose entries are still the same references, and react-markdown
  // never looks at the container's identity.

  // Only ONE test lives here on purpose. A companion asserting the same thing on
  // the `a` element was written and then deleted: `KbMarkdownLink` is a
  // module-scope function declaration, so its reference survives the map literal
  // being rebuilt and no mutation could kill that test. A test nothing can fail
  // reports safety it does not provide.
  it('keeps the block-code element mounted across re-renders', () => {
    const { rerender } = render(<KnowledgeMarkdown content={'```ts\nconst x = 1\n```'} />)
    const first = screen.getByTestId('shiki')
    rerender(<KnowledgeMarkdown content={'```ts\nconst x = 1\n```'} />)
    expect(screen.getByTestId('shiki')).toBe(first)
  })
})

describe('LibraryMarkdownPreview mounts the STAGE 2 reading view (FR-013a, US-7)', () => {
  it('renders the view slot through the KB reading view, not chat’s renderer and not stage 1', () => {
    // The composition is worth nothing if the Library still mounts
    // HistoricalMessageMarkdown — and stage 2 is worth nothing if the Library
    // mounts stage 1, which is what it did: KnowledgeReader, KnowledgeOutline,
    // KnowledgeBacklinks and preview/knowledgeMarkdown.tsx had no importer
    // outside their own tests, so a reader opening a note got `[[Wikilinks]]`
    // as literal text while 138 tests asserted otherwise.
    //
    // Proven behaviourally by three things stage 1 cannot do: the reading
    // column exists, the private comment is hidden, and the WIKILINK is a link.
    //
    // DIES ON: reverting renderView to <KnowledgeMarkdown …/> (no reading
    // column, wikilink stays literal) or to <HistoricalMessageMarkdown …/>
    // (the comment survives too).
    render(
      <LibraryMarkdownPreview
        workspaceId="ws-1"
        entry={entry}
        content={'[[Other Note]] %%hidden aside%% visible'}
      />,
    )
    const body = document.body.textContent ?? ''
    expect(body).not.toContain('hidden aside')
    expect(body).toContain('visible')
    expect(screen.getByTestId('knowledge-reader-article')).toBeInTheDocument()
    expect(screen.getByTestId('markdown-link').getAttribute('data-kb-target')).toBe('Other Note')
  })
})
