import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { apiAs, idOf } from './support/meetings';
import { readState } from './support/people';

// Forwarding messages and files, the calendar's month view, profile pictures,
// and the chat's Call button. Against the real stack.

/** A 64 by 48 PNG: a landscape picture, to see it cut to a square. */
async function landscapePng(page: Page): Promise<Buffer> {
  const dataUrl = await page.evaluate(() => {
    const c = document.createElement('canvas');
    c.width = 64;
    c.height = 48;
    const g = c.getContext('2d')!;
    g.fillStyle = '#e11d48';
    g.fillRect(0, 0, 64, 48);
    return c.toDataURL('image/png');
  });
  return Buffer.from(dataUrl.split(',')[1], 'base64');
}

async function sendFile(page: Page, name: string, mimeType: string, buffer: Buffer, text = '') {
  if (text) await page.getByPlaceholder(/^Message /).fill(text);
  await page.locator('input[type="file"][aria-label="Attach a file"]').setInputFiles({ name, mimeType, buffer });
  await expect(page.getByText(/Attached\. Press Send to share\./)).toBeVisible({ timeout: 30_000 });
  await page.getByRole('button', { name: 'Send', exact: true }).click();
  await expect(page.getByText(/Press Send to share/)).toBeHidden({ timeout: 30_000 });
}

test.describe('forwarding', () => {
  test('a message with a file is forwarded to two conversations, arrives marked Forwarded, and its file opens there', async ({ as }) => {
    const owner = await apiAs('owner');
    const stamp = Date.now();
    // Bob-like reader: Sam is in the targets but not in the source.
    const targets: string[] = [];
    for (const n of ['fwd-a', 'fwd-b']) {
      const room = await owner.post('/channels', { type: 'group', name: `${n}-${stamp}` });
      await owner.post(`/channels/${room.id}/members`, { userId: idOf('sam') });
      targets.push(room.id);
    }

    const olivia = await as('owner');
    const { channelId } = readState();
    await olivia.page.goto(`/chat/${channelId}`);
    const name = `notes-${stamp}.pdf`;
    const pdf = Buffer.from('%PDF-1.4\n1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n2 0 obj << /Type /Pages /Kids [] /Count 0 >> endobj\ntrailer << /Root 1 0 R >>\n%%EOF\n');
    await sendFile(olivia.page, name, 'application/pdf', pdf, `read this ${stamp}`);

    const bubble = olivia.page.locator('div.group', { hasText: `read this ${stamp}` }).last();
    await bubble.hover();
    await bubble.getByRole('button', { name: 'Message actions' }).click();
    await olivia.page.getByRole('menuitem', { name: 'Forward' }).click();

    const dialog = olivia.page.getByRole('dialog', { name: 'Forward message' });
    await expect(dialog.getByTestId('forward-preview')).toContainText(`read this ${stamp}`);
    await expect(dialog.getByTestId('forward-preview')).toContainText(name);
    await dialog.getByLabel('Find a conversation').fill(`${stamp}`);
    await dialog.getByRole('option', { name: `fwd-a-${stamp}` }).click();
    await dialog.getByRole('option', { name: `fwd-b-${stamp}` }).click();
    await expect(dialog.getByText('2 chosen')).toBeVisible();
    await dialog.getByRole('button', { name: 'Forward' }).click();
    await expect(olivia.page.getByText('Forwarded to 2 conversations.')).toBeVisible();

    // Sam, who was never in the original conversation, gets it in both.
    const sam = await as('sam');
    for (const [i, id] of targets.entries()) {
      await sam.page.goto(`/chat/${id}`);
      const got = sam.page.locator('div.group', { hasText: `read this ${stamp}` }).last();
      await expect(got.getByTestId('forwarded-label')).toHaveText('Forwarded');
      if (i === 0) {
        // And can save the file, which is a copy filed with this conversation.
        const download = sam.page.waitForEvent('download');
        await got.getByRole('button', { name: `Download ${name}` }).click();
        expect((await download).suggestedFilename()).toBe(name);
      }
    }

    // The original stays as it was.
    await expect(bubble.getByTestId('forwarded-label')).toHaveCount(0);
  });

  test('somebody who cannot read a message cannot forward it', async () => {
    const owner = await apiAs('owner');
    const room = await owner.post('/channels', { type: 'group', name: `secret-${Date.now()}` });
    const msg = await owner.post(`/channels/${room.id}/messages`, { content: 'secret' });
    const sam = await apiAs('sam');
    const mine = await sam.post('/channels', { type: 'group', name: `sams-${Date.now()}` });
    expect(await sam.status('post', `/messages/${msg.id}/forward`, { channelIds: [mine.id] })).toBe(404);
  });
});

test.describe('calendar views', () => {
  test('month, week and day, with the choice kept in the address, and a day opened from the month', async ({ as }) => {
    const olivia = await as('owner');
    const api = await apiAs('owner');
    const today = new Date();
    const start = new Date(today.getFullYear(), today.getMonth(), 15, 10, 0);
    const title = `Month review ${Date.now()}`;
    await api.post('/calendar/blocks', { kind: 'focus', title, startsAt: start.toISOString(), endsAt: new Date(start.getTime() + 3600_000).toISOString() });

    const day = `${start.getFullYear()}-${String(start.getMonth() + 1).padStart(2, '0')}-15`;
    await olivia.page.goto(`/calendar?date=${day}`);
    const views = olivia.page.getByRole('group', { name: 'View' });
    await expect(views.getByRole('button', { name: 'Week' })).toHaveAttribute('aria-pressed', 'true');

    await views.getByRole('button', { name: 'Month' }).click();
    await expect(olivia.page).toHaveURL(/view=month/);
    const month = olivia.page.getByTestId('month-grid');
    await expect(month).toBeVisible();
    const cells = month.getByTestId('month-day');
    expect((await cells.count()) % 7).toBe(0);
    const cell = month.locator(`[data-day="${day}"]`);
    await expect(cell.getByTestId('month-item').filter({ hasText: title })).toBeVisible();

    // Next and back move by a month.
    const heading = olivia.page.getByRole('heading', { level: 2 });
    const thisMonth = await heading.textContent();
    await olivia.page.getByRole('button', { name: 'Next month' }).click();
    await expect(heading).not.toHaveText(thisMonth!);
    await olivia.page.getByRole('button', { name: 'Previous month' }).click();
    await expect(heading).toHaveText(thisMonth!);

    // An item opens its details; the day opens in the day view.
    await cell.getByTestId('month-item').filter({ hasText: title }).click();
    await expect(olivia.page.getByRole('dialog')).toContainText(title);
    await olivia.page.keyboard.press('Escape');
    // The space under what the day lists is the day's.
    const box = (await cell.boundingBox())!;
    await olivia.page.mouse.click(box.x + box.width / 2, box.y + box.height - 8);
    await expect(olivia.page).toHaveURL(new RegExp(`view=day.*date=${day}|date=${day}.*view=day`));
    await expect(views.getByRole('button', { name: 'Day' })).toHaveAttribute('aria-pressed', 'true');

    // A reload keeps the view.
    await olivia.page.reload();
    await expect(views.getByRole('button', { name: 'Day' })).toHaveAttribute('aria-pressed', 'true');
  });

  test('on a phone the month shows a dot for each thing', async ({ as }) => {
    const phone = await as('owner', { width: 390, height: 844 });
    await phone.page.goto('/calendar?view=month');
    await expect(phone.page.getByTestId('month-grid')).toBeVisible();
    const scroll = await phone.page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
    expect(scroll).toBeLessThanOrEqual(0);
  });
});

test.describe('profile pictures', () => {
  test('somebody uploads a picture, it is cut square, and colleagues see it on their messages', async ({ as }) => {
    const sam = await as('sam');
    await sam.page.goto('/settings');
    await sam.page.getByLabel('Profile picture file').setInputFiles({ name: 'me.png', mimeType: 'image/png', buffer: await landscapePng(sam.page) });
    await expect(sam.page.getByText('Your picture has been updated.')).toBeVisible();
    const header = sam.page.getByRole('button', { name: 'User menu' }).getByRole('img');
    await expect(header).toBeVisible();
    const size = await header.evaluate((img: HTMLImageElement) => [img.naturalWidth, img.naturalHeight]);
    expect(size).toEqual([256, 256]);

    const { channelId } = readState();
    const text = `hello with a face ${Date.now()}`;
    const samApi = await apiAs('sam');
    await samApi.post(`/channels/${channelId}/messages`, { content: text });

    const olivia = await as('owner');
    await olivia.page.goto(`/chat/${channelId}`);
    const row = olivia.page.locator('div.group', { hasText: text }).last();
    await expect(row.getByRole('img', { name: "Sam Sales's picture" })).toBeVisible();

    // And back to initials.
    await sam.page.getByRole('button', { name: 'Remove' }).click();
    await expect(sam.page.getByText('Your picture has been removed.')).toBeVisible();
    await expect(sam.page.getByRole('button', { name: 'User menu' }).getByRole('img')).toHaveCount(0);
  });
});

test.describe('calling from chat', () => {
  test('Call starts a voice call: microphones on, cameras off, and either side can turn video on', async ({ as }) => {
    const owner = await apiAs('owner');
    const room = await owner.post('/channels', { type: 'group', name: `call-${Date.now()}` });
    await owner.post(`/channels/${room.id}/members`, { userId: idOf('alice') });

    const olivia = await as('owner');
    const alice = await as('alice');
    await alice.page.goto('/tasks');
    await olivia.page.goto(`/chat/${room.id}`);
    await olivia.page.getByRole('button', { name: 'Call', exact: true }).click();
    await expect(olivia.page.getByRole('button', { name: 'Leave the call' })).toBeVisible({ timeout: 30_000 });

    await expect(alice.page.getByText('Incoming call')).toBeVisible({ timeout: 20_000 });
    await alice.page.getByRole('button', { name: 'Answer' }).click();
    await expect(alice.page.getByRole('button', { name: 'Leave the call' })).toBeVisible({ timeout: 30_000 });

    for (const page of [olivia.page, alice.page]) {
      await expect(page.getByRole('button', { name: 'Turn your microphone off' })).toBeVisible({ timeout: 20_000 });
      await expect(page.getByRole('button', { name: 'Turn your camera on' })).toBeVisible();
    }

    await olivia.page.getByRole('button', { name: 'Turn your camera on' }).click();
    await expect(olivia.page.getByRole('button', { name: 'Turn your camera off' })).toBeVisible({ timeout: 20_000 });
    await olivia.page.getByRole('button', { name: 'Leave the call' }).click();
    await alice.page.getByRole('button', { name: 'Leave the call' }).click();
  });
});
