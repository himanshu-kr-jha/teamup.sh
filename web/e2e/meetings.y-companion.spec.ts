import type { Page } from '@playwright/test';
import { expect, settled, test, type Session } from './support/fixtures';
import { apiAs, createCall, idOf, joinCall, leaveCall, passPrejoin, prejoinScreen, waitForLog } from './support/meetings';
import { PEOPLE } from './support/people';

// Companion mode (spec item 14, 19 November): one person in a call from a laptop
// and a phone at once. Each browser is its own identity in the room; everything
// that counts people counts them once; and joining from the second device asks
// whether it is only a second camera or takes the call over.

const SHOTS = 'test-results/companion';

const ALICE = PEOPLE.alice.name;
const tiles = (page: Page, who: string) => page.locator(`[data-testid="participant-tile"][data-person="${who}"]`);
const tileOf = (page: Page, who: string, device: 'phone' | 'laptop') =>
  page.locator(`[data-testid="participant-tile"][data-person="${who}"][data-device="${device}"]`);
const heading = (page: Page) => page.getByTestId('call-heading');
const choice = (page: Page) => page.getByTestId('companion-choice');
const cameraOnly = (page: Page) => choice(page).getByRole('button', { name: 'Camera only' });
const everything = (page: Page) => choice(page).getByRole('button', { name: /^Use this (phone|laptop|tablet) for everything$/ });
const companionNote = (page: Page) => page.getByTestId('companion-note');
const leaveButton = (page: Page) => page.getByRole('button', { name: 'Leave the call' }).first();
const chip = (page: Page) => page.getByTestId('hands-chip');
const ring = (page: Page) => page.getByRole('dialog').filter({ hasText: /^Incoming (video )?call/ });

/** Whether this page plays live sound from somebody else in the call. */
const hearing = (page: Page) =>
  page.evaluate(() =>
    Array.from(document.querySelectorAll('audio')).some((el) => {
      const stream = el.srcObject as MediaStream | null;
      return !el.paused && !el.muted && el.volume > 0 && !!stream && stream.getAudioTracks().some((t) => t.readyState === 'live' && t.enabled && !t.muted);
    })
  );

/** Whether the page scrolls sideways, which a phone layout must not. */
const scrollsSideways = (page: Page) =>
  page.evaluate(() => {
    const root = document.scrollingElement ?? document.documentElement;
    return root.scrollWidth > root.clientWidth + 1;
  });

/** Opens a call's page and, past the screen before it, waits for the question this device is asked. */
async function reachChoice(page: Page, callId: string) {
  await page.goto(`/calls/${callId}`);
  await expect(prejoinScreen(page).or(choice(page)).first()).toBeVisible({ timeout: 30_000 });
  if (await prejoinScreen(page).isVisible()) await page.getByRole('button', { name: /^Join( now)?$/ }).click();
  await expect(choice(page)).toBeVisible({ timeout: 30_000 });
  await settled(page);
}

/** Alice in on her laptop, with Olivia, and then on her phone as a second camera. */
async function aliceOnTwoDevices(
  as: (who: 'alice' | 'owner', o?: object) => Promise<Session>,
  title: string,
  theme: 'light' | 'dark' = 'light'
) {
  const owner = await apiAs('owner');
  const call = await createCall(owner, `${title} ${Date.now()}`, [idOf('alice')]);
  const olivia = await as('owner');
  await joinCall(olivia.page, call.id);
  const laptop = await as('alice', { theme });
  await joinCall(laptop.page, call.id);
  await expect(tiles(olivia.page, idOf('alice'))).toHaveCount(1, { timeout: 20_000 });
  const phone = await as('alice', { phone: 'android', theme });
  await reachChoice(phone.page, call.id);
  return { owner, call, olivia, laptop, phone };
}

test.describe('companion mode', () => {
  test('a laptop and a phone are both in, named on their tiles, and Alice is counted once; the phone joins as a camera with no sound', async ({ as }) => {
    test.setTimeout(180_000);
    const { owner, call, olivia, laptop, phone } = await aliceOnTwoDevices(as, 'Two devices');
    const alice = idOf('alice');

    // The question, as the phone asks it.
    await expect(choice(phone.page)).toContainText('You are already in this call');
    await expect(choice(phone.page)).toContainText('You are in it on your laptop. How should this phone join?');
    await expect(everything(phone.page)).toHaveAccessibleName('Use this phone for everything');
    expect(await scrollsSideways(phone.page)).toBe(false);
    await phone.page.screenshot({ path: `${SHOTS}/choice-phone-light.png` });
    // Olivia talks, so there is sound to hear.
    await olivia.page.getByRole('button', { name: 'Turn your microphone on' }).click();
    await cameraOnly(phone.page).click();

    // The phone is in, as a second camera: camera on, microphone and sound off.
    await expect(leaveButton(phone.page)).toBeVisible({ timeout: 30_000 });
    await expect(companionNote(phone.page)).toContainText("Camera only: this phone's microphone and sound are off");
    await expect(phone.page.getByRole('button', { name: 'Turn your microphone on' })).toBeVisible();
    await expect(phone.page.getByRole('button', { name: 'Turn your camera off' })).toBeVisible({ timeout: 20_000 });
    await expect(tiles(phone.page, idOf('owner'))).toHaveCount(1, { timeout: 20_000 });
    await phone.page.waitForTimeout(2000);
    expect(await hearing(phone.page), 'a second camera plays no sound').toBe(false);
    // No "Turn on sound" prompt for sound that is off on purpose besides the note's own button.
    await expect(phone.page.getByRole('button', { name: 'Turn on sound' })).toHaveCount(1);

    // Everybody sees both of Alice's tiles, named, and counts her once.
    for (const page of [olivia.page, laptop.page, phone.page]) {
      await expect(tiles(page, alice)).toHaveCount(2, { timeout: 20_000 });
      await expect(heading(page)).toContainText('In a call · 2');
    }
    await expect(tileOf(olivia.page, alice, 'laptop')).toContainText(`${ALICE} (laptop)`);
    await expect(tileOf(olivia.page, alice, 'phone')).toContainText(`${ALICE} (phone)`);
    await expect(tileOf(laptop.page, alice, 'laptop')).toContainText(`${ALICE} (laptop) (you)`);
    await expect(tileOf(laptop.page, alice, 'phone')).toContainText(`${ALICE} (phone)`);
    await expect(tileOf(phone.page, alice, 'phone')).toContainText(/Alice \(phone\)|You \(phone\)|Alice Archer \(phone\) \(you\)/);
    await olivia.page.screenshot({ path: `${SHOTS}/tiles-desktop-light.png` });
    await phone.page.screenshot({ path: `${SHOTS}/tiles-phone-light.png` });
    expect(await scrollsSideways(phone.page)).toBe(false);

    // The attendance trail has a session for each device, both open.
    const log = await waitForLog(owner, call.id, 'both of Alice’s devices in', (l) => l.sessions.filter((s) => s.userId === alice && !s.leftAt).length === 2);
    expect(log.sessions.filter((s) => s.userId === idOf('owner')).length).toBeGreaterThanOrEqual(1);

    // Her sound is hers to turn on.
    await phone.page.getByRole('button', { name: 'Turn on sound' }).click();
    await expect(companionNote(phone.page)).toHaveCount(0);
    await expect.poll(() => hearing(phone.page), { timeout: 20_000 }).toBe(true);

    // The phone leaving is not the call ending, and the laptop is alone with nobody again but Olivia.
    await leaveCall(phone.page);
    await expect(tiles(olivia.page, alice)).toHaveCount(1, { timeout: 20_000 });
    await expect(tileOf(olivia.page, alice, 'laptop')).toContainText(ALICE);
    await expect(tileOf(olivia.page, alice, 'laptop')).not.toContainText('(laptop)');
    expect((await owner.get(`/calls/${call.id}`)).status).toBe('active');

    for (const s of [laptop, olivia]) await leaveCall(s.page);
    await owner.dispose();
  });

  test('"Use this device for everything" moves the call to the phone, and the laptop says so', async ({ as }) => {
    test.setTimeout(150_000);
    const { owner, call, olivia, laptop, phone } = await aliceOnTwoDevices(as, 'Move over', 'dark');
    const alice = idOf('alice');
    await phone.page.screenshot({ path: `${SHOTS}/choice-phone-dark.png` });
    await everything(phone.page).click();

    await expect(leaveButton(phone.page)).toBeVisible({ timeout: 30_000 });
    await expect(companionNote(phone.page)).toHaveCount(0);
    // The laptop is told where the call went, and is out of it.
    await expect(laptop.page.getByText('Moved to your phone. This device has left the call.').first()).toBeVisible({ timeout: 20_000 });
    await expect(leaveButton(laptop.page)).toHaveCount(0, { timeout: 15_000 });
    await expect(laptop.page).toHaveURL(/\/calls$/);
    await laptop.page.screenshot({ path: `${SHOTS}/moved-laptop.png` });

    // Olivia sees one Alice, on her phone, not named for it any more; the call goes on.
    await expect(tiles(olivia.page, alice)).toHaveCount(1, { timeout: 20_000 });
    await expect(tileOf(olivia.page, alice, 'phone')).toBeVisible();
    await expect(tileOf(olivia.page, alice, 'phone')).not.toContainText('(phone)');
    await expect(heading(olivia.page)).toContainText('In a call · 2');
    expect((await owner.get(`/calls/${call.id}`)).status).toBe('active');
    await waitForLog(owner, call.id, 'the laptop’s session closed and the phone’s open', (l) => {
      const hers = l.sessions.filter((s) => s.userId === alice);
      return hers.length === 2 && hers.filter((s) => !s.leftAt).length === 1;
    });

    for (const s of [phone, olivia]) await leaveCall(s.page);
    await owner.dispose();
  });

  test('a hand raised on either device is one hand, and lowering it on the other takes it down', async ({ as }) => {
    test.setTimeout(150_000);
    const { owner, olivia, laptop, phone } = await aliceOnTwoDevices(as, 'One hand', 'dark');
    await cameraOnly(phone.page).click();
    await expect(leaveButton(phone.page)).toBeVisible({ timeout: 30_000 });
    await expect(tiles(olivia.page, idOf('alice'))).toHaveCount(2, { timeout: 20_000 });

    await phone.page.getByTestId('hand-button').click();
    for (const page of [olivia.page, laptop.page, phone.page]) {
      await expect(chip(page)).toHaveText(/^1 hand raised/, { timeout: 15_000 });
      await expect(page.getByTestId('hand-badge')).toHaveCount(1);
    }
    await settled(laptop.page);
    await laptop.page.screenshot({ path: `${SHOTS}/hand-laptop-dark.png` });
    await phone.page.screenshot({ path: `${SHOTS}/hand-phone-dark.png` });
    expect(await scrollsSideways(phone.page)).toBe(false);
    // Hers on both devices: the laptop's button says it is up.
    await expect(laptop.page.getByRole('button', { name: 'Lower your hand' })).toBeVisible();
    // Raising "again" on the laptop is lowering the one hand.
    await laptop.page.getByRole('button', { name: 'Lower your hand' }).click();
    for (const page of [olivia.page, laptop.page, phone.page]) await expect(chip(page)).toHaveCount(0, { timeout: 15_000 });
    await expect(phone.page.getByRole('button', { name: 'Raise your hand' })).toBeVisible();

    for (const s of [phone, laptop, olivia]) await leaveCall(s.page);
    await owner.dispose();
  });

  test('a call of two still ends when it should, though the person called is in from a phone too', async ({ as }) => {
    test.setTimeout(150_000);
    // Alice calls Olivia, as from their conversation: named after the person called.
    const caller = await apiAs('alice');
    const call = await caller.post('/calls', { title: PEOPLE.owner.name, callType: 'video', invitees: [idOf('owner')] });
    const owner = await apiAs('owner');
    const alice = await as('alice');
    await joinCall(alice.page, call.id);
    const oLaptop = await as('owner');
    await joinCall(oLaptop.page, call.id);
    const oPhone = await as('owner', { phone: 'android' });
    await reachChoice(oPhone.page, call.id);
    await cameraOnly(oPhone.page).click();
    await expect(leaveButton(oPhone.page)).toBeVisible({ timeout: 30_000 });

    // Each of Olivia's devices names the call after Alice, and counts two people.
    for (const page of [oLaptop.page, oPhone.page]) {
      await expect(heading(page)).toContainText(ALICE, { timeout: 20_000 });
      await expect(heading(page)).toContainText('In a call · 2', { timeout: 20_000 });
    }
    await expect(heading(alice.page)).toContainText(PEOPLE.owner.name);
    // Alice sees both of Olivia's devices before she leaves, or she would be last out.
    await expect(heading(alice.page)).toContainText('In a call · 2', { timeout: 20_000 });
    await expect(tiles(alice.page, idOf('owner'))).toHaveCount(2, { timeout: 20_000 });

    // Alice hangs up. Olivia, with only her own phone, is waiting again, not in a call of two.
    await leaveCall(alice.page);
    for (const page of [oLaptop.page, oPhone.page]) await expect(heading(page)).toContainText('Waiting for others', { timeout: 20_000 });
    expect((await owner.get(`/calls/${call.id}`)).status).toBe('active');

    // Her laptop leaving does not end it under her phone; the phone, last out, does.
    await leaveCall(oLaptop.page);
    await oPhone.page.waitForTimeout(1500);
    expect((await owner.get(`/calls/${call.id}`)).status).toBe('active');
    await expect(leaveButton(oPhone.page)).toBeVisible();
    // Once the phone sees the laptop gone, it is last out.
    await expect(tiles(oPhone.page, idOf('owner'))).toHaveCount(1, { timeout: 20_000 });
    await leaveCall(oPhone.page);
    await expect.poll(async () => (await owner.get(`/calls/${call.id}`)).status, { timeout: 20_000 }).toBe('ended');

    await caller.dispose();
    await owner.dispose();
  });

  test('answering on one device stops the ring on the other, and somebody already in is not rung again', async ({ as }) => {
    test.setTimeout(150_000);
    const laptop = await as('alice');
    const phone = await as('alice', { phone: 'android' });
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Ring both ${Date.now()}`, [idOf('alice')]);
    const olivia = await as('owner');
    await joinCall(olivia.page, call.id);

    await expect(ring(laptop.page)).toBeVisible({ timeout: 20_000 });
    await expect(ring(phone.page)).toBeVisible({ timeout: 20_000 });
    await phone.page.screenshot({ path: `${SHOTS}/ringing-phone.png` });
    await ring(phone.page).getByRole('button', { name: 'Answer' }).click();
    await passPrejoin(phone.page);
    await expect(leaveButton(phone.page)).toBeVisible({ timeout: 30_000 });
    await expect(ring(laptop.page), 'the laptop stops ringing').toHaveCount(0, { timeout: 15_000 });

    // In from her phone, she is in: ringing her into the call is refused.
    const res = await owner.ctx.post(`/api/calls/${call.id}/ring`, { data: { userIds: [idOf('alice')] } });
    expect(res.status()).toBe(409);
    expect((await res.json()).error).toContain('already in this call');
    await laptop.page.waitForTimeout(2000);
    await expect(ring(laptop.page)).toHaveCount(0);

    for (const s of [phone, olivia]) await leaveCall(s.page);
    await owner.dispose();
  });

  test('a reload of either device keeps its place, and is not asked again', async ({ as }) => {
    test.setTimeout(180_000);
    const { owner, olivia, laptop, phone } = await aliceOnTwoDevices(as, 'Reload both');
    const alice = idOf('alice');
    await cameraOnly(phone.page).click();
    await expect(leaveButton(phone.page)).toBeVisible({ timeout: 30_000 });
    await expect(tiles(olivia.page, alice)).toHaveCount(2, { timeout: 20_000 });
    const before = await tiles(olivia.page, alice).evaluateAll((els) => els.map((e) => e.getAttribute('data-identity')).sort());

    // The phone reloads: straight back in, still a second camera, never asked.
    await phone.page.reload();
    await expect(leaveButton(phone.page)).toBeVisible({ timeout: 30_000 });
    await expect(choice(phone.page)).toHaveCount(0);
    await expect(companionNote(phone.page)).toBeVisible();
    await expect(phone.page.getByRole('button', { name: 'Turn your microphone on' })).toBeVisible();

    // The laptop reloads: back in as itself.
    await laptop.page.reload();
    await expect(leaveButton(laptop.page)).toBeVisible({ timeout: 30_000 });
    await expect(choice(laptop.page)).toHaveCount(0);
    await expect(companionNote(laptop.page)).toHaveCount(0);

    // Olivia still sees the same two devices, by the same identities, and Alice once in the count.
    await expect(tiles(olivia.page, alice)).toHaveCount(2, { timeout: 20_000 });
    await olivia.page.waitForTimeout(2000);
    const after = await tiles(olivia.page, alice).evaluateAll((els) => els.map((e) => e.getAttribute('data-identity')).sort());
    expect(after).toEqual(before);
    await expect(heading(olivia.page)).toContainText('In a call · 2');

    for (const s of [phone, laptop, olivia]) await leaveCall(s.page);
    await owner.dispose();
  });

  test('a second tab of the same browser takes the call over from the first, and the first says so', async ({ as }) => {
    test.setTimeout(120_000);
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Two tabs ${Date.now()}`, [idOf('alice')]);
    const olivia = await as('owner');
    await joinCall(olivia.page, call.id);
    const alice = await as('alice');
    await joinCall(alice.page, call.id);
    await expect(tiles(olivia.page, idOf('alice'))).toHaveCount(1, { timeout: 20_000 });

    // The same browser is the same device: no question, and only one of it in the call.
    const second = await alice.context.newPage();
    await second.goto(`/calls/${call.id}`);
    await passPrejoin(second);
    await expect(leaveButton(second)).toBeVisible({ timeout: 30_000 });
    await expect(choice(second)).toHaveCount(0);
    await expect(alice.page.getByText('You joined this call in another tab, so it moved there.').first()).toBeVisible({ timeout: 20_000 });
    await expect(leaveButton(alice.page)).toHaveCount(0);
    await expect(alice.page).toHaveURL(/\/calls$/);
    await olivia.page.waitForTimeout(2000);
    await expect(tiles(olivia.page, idOf('alice'))).toHaveCount(1);
    expect((await owner.get(`/calls/${call.id}`)).status).toBe('active');

    for (const page of [second, olivia.page]) await leaveCall(page);
    await owner.dispose();
  });

  test('a guest sees Alice once in the count and both of her tiles', async ({ as }) => {
    test.setTimeout(150_000);
    const { owner, call, olivia, phone } = await aliceOnTwoDevices(as, 'With a guest');
    await cameraOnly(phone.page).click();
    await expect(leaveButton(phone.page)).toBeVisible({ timeout: 30_000 });

    // The direct link, which needs no passcode.
    const link = await owner.put(`/calls/${call.id}/guest-link`, { enabled: true });
    const guest = await as('sam', { signedOut: true });
    await guest.page.goto(link.directPath as string);
    await guest.page.getByLabel('Your name').fill('Gita Guest');
    await guest.page.getByRole('button', { name: 'Join the meeting' }).click();
    await expect(guest.page.getByRole('button', { name: 'Leave' })).toBeVisible({ timeout: 30_000 });

    // Olivia, Alice and the guest: three people, four tiles.
    await expect(guest.page.getByText(`${ALICE} (laptop)`, { exact: true })).toBeVisible({ timeout: 20_000 });
    await expect(guest.page.getByText(`${ALICE} (phone)`, { exact: true })).toBeVisible();
    await expect(guest.page.getByText(/ · 3$/)).toBeVisible();
    await expect(olivia.page.getByText('Gita Guest (guest)', { exact: true })).toBeVisible({ timeout: 20_000 });
    await expect(heading(olivia.page)).toContainText('In a call · 3');
    await guest.page.screenshot({ path: `${SHOTS}/guest-view.png` });

    await guest.page.getByRole('button', { name: 'Leave' }).click();
    for (const s of [phone, olivia]) await leaveCall(s.page);
    await owner.dispose();
  });
});
