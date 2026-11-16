import { expect, test } from './support/fixtures';
import { Api } from './support/api';
import { apiAs, ensureMember, sql } from './support/meetings';
import { PASSWORD } from './support/people';

// Somebody who forgot their password resets it themself, with a link emailed by
// the organization's own mail server (Mailpit here); without mail, the
// administrators are asked in the app. The owner's request of 16 November 2026.

const SMTP_PORT = Number(process.env.E2E_MAILPIT_SMTP_PORT ?? 55480);
const MAILPIT = process.env.E2E_MAILPIT_API ?? 'http://127.0.0.1:55481';

// Somebody new for each test, so no other spec's password changes, and each
// starts with no links and no count against the three an hour.
let RITA = { key: '', name: 'Rita Reset', email: '', password: PASSWORD };
const NEW_PASSWORD = 'Rita-new-pass-2026';

async function mailTo(address: string, subject: RegExp): Promise<string> {
  let body = '';
  await expect
    .poll(
      async () => {
        const res = await fetch(`${MAILPIT}/api/v1/search?query=${encodeURIComponent(`to:"${address}"`)}`);
        const found = ((await res.json()).messages ?? []) as Array<{ ID: string; Subject: string }>;
        const hit = found.find((m) => subject.test(m.Subject));
        if (!hit) return false;
        body = (await (await fetch(`${MAILPIT}/api/v1/message/${hit.ID}`)).json()).Text as string;
        return true;
      },
      { message: `an email to ${address} matching ${subject}`, timeout: 20_000 },
    )
    .toBe(true);
  return body;
}

const clearInbox = () => fetch(`${MAILPIT}/api/v1/messages`, { method: 'DELETE' });

test.describe('forgotten password', () => {
  test.beforeAll(async () => {
    const reachable = await fetch(`${MAILPIT}/api/v1/messages`).then((r) => r.ok).catch(() => false);
    if (!reachable) throw new Error(`MAILPIT NOT RUNNING at ${MAILPIT}: start it as web/e2e/README-services.md says. This is a setup fault.`);
  });
  // eslint-disable-next-line no-empty-pattern -- Playwright needs the fixtures pattern to reach the test info.
  test.beforeEach(async ({}, info) => {
    await clearInbox();
    const key = `rita-${Date.now()}-${info.repeatEachIndex}`;
    RITA = { key, name: `Rita Reset ${key.slice(-6)}`, email: `${key}@e2e.test`, password: PASSWORD };
    await ensureMember(RITA);
  });
  test.afterAll(() => {
    sql(`DELETE FROM org_configs WHERE key = 'smtp'`);
    sql(`DELETE FROM password_resets`);
  });

  test('a link by email sets a new password once, signs out everywhere, and says so', async ({ page }) => {
    const owner = await apiAs('owner');
    await owner.put('/org/mail', { host: '127.0.0.1', port: SMTP_PORT, user: 'teamup', password: 'mailpit-accepts-any', from: 'TeamUp <teamup@e2e.test>' });
    await owner.dispose();
    // Signed in somewhere else, which the reset ends.
    const elsewhere = await Api.signIn(RITA.email, RITA.password);

    // From the sign-in page, with the address she had typed.
    await page.goto('/login');
    await page.getByLabel('Email').fill(RITA.email);
    await page.getByRole('link', { name: 'Forgot password?' }).click();
    await expect(page.getByRole('heading', { name: 'Forgot your password?' })).toBeVisible();
    await expect(page.getByLabel('Email')).toHaveValue(RITA.email);
    await page.getByRole('button', { name: 'Send reset link' }).click();
    await expect(page.getByRole('heading', { name: 'Check your email' })).toBeVisible();
    await expect(page.getByText(`If there is an account at ${RITA.email}`, { exact: false })).toBeVisible();

    const email = await mailTo(RITA.email, /^Reset your .* password on TeamUp$/);
    const link = email.match(/https?:\/\/\S+\/reset-password#token=\S+/)?.[0];
    expect(link, `a reset link in:\n${email}`).toBeTruthy();
    expect(email).toContain('30 minutes');
    const path = new URL(link!).pathname + new URL(link!).hash;

    // The link opens the page for her account, and leaves the address bar clean.
    await page.goto(path);
    await expect(page.getByRole('heading', { name: 'Choose a new password' })).toBeVisible();
    await expect(page.getByText(`For ${RITA.email}.`, { exact: false })).toBeVisible();
    expect(page.url()).not.toContain('token');

    await page.getByLabel('New password', { exact: true }).fill(NEW_PASSWORD);
    await page.getByLabel('Confirm new password').fill('something-else-entirely');
    await page.getByRole('button', { name: 'Change password' }).click();
    await expect(page.getByRole('alert')).toHaveText('The two passwords do not match.');
    await page.getByLabel('Confirm new password').fill(NEW_PASSWORD);
    await page.getByRole('button', { name: 'Change password' }).click();

    // Back at sign-in, told, with her address in.
    await expect(page).toHaveURL(/\/login$/);
    await expect(page.getByRole('status').filter({ hasText: 'Your password has been changed. Sign in with the new one.' })).toBeVisible();
    await expect(page.getByLabel('Email')).toHaveValue(RITA.email);
    expect(await elsewhere.status('get', '/auth/me'), 'the other session is over').toBe(401);
    await elsewhere.dispose();

    // The old password is dead, and the new one works.
    expect((await page.request.post('/api/auth/login', { data: { email: RITA.email, password: RITA.password } })).status()).toBe(401);
    await page.getByLabel('Password', { exact: true }).fill(NEW_PASSWORD);
    await page.getByRole('button', { name: 'Sign in' }).click();
    await expect(page).not.toHaveURL(/\/login/);

    // She is told by email, and the link works only once.
    await mailTo(RITA.email, /password was changed$/);
    await page.goto(path);
    await expect(page.getByRole('heading', { name: 'This link does not work' })).toBeVisible();
    await expect(page.getByText('It has expired, was already used, or a newer one was sent.', { exact: false })).toBeVisible();
    await page.getByRole('link', { name: 'Ask for a new link' }).click();
    await expect(page.getByRole('heading', { name: 'Forgot your password?' })).toBeVisible();
  });

  test('an address with no account is answered the same, and nothing is sent', async ({ page }) => {
    const owner = await apiAs('owner');
    await owner.put('/org/mail', { host: '127.0.0.1', port: SMTP_PORT, user: 'teamup', password: 'mailpit-accepts-any', from: 'TeamUp <teamup@e2e.test>' });
    await owner.dispose();
    const nobody = `nobody-${Date.now()}@e2e.test`;
    await page.goto('/forgot-password');
    await page.getByLabel('Email').fill(nobody);
    await page.getByRole('button', { name: 'Send reset link' }).click();
    await expect(page.getByText(`If there is an account at ${nobody}`, { exact: false })).toBeVisible();
    // Rita, asked for after, is emailed: so the unknown address had its chance to be sent first.
    await page.getByRole('button', { name: 'Use a different address' }).click();
    await page.getByLabel('Email').fill(RITA.email);
    await page.getByRole('button', { name: 'Send reset link' }).click();
    await mailTo(RITA.email, /^Reset your /);
    const res = await fetch(`${MAILPIT}/api/v1/search?query=${encodeURIComponent(`to:"${nobody}"`)}`);
    expect((await res.json()).messages ?? []).toEqual([]);
  });

  test('without mail, the administrators are asked in the app, and it opens Members', async ({ page, as }) => {
    sql(`DELETE FROM org_configs WHERE key = 'smtp'`);
    await page.goto('/forgot-password');
    await page.getByLabel('Email').fill(RITA.email);
    await page.getByRole('button', { name: 'Send reset link' }).click();
    await expect(page.getByRole('heading', { name: 'Check your email' })).toBeVisible();
    await expect(page.getByText('If your organization has no email set up, its administrators have already been told you asked.')).toBeVisible();

    const olivia = await as('owner');
    await olivia.page.goto('/chat');
    await olivia.page.getByRole('button', { name: /^Notifications/ }).click();
    const note = olivia.page.getByRole('button', { name: new RegExp(`${RITA.name} forgot their password`) });
    await expect(note).toBeVisible({ timeout: 15_000 });
    await expect(note).toContainText('Mail is not set up, so no reset link could be emailed.');
    await note.click();
    await expect(olivia.page).toHaveURL(/\/organization\?tab=members/);
    await expect(olivia.page.getByRole('tab', { name: 'Members', selected: true })).toBeVisible();
  });
});
