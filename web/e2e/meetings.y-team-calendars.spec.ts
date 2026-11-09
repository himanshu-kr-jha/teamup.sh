import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { PEOPLE } from './support/people';
import { apiAs, at, dayParam, idOf, iso } from './support/meetings';

// Ticket 57, decision 64: a manager opens the calendars of the people on the teams
// they run, and teams nested under them, read-only, from a picker on the
// calendar page. Mia runs Backend (Alice, Ann) and Platform beneath it (Pat).

const DAY = 11;
const stamp = Date.now();
const ALICE_BLOCK = `Alice writing ${stamp}`;
const PAT_BLOCK = `Pat on call ${stamp}`;
const SAM_BLOCK = `Sam prospecting ${stamp}`;

const picker = (page: Page) => page.getByRole('combobox', { name: 'Whose calendar' });
const event = (page: Page, title: string) => page.getByRole('main').getByRole('button', { name: new RegExp(title) });
const banner = (page: Page) => page.getByTestId('viewing-banner');

test.describe('team calendars', () => {
  const made: { who: 'alice' | 'pat' | 'sam'; id: string }[] = [];

  test.beforeAll(async () => {
    for (const [who, title] of [['alice', ALICE_BLOCK], ['pat', PAT_BLOCK], ['sam', SAM_BLOCK]] as const) {
      const api = await apiAs(who);
      const res = await api.post('/calendar/blocks', { kind: 'focus', title, startsAt: iso(at(DAY, 10)), endsAt: iso(at(DAY, 11)) });
      made.push({ who, id: res.block.id });
      await api.dispose();
    }
  });

  test.afterAll(async () => {
    for (const { who, id } of made) {
      const api = await apiAs(who);
      await api.del(`/calendar/blocks/${id}`);
      await api.dispose();
    }
  });

  test('a manager picks somebody on their teams and reads their calendar, in every view, without changing it', async ({ as }) => {
    const mia = await as('mia');
    await mia.page.goto(`/calendar?view=week&date=${dayParam(at(DAY, 0))}`);
    await expect(picker(mia.page)).toHaveText('My calendar');
    await expect(banner(mia.page)).toHaveCount(0);

    // The picker: herself, then her teams, and nobody else's people.
    await picker(mia.page).click();
    const listbox = mia.page.getByRole('listbox');
    await expect(listbox.getByRole('group')).toHaveCount(2);
    await expect(listbox.getByRole('group').filter({ hasText: 'Backend' }).getByRole('option')).toHaveText([PEOPLE.alice.name, PEOPLE.ann.name]);
    await expect(listbox.getByRole('group').filter({ hasText: 'Platform' }).getByRole('option')).toHaveText([PEOPLE.pat.name]);
    await expect(listbox.getByRole('option', { name: PEOPLE.sam.name })).toHaveCount(0);
    await listbox.getByRole('option', { name: PEOPLE.alice.name }).click();

    // Whose it is, said plainly, and in the address.
    await expect(mia.page).toHaveURL(new RegExp(`person=${idOf('alice')}`));
    await expect(banner(mia.page)).toContainText(`${PEOPLE.alice.name}'s calendar, read-only`);
    await expect(picker(mia.page)).toHaveText(PEOPLE.alice.name);
    await expect(event(mia.page, ALICE_BLOCK)).toBeVisible();

    // Read-only: nothing to block, nothing happens on an empty slot, and her block
    // opens to be read with nothing to press.
    await expect(mia.page.getByRole('button', { name: 'Block time' })).toHaveCount(0);
    const day = mia.page.getByRole('group', { name: new RegExp(`${at(DAY, 0).toLocaleDateString('en-US', { month: 'long' })} ${at(DAY, 0).getDate()}$`) });
    // 02:00, where nothing is.
    await day.click({ position: { x: 10, y: 130 } });
    await expect(mia.page.getByRole('dialog')).toHaveCount(0);
    await event(mia.page, ALICE_BLOCK).click();
    const dialog = mia.page.getByRole('dialog');
    await expect(dialog).toContainText(ALICE_BLOCK);
    await expect(dialog).toContainText('Focus time');
    await expect(dialog.getByRole('button', { name: /Edit|Remove|Cancel meeting|Join/ })).toHaveCount(0);
    await mia.page.keyboard.press('Escape');

    // Day and month show the same calendar, and a reload keeps it.
    const views = mia.page.getByRole('group', { name: 'View' });
    await views.getByRole('button', { name: 'Day' }).click();
    await expect(banner(mia.page)).toContainText(PEOPLE.alice.name);
    await expect(event(mia.page, ALICE_BLOCK)).toBeVisible();
    await views.getByRole('button', { name: 'Month' }).click();
    await expect(mia.page.getByTestId('month-item').filter({ hasText: ALICE_BLOCK })).toBeVisible();
    await mia.page.reload();
    await expect(banner(mia.page)).toContainText(PEOPLE.alice.name);
    await expect(mia.page.getByTestId('month-item').filter({ hasText: ALICE_BLOCK })).toBeVisible();

    // Somebody on a team nested under hers.
    await picker(mia.page).click();
    await mia.page.getByRole('option', { name: PEOPLE.pat.name }).click();
    await expect(banner(mia.page)).toContainText(`${PEOPLE.pat.name}'s calendar`);
    await expect(mia.page.getByTestId('month-item').filter({ hasText: PAT_BLOCK })).toBeVisible();
    await expect(mia.page.getByTestId('month-item').filter({ hasText: ALICE_BLOCK })).toHaveCount(0);

    // And back to her own, which she can change again.
    await banner(mia.page).getByRole('button', { name: 'Back to mine' }).click();
    await expect(mia.page).not.toHaveURL(/person=/);
    await expect(banner(mia.page)).toHaveCount(0);
    await expect(picker(mia.page)).toHaveText('My calendar');
    await expect(mia.page.getByRole('button', { name: 'Block time' })).toBeVisible();

    // The server holds the line too: her report's block is not hers to change.
    const api = await apiAs('mia');
    const aliceBlock = made.find((m) => m.who === 'alice')!.id;
    expect(await api.status('patch', `/calendar/blocks/${aliceBlock}`, { title: 'Mine now' })).toBe(404);
    expect(await api.status('delete', `/calendar/blocks/${aliceBlock}`)).toBe(404);
    await api.dispose();
  });

  test('somebody who manages nobody has no picker, and a link to a colleague is refused', async ({ as }) => {
    const sam = await as('sam');
    await sam.page.goto(`/calendar?date=${dayParam(at(DAY, 0))}`);
    await expect(event(sam.page, SAM_BLOCK)).toBeVisible();
    await expect(picker(sam.page)).toHaveCount(0);

    await sam.page.goto(`/calendar?date=${dayParam(at(DAY, 0))}&person=${idOf('alice')}`);
    await expect(sam.page.getByRole('alert')).toContainText('only see your own calendar');
    await expect(sam.page.getByRole('main')).not.toContainText(ALICE_BLOCK);
    await sam.page.getByRole('button', { name: 'Open my calendar' }).click();
    await expect(sam.page).not.toHaveURL(/person=/);
    await expect(event(sam.page, SAM_BLOCK)).toBeVisible();

    // Another team's manager is refused the same way.
    const max = await apiAs('max');
    expect(await max.status('get', `/calendar?userId=${idOf('alice')}&from=${iso(at(DAY, 0))}&to=${iso(at(DAY + 1, 0))}`)).toBe(403);
    await max.dispose();
  });

  test('an administrator may open anybody, listed after the teams they run', async ({ as }) => {
    const olivia = await as('owner');
    await olivia.page.goto(`/calendar?date=${dayParam(at(DAY, 0))}`);
    await picker(olivia.page).click();
    const everybody = olivia.page.getByRole('listbox').getByRole('group').filter({ hasText: 'Everybody else' });
    for (const who of ['mia', 'max', 'alice', 'ann', 'pat', 'sam'] as const) {
      await expect(everybody.getByRole('option', { name: PEOPLE[who].name, exact: true })).toBeVisible();
    }
    await everybody.getByRole('option', { name: PEOPLE.sam.name, exact: true }).click();
    await expect(banner(olivia.page)).toContainText(`${PEOPLE.sam.name}'s calendar, read-only`);
    await expect(event(olivia.page, SAM_BLOCK)).toBeVisible();
  });

  test('on a phone the picker and the banner fit, with no sideways scroll', async ({ as }) => {
    for (const theme of ['light', 'dark'] as const) {
      const phone = await as('mia', { width: 390, height: 844, theme });
      await phone.page.goto(`/calendar?date=${dayParam(at(DAY, 0))}&person=${idOf('alice')}`);
      await expect(phone.page.getByRole('group', { name: 'View' }).getByRole('button', { name: 'Day' })).toHaveAttribute('aria-pressed', 'true');
      await expect(banner(phone.page)).toContainText(PEOPLE.alice.name);
      await expect(event(phone.page, ALICE_BLOCK)).toBeVisible();
      await expect(picker(phone.page)).toBeVisible();
      const scroll = await phone.page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
      expect(scroll).toBeLessThanOrEqual(0);
      const bannerBox = (await banner(phone.page).boundingBox())!;
      expect(bannerBox.x + bannerBox.width).toBeLessThanOrEqual(390);
    }
  });
});
