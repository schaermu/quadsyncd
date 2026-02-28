---
applyTo: "webui/**"
---

# Web UI review instructions (quadsyncd)

The web UI is a **Svelte 5 SPA** (TypeScript, Tailwind CSS v4, DaisyUI v5) served as a
Go-embedded static asset. It communicates with the Go backend exclusively through the
typed REST API (`src/lib/api.ts`) and a Server-Sent Events stream (`src/lib/sse.ts`).

---

## TypeScript

- All API response shapes must have corresponding TypeScript interfaces defined in
  `src/lib/api.ts`. Never use `any` or untyped `unknown` for API responses; use the
  narrowest type that is correct.
- Prefer explicit return types on exported functions. Infer locally where it is obvious.
- Do not cast with `as T` unless you have validated the shape (e.g. after a JSON parse
  inside a `try`/`catch`). Prefer type guards or Zod-style validation for untrusted data.
- Avoid non-null assertions (`!`). Prefer optional chaining (`?.`) and nullish coalescing
  (`??`) instead.

## Svelte 5 patterns

- Use **Svelte 5 runes** (`$state`, `$derived`, `$effect`, `$props`) for all reactive
  state. Do not use legacy Svelte 4 reactive declarations (`$:`) or writable/readable
  stores for component-local state.
- Declare props with `$props()`: `let { foo, bar }: { foo: string; bar?: number } = $props();`
- Keep `<script lang="ts">` blocks small. Extract pure business logic into `src/lib/`
  modules; reserve `$effect` and `$state` for UI-coordination concerns only.
- Use `onMount`/`onDestroy` for side effects tied to component lifecycle (API calls,
  SSE subscriptions). Always return or call the cleanup callback from `onDestroy`.
- Avoid inline event handlers for anything non-trivial; extract named handler functions
  inside the `<script>` block for clarity and testability.

## API client (`src/lib/api.ts`)

- All HTTP calls must go through the `apiFetch` helper. Do not call `fetch()` directly
  in components or pages.
- State-mutating calls (POST/PUT/DELETE) must include the `X-CSRF-Token` header via
  `getCsrfToken()`. Read-only GETs do not need it.
- Always encode dynamic URL path segments with `encodeURIComponent`. Always build query
  strings with `URLSearchParams` — never string-concatenate parameters.
- New API endpoints must be represented by a dedicated exported function (e.g.
  `fetchFoo(): Promise<FooResponse>`). Do not inline ad-hoc `apiFetch` calls at the
  call site.
- New response types must be defined as exported interfaces in `api.ts` before the
  functions that return them.

## SSE client (`src/lib/sse.ts`)

- The module-level singleton SSE client is an established project convention. New code
  should use `onSSEEvent` / `connectSSE` / `disconnectSSE` from `src/lib/sse.ts` rather
  than creating independent `EventSource` instances.
- **Avoid full-page re-fetches in SSE handlers.** Prefer targeted, incremental state
  updates: update only the affected item in the existing `$state` arrays/objects rather
  than calling a global `load()` that re-fetches everything. A full reload is acceptable
  only when the event genuinely invalidates the entire page state and the data set is
  small (e.g., < 20 items).
- Always unsubscribe in `onDestroy` using the cleanup function returned by `onSSEEvent`.

## Components and pages

- Shared presentational elements (badges, loading states, error states, empty states)
  live in `src/components/`. Do not duplicate their logic inline in pages.
- Pages live in `src/pages/` and are registered in the route table in `App.svelte`.
  Keep page components thin: delegate data-fetching to `src/lib/api.ts` and formatting
  to `src/lib/format.ts`.
- Formatting logic (timestamps, relative time, SHA truncation, status colors) belongs in
  `src/lib/format.ts`, not inline in templates.
- Theme management belongs in `src/lib/theme.ts`.

## Styling (Tailwind + DaisyUI)

- Use **DaisyUI component classes** (`btn`, `card`, `badge`, `table`, etc.) as the first
  choice for UI primitives. Fall back to raw Tailwind utilities only when DaisyUI offers
  no suitable component.
- Do not hardcode color values (hex, rgb). Use DaisyUI semantic tokens
  (`bg-base-100`, `text-base-content`, `text-error`, etc.) so that dark/light theme
  switching works automatically.
- Do not add `<style>` blocks for layout or colour concerns that Tailwind utilities can
  express. Reserve `<style>` for pseudo-element tricks or animations that utility classes
  cannot produce.
- Responsive classes should use Tailwind breakpoint prefixes (`sm:`, `lg:`). Do not use
  media queries in `<style>` blocks for responsive layout.

## Accessibility (basic)

- Interactive elements must be keyboard-reachable. Avoid `on:click` (or Svelte 5
  `onclick`) on non-interactive elements (`<div>`, `<span>`); use `<button>` or `<a>`
  instead.
- `<button>` elements must have a discernible text label or an `aria-label` attribute.
- Images and icons that convey meaning must have descriptive `alt` text. Decorative
  images must have `alt=""`.
- Tables must have `<th>` header cells. Use `scope="col"` / `scope="row"` when the
  table has both row and column headers.
- Do not remove focus outlines (`outline-none`) without providing an equivalent
  `:focus-visible` style.

## Testing (Vitest)

- Logic in `src/lib/` (API helpers, formatting, SSE subscriptions) must have
  corresponding unit tests using **Vitest**. Tests live alongside source files as
  `*.test.ts` or in a `__tests__/` directory under `webui/src/`.
- Tests must not make real network calls. Mock `fetch` using `vi.stubGlobal` or a
  `vi.fn()` replacement and assert on the arguments passed to it.
- `format.ts` utility functions (timestamps, relative time, short SHA, color mappings)
  are pure; they must have table-driven unit tests covering edge cases (undefined input,
  boundary values).
- When adding a new `apiFetch`-based function to `api.ts`, add a test that verifies the
  correct URL, method, headers, and that the resolved value matches the mocked response.
- Svelte component tests are encouraged but not required; prioritise testing the logic in
  `src/lib/` first.

## Build hygiene

- The `webui/` build must stay clean: `npm run check` (`svelte-check` + `tsc`) must
  produce zero errors and zero warnings after any change.
- Do not introduce new `devDependencies` without justification. Production runtime
  dependencies are prohibited (`dependencies` in `package.json` should remain empty);
  everything must be bundled at build time via `devDependencies`.
- Do not commit `node_modules/`, `dist/`, or any build artefact that is produced by
  `make build-webui`. The only exception is `internal/webui/dist/` which is the
  committed embedded asset updated as part of the release build.
