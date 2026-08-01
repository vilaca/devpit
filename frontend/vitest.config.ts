import { defineConfig } from "vitest/config";
import { svelte } from "@sveltejs/vite-plugin-svelte";

// Vitest reuses the Vite/Svelte toolchain the app already builds with, so
// `.svelte.ts` rune modules (e.g. dashboard.svelte.ts) compile in tests too.
// The targets are pure TS/logic — no component-DOM harness — so the default
// node environment is enough.
export default defineConfig({
  plugins: [svelte()],
  // The precedence-parity test imports internal/attention/states.go?raw to
  // compare against the Go source of truth, which lives above the frontend
  // dir — allow Vite to read the repo root.
  server: { fs: { allow: [".."] } },
  test: {
    include: ["src/**/*.test.ts"],
  },
});
