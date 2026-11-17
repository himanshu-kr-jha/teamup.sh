// Files that come with a paste or a drop: a screenshot copied to the
// clipboard, "Copy image" in a browser, or files dragged in from a folder.
//
// A screenshot arrives as "image.png" whatever took it, and three of them in one
// message would be three files of the same name, so a picture with no name of its
// own is named for when it was pasted, as the screenshot tools do.

/** The files a paste or a drop carries, if any; none for plain text. */
export function filesFrom(data: DataTransfer | null, now = new Date()): File[] {
  if (!data) return [];
  const files: File[] = [];
  // items, not files: some browsers put a pasted picture only in items.
  const items = Array.from(data.items ?? []);
  for (const item of items) {
    if (item.kind !== 'file') continue;
    const file = item.getAsFile();
    if (file) files.push(file);
  }
  if (files.length === 0) files.push(...Array.from(data.files ?? []));
  return files.map((file, i) => named(file, now, i, files.length));
}

/** Whether a file's name is only what the clipboard calls every picture. */
const unnamed = (name: string) => !name || /^image(\.\w+)?$/i.test(name) || /^blob$/i.test(name);

const pad = (n: number) => String(n).padStart(2, '0');

export function screenshotName(now: Date, type: string, index = 0, count = 1): string {
  const ext = (type.split('/')[1] || 'png').replace('jpeg', 'jpg').replace(/\+.*$/, '');
  const stamp = `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())} ${pad(now.getHours())}.${pad(now.getMinutes())}.${pad(now.getSeconds())}`;
  return `Screenshot ${stamp}${count > 1 ? ` (${index + 1})` : ''}.${ext}`;
}

function named(file: File, now: Date, index: number, count: number): File {
  if (!file.type.startsWith('image/') || !unnamed(file.name)) return file;
  return new File([file], screenshotName(now, file.type, index, count), { type: file.type, lastModified: file.lastModified });
}

/**
 * What a paste into a message box attaches. Cells copied from a spreadsheet
 * come with a picture of themselves as well as their text, and are meant as
 * text, so anything with text to paste pastes the text, as it always did.
 */
export function pastedAttachments(data: DataTransfer | null, now = new Date()): File[] {
  if (!data) return [];
  if (data.getData('text/plain').trim()) return [];
  return filesFrom(data, now);
}
