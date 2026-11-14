import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { expect, test } from './support/fixtures';
import { apiAs, createCall, idOf, joinCall, leaveCall } from './support/meetings';
import { readState } from './support/people';

// The meeting note-taker in a real call on the real LiveKit: it joins visibly,
// sends what people say to speech to text, and when the call ends writes the
// minutes and sends them to everybody who was there. Sarvam and the model are
// replaced by a local stand-in that answers as they do; Chromium's fake
// microphone is the speech.

let standIn: Server;
let standInUrl = '';
const heard: string[] = [];
// The model is down for the first attempt, as NVIDIA was for the first real meeting.
let modelDown = true;

test.describe('meeting notes', () => {
  test.beforeAll(async () => {
    standIn = createServer((req, res) => {
      let body = '';
      req.on('data', (c) => (body += c));
      req.on('end', () => {
        res.setHeader('Content-Type', 'application/json');
        if (req.url === '/speech-to-text') {
          heard.push(String(req.headers['api-subscription-key']));
          res.end(JSON.stringify({ transcript: 'We will ship the release on Friday.' }));
        } else if (req.url === '/v1/messages' && modelDown) {
          res.statusCode = 503;
          res.end(JSON.stringify({ error: { message: 'model overloaded' } }));
        } else if (req.url === '/v1/messages') {
          const asked = JSON.parse(body).messages[0].content as string;
          const ok = asked.includes('We will ship the release on Friday.');
          res.end(
            JSON.stringify({
              content: [
                {
                  type: 'text',
                  text: JSON.stringify({
                    summary: ok ? 'The team agreed to ship the release on Friday.' : 'NO TRANSCRIPT',
                    decisions: ['Ship on Friday'],
                    actionItems: [{ owner: 'Olivia Owner', task: 'Tag the release', due: 'Friday' }],
                    openQuestions: [],
                  }),
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
  });

  test.afterAll(async () => {
    // Later specs' calls must not have a note-taker in them.
    const owner = await apiAs('owner');
    await owner.ctx.put('/api/org/notetaker', { data: { enabled: false, llmProvider: 'anthropic' } });
    await new Promise((resolve) => standIn.close(resolve));
  });

  test('an administrator turns it on; it joins the call visibly, and everybody who was there gets the minutes', async ({ as }) => {
    test.setTimeout(240_000);
    const olivia = await as('owner');
    await olivia.page.goto('/organization');
    await olivia.page.getByRole('tab', { name: 'Meeting notes' }).click();
    await olivia.page.getByLabel('Sarvam API key').fill('sk-sarvam-test');
    await olivia.page.getByLabel('API key', { exact: true }).fill('sk-ant-test');
    await olivia.page.getByRole('button', { name: 'Save' }).click();
    // Pointed at the stand-in through the API, as the page offers no address for Sarvam or Claude.
    // A saved key does not follow a changed address by itself, so the keys go with it.
    const owner = await apiAs('owner');
    await owner.ctx.put('/api/org/notetaker', {
      data: {
        enabled: false,
        sttKey: 'sk-sarvam-test',
        sttBaseUrl: standInUrl,
        llmProvider: 'anthropic',
        llmKey: 'sk-ant-test',
        llmBaseUrl: standInUrl,
      },
    });
    await olivia.page.reload();
    await olivia.page.getByRole('tab', { name: 'Meeting notes' }).click();
    await expect(olivia.page.getByLabel('Sarvam API key')).toHaveAttribute('placeholder', /Saved/);
    await olivia.page.getByRole('switch').click();
    await expect(olivia.page.getByTestId('notetaker-state')).toHaveText('On, and ready.');

    // A member cannot turn it off.
    const alice = await apiAs('alice');
    expect(await alice.status('put', '/org/notetaker', { enabled: false })).toBe(403);

    const title = `Release sync ${Date.now()}`;
    const call = await createCall(owner, title, [idOf('alice')]);
    const aliceSession = await as('alice');
    await joinCall(olivia.page, call.id);
    await joinCall(aliceSession.page, call.id);
    await olivia.page.getByRole('button', { name: 'Turn your microphone on' }).click();
    await aliceSession.page.getByRole('button', { name: 'Turn your microphone on' }).click();

    // It is in the call, for everybody to see.
    for (const page of [olivia.page, aliceSession.page]) {
      await expect(page.getByTestId('notes-banner')).toBeVisible({ timeout: 30_000 });
      await expect(page.getByText('Notes, taking the minutes')).toBeVisible();
    }

    // The fake microphone beeps, which LiveKit hears as speaking.
    await expect.poll(() => heard.length, { timeout: 60_000 }).toBeGreaterThan(0);
    expect(heard.every((k) => k === 'sk-sarvam-test')).toBe(true);

    await leaveCall(olivia.page);
    await leaveCall(aliceSession.page);

    // After the room has been empty for a little while, the minutes are
    // written; the model is down, so everybody who was there is told they
    // could not be.
    const minutesPath = `/calls/${call.id}/minutes`;
    const told = (type: string) => async () => {
      const list = (await alice.get('/notifications')).notifications as Array<{ type: string; data?: { url?: string } }>;
      return list.some((n) => n.type === type && n.data?.url === minutesPath);
    };
    // 30 s for the room to stay empty, then three asks with two pauses of
    // 30 s give or take a quarter: up to about 110 s.
    await expect.poll(told('minutes.failed'), { timeout: 150_000, intervals: [3_000] }).toBe(true);

    // Alice finds the meeting on the Meeting notes page.
    const page = aliceSession.page;
    await page.goto('/chat');
    await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Meeting notes' }).click();
    await expect(page.getByRole('heading', { name: 'Meeting notes', level: 1 })).toBeVisible();
    const row = page.getByTestId('meeting-note').filter({ hasText: title });
    await expect(row).toContainText('Not written');
    await expect(row).toContainText('Olivia Owner');
    await row.click();
    await expect(page.getByTestId('minutes-state')).toContainText('The minutes could not be written.');
    await expect(page.getByTestId('minutes-state')).toContainText('503');
    // What was said is kept, and readable already.
    await page.getByRole('button', { name: 'Show the transcript' }).click();
    await expect(page.getByRole('region', { name: 'Transcript' })).toContainText('We will ship the release on Friday.');

    // The model is back; Alice has them written again.
    modelDown = false;
    await page.getByRole('button', { name: 'Write the minutes again' }).click();
    await expect.poll(told('minutes.ready'), { timeout: 60_000, intervals: [2_000] }).toBe(true);
    const minutes = aliceSession.page.getByTestId('minutes');
    await expect(minutes).toContainText('The team agreed to ship the release on Friday.');
    await expect(minutes).toContainText('Ship on Friday');
    await expect(minutes).toContainText('Tag the release');
    // The transcript she opened before stays open.
    await expect(page.getByRole('button', { name: 'Hide the transcript' })).toBeVisible();
    await expect(page.getByRole('region', { name: 'Transcript' })).toContainText('We will ship the release on Friday.');

    // Asked for: minutes that can be shared, in TeamUp and with other apps.
    // Copied, they carry HTML for a document or an email and *marks* for chat apps.
    await page.context().grantPermissions(['clipboard-read', 'clipboard-write']);
    await page.getByRole('button', { name: 'Copy', exact: true }).click();
    // Exactly, because the toast is also read out by a live region that repeats its words.
    await expect(page.getByText('Copied. Paste into a document, an email or another app.', { exact: true })).toBeVisible();
    const copied = await page.evaluate(async () => {
      const [item] = await navigator.clipboard.read();
      return {
        html: await (await item.getType('text/html')).text(),
        text: await (await item.getType('text/plain')).text(),
      };
    });
    expect(copied.html).toContain('<strong>Decisions</strong>');
    expect(copied.html).toContain('<li>Ship on Friday</li>');
    expect(copied.text).toContain(`*Minutes: ${title}*`);
    expect(copied.text).toContain('- Ship on Friday');
    expect(copied.text).toContain(`/calls/${call.id}/minutes`);

    // Sent to a conversation, they arrive there as a formatted message.
    await page.getByRole('button', { name: 'Send to a conversation' }).click();
    const sendTo = page.getByRole('dialog', { name: 'Send the minutes' });
    await sendTo.getByLabel('Find a conversation').fill('e2e-room');
    await sendTo.getByRole('option', { name: 'e2e-room' }).click();
    await sendTo.getByRole('button', { name: 'Send', exact: true }).click();
    await expect(page.getByText('Sent the minutes.', { exact: true })).toBeVisible();
    const pat = await as('pat');
    await pat.page.goto(`/chat/${readState().channelId}`);
    const posted = pat.page.getByTestId('message').filter({ hasText: `Minutes: ${title}` }).last();
    await expect(posted.locator('strong', { hasText: `Minutes: ${title}` })).toBeVisible();
    await expect(posted.locator('li', { hasText: 'Ship on Friday' })).toBeVisible();
    await posted.screenshot({ path: 'test-results/minutes-in-chat.png' });
    await page.screenshot({ path: 'test-results/minutes-share.png' });
    await pat.page.close();

    // Back on the list, with the summary, and found by searching for it.
    await page.getByRole('link', { name: 'Meeting notes' }).first().click();
    await page.getByLabel('Search meeting notes').fill('release on friday');
    await expect(page.getByTestId('meeting-note').filter({ hasText: title })).toContainText(
      'The team agreed to ship the release on Friday.'
    );
    await expect(page.getByTestId('meeting-note').filter({ hasText: title })).toContainText('1 action item');
    await page.getByLabel('Search meeting notes').fill('nothing like this was said');
    await expect(page.getByTestId('notes-empty')).toContainText('No meeting notes match');

    // Somebody who was not there cannot read them, nor see them listed.
    const sam = await apiAs('sam');
    expect(await sam.status('get', `/calls/${call.id}/minutes`)).toBe(404);
    const samList = (await sam.get('/meeting-notes?scope=all')).items as Array<{ callId: string }>;
    expect(samList.some((i) => i.callId === call.id)).toBe(false);
  });
});
