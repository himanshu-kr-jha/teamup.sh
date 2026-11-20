import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { apiAs, createCall, idOf, joinCall, leaveCall, lobbyJoin, prejoinScreen } from './support/meetings';
import { PEOPLE } from './support/people';
import { COLOURS, addOrgPictures, removeAllOrgPictures, solidPng } from './support/orgPictures';

// The owner's request of 19 November 2026: the organization uploads a set of
// pictures, and those are the only background pictures anybody can choose:
// no pictures that came with TeamUp, no picture of your own. The blurs stay,
// and so do the company backgrounds with each person's name on.

const menuOf = (page: Page) => page.getByRole('menu', { name: 'Background effects' });

/** The average colour across the top of somebody's video as another person receives it. */
async function receivedColour(page: Page, name: string) {
  return page.evaluate((who) => {
    const tile = [...document.querySelectorAll<HTMLElement>('[data-testid="participant-tile"]')].find((el) => el.textContent?.includes(who));
    const video = tile?.querySelector<HTMLVideoElement>('video');
    if (!video || video.videoWidth === 0) return null;
    const c = document.createElement('canvas');
    c.width = 64;
    c.height = 36;
    const g = c.getContext('2d')!;
    g.drawImage(video, 0, 0, 64, 36);
    const { data } = g.getImageData(0, 0, 64, 8);
    let r = 0, gr = 0, b = 0;
    for (let i = 0; i < data.length; i += 4) {
      r += data[i];
      gr += data[i + 1];
      b += data[i + 2];
    }
    const n = data.length / 4;
    return { r: r / n, g: gr / n, b: b / n };
  }, name);
}

test.describe('the organization’s background pictures', () => {
  test.beforeEach(async () => {
    const owner = await apiAs('owner');
    await removeAllOrgPictures(owner);
    await owner.dispose();
  });
  test.afterEach(async () => {
    const owner = await apiAs('owner');
    await removeAllOrgPictures(owner);
    await owner.dispose();
  });

  test('with none yet, the menu says so, and an administrator is pointed to where they are added', async ({ as }) => {
    test.setTimeout(120_000);
    const owner = await apiAs('owner');
    const call = await createCall(owner, `No pictures ${Date.now()}`, [idOf('alice')]);
    // Olivia stays in the call throughout, so it is not over when Alice leaves.
    const olivia = await as('owner', { theme: 'dark' });
    await joinCall(olivia.page, call.id);
    for (const theme of ['light', 'dark'] as const) {
      const alice = await as('alice', { theme });
      await joinCall(alice.page, call.id);
      await alice.page.getByTestId('background-effects').click();
      const menu = menuOf(alice.page);
      await expect(menu.getByTestId('background-pictures-empty')).toContainText('Your organization has not added any background pictures yet.');
      // Alice cannot add any, so she is not offered to.
      await expect(menu.getByRole('menuitem', { name: 'Add pictures' })).toHaveCount(0);
      for (const name of ['None', 'Slight blur', 'Blur']) await expect(menu.getByRole('menuitemradio', { name, exact: true })).toBeVisible();
      await alice.page.waitForTimeout(300);
      await menu.screenshot({ path: `test-results/orgbg-picker-empty-member-${theme}.png` });
      await alice.page.keyboard.press('Escape');
      await leaveCall(alice.page);
      await alice.context.close();
    }

    // Olivia administers the organization: the menu takes her to Appearance.
    await olivia.page.getByTestId('background-effects').click();
    const add = menuOf(olivia.page).getByRole('menuitem', { name: 'Add pictures' });
    await expect(add).toBeVisible();
    await olivia.page.waitForTimeout(300);
    await menuOf(olivia.page).screenshot({ path: 'test-results/orgbg-picker-empty-admin-dark.png' });
    await add.click();
    await expect(olivia.page).toHaveURL(/\/organization\?tab=appearance$/);
    await expect(olivia.page.getByText('Background pictures', { exact: true })).toBeVisible();
    await expect(olivia.page.getByText('No background pictures yet')).toBeVisible();
    // The call goes on in the corner meanwhile.
    await expect(olivia.page.getByTestId('call-window')).toBeVisible();
    await leaveCall(olivia.page);
    await owner.dispose();
  });

  test('an administrator adds a set, names, orders and removes them, and a member wears one before and in the call', async ({ as }) => {
    test.setTimeout(180_000);
    const owner = await apiAs('owner');
    const olivia = await as('owner', { width: 1440, height: 1000 });
    await olivia.page.goto('/organization?tab=appearance');
    const card = olivia.page.getByTestId('call-pictures-card');
    await expect(olivia.page.getByText('No background pictures yet')).toBeVisible();
    await olivia.page.screenshot({ path: 'test-results/orgbg-admin-empty.png', fullPage: false });

    // Several at once, one of them too small, which is refused and says why.
    await olivia.page.getByLabel('Background picture files').setInputFiles([
      { name: 'harbour-at-dusk.png', mimeType: 'image/png', buffer: solidPng(COLOURS.night) },
      { name: 'warm_lobby.png', mimeType: 'image/png', buffer: solidPng(COLOURS.dawn) },
      { name: 'tiny.png', mimeType: 'image/png', buffer: solidPng(COLOURS.red, 640, 360) },
      { name: 'red-wall.png', mimeType: 'image/png', buffer: solidPng(COLOURS.red, 1920, 1080) },
    ]);
    await expect(olivia.page.getByText('3 pictures are added.', { exact: false }).first()).toBeVisible({ timeout: 30_000 });
    await expect(olivia.page.getByRole('alert')).toContainText('tiny.png: The picture must be at least 1280 x 720.');
    const list = olivia.page.getByRole('list', { name: 'Background pictures' });
    await expect(list.getByRole('listitem')).toHaveCount(3);
    await expect(list.getByRole('img', { name: 'Harbour at dusk' })).toBeVisible();
    await expect(list.getByRole('img', { name: 'Warm lobby' })).toBeVisible();
    await expect(list.getByRole('img', { name: 'Red wall' })).toBeVisible();
    // The thumbnails are the small copies, and they have loaded.
    await expect
      .poll(() => list.locator('img').evaluateAll((imgs) => (imgs as HTMLImageElement[]).map((i) => i.complete && i.naturalWidth)))
      .toEqual([384, 384, 384]);

    // Renamed, moved first, and one taken away.
    const name = list.getByLabel('Name of Warm lobby');
    await name.fill('Lobby');
    await name.press('Enter');
    await expect(list.getByRole('img', { name: 'Lobby' })).toBeVisible();
    await list.getByRole('button', { name: 'Move Lobby earlier' }).click();
    await expect.poll(() => list.locator('img').evaluateAll((imgs) => imgs.map((i) => i.getAttribute('alt')))).toEqual([
      'Lobby',
      'Harbour at dusk',
      'Red wall',
    ]);
    await list.getByRole('button', { name: 'Remove Red wall' }).click();
    await expect(list.getByRole('listitem')).toHaveCount(2);
    await expect(olivia.page.getByText('Red wall is removed.', { exact: false }).first()).toBeVisible();
    await card.scrollIntoViewIfNeeded();
    await olivia.page.mouse.move(0, 0);
    await olivia.page.waitForTimeout(500);
    await card.screenshot({ path: 'test-results/orgbg-admin-card-light.png' });
    // The same in the dark, and on a phone.
    const dark = await as('owner', { theme: 'dark', width: 1440, height: 1000 });
    await dark.page.goto('/organization?tab=appearance');
    await expect(dark.page.getByRole('img', { name: 'Lobby' })).toBeVisible();
    await dark.page.getByText('Background pictures', { exact: true }).scrollIntoViewIfNeeded();
    await dark.page.waitForTimeout(500);
    await dark.page.screenshot({ path: 'test-results/orgbg-admin-card-dark.png' });
    const phone = await as('owner', { width: 390, height: 844 });
    await phone.page.goto('/organization?tab=appearance');
    await expect(phone.page.getByRole('img', { name: 'Lobby' })).toBeVisible();
    await phone.page.getByText('Background pictures', { exact: true }).scrollIntoViewIfNeeded();
    expect(await phone.page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
    await phone.page.waitForTimeout(500);
    await phone.page.screenshot({ path: 'test-results/orgbg-admin-card-phone.png' });

    // A member cannot change them.
    const aliceApi = await apiAs('alice');
    expect(await aliceApi.status('delete', `/org/call-pictures/${(await owner.get('/org/call-backgrounds')).pictures[0].id}`)).toBe(403);
    await aliceApi.dispose();

    // Alice, before joining, chooses one; the menu has only the blurs and the organization's.
    const call = await createCall(owner, `Pictures ${Date.now()}`, [idOf('alice'), idOf('pat')]);
    const alice = await as('alice');
    await alice.page.goto(`/calls/${call.id}`);
    await expect(prejoinScreen(alice.page)).toBeVisible();
    await alice.page.getByRole('button', { name: 'Turn your camera on' }).click();
    await alice.page.getByTestId('background-effects').click();
    const menu = menuOf(alice.page);
    await expect(menu.getByRole('group', { name: 'Pictures' }).getByRole('menuitemradio')).toHaveCount(2);
    await expect(menu.getByRole('menuitemradio', { name: 'Lobby', exact: true })).toBeVisible();
    await expect(menu.getByRole('menuitemradio', { name: 'Harbour at dusk', exact: true })).toBeVisible();
    await expect(menu.getByRole('menuitemradio', { name: 'Red wall', exact: true })).toHaveCount(0);
    await expect
      .poll(() => menu.getByRole('group', { name: 'Pictures' }).locator('img').evaluateAll((imgs) => (imgs as HTMLImageElement[]).every((i) => i.complete && i.naturalWidth > 0)))
      .toBe(true);
    await alice.page.waitForTimeout(300);
    await menu.screenshot({ path: 'test-results/orgbg-picker-prejoin.png' });
    await menu.getByRole('menuitemradio', { name: 'Harbour at dusk', exact: true }).click();
    await expect(menu.getByRole('menuitemradio', { name: 'Harbour at dusk', exact: true })).toHaveAttribute('aria-checked', 'true');
    await alice.page.keyboard.press('Escape');
    await lobbyJoin(alice.page).click();
    await expect(alice.page.getByRole('button', { name: 'Leave the call' })).toBeVisible({ timeout: 30_000 });
    // In with the camera on, as chosen before joining.
    await expect(alice.page.getByRole('button', { name: 'Turn your camera off' }).first()).toBeVisible({ timeout: 20_000 });

    // Olivia, in the call, receives Alice's video in front of it: a dark blue-grey.
    await joinCall(olivia.page, call.id);
    await expect
      .poll(
        async () => {
          const c = await receivedColour(olivia.page, PEOPLE.alice.name);
          return !!c && c.r < 45 && c.g < 50 && c.b < 70 && c.b >= c.r;
        },
        { timeout: 45_000, message: 'Alice never arrived in front of the harbour' }
      )
      .toBe(true);

    // In the call the same menu, in the light and on a phone-sized window.
    await alice.page.getByTestId('background-effects').click();
    await expect(menuOf(alice.page).getByRole('menuitemradio', { name: 'Harbour at dusk', exact: true })).toHaveAttribute('aria-checked', 'true');
    await alice.page.waitForTimeout(800);
    await menuOf(alice.page).screenshot({ path: 'test-results/orgbg-picker-call-light.png' });
    await alice.page.keyboard.press('Escape');
    const narrow = await as('pat', { width: 390, height: 844, theme: 'dark' });
    await joinCall(narrow.page, call.id);
    await narrow.page.getByTestId('background-effects').click();
    await expect(menuOf(narrow.page).getByRole('menuitemradio', { name: 'Lobby', exact: true })).toBeVisible();
    const box = (await menuOf(narrow.page).boundingBox())!;
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(390);
    await narrow.page.waitForTimeout(500);
    await narrow.page.screenshot({ path: 'test-results/orgbg-picker-phone-dark.png' });
    await narrow.page.keyboard.press('Escape');
    await leaveCall(narrow.page);
    await leaveCall(alice.page);
    await leaveCall(olivia.page);
    await owner.dispose();
  });

  test('a guest, who is not of the organization, has the blurs but not its pictures', async ({ as }) => {
    test.setTimeout(120_000);
    const owner = await apiAs('owner');
    await addOrgPictures(owner, { Harbour: COLOURS.night });
    const call = await createCall(owner, `Guest backgrounds ${Date.now()}`);
    const olivia = await as('owner');
    await joinCall(olivia.page, call.id);
    const link = await owner.put(`/calls/${call.id}/guest-link`, { enabled: true });
    const guest = await as('sam', { signedOut: true });
    await guest.page.goto(link.directPath);
    await guest.page.getByLabel('Your name').fill('Gail Guest');
    await guest.page.getByRole('button', { name: 'Join the meeting' }).click();
    await expect(guest.page.getByRole('button', { name: 'Leave' })).toBeVisible({ timeout: 30_000 });
    await guest.page.getByTestId('background-effects').click();
    const menu = menuOf(guest.page);
    for (const name of ['None', 'Slight blur', 'Blur']) await expect(menu.getByRole('menuitemradio', { name, exact: true })).toBeVisible();
    await expect(menu.getByRole('menuitemradio', { name: 'Harbour', exact: true })).toHaveCount(0);
    await expect(menu.getByText('Pictures', { exact: true })).toHaveCount(0);
    await expect(menu.getByTestId('background-pictures-empty')).toHaveCount(0);
    await guest.page.keyboard.press('Escape');
    await guest.page.getByRole('button', { name: 'Leave' }).click();
    await leaveCall(olivia.page);
    await owner.dispose();
  });
});
