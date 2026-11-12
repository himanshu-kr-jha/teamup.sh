import { describe, expect, it } from 'vitest';
import { composeShared } from './inbox';

describe('composeShared', () => {
  it('keeps text that already carries the link as it is', () => {
    expect(composeShared({ title: 'A page', text: 'Look at this https://example.com/a', url: 'https://example.com/a' })).toBe(
      'A page\nLook at this https://example.com/a'
    );
  });

  it('adds the link when the text does not have it', () => {
    expect(composeShared({ text: 'Worth a read', url: 'https://example.com/b' })).toBe('Worth a read\nhttps://example.com/b');
  });

  it('does not say the title twice', () => {
    expect(composeShared({ title: 'Notes', text: 'Notes from Monday' })).toBe('Notes from Monday');
    expect(composeShared({ title: 'https://example.com', url: 'https://example.com' })).toBe('https://example.com');
  });

  it('is empty when nothing came', () => {
    expect(composeShared({})).toBe('');
    expect(composeShared({ title: '  ', text: '\n' })).toBe('');
  });
});
