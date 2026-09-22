# Design-system verification owner guide

The manifest declares what a public component must prove. A story supplies the isolated fixture and selectors. Test reports prove that the named checks actually ran; declarations alone never count.

## Copyable story metadata

```tsx
import type { Meta, StoryObj } from '@storybook/react-vite'
import { Button } from './button'

const meta = {
  title: 'Design System/Button',
  component: Button,
  parameters: {
    designSystem: {
      keyboard: [{ trigger: '[data-testid="button"]', key: 'Enter', expectFocus: '[data-testid="button"]' }],
      pointerTargets: ['[data-testid="button"]'],
      motionTargets: ['[data-testid="button"]'],
      forcedColors: {
        boundaries: ['[data-testid="button"]'],
        differences: [{
          cue: 'foreground', selector: '[data-testid="button"]', property: 'color',
          againstSelector: '[data-testid="button"]', againstProperty: 'backgroundColor',
        }],
        focus: ['[data-testid="button"]'],
      },
      reflowExemptions: [], // each real exemption is { selector, reason }
      browserAssertions: [{ selector: '[data-testid="button"]', text: 'Continue' }],
    },
  },
} satisfies Meta<typeof Button>

export default meta
type Story = StoryObj<typeof meta>
export const Primary: Story = { args: { children: 'Continue', 'data-testid': 'button' } }
```

Only declare selectors needed by applicable checks. Static output may mark pointer or keyboard checks non-applicable in its manifest with a concrete reason. Do not add empty arrays to make a browser check pass.

## Copyable manifest

```json
{
  "version": 1,
  "component": "Button",
  "exports": ["Button"],
  "category": "primitive",
  "source": "src/components/ui/button.tsx",
  "documentation": "docs/internal/design/components/button.md",
  "owner": "design-system",
  "themes": ["dark"],
  "variants": ["default"],
  "sizes": ["default"],
  "states": ["enabled", "disabled", "focus-visible"],
  "stories": [{ "file": "src/components/ui/button.stories.tsx", "exports": ["Primary"] }],
  "checks": [
    { "id": "button-unit", "kind": "unit", "file": "src/components/ui/button.test.tsx", "test": "Button renders", "applicable": true },
    { "id": "button-interaction", "kind": "interaction", "file": "src/components/ui/button.stories.tsx", "test": "Idle Invokes Action", "story": "IdleInvokesAction", "applicable": true },
    { "id": "button-axe", "kind": "axe", "file": "tests/design-system/browser.spec.ts", "test": "Button [check:button-axe] axe", "story": "Primary", "applicable": true },
    { "id": "button-keyboard", "kind": "keyboard", "file": "tests/design-system/browser.spec.ts", "test": "Button [check:button-keyboard] keyboard", "story": "Primary", "applicable": true },
    { "id": "button-browser", "kind": "browser", "file": "tests/design-system/browser.spec.ts", "test": "Button [check:button-browser] browser", "story": "Primary", "applicable": true },
    { "id": "button-pointer", "kind": "pointer", "file": "tests/design-system/browser.spec.ts", "test": "Button [check:button-pointer] pointer", "story": "Primary", "applicable": true },
    { "id": "button-reduced-motion", "kind": "reduced-motion", "file": "tests/design-system/browser.spec.ts", "test": "Button [check:button-reduced-motion] reduced-motion", "story": "Primary", "applicable": true },
    { "id": "button-forced-colors", "kind": "forced-colors", "file": "tests/design-system/browser.spec.ts", "test": "Button [check:button-forced-colors] forced-colors", "story": "Primary", "applicable": true },
    { "id": "button-root-size", "kind": "root-size", "file": "tests/design-system/browser.spec.ts", "test": "Button [check:button-root-size] root-size", "story": "Primary", "applicable": true },
    { "id": "button-zoom", "kind": "zoom", "file": "tests/design-system/browser.spec.ts", "test": "Button [check:button-zoom] zoom", "story": "Primary", "applicable": true },
    { "id": "button-reflow", "kind": "reflow", "file": "tests/design-system/browser.spec.ts", "test": "Button [check:button-reflow] reflow", "story": "Primary", "applicable": true }
  ]
}
```

Every manifest must declare all 11 check kinds shown above. Use `applicable: false` with a concrete `reason` when a check does not apply; omission is invalid.

Browser coverage is recorded separately from the component manifest because it is execution evidence. The Playwright report supplies engine/project, check ID, status and duration. The verification command requires every applicable manifest check to have a passing result from its declared file and test. Root-size checks prove the 12–20px clamp with 10, 12, 14, 20 and 22px inputs and reject any visible text below 12px. CSS `zoom: 2` and 320px reflow are automated layout proxies; they are not native browser 200% zoom proof. Native 200% zoom remains explicit manual evidence required at release. Pointer checks run in fine and Chromium coarse-pointer projects. Forced-colors support is asserted at runtime; an unsupported engine fails visibly and is reported as a coverage gap. A forced-colors claim must declare exact `forcedColors` cues: visible boundaries, computed foreground/indicator/progress color differences, focus outlines, and meaningful state attributes as applicable. Element visibility alone is not evidence.

Popover is the single reviewed exception to the CSS-zoom proxy. Its portalled Radix content is measured in a layout coordinate space that diverges from CSS `zoom`'s visual coordinate space across engines, so its manifest marks the automated zoom check inapplicable with that exact reason. Its 320px reflow check remains applicable. This exception does not count as a zoom pass and does not apply to any other component; Popover and every public component remain pending native 200% manual acceptance in `docs/internal/design/evidence/native-200-percent-zoom-checklist.md`.

The reviewed modal DropdownMenu accessibility boundary uses two scopes. While the menu is open, the complete axe ruleset must pass for the menu subtree, and a separate whole-document scan limited to `aria-hidden-focus` must match the single registered Storybook-root finding. The browser test then proves keyboard and programmatic focus containment, closes the menu with Escape, verifies trigger focus restoration, and requires a complete whole-document axe scan with zero violations. Any changed rule, node, story, registry entry, focus behavior, or additional violation fails the check; screen-reader evidence remains a manual release requirement.

No screenshots or snapshot baselines belong in this suite.

## Evidence commands and exact names

Vitest's unit `fullName` joins suite and test names with spaces; copy that exact string into `check.test` (never use `>` separators). Storybook interaction evidence uses the story display name as `check.test` and the source export as `check.story`. For example, export `DialogContract` produces display name `Dialog Contract`.

Generate separate reports so the verifier can prove every engine actually ran:

```text
node scripts/design-system/verification-run-tests.mjs unit
node scripts/design-system/verification-run-tests.mjs storybook
npm run test:design-system:browser
```

Verification consumes `design-system-components.json`, the three `design-system-storybook-{engine}.json` files, and `design-system-browser.json`. It requires exact file and test matches, clean attempts, and every required browser project. An applicable interaction check also requires an explicit `play` function on the named story; a render-only pass is not interaction evidence.
