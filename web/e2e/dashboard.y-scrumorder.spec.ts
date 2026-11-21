import type { Locator, Page } from '@playwright/test';
import { Api } from './support/api';
import { PEOPLE, readState, TASKS } from './support/people';
import { expect, openTasks, test } from './support/fixtures';

// Asked for: the scrum reads like the team's spreadsheet, one row per person
// with their lead and their tasks across, and whoever looks at it puts people and
// tasks in their own order, by mouse, keyboard or finger. The order is kept on the
// server, is theirs alone, and a completed scrum keeps it.
//
// Runs after dashboard.spec.ts, on the same seeded organization. Mia manages
// Backend (Alice, Ann) with Platform (Pat) beneath it.

const table = (page: Page) => page.getByTestId('scrum-table');
const names = (page: Page) => table(page).getByTestId('scrum-person-name').allTextContents();
const row = (page: Page, name: string): Locator =>
  table(page).locator('tr', {
    has: page.getByTestId('scrum-person-name').getByText(name, { exact: true }),
  });
const taskTitles = (r: Locator) => r.locator('td[data-task] a[href^="/tasks/"]').allTextContents();

async function apiAs(who: keyof typeof PEOPLE) {
  return Api.signIn(PEOPLE[who].email, PEOPLE[who].password);
}

/** Waits for the move to be saved, so a reload reads what the server has. */
const saved = (page: Page, path: RegExp) =>
  page.waitForResponse((r) => r.request().method() === 'PUT' && path.test(new URL(r.url()).pathname) && r.ok());

async function centre(l: Locator) {
  const b = await l.boundingBox();
  if (!b) throw new Error('not on screen');
  return { x: b.x + b.width / 2, y: b.y + b.height / 2 };
}

/** A mouse drag in small steps, as a hand does it. */
async function drag(page: Page, from: Locator, to: Locator, { above = false } = {}) {
  const a = await centre(from);
  const b = await centre(to);
  await page.mouse.move(a.x, a.y);
  await page.mouse.down();
  const y = b.y + (above ? -8 : 8);
  for (let i = 1; i <= 12; i++) await page.mouse.move(a.x, a.y + ((y - a.y) * i) / 12);
  await page.mouse.up();
}

/** A finger, through Chromium's own touch input, so touch-action applies as on a phone. */
async function swipe(page: Page, from: { x: number; y: number }, to: { x: number; y: number }) {
  const cdp = await page.context().newCDPSession(page);
  const point = (p: { x: number; y: number }) => [{ x: Math.round(p.x), y: Math.round(p.y), id: 1 }];
  await cdp.send('Input.dispatchTouchEvent', {
    type: 'touchStart',
    touchPoints: point(from),
  });
  for (let i = 1; i <= 12; i++) {
    const at = {
      x: from.x + ((to.x - from.x) * i) / 12,
      y: from.y + ((to.y - from.y) * i) / 12,
    };
    await cdp.send('Input.dispatchTouchEvent', {
      type: 'touchMove',
      touchPoints: point(at),
    });
  }
  await cdp.send('Input.dispatchTouchEvent', {
    type: 'touchEnd',
    touchPoints: [],
  });
  await cdp.detach();
}

test.describe('the scrum table', () => {
  // Leave Mia's table as nobody arranged it, for the specs after this one.
  test.afterAll(async () => {
    const mia = await apiAs('mia');
    await mia.put('/scrums/order', { people: [] });
    const ids = readState().ids;
    for (const who of ['alice', 'ann', 'pat'] as const) await mia.put(`/scrums/order/tasks/${ids[who]}`, { tasks: [] });
    await mia.dispose();
  });

  test('reads like the sheet: name, lead, tasks across with dates and status, and done', async ({ as }) => {
    const { page } = await as('mia');
    await openTasks(page, '?tab=scrum');
    const headers = await table(page).locator('thead th').allTextContents();
    expect(headers.slice(0, 4)).toEqual(['Move', 'Name', 'My Lead', 'Task 1']);
    expect(headers).toContain('Done');

    // As many task columns as the busiest person has open work.
    const rows = table(page).locator('tbody tr');
    let busiest = 0;
    for (let i = 0; i < (await rows.count()); i++)
      busiest = Math.max(busiest, await rows.nth(i).locator('td[data-task]').count());
    await expect(table(page).locator('thead th', { hasText: /^Task \d+$/ })).toHaveCount(Math.max(busiest, 1));

    const alice = row(page, PEOPLE.alice.name);
    await expect(alice.getByTestId('scrum-leads')).toHaveText(PEOPLE.mia.name);
    // Platform has no manager of its own.
    await expect(row(page, PEOPLE.pat.name).getByTestId('scrum-leads')).toHaveText('No lead');
    const blocked = alice.locator('td[data-task]', {
      hasText: TASKS.aliceBlocked,
    });
    await expect(blocked).toContainText('Blocked');
    await expect(blocked.getByText('Blocked', { exact: true })).toHaveClass(/text-red-600/);
    await expect(
      row(page, PEOPLE.pat.name).locator('td[data-task]', {
        hasText: TASKS.patCerts,
      })
    ).toContainText(/Overdue/);
    await expect(row(page, PEOPLE.ann.name)).toContainText('No open tasks');

    // A cell opens its task.
    await alice.getByRole('link', { name: TASKS.aliceBlocked }).click();
    await expect(page).toHaveURL(/\/tasks\/[0-9a-f-]{36}$/);
  });

  test('a person is dragged to the top with the mouse, and stays there after a reload', async ({ as }) => {
    const { page } = await as('mia');
    await openTasks(page, '?tab=scrum');
    const before = await names(page);
    expect(before.length).toBeGreaterThanOrEqual(3);
    const last = before[before.length - 1];

    const done = saved(page, /^\/api\/scrums\/order$/);
    await drag(page, page.getByRole('button', { name: `Move ${last}`, exact: true }), row(page, before[0]), {
      above: true,
    });
    await done;
    const after = [last, ...before.slice(0, -1)];
    await expect.poll(() => names(page)).toEqual(after);

    await page.reload();
    await expect.poll(() => names(page)).toEqual(after);

    // Nobody else sees it: the owner has an order of their own, and it is empty.
    const owner = await apiAs('owner');
    expect((await owner.get('/scrums/order')).people).toEqual([]);
    await owner.dispose();
  });

  test('a task is moved along its row from the keyboard, and the move is kept', async ({ as }) => {
    const { page } = await as('mia');
    await openTasks(page, '?tab=scrum');
    const alice = row(page, PEOPLE.alice.name);
    const before = await taskTitles(alice);
    expect(before.length).toBeGreaterThanOrEqual(2);

    const done = saved(page, /^\/api\/scrums\/order\/tasks\//);
    await alice.getByRole('button', { name: `Move ${before[0]}`, exact: true }).focus();
    // What a screen reader hears says where it is at each step.
    const heard = page.locator('[id^="DndLiveRegion"]');
    await page.keyboard.press('Space');
    await expect(
      heard.filter({
        hasText: `${before[0]} is over ${PEOPLE.alice.name}'s task 1 of`,
      })
    ).toHaveCount(1);
    await page.keyboard.press('ArrowRight');
    await expect(heard.filter({ hasText: `is over ${PEOPLE.alice.name}'s task 2 of` })).toHaveCount(1);
    await page.keyboard.press('Space');
    await done;
    const after = [before[1], before[0], ...before.slice(2)];
    await expect.poll(() => taskTitles(alice)).toEqual(after);

    await page.reload();
    await expect.poll(() => taskTitles(row(page, PEOPLE.alice.name))).toEqual(after);
    await expect(row(page, PEOPLE.alice.name).locator('td[data-task]').first()).toContainText(before[1]);
  });

  test('on a phone the names stay while the tasks scroll, and a finger drags a row', async ({ as }) => {
    const { page } = await as('mia', {
      phone: 'android',
      width: 375,
      height: 812,
    });
    await openTasks(page, '?tab=scrum');
    await expect(table(page)).toBeVisible();
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth
    );
    expect(overflow, 'the page itself must not scroll sideways').toBeLessThanOrEqual(1);

    // My Lead folds under the name, and Task 1 takes most of the width with Task 2 peeking in.
    await expect(table(page).getByRole('columnheader', { name: 'My Lead' })).toBeHidden();
    await expect(row(page, PEOPLE.alice.name).getByTestId('scrum-leads-inline')).toHaveText(`Lead: ${PEOPLE.mia.name}`);
    const task1 = (await table(page).getByRole('columnheader', { name: 'Task 1', exact: true }).boundingBox())!;
    expect(task1.width, 'Task 1 is about three quarters of what the names leave').toBeGreaterThan(140);
    expect(task1.x + task1.width, 'Task 1 is whole on screen').toBeLessThanOrEqual(375);
    const task2 = await table(page).getByRole('columnheader', { name: 'Task 2', exact: true }).boundingBox();
    if (task2) expect(task2.x, 'Task 2 peeks in').toBeLessThan(375);

    const before = await names(page);
    const nameCell = table(page).getByTestId('scrum-person-name').first();
    const x = (await nameCell.boundingBox())!.x;
    await table(page).evaluate((el) => (el.scrollLeft = 600));
    expect((await nameCell.boundingBox())!.x, 'the name column is sticky').toBeCloseTo(x, 0);
    await table(page).evaluate((el) => (el.scrollLeft = 0));

    const second = before[1];
    const done = saved(page, /^\/api\/scrums\/order$/);
    const from = await centre(page.getByRole('button', { name: `Move ${second}`, exact: true }));
    const to = await centre(row(page, before[0]));
    await swipe(page, from, { x: from.x, y: to.y - 10 });
    await done;
    const after = [second, before[0], ...before.slice(2)];
    await expect.poll(() => names(page)).toEqual(after);
    await page.reload();
    await expect.poll(() => names(page)).toEqual(after);
  });

  test('a completed scrum keeps the order and the leads, and History shows the same table', async ({ as }) => {
    const { page } = await as('mia');
    await openTasks(page, '?tab=scrum');
    const people = await names(page);
    const aliceTasks = await taskTitles(row(page, PEOPLE.alice.name));

    await page.getByRole('button', { name: 'Complete scrum' }).first().click();
    const dialog = page.getByRole('dialog');
    await dialog.getByRole('button', { name: 'Complete scrum' }).click();
    await expect(page.getByText('Scrum saved', { exact: true })).toBeVisible();

    await page.getByRole('tab', { name: 'History' }).click();
    await page
      .getByRole('link', { name: /held by Mia Manager/ })
      .first()
      .click();
    await expect(page.getByText('Saved, read-only')).toBeVisible();
    await expect.poll(() => names(page)).toEqual(people);
    expect(await taskTitles(row(page, PEOPLE.alice.name))).toEqual(aliceTasks);
    await expect(row(page, PEOPLE.alice.name).getByTestId('scrum-leads')).toHaveText(PEOPLE.mia.name);
    // Read-only: nothing to drag.
    await expect(table(page).getByRole('button', { name: /^Move / })).toHaveCount(0);
  });
});
