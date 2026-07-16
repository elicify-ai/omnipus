import { useEffect, useState } from 'react'

/**
 * useMediaQuery — subscribe to a CSS media query and re-render on change.
 *
 * Needed anywhere a Tailwind responsive class (`sm:`, `md:`, …) can't gate
 * the behavior because the thing being gated isn't CSS — a boolean HTML
 * attribute (e.g. `inert`), conditional JS logic, etc. See AppShell's
 * <sm docked-browser takeover for the motivating case.
 *
 * SSR/no-matchMedia-safe: returns `false` until the environment can answer.
 */
export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState<boolean>(() =>
    typeof window !== 'undefined' && typeof window.matchMedia === 'function'
      ? window.matchMedia(query).matches
      : false
  )

  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return undefined
    const mql = window.matchMedia(query)
    // Sync immediately in case the query string changed between renders.
    setMatches(mql.matches)
    function handleChange(e: MediaQueryListEvent) {
      setMatches(e.matches)
    }
    mql.addEventListener('change', handleChange)
    return () => mql.removeEventListener('change', handleChange)
  }, [query])

  return matches
}
