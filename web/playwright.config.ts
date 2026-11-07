import { defineConfig, devices } from '@playwright/test';

/**
 * End-to-end tests against a REAL TeamUp server, database and browser.
 *
 *   E2E_BASE_URL       where the server listens (default http://localhost:8080)
 *   E2E_OWNER_EMAIL    the organization owner the server was set up with
 *   E2E_OWNER_PASSWORD
 *   E2E_SHOTS_DIR      where screenshots.spec.ts writes its images
 *
 * The suite seeds its own people and tasks through the API and refuses to run
 * against a database that already holds them, so point it at a freshly
 * set-up instance. e2e/run.sh does that end to end (new database, server,
 * tests, shutdown).
 */
export default defineConfig({
  testDir: './e2e',
  globalSetup: './e2e/global-setup.ts',
  // The specs share one seeded organization and some of them change it, so they
  // run one after another, in file order.
  fullyParallel: false,
  workers: 1,
  retries: 0,
  // Scrum liveness waits out a 30 second poll.
  timeout: 90_000,
  expect: { timeout: 10_000 },
  reporter: [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]],
  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://localhost:8080',
    trace: 'retain-on-failure',
    // A click on something that never appears should fail in seconds with a name, not hang to the test timeout.
    actionTimeout: 20_000,
    screenshot: 'only-on-failure',
    viewport: { width: 1280, height: 800 },
  },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        viewport: { width: 1280, height: 800 },
        launchOptions: {
          // Real WebRTC with a fake camera, microphone and screen: the specs that join calls
          // need getUserMedia and getDisplayMedia to succeed without a prompt.
          args: [
            '--use-fake-ui-for-media-stream',
            '--use-fake-device-for-media-stream',
            '--auto-select-desktop-capture-source=Entire screen',
            '--autoplay-policy=no-user-gesture-required',
          ],
        },
      },
    },
  ],
});
