import { readFileSync } from 'node:fs';
import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { apiAs, idOf } from './support/meetings';
import { readState } from './support/people';

const PICTURE = readFileSync(new URL('./fixtures/picture.png', import.meta.url)).toString('base64');

/** Puts a picture on the system clipboard, as taking a screenshot does. */
async function screenshotToClipboard(page: Page) {
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write']);
  await page.evaluate(async (b64) => {
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
    await navigator.clipboard.write([new ClipboardItem({ 'image/png': new Blob([bytes], { type: 'image/png' }) })]);
  }, PICTURE);
}

// The owner's request of 17 November 2026: take a screenshot, press Ctrl+V in
// the message box, and it is attached, ready to send.
test('a screenshot pasted into a message box is attached and sent', async ({ as }) => {
  const olivia = await as('owner');
  const page = olivia.page;
  const { channelId } = readState();
  await page.goto(`/chat/${channelId}`);
  const box = page.getByRole('textbox', { name: /Message/ }).first();
  await box.click();
  await screenshotToClipboard(page);
  await page.keyboard.press('ControlOrMeta+V');
  await expect(page.getByText('Attached. Press Send to share.')).toBeVisible({ timeout: 30_000 });
  const preview = page.getByRole('button', { name: /^Preview Screenshot \d{4}-\d\d-\d\d \d\d\.\d\d\.\d\d\.png$/ }).first();
  await expect(preview).toBeVisible();
  // The box is still empty: the picture was not pasted as text.
  await expect(box).toHaveValue('');

  const stamp = Date.now();
  await box.fill(`see this ${stamp}`);
  await page.getByRole('button', { name: 'Send', exact: true }).click();
  const sent = page.getByText(`see this ${stamp}`).locator('xpath=ancestor::div[.//img][1]');
  await expect(sent.getByRole('button', { name: /^Open Screenshot .*\.png$/ }).first()).toBeVisible({ timeout: 15_000 });
  await page.screenshot({ path: 'test-results/paste-chat.png' });
});

test('text copied with a picture of itself, as from a spreadsheet, pastes as text', async ({ as }) => {
  const olivia = await as('owner');
  const page = olivia.page;
  const { channelId } = readState();
  await page.goto(`/chat/${channelId}`);
  const box = page.getByRole('textbox', { name: /Message/ }).first();
  await box.click();
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write']);
  await page.evaluate(async (b64) => {
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
    await navigator.clipboard.write([
      new ClipboardItem({
        'text/plain': new Blob(['Q3\t120'], { type: 'text/plain' }),
        'image/png': new Blob([bytes], { type: 'image/png' }),
      }),
    ]);
  }, PICTURE);
  await page.keyboard.press('ControlOrMeta+V');
  await expect(box).toHaveValue('Q3\t120');
  await expect(page.getByText('Attached. Press Send to share.')).toBeHidden();
  await box.fill('');
});

test('a screenshot pasted into a task comment is attached to it', async ({ as }) => {
  const owner = await apiAs('owner');
  const task = await owner.post('/tasks', { title: `Pasted ${Date.now()}`, assigneeId: idOf('alice') });
  try {
    const { page } = await as('owner');
    await page.goto(`/tasks/${task.id}`);
    await page.getByRole('button', { name: 'Add Comment' }).click();
    const box = page.getByRole('textbox', { name: 'Comment' });
    await box.click();
    await screenshotToClipboard(page);
    await page.keyboard.press('ControlOrMeta+V');
    await expect(page.getByRole('button', { name: /^Preview Screenshot .*\.png$/ }).first()).toBeVisible({ timeout: 30_000 });
    await box.fill('the roof, as I found it');
    await page.getByRole('button', { name: 'Post comment' }).click();
    const comment = page.locator('li', { hasText: 'the roof, as I found it' });
    await expect(comment.getByRole('button', { name: /^Open Screenshot .*\.png$/ }).first()).toBeVisible({ timeout: 15_000 });
  } finally {
    await owner.del(`/tasks/${task.id}`).catch(() => {});
    await owner.dispose();
  }
});
