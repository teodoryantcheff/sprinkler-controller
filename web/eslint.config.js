import js from '@eslint/js'
import svelte from 'eslint-plugin-svelte'
import globals from 'globals'
import ts from 'typescript-eslint'
import svelteConfig from './svelte.config.js'

export default ts.config(
  { ignores: ['node_modules/'] },
  js.configs.recommended,
  ...ts.configs.recommended,
  ...svelte.configs.recommended,
  { languageOptions: { globals: { ...globals.browser } } },
  {
    files: ['**/*.svelte', '**/*.svelte.ts'],
    languageOptions: { parserOptions: { parser: ts.parser, extraFileExtensions: ['.svelte'], svelteConfig } },
  },
  {
    rules: {
      // Lists here are display-only (no per-item component state); keys add noise, not correctness.
      'svelte/require-each-key': 'off',
    },
  },
  { files: ['public/sw.js'], languageOptions: { globals: { ...globals.serviceworker } } },
  { files: ['scripts/**', '*.config.*'], languageOptions: { globals: { ...globals.node } } },
)
