// brand-disclaimer.tsx — Trademark disclaimer for ADR-031 (G-1=B legal control).
//
// Mount this wherever <BrandIcon> marks appear (Providers, Channels, onboarding).
// The text is a constant so all surfaces stay byte-identical.

export const BRAND_DISCLAIMER_TEXT =
  'Logos are trademarks of their respective owners, used for identification only — no affiliation or endorsement implied.'

interface BrandDisclaimerProps {
  className?: string
}

export function BrandDisclaimer({ className }: BrandDisclaimerProps) {
  return (
    <p
      className={className}
      style={{
        fontSize: '0.75rem',
        lineHeight: '1.4',
        color: 'var(--color-secondary, #E2E8F0)',
        opacity: 0.55,
      }}
    >
      {BRAND_DISCLAIMER_TEXT}
    </p>
  )
}
