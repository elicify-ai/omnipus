// SettingsSectionBody — maps the open Settings section (Settings redesign
// #1519, FR-001) to the section files that hold its settings. The shell
// (SettingsScreen) owns the heading, the restart banner and the leave prompt;
// this file only chooses what renders below the heading.

import { ProvidersSection } from './ProvidersSection'
import { IntegrationsSection } from './IntegrationsSection'
import { MemorySection } from './MemorySection'
import { SystemAgentsSection } from './SystemAgentsSection'
import { ThirdPartyAgentsSection } from './ThirdPartyAgentsSection'
import { LimitsSection } from './LimitsSection'
import { SectionSaveScope } from './SectionSaveScope'
import { SecuritySection } from './SecuritySection'
import { GodModeControl } from './GodModeControl'
import { SandboxSection } from './SandboxSection'
import { ExecProxyStatusCard } from './ExecProxyStatusCard'
import { GatewaySection } from './GatewaySection'
import { DataRetentionSection } from './DataRetentionSection'
import { AboutSection } from './AboutSection'
import type { SettingsSectionId } from './settingsSections'

export interface SettingsSectionBodyProps { // not-wire-format: React props
  section: SettingsSectionId
  prefillOverride?: { provider: string; model: string }
  focusGodMode?: boolean
  backupRemoved?: boolean
}

function Group({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section aria-label={title} className="space-y-[var(--space-2)]">
      <h3 className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)]">{title}</h3>
      {children}
    </section>
  )
}

export function SettingsSectionBody({ section, prefillOverride, focusGodMode, backupRemoved }: SettingsSectionBodyProps) {
  switch (section) {
    case 'providers':
      return (
        <SectionSaveScope>
          <div className="space-y-[var(--space-6)]">
            <ProvidersSection />
            <Group title="Voice input and web search">
              <IntegrationsSection />
            </Group>
          </div>
        </SectionSaveScope>
      )
    case 'memory':
      return <MemorySection />
    case 'system-agents':
      return <SystemAgentsSection />
    case 'third-party-agents':
      return <ThirdPartyAgentsSection />
    case 'limits':
      return <LimitsSection prefillOverride={prefillOverride} />
    case 'protection':
      return <SecuritySection view="protection" godMode={<GodModeControl focusOnMount={focusGodMode} />} />
    case 'sandbox':
      return (
        <div className="space-y-[var(--space-6)]">
          <SandboxSection />
          <Group title="Network proxy for shell commands">
            <ExecProxyStatusCard />
          </Group>
        </div>
      )
    case 'keys':
      return <SecuritySection view="keys" />
    case 'gateway':
      return <GatewaySection />
    case 'data':
      return <DataRetentionSection backupRemoved={backupRemoved} />
    case 'about':
      return <AboutSection />
  }
}
