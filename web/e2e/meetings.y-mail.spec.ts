import type { Page } from '@playwright/test';
import { expect, test } from './support/fixtures';
import { sql } from './support/meetings';

// An administrator sets up a mail server after first-time setup, and a new member is
// emailed their sign-in details, which really work. Uses the Mailpit container from
// web/e2e/README-services.md as the mail server.

const SMTP_PORT = Number(process.env.E2E_MAILPIT_SMTP_PORT ?? 55480);
const MAILPIT = process.env.E2E_MAILPIT_API ?? 'http://127.0.0.1:55481';

interface MailpitMessage {
  ID: string;
  Subject: string;
  To: { Address: string }[];
}

async function inbox(address: string): Promise<MailpitMessage[]> {
  const res = await fetch(`${MAILPIT}/api/v1/search?query=${encodeURIComponent(`to:"${address}"`)}`);
  if (!res.ok) throw new Error(`Mailpit search answered ${res.status}`);
  return (await res.json()).messages ?? [];
}

async function bodyOf(id: string): Promise<string> {
  const res = await fetch(`${MAILPIT}/api/v1/message/${id}`);
  return (await res.json()).Text as string;
}

async function openEmailTab(page: Page) {
  await page.goto('/organization');
  await page.getByRole('tab', { name: 'Email' }).click();
  await expect(page.getByLabel('Mail server')).toBeVisible();
}

async function fillMail(page: Page, port: number) {
  await page.getByLabel('Mail server').fill('127.0.0.1');
  await page.getByLabel('Port').fill(String(port));
  await page.getByLabel('Username').fill('teamup');
  await page.getByLabel('Password', { exact: true }).fill('mailpit-accepts-any');
  await page.getByLabel('Send as').fill('TeamUp <teamup@e2e.test>');
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.getByText('Mail settings saved.', { exact: false }).first()).toBeVisible();
}

async function addMember(page: Page, name: string, email: string) {
  await page.getByRole('tab', { name: 'Members' }).click();
  await page.getByRole('button', { name: /Add member/ }).first().click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Name').fill(name);
  await dialog.getByLabel('Email').fill(email);
  await dialog.getByRole('button', { name: 'Add member' }).click();
  await expect(page.getByRole('heading', { name: 'Member added' })).toBeVisible({ timeout: 60_000 });
}

test.describe('welcome email', () => {
  test.beforeAll(async () => {
    const reachable = await fetch(`${MAILPIT}/api/v1/messages`).then((r) => r.ok).catch(() => false);
    if (!reachable) throw new Error(`MAILPIT NOT RUNNING at ${MAILPIT}: start it as web/e2e/README-services.md says. This is a setup fault.`);
  });
  // Later specs add members too, and must find no mail server set up, as before.
  test.afterAll(() => {
    sql(`DELETE FROM org_configs WHERE key = 'smtp'`);
  });

  test('an administrator sets up mail, a new member is emailed working sign-in details, and the password never comes back', async ({ as }) => {
    const olivia = await as('owner');
    await openEmailTab(olivia.page);
    await fillMail(olivia.page, SMTP_PORT);

    // The password field is empty once saved, and says one is kept.
    await olivia.page.reload();
    await olivia.page.getByRole('tab', { name: 'Email' }).click();
    await expect(olivia.page.getByLabel('Password', { exact: true })).toHaveValue('');
    await expect(olivia.page.getByLabel('Password', { exact: true })).toHaveAttribute('placeholder', /Saved/);

    await olivia.page.getByRole('button', { name: 'Send test email' }).click();
    await expect(olivia.page.getByRole('status').filter({ hasText: 'Sent to owner@e2e.test' })).toBeVisible();

    // Changing only the password, which saves back the same settings, still saves
    // and leaves the form ready to test.
    await olivia.page.getByLabel('Password', { exact: true }).fill('a-new-app-password');
    await olivia.page.getByRole('button', { name: 'Save' }).click();
    await expect(olivia.page.getByLabel('Password', { exact: true })).toHaveValue('');
    await expect(olivia.page.getByRole('button', { name: 'Send test email' })).toBeEnabled();
    await expect.poll(async () => (await inbox('owner@e2e.test')).some((m) => m.Subject === 'TeamUp can send email')).toBe(true);

    const email = `newbie-${Date.now()}@e2e.test`;
    await addMember(olivia.page, 'Nell Newbie', email);
    await expect(olivia.page.getByRole('dialog').getByText(`Sent to ${email}`)).toBeVisible();
    const shown = await olivia.page.getByLabel('Temporary password').inputValue();
    await olivia.page.getByRole('button', { name: 'Done' }).click();

    await expect.poll(async () => (await inbox(email)).length).toBe(1);
    const [welcome] = await inbox(email);
    const text = await bodyOf(welcome.ID);
    expect(text).toContain('Hello Nell Newbie');
    expect(text).toContain(`Email     ${email}`);
    expect(text).toContain(`Password  ${shown}`);
    expect(text).toMatch(/http:\/\/localhost:\d+\/login/);
    expect(text).not.toContain('—');

    // The emailed password works, and only as far as choosing their own.
    const nell = await as('owner', { signedOut: true });
    await nell.page.goto('/login');
    await nell.page.getByLabel('Email').fill(email);
    await nell.page.getByLabel('Password').fill(shown);
    await nell.page.getByRole('button', { name: /^sign in$/i }).click();
    await expect(nell.page).toHaveURL(/\/change-password/);
  });

  test('when the mail server is down the administrator is told why, and still gets the password to pass on', async ({ as }) => {
    const olivia = await as('owner');
    await openEmailTab(olivia.page);
    // Nothing listens on this port.
    await fillMail(olivia.page, 55499);
    await olivia.page.getByRole('button', { name: 'Send test email' }).click();
    await expect(olivia.page.getByRole('status').filter({ hasText: /did not take it: could not reach 127\.0\.0\.1:55499/ })).toBeVisible();

    const email = `unlucky-${Date.now()}@e2e.test`;
    await addMember(olivia.page, 'Una Unlucky', email);
    const dialog = olivia.page.getByRole('dialog');
    await expect(dialog.getByText(`Pass these on to ${email}`)).toBeVisible();
    await expect(dialog.getByRole('status')).toContainText('The email could not be sent: could not reach 127.0.0.1:55499');
    expect((await dialog.getByLabel('Temporary password').inputValue()).length).toBeGreaterThanOrEqual(16);
    expect(await inbox(email)).toHaveLength(0);
  });
});
