import type { Page } from '@playwright/test';
import { PEOPLE } from './support/people';
import { expect, openTasks, test } from './support/fixtures';
import {
  apiAs,
  at,
  createCall,
  dayParam,
  ensureMember,
  iso,
  idOf,
  joinCall,
  leaveCall,
  lobbyJoin,
  oscillators,
  spyOnAudio,
  waitForLog,
} from './support/meetings';

// The defects filed against the first version of calendars and meetings, each
// reproduced the way somebody really hits it. See tickets 20 to 35. Named to run after
// meetings.spec.ts, which counts what is in the shared organization. Calls here leave Max
// as the person still being rung, because the next spec's people get rung on signing in.

const NAME = (k: keyof typeof PEOPLE) => PEOPLE[k].name;
const statusOf = async (api: Awaited<ReturnType<typeof apiAs>>, id: string): Promise<string> => (await api.get(`/calls/${id}`)).status;
const minutesFromNow = (m: number) => new Date(Date.now() + m * 60_000);
const inACall = (page: Page) => page.getByRole('button', { name: 'Leave the call' });

/** Clicks something on a day of the calendar, which may be folded into "more at this time" when the day is full. */
async function openOnCalendar(page: Page, name: string) {
  await page.goto(`/calendar?date=${dayParam(new Date())}`);
  const main = page.getByRole('main');
  await expect(main.getByRole('button', { name: 'Block time' })).toBeVisible();
  const direct = main.getByRole('button', { name: new RegExp(name) }).first();
  const chips = main.getByRole('button', { name: /more at this time/ });
  await expect.poll(async () => (await direct.count()) + (await chips.count()), { message: `${name} or a fold` }).toBeGreaterThan(0);
  if (await direct.count()) return direct.click();
  for (let i = 0; i < (await chips.count()); i++) {
    await chips.nth(i).click();
    const inside = page.getByRole('dialog').getByRole('button', { name: new RegExp(name) });
    if (await inside.count()) return inside.first().click();
    await page.keyboard.press('Escape');
  }
  throw new Error(`${name} is nowhere on the calendar`);
}

test.describe('ticket 20: a meeting that opened early and emptied', () => {
  test('it stays on Calls and on the calendar with a Join until its end, and joining brings it back', async ({ as }) => {
    const owner = await apiAs('owner');
    const title = `Planning ${Date.now()}`;
    const created = await owner.post('/meetings', { title, startsAt: iso(minutesFromNow(20)), endsAt: iso(minutesFromNow(80)), inviteeIds: [idOf('alice')] });
    const id = created.meeting.id;

    // The organizer presses Join, is alone, and leaves.
    const olivia = await as('owner');
    await olivia.page.goto('/calls');
    const card = (page: Page) => page.getByRole('main').locator('div', { hasText: title }).filter({ has: page.getByRole('button', { name: 'Join', exact: true }) }).last();
    await card(olivia.page).getByRole('button', { name: 'Join', exact: true }).click();
    await expect(inACall(olivia.page)).toBeVisible({ timeout: 30_000 });
    await leaveCall(olivia.page);
    await expect.poll(() => statusOf(owner, id)).toBe('ended');

    // The invitee is still expected.
    const alice = await as('alice');
    await alice.page.goto('/calls');
    await expect(card(alice.page).getByRole('button', { name: 'Join', exact: true })).toBeVisible();
    await expect(alice.page.getByText(title).first()).toBeVisible();
    const aliceCard = card(alice.page);
    await expect(aliceCard.getByText('ended', { exact: true })).toHaveCount(0);
    await expect(aliceCard.getByText('scheduled', { exact: true })).toBeVisible();

    await openOnCalendar(alice.page, title);
    const dialog = alice.page.getByRole('dialog');
    await expect(dialog.getByText(/finished/)).toHaveCount(0);
    await dialog.getByRole('button', { name: 'Join', exact: true }).click();
    await expect(inACall(alice.page)).toBeVisible({ timeout: 30_000 });
    await expect.poll(() => statusOf(owner, id)).toBe('active');
    await owner.dispose();
  });
});

test.describe('ticket 21: opening a meeting\'s page does not start it', () => {
  test('somebody invited who follows a link next week stops at a lobby, and the meeting can still be moved', async ({ as }) => {
    const owner = await apiAs('owner');
    const created = await owner.post('/meetings', { title: 'Next week review', startsAt: iso(at(7, 10)), endsAt: iso(at(7, 11)), inviteeIds: [idOf('alice')] });
    const id = created.meeting.id;

    const alice = await as('alice');
    await alice.page.goto(`/calls/${id}`);
    await expect(alice.page.getByRole('heading', { name: 'Next week review' })).toBeVisible();
    await expect(alice.page.getByText(/Joining now starts it for everybody/)).toBeVisible();
    await expect(inACall(alice.page)).toHaveCount(0);
    expect(await statusOf(owner, id), 'looking is not joining').toBe('scheduled');

    const later = await owner.patch(`/meetings/${id}`, { startsAt: iso(at(7, 12)), endsAt: iso(at(7, 13)) });
    expect(later.meeting.status).toBe('scheduled');

    // A reload does not start it either.
    await alice.page.reload();
    await expect(lobbyJoin(alice.page)).toBeVisible();
    expect(await statusOf(owner, id)).toBe('scheduled');

    // Meaning to is what starts it, and then it is locked.
    await lobbyJoin(alice.page).click();
    await expect(inACall(alice.page)).toBeVisible({ timeout: 30_000 });
    expect(await statusOf(owner, id)).toBe('active');
    expect(await owner.status('patch', `/meetings/${id}`, { startsAt: iso(at(7, 14)), endsAt: iso(at(7, 15)) })).toBe(409);
    await owner.dispose();
  });

  test('a link followed in the half hour before it asks too', async ({ as }) => {
    const owner = await apiAs('owner');
    const created = await owner.post('/meetings', { title: 'Soon', startsAt: iso(minutesFromNow(10)), endsAt: iso(minutesFromNow(70)), inviteeIds: [idOf('alice')] });
    const alice = await as('alice');
    await alice.page.goto(`/calls/${created.meeting.id}`);
    await expect(alice.page.getByRole('button', { name: 'Join', exact: true })).toBeVisible();
    expect(await statusOf(owner, created.meeting.id)).toBe('scheduled');
    await owner.dispose();
  });

  test('a knock notification shows the prompt without starting the meeting, and whoever is let in stops at the lobby too', async ({ as }) => {
    const owner = await apiAs('owner');
    const created = await owner.post('/meetings', { title: 'Board prep', startsAt: iso(at(6, 10)), endsAt: iso(at(6, 11)), inviteeIds: [idOf('pat')] });
    const id = created.meeting.id;
    const sam = await as('sam');
    await sam.page.goto(`/calls/${id}`);
    await expect(sam.page.getByText('This is a private call')).toBeVisible();
    await sam.page.getByRole('button', { name: 'Ask to join' }).click();
    await expect(sam.page.getByText('Waiting for someone to let you in')).toBeVisible();

    // Signed in afterwards, so the knock is only a notification.
    const olivia = await as('owner');
    await olivia.page.getByRole('button', { name: /^Notifications, / }).click();
    await olivia.page.getByText(`${NAME('sam')} asks to join a call`).first().click();
    await expect(olivia.page).toHaveURL(new RegExp(`/calls/${id}`));
    await expect(lobbyJoin(olivia.page)).toBeVisible();
    expect(await statusOf(owner, id), 'the organizer only looked').toBe('scheduled');

    const admit = olivia.page.getByRole('button', { name: `Admit ${NAME('sam')}` });
    await expect(admit).toBeVisible();
    await admit.click();
    await expect(admit).toHaveCount(0);

    await expect(lobbyJoin(sam.page)).toBeVisible();
    await expect(sam.page.getByRole('heading', { name: 'Board prep' })).toBeVisible();
    expect(await statusOf(owner, id), 'being let in is being invited, not starting it').toBe('scheduled');
    await owner.dispose();
  });

  test('a cancelled meeting says so', async ({ as }) => {
    const owner = await apiAs('owner');
    const created = await owner.post('/meetings', { title: 'Off', startsAt: iso(at(5, 10)), endsAt: iso(at(5, 11)), inviteeIds: [idOf('alice')] });
    await owner.del(`/meetings/${created.meeting.id}`);
    const alice = await as('alice');
    await alice.page.goto(`/calls/${created.meeting.id}`);
    await expect(alice.page.getByText('This meeting was cancelled.')).toBeVisible();
    await owner.dispose();
  });
});

test.describe('ticket 22 and 31: a standing meeting on your own calendar', () => {
  test('names nobody else who came, and offers no log you could not read', async ({ as }) => {
    const owner = await apiAs('owner');
    const name = `Morning ${Date.now()}`;
    const clock = minutesFromNow(-5);
    const timeOfDay = `${String(clock.getHours()).padStart(2, '0')}:${String(clock.getMinutes()).padStart(2, '0')}`;
    const made = await owner.post('/standing-meetings', { name, timeOfDay, timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone, weekdays: [1, 2, 3, 4, 5, 6, 7] });
    const roomOf = made.standingMeeting.id;

    const pat = await as('pat');
    const alice = await as('alice');
    for (const person of [pat, alice]) {
      await person.page.goto('/calls');
      await person.page.getByRole('region', { name: 'Standing meetings' }).getByRole('button', { name: `Join ${name}` }).click();
      await expect(inACall(person.page)).toBeVisible({ timeout: 30_000 });
    }
    const callId = (await owner.post(`/standing-meetings/${roomOf}/join`)).callId;
    await waitForLog(owner, callId, 'both connected', (l) => l.sessions.filter((s) => !s.leftAt).length >= 2);

    // The call log button is not offered inside the room to somebody who may not read it.
    await alice.page.getByRole('button', { name: 'Minimise the call' }).waitFor();
    await expect(alice.page.getByRole('button', { name: 'Show the call log' })).toHaveCount(0);

    await openOnCalendar(alice.page, name);
    const dialog = alice.page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText(NAME('pat'))).toHaveCount(0);
    await expect(dialog.getByText('Invited:')).toHaveCount(0);
    await expect(dialog.getByRole('button', { name: 'Log' })).toHaveCount(0);
    // It recurs every day; left behind it crowds other tests' calendars.
    await owner.del(`/standing-meetings/${roomOf}`);
    await owner.dispose();
  });
});

test.describe('ticket 26: somebody deactivated', () => {
  test('is taken out of the call they are in, and cannot get back in', async ({ as }) => {
    const leaver = { key: 'leaver', name: 'Lee Leaver', email: 'lee.leaver@e2e.test', password: 'E2e-pass-12345!' };
    const id = await ensureMember(leaver);
    const owner = await apiAs('owner');
    const call = await createCall(owner, 'Last day', [id]);
    const olivia = await as('owner');
    const lee = await as(leaver);
    await joinCall(olivia.page, call.id);
    await joinCall(lee.page, call.id);
    await waitForLog(owner, call.id, 'both connected', (l) => l.sessions.filter((s) => !s.leftAt).length === 2);

    await owner.del(`/users/${id}`);

    await expect(inACall(lee.page), 'out of the room without doing anything').toHaveCount(0, { timeout: 15_000 });
    await waitForLog(owner, call.id, 'Lee is recorded as having left', (l) => l.sessions.some((s) => s.userId === id && !!s.leftAt));
    await expect(inACall(olivia.page), 'and the call goes on for everybody else').toBeVisible();
    // Their session is over, so their own page sends them to sign in; asking
    // for the call again must not get them back in either.
    await expect(lee.page).toHaveURL(/\/login/, { timeout: 15_000 });
    await lee.page.goto(`/calls/${call.id}`);
    await expect(lee.page).toHaveURL(/\/login/);
    await owner.dispose();
    // Their own page logged the refusals on the way out, which is the point of this test.
    lee.problems.length = 0;
  });
});

test.describe('ticket 28: a browser that will not play the call', () => {
  test('says so, and a click starts the sound', async ({ as }) => {
    const owner = await apiAs('owner');
    const call = await createCall(owner, 'Blocked audio', [idOf('alice')]);
    const alice = await as('alice');
    // What a browser with no engagement for this site does until it has had a click.
    await alice.context.addInitScript(() => {
      const realPlay = HTMLMediaElement.prototype.play;
      const w = window as unknown as { __allowed: boolean };
      w.__allowed = false;
      document.addEventListener('click', () => (w.__allowed = true), true);
      HTMLMediaElement.prototype.play = function (this: HTMLMediaElement) {
        if (!w.__allowed) return Promise.reject(new DOMException('needs a gesture', 'NotAllowedError'));
        return realPlay.call(this);
      };
    });
    const olivia = await as('owner');
    await joinCall(olivia.page, call.id);
    await olivia.page.getByRole('button', { name: 'Turn your microphone on' }).click();
    await joinCall(alice.page, call.id);

    const button = alice.page.getByRole('button', { name: 'Turn on sound' });
    await expect(button).toBeVisible({ timeout: 20_000 });
    const shots = process.env.E2E_SHOTS_DIR ?? 'e2e-shots';
    await alice.page.screenshot({ path: `${shots}/blocked-audio__1280__light.png` });
    await alice.page.setViewportSize({ width: 375, height: 812 });
    await alice.page.evaluate(() => document.documentElement.classList.add('dark'));
    await alice.page.waitForTimeout(500); // colours fade
    await alice.page.screenshot({ path: `${shots}/blocked-audio__375__dark.png` });
    await alice.page.evaluate(() => document.documentElement.classList.remove('dark'));
    await alice.page.setViewportSize({ width: 1280, height: 800 });
    await button.click();
    await expect(button).toHaveCount(0);
    await owner.dispose();
  });
});

test.describe('ticket 32: the knock prompt in a second tab', () => {
  test('goes away when the same person answers in the first', async ({ as }) => {
    const owner = await apiAs('owner');
    const call = await createCall(owner, 'Two tabs', [idOf('max')]);
    const olivia = await as('owner');
    const second = await olivia.context.newPage();
    await joinCall(olivia.page, call.id);
    await second.goto('/calls');
    await waitForLog(owner, call.id, 'connected', (l) => l.sessions.some((s) => !s.leftAt));

    const sam = await as('sam');
    await sam.page.goto(`/calls/${call.id}`);
    await sam.page.getByRole('button', { name: 'Ask to join' }).click();
    const prompt = (page: Page) => page.getByRole('button', { name: `Deny ${NAME('sam')}` });
    await expect(prompt(olivia.page)).toBeVisible();
    await expect(prompt(second)).toBeVisible();

    await prompt(olivia.page).click();
    await expect(prompt(second), 'it was answered').toHaveCount(0);
    await owner.dispose();
  });
});

test.describe('ticket 33: a valid session with empty local storage', () => {
  test('stays where it was going, and a signed-out person is still sent to sign in', async ({ as }) => {
    const alice = await as('alice');
    await alice.page.goto('/calendar');
    await expect(alice.page.getByRole('main').getByRole('button', { name: 'Block time' })).toBeVisible();
    await alice.page.evaluate(() => localStorage.removeItem('auth-storage'));

    await alice.page.goto('/calendar?date=2026-11-09');
    await expect(alice.page).toHaveURL(/\/calendar\?date=2026-11-09/);
    await expect(alice.page.getByRole('main').getByRole('button', { name: 'Block time' })).toBeVisible();

    const nobody = await alice.context.browser()!.newContext({ baseURL: new URL(alice.page.url()).origin });
    const page = await nobody.newPage();
    await page.goto('/calendar?date=2026-11-09');
    await expect(page).toHaveURL(/\/login\?next=/);
    await expect(page.getByLabel('Email')).toBeVisible();
    await nobody.close();
  });
});

test.describe('ticket 34: following a link to a call that is ringing you', () => {
  test('stops the ringing as soon as you are on your way in', async ({ as }) => {
    const owner = await apiAs('owner');
    const alice = await as('alice');
    await spyOnAudio(alice);
    await alice.page.goto('/chat');
    const olivia = await as('owner');
    const call = await createCall(owner, 'Ring check', [idOf('alice')]);
    await joinCall(olivia.page, call.id);
    const ringing = alice.page.getByRole('dialog').filter({ hasText: /^Incoming/ });
    await expect(ringing).toBeVisible();

    // A link inside the app, followed while it rings. Asking for a token takes a moment.
    await alice.page.route(`**/api/calls/${call.id}/token`, async (route) => {
      await new Promise((resolve) => setTimeout(resolve, 4000));
      await route.continue();
    });
    await alice.page.evaluate((id) => {
      history.pushState({}, '', `/calls/${id}`);
      dispatchEvent(new PopStateEvent('popstate'));
    }, call.id);
    await expect(alice.page.getByRole('status', { name: 'Joining the call' })).toBeVisible();
    await expect(ringing, 'gone while the token is still on its way').toHaveCount(0, { timeout: 1500 });
    const rings = await oscillators(alice.page);
    await expect(inACall(alice.page)).toBeVisible({ timeout: 30_000 });
    expect(await oscillators(alice.page), 'and it rang no more').toBe(rings);
    await owner.dispose();
  });
});

test.describe('ticket 35: an earlier call ringing out while you are in another', () => {
  test('does not pull you out of the one you are in', async ({ as }) => {
    const owner = await apiAs('owner');
    const pat = await apiAs('pat');
    const unanswered = await createCall(owner, '', [idOf('pat')]);
    const current = await createCall(owner, 'The one I am in', [idOf('max')]);
    const olivia = await as('owner');
    await joinCall(olivia.page, current.id);

    await pat.post(`/calls/${unanswered.id}/decline`, { reason: 'no_answer' });
    await olivia.page.waitForTimeout(1500);
    await expect(olivia.page).toHaveURL(new RegExp(`/calls/${current.id}`));
    await expect(olivia.page.getByRole('button', { name: 'Minimise the call' })).toBeVisible();
    await Promise.all([owner.dispose(), pat.dispose()]);
  });

  test('does not pull you away while you are still joining another', async ({ as }) => {
    const owner = await apiAs('owner');
    const pat = await apiAs('pat');
    const unanswered = await createCall(owner, '', [idOf('pat')]);
    const next = await createCall(owner, 'The one I am joining', [idOf('max')]);
    const olivia = await as('owner');
    // Hold the join, so that the frame arrives in the moment before she is in the call.
    let release = () => {};
    const held = new Promise<void>((resolve) => (release = resolve));
    await olivia.page.route(`**/api/calls/${next.id}/token`, async (route) => {
      await held;
      await route.continue();
    });
    await olivia.page.goto(`/calls/${next.id}`);
    await olivia.page.waitForTimeout(1000);

    await pat.post(`/calls/${unanswered.id}/decline`, { reason: 'no_answer' });
    await olivia.page.waitForTimeout(1500);
    release();
    await expect(olivia.page.getByRole('button', { name: 'Leave the call' })).toBeVisible({ timeout: 30_000 });
    await expect(olivia.page).toHaveURL(new RegExp(`/calls/${next.id}`));
    await Promise.all([owner.dispose(), pat.dispose()]);
  });

  test('does not pull you away from what you moved on to, in no call at all', async ({ as }) => {
    const owner = await apiAs('owner');
    const pat = await apiAs('pat');
    const unanswered = await createCall(owner, '', [idOf('pat')]);
    const olivia = await as('owner');
    await olivia.page.goto('/tasks');
    await expect(olivia.page.getByRole('main')).toBeVisible();

    await pat.post(`/calls/${unanswered.id}/decline`, { reason: 'no_answer' });
    await olivia.page.waitForTimeout(1500);
    await expect(olivia.page).toHaveURL(/\/tasks/);
    await Promise.all([owner.dispose(), pat.dispose()]);
  });
});

test.describe('decision 38: adding yourself to a task', () => {
  test('is not Added to; somebody else adding you is', async ({ as }) => {
    const owner = await apiAs('owner');
    const title = `Follow me ${Date.now()}`;
    const task = await owner.post('/tasks', { title, assigneeId: idOf('pat'), status: 'in_progress' });

    const sam = await as('sam');
    await sam.page.goto(`/tasks/${task.id}`);
    await sam.page.getByRole('button', { name: 'Follow this task' }).click();
    await expect.poll(async () => ((await owner.get(`/tasks/${task.id}/watchers`)) as any).users.map((u: any) => u.userId)).toContain(idOf('sam'));

    await openTasks(sam.page);
    await expect(sam.page.getByRole('main').getByText(title)).toHaveCount(0);

    await owner.post(`/tasks/${task.id}/watchers`, { userId: idOf('sam') });
    await sam.page.reload();
    await expect(sam.page.getByRole('heading', { name: /Added to/ })).toBeVisible();
    await expect(sam.page.getByRole('main').getByText(title)).toBeVisible();
    await owner.dispose();
  });
});
