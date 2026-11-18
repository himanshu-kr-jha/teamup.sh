import { expect, test } from './support/fixtures';
import { apiAs, idOf } from './support/meetings';
import { PEOPLE } from './support/people';

// The owner's request of 17 November 2026: everybody says what they do at the
// company, such as "AI/ML Engineer", and everyone in the organization sees it
// beside their name. They set it themselves, or an administrator does.
test('a company role is set in Settings or by an admin, and shown beside the name', async ({ as }) => {
  test.setTimeout(90_000);
  const owner = await apiAs('owner');
  const aliceApi = await apiAs('alice');
  try {
    // Alice says what she does.
    const alice = await as('alice');
    await alice.page.goto('/settings');
    await alice.page.getByLabel('Company role').fill('AI/ML Engineer');
    await alice.page.getByRole('button', { name: 'Save Changes' }).click();
    await expect(alice.page.getByText('Your profile has been saved.').first()).toBeVisible();
    await alice.page.reload();
    await expect(alice.page.getByLabel('Company role')).toHaveValue('AI/ML Engineer');

    // Olivia sees it among the members, and in a conversation with Alice.
    const olivia = await as('owner');
    const page = olivia.page;
    await page.goto('/organization');
    const aliceRow = page.getByRole('list', { name: 'Members' }).getByRole('listitem').filter({ hasText: PEOPLE.alice.email });
    await expect(aliceRow.getByTestId('member-job-title')).toHaveText('AI/ML Engineer');

    const dm = await aliceApi.post('/channels/direct', { userId: idOf('owner') });
    await page.goto(`/chat/${dm.id}`);
    await expect(page.getByRole('heading', { level: 2, name: PEOPLE.alice.name }).locator('xpath=../p')).toContainText('AI/ML Engineer');

    // An administrator sets Pat's.
    await page.goto('/organization');
    await page.getByRole('button', { name: `Company role of ${PEOPLE.pat.name}` }).click();
    const dialog = page.getByRole('dialog');
    await dialog.getByLabel('Company role').fill('Site Reliability Engineer');
    await dialog.getByRole('button', { name: 'Save' }).click();
    await expect(dialog).toBeHidden();
    const patRow = page.getByRole('list', { name: 'Members' }).getByRole('listitem').filter({ hasText: PEOPLE.pat.email });
    await expect(patRow.getByTestId('member-job-title')).toHaveText('Site Reliability Engineer');
    await page.screenshot({ path: 'test-results/job-title-members.png' });
  } finally {
    await owner.patch(`/users/${idOf('alice')}`, { jobTitle: '' }).catch(() => {});
    await owner.patch(`/users/${idOf('pat')}`, { jobTitle: '' }).catch(() => {});
    await Promise.all([owner.dispose(), aliceApi.dispose()]);
  }
});

// The owner's request of 17 November 2026: a search on the member list, by
// name, email, company role or team, and filters for a team and a role.
test('the member list is searched by what is written about people, and narrowed by team and role', async ({ as }) => {
  const owner = await apiAs('owner');
  await owner.patch(`/users/${idOf('alice')}`, { jobTitle: 'AI/ML Engineer' });
  try {
    const { page } = await as('owner');
    await page.goto('/organization');
    const list = page.getByRole('list', { name: 'Members' });
    const row = (who: keyof typeof PEOPLE) => list.getByRole('listitem').filter({ hasText: PEOPLE[who].email });
    await expect(row('alice')).toBeVisible();

    // "/" goes to the search, from anywhere on the page but a field.
    await page.getByRole('heading', { name: 'Members' }).click();
    await page.keyboard.press('/');
    const search = page.getByRole('searchbox', { name: 'Search members' });
    await expect(search).toBeFocused();

    await search.fill('ml engineer');
    await expect(row('alice')).toBeVisible();
    await expect(row('pat')).toBeHidden();
    await expect(page.getByText(/^1 of \d+ members$/)).toBeVisible();

    await search.fill('platform');
    await expect(row('pat')).toBeVisible();
    await expect(row('pat').getByTestId('member-teams')).toContainText('Platform');
    await expect(row('alice')).toBeHidden();

    await search.fill('nobody-is-called-this');
    await expect(page.getByText('No one matches “nobody-is-called-this”.')).toBeVisible();
    await page.getByRole('button', { name: 'Clear the search' }).click();
    await expect(search).toHaveValue('');
    await expect(row('alice')).toBeVisible();

    await page.getByRole('combobox', { name: 'Team' }).click();
    await page.getByRole('option', { name: 'Sales', exact: true }).click();
    await expect(row('sam')).toBeVisible();
    await expect(row('alice')).toBeHidden();
    await page.getByRole('combobox', { name: 'Team' }).click();
    await page.getByRole('option', { name: 'All teams' }).click();

    await page.getByRole('combobox', { name: 'Role' }).click();
    await page.getByRole('option', { name: 'manager', exact: true }).click();
    await expect(row('mia')).toBeVisible();
    await expect(row('max')).toBeVisible();
    await expect(row('alice')).toBeHidden();
    await page.screenshot({ path: 'test-results/member-search.png' });
  } finally {
    await owner.patch(`/users/${idOf('alice')}`, { jobTitle: '' }).catch(() => {});
    await owner.dispose();
  }
});
