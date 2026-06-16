# Feature Specification: Wave 0 -- Brand & Design Foundation

**Created**: 2026-03-28
**Status**: Draft
**Input**: Discovery session confirming brand guidelines, UI spec (Appendix C), and Wave 0 scope for Omnipus pre-implementation.

---

## User Stories & Acceptance Criteria

### User Story 1 -- Tailwind v4 Theme Configuration (Priority: P0)

A frontend engineer wants a fully configured Tailwind CSS v4 theme with all Omnipus brand tokens (colors, typography, spacing) so that every component built from this point forward automatically uses the correct brand palette without manual color values.

**Why this priority**: Every other visual element depends on the theme existing first. This is the foundational layer.

**Independent Test**: Import the Tailwind config in an empty Vite project, render a `<div>` with brand classes (`bg-primary`, `text-secondary`, `text-accent`), and verify computed styles match the brand hex values.

**Acceptance Scenarios**:

1. **Given** a fresh Vite 6 + React 19 + Tailwind CSS v4 project, **When** the theme is loaded, **Then** CSS custom properties `--color-primary` (#0A0A0B), `--color-secondary` (#E2E8F0), `--color-accent` (#D4AF37), `--color-success` (#10B981), and `--color-error` (#EF4444) are available globally.
2. **Given** the Tailwind theme is configured, **When** a developer uses `font-headline`, `font-body`, or `font-mono` utility classes, **Then** the rendered text uses Outfit, Inter, or JetBrains Mono respectively, with correct fallback stacks.
3. **Given** the theme is loaded, **When** rendering on a system without web fonts loaded yet, **Then** fallback system fonts display without layout shift (font-display: swap configured).
4. **Given** the Tailwind theme, **When** a developer uses `bg-primary`, `text-secondary`, `border-accent`, `text-success`, `text-error`, **Then** the correct brand hex values are applied.

---

### User Story 2 -- shadcn/ui Component Theming (Priority: P0)

A frontend engineer wants shadcn/ui components (buttons, cards, dialogs, inputs, tooltips) to render in the Omnipus brand style (dark-first, Forge Gold accents, Liquid Silver text) out of the box, so that no per-component color overrides are needed.

**Why this priority**: shadcn/ui is the component foundation. Without brand-correct defaults, every subsequent screen requires manual overrides.

**Independent Test**: Render a shadcn/ui Button (default and destructive variants), Card, Input, and Dialog. Verify they match brand colors without any inline style overrides.

**Acceptance Scenarios**:

1. **Given** shadcn/ui is installed with the Omnipus theme, **When** rendering a default `<Button>`, **Then** it has a Forge Gold (#D4AF37) background with Deep Space Black (#0A0A0B) text.
2. **Given** shadcn/ui is installed, **When** rendering a destructive `<Button>`, **Then** it uses Ruby (#EF4444) background.
3. **Given** shadcn/ui is themed, **When** rendering a `<Card>`, **Then** the card background is a surface variant slightly lighter than Deep Space Black (e.g., #111113 or similar elevated surface), with Liquid Silver text and subtle border.
4. **Given** shadcn/ui is themed, **When** rendering an `<Input>`, **Then** the input has a dark background, Liquid Silver text, and Forge Gold focus ring.
5. **Given** the theme, **When** a `<Dialog>` opens, **Then** the overlay is semi-transparent Deep Space Black and the dialog panel matches the card surface style.

---

### User Story 3 -- Logo Processing & Asset Pipeline (Priority: P1)

A designer/engineer wants the octopus mascot SVG processed into multiple variants (black background removed, favicon, system agent avatar, social preview) so that the brand is consistently represented across all touch points.

**Why this priority**: The logo appears in the favicon, sidebar, loading states, social preview, and landing page. It blocks visual polish of all screens.

**Independent Test**: Open each processed asset file independently and verify: (a) the primary SVG has a transparent background, (b) favicon renders correctly at 16x16, 32x32, and 180x180 (apple-touch-icon), (c) the simplified avatar is recognizable at 32px.

**Acceptance Scenarios**:

1. **Given** the source SVG `/IMG_0432.svg`, **When** the logo is processed, **Then** a variant with transparent background exists at `/src/assets/logo/omnipus-logo.svg` with the black background rectangle removed.
2. **Given** the transparent logo SVG, **When** favicons are generated, **Then** `favicon.ico` (multi-size), `favicon-32x32.png`, `favicon-16x16.png`, `apple-touch-icon.png` (180x180), and `android-chrome-192x192.png` exist in `/public/`.
3. **Given** the transparent logo, **When** the system agent avatar is generated, **Then** a simplified octopus head variant exists at `/src/assets/logo/omnipus-avatar.svg`, recognizable at 32px rendering size.
4. **Given** the brand guidelines, **When** the GitHub social preview is generated, **Then** the image is 1280x640px, features the full mascot on Deep Space Black, includes the Omnipus wordmark in Outfit Bold, and the tagline "Elite Simplicity. Sovereign Control." in Inter.

---

### User Story 4 -- UI Shell with Five Branded Empty Screens (Priority: P0)

A frontend engineer wants a navigable application shell with all five screens (Chat, Command Center, Agents, Skills & Tools, Settings) rendered as branded empty states so that routing, layout, and screen switching work correctly before feature implementation begins.

**Why this priority**: The shell is the skeleton that all Wave 1+ features fill in. Routing, layout, and screen transitions must be proven before any feature work begins.

**Independent Test**: Launch the dev server, navigate to each of the 5 routes, verify each renders a branded empty state with the correct screen title, an appropriate Phosphor icon, and placeholder text. Verify URL changes and browser back/forward work.

**Acceptance Scenarios**:

1. **Given** the app is running, **When** the user navigates to `/` (root), **Then** the Chat screen is displayed as the default/home route.
2. **Given** the app is running, **When** the user navigates to `/command-center`, `/agents`, `/skills`, or `/settings`, **Then** each screen renders a branded empty state with: (a) the screen title in Outfit Bold, (b) the designated Phosphor icon (Gauge, Robot, Puzzle, Gear) rendered at 64px in Liquid Silver, (c) placeholder body text in Inter describing what will appear here, (d) Deep Space Black background.
3. **Given** the app is running on any screen, **When** the user uses browser back/forward, **Then** the correct screen renders without full page reload (SPA routing via TanStack Router).
4. **Given** the Chat empty state, **When** no agent is connected, **Then** the screen displays the octopus mascot illustration, the text "Welcome to Omnipus", and a subtitle "Your agents are standing by" in brand typography.

---

### User Story 5 -- Sidebar Navigation with Pin Option (Priority: P0)

A user wants a sidebar that defaults to an overlay drawer and can be pinned open, so that casual users get maximum content area while power users get persistent navigation.

**Why this priority**: Navigation is required to reach all 5 screens. The sidebar is the sole navigation mechanism.

**Independent Test**: Open the app, verify sidebar is closed by default. Open it, verify overlay behavior. Pin it, verify persistent behavior. Resize the viewport below 768px, verify pin is hidden and overlay-only mode activates.

**Acceptance Scenarios**:

1. **Given** the app loads for the first time, **When** the page renders, **Then** the sidebar is closed and the content area occupies full width.
2. **Given** the sidebar is closed, **When** the user clicks the hamburger icon or presses `Ctrl+B` / `Cmd+B`, **Then** the sidebar slides in from the left as an overlay with a drop shadow, without dimming the background content.
3. **Given** the sidebar is open in overlay mode, **When** the user clicks outside the sidebar, presses Escape, clicks the hamburger, or clicks a nav item, **Then** the sidebar closes and the selected route loads (if a nav item was clicked).
4. **Given** the sidebar is open, **When** the user clicks the PushPin icon at the bottom, **Then** the sidebar becomes pinned (persistent), the content area shrinks to accommodate it, and the pin icon changes to PushPinSlash.
5. **Given** the sidebar is pinned, **When** the user clicks PushPinSlash, **Then** the sidebar unpins and returns to overlay mode, and the content area expands to full width.
6. **Given** the sidebar is pinned, **When** the user clicks a nav item, **Then** the route changes but the sidebar remains open.
7. **Given** the viewport width is below 768px, **When** the sidebar is opened, **Then** it is always in overlay mode and the pin icon is hidden.
8. **Given** the user has pinned the sidebar, **When** they close and reopen the app, **Then** the sidebar remains pinned (preference persisted via Zustand with localStorage).

---

### User Story 6 -- Static Landing Page for omnipus.ai (Priority: P2)

A visitor to omnipus.ai wants a polished single-page site that communicates the Omnipus value proposition, shows the mascot, and links to the GitHub repo, so that they understand what Omnipus is and can get started.

**Why this priority**: Important for project credibility but does not block engineering work. Can ship slightly after the app shell.

**Independent Test**: Open the landing page in a browser. Verify: hero section with mascot, tagline, CTA, feature highlights, and footer. Verify mobile responsiveness. Verify no JavaScript required for core content (static HTML/CSS or SSG output).

**Acceptance Scenarios**:

1. **Given** a user visits omnipus.ai, **When** the page loads, **Then** they see a hero section with: the octopus mascot on Deep Space Black, the Omnipus wordmark in Outfit Bold, the tagline "Elite Simplicity. Sovereign Control.", and a primary CTA button ("Get Started" or "View on GitHub") in Forge Gold.
2. **Given** the landing page, **When** scrolling down, **Then** a features section highlights 3-5 key capabilities (e.g., kernel-level sandboxing, multi-agent orchestration, single binary deployment) with Phosphor icons and brief descriptions.
3. **Given** the landing page on a mobile device (viewport < 768px), **When** rendered, **Then** all sections stack vertically, text remains readable, and the mascot scales appropriately.
4. **Given** the landing page, **When** inspecting the footer, **Then** it contains links to GitHub, documentation (placeholder), and the Omnipus brand name with copyright.

---

### User Story 7 -- GitHub Social Preview Image (Priority: P3)

A project maintainer wants a branded GitHub social preview image so that links to the Omnipus repo on social media, Slack, Discord, and documentation render an attractive, professional preview card.

**Why this priority**: Low effort, high visibility. Blocks on logo processing (US-3) but is otherwise independent.

**Independent Test**: Upload the image to a test GitHub repo's social preview setting, share the repo URL on a platform that renders Open Graph images, and verify the image displays correctly.

**Acceptance Scenarios**:

1. **Given** the social preview image is generated, **When** inspected, **Then** it is exactly 1280x640 pixels, PNG format, under 1MB file size.
2. **Given** the social preview image, **When** rendered at thumbnail size (~300px wide, e.g., in a Slack unfurl), **Then** the Omnipus mascot and wordmark are still recognizable.
3. **Given** the image, **When** inspected, **Then** it uses the brand color palette: Deep Space Black background, Forge Gold and Liquid Silver mascot, Outfit Bold wordmark, Inter tagline text.

---

### User Story 8 -- @omnipus/ui Package Scaffolding (Priority: P1)

A frontend engineer wants the project scaffolded as a proper `@omnipus/ui` npm package in library mode (Vite library build) so that components can be consumed by the open-source embedded SPA, the Electron desktop app, and the future SaaS variant.

**Why this priority**: The package structure determines import paths, tree-shaking, and build configuration for all three product variants. Must be right from the start.

**Independent Test**: Run `npm pack` on the package, install the resulting tarball in a separate test project, and verify that importing `@omnipus/ui` components renders correctly with styles applied.

**Acceptance Scenarios**:

1. **Given** the project is scaffolded, **When** inspecting `package.json`, **Then** the package name is `@omnipus/ui`, it has `"type": "module"`, correct `main`/`module`/`types` entry points, and `peerDependencies` for `react` and `react-dom`.
2. **Given** the project is scaffolded, **When** running `npm run dev`, **Then** a Vite dev server starts with hot module replacement and renders the app shell.
3. **Given** the project is scaffolded, **When** running `npm run build`, **Then** it produces both a library build (ESM + types) and a standalone SPA build (for `go:embed`).
4. **Given** the build output, **When** the SPA build is served statically, **Then** all routes work correctly (TanStack Router with hash or history mode) and all assets load.
5. **Given** the project, **When** inspecting dependencies, **Then** React 19, Vite 6, Tailwind CSS v4, shadcn/ui (Radix), Zustand, TanStack Query, TanStack Router, `@phosphor-icons/react`, and `framer-motion` are present in the dependency tree.

---

## Behavioral Contract

Primary flows:
- When the app loads, the system renders the Chat screen at the root route with a branded empty state and the sidebar closed.
- When the user opens the sidebar (hamburger click or Ctrl+B/Cmd+B), the system slides in an overlay drawer from the left showing 5 navigation items and a pin toggle.
- When the user clicks a navigation item in overlay mode, the system navigates to that route and closes the sidebar.
- When the user pins the sidebar, the system persists the preference and reshapes the content layout to accommodate the permanent sidebar.
- When the user navigates between screens, the system renders the corresponding branded empty state without full page reloads.

Error flows:
- When a web font fails to load (CDN down, offline), the system renders with system fallback fonts (sans-serif for Outfit/Inter, monospace for JetBrains Mono) without layout breakage.
- When the browser does not support CSS custom properties (very old browser), the system displays with hardcoded fallback values embedded in the compiled CSS.
- When JavaScript fails to load or execute, the landing page still renders core content (static HTML/CSS).

Boundary conditions:
- When the viewport is exactly 768px wide, the system uses the tablet breakpoint (overlay or pinned, user choice).
- When the viewport is 767px wide, the system uses the phone breakpoint (overlay only, pin hidden).
- When the viewport is resized from desktop to phone while the sidebar is pinned, the system transitions the sidebar to overlay mode and unpins it.
- When localStorage is unavailable (private browsing in some browsers), the sidebar pin preference defaults to unpinned and does not persist.

---

## Edge Cases

- What happens when the user rapidly toggles the sidebar open/close? Expected: Framer Motion animations handle interruption gracefully, snapping to the target state without stacking animations.
- What happens when the user navigates directly to an unknown route (e.g., `/nonexistent`)? Expected: TanStack Router renders a branded 404 empty state with the octopus mascot and a link back to Chat.
- What happens when the favicon is requested before the asset pipeline runs? Expected: A fallback inline SVG favicon is defined in `index.html` so the browser tab always shows an icon.
- What happens when the user has system-level dark/light mode preferences? Expected: The app is dark-first by design; `prefers-color-scheme` is acknowledged but the dark theme is the default regardless. Light mode is not in Wave 0 scope.
- What happens when Phosphor Icons fail to tree-shake due to barrel import? Expected: The project uses direct imports (`from '@phosphor-icons/react/dist/ssr/ChatCircle'` or similar path imports) to guarantee tree-shaking.
- What happens when the landing page is accessed with JavaScript disabled? Expected: The hero, features, and footer sections render from static HTML/CSS. No critical content is JS-dependent.

---

## Explicit Non-Behaviors

- The system must not implement a light mode theme in Wave 0 because the brand is dark-first and light mode is a secondary consideration for later waves.
- The system must not implement any functional features (chat messaging, agent creation, tool execution, settings forms) because Wave 0 is exclusively brand and design foundation with empty state shells.
- The system must not use emoji anywhere in the UI chrome (buttons, labels, headers, navigation) because the brand guidelines mandate Phosphor Icons for all UI elements.
- The system must not use Lucide icons because the project explicitly chose Phosphor Icons to avoid the "AI slop" aesthetic (see Appendix C, C.3.1).
- The system must not add AssistantUI, Recharts, Shiki, mermaid.js, or remark-math dependencies in Wave 0 because those are feature-level dependencies for Wave 1+.
- The system must not implement the emoji-to-icon translator in Wave 0 because there is no chat rendering to translate.
- The system must not connect to any backend, WebSocket, or Go gateway because Wave 0 is a purely static frontend shell.
- The system must not use Jotai for state management because the confirmed requirement specifies Zustand (superseding the BRD's mention of Jotai).
- The system must not implement the session/agent hierarchy slide-over panel because that requires functional agent and session data models (Wave 1+).

---

## Integration Boundaries

### Google Fonts CDN

- **Data in**: HTTP requests for Outfit, Inter, and JetBrains Mono font files.
- **Data out**: WOFF2 font files.
- **Contract**: Standard Google Fonts CSS `@import` or `<link>` tags. Fonts loaded with `font-display: swap`.
- **On failure**: System falls back to `system-ui, -apple-system, sans-serif` for Outfit/Inter and `ui-monospace, monospace` for JetBrains Mono. No visual breakage.
- **Development**: Real service -- fonts are public and free. Self-hosting the font files is preferred for production to eliminate the external dependency; the build pipeline should download and bundle them.

### npm Registry

- **Data in**: Package install requests during `npm install`.
- **Data out**: Package tarballs for all dependencies.
- **Contract**: Standard npm registry protocol.
- **On failure**: Build fails. Developer retries. No runtime impact (packages are bundled).
- **Development**: Real service.

### GitHub (Social Preview)

- **Data in**: 1280x640 PNG image uploaded via GitHub repo settings.
- **Data out**: Open Graph `og:image` tag served by GitHub on repo page.
- **Contract**: GitHub expects PNG or JPG, recommended 1280x640.
- **On failure**: GitHub shows default gray preview. No functional impact.
- **Development**: Real service.

---

## BDD Scenarios

### Feature: Tailwind Theme Configuration

#### Scenario: Brand color tokens are available as CSS custom properties

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the Vite dev server is running with the configured Tailwind theme
- **When** a component renders with class `bg-primary`
- **Then** the computed `background-color` resolves to `rgb(10, 10, 11)` (#0A0A0B)

#### Scenario: All five brand colors map to correct utility classes

**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Happy Path

- **Given** the Tailwind theme is loaded
- **When** elements render with classes `bg-primary`, `text-secondary`, `border-accent`, `text-success`, `text-error`
- **Then** computed colors match #0A0A0B, #E2E8F0, #D4AF37, #10B981, #EF4444 respectively

#### Scenario Outline: Typography font families resolve correctly

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Happy Path

- **Given** the Tailwind theme is loaded and fonts are available
- **When** an element renders with class `<font_class>`
- **Then** the computed `font-family` starts with `<expected_font>`

**Examples**:

| font_class | expected_font |
|---|---|
| font-headline | Outfit |
| font-body | Inter |
| font-mono | JetBrains Mono |

#### Scenario: Font fallback when Google Fonts CDN is unreachable

**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Error Path

- **Given** the app is loaded in an environment where Google Fonts are blocked
- **When** any text renders
- **Then** fallback system fonts display without layout shift
- **And** no console errors cause the app to crash

---

### Feature: shadcn/ui Brand Theming

#### Scenario: Default button uses Forge Gold

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path

- **Given** shadcn/ui is installed with the Omnipus theme
- **When** a default `<Button>` renders
- **Then** its background color is Forge Gold (#D4AF37)
- **And** its text color is Deep Space Black (#0A0A0B)

#### Scenario: Destructive button uses Ruby

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Happy Path

- **Given** shadcn/ui is installed with the Omnipus theme
- **When** a destructive `<Button variant="destructive">` renders
- **Then** its background color is Ruby (#EF4444)

#### Scenario: Input shows Forge Gold focus ring

**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Happy Path

- **Given** shadcn/ui is themed
- **When** an `<Input>` receives focus
- **Then** a Forge Gold ring/border appears
- **And** text inside the input is Liquid Silver

#### Scenario: Card uses elevated dark surface

**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Happy Path

- **Given** shadcn/ui is themed
- **When** a `<Card>` renders
- **Then** its background is slightly lighter than Deep Space Black (elevated surface)
- **And** its text is Liquid Silver (#E2E8F0)

---

### Feature: Logo Processing

#### Scenario: Source SVG has black background removed

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the source SVG at `/IMG_0432.svg`
- **When** the logo processing pipeline runs
- **Then** `/src/assets/logo/omnipus-logo.svg` exists with a transparent background
- **And** the mascot graphics are preserved without distortion

#### Scenario: Favicon set is generated at required sizes

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Happy Path

- **Given** the transparent logo SVG
- **When** the favicon generation step completes
- **Then** `favicon.ico`, `favicon-32x32.png`, `favicon-16x16.png`, `apple-touch-icon.png` (180x180), and `android-chrome-192x192.png` exist in `/public/`
- **And** each file has the correct pixel dimensions

#### Scenario: System agent avatar is recognizable at small sizes

**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Edge Case

- **Given** the simplified avatar SVG at `/src/assets/logo/omnipus-avatar.svg`
- **When** rendered at 32x32 pixels
- **Then** the octopus head shape is distinguishable (visual inspection checkpoint)

---

### Feature: UI Shell with Five Screens

#### Scenario: Root route loads Chat screen

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the Vite dev server is running
- **When** the user navigates to `/`
- **Then** the Chat empty state screen renders
- **And** the page title contains "Omnipus"

#### Scenario: Each non-chat screen renders its branded empty state

**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Happy Path

- **Given** the app is running
- **When** the user navigates to `/command-center`
- **Then** the screen title "Command Center" renders in Outfit Bold
- **And** a Gauge Phosphor icon renders at 64px in Liquid Silver
- **And** placeholder text describes the screen's future purpose

#### Scenario Outline: All five routes are navigable

**Traces to**: User Story 4, Acceptance Scenarios 1-2
**Category**: Happy Path

- **Given** the app is running
- **When** the user navigates to `<route>`
- **Then** the screen displays `<title>` and `<icon>` icon

**Examples**:

| route | title | icon |
|---|---|---|
| / | Chat | ChatCircle |
| /command-center | Command Center | Gauge |
| /agents | Agents | Robot |
| /skills | Skills & Tools | Puzzle |
| /settings | Settings | Gear |

#### Scenario: Browser back/forward works without full reload

**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the user has navigated from `/` to `/agents` to `/settings`
- **When** the user presses browser back twice
- **Then** they return to `/` without a full page reload
- **And** the Chat empty state is displayed

#### Scenario: Chat empty state shows mascot and welcome message

**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Happy Path

- **Given** the Chat screen is displayed
- **When** no agent is connected (always true in Wave 0)
- **Then** the octopus mascot illustration is visible
- **And** "Welcome to Omnipus" is displayed in Outfit Bold
- **And** "Your agents are standing by" is displayed in Inter

#### Scenario: Unknown route shows branded 404

**Traces to**: User Story 4 (implied)
**Category**: Edge Case

- **Given** the app is running
- **When** the user navigates to `/nonexistent`
- **Then** a branded 404 page renders with the octopus mascot
- **And** a link to return to Chat (`/`) is displayed

---

### Feature: Sidebar Navigation with Pin

#### Scenario: Sidebar is closed on first load

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Happy Path

- **Given** no prior user preferences exist
- **When** the app loads
- **Then** the sidebar is not visible
- **And** the content area occupies the full viewport width

#### Scenario: Sidebar opens as overlay on hamburger click

**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Happy Path

- **Given** the sidebar is closed
- **When** the user clicks the hamburger icon
- **Then** the sidebar slides in from the left with a drop shadow
- **And** the background content is not dimmed
- **And** the sidebar contains nav items: Chat, Command Center, Agents, Skills & Tools, Settings, and a PushPin icon

#### Scenario: Sidebar opens via keyboard shortcut

**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Alternate Path

- **Given** the sidebar is closed
- **When** the user presses `Ctrl+B` (or `Cmd+B` on macOS)
- **Then** the sidebar slides open as an overlay

#### Scenario: Sidebar closes on outside click

**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the sidebar is open in overlay mode
- **When** the user clicks outside the sidebar
- **Then** the sidebar closes

#### Scenario: Sidebar closes on Escape key

**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** the sidebar is open in overlay mode
- **When** the user presses Escape
- **Then** the sidebar closes

#### Scenario: Nav item click navigates and closes sidebar

**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the sidebar is open in overlay mode
- **When** the user clicks "Agents"
- **Then** the route changes to `/agents`
- **And** the sidebar closes

#### Scenario: Pinning the sidebar

**Traces to**: User Story 5, Acceptance Scenarios 4-5
**Category**: Happy Path

- **Given** the sidebar is open in overlay mode
- **When** the user clicks the PushPin icon
- **Then** the sidebar becomes pinned (persistent)
- **And** the content area shrinks to accommodate the sidebar width
- **And** the pin icon changes to PushPinSlash

#### Scenario: Unpinning the sidebar

**Traces to**: User Story 5, Acceptance Scenario 5
**Category**: Happy Path

- **Given** the sidebar is pinned
- **When** the user clicks the PushPinSlash icon
- **Then** the sidebar unpins and closes
- **And** the content area expands to full width

#### Scenario: Pinned sidebar persists nav clicks

**Traces to**: User Story 5, Acceptance Scenario 6
**Category**: Happy Path

- **Given** the sidebar is pinned
- **When** the user clicks "Settings"
- **Then** the route changes to `/settings`
- **And** the sidebar remains open and pinned

#### Scenario: Pin state persists across sessions

**Traces to**: User Story 5, Acceptance Scenario 8
**Category**: Happy Path

- **Given** the user has pinned the sidebar
- **When** the user reloads the page
- **Then** the sidebar is pinned on load

#### Scenario: Pin icon hidden on phone breakpoint

**Traces to**: User Story 5, Acceptance Scenario 7
**Category**: Edge Case

- **Given** the viewport width is below 768px
- **When** the sidebar is opened
- **Then** the pin icon is not visible
- **And** the sidebar is in overlay mode only

#### Scenario: Viewport resize unpins sidebar

**Traces to**: User Story 5, Acceptance Scenario 7 (implied)
**Category**: Edge Case

- **Given** the sidebar is pinned on a desktop viewport
- **When** the viewport is resized below 768px
- **Then** the sidebar transitions to overlay mode
- **And** the pin icon is hidden

---

### Feature: Landing Page

#### Scenario: Hero section renders correctly

**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a user visits the landing page
- **When** the page loads
- **Then** the hero section displays the octopus mascot on Deep Space Black background
- **And** the Omnipus wordmark is in Outfit Bold
- **And** the tagline "Elite Simplicity. Sovereign Control." is visible
- **And** a Forge Gold CTA button is present

#### Scenario: Features section describes key capabilities

**Traces to**: User Story 6, Acceptance Scenario 2
**Category**: Happy Path

- **Given** the landing page is loaded
- **When** the user scrolls to the features section
- **Then** 3-5 feature cards are visible with Phosphor icons and descriptions

#### Scenario: Landing page is mobile responsive

**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Edge Case

- **Given** the landing page is loaded on a 375px-wide viewport
- **When** rendered
- **Then** all sections stack vertically
- **And** text remains readable (minimum 16px body text)
- **And** the mascot scales without overflow

#### Scenario: Footer contains required links

**Traces to**: User Story 6, Acceptance Scenario 4
**Category**: Happy Path

- **Given** the landing page is loaded
- **When** inspecting the footer
- **Then** it contains a GitHub link, a documentation placeholder link, and copyright text

---

### Feature: Social Preview Image

#### Scenario: Image meets GitHub specifications

**Traces to**: User Story 7, Acceptance Scenarios 1-3
**Category**: Happy Path

- **Given** the social preview image is generated
- **When** inspected
- **Then** dimensions are 1280x640 pixels
- **And** format is PNG
- **And** file size is under 1MB

#### Scenario: Image is legible at thumbnail size

**Traces to**: User Story 7, Acceptance Scenario 2
**Category**: Edge Case

- **Given** the social preview image
- **When** scaled to 300px wide (typical social media thumbnail)
- **Then** the Omnipus mascot and wordmark remain recognizable

---

### Feature: Package Scaffolding

#### Scenario: Package.json is correctly configured

**Traces to**: User Story 8, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the project is scaffolded
- **When** inspecting `package.json`
- **Then** the name is `@omnipus/ui`
- **And** `"type": "module"` is set
- **And** `peerDependencies` includes `react` and `react-dom`

#### Scenario: Dev server starts successfully

**Traces to**: User Story 8, Acceptance Scenario 2
**Category**: Happy Path

- **Given** `npm install` has completed
- **When** running `npm run dev`
- **Then** the Vite dev server starts and serves the app shell on localhost

#### Scenario: Build produces SPA and library outputs

**Traces to**: User Story 8, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the project is scaffolded
- **When** running `npm run build`
- **Then** the output includes a standalone SPA build (index.html + assets)
- **And** the output includes a library build with ESM entry and TypeScript declarations

#### Scenario: Required dependencies are present

**Traces to**: User Story 8, Acceptance Scenario 5
**Category**: Happy Path

- **Given** the project is scaffolded
- **When** inspecting the dependency tree
- **Then** React 19, Vite 6, Tailwind CSS v4, Zustand, TanStack Query, TanStack Router, `@phosphor-icons/react`, and `framer-motion` are installed

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit | Tailwind config, theme tokens, utility functions, Zustand store | Validates design tokens resolve correctly and state logic works |
| Integration | Component rendering with theme, sidebar + routing, screen layout | Validates components use the theme correctly and interact properly |
| E2E | Full app navigation, sidebar pin flow, screen transitions, responsive behavior | Validates complete user workflows from browser perspective |

### Test Implementation Order

Write these tests BEFORE implementing the feature code. Order: unit first, then integration, then E2E. Within each level, order by dependency.

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|---|---|---|---|---|
| 1 | `test_brand_colors_defined` | Unit | Scenario: Brand color tokens are available | Verify the Tailwind config exports all 5 brand color tokens with correct hex values |
| 2 | `test_font_families_defined` | Unit | Scenario: Typography font families resolve | Verify the Tailwind config exports headline, body, and mono font families |
| 3 | `test_sidebar_store_default_state` | Unit | Scenario: Sidebar is closed on first load | Zustand store initializes with `isOpen: false`, `isPinned: false` |
| 4 | `test_sidebar_store_toggle` | Unit | Scenario: Sidebar opens as overlay | Zustand store `toggle()` flips `isOpen` |
| 5 | `test_sidebar_store_pin` | Unit | Scenario: Pinning the sidebar | Zustand store `pin()` sets `isPinned: true` and `isOpen: true` |
| 6 | `test_sidebar_store_unpin` | Unit | Scenario: Unpinning the sidebar | Zustand store `unpin()` sets `isPinned: false` and `isOpen: false` |
| 7 | `test_sidebar_store_persistence` | Unit | Scenario: Pin state persists across sessions | Zustand persist middleware reads from localStorage on init |
| 8 | `test_button_default_variant_colors` | Integration | Scenario: Default button uses Forge Gold | Render `<Button>`, assert background-color matches Forge Gold |
| 9 | `test_button_destructive_variant` | Integration | Scenario: Destructive button uses Ruby | Render `<Button variant="destructive">`, assert Ruby background |
| 10 | `test_card_surface_color` | Integration | Scenario: Card uses elevated dark surface | Render `<Card>`, assert dark elevated background and Liquid Silver text |
| 11 | `test_input_focus_ring` | Integration | Scenario: Input shows Forge Gold focus ring | Render `<Input>`, focus it, assert Forge Gold ring |
| 12 | `test_chat_empty_state` | Integration | Scenario: Chat empty state shows mascot | Render Chat screen, assert mascot image, welcome text, subtitle |
| 13 | `test_screen_empty_states` | Integration | Scenario: Each non-chat screen renders empty state | Render each screen route, assert title, icon, placeholder text |
| 14 | `test_404_page` | Integration | Scenario: Unknown route shows branded 404 | Navigate to invalid route, assert 404 content and home link |
| 15 | `test_sidebar_overlay_rendering` | Integration | Scenario: Sidebar opens as overlay | Render sidebar open state, assert nav items and drop shadow |
| 16 | `test_sidebar_pin_icon_hidden_mobile` | Integration | Scenario: Pin icon hidden on phone breakpoint | Render at <768px, assert pin icon not in DOM |
| 17 | `test_package_json_structure` | Unit | Scenario: Package.json is correctly configured | Read package.json, verify name, type, peerDependencies |
| 18 | `test_full_navigation_flow` | E2E | Scenario: All five routes are navigable | Navigate to each route via sidebar, verify correct screen renders |
| 19 | `test_sidebar_pin_unpin_flow` | E2E | Scenario: Pinning/unpinning sidebar | Open sidebar, pin, navigate, verify persistence, unpin, verify close |
| 20 | `test_keyboard_shortcut_sidebar` | E2E | Scenario: Sidebar opens via keyboard shortcut | Press Ctrl+B, verify sidebar opens |
| 21 | `test_browser_back_forward` | E2E | Scenario: Browser back/forward works | Navigate between screens, use back/forward, verify correct screens |
| 22 | `test_responsive_sidebar_behavior` | E2E | Scenario: Viewport resize unpins sidebar | Pin sidebar, resize to mobile, verify overlay mode |
| 23 | `test_build_outputs` | E2E | Scenario: Build produces SPA and library outputs | Run build, verify output files exist with correct structure |

### Test Datasets

#### Dataset: Brand Color Tokens

| # | Input (Token Name) | Boundary Type | Expected Output (Hex) | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `--color-primary` | Normal | `#0A0A0B` | BDD: Brand color tokens | Deep Space Black |
| 2 | `--color-secondary` | Normal | `#E2E8F0` | BDD: Brand color tokens | Liquid Silver |
| 3 | `--color-accent` | Normal | `#D4AF37` | BDD: Brand color tokens | Forge Gold |
| 4 | `--color-success` | Normal | `#10B981` | BDD: Brand color tokens | Emerald |
| 5 | `--color-error` | Normal | `#EF4444` | BDD: Brand color tokens | Ruby |

#### Dataset: Route Navigation

| # | Input (URL Path) | Boundary Type | Expected Output (Screen Title) | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `/` | Normal | Chat | BDD: All five routes | Default/home route |
| 2 | `/command-center` | Normal | Command Center | BDD: All five routes | |
| 3 | `/agents` | Normal | Agents | BDD: All five routes | |
| 4 | `/skills` | Normal | Skills & Tools | BDD: All five routes | |
| 5 | `/settings` | Normal | Settings | BDD: All five routes | |
| 6 | `/nonexistent` | Error | 404 Page | BDD: Unknown route | Any unmatched path |
| 7 | `/AGENTS` | Edge | 404 Page | BDD: Unknown route | Routes are case-sensitive |
| 8 | (empty, root) | Boundary | Chat | BDD: Root route loads Chat | No path segment |

#### Dataset: Viewport Breakpoints

| # | Input (Viewport Width) | Boundary Type | Expected Behavior | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | 1440px | Normal | Desktop: overlay or pin available | BDD: Sidebar scenarios | Typical desktop |
| 2 | 1024px | Boundary | Desktop/tablet: overlay or pin | BDD: Sidebar scenarios | Upper boundary |
| 3 | 768px | Boundary | Tablet: overlay or pin available | BDD: Pin icon hidden | Exact breakpoint |
| 4 | 767px | Boundary | Phone: overlay only, no pin | BDD: Pin icon hidden | Just below breakpoint |
| 5 | 375px | Normal | Phone: overlay only, no pin | BDD: Landing page mobile | iPhone SE |
| 6 | 320px | Edge | Phone: overlay only, no pin | BDD: Landing page mobile | Smallest common phone |

#### Dataset: Sidebar State Transitions

| # | Initial State | Action | Expected State | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | closed, unpinned | hamburger click | open, unpinned (overlay) | BDD: Sidebar opens | |
| 2 | open, unpinned | outside click | closed, unpinned | BDD: Sidebar closes on outside click | |
| 3 | open, unpinned | Escape | closed, unpinned | BDD: Sidebar closes on Escape | |
| 4 | open, unpinned | nav item click | closed, unpinned + navigated | BDD: Nav item click | |
| 5 | open, unpinned | pin click | open, pinned | BDD: Pinning sidebar | |
| 6 | open, pinned | nav item click | open, pinned + navigated | BDD: Pinned sidebar nav | |
| 7 | open, pinned | unpin click | closed, unpinned | BDD: Unpinning sidebar | |
| 8 | open, pinned | viewport < 768 | open, unpinned (overlay) | BDD: Viewport resize | |

### Regression Test Requirements

**New functionality:**

> No regression impact -- new capability (greenfield project). No existing tests or behaviors to protect.

---

## Functional Requirements

- **FR-001**: System MUST define CSS custom properties for all five brand colors: `--color-primary` (#0A0A0B), `--color-secondary` (#E2E8F0), `--color-accent` (#D4AF37), `--color-success` (#10B981), `--color-error` (#EF4444).
- **FR-002**: System MUST configure Tailwind CSS v4 with font family tokens: `font-headline` (Outfit), `font-body` (Inter), `font-mono` (JetBrains Mono), each with appropriate system fallbacks.
- **FR-003**: System MUST configure `font-display: swap` for all web fonts to prevent invisible text during loading.
- **FR-004**: System MUST theme all shadcn/ui components to use brand colors by default, including but not limited to Button, Card, Input, Dialog, and Tooltip. Note (MIN-002): Installed shadcn/ui components as of Wave 0 baseline: button, card, input, dialog, select, tabs, avatar, progress, accordion, dropdown-menu, separator, textarea, badge, switch, sheet, label, slider, table. Additional components are added as-needed by subsequent waves. The exact list reflects the 22 components present in `src/components/ui/` at the time Wave 0 was implemented.
- **FR-005**: System MUST process the source logo SVG to produce a transparent-background variant stored at `/src/assets/logo/omnipus-logo.svg`.
- **FR-006**: System MUST generate a favicon set: `favicon.ico` (multi-size), `favicon-32x32.png`, `favicon-16x16.png`, `apple-touch-icon.png` (180x180), `android-chrome-192x192.png`.
- **FR-007**: System MUST generate a simplified octopus-head avatar SVG at `/src/assets/logo/omnipus-avatar.svg`, recognizable at 32px rendering size.
- **FR-008**: System MUST implement TanStack Router with five routes: `/` (Chat), `/command-center`, `/agents`, `/skills`, `/settings`, plus a catch-all 404 route.
- **FR-009**: System MUST render a branded empty state for each of the five screens, displaying the screen title (Outfit Bold), designated Phosphor icon (64px, Liquid Silver), and placeholder description text (Inter).
- **FR-010**: System MUST render the Chat empty state with the octopus mascot, "Welcome to Omnipus" (Outfit Bold), and "Your agents are standing by" (Inter).
- **FR-011**: System MUST implement a sidebar as an overlay drawer (default closed) containing navigation items for all five screens plus a pin toggle.
- **FR-012**: System MUST support opening the sidebar via hamburger icon click and `Ctrl+B` / `Cmd+B` keyboard shortcut.
- **FR-013**: System MUST close the overlay sidebar when the user clicks outside, presses Escape, clicks the hamburger, or clicks a nav item.
- **FR-014**: System MUST support pinning the sidebar to a persistent panel that shrinks the content area, with pin state persisted in localStorage via Zustand.
- **FR-015**: System MUST hide the pin icon and enforce overlay-only mode when viewport width is below 768px.
- **FR-016**: System MUST transition a pinned sidebar to overlay mode when the viewport is resized below 768px.
- **FR-017**: System SHOULD animate sidebar open/close transitions using Framer Motion with graceful interruption handling.
- **FR-018**: System MUST generate a GitHub social preview image at 1280x640px PNG, under 1MB, featuring the mascot on Deep Space Black with the Omnipus wordmark (Outfit Bold) and tagline (Inter).
- **FR-019**: System MUST scaffold the project as `@omnipus/ui` with `package.json` containing correct name, `"type": "module"`, entry points, and peer dependencies for React 19 and React DOM.
- **FR-020**: System MUST produce both a standalone SPA build (for `go:embed`) and a library build (ESM + TypeScript declarations) from `npm run build`.
- **FR-021**: System MUST implement a static landing page for omnipus.ai with hero section (mascot, wordmark, tagline, CTA), features section (3-5 items with Phosphor icons), and footer (GitHub link, docs placeholder, copyright).
- **FR-022**: System MUST ensure the landing page renders core content without JavaScript (static HTML/CSS).
- **FR-023**: System MUST use only Phosphor Icons (`@phosphor-icons/react`) for all UI iconography. Lucide, Heroicons, and other icon libraries are prohibited.
- **FR-024**: System MUST use Zustand (not Jotai) for UI state management.
- **FR-025**: System SHOULD self-host font files in the build output to eliminate runtime dependency on Google Fonts CDN.

---

## Success Criteria

- **SC-001**: All five routes render branded empty states with correct brand colors, typography, and icons when the dev server runs.
- **SC-002**: The sidebar opens/closes/pins/unpins correctly across desktop, tablet, and phone breakpoints with no visual glitches.
- **SC-003**: `npm run build` completes without errors and produces both SPA and library build outputs.
- **SC-004**: The favicon renders correctly in Chrome, Firefox, and Safari browser tabs.
- **SC-005**: The social preview image is 1280x640px PNG, under 1MB, with legible mascot and wordmark at 300px thumbnail width.
- **SC-006**: All shadcn/ui components render in brand colors without per-component overrides.
- **SC-007**: The landing page renders at mobile, tablet, and desktop breakpoints without overflow or unreadable text.
- **SC-008**: Lighthouse performance score for the SPA shell is above 90 (no heavy feature dependencies in Wave 0).
- **SC-009**: The `@omnipus/ui` package can be installed from a local tarball (`npm pack`) into a separate project and renders components correctly.
- **SC-010**: Keyboard shortcut (Ctrl+B / Cmd+B) toggles the sidebar in both Chrome and Firefox.

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|---|---|---|---|
| FR-001 | US-1 | Brand color tokens available; All five brand colors | `test_brand_colors_defined` |
| FR-002 | US-1 | Typography font families resolve | `test_font_families_defined` |
| FR-003 | US-1 | Font fallback when CDN unreachable | `test_font_families_defined` (fallback assertion) |
| FR-004 | US-2 | Default button uses Forge Gold; Destructive button uses Ruby; Input focus ring; Card surface | `test_button_default_variant_colors`, `test_button_destructive_variant`, `test_input_focus_ring`, `test_card_surface_color` |
| FR-005 | US-3 | Source SVG has black background removed | (manual/visual verification) |
| FR-006 | US-3 | Favicon set is generated | (build verification) |
| FR-007 | US-3 | System agent avatar recognizable | (visual verification) |
| FR-008 | US-4 | All five routes navigable; Unknown route shows 404 | `test_screen_empty_states`, `test_404_page`, `test_full_navigation_flow` |
| FR-009 | US-4 | Each non-chat screen renders empty state | `test_screen_empty_states` |
| FR-010 | US-4 | Chat empty state shows mascot | `test_chat_empty_state` |
| FR-011 | US-5 | Sidebar is closed on first load; Sidebar opens as overlay | `test_sidebar_store_default_state`, `test_sidebar_overlay_rendering` |
| FR-012 | US-5 | Sidebar opens via keyboard shortcut | `test_keyboard_shortcut_sidebar` |
| FR-013 | US-5 | Sidebar closes on outside click; Escape; Nav item click | `test_sidebar_pin_unpin_flow`, `test_full_navigation_flow` |
| FR-014 | US-5 | Pinning sidebar; Pin state persists | `test_sidebar_store_pin`, `test_sidebar_store_persistence`, `test_sidebar_pin_unpin_flow` |
| FR-015 | US-5 | Pin icon hidden on phone breakpoint | `test_sidebar_pin_icon_hidden_mobile` |
| FR-016 | US-5 | Viewport resize unpins sidebar | `test_responsive_sidebar_behavior` |
| FR-017 | US-5 | Sidebar opens as overlay (animation) | `test_sidebar_overlay_rendering` |
| FR-018 | US-7 | Image meets GitHub specifications; Legible at thumbnail | (build verification + visual) |
| FR-019 | US-8 | Package.json correctly configured | `test_package_json_structure` |
| FR-020 | US-8 | Build produces SPA and library outputs | `test_build_outputs` |
| FR-021 | US-6 | Hero section renders; Features section; Footer | (E2E visual / landing page tests) |
| FR-022 | US-6 | Landing page mobile responsive | (E2E with JS disabled) |
| FR-023 | US-1, US-4, US-5, US-6 | All icon-related scenarios | (code review: no Lucide imports) |
| FR-024 | US-5 | Sidebar store scenarios | `test_sidebar_store_*` |
| FR-025 | US-1 | Font fallback when CDN unreachable | (build verification: fonts in dist) |

**Completeness check**: Every FR-xxx row has at least one BDD scenario and one test or verification method. Every BDD scenario appears in at least one row.

---

## Ambiguity Warnings — RESOLVED

All ambiguities resolved on 2026-03-28:

| # | What Was Ambiguous | Resolution |
|---|---|---|
| 1 | Landing page platform | **Separate repo.** Not co-located with the app. |
| 2 | Elevated surface color | **Derived** — 5% lighter than primary (~#111113). Engineer picks exact shade. |
| 3 | Sidebar width | **256px** pinned width. |
| 4 | Zustand vs Jotai | **Zustand confirmed.** BRD Appendix C already updated. |
| 5 | Social preview method | **Manual design** — brand asset, crafted not generated. |
| 6 | Routing mode | **Hash routing** for go:embed SPA, history routing for dev server. |
| 7 | Font hosting | **Self-host** via `@fontsource` packages. No CDN dependency. |
| 8 | AssistantUI in Wave 0 | **YES — install AssistantUI and shadcn from the beginning.** Both must be present in Wave 0 package.json and configured, even if chat functionality isn't wired until later waves. |

---

## Evaluation Scenarios (Holdout)

> **Note**: These scenarios are for post-implementation evaluation only.
> They must NOT be visible to the implementing agent during development.
> Do not reference these in the TDD plan or traceability matrix.

### Scenario: Full navigation round-trip with pinned sidebar
- **Setup**: App loaded fresh, no prior state.
- **Action**: Open sidebar, pin it, navigate Chat -> Command Center -> Agents -> Skills & Tools -> Settings -> Chat, then unpin.
- **Expected outcome**: All five screens render with correct branded empty states, sidebar stays pinned through all navigations, content area is consistently narrowed, unpinning restores full width.
- **Category**: Happy Path

### Scenario: Brand consistency across all screens
- **Setup**: App loaded with dev tools open to inspect computed styles.
- **Action**: Navigate to each of the 5 screens and inspect heading font-family, background color, icon color.
- **Expected outcome**: Every screen heading uses Outfit, every background is #0A0A0B, every icon is Liquid Silver #E2E8F0, no screen deviates from the brand palette.
- **Category**: Happy Path

### Scenario: Complete fresh install and build
- **Setup**: Clone repo, no node_modules, no prior state.
- **Action**: Run `npm install && npm run build`.
- **Expected outcome**: Zero errors, both SPA and library builds produced, total build output under 2MB (no heavy dependencies in Wave 0).
- **Category**: Happy Path

### Scenario: localStorage unavailable (private browsing)
- **Setup**: App loaded in a browser with localStorage throwing on access (e.g., Safari private browsing in some configurations).
- **Action**: Open sidebar, attempt to pin.
- **Expected outcome**: Sidebar pins for the current session but does not crash. On reload, sidebar defaults to unpinned. No console errors from Zustand persist middleware.
- **Category**: Error

### Scenario: Network offline after initial load
- **Setup**: App loaded, then network disconnected.
- **Action**: Navigate between all screens, open/close/pin sidebar.
- **Expected outcome**: All navigation and sidebar interactions work. Fonts either loaded already (cached) or fallbacks render. No spinners, no errors. Wave 0 has zero network dependencies at runtime.
- **Category**: Error

### Scenario: Rapid sidebar toggle (animation stress)
- **Setup**: App loaded, sidebar closed.
- **Action**: Click hamburger 10 times rapidly in succession.
- **Expected outcome**: Sidebar settles to a consistent open or closed state. No animation stacking, no z-index glitches, no orphaned overlay shadows.
- **Category**: Edge Case

### Scenario: Direct URL entry for each route
- **Setup**: App not yet loaded in browser.
- **Action**: Type `localhost:5173/agents` directly in browser address bar and press Enter. Repeat for each of the 5 routes and one invalid route.
- **Expected outcome**: Each route renders the correct screen directly (no redirect-to-home-then-navigate). Invalid route shows 404 page.
- **Category**: Edge Case

---

## Assumptions

- This is a greenfield project with no existing codebase, no existing node_modules, no existing configuration files. Everything is created from scratch.
- The implementing engineer has Node.js 20+ and npm 10+ available.
- The project will use npm (not yarn, pnpm, or bun) as the package manager unless explicitly changed.
- React 19, Vite 6, and Tailwind CSS v4 are all released and stable at the time of implementation (March 2026).
- shadcn/ui supports Tailwind CSS v4 and React 19 at the time of implementation.
- The `@omnipus/ui` package is not published to npm in Wave 0. It is consumed locally (monorepo or `npm link`).
- No Go backend or WebSocket connection is needed for Wave 0. The frontend runs standalone.
- The landing page for omnipus.ai may be hosted separately from the app (e.g., GitHub Pages, Vercel, or a static file server). The hosting platform decision is deferred.
- The logo SVG at `/IMG_0432.svg` contains a black rectangle background that can be removed by editing the SVG XML (removing the background `<rect>` or similar element).
- Vitest is the test runner (aligns with the Vite ecosystem). React Testing Library for component tests. Playwright for E2E tests.
- No CI/CD pipeline exists yet. Tests are run locally during Wave 0.
- The BRD mentions Jotai for UI state, but the discovery session confirmed Zustand. This spec follows the confirmed decision (Zustand).
- Light mode is explicitly out of scope for Wave 0. The app is dark-only.

## Clarifications

### 2026-03-28

- Q: Jotai or Zustand for state management? -> A: Zustand, per discovery session confirmation. BRD Appendix C reference to Jotai is superseded.
- Q: Should Wave 0 include any functional features (chat, agents, settings forms)? -> A: No. Wave 0 is brand + design foundation only. All screens are branded empty shells.
- Q: Is AssistantUI needed in Wave 0? -> A: No. AssistantUI is for chat rendering (Wave 1+).
- Q: Should the sidebar have a dimmed backdrop when open as overlay? -> A: No. Per Appendix C section C.6.1: "No dimmed backdrop. Content behind stays fully visible and clickable."
