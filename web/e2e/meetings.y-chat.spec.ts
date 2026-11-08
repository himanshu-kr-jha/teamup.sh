import { expect, test } from './support/fixtures';
import { apiAs, idOf, sql } from './support/meetings';
import { readState } from './support/people';

// Mentions, history a day at a time, closing with Esc, several files at once,
// and voice and video messages recorded on the spot. Against the real stack;
// the browser's microphone and camera are Chromium's fake devices.

const composer = (page: import('@playwright/test').Page) => page.getByRole('textbox', { name: 'Message', exact: true });

test.describe('mentions', () => {
  test('typing @ suggests people, the mention shows as @Name, and the person is notified', async ({ as }) => {
    const olivia = await as('owner');
    const { channelId } = readState();
    const alice = await apiAs('alice');
    const before = ((await alice.get('/notifications')).notifications as Array<{ type: string }> | undefined)?.length ?? 0;

    await olivia.page.goto(`/chat/${channelId}`);
    await composer(olivia.page).click();
    await composer(olivia.page).pressSequentially('thanks @ali');
    const suggestions = olivia.page.getByRole('listbox', { name: 'People to mention' });
    await expect(suggestions.getByRole('option', { name: /Alice Archer/ })).toBeVisible();
    await composer(olivia.page).press('Enter');
    await expect(composer(olivia.page)).toHaveValue('thanks @alice ');
    const text = `for the notes ${Date.now()}`;
    await composer(olivia.page).pressSequentially(text);
    await composer(olivia.page).press('Enter');

    const bubble = olivia.page.locator('div.group', { hasText: text }).last();
    await expect(bubble.getByTestId('mention')).toHaveText('@Alice Archer');

    await expect
      .poll(async () => {
        const list = (await alice.get('/notifications')).notifications as Array<{ type: string; title: string }>;
        return list.filter((n) => n.type === 'chat.mention').length;
      })
      .toBeGreaterThan(0);
    expect(((await alice.get('/notifications')).notifications as unknown[]).length).toBeGreaterThan(before);

    // Esc closes the suggestions first, and only then the conversation.
    await composer(olivia.page).pressSequentially('@');
    await expect(suggestions).toBeVisible();
    await olivia.page.keyboard.press('Escape');
    await expect(suggestions).toBeHidden();
    await expect(olivia.page).toHaveURL(new RegExp(`/chat/${channelId}$`));
    await olivia.page.keyboard.press('Escape');
    await expect(olivia.page).toHaveURL(/\/chat$/);
    await expect(olivia.page.getByTestId('no-conversation')).toBeVisible();
    await expect(composer(olivia.page)).toHaveCount(0);
  });
});

test.describe('history', () => {
  test('a conversation opens on its newest day and loads the day before on the way up', async ({ as }) => {
    const owner = await apiAs('owner');
    const room = await owner.post('/channels', { type: 'group', name: `days-${Date.now()}` });
    const org = sql(`SELECT org_id FROM channels WHERE id = '${room.id}'`);
    // Sixty messages on each of two days, a week apart: more than a window holds.
    sql(`INSERT INTO messages (org_id, channel_id, user_id, content, created_at)
         SELECT '${org}', '${room.id}', '${idOf('owner')}', 'older day ' || g, NOW() - INTERVAL '8 days' + g * INTERVAL '1 minute'
         FROM generate_series(1, 60) g`);
    sql(`INSERT INTO messages (org_id, channel_id, user_id, content, created_at)
         SELECT '${org}', '${room.id}', '${idOf('owner')}', 'newer day ' || g, date_trunc('day', NOW()) + g * INTERVAL '1 minute'
         FROM generate_series(1, 60) g`);

    const olivia = await as('owner');
    const first = olivia.page.waitForRequest((r) => r.url().includes(`/channels/${room.id}/messages`) && r.url().includes('unit=day'));
    await olivia.page.goto(`/chat/${room.id}`);
    await first;
    await expect(olivia.page.getByText('newer day 60', { exact: true })).toBeVisible();
    await expect(olivia.page.getByText('older day 60', { exact: true })).toHaveCount(0);

    // Scrolling to the top brings the day before, wherever it is.
    const list = olivia.page.locator('div.overflow-y-auto', { has: olivia.page.getByText('newer day 1', { exact: true }) }).last();
    await list.evaluate((el) => el.scrollTo(0, 0));
    await expect(olivia.page.getByText('older day 60', { exact: true })).toBeAttached({ timeout: 15_000 });
    await list.evaluate((el) => el.scrollTo(0, 0));
    await expect(olivia.page.getByText(/This is the beginning of/)).toBeVisible({ timeout: 15_000 });
  });
});

test.describe('attachments', () => {
  test('several files go up at once and arrive in one message', async ({ as }) => {
    const olivia = await as('owner');
    const { channelId } = readState();
    await olivia.page.goto(`/chat/${channelId}`);
    const stamp = Date.now();
    const files = ['a', 'b', 'c'].map((n) => ({ name: `${n}-${stamp}.txt`, mimeType: 'text/plain', buffer: Buffer.from(`file ${n}`) }));
    await olivia.page.locator('input[type="file"][aria-label="Attach a file"]').setInputFiles(files);
    await expect(olivia.page.getByText('3 attached. Press Send to share.')).toBeVisible({ timeout: 30_000 });
    await olivia.page.getByRole('button', { name: 'Send', exact: true }).click();
    for (const f of files) await expect(olivia.page.getByText(f.name).last()).toBeVisible();
  });

  test('a voice message and a video message are recorded on the spot and play where they were sent', async ({ as }) => {
    const olivia = await as('owner');
    await olivia.context.grantPermissions(['microphone', 'camera']);
    const { channelId } = readState();
    await olivia.page.goto(`/chat/${channelId}`);

    await olivia.page.getByRole('button', { name: 'Record a voice message' }).click();
    const bar = olivia.page.getByRole('group', { name: 'Recording a voice message' });
    await expect(bar.getByTestId('recording-time')).toHaveText('0:02', { timeout: 10_000 });
    await bar.getByRole('button', { name: 'Done' }).click();
    await expect(olivia.page.getByText('Attached. Press Send to share.')).toBeVisible({ timeout: 30_000 });
    await olivia.page.getByRole('button', { name: 'Send', exact: true }).click();
    const voice = olivia.page.getByTestId('voice-message').last();
    await expect(voice).toBeVisible({ timeout: 15_000 });
    await expect
      .poll(() => voice.locator('audio').evaluate((a: HTMLAudioElement) => a.readyState), { timeout: 15_000 })
      .toBeGreaterThan(0);

    await olivia.page.getByRole('button', { name: 'Record a video message' }).click();
    const dialog = olivia.page.getByRole('dialog', { name: 'Video message' });
    await dialog.getByRole('button', { name: 'Record' }).click();
    await olivia.page.waitForTimeout(2000);
    await dialog.getByRole('button', { name: 'Stop' }).click();
    await expect(dialog.getByTestId('video-note-preview')).toBeVisible();
    await dialog.getByRole('button', { name: 'Attach' }).click();
    await expect(olivia.page.getByText('Attached. Press Send to share.')).toBeVisible({ timeout: 30_000 });
    await olivia.page.getByRole('button', { name: 'Send', exact: true }).click();
    const video = olivia.page.getByTestId('video-message').last();
    await expect(video).toBeVisible({ timeout: 15_000 });
    await expect
      .poll(() => video.locator('video').evaluate((v: HTMLVideoElement) => v.readyState), { timeout: 15_000 })
      .toBeGreaterThan(0);
  });
});
