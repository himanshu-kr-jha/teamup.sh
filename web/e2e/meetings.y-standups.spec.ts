import type { Page } from '@playwright/test';
import { test, expect } from './support/fixtures';
import { apiAs, leaveCall, passPrejoin } from './support/meetings';
import { PEOPLE } from './support/people';

// Stand-ups for one team: the owner's decisions of 14 November 2026. Mia manages
// Backend (Alice, Ann) with Platform (Pat) beneath it; Sam is in Sales, under Max.

const NAME = (k: keyof typeof PEOPLE) => PEOPLE[k].name;
const admitButton = (page: Page, who: string) => page.getByRole('button', { name: `Admit ${who}` });
const card = (page: Page, name: string) => page.getByTestId('standing-card').filter({ has: page.getByRole('heading', { name, exact: true }) });

/** Running now, every day, so the room is open whatever day and time the suite runs. */
function timeNow(): string {
  const d = new Date(Date.now() - 5 * 60_000);
  const m = d.toDateString() === new Date().toDateString() ? d.getHours() * 60 + d.getMinutes() : 1;
  return `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`;
}

/** The local date this many days from today, as the calendar's address takes it. */
function calendarDay(offset: number): string {
  const d = new Date();
  d.setDate(d.getDate() + offset);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

test('a manager makes a stand-up for their team: the team sees it, nobody else does', async ({ as }) => {
  test.setTimeout(90_000);
  const name = `Backend stand-up ${Date.now()}`;
  const mia = await as('mia');
  await mia.page.goto('/calls');
  await mia.page.getByRole('button', { name: 'New', exact: true }).click();
  const dialog = mia.page.getByRole('dialog');
  await dialog.getByLabel('Name', { exact: true }).fill(name);
  await dialog.getByLabel('Time', { exact: true }).fill(timeNow());
  const off = dialog.getByRole('group', { name: 'Days' }).getByRole('button', { pressed: false });
  while ((await off.count()) > 0) await off.first().click();

  // A manager is offered her own teams, not the whole organization.
  const who = dialog.getByRole('group', { name: 'Who is it for' });
  await expect(who.getByRole('radio', { name: 'Everybody in the organization' })).toHaveCount(0);
  const teams = who.getByRole('list', { name: 'Teams' });
  await expect(teams.getByRole('checkbox', { name: /^Sales/ })).toHaveCount(0);
  await expect(dialog.getByRole('button', { name: 'Create' })).toBeDisabled();
  await teams.getByRole('checkbox', { name: /^Backend/ }).check();
  await expect(teams.getByRole('checkbox', { name: /^Backend/ })).toBeChecked();
  await dialog.getByRole('button', { name: 'Create' }).click();
  await expect(card(mia.page, name).getByTestId('standing-audience')).toHaveText('For Backend');

  // Alice on Backend and Pat on Platform, beneath it, have it; Sam in Sales does not.
  for (const key of ['alice', 'pat'] as const) {
    const s = await as(key);
    await s.page.goto('/calls');
    await expect(s.page.getByRole('button', { name: `Join ${name}` })).toBeVisible();
  }
  const sam = await as('sam');
  await sam.page.goto('/calls');
  await expect(sam.page.getByRole('heading', { name: 'Calls' }).first()).toBeVisible();
  await expect(sam.page.getByText(name)).toHaveCount(0);

  // And on calendars: Alice's has it, Sam's does not. Tomorrow's, as it runs every
  // day: today's is full of the suite's own calls around now, and a pile-up that
  // deep put it into "+26 more at this time".
  const tomorrow = calendarDay(1);
  const aliceCal = await as('alice');
  await aliceCal.page.goto(`/calendar?view=day&date=${tomorrow}`);
  await expect(aliceCal.page.getByRole('group', { name: 'View' }).getByRole('button', { name: 'Day' })).toHaveAttribute('aria-pressed', 'true');
  await expect(aliceCal.page.getByText(name).first()).toBeVisible();
  await sam.page.goto(`/calendar?view=day&date=${tomorrow}`);
  await expect(sam.page.getByRole('heading', { level: 1 }).first()).toBeVisible();
  await sam.page.waitForTimeout(1500);
  await expect(sam.page.getByText(name)).toHaveCount(0);

  // The owner looks after every meeting, but is not in this one by rank.
  const olivia = await as('owner');
  await olivia.page.goto('/calls');
  const theirs = card(olivia.page, name);
  await expect(theirs.getByText('Not one of yours')).toBeVisible();
  await expect(theirs.getByRole('button', { name: `Edit ${name}` })).toBeVisible();
  await expect(olivia.page.getByRole('button', { name: `Join ${name}` })).toHaveCount(0);

  const api = await apiAs('mia');
  const id = (await api.get('/standing-meetings')).standingMeetings.find((m: any) => m.name === name).id;
  await api.del(`/standing-meetings/${id}`);
  await api.dispose();
});

test('somebody outside the team with the link knocks, and is let in for the day', async ({ as }) => {
  test.setTimeout(120_000);
  const mia = await apiAs('mia');
  const made = await mia.post('/standing-meetings', {
    name: `Door test ${Date.now()}`,
    timeOfDay: timeNow(),
    timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    weekdays: [1, 2, 3, 4, 5, 6, 7],
    everyone: false,
    teamIds: [(await mia.get('/standing-audience-options')).teams.find((t: any) => t.name === 'Backend').id],
  });
  const meeting = made.standingMeeting;

  const alice = await as('alice');
  await alice.page.goto('/calls');
  await alice.page.getByRole('button', { name: `Join ${meeting.name}` }).click();
  await passPrejoin(alice.page);
  await expect(alice.page.getByRole('button', { name: 'Leave the call' })).toBeVisible({ timeout: 30_000 });
  const callId = new URL(alice.page.url()).pathname.split('/').pop()!;
  // The door rings who is connected, which the webhooks say.
  await alice.page.waitForTimeout(2500);

  // Sam has today's link, from somebody in the team.
  const sam = await as('sam');
  await sam.page.goto(`/calls/${callId}`);
  await expect(sam.page.getByText('This is a private call')).toBeVisible();
  await sam.page.getByRole('button', { name: 'Ask to join' }).click();
  await expect(sam.page.getByText('Waiting for someone to let you in')).toBeVisible();

  // Alice, in the team, answers the door.
  await expect(admitButton(alice.page, NAME('sam'))).toBeVisible({ timeout: 20_000 });
  await admitButton(alice.page, NAME('sam')).click();
  await expect(sam.page.getByRole('button', { name: 'Leave the call' })).toBeVisible({ timeout: 30_000 });
  // In together: Alice is told he came, and he can see her.
  await expect(alice.page.getByText(`${NAME('sam')} joined the call`, { exact: true })).toBeVisible({ timeout: 20_000 });
  await expect(sam.page.getByText(NAME('alice')).first()).toBeVisible({ timeout: 20_000 });

  // Let in for today's meeting only: the stand-up is still not one of his. (The
  // call he was in stays in his history, as any call does.)
  await leaveCall(sam.page);
  await sam.page.goto('/calls');
  await expect(sam.page.getByRole('heading', { name: 'Calls' }).first()).toBeVisible();
  await expect(sam.page.getByTestId('standing-card').filter({ hasText: meeting.name })).toHaveCount(0);
  await expect(sam.page.getByRole('button', { name: `Join ${meeting.name}` })).toHaveCount(0);

  await leaveCall(alice.page);
  await mia.del(`/standing-meetings/${meeting.id}`);
  await mia.dispose();
});
