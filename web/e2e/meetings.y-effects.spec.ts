import type { Page } from '@playwright/test';
import { expect, test, type Session } from './support/fixtures';
import { apiAs, createCall, idOf, joinCall, leaveCall } from './support/meetings';

// The sounds of calls and messages, and backgrounds behind your camera.
// Against the real stack, with Chromium's fake camera.

/** Records each recorded sound as it starts (shared/sounds announces them). */
async function listenForSounds(session: Session) {
  await session.context.addInitScript(() => {
    const w = window as unknown as { __sounds: string[] };
    w.__sounds = [];
    window.addEventListener('teamup:sound', (e) => w.__sounds.push((e as CustomEvent<string>).detail));
  });
}
const sounds = (page: Page) => page.evaluate(() => (window as unknown as { __sounds?: string[] }).__sounds ?? []);

/** The average colour of the middle of your own camera's picture, as you see it. */
async function ownCameraColour(page: Page) {
  return page.evaluate(() => {
    const video = document.querySelector<HTMLVideoElement>('video[data-lk-local-participant="true"]');
    if (!video || video.videoWidth === 0) return null;
    const c = document.createElement('canvas');
    c.width = 64;
    c.height = 36;
    const g = c.getContext('2d')!;
    g.drawImage(video, 0, 0, 64, 36);
    // A band across the top, where a person would not be.
    const { data } = g.getImageData(0, 0, 64, 8);
    let r = 0,
      gr = 0,
      b = 0;
    for (let i = 0; i < data.length; i += 4) {
      r += data[i];
      gr += data[i + 1];
      b += data[i + 2];
    }
    const n = data.length / 4;
    return { r: r / n, g: gr / n, b: b / n };
  });
}

test.describe('call and message sounds', () => {
  test('the callee hears the ringtone, the caller the ringback, a message its sound, and leaving the end of the call', async ({ as }) => {
    const owner = await apiAs('owner');
    const room = await owner.post('/channels', { type: 'group', name: `sounds-${Date.now()}` });
    await owner.post(`/channels/${room.id}/members`, { userId: idOf('alice') });

    const olivia = await as('owner');
    const alice = await as('alice');
    await listenForSounds(olivia);
    await listenForSounds(alice);
    await alice.page.goto('/tasks');
    await olivia.page.goto(`/chat/${room.id}`);

    // A message from somebody else makes its sound.
    await owner.post(`/channels/${room.id}/messages`, { content: `hello ${Date.now()}` });
    await expect.poll(() => sounds(alice.page), { timeout: 15_000 }).toContain('notification');

    await olivia.page.getByRole('button', { name: 'Call', exact: true }).click();
    await expect(alice.page.getByText('Incoming call')).toBeVisible({ timeout: 20_000 });
    // By name, not by email address.
    await expect(alice.page.getByText('Olivia Owner is calling.')).toBeVisible();
    await expect(alice.page.getByRole('dialog')).not.toContainText('@');
    await expect.poll(() => sounds(alice.page), { timeout: 15_000 }).toContain('ringtone');
    await expect(olivia.page.getByText('Ringing…')).toBeVisible({ timeout: 20_000 });
    await expect.poll(() => sounds(olivia.page), { timeout: 15_000 }).toContain('ringback');
    // The caller's own call is not a message to them, nor a ring.
    expect(await sounds(olivia.page)).not.toContain('ringtone');

    await alice.page.getByRole('button', { name: 'Answer' }).click();
    await expect(alice.page.getByRole('button', { name: 'Leave the call' })).toBeVisible({ timeout: 30_000 });
    // Olivia sees her come in, and the ringing stops.
    await expect(olivia.page.getByText('Ringing\u2026')).toBeHidden({ timeout: 20_000 });
    expect(await sounds(olivia.page)).not.toContain('call-ended');

    // Olivia leaves while Alice is still there, so she only leaves; the call
    // itself ends when Alice does, which is not what this is about.
    await leaveCall(olivia.page);
    await expect.poll(() => sounds(olivia.page), { timeout: 10_000 }).toContain('call-ended');
    expect(await sounds(alice.page)).not.toContain('call-ended');
  });
});

test.describe('the bell in a conversation', () => {
  test('chooses what a conversation interrupts you with: every message, only mentions of you, or nothing', async ({ as }) => {
    const owner = await apiAs('owner');
    const room = await owner.post('/channels', { type: 'group', name: `bell-${Date.now()}` });
    await owner.post(`/channels/${room.id}/members`, { userId: idOf('alice') });
    const alice = await as('alice');
    await listenForSounds(alice);
    await alice.page.goto('/tasks');
    await alice.page.goto(`/chat/${room.id}`);
    const bell = alice.page.getByTestId('notify-menu');
    await expect(bell).toHaveAttribute('aria-label', 'Notifications: All messages');

    // Somewhere else in the app, so a message here is news.
    const heard = async (text: string) => {
      const before = (await sounds(alice.page)).filter((s) => s === 'notification').length;
      await owner.post(`/channels/${room.id}/messages`, { content: text });
      await alice.page.waitForTimeout(2500);
      return (await sounds(alice.page)).filter((s) => s === 'notification').length > before;
    };

    await bell.click();
    await alice.page.getByRole('menuitemradio', { name: /Only @mentions/ }).click();
    await expect(alice.page.getByText('You will only be notified when somebody mentions you here.').first()).toBeVisible();
    await expect(bell).toHaveAttribute('aria-label', 'Notifications: Only @mentions');
    await alice.page.goto('/tasks');
    expect(await heard(`just news ${Date.now()}`)).toBe(false);
    expect(await heard(`@alice a look? ${Date.now()}`)).toBe(true);

    await alice.page.goto(`/chat/${room.id}`);
    await bell.click();
    await alice.page.getByRole('menuitemradio', { name: /Nothing/ }).click();
    await expect(bell).toHaveAttribute('aria-label', 'Notifications: Nothing');
    await alice.page.goto('/tasks');
    expect(await heard(`@alice again ${Date.now()}`)).toBe(false);
    // The unread count is kept whatever you choose.
    await expect(alice.page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: /Chat/ })).toContainText(/\d/);

    // Kept by the server: it is still Nothing after a reload.
    await alice.page.goto(`/chat/${room.id}`);
    await expect(bell).toHaveAttribute('aria-label', 'Notifications: Nothing');
  });

  // Seen in CI: a message that arrived just after a page loaded, before the list of
  // conversations had, chimed in a conversation set to Only @mentions. Without the
  // list the page did not know the choice, and took it to be All messages.
  test('a quiet conversation stays quiet in the moments after a page loads', async ({ as }) => {
    const owner = await apiAs('owner');
    const room = await owner.post('/channels', { type: 'group', name: `hush-${Date.now()}` });
    await owner.post(`/channels/${room.id}/members`, { userId: idOf('alice') });
    const alice = await as('alice');
    await alice.page.goto(`/chat/${room.id}`);
    const bell = alice.page.getByTestId('notify-menu');
    await bell.click();
    await alice.page.getByRole('menuitemradio', { name: /Only @mentions/ }).click();
    await expect(alice.page.getByText('You will only be notified when somebody mentions you here.').first()).toBeVisible();

    // A slow network: the list of conversations takes a few seconds after a load.
    let slow = true;
    await alice.page.route(/\/api\/channels(\?.*)?$/, async (route) => {
      if (slow) await new Promise((r) => setTimeout(r, 4_000));
      await route.continue();
    });
    await listenForSounds(alice);
    await alice.page.goto('/tasks');
    // Signed in and connected, with the list still on its way.
    await alice.page.waitForTimeout(1_500);
    await owner.post(`/channels/${room.id}/messages`, { content: `just news ${Date.now()}` });
    await alice.page.waitForTimeout(5_000);
    expect(await sounds(alice.page), 'a quiet conversation chimed').not.toContain('notification');

    // And a mention still gets through.
    slow = false;
    await owner.post(`/channels/${room.id}/messages`, { content: `@alice a look? ${Date.now()}` });
    await expect.poll(() => sounds(alice.page), { timeout: 10_000 }).toContain('notification');
  });
});

test.describe('backgrounds', () => {
  test('a picture replaces what is behind you, from TeamUp itself, and is remembered for the next call', async ({ as }) => {
    const owner = await apiAs('owner');
    const olivia = await as('owner');
    const fetched: string[] = [];
    olivia.page.on('request', (r) => fetched.push(r.url()));

    const first = await createCall(owner, `Backgrounds ${Date.now()}`);
    await joinCall(olivia.page, first.id);
    await olivia.page.getByRole('button', { name: 'Turn your camera on' }).click();
    await expect.poll(() => ownCameraColour(olivia.page), { timeout: 20_000 }).not.toBeNull();
    const plain = (await ownCameraColour(olivia.page))!;

    const button = olivia.page.getByTestId('background-effects');
    await expect(button).toHaveAttribute('aria-pressed', 'false');
    await button.click();
    const menu = olivia.page.getByRole('menu', { name: 'Background effects' });
    await expect(menu).toBeVisible();
    for (const name of ['None', 'Slight blur', 'Blur', 'Studio', 'Indigo', 'Dawn', 'Ocean', 'Forest', 'Night', 'Add a picture']) {
      await expect(menu.getByRole('menuitemradio', { name, exact: true })).toBeVisible();
    }
    await expect(menu.getByRole('menuitemradio', { name: 'None', exact: true })).toHaveAttribute('aria-checked', 'true');
    await menu.getByRole('menuitemradio', { name: 'Night', exact: true }).click();
    await expect(menu).toBeHidden();
    await expect(button).toHaveAttribute('aria-pressed', 'true');

    // Night is a dark blue-grey: the top of the picture turns to it.
    await expect
      .poll(
        async () => {
          const c = await ownCameraColour(olivia.page);
          return !!c && c.r < 45 && c.g < 50 && c.b < 70 && c.b >= c.r;
        },
        { timeout: 30_000, message: `the camera's background never became Night (it was ${JSON.stringify(plain)})` }
      )
      .toBe(true);
    await expect(olivia.page.getByText('could not be changed')).toHaveCount(0);

    // Its runtime and model came from TeamUp, not from a CDN.
    expect(fetched.some((u) => /\/mediapipe\/tasks-vision-[\d.]+\/wasm\/vision_wasm/.test(u))).toBe(true);
    expect(fetched.some((u) => u.endsWith('/mediapipe/selfie_segmenter.tflite'))).toBe(true);
    expect(fetched.filter((u) => /jsdelivr|googleapis|unpkg/.test(u))).toEqual([]);

    // Remembered: the next call starts with it.
    await leaveCall(olivia.page);
    const second = await createCall(owner, `Backgrounds again ${Date.now()}`);
    await joinCall(olivia.page, second.id);
    await olivia.page.getByRole('button', { name: 'Turn your camera on' }).click();
    await expect(button).toHaveAttribute('aria-pressed', 'true');
    await expect
      .poll(async () => {
        const c = await ownCameraColour(olivia.page);
        return !!c && c.r < 45 && c.g < 50 && c.b < 70;
      }, { timeout: 30_000 })
      .toBe(true);

    // Your own picture: a bright red one, added from a file.
    const red = await redPng(olivia.page);
    await button.click();
    const chooser = olivia.page.waitForEvent('filechooser');
    await menu.getByRole('menuitemradio', { name: 'Add a picture' }).click();
    await (await chooser).setFiles({ name: 'red.png', mimeType: 'image/png', buffer: red });
    await expect
      .poll(async () => {
        const c = await ownCameraColour(olivia.page);
        return !!c && c.r > 150 && c.g < 80 && c.b < 80;
      }, { timeout: 30_000 })
      .toBe(true);
    await button.click();
    await expect(menu.getByRole('menuitemradio', { name: 'Your picture', exact: true })).toHaveAttribute('aria-checked', 'true');
    await expect(menu.getByRole('menuitemradio', { name: 'Change your picture' })).toBeVisible();

    // And off again: the camera as it is.
    await menu.getByRole('menuitemradio', { name: 'None', exact: true }).click();
    await expect(button).toHaveAttribute('aria-pressed', 'false');
    await expect
      .poll(async () => {
        const c = await ownCameraColour(olivia.page);
        return !!c && Math.abs(c.r - plain.r) + Math.abs(c.g - plain.g) + Math.abs(c.b - plain.b) < 60;
      }, { timeout: 30_000 })
      .toBe(true);
  });

  test('a background chosen while the camera is off is on as soon as it comes back on, without turning it off and on again', async ({ as }) => {
    const owner = await apiAs('owner');
    const olivia = await as('owner');
    const call = await createCall(owner, `Camera off ${Date.now()}`);
    await joinCall(olivia.page, call.id);
    await olivia.page.getByRole('button', { name: 'Turn your camera on' }).click();
    await expect.poll(() => ownCameraColour(olivia.page), { timeout: 20_000 }).not.toBeNull();
    await olivia.page.getByRole('button', { name: 'Turn your camera off' }).click();
    await expect(olivia.page.getByRole('button', { name: 'Turn your camera on' })).toBeVisible();

    await olivia.page.getByTestId('background-effects').click();
    await olivia.page.getByRole('menuitemradio', { name: 'Night', exact: true }).click();
    await olivia.page.getByRole('button', { name: 'Turn your camera on' }).click();
    await expect
      .poll(async () => {
        const c = await ownCameraColour(olivia.page);
        return !!c && c.r < 45 && c.g < 50 && c.b < 70 && c.b > c.r + 8;
      }, { timeout: 30_000 })
      .toBe(true);
    // Then changed while it is on: straight away, in place.
    await olivia.page.getByTestId('background-effects').click();
    await olivia.page.getByRole('menuitemradio', { name: 'Dawn', exact: true }).click();
    await expect
      .poll(async () => {
        const c = await ownCameraColour(olivia.page);
        return !!c && c.r > 200 && c.g > 160;
      }, { timeout: 15_000 })
      .toBe(true);
  });

  test('a colour filter changes the picture, alone or with a background', async ({ as }) => {
    const owner = await apiAs('owner');
    const olivia = await as('owner');
    const call = await createCall(owner, `Colour ${Date.now()}`);
    await joinCall(olivia.page, call.id);
    await olivia.page.getByRole('button', { name: 'Turn your camera on' }).click();
    await expect.poll(() => ownCameraColour(olivia.page), { timeout: 20_000 }).not.toBeNull();

    // The fake camera is green; black and white leaves no colour in it.
    const button = olivia.page.getByTestId('background-effects');
    await button.click();
    const menu = olivia.page.getByRole('menu', { name: 'Background effects' });
    for (const name of ['Natural', 'Warm', 'Cool', 'Black & white', 'Vintage', 'Bright', 'Vivid']) {
      await expect(menu.getByRole('menuitemradio', { name, exact: true })).toBeVisible();
    }
    await expect(menu.getByRole('menuitemradio', { name: 'Natural', exact: true })).toHaveAttribute('aria-checked', 'true');
    await menu.getByRole('menuitemradio', { name: 'Black & white', exact: true }).click();
    await expect(button).toHaveAttribute('aria-pressed', 'true');
    const grey = async () => {
      const c = await ownCameraColour(olivia.page);
      return !!c && Math.abs(c.r - c.g) < 6 && Math.abs(c.g - c.b) < 6;
    };
    await expect.poll(grey, { timeout: 30_000 }).toBe(true);

    // With a background too: Dawn, in black and white, is a light grey.
    await button.click();
    await menu.getByRole('menuitemradio', { name: 'Dawn', exact: true }).click();
    await expect
      .poll(async () => {
        const c = await ownCameraColour(olivia.page);
        return !!c && c.r > 150 && Math.abs(c.r - c.b) < 8;
      }, { timeout: 30_000 })
      .toBe(true);

    // Remembered for the next call, like the background.
    expect(await olivia.page.evaluate(() => localStorage.getItem('teamup.call.look'))).toBe('mono');
  });

  test('blur keeps the room but softens it', async ({ as }) => {
    const owner = await apiAs('owner');
    const olivia = await as('owner');
    const call = await createCall(owner, `Blur ${Date.now()}`);
    await joinCall(olivia.page, call.id);
    await olivia.page.getByRole('button', { name: 'Turn your camera on' }).click();
    await expect.poll(() => ownCameraColour(olivia.page), { timeout: 20_000 }).not.toBeNull();

    // How much the picture changes from one pixel to the next: blur lowers it.
    const detail = () =>
      olivia.page.evaluate(() => {
        const video = document.querySelector<HTMLVideoElement>('video[data-lk-local-participant="true"]')!;
        const c = document.createElement('canvas');
        c.width = 320;
        c.height = 180;
        const g = c.getContext('2d')!;
        g.drawImage(video, 0, 0, 320, 180);
        const { data } = g.getImageData(0, 0, 320, 180);
        let sum = 0;
        for (let i = 4; i < data.length; i += 4) sum += Math.abs(data[i] - data[i - 4]);
        return sum / (data.length / 4);
      });
    const sharp = await detail();
    await olivia.page.getByTestId('background-effects').click();
    await olivia.page.getByRole('menuitemradio', { name: 'Blur', exact: true }).click();
    await expect.poll(detail, { timeout: 30_000 }).toBeLessThan(sharp * 0.7);
    await expect(olivia.page.getByText('could not be changed')).toHaveCount(0);
  });
});

async function redPng(page: Page): Promise<Buffer> {
  const dataUrl = await page.evaluate(() => {
    const c = document.createElement('canvas');
    c.width = 320;
    c.height = 180;
    const g = c.getContext('2d')!;
    g.fillStyle = '#dc2626';
    g.fillRect(0, 0, 320, 180);
    return c.toDataURL('image/png');
  });
  return Buffer.from(dataUrl.split(',')[1], 'base64');
}
