import path from 'node:path';
import { fileURLToPath } from 'node:url';
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
/**
 * The suite runs at midday. Many specs place things "30 minutes ago" or "today",
 * and between midnight and half past those land on yesterday, which fails tests
 * that are about something else. So the test code and the browser both run in
 * whichever of these zones it is nearest noon in now; the server works in
 * instants and each meeting's own zone, so it is unaffected. E2E_TZ overrides.
 * Midnight itself is covered by the Go tests, which set their clocks.
 */
const MIDDAY_ZONES = [
  'Pacific/Kiritimati', 'Pacific/Auckland', 'Australia/Sydney', 'Asia/Tokyo', 'Asia/Shanghai',
  'Asia/Bangkok', 'Asia/Dhaka', 'Asia/Karachi', 'Asia/Dubai', 'Europe/Moscow', 'Africa/Cairo',
  'Europe/Paris', 'Europe/London', 'Atlantic/Azores', 'America/Noronha', 'America/Sao_Paulo',
  'America/Halifax', 'America/New_York', 'America/Chicago', 'America/Denver', 'America/Los_Angeles',
  'America/Anchorage', 'Pacific/Honolulu', 'Pacific/Pago_Pago',
];
function middayZone(): string {
  const now = new Date();
  const hour = (zone: string) => {
    const parts = new Intl.DateTimeFormat('en-GB', { timeZone: zone, hour: 'numeric', minute: 'numeric', hourCycle: 'h23' }).formatToParts(now);
    const get = (t: string) => Number(parts.find((p) => p.type === t)?.value ?? 0);
    return get('hour') + get('minute') / 60;
  };
  return MIDDAY_ZONES.reduce((best, z) => (Math.abs(hour(z) - 12) < Math.abs(hour(best) - 12) ? z : best));
}
// Set before the workers start, so they inherit it.
process.env.TZ = process.env.E2E_TZ ?? process.env.TZ_E2E_CHOSEN ?? middayZone();
process.env.TZ_E2E_CHOSEN = process.env.TZ;

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
    timezoneId: process.env.TZ,
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
            // Speech-like audio instead of the default short beep, so that LiveKit
            // hears somebody talking, as the meeting note-taker needs.
            `--use-file-for-fake-audio-capture=${path.resolve(path.dirname(fileURLToPath(import.meta.url)), 'e2e/fixtures/speech.wav')}`,
            '--auto-select-desktop-capture-source=Entire screen',
            '--autoplay-policy=no-user-gesture-required',
          ],
        },
      },
    },
  ],
});
