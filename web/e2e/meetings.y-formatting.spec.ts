import { expect, test } from './support/fixtures';
import { readState } from './support/people';

// Asked for: what is sent from the message box was not formatted, and pasting
// lost the formatting too; and messages should be shareable with other apps.

test('a message keeps its lines and formatting, typed, pasted or copied out', async ({ as }) => {
  test.setTimeout(90_000);
  const { channelId } = readState();
  const alice = await as('alice');
  const pat = await as('pat');
  await alice.page.goto(`/chat/${channelId}`);
  await pat.page.goto(`/chat/${channelId}`);
  const box = alice.page.getByLabel('Message', { exact: true });
  await expect(box).toBeEnabled();
  const stamp = Date.now();

  // Shift+Enter starts a new line; the marks chat apps use format it.
  await box.click();
  await box.pressSequentially(`Plan ${stamp}: *ship* _friday_ ~monday~ \`make check\``);
  await box.press('Shift+Enter');
  await box.pressSequentially('- one');
  await box.press('Shift+Enter');
  await box.pressSequentially('- two');
  // The box grows with what is written, and the message is not sent yet.
  expect((await box.boundingBox())!.height).toBeGreaterThan(60);
  await box.press('Enter');
  await expect(box).toHaveValue('');

  const sent = pat.page.getByTestId('message').filter({ hasText: `Plan ${stamp}` });
  await expect(sent.locator('strong')).toHaveText('ship');
  await expect(sent.locator('em')).toHaveText('friday');
  await expect(sent.locator('s')).toHaveText('monday');
  await expect(sent.locator('code')).toHaveText('make check');
  await expect(sent.locator('li')).toHaveText(['one', 'two']);
  await sent.screenshot({ path: 'test-results/formatted-message.png' });

  // Formatted text pasted in from a document or a page keeps its formatting.
  await box.click();
  await box.evaluate((el) => {
    const data = new DataTransfer();
    data.setData('text/html', '<p>From the <b>doc</b>, see <a href="https://example.com/spec">the spec</a></p><ol><li>first</li><li>second</li></ol>');
    data.setData('text/plain', 'From the doc, see the spec\nfirst\nsecond');
    el.dispatchEvent(new ClipboardEvent('paste', { clipboardData: data, bubbles: true, cancelable: true }));
  });
  await expect(box).toHaveValue('From the *doc*, see [the spec](https://example.com/spec)\n\n1. first\n2. second');
  await box.press('Enter');
  const pasted = pat.page.getByTestId('message').filter({ hasText: 'From the doc' });
  await expect(pasted.locator('strong')).toHaveText('doc');
  await expect(pasted.getByRole('link', { name: 'the spec' })).toHaveAttribute('href', 'https://example.com/spec');
  await expect(pasted.locator('ol li')).toHaveText(['first', 'second']);

  // The formatting buttons format what is selected.
  await alice.page.getByRole('button', { name: 'Formatting' }).click();
  await box.fill('make this bold');
  await box.evaluate((el: HTMLTextAreaElement) => el.setSelectionRange(5, 9));
  await alice.page.getByRole('toolbar', { name: 'Formatting' }).getByRole('button', { name: 'Bold' }).click();
  await expect(box).toHaveValue('make *this* bold');
  // And the keys: Ctrl+I on a selection.
  await box.evaluate((el: HTMLTextAreaElement) => el.setSelectionRange(12, 16));
  await box.press('Control+i');
  await expect(box).toHaveValue('make *this* _bold_');
  await alice.page.screenshot({ path: 'test-results/formatting-composer.png' });
  // On a phone the bar and the box still fit beside the buttons.
  const phone = await alice.page.context().newPage();
  await phone.setViewportSize({ width: 390, height: 780 });
  await phone.goto(`/chat/${channelId}`);
  const phoneBox = phone.getByLabel('Message', { exact: true });
  await phoneBox.fill('A longer message on a phone, *bold* and _italic_,\nover a few lines\n- one\n- two');
  await phone.getByRole('button', { name: 'Formatting' }).click();
  await expect(phone.getByRole('toolbar', { name: 'Formatting' })).toBeVisible();
  expect(await phone.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await phone.screenshot({ path: 'test-results/formatting-phone.png' });
  await phone.close();
  await box.fill('');

  // Copied out, a message carries HTML for documents and marks for chat apps.
  await pat.page.context().grantPermissions(['clipboard-read', 'clipboard-write']);
  await sent.hover();
  await sent.locator('..').getByRole('button', { name: 'Message actions' }).click();
  await pat.page.getByRole('menuitem', { name: 'Copy text' }).click();
  await expect(pat.page.getByText('Copied, with its formatting.')).toBeVisible();
  const copied = await pat.page.evaluate(async () => {
    const [item] = await navigator.clipboard.read();
    return { html: await (await item.getType('text/html')).text(), text: await (await item.getType('text/plain')).text() };
  });
  expect(copied.html).toContain('<strong>ship</strong>');
  expect(copied.html).toContain('<ul><li>one</li><li>two</li></ul>');
  expect(copied.text).toBe(`Plan ${stamp}: *ship* _friday_ ~monday~ \`make check\`\n\n- one\n- two`);
});
