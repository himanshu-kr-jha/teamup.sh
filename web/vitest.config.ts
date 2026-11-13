import { configDefaults, defineConfig, mergeConfig } from 'vitest/config';
import viteConfig from './vite.config.ts';

export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      // Playwright's specs live in e2e/ and also end in .spec.ts; Vitest must not
      // try to run them.
      exclude: [...configDefaults.exclude, 'e2e/**'],
      // A due date and a calendar day are local, so their tests run in several
      // time zones, and Node only honours a change of TZ outside a worker thread.
      // Every file runs in a process of its own: picking files by glob missed
      // them in a checkout under a dot folder (such as a git worktree in
      // .claude/), where `**` does not reach, and they then ran in one zone only.
      pool: 'forks',
    },
  })
);
