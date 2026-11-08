import { execFileSync } from 'node:child_process';
import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { apiAs, idOf } from './support/meetings';
import { BASE_URL } from './support/api';
import { readState } from './support/people';

// The status page, the organization's own colour and logo, reading PDFs and
// saving files where they were sent, and the number of unread chats on the
// Chat tab. Against the real stack: real server, database, Redis and MinIO.

const MINIO_CONTAINER = process.env.E2E_MINIO_CONTAINER ?? 'teamup-cal-test-minio';

/** A real two-page PDF, with the cross-reference offsets a reader checks. */
function twoPagePdf(): Buffer {
  const objects = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R 5 0 R] /Count 2 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Contents 4 0 R /Resources << /Font << /F1 7 0 R >> >> >>',
    null,
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Contents 6 0 R /Resources << /Font << /F1 7 0 R >> >> >>',
    null,
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
  ];
  const stream = (text: string) => {
    const body = `BT /F1 24 Tf 40 100 Td (${text}) Tj ET`;
    return `<< /Length ${body.length} >>\nstream\n${body}\nendstream`;
  };
  objects[3] = stream('Page one');
  objects[5] = stream('Page two');
  let out = '%PDF-1.4\n';
  const offsets: number[] = [];
  objects.forEach((o, i) => {
    offsets.push(out.length);
    out += `${i + 1} 0 obj\n${o}\nendobj\n`;
  });
  const xref = out.length;
  out += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`;
  for (const o of offsets) out += `${String(o).padStart(10, '0')} 00000 n \n`;
  out += `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
  return Buffer.from(out, 'latin1');
}

/** A 16 by 16 teal square, as PNG bytes. */
const LOGO_PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAIAAACQkWg2AAAAF0lEQVR4nGMQrZtBEmIY1TCqYVQDBg0AnkbDsaHOsrIAAAAASUVORK5CYII=',
  'base64'
);

async function attachAndSend(page: Page, name: string, mimeType: string, buffer: Buffer) {
  await page.locator('input[type="file"][aria-label="Attach a file"]').setInputFiles({ name, mimeType, buffer });
  await expect(page.getByText(/Attached\. Press Send to share\./)).toBeVisible({ timeout: 30_000 });
  await page.getByRole('button', { name: 'Send', exact: true }).click();
  await expect(page.getByText(/Press Send to share/)).toBeHidden({ timeout: 30_000 });
}

test.describe('files in chat', () => {
  test('a PDF opens to be read in the chat, page by page, and downloads under its own name', async ({ as }) => {
    const olivia = await as('owner');
    const { channelId } = readState();
    await olivia.page.goto(`/chat/${channelId}`);
    const name = `plan-${Date.now()}.pdf`;
    await attachAndSend(olivia.page, name, 'application/pdf', twoPagePdf());

    await olivia.page.getByRole('button', { name: `Open ${name}` }).last().click();
    const viewer = olivia.page.getByRole('dialog', { name });
    await expect(viewer).toBeVisible();
    await expect(viewer.getByText('2 pages')).toBeVisible({ timeout: 30_000 });
    await expect(viewer.getByTestId('pdf-page').first()).toHaveAttribute('data-drawn', 'true', { timeout: 30_000 });
    await expect(viewer.getByTestId('pdf-page')).toHaveCount(2);

    // Something was really drawn, not a white rectangle.
    const inked = await viewer
      .locator('canvas')
      .first()
      .evaluate((c: HTMLCanvasElement) => {
        const d = c.getContext('2d')!.getImageData(0, 0, c.width, c.height).data;
        let dark = 0;
        for (let i = 0; i < d.length; i += 4) if (d[i] < 128) dark++;
        return dark;
      });
    expect(inked).toBeGreaterThan(50);

    // Zooming draws it bigger.
    const before = (await viewer.getByTestId('pdf-page').first().boundingBox())!.width;
    await viewer.getByRole('button', { name: 'Zoom in' }).click();
    await expect.poll(async () => (await viewer.getByTestId('pdf-page').first().boundingBox())!.width).toBeGreaterThan(before * 1.2);

    const saved = olivia.page.waitForEvent('download');
    await viewer.getByRole('button', { name: 'Download' }).click();
    expect((await saved).suggestedFilename()).toBe(name);

    await olivia.page.keyboard.press('Escape');
    await expect(viewer).toBeHidden();

    // And straight from the chat, without opening it.
    const direct = olivia.page.waitForEvent('download');
    await olivia.page.getByRole('button', { name: `Download ${name}` }).last().click();
    expect((await direct).suggestedFilename()).toBe(name);
  });

  test("a message's files can be saved from its menu, and a picture from its own button", async ({ as }) => {
    const olivia = await as('owner');
    const { channelId } = readState();
    await olivia.page.goto(`/chat/${channelId}`);
    const name = `square-${Date.now()}.png`;
    await attachAndSend(olivia.page, name, 'image/png', LOGO_PNG);

    const picture = olivia.page.getByRole('img', { name }).last();
    await expect(picture).toBeVisible({ timeout: 30_000 });
    await picture.hover();
    const fromButton = olivia.page.waitForEvent('download');
    await olivia.page.getByRole('button', { name: `Download ${name}` }).last().click();
    expect((await fromButton).suggestedFilename()).toBe(name);

    // The message's own menu.
    const bubble = olivia.page.locator('div.group', { has: picture }).last();
    await bubble.hover();
    await bubble.getByRole('button', { name: 'Message actions' }).click();
    const fromMenu = olivia.page.waitForEvent('download');
    await olivia.page.getByRole('menuitem', { name: 'Download' }).click();
    expect((await fromMenu).suggestedFilename()).toBe(name);
  });
});

test.describe('unread chats', () => {
  test('the Chat tab counts the conversations with something new, live, wherever you are', async ({ as }) => {
    const owner = await apiAs('owner');
    const aliceId = idOf('alice');
    const stamp = Date.now();
    const rooms: string[] = [];
    for (const n of ['a', 'b']) {
      const room = await owner.post('/channels', { type: 'public', name: `badge-${n}-${stamp}` });
      await owner.post(`/channels/${room.id}/members`, { userId: aliceId });
      rooms.push(room.id);
    }

    const alice = await as('alice');
    const aliceApi = await apiAs('alice');
    const already = ((await aliceApi.get('/channels')).channels as Array<{ unreadCount: number }>).filter(
      (c) => c.unreadCount > 0
    ).length;
    await alice.page.goto('/tasks');
    const badge = alice.page.getByRole('navigation', { name: 'Main' }).getByTestId('chat-unread-badge');
    if (already === 0) await expect(badge).toBeHidden();

    // Three messages in one room and one in the other: two chats, not four messages.
    for (const text of ['one', 'two', 'three']) await owner.post(`/channels/${rooms[0]}/messages`, { content: text });
    await owner.post(`/channels/${rooms[1]}/messages`, { content: 'hello' });
    await expect(badge).toHaveText(String(already + 2));

    // On a phone, along the bottom.
    const phone = await as('alice', { width: 390, height: 844 });
    await phone.page.goto('/tasks');
    await expect(phone.page.getByRole('navigation', { name: 'Sections' }).getByTestId('chat-unread-badge')).toHaveText(
      String(already + 2)
    );

    // Reading one leaves one. Reading only counts in a window in front, as for
    // anybody who opens a conversation in a tab they are not looking at.
    await alice.page.bringToFront();
    await alice.page.goto(`/chat/${rooms[0]}`);
    await expect(alice.page.getByText('three')).toBeVisible();
    await expect(badge).toHaveText(String(already + 1));
  });
});

test.describe('appearance', () => {
  test.afterAll(async () => {
    // Later specs take screenshots of the default look.
    const owner = await apiAs('owner');
    await owner.ctx.put('/api/org/branding', { data: { color: '', logo: '' } });
  });

  test("an administrator gives the app the organization's colour and logo, and everybody sees it, signed in or not", async ({
    as,
    browser,
  }) => {
    const olivia = await as('owner');
    await olivia.page.goto('/organization');
    await olivia.page.getByRole('tab', { name: 'Appearance' }).click();
    await olivia.page.getByRole('radio', { name: 'Teal' }).click();
    await olivia.page.getByLabel('Logo file').setInputFiles({ name: 'logo.png', mimeType: 'image/png', buffer: LOGO_PNG });

    // Nothing changes for anybody until it is saved, the preview shows it first.
    const primary = (page: Page) =>
      page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--primary').trim());
    expect(await primary(olivia.page)).toBe('239 84% 67%');
    const preview = olivia.page.getByRole('group', { name: 'Preview' });
    await expect(preview.getByRole('button', { name: 'Send' })).toHaveCSS('background-color', 'rgb(13, 148, 136)');

    await olivia.page.getByRole('button', { name: 'Save' }).click();
    await expect(olivia.page.getByText('Appearance saved.', { exact: false })).toBeVisible();
    await expect.poll(() => primary(olivia.page)).toBe('174.7 83.9% 31.6%');
    await expect(olivia.page.getByRole('complementary', { name: 'Main navigation' }).getByTestId('brand-logo')).toBeVisible();

    // Somebody signed out sees it on the sign-in page.
    const stranger = await browser.newContext();
    const page = await stranger.newPage();
    await page.goto(`${BASE_URL}/login`);
    await expect(page.getByTestId('brand-logo')).toBeVisible();
    await expect(page.getByRole('button', { name: /^sign in$/i })).toHaveCSS('background-color', 'rgb(13, 148, 136)');

    // And it is already there on the next load, before anything is fetched.
    await page.route('**/api/branding', (route) => route.abort());
    await page.reload();
    expect(await primary(page)).toBe('174.7 83.9% 31.6%');
    await stranger.close();

    // Back to TeamUp's own.
    await olivia.page.getByRole('button', { name: "Back to TeamUp's look" }).click();
    await olivia.page.getByRole('button', { name: 'Save' }).click();
    await expect.poll(() => primary(olivia.page)).toBe('239 84% 67%');
    await expect(olivia.page.getByRole('complementary', { name: 'Main navigation' }).getByTestId('brand-logo')).toHaveCount(0);
  });

  test('a member cannot change it', async () => {
    const alice = await apiAs('alice');
    expect(await alice.status('put', '/org/branding', { color: '#000000' })).toBe(403);
  });
});

test.describe('service status', () => {
  test('anybody can see that everything works, without signing in, and nothing private is on it', async ({ browser }) => {
    const stranger = await browser.newContext();
    const page = await stranger.newPage();
    await page.goto(`${BASE_URL}/status`);
    await expect(page).toHaveURL(/\/status$/);
    await expect(page.getByTestId('status-overall')).toHaveAttribute('data-state', 'operational');
    await expect(page.getByText('Everything is working')).toBeVisible();
    for (const name of ['TeamUp', 'Messages and data', 'Live updates', 'File sharing', 'Calls']) {
      await expect(page.getByRole('listitem').filter({ hasText: name }).first()).toBeVisible();
    }
    const raw = await (await stranger.request.get(`${BASE_URL}/api/status`)).text();
    expect(raw).not.toMatch(/localhost|55463|"message"/);
    await stranger.close();
  });

  test('when file storage stops answering, the page and the app say so, and the outage stays on record', async ({ as }) => {
    test.setTimeout(150_000);
    const olivia = await as('owner');
    await olivia.page.goto('/tasks');
    await expect(olivia.page.getByTestId('outage-banner')).toHaveCount(0);

    execFileSync('docker', ['pause', MINIO_CONTAINER]);
    try {
      const status = await olivia.context.newPage();
      await status.goto('/status');
      await expect
        .poll(
          async () => {
            await status.getByRole('button', { name: 'Check again' }).click();
            return status.getByTestId('status-minio').getAttribute('data-state');
          },
          { timeout: 60_000, intervals: [3_000] }
        )
        .toBe('outage');
      await expect(status.getByTestId('status-overall')).toHaveAttribute('data-state', 'degraded');
      await expect(status.getByText('File sharing is down')).toBeVisible();

      // Inside the app, without anybody having to go looking.
      await olivia.page.reload();
      await expect(olivia.page.getByTestId('outage-banner')).toContainText('Not working right now: File sharing.', {
        timeout: 30_000,
      });
      await olivia.page.getByRole('link', { name: 'See status' }).click();
      await expect(olivia.page).toHaveURL(/\/status$/);
    } finally {
      execFileSync('docker', ['unpause', MINIO_CONTAINER]);
    }

    await expect
      .poll(
        async () => {
          await olivia.page.getByRole('button', { name: 'Check again' }).click();
          return olivia.page.getByTestId('status-minio').getAttribute('data-state');
        },
        { timeout: 60_000, intervals: [3_000] }
      )
      .toBe('operational');
    await expect(olivia.page.getByTestId('status-incidents')).toContainText(/File sharing was down for \d+ min/);
  });
});
