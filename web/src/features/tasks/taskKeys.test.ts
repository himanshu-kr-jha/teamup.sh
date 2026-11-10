import { describe, expect, it } from 'vitest';
import { splitTaskKeys } from '@/features/chat/MentionUI';
import { suggestKey } from '@/features/admin/ProjectsTab';
import { branchFor } from './DevelopmentCard';

describe('task keys in text', () => {
  const known = new Set(['TASK', 'PHO']);
  it('links the keys of real projects, in any case, and nothing else', () => {
    expect(splitTaskKeys('see pho-12 and UTF-8, then PHO-3.', known)).toEqual([
      { text: 'see ' },
      { text: 'pho-12', key: 'PHO-12' },
      { text: ' and UTF-8, then ' },
      { text: 'PHO-3', key: 'PHO-3' },
      { text: '.' },
    ]);
  });
  it('leaves text with no keys alone', () => {
    expect(splitTaskKeys('nothing here', known)).toEqual([{ text: 'nothing here' }]);
  });
});

describe('suggestKey', () => {
  it('takes the start of one word, or the first letters of several', () => {
    expect(suggestKey('Photon')).toBe('PHO');
    expect(suggestKey('Beta backend')).toBe('BB');
    expect(suggestKey('Mobile app (iOS)')).toBe('MAI');
    expect(suggestKey('2024 plans')).toBe('');
  });
});

describe('branchFor', () => {
  it('is the key and a few words of the title, as a branch name', () => {
    expect(branchFor('PHO-12', 'Fix sign-in on Safari, again!')).toBe('pho-12-fix-sign-in-on-safari-again');
  });
});
