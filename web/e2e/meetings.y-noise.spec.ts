import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { apiAs, createCall, idOf, joinCall, leaveCall, lobbyJoin, prejoinScreen } from './support/meetings';

// Asked for (19 November): noise cancellation on your microphone, on the device
// (RNNoise), on unless you turn it off, from the screen before a call and from
// the microphone's menu in it.

const SHOTS = 'test-results/noise';

const noiseSwitch = (page: Page) => page.getByRole('switch', { name: 'Noise cancellation' });
const micMenu = (page: Page) => page.getByRole('button', { name: 'Choose a microphone' });

/**
 * How loud the sound from the other person is on this page, at its loudest over
 * two seconds: what LiveKit delivered, measured where it is played.
 */
const remoteLevel = (page: Page) =>
  page.evaluate(async () => {
    const streams = Array.from(document.querySelectorAll('audio'))
      .map((el) => el.srcObject as MediaStream | null)
      .filter((s): s is MediaStream => !!s && s.getAudioTracks().some((t) => t.readyState === 'live'));
    if (streams.length === 0) return 0;
    const ctx = new AudioContext();
    const analysers = streams.map((s) => {
      const a = ctx.createAnalyser();
      a.fftSize = 2048;
      ctx.createMediaStreamSource(s).connect(a);
      return a;
    });
    await ctx.resume();
    const samples = new Float32Array(2048);
    let loudest = 0;
    for (let i = 0; i < 40; i++) {
      await new Promise((r) => setTimeout(r, 50));
      for (const a of analysers) {
        a.getFloatTimeDomainData(samples);
        let sum = 0;
        for (const v of samples) sum += v * v;
        loudest = Math.max(loudest, Math.sqrt(sum / samples.length));
      }
    }
    await ctx.close();
    return loudest;
  });

test.describe('noise cancellation', () => {
  test('is on by default before a call, filters the level shown, and is remembered when turned off', async ({ as }) => {
    test.setTimeout(120_000);
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Quiet room ${Date.now()}`, [idOf('alice')]);
    const alice = await as('alice');
    const page = alice.page;
    await page.goto(`/calls/${call.id}`);
    await expect(prejoinScreen(page)).toBeVisible({ timeout: 20_000 });

    await expect(noiseSwitch(page)).toBeChecked();
    await page.getByRole('button', { name: 'Turn your microphone on' }).click();
    // The level is of the microphone after RNNoise, and the fake microphone's speech still moves it.
    await expect(noiseSwitch(page)).toHaveAttribute('data-status', 'on', { timeout: 20_000 });
    const meter = page.getByRole('meter', { name: 'Microphone level' });
    await expect.poll(async () => Number(await meter.getAttribute('data-level')), { timeout: 20_000 }).toBeGreaterThan(0);
    await page.screenshot({ path: `${SHOTS}/prejoin-on.png` });

    await noiseSwitch(page).click();
    await expect(noiseSwitch(page)).not.toBeChecked();
    await expect(noiseSwitch(page)).toHaveAttribute('data-status', 'off');
    await expect.poll(async () => Number(await meter.getAttribute('data-level')), { timeout: 20_000 }).toBeGreaterThan(0);

    // Kept for next time, per person.
    await page.reload();
    await expect(prejoinScreen(page)).toBeVisible({ timeout: 20_000 });
    await expect(noiseSwitch(page)).not.toBeChecked();
    await noiseSwitch(page).click();
    await expect(noiseSwitch(page)).toBeChecked();

    await owner.post(`/calls/${call.id}/end`).catch(() => undefined);
    await owner.dispose();
    expect(alice.problems).toEqual([]);
  });

  test('in a call: on the microphone, through muting and switching it off and on, and the other person still hears you', async ({ as }) => {
    test.setTimeout(180_000);
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Noisy room ${Date.now()}`, [idOf('alice')]);
    const alice = await as('alice');
    await joinCall(alice.page, call.id);

    const olivia = await as('owner');
    const page = olivia.page;
    await page.goto(`/calls/${call.id}`);
    await expect(prejoinScreen(page)).toBeVisible({ timeout: 20_000 });
    await page.getByRole('button', { name: 'Turn your microphone on' }).click();
    await lobbyJoin(page).click();
    await expect(page.getByRole('button', { name: 'Leave the call' }).first()).toBeVisible({ timeout: 30_000 });
    await expect(page.getByRole('button', { name: 'Turn your microphone off' })).toBeVisible({ timeout: 20_000 });

    // RNNoise is on the track that is sent, and Alice hears Olivia's speech through it.
    await expect(micMenu(page)).toHaveAttribute('data-noise-cancellation', 'on', { timeout: 20_000 });
    await expect.poll(() => remoteLevel(alice.page), { timeout: 30_000, message: 'Alice hears Olivia' }).toBeGreaterThan(0.005);

    // Muting and unmuting keeps it.
    await page.getByRole('button', { name: 'Turn your microphone off' }).click();
    await expect(page.getByRole('button', { name: 'Turn your microphone on' })).toBeVisible();
    await page.getByRole('button', { name: 'Turn your microphone on' }).click();
    await expect(page.getByRole('button', { name: 'Turn your microphone off' })).toBeVisible();
    await expect(micMenu(page)).toHaveAttribute('data-noise-cancellation', 'on');
    await expect.poll(() => remoteLevel(alice.page), { timeout: 30_000, message: 'Alice hears Olivia after unmuting' }).toBeGreaterThan(0.005);

    // Off from the microphone's menu, and on again.
    await micMenu(page).click();
    const menu = page.getByRole('menu');
    await expect(menu.getByText('Microphone', { exact: true })).toBeVisible();
    await expect(noiseSwitch(page)).toBeChecked();
    await page.screenshot({ path: `${SHOTS}/call-menu.png` });
    await noiseSwitch(page).click();
    await expect(noiseSwitch(page)).not.toBeChecked();
    await expect(micMenu(page)).toHaveAttribute('data-noise-cancellation', 'off', { timeout: 20_000 });
    await page.keyboard.press('Escape');
    await expect.poll(() => remoteLevel(alice.page), { timeout: 30_000, message: 'Alice hears Olivia without it' }).toBeGreaterThan(0.005);
    await micMenu(page).click();
    await noiseSwitch(page).click();
    await expect(micMenu(page)).toHaveAttribute('data-noise-cancellation', 'on', { timeout: 20_000 });

    // Another microphone: the processor moves to it.
    await expect.poll(() => menu.getByRole('menuitemradio').count()).toBeGreaterThan(1);
    await menu.locator('[role="menuitemradio"][aria-checked="false"]').last().click();
    await expect(menu).toHaveCount(0);
    await expect(micMenu(page)).toHaveAttribute('data-noise-cancellation', 'on', { timeout: 20_000 });
    await expect.poll(() => remoteLevel(alice.page), { timeout: 30_000, message: 'Alice hears Olivia on the other microphone' }).toBeGreaterThan(0.005);

    for (const s of [alice, olivia]) await leaveCall(s.page);
    await owner.dispose();
    expect(alice.problems).toEqual([]);
    expect(olivia.problems).toEqual([]);
  });

  test('leaving while it is still starting leaves no microphone open and no audio running', async ({ as }) => {
    test.setTimeout(120_000);
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Quick exit ${Date.now()}`, [idOf('alice')]);
    const alice = await as('alice');
    const page = alice.page;
    // Every microphone and every 48 kHz audio context the page opens, to see they are let go.
    await alice.context.addInitScript(() => {
      const w = window as unknown as { __mics: MediaStreamTrack[]; __contexts: AudioContext[] };
      w.__mics = [];
      w.__contexts = [];
      const gum = navigator.mediaDevices.getUserMedia.bind(navigator.mediaDevices);
      navigator.mediaDevices.getUserMedia = async (c) => {
        const stream = await gum(c);
        w.__mics.push(...stream.getAudioTracks());
        return stream;
      };
      const Original = window.AudioContext;
      window.AudioContext = class extends Original {
        constructor(options?: AudioContextOptions) {
          super(options);
          if (options?.sampleRate === 48000) w.__contexts.push(this);
        }
      } as typeof AudioContext;
    });
    // The processor's code held back, so leaving happens while it is still on its way.
    const gate: { open?: () => void } = {};
    const opened = new Promise<void>((r) => (gate.open = r));
    await page.route(/\/assets\/rnnoise-[^/]+\.js$/, async (route) => {
      await opened;
      await route.continue();
    });
    await page.goto(`/calls/${call.id}`);
    await expect(prejoinScreen(page)).toBeVisible({ timeout: 20_000 });
    await page.getByRole('button', { name: 'Turn your microphone on' }).click();
    await lobbyJoin(page).click();
    await expect(page.getByRole('button', { name: 'Leave the call' }).first()).toBeVisible({ timeout: 30_000 });
    await expect(micMenu(page)).toHaveAttribute('data-noise-cancellation', 'starting');
    await leaveCall(page);
    await expect(page).toHaveURL(/\/calls$/);
    gate.open!();
    await page.waitForTimeout(3000);
    const left = await page.evaluate(() => {
      const w = window as unknown as { __mics: MediaStreamTrack[]; __contexts: AudioContext[] };
      return { mics: w.__mics.filter((t) => t.readyState === 'live').length, contexts: w.__contexts.filter((c) => c.state !== 'closed').length };
    });
    expect(left).toEqual({ mics: 0, contexts: 0 });
    await owner.post(`/calls/${call.id}/end`).catch(() => undefined);
    await owner.dispose();
    expect(alice.problems).toEqual([]);
  });

  test('the switch on a phone, in light and dark', async ({ as }) => {
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Phone noise ${Date.now()}`, [idOf('alice')]);
    for (const theme of ['light', 'dark'] as const) {
      const s = await as('alice', { phone: 'android', theme });
      await s.page.goto(`/calls/${call.id}`);
      await expect(prejoinScreen(s.page)).toBeVisible({ timeout: 20_000 });
      await noiseSwitch(s.page).scrollIntoViewIfNeeded();
      await expect(noiseSwitch(s.page)).toBeVisible();
      const box = (await noiseSwitch(s.page).boundingBox())!;
      expect(box.x + box.width, 'the switch is within the screen').toBeLessThanOrEqual(390);
      await s.page.screenshot({ path: `${SHOTS}/prejoin-phone-${theme}.png`, fullPage: true });
      await s.context.close();
    }
    await owner.post(`/calls/${call.id}/end`).catch(() => undefined);
    await owner.dispose();
  });
});
