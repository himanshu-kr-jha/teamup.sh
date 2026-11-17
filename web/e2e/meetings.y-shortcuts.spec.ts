import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { apiAs, createCall, idOf, joinCall, leaveCall } from './support/meetings';
import { TASKS } from './support/people';

// Asked for: shortcut keys for everyday work, so TeamUp is not mouse
// dependent. Ctrl+D and Ctrl+E in a call, Space to talk, G then a letter to go
// places, Ctrl+K to find anything, ? for the list, and a switch for single keys.

const SHOTS = 'test-results/shortcuts';

/** Nothing focused, as after clicking on an empty part of the page. */
const blur = (page: Page) => page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());

/** Microphone-off marks on the tiles a page shows: one per person who cannot be heard. */
const mutedTiles = (page: Page) => page.getByLabel('Microphone off', { exact: true });

test.describe('keyboard shortcuts', () => {
  test('in a call, Ctrl+D, Ctrl+E, Space and Ctrl+Alt+C work the call, also from the corner window', async ({ as }) => {
    test.setTimeout(150_000);
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Keys ${Date.now()}`, [idOf('alice')]);
    const olivia = await as('owner');
    await joinCall(olivia.page, call.id);
    const alice = await as('alice');
    await joinCall(alice.page, call.id);
    await expect(olivia.page.getByText('Alice Archer', { exact: true }).first()).toBeVisible({ timeout: 20_000 });

    const page = olivia.page;
    const micOn = page.getByRole('button', { name: 'Turn your microphone on' });
    const micOff = page.getByRole('button', { name: 'Turn your microphone off' });
    // A meeting starts muted, for both of them.
    await expect(micOn).toBeVisible({ timeout: 20_000 });
    await expect(mutedTiles(alice.page)).toHaveCount(2, { timeout: 20_000 });

    // The button says its key, to the eye and to a screen reader.
    await expect(micOn).toHaveAttribute('title', 'Turn your microphone on (Ctrl+D)');
    await expect(micOn).toHaveAttribute('aria-keyshortcuts', 'Control+D');

    await blur(page);
    await page.keyboard.press('Control+d');
    await expect(micOff).toBeVisible();
    // Published, not only drawn: Alice no longer sees Olivia as muted.
    await expect(mutedTiles(alice.page)).toHaveCount(1, { timeout: 20_000 });
    await page.keyboard.press('Control+d');
    await expect(micOn).toBeVisible();
    await expect(mutedTiles(alice.page)).toHaveCount(2, { timeout: 20_000 });

    await page.keyboard.press('Control+e');
    await expect(page.getByRole('button', { name: 'Turn your camera off' })).toBeVisible({ timeout: 20_000 });
    await expect
      .poll(() => alice.page.evaluate(() => Array.from(document.querySelectorAll('video')).some((v) => v.videoWidth > 0)), {
        timeout: 20_000,
      })
      .toBe(true);
    await page.keyboard.press('Control+e');
    await expect(page.getByRole('button', { name: 'Turn your camera on' })).toBeVisible({ timeout: 20_000 });

    // Push to talk: heard while Space is held, muted again when it is let go.
    await blur(page);
    await page.keyboard.down('Space');
    await expect(micOff).toBeVisible();
    await expect(mutedTiles(alice.page)).toHaveCount(1, { timeout: 20_000 });
    await page.keyboard.up('Space');
    await expect(micOn).toBeVisible();
    await expect(mutedTiles(alice.page)).toHaveCount(2, { timeout: 20_000 });

    const chatButton = page.getByTestId('meeting-chat-button');
    await expect(chatButton).toHaveAttribute('aria-keyshortcuts', 'Control+Alt+C');
    await page.keyboard.press('Control+Alt+c');
    await expect(chatButton).toHaveAttribute('aria-pressed', 'true');
    // From inside the chat's own message box too, which leaves the key alone.
    await page.getByRole('textbox', { name: 'Message to everyone in the call' }).click();
    await page.keyboard.press('Control+Alt+c');
    await expect(chatButton).toHaveAttribute('aria-pressed', 'false');

    // G then C leaves the call in its corner, where Ctrl+D still works it.
    await blur(page);
    await page.keyboard.press('g');
    await page.keyboard.press('c');
    await expect(page).toHaveURL(/\/chat$/);
    const corner = page.getByTestId('call-window');
    await expect(corner).toBeVisible();
    await page.keyboard.press('Control+d');
    await expect(corner.getByRole('button', { name: 'Turn your microphone off' })).toBeVisible();
    await page.keyboard.press('Control+d');
    await expect(corner.getByRole('button', { name: 'Turn your microphone on' })).toBeVisible();

    await leaveCall(alice.page);
    await leaveCall(page);
  });

  test('G then a letter goes places, but not while typing; ? lists every shortcut', async ({ as }) => {
    const olivia = await as('owner');
    const page = olivia.page;
    const owner = await apiAs('owner');
    const dm = await owner.post('/channels/direct', { userId: idOf('alice') });

    await page.goto('/chat');
    await blur(page);
    await page.keyboard.press('g');
    await page.keyboard.press('t');
    await expect(page).toHaveURL(/\/tasks/);
    await page.keyboard.press('g');
    await page.keyboard.press('k');
    await expect(page).toHaveURL(/\/calendar/);
    await page.keyboard.press('g');
    await page.keyboard.press('c');
    await expect(page).toHaveURL(/\/chat$/);

    // Writing "g t" in a message is writing, not going to Tasks.
    await page.goto(`/chat/${dm.id}`);
    const box = page.getByRole('textbox', { name: 'Message' });
    await box.click();
    await page.keyboard.type('g t');
    await expect(box).toHaveValue('g t');
    await page.waitForTimeout(300);
    await expect(page).toHaveURL(new RegExp(`/chat/${dm.id}$`));
    await box.fill('');

    // ? opens the list, and ? or Esc closes it.
    await blur(page);
    await page.keyboard.press('?');
    const sheet = page.getByTestId('shortcut-sheet');
    await expect(sheet).toBeVisible();
    await expect(sheet.getByRole('heading', { name: 'Keyboard shortcuts' })).toBeVisible();
    for (const section of ['Anywhere', 'Go to', 'In a call', 'Tasks', 'Chat']) {
      await expect(sheet.getByRole('heading', { name: section, exact: true })).toBeVisible();
    }
    await expect(sheet.getByText('Turn your microphone on or off')).toBeVisible();
    await page.waitForTimeout(250);
    await sheet.screenshot({ path: `${SHOTS}/cheat-sheet-desktop.png` });
    await page.keyboard.press('?');
    await expect(sheet).toBeHidden();
    await page.keyboard.press('Control+/');
    await expect(sheet).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(sheet).toBeHidden();

    // From the menu too, for whoever does not know the key yet.
    await page.getByRole('button', { name: 'User menu' }).click();
    await page.getByRole('menuitem', { name: /Keyboard shortcuts/ }).click();
    await expect(sheet).toBeVisible();
  });

  test('the cheat sheet fits a phone without scrolling sideways', async ({ as }) => {
    const sam = await as('sam', { width: 390, height: 844 });
    const page = sam.page;
    await page.goto('/chat');
    await page.getByRole('button', { name: 'User menu' }).click();
    await page.getByRole('menuitem', { name: /Keyboard shortcuts/ }).click();
    const sheet = page.getByTestId('shortcut-sheet');
    await expect(sheet).toBeVisible();
    await page.waitForTimeout(250);
    const sideways = await sheet.evaluate((el) => el.scrollWidth - el.clientWidth);
    expect(sideways).toBe(0);
    const b = (await sheet.boundingBox())!;
    expect(b.x).toBeGreaterThanOrEqual(0);
    expect(b.x + b.width).toBeLessThanOrEqual(390);
    // A member who cannot open the Organization screen is not offered a key for it.
    await expect(sheet.getByText('Organization', { exact: true })).toHaveCount(0);
    await page.screenshot({ path: `${SHOTS}/cheat-sheet-phone.png` });
    await sheet.evaluate((el) => el.scrollTo(0, el.scrollHeight));
    await page.screenshot({ path: `${SHOTS}/cheat-sheet-phone-end.png` });
  });

  test('Ctrl+K finds a person and a task, and opens them', async ({ as }) => {
    const olivia = await as('owner');
    const page = olivia.page;
    await page.goto('/tasks');

    await page.keyboard.press('Control+k');
    const palette = page.getByTestId('command-palette');
    await expect(palette).toBeVisible();
    const input = palette.getByRole('combobox');
    await expect(input).toBeFocused();
    // With nothing typed, the places to go.
    await expect(palette.getByRole('option', { name: 'Calendar' })).toBeVisible();
    await page.waitForTimeout(250);
    await palette.screenshot({ path: `${SHOTS}/palette-empty-desktop.png` });

    await input.fill('alice');
    await expect(palette.getByRole('option', { name: /Alice Archer/ })).toBeVisible();
    await page.waitForTimeout(150);
    await palette.screenshot({ path: `${SHOTS}/palette-desktop.png` });
    // People come first, so Enter opens a conversation with her.
    await page.keyboard.press('Enter');
    await expect(palette).toBeHidden();
    await expect(page).toHaveURL(/\/chat\/[0-9a-f-]+$/);
    await expect(page.getByRole('heading', { name: /Alice Archer/ })).toBeVisible();

    await page.keyboard.press('Control+k');
    await input.fill(TASKS.patCerts);
    const task = palette.getByRole('option', { name: new RegExp(TASKS.patCerts) });
    await expect(task).toBeVisible();
    await task.click();
    await expect(page).toHaveURL(/\/tasks\/[0-9a-f-]+$/);
    await expect(page.getByRole('heading', { name: TASKS.patCerts })).toBeVisible();

    // On the task: S opens the status, E edits, M writes a comment.
    await blur(page);
    await page.keyboard.press('s');
    await expect(page.getByRole('listbox')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('listbox')).toBeHidden();
    await blur(page);
    await page.keyboard.press('e');
    const title = page.getByRole('main').locator('input.text-2xl');
    await expect(title).toBeFocused();
    await expect(title).toHaveValue(TASKS.patCerts);
    await page.keyboard.press('Escape');
    await expect(page.getByRole('heading', { name: TASKS.patCerts })).toBeVisible();
    await blur(page);
    await page.keyboard.press('m');
    await expect(page.getByRole('textbox', { name: 'Comment' })).toBeFocused();
    await expect(page.getByRole('textbox', { name: 'Comment' })).toHaveValue('');
  });

  test('the palette fits a phone', async ({ as }) => {
    const sam = await as('sam', { width: 390, height: 844 });
    const page = sam.page;
    await page.goto('/chat');
    await page.keyboard.press('Control+k');
    const palette = page.getByTestId('command-palette');
    await palette.getByRole('combobox').fill('a');
    await expect(palette.getByRole('option').first()).toBeVisible();
    await page.waitForTimeout(250);
    const b = (await palette.boundingBox())!;
    expect(b.x).toBeGreaterThanOrEqual(0);
    expect(b.x + b.width).toBeLessThanOrEqual(390);
    expect(await palette.evaluate((el) => el.scrollWidth - el.clientWidth)).toBe(0);
    await page.screenshot({ path: `${SHOTS}/palette-phone.png` });
  });

  test('C on the task board opens New task, and / searches the board', async ({ as }) => {
    const alice = await as('alice');
    const page = alice.page;
    await page.goto('/tasks?tab=board');
    await expect(page.getByRole('textbox', { name: 'Search tasks' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'New Task' })).toHaveAttribute('aria-keyshortcuts', 'C');
    await blur(page);
    await page.keyboard.press('c');
    const dialog = page.getByRole('dialog', { name: 'New task' });
    await expect(dialog).toBeVisible();
    // The key opened it; it did not type itself into the form.
    await expect(dialog.getByLabel('Title')).toHaveValue('');
    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();

    await blur(page);
    await page.keyboard.press('/');
    await expect(page.getByRole('textbox', { name: 'Search tasks' })).toBeFocused();
    await expect(page.getByRole('textbox', { name: 'Search tasks' })).toHaveValue('');
  });

  test('Alt+Down and Alt+Up move between conversations, from the message box too', async ({ as }) => {
    const owner = await apiAs('owner');
    await owner.post('/channels/direct', { userId: idOf('alice') });
    await owner.post('/channels/direct', { userId: idOf('mia') });
    const olivia = await as('owner');
    const page = olivia.page;
    await page.goto('/chat');
    const list = page.getByRole('main').locator('aside .space-y-0\\.5 > button');
    await expect(list.nth(1)).toBeVisible();
    const names = (await list.locator('span.flex-1').allInnerTexts()).map((t) => t.trim());

    await list.nth(0).click();
    await expect(page.getByRole('heading', { name: names[0] })).toBeVisible();
    const first = page.url();
    await page.getByRole('textbox', { name: 'Message' }).click();
    await page.keyboard.press('Alt+ArrowDown');
    await expect(page.getByRole('heading', { name: names[1] })).toBeVisible();
    expect(page.url()).not.toBe(first);
    await page.keyboard.press('Alt+ArrowUp');
    await expect(page.getByRole('heading', { name: names[0] })).toBeVisible();
    expect(page.url()).toBe(first);

    // Alt+Shift+Down skips to the nearest conversation with something unread.
    const alice = await apiAs('alice');
    const dm = await alice.post('/channels/direct', { userId: idOf('owner') });
    await alice.post(`/channels/${dm.id}/messages`, { content: 'Unread, for the keys', tempId: `keys-${Date.now()}` });
    // The count beside a conversation's name; a presence dot is round too, so it is told apart by its width.
    const badge = 'span[class*="min-w-[1.25rem]"]';
    const unreadMark = list.filter({ has: page.locator(badge) });
    await expect(unreadMark.first()).toBeVisible({ timeout: 15_000 });
    const expected = await list.evaluateAll((buttons, badge) => {
      const at = buttons.findIndex((b) => b.className.includes('bg-primary'));
      for (let i = 1; i <= buttons.length; i++) {
        const b = buttons[(at + i) % buttons.length];
        if (b.querySelector(badge)) return b.querySelector('span.flex-1')?.textContent?.trim() ?? '';
      }
      return '';
    }, badge);
    expect(expected).not.toBe('');
    await page.getByRole('textbox', { name: 'Message' }).click();
    await page.keyboard.press('Alt+Shift+ArrowDown');
    await expect(page.getByRole('heading', { name: expected })).toBeVisible();
  });

  test('turning single keys off in Settings keeps Ctrl+K but not G or ?', async ({ as }) => {
    const ann = await as('ann');
    const page = ann.page;
    await page.goto('/settings?tab=appearance');
    const toggle = page.getByRole('switch', { name: 'Single-key shortcuts' });
    await expect(toggle).toBeChecked();
    await toggle.click();
    await expect(toggle).not.toBeChecked();

    await blur(page);
    await page.keyboard.press('g');
    await page.keyboard.press('t');
    await page.keyboard.press('?');
    await page.waitForTimeout(300);
    await expect(page).toHaveURL(/\/settings/);
    await expect(page.getByTestId('shortcut-sheet')).toHaveCount(0);

    await page.keyboard.press('Control+k');
    await expect(page.getByTestId('command-palette')).toBeVisible();
    await page.keyboard.press('Escape');

    // Remembered on this device, through a reload.
    await page.reload();
    await expect(page.getByRole('switch', { name: 'Single-key shortcuts' })).not.toBeChecked();
    await page.getByRole('switch', { name: 'Single-key shortcuts' }).click();
    await blur(page);
    await page.keyboard.press('g');
    await page.keyboard.press('t');
    await expect(page).toHaveURL(/\/tasks/);
  });
});
