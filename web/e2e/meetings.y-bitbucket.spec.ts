import { expect, test } from './support/fixtures';
import { apiAs, idOf } from './support/meetings';
import { PEOPLE } from './support/people';
import { ACCOUNTS, fakeBitbucket, sendBitbucket } from './support/bitbucket';

// Reported: some of Cognecto's code is on Bitbucket, which TeamUp knew nothing of.
test('Bitbucket: connected by the workspace owner, then pull requests move tasks and reach reviewers', async ({ as, request }) => {
  test.setTimeout(150_000);
  const bitbucket = await fakeBitbucket();
  const owner = await apiAs('owner');
  const key = `B${Date.now().toString(36).slice(-5).toUpperCase().replace(/^[0-9]/, 'Q')}`;
  let taskId: string | undefined;
  try {
    const olivia = await as('owner');
    const page = olivia.page;
    await page.goto('/organization');
    await page.getByRole('tab', { name: 'Integrations' }).click();
    await page.getByLabel('1. Your workspace').fill('cognecto');
    await expect(page.getByRole('button', { name: 'Copy callback URL' })).toBeVisible();
    await expect(page.getByRole('link', { name: /Add consumer/ })).toHaveAttribute('href', /bitbucket\.org\/cognecto\/workspace\/settings\/oauth-consumers\/new/);
    await page.screenshot({ path: 'test-results/bitbucket-setup.png', fullPage: true });
    await page.getByLabel('Key').fill('e2e-key');
    await page.getByLabel('Secret').fill('e2e-secret');
    bitbucket.signInAs('owner-code');
    await page.getByRole('button', { name: 'Connect Bitbucket' }).click();
    await expect(page.getByText('Bitbucket is connected.', { exact: false }).first()).toBeVisible({ timeout: 20_000 });
    await expect(page.getByText('Connected to cognecto, seeing 1 repository.')).toBeVisible();
    expect(bitbucket.hook()?.url).toMatch(/\/api\/integrations\/bitbucket\/hook\/[0-9a-f]+$/);

    // Cortex is on Bitbucket.
    await page.getByRole('tab', { name: 'Projects' }).click();
    await page.getByRole('button', { name: 'New project' }).click();
    const dialog = page.getByRole('dialog');
    await dialog.getByLabel('Name').fill(`Cortex ${key}`);
    await dialog.getByLabel('Key').fill(key);
    await dialog.getByRole('button', { name: 'Create project' }).click();
    await expect(dialog).toBeHidden();
    const card = page.locator('li', { has: page.getByRole('heading', { name: new RegExp(`Cortex ${key}`) }) });
    await card.getByRole('button', { name: 'Repositories' }).click();
    await page.getByRole('dialog').getByRole('checkbox', { name: /cognecto\/cortex/ }).check();
    await page.getByRole('dialog').getByRole('button', { name: 'Save' }).click();
    await expect(card.getByRole('link', { name: 'cognecto/cortex' })).toBeVisible();

    // Alice and Sam link their Bitbucket accounts.
    for (const [person, code] of [['alice', 'alice-code'], ['sam', 'sam-code']] as const) {
      bitbucket.signInAs(code);
      const s = await as(person);
      await s.page.goto('/settings');
      await s.page.getByRole('button', { name: 'Link Bitbucket' }).click();
      await expect(s.page.getByText(`Linked as @${ACCOUNTS[code].nickname}`)).toBeVisible({ timeout: 20_000 });
      if (person === 'alice') await s.page.screenshot({ path: 'test-results/bitbucket-linked.png' });
    }

    const projects = (await owner.get('/projects')).projects as Array<{ id: string; key: string }>;
    const project = projects.find((p) => p.key === key)!;
    const task = await owner.post('/tasks', { title: 'Better ranking', projectId: project.id, assigneeId: idOf('alice') });
    taskId = task.id;

    const hook = bitbucket.hook();
    const send = (event: string, payload: unknown) => sendBitbucket(request, hook, event, payload);
    const repo = { uuid: '{cortex}', full_name: 'cognecto/cortex', is_private: true, links: { html: { href: 'https://bitbucket.org/cognecto/cortex' } } };
    const pull = (state: string) => ({
      id: 9, title: `${key}-1 Better ranking`, state, author: ACCOUNTS['alice-code'], reviewers: [ACCOUNTS['sam-code']],
      source: { branch: { name: 'feature/ranking' } }, destination: { branch: { name: 'main' } },
      links: { html: { href: 'https://bitbucket.org/cognecto/cortex/pull-requests/9' } },
      created_on: new Date().toISOString(), updated_on: new Date().toISOString(),
    });
    await send('pullrequest:created', { actor: ACCOUNTS['alice-code'], pullrequest: pull('OPEN'), repository: repo });

    const sam = await apiAs('sam');
    await expect
      .poll(async () => ((await sam.get('/notifications?limit=20')).notifications as any[]).some((n) => n.type === 'code.review_requested' && n.data?.taskId === taskId))
      .toBe(true);
    await sam.dispose();

    await page.goto(`/tasks/${taskId}`);
    const prs = page.getByRole('list', { name: 'Pull requests' });
    await expect(prs.getByRole('link', { name: `${key}-1 Better ranking` })).toHaveAttribute('href', /bitbucket\.org\/cognecto\/cortex\/pull-requests\/9/);
    await expect(prs.getByText(PEOPLE.alice.name)).toBeVisible();
    await expect(page.getByRole('combobox').filter({ hasText: /in review/i }).first()).toBeVisible();

    await send('pullrequest:approved', { actor: ACCOUNTS['sam-code'], approval: { user: ACCOUNTS['sam-code'] }, pullrequest: pull('OPEN'), repository: repo });
    await send('pullrequest:fulfilled', { actor: ACCOUNTS['sam-code'], pullrequest: pull('MERGED'), repository: repo });
    await page.reload();
    await expect(page.getByRole('combobox').filter({ hasText: /^done$/i }).first()).toBeVisible();
    await expect(prs.getByRole('list', { name: 'Reviewers' }).getByText('approved')).toBeVisible();
    await expect(page.getByText('Bitbucket', { exact: false }).first()).toBeVisible();
    await page.screenshot({ path: 'test-results/bitbucket-task.png' });

    // Disconnecting takes the webhook off the workspace.
    await page.goto('/organization?tab=integrations');
    const bbCard = page.locator('div.rounded-lg', { has: page.getByText('Bitbucket', { exact: true }) }).last();
    await bbCard.getByRole('button', { name: 'Disconnect' }).click();
    await bbCard.getByRole('button', { name: 'Disconnect' }).click();
    await expect(page.getByText('Bitbucket disconnected', { exact: false }).first()).toBeVisible();
    expect(bitbucket.hook()).toBeUndefined();
  } finally {
    if (taskId) await owner.del(`/tasks/${taskId}`).catch(() => {});
    await owner.del('/integrations/bitbucket').catch(() => {});
    const projects = ((await owner.get('/projects').catch(() => ({ projects: [] }))).projects ?? []) as Array<{ id: string; key: string }>;
    for (const p of projects.filter((p) => p.key === key)) await owner.del(`/projects/${p.id}`).catch(() => {});
    await owner.dispose();
    await bitbucket.close();
  }
});
