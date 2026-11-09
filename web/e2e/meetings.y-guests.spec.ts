import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { apiAs, dayParam, iso, idOf, localInput, openOnCalendar, sql } from './support/meetings';

// Guests from outside the organization: the organizer makes a guest link for a
// scheduled meeting, somebody with no account opens it, gives their name and
// the passcode, waits for the meeting to start, and is in the call, marked as a
// guest, until the organizer removes them.

const minutesFromNow = (m: number) => new Date(Date.now() + m * 60_000);
const inACall = (page: Page) => page.getByRole('button', { name: 'Leave the call' });

/** Whether a page is playing a live audio track from somebody else. */
const hearing = (page: Page) =>
  page.evaluate(() =>
    Array.from(document.querySelectorAll('audio')).some((el) => {
      const stream = el.srcObject as MediaStream | null;
      return !el.paused && !!stream && stream.getAudioTracks().some((t) => t.readyState === 'live');
    })
  );

test.describe('guests', () => {
  test('a guest joins a scheduled meeting by its link, name and passcode, and the organizer can remove them', async ({ as }) => {
    test.setTimeout(150_000);
    const owner = await apiAs('owner');
    const title = `Client review ${Date.now()}`;
    const created = await owner.post('/meetings', {
      title,
      startsAt: iso(minutesFromNow(10)),
      endsAt: iso(minutesFromNow(40)),
      inviteeIds: [idOf('alice')],
    });
    const id = created.meeting.id as string;

    // The organizer makes the link from the meeting in the calendar.
    const olivia = await as('owner');
    await openOnCalendar(olivia.page, title);
    const dialog = olivia.page.getByRole('dialog');
    await dialog.getByRole('button', { name: 'Make guest links' }).click();
    const url = await dialog.getByLabel('Link with passcode', { exact: true }).inputValue();
    expect(await dialog.getByLabel('Direct link', { exact: true }).inputValue()).not.toBe(url);
    const passcode = (await dialog.getByTestId('guest-passcode').textContent())!.trim();
    expect(url).toMatch(/\/join\/[A-Za-z0-9_-]{20,}$/);
    expect(passcode).toMatch(/^\d{6}$/);
    await olivia.page.keyboard.press('Escape');

    // Alice, invited, can pass it on but not change it.
    const alice = await apiAs('alice');
    const seen = await alice.get(`/calls/${id}/guest-link`);
    expect(seen.passcode).toBe(passcode);
    expect(seen.canManage).toBe(false);

    // A guest with no account opens it before the meeting has started.
    const guest = await as('sam', { signedOut: true });
    await guest.page.goto(new URL(url).pathname);
    await expect(guest.page.getByRole('heading', { name: title })).toBeVisible();
    const join = guest.page.getByRole('button', { name: /Join the meeting|Waiting for the meeting to start/ });
    await expect(join).toHaveText('Waiting for the meeting to start');
    await expect(join).toBeDisabled();
    await guest.page.getByLabel('Your name').fill('Pat Partner');
    await guest.page.getByLabel('Passcode').fill('000000');

    // The organizer starts it, and the waiting page lets the guest in.
    await olivia.page.goto(`/calls/${id}`);
    await olivia.page.getByRole('button', { name: /^Join( now)?$/ }).click();
    await expect(inACall(olivia.page)).toBeVisible({ timeout: 30_000 });
    await expect(join).toHaveText('Join the meeting', { timeout: 15_000 });

    await join.click();
    await expect(guest.page.getByRole('alert')).toContainText('passcode is not right');
    await guest.page.getByLabel('Passcode').fill(passcode);
    await join.click();
    await expect(guest.page.getByRole('button', { name: 'Leave' })).toBeVisible({ timeout: 30_000 });
    await expect(guest.page.getByText('Pat Partner (guest) (you)')).toBeVisible({ timeout: 20_000 });
    await expect(guest.page.getByText('Olivia Owner', { exact: true })).toBeVisible();
    // A guest has no account to mute anybody with.
    await expect(guest.page.getByRole('button', { name: /^Mute / })).toHaveCount(0);

    // Everybody in the meeting sees who is a guest.
    await expect(olivia.page.getByText('Pat Partner (guest)', { exact: true })).toBeVisible({ timeout: 20_000 });

    // The organizer removes them, and they are told.
    await olivia.page.getByText('Pat Partner (guest)', { exact: true }).hover();
    await olivia.page.getByRole('button', { name: 'Remove Pat Partner from the meeting' }).click();
    await expect(guest.page.getByTestId('guest-notice')).toContainText('The organizer removed you from the meeting.', { timeout: 20_000 });
    await expect(guest.page.getByRole('button', { name: 'Join again' })).toHaveCount(0);

    // Turned off, the link stops working.
    await owner.put(`/calls/${id}/guest-link`, { enabled: false });
    await guest.page.reload();
    await expect(guest.page.getByTestId('guest-notice')).toContainText('This link does not work');

    await olivia.page.getByRole('button', { name: 'Leave the call' }).click();
    await Promise.all([owner.dispose(), alice.dispose()]);
  });

  test('scheduling with a guest\'s email sends them the direct link, which needs no passcode, and they can come back from the same browser', async ({ as }) => {
    test.setTimeout(150_000);
    const SMTP_PORT = Number(process.env.E2E_MAILPIT_SMTP_PORT ?? 55480);
    const MAILPIT = process.env.E2E_MAILPIT_API ?? 'http://127.0.0.1:55481';
    const owner = await apiAs('owner');
    await owner.put('/org/mail', { host: '127.0.0.1', port: SMTP_PORT, user: 'teamup', password: 'mailpit-accepts-any', from: 'TeamUp <teamup@e2e.test>' });
    try {
      const guestEmail = `client-${Date.now()}@outside.test`;
      const title = `Pitch ${Date.now()}`;

      // The organizer schedules it with an outside guest's address.
      const olivia = await as('owner');
      await olivia.page.goto(`/calendar?date=${dayParam(new Date())}`);
      await olivia.page.getByRole('button', { name: 'Schedule', exact: true }).click();
      const dialog = olivia.page.getByRole('dialog');
      await dialog.getByLabel('Name', { exact: true }).fill(title);
      await dialog.getByLabel('Starts', { exact: true }).fill(localInput(minutesFromNow(10)));
      await dialog.getByLabel('Guests from outside').fill('not-an-address');
      await expect(dialog.getByText('That does not look like an email address.')).toBeVisible();
      await dialog.getByLabel('Guests from outside').fill(guestEmail);
      await dialog.getByLabel('Guests from outside').press('Enter');
      await expect(dialog.getByRole('list', { name: 'Guests invited by email' })).toContainText(guestEmail);
      await dialog.getByRole('button', { name: /^Schedule( anyway)?$/ }).click();
      await expect(olivia.page.getByText(/guest has been emailed an invitation/).first()).toBeVisible();

      // The guest's email has the direct link, and the passcode one as a fallback.
      let body = '';
      await expect
        .poll(async () => {
          const res = await fetch(`${MAILPIT}/api/v1/search?query=${encodeURIComponent(`to:"${guestEmail}"`)}`);
          const found = ((await res.json()).messages ?? []) as Array<{ ID: string; Subject: string }>;
          const invite = found.find((m) => m.Subject === `Invitation: ${title}`);
          if (!invite) return '';
          body = (await (await fetch(`${MAILPIT}/api/v1/message/${invite.ID}`)).json()).Text as string;
          return body;
        }, { timeout: 30_000 })
        .toContain('no account needed');
      const links = [...body.matchAll(/https?:\/\/\S+\/join\/[A-Za-z0-9_-]+/g)].map((m) => m[0]);
      expect(links).toHaveLength(2);
      expect(body).toMatch(/enter the passcode \d{6}/);
      const direct = new URL(links[0]).pathname;

      // The organizer starts it.
      const id = sql(`SELECT id FROM calls WHERE title = '${title}'`);
      await olivia.page.goto(`/calls/${id}`);
      await olivia.page.getByRole('button', { name: /^Join( now)?$/ }).click();
      await expect(inACall(olivia.page)).toBeVisible({ timeout: 30_000 });

      // The direct link asks only for a name.
      const guest = await as('sam', { signedOut: true });
      await guest.page.goto(direct);
      await expect(guest.page.getByRole('heading', { name: title })).toBeVisible();
      await expect(guest.page.getByLabel('Passcode')).toHaveCount(0);
      await guest.page.getByLabel('Your name').fill('Chris Client');
      await guest.page.getByRole('button', { name: 'Join the meeting' }).click();
      await expect(guest.page.getByRole('button', { name: 'Leave', exact: true })).toBeVisible({ timeout: 30_000 });
      await expect(olivia.page.getByText('Chris Client (guest)', { exact: true })).toBeVisible({ timeout: 20_000 });

      // Back from the same browser after closing the page: one click, as the same guest.
      await guest.page.reload();
      await expect(guest.page.getByTestId('guest-rejoin')).toContainText('Chris Client');
      await guest.page.getByRole('button', { name: 'Join again' }).click();
      await expect(guest.page.getByRole('button', { name: 'Leave', exact: true })).toBeVisible({ timeout: 30_000 });
      await expect(olivia.page.getByText('Chris Client (guest)', { exact: true })).toHaveCount(1, { timeout: 20_000 });
      expect(sql(`SELECT COUNT(*) FROM call_guests WHERE call_id = '${id}'`)).toBe('1');
      await expect.poll(() => hearing(olivia.page), { timeout: 20_000 }).toBe(true);

      // Leaving, then joining again from the page itself.
      await guest.page.getByRole('button', { name: 'Leave', exact: true }).click();
      await expect(guest.page.getByTestId('guest-notice')).toContainText('You left the meeting.');
      await guest.page.getByRole('button', { name: 'Join again' }).click();
      await guest.page.getByRole('button', { name: 'Join again' }).click();
      await expect(guest.page.getByRole('button', { name: 'Leave', exact: true })).toBeVisible({ timeout: 30_000 });
      await expect(olivia.page.getByText('Chris Client (guest)', { exact: true })).toBeVisible({ timeout: 20_000 });
      // Heard again, whatever LiveKit's client logs about the race on the way.
      // (Olivia joined with her microphone off, as members join meetings, so
      // there is nothing for the guest to hear.)
      await expect.poll(() => hearing(olivia.page), { timeout: 20_000 }).toBe(true);

      await olivia.page.getByRole('button', { name: 'Leave the call' }).click();
    } finally {
      sql(`DELETE FROM org_configs WHERE key = 'smtp'`);
      await owner.dispose();
    }
  });
});
