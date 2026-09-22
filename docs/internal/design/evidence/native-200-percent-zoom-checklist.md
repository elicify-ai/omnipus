# Native 200% browser zoom acceptance

Status: **pending**. No native 200% zoom pass is claimed by this document.

CSS `zoom`, device pixel ratio, viewport contraction and Chromium page scale are not substitutes for native browser zoom. The automated CSS-zoom checks remain layout proxies where applicable, and the separate 320px reflow checks remain automated. Popover's CSS-zoom proxy is inapplicable because its portalled Radix content is measured in a layout coordinate space that diverges from the proxy's visual coordinate space.

## Release evidence record

- [ ] Record the candidate source fingerprint and immutable Storybook artifact URL.
- [ ] Record tester, date, operating system, display scale and browser versions.
- [ ] Test native browser zoom at exactly 200% in Chromium, Firefox and WebKit/Safari. Record the browser's native zoom indicator; do not use CSS injection, device scale or pinch zoom.
- [ ] Review every public component in the design-system manifest inventory at 200%, including every interactive, open, selected, invalid, disabled, pending and long-content state declared by its stories.
- [ ] Confirm there is no loss of content or function, clipped essential text, unreachable action, unintended document-level horizontal scrolling, obscured focus indicator or inaccessible portalled content.
- [ ] Confirm keyboard navigation, focus containment and focus restoration still work for interactive components at 200%.
- [ ] Record any failure with component, story, browser, reproduction steps and ownership. A failure leaves this checklist pending.

## Popover-specific acceptance

- [ ] Open Popover from its trigger at 200% in each required browser.
- [ ] Verify the content remains visible, reachable and aligned with the trigger without escaping the viewport.
- [ ] Repeat near the left and right viewport edges and in the narrowest supported window.
- [ ] Verify keyboard focus enters the content, Escape closes it and focus returns to the trigger.
- [ ] Confirm the applicable 320px automated reflow check passed for the same candidate artifact.

## DateTimePicker-specific acceptance

- [ ] Open the portalled calendar and time controls at 200% in each required browser.
- [ ] Verify the calendar and footer remain visible, reachable and free of document-level horizontal scrolling.
- [ ] Verify day, time and Done controls remain keyboard operable and their focus indicators remain visible.
- [ ] Confirm the closed trigger's CSS-zoom proxy and the open component's 320px automated reflow check passed for the same candidate artifact. The CSS proxy does not cover open portal geometry.

## Completion

- [ ] Link the raw manual evidence for every required browser here.
- [ ] Record the reviewer and approval date.
- [ ] Change `Status` from `pending` only after every item above is complete for the same immutable candidate.
