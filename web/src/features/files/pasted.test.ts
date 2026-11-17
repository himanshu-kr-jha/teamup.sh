import { describe, expect, it } from 'vitest';
import { filesFrom, pastedAttachments, screenshotName } from './pasted';

const at = new Date(2026, 10, 17, 16, 57, 3);

function clipboard(files: File[], text = ''): DataTransfer {
  const items = [
    ...(text ? [{ kind: 'string', type: 'text/plain', getAsFile: () => null }] : []),
    ...files.map((f) => ({ kind: 'file', type: f.type, getAsFile: () => f })),
  ];
  return { items, files, getData: (t: string) => (t === 'text/plain' ? text : '') } as unknown as DataTransfer;
}

describe('filesFrom', () => {
  it('names a pasted screenshot for when it was pasted', () => {
    const [file] = filesFrom(clipboard([new File(['x'], 'image.png', { type: 'image/png' })]), at);
    expect(file.name).toBe('Screenshot 2026-11-17 16.57.03.png');
    expect(file.type).toBe('image/png');
  });

  it('numbers several pasted at once, and keeps a real name', () => {
    const files = filesFrom(
      clipboard([
        new File(['a'], 'image.png', { type: 'image/png' }),
        new File(['b'], 'image.jpeg', { type: 'image/jpeg' }),
        new File(['c'], 'site-plan.pdf', { type: 'application/pdf' }),
        new File(['d'], 'roof.jpg', { type: 'image/jpeg' }),
      ]),
      at,
    );
    expect(files.map((f) => f.name)).toEqual([
      'Screenshot 2026-11-17 16.57.03 (1).png',
      'Screenshot 2026-11-17 16.57.03 (2).jpg',
      'site-plan.pdf',
      'roof.jpg',
    ]);
  });

  it('has nothing for plain text', () => {
    expect(filesFrom(clipboard([], 'hello'), at)).toEqual([]);
    expect(filesFrom(null, at)).toEqual([]);
  });

  it('falls back to files when items has none', () => {
    const f = new File(['x'], 'notes.txt', { type: 'text/plain' });
    expect(filesFrom({ items: [], files: [f] } as unknown as DataTransfer, at)).toEqual([f]);
  });
});

describe('screenshotName', () => {
  it('uses the picture type for the extension', () => {
    expect(screenshotName(at, 'image/svg+xml')).toBe('Screenshot 2026-11-17 16.57.03.svg');
  });
});

describe('pastedAttachments', () => {
  it('attaches a screenshot', () => {
    expect(pastedAttachments(clipboard([new File(['x'], 'image.png', { type: 'image/png' })]), at)).toHaveLength(1);
  });

  it('pastes cells from a spreadsheet as text, though a picture of them comes too', () => {
    expect(pastedAttachments(clipboard([new File(['x'], 'image.png', { type: 'image/png' })], 'Q3\t120'), at)).toEqual([]);
  });
});
