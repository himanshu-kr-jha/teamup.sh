import { writeFileSync } from 'node:fs';
import { Api } from './support/api';
import { OWNER, PEOPLE, STATE_FILE, TASKS, type PersonKey, type SeedState } from './support/people';

/**
 * Builds the organization the specs run against, through the same API the app
 * uses: an owner who runs no team; Mia, who manages Backend (Alice, Ann) with
 * the nested team Platform (Pat); and Max, who manages Sales (Sam).
 */
export default async function globalSetup() {
  // Re-running part of the suite against the organization a previous run left behind.
  if (process.env.E2E_REUSE_SEED === '1') return;
  const owner = await Api.signIn(OWNER.email, OWNER.password);
  try {
    const existing: any = await owner.get('/users');
    const users: any[] = existing.users ?? existing.data ?? existing;
    if (users.some((u) => u.email === PEOPLE.alice.email)) {
      throw new Error(
        'This database already holds the E2E people. The suite needs a freshly set-up instance: run e2e/run.sh, or recreate the database and restart the server.'
      );
    }

    const ids = { owner: owner.userId } as Record<PersonKey, string>;
    const roleOf: Partial<Record<PersonKey, string>> = { mia: 'manager', max: 'manager' };
    for (const key of ['mia', 'max', 'alice', 'ann', 'pat', 'sam'] as const) {
      const person = PEOPLE[key];
      const created = await owner.post('/users', { name: person.name, email: person.email, role: roleOf[key] });
      // Settle the temporary password the way a person does on first sign-in,
      // so the real login form lands in the app rather than on the change page.
      const them = await Api.signIn(person.email, created.temporaryPassword);
      await them.post('/auth/password', { currentPassword: created.temporaryPassword, newPassword: person.password });
      await them.dispose();
      ids[key] = created.user.id;
    }

    const backend = await owner.post('/teams', { name: 'Backend', managerId: ids.mia });
    const platform = await owner.post('/teams', { name: 'Platform', parentId: backend.id });
    const sales = await owner.post('/teams', { name: 'Sales', managerId: ids.max });
    for (const [team, who] of [
      [backend, 'alice'],
      [backend, 'ann'],
      [platform, 'pat'],
      [sales, 'sam'],
    ] as const) {
      await owner.post(`/teams/${team.id}/members`, { userId: ids[who] });
    }

    const task = (title: string, who: PersonKey, extra: Record<string, unknown> = {}) =>
      owner.post('/tasks', { title, assigneeId: ids[who], ...extra });
    const daysAgo = (n: number) => new Date(Date.now() - n * 86_400_000).toISOString();

    await task(TASKS.aliceSpec, 'alice', { status: 'todo' });
    await task(TASKS.aliceReport, 'alice', { status: 'todo' });
    await task(TASKS.aliceApi, 'alice', { status: 'in_progress' });
    await task(TASKS.aliceBlocked, 'alice', { status: 'blocked' });
    await task(TASKS.patPipeline, 'pat', { status: 'in_progress' });
    await task(TASKS.patLogs, 'pat', { status: 'todo' });
    await task(TASKS.patCerts, 'pat', { status: 'todo', dueDate: daysAgo(3), priority: 'high' });
    await task(TASKS.samCalls, 'sam', { status: 'todo' });
    await task(TASKS.samQuote, 'sam', { status: 'in_progress' });

    // A conversation for the chat checks.
    const channel = await owner.post('/channels', { type: 'public', name: 'e2e-room' });
    for (const key of ['alice', 'pat'] as const) {
      await owner.post(`/channels/${channel.id}/members`, { userId: ids[key] });
    }

    const state: SeedState = {
      ids,
      teams: { backend: backend.id, platform: platform.id, sales: sales.id },
      channelId: channel.id,
    };
    writeFileSync(STATE_FILE, JSON.stringify(state, null, 2));
  } finally {
    await owner.dispose();
  }
}
