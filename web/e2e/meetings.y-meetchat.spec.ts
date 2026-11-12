import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { apiAs, idOf, iso } from './support/meetings';

// A meeting's own chat, as in a video meeting's side panel: the people in the
// call write to each other beside the video, a guest included, somebody who
// comes in late reads what was written before, a message that arrives while
// the chat is closed shows over the video and counts on the chat button, and
// afterwards the chat stays with the call.

const minutesFromNow = (m: number) => new Date(Date.now() + m * 60_000);
const inACall = (page: Page) => page.getByRole('button', { name: 'Leave the call' });
const chatButton = (page: Page) => page.getByTestId('meeting-chat-button');
const chat = (page: Page) => page.getByTestId('meeting-chat');
const say = async (page: Page, text: string) => {
  const box = chat(page).getByLabel('Message to everyone in the call');
  await box.fill(text);
  await box.press('Enter');
  await expect(box).toHaveValue('');
};

test.describe('meeting chat', () => {
  test('people in a meeting chat beside the video, a late guest reads what came before, and the chat stays with the call', async ({ as }) => {
    test.setTimeout(180_000);
    const owner = await apiAs('owner');
    const alice = await apiAs('alice');
    const title = `Chatty review ${Date.now()}`;
    const created = await owner.post('/meetings', {
      title,
      startsAt: iso(minutesFromNow(10)),
      endsAt: iso(minutesFromNow(40)),
      inviteeIds: [idOf('alice')],
    });
    const id = created.meeting.id as string;
    const link = await owner.put(`/calls/${id}/guest-link`, { enabled: true });

    const olivia = await as('owner');
    await olivia.page.goto(`/calls/${id}`);
    await olivia.page.getByRole('button', { name: /^Join( now)?$/ }).click();
    await expect(inACall(olivia.page)).toBeVisible({ timeout: 30_000 });

    const ally = await as('alice');
    // Invited, and the meeting is on: opening it is joining it.
    await ally.page.goto(`/calls/${id}`);
    await expect(inACall(ally.page)).toBeVisible({ timeout: 30_000 });
    await expect(olivia.page.getByText('Alice Archer', { exact: true })).toBeVisible({ timeout: 20_000 });

    // Olivia opens the chat beside the video and writes.
    await chatButton(olivia.page).click();
    await expect(chat(olivia.page)).toBeVisible();
    await expect(chat(olivia.page)).toContainText('No messages yet.');
    await expect(chat(olivia.page)).toContainText('Everyone in the call can see these, guests included.');
    await say(olivia.page, 'Agenda: https://example.test/agenda');
    const mine = chat(olivia.page).getByTestId('meeting-chat-message').last();
    await expect(mine).toContainText('You');
    await expect(mine.getByRole('link', { name: 'https://example.test/agenda' })).toBeVisible();

    // Beside the video on a wide screen, the page does not scroll sideways.
    const box = await chat(olivia.page).boundingBox();
    const width = await olivia.page.evaluate(() => window.innerWidth);
    expect(box!.x + box!.width).toBeLessThanOrEqual(width + 0.5);
    expect(await olivia.page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);

    // Alice, with the chat closed, sees it over the video and on the button.
    const preview = ally.page.getByTestId('meeting-chat-preview');
    await expect(preview).toContainText('Olivia Owner', { timeout: 15_000 });
    await expect(preview).toContainText('Agenda: https://example.test/agenda');
    await expect(ally.page.getByTestId('meeting-chat-unread')).toHaveText('1');
    await preview.getByRole('button').first().click();
    await expect(chat(ally.page)).toContainText('Agenda: https://example.test/agenda');
    await expect(ally.page.getByTestId('meeting-chat-unread')).toHaveCount(0);
    await say(ally.page, 'Thanks, reading it now');
    await expect(chat(olivia.page)).toContainText('Alice Archer', { timeout: 15_000 });
    await expect(chat(olivia.page)).toContainText('Thanks, reading it now');

    // A guest who comes in later reads what was written before, and writes too.
    const guest = await as('sam', { signedOut: true });
    await guest.page.goto(link.directPath as string);
    await guest.page.getByLabel('Your name').fill('Pat Partner');
    await guest.page.getByRole('button', { name: 'Join the meeting' }).click();
    await expect(guest.page.getByRole('button', { name: 'Leave' })).toBeVisible({ timeout: 30_000 });
    // What was there before they came is there to read, not news.
    await expect(guest.page.getByTestId('meeting-chat-unread')).toHaveCount(0);
    await chatButton(guest.page).click();
    await expect(chat(guest.page).getByTestId('meeting-chat-message')).toHaveCount(2);
    await expect(chat(guest.page)).toContainText('Agenda: https://example.test/agenda');
    await expect(chat(guest.page)).toContainText('Thanks, reading it now');
    await say(guest.page, 'Hello from Pat');
    // Their own message comes after what was there before, in the order written.
    await expect(chat(guest.page).getByTestId('meeting-chat-message').last()).toContainText('Hello from Pat');
    await expect(chat(guest.page).getByTestId('meeting-chat-message').first()).toContainText('Agenda');
    await expect(chat(olivia.page)).toContainText('Pat Partner (guest)', { timeout: 15_000 });
    await expect(chat(ally.page)).toContainText('Hello from Pat', { timeout: 15_000 });

    // Closed and minimised, a new message still counts on the corner window.
    await chat(ally.page).getByRole('button', { name: 'Close the chat' }).click();
    await ally.page.getByRole('button', { name: 'Minimise the call' }).click();
    await say(olivia.page, 'One more thing');
    await say(olivia.page, 'And a second one');
    // Each message is its own bubble with its own time, even in a run from one person.
    const own = chat(olivia.page).getByTestId('meeting-chat-message');
    await expect(own.last()).toContainText('And a second one');
    await expect(own.last().locator('time')).toHaveCount(1);
    await expect(own.nth(-2).locator('time')).toHaveCount(1);
    await expect(ally.page.getByTestId('meeting-chat-unread-compact')).toHaveText('2', { timeout: 15_000 });

    // Afterwards, the chat stays with the call for the people who were in it.
    await guest.page.getByRole('button', { name: 'Leave' }).click();
    await ally.page.getByRole('button', { name: 'Leave the call' }).click();
    await olivia.page.getByRole('button', { name: 'Leave the call' }).click();
    await expect
      .poll(async () => ((await alice.get(`/calls/${id}/chat`)).messages as Array<{ content: string }>).map((m) => m.content), {
        timeout: 20_000,
      })
      .toEqual(['Agenda: https://example.test/agenda', 'Thanks, reading it now', 'Hello from Pat', 'One more thing', 'And a second one']);

    await Promise.all([owner.dispose(), alice.dispose()]);
  });

  test('on a phone the chat covers the video, and closing it brings the video back', async ({ as }) => {
    test.setTimeout(120_000);
    const owner = await apiAs('owner');
    const title = `Phone chat ${Date.now()}`;
    const created = await owner.post('/meetings', {
      title,
      startsAt: iso(minutesFromNow(10)),
      endsAt: iso(minutesFromNow(40)),
      inviteeIds: [idOf('alice')],
    });
    const id = created.meeting.id as string;

    const olivia = await as('owner');
    await olivia.page.setViewportSize({ width: 390, height: 844 });
    await olivia.page.goto(`/calls/${id}`);
    await olivia.page.getByRole('button', { name: /^Join( now)?$/ }).click();
    await expect(inACall(olivia.page)).toBeVisible({ timeout: 30_000 });

    await chatButton(olivia.page).click();
    const panel = chat(olivia.page);
    await expect(panel).toBeVisible();
    const box = (await panel.boundingBox())!;
    expect(box.x).toBeGreaterThanOrEqual(-0.5);
    expect(box.x + box.width).toBeLessThanOrEqual(390.5);
    expect(box.width).toBeGreaterThan(380);
    expect(await olivia.page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
    await say(olivia.page, 'Typed on a phone');
    await expect(panel).toContainText('Typed on a phone');
    // The controls stay below it, so the chat can be closed from either place.
    await expect(chatButton(olivia.page)).toBeVisible();
    await chatButton(olivia.page).click();
    await expect(panel).toHaveCount(0);

    await olivia.page.getByRole('button', { name: 'Leave the call' }).click();
    await owner.dispose();
  });
});
