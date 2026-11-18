import type { Page } from '@playwright/test';
import { expect, test, type Session } from './support/fixtures';
import { apiAs, createCall, joinCall } from './support/meetings';

// The banner over a call that says your camera or microphone cannot be used
// (18 November: it said so in a call of four while both worked). It is for a
// failure that actually stops your camera or microphone, says which and why,
// goes away once that device works, and can be dismissed.
// Against the real stack, with Chromium's fake camera and microphone.

type Failure = { kind: 'video' | 'audio' | 'display'; name: string };

/**
 * Lets a test make the browser refuse the next request for a camera, a
 * microphone or a screen, with the error a real browser gives: NotAllowedError
 * for a refused permission or a closed screen picker, NotReadableError for a
 * device another app holds. Each failure is used once.
 */
async function canRefuseDevices(session: Session) {
  await session.context.addInitScript(() => {
    const w = window as unknown as { __refuse: Failure[] };
    w.__refuse = [];
    const media = navigator.mediaDevices;
    const take = (kind: Failure['kind']) => {
      const i = w.__refuse.findIndex((f) => f.kind === kind);
      return i < 0 ? null : w.__refuse.splice(i, 1)[0];
    };
    const realUser = media.getUserMedia.bind(media);
    media.getUserMedia = async (constraints?: MediaStreamConstraints) => {
      const failure = (constraints?.video && take('video')) || (constraints?.audio && take('audio'));
      if (failure) throw new DOMException(`refused by the test (${failure.name})`, failure.name);
      return realUser(constraints);
    };
    const realDisplay = media.getDisplayMedia.bind(media);
    media.getDisplayMedia = async (options?: DisplayMediaStreamOptions) => {
      const failure = take('display');
      if (failure) throw new DOMException('Permission denied by user', failure.name);
      return realDisplay(options);
    };
  });
}

const refuseNext = (page: Page, failure: Failure) =>
  page.evaluate((f) => (window as unknown as { __refuse: Failure[] }).__refuse.push(f), failure);

const banner = (page: Page) => page.getByTestId('media-error');
/** What the banner said before 19 November, whatever had gone wrong. */
const OLD_WORDING = 'TeamUp could not use your camera or microphone';

async function inACall(as: (who: 'owner') => Promise<Session>, title: string) {
  const owner = await apiAs('owner');
  const call = await createCall(owner, `${title} ${Date.now()}`);
  await owner.dispose();
  const olivia = await as('owner');
  await canRefuseDevices(olivia);
  await joinCall(olivia.page, call.id);
  return olivia;
}

test.describe('the camera and microphone banner in a call', () => {
  test('closing the screen picker is not a camera or microphone problem', async ({ as }) => {
    const olivia = await inACall(as, 'Picker closed');
    const page = olivia.page;
    await refuseNext(page, { kind: 'display', name: 'NotAllowedError' });
    await page.getByRole('button', { name: 'Share your screen' }).click();
    await page.waitForTimeout(1500);
    await expect(banner(page)).toHaveCount(0);
    await expect(page.getByText(OLD_WORDING)).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Share your screen' })).toBeVisible();
  });

  test('a camera busy at the first try says so, and the banner goes when the camera comes on', async ({ as }) => {
    const olivia = await inACall(as, 'Camera busy');
    const page = olivia.page;
    await refuseNext(page, { kind: 'video', name: 'NotReadableError' });
    await page.getByRole('button', { name: 'Turn your camera on' }).click();
    await expect(banner(page)).toBeVisible({ timeout: 10_000 });
    await expect(banner(page)).toContainText('Your camera is in use by another app, or could not start.');
    await expect(page.getByText(OLD_WORDING)).toHaveCount(0);
    // Said once, in the banner, not again in a toast.
    await page.waitForTimeout(500);
    await expect(page.getByText('Your camera is in use by another app')).toHaveCount(1);

    // Pixel checks of the banner itself, light and dark, wide and phone.
    const shots = process.env.E2E_SHOTS_DIR ?? 'e2e-shots';
    await page.screenshot({ path: `${shots}/media-error__1280__light.png` });
    await page.evaluate(() => document.documentElement.classList.add('dark'));
    await page.waitForTimeout(400);
    await page.screenshot({ path: `${shots}/media-error__1280__dark.png` });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.waitForTimeout(400);
    await page.screenshot({ path: `${shots}/media-error__390__dark.png` });
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
    await page.evaluate(() => document.documentElement.classList.remove('dark'));
    await page.waitForTimeout(400);
    await page.screenshot({ path: `${shots}/media-error__390__light.png` });
    await page.setViewportSize({ width: 1280, height: 800 });

    // The second try works, and nothing is left saying it does not.
    await page.getByRole('button', { name: 'Turn your camera on' }).click();
    await expect(page.getByRole('button', { name: 'Turn your camera off' })).toBeVisible({ timeout: 15_000 });
    await expect(banner(page)).toHaveCount(0);
    await expect(page.getByText(OLD_WORDING)).toHaveCount(0);
  });

  test('a refused microphone is named, can be dismissed, and a camera working does not hide it', async ({ as }) => {
    const olivia = await inACall(as, 'Mic refused');
    const page = olivia.page;
    await refuseNext(page, { kind: 'audio', name: 'NotAllowedError' });
    await page.getByRole('button', { name: 'Turn your microphone on' }).click();
    await expect(banner(page)).toContainText('Your browser blocked the microphone. Allow it in the address bar, then turn the microphone on again.', {
      timeout: 10_000,
    });
    // The camera working says nothing about the microphone.
    await page.getByRole('button', { name: 'Turn your camera on' }).click();
    await expect(page.getByRole('button', { name: 'Turn your camera off' })).toBeVisible({ timeout: 15_000 });
    await expect(banner(page)).toBeVisible();

    await banner(page).getByRole('button', { name: 'Dismiss' }).click();
    await expect(banner(page)).toHaveCount(0);
  });

  test('turning the camera on and off quickly is not an error', async ({ as }) => {
    const olivia = await inACall(as, 'Quick toggle');
    const page = olivia.page;
    const camera = page.getByRole('button', { name: /^Turn your camera (on|off)$/ });
    await camera.dblclick();
    await page.waitForTimeout(2500);
    await camera.click();
    await page.waitForTimeout(2500);
    await expect(banner(page)).toHaveCount(0);
    await expect(page.getByText(OLD_WORDING)).toHaveCount(0);
  });
});
