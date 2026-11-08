import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { apiAs, dayParam, iso, idOf } from './support/meetings';

// Guests from outside the organization: the organizer makes a guest link for a
// scheduled meeting, somebody with no account opens it, gives their name and
// the passcode, waits for the meeting to start, and is in the call, marked as a
// guest, until the organizer removes them.

const minutesFromNow = (m: number) => new Date(Date.now() + m * 60_000);
const inACall = (page: Page) => page.getByRole('button', { name: 'Leave the call' });

/** Opens a meeting from today's calendar, also when it is folded among others at the same time. */
async function openInToday(page: Page, title: string) {
  await page.goto(`/calendar?view=day&date=${dayParam(new Date())}`);
  const main = page.getByRole('main');
  const direct = main.getByRole('button', { name: new RegExp(`^${title}`) });
  const chips = main.getByRole('button', { name: /more at this time/ });
  await expect.poll(async () => (await direct.count()) + (await chips.count()), { message: `${title} or a fold` }).toBeGreaterThan(0);
  if (await direct.count()) return direct.first().click();
  for (let i = 0; i < (await chips.count()); i++) {
    await chips.nth(i).click();
    const listed = page.getByRole('dialog').getByRole('button', { name: new RegExp(`^${title}`) });
    if (await listed.count()) return listed.first().click();
    await page.keyboard.press('Escape');
  }
  throw new Error(`${title} is not on today's calendar`);
}

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
    await openInToday(olivia.page, title);
    const dialog = olivia.page.getByRole('dialog');
    await dialog.getByRole('button', { name: 'Make a guest link' }).click();
    const url = await dialog.getByLabel('Guest link', { exact: true }).inputValue();
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
});
