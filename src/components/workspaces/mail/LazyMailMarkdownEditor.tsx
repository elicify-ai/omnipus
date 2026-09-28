import { lazy, Suspense } from 'react'
import type { MailMarkdownEditorProps } from './MailMarkdownEditor'

const MailMarkdownEditor = lazy(async () => {
  const module = await import('./MailMarkdownEditor')
  return { default: module.MailMarkdownEditor }
})

/** Keeps Tiptap, ProseMirror and the editor stylesheet out of Mail's own
 * chunk until a human opens compose or chooses Edit on a draft. */
export function LazyMailMarkdownEditor(props: MailMarkdownEditorProps) {
  return (
    <Suspense fallback={<p role="status">Loading editor…</p>}>
      <MailMarkdownEditor {...props} />
    </Suspense>
  )
}
