import { expect, test } from './support/fixtures';
import { apiAs, idOf, joinCall, createCall, leaveCall } from './support/meetings';
import { PEOPLE } from './support/people';

// Asked for: somebody already in a call or meeting is not rung, and the caller
// is told they are busy; a call nobody answered, and one turned down, each
// have their own mark.
test.describe('calls that did not happen', () => {
  test('calling somebody in another call says they are busy, and a declined call says declined', async ({ as }) => {
    const owner = await apiAs('owner');
    const pat = await apiAs('pat');
    const toAlice = await pat.post('/channels/direct', { userId: idOf('alice') });
    const toSam = await pat.post('/channels/direct', { userId: idOf('sam') });

    // Alice is in a meeting.
    const meeting = await createCall(owner, `Busy check ${Date.now()}`, [idOf('alice')]);
    const alice = await as('alice');
    await joinCall(alice.page, meeting.id);

    const caller = await as('pat');
    await caller.page.goto(`/chat/${toAlice.id}`);
    await caller.page.getByRole('button', { name: 'Call', exact: true }).click();
    await expect(caller.page.getByText(`${PEOPLE.alice.name} is in another call or meeting.`, { exact: true })).toBeVisible({ timeout: 20_000 });
    const busy = caller.page.locator('[data-testid="call-event"][data-kind="busy"]').last();
    await expect(busy, 'the caller is back in the conversation, which says so').toContainText('Busy, in another call or meeting', {
      timeout: 20_000,
    });
    await expect(alice.page.getByText('Incoming call'), 'Alice was not rung in her meeting').toHaveCount(0);
    await caller.page.screenshot({ path: 'test-results/call-busy-caller.png' });

    // Alice finds out who called, in red, once she looks.
    await alice.page.goto(`/chat/${toAlice.id}`);
    const missed = alice.page.locator('[data-testid="call-event"][data-kind="busy"]').last();
    await expect(missed).toContainText(`Missed call from ${PEOPLE.pat.name} while you were in another call`);
    await alice.page.screenshot({ path: 'test-results/call-busy-callee.png' });
    await leaveCall(alice.page).catch(() => {});

    // Sam turns a call down.
    const sam = await as('sam');
    await sam.page.goto('/tasks');
    await caller.page.goto(`/chat/${toSam.id}`);
    await caller.page.getByRole('button', { name: 'Call', exact: true }).click();
    await expect(sam.page.getByText('Incoming call')).toBeVisible({ timeout: 20_000 });
    await sam.page.getByRole('button', { name: 'Decline' }).click();
    await expect(caller.page.locator('[data-testid="call-event"][data-kind="declined"]').last()).toContainText('Call declined', {
      timeout: 20_000,
    });
    await sam.page.goto(`/chat/${toSam.id}`);
    await expect(sam.page.locator('[data-testid="call-event"][data-kind="declined"]').last()).toContainText(
      `You declined a call from ${PEOPLE.pat.name}`
    );

    // And the calls list says which was which.
    await caller.page.goto('/calls');
    await expect(caller.page.getByTestId('call-status').filter({ hasText: /^Busy$/ }).first()).toBeVisible();
    await expect(caller.page.getByTestId('call-status').filter({ hasText: /^Declined$/ }).first()).toBeVisible();
    await expect(caller.page.getByText(/PM0|AM0/), 'no stray zero after the end time').toHaveCount(0);
    await caller.page.screenshot({ path: 'test-results/call-marks-list.png' });
    await sam.page.goto('/calls');
    await expect(sam.page.getByTestId('call-status').filter({ hasText: /^You declined$/ }).first()).toBeVisible();

    await Promise.all([owner.dispose(), pat.dispose()]);
  });

  // Seen on GitHub: declined, the caller ended up on the calls list instead of
  // the conversation. Two things follow a decline, in either order: being told
  // it was missed moves them to the conversation, and the room closing steps
  // them off the call page. Each order is forced here by holding one back.
  for (const late of ['media', 'app'] as const) {
    test(`a declined caller ends up in the conversation when the ${late === 'media' ? "room's closing" : 'word that it was missed'} arrives second`, async ({ as }) => {
      const pat = await apiAs('pat');
      const toSam = await pat.post('/channels/direct', { userId: idOf('sam') });
      const sam = await as('sam');
      await sam.page.goto('/tasks');
      const caller = await as('pat');

      let holding = false;
      const port = late === 'media' ? new URL(process.env.E2E_LIVEKIT_URL ?? 'http://localhost:55464').port : null;
      await caller.page.routeWebSocket(port ? new RegExp(`:${port}/`) : /\/ws$/, (ws) => {
        const server = ws.connectToServer();
        server.onMessage((m) => {
          if (holding) setTimeout(() => ws.send(m), 3_000);
          else ws.send(m);
        });
      });

      await caller.page.goto(`/chat/${toSam.id}`);
      await caller.page.getByRole('button', { name: 'Call', exact: true }).click();
      await expect(sam.page.getByText('Incoming call')).toBeVisible({ timeout: 20_000 });
      await expect(caller.page.getByRole('button', { name: 'Leave the call' })).toBeVisible({ timeout: 20_000 });
      holding = true;
      await sam.page.getByRole('button', { name: 'Decline' }).click();

      await expect(caller.page).toHaveURL(new RegExp(`/chat/${toSam.id}$`), { timeout: 20_000 });
      // After whatever was held back has arrived, still there.
      await caller.page.waitForTimeout(5_000);
      await expect(caller.page).toHaveURL(new RegExp(`/chat/${toSam.id}$`));
      await expect(caller.page.locator('[data-testid="call-event"][data-kind="declined"]').last()).toContainText('Call declined');
      await pat.dispose();
    });
  }
});
