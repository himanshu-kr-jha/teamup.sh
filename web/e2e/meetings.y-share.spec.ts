import { expect, test } from '@playwright/test';
import { BASE_URL } from './support/api';
import { signIn } from './support/fixtures';
import { apiAs, ensureMember, sql } from './support/meetings';
import { PEOPLE, readState } from './support/people';

// Asked for: sharing something from any other app should offer TeamUp, and then
// ask whom to send it to, from the chats there are or a new one.

const SKY = { key: 'sky', name: 'Sky Sharer', email: 'sky@e2e.test', password: 'E2e-pass-12345!' };

test('what another app shares lands in TeamUp, to send to a conversation or a new chat', async ({ browser, request }) => {
  test.setTimeout(90_000);
  const { channelId } = readState();
  const channelName = sql(`SELECT name FROM channels WHERE id = '${channelId}'`);
  const skyId = await ensureMember(SKY);
  // Somebody Alice has never written to, so they are offered as a new chat.
  sql(`DELETE FROM channels WHERE type = 'dm' AND id IN (
         SELECT a.channel_id FROM channel_members a JOIN channel_members b ON a.channel_id = b.channel_id
          WHERE a.user_id = (SELECT id FROM users WHERE email = '${PEOPLE.alice.email}') AND b.user_id = '${skyId}')`);

  // The phone learns TeamUp takes shares, files too, from the manifest.
  const manifest = await (await request.get('/manifest.webmanifest')).json();
  expect(manifest.share_target).toMatchObject({ action: '/share', method: 'POST', enctype: 'multipart/form-data' });
  expect(manifest.share_target.params.files[0].name).toBe('files');

  // Its pages go through the service worker, so What's new and the tour are marked seen where they are kept.
  sql(`UPDATE users SET tour_completed_at = COALESCE(tour_completed_at, NOW()), whats_new_seen = '9999-12-31' WHERE email = '${PEOPLE.alice.email}'`);
  const context = await browser.newContext({ baseURL: BASE_URL, viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });
  const page = await context.newPage();
  await signIn(page, 'alice');
  await page.evaluate(() => navigator.serviceWorker.ready);
  await page.reload();
  await expect.poll(() => page.evaluate(() => !!navigator.serviceWorker.controller)).toBe(true);

  // What the share sheet does: a form POSTed to /share, with the file in it.
  const stamp = Date.now();
  await page.evaluate((stamp) => {
    const form = document.createElement('form');
    form.method = 'POST';
    form.action = '/share';
    form.enctype = 'multipart/form-data';
    const field = (name: string, value: string) => {
      const input = document.createElement('input');
      input.type = 'hidden';
      input.name = name;
      input.value = value;
      form.appendChild(input);
    };
    field('title', 'Release notes');
    field('text', `Have a look ${stamp}`);
    field('url', 'https://example.com/notes');
    const files = document.createElement('input');
    files.type = 'file';
    files.name = 'files';
    const data = new DataTransfer();
    data.items.add(new File(['hello from another app'], 'notes.txt', { type: 'text/plain' }));
    files.files = data.files;
    form.appendChild(files);
    document.body.appendChild(form);
    form.submit();
  }, stamp);

  await expect(page).toHaveURL(/\/share$/);
  const box = page.getByLabel('Message', { exact: true });
  await expect(box).toHaveValue(`Release notes\nHave a look ${stamp}\nhttps://example.com/notes`);
  await expect(page.getByRole('list', { name: 'Files' })).toContainText('notes.txt');

  // The conversations in use, and anybody not talked to yet under New chat.
  await page.getByRole('option', { name: channelName }).click();
  await page.getByLabel('Find a conversation or a person').fill('Sky');
  const newChat = page.getByRole('listbox', { name: 'New chat' });
  await newChat.getByRole('option', { name: /Sky Sharer/ }).click();
  await page.getByLabel('Find a conversation or a person').fill('');
  await expect(page.getByText(`${channelName}, Sky Sharer`)).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await page.screenshot({ path: 'test-results/share-into-teamup.png' });

  await page.getByRole('button', { name: 'Send', exact: true }).click();
  await expect(page).toHaveURL(/\/chat$/);

  // It reached both, as one message with the file.
  const alice = await apiAs('alice');
  try {
    const inChannel: any = await alice.get(`/channels/${channelId}/messages`);
    const sent = inChannel.messages.find((m: any) => m.content.includes(`Have a look ${stamp}`));
    expect(sent?.attachments?.map((a: any) => a.filename)).toEqual(['notes.txt']);
    const channels: any = await alice.get('/channels');
    const dm = (channels.channels ?? channels.data).find((c: any) => c.type === 'dm' && c.counterpartId === skyId);
    expect(dm, 'a new chat with Sky').toBeTruthy();
    const inDm: any = await alice.get(`/channels/${dm.id}/messages`);
    expect(inDm.messages.some((m: any) => m.content.includes(`Have a look ${stamp}`) && m.attachments?.length === 1)).toBe(true);
  } finally {
    await alice.dispose();
  }

  // Once sent it is gone: opening Share again has nothing waiting.
  await page.goto('/share');
  await expect(page.getByText('Nothing came with the share.')).toBeVisible();
  await context.close();
});
