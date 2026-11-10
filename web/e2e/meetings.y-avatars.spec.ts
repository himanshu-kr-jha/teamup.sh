import { readFileSync } from 'node:fs';
import type { Locator } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { apiAs, idOf } from './support/meetings';
import { PEOPLE } from './support/people';

const PICTURE = readFileSync(new URL('./fixtures/picture.png', import.meta.url));

/** Her picture, loaded: an <img> named for her, really drawn. */
async function showsPicture(where: Locator) {
  const img = where.getByRole('img', { name: `${PEOPLE.alice.name}'s picture` }).first();
  await expect(img).toBeVisible();
  await expect.poll(() => img.evaluate((el: HTMLImageElement) => el.complete && el.naturalWidth > 0)).toBe(true);
}

// Reported: somebody's profile picture showed in a few places and a letter
// everywhere else. Now it is the picture wherever the person is shown.
test('a profile picture shows wherever its person does', async ({ as }) => {
  test.setTimeout(90_000);
  const stamp = Date.now();
  const alice = await as('alice');
  await alice.page.goto('/settings');
  await alice.page.getByLabel('Profile picture file').setInputFiles({ name: 'me.png', mimeType: 'image/png', buffer: PICTURE });
  await expect(alice.page.getByRole('button', { name: 'Change picture' })).toBeVisible({ timeout: 30_000 });

  const owner = await apiAs('owner');
  const aliceApi = await apiAs('alice');
  const task = await owner.post('/tasks', { title: `Pictured ${stamp}`, assigneeId: idOf('alice') });
  try {
    const dm = await aliceApi.post('/channels/direct', { userId: idOf('owner') });
    await aliceApi.post(`/channels/${dm.id}/messages`, { content: `hello ${stamp}`, tempId: `pic-${stamp}` });
    await aliceApi.post(`/tasks/${task.id}/comments`, { content: `on it ${stamp}` });

    const olivia = await as('owner');
    const page = olivia.page;

    // Chat: the conversation list, the conversation's header, her message.
    await page.goto(`/chat/${dm.id}`);
    await showsPicture(page.getByRole('button', { name: new RegExp(PEOPLE.alice.name) }).first());
    await showsPicture(page.getByRole('heading', { level: 2, name: PEOPLE.alice.name }).locator('xpath=../..'));
    await showsPicture(page.getByText(`hello ${stamp}`).locator('xpath=ancestor::div[.//img][1]'));
    await page.screenshot({ path: 'test-results/avatars-chat.png' });

    // A task: who it is assigned to, and her comment.
    await page.goto(`/tasks/${task.id}`);
    await showsPicture(page.getByText('Assignee', { exact: true }).first().locator('xpath=ancestor::div[contains(@class, "rounded")][1]'));
    await showsPicture(page.locator('li', { hasText: `on it ${stamp}` }));
    await page.screenshot({ path: 'test-results/avatars-task.png', fullPage: true });

    // Who is in the channel.
    await page.goto(`/chat/${dm.id}`);
    await page.getByRole('button', { name: 'Channel settings' }).click();
    await showsPicture(page.getByRole('dialog'));
  } finally {
    await owner.del(`/tasks/${task.id}`).catch(() => {});
    await alice.page.goto('/settings');
    await alice.page.getByRole('button', { name: 'Remove' }).click();
    await expect(alice.page.getByRole('button', { name: 'Upload picture' })).toBeVisible();
    await Promise.all([owner.dispose(), aliceApi.dispose()]);
  }
});
