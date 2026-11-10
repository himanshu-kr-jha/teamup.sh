import { createServer, type IncomingMessage, type Server } from 'node:http';
import { expect, test } from './support/fixtures';
import { apiAs, idOf, iso } from './support/meetings';
import { PEOPLE } from './support/people';

const PORT = Number(process.env.E2E_GOOGLE_PORT ?? 18091);

type GoogleEvent = Record<string, any> & { id: string };

/**
 * Google, as far as TeamUp can tell: the consent page (which agrees at once),
 * the token endpoint, one primary calendar and the revoke endpoint. The
 * server under test is pointed here by TEAMUP_GCAL_ENDPOINT (run.sh).
 */
async function fakeGoogle() {
  const events = new Map<string, GoogleEvent>();
  const revoked: string[] = [];
  let made = 0;
  const read = (req: IncomingMessage) =>
    new Promise<string>((done) => {
      let body = '';
      req.on('data', (c) => (body += c));
      req.on('end', () => done(body));
    });
  const idToken = (email: string) =>
    [{ alg: 'none' }, { email }].map((p) => Buffer.from(JSON.stringify(p)).toString('base64url')).join('.') + '.sig';

  const server: Server = createServer(async (req, res) => {
    const url = new URL(req.url ?? '/', `http://127.0.0.1:${PORT}`);
    const body = await read(req);
    const json = (code: number, value: unknown) => res.writeHead(code, { 'Content-Type': 'application/json' }).end(JSON.stringify(value));
    if (url.pathname === '/o/oauth2/v2/auth') {
      const back = new URL(url.searchParams.get('redirect_uri')!);
      back.searchParams.set('code', 'consented');
      back.searchParams.set('state', url.searchParams.get('state')!);
      return res.writeHead(302, { Location: back.toString() }).end();
    }
    if (url.pathname === '/token') {
      return json(200, {
        access_token: `access-${Date.now()}`,
        token_type: 'Bearer',
        expires_in: 3600,
        refresh_token: 'refresh-e2e',
        id_token: idToken('olivia@gmail.example'),
      });
    }
    if (url.pathname === '/revoke') {
      revoked.push(new URLSearchParams(body).get('token') ?? '');
      return res.writeHead(200).end();
    }
    const base = '/calendar/v3/calendars/primary/events';
    if (url.pathname === base && req.method === 'GET') return json(200, { items: [...events.values()] });
    if (url.pathname === base && req.method === 'POST') {
      const e = { ...JSON.parse(body), id: `made${++made}` };
      events.set(e.id, e);
      return json(200, e);
    }
    if (url.pathname.startsWith(`${base}/`)) {
      const id = decodeURIComponent(url.pathname.slice(base.length + 1));
      const e = events.get(id);
      if (!e) return json(404, {});
      if (req.method === 'PATCH') {
        Object.assign(e, JSON.parse(body));
        return json(200, e);
      }
      if (req.method === 'DELETE') {
        events.delete(id);
        return res.writeHead(204).end();
      }
    }
    res.writeHead(404).end();
  });
  await new Promise<void>((done) => server.listen(PORT, '127.0.0.1', done));
  return {
    events,
    revoked,
    ours: (callId: string) => [...events.values()].find((e) => e.extendedProperties?.private?.teamupCallId === callId),
    close: () => new Promise((done) => server.close(done)),
  };
}

const at = (hoursFromNow: number) => {
  const d = new Date(Date.now() + hoursFromNow * 3_600_000);
  d.setMinutes(0, 0, 0);
  return d;
};

// Reported: meetings people have in Google Calendar were not on TeamUp, and
// TeamUp's were not in Google Calendar. Each person now connects theirs.
test.describe('Google Calendar', () => {
  test('connected once, Google meetings show as busy time and TeamUp meetings go into Google', async ({ as }) => {
    test.setTimeout(120_000);
    const google = await fakeGoogle();
    const owner = await apiAs('owner');
    let meetingId: string | undefined;
    try {
      // Early on a day ahead: around now, the shared accounts' other meetings fold it into "+N".
      const start = new Date();
      start.setDate(start.getDate() + 3);
      start.setHours(6, 0, 0, 0);
      const day = `${start.getFullYear()}-${String(start.getMonth() + 1).padStart(2, '0')}-${String(start.getDate()).padStart(2, '0')}`;
      google.events.set('acme', {
        id: 'acme',
        summary: 'Client call with ACME',
        start: { dateTime: start.toISOString() },
        end: { dateTime: new Date(start.getTime() + 3_600_000).toISOString() },
        htmlLink: 'https://calendar.google.com/calendar/event?eid=acme',
      });

      const olivia = await as('owner');
      const page = olivia.page;
      await page.goto('/calendar');
      await page.getByRole('button', { name: 'Connect Google Calendar' }).click();
      // Google's page, and straight back.
      await expect(page).toHaveURL(/\/calendar/);
      await expect(page.getByText('Google Calendar connected.', { exact: false }).first()).toBeVisible();
      await expect(page).not.toHaveURL(/google=/);

      // In TeamUp, as busy time.
      const settings = page.getByRole('button', { name: 'Google Calendar settings' });
      await settings.click();
      const dialog = page.getByRole('dialog', { name: 'Google Calendar' });
      await expect(dialog.getByText('Connected as olivia@gmail.example.')).toBeVisible();
      await page.screenshot({ path: 'test-results/gcal-settings.png' });
      await dialog.getByRole('button', { name: 'Sync now' }).click();
      await expect(page.getByText('Up to date with Google Calendar.').first()).toBeVisible();
      await dialog.getByRole('button', { name: 'Close' }).click();
      await page.goto(`/calendar?view=day&date=${day}`);
      const acme = page.getByRole('button', { name: /^Client call with ACME/ });
      await expect(acme).toBeVisible();
      await page.screenshot({ path: 'test-results/gcal-day.png' });
      await acme.click();
      const event = page.getByRole('dialog', { name: 'Client call with ACME' });
      await expect(event.getByText('From Google Calendar')).toBeVisible();
      await expect(event.getByRole('link', { name: 'Open in Google Calendar' })).toHaveAttribute('href', /calendar\.google\.com/);
      await page.keyboard.press('Escape');

      // A colleague booking over it is warned.
      const alice = await apiAs('alice');
      const clash = await alice.post('/meetings', {
        title: 'Clashes with ACME',
        startsAt: iso(start),
        endsAt: iso(new Date(start.getTime() + 1_800_000)),
        inviteeIds: [idOf('owner')],
      });
      expect(clash.conflicts).toEqual([expect.objectContaining({ userId: idOf('owner'), kind: 'meeting' })]);
      await alice.del(`/meetings/${clash.meeting.id}`);
      await alice.dispose();

      // A TeamUp meeting goes into her Google Calendar without her doing anything, and leaves with it.
      const created = await owner.post('/meetings', {
        title: 'Roadmap review',
        startsAt: iso(at(3)),
        endsAt: iso(at(4)),
        inviteeIds: [idOf('alice')],
      });
      meetingId = created.meeting.id;
      await expect.poll(() => google.ours(meetingId!)?.summary, { message: 'the meeting reached Google Calendar', timeout: 15_000 }).toBe('Roadmap review');
      expect(google.ours(meetingId!)?.description).toContain(`/calls/${meetingId}`);
      await owner.del(`/meetings/${meetingId}`);
      const cancelled = meetingId;
      meetingId = undefined;
      await expect.poll(() => google.ours(cancelled!), { message: 'the cancelled meeting left Google Calendar', timeout: 15_000 }).toBeUndefined();

      // Disconnecting takes it all away again.
      await settings.click();
      await dialog.getByRole('button', { name: 'Disconnect' }).click();
      await dialog.getByRole('button', { name: 'Disconnect' }).click();
      await expect(page.getByText('Google Calendar disconnected.').first()).toBeVisible();
      await expect(page.getByRole('button', { name: 'Connect Google Calendar' })).toBeVisible();
      await expect(acme).toHaveCount(0);
      expect(google.revoked).toContain('refresh-e2e');
    } finally {
      if (meetingId) await owner.del(`/meetings/${meetingId}`).catch(() => {});
      await owner.call('delete', '/calendar/google').catch(() => {});
      await owner.dispose();
      await google.close();
    }
  });

  test('saying no on Google’s page leaves things as they were, and says so', async ({ as }) => {
    const olivia = await as('owner');
    await olivia.page.goto('/calendar?google=declined');
    await expect(olivia.page.getByText('Google Calendar was not connected', { exact: false }).first()).toBeVisible();
    await expect(olivia.page.getByRole('button', { name: 'Connect Google Calendar' })).toBeVisible();
  });
});

// Reported: work done in a meeting held somewhere else could not go on the calendar.
test('an entry goes on your own calendar with its notes', async ({ as }) => {
  const stamp = Date.now();
  const alice = await as('alice');
  const page = alice.page;
  await page.goto('/calendar?view=day');
  await page.getByRole('button', { name: 'Add entry' }).click();
  const dialog = page.getByRole('dialog', { name: 'Add an entry' });
  await expect(dialog.getByRole('button', { name: /Entry/, pressed: true })).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Add to calendar' })).toBeDisabled();
  await dialog.getByLabel('Title').fill(`Workshop at ACME ${stamp}`);
  await dialog.getByLabel('Notes (optional)').fill('Agreed the pilot scope.\nNext: send the plan.');
  // Early on a day ahead, where the shared accounts' other meetings do not crowd it into "+N".
  const when = new Date();
  when.setDate(when.getDate() + 3);
  const day = `${when.getFullYear()}-${String(when.getMonth() + 1).padStart(2, '0')}-${String(when.getDate()).padStart(2, '0')}`;
  await dialog.getByLabel('From').fill(`${day}T06:00`);
  await dialog.getByLabel('To').fill(`${day}T07:00`);
  await page.screenshot({ path: 'test-results/entry-dialog.png' });
  await dialog.getByRole('button', { name: 'Add to calendar' }).click();
  await expect(page.getByText('Entry added.').first()).toBeVisible();
  await page.goto(`/calendar?view=day&date=${day}`);

  const entry = page.getByRole('button', { name: new RegExp(`^Workshop at ACME ${stamp}`) });
  await expect(entry).toBeVisible();
  await page.screenshot({ path: 'test-results/entry-day.png' });
  await entry.click();
  const shown = page.getByRole('dialog', { name: `Workshop at ACME ${stamp}` });
  await expect(shown.getByText('Your entry')).toBeVisible();
  await expect(shown.getByText('Agreed the pilot scope.', { exact: false })).toBeVisible();
  await shown.getByRole('button', { name: 'Remove' }).click();
  await shown.getByRole('button', { name: 'Remove' }).click();
  await expect(entry).toHaveCount(0);

  // The calendar's buttons still fit a phone.
  const phone = await as('alice', { phone: 'android' });
  await phone.page.goto('/calendar');
  await expect(phone.page.getByRole('button', { name: 'Add entry' })).toBeVisible();
  expect(await phone.page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await phone.page.screenshot({ path: 'test-results/entry-phone.png' });
});

// Reported: nobody could be brought into a task's conversation by name.
test('naming somebody in a task comment tells them', async ({ as }) => {
  const stamp = Date.now();
  const owner = await apiAs('owner');
  const task = await owner.post('/tasks', { title: `Mention me ${stamp}` });
  try {
    const olivia = await as('owner');
    const page = olivia.page;
    await page.goto(`/tasks/${task.id}`);
    await page.getByRole('button', { name: 'Add Comment' }).click();
    const box = page.getByRole('textbox', { name: 'Comment' });
    await expect(box).toBeFocused();
    await box.pressSequentially('Can you look, @ali');
    const suggestion = page.getByRole('option', { name: new RegExp(PEOPLE.alice.name) });
    await expect(suggestion).toBeVisible();
    await page.screenshot({ path: 'test-results/comment-mention.png' });
    await page.keyboard.press('Enter');
    await expect(box).toHaveValue('Can you look, @alice ');
    await box.pressSequentially('please?');
    await page.getByRole('button', { name: 'Post comment' }).click();
    await expect(page.getByTestId('mention').filter({ hasText: `@${PEOPLE.alice.name}` })).toBeVisible();

    const alice = await apiAs('alice');
    await expect
      .poll(async () => ((await alice.get('/notifications?limit=50')).notifications as any[]).some((n) => n.type === 'task.mention' && JSON.stringify(n).includes(`Mention me ${stamp}`)))
      .toBe(true);
    await alice.dispose();
  } finally {
    await owner.del(`/tasks/${task.id}`).catch(() => {});
    await owner.dispose();
  }
});
