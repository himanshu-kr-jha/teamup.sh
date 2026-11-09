import { describe, expect, it } from 'vitest';
import { insertMention, mentionQuery, mentionsPerson, resolveHandle, splitMentions, suggestPeople } from './mentions';

const people = [
  { id: 'a', name: 'Alice Archer', email: 'alice@e2e.test' },
  { id: 'b', name: 'Bob Smith', email: 'bob.smith@e2e.test' },
  { id: 'c', name: 'Ann Idle', email: 'ann@e2e.test' },
];

describe('mentions', () => {
  it('names somebody by username or by name without spaces, in any case', () => {
    expect(resolveHandle('alice', people)?.id).toBe('a');
    expect(resolveHandle('AliceArcher', people)?.id).toBe('a');
    expect(resolveHandle('bob.smith', people)?.id).toBe('b');
    expect(resolveHandle('bob.smith.', people)?.id).toBe('b');
    expect(resolveHandle('nobody', people)).toBeUndefined();
  });

  it('cuts a message into text and mentions, leaving emails and strangers alone', () => {
    const parts = splitMentions('hi @alice, mail ann@e2e.test or @zed. Thanks @BobSmith.', people);
    expect(parts.map((p) => ('person' in p ? `[${p.text}]` : p.text)).join('')).toBe(
      'hi [@alice], mail ann@e2e.test or @zed. Thanks [@BobSmith].'
    );
  });

  it('finds the @-word being typed', () => {
    expect(mentionQuery('hello @al', 9)).toEqual({ query: 'al', start: 6 });
    expect(mentionQuery('@', 1)).toEqual({ query: '', start: 0 });
    expect(mentionQuery('mail ann@e2e', 12)).toBeNull();
    expect(mentionQuery('hello @al there', 15)).toBeNull();
  });

  it('suggests the closest names first', () => {
    expect(suggestPeople('a', people).map((p) => p.id)).toEqual(['a', 'c']);
    expect(suggestPeople('smi', people).map((p) => p.id)).toEqual(['b']);
    expect(suggestPeople('', people)).toHaveLength(3);
  });

  it('puts the username in place of what was typed', () => {
    expect(insertMention('hey @al ok', 4, 7, people[0])).toEqual({ text: 'hey @alice  ok', caret: 11 });
  });
});

describe('mentionsPerson', () => {
  const alice = { id: 'a', name: 'Alice Archer', email: 'alice@e2e.test' };
  it('is true for her username or her name, in any case, and a full stop after it', () => {
    expect(mentionsPerson('thanks @alice.', alice)).toBe(true);
    expect(mentionsPerson('@AliceArcher can you look', alice)).toBe(true);
  });
  it('is false for somebody else, an address, or no mention at all', () => {
    expect(mentionsPerson('@bob have a look', alice)).toBe(false);
    expect(mentionsPerson('write to alice@e2e.test', alice)).toBe(false);
    expect(mentionsPerson('alice, have a look', alice)).toBe(false);
  });
});
