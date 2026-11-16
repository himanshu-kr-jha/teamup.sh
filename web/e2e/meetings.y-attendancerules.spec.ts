import { readFileSync } from 'node:fs';
import { expect, test } from './support/fixtures';
import { apiAs, idOf, leaveCall, sql, waitForLog } from './support/meetings';
import { PEOPLE } from './support/people';

// Attendance rules, the owner's decisions of 16 November 2026: a stand-up may
// require cameras, whoever works in the field checks in with a photo and their
// location, a reviewer decides what is in review, and attendance exports.

const NAME = (k: keyof typeof PEOPLE) => PEOPLE[k].name;

/** A minute of the day, this many minutes from now, in this machine's zone. */
function timeOfDay(offsetMinutes: number): string {
  const d = new Date(Date.now() + offsetMinutes * 60_000);
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
}

function today(): string {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

/** A stand-up for everybody that started ten minutes ago, made days ago so today counts. */
async function standUp(name: string, cameraRequired: boolean): Promise<string> {
  const owner = await apiAs('owner');
  const made = await owner.post('/standing-meetings', {
    name,
    timeOfDay: timeOfDay(-10),
    timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    weekdays: [1, 2, 3, 4, 5, 6, 7],
    durationMinutes: 60,
    cameraRequired,
  });
  await owner.dispose();
  const id = made.standingMeeting.id as string;
  sql(`UPDATE standing_meetings SET created_at = now() - interval '3 days' WHERE id = '${id}'`);
  sql(`UPDATE standing_meeting_schedules SET effective_at = now() - interval '3 days' WHERE standing_meeting_id = '${id}'`);
  sql(`UPDATE users SET created_at = now() - interval '10 days' WHERE org_id = (SELECT org_id FROM users WHERE email = '${PEOPLE.owner.email}')`);
  return id;
}

const row = (page: import('@playwright/test').Page, name: string) =>
  page.getByRole('table').first().getByRole('row', { name: new RegExp(name) });

test.describe('attendance rules', () => {
  test.afterAll(() => {
    sql(`DELETE FROM field_checkins; DELETE FROM attendance_reviews; DELETE FROM org_configs WHERE key = 'field_work'`);
  });

  test('a camera that never came on is in review until a reviewer decides, and attendance exports', async ({ as }) => {
    test.setTimeout(180_000);
    const name = `Camera scrum ${Date.now()}`;

    // The switch is in the stand-up's own settings.
    const olivia = await as('owner');
    await olivia.page.goto('/calls');
    await olivia.page.getByRole('button', { name: 'New', exact: true }).click();
    const dialog = olivia.page.getByRole('dialog');
    await expect(dialog.getByRole('switch', { name: /Camera must be on/ })).toHaveAttribute('aria-checked', 'false');
    await dialog.getByRole('button', { name: 'Cancel' }).click();
    const id = await standUp(name, true);

    // Alice joins and never turns her camera on; Pat turns his on.
    const alice = await as('alice');
    const pat = await as('pat');
    await pat.context.grantPermissions(['camera', 'microphone']);
    for (const s of [alice, pat]) {
      await s.page.goto('/calls');
      await s.page.getByRole('button', { name: `Join ${name}` }).click();
      await expect(s.page.getByRole('button', { name: 'Leave the call' })).toBeVisible({ timeout: 30_000 });
    }
    await pat.page.getByRole('button', { name: 'Turn your camera on' }).click();
    const owner = await apiAs('owner');
    const callId = sql(`SELECT id FROM calls WHERE standing_meeting_id = '${id}'`);
    await waitForLog(owner, callId, "Pat's camera on", (l) => l.events.some((e) => e.type === 'camera_on' && e.userId === idOf('pat')));
    await leaveCall(alice.page);
    await leaveCall(pat.page);

    await olivia.page.goto(`/calls/standing/${id}/attendance?from=${today()}&to=${today()}`);
    const day = olivia.page.getByRole('region', { name: `Attendance on ${today()}` });
    await expect(day.getByText('Cameras required')).toBeVisible();
    await expect(row(olivia.page, NAME('alice'))).toContainText('In review');
    await expect(row(olivia.page, NAME('alice'))).toContainText('Camera never turned on');
    // Pat came ten minutes after the start, inside the window: present, not late.
    await expect(row(olivia.page, NAME('pat'))).toContainText('Present');
    await expect(day.getByText(/^Counts /)).toBeVisible();
    await expect(day).toContainText('in review');

    // The owner reviews: present, with a reason.
    await row(olivia.page, NAME('alice')).getByRole('button', { name: `Decide ${NAME('alice')} on ${today()}` }).click();
    const review = olivia.page.getByRole('dialog');
    await expect(review).toContainText('they joined but never turned their camera on');
    await review.getByRole('radio', { name: 'Present' }).click();
    await review.getByLabel(/Note/).fill('Her camera was broken; she spoke throughout');
    await review.getByRole('button', { name: 'Save', exact: true }).click();
    await expect(review).toHaveCount(0);
    await expect(row(olivia.page, NAME('alice'))).toContainText('Present');
    await expect(row(olivia.page, NAME('alice'))).toContainText(`Changed by ${NAME('owner')}`);
    await expect(row(olivia.page, NAME('alice'))).toContainText('was in review: Her camera was broken');

    // Any other day can be changed too: Pat, present, set late by 7 minutes.
    await row(olivia.page, NAME('pat')).getByRole('button', { name: `Change ${NAME('pat')} on ${today()}` }).click();
    const change = olivia.page.getByRole('dialog');
    await change.getByRole('radio', { name: 'Late' }).click();
    await change.getByLabel('Minutes late').fill('7');
    await change.getByRole('button', { name: 'Save', exact: true }).click();
    await expect(change).toHaveCount(0);
    await expect(row(olivia.page, NAME('pat'))).toContainText('Late');
    await expect(row(olivia.page, NAME('pat'))).toContainText('was present');

    // Somebody given only See attendance reads it, and is offered nothing to change.
    const owner2 = await apiAs('owner');
    const role = await owner2.post('/roles', { name: `Viewer ${Date.now()}`, permissions: ['attendance.view'] });
    await owner2.post(`/users/${idOf('mia')}/roles`, { roleId: role.id });
    try {
      const mia = await as('mia');
      await mia.page.goto(`/calls/standing/${id}/attendance?from=${today()}&to=${today()}`);
      await expect(mia.page.getByRole('region', { name: `Attendance on ${today()}` })).toBeVisible();
      await expect(mia.page.getByRole('button', { name: /^(Decide|Change) / })).toHaveCount(0);
    } finally {
      await owner2.call('delete', `/users/${idOf('mia')}/roles/${role.id}`);
      await owner2.call('delete', `/roles/${role.id}`);
      await owner2.dispose();
    }

    // The export: people down the side, the day across, totals at the end.
    const csv = olivia.page.waitForEvent('download');
    await olivia.page.getByRole('link', { name: 'CSV' }).click();
    const csvFile = await csv;
    expect(csvFile.suggestedFilename()).toBe(`${name} attendance ${today()}.csv`);
    const text = readFileSync(await csvFile.path(), 'utf8').replace(/^\uFEFF/, '');
    expect(text.split('\n')[0].trim()).toBe(`Name,${today()},Present,Absent,Leave,Holiday,Field work,In review,Unknown`);
    expect(text).toContain(`${NAME('alice')},Present,1,0,0,0,0,0,0`);
    expect(text).toContain(`${NAME('pat')},Late 7 min,1,0,0,0,0,0,0`);
    const xlsx = olivia.page.waitForEvent('download');
    await olivia.page.getByRole('link', { name: 'Excel' }).click();
    const xlsxFile = await xlsx;
    expect(xlsxFile.suggestedFilename()).toBe(`${name} attendance ${today()}.xlsx`);
    expect(readFileSync(await xlsxFile.path()).subarray(0, 2).toString()).toBe('PK');

    await owner.call('delete', `/standing-meetings/${id}`);
    await owner.dispose();
  });

  test('somebody in the field checks in with a photo and their location, and is in the field, not absent', async ({ as }) => {
    test.setTimeout(120_000);
    const name = `Field scrum ${Date.now()}`;
    const id = await standUp(name, false);

    // Off until the organization turns it on, which is the owner's switch.
    const ann = await as('ann');
    await ann.page.goto('/calls');
    await expect(ann.page.getByRole('heading', { name: 'Standing meetings' })).toBeVisible();
    await expect(ann.page.getByTestId('field-work-bar')).toHaveCount(0);
    const olivia = await as('owner');
    await olivia.page.goto(`/calls/standing/${id}/attendance?from=${today()}&to=${today()}`);
    await olivia.page.getByRole('switch', { name: /Field work check-ins/ }).click();
    await expect(olivia.page.getByRole('switch', { name: /Field work check-ins/ })).toHaveAttribute('aria-checked', 'true');

    // Ann, at a site: the camera and where she is.
    await ann.context.grantPermissions(['camera', 'geolocation']);
    await ann.context.setGeolocation({ latitude: 23.3441, longitude: 85.3096, accuracy: 12 });
    await ann.page.reload();
    const bar = ann.page.getByTestId('field-work-bar');
    await bar.getByRole('button', { name: 'Check in from the field' }).click();
    const dialog = ann.page.getByRole('dialog');
    await expect(dialog.getByText('Location found, within about 12 m.')).toBeVisible({ timeout: 20_000 });
    await expect(dialog.getByRole('button', { name: 'Send check-in' })).toBeDisabled();
    await dialog.getByRole('button', { name: 'Take photo' }).click();
    await expect(dialog.getByRole('img', { name: 'The photo you took' })).toBeVisible();
    await dialog.getByLabel(/Note/).fill('Survey at the Ranchi site, block C');
    await dialog.getByRole('button', { name: 'Send check-in' }).click();
    await expect(dialog).toHaveCount(0);
    await expect(bar).toContainText('In the field today.');
    await expect(bar).toContainText('Survey at the Ranchi site, block C');

    // The owner reads it as field work, with the photo and the place.
    await olivia.page.reload();
    const annRow = row(olivia.page, NAME('ann'));
    await expect(annRow).toContainText('Field work');
    await expect(annRow.getByRole('link', { name: /on the map/ })).toHaveAttribute('href', /mlat=23\.344/);
    await annRow.getByRole('button', { name: `Show ${NAME('ann')}'s field work photo` }).click();
    const photo = olivia.page.getByRole('dialog').getByRole('img', { name: `Field work photo from ${NAME('ann')}` });
    await expect(photo).toBeVisible();
    await expect.poll(() => photo.evaluate((img: HTMLImageElement) => img.naturalWidth)).toBeGreaterThan(0);

    // In one place: Ann's own check-ins, and everybody's for the owner.
    await ann.page.goto('/field-work');
    await expect(ann.page.getByText('Your check-ins from the field', { exact: false })).toBeVisible();
    await expect(ann.page.getByTestId('field-checkin')).toHaveCount(1);
    await expect(ann.page.getByTestId('field-checkin')).toContainText('Survey at the Ranchi site, block C');
    await expect(ann.page.getByTestId('field-checkin')).toContainText('23.34410° N, 85.30960° E');
    await olivia.page.goto('/field-work');
    await expect(olivia.page.getByText('Everybody’s check-ins from the field', { exact: false })).toBeVisible();
    await expect(olivia.page.getByTestId('field-checkin').filter({ hasText: NAME('ann') })).toHaveCount(1);
    const samSession = await as('sam');
    await samSession.page.goto('/field-work');
    await expect(samSession.page.getByText('No check-ins in these dates')).toBeVisible();

    // A colleague cannot open the photo.
    const sam = await apiAs('sam');
    const checkin = sql(`SELECT id FROM field_checkins WHERE user_id = '${idOf('ann')}' ORDER BY taken_at DESC LIMIT 1`);
    expect(await sam.status('get', `/field-work/checkins/${checkin}/photo`)).toBe(404);
    await sam.dispose();

    const owner = await apiAs('owner');
    await owner.call('delete', `/standing-meetings/${id}`);
    await owner.dispose();
  });
});
