import { Component, type ReactNode } from 'react'

interface Props {
  children: ReactNode
  fallback?: ReactNode
}

interface State {
  hasError: boolean
  error: Error | null
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { hasError: false, error: null }

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error }
  }

  componentDidCatch(error: Error, info: React.ErrorInfo) {
    console.error('[ErrorBoundary] Caught error:', error, info)
  }

  render() {
    if (this.state.hasError) {
      const error = this.state.error
      // A failed lazy chunk import (stale chunk hash after a redeploy) caches the
      // rejected module promise, so simply flipping hasError=false re-renders the
      // same lazy component and re-throws immediately — an infinite loop. For that
      // class of error, the only real recovery is a full reload that re-fetches the
      // current asset manifest, so offer a "Reload" button instead of "Try again".
      const isChunkLoadError =
        error?.name === 'ChunkLoadError' ||
        /Loading chunk|dynamically imported module|Failed to fetch|importing a module script failed/i.test(
          error?.message ?? '',
        )

      return this.props.fallback ?? (
        <div className="flex flex-col items-center justify-center p-8 gap-3 text-sm" style={{ color: 'var(--color-muted)' }}>
          <p style={{ color: 'var(--color-error)' }}>Something went wrong</p>
          <p className="text-xs">{error?.message}</p>
          {isChunkLoadError ? (
            <button
              onClick={() => window.location.reload()}
              className="px-3 py-1.5 rounded-md text-xs border transition-colors"
              style={{ borderColor: 'var(--color-border)', color: 'var(--color-secondary)' }}
            >
              Reload
            </button>
          ) : (
            <button
              onClick={() => this.setState({ hasError: false, error: null })}
              className="px-3 py-1.5 rounded-md text-xs border transition-colors"
              style={{ borderColor: 'var(--color-border)', color: 'var(--color-secondary)' }}
            >
              Try again
            </button>
          )}
        </div>
      )
    }
    return this.props.children
  }
}
