import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';

// TeamUp on a phone's home screen: what the browser needs to install it, and
// the app's own offer to, on Android at a tap and on an iPhone by showing how.

/** Stands in for Chrome's offer to install, which a test browser never makes. Returns how often it was used. */
async function offerInstall(page: Page) {
  await page.evaluate(() => {
    const w = window as unknown as { asked: number };
    w.asked = 0;
    const e = new Event('beforeinstallprompt', { cancelable: true }) as Event & { prompt: () => Promise<void>; userChoice: Promise<{ outcome: string }> };
    e.prompt = async () => {
      w.asked++;
      setTimeout(() => window.dispatchEvent(new Event('appinstalled')), 50);
    };
    e.userChoice = Promise.resolve({ outcome: 'accepted' });
    window.dispatchEvent(e);
  });
}

test.describe('TeamUp as an app on a phone', () => {
  test('the browser is given what it needs to install it, an iPhone included', async ({ request }) => {
    const manifest = await (await request.get('/manifest.webmanifest')).json();
    expect(manifest).toMatchObject({ id: '/', name: 'TeamUp', display: 'standalone', start_url: '/' });
    // A call turned sideways fills the screen.
    expect(manifest.orientation).toBeUndefined();
    for (const icon of manifest.icons) {
      const res = await request.get(icon.src);
      expect(res.status(), icon.src).toBe(200);
      expect(res.headers()['content-type']).toBe('image/png');
    }
    expect(manifest.icons.some((i: { purpose: string }) => i.purpose === 'maskable')).toBe(true);

    const html = await (await request.get('/')).text();
    for (const tag of ['rel="apple-touch-icon"', 'name="apple-mobile-web-app-capable"', 'viewport-fit=cover', 'rel="manifest"']) {
      expect(html, tag).toContain(tag);
    }
    for (const file of ['/apple-touch-icon.png', '/favicon.svg', '/favicon-32.png', '/sw.js']) {
      expect((await request.get(file)).status(), file).toBe(200);
    }
  });

  test('on Android it installs from the suggestion, which then goes, and Settings says it is installed', async ({ as }) => {
    const sam = await as('sam', { phone: 'android' });
    const banner = sam.page.getByTestId('install-banner');
    // Nothing to offer until the browser offers it.
    await sam.page.waitForTimeout(500);
    await expect(banner).toHaveCount(0);

    await offerInstall(sam.page);
    await expect(banner).toBeVisible();
    await expect(banner).toContainText('Get the TeamUp app');
    // Above the bottom navigation, not over it.
    const bar = await sam.page.getByRole('navigation', { name: 'Sections' }).boundingBox();
    const box = await banner.boundingBox();
    expect(box!.y + box!.height).toBeLessThanOrEqual(bar!.y);
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(390);

    await banner.getByRole('button', { name: 'Install' }).click();
    await expect(banner).toHaveCount(0);
    expect(await sam.page.evaluate(() => (window as unknown as { asked: number }).asked)).toBe(1);

    await sam.page.goto('/settings');
    await expect(sam.page.getByTestId('install-card')).toContainText('TeamUp is installed on this device.');
    expect(sam.problems).toEqual([]);
  });

  test('on an iPhone it shows how, and once put away it stays away; Settings still shows how', async ({ as }) => {
    const alice = await as('alice', { phone: 'iphone' });
    const banner = alice.page.getByTestId('install-banner');
    await expect(banner).toBeVisible({ timeout: 10_000 });
    await banner.getByRole('button', { name: 'Install' }).click();

    const how = alice.page.getByRole('dialog', { name: 'Add TeamUp to your home screen' });
    await expect(how).toBeVisible();
    await expect(how).toContainText('Share');
    await expect(how).toContainText('Add to Home Screen');
    // The steps fit a phone's screen.
    await expect
      .poll(async () => {
        const box = await how.boundingBox();
        return box && box.x >= 0 && box.x + box.width <= 390;
      })
      .toBe(true);
    await alice.page.keyboard.press('Escape');
    await expect(how).toHaveCount(0);
    await expect(banner).toHaveCount(0);

    await alice.page.reload();
    await alice.page.waitForTimeout(1000);
    await expect(banner).toHaveCount(0);

    await alice.page.goto('/settings');
    const card = alice.page.getByTestId('install-card');
    await expect(card).toContainText('Add to Home Screen');
    expect(alice.problems).toEqual([]);
  });

  test('a narrow window on a computer is not asked to install', async ({ as }) => {
    const pat = await as('pat', { width: 390, height: 800 });
    await offerInstall(pat.page);
    await pat.page.waitForTimeout(500);
    await expect(pat.page.getByTestId('install-banner')).toHaveCount(0);
  });
});
