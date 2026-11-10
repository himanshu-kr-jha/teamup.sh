import { expect, openTasks, test } from './support/fixtures';
import { apiAs, idOf } from './support/meetings';

// Reported: a manager's "My tasks" showed the work they had handed to their
// people, as if it were theirs. My tasks is now the work that is yours to do;
// what you handed out is under "Created by me", and with your team's work.
test.describe('my tasks', () => {
  const stamp = Date.now();
  const handedOut = `Handed to Alice ${stamp}`;
  const notYetGiven = `Not given to anybody ${stamp}`;
  const given = `Given to Mia ${stamp}`;
  const made: string[] = [];

  test.beforeAll(async () => {
    const mia = await apiAs('mia');
    const owner = await apiAs('owner');
    made.push((await mia.post('/tasks', { title: handedOut, assigneeId: idOf('alice') })).id);
    made.push((await mia.post('/tasks', { title: notYetGiven })).id);
    made.push((await owner.post('/tasks', { title: given, assigneeId: idOf('mia') })).id);
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
      await expect(card(given)).toBeVisible();
      await expect(card(notYetGiven), 'raised and not handed out is still hers to do').toBeVisible();
      await expect(card(handedOut), "Alice's task is not Mia's").toHaveCount(0);

      const which = mia.page.getByRole('combobox', { name: 'Which of my tasks' });
      await expect(which).toHaveText('Assigned to me');
      await which.click();
      await mia.page.getByRole('option', { name: 'Created by me' }).click();
      await expect(card(handedOut)).toBeVisible();
      await expect(card(given)).toHaveCount(0);
      await expect(card(notYetGiven)).toHaveCount(0);
      await mia.page.screenshot({ path: `test-results/my-tasks-${phone ? 'phone' : 'desktop'}.png` });

      // And with the rest of her team's work.
      await mia.page.getByRole('tab', { name: 'Team tasks' }).click();
      await expect(card(handedOut)).toBeVisible();
      await expect(which).toHaveCount(0);
    });
  }
});
