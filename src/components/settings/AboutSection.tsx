import { useQuery } from '@tanstack/react-query'
import { Copy, GithubLogo, ArrowSquareOut } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
import { fetchAboutInfo } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import omnipusLogo from '@/assets/logo/omnipus-logo.svg'

function formatUptime(seconds: number): string {
  if (seconds < 60) return `${seconds}s`
  const m = Math.floor(seconds / 60)
  if (m < 60) return `${m}m ${seconds % 60}s`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ${m % 60}m`
  const d = Math.floor(h / 24)
  return `${d}d ${h % 24}h`
}

export function AboutSection() {
  const { addToast } = useUiStore()

  const { data: info, isLoading, isError } = useQuery({
    queryKey: ['about'],
    queryFn: fetchAboutInfo,
    retry: 1,
  })

  async function copySystemInfo() {
    if (!info) return
    const text = [
      `Omnipus ${info.version}`,
      `Go: ${info.go_version}`,
      `OS: ${info.os}`,
      `Arch: ${info.arch}`,
      `Uptime: ${formatUptime(info.uptime_seconds)}`,
    ].join('\n')
    try {
      await navigator.clipboard.writeText(text)
      addToast({ message: 'System info copied to clipboard', variant: 'success' })
    } catch {
      addToast({ message: 'Could not copy to clipboard', variant: 'error' })
    }
  }

  return (
    <div className="space-y-6">
      {/* Logo + tagline — FR-109: real logo, not placeholder */}
      <div className="flex flex-col items-center py-6 gap-3">
        <img
          src={omnipusLogo}
          alt="omnipus.ai logo"
          data-testid="omnipus-logo"
          className="w-16 h-16 select-none"
        />
        <div className="text-center">
          <h1 className="font-headline font-bold text-xl lowercase text-[var(--color-secondary)]">omnipus<span className="text-[var(--color-accent)]">.ai</span></h1>
          <p className="text-xs text-[var(--color-muted)] mt-0.5">The Sovereign Deep — Agentic Core</p>
        </div>
      </div>

      <Separator />

      {/* System info */}
      <section className="space-y-3">
        <div className="flex items-center justify-between">
          <h3 className="text-xs font-semibold text-[var(--color-muted)] uppercase tracking-wider">System Info</h3>
          <Button
            variant="outline"
            size="sm"
            className="h-7 px-2 gap-1 text-xs"
            onClick={copySystemInfo}
            disabled={!info}
          >
            <Copy size={11} />
            Copy
          </Button>
        </div>

        <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] divide-y divide-[var(--color-border)]">
          {isLoading && (
            <div className="p-4 text-sm text-[var(--color-muted)]">Loading system info...</div>
          )}
          {isError && (
            <div className="p-4 text-sm text-[var(--color-error)]">
              Could not fetch system info — gateway may be offline.
            </div>
          )}
          {info && (
            <>
              <InfoRow
                label="Version"
                value={info.version === 'dev' ? 'development build' : info.version}
                mono
                testId="build-version"
              />
              <InfoRow label="Go version" value={info.go_version} mono />
              <InfoRow label="Operating system" value={info.os} mono />
              <InfoRow label="Architecture" value={info.arch} mono />
              <InfoRow label="Uptime" value={formatUptime(info.uptime_seconds)} mono />
            </>
          )}
        </div>
      </section>

      <Separator />

      {/* Open source */}
      <section className="space-y-3">
        <h3 className="text-xs font-semibold text-[var(--color-muted)] uppercase tracking-wider">Open Source</h3>
        <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 flex items-center justify-between">
          <div>
            <p className="text-sm text-[var(--color-secondary)]">GitHub Repository</p>
            <p className="text-xs text-[var(--color-muted)] mt-0.5">
              Source code, issues, and contributions
            </p>
          </div>
          <Button
            variant="outline"
            size="sm"
            className="h-8 gap-1.5 text-xs"
            asChild
          >
            <a href="https://github.com/elicify-ai/omnipus" target="_blank" rel="noopener noreferrer">
              <GithubLogo size={13} />
              View on GitHub
              <ArrowSquareOut size={11} className="text-[var(--color-muted)]" />
            </a>
          </Button>
        </div>
      </section>

      <div className="text-center text-[10px] text-[var(--color-muted)]">
        Omnipus is released under the MIT License &mdash; omnipus.ai
      </div>
    </div>
  )
}

function InfoRow({ label, value, mono, testId }: { label: string; value: string; mono?: boolean; testId?: string }) {
  return (
    <div className="flex items-center justify-between px-4 py-2.5">
      <span className="text-xs text-[var(--color-muted)]">{label}</span>
      <span data-testid={testId} className={`text-xs text-[var(--color-secondary)] ${mono ? 'font-mono' : ''}`}>{value}</span>
    </div>
  )
}
