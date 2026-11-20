import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { apiAs, at, dayParam, iso, idOf, passPrejoin, sql, waitForLog } from './support/meetings';

// Somebody at a stand-up in the morning who looks into its room again in the
// evening was in it twice, not all day: the calendar shows two blocks, each the
// height of the time they were really there (reported 20 November 2026).

/** The day grid's scale, as CalendarGrid draws it. */
const HOUR_HEIGHT = 56;
/** The shortest an event is drawn, in minutes, as layout.ts's MIN_EVENT_MINUTES. */
const MIN_EVENT_MINUTES = 20;
const heightOf = (minutes: number) => (Math.max(minutes, MIN_EVENT_MINUTES) / 60) * HOUR_HEIGHT - 2;

const inACall = (page: Page) => page.getByRole('button', { name: 'Leave the call' });

async function joinStandUp(page: Page, name: string) {
  await page.goto('/calls');
  await page.getByRole('region', { name: 'Standing meetings' }).getByRole('button', { name: `Join ${name}` }).click();
  await passPrejoin(page);
  await expect(inACall(page)).toBeVisible({ timeout: 30_000 });
}

test('a stand-up somebody leaves and looks into again much later is two blocks on their calendar', async ({ as }) => {
  test.setTimeout(120_000);
  const owner = await apiAs('owner');
  const name = `Morning ${Date.now()}`;
  const clock = new Date(Date.now() - 5 * 60_000);
  const timeOfDay = `${String(clock.getHours()).padStart(2, '0')}:${String(clock.getMinutes()).padStart(2, '0')}`;
  const made = await owner.post('/standing-meetings', {
    name,
    timeOfDay,
    timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    weekdays: [1, 2, 3, 4, 5, 6, 7],
  });
  const meetingId = made.standingMeeting.id;
  const callId = (await owner.post(`/standing-meetings/${meetingId}/join`)).callId;
  const alice = idOf('alice');

  try {
    // Alice really comes, leaves, and comes back: two sessions, from LiveKit.
    const person = await as('alice');
    await joinStandUp(person.page, name);
    await waitForLog(owner, callId, 'Alice connected', (l) => l.sessions.some((s) => s.userId === alice && !s.leftAt));
    await inACall(person.page).click();
    await waitForLog(owner, callId, 'Alice left', (l) => l.sessions.some((s) => s.userId === alice) && l.sessions.every((s) => !!s.leftAt));
    await joinStandUp(person.page, name);
    await waitForLog(owner, callId, 'Alice back', (l) => l.sessions.filter((s) => s.userId === alice).length >= 2 && l.sessions.some((s) => !s.leftAt));
    await inACall(person.page).click();
    await waitForLog(owner, callId, 'Alice gone again', (l) => l.sessions.every((s) => !!s.leftAt));

    // Those were a minute apart; put them where the report had them, on a day of
    // their own: 08:30 to 09:35, and a look in at 20:17 for a minute. The room
    // stayed open with others in it until 21:05. Her call_participants row says
    // what it would: first join 08:30, last leave 20:18.
    const day = at(-2, 0);
    const first = sql(`SELECT id FROM call_sessions WHERE call_id = '${callId}' AND user_id = '${alice}' ORDER BY joined_at LIMIT 1`);
    sql(`UPDATE call_sessions SET joined_at = '${iso(at(-2, 8, 30))}', left_at = '${iso(at(-2, 9, 35))}' WHERE id = '${first}'`);
    sql(`UPDATE call_sessions SET joined_at = '${iso(at(-2, 20, 17))}', left_at = '${iso(at(-2, 20, 18))}'
         WHERE call_id = '${callId}' AND user_id = '${alice}' AND id <> '${first}'`);
    sql(`UPDATE call_participants SET joined_at = '${iso(at(-2, 8, 30))}', left_at = '${iso(at(-2, 20, 18))}'
         WHERE call_id = '${callId}' AND user_id = '${alice}'`);
    sql(`UPDATE calls SET status = 'ended', occurrence_date = '${dayParam(day)}', started_at = '${iso(at(-2, 8, 30))}',
         scheduled_start = '${iso(at(-2, 8, 30))}', scheduled_end = '${iso(at(-2, 9, 0))}', ended_at = '${iso(at(-2, 21, 5))}'
         WHERE id = '${callId}'`);

    const page = person.page;
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto(`/calendar?view=day&date=${dayParam(day)}`);
    const main = page.getByRole('main');
    const morning = main.getByRole('button', { name: new RegExp(`^${name}, 0?8:30 AM to 0?9:35 AM$`) });
    const evening = main.getByRole('button', { name: new RegExp(`^${name}, 0?8:17 PM to 0?8:18 PM$`) });
    await expect(morning).toBeVisible();
    await expect(evening).toBeVisible();
    // Nothing of it runs through the day between them.
    await expect(main.getByRole('button', { name: new RegExp(`^${name}, `) })).toHaveCount(2);

    const m = (await morning.boundingBox())!;
    const e = (await evening.boundingBox())!;
    expect(Math.abs(m.height - heightOf(65))).toBeLessThan(3);
    expect(Math.abs(e.height - heightOf(1))).toBeLessThan(3);
    // Eleven hours apart on the grid, and each as wide as the column: neither is
    // squeezed beside the other or folded into "more at this time".
    expect(e.y - (m.y + m.height)).toBeGreaterThan(10 * HOUR_HEIGHT);
    expect(Math.abs(m.width - e.width)).toBeLessThan(2);

    await morning.scrollIntoViewIfNeeded();
    await page.screenshot({ path: test.info().outputPath('morning.png') });
    await evening.scrollIntoViewIfNeeded();
    await page.screenshot({ path: test.info().outputPath('evening.png') });

    // Each opens the same stand-up.
    await evening.click();
    const dialog = page.getByRole('dialog');
    await expect(dialog.getByText(name).first()).toBeVisible();
    await page.keyboard.press('Escape');
  } finally {
    // It recurs every day, and its call on a quiet day would pile up on repeats.
    await owner.del(`/standing-meetings/${meetingId}`);
    sql(`DELETE FROM calls WHERE id = '${callId}'`);
    await owner.dispose();
  }
});
