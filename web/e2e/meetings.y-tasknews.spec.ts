import { expect, openTasks, test } from './support/fixtures';
import { apiAs, idOf } from './support/meetings';
import { readState } from './support/people';

// Reported: files sent on a task's comment could not be downloaded.
test.describe('task comment files', () => {
  const made: string[] = [];
  test.afterAll(async () => {
    const owner = await apiAs('owner');
    for (const id of made) await owner.del(`/tasks/${id}`).catch(() => {});
    await owner.dispose();
  });

  test('every kind of file sent on a comment downloads', async ({ as }) => {
    const owner = await apiAs('owner');
    const task = await owner.post('/tasks', { title: `Files on a comment ${Date.now()}`, assigneeId: idOf('alice') });
    made.push(task.id);
    await owner.dispose();

    const olivia = await as('owner');
    await olivia.page.goto(`/tasks/${task.id}`);
    await olivia.page.getByRole('button', { name: 'Add Comment' }).click();
    const png = Buffer.from(
      'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==',
      'base64'
    );
    await olivia.page.locator('input[type="file"][aria-label="Attach files to this comment"]').setInputFiles([
      { name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from('hello') },
      { name: 'shot.png', mimeType: 'image/png', buffer: png },
      { name: 'sheet.csv', mimeType: 'text/csv', buffer: Buffer.from('a,b\n1,2') },
    ]);
    await expect(olivia.page.getByText('shot.png').first()).toBeVisible({ timeout: 30_000 });
    await olivia.page.getByLabel('Comment', { exact: true }).fill('Here they are');
    await olivia.page.getByRole('button', { name: 'Post comment' }).click();
    const comment = olivia.page.locator('li', { hasText: 'Here they are' }).first();
    await expect(comment).toBeVisible();

    for (const name of ['notes.txt', 'sheet.csv']) {
      const download = olivia.page.waitForEvent('download', { timeout: 10_000 });
      await comment.getByRole('button', { name: new RegExp(name.replace('.', '\\.')) }).click();
      expect((await download).suggestedFilename(), `${name} downloads`).toBe(name);
    }
    const download = olivia.page.waitForEvent('download', { timeout: 10_000 });
    await comment.getByRole('button', { name: 'Download shot.png' }).click();
    expect((await download).suggestedFilename()).toBe('shot.png');
  });
});

// Reported: a reply in a thread kept one tick after it had been read.
test.describe('thread ticks', () => {
  test('a reply turns to a double tick once somebody opens the thread, live and after a reload', async ({ as }) => {
    const { channelId } = readState();
    const stamp = Date.now();
    const owner = await apiAs('owner');
    const alice = await apiAs('alice');
    const root = await owner.post(`/channels/${channelId}/messages`, { content: `Question ${stamp}` });
    await alice.post(`/channels/${channelId}/messages`, { content: `Answer ${stamp}`, replyTo: root.id });
    await Promise.all([owner.dispose(), alice.dispose()]);

    const author = await as('alice');
    await author.page.goto(`/chat/${channelId}`);
    const openThread = async (page: import('@playwright/test').Page) => {
      await page.getByText(`Question ${stamp}`).waitFor();
      await page.getByRole('button', { name: /1 reply/ }).last().click();
      await expect(page.getByRole('textbox', { name: 'Reply in thread' })).toBeVisible();
    };
    await openThread(author.page);
    const panel = author.page.getByRole('complementary').filter({ has: author.page.getByRole('textbox', { name: 'Reply in thread' }) });
    await expect(panel.getByLabel('Sent', { exact: true }).last(), 'nobody has read it yet').toBeVisible();
    await expect(panel.getByLabel('Read', { exact: true })).toHaveCount(0);

    // Reading the conversation is not reading the thread.
    const reader = await as('owner');
    await reader.page.goto(`/chat/${channelId}`);
    await reader.page.getByText(`Question ${stamp}`).waitFor();
    await reader.page.waitForTimeout(2_000);
    await expect(panel.getByLabel('Read', { exact: true })).toHaveCount(0);

    // Opening it is.
    await openThread(reader.page);
    await expect(panel.getByLabel('Read', { exact: true }).last(), 'live').toBeVisible({ timeout: 10_000 });
    await author.page.reload();
    await openThread(author.page);
    await expect(panel.getByLabel('Read', { exact: true }).last(), 'after a reload').toBeVisible();
  });
});

// Asked for: news about a task is heard, marked on Tasks and on the task, and
// a task due shows on the calendar.
test.describe('task news', () => {
  const made: string[] = [];
  test.afterAll(async () => {
    const owner = await apiAs('owner');
    for (const id of made) await owner.del(`/tasks/${id}`).catch(() => {});
    await owner.dispose();
  });

  test('a comment on your task plays the sound and marks Tasks and the task until you open it', async ({ as }) => {
    const title = `Draft the brief ${Date.now()}`;
    const due = new Date();
    due.setHours(23, 59, 59, 0);
    const owner = await apiAs('owner');
    const task = await owner.post('/tasks', { title, assigneeId: idOf('alice'), dueDate: due.toISOString() });
    made.push(task.id);

    const alice = await as('alice');
    await alice.context.addInitScript(() => {
      const w = window as unknown as { __sounds: string[] };
      w.__sounds = [];
      window.addEventListener('teamup:sound', (e) => w.__sounds.push((e as CustomEvent<string>).detail));
    });
    await openTasks(alice.page, '?tab=board');
    // Being handed it was news too; opening it reads that.
    await alice.page.getByRole('link', { name: title }).click();
    await expect(alice.page.getByRole('heading', { name: title })).toBeVisible();
    await openTasks(alice.page, '?tab=board');
    const card = alice.page.locator('div.rounded-lg', { has: alice.page.getByRole('link', { name: title }) }).first();
    await expect(card.getByTestId('task-unread-dot')).toHaveCount(0);
    await alice.page.locator('body').click({ position: { x: 5, y: 5 } }); // a page nobody has touched may not play sound

    await owner.post(`/tasks/${task.id}/comments`, { content: 'Can you add the numbers?' });
    await owner.dispose();

    await expect(card.getByTestId('task-unread-dot'), 'the task is marked').toBeVisible({ timeout: 10_000 });
    await expect(alice.page.getByTestId('tasks-unread-badge').first(), 'and so is Tasks').toBeVisible();
    await expect
      .poll(() => alice.page.evaluate(() => (window as unknown as { __sounds: string[] }).__sounds), { message: 'it was heard' })
      .toContain('notification');
    await alice.page.screenshot({ path: 'test-results/task-news-board.png' });

    // On the calendar, on the day it is due.
    await alice.page.goto('/calendar?view=week');
    const chip = alice.page.getByTestId('due-task').filter({ hasText: title });
    await expect(chip).toBeVisible();
    await alice.page.screenshot({ path: 'test-results/task-news-calendar-week.png' });
    await alice.page.goto('/calendar?view=month');
    await expect(chip).toBeVisible();
    await alice.page.screenshot({ path: 'test-results/task-news-calendar-month.png' });

    // Opening it reads it.
    await chip.click();
    await expect(alice.page.getByRole('heading', { name: title })).toBeVisible();
    await openTasks(alice.page, '?tab=board');
    await expect(card.getByTestId('task-unread-dot'), 'read once opened').toHaveCount(0, { timeout: 10_000 });
  });
});
