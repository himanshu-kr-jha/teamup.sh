import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { apiAs, createCall, idOf, joinCall } from './support/meetings';

// Asked for: a microphone that moves while somebody speaks, as in Google Meet, and
// raising a hand. The hand is a LiveKit participant attribute that only the server
// writes, so what one browser shows is what every browser shows.
//
// Chromium's fake microphone plays e2e/fixtures/speech.wav, so a microphone that
// is on is somebody talking. A video call starts with everybody's microphone off.

const tile = (page: Page, who: 'owner' | 'alice') =>
  page.locator(`[data-testid="participant-tile"][data-identity="${idOf(who)}"]`).first();
const bars = (page: Page, who: 'owner' | 'alice') => tile(page, who).getByTestId('speaking-bars');
const badge = (page: Page, who: 'owner' | 'alice') => tile(page, who).getByTestId('hand-badge');
const chip = (page: Page) => page.getByTestId('hands-chip');

/** The tallest the three bars on a tile have been over a short while, in pixels. */
async function tallestBar(page: Page, who: 'owner' | 'alice', forMs = 1500): Promise<number> {
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
    await olivia.page.emulateMedia({ reducedMotion: 'reduce' });
    await expect(bars(olivia.page, 'alice')).toHaveAttribute('data-speaking', 'true', { timeout: 20_000 });
    const still = await bars(olivia.page, 'alice').evaluate((el) =>
      Array.from(el.children).map((b) => Math.round((b as HTMLElement).getBoundingClientRect().height))
    );
    expect(still).toEqual([8, 12, 8]);
    await olivia.page.emulateMedia({ reducedMotion: 'no-preference' });

    // ── raising a hand while talking: it comes down by itself ──────────────
    const raise = alice.page.getByRole('button', { name: 'Raise your hand' });
    await expect(raise).toHaveAttribute('title', 'Raise your hand (Ctrl+Shift+H)');
    await raise.click();
    await expect(olivia.page.getByText('Alice Archer raised their hand', { exact: true }).first()).toBeVisible();
    await expect(alice.page.getByText('Your hand was lowered because you started speaking.', { exact: true })).toBeVisible({ timeout: 15_000 });
    await expect(badge(olivia.page, 'alice')).toHaveCount(0);
    await expect(alice.page.getByRole('button', { name: 'Raise your hand' })).toBeVisible();

    // Muted again: no bars.
    await aliceMic.click();
    await expect(tile(olivia.page, 'alice').getByLabel('Microphone off')).toBeVisible();
    await expect(bars(olivia.page, 'alice')).toHaveCount(0);

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
    await olivia.page.getByRole('button', { name: 'Expand the call' }).click();

    await olivia.page.getByRole('button', { name: 'Leave the call' }).click();
    await alice.page.getByRole('button', { name: 'Leave the call' }).click();
    await expect(alice.page.getByRole('button', { name: 'Leave the call' })).toHaveCount(0);
    await allGone(owner, call.id);
    expect(olivia.problems).toEqual([]);
    expect(alice.problems).toEqual([]);
    await owner.dispose();
  });
});
