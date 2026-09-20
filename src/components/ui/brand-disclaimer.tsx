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
        fontSize: 'var(--type-utility-xs-size)',
        lineHeight: 'var(--font-line-height-compact)',
        color: 'var(--color-secondary, #E2E8F0)',
        opacity: 0.55,
      }}
    >
      {BRAND_DISCLAIMER_TEXT}
    </p>
  )
}
