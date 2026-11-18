import { expect, test, openTasks } from './support/fixtures';
import { apiAs } from './support/meetings';
import { PEOPLE } from './support/people';

// The owner's request of 19 November 2026: wherever people or teams are listed
// to choose from, type to find them instead of scrolling. One rule everywhere:
// name, email, company role or team, best matches first; Enter picks the only
// match, the arrows move, and Escape clears what was typed before it closes.

test('starting a call: search, Enter, arrows, the chosen stay in view, nobody matches, Escape', async ({ as }) => {
  const olivia = await as('owner');
  const page = olivia.page;
  await page.goto('/calls');
  await page.getByRole('button', { name: 'New Call' }).click();
  const dialog = page.getByRole('dialog');
  const find = dialog.getByLabel('Find people');

  // On a computer the search has the cursor, ready to type a name.
  await expect(find).toBeFocused();

  // One match: Enter ticks it and empties the box for the next name.
  await find.fill('pat pla');
  await expect(dialog.getByRole('list', { name: 'People' }).getByRole('checkbox')).toHaveCount(1);
  await find.press('Enter');
  await expect(find).toHaveValue('');
  await expect(dialog.getByRole('checkbox', { name: new RegExp(PEOPLE.pat.name) })).toBeChecked();

  // By email, then the arrows: down onto the first match, Enter ticks it.
  await find.fill(PEOPLE.sam.email);
  await find.press('ArrowDown');
  await find.press('Enter');
  await expect(dialog.getByRole('checkbox', { name: new RegExp(PEOPLE.sam.name) })).toBeChecked();

  // A team finds its people: Pat is on Platform.
  await find.fill('platform');
  await expect(dialog.getByRole('checkbox', { name: new RegExp(PEOPLE.pat.name) })).toBeVisible();
  await expect(dialog.getByRole('checkbox', { name: new RegExp(PEOPLE.sam.name) })).toHaveCount(0);

  // Nobody: said plainly, and whoever is chosen is still there above.
  await find.fill('zz-nobody');
  await expect(dialog.getByText('No one matches “zz-nobody”.')).toBeVisible();
  const chosen = dialog.getByRole('list', { name: 'Chosen' });
  await expect(chosen.getByRole('listitem')).toHaveText([PEOPLE.pat.name, PEOPLE.sam.name]);
  await expect(dialog.getByText('2 chosen.')).toBeVisible();

  // Escape clears the search first, and only then closes the dialog.
  await find.press('Escape');
  await expect(find).toHaveValue('');
  await expect(dialog).toBeVisible();
  await find.press('Escape');
  await expect(dialog).toBeHidden();
});

test('on a phone the search does not take the focus, so the keyboard stays down', async ({ as }) => {
  const olivia = await as('owner', { phone: 'android' });
  const page = olivia.page;
  await page.goto('/chat');
  await page.getByRole('button', { name: 'New chat' }).click();
  const dialog = page.getByRole('dialog');
  const find = dialog.getByLabel('Search people');
  await expect(dialog.getByRole('option', { name: new RegExp(PEOPLE.alice.name) })).toBeVisible();
  await expect(find).not.toBeFocused();
  // Typing still works the same once tapped.
  await find.fill('idle');
  await expect(dialog.getByRole('option')).toHaveCount(1);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
});

test('scheduling a meeting: find somebody by name and pick them with Enter', async ({ as }) => {
  const mia = await as('mia');
  const page = mia.page;
  await page.goto('/calendar');
  await page.getByRole('button', { name: 'Schedule', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog.getByLabel('Name', { exact: true })).toBeFocused();
  const find = dialog.getByLabel('Find people');
  await find.fill('idle');
  await find.press('Enter');
  await expect(dialog.getByRole('list', { name: 'Chosen' })).toHaveText(PEOPLE.ann.name);
  // With somebody chosen the list folds away until there is something to look for.
  await expect(dialog.getByRole('list', { name: 'People' })).toHaveCount(0);
  await find.fill('qq-nobody');
  await expect(dialog.getByText('No one matches “qq-nobody”.')).toBeVisible();
  await find.press('Escape');
  await expect(dialog).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
});

test('assigning a task: the assignee is a searchable list, by keyboard', async ({ as }) => {
  const owner = await apiAs('owner');
  const stamp = Date.now();
  const task = await owner.post('/tasks', { title: `Search assignee ${stamp}` });
  try {
    const olivia = await as('owner');
    const page = olivia.page;
    await page.goto(`/tasks/${task.id}`);
    await expect(page.getByRole('heading', { name: `Search assignee ${stamp}` })).toBeVisible();

    await page.getByRole('combobox', { name: 'Assign someone' }).click();
    const box = page.getByRole('combobox', { name: 'Search people' });
    await expect(box).toBeFocused();

    // Nothing: said so, Escape clears it and the list stays open.
    await box.fill('zz-nobody');
    await expect(page.getByText('No one matches “zz-nobody”.')).toBeVisible();
    await box.press('Escape');
    await expect(box).toHaveValue('');
    await expect(page.getByRole('listbox')).toBeVisible();

    // By team, best match first, with the arrows.
    await box.fill('sales');
    await expect(page.getByRole('option')).toHaveText([PEOPLE.sam.name]);
    await box.press('ArrowDown');
    await box.press('Enter');
    await expect(page.getByRole('listbox')).toHaveCount(0);
    const reassign = page.getByRole('combobox', { name: 'Reassign' });
    await expect(reassign).toBeVisible();
    await expect(reassign.locator('xpath=ancestor::div[contains(@class, "rounded")][1]')).toContainText(PEOPLE.sam.name);

    // Escape with nothing typed closes the list and changes nothing.
    await reassign.click();
    await expect(box).toBeFocused();
    // The "saved" toast takes the first Escape if it is still up.
    await expect(async () => {
      await page.keyboard.press('Escape');
      await expect(page.getByRole('listbox')).toHaveCount(0, { timeout: 500 });
    }).toPass();
  } finally {
    await owner.del(`/tasks/${task.id}`).catch(() => undefined);
    await owner.dispose();
  }
});

test('teams: found by name or by somebody on them, on Organization and in the task filter', async ({ as }) => {
  const olivia = await as('owner');
  const page = olivia.page;
  await page.goto('/organization');
  await page.getByRole('tab', { name: 'Teams' }).click();
  const search = page.getByLabel('Search teams');
  const teamRow = (name: string) => page.getByText(name, { exact: true });
  await search.fill(PEOPLE.sam.name.split(' ')[0]);
  await expect(teamRow('Sales')).toBeVisible();
  await expect(teamRow('Backend')).toHaveCount(0);
  await search.fill('zz-nobody');
  await expect(page.getByText('No team matches “zz-nobody”.')).toBeVisible();
  await search.press('Escape');
  await expect(teamRow('Backend')).toBeVisible();

  // The task dashboard's team filter: type, Enter.
  const mia = await as('mia');
  await openTasks(mia.page);
  await mia.page.getByRole('combobox', { name: 'Filter by team' }).click();
  const box = mia.page.getByRole('combobox', { name: 'Search teams' });
  await box.fill('plat');
  await expect(mia.page.getByRole('option').first()).toHaveText('Platform');
  await box.press('ArrowDown');
  await box.press('Enter');
  await expect(mia.page.getByRole('combobox', { name: 'Filter by team' })).toHaveText('Platform');
  await expect(mia.page.getByRole('row', { name: new RegExp(`^${PEOPLE.pat.name}`) })).toBeVisible();
  await expect(mia.page.getByRole('row', { name: new RegExp(`^${PEOPLE.alice.name}`) })).toHaveCount(0);
});
