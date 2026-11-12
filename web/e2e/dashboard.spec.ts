import type { Locator, Page } from '@playwright/test';
import { Api } from './support/api';
import { PEOPLE, readState, TASKS } from './support/people';
import { createTaskInUi, expect, localDay, openTasks, signIn, tabNames, test, tileCounts } from './support/fixtures';

// One seeded organization, changed by some of these tests, so they run in file
// order (one worker). A failure does not stop the ones after it: each says what
// it needs from the earlier ones in its name.

const SIX_TABS = ['Overview', 'By person', 'Scrum', 'History', 'Contribution', 'Board'];

/** One person's card on the scrum tab (or in a saved scrum). */
const scrumCard = (page: Page, name: string): Locator =>
  page.locator('section', { has: page.getByRole('heading', { level: 2, name, exact: true }) });

/** A person's row in the Overview table. */
const overviewRow = (page: Page, name: string): Locator => page.getByRole('row', { name: new RegExp(`^${name}`) });

async function apiAs(who: keyof typeof PEOPLE) {
  return Api.signIn(PEOPLE[who].email, PEOPLE[who].password);
}

async function pickOption(page: Page, trigger: Locator, option: string) {
  await trigger.click();
  await page.getByRole('option', { name: option, exact: true }).click();
}

test.describe('1. employee sees only their own work', () => {
  test('Alice: two tabs, her tasks, nobody else', async ({ as }) => {
    const { page } = await as('alice');
    await openTasks(page);

    await expect(tabNames(page)).toHaveText(['Overview', 'Board']);
    await expect(page.getByRole('tab', { name: 'Overview' })).toHaveAttribute('aria-selected', 'true');

    // Seeded for Alice: two todo, one in progress, one blocked.
    await expect(page.getByRole('link', { name: TASKS.aliceSpec })).toBeVisible();
    expect(await tileCounts(page)).toEqual({ pending: 2, ongoing: 1, blocked: 1, overdue: 0, completed: 0 });
    for (const title of [TASKS.aliceReport, TASKS.aliceApi, TASKS.aliceBlocked]) {
      await expect(page.getByRole('link', { name: title })).toBeVisible();
    }
    for (const stranger of [TASKS.patPipeline, TASKS.patLogs, TASKS.samCalls, TASKS.samQuote]) {
      await expect(page.getByText(stranger)).toHaveCount(0);
    }
    for (const name of [PEOPLE.pat.name, PEOPLE.sam.name, PEOPLE.ann.name]) {
      await expect(page.getByText(name)).toHaveCount(0);
    }
    // Nothing to narrow, so no team filter is drawn.
    await expect(page.getByLabel('Filter by team')).toHaveCount(0);
  });

  test('Alice: a deep link to the scrum tab falls back to Overview', async ({ as }) => {
    const { page } = await as('alice');
    await page.goto('/tasks?tab=scrum');
    await expect(tabNames(page)).toHaveText(['Overview', 'Board']);
    await expect(page.getByRole('tab', { name: 'Overview' })).toHaveAttribute('aria-selected', 'true');
    await expect(page.getByRole('button', { name: 'Complete scrum' })).toHaveCount(0);
    for (const tab of ['person', 'history', 'contribution']) {
      await page.goto(`/tasks?tab=${tab}`);
      await expect(page.getByRole('tab', { name: 'Overview' })).toHaveAttribute('aria-selected', 'true');
    }
  });

  test('Alice: asking the API for somebody else\'s work shows nothing of theirs', async ({ as }) => {
    await as('alice');
    const { teams, ids } = readState();
    const alice = await apiAs('alice');
    try {
      for (const teamId of [teams.backend, teams.platform, teams.sales]) {
        const dash = await alice.get(`/tasks/dashboard?teamId=${teamId}`);
        expect(dash.scope, `dashboard scope for teamId=${teamId}`).toBe('self');
        expect(dash.people.map((p: any) => p.name)).toEqual([PEOPLE.alice.name]);
        const json = JSON.stringify(dash);
        for (const stranger of [TASKS.patPipeline, TASKS.samCalls, PEOPLE.sam.name, PEOPLE.pat.name]) {
          expect(json, `dashboard?teamId=${teamId} leaked ${stranger}`).not.toContain(stranger);
        }
        const contributions = await alice.get(`/tasks/contributions?teamId=${teamId}`);
        expect(contributions.people.map((p: any) => p.name)).toEqual([PEOPLE.alice.name]);
      }
      // Another person's contribution detail is not hers to read, and does not admit it exists.
      expect(await alice.status('get', `/tasks/contributions/${ids.sam}`)).toBe(404);
      expect(await alice.status('get', `/tasks/contributions/${ids.pat}`)).toBe(404);
      // A scrum is not hers to save, and none are hers to read.
      expect([403, 404]).toContain(await alice.status('post', '/scrums', { teamId: teams.backend }));
      expect([403, 404]).toContain(await alice.status('post', '/scrums', {}));
      const scrums = await alice.get('/scrums');
      expect(scrums.scrums ?? scrums.data ?? scrums).toEqual([]);
      // The board's "mine" scope is hers alone.
      const mine = await alice.get('/tasks?scope=mine');
      const titles = (mine.tasks ?? mine.data ?? mine).map((t: any) => t.title);
      expect(titles.sort()).toEqual(
        [TASKS.aliceSpec, TASKS.aliceReport, TASKS.aliceApi, TASKS.aliceBlocked].sort()
      );
    } finally {
      await alice.dispose();
    }
  });
});

test.describe('2. manager sees the people on their teams', () => {
  test('Mia: six tabs; Alice, Ann and Pat but not Sam', async ({ as }) => {
    const { page } = await as('mia');
    await openTasks(page);
    await expect(tabNames(page)).toHaveText(SIX_TABS);

    await expect(overviewRow(page, 'Alice Archer')).toBeVisible();
    await expect(overviewRow(page, 'Ann Idle')).toBeVisible();
    await expect(overviewRow(page, 'Pat Platform')).toBeVisible();
    await expect(page.getByText(PEOPLE.sam.name)).toHaveCount(0);
    await expect(page.getByText(PEOPLE.max.name)).toHaveCount(0);
    // Her own work is not on her dashboard: she is not on a team she manages.
    await expect(overviewRow(page, 'Mia Manager')).toHaveCount(0);

    // Row cells: Pending Ongoing Blocked Overdue Added to Completed
    await expect(overviewRow(page, 'Alice Archer').getByRole('cell')).toHaveText([/Alice Archer$/, '2', '1', '1', '0', '0', '0']);
    await expect(overviewRow(page, 'Ann Idle').getByRole('cell')).toHaveText([/Ann Idle$/, '0', '0', '0', '0', '0', '0']);
    await expect(overviewRow(page, 'Pat Platform').getByRole('cell')).toHaveText([/Pat Platform$/, '2', '1', '0', '1', '0', '0']);
    // Totals across the three of them.
    expect(await tileCounts(page)).toEqual({ pending: 4, ongoing: 2, blocked: 1, overdue: 1, completed: 0 });
  });

  test('Mia: the team filter includes the nested team and narrows to it', async ({ as }) => {
    const { page } = await as('mia');
    await openTasks(page);
    const filter = page.getByLabel('Filter by team');
    await filter.click();
    await expect(page.getByRole('option')).toHaveText(['All teams', 'Backend', 'Platform']);
    await page.getByRole('option', { name: 'Platform' }).click();
    await expect(overviewRow(page, 'Pat Platform')).toBeVisible();
    await expect(overviewRow(page, 'Alice Archer')).toHaveCount(0);
    await expect(overviewRow(page, 'Ann Idle')).toHaveCount(0);
  });

  test('Mia: By person shows one person only', async ({ as }) => {
    const { page } = await as('mia');
    await openTasks(page, '?tab=person');
    await expect(page.locator('p', { hasText: 'Pick a person' })).toBeVisible();

    await pickOption(page, page.getByRole('combobox', { name: 'Person' }), 'Alice Archer');
    expect(await tileCounts(page)).toEqual({ pending: 2, ongoing: 1, blocked: 1, overdue: 0, completed: 0 });
    for (const title of [TASKS.aliceSpec, TASKS.aliceReport, TASKS.aliceApi, TASKS.aliceBlocked]) {
      await expect(page.getByRole('link', { name: title })).toBeVisible();
    }
    for (const other of [TASKS.patPipeline, TASKS.patCerts, TASKS.samCalls]) {
      await expect(page.getByText(other)).toHaveCount(0);
    }

    // Switching to Ann, who has nothing.
    await pickOption(page, page.getByRole('combobox', { name: 'Person' }), 'Ann Idle');
    await expect(page.getByText('Ann Idle has no tasks')).toBeVisible();
    await expect(page.getByText(TASKS.aliceSpec)).toHaveCount(0);

    // Pat's overdue certificate is flagged.
    await pickOption(page, page.getByRole('combobox', { name: 'Person' }), 'Pat Platform');
    // An overdue task is listed under its status and again under Overdue.
    await expect(page.getByRole('link', { name: TASKS.patCerts }).first()).toBeVisible();
    expect((await tileCounts(page)).overdue).toBe(1);
  });

  test('Mia: Scrum shows everyone, Ann with no work included', async ({ as }) => {
    const { page } = await as('mia');
    await openTasks(page, '?tab=scrum');
    await expect(scrumCard(page, 'Alice Archer')).toBeVisible();
    await expect(scrumCard(page, 'Pat Platform')).toBeVisible();
    await expect(scrumCard(page, 'Ann Idle')).toBeVisible();
    await expect(scrumCard(page, 'Ann Idle')).toContainText('No tasks');
    await expect(scrumCard(page, 'Sam Sales')).toHaveCount(0);
    await expect(scrumCard(page, 'Alice Archer')).toContainText(TASKS.aliceBlocked);
    await expect(scrumCard(page, 'Pat Platform')).toContainText('1 overdue');
    await expect(page.getByText('No scrum saved yet, so Done covers the last 7 days.')).toBeVisible();
  });
});

test.describe('3. the scrum summary is live', () => {
  test('a task Alice creates shows on Mia\'s open scrum, with a New badge, without Mia doing anything', async ({ as }) => {
    const mia = await as('mia');
    const alice = await as('alice');
    await openTasks(mia.page, '?tab=scrum');
    await expect(scrumCard(mia.page, 'Alice Archer')).toBeVisible();

    const title = `Alice live task ${Date.now()}`;
    await openTasks(alice.page);
    await createTaskInUi(alice.page, { title, assignee: PEOPLE.alice.name });
    await expect(alice.page.getByRole('link', { name: title })).toBeVisible();

    // The 30 second poll, or a task notification, whichever comes first.
    const row = scrumCard(mia.page, 'Alice Archer').locator('li', { hasText: title });
    await expect(row).toBeVisible({ timeout: 45_000 });
    await expect(row.getByText('New', { exact: true })).toBeVisible();
  });

  test('returning to the window refreshes the scrum straight away', async ({ as }) => {
    const mia = await as('mia');
    await openTasks(mia.page, '?tab=scrum');
    await expect(scrumCard(mia.page, 'Pat Platform')).toBeVisible();

    const title = `Pat focus task ${Date.now()}`;
    const owner = await apiAs('owner');
    await owner.post('/tasks', { title, assigneeId: readState().ids.pat, status: 'in_progress' });
    await owner.dispose();

    // What the browser does when the tab comes back to the front.
    await mia.page.evaluate(() => {
      Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true });
      document.dispatchEvent(new Event('visibilitychange', { bubbles: true }));
    });
    await expect(scrumCard(mia.page, 'Pat Platform').locator('li', { hasText: title })).toBeVisible({ timeout: 5_000 });
  });
});

test.describe('5a and 4. finishing work, completing a scrum, and what is frozen', () => {
  test('Alice marks a task done from the board; Mia\'s scrum counts it', async ({ as }) => {
    const alice = await as('alice');
    const mia = await as('mia');
    await openTasks(alice.page, '?tab=board');
    const card = alice.page.locator('div.rounded-lg', { has: alice.page.getByRole('link', { name: TASKS.aliceReport }) }).last();
    await card.getByRole('button', { name: 'Task actions' }).click();
    await alice.page.getByRole('menuitem', { name: 'Mark Done' }).click();

    await openTasks(alice.page);
    await expect(alice.page.getByRole('heading', { name: /^Completed/ })).toBeVisible();
    expect((await tileCounts(alice.page)).completed).toBe(1);
    // An employee's completed work is the last 7 days, whatever scrums exist.
    await expect(alice.page.getByText(/last 7 days/i).first()).toBeVisible();

    await openTasks(mia.page, '?tab=scrum');
    const aliceCard = scrumCard(mia.page, 'Alice Archer');
    await expect(aliceCard).toContainText(TASKS.aliceReport);
    await expect(aliceCard.getByRole('heading', { level: 3, name: /^Done/ })).toBeVisible();
    await expect(mia.page.getByText(/^1 done/)).toBeVisible();
  });

  test('Mia completes a scrum with notes; it is in History; edits afterwards do not touch it', async ({ as }) => {
    const mia = await as('mia');
    const notes = `Standup notes ${Date.now()}`;
    await openTasks(mia.page, '?tab=scrum');
    await expect(mia.page.getByText(/^1 done/)).toBeVisible();

    await mia.page.getByRole('button', { name: 'Complete scrum' }).first().click();
    const dialog = mia.page.getByRole('dialog');
    await dialog.getByLabel('Notes (optional)').fill(notes);
    await dialog.getByRole('button', { name: 'Complete scrum' }).click();
    await expect(mia.page.getByText('Scrum saved', { exact: true })).toBeVisible();
    await expect(dialog).toBeHidden();

    // Done since last scrum resets, and the New badges clear.
    await expect(mia.page.getByText(/^0 done/)).toBeVisible();
    await expect(mia.page.getByText(/Done since the last scrum, /)).toBeVisible();
    await expect(mia.page.getByText('New', { exact: true })).toHaveCount(0);
    await expect(scrumCard(mia.page, 'Alice Archer')).not.toContainText(TASKS.aliceReport);

    // History lists it.
    await mia.page.getByRole('tab', { name: 'History' }).click();
    const entry = mia.page.getByRole('link', { name: /held by Mia Manager/ });
    await expect(entry).toHaveCount(1);
    await expect(entry).toContainText('3 people');
    await entry.click();
    await expect(mia.page.getByText(notes)).toBeVisible();
    await expect(mia.page.getByText('Saved, read-only')).toBeVisible();
    await expect(scrumCard(mia.page, 'Alice Archer')).toContainText(TASKS.aliceReport);
    await expect(scrumCard(mia.page, 'Alice Archer')).toContainText(TASKS.aliceApi);
    await expect(scrumCard(mia.page, 'Pat Platform')).toContainText(TASKS.patLogs);
    await expect(scrumCard(mia.page, 'Ann Idle')).toContainText('No tasks');
    const savedUrl = mia.page.url();

    // Rename one task and delete another, as the people who own them.
    const alice = await apiAs('alice');
    const owner = await apiAs('owner');
    const tasks = (await owner.get('/tasks')) as any;
    const all: any[] = tasks.tasks ?? tasks.data ?? tasks;
    const api = all.find((t) => t.title === TASKS.aliceApi);
    const logs = all.find((t) => t.title === TASKS.patLogs);
    await alice.patch(`/tasks/${api.id}`, { title: 'Alice build API (renamed)' });
    await owner.del(`/tasks/${logs.id}`);
    await alice.dispose();
    await owner.dispose();

    // The saved scrum still says what it said.
    await mia.page.goto(savedUrl);
    await expect(scrumCard(mia.page, 'Alice Archer')).toContainText(TASKS.aliceApi);
    await expect(scrumCard(mia.page, 'Alice Archer')).not.toContainText('(renamed)');
    await expect(scrumCard(mia.page, 'Pat Platform')).toContainText(TASKS.patLogs);

    // The live one has moved on.
    await openTasks(mia.page, '?tab=scrum');
    await expect(scrumCard(mia.page, 'Alice Archer')).toContainText('Alice build API (renamed)');
    await expect(scrumCard(mia.page, 'Pat Platform')).not.toContainText(TASKS.patLogs);
    await expect(mia.page.getByText('New', { exact: true })).toHaveCount(0);
  });

  test('after a scrum only work created since is New', async ({ as }) => {
    const mia = await as('mia');
    const alice = await as('alice');
    await openTasks(mia.page, '?tab=scrum');
    await expect(scrumCard(mia.page, 'Alice Archer')).toBeVisible();

    const title = `Alice after scrum ${Date.now()}`;
    await openTasks(alice.page);
    await createTaskInUi(alice.page, { title, assignee: PEOPLE.alice.name });

    const fresh = scrumCard(mia.page, 'Alice Archer').locator('li', { hasText: title });
    await expect(fresh).toBeVisible({ timeout: 45_000 });
    await expect(fresh.getByText('New', { exact: true })).toBeVisible();
    await expect(mia.page.getByText('New', { exact: true })).toHaveCount(1);
  });

  test('the scrum cannot be edited or deleted over the API', async ({ as }) => {
    await as('mia');
    const mia = await apiAs('mia');
    try {
      const list = await mia.get('/scrums');
      const scrums: any[] = list.scrums ?? list.data ?? list;
      expect(scrums.length).toBe(1);
      for (const method of ['patch', 'delete'] as const) {
        expect([404, 405], `${method} on a saved scrum`).toContain(await mia.status(method, `/scrums/${scrums[0].id}`, {}));
      }
    } finally {
      await mia.dispose();
    }
  });
});

test.describe('5b. contribution', () => {
  test('Alice\'s completed count and drill-down, and the date range', async ({ as }) => {
    const { page } = await as('mia');
    await openTasks(page, '?tab=contribution');
    const row = page.getByRole('row', { name: /^Alice Archer/ });
    // Completed, Involved, Added to, Open. One finished; she is also involved in everything that was live.
    await expect(row.getByRole('cell').nth(1)).toHaveText('1');
    const involved = Number(await row.getByRole('cell').nth(2).innerText());
    const open = Number(await row.getByRole('cell').nth(4).innerText());
    expect(involved).toBeGreaterThanOrEqual(1);
    expect(open).toBeGreaterThanOrEqual(3);
    await expect(page.getByRole('row', { name: /^Ann Idle/ }).getByRole('cell')).toHaveText([/Ann Idle$/, '0', '0', '0', '0']);

    await row.getByRole('button', { name: 'Alice Archer' }).click();
    const dialog = page.getByRole('dialog');
    const completed = dialog.locator('section', { has: page.getByRole('heading', { name: /^Completed/ }) });
    await expect(completed).toContainText(TASKS.aliceReport);
    await expect(completed.getByRole('heading')).toContainText('1');
    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();

    // A range that ended before any of this was created.
    await page.getByLabel('From', { exact: true }).fill(localDay(-20));
    await page.getByLabel('To', { exact: true }).fill(localDay(-10));
    await expect(row.getByRole('cell').nth(1)).toHaveText('0');
    await expect(row.getByRole('cell').nth(2)).toHaveText('0');

    // And one that includes today again.
    await page.getByLabel('To', { exact: true }).fill(localDay(0));
    await expect(row.getByRole('cell').nth(1)).toHaveText('1');
  });

  test('a range that ends before it starts is refused in words', async ({ as }) => {
    const { page } = await as('mia');
    await openTasks(page, `?tab=contribution&from=${localDay(0)}&to=${localDay(-5)}`);
    await expect(page.getByRole('alert')).toBeVisible();
  });
});

test.describe('6. owner sees the whole organization', () => {
  test('org view with the team filter', async ({ as }) => {
    const { page } = await as('owner');
    await openTasks(page);
    await expect(tabNames(page)).toHaveText(SIX_TABS);
    for (const name of ['Alice Archer', 'Ann Idle', 'Pat Platform', 'Sam Sales']) {
      await expect(overviewRow(page, name)).toBeVisible();
    }

    const filter = page.getByLabel('Filter by team');
    await filter.click();
    await expect(page.getByRole('option')).toHaveText(['All teams', 'Backend', 'Platform', 'Sales']);
    await page.keyboard.press('Escape');

    await pickOption(page, filter, 'Sales');
    await expect(overviewRow(page, 'Sam Sales')).toBeVisible();
    await expect(overviewRow(page, 'Alice Archer')).toHaveCount(0);
    await expect(overviewRow(page, 'Pat Platform')).toHaveCount(0);

    await pickOption(page, filter, 'Backend');
    for (const name of ['Alice Archer', 'Ann Idle', 'Pat Platform']) await expect(overviewRow(page, name)).toBeVisible();
    await expect(overviewRow(page, 'Sam Sales')).toHaveCount(0);

    await pickOption(page, filter, 'Platform');
    await expect(overviewRow(page, 'Pat Platform')).toBeVisible();
    await expect(overviewRow(page, 'Alice Archer')).toHaveCount(0);
  });

  test('org scope from the server, and the owner\'s scrum covers everyone', async ({ as }) => {
    await as('owner');
    const owner = await apiAs('owner');
    try {
      const dash = await owner.get('/tasks/dashboard');
      expect(dash.scope).toBe('org');
      const names = dash.people.map((p: any) => p.name);
      for (const name of ['Alice Archer', 'Ann Idle', 'Pat Platform', 'Sam Sales']) expect(names).toContain(name);
      // A team she does not belong to is still the owner's to ask for.
      const sales = await owner.get(`/tasks/dashboard?teamId=${readState().teams.sales}`);
      expect(sales.people.map((p: any) => p.name)).toEqual(['Sam Sales']);
    } finally {
      await owner.dispose();
    }
  });

  test('Mia cannot ask for Max\'s team', async ({ as }) => {
    await as('mia');
    const mia = await apiAs('mia');
    try {
      expect(await mia.status('get', `/tasks/dashboard?teamId=${readState().teams.sales}`)).toBe(404);
    } finally {
      await mia.dispose();
    }
  });
});

test.describe('7. board', () => {
  test('a Blocked task is in the Blocked column; list and board states work', async ({ as }) => {
    const { page } = await as('alice');
    await openTasks(page, '?tab=board');
    const blockedColumn = page.locator('div.w-72', { has: page.getByText(/^Blocked\s*\d*$/) });
    await expect(blockedColumn).toContainText(TASKS.aliceBlocked);
    await expect(blockedColumn.locator('span.tabular-nums').first()).toHaveText('1');
    const columnTitles = page.locator('div.w-72 > div:first-child');
    await expect(columnTitles).toHaveText([/To Do/, /In Progress/, /In Review/, /Blocked/, /Done/]);

    const layout = page.getByRole('group', { name: 'Layout' });
    const board = layout.getByRole('button', { name: 'board' });
    const list = layout.getByRole('button', { name: 'list' });
    await expect(board).toHaveAttribute('aria-pressed', 'true');
    await expect(list).toHaveAttribute('aria-pressed', 'false');

    await list.click();
    await expect(list).toHaveAttribute('aria-pressed', 'true');
    await expect(board).toHaveAttribute('aria-pressed', 'false');
    await expect(page.locator('div.w-72')).toHaveCount(0);
    await expect(page.getByRole('link', { name: TASKS.aliceBlocked })).toBeVisible();

    await board.click();
    await expect(board).toHaveAttribute('aria-pressed', 'true');
    await expect(page.locator('div.w-72')).toHaveCount(5);
  });

  test('the phone board has a Blocked pill', async ({ as }) => {
    const { page } = await as('alice', { width: 375, height: 812 });
    await openTasks(page, '?tab=board');
    await page.getByRole('button', { name: /^Blocked/ }).click();
    await expect(page.getByRole('link', { name: TASKS.aliceBlocked })).toBeVisible();
  });
});

test.describe('8. audit items that never met a real browser', () => {
  test('30: logging out lands on /login, stays signed out, and leaves nothing of the last person', async ({ as }) => {
    const { page, context } = await as('alice');
    await openTasks(page);
    await expect(page.getByRole('link', { name: TASKS.aliceSpec })).toBeVisible();

    await page.getByRole('button', { name: 'User menu' }).click();
    await page.getByRole('menuitem', { name: 'Log out' }).click();
    await expect(page).toHaveURL(/\/login/);

    // Reload and a direct visit: still signed out.
    await page.reload();
    await expect(page).toHaveURL(/\/login/);
    await page.goto('/tasks');
    await expect(page).toHaveURL(/\/login/);
    await expect(page.getByText(TASKS.aliceSpec)).toHaveCount(0);
    // Back does not resurrect the page.
    await page.goBack();
    await expect(page.getByText(TASKS.aliceSpec)).toHaveCount(0);
    expect((await context.cookies()).filter((c) => /token|session/i.test(c.name) && c.value)).toEqual([]);

    // No cached copy of her answers is left for the next person on this browser.
    const leftovers = await page.evaluate(async (needle) => {
      const found: string[] = [];
      for (const name of await caches.keys()) {
        const cache = await caches.open(name);
        for (const req of await cache.keys()) {
          const res = await cache.match(req);
          if (res && (await res.clone().text()).includes(needle)) found.push(`${name}: ${req.url}`);
        }
      }
      return found;
    }, TASKS.aliceSpec);
    expect(leftovers).toEqual([]);

    // The next person to sign in on this browser sees only their own.
    await page.getByLabel('Email').fill(PEOPLE.sam.email);
    await page.getByLabel('Password').fill(PEOPLE.sam.password);
    await page.getByRole('button', { name: /^sign in$/i }).click();
    // The last address asked for while signed out was /tasks, so that is where sign-in goes.
    await expect(page).toHaveURL(/\/tasks/);
    await openTasks(page);
    await expect(page.getByRole('link', { name: TASKS.samCalls })).toBeVisible();
    await expect(page.getByText(TASKS.aliceSpec)).toHaveCount(0);
    await expect(page.getByText('Alice Archer')).toHaveCount(0);
  });

  test('39: a failed chat send gives the composer its text back, and a retry works', async ({ as }) => {
    const { page } = await as('alice');
    const { channelId } = readState();
    await page.goto(`/chat/${channelId}`);
    const composer = page.getByLabel('Message', { exact: true });
    await expect(composer).toBeEnabled();

    const text = `will fail ${Date.now()}`;
    await page.route(`**/api/channels/${channelId}/messages`, (route) =>
      route.request().method() === 'POST'
        ? route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"boom"}' })
        : route.continue()
    );
    await composer.fill(text);
    await composer.press('Enter');
    await expect(page.getByText('Could not send that message.').or(page.getByText(/boom|try again/i)).first()).toBeVisible();
    await expect(composer).toHaveValue(text);
    await expect(page.getByText(text)).toHaveCount(0);

    await page.unroute(`**/api/channels/${channelId}/messages`);
    await composer.press('Enter');
    await expect(composer).toHaveValue('');
    await expect(page.getByText(text)).toBeVisible();
    await page.reload();
    await expect(page.getByText(text)).toBeVisible();
  });

  test('35: a hidden tab does not mark messages read; showing it does', async ({ as }) => {
    const pat = await as('pat');
    const alice = await as('alice');
    const { channelId } = readState();
    // The tab is hidden until the test says otherwise.
    await pat.context.addInitScript(() => {
      (window as any).__visible = false;
      Object.defineProperty(document, 'visibilityState', { get: () => ((window as any).__visible ? 'visible' : 'hidden'), configurable: true });
      Object.defineProperty(document, 'hidden', { get: () => !(window as any).__visible, configurable: true });
    });
    await pat.page.goto(`/chat/${channelId}`);
    await expect(pat.page.getByLabel('Message', { exact: true })).toBeEnabled();

    const patApi = await apiAs('pat');
    const unread = async () => {
      const res = await patApi.get('/channels');
      const channels: any[] = res.channels ?? res.data ?? res;
      return channels.find((c) => c.id === channelId)?.unreadCount ?? 0;
    };
    const before = await unread();

    const text = `read me later ${Date.now()}`;
    await alice.page.goto(`/chat/${channelId}`);
    const composer = alice.page.getByLabel('Message', { exact: true });
    await composer.fill(text);
    await composer.press('Enter');

    // It arrives over the socket, in a tab nobody is looking at.
    await expect(pat.page.getByText(text)).toBeVisible();
    // Give a wrongful mark-read its chance: wait for the page to be idle.
    await pat.page.waitForLoadState('networkidle');
    expect(await unread(), 'unread count while hidden').toBe(before + 1);

    await pat.page.evaluate(() => {
      (window as any).__visible = true;
      document.dispatchEvent(new Event('visibilitychange', { bubbles: true }));
    });
    await expect.poll(unread, { message: 'unread count once the tab is shown' }).toBe(0);
    await patApi.dispose();
  });

  test('36: task lists refresh when the window regains focus', async ({ as }) => {
    // Mia is not told when somebody else's task is created, so only focus can show it to her.
    const mia = await as('mia');
    await openTasks(mia.page, '?tab=board');
    await mia.page.getByRole('tab', { name: 'Team tasks' }).click();
    await expect(mia.page.getByRole('link', { name: TASKS.patPipeline })).toBeVisible();

    const title = `Pat board focus ${Date.now()}`;
    const owner = await apiAs('owner');
    await owner.post('/tasks', { title, assigneeId: readState().ids.pat, status: 'in_progress' });
    await owner.dispose();
    // Nothing happens to a page nobody returns to.
    await expect(mia.page.getByRole('link', { name: title })).toHaveCount(0);

    const returnToWindow = () =>
      mia.page.evaluate(() => document.dispatchEvent(new Event('visibilitychange', { bubbles: true })));
    await returnToWindow();
    await expect(mia.page.getByRole('link', { name: title })).toBeVisible({ timeout: 5_000 });

    // The overview too.
    await mia.page.getByRole('tab', { name: 'Overview' }).click();
    const before = (await tileCounts(mia.page)).ongoing;
    const owner2 = await apiAs('owner');
    await owner2.post('/tasks', { title: `${title} again`, assigneeId: readState().ids.pat, status: 'in_progress' });
    await owner2.dispose();
    await returnToWindow();
    await expect.poll(async () => (await tileCounts(mia.page)).ongoing, { timeout: 5_000 }).toBe(before + 1);
  });

  test('31: a chat edit shows live for the other person, without a false New message', async ({ as }) => {
    const pat = await as('pat');
    const alice = await as('alice');
    const { channelId } = readState();
    // Count desktop notifications Pat's browser is asked to raise.
    await pat.context.grantPermissions(['notifications']);
    await pat.context.addInitScript(() => {
      // Kept in sessionStorage so the count survives moving between pages in this tab.
      const log = (entry: unknown) => {
        const all = JSON.parse(sessionStorage.getItem('__notified') ?? '[]');
        all.push(entry);
        sessionStorage.setItem('__notified', JSON.stringify(all));
      };
      class FakeNotification {
        static permission = 'granted';
        static requestPermission = async () => 'granted';
        onclick: unknown = null;
        constructor(title: string, opts?: { body?: string }) {
          log({ title, body: opts?.body });
        }
        close() {}
      }
      (window as any).Notification = FakeNotification;
    });

    // Pat is on the tasks page, so the conversation is not in front of her.
    await openTasks(pat.page);
    await alice.page.goto(`/chat/${channelId}`);
    const original = `typo ${Date.now()}`;
    const composer = alice.page.getByLabel('Message', { exact: true });
    await composer.fill(original);
    await composer.press('Enter');
    await expect(alice.page.getByText(original)).toBeVisible();

    const patApi = await apiAs('pat');
    const unread = async () => {
      const res = await patApi.get('/channels');
      return ((res.channels ?? res.data ?? res) as any[]).find((c) => c.id === channelId)?.unreadCount ?? 0;
    };
    await expect.poll(() => pat.page.evaluate(() => JSON.parse(sessionStorage.getItem('__notified') ?? '[]').length)).toBe(1);
    const unreadAfterSend = await unread();
    expect(unreadAfterSend).toBeGreaterThanOrEqual(1);

    // Alice fixes the typo, from the message's own menu.
    const fixed = `${original} (fixed)`;
    const bubble = alice.page.locator('div.group', { hasText: original }).last();
    await bubble.hover();
    await bubble.getByRole('button', { name: 'Message actions' }).click();
    await alice.page.getByRole('menuitem', { name: 'Edit' }).click();
    const edit = alice.page.getByLabel('Edit message');
    // Picking Edit puts you in the box, not back on the menu's button.
    await expect(alice.page.getByRole('menu')).toHaveCount(0);
    await expect(edit).toBeFocused();
    await edit.fill(fixed);
    await edit.press('Enter');
    await expect(alice.page.getByText(fixed)).toBeVisible();

    // Pat opens the conversation: it already says the fixed thing, with no second notification.
    await pat.page.goto(`/chat/${channelId}`);
    await expect(pat.page.getByText(fixed)).toBeVisible();
    await expect(pat.page.getByText(original, { exact: true })).toHaveCount(0);
    expect(await pat.page.evaluate(() => JSON.parse(sessionStorage.getItem('__notified') ?? '[]').length)).toBe(1);

    // And with Pat looking at it, a second edit arrives live.
    const again = `${fixed} again`;
    const bubble2 = alice.page.locator('div.group', { hasText: fixed }).last();
    await bubble2.hover();
    await bubble2.getByRole('button', { name: 'Message actions' }).click();
    await alice.page.getByRole('menuitem', { name: 'Edit' }).click();
    const edit2 = alice.page.getByLabel('Edit message');
    await expect(alice.page.getByRole('menu')).toHaveCount(0);
    await expect(edit2).toBeFocused();
    await edit2.fill(again);
    await edit2.press('Enter');
    await expect(pat.page.getByText(again)).toBeVisible();
    expect(await pat.page.evaluate(() => JSON.parse(sessionStorage.getItem('__notified') ?? '[]').length), 'notifications after two edits').toBe(1);
    await patApi.dispose();
  });
});

test.describe('9. due today is not overdue', () => {
  test('a task due today, created through the UI, is not overdue on its card or in the count', async ({ as }) => {
    // A person of its own, so nothing else in the organization can colour the numbers.
    const owner = await apiAs('owner');
    const stamp = Date.now();
    const created = await owner.post('/users', { name: `Tess Today ${stamp}`, email: `tess${stamp}@e2e.test` });
    const them = await Api.signIn(`tess${stamp}@e2e.test`, created.temporaryPassword);
    await them.post('/auth/password', { currentPassword: created.temporaryPassword, newPassword: PEOPLE.alice.password });
    await them.dispose();
    await owner.dispose();

    const { page } = await as('alice', { signedOut: true });
    await signIn(page, { email: `tess${stamp}@e2e.test`, password: PEOPLE.alice.password });

    const title = `Due today ${stamp}`;
    await openTasks(page);
    await createTaskInUi(page, { title, assignee: `Tess Today ${stamp}`, dueDay: localDay(0) });

    // The card on the board.
    await openTasks(page, '?tab=board');
    const card = page.locator('div.rounded-lg', { has: page.getByRole('link', { name: title }) }).last();
    await expect(card).toBeVisible();
    await expect.soft(card, 'the card must say it is due, not overdue').not.toContainText(/overdue/i);
    await expect.soft(card).toContainText(/Due /);

    // The dashboard count and the row.
    await openTasks(page);
    await expect(page.getByRole('link', { name: title }).first()).toBeVisible();
    expect.soft((await tileCounts(page)).overdue, 'Overdue tile').toBe(0);
    await expect.soft(page.getByRole('heading', { name: /^Overdue/ })).toHaveCount(0);
    await expect.soft(page.locator('li', { hasText: title }).first()).not.toContainText(/overdue/i);

    // The same task seen by the person who answers for her, through the API.
    const api = await Api.signIn(`tess${stamp}@e2e.test`, PEOPLE.alice.password);
    const dash = await api.get('/tasks/dashboard');
    expect.soft(dash.totals.overdue, 'totals.overdue from the server').toBe(0);
    await api.dispose();
  });
});

test.describe('52. a due date set on the task page', () => {
  test('stays the day that was picked and is not overdue during it', async ({ as }) => {
    const owner = await apiAs('owner');
    const stamp = Date.now();
    const created = await owner.post('/users', { name: `Dana Day ${stamp}`, email: `dana${stamp}@e2e.test` });
    const them = await Api.signIn(`dana${stamp}@e2e.test`, created.temporaryPassword);
    await them.post('/auth/password', { currentPassword: created.temporaryPassword, newPassword: PEOPLE.alice.password });
    await them.dispose();
    await owner.dispose();

    const { page } = await as('alice', { signedOut: true });
    await signIn(page, { email: `dana${stamp}@e2e.test`, password: PEOPLE.alice.password });

    const title = `No date yet ${stamp}`;
    await openTasks(page);
    await createTaskInUi(page, { title, assignee: `Dana Day ${stamp}` });
    await page.getByRole('link', { name: title }).first().click();

    await page.getByLabel('Set due date').fill(localDay(0));
    const today = new Date().toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' });
    // The card, not the history, which says the same date once it was set.
    await expect(page.getByTestId('due-date')).toHaveText(today);
    await page.reload();
    await expect(page.getByTestId('due-date')).toHaveText(today);

    await openTasks(page);
    await expect(page.getByRole('link', { name: title }).first()).toBeVisible();
    expect((await tileCounts(page)).overdue).toBe(0);
    await expect(page.locator('li', { hasText: title }).first()).not.toContainText(/overdue/i);
  });
});

test.describe('51. the chosen theme', () => {
  test('a saved dark theme is on the page before any of the app has run', async ({ as }) => {
    const { page } = await as('alice', { signedOut: true, theme: 'dark' });
    await page.route('**/assets/*.js', (route) => route.abort());
    await page.goto('/login');
    await expect(page.locator('html')).toHaveClass(/\bdark\b/);
  });

  test('the header toggle starts from the theme on screen, and a click saves the new choice', async ({ as }) => {
    const { page } = await as('alice', { theme: 'dark' });
    await openTasks(page);
    await expect(page.locator('html')).toHaveClass(/\bdark\b/);
    await expect(page.getByRole('button', { name: 'Switch to light mode' })).toBeVisible();

    await page.getByRole('button', { name: 'Switch to light mode' }).click();
    await expect(page.locator('html')).not.toHaveClass(/\bdark\b/);
    await expect(page.getByRole('button', { name: 'Switch to dark mode' })).toBeVisible();
    expect(await page.evaluate(() => localStorage.getItem('theme'))).toBe('light');
  });

  test('changing it in Settings is reflected by the header toggle', async ({ as }) => {
    const { page } = await as('alice');
    await page.goto('/settings');
    await page.getByRole('tab', { name: 'Appearance' }).click();
    await pickOption(page, page.getByLabel('Theme'), 'Dark');
    await expect(page.locator('html')).toHaveClass(/\bdark\b/);
    await expect(page.getByRole('button', { name: 'Switch to light mode' })).toBeVisible();
  });
});

test.describe('polish. the tab strip on a phone', () => {
  const fade = (page: Page, side: 'left' | 'right') => page.getByTestId(`tabs-more-${side}`);
  const pageScrollsSideways = (page: Page) =>
    page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 1);

  test('says there is more, and every tab can be reached', async ({ as }) => {
    const { page } = await as('mia', { width: 375, height: 812 });
    await openTasks(page);
    await expect(tabNames(page)).toHaveText(SIX_TABS);
    await expect(fade(page, 'right')).toBeVisible();
    await expect(fade(page, 'left')).toHaveCount(0);
    await expect(page.getByRole('tab', { name: 'Board' })).not.toBeInViewport({ ratio: 1 });
    expect(await pageScrollsSideways(page)).toBe(false);

    await page.getByRole('tab', { name: 'Board' }).click();
    await expect(page.getByRole('tab', { name: 'Board' })).toHaveAttribute('aria-selected', 'true');
    await expect(page.getByRole('tab', { name: 'Board' })).toBeInViewport({ ratio: 1 });
    await expect(fade(page, 'right')).toHaveCount(0);
    await expect(fade(page, 'left')).toBeVisible();
    expect(await pageScrollsSideways(page)).toBe(false);
  });

  test('a deep link to a tab in the middle brings it into view with both sides marked', async ({ as }) => {
    const { page } = await as('mia', { width: 375, height: 812 });
    await openTasks(page, '?tab=history');
    await expect(page.getByRole('tab', { name: 'History' })).toBeInViewport({ ratio: 1 });
    await expect(fade(page, 'right')).toBeVisible();
    await expect(fade(page, 'left')).toBeVisible();
  });

  test('nothing is drawn on a screen where all the tabs fit', async ({ as }) => {
    const { page } = await as('mia');
    await openTasks(page);
    await expect(fade(page, 'right')).toHaveCount(0);
    await expect(fade(page, 'left')).toHaveCount(0);
  });
});

test.describe('54. forgotten password', () => {
  test('an admin resets it, the temporary password works, the member is made to change it, the old one is dead', async ({ as }) => {
    const owner = await apiAs('owner');
    const stamp = Date.now();
    const name = `Fay Forgetful ${stamp}`;
    const email = `fay${stamp}@e2e.test`;
    const oldPassword = `Old-pass-${stamp}`;
    const newPassword = `New-pass-${stamp}`;
    const created = await owner.post('/users', { name, email });
    const fay = await Api.signIn(email, created.temporaryPassword);
    await fay.post('/auth/password', { currentPassword: created.temporaryPassword, newPassword: oldPassword });
    await fay.dispose();
    const signedInBefore = await Api.signIn(email, oldPassword);
    await owner.dispose();

    // The login page says what to do, and no longer links anywhere.
    const member = await as('alice', { signedOut: true });
    await member.page.goto('/login');
    await expect(member.page.getByText('Ask an administrator of your organization to reset it.')).toBeVisible();
    await expect(member.page.getByRole('link', { name: /forgot/i })).toHaveCount(0);

    // The owner resets it from the Organization page.
    const { page } = await as('owner');
    await page.goto('/organization');
    const row = page.locator('div.rounded-lg', { hasText: email }).last();
    await row.getByRole('button', { name: 'Reset password' }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Reset password' }).click();
    await expect(page.getByRole('heading', { name: 'Password reset' })).toBeVisible();
    const temporary = await page.getByLabel('Temporary password').inputValue();
    expect(temporary.length).toBeGreaterThanOrEqual(16);
    await page.getByRole('button', { name: 'Done' }).click();

    // Whoever was signed in as them is out, and the old password is dead.
    expect(await signedInBefore.status('get', '/auth/me')).toBe(401);
    await signedInBefore.dispose();
    await expect(Api.signIn(email, oldPassword)).rejects.toThrow(/401/);

    // The temporary password gets them in, but only as far as changing it.
    await member.page.getByLabel('Email').fill(email);
    await member.page.getByLabel('Password').fill(temporary);
    await member.page.getByRole('button', { name: /^sign in$/i }).click();
    await expect(member.page).toHaveURL(/\/change-password/);
    await member.page.goto('/tasks');
    await expect(member.page).toHaveURL(/\/change-password/);
    await member.page.getByLabel('Temporary password').fill(temporary);
    await member.page.getByLabel('New password', { exact: true }).fill(newPassword);
    await member.page.getByLabel('Confirm new password').fill(newPassword);
    await member.page.getByRole('button', { name: /set password/i }).click();
    await expect(member.page).toHaveURL(/\/login/);

    // Their own password works from here on, and the temporary one does not.
    await signIn(member.page, { email, password: newPassword });
    await expect(Api.signIn(email, temporary)).rejects.toThrow(/401/);
  });
});
