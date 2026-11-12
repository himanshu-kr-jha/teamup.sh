import { expect, test } from './support/fixtures';
import { sql } from './support/meetings';
import { LATEST, RELEASES } from '../src/features/whatsnew/releases';

// What's new: after an update it opens by itself, once, with what changed, on
// whichever device the person uses next; it is always there from the menu under
// their picture; and somebody new, who has just been shown around, is not told
// about changes to an app they have never seen before.

test.describe("what's new", () => {
  test('opens by itself once after an update, and stays closed after a reload', async ({ as }) => {
    sql(`UPDATE users SET whats_new_seen = NULL WHERE email = 'alice@e2e.test'`);
    const alice = await as('alice', { whatsNew: true });
    const dialog = alice.page.getByTestId('whats-new-dialog');
    await expect(dialog).toBeVisible({ timeout: 10_000 });
    await expect(dialog).toContainText(RELEASES[0].title);
    await expect(dialog.getByTestId('whats-new-change')).toHaveCount(RELEASES[0].changes.length);
    await dialog.getByRole('button', { name: 'Got it' }).click();
    await expect(dialog).toHaveCount(0);
    await expect.poll(() => sql(`SELECT whats_new_seen FROM users WHERE email = 'alice@e2e.test'`)).toBe(LATEST);

    await alice.page.reload();
    await alice.page.waitForTimeout(2000);
    await expect(dialog).toHaveCount(0);
  });

  test('somebody who missed several updates sees them all, and All updates opens the page', async ({ as }) => {
    test.skip(RELEASES.length < 2, 'needs two updates');
    sql(`UPDATE users SET whats_new_seen = '${RELEASES[RELEASES.length - 1].id}' WHERE email = 'ann@e2e.test'`);
    const ann = await as('ann', { whatsNew: true });
    const dialog = ann.page.getByTestId('whats-new-dialog');
    await expect(dialog).toBeVisible({ timeout: 10_000 });
    await expect(dialog).toContainText(`${RELEASES.length - 1} updates since you last looked.`);
    const missed = RELEASES.slice(0, -1).reduce((n, r) => n + r.changes.length, 0);
    await expect(dialog.getByTestId('whats-new-change')).toHaveCount(missed);
    await dialog.getByRole('button', { name: 'All updates' }).click();
    await expect(ann.page).toHaveURL(/\/whats-new$/);
    await expect(ann.page.getByRole('heading', { name: 'What’s new in TeamUp' })).toBeVisible();
    await expect(ann.page.getByTestId('whats-new-release')).toHaveCount(RELEASES.length);
    await expect.poll(() => sql(`SELECT whats_new_seen FROM users WHERE email = 'ann@e2e.test'`)).toBe(LATEST);
    expect(await ann.page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  });

  test('is in the menu under your picture, and fits a phone', async ({ as }) => {
    const pat = await as('pat', { width: 390, height: 800 });
    await pat.page.goto('/chat');
    await pat.page.getByRole('button', { name: 'User menu' }).click();
    await pat.page.getByRole('menuitem', { name: 'What’s new' }).click();
    await expect(pat.page).toHaveURL(/\/whats-new$/);
    await expect(pat.page.getByTestId('whats-new-release').first()).toBeVisible();
    expect(await pat.page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  });

  test('somebody new is shown around, and then not told what is new', async ({ as }) => {
    sql(`UPDATE users SET tour_completed_at = NULL, whats_new_seen = NULL WHERE email = 'max@e2e.test'`);
    const max = await as('max', { tour: true, whatsNew: true });
    await expect(max.page.getByTestId('welcome-tour')).toBeVisible({ timeout: 10_000 });
    await max.page.getByRole('button', { name: 'Skip the tour' }).click();
    await expect.poll(() => sql(`SELECT whats_new_seen FROM users WHERE email = 'max@e2e.test'`)).toBe(LATEST);
    await max.page.waitForTimeout(2000);
    await expect(max.page.getByTestId('whats-new-dialog')).toHaveCount(0);
  });
});
