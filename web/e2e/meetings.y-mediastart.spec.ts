import type { Page } from '@playwright/test';
import { expect, test, type Session } from './support/fixtures';
import { apiAs, createCall, idOf, joinCall, lobbyJoin, prejoinScreen } from './support/meetings';

// The owner's report of 20 November 2026: with a background chosen in an earlier
// call, the camera took a while to start in the next one, and the microphone
// too, which he put down to noise cancellation.
//
// Measured where it matters, on the joiner's own connection: from pressing Join
// to the first video frame going out, and to the first sound going out. And,
// since a background is chosen to hide the room, that no frame of the room as it
// is ever leaves: every video sender is watched for the camera's own track.
//
// Before the fix, measured on a test machine without a graphics card, with blur
// chosen: 8 to 20 frames of the room as it is went out at every join, while the
// background was set up again for each call; the preview before a second call
// took as long as before the first (4 to 8 s); and the microphone's noise
// cancellation came on 7 s after Join, as MediaPipe held the page. The numbers
// for each run are logged, and are in the pull request.

/** Instruments a page: every peer connection, which video tracks are the camera's own, and when each first sends. */
async function instrument(session: Session) {
  await session.context.addInitScript(() => {
    const w = window as unknown as {
      __pcs: RTCPeerConnection[];
      __raw: WeakSet<MediaStreamTrack>;
      __media: Media;
    };
    type Media = {
      video?: number;
      audio?: number;
      rawSent: number;
      rawFrames: number;
      framesAt: [number, number][];
      gumVideo?: number;
      gumAudio?: number;
      connected?: number;
      busy: number;
      longest: number;
      busyAtVideo?: number;
      /** WebGL contexts made on an OffscreenCanvas: each setting up of the background makes one, and nothing else does. */
      gl: number;
      /** The widest video layer going out, at the last look. */
      width: number;
    };
    // Room for every download of a long session, for downloads() to read.
    performance.setResourceTimingBufferSize(5000);
    w.__pcs = [];
    w.__raw = new WeakSet();
    w.__media = { rawSent: 0, rawFrames: 0, framesAt: [], busy: 0, longest: 0, gl: 0, width: 0 };
    // The processor draws on an OffscreenCanvas of its own, through WebGL, and
    // MediaPipe asks the same canvas again; only a new context is counted. (The
    // check for whether the browser can, on a plain canvas, is not.)
    const seen = new WeakSet<object>();
    const getContext = OffscreenCanvas.prototype.getContext as (this: OffscreenCanvas, type: string, ...rest: unknown[]) => unknown;
    (OffscreenCanvas.prototype as unknown as { getContext: typeof getContext }).getContext = function (
      this: OffscreenCanvas,
      type: string,
      ...rest: unknown[]
    ) {
      const made = getContext.call(this, type, ...rest);
      if (made && typeof made === 'object' && type.startsWith('webgl') && !seen.has(made)) {
        seen.add(made);
        w.__media.gl++;
      }
      return made;
    };
    const now = () => performance.timeOrigin + performance.now();
    const Original = window.RTCPeerConnection;
    window.RTCPeerConnection = class extends Original {
      constructor(config?: RTCConfiguration) {
        super(config);
        w.__pcs.push(this);
        this.addEventListener('connectionstatechange', () => {
          if (this.connectionState === 'connected') w.__media.connected ??= now();
        });
      }
    } as typeof RTCPeerConnection;
    const gum = navigator.mediaDevices.getUserMedia.bind(navigator.mediaDevices);
    navigator.mediaDevices.getUserMedia = async (c) => {
      const stream = await gum(c);
      stream.getVideoTracks().forEach((t) => w.__raw.add(t));
      if (stream.getVideoTracks().length) w.__media.gumVideo ??= now();
      if (stream.getAudioTracks().length) w.__media.gumAudio ??= now();
      return stream;
    };
    // How long the page was too busy to do anything else: every task over 50 ms.
    try {
      new PerformanceObserver((list) => {
        for (const e of list.getEntries()) {
          w.__media.busy += e.duration;
          w.__media.longest = Math.max(w.__media.longest, e.duration);
        }
      }).observe({ type: 'longtask', buffered: false });
    } catch {
      // No long tasks in this browser.
    }
    const clone = MediaStreamTrack.prototype.clone;
    MediaStreamTrack.prototype.clone = function (this: MediaStreamTrack) {
      const copy = clone.call(this);
      if (w.__raw.has(this)) w.__raw.add(copy);
      return copy;
    };
    // Frames sent, per sender, at the last look; a raise while the camera's own
    // track is on that sender is the room going out as it is.
    const last = new Map<string, number>();
    const look = async () => {
      let widest = 0;
      for (const pc of w.__pcs) {
        if (pc.connectionState === 'closed') continue;
        for (const sender of pc.getSenders()) {
          const track = sender.track;
          if (!track) continue;
          let stats: RTCStatsReport;
          try {
            stats = await sender.getStats();
          } catch {
            continue;
          }
          stats.forEach((s) => {
            if (s.type !== 'outbound-rtp') return;
            const key = `${s.id}`;
            if (s.kind === 'video') {
              const sent = (s.framesSent as number) ?? 0;
              widest = Math.max(widest, (s.frameWidth as number) ?? 0);
              const before = last.get(key) ?? 0;
              if (sent > before) {
                if (w.__media.video === undefined) {
                  w.__media.video = now();
                  w.__media.busyAtVideo = w.__media.busy;
                }
                if (w.__raw.has(track)) {
                  w.__media.rawSent++;
                  w.__media.rawFrames += sent - before;
                }
              }
              last.set(key, sent);
            } else if (s.kind === 'audio' && ((s.packetsSent as number) ?? 0) > 0) {
              w.__media.audio ??= now();
            }
          });
        }
      }
      if (widest) w.__media.width = widest;
      setTimeout(look, 30);
    };
    void look();
  });
}

const clock = (page: Page) => page.evaluate(() => performance.timeOrigin + performance.now());
interface Media {
  video?: number;
  audio?: number;
  rawSent: number;
  rawFrames: number;
  gumVideo?: number;
  gumAudio?: number;
  connected?: number;
  busy: number;
  longest: number;
  busyAtVideo?: number;
  gl: number;
  width: number;
}
const media = (page: Page) => page.evaluate(() => (window as unknown as { __media: Media }).__media);
const resetMedia = (page: Page) =>
  page.evaluate(() => {
    const m = (window as unknown as { __media: Media }).__media;
    for (const k of ['video', 'audio', 'gumVideo', 'gumAudio', 'connected', 'busyAtVideo'] as const) delete m[k];
    m.rawSent = 0;
    m.rawFrames = 0;
    m.busy = 0;
    m.longest = 0;
  });

/** How a page's downloads of the background's and noise cancellation's files went: from the cache or not, and how long. */
const downloads = (page: Page) =>
  page.evaluate(() =>
    (performance.getEntriesByType('resource') as PerformanceResourceTiming[])
      .filter((e) => /mediapipe|rnnoise|tflite|track-processors|cameraProcessor/i.test(e.name))
      .map((e) => `${e.name.replace(location.origin, '').slice(0, 70)} ${Math.round(e.duration)}ms ${e.transferSize ? `${Math.round(e.transferSize / 1024)}KB` : 'cache'}`)
  );

/** Your background, noise cancellation and the camera and microphone on, as somebody who has been in calls before would have them. */
async function rememberChoices(page: Page, background: boolean, noise: boolean) {
  await page.evaluate(
    async ({ background, noise }) => {
      const me = await fetch('/api/auth/me', { credentials: 'include' }).then((r) => r.json());
      const id = me.user?.id ?? me.id;
      localStorage.setItem(`teamup.call.join.${id}`, JSON.stringify({ mic: true, camera: true }));
      localStorage.setItem(`teamup.call.noiseCancellation.${id}`, noise ? 'on' : 'off');
      if (background) localStorage.setItem('teamup.call.background', JSON.stringify({ kind: 'blur', strength: 'strong' }));
      else localStorage.removeItem('teamup.call.background');
    },
    { background, noise }
  );
}

/**
 * E2E_MEASURE_ONLY=1 logs the numbers without checking them, to measure a build
 * that would fail, such as the one before the fix.
 */
const measureOnly = !!process.env.E2E_MEASURE_ONLY;
const check = measureOnly ? () => undefined : (run: () => void) => run();

const micMenu = (page: Page) => page.getByRole('button', { name: 'Choose a microphone' });

/** What the other side has received of the sound, and how much of it the browser had to make up for a gap. */
const heard = (page: Page) =>
  page.evaluate(async () => {
    const pcs = (window as unknown as { __pcs: RTCPeerConnection[] }).__pcs;
    let total = 0;
    let concealed = 0;
    for (const pc of pcs) {
      if (pc.connectionState === 'closed') continue;
      (await pc.getStats()).forEach((r) => {
        if (r.type !== 'inbound-rtp' || r.kind !== 'audio') return;
        total += (r.totalSamplesReceived as number) ?? 0;
        concealed += (r.concealedSamples as number) ?? 0;
      });
    }
    return { total, concealed };
  });

interface Timing {
  label: string;
  /** From the screen before the call opening to Join being pressed. */
  prejoin: number;
  /** From the screen opening to your camera's preview showing, background and all. */
  preview: number;
  /** From the screen opening to the microphone's level moving. */
  meter: number;
  video: number;
  audio: number;
  noise: number | null;
  rawFrames: number;
  /**
   * Of the sound the other side received from the first of it until noise
   * cancellation had been on for a while, the share the browser had to make up
   * for a gap: switching to RNNoise must not leave one.
   */
  gap: number | null;
  /** From Join to: the camera and microphone opened, the connection up. */
  gumVideo: number;
  gumAudio: number;
  connected: number;
  /** How many times the background was set up, from the screen before the call opening until joined. */
  setups: number;
  /** How long, between Join and the first video frame, the page was too busy for anything else. */
  busy: number;
  longest: number;
}

/**
 * Opens the call's screen, waits until the preview shows, presses Join, and
 * measures from there. `spa` moves within the page, as following a link in
 * TeamUp does, rather than loading it again.
 */
async function measureJoin(
  page: Page,
  listener: Page,
  callId: string,
  label: string,
  opts: { spa: boolean; noise: boolean }
): Promise<Timing> {
  const opened = await clock(page);
  const glBefore = (await media(page)).gl;
  if (opts.spa) {
    await page.evaluate((id) => {
      history.pushState({}, '', `/calls/${id}`);
      dispatchEvent(new PopStateEvent('popstate'));
    }, callId);
  } else {
    await page.goto(`/calls/${callId}`);
  }
  await expect(prejoinScreen(page)).toBeVisible({ timeout: 30_000 });
  // As a person does: looks at themselves first, then joins. How long the
  // preview and the microphone's level each took to show is noted too.
  const [preview, meter] = await Promise.all([
    (async () => {
      await expect(page.getByTestId('prejoin-preview')).toBeVisible({ timeout: 30_000 });
      await expect(page.getByTestId('background-effects').locator('.animate-spin')).toHaveCount(0, { timeout: 30_000 });
      return Math.round((await clock(page)) - opened);
    })(),
    (async () => {
      const level = page.getByRole('meter', { name: 'Microphone level' });
      await expect.poll(async () => Number(await level.getAttribute('data-level')), { timeout: 30_000, intervals: [50] }).toBeGreaterThan(0);
      return Math.round((await clock(page)) - opened);
    })(),
  ]);
  await resetMedia(page);
  const pressed = await clock(page);
  await lobbyJoin(page).click();
  await expect.poll(async () => (await media(page)).video ?? null, { timeout: 30_000, message: `${label}: video sent` }).not.toBeNull();
  await expect.poll(async () => (await media(page)).audio ?? null, { timeout: 30_000, message: `${label}: sound sent` }).not.toBeNull();
  // From the first of the sound reaching the other side.
  await expect.poll(async () => (await heard(listener)).total, { timeout: 30_000, message: `${label}: sound heard` }).toBeGreaterThan(0);
  const before = await heard(listener);
  let noise: number | null = null;
  if (opts.noise) {
    await expect(micMenu(page)).toHaveAttribute('data-noise-cancellation', 'on', { timeout: 30_000 });
    noise = Math.round((await clock(page)) - pressed);
  }
  // A moment longer, for any room frame that would go out late.
  await page.waitForTimeout(1_500);
  const after = await heard(listener);
  const gap = after.total > before.total ? (after.concealed - before.concealed) / (after.total - before.total) : null;
  const m = await media(page);
  return {
    label,
    prejoin: Math.round(pressed - opened),
    preview,
    meter,
    video: Math.round(m.video! - pressed),
    audio: Math.round(m.audio! - pressed),
    noise,
    rawFrames: m.rawFrames,
    setups: m.gl - glBefore,
    gap: gap === null ? null : Math.round(gap * 1000) / 1000,
    gumVideo: Math.round((m.gumVideo ?? NaN) - pressed),
    gumAudio: Math.round((m.gumAudio ?? NaN) - pressed),
    connected: Math.round((m.connected ?? NaN) - pressed),
    busy: Math.round(m.busyAtVideo ?? 0),
    longest: Math.round(m.longest),
  };
}

/** Hangs up, unless the call has already ended for this page, as it does for the last one in it. */
async function leave(page: Page) {
  const button = page.getByRole('button', { name: 'Leave the call' }).first();
  if (await button.isVisible().catch(() => false)) await button.click({ timeout: 20_000 }).catch(() => undefined);
  await expect(page.getByRole('button', { name: 'Leave the call' })).toHaveCount(0, { timeout: 20_000 });
}

test.describe('starting the camera and microphone in a call', () => {
  test.describe.configure({ timeout: 420_000 });

  for (const scenario of [
    { background: true, noise: true },
    { background: true, noise: false },
    { background: false, noise: true },
    { background: false, noise: false },
  ]) {
    const name = `${scenario.background ? 'blur' : 'no background'}, noise cancellation ${scenario.noise ? 'on' : 'off'}`;
    const promise = scenario.background ? 'the room is never sent, and the background is kept for the next call' : 'the camera and sound start';
    test(`${name}: ${promise}, in a first and a second call, cold and warm`, async ({ as }) => {
      const owner = await apiAs('owner');
      const alice = await as('alice');
      const olivia = await as('owner');
      await instrument(olivia);
      await instrument(alice);
      // The init script runs from the next page on.
      await olivia.page.reload();
      await alice.page.reload();
      await rememberChoices(olivia.page, scenario.background, scenario.noise);
      const results: Timing[] = [];
      const calls: string[] = [];
      for (const [i, how] of (['first call, cold cache', 'second call, same tab', 'first call after reload, warm cache'] as const).entries()) {
        const call = await createCall(owner, `Media start ${i} ${Date.now()}`, [idOf('alice')]);
        calls.push(call.id);
        await joinCall(alice.page, call.id);
        if (i === 2) await olivia.page.goto('/chat');
        const timing = await measureJoin(olivia.page, alice.page, call.id, how, { spa: i === 1, noise: scenario.noise });
        results.push(timing);
        console.log(`${name} / ${how}: ${JSON.stringify(timing)}`);
        if (scenario.background) {
          check(() => expect(timing.rawFrames, `${how}: frames of the room as it is were sent, with a background chosen`).toBe(0));
        }
        // Alice heard Olivia throughout, RNNoise coming on included: a gap of
        // even a tenth of a second would be several per cent of these 2 to 8 seconds.
        if (timing.gap !== null) check(() => expect(timing.gap, `${how}: Alice's sound had gaps`).toBeLessThan(0.05));
        if (i === 0 || i === 2) console.log(`${name} / ${how}: downloads\n  ${(await downloads(olivia.page)).join('\n  ')}`);
        await leave(olivia.page);
        await leave(alice.page);
      }
      for (const id of calls) await owner.post(`/calls/${id}/end`).catch(() => undefined);
      await owner.dispose();
      if (scenario.background) {
        // The background made ready for the first call is kept for the next, in
        // the same tab: not set up again, which is what took seconds. Counted
        // rather than timed, so a busy machine cannot make it pass or fail. Before,
        // each call set it up again, the preview and the call's camera each.
        const [first, second] = results;
        check(() => expect(first.setups, 'the first call set the background up more than once').toBeLessThanOrEqual(1));
        check(() => expect(second.setups, 'the second call set the background up again').toBe(0));
      }
    });
  }

  // Every camera in a call opens with an idle stand-in on it, so that a
  // background chosen while it is off is on before its first frame. Choosing one
  // while the camera is on replaces the stand-in, as before (#147: the camera
  // goes on as it is while the background gets ready, then switches in one go).
  test('a background chosen during the call stays on: no frame of the room after the switch', async ({ as }) => {
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Blur in the call ${Date.now()}`, [idOf('alice')]);
    const alice = await as('alice');
    const olivia = await as('owner');
    await instrument(olivia);
    await olivia.page.reload();
    await rememberChoices(olivia.page, false, false);
    await joinCall(alice.page, call.id);
    await joinCall(olivia.page, call.id);
    await expect(olivia.page.getByRole('button', { name: 'Turn your camera off' })).toBeVisible({ timeout: 30_000 });
    await expect.poll(async () => (await media(olivia.page)).video ?? null, { timeout: 30_000 }).not.toBeNull();
    // The camera at its full size first, as it is sent.
    await expect.poll(async () => (await media(olivia.page)).width, { timeout: 30_000 }).toBeGreaterThanOrEqual(1280);

    await olivia.page.getByTestId('background-effects').click();
    const menu = olivia.page.getByRole('menu', { name: 'Background effects' });
    await menu.getByRole('menuitemradio', { name: 'Blur', exact: true }).click();
    await expect(menu.getByRole('menuitemradio', { name: 'Blur', exact: true })).toHaveAttribute('aria-checked', 'true');
    await expect(olivia.page.getByTestId('background-effects').locator('.animate-spin')).toHaveCount(0, { timeout: 30_000 });
    // From the switch on, only blurred frames go out, for as long as it is chosen.
    await olivia.page.waitForTimeout(1_000);
    await resetMedia(olivia.page);
    await olivia.page.waitForTimeout(6_000);
    const m = await media(olivia.page);
    console.log(`blur chosen in the call: ${m.rawFrames} frames of the room sent in the 6 s after the switch, sent ${m.width} wide`);
    // (The width is only logged: on a machine without a graphics card the
    // browser sends a smaller picture once encoding has the background's frames
    // to keep up with, on main as well.)
    expect(m.rawFrames, 'the room was sent again after the background went on').toBe(0);
    await olivia.page.keyboard.press('Escape');
    await owner.post(`/calls/${call.id}/end`).catch(() => undefined);
    await owner.dispose();
  });
});
