import { Component, type ReactNode } from 'react'
import { Button } from '@/components/ui/button'

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
        <div className="flex flex-col items-center justify-center p-[var(--space-5)] gap-[var(--space-2-5)] text-[length:var(--type-body-compact-size)]" style={{ color: 'var(--color-muted)' }}>
          <p style={{ color: 'var(--color-error)' }}>Something went wrong</p>
          <p className="text-[length:var(--type-utility-xs-size)]">{error?.message}</p>
          {isChunkLoadError ? (
            <Button
              variant="outline"
              onClick={() => window.location.reload()}
              className="h-auto px-[var(--space-2-5)] py-[var(--space-1)] rounded-md font-[var(--font-weight-regular)] text-[length:var(--type-utility-xs-size)] hover:bg-transparent"
              style={{ borderColor: 'var(--color-border)', color: 'var(--color-secondary)' }}
            >
              Reload
            </Button>
          ) : (
            <Button
              variant="outline"
              onClick={() => this.setState({ hasError: false, error: null })}
              className="h-auto px-[var(--space-2-5)] py-[var(--space-1)] rounded-md font-[var(--font-weight-regular)] text-[length:var(--type-utility-xs-size)] hover:bg-transparent"
              style={{ borderColor: 'var(--color-border)', color: 'var(--color-secondary)' }}
            >
              Try again
            </Button>
          )}
        </div>
      )
    }
    return this.props.children
  }
}
