import { expect, test } from './support/fixtures';
import { apiAs, idOf } from './support/meetings';
import { PEOPLE } from './support/people';

// Asked for: two people in a call ring a third colleague into it from inside
// the call, the third hears whose call it is and who is in it, and answering
// brings them in. Somebody who declines is told to everybody in the call, and
// the call carries on.
test.describe('adding people to a call already going', () => {
  test('a colleague rung into a call answers and is in it with both; one who declines leaves it going', async ({ as }) => {
    const pat = await apiAs('pat');
    const toSam = await pat.post('/channels/direct', { userId: idOf('sam') });

    // Sam on a phone, Pat in the dark, so both looks are seen.
    const sam = await as('sam', { phone: 'android' });
    await sam.page.goto('/tasks');
    const alice = await as('alice');
    await alice.page.goto('/tasks');
    const ann = await as('ann');
    await ann.page.goto('/tasks');
    const caller = await as('pat', { theme: 'dark' });

    // A calls B, and both are in. A call, so it stays a call when somebody is added.
    await caller.page.goto(`/chat/${toSam.id}`);
    await caller.page.getByRole('button', { name: 'Call', exact: true }).click();
    await expect(sam.page.getByText('Incoming call')).toBeVisible({ timeout: 20_000 });
    await sam.page.getByRole('button', { name: 'Answer' }).click();
    await expect(caller.page.getByText(/In a call · 2/)).toBeVisible({ timeout: 30_000 });
    await expect(sam.page.getByText(/In a call · 2/)).toBeVisible({ timeout: 30_000 });
    const callPath = new URL(caller.page.url()).pathname;
    expect(callPath).toMatch(/^\/calls\//);

    // A adds C. The picker tells colleagues from guests, and does not offer whoever is here.
    await caller.page.getByRole('button', { name: 'Add people' }).click();
    const dialog = caller.page.getByRole('dialog', { name: 'Add people' });
    await expect(dialog.getByText(/To bring in somebody from outside, use Guests/)).toBeVisible();
    const offered = dialog.getByRole('list', { name: 'People' });
    await expect(offered.getByText(PEOPLE.alice.name, { exact: true })).toBeVisible();
    await expect(offered.getByText(PEOPLE.sam.name, { exact: true }), 'Sam is already in the call').toHaveCount(0);
    await expect(offered.getByText(PEOPLE.pat.name, { exact: true })).toHaveCount(0);
    await dialog.getByLabel('Find people').fill('alice');
    await offered.getByText(PEOPLE.alice.name, { exact: true }).click();
    await caller.page.screenshot({ path: 'test-results/add-people-dialog-dark.png' });
    await dialog.getByRole('button', { name: 'Ring', exact: true }).click();
    await expect(dialog).toHaveCount(0);

    // Both in the call see her being rung, and she hears whose call it is.
    const ringingAlice = `Ringing ${PEOPLE.alice.name}…`;
    await expect(caller.page.getByText(ringingAlice)).toBeVisible();
    await expect(sam.page.getByText(ringingAlice)).toBeVisible({ timeout: 10_000 });
    await expect(alice.page.getByText('Incoming call')).toBeVisible({ timeout: 20_000 });
    await expect(alice.page.getByText(`${PEOPLE.pat.name} is calling you into a call with ${PEOPLE.sam.name}.`)).toBeVisible();
    await caller.page.screenshot({ path: 'test-results/add-people-ringing-dark.png' });
    await sam.page.screenshot({ path: 'test-results/add-people-ringing-phone.png' });
    await alice.page.screenshot({ path: 'test-results/add-people-incoming.png' });

    // She answers, and is in this call with both of them, with her microphone on as in any call.
    await alice.page.getByRole('button', { name: 'Answer' }).click();
    await expect(alice.page).toHaveURL(new RegExp(`${callPath}$`));
    await expect(alice.page.getByText(/In a call · 3/)).toBeVisible({ timeout: 30_000 });
    await expect(alice.page.getByRole('button', { name: 'Turn your microphone off' })).toBeVisible({ timeout: 20_000 });
    await expect(caller.page.getByText(/In a call · 3/)).toBeVisible({ timeout: 30_000 });
    await expect(sam.page.getByText(/In a call · 3/)).toBeVisible({ timeout: 30_000 });
    await expect(caller.page.getByText(ringingAlice)).toHaveCount(0);

    // B adds D, who turns it down: everybody in the call is told, and the call goes on.
    await sam.page.getByRole('button', { name: 'Add people' }).click();
    const samDialog = sam.page.getByRole('dialog', { name: 'Add people' });
    await expect(samDialog.getByRole('list', { name: 'People' }).getByText(PEOPLE.alice.name, { exact: true })).toHaveCount(0);
    await samDialog.getByLabel('Find people').fill('ann');
    await samDialog.getByRole('list', { name: 'People' }).getByText(PEOPLE.ann.name, { exact: true }).click();
    await sam.page.screenshot({ path: 'test-results/add-people-dialog-phone.png' });
    await samDialog.getByRole('button', { name: 'Ring', exact: true }).click();
    await expect(ann.page.getByText(new RegExp(`^${PEOPLE.sam.name} is calling you into a call with .+\\.$`))).toBeVisible({ timeout: 20_000 });
    await expect(caller.page.getByText(`Ringing ${PEOPLE.ann.name}…`)).toBeVisible({ timeout: 10_000 });
    await ann.page.getByRole('button', { name: 'Decline' }).click();

    for (const who of [caller, sam, alice]) {
      await expect(who.page.getByText(`${PEOPLE.ann.name} declined.`, { exact: true }), `${who.who} hears Ann declined`).toBeVisible({ timeout: 15_000 });
      await expect(who.page.getByText(`Ringing ${PEOPLE.ann.name}…`)).toHaveCount(0);
    }
    await caller.page.screenshot({ path: 'test-results/add-people-declined-dark.png' });
    // Nobody was moved to the conversation, and nobody left the call.
    await caller.page.waitForTimeout(2_000);
    for (const who of [caller, sam, alice]) {
      await expect(who.page).toHaveURL(new RegExp(`${callPath}$`));
      await expect(who.page.getByText(/In a call · 3/)).toBeVisible();
    }

    // Ringing her again is allowed, and cancelling stops her ring.
    await caller.page.getByRole('button', { name: 'Add people' }).click();
    const again = caller.page.getByRole('dialog', { name: 'Add people' });
    await again.getByRole('list', { name: 'People' }).getByText(PEOPLE.ann.name, { exact: true }).click();
    await again.getByRole('button', { name: 'Ring', exact: true }).click();
    await expect(ann.page.getByText('Incoming call')).toBeVisible({ timeout: 20_000 });
    await caller.page.getByRole('button', { name: `Stop ringing ${PEOPLE.ann.name}` }).click();
    await expect(ann.page.getByText('Incoming call'), 'her ring stops').toHaveCount(0, { timeout: 15_000 });
    await expect(sam.page.getByText(`Ringing ${PEOPLE.ann.name}…`)).toHaveCount(0, { timeout: 10_000 });

    for (const who of [alice, sam, caller]) await who.page.getByRole('button', { name: 'Leave the call' }).click();
    await pat.dispose();
  });
});
