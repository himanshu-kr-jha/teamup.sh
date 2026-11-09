import { mkdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import type { Page } from '@playwright/test';
import { PEOPLE } from './support/people';
import { expect, test, type Session } from './support/fixtures';
import { apiAs, at, createCall, dayParam, iso, idOf, joinCall, leaveCall, waitForLog } from './support/meetings';

/**
 * Every screen this feature added, in each role, at a computer and a phone width,
 * light and dark. Named so it runs after meetings.spec.ts (which leaves a standing
 * meeting, attendance and Added to work behind) and before screenshots.spec.ts.
 * Each shot also passes the cheap layout checks; the images are for the human who reads them.
 */
const OUT = process.env.E2E_SHOTS_DIR ?? path.resolve('e2e-shots');
mkdirSync(OUT, { recursive: true });

const VIEWPORTS = [
  { name: '1280', width: 1280, height: 800 },
  { name: '375', width: 375, height: 812 },
] as const;
const THEMES = ['light', 'dark'] as const;

async function layoutProblems(page: Page): Promise<{ problems: string[]; notes: string[] }> {
  return page.evaluate(() => {
    const problems: string[] = [];
    const notes: string[] = [];
    const root = document.documentElement;
    if (root.scrollWidth > root.clientWidth + 1) {
      problems.push(`page scrolls sideways: ${root.scrollWidth}px of content in a ${root.clientWidth}px window`);
    }
    for (const el of Array.from(document.querySelectorAll<HTMLElement>('main *, [role="dialog"] *'))) {
      const style = getComputedStyle(el);
      if (style.visibility === 'hidden' || style.display === 'none' || !el.textContent?.trim() || el.clientWidth <= 1) continue;
      if (el.children.length === 0 && el.scrollWidth > el.clientWidth + 1 && /hidden|clip/.test(style.overflowX)) {
        notes.push(`clipped text "${el.textContent.trim().slice(0, 50)}" (${el.scrollWidth}px in ${el.clientWidth}px)`);
      }
      const rect = el.getBoundingClientRect();
      if (rect.width > 0 && rect.right > root.clientWidth + 1 && !el.closest('[class*="overflow-x-auto"], [class*="overflow-y-auto"], [class*="overflow-hidden"]')) {
        problems.push(`<${el.tagName.toLowerCase()}> "${el.textContent.trim().slice(0, 40)}" runs off the right edge (${Math.round(rect.right)}px)`);
      }
    }
    return { problems: [...new Set(problems)], notes: [...new Set(notes)] };
  });
}

let seeded = false;
async function seedCalendarAndTasks() {
  if (seeded) return;
  seeded = true;
  const owner = await apiAs('owner');
  const alice = await apiAs('alice');
  const items = (await alice.get(`/calendar?from=${iso(at(0, 0))}&to=${iso(at(1, 0))}`)).items as any[];
  if (!items.some((i) => i.title === 'Deep work')) {
    await alice.post('/calendar/blocks', { kind: 'focus', title: 'Deep work', startsAt: iso(at(0, 9)), endsAt: iso(at(0, 11)) });
    await alice.post('/calendar/blocks', { kind: 'open', title: '', startsAt: iso(at(0, 13)), endsAt: iso(at(0, 17)) });
    for (const [title, s, e] of [
      ['Design review', [14, 0], [15, 0]],
      ['Budget sync', [14, 0], [14, 45]],
      ['Retro', [14, 30], [15, 30]],
    ] as const) {
      await owner.post('/meetings', { title, startsAt: iso(at(0, s[0], s[1])), endsAt: iso(at(0, e[0], e[1])), inviteeIds: [idOf('alice'), idOf('pat')] });
    }
  }
  if ((await owner.get('/standing-meetings')).standingMeetings.length === 0) {
    await owner.post('/standing-meetings', { name: 'Daily scrum', timeOfDay: '09:30', timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone, weekdays: [1, 2, 3, 4, 5, 6, 7] });
  }
  const tasks = (await owner.get('/tasks')) as any;
  const all: any[] = tasks.tasks ?? tasks.data ?? tasks;
  if (!all.some((t) => t.title === 'Shots: plan owned by Alice')) {
    const t = await owner.post('/tasks', { title: 'Shots: plan owned by Alice', assigneeId: idOf('alice'), status: 'in_progress' });
    await owner.post(`/tasks/${t.id}/watchers`, { userId: idOf('ann') });
  }
  await Promise.all([owner.dispose(), alice.dispose()]);
}

for (const viewport of VIEWPORTS) {
  for (const theme of THEMES) {
    test(`meeting screens at ${viewport.name} wide, ${theme}`, async ({ as }, info) => {
      test.setTimeout(300_000);
      await seedCalendarAndTasks();
      const found: string[] = [];
      const notes: string[] = [];
      const open = (who: keyof typeof PEOPLE) => as(who, { width: viewport.width, height: viewport.height, theme });
      const shoot = async (s: Session, label: string, name: string) => {
        const page = s.page;
        await expect(page.locator('html')).toHaveClass(theme === 'dark' ? /dark/ : /^(?!.*\bdark\b)/);
        // Colours may still be fading, but a spinner or a speaking ring animates for ever, so do not wait on those.
        await page.evaluate(() =>
          Promise.race([
            Promise.all(document.getAnimations().map((a) => a.finished.catch(() => undefined))),
            new Promise((resolve) => setTimeout(resolve, 800)),
          ])
        );
        await page.waitForTimeout(500);
        await page.screenshot({ path: path.join(OUT, `${label}__${name}__${viewport.name}__${theme}.png`) });
        // A page with nothing on it passes every check below. Seen once, and never caught
        // in the act, so if it comes back this says where the page had got to.
        const drawn = await page.evaluate(() => ({ text: document.body.innerText.trim().length, root: document.getElementById('root')?.childElementCount ?? -1, state: document.readyState, url: location.href }));
        if (drawn.text === 0) found.push(`${label} ${name}: blank page ${JSON.stringify(drawn)}`);
        const layout = await layoutProblems(page);
        found.push(...layout.problems.map((p) => `${label} ${name}: ${p}`));
        notes.push(...layout.notes.map((p) => `${label} ${name}: ${p}`));
      };
      const idle = (page: Page) => expect(page.getByRole('status')).toHaveCount(0);

      const owner = await open('owner');
      const mia = await open('mia');
      const alice = await open('alice');
      const ann = await open('ann');
      const sam = await open('sam');
      const pat = await open('pat');
      const ownerApi = await apiAs('owner');
      // A room somebody has been in, so there is a day with a log.
      const rooms = (await ownerApi.get('/standing-meetings')).standingMeetings as any[];
      let standing = rooms[0];
      for (const room of rooms) {
        const days = (await ownerApi.get(`/standing-meetings/${room.id}/attendance`)).days as any[];
        if (days.some((d) => d.callId)) {
          standing = room;
          break;
        }
      }

      // Calls with the standing room pinned, and the admin's dialog.
      await owner.page.goto('/calls');
      await expect(owner.page.getByRole('region', { name: 'Standing meetings' }).getByRole('heading', { name: standing.name })).toBeVisible();
      await shoot(owner, 'admin', 'calls-standing');
      await owner.page.getByRole('button', { name: 'New', exact: true }).click();
      await expect(owner.page.getByRole('dialog')).toBeVisible();
      await shoot(owner, 'admin', 'standing-dialog');
      await owner.page.keyboard.press('Escape');
      await owner.page.getByRole('button', { name: `Archive ${standing.name}` }).click();
      await shoot(owner, 'admin', 'standing-archive-confirm');
      await owner.page.keyboard.press('Escape');
      await alice.page.goto('/calls');
      await expect(alice.page.getByRole('region', { name: 'Standing meetings' }).getByRole('heading', { name: standing.name })).toBeVisible();
      await shoot(alice, 'employee', 'calls-standing');

      // Attendance by day, as each role reads it.
      for (const [who, label] of [[owner, 'admin'], [mia, 'manager'], [alice, 'employee']] as const) {
        await who.page.goto(`/calls/standing/${standing.id}/attendance`);
        await expect(who.page.getByRole('heading', { level: 1 })).toBeVisible();
        await idle(who.page);
        await shoot(who, label, 'attendance');
      }
      await owner.page.goto(`/calls/standing/${standing.id}/attendance`);
      await expect(owner.page.getByRole('heading', { level: 1 })).toBeVisible();
      await idle(owner.page);
      const logButton = (s: Session) => s.page.getByRole('button', { name: /Show the call log/ }).first();
      if (await logButton(owner).count()) {
        await logButton(owner).click();
        await expect(owner.page.getByRole('dialog')).toBeVisible();
        await idle(owner.page);
        await shoot(owner, 'admin', 'attendance-call-log');
        await owner.page.keyboard.press('Escape');
        // An employee is not offered a log that would only refuse them (decision 27).
        await expect(logButton(alice)).toHaveCount(0);
      }

      // Calendar: own, somebody else's with and without titles, and the dialogs.
      const today = dayParam(at(0, 0));
      await alice.page.goto(`/calendar?date=${today}`);
      await expect(alice.page.getByRole('main').getByRole('button', { name: /Design review|Deep work/ }).first()).toBeVisible();
      await shoot(alice, 'employee', 'calendar-own');
      await sam.page.goto(`/calendar?date=${today}&person=${idOf('alice')}`);
      await expect(sam.page.getByRole('alert')).toContainText('only see your own calendar');
      await shoot(sam, 'employee-sam', 'calendar-others-refused');
      await mia.page.goto(`/calendar?date=${today}&person=${idOf('alice')}`);
      await expect(mia.page.getByRole('main').getByRole('button', { name: /Design review|Deep work/ }).first()).toBeVisible();
      await shoot(mia, 'manager', 'calendar-others-titles');
      await owner.page.goto(`/calendar?date=${today}`);
      await expect(owner.page.getByRole('main').getByRole('button', { name: /Design review|Budget sync|Retro/ }).first()).toBeVisible();
      await shoot(owner, 'admin', 'calendar-own');
      await owner.page.getByRole('main').getByRole('button', { name: /Design review|Budget sync|Retro/ }).first().click();
      await expect(owner.page.getByRole('dialog')).toBeVisible();
      await shoot(owner, 'admin', 'calendar-event-dialog');
      await owner.page.keyboard.press('Escape');
      const more = alice.page.getByRole('main').getByRole('button', { name: /more at this time/ }).first();
      if (await more.count()) {
        await more.click();
        await expect(alice.page.getByRole('dialog')).toBeVisible();
        await shoot(alice, 'employee', 'calendar-overflow-list');
        await alice.page.keyboard.press('Escape');
      }
      await alice.page.getByRole('button', { name: 'Block time' }).click();
      await expect(alice.page.getByRole('dialog')).toBeVisible();
      await shoot(alice, 'employee', 'calendar-block-dialog');
      await alice.page.keyboard.press('Escape');
      await mia.page.goto(`/calendar?date=${today}`);
      await mia.page.getByRole('button', { name: 'Schedule', exact: true }).click();
      const dialog = mia.page.getByRole('dialog');
      await dialog.getByLabel('Name', { exact: true }).fill('Check in');
      await dialog.getByLabel('Starts', { exact: true }).fill(`${today}T14:00`);
      await dialog.getByLabel('Duration', { exact: true }).click();
      await mia.page.getByRole('option', { name: '1 hour', exact: true }).click();
      await dialog.getByLabel('People', { exact: true }).fill('Alice');
      await dialog.getByRole('button', { name: PEOPLE.alice.name }).click();
      await expect(dialog.getByRole('status').filter({ hasText: 'You can still go ahead' })).toBeVisible();
      await shoot(mia, 'manager', 'calendar-schedule-conflict');
      await mia.page.keyboard.press('Escape');

      // A meeting days away, from a link: it asks, and starts nothing.
      const later = await ownerApi.post('/meetings', { title: `Shots lobby ${viewport.name} ${theme}`, startsAt: iso(at(6, 10)), endsAt: iso(at(6, 11)), inviteeIds: [idOf('alice')] });
      await alice.page.goto(`/calls/${later.meeting.id}`);
      await expect(alice.page.getByRole('button', { name: 'Join now' })).toBeVisible();
      await shoot(alice, 'employee', 'meeting-lobby');

      // A private call: the door, waiting, the prompt, the stage and the log.
      const call = await createCall(ownerApi, `Shots call ${viewport.name} ${theme}`, [idOf('pat')]);
      await joinCall(owner.page, call.id);
      await joinCall(pat.page, call.id);
      await waitForLog(ownerApi, call.id, 'both connected', (l) => l.sessions.filter((s) => !s.leftAt).length === 2);
      await expect(owner.page.getByText(/In a call \u00b7 2/)).toBeVisible();
      await expect(pat.page.getByText(/In a call \u00b7 2/)).toBeVisible();
      await shoot(owner, 'admin', 'call-two');
      await shoot(pat, 'member', 'call-two');
      await sam.page.goto(`/calls/${call.id}`);
      await expect(sam.page.getByText('This is a private call')).toBeVisible();
      await shoot(sam, 'unrelated', 'call-door');
      await sam.page.getByRole('button', { name: 'Ask to join' }).click();
      await expect(sam.page.getByText('Waiting for someone to let you in')).toBeVisible();
      await shoot(sam, 'unrelated', 'call-waiting');
      await expect(owner.page.getByRole('button', { name: `Admit ${PEOPLE.sam.name}` })).toBeVisible();
      await shoot(owner, 'admin', 'call-knock-prompt');
      await pat.page.getByRole('button', { name: `Deny ${PEOPLE.sam.name}` }).click();
      await expect(sam.page.getByText('You were not let in')).toBeVisible();
      await shoot(sam, 'unrelated', 'call-denied');
      // Somebody else gets the other-organization screen.
      await pat.page.getByRole('button', { name: 'Share your screen' }).click();
      await expect(owner.page.getByText(`${PEOPLE.pat.name} is presenting`)).toBeVisible();
      await shoot(owner, 'admin', 'call-screen-share');
      await shoot(pat, 'member', 'call-sharing');
      await owner.page.getByRole('button', { name: 'Show the call log' }).click();
      await expect(owner.page.getByRole('dialog')).toBeVisible();
      await expect(owner.page.getByRole('dialog').getByText(`${PEOPLE.sam.name} asked to join`)).toBeVisible();
      await shoot(owner, 'admin', 'call-log');
      await owner.page.keyboard.press('Escape');
      await pat.page.getByRole('button', { name: 'Stop sharing your screen' }).click();
      await leaveCall(pat.page);
      await leaveCall(owner.page);

      // Added to, for the person added and the people who manage them.
      await ann.page.goto('/tasks');
      await expect(ann.page.getByRole('heading', { name: /Added to/ })).toBeVisible();
      await idle(ann.page);
      await shoot(ann, 'employee', 'tasks-added-to');
      for (const [tab, extra] of [['overview', ''], ['person', `&person=${idOf('ann')}`], ['scrum', ''], ['contribution', ''], ['contribution', `&person=${idOf('ann')}`]] as const) {
        await mia.page.goto(`/tasks?tab=${tab}${extra}`);
        if (tab === 'contribution' && extra) await expect(mia.page.getByRole('dialog', { name: PEOPLE.ann.name })).toBeVisible();
        else await expect(mia.page.getByRole('tablist', { name: 'Task views' })).toBeVisible();
        await idle(mia.page);
        await shoot(mia, 'manager', `tasks-${tab}${extra ? '-detail' : ''}`);
      }
      await owner.page.goto('/tasks');
      await expect(owner.page.getByRole('tablist', { name: 'Task views' })).toBeVisible();
      await idle(owner.page);
      await shoot(owner, 'admin', 'tasks-overview');

      await ownerApi.dispose();
      writeFileSync(
        path.join(OUT, `layout-meetings__${viewport.name}__${theme}.txt`),
        `PROBLEMS\n${found.join('\n') || 'none'}\nNOTES (clipped text)\n${notes.join('\n') || 'none'}\n`
      );
      await info.attach('layout problems', { body: found.join('\n') || 'none', contentType: 'text/plain' });
      expect(found, 'layout problems').toEqual([]);
    });
  }
}
