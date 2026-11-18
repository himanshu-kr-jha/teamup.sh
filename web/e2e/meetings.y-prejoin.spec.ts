import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { apiAs, createCall, idOf, joinCall, leaveCall, lobbyJoin, prejoinScreen } from './support/meetings';

// Asked for (18 November): pressing Join on a call first shows a screen to test
// and set up your camera, microphone and background, and to see who is already
// in, as Google Meet does. A voice call still goes straight in, like a phone.

const SHOTS = 'test-results/prejoin';

const preview = (page: Page) => page.getByTestId('prejoin-preview');
const whoIsHere = (page: Page) => page.getByRole('status', { name: 'Who is here' });

/** Whether the camera's picture is really drawn, not only a black box. */
const drawing = (page: Page) =>
  preview(page).evaluate((v: HTMLVideoElement) => v.videoWidth > 0 && v.readyState >= 2 && !v.paused);

/** Nothing focused, as after clicking on an empty part of the page. */
const blur = (page: Page) => page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());

/**
 * Ends a test's call, so nobody it invited is still being rung when the next spec
 * signs them in: somebody who only looked at the screen before it never answered.
 */
const endAndForget = async (api: Awaited<ReturnType<typeof apiAs>>, id: string) => {
  await api.post(`/calls/${id}/end`).catch(() => undefined);
  await api.dispose();
};

/** Whether this page has a horizontal scroll, which a phone layout must not. */
const scrollsSideways = (page: Page) =>
  page.evaluate(() => {
    const root = document.scrollingElement ?? document.documentElement;
    return root.scrollWidth > root.clientWidth + 1;
  });

test.describe('the screen before a call', () => {
  test('Join stops at it: camera preview, microphone level, who is here, the keys, and in with what was chosen', async ({ as }) => {
    test.setTimeout(150_000);
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Green room ${Date.now()}`, [idOf('alice')]);

    // Alice is in already, so Olivia's screen has somebody to show.
    const alice = await as('alice');
    await joinCall(alice.page, call.id);
    // A browser that has not joined before goes in as before: microphone and camera off.
    await expect(alice.page.getByRole('button', { name: 'Turn your microphone on' })).toBeVisible({ timeout: 20_000 });
    await expect(alice.page.getByRole('button', { name: 'Turn your camera on' })).toBeVisible();

    const olivia = await as('owner');
    const page = olivia.page;
    await page.goto(`/calls/${call.id}`);
    await expect(prejoinScreen(page)).toBeVisible({ timeout: 20_000 });
    // Not in the call: nothing has asked for a token yet.
    await expect(page.getByRole('button', { name: 'Leave the call' })).toHaveCount(0);
    await expect(page.getByText(call.title, { exact: true })).toBeVisible();
    await expect(page.getByText('Your camera is off')).toBeVisible();

    // Who is here comes from the webhooks, and is looked at again while the screen is open.
    await expect(whoIsHere(page)).toContainText('Alice is here', { timeout: 30_000 });

    // The camera, one click away, mirrored as it is to you in the call.
    await page.getByRole('button', { name: 'Turn your camera on' }).click();
    await expect(preview(page)).toBeVisible({ timeout: 20_000 });
    await expect.poll(() => drawing(page), { timeout: 20_000 }).toBe(true);
    const transform = await preview(page).evaluate((v) => getComputedStyle(v).transform);
    expect(transform, 'the preview is mirrored').toMatch(/^matrix\(-1,/);

    // The microphone, with a level that moves to the fake microphone's speech.
    await page.getByRole('button', { name: 'Turn your microphone on' }).click();
    const meter = page.getByRole('meter', { name: 'Microphone level' });
    await expect.poll(async () => Number(await meter.getAttribute('data-level')), { timeout: 20_000 }).toBeGreaterThan(0);

    // The devices are listed by name once one of them is open.
    await expect(page.getByRole('combobox', { name: 'Choose a camera' })).toBeEnabled();
    await expect(page.getByRole('combobox', { name: 'Choose a microphone' })).toBeEnabled();
    await page.getByRole('button', { name: 'Test speaker' }).click();

    // The background picker works here, on the preview.
    await expect(page.getByTestId('background-effects')).toBeEnabled({ timeout: 20_000 });
    await page.getByTestId('background-effects').click();
    await expect(page.getByRole('menu', { name: 'Background effects' })).toBeVisible();
    await page.waitForTimeout(300);
    await page.screenshot({ path: `${SHOTS}/background-menu.png` });
    await page.getByRole('menuitemradio', { name: 'Blur', exact: true }).click();
    await expect(page.getByRole('menu', { name: 'Background effects' })).toHaveCount(0);
    // On the preview itself: the camera goes through the background processor.
    await expect(page.getByTestId('background-effects')).toHaveAttribute('aria-pressed', 'true');
    await expect(page.getByTestId('background-effects').locator('.animate-spin')).toHaveCount(0, { timeout: 30_000 });
    await expect.poll(() => drawing(page), { timeout: 20_000 }).toBe(true);
    await page.getByTestId('background-effects').click();
    await page.getByRole('menuitemradio', { name: 'None', exact: true }).click();
    await expect(page.getByTestId('background-effects').locator('.animate-spin')).toHaveCount(0, { timeout: 30_000 });

    // Once the menu has faded out.
    await page.waitForTimeout(400);
    await page.screenshot({ path: `${SHOTS}/desktop-light.png` });

    // Ctrl+D and Ctrl+E work the switches here, as in the call.
    await blur(page);
    await page.keyboard.press('Control+d');
    await expect(page.getByRole('button', { name: 'Turn your microphone on' })).toBeVisible();
    await page.keyboard.press('Control+d');
    await expect(page.getByRole('button', { name: 'Turn your microphone off' })).toBeVisible();
    await page.keyboard.press('Control+e');
    await expect(page.getByRole('button', { name: 'Turn your camera on' })).toBeVisible();
    await expect(preview(page)).toHaveCount(0);
    await page.keyboard.press('Control+e');
    await expect.poll(() => drawing(page), { timeout: 20_000 }).toBe(true);

    // In, with the microphone and camera on as chosen.
    await lobbyJoin(page).click();
    await expect(page.getByRole('button', { name: 'Leave the call' })).toBeVisible({ timeout: 30_000 });
    await expect(page.getByRole('button', { name: 'Turn your microphone off' }).first()).toBeVisible({ timeout: 20_000 });
    await expect(page.getByRole('button', { name: 'Turn your camera off' }).first()).toBeVisible();
    // Alice sees Olivia's camera.
    await expect
      .poll(() => alice.page.evaluate(() => Array.from(document.querySelectorAll('video')).some((v) => v.videoWidth > 0)), {
        timeout: 20_000,
      })
      .toBe(true);
    // The preview let go of the camera: one camera track in this page, the call's.
    await expect
      .poll(() => page.evaluate(() => Array.from(document.querySelectorAll('video')).filter((v) => v.dataset.testid === 'prejoin-preview').length))
      .toBe(0);

    // Next time the screen starts as you joined: both on.
    await leaveCall(page);
    await expect(page).toHaveURL(/\/calls$/);
    await page.goto(`/calls/${call.id}`);
    await expect(prejoinScreen(page)).toBeVisible({ timeout: 20_000 });
    await expect(page.getByRole('button', { name: 'Turn your microphone off' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Turn your camera off' })).toBeVisible();
    await expect.poll(() => drawing(page), { timeout: 20_000 }).toBe(true);
    // Back leaves without joining.
    await page.getByRole('button', { name: 'Back', exact: true }).click();
    await expect(page).toHaveURL(/\/calls$/);
    await expect(page.getByRole('button', { name: 'Leave the call' })).toHaveCount(0);
    await owner.dispose();
  });

  test('on a phone, in the dark: the preview on top, everything else below, nothing sideways', async ({ as }) => {
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Green room phone ${Date.now()}`, [idOf('sam')]);
    const sam = await as('sam', { phone: 'android', theme: 'dark' });
    const page = sam.page;
    await page.goto(`/calls/${call.id}`);
    await expect(prejoinScreen(page)).toBeVisible({ timeout: 20_000 });
    await expect(whoIsHere(page)).toContainText('Nobody is here yet');
    await page.getByRole('button', { name: 'Turn your camera on' }).click();
    await expect.poll(() => drawing(page), { timeout: 20_000 }).toBe(true);

    const camera = await page.getByRole('region', { name: 'Your camera' }).boundingBox();
    const joinPart = await page.getByRole('region', { name: 'Join the call' }).boundingBox();
    expect(camera && joinPart && camera.y + camera.height <= joinPart.y, 'the preview is above the rest').toBeTruthy();
    expect(camera!.width).toBeGreaterThan(300);
    expect(await scrollsSideways(page)).toBe(false);
    await page.screenshot({ path: `${SHOTS}/phone-dark.png`, fullPage: true });
    await endAndForget(owner, call.id);
  });

  test('a refused camera says so, and you can still join', async ({ as }) => {
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Green room refused ${Date.now()}`, [idOf('max')]);
    const max = await as('max', { theme: 'dark' });
    // As a browser told never to share the camera answers.
    await max.context.addInitScript(() => {
      const original = navigator.mediaDevices.getUserMedia.bind(navigator.mediaDevices);
      navigator.mediaDevices.getUserMedia = (constraints?: MediaStreamConstraints) =>
        constraints?.video ? Promise.reject(new DOMException('Permission denied', 'NotAllowedError')) : original(constraints);
    });
    const page = max.page;
    await page.goto(`/calls/${call.id}`);
    await expect(prejoinScreen(page)).toBeVisible({ timeout: 20_000 });
    await page.getByRole('button', { name: 'Turn your camera on' }).click();
    await expect(page.getByRole('alert').filter({ hasText: 'Your browser is not letting TeamUp use your camera' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Turn your camera on' })).toBeVisible();
    await page.screenshot({ path: `${SHOTS}/camera-refused.png` });

    await lobbyJoin(page).click();
    await expect(page.getByRole('button', { name: 'Leave the call' })).toBeVisible({ timeout: 30_000 });
    await expect(page.getByRole('button', { name: 'Turn your camera on' }).first()).toBeVisible();
    await owner.dispose();
  });

  test('a voice call goes straight in, microphone on, like a phone', async ({ as }) => {
    const owner = await apiAs('owner');
    const call = await owner.post('/calls', { title: `Voice ${Date.now()}`, callType: 'audio', invitees: [idOf('max')] });
    const olivia = await as('owner');
    await olivia.page.goto(`/calls/${call.id}`);
    await expect(olivia.page.getByRole('button', { name: 'Leave the call' })).toBeVisible({ timeout: 30_000 });
    await expect(prejoinScreen(olivia.page)).toHaveCount(0);
    await expect(olivia.page.getByRole('button', { name: 'Turn your microphone off' }).first()).toBeVisible({ timeout: 20_000 });
    await owner.dispose();
  });
});
