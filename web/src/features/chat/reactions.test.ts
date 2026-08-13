import { describe, it, expect } from 'vitest';
import { applyReaction, hasReacted, reactionSummary, summariseReactions } from './reactions';

describe('applyReaction', () => {
  it('records a reaction on a message that had none', () => {
    expect(applyReaction({}, { emoji: '👍', userId: 'u1', action: 'add' })).toEqual({ '👍': ['u1'] });
  });

  it('tolerates a message with no reactions field at all', () => {
    expect(applyReaction(undefined, { emoji: '👍', userId: 'u1', action: 'add' })).toEqual({
      '👍': ['u1'],
    });
  });

  it('adds somebody alongside the people already there', () => {
    const before = { '👍': ['u1'] };
    expect(applyReaction(before, { emoji: '👍', userId: 'u2', action: 'add' })).toEqual({
      '👍': ['u1', 'u2'],
    });
  });

  // The optimistic update and the broadcast can both land on the same client,
  // and reacting twice must not read as two people.
  it('counts the same person once however many times it is applied', () => {
    let map = applyReaction({}, { emoji: '👍', userId: 'u1', action: 'add' });
    map = applyReaction(map, { emoji: '👍', userId: 'u1', action: 'add' });
    expect(map).toEqual({ '👍': ['u1'] });
  });

  it('takes a reaction back', () => {
    const before = { '👍': ['u1', 'u2'] };
    expect(applyReaction(before, { emoji: '👍', userId: 'u1', action: 'remove' })).toEqual({
      '👍': ['u2'],
    });
  });

  // An emoji nobody is on any more should stop taking up space under the
  // message rather than sit there as an empty pill.
  it('drops an emoji once the last person takes it back', () => {
    const before = { '👍': ['u1'], '🎉': ['u2'] };
    expect(applyReaction(before, { emoji: '👍', userId: 'u1', action: 'remove' })).toEqual({
      '🎉': ['u2'],
    });
  });

  it('shrugs off removing something that was never there', () => {
    expect(applyReaction({ '👍': ['u1'] }, { emoji: '🎉', userId: 'u9', action: 'remove' })).toEqual({
      '👍': ['u1'],
    });
  });

  // React state: the old object has to stay untouched or nothing re-renders.
  it('returns a new map instead of editing the old one', () => {
    const before = { '👍': ['u1'] };
    const after = applyReaction(before, { emoji: '👍', userId: 'u2', action: 'add' });
    expect(before).toEqual({ '👍': ['u1'] });
    expect(after).not.toBe(before);
    expect(after['👍']).not.toBe(before['👍']);
  });
});

describe('hasReacted', () => {
  it('knows whether you are already on an emoji', () => {
    expect(hasReacted({ '👍': ['u1'] }, '👍', 'u1')).toBe(true);
    expect(hasReacted({ '👍': ['u1'] }, '👍', 'u2')).toBe(false);
    expect(hasReacted({ '👍': ['u1'] }, '🎉', 'u1')).toBe(false);
    expect(hasReacted(undefined, '👍', 'u1')).toBe(false);
    expect(hasReacted({ '👍': ['u1'] }, '👍', undefined)).toBe(false);
  });
});

describe('reactionSummary', () => {
  const names: Record<string, string> = { u1: 'Ana', u2: 'Bo', u3: 'Cass', u4: 'Dev', u5: 'Eli' };
  const nameOf = (id: string) => names[id];

  it('names the person who reacted', () => {
    expect(reactionSummary(['u1'], 'me', nameOf)).toBe('Ana');
  });

  it('calls you "You", and puts you first', () => {
    expect(reactionSummary(['u1', 'me'], 'me', nameOf)).toBe('You and Ana');
  });

  it('joins a handful with an "and"', () => {
    expect(reactionSummary(['u1', 'u2', 'u3'], 'me', nameOf)).toBe('Ana, Bo and Cass');
  });

  it('stops naming people past a few and counts the rest', () => {
    expect(reactionSummary(['u1', 'u2', 'u3', 'u4', 'u5'], 'me', nameOf)).toBe(
      'Ana, Bo, Cass and 2 others'
    );
    expect(reactionSummary(['u1', 'u2', 'u3', 'u4'], 'me', nameOf)).toBe('Ana, Bo, Cass and 1 other');
  });

  // Somebody who has left, or who the availability list has not caught up
  // with, still reacted — the count must not silently lose them.
  it('does not drop a reaction from somebody it cannot name', () => {
    expect(reactionSummary(['u1', 'ghost'], 'me', nameOf)).toBe('Ana and Someone');
  });

  it('has nothing to say about nobody', () => {
    expect(reactionSummary([], 'me', nameOf)).toBe('');
  });
});

describe('summariseReactions', () => {
  it('turns the map into pills in a stable order, most-reacted first', () => {
    const map = { '🎉': ['u1'], '👍': ['u1', 'u2', 'u3'], '👀': ['u1', 'u2'] };
    expect(summariseReactions(map, 'u1').map((r) => [r.emoji, r.count, r.mine])).toEqual([
      ['👍', 3, true],
      ['👀', 2, true],
      ['🎉', 1, true],
    ]);
  });

  it('marks the ones you are not on', () => {
    const map = { '👍': ['u2'] };
    expect(summariseReactions(map, 'u1')).toEqual([
      { emoji: '👍', count: 1, mine: false, userIds: ['u2'] },
    ]);
  });

  it('breaks a tie by emoji so the row does not shuffle on every render', () => {
    const map = { '🎉': ['u1'], '👍': ['u2'] };
    const once = summariseReactions(map, 'u1').map((r) => r.emoji);
    const twice = summariseReactions({ '👍': ['u2'], '🎉': ['u1'] }, 'u1').map((r) => r.emoji);
    expect(once).toEqual(twice);
  });

  it('has nothing to show for a message nobody reacted to', () => {
    expect(summariseReactions({}, 'u1')).toEqual([]);
    expect(summariseReactions(undefined, 'u1')).toEqual([]);
  });
});
