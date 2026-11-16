import { expect, test } from './support/fixtures';
import { apiAs, idOf, leaveCall, sql } from './support/meetings';
import { PEOPLE } from './support/people';

// Stand-ups ring like a call as they start, and nothing rings somebody on leave:
// the owner's request of 15 November 2026.

const NAME = (k: keyof typeof PEOPLE) => PEOPLE[k].name;

function utcDay(offset = 0): string {
  const d = new Date();
  d.setUTCDate(d.getUTCDate() + offset);
  return d.toISOString().slice(0, 10);
}

const clean = () => sql(`DELETE FROM leave_changes; DELETE FROM leave_periods; DELETE FROM org_holidays`);

test.describe('ringing and leave', () => {
  test.beforeEach(() => clean());
  test.afterAll(() => clean());

  test('a stand-up rings everybody it is for as it starts, except whoever is on leave', async ({ as }) => {
    test.setTimeout(120_000);
    // Ann is on leave today, her whole day wherever her browser is.
    const ann = await as('ann');
    await ann.page.goto('/chat');
    const annApi = await apiAs('ann');
    await annApi.post('/leave', { startsOn: utcDay(-1), endsOn: utcDay(1) });
    await annApi.dispose();

    const alice = await as('alice');
    await alice.page.goto('/chat');

    // Starting this minute, in UTC, made a day ago so today's slot counts.
    const now = new Date();
    const name = `Ringing scrum ${Date.now()}`;
    const owner = await apiAs('owner');
    const created = await owner.post('/standing-meetings', {
      name,
      timeOfDay: `${String(now.getUTCHours()).padStart(2, '0')}:${String(now.getUTCMinutes()).padStart(2, '0')}`,
      timeZone: 'UTC',
      weekdays: [1, 2, 3, 4, 5, 6, 7],
      durationMinutes: 15,
    });
    const id = created.standingMeeting.id as string;
    sql(`UPDATE standing_meetings SET created_at = now() - interval '1 day' WHERE id = '${id}'`);

    // The server sweeps every 15 seconds.
    const ring = alice.page.getByRole('dialog').filter({ hasText: 'Stand-up starting' });
    await expect(ring).toBeVisible({ timeout: 45_000 });
    await expect(ring.getByText(`${name} is starting now.`)).toBeVisible();
    await expect(ann.page.getByText('Stand-up starting')).toHaveCount(0);

    // Join takes her into the day's room.
    await ring.getByRole('button', { name: 'Join' }).click();
    await expect(alice.page.getByRole('button', { name: 'Leave the call' })).toBeVisible({ timeout: 30_000 });
    expect(sql(`SELECT count(*) FROM calls WHERE standing_meeting_id = '${id}'`)).toBe('1');
    await leaveCall(alice.page);

    // Ann is on leave in attendance too.
    const days = (await owner.get(`/standing-meetings/${id}/attendance?date=${utcDay()}`)).days;
    expect(days[0].people.find((p: any) => p.name === NAME('ann'))).toMatchObject({ status: 'on_leave' });
    await owner.call('delete', `/standing-meetings/${id}`);
    await owner.dispose();
  });

  test('calling somebody on leave does not ring them, and the caller is told', async ({ as }) => {
    const annApi = await apiAs('ann');
    await annApi.post('/leave', { startsOn: utcDay(-1), endsOn: utcDay(1) });
    await annApi.dispose();
    const ann = await as('ann');
    await ann.page.goto('/chat');

    const alice = await as('alice');
    await alice.page.goto('/calls');
    const res = await alice.page.request.post('/api/calls', { data: { callType: 'audio', invitees: [idOf('ann')] } });
    expect(res.status()).toBe(201);
    await expect(alice.page.getByText(`${NAME('ann')} is on leave, so was not rung.`, { exact: true })).toBeVisible({ timeout: 15_000 });
    await expect(ann.page.getByRole('dialog').filter({ hasText: 'Incoming' })).toHaveCount(0);

    // The calls list says why, to each of them.
    await alice.page.goto('/calls');
    await expect(alice.page.getByText('On leave', { exact: true }).first()).toBeVisible();
    await ann.page.goto('/calls');
    await expect(ann.page.getByText('Missed while on leave').first()).toBeVisible();
  });
});
