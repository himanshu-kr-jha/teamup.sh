import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { apiAs, createCall, idOf, joinCall } from './support/meetings';

// Asked for: the minimised call window can be made bigger or smaller, the
// people in it grow and shrink with it, and on a phone it follows a finger.

const callWindow = (page: Page) => page.getByTestId('call-window');

async function box(page: Page) {
  const b = await callWindow(page).boundingBox();
  if (!b) throw new Error('the call window is not on screen');
  return b;
}

/** A finger, through Chromium's own touch input, so the page's touch-action rules apply as on a phone. */
async function swipe(page: Page, from: { x: number; y: number }, to: { x: number; y: number }) {
  const cdp = await page.context().newCDPSession(page);
  const point = (p: { x: number; y: number }) => [{ x: Math.round(p.x), y: Math.round(p.y), id: 1 }];
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: point(from) });
  for (let i = 1; i <= 8; i++) {
    const at = { x: from.x + ((to.x - from.x) * i) / 8, y: from.y + ((to.y - from.y) * i) / 8 };
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: point(at) });
  }
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
  await cdp.detach();
}

/**
 * Waits for the call to be over on the server. A page closed the moment Leave
 * is clicked can leave its person in the call for a while, and the tests after
 * this one would find them busy. The media server closes an empty room some
 * twenty seconds after the last person has gone, which is what ends the call.
 */
async function allGone(api: Awaited<ReturnType<typeof apiAs>>, callId: string) {
  await expect
    .poll(async () => (await api.get(`/calls/${callId}`)).status, { message: 'the call to be over', timeout: 60_000 })
    .toBe('ended');
}

test.describe('the minimised call window', () => {
  test('is made bigger and smaller from its corner, its people grow with it, and it keeps that size', async ({ as }) => {
    test.setTimeout(120_000);
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Window ${Date.now()}`, [idOf('alice')]);
    const olivia = await as('owner');
    await joinCall(olivia.page, call.id);
    const alice = await as('alice');
    await joinCall(alice.page, call.id);
    await expect(olivia.page.getByText('Alice Archer', { exact: true }).first()).toBeVisible({ timeout: 20_000 });

    await olivia.page.getByRole('button', { name: 'Minimise the call' }).click();
    const before = await box(olivia.page);
    expect(Math.round(before.width)).toBe(320);
    const picture = olivia.page.getByTestId('call-window').locator('[class*="cqmin"]').first();
    const smallPicture = (await picture.boundingBox())!.width;

    // Pulled out from the top left corner, away from where it is anchored.
    const grip = olivia.page.getByRole('button', { name: 'Resize the call window' });
    const g = (await grip.boundingBox())!;
    await olivia.page.mouse.move(g.x + g.width / 2, g.y + g.height / 2);
    await olivia.page.mouse.down();
    await olivia.page.mouse.move(g.x - 200, g.y - 160, { steps: 8 });
    await olivia.page.mouse.up();
    const bigger = await box(olivia.page);
    expect(bigger.width).toBeGreaterThan(before.width + 150);
    expect(bigger.height).toBeGreaterThan(before.height + 120);
    // Still anchored where it was: the bottom right corner did not move.
    expect(Math.abs(bigger.x + bigger.width - (before.x + before.width))).toBeLessThan(2);
    expect(Math.abs(bigger.y + bigger.height - (before.y + before.height))).toBeLessThan(2);
    // The people in it grew too.
    await expect.poll(async () => (await picture.boundingBox())!.width).toBeGreaterThan(smallPicture + 10);
    await olivia.page.screenshot({ path: 'test-results/call-window-bigger.png' });

    // Never past its largest, however far it is pulled.
    const g2 = (await grip.boundingBox())!;
    await olivia.page.mouse.move(g2.x + 5, g2.y + 5);
    await olivia.page.mouse.down();
    await olivia.page.mouse.move(5, 5, { steps: 8 });
    await olivia.page.mouse.up();
    expect((await box(olivia.page)).width).toBeLessThanOrEqual(720.5);

    // And from the keyboard, smaller, but never below its smallest.
    await grip.focus();
    for (let i = 0; i < 40; i++) await olivia.page.keyboard.press('ArrowRight');
    for (let i = 0; i < 40; i++) await olivia.page.keyboard.press('ArrowDown');
    const smallest = await box(olivia.page);
    expect(Math.round(smallest.width)).toBe(240);
    expect(Math.round(smallest.height)).toBe(150);
    // The buttons on it still work at its smallest.
    await expect(callWindow(olivia.page).getByRole('button', { name: 'Expand the call' })).toBeVisible();

    // The size is kept for the next time on this device.
    for (let i = 0; i < 6; i++) await olivia.page.keyboard.press('ArrowLeft');
    const kept = await box(olivia.page);
    await olivia.page.getByRole('button', { name: 'Expand the call' }).click();
    await olivia.page.getByRole('button', { name: 'Minimise the call' }).click();
    expect(Math.round((await box(olivia.page)).width)).toBe(Math.round(kept.width));
    const stored = await olivia.page.evaluate(() => JSON.parse(localStorage.getItem('teamup.callWindow.size') ?? 'null'));
    expect(Math.round(stored.width)).toBe(Math.round(kept.width));

    await olivia.page.getByRole('button', { name: 'Leave the call' }).click();
    await expect(olivia.page.getByRole('button', { name: 'Leave the call' })).toHaveCount(0);
    await alice.page.getByRole('button', { name: 'Leave the call' }).click();
    await expect(alice.page.getByRole('button', { name: 'Leave the call' })).toHaveCount(0);
    await allGone(owner, call.id);
    await owner.dispose();
  });

  test('on a phone it follows a finger from anywhere on it, and stays on screen', async ({ as }) => {
    test.setTimeout(120_000);
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Phone window ${Date.now()}`, [idOf('alice')]);
    const alice = await as('alice', { phone: 'android' });
    await joinCall(alice.page, call.id);
    await alice.page.getByRole('button', { name: 'Minimise the call' }).click();
    await expect(callWindow(alice.page)).toBeVisible();
    const before = await box(alice.page);

    // A finger on the middle of the window, the video rather than its bar, moves it up and left.
    const middle = { x: before.x + before.width / 2, y: before.y + before.height / 2 + 20 };
    await swipe(alice.page, middle, { x: middle.x - 40, y: middle.y - 250 });
    const moved = await box(alice.page);
    expect(moved.y).toBeLessThan(before.y - 200);
    expect(moved.x).toBeLessThan(before.x - 20);
    // The page under it did not scroll instead.
    expect(await alice.page.evaluate(() => window.scrollY)).toBe(0);
    await alice.page.screenshot({ path: 'test-results/call-window-phone-moved.png' });

    // Flung far past the top: it stops at the edge rather than going off it.
    const now = await box(alice.page);
    await swipe(alice.page, { x: now.x + now.width / 2, y: now.y + now.height / 2 }, { x: now.x + now.width / 2, y: -400 });
    const top = await box(alice.page);
    expect(top.y).toBeGreaterThanOrEqual(7.5);

    // Made bigger with a finger on its grip, no wider than the screen.
    const grip = (await alice.page.getByRole('button', { name: 'Resize the call window' }).boundingBox())!;
    await swipe(alice.page, { x: grip.x + grip.width / 2, y: grip.y + grip.height / 2 }, { x: 0, y: grip.y + grip.height / 2 });
    const wide = await box(alice.page);
    expect(wide.width).toBeGreaterThan(before.width);
    expect(wide.x).toBeGreaterThanOrEqual(7.5);
    expect(wide.x + wide.width).toBeLessThanOrEqual(390 - 7.5);

    await alice.page.getByRole('button', { name: 'Leave the call' }).click();
    await expect(alice.page.getByRole('button', { name: 'Leave the call' })).toHaveCount(0);
    await allGone(owner, call.id);
    await owner.dispose();
  });
});
