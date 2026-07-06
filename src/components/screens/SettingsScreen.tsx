import { useQuery } from '@tanstack/react-query'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { ProvidersSection } from '@/components/settings/ProvidersSection'
import { IntegrationsSection } from '@/components/settings/IntegrationsSection'
import { SecuritySection } from '@/components/settings/SecuritySection'
import { GatewaySection } from '@/components/settings/GatewaySection'
import { DataSection } from '@/components/settings/DataSection'
import { AboutSection } from '@/components/settings/AboutSection'
import { DevicesSection } from '@/components/settings/DevicesSection'
import { PerformanceSection } from '@/components/settings/PerformanceSection'
import { ChatSection } from '@/components/settings/ChatSection'
import { MemorySection } from '@/components/settings/MemorySection'
import { ScreenHeader } from '@/components/layout/ScreenHeader'
import { fetchAboutInfo, isDevicePairingEnabled } from '@/lib/api'
// O4: the passive RestartBanner at the top of Settings is replaced by the
// modal-on-save flow (GatewaySection) + persistent Restart control in Gateway tab.
// Operators find the restart action under Settings → Gateway.

export function SettingsScreen() {
  const { data: aboutInfo } = useQuery({
    queryKey: ['about'],
    queryFn: fetchAboutInfo,
  })
  const devicePairingEnabled = isDevicePairingEnabled(aboutInfo)

  return (
    <div className="absolute inset-0 flex flex-col">
      <ScreenHeader title="Settings" />
      <div className="flex-1 overflow-y-auto pb-[env(safe-area-inset-bottom)]">
      <div className="max-w-3xl mx-auto px-4 py-6">
        {/* Header */}
        <div className="mb-6">
          <h1 className="font-headline text-2xl font-bold text-[var(--color-secondary)]">Settings</h1>
          <p className="text-sm text-[var(--color-muted)] mt-0.5">
            Configure providers, integrations, gateway, security, and data management.
            Personal preferences live under Profile.
          </p>
        </div>

        <Tabs defaultValue="providers">
          {/* Sticky tab bar — stays visible while scrolling tab content */}
          <TabsList className="mb-6 flex-wrap h-auto gap-1 sticky top-0 z-10 bg-[var(--color-primary)] py-2 -mx-1 px-1">
            <TabsTrigger data-testid="settings-tab-providers" value="providers">Providers</TabsTrigger>
            <TabsTrigger data-testid="settings-tab-integrations" value="integrations">Integrations</TabsTrigger>
            <TabsTrigger data-testid="settings-tab-security" value="security">Security</TabsTrigger>
            <TabsTrigger value="gateway">Gateway</TabsTrigger>
            <TabsTrigger value="data">Data</TabsTrigger>
            <TabsTrigger value="memory">Memory</TabsTrigger>
            {devicePairingEnabled && <TabsTrigger value="devices">Devices</TabsTrigger>}
            <TabsTrigger value="performance">Performance</TabsTrigger>
            <TabsTrigger value="chat">Chat</TabsTrigger>
            <TabsTrigger data-testid="settings-tab-about" value="about">About</TabsTrigger>
          </TabsList>

          <TabsContent value="providers">
            <ProvidersSection />
          </TabsContent>

          <TabsContent value="integrations">
            <IntegrationsSection />
          </TabsContent>

          <TabsContent value="security">
            <SecuritySection />
          </TabsContent>

          <TabsContent value="gateway">
            <GatewaySection />
          </TabsContent>

          <TabsContent value="data">
            <DataSection />
          </TabsContent>

          <TabsContent value="memory">
            <MemorySection />
          </TabsContent>

          {devicePairingEnabled && (
            <TabsContent value="devices">
              <DevicesSection />
            </TabsContent>
          )}

          <TabsContent value="performance">
            <PerformanceSection />
          </TabsContent>

          <TabsContent value="chat">
            <ChatSection />
          </TabsContent>

          <TabsContent value="about">
            <AboutSection />
          </TabsContent>
        </Tabs>
      </div>
      </div>
    </div>
  )
}
