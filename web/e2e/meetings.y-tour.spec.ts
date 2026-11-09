import type { Locator, Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { sql } from './support/meetings';

// The tour somebody is shown the first time they sign in: a look around, one
// place at a time, then turning on notifications, then a welcome. Once only, kept
// by the server, and Settings can show it again.

const notSeen = (email: string) => sql(`UPDATE users SET tour_completed_at = NULL WHERE email = '${email}'`);

/** Whether the light is on this element: the lit box sits around it. */
async function litOn(page: Page, target: Locator) {
  const box = await target.boundingBox();
  const light = await page.getByTestId('welcome-tour').locator('div.rounded-xl').first().boundingBox();
  if (!box || !light) return false;
  return (
    light.x <= box.x + 1 && light.y <= box.y + 1 && light.x + light.width >= box.x + box.width - 1 && light.y + light.height >= box.y + box.height - 1
  );
}

test.describe('the welcome tour', () => {
  test('a new person is shown around, turns on notifications, is welcomed, and is not shown it again', async ({ as }) => {
    notSeen('sam@e2e.test');
    const sam = await as('sam', { tour: true });
    // The headless browser says notifications are blocked whatever it is given,
    // so the browser's side is stood in for: not asked yet, then allowed when asked.
    await sam.context.addInitScript(() => {
      let permission: NotificationPermission = 'default';
      Object.defineProperty(Notification, 'permission', { get: () => permission, configurable: true });
      Notification.requestPermission = async () => (permission = 'granted');
    });
    await sam.page.reload();
    const tour = sam.page.getByRole('dialog', { name: 'Hello, Sam' });
    await expect(tour).toBeVisible({ timeout: 10_000 });
    await tour.getByRole('button', { name: 'Show me around' }).click();

    const main = sam.page.getByRole('navigation', { name: 'Main' });
    const places: [string, Locator][] = [
      ['Chat', main.getByRole('link', { name: 'Chat' })],
      ['Calls and meetings', main.getByRole('link', { name: 'Calls' })],
      ['Meeting notes', main.getByRole('link', { name: 'Meeting notes' })],
      ['Calendar', main.getByRole('link', { name: 'Calendar' })],
      ['Tasks', main.getByRole('link', { name: 'Tasks' })],
      ['Your availability', sam.page.locator('[data-tour="availability"]')],
    ];
    for (const [i, [title, target]] of places.entries()) {
      const step = sam.page.getByRole('dialog', { name: title });
      await expect(step).toBeVisible();
      await expect(step).toContainText(`${i + 1} of ${places.length}`);
      await expect.poll(() => litOn(sam.page, target), { message: `the light is on ${title}` }).toBe(true);
      await step.getByRole('button', { name: 'Next' }).click();
    }

    const ask = sam.page.getByRole('dialog', { name: 'Turn on notifications' });
    await expect(ask).toBeVisible();
    await ask.getByRole('button', { name: 'Turn on notifications' }).click();
    const done = sam.page.getByRole('dialog', { name: 'You will be notified' });
    await expect(done).toBeVisible();
    await done.getByRole('button', { name: 'Continue' }).click();

    const welcome = sam.page.getByRole('dialog', { name: 'Welcome to E2E Org, Sam!' });
    await expect(welcome).toBeVisible();
    await welcome.getByRole('button', { name: 'Get started' }).click();
    await expect(sam.page.getByTestId('welcome-tour')).toHaveCount(0);

    // Kept by the server: not shown again, here or anywhere else.
    expect(sql(`SELECT tour_completed_at IS NOT NULL FROM users WHERE email = 'sam@e2e.test'`)).toBe('t');
    await sam.page.reload();
    await sam.page.waitForTimeout(1500);
    await expect(sam.page.getByTestId('welcome-tour')).toHaveCount(0);
  });

  test('Escape leaves it, Settings shows it again, and on a phone it points at the bar along the bottom', async ({ as }) => {
    notSeen('alice@e2e.test');
    const ownersBefore = sql(`SELECT COALESCE(tour_completed_at::text, 'never') FROM users WHERE email = 'owner@e2e.test'`);
    const alice = await as('alice', { tour: true, width: 390, height: 800 });
    const tour = alice.page.getByTestId('welcome-tour');
    await expect(tour).toBeVisible({ timeout: 10_000 });
    await alice.page.getByRole('button', { name: 'Show me around' }).click();

    // A phone's bar has no meeting notes, so there is one place fewer, and the
    // card sits above the light rather than beside it.
    const bar = alice.page.getByRole('navigation', { name: 'Sections' });
    const step = alice.page.getByRole('dialog', { name: 'Chat' });
    await expect(step).toContainText('1 of 5');
    await expect.poll(() => litOn(alice.page, bar.getByRole('link', { name: 'Chat' }))).toBe(true);
    const card = await step.locator('div.rounded-2xl').boundingBox();
    const tab = await bar.getByRole('link', { name: 'Chat' }).boundingBox();
    expect(card!.y + card!.height).toBeLessThanOrEqual(tab!.y);
    expect(card!.x).toBeGreaterThanOrEqual(0);
    expect(card!.x + card!.width).toBeLessThanOrEqual(390);

    await alice.page.keyboard.press('Escape');
    await expect(tour).toHaveCount(0);
    expect(sql(`SELECT tour_completed_at IS NOT NULL FROM users WHERE email = 'alice@e2e.test'`)).toBe('t');

    await alice.page.goto('/settings');
    await alice.page.getByRole('button', { name: 'Show the tour again' }).click();
    await expect(tour).toBeVisible({ timeout: 10_000 });
    await expect(alice.page.getByRole('dialog', { name: 'Hello, Alice' })).toBeVisible();
    await alice.page.getByRole('button', { name: 'Skip the tour' }).click();
    await expect(tour).toHaveCount(0);

    // Nobody else's tour was touched.
    expect(sql(`SELECT COALESCE(tour_completed_at::text, 'never') FROM users WHERE email = 'owner@e2e.test'`)).toBe(ownersBefore);
  });
});
