import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { apiAs, idOf, sql } from './support/meetings';
import { PEOPLE } from './support/people';

// Leave and holidays in attendance: the owner's decision of 15 November 2026.
// People write their own leave; whoever manages them, and administrators, can add
// or correct it. Mia manages Backend (Alice, Ann); Sam is in Sales, under Max.

const NAME = (k: keyof typeof PEOPLE) => PEOPLE[k].name;

/** The UTC date this many days from today: the meeting below runs in UTC. */
function utcDay(offset: number): string {
  const d = new Date();
  d.setUTCDate(d.getUTCDate() + offset);
  return d.toISOString().slice(0, 10);
}

/** The local date this many days from today, which is what "today" means to a person. */
function localDay(offset: number): string {
  const d = new Date();
  d.setDate(d.getDate() + offset);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

const clean = () => sql(`DELETE FROM leave_changes; DELETE FROM leave_periods; DELETE FROM org_holidays`);

/** A daily scrum at 09:00 UTC, made days ago, for everybody, with everybody already in the organization. */
async function dailyScrum(name: string): Promise<string> {
  const owner = await apiAs('owner');
  const created = await owner.post('/standing-meetings', {
    name,
    timeOfDay: '09:00',
    timeZone: 'UTC',
    weekdays: [1, 2, 3, 4, 5, 6, 7],
    durationMinutes: 15,
  });
  await owner.dispose();
  const id = created.standingMeeting.id as string;
  sql(`UPDATE standing_meetings SET created_at = now() - interval '5 days' WHERE id = '${id}'`);
  sql(`UPDATE standing_meeting_schedules SET effective_at = now() - interval '5 days' WHERE standing_meeting_id = '${id}'`);
  sql(`UPDATE users SET created_at = now() - interval '10 days' WHERE org_id = (SELECT org_id FROM users WHERE email = '${PEOPLE.owner.email}')`);
  return id;
}

const row = (page: Page, name: string) => page.getByRole('table').first().getByRole('row', { name: new RegExp(name) });

test.describe('leave and holidays', () => {
  test.beforeEach(() => clean());
  test.afterAll(() => clean());

  test('a manager adds leave for a day already gone: attendance says On leave, added after the meeting, and the report is told', async ({ as }) => {
    test.setTimeout(120_000);
    const id = await dailyScrum(`Leave scrum ${Date.now()}`);
    const yesterday = utcDay(-1);

    // Before: Ann simply did not come.
    const owner = await apiAs('owner');
    const before = (await owner.get(`/standing-meetings/${id}/attendance?date=${yesterday}`)).days[0];
    expect(before.people.find((p: any) => p.name === NAME('ann'))).toMatchObject({ status: 'absent' });

    // Mia adds it from the Leave page, for Ann.
    const mia = await as('mia');
    await mia.page.goto('/leave');
    await expect(mia.page.getByRole('heading', { name: 'Leave', exact: true })).toBeVisible();
    await expect(mia.page.getByText('Your leave and the leave of the people you manage.')).toBeVisible();
    await mia.page.getByRole('button', { name: 'Add leave' }).click();
    const dialog = mia.page.getByRole('dialog');
    await dialog.getByLabel('Whose leave').click();
    await mia.page.getByRole('option', { name: NAME('ann') }).click();
    await dialog.getByLabel('First day').fill(yesterday);
    await dialog.getByLabel('Last day').fill(yesterday);
    await dialog.getByLabel(/^Note/).fill('Fever');
    await dialog.getByRole('button', { name: 'Add leave' }).click();
    await expect(dialog).toHaveCount(0);
    const past = mia.page.getByRole('region', { name: 'Past year' });
    await expect(past.getByText(new RegExp(`^${NAME('ann')}: `))).toBeVisible();
    await expect(past.getByText('Fever')).toBeVisible();
    await expect(past.getByText(`Added by ${NAME('mia')}`, { exact: false })).toBeVisible();
    await expect(past.getByText(', after it had started')).toBeVisible();

    // The owner reads that day's attendance.
    const olivia = await as('owner');
    await olivia.page.goto(`/calls/standing/${id}/attendance?from=${yesterday}&to=${yesterday}`);
    await expect(row(olivia.page, NAME('ann'))).toContainText('On leave');
    await expect(row(olivia.page, NAME('ann'))).toContainText(`Added by ${NAME('mia')} on`);
    await expect(row(olivia.page, NAME('ann'))).toContainText(', after the meeting');
    await expect(row(olivia.page, NAME('alice'))).toContainText('Absent');
    await expect(olivia.page.getByRole('region', { name: `Attendance on ${yesterday}` })).toContainText('1 on leave');

    // Ann is told, and the notification opens her leave.
    const ann = await as('ann');
    const notes = (await (await apiAs('ann')).get('/notifications')).notifications;
    expect(notes.some((n: any) => n.type === 'leave.changed' && n.title === `${NAME('mia')} added leave for you`)).toBe(true);
    await ann.page.goto('/leave');
    await expect(ann.page.getByText('Your leave.', { exact: false })).toBeVisible();
    await expect(ann.page.getByRole('region', { name: 'Past year' }).getByText(`Added by ${NAME('mia')}`, { exact: false })).toBeVisible();

    // The history says who did what.
    await ann.page.getByRole('button', { name: /^History of / }).click();
    await expect(ann.page.getByRole('dialog').getByText(`Added by ${NAME('mia')}:`, { exact: false })).toBeVisible();

    // Sam is a colleague, not her manager: nothing to see, nothing to write.
    const sam = await apiAs('sam');
    expect(await sam.status('post', '/leave', { userId: idOf('ann'), startsOn: utcDay(3) })).toBe(404);
    expect((await sam.get(`/leave?from=${utcDay(-30)}&to=${utcDay(30)}`)).leave).toEqual([]);
    await sam.dispose();
    await owner.dispose();
  });

  test('a holiday means nobody is absent, and only an administrator keeps the list', async ({ as }) => {
    test.setTimeout(90_000);
    const id = await dailyScrum(`Holiday scrum ${Date.now()}`);
    const day = utcDay(-2);

    const mia = await as('mia');
    await mia.page.goto('/leave');
    await expect(mia.page.getByText('An administrator keeps this list.')).toBeVisible();
    await expect(mia.page.getByRole('button', { name: 'Add holiday' })).toHaveCount(0);

    // The owner keeps them under Organization; on Leave they are only read, with the way there.
    const olivia = await as('owner');
    await olivia.page.goto('/leave');
    await expect(olivia.page.getByRole('button', { name: 'Add holiday' })).toHaveCount(0);
    await olivia.page.getByRole('link', { name: 'Organization, Leave & holidays' }).click();
    await expect(olivia.page.getByRole('tab', { name: 'Leave & holidays', selected: true })).toBeVisible();
    const holidays = olivia.page.getByRole('region', { name: 'Holidays' });
    await holidays.getByLabel('Date').fill(day);
    await holidays.getByLabel('Name').fill("Founders' day");
    await holidays.getByRole('button', { name: 'Add holiday' }).click();
    await expect(holidays.getByText("Founders' day")).toBeVisible();

    await olivia.page.goto(`/calls/standing/${id}/attendance?from=${day}&to=${day}`);
    const section = olivia.page.getByRole('region', { name: `Attendance on ${day}` });
    await expect(section.getByText("Holiday: Founders' day")).toBeVisible();
    await expect(section.getByText('Nobody was expected')).toBeVisible();
    await expect(row(olivia.page, NAME('alice'))).toContainText('Holiday');
    await expect(section.getByText('Absent', { exact: true })).toHaveCount(0);

    // Taking it off makes it an ordinary day again.
    await olivia.page.goto('/organization?tab=leave');
    await holidays.getByRole('button', { name: "Remove Founders' day" }).click();
    await olivia.page.getByRole('dialog').getByRole('button', { name: 'Remove' }).click();
    await expect(holidays.getByText('No holidays this year or next.')).toBeVisible();
  });

  test('Leave opens from the profile menu, not the sidebar, and leave and holidays are on the calendar', async ({ as }) => {
    const day = localDay(2);
    const ann = await apiAs('ann');
    await ann.post('/leave', { startsOn: day, endsOn: day, part: 'afternoon' });
    await ann.dispose();
    const owner = await apiAs('owner');
    await owner.post('/holidays', { date: day, name: 'Harvest day' });
    await owner.dispose();

    const annSession = await as('ann');
    await annSession.page.goto('/chat');
    await expect(annSession.page.getByRole('navigation').getByRole('link', { name: 'Leave' })).toHaveCount(0);
    await annSession.page.getByRole('button', { name: 'User menu' }).click();
    await annSession.page.getByRole('menuitem', { name: 'Leave' }).click();
    await expect(annSession.page).toHaveURL(/\/leave$/);
    await expect(annSession.page.getByRole('heading', { name: 'Leave', exact: true })).toBeVisible();

    // Her calendar, the day itself: the holiday, and her afternoon off, above the hours.
    await annSession.page.goto(`/calendar?view=day&date=${day}`);
    const row = annSession.page.getByTestId('due-row');
    await expect(row.getByTestId('day-mark').filter({ hasText: 'Holiday: Harvest day' })).toBeVisible();
    await expect(row.getByTestId('day-mark').filter({ hasText: 'On leave, afternoon' })).toBeVisible();
    await expect(row).toContainText('All day');
    // And the month.
    await annSession.page.goto(`/calendar?view=month&date=${day}`);
    const cell = annSession.page.locator(`[data-testid="month-day"][data-day="${day}"]`);
    await expect(cell.getByTestId('day-mark')).toHaveCount(2);

    // Her manager, opening her calendar, reads whose leave it is.
    const mia = await as('mia');
    await mia.page.goto(`/calendar?view=day&date=${day}&person=${idOf('ann')}`);
    await expect(mia.page.getByTestId('day-mark').filter({ hasText: `${NAME('ann').split(' ')[0]} on leave, afternoon` })).toBeVisible();
  });

  test('choosing On leave as your status records leave from today, and coming back ends it', async ({ as }) => {
    const alice = await as('alice');
    await alice.page.goto('/leave');
    await expect(alice.page.getByText('No leave coming up.')).toBeVisible();

    await alice.page.getByRole('button', { name: 'Set your availability' }).click();
    const dialog = alice.page.getByRole('dialog');
    await dialog.getByRole('button', { name: /^On leave/ }).click();
    await dialog.getByLabel(/^Back on/).fill(localDay(3));
    await dialog.getByRole('button', { name: 'Save' }).click();
    await expect(dialog).toHaveCount(0);

    const now = alice.page.getByRole('region', { name: 'Now and coming up' });
    await expect(now.getByText('Now', { exact: true })).toBeVisible();
    await expect(now.getByText(`Added by ${NAME('alice')}`, { exact: false })).toBeVisible();

    // Her manager sees it too, as on leave.
    const roster = (await (await apiAs('mia')).get(`/presence/availability?today=${localDay(0)}`)).people;
    expect(roster.find((p: any) => p.userId === idOf('alice'))).toMatchObject({ status: 'on_leave' });

    // Back the same day: there was no leave after all.
    await alice.page.getByRole('button', { name: 'Set your availability' }).click();
    await dialog.getByRole('button', { name: /^Available/ }).click();
    await dialog.getByRole('button', { name: 'Save' }).click();
    await expect(dialog).toHaveCount(0);
    await expect(alice.page.getByText('No leave coming up.')).toBeVisible();
  });
});
