import { expect, test } from './support/fixtures';
import { apiAs, idOf } from './support/meetings';

// A role made on the Roles screen from a checklist of permissions does what
// its boxes say: the person given it sees and can use exactly that, and
// cannot hand out more than they hold.

test.describe('roles', () => {
  test('a role made from the checklist gives exactly its permissions, and nobody grants more than they hold', async ({ as }) => {
    const owner = await apiAs('owner');
    const roleName = `Office manager ${Date.now()}`;
    const keeperName = `Role keeper ${Date.now()}`;
    const made: string[] = [];
    try {
      // The owner makes a role from the checklist.
      const olivia = await as('owner');
      await olivia.page.goto('/organization');
      await olivia.page.getByRole('tab', { name: 'Roles' }).click();
      await olivia.page.getByRole('button', { name: 'New role' }).click();
      const dialog = olivia.page.getByRole('dialog');
      await dialog.getByLabel('Name', { exact: true }).fill(roleName);
      await dialog.getByLabel('What it is for').fill('Runs the office');
      await dialog.getByRole('checkbox', { name: /^Change organization settings/ }).check();
      await dialog.getByRole('checkbox', { name: /^Manage members/ }).check();
      await expect(dialog.getByTestId('permission-count')).toHaveText('2 of 10');
      // A whole area at once, and back.
      await dialog.getByRole('group', { name: 'Meetings' }).getByRole('button', { name: 'All' }).click();
      await expect(dialog.getByTestId('permission-count')).toHaveText('6 of 10');
      await dialog.getByRole('group', { name: 'Meetings' }).getByRole('button', { name: 'None' }).click();
      await expect(dialog.getByTestId('permission-count')).toHaveText('2 of 10');
      await dialog.getByRole('button', { name: 'Create role' }).click();
      const row = olivia.page.getByTestId('role').filter({ hasText: roleName });
      await expect(row).toContainText('Change organization settings, Manage members');

      // And gives it to Alice from the Members tab, choosing from a list that says what each role does.
      await olivia.page.getByRole('tab', { name: 'Members' }).click();
      const alicesRow = olivia.page.locator('div.rounded-lg', { hasText: 'alice@e2e.test' }).last();
      await alicesRow.getByRole('button', { name: 'Assign role' }).click();
      await olivia.page.getByRole('dialog').getByLabel('Role').click();
      await olivia.page.getByRole('option', { name: new RegExp(roleName) }).click();
      await olivia.page.getByRole('dialog').getByRole('button', { name: 'Assign role' }).click();
      await expect(alicesRow).toContainText(roleName);
      const roles = (await owner.get('/roles')).roles as Array<{ id: string; name: string }>;
      made.push(roles.find((r) => r.name === roleName)!.id);

      // Alice now has exactly that: the settings and the members, not the roles.
      const alice = await as('alice');
      await alice.page.goto('/organization');
      await expect(alice.page.getByRole('tab', { name: 'Email' })).toBeVisible();
      await expect(alice.page.getByRole('tab', { name: 'Meeting notes' })).toHaveCount(0);
      await expect(alice.page.getByRole('button', { name: 'Add member' })).toBeVisible();
      await expect(alice.page.getByRole('button', { name: 'Assign role' })).toHaveCount(0);
      await alice.page.getByRole('tab', { name: 'Roles' }).click();
      await expect(alice.page.getByRole('button', { name: 'New role' })).toHaveCount(0);
      const aliceApi = await apiAs('alice');
      expect(await aliceApi.status('get', '/org/mail')).toBe(200);
      expect(await aliceApi.status('get', '/org/notetaker')).toBe(403);
      expect(await aliceApi.status('post', '/roles', { name: 'Sneaky', permissions: ['org.settings'] })).toBe(403);
      await aliceApi.dispose();

      // Somebody who manages roles sees what they cannot grant, and cannot tick it.
      const keeper = await owner.post('/roles', { name: keeperName, permissions: ['roles.manage', 'tasks.all'] });
      made.push(keeper.id);
      await owner.post(`/users/${idOf('sam')}/roles`, { roleId: keeper.id });
      const sam = await as('sam');
      await sam.page.goto('/organization');
      await sam.page.getByRole('tab', { name: 'Roles' }).click();
      await sam.page.getByRole('button', { name: 'New role' }).click();
      const samDialog = sam.page.getByRole('dialog');
      await expect(samDialog.getByRole('checkbox', { name: /^Change organization settings/ })).toBeDisabled();
      await expect(samDialog.getByRole('checkbox', { name: /^See and change everybody's tasks/ })).toBeEnabled();
      // Starting from Admin keeps only what Sam holds.
      await samDialog.getByLabel('Start from').click();
      await sam.page.getByRole('option', { name: 'admin', exact: true }).click();
      await expect(samDialog.getByTestId('permission-count')).toHaveText('2 of 10');
      await sam.page.keyboard.press('Escape');
      // And the admin and owner roles are not offered to hand out.
      await sam.page.getByRole('tab', { name: 'Members' }).click();
      await sam.page.locator('div.rounded-lg', { hasText: 'ann@e2e.test' }).last().getByRole('button', { name: 'Assign role' }).click();
      await sam.page.getByRole('dialog').getByLabel('Role').click();
      await expect(sam.page.getByRole('option', { name: /^admin/ })).toHaveCount(0);
      await expect(sam.page.getByRole('option', { name: /^owner/ })).toHaveCount(0);
      await expect(sam.page.getByRole('option', { name: new RegExp(keeperName) })).toBeVisible();
    } finally {
      for (const id of made) {
        await owner.del(`/users/${idOf('alice')}/roles/${id}`).catch(() => undefined);
        await owner.del(`/users/${idOf('sam')}/roles/${id}`).catch(() => undefined);
        await owner.del(`/roles/${id}`).catch(() => undefined);
      }
      await owner.dispose();
    }
  });
});
