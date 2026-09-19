# accordion

Source: `src/components/ui/accordion.tsx`. Owner: design-system.

This component preserves the existing Sovereign Deep presentation. Its public exports are `Accordion, AccordionItem, AccordionTrigger, AccordionContent`. The shared overlay story records default, keyboard, pointer, forced-colour, reduced-motion, root-size, zoom and reflow coverage.

## Contract

Accordion delegates single or multiple expansion, controlled or uncontrolled state, disabled items, and keyboard navigation to Radix Accordion. The trigger is rendered inside a Radix header and retains Radix’s expanded-state and content relationships. Arrow keys, Home, and End follow Radix’s orientation-aware navigation among enabled triggers.

`AccordionTrigger` defaults to `tabIndex={0}` while allowing a caller override. Its chevron is decorative and hidden from assistive technology. Content expansion animations and the chevron transition are disabled for reduced motion. Accordion renders inline content and does not provide modal focus trapping or overlay dismissal.

## Evidence setup

Where activation begins from a closed trigger, dedicated keyboard and pointer fixtures verify that initial transition. Stable-state fixtures verify accessibility, motion, forced-colour, zoom, and reflow behavior without toggling during setup.
