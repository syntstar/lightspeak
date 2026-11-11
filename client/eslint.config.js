import js from "@eslint/js";
import ts from "typescript-eslint";
import svelte from "eslint-plugin-svelte";
import globals from "globals";

export default ts.config(
  { ignores: ["dist/", "dist-web/", "web-public/", "src-tauri/", "node_modules/"] },
  js.configs.recommended,
  ...ts.configs.recommended,
  ...svelte.configs.recommended,
  {
    languageOptions: { globals: { ...globals.browser } },
    rules: {
      "@typescript-eslint/no-unused-vars": ["error", { argsIgnorePattern: "^_", varsIgnorePattern: "^_" }],
      // User content must never be rendered as HTML.
      "svelte/no-at-html-tags": "error",
      // Local, intentionally non-reactive Maps/Sets are common (caches,
      // lookups); reactive state always uses $state.
      "svelte/prefer-svelte-reactivity": "off",
    },
  },
  {
    files: ["**/*.svelte", "**/*.svelte.ts"],
    languageOptions: { parserOptions: { parser: ts.parser } },
  },
);
