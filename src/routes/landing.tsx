import { createFileRoute } from '@tanstack/react-router'
import {
  ShieldCheck,
  Cube,
  ArrowsClockwise,
  GithubLogo,
  Lightning,
  LockKey,
  Users,
} from '@phosphor-icons/react'
import { Card, CardContent } from '@/components/ui/card'
import OmnipusAvatar from '@/assets/logo/omnipus-avatar.svg?url'

const FEATURES = [
  {
    Icon: ShieldCheck,
    title: 'Kernel-Level Sandboxing',
    description:
      'Landlock + seccomp on Linux. Job Objects + Restricted Tokens on Windows. Your agents operate in security boundaries by default.',
  },
  {
    Icon: ArrowsClockwise,
    title: 'Multi-Agent Orchestration',
    description:
      'Spawn, coordinate, and monitor fleets of specialized agents working in parallel on complex tasks.',
  },
  {
    Icon: Cube,
    title: 'Single Binary Deployment',
    description:
      'One Go binary. Embedded web UI. No Docker, no node_modules, no runtime dependencies. Runs anywhere.',
  },
  {
    Icon: LockKey,
    title: 'AES-256 Credential Vault',
    description:
      'Credentials encrypted with AES-256-GCM and Argon2id KDF. Never stored in plaintext. Never in config files.',
  },
  {
    Icon: Lightning,
    title: 'Zero-IPC Go Channels',
    description:
      'Built-in channels (Discord, Slack, Telegram, WhatsApp) compiled directly into the binary. Zero inter-process overhead.',
  },
  {
    Icon: Users,
    title: 'Multi-Variant Architecture',
    description:
      'Open Source, Desktop, and Cloud variants sharing the same agentic core and component library.',
  },
] as const

// US-6: Static landing page — hero, features, footer
function LandingPage() {
  return (
    <div className="min-h-screen bg-[var(--color-primary)] text-[var(--color-secondary)]">
      {/* Hero */}
      <section className="flex flex-col items-center justify-center min-h-screen px-6 py-20 text-center gap-8">
        {/* Warning fix #6: explicit size at all 3 breakpoints (phone/tablet/desktop) */}
        <img
          src={OmnipusAvatar}
          alt="omnipus.ai mark"
          className="h-24 w-24 sm:h-36 sm:w-36 md:h-48 md:w-48 drop-shadow-2xl"
        />
        <div className="max-w-2xl">
          <h1 className="font-headline text-5xl md:text-7xl font-bold lowercase text-[var(--color-secondary)] leading-tight mb-4">
            omnipus<span className="text-[var(--color-accent)]">.ai</span>
          </h1>
          {/* US-6: Tagline per brand guidelines */}
          <p className="text-[var(--color-accent)] font-headline text-xl md:text-2xl font-bold mb-4">
            Elite Simplicity. Sovereign Control.
          </p>
          <p className="text-[var(--color-muted)] text-lg leading-relaxed">
            The autonomous command center with military-grade security. Hire AI agents safely.
            Pay for outcomes. Retain full control.
          </p>
        </div>

        {/* US-6: Forge Gold CTA */}
        <div className="flex flex-col sm:flex-row gap-4 mt-4">
          <a
            href="https://github.com/omnipus-ai/omnipus"
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-2 px-6 py-3 rounded-lg bg-[var(--color-accent)] text-[var(--color-primary)] font-headline font-bold text-base hover:bg-[var(--color-accent-hover)] transition-colors"
          >
            <GithubLogo size={20} />
            View on GitHub
          </a>
          <a
            href="/#/"
            className="inline-flex items-center gap-2 px-6 py-3 rounded-lg border border-[var(--color-border)] text-[var(--color-secondary)] font-medium text-base hover:bg-[var(--color-surface-2)] transition-colors"
          >
            Open App
          </a>
        </div>
      </section>

      {/* Features — Warning fix #5: use <Card> component */}
      <section className="px-6 py-20 max-w-6xl mx-auto">
        <h2 className="font-headline text-3xl md:text-4xl font-bold text-center mb-4">
          Built for professionals who demand more
        </h2>
        <p className="text-[var(--color-muted)] text-center text-lg mb-16 max-w-2xl mx-auto">
          Every feature is a deliberate choice. Nothing is bolted on.
        </p>
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-6">
          {FEATURES.map(({ Icon, title, description }) => (
            <Card key={title}>
              <CardContent className="flex flex-col gap-4 pt-6">
                <Icon size={32} weight="duotone" className="text-[var(--color-accent)]" />
                <h3 className="font-headline text-lg font-bold">{title}</h3>
                <p className="text-[var(--color-muted)] text-sm leading-relaxed">{description}</p>
              </CardContent>
            </Card>
          ))}
        </div>
      </section>

      {/* Footer */}
      <footer className="border-t border-[var(--color-border)] px-6 py-10">
        <div className="max-w-6xl mx-auto flex flex-col sm:flex-row items-center justify-between gap-4">
          <div className="flex items-center gap-3">
            <img src={OmnipusAvatar} alt="Omnipus" className="h-8 w-8" />
            <span className="font-headline font-bold lowercase text-[var(--color-secondary)]">omnipus<span className="text-[var(--color-accent)]">.ai</span></span>
          </div>
          <div className="flex items-center gap-6 text-sm text-[var(--color-muted)]">
            <a
              href="https://github.com/omnipus-ai/omnipus"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1.5 hover:text-[var(--color-secondary)] transition-colors"
            >
              <GithubLogo size={16} />
              GitHub
            </a>
            <a
              href="https://github.com/omnipus-ai/omnipus"
              target="_blank"
              rel="noopener noreferrer"
              className="hover:text-[var(--color-secondary)] transition-colors"
            >
              Documentation
            </a>
          </div>
          <p className="text-sm text-[var(--color-muted)]">
            &copy; {new Date().getFullYear()} Omnipus. MIT License.
          </p>
        </div>
      </footer>
    </div>
  )
}

export const Route = createFileRoute('/landing')({
  component: LandingPage,
})
