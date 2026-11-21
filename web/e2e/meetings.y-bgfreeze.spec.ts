import type { Browser, Page } from '@playwright/test';
import { expect, openSession, test, type Session } from './support/fixtures';
import type { Api } from './support/api';
import { apiAs, createCall, idOf, joinCall, lobbyJoin, prejoinScreen } from './support/meetings';
import { COLOURS, addOrgPictures, removeOrgPictures, type OrgPicture } from './support/orgPictures';

// The owner's report of 18 November 2026: putting a background on with the camera
// on took a long time, and the video stood still meanwhile, for you and for the
// others. Now the camera goes on as it is while the background is made ready,
// and switches over in one step once it is, as Google Meet does.
//
// Measured from the other side of the call: how long Olivia's video, as Alice
// receives it, goes without a new frame. Alice has a browser of her own, as she
// would on her own computer: two pages of one browser share its graphics
// process, and Olivia's setting up would hold Alice's picture up too.

/** Starts recording when each frame of the one video from somebody else is shown. */
async function watchRemoteFrames(page: Page) {
  await expect
    .poll(() => page.evaluate(() => !!document.querySelector<HTMLVideoElement>('video:not([data-lk-local-participant="true"])')?.videoWidth), {
      timeout: 30_000,
    })
    .toBe(true);
  await page.evaluate(() => {
    const w = window as unknown as { __frames: number[] };
    w.__frames = [];
    const video = document.querySelector<HTMLVideoElement>('video:not([data-lk-local-participant="true"])')!;
    const tick = (now: number) => {
      w.__frames.push(performance.timeOrigin + now);
      video.requestVideoFrameCallback(tick);
    };
    video.requestVideoFrameCallback(tick);
  });
}


const clock = (page: Page) => page.evaluate(() => performance.timeOrigin + performance.now());

/**
 * The longest time between two frames since `from`, up to now, and the usual
 * time between them over the last `tail` milliseconds, once the effect is on.
 */
async function gaps(page: Page, from: number, tail = 2_000) {
  return page.evaluate(
    ({ from, tail }) => {
      const end = performance.timeOrigin + performance.now();
      const frames = (window as unknown as { __frames: number[] }).__frames.filter((t) => t >= from);
      let longest = 0;
      for (let i = 1; i < frames.length; i++) longest = Math.max(longest, frames[i] - frames[i - 1]);
      if (frames.length) longest = Math.max(longest, end - frames[frames.length - 1]);
      const late = frames.filter((t) => t >= end - tail);
      const steps = late.slice(1).map((t, i) => t - late[i]).sort((a, b) => a - b);
      const usual = steps.length ? steps[Math.floor(steps.length / 2)] : 0;
      return { longest: Math.round(longest), usual: Math.round(usual), frames: frames.length };
    },
    { from, tail }
  );
}

/** How much the picture changes from one pixel to the next: blur lowers it. */
const detail = (page: Page, selector: string) =>
  page.evaluate((sel) => {
    const video = document.querySelector<HTMLVideoElement>(sel);
    if (!video || !video.videoWidth) return null;
    const c = document.createElement('canvas');
    c.width = 320;
    c.height = 180;
    const g = c.getContext('2d')!;
    g.drawImage(video, 0, 0, 320, 180);
    const { data } = g.getImageData(0, 0, 320, 180);
    let sum = 0;
    for (let i = 4; i < data.length; i += 4) sum += Math.abs(data[i] - data[i - 4]);
    return sum / (data.length / 4);
  }, selector);
const REMOTE = 'video:not([data-lk-local-participant="true"])';

/** The average colour of the top of the picture, where a person would not be. */
const colour = (page: Page, selector: string) =>
  page.evaluate((sel) => {
    const video = document.querySelector<HTMLVideoElement>(sel);
    if (!video || !video.videoWidth) return null;
    const c = document.createElement('canvas');
    c.width = 64;
    c.height = 36;
    const g = c.getContext('2d')!;
    g.drawImage(video, 0, 0, 64, 36);
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
  }, selector);
const isNight = (c: { r: number; g: number; b: number } | null) => !!c && c.r < 45 && c.g < 50 && c.b < 70 && c.b >= c.r;

/** Keeps every copy of a camera made on the page, so a test can see that none is left running. */
async function countTracks(session: Session) {
  await session.context.addInitScript(() => {
    const w = window as unknown as { __clones: MediaStreamTrack[] };
    w.__clones = [];
    const clone = MediaStreamTrack.prototype.clone;
    MediaStreamTrack.prototype.clone = function (this: MediaStreamTrack) {
      const copy = clone.call(this);
      w.__clones.push(copy);
      return copy;
    };
  });
}
const liveTracks = (page: Page) =>
  page.evaluate(() => {
    const w = window as unknown as { __clones: MediaStreamTrack[] };
    return { clones: w.__clones.filter((t) => t.readyState === 'live').length };
  });

/**
 * The longest a frame may take to come during a switch: about two of the
 * processor's own frames. On a test machine, which has no graphics card and runs
 * MediaPipe and the drawing on its processor, the processor puts out a frame every
 * 200 to 300 ms whatever happens (on a computer with one, it keeps up with the
 * camera), so the bound follows it, and is never under 600 ms. A switch measured
 * 230 to 400 ms here, and up to 650 ms with the machine loaded by other work.
 * Before the fix, Alice's video stood still for 1.5 to 1.6 seconds.
 */
const allowed = (usual: number) => Math.max(600, Math.round(usual * 2.5));

test.describe('putting a background on in a call', () => {
  let apart: Browser | undefined;
  let away: Page | undefined;
  // The organization's own picture, the only kind there is, for a picture to go on.
  let admin: Api;
  let pictures: OrgPicture[] = [];
  test.beforeAll(async () => {
    admin = await apiAs('owner');
    pictures = await addOrgPictures(admin, { Night: COLOURS.night });
  });
  test.afterAll(async () => {
    await removeOrgPictures(admin, pictures);
    await admin.dispose();
  });
  test.afterEach(async () => {
    // Hung up first, as a person would, so the media server does not hold her
    // in this call while the next test rings her.
    const leave = away?.getByRole('button', { name: 'Leave the call' }).first();
    if (leave && (await leave.isVisible().catch(() => false))) {
      await leave.click({ timeout: 5_000 }).catch(() => undefined);
      await leave.waitFor({ state: 'detached', timeout: 5_000 }).catch(() => undefined);
    }
    await apart?.close();
    apart = undefined;
    away = undefined;
  });

  /** Alice, in a browser of her own, and Olivia, in a call together with Olivia's camera on. */
  async function inACall(as: (who: 'owner') => Promise<Session>, launch: () => Promise<Browser>) {
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Background switch ${Date.now()}`, [idOf('alice')]);
    apart = await launch();
    const alice = await openSession(apart, 'alice');
    away = alice.page;
    const olivia = await as('owner');
    await countTracks(olivia);
    await joinCall(alice.page, call.id);
    await joinCall(olivia.page, call.id);
    await olivia.page.getByRole('button', { name: 'Turn your camera on' }).click();
    await watchRemoteFrames(alice.page);
    // Settled first: a few seconds of video before anything changes.
    await alice.page.waitForTimeout(3_000);
    return { alice, olivia };
  }

  test('the others never see the video stand still while a background goes on, changes, or comes off', async ({ as, playwright, launchOptions }) => {
    const { alice, olivia } = await inACall(as, () => playwright.chromium.launch(launchOptions));
    const sharp = (await detail(alice.page, REMOTE))!;
    const button = olivia.page.getByTestId('background-effects');
    const menu = olivia.page.getByRole('menu', { name: 'Background effects' });

    // Blur, from nothing: made ready in the preview first, then sent.
    let from = await clock(alice.page);
    await button.click();
    const preview = menu.getByTestId('background-preview');
    await expect(preview).toBeVisible();
    // The preview's own picture before the blur, to compare it with after: the
    // tile in the call is cropped and scaled otherwise, and measured against it
    // a blurred preview could read as sharper than the tile.
    const inPreview = '[data-testid="background-preview"] video';
    await expect.poll(async () => (await detail(olivia.page, inPreview)) ?? 0, { timeout: 15_000 }).toBeGreaterThan(0);
    const sharpHere = (await detail(olivia.page, inPreview))!;
    await menu.getByRole('menuitemradio', { name: 'Blur', exact: true }).click();
    // The menu stays open, and the preview shows the blur.
    await expect(menu.getByRole('menuitemradio', { name: 'Blur', exact: true })).toHaveAttribute('aria-checked', 'true');
    await expect.poll(async () => (await detail(alice.page, REMOTE))! < sharp * 0.7, { timeout: 30_000 }).toBe(true);
    await alice.page.waitForTimeout(2_000);
    const on = await gaps(alice.page, from);
    console.log(`blur on: longest ${on.longest} ms, usual ${on.usual} ms, ${on.frames} frames`);
    expect(on.longest, `Olivia's video stood still for ${on.longest} ms while blur went on`).toBeLessThanOrEqual(allowed(on.usual));
    expect((await detail(olivia.page, inPreview))!).toBeLessThan(sharpHere * 0.85);

    // Blur to a picture: the same processor, changed in place.
    from = await clock(alice.page);
    await menu.getByRole('menuitemradio', { name: 'Night', exact: true }).click();
    await expect.poll(async () => isNight(await colour(alice.page, REMOTE)), { timeout: 30_000 }).toBe(true);
    await alice.page.waitForTimeout(2_000);
    const change = await gaps(alice.page, from);
    console.log(`blur to picture: longest ${change.longest} ms, usual ${change.usual} ms`);
    expect(change.longest, `Olivia's video stood still for ${change.longest} ms while the picture went on`).toBeLessThanOrEqual(
      allowed(change.usual)
    );

    // And off: the camera as it is, handed back to the call before the processor goes.
    from = await clock(alice.page);
    await menu.getByRole('menuitemradio', { name: 'None', exact: true }).click();
    await expect.poll(async () => (await detail(alice.page, REMOTE))! > sharp * 0.85, { timeout: 30_000 }).toBe(true);
    await alice.page.waitForTimeout(2_000);
    const off = await gaps(alice.page, from);
    console.log(`off: longest ${off.longest} ms, usual ${off.usual} ms`);
    // Measured against the pace the video had with the background on: the last
    // frame before the switch comes at that pace, and the plain camera's quicker
    // one after it made a busy machine's ordinary gap (733 ms against 550 ms
    // frames) look like a freeze.
    expect(off.longest, `Olivia's video stood still for ${off.longest} ms while the background came off`).toBeLessThanOrEqual(
      allowed(Math.max(off.usual, change.usual))
    );

    // Closing the picker lets the preview's copy of the camera go, and its processor.
    await olivia.page.keyboard.press('Escape');
    await expect(menu).toBeHidden();
    await expect.poll(() => liveTracks(olivia.page)).toEqual({ clones: 0 });
  });

  test('closing the picker while a background is being made ready still puts it on, and leaves nothing running', async ({
    as,
    playwright,
    launchOptions,
  }) => {
    const { alice, olivia } = await inACall(as, () => playwright.chromium.launch(launchOptions));
    const sharp = (await detail(alice.page, REMOTE))!;
    const from = await clock(alice.page);
    await olivia.page.getByTestId('background-effects').click();
    await olivia.page.getByRole('menuitemradio', { name: 'Blur', exact: true }).click();
    await olivia.page.keyboard.press('Escape');
    await expect(olivia.page.getByRole('menu', { name: 'Background effects' })).toBeHidden();

    await expect.poll(async () => (await detail(alice.page, REMOTE))! < sharp * 0.7, { timeout: 30_000 }).toBe(true);
    await alice.page.waitForTimeout(2_000);
    const on = await gaps(alice.page, from);
    console.log(`blur on, picker closed: longest ${on.longest} ms, usual ${on.usual} ms`);
    expect(on.longest).toBeLessThanOrEqual(allowed(on.usual));
    // Only the processor on the camera itself is left: its copy of the camera, and what it puts out.
    await expect.poll(() => liveTracks(olivia.page)).toEqual({ clones: 1 });

    // Its button says the effect is on, and the call can be left cleanly.
    await expect(olivia.page.getByTestId('background-effects')).toHaveAttribute('aria-pressed', 'true');
  });

  // Seen in the cross-feature tests: with blur chosen on the screen before the
  // call, the preview's camera was stopped on joining with its processor still
  // on it, and Chrome reported frames left unclosed a few seconds later. The
  // fixture fails on any such console error.
  test('blur chosen before joining goes into the call with nothing left behind', async ({ as }) => {
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Blur before joining ${Date.now()}`, [idOf('alice')]);
    const alice = await as('alice');
    await joinCall(alice.page, call.id);

    const olivia = await as('owner');
    await olivia.page.goto(`/calls/${call.id}`);
    await expect(prejoinScreen(olivia.page)).toBeVisible({ timeout: 30_000 });
    const camera = olivia.page.getByRole('button', { name: 'Turn your camera on' });
    if (await camera.isVisible()) await camera.click();
    const button = olivia.page.getByTestId('background-effects');
    await expect(button).toBeEnabled({ timeout: 20_000 });
    await button.click();
    await olivia.page.getByRole('menuitemradio', { name: 'Blur', exact: true }).click();
    await olivia.page.keyboard.press('Escape');
    await expect(button).toHaveAttribute('aria-pressed', 'true');
    await expect(button.locator('.animate-spin')).toHaveCount(0, { timeout: 30_000 });

    await lobbyJoin(olivia.page).click();
    await expect(olivia.page.getByRole('button', { name: 'Leave the call' }).first()).toBeVisible({ timeout: 30_000 });
    await watchRemoteFrames(alice.page);
    // Blurred in the call too.
    await expect(button).toHaveAttribute('aria-pressed', 'true');
    // Long enough for the browser to collect what the preview left, and say so.
    await olivia.page.waitForTimeout(15_000);
    const seen = await gaps(alice.page, (await clock(alice.page)) - 5_000);
    // Still moving. Without a graphics card the background puts out one to three
    // frames a second, so ten in five seconds (asked before) failed on main too.
    expect(seen.frames).toBeGreaterThan(4);
  });

  // Also seen in the cross-feature tests: frames reported unclosed 10 to 20
  // seconds after leaving a call of three in which blur had been turned on.
  test('leaving a call with blur on leaves nothing behind', async ({ as }) => {
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Blur then leave ${Date.now()}`, [idOf('alice'), idOf('pat')]);
    const micOn = async (page: Page) => {
      const mic = page.getByRole('button', { name: 'Turn your microphone on' }).first();
      if (await mic.isVisible()) await mic.click();
    };
    const pat = await as('pat');
    const alice = await as('alice');
    const olivia = await as('owner');
    for (const s of [pat, alice, olivia]) {
      await joinCall(s.page, call.id);
      await micOn(s.page);
    }
    await olivia.page.getByRole('button', { name: 'Turn your camera on' }).click();
    await watchRemoteFrames(alice.page);
    const sharp = (await detail(alice.page, REMOTE))!;
    await olivia.page.getByTestId('background-effects').click();
    await olivia.page.getByRole('menuitemradio', { name: 'Blur', exact: true }).click();
    await expect.poll(async () => (await detail(alice.page, REMOTE))! < sharp * 0.7, { timeout: 30_000 }).toBe(true);
    await olivia.page.keyboard.press('Escape');

    await olivia.page.getByRole('button', { name: 'Leave the call' }).first().click();
    await expect(olivia.page.getByRole('button', { name: 'Leave the call' })).toHaveCount(0, { timeout: 15_000 });
    // Long enough for the browser to collect whatever the call left, and say so.
    await olivia.page.waitForTimeout(30_000);
  });
});
