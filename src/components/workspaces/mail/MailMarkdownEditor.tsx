import { useEffect, useRef, useState, type ReactNode } from 'react'
import type { Editor } from '@tiptap/core'
import { Markdown } from '@tiptap/markdown'
import { EditorContent, useEditor, useEditorState } from '@tiptap/react'
import { StarterKit } from '@tiptap/starter-kit'
import { Underline } from '@tiptap/extension-underline'
import { ArrowClockwise, ArrowCounterClockwise, ListBullets } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Field } from '@/components/ui/field'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Tooltip } from '@/components/ui/tooltip'
import './mail-markdown-editor.css'

export interface MailMarkdownEditorProps {
  markdown: string
  onMarkdownChange(markdown: string): void
  ariaLabel?: string
}

export type MailEditorAction =
  | 'bold'
  | 'italic'
  | 'underline'
  | 'heading'
  | 'bullet-list'
  | 'ordered-list'
  | 'blockquote'
  | 'undo'
  | 'redo'

/**
 * Goldmark accepts inline HTML and the outbound allowlist explicitly keeps
 * <u>. Tiptap's stock `++underline++` Markdown is not understood by Goldmark,
 * so this extension serializes the mark to the compatible form and parses it
 * back when a saved draft is reopened.
 */
const MailUnderline = Underline.extend({
  renderMarkdown(node, helpers) {
    return `<u>${helpers.renderChildren(node)}</u>`
  },
  markdownTokenizer: {
    name: 'underline',
    level: 'inline',
    start(source) {
      return source.indexOf('<u>')
    },
    tokenize(source, _tokens, lexer) {
      const match = /^<u>([\s\S]+?)<\/u>/.exec(source)
      if (!match) return undefined
      return {
        type: 'underline',
        raw: match[0],
        text: match[1],
        tokens: lexer.inlineTokens(match[1]),
      }
    },
  },
})

export function createMailEditorExtensions() {
  return [
    StarterKit.configure({
      heading: { levels: [1, 2, 3] },
      link: {
        autolink: true,
        defaultProtocol: 'https',
        linkOnPaste: true,
        openOnClick: false,
        isAllowedUri: isAllowedMailLink,
      },
      underline: false,
    }),
    MailUnderline,
    Markdown.configure({ markedOptions: { gfm: true, breaks: false } }),
  ]
}

export function runMailEditorAction(editor: Editor, action: MailEditorAction): boolean {
  const chain = editor.chain().focus()
  switch (action) {
    case 'bold': return chain.toggleBold().run()
    case 'italic': return chain.toggleItalic().run()
    case 'underline': return chain.toggleUnderline().run()
    case 'heading': return chain.toggleHeading({ level: 2 }).run()
    case 'bullet-list': return chain.toggleBulletList().run()
    case 'ordered-list': return chain.toggleOrderedList().run()
    case 'blockquote': return chain.toggleBlockquote().run()
    case 'undo': return chain.undo().run()
    case 'redo': return chain.redo().run()
  }
}

function isAllowedMailLink(value: string): boolean {
  try {
    const parsed = new URL(value)
    return parsed.protocol === 'http:' || parsed.protocol === 'https:' || parsed.protocol === 'mailto:'
  } catch {
    return false
  }
}

function normalizeMailLink(value: string): string | null {
  const trimmed = value.trim()
  if (/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(trimmed)) return `mailto:${trimmed}`
  const candidate = /^[a-z][a-z\d+.-]*:/i.test(trimmed) ? trimmed : `https://${trimmed}`
  return isAllowedMailLink(candidate) ? candidate : null
}

function ToolbarButton({
  label,
  shortcut,
  active = false,
  disabled = false,
  onClick,
  children,
}: {
  label: string
  shortcut?: string
  active?: boolean
  disabled?: boolean
  onClick(): void
  children: ReactNode
}) {
  const description = shortcut ? `${label} (${shortcut})` : label
  return (
    <Tooltip content={description} interactive>
      <IconButton
        size="sm"
        variant={active ? 'secondary' : 'ghost'}
        aria-label={description}
        aria-pressed={active}
        disabled={disabled}
        onClick={onClick}
      >
        {children}
      </IconButton>
    </Tooltip>
  )
}

export function MailMarkdownEditor({ markdown, onMarkdownChange, ariaLabel = 'Message' }: MailMarkdownEditorProps) {
  const onChangeRef = useRef(onMarkdownChange)
  onChangeRef.current = onMarkdownChange
  const [linkOpen, setLinkOpen] = useState(false)
  const [linkValue, setLinkValue] = useState('')
  const [linkError, setLinkError] = useState<string | null>(null)

  const editor = useEditor({
    extensions: createMailEditorExtensions(),
    content: markdown,
    contentType: 'markdown',
    editorProps: {
      attributes: {
        'aria-label': ariaLabel,
        'aria-multiline': 'true',
        'class': 'mail-markdown-editor-content',
        'data-no-focus-ring': 'true',
        'role': 'textbox',
      },
    },
    onUpdate: ({ editor: nextEditor }) => onChangeRef.current(nextEditor.getMarkdown()),
  })

  useEffect(() => {
    if (!editor || editor.getMarkdown() === markdown) return
    editor.commands.setContent(markdown, { contentType: 'markdown', emitUpdate: false })
  }, [editor, markdown])

  const state = useEditorState({
    editor,
    selector: ({ editor: current }) => ({
      bold: current?.isActive('bold') ?? false,
      italic: current?.isActive('italic') ?? false,
      underline: current?.isActive('underline') ?? false,
      heading: current?.isActive('heading', { level: 2 }) ?? false,
      bulletList: current?.isActive('bulletList') ?? false,
      orderedList: current?.isActive('orderedList') ?? false,
      blockquote: current?.isActive('blockquote') ?? false,
      link: current?.isActive('link') ?? false,
      canUndo: current?.can().undo() ?? false,
      canRedo: current?.can().redo() ?? false,
    }),
  })

  if (!editor) return <p role="status">Loading editor…</p>

  const apply = (action: MailEditorAction) => runMailEditorAction(editor, action)
  const openLinkDialog = () => {
    setLinkValue(String(editor.getAttributes('link').href ?? ''))
    setLinkError(null)
    setLinkOpen(true)
  }
  const saveLink = () => {
    const href = normalizeMailLink(linkValue)
    if (!href) {
      setLinkError('Use an http, https or email address.')
      return
    }
    editor.chain().focus().extendMarkRange('link').setLink({ href }).run()
    setLinkOpen(false)
  }

  return (
    <div className="mail-markdown-editor" data-testid="mail-markdown-editor">
      <div className="mail-markdown-editor-toolbar" role="toolbar" aria-label="Message formatting">
        <div className="mail-markdown-editor-toolbar-group">
          <ToolbarButton label="Bold" shortcut="⌘B" active={state.bold} onClick={() => apply('bold')}>
            <span className="mail-markdown-editor-glyph mail-markdown-editor-glyph-bold" aria-hidden="true">B</span>
          </ToolbarButton>
          <ToolbarButton label="Italic" shortcut="⌘I" active={state.italic} onClick={() => apply('italic')}>
            <span className="mail-markdown-editor-glyph mail-markdown-editor-glyph-italic" aria-hidden="true">I</span>
          </ToolbarButton>
          <ToolbarButton label="Underline" shortcut="⌘U" active={state.underline} onClick={() => apply('underline')}>
            <span className="mail-markdown-editor-glyph mail-markdown-editor-glyph-underline" aria-hidden="true">U</span>
          </ToolbarButton>
        </div>
        <div className="mail-markdown-editor-separator" aria-hidden="true" />
        <div className="mail-markdown-editor-toolbar-group">
          <ToolbarButton label="Heading" active={state.heading} onClick={() => apply('heading')}>
            <span className="mail-markdown-editor-glyph" aria-hidden="true">H2</span>
          </ToolbarButton>
          <ToolbarButton label="Bulleted list" active={state.bulletList} onClick={() => apply('bullet-list')}>
            <ListBullets size={16} aria-hidden="true" />
          </ToolbarButton>
          <ToolbarButton label="Numbered list" active={state.orderedList} onClick={() => apply('ordered-list')}>
            <span className="mail-markdown-editor-glyph" aria-hidden="true">1.</span>
          </ToolbarButton>
          <ToolbarButton label="Quote" active={state.blockquote} onClick={() => apply('blockquote')}>
            <span className="mail-markdown-editor-glyph" aria-hidden="true">“</span>
          </ToolbarButton>
          <ToolbarButton label="Link" shortcut="⌘K" active={state.link} onClick={openLinkDialog}>
            <span className="mail-markdown-editor-glyph mail-markdown-editor-glyph-link" aria-hidden="true">Link</span>
          </ToolbarButton>
        </div>
        <div className="mail-markdown-editor-toolbar-spacer" />
        <div className="mail-markdown-editor-toolbar-group">
          <ToolbarButton label="Undo" shortcut="⌘Z" disabled={!state.canUndo} onClick={() => apply('undo')}>
            <ArrowCounterClockwise size={16} aria-hidden="true" />
          </ToolbarButton>
          <ToolbarButton label="Redo" shortcut="⇧⌘Z" disabled={!state.canRedo} onClick={() => apply('redo')}>
            <ArrowClockwise size={16} aria-hidden="true" />
          </ToolbarButton>
        </div>
      </div>
      <EditorContent editor={editor} className="mail-markdown-editor-surface" />
      <Dialog open={linkOpen} onOpenChange={setLinkOpen}>
        <DialogContent className="mail-markdown-editor-link-dialog">
          <DialogHeader>
            <DialogTitle>Add link</DialogTitle>
            <DialogDescription>Link the selected text to a secure web page or email address.</DialogDescription>
          </DialogHeader>
          <Field label="Web or email address" error={linkError ?? undefined}>
            <Input
              autoFocus
              value={linkValue}
              onChange={(event) => {
                setLinkValue(event.target.value)
                setLinkError(null)
              }}
              onKeyDown={(event) => {
                if (event.key === 'Enter') {
                  event.preventDefault()
                  saveLink()
                }
              }}
              placeholder="https://example.com"
            />
          </Field>
          <DialogFooter>
            {state.link && (
              <Button
                variant="ghost"
                onClick={() => {
                  editor.chain().focus().extendMarkRange('link').unsetLink().run()
                  setLinkOpen(false)
                }}
              >
                Remove link
              </Button>
            )}
            <Button variant="secondary" onClick={() => setLinkOpen(false)}>Cancel</Button>
            <Button onClick={saveLink}>Apply link</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
