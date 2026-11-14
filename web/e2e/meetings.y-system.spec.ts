import type { Locator } from '@playwright/test';
import { test, expect } from './support/fixtures';
import { apiAs, createCall, joinCall, leaveCall, sql } from './support/meetings';

// The System tab: how hard the server is working, for whoever may change the
// organization's settings, and for nobody else.

/** The number shown under a label, such as "Online now". */
const figure = (tab: Locator, label: string) => tab.locator('dt', { hasText: new RegExp(`^${label}$`) }).locator('xpath=following-sibling::dd');

test('an administrator sees the server, people, calls and the last backup, live', async ({ as }) => {
  test.setTimeout(90_000);
  const owner = await apiAs('owner');
  // What the nightly backup writes when it finishes (scripts/backup-db.sh).
  sql(`INSERT INTO backup_runs (finished_at, size_bytes, uploaded) VALUES (NOW() - INTERVAL '2 hours', 266000, NULL)`);

  const olivia = await as('owner');
  await olivia.page.goto('/organization?tab=system');
  const tab = olivia.page.getByTestId('system-tab');
  await expect(tab).toBeVisible();
  await expect(olivia.page.getByRole('tab', { name: 'System' })).toHaveAttribute('aria-selected', 'true');

  // The machine, read from the server it runs on. Processors need two readings
  // fifteen seconds apart before they have a number.
  for (const meter of ['Memory', 'Disk']) {
    await expect(tab.getByRole('meter', { name: meter })).toHaveAttribute('aria-valuenow', /^\d+$/);
  }
  await expect(tab.getByRole('meter', { name: 'Processors' })).toHaveAttribute('aria-valuenow', /^\d+$/, { timeout: 30_000 });
  await expect(tab.getByText(/free of \d/)).toBeVisible();

  // Olivia herself is online.
  await expect(figure(tab, 'Online now')).not.toHaveText('0');
  // And her tab is one of the open connections.
  await expect(tab.getByText(/^[1-9]\d* open connections? to this server/)).toBeVisible();

  // The backup, as it recorded itself.
  await expect(tab.getByText('2 hours ago')).toBeVisible();
  await expect(tab.getByText(/266 KB, kept on this server/)).toBeVisible();

  // A call starts, and the tab sees her in it without a reload.
  const call = await createCall(owner, `System check ${Date.now()}`);
  const inCall = await as('owner');
  await joinCall(inCall.page, call.id);
  await expect(figure(tab, 'People in calls')).toHaveText('1', { timeout: 20_000 });
  await expect(tab.getByText(call.title)).toBeVisible();
  await leaveCall(inCall.page);
  await expect(figure(tab, 'People in calls')).toHaveText('0', { timeout: 20_000 });

  // The last day, drawn: four charts.
  await expect(tab.getByTestId('system-history').getByRole('img')).toHaveCount(4);
  await owner.dispose();
});

test('somebody who cannot change the settings sees no System tab, and the server says no', async ({ as }) => {
  const alice = await as('alice');
  // An ordinary member is not shown the Organization page at all.
  await alice.page.goto('/organization?tab=system');
  await expect(alice.page).toHaveURL(/\/chat/);
  await expect(alice.page.getByText('Pick a conversation', { exact: true })).toBeVisible();
  await expect(alice.page.getByTestId('system-tab')).toHaveCount(0);

  const api = await apiAs('alice');
  expect(await api.status('get', '/org/system')).toBe(403);
  expect(await api.status('get', '/org/system/history')).toBe(403);
  await api.dispose();
});

test('a missed backup is a warning in red', async ({ as }) => {
  sql(`DELETE FROM backup_runs`);
  sql(`INSERT INTO backup_runs (finished_at, size_bytes, uploaded, problem) VALUES (NOW() - INTERVAL '3 days', 1000, false, 'the bucket answered 403')`);
  const olivia = await as('owner');
  await olivia.page.goto('/organization?tab=system');
  const warnings = olivia.page.getByTestId('system-warnings');
  await expect(warnings.getByText('The last backup was 3 days ago. One should run every night.')).toBeVisible();
  await expect(olivia.page.getByText('the bucket answered 403')).toBeVisible();
  sql(`DELETE FROM backup_runs`);
});
