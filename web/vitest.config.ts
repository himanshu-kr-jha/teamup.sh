import { configDefaults, defineConfig, mergeConfig } from 'vitest/config';
import viteConfig from './vite.config';

export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      // Playwright's specs live in e2e/ and also end in .spec.ts; Vitest must not
      // try to run them.
      exclude: [...configDefaults.exclude, 'e2e/**'],
      // A due date is a local calendar day, so its tests run in several time
      // zones, and Node only honours a change of TZ outside a worker thread.
      poolMatchGlobs: [
        ['**/dueDate.test.ts', 'forks'],
        ['**/calendar/layout.test.ts', 'forks'],
      ],
    },
  })
);
