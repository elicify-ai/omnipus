import { ProfileSection } from '@/components/settings/ProfileSection'
import { ChatSection } from '@/components/settings/ChatSection'
import { ScreenHeader } from '@/components/layout/ScreenHeader'

// ProfileScreen hosts the user's PERSONAL settings — identity, locale,
// appearance, password, and workspace context — separate from app-level
// configuration which lives under Settings (Spec-6 FR-12.2: Profile vs Settings
// split). Reached from the Profile entry in the sidebar.
export function ProfileScreen() {
  return (
    <div className="absolute inset-0 flex flex-col">
      <ScreenHeader title="Profile" />
      <div className="flex-1 overflow-y-auto pb-[env(safe-area-inset-bottom,var(--space-0))]">
        <div className="max-w-3xl mx-auto px-[var(--space-3)] py-[var(--space-4)]">
          <div className="mb-[var(--space-4)]">
            <h1 className="font-headline text-2xl font-bold text-[var(--color-secondary)]">Profile</h1>
            <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)] mt-[var(--space-0-5)]">
              Your personal preferences, password, and shared workspace context.
            </p>
          </div>
          <ProfileSection />
          {/* Chat display moved here from the old Settings Chat tab (Settings redesign #1519, D-3 row 10). */}
          <section id="chat-display" className="mt-[var(--space-6)] scroll-mt-[var(--space-4)]">
            <ChatSection />
          </section>
        </div>
      </div>
    </div>
  )
}
