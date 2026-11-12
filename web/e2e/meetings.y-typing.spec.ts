import { expect, test } from './support/fixtures';
import { PEOPLE, readState } from './support/people';

// Asked for: the server already passed on who is typing, but nothing sent it
// and nothing showed it. Now the other people in a conversation see
// "Alice is typing…" under the message box, and it goes when she stops.

test('the others in a conversation see who is typing, and it goes when they stop or send', async ({ as }) => {
  test.setTimeout(90_000);
  const { channelId } = readState();
  const alice = await as('alice');
  const pat = await as('pat');
  await alice.page.goto(`/chat/${channelId}`);
  await pat.page.goto(`/chat/${channelId}`);

  const box = alice.page.getByLabel('Message', { exact: true });
  const seenByPat = pat.page.getByTestId('typing-indicator');
  await expect(box).toBeEnabled();
  // Nobody is typing yet.
  await expect(seenByPat).toHaveText('');

  // Alice starts to write.
  await box.pressSequentially('Hello th', { delay: 40 });
  await expect(seenByPat).toHaveText(`${PEOPLE.alice.name} is typing…`, { timeout: 10_000 });
  await pat.page.screenshot({ path: 'test-results/typing-seen.png' });
  // Never shown to the person typing.
  await expect(alice.page.getByTestId('typing-indicator')).toHaveText('');
  // Showing it does not move the conversation or the box.
  const boxBefore = await pat.page.getByLabel('Message', { exact: true }).boundingBox();

  // She clears what she wrote: no longer typing.
  await box.fill('');
  await expect(seenByPat).toHaveText('', { timeout: 10_000 });
  const boxAfter = await pat.page.getByLabel('Message', { exact: true }).boundingBox();
  expect(boxAfter!.y).toBe(boxBefore!.y);

  // She writes and sends: it goes as the message arrives.
  const words = `typed and sent ${Date.now()}`;
  await box.pressSequentially(words.slice(0, 6), { delay: 40 });
  await expect(seenByPat).toHaveText(`${PEOPLE.alice.name} is typing…`, { timeout: 10_000 });
  await box.pressSequentially(words.slice(6));
  await box.press('Enter');
  await expect(pat.page.getByText(words)).toBeVisible();
  await expect(seenByPat).toHaveText('');

  // She types and then just stops: it goes by itself after a pause.
  await box.pressSequentially('one more', { delay: 40 });
  await expect(seenByPat).toHaveText(`${PEOPLE.alice.name} is typing…`, { timeout: 10_000 });
  await expect(seenByPat).toHaveText('', { timeout: 12_000 });

  // Leaving the conversation is stopping too.
  await box.pressSequentially(' again', { delay: 40 });
  await expect(seenByPat).toHaveText(`${PEOPLE.alice.name} is typing…`, { timeout: 10_000 });
  await alice.page.goto('/tasks');
  await expect(seenByPat).toHaveText('', { timeout: 10_000 });
});
