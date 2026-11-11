import { createHmac, generateKeyPairSync } from 'node:crypto';
import { createServer, type IncomingMessage, type Server } from 'node:http';
import { expect, test } from './support/fixtures';
import { apiAs, idOf } from './support/meetings';
import { PEOPLE } from './support/people';

const PORT = Number(process.env.E2E_GITHUB_PORT ?? 18092);
const BASE = `http://127.0.0.1:${PORT}`;
const WEBHOOK_SECRET = 'e2e-webhook-secret';

/** Who signs in on GitHub's page, by the code it hands back. */
const ACCOUNTS: Record<string, { id: number; login: string; emails: string[] }> = {
  'alice-code': { id: 501, login: 'alice-gh', emails: ['alice.personal@example.com'] },
  'sam-code': { id: 502, login: 'sam-gh', emails: [] },
};

/**
 * GitHub, as far as TeamUp and a browser can tell: the page where an admin
 * confirms making the app, installing it, the sign-in that links an account,
 * and the API calls TeamUp makes. The server under test is pointed here by
 * TEAMUP_GITHUB_WEB_URL and TEAMUP_GITHUB_API_URL (run.sh).
 */
async function fakeGitHub() {
  const { privateKey } = generateKeyPairSync('rsa', {
    modulusLength: 2048,
    privateKeyEncoding: { type: 'pkcs1', format: 'pem' },
    publicKeyEncoding: { type: 'spki', format: 'pem' },
  });
  const app: { manifest?: any } = {};
  const collaborators: string[] = [];
  // What people may do with every repository already, by GitHub login.
  const holds: Record<string, string> = {};
  let nextUser = 'alice-code';
  let personalCode = 'made';
  const read = (req: IncomingMessage) =>
    new Promise<string>((done) => {
      let body = '';
      req.on('data', (c) => (body += c));
      req.on('end', () => done(body));
    });

  const server: Server = createServer(async (req, res) => {
    const url = new URL(req.url ?? '/', BASE);
    const body = await read(req);
    const json = (value: unknown) => res.writeHead(200, { 'Content-Type': 'application/json' }).end(JSON.stringify(value));
    const go = (to: string) => res.writeHead(302, { Location: to }).end();

    // What somebody may do with a repository already.
    const permission = url.pathname.match(/^\/repos\/[^/]+\/[^/]+\/collaborators\/([^/]+)\/permission$/);
    if (permission) return json({ permission: holds[permission[1]] ?? 'none' });
    // Giving and taking away a repository: somebody outside the organization is invited.
    const collab = url.pathname.match(/^\/repos\/([^/]+)\/([^/]+)\/collaborators\/([^/]+)$/);
    if (collab) {
      collaborators.push(`${req.method} ${collab[1]}/${collab[2]} ${collab[3]}${body ? ' ' + JSON.parse(body).permission : ''}`);
      if (req.method === 'PUT') return res.writeHead(201, { 'Content-Type': 'application/json' }).end(JSON.stringify({ id: 7 }));
      return res.writeHead(204).end();
    }
    // Whose name it is: an organization, or somebody's own account.
    if (url.pathname === '/users/cognecto') return json({ id: 1, login: 'cognecto', type: 'Organization' });
    if (url.pathname === '/users/solo-dev') return json({ id: 2, login: 'solo-dev', type: 'User' });
    // The admin presses Create on GitHub's page. A personal account's page
    // has been seen to come back without the state.
    if (/^\/organizations\/[^/]+\/settings\/apps\/new$/.test(url.pathname) && req.method === 'POST') {
      app.manifest = JSON.parse(new URLSearchParams(body).get('manifest') ?? '{}');
      return go(`${app.manifest.redirect_url}?code=made&state=${encodeURIComponent(url.searchParams.get('state') ?? '')}`);
    }
    if (url.pathname === '/settings/apps/new' && req.method === 'POST') {
      app.manifest = JSON.parse(new URLSearchParams(body).get('manifest') ?? '{}');
      return go(`${app.manifest.redirect_url}?code=${personalCode}`);
    }
    if (url.pathname === '/app-manifests/made/conversions') {
      return json({ id: 9001, slug: 'teamup-e2e', client_id: 'Iv1.e2e', client_secret: 'e2e-client-secret', webhook_secret: WEBHOOK_SECRET, pem: privateKey, owner: { login: 'cognecto' } });
    }
    // ...and Install, choosing its repositories.
    if (url.pathname === '/apps/teamup-e2e/installations/new') return go(`${app.manifest.setup_url}?installation_id=77&setup_action=install`);
    if (url.pathname === '/app/installations/77') return json({ id: 77, app_id: 9001, account: { login: 'cognecto' } });
    if (url.pathname === '/app/installations/77/access_tokens') return json({ token: 'installation-token', expires_at: '2099-01-01T00:00:00Z' });
    if (url.pathname === '/installation/repositories') {
      return json({
        repositories: [
          { id: 11, full_name: 'cognecto/photon-web', html_url: 'https://github.com/cognecto/photon-web', private: true },
          { id: 12, full_name: 'cognecto/photon-api', html_url: 'https://github.com/cognecto/photon-api', private: true },
        ],
      });
    }
    // Somebody signs in to link their account.
    if (url.pathname === '/login/oauth/authorize') {
      return go(`${url.searchParams.get('redirect_uri')}?code=${nextUser}&state=${encodeURIComponent(url.searchParams.get('state') ?? '')}`);
    }
    if (url.pathname === '/login/oauth/access_token') return json({ access_token: `user-${new URLSearchParams(body).get('code')}` });
    const who = ACCOUNTS[(req.headers.authorization ?? '').replace('token user-', '')];
    if (url.pathname === '/user' && who) return json({ id: who.id, login: who.login });
    if (url.pathname === '/user/emails' && who) return json(who.emails.map((email) => ({ email, verified: true })));
    res.writeHead(404).end();
  });
  await new Promise<void>((done) => server.listen(PORT, '127.0.0.1', done));
  return {
    signInAs: (code: string) => (nextUser = code),
    personalCodeIs: (code: string) => (personalCode = code),
    collaborators,
    holds,
    hookUrl: () => app.manifest?.hook_attributes?.url as string,
    close: () => new Promise((done) => server.close(done)),
  };
}

// Reported: connecting a personal GitHub account made the app on GitHub, came
// back to TeamUp with an error, and the next try said the name was taken.
// A personal account goes to its own new-app page, and if GitHub's keys are
// lost all the same, the card says what to do on GitHub before trying again.
test('GitHub: a personal account goes to its own page, and a lost app says what to do', async ({ as }) => {
  const github = await fakeGitHub();
  try {
    github.personalCodeIs('already-used');
    const { page } = await as('owner');
    await page.goto('/organization');
    await page.getByRole('tab', { name: 'Integrations' }).click();
    await page.getByLabel('GitHub organization or account').fill('solo-dev');
    await page.getByRole('button', { name: 'Connect GitHub' }).click();
    const notice = page.getByRole('alert').filter({ hasText: 'GitHub made the app, but TeamUp could not take its keys.' });
    await expect(notice).toBeVisible({ timeout: 20_000 });
    await expect(notice.getByRole('link', { name: 'Your GitHub Apps' })).toHaveAttribute('href', 'https://github.com/settings/apps');
    // The address is tidied, but the notice stays, and Connect is offered again.
    await expect(page).not.toHaveURL(/github=/);
    await expect(page.getByRole('button', { name: 'Connect GitHub' })).toBeVisible();
    await page.screenshot({ path: 'test-results/github-lost-app.png' });
  } finally {
    await github.close();
  }
});

// Reported: work done on GitHub had to be copied onto tasks by hand, and
// nobody heard here that their review was wanted.
test('GitHub: connected once, then pull requests move tasks and reach reviewers', async ({ as, request }) => {
  test.setTimeout(150_000);
  const github = await fakeGitHub();
  const owner = await apiAs('owner');
  // Keys are never reused, so each run makes its own project.
  const key = `P${Date.now().toString(36).slice(-5).toUpperCase().replace(/^[0-9]/, 'Q')}`;
  let taskId: string | undefined;
  try {
    // The admin connects the organization's GitHub.
    const olivia = await as('owner');
    const page = olivia.page;
    await page.goto('/organization');
    await page.getByRole('tab', { name: 'Integrations' }).click();
    await page.getByLabel('GitHub organization').fill('cognecto');
    await page.getByRole('button', { name: 'Connect GitHub' }).click();
    await expect(page.getByText('GitHub is connected.', { exact: false }).first()).toBeVisible({ timeout: 20_000 });
    await expect(page.getByText('Connected to cognecto, seeing 2 repositories.')).toBeVisible();
    await page.screenshot({ path: 'test-results/github-connected.png' });

    // A project made of both repositories, keyed for its tasks.
    await page.getByRole('tab', { name: 'Projects' }).click();
    await page.getByRole('button', { name: 'New project' }).click();
    const dialog = page.getByRole('dialog');
    await dialog.getByLabel('Name').fill(`Photon ${key}`);
    await dialog.getByLabel('Key').fill(key);
    await dialog.getByRole('button', { name: 'Create project' }).click();
    await expect(dialog).toBeHidden();
    const card = page.locator('li', { has: page.getByRole('heading', { name: new RegExp(`Photon ${key}`) }) });
    await card.getByRole('button', { name: 'Repositories' }).click();
    await page.getByRole('dialog').getByRole('checkbox', { name: /photon-web/ }).check();
    await page.getByRole('dialog').getByRole('checkbox', { name: /photon-api/ }).check();
    await page.getByRole('dialog').getByRole('button', { name: 'Save' }).click();
    await expect(card.getByRole('link', { name: 'cognecto/photon-api' })).toBeVisible();
    await page.screenshot({ path: 'test-results/github-projects.png' });

    // Alice and Sam link the GitHub accounts they work with.
    for (const [person, code, login] of [['alice', 'alice-code', 'alice-gh'], ['sam', 'sam-code', 'sam-gh']] as const) {
      github.signInAs(code);
      const s = await as(person);
      await s.page.goto('/settings');
      await s.page.getByRole('button', { name: 'Link GitHub' }).click();
      await expect(s.page.getByText(`Linked as @${login}`)).toBeVisible({ timeout: 20_000 });
      if (person === 'alice') await s.page.screenshot({ path: 'test-results/github-linked.png' });
    }

    // A task in the project.
    const projects = (await owner.get('/projects')).projects as Array<{ id: string; key: string }>;
    const project = projects.find((p) => p.key === key)!;
    const task = await owner.post('/tasks', { title: 'Fix sign-in on Safari', projectId: project.id, assigneeId: idOf('alice') });
    taskId = task.id;
    expect(task.key).toBe(`${key}-1`);

    // GitHub tells TeamUp a pull request was opened on the task's branch, with Sam to review.
    const send = async (event: string, delivery: string, payload: unknown) => {
      const raw = JSON.stringify(payload);
      const signature = 'sha256=' + createHmac('sha256', WEBHOOK_SECRET).update(raw).digest('hex');
      const res = await request.post(github.hookUrl().replace(/^https?:\/\/[^/]+/, ''), {
        data: raw,
        headers: { 'Content-Type': 'application/json', 'X-GitHub-Event': event, 'X-GitHub-Delivery': delivery, 'X-Hub-Signature-256': signature },
      });
      expect(res.status(), `${event} accepted`).toBe(204);
    };
    const repo = { id: 11, full_name: 'cognecto/photon-web', html_url: 'https://github.com/cognecto/photon-web', private: true };
    const pull = (extra: Record<string, unknown> = {}) => ({
      number: 42, title: 'Safari sign-in', body: '', state: 'open', merged: false, html_url: 'https://github.com/cognecto/photon-web/pull/42',
      user: { login: 'alice-gh' }, head: { ref: `${key.toLowerCase()}-1-fix-sign-in` }, base: { ref: 'main' },
      requested_reviewers: [{ login: 'sam-gh' }], created_at: new Date().toISOString(), ...extra,
    });
    const stamp = Date.now();
    await send('pull_request', `open-${stamp}`, { action: 'opened', pull_request: pull(), repository: repo, sender: { login: 'alice-gh' } });

    // Sam is told here, with a way to the task.
    const sam = await apiAs('sam');
    await expect
      .poll(async () => ((await sam.get('/notifications?limit=20')).notifications as any[]).some((n) => n.type === 'code.review_requested' && n.data?.taskId === taskId))
      .toBe(true);
    await sam.dispose();

    // The task moved to review by itself, and shows the pull request.
    await page.goto(`/tasks/${taskId}`);
    await expect(page.getByTestId('task-key')).toHaveText(`${key}-1`);
    const prs = page.getByRole('list', { name: 'Pull requests' });
    await expect(prs.getByRole('link', { name: 'Safari sign-in' })).toHaveAttribute('href', 'https://github.com/cognecto/photon-web/pull/42');
    await expect(prs.getByText(PEOPLE.alice.name)).toBeVisible();
    await expect(prs.getByRole('list', { name: 'Reviewers' }).getByText('review requested')).toBeVisible();
    await expect(page.getByRole('combobox').filter({ hasText: /in review/i }).first()).toBeVisible();
    await page.screenshot({ path: 'test-results/github-task.png', fullPage: true });

    // Sam approves, the pull request is merged: the task is done, and its history says GitHub did it.
    await send('pull_request_review', `review-${stamp}`, { action: 'submitted', review: { state: 'approved', user: { login: 'sam-gh' } }, pull_request: pull(), repository: repo });
    await send('pull_request', `merge-${stamp}`, {
      action: 'closed', pull_request: pull({ state: 'closed', merged: true, merged_at: new Date().toISOString() }), repository: repo, sender: { login: 'sam-gh' },
    });
    await page.reload();
    await expect(page.getByRole('combobox').filter({ hasText: /^done$/i }).first()).toBeVisible();
    await expect(prs.getByText('Merged')).toBeVisible();
    await expect(prs.getByRole('list', { name: 'Reviewers' }).getByText('approved')).toBeVisible();

    // A key written in a comment opens the task.
    await page.getByRole('button', { name: 'Add Comment' }).click();
    await page.getByRole('textbox', { name: 'Comment' }).fill(`Follow-up to ${key.toLowerCase()}-1`);
    await page.getByRole('button', { name: 'Post comment' }).click();
    await page.getByRole('link', { name: `${key.toLowerCase()}-1` }).click();
    await expect(page).toHaveURL(new RegExp(`/tasks/${taskId}$`));

    // Alice asks for write access to the project; an administrator approves it, and GitHub invites her.
    const aliceSession = await as('alice');
    await aliceSession.page.goto('/projects');
    const aliceCard = aliceSession.page.locator('li', { has: aliceSession.page.getByRole('heading', { name: new RegExp(`Photon ${key}`) }) });
    await expect(aliceCard.getByTestId('my-access')).toHaveText('You have no access to its code');
    await aliceCard.getByRole('button', { name: 'Request access' }).click();
    const ask = aliceSession.page.getByRole('dialog');
    await ask.getByRole('button', { name: /Write/ }).click();
    await ask.getByLabel('Why (optional)').fill('Fixing the Safari sign-in');
    await aliceSession.page.screenshot({ path: 'test-results/access-request.png' });
    await ask.getByRole('button', { name: 'Send request' }).click();
    await expect(aliceCard.getByTestId('my-access')).toContainText('You asked for write access');

    const owner2 = await apiAs('owner');
    await expect
      .poll(async () => ((await owner2.get('/notifications?limit=20')).notifications as any[]).some((n) => n.type === 'access.requested'))
      .toBe(true);
    await owner2.dispose();
    await page.goto('/projects');
    const waiting = page.getByRole('list', { name: 'Access requests' });
    await expect(waiting.getByText('Fixing the Safari sign-in', { exact: false })).toBeVisible();
    await page.screenshot({ path: 'test-results/access-waiting.png' });
    await waiting.getByRole('button', { name: `Approve ${PEOPLE.alice.name}` }).click();
    await expect(page.getByText(`${PEOPLE.alice.name} has write access`, { exact: false }).first()).toBeVisible();
    expect(github.collaborators).toContain('PUT cognecto/photon-web alice-gh push');
    expect(github.collaborators).toContain('PUT cognecto/photon-api alice-gh push');

    await aliceSession.page.reload();
    await expect(aliceCard.getByTestId('my-access')).toContainText('You have write access (accept GitHub’s invitation to use it)');

    // ...and takes it away again; the history says so.
    await page.getByRole('button', { name: `Access to Photon ${key}` }).click();
    const accessDialog = page.getByRole('dialog', { name: new RegExp(`Access to Photon ${key}`) });
    await expect(accessDialog.getByRole('list', { name: `${PEOPLE.alice.name}'s repositories` }).getByText('invited on GitHub').first()).toBeVisible();
    await page.screenshot({ path: 'test-results/access-dialog.png' });
    await accessDialog.getByRole('button', { name: `Remove ${PEOPLE.alice.name}'s access` }).click();
    await accessDialog.getByRole('button', { name: 'Remove access' }).click();
    await expect(page.getByText(`${PEOPLE.alice.name}'s access is removed.`).first()).toBeVisible();
    expect(github.collaborators).toContain('DELETE cognecto/photon-web alice-gh');
    const history = accessDialog.getByRole('list', { name: 'Access history' });
    await expect(history.getByText('asked for', { exact: false })).toBeVisible();
    await expect(history.getByText(`${PEOPLE.alice.name}'s access was removed`, { exact: false })).toBeVisible();
    await page.keyboard.press('Escape');

    // Sam could push to both repositories on GitHub before TeamUp looked. An
    // admin checks, and Sam is shown as having it, without asking.
    github.holds['sam-gh'] = 'write';
    if (!(await accessDialog.isVisible())) await page.getByRole('button', { name: `Access to Photon ${key}` }).click();
    await accessDialog.getByRole('button', { name: 'Check now' }).click();
    await expect(page.getByText('Access on GitHub and Bitbucket checked.').first()).toBeVisible();
    const people = accessDialog.getByRole('list', { name: 'People with access' });
    await expect(people.getByText('already had it on GitHub, seen', { exact: false })).toBeVisible();
    await expect(accessDialog.getByRole('list', { name: `${PEOPLE.sam.name}'s repositories` }).getByText('already has access')).toHaveCount(2);
    await expect(history.getByText(`${PEOPLE.sam.name} already had write access to 2 of 2 repositories on GitHub or Bitbucket`)).toBeVisible();
    await page.screenshot({ path: 'test-results/access-found.png' });
    await page.keyboard.press('Escape');
    expect(github.collaborators.filter((c) => c.includes('sam-gh'))).toEqual([]);
    const samSession = await as('sam');
    await samSession.page.goto('/projects');
    const samCard = samSession.page.locator('li', { has: samSession.page.getByRole('heading', { name: new RegExp(`Photon ${key}`) }) });
    await expect(samCard.getByTestId('my-access')).toHaveText('You already have write access on GitHub');
    await expect(samCard.getByRole('button', { name: /Request access|Ask for write access/ })).toHaveCount(0);
    await samSession.page.screenshot({ path: 'test-results/access-found-member.png' });

    // A forged event is refused.
    const forged = await request.post(github.hookUrl().replace(/^https?:\/\/[^/]+/, ''), {
      data: '{}',
      headers: { 'Content-Type': 'application/json', 'X-GitHub-Event': 'pull_request', 'X-Hub-Signature-256': 'sha256=00' },
    });
    expect(forged.status()).toBe(401);
  } finally {
    if (taskId) await owner.del(`/tasks/${taskId}`).catch(() => {});
    await owner.del('/integrations/github').catch(() => {});
    const projects = ((await owner.get('/projects').catch(() => ({ projects: [] }))).projects ?? []) as Array<{ id: string; key: string }>;
    for (const p of projects.filter((p) => p.key === key)) await owner.del(`/projects/${p.id}`).catch(() => {});
    await owner.dispose();
    await github.close();
  }
});
