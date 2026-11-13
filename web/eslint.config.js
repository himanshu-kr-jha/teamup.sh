import js from '@eslint/js';
import globals from 'globals';
import tseslint from 'typescript-eslint';
import reactHooks from 'eslint-plugin-react-hooks';
import reactRefresh from 'eslint-plugin-react-refresh';

export default tseslint.config(
  { ignores: ['dist', 'dev-dist', 'node_modules', 'playwright-report', 'test-results'] },
  {
    files: ['**/*.{ts,tsx}'],
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    languageOptions: {
      ecmaVersion: 'latest',
      sourceType: 'module',
      globals: { ...globals.browser },
    },
    plugins: { 'react-hooks': reactHooks, 'react-refresh': reactRefresh },
    rules: {
      // The two rules of hooks the project has always held to. The React
      // Compiler rules newer versions of the plugin recommend are for code
      // written for the compiler, which this is not.
      'react-hooks/rules-of-hooks': 'error',
      'react-hooks/exhaustive-deps': 'warn',
      // Purely a hot-reload ergonomics rule. This codebase deliberately colocates
      // providers with their hooks (AuthProvider/useAuth) and shadcn components
      // with their variant definitions, so it only produces noise here.
      'react-refresh/only-export-components': 'off',
      // The API layer and websocket envelopes are intentionally loosely typed.
      '@typescript-eslint/no-explicit-any': 'off',
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_', varsIgnorePattern: '^_' }],
    },
  },
  {
    // Playwright's fixtures call their callback use(), which is not React's.
    files: ['e2e/**/*.ts'],
    rules: { 'react-hooks/rules-of-hooks': 'off' },
  },
  {
    files: ['src/sw/**/*.ts'],
    languageOptions: { globals: { ...globals.browser, ...globals.serviceworker } },
  }
);
