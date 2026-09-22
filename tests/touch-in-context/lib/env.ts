/**
 * Environment classification for the D17 in-context touch check.
 *
 * The suite runs every route surface across five Playwright projects (see
 * playwright.touch-in-context.config.ts): a touch phone and a touch tablet,
 * each in Chromium and WebKit, plus one desktop-pointer baseline in
 * Chromium. Assertions (key-row budgets in particular) need to know which of
 * the three ENVIRONMENT KINDS a given project belongs to, independent of
 * which browser engine is driving it.
 *
 * We derive the kind from the project name rather than inspecting
 * `use.hasTouch`/`isMobile` at runtime, because the kind is a harness-level
 * concept (which budget column applies) and the project name is the one
 * place that is stable, human-readable in reports, and set in exactly one
 * file (the config). Keeping the two in lockstep is enforced by
 * `projectNameToEnvKind` throwing on an unrecognized name rather than
 * silently defaulting — an unmapped project is a config bug, not something
 * this file should paper over.
 */
export type EnvKind = 'desktopPointer' | 'touchPhone' | 'touchTablet';

export const ENV_KINDS: readonly EnvKind[] = ['desktopPointer', 'touchPhone', 'touchTablet'];

/**
 * Maps a Playwright project name (as declared in
 * playwright.touch-in-context.config.ts) to its environment kind.
 */
export function projectNameToEnvKind(projectName: string): EnvKind {
  if (projectName.startsWith('touch-phone-')) return 'touchPhone';
  if (projectName.startsWith('touch-tablet-')) return 'touchTablet';
  if (projectName.startsWith('desktop-pointer-')) return 'desktopPointer';
  throw new Error(
    `touch-in-context: project name "${projectName}" does not map to a known EnvKind. ` +
      'Project names must start with "touch-phone-", "touch-tablet-" or "desktop-pointer-" ' +
      '(see playwright.touch-in-context.config.ts).',
  );
}

/** Human-readable label used in the JSON report and screenshot filenames. */
export function envKindLabel(kind: EnvKind): string {
  switch (kind) {
    case 'desktopPointer':
      return 'desktop pointer';
    case 'touchPhone':
      return 'touch phone';
    case 'touchTablet':
      return 'touch tablet';
  }
}
