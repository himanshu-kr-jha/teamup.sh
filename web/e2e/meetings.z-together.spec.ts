import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import type { Page } from '@playwright/test';
import { expect, test, type Session } from './support/fixtures';
import { apiAs, createCall, ensureMember, EXTRA, idOf, joinCall, leaveCall, lobbyJoin, prejoinScreen, waitForLog } from './support/meetings';
import { PEOPLE } from './support/people';

// The new call features of 18 and 19 November together, in long calls of three
// and four people, as they meet in real use: the screen before a call, noise
// cancellation, speaking bars and raised hands, ringing people into a call,
// notes paused and resumed, a refresh that goes straight back in, and the
// camera and microphone banner. The last test walks the older flows again with
// all of that in. Against the real stack; speech to text and the model are a
// local stand-in, as in meetings.y-notesonoff.spec.ts.

const SHOTS = 'test-results/together';

// ── the stand-in for speech to text and the model ─────────────────────────────
let standIn: Server;
/** While true, what speech to text hears is marked, so it would show if it reached the notes. */
let pausedNow = false;
const heard: number[] = [];
const asked: string[] = [];

// ── what is on screen ─────────────────────────────────────────────────────────
const tile = (page: Page, id: string) => page.locator(`[data-testid="participant-tile"][data-person="${id}"]`).first();
const bars = (page: Page, id: string) => tile(page, id).getByTestId('speaking-bars');
const badge = (page: Page, id: string) => tile(page, id).getByTestId('hand-badge');
const chip = (page: Page) => page.getByTestId('hands-chip');
const notesBanner = (page: Page) => page.getByTestId('notes-banner');
const mediaBanner = (page: Page) => page.getByTestId('media-error');
const callWindow = (page: Page) => page.getByTestId('call-window');
const leaveButton = (page: Page) => page.getByRole('button', { name: 'Leave the call' }).first();
const noiseSwitch = (page: Page) => page.getByRole('switch', { name: 'Noise cancellation' });
const micMenu = (page: Page) => page.getByRole('button', { name: 'Choose a microphone' });
const ring = (page: Page) => page.getByRole('dialog').filter({ hasText: /^Incoming (video )?call/ });

const NOTES_ON = 'Notes are being taken. Everybody here gets the minutes when the call ends.';
const PAUSED_BY_ALICE = /^Notes paused by Alice Archer at \d{1,2}:\d{2}( [AP]M)?\. Nothing said now is written down\.$/;

/** The tallest the bars on a tile have been over a short while, in pixels. */
const tallestBar = (page: Page, id: string, forMs = 1500) =>
  bars(page, id).evaluate(
    (el, ms) =>
      new Promise<number>((resolve) => {
        let tallest = 0;
        const end = performance.now() + ms;
        const look = () => {
          for (const bar of Array.from(el.children)) tallest = Math.max(tallest, (bar as HTMLElement).getBoundingClientRect().height);
          if (performance.now() < end) requestAnimationFrame(look);
          else resolve(tallest);
        };
        look();
      }),
    forMs
  );

/** Writes down every toast that says somebody joined, from now on. */
async function watchArrivals(page: Page) {
  await page.evaluate(() => {
    const w = window as unknown as { __arrivals: string[] };
    w.__arrivals = [];
    new MutationObserver((changes) => {
      for (const c of changes) c.addedNodes.forEach((n) => /joined the call|left the call/.test(n.textContent ?? '') && w.__arrivals.push(n.textContent ?? ''));
    }).observe(document.body, { childList: true, subtree: true, characterData: true });
  });
}
const arrivals = (page: Page) => page.evaluate(() => (window as unknown as { __arrivals: string[] }).__arrivals);

/**
 * Lets a test make the browser refuse the next request for a screen, with the
 * error a real browser gives when its picker is closed (as in meetings.y-mediaerror.spec.ts).
 */
async function canRefuseScreen(session: Session) {
  await session.context.addInitScript(() => {
    const w = window as unknown as { __refuseScreen: number };
    w.__refuseScreen = 0;
    const media = navigator.mediaDevices;
    const real = media.getDisplayMedia.bind(media);
    media.getDisplayMedia = async (options?: DisplayMediaStreamOptions) => {
      if (w.__refuseScreen > 0) {
        w.__refuseScreen--;
        throw new DOMException('Permission denied by user', 'NotAllowedError');
      }
      return real(options);
    };
  });
}

async function say(page: Page, text: string) {
  const box = page.getByTestId('meeting-chat').getByLabel('Message to everyone in the call');
  if (!(await box.isVisible())) await page.getByTestId('meeting-chat-button').click();
  await box.fill(text);
  await box.press('Enter');
  await expect(box).toHaveValue('');
}

/** Whether the page scrolls sideways, which a phone layout must not. */
const scrollsSideways = (page: Page) =>
  page.evaluate(() => {
    const root = document.scrollingElement ?? document.documentElement;
    return root.scrollWidth > root.clientWidth + 1;
  });

/** Whether any element that holds text runs out of the right edge of the screen. */
const cutOffText = (page: Page) =>
  page.evaluate(() => {
    const out: string[] = [];
    for (const el of Array.from(document.querySelectorAll('button, span, p, h1, h2, h3, div'))) {
      const own = Array.from(el.childNodes).some((n) => n.nodeType === 3 && n.textContent!.trim());
      if (!own) continue;
      const r = el.getBoundingClientRect();
      if (r.width && r.height && getComputedStyle(el).visibility !== 'hidden' && (r.right > innerWidth + 1 || r.left < -1)) out.push((el.textContent ?? '').trim().slice(0, 40));
    }
    return out;
  });

/** Settles fades before a screenshot. */
const still = (page: Page) => page.waitForTimeout(400);

async function joinThroughPrejoin(page: Page, callId: string, set: (page: Page) => Promise<void> = async () => undefined) {
  await page.goto(`/calls/${callId}`);
  await expect(prejoinScreen(page)).toBeVisible({ timeout: 20_000 });
  await set(page);
  await lobbyJoin(page).click();
  await expect(leaveButton(page)).toBeVisible({ timeout: 30_000 });
  await expect(prejoinScreen(page)).toHaveCount(0);
}

test.describe('the new call features together', () => {
  test.beforeAll(async () => {
    standIn = createServer((req, res) => {
      let body = '';
      req.on('data', (c) => (body += c));
      req.on('end', () => {
        res.setHeader('Content-Type', 'application/json');
        if (req.url === '/speech-to-text') {
          heard.push(Date.now());
          res.end(JSON.stringify({ transcript: pausedNow ? `Heard while paused ${heard.length}.` : `Spoken clip ${heard.length}.` }));
        } else if (req.url === '/v1/messages') {
          asked.push(JSON.parse(body).messages[0].content as string);
          res.end(
            JSON.stringify({
              content: [{ type: 'text', text: JSON.stringify({ summary: 'The team planned the release.', decisions: [], actionItems: [], openQuestions: [] }) }],
            })
          );
        } else {
          res.statusCode = 404;
          res.end('{}');
        }
      });
    });
    await new Promise<void>((resolve) => standIn.listen(0, '127.0.0.1', resolve));
    const url = `http://127.0.0.1:${(standIn.address() as AddressInfo).port}`;
    const owner = await apiAs('owner');
    await owner.ctx.put('/api/org/notetaker', {
      data: { enabled: true, onlyWhenAsked: false, sttKey: 'sk-sarvam-test', sttBaseUrl: url, llmProvider: 'anthropic', llmKey: 'sk-ant-test', llmBaseUrl: url },
    });
    await owner.dispose();
  });

  test.afterAll(async () => {
    const owner = await apiAs('owner');
    await owner.ctx.put('/api/org/notetaker', { data: { enabled: false, llmProvider: 'anthropic' } });
    await owner.dispose();
    await new Promise((resolve) => standIn.close(resolve));
  });

  test('a four-person video meeting: pre-join, noise cancellation, speaking, hands, ringing Zoe in, notes paused, a refresh, and the minutes', async ({ as }) => {
    test.setTimeout(420_000);
    const zoeId = await ensureMember(EXTRA.zoe);
    const [ALICE, SAM] = [idOf('alice'), idOf('sam')];
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Release planning ${Date.now()}`, [ALICE, SAM]);

    // Olivia on a light desktop, Alice on a light desktop, Sam on a dark phone, Zoe on a light desktop.
    const olivia = await as('owner');
    const alice = await as('alice');
    const sam = await as('sam', { phone: 'android', theme: 'dark' });
    const zoe = await as(EXTRA.zoe);
    await zoe.page.goto('/tasks');

    await test.step('1. Olivia starts it; Alice and Sam come in through the screen before the call', async () => {
      await joinThroughPrejoin(olivia.page, call.id);
      // The note-taker comes in once there is somebody to take notes of.
      await expect(olivia.page.getByText('Waiting for others')).toBeVisible();

      // Alice: camera on, microphone on, noise cancellation left on. Blur goes on
      // once she is in (chosen before joining is the test after this one).
      await joinThroughPrejoin(alice.page, call.id, async (page) => {
        await expect(noiseSwitch(page)).toBeChecked();
        await page.getByRole('button', { name: 'Turn your camera on' }).click();
        await expect(page.getByTestId('prejoin-preview')).toBeVisible({ timeout: 20_000 });
        await page.getByRole('button', { name: 'Turn your microphone on' }).click();
        await expect(noiseSwitch(page)).toHaveAttribute('data-status', 'on', { timeout: 20_000 });
        await still(page);
        await page.screenshot({ path: `${SHOTS}/1-alice-prejoin-light.png` });
      });
      await expect(alice.page.getByRole('button', { name: 'Turn your microphone off' }).first()).toBeVisible({ timeout: 20_000 });
      await expect(alice.page.getByRole('button', { name: 'Turn your camera off' }).first()).toBeVisible({ timeout: 20_000 });
      await alice.page.getByTestId('background-effects').click();
      await alice.page.getByRole('menuitemradio', { name: 'Blur', exact: true }).click();
      await alice.page.keyboard.press('Escape');
      await expect(alice.page.getByTestId('background-effects')).toHaveAttribute('aria-pressed', 'true');
      await expect(alice.page.getByTestId('background-effects').locator('.animate-spin')).toHaveCount(0, { timeout: 30_000 });
      await expect(micMenu(alice.page)).toHaveAttribute('data-noise-cancellation', 'on', { timeout: 20_000 });
      await expect(mediaBanner(alice.page)).toHaveCount(0);

      // Sam, on his phone: microphone on.
      await joinThroughPrejoin(sam.page, call.id, async (page) => {
        await expect(page.getByRole('status', { name: 'Who is here' })).toContainText(/Olivia|Alice/, { timeout: 30_000 });
        await page.getByRole('button', { name: 'Turn your microphone on' }).click();
        expect(await scrollsSideways(page)).toBe(false);
        await still(page);
        await page.screenshot({ path: `${SHOTS}/1-sam-prejoin-phone-dark.png`, fullPage: true });
      });
      await expect(sam.page.getByRole('button', { name: 'Turn your microphone off' }).first()).toBeVisible({ timeout: 20_000 });
      for (const s of [olivia, alice, sam]) await expect(notesBanner(s.page)).toHaveText(NOTES_ON, { timeout: 30_000 });
    });

    await test.step('1. Alice speaks through noise cancellation, and her bars move on Olivia and Sam', async () => {
      for (const page of [olivia.page, sam.page]) {
        await expect(bars(page, ALICE)).toHaveAttribute('data-speaking', 'true', { timeout: 20_000 });
        await expect.poll(() => tallestBar(page, ALICE), { message: 'Alice’s bars rise with her voice', timeout: 15_000 }).toBeGreaterThan(6);
      }
      // Her camera reaches Olivia, blurred or not, as a picture.
      await expect
        .poll(() => olivia.page.evaluate(() => Array.from(document.querySelectorAll('video')).some((v) => v.videoWidth > 0)), { timeout: 20_000 })
        .toBe(true);
    });

    await test.step('1. Sam raises his hand, Alice second; the order is right everywhere', async () => {
      await sam.page.getByRole('button', { name: 'Raise your hand' }).click();
      await expect(badge(olivia.page, SAM)).toHaveText('1');
      await alice.page.getByRole('button', { name: 'Raise your hand' }).click();
      for (const page of [olivia.page, alice.page, sam.page]) {
        await expect(badge(page, SAM)).toHaveText('1');
        await expect(badge(page, ALICE)).toHaveText('2');
        await expect(chip(page)).toHaveText(/2 hands raised/);
      }
      // Alice goes on talking with her hand up: a sentence already going does not lower it.
      await alice.page.waitForTimeout(4_000);
      await expect(badge(olivia.page, ALICE)).toHaveText('2');
      await still(olivia.page);
      await olivia.page.screenshot({ path: `${SHOTS}/1-hands-olivia-light.png` });
      await sam.page.screenshot({ path: `${SHOTS}/1-hands-sam-phone-dark.png` });
      expect(await scrollsSideways(sam.page)).toBe(false);
    });

    await test.step('1. Olivia rings Zoe in with Add people; Zoe answers, sees the hands and the notes banner', async () => {
      await olivia.page.getByRole('button', { name: 'Add people' }).click();
      const dialog = olivia.page.getByRole('dialog', { name: 'Add people' });
      const offered = dialog.getByRole('list', { name: 'People' });
      await expect(offered.getByText(PEOPLE.alice.name, { exact: true }), 'Alice is already in').toHaveCount(0);
      await dialog.getByLabel('Find people').fill('zoe');
      await offered.getByText(EXTRA.zoe.name, { exact: true }).click();
      await dialog.getByRole('button', { name: 'Ring', exact: true }).click();
      await expect(dialog).toHaveCount(0);
      for (const page of [olivia.page, alice.page, sam.page]) await expect(page.getByText(`Ringing ${EXTRA.zoe.name}…`)).toBeVisible({ timeout: 10_000 });
      await sam.page.screenshot({ path: `${SHOTS}/1-ringing-zoe-phone-dark.png` });

      await expect(ring(zoe.page)).toBeVisible({ timeout: 20_000 });
      await expect(ring(zoe.page)).toContainText(`${PEOPLE.owner.name} is calling you into a call with`);
      await zoe.page.screenshot({ path: `${SHOTS}/1-zoe-incoming-light.png` });
      await ring(zoe.page).getByRole('button', { name: 'Answer' }).click();
      // Answering a ring goes straight in: no screen before it.
      await expect(leaveButton(zoe.page)).toBeVisible({ timeout: 30_000 });
      await expect(prejoinScreen(zoe.page)).toHaveCount(0);
      await expect(zoe.page).toHaveURL(new RegExp(`/calls/${call.id}$`));
      await expect(badge(zoe.page, SAM)).toHaveText('1', { timeout: 20_000 });
      await expect(badge(zoe.page, ALICE)).toHaveText('2');
      await expect(notesBanner(zoe.page)).toHaveText(NOTES_ON, { timeout: 30_000 });
      for (const page of [olivia.page, alice.page, sam.page]) {
        await expect(tile(page, zoeId)).toBeVisible({ timeout: 20_000 });
        await expect(page.getByText(`Ringing ${EXTRA.zoe.name}…`)).toHaveCount(0);
      }
      await still(zoe.page);
      await zoe.page.screenshot({ path: `${SHOTS}/1-zoe-in-light.png` });
    });

    await test.step('1. Olivia lowers all hands', async () => {
      await chip(olivia.page).click();
      const list = olivia.page.getByTestId('hands-list');
      await expect(list.locator('li')).toHaveText([/1\s*Sam Sales/, /2\s*Alice Archer/]);
      await still(olivia.page);
      await olivia.page.screenshot({ path: `${SHOTS}/1-hands-list-light.png` });
      await list.getByRole('menuitem', { name: 'Lower all' }).click();
      for (const page of [olivia.page, alice.page, sam.page, zoe.page]) {
        await expect(chip(page)).toHaveCount(0);
        await expect(page.getByTestId('hand-badge')).toHaveCount(0);
      }
      await expect(alice.page.getByRole('button', { name: 'Raise your hand' })).toBeVisible();
      await expect(sam.page.getByRole('button', { name: 'Raise your hand' })).toBeVisible();
    });

    await test.step('1. Olivia adds her phone as a second camera: still four people, her tiles named, the notes go on', async () => {
      const OLIVIA = idOf('owner');
      const phone = await as('owner', { phone: 'iphone' });
      await phone.page.goto(`/calls/${call.id}`);
      await expect(prejoinScreen(phone.page)).toBeVisible({ timeout: 20_000 });
      await lobbyJoin(phone.page).click();
      const choice = phone.page.getByTestId('companion-choice');
      await expect(choice).toBeVisible({ timeout: 30_000 });
      await choice.getByRole('button', { name: 'Camera only' }).click();
      await expect(leaveButton(phone.page)).toBeVisible({ timeout: 30_000 });
      await expect(phone.page.getByTestId('companion-note')).toBeVisible();
      await expect(notesBanner(phone.page)).toHaveText(NOTES_ON, { timeout: 30_000 });
      const hers = (page: Page) => page.locator(`[data-testid="participant-tile"][data-person="${OLIVIA}"]`);
      for (const page of [olivia.page, alice.page, sam.page, zoe.page, phone.page]) {
        await expect(hers(page)).toHaveCount(2, { timeout: 20_000 });
        await expect(page.getByTestId('call-heading')).toContainText(/4/);
      }
      await expect(olivia.page.getByTestId('call-heading')).toContainText('In a call · 4');
      await expect(alice.page.getByText(`${PEOPLE.owner.name} (phone)`, { exact: true })).toBeVisible();
      await expect(alice.page.getByText(`${PEOPLE.owner.name} (laptop)`, { exact: true })).toBeVisible();
      await still(phone.page);
      await phone.page.screenshot({ path: `${SHOTS}/1-olivia-phone-camera-only.png` });
      expect(await scrollsSideways(phone.page)).toBe(false);
      await leaveCall(phone.page);
      for (const page of [olivia.page, alice.page, sam.page, zoe.page]) await expect(hers(page)).toHaveCount(1, { timeout: 20_000 });
      await expect(alice.page.getByText(`${PEOPLE.owner.name} (laptop)`, { exact: true })).toHaveCount(0);
    });

    await test.step('2. hands up again, Alice pauses the notes, and everybody, Zoe too, sees who did', async () => {
      await sam.page.getByRole('button', { name: 'Raise your hand' }).click();
      await expect(badge(olivia.page, SAM)).toHaveText('1');
      await zoe.page.getByRole('button', { name: 'Raise your hand' }).click();
      await expect(badge(olivia.page, zoeId)).toHaveText('2');
      await expect.poll(() => heard.length, { message: 'speech reaches the notes', timeout: 60_000 }).toBeGreaterThan(0);
      await say(olivia.page, 'The release notes are in the drive');

      await alice.page.getByRole('button', { name: 'Pause notes' }).click();
      for (const page of [olivia.page, alice.page, sam.page, zoe.page]) {
        await expect(notesBanner(page)).toHaveText(PAUSED_BY_ALICE, { timeout: 10_000 });
      }
      // What was being said at the pause may still arrive; from here on anything heard is marked.
      await olivia.page.waitForTimeout(3_000);
      pausedNow = true;
      await say(olivia.page, 'Off the record: the budget is tight');
      await still(sam.page);
      await sam.page.screenshot({ path: `${SHOTS}/2-paused-sam-phone-dark.png` });
      await olivia.page.screenshot({ path: `${SHOTS}/2-paused-olivia-light.png` });
      expect(await scrollsSideways(sam.page)).toBe(false);
      expect(await cutOffText(sam.page), 'text that runs off the phone screen').toEqual([]);
    });

    await test.step('2. Sam reloads during the pause: straight back, microphone on, banner paused, and nobody told', async () => {
      await alice.page.waitForTimeout(1_000);
      for (const s of [olivia, alice, zoe]) await watchArrivals(s.page);
      await sam.page.reload();
      await expect(prejoinScreen(sam.page)).toHaveCount(0);
      await expect(leaveButton(sam.page)).toBeVisible({ timeout: 30_000 });
      await expect(prejoinScreen(sam.page)).toHaveCount(0);
      await expect(sam.page.getByRole('button', { name: 'Turn your microphone off' }).first()).toBeVisible({ timeout: 20_000 });
      await expect(notesBanner(sam.page)).toHaveText(PAUSED_BY_ALICE, { timeout: 20_000 });
      await expect(bars(olivia.page, SAM)).toHaveAttribute('data-speaking', 'true', { timeout: 20_000 });
      // His raised hand comes back with him, still first in the queue, for everybody.
      for (const page of [olivia.page, alice.page, zoe.page, sam.page]) {
        await expect(badge(page, SAM)).toHaveText('1', { timeout: 20_000 });
        await expect(badge(page, zoeId)).toHaveText('2');
      }
      for (const page of [olivia.page, alice.page, zoe.page]) expect(await arrivals(page), 'no join or leave toast for a reload').toEqual([]);
      await still(sam.page);
      await sam.page.screenshot({ path: `${SHOTS}/2-sam-after-reload-phone-dark.png` });
    });

    await test.step('2. Alice resumes, and the call ends', async () => {
      const before = heard.length;
      await alice.page.waitForTimeout(8_000);
      expect(heard.length, 'clips sent to speech to text while the notes were paused').toBe(before);
      pausedNow = false;
      await alice.page.getByRole('button', { name: 'Resume notes' }).click();
      for (const page of [olivia.page, alice.page, sam.page, zoe.page]) await expect(notesBanner(page)).toHaveText(NOTES_ON, { timeout: 10_000 });
      await expect.poll(() => heard.length, { timeout: 60_000 }).toBeGreaterThan(before);
      await say(olivia.page, 'Back on: we ship on Friday');
      for (const s of [zoe, sam, alice, olivia]) await leaveCall(s.page);
    });

    await test.step('2. the minutes and the transcript have nothing from the pause, and say where it was', async () => {
      await expect.poll(() => asked.length, { timeout: 150_000, intervals: [2_000] }).toBeGreaterThan(0);
      const prompt = asked[asked.length - 1];
      expect(prompt).toContain('The release notes are in the drive');
      expect(prompt).toContain('Back on: we ship on Friday');
      expect(prompt).toMatch(/TeamUp: Notes were paused from \d\d:\d\d to \d\d:\d\d by Alice Archer\./);
      expect(prompt).not.toContain('Off the record');
      expect(prompt).not.toContain('Heard while paused');

      await olivia.page.goto(`/calls/${call.id}/minutes`);
      await expect(olivia.page.getByTestId('minutes')).toContainText('The team planned the release.', { timeout: 30_000 });
      await expect(olivia.page.getByTestId('minutes-pauses')).toContainText(/Notes were paused from .+ to .+ by Alice Archer\./);
      await olivia.page.getByRole('button', { name: 'Show the transcript' }).click();
      const transcript = olivia.page.getByRole('region', { name: 'Transcript' });
      await expect(transcript).toContainText('Spoken clip');
      await expect(transcript).not.toContainText('Heard while paused');
      await expect(transcript).not.toContainText('Off the record');
      await still(olivia.page);
      await olivia.page.screenshot({ path: `${SHOTS}/2-minutes-light.png`, fullPage: true });
      // Zoe, rung in part way, is among the attendees.
      await expect(olivia.page.getByTestId('minutes').locator('header')).toContainText(EXTRA.zoe.name);
    });
    await owner.dispose();
  });

  // Found by this spec: a raised hand is a LiveKit attribute, and a reload is a new
  // connection without it. The proof that would put it back (Hands.tsx, kept) lives
  // only in memory and is used only on RoomEvent.Reconnected, so after a reload the
  // hand is silently gone for everybody, whatever the microphone is doing.
  test('a raised hand keeps its place through a reload', async ({ as }) => {
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Hand reload ${Date.now()}`, [idOf('sam')]);
    const olivia = await as('owner');
    await joinCall(olivia.page, call.id);
    const sam = await as('sam');
    await joinCall(sam.page, call.id);
    await sam.page.getByRole('button', { name: 'Raise your hand' }).click();
    await expect(badge(olivia.page, idOf('sam'))).toHaveText('1');
    await sam.page.reload();
    await expect(leaveButton(sam.page)).toBeVisible({ timeout: 30_000 });
    await sam.page.waitForTimeout(6_000);
    await expect(badge(olivia.page, idOf('sam'))).toHaveText('1');
    await expect(sam.page.getByRole('button', { name: 'Lower your hand' })).toBeVisible();
    await owner.post(`/calls/${call.id}/end`).catch(() => undefined);
    await owner.dispose();
  });

  // Found by this spec: with blur chosen on the screen before the call, Chrome warned
  // "A VideoFrame was garbage collected without being closed" within seconds of joining
  // a call somebody else is in. The browser's own frame streams, which the processor
  // used, dropped frames unclosed when the preview's camera stopped on joining; the
  // camera is now run frame by frame (cameraProcessor.ts, runFrames).
  test('blur chosen before joining does not leak camera frames', async ({ as }) => {
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Blur before ${Date.now()}`, [idOf('alice')]);
    const olivia = await as('owner');
    await joinCall(olivia.page, call.id);
    const alice = await as('alice');
    await joinThroughPrejoin(alice.page, call.id, async (page) => {
      await page.getByRole('button', { name: 'Turn your camera on' }).click();
      await page.getByTestId('background-effects').click();
      await page.getByRole('menuitemradio', { name: 'Blur', exact: true }).click();
      // The menu stays open for the next choice; Escape closes it.
      await page.keyboard.press('Escape');
      await expect(page.getByTestId('background-effects').locator('.animate-spin')).toHaveCount(0, { timeout: 30_000 });
    });
    await alice.page.waitForTimeout(15_000);
    expect(alice.problems).toEqual([]);
    for (const s of [alice, olivia]) await leaveCall(s.page);
    await owner.dispose();
  });

  test('a 1:1 voice call: Sam rung in declines, Back on a phone keeps the call, a closed screen picker is no banner', async ({ as }) => {
    test.setTimeout(240_000);
    const ALICE = idOf('alice');
    const api = await apiAs('owner');
    const dm = await api.post('/channels/direct', { userId: ALICE });

    const alice = await as('alice', { phone: 'android', theme: 'dark' });
    await alice.page.goto('/tasks');
    const sam = await as('sam');
    await sam.page.goto('/tasks');
    const olivia = await as('owner');
    await canRefuseScreen(olivia);

    await test.step('3. Olivia calls Alice; both straight in, no screen before it', async () => {
      await olivia.page.goto(`/chat/${dm.id}`);
      await olivia.page.getByRole('button', { name: 'Call', exact: true }).click();
      await expect(leaveButton(olivia.page)).toBeVisible({ timeout: 30_000 });
      await expect(prejoinScreen(olivia.page)).toHaveCount(0);
      await expect(ring(alice.page)).toBeVisible({ timeout: 20_000 });
      await expect(ring(alice.page)).toContainText('Incoming call');
      await ring(alice.page).getByRole('button', { name: 'Answer' }).click();
      await expect(leaveButton(alice.page)).toBeVisible({ timeout: 30_000 });
      await expect(prejoinScreen(alice.page)).toHaveCount(0);
      await expect(alice.page.getByRole('button', { name: 'Turn your microphone off' }).first()).toBeVisible({ timeout: 20_000 });
      await expect(olivia.page.getByText(PEOPLE.alice.name, { exact: true }).first()).toBeVisible({ timeout: 20_000 });
    });

    await test.step('3. Olivia rings Sam, who declines; the call goes on', async () => {
      await olivia.page.getByRole('button', { name: 'Add people' }).click();
      const dialog = olivia.page.getByRole('dialog', { name: 'Add people' });
      await dialog.getByLabel('Find people').fill('sam');
      await dialog.getByRole('list', { name: 'People' }).getByText(PEOPLE.sam.name, { exact: true }).click();
      await dialog.getByRole('button', { name: 'Ring', exact: true }).click();
      await expect(alice.page.getByText(`Ringing ${PEOPLE.sam.name}…`)).toBeVisible({ timeout: 10_000 });
      await alice.page.screenshot({ path: `${SHOTS}/3-ringing-sam-phone-dark.png` });
      await expect(ring(sam.page)).toBeVisible({ timeout: 20_000 });
      await ring(sam.page).getByRole('button', { name: 'Decline' }).click();
      for (const s of [olivia, alice]) {
        await expect(s.page.getByText(`${PEOPLE.sam.name} declined.`, { exact: true })).toBeVisible({ timeout: 15_000 });
        await expect(s.page.getByText(`Ringing ${PEOPLE.sam.name}…`)).toHaveCount(0);
      }
      await olivia.page.waitForTimeout(2_000);
      await expect(leaveButton(olivia.page)).toBeVisible();
      await expect(leaveButton(alice.page)).toBeVisible();
      await expect(alice.page).toHaveURL(/\/calls\//);
    });

    await test.step('3. Alice presses Back on her phone: the call stays, in the corner', async () => {
      await alice.page.goBack();
      await expect(alice.page).toHaveURL(/\/tasks/);
      await expect(callWindow(alice.page)).toBeVisible();
      // Alice and Olivia; the note-taker is not one of the people.
      await expect(callWindow(alice.page).getByText('2 in call')).toBeVisible({ timeout: 20_000 });
      const box = (await callWindow(alice.page).boundingBox())!;
      expect(box.x).toBeGreaterThanOrEqual(0);
      expect(box.x + box.width).toBeLessThanOrEqual(390);
      expect(await scrollsSideways(alice.page)).toBe(false);
      await still(alice.page);
      await alice.page.screenshot({ path: `${SHOTS}/3-back-corner-phone-dark.png` });
      await expect(olivia.page.getByText(PEOPLE.alice.name, { exact: true }).first()).toBeVisible();
    });

    await test.step('3. Olivia closes the screen picker: no camera or microphone banner', async () => {
      await olivia.page.evaluate(() => ((window as unknown as { __refuseScreen: number }).__refuseScreen = 1));
      await olivia.page.getByRole('button', { name: 'Share your screen' }).click();
      await olivia.page.waitForTimeout(1_500);
      await expect(mediaBanner(olivia.page)).toHaveCount(0);
      await expect(olivia.page.getByText('could not use your camera or microphone')).toHaveCount(0);
      await expect(olivia.page.getByRole('button', { name: 'Share your screen' })).toBeVisible();
      await olivia.page.screenshot({ path: `${SHOTS}/3-picker-closed-light.png` });
    });

    await test.step('3. Olivia hangs up; Alice is left alone, and the call is over once she goes too', async () => {
      const callId = new URL(olivia.page.url()).pathname.split('/').pop()!;
      await leaveCall(olivia.page);
      await expect(leaveButton(olivia.page)).toHaveCount(0);
      // Leaving is only leaving (ActiveCallProvider.hangUp): the call stays open for whoever is still in it,
      // and alone with the note-taker is alone.
      await expect(callWindow(alice.page).getByText('Waiting for others')).toBeVisible({ timeout: 20_000 });
      await still(alice.page);
      await alice.page.screenshot({ path: `${SHOTS}/3-alone-phone-dark.png` });
      await callWindow(alice.page).getByRole('button', { name: 'Leave the call' }).click();
      await expect(leaveButton(alice.page)).toHaveCount(0);
      await expect.poll(async () => (await api.get(`/calls/${callId}`)).status, { timeout: 30_000 }).toBe('ended');
    });
    await api.dispose();
  });

  test('the older flows with the new code in: a missed call, a guest, a stand-up from its card, the corner window', async ({ as }) => {
    test.setTimeout(360_000);

    await test.step('4. a 1:1 call nobody answers moves the caller to the conversation with the note', async () => {
      const api = await apiAs('owner');
      const dm = await api.post('/channels/direct', { userId: idOf('pat') });
      const pat = await as('pat');
      await pat.page.goto('/tasks');
      const olivia = await as('owner');
      await olivia.page.goto(`/chat/${dm.id}`);
      await olivia.page.getByRole('button', { name: 'Call', exact: true }).click();
      await expect(leaveButton(olivia.page)).toBeVisible({ timeout: 30_000 });
      await expect(ring(pat.page)).toBeVisible({ timeout: 20_000 });
      // Nobody answers: the ring runs out after 45 seconds.
      await expect(olivia.page).toHaveURL(new RegExp(`/chat/${dm.id}$`), { timeout: 90_000 });
      await expect(leaveButton(olivia.page)).toHaveCount(0);
      await expect(callWindow(olivia.page)).toHaveCount(0);
      await expect(olivia.page.locator('[data-testid="call-event"][data-kind="missed"]').last()).toContainText('No answer', { timeout: 20_000 });
      await expect(ring(pat.page)).toHaveCount(0, { timeout: 20_000 });
      await pat.page.goto(`/chat/${dm.id}`);
      await expect(pat.page.locator('[data-testid="call-event"][data-kind="missed"]').last()).toContainText(`Missed call from ${PEOPLE.owner.name}`);
      // And a reload now does not take the caller back into anything.
      await olivia.page.reload();
      await olivia.page.waitForTimeout(3_000);
      await expect(leaveButton(olivia.page)).toHaveCount(0);
      await expect(olivia.page.getByRole('status').filter({ hasText: 'Reconnecting to the call' })).toHaveCount(0);
      await olivia.page.screenshot({ path: `${SHOTS}/4-missed-caller-light.png` });
      await api.dispose();
    });

    await test.step('4. a guest joins by link and sees the hands and the notes banner', async () => {
      const owner = await apiAs('owner');
      const call = await createCall(owner, `Client call ${Date.now()}`, [idOf('alice')]);
      const link = await owner.put(`/calls/${call.id}/guest-link`, { enabled: true });
      const olivia = await as('owner');
      await joinThroughPrejoin(olivia.page, call.id);
      const alice = await as('alice');
      await joinThroughPrejoin(alice.page, call.id);
      await alice.page.getByRole('button', { name: 'Raise your hand' }).click();

      const guest = await as('sam', { signedOut: true, phone: 'android', theme: 'dark' });
      await guest.page.goto(link.directPath as string);
      await guest.page.getByLabel('Your name').fill('Pat Partner');
      await guest.page.getByRole('button', { name: 'Join the meeting' }).click();
      await expect(guest.page.getByRole('button', { name: 'Leave' })).toBeVisible({ timeout: 30_000 });
      await expect(notesBanner(guest.page)).toContainText('Notes are being taken, including what you say', { timeout: 30_000 });
      await expect(badge(guest.page, idOf('alice'))).toHaveText('1', { timeout: 20_000 });
      await expect(guest.page.getByRole('button', { name: 'Pause notes' })).toHaveCount(0);
      // Olivia raises hers from the keyboard; the guest sees her second.
      await olivia.page.keyboard.press('Control+Shift+H');
      await expect(badge(guest.page, idOf('owner'))).toHaveText('2', { timeout: 20_000 });
      await expect(olivia.page.getByText('Pat Partner (guest)', { exact: true })).toBeVisible({ timeout: 20_000 });
      expect(await scrollsSideways(guest.page)).toBe(false);
      await still(guest.page);
      await guest.page.screenshot({ path: `${SHOTS}/4-guest-phone-dark.png` });
      await guest.page.getByRole('button', { name: 'Leave' }).click();
      for (const s of [alice, olivia]) await leaveCall(s.page);
      await owner.post(`/calls/${call.id}/end`).catch(() => undefined);
      await owner.dispose();
    });

    await test.step('4. a stand-up joined from its card through the screen before it records attendance; its corner window drags, resizes and keeps toasts on screen', async () => {
      const mia = await apiAs('mia');
      // Attendance is for administrators and whoever is given See attendance.
      const owner = await apiAs('owner');
      const d = new Date(Date.now() - 5 * 60_000);
      const minutes = d.toDateString() === new Date().toDateString() ? d.getHours() * 60 + d.getMinutes() : 1;
      const timeOfDay = `${String(Math.floor(minutes / 60)).padStart(2, '0')}:${String(minutes % 60).padStart(2, '0')}`;
      const backend = (await mia.get('/standing-audience-options')).teams.find((t: any) => t.name === 'Backend').id;
      const made = await mia.post('/standing-meetings', {
        name: `Backend daily ${Date.now()}`,
        timeOfDay,
        timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone,
        weekdays: [1, 2, 3, 4, 5, 6, 7],
        everyone: false,
        teamIds: [backend],
      });
      const meeting = made.standingMeeting;
      try {
        const alice = await as('alice');
        await alice.page.goto('/calls');
        await alice.page.getByRole('button', { name: `Join ${meeting.name}` }).click();
        await expect(prejoinScreen(alice.page)).toBeVisible({ timeout: 20_000 });
        await expect(alice.page.getByRole('heading', { name: meeting.name })).toBeVisible();
        await lobbyJoin(alice.page).click();
        await expect(leaveButton(alice.page)).toBeVisible({ timeout: 30_000 });
        const ann = await as('ann');
        await ann.page.goto('/calls');
        await ann.page.getByRole('button', { name: `Join ${meeting.name}` }).click();
        await lobbyJoin(ann.page).click();
        await expect(leaveButton(ann.page)).toBeVisible({ timeout: 30_000 });

        const callId = (await owner.get(`/standing-meetings/${meeting.id}/attendance`)).days.at(-1).callId;
        await waitForLog(owner, callId, 'Alice and Ann connected', (l) => l.sessions.filter((s) => !s.leftAt && s.userName !== 'Notes').length >= 2);
        const day = (await owner.get(`/standing-meetings/${meeting.id}/attendance`)).days.at(-1);
        const row = (name: string) => day.people.find((p: any) => p.name === name);
        expect(row(PEOPLE.alice.name)).toMatchObject({ status: 'present' });
        expect(row(PEOPLE.ann.name)).toMatchObject({ status: 'present' });
        expect(row(PEOPLE.pat.name)).toMatchObject({ status: 'absent' });

        // The corner window: dragged up, made bigger, and a toast about a hand stays on screen.
        await alice.page.getByRole('button', { name: 'Minimise the call' }).click();
        const corner = callWindow(alice.page);
        await expect(corner).toBeVisible();
        const before = (await corner.boundingBox())!;
        const grip = (await alice.page.getByRole('button', { name: 'Resize the call window' }).boundingBox())!;
        await alice.page.mouse.move(grip.x + grip.width / 2, grip.y + grip.height / 2);
        await alice.page.mouse.down();
        await alice.page.mouse.move(grip.x - 120, grip.y - 90, { steps: 8 });
        await alice.page.mouse.up();
        const bigger = (await corner.boundingBox())!;
        expect(bigger.width).toBeGreaterThan(before.width + 80);

        await ann.page.getByRole('button', { name: 'Raise your hand' }).click();
        const toast = alice.page.getByText('Ann Idle raised their hand', { exact: true }).last();
        await expect(toast).toBeVisible();
        await expect(toast).toBeInViewport({ ratio: 1 });
        const toastBox = (await toast.boundingBox())!;
        const cornerBox = (await corner.boundingBox())!;
        expect(toastBox.y + toastBox.height, 'the toast sits above the window').toBeLessThanOrEqual(cornerBox.y + 1);
        await expect(corner.getByTestId('hand-badge')).toHaveText('1');
        await alice.page.screenshot({ path: `${SHOTS}/4-corner-toast-light.png` });

        const bar = corner.getByText(/\d in call/);
        const b = (await bar.boundingBox())!;
        await alice.page.mouse.move(b.x + b.width / 2, b.y + b.height / 2);
        await alice.page.mouse.down();
        await alice.page.mouse.move(b.x + b.width / 2, 5, { steps: 10 });
        await alice.page.mouse.up();
        expect((await corner.boundingBox())!.y).toBeLessThan(20);
        await ann.page.getByRole('button', { name: 'Lower your hand' }).click();
        await ann.page.getByRole('button', { name: 'Raise your hand' }).click();
        const again = alice.page.getByText('Ann Idle raised their hand', { exact: true }).last();
        await expect(again).toBeInViewport({ ratio: 1 });
        await alice.page.screenshot({ path: `${SHOTS}/4-corner-top-light.png` });

        for (const s of [ann, alice]) await leaveCall(s.page);
      } finally {
        await mia.del(`/standing-meetings/${meeting.id}`);
        await mia.dispose();
        await owner.dispose();
      }
    });
  });
});
