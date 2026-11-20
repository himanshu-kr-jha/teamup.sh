import { writeFileSync } from 'node:fs';
import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { apiAs, createCall, idOf, joinCall } from './support/meetings';
import { PEOPLE } from './support/people';
import { nameplateLayout } from '../src/features/calls/nameplate';

// The owner's request of 17 November 2026: a company background behind
// everybody's camera, like the two Cognecto pictures, each with that person's
// own name and company role in the top right. An administrator puts in the dark
// and light templates once, and everybody chooses one in a call.
//
// The name is drawn into the video before it is sent, so it must read the right
// way round to everybody else. TeamUp does not mirror your own preview either,
// so you read it the right way round too.

const TEMPLATE = (theme: 'dark' | 'light') => new URL(`../../docs/branding/cognecto-call-background-${theme}.png`, import.meta.url);

/** The picture of a person's tile as another person receives it, at the video's own size. */
async function receivedFrame(page: Page, name: string) {
  return page.evaluate((who) => {
    const tile = [...document.querySelectorAll<HTMLElement>('.group')].find(
      (el) => el.querySelector('video') && el.textContent?.includes(who)
    );
    const video = tile?.querySelector<HTMLVideoElement>('video:not([data-lk-local-participant="true"])');
    if (!video || video.videoWidth === 0) return null;
    return { width: video.videoWidth, height: video.videoHeight };
  }, name);
}

/**
 * How a person's video is fitted into their tile, and how much of the picture's
 * width and height can be seen: 1 for all of it, less where covering crops it.
 */
async function shown(page: Page, name: string) {
  return page.evaluate((who) => {
    const tile = [...document.querySelectorAll<HTMLElement>('.group')].find(
      (el) => el.querySelector('video') && el.textContent?.includes(who)
    );
    const video = tile?.querySelector<HTMLVideoElement>('video:not([data-lk-local-participant="true"])');
    if (!video || video.videoWidth === 0 || video.clientWidth === 0) return null;
    const fit = getComputedStyle(video).objectFit;
    const box = video.clientWidth / video.clientHeight;
    const picture = video.videoWidth / video.videoHeight;
    const round = (n: number) => Math.round(n * 1000) / 1000;
    if (fit !== 'cover') return { fit, widthShown: 1, heightShown: 1 };
    return { fit, widthShown: round(Math.min(1, box / picture)), heightShown: round(Math.min(1, picture / box)) };
  }, name);
}

/** Pins a person's tile to the stage, from the button that shows on hover. */
async function pinTile(page: Page, name: string) {
  await page.getByText(name, { exact: false }).first().hover();
  await page.getByRole('button', { name: `Pin ${name}` }).click();
}

/**
 * How closely the difference between the frame received and the bare template,
 * where the name and role should be, follows the text as written and the same
 * text mirrored in place. Text that reads the right way round follows the first
 * far more closely than the second.
 */
async function orientation(page: Page, name: string, template: string, lines: ReturnType<typeof nameplateLayout>) {
  return page.evaluate(
    async ({ who, template, lines }) => {
      const tile = [...document.querySelectorAll<HTMLElement>('.group')].find(
        (el) => el.querySelector('video') && el.textContent?.includes(who)
      )!;
      const video = tile.querySelector<HTMLVideoElement>('video:not([data-lk-local-participant="true"])')!;
      const W = video.videoWidth;
      const H = video.videoHeight;
      const canvas = (w: number, h: number) => {
        const c = document.createElement('canvas');
        c.width = w;
        c.height = h;
        return [c, c.getContext('2d', { willReadFrequently: true })!] as const;
      };
      // What arrived.
      const [frameCanvas, frame] = canvas(W, H);
      frame.drawImage(video, 0, 0, W, H);
      // The bare template, covering the frame as the processor draws it.
      const img = new Image();
      img.src = template;
      await img.decode();
      const [, bare] = canvas(W, H);
      const s = Math.max(W / img.naturalWidth, H / img.naturalHeight);
      bare.drawImage(img, (W - img.naturalWidth * s) / 2, (H - img.naturalHeight * s) / 2, img.naturalWidth * s, img.naturalHeight * s);
      // The text alone, laid out for a 1280-wide picture and scaled to the frame.
      const [, text] = canvas(W, H);
      text.scale(W / 1280, W / 1280);
      text.textAlign = 'right';
      text.textBaseline = 'alphabetic';
      // In the same font the sender drew it in, or the same fallback.
      for (const [family, url] of [['Manrope', '/fonts/manrope-latin.woff2'], ['Space Grotesk', '/fonts/space-grotesk-latin.woff2']]) {
        try {
          document.fonts.add(await new FontFace(family, `url(${url})`, { weight: '400 600' }).load());
        } catch {
          // The system font, as the sender would have had.
        }
      }
      for (const l of lines) {
        text.font = l.font;
        text.letterSpacing = `${l.tracking}px`;
        text.fillStyle = '#fff';
        text.fillText(l.text, l.x, l.y);
      }
      const a = frame.getImageData(0, 0, W, H).data;
      const b = bare.getImageData(0, 0, W, H).data;
      const t = text.getImageData(0, 0, W, H).data;
      // Where the text is, and the box around it.
      let x0 = W, x1 = 0, y0 = H, y1 = 0;
      for (let y = 0; y < H; y++)
        for (let x = 0; x < W; x++)
          if (t[(y * W + x) * 4 + 3] > 128) {
            x0 = Math.min(x0, x); x1 = Math.max(x1, x); y0 = Math.min(y0, y); y1 = Math.max(y1, y);
          }
      // How closely what changed follows the text, and the text mirrored.
      const changed: number[] = [];
      const straight: number[] = [];
      const mirrored: number[] = [];
      for (let y = y0; y <= y1; y++)
        for (let x = x0; x <= x1; x++) {
          const i = (y * W + x) * 4;
          changed.push(Math.abs(a[i] - b[i]) + Math.abs(a[i + 1] - b[i + 1]) + Math.abs(a[i + 2] - b[i + 2]));
          straight.push(t[i + 3]);
          mirrored.push(t[(y * W + (x0 + x1 - x)) * 4 + 3]);
        }
      const correlation = (p: number[], q: number[]) => {
        const n = p.length;
        const mp = p.reduce((s, v) => s + v, 0) / n;
        const mq = q.reduce((s, v) => s + v, 0) / n;
        let pq = 0, pp = 0, qq = 0;
        for (let i = 0; i < n; i++) {
          pq += (p[i] - mp) * (q[i] - mq);
          pp += (p[i] - mp) ** 2;
          qq += (q[i] - mq) ** 2;
        }
        return pp && qq ? pq / Math.sqrt(pp * qq) : 0;
      };
      return {
        straight: correlation(changed, straight),
        mirrored: correlation(changed, mirrored),
        box: [x0, y0, x1, y1],
        size: [W, H],
        snapshot: frameCanvas.toDataURL('image/png'),
      };
    },
    { who: name, template, lines }
  );
}

test('an administrator puts in the company backgrounds, and a person in a call wears one with their own name, the right way round for others', async ({ as }) => {
  test.setTimeout(180_000);
  const owner = await apiAs('owner');
  try {
    // Olivia puts in the two templates on Organization > Appearance.
    // A large window, so Alice's video comes at a size her name can be read at.
    const olivia = await as('owner', { width: 1920, height: 1080 });
    await olivia.page.goto('/organization');
    await olivia.page.getByRole('tab', { name: 'Appearance' }).click();
    const heading = olivia.page.getByText('Company backgrounds', { exact: true });
    await expect(olivia.page.getByText('No dark background')).toBeVisible();
    await olivia.page.getByLabel('Dark background file').setInputFiles(TEMPLATE('dark').pathname);
    await expect(olivia.page.getByText('The dark call background is saved.', { exact: false }).first()).toBeVisible();
    await olivia.page.getByLabel('Light background file').setInputFiles(TEMPLATE('light').pathname);
    await expect(olivia.page.getByText('The light call background is saved.', { exact: false }).first()).toBeVisible();
    await expect(olivia.page.getByRole('img', { name: 'The dark call background' })).toBeVisible();
    await expect(olivia.page.getByRole('img', { name: 'The light call background' })).toBeVisible();
    // A picture of the wrong shape is refused, and says why.
    const square = await squarePng(olivia.page);
    await olivia.page.getByLabel('Light background file').setInputFiles({ name: 'square.png', mimeType: 'image/png', buffer: square });
    await expect(olivia.page.getByRole('alert')).toContainText('16:9');
    await olivia.page.getByText('A 16:9 PNG, JPEG or WebP picture').scrollIntoViewIfNeeded();
    await expect(heading).toBeVisible();
    await olivia.page.waitForTimeout(500);
    await olivia.page.screenshot({ path: 'test-results/brandbg-appearance.png' });

    // Alice says what she does.
    const alice = await as('alice');
    await alice.page.goto('/settings');
    await alice.page.getByLabel('Company role').fill('AI/ML Engineer');
    await alice.page.getByRole('button', { name: 'Save Changes' }).click();
    await expect(alice.page.getByText('Your profile has been saved.').first()).toBeVisible();

    // Both in a call; Alice turns her camera on and chooses Company (dark).
    const call = await createCall(owner, `Company background ${Date.now()}`, [idOf('alice'), idOf('pat')]);
    await joinCall(olivia.page, call.id);
    await joinCall(alice.page, call.id);
    await alice.page.getByRole('button', { name: 'Turn your camera on' }).click();
    const button = alice.page.getByTestId('background-effects');
    await button.click();
    const menu = alice.page.getByRole('menu', { name: 'Background effects' });
    await expect(menu.getByRole('menuitemradio', { name: 'Company (dark)', exact: true })).toBeVisible();
    await expect(menu.getByRole('menuitemradio', { name: 'Company (light)', exact: true })).toBeVisible();
    // The tiles carry her own name: they are made, not the bare template.
    await expect(menu.getByRole('menuitemradio', { name: 'Company (dark)' }).locator('img')).toHaveAttribute('src', /^data:image\/jpeg/);
    await alice.page.waitForTimeout(300);
    await menu.screenshot({ path: 'test-results/brandbg-picker.png' });
    await menu.getByRole('menuitemradio', { name: 'Company (dark)', exact: true }).click();
    await expect(button).toHaveAttribute('aria-pressed', 'true');
    // Choosing keeps the menu open, so the preview shows it; Escape closes it.
    await alice.page.keyboard.press('Escape');
    await expect(menu).toBeHidden();

    // Olivia receives Alice's video with the name and role in the top right,
    // reading the right way round. In the grid her tile is 16:9, so all of it shows.
    await expect.poll(() => receivedFrame(olivia.page, PEOPLE.alice.name), { timeout: 30_000 }).not.toBeNull();
    await expect.poll(async () => (await shown(olivia.page, PEOPLE.alice.name))?.widthShown ?? 0, { timeout: 15_000 }).toBeGreaterThan(0.97);
    // Pinned, her tile fills the stage, whatever its shape, and the full-size video is sent.
    await pinTile(olivia.page, PEOPLE.alice.name);
    // The stage is shown whole, letterboxed, so the sides, and her name, are not cut off.
    await expect.poll(() => shown(olivia.page, PEOPLE.alice.name), { timeout: 15_000 }).toMatchObject({ fit: 'contain', widthShown: 1, heightShown: 1 });
    const template = await owner.get('/org/call-backgrounds');
    const lines = nameplateLayout(1280, 720, PEOPLE.alice.name, 'AI/ML Engineer', 'dark', () => 0);
    let seen: Awaited<ReturnType<typeof orientation>> | null = null;
    await expect
      .poll(
        async () => {
          seen = await orientation(olivia.page, PEOPLE.alice.name, template.dark, lines);
          return seen.straight > 0.5 && seen.straight > seen.mirrored + 0.2;
        },
        { timeout: 45_000, message: 'the name never arrived the right way round' }
      )
      .toBe(true);
    const got = seen!;
    writeFileSync('test-results/brandbg-received-frame.png', Buffer.from(got.snapshot.split(',')[1], 'base64'));
    // In the top right: right of the middle, in the top fifth.
    expect(got.box[0]).toBeGreaterThan(got.size[0] / 2);
    expect(got.box[3]).toBeLessThan(got.size[1] / 5);
    // Alice's own preview is not mirrored, so she reads her name as others do.
    const ownTransform = await alice.page.evaluate(
      () => getComputedStyle(document.querySelector('video[data-lk-local-participant="true"]')!).transform
    );
    expect(ownTransform).toBe('none');
    // The picture as it will be seen: a busy test machine sometimes sends the
    // small copy for a few seconds while the segmenter catches up.
    await expect
      .poll(async () => (await receivedFrame(olivia.page, PEOPLE.alice.name))?.width ?? 0, { timeout: 20_000 })
      .toBeGreaterThanOrEqual(1280);
    await olivia.page.waitForTimeout(500);
    await olivia.page.screenshot({ path: 'test-results/brandbg-olivia-sees-alice.png' });
    await alice.page.screenshot({ path: 'test-results/brandbg-alice-sees-herself.png' });

    // On a phone held upright: Alice's tile in the grid is 16:9 and shows all of
    // her picture, and pinned it is letterboxed rather than cut to the phone's shape.
    const pat = await as('pat', { width: 390, height: 844 });
    await joinCall(pat.page, call.id);
    await expect.poll(async () => (await shown(pat.page, PEOPLE.alice.name))?.widthShown ?? 0, { timeout: 30_000 }).toBeGreaterThan(0.97);
    await pat.page.waitForTimeout(1500);
    await pat.page.screenshot({ path: 'test-results/brandbg-phone-grid.png' });
    await pinTile(pat.page, PEOPLE.alice.name);
    await expect.poll(() => shown(pat.page, PEOPLE.alice.name), { timeout: 15_000 }).toMatchObject({ fit: 'contain', widthShown: 1 });
    await pat.page.waitForTimeout(1500);
    await pat.page.screenshot({ path: 'test-results/brandbg-phone-pinned.png' });
  } finally {
    await owner.del('/org/call-backgrounds/dark').catch(() => {});
    await owner.del('/org/call-backgrounds/light').catch(() => {});
    await owner.patch(`/users/${idOf('alice')}`, { jobTitle: '' }).catch(() => {});
    await owner.dispose();
  }
});

async function squarePng(page: Page): Promise<Buffer> {
  const dataUrl = await page.evaluate(() => {
    const c = document.createElement('canvas');
    c.width = 1440;
    c.height = 1440;
    c.getContext('2d')!.fillRect(0, 0, 1440, 1440);
    return c.toDataURL('image/png');
  });
  return Buffer.from(dataUrl.split(',')[1], 'base64');
}
