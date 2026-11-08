import { expect, test } from './support/fixtures';

// Whenever TeamUp is waiting, it says so in its own colours: a loading screen
// while it starts, and a thin bar along the top while a page waits on the
// server. Nothing is shown for a wait too short to notice.

test.describe('loading', () => {
  test('a slow start shows the loading screen in the app\'s colours, then the app', async ({ as }) => {
    const olivia = await as('owner');
    // The first thing the app asks is whether it is set up; make it slow.
    await olivia.page.route('**/api/wizard/status', async (route) => {
      await new Promise((r) => setTimeout(r, 1500));
      await route.continue();
    });
    await olivia.page.goto('/tasks');
    const screen = olivia.page.getByTestId('loading-screen');
    await expect(screen).toBeVisible();
    await expect(screen).toContainText('TeamUp');
    // The bar is the organization's colour, not the browser's.
    const colour = await screen.locator('span.bg-primary').first().evaluate((el) => getComputedStyle(el).backgroundColor);
    const primary = await olivia.page.evaluate(() => {
      const probe = document.createElement('span');
      probe.className = 'bg-primary';
      document.body.appendChild(probe);
      const c = getComputedStyle(probe).backgroundColor;
      probe.remove();
      return c;
    });
    expect(colour).toBe(primary);
    await expect(screen).toBeHidden({ timeout: 15_000 });
    await expect(olivia.page.getByRole('heading', { name: 'Tasks', level: 1 })).toBeVisible();
  });

  test('a page waiting on the server shows the bar along the top, and a quick one does not', async ({ as }) => {
    const olivia = await as('owner');
    await olivia.page.goto('/chat');
    const bar = olivia.page.getByTestId('top-progress');
    await expect(bar).not.toHaveAttribute('data-busy');

    await olivia.page.route('**/api/calendar?**', async (route) => {
      await new Promise((r) => setTimeout(r, 2000));
      await route.continue();
    });
    await olivia.page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Calendar' }).click();
    await expect(bar).toHaveAttribute('data-busy', 'true');
    await expect(bar).not.toHaveAttribute('data-busy', { timeout: 15_000 });

    // A quick page, nothing.
    let flashed = false;
    await olivia.page.exposeFunction('sawBar', () => (flashed = true));
    await olivia.page.evaluate(() => {
      const el = document.querySelector('[data-testid="top-progress"]')!;
      new MutationObserver(() => el.hasAttribute('data-busy') && (window as unknown as { sawBar: () => void }).sawBar()).observe(el, { attributes: true });
    });
    await olivia.page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Chat' }).click();
    await olivia.page.waitForTimeout(800);
    expect(flashed).toBe(false);
  });
});
