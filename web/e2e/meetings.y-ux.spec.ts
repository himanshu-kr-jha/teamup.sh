import type { Page } from '@playwright/test';
import { expect, openTasks, test } from './support/fixtures';
import { apiAs, idOf } from './support/meetings';

// A pass over the app as somebody running it from a laptop and from a phone.

/** A task's status as the server has it. */
async function statusOf(id: string) {
  const alice = await apiAs('alice');
  const task = await alice.get(`/tasks/${id}`);
  await alice.dispose();
  return task.status as string;
}

/** The card for a title on whichever board is on screen. */
const card = (page: Page, title: string) =>
  page.locator('[aria-roledescription="draggable"]').filter({ hasText: title, visible: true }).first();

test.describe('dragging a task to another column changes its status', () => {
  const stamp = Date.now();
  const made: string[] = [];
  const make = async (title: string) => {
    const alice = await apiAs('alice');
    const id = (await alice.post('/tasks', { title, assigneeId: idOf('alice'), status: 'todo' })).id as string;
    await alice.dispose();
    made.push(id);
    return id;
  };

  test.afterAll(async () => {
    const owner = await apiAs('owner');
    for (const id of made) await owner.del(`/tasks/${id}`).catch(() => {});
    await owner.dispose();
  });

  test('with a mouse, from To Do to In Review, and a click still opens the task', async ({ as }) => {
    const title = `Dragged by mouse ${stamp}`;
    const id = await make(title);
    const { page } = await as('alice');
    await openTasks(page, '?tab=board');

    const from = await card(page, title).boundingBox();
    const to = await page.getByRole('region', { name: 'In Review' }).boundingBox();
    if (!from || !to) throw new Error('board not drawn');
    const saved = page.waitForResponse((r) => r.request().method() === 'PATCH' && r.url().endsWith(`/api/tasks/${id}`));
    await page.mouse.move(from.x + 40, from.y + 20);
    await page.mouse.down();
    await page.mouse.move(from.x + 60, from.y + 30, { steps: 4 });
    // While it is held, the column under it says so.
    await page.mouse.move(to.x + to.width / 2, to.y + 120, { steps: 12 });
    await expect(page.getByRole('region', { name: 'In Review' })).toHaveAttribute('data-drop-over', '');
    await page.mouse.up();

    expect((await saved).ok()).toBe(true);
    await expect(page.getByRole('region', { name: 'In Review' }).getByText(title)).toBeVisible();
    expect(await statusOf(id)).toBe('in_review');

    // Picking a card up needs a few pixels of travel, so a plain click is a click.
    await page.getByRole('link', { name: new RegExp(title) }).filter({ visible: true }).click();
    await expect(page).toHaveURL(new RegExp(`/tasks/${id}$`));
  });

  test('with the keyboard, a column per arrow press', async ({ as }) => {
    const title = `Dragged by keyboard ${stamp}`;
    const id = await make(title);
    const { page } = await as('alice');
    await openTasks(page, '?tab=board');

    await card(page, title).focus();
    await page.keyboard.press('Space');
    // What a screen reader hears, from here on.
    await expect(page.locator('[id^="DndLiveRegion"]')).toContainText(title);
    await page.keyboard.press('ArrowRight');
    await expect(page.getByRole('region', { name: 'In Progress' })).toHaveAttribute('data-drop-over', '');
    const saved = page.waitForResponse((r) => r.request().method() === 'PATCH' && r.url().endsWith(`/api/tasks/${id}`));
    await page.keyboard.press('Space');
    expect((await saved).ok()).toBe(true);
    expect(await statusOf(id)).toBe('in_progress');
  });

  test('on a phone, held down and dropped on a column chip', async ({ as }) => {
    const title = `Dragged on a phone ${stamp}`;
    const id = await make(title);
    const { page } = await as('alice', { phone: 'android' });
    await openTasks(page, '?tab=board');

    const from = await card(page, title).boundingBox();
    if (!from) throw new Error('card not drawn');
    const cdp = await page.context().newCDPSession(page);
    const touch = (type: 'touchStart' | 'touchMove' | 'touchEnd', x = 0, y = 0) =>
      cdp.send('Input.dispatchTouchEvent', { type, touchPoints: type === 'touchEnd' ? [] : [{ x, y, id: 1 }] });
    const x0 = from.x + 30;
    const y0 = from.y + 20;

    // A finger that moves at once is scrolling, not dragging.
    await touch('touchStart', x0, y0);
    await page.waitForTimeout(350);
    await touch('touchMove', x0 + 3, y0 + 3);
    // Picked up once the finger has rested long enough, which on a busy machine
    // is not the moment the pause above ends.
    await expect(page.locator('[id^="DndLiveRegion"]')).toContainText(`Picked up ${title}`, { timeout: 10_000 });
    // Held, the chips wrap so every column is in reach at once.
    const blocked = page.getByRole('button', { name: /^Blocked/ }).filter({ visible: true });
    await expect(blocked).toBeInViewport({ ratio: 1, timeout: 10_000 });
    await expect(page.getByRole('button', { name: /^Done/ }).filter({ visible: true })).toBeInViewport({ ratio: 1 });
    const chip = await blocked.boundingBox();
    if (!chip) throw new Error('no Blocked chip');
    for (let i = 1; i <= 10; i++) {
      await touch('touchMove', x0 + ((chip.x + chip.width / 2 - x0) * i) / 10, y0 + ((chip.y + chip.height / 2 - y0) * i) / 10);
    }
    await expect(blocked).toHaveAttribute('data-drop-over', '');
    const saved = page.waitForResponse((r) => r.request().method() === 'PATCH' && r.url().endsWith(`/api/tasks/${id}`));
    await touch('touchEnd');
    expect((await saved).ok()).toBe(true);
    await expect(page.getByText('Moved to Blocked.', { exact: true })).toBeVisible();
    expect(await statusOf(id)).toBe('blocked');
  });
});

test.describe('the organization on a phone', () => {
  test('every team action is on screen, the chosen tab is in view, and deleting asks first', async ({ as }) => {
    const owner = await apiAs('owner');
    const name = `Tiny team ${Date.now()}`;
    await owner.post('/teams', { name });
    await owner.dispose();

    const { page } = await as('owner', { phone: 'android' });
    await page.goto('/organization?tab=teams');
    await expect(page.getByRole('tab', { name: 'Teams' })).toBeInViewport({ ratio: 1 });
    // Nothing on the page reaches past the screen's edge.
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
    const row = page.locator('div.rounded-lg', { has: page.getByText(name, { exact: true }) }).last();
    await row.scrollIntoViewIfNeeded();
    for (const label of ['Add member', 'Members']) {
      await expect(row.getByRole('button', { name: label, exact: true })).toBeInViewport({ ratio: 1 });
    }

    await row.getByRole('button', { name: `Delete the team ${name}` }).click();
    await expect(page.getByRole('dialog', { name: `Delete ${name}?` })).toBeVisible();
    await page.getByRole('button', { name: 'Cancel' }).click();
    await expect(page.getByText(name, { exact: true })).toBeVisible();

    await row.getByRole('button', { name: `Delete the team ${name}` }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Delete team' }).click();
    await expect(page.getByText(name, { exact: true })).toHaveCount(0);
  });

  test('the calendar names the time zone in words', async ({ as }) => {
    const { page } = await as('alice');
    await page.goto('/calendar');
    await expect(page.getByText(/^Times in .+ \(GMT[+-]?[\d:]*\)$|^Times in GMT/)).toBeVisible();
    await expect(page.getByText(/Times in \w+\/\w+/)).toHaveCount(0);
  });

  test('search is a tap away in the header', async ({ as }) => {
    const { page } = await as('alice', { phone: 'iphone' });
    await page.goto('/chat');
    await page.getByRole('button', { name: 'Search messages and files' }).click();
    await expect(page.getByRole('searchbox', { name: 'Search messages and files' })).toBeFocused();
    await page.getByRole('button', { name: 'Close search' }).click();
    await expect(page.getByRole('searchbox', { name: 'Search messages and files' })).toBeHidden();
  });
});
