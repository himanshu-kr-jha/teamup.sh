import type { Locator, Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { apiAs, createCall, idOf, joinCall } from './support/meetings';

// From the screenshots of the 18 November call features together: on a phone the
// call's bar cut its title and its count short, a call between two showed the
// person called their own name, the corner window cut names short and wrapped a
// third tile to a second row, and the ring put the focus on Decline.

const SHOTS = 'test-results/callheader';
const heading = (page: Page) => page.getByTestId('call-heading');

/** Whether a piece of text is shown whole, not cut short with an ellipsis. */
const whole = (locator: Locator) => locator.evaluate((el) => el.scrollWidth <= el.clientWidth + 1);

test.describe('the call bar and the corner window', () => {
  test('a call between two: each sees the other, the title and count fit on a phone, and the ring focuses Answer', async ({ as }) => {
    const pat = await apiAs('pat');
    const room = await pat.post('/channels/direct', { userId: idOf('alice') });
    const alice = await as('alice', { phone: 'android', theme: 'dark' });
    const caller = await as('pat');
    await caller.page.goto(`/chat/${room.id}`);
    await caller.page.getByRole('button', { name: 'Call', exact: true }).click();

    const ring = alice.page.getByRole('dialog').filter({ hasText: /^Incoming call/ });
    await expect(ring).toBeVisible({ timeout: 20_000 });
    await expect(ring.getByRole('button', { name: 'Answer' }), 'Enter answers').toBeFocused();
    await alice.page.waitForTimeout(300); // the dialog's opening animation
    await alice.page.screenshot({ path: `${SHOTS}/ring-phone-dark.png` });
    await ring.getByRole('button', { name: 'Answer' }).click();
    await expect(alice.page.getByRole('button', { name: 'Leave the call' }).first()).toBeVisible({ timeout: 30_000 });

    // Named after the conversation as Pat sees it, which is Alice: each sees the other.
    await expect(heading(alice.page)).toContainText('Pat Platform');
    await expect(heading(alice.page)).not.toContainText('Alice Archer');
    await expect(heading(caller.page)).toContainText('Alice Archer');
    await expect(heading(alice.page)).toContainText('In a call · 2', { timeout: 20_000 });
    for (const line of await heading(alice.page).locator('span').all()) {
      expect(await whole(line), `"${await line.textContent()}" is shown whole at 390px`).toBe(true);
    }
    // The link and the guests are under More on a phone; the rest stay in reach.
    await expect(alice.page.getByRole('button', { name: 'More call options' })).toBeVisible();
    await expect(alice.page.getByRole('button', { name: 'Copy the link to this call' })).toBeHidden();
    await alice.page.screenshot({ path: `${SHOTS}/bar-phone-dark.png` });
    await caller.page.screenshot({ path: `${SHOTS}/bar-desktop-light.png` });
    await alice.page.getByRole('button', { name: 'More call options' }).click();
    await expect(alice.page.getByRole('menuitem', { name: 'Copy the link to this call' })).toBeVisible();
    await expect(alice.page.getByRole('menuitem', { name: 'Guests' })).toBeVisible();
    await alice.page.keyboard.press('Escape');
    await pat.dispose();
  });

  test('a meeting of three on a phone: a title that fits, and the corner window on one row with first names', async ({ as }) => {
    test.setTimeout(150_000);
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Release planning ${Date.now() % 1000}`, [idOf('alice'), idOf('max')]);
    const olivia = await as('owner');
    const alice = await as('alice');
    const max = await as('max', { phone: 'android', theme: 'dark' });
    await joinCall(olivia.page, call.id);
    await joinCall(alice.page, call.id);
    await joinCall(max.page, call.id);
    await expect(heading(max.page)).toContainText('In a call · 3', { timeout: 30_000 });
    for (const line of await heading(max.page).locator('span').all()) {
      expect(await whole(line), `"${await line.textContent()}" is shown whole at 390px`).toBe(true);
    }
    await max.page.screenshot({ path: `${SHOTS}/meeting-phone-dark.png` });

    await max.page.getByRole('button', { name: 'Minimise the call' }).click();
    const corner = max.page.getByTestId('call-window');
    await expect(corner).toBeVisible();
    const names = corner.locator('span[title]');
    await expect(names).toHaveCount(3, { timeout: 20_000 });
    await expect(corner.locator('span[title="Alice Archer"]')).toHaveText('Alice');
    await expect(corner.locator('span[title="Max Manager (you)"]')).toHaveText('You');
    const tops = await names.evaluateAll((els) => els.map((el) => Math.round(el.getBoundingClientRect().top)));
    expect(Math.max(...tops) - Math.min(...tops), `one row of tiles: ${tops.join(', ')}`).toBeLessThanOrEqual(2);
    for (const name of await names.all()) expect(await whole(name), `${await name.getAttribute('title')} fits`).toBe(true);
    await max.page.screenshot({ path: `${SHOTS}/corner-phone-dark.png` });
    await owner.dispose();
  });
});
