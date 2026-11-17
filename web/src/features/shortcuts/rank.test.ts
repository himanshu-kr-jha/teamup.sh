import { describe, expect, it } from 'vitest';
import { rank } from './rank';
import { stepConversation, stepUnread } from '@/features/chat/conversationKeys';

describe('rank', () => {
  const people = [
    { name: 'Sam Mia', email: 'sam@x.test' },
    { name: 'Mia Lee', email: 'mia@x.test' },
    { name: 'Tamia Ray', email: 'tamia@x.test' },
    { name: 'Max', email: 'max@x.test' },
  ];
  const fields = (p: (typeof people)[number]) => [p.name, p.email];

  it('puts a name that starts with the term first, then a word that does, then the rest', () => {
    expect(rank(people, 'mia', fields).map((p) => p.name)).toEqual(['Mia Lee', 'Sam Mia', 'Tamia Ray']);
  });

  it('ignores case and surrounding space, and keeps everything for an empty term', () => {
    expect(rank(people, '  MAX ', fields).map((p) => p.name)).toEqual(['Max']);
    expect(rank(people, '', fields)).toBe(people);
  });

  it('matches any field, such as a task key', () => {
    const tasks = [
      { title: 'Fix login', key: 'PHO-12' },
      { title: 'Write docs', key: 'PHO-13' },
    ];
    expect(rank(tasks, 'pho-13', (t) => [t.title, t.key]).map((t) => t.title)).toEqual(['Write docs']);
  });
});

describe('moving between conversations', () => {
  const list = [{ id: 'a' }, { id: 'b', unreadCount: 2 }, { id: 'c' }, { id: 'd', unreadCount: 1 }];

  it('steps up and down, going round at the ends', () => {
    expect(stepConversation(list, 'a', 1)).toBe('b');
    expect(stepConversation(list, 'a', -1)).toBe('d');
    expect(stepConversation(list, 'd', 1)).toBe('a');
  });

  it('starts at the top or bottom with nothing open, and has nowhere to go in an empty list', () => {
    expect(stepConversation(list, null, 1)).toBe('a');
    expect(stepConversation(list, undefined, -1)).toBe('d');
    expect(stepConversation([], 'a', 1)).toBeNull();
  });

  it('finds the next unread after the open one, going round', () => {
    expect(stepUnread(list, 'a', 1)).toBe('b');
    expect(stepUnread(list, 'b', 1)).toBe('d');
    expect(stepUnread(list, 'd', 1)).toBe('b');
    expect(stepUnread(list, null, 1)).toBe('b');
    expect(stepUnread([{ id: 'a' }, { id: 'b' }], 'a', 1)).toBeNull();
  });

  it('finds the previous unread before the open one, going round', () => {
    expect(stepUnread(list, 'c', -1)).toBe('b');
    expect(stepUnread(list, 'b', -1)).toBe('d');
    expect(stepUnread(list, 'a', -1)).toBe('d');
    expect(stepUnread(list, null, -1)).toBe('d');
    expect(stepUnread([{ id: 'a' }, { id: 'b' }], 'a', -1)).toBeNull();
  });
});
