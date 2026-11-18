import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { apiAs, createCall, idOf, joinCall, leaveCall, prejoinScreen, waitForLog } from './support/meetings';

// Asked for (19 November): a refresh in the middle of a call goes straight back
// into it, with the microphone as it was, and the others do not see you leave;
// Back on the call's page keeps the call, shrunk into the corner.

const SHOTS = 'test-results/rejoin';

const callWindow = (page: Page) => page.getByTestId('call-window');
const leaveButton = (page: Page) => page.getByRole('button', { name: 'Leave the call' }).first();
const reconnecting = (page: Page) => page.getByRole('status').filter({ hasText: 'Reconnecting to the call' });

/** Writes down every toast that says somebody joined or left, from now on. */
async function watchArrivals(page: Page) {
  await page.evaluate(() => {
    const w = window as unknown as { __arrivals: string[] };
    w.__arrivals = [];
    new MutationObserver((changes) => {
      for (const c of changes) {
        c.addedNodes.forEach((n) => {
          const t = n.textContent ?? '';
          if (/joined the call|left the call/.test(t)) w.__arrivals.push(t);
        });
      }
    }).observe(document.body, { childList: true, subtree: true, characterData: true });
  });
}
const arrivals = (page: Page) => page.evaluate(() => (window as unknown as { __arrivals: string[] }).__arrivals);

/** Holds back the token a page asks for, so a moment that is usually too short to see can be looked at. */
async function slowToken(page: Page, ms: number) {
  await page.route(/\/api\/calls\/[^/]+\/token$/, async (route) => {
    await new Promise((r) => setTimeout(r, ms));
    await route.continue();
  });
}

test.describe('a call that survives a refresh', () => {
  test('a reload goes straight back in, microphone on, and the other person never sees a leave or a join', async ({ as }) => {
    test.setTimeout(150_000);
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Refresh ${Date.now()}`, [idOf('alice')]);

    const alice = await as('alice');
    await joinCall(alice.page, call.id);
    const olivia = await as('owner');
    await joinCall(olivia.page, call.id);
    await olivia.page.getByRole('button', { name: 'Turn your microphone on' }).click();
    await expect(olivia.page.getByRole('button', { name: 'Turn your microphone off' })).toBeVisible({ timeout: 20_000 });
    await expect(olivia.page.getByText('Alice Archer', { exact: true }).first()).toBeVisible({ timeout: 20_000 });
    await expect(alice.page.getByText('Olivia Owner', { exact: true }).first()).toBeVisible({ timeout: 20_000 });
    // Past the moment arrivals are only the room you walked into.
    await alice.page.waitForTimeout(2500);
    await watchArrivals(alice.page);

    // Slow enough to see what the page says meanwhile.
    await slowToken(olivia.page, 2500);
    await olivia.page.reload();
    await expect(reconnecting(olivia.page)).toBeVisible();
    await expect(prejoinScreen(olivia.page)).toHaveCount(0);
    await olivia.page.screenshot({ path: `${SHOTS}/reconnecting-call-page.png` });
    await expect(leaveButton(olivia.page)).toBeVisible({ timeout: 30_000 });
    await expect(prejoinScreen(olivia.page)).toHaveCount(0);
    // Full size on its own page, with the microphone as it was.
    await expect(callWindow(olivia.page)).toHaveCount(0);
    await expect(olivia.page.getByRole('button', { name: 'Turn your microphone off' })).toBeVisible({ timeout: 20_000 });
    await expect(olivia.page.getByText('Alice Archer', { exact: true }).first()).toBeVisible({ timeout: 20_000 });
    await expect(reconnecting(olivia.page)).toHaveCount(0);
    await olivia.page.unroute(/\/api\/calls\/[^/]+\/token$/);

    // Alice saw nothing: no "left", and no "joined" once Olivia was back.
    await alice.page.waitForTimeout(4000);
    expect(await arrivals(alice.page)).toEqual([]);
    await expect(alice.page.getByText('Olivia Owner', { exact: true }).first()).toBeVisible();

    // A reload elsewhere in the app comes back in the corner.
    await olivia.page.getByRole('button', { name: 'Minimise the call' }).click();
    await expect(callWindow(olivia.page)).toBeVisible();
    await slowToken(olivia.page, 2500);
    await olivia.page.reload();
    await expect(reconnecting(olivia.page)).toBeVisible();
    await olivia.page.screenshot({ path: `${SHOTS}/reconnecting-corner.png` });
    await expect(callWindow(olivia.page)).toBeVisible({ timeout: 30_000 });
    await expect(callWindow(olivia.page).getByText('2 in call')).toBeVisible({ timeout: 20_000 });
    await olivia.page.unroute(/\/api\/calls\/[^/]+\/token$/);
    expect(await arrivals(alice.page)).toEqual([]);

    // The attendance trail has her in the call the whole time but for the reload.
    const log = await waitForLog(owner, call.id, 'Olivia back in after the reloads', (l) =>
      l.sessions.filter((s) => s.userName === 'Olivia Owner').length >= 3 && l.sessions.some((s) => s.userName === 'Olivia Owner' && !s.leftAt)
    );
    expect(log.sessions.filter((s) => !s.leftAt).length).toBe(2);
    expect((await owner.get(`/calls/${call.id}`)).status).toBe('active');

    for (const s of [alice, olivia]) await leaveCall(s.page);
    await owner.dispose();
    expect(alice.problems).toEqual([]);
    expect(olivia.problems).toEqual([]);
  });

  test('the person left behind does not end a call somebody is only reloading back into', async ({ as }) => {
    test.setTimeout(150_000);
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Refresh alone ${Date.now()}`, [idOf('alice')]);
    const alice = await as('alice');
    await joinCall(alice.page, call.id);
    const olivia = await as('owner');
    await joinCall(olivia.page, call.id);
    await expect(alice.page.getByText('Olivia Owner', { exact: true }).first()).toBeVisible({ timeout: 20_000 });

    // Olivia reloads, slowly, and Alice hangs up while she is away.
    await slowToken(olivia.page, 4000);
    await olivia.page.reload();
    await expect(reconnecting(olivia.page)).toBeVisible();
    await expect(alice.page.getByText('Olivia Owner', { exact: true })).toHaveCount(0, { timeout: 20_000 });
    await leaveCall(alice.page);

    // Alice looked like the last one out, but the call is still on and Olivia is back in it.
    await expect(leaveButton(olivia.page)).toBeVisible({ timeout: 30_000 });
    await expect(olivia.page.getByText('Waiting for others')).toBeVisible({ timeout: 20_000 });
    expect((await owner.get(`/calls/${call.id}`)).status).toBe('active');

    // Leaving for real, as the last one, ends it.
    await leaveCall(olivia.page);
    await expect.poll(async () => (await owner.get(`/calls/${call.id}`)).status, { timeout: 30_000 }).toBe('ended');
    await owner.dispose();
    expect(alice.problems).toEqual([]);
    expect(olivia.problems).toEqual([]);
  });

  test('leaving and then reloading does not go back in', async ({ as }) => {
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Left ${Date.now()}`, [idOf('alice')]);
    const alice = await as('alice');
    await joinCall(alice.page, call.id);
    const olivia = await as('owner');
    await joinCall(olivia.page, call.id);
    // Leaving before Alice is even on screen would look like being last out.
    await expect(olivia.page.getByText('Alice Archer', { exact: true }).first()).toBeVisible({ timeout: 20_000 });
    await leaveCall(olivia.page);
    await expect(olivia.page).toHaveURL(/\/calls$/);
    await olivia.page.reload();
    await expect(olivia.page.getByRole('heading', { name: 'Calls' }).first()).toBeVisible({ timeout: 20_000 });
    await olivia.page.waitForTimeout(3000);
    await expect(leaveButton(olivia.page)).toHaveCount(0);
    await expect(callWindow(olivia.page)).toHaveCount(0);
    await expect(reconnecting(olivia.page)).toHaveCount(0);
    await leaveCall(alice.page);
    await owner.dispose();
    expect(olivia.problems).toEqual([]);
  });
});

test.describe('Back on a call', () => {
  test('takes you back in the app with the call in the corner, also when the call was the first page', async ({ as }) => {
    test.setTimeout(120_000);
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Back ${Date.now()}`, [idOf('alice')]);
    const alice = await as('alice');
    await joinCall(alice.page, call.id);

    // Opened straight from its link: Back would have left the site, and the call.
    const olivia = await as('owner', { phone: 'android' });
    const page = olivia.page;
    await joinCall(page, call.id);
    await expect(page.getByText('Alice Archer', { exact: true }).first()).toBeVisible({ timeout: 20_000 });
    await page.goBack();
    await expect(page).toHaveURL(/\/calls$/);
    await expect(callWindow(page)).toBeVisible();
    await expect(callWindow(page).getByText('2 in call')).toBeVisible({ timeout: 20_000 });
    await page.screenshot({ path: `${SHOTS}/back-phone.png` });

    // Back into it from the corner, and Back again: to the page before, still in the call.
    await page.getByRole('button', { name: 'Expand the call' }).click();
    await expect(page).toHaveURL(new RegExp(`/calls/${call.id}$`));
    await expect(callWindow(page)).toHaveCount(0);
    await page.goBack();
    await expect(page).toHaveURL(/\/calls$/);
    await expect(callWindow(page).getByText('2 in call')).toBeVisible({ timeout: 20_000 });
    await expect(alice.page.getByText('Olivia Owner', { exact: true }).first()).toBeVisible();

    for (const s of [alice, olivia]) await leaveCall(s.page);
    await owner.dispose();
    expect(olivia.problems).toEqual([]);
  });
});
