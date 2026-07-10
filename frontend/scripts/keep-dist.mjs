// Vite's emptyOutDir wipes the whole build dir (../internal/web/dist) on every
// build, including the committed .gitkeep that keeps `//go:embed all:dist`
// compiling on a fresh clone. Recreate it after each build so git state stays
// clean and the Go embed always has a matching file. See internal/web/embed.go.
import { mkdirSync, writeFileSync } from "node:fs";

const distDir = new URL("../../internal/web/dist/", import.meta.url);
mkdirSync(distDir, { recursive: true });
writeFileSync(new URL(".gitkeep", distDir), "");
