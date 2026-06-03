import { useQuery } from '@tanstack/react-query'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { ProvidersSection } from '@/components/settings/ProvidersSection'
import { SecuritySection } from '@/components/settings/SecuritySection'
import { GatewaySection } from '@/components/settings/GatewaySection'
import { DataSection } from '@/components/settings/DataSection'
import { ProfileSection } from '@/components/settings/ProfileSection'
import { AboutSection } from '@/components/settings/AboutSection'
import { UsersSection } from '@/components/settings/UsersSection'
import { useAuthStore } from '@/store/auth'
import { DevicesSection } from '@/components/settings/DevicesSection'
import { RestartBanner } from '@/components/settings/RestartBanner'
import { fetchConfig } from '@/lib/api'

export function SettingsScreen() {
  const role = useAuthStore((s) => s.role)
  const isAdmin = role === 'admin'

  const { data: config } = useQuery({
    queryKey: ['config'],
    queryFn: fetchConfig,
    enabled: isAdmin,
    staleTime: 30_000,
  })

  const devModeBypass = Boolean(config?.gateway?.dev_mode_bypass)
  // Access tab is only shown to admins AND when dev_mode_bypass is off.
  const showAccessTab = isAdmin && !devModeBypass

  return (
    <div className="absolute inset-0 overflow-y-auto pb-[env(safe-area-inset-bottom)]">
      <div className="max-w-3xl mx-auto px-4 py-6">
        {/* Header */}
        <div className="mb-6">
          <h1 className="font-headline text-2xl font-bold text-[var(--color-secondary)]">Settings</h1>
          <p className="text-sm text-[var(--color-muted)] mt-0.5">
            Configure gateway, credentials, security, and data management.
          </p>
        </div>

        <RestartBanner />

        <Tabs defaultValue="providers">
          {/* Sticky tab bar — stays visible while scrolling tab content */}
          <TabsList className="mb-6 flex-wrap h-auto gap-1 sticky top-0 z-10 bg-[var(--color-primary)] py-2 -mx-1 px-1">
            <TabsTrigger data-testid="settings-tab-providers" value="providers">Providers</TabsTrigger>
            <TabsTrigger data-testid="settings-tab-security" value="security">Security</TabsTrigger>
            <TabsTrigger value="gateway">Gateway</TabsTrigger>
            <TabsTrigger value="data">Data</TabsTrigger>
            <TabsTrigger value="profile">Profile</TabsTrigger>
            {isAdmin && <TabsTrigger value="devices">Devices</TabsTrigger>}
            {showAccessTab && <TabsTrigger value="access">Access</TabsTrigger>}
            <TabsTrigger data-testid="settings-tab-about" value="about">About</TabsTrigger>
          </TabsList>

          <TabsContent value="providers">
            <ProvidersSection />
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

          <TabsContent value="profile">
            <ProfileSection />
          </TabsContent>

          {isAdmin && (
            <TabsContent value="devices">
              <DevicesSection />
            </TabsContent>
          )}

          {isAdmin && (
            <TabsContent value="access">
              <UsersSection devModeBypass={devModeBypass} />
            </TabsContent>
          )}

          <TabsContent value="about">
            <AboutSection />
          </TabsContent>
        </Tabs>
      </div>
    </div>
  )
}
