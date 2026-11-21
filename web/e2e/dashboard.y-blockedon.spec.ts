import type { Locator, Page } from '@playwright/test';
import { Api } from './support/api';
import { PEOPLE, readState } from './support/people';
import { expect, openTasks, test } from './support/fixtures';

// Asked for: in the scrum, what is blocked shows at the end, like Done, with
// what each task is blocked on. Marking a task Blocked asks for that reason,
// wherever it is done, and the task page shows it and lets it be changed.
//
// Runs on the seeded organization: Mia manages Backend, where Alice works.

const stamp = Date.now();
const WAITS = `Blocked by vendor contract ${stamp}`;
const STUCK = `Integrate vendor API ${stamp}`;
const PAGE_STUCK = `Write the rollout plan ${stamp}`;

const card = (page: Page, title: string) =>
  page.getByTestId('task-card').filter({ has: page.getByRole('link', { name: title }) });
const scrumRow = (page: Page, name: string): Locator =>
  page
    .getByTestId('scrum-table')
    .locator('tr', { has: page.getByTestId('scrum-person-name').getByText(name, { exact: true }) });

async function apiAs(who: keyof typeof PEOPLE) {
  return Api.signIn(PEOPLE[who].email, PEOPLE[who].password);
}

async function pick(dialog: Locator, field: string, search: string, choice: string | RegExp) {
  await dialog.getByRole('combobox', { name: field }).click();
  await dialog.page().getByRole('combobox', { name: search }).fill(typeof choice === 'string' ? choice : search);
  await dialog.page().getByRole('option', { name: choice }).first().click();
}

const ids: Record<string, string> = {};

test.describe('blocked on', () => {
  test.beforeAll(async () => {
    const owner = await apiAs('owner');
    const alice = readState().ids.alice;
    for (const title of [WAITS, STUCK, PAGE_STUCK]) {
      const t = await owner.post('/tasks', { title, assigneeId: alice, status: 'in_progress' });
      ids[title] = t.id;
    }
    await owner.dispose();
  });

  test.afterAll(async () => {
    const owner = await apiAs('owner');
    for (const id of Object.values(ids)) await owner.del(`/tasks/${id}`);
    await owner.dispose();
  });

  test('Alice marks a task Blocked from the board and says why; Mia sees it at the end of her scrum', async ({ as }) => {
    const alice = await as('alice');
    await openTasks(alice.page, '?tab=board');
    await card(alice.page, STUCK).getByRole('button', { name: 'Task actions' }).click();
    await alice.page.getByRole('menuitem', { name: 'Mark Blocked' }).click();

    const dialog = alice.page.getByRole('dialog', { name: 'Mark blocked' });
    await expect(dialog).toBeVisible();
    // A reason is needed before it can be saved.
    await expect(dialog.getByRole('button', { name: 'Mark blocked' })).toBeDisabled();
    await dialog.getByLabel('Blocked on', { exact: true }).fill('Waiting for the API keys');
    await pick(dialog, 'Waiting on a person (optional)', 'Search people', PEOPLE.mia.name);
    await pick(dialog, 'Waiting for a task (optional)', 'Search tasks', WAITS);
    const saved = alice.page.waitForResponse(
      (r) => r.request().method() === 'PATCH' && r.url().endsWith(`/api/tasks/${ids[STUCK]}`)
    );
    await dialog.getByRole('button', { name: 'Mark blocked' }).click();
    expect((await saved).ok()).toBe(true);
    await expect(dialog).toBeHidden();
    await expect(card(alice.page, STUCK).getByTestId('task-blocked-on')).toHaveText(
      `Blocked on: Waiting for the API keys; waiting on ${PEOPLE.mia.name}; waiting for ${WAITS}`
    );

    const mia = await as('mia');
    await openTasks(mia.page, '?tab=scrum');
    const row = scrumRow(mia.page, PEOPLE.alice.name);
    await expect(row.locator('td[data-task]', { hasText: STUCK })).toHaveCount(0);
    const item = row.getByTestId('scrum-blocked').locator('li', { hasText: STUCK });
    await expect(item.getByTestId('scrum-blocked-reason')).toHaveText(
      `Waiting for the API keys; waiting on ${PEOPLE.mia.name}; waiting for ${WAITS}`
    );
    await expect(item.getByRole('link', { name: WAITS })).toHaveAttribute('href', `/tasks/${ids[WAITS]}`);
  });

  test('the task page shows the reason, changes it, and keeps it in the history', async ({ as }) => {
    const { page } = await as('alice');
    await page.goto(`/tasks/${ids[STUCK]}`);
    const panel = page.getByTestId('task-blocked-on');
    await expect(panel).toContainText('Waiting for the API keys');
    await expect(panel).toContainText(`Waiting on ${PEOPLE.mia.name}`);
    await panel.getByRole('button', { name: 'Change' }).click();
    const dialog = page.getByRole('dialog', { name: 'What is it blocked on?' });
    await expect(dialog.getByLabel('Blocked on', { exact: true })).toHaveValue('Waiting for the API keys');
    await dialog.getByLabel('Blocked on', { exact: true }).fill('Keys promised for Monday');
    await dialog.getByRole('button', { name: 'Save' }).click();
    await expect(dialog).toBeHidden();
    await expect(panel).toContainText('Keys promised for Monday');
    await expect(page.getByText(/changed what it is blocked on/)).toBeVisible();
  });

  test('choosing Blocked as the status on the task page asks why too', async ({ as }) => {
    const { page } = await as('alice');
    await page.goto(`/tasks/${ids[PAGE_STUCK]}`);
    await page.getByRole('combobox').filter({ hasText: /^in progress$/i }).first().click();
    await page.getByRole('option', { name: /^blocked$/i }).click();
    const dialog = page.getByRole('dialog', { name: 'Mark blocked' });
    await expect(dialog).toBeVisible();
    // Cancelling leaves it as it was.
    await dialog.getByRole('button', { name: 'Cancel' }).click();
    await expect(page.getByTestId('task-blocked-on')).toHaveCount(0);

    await page.getByRole('combobox').filter({ hasText: /^in progress$/i }).first().click();
    await page.getByRole('option', { name: /^blocked$/i }).click();
    await dialog.getByLabel('Blocked on', { exact: true }).fill('Needs sign-off from legal');
    await dialog.getByRole('button', { name: 'Mark blocked' }).click();
    await expect(page.getByTestId('task-blocked-on')).toContainText('Needs sign-off from legal');
  });

  test('unblocked, a task goes back to its place among Task 1, 2, 3 and its reason is gone', async ({ as }) => {
    const alice = await apiAs('alice');
    await alice.patch(`/tasks/${ids[STUCK]}`, { status: 'in_progress' });
    const task = await alice.get(`/tasks/${ids[STUCK]}`);
    expect(task.blockedOn).toBeUndefined();
    await alice.dispose();

    const mia = await as('mia');
    await openTasks(mia.page, '?tab=scrum');
    const row = scrumRow(mia.page, PEOPLE.alice.name);
    await expect(row.locator('td[data-task]', { hasText: STUCK })).toHaveCount(1);
    await expect(row.getByTestId('scrum-blocked')).not.toContainText(STUCK);
  });
});
