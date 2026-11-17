import { expect, openTasks, test } from './support/fixtures';
import { apiAs, idOf } from './support/meetings';

// Reported: a manager's "My tasks" showed the work they had handed to their
// people, as if it were theirs. Then the owner asked (17 November 2026) to see
// every kind of their own together. So "All of mine" lists them all, each card
// saying how it is yours, and "Assigned to me" is still only yours to do.
test.describe('my tasks', () => {
  const stamp = Date.now();
  const handedOut = `Handed to Alice ${stamp}`;
  const notYetGiven = `Not given to anybody ${stamp}`;
  const given = `Given to Mia ${stamp}`;
  const followed = `Mia follows ${stamp}`;
  const made: string[] = [];

  test.beforeAll(async () => {
    const mia = await apiAs('mia');
    const owner = await apiAs('owner');
    made.push((await mia.post('/tasks', { title: handedOut, assigneeId: idOf('alice') })).id);
    made.push((await mia.post('/tasks', { title: notYetGiven })).id);
    made.push((await owner.post('/tasks', { title: given, assigneeId: idOf('mia') })).id);
    const watched = await owner.post('/tasks', { title: followed, assigneeId: idOf('alice') });
    made.push(watched.id);
    await owner.post(`/tasks/${watched.id}/watchers`, { userId: idOf('mia') });
    await Promise.all([mia.dispose(), owner.dispose()]);
  });

  test.afterAll(async () => {
    const owner = await apiAs('owner');
    for (const id of made) await owner.del(`/tasks/${id}`).catch(() => {});
    await owner.dispose();
  });

  for (const phone of [false, true]) {
    test(`a manager's own list is their own work, and what they handed out is apart${phone ? ', on a phone' : ''}`, async ({ as }) => {
      const mia = await as('mia', phone ? { phone: 'android' } : {});
      await openTasks(mia.page, '?tab=board');
      const card = (title: string) => mia.page.getByRole('link', { name: title });

      // Phones show one column at a time; these are all to do.
      await expect(mia.page.getByRole('tab', { name: 'My tasks' })).toHaveAttribute('aria-selected', 'true');
      const which = mia.page.getByRole('combobox', { name: 'Which of my tasks' });
      const marks = (title: string) =>
        mia.page.getByTestId('task-card').filter({ has: card(title) }).getByTestId('task-mine');

      // All of mine, each saying how.
      await which.click();
      await mia.page.getByRole('option', { name: 'All of mine' }).click();
      await expect(marks(given)).toHaveText(['Assigned']);
      await expect(marks(notYetGiven)).toHaveText(['Assigned']);
      await expect(marks(handedOut)).toHaveText(['Created']);
      await expect(marks(followed)).toHaveText(['Added']);
      await mia.page.screenshot({ path: `test-results/my-tasks-all-${phone ? 'phone' : 'desktop'}.png` });

      // Only what is hers to do.
      await which.click();
      await mia.page.getByRole('option', { name: 'Assigned to me' }).click();
      await expect(card(given)).toBeVisible();
      await expect(card(notYetGiven), 'raised and not handed out is still hers to do').toBeVisible();
      await expect(card(handedOut), "Alice's task is not Mia's").toHaveCount(0);
      await expect(card(followed)).toHaveCount(0);
      await expect(mia.page.getByTestId('task-mine')).toHaveCount(0);

      await which.click();
      await mia.page.getByRole('option', { name: 'Created by me' }).click();
      await expect(card(handedOut)).toBeVisible();
      await expect(card(given)).toHaveCount(0);
      await expect(card(notYetGiven)).toHaveCount(0);

      // The choice is kept for next time.
      await mia.page.reload();
      await expect(which).toHaveText('Created by me');
      await expect(card(handedOut)).toBeVisible();
      await mia.page.screenshot({ path: `test-results/my-tasks-${phone ? 'phone' : 'desktop'}.png` });

      // And with the rest of her team's work.
      await mia.page.getByRole('tab', { name: 'Team tasks' }).click();
      await expect(card(handedOut)).toBeVisible();
      await expect(which).toHaveCount(0);
    });
  }
});
