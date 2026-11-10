import { readFileSync } from 'node:fs';
import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { apiAs, idOf } from './support/meetings';
import { PEOPLE, readState } from './support/people';

const PICTURE = readFileSync(new URL('./fixtures/picture.png', import.meta.url));

const messageBox = (page: Page) => page.getByRole('textbox', { name: 'Message', exact: true });
const isFocused = (page: Page) => messageBox(page).evaluate((el) => el === document.activeElement);

// Reported: opening a conversation left the cursor nowhere, so every message
// began with a click on the box; members could only be added one at a time;
// and what was attached could not be looked at before it was sent.
test.describe('writing a message', () => {
  const stamp = Date.now();
  const made: { channels: string[]; teams: string[]; tasks: string[] } = { channels: [], teams: [], tasks: [] };

  test.afterAll(async () => {
    const owner = await apiAs('owner');
    for (const id of made.channels) await owner.del(`/channels/${id}`).catch(() => {});
    for (const id of made.teams) await owner.del(`/teams/${id}`).catch(() => {});
    for (const id of made.tasks) await owner.del(`/tasks/${id}`).catch(() => {});
    await owner.dispose();
  });

  test('the cursor is in the message box as soon as a conversation is open', async ({ as }) => {
    const owner = await apiAs('owner');
    const other = await owner.post('/channels', { type: 'group', name: `focus-${stamp}` });
    made.channels.push(other.id);
    await owner.dispose();

    const olivia = await as('owner');
    const page = olivia.page;
    const { channelId } = readState();
    await page.goto(`/chat/${channelId}`);
    await expect.poll(() => isFocused(page), { message: 'opened by its address' }).toBe(true);

    // Typing goes straight in.
    await page.keyboard.type('hello');
    await expect(messageBox(page)).toHaveValue('hello');
    await messageBox(page).fill('');

    await page.getByRole('button', { name: new RegExp(`focus-${stamp}`) }).first().click();
    await expect(messageBox(page)).toHaveAttribute('placeholder', `Message #focus-${stamp}`);
    await expect.poll(() => isFocused(page), { message: 'opened from the list' }).toBe(true);

    // Started from the new-chat dialog, which hands focus back to its button as it closes.
    await page.getByRole('button', { name: 'New chat' }).click();
    const dialog = page.getByRole('dialog');
    await dialog.getByRole('button', { name: new RegExp(PEOPLE.sam.name) }).click();
    await expect(dialog).toBeHidden();
    await expect(messageBox(page)).toHaveAttribute('placeholder', `Message ${PEOPLE.sam.name}`);
    await expect.poll(() => isFocused(page), { message: 'started from the new-chat dialog' }).toBe(true);

    // A thread opens ready for the reply, and closing it comes back to the conversation's box.
    await page.keyboard.type(`threaded ${stamp}`);
    await page.keyboard.press('Enter');
    await page.getByText(`threaded ${stamp}`).hover();
    await page.getByRole('button', { name: 'Message actions' }).last().click();
    await page.getByRole('menuitem', { name: 'Reply in thread' }).click();
    await expect(page.getByRole('textbox', { name: 'Reply in thread' })).toBeFocused();
    await page.getByRole('button', { name: 'Close thread' }).click();
    await expect.poll(() => isFocused(page), { message: 'back from the thread' }).toBe(true);
  });

  test('on a phone the keyboard stays down until the box is tapped', async ({ as }) => {
    const olivia = await as('owner', { phone: 'android' });
    const { channelId } = readState();
    await olivia.page.goto(`/chat/${channelId}`);
    await expect(messageBox(olivia.page)).toBeEnabled();
    await olivia.page.waitForTimeout(500);
    expect(await isFocused(olivia.page)).toBe(false);
  });

  test('several files are attached at once, and each can be looked at before it is sent', async ({ as }) => {
    const olivia = await as('owner');
    const page = olivia.page;
    const { channelId } = readState();
    await page.goto(`/chat/${channelId}`);
    const files = {
      picture: `picture-${stamp}.png`,
      notes: `notes-${stamp}.txt`,
      plan: `plan-${stamp}.docx`,
    };
    await page.locator('input[type="file"][aria-label="Attach a file"]').setInputFiles([
      { name: files.picture, mimeType: 'image/png', buffer: PICTURE },
      { name: files.notes, mimeType: 'text/plain', buffer: Buffer.from(`Agenda for ${stamp}\n1. Ship it`) },
      { name: files.plan, mimeType: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document', buffer: Buffer.from('PK not really') },
    ]);
    await expect(page.getByText('3 attached. Press Send to share.')).toBeVisible({ timeout: 30_000 });
    const tray = page.getByRole('list', { name: 'Files to send' });
    await expect(tray.getByRole('button', { name: /^Preview / })).toHaveCount(3);
    await page.screenshot({ path: 'test-results/compose-staged.png' });

    // The picture.
    await tray.getByRole('button', { name: `Preview ${files.picture}` }).click();
    const preview = page.getByRole('dialog', { name: `Preview of ${files.picture}` });
    const shown = preview.getByRole('img', { name: files.picture });
    await expect(shown).toBeVisible();
    await expect(preview.getByText('1 of 3')).toBeVisible();
    await expect.poll(() => shown.evaluate((img: HTMLImageElement) => img.naturalWidth)).toBe(96);
    await page.screenshot({ path: 'test-results/compose-preview-picture.png' });

    // The text file, one step on.
    await page.keyboard.press('ArrowRight');
    const notes = page.getByRole('dialog', { name: `Preview of ${files.notes}` });
    await expect(notes.getByText(`Agenda for ${stamp}`)).toBeVisible();
    await page.screenshot({ path: 'test-results/compose-preview-text.png' });

    // A Word document cannot be shown in a browser, and says so.
    await notes.getByRole('button', { name: 'Next file' }).click();
    const plan = page.getByRole('dialog', { name: `Preview of ${files.plan}` });
    await expect(plan.getByText('There is no preview for a .docx file.', { exact: false })).toBeVisible();

    // Removed from the preview, it is not sent.
    await plan.getByRole('button', { name: 'Remove' }).click();
    await expect(page.getByRole('dialog', { name: /^Preview of / })).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog')).toHaveCount(0);
    await expect(page.getByText('2 attached. Press Send to share.')).toBeVisible();
    // Escape closed the preview, not the conversation.
    await expect(messageBox(page)).toBeVisible();

    await page.getByRole('button', { name: 'Send', exact: true }).click();
    await expect(page.getByText('Press Send to share')).toBeHidden({ timeout: 30_000 });
    await expect(page.getByRole('button', { name: `Open ${files.picture}` }).last()).toBeVisible();
    await expect(page.getByText(files.notes).last()).toBeVisible();
    await expect(page.getByText(files.plan)).toHaveCount(0);
  });

  test('a task comment takes several files, previewed the same way', async ({ as }) => {
    const owner = await apiAs('owner');
    const task = await owner.post('/tasks', { title: `Files on a task ${stamp}` });
    made.tasks.push(task.id);
    await owner.dispose();

    const olivia = await as('owner');
    const page = olivia.page;
    await page.goto(`/tasks/${task.id}`);
    await page.getByRole('button', { name: 'Add Comment' }).click();
    await page.locator('input[type="file"][aria-label="Attach files to this comment"]').setInputFiles([
      { name: `one-${stamp}.png`, mimeType: 'image/png', buffer: PICTURE },
      { name: `two-${stamp}.txt`, mimeType: 'text/plain', buffer: Buffer.from('second file') },
    ]);
    await expect(page.getByText('2 attached. Press Post comment to share.')).toBeVisible({ timeout: 30_000 });
    await page.getByRole('button', { name: `Preview two-${stamp}.txt` }).click();
    await expect(page.getByRole('dialog', { name: `Preview of two-${stamp}.txt` }).getByText('second file')).toBeVisible();
    await page.getByRole('button', { name: 'Close preview' }).click();
    await page.screenshot({ path: 'test-results/compose-task-comment.png' });
    await page.getByRole('button', { name: 'Post comment' }).click();
    await expect(page.getByRole('button', { name: `Open one-${stamp}.png` })).toBeVisible({ timeout: 30_000 });
    await expect(page.getByText(`two-${stamp}.txt`)).toBeVisible();
  });

  test('several people are added to a group, a team and a task in one go', async ({ as }) => {
    const owner = await apiAs('owner');
    const group = await owner.post('/channels', { type: 'group', name: `crew-${stamp}` });
    made.channels.push(group.id);
    const team = await owner.post('/teams', { name: `Crew ${stamp}` });
    made.teams.push(team.id);
    const task = await owner.post('/tasks', { title: `Crew task ${stamp}` });
    made.tasks.push(task.id);
    await owner.dispose();

    const olivia = await as('owner');
    const page = olivia.page;
    const tick = (name: string) => page.getByRole('dialog').last().getByRole('checkbox', { name: new RegExp(name) }).check();

    // A group.
    await page.goto(`/chat/${group.id}`);
    await page.getByRole('button', { name: 'Channel settings' }).click();
    await page.getByRole('button', { name: 'Add members' }).click();
    await tick(PEOPLE.alice.name);
    const find = page.getByRole('dialog').last().getByLabel('Find people');
    await find.fill('sam');
    await tick(PEOPLE.sam.name);
    await find.fill('');
    await expect(page.getByText('2 chosen.')).toBeVisible();
    await page.screenshot({ path: 'test-results/compose-pick-people.png' });
    await page.getByRole('button', { name: 'Add 2 members' }).click();
    await expect(page.getByRole('button', { name: `Remove ${PEOPLE.alice.name}` })).toBeVisible();
    await expect(page.getByRole('button', { name: `Remove ${PEOPLE.sam.name}` })).toBeVisible();

    // A team.
    await page.goto('/organization');
    await page.getByRole('tab', { name: 'Teams' }).click();
    const row = page.getByText(`Crew ${stamp}`, { exact: true }).locator('xpath=ancestor::div[contains(@class, "rounded-lg")][1]');
    await row.getByRole('button', { name: 'Add member' }).click();
    await tick(PEOPLE.pat.name);
    await tick(PEOPLE.ann.name);
    await page.getByRole('button', { name: 'Add 2 members' }).click();
    await expect(page.getByRole('dialog')).toBeHidden();
    await expect(row.getByText('2 members')).toBeVisible();

    // A task.
    await page.goto(`/tasks/${task.id}`);
    await page.getByRole('button', { name: 'Add', exact: true }).click();
    await tick(PEOPLE.mia.name);
    await tick(PEOPLE.max.name);
    await page.getByRole('button', { name: 'Add 2 people' }).click();
    await expect(page.getByRole('dialog')).toBeHidden();
    await expect(page.getByRole('button', { name: `Remove ${PEOPLE.mia.name}` })).toBeVisible();
    await expect(page.getByRole('button', { name: `Remove ${PEOPLE.max.name}` })).toBeVisible();
  });

  test('a new private group is made with its people', async ({ as }) => {
    const olivia = await as('owner');
    const page = olivia.page;
    await page.goto('/chat');
    await page.getByRole('button', { name: 'New Channel' }).click();
    const dialog = page.getByRole('dialog');
    await dialog.getByLabel('Name').fill(`launch-${stamp}`);
    await dialog.getByRole('checkbox', { name: new RegExp(PEOPLE.alice.name) }).check();
    await dialog.getByRole('checkbox', { name: new RegExp(PEOPLE.pat.name) }).check();
    await page.screenshot({ path: 'test-results/compose-new-group.png' });
    await dialog.getByRole('button', { name: 'Create channel' }).click();
    await expect(dialog).toBeHidden();
    await expect(messageBox(page)).toHaveAttribute('placeholder', `Message #launch-${stamp}`);

    const owner = await apiAs('owner');
    const listed = (await owner.get('/channels')) as any;
    const created = (listed.channels ?? listed).find((c: any) => c.name === `launch-${stamp}`);
    made.channels.push(created.id);
    const members = (await owner.get(`/channels/${created.id}/members`)) as any;
    await owner.dispose();
    const ids = (members.members ?? members.users ?? members).map((m: any) => m.userId);
    expect(ids).toEqual(expect.arrayContaining([idOf('owner'), idOf('alice'), idOf('pat')]));
    expect(ids).toHaveLength(3);
  });
});
