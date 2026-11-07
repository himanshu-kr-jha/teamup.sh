import { mkdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import type { Page } from '@playwright/test';
import type { PersonKey } from './support/people';
import { expect, openTasks, test } from './support/fixtures';

/**
 * Every dashboard tab, per role, at desktop and phone width, light and dark.
 * Runs after dashboard.spec.ts (file order), so History has a saved scrum.
 * Each shot also has to pass the cheap layout checks below; the images are for
 * the human who reads them.
 */
const OUT = process.env.E2E_SHOTS_DIR ?? path.resolve('e2e-shots');
mkdirSync(OUT, { recursive: true });

const TABS: Record<'self' | 'manager', [string, string][]> = {
  self: [
    ['overview', 'Overview'],
    ['board', 'Board'],
  ],
  manager: [
    ['overview', 'Overview'],
    ['person', 'By person'],
    ['scrum', 'Scrum'],
    ['history', 'History'],
    ['contribution', 'Contribution'],
    ['board', 'Board'],
  ],
};

const ROLES: { who: PersonKey; label: string; tabs: [string, string][] }[] = [
  { who: 'alice', label: 'employee-alice', tabs: TABS.self },
  { who: 'mia', label: 'manager-mia', tabs: TABS.manager },
  { who: 'owner', label: 'owner', tabs: TABS.manager },
];
const VIEWPORTS = [
  { name: '1280', width: 1280, height: 800 },
  { name: '375', width: 375, height: 812 },
] as const;
const THEMES = ['light', 'dark'] as const;

/** The session saved this theme, and the app restores it on every load. */
async function settleTheme(page: Page, theme: 'light' | 'dark') {
  await expect(page.locator('html')).toHaveClass(theme === 'dark' ? /dark/ : /^(?!.*\bdark\b)/);
  // Colours can still be fading when a page has just been drawn; a shot taken then is a blend.
  await page.evaluate(() => Promise.all(document.getAnimations().map((a) => a.finished.catch(() => undefined))));
}

/**
 * Anything wider than the screen is a problem. Text cut off by its own box with an
 * ellipsis is a note: it is often deliberate, and is for a person to judge.
 */
async function layoutProblems(page: Page): Promise<{ problems: string[]; notes: string[] }> {
  return page.evaluate(() => {
    const problems: string[] = [];
    const notes: string[] = [];
    const root = document.documentElement;
    if (root.scrollWidth > root.clientWidth + 1) {
      problems.push(`page scrolls sideways: ${root.scrollWidth}px of content in a ${root.clientWidth}px window`);
    }
    for (const el of Array.from(document.querySelectorAll<HTMLElement>('main *, [role="tabpanel"] *'))) {
      const style = getComputedStyle(el);
      // sr-only text is clipped to 1px on purpose.
      if (style.visibility === 'hidden' || style.display === 'none' || !el.textContent?.trim() || el.clientWidth <= 1) continue;
      if (el.children.length === 0 && el.scrollWidth > el.clientWidth + 1 && /hidden|clip/.test(style.overflowX)) {
        notes.push(`clipped text "${el.textContent.trim().slice(0, 50)}" (${el.scrollWidth}px in ${el.clientWidth}px)`);
      }
      const rect = el.getBoundingClientRect();
      if (rect.width > 0 && rect.right > root.clientWidth + 1 && !el.closest('[class*="overflow-x-auto"]')) {
        problems.push(`<${el.tagName.toLowerCase()}> "${el.textContent.trim().slice(0, 40)}" runs off the right edge (${Math.round(rect.right)}px)`);
      }
    }
    return { problems: [...new Set(problems)], notes: [...new Set(notes)] };
  });
}

for (const role of ROLES) {
  for (const viewport of VIEWPORTS) {
    for (const theme of THEMES) {
      test(`${role.label} at ${viewport.name} wide, ${theme}`, async ({ as }, info) => {
        const { page } = await as(role.who, { width: viewport.width, height: viewport.height, theme });
        const found: string[] = [];
        const notes: string[] = [];
        for (const [id, label] of role.tabs) {
          await openTasks(page, `?tab=${id}`);
          await settleTheme(page, theme);
          await expect(page.getByRole('tab', { name: label })).toHaveAttribute('aria-selected', 'true');
          // The view has finished loading when its spinner is gone.
          await expect(page.getByRole('status')).toHaveCount(0);
          await page.screenshot({ path: path.join(OUT, `${role.label}__${id}__${viewport.name}__${theme}.png`) });
          const layout = await layoutProblems(page);
          found.push(...layout.problems.map((p) => `${id}: ${p}`));
          notes.push(...layout.notes.map((p) => `${id}: ${p}`));
        }
        writeFileSync(
          path.join(OUT, `layout__${role.label}__${viewport.name}__${theme}.txt`),
          `PROBLEMS\n${found.join('\n') || 'none'}\nNOTES (clipped text)\n${notes.join('\n') || 'none'}\n`
        );
        await info.attach('layout problems', { body: found.join('\n') || 'none', contentType: 'text/plain' });
        expect(found, 'layout problems').toEqual([]);
      });
    }
  }
}

for (const viewport of VIEWPORTS) {
  for (const theme of THEMES) {
    test(`manager dialogs and saved scrum at ${viewport.name} wide, ${theme}`, async ({ as }) => {
      const { page } = await as('mia', { width: viewport.width, height: viewport.height, theme });
      const shot = (name: string) => page.screenshot({ path: path.join(OUT, `manager-mia__${name}__${viewport.name}__${theme}.png`) });

      await openTasks(page, '?tab=scrum');
      await settleTheme(page, theme);
      await page.getByRole('button', { name: 'Complete scrum' }).first().click();
      await expect(page.getByRole('dialog')).toBeVisible();
      await shot('complete-scrum-dialog');
      await page.keyboard.press('Escape');

      await openTasks(page, '?tab=history');
      await settleTheme(page, theme);
      await page.getByRole('link', { name: /held by/ }).first().click();
      await expect(page.getByText('Saved, read-only')).toBeVisible();
      await shot('saved-scrum');

      await openTasks(page, '?tab=contribution');
      await settleTheme(page, theme);
      await page.getByRole('button', { name: 'Alice Archer' }).click();
      await expect(page.getByRole('dialog')).toBeVisible();
      await expect(page.getByRole('status')).toHaveCount(0);
      await shot('contribution-detail');
    });
  }
}
