import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { apiAs, createCall, idOf, joinCall } from './support/meetings';
import type { PersonKey } from './support/people';

// Asked for: a microphone that moves while somebody speaks, as in Google Meet, and
// raising a hand. The hand is a LiveKit participant attribute that only the server
// writes, so what one browser shows is what every browser shows.
//
// Chromium's fake microphone plays e2e/fixtures/speech.wav, so a microphone that
// is on is somebody talking. A video call starts with everybody's microphone off.

const tile = (page: Page, who: PersonKey) =>
  page.locator(`[data-testid="participant-tile"][data-person="${idOf(who)}"]`).first();
const bars = (page: Page, who: PersonKey) => tile(page, who).getByTestId('speaking-bars');
const badge = (page: Page, who: PersonKey) => tile(page, who).getByTestId('hand-badge');
const chip = (page: Page) => page.getByTestId('hands-chip');

/** The tallest the three bars on a tile have been over a short while, in pixels. */
async function tallestBar(page: Page, who: PersonKey, forMs = 1500): Promise<number> {
  return bars(page, who).evaluate(
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
}

/**
 * What the hands chip lies over: any tile, or any button on one. It has a row of
 * its own, so this should always be empty.
 */
async function underTheChip(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const chip = document.querySelector('[data-testid="hands-chip"]')?.getBoundingClientRect();
    if (!chip) return ['no chip'];
    const meets = (r: DOMRect) => r.left < chip.right && chip.left < r.right && r.top < chip.bottom && chip.top < r.bottom;
    const hit: string[] = [];
    document.querySelectorAll('[data-testid="participant-tile"]').forEach((tile) => {
      if (meets(tile.getBoundingClientRect())) hit.push(`tile ${tile.getAttribute('data-identity')}`);
      tile.querySelectorAll('button').forEach((b) => {
        if (meets(b.getBoundingClientRect())) hit.push(`button ${b.getAttribute('aria-label')}`);
      });
    });
    return hit;
  });
}

async function allGone(api: Awaited<ReturnType<typeof apiAs>>, callId: string) {
  await expect
    .poll(async () => (await api.get(`/calls/${callId}`)).status, { message: 'the call to be over', timeout: 60_000 })
    .toBe('ended');
}

test.describe('speaking and raised hands', () => {
  test('the microphone moves with a voice, and hands go up, queue and come down', async ({ as }) => {
    test.setTimeout(180_000);
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Hands ${Date.now()}`, [idOf('alice')]);
    // Olivia organizes, on a dark desktop; Alice is on a light phone.
    const olivia = await as('owner', { theme: 'dark' });
    await joinCall(olivia.page, call.id);
    const alice = await as('alice', { phone: 'android' });
    await joinCall(alice.page, call.id);
    await expect(tile(olivia.page, 'alice')).toBeVisible({ timeout: 20_000 });
    await expect(tile(alice.page, 'owner')).toBeVisible({ timeout: 20_000 });

    // ── the moving microphone ──────────────────────────────────────────────
    // Muted: the crossed-out microphone, and no bars.
    await expect(tile(olivia.page, 'alice').getByLabel('Microphone off')).toBeVisible();
    await expect(bars(olivia.page, 'alice')).toHaveCount(0);

    // Alice turns her microphone on and talks: her tile's bars light up and move.
    await alice.page.getByRole('button', { name: 'Turn your microphone on' }).click();
    await expect(bars(olivia.page, 'alice')).toHaveAttribute('data-speaking', 'true', { timeout: 20_000 });
    await expect(tile(olivia.page, 'alice')).toHaveClass(/ring-green-500/);
    await expect.poll(() => tallestBar(olivia.page, 'alice'), { message: 'the bars to rise with her voice', timeout: 15_000 }).toBeGreaterThan(6);
    // Her own button shows her voice going out, and so does her own tile.
    const aliceMic = alice.page.getByRole('button', { name: 'Turn your microphone off' });
    await expect(aliceMic.getByTestId('speaking-bars')).toBeVisible();
    await expect(bars(alice.page, 'alice')).toBeVisible();
    // On a phone the controls wrap onto two rows, and both stay on the screen.
    const controls = alice.page.getByRole('group', { name: 'Call controls' });
    const box = (await controls.boundingBox())!;
    expect(box.y + box.height).toBeLessThanOrEqual(844);
    expect(box.height).toBeLessThan(110);
    await expect(alice.page.getByRole('button', { name: 'Raise your hand' })).toBeInViewport({ ratio: 1 });
    await olivia.page.screenshot({ path: 'test-results/hands-speaking-dark.png' });
    await alice.page.screenshot({ path: 'test-results/hands-speaking-phone.png' });
    // A voice, not a block: the three bars do not all sit at the top.
    const samples = await bars(olivia.page, 'alice').evaluate(
      (el) =>
        new Promise<number[][]>((resolve) => {
          const out: number[][] = [];
          const take = () => {
            out.push(Array.from(el.children).map((b) => Math.round((b as HTMLElement).getBoundingClientRect().height)));
            if (out.length < 40) setTimeout(take, 50);
            else resolve(out);
          };
          take();
        })
    );
    expect(new Set(samples.map((s) => s.join(','))).size).toBeGreaterThan(3);

    // Asking for less movement: the bars hold the shape of a voice and keep still.
    // The page takes the new setting on its next render, and LiveKit's "speaking"
    // drops for about 400ms between phrases of the fake speech: so wait for the
    // still mode, and read the heights in the same moment as the speaking flag.
    await olivia.page.emulateMedia({ reducedMotion: 'reduce' });
    await expect(bars(olivia.page, 'alice')).toHaveAttribute('data-motion', 'still');
    const stillWhileSpeaking = () =>
      bars(olivia.page, 'alice').evaluate((el) =>
        el.getAttribute('data-speaking') === 'true'
          ? Array.from(el.children).map((b) => Math.round((b as HTMLElement).getBoundingClientRect().height))
          : null
      );
    await expect.poll(stillWhileSpeaking, { message: 'still bars in the shape of a voice', timeout: 20_000 }).toEqual([8, 12, 8]);
    await olivia.page.emulateMedia({ reducedMotion: 'no-preference' });
    await expect(bars(olivia.page, 'alice')).toHaveAttribute('data-motion', 'moving');

    // ── a hand raised mid-sentence stays up; speaking after a pause lowers it ──
    const raise = alice.page.getByRole('button', { name: 'Raise your hand' });
    await expect(raise).toHaveAttribute('title', 'Raise your hand (Ctrl+Shift+H)');
    await raise.click();
    await expect(olivia.page.getByText('Alice Archer raised their hand', { exact: true }).first()).toBeVisible();
    // The fake microphone never pauses, so this is one long sentence that was
    // already going when the hand went up: it does not count.
    await alice.page.waitForTimeout(5_000);
    await expect(badge(olivia.page, 'alice')).toHaveText('1');

    // Muted: no bars, and the quiet that lets her next words count.
    await aliceMic.click();
    await expect(tile(olivia.page, 'alice').getByLabel('Microphone off')).toBeVisible();
    await expect(bars(olivia.page, 'alice')).toHaveCount(0);
    await alice.page.waitForTimeout(1_500);
    await alice.page.getByRole('button', { name: 'Turn your microphone on' }).click();
    await expect(alice.page.getByText('Your hand was lowered because you started speaking.', { exact: true })).toBeVisible({ timeout: 30_000 });
    await expect(badge(olivia.page, 'alice')).toHaveCount(0);
    await expect(alice.page.getByRole('button', { name: 'Raise your hand' })).toBeVisible();
    await aliceMic.click();
    await expect(tile(olivia.page, 'alice').getByLabel('Microphone off')).toBeVisible();

    // ── a queue of hands ───────────────────────────────────────────────────
    await alice.page.getByRole('button', { name: 'Raise your hand' }).click();
    await expect(badge(olivia.page, 'alice')).toHaveText('1');
    await expect(badge(olivia.page, 'alice')).toHaveAttribute('aria-label', 'Hand raised, 1st in line');
    await expect(olivia.page.getByText('Alice Archer raised their hand', { exact: true }).first()).toBeVisible();
    await expect(chip(olivia.page)).toHaveText(/1 hand raised/);
    await expect(alice.page.getByRole('button', { name: 'Lower your hand' })).toHaveAttribute('aria-pressed', 'true');

    // Olivia raises hers from the keyboard, and is second.
    await olivia.page.keyboard.press('Control+Shift+H');
    await expect(badge(alice.page, 'owner')).toHaveText('2');
    await expect(badge(alice.page, 'alice')).toHaveText('1');
    await expect(alice.page.getByText('Olivia Owner raised their hand', { exact: true }).first()).toBeVisible();
    await expect(chip(alice.page)).toHaveText(/2 hands raised/);
    await expect(chip(olivia.page)).toHaveText(/2 hands raised/);
    // The chip has a row of its own: it lies over no tile and no tile's buttons,
    // on a phone or on a desktop.
    expect(await underTheChip(alice.page)).toEqual([]);
    expect(await underTheChip(olivia.page)).toEqual([]);
    // A muted hand stays up.
    await olivia.page.waitForTimeout(4_000);
    await expect(badge(olivia.page, 'alice')).toHaveText('1');
    await olivia.page.screenshot({ path: 'test-results/hands-queue-dark.png' });
    await alice.page.screenshot({ path: 'test-results/hands-queue-phone.png' });

    // Alice sees the list in order, and nothing to lower other people's hands with.
    await chip(alice.page).click();
    const aliceList = alice.page.getByTestId('hands-list');
    await expect(aliceList.locator('li')).toHaveText([/1\s*Alice Archer/, /2\s*Olivia Owner/]);
    await expect(aliceList.getByRole('menuitem')).toHaveCount(0);
    await alice.page.waitForTimeout(300); // its opening fade
    await alice.page.screenshot({ path: 'test-results/hands-list-phone.png' });
    await alice.page.keyboard.press('Escape');

    // The organizer lowers Alice's hand from the list.
    await chip(olivia.page).click();
    const list = olivia.page.getByTestId('hands-list');
    await expect(list.locator('li')).toHaveText([/1\s*Alice Archer/, /2\s*Olivia Owner/]);
    await expect(list.getByRole('menuitem', { name: 'Lower all' })).toBeVisible();
    await olivia.page.waitForTimeout(300);
    await olivia.page.screenshot({ path: 'test-results/hands-list-dark.png' });
    await list.getByRole('menuitem', { name: "Lower Alice Archer's hand" }).click();
    await expect(alice.page.getByText('Your hand was lowered.', { exact: true })).toBeVisible();
    await expect(badge(alice.page, 'alice')).toHaveCount(0);
    await expect(badge(alice.page, 'owner')).toHaveText('1');
    await expect(list.locator('li')).toHaveText([/1\s*Olivia Owner/]);
    await olivia.page.keyboard.press('Escape');

    // Up again, and down together.
    await alice.page.getByRole('button', { name: 'Raise your hand' }).click();
    await expect(chip(olivia.page)).toHaveText(/2 hands raised/);
    await chip(olivia.page).click();
    await olivia.page.getByTestId('hands-list').getByRole('menuitem', { name: 'Lower all' }).click();
    await expect(chip(olivia.page)).toHaveCount(0);
    await expect(chip(alice.page)).toHaveCount(0);
    await expect(badge(alice.page, 'owner')).toHaveCount(0);

    // ── in the corner window ───────────────────────────────────────────────
    await alice.page.getByRole('button', { name: 'Raise your hand' }).click();
    await olivia.page.getByRole('button', { name: 'Minimise the call' }).click();
    const corner = olivia.page.getByTestId('call-window');
    await expect(corner.getByTestId('hand-badge')).toHaveText('1');
    await expect(corner.getByLabel('Microphone off').first()).toBeVisible();
    // A toast about the call sits above the window rather than over it.
    await alice.page.getByRole('button', { name: 'Lower your hand' }).click();
    await alice.page.getByRole('button', { name: 'Raise your hand' }).click();
    const toast = olivia.page.getByText('Alice Archer raised their hand', { exact: true }).last();
    await expect(toast).toBeVisible();
    const cornerBox = (await corner.boundingBox())!;
    const toastBox = (await toast.boundingBox())!;
    expect(toastBox.y + toastBox.height).toBeLessThanOrEqual(cornerBox.y);
    await corner.screenshot({ path: 'test-results/hands-call-window-dark.png' });
    await olivia.page.screenshot({ path: 'test-results/hands-call-window-page-dark.png' });

    // Dragged up to the top, the window is out of the toasts' way, and they stay
    // at the bottom of the screen rather than being pushed off its top.
    const grab = corner.getByText('2 in call');
    const g = (await grab.boundingBox())!;
    await olivia.page.mouse.move(g.x + g.width / 2, g.y + g.height / 2);
    await olivia.page.mouse.down();
    await olivia.page.mouse.move(g.x + g.width / 2, 5, { steps: 10 });
    await olivia.page.mouse.up();
    expect((await corner.boundingBox())!.y).toBeLessThan(20);
    await alice.page.getByRole('button', { name: 'Lower your hand' }).click();
    await alice.page.getByRole('button', { name: 'Raise your hand' }).click();
    const topToast = olivia.page.getByText('Alice Archer raised their hand', { exact: true }).last();
    await expect(topToast).toBeInViewport({ ratio: 1 });
    const topBox = (await topToast.boundingBox())!;
    expect(topBox.y).toBeGreaterThan(800 - 150);
    await olivia.page.screenshot({ path: 'test-results/hands-call-window-top-dark.png' });
    await olivia.page.getByRole('button', { name: 'Expand the call' }).click();

    await olivia.page.getByRole('button', { name: 'Leave the call' }).click();
    await alice.page.getByRole('button', { name: 'Leave the call' }).click();
    await expect(alice.page.getByRole('button', { name: 'Leave the call' })).toHaveCount(0);
    await allGone(owner, call.id);
    expect(olivia.problems).toEqual([]);
    expect(alice.problems).toEqual([]);
    await owner.dispose();
  });

  // A full reconnect is a new connection to LiveKit, which starts with no
  // attributes. The hand used to vanish then, without a word.
  test('a hand stays up, in its place, through a full reconnect', async ({ as }) => {
    test.setTimeout(120_000);
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Hands reconnect ${Date.now()}`, [idOf('alice')]);
    const olivia = await as('owner');
    await joinCall(olivia.page, call.id);

    // Alice's signalling goes through here, so LiveKit can be made to tell her to
    // reconnect from scratch, as it does when one of its servers goes away.
    const alice = await as('alice');
    const port = new URL(process.env.E2E_LIVEKIT_URL ?? 'http://localhost:55464').port;
    const signals: { send: (m: Buffer) => void }[] = [];
    await alice.page.routeWebSocket(new RegExp(`:${port}/rtc`), (ws) => {
      const server = ws.connectToServer();
      server.onMessage((m) => ws.send(m));
      signals.push(ws);
    });
    await joinCall(alice.page, call.id);
    await expect(tile(olivia.page, 'alice')).toBeVisible({ timeout: 20_000 });

    const raised = alice.page.waitForResponse((r) => r.url().endsWith(`/calls/${call.id}/hand`) && r.request().method() === 'PUT');
    await alice.page.getByRole('button', { name: 'Raise your hand' }).click();
    const firstUp: string = (await (await raised).json()).raisedAt;
    await expect(badge(olivia.page, 'alice')).toHaveText('1');
    await olivia.page.keyboard.press('Control+Shift+H');
    await expect(badge(olivia.page, 'owner')).toHaveText('2');

    // SignalResponse { leave (8): LeaveRequest { action (3): RECONNECT (2) } }
    const before = signals.length;
    signals[before - 1].send(Buffer.from([0x42, 0x02, 0x18, 0x02]));
    await expect.poll(() => signals.length, { message: 'Alice to connect again', timeout: 20_000 }).toBeGreaterThan(before);
    // Long enough for the new connection to settle, and for everyone to hear about it.
    await alice.page.waitForTimeout(4_000);

    // Still up, still first, on both screens, and nobody was told it came down.
    await expect(badge(olivia.page, 'alice')).toHaveText('1', { timeout: 20_000 });
    await expect(badge(olivia.page, 'owner')).toHaveText('2');
    await expect(badge(alice.page, 'alice')).toHaveText('1');
    await expect(alice.page.getByRole('button', { name: 'Lower your hand' })).toBeVisible();
    await expect(alice.page.getByText('Your hand was lowered.', { exact: true })).toHaveCount(0);
    // And on the server: asking again gives back the time it first went up.
    const again = await alice.page.request.put(`/api/calls/${call.id}/hand`, { data: { raised: true } });
    expect(again.ok()).toBe(true);
    expect((await again.json()).raisedAt).toBe(firstUp);

    await olivia.page.getByRole('button', { name: 'Leave the call' }).click();
    await alice.page.getByRole('button', { name: 'Leave the call' }).click();
    await expect(alice.page.getByRole('button', { name: 'Leave the call' })).toHaveCount(0);
    await allGone(owner, call.id);
    expect(olivia.problems).toEqual([]);
    expect(alice.problems).toEqual([]);
    await owner.dispose();
  });

  // A reload is a new connection, with no hand on it. The tab keeps the hand's
  // proof, so coming back into the call puts it back where it was.
  test('a raised hand comes back in its place after a reload, and a lowered one does not', async ({ as }) => {
    test.setTimeout(150_000);
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Hands reload ${Date.now()}`, [idOf('sam')]);
    const olivia = await as('owner');
    await joinCall(olivia.page, call.id);
    const sam = await as('sam');
    await joinCall(sam.page, call.id);
    await expect(tile(olivia.page, 'sam')).toBeVisible({ timeout: 20_000 });

    const raised = sam.page.waitForResponse((r) => r.url().endsWith(`/calls/${call.id}/hand`) && r.request().method() === 'PUT');
    await sam.page.getByRole('button', { name: 'Raise your hand' }).click();
    const firstUp: string = (await (await raised).json()).raisedAt;
    await expect(badge(olivia.page, 'sam')).toHaveText('1');
    await olivia.page.keyboard.press('Control+Shift+H');
    await expect(badge(olivia.page, 'owner')).toHaveText('2');
    // Nothing that lets anybody in is kept, only the hand.
    const stored = await sam.page.evaluate(() => JSON.stringify(sessionStorage));
    expect(stored).toContain(firstUp);
    expect(stored).not.toMatch(/eyJ[A-Za-z0-9_-]{10,}\./);

    await sam.page.reload();
    await joinCall(sam.page, call.id);
    await expect(badge(olivia.page, 'sam')).toHaveText('1', { timeout: 20_000 });
    await expect(badge(olivia.page, 'owner')).toHaveText('2');
    await expect(badge(sam.page, 'sam')).toHaveText('1');
    await expect(sam.page.getByRole('button', { name: 'Lower your hand' })).toBeVisible();
    const again = await sam.page.request.put(`/api/calls/${call.id}/hand`, { data: { raised: true, refresh: true } });
    expect((await again.json()).raisedAt).toBe(firstUp);
    await expect(sam.page.getByText('at the back of the line', { exact: false })).toHaveCount(0);

    // Lowered, then reloaded: it stays down.
    await sam.page.getByRole('button', { name: 'Lower your hand' }).click();
    await expect(badge(olivia.page, 'sam')).toHaveCount(0);
    await expect(badge(olivia.page, 'owner')).toHaveText('1');
    await sam.page.reload();
    await joinCall(sam.page, call.id);
    await expect(tile(olivia.page, 'sam')).toBeVisible({ timeout: 20_000 });
    await sam.page.waitForTimeout(3_000);
    await expect(badge(olivia.page, 'sam')).toHaveCount(0);
    await expect(badge(sam.page, 'sam')).toHaveCount(0);
    await expect(badge(olivia.page, 'owner')).toHaveText('1');

    await olivia.page.getByRole('button', { name: 'Leave the call' }).click();
    await sam.page.getByRole('button', { name: 'Leave the call' }).click();
    await expect(sam.page.getByRole('button', { name: 'Leave the call' })).toHaveCount(0);
    await allGone(owner, call.id);
    expect(olivia.problems).toEqual([]);
    expect(sam.problems).toEqual([]);
    await owner.dispose();
  });

  test('with five people on a desktop, the chip covers no tile', async ({ as }) => {
    test.setTimeout(150_000);
    const owner = await apiAs('owner');
    const others: PersonKey[] = ['alice', 'sam', 'mia', 'max'];
    const call = await createCall(owner, `Hands five ${Date.now()}`, others.map(idOf));
    const olivia = await as('owner');
    await joinCall(olivia.page, call.id);
    const sessions = [];
    for (const who of others) {
      const s = await as(who);
      await joinCall(s.page, call.id);
      sessions.push(s);
    }
    await expect(olivia.page.getByTestId('participant-tile')).toHaveCount(5, { timeout: 30_000 });

    await sessions[0].page.getByRole('button', { name: 'Raise your hand' }).click();
    await olivia.page.keyboard.press('Control+Shift+H');
    await expect(chip(olivia.page)).toHaveText(/2 hands raised/);
    // Hovering a tile shows its buttons, which the chip used to cover.
    await tile(olivia.page, 'alice').hover();
    expect(await underTheChip(olivia.page)).toEqual([]);
    await olivia.page.screenshot({ path: 'test-results/hands-five-desktop.png' });

    for (const s of [olivia, ...sessions]) await s.page.getByRole('button', { name: 'Leave the call' }).click();
    await allGone(owner, call.id);
    await owner.dispose();
  });

  test("a guest on a phone sees hands, the chip in its own row, and toasts above the controls", async ({ as }) => {
    test.setTimeout(150_000);
    const owner = await apiAs('owner');
    const title = `Hands guest ${Date.now()}`;
    const created = await owner.post('/meetings', {
      title,
      startsAt: new Date(Date.now() + 10 * 60_000).toISOString(),
      endsAt: new Date(Date.now() + 40 * 60_000).toISOString(),
      inviteeIds: [],
    });
    const id = created.meeting.id as string;
    const link = await owner.put(`/calls/${id}/guest-link`, { enabled: true });

    const olivia = await as('owner');
    await olivia.page.goto(`/calls/${id}`);
    await olivia.page.getByRole('button', { name: /^Join( now)?$/ }).click();
    await expect(olivia.page.getByRole('button', { name: 'Leave the call' })).toBeVisible({ timeout: 30_000 });

    const guest = await as('sam', { signedOut: true, phone: 'android', theme: 'dark' });
    await guest.page.goto(link.directPath);
    await guest.page.getByLabel('Your name').fill('Gita Guest');
    await guest.page.getByRole('button', { name: 'Join the meeting' }).click();
    await expect(guest.page.getByRole('button', { name: 'Leave', exact: true })).toBeVisible({ timeout: 30_000 });
    await expect(olivia.page.getByText('Gita Guest (guest)', { exact: true })).toBeVisible({ timeout: 20_000 });
    // No button for a guest to raise a hand with.
    await expect(guest.page.getByRole('button', { name: 'Raise your hand' })).toHaveCount(0);

    await olivia.page.keyboard.press('Control+Shift+H');
    await expect(badge(guest.page, 'owner')).toHaveText('1');
    await expect(chip(guest.page)).toHaveText(/1 hand raised/);
    expect(await underTheChip(guest.page)).toEqual([]);

    const toast = guest.page.getByText('Olivia Owner raised their hand', { exact: true }).first();
    await expect(toast).toBeVisible();
    const controls = (await guest.page.getByRole('group', { name: 'Call controls' }).boundingBox())!;
    const toastBox = (await toast.boundingBox())!;
    expect(toastBox.y + toastBox.height).toBeLessThanOrEqual(controls.y);
    await guest.page.screenshot({ path: 'test-results/hands-guest-phone-dark.png' });

    await guest.page.getByRole('button', { name: 'Leave', exact: true }).click();
    await olivia.page.getByRole('button', { name: 'Leave the call' }).click();
    await owner.dispose();
  });
});
