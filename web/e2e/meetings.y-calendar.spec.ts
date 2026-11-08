import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { apiAs, at, dayParam, iso, idOf, leaveCall, localInput, sql, waitForLog } from './support/meetings';

// Scheduling a meeting by its length, and a meeting that runs past its end showing
// as extended on the calendar of whoever is still in it.

const minutesFromNow = (m: number) => new Date(Date.now() + m * 60_000);
const inACall = (page: Page) => page.getByRole('button', { name: 'Leave the call' });
const event = (page: Page, name: RegExp) => page.getByRole('main').getByRole('button', { name });

/**
 * Opens a meeting on today's calendar, which a full run fills so much that it may be
 * folded into "more at this time". Says whether it was drawn on the grid itself.
 */
async function openToday(page: Page, title: string, extended: RegExp): Promise<boolean> {
  await page.goto(`/calendar?date=${dayParam(new Date())}`);
  const main = page.getByRole('main');
  await expect(main.getByRole('button', { name: 'Block time' })).toBeVisible();
  const direct = main.getByRole('button', { name: new RegExp(`${title}, .*${extended.source}`) });
  const chips = main.getByRole('button', { name: /more at this time/ });
  await expect.poll(async () => (await direct.count()) + (await chips.count()), { message: `${title} or a fold` }).toBeGreaterThan(0);
  if (await direct.count()) {
    await expect(direct.getByTestId('overrun')).toBeVisible();
    await direct.click();
    return true;
  }
  for (let i = 0; i < (await chips.count()); i++) {
    await chips.nth(i).click();
    const listed = page.getByRole('dialog').getByRole('button', { name: new RegExp(`${title}.*${extended.source}`) });
    if (await listed.count()) {
      await listed.click();
      return false;
    }
    await page.keyboard.press('Escape');
  }
  throw new Error(`${title} is nowhere on today's calendar as extended`);
}

test.describe('scheduling by length', () => {
  test('a meeting is a start and a length from the list, and editing one of another length keeps it', async ({ as }) => {
    const owner = await apiAs('owner');
    const olivia = await as('owner');
    const day = at(5, 0);
    const title = `Stand-in ${Date.now()}`;

    await olivia.page.goto(`/calendar?date=${dayParam(day)}`);
    await olivia.page.getByRole('button', { name: 'Schedule', exact: true }).click();
    const dialog = olivia.page.getByRole('dialog');
    await expect(dialog.getByLabel('Ends', { exact: true })).toHaveCount(0);
    await dialog.getByLabel('Name', { exact: true }).fill(title);
    await dialog.getByLabel('Starts', { exact: true }).fill(localInput(at(5, 10)));
    await dialog.getByLabel('Duration', { exact: true }).click();
    const options = olivia.page.getByRole('option');
    await expect(options).toHaveText(['5 min', '10 min', '15 min', '30 min', '45 min', '1 hour']);
    await olivia.page.getByRole('option', { name: '15 min', exact: true }).click();
    await dialog.getByLabel('People', { exact: true }).fill('Alice');
    await dialog.getByRole('button', { name: /Alice/ }).first().click();
    await dialog.getByRole('button', { name: /^Schedule( anyway)?$/ }).click();
    await expect(event(olivia.page, new RegExp(`${title}, 10:00 AM to 10:15 AM`))).toBeVisible();

    const items = (await owner.get(`/calendar?from=${iso(at(5, 0))}&to=${iso(at(6, 0))}`)).items as any[];
    const made = items.find((i) => i.title === title);
    expect(new Date(made.endsAt).getTime() - new Date(made.startsAt).getTime()).toBe(15 * 60_000);

    // One made another way, an hour and a half long, keeps its length when edited.
    const long = `Workshop ${Date.now()}`;
    const created = await owner.post('/meetings', { title: long, startsAt: iso(at(5, 13)), endsAt: iso(at(5, 14, 30)), inviteeIds: [idOf('alice')] });
    await olivia.page.reload();
    await event(olivia.page, new RegExp(long)).click();
    await olivia.page.getByRole('button', { name: 'Edit' }).click();
    await expect(dialog.getByLabel('Duration', { exact: true })).toHaveText('1 h 30 min');
    await dialog.getByLabel('Name', { exact: true }).fill(`${long} moved`);
    await dialog.getByRole('button', { name: 'Save' }).click();
    await expect(event(olivia.page, new RegExp(`${long} moved, 01:00 PM to 02:30 PM`))).toBeVisible();
    const after = (await owner.get(`/calendar?from=${iso(at(5, 0))}&to=${iso(at(6, 0))}`)).items as any[];
    const kept = after.find((i) => i.callId === created.meeting.id);
    expect(new Date(kept.endsAt).getTime() - new Date(kept.startsAt).getTime()).toBe(90 * 60_000);
    await owner.dispose();
  });
});

test.describe('a meeting that runs past its end', () => {
  test('shows as extended while it goes on, and until you left once it is over', async ({ as }) => {
    const owner = await apiAs('owner');
    const title = `Overrun ${Date.now()}`;
    const created = await owner.post('/meetings', {
      title,
      startsAt: iso(minutesFromNow(10)),
      endsAt: iso(minutesFromNow(25)),
      inviteeIds: [idOf('alice')],
    });
    const id = created.meeting.id;

    const olivia = await as('owner');
    await olivia.page.goto(`/calls/${id}`);
    await olivia.page.getByRole('button', { name: /^Join( now)?$/ }).click();
    await expect(inACall(olivia.page)).toBeVisible({ timeout: 30_000 });
    await waitForLog(owner, id, 'Olivia connected', (log) => log.sessions.some((s) => s.userId === idOf('owner') && !s.leftAt));

    // The meeting was due to end 10 minutes ago and she is still in it.
    sql(`UPDATE calls SET scheduled_start = NOW() - interval '30 minutes', scheduled_end = NOW() - interval '10 minutes' WHERE id = '${id}'`);
    sql(`UPDATE call_sessions SET joined_at = NOW() - interval '28 minutes' WHERE call_id = '${id}'`);
    sql(`UPDATE call_participants SET joined_at = NOW() - interval '28 minutes' WHERE call_id = '${id}' AND joined_at IS NOT NULL`);

    const calendar = await as('owner');
    await openToday(calendar.page, title, /extended 10 min/);
    await expect(calendar.page.getByRole('dialog').getByText(/^Scheduled until .*, still going \(1\d min over so far\)$/)).toBeVisible();
    await calendar.page.keyboard.press('Escape');

    // Alice was invited and never came: her calendar keeps the planned time.
    const alice = await apiAs('alice');
    const hers = ((await alice.get(`/calendar?from=${iso(at(0, 0))}&to=${iso(at(1, 0))}`)).items as any[]).find((i) => i.callId === id);
    expect(hers.scheduledEndsAt).toBeUndefined();

    await leaveCall(olivia.page);
    await expect.poll(async () => (await owner.get(`/calls/${id}`)).status, { timeout: 60_000 }).toBe('ended');
    await openToday(calendar.page, title, /extended 1\d min/);
    await expect(calendar.page.getByRole('dialog').getByText(/^Scheduled until .*, extended to .* \(1\d min over\)$/)).toBeVisible();
    await Promise.all([owner.dispose(), alice.dispose()]);
  });
});
