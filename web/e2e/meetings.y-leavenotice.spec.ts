import { expect, test } from './support/fixtures';
import { apiAs, idOf, sql } from './support/meetings';

// Somebody on leave is not chased: a conversation with them says so above the
// message box, Call says so before anything rings, and the new-call list marks
// them. The owner's request of 16 November 2026.

function utcDay(offset = 0): string {
  const d = new Date();
  d.setUTCDate(d.getUTCDate() + offset);
  return d.toISOString().slice(0, 10);
}

const clean = () => sql(`DELETE FROM leave_changes; DELETE FROM leave_periods; DELETE FROM org_holidays`);

test.describe('on leave, and not chased', () => {
  test.beforeEach(() => clean());
  test.afterAll(() => clean());

  test('a conversation with somebody on leave says when they are back, and Call offers to write instead', async ({ as }) => {
    // Ann is away until two days from now, so she is back the day after.
    const annApi = await apiAs('ann');
    await annApi.post('/leave', { startsOn: utcDay(-1), endsOn: utcDay(2) });
    await annApi.dispose();

    const aliceApi = await apiAs('alice');
    const dm = await aliceApi.post('/channels/direct', { userId: idOf('ann') });
    const callsBefore = sql(`SELECT count(*) FROM calls`);

    const alice = await as('alice');
    await alice.page.goto(`/chat/${dm.id}`);
    const banner = alice.page.getByTestId('on-leave-banner');
    await expect(banner).toContainText('Ann is on leave, back on');
    await expect(banner).toContainText('please do not chase them while they are away');

    // Call: told first, and nothing is started.
    await alice.page.getByRole('button', { name: 'Call', exact: true }).click();
    const dialog = alice.page.getByRole('dialog');
    await expect(dialog.getByRole('heading', { name: 'Ann is on leave' })).toBeVisible();
    await expect(dialog).toContainText('Calls do not ring while somebody is on leave');
    await dialog.getByRole('button', { name: 'Write a message' }).click();
    await expect(dialog).toHaveCount(0);
    await expect(alice.page.getByRole('textbox', { name: 'Message', exact: true })).toBeFocused();
    expect(sql(`SELECT count(*) FROM calls`), 'no call was started').toBe(callsBefore);

    // Starting a call from Calls, her name says she will not be rung.
    await alice.page.goto('/calls');
    await alice.page.getByRole('button', { name: 'New Call' }).click();
    const start = alice.page.getByRole('dialog');
    await expect(start.locator('label', { hasText: 'Ann Idle' })).toContainText('On leave, back on');
    await expect(start.locator('label', { hasText: 'Alice' })).toHaveCount(0);

    // Somebody who is around has no banner, and Call does not ask.
    const toPat = await aliceApi.post('/channels/direct', { userId: idOf('pat') });
    await alice.page.goto(`/chat/${toPat.id}`);
    await expect(alice.page.getByRole('heading', { level: 2 }).first()).toBeVisible();
    await expect(alice.page.getByTestId('on-leave-banner')).toHaveCount(0);
    await aliceApi.dispose();
  });
});
