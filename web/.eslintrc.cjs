module.exports = {
  root: true,
  env: { browser: true, es2021: true },
  extends: [
    'eslint:recommended',
    'plugin:@typescript-eslint/recommended',
    'plugin:react-hooks/recommended',
  ],
  ignorePatterns: ['dist', 'dev-dist', 'node_modules', '.eslintrc.cjs'],
  parser: '@typescript-eslint/parser',
  parserOptions: { ecmaVersion: 'latest', sourceType: 'module' },
  plugins: ['react-refresh'],
  rules: {
    // Purely a hot-reload ergonomics rule. This codebase deliberately colocates
    // providers with their hooks (AuthProvider/useAuth) and shadcn components
    // with their variant definitions, so it only produces noise here.
    'react-refresh/only-export-components': 'off',
    // The API layer and websocket envelopes are intentionally loosely typed.
    '@typescript-eslint/no-explicit-any': 'off',
    '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_', varsIgnorePattern: '^_' }],
  },
  overrides: [
    {
      files: ['src/sw/**/*.ts'],
      env: { browser: true, serviceworker: true },
    },
  ],
};
