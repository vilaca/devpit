# Theme and Color Scheme (Dark Mode)

## Scope

Implemented — theme toggle, OS-preference default, and `localStorage`
persistence live in `frontend/` (`frontend/src/lib/theme.svelte.ts`). See
`docs/Roadmap.md`.

## Context

DevPit runs as a local single-user web application. Users spend extended
periods looking at the dashboard, so a dark theme reduces eye strain in low-
light environments. The roadmap called for a user-toggled night mode that
persists across sessions, defaulting to the OS preference until the user
makes an explicit choice.

Because DevPit has no server-side rendering and no user account system,
server persistence is unnecessary and over-engineered; `localStorage` is the
right scope.

## Decision

**Token-based CSS theming via `data-theme` attribute + OS fallback media query.**

- All colors are defined as CSS custom properties (design tokens) in `app.css`.
  Components reference tokens exclusively and never hard-code colors.
- The active theme is signalled by `data-theme="dark"` or `data-theme="light"`
  on `<html>`. CSS selectors `[data-theme="dark"]` drive the dark palette.
- An `@media (prefers-color-scheme: dark)` rule with `:root:not([data-theme="light"])`
  handles the OS default so the correct theme renders even before JS runs.
- An inline `<script>` in `index.html` runs synchronously before first paint:
  it reads `localStorage("theme")`, falls back to `matchMedia`, and sets
  `data-theme`. This eliminates the light-flash-then-dark problem common to
  JS-driven themes.
- User choice is persisted to `localStorage` under key `"theme"`.
- A sun/moon toggle button lives in `TopBar`.
- Dark state is exposed as a reactive Svelte module (`lib/theme.svelte.ts`) so
  components can bind to it without prop drilling.

### Dark palette adjustments

State, health, and marker tokens have dedicated dark-mode overrides. The light
values are dark/rich (designed for white backgrounds); the dark values are
lighter and slightly desaturated equivalents that maintain sufficient contrast
on the `#14171a` surface.

## Consequences

- Zero backend changes; zero build-time complexity.
- The `localStorage` key `"theme"` is app-global — adding multi-tab support
  in the future would require listening to the `storage` event.
- If a user clears `localStorage`, the theme resets to the OS preference,
  which is the correct fallback.
