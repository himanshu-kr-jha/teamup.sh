import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { apiAs, createCall, idOf, joinCall, leaveCall } from './support/meetings';
import { PEOPLE } from './support/people';

// The owner's report of 19 November 2026: sharing the whole screen, or the tab
// TeamUp is in, showed your own share live in your own call, which then
// captured itself, over and over, an endless mirror. As in Google Meet, the
// person presenting sees a card saying so, with a button to stop, where their
// own share would be; everybody else sees the screen as before.

/**
 * The videos on a page showing a screen captured on this very device: a track
 * from getDisplayMedia says what it captures, and one received from somebody
 * else does not.
 */
function ownCaptureVideos(page: Page) {
  return page.evaluate(
    () =>
      [...document.querySelectorAll<HTMLVideoElement>('[data-testid="screen-tile"] video')].filter((v) => {
        const track = (v.srcObject as MediaStream | null)?.getVideoTracks()[0];
        return !!track && track.readyState === 'live' && !!(track.getSettings() as MediaTrackSettings & { displaySurface?: string }).displaySurface;
      }).length
  );
}

/** Whether somebody's shared screen arrives on a page, as moving pictures. */
function receivedScreen(page: Page, identity: string) {
  return page.evaluate((who) => {
    const video = document.querySelector<HTMLVideoElement>(`[data-testid="screen-tile"][data-identity="${who}"] video`);
    return !!video && video.videoWidth > 0 && !video.paused;
  }, identity);
}

for (const theme of ['light', 'dark'] as const) {
  test(`the person presenting sees a card instead of their own screen, and the others see the screen (${theme})`, async ({ as }) => {
    test.setTimeout(150_000);
    const owner = await apiAs('owner');
    try {
      const olivia = await as('owner', { theme });
      const alice = await as('alice', { theme });
      const call = await createCall(owner, `Presenting ${theme} ${Date.now()}`, [idOf('alice')]);
      await joinCall(olivia.page, call.id);
      await joinCall(alice.page, call.id);

      // Alice shares her whole screen (the browser picks it, as a person would).
      await alice.page.getByRole('button', { name: 'Share your screen' }).click();
      await expect(alice.page.getByRole('button', { name: 'Stop sharing your screen' })).toBeVisible();

      // Her own share is a card, not the screen itself: nothing on her page shows what she captures.
      const card = alice.page.getByTestId('presenting-card');
      await expect(card).toBeVisible();
      await expect(card).toContainText('You are presenting');
      await expect(card.getByRole('button', { name: 'Stop presenting' })).toBeVisible();
      await expect.poll(() => ownCaptureVideos(alice.page), { timeout: 5_000 }).toBe(0);
      await expect(alice.page.locator(`[data-testid="screen-tile"][data-identity="${idOf('alice')}"] video`)).toHaveCount(0);
      // Olivia sees Alice's screen, moving.
      await expect(olivia.page.getByText(`${PEOPLE.alice.name} is presenting`)).toBeVisible();
      await expect.poll(() => receivedScreen(olivia.page, idOf('alice')), { timeout: 30_000 }).toBe(true);
      await expect(olivia.page.getByTestId('presenting-card')).toHaveCount(0);
      await alice.page.waitForTimeout(500);
      await alice.page.screenshot({ path: `test-results/presenting-card-${theme}.png` });
      await olivia.page.screenshot({ path: `test-results/presenting-seen-${theme}.png` });

      // Olivia shares as well, and takes the stage: Alice's own share waits in
      // the strip, as a small card, and Olivia's screen is on her stage.
      await olivia.page.getByRole('button', { name: 'Share your screen' }).click();
      const strip = alice.page.getByRole('button', { name: `Show ${PEOPLE.owner.name}'s screen on stage` });
      await expect(strip).toBeVisible();
      await strip.click();
      await expect.poll(() => receivedScreen(alice.page, idOf('owner')), { timeout: 30_000 }).toBe(true);
      await expect(alice.page.getByTestId('presenting-card')).toBeVisible();
      await expect.poll(() => ownCaptureVideos(alice.page)).toBe(0);
      await expect.poll(() => ownCaptureVideos(olivia.page)).toBe(0);
      await olivia.page.getByRole('button', { name: 'Stop sharing your screen' }).click();

      // In the corner window it is the card too.
      await alice.page.getByRole('button', { name: 'Minimise the call' }).click();
      const corner = alice.page.getByTestId('call-window');
      await expect(corner).toBeVisible();
      await expect(corner.getByTestId('presenting-card')).toBeVisible();
      await expect.poll(() => ownCaptureVideos(alice.page)).toBe(0);
      await alice.page.waitForTimeout(500);
      await corner.screenshot({ path: `test-results/presenting-corner-${theme}.png` });

      // Stop presenting stops the share, for everybody.
      await corner.getByRole('button', { name: 'Stop presenting' }).click();
      await expect(alice.page.getByTestId('presenting-card')).toHaveCount(0);
      await expect(olivia.page.getByText(`${PEOPLE.alice.name} is presenting`)).toHaveCount(0);
      await leaveCall(alice.page);
      await leaveCall(olivia.page);
    } finally {
      await owner.dispose();
    }
  });
}

test('on a phone-sized window the card fits, and stopping from it stops the share', async ({ as }) => {
  test.setTimeout(120_000);
  const owner = await apiAs('owner');
  try {
    const olivia = await as('owner');
    // A narrow desktop window: a phone's browser cannot share a screen at all.
    const alice = await as('alice', { width: 390, height: 844 });
    const call = await createCall(owner, `Presenting narrow ${Date.now()}`, [idOf('alice')]);
    await joinCall(olivia.page, call.id);
    await joinCall(alice.page, call.id);
    await alice.page.getByRole('button', { name: 'Share your screen' }).click();
    const card = alice.page.getByTestId('presenting-card');
    await expect(card).toBeVisible();
    await expect.poll(() => ownCaptureVideos(alice.page)).toBe(0);
    await expect.poll(() => receivedScreen(olivia.page, idOf('alice')), { timeout: 30_000 }).toBe(true);
    expect(await alice.page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
    await alice.page.waitForTimeout(500);
    await alice.page.screenshot({ path: 'test-results/presenting-card-phone.png' });
    await card.getByRole('button', { name: 'Stop presenting' }).click();
    await expect(alice.page.getByRole('button', { name: 'Share your screen' })).toBeVisible();
    await expect(olivia.page.getByText(`${PEOPLE.alice.name} is presenting`)).toHaveCount(0);
    await leaveCall(alice.page);
    await leaveCall(olivia.page);
  } finally {
    await owner.dispose();
  }
});
