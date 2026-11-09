import { test as base, expect, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { BASE_URL } from './api';
import { PEOPLE, type Person, type PersonKey } from './people';

export interface Session {
  who: string;
  context: BrowserContext;
  page: Page;
  /** console.error output and uncaught exceptions seen on any page of this session. */
  problems: string[];
}

export interface SessionOptions {
  width?: number;
  height?: number;
  theme?: 'light' | 'dark';
  /** Skip the login form (for the login tests themselves). */
  signedOut?: boolean;
  /**
   * Show the welcome tour as a new person would see it. Every other session is
   * told it has seen the tour already, so it does not cover what a test is about.
   */
  tour?: boolean;
}

// Network failures the browser reports as console errors even though the test
// caused them on purpose (aborted or faked requests).
const EXPECTED_NOISE = [
  /Failed to load resource: net::ERR_FAILED/,
  // Opening the calendar of somebody you do not manage is refused on purpose (decision 64).
  /status of 403 .*\/api\/calendar\?/,
  // Test 39 makes the send fail with a 500 on purpose.
  /status of 500 .*\/api\/channels\/[^/]+\/messages/,
  // LiveKit's client logs this at error level whenever somebody hangs up while data is in flight.
  /DataChannel error on (lossy|reliable): User-Initiated Abort/,
  /publisher data channel '[A-Z_]+' closed unexpectedly/,
  // Playwright contexts are incognito, where Chrome cannot subscribe to push.
  /does not support the Push API in incognito mode/,
  // Following a private call's link meets the door: the token is refused with 403 (knock_required),
  // and asking again within a minute of a denial is refused with 429. The page handles both.
  /status of 403 .*\/api\/calls\/[^/]+\/token/,
  /status of 429 .*\/api\/calls\/[^/]+\/knock/,
  // A link to a call that has ended is refused with 409 and the page says the call has ended.
  /status of 409 .*\/api\/calls\/[^/]+\/(token|knock)/,
  // A link into another organization's call is a 404, as if the call did not exist.
  /status of 404 .*\/api\/calls\/[^/]+\/token/,
  // The page reads the call before deciding whether to join, and gets the same 404.
  /status of 404 .*\/api\/calls\/[^/]+$/,
  // Reloading a standing room asks who is knocking, and a room nobody owns answers 403.
  /status of 403 .*\/api\/calls\/[^/]+\/requests/,
  // The call log of a call you are not part of is refused with 404 and the dialog says so.
  /status of 404 .*\/api\/calls\/[^/]+\/log/,
  // A guest's wrong passcode is refused with 401, and a link that was turned off with 404;
  // the guest page says so.
  /status of 401 .*\/api\/guest\/[^/]+\/join/,
  /status of 404 .*\/api\/guest\/[^/]+$/,
  // LiveKit's client logs this at error level when a track arrives before the participant it
  // belongs to, as when somebody rejoins under the same identity at once. The track is set up
  // again once the participant is known: meetings.y-guests.spec.ts checks the sound arrives.
  /Tried to add a track for a participant, that's not present/,
];
// Asking "who am I?" before signing in is answered 401 by design, and the browser logs every 401.
const SIGNED_OUT_PROBES = /status of 401 .*\/api\/auth\/(me|refresh)\b/;

/**
 * Signs in through the real login form, in a fresh browser context, so two
 * people can be on screen at once.
 */
export async function openSession(browser: Browser, who: PersonKey | Person, options: SessionOptions = {}): Promise<Session> {
  const context = await browser.newContext({
    baseURL: BASE_URL,
    viewport: { width: options.width ?? 1280, height: options.height ?? 800 },
    colorScheme: options.theme ?? 'light',
  });
  const theme = options.theme ?? 'light';
  await context.addInitScript((t) => {
    try {
      localStorage.setItem('theme', t);
    } catch {
      /* private mode */
    }
  }, theme);
  const problems: string[] = [];
  const watch = (page: Page) => {
    page.on('console', (m) => {
      if (m.type() === 'error' && !EXPECTED_NOISE.some((re) => re.test(`${m.text()} ${m.location().url}`)) && !SIGNED_OUT_PROBES.test(`${m.text()} ${m.location().url}`)) {
        problems.push(`[console] ${m.text()} (${m.location().url})`);
      }
    });
    page.on('pageerror', (e) => problems.push(`[pageerror] ${e.message}`));
  };
  context.on('page', watch);
  if (!options.tour) await hideWelcomeTour(context);
  const page = await context.newPage();

  if (!options.signedOut) await signIn(page, who);
  return { who: typeof who === 'string' ? who : who.name, context, page, problems };
}

/**
 * Tells a browser that its person has seen the welcome tour already, so it does
 * not cover what a test is about. Said in the answers that carry the person
 * rather than stored, so a test that wants the tour for the same person can
 * still have it.
 */
export async function hideWelcomeTour(context: BrowserContext) {
  await context.route(/\/api\/auth\/(me|login)$/, async (route) => {
    const response = await route.fetch();
    let body = await response.text();
    try {
      const json = JSON.parse(body);
      if (json?.user && json.user.tourCompletedAt == null) {
        json.user.tourCompletedAt = '2000-01-01T00:00:00Z';
        body = JSON.stringify(json);
      }
    } catch {
      // Not JSON; passed on as it is.
    }
    await route.fulfill({ response, body });
  });
}

/** The real login form: type an email and a password, press the button. */
export async function signIn(page: Page, who: PersonKey | { email: string; password: string }) {
  const person = typeof who === 'string' ? PEOPLE[who] : who;
  await page.goto('/login');
  await page.getByLabel('Email').fill(person.email);
  await page.getByLabel('Password').fill(person.password);
  await page.getByRole('button', { name: /^sign in$/i }).click();
  await expect(page).toHaveURL(/\/chat/);
}

export const test = base.extend<{
  as: (who: PersonKey | Person, options?: SessionOptions) => Promise<Session>;
}>({
  as: async ({ browser }, use) => {
    const sessions: Session[] = [];
    await use(async (who, options) => {
      const session = await openSession(browser, who, options);
      sessions.push(session);
      return session;
    });
    const all = sessions.flatMap((s) => s.problems.map((p) => `${s.who}: ${p}`));
    await Promise.all(sessions.map((s) => s.context.close()));
    // A page that logs errors to the console is broken in a way no assertion
    // above necessarily noticed.
    expect(all, 'console errors and uncaught exceptions').toEqual([]);
  },
});

export { expect };

/** Opens /tasks on a tab and waits until the tab strip is drawn. */
export async function openTasks(page: Page, query = '') {
  await page.goto(`/tasks${query}`);
  await expect(page.getByRole('tablist', { name: 'Task views' })).toBeVisible();
}

export function tabNames(page: Page) {
  return page.getByRole('tablist', { name: 'Task views' }).getByRole('tab');
}

/** The five tiles at the top of a view, by label. */
export async function tileCounts(page: Page): Promise<Record<string, number>> {
  const out: Record<string, number> = {};
  for (const label of ['Pending', 'Ongoing', 'Blocked', 'Overdue', 'Completed']) {
    const value = page.locator('dl > div', { has: page.getByText(label, { exact: true }) }).first().locator('dd');
    out[label.toLowerCase()] = Number(await value.innerText());
  }
  return out;
}

/** The YYYY-MM-DD of a date as the browser's local calendar sees it. */
export function localDay(offsetDays = 0): string {
  const d = new Date();
  d.setDate(d.getDate() + offsetDays);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/** New task dialog, filled in the way a person would. */
export async function createTaskInUi(
  page: Page,
  task: { title: string; assignee?: string; dueDay?: string }
) {
  await page.getByRole('button', { name: /^new task$/i }).first().click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Title').fill(task.title);
  if (task.assignee) {
    await dialog.getByLabel('Assignee').click();
    await page.getByRole('option', { name: task.assignee }).click();
  }
  if (task.dueDay) await dialog.getByLabel('Due date').fill(task.dueDay);
  await dialog.getByRole('button', { name: 'Create task' }).click();
  await expect(dialog).toBeHidden();
}
