import { useEffect, type RefObject } from 'react'

/**
 * Stamp tabindex="0" on THIRD-PARTY-rendered interactive elements inside one
 * container — e.g. React Flow's <Controls> zoom/fit buttons, which the library
 * renders itself so there is no JSX site in this repo to carry the attribute.
 *
 * Repo convention: every native interactive element carries an EXPLICIT
 * tabIndex in source (see src/lib/tabindex-convention.test.ts) because
 * WebKit's default Tab policy (Safari macOS default prefs; iPadOS hardware
 * keyboard without Full Keyboard Access) only visits form fields and
 * explicitly-tabindexed elements. This scoped hook is the sanctioned
 * exception for library-rendered DOM — do NOT reach for it for elements this
 * repo renders itself; put tabIndex={0} in the JSX instead.
 *
 * The observer watches childList only and the stamp writes an attribute, so
 * it cannot feed itself. Elements that already manage a tabindex (roving
 * widgets) are never touched.
 */
export function useLibraryTabIndex(
  ref: RefObject<HTMLElement | null>,
  selector = 'button:not([tabindex]), a[href]:not([tabindex])'
): void {
  useEffect(() => {
    const root = ref.current
    if (!root) return
    const stamp = () =>
      root.querySelectorAll(selector).forEach((el) => el.setAttribute('tabindex', '0'))
    stamp()
    const observer = new MutationObserver(stamp)
    observer.observe(root, { childList: true, subtree: true })
    return () => observer.disconnect()
  }, [ref, selector])
}
