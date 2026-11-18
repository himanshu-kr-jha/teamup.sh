import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { apiAs, createCall, idOf, joinCall, leaveCall } from './support/meetings';

// Meeting notes turned on and off for one call, and paused and resumed during
// it, in a real call on the real LiveKit. Sarvam and the model are a local
// stand-in that counts what it is sent; Chromium's fake microphone beeps the
// whole time, so anything heard while paused would show.

let standIn: Server;
let standInUrl = '';
// When each clip reached speech to text, and what the model was asked.
const heard: number[] = [];
const asked: string[] = [];

const banner = (page: Page) => page.getByTestId('notes-banner');
const PAUSED = /^Notes paused by Alice Archer at \d{1,2}:\d{2}( [AP]M)?\. Nothing said now is written down\.$/;

async function say(page: Page, text: string) {
  const box = page.getByTestId('meeting-chat').getByLabel('Message to everyone in the call');
  await box.fill(text);
  await box.press('Enter');
  await expect(box).toHaveValue('');
}

test.describe('meeting notes on and off', () => {
  test.beforeAll(async () => {
    standIn = createServer((req, res) => {
      let body = '';
      req.on('data', (c) => (body += c));
      req.on('end', () => {
        res.setHeader('Content-Type', 'application/json');
        if (req.url === '/speech-to-text') {
          heard.push(Date.now());
          res.end(JSON.stringify({ transcript: `Spoken clip ${heard.length}.` }));
        } else if (req.url === '/v1/messages') {
          asked.push(JSON.parse(body).messages[0].content as string);
          res.end(
            JSON.stringify({
              content: [
                {
                  type: 'text',
                  text: JSON.stringify({ summary: 'They talked about the release.', decisions: [], actionItems: [], openQuestions: [] }),
                },
              ],
            })
          );
        } else {
          res.statusCode = 404;
          res.end('{}');
        }
      });
    });
    await new Promise<void>((resolve) => standIn.listen(0, '127.0.0.1', resolve));
    standInUrl = `http://127.0.0.1:${(standIn.address() as AddressInfo).port}`;
    const owner = await apiAs('owner');
    await owner.ctx.put('/api/org/notetaker', {
      data: {
        enabled: true,
        onlyWhenAsked: false,
        sttKey: 'sk-sarvam-test',
        sttBaseUrl: standInUrl,
        llmProvider: 'anthropic',
        llmKey: 'sk-ant-test',
        llmBaseUrl: standInUrl,
      },
    });
  });

  test.afterAll(async () => {
    // Later specs' calls must not have a note-taker in them.
    const owner = await apiAs('owner');
    await owner.ctx.put('/api/org/notetaker', { data: { enabled: false, llmProvider: 'anthropic' } });
    await new Promise((resolve) => standIn.close(resolve));
  });

  test('anybody pauses the notes, everybody sees who did, and nothing said or typed then reaches the minutes', async ({ as }) => {
    test.setTimeout(240_000);
    const owner = await apiAs('owner');
    const title = `Pausable sync ${Date.now()}`;
    const call = await createCall(owner, title, [idOf('alice')]);
    const link = await owner.put(`/calls/${call.id}/guest-link`, { enabled: true });

    const olivia = await as('owner');
    const alice = await as('alice');
    await joinCall(olivia.page, call.id);
    await joinCall(alice.page, call.id);
    await olivia.page.getByRole('button', { name: 'Turn your microphone on' }).click();
    await alice.page.getByRole('button', { name: 'Turn your microphone on' }).click();

    // A guest is in it too, and sees the same banner.
    const guest = await as('sam', { signedOut: true });
    await guest.page.goto(link.directPath as string);
    await guest.page.getByLabel('Your name').fill('Pat Partner');
    await guest.page.getByRole('button', { name: 'Join the meeting' }).click();
    await expect(guest.page.getByRole('button', { name: 'Leave' })).toBeVisible({ timeout: 30_000 });
    await expect(guest.page.getByRole('button', { name: 'Pause notes' })).toHaveCount(0);

    for (const page of [olivia.page, alice.page]) {
      await expect(banner(page)).toHaveText('Notes are being taken. Everybody here gets the minutes when the call ends.', { timeout: 30_000 });
    }
    await expect(banner(guest.page)).toContainText('Notes are being taken, including what you say', { timeout: 30_000 });
    await expect.poll(() => heard.length, { timeout: 60_000 }).toBeGreaterThan(0);
    await olivia.page.getByTestId('meeting-chat-button').click();
    await say(olivia.page, 'The release notes are in the drive');

    // Alice, not the host, pauses them. Everybody sees who did, at once.
    await alice.page.getByRole('button', { name: 'Pause notes' }).click();
    for (const page of [olivia.page, alice.page, guest.page]) {
      await expect(banner(page)).toHaveText(PAUSED, { timeout: 10_000 });
      await expect(banner(page)).toHaveAttribute('data-state', 'paused');
    }
    // The note-taker's own tile says so too.
    await expect(olivia.page.getByText('Notes, paused', { exact: true })).toBeVisible();
    await olivia.page.screenshot({ path: 'test-results/notes-paused.png' });
    // On a phone, in the dark, the banner and the button still fit.
    await alice.page.setViewportSize({ width: 390, height: 844 });
    await alice.page.evaluate(() => document.documentElement.classList.add('dark'));
    await expect(alice.page.getByRole('button', { name: 'Resume notes' })).toBeInViewport();
    await alice.page.screenshot({ path: 'test-results/notes-paused-phone-dark.png' });
    await say(olivia.page, 'Off the record: the budget is tight');

    // What was being said at the pause may still arrive; after that, nothing
    // more is sent while the microphones go on beeping.
    await olivia.page.waitForTimeout(3_000);
    const before = heard.length;
    await olivia.page.waitForTimeout(12_000);
    expect(heard.length, 'clips sent to speech to text while the notes were paused').toBe(before);

    // Olivia resumes them, and they are heard again.
    await olivia.page.getByRole('button', { name: 'Resume notes' }).click();
    for (const page of [olivia.page, alice.page]) {
      await expect(banner(page)).toHaveText('Notes are being taken. Everybody here gets the minutes when the call ends.', { timeout: 10_000 });
    }
    await expect(alice.page.getByRole('button', { name: 'Pause notes' })).toBeVisible();
    await expect.poll(() => heard.length, { timeout: 60_000 }).toBeGreaterThan(before);
    await say(olivia.page, 'Back on: we ship on Friday');

    await guest.page.getByRole('button', { name: 'Leave' }).click();
    await leaveCall(olivia.page);
    await leaveCall(alice.page);

    // Once the room has been empty a while, the minutes are written from the
    // recorded parts, with the gap said plainly.
    await expect.poll(() => asked.length, { timeout: 120_000, intervals: [2_000] }).toBeGreaterThan(0);
    const prompt = asked[asked.length - 1];
    expect(prompt).toContain('The release notes are in the drive');
    expect(prompt).toContain('Back on: we ship on Friday');
    expect(prompt).toMatch(/TeamUp: Notes were paused from \d\d:\d\d to \d\d:\d\d by Alice Archer\./);
    expect(prompt).not.toContain('Off the record');

    await olivia.page.goto(`/calls/${call.id}/minutes`);
    await expect(olivia.page.getByTestId('minutes')).toContainText('They talked about the release.', { timeout: 30_000 });
    await expect(olivia.page.getByTestId('minutes-pauses')).toContainText(/Notes were paused from .+ to .+ by Alice Archer\./);
  });

  test('a call made with notes off has no note-taker until somebody starts them', async ({ as }) => {
    test.setTimeout(180_000);
    const olivia = await as('owner');
    await olivia.page.goto('/calls');
    await olivia.page.getByRole('button', { name: 'New Call' }).click();
    const dialog = olivia.page.getByRole('dialog', { name: 'Start a call' });
    await dialog.getByLabel('Title (optional)').fill(`Quiet chat ${Date.now()}`);
    await dialog.getByText('Alice Archer', { exact: true }).click();
    const takeNotes = dialog.getByRole('switch', { name: /Take notes/ });
    // Every call, as the organization says, until changed here.
    await expect(takeNotes).toBeChecked();
    await takeNotes.click();
    await expect(takeNotes).not.toBeChecked();
    await dialog.screenshot({ path: 'test-results/notes-switch.png' });
    await dialog.getByRole('button', { name: 'Start call' }).click();
    await expect(olivia.page.getByRole('button', { name: 'Leave the call' })).toBeVisible({ timeout: 30_000 });
    const callId = new URL(olivia.page.url()).pathname.split('/').pop()!;

    const alice = await as('alice');
    await joinCall(alice.page, callId);
    await expect(olivia.page.getByText('Alice Archer', { exact: true })).toBeVisible({ timeout: 20_000 });

    // Two people, and still no note-taker, and nothing said about notes.
    await alice.page.waitForTimeout(6_000);
    for (const page of [olivia.page, alice.page]) {
      await expect(page.getByText('Notes, taking the minutes')).toHaveCount(0);
      await expect(banner(page)).toHaveCount(0);
    }

    // Alice starts them: the note-taker comes in, and everybody is told.
    await alice.page.getByRole('button', { name: 'Start notes' }).click();
    for (const page of [olivia.page, alice.page]) {
      await expect(banner(page)).toHaveText('Notes are being taken. Everybody here gets the minutes when the call ends.', { timeout: 30_000 });
      await expect(page.getByText('Notes, taking the minutes')).toBeVisible();
    }
    await expect(olivia.page.getByRole('button', { name: 'Pause notes' })).toBeVisible();
    await leaveCall(olivia.page);
    await leaveCall(alice.page);
  });

  test('the note-taker is not a person: not counted, not announced, and alone with it is alone', async ({ as }) => {
    test.setTimeout(240_000);
    const owner = await apiAs('owner');
    const call = await createCall(owner, `Two and Notes ${Date.now()}`, [idOf('alice')]);
    const olivia = await as('owner');
    const alice = await as('alice');
    await joinCall(olivia.page, call.id);
    await joinCall(alice.page, call.id);
    await expect(banner(olivia.page)).toBeVisible({ timeout: 30_000 });
    await expect(olivia.page.getByText('Notes, taking the minutes')).toBeVisible();

    // Two people and the note-taker: two in the call, and nobody "joined" when it came in.
    for (const page of [olivia.page, alice.page]) {
      await expect(page.getByText(/In a call · 2$/)).toBeVisible();
      await expect(page.getByText('Notes joined the call')).toHaveCount(0);
    }

    // Alice leaves. Olivia, alone with the note-taker, is alone: asked whether
    // she is still there, and the call ends when she goes.
    await leaveCall(alice.page);
    await expect(olivia.page.getByText('Waiting for others')).toBeVisible({ timeout: 15_000 });
    const still = olivia.page.getByRole('dialog', { name: 'Still there?' });
    await expect(still).toBeVisible({ timeout: 120_000 });
    await still.getByRole('button', { name: 'Leave the call' }).click();
    await expect.poll(async () => (await owner.get(`/calls/${call.id}`)).status, { timeout: 30_000 }).toBe('ended');
  });
});
