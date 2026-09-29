// D50's narrow, design-system-native adaptation of Aslam97/minimal-tiptap
// (MIT): Tiptap's official Markdown input/output with only the mail toolbar.
import { useEffect, useRef, useState, type ReactNode } from 'react'
import type { Editor } from '@tiptap/core'
import { Blockquote } from '@tiptap/extension-blockquote'
import { Bold } from '@tiptap/extension-bold'
import { Code as TiptapCode } from '@tiptap/extension-code'
import { Document } from '@tiptap/extension-document'
import { HardBreak } from '@tiptap/extension-hard-break'
import { Heading } from '@tiptap/extension-heading'
import { Italic } from '@tiptap/extension-italic'
import { Link as TiptapLink } from '@tiptap/extension-link'
import { BulletList } from '@tiptap/extension-list/bullet-list'
import { ListItem } from '@tiptap/extension-list/item'
import { ListKeymap } from '@tiptap/extension-list/keymap'
import { OrderedList } from '@tiptap/extension-list/ordered-list'
import { Paragraph } from '@tiptap/extension-paragraph'
import { Strike } from '@tiptap/extension-strike'
import { Text } from '@tiptap/extension-text'
import { UndoRedo } from '@tiptap/extensions/undo-redo'
import { Markdown } from '@tiptap/markdown'
import { EditorContent, useEditor, useEditorState } from '@tiptap/react'
import {
  ArrowClockwise,
  ArrowCounterClockwise,
  Code,
  Link,
  ListBullets,
} from '@phosphor-icons/react'
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
import { Separator } from '@/components/ui/separator'
import { Tooltip } from '@/components/ui/tooltip'
import { isValidMailRecipient } from './MailRecipientInput'
import './mail-markdown-editor.css'

export interface MailMarkdownEditorProps {
  markdown: string
  onMarkdownChange(markdown: string): void
  ariaLabel?: string
  id?: string
  required?: boolean
  'aria-describedby'?: string
  'aria-invalid'?: React.AriaAttributes['aria-invalid']
}

type HeadingLevel = 1 | 2 | 3
export type MailEditorAction =
  | 'bold'
  | 'italic'
  | 'strike'
  | 'heading-1'
  | 'heading-2'
  | 'heading-3'
  | 'bullet-list'
  | 'ordered-list'
  | 'blockquote'
  | 'code'
  | 'undo'
  | 'redo'

function isAllowedMailLink(value: string): boolean {
  try {
    const parsed = new URL(value)
    if (parsed.protocol === 'http:' || parsed.protocol === 'https:') return true
    return parsed.protocol === 'mailto:' && isValidMailRecipient(decodeURIComponent(parsed.pathname))
  } catch {
    return false
  }
}

export function normalizeMailLink(value: string): string | null {
  const trimmed = value.trim()
  if (/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(trimmed)) return `mailto:${trimmed}`
  const candidate = /^[a-z][a-z\d+.-]*:/i.test(trimmed) ? trimmed : `https://${trimmed}`
  return isAllowedMailLink(candidate) ? candidate : null
}

export function createMailEditorExtensions() {
  return [
    Blockquote,
    Bold,
    BulletList,
    TiptapCode,
    Document,
    HardBreak,
    Heading.configure({ levels: [1, 2, 3] }),
    Italic,
    ListItem,
    ListKeymap,
    TiptapLink.configure({
      autolink: true,
      defaultProtocol: 'https',
      linkOnPaste: true,
      openOnClick: false,
      isAllowedUri: isAllowedMailLink,
    }),
    OrderedList,
    Paragraph,
    Strike,
    Text,
    UndoRedo,
    Markdown.configure({ markedOptions: { gfm: true, breaks: false } }),
  ]
}

export function runMailEditorAction(editor: Editor, action: MailEditorAction): boolean {
  const chain = editor.chain().focus()
  switch (action) {
    case 'bold': return chain.toggleBold().run()
    case 'italic': return chain.toggleItalic().run()
    case 'strike': return chain.toggleStrike().run()
    case 'heading-1': return chain.toggleHeading({ level: 1 }).run()
    case 'heading-2': return chain.toggleHeading({ level: 2 }).run()
    case 'heading-3': return chain.toggleHeading({ level: 3 }).run()
    case 'bullet-list': return chain.toggleBulletList().run()
    case 'ordered-list': return chain.toggleOrderedList().run()
    case 'blockquote': return chain.toggleBlockquote().run()
    case 'code': return chain.toggleCode().run()
    case 'undo': return chain.undo().run()
    case 'redo': return chain.redo().run()
  }
}

export function setMailEditorLink(editor: Editor, href: string): boolean {
  return editor.chain().focus().extendMarkRange('link').setLink({ href }).run()
}

function ToolbarButton({
  label,
  shortcut,
  active,
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
  const accessibleName = shortcut === undefined ? label : `${label} (${shortcut})`
  return (
    <Tooltip content={accessibleName} interactive>
      <IconButton
        size="sm"
        variant={active ? 'secondary' : 'ghost'}
        aria-label={accessibleName}
        aria-pressed={active === undefined ? undefined : active}
        disabled={disabled}
        className="mail-markdown-editor-toolbar-button"
        onMouseDown={(event) => event.preventDefault()}
        onClick={onClick}
      >
        {children}
      </IconButton>
    </Tooltip>
  )
}

export function MailMarkdownEditor({
  markdown,
  onMarkdownChange,
  ariaLabel = 'Message',
  id,
  required,
  'aria-describedby': ariaDescribedBy,
  'aria-invalid': ariaInvalid,
}: MailMarkdownEditorProps) {
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
        'aria-placeholder': 'Write your message…',
        'aria-describedby': ariaDescribedBy ?? '',
        'aria-invalid': ariaInvalid ? 'true' : 'false',
        'aria-required': required ? 'true' : 'false',
        'class': 'mail-markdown-editor-content min-h-0 flex-1',
        'data-no-focus-ring': 'true',
        'id': id ?? '',
        'role': 'textbox',
      },
    },
    onUpdate: ({ editor: nextEditor }) => onChangeRef.current(nextEditor.getMarkdown()),
  })

  useEffect(() => {
    if (!editor || editor.getMarkdown() === markdown) return
    editor.commands.setContent(markdown, { contentType: 'markdown', emitUpdate: false })
  }, [editor, markdown])

  // The pre-D50 composer exposed a textarea. Its public DOM contract is kept
  // while the contenteditable remains the one visible, accessible control:
  // legacy consumers that assign `.value` then dispatch `change` still drive
  // the same Markdown state as real Tiptap input.
  useEffect(() => {
    if (!editor) return
    const surface = editor.view.dom as HTMLElement & { value?: string }
    Object.defineProperty(surface, 'value', {
      configurable: true,
      get: () => editor.getMarkdown(),
      set: (value: string) => editor.commands.setContent(String(value), { contentType: 'markdown' }),
    })
    return () => { delete surface.value }
  }, [editor])

  const state = useEditorState({
    editor,
    selector: ({ editor: current }) => ({
      bold: current?.isActive('bold') ?? false,
      canBold: current?.can().chain().focus().toggleBold().run() ?? false,
      italic: current?.isActive('italic') ?? false,
      canItalic: current?.can().chain().focus().toggleItalic().run() ?? false,
      strike: current?.isActive('strike') ?? false,
      canStrike: current?.can().chain().focus().toggleStrike().run() ?? false,
      heading1: current?.isActive('heading', { level: 1 }) ?? false,
      canHeading1: current?.can().chain().focus().toggleHeading({ level: 1 }).run() ?? false,
      heading2: current?.isActive('heading', { level: 2 }) ?? false,
      canHeading2: current?.can().chain().focus().toggleHeading({ level: 2 }).run() ?? false,
      heading3: current?.isActive('heading', { level: 3 }) ?? false,
      canHeading3: current?.can().chain().focus().toggleHeading({ level: 3 }).run() ?? false,
      bulletList: current?.isActive('bulletList') ?? false,
      canBulletList: current?.can().chain().focus().toggleBulletList().run() ?? false,
      orderedList: current?.isActive('orderedList') ?? false,
      canOrderedList: current?.can().chain().focus().toggleOrderedList().run() ?? false,
      blockquote: current?.isActive('blockquote') ?? false,
      canBlockquote: current?.can().chain().focus().toggleBlockquote().run() ?? false,
      code: current?.isActive('code') ?? false,
      canCode: current?.can().chain().focus().toggleCode().run() ?? false,
      link: current?.isActive('link') ?? false,
      canUndo: current?.can().undo() ?? false,
      canRedo: current?.can().redo() ?? false,
    }),
  })

  if (!editor) return <p role="status">Loading editor…</p>

  const apply = (action: MailEditorAction) => runMailEditorAction(editor, action)
  const toggleHeading = (level: HeadingLevel) => apply(`heading-${level}`)
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
    if (setMailEditorLink(editor, href)) {
      setLinkOpen(false)
    } else {
      setLinkError('Select text that can be linked, then try again.')
    }
  }

  return (
    <div
      className="mail-markdown-editor"
      data-testid="mail-markdown-editor"
      onKeyDownCapture={(event) => {
        if ((event.metaKey || event.ctrlKey) && event.key.toLocaleLowerCase() === 'k') {
          event.preventDefault()
          openLinkDialog()
        }
      }}
    >
      <div className="mail-markdown-editor-toolbar" role="group" aria-label="Message formatting">
        <div className="mail-markdown-editor-toolbar-group">
          <ToolbarButton label="Bold" shortcut="Mod+B" active={state.bold} disabled={!state.canBold} onClick={() => apply('bold')}>
            <span className="mail-markdown-editor-glyph mail-markdown-editor-glyph-bold" aria-hidden="true">B</span>
          </ToolbarButton>
          <ToolbarButton label="Italic" shortcut="Mod+I" active={state.italic} disabled={!state.canItalic} onClick={() => apply('italic')}>
            <span className="mail-markdown-editor-glyph mail-markdown-editor-glyph-italic" aria-hidden="true">I</span>
          </ToolbarButton>
          <ToolbarButton label="Strike" shortcut="Mod+Shift+S" active={state.strike} disabled={!state.canStrike} onClick={() => apply('strike')}>
            <span className="mail-markdown-editor-glyph mail-markdown-editor-glyph-strike" aria-hidden="true">S</span>
          </ToolbarButton>
          <ToolbarButton label="Inline code" shortcut="Mod+E" active={state.code} disabled={!state.canCode} onClick={() => apply('code')}>
            <Code size={16} aria-hidden="true" />
          </ToolbarButton>
        </div>
        <Separator orientation="vertical" className="mail-markdown-editor-separator" />
        <div className="mail-markdown-editor-toolbar-group">
          <ToolbarButton label="Heading 1" active={state.heading1} disabled={!state.canHeading1} onClick={() => toggleHeading(1)}>
            <span className="mail-markdown-editor-glyph" aria-hidden="true">H1</span>
          </ToolbarButton>
          <ToolbarButton label="Heading 2" active={state.heading2} disabled={!state.canHeading2} onClick={() => toggleHeading(2)}>
            <span className="mail-markdown-editor-glyph" aria-hidden="true">H2</span>
          </ToolbarButton>
          <ToolbarButton label="Heading 3" active={state.heading3} disabled={!state.canHeading3} onClick={() => toggleHeading(3)}>
            <span className="mail-markdown-editor-glyph" aria-hidden="true">H3</span>
          </ToolbarButton>
        </div>
        <Separator orientation="vertical" className="mail-markdown-editor-separator" />
        <div className="mail-markdown-editor-toolbar-group">
          <ToolbarButton label="Bulleted list" active={state.bulletList} disabled={!state.canBulletList} onClick={() => apply('bullet-list')}>
            <ListBullets size={16} aria-hidden="true" />
          </ToolbarButton>
          <ToolbarButton label="Numbered list" active={state.orderedList} disabled={!state.canOrderedList} onClick={() => apply('ordered-list')}>
            <span className="mail-markdown-editor-glyph" aria-hidden="true">1.</span>
          </ToolbarButton>
          <ToolbarButton label="Blockquote" active={state.blockquote} disabled={!state.canBlockquote} onClick={() => apply('blockquote')}>
            <span className="mail-markdown-editor-glyph" aria-hidden="true">“</span>
          </ToolbarButton>
          <ToolbarButton label="Link" shortcut="Mod+K" active={state.link} onClick={openLinkDialog}>
            <Link size={16} aria-hidden="true" />
          </ToolbarButton>
        </div>
        <div className="mail-markdown-editor-toolbar-spacer" />
        <div className="mail-markdown-editor-toolbar-group">
          <ToolbarButton label="Undo" shortcut="Mod+Z" disabled={!state.canUndo} onClick={() => apply('undo')}>
            <ArrowCounterClockwise size={16} aria-hidden="true" />
          </ToolbarButton>
          <ToolbarButton label="Redo" shortcut="Mod+Shift+Z" disabled={!state.canRedo} onClick={() => apply('redo')}>
            <ArrowClockwise size={16} aria-hidden="true" />
          </ToolbarButton>
        </div>
      </div>
      <EditorContent editor={editor} className="mail-markdown-editor-surface" />
      <Dialog open={linkOpen} onOpenChange={setLinkOpen}>
        <DialogContent>
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
