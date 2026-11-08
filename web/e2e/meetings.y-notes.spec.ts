import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { expect, test } from './support/fixtures';
import { apiAs, createCall, idOf, joinCall, leaveCall } from './support/meetings';

// The meeting note-taker in a real call on the real LiveKit: it joins visibly,
// sends what people say to speech to text, and when the call ends writes the
// minutes and sends them to everybody who was there. Sarvam and the model are
// replaced by a local stand-in that answers as they do; Chromium's fake
// microphone is the speech.

let standIn: Server;
let standInUrl = '';
const heard: string[] = [];

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
    test.setTimeout(180_000);
    const olivia = await as('owner');
    await olivia.page.goto('/organization');
    await olivia.page.getByRole('tab', { name: 'Meeting notes' }).click();
    await olivia.page.getByLabel('Sarvam API key').fill('sk-sarvam-test');
    await olivia.page.getByLabel('API key', { exact: true }).fill('sk-ant-test');
    await olivia.page.getByRole('button', { name: 'Save' }).click();
    // Pointed at the stand-in through the API, as the page offers no address for Sarvam or Claude.
    const owner = await apiAs('owner');
    await owner.ctx.put('/api/org/notetaker', {
      data: { enabled: false, sttBaseUrl: standInUrl, llmProvider: 'anthropic', llmBaseUrl: standInUrl },
    });
    await olivia.page.reload();
    await olivia.page.getByRole('tab', { name: 'Meeting notes' }).click();
    await expect(olivia.page.getByLabel('Sarvam API key')).toHaveAttribute('placeholder', /Saved/);
    await olivia.page.getByRole('switch').click();
    await expect(olivia.page.getByTestId('notetaker-state')).toHaveText('On, and ready.');

    // A member cannot turn it off.
    const alice = await apiAs('alice');
    expect(await alice.status('put', '/org/notetaker', { enabled: false })).toBe(403);

    const call = await createCall(owner, `Release sync ${Date.now()}`, [idOf('alice')]);
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

    // After the room has been empty for a little while, the minutes go out.
    const minutesPath = `/calls/${call.id}/minutes`;
    await expect
      .poll(
        async () => {
          const list = (await alice.get('/notifications')).notifications as Array<{ type: string; data?: { url?: string } }>;
          return list.some((n) => n.type === 'minutes.ready' && n.data?.url === minutesPath);
        },
        { timeout: 90_000, intervals: [3_000] }
      )
      .toBe(true);

    await aliceSession.page.goto(minutesPath);
    const minutes = aliceSession.page.getByTestId('minutes');
    await expect(minutes).toContainText('The team agreed to ship the release on Friday.');
    await expect(minutes).toContainText('Ship on Friday');
    await expect(minutes).toContainText('Tag the release');
    await aliceSession.page.getByRole('button', { name: 'Show the transcript' }).click();
    await expect(aliceSession.page.getByRole('region', { name: 'Transcript' })).toContainText('We will ship the release on Friday.');

    // Somebody who was not there cannot read them.
    const sam = await apiAs('sam');
    expect(await sam.status('get', `/calls/${call.id}/minutes`)).toBe(404);
  });
});
