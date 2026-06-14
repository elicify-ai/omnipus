import { ProfileSection } from '@/components/settings/ProfileSection'

// ProfileScreen hosts the user's PERSONAL settings — identity, locale,
// appearance, password, and workspace context — separate from app-level
// configuration which lives under Settings (Spec-6 FR-12.2: Profile vs Settings
// split). Reached from the Profile entry in the sidebar.
export function ProfileScreen() {
  return (
    <div className="absolute inset-0 overflow-y-auto pb-[env(safe-area-inset-bottom)]">
      <div className="max-w-3xl mx-auto px-4 py-6">
        <div className="mb-6">
          <h1 className="font-headline text-2xl font-bold text-[var(--color-secondary)]">Profile</h1>
          <p className="text-sm text-[var(--color-muted)] mt-0.5">
            Your personal preferences, password, and shared workspace context.
          </p>
        </div>
        <ProfileSection />
      </div>
    </div>
  )
}
