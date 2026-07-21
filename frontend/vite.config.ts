import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";

// The devpit binary serves the built SPA and the REST/SSE API from the same
// origin (localhost:7474) via go:embed. In dev we run Vite's server instead and
// proxy just the API surface through to the running backend, so the browser
// still sees one origin. The API lives at the root (no /api prefix), so we
// proxy each concrete path rather than a prefix — everything else is the SPA.
const API_TARGET = "http://localhost:7474";
const apiPaths = ["/attention", "/connections", "/sync-log", "/events", "/items"];

export default defineConfig({
  plugins: [svelte()],
  // Output straight into the Go embed package so `go build` picks it up.
  build: {
    outDir: "../internal/web/dist",
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: Object.fromEntries(
      apiPaths.map((p) => [
        p,
        {
          target: API_TARGET,
          changeOrigin: true,
          // /events is Server-Sent Events: disable buffering so frames stream.
          ...(p === "/events" ? { ws: false } : {}),
        },
      ]),
    ),
  },
});
