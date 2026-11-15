import { expect, test } from './support/fixtures';
import { apiAs } from './support/meetings';
import { ACCOUNTS, connectBitbucket, fakeBitbucket, linkBitbucket, sendBitbucket } from './support/bitbucket';

// Reported: research and bugs were filed with no project, and once they became a
// project's work a second task was made, so the story was split in two. A task
// now moves into its project, keeping what it was called, and starts its own
// branch for whoever may push to the repository; anybody else asks for access.
test('a task moves into its project and starts its branch, once its holder has write access', async ({ as, request }) => {
  test.setTimeout(180_000);
  const bitbucket = await fakeBitbucket();
  const owner = await apiAs('owner');
  const key = `T${Date.now().toString(36).slice(-5).toUpperCase().replace(/^[0-9]/, 'Q')}`;
  const projectName = `Cortex ${key}`;
  let taskId: string | undefined;
  try {
    // Cortex, on Bitbucket.
    const olivia = await as('owner');
    await connectBitbucket(olivia.page, bitbucket);
    const project = await owner.post('/projects', { name: projectName, key });
    const repos = (await owner.get('/integrations/repositories')).repositories as Array<{ id: string; fullName: string }>;
    await owner.put(`/projects/${project.id}/repositories`, { repositoryIds: [repos.find((r) => r.fullName === 'cognecto/cortex')!.id] });

    // Alice looks into something before anybody knows whose work it is.
    const alice = await as('alice');
    await linkBitbucket(alice.page, bitbucket, 'alice-code');
    const aliceApi = await apiAs('alice');
    const task = await aliceApi.post('/tasks', { title: 'Research slow search', assigneeId: aliceApi.userId });
    await aliceApi.dispose();
    taskId = task.id as string;
    const oldKey = task.key as string;
    expect(oldKey).toMatch(/^TASK-\d+$/);

    const page = alice.page;
    await page.goto(`/tasks/${taskId}`);
    await expect(page.getByText('Not in a project yet.', { exact: false })).toBeVisible();
    await page.getByRole('button', { name: 'Move to a project' }).click();
    const dialog = page.getByRole('dialog', { name: 'Move to a project' });
    await dialog.getByLabel('Project').selectOption({ label: `${projectName} (${key})` });
    await expect(dialog.getByText(`still links here`, { exact: false })).toBeVisible();
    await dialog.getByRole('button', { name: 'Move' }).click();
    await expect(dialog).toBeHidden();
    await expect(page.getByTestId('task-key')).toHaveText(`${key}-1`);
    await expect(page.getByText(`Moved to ${projectName} as ${key}-1. ${oldKey} still links here.`).first()).toBeVisible();

    // She may not push to Cortex yet, so she asks.
    const start = page.getByTestId('start-development');
    await expect(start.getByText(`You need write access to ${projectName} to start a branch.`)).toBeVisible();
    await expect(start.getByRole('button', { name: /^Create branch/ })).toHaveCount(0);
    await start.scrollIntoViewIfNeeded();
    await page.screenshot({ path: 'test-results/task-branch-ask.png' });
    await start.getByRole('button', { name: 'Ask for write access' }).click();
    await expect(start.getByText(`You asked for access to ${projectName}.`, { exact: false })).toBeVisible();

    // An administrator agrees, and Bitbucket gives her the repository.
    const pending = (await owner.get('/access/requests')).requests as Array<{ id: string; projectId: string; level: string; reason: string }>;
    const asked = pending.find((r) => r.projectId === project.id)!;
    expect(asked.level).toBe('write');
    expect(asked.reason).toBe(`To start the branch for ${key}-1`);
    await owner.post(`/access/requests/${asked.id}/approve`, { note: '' });

    await page.reload();
    const branch = `${key.toLowerCase()}-1-research-slow-search`;
    await page.getByTestId('start-development').getByRole('button', { name: `Create branch ${branch}` }).click();
    await expect(page.getByText(`Branch ${branch} is ready on cognecto/cortex.`).first()).toBeVisible();
    expect(bitbucket.branches).toContain(`cognecto/cortex ${branch} from hash-master`);
    // Its one repository has the branch now, so there is nothing more to start.
    await expect(page.getByTestId('start-development')).toHaveCount(0);
    const made = page.getByTestId('task-branch');
    await expect(made.getByRole('link', { name: branch })).toHaveAttribute('href', `https://bitbucket.org/cognecto/cortex/branch/${branch}`);
    await expect(made.getByText(`git fetch origin && git switch ${branch}`)).toBeVisible();
    await expect(made.getByText(/cognecto\/cortex, from master, by Alice/)).toBeVisible();
    // Starting the branch is starting the work.
    await expect(page.getByRole('combobox').filter({ hasText: /in progress/i }).first()).toBeVisible();

    // A pull request that names the key it had still lands on it.
    await sendBitbucket(request, bitbucket.hook(), 'pullrequest:created', {
      actor: ACCOUNTS['alice-code'],
      repository: { uuid: '{cortex}', full_name: 'cognecto/cortex', is_private: true, links: { html: { href: 'https://bitbucket.org/cognecto/cortex' } } },
      pullrequest: {
        id: 41, title: `${oldKey} Faster search index`, state: 'OPEN', author: ACCOUNTS['alice-code'], reviewers: [],
        source: { branch: { name: branch } }, destination: { branch: { name: 'master' } },
        links: { html: { href: 'https://bitbucket.org/cognecto/cortex/pull-requests/41' } },
        created_on: new Date().toISOString(), updated_on: new Date().toISOString(),
      },
    });
    await page.reload();
    await expect(page.getByRole('list', { name: 'Pull requests' }).getByRole('link', { name: `${oldKey} Faster search index` })).toBeVisible();

    // And its history tells the story.
    await expect(page.getByText(`moved it to ${projectName} as ${key}-1, was ${oldKey}`)).toBeVisible();
    await page.screenshot({ path: 'test-results/task-branch.png', fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.getByTestId('task-branch').scrollIntoViewIfNeeded();
    await page.screenshot({ path: 'test-results/task-branch-phone.png' });
  } finally {
    if (taskId) await owner.del(`/tasks/${taskId}`).catch(() => {});
    await owner.del('/integrations/bitbucket').catch(() => {});
    const projects = ((await owner.get('/projects').catch(() => ({ projects: [] }))).projects ?? []) as Array<{ id: string; key: string }>;
    for (const p of projects.filter((p) => p.key === key)) await owner.del(`/projects/${p.id}`).catch(() => {});
    await owner.dispose();
    await bitbucket.close();
  }
});
