import { test, expect } from '@playwright/test';
import type { Browser, BrowserContext, Page } from '@playwright/test';
import { BASE_URL } from './support/api';
import { signIn } from './support/fixtures';
import { apiAs, createCall, idOf, joinCall, leaveCall, waitForLog } from './support/meetings';
import { startSwapServer, type SwapServer } from './support/swap-server';

/** A person's browser on the swappable origin, with the service worker installed and in charge of the page. */
async function openBrowser(browser: Browser, swap: SwapServer, who: 'alice', options: { fakeClock?: boolean } = {}) {
  const context = await browser.newContext({ baseURL: swap.url, viewport: { width: 1280, height: 800 } });
  const page = await context.newPage();
  if (options.fakeClock) await page.clock.install();
  await signIn(page, who);
  await page.evaluate(() => navigator.serviceWorker.ready);
  // A page is controlled from its next load.
  await page.reload();
  await expect.poll(() => page.evaluate(() => !!navigator.serviceWorker.controller)).toBe(true);
  return { context, page };
}

/** Marks this document, so that any reload at all, however quick, is seen. */
const markDocument = (page: Page) => page.evaluate(() => ((window as unknown as { __sameDocument: boolean }).__sameDocument = true));
const sameDocument = (page: Page) => page.evaluate(() => (window as unknown as { __sameDocument?: boolean }).__sameDocument === true);
/** Whether this page was served the build deploy() put up. */
const onNewBuild = (page: Page) => page.evaluate(() => !!document.querySelector('meta[name="teamup-build"]'));
/** What the browser does by itself shortly after a page opens. Done by hand because it does not reliably do it while being driven. */
const checkForUpdate = (page: Page) =>
  page.evaluate(async () => {
    await (await navigator.serviceWorker.getRegistration())?.update();
  });
const offer = (page: Page) => page.getByRole('status').filter({ hasText: 'A new version of TeamUp is available.' });

test.describe('a deploy while people have TeamUp open', () => {
  let swap: SwapServer;
  let context: BrowserContext | undefined;

  test.beforeEach(async () => {
    swap = await startSwapServer(BASE_URL);
  });
  test.afterEach(async () => {
    await context?.close();
    context = undefined;
    await swap.close();
  });

  test('a call is never reloaded: not when the version arrives, not when another tab accepts it; it is offered after the call', async ({ browser }) => {
    const alice = await openBrowser(browser, swap, 'alice');
    context = alice.context;
    const api = await apiAs('owner');
    const call = await createCall(api, 'Deploy during a call', [idOf('alice')]);
    const inTheCall = alice.page;

    await joinCall(inTheCall, call.id);
    await waitForLog(api, call.id, 'Alice to be in the call', (l) => l.sessions.some((s) => s.userId === idOf('alice') && s.joinedAt && !s.leftAt));
    await markDocument(inTheCall);
    let loads = 0;
    inTheCall.on('load', () => loads++);

    swap.deploy();
    const other = await alice.context.newPage();
    await other.goto('/tasks');
    await checkForUpdate(other);

    // The other tab is told, quietly. The one in the call is not interrupted at all.
    await expect(offer(other)).toBeVisible();
    await expect(offer(inTheCall)).toHaveCount(0);
    expect(await sameDocument(inTheCall)).toBe(true);

    // Accepting there moves that tab, and the new worker takes over, while the call carries on.
    await markDocument(other);
    await offer(other).getByRole('button', { name: 'Reload' }).click();
    await expect.poll(() => sameDocument(other).catch(() => true), { timeout: 20_000 }).toBe(false);
    await expect.poll(() => onNewBuild(other)).toBe(true);
    await expect(offer(other)).toHaveCount(0);

    expect(loads).toBe(0);
    expect(await sameDocument(inTheCall)).toBe(true);
    await expect(inTheCall.getByRole('button', { name: 'Leave the call' })).toBeVisible();
    await expect(offer(inTheCall)).toHaveCount(0);
    const log = await waitForLog(api, call.id, 'the log', () => true);
    const sessions = log.sessions.filter((s) => s.userId === idOf('alice'));
    expect(sessions.length).toBe(1);
    expect(sessions[0].leftAt ?? null).toBeNull();

    // After the call, that tab gets the offer as well, and the reload lands on the new build.
    await leaveCall(inTheCall);
    await expect(offer(inTheCall)).toBeVisible();
    expect(await sameDocument(inTheCall)).toBe(true);
    expect(await onNewBuild(inTheCall)).toBe(false);
    await offer(inTheCall).getByRole('button', { name: 'Reload' }).click();
    await expect.poll(() => sameDocument(inTheCall).catch(() => true), { timeout: 20_000 }).toBe(false);
    await expect.poll(() => onNewBuild(inTheCall)).toBe(true);
    await api.dispose();
  });

  test('one tab in a call hears of a new version itself, says nothing until the call ends, and "not now" leaves the page alone', async ({ browser }) => {
    const alice = await openBrowser(browser, swap, 'alice');
    context = alice.context;
    const api = await apiAs('owner');
    const call = await createCall(api, 'Deploy, one tab', [idOf('alice')]);

    await joinCall(alice.page, call.id);
    await markDocument(alice.page);
    swap.deploy();
    await checkForUpdate(alice.page);
    await expect
      .poll(() => alice.page.evaluate(async () => !!(await navigator.serviceWorker.getRegistration())?.waiting), { timeout: 20_000 })
      .toBe(true);
    await expect(offer(alice.page)).toHaveCount(0);
    expect(await sameDocument(alice.page)).toBe(true);

    await leaveCall(alice.page);
    await expect(offer(alice.page)).toBeVisible();
    await alice.page.screenshot({ path: test.info().outputPath('offer.png') });
    await offer(alice.page).getByRole('button', { name: 'Not now' }).click();
    await expect(offer(alice.page)).toHaveCount(0);
    expect(await sameDocument(alice.page)).toBe(true);
    await api.dispose();
  });

  test('a tab left open finds the new version by itself within the hour', async ({ browser }) => {
    const alice = await openBrowser(browser, swap, 'alice', { fakeClock: true });
    context = alice.context;
    await markDocument(alice.page);
    swap.deploy();
    await expect(offer(alice.page)).toHaveCount(0);
    await alice.page.clock.fastForward(61 * 60 * 1000);
    await expect(offer(alice.page)).toBeVisible({ timeout: 20_000 });
    expect(await sameDocument(alice.page)).toBe(true);
  });
});
