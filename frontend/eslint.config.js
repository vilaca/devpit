// Maximal linter set, same stance as .golangci.yml: correctness configs on,
// stylistic configs off (prettier owns formatting), disables only for rules
// that fire on deliberate patterns — each with a justification comment.
import svelteConfig from "./svelte.config.js";
import { defineConfig } from "eslint/config";
import globals from "globals";
import js from "@eslint/js";
import ts from "typescript-eslint";
import svelte from "eslint-plugin-svelte";

export default defineConfig(
  {
    ignores: ["dist/**"],
  },
  js.configs.recommended,
  ts.configs.recommendedTypeChecked,
  svelte.configs.recommended,
  {
    languageOptions: {
      globals: {
        ...globals.browser,
      },
      parserOptions: {
        // Root-level tooling scripts aren't part of tsconfig.json's include
        // (they're plain Node/config scripts, not app source) — let them
        // type-check against TS's default out-of-project config instead.
        projectService: {
          allowDefaultProject: ["eslint.config.js", "svelte.config.js", "scripts/*.mjs"],
        },
        tsconfigRootDir: import.meta.dirname,
      },
    },
  },
  {
    files: ["**/*.svelte", "**/*.svelte.ts"],
    languageOptions: {
      parserOptions: {
        projectService: true,
        extraFileExtensions: [".svelte"],
        parser: ts.parser,
        svelteConfig,
      },
    },
  },
  {
    // Root tooling scripts sit outside tsconfig.json's project and only get
    // TS's default out-of-project resolution — type-aware rules produce noise
    // (unresolvable Node globals, `error`-typed values) rather than real
    // findings there, so type-checked linting is off for these files only.
    files: ["eslint.config.js", "svelte.config.js", "scripts/*.mjs"],
    ...ts.configs.disableTypeChecked,
  },
);
