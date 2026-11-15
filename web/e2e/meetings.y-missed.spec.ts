import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { apiAs, idOf, iso, openOnCalendar, sql } from './support/meetings';

// A meeting nobody started is missed once its time is up, and not before.

const minutesFromNow = (m: number) => new Date(Date.now() + m * 60_000);

/** Schedules a meeting and then moves it to a time relative to now, as if it had been made long ago. */
async function meeting(title: string, startOffset: number, endOffset: number) {
  const owner = await apiAs('owner');
  const created = await owner.post('/meetings', {
    title,
    startsAt: iso(minutesFromNow(60)),
    endsAt: iso(minutesFromNow(90)),
    inviteeIds: [idOf('alice')],
  });
  await owner.dispose();
  const id = created.meeting.id as string;
  sql(
    `UPDATE calls SET scheduled_start = NOW() + interval '${startOffset} minutes', scheduled_end = NOW() + interval '${endOffset} minutes' WHERE id = '${id}'`
  );
  return id;
}

const cardOf = (page: Page, title: string) =>
  page.locator('div.rounded-lg', { has: page.getByRole('heading', { name: title, exact: true }) }).last();

test.describe('a meeting nobody started', () => {
  test('is missed once its end has passed: no Join anywhere, and it cannot be opened', async ({ as }) => {
    const title = `Missed standup ${Date.now()}`;
    // Due from 40 minutes ago for 30 minutes, so it ended 10 minutes ago.
    const id = await meeting(title, -40, -10);

    const { page } = await as('alice');
    await page.goto('/calls');
    const card = cardOf(page, title);
    await expect(card.getByTestId('call-status')).toHaveText('Missed');
    await expect(card.getByRole('button', { name: 'Join' })).toHaveCount(0);
    await expect(card.getByText(/^Was due .* at .*, nobody joined$/)).toBeVisible();

    // The Scheduled filter is for what is still to come; Missed finds it.
    await page.getByRole('combobox').selectOption('scheduled');
    await expect(page.getByRole('heading', { name: title, exact: true })).toHaveCount(0);
    await page.getByRole('combobox').selectOption('missed');
    await expect(cardOf(page, title).getByTestId('call-status')).toHaveText('Missed');

    // A link to it says so, and offers nothing to press.
    await page.goto(`/calls/${id}`);
    await expect(page.getByRole('heading', { name: 'This meeting was missed' })).toBeVisible();
    await expect(page.getByRole('button', { name: /^Join/ })).toHaveCount(0);

    // Nor does asking the server open it.
    const alice = await apiAs('alice');
    expect(await alice.status('post', `/calls/${id}/token`)).toBe(409);
    const after = await alice.get(`/calls/${id}`);
    expect(after.status).toBe('scheduled');
    expect(after.startedAt).toBeUndefined();
    await alice.dispose();
  });

  test('on the calendar it says missed, and its organizer can move it to a new time', async ({ as }) => {
    const title = `Missed review ${Date.now()}`;
    const id = await meeting(title, -40, -10);

    const { page } = await as('owner');
    await openOnCalendar(page, title);
    const dialog = page.getByRole('dialog');
    await expect(dialog.getByText(/, missed$/)).toBeVisible();
    await expect(dialog.getByRole('button', { name: 'Join' })).toHaveCount(0);
    await expect(dialog.getByRole('button', { name: 'Edit' })).toBeVisible();

    // Moved to tomorrow, it is a meeting to come again.
    const owner = await apiAs('owner');
    await owner.patch(`/meetings/${id}`, { startsAt: iso(minutesFromNow(24 * 60)), endsAt: iso(minutesFromNow(24 * 60 + 30)) });
    await owner.dispose();
    const alice = await as('alice');
    await alice.page.goto('/calls');
    await expect(cardOf(alice.page, title).getByTestId('call-status')).toHaveText('scheduled');
  });

  test('is not missed while its time is still running, only late', async ({ as }) => {
    const title = `Late start ${Date.now()}`;
    // Due 10 minutes ago, for 30 minutes: nobody has come yet, but there is time.
    await meeting(title, -10, 20);

    const { page } = await as('alice');
    await page.goto('/calls');
    const card = cardOf(page, title);
    await expect(card.getByTestId('call-status')).toHaveText('scheduled');
    await expect(card.getByRole('button', { name: 'Join' })).toBeVisible();
  });
});
