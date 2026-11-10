import type { Page } from '@playwright/test';
import { PEOPLE, readState } from './support/people';
import { createTaskInUi, expect, openTasks, test } from './support/fixtures';
import {
  EXTRA,
  apiAs,
  at,
  dayParam,
  iso,
  localInput,
  createCall,
  ensureMember,
  ensureOtherOrg,
  idOf,
  joinCall,
  leaveCall,
  oscillators,
  sql,
  resetOscillators,
  spyOnAudio,
  waitForLog,
} from './support/meetings';

// Real server with the frontend embedded, real Postgres, Redis, MinIO, a real
// LiveKit that posts its webhooks to the server, and real Chromium with a fake
// camera, microphone and screen. People sign in through the login form, one
// browser context each. Every test makes its own calls, so they do not depend
// on one another, but they do share the organization the seed built.

const NAME = (k: keyof typeof PEOPLE) => PEOPLE[k].name;

test.describe('1. a call, its attendance, and the join sound', () => {
  test('webhooks reach the server: two people in a call produce attendance rows', async ({ as }) => {
    const olivia = await as('owner');
    const alice = await as('alice');
    const api = await apiAs('owner');
    const call = await createCall(api, 'Attendance check', [idOf('alice')]);

    await joinCall(olivia.page, call.id);
    await joinCall(alice.page, call.id);

    const log = await waitForLog(api, call.id, 'both people to have an open session', (l) =>
      [idOf('owner'), idOf('alice')].every((id) => l.sessions.some((s) => s.userId === id && s.joinedAt && !s.leftAt))
    );
    expect(log.attendanceRecorded).toBe(true);
    expect(log.events.filter((e) => e.type === 'joined').map((e) => e.userId).sort()).toEqual([idOf('owner'), idOf('alice')].sort());

    // The log panel shows the same rows to the organizer.
    await olivia.page.getByRole('button', { name: 'Show the call log' }).click();
    const dialog = olivia.page.getByRole('dialog', { name: 'Call log' });
    await expect(dialog.getByText(`${NAME('alice')} joined`)).toBeVisible();
    await expect(dialog.getByText(`${NAME('owner')} joined`)).toBeVisible();
    await olivia.page.keyboard.press('Escape');

    // Leaving closes the session; coming back opens a second one: two sessions for Alice, both kept.
    await leaveCall(alice.page);
    await waitForLog(api, call.id, 'Alice\'s first session to close', (l) =>
      l.sessions.some((s) => s.userId === idOf('alice') && s.leftAt)
    );
    await joinCall(alice.page, call.id);
    const again = await waitForLog(api, call.id, 'Alice to have two sessions, one open', (l) => {
      const mine = l.sessions.filter((s) => s.userId === idOf('alice'));
      return mine.length === 2 && mine.some((s) => !s.leftAt) && mine.some((s) => s.leftAt);
    });
    expect(again.events.filter((e) => e.type === 'left' && e.userId === idOf('alice'))).toHaveLength(1);

    await leaveCall(alice.page);
    await leaveCall(olivia.page);
    await waitForLog(api, call.id, 'everybody to have left', (l) => l.sessions.length >= 3 && l.sessions.every((s) => s.leftAt));
    await api.dispose();
  });

  test('others see a toast and hear a chime when somebody joins; you do not for yourself', async ({ as }) => {
    const olivia = await as('owner');
    const pat = await as('pat');
    const sam = await as('sam');
    await Promise.all([olivia, pat, sam].map(spyOnAudio));
    const api = await apiAs('owner');
    const call = await createCall(api, 'Chime check', [idOf('pat'), idOf('sam')]);

    await joinCall(olivia.page, call.id);
    // Really connected, and then past the moment when arrivals are the room you walked into, which is not news.
    await waitForLog(api, call.id, 'Olivia connected', (l) => l.sessions.some((s) => !s.leftAt));
    await olivia.page.waitForTimeout(2500);
    expect(await oscillators(olivia.page), 'nobody else was there, so no chime for Olivia').toBe(0);

    await joinCall(pat.page, call.id);
    await resetOscillators(pat.page); // the ring that brought Pat here is not a chime
    await expect(olivia.page.getByText(`${NAME('pat')} joined the call`, { exact: true })).toBeVisible();
    await expect.poll(() => oscillators(olivia.page), { message: 'Olivia hears a chime' }).toBeGreaterThan(0);
    const afterPat = await oscillators(olivia.page);

    // Pat walked into a room with Olivia already in it: no toast naming Olivia, no chime.
    await pat.page.waitForTimeout(3000);
    await expect(pat.page.getByText(/joined the call/)).toHaveCount(0);
    expect(await oscillators(pat.page), 'Pat hears nothing about their own arrival').toBe(0);

    // A third arrival is announced to both of them, and not to Sam.
    await joinCall(sam.page, call.id);
    await resetOscillators(sam.page);
    await expect(olivia.page.getByText(`${NAME('sam')} joined the call`, { exact: true })).toBeVisible();
    await expect(pat.page.getByText(`${NAME('sam')} joined the call`, { exact: true })).toBeVisible();
    await expect.poll(() => oscillators(olivia.page)).toBeGreaterThan(afterPat);
    await expect.poll(() => oscillators(pat.page)).toBeGreaterThan(0);
    await sam.page.waitForTimeout(2500);
    await expect(sam.page.getByText(/joined the call/)).toHaveCount(0);
    expect(await oscillators(sam.page)).toBe(0);

    for (const s of [sam, pat, olivia]) await leaveCall(s.page);
    await api.dispose();
  });
});

async function stageText(page: Page) {
  return page.getByText(/ is presenting$| are presenting$/).allInnerTexts();
}

test.describe('2. screen share', () => {
  test('a non-organizer shares and the organizer sees it; two sharers; swapping the stage; stopping is logged', async ({ as }) => {
    const olivia = await as('owner');
    const alice = await as('alice');
    const api = await apiAs('owner');
    const call = await createCall(api, 'Share check', [idOf('alice')]);
    await joinCall(olivia.page, call.id);
    await joinCall(alice.page, call.id);

    // Alice is not the organizer and no permission was granted to her: she just shares.
    await alice.page.getByRole('button', { name: 'Share your screen' }).click();
    await expect(alice.page.getByRole('button', { name: 'Stop sharing your screen' })).toBeVisible();
    await expect(olivia.page.getByText(`${NAME('alice')} is presenting`)).toBeVisible();
    expect(await stageText(olivia.page)).toEqual([`${NAME('alice')} is presenting`]);

    // The organizer shares too. One takes the stage; the other waits in the strip until clicked.
    await olivia.page.getByRole('button', { name: 'Share your screen' }).click();
    await expect(olivia.page.getByRole('button', { name: 'Stop sharing your screen' })).toBeVisible();
    const swap = alice.page.getByRole('button', { name: `Show ${NAME('owner')}'s screen on stage` });
    await expect(swap).toBeVisible();
    await expect(alice.page.getByText('You are presenting')).toBeVisible();
    await swap.click();
    await expect(alice.page.getByText(`${NAME('owner')} is presenting`)).toBeVisible();
    // And back again: Alice's own share is now the one in the strip.
    await alice.page.getByRole('button', { name: /^Show your screen on stage$|^Show .*screen on stage$/ }).first().click();
    await expect(alice.page.getByText('You are presenting')).toBeVisible();

    await olivia.page.getByRole('button', { name: 'Stop sharing your screen' }).click();
    await alice.page.getByRole('button', { name: 'Stop sharing your screen' }).click();
    await expect(olivia.page.getByText(/is presenting/)).toHaveCount(0);

    const log = await waitForLog(api, call.id, 'both shares started and stopped in the log', (l) =>
      ['screen_share_started', 'screen_share_stopped'].every((type) =>
        [idOf('alice'), idOf('owner')].every((id) => l.events.some((e) => e.type === type && e.userId === id))
      )
    );
    const started = log.events.filter((e) => e.type === 'screen_share_started');
    const stopped = log.events.filter((e) => e.type === 'screen_share_stopped');
    expect(started).toHaveLength(2);
    expect(stopped).toHaveLength(2);
    for (const stop of stopped) {
      const start = started.find((s) => s.userId === stop.userId)!;
      expect(new Date(stop.at).getTime()).toBeGreaterThanOrEqual(new Date(start.at).getTime());
    }

    // The log panel tells the same story in words.
    await olivia.page.getByRole('button', { name: 'Show the call log' }).click();
    const dialog = olivia.page.getByRole('dialog', { name: 'Call log' });
    await expect(dialog.getByText(`${NAME('alice')} started sharing their screen`)).toBeVisible();
    await expect(dialog.getByText(`${NAME('alice')} stopped sharing their screen`)).toBeVisible();
    await olivia.page.keyboard.press('Escape');
    await leaveCall(alice.page);
    await leaveCall(olivia.page);
    await api.dispose();
  });
});


interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

/** Where every tile in the call is on the page. */
async function tileRects(page: Page): Promise<Rect[]> {
  return page.evaluate(() =>
    Array.from(document.querySelectorAll('div.group.relative.overflow-hidden.rounded-lg')).map((el) => {
      const r = el.getBoundingClientRect();
      return { x: r.x, y: r.y, w: r.width, h: r.height };
    })
  );
}

function checkGrid(tiles: Rect[], count: number, viewportWidth: number) {
  expect(tiles, `${count} people`).toHaveLength(count);
  const w = tiles[0].w;
  for (const t of tiles) {
    expect(Math.abs(t.w - w), 'every tile is the same size').toBeLessThanOrEqual(1);
    expect(Math.abs(t.w / t.h - 16 / 9), 'tiles are 16:9').toBeLessThan(0.05);
    expect(t.x).toBeGreaterThanOrEqual(-1);
    expect(t.x + t.w).toBeLessThanOrEqual(viewportWidth + 1);
  }
  for (let i = 0; i < tiles.length; i++) {
    for (let j = i + 1; j < tiles.length; j++) {
      const a = tiles[i];
      const b = tiles[j];
      const overlap = a.x < b.x + b.w - 1 && b.x < a.x + a.w - 1 && a.y < b.y + b.h - 1 && b.y < a.y + a.h - 1;
      expect(overlap, `tiles ${i} and ${j} overlap`).toBe(false);
    }
  }
  // The last row may be short and centred, so columns are the most tiles that share a row.
  const perRow = new Map<number, number>();
  for (const t of tiles) perRow.set(Math.round(t.y), (perRow.get(Math.round(t.y)) ?? 0) + 1);
  return { columns: Math.max(...perRow.values()), rows: perRow.size };
}

test.describe('3. tile layout', () => {
  test('1, 2, 3, 5 and 9 real people fill the window with equal 16:9 tiles', async ({ as }) => {
    test.setTimeout(240_000);
    const ids = {
      zoe: await ensureMember(EXTRA.zoe),
      zed: await ensureMember(EXTRA.zed),
    };
    const api = await apiAs('owner');
    const others = ['alice', 'ann', 'pat', 'sam', 'max', 'mia'] as const;
    const call = await createCall(api, 'All hands', [...others.map(idOf), ids.zoe, ids.zed]);
    const olivia = await as('owner');
    await joinCall(olivia.page, call.id);

    const arrivals: (typeof others[number] | 'zoe' | 'zed')[] = ['alice', 'ann', 'pat', 'sam', 'max', 'mia', 'zoe', 'zed'];
    const expectAt = new Set([1, 2, 3, 5, 9]);
    const seen: Record<number, { columns: number; rows: number }> = {};
    const check = async (n: number) => {
      await expect.poll(async () => (await tileRects(olivia.page)).length, { timeout: 30_000 }).toBe(n);
      await olivia.page.waitForTimeout(600); // layout settles after the last tile
      seen[n] = checkGrid(await tileRects(olivia.page), n, 1280);
      // And it is the arrangement that makes the tiles largest in the room they have.
      const room = await olivia.page.evaluate(() => {
        const r = document.querySelector('div.flex.min-h-0.flex-1.items-center.justify-center.overflow-hidden')!.getBoundingClientRect();
        return { w: r.width, h: r.height };
      });
      let best = 0;
      for (let columns = 1; columns <= n; columns++) {
        const rows = Math.ceil(n / columns);
        best = Math.max(best, Math.min((room.w - 8 * (columns - 1)) / columns, ((room.h - 8 * (rows - 1)) / rows) * (16 / 9)));
      }
      const [first] = await tileRects(olivia.page);
      if (n === 2) {
        // Decision 37 gives up the extra size here, for two across.
        best = Math.min((room.w - 8) / 2, room.h * (16 / 9));
      }
      expect(first.w, `${n} people in ${room.w}x${room.h}: tile width ${first.w} against the best possible ${best}`).toBeGreaterThanOrEqual(best - 2);
    };
    await check(1);
    expect(seen[1]).toEqual({ columns: 1, rows: 1 });
    let count = 1;
    for (const who of arrivals) {
      const person = who === 'zoe' || who === 'zed' ? EXTRA[who] : who;
      const s = await as(person);
      await joinCall(s.page, call.id);
      count++;
      if (expectAt.has(count)) await check(count);
    }
    console.log('tile grids (columns x rows) by number of people:', JSON.stringify(seen));
    // Two people on a laptop window sit side by side, even though stacking would be a little larger (decision 37).
    expect(seen[2]).toEqual({ columns: 2, rows: 1 });
    expect(seen[3].columns).toBeLessThanOrEqual(3);
    expect(seen[9]).toEqual({ columns: 3, rows: 3 });

    // The same nine on a phone: nothing scrolls sideways, and all nine are still tiles.
    await olivia.page.setViewportSize({ width: 375, height: 812 });
    await olivia.page.waitForTimeout(800);
    const tiles = await tileRects(olivia.page);
    expect(tiles).toHaveLength(9);
    const scrollW = await olivia.page.evaluate(() => document.documentElement.scrollWidth);
    expect(scrollW, 'no sideways scroll on a phone').toBeLessThanOrEqual(375);
    for (const t of tiles) expect(t.x + t.w).toBeLessThanOrEqual(376);
    console.log('tile grids (columns x rows) by number of people:', JSON.stringify(seen));
    await api.dispose();
  });
});

const admitButton = (page: Page, who: string) => page.getByRole('button', { name: `Admit ${who}` });
const denyButton = (page: Page, who: string) => page.getByRole('button', { name: `Deny ${who}` });

test.describe('4. knocking on a private call', () => {
  test('a non-member asks, is turned away, asks again after a minute, is let in with no reload, and stays a member', async ({ as }) => {
    test.setTimeout(180_000);
    const olivia = await as('owner');
    const pat = await as('pat');
    const sam = await as('sam');
    const api = await apiAs('owner');
    const call = await createCall(api, 'Board call', [idOf('pat')]);
    await joinCall(olivia.page, call.id);
    await joinCall(pat.page, call.id);
    // Pings go to who is connected, which webhooks say.
    await waitForLog(api, call.id, 'Olivia and Pat connected', (l) => l.sessions.filter((s) => !s.leftAt).length === 2);

    await sam.page.goto(`/calls/${call.id}`);
    await expect(sam.page.getByText('This is a private call')).toBeVisible();
    // Opening the link pinged nobody.
    await expect(admitButton(olivia.page, NAME('sam'))).toHaveCount(0);
    await sam.page.getByRole('button', { name: 'Ask to join' }).click();
    await expect(sam.page.getByText('Waiting for someone to let you in')).toBeVisible();
    await expect(admitButton(olivia.page, NAME('sam'))).toBeVisible();
    await expect(admitButton(pat.page, NAME('sam'))).toBeVisible();

    // Pat turns Sam away: Sam is told, and the prompt clears for Olivia.
    await denyButton(pat.page, NAME('sam')).click();
    await expect(sam.page.getByText('You were not let in')).toBeVisible();
    await expect(admitButton(olivia.page, NAME('sam'))).toHaveCount(0);
    await expect(sam.page.getByRole('button', { name: 'Ask again' })).toBeDisabled();

    // A reload forgets the countdown; asking says when, and does not ping anybody.
    await sam.page.reload();
    await sam.page.getByRole('button', { name: 'Ask to join' }).click();
    await expect(sam.page.getByText(/You can ask again in \d+ seconds/)).toBeVisible();
    await expect(admitButton(olivia.page, NAME('sam'))).toHaveCount(0);

    await expect(sam.page.getByRole('button', { name: 'Ask again' })).toBeEnabled({ timeout: 70_000 });
    await sam.page.getByRole('button', { name: 'Ask again' }).click();
    await expect(sam.page.getByText('Waiting for someone to let you in')).toBeVisible();
    await expect(admitButton(olivia.page, NAME('sam'))).toBeVisible();
    await admitButton(olivia.page, NAME('sam')).click();

    // In, with no reload; Pat's prompt clears; the others hear about the arrival.
    await expect(sam.page.getByRole('button', { name: 'Leave the call' })).toBeVisible();
    await expect(admitButton(pat.page, NAME('sam'))).toHaveCount(0);
    await expect(pat.page.getByText(`${NAME('sam')} joined the call`, { exact: true })).toBeVisible();

    // Admitted people are members: reconnecting does not knock again.
    await sam.page.reload();
    await expect(sam.page.getByRole('button', { name: 'Leave the call' })).toBeVisible({ timeout: 30_000 });
    await expect(sam.page.getByText('This is a private call')).toHaveCount(0);

    const log = await waitForLog(api, call.id, 'knocked, denied, knocked, admitted in the log', (l) =>
      ['knocked', 'denied', 'admitted'].every((t) => l.events.some((e) => e.type === t))
    );
    const kinds = log.events.filter((e) => ['knocked', 'denied', 'admitted'].includes(e.type));
    expect(kinds.map((e) => e.type)).toEqual(['knocked', 'denied', 'knocked', 'admitted']);
    expect(kinds[1].actorId).toBe(idOf('pat'));
    expect(kinds[3].actorId).toBe(idOf('owner'));
    expect(kinds.every((e) => e.userId === idOf('sam'))).toBe(true);
    await api.dispose();
  });

  test('nobody connected: the organizer and invited people are asked from wherever they are, and either can let the person in', async ({ as }) => {
    const olivia = await as('owner');
    const alice = await as('alice');
    const sam = await as('sam', { width: 375, height: 700 });
    const api = await apiAs('owner');
    await olivia.page.goto('/chat');
    await alice.page.goto('/chat');
    const meeting = (
      await api.post('/meetings', {
        title: 'Planning',
        startsAt: new Date(Date.now() + 10 * 60_000).toISOString(),
        endsAt: new Date(Date.now() + 40 * 60_000).toISOString(),
        inviteeIds: [idOf('alice')],
      })
    ).meeting;

    await sam.page.goto(`/calls/${meeting.id}`);
    await sam.page.getByRole('button', { name: 'Ask to join' }).click();
    await expect(sam.page.getByText('Waiting for someone to let you in')).toBeVisible();
    await expect(admitButton(olivia.page, NAME('sam'))).toBeVisible();
    await expect(admitButton(alice.page, NAME('sam'))).toBeVisible();

    await admitButton(alice.page, NAME('sam')).click();
    await expect(admitButton(olivia.page, NAME('sam'))).toHaveCount(0);
    await expect(sam.page.getByRole('button', { name: 'Minimise the call' })).toBeVisible();
    await api.dispose();
  });

  test('the call ends while somebody waits', async ({ as }) => {
    const olivia = await as('owner');
    const pat = await as('pat');
    const max = await as('max');
    const api = await apiAs('owner');
    const call = await createCall(api, 'Short call', [idOf('pat')]);
    await joinCall(olivia.page, call.id);
    await joinCall(pat.page, call.id);
    await max.page.goto(`/calls/${call.id}`);
    await max.page.getByRole('button', { name: 'Ask to join' }).click();
    await expect(max.page.getByText('Waiting for someone to let you in')).toBeVisible();
    await expect(admitButton(olivia.page, NAME('max'))).toBeVisible();

    await leaveCall(pat.page);
    await leaveCall(olivia.page);
    await expect(max.page.getByText('This call has ended')).toBeVisible({ timeout: 30_000 });
    // Arriving after the end says the same, and there is nothing to ask.
    await max.page.reload();
    await expect(max.page.getByText('This call has ended.')).toBeVisible();
    await expect(max.page.getByRole('button', { name: /ask (again )?to join/i })).toHaveCount(0);
    await api.dispose();
  });

  test('an org admin and a manager who can read the log still knock like anybody else', async ({ as }) => {
    const sam = await as('sam');
    const pat = await as('pat');
    const olivia = await as('owner');
    const mia = await as('mia');
    const samApi = await apiAs('sam');
    const call = await createCall(samApi, 'Private sync', [idOf('pat')]);
    await joinCall(sam.page, call.id);
    await joinCall(pat.page, call.id);

    // Mia answers for Pat, and so may read the log (decision 11), but is not in the call.
    const miaApi = await apiAs('mia');
    expect(await miaApi.status('get', `/calls/${call.id}/log`)).toBe(200);
    expect(await miaApi.status('post', `/calls/${call.id}/token`)).toBe(403);
    await mia.page.goto(`/calls/${call.id}`);
    await expect(mia.page.getByText('This is a private call')).toBeVisible();
    await expect(mia.page.getByRole('button', { name: 'Ask to join' })).toBeVisible();

    // The org admin is no different at the door.
    const ownerApi = await apiAs('owner');
    expect(await ownerApi.status('post', `/calls/${call.id}/token`)).toBe(403);
    await olivia.page.goto(`/calls/${call.id}`);
    await expect(olivia.page.getByText('This is a private call')).toBeVisible();
    await olivia.page.getByRole('button', { name: 'Ask to join' }).click();
    await expect(admitButton(sam.page, NAME('owner'))).toBeVisible();
    // Admin cannot answer for the call either: not a member.
    expect(await ownerApi.status('post', `/calls/${call.id}/requests/00000000-0000-0000-0000-000000000000/admit`)).toBeGreaterThanOrEqual(400);
    await admitButton(sam.page, NAME('owner')).click();
    await expect(olivia.page.getByRole('button', { name: 'Leave the call' })).toBeVisible();
    await Promise.all([miaApi.dispose(), samApi.dispose(), ownerApi.dispose()]);
  });
});

test.describe('5. who may read a call log', () => {
  test('member, admin and a manager with authority read it; an unrelated person and another organization get 404', async ({ as }) => {
    await ensureOtherOrg();
    const aliceApi = await apiAs('alice');
    const call = await createCall(aliceApi, 'Log permissions', [idOf('pat')]);

    const readers = { alice: 200, pat: 200, owner: 200, mia: 200, max: 404, sam: 404, ann: 404 } as const;
    for (const [who, want] of Object.entries(readers)) {
      const api = await apiAs(who as keyof typeof PEOPLE);
      expect(await api.status('get', `/calls/${call.id}/log`), `${who} reading the log`).toBe(want);
      await api.dispose();
    }
    const olga = await apiAs(EXTRA.olga);
    expect(await olga.status('get', `/calls/${call.id}/log`), 'other organization reading the log').toBe(404);
    expect(await olga.status('get', `/calls/${call.id}`), 'other organization reading the call').toBe(404);
    expect(await olga.status('post', `/calls/${call.id}/token`), 'other organization asking for a token').toBe(404);
    expect(await olga.status('post', `/calls/${call.id}/knock`), 'other organization knocking').toBe(404);

    // Somebody who may not read the log gets the bare call, with no title and no people.
    const sam = await apiAs('sam');
    const bare = await sam.get(`/calls/${call.id}`);
    expect(bare.canKnock).toBe(true);
    expect(JSON.stringify(bare)).not.toContain('Log permissions');
    expect(Object.keys(bare).sort()).toEqual(['callType', 'canKnock', 'canReadLog', 'id', 'status']);
    const full = await aliceApi.get(`/calls/${call.id}`);
    expect(JSON.stringify(full)).toContain('Log permissions');

    // The trail has no way to be edited.
    for (const method of ['put', 'patch', 'delete', 'post'] as const) {
      expect([404, 405], `${method} on the log`).toContain(await aliceApi.status(method, `/calls/${call.id}/log`, {}));
    }
    await Promise.all([sam.dispose(), olga.dispose(), aliceApi.dispose()]);

    // What a person sees: the unrelated one gets the door, the other organization a not-found screen.
    const samSession = await as('sam');
    await samSession.page.goto(`/calls/${call.id}`);
    await expect(samSession.page.getByText('This is a private call')).toBeVisible();
    await expect(samSession.page.getByText('Log permissions')).toHaveCount(0);
    const stranger = await as(EXTRA.olga);
    await stranger.page.goto(`/calls/${call.id}`);
    await expect(stranger.page.getByText('This call does not exist, or is not in your organization.')).toBeVisible();
    await expect(stranger.page.getByText('This is a private call')).toHaveCount(0);
    await expect(stranger.page.getByText('Log permissions')).toHaveCount(0);
    await expect(stranger.page.getByRole('button', { name: /ask to join/i })).toHaveCount(0);
  });
});

const calendarUrl = (day: Date, person?: string) => `/calendar?date=${dayParam(day)}${person ? `&person=${person}` : ''}`;
const eventButton = (page: Page, title: string | RegExp) =>
  page.getByRole('main').getByRole('button', { name: typeof title === 'string' ? new RegExp(title) : title });

async function fillTimes(page: Page, fields: [string, string], from: Date, to: Date) {
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel(fields[0], { exact: true }).fill(localInput(from));
  await dialog.getByLabel(fields[1], { exact: true }).fill(localInput(to));
}

/** A meeting is a start and a length picked from the list. */
async function fillMeetingTime(page: Page, from: Date, duration: string) {
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Starts', { exact: true }).fill(localInput(from));
  await dialog.getByLabel('Duration', { exact: true }).click();
  await page.getByRole('option', { name: duration, exact: true }).click();
}

test.describe('6. calendar', () => {
  test('somebody blocks focus time and frees an open slot, and is added to a meeting without doing anything', async ({ as }) => {
    const alice = await as('alice');
    const olivia = await as('owner');
    const api = await apiAs('owner');
    const day = at(1, 0);
    await alice.page.goto(calendarUrl(day));
    await expect(alice.page.getByRole('heading', { level: 2 }).first()).toBeVisible();

    // Focus time, through the dialog.
    await alice.page.getByRole('button', { name: 'Block time' }).click();
    let dialog = alice.page.getByRole('dialog');
    await dialog.getByLabel('Name (optional)').fill('Writing the spec');
    await fillTimes(alice.page, ['From', 'To'], at(1, 9), at(1, 11));
    await dialog.getByRole('button', { name: 'Add to calendar' }).click();
    await expect(eventButton(alice.page, 'Writing the spec')).toBeVisible();

    // Open for meetings.
    await alice.page.getByRole('button', { name: 'Block time' }).click();
    dialog = alice.page.getByRole('dialog');
    await dialog.getByRole('button', { name: /Open for meetings/ }).click();
    await fillTimes(alice.page, ['From', 'To'], at(1, 13), at(1, 17));
    await dialog.getByRole('button', { name: 'Add to calendar' }).click();
    await expect(alice.page.getByRole('main').getByRole('button', { name: /^Open for meetings, / })).toBeVisible();

    // Olivia schedules a meeting with Alice in it. Alice does nothing and it appears.
    const meeting = await api.post('/meetings', {
      title: 'Roadmap review',
      startsAt: iso(at(1, 14)),
      endsAt: iso(at(1, 15)),
      inviteeIds: [idOf('alice')],
    });
    expect(meeting.meeting.id).toBeTruthy();
    await expect(eventButton(alice.page, 'Roadmap review')).toBeVisible({ timeout: 15_000 });
    // It sits on the organizer's own calendar too.
    await olivia.page.goto(calendarUrl(day));
    await expect(eventButton(olivia.page, 'Roadmap review')).toBeVisible();
    await api.dispose();
  });

  test('only somebody with authority opens a calendar, and scheduling sees busy time with no titles', async ({ as }) => {
    const aliceApi = await apiAs('alice');
    const olivia = await apiAs('owner');
    const day = at(2, 0);
    await aliceApi.post('/calendar/blocks', { kind: 'focus', title: 'Secret project', startsAt: iso(at(2, 9)), endsAt: iso(at(2, 10)) });
    await olivia.post('/meetings', { title: 'Confidential layoffs', startsAt: iso(at(2, 11)), endsAt: iso(at(2, 12)), inviteeIds: [idOf('alice')] });
    const secrets = ['Secret project', 'Confidential layoffs'];

    for (const [who, mayRead] of [['sam', false], ['max', false], ['ann', false], ['mia', true], ['owner', true]] as const) {
      const s = await as(who);
      const bodies: string[] = [];
      s.page.on('response', async (res) => {
        if (res.url().includes('/api/calendar')) bodies.push(await res.text().catch(() => ''));
      });
      await s.page.goto(calendarUrl(day, idOf('alice')));
      if (mayRead) await expect(s.page.getByRole('main').getByRole('button', { name: /Secret project/ }).first()).toBeVisible();
      // Without authority there is no calendar to read at all (decision 64).
      else await expect(s.page.getByRole('alert')).toContainText('only see your own calendar');
      await s.page.waitForTimeout(500);
      const dom = await s.page.getByRole('main').innerText();
      const aria = (await s.page.getByRole('main').getByRole('button').evaluateAll((els) => els.map((e) => e.getAttribute('aria-label') ?? ''))).join('\n');
      const network = bodies.join('\n');
      expect(network.length, `${who} fetched Alice's calendar`).toBeGreaterThan(10);
      for (const secret of secrets) {
        if (mayRead) {
          expect(network, `${who} has authority, so ${secret} is in the response`).toContain(secret);
          expect(`${dom}\n${aria}`, `${who} sees ${secret}`).toContain(secret);
        } else {
          expect(network, `${who} must not receive ${secret}`).not.toContain(secret);
          expect(dom, `${who} must not see ${secret} on the page`).not.toContain(secret);
          expect(aria, `${who} must not see ${secret} in a label`).not.toContain(secret);
        }
      }
      if (!mayRead) {
        const api = await apiAs(who);
        expect(await api.status('get', `/calendar?userId=${idOf('alice')}&from=${iso(at(2, 0))}&to=${iso(at(3, 0))}`)).toBe(403);
        // Scheduling still finds her busy, and not what for.
        const avail = await api.get(`/calendar/availability?userIds=${idOf('alice')}&from=${iso(at(2, 0))}&to=${iso(at(3, 0))}`);
        expect(JSON.stringify(avail)).toContain('"busy"');
        expect(JSON.stringify(avail)).not.toMatch(/Secret|Confidential|title/);
        await api.dispose();
      }
    }
    // Not even an admin gets titles from the availability read used for scheduling.
    const avail = await olivia.get(`/calendar/availability?userIds=${idOf('alice')}&from=${iso(at(2, 0))}&to=${iso(at(3, 0))}`);
    expect(JSON.stringify(avail)).not.toMatch(/Secret|Confidential|title/);
    await Promise.all([aliceApi.dispose(), olivia.dispose()]);
  });

  test('scheduling over somebody warns, and the organizer goes ahead anyway', async ({ as }) => {
    const aliceApi = await apiAs('alice');
    await aliceApi.post('/calendar/blocks', { kind: 'focus', title: 'Deep work', startsAt: iso(at(4, 10)), endsAt: iso(at(4, 12)) });
    const mia = await as('mia');
    await mia.page.goto(calendarUrl(at(4, 0)));
    await mia.page.getByRole('button', { name: 'Schedule', exact: true }).click();
    const dialog = mia.page.getByRole('dialog');
    await dialog.getByLabel('Name', { exact: true }).fill('Check in');
    await fillMeetingTime(mia.page, at(4, 10, 30), '1 hour');
    await dialog.getByLabel('People', { exact: true }).fill('Pat');
    await dialog.getByRole('button', { name: NAME('pat') }).click();
    await expect(dialog.getByRole('status').filter({ hasText: 'You can still go ahead' })).toHaveCount(0);
    await expect(dialog.getByRole('button', { name: 'Schedule', exact: true })).toBeVisible();
    await dialog.getByLabel('People', { exact: true }).fill('Alice');
    await dialog.getByRole('button', { name: NAME('alice') }).click();
    const warning = dialog.getByRole('status').filter({ hasText: 'You can still go ahead' });
    await expect(warning).toContainText(`${NAME('alice')}: focus time`);
    // Pat is free: the warning names nobody else.
    await expect(warning).not.toContainText(NAME('pat'));
    await dialog.getByRole('button', { name: 'Schedule anyway' }).click();
    await expect(mia.page.getByText(`Heads up: ${NAME('alice')} has something else then.`, { exact: true })).toBeVisible();
    await expect(eventButton(mia.page, 'Check in')).toBeVisible();
    const mine = await apiAs('alice');
    const items = (await mine.get(`/calendar?from=${iso(at(4, 0))}&to=${iso(at(5, 0))}`)).items;
    expect(items.some((i: any) => i.title === 'Check in' && i.source === 'meeting')).toBe(true);
    await Promise.all([mine.dispose(), aliceApi.dispose()]);
  });

  test('moving and cancelling tell the invitees and not the organizer; an invitee cannot move it', async ({ as }) => {
    const olivia = await as('owner');
    const alice = await as('alice');
    const day = at(3, 0);
    const ownerApi = await apiAs('owner');
    const aliceApi = await apiAs('alice');
    const patApi = await apiAs('pat');
    const created = await ownerApi.post('/meetings', {
      title: 'Offsite planning',
      startsAt: iso(at(3, 10)),
      endsAt: iso(at(3, 11)),
      inviteeIds: [idOf('alice'), idOf('pat')],
    });
    const id = created.meeting.id;
    const notificationsOf = async (api: typeof aliceApi, type: string) =>
      (await api.get('/notifications?limit=50')).notifications.filter((n: any) => n.type === type && JSON.stringify(n).includes('Offsite planning'));

    await alice.page.goto(calendarUrl(day));
    await expect(eventButton(alice.page, 'Offsite planning')).toBeVisible();
    await eventButton(alice.page, 'Offsite planning').click();
    await expect(alice.page.getByRole('dialog').getByText('Offsite planning')).toBeVisible();
    // The invitee has no way to move or cancel it in the page, and the server agrees.
    await expect(alice.page.getByRole('button', { name: 'Edit' })).toHaveCount(0);
    await expect(alice.page.getByRole('button', { name: 'Cancel meeting' })).toHaveCount(0);
    expect(await aliceApi.status('patch', `/meetings/${id}`, { startsAt: iso(at(3, 12)), endsAt: iso(at(3, 13)) })).toBe(403);
    expect(await aliceApi.status('delete', `/meetings/${id}`)).toBe(403);
    await alice.page.keyboard.press('Escape');

    // The organizer moves it through the dialog.
    await olivia.page.goto(calendarUrl(day));
    await eventButton(olivia.page, 'Offsite planning').click();
    await olivia.page.getByRole('button', { name: 'Edit' }).click();
    const dialog = olivia.page.getByRole('dialog');
    await fillMeetingTime(olivia.page, at(3, 15), '1 hour');
    await dialog.getByRole('button', { name: 'Save' }).click();
    // Alice's open calendar moves it with no reload.
    await expect(alice.page.getByRole('main').getByRole('button', { name: /Offsite planning, 03:00 PM to 04:00 PM/ })).toBeVisible({ timeout: 15_000 });
    await expect.poll(async () => (await notificationsOf(aliceApi, 'meeting.moved')).length).toBe(1);
    expect((await notificationsOf(patApi, 'meeting.moved')).length).toBe(1);
    expect((await notificationsOf(ownerApi, 'meeting.moved')).length, 'the organizer is not told about their own change').toBe(0);
    // A bell notification, in words.
    await alice.page.getByRole('button', { name: /Notifications/ }).click();
    await expect(alice.page.getByRole('menu').getByText('Offsite planning moved').first()).toBeVisible();
    await alice.page.keyboard.press('Escape');

    // Cancel.
    await eventButton(olivia.page, 'Offsite planning').click();
    await olivia.page.getByRole('button', { name: 'Cancel meeting' }).click();
    await olivia.page.getByRole('dialog').getByRole('button', { name: 'Cancel meeting' }).last().click();
    await expect(eventButton(olivia.page, 'Offsite planning')).toHaveCount(0);
    await expect(eventButton(alice.page, 'Offsite planning')).toHaveCount(0, { timeout: 15_000 });
    await expect.poll(async () => (await notificationsOf(aliceApi, 'meeting.cancelled')).length).toBe(1);
    expect((await notificationsOf(ownerApi, 'meeting.cancelled')).length).toBe(0);
    await Promise.all([ownerApi.dispose(), aliceApi.dispose(), patApi.dispose()]);
  });

  test('a call somebody is added to shows on their calendar, and a past call shows the times people were really in it', async ({ as }) => {
    const olivia = await as('owner');
    const alice = await as('alice');
    const ownerApi = await apiAs('owner');
    const call = await createCall(ownerApi, 'Quick sync', [idOf('alice'), idOf('pat')]);
    await joinCall(olivia.page, call.id);
    // Alice is rung, and answers.
    await alice.page.getByRole('button', { name: 'Answer' }).click({ timeout: 20_000 });
    await expect(alice.page.getByRole('button', { name: 'Leave the call' })).toBeVisible({ timeout: 30_000 });
    await alice.page.waitForTimeout(4000);
    await leaveCall(alice.page);
    await olivia.page.waitForTimeout(1500);
    await leaveCall(olivia.page);
    const log = await waitForLog(ownerApi, call.id, 'both sessions closed', (l) => l.sessions.length === 2 && l.sessions.every((s) => s.leftAt));
    const hers = log.sessions.find((s) => s.userId === idOf('alice'))!;

    // Alice was in it for these real minutes. Pat never connected, so sees the call's own times.
    const aliceApi = await apiAs('alice');
    const patApi = await apiAs('pat');
    const from = iso(at(0, 0));
    const to = iso(at(1, 0));
    const aliceItem = (await aliceApi.get(`/calendar?from=${from}&to=${to}`)).items.find((i: any) => i.callId === call.id);
    expect(aliceItem, 'the call is on Alice\'s calendar').toBeTruthy();
    expect(aliceItem.title).toBe('Quick sync');
    expect(Math.abs(new Date(aliceItem.startsAt).getTime() - new Date(hers.joinedAt!).getTime())).toBeLessThan(2000);
    expect(Math.abs(new Date(aliceItem.endsAt).getTime() - new Date(hers.leftAt!).getTime())).toBeLessThan(2000);
    const patItem = (await patApi.get(`/calendar?from=${from}&to=${to}`)).items.find((i: any) => i.callId === call.id);
    expect(patItem, 'the call is on Pat\'s calendar although Pat never connected').toBeTruthy();
    expect(patItem.source).toBe('call');

    // And the page says the same times.
    const clock = (iso: string) => new Date(iso).toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit' });
    await alice.page.goto(calendarUrl(at(0, 0)));
    const real = new RegExp(`Quick sync.*${clock(hers.joinedAt!)} to ${clock(hers.leftAt!)}`);
    await expect(eventButton(alice.page, /Attendance check|Quick sync|more at this time/).first()).toBeVisible();
    // Every call of this run piles onto the same minutes, so it may sit behind a "+N" chip.
    const direct = eventButton(alice.page, real);
    if ((await direct.count()) === 0) {
      let found = false;
      for (const chip of await alice.page.getByRole('main').getByRole('button', { name: /more at this time/ }).all()) {
        await chip.click();
        found = (await alice.page.getByRole('dialog').getByRole('button', { name: real }).count()) > 0;
        if (found) break;
        await alice.page.keyboard.press('Escape');
      }
      expect(found, 'the call at its real times, directly or in a pile').toBe(true);
    } else {
      await expect(direct).toBeVisible();
    }
    await Promise.all([ownerApi.dispose(), aliceApi.dispose(), patApi.dispose()]);
  });
});

/** HH:MM this many minutes from now, kept inside today so the scrum belongs to today. */
function timeOfDay(offsetMinutes: number): string {
  const d = new Date(Date.now() + offsetMinutes * 60_000);
  const today = new Date();
  const minutes = d.toDateString() === today.toDateString() ? d.getHours() * 60 + d.getMinutes() : offsetMinutes < 0 ? 1 : 23 * 60 + 59;
  const m = Math.max(1, Math.min(minutes, 23 * 60 + 59));
  return `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`;
}

async function createStandingInUi(page: Page, name: string, time: string, length = '30') {
  await page.goto('/calls');
  await page.getByRole('button', { name: 'New', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Name', { exact: true }).fill(name);
  await dialog.getByLabel('Time', { exact: true }).fill(time);
  await dialog.getByLabel('Length in minutes').fill(length);
  // Every day, so the room runs whatever day the suite runs on.
  const off = dialog.getByRole('group', { name: 'Days' }).getByRole('button', { pressed: false });
  while ((await off.count()) > 0) await off.first().click();
  await dialog.getByRole('button', { name: 'Create' }).click();
  await expect(page.getByRole('heading', { name })).toBeVisible();
}

const standingId = async (api: Awaited<ReturnType<typeof apiAs>>, name: string): Promise<string> =>
  (await api.get('/standing-meetings')).standingMeetings.find((m: any) => m.name === name).id;

const attendanceRow = (page: Page, name: string) => page.getByRole('table').first().getByRole('row', { name: new RegExp(name) });

async function joinStandingByCard(page: Page, name: string) {
  await page.goto('/calls');
  await page.getByRole('button', { name: `Join ${name}` }).click();
  await expect(page.getByRole('button', { name: 'Leave the call' })).toBeVisible({ timeout: 30_000 });
}

test.describe('7. the standing meeting', () => {
  const MORNING = 'Morning scrum';
  const EVENING = 'Evening scrum';
  const SILENT = 'Silent scrum';

  // A standing meeting recurs every day of the week and shows on everybody's
  // calendar. Left behind, these filled the hours around the time the suite ran
  // and folded later tests' own events away into "+3" (team calendars).
  test.afterAll(async () => {
    const owner = await apiAs('owner');
    const rooms = (await owner.get('/standing-meetings')).standingMeetings as { id: string; name: string }[];
    for (const room of rooms.filter((r) => [MORNING, EVENING, SILENT].includes(r.name))) {
      await owner.del(`/standing-meetings/${room.id}`);
    }
    await owner.dispose();
  });

  test('an admin makes the morning and evening rooms; an employee and a manager cannot manage them', async ({ as }) => {
    const olivia = await as('owner');
    await createStandingInUi(olivia.page, MORNING, timeOfDay(-30));
    await createStandingInUi(olivia.page, EVENING, timeOfDay(20), '15');
    await expect(olivia.page.getByText('15 min')).toBeVisible();
    await expect(olivia.page.getByRole('button', { name: `Edit ${MORNING}` })).toBeVisible();

    const alice = await as('alice');
    await alice.page.goto('/calls');
    await expect(alice.page.getByRole('heading', { name: MORNING })).toBeVisible();
    await expect(alice.page.getByRole('heading', { name: EVENING })).toBeVisible();
    await expect(alice.page.getByRole('button', { name: 'New', exact: true })).toHaveCount(0);
    await expect(alice.page.getByRole('button', { name: /^(Edit|Archive) / })).toHaveCount(0);

    const owner = await apiAs('owner');
    const id = await standingId(owner, MORNING);
    for (const who of ['alice', 'mia'] as const) {
      const api = await apiAs(who);
      expect(await api.status('post', '/standing-meetings', { name: 'Sneaky', timeOfDay: '09:00', timeZone: 'UTC', weekdays: [1] }), `${who} creating`).toBe(403);
      expect(await api.status('patch', `/standing-meetings/${id}`, { name: 'Renamed' }), `${who} editing`).toBe(403);
      expect(await api.status('delete', `/standing-meetings/${id}`), `${who} archiving`).toBe(403);
      await api.dispose();
    }
    // They are on everybody's calendar for today as busy, and a meeting conflict.
    const items = (await (await apiAs('alice')).get(`/calendar?from=${iso(at(0, 0))}&to=${iso(at(1, 0))}`)).items;
    expect(items.filter((i: any) => i.source === 'standing').length).toBe(2);
    await owner.dispose();
  });

  test('everybody joins without knocking; rejoining the same day is the same record; attendance by day shows present, late and absent', async ({ as }) => {
    test.setTimeout(180_000);
    const owner = await apiAs('owner');
    const id = await standingId(owner, MORNING);
    const eveningId = await standingId(owner, EVENING);
    // People who were in the organization before today, so the days before count them as absent.
    sql(`UPDATE users SET created_at = now() - interval '10 days' WHERE org_id = (SELECT org_id FROM users WHERE email = '${PEOPLE.owner.email}')`);
    sql(`UPDATE standing_meetings SET created_at = now() - interval '3 days'`);

    const alice = await as('alice');
    const sam = await as('sam');
    const max = await as('max');
    await joinStandingByCard(alice.page, MORNING);
    await alice.page.waitForTimeout(2500); // arrivals in the first two seconds are the room you walked into
    await joinStandingByCard(sam.page, MORNING);
    await sam.page.waitForTimeout(2500);
    const toast = expect(sam.page.getByText(`${NAME('max')} joined the call`, { exact: true })).toBeVisible();
    await joinStandingByCard(max.page, MORNING);
    for (const s of [alice, sam, max]) await expect(s.page.getByText('This is a private call')).toHaveCount(0);
    await toast;

    const first = await owner.get(`/standing-meetings/${id}/attendance`);
    const callId = first.days.at(-1).callId;
    expect(callId).toBeTruthy();
    await waitForLog(owner, callId, 'three people connected', (l) => l.sessions.filter((s) => !s.leftAt).length === 3);

    // Alice drops out and comes back: still one occurrence, two sessions.
    await leaveCall(alice.page);
    await waitForLog(owner, callId, 'Alice left', (l) => l.sessions.some((s) => s.userId === idOf('alice') && s.leftAt));
    await joinStandingByCard(alice.page, MORNING);
    await waitForLog(owner, callId, 'Alice has two sessions', (l) => l.sessions.filter((s) => s.userId === idOf('alice')).length === 2);
    const today = first.days.at(-1).date;
    expect(sql(`SELECT count(*) FROM calls WHERE standing_meeting_id = '${id}' AND occurrence_date = '${today}'`)).toBe('1');
    const joinAgain = await (await apiAs('alice')).post(`/standing-meetings/${id}/join`);
    expect(joinAgain.callId, 'the same record').toBe(callId);

    // Today, as the admin sees it: Alice and Sam and Max came, 30 minutes after the start; everybody else is absent.
    const olivia = await as('owner');
    await olivia.page.goto(`/calls/standing/${id}/attendance`);
    const section = olivia.page.getByRole('region', { name: `Attendance on ${today}` });
    await expect(section).toBeVisible();
    await expect(attendanceRow(olivia.page, NAME('alice'))).toContainText('Present');
    await expect(attendanceRow(olivia.page, NAME('sam'))).toContainText('Present');
    await expect(attendanceRow(olivia.page, NAME('ann'))).toContainText('Absent');
    await expect(attendanceRow(olivia.page, NAME('pat'))).toContainText('Absent');
    const late = Number(await attendanceRow(olivia.page, NAME('alice')).getByRole('cell').last().innerText());
    expect(late, 'minutes late, scheduled 30 minutes ago').toBeGreaterThanOrEqual(25);
    expect(late).toBeLessThanOrEqual(40);
    await expect(attendanceRow(olivia.page, NAME('ann')).getByRole('cell').last()).toHaveText('-');
    const day = (await owner.get(`/standing-meetings/${id}/attendance`)).days.at(-1);
    const row = (name: string) => day.people.find((p: any) => p.name === name);
    expect(row(NAME('alice'))).toMatchObject({ status: 'present' });
    expect(row(NAME('alice')).minutesLate).toBeGreaterThanOrEqual(25);
    expect(row(NAME('alice')).minutesPresent).toBeGreaterThanOrEqual(0);
    expect(row(NAME('alice')).firstJoinAt).toBeTruthy();
    expect(row(NAME('ann'))).toMatchObject({ status: 'absent' });

    // Yesterday: nobody pressed Join, so it is reliably absent for everybody (the days before were due too).
    const range = await owner.get(`/standing-meetings/${id}/attendance?from=${dayParam(at(-2, 0))}&to=${today}`);
    const yesterday = range.days.find((d: any) => d.date === dayParam(at(-1, 0)));
    expect(yesterday, 'a past day that was due').toBeTruthy();
    expect(yesterday.people.length).toBeGreaterThan(5);
    expect(yesterday.people.every((p: any) => p.status === 'absent')).toBe(true);

    // The evening room, moved to yesterday as if it had run then (time cannot be bent in a browser): on time, present.
    await joinStandingByCard(alice.page, EVENING);
    const eveningCall = (await owner.get(`/standing-meetings/${eveningId}/attendance`)).days.at(-1).callId;
    await waitForLog(owner, eveningCall, 'Alice connected to the evening room', (l) => l.sessions.length === 1);
    await leaveCall(alice.page);
    await waitForLog(owner, eveningCall, 'Alice left the evening room', (l) => l.sessions.every((s) => s.leftAt));
    const shift = (table: string, cols: string[], where: string) =>
      sql(`UPDATE ${table} SET ${cols.map((c) => `${c} = ${c} - interval '1 day'`).join(', ')} WHERE ${where}`);
    shift('calls', ['occurrence_date', 'scheduled_start', 'scheduled_end', 'started_at', 'ended_at'], `id = '${eveningCall}'`);
    shift('call_sessions', ['joined_at', 'left_at'], `call_id = '${eveningCall}'`);
    shift('call_events', ['created_at'], `call_id = '${eveningCall}'`);
    const evening = await owner.get(`/standing-meetings/${eveningId}/attendance?from=${dayParam(at(-1, 0))}&to=${today}`);
    const eveningYesterday = evening.days.find((d: any) => d.date === dayParam(at(-1, 0)))!;
    expect(eveningYesterday.people.find((p: any) => p.name === NAME('alice'))).toMatchObject({ status: 'present', minutesLate: 0 });
    expect(eveningYesterday.people.find((p: any) => p.name === NAME('ann'))).toMatchObject({ status: 'absent' });
    await olivia.page.goto(`/calls/standing/${eveningId}/attendance?from=${dayParam(at(-1, 0))}&to=${today}`);
    await expect(olivia.page.getByRole('region', { name: `Attendance on ${eveningYesterday.date}` })).toBeVisible();
    await expect(attendanceRow(olivia.page, NAME('alice'))).toContainText('Present');
    await expect(attendanceRow(olivia.page, NAME('alice')).getByRole('cell').last()).toHaveText('0');

    await owner.dispose();
  });

  test('who sees whose attendance: admin everybody, manager their people, employee only themself', async ({ as }) => {
    const owner = await apiAs('owner');
    const id = await standingId(owner, MORNING);
    const names = async (who: keyof typeof PEOPLE, query = '') => {
      const api = await apiAs(who);
      const res = await api.get(`/standing-meetings/${id}/attendance${query}`);
      await api.dispose();
      return res.days.at(-1).people.map((p: any) => p.name).sort();
    };
    const everyone = await names('owner');
    for (const n of [NAME('alice'), NAME('sam'), NAME('mia'), NAME('max'), NAME('ann'), NAME('pat')]) expect(everyone).toContain(n);
    expect(everyone.length).toBeGreaterThanOrEqual(7);
    // Mia manages Backend and Platform, and sees herself; nobody from Sales, and not the owner.
    expect(await names('mia')).toEqual([NAME('alice'), NAME('ann'), NAME('mia'), NAME('pat')].sort());
    expect(await names('alice')).toEqual([NAME('alice')]);
    expect(await names('sam')).toEqual([NAME('sam')]);
    // An employee cannot widen it with a team, and a manager cannot ask for a team that is not theirs.
    const teams = readState().teams;
    expect(await names('alice', `?teamId=${teams.sales}`)).toEqual([NAME('alice')]);
    const mia = await apiAs('mia');
    expect(await mia.status('get', `/standing-meetings/${id}/attendance?teamId=${teams.sales}`)).toBe(404);
    // The admin narrows to one team.
    const sales = await names('owner', `?teamId=${teams.sales}`);
    expect(sales).toContain(NAME('sam'));
    expect(sales).not.toContain(NAME('alice'));
    await mia.dispose();

    // The same through the pages.
    const miaSession = await as('mia');
    await miaSession.page.goto(`/calls/standing/${id}/attendance`);
    await expect(attendanceRow(miaSession.page, NAME('alice'))).toContainText('Present');
    await expect(attendanceRow(miaSession.page, NAME('ann'))).toContainText('Absent');
    await expect(miaSession.page.getByRole('cell', { name: NAME('sam') })).toHaveCount(0);
    const aliceSession = await as('alice');
    await aliceSession.page.goto(`/calls/standing/${id}/attendance`);
    await expect(attendanceRow(aliceSession.page, NAME('alice'))).toContainText('Present');
    await expect(aliceSession.page.getByRole('cell', { name: NAME('ann') })).toHaveCount(0);
    await expect(aliceSession.page.getByRole('cell', { name: NAME('sam') })).toHaveCount(0);
    // An employee is not offered a log that would only refuse them.
    await expect(aliceSession.page.getByRole('button', { name: /Show the call log/ })).toHaveCount(0);
    // A standing meeting has no members, so being in the room never opened the log (decision 27).
    const alice = await apiAs('alice');
    const callId = (await owner.get(`/standing-meetings/${id}/attendance`)).days.at(-1).callId;
    expect(await alice.status('get', `/calls/${callId}/log`)).toBe(404);
    await alice.dispose();
    await owner.dispose();
  });

  test('a day where people pressed Join and no connection was ever reported is unknown, not absent; archiving keeps the history', async ({ as }) => {
    const olivia = await as('owner');
    await createStandingInUi(olivia.page, SILENT, timeOfDay(-5));
    const owner = await apiAs('owner');
    const id = await standingId(owner, SILENT);
    // A token is only permission: Ann asks for it and never connects, as when webhooks are down.
    const ann = await apiAs('ann');
    const joined = await ann.post(`/standing-meetings/${id}/join`);
    expect(joined.token).toBeTruthy();
    const day = (await owner.get(`/standing-meetings/${id}/attendance`)).days.at(-1);
    expect(day.attendanceRecorded).toBe(false);
    expect(day.people.every((p: any) => p.status === 'unknown')).toBe(true);
    await olivia.page.goto(`/calls/standing/${id}/attendance`);
    await expect(olivia.page.getByRole('note').filter({ hasText: 'connection reports never arrived' })).toBeVisible();
    await expect(attendanceRow(olivia.page, NAME('alice'))).toContainText('Unknown');
    await olivia.page.getByRole('button', { name: /Show the call log/ }).first().click();
    await expect(olivia.page.getByRole('dialog').getByText('No attendance data for this call.')).toBeVisible();
    await olivia.page.keyboard.press('Escape');

    await olivia.page.goto('/calls');
    await olivia.page.getByRole('button', { name: `Archive ${SILENT}` }).click();
    await olivia.page.getByRole('dialog').getByRole('button', { name: 'Archive', exact: true }).click();
    await expect(olivia.page.getByRole('heading', { name: SILENT })).toHaveCount(0);
    expect(await (await apiAs('alice')).status('post', `/standing-meetings/${id}/join`)).toBeGreaterThanOrEqual(404);
    // The history is kept.
    expect(await owner.status('get', `/standing-meetings/${id}/attendance`)).toBe(200);
    await Promise.all([ann.dispose(), owner.dispose()]);
  });
});

/** One person's card on the scrum tab (or in a saved scrum). */
const scrumCard = (page: Page, name: string) =>
  page.locator('section', { has: page.getByRole('heading', { level: 2, name, exact: true }) });

async function taskIdByTitle(title: string): Promise<string> {
  const api = await apiAs('owner');
  const res = (await api.get('/tasks')) as any;
  await api.dispose();
  return (res.tasks ?? res.data ?? res).find((t: any) => t.title === title).id;
}

async function addToTaskInUi(page: Page, title: string, who: string) {
  await page.goto(`/tasks/${await taskIdByTitle(title)}`);
  await expect(page.getByRole('heading', { name: title })).toBeVisible();
  await page.getByRole('button', { name: 'Add', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByRole('checkbox', { name: new RegExp(who) }).check();
  await dialog.getByRole('button', { name: 'Add', exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByRole('button', { name: `Remove ${who}` })).toBeVisible();
}

async function contributionOf(api: Awaited<ReturnType<typeof apiAs>>, userId: string) {
  const res = await api.get('/tasks/contributions');
  return (res.people as any[]).find((p) => p.userId === userId);
}

test.describe('8. tasks you were added to', () => {
  // An employee can only put people on tasks that the org chart lets them assign to, so the person adding is a manager.
  const PLAN = 'Launch plan owned by Mia';

  test('Mia adds Ann to her task: Ann sees it under Added to and is told; the manager and the admin see it in every view', async ({ as }) => {
    const ann = await as('ann');
    const mia = await as('mia');
    await openTasks(mia.page);
    await createTaskInUi(mia.page, { title: PLAN, assignee: NAME('mia') });
    await openTasks(ann.page);
    await expect(ann.page.getByRole('heading', { name: /Added to/ })).toHaveCount(0);

    await addToTaskInUi(mia.page, PLAN, NAME('ann'));

    // Ann does nothing: it shows under Added to, apart from her own work, and she is notified.
    await openTasks(ann.page);
    const added = ann.page.getByRole('heading', { name: /Added to/ });
    await expect(added).toBeVisible();
    await expect(ann.page.getByRole('link', { name: PLAN })).toBeVisible();
    await ann.page.getByRole('button', { name: /Notifications/ }).click();
    await expect(ann.page.getByRole('menu').getByText(`You were added to ${PLAN}`).first()).toBeVisible();
    await ann.page.keyboard.press('Escape');
    const annApi = await apiAs('ann');
    const dash = await annApi.get('/tasks/dashboard');
    expect(dash.people[0].counts.addedTo).toBe(1);
    expect(dash.people[0].counts.pending, 'not counted as her own pending work').toBe(0);
    expect(dash.totals.addedTo).toBe(1);

    // Her manager sees it on By person, Scrum and Contribution.
    await openTasks(mia.page, `?tab=person&person=${idOf('ann')}`);
    await expect(mia.page.getByRole('heading', { name: /Added to/ })).toBeVisible();
    await expect(mia.page.getByRole('link', { name: PLAN })).toBeVisible();
    await openTasks(mia.page, '?tab=scrum');
    await expect(scrumCard(mia.page, NAME('ann'))).toContainText(PLAN);
    await expect(scrumCard(mia.page, NAME('ann'))).toContainText(/Added to/);
    await openTasks(mia.page, '?tab=contribution');
    const row = mia.page.getByRole('row', { name: new RegExp(`^${NAME('ann')}`) });
    await expect(row).toBeVisible();
    const miaApi = await apiAs('mia');
    expect((await contributionOf(miaApi, idOf('ann'))).addedTo).toBe(1);
    await mia.page.goto(`/tasks?tab=contribution&person=${idOf('ann')}`);
    const detail = mia.page.getByRole('dialog', { name: NAME('ann') });
    await expect(detail).toBeVisible();
    await expect(detail.getByText('Added to', { exact: false }).first()).toBeVisible();
    await expect(detail.getByText(PLAN).first()).toBeVisible();

    // A saved scrum records it, and does not change afterwards.
    await openTasks(mia.page, '?tab=scrum');
    await mia.page.getByRole('button', { name: 'Complete scrum' }).first().click();
    const dialog = mia.page.getByRole('dialog');
    await dialog.getByRole('button', { name: 'Complete scrum' }).click();
    await expect(mia.page.getByText('Scrum saved', { exact: true })).toBeVisible();
    const scrums = (await miaApi.get('/scrums')).scrums as any[];
    const saved = await miaApi.get(`/scrums/${scrums[0].id}`);
    const annInScrum = saved.participants.find((p: any) => p.userId === idOf('ann'));
    expect(annInScrum.counts.addedTo).toBe(1);
    expect(saved.entries.some((e: any) => e.userId === idOf('ann') && e.addedTo === true && e.title === PLAN)).toBe(true);

    // Mia finishes the task. It is her completion alone: Ann is not credited as completing it.
    const ownerApi = await apiAs('owner');
    const tasks = (await miaApi.get('/tasks')) as any;
    const task = (tasks.tasks ?? tasks.data ?? tasks).find((t: any) => t.title === PLAN);
    const before = { mia: (await contributionOf(ownerApi, idOf('mia'))).completed, ann: (await contributionOf(ownerApi, idOf('ann'))).completed };
    await miaApi.patch(`/tasks/${task.id}`, { status: 'done' });
    const after = { mia: await contributionOf(ownerApi, idOf('mia')), ann: await contributionOf(ownerApi, idOf('ann')) };
    expect(after.mia.completed).toBe(before.mia + 1);
    expect(after.ann.completed, 'not credited twice').toBe(before.ann);
    expect(after.ann.addedTo, 'still a contribution in the range').toBe(1);
    // No longer open, so no longer under Added to on the dashboard.
    expect((await annApi.get('/tasks/dashboard')).people[0].counts.addedTo).toBe(0);
    // And the saved scrum is as it was.
    const again = await miaApi.get(`/scrums/${scrums[0].id}`);
    expect(again.participants.find((p: any) => p.userId === idOf('ann')).counts.addedTo).toBe(1);
    await Promise.all([annApi.dispose(), miaApi.dispose(), ownerApi.dispose()]);
  });

  test('taking somebody off the task takes it away from them, and adding them twice tells them once', async ({ as }) => {
    const mia = await as('mia');
    const ann = await as('ann');
    const title = 'Quarterly report owned by Mia';
    await openTasks(mia.page);
    await createTaskInUi(mia.page, { title, assignee: NAME('mia') });
    await addToTaskInUi(mia.page, title, NAME('ann'));

    const miaApi = await apiAs('mia');
    const annApi = await apiAs('ann');
    const tasks = (await miaApi.get('/tasks')) as any;
    const task = (tasks.tasks ?? tasks.data ?? tasks).find((t: any) => t.title === title);
    await miaApi.post(`/tasks/${task.id}/watchers`, { userId: idOf('ann') });
    const told = (await annApi.get('/notifications?limit=50')).notifications.filter((n: any) => n.type === 'task.added' && JSON.stringify(n).includes(title));
    expect(told, 'told once however often she was added').toHaveLength(1);

    await openTasks(ann.page);
    await expect(ann.page.getByRole('link', { name: title })).toBeVisible();

    // The org chart that limits whom an employee may assign to limits whom they may put on a task.
    const aliceApi = await apiAs('alice');
    const own = await aliceApi.post('/tasks', { title: 'Alice own task for peers', assigneeId: idOf('alice') });
    console.log('employee adding a peer to their own task answers', await aliceApi.status('post', `/tasks/${own.id}/watchers`, { userId: idOf('ann') }));
    await aliceApi.dispose();

    await mia.page.getByRole('button', { name: `Remove ${NAME('ann')}` }).click();
    await expect(mia.page.getByRole('button', { name: `Remove ${NAME('ann')}` })).toHaveCount(0);
    await openTasks(ann.page);
    await expect(ann.page.getByRole('link', { name: title })).toHaveCount(0);
    const dash = await annApi.get('/tasks/dashboard');
    expect(dash.people[0].tasks.addedTo.some((t: any) => t.title === title)).toBe(false);
    await Promise.all([miaApi.dispose(), annApi.dispose()]);
  });
});

test.describe('1b. the join toast in a standing room', () => {
  test('somebody coming into the standing room is announced to the people already in it', async ({ as }) => {
    const owner = await apiAs('owner');
    const made = await owner.post('/standing-meetings', { name: 'Toast room', timeOfDay: timeOfDay(-10), timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone, weekdays: [1, 2, 3, 4, 5, 6, 7] });
    const roomId = made.standingMeeting.id;
    const alice = await as('alice');
    const sam = await as('sam');
    await joinStandingByCard(alice.page, 'Toast room');
    await alice.page.waitForTimeout(2500);
    await joinStandingByCard(sam.page, 'Toast room');
    await expect(alice.page.getByText(`${NAME('sam')} joined the call`, { exact: true })).toBeVisible();
    await sam.page.waitForTimeout(2500);
    await expect(sam.page.getByText(/joined the call/)).toHaveCount(0);
    await leaveCall(sam.page);
    await leaveCall(alice.page);
    await owner.del(`/standing-meetings/${roomId}`);
    await owner.dispose();
  });
});

// Red until the defects they name are fixed. Each says what the spec or the page promises and what is seen.
test.describe('10. defects found by this suite, kept fixed', () => {
  /** The centre of the element is the element, or something inside it: nothing is drawn over it. */
  const reachable = (locator: import('@playwright/test').Locator) =>
    locator.evaluate((el) => {
      const r = el.getBoundingClientRect();
      const top = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2);
      return !!top && (el === top || el.contains(top));
    });

  test('defect 30: on a phone the join toast must not cover the knock prompt or the call controls', async ({ as }) => {
    const olivia = await as('owner', { width: 375, height: 812 });
    const pat = await as('pat');
    const sam = await as('sam');
    const api = await apiAs('owner');
    const call = await createCall(api, 'Toast over prompt', [idOf('pat')]);
    await joinCall(olivia.page, call.id);
    await waitForLog(api, call.id, 'Olivia connected', (l) => l.sessions.some((s) => !s.leftAt));
    await olivia.page.waitForTimeout(2500);
    await sam.page.goto(`/calls/${call.id}`);
    await expect(sam.page.getByText('This is a private call')).toBeVisible();

    await joinCall(pat.page, call.id);
    const toast = olivia.page.getByText(`${NAME('pat')} joined the call`, { exact: true });
    await expect(toast).toBeVisible();
    await sam.page.getByRole('button', { name: 'Ask to join' }).click();
    const admit = admitButton(olivia.page, NAME('sam'));
    await expect(admit).toBeVisible();
    // While the toast is up, anybody on a phone has to be able to press Admit and Leave.
    const state = await olivia.page.evaluate(() => ({ toast: !!Array.from(document.querySelectorAll('li, div')).find((e) => e.textContent === 'Pat Platform joined the call') }));
    expect(state.toast, 'the toast is still showing when the prompt appears').toBe(true);
    expect(await reachable(admit), 'Admit is not covered by the join toast').toBe(true);
    for (const name of ['Leave the call', 'Minimise the call', 'Show the call log', 'Turn your microphone on', 'Turn your camera on']) {
      expect(await reachable(olivia.page.getByRole('button', { name })), `${name} is not covered by the join toast`).toBe(true);
    }
    await api.dispose();
  });

  test('defect 30: at a computer the join toast sits above the call controls', async ({ as }) => {
    const olivia = await as('owner', { width: 1280, height: 800 });
    const pat = await as('pat');
    const api = await apiAs('owner');
    const call = await createCall(api, 'Toast at a computer', [idOf('pat')]);
    await joinCall(olivia.page, call.id);
    await waitForLog(api, call.id, 'Olivia connected', (l) => l.sessions.some((s) => !s.leftAt));
    await olivia.page.waitForTimeout(2500);
    await joinCall(pat.page, call.id);
    await expect(olivia.page.getByText(`${NAME('pat')} joined the call`, { exact: true })).toBeVisible();
    for (const name of ['Share your screen', 'Turn your microphone on', 'Turn your camera on', 'Leave the call']) {
      expect(await reachable(olivia.page.getByRole('button', { name })), `${name} is not covered by the join toast`).toBe(true);
    }
    await api.dispose();
  });

  test('defect 30: a toast on a phone sits above the bottom navigation', async ({ as }) => {
    const api = await apiAs('owner');
    await createCall(api, 'Something to copy', []);
    const olivia = await as('owner', { width: 375, height: 812 });
    await olivia.page.goto('/calls');
    await olivia.page.getByRole('button', { name: /^Copy the link to/ }).first().click();
    const toast = olivia.page.getByText(/^(Link copied|Could not copy)/).first();
    await expect(toast).toBeVisible();
    const box = (await toast.boundingBox())!;
    const nav = (await olivia.page.getByRole('navigation').last().boundingBox())!;
    expect(box.y + box.height, 'clear of the bottom navigation').toBeLessThanOrEqual(nav.y);
    await api.dispose();
  });

  test('defect 31: an employee is not offered a call log that can only refuse them', async ({ as }) => {
    const owner = await apiAs('owner');
    const made = await owner.post('/standing-meetings', { name: 'Log button room', timeOfDay: timeOfDay(-5), timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone, weekdays: [1, 2, 3, 4, 5, 6, 7] });
    const alice = await apiAs('alice');
    await alice.post(`/standing-meetings/${made.standingMeeting.id}/join`);
    expect(await alice.status('get', `/calls/${(await alice.post(`/standing-meetings/${made.standingMeeting.id}/join`)).callId}/log`), 'the server never lets her read it').toBe(404);
    const session = await as('alice');
    await session.page.goto(`/calls/standing/${made.standingMeeting.id}/attendance`);
    await expect(session.page.getByRole('heading', { level: 1, name: 'Log button room' })).toBeVisible();
    await expect(session.page.getByRole('region', { name: /^Attendance on / }).first()).toBeVisible();
    await expect(session.page.getByRole('button', { name: /Show the call log/ }), 'a button whose only answer is "You do not have access"').toHaveCount(0);

    // Somebody who can read it is still offered it, and it opens.
    const olivia = await as('owner');
    await olivia.page.goto(`/calls/standing/${made.standingMeeting.id}/attendance`);
    await olivia.page.getByRole('button', { name: /Show the call log/ }).first().click();
    await expect(olivia.page.getByRole('dialog')).toBeVisible();
    await expect(olivia.page.getByText('You do not have access to the log of this call.')).toHaveCount(0);
    await owner.del(`/standing-meetings/${made.standingMeeting.id}`);
    await Promise.all([owner.dispose(), alice.dispose()]);
  });
});
