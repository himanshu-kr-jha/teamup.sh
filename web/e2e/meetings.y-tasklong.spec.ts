import type { Page } from '@playwright/test';
import { expect, openTasks, test } from './support/fixtures';
import { apiAs, idOf } from './support/meetings';

// A task that has been worked on for a while: its comments and history are
// kept short, with the rest a click away. And labels, which could be removed
// but never made: typing one makes it, typing it again finds it, and the board
// can show only the tasks with it.

const card = (page: Page, title: string) => page.getByRole('heading', { name: title }).locator('xpath=ancestor::*[contains(@class,"rounded")][1]');

test.describe('long tasks and labels', () => {
  const made: string[] = [];
  test.afterAll(async () => {
    const owner = await apiAs('owner');
    for (const id of made) await owner.del(`/tasks/${id}`).catch(() => {});
    await owner.dispose();
  });

  test('a task with many comments and changes shows the latest few and the rest on asking', async ({ as }) => {
    const owner = await apiAs('owner');
    const task = await owner.post('/tasks', { title: `Long task ${Date.now()}`, assigneeId: idOf('alice') });
    made.push(task.id);
    for (let i = 1; i <= 8; i++) await owner.post(`/tasks/${task.id}/comments`, { content: `Comment number ${i}` });
    for (const priority of ['high', 'low', 'urgent', 'medium', 'high', 'low', 'urgent']) {
      await owner.patch(`/tasks/${task.id}`, { priority });
    }
    await owner.dispose();

    const olivia = await as('owner');
    await olivia.page.goto(`/tasks/${task.id}`);

    // Comments: the newest five, and the three before them on asking.
    const comments = olivia.page.getByText(/^Comment number \d$/);
    await expect(comments).toHaveCount(5);
    await expect(comments.last()).toHaveText('Comment number 8');
    await expect(olivia.page.getByText('Comment number 3', { exact: true })).toHaveCount(0);
    await olivia.page.getByRole('button', { name: 'Show 3 earlier comments' }).click();
    await expect(comments).toHaveCount(8);
    await expect(comments.first()).toHaveText('Comment number 1');
    await olivia.page.getByRole('button', { name: 'Show less' }).first().click();
    await expect(comments).toHaveCount(5);

    // History: the latest five changes, and the rest below them on asking.
    const activity = card(olivia.page, 'Activity');
    const more = activity.getByRole('button', { name: /^Show \d+ more changes$/ });
    await expect(more).toBeVisible();
    const hidden = Number((await more.textContent())!.match(/\d+/)![0]);
    expect(hidden).toBeGreaterThan(0);
    const entries = activity.locator('li');
    await expect(entries).toHaveCount(5);
    await more.click();
    await expect(entries).toHaveCount(5 + hidden);
    await expect(activity.getByRole('button', { name: 'Show less' })).toBeVisible();
  });

  test('typing a label makes it, typing it again finds it, and the board shows only the tasks with it', async ({ as }) => {
    const owner = await apiAs('owner');
    const stamp = Date.now();
    const name = `Front end ${stamp}`;
    const tagged = await owner.post('/tasks', { title: `Tagged ${stamp}`, assigneeId: idOf('owner') });
    const other = await owner.post('/tasks', { title: `Untagged ${stamp}`, assigneeId: idOf('owner') });
    made.push(tagged.id, other.id);
    await owner.dispose();

    const olivia = await as('owner');
    await olivia.page.goto(`/tasks/${tagged.id}`);
    const labels = card(olivia.page, 'Labels');
    await expect(labels).toContainText('Labels sort tasks into kinds');
    const box = labels.getByLabel('Add a label');
    await box.fill(name);
    await labels.getByRole('button', { name: `Make the label “${name}”` }).click();
    await expect(labels.getByTestId('task-label').filter({ hasText: name })).toBeVisible();
    await expect(box).toHaveValue('');

    // On the other task it is offered, in whatever case it is typed, not made twice.
    await olivia.page.goto(`/tasks/${other.id}`);
    const box2 = card(olivia.page, 'Labels').getByLabel('Add a label');
    await box2.fill(name.toUpperCase());
    await expect(card(olivia.page, 'Labels').getByRole('button', { name: /Make the label/ })).toHaveCount(0);
    await box2.press('Escape');
    const check = await apiAs('owner');
    const all = (await check.get('/labels')).labels as Array<{ name: string }>;
    expect(all.filter((l) => l.name.toLowerCase() === name.toLowerCase())).toHaveLength(1);
    await check.dispose();

    // The board narrows to the tasks with it.
    await openTasks(olivia.page, '?tab=board');
    await expect(olivia.page.getByText(new RegExp(`(?<!Un)Tagged ${stamp}`)).filter({ visible: true }).first()).toBeVisible();
    await expect(olivia.page.getByText(`Untagged ${stamp}`).filter({ visible: true }).first()).toBeVisible();
    await olivia.page.getByRole('combobox', { name: 'Filter by label' }).click();
    await olivia.page.getByRole('option', { name }).click();
    await expect(olivia.page.getByText(new RegExp(`(?<!Un)Tagged ${stamp}`)).filter({ visible: true }).first()).toBeVisible();
    await expect(olivia.page.getByText(`Untagged ${stamp}`)).toHaveCount(0);
    expect(await olivia.page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  });
});
